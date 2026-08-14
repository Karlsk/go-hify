package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// schema 测试：统一校验（创建 / 更新共用 validateNameStatus）、枚举常量钉住、
// ID 字符串化序列化（防 JS 超 2^53 丢精度）。

func TestValidateNameStatus_CreateUpdateShared(t *testing.T) {
	// 统一校验：CreateReq 与 UpdateReq 走同一规则（validateNameStatus）。
	cases := []struct {
		status  string
		wantErr bool
	}{
		{StatusDraft, false},
		{StatusActive, false},
		{StatusArchived, false},
		{"", true},        // 空 status
		{"unknown", true}, // 非枚举值
		{"DRAFT", true},   // 大小写敏感
	}
	for _, tc := range cases {
		if err := (CreateReq{Name: "n", Status: tc.status}).Validate(); (err != nil) != tc.wantErr {
			t.Fatalf("CreateReq.Validate(status=%q) err = %v, wantErr %v", tc.status, err, tc.wantErr)
		}
		if err := (UpdateReq{ID: 1, Name: "n", Status: tc.status}).Validate(); (err != nil) != tc.wantErr {
			t.Fatalf("UpdateReq.Validate(status=%q) err = %v, wantErr %v", tc.status, err, tc.wantErr)
		}
	}

	// 空 name 也报跨字段错（binding required 之外的服务层防线）。
	if err := (CreateReq{Name: "", Status: StatusDraft}).Validate(); err == nil {
		t.Fatal("CreateReq with empty name should fail Validate")
	}

	// UpdateReq.ID=0 视为非法（handler 从路径参数赋值，正常不会为 0；Validate 兜底）。
	if err := (UpdateReq{ID: 0, Name: "n", Status: StatusDraft}).Validate(); err == nil {
		t.Fatal("UpdateReq with zero ID should fail Validate")
	}

	// 错误信息列出合法枚举值。
	err := CreateReq{Name: "n", Status: "bad"}.Validate()
	if err == nil || !strings.Contains(err.Error(), StatusDraft) {
		t.Fatalf("err = %v, want message listing %s", err, StatusDraft)
	}
}

func TestValidate_NoCrossFieldRules(t *testing.T) {
	// Get/Delete/List 当前无跨字段规则，Validate 恒 nil。
	if err := (GetReq{ID: 1}).Validate(); err != nil {
		t.Fatalf("GetReq.Validate = %v, want nil", err)
	}
	if err := (DeleteReq{ID: 1}).Validate(); err != nil {
		t.Fatalf("DeleteReq.Validate = %v, want nil", err)
	}
	if err := (ListReq{}).Validate(); err != nil {
		t.Fatalf("ListReq.Validate = %v, want nil", err)
	}
}

func TestStatusConstantsPinned(t *testing.T) {
	// 枚举值与 migrations/00008_demo_items.sql 的 CHECK 约束一一对应，钉住防漂移。
	if StatusDraft != "draft" || StatusActive != "active" || StatusArchived != "archived" {
		t.Fatalf("status constants drifted: %s / %s / %s", StatusDraft, StatusActive, StatusArchived)
	}
}

func TestDemoItemSchema_JSON(t *testing.T) {
	// ID 必须序列化为字符串（防 JS 超 2^53 丢精度，CLAUDE.md《字段命名与类型》）。
	b, err := json.Marshal(DemoItemSchema{ID: "18446744073709551615", Name: "n", Status: StatusDraft})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"id":"18446744073709551615"`) {
		t.Fatalf("id must serialize as string, got: %s", b)
	}
}
