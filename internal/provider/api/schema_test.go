package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// 常量钉住：DB CHECK / 前端枚举 / eino 适配都依赖这些字符串值，改动即破坏性变更。
func TestConstantsPinned(t *testing.T) {
	cases := []struct{ got, want string }{
		{KindOpenAI, "openai"},
		{KindClaude, "claude"},
		{KindGemini, "gemini"},
		{KindOllama, "ollama"},
		{KindOpenAICompatible, "openai_compatible"},
		{CapabilityChat, "chat"},
		{CapabilityEmbedding, "embedding"},
		{SourceDiscovered, "discovered"},
		{SourceManual, "manual"},
		{HealthUnknown, "unknown"},
		{HealthUp, "up"},
		{HealthDegraded, "degraded"},
		{HealthDown, "down"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("常量漂移：got %q, want %q", c.got, c.want)
		}
	}
}

func TestValidateProvider_Create(t *testing.T) {
	cases := []struct {
		name    string
		req     CreateProviderReq
		wantErr bool
	}{
		{"openai 带密钥", CreateProviderReq{Name: "OpenAI", Kind: KindOpenAI, APIKey: "sk-xxx"}, false},
		{"openai 缺密钥", CreateProviderReq{Name: "OpenAI", Kind: KindOpenAI}, true},
		{"claude 缺密钥", CreateProviderReq{Name: "Claude", Kind: KindClaude}, true},
		{"gemini 缺密钥", CreateProviderReq{Name: "Gemini", Kind: KindGemini}, true},
		{"ollama 无密钥合法", CreateProviderReq{Name: "本机", Kind: KindOllama}, false},
		{"openai_compatible 缺 base_url", CreateProviderReq{Name: "vLLM", Kind: KindOpenAICompatible}, true},
		{"openai_compatible 齐全", CreateProviderReq{Name: "vLLM", Kind: KindOpenAICompatible, BaseURL: "http://localhost:8000/v1"}, false},
		{"名称为空", CreateProviderReq{Name: "", Kind: KindOpenAI, APIKey: "sk-xxx"}, true},
		{"kind 非法", CreateProviderReq{Name: "Azure", Kind: "azure", APIKey: "xxx"}, true},
		{"base_url 协议非法", CreateProviderReq{Name: "OpenAI", Kind: KindOpenAI, APIKey: "sk-xxx", BaseURL: "ftp://api.openai.com"}, true},
		{"base_url https 合法", CreateProviderReq{Name: "OpenAI", Kind: KindOpenAI, APIKey: "sk-xxx", BaseURL: "https://api.openai.com/v1"}, false},
	}
	for _, c := range cases {
		err := c.req.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

func TestValidateProvider_Update(t *testing.T) {
	cases := []struct {
		name    string
		req     UpdateProviderReq
		wantErr bool
	}{
		{"合法更新（api_key 空 = 不变）", UpdateProviderReq{ID: 1, Name: "OpenAI"}, false},
		{"ID 缺失", UpdateProviderReq{Name: "OpenAI"}, true},
		{"名称为空", UpdateProviderReq{ID: 1, Name: "", Enabled: true}, true},
		{"base_url 协议非法", UpdateProviderReq{ID: 1, Name: "x", BaseURL: "ws://a"}, true},
		// 更新不含 kind，keep_alive 的 kind 限定跳过（service 取实体后兜底），api 校验放行
		{"更新带 keep_alive 放行（service 兜底）", UpdateProviderReq{ID: 1, Name: "x", ExtraConfig: map[string]any{"keep_alive": "30m"}}, false},
	}
	for _, c := range cases {
		err := c.req.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

// ValidateWithKind 用真实 kind 补验：Validate 放行的请求在此被拒。
func TestValidateProvider_UpdateWithKind(t *testing.T) {
	cases := []struct {
		name    string
		req     UpdateProviderReq
		kind    string
		wantErr bool
	}{
		{"openai 合法", UpdateProviderReq{ID: 1, Name: "OpenAI"}, KindOpenAI, false},
		{"openai_compatible 缺 base_url", UpdateProviderReq{ID: 1, Name: "vLLM"}, KindOpenAICompatible, true},
		{"keep_alive 非 ollama 被拒", UpdateProviderReq{ID: 1, Name: "x", ExtraConfig: map[string]any{"keep_alive": "30m"}}, KindOpenAI, true},
		{"keep_alive ollama 放行", UpdateProviderReq{ID: 1, Name: "x", ExtraConfig: map[string]any{"keep_alive": "30m"}}, KindOllama, false},
		{"ID 缺失", UpdateProviderReq{Name: "x"}, KindOpenAI, true},
	}
	for _, c := range cases {
		err := c.req.ValidateWithKind(c.kind)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

func TestValidateListProvidersReq(t *testing.T) {
	cases := []struct {
		name    string
		req     ListProvidersReq
		wantErr bool
	}{
		{"无筛选", ListProvidersReq{}, false},
		{"kind 合法", ListProvidersReq{Kind: KindClaude}, false},
		{"kind 非法", ListProvidersReq{Kind: "azure"}, true},
	}
	for _, c := range cases {
		err := c.req.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

func TestValidateExtraConfig(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		extra   map[string]any
		wantErr bool
	}{
		{"空 map", KindOpenAI, map[string]any{}, false},
		{"nil map", KindOpenAI, nil, false},
		{"bulkhead 合法", KindOpenAI, map[string]any{"bulkhead": float64(16)}, false},
		{"bulkhead 下界", KindOpenAI, map[string]any{"bulkhead": float64(1)}, false},
		{"bulkhead 上界", KindOpenAI, map[string]any{"bulkhead": float64(128)}, false},
		{"bulkhead 超 上界", KindOpenAI, map[string]any{"bulkhead": float64(129)}, true},
		{"bulkhead 零值", KindOpenAI, map[string]any{"bulkhead": float64(0)}, true},
		{"bulkhead 非整数", KindOpenAI, map[string]any{"bulkhead": 1.5}, true},
		{"bulkhead 字符串", KindOpenAI, map[string]any{"bulkhead": "16"}, true},
		{"bulkhead 布尔", KindOpenAI, map[string]any{"bulkhead": true}, true},
		{"ttft_seconds 合法", KindOpenAI, map[string]any{"ttft_seconds": float64(30)}, false},
		{"ttft_seconds 上界", KindOllama, map[string]any{"ttft_seconds": float64(600)}, false},
		{"ttft_seconds 超 上界", KindOllama, map[string]any{"ttft_seconds": float64(601)}, true},
		{"keep_alive ollama 合法", KindOllama, map[string]any{"keep_alive": "30m"}, false},
		{"keep_alive 非 ollama", KindOpenAI, map[string]any{"keep_alive": "30m"}, true},
		{"keep_alive 非字符串", KindOllama, map[string]any{"keep_alive": float64(5)}, true},
		{"未知键", KindOpenAI, map[string]any{"timeout": float64(30)}, true},
	}
	for _, c := range cases {
		err := validateExtraConfig(c.kind, c.extra)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

func TestValidateModel(t *testing.T) {
	dim := int32(1536)
	cases := []struct {
		name    string
		req     CreateModelReq
		wantErr bool
	}{
		{"chat 无维度", CreateModelReq{ProviderID: 1, Name: "GPT-4o", ModelID: "gpt-4o", Capability: CapabilityChat}, false},
		{"chat 带维度", CreateModelReq{ProviderID: 1, Name: "GPT-4o", ModelID: "gpt-4o", Capability: CapabilityChat, EmbeddingDim: &dim}, true},
		{"embedding 带维度", CreateModelReq{ProviderID: 1, Name: "text-embedding-3", ModelID: "text-embedding-3-small", Capability: CapabilityEmbedding, EmbeddingDim: &dim}, false},
		{"embedding 缺维度", CreateModelReq{ProviderID: 1, Name: "text-embedding-3", ModelID: "text-embedding-3-small", Capability: CapabilityEmbedding}, true},
		{"价格整数部分合法", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("5"), OutputPrice: strPtr("15")}, false},
		{"价格小数合法", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("0.075")}, false},
		{"价格零合法", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("0")}, false},
		{"价格小数超 4 位", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("1.23456")}, true},
		{"价格负数", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("-1")}, true},
		{"价格非数字", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("free")}, true},
		{"价格整数超 8 位", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr("123456789")}, true},
		{"价格缺整数部分", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, InputPrice: strPtr(".5")}, true},
		{"think_level 合法", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, ExtraParams: map[string]any{"think_level": "high"}}, false},
		{"think_level 非法值", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, ExtraParams: map[string]any{"think_level": "ultra"}}, true},
		{"think_level 非字符串", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, ExtraParams: map[string]any{"think_level": 3}}, true},
		{"extra_params 未知键", CreateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat, ExtraParams: map[string]any{"top_k": 1}}, true},
	}
	for _, c := range cases {
		err := c.req.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
	}
}

func TestValidateModel_UpdateID(t *testing.T) {
	if err := (UpdateModelReq{ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat}).Validate(); err == nil {
		t.Error("ID 缺失应报错")
	}
	if err := (UpdateModelReq{ID: 1, ProviderID: 1, Name: "x", ModelID: "x", Capability: CapabilityChat}).Validate(); err != nil {
		t.Errorf("合法更新不应报错: %v", err)
	}
}

// Schema 序列化安全：id 字符串化；永不出现密文字段或明文密钥。
func TestProviderSchema_JSONNoSecrets(t *testing.T) {
	s := ProviderSchema{}
	s.ID = "12345678901234567" // 超过 2^53 的 id 也不丢精度（字符串化）
	s.Name = "OpenAI"
	s.Kind = KindOpenAI
	s.HasAPIKey = true
	s.ExtraConfig = map[string]any{}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)
	for _, want := range []string{`"id":"12345678901234567"`, `"has_api_key":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("JSON 缺少 %s：%s", want, body)
		}
	}
	for _, banned := range []string{"api_key_encrypted", `"api_key":`, "sk-plaintext", "auth_config"} {
		if strings.Contains(body, banned) {
			t.Errorf("JSON 泄露敏感字段 %s：%s", banned, body)
		}
	}
}

// 详情聚合 JSON 形态：provider 字段扁平展开（无嵌套 provider 对象）、models 空为 []、
// health 缺省为 null（从未探测）；同样不出现密文。
func TestProviderDetailSchema_JSON(t *testing.T) {
	d := ProviderDetailSchema{Models: []ModelSchema{}, Health: nil}
	d.ID = "1"
	d.Name = "OpenAI"
	d.Kind = KindOpenAI
	d.HasAPIKey = true
	d.ExtraConfig = map[string]any{}

	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"name":"OpenAI"`, `"models":[]`, `"health":null`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 缺少 %s：%s", want, s)
		}
	}
	for _, banned := range []string{"api_key_encrypted", `"api_key":`, "auth_config", `{"ProviderSchema"`} {
		if strings.Contains(s, banned) {
			t.Errorf("JSON 不应出现 %s：%s", banned, s)
		}
	}
}

func strPtr(s string) *string { return &s }
