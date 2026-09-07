---
name: rdp-implementation
description: 按定稿 spec严格实现——以 spec 为契约、四层结构（api/service/store/handler）为准、TDD（RED→GREEN→REFACTOR）逐任务推进；先探索 codebase、多轮澄清消歧后再编码，完成后联动 rdp-verification 验证。用户要求"按 spec 实施 / 开始实施 spec NN / 实现某研发计划"时使用；
argument-hint: [spec 文档路径，如 docs/changelog/rag/backend_spec_01_model_migration.md]
---

# RDP 任务实现（spec 即契约 + TDD）

## 适用场景

- 用户要求按某篇定稿 spec 完成实现（如「开始实施 spec 01」「按 rag CRUD spec 实现」）
- 按契约文档推进某个开发阶段 / 波次（spec 系列中的某一篇或连续几篇）
- **不适用**：从零开始的新模块（无 spec，需要咨询、数据模型设计）→ 走 `module-delivery`；已有模块内无 spec 的小改动 → 直接实现

## 核心原则

1. **spec 即契约**：spec 里的接口签名、SQL、行为语义、验收门是最高实施基准，不得擅自偏离；发现 spec 内部冲突、spec 与 CLAUDE.md 冲突、或 spec 与代码现状冲突时，**停下来问用户，绝不自行折中**
2. **四层结构即分层**：按 CLAUDE.md《代码组织规范》的 api（契约）/ service（业务）/ store（数据）/ handler（HTTP）组织，依赖单向（handler → api ← service；store 实现 service.Store 接口）；各层职责、模板、禁止清单以 CLAUDE.md 为准，本流程不重复
3. **严格 TDD**：每个任务先写失败测试并运行确认 RED，再写最小实现到 GREEN，最后 REFACTOR；禁止先写实现再补测试
4. **交互式澄清**：有歧义就问（AskUserQuestion），绝不用假设替代确认；澄清分阶段多轮进行

**基准优先级**：CLAUDE.md（项目规范，唯一事实源）> 本次 spec 文档 > `~/.claude/rules/golang` 全局规则 > 本 skill 默认标准。

## 总体流程

复制以下清单逐项跟踪（TodoWrite）：

```
实现进度:
- [ ] 0. 输入解析：定位并通读 spec 及其上位文档
- [ ] 1. 探索 codebase
- [ ] 2. 第一轮澄清：范围与歧义 ⏸
- [ ] 3. 制定实现计划（任务拆分 + 排序）
- [ ] 4. TDD 实现循环（逐任务 RED→GREEN→REFACTOR）
- [ ] 5. 全量自检
- [ ] 6. 调用 rdp-verification 验证 ⏸
```

## 第 0 步：输入解析

1. 定位 spec 文档（路径由调用方传入；**未传入则询问用户**，不得猜测——本仓 spec 系列常为多篇，篇号即顺序）
2. 通读 spec 全文及其引用的上位文档，提取：
   - **任务清单**：本篇交付物（spec 通常自带「交付」「实现顺序」节）及其编号
   - **接口契约**：冻结的函数签名、SQL、常量、哨兵错误、行为语义
   - **目标代码范围**：涉及的包路径（`internal/<module>/...`、`internal/platform/...`、`migrations/`、`web/`）与测试文件
   - **验收门**：spec 末节的「单测与验收门」= 本篇 DoD（含覆盖率门槛，默认 ≥80%）
   - **前置依赖**：spec 头部标注的前置篇（如「前置依赖：01、04」）——先核实其已合入；未闭环时**告知用户并询问是否继续**
3. 若 spec 属于「总览 + 分篇」系列（如 rag 的 8 篇），通读总览的决策表与波次门，确认本篇在系列中的位置与提交门归属

## 第 1 步：探索 codebase

用 Explore agent 做一次综合调研（不要跳过，即使自认为了解项目）：

1. **现状盘点**：目标模块现有代码、占位文件、已实现部分——避免重复实现或覆盖已有成果（本仓习惯先建占位文件再填实）
2. **模式对齐**：对齐同类模块既有模式——`internal/provider`、`internal/agent` 的 service/store/handler 写法、哨兵翻译（23505/23503）、Cache-Aside、`respond` 信封用法；`module-delivery` skill 的「踩坑清单」逐条过一遍
3. **依赖核查**：spec 要求的第三方库是否已在 go.mod（如 pgvector-go）；goose 迁移文件下一版本号是否与 spec 假设一致
4. **测试基线**：现有测试组织（同包 `*_test.go`、表驱动、stub 内嵌接口、sqlmock、httptest）；跑一次基线确认全绿

```bash
# 确认基线全绿（基线不绿先报告用户，不在脏基线上开工）
go build ./... && go vet ./...
go test ./... -race -count=1
```

5. **项目规范**：CLAUDE.md 与本篇相关的强制约定（依赖方向、SQL 红线、SSE 约束——chat 相关必读《外部 LLM 调用设计》）

## 第 2 步：第一轮澄清（范围与歧义）⏸

探索完成后，汇总发现并用 AskUserQuestion 澄清。**只问真正有歧义的问题**，典型澄清点：

- spec 任务清单与代码现状不一致（部分已实现、占位与 spec 描述冲突）
- spec 语焉不详的行为语义（边界条件、错误处理策略、默认值）
- spec 与 CLAUDE.md 或既有代码的冲突（如命名、层职责、SQL 规范）
- 本次范围边界（单篇 or 连续几篇；是否包含 spec 标注「留待后续」的项）
- CLAUDE.md 表述与 spec 定稿有偏差处（如「固定长度分块」vs 递归分割）——经用户确认后**顺手更新 CLAUDE.md**，不默改

澄清原则：

- 每轮问题聚焦一个阶段的决策，不把所有问题堆在一轮
- 每个问题给出带推荐项的选项，说明各选项的影响
- **实现过程中每遇到新歧义，立即暂停并单独发起澄清**，不得用假设继续
- 用户的回答记入实现计划，后续不再重复询问已确认事项

## 第 3 步：制定实现计划

基于 spec + 探索 + 澄清结果，输出实现计划（TodoWrite 跟踪）：

1. **四层映射**：把 spec 交付物映射到包结构——api（契约：接口/schema/哨兵）→ service/model.go（实体）→ service（业务编排）→ store（数据访问）→ handler（HTTP 薄绑定）→ 组合根（server.go 装配）；明确依赖方向与注入点
2. **任务排序**：优先遵循 spec 自带的「实现顺序」节；无标注时按 模型/迁移 → 契约 → service → store → handler → 装配 排序；纯函数（如 chunker）可并行先行；每个任务粒度 = 一个 TDD 循环可完成（一个函数 / 一个方法族 / 一组表驱动用例）
3. **逐任务标注**：对应的 spec 小节、目标文件、测试文件、验收点；迁移任务单列（migrations/NNNNN_*.sql，goose 格式，Up/Down 成对）
4. 计划呈现给用户确认后再进入实现（此处修正成本最低）

## 第 4 步：TDD 实现循环

对计划中的每个任务，严格执行：

```
单任务循环:
1. RED    — 依据 spec 的接口契约与行为语义先写测试（同包 *_test.go，表驱动优先）
            go test 跑一遍，确认因"功能未实现"而失败——新符号未定义导致的编译失败是 Go 的正常 RED；
            若因无关语法/导入错误而红，先修测试本身
2. GREEN  — 写最小实现使测试通过，不做超出本任务的设计
3. REFACTOR — 在测试保护下消除重复、对齐邻近代码风格（不改变行为）
4. 门禁   — go build ./... && go vet ./... && go test ./... -race -count=1 全绿
5. 勾掉对应 todo，进入下一个任务
```

实现要求：

- **契约逐字对齐 spec**：类型名、方法签名、常量值、SQL 形态、哨兵错误文案（= `error.code`）；引用 spec 条目用其编号（如「spec 04 §2 端点 6」），不自造同义词
- **仓规红线**（CLAUDE.md 强制，实现时逐条对照）：
  - 跨模块只 import 对方 `api` 包（别名 `<module><layer>`，如 `providerapi`）；service 全程无 gin 类型
  - 禁 AutoMigrate；迁移只增不改；DDL 走《建表执行清单》
  - 禁 `SELECT *`（显式列清单）；DML 必带 WHERE；参数类型对齐列类型
  - 事务最小化、事务内禁外部调用（HTTP/LLM/Redis）；异步 goroutine 脱钩请求 ctx 用 `context.WithoutCancel`
  - 错误链路：store 原样上抛 → service 翻译哨兵（`errors.Is` 可判）→ handler 映射状态码走 `respond.Fail*`；任何一层不吞错
  - 明文凭据（API Key）只在调用瞬间存在，禁止入日志/缓存/响应
- **测试隔离**：单测零真实网络、零真实 LLM、零真实 PG/Redis——store 用 sqlmock、handler 用 httptest、下游用 stub（内嵌接口只覆写用到的方法）；纯函数（chunker 等）直接表驱动
- 范围红线：只实现本篇 spec 清单内的内容；发现「顺手改进」冲动时记录下来问用户，不直接做
- **提交**：按 conventional commits（feat/fix/refactor/test/chore，无 attribution）；**commit 时机由用户明示**——本仓惯例是攒到波次门（spec 的「可提交」节点）按模块/层次拆分提交，不逐任务自动 commit

## 第 5 步：全量自检

所有任务完成后，交付前自查：

```bash
go build ./... && go vet ./...                              # 编译 + 静态检查
go test ./... -race -count=1                                # 全量测试
go test ./internal/<module>/... -race -cover                # 覆盖率 ≥80%（spec 另有门槛从其规定）
make migrate-status                                         # 若本篇含迁移：确认 applied
cd web && npm run type-check                                # 若本篇动了前端（vue-tsc）
```

- 对照第 0 步提取的任务清单逐条核对：每项均有实现 + 测试，无范围蔓延
- 若本篇含组合根装配：`make start` 启动无报错 + `curl /health` + 一条路由冒烟（spec 验收门有 curl 清单则照跑）
- 覆盖率不达门：补测试，不调低门槛

## 第 6 步：联动验证 ⏸

自检通过后，调用 `rdp-verification` skill 对本次实现做全面验证，传入同一份 spec 文档路径：

- 验证报告中的 **Critical/Major 问题必须修复**后重新验证，直至通过
- 验证发现的契约偏差不得通过改 spec 解决，回到实现侧修正（spec 修订需用户显式批准）
- 最终向用户汇报：任务完成情况、验证结论、遗留的 Minor 建议、建议的提交拆分

## 注意事项

- 基线不绿、前置 spec 未闭环、spec 与现状冲突——三类情况都必须先与用户确认，不得带病开工
- 所有结果如实呈报：测试失败就报失败，RED 被跳过就说明原因，不得粉饰
- 实现中途发现 spec 契约无法实现（依赖的下游接口不存在、SQL 语义不成立），立即停止并报告冲突证据
- 多篇 spec 连续实施时，每篇过自己的验收门再进下一篇；波次提交门（如「05 后骨架冒烟可提交」）到点提醒用户
- 长任务用 TodoWrite 持续维护进度，保证中断后可恢复
