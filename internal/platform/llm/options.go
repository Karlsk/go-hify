package llm

import (
	"fmt"

	"github.com/cloudwego/eino/components/model"

	"github.com/Karlsk/go-hify/internal/platform/tool"
)

// CallOptions 单次 LLM 调用的可选参数（工具 + 生成参数）。
// 指针字段 = 未设置（沿用 provider/模型默认值）；仅内存传递，不序列化。
// nil 接收者合法（调用方零参数时传 nil，einoOptions 返回空）。
type CallOptions struct {
	Tools       []tool.ToolDef // 统一工具定义（chat 工具循环注入；eino 转换惰性缓存于本结构体）
	Temperature *float32
	TopP        *float32
	MaxTokens   int64 // 0 = 未设置

	// cache 惰性缓存的 eino 调用时选项（含 ToolDef→ToolInfo 转换）。缓存前提：
	// CallOptions 构造后调用方不得再改字段——chat 每轮循环要么复用同一 opts
	//（工具不变），要么为收尾轮新建无工具 opts，不改既有实例。
	cache *callOptionsCache
}

// callOptionsCache einoOptions 的缓存载体。
type callOptionsCache struct {
	opts []model.Option
}

// einoOptions 转换为 eino 调用时选项；只对已设置的字段生成 option，未设置的字段
// 走 adapter 默认值（不显式传 0 覆盖模型默认）。首次调用转换并缓存（错误不缓存，
// 修复后可重试），其后全调用链复用——Client 入口先调它校验，坏 tool schema 在
// 抢槽 / 熔断之前失败，不占槽位、不进熔断计数。
func (o *CallOptions) einoOptions() ([]model.Option, error) {
	if o == nil {
		return nil, nil
	}
	if o.cache != nil {
		return o.cache.opts, nil
	}
	opts := make([]model.Option, 0, 4)
	if len(o.Tools) > 0 {
		infos, err := toolDefsToEino(o.Tools)
		if err != nil {
			return nil, fmt.Errorf("convert tools: %w", err)
		}
		opts = append(opts, model.WithTools(infos))
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
	o.cache = &callOptionsCache{opts: opts}
	return opts, nil
}

// ValidateTools 试跑 ToolDef→eino 转换：chat 在进工具循环前调用，转换失败
//（理论罕见——Registry 已挡 JSON 层，这里是更深层 schema 结构的兜底）WARN 后
// 摘掉工具照常对话。与 CallOptions 惰性缓存共用同一实现，不二次转换。
func ValidateTools(defs []tool.ToolDef) error {
	_, err := (&CallOptions{Tools: defs}).einoOptions()
	return err
}
