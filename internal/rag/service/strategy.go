// strategy.go —— 切分策略配置（jsonb 序列化）：KB 级粒度，一期仅 fixed_length。
// 存储在 knowledge_bases.chunk_strategy（jsonb），空 {} 降级读全局默认（RagCfg）。
package service

// ChunkStrategy 切分策略配置（jsonb 序列化到 knowledge_bases.chunk_strategy）。
// 一期仅支持 type="fixed_length"，二期可扩展新策略类型（jsonb 天然前向兼容）。
type ChunkStrategy struct {
	// Type 策略类型枚举：一期仅 "fixed_length"（段落→句子→硬截三级降级，MD 围栏原子保护）。
	Type string `json:"type"`
	// ChunkSize 目标块大小（rune），0=用全局默认（RAG_CHUNK_SIZE）。
	ChunkSize int `json:"chunk_size"`
	// ChunkOverlap 块间重叠尾缀（rune），0=用全局默认（RAG_CHUNK_OVERLAP）。
	ChunkOverlap int `json:"chunk_overlap"`
	// Separator 段落分隔符（字符串），""=用默认 "\n\n"；"\n"=单换行切；" "=空格切。
	// 影响 splitParagraphs 的首优先级切分，句子降级和硬截逻辑不变。
	Separator string `json:"separator"`
}

// ChunkConfig 全局默认值来源（RagCfg 的子集）。
type ChunkConfig struct {
	DefaultSize    int // 全局 RAG_CHUNK_SIZE 默认值
	DefaultOverlap int // 全局 RAG_CHUNK_OVERLAP 默认值
}

// Resolve 降级解析：0 值 / 空串字段回退到全局默认。返回值保证 size > 0, 0 <= overlap < size, sep 非空。
func (s ChunkStrategy) Resolve(cfg ChunkConfig) (size, overlap int, sep string) {
	size = s.ChunkSize
	if size <= 0 {
		size = cfg.DefaultSize
	}
	if size <= 0 {
		size = 500 // 绝对兜底（config.mustValidate 已保证 > 0，此处防御不可达）
	}

	overlap = s.ChunkOverlap
	if overlap < 0 {
		overlap = cfg.DefaultOverlap
	}
	if overlap >= size {
		overlap = cfg.DefaultOverlap
	}
	if overlap >= size {
		overlap = size / 10 // 绝对兜底：10%
	}

	sep = s.Separator
	if sep == "" {
		sep = "\n\n"
	}
	return
}

// IsEmpty 策略是否全空（未配置），调用方据此决定是否用全局默认。
func (s ChunkStrategy) IsEmpty() bool {
	return s.Type == "" && s.ChunkSize == 0 && s.ChunkOverlap == 0 && s.Separator == ""
}
