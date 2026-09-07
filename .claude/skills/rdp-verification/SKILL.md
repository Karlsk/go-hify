---
name: rdp-verification
description: 对指定 spec（docs/changelog/<module>/backend_spec_*.md 等）中已实现的任务进行全面测试验证并输出验证报告，覆盖编译、单元测试、功能、接口兼容性、测试覆盖率、向后兼容与性能七个维度，严格遵循 CLAUDE.md 仓规。rdp-implementation 第 6 步联动调用，或用户要求「验证某 spec 的实现 / 全面测试验证 / 产出验证报告」时使用。
argument-hint: [spec 文档路径，如 docs/changelog/rag/backend_spec_04_crud.md]
---

# RDP 任务测试验证

## 适用场景

- 用户要求对某篇 spec 任务的实现进行全面测试验证
- 某个开发阶段 / 波次（提交门）完成后需要验收
- `rdp-implementation` 第 6 步联动调用（传入同一份 spec 路径）
- 用户调用 `/rdp-verification <spec文档路径或任务描述>` 时触发

## 第 0 步：确定验证目标与基准

**输入解析**（来自调用参数或用户消息）：

1. 定位本次验证的 spec 文档（路径由调用方传入；**未传入则询问用户**，不得猜测——本仓 spec 系列常为多篇，篇号即范围）
2. 通读 spec 全文及其引用的上位文档（如 rag 的总览 backend_module_spec.md），提取以下要素作为验证基准：
   - **任务清单**：本篇交付物（spec 自带的「交付」节及实现顺序）及其编号
   - **目标代码范围**：涉及的包路径（记为 `<TARGET_PATH>`，如 `./internal/rag/...`、`./internal/platform/llm/...`，用于覆盖率统计与依赖检查）、`migrations/`、`web/`
   - **接口契约**：spec 冻结的函数签名、SQL 形态、常量值、哨兵错误文案（= `error.code`）、行为语义
   - **验收门**：spec 末节「单测与验收门」= 本篇 DoD（含覆盖率门槛，默认 ≥80%）
   - **约束规则**：依赖方向限制（CLAUDE.md《代码组织规范》依赖清单）、SQL 红线、范围红线
3. 结合 CLAUDE.md 的项目规范（《代码组织规范》《数据库规范》《接口规范》《外部 LLM 调用设计》）作为通用基准；`module-delivery` skill 的「踩坑清单」作为常见缺陷清单

**基准优先级**：spec 文档中的明确要求 > CLAUDE.md 仓规 > `~/.claude/rules/golang` 全局规则 > 本 skill 的默认标准。

**前置门禁**：spec 头部标注的前置篇（如「前置依赖：01、04」）应已合入；未闭环需在报告中标注，不静默跳过。

## 验证流程

复制以下清单并逐项跟踪进度（TodoWrite）：

```
验证进度:
- [ ] 0. 确定验证目标与基准
- [ ] 1. 编译验证
- [ ] 2. 单元测试验证
- [ ] 3. 功能验证
- [ ] 4. 接口兼容性验证
- [ ] 5. 测试覆盖验证
- [ ] 6. 向后兼容验证
- [ ] 7. 性能验证
- [ ] 8. 输出验证报告
```

### 1. 编译验证

```bash
go build ./... && go vet ./...          # 编译 + 静态检查（全仓）
cd web && npm run type-check            # 仅当本篇动了前端（vue-tsc）
make migrate-status                     # 仅当本篇含迁移：确认 applied 且无 pending
```

**通过标准**：所涉命令全部零错误退出。build / vet / type-check 任一失败即视为编译验证不通过。

### 2. 单元测试验证

```bash
go test ./... -race -count=1            # 全仓（-race 必带，仓规）
```

**通过标准**：

- 全部测试通过，零失败、零跳过（skip 需逐一说明理由）
- 与本篇 spec 任务对应的测试（各篇「单测与验收门」表列出的重点用例）全部存在且通过——缺失记为问题，不只看全绿
- 测试中**零真实网络调用、零真实 LLM 调用、零真实 PG/Redis**（仓规隔离要求：sqlmock / httptest / stub）；发现依赖外部服务的测试记为问题

### 3. 功能验证

对照第 0 步提取的任务清单逐条核对：

1. 列出 spec 本次范围内的全部任务项（含编号，如「spec 04 §2 端点 6」）
2. 对每个任务项，确认存在对应实现代码与对应测试（文件:行号 引用）
3. 若 spec 定义了行为语义（边界条件、错误路径、事务规则），核对实现与测试是否逐条符合——重点核对不变量与事务规则（如 rag 的「文档离开 ready 必须同事务删 chunks」「embedding 永不进事务」「异步 goroutine 用 `context.WithoutCancel`」）
4. 运行 spec 验收门标注的命令（若有；人工 curl 冒烟默认不纳入，见注意事项）

**通过标准**：每个任务项均有实现 + 测试覆盖；无超出 spec 范围的实现（范围蔓延记为问题）。

### 4. 接口兼容性验证

若 spec 定义了接口契约（四层签名、SQL、常量、哨兵）：

1. **逐字比对**公共契约：接口方法集、方法签名（参数名/顺序/类型/返回值）、Req/Schema 字段与 `json` tag、常量值、哨兵错误文案（= `error.code`，前端靠它分支）、SQL 形态（列表、WHERE、ORDER BY、事务包裹）
2. 可用 `go doc` 辅助导出实际签名进行比对：

```bash
go doc ./internal/<module>/api          # 实际导出的接口与类型
```

3. 核对仓规形制：import 别名 `<module><layer>`、类型名不与包名叠字、缩写词全大写（`APIKey` 非 `ApiKey`）、`json:"id,string"`、列表字段空返 `[]` 不返 `null`

若 spec 未定义接口契约（纯内部函数篇，如 chunker），则检查导出符号有完整 doc comment、签名符合 spec。

**通过标准**：契约与 spec 完全一致。发现偏差**不得自行修改 spec**，在报告中标记为 Critical 并说明冲突点——修复只能回实现侧（spec 修订需用户显式批准）。

### 5. 测试覆盖验证

```bash
go test ./internal/<module>/... -race -count=1 -cover -coverprofile=cover.out
go tool cover -func=cover.out           # 每函数覆盖率 + 末行总计
```

**通过标准**：

- 覆盖率达到 spec 标注的门槛；spec 未标注时默认 ≥80%
- 记录每个包的覆盖率，对未达标包列出低覆盖函数（`go tool cover -func` 输出中 0% 与低百分比项），并给出补测建议

### 6. 向后兼容验证

1. **依赖方向检查**（CLAUDE.md 依赖清单）：目标模块不得 import 上游模块、不得 import 下游模块的非 `api` 包：

```bash
# 示例：rag 只许依赖 provider/mcp 的 api + platform；按目标模块改清单
grep -rn "internal/agent\|internal/chat\|internal/workflow" internal/rag/ && echo "VIOLATION" || echo "OK: 无违规依赖"
grep -rn "internal/provider/service\|internal/provider/store\|internal/provider/handler" internal/rag/ && echo "VIOLATION" || echo "OK"
# service/store 全程无 gin
grep -rn "gin-gonic" internal/<module>/service/ internal/<module>/store/ && echo "VIOLATION" || echo "OK"
```

2. **仓规红线 grep**：

```bash
grep -rn "AutoMigrate" internal/ migrations/ && echo "VIOLATION" || echo "OK: 无 AutoMigrate"
grep -rniE "SELECT \*" internal/ && echo "VIOLATION" || echo "OK: 无 SELECT *"
grep -rn "slog.*\(APIKey\|BaseURL\|apiKey\)" internal/ && echo "VIOLATION" || echo "OK: 明文凭据未入日志"
```

3. **迁移只增不改**（仅当本篇含迁移）：既有迁移文件被修改记为问题，只允许新增：

```bash
git status --porcelain migrations/      # M = 问题；A/?? (新增) = 合法
```

4. **既有应用不受影响**：主入口编译通过 + 全量测试无回归：

```bash
go build ./cmd/hify && echo "OK: 编译正常"
go test ./... -race -count=1
```

**通过标准**：无违规依赖、无红线命中、迁移只增不改、既有测试无回归。

### 7. 性能验证

轻量级性能冒烟（不做基准压测）：

```bash
# 各包测试耗时（>5s 的包/单测记为问题——检查是否误用真实 I/O 或 sleep 过长）
go test ./<TARGET_PATH>/... -race -count=1 -v 2>&1 | grep -E "^(ok|FAIL|--- PASS|--- FAIL)"

# 模块级 init 检查（init 内不应有网络/DB/Redis 初始化）
grep -rn "^func init()" internal/<module>/
```

**通过标准**：

- 无单包测试耗时异常（>5s 记为问题，检查是否误用真实 I/O）
- 无模块级重初始化（init / 包级 var 建连）
- 事务内禁外部调用（HTTP / LLM / Redis）——第 3 步已核对，此处复核嫌疑点
- 若 spec 定义了具体性能指标（如 bulkhead 槽位、内存峰值上限），按 spec 指标核对实现与测试断言

### 8. 输出验证报告

按下述模板输出完整报告（直接回复用户，不创建文件，除非用户要求落盘）。

## 验证报告模板

```markdown
# spec 任务验证报告

**验证对象**: <spec 文档 / 任务范围>
**验证时间**: <日期>
**验证基准**: <spec 路径> + CLAUDE.md

## 结果总览

| # | 验证项 | 结果 | 说明 |
|---|--------|------|------|
| 1 | 编译验证 | ✅/❌ | build / vet / type-check / migrate-status |
| 2 | 单元测试 | ✅/❌ | X passed, Y failed |
| 3 | 功能验证 | ✅/❌ | 任务项 N/N 通过 |
| 4 | 接口兼容性 | ✅/❌ | 契约逐字比对结果 |
| 5 | 测试覆盖 | ✅/❌ | XX%（门槛 YY%） |
| 6 | 向后兼容 | ✅/❌ | 依赖方向 / 红线 / 回归 |
| 7 | 性能验证 | ✅/❌ | 慢测试 / init 检查 |

**总体结论**: <通过 / 有条件通过 / 不通过>

## 详细结果

### <逐项展开，附关键命令输出摘要>

## 发现的问题

按严重程度分级：
- **Critical**（阻断验收：契约签名/常量/哨兵偏差、测试失败、覆盖率不达标、违规依赖、AutoMigrate）
- **Major**（需修复：慢测试、范围蔓延、隔离缺失、红线 grep 命中）
- **Minor**（建议改进）

每个问题包含：问题描述、证据（命令输出/文件:行号）、影响范围、解决方案建议（引用 spec 对应条目编号）。

## 解决方案建议

<针对每个问题给出可执行的修复建议，引用 spec 文档自身的编号体系>

## 与实现的闭环

<Critical/Major 问题回 rdp-implementation 修复后须重新验证；契约偏差只能改实现侧>
```

## 注意事项

- **只验证、不修改**：验证过程中发现问题只记录进报告，除非用户明确要求修复（修复走 rdp-implementation，修复后重新验证）
- 引用问题时使用 spec 文档自身的编号体系（篇号 + 小节 + 端点号），不得自造同义词
- **人工验收默认不纳入**：需要真实 PG/Redis/LLM 环境的端到端 curl 冒烟（如 spec 08 §3/§4）与 soak 测试默认不执行；用户明确要求时才做（需本地 `make start` 环境可用）
- 若 spec 标注了前置门禁（前置篇、波次门），先确认其已闭环，未闭环在报告中标注
- 所有命令输出**如实呈报**：测试失败就报失败，grep 无命中就报无命中，不得粉饰结果
- 明文凭据（API Key / BaseURL）在验证过程中同样不得写入报告或日志
