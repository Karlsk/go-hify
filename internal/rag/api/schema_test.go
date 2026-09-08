package api

// spec 03（backend_spec_03_api_contract）单测：纯契约叶子包——哨兵/常量钉死 + Validate 表驱动。
// 验收门：包独立编译不 import gin/gorm（叶子包纪律）；测试风格对齐 agent/api（testify）。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// boolPtr 测试辅助（*bool 三态构造）。
func boolPtr(b bool) *bool { return &b }

// spec 03 §3：哨兵错误 code（= error.code，前端靠它分支）逐字钉死。
func TestSentinelCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{ErrKnowledgeBaseNotFound, "KNOWLEDGE_BASE_NOT_FOUND"},
		{ErrKnowledgeBaseNameConflict, "KNOWLEDGE_BASE_NAME_CONFLICT"},
		{ErrKnowledgeBaseInUse, "KNOWLEDGE_BASE_IN_USE"},
		{ErrDocumentNotFound, "DOCUMENT_NOT_FOUND"},
		{ErrDocumentProcessing, "DOCUMENT_PROCESSING"},
		{ErrEmbeddingModelMismatch, "EMBEDDING_MODEL_MISMATCH"},
		{ErrEmbeddingDimMismatch, "EMBEDDING_DIM_MISMATCH"},
	}
	for _, c := range cases {
		assert.Equal(t, c.code, c.err.Error())
	}
}

// spec 03 §2：应用层常量钉死（与 DB CHECK / 前端约束对齐，漂移即坏）。
func TestConstantsPinned(t *testing.T) {
	assert.Equal(t, 1536, RequiredEmbeddingDim)
	assert.Equal(t, 128, MaxKBNameLen)
	assert.Equal(t, 512, MaxKBDescriptionLen)
	assert.Equal(t, 255, MaxDocumentNameLen)
	assert.Equal(t, 1024, MaxQueryRunes)
	assert.Equal(t, 10, MaxRetrieveKBs)
	assert.Equal(t, 1, TopKMin)
	assert.Equal(t, 20, TopKMax)
	assert.ElementsMatch(t, []string{".txt", ".md"}, AllowedUploadExts)
}

// spec 03 §2：Update 的 id 必填（json:"-" 防路径覆盖）与 Enabled *bool 三态语义。
func TestUpdateKnowledgeBaseReqValidate(t *testing.T) {
	valid := UpdateKnowledgeBaseReq{ID: 1, Name: "产品手册"}
	assert.NoError(t, valid.Validate())

	noID := UpdateKnowledgeBaseReq{Name: "产品手册"}
	assert.ErrorContains(t, noID.Validate(), "id 必填")

	// Enabled 三态：nil=不变；显式 true/false 均合法——Validate 不区分。
	assert.NoError(t, (UpdateKnowledgeBaseReq{ID: 1, Name: "x"}).Validate())
	assert.NoError(t, (UpdateKnowledgeBaseReq{ID: 1, Name: "x", Enabled: boolPtr(true)}).Validate())
	assert.NoError(t, (UpdateKnowledgeBaseReq{ID: 1, Name: "x", Enabled: boolPtr(false)}).Validate())
}

// UpdateKnowledgeBaseReq.ID 带 json:"-"：序列化不泄漏 id 键；反序列化时 body 的
// {"id":999} 不得绑定（防覆盖——encoding/json 对无 tag 字段按字段名大小写不敏感匹配）。
func TestUpdateKnowledgeBaseReqJSONShape(t *testing.T) {
	b, err := json.Marshal(UpdateKnowledgeBaseReq{ID: 42, Name: "手册", Enabled: boolPtr(false)})
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"name":"手册"`)
	assert.Contains(t, s, `"enabled":false`)
	assert.NotContains(t, s, `"id"`)

	var got UpdateKnowledgeBaseReq
	assert.NoError(t, json.Unmarshal([]byte(`{"id":999,"name":"x"}`), &got))
	assert.Zero(t, got.ID, "body 注入的 id 不得被绑定")
	assert.Equal(t, "x", got.Name)
}

// spec 03 §2：Create/List/Get/Delete 无跨字段规则——Validate 恒 nil
// （binding tag 管字段格式，仓库惯例无规则仍显式声明 Validate）。
func TestKBReqValidateTrivial(t *testing.T) {
	assert.NoError(t, (CreateKnowledgeBaseReq{Name: "a", EmbeddingModelID: 1}).Validate())
	assert.NoError(t, (ListKnowledgeBasesReq{}).Validate())
	assert.NoError(t, (GetKnowledgeBaseReq{ID: 1}).Validate())
	assert.NoError(t, (DeleteKnowledgeBaseReq{ID: 1}).Validate())
}

// newUploadReq 上传请求构造器（合法基线 + 按需变异）。
func newUploadReq(mutate func(*UploadDocumentReq)) UploadDocumentReq {
	r := UploadDocumentReq{
		KnowledgeBaseID: 1,
		FileName:        "manual.txt",
		Content:         "正文内容",
		FileType:        "txt",
		FileSize:        24,
	}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

// spec 03 §2：上传扩展名白名单（小写比较；取扩展名而非 mime）+ FileType 一致性。
func TestUploadDocumentReqExtWhitelist(t *testing.T) {
	cases := []struct {
		name     string
		fileName string
		fileType string
		wantErr  bool
	}{
		{"小写 txt", "a.txt", "txt", false},
		{"大写 TXT", "b.TXT", "txt", false},
		{"混合 Md", "c.Md", "md", false},
		{"小写 md", "d.md", "md", false},
		{"pdf 拒", "e.pdf", "pdf", true},
		{"无扩展名拒", "noext", "", true},
		{"双扩展名取最后一段拒", "f.txt.exe", "exe", true},
		{"FileType 与扩展名不一致拒", "g.txt", "md", true},
		{"FileType 未小写拒", "h.txt", "TXT", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newUploadReq(func(u *UploadDocumentReq) { u.FileName, u.FileType = c.fileName, c.fileType })
			err := r.Validate()
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// spec 03 §2：Name 空则回退文件名（取 Base 去路径）≤255；显式 Name 保留。
func TestUploadDocumentReqNameFallback(t *testing.T) {
	r := newUploadReq(func(u *UploadDocumentReq) { u.FileName = "docs/manual.txt"; u.Name = "" })
	assert.NoError(t, r.Validate())
	assert.Equal(t, "manual.txt", r.Name, "空 Name 回退文件名 Base")

	kept := newUploadReq(func(u *UploadDocumentReq) { u.Name = "自定义名称" })
	assert.NoError(t, kept.Validate())
	assert.Equal(t, "自定义名称", kept.Name)

	long := strings.Repeat("名", MaxDocumentNameLen+1)
	tooLong := newUploadReq(func(u *UploadDocumentReq) { u.FileName = long + ".txt"; u.Name = "" })
	assert.ErrorContains(t, tooLong.Validate(), "255")
}

// spec 03 §2：Content 去 BOM + 首尾空白后回写；纯空白 / 仅 BOM / 非法 UTF-8 拒。
func TestUploadDocumentReqContentNormalization(t *testing.T) {
	r := newUploadReq(func(u *UploadDocumentReq) { u.Content = "\uFEFF  正文 \n\t" })
	assert.NoError(t, r.Validate())
	assert.Equal(t, "正文", r.Content, "BOM 剥离 + TrimSpace 回写")

	blank := newUploadReq(func(u *UploadDocumentReq) { u.Content = "  \n\t " })
	assert.ErrorContains(t, blank.Validate(), "content")

	bomOnly := newUploadReq(func(u *UploadDocumentReq) { u.Content = "\uFEFF" })
	assert.ErrorContains(t, bomOnly.Validate(), "content")

	badUTF8 := newUploadReq(func(u *UploadDocumentReq) { u.Content = "坏\xff\xfe" })
	assert.ErrorContains(t, badUTF8.Validate(), "UTF-8")
}

// 程序内构造防御：KnowledgeBaseID 必填（handler 装配遗漏即在此暴露）+ FileSize 必为正。
func TestUploadDocumentReqRequiredFields(t *testing.T) {
	noKB := newUploadReq(func(u *UploadDocumentReq) { u.KnowledgeBaseID = 0 })
	assert.ErrorContains(t, noKB.Validate(), "knowledge_base_id")

	zeroSize := newUploadReq(func(u *UploadDocumentReq) { u.FileSize = 0 })
	assert.ErrorContains(t, zeroSize.Validate(), "file_size")
}

// 文档族其余 Req：Get/Delete/Reindex 无跨字段规则；ListDocuments 校验 KB 归属必填。
func TestDocumentReqValidate(t *testing.T) {
	assert.NoError(t, (GetDocumentReq{ID: 1}).Validate())
	assert.NoError(t, (DeleteDocumentReq{ID: 1}).Validate())
	assert.NoError(t, (ReindexDocumentReq{ID: 1}).Validate())

	assert.NoError(t, (ListDocumentsReq{KnowledgeBaseID: 1, Limit: 20}).Validate())
	assert.ErrorContains(t, (ListDocumentsReq{}).Validate(), "knowledge_base_id")
}

// ---- T4：响应族 + Retrieve 契约 ----

// spec 03 §2：KB 响应族——外键字符串化（embedding_model_id）+ Enabled + DocumentCount
// （详情端点 3 也带 DocumentCount）；ListItem 聚合 EmbeddingModelName（悬空 ""），
// embed 展平；ListResult 喂 OKWithOffset。
func TestKnowledgeBaseSchemaJSONShape(t *testing.T) {
	kb := KnowledgeBaseSchema{
		Name:             "产品手册",
		Description:      "内部文档",
		EmbeddingModelID: "42",
		Enabled:          true,
		DocumentCount:    3,
	}
	kb.ID = "7"
	b, err := json.Marshal(kb)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"id":"7"`)
	assert.Contains(t, s, `"name":"产品手册"`)
	assert.Contains(t, s, `"embedding_model_id":"42"`)
	assert.Contains(t, s, `"enabled":true`)
	assert.Contains(t, s, `"document_count":3`)
	assert.Contains(t, s, `"created_at"`)
	assert.Contains(t, s, `"updated_at"`)

	item := KnowledgeBaseListItem{KnowledgeBaseSchema: kb, EmbeddingModelName: "text-embedding"}
	b2, err := json.Marshal(item)
	assert.NoError(t, err)
	s2 := string(b2)
	assert.Contains(t, s2, `"embedding_model_name":"text-embedding"`)
	assert.Contains(t, s2, `"name":"产品手册"`) // embed 展平，非嵌套对象

	res := KnowledgeBaseListResult{Items: []KnowledgeBaseListItem{item}, Page: 1, PageSize: 20, Total: 1}
	b3, err := json.Marshal(res)
	assert.NoError(t, err)
	s3 := string(b3)
	assert.Contains(t, s3, `"items"`)
	assert.Contains(t, s3, `"page":1`)
	assert.Contains(t, s3, `"page_size":20`)
	assert.Contains(t, s3, `"total":1`)
}

// spec 03 §2：文档响应族——status 四态 / error_message / file_type / file_size /
// chunk_count；Detail +Content；ListResult 自声明（不引 page 包）喂 OKWithCursor。
func TestDocumentSchemaJSONShape(t *testing.T) {
	doc := DocumentSchema{
		Name:         "manual.txt",
		FileType:     "txt",
		FileSize:     1024,
		Status:       "ready",
		ChunkCount:   8,
		ErrorMessage: "",
	}
	doc.ID = "9"
	b, err := json.Marshal(doc)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"id":"9"`)
	assert.Contains(t, s, `"name":"manual.txt"`)
	assert.Contains(t, s, `"file_type":"txt"`)
	assert.Contains(t, s, `"file_size":1024`)
	assert.Contains(t, s, `"status":"ready"`)
	assert.Contains(t, s, `"chunk_count":8`)
	assert.Contains(t, s, `"error_message":""`)

	detail := DocumentDetailSchema{DocumentSchema: doc, Content: "正文全文"}
	b2, err := json.Marshal(detail)
	assert.NoError(t, err)
	s2 := string(b2)
	assert.Contains(t, s2, `"content":"正文全文"`)
	assert.Contains(t, s2, `"name":"manual.txt"`) // embed 展平

	lr := DocumentListResult{Items: []DocumentSchema{doc}, Limit: 20, HasMore: true, NextCursor: "abc"}
	b3, err := json.Marshal(lr)
	assert.NoError(t, err)
	s3 := string(b3)
	assert.Contains(t, s3, `"items"`)
	assert.Contains(t, s3, `"limit":20`)
	assert.Contains(t, s3, `"has_more":true`)
	assert.Contains(t, s3, `"next_cursor":"abc"`)
}

// spec 03 §2：RetrievedChunk 三 ID 字符串化 + DocumentName（二次查询，悬空 ""）+
// Similarity = 1 - 余弦距离。
func TestRetrievedChunkJSONShape(t *testing.T) {
	rc := RetrievedChunk{
		ChunkID:        "11",
		DocumentID:     "9",
		KnowledgeBaseID: "7",
		DocumentName:   "manual.txt",
		ChunkIndex:     3,
		Content:        "片段内容",
		Similarity:     0.87,
	}
	b, err := json.Marshal(rc)
	assert.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"chunk_id":"11"`)
	assert.Contains(t, s, `"document_id":"9"`)
	assert.Contains(t, s, `"knowledge_base_id":"7"`)
	assert.Contains(t, s, `"document_name":"manual.txt"`)
	assert.Contains(t, s, `"chunk_index":3`)
	assert.Contains(t, s, `"content":"片段内容"`)
	assert.Contains(t, s, `"similarity":0.87`)
}

// newRetrieveReq 检索请求构造器（合法基线 + 按需变异）。
func newRetrieveReq(mutate func(*RetrieveReq)) RetrieveReq {
	r := RetrieveReq{Query: "如何配置 Agent", KBIDs: []uint64{1}}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

// kbIDs 构造 1..n 的 KB id 列表。
func kbIDs(n int) []uint64 {
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	return ids
}

// spec 03 §2：RetrieveReq 校验——query 非空（trim 后）且 rune ≤1024（中文友好）；
// kb_ids 程序内必经 1..10。
func TestRetrieveReqValidate(t *testing.T) {
	assert.NoError(t, newRetrieveReq(nil).Validate())

	assert.Error(t, newRetrieveReq(func(r *RetrieveReq) { r.Query = "" }).Validate())
	assert.Error(t, newRetrieveReq(func(r *RetrieveReq) { r.Query = "   \n\t" }).Validate())

	// rune 计：恰好 1024 个中文过，1025 拒（报错含上限值）。
	assert.NoError(t, newRetrieveReq(func(r *RetrieveReq) { r.Query = strings.Repeat("问", MaxQueryRunes) }).Validate())
	long := newRetrieveReq(func(r *RetrieveReq) { r.Query = strings.Repeat("问", MaxQueryRunes+1) })
	assert.ErrorContains(t, long.Validate(), "1024")

	// kb_ids 边界：0 拒 / 1 过 / 10 过 / 11 拒。
	zero := newRetrieveReq(func(r *RetrieveReq) { r.KBIDs = nil })
	assert.ErrorContains(t, zero.Validate(), "kb_ids")
	assert.NoError(t, newRetrieveReq(func(r *RetrieveReq) { r.KBIDs = kbIDs(MaxRetrieveKBs) }).Validate())
	eleven := newRetrieveReq(func(r *RetrieveReq) { r.KBIDs = kbIDs(MaxRetrieveKBs + 1) })
	assert.ErrorContains(t, eleven.Validate(), "10")
}

// RetrieveReq JSON 形状：query/top_k 可从 body 绑定；KBIDs json:"-" 程序内填
// （handler 绑路径 → 复用跨 KB 契约），body 注入不生效（spec 05 §3）。
func TestRetrieveReqJSONShape(t *testing.T) {
	var got RetrieveReq
	assert.NoError(t, json.Unmarshal([]byte(`{"query":"q","top_k":5,"kb_ids":[99]}`), &got))
	assert.Equal(t, "q", got.Query)
	assert.Equal(t, 5, got.TopK)
	assert.Empty(t, got.KBIDs, "body 注入的 kb_ids 不得被绑定")

	b, err := json.Marshal(got)
	assert.NoError(t, err)
	assert.NotContains(t, string(b), `"kb_ids"`, "序列化不泄漏程序内字段")
	assert.Contains(t, string(b), `"query":"q"`)
}
