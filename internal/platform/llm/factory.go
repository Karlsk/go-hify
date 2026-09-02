package llm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"
)

// chatModelStreamer 把 eino model.ChatModel 适配成 Streamer：
// Stream/Generate 的变参 model.Option 与 Streamer 接口的 *CallOptions 差一个转换层，
// 此包装负责 CallOptions → eino 调用时选项（工具与生成参数按调用传入，不做构造期绑定）。
type chatModelStreamer struct {
	m model.ChatModel
}

// Stream 实现 Streamer：生命周期与传入 ctx 绑定，ctx 取消即打断上游 Recv。opts 可为 nil。
func (a chatModelStreamer) Stream(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*schema.StreamReader[*schema.Message], error) {
	return a.m.Stream(ctx, msgs, opts.einoOptions()...)
}

// Generate 实现 Streamer：非流式单次生成（workflow LLM 节点用）。opts 可为 nil。
func (a chatModelStreamer) Generate(ctx context.Context, msgs []*schema.Message, opts *CallOptions) (*schema.Message, error) {
	return a.m.Generate(ctx, msgs, opts.einoOptions()...)
}

// NewUpstreamFactory 构造注入 Manager 的 UpstreamFactory：
// 按 opts.Kind 建对应 eino adapter，并注入定制 HTTP client（流式壳，无 Timeout——
// 超时全由 ctx 三层驱动，见 httpx.go）。构造为纯内存校验，不发网络请求。
func NewUpstreamFactory(hc *http.Client) UpstreamFactory {
	return func(opts UpstreamOptions) (Streamer, error) {
		cm, err := newChatModel(opts, hc)
		if err != nil {
			return nil, err
		}
		return chatModelStreamer{m: cm}, nil
	}
}

// newChatModel 按 kind 构造 eino ChatModel（一期只走各家原生 API：
// claude 不走 Bedrock / Vertex，gemini 不走 Vertex AI）。
func newChatModel(opts UpstreamOptions, hc *http.Client) (model.ChatModel, error) {
	// eino 构造函数签名要求 ctx，但构造期无网络调用，用 Background 即可。
	ctx := context.Background()
	switch opts.Kind {
	case KindOpenAI:
		// 注意：eino openai adapter 的 BaseURL 字段仅 Azure 场景使用，
		// 非 Azure 官方端点无自定义入口，故 openai 的 opts.BaseURL 暂不映射。
		return openai.NewChatModel(ctx, &openai.ChatModelConfig{
			APIKey:     opts.APIKey,
			Model:      opts.Model,
			HTTPClient: hc,
		})
	case KindClaude:
		cfg := &claude.Config{
			APIKey:     opts.APIKey,
			Model:      opts.Model,
			HTTPClient: hc,
		}
		if opts.BaseURL != "" {
			cfg.BaseURL = &opts.BaseURL // claude adapter 的 BaseURL 是 *string
		}
		return claude.NewChatModel(ctx, cfg)
	case KindGemini:
		// gemini adapter 要求外部构造 genai.Client 注入；Backend 缺省即 Gemini API（API Key 路径）。
		gc, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:     opts.APIKey,
			HTTPClient: hc,
		})
		if err != nil {
			return nil, fmt.Errorf("create gemini client: %w", err)
		}
		return gemini.NewChatModel(ctx, &gemini.Config{Client: gc, Model: opts.Model})
	case KindOllama:
		return ollama.NewChatModel(ctx, &ollama.ChatModelConfig{
			BaseURL:    opts.BaseURL,
			Model:      opts.Model,
			HTTPClient: hc,
		})
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedKind, opts.Kind)
	}
}
