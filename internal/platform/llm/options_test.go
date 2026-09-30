package llm

import (
	"testing"

	"github.com/cloudwego/eino/components/model"

	"github.com/Karlsk/go-hify/internal/platform/tool"
)

func TestCallOptionsEinoOptionsNil(t *testing.T) {
	var o *CallOptions
	got, err := o.einoOptions()
	if err != nil || len(got) != 0 {
		t.Fatalf("nil receiver options = (%v, %v), want empty", got, err)
	}
	o = &CallOptions{}
	got, err = o.einoOptions()
	if err != nil || len(got) != 0 {
		t.Fatalf("empty options = (%v, %v), want empty (未设置字段不生成 option)", got, err)
	}
}

func TestCallOptionsEinoOptionsPartial(t *testing.T) {
	temp := float32(0.5)
	o := &CallOptions{Temperature: &temp} // 只设 temperature
	got, err := o.einoOptions()
	if err != nil {
		t.Fatalf("einoOptions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("options = %d, want 1", len(got))
	}
	eo := model.GetCommonOptions(&model.Options{}, got...)
	if eo.Temperature == nil || *eo.Temperature != temp {
		t.Fatalf("temperature = %v, want %v", eo.Temperature, temp)
	}
	if eo.TopP != nil || eo.MaxTokens != nil || eo.Tools != nil {
		t.Fatalf("未设置字段不得生成 option: %+v", eo)
	}
}

func TestCallOptionsEinoOptionsAll(t *testing.T) {
	temp, topP := float32(0.7), float32(0.9)
	defs := []tool.ToolDef{{
		Name:        "search",
		Description: "搜索",
		InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}}
	o := &CallOptions{Tools: defs, Temperature: &temp, TopP: &topP, MaxTokens: 2048}
	got, err := o.einoOptions()
	if err != nil {
		t.Fatalf("einoOptions: %v", err)
	}
	eo := model.GetCommonOptions(&model.Options{}, got...)
	if len(eo.Tools) != 1 || eo.Tools[0].Name != "search" {
		t.Fatalf("tools = %v, want [search]", eo.Tools)
	}
	if eo.Temperature == nil || *eo.Temperature != temp {
		t.Fatalf("temperature = %v, want %v", eo.Temperature, temp)
	}
	if eo.TopP == nil || *eo.TopP != topP {
		t.Fatalf("top_p = %v, want %v", eo.TopP, topP)
	}
	if eo.MaxTokens == nil || *eo.MaxTokens != 2048 {
		t.Fatalf("max_tokens = %v, want 2048", eo.MaxTokens)
	}
}

func TestCallOptionsMaxTokensZeroUnset(t *testing.T) {
	o := &CallOptions{MaxTokens: 0} // 0 = 未设置
	got, err := o.einoOptions()
	if err != nil || len(got) != 0 {
		t.Fatalf("MaxTokens=0 options = (%v, %v), want 0（不得用 0 覆盖模型默认）", got, err)
	}
}

// 转换成功后缓存复用：第二次调用不得重新转换（同一切片直接返回）。
func TestCallOptionsEinoOptionsCached(t *testing.T) {
	defs := []tool.ToolDef{{Name: "t", InputSchema: []byte(`{}`)}}
	o := &CallOptions{Tools: defs}
	first, err := o.einoOptions()
	if err != nil {
		t.Fatalf("first einoOptions: %v", err)
	}
	second, err := o.einoOptions()
	if err != nil {
		t.Fatalf("second einoOptions: %v", err)
	}
	if len(first) == 0 || len(second) != len(first) {
		t.Fatalf("cache miss: first=%d second=%d", len(first), len(second))
	}
}

// 坏 schema：转换报错且不缓存（修复后可重试——此处换好 schema 应当成功）。
func TestCallOptionsBadToolSchemaNotCached(t *testing.T) {
	o := &CallOptions{Tools: []tool.ToolDef{{Name: "t", InputSchema: []byte(`{"type":"ob`)}}}
	if _, err := o.einoOptions(); err == nil {
		t.Fatal("want error for invalid json schema")
	}
	if o.cache != nil {
		t.Fatal("失败不得写入缓存")
	}
	o.Tools = []tool.ToolDef{{Name: "t", InputSchema: []byte(`{}`)}}
	if _, err := o.einoOptions(); err != nil {
		t.Fatalf("修复后重试应成功: %v", err)
	}
}

func TestValidateTools(t *testing.T) {
	ok := []tool.ToolDef{
		{Name: "a", InputSchema: []byte(`{"type":"object","properties":{"x":{"type":"integer"}}}`)},
		{Name: "b"}, // 空 schema = 无参工具
	}
	if err := ValidateTools(ok); err != nil {
		t.Fatalf("valid tools: %v", err)
	}
	bad := []tool.ToolDef{{Name: "c", InputSchema: []byte(`not-json`)}}
	if err := ValidateTools(bad); err == nil {
		t.Fatal("want error for invalid schema")
	}
}
