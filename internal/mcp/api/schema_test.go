package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 钉住测试：哨兵 code、应用层常量（与 DB CHECK / 前端枚举对齐）防漂移；
// 响应 JSON 形态（id 字符串化、env 不泄出摘要层、空容器返 {}/[]）防前端契约被无意改坏。

// jsonTime 测试用固定时间（BaseSchema 时间字段只需非零可序列化）。
var jsonTime = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func TestConstantsPinned(t *testing.T) {
	// 哨兵 code = Error() 字符串，与 CLAUDE.md 错误码表命名空间一致。
	assert.Equal(t, "MCP_SERVER_NOT_FOUND", ErrMCPServerNotFound.Error())
	assert.Equal(t, "MCP_SERVER_NAME_CONFLICT", ErrMCPServerNameConflict.Error())
	assert.Equal(t, "MCP_SERVER_IN_USE", ErrMCPServerInUse.Error())
	assert.Equal(t, "MCP_TOOL_NOT_FOUND", ErrMCPToolNotFound.Error())
	assert.Equal(t, "MCP_TOOL_CALL_FAILED", ErrMCPToolCallFailed.Error())

	// transport 枚举与 migrations 00003 的 CHECK (transport IN ('stdio','http','sse')) 对齐。
	assert.Equal(t, "stdio", TransportStdio)
	assert.Equal(t, "http", TransportHTTP)
	assert.Equal(t, "sse", TransportSSE)

	// 长度上限与 binding tag 的 max= 取值一致（tag 是字符串，此处钉住常量防漂移）。
	assert.Equal(t, 128, MaxNameLen)
	assert.Equal(t, 512, MaxCommandLen)
	assert.Equal(t, 2048, MaxURLLen)
}

func TestValidateMCPServer(t *testing.T) {
	t.Run("stdio 带 command 合法", func(t *testing.T) {
		r := CreateMCPServerReq{Name: "天气", Transport: "stdio", Command: "uvx mcp-weather"}
		assert.NoError(t, r.Validate())
	})
	t.Run("stdio 缺 command 被拒", func(t *testing.T) {
		r := CreateMCPServerReq{Name: "天气", Transport: "stdio"}
		assert.ErrorContains(t, r.Validate(), "command")
	})
	t.Run("http 带 url 合法", func(t *testing.T) {
		r := CreateMCPServerReq{Name: "天气", Transport: "http", URL: "http://mcp.local/mcp"}
		assert.NoError(t, r.Validate())
	})
	t.Run("http 缺 url 被拒", func(t *testing.T) {
		r := CreateMCPServerReq{Name: "天气", Transport: "http"}
		assert.ErrorContains(t, r.Validate(), "url")
	})
	t.Run("sse 缺 url 被拒", func(t *testing.T) {
		r := CreateMCPServerReq{Name: "天气", Transport: "sse"}
		assert.ErrorContains(t, r.Validate(), "url")
	})
	t.Run("未知 transport 被拒（Validate 直调兜底，覆盖绕过 handler 的调用方）", func(t *testing.T) {
		r := CreateMCPServerReq{Name: "天气", Transport: "grpc"}
		assert.ErrorContains(t, r.Validate(), "transport")
	})
	t.Run("UpdateReq 缺 id 被拒", func(t *testing.T) {
		r := UpdateMCPServerReq{Name: "天气", Transport: "http", URL: "http://x/mcp"}
		assert.ErrorContains(t, r.Validate(), "id 必填")
	})
}

func TestRunToolReqValidate(t *testing.T) {
	t.Run("最小合法", func(t *testing.T) {
		r := RunToolReq{ID: 1, ToolName: "search"}
		assert.NoError(t, r.Validate())
	})
	t.Run("缺 id 被拒", func(t *testing.T) {
		r := RunToolReq{ToolName: "search"}
		assert.ErrorContains(t, r.Validate(), "id 必填")
	})
	t.Run("arguments 对象合法", func(t *testing.T) {
		r := RunToolReq{ID: 1, ToolName: "search", Arguments: json.RawMessage(`{"q":"天气"}`)}
		assert.NoError(t, r.Validate())
	})
	t.Run("arguments null 等价无参", func(t *testing.T) {
		r := RunToolReq{ID: 1, ToolName: "search", Arguments: json.RawMessage(`null`)}
		assert.NoError(t, r.Validate())
	})
	t.Run("arguments 数组被拒（MCP 参数须为对象）", func(t *testing.T) {
		r := RunToolReq{ID: 1, ToolName: "search", Arguments: json.RawMessage(`[1,2]`)}
		assert.ErrorContains(t, r.Validate(), "对象")
	})
	t.Run("arguments 非法 JSON 被拒", func(t *testing.T) {
		r := RunToolReq{ID: 1, ToolName: "search", Arguments: json.RawMessage(`{bad`)}
		assert.ErrorContains(t, r.Validate(), "JSON")
	})
}

func TestMCPServerSchema_JSON(t *testing.T) {
	// 响应形态：id 字符串化、snake_case、enabled 展平；env 不在摘要 Schema。
	s := MCPServerSchema{Name: "天气", Transport: "http", URL: "http://mcp.local/mcp", Enabled: false}
	s.ID = "42"
	s.CreatedAt = jsonTime

	b, err := json.Marshal(s)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, "42", m["id"])
	assert.Equal(t, "天气", m["name"])
	assert.Equal(t, "http", m["transport"])
	assert.Equal(t, false, m["enabled"], "停用状态须可读（显式 false 不被吞的响应侧证据）")
	assert.NotContains(t, m, "env", "摘要层不含 env（仅详情回传，server_mgmt_spec.md §2）")
	assert.Contains(t, m, "created_at")
	assert.Contains(t, m, "updated_at")
}

func TestMCPServerListItem_JSON(t *testing.T) {
	it := MCPServerListItem{ToolCount: 3}
	it.ID = "1"
	it.Name = "天气"
	b, err := json.Marshal(it)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, float64(3), m["tool_count"], "聚合列展平")
	assert.NotContains(t, m, "env", "列表项不含 env")
	assert.Contains(t, m, "enabled", "嵌入 MCPServerSchema 字段展平")
}

func TestMCPServerDetailSchema_JSON(t *testing.T) {
	// 详情层 env / tools 均回传；nil 的危险形态序列化为 null——service 的
	// toSchema 必须用 make / 字面量初始化（接口规范《空值约定》）。
	t.Run("nil Env / Tools 序列化为 null——service 必须保证非 nil", func(t *testing.T) {
		b, err := json.Marshal(MCPServerDetailSchema{})
		assert.NoError(t, err)
		var m map[string]any
		assert.NoError(t, json.Unmarshal(b, &m))
		assert.Nil(t, m["env"], "nil map 会序列化成 null——toDetailSchema 必须初始化")
		assert.Nil(t, m["tools"], "nil 切片会序列化成 null——toDetailSchema 必须初始化")
	})
	t.Run("空容器返 {} / []", func(t *testing.T) {
		d := MCPServerDetailSchema{Env: map[string]string{}, Tools: []ToolSchema{}}
		d.ID = "1"
		b, err := json.Marshal(d)
		assert.NoError(t, err)
		var m map[string]any
		assert.NoError(t, json.Unmarshal(b, &m))
		assert.Equal(t, map[string]any{}, m["env"])
		assert.Equal(t, []any{}, m["tools"])
	})
}

func TestToolSchema_JSON(t *testing.T) {
	// id / server_id 字符串化；input_schema 原样透传（对象键序无关，断言解析等价）。
	ts := ToolSchema{
		ID: "10", MCPServerID: "7", Name: "search", Description: "搜索",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}
	b, err := json.Marshal(ts)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, "10", m["id"])
	assert.Equal(t, "7", m["server_id"])
	schemaVal, ok := m["input_schema"].(map[string]any)
	assert.True(t, ok, "input_schema 透传为对象")
	assert.Equal(t, "object", schemaVal["type"])
}

func TestToolCheck_JSON(t *testing.T) {
	// 绑定校验契约：ToolSchema 展平 + server_enabled 标志。
	tc := ToolCheck{ServerEnabled: false}
	tc.ID = "10"
	tc.MCPServerID = "7"
	b, err := json.Marshal(tc)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, false, m["server_enabled"], "停用标志是 agent 绑定校验的判据，须可达")
	assert.Equal(t, "7", m["server_id"], "ToolSchema 字段展平")
}

func TestDiscoverResultSchema_JSON(t *testing.T) {
	t.Run("成功：error_message 键缺省（omitempty）", func(t *testing.T) {
		b, err := json.Marshal(DiscoverResultSchema{Success: true, LatencyMs: 120, ToolCount: 5})
		assert.NoError(t, err)
		var m map[string]any
		assert.NoError(t, json.Unmarshal(b, &m))
		assert.NotContains(t, m, "error_message")
		assert.Equal(t, float64(5), m["tool_count"])
	})
	t.Run("失败：error_message 非空（动作完成即 200，成败在字段里）", func(t *testing.T) {
		b, err := json.Marshal(DiscoverResultSchema{Success: false, ErrorMessage: "dial tcp: refused"})
		assert.NoError(t, err)
		var m map[string]any
		assert.NoError(t, json.Unmarshal(b, &m))
		assert.Equal(t, "dial tcp: refused", m["error_message"])
	})
}
