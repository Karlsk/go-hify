package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 钉住测试：哨兵 code、应用层常量（与 DB CHECK / 前端枚举对齐）防漂移；
// 响应 JSON 形态（id 字符串化、空列表返 []）防前端契约被无意改坏。

// jsonTime 测试用固定时间（BaseSchema 时间字段只需非零可序列化）。
var jsonTime = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func TestConstantsPinned(t *testing.T) {
	// 哨兵 code = Error() 字符串，与 CLAUDE.md 错误码表命名空间一致。
	assert.Equal(t, "AGENT_NOT_FOUND", ErrAgentNotFound.Error())
	assert.Equal(t, "TOOL_NOT_FOUND", ErrToolNotFound.Error())

	// 温度边界与 migrations 00004 的 CHECK (temperature BETWEEN 0 AND 2) 对齐。
	assert.Equal(t, 0.0, TemperatureMin)
	assert.Equal(t, 2.0, TemperatureMax)
	assert.Equal(t, 0.7, DefaultTemperature)

	// 上下文轮数边界与 migrations 00009 的 DEFAULT 10 / binding 1-100 对齐。
	assert.Equal(t, 1, MaxContextTurnsMin)
	assert.Equal(t, 100, MaxContextTurnsMax)
	assert.Equal(t, 10, DefaultMaxContextTurns)

	// 绑定上限与 binding tag max=100 对齐（防提示词无界膨胀）。
	assert.Equal(t, 100, MaxToolBindings)
}

func TestValidateAgent_Create(t *testing.T) {
	newReq := func(mutate func(*CreateAgentReq)) CreateAgentReq {
		r := CreateAgentReq{Name: "客服助手", ModelID: 5}
		mutate(&r)
		return r
	}

	t.Run("最小合法", func(t *testing.T) {
		assert.NoError(t, newReq(func(*CreateAgentReq) {}).Validate())
	})
	t.Run("备用模型等于主模型被拒", func(t *testing.T) {
		r := newReq(func(r *CreateAgentReq) { fb := uint64(5); r.FallbackModelID = &fb })
		assert.ErrorContains(t, r.Validate(), "fallback_model_id")
	})
	t.Run("备用模型不同则合法", func(t *testing.T) {
		r := newReq(func(r *CreateAgentReq) { fb := uint64(6); r.FallbackModelID = &fb })
		assert.NoError(t, r.Validate())
	})
	t.Run("tool_ids 重复被拒", func(t *testing.T) {
		r := newReq(func(r *CreateAgentReq) { r.ToolIDs = []uint64{10, 12, 10} })
		assert.ErrorContains(t, r.Validate(), "重复")
	})
	t.Run("tool_ids 不重复则合法", func(t *testing.T) {
		r := newReq(func(r *CreateAgentReq) { r.ToolIDs = []uint64{10, 12} })
		assert.NoError(t, r.Validate())
	})
}

func TestValidateAgent_Update(t *testing.T) {
	t.Run("缺 id 被拒", func(t *testing.T) {
		r := UpdateAgentReq{Name: "客服助手", ModelID: 5}
		assert.ErrorContains(t, r.Validate(), "id 必填")
	})
	t.Run("带 id 合法", func(t *testing.T) {
		r := UpdateAgentReq{ID: 1, Name: "客服助手", ModelID: 5}
		assert.NoError(t, r.Validate())
	})
}

func TestAgentSchema_JSON(t *testing.T) {
	// 响应形态：id / model_id 字符串化，可空字段显式 null，snake_case 字段名。
	s := AgentSchema{}
	s.ID = "42"
	s.CreatedAt = jsonTime
	s.Name = "客服助手"
	s.ModelID = "5"
	s.Temperature = 0.7
	s.MaxContextTurns = 10
	s.Enabled = true

	b, err := json.Marshal(s)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, "42", m["id"])
	assert.Equal(t, "5", m["model_id"])
	assert.Equal(t, "客服助手", m["name"])
	assert.Equal(t, 0.7, m["temperature"])
	assert.Equal(t, float64(10), m["max_context_turns"])
	assert.Equal(t, true, m["enabled"])
	assert.Nil(t, m["fallback_model_id"], "未设置备用模型 → null")
	assert.Nil(t, m["max_output_tokens"], "跟随模型默认 → null")
	assert.Contains(t, m, "created_at")
	assert.Contains(t, m, "updated_at")
}

func TestAgentListItem_JSON(t *testing.T) {
	// 列表项 = AgentSchema 展平 + 聚合列；悬空引用 ModelName=""（前端 fallback model_id）。
	it := AgentListItem{ModelName: "gpt-4o", ToolCount: 2}
	it.ID = "1"
	it.ModelID = "5"
	b, err := json.Marshal(it)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, "gpt-4o", m["model_name"], "聚合列展平在列表项上")
	assert.Equal(t, float64(2), m["tool_count"])
	assert.Contains(t, m, "name", "嵌入 AgentSchema 字段展平")
	assert.Contains(t, m, "enabled")
}

func TestAgentDetailSchema_JSONToolIDs(t *testing.T) {
	t.Run("绑定列表字符串化", func(t *testing.T) {
		d := AgentDetailSchema{ToolIDs: []string{"10", "12"}}
		d.ID = "1"
		b, err := json.Marshal(d)
		assert.NoError(t, err)
		var m map[string]any
		assert.NoError(t, json.Unmarshal(b, &m))

		tools, ok := m["tool_ids"].([]any)
		assert.True(t, ok, "tool_ids 是数组")
		assert.Equal(t, []any{"10", "12"}, tools)
		// 嵌入展平：detail 同时含 agent 自身字段。
		assert.Contains(t, m, "name")
		assert.Contains(t, m, "system_prompt")
	})
	t.Run("nil ToolIDs 序列化为 null——service 必须保证非 nil", func(t *testing.T) {
		// 此钉住测试描述契约责任：空绑定必须返 [] 不返 null（接口规范《空值约定》）。
		// service 的 toSchema 负责初始化切片；这里断言 nil 的危险形态，防止有人删掉初始化逻辑。
		d := AgentDetailSchema{}
		b, err := json.Marshal(d)
		assert.NoError(t, err)
		var m map[string]any
		assert.NoError(t, json.Unmarshal(b, &m))
		assert.Nil(t, m["tool_ids"], "nil 切片会序列化成 null——toSchema 必须用 make 初始化")
	})
}
