package api

// spec 03 §1：KnowledgeBaseService 方法集钉死。方法数 13：KB CRUD 5 + 文档 7 + Retrieve 1
//——软删改造 v2（用户批准的 spec 修订）增补 DisableDocument / EnableDocument（文档深度
//停用 / 重新启用，可逆下架；DELETE 改真删）。增删方法必须先走 spec 修订（用户显式
//批准），测试在编译期之外多一道运行期闸门。

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestKnowledgeBaseServiceMethodSet 方法集（名称 × 签名）逐字钉死。
func TestKnowledgeBaseServiceMethodSet(t *testing.T) {
	typ := reflect.TypeOf((*KnowledgeBaseService)(nil)).Elem() // 指针类型方法集为空，取接口本体

	want := map[string]string{
		"Create":           "func(context.Context, api.CreateKnowledgeBaseReq) (*api.KnowledgeBaseSchema, error)",
		"Get":              "func(context.Context, api.GetKnowledgeBaseReq) (*api.KnowledgeBaseSchema, error)",
		"List":             "func(context.Context, api.ListKnowledgeBasesReq) (*api.KnowledgeBaseListResult, error)",
		"Update":           "func(context.Context, api.UpdateKnowledgeBaseReq) (*api.KnowledgeBaseSchema, error)",
		"Delete":           "func(context.Context, api.DeleteKnowledgeBaseReq) error",
		"UploadDocument":   "func(context.Context, api.UploadDocumentReq) (*api.DocumentSchema, error)",
		"GetDocument":      "func(context.Context, api.GetDocumentReq) (*api.DocumentDetailSchema, error)",
		"ListDocuments":    "func(context.Context, api.ListDocumentsReq) (*api.DocumentListResult, error)",
		"DeleteDocument":   "func(context.Context, api.DeleteDocumentReq) error",
		"DisableDocument":  "func(context.Context, api.DisableDocumentReq) error",
		"EnableDocument":   "func(context.Context, api.EnableDocumentReq) (*api.DocumentSchema, error)",
		"ReindexDocument":  "func(context.Context, api.ReindexDocumentReq) (*api.DocumentSchema, error)",
		"Retrieve":         "func(context.Context, api.RetrieveReq) ([]api.RetrievedChunk, error)",
	}
	assert.Equal(t, len(want), typ.NumMethod(), "方法集必须恰好 13 个（spec 03 §1 清单 + 软删 v2 修订）")

	got := make(map[string]string, typ.NumMethod())
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		got[m.Name] = m.Type.String()
	}
	assert.Equal(t, want, got)
}

// 编译期断言：接口签名可赋值性——Req/Schema 名写错即编译失败（双保险，防字符串 typo）。
var _ = func() func(context.Context, RetrieveReq) ([]RetrievedChunk, error) {
	var svc KnowledgeBaseService
	return svc.Retrieve
}
