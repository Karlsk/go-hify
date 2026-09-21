package service

// executor：六类节点的执行分发（spec 06 §4.2 / FR5）。runNode 是密封 NodeConfig 的
// type switch（无断言无反射），所有错误统一带 `node <key>:` 前缀上抛；错误分类遵循
// O4 二分法——图缺陷（渲染缺失变量 / 非法表达式 / tool 未支持）包装 errs.ErrValidationFailed，
// 环境限制（scheme / SSRF / HTTP 失败）包装 workflowapi.ErrWorkflowExecutionFailed，
// 下游哨兵（模型 / KB 不存在、供应商忙）原样透传。llm 调用经 platform/llm（重试 /
// 熔断 / 超时只在那里）且自记 executions——ConversationID=nil 即 workflow 节点调用标识。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// llmConfigResolver providerapi.ModelService 的窄面（小接口惯例）：callLLM 链只用到
// ResolveLLMConfig（明文 API key 只在此调用链瞬间存在）。
type llmConfigResolver interface {
	ResolveLLMConfig(ctx context.Context, req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error)
}

// llmClientFactory platform/llm.Manager 的窄面：按供应商名 + 上游配置取 client。
type llmClientFactory interface {
	Client(key string, opts llm.UpstreamOptions) (*llm.Client, error)
}

// executionWriter logging.ExecutionStore 的窄面：llm 节点自记 executions 的写入缝。
type executionWriter interface {
	Create(ctx context.Context, e *logging.Execution) error
}

// ragRetriever ragapi 服务的窄面：knowledge_retrieval 节点检索。
type ragRetriever interface {
	Retrieve(ctx context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error)
}

// childExecFunc workflow 节点的子执行缝（spec 08 §4.3）：签名与
// workflowService.executeChild 对齐（同包函数类型——executor 不持有 service 类型，
// 避免执行器与编排层互相持有）；生产由 New 接线 executeChild，测试注入 stub。
type childExecFunc func(ctx context.Context, workflowID uint64, fields map[string]string, parent *execContext) (childResult, error)

// executor 节点执行器（无状态，execContext 承载单次执行的可变状态）。
type executor struct {
	resolveLLM   llmConfigResolver
	clients      llmClientFactory
	execs        executionWriter
	rags         ragRetriever
	blockPrivate bool          // O6：WORKFLOW_API_BLOCK_PRIVATE（RFC1918 一并拒绝）
	execChild    childExecFunc // workflow 节点执行缝（spec 08）：New 接线，测试注入 stub
}

// newExecutor 组装执行器（依赖由组合根注入；测试注入 stub）。
func newExecutor(resolveLLM llmConfigResolver, clients llmClientFactory,
	execs executionWriter, rags ragRetriever, blockPrivate bool) *executor {
	return &executor{resolveLLM: resolveLLM, clients: clients, execs: execs, rags: rags, blockPrivate: blockPrivate}
}

// runNode 按节点 config 类型分发执行，返回节点输出字符串。错误一律
// `node <key>: %w` 包装（失败定位，O4）；tool 节点执行期 fail-fast（mcp 未建）。
func (e *executor) runNode(ctx context.Context, key string, cfg workflowapi.NodeConfig, c *execContext) (string, error) {
	var out string
	var err error
	switch v := cfg.(type) {
	case *workflowapi.LLMConfig:
		out, err = e.callLLM(ctx, key, v, c)
	case *workflowapi.ConditionConfig:
		out, err = e.evalCondition(v, c)
	case *workflowapi.KnowledgeRetrievalConfig:
		out, err = e.retrieve(ctx, v, c)
	case *workflowapi.ApiCallConfig:
		out, err = e.callAPI(ctx, v, c)
	case *workflowapi.EndConfig:
		out, err = e.buildOutput(v, c)
	case *workflowapi.WorkflowNodeConfig:
		out, err = e.callWorkflow(ctx, key, v, c)
	case *workflowapi.ToolConfig:
		err = fmt.Errorf("%w: tool node %q not supported (mcp 未建，图缺陷)", errs.ErrValidationFailed, key)
	default:
		err = fmt.Errorf("unknown node config type %T", cfg)
	}
	if err != nil {
		return "", fmt.Errorf("node %s: %w", key, err)
	}
	return out, nil
}

// evalCondition condition 节点：纯内存求值（零外部调用），错误（缺失变量 / 非法
// 表达式）归图缺陷类。
func (e *executor) evalCondition(cfg *workflowapi.ConditionConfig, c *execContext) (string, error) {
	out, err := c.evalCondition(cfg.Expression)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
	}
	c.setNodeIn(map[string]string{"expression": cfg.Expression})
	return out, nil
}

// callLLM llm 节点全链：strict 渲染（先于下游调用——图缺陷不浪费供应商额度）→
// ResolveLLMConfig（明文 key 只在调用链瞬间存在）→ llm client → Generate 非流式
// （单轮、无 SSE，不经 chat）→ 自记 executions（失败也记，pre-attempt 除外）。
func (e *executor) callLLM(ctx context.Context, key string, cfg *workflowapi.LLMConfig, c *execContext) (string, error) {
	prompt, err := c.render(cfg.Prompt)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
	}
	c.setNodeIn(map[string]string{"prompt": prompt}) // 渲染成功即记：下游失败时入参仍可读
	resolved, err := e.resolveLLM.ResolveLLMConfig(ctx, providerapi.ResolveLLMConfigReq{ModelID: cfg.ModelID})
	if err != nil {
		return "", err // 下游哨兵透传（MODEL_NOT_FOUND 等）
	}
	client, err := e.clients.Client(resolved.ProviderName, llm.UpstreamOptions{
		Kind:    llm.ProviderKind(resolved.Kind),
		BaseURL: resolved.BaseURL,
		APIKey:  resolved.APIKey,
		Model:   resolved.ModelID,
	})
	if err != nil {
		return "", err // pre-attempt（busy / breaker / kind）：未打上游，不落 executions
	}
	msgs := []*schema.Message{{Role: schema.User, Content: prompt}}
	opts := &llm.CallOptions{}
	if cfg.Temperature != 0 { // 0 = 跟随模型默认（不设字段）
		t := float32(cfg.Temperature)
		opts.Temperature = &t
	}
	start := time.Now()
	reply, genErr := client.Generate(ctx, msgs, opts)
	if !isPreAttemptErr(genErr) {
		e.recordExecution(ctx, key, resolved, cfg.ModelID, prompt, start, reply, genErr)
	}
	if genErr != nil {
		return "", genErr
	}
	return reply.Content, nil
}

// recordExecution llm 节点自记 executions（chat recordExecution 同款）：ConversationID
// =nil 即 workflow 调用标识；失败也记（ErrorClass 兜底 Network）；落库失败只 WARN
// 不阻断执行。
func (e *executor) recordExecution(ctx context.Context, nodeKey string, resolved *providerapi.LLMConfig,
	modelID uint64, prompt string, start time.Time, reply *schema.Message, callErr error) {
	row := &logging.Execution{
		ModelID:    &modelID,
		ModelName:  resolved.ModelID, // 冗余快照：模型删除后记录仍可读
		Input:      map[string]any{"prompt": prompt},
		DurationMs: int32(time.Since(start).Milliseconds()),
	}
	if callErr != nil {
		cls := string(executionErrorClass(callErr))
		row.ErrorClass = &cls
	} else if reply != nil {
		row.Output = map[string]any{"content": reply.Content}
		if reply.ResponseMeta != nil {
			row.FinishReason = reply.ResponseMeta.FinishReason
			if u := reply.ResponseMeta.Usage; u != nil {
				row.PromptTokens, row.CompletionTokens = int64(u.PromptTokens), int64(u.CompletionTokens)
				row.TotalTokens = row.PromptTokens + row.CompletionTokens
			}
		}
	}
	if err := e.execs.Create(ctx, row); err != nil {
		slog.WarnContext(ctx, "workflow: record node execution failed", "node", nodeKey, "err", err)
	}
}

// executionErrorClass 错误 → executions.error_class（chat 同款：llm.Classify 优先，
// 未分类兜底 Network——能走到这里说明上游交互已发生）。
func executionErrorClass(err error) llm.Class {
	if class, ok := llm.Classify(err); ok {
		return class
	}
	return llm.ClassNetwork
}

// isPreAttemptErr 未真正发起上游调用的失败（bulkhead 抢槽 / 熔断秒拒 / 不支持的
// kind）——不落 executions（chat isPreAttemptErr 同款）。
func isPreAttemptErr(err error) bool {
	return err != nil && (errors.Is(err, llm.ErrProviderBusy) ||
		errors.Is(err, llm.ErrProviderUnavailable) ||
		errors.Is(err, llm.ErrUnsupportedKind))
}

// retrieve knowledge_retrieval 节点：query 固定取 input（O1 终形），TopK 直传
//（0 = rag service 取默认并 clamp，不在本层归一），结果格式化为编号段落文本落池。
func (e *executor) retrieve(ctx context.Context, cfg *workflowapi.KnowledgeRetrievalConfig, c *execContext) (string, error) {
	c.setNodeIn(map[string]string{"query": c.vars["input"]}) // query 固定 = input（O1）
	chunks, err := e.rags.Retrieve(ctx, ragapi.RetrieveReq{
		Query: c.vars["input"],
		TopK:  cfg.TopK,
		KBIDs: []uint64{cfg.KnowledgeBaseID},
	})
	if err != nil {
		return "", err // 下游哨兵透传（KNOWLEDGE_BASE_NOT_FOUND 等）
	}
	parts := make([]string, 0, len(chunks))
	for i, ch := range chunks {
		parts = append(parts, fmt.Sprintf("[%d] %s（来源：%s）", i+1, ch.Content, ch.DocumentName))
	}
	return strings.Join(parts, "\n"), nil
}

// callWorkflow workflow 节点（spec 08 §4.3 / FR5）：inputs 逐值 strict 渲染（缺失即
// 图缺陷 400 带 inputs.<字段> 定位，先于子调用——不浪费子图 IO）→ 透传 execChild
// 递归执行子图 → 子终稿原样返回（runNode 统一落父池 + `node %s:` 前缀包装）；失败
// 子 run 只要 run 已落也链接进 childRunIDs（轨迹完整优先）；节点入参摘要记
// workflow_id（字符串化）与渲染后 inputs 映射。
func (e *executor) callWorkflow(ctx context.Context, key string, cfg *workflowapi.WorkflowNodeConfig, c *execContext) (string, error) {
	rendered := make(map[string]string, len(cfg.Inputs))
	for k, tpl := range cfg.Inputs {
		v, err := c.render(tpl)
		if err != nil {
			return "", fmt.Errorf("%w: inputs.%s: %v", errs.ErrValidationFailed, k, err)
		}
		rendered[k] = v
	}
	inJSON, err := json.Marshal(rendered)
	if err != nil {
		inJSON = []byte("{}") // 防御兜底：map[string]string 序列化不会失败
	}
	c.setNodeIn(map[string]string{"workflow_id": strconv.FormatUint(cfg.WorkflowID, 10), "inputs": string(inJSON)})
	res, err := e.execChild(ctx, cfg.WorkflowID, rendered, c)
	if res.runID != 0 {
		c.childRunIDs = append(c.childRunIDs, res.runID) // 失败子 run 也参与链接
	}
	return res.output, err
}

// buildAPIRequest api 节点纯渲染（无网络）：url / method / headers 值 / body 全链
// {{var}} strict 渲染。渲染失败或渲染产物非法归图缺陷类。
func buildAPIRequest(cfg workflowapi.ApiCallConfig, c *execContext) (*http.Request, error) {
	renderStrict := func(tpl, what string) (string, error) {
		out, err := c.render(tpl)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %v", errs.ErrValidationFailed, what, err)
		}
		return out, nil
	}
	urlStr, err := renderStrict(cfg.URL, "url")
	if err != nil {
		return nil, err
	}
	method, err := renderStrict(cfg.Method, "method")
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if cfg.Body != "" {
		rendered, err := renderStrict(cfg.Body, "body")
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(rendered)
	}
	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request: %v", errs.ErrValidationFailed, err)
	}
	for k, v := range cfg.Headers {
		rv, err := renderStrict(v, "header "+k)
		if err != nil {
			return nil, err
		}
		req.Header.Set(k, rv)
	}
	return req, nil
}

// callAPI api 节点出站执行：渲染后复验 scheme（模板可注入 file:// 等）→ SSRF 防护
// client（建连时校验 + redirect 每跳复验，O6）→ 响应体即节点输出。scheme / SSRF /
// HTTP 失败全归环境限制类（500）。
func (e *executor) callAPI(ctx context.Context, cfg *workflowapi.ApiCallConfig, c *execContext) (string, error) {
	req, err := buildAPIRequest(*cfg, c)
	if err != nil {
		return "", err
	}
	c.setNodeIn(map[string]string{"url": req.URL.String(), "method": req.Method})
	req = req.WithContext(ctx)
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme %q not allowed", workflowapi.ErrWorkflowExecutionFailed, req.URL.Scheme)
	}
	client := outboundClient(cfg.TimeoutSec, cfg.SSLVerify, e.blockPrivate)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", workflowapi.ErrWorkflowExecutionFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB 上限：出站响应进变量池前先封顶
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", workflowapi.ErrWorkflowExecutionFailed, err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%w: http status %d", workflowapi.ErrWorkflowExecutionFailed, resp.StatusCode)
	}
	return string(body), nil
}

// buildOutput end 节点：output 模板渲染终稿；空 output = 取最后执行节点的输出
//（与「无出边 = 隐式结束」语义一致）；无步骤（end 即首节点）返回空串。
func (e *executor) buildOutput(cfg *workflowapi.EndConfig, c *execContext) (string, error) {
	if cfg.Output == "" {
		if len(c.steps) == 0 {
			return "", nil
		}
		return c.vars[c.steps[len(c.steps)-1].NodeKey], nil
	}
	c.setNodeIn(map[string]string{"output": cfg.Output}) // 模板原文（渲染结果即节点输出）
	out, err := c.render(cfg.Output)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
	}
	return out, nil
}

// route 游走出边选择（db_model 决策 #4）：按声明顺序找首条 `*cond == result` 的条件
// 边（condition 求值 "true"/"false" 与裸 var 多路分支的值统一走这条）；无命中再取
// 首条无条件边（线性节点直走，结果不参与）；两类皆无 = 图缺陷（O4 类 400，
// `node <key>:` 前缀由 execute 游走补）。
func route(edges []WorkflowEdge, result string) (string, bool, error) {
	if len(edges) == 0 {
		return "", false, nil // 无出边 = 隐式终止
	}
	for i := range edges {
		if e := &edges[i]; e.Condition != nil && *e.Condition == result {
			return e.TargetNodeKey, true, nil
		}
	}
	for i := range edges {
		if e := &edges[i]; e.Condition == nil {
			return e.TargetNodeKey, true, nil
		}
	}
	return "", false, fmt.Errorf("%w: no outgoing edge matched %q", errs.ErrValidationFailed, result)
}
