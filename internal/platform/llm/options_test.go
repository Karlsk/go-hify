package llm

import (
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestCallOptionsEinoOptionsNil(t *testing.T) {
	var o *CallOptions
	if got := o.einoOptions(); len(got) != 0 {
		t.Fatalf("nil receiver options = %v, want empty", got)
	}
	o = &CallOptions{}
	if got := o.einoOptions(); len(got) != 0 {
		t.Fatalf("empty options = %v, want empty (未设置字段不生成 option)", got)
	}
}

func TestCallOptionsEinoOptionsPartial(t *testing.T) {
	temp := float32(0.5)
	o := &CallOptions{Temperature: &temp} // 只设 temperature
	got := o.einoOptions()
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
	tools := []*schema.ToolInfo{{Name: "search"}}
	o := &CallOptions{Tools: tools, Temperature: &temp, TopP: &topP, MaxTokens: 2048}
	eo := model.GetCommonOptions(&model.Options{}, o.einoOptions()...)
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
	if got := o.einoOptions(); len(got) != 0 {
		t.Fatalf("MaxTokens=0 options = %d, want 0（不得用 0 覆盖模型默认）", len(got))
	}
}
