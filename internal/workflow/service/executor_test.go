package service

// executor 测试（spec 06 §4.2 / O4-O6）：runNode 六类分发（tool fail-fast 图缺陷 400 /
// 未知 config 兜底）、callLLM 全链（resolve → client → Generate 非流式 → executions
// 自记恰好一行 ConversationID=nil / 抢槽失败不落库）、retrieve 编号段落落池、
// api 节点全链渲染 + scheme/SSRF 环境类 500、end buildOutput、route 声明顺序首条命中
// 与无命中 fail-fast。零真实网络 / LLM（fake Streamer + 不可达 IP 字面量）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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

// ---- llm 节点 system_prompt（spec 011 加法修订：FR-001~FR-006）----

// TestRunNodeLLMSystemPrompt 带 system_prompt 执行（SC-001/FR-002/FR-003/FR-005）：
// 非空 → strict 渲染 → Generate 收到恰 [system, user] 两条消息，内容为各自模板
// 渲染后文本（{{base.field}} 一级下钻语义与 prompt 一致）；node_in 摘要与
// executions 行 Input 一并记录渲染后的 system_prompt。
func TestRunNodeLLMSystemPrompt(t *testing.T) {
	e, _, _, fs, execs, _ := newTestExecutor(okGen("ORDER_QUERY"))
	c := newExecContext(`{"question":"查订单"}`)

	out, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3,
			SystemPrompt: "你是客服路由分类器：{{input.question}}",
			Prompt:       "判断意图：{{input.question}}"}, c)
	require.NoError(t, err)
	assert.Equal(t, "ORDER_QUERY", out)

	msgs := fs.lastMsgs()
	require.Len(t, msgs, 2, "带 system_prompt：恰两条消息")
	assert.Equal(t, schema.System, msgs[0].Role)
	assert.Equal(t, "你是客服路由分类器：查订单", msgs[0].Content, "system 模板一级下钻渲染（FR-002）")
	assert.Equal(t, schema.User, msgs[1].Role)
	assert.Equal(t, "判断意图：查订单", msgs[1].Content)

	assert.Equal(t, "你是客服路由分类器：查订单", c.pendingIn["system_prompt"], "node_in 摘要含渲染后 system（FR-005）")
	assert.Equal(t, "判断意图：查订单", c.pendingIn["prompt"])

	require.Len(t, execs.rows, 1)
	assert.Equal(t, "你是客服路由分类器：查订单", execs.rows[0].Input["system_prompt"], "executions Input 含渲染后 system（FR-005）")
	assert.Equal(t, "判断意图：查订单", execs.rows[0].Input["prompt"])
}

// TestRunNodeLLMSystemPromptMissingVar system_prompt 缺失变量 fail-fast（SC-003/FR-004）：
// 与 prompt 同语义——ErrValidationFailed、文案含变量名与节点 key；发生在
// ResolveLLMConfig 之前：零上游调用、executions 零落（图缺陷不浪费供应商额度）。
func TestRunNodeLLMSystemPromptMissingVar(t *testing.T) {
	e, r, factory, fs, execs, _ := newTestExecutor(okGen("x"))

	_, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3,
			SystemPrompt: "你是{{typo_role}}",
			Prompt:       "判断意图：{{input}}"}, newExecContext("in"))
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "system_prompt 缺失变量 → 图缺陷 400")
	assert.Contains(t, err.Error(), "typo_role")
	assert.Contains(t, err.Error(), "node classify:")
	assert.Equal(t, 0, r.calls, "fail-fast 于 resolve 之前")
	assert.Empty(t, factory.gotKey, "零上游调用")
	assert.Nil(t, fs.lastMsgs(), "Generate 未被调")
	assert.Empty(t, execs.rows, "executions 零落")
}

// TestRunNodeLLMSystemPromptEmptyEqualsAbsent 空串等价缺省（Edge Cases）：
// SystemPrompt="" 与不携带行为一致——单 user 消息、记录不新增 system_prompt 键。
func TestRunNodeLLMSystemPromptEmptyEqualsAbsent(t *testing.T) {
	e, _, _, fs, execs, _ := newTestExecutor(okGen("x"))
	c := newExecContext("in")

	_, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3, SystemPrompt: "", Prompt: "判断意图：{{input}}"}, c)
	require.NoError(t, err)

	msgs := fs.lastMsgs()
	require.Len(t, msgs, 1, "空串 = 缺省：仍单条 user 消息")
	assert.Equal(t, schema.User, msgs[0].Role)
	assert.Equal(t, "判断意图：in", msgs[0].Content)

	assert.NotContains(t, c.pendingIn, "system_prompt", "空串不新增记录键（旧形态不变）")
	require.Len(t, execs.rows, 1)
	assert.NotContains(t, execs.rows[0].Input, "system_prompt")
	assert.Equal(t, "判断意图：in", execs.rows[0].Input["prompt"])
}

// TestExecuteNestedChildSystemPrompt 嵌套子图内 LLM 节点同样生效（US1-4）：
// 子图 c_work 带 system_prompt → 子池渲染（{{input.query}}）→ 恰 [system, user]，
// 子 executions 行 Input 同步携带渲染后 system_prompt。
func TestExecuteNestedChildSystemPrompt(t *testing.T) {
	child := childTaskGraph(5, "published",
		`[{"name":"query","type":"string","required":true}]`, "",
		`检索：{{input.query}}`, `{{c_work}}`)
	for i, n := range child.nodes {
		if n.NodeKey == "c_work" {
			child.nodes[i].Config = `{"model_id":"3","system_prompt":"子任务角色：{{input.query}}","prompt":"检索：{{input.query}}"}`
		}
	}
	parent := parentNestedGraph(3, "chat", "published", 5, `{"query":"{{classify}}"}`, `{{sub.answer}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child},
		twoGen("ORDER_QUERY", `{"answer":"子答案"}`))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单"})
	require.NoError(t, err)
	assert.Equal(t, "子答案", res.Output, "父 end 经 {{sub.answer}} 取子终稿字段，行为不受影响")

	msgs := env.streamer.lastMsgs()
	require.Len(t, msgs, 2, "子图 LLM 节点带 system_prompt：恰 [system, user]")
	assert.Equal(t, schema.System, msgs[0].Role)
	assert.Equal(t, "子任务角色：ORDER_QUERY", msgs[0].Content, "子池 {{input.query}} 渲染")
	assert.Equal(t, schema.User, msgs[1].Role)
	assert.Equal(t, "检索：ORDER_QUERY", msgs[1].Content)

	require.Len(t, env.execs.rows, 2, "父 classify + 子 c_work 两行")
	assert.Equal(t, "子任务角色：ORDER_QUERY", env.execs.rows[1].Input["system_prompt"], "子行 Input 携带渲染后 system")
}

// ---- spec 014 T002：未声明节点的 golden 基准（US3 SC-003 逐字节零变化对照）----

// TestCallLLMUndeclaredGolden 改造前落样：未声明输出字段的 llm 节点全链基准——
// 出站消息序列（单 user / system+user 两形态）、node_in、executions Input 均为
// 渲染后原文，无任何追加文本与新键。spec 014 合入后本用例零改动须保持全绿
//（未声明节点若被注入或引入新键，逐字节断言即击穿）。
func TestCallLLMUndeclaredGolden(t *testing.T) {
	t.Run("单 user 形态", func(t *testing.T) {
		e, _, _, fs, execs, _ := newTestExecutor(okGen("ORDER_QUERY"))
		c := newExecContext("查订单")

		out, err := e.runNode(context.Background(), "classify",
			&workflowapi.LLMConfig{ModelID: 3, Prompt: "判断意图：{{input}}"}, c)
		require.NoError(t, err)
		assert.Equal(t, "ORDER_QUERY", out)

		msgs := fs.lastMsgs()
		require.Len(t, msgs, 1, "恰一条消息")
		assert.Equal(t, schema.User, msgs[0].Role)
		assert.Equal(t, "判断意图：查订单", msgs[0].Content, "user 内容 = 渲染后 prompt 原文，无注入后缀")

		assert.Equal(t, map[string]string{"prompt": "判断意图：查订单"}, c.takeNodeIn(), "node_in 恰 prompt 一键")
		require.Len(t, execs.rows, 1)
		assert.Equal(t, map[string]any{"prompt": "判断意图：查订单"}, execs.rows[0].Input,
			"executions Input 与 node_in 同形态")
	})
	t.Run("system+user 形态", func(t *testing.T) {
		e, _, _, fs, execs, _ := newTestExecutor(okGen("ORDER_QUERY"))
		c := newExecContext("查订单")

		_, err := e.runNode(context.Background(), "classify",
			&workflowapi.LLMConfig{ModelID: 3, SystemPrompt: "你是分类器", Prompt: "判断意图：{{input}}"}, c)
		require.NoError(t, err)

		msgs := fs.lastMsgs()
		require.Len(t, msgs, 2, "恰 [system, user] 两条")
		assert.Equal(t, schema.System, msgs[0].Role)
		assert.Equal(t, "你是分类器", msgs[0].Content)
		assert.Equal(t, schema.User, msgs[1].Role)
		assert.Equal(t, "判断意图：查订单", msgs[1].Content, "user 无注入后缀；system 不受影响")

		assert.Equal(t, map[string]string{"prompt": "判断意图：查订单", "system_prompt": "你是分类器"}, c.takeNodeIn())
		require.Len(t, execs.rows, 1)
		assert.Equal(t, map[string]any{"prompt": "判断意图：查订单", "system_prompt": "你是分类器"}, execs.rows[0].Input)
	})
}

// ---- spec 014 T009：US2 注入与校验（FR-005 / FR-006 / SC-002 / SC-005）----

// TestBuildJSONDirective 注入指令纯函数（research D2 文案逐字冻结）：前缀 + 每字段
// 一行 `- {name}（{type}，{必填|可选}）`；description 不进指令；不做模板渲染。
func TestBuildJSONDirective(t *testing.T) {
	fields := []workflowapi.SchemaField{
		{Name: "code", Type: "string", Required: true, Description: "分类码"},
		{Name: "score", Type: "number", Description: ""},
		{Name: "verbose", Type: "boolean"},
	}
	want := "\n\n请只输出一个 JSON 对象（不要使用 markdown 代码块，不要包含 JSON 以外的任何文本），对象包含以下字段：\n" +
		"- code（string，必填）\n" +
		"- score（number，可选）\n" +
		"- verbose（boolean，可选）\n"
	got := buildJSONDirective(fields)
	assert.Equal(t, want, got)
	assert.NotContains(t, got, "分类码", "description 不进指令")
}

// TestValidateLLMOutput 回复按声明校验（FR-005，语义对齐 validateOutputSchema 家族）：
// 未声明零校验 / 非 JSON 对象拒（纯文本、null、数组、markdown 围栏）/ required 缺失拒
// 含字段名 / 类型探针不符拒含字段名与期望类型 / 多余字段宽容。
func TestValidateLLMOutput(t *testing.T) {
	fields := []workflowapi.SchemaField{
		{Name: "code", Type: "string", Required: true},
		{Name: "score", Type: "number"},
	}
	tests := []struct {
		name     string
		output   string
		fields   []workflowapi.SchemaField
		wantErr  bool
		contains string
	}{
		{"未声明零校验", "任意文本", nil, false, ""},
		{"合规 JSON 过", `{"code":"ORDER_QUERY"}`, fields, false, ""},
		{"多余字段宽容", `{"code":"ok","extra":1}`, fields, false, ""},
		{"缺必填拒含字段名", `{"score":1}`, fields, true, "code"},
		{"类型不符拒含字段名与期望类型", `{"code":123}`, fields, true, "code"},
		{"可选字段缺失过", `{"code":"ok"}`, fields, false, ""},
		{"纯文本拒", "ORDER_QUERY", fields, true, ""},
		{"null 拒", "null", fields, true, ""},
		{"数组拒", `[1,2]`, fields, true, ""},
		{"markdown 围栏拒（围栏不剥）", "```json\n{\"code\":\"x\"}\n```", fields, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLLMOutput(tt.output, tt.fields)
			if tt.wantErr {
				require.Error(t, err)
				if tt.contains != "" {
					assert.Contains(t, err.Error(), tt.contains)
				}
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestRunNodeLLMOutputSchemaInjection 注入位置与记录实发（FR-006 / SC-005）：strict
// 渲染成功后、setNodeIn 之前追加——stub client 收到的 user 消息以指令全文结尾；
// node_in["prompt"] 与 executions Input 记录追加后的实发文本；system_prompt 不被注入；
// 空数组声明等价未声明（消息逐字节回到 golden 基准）。
func TestRunNodeLLMOutputSchemaInjection(t *testing.T) {
	fields := []workflowapi.SchemaField{{Name: "code", Type: "string", Required: true, Description: "分类码"}}
	directive := buildJSONDirective(fields)

	t.Run("声明非空：user = 渲染后 prompt + 指令，记录即实发", func(t *testing.T) {
		e, _, _, fs, execs, _ := newTestExecutor(okGen(`{"code":"ORDER_QUERY"}`))
		c := newExecContext("查订单")

		out, err := e.runNode(context.Background(), "classify",
			&workflowapi.LLMConfig{ModelID: 3, SystemPrompt: "你是分类器",
				Prompt: "判断意图：{{input}}", OutputSchema: fields}, c)
		require.NoError(t, err)
		assert.Equal(t, `{"code":"ORDER_QUERY"}`, out, "合规回复原样入池")

		msgs := fs.lastMsgs()
		require.Len(t, msgs, 2)
		assert.Equal(t, "你是分类器", msgs[0].Content, "system_prompt 不被注入")
		assert.True(t, strings.HasSuffix(msgs[1].Content, directive), "user 消息以指令全文结尾")
		assert.Equal(t, "判断意图：查订单"+directive, msgs[1].Content, "所见即所发")

		assert.Equal(t, "判断意图：查订单"+directive, c.pendingIn["prompt"], "node_in 记录实发文本")
		require.Len(t, execs.rows, 1)
		assert.Equal(t, "判断意图：查订单"+directive, execs.rows[0].Input["prompt"], "executions Input 记录实发")
	})
	t.Run("空数组等价未声明：消息与 node_in 逐字节回到 golden 基准", func(t *testing.T) {
		e, _, _, fs, execs, _ := newTestExecutor(okGen("ORDER_QUERY"))
		c := newExecContext("查订单")

		_, err := e.runNode(context.Background(), "classify",
			&workflowapi.LLMConfig{ModelID: 3, Prompt: "判断意图：{{input}}",
				OutputSchema: []workflowapi.SchemaField{}}, c)
		require.NoError(t, err)

		msgs := fs.lastMsgs()
		require.Len(t, msgs, 1)
		assert.Equal(t, "判断意图：查订单", msgs[0].Content, "零注入（len 门，非 nil 门）")
		assert.Equal(t, map[string]string{"prompt": "判断意图：查订单"}, c.pendingIn)
		require.Len(t, execs.rows, 1)
		assert.Equal(t, map[string]any{"prompt": "判断意图：查订单"}, execs.rows[0].Input)
	})
}

// TestRunNodeLLMOutputSchemaValidationFail 校验失败 → 节点失败（FR-005 / SC-002）：
// ErrValidationFailed + node 前缀 + 字段名；不落池；executions 行照记且 LLM 调用
// 本身成功（ErrorClass nil——校验失败是图契约层，不是供应商故障）。
func TestRunNodeLLMOutputSchemaValidationFail(t *testing.T) {
	e, _, _, _, execs, _ := newTestExecutor(okGen(`{"score":1}`)) // 缺必填 code
	c := newExecContext("in")

	_, err := e.runNode(context.Background(), "classify",
		&workflowapi.LLMConfig{ModelID: 3, Prompt: "p", OutputSchema: []workflowapi.SchemaField{
			{Name: "code", Type: "string", Required: true}}}, c)
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "校验失败 → 图缺陷 400")
	assert.Contains(t, err.Error(), "node classify:")
	assert.Contains(t, err.Error(), "code", "错误含缺失字段名")
	_, pooled := c.vars["classify"]
	assert.False(t, pooled, "校验失败不落池")

	require.Len(t, execs.rows, 1, "Generate 已成功，executions 照记")
	assert.Nil(t, execs.rows[0].ErrorClass, "LLM 调用本身成功，错误类为空")
}

// TestExecuteNestedChildOutputSchema 嵌套子图内声明节点同注入同校验（SC-005）：
// 子池渲染后追加指令、合规回复过校验、{{c_work.code}} 一级下钻取声明字段。
func TestExecuteNestedChildOutputSchema(t *testing.T) {
	child := childTaskGraph(5, "published",
		`[{"name":"query","type":"string","required":true}]`, "",
		`检索：{{input.query}}`, `{{c_work.code}}`)
	for i, n := range child.nodes {
		if n.NodeKey == "c_work" {
			child.nodes[i].Config = `{"model_id":"3","prompt":"检索：{{input.query}}","output_schema":[{"name":"code","type":"string","required":true}]}`
		}
	}
	parent := parentNestedGraph(3, "chat", "published", 5, `{"query":"{{classify}}"}`, `{{sub}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child},
		twoGen("ORDER_QUERY", `{"code":"子码"}`))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单"})
	require.NoError(t, err)
	assert.Equal(t, "子码", res.Output, "子终稿经 {{c_work.code}} 下钻取声明字段")

	msgs := env.streamer.lastMsgs()
	require.Len(t, msgs, 1, "子图 c_work 无 system：单 user")
	assert.Equal(t, "检索：ORDER_QUERY"+buildJSONDirective([]workflowapi.SchemaField{
		{Name: "code", Type: "string", Required: true}}),
		msgs[0].Content, "子图声明节点同样注入（子池独立渲染后追加）")
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

// ---- spec 08 T014：runNode workflow 分支（inputs 渲染 / JSON 组装 / 子终稿落池 /
// 前缀链 / 子池隔离）+ 结构化 I/O 契约（assembleInputJSON / validateOutputSchema）----

// childCall 一次 executeChild 调用的入参快照。
type childCall struct {
	ctx        context.Context
	workflowID uint64
	fields     map[string]string
	parent     *execContext
}

// childExecStub executeChild 的可编程 stub：按序弹 results / errs（末位驻留），
// 记录全部调用供断言。
type childExecStub struct {
	calls   []childCall
	results []childResult
	errs    []error
}

func (s *childExecStub) call(ctx context.Context, workflowID uint64, fields map[string]string,
	parent *execContext) (childResult, error) {
	s.calls = append(s.calls, childCall{ctx: ctx, workflowID: workflowID, fields: fields, parent: parent})
	i := len(s.calls) - 1
	var res childResult
	if i < len(s.results) {
		res = s.results[i]
	}
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	return res, err
}

// inputs 逐值渲染 strict → 透传 executeChild → 子终稿落父池 node_key（下游模板可引）
// + runID 累积 + 节点入参轨迹（workflow_id 字符串化）。
func TestRunNodeWorkflow(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	stub := &childExecStub{results: []childResult{{output: "子答案", runID: 9}}}
	e.execChild = stub.call
	c := newExecContext("查订单")

	out, err := e.runNode(context.Background(), "sub",
		&workflowapi.WorkflowNodeConfig{WorkflowID: 5, Inputs: map[string]string{"query": "{{input}}"}}, c)
	require.NoError(t, err)
	assert.Equal(t, "子答案", out, "子终稿原样落父变量池")

	require.Len(t, stub.calls, 1)
	assert.Equal(t, uint64(5), stub.calls[0].workflowID)
	assert.Equal(t, map[string]string{"query": "查订单"}, stub.calls[0].fields, "inputs 逐值渲染后透传")
	assert.Same(t, c, stub.calls[0].parent, "父 execContext 直传（深度 / 引用沿此递归）")
	assert.Equal(t, []uint64{9}, c.childRunIDs, "子 run id 累积供父收尾回填")
	inTrace := c.takeNodeIn()
	assert.Equal(t, "5", inTrace["workflow_id"], "轨迹 workflow_id 字符串化")
	assert.Contains(t, inTrace["inputs"], `"query":"查订单"`, "轨迹 inputs 为渲染后映射")
}

// inputs 值渲染缺失变量 → 图缺陷 400 带 inputs.<字段> 定位，零下游调用。
func TestRunNodeWorkflowStrictMissing(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	stub := &childExecStub{}
	e.execChild = stub.call
	c := newExecContext("查订单")

	_, err := e.runNode(context.Background(), "sub",
		&workflowapi.WorkflowNodeConfig{WorkflowID: 5, Inputs: map[string]string{"query": "{{typo}}"}}, c)
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "缺失变量 → 图缺陷 400")
	assert.Contains(t, err.Error(), "inputs.query", "定位到 inputs 字段位")
	assert.Contains(t, err.Error(), "node sub:", "统一 node 前缀包装")
	assert.Empty(t, stub.calls, "渲染先于子调用")
}

// 子图错误经父 runNode 包装点叠加前缀：node outer: node inner:（FR6 可读定位链）。
func TestRunNodeWorkflowErrorPrefixChain(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	stub := &childExecStub{errs: []error{fmt.Errorf("node inner: %w", errs.ErrValidationFailed)}}
	e.execChild = stub.call
	c := newExecContext("in")

	_, err := e.runNode(context.Background(), "outer",
		&workflowapi.WorkflowNodeConfig{WorkflowID: 5, Inputs: map[string]string{"input": "v"}}, c)
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), "node outer: node inner:", "前缀链逐层叠加")
}

// 子执行失败但 run 已落（降级语义保证 runID 照返）→ 仍链接进 childRunIDs（轨迹完整优先）。
func TestRunNodeWorkflowFailedChildRunStillLinked(t *testing.T) {
	e, _, _, _, _, _ := newTestExecutor(okGen("x"))
	stub := &childExecStub{results: []childResult{{runID: 5}}, errs: []error{errors.New("child boom")}}
	e.execChild = stub.call
	c := newExecContext("in")

	_, err := e.runNode(context.Background(), "sub",
		&workflowapi.WorkflowNodeConfig{WorkflowID: 5, Inputs: map[string]string{"input": "v"}}, c)
	require.Error(t, err)
	assert.Equal(t, []uint64{5}, c.childRunIDs, "失败子 run 也参与链接")
}

// assembleInputJSON 按子 input_schema 组装 JSON 文本入参（纯函数表驱动）：
// string 直存 / number / boolean 类型转换 / required 缺失 / 未声明字段 / optional 省略 /
// 无 schema 单一入参回退（原样返回非 JSON 包装）。
func TestAssembleInputJSON(t *testing.T) {
	querySchema := strPtr(`[{"name":"query","type":"string","required":true}]`)
	mixedSchema := strPtr(`[{"name":"query","type":"string","required":true},{"name":"top","type":"number"}]`)
	optionalSchema := strPtr(`[{"name":"query","type":"string","required":true},{"name":"top","type":"number"}]`)
	boolSchema := strPtr(`[{"name":"flag","type":"boolean"}]`)
	twoOptSchema := strPtr(`[{"name":"a","type":"string"},{"name":"b","type":"string"}]`)

	tests := []struct {
		name      string
		fields    map[string]string
		schema    *string
		want      string
		wantErr   bool
		contains  string
	}{
		{"无 schema 恰 {input}：原样返回（非 JSON 包装）", map[string]string{"input": "查订单"}, nil, "查订单", false, ""},
		{"无 schema 键集非 {input} 拒", map[string]string{"query": "x"}, nil, "", true, "input"},
		{"string 字段直存", map[string]string{"query": "查单"}, querySchema, `{"query":"查单"}`, false, ""},
		{"number 字段合法转换", map[string]string{"top": "3.5"}, strPtr(`[{"name":"top","type":"number"}]`), `{"top":3.5}`, false, ""},
		{"number 字段非法拒", map[string]string{"top": "abc"}, strPtr(`[{"name":"top","type":"number"}]`), "", true, "number"},
		{"boolean 字段合法转换", map[string]string{"flag": "true"}, boolSchema, `{"flag":true}`, false, ""},
		{"boolean 字段非法拒", map[string]string{"flag": "yes"}, boolSchema, "", true, "boolean"},
		{"required 缺失拒", map[string]string{"top": "1"}, mixedSchema, "", true, "query"},
		{"optional 缺失省略", map[string]string{"query": "q"}, optionalSchema, `{"query":"q"}`, false, ""},
		{"未声明字段拒", map[string]string{"query": "q", "extra": "x"}, querySchema, "", true, "extra"},
		{"键按字母序输出（可复现）", map[string]string{"b": "2", "a": "1"}, twoOptSchema, `{"a":"1","b":"2"}`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := assembleInputJSON(tt.fields, tt.schema)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.contains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// validateOutputSchema task 型终稿按 output_schema 校验（纯函数表驱动）：
// nil 宽容 / 非 JSON 对象拒（含数组）/ required 缺失 / 三类类型探针 / 类型不符 / 多余宽容。
func TestValidateOutputSchema(t *testing.T) {
	answerSchema := strPtr(`[{"name":"answer","type":"string","required":true}]`)
	tests := []struct {
		name     string
		output   string
		schema   *string
		wantErr  bool
		contains string
	}{
		{"未声明 schema：零校验", "任意文本", nil, false, ""},
		{"非 JSON 对象拒", "子答案", answerSchema, true, "JSON"},
		{"JSON 数组拒（须对象）", `[1,2]`, answerSchema, true, "JSON"},
		{"required 缺失拒", `{"top":1}`, answerSchema, true, "answer"},
		{"string 类型过", `{"answer":"ok"}`, answerSchema, false, ""},
		{"number 类型过", `{"top":3.5}`, strPtr(`[{"name":"top","type":"number"}]`), false, ""},
		{"boolean 类型过", `{"flag":true}`, strPtr(`[{"name":"flag","type":"boolean"}]`), false, ""},
		{"类型不符拒", `{"answer":"txt"}`, strPtr(`[{"name":"answer","type":"number"}]`), true, "类型不符"},
		{"多余字段宽容读取", `{"answer":"ok","extra":1}`, answerSchema, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOutputSchema(tt.output, tt.schema)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.contains)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// ---- Execute 级嵌套端到端（真实执行器 + executeChild 全链）----

// 全链 happy path：classify 渲染进 inputs → 组装 JSON 入参 → 子图池下钻 {{input.query}} →
// 子终稿过 output_schema → 落父池 {{sub.answer}} 下钻 → 子 run 行（trigger/引用/Input）+
// 父 run 行（chat 触发）+ parent_run_id 回填。
func TestExecuteNestedHappyPath(t *testing.T) {
	parent := parentNestedGraph(3, "chat", "published", 5, `{"query":"{{classify}}"}`, `{{sub.answer}}`)
	child := childTaskGraph(5, "published",
		`[{"name":"query","type":"string","required":true}]`,
		`[{"name":"answer","type":"string","required":true}]`,
		`检索：{{input.query}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child},
		twoGen("ORDER_QUERY", `{"answer":"子答案"}`))
	convID := uint64(88)

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{
		ID: 3, Input: "查订单", ConversationID: &convID})
	require.NoError(t, err)
	assert.Equal(t, "子答案", res.Output, "父 end 经 {{sub.answer}} 一级下钻取子终稿字段")

	require.Len(t, env.runs, 2, "子先父后两 run 行")
	childRun := env.runs[0]
	assert.Equal(t, uint64(5), childRun.WorkflowID)
	assert.Equal(t, "workflow", childRun.TriggerSource)
	require.NotNil(t, childRun.ConversationID)
	assert.Equal(t, uint64(88), *childRun.ConversationID, "conversation 引用透传")
	assert.Equal(t, "succeeded", childRun.Status)
	assert.Equal(t, `{"query":"ORDER_QUERY"}`, decodeJSONb(t, childRun.Input)["input"],
		"子 run 入参 = 渲染后按 schema 组装的 JSON 文本")
	parentRun := env.runs[1]
	assert.Equal(t, uint64(3), parentRun.WorkflowID)
	assert.Equal(t, "chat", parentRun.TriggerSource)
	assert.Equal(t, "succeeded", parentRun.Status)

	assert.Equal(t, []backfillCall{{parentRunID: 8, childRunIDs: []uint64{7}}}, env.backfills,
		"父收尾批量回填 parent_run_id")

	var subTrace bool
	for _, nt := range res.NodeTrace {
		if nt.NodeKey == "sub" && nt.NodeType == "workflow" {
			subTrace = true
		}
	}
	assert.True(t, subTrace, "父轨迹含 workflow 类型节点行")
}

// 子池隔离（FR5）：子图模板引用父 vars（classify）→ 运行期 strict 缺失，
// 前缀链 node sub: node c_work:；两 run 行照写（失败轨迹）且回填照常。
func TestExecuteNestedPoolIsolationAndPrefixChain(t *testing.T) {
	parent := parentNestedGraph(3, "chat", "published", 5, `{"input":"{{classify}}"}`, `{{sub}}`)
	child := childTaskGraph(5, "published", "", "", `答案：{{classify}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child}, singleGen("ORDER_QUERY"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单"})
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "子图缺陷 → 400")
	assert.Contains(t, err.Error(), "node sub: node c_work:", "跨层前缀链定位")
	assert.Contains(t, err.Error(), "classify", "报错带缺失变量名")

	require.Len(t, env.runs, 2)
	assert.Equal(t, "failed", env.runs[0].Status)
	assert.Equal(t, "c_work", env.runs[0].ErrorNode, "子 error_node 定位子节点")
	assert.Equal(t, "failed", env.runs[1].Status)
	assert.Equal(t, "sub", env.runs[1].ErrorNode, "父 error_node 定位 sub-workflow 节点")
	assert.Equal(t, []backfillCall{{parentRunID: 8, childRunIDs: []uint64{7}}}, env.backfills,
		"失败子 run 照常链接")
}

// 子终稿 output schema 校验失败（FR9）：声明 schema 而终稿非 JSON → 图缺陷 400 带父前缀；
// 子 run 行 error_node 定位子 end 节点；回填照常。
func TestExecuteNestedChildOutputSchemaFail(t *testing.T) {
	parent := parentNestedGraph(3, "chat", "published", 5, `{"input":"{{classify}}"}`, `{{sub}}`)
	child := childTaskGraph(5, "published", "",
		`[{"name":"answer","type":"string","required":true}]`,
		`工作：{{input}}`, `终稿：{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child}, singleGen("子答案"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单"})
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "output 契约违反 → 图缺陷 400")
	assert.Contains(t, err.Error(), "node sub:")
	assert.Contains(t, err.Error(), "终稿非合法 JSON")

	require.Len(t, env.runs, 2)
	assert.Equal(t, "failed", env.runs[0].Status)
	assert.Equal(t, "c_end", env.runs[0].ErrorNode, "子 error_node = 最后执行节点（output 校验在 walk 后）")
	assert.Equal(t, "sub", env.runs[1].ErrorNode)
	assert.Equal(t, []backfillCall{{parentRunID: 8, childRunIDs: []uint64{7}}}, env.backfills)
}

// chainGraph 深度链环节点：单 workflow 节点引 childID、无出边（隐式终止）。
func chainGraph(id, childID uint64) *graphSnapshot {
	wf := &Workflow{Name: "链", StartNodeKey: "w", Status: "published", Type: "task"}
	wf.ID = id
	nodes := []WorkflowNode{{NodeKey: "w", Type: "workflow",
		Config: fmt.Sprintf(`{"workflow_id":%q,"inputs":{"input":"{{input}}"}}`, strconv.FormatUint(childID, 10))}}
	return &graphSnapshot{wf: wf, nodes: nodes}
}

// 执行期深度兜底（FR4/O4）：1→2→3→4 第 3 层超限（限顶层 + 2 层），
// 深度检查先于 loadGraph（4 号图不存在也不报 404）；三个 run 行照写失败、逐层回填。
func TestExecuteNestedDepthExceeded(t *testing.T) {
	env := newNestedEnv(t, map[uint64]*graphSnapshot{
		1: chainGraph(1, 2), 2: chainGraph(2, 3), 3: chainGraph(3, 4), // 4 不存在
	}, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 1, Input: "in"})
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), "node w: node w: node w:", "三层前缀链")
	assert.Contains(t, err.Error(), "深度")

	require.Len(t, env.runs, 3, "每已执行层一行失败 run（第 4 层零 IO 零行）")
	for i, run := range env.runs {
		assert.Equal(t, "failed", run.Status, "run[%d] 失败", i)
		assert.Equal(t, "w", run.ErrorNode)
	}
	assert.Equal(t, []backfillCall{
		{parentRunID: 8, childRunIDs: []uint64{7}},
		{parentRunID: 9, childRunIDs: []uint64{8}},
	}, env.backfills, "中间层逐层回填")
}
