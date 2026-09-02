package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// factory 测试：四家 adapter 离线构造（不发网络请求）+ unknown kind 哨兵 + Streamer 适配透传。

// fakeChatModel 实现 model.ChatModel，记录收到的消息数与调用选项（测 wrapper 透传）。
type fakeChatModel struct {
	streamed    int
	generated   int
	streamOpts  *model.Options
	generateOpt *model.Options
}

func (f *fakeChatModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	f.generated++
	f.generateOpt = model.GetCommonOptions(&model.Options{}, opts...)
	return msg("gen"), nil
}

func (f *fakeChatModel) Stream(_ context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.streamed = len(msgs)
	f.streamOpts = model.GetCommonOptions(&model.Options{}, opts...)
	return nil, nil
}

func (f *fakeChatModel) BindTools([]*schema.ToolInfo) error { return nil }

func TestChatModelStreamer_Delegates(t *testing.T) {
	fake := &fakeChatModel{}
	s := chatModelStreamer{m: fake}
	msgs := []*schema.Message{{Role: schema.User, Content: "hi"}}
	if _, err := s.Stream(context.Background(), msgs, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if fake.streamed != len(msgs) {
		t.Fatalf("streamed = %d, want %d (wrapper must pass messages through)", fake.streamed, len(msgs))
	}
	got, err := s.Generate(context.Background(), msgs, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.Content != "gen" || fake.generated != 1 {
		t.Fatalf("Generate delegated = (%q, %d calls)", got.Content, fake.generated)
	}
}

func TestChatModelStreamer_PassesOptions(t *testing.T) {
	fake := &fakeChatModel{}
	s := chatModelStreamer{m: fake}
	temp := float32(0.7)
	topP := float32(0.9)
	tools := []*schema.ToolInfo{{Name: "search"}}
	opts := &CallOptions{Tools: tools, Temperature: &temp, TopP: &topP, MaxTokens: 1024}

	if _, err := s.Stream(context.Background(), nil, opts); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	so := fake.streamOpts
	if len(so.Tools) != 1 || so.Tools[0].Name != "search" {
		t.Fatalf("stream tools = %v, want [search]", so.Tools)
	}
	if so.Temperature == nil || *so.Temperature != temp {
		t.Fatalf("stream temperature = %v, want %v", so.Temperature, temp)
	}
	if so.TopP == nil || *so.TopP != topP {
		t.Fatalf("stream top_p = %v, want %v", so.TopP, topP)
	}

	if _, err := s.Generate(context.Background(), nil, opts); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gopt := fake.generateOpt
	if gopt.Temperature == nil || *gopt.Temperature != temp {
		t.Fatalf("generate temperature = %v, want %v", gopt.Temperature, temp)
	}
	// MaxTokens 在 eino Options 里是 MaxTokens *int——GetCommonOptions 提取后断言
	if gopt.MaxTokens == nil || *gopt.MaxTokens != 1024 {
		t.Fatalf("generate max_tokens = %v, want 1024", gopt.MaxTokens)
	}
}

func TestNewUpstreamFactory_AllKindsConstruct(t *testing.T) {
	f := NewUpstreamFactory(NewStreamClient(NewSharedTransport()))
	cases := []struct {
		name string
		opts UpstreamOptions
	}{
		{"openai", UpstreamOptions{Kind: KindOpenAI, APIKey: "sk-test", Model: "gpt-4o"}},
		{"claude", UpstreamOptions{Kind: KindClaude, APIKey: "sk-ant-test", Model: "claude-sonnet-4-5", BaseURL: "https://api.anthropic.com"}},
		{"gemini", UpstreamOptions{Kind: KindGemini, APIKey: "gm-test", Model: "gemini-2.0-flash"}},
		{"ollama", UpstreamOptions{Kind: KindOllama, BaseURL: "http://localhost:11434", Model: "llama3.1"}},
	}
	for _, tc := range cases {
		s, err := f(tc.opts)
		if err != nil {
			t.Fatalf("%s: construct: %v", tc.name, err)
		}
		if s == nil {
			t.Fatalf("%s: nil Streamer", tc.name)
		}
	}
}

func TestNewUpstreamFactory_UnsupportedKind(t *testing.T) {
	f := NewUpstreamFactory(NewStreamClient(NewSharedTransport()))
	_, err := f(UpstreamOptions{Kind: "cohere", Model: "command-r"})
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Fatalf("err = %v, want errors.Is ErrUnsupportedKind", err)
	}
}
