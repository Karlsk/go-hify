package service

// executor：六类节点的执行分发（spec 06 §4.2 / FR5 + spec 016 tool 节点）。runNode
// 是密封 NodeConfig 的 type switch（无断言无反射），所有错误统一带 `node <key>:` 前缀
// 上抛；错误分类遵循 O4 二分法——图缺陷（渲染缺失变量 / 非法表达式 / tool args 缺失
// 变量）包装 errs.ErrValidationFailed，环境限制（scheme / SSRF / HTTP 失败 / 工具调用
// 失败 / 装配缺位）包装 workflowapi.ErrWorkflowExecutionFailed 或 mcpapi 哨兵，下游
// 哨兵（模型 / KB / 工具不存在、供应商忙）原样透传。llm 调用经 platform/llm（重试 /
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

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/logging"
	"github.com/Karlsk/go-hify/internal/platform/reactloop"
	"github.com/Karlsk/go-hify/internal/platform/tool"
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

// toolSourceFactory mcpapi.MCPClientService 的窄面（小接口惯例，spec 016 D1；
// 签名即已交付 mcpapi 契约原形——构造无 IO，ctx 在 Source.Tools / Invoke 上）：
// tool 节点按 ToolID 解析统一工具来源（platform/tool.Source）。
type toolSourceFactory interface {
	ToolSourceFor(req mcpapi.ToolSourceReq) tool.Source
}

// childExecFunc workflow 节点的子执行缝（spec 08 §4.3）：签名与
// workflowService.executeChild 对齐（同包函数类型——executor 不持有 service 类型，
// 避免执行器与编排层互相持有）；生产由 New 接线 executeChild，测试注入 stub。
type childExecFunc func(ctx context.Context, workflowID uint64, fields map[string]string, parent *execContext) (childResult, error)

// executor 节点执行器（无状态，execContext 承载单次执行的可变状态）。
type executor struct {
	resolveLLM   llmConfigResolver
	clients       llmClientFactory
	execs         executionWriter
	rags          ragRetriever
	toolSources   toolSourceFactory // tool 节点工具来源（spec 016）；nil = 装配缺位（fail-fast）
	blockPrivate  bool             // O6：WORKFLOW_API_BLOCK_PRIVATE（RFC1918 一并拒绝）
	execChild     childExecFunc    // workflow 节点执行缝（spec 08）：New 接线，测试注入 stub
}

// newExecutor 组装执行器（依赖由组合根注入；测试注入 stub）。
func newExecutor(resolveLLM llmConfigResolver, clients llmClientFactory,
	execs executionWriter, rags ragRetriever, toolSources toolSourceFactory,
	blockPrivate bool) *executor {
	return &executor{resolveLLM: resolveLLM, clients: clients, execs: execs, rags: rags,
		toolSources: toolSources, blockPrivate: blockPrivate}
}

// runNode 按节点 config 类型分发执行，返回节点输出字符串。错误一律
// `node <key>: %w` 包装（失败定位，O4）。
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
		out, err = e.callTool(ctx, key, v, c)
	case *workflowapi.ReactConfig:
		out, err = e.callReact(ctx, key, v, c)
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

// callLLM llm 节点全链：strict 渲染（先于下游调用——图缺陷不浪费供应商额度；
// system_prompt 先于 prompt，spec 011）→ ResolveLLMConfig（明文 key 只在调用链
// 瞬间存在）→ llm client → Generate 非流式（单轮、无 SSE，不经 chat）→ 自记
// executions（失败也记，pre-attempt 除外）。system_prompt 非空发 [system, user]，
// 空串/缺省保持单 user 消息现状（加法兼容）。
func (e *executor) callLLM(ctx context.Context, key string, cfg *workflowapi.LLMConfig, c *execContext) (string, error) {
	system := ""
	if cfg.SystemPrompt != "" {
		var err error
		system, err = c.render(cfg.SystemPrompt)
		if err != nil {
			return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
		}
	}
	prompt, err := c.render(cfg.Prompt)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
	}
	if len(cfg.OutputSchema) > 0 { // spec 014：注入在渲染后、nodeIn 构造前——记录即实发
		prompt += buildJSONDirective(cfg.OutputSchema)
	}
	nodeIn := map[string]string{"prompt": prompt} // 渲染成功即记：下游失败时入参仍可读
	if system != "" {
		nodeIn["system_prompt"] = system
	}
	c.setNodeIn(nodeIn)
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
	msgs := make([]llm.Message, 0, 2)
	if system != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: system})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: prompt})
	opts := &llm.CallOptions{}
	if cfg.Temperature != 0 { // 0 = 跟随模型默认（不设字段）
		t := float32(cfg.Temperature)
		opts.Temperature = &t
	}
	start := time.Now()
	reply, genErr := client.Generate(ctx, msgs, opts)
	if !isPreAttemptErr(genErr) {
		e.recordExecution(ctx, key, resolved, cfg.ModelID, prompt, system, start, reply, genErr)
	}
	if genErr != nil {
		return "", genErr
	}
	if err := validateLLMOutput(reply.Content, cfg.OutputSchema); err != nil { // spec 014：声明非空才校验（未声明零变化）
		return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
	}
	return reply.Content, nil
}

// buildJSONDirective 由声明字段生成追加到 user 消息末尾的固定 JSON 输出指令
//（spec 014 FR-006，research D2 文案冻结）：系统固定文案，不做模板渲染、不含用户
// 变量；description 不进指令（声明说明是编排语义，不是提示词素材）。
func buildJSONDirective(fields []workflowapi.SchemaField) string {
	var b strings.Builder
	b.WriteString("\n\n请只输出一个 JSON 对象（不要使用 markdown 代码块，不要包含 JSON 以外的任何文本），对象包含以下字段：\n")
	for _, f := range fields {
		req := "可选"
		if f.Required {
			req = "必填"
		}
		fmt.Fprintf(&b, "- %s（%s，%s）\n", f.Name, f.Type, req)
	}
	return b.String()
}

// validateLLMOutput llm 节点回复按声明严格校验（spec 014 FR-005，语义对齐
// execute.go validateOutputSchema 家族）：未声明零校验；回复须合法 JSON 对象（null /
// 数组 / 纯文本 / 围栏包裹均拒，围栏不剥）；required 缺失拒；probeSchemaType 类型
// 探针不符拒（字段在场才探，可选缺失放行）；多余字段宽容。错误文案含字段名。
func validateLLMOutput(output string, fields []workflowapi.SchemaField) error {
	if len(fields) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &obj); err != nil || obj == nil {
		return fmt.Errorf("回复非合法 JSON 对象")
	}
	for _, f := range fields {
		raw, ok := obj[f.Name]
		if !ok {
			if f.Required {
				return fmt.Errorf("缺少必填字段 %s", f.Name)
			}
			continue
		}
		if !probeSchemaType(string(raw), f.Type) {
			return fmt.Errorf("字段 %s 类型应为 %s", f.Name, f.Type)
		}
	}
	return nil
}

// recordExecution llm 节点自记 executions（chat recordExecution 同款）：ConversationID
// =nil 即 workflow 调用标识；失败也记（ErrorClass 兜底 Network）；落库失败只 WARN
// 不阻断执行。system 非空时 Input 多记一个 system_prompt 键（spec 011，有值才加）。
func (e *executor) recordExecution(ctx context.Context, nodeKey string, resolved *providerapi.LLMConfig,
	modelID uint64, prompt, system string, start time.Time, reply *llm.Final, callErr error) {
	input := map[string]any{"prompt": prompt}
	if system != "" {
		input["system_prompt"] = system
	}
	e.recordExecutionInput(ctx, nodeKey, resolved, modelID, input, start, reply, callErr)
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

// retrieve knowledge_retrieval 节点：query 模板非空时 strict 渲染作检索问句
//（spec 020 加法修订；渲染空串视同未配），空/缺省回退 input（O1 终形现状）；
// TopK 直传（0 = rag service 取默认并 clamp，不在本层归一），结果格式化为编号段落
// 文本落池。渲染缺失变量归图缺陷（先于检索 IO）。
func (e *executor) retrieve(ctx context.Context, cfg *workflowapi.KnowledgeRetrievalConfig, c *execContext) (string, error) {
	query := c.vars["input"]
	if cfg.Query != "" {
		rendered, err := c.render(cfg.Query)
		if err != nil {
			return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
		}
		if rendered != "" {
			query = rendered
		}
	}
	c.setNodeIn(map[string]string{"query": query}) // 记渲染后实际检索值
	chunks, err := e.rags.Retrieve(ctx, ragapi.RetrieveReq{
		Query: query,
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

// callTool tool 节点执行（spec 016 §5 / D2）：解析工具（nil 装配 → 同码「工具来源
// 未注入」；ToolSourceFor 按 ToolID → Tools 预检，空清单 = 工具已删 / server 停用
// 静默过滤 → 404 哨兵透传）→ args 逐值严格渲染（缺失变量 → 图缺陷 400 带
// args.<k> 定位，先于调用——不浪费工具 IO）→ 按 key 排序 marshal（确定性序列化，
// 轨迹可复现）→ 单次确定性 Invoke（非 ReACT）→ Content 即节点输出（落池与 node
// run Output / DurationMs 归 walk 层既有机制）。前置 Tools() 预检中和 boundToolSource
// 加载失败吞错路径——预检通过后 Invoke IsError 只剩真实调用失败一种来源；fail-fast
// 终止 run（错误文本进 message），与 chat 的「错误即上下文」分野：契约同、消费语义
// 由调用方定（contracts/errors.md 钉死）。
func (e *executor) callTool(ctx context.Context, key string, cfg *workflowapi.ToolConfig, c *execContext) (string, error) {
	if e.toolSources == nil {
		return "", fmt.Errorf("%w: 工具来源未注入", mcpapi.ErrMCPToolCallFailed)
	}
	src := e.toolSources.ToolSourceFor(mcpapi.ToolSourceReq{ToolIDs: []uint64{cfg.ToolID}})
	defs, err := src.Tools(ctx)
	if err != nil {
		return "", fmt.Errorf("load tools: %w", err) // DB 错 %w 包装（500），预检吞错路径由本行兜住
	}
	if len(defs) == 0 {
		return "", mcpapi.ErrMCPToolNotFound // 工具已删 / server 停用静默过滤 → 404 哨兵透传
	}
	rendered := make(map[string]string, len(cfg.Args))
	for k, tpl := range cfg.Args {
		v, err := c.render(tpl)
		if err != nil {
			return "", fmt.Errorf("%w: args.%s: %v", errs.ErrValidationFailed, k, err)
		}
		rendered[k] = v
	}
	argsJSON, err := json.Marshal(rendered) // map 按 key 排序序列化（Go 标准库保证）
	if err != nil {
		return "", fmt.Errorf("marshal args: %w", err) // 防御兜底：map[string]string 序列化不会失败
	}
	c.setNodeIn(map[string]string{ // 渲染成功即记：调用失败时入参仍可读（对齐 callWorkflow）
		"tool_id": strconv.FormatUint(cfg.ToolID, 10),
		"args":    string(argsJSON),
	})
	res := src.Invoke(ctx, tool.ToolCall{
		ID:        fmt.Sprintf("wf-%s", key), // 执行器生成锚点（workflow 单次调用，无 LLM 轮次）
		Name:      defs[0].Name,              // 调用名 = Source 供出的派生名（s{id}__{tool}），executor 不自造
		Arguments: argsJSON,
	})
	if res.IsError {
		return "", fmt.Errorf("%w: %s", mcpapi.ErrMCPToolCallFailed, truncateUTF8(res.Content, maxTraceValueBytes))
	}
	return res.Content, nil
}

// callReact react 节点（spec 020 FR-003 / C2）：图内 mini-agent——prompts strict 渲染
//（缺失变量图缺陷 400，先于一切下游 IO）→ 模型解析链同 callLLM → 工具集并集（两绑定
// 均空 = reg nil 纯推理零 mcp 交互；非空 ToolSourceFor+NewRegistry，构建失败 %w 500、
// 过滤后空清单 ErrMCPToolNotFound 404 fail-fast 同 callTool 先例）→ reactloop.Run
// Generate 模式（MaxRounds 透传）→ 终答落池。HitLimit 非错误（终答照落）；每轮经
// OnRound 自记 executions（ConversationID=nil 同 llm 节点标识）；OnToolCall/OnToolResult
// 累积 tool_chain 进 nodeIn（workflow 无 messages 表的调用链排障补位，C3）。
func (e *executor) callReact(ctx context.Context, key string, cfg *workflowapi.ReactConfig, c *execContext) (string, error) {
	system := ""
	if cfg.SystemPrompt != "" {
		var err error
		system, err = c.render(cfg.SystemPrompt)
		if err != nil {
			return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
		}
	}
	prompt, err := c.render(cfg.Prompt)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errs.ErrValidationFailed, err)
	}
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
	var reg *tool.Registry
	if len(cfg.ServerIDs) > 0 || len(cfg.ToolIDs) > 0 {
		if e.toolSources == nil {
			return "", fmt.Errorf("%w: 工具来源未注入", mcpapi.ErrMCPToolCallFailed)
		}
		src := e.toolSources.ToolSourceFor(mcpapi.ToolSourceReq{ToolIDs: cfg.ToolIDs, ServerIDs: cfg.ServerIDs})
		reg, err = tool.NewRegistry(ctx, src)
		if err != nil {
			return "", fmt.Errorf("build react tool registry: %w", err)
		}
		if len(reg.Defs()) == 0 {
			return "", mcpapi.ErrMCPToolNotFound // 绑定即意图：server 停用 / 工具删 fail-fast，不静默降级
		}
	}
	msgs := make([]llm.Message, 0, 2)
	if system != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: system})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: prompt})
	opts := &llm.CallOptions{}
	if cfg.Temperature != 0 {
		t := float32(cfg.Temperature)
		opts.Temperature = &t
	}
	if reg != nil {
		opts.Tools = reg.Defs()
	}

	// 观测闭包态：executions 逐轮计时近似点 + tool_chain 累积（args 截 200 rune 防膨胀）
	type chainEntry struct {
		Round   int    `json:"round"`
		ID      string `json:"id"`
		Name    string `json:"name"`
		Args    string `json:"args"`
		IsError bool   `json:"is_error"`
	}
	var (
		chain      = make([]chainEntry, 0, 4)
		currentRnd int
		roundStart = time.Now()
		toolCalls  int
	)
	hooks := reactloop.Hooks{
		OnRound: func(round int, _ []llm.Message, fin *llm.Final, callErr error) error {
			roundCalls := 0
			if fin != nil {
				roundCalls = len(fin.ToolCalls)
			}
			e.recordReactExecution(ctx, key, resolved, cfg.ModelID, prompt, system,
				round, roundCalls, roundStart, fin, callErr)
			currentRnd = round
			roundStart = time.Now()
			return nil
		},
		OnToolCall: func(v reactloop.CallView) error {
			toolCalls++
			chain = append(chain, chainEntry{Round: currentRnd, ID: v.Call.ID, Name: v.Call.Name,
				Args: truncateUTF8(string(v.Call.Arguments), 200)})
			return nil
		},
		OnToolResult: func(v reactloop.CallView, result tool.ToolResult) error {
			for i := range chain {
				if chain[i].ID == v.Call.ID && chain[i].Round == currentRnd {
					chain[i].IsError = result.IsError
				}
			}
			return nil
		},
	}
	res, runErr := reactloop.Run(ctx, client, reg, msgs, opts,
		reactloop.Config{MaxRounds: cfg.MaxRounds}, hooks)

	nodeIn := map[string]string{"prompt": prompt, "max_rounds": strconv.Itoa(cfg.MaxRounds)}
	if system != "" {
		nodeIn["system_prompt"] = system
	}
	nodeIn["rounds"] = strconv.Itoa(res.Rounds)
	nodeIn["tool_calls"] = strconv.Itoa(toolCalls)
	if len(chain) > 0 {
		if b, merr := json.Marshal(chain); merr == nil {
			nodeIn["tool_chain"] = string(b)
		}
	}
	if res.HitLimit {
		nodeIn["hit_limit"] = "true"
	}
	c.setNodeIn(nodeIn)

	if res.Cancelled {
		return "", ctx.Err() // 调用方取消 / 总超时：walk 层既有归一捕获
	}
	if res.Aborted {
		return "", fmt.Errorf("%w: react loop aborted", workflowapi.ErrWorkflowExecutionFailed)
	}
	if runErr != nil {
		return "", runErr // LLM 调用错误原样透传（分类 / 哨兵同 callLLM）
	}
	return res.Content, nil
}

// recordReactExecution react 节点逐轮 executions（C3）：Input 带轮次维度
//（{prompt, system_prompt?, round, tool_calls}），其余形态同 recordExecution。
func (e *executor) recordReactExecution(ctx context.Context, nodeKey string, resolved *providerapi.LLMConfig,
	modelID uint64, prompt, system string, round, roundCalls int, start time.Time, reply *llm.Final, callErr error) {
	input := map[string]any{"prompt": prompt, "round": round, "tool_calls": roundCalls}
	if system != "" {
		input["system_prompt"] = system
	}
	e.recordExecutionInput(ctx, nodeKey, resolved, modelID, input, start, reply, callErr)
}

// recordExecutionInput executions 行 builder（llm 与 react 节点共用）：ConversationID
// =nil 即 workflow 调用标识；失败也记（ErrorClass 兜底 Network）；落库失败只 WARN。
func (e *executor) recordExecutionInput(ctx context.Context, nodeKey string, resolved *providerapi.LLMConfig,
	modelID uint64, input map[string]any, start time.Time, reply *llm.Final, callErr error) {
	row := &logging.Execution{
		ModelID:    &modelID,
		ModelName:  resolved.ModelID, // 冗余快照：模型删除后记录仍可读
		Input:      input,
		DurationMs: int32(time.Since(start).Milliseconds()),
	}
	if callErr != nil {
		cls := string(executionErrorClass(callErr))
		row.ErrorClass = &cls
	} else if reply != nil {
		row.Output = map[string]any{"content": reply.Content}
		row.FinishReason = reply.FinishReason
		row.PromptTokens, row.CompletionTokens = reply.Usage.Input, reply.Usage.Output
		row.TotalTokens = row.PromptTokens + row.CompletionTokens
	}
	if err := e.execs.Create(ctx, row); err != nil {
		slog.WarnContext(ctx, "workflow: record node execution failed", "node", nodeKey, "err", err)
	}
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

// buildOutput end 节点终稿（spec 018 扩展）：output_fields 非空（映射模式）优先——
// buildOutputFields 按引用构造 JSON 对象（Marshal 转义，字符串含引号 / 换行安全）；
// 否则走既有 output 模板路径（高级模式）：空 output = 取最后执行节点的输出（与
// 「无出边 = 隐式结束」语义一致）；无步骤（end 即首节点）返回空串。
func (e *executor) buildOutput(cfg *workflowapi.EndConfig, c *execContext) (string, error) {
	if len(cfg.OutputFields) > 0 {
		return e.buildOutputFields(cfg.OutputFields, c)
	}
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

// buildOutputFields 映射模式终稿构造（spec 018 FR-009 / R6）：逐字段 lookup(ref) 取池值
//（含一级下钻，缺失 = strict 图缺陷），按 ec.outputTypes 声明类型处理值——声明 string
// 按字符串落位（json.Marshal 转义）；声明 number / boolean 须可解析为对应 JSON 型
//（原文 RawMessage 落位，解析失败 = 声明与实际不符的图缺陷）；字段未声明类型回退
// 「可解析 JSON 则原样、否则按字符串」。整对象 Marshal 产出（map 按 key 排序，确定性
// 序列化），既有 validateOutputSchema 兜底校验。nodeIn 记映射摘要（对齐既有 output
// 模板原文形态——记录渲染前配置，排障可复现）。
func (e *executor) buildOutputFields(fields []workflowapi.OutputField, c *execContext) (string, error) {
	inTrace, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("marshal output_fields: %w", err) // 防御兜底：结构体序列化不会失败
	}
	c.setNodeIn(map[string]string{"output_fields": string(inTrace)})
	obj := make(map[string]any, len(fields))
	for _, f := range fields {
		m := singleVarRE.FindStringSubmatch(f.Ref)
		if m == nil { // 理论不可达（保存期 EndConfig.Validate 已挡），运行时兜底归图缺陷
			return "", fmt.Errorf("%w: output_fields 字段 %s ref 非法 %q", errs.ErrValidationFailed, f.Name, f.Ref)
		}
		v, ok := c.lookup(m[1])
		if !ok {
			return "", fmt.Errorf("%w: variable %q not defined", errs.ErrValidationFailed, m[1])
		}
		switch c.outputTypes[f.Name] {
		case "string":
			obj[f.Name] = v
		case "number":
			if !isJSONNumberText(v) {
				return "", fmt.Errorf("%w: output 字段 %s 声明 number 但值不可解析: %q", errs.ErrValidationFailed, f.Name, v)
			}
			obj[f.Name] = json.RawMessage(v)
		case "boolean":
			if v != "true" && v != "false" {
				return "", fmt.Errorf("%w: output 字段 %s 声明 boolean 但值不可解析: %q", errs.ErrValidationFailed, f.Name, v)
			}
			obj[f.Name] = json.RawMessage(v)
		default: // 未声明（nil 表或字段不在 schema）：可解析 JSON 则原样、否则按字符串
			if json.Valid([]byte(v)) && v != "null" {
				obj[f.Name] = json.RawMessage(v)
			} else {
				obj[f.Name] = v
			}
		}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal output object: %w", err) // 防御兜底：值均为合法 JSON 类型
	}
	return string(b), nil
}

// isJSONNumberText 值原文是否为合法 JSON number 字面量。null 对非指针类型是无操作
// 成功（json.Number 解码不报错），显式排除。
func isJSONNumberText(v string) bool {
	if v == "null" {
		return false
	}
	var n json.Number
	return json.Unmarshal([]byte(v), &n) == nil
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
