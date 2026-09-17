# Spec-Kit 执行指导：从 changelog spec 到代码（以 workflow spec 05 为例）

`docs/changelog/<module>/` 下的 spec 系列文档有双重身份：既是模块咨询定稿的**契约文档**（冻结签名 / SQL / 哨兵 / 验收门），也是 Spec-Kit 的**开发原料**。这份指导讲怎么把一篇 spec 变成 `specs/` 下能跑的 feature——完整命令序列以 workflow spec 05（agent → workflow 绑定）为例给出，后续每一篇照同一套流程重复即可。

> **本流程已固化为 skill**：日常直接用 `/spec-dev <spec 文档路径>`（`.claude/skills/spec-dev/`），它按本文指导的门禁自动走完全程、在 tasks 后停等确认。本文档保留为原理讲解和手动兜底。
>
> **与既有 skill 的分工**：新模块从零（无 spec）先走 `module-delivery`——其 Step 1 咨询定稿产出 `db_model.md` / `api_contract.md` / 分篇实施 spec，就是本流程的原料。已有定稿 spec 后有两条路：**直通路径** `/rdp-implementation <spec 路径>`（跳过 spec-kit 工件生成直接以 TDD 消费，仓内 workflow spec 01-04 的四个 feat commit 即此路径产物），或本文档的 **spec-kit 路径**（specify → plan → tasks 留痕到 `specs/`，implement 按 rdp 红线执行）。两条路的实施标准完全相同，区别只在是否生成 `specs/` 工件。本文档保留为原理讲解和手动兜底。

---

## 一、总原则：一次只喂一篇 spec

每篇 spec 的「本篇交付」清单就是一个 feature 的边界。spec 05 验收合入后再开执行引擎的 specify——执行引擎（触发形态 / 运行历史）消费的正是 spec 05 落下的绑定契约。**篇号就是实施顺序**，这是把交付物按篇切开的用意。

两层输入不要混：

- **`/speckit-specify` 喂 WHAT/WHY**——需求陈述（背景、场景、功能需求、边界、验收），不带技术选型与冻结签名；
- **`/speckit-plan` 喂 HOW**——四层落位、冻结契约（spec §4 的签名 / SQL / 哨兵文案）、任务顺序、测试策略。

spec 文档的结构天然对应这个分工：§1 背景与范围、§5 行为语义供 specify 取材，§4 冻结契约、§6 任务清单供 plan 取材，「本篇交付」表（§2）就是 tasks 的核对清单。验收分两层：spec §8 测试清单 + §9 验收门是机器判卷（`go test` 全绿、覆盖率 ≥80%、依赖方向 grep、`make migrate-status`），implement 自动执行；`docs/testing/<module>-manual-test.md` 与 `make start` 冒烟是人工抽查项。harness 的成熟度现状：rag（backend_spec 01-08）与 workflow（impl_spec 01-05）系列都带「状态头 + 上位契约 + 冻结契约 + 验收门」的完整范式；早期 provider / agent 分篇格式较松散，喂 specify 前按第五章骨架重写需求陈述一节即可，changelog 原文不动。

全流程一条铁律（rdp-implementation 核心原则）：**spec 即契约**——发现 spec 内部冲突、spec 与 CLAUDE.md 冲突、或 spec 与代码现状冲突时，停下来问用户，绝不自行折中。

## 二、前提确认（不用跑命令）

- Constitution 已存在（`.specify/memory/constitution.md` v1.0.0，七原则），不需要重建。
- `specs/` 目录为空，specify 会从 001 开始编号，与 `docs/changelog/` 互不覆盖。
- 基准优先级（沿用 rdp-implementation）：**CLAUDE.md（唯一事实源）> changelog spec 定稿 > spec-kit 生成的 `specs/` 工件 > 全局规则**。`specs/` 是执行留痕，与 changelog spec 出入时以 changelog spec 为准并回修留痕，不改定稿。

## 三、完整命令序列（以 workflow spec 05 为例）

### 第 0 步：前置核查（开工前三分钟）

```bash
make migrate-status                                            # 应为 17 条 applied，下一号 00018 才对得上 spec 假设
go build ./... && go vet ./... && go test ./... -race -count=1 # 基线全绿才开工
```

基线不绿或迁移号对不上，先报告用户，不在脏基线上开工（rdp-implementation 第 1 步同款要求）。

### 第 1 步：/speckit-specify（生成规格）

把下面第四章的需求陈述整段作为参数：

```text
/speckit-specify <粘贴第四章"workflow spec 05 需求"全文>
```

### 第 2 步：/speckit-clarify（必选，建议跑）

```text
/speckit-clarify
```

它会挑出规格里的歧义点问你（最多 3 个）。spec 05 的五项拍板（A/B/B2/C/C2）已在定稿时落定，一般没什么可问；真正值得问的是 spec 与代码现状的偏差（如 agent service 既有 FK 单测的 sqlmock fixture 是否缺 `ConstraintName` 字段——spec §4.4 的分发逻辑依赖它）。

### 第 3 步：/speckit-plan（技术方案）

```text
/speckit-plan 技术栈：Go 1.26 + Gin + GORM（pgx 驱动，FK 23503 经 *pgconn.PgError 按 ConstraintName 分发）；迁移走 goose SQL 只增不改（动手前先 make migrate-status 确认下一号为 00018——spec 假设以此为准）。模块落位：本篇横跨两模块各半——agent 侧（api 的 schema 字段 + ErrWorkflowNotFound 哨兵、service 的 model 字段 + translateAgentFK 约束名分发、store 的 selectAgent 加列、handler 的 404 映射）与 workflow 侧（api 哨兵 ErrWorkflowInUse、service Delete 的 23503 翻译、handler 的 409 映射）；冻结契约逐字对齐 docs/changelog/workflow/impl_spec_05_agent_binding.md §4（类型名、方法签名、SQL 形态、哨兵文案 = error.code），引用条目用 spec 编号不自造同义词。依赖红线：agent 不得 import workflowapi——哨兵 WORKFLOW_NOT_FOUND 同名各持一份，FK 是存在性的唯一校验。测试策略按 spec §8 执行：agent api 序列化 round-trip（null/"3" 两态）与 binding 零值拒绝、service 23503 分发表驱动（stub 构造 *pgconn.PgError，覆盖 fk_agents_workflow → ErrWorkflowNotFound、agents_model_id_fkey → ErrModelNotFound、非 23503 原样包装三个关键回归点）、store sqlmock 列断言、handler httptest 404/409 信封；全部同包 *_test.go、零真实 PG/Redis。实现完成的定义是 go build ./... && go vet ./... && go test ./... -race -count=1 全绿，且 agent/workflow 两模块覆盖率各 ≥80%。
```

### 第 4 步：/speckit-tasks（拆任务）

```text
/speckit-tasks
```

产出任务清单后**先自己过一眼再往下走**：对照 spec §2 交付物表 7 项核对没有漏项，三个坑是否体现为任务或任务内约束——

- 坑一：23503 必须按约束名分发（加列后同一条 INSERT/UPDATE 可能撞三个 FK，只判 code 会把 workflow 误译成 MODEL_NOT_FOUND）；
- 坑二：agent 不得 import workflowapi（依赖清单红线，哨兵各持一份是刻意重复）；
- 坑三：PUT 是全量语义（只发一个 workflow_id 会把 name/model_id 等置零导致 400，验收用例须按全量体构造）。

### 第 5 步：/speckit-analyze（可选，一致性检查）

```text
/speckit-analyze
```

交叉核对 spec / plan / tasks 与 constitution 有无冲突（比如任务里是否不小心让 agent import 了 workflowapi——违反原则 II 单向依赖；是否引入了绑定期发布态校验——拍板 B 的否决项）。

### 第 6 步：/speckit-implement（执行）

```text
/speckit-implement
```

implement 的完成标准就是 spec §9 验收门全绿——迁移 18 条 applied、`go test` 全绿、双模块覆盖率 ≥80%、依赖方向 grep 干净、§7 文档同步逐条完成。执行红线与 rdp-implementation 一致：逐任务 TDD（RED → GREEN → REFACTOR，每任务过 `go build && go vet && go test -race` 门禁）、测试隔离（sqlmock / httptest / stub，零真实网络与真实 PG/Redis）、范围红线（只做本篇清单内的，顺手改进记下来问用户不直接做）。实现完成后联动 `/rdp-verification` 传入同一份 spec 路径做七维验证，Critical/Major 问题必须修复。机器判卷过后，人工只需过三条：`make migrate-status` 确认 18 条 applied、`make start` 冒烟走 `docs/testing/agent-manual-test.md` 的「workflow 绑定」小节（绑 / 解绑 / 404 / 409）、grep 确认 `internal/agent/` 下无 `internal/workflow` import。提交时机由用户明示——本篇的可提交节点就是 spec §10 那一条：`feat(agent,workflow): agent→workflow 绑定（可空 FK RESTRICT + 双向哨兵翻译）`。

## 四、workflow spec 05 需求（specify 的输入原文）

> 跑 Spec-Kit 时从「背景与价值」到「依赖与假设」整段粘贴；冻结契约（§4）不进这一层，留给 plan。

**workflow spec 05 需求：agent → workflow 绑定**

**背景与价值。** Agent 可绑定一个已定义的工作流（对话中可触发执行），这是 Dify 式 Agent 编排的最小形态。约束有两个：一是架构上依赖清单允许 `workflow → agent`、**不允许 `agent → workflow`**，agent 不能 import workflowapi，绑定期的工作流存在性校验只能走数据库外键（与 agent 绑 KB 同款）；二是删除语义上，被绑定的 workflow 若可静默删除，agent 详情缓存（Cache-Aside 30 分钟）会出现悬空引用，比显式报错难排障得多。本篇解决"绑定关系本身"，执行与触发是后续执行引擎 spec 的事。

**用户场景。**

1. 管理员给客服 Agent 绑一个「工单分类」工作流：`PUT /agents/1` 全量体携带 `workflow_id` 字段即完成，不碰任何代码；解绑同样是 PUT 带 `workflow_id: null`。
2. 运维删除一个已被绑定的 workflow：得到明确的 409 `WORKFLOW_IN_USE`（先解绑再删），而不是静默解绑后 agent 指向不存在的工单。
3. 开发者配错 `workflow_id`（引用不存在的 id）：得到清晰的 404 `WORKFLOW_NOT_FOUND`，不允许静默跑过或含混的 500。

**功能需求。**

- FR1：绑定是既有 Create/PUT 的可空字段（`workflow_id`，RESTRICT 外键），零新路由；缺省/null = 解绑；N 个 agent 可绑同一 workflow。
- FR2：绑定期只校验存在性（FK 23503 翻译 404），不校验发布态——draft/disabled 的 workflow 可被绑定，执行期由消费方 fail-fast（`WORKFLOW_NOT_PUBLISHED` 503）。
- FR3：删除互锁：被绑定的 workflow 禁删（23503 翻译 409 `WORKFLOW_IN_USE`）。
- FR4：叠加语义：workflow_id 不与 model_id / tool_ids / knowledge_base_ids 互斥——对话配置照常生效，workflow 是附加触发能力。
- FR5：agent 列表/详情返回字符串化 `workflow_id`（null=未绑定），不跨模块聚合 workflow 展示名（防 N+1）。
- FR6：agent 写语句上的 23503 必须按约束名分发翻译（model 两个约束 → `MODEL_NOT_FOUND`，workflow 约束 → `WORKFLOW_NOT_FOUND`），不串台。

**明确不做（边界）。** `POST /workflows/{id}/execute` 路由与执行器、chat 会话期消费绑定（触发形态 / 试运行 / 运行历史三项决策递延到执行引擎 spec）、agent 列表按 workflow 聚合展示名——全部属于后续特性。

**验收标准。** 以 [impl_spec_05_agent_binding.md](changelog/workflow/impl_spec_05_agent_binding.md) 为准：可自动化的部分由 spec §8 测试清单承载（`go test` 全绿即通过），四条硬门槛——迁移 18 条全部 applied、agent/workflow 双模块覆盖率各 ≥80%、`internal/agent/` 依赖方向 grep 干净、23503 分发不串台——都有对应的回归测试；harness 覆盖不到的人工项见 spec §6 T8 与 `docs/testing/agent-manual-test.md`（绑 / 解绑 / 404 / 409 冒烟）。

**依赖与假设。** workflow spec 01-04 已合入（commit `25231d6`）；agent 模块既有迁移（00004 / 00009 / 00014）已 applied，下一迁移号为 00018；本篇的直接下游消费者是执行引擎 spec（chat 触发形态 E1 拍板后立项）。

## 五、后续各篇照此复制

给任意一篇写 specify 输入时，套第四章的骨架即可：**背景与价值 → 用户场景 → 功能需求（从 spec §1 / §5 行为语义提炼）→ 明确不做（spec「明确不做」节）→ 验收标准（自动化部分指向 spec §8/§9，人工部分指向 manual-test 文档）→ 依赖与假设（指向前序篇的合入 commit）**。plan 参数从 spec §4 抄签名、SQL、哨兵文案与模块落位，并带上一句测试策略（照 spec 05 plan 参数末尾那句的格式：测试清单 + 关键回归点 + 「实现完成的定义是 go build / go vet / go test -race 全绿 + 覆盖率 ≥80%」）。

按 CLAUDE.md 依赖图的构建顺序：platform（基建）→ provider → mcp → agent → rag → workflow → chat（chat 最后、零被依赖，始终保持可拆出状态）。已交付：provider、agent、rag、chat v1、workflow CRUD（spec 01-04）。当前队列：workflow spec 05（定稿，**暂缓实施中**，待明确开工指令）→ workflow 执行引擎（E1-E3 递延决策拍板后立项）→ chat v1 工具循环执行。

不产码的篇不进本流程：`db_model.md` / `api_contract.md` 这类咨询定稿篇是 module-delivery Step 1 的产出、后续分篇的上位契约，直接当 plan 的「上位契约」引用；跨模块串联验收（如 chat 全链路对账）不单开 feature，固化成 manual-test 文档里的冒烟小节即可。
