package service

// execcontext 测试（spec 06 O2 终形）：render strict 求值（缺失变量报错且文案含变量名）、
// condition 迷你表达式（裸 var / == 比较 / 字面量含单引号与空串）、set 落池与 steps 累积。
// 纯内存，零外部依赖（spec 06 §7：零真实 PG / Redis / 网络 / LLM）。

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCtx 预置 input + 两个节点输出的典型池。
func newTestCtx() *execContext {
	c := newExecContext("查订单")
	c.set("classify", "ORDER_QUERY")
	c.set("reply", "订单已发货")
	return c
}

func TestRenderMixed(t *testing.T) {
	c := newTestCtx()
	got, err := c.render("意图={{classify}}，输入={{input}}，回复={{reply}}")
	require.NoError(t, err)
	assert.Equal(t, "意图=ORDER_QUERY，输入=查订单，回复=订单已发货", got)
}

func TestRenderNoPlaceholder(t *testing.T) {
	c := newTestCtx()
	got, err := c.render("没有占位符的静态文本")
	require.NoError(t, err)
	assert.Equal(t, "没有占位符的静态文本", got)
}

func TestRenderEmpty(t *testing.T) {
	c := newTestCtx()
	got, err := c.render("")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// strict：缺失变量即执行错误，错误文案含缺失名（错字可定位，O2 拒绝宽松空串）。
func TestRenderStrictMissing(t *testing.T) {
	c := newTestCtx()
	_, err := c.render("前缀 {{typo_key}} 后缀")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "typo_key")
}

func TestRenderMultipleMissingNamesFirstOne(t *testing.T) {
	c := newTestCtx()
	_, err := c.render("{{a_missing}}{{b_missing}}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a_missing")
}

// ---- condition 迷你表达式（O2 ③：求值结果恒为字符串，与模板共用 render 底层）----

func TestEvalCondition(t *testing.T) {
	c := newTestCtx()
	c.set("weird", "it's fine")
	c.set("empty", "")
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"裸 var 直取节点输出", "{{classify}}", "ORDER_QUERY"},
		{"裸 var 直取 input", "{{input}}", "查订单"},
		{"比较命中", "{{classify}} == 'ORDER_QUERY'", "true"},
		{"比较未命中", "{{classify}} == 'REFUND'", "false"},
		{"比较两侧空格宽容", "{{classify}}=='ORDER_QUERY'", "true"},
		{"字面量含单引号（首尾引号定界）", "{{weird}} == 'it's fine'", "true"},
		{"空串字面量命中", "{{empty}} == ''", "true"},
		{"空串字面量未命中", "{{classify}} == ''", "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.evalCondition(tc.expr)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// strict 同样适用于表达式左侧变量。
func TestEvalConditionMissingVar(t *testing.T) {
	c := newTestCtx()
	_, err := c.evalCondition("{{typo_key}} == 'X'")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "typo_key")
}

// 非法形态（左侧非 {{var}} / 右侧裸词）报错而非静默通过。
func TestEvalConditionMalformed(t *testing.T) {
	c := newTestCtx()
	for _, expr := range []string{
		"classify == 'X'",  // 左侧非 {{var}}
		"{{classify}} = X", // 右侧无引号（且非 ==）
		"{{classify}} != 'X'",
	} {
		_, err := c.evalCondition(expr)
		assert.Error(t, err, "expr=%q 应报错", expr)
	}
}

// ---- set 落池与 steps 累积 ----

func TestSetPoolsValue(t *testing.T) {
	c := newExecContext("in")
	c.set("n1", "v1")
	assert.Equal(t, "v1", c.vars["n1"])
	// 同 key 覆写（R7 保证运行期每 key 恰写一次，此为底层语义记录）
	c.set("n1", "v2")
	assert.Equal(t, "v2", c.vars["n1"])
}

func TestStepsAccumulate(t *testing.T) {
	c := newExecContext("in")
	c.record(nodeStep{NodeKey: "a", NodeType: "llm", Status: "succeeded", DurationMs: 10})
	c.record(nodeStep{NodeKey: "b", NodeType: "end", Status: "succeeded", DurationMs: 5})
	require.Len(t, c.steps, 2)
	assert.Equal(t, "a", c.steps[0].NodeKey) // 顺序即执行序
	assert.Equal(t, "b", c.steps[1].NodeKey)
}
