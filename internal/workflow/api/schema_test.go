package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// jsonTime 测试用固定时间（时间字段只需非零可序列化）。
var jsonTime = time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)

// llmCfg 测试用合法 llm config 片段。
var llmCfg = json.RawMessage(`{"model_id":"3","prompt":"p"}`)

// 钉住测试：哨兵 code、枚举常量（与迁移 00016 的两处 CHECK 对齐）防漂移；
// 响应 JSON 形态（id 字符串化、空列表返 []）防前端契约被无意改坏。

func TestConstantsPinned(t *testing.T) {
	// 哨兵 code = Error() 字符串，与 CLAUDE.md 错误码表命名空间一致。
	assert.Equal(t, "WORKFLOW_NOT_FOUND", ErrWorkflowNotFound.Error())
	assert.Equal(t, "WORKFLOW_NAME_CONFLICT", ErrWorkflowNameConflict.Error())
	assert.Equal(t, "WORKFLOW_NOT_PUBLISHED", ErrWorkflowNotPublished.Error())

	// 状态枚举与迁移 00016 CHECK (status IN ('draft','published','disabled')) 对齐。
	assert.Equal(t, "draft", string(StatusDraft))
	assert.Equal(t, "published", string(StatusPublished))
	assert.Equal(t, "disabled", string(StatusDisabled))

	// 节点类型与 CHECK 对齐（00016 建四类，00017 加宽补 api/end，2026-09-16 拍板；
	// 00020 补 workflow——sub-workflow 嵌套节点，spec 08）。
	assert.Equal(t, "llm", string(NodeLLM))
	assert.Equal(t, "tool", string(NodeTool))
	assert.Equal(t, "condition", string(NodeCondition))
	assert.Equal(t, "knowledge_retrieval", string(NodeKnowledgeRetrieval))
	assert.Equal(t, "api", string(NodeAPI))
	assert.Equal(t, "end", string(NodeEnd))
	assert.Equal(t, "workflow", string(NodeWorkflow))

	// 分型枚举与迁移 00020 CHECK (type IN ('chat','task')) 对齐（spec 08 §4.1）。
	assert.Equal(t, "chat", string(WorkflowTypeChat))
	assert.Equal(t, "task", string(WorkflowTypeTask))
}

func TestConfigValidate(t *testing.T) {
	t.Run("LLMConfig", func(t *testing.T) {
		assert.NoError(t, LLMConfig{ModelID: 3, Prompt: "p"}.Validate())
		assert.ErrorContains(t, LLMConfig{Prompt: "p"}.Validate(), "model_id")
		assert.ErrorContains(t, LLMConfig{ModelID: 3}.Validate(), "prompt")
	})
	t.Run("ToolConfig", func(t *testing.T) {
		assert.NoError(t, ToolConfig{ToolID: 12}.Validate())
		// 2026-09-16 拍板补上：零值 tool_id 永不合法，保存期挡（存在性仍推迟执行器）。
		assert.ErrorContains(t, ToolConfig{}.Validate(), "tool_id")
	})
	t.Run("ConditionConfig", func(t *testing.T) {
		assert.NoError(t, ConditionConfig{Expression: "{{classify}} == 'ORDER_QUERY'"}.Validate())
		assert.ErrorContains(t, ConditionConfig{}.Validate(), "expression")
	})
	t.Run("KnowledgeRetrievalConfig", func(t *testing.T) {
		assert.NoError(t, KnowledgeRetrievalConfig{KnowledgeBaseID: 7}.Validate())
		assert.NoError(t, KnowledgeRetrievalConfig{KnowledgeBaseID: 7, TopK: 1}.Validate())
		assert.NoError(t, KnowledgeRetrievalConfig{KnowledgeBaseID: 7, TopK: 20}.Validate())
		assert.ErrorContains(t, KnowledgeRetrievalConfig{TopK: 3}.Validate(), "knowledge_base_id")
		assert.ErrorContains(t, KnowledgeRetrievalConfig{KnowledgeBaseID: 7, TopK: 21}.Validate(), "top_k")
		assert.ErrorContains(t, KnowledgeRetrievalConfig{KnowledgeBaseID: 7, TopK: -1}.Validate(), "top_k")
	})
	t.Run("ApiCallConfig", func(t *testing.T) {
		assert.NoError(t, ApiCallConfig{URL: "https://api.example.com/orders", Method: "POST"}.Validate())
		assert.NoError(t, ApiCallConfig{URL: "https://self-signed.internal/x", Method: "GET", SSLVerify: true}.Validate())
		assert.NoError(t, ApiCallConfig{URL: "http://localhost:8080/x", Method: "GET",
			Headers: map[string]string{"X-Request-Id": "{{input.req_id}}"},
			Body:    `{"id":"{{input.id}}"}`, TimeoutSec: 30}.Validate())
		assert.NoError(t, ApiCallConfig{URL: "https://x.com", Method: "GET", TimeoutSec: 1}.Validate())
		assert.NoError(t, ApiCallConfig{URL: "https://x.com", Method: "GET", TimeoutSec: 60}.Validate())
		assert.ErrorContains(t, ApiCallConfig{Method: "GET"}.Validate(), "url")
		assert.ErrorContains(t, ApiCallConfig{URL: "ftp://x.com", Method: "GET"}.Validate(), "url")
		assert.ErrorContains(t, ApiCallConfig{URL: "http://", Method: "GET"}.Validate(), "url") // 无 host
		assert.ErrorContains(t, ApiCallConfig{URL: "https://x.com"}.Validate(), "method")       // 缺 method
		assert.ErrorContains(t, ApiCallConfig{URL: "https://x.com", Method: "get"}.Validate(), "method")
		assert.ErrorContains(t, ApiCallConfig{URL: "https://x.com", Method: "TRACE"}.Validate(), "method")
		assert.ErrorContains(t, ApiCallConfig{URL: "https://x.com", Method: "GET", TimeoutSec: 61}.Validate(), "timeout_sec")
		assert.ErrorContains(t, ApiCallConfig{URL: "https://x.com", Method: "GET", TimeoutSec: -1}.Validate(), "timeout_sec")
	})
	t.Run("EndConfig", func(t *testing.T) {
		// 空配置即合法（output 可选）；模板语义归执行器，本层不做形状校验。
		assert.NoError(t, EndConfig{}.Validate())
		assert.NoError(t, EndConfig{Output: "{{reply}}"}.Validate())
	})
}

// TestLLMConfigSystemPromptSerialization system_prompt 序列化契约（spec 011 FR-006）：
// 有值出键且往返保值；空串与缺省均不出键——既有配置保存读回不引入新键。
func TestLLMConfigSystemPromptSerialization(t *testing.T) {
	t.Run("有值 marshal 出键且往返保值", func(t *testing.T) {
		b, err := json.Marshal(LLMConfig{ModelID: 3, SystemPrompt: "你是分类器", Prompt: "{{input}}"})
		assert.NoError(t, err)
		assert.Contains(t, string(b), `"system_prompt":"你是分类器"`)
		var c LLMConfig
		assert.NoError(t, json.Unmarshal(b, &c))
		assert.Equal(t, "你是分类器", c.SystemPrompt)
	})
	t.Run("空串 marshal 不出键", func(t *testing.T) {
		b, err := json.Marshal(LLMConfig{ModelID: 3, Prompt: "p"})
		assert.NoError(t, err)
		assert.NotContains(t, string(b), "system_prompt")
	})
	t.Run("ParseNodeConfig 带 system_prompt 解析", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeLLM, json.RawMessage(`{"model_id":"3","system_prompt":"你是分类器：{{input.style}}","prompt":"{{input.q}}"}`))
		assert.NoError(t, err)
		c, ok := cfg.(*LLMConfig)
		assert.True(t, ok)
		assert.Equal(t, "你是分类器：{{input.style}}", c.SystemPrompt)
	})
}

// ---- spec 014 T003：LLMConfig.OutputSchema 加法键（FR-001 / FR-002 / SC-003）----

// TestLLMConfigOutputSchemaSerialization output_schema 序列化三态（spec 014 FR-001）：
// 带值产出 `output_schema` 数组键且往返保值；空数组与缺省均不出键（omitempty 对齐
// task 型 workflow 级同名键——存量图保存读回不引入新键）。
func TestLLMConfigOutputSchemaSerialization(t *testing.T) {
	declared := []SchemaField{
		{Name: "code", Type: "string", Required: true, Description: "分类码"},
		{Name: "score", Type: "number"},
	}
	t.Run("带值 marshal 出键且往返保值", func(t *testing.T) {
		b, err := json.Marshal(LLMConfig{ModelID: 3, Prompt: "p", OutputSchema: declared})
		assert.NoError(t, err)
		assert.Contains(t, string(b), `"output_schema":[`+
			`{"name":"code","type":"string","required":true,"description":"分类码"},`+
			`{"name":"score","type":"number","required":false,"description":""}]`)
		var c LLMConfig
		assert.NoError(t, json.Unmarshal(b, &c))
		assert.Equal(t, declared, c.OutputSchema)
	})
	t.Run("空数组 marshal 不出键", func(t *testing.T) {
		b, err := json.Marshal(LLMConfig{ModelID: 3, Prompt: "p", OutputSchema: []SchemaField{}})
		assert.NoError(t, err)
		assert.NotContains(t, string(b), "output_schema")
	})
	t.Run("缺省 marshal 不出键（存量形态零变化）", func(t *testing.T) {
		b, err := json.Marshal(LLMConfig{ModelID: 3, Prompt: "p"})
		assert.NoError(t, err)
		assert.NotContains(t, string(b), "output_schema")
	})
	t.Run("ParseNodeConfig 带 output_schema 解析", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeLLM, json.RawMessage(
			`{"model_id":"3","prompt":"p","output_schema":[{"name":"code","type":"string","required":true,"description":"分类码"}]}`))
		assert.NoError(t, err)
		c, ok := cfg.(*LLMConfig)
		assert.True(t, ok)
		assert.Equal(t, []SchemaField{{Name: "code", Type: "string", Required: true, Description: "分类码"}}, c.OutputSchema)
	})
}

// TestLLMConfigOutputSchemaValidate 保存期校验（spec 014 FR-002）：OutputSchema 非空时
// 委托既有 ValidateSchemaFields——name 空 / 节点内重名 / type 越枚举拒；仅新键新增
// 拒绝路径，存量无声明 config 零新增拒绝。
func TestLLMConfigOutputSchemaValidate(t *testing.T) {
	t.Run("合法声明过", func(t *testing.T) {
		assert.NoError(t, LLMConfig{ModelID: 3, Prompt: "p", OutputSchema: []SchemaField{
			{Name: "code", Type: "string", Required: true},
			{Name: "score", Type: "number"},
		}}.Validate())
	})
	t.Run("name 空拒", func(t *testing.T) {
		assert.ErrorContains(t, LLMConfig{ModelID: 3, Prompt: "p",
			OutputSchema: []SchemaField{{Name: "", Type: "string"}}}.Validate(), "name")
	})
	t.Run("重名拒", func(t *testing.T) {
		assert.ErrorContains(t, LLMConfig{ModelID: 3, Prompt: "p", OutputSchema: []SchemaField{
			{Name: "code", Type: "string"}, {Name: "code", Type: "number"},
		}}.Validate(), "重名")
	})
	t.Run("type 越枚举拒", func(t *testing.T) {
		assert.ErrorContains(t, LLMConfig{ModelID: 3, Prompt: "p",
			OutputSchema: []SchemaField{{Name: "code", Type: "integer"}}}.Validate(), "type")
	})
	t.Run("空数组不触发校验（等价未声明）", func(t *testing.T) {
		assert.NoError(t, LLMConfig{ModelID: 3, Prompt: "p", OutputSchema: []SchemaField{}}.Validate())
	})
	t.Run("存量无声明 config 零新增拒绝", func(t *testing.T) {
		assert.NoError(t, LLMConfig{ModelID: 3, Prompt: "p"}.Validate())
	})
}

func TestParseNodeConfig(t *testing.T) {
	t.Run("llm happy", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeLLM, json.RawMessage(`{"model_id":"3","prompt":"判断意图：{{input}}","temperature":0}`))
		assert.NoError(t, err)
		c, ok := cfg.(*LLMConfig)
		assert.True(t, ok)
		assert.Equal(t, uint64(3), c.ModelID)
		assert.Equal(t, float64(0), c.Temperature)
	})
	t.Run("tool happy", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeTool, json.RawMessage(`{"tool_id":"12","args":{"order_id":"{{input.order_id}}"}}`))
		assert.NoError(t, err)
		c, ok := cfg.(*ToolConfig)
		assert.True(t, ok)
		assert.Equal(t, uint64(12), c.ToolID)
		assert.Equal(t, map[string]string{"order_id": "{{input.order_id}}"}, c.Args)
	})
	t.Run("condition happy", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeCondition, json.RawMessage(`{"expression":"{{classify}} == 'ORDER_QUERY'"}`))
		assert.NoError(t, err)
		c, ok := cfg.(*ConditionConfig)
		assert.True(t, ok)
		assert.Contains(t, c.Expression, "ORDER_QUERY")
	})
	t.Run("knowledge_retrieval happy", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeKnowledgeRetrieval, json.RawMessage(`{"knowledge_base_id":"7","top_k":3}`))
		assert.NoError(t, err)
		c, ok := cfg.(*KnowledgeRetrievalConfig)
		assert.True(t, ok)
		assert.Equal(t, uint64(7), c.KnowledgeBaseID)
		assert.Equal(t, 3, c.TopK)
	})
	t.Run("api happy", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeAPI, json.RawMessage(`{"url":"https://api.example.com/orders","method":"POST","headers":{"X-Request-Id":"{{input.req_id}}"},"body":"{\"id\":\"{{input.id}}\"}","timeout_sec":30,"ssl_verify":true}`))
		assert.NoError(t, err)
		c, ok := cfg.(*ApiCallConfig)
		assert.True(t, ok)
		assert.Equal(t, "https://api.example.com/orders", c.URL)
		assert.Equal(t, "POST", c.Method)
		assert.Equal(t, map[string]string{"X-Request-Id": "{{input.req_id}}"}, c.Headers)
		assert.Equal(t, `{"id":"{{input.id}}"}`, c.Body)
		assert.Equal(t, 30, c.TimeoutSec)
		assert.True(t, c.SSLVerify)
	})
	t.Run("api ssl_verify 缺省 false", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeAPI, json.RawMessage(`{"url":"https://x.com","method":"GET"}`))
		assert.NoError(t, err)
		c, ok := cfg.(*ApiCallConfig)
		assert.True(t, ok)
		assert.False(t, c.SSLVerify) // 不传 = false = 跳过证书校验（内网自签，2026-09-16 拍板）
	})
	t.Run("end happy（空配置）", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeEnd, json.RawMessage(`{}`))
		assert.NoError(t, err)
		c, ok := cfg.(*EndConfig)
		assert.True(t, ok)
		assert.Empty(t, c.Output)
	})
	t.Run("end happy（output 模板）", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeEnd, json.RawMessage(`{"output":"回复：{{reply}}"}`))
		assert.NoError(t, err)
		c, ok := cfg.(*EndConfig)
		assert.True(t, ok)
		assert.Equal(t, "回复：{{reply}}", c.Output)
	})
	t.Run("api 校验失败带类型", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeAPI, json.RawMessage(`{"method":"GET"}`)) // 缺 url
		assert.ErrorIs(t, err, errInvalidNodeConfig)
		assert.ErrorContains(t, err, "api")
		assert.ErrorContains(t, err, "url")
	})
	t.Run("未知类型", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeType("magic"), json.RawMessage(`{}`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
		assert.ErrorContains(t, err, "magic")
	})
	t.Run("坏 JSON", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeLLM, json.RawMessage(`{"model_id":`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
		assert.ErrorContains(t, err, "llm")
	})
	t.Run("字段类型错（id 未加引号）", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeLLM, json.RawMessage(`{"model_id":3,"prompt":"p"}`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
	})
	t.Run("校验失败带类型", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeTool, json.RawMessage(`{"args":{}}`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
		assert.ErrorContains(t, err, "tool")
		assert.ErrorContains(t, err, "tool_id")
	})
}

func TestValidateReqs(t *testing.T) {
	t.Run("Get/Delete/Publish/Disable/List 无跨字段规则", func(t *testing.T) {
		assert.NoError(t, GetWorkflowReq{ID: 1}.Validate())
		assert.NoError(t, DeleteWorkflowReq{ID: 1}.Validate())
		assert.NoError(t, PublishWorkflowReq{ID: 1}.Validate())
		assert.NoError(t, DisableWorkflowReq{ID: 1}.Validate())
		assert.NoError(t, ListWorkflowsReq{Page: 1, PageSize: 20}.Validate())
	})
	t.Run("UpdateWorkflowReq 缺 id 被拒", func(t *testing.T) {
		r := UpdateWorkflowReq{UpsertReq: validLinearReq()}
		err := r.Validate()
		assert.ErrorContains(t, err, "id 必填")
	})
	t.Run("UpdateWorkflowReq 委托整图规则", func(t *testing.T) {
		r := UpdateWorkflowReq{ID: 1, UpsertReq: validLinearReq()}
		r.StartNodeKey = "ghost"
		assert.ErrorContains(t, r.Validate(), "start_node_key")
	})
	t.Run("UpdateWorkflowReq 携带 type 即拒（同值）", func(t *testing.T) {
		r := UpdateWorkflowReq{ID: 1, UpsertReq: validLinearReq()}
		same := "chat"
		r.Type = &same
		assert.ErrorContains(t, r.Validate(), "type 不可变")
	})
	t.Run("UpdateWorkflowReq 携带 type 即拒（异值）", func(t *testing.T) {
		r := UpdateWorkflowReq{ID: 1, UpsertReq: validLinearReq()}
		other := "task"
		r.Type = &other
		assert.ErrorContains(t, r.Validate(), "type 不可变")
	})
}

// validLinearReq 最小合法线性图（单 llm 节点、无边）；Type=chat（spec 08 起必填）。
func validLinearReq() UpsertReq {
	return UpsertReq{
		Name:         "线性",
		Type:         WorkflowTypeChat,
		StartNodeKey: "only",
		Nodes:        []NodeReq{{Key: "only", Type: NodeLLM, Config: json.RawMessage(`{"model_id":"3","prompt":"p"}`)}},
		Edges:        []EdgeReq{},
	}
}

func TestSchemaSerialization(t *testing.T) {
	t.Run("Detail id 字符串化且空列表返 []", func(t *testing.T) {
		d := WorkflowDetailSchema{
			WorkflowSummarySchema: WorkflowSummarySchema{
				ID: "42", Name: "智能客服分流", Status: "draft",
				CreatedAt: jsonTime, UpdatedAt: jsonTime,
			},
			StartNodeKey: "classify",
			Nodes:        []NodeSchema{},
			Edges:        []EdgeSchema{},
		}
		b, err := json.Marshal(d)
		assert.NoError(t, err)
		s := string(b)
		assert.Contains(t, s, `"id":"42"`) // 字符串 id，绝不能双重编码
		assert.NotContains(t, s, `\"42\"`)
		assert.Contains(t, s, `"nodes":[]`) // 空列表 [] 不 null
		assert.Contains(t, s, `"edges":[]`)
		assert.Contains(t, s, `"start_node_key":"classify"`)
	})
	t.Run("Edge 无条件直走 condition 为 null", func(t *testing.T) {
		e := EdgeSchema{SourceNodeKey: "a", TargetNodeKey: "b"}
		b, err := json.Marshal(e)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"source_node_key":"a","target_node_key":"b","condition":null}`, string(b))
	})
	t.Run("NodeSchema config 原样透传", func(t *testing.T) {
		n := NodeSchema{Key: "classify", Type: "llm", Name: "意图识别", Config: json.RawMessage(`{"model_id":"3"}`)}
		b, err := json.Marshal(n)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"key":"classify","type":"llm","name":"意图识别","config":{"model_id":"3"}}`, string(b))
	})
}

// smartSupportReq api_contract §4 智能客服分流示例图：condition 双分支 + 分支汇流
// （router→order_api→reply 与 router→reply 两条路径同汇点）——顺带钉住
// 「汇流不是环」的语义（环检测必须用路径标记而非简单 visited）。
func smartSupportReq() UpsertReq {
	cond := func(s string) *string { return &s }
	return UpsertReq{
		Name:         "智能客服分流",
		Description:  "意图识别 → 分支 → 查单 / 通用回复",
		Type:         WorkflowTypeChat,
		StartNodeKey: "classify",
		Nodes: []NodeReq{
			{Key: "classify", Type: NodeLLM, Name: "意图识别",
				Config: json.RawMessage(`{"model_id":"3","prompt":"判断用户意图，只输出 ORDER_QUERY 或 POLICY_QUERY：{{input}}","temperature":0}`)},
			{Key: "router", Type: NodeCondition, Name: "意图分流",
				Config: json.RawMessage(`{"expression":"{{classify}} == 'ORDER_QUERY'"}`)},
			{Key: "order_api", Type: NodeTool, Name: "查询订单",
				Config: json.RawMessage(`{"tool_id":"12","args":{"order_id":"{{input.order_id}}"}}`)},
			{Key: "reply", Type: NodeLLM, Name: "生成回复",
				Config: json.RawMessage(`{"model_id":"3","prompt":"根据 {{order_api}} 的结果回复用户：{{input}}"}`)},
		},
		Edges: []EdgeReq{
			{SourceNodeKey: "classify", TargetNodeKey: "router"},
			{SourceNodeKey: "router", TargetNodeKey: "order_api", Condition: cond("true")},
			{SourceNodeKey: "router", TargetNodeKey: "reply", Condition: cond("false")},
			{SourceNodeKey: "order_api", TargetNodeKey: "reply"},
		},
	}
}

func TestUpsertValidate(t *testing.T) {
	mut := func(f func(*UpsertReq)) UpsertReq {
		r := smartSupportReq()
		f(&r)
		return r
	}

	t.Run("happy 示例图（含分支汇流）", func(t *testing.T) {
		assert.NoError(t, smartSupportReq().Validate())
	})
	t.Run("R1 节点 key 重复", func(t *testing.T) {
		r := mut(func(r *UpsertReq) {
			r.Nodes = append(r.Nodes, NodeReq{Key: "classify", Type: NodeLLM, Config: llmCfg})
		})
		assert.ErrorContains(t, r.Validate(), "classify")
	})
	t.Run("R2 坏 config 错误带节点 key", func(t *testing.T) {
		r := mut(func(r *UpsertReq) {
			for i := range r.Nodes {
				if r.Nodes[i].Key == "order_api" {
					r.Nodes[i].Config = json.RawMessage(`{"args":{}}`)
				}
			}
		})
		err := r.Validate()
		assert.ErrorContains(t, err, "order_api")
		assert.ErrorContains(t, err, "tool_id")
	})
	t.Run("R3 start 不在节点集", func(t *testing.T) {
		r := mut(func(r *UpsertReq) { r.StartNodeKey = "ghost" })
		assert.ErrorContains(t, r.Validate(), "start_node_key")
	})
	t.Run("R4 悬挂 source", func(t *testing.T) {
		r := mut(func(r *UpsertReq) {
			r.Edges = append(r.Edges, EdgeReq{SourceNodeKey: "ghost", TargetNodeKey: "reply"})
		})
		assert.ErrorContains(t, r.Validate(), "ghost")
	})
	t.Run("R4 悬挂 target", func(t *testing.T) {
		r := mut(func(r *UpsertReq) {
			r.Edges = append(r.Edges, EdgeReq{SourceNodeKey: "classify", TargetNodeKey: "ghost"})
		})
		assert.ErrorContains(t, r.Validate(), "ghost")
	})
	t.Run("R5 condition 出边缺匹配值", func(t *testing.T) {
		r := mut(func(r *UpsertReq) { r.Edges[1].Condition = nil }) // router→order_api
		assert.ErrorContains(t, r.Validate(), "router")
	})
	t.Run("R5 非 condition 出边带 condition", func(t *testing.T) {
		r := mut(func(r *UpsertReq) { c := "true"; r.Edges[0].Condition = &c }) // classify→router
		assert.ErrorContains(t, r.Validate(), "classify")
	})
	t.Run("R6 非 condition 双出边", func(t *testing.T) {
		r := mut(func(r *UpsertReq) {
			r.Edges = append(r.Edges, EdgeReq{SourceNodeKey: "classify", TargetNodeKey: "reply"})
		})
		assert.ErrorContains(t, r.Validate(), "classify")
	})
	t.Run("R7 环 a→b→a", func(t *testing.T) {
		r := UpsertReq{
			Name: "环", Type: WorkflowTypeChat, StartNodeKey: "a",
			Nodes: []NodeReq{
				{Key: "a", Type: NodeLLM, Config: llmCfg},
				{Key: "b", Type: NodeLLM, Config: llmCfg},
			},
			Edges: []EdgeReq{
				{SourceNodeKey: "a", TargetNodeKey: "b"},
				{SourceNodeKey: "b", TargetNodeKey: "a"},
			},
		}
		assert.ErrorContains(t, r.Validate(), "环")
	})
	t.Run("R7 自环", func(t *testing.T) {
		r := validLinearReq() // 单节点 only
		r.Edges = []EdgeReq{{SourceNodeKey: "only", TargetNodeKey: "only"}}
		assert.ErrorContains(t, r.Validate(), "环")
	})
	t.Run("R8 不可达孤立节点", func(t *testing.T) {
		r := mut(func(r *UpsertReq) {
			r.Nodes = append(r.Nodes, NodeReq{Key: "island", Type: NodeKnowledgeRetrieval,
				Config: json.RawMessage(`{"knowledge_base_id":"7"}`)})
		})
		assert.ErrorContains(t, r.Validate(), "island")
	})
	t.Run("end 收尾 happy（可选；示例图无 end 仍过 = 向后兼容）", func(t *testing.T) {
		// 隐式结束：单节点无出边（现行为，end 引入后仍合法）。
		assert.NoError(t, validLinearReq().Validate())
		// 显式 end 收尾：a(llm) → b(end)。
		r := UpsertReq{
			Name: "end 收尾", Type: WorkflowTypeChat, StartNodeKey: "a",
			Nodes: []NodeReq{
				{Key: "a", Type: NodeLLM, Config: llmCfg},
				{Key: "b", Type: NodeEnd, Config: json.RawMessage(`{"output":"{{a}}"}`)},
			},
			Edges: []EdgeReq{{SourceNodeKey: "a", TargetNodeKey: "b"}},
		}
		assert.NoError(t, r.Validate())
	})
	t.Run("R9 end 节点带出边被拒", func(t *testing.T) {
		// a→b(end)→c 无环且全可达，唯一违规点是 end 出边——钉住 R9 独立于 R7/R8。
		r := UpsertReq{
			Name: "end 出边", Type: WorkflowTypeChat, StartNodeKey: "a",
			Nodes: []NodeReq{
				{Key: "a", Type: NodeLLM, Config: llmCfg},
				{Key: "b", Type: NodeEnd, Config: json.RawMessage(`{}`)},
				{Key: "c", Type: NodeLLM, Config: llmCfg},
			},
			Edges: []EdgeReq{
				{SourceNodeKey: "a", TargetNodeKey: "b"},
				{SourceNodeKey: "b", TargetNodeKey: "c"},
			},
		}
		err := r.Validate()
		assert.ErrorContains(t, err, "b")
		assert.ErrorContains(t, err, "出边")
	})
}

// ---- spec 08 T004：WorkflowNodeConfig 解析 + UpsertReq.Type + SchemaField 形态 ----

func TestParseWorkflowNodeConfig(t *testing.T) {
	t.Run("合法 config（workflow_id 字符串化 + inputs 模板映射）", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeWorkflow, json.RawMessage(`{"workflow_id":"7","inputs":{"query":"{{input}}","top":"3"}}`))
		assert.NoError(t, err)
		c, ok := cfg.(*WorkflowNodeConfig)
		assert.True(t, ok)
		assert.Equal(t, uint64(7), c.WorkflowID)
		assert.Equal(t, map[string]string{"query": "{{input}}", "top": "3"}, c.Inputs)
	})
	t.Run("workflow_id 缺失（零值）拒", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeWorkflow, json.RawMessage(`{"inputs":{"input":"{{input}}"}}`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
		assert.ErrorContains(t, err, "workflow_id")
	})
	t.Run("inputs 非映射拒", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeWorkflow, json.RawMessage(`{"workflow_id":"7","inputs":"notamap"}`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
	})
	t.Run("inputs 值非字符串拒（密封 map[string]string）", func(t *testing.T) {
		_, err := ParseNodeConfig(NodeWorkflow, json.RawMessage(`{"workflow_id":"7","inputs":{"query":3}}`))
		assert.ErrorIs(t, err, errInvalidNodeConfig)
	})
	t.Run("inputs 缺省合法（键集语义归 service R11，api 只管形状）", func(t *testing.T) {
		cfg, err := ParseNodeConfig(NodeWorkflow, json.RawMessage(`{"workflow_id":"7"}`))
		assert.NoError(t, err)
		c, ok := cfg.(*WorkflowNodeConfig)
		assert.True(t, ok)
		assert.Nil(t, c.Inputs)
	})
}

func TestUpsertReqType(t *testing.T) {
	typed := func(typ WorkflowType) UpsertReq {
		r := validLinearReq()
		r.Type = typ
		return r
	}
	t.Run("type 必填", func(t *testing.T) {
		r := validLinearReq()
		r.Type = ""
		assert.ErrorContains(t, r.Validate(), "type")
	})
	t.Run("oneof chat/task 过、非法值拒", func(t *testing.T) {
		assert.NoError(t, typed(WorkflowTypeChat).Validate())
		assert.NoError(t, typed(WorkflowTypeTask).Validate())
		assert.ErrorContains(t, typed(WorkflowType("bogus")).Validate(), "type")
	})
	t.Run("Update 不携带 type 不受必填牵连（validateGraph 委托）", func(t *testing.T) {
		// Update body 不带 type：UpdateWorkflowReq.Validate 走图规则而非 UpsertReq.Validate，
		// 必填只 gate Create（携带即拒归 US3）。
		r := UpdateWorkflowReq{ID: 1, UpsertReq: validLinearReq()}
		assert.NoError(t, r.Validate())
	})
}

func TestValidateSchemaFields(t *testing.T) {
	assert.NoError(t, ValidateSchemaFields(nil), "nil schema = 未声明，合法")
	assert.NoError(t, ValidateSchemaFields([]SchemaField{
		{Name: "query", Type: "string", Required: true, Description: "查询词"},
		{Name: "top", Type: "number"},
		{Name: "verbose", Type: "boolean"},
	}))
	assert.ErrorContains(t, ValidateSchemaFields([]SchemaField{
		{Name: "query", Type: "string"}, {Name: "query", Type: "number"},
	}), "重名")
	assert.ErrorContains(t, ValidateSchemaFields([]SchemaField{{Name: "q", Type: "integer"}}), "type")
	assert.ErrorContains(t, ValidateSchemaFields([]SchemaField{{Name: "", Type: "string"}}), "name")
}

// ── 运行历史查询契约（spec 015，contracts/api.md 冻结面）──

// TestErrRunNotFoundPinned 本篇唯一新增哨兵：code = Error() 字符串，与 CLAUDE.md
// 错误码表命名空间一致（404——run 不存在或跨工作流同判，不泄露存在性，D3）。
func TestErrRunNotFoundPinned(t *testing.T) {
	assert.Equal(t, "RUN_NOT_FOUND", ErrRunNotFound.Error())
}

// TestRunsReqContract 请求形态：ListRunsReq 走 form tag（D1：api 层 cursor 只是
// string，page 工具全在 service 层）；WorkflowID 由路由参数注入、不参与 query 绑定。
// GetRunReq 两路由参数（handler strconv 注入，非法数字 400 兜底）。
func TestRunsReqContract(t *testing.T) {
	rt := reflect.TypeOf(ListRunsReq{})
	f, ok := rt.FieldByName("Limit")
	assert.True(t, ok)
	assert.Equal(t, "limit", f.Tag.Get("form"))
	f, ok = rt.FieldByName("Cursor")
	assert.True(t, ok)
	assert.Equal(t, "cursor", f.Tag.Get("form"))
	f, ok = rt.FieldByName("WorkflowID")
	assert.True(t, ok)
	assert.Equal(t, "-", f.Tag.Get("form"), "WorkflowID 不参与 query 绑定（路由注入）")
	assert.NoError(t, ListRunsReq{WorkflowID: 1, Limit: 20}.Validate())

	var gr GetRunReq
	gr.WorkflowID, gr.RunID = 1, 2
	assert.IsType(t, uint64(0), gr.WorkflowID, "路由参数 uint64（strconv 注入）")
	assert.IsType(t, uint64(0), gr.RunID)
	assert.NoError(t, gr.Validate())
}

// TestRunSummarySchemaContract 摘要面恰好 9 字段、JSON 无 input/output 键（FR-002）、
// id 字符串化不双重编码（对齐 WorkflowSummarySchema 2026-09-16 修订形态）。
func TestRunSummarySchemaContract(t *testing.T) {
	assert.Equal(t, 9, reflect.TypeOf(RunSummarySchema{}).NumField(),
		"摘要面恰好 9 字段（contracts/api.md 冻结）")

	s := RunSummarySchema{
		ID: "42", Status: "failed", TriggerSource: "chat", IsTrial: true,
		DurationMs: 1234, ErrorNode: "reply", ErrorMsg: "boom",
		StartedAt: jsonTime, CreatedAt: jsonTime,
	}
	b, err := json.Marshal(s)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))
	assert.NotContains(t, m, "input", "列表载荷禁 input 大文本（FR-002）")
	assert.NotContains(t, m, "output", "列表载荷禁 output 大文本（FR-002）")
	assert.Len(t, m, 9)
	assert.Equal(t, "42", m["id"], "id 字符串化")
	assert.Equal(t, "failed", m["status"])
	assert.Equal(t, "chat", m["trigger_source"])
	assert.Equal(t, true, m["is_trial"])
	assert.Equal(t, float64(1234), m["duration_ms"])
	assert.Equal(t, "reply", m["error_node"])
	assert.Equal(t, "boom", m["error_msg"])
	assert.Contains(t, m, "started_at", "「调用时间」列 = 执行起点")
	assert.Contains(t, m, "created_at", "落库时刻 = 排序键（D2）")
	assert.NotContains(t, string(b), `\"42\"`, "字符串 id 不得双重编码")
}

// TestRunDetailSchemaContract 详情全字段 16（含 nodes）；三可空外键 *string
//（null = 非对话触发 / 顶层运行）；空轨迹 [] 非 null。
func TestRunDetailSchemaContract(t *testing.T) {
	rt := reflect.TypeOf(RunDetailSchema{})
	assert.Equal(t, 16, rt.NumField(), "详情全字段 16（contracts/api.md 冻结）")
	for _, name := range []string{"ConversationID", "MessageID", "ParentRunID"} {
		f, ok := rt.FieldByName(name)
		assert.True(t, ok, "%s 字段须存在", name)
		assert.Equal(t, reflect.Ptr, f.Type.Kind(), "%s 须为指针（null 语义）", name)
		assert.Equal(t, reflect.String, f.Type.Elem().Kind(), "%s 须为 *string", name)
	}

	conv, msg, parent := "7", "8", "9"
	d := RunDetailSchema{
		ID: "42", Status: "failed", TriggerSource: "workflow", IsTrial: false,
		ConversationID: &conv, MessageID: &msg, TraceID: "trace-1", ParentRunID: &parent,
		Input: "in", Output: "out", ErrorNode: "api1", ErrorMsg: "err", DurationMs: 99,
		StartedAt: jsonTime, CreatedAt: jsonTime, Nodes: []NodeRunSchema{},
	}
	b, err := json.Marshal(d)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"id":"42","status":"failed","trigger_source":"workflow","is_trial":false,`+
		`"conversation_id":"7","message_id":"8","trace_id":"trace-1","parent_run_id":"9",`+
		`"input":"in","output":"out","error_node":"api1","error_msg":"err","duration_ms":99,`+
		`"started_at":"2026-09-16T08:00:00Z","created_at":"2026-09-16T08:00:00Z","nodes":[]}`, string(b))

	// 空值面：三可空 nil → null（非对话触发 / 顶层运行）；空轨迹 [] 非 null。
	d.ConversationID, d.MessageID, d.ParentRunID = nil, nil, nil
	b2, err := json.Marshal(d)
	assert.NoError(t, err)
	assert.Contains(t, string(b2), `"conversation_id":null`)
	assert.Contains(t, string(b2), `"message_id":null`)
	assert.Contains(t, string(b2), `"parent_run_id":null`)
	assert.Contains(t, string(b2), `"nodes":[]`)
}

// TestNodeRunSchemaContract 轨迹行 8 字段；error_msg 落库恒空但契约位保留
//（既有形态：错误定位走 run 级 error_msg + error_node 高亮）。
func TestNodeRunSchemaContract(t *testing.T) {
	assert.Equal(t, 8, reflect.TypeOf(NodeRunSchema{}).NumField())
	n := NodeRunSchema{
		Seq: 1, NodeKey: "classify", NodeType: "llm", Status: "succeeded",
		Input: "i", Output: "o", ErrorMsg: "", DurationMs: 5,
	}
	b, err := json.Marshal(n)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"seq":1,"node_key":"classify","node_type":"llm","status":"succeeded",`+
		`"input":"i","output":"o","error_msg":"","duration_ms":5}`, string(b))
}
