# RAG spec 08 · 组装与验收（backend_spec_08_assembly）

> 状态：**实施 spec**（2026-09-07），8 篇之 08（末篇）；决策依据见总览 [backend_module_spec.md](backend_module_spec.md)。前置依赖：01-07 全部。
> 交付：组合根装配（server.go）+ 路由注册 + 两道人工验收门（骨架冒烟 / 端到端 rag-manual-test.md）。本篇零业务逻辑，只连线与验证。

## 1. 组合根装配（internal/app/server.go）

- 装配插 agent 块（:117-122）后、chat 块（:124）前——依赖方向 `rag → provider, platform`，不依赖 agent/chat：

```go
ragStore  := ragstore.New(gormDB)
ragCache  := cache.New(rdb, cache.DefaultConfig())
embedder  := llm.NewEmbedder(llmTransport)          // 复用共享 Transport
ragSvc, ragRecovery := ragsvc.New(ragStore, modelSvc, embedder, ragCache, ragCfg)
ragRecovery.MarkInterruptedFailed(appCtx)            // 失败仅 WARN（07）
// ... handler 注册插 agenthandler 与 chathandler 之间，删 :160 TODO 的 rag 字样
```

- import 别名：`ragapi / ragsvc / ragstore / raghandler`（仓规 `<module><layer>`）。
- 装配顺序在模块内：store → service（注入 provider 的 modelSvc + llm 的 embedder）→ handler RegisterRoutes。
- 两波说明（决策 18 仍有效）：若按「骨架波提交」节奏，05 完成后先做本篇 §2 骨架装配（当时 New 尚单返回、无 Recovery 行、:160 TODO 删 rag 字样）并过 §3 冒烟提交；07 合入后补双返回 + Recovery 行 + 接线，过 §4 端到端。若 01-07 连续做完，直接从 §4 开始。

## 2. 路由注册

- `raghandler.New(ragSvc, cfg.Rag.MaxUploadBytes).RegisterRoutes(rg)`——两组路由分开注册（`/knowledge-bases/:id/...` 与 `/documents/:id`），组内无 `:id` 与静态段同级冲突（04）。
- 全部 11 端点在 `/api/v1` 下、auth 中间件之后（组合根先挂中间件再 RegisterRoutes 的既有顺序）。

## 3. 骨架冒烟（curl，可提交门）

建 KB 201（enabled=true）→ 列表分页 + `?name=` 模糊命中 → 详情 200 → PUT 改 name/enabled 200 → 删有文档 KB 409 / 删空 KB 204 → 上传 .md 202 **status=pending**（file_type/file_size 回显）→ 上传 .pdf / 超 2MB 400 → 文档列表游标分页 + 详情（content / chunk_count=0）→ 软删文档 204 → `POST /knowledge-bases/{id}/retrieve` 空库返回 `[]`。

## 4. 端到端验收（完整闭环）

1. `make migrate-up`（00011 applied；`\d document_chunks` 确认改名+新列）→ `go test -race -cover ./...` 全绿（≥80%）。
2. 起 dev 服务，照 **docs/testing/rag-manual-test.md**（新建，照 agent-manual-test.md 结构）curl 冒烟：
   - 造 provider + embedding 模型 → 建 KB 201（enabled=true）→ 传含独有事实的 MD（如「Hify 退货流程：7 天内联系客服，运费由公司承担」）→ 202 **status=pending** → 轮询见 pending→processing→ready（看 chunk_count > 0、file_type/file_size 回显）；
   - **检索验收**：`{"query":"退货要自己出运费吗"}` → top1 含「运费由公司承担」且带 DocumentName，similarity 明显高于无关 query（语义改写命中，非关键词匹配）；
   - **disabled 剔除**：PUT KB enabled=false → 同 query 返回 `[]`；改回 true 恢复命中；
   - **name 模糊**：`GET /knowledge-bases?name=手册` 命中「产品手册库」；
   - 负向：重名 409 / .pdf 400 / 超 2MB 400 / 删有文档 KB 409 / processing 中 reindex 409；
   - 生命周期：reindex→ready 且 chunk_count 不变；DELETE 文档后 retrieve 不再命中其内容（**验证软删联动删 chunks**：`SELECT count(*) FROM document_chunks WHERE document_id=?` = 0）；处理中重启服务→failed + error_message 含「服务重启中断」。
3. SQL 侧：`EXPLAIN (ANALYZE)` 确认召回走 idx_document_chunks_embedding（小表 Seq Scan 属正常——[pgvector_quickstart.md](pgvector_quickstart.md) 坑 1）。

## 5. 验收门与风险指针

- 门：§3 骨架冒烟全过（可提交）；§4 端到端全过 + rag-manual-test.md 落盘（模块完结）。
- 风险汇总见各篇风险节；跨篇两条：既有环境必须**先 migrate 再起服务**（00011 新列+改名，否则显式列清单 SELECT 报列不存在）；`ResolveLLMConfig` 明文凭据只在调用瞬间存在，禁止缓存/入日志（仓规）。
