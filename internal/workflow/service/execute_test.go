package service

// Execute 端到端测试（spec 06 §5 FR1-FR6 + spec 016 tool 节点图级面）：真实执行器
// + 真实 llm.NewClient 包可编程 fake 上游 + stub store / executions / rag——零真实
// PG / LLM / 网络。覆盖：把关节点（published / draft / disabled / trial 例外）、
// 快照游走（线性 / 隐式终止 / 条件分支 / 首条命中 / 无命中 fail-fast）、tool 节点
//（llm→tool→end 正常链路 / condition 按工具输出路由 / 404 透传，spec 016 §7 清单 2/7）、
// 错误分类与失败轨迹照写（ErrorNode 定位）、5min 上限归一 ErrWorkflowExecutionFailed、
// 调用方取消不归一、onNodeDone 回调缝、chat 触发引用。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	mcpapi "github.com/Karlsk/go-hify/internal/mcp/api"
	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	"github.com/Karlsk/go-hify/internal/platform/reactloop"
	"github.com/Karlsk/go-hify/internal/platform/tool"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	"github.com/Karlsk/go-hify/internal/platform/traceid"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// execEnv 一次执行测试的装配：svc 持真实执行器（真 client + fake 上游），store stub
// 三查回图、CreateRun 记录快照（回填 run.ID=7 模拟 RETURNING）。tools 默认空清单
//（tool 节点用例在构造后设 defs / invokeFn——ToolSourceFor 执行期惰性调用）。
type execEnv struct {
	svc      *workflowService
	st       *stubStore
	factory  *stubClientFactory
	streamer *genStreamer
	execs    *execRecorder
	rags     *stubRetrieve
	tools    *stubToolSourceFactory

	createRunCalls int
	lastRun        *WorkflowRun
	lastNodeRuns   []WorkflowNodeRun
}

// newExecEnv 按给定状态与图装配；gen 编排 fake 上游应答（按调用序）。
func newExecEnv(t *testing.T, status string, nodes []WorkflowNode, edges []WorkflowEdge,
	gen func(context.Context, int) (*schema.Message, error)) *execEnv {
	t.Helper()
	env := &execEnv{}
	env.streamer = &genStreamer{gen: gen}
	env.factory = &stubClientFactory{client: llm.NewClient("test", fastProfile(), env.streamer)}
	env.execs = &execRecorder{}
	env.rags = &stubRetrieve{}
	env.tools = &stubToolSourceFactory{}
	env.st = &stubStore{
		getByIDFn: func(id uint64) (*Workflow, error) {
			wf := &Workflow{Name: "智能客服分流", StartNodeKey: nodes[0].NodeKey, Status: status}
			wf.ID = id
			return wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return nodes, nil },
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return edges, nil },
		createRunFn: func(run *WorkflowRun, nodeRuns []WorkflowNodeRun) error {
			env.createRunCalls++
			run.ID = 7 // 模拟 RETURNING 回填
			env.lastRun, env.lastNodeRuns = run, nodeRuns
			return nil
		},
	}
	models := &stubModels{resolveFn: func(req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
		return testResolveCfg(), nil
	}}
	svc := New(env.st, models, nil, &recordCache{}, env.factory, env.execs, env.rags, env.tools, false)
	env.svc = svc.(*workflowService)
	return env
}

// linearGraph 两节点线性图：classify(llm) → finish(end，output 引用 classify)。
func linearGraph() ([]WorkflowNode, []WorkflowEdge) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Name: "意图识别",
			Config: `{"model_id":"3","prompt":"判断意图：{{input}}","temperature":0.7}`},
		{NodeKey: "finish", Type: "end", Config: `{"output":"{{classify}}"}`},
	}
	edges := []WorkflowEdge{{SourceNodeKey: "classify", TargetNodeKey: "finish"}}
	return nodes, edges
}

// singleGen 恒返回既定文本的编排。
func singleGen(s string) func(context.Context, int) (*schema.Message, error) {
	return func(context.Context, int) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, Content: s,
			ResponseMeta: &schema.ResponseMeta{FinishReason: "stop",
				Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5}}}, nil
	}
}

// ---- 正式路径：published 把关 + 线性游走 + 收尾两批写 ----

func TestExecuteLinear(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen("ORDER_QUERY"))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "查订单"})
	require.NoError(t, err)
	assert.Equal(t, "7", res.RunID, "store RETURNING 回填后组转字符串")
	assert.Equal(t, "succeeded", res.Status)
	assert.Equal(t, "ORDER_QUERY", res.Output, "end.output={{classify}} 渲染终稿")
	assert.GreaterOrEqual(t, res.DurationMs, 0)
	require.Len(t, res.NodeTrace, 2)
	assert.Equal(t, "classify", res.NodeTrace[0].NodeKey)
	assert.Equal(t, "llm", res.NodeTrace[0].NodeType)
	assert.Equal(t, "succeeded", res.NodeTrace[0].Status)
	assert.Equal(t, "finish", res.NodeTrace[1].NodeKey)

	// run 行快照：console 触发（无 conversation 引用）、非试运行、无失败节点
	require.Equal(t, 1, env.createRunCalls)
	run := env.lastRun
	assert.Equal(t, uint64(42), run.WorkflowID)
	assert.Equal(t, "智能客服分流", run.WorkflowName, "冗余快照：改名后历史可读")
	assert.Equal(t, "console", run.TriggerSource)
	assert.Nil(t, run.ConversationID)
	assert.False(t, run.IsTrial)
	assert.Equal(t, "succeeded", run.Status)
	assert.Empty(t, run.ErrorNode)
	// runs.input 是 jsonb 对象（db_model §12「执行入参（截断后）」）——原始文本进
	// jsonb 列被 PG 拒收（22P02），真库冒烟 run_id 空串降级即此因
	inM := decodeJSONb(t, run.Input)
	assert.Equal(t, "查订单", inM["input"])
	// 节点轨迹 seq 连续、按执行序
	require.Len(t, env.lastNodeRuns, 2)
	assert.Equal(t, 1, env.lastNodeRuns[0].Seq)
	assert.Equal(t, "classify", env.lastNodeRuns[0].NodeKey)
	assert.Equal(t, 2, env.lastNodeRuns[1].Seq)
	// llm 节点 executions 自记恰好一行（ConversationID=nil）
	require.Len(t, env.execs.rows, 1)
	assert.Nil(t, env.execs.rows[0].ConversationID)
}

// 无出边 = 隐式终止，末节点输出即终稿（FR2）。
func TestExecuteImplicitEnd(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Config: `{"model_id":"3","prompt":"p"}`},
	}
	env := newExecEnv(t, "published", nodes, nil, singleGen("隐式终稿"))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.NoError(t, err)
	assert.Equal(t, "隐式终稿", res.Output)
	assert.Len(t, res.NodeTrace, 1)
	assert.Len(t, env.lastNodeRuns, 1)
}

// ---- 条件分支：求值 → route → 分支节点执行 ----

// condGraph classify → router → order(true)/refund(false) 三叉图；order/refund 均为
// llm 节点。gen 第 1 次答意图（供条件求值），第 2 次答分支文案。
func condGraph() ([]WorkflowNode, []WorkflowEdge) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Config: `{"model_id":"3","prompt":"判断意图：{{input}}"}`},
		{NodeKey: "router", Type: "condition", Config: `{"expression":"{{classify}} == 'ORDER_QUERY'"}`},
		{NodeKey: "order", Type: "llm", Config: `{"model_id":"3","prompt":"订单答复"}`},
		{NodeKey: "refund", Type: "llm", Config: `{"model_id":"3","prompt":"退款答复"}`},
	}
	t, f := "true", "false"
	edges := []WorkflowEdge{
		{SourceNodeKey: "classify", TargetNodeKey: "router"},
		{SourceNodeKey: "router", TargetNodeKey: "order", Condition: &t},
		{SourceNodeKey: "router", TargetNodeKey: "refund", Condition: &f},
	}
	return nodes, edges
}

func twoGen(first, second string) func(context.Context, int) (*schema.Message, error) {
	return func(_ context.Context, call int) (*schema.Message, error) {
		if call == 1 {
			return singleGen(first)(context.Background(), 0)
		}
		return singleGen(second)(context.Background(), 0)
	}
}

func TestExecuteConditionRoute(t *testing.T) {
	nodes, edges := condGraph()
	env := newExecEnv(t, "published", nodes, edges, twoGen("ORDER_QUERY", "订单已发货"))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "查订单"})
	require.NoError(t, err)
	assert.Equal(t, "订单已发货", res.Output)
	require.Len(t, res.NodeTrace, 3)
	assert.Equal(t, "router", res.NodeTrace[1].NodeKey)
	assert.Equal(t, "order", res.NodeTrace[2].NodeKey, "{{classify}}=='ORDER_QUERY' → true 命中 order 边")
	require.Len(t, env.lastNodeRuns, 3)
	assert.Equal(t, []int{1, 2, 3}, []int{env.lastNodeRuns[0].Seq, env.lastNodeRuns[1].Seq, env.lastNodeRuns[2].Seq})
}

func TestExecuteConditionFirstMatch(t *testing.T) {
	nodes, edges := condGraph()
	// 两条出边条件都改 "true"：双双命中取先声明的 order
	tt := "true"
	edges[1].Condition, edges[2].Condition = &tt, &tt
	env := newExecEnv(t, "published", nodes, edges, twoGen("ORDER_QUERY", "首条"))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.NoError(t, err)
	assert.Equal(t, "首条", res.Output)
	assert.Equal(t, "order", res.NodeTrace[len(res.NodeTrace)-1].NodeKey, "声明顺序首条命中")
}

// 无命中出边 = 图缺陷 400，失败 run 照写（ErrorNode=router），已完成节点轨迹保留。
func TestExecuteConditionNoMatch(t *testing.T) {
	nodes, edges := condGraph()
	f := "false"
	edges[1].Condition, edges[2].Condition = &f, &f
	env := newExecEnv(t, "published", nodes, edges, twoGen("ORDER_QUERY", "unreachable"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "图缺陷类 → 400")
	assert.Contains(t, err.Error(), "node router:")

	require.Equal(t, 1, env.createRunCalls, "失败路径也写轨迹（FR7）")
	assert.Equal(t, "failed", env.lastRun.Status)
	assert.Equal(t, "router", env.lastRun.ErrorNode, "错误信息带失败节点 key 定位")
	require.Len(t, env.lastNodeRuns, 2, "classify + router 均已执行成功，失败在游走层")
	assert.Equal(t, "succeeded", env.lastNodeRuns[0].Status)
	assert.Equal(t, "succeeded", env.lastNodeRuns[1].Status)
}

// ---- 运行时错误：图缺陷 400 + 失败节点定位 + 轨迹照写 ----

func TestExecuteMissingVar(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Config: `{"model_id":"3","prompt":"{{typo_key}}"}`},
	}
	env := newExecEnv(t, "published", nodes, nil, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "缺失变量运行时兜底 → 图缺陷 400")
	assert.Contains(t, err.Error(), "node classify:")

	require.Equal(t, 1, env.createRunCalls)
	assert.Equal(t, "failed", env.lastRun.Status)
	assert.Equal(t, "classify", env.lastRun.ErrorNode)
	require.Len(t, env.lastNodeRuns, 1)
	assert.Equal(t, "failed", env.lastNodeRuns[0].Status)
	assert.Empty(t, env.execs.rows, "渲染先于上游调用，未真正打到 LLM")
}

// 工具缺失（Tools 清单空 = 已删 / server 停用静默过滤）：404 哨兵透传 + 节点定位 +
// run failed + error_node（spec 016 §7 清单 2 的图级面；取代 spec 06 占位 fail-fast
// 断言，spec §7 清单 8 授权）。nodeIn 未落（Tools 预检先于渲染与 setNodeIn）。
func TestExecuteToolNotFound(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "t1", Type: "tool", Config: `{"tool_id":"5"}`},
	}
	env := newExecEnv(t, "published", nodes, nil, singleGen("x")) // tools 默认空清单

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)
	assert.ErrorIs(t, err, mcpapi.ErrMCPToolNotFound, "工具缺失 → 404 哨兵透传")
	assert.Contains(t, err.Error(), "node t1:", "节点 key 定位")
	assert.Equal(t, "failed", env.lastRun.Status)
	assert.Equal(t, "t1", env.lastRun.ErrorNode)
	require.Len(t, env.lastNodeRuns, 1)
	assert.Equal(t, "failed", env.lastNodeRuns[0].Status)
	assert.Zero(t, env.tools.invokes, "未起任何调用")
}

// 工具调用失败（Invoke IsError，§7 清单 3 图级面）：同码 500 哨兵 + 错误文本进
// message + run failed + error_node 定位 tool 节点；失败发生在调用之后，nodeIn
// 照落（入参摘要可读）；walk 层失败分支照记 In（execute.go 失败臂）。
func TestExecuteToolCallFailed(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "t1", Type: "tool", Config: `{"tool_id":"5","args":{"q":"{{input}}"}}`},
	}
	env := newExecEnv(t, "published", nodes, nil, singleGen("x"))
	env.tools.defs = []tool.ToolDef{{Name: "s5__search"}}
	env.tools.invokeFn = errToolInvoke("目标服务不可达")

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "查订单"})
	require.Error(t, err)
	assert.ErrorIs(t, err, mcpapi.ErrMCPToolCallFailed, "调用失败 → 500 哨兵")
	assert.Contains(t, err.Error(), "node t1:", "节点 key 定位")
	assert.Contains(t, err.Error(), "目标服务不可达", "错误文本进 message")

	assert.Equal(t, "failed", env.lastRun.Status)
	assert.Equal(t, "t1", env.lastRun.ErrorNode, "error_node 定位到 tool 节点")
	assert.Contains(t, env.lastRun.ErrorMsg, "目标服务不可达", "错误文本落 run 级 ErrorMsg（node 级恒空，既有形态）")
	require.Len(t, env.lastNodeRuns, 1)
	assert.Equal(t, "failed", env.lastNodeRuns[0].Status)
	assert.Empty(t, env.lastNodeRuns[0].ErrorMsg, "node run ErrorMsg 落库恒空（冻结形态）")
	inM := decodeJSONb(t, env.lastNodeRuns[0].Input)
	assert.Equal(t, "5", inM["tool_id"], "调用后失败：入参摘要仍可读")
	assert.Equal(t, `{"q":"查订单"}`, inM["args"])
	require.Len(t, env.tools.invokes, 1)
}

// ---- 图级正常链路（spec 016 §7 清单 7）----

// 图级 llm→tool→end 三节点（spec 016 US1 独立可测形态）：llm 产物渲染进 tool args
// 模板 → 工具 Content 落池 → end 终答模板引用工具输出；tool 节点轨迹 In 记
// tool_id + 渲染后 args、Out 记内容（quickstart §5 场景 2-3 的自动化等价）。
func TestExecuteToolChain(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Config: `{"model_id":"3","prompt":"判断意图：{{input}}"}`},
		{NodeKey: "lookup", Type: "tool", Config: `{"tool_id":"5","args":{"q":"{{classify}}"}}`},
		{NodeKey: "finish", Type: "end", Config: `{"output":"答复：{{lookup}}"}`},
	}
	edges := []WorkflowEdge{
		{SourceNodeKey: "classify", TargetNodeKey: "lookup"},
		{SourceNodeKey: "lookup", TargetNodeKey: "finish"},
	}
	env := newExecEnv(t, "published", nodes, edges, singleGen("ORDER_QUERY"))
	env.tools.defs = []tool.ToolDef{{Name: "s5__search", Description: "查订单"}}
	env.tools.invokeFn = okToolInvoke("ORDER-123 已发货")

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "查订单"})
	require.NoError(t, err)
	assert.Equal(t, "答复：ORDER-123 已发货", res.Output, "end 模板引用工具输出，终答含工具取回的数据")

	require.Len(t, res.NodeTrace, 3)
	assert.Equal(t, "lookup", res.NodeTrace[1].NodeKey)
	assert.Equal(t, "tool", res.NodeTrace[1].NodeType)
	assert.Equal(t, "succeeded", res.NodeTrace[1].Status, "tool 节点 succeeded")
	assert.Equal(t, "succeeded", res.NodeTrace[2].Status)

	// 工具收到单次确定性调用：args 为 llm 产物渲染后的按 key 排序 JSON
	require.Len(t, env.tools.invokes, 1)
	assert.Equal(t, "s5__search", env.tools.invokes[0].Name, "调用名 = Source 供出的派生名")
	assert.Equal(t, `{"q":"ORDER_QUERY"}`, string(env.tools.invokes[0].Arguments))

	// node_runs：tool 步 In 记 tool_id + args、Out 记内容、DurationMs 落值
	require.Len(t, env.lastNodeRuns, 3)
	toolRun := env.lastNodeRuns[1]
	assert.Equal(t, "lookup", toolRun.NodeKey)
	assert.Equal(t, "succeeded", toolRun.Status)
	inM := decodeJSONb(t, toolRun.Input)
	assert.Equal(t, "5", inM["tool_id"])
	assert.Equal(t, `{"q":"ORDER_QUERY"}`, inM["args"])
	outM := decodeJSONb(t, toolRun.Output)
	assert.Equal(t, "ORDER-123 已发货", outM["output"])
	assert.GreaterOrEqual(t, toolRun.DurationMs, 0, "耗时照记")
	assert.Empty(t, toolRun.ErrorMsg)

	// executions 只有 classify 一行（tool 非 LLM 调用不落，spec 016 边界）
	require.Len(t, env.execs.rows, 1)
}

// condition 节点按 tool 输出路由二选一（§7 清单 7 后半）：工具结果 "IN_STOCK" 命中
// true 边走 ship，soldout 分支不执行——工具输出即条件求值的一等变量。
func TestExecuteToolConditionRoute(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "lookup", Type: "tool", Config: `{"tool_id":"5","args":{"q":"{{input}}"}}`},
		{NodeKey: "router", Type: "condition", Config: `{"expression":"{{lookup}} == 'IN_STOCK'"}`},
		{NodeKey: "ship", Type: "llm", Config: `{"model_id":"3","prompt":"发货答复"}`},
		{NodeKey: "soldout", Type: "llm", Config: `{"model_id":"3","prompt":"缺货答复"}`},
	}
	tt, ff := "true", "false"
	edges := []WorkflowEdge{
		{SourceNodeKey: "lookup", TargetNodeKey: "router"},
		{SourceNodeKey: "router", TargetNodeKey: "ship", Condition: &tt},
		{SourceNodeKey: "router", TargetNodeKey: "soldout", Condition: &ff},
	}
	env := newExecEnv(t, "published", nodes, edges, singleGen("明天送达"))
	env.tools.defs = []tool.ToolDef{{Name: "s5__stock", Description: "查库存"}}
	env.tools.invokeFn = okToolInvoke("IN_STOCK")

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "查库存"})
	require.NoError(t, err)
	assert.Equal(t, "明天送达", res.Output, "隐式终止：末节点输出即终稿")
	require.Len(t, res.NodeTrace, 3, "lookup + router + ship，soldout 未执行")
	assert.Equal(t, "lookup", res.NodeTrace[0].NodeKey)
	assert.Equal(t, "router", res.NodeTrace[1].NodeKey)
	assert.Equal(t, "ship", res.NodeTrace[2].NodeKey, "工具输出 IN_STOCK 命中 true 边")
	assert.Equal(t, 1, env.streamer.calls, "只有 ship 分支打到 LLM")
}

// ---- 把关节点：published 才放行 / draft / disabled 文案区分 / trial 例外 ----

func TestExecuteNotFound(t *testing.T) {
	env := newExecEnv(t, "published", nil, nil, singleGen("x"))
	env.st.getByIDFn = func(id uint64) (*Workflow, error) { return nil, gorm.ErrRecordNotFound }

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 999, Input: "in"})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotFound)
	assert.Zero(t, env.createRunCalls, "快照都没加载到，不写轨迹")
}

func TestExecuteDraftBlocked(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "draft", nodes, edges, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotPublished, "draft 正式执行 → 503")
	assert.Contains(t, err.Error(), "draft", "文案区分两态")
	assert.Zero(t, env.createRunCalls, "把关失败不执行不落轨迹")
	assert.Zero(t, env.streamer.calls)
}

func TestExecuteDisabledBlocked(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "disabled", nodes, edges, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowNotPublished, "disabled 正式执行 → 503")
	assert.Contains(t, err.Error(), "disabled", "文案区分两态")
}

// trial 唯一例外：draft + trial=true 直接执行（作者调试半成品，FR1/O3）。
func TestExecuteTrial(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "draft", nodes, edges, singleGen("试运行结果"))

	res, err := env.svc.Execute(context.Background(),
		workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in", Trial: true})
	require.NoError(t, err)
	assert.Equal(t, "试运行结果", res.Output)
	assert.True(t, env.lastRun.IsTrial, "试运行标记与真实流量可区分")
}

// trial 对 disabled 同样放开（O3 覆盖全部非 published 态）。
func TestExecuteTrialDisabled(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "disabled", nodes, edges, singleGen("停用态试运行"))

	res, err := env.svc.Execute(context.Background(),
		workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in", Trial: true})
	require.NoError(t, err)
	assert.Equal(t, "停用态试运行", res.Output)
	assert.True(t, env.lastRun.IsTrial)
}

// published + trial：正常执行且 is_trial 照记（正式图也可调试，标记区分流量）。
func TestExecuteTrialPublished(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen("正式图试运行"))

	res, err := env.svc.Execute(context.Background(),
		workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in", Trial: true})
	require.NoError(t, err)
	assert.Equal(t, "正式图试运行", res.Output)
	assert.True(t, env.lastRun.IsTrial, "published 下 trial 也落 is_trial=true")
}

// ---- 5min 上限（O5）与调用方取消的语义二分 ----

func TestExecuteTimeout(t *testing.T) {
	nodes, edges := linearGraph()
	old := workflowTimeout
	workflowTimeout = 40 * time.Millisecond
	t.Cleanup(func() { workflowTimeout = old })

	blockGen := func(ctx context.Context, _ int) (*schema.Message, error) {
		<-ctx.Done() // 模拟上游慢：等到 ctx 到期
		return nil, ctx.Err()
	}
	env := newExecEnv(t, "published", nodes, edges, blockGen)

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)
	assert.ErrorIs(t, err, workflowapi.ErrWorkflowExecutionFailed, "总时长超限 → 环境限制 500（O4）")
	assert.Equal(t, "classify", env.lastRun.ErrorNode)
	assert.Equal(t, "failed", env.lastRun.Status)
}

// 调用方取消（如客户端断连）：不归一为 WORKFLOW_EXECUTION_FAILED，原样带节点前缀上抛。
func TestExecuteCtxCancel(t *testing.T) {
	nodes, edges := linearGraph()
	ctx, cancel := context.WithCancel(context.Background())
	cancelGen := func(context.Context, int) (*schema.Message, error) {
		cancel()
		return nil, context.Canceled
	}
	env := newExecEnv(t, "published", nodes, edges, cancelGen)

	_, err := env.svc.Execute(ctx, workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, workflowapi.ErrWorkflowExecutionFailed, "调用方取消 ≠ 环境限制")
	assert.Contains(t, err.Error(), "node classify:")
	assert.Equal(t, "failed", env.lastRun.Status, "取消也照写失败轨迹")
}

// ---- onNodeDone 回调缝（FR9：nil = 零开销，非 nil 按执行序回调）----

func TestExecuteOnNodeDone(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen("ORDER_QUERY"))

	var got [][2]string
	env.svc.onNodeDone = func(nodeKey, output string) { got = append(got, [2]string{nodeKey, output}) }

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.NoError(t, err)
	assert.Equal(t, [][2]string{{"classify", "ORDER_QUERY"}, {"finish", "ORDER_QUERY"}},
		got, "每节点成功后按执行序回调")
}

// ---- chat 触发引用（进程内调用方，FR1 下游消费者）----

func TestExecuteConversationRef(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen("x"))
	convID := uint64(88)

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{
		ID: 42, Input: "in", ConversationID: &convID})
	require.NoError(t, err)
	require.NotNil(t, env.lastRun.ConversationID)
	assert.Equal(t, uint64(88), *env.lastRun.ConversationID, "弱引用落 run 行")
	assert.Equal(t, "chat", env.lastRun.TriggerSource)
}

// ---- 写入降级（O7 ③）：CreateRun 持续失败 → 重试恰好一次、结果照返、RunID 置空、
// ERROR 日志带 trace_id ----

func TestExecuteCreateRunDegrade(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen("ORDER_QUERY"))
	env.st.createRunFn = func(run *WorkflowRun, nodeRuns []WorkflowNodeRun) error {
		env.createRunCalls++
		return assert.AnError
	}

	cap := &logCapture{}
	old := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(old) })

	ctx := traceid.With(context.Background(), "trace-degrade-1")
	res, err := env.svc.Execute(ctx, workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.NoError(t, err, "轨迹写入失败不阻断结果返回")
	assert.Equal(t, "ORDER_QUERY", res.Output, "结果照返")
	assert.Empty(t, res.RunID, "降级：RunID 置空")
	assert.Equal(t, 2, env.createRunCalls, "重试恰好一次后放弃")

	var errRec *slog.Record
	for i := range cap.records {
		if cap.records[i].Level == slog.LevelError {
			errRec = &cap.records[i]
			break
		}
	}
	require.NotNil(t, errRec, "降级走 ERROR 日志")
	gotTrace, ok := attrString(*errRec, "trace_id")
	require.True(t, ok, "ERROR 日志带 trace_id（对账 slog 与既有线索）")
	assert.Equal(t, "trace-degrade-1", gotTrace)
}

// ---- spec 08 T013：executeChild（递归执行 / 子把关 / 子 run 行 / 深度兜底）----

// graphSnapshot 一张已存图的完整快照（wf + 节点 + 边），newNestedEnv 按 id dispatch。
type graphSnapshot struct {
	wf    *Workflow
	nodes []WorkflowNode
	edges []WorkflowEdge
}

// backfillCall 一次 parent_run_id 回填的入参快照。
type backfillCall struct {
	parentRunID uint64
	childRunIDs []uint64
}

// nestedEnv 嵌套执行测试装配（spec 08）：多图 store dispatch（未命中 404）+ run 落库
// 记录（子先父后：子 run 在父 walk 内写完才轮到父收尾，id 从 7 起按序分配）+
// parent_run_id 回填记录。既有 newExecEnv 的 Workflow fixture 无 Type / schema 字段且
// CreateRun 硬编码 run.ID=7，嵌套场景需要本套独立装配。
type nestedEnv struct {
	svc      *workflowService
	st       *stubStore
	factory  *stubClientFactory
	streamer *genStreamer
	execs    *execRecorder
	rags     *stubRetrieve

	createRunCalls int
	runs           []*WorkflowRun
	backfills      []backfillCall
	nextRunID      uint64
}

// newNestedEnv 按 graphs 构建 dispatch store 与全量服务（真实执行器 + fake 上游）。
func newNestedEnv(t *testing.T, graphs map[uint64]*graphSnapshot,
	gen func(context.Context, int) (*schema.Message, error)) *nestedEnv {
	t.Helper()
	env := &nestedEnv{nextRunID: 7}
	env.streamer = &genStreamer{gen: gen}
	env.factory = &stubClientFactory{client: llm.NewClient("test", fastProfile(), env.streamer)}
	env.execs = &execRecorder{}
	env.rags = &stubRetrieve{}
	env.st = &stubStore{
		getByIDFn: func(id uint64) (*Workflow, error) {
			g, ok := graphs[id]
			if !ok {
				return nil, gorm.ErrRecordNotFound
			}
			return g.wf, nil
		},
		listNodesFn: func(workflowID uint64) ([]WorkflowNode, error) { return graphs[workflowID].nodes, nil },
		listEdgesFn: func(workflowID uint64) ([]WorkflowEdge, error) { return graphs[workflowID].edges, nil },
		createRunFn: func(run *WorkflowRun, nodeRuns []WorkflowNodeRun) error {
			env.createRunCalls++
			run.ID = env.nextRunID // 模拟 RETURNING 回填
			env.nextRunID++
			env.runs = append(env.runs, run)
			return nil
		},
		updateParentRunIDsFn: func(parentRunID uint64, childRunIDs []uint64) error {
			env.backfills = append(env.backfills, backfillCall{parentRunID: parentRunID, childRunIDs: childRunIDs})
			return nil
		},
	}
	models := &stubModels{resolveFn: func(req providerapi.ResolveLLMConfigReq) (*providerapi.LLMConfig, error) {
		return testResolveCfg(), nil
	}}
	env.svc = New(env.st, models, nil, &recordCache{}, env.factory, env.execs, env.rags, nil, false).(*workflowService) // 嵌套图无 tool 节点：工具来源不接线
	return env
}

// parentNestedGraph 嵌套父图：classify(llm) → sub(workflow，引 childID，inputs 为
// raw JSON 映射文本) → finish(end，output 引 sub 终稿)。
func parentNestedGraph(id uint64, wfType, status string, childID uint64, inputs, endOutput string) *graphSnapshot {
	wf := &Workflow{Name: "父图", StartNodeKey: "classify", Status: status, Type: wfType}
	wf.ID = id
	nodeCfg := fmt.Sprintf(`{"workflow_id":%q,"inputs":%s}`, strconv.FormatUint(childID, 10), inputs)
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Name: "意图识别", Config: `{"model_id":"3","prompt":"判断意图：{{input}}"}`},
		{NodeKey: "sub", Type: "workflow", Config: nodeCfg},
		{NodeKey: "finish", Type: "end", Config: fmt.Sprintf(`{"output":%q}`, endOutput)},
	}
	edges := []WorkflowEdge{
		{SourceNodeKey: "classify", TargetNodeKey: "sub"},
		{SourceNodeKey: "sub", TargetNodeKey: "finish"},
	}
	return &graphSnapshot{wf: wf, nodes: nodes, edges: edges}
}

// childTaskGraph 嵌套子图（task 型）：c_work(llm) → c_end(end)。schema 空串 = 未声明
// → NULL（单一入参回退）。
func childTaskGraph(id uint64, status, inputSchema, outputSchema, promptTpl, outputTpl string) *graphSnapshot {
	wf := &Workflow{Name: "子任务", StartNodeKey: "c_work", Status: status, Type: "task"}
	wf.ID = id
	if inputSchema != "" {
		wf.InputSchema = strPtr(inputSchema)
	}
	if outputSchema != "" {
		wf.OutputSchema = strPtr(outputSchema)
	}
	nodes := []WorkflowNode{
		{NodeKey: "c_work", Type: "llm", Config: fmt.Sprintf(`{"model_id":"3","prompt":%q}`, promptTpl)},
		{NodeKey: "c_end", Type: "end", Config: fmt.Sprintf(`{"output":%q}`, outputTpl)},
	}
	edges := []WorkflowEdge{{SourceNodeKey: "c_work", TargetNodeKey: "c_end"}}
	return &graphSnapshot{wf: wf, nodes: nodes, edges: edges}
}

// 子 run 行形态（O5 Option A）：trigger_source='workflow'、引用透传、trial 跟随、
// Input 为按子 input_schema 组装的 JSON 文本（wrapTraceJSON 包 {"input": ...}）。
func TestExecuteChildTransfersRefs(t *testing.T) {
	child := childTaskGraph(5, "published",
		`[{"name":"query","type":"string","required":true}]`, "",
		`检索：{{input.query}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{5: child}, singleGen("子答案"))

	parent := newExecContext("in")
	parent.trial = true
	convID, msgID := uint64(88), uint64(99)
	parent.conversationID, parent.messageID = &convID, &msgID

	res, err := env.svc.executeChild(context.Background(), 5, map[string]string{"query": "查单"}, parent)
	require.NoError(t, err)
	assert.Equal(t, "子答案", res.output, "子终稿原样返回")
	assert.Equal(t, uint64(7), res.runID)

	require.Len(t, env.runs, 1)
	run := env.runs[0]
	assert.Equal(t, uint64(5), run.WorkflowID)
	assert.Equal(t, "workflow", run.TriggerSource)
	assert.True(t, run.IsTrial, "trial 跟随父")
	require.NotNil(t, run.ConversationID)
	assert.Equal(t, uint64(88), *run.ConversationID)
	require.NotNil(t, run.MessageID)
	assert.Equal(t, uint64(99), *run.MessageID)
	assert.Equal(t, "succeeded", run.Status)
	inObj := decodeJSONb(t, run.Input)
	assert.Equal(t, `{"query":"查单"}`, inObj["input"], "组装入参按 schema 转 JSON 文本")
	assert.Empty(t, env.backfills, "子无孙 run：不回填")
}

// 深度兜底（FR4）：父 depth 已达上限 → 图缺陷 400，零 IO 零轨迹（先于 loadGraph）。
func TestExecuteChildDepthExceededNoStore(t *testing.T) {
	env := newNestedEnv(t, map[uint64]*graphSnapshot{}, singleGen("x"))
	parent := newExecContext("in")
	parent.depth = maxNestLevel

	_, err := env.svc.executeChild(context.Background(), 5, map[string]string{"input": "v"}, parent)
	require.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), "深度")
	assert.Empty(t, env.st.seq, "超限即拒：不触任何 store 调用（无 run 行）")
}

// 引用已删图（FR10）：loadGraph 404 → ErrWorkflowNotPublished 之前的 NOT_FOUND 透传。
func TestExecuteChildNotFound(t *testing.T) {
	env := newNestedEnv(t, map[uint64]*graphSnapshot{}, singleGen("x"))
	parent := newExecContext("in")

	_, err := env.svc.executeChild(context.Background(), 5, map[string]string{"input": "v"}, parent)
	require.ErrorIs(t, err, workflowapi.ErrWorkflowNotFound)
	assert.Equal(t, 0, env.createRunCalls, "无子 run 行")
}

// 子 run 写失败降级：重试恰好一次，runID=0 照返（调用方继续消费子终稿）。
func TestExecuteChildCreateRunDegrade(t *testing.T) {
	child := childTaskGraph(5, "published", "", "", `工作：{{input}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{5: child}, singleGen("子答案"))
	env.st.createRunFn = func(run *WorkflowRun, nodeRuns []WorkflowNodeRun) error {
		env.createRunCalls++
		return assert.AnError
	}
	parent := newExecContext("in")

	res, err := env.svc.executeChild(context.Background(), 5, map[string]string{"input": "v"}, parent)
	require.NoError(t, err, "写入降级不影响业务返回")
	assert.Equal(t, "子答案", res.output)
	assert.Equal(t, uint64(0), res.runID, "降级：runID=0（不参与父回填）")
	assert.Equal(t, 2, env.createRunCalls, "重试恰好一次后放弃")
	assert.Empty(t, env.backfills)
}

// 正式运行子 draft 拒（FR7）：executeChild 直接调用——哨兵原样（父节点前缀由
// executor 的 runNode 包装点加）。
func TestExecuteChildFormalDraftBlocked(t *testing.T) {
	child := childTaskGraph(5, "draft", "", "", `工作：{{input}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{5: child}, singleGen("x"))
	parent := newExecContext("in")

	_, err := env.svc.executeChild(context.Background(), 5, map[string]string{"input": "v"}, parent)
	require.ErrorIs(t, err, workflowapi.ErrWorkflowNotPublished)
	assert.Contains(t, err.Error(), "draft")
	assert.Equal(t, 0, env.createRunCalls, "把关失败零 run 行")
}

// Execute 级：trial 跟随——父 trial 放开子 draft（FR7），子 run IsTrial 透传。
func TestExecuteNestedTrialFollowsChildDraft(t *testing.T) {
	parent := parentNestedGraph(3, "chat", "published", 5, `{"input":"{{classify}}"}`, `{{sub}}`)
	child := childTaskGraph(5, "draft", "", "", `工作：{{input}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child}, singleGen("子答案"))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单", Trial: true})
	require.NoError(t, err, "父试运行放开子 draft")
	assert.Equal(t, "子答案", res.Output)

	require.Len(t, env.runs, 2)
	assert.True(t, env.runs[0].IsTrial, "子 run trial 跟随父")
	assert.Equal(t, "workflow", env.runs[0].TriggerSource)
	assert.True(t, env.runs[1].IsTrial, "父 run 自身 trial")
	assert.Len(t, env.backfills, 1)
}

// Execute 级：正式运行子 draft 拒——哨兵带父 node 前缀，子零 run 行，父写失败行，
// 无回填（无子 run 可链）。
func TestExecuteNestedFormalChildDraftBlocked(t *testing.T) {
	parent := parentNestedGraph(3, "chat", "published", 5, `{"input":"{{classify}}"}`, `{{sub}}`)
	child := childTaskGraph(5, "draft", "", "", `工作：{{input}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child}, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单"})
	require.ErrorIs(t, err, workflowapi.ErrWorkflowNotPublished)
	assert.Contains(t, err.Error(), "node sub:", "哨兵带父 node 前缀（FR6）")
	assert.Contains(t, err.Error(), "draft")

	require.Len(t, env.runs, 1, "子把关失败零 run 行、父写失败行")
	assert.Equal(t, "sub", env.runs[0].ErrorNode, "父 error_node 定位到 sub-workflow 节点")
	assert.Equal(t, "failed", env.runs[0].Status)
	assert.Empty(t, env.backfills)
}

// 父 run 写失败跳过回填（O5）：子 run 已落（id=7）但父行写败两次 → RunID 空、
// 不发 UpdateParentRunIDs（trace_id 兜底走既有 ERROR 日志路径）。
func TestExecuteNestedParentWriteFailSkipsBackfill(t *testing.T) {
	parent := parentNestedGraph(3, "chat", "published", 5, `{"input":"{{classify}}"}`, `{{sub}}`)
	child := childTaskGraph(5, "published", "", "", `工作：{{input}}`, `{{c_work}}`)
	env := newNestedEnv(t, map[uint64]*graphSnapshot{3: parent, 5: child}, singleGen("子答案"))
	env.st.createRunFn = func(run *WorkflowRun, nodeRuns []WorkflowNodeRun) error {
		env.createRunCalls++
		if run.WorkflowID == 3 { // 父行写败；子行照常
			return assert.AnError
		}
		run.ID = env.nextRunID
		env.nextRunID++
		env.runs = append(env.runs, run)
		return nil
	}

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 3, Input: "查订单"})
	require.NoError(t, err, "父写失败降级不影响业务返回")
	assert.Equal(t, "子答案", res.Output)
	assert.Empty(t, res.RunID)
	assert.Equal(t, 3, env.createRunCalls, "子 1 次 + 父重试 1 次")
	assert.Empty(t, env.backfills, "父行未落库 → 跳过回填")
}

// ---- spec 018：顶层入参归一化与 optional 默认值（FR-007 / FR-008）----

// TestNormalizeInputJSON 纯函数表驱动（FR-008 / R5）：合法 JSON 对象补缺失 optional
// 默认后重序列化（已有字段原文不动）；非合法 JSON / 非对象 / 无可补 → 原文返回
//（只补默认不新增拒绝，D5 边界）；required 缺失不补（保存期已拒 required+default，
// 运行期防御同守）。
func TestNormalizeInputJSON(t *testing.T) {
	schema := []workflowapi.SchemaField{
		{Name: "q", Type: "string", Required: true},
		{Name: "limit", Type: "number", Default: json.RawMessage(`10`)},
		{Name: "verbose", Type: "boolean", Default: json.RawMessage(`false`)},
		{Name: "note", Type: "string", Default: json.RawMessage(`"n"`)},
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"缺失带默认 optional 补全", `{"q":"查"}`, `{"limit":10,"note":"n","q":"查","verbose":false}`},
		{"字段已在场不覆盖", `{"q":"查","limit":5}`, `{"limit":5,"note":"n","q":"查","verbose":false}`},
		{"未声明字段带不管", `{"q":"查","extra":1}`, `{"extra":1,"limit":10,"note":"n","q":"查","verbose":false}`},
		{"非合法 JSON 原样返回", `纯文本入参`, `纯文本入参`},
		{"数组原样返回", `[1,2]`, `[1,2]`},
		{"数字原样返回", `42`, `42`},
		{"带引号字符串原样返回", `"文本"`, `"文本"`},
		{"null 原样返回", `null`, `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeInputJSON(tt.input, schema))
		})
	}
	t.Run("空 schema 原样返回", func(t *testing.T) {
		assert.Equal(t, `{"q":"查"}`, normalizeInputJSON(`{"q":"查"}`, nil))
	})
	t.Run("required 缺失不补（防御）", func(t *testing.T) {
		got := normalizeInputJSON(`{}`, []workflowapi.SchemaField{
			{Name: "q", Type: "string", Required: true, Default: json.RawMessage(`"兜底"`)},
		})
		assert.Equal(t, `{}`, got, "required 字段的 default 不生效（保存期已拒配置）")
	})
}

// TestExecuteTaskInputDefault 顶层 task 型声明 schema：input 为合法 JSON 对象且缺
// 带默认 optional 字段 → 归一化补默认进变量池（{{input.limit}} 渲染 10），run 轨迹
// 记归一化后 input（实际执行值可追溯）。
func TestExecuteTaskInputDefault(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Name: "带默认",
			Config: `{"model_id":"3","prompt":"limit={{input.limit}}"}`},
		{NodeKey: "finish", Type: "end", Config: `{"output":"{{classify}}"}`},
	}
	edges := []WorkflowEdge{{SourceNodeKey: "classify", TargetNodeKey: "finish"}}
	env := newExecEnv(t, "published", nodes, edges, singleGen("ok"))
	env.st.getByIDFn = func(id uint64) (*Workflow, error) {
		wf := &Workflow{Name: "task 默认值", StartNodeKey: "classify", Status: "published", Type: "task",
			InputSchema: strPtr(`[{"name":"q","type":"string","required":true},{"name":"limit","type":"number","default":10}]`)}
		wf.ID = id
		return wf, nil
	}

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: `{"q":"查"}`})
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Output)
	require.Len(t, env.lastNodeRuns, 2)
	assert.Contains(t, env.lastNodeRuns[0].Input, `"prompt":"limit=10"`, "模板按补默认后的 input 渲染")
	assert.Contains(t, env.lastRun.Input, `\"limit\":10`, "run 轨迹记归一化后 input（wrapTraceJSON 内嵌转义形态）")
}

// TestExecuteTaskInputNonJSONPassthrough task 型声明 schema 但 input 为非 JSON 纯文本 →
// 归一化放行原文（只补默认不新增拒绝），{{input}} 全文渲染、执行正常。
func TestExecuteTaskInputNonJSONPassthrough(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Name: "纯文本",
			Config: `{"model_id":"3","prompt":"x={{input}}"}`},
		{NodeKey: "finish", Type: "end", Config: `{"output":"{{classify}}"}`},
	}
	edges := []WorkflowEdge{{SourceNodeKey: "classify", TargetNodeKey: "finish"}}
	env := newExecEnv(t, "published", nodes, edges, singleGen("ok"))
	env.st.getByIDFn = func(id uint64) (*Workflow, error) {
		wf := &Workflow{Name: "纯文本放行", StartNodeKey: "classify", Status: "published", Type: "task",
			InputSchema: strPtr(`[{"name":"q","type":"string","required":true}]`)}
		wf.ID = id
		return wf, nil
	}

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: `纯文本入参`})
	require.NoError(t, err, "非 JSON input 照旧放行不新增拒绝")
	assert.Equal(t, "ok", res.Output)
	require.Len(t, env.lastNodeRuns, 2)
	assert.Contains(t, env.lastNodeRuns[0].Input, `"prompt":"x=纯文本入参"`, "全文渲染不受归一化影响")
}

// ---- spec 020：react 节点图级（FR-003 / C2 / C3）----

// reactGraph 单 react 节点 → end 引用其终答的最小图。
func reactGraph(cfg string) ([]WorkflowNode, []WorkflowEdge) {
	nodes := []WorkflowNode{
		{NodeKey: "agent", Type: "react", Name: "自主", Config: cfg},
		{NodeKey: "finish", Type: "end", Config: `{"output":"{{agent}}"}`},
	}
	edges := []WorkflowEdge{{SourceNodeKey: "agent", TargetNodeKey: "finish"}}
	return nodes, edges
}

func reactToolCallMsg() *schema.Message {
	return &schema.Message{Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "s1__weather", Arguments: `{"city":"北京"}`}}},
		ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls",
			Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 0}}}
}

func reactFinalMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content,
		ResponseMeta: &schema.ResponseMeta{FinishReason: "stop",
			Usage: &schema.TokenUsage{PromptTokens: 8, CompletionTokens: 4}}}
}

// ① 两轮工具循环：终答落池、并集请求透传、nodeIn tool_chain、executions 逐轮。
func TestExecuteReactToolRounds(t *testing.T) {
	nodes, edges := reactGraph(
		`{"model_id":"3","prompt":"任务：{{input}}","server_ids":[1],"tool_ids":[7],"max_rounds":4}`)
	env := newExecEnv(t, "published", nodes, edges,
		func(_ context.Context, call int) (*schema.Message, error) {
			if call == 1 {
				return reactToolCallMsg(), nil
			}
			return reactFinalMsg("终答"), nil
		})
	env.tools.defs = []tool.ToolDef{{Name: "s1__weather", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	env.tools.invokeFn = okToolInvoke("晴")

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: "天气"})
	require.NoError(t, err)
	assert.Equal(t, "终答", res.Output, "react 终答经 end 模板落终稿")

	// 并集绑定透传 mcp 请求面
	assert.Equal(t, []uint64{7}, env.tools.lastReq.ToolIDs)
	assert.Equal(t, []uint64{1}, env.tools.lastReq.ServerIDs)
	assert.Len(t, env.tools.invokes, 1)

	// node_runs：react 单行，input 含 tool_chain 与轮数
	require.Len(t, env.lastNodeRuns, 2)
	reactIn := env.lastNodeRuns[0].Input
	assert.Contains(t, reactIn, `"tool_chain"`)
	assert.Contains(t, reactIn, "s1__weather")
	assert.Contains(t, reactIn, `"rounds":"2"`, "nodeIn 值字符串化形态")
	assert.Contains(t, reactIn, `"max_rounds":"4"`)
	assert.Contains(t, env.lastNodeRuns[0].Output, `"output":"终答"`, "Output 走 wrapTraceJSON 包装")

	// executions 逐轮两行：ConversationID=nil（workflow 标识）+ Input 含 round/tool_calls
	require.Len(t, env.execs.rows, 2)
	assert.Nil(t, env.execs.rows[0].ConversationID)
	assert.Equal(t, 0, env.execs.rows[0].Input["round"])
	assert.Equal(t, 1, env.execs.rows[0].Input["tool_calls"])
	assert.Equal(t, 1, env.execs.rows[1].Input["round"])
	assert.Equal(t, "终答", env.execs.rows[1].Output["content"])
}

// ② 绑定全空 = 纯推理单轮：零 mcp 交互。
func TestExecuteReactPureReasoning(t *testing.T) {
	nodes, edges := reactGraph(`{"model_id":"3","prompt":"推理：{{input}}"}`)
	env := newExecEnv(t, "published", nodes, edges,
		func(_ context.Context, call int) (*schema.Message, error) {
			return reactFinalMsg("纯推理终答"), nil
		})

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: "q"})
	require.NoError(t, err)
	assert.Equal(t, "纯推理终答", res.Output)
	assert.Equal(t, 0, env.tools.calls, "绑定全空零 mcp 交互")
	assert.Equal(t, 1, env.streamer.calls, "单轮直答")
}

// ③ 并集过滤后空清单 → ErrMCPToolNotFound fail-fast（绑定即意图，不静默降级）。
func TestExecuteReactEmptyToolSetFailFast(t *testing.T) {
	nodes, edges := reactGraph(`{"model_id":"3","prompt":"{{input}}","server_ids":[5]}`)
	env := newExecEnv(t, "published", nodes, edges,
		func(_ context.Context, call int) (*schema.Message, error) {
			return reactFinalMsg("不该到达"), nil
		})
	env.tools.defs = nil // server 停用 / 工具删 → 过滤后空清单

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: "q"})
	require.ErrorIs(t, err, mcpapi.ErrMCPToolNotFound)
	assert.Equal(t, "failed", res.Status)
	assert.Equal(t, "agent", res.NodeTrace[0].NodeKey)
	assert.Equal(t, 0, env.streamer.calls, "fail-fast 先于 LLM 调用")
}

// ④ max_rounds=1 透传：共 2 次调用（1 工具轮 + 第 2 轮 nudge 收尾摘工具）。
func TestExecuteReactMaxRounds(t *testing.T) {
	nodes, edges := reactGraph(`{"model_id":"3","prompt":"{{input}}","tool_ids":[7],"max_rounds":1}`)
	env := newExecEnv(t, "published", nodes, edges,
		func(_ context.Context, call int) (*schema.Message, error) {
			if call == 1 {
				return reactToolCallMsg(), nil
			}
			return reactFinalMsg("收尾终答"), nil // 上限轮模型仍幻觉调用也强制收尾
		})
	env.tools.defs = []tool.ToolDef{{Name: "s1__weather", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	env.tools.invokeFn = okToolInvoke("晴")

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: "q"})
	require.NoError(t, err, "HitLimit 非错误")
	assert.Equal(t, "收尾终答", res.Output)
	assert.Equal(t, 2, env.streamer.calls)
	assert.Contains(t, env.streamer.lastMsgs()[len(env.streamer.lastMsgs())-1].Content, reactloop.NudgeText,
		"nudge 瞬态进收尾轮请求")
	assert.Empty(t, env.streamer.lastOpts().Tools, "收尾轮摘工具")
}

// ⑤ prompt 渲染缺失变量 → 图缺陷 400 带 node 前缀，零 LLM / mcp 交互。
func TestExecuteReactRenderMissing(t *testing.T) {
	nodes, edges := reactGraph(`{"model_id":"3","prompt":"{{nope}}","tool_ids":[7]}`)
	env := newExecEnv(t, "published", nodes, edges,
		func(_ context.Context, call int) (*schema.Message, error) {
			return reactFinalMsg("不该到达"), nil
		})
	env.tools.defs = []tool.ToolDef{{Name: "s1__weather", InputSchema: json.RawMessage(`{"type":"object"}`)}}

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 9, Input: "q"})
	require.ErrorIs(t, err, errs.ErrValidationFailed)
	assert.Contains(t, err.Error(), "node agent:")
	assert.Contains(t, err.Error(), "nope")
	assert.Equal(t, 0, env.streamer.calls)
	assert.Equal(t, 0, env.tools.calls, "渲染先于 mcp 交互")
}
