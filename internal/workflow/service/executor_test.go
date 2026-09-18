package service

// executor 测试（spec 06 §4.2 / O4-O6）：runNode 六类分发（tool fail-fast 图缺陷 400 /
// 未知 config 兜底）、callLLM 全链（resolve → client → Generate 非流式 → executions
// 自记恰好一行 ConversationID=nil / 抢槽失败不落库）、retrieve 编号段落落池、
// api 节点全链渲染 + scheme/SSRF 环境类 500、end buildOutput、route 声明顺序首条命中
// 与无命中 fail-fast。零真实网络 / LLM（fake Streamer + 不可达 IP 字面量）。

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	ragapi "github.com/Karlsk/go-hify/internal/rag/api"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// newTestExecutor 常规装配：可解析配置 + 可编程上游 + 记录式 executions/rags。
func newTestExecutor(gen func(ctx context.Context, call int) (*schema.Message, error)) (
	*executor, *stubResolve, *stubClientFactory, *genStreamer, *execRecorder, *stubRetrieve,
) {
	r := &stubResolve{cfg: testResolveCfg()}
	fs := &genStreamer{gen: gen}
	factory := &stubClientFactory{client: llm.NewClient("test", fastProfile(), fs)}
	execs := &execRecorder{}
	rags := &stubRetrieve{}
	return newExecutor(r, factory, execs, rags, false), r, factory, fs, execs, rags
}

// okGen 单值应答编排（忽略调用序）。
func okGen(s string) func(context.Context, int) (*schema.Message, error) {
	return func(context.Context, int) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, Content: s,
			ResponseMeta: &schema.ResponseMeta{FinishReason: "stop",
				Usage: &schema.TokenUsage{PromptTokens: 12, CompletionTokens: 3}}}, nil
	}
}

// ---- llm 节点：全链 + executions 自记 ----

func TestRunNodeLLMChain(t *testing.T) {
	e, r, factory, fs, execs, _ := newTestExecutor(okGen("ORDER_QUERY"))
	c := newExecContext("查订单")

	out, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3, Prompt: "判断意图：{{input}}", Temperature: 0.7}, c)
	require.NoError(t, err)
	assert.Equal(t, "ORDER_QUERY", out)

	// resolve → client → Generate 全链（O2：model_id → ResolveLLMConfig → UpstreamOptions）
	assert.Equal(t, 1, r.calls)
	assert.Equal(t, uint64(3), r.lastReq.ModelID)
	assert.Equal(t, "openai主力", factory.gotKey)
	assert.Equal(t, "openai", string(factory.gotOpts.Kind))
	assert.Equal(t, "gpt-4o", factory.gotOpts.Model)
	assert.Equal(t, "sk-test", factory.gotOpts.APIKey, "明文 key 只存在于调用链瞬间")

	msgs := fs.lastMsgs()
	require.Len(t, msgs, 1)
	assert.Equal(t, "判断意图：查订单", msgs[0].Content, "prompt 经 {{input}} 渲染后出站")
	opts := fs.lastOpts()
	require.NotNil(t, opts.Temperature)
	assert.InDelta(t, 0.7, *opts.Temperature, 0.0001)

	// executions 自记恰好一行：ConversationID=nil 即 workflow 节点调用标识
	require.Len(t, execs.rows, 1)
	row := execs.rows[0]
	assert.Nil(t, row.ConversationID)
	require.NotNil(t, row.ModelID)
	assert.Equal(t, uint64(3), *row.ModelID)
	assert.Equal(t, "gpt-4o", row.ModelName, "冗余快照：模型删除后记录仍可读")
	assert.Equal(t, int64(12), row.PromptTokens)
	assert.Equal(t, int64(3), row.CompletionTokens)
	assert.Equal(t, "stop", row.FinishReason)
	assert.Nil(t, row.ErrorClass)

	// runNode 不落池——set 由 execute 游走做（仅成功节点），职责分离
	_, pooled := c.vars["classify"]
	assert.False(t, pooled)
}

// temperature 0 = 跟随模型默认（不传 CallOptions.Temperature）。
func TestRunNodeLLMTemperatureZero(t *testing.T) {
	e, _, _, fs, _, _ := newTestExecutor(okGen("x"))
	_, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3, Prompt: "p"}, newExecContext("in"))
	require.NoError(t, err)
	assert.Nil(t, fs.lastOpts().Temperature)
}

// resolve 失败：下游哨兵透传（404 MODEL_NOT_FOUND），带 node 前缀；未打到上游不落 executions。
func TestRunNodeLLMResolveNotFound(t *testing.T) {
	e, r, factory, _, execs, _ := newTestExecutor(okGen("x"))
	e.resolveLLM = &stubResolve{err: providerapi.ErrModelNotFound}

	_, err := e.runNode(context.Background(), "reply",
		&workflowapi.LLMConfig{ModelID: 9, Prompt: "p"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, providerapi.ErrModelNotFound, "下游哨兵原样透传")
	assert.Contains(t, err.Error(), "node reply:")
	assert.Empty(t, factory.gotKey, "不可达：client 工厂未被调")
	assert.Empty(t, execs.rows)
	_ = r
}

// Generate 失败：错误传播 + executions 仍自记（ErrorClass 兜底 Network）。
func TestRunNodeLLMGenerateError(t *testing.T) {
	e, _, _, _, execs, _ := newTestExecutor(func(context.Context, int) (*schema.Message, error) {
		return nil, assert.AnError
	})

	_, err := e.runNode(context.Background(), "reply",
		&workflowapi.LLMConfig{ModelID: 3, Prompt: "p"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "node reply:")

	require.Len(t, execs.rows, 1)
	row := execs.rows[0]
	assert.NotNil(t, row.ErrorClass, "失败也自记且带错误类")
	assert.Equal(t, "Network", *row.ErrorClass, "未分类错误兜底 Network（chat recordExecution 同款）")
}

// 抢槽失败（ErrProviderBusy）：未真正打到供应商，不落 executions（chat isPreAttemptErr 先例）。
func TestRunNodeLLMProviderBusyNoRecord(t *testing.T) {
	r := &stubResolve{cfg: testResolveCfg()}
	factory := &stubClientFactory{err: llm.ErrProviderBusy}
	execs := &execRecorder{}
	e := newExecutor(r, factory, execs, &stubRetrieve{}, false)

	_, err := e.runNode(context.Background(), "reply",
		&workflowapi.LLMConfig{ModelID: 3, Prompt: "p"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrProviderBusy, "哨兵透传（handler 503）")
	assert.Contains(t, err.Error(), "node reply:")
	assert.Empty(t, execs.rows, "没打到上游不落 executions")
}

// prompt 缺失变量：图缺陷类 400（strict 渲染，运行时兜底）。
func TestRunNodeLLMMissingVar(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	_, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3, Prompt: "{{typo_key}}"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "缺失变量 → 图缺陷 400")
	assert.Contains(t, err.Error(), "typo_key")
	assert.Contains(t, err.Error(), "node classify:")
}

// ---- condition 节点：纯内存求值，零外部调用 ----

func TestRunNodeCondition(t *testing.T) {
	e, r, _, _, execs, _ := newTestExecutor(okGen("x"))
	c := newExecContext("查订单")
	c.set("classify", "ORDER_QUERY")

	out, err := e.runNode(context.Background(), "router",
		&workflowapi.ConditionConfig{Expression: "{{classify}} == 'ORDER_QUERY'"}, c)
	require.NoError(t, err)
	assert.Equal(t, "true", out)
	assert.Equal(t, 0, r.calls, "纯内存求值：零 LLM 调用")
	assert.Empty(t, execs.rows)
}

// ---- knowledge_retrieval 节点：query=input，结果格式化为编号段落文本 ----

func TestRunNodeRetrieve(t *testing.T) {
	e, _, _, _, _, rags := newTestExecutor(okGen("x"))
	rags.resp = []ragapi.RetrievedChunk{
		{Content: "订单已发货", DocumentName: "faq.md"},
		{Content: "退换货政策", DocumentName: "policy.md"},
	}

	out, err := e.runNode(context.Background(), "kb",
		&workflowapi.KnowledgeRetrievalConfig{KnowledgeBaseID: 7, TopK: 5}, newExecContext("查订单"))
	require.NoError(t, err)
	assert.Equal(t, "[1] 订单已发货（来源：faq.md）\n[2] 退换货政策（来源：policy.md）", out)

	assert.Equal(t, 1, rags.calls)
	assert.Equal(t, "查订单", rags.lastReq.Query, "检索 query = input（O1 单一入参终形）")
	assert.Equal(t, []uint64{7}, rags.lastReq.KBIDs)
	assert.Equal(t, 5, rags.lastReq.TopK)
}

// TopK=0 直传（rag service 取默认并 clamp，不在本层归一）；空结果 → 空串落池。
func TestRunNodeRetrieveEmpty(t *testing.T) {
	e, _, _, _, _, rags := newTestExecutor(okGen("x"))
	out, err := e.runNode(context.Background(), "kb",
		&workflowapi.KnowledgeRetrievalConfig{KnowledgeBaseID: 7}, newExecContext("查订单"))
	require.NoError(t, err)
	assert.Equal(t, "", out)
	assert.Equal(t, 0, rags.lastReq.TopK)
}

// ---- api 节点：全链渲染（纯函数）+ scheme/SSRF 分类 ----

func TestBuildAPIRequest(t *testing.T) {
	c := newExecContext("查订单")
	c.set("token", "abc")
	cfg := workflowapi.ApiCallConfig{
		URL:     "https://api.example.com/orders/{{input}}",
		Method:  "POST",
		Headers: map[string]string{"X-Token": "{{token}}", "Content-Type": "application/json"},
		Body:    `{"q":"{{input}}"}`,
	}

	req, err := buildAPIRequest(cfg, c)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "https", req.URL.Scheme)
	assert.Equal(t, "api.example.com", req.URL.Host)
	assert.Equal(t, "/orders/查订单", req.URL.Path)
	assert.Equal(t, "abc", req.Header.Get("X-Token"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"q":"查订单"}`, string(body))
}

// 渲染后 scheme 非 http/https（模板注入 file://）：环境限制类 500。
func TestRunNodeAPISchemeBlocked(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	_, err := e.runNode(context.Background(), "api1",
		&workflowapi.ApiCallConfig{URL: "file:///etc/passwd", Method: "GET"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowExecutionFailed, "scheme 拦截 → 环境限制 500")
	assert.Contains(t, err.Error(), "node api1:")
}

// SSRF 恒禁段（loopback）：Dialer.Control 建连时拦截 → 环境限制类 500。
func TestRunNodeAPISSRFBlocked(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	_, err := e.runNode(context.Background(), "api1",
		&workflowapi.ApiCallConfig{URL: "http://127.0.0.1:1/ping", Method: "GET"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowExecutionFailed, "SSRF 拦截 → 环境限制 500")
	assert.Contains(t, err.Error(), "node api1:")
}

// ---- end 节点：buildOutput ----

func TestRunNodeEnd(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	c := newExecContext("in")
	c.set("reply", "订单已发货")

	out, err := e.runNode(context.Background(), "finish", &workflowapi.EndConfig{Output: "{{reply}}"}, c)
	require.NoError(t, err)
	assert.Equal(t, "订单已发货", out)

	// 空 output = 取最后执行节点的输出（与隐式结束语义一致）
	c2 := newExecContext("in")
	c2.set("reply", "答案")
	c2.record(nodeStep{NodeKey: "reply", NodeType: "llm", Status: "succeeded"})
	out2, err := e.runNode(context.Background(), "finish", &workflowapi.EndConfig{}, c2)
	require.NoError(t, err)
	assert.Equal(t, "答案", out2)
}

// ---- tool 节点：执行期 fail-fast（mcp 未建，图缺陷类 400）----

func TestRunNodeTool(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	_, err := e.runNode(context.Background(), "t1", &workflowapi.ToolConfig{ToolID: 5}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "tool 未支持 → 图缺陷 400")
	assert.Contains(t, err.Error(), "node t1:")
	assert.Contains(t, err.Error(), "tool")
}

// 未知 config（密封面外的兜底臂）：报错而非静默跳过。
func TestRunNodeUnknownConfig(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	_, err := e.runNode(context.Background(), "x", nil, newExecContext("in"))
	assert.Error(t, err)
}

// ---- route：按声明顺序首条命中 / 无条件直走 / 无命中 fail-fast ----

func TestRoute(t *testing.T) {
	cond := func(s string) *string { return &s }
	cases := []struct {
		name   string
		edges  []WorkflowEdge
		result string
		want   string
		wantOK bool
	}{
		{"无出边 = 隐式终止", nil, "任意输出", "", false},
		{"线性节点无条件直走（结果不参与）", []WorkflowEdge{{TargetNodeKey: "next"}}, "节点输出", "next", true},
		{"condition 命中 true 分支", []WorkflowEdge{
			{TargetNodeKey: "a", Condition: cond("true")},
			{TargetNodeKey: "b", Condition: cond("false")},
		}, "true", "a", true},
		{"condition 命中 false 分支", []WorkflowEdge{
			{TargetNodeKey: "a", Condition: cond("true")},
			{TargetNodeKey: "b", Condition: cond("false")},
		}, "false", "b", true},
		{"首条命中（声明顺序，双双 true 取先声明）", []WorkflowEdge{
			{TargetNodeKey: "x", Condition: cond("true")},
			{TargetNodeKey: "y", Condition: cond("true")},
		}, "true", "x", true},
		{"裸 var 多路分支：按值匹配", []WorkflowEdge{
			{TargetNodeKey: "order", Condition: cond("ORDER_QUERY")},
			{TargetNodeKey: "refund", Condition: cond("REFUND")},
		}, "REFUND", "refund", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, ok, err := route(tc.edges, tc.result)
			require.NoError(t, err)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, next)
		})
	}
}

// 无命中出边 = 图缺陷（执行错误 fail-fast，O4 类 400；node 前缀由 execute 游走补）。
func TestRouteNoMatchFailFast(t *testing.T) {
	f, b := "false", "false"
	edges := []WorkflowEdge{
		{TargetNodeKey: "a", Condition: &f},
		{TargetNodeKey: "b", Condition: &b},
	}
	_, _, err := route(edges, "true")
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed)
}
