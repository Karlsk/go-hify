package service

// execute：Execute 编排（spec 06 §5 / FR1-FR7）。同步纯函数——ctx 进、结果出、
// 不感知 SSE。四段：快照加载（三查直读 store，读实时图不走缓存）→ 把关节点
//（published 放行，draft / disabled 拒绝且文案区分两态，?trial=true 唯一例外）→
// 5min 总时长上限内线性游走（O5，WithTimeoutCause + context.Cause 区分超时与调用方
// 取消）→ 收尾一事务写两层轨迹（失败路径也写；写入用 WithoutCancel 脱钩请求 ctx，
// 写入失败降级 RunID 置空不阻断结果返回）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Karlsk/go-hify/internal/platform/errs"
	"github.com/Karlsk/go-hify/internal/platform/traceid"
	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// workflowTimeout 工作流总时长上限（O5：5min；包级变量是测试缝）。防 llm 重试 ×
// 多节点叠加把一次执行拖成无限等待。
var workflowTimeout = 5 * time.Minute

// errWorkflowTimeout WithTimeoutCause 的 cause 哨兵：context.Cause 命中它即判
//「总时长超限」（环境限制类 500）；调用方取消 / 其他 cause 不归一。
var errWorkflowTimeout = errors.New("workflow execution exceeded overall timeout")

// runStatusSucceeded / runStatusFailed 轨迹状态（无 RUNNING 态——append-only 收尾
// 统一写，O7）。
const (
	runStatusSucceeded = "succeeded"
	runStatusFailed    = "failed"
)

// 轨迹截断上限（O7 ④ / db_model §12 决策 #5）：各轨迹落库值（runs.input /
// runs.output / node_runs.input / node_runs.output）单值截 16KB，jsonb 列超限在
// 对象内标 truncated:true；slog 节点轨迹截 1KB。API 返回值不截。
const (
	maxTraceValueBytes = 16 << 10
	maxNodeLogBytes    = 1 << 10
)

// truncateUTF8 按字节上限截断并剪掉截口处的无效 UTF-8 尾部（多字节字符被切开时）。
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// wrapTraceJSON 轨迹 jsonb 包装（run 行 input 与 node 行 input/output 共用）：
// 单值超 16KB 截断并在对象内标 `truncated: true`（回放时分得清「本来就这么短」）；
// nil / 空 map → `{}`。runs.output 是 text 列无 jsonb 容器，只截断不加标记。
func wrapTraceJSON(m map[string]string) string {
	obj := make(map[string]any, len(m)+1)
	for k, v := range m {
		if len(v) > maxTraceValueBytes {
			obj[k] = truncateUTF8(v, maxTraceValueBytes)
			obj["truncated"] = true
		} else {
			obj[k] = v
		}
	}
	b, err := json.Marshal(obj)
	if err != nil { // map[string]string 序列化不会失败，防御兜底
		return "{}"
	}
	return string(b)
}

// Execute 实现 workflowapi.WorkflowService.Execute。错误语义（O4）：图缺陷 400 /
// 环境限制 WORKFLOW_EXECUTION_FAILED 500 / 下游哨兵透传——分类在 executor 与游走
// 层完成，此处仅做超时归一；失败时返回 err 的同时失败轨迹已照写。
func (s *workflowService) Execute(ctx context.Context, req workflowapi.ExecuteWorkflowReq) (*workflowapi.RunResultSchema, error) {
	// ① 快照加载：loadGraph 三查直读 store（进行中执行不受并发编辑影响——数据已在
	// 内存），不经 Get / 缓存——执行读实时图，编辑立即对下一次执行生效（FR2）。
	wf, nodes, edges, err := s.loadGraph(ctx, req.ID)
	if err != nil {
		return nil, err
	}

	// ② 把关节点：正式运行仅 published；?trial=true 放开 draft / disabled（状态机
	// 唯一例外，O3）。文案区分两态（前端提示作者 publish 或重新启用）。
	if wf.Status != string(workflowapi.StatusPublished) && !req.Trial {
		switch wf.Status {
		case string(workflowapi.StatusDraft):
			return nil, fmt.Errorf("%w: workflow is draft, publish it first or run with ?trial=true",
				workflowapi.ErrWorkflowNotPublished)
		case string(workflowapi.StatusDisabled):
			return nil, fmt.Errorf("%w: workflow is disabled", workflowapi.ErrWorkflowNotPublished)
		default:
			return nil, fmt.Errorf("%w: workflow status is %s", workflowapi.ErrWorkflowNotPublished, wf.Status)
		}
	}

	// ③ 游走：总时长上限包住全过程（含 llm 重试在内的全部节点）。
	ctx, cancel := context.WithTimeoutCause(ctx, workflowTimeout, errWorkflowTimeout)
	defer cancel()

	started := time.Now()
	ec := newExecContext(req.Input)
	outcome, execErr := s.walk(ctx, wf, nodes, edges, ec)

	// ④ 收尾：两层轨迹一事务统一写（失败 / 取消路径也写——排障唯一线索，FR7）。
	// 写入脱钩请求 ctx：调用方断连不能丢轨迹。写入失败降级（O7 ③）：重试恰好一次，
	// 仍败则结果照返、RunID 置空、ERROR 日志带 trace_id——轨迹可丢，执行结果不可丢。
	writeCtx := context.WithoutCancel(ctx)
	run := s.buildRun(writeCtx, req, wf, started, outcome, execErr)
	nodeRuns := buildNodeRuns(ec) // RunID 由 store 事务内 INSERT RETURNING 后回填
	runID := ""
	writeErr := s.store.CreateRun(writeCtx, run, nodeRuns)
	if writeErr != nil {
		writeErr = s.store.CreateRun(writeCtx, run, nodeRuns) // 重试恰好一次
	}
	if writeErr != nil {
		traceID := "" // 显式带 trace_id：handler 未包 trace 提取层时仍可对账
		if id, ok := traceid.From(writeCtx); ok {
			traceID = id
		}
		slog.ErrorContext(writeCtx, "workflow: write run trace failed",
			"workflow_id", req.ID, "trace_id", traceID, "err", writeErr)
	} else {
		runID = strconv.FormatUint(run.ID, 10)
	}
	return &workflowapi.RunResultSchema{
		RunID:      runID,
		Status:     outcome.status,
		Output:     outcome.output,
		DurationMs: int(time.Since(started).Milliseconds()),
		NodeTrace:  toNodeTrace(ec),
	}, execErr
}

// walkOutcome 游走产物：终态 / 终稿 / 失败节点 key（成功时空——run.ErrorNode 空值
// 约定）。
type walkOutcome struct {
	status    string
	output    string
	errorNode string
}

// walk 从 start_node_key 线性游走：逐节点 runNode → 成功输出落池 + 记 step + 进度
// 回调 → route 选下一条边；无出边 = 隐式终止（末节点输出即终稿，FR2）。节点执行
// 失败或 route 无命中 → 失败 step 照记 + 返回首错（ErrorNode 定位）。R6 / R7 保证
// 图无环、引用有效——游走必然终止；超时经 context.Cause 归一（O5）。
func (s *workflowService) walk(ctx context.Context, wf *Workflow, nodes []WorkflowNode,
	edges []WorkflowEdge, ec *execContext) (walkOutcome, error) {
	nodeByKey := make(map[string]WorkflowNode, len(nodes))
	for _, n := range nodes {
		nodeByKey[n.NodeKey] = n
	}
	edgesBySource := make(map[string][]WorkflowEdge, len(edges))
	for _, e := range edges { // ListEdges 按 id 升序 = 声明序，route 首条命中语义依赖此序
		edgesBySource[e.SourceNodeKey] = append(edgesBySource[e.SourceNodeKey], e)
	}

	current := wf.StartNodeKey
	out := ""
	for current != "" {
		node, ok := nodeByKey[current]
		if !ok { // R6 保证引用有效，防御式兜底（图缺陷）
			return walkOutcome{status: runStatusFailed, errorNode: current},
				fmt.Errorf("node %s: %w: node not found in graph", current, errs.ErrValidationFailed)
		}
		cfg, err := workflowapi.ParseNodeConfig(workflowapi.NodeType(node.Type), json.RawMessage(node.Config))
		if err != nil { // 理论不可达（保存期已校验），运行时兜底归图缺陷
			err = fmt.Errorf("node %s: %w: %v", current, errs.ErrValidationFailed, err)
			ec.record(nodeStep{NodeKey: current, NodeType: node.Type, Status: runStatusFailed})
			return walkOutcome{status: runStatusFailed, errorNode: current}, err
		}
		nodeStart := time.Now()
		ec.pendingIn = nil // 防御清零：入参摘要按节点一一对应
		out, err = s.exec.runNode(ctx, current, cfg, ec)
		if err != nil {
			if errors.Is(context.Cause(ctx), errWorkflowTimeout) {
				err = fmt.Errorf("%w: %v", workflowapi.ErrWorkflowExecutionFailed, err) // O5 归一
			}
			ec.record(nodeStep{NodeKey: current, NodeType: node.Type, In: ec.takeNodeIn(),
				Status: runStatusFailed, DurationMs: int(time.Since(nodeStart).Milliseconds())})
			return walkOutcome{status: runStatusFailed, errorNode: current}, err
		}
		ec.set(current, out) // 仅成功节点输出落池
		ec.record(nodeStep{NodeKey: current, NodeType: node.Type, In: ec.takeNodeIn(), Out: out,
			Status: runStatusSucceeded, DurationMs: int(time.Since(nodeStart).Milliseconds())})
		slog.InfoContext(ctx, "workflow node done", "node", current, "type", node.Type,
			"status", runStatusSucceeded, "output", truncateUTF8(out, maxNodeLogBytes)) // 轨迹同步一条，截 1KB
		if s.onNodeDone != nil {
			s.onNodeDone(current, out) // FR9 进度回调缝（nil = 零开销）
		}
		next, hasNext, err := route(edgesBySource[current], out)
		if err != nil { // 无命中出边 = 图缺陷，前缀挂失败节点（route 只知 result 不知 key）
			return walkOutcome{status: runStatusFailed, errorNode: current},
				fmt.Errorf("node %s: %w", current, err)
		}
		if !hasNext {
			break // 隐式终止：out 即末节点输出（终稿）
		}
		current = next
	}
	return walkOutcome{status: runStatusSucceeded, output: out}, nil
}

// buildRun 组装 run 行（快照语义：WorkflowName / 触发来源 / trial 标记 / 终态与
// 失败定位；trace_id 从 ctx 提取串起 slog 与轨迹）。
func (s *workflowService) buildRun(ctx context.Context, req workflowapi.ExecuteWorkflowReq,
	wf *Workflow, started time.Time, outcome walkOutcome, execErr error) *WorkflowRun {
	trigger := "console"
	if req.ConversationID != nil {
		trigger = "chat" // 进程内调用方（FR1 下游消费者）
	}
	run := &WorkflowRun{
		WorkflowID:     wf.ID,
		WorkflowName:   wf.Name, // 冗余快照：改名后历史可读
		TriggerSource:  trigger,
		IsTrial:        req.Trial,
		ConversationID: req.ConversationID,
		MessageID:      req.MessageID,
		Status:         outcome.status,
		Input:          wrapTraceJSON(map[string]string{"input": req.Input}), // jsonb 对象（db_model §12「执行入参（截断后）」）
		Output:         truncateUTF8(outcome.output, maxTraceValueBytes), // text 列：截断无标记容器
		ErrorNode:      outcome.errorNode,
		DurationMs:     int(time.Since(started).Milliseconds()),
		StartedAt:      started,
	}
	if id, ok := traceid.From(ctx); ok {
		run.TraceID = id
	}
	if execErr != nil {
		run.ErrorMsg = execErr.Error()
	}
	return run
}

// buildNodeRuns 内存步骤 → 轨迹行（seq 从 1 连续；RunID 留空由 store 事务回填；
// input / output 为截断摘要 jsonb——非 ctx 全量快照，O7 ④）。
func buildNodeRuns(ec *execContext) []WorkflowNodeRun {
	runs := make([]WorkflowNodeRun, 0, len(ec.steps))
	for i, st := range ec.steps {
		runs = append(runs, WorkflowNodeRun{
			Seq:        i + 1,
			NodeKey:    st.NodeKey,
			NodeType:   st.NodeType,
			Status:     st.Status,
			DurationMs: st.DurationMs,
			Input:      wrapTraceJSON(st.In),
			Output:     wrapTraceJSON(map[string]string{"output": st.Out}),
		})
	}
	return runs
}

// toNodeTrace 内存步骤 → 响应摘要（数组顺序即执行序；ErrorMsg 成功恒 ""）。
func toNodeTrace(ec *execContext) []workflowapi.NodeRunSummary {
	trace := make([]workflowapi.NodeRunSummary, 0, len(ec.steps))
	for _, st := range ec.steps {
		trace = append(trace, workflowapi.NodeRunSummary{
			NodeKey:    st.NodeKey,
			NodeType:   st.NodeType,
			Status:     st.Status,
			DurationMs: st.DurationMs,
		})
	}
	return trace
}
