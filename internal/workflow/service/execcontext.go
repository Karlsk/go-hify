package service

// execcontext：单次执行的变量池与节点轨迹累积（spec 06 §4.2 / O2）。
// vars 是 flat string 池——"input"（O1 单一入参终形）+ 各成功节点的 node_key 输出；
// steps 按执行序累积节点步骤，收尾统一转写 workflow_node_runs（O7，无 RUNNING 态）。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// nodeStep 单节点的执行步骤（内存形态，收尾批量落库）。In / Out 存原值，
// 截断发生在落库边界（wrapTraceJSON / buildRun）。
type nodeStep struct {
	NodeKey    string
	NodeType   string
	Status     string
	DurationMs int
	In         map[string]string // 入参摘要（executor 各分支渲染成功后回填；nil = 空对象）
	Out        string            // 节点输出（失败节点空串）
}

// placeholderRE 模板占位符 {{var}}；变量名不含花括号。
var placeholderRE = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// singleVarRE 整串恰好一个占位符（condition 裸 var 形态）。
var singleVarRE = regexp.MustCompile(`^\{\{([^{}]+)\}\}$`)

// execContext 一次执行的可变状态（单 goroutine 游走，不加锁）。
type execContext struct {
	vars      map[string]string
	steps     []nodeStep
	pendingIn map[string]string // 当前节点入参摘要：executor 分支写、walk record 时取走

	// 嵌套执行透传（spec 08）：depth 顶层=0（executeChild 递增、超 maxNestLevel 拒）；
	// trial / conversationID / messageID 子 run 行与把关跟随父；childRunIDs 直接子
	// run id 集（callWorkflow 落、收尾 UpdateParentRunIDs 批量回填 parent_run_id）。
	depth          int
	trial          bool
	conversationID *uint64
	messageID      *uint64
	childRunIDs    []uint64
}

// newExecContext 以唯一入参 input 建池（O1）。
func newExecContext(input string) *execContext {
	return &execContext{vars: map[string]string{"input": input}}
}

// set 落池（同 key 覆写；R7 保证运行期每 key 恰写一次，此处仅记录底层语义）。
func (c *execContext) set(key, val string) {
	c.vars[key] = val
}

// record 累积节点步骤，顺序即执行序（seq 在收尾统一编号）。
func (c *execContext) record(s nodeStep) {
	c.steps = append(c.steps, s)
}

// setNodeIn 记录当前节点入参摘要（executor 各分支渲染成功后写；llm 的 prompt、
// api 的 url 等排障关键值在下游调用失败时仍可读）。
func (c *execContext) setNodeIn(m map[string]string) {
	c.pendingIn = m
}

// takeNodeIn 取走当前节点入参摘要并清零（walk record 时调用；未写即 nil → 落 {}）。
func (c *execContext) takeNodeIn() map[string]string {
	m := c.pendingIn
	c.pendingIn = nil
	return m
}

// lookup 变量取值 + 一级下钻（spec 08 FR9 / O7②）：无点号直取池值（行为不变）；
// 带点号时基名（首个点号前）取池值、值须为合法 JSON 对象且含该字段——string 字段取
// 内容（去 JSON 引号），number / boolean / 对象字段取原 JSON 文本；深度一层为止
//（字段名含点号整体匹配，不递归）。任一条件不满足即视为变量未定义（strict，错误
// 文案由调用方带完整点分名）。
func (c *execContext) lookup(name string) (string, bool) {
	if base, field, ok := strings.Cut(name, "."); ok {
		raw, exists := c.vars[base]
		if !exists {
			return "", false
		}
		return drillField(raw, field)
	}
	v, ok := c.vars[name]
	return v, ok
}

// drillField JSON 对象池值的一级字段下钻；非 JSON 对象或字段缺失 → 未定义。
func drillField(poolVal, field string) (string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(poolVal), &obj); err != nil {
		return "", false
	}
	raw, ok := obj[field]
	if !ok {
		return "", false
	}
	return rawJSONToText(raw), true
}

// rawJSONToText JSON 值 → 模板文本：string 值去引号取内容，其余（number /
// boolean / 对象 / 数组）取原 JSON 文本。
func rawJSONToText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// render strict 模板渲染：{{var}} 替换为池值（经 lookup 支持一级下钻），缺失变量即
// 执行错误（文案含变量名，错字可定位），报首个缺失。
func (c *execContext) render(tpl string) (string, error) {
	var missing string
	out := placeholderRE.ReplaceAllStringFunc(tpl, func(m string) string {
		name := m[2 : len(m)-2]
		if v, ok := c.lookup(name); ok {
			return v
		}
		if missing == "" {
			missing = name
		}
		return m
	})
	if missing != "" {
		return "", fmt.Errorf("variable %q not defined", missing)
	}
	return out, nil
}

// evalCondition condition 迷你表达式求值（O2 ③），结果恒为字符串：
// 裸 var `{{var}}` 直取池值（多路分支按值匹配）；比较 `{{var}} == 'literal'`
// 返回 "true"/"false"，字面量以首尾单引号定界（内嵌单引号与空串皆合法）。
// 左侧必须恰为单个 {{var}}、右侧必须带引号，其余形态一律报错（图缺陷）。
func (c *execContext) evalCondition(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	if idx := strings.Index(expr, "=="); idx < 0 {
		// 裸 var：整个表达式必须恰为一个占位符
		m := singleVarRE.FindStringSubmatch(expr)
		if m == nil {
			return "", fmt.Errorf("invalid condition expression %q", expr)
		}
		v, ok := c.lookup(m[1])
		if !ok {
			return "", fmt.Errorf("variable %q not defined", m[1])
		}
		return v, nil
	} else {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		m := singleVarRE.FindStringSubmatch(left)
		if m == nil {
			return "", fmt.Errorf("invalid condition expression %q: left must be {{var}}", expr)
		}
		v, ok := c.lookup(m[1])
		if !ok {
			return "", fmt.Errorf("variable %q not defined", m[1])
		}
		if len(right) < 2 || !strings.HasPrefix(right, "'") || !strings.HasSuffix(right, "'") {
			return "", fmt.Errorf("invalid condition expression %q: literal must be quoted", expr)
		}
		if v == right[1:len(right)-1] {
			return "true", nil
		}
		return "false", nil
	}
}
