# Chat 模块 · RAG 检索注入 spec（rag_injection）

> 状态：**已实施完成**（2026-09-14 起草并经用户拍板 D1-D4 全部采纳推荐项；实施完成见 [agent/kb_binding_crud.md](../agent/kb_binding_crud.md) + [chat/rag_injection_impl.md](rag_injection_impl.md)）。需求原文按 Java/Spring 概念表述，本 spec 完成 Java → Hify 概念映射与现状对齐后给出实施设计。
> 相关既有文档：chat 编排见 [context_and_history_storage.md](context_and_history_storage.md)；rag 检索契约见 [../rag/backend_spec_05_retrieval.md](../rag/backend_spec_05_retrieval.md)；agent 数据模型见 [../agent/db_model.md](../agent/db_model.md)。

## 0. 需求原文与概念映射

需求原文（Java 概念加粗）：

> 修改 **ChatService** 的 **buildMessages** 方法，在 System Prompt 后插入 RAG 检索结果。
> - 检查 Agent 是否有 **knowledgeBaseId**；没有就跳过，直接返回原始 system prompt
> - 有的话：把用户消息向量化，调 **document_chunk** 相似度查询，topK=3，过滤相似度低于 0.75 的结果
> - 把检索到的 chunk 拼进 system prompt（模板见 §4.4）
> - 不要修改流式调用、**SseEmitter** 转发、消息存储的逻辑。不要改 **Controller** 层。

映射表：

| 需求原文（Java 概念） | Hify 落点 | 说明 |
|---|---|---|
| ChatService | `internal/chat/service` 的 `chatService`，发消息编排在 `turn.go` 的 `runTurn` | |
| buildMessages | `turn.go` 的包级纯函数 `assembleMessages`（turn.go:263） | 拼的是 eino `[]*schema.Message` |
| Agent.knowledgeBaseId（单数） | **M:N**：`agent_knowledge_bases` 表（00004 建，PK(agent_id, knowledge_base_id)，双向 CASCADE） | 无单数字段；绑定 0..N 个 KB，检索传全部 |
| 用户消息向量化 + document_chunk 相似度查询 | `ragapi.KnowledgeBaseService.Retrieve(ctx, RetrieveReq{Query, TopK, KBIDs})` | 向量化（ResolveLLMConfig → EmbedStrings → 维度校验）与 pgvector 召回**全部已在 rag service 内**；chat 不直接碰 document_chunks 表（跨模块禁查表，仓规） |
| 过滤相似度低于 0.75 | rag Retrieve **无服务端阈值**（返回原样 top-k，`Similarity = 1 - 余弦距离`），过滤在 chat 注入路径做 | 检索测试弹窗等 HTTP 路径不受影响 |
| SseEmitter 转发 | `emit func(chatapi.StreamEvent) error` 回调 | 不动 |
| Controller 层 | `internal/chat/handler` | 不动（组合根装配一行除外，见 §5） |

**范围修正（必须先确认）**：需求说"只改 buildMessages 一个方法"，但本仓该目标不可达——`assembleMessages` 是纯函数，无法访问 KB 绑定；且 `agent_knowledge_bases` 表虽存在，agent 模块 CRUD **完全没有暴露绑定读写**（Create/Update 请求无 `knowledge_base_ids` 字段、Get 不返回、store 无方法、前端表单无入口）。因此本 spec 拆为：

- **前置 A：agent 模块 KB 绑定 CRUD**（§3）——绑定的写入与读取能力；
- **主任务 B：chat 检索注入**（§4）——需求的本体。

不做 A 则 B 无数据来源（读不到绑定）、无使用入口（绑不上 KB）。

## 1. 现状盘点（探索结论）

- **表已就绪**：`agent_knowledge_bases`（00004）：`agent_id REFERENCES agents ON DELETE CASCADE`、`knowledge_base_id REFERENCES knowledge_bases ON DELETE CASCADE`、`idx_agent_kbs_kb` 反查索引、COMMENT 齐全。rag 的 KB Delete 已依赖其 FK CASCADE 清绑定。
- **agent 模块未暴露**：`AgentService` 接口、Create/Update Req、`AgentDetailSchema`、store、handler、前端 `agent.ts` / `AgentList.vue` 均只有 `tool_ids`，无任何 KB 绑定痕迹（仅 Delete 注释提到 CASCADE）。绑定粒度 = KB（对齐 agent db_model 决策 #6「绑定即授权」的精神；CLAUDE.md 资源清单预留的 `/agents/{id}/knowledge-bases` 子路由不采用——沿用 body 内嵌 `tool_ids` 的既有模式与决策 §4.3）。
- **rag Retrieve 已为 chat 预留**（api 注释原文"为 chat / workflow 预留；HTTP 单 KB 路由复用同一契约"）：`KBIDs []uint64`（程序内填充，1..MaxRetrieveKBs=10）、disabled KB 静默剔除、混嵌入模型 → `ErrEmbeddingModelMismatch`、embedding 侧哨兵原样上抛、`TopK` 传 0 走 cfg 默认并 clamp。
- **chat 侧**：`assembleMessages` 拼 `[system(agent.SystemPrompt 现取)] + 截断历史 + 当前 user`；system prompt 不落库（每轮由 agent 配置拼装）；`chatService` 依赖为 store/agents/providers/clients/execs 五项，无 rag。
- **依赖方向合法**：依赖清单 `chat → rag` ✓、`chat → agent` ✓。装配顺序 `server.go` 中 ragSvc（:144）先于 chatSvc（:155），注入无障碍。
- **agent ↛ rag**：agent 不得依赖 rag——绑定写入的存在性校验只能靠 FK 23503 兜底翻译（`ErrToolNotFound` vs mcp_tools 的既有同款先例），不能前置调 rag api 校验。

## 2. 决策点汇总（已拍板，2026-09-14 用户确认，全部采纳推荐）

| # | 决策点 | 结论 |
|---|---|---|
| D1 | 前置 A 是否含前端（AgentList 编辑弹窗 KB 多选） | **含前端**——否则功能无使用入口，只能 SQL 手插数据 |
| D2 | chat 检索失败（embedding 失败 / 混模型 Mismatch / DB 错误）的语义 | **降级**：不注入资料照常对话 + WARN 日志（RAG 非硬依赖；对齐 CLAUDE.md 语义缓存"embedding 失败降级为普通对话"先例） |
| D3 | agent 的 system_prompt 为空且有命中时 | **仍注入资料段**（无前缀 prompt，只有"请基于以下参考资料…"起）——资料对空 prompt agent 仍有价值 |
| D4 | `AgentListItem` 是否加 `kb_count` 聚合列 | **加**——对齐 `tool_count` 先例，列表可见绑定数 |

钉死值（需求给定，不再讨论）：topK=3、相似度阈值 0.75、prompt 模板原文。

## 3. 前置 A：agent 模块 KB 绑定 CRUD

### 3.1 api 契约（internal/agent/api）

```go
// schema.go 增补
const (
    // MaxKBBindings 单 Agent 绑定 KB 数上限——必须 ≤ ragapi.MaxRetrieveKBs(10)：
    // 绑超 10 个则 chat 检索必撞 RetrieveReq.Validate 的 kb_ids 上限，运行期才爆；
    // 绑定期就挡。数值上取等（10）。
    MaxKBBindings = 10
)

// CreateAgentReq / UpdateAgentReq 各增（对齐 tool_ids 模式）：
KnowledgeBaseIDs []uint64 `json:"knowledge_base_ids" binding:"omitempty,max=10,dive,gt=0"`

// AgentDetailSchema 增：
KnowledgeBaseIDs []string `json:"knowledge_base_ids"` // 绑定的 knowledge_bases.id（字符串化）

// AgentListItem 增（D4 通过时）：
KBCount int64 `json:"kb_count"`
```

- `validateAgent` 增 KB 重复检查（kb_ids 重复无 uq 提前 400 价值——表 PK (agent_id, kb_id) 会挡，但提前报友好，对齐 tool_ids 重复检查）。
- 哨兵（errors.go 增）：`ErrKnowledgeBaseNotFound = errors.New("KNOWLEDGE_BASE_NOT_FOUND")`——FK 23503 翻译，409？**否，404**：绑定请求里的 kb_id 不存在，语义与 rag 的 `KNOWLEDGE_BASE_NOT_FOUND`(404) 一致。与 `ErrToolNotFound`（agent 侧翻译 mcp 表 FK）完全同款先例：agent 不能依赖 rag，自有同名哨兵、同码同状态码（两包各一份 `KNOWLEDGE_BASE_NOT_FOUND`，前端语义无歧义）。

### 3.2 model + store（internal/agent/service、internal/agent/store）

- model（`service/model.go` 增，表归 agent 模块）：`AgentKnowledgeBase{AgentID, KnowledgeBaseID uint64; CreatedAt time.Time}`，embed 不了标准 mixin（复合 PK），自声明 `CreatedAt`；`TableName() = "agent_knowledge_bases"`。
- Store 接口增四方法（store.go 实现，全部纯 SQL 原样上抛）：
  - `CreateAgentKBs(ctx, agentID uint64, kbIDs []uint64) error`（批量插，空切片直返）
  - `DeleteAgentKBs(ctx, agentID uint64) error`（Update 先删后插用；DELETE 带 WHERE agent_id）
  - `ListAgentKBIDs(ctx, agentID uint64) ([]uint64, error)`
  - `CountKBsByAgentIDs(ctx, agentIDs []uint64) (map[uint64]int64, error)`（D4 列表聚合，对齐 tool_count 的批量现读防 N+1）

### 3.3 service（internal/agent/service/service.go）

- `Create` / `Update`：事务内与 agent_tools 同步先删后插（Update）/ 直接插（Create）；kb_id 不存在 → FK 23503 翻译 `ErrKnowledgeBaseNotFound`（23503 与 23505 的既有翻译路径同款）。
- `Get`：事务外补读 `ListAgentKBIDs` → 字符串化进 `AgentDetailSchema.KnowledgeBaseIDs`（保证非 nil，空绑定返 `[]`）。
- `List`：D4 通过时批量聚合 `kb_count`。

### 3.4 handler（internal/agent/handler）

无新路由（body 内嵌模式）。`create` / `update` 的错误映射各补一行 `errors.Is(err, agentapi.ErrKnowledgeBaseNotFound)` → 404。

### 3.5 前端

- `web/src/api/agent.ts`：`AgentDetail` 增 `knowledge_base_ids: string[]`；create/update payload 增 `knowledge_base_ids?: number[]`（对齐 tool_ids 的"响应字符串 / 请求数值"双约定）。
- `web/src/views/agent/AgentList.vue` 编辑弹窗：增 KB 多选 `el-select multiple`（数据源 `getKnowledgeBaseList` 全量拉取 + `enabled` 过滤展示，选项 label=名称、value=数值 id；提交转 Number 数组）；详情/编辑回填 `knowledge_base_ids`；列表列 `kb_count`（D4）。
- 前端不校验 KB 存在性 / 嵌入模型一致性（后端 FK + 运行期降级兜底）。

## 4. 主任务 B：chat 检索注入

### 4.1 依赖注入（internal/chat/service/service.go + server.go）

- chatService 增第六依赖，小接口收窄（对齐既有 agentGetter 等惯例）：

```go
// ragRetriever chat 用到的 rag 能力（检索注入，ragapi.KnowledgeBaseService 的单方法收窄）。
ragRetriever interface {
    Retrieve(ctx context.Context, req ragapi.RetrieveReq) ([]ragapi.RetrievedChunk, error)
}
```

- `New(store, agents, providers, clients, execs, rag ragRetriever)` 签名加一参；`server.go:155` 一行改传 `ragSvc`。
- import `ragapi "github.com/Karlsk/go-hify/internal/rag/api"`——chat → rag 依赖清单内合法。

### 4.2 检索编排（turn.go）

`assembleMessages` 改签名（agent 参数 → 已增强的 systemPrompt 字符串），编排插在落 user 消息之后、调 LLM 之前：

```go
// runTurn 内（turn.go :90 附近）：
sysPrompt := s.buildSystemPrompt(ctx, setup.agent, req.Content) // 新私有方法（IO：检索）
msgs := assembleMessages(sysPrompt, history, req.Content)       // 纯函数，签名改
```

`buildSystemPrompt`（<50 行，IO 编排）：

1. 解析 `setup.agent.KnowledgeBaseIDs`（[]string → []uint64；空 → 直接返回 `setup.agent.SystemPrompt`，**不调 Retrieve**——需求"没有就跳过"）；
2. `s.rag.Retrieve(ctx, ragapi.RetrieveReq{Query: req.Content, TopK: ragInjectionTopK, KBIDs: kbIDs})`；
3. `err != nil` → **D2 降级**：`slog.WarnContext`（记 agent_id、err）+ 返回原 SystemPrompt（任何错误都降级——EmbeddingModelMismatch / ErrProviderBusy / RateLimited / DB 错一律如此，RAG 不是对话硬依赖）；
4. 过滤 `Similarity >= ragMinSimilarity`；全滤掉 → 返回原 SystemPrompt（不加尾缀——资料段单独存在无意义）；
5. `augmentSystemPrompt(base, chunks)` 纯函数拼接（§4.4）。

常量（turn.go，包内）：

```go
ragInjectionTopK = 3   // chat 注入路径钉值（需求给定）；rag cfg.TopK 默认 5 仅服务 HTTP 检索测试
ragMinSimilarity = 0.75 // 需求给定；RetrievedChunk.Similarity = 1 - 余弦距离
```

ctx 用请求 ctx（同步阻塞、受流式 TTFT 预算约束；embedding 内部有 5s 总预算，见 rag spec 02）。**不用** `WithoutCancel`（那是入库管线异步场景）。每条 user 消息都检索（多轮对话每轮独立检索；一期不做 query 向量缓存——CLAUDE.md 性能 §② 留的"RAG 召回与语义缓存共享向量"接口不在本期）。

### 4.3 prompt 拼接（纯函数，表驱动单测）

```go
// augmentSystemPrompt agent 原 prompt + 过滤后的命中片段 → 最终 system prompt。
// base 为空且无命中不该被调到（调用方已短路）；base 为空且有命中时输出以资料段开头（D3）。
func augmentSystemPrompt(base string, chunks []ragapi.RetrievedChunk) string
```

### 4.4 模板（需求给定原文，逐字对齐）

```
{Agent 原始 Prompt}

请基于以下参考资料回答用户问题。
如果资料中没有相关信息，直接说"我没有找到相关资料"，不要编造。

【参考资料】
[1] {chunk1内容}
[2] {chunk2内容}
```

- base 与资料段之间空一行；`[n]` 编号自 1 起，`{chunk内容}` = `RetrievedChunk.Content` 原文（不截断——chunk 大小已被 KB 切分策略钉住，默认 ~800 rune；token 预算一期不做，rag 总览已声明留后）。
- 不带 DocumentName 前缀（模板原文只给内容；文档名在 executions.input 的检索快照里排障可见，见 §4.6）。
- D3（base 为空）：输出 = 资料段起头（`请基于以下参考资料回答用户问题。` 起），无前导空行。

### 4.5 assembleMessages 签名改动

```go
// 现：assembleMessages(a *agentapi.AgentDetailSchema, history []Message, content string)
// 改：assembleMessages(systemPrompt string, history []Message, content string)
```

函数体只改一处：`if a.SystemPrompt != ""` → `if systemPrompt != ""`。既有调用方测试同步改（纯函数，行为等价）。

### 4.6 不动清单与合理副作用

不动：`Stream` / consume 循环 / `emit` / `persistAssistant` / `TouchConversation` / `recordExecution` 逻辑 / handler 层 / `SendMessage` 与 `Stream` 两模式入口签名。

合理副作用（不额外处理）：
- **executions.input 自然含增强后 system prompt**（`summarizeMessages` 快照 msgs）——排障可见真实输入，符合"executions 是排障唯一线索"定位；预期 input token 计费上升（≤3 chunk × ~800 rune）。
- TTFT 顺延一次 embedding（~200ms–1s+）——一期接受（见 §8 风险）。

## 5. 实施顺序（TDD，每步全绿再进）

| 步 | 内容 | 文件 |
|---|---|---|
| A1 | agent api：schema 字段 + 常量 + Validate + 哨兵（先测后码） | agent/api/schema.go、errors.go、schema_test.go |
| A2 | agent model + store 四方法（sqlmock） | agent/service/model.go、agent/store/store.go、store_test.go |
| A3 | agent service：Create/Update 事务绑写 + 23503 翻译 + Get/List 聚合（stub Store） | agent/service/service.go、service_test.go |
| A4 | agent handler 两行映射 + 前端 agent.ts / AgentList.vue（type-check） | agent/handler、web/src/api/agent.ts、web/src/views/agent/AgentList.vue |
| B1 | chat：ragRetriever 接口 + New 签名 + server.go 一行 | chat/service/service.go、app/server.go |
| B2 | chat：buildSystemPrompt + augmentSystemPrompt + assembleMessages 签名（stub rag，表驱动） | chat/service/turn.go、turn_test.go、doubles_test.go、service_test.go |

组合根 `server.go` 仅一行改动（:155 传 ragSvc），不算"改 Controller 层"（装配不属于 handler）。

## 6. 测试计划

- A：schema Validate 表驱动（重复 kb / 超 10 / 0 值）；store sqlmock（批量插 / 删带 WHERE / 聚合）；service stub（事务内先删后插顺序、23503 → 哨兵、Get 回读非 nil、kb_count）；handler httptest（404 映射）。
- B：`augmentSystemPrompt` 表驱动（base 空/非空 × 1..3 chunk，逐字断言模板）；`buildSystemPrompt`（stub ragRetriever）五分支：无绑定不调 Retrieve / Retrieve err 降级 WARN / 全滤返回原样 / 部分滤 / 命中拼接；`assembleMessages` 新签名既有用例平移。
- 覆盖率 ≥80%（仓规）；零真实网络 / LLM / PG / Redis。

## 7. 验收门

1. `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿。
2. `cd web && npm run type-check` 零错误。
3. 手动冒烟（dev）：建 KB 传文档至 ready → Agent 编辑弹窗绑该 KB → 对话问文档内事实 → 回答引用资料且正确；问无关问题 → 回答"我没有找到相关资料"或正常拒答；解绑 KB → 恢复原 prompt 行为；人为停用 KB → 对话降级照常（日志 WARN）。

## 8. 风险与边界

- **TTFT 顺延**：每条消息一次 embedding（200ms–1s+）。接受（需求本体即同步检索）；后续可挂"共享 query 向量"接口（CLAUDE.md 性能 §② 预留）。
- **混嵌入模型绑定**：agent 可绑多个不同嵌入模型的 KB（绑定期无法校验——agent ↛ rag，依赖禁止；rag 侧建 KB 只保证单库内 1536）。运行期 Retrieve 报 `ErrEmbeddingModelMismatch` → chat 降级 WARN。属可接受的配置错误面（管理员改绑定时从 WARN 日志可发现）。
- **embedding 费用**：每条消息一次（3.5 节注入语义缓存后可复用向量，不在本期）。
- **executions.input 变大**：增强 prompt 入快照，TOAST 兜底，查询侧已禁 SELECT *。
- agent_kb 反查索引已有（`idx_agent_kbs_kb`）；绑定表极小，无性能议题。

## 9. CLAUDE.md / changelog 同步（实施完成后）

- CLAUDE.md 错误码表 `KNOWLEDGE_BASE_NOT_FOUND` 行补注"agent 侧绑定写入同码哨兵（agentapi.ErrKnowledgeBaseNotFound，FK 23503 翻译）"。
- agent/rag changelog 各补一段（绑定 CRUD 上线、chat 注入上线）。
