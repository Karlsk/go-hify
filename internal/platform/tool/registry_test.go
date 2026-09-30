package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// fakeSource 脚本化 Source：固定清单 + 调用记录 + 可配失败。
type fakeSource struct {
	defs     []ToolDef
	loadErr  error        // Tools() 失败注入
	invokeFn func(call ToolCall) ToolResult
	invoked  []ToolCall
}

func (f *fakeSource) Tools(context.Context) ([]ToolDef, error) { return f.defs, f.loadErr }

func (f *fakeSource) Invoke(_ context.Context, call ToolCall) ToolResult {
	f.invoked = append(f.invoked, call)
	if f.invokeFn != nil {
		return f.invokeFn(call)
	}
	return ToolResult{CallID: call.ID, Name: call.Name, Content: "ok"}
}

func rawSchema(s string) json.RawMessage { return json.RawMessage(s) }

func TestNewRegistry(t *testing.T) {
	tests := []struct {
		name    string
		sources []Source
		wantErr bool
		wantDef []ToolDef
	}{
		{
			name:    "single source ok",
			sources: []Source{&fakeSource{defs: []ToolDef{{Name: "s1__get", Description: "get", InputSchema: rawSchema(`{"type":"object","properties":{"q":{"type":"string"}}}`)}}}},
			wantDef: []ToolDef{{Name: "s1__get", Description: "get", InputSchema: rawSchema(`{"type":"object","properties":{"q":{"type":"string"}}}`)}},
		},
		{
			name: "multi source merged",
			sources: []Source{
				&fakeSource{defs: []ToolDef{{Name: "a", InputSchema: rawSchema(`{}`)}}},
				&fakeSource{defs: []ToolDef{{Name: "b", InputSchema: nil}}},
			},
			wantDef: []ToolDef{{Name: "a", InputSchema: rawSchema(`{}`)}, {Name: "b"}},
		},
		{name: "empty source ok", sources: []Source{&fakeSource{defs: nil}}, wantDef: []ToolDef{}},
		{
			name:    "source load error",
			sources: []Source{&fakeSource{loadErr: errors.New("db down")}},
			wantErr: true,
		},
		{
			name:    "invalid json schema rejected",
			sources: []Source{&fakeSource{defs: []ToolDef{{Name: "x", InputSchema: rawSchema(`{broken`)}}}},
			wantErr: true,
		},
		{
			name:    "non-object top level rejected",
			sources: []Source{&fakeSource{defs: []ToolDef{{Name: "x", InputSchema: rawSchema(`[1,2]`)}}}},
			wantErr: true,
		},
		{
			name:    "null schema allowed",
			sources: []Source{&fakeSource{defs: []ToolDef{{Name: "x", InputSchema: rawSchema(`null`)}}}},
			wantDef: []ToolDef{{Name: "x", InputSchema: rawSchema(`null`)}},
		},
		{
			name: "duplicate name rejected",
			sources: []Source{
				&fakeSource{defs: []ToolDef{{Name: "dup", InputSchema: rawSchema(`{}`)}}},
				&fakeSource{defs: []ToolDef{{Name: "dup", InputSchema: rawSchema(`{}`)}}},
			},
			wantErr: true,
		},
		{name: "empty name rejected", sources: []Source{&fakeSource{defs: []ToolDef{{Name: "", InputSchema: rawSchema(`{}`)}}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewRegistry(context.Background(), tt.sources...)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (defs=%v)", r.defs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := r.Defs()
			if len(got) != len(tt.wantDef) {
				t.Fatalf("defs len = %d, want %d (%v)", len(got), len(tt.wantDef), got)
			}
			for i := range got {
				if got[i].Name != tt.wantDef[i].Name {
					t.Errorf("defs[%d].Name = %q, want %q", i, got[i].Name, tt.wantDef[i].Name)
				}
			}
		})
	}
}

func TestDefsReturnsCopy(t *testing.T) {
	src := &fakeSource{defs: []ToolDef{{Name: "a", InputSchema: rawSchema(`{}`)}}}
	r, err := NewRegistry(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	d1 := r.Defs()
	d1[0].Name = "mutated"
	if got := r.Defs()[0].Name; got != "a" {
		t.Fatalf("Defs() leaked internal state: %q", got)
	}
}

func TestInvokeRouting(t *testing.T) {
	okSrc := &fakeSource{defs: []ToolDef{{Name: "s1__get", InputSchema: rawSchema(`{}`)}}}
	errSrc := &fakeSource{
		defs: []ToolDef{{Name: "s2__boom", InputSchema: rawSchema(`{}`)}},
		invokeFn: func(call ToolCall) ToolResult {
			return ToolResult{CallID: call.ID, Name: call.Name, IsError: true, Content: "tool exploded"}
		},
	}
	r, err := NewRegistry(context.Background(), okSrc, errSrc)
	if err != nil {
		t.Fatal(err)
	}

	// 命中 okSrc：透传其结果
	call := ToolCall{ID: "c1", Name: "s1__get", Arguments: rawSchema(`{"q":"hi"}`)}
	res := r.Invoke(context.Background(), call)
	if res.IsError || res.Content != "ok" || res.CallID != "c1" {
		t.Fatalf("invoke ok path = %+v", res)
	}
	if len(okSrc.invoked) != 1 || !reflect.DeepEqual(okSrc.invoked[0].Arguments, call.Arguments) {
		t.Fatalf("invoked = %+v", okSrc.invoked)
	}

	// 命中 errSrc：IsError 结果原样透传（错误喂回 LLM）
	res = r.Invoke(context.Background(), ToolCall{ID: "c2", Name: "s2__boom"})
	if !res.IsError || res.Content != "tool exploded" {
		t.Fatalf("invoke error path = %+v", res)
	}

	// 未知名：IsError 结果（LLM 幻觉工具名的真实场景）
	res = r.Invoke(context.Background(), ToolCall{ID: "c3", Name: "nope"})
	if !res.IsError || res.CallID != "c3" || res.Name != "nope" {
		t.Fatalf("unknown name = %+v", res)
	}
	if len(okSrc.invoked) != 1 || len(errSrc.invoked) != 1 {
		t.Fatalf("unknown name must not reach sources: ok=%d err=%d", len(okSrc.invoked), len(errSrc.invoked))
	}
}
