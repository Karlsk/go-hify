package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Registry 聚合各来源（Source）的工具：构建时校验（InputSchema 合法性、调用名唯一），
// Invoke 按调用名路由回来源。chat 每轮对话构建一次；构建失败调用方降级为无工具。
// 未来内置工具上线时把多个 Source 一起传入即可（chat 侧零改动）。
type Registry struct {
	defs    []ToolDef
	sources map[string]Source // 调用名 → 来源（路由索引）
}

// NewRegistry 逐来源加载工具清单并建路由索引。任一失败即整体失败（返回 error，
// 调用方 WARN 后降级为无工具——工具不是对话硬依赖，mirror chat RAG 降级先例）：
//   - Source.Tools() 出错（DB 故障等）；
//   - InputSchema 非合法 JSON 或顶层不是对象（垃圾 schema 在这里挡下，
//     不流到 LLM 调用里炸——discover 未试跑转换的兜底）；
//   - 调用名重复（路由歧义；MCP 侧调用名带 server 前缀，仅极端 sanitize 碰撞可达）。
func NewRegistry(ctx context.Context, sources ...Source) (*Registry, error) {
	r := &Registry{sources: make(map[string]Source)}
	for _, src := range sources {
		defs, err := src.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("load tools from source: %w", err)
		}
		for _, d := range defs {
			if err := validateDef(d); err != nil {
				return nil, err
			}
			if _, dup := r.sources[d.Name]; dup {
				return nil, fmt.Errorf("duplicate tool name %q", d.Name)
			}
			r.sources[d.Name] = src
			r.defs = append(r.defs, d)
		}
	}
	return r, nil
}

// Defs 工具定义清单（拷贝，防调用方改动内部状态）；空 = 无工具。
func (r *Registry) Defs() []ToolDef {
	out := make([]ToolDef, len(r.defs))
	copy(out, r.defs)
	return out
}

// Invoke 按调用名路由执行。未知名（LLM 幻觉出未提供的工具是真实场景）不 panic
// 不 error——返回 IsError 结果喂回 LLM 自纠。
func (r *Registry) Invoke(ctx context.Context, call ToolCall) ToolResult {
	src, ok := r.sources[call.Name]
	if !ok {
		return ToolResult{CallID: call.ID, Name: call.Name, IsError: true,
			Content: fmt.Sprintf("未知工具 %q：不在可用工具清单内", call.Name)}
	}
	return src.Invoke(ctx, call)
}

// validateDef 工具定义入册校验：调用名非空 + InputSchema 合法 JSON 且顶层为对象。
// 空清单（nil / "null"）归一为 {}（MCP 无参工具）。
func validateDef(d ToolDef) error {
	if d.Name == "" {
		return fmt.Errorf("tool name empty")
	}
	schema := d.InputSchema
	if len(schema) == 0 || string(schema) == "null" {
		return nil // 无参数形态合法（Source 侧保证非 nil，这里再兜底）
	}
	var top map[string]any
	if err := json.Unmarshal(schema, &top); err != nil {
		return fmt.Errorf("tool %q input_schema invalid json: %w", d.Name, err)
	}
	if top == nil {
		return nil // "null" 之外的 nil 形态（理论不可达）按无参放行
	}
	return nil
}
