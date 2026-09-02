package llm

import (
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// CallOptions 单次 LLM 调用的可选参数（工具 + 生成参数）。
// 指针字段 = 未设置（沿用 provider/模型默认值）；仅内存传递，不序列化。
// nil 接收者合法（调用方零参数时传 nil，einoOptions 返回空）。
type CallOptions struct {
	Tools       []*schema.ToolInfo // 可调用的 MCP 工具（chat 工具循环注入）
	Temperature *float32
	TopP        *float32
	MaxTokens   int64 // 0 = 未设置
}

// einoOptions 转换为 eino 调用时选项；只对已设置的字段生成 option，
// 未设置的字段走 adapter 默认值（不显式传 0 覆盖模型默认）。
func (o *CallOptions) einoOptions() []model.Option {
	if o == nil {
		return nil
	}
	opts := make([]model.Option, 0, 4)
	if len(o.Tools) > 0 {
		opts = append(opts, model.WithTools(o.Tools))
	}
	if o.Temperature != nil {
		opts = append(opts, model.WithTemperature(*o.Temperature))
	}
	if o.TopP != nil {
		opts = append(opts, model.WithTopP(*o.TopP))
	}
	if o.MaxTokens > 0 {
		opts = append(opts, model.WithMaxTokens(int(o.MaxTokens))) // 64 位平台 int64→int 无损
	}
	return opts
}
