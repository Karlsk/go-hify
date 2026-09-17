---
name: spec-dev
description: >-
  按定稿 changelog spec 驱动开发一个 feature：输入 spec 文档路径（docs/changelog/<module>/ 下某篇），
  自动完成前置核查 → speckit-specify → clarify → plan → tasks（停等确认）→ analyze → implement →
  spec 级验收报告，全程施加硬/软两类门禁，保证产出严格贴合 CLAUDE.md 与 spec 冻结契约。
  当用户说「按 spec 开发 / 实施 spec NN / 用 spec-kit 做某篇 spec」时使用。
argument-hint: "spec 文档路径，如：docs/changelog/workflow/impl_spec_05_agent_binding.md"
user-invocable: true
---

# Hify 按 spec 驱动开发 Skill

## User Input

```text
$ARGUMENTS
```

参数是 spec 文档路径（`docs/changelog/<module>/...`）。流程原理见 `docs/spec-kit-execution-guide.md`——本 skill 是它的可执行版本，条款以本文为准执行，不需要复读该文档给用户。

## 总纪律（贯穿全程）

> **能机器判的绝不留给人，机器判不了的绝不自行发挥。**

- **硬门禁**：`go build ./... && go vet ./... && go test ./... -race -count=1`、覆盖率 ≥80%（spec 另有门槛从其规定）、`make migrate-status`、依赖方向 grep——不过不放行，不许绕。
- **软门禁**：遇到下列任一情况，**立即停下、向用户报告、等确认后继续**，不得自行决定：
  1. 需要创建 spec「本篇交付」清单之外的任何对外概念（导出类型、REST 路由、迁移列、哨兵错误、Redis key、配置项）；
  2. 需要修改任何冻结契约（接口签名、SQL、常量、哨兵文案、行为语义）——spec 修订需用户显式批准，不得通过改 spec 让实现通过；
  3. spec 与 CLAUDE.md 冲突、spec 内部自相矛盾、或 spec 与代码现状冲突；
  4. 需要修改前序篇已交付的公共接口；
  5. spec 要求的第三方依赖在 go.mod 核实不到，或下一迁移号与 spec 假设不一致；
  6. 需要新增 plan 未列明的第三方依赖。
- **反作弊**：不得删断言、`t.Skip`、调低覆盖率门槛让测试变绿。实现错修实现；认为 spec/测试错，停下报告。未全绿不得宣称完成。
- 全程**不自动 commit / push**——commit 时机由用户明示，攒到 spec「可提交节点」按模块/层次拆分提交（建议 message 即 spec §可提交节点写的那条）。
- 参数只收**一篇** spec；连续多篇时逐篇过各自的验收门再进下一篇。

---

## 第 0 步：输入解析与 spec 分流

按文件名与状态头判型；判不了的直接问用户，不猜测。

| spec 类型 | 判据 | 处理 |
|---|---|---|
| 规范分篇 | `backend_spec_NN_*` / `impl_spec_NN_*`，带状态头 + 冻结契约 + 验收门 | 走完整流程（第 1~7 步） |
| 咨询定稿篇 | `db_model.md` / `api_contract.md` / 模块总览 spec | 拒绝并说明：这篇不产码，是分篇的上位契约；引导改传某篇分篇 |
| 前端 spec | `*_frontend_*.md` / `frontend_*_spec.md` | 适配模式：specify / plan / tasks 照走；门禁换 `cd web && npm run type-check && npm run build`；manual-test 按 `docs/testing/` 对应文档；迁移与 go 覆盖率项跳过 |
| 已实施 | 状态头标「已实施 / 已合入」 | 停下确认是否重复实施（用户要的可能是增量改动 → 直接实现或先出新 spec） |
| 暂缓中 | 状态头含「暂缓」 | 停下等用户明确开工指令 |
| 无效输入 | 路径不存在 / 未传入 | 询问用户（可用 `ls docs/changelog/<module>/` 辅助列出候选），不猜测 |

## 第 1 步：开工纪律——必读、基线与依赖检查

1. **读三样**（用 Read 完整读，不凭记忆）：
   - 本篇 spec 全文——重点 §2 交付物表、§4 冻结契约、§5 行为语义、§6 任务清单、§8 测试清单、§9 验收门、§可提交节点；
   - spec 头「上位契约」列出的文档对应节（api_contract / db_model 相关节、CLAUDE.md《数据库规范》《接口规范》错误码表等）；
   - 前序篇（spec 头「前置依赖」）的交付物清单。
2. **依赖存在性检查**：前置篇的核心交付物在 `internal/` 里 grep / Glob 确认存在；`make migrate-status` 的 applied 条数与 spec 假设的下一迁移号对齐（spec 要新增 000NN → 当前应恰好 NN-1 条 applied）。任何缺失 → 停下报告「先做 spec XX」，不得跳篇自造。
3. **基线**：`go build ./... && go vet ./... && go test ./... -race -count=1` 全绿；不绿停下报告，不在脏基线上开工。
4. **模式对齐**（用 Explore agent，不跳过）：目标模块既有四层写法、哨兵翻译（23505 唯一冲突 / 23503 FK 违反）、Cache-Aside 写时删 key、`respond` 信封用法。
5. **分支**：before_specify hook（speckit-git-feature）会建 feature 分支；hook 未生效则手动 `git checkout -b {NNN}-<module>-<spec-slug>`，不在 main 上直接开发。

## 第 2 步：组装并执行 /speckit-specify

用 Skill 工具调用 `speckit-specify`，参数按此骨架从**spec §1 背景与范围、§5 行为语义**提炼（只写 WHAT/WHY，不带签名与技术选型）：

```text
<module> spec NN 需求：<spec 标题>——<一句话定位>
背景与价值。<spec §1 背景，含架构约束>
用户场景。<2~3 个，把 §5 行为语义提炼成用户可感知的场景>
功能需求。FR1~FRn <从 §5 逐条提炼，每条可测试>
明确不做（边界）。<spec「明确不做」节逐项照搬>
验收标准。以 <spec 路径> §8/§9 为准：go test 全绿即通过，关键回归点：<spec §8 列出的守点>；
  人工项见 docs/testing/<module>-manual-test.md。
依赖与假设。<前置篇合入 commit；下一迁移号；本篇的下游消费者>
```

## 第 3 步：/speckit-clarify（必跑）

调用 `speckit-clarify`。有问题答问题——**答案只从 spec 与 CLAUDE.md 里找**，找不到 → 软门禁停下问用户；拍板项已落定的不重复问，真正值得问的是 spec 与代码现状的偏差（如既有单测 fixture 是否缺 `ConstraintName` 字段）；无问题继续。

## 第 4 步：组装并执行 /speckit-plan

参数 = 固定技术栈句 + 模块落位句 + 冻结契约句 + 测试策略句：

**固定技术栈句**（照抄，NNNN 换实际下一迁移号）：`Go 1.26 + Gin + GORM（pgx 驱动，唯一冲突 23505 / FK 23503 经 *pgconn.PgError 判定与分发）。迁移走 goose SQL 只增不改（动手前先 make migrate-status 确认下一号为 NNNN）。禁 AutoMigrate。`

**模块落位句**：从 spec §2 交付物表逐行生成「交付物 → `internal/<module>/<layer>/<file>`」映射；跨模块篇标明两侧改动与依赖红线（谁不得 import 谁）；含组合根装配（`internal/app/server.go`）或前端（`web/`）的交付物一并列出。

**冻结契约句**：`冻结契约逐字对齐 <spec 路径> §4（类型名、方法签名、SQL 形态、哨兵文案 = error.code，前端直接消费），引用条目用 spec 编号不自造同义词。`

**测试策略句**：`测试策略按 spec §8 执行：<测试文件/用例清单>（覆盖 <关键回归点>）；同包 *_test.go，sqlmock / httptest / stub 隔离，零真实 PG / Redis / 网络 / LLM。实现完成的定义是 go build ./... && go vet ./... && go test ./... -race -count=1 全绿，且相关模块覆盖率各 ≥80%。`

## 第 5 步：/speckit-tasks + 固定软停点

1. 调用 `speckit-tasks`。
2. **自动比对**：任务清单 ↔ spec §2 交付物表逐项核对，顺序对照 §6 任务清单（TDD 粒度）；确认测试任务先于或伴随对应实现任务；spec §4 标「核心行为变更 / 红线」的坑位是否体现为任务或任务内约束。
3. 输出比对结果（齐 / 缺什么 / 多什么），**停下等用户确认**后才进入下一步。这是流程中唯一的固定停点，不许跳过。

## 第 6 步：/speckit-analyze（建议跑）+ /speckit-implement

analyze 交叉核对 spec / plan / tasks 与 constitution 有无冲突（依赖方向、无软删、RESTRICT 默认、SQL 红线）。

implement 期间逐任务执行，附加门禁：

- **写前**：spec 要求的第三方库在 go.mod 核实方法存在；迁移文件号与 goose 序列一致；核实不到 → 软门禁。
- **写中**：
  - 只创建交付物点名的对外概念；冻结契约逐字保真（哨兵文案 = `error.code`）；
  - 仓规红线逐条对照（CLAUDE.md 强制）：跨模块只 import 下游 `api` 包（别名 `<module><layer>`，如 `providerapi`）；service 全程无 gin 类型；禁 AutoMigrate、迁移只增不改；禁 `SELECT *`、DML 必带 WHERE、参数类型对齐列类型；事务最小化、事务内禁外部调用（HTTP / LLM / 阻塞式 Redis）、异步 goroutine 脱钩请求 ctx 用 `context.WithoutCancel`；错误链路三层不吞错（store 原样上抛 → service 翻译哨兵 → handler `errors.Is` 映射 `respond.Fail*`）；明文凭据只在调用瞬间存在，禁入日志 / 缓存 / 响应；
  - 严格 TDD：RED（依据 §4 契约先写测试并运行确认失败——新符号未定义导致的编译失败是 Go 的正常 RED；因无关语法红先修测试本身）→ GREEN（最小实现，不做超出本任务的设计）→ REFACTOR（测试保护下对齐邻近代码风格）。
- **写后（任务级 DoD）**：`go build ./... && go vet ./... && go test ./... -race -count=1` 过门禁再勾任务；红了当场修，不攒到最后。
- spec §8 写明断言 / 用例的关键回归测试**必须原样落地**（断言逻辑逐条保真）。
- 范围红线：只做本篇清单内的内容；「顺手改进」冲动记下来问用户，不直接做。

## 第 7 步：spec 级收尾——证据 DoD 与验收报告

全部满足才可宣布本篇完成，逐项把证据写进验收报告：

1. 全量门禁全绿：`go build ./... && go vet ./... && go test ./... -race -count=1` + 相关模块 `go test ./internal/<module>/... -race -cover` ≥80%（贴关键输出）；
2. spec §8 测试清单逐项对号：每个测试点都有落地测试，关键回归点逐个确认；
3. §2 交付物逐项 ls / grep 存在性核对；
4. **前置篇测试回归绿**（跨篇契约证据）+ 依赖方向 grep（本篇红线涉及的模块对，如 `internal/agent/` 无 `internal/workflow` import）；
5. 含迁移时：`make migrate-status` 条数全部 applied；既有迁移文件未动（只增不改）；
6. spec §7 文档同步逐条完成（CLAUDE.md 错误码表 / 索引地图、`docs/design/data-model.md`、系列 `db_model.md` 决策表）；
7. 验收报告收尾：剩余**人工项清单**（`make start` 冒烟走 `docs/testing/<module>-manual-test.md` 对应小节、真实供应商 / 真 key、grep 无明文凭据等），明确告知用户「验收门已判卷，这几项等你人工过」；
8. **变更总结**（给 reviewer 的导读，直接输出在对话里，不另开文件）——以 `git status --short` / `git diff --stat` 实测为准，三段固定结构：
   - **改动点**：按模块分组列新增 / 修改 / 删除的文件，每处一句话动机；前序篇文件被本篇触碰的（哪怕只改 import）单独标出；
   - **重点 review 清单**：按风险排序 3~6 条——架构决策（依赖方向、契约变化）优先，其次冻结契约保真点（逐行对照 §4 的位置）、跨篇契约兼容性、门禁妥协点；每条给文件行级定位；
   - **如何验证**：可直接复制执行的命令块（全量门禁、只跑本模块测试、关键回归单测、依赖方向 grep 等），每条命令注明预期结果；最后重复剩余人工项。

报告完停止——commit / push 由用户决定；附上 spec「可提交节点」的建议 commit message。
