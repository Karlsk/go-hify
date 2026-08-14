package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// factory 测试：四家 adapter 离线构造（不发网络请求）+ unknown kind 哨兵 + Streamer 适配透传。

// fakeChatModel 实现 model.ChatModel，仅记录 Stream 收到的消息数（测 wrapper 透传）。
type fakeChatModel struct {
	streamed int
}

func (f *fakeChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, nil
}

func (f *fakeChatModel) Stream(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.streamed = len(msgs)
	return nil, nil
}

func (f *fakeChatModel) BindTools([]*schema.ToolInfo) error { return nil }

func TestChatModelStreamer_Delegates(t *testing.T) {
	fake := &fakeChatModel{}
	s := chatModelStreamer{m: fake}
	msgs := []*schema.Message{{Role: schema.User, Content: "hi"}}
	if _, err := s.Stream(context.Background(), msgs); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if fake.streamed != len(msgs) {
		t.Fatalf("streamed = %d, want %d (wrapper must pass messages through)", fake.streamed, len(msgs))
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
