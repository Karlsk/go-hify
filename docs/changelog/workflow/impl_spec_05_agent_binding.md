# Workflow 实现 spec 05：agent → workflow 绑定

> 状态：**待实施**（2026-09-17 定稿；§3 五项拍板全部落定——A=RESTRICT / B=不校验发布态 / B2=实时版本 / C=Create/PUT 字段化 / C2=不互斥。供 rdp-implementation 以 TDD 消费；用户指示**暂缓实施**，待明确开工指令）。
> 上位契约：[CLAUDE.md](../../../../CLAUDE.md)《代码组织规范》依赖清单、《数据库规范》外键与索引、《接口规范》错误码表；[api_contract.md](./api_contract.md)（不新增路由）；[db_model.md](./db_model.md) §决策表（本篇新增 #11）。冲突时停下来问用户。
> 前置依赖：spec 01-04（workflow CRUD/状态机已合入，commit `25231d6`）；agent 模块既有实现（00004 / 00009 / 00014 已 applied）。
> 跨模块说明：本篇以 workflow 系列编号，但改动主体在 **agent 模块**（列 + 契约 + 翻译）与 **workflow 模块**（删除互锁）各半。

## 1. 背景与范围

Agent 可绑定一个已定义的工作流（对话中可触发执行——Dify 式 Agent 编排的最小形态）。
架构约束：依赖清单允许 `workflow → agent`、**不允许 `agent → workflow`**——agent 不能
import workflowapi。因此绑定期 workflow 的存在性校验走 **FK 23503 翻译**，与 agent 绑 KB
（`agent 不依赖 rag，FK 是 KB 存在性的唯一校验`）完全同款；执行与触发是消费方
（chat / 执行器）的事，经 workflowapi 消费时再校验发布态（`ErrWorkflowNotPublished`）。

**本篇交付**：`agents.workflow_id` 可空外键 + agent 侧写入契约与翻译 + workflow 侧删除互锁。

**明确不做**（用户拍板「具体的执行引擎后续再实现」）：

- `POST /workflows/{id}/execute` 路由与执行器（后续 spec）
- chat 会话期消费绑定（建会话 / 发消息时触发工作流）——后续 spec
- agent 列表按 workflow 聚合展示名（无此需求；只回 id，防跨模块 N+1）

### 1.1 递延到执行引擎 spec 的决策（2026-09-17 讨论记录，届时拍板）

绑定契约对以下形态均够用，本篇不预设；执行引擎 spec 须逐项拍板后再设计表结构。

| # | 事项 | 候选形态与备注 |
|---|---|---|
| E1 | chat 消费绑定的触发形态 | **工具形态**：workflow 包装为虚拟工具（类似 subagent 被 Task 工具包裹），模型自主调用，复用 tool_call / tool_result SSE 事件；**管道形态**：消息进对话循环前确定性先过 workflow（分类 / 分流 / 检索增强），输出注入上下文。两者不互斥。前置事实：chat v1 工具循环未执行（ToolIDs 读到不执行），两种形态都依赖执行引擎先行 |
| E2 | 试运行（单测）能力 | 当前 execute 契约对 draft/disabled 一律 503 拒绝；试运行需放开 draft + 标记 test run（形态待定：query 参数或独立端点） |
| E3 | 运行历史 + 节点轨迹 | `workflow_runs`（每次运行一行：input/output/status/耗时/触发来源）+ 节点级 steps（node key、input/output、耗时、错误）——CLAUDE.md 护栏「运行日志是排障唯一线索」的 P0。schema 待 E1 与执行模型（同步/异步）定型后设计，现在建表会返工 |

## 2. 交付物

| # | 交付物 | 路径 |
|---|---|---|
| 1 | 迁移 00018（加列 + FK + 索引 + COMMENT） | `migrations/00018_agent_workflow_binding.sql` |
| 2 | agent api：Req/Schema 字段 + 哨兵 | `internal/agent/api/schema.go`、`errors.go` |
| 3 | agent service：model 字段 + 转换 + 23503 约束名分发 | `internal/agent/service/model.go`、`service.go` |
| 4 | agent store：`selectAgent` 列清单 | `internal/agent/store/store.go` |
| 5 | agent handler：404 映射 | `internal/agent/handler/handler.go` |
| 6 | workflow 侧：哨兵 + Delete 23503 翻译 + 409 映射 | `internal/workflow/api/errors.go`、`service/service.go`、`handler/handler.go` |
| 7 | 文档同步（CLAUDE.md 错误码表 + 索引地图、data-model.md、db_model.md） | 见 §7 T7 |

## 3. 拍板项（三项，定稿前须用户确认）

| # | 决策点 | 结论 | 备选与影响 |
|---|---|---|---|
| A | FK 的 `ON DELETE` 行为 | **✅ 已拍板（2026-09-17）：RESTRICT**——被绑定的 workflow 禁删（409 `WORKFLOW_IN_USE`），与 `agents.model_id`（MODEL_IN_USE）、`conversations.agent_id`（AGENT_IN_USE）同款互锁；agent 详情缓存（Cache-Aside 30min）不可能出现悬空 workflow_id。核心论据：引用关系一律 RESTRICT（仅真子表 CASCADE）；SET NULL 等于 workflow 的 DELETE 跨模块偷偷改写 agents 行（绕过 agent 模块边界与缓存失效）；静默解绑比显式 409 难排障；下架场景本就该走 disable 而非删除 | SET NULL（否决）：删 workflow 不被挡、自动解绑；代价是缓存窗口内 agent 详情指向已删 workflow，且与仓内「RESTRICT 默认、真子表才 CASCADE/置空」约定不符 |
| B | 绑定期是否校验 workflow 发布态 | **✅ 已拍板（2026-09-17）：不校验**——绑定期只管存在性（FK）；发布态是执行前置（`ErrWorkflowNotPublished` 503），由消费方在执行时 fail-fast。依赖清单也不允许 agent 调 workflowapi 查状态，此项实为「依赖规则下的必然」 | 校验发布态：需引入 agent → workflow 依赖或反向注入接口，破坏依赖图，不建议 |
| B2 | 执行读哪个版本（B 的追问） | **✅ 已拍板（2026-09-17）：实时版本**——workflows 三表只存一份图，无发布快照；已发布的 workflow 被 PUT 编辑后**状态保持 published（决策 #6「编辑不降级」，不回 draft）、仍可执行，且立即对下一次执行生效**（图写入是事务整图替换 + R1-R9 全量校验，库里永远是完整合法态）。执行器 spec 按实时加载落语义 | 发布快照（Dify 式 draft/published 双图）：workflows 加 `published_graph jsonb`，publish 冻结、执行读快照、编辑须重新发布。将来需要时纯加列，不破坏本篇绑定契约 |
| C | API 形态 | **✅ 已拍板（2026-09-17）：既有 Create/PUT 字段化**——`workflow_id` 可空字段，PUT 全量语义（缺省/null = 解绑），零新路由 | 独立子资源端点（如 `PUT /agents/{id}/workflow`）：多一组路由 + 局部更新语义，与仓内 PUT 全量约定不一致，不建议 |
| C2 | workflow_id 与现有字段是否互斥（C 的追问） | **✅ 已拍板（2026-09-17）：不互斥，叠加语义**——agent 的 model/提示词/RAG/工具照常构成对话循环，workflow 是对话中**可触发的附加能力**（CLAUDE.md 依赖清单「chat → workflow 对话中可触发工作流执行」）；validateAgent 零新增规则 | 互斥（纯工作流入口）：绑定后对话不走模型循环——需 model_id 改可空 + 对话引擎分叉，改动远超本 spec；将来确有此形态再加模式字段（如 `mode: chat\|workflow`）而非互斥校验 |

> 用户示例 curl（`PUT /agents/1 -d '{"workflowId": 1}'`）两点不合仓内契约：字段名应为
> **snake_case `workflow_id`**；PUT 是**全量提交**——只发一个字段会把 name/model_id 等置零
> 导致 400。正确用法见 §5 示例（C 项按推荐落定后）。

## 4. 冻结契约

### 4.1 迁移 00018（goose，只增不改）

```sql
-- +goose Up
-- Agent → workflow 绑定（spec 05）：agents.workflow_id 可空外键。
-- RESTRICT（拍板 A）：被绑定的 workflow 禁删（409 WORKFLOW_IN_USE），与
-- agents.model_id / conversations.agent_id 同款互锁；可空 = 不绑定是常态。
-- 约束显式命名 fk_agents_workflow：service 侧 23503 按约束名分发翻译（§4.4）。
ALTER TABLE agents
    ADD COLUMN workflow_id bigint,
    ADD CONSTRAINT fk_agents_workflow FOREIGN KEY (workflow_id)
        REFERENCES workflows (id) ON DELETE RESTRICT;

CREATE INDEX idx_agents_workflow_id ON agents (workflow_id);

COMMENT ON COLUMN agents.workflow_id IS '绑定的 workflows.id（可空=未绑定；ON DELETE RESTRICT，被绑定的 workflow 删除被挡 409 WORKFLOW_IN_USE）';

-- +goose Down
ALTER TABLE agents DROP COLUMN workflow_id;  -- 列上的索引与 FK 随列级联删除
```

### 4.2 agent api（schema.go / errors.go）

```go
// CreateAgentReq / UpdateAgentReq 在 RAGMinSimilarity 后新增（两处同款）：
// WorkflowID 绑定的工作流（可空；nil = 不绑定 / PUT 全量语义下 = 解绑）。
// workflow 不存在时 FK 23503 由 service 翻译 ErrWorkflowNotFound（agent 不得
// 依赖 workflow（依赖清单），FK 是存在性的唯一校验——与 KnowledgeBaseIDs 同款）。
WorkflowID *uint64 `json:"workflow_id" binding:"omitempty,gt=0"`

// AgentSchema 在 RAGMinSimilarity 后新增：
// WorkflowID 绑定的工作流（null=未绑定，字符串化外键）。
WorkflowID *string `json:"workflow_id"`
```

`validateAgent` 签名与规则**不变**（workflow_id 无跨字段规则，零值由 binding `gt=0` 挡）。

```go
// errors.go 新增哨兵：
// ErrWorkflowNotFound 绑定的工作流不存在（404；workflow_id 撞 FK 23503 的翻译——
// agent 不得依赖 workflow（依赖清单），FK 是存在性的唯一校验机制，与
// ErrToolNotFound / ErrKnowledgeBaseNotFound 同款）。码与 workflowapi.ErrWorkflowNotFound
// 同名同义：agent api 不能 import workflow api，各持一份哨兵，前端语义无歧义。
ErrWorkflowNotFound = errors.New("WORKFLOW_NOT_FOUND")
```

### 4.3 agent service（model.go / service.go）

```go
// model.go Agent 在 RAGMinSimilarity 后新增：
WorkflowID *uint64 // 绑定的工作流（nil=未绑定；fk_agents_workflow RESTRICT）
```

`toModelCreate` / `applyUpdate` 各加一行直赋；`toSchema` 尾部按 `FallbackModelID` 同款
指针字符串化。agent 详情缓存失效矩阵不变（Create/Update/Delete 既有 evict 已覆盖重绑）。

### 4.4 23503 约束名分发（本篇核心行为变更）

现状：agent service 的 `isFKViolation` 只判 code，`CreateAgent`/`UpdateAgent` 语句上的
23503 一律译 `ErrModelNotFound`。加列后同一条 INSERT/UPDATE 可能撞**三个** FK，必须按
`pgErr.ConstraintName` 分发（model 两个是 00004 内联 REFERENCES 的 PG 自动命名）：

```go
// fkAgentsWorkflow agents.workflow_id 的约束名（00018 显式命名）。
const fkAgentsWorkflow = "fk_agents_workflow"

// translateAgentFK 把 agents 行级写语句上的 23503 按约束名翻译哨兵；
// 非 23503 返回 false。model 侧两个约束（agents_model_id_fkey /
// agents_fallback_model_id_fkey）与兜底保持既有 ErrModelNotFound 语义。
func translateAgentFK(err error) (error, bool) {
    var pgErr *pgconn.PgError
    if !errors.As(err, &pgErr) || pgErr.Code != pgCodeFKViolation {
        return nil, false
    }
    if pgErr.ConstraintName == fkAgentsWorkflow {
        return agentapi.ErrWorkflowNotFound, true
    }
    return providerapi.ErrModelNotFound, true
}
```

Create / Update 事务内 `CreateAgent` / `UpdateAgent` 两处的 `isFKViolation(err) → ErrModelNotFound`
替换为 `if sent, ok := translateAgentFK(err); ok { return sent }`；Tools / KBs 语句的翻译**不动**
（各自语句上的 23503 无歧义）。`isFKViolation` 保留给其余语句。

### 4.5 workflow 侧（api/errors.go / service / handler）

```go
// ErrWorkflowInUse 工作流被 agent 绑定，无法删除（409；agents.workflow_id FK
// RESTRICT 的 23503 翻译）。可先在 agent 侧解绑（PUT agents workflow_id=null）。
var ErrWorkflowInUse = errors.New("WORKFLOW_IN_USE")
```

service：新增 `pgCodeFKViolation = "23503"` 常量 + `isFKViolation` helper（agent/provider 同款）；
`Delete` 在 `err != nil` 分支最前加 `if isFKViolation(err) { return workflowapi.ErrWorkflowInUse }`。
handler：`failWorkflow` switch 加 `case errors.Is(err, workflowapi.ErrWorkflowInUse): respond.Fail(c, http.StatusConflict, …)`。

### 4.6 agent handler

`failAgent` switch 加 `case errors.Is(err, agentapi.ErrWorkflowNotFound):` → **404**
（与 ErrToolNotFound / ErrKnowledgeBaseNotFound 同组）。

## 5. 行为语义

- **绑定**：`POST /agents` 带 `workflow_id`，或 `PUT /agents/{id}` 全量体携带；解绑 = PUT
  体中 `workflow_id` 缺省或 `null`。N 个 agent 可绑同一 workflow（N:1，无唯一约束）。
  **叠加语义（拍板 C2）**：不与 model_id / tool_ids / knowledge_base_ids 等任何现有字段
  互斥——对话配置照常生效，workflow 是附加触发能力。
  ```bash
  curl -X PUT localhost:8081/api/v1/agents/1 -H 'Content-Type: application/json' -d '{
    "name":"客服助手","model_id":1,"workflow_id":3, ... 其余字段全量提交 }'
  ```
- **存在性**：仅 FK。绑不存在的 workflow → 23503 → 404 `WORKFLOW_NOT_FOUND`
  （预检后被并发删除的场景同样被 FK 兜住）。
- **发布态**：绑定期不校验（拍板 B）；draft/disabled 的 workflow 可被绑定，执行期由
  消费方校验（`WORKFLOW_NOT_PUBLISHED` 503）。执行读**实时图**（拍板 B2）：已发布的
  workflow 被 PUT 编辑立即对下一次执行生效，无需重新 publish。
- **删除互锁**：workflow 被绑定时 `DELETE /workflows/{id}` → 409 `WORKFLOW_IN_USE`；
  解绑或删 agent 后可删（nodes/edges 仍随 CASCADE 清理）。workflow 的
  publish/disable/PUT 编辑**不受**绑定影响（编辑不降级，既有语义不变）。
- **缓存**：agent-cache（detail:{id}）随既有写时删 key 失效；RESTRICT 保证库内不出现
  悬空 workflow_id，缓存窗口内引用恒有效（拍板 A 的第二收益）。
- **多对多边界**：单向单值。workflow 侧不感知绑定方列表（无反查端点、无绑定计数）。

## 6. 任务清单与实施顺序（TDD 粒度）

1. **T1** 迁移 00018 → `make migrate-up && make migrate-status`（18 条 applied）；
2. **T2** agent api：字段 + 哨兵（RED：schema 序列化 round-trip、binding 零值拒绝）；
3. **T3** agent service：model 字段 + 三个转换函数 + `translateAgentFK` 分发
   （RED：Create/Update 撞 `fk_agents_workflow` → ErrWorkflowNotFound；model 约束名 → ErrModelNotFound；既有 FK 用例 fixture 补 `ConstraintName`）；
4. **T4** agent store：`selectAgent` 加列（既有 sqlmock 用例列断言同步）；
5. **T5** agent handler：404 映射（httptest 表驱动补一行）；
6. **T6** workflow 侧：哨兵 + Delete 翻译 + 409 映射（service stub 返 23503 → 断言 409）；
7. **T7** 文档同步（见 §7）；
8. **T8**（可选）`docs/testing/agent-manual-test.md` 补「workflow 绑定」小节（绑/解绑/404/409 冒烟）。

每步门禁：`go build ./... && go vet ./... && go test ./... -race -count=1`。

## 7. 文档同步清单（T7）

| 文档 | 改动 |
|---|---|
| CLAUDE.md 错误码表 | `WORKFLOW_NOT_PUBLISHED` 行后加 `| WORKFLOW_IN_USE | 409 | workflowapi.ErrWorkflowInUse（被 agents.workflow_id 引用，删除被挡） |`；`WORKFLOW_NOT_FOUND` 行补注 agentapi 同码哨兵（照 KNOWLEDGE_BASE_NOT_FOUND 行格式） |
| CLAUDE.md 索引地图 | `agents` 行 → `(model_id)`、`(fallback_model_id)`、`(workflow_id)` |
| docs/design/data-model.md | agents 表 + `workflow_id` 列；关系清单加 `agents N──1 workflows # ON DELETE RESTRICT（spec 05）` |
| db_model.md（本系列） | 决策表加 #11：agent 绑定走可空 FK **RESTRICT** + 双向哨兵；同条记录 B2（执行读实时图、无发布快照）与 C2（绑定叠加语义、不互斥）的拍板结论与日期 |

## 8. 测试清单（零真实 PG/Redis，同包 `*_test.go`）

- **agent api**：`workflow_id` 序列化（null / "3" 两态）；binding `gt=0`（传 0 → 400）；
  `Validate` 不受影响（无新跨字段规则）。
- **agent service**：Create/Update 带 WorkflowID 落 model 转换；23503 分发表驱动
  （`fk_agents_workflow` → ErrWorkflowNotFound；`agents_model_id_fkey` → ErrModelNotFound；
  无约束名/非 23503 → 原样包装）；toSchema 指针字符串化 null/值两态；解绑（PUT null）
  → model 字段回 nil。
- **agent store**：`selectAgent` 含 workflow_id 列（sqlmock 期望 SQL 断言）；Create/Save
  语句参数含新列。
- **agent handler**：ErrWorkflowNotFound → 404 信封。
- **workflow service**：store 返 23503（stub 构造 *pgconn.PgError）→ ErrWorkflowInUse；
  正常删除路径回归不受影响。
- **workflow handler**：ErrWorkflowInUse → 409 信封（`error.code = "WORKFLOW_IN_USE"`）。

## 9. 验收门

- [ ] `make migrate-status` 18 条全部 applied；迁移文件只增（既有 00001-00017 未动）
- [ ] `go build ./... && go vet ./... && go test ./... -race -count=1` 全绿
- [ ] `go test ./internal/agent/... ./internal/workflow/... -race -cover` 两模块各 ≥ **80%**
- [ ] 依赖方向 grep：`internal/agent/` 无 `internal/workflow` import（哨兵同名各持一份）
- [ ] §7 文档同步逐条完成

## 10. 可提交节点

`feat(agent,workflow): agent→workflow 绑定（可空 FK RESTRICT + 双向哨兵翻译）`
