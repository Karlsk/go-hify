package service

// 轨迹截断测试（spec 06 FR7 / O7 ④，db_model §12 决策 #5 + §12 runs.input 列注
//「执行入参（截断后）」）：各轨迹落库值单值截断 16KB——node_runs.input /
// node_runs.output / runs.input 为 jsonb 列，超限截断并在对象内标
// `truncated: true`（回放时分得清「本来就这么短」）；runs.output 为 text 列只截断；
// API 返回值不截（截断只发生在落库边界）。slog 节点轨迹同步一条、output 截 1KB。

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	workflowapi "github.com/Karlsk/go-hify/internal/workflow/api"
)

// logCapture 捕获 slog 记录（slog.Handler 最小实现；测试期 SetDefault，结束恢复）。
type logCapture struct {
	records []slog.Record
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logCapture) WithGroup(string) slog.Handler      { return h }

// attrString 取记录里首个指定 key 的字符串属性。
func attrString(r slog.Record, key string) (string, bool) {
	var val string
	var found bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key && !found {
			val, found = a.Value.String(), true
			return false
		}
		return true
	})
	return val, found
}

// decodeJSONb 解析轨迹列的 jsonb 文本。
func decodeJSONb(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m), "轨迹列应为合法 jsonb：%q", s)
	return m
}

// 超 16384 字节：各轨迹落库值截断（jsonb 列加 truncated 标记，runs.output 为
// text 列无标记容器）；未超限的入参摘要（prompt 短）不标记。
func TestExecuteTraceTruncation(t *testing.T) {
	nodes, edges := linearGraph()
	big := strings.Repeat("x", 16385)
	env := newExecEnv(t, "published", nodes, edges, singleGen(big))

	res, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "查订单"})
	require.NoError(t, err)
	assert.Len(t, res.Output, 16385, "API 返回原值不截（截断只发生在落库边界）")

	// runs.output（text 列）：16KB 截断
	assert.Len(t, env.lastRun.Output, 16384, "runs.output 超 16KB 截断")
	assert.Equal(t, strings.Repeat("x", 16384), env.lastRun.Output)

	// node_runs[0]（classify llm）：output 单值截断 + truncated:true；input prompt 未超限不标
	require.Len(t, env.lastNodeRuns, 2)
	outM := decodeJSONb(t, env.lastNodeRuns[0].Output)
	assert.Len(t, outM["output"], 16384, "node_runs.output 单值截 16KB")
	assert.Equal(t, true, outM["truncated"], "jsonb 内标记被截断")
	inM := decodeJSONb(t, env.lastNodeRuns[0].Input)
	assert.Equal(t, "判断意图：查订单", inM["prompt"], "入参摘要 = 渲染后 prompt")
	_, marked := inM["truncated"]
	assert.False(t, marked, "未超限不标 truncated（回放分得清本来就这么短）")

	// node_runs[1]（finish end）：入参摘要 = output 模板原文；终稿超限同样截断标记
	inM1 := decodeJSONb(t, env.lastNodeRuns[1].Input)
	assert.Equal(t, "{{classify}}", inM1["output"])
	outM1 := decodeJSONb(t, env.lastNodeRuns[1].Output)
	assert.Len(t, outM1["output"], 16384)
	assert.Equal(t, true, outM1["truncated"])
}

// runs.input 同为 jsonb（db_model §12「执行入参（截断后）」）：超 16KB 截断加标记。
// 绑定层 max=16384 按 rune 计数（中文可超 16KB 字节），截断不能只靠入参校验兜底。
func TestExecuteTraceInputTruncation(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen("ok"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: strings.Repeat("x", 20000)})
	require.NoError(t, err)

	inM := decodeJSONb(t, env.lastRun.Input)
	inStr, ok := inM["input"].(string)
	require.True(t, ok)
	assert.Len(t, inStr, 16384, "runs.input 超 16KB 截断")
	assert.Equal(t, true, inM["truncated"], "jsonb 内标记被截断")
}

// 恰 16384 字节 = 边界内：各落库值原样保留、无 truncated 标记。
func TestExecuteTraceExactLimit(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen(strings.Repeat("x", 16384)))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.NoError(t, err)

	assert.Len(t, env.lastRun.Output, 16384, "恰 16KB 不截断")
	outM := decodeJSONb(t, env.lastNodeRuns[0].Output)
	assert.Len(t, outM["output"], 16384)
	_, marked := outM["truncated"]
	assert.False(t, marked, "边界值不标记")
}

// 失败节点（渲染失败）入参摘要为空对象 {}，不是残缺 jsonb。
func TestExecuteTraceFailedNodeEmptyIn(t *testing.T) {
	nodes := []WorkflowNode{
		{NodeKey: "classify", Type: "llm", Config: `{"model_id":"3","prompt":"{{typo_key}}"}`},
	}
	env := newExecEnv(t, "published", nodes, nil, singleGen("x"))

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.Error(t, err)

	require.Len(t, env.lastNodeRuns, 1)
	inM := decodeJSONb(t, env.lastNodeRuns[0].Input)
	assert.Empty(t, inM, "渲染失败发生在入参摘要写入之前 → 空对象")
	outM := decodeJSONb(t, env.lastNodeRuns[0].Output)
	assert.Empty(t, outM["output"], "失败节点无输出")
}

// slog 节点轨迹：每节点成功后同步一条，output 截 1KB（FR7）。
func TestExecuteSlogNodeTrace(t *testing.T) {
	nodes, edges := linearGraph()
	env := newExecEnv(t, "published", nodes, edges, singleGen(strings.Repeat("y", 5000)))

	cap := &logCapture{}
	old := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(old) })

	_, err := env.svc.Execute(context.Background(), workflowapi.ExecuteWorkflowReq{ID: 42, Input: "in"})
	require.NoError(t, err)

	var nodeRec *slog.Record
	for i := range cap.records {
		if cap.records[i].Message == "workflow node done" {
			nodeRec = &cap.records[i]
			break
		}
	}
	require.NotNil(t, nodeRec, "节点轨迹同步记 slog")
	out, ok := attrString(*nodeRec, "output")
	require.True(t, ok, "记录带 output 属性")
	assert.Len(t, out, 1024, "slog 节点轨迹截 1KB")
	assert.Equal(t, "classify", mustAttrString(t, *nodeRec, "node"))
}

// mustAttrString attrString 的 require 版。
func mustAttrString(t *testing.T, r slog.Record, key string) string {
	t.Helper()
	v, ok := attrString(r, key)
	require.True(t, ok, "记录缺 %q 属性", key)
	return v
}
