package api

import (
	"encoding/json"
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

	// 节点类型与 CHECK 对齐（00016 建四类，00017 加宽补 api/end，2026-09-16 拍板）。
	assert.Equal(t, "llm", string(NodeLLM))
	assert.Equal(t, "tool", string(NodeTool))
	assert.Equal(t, "condition", string(NodeCondition))
	assert.Equal(t, "knowledge_retrieval", string(NodeKnowledgeRetrieval))
	assert.Equal(t, "api", string(NodeAPI))
	assert.Equal(t, "end", string(NodeEnd))
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
}

// validLinearReq 最小合法线性图（单 llm 节点、无边）。
func validLinearReq() UpsertReq {
	return UpsertReq{
		Name:         "线性",
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
			Name: "环", StartNodeKey: "a",
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
			Name: "end 收尾", StartNodeKey: "a",
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
			Name: "end 出边", StartNodeKey: "a",
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
