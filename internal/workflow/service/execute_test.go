package service

// Execute 端到端测试（spec 06 §5 FR1-FR6）：真实执行器 + 真实 llm.NewClient 包可编程
// fake 上游 + stub store / executions / rag——零真实 PG / LLM / 网络。覆盖：把关节点
//（published / draft / disabled / trial 例外）、快照游走（线性 / 隐式终止 / 条件分支 /
// 首条命中 / 无命中 fail-fast）、错误分类与失败轨迹照写（ErrorNode 定位）、5min 上限
// 归一 ErrWorkflowExecutionFailed、调用方取消不归一、onNodeDone 回调缝、chat 触发引用。

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/llm"
	providerapi "github.com/Karlsk/go-hify/internal/provider/api"
	"github.com/Karlsk/go-hify/internal/platform/traceid"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// execEnv 一次执行测试的装配：svc 持真实执行器（真 client + fake 上游），store stub
// 三查回图、CreateRun 记录快照（回填 run.ID=7 模拟 RETURNING）。
type execEnv struct {
	svc      *workflowService
	st       *stubStore
	factory  *stubClientFactory
	streamer *genStreamer
	execs    *execRecorder
	rags     *stubRetrieve

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
	svc := New(env.st, models, nil, &recordCache{}, env.factory, env.execs, env.rags, false)
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

func TestExecuteToolFailFast(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "t1", Type: "tool", Config: `{"tool_id":"5"}`},
	}
	env := newExecEnv(t, "published", nodes, nil, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)
	assert.ErrorIs(t, err, errs.ErrValidationFailed, "tool 未支持 → 图缺陷 400")
	assert.Contains(t, err.Error(), "node t1:")
	assert.Equal(t, "t1", env.lastRun.ErrorNode)
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
