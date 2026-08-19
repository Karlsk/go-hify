# Spec：sync 导入默认停用（enabled=false），按需勾选启用

状态：待实施。前置批次：wiring_sync_spec.md（sync 真实实现 + M-1 列级更新）。

## 0. 需求与决策

需求：上游目录全量 sync 会把几十上百个模型全部导入为"可用"，团队只想用其中几个（"不全 sync"）。

备选方案：

| 方案 | API 面 | 新增代码 | 选择状态存哪 |
|---|---|---|---|
| A. discover 预览 + import 批量添加 | +2 端点 | schema + service + handler + 测试 | 库内行（只导入选中的） |
| **B. sync 导入默认 enabled=false + 勾选启用（采纳）** | +0 端点 | 1 行默认值 + 测试/文档 | `models.enabled` 字段本身 |

决策：**B**。理由：零新 API 面（一人维护优先）；选择状态持久化在 enabled 字段、跨会话/用户共享；且与 db_model.md 原始设计意图一致——`enabled` 列注释本就是"发现会拉回上百个模型，只留常用的"、"sync 不覆盖"，当前实现的插入即 true 反而偏离原设计，本批补全。

## 1. 已验证的既有行为支撑（方案 B 成立的前提）

| # | 验证点 | 代码证据 |
|---|---|---|
| 1 | PUT 能翻 enabled | `UpdateModelReq.Enabled bool`（api/schema.go:305）→ service `m.Enabled = req.Enabled`（service.go:511） |
| 2 | PUT 保留 source=discovered | Update 逐字段赋值不含 Source、req 无该字段 → 勾选启用后仍归 discovered，后续 sync 的 UpdateModelName（WHERE source='discovered'）继续跟进上游改名 |
| 3 | sync 重跑不打回用户勾选 | UpdateModelName 列级只写 name/updated_at（M-1 修复红利；若仍是全列 Save，每次 sync 都会把 enabled 打回默认、勾选丢失） |
| 4 | manual 创建默认 enabled=true 不变 | `CreateModelReq.Enabled *bool` 省略=true（api/schema.go:272）——不对称是刻意的：手动建=明确要，目录发现=待启用 |

前端交互闭环（全部现有端点）：`POST /providers/:id/models/sync`（全量拉入，全 disabled）→ `GET /providers/:id/models`（列表带 enabled，勾选框渲染）→ 勾选 `PUT /models/:id {"enabled":true,...}` → detail 缓存已失效，立即可见。

## 2. 改动清单

1. **代码（1 行）**：`internal/provider/service/sync.go` `applyDiscovered` 插入路径 `Enabled: true` → `false`；同步更新 `SyncModels` / `applyDiscovered` doc comment。
2. **测试**：
   - `sync_test.go` `TestSyncModels_Kinds` 等断言 `Enabled` 改为 false；
   - 新增：sync 后 PUT 启用（走 modelService.Update）→ 再次 sync → 断言 enabled 仍为 true 且 name 刷新仍生效（覆盖 §1-2/§1-3 两条链路）。
3. **文档**：
   - wiring_sync_spec.md §3.3 表格：插入行 `Enabled=true` → `false`（待启用，勾选走 PUT /models/:id）；
   - provider-manual-test.md §5：预期 `enabled:true` → `false`，新增勾选启用走查（PUT 后列表/详情可见）；
   - db_model.md §models.enabled 补一句"新发现行默认 false，勾选启用走 PUT"。

## 3. 语义记录（显式决策）

- sync 语义从"导入即可用"变为"导入为待启用"——全量开场景多一步，接受；将来前端可做批量启用交互（不在本批）。
- **取消勾选 = 同一 PUT 翻 `enabled:false`，本批零改动**；软停用而非删行（行保留、re-check 即恢复，真删走 DELETE + ErrModelInUse 挡板）。停用被 agent 引用的 model **不设 in-use 挡板**——FK 挡 DELETE 防引用悬空，"引用还在但不再可用"正是软停用的合法语义。
- `models` 表镜像上游全目录（用 2 留 50）：配置表极小、已分页，可接受；**agent/聊天侧将来选模型的下拉一律按 `enabled=true` 过滤**——chat/agent 模块未实施，现在定此语义正是时机。
- **勾选交互形态（前端任务参考，本批不定死）**：首选逐行开关（models 列表每行 el-switch、点击即一个 PUT）——"二次打开勾选集合变化需 diff"的问题在该形态下不存在；若做选择器弹窗+确认，diff 留前端（Set 对比 + 逐条 PUT，变化数小且并发友好，只动自己改过的行）。不做后端批量声明式端点（全量集合提交需解决 manual 行误伤边界与 id 异常分支，低频管理操作不值得）。
- 后续决策点（chat/agent 实施时定，不属本批）：agent 引用的 model 被停用后，chat 发起新会话的错误语义（新增 `MODEL_DISABLED` 哨兵还是复用现有码）；provider 模块只保证 `enabled:false` 事实可查。
- 手测/开发环境已存在的 enabled=true discovered 行：一次性手动处理（删了重 sync 即全为 false）。

## 4. 范围外

- 不加 discover / import 端点（将来若要"入库前预览目录"再评估）；
- 不加批量启用端点（前端逐条 PUT 即可，量小）；
- 不做"一键全启用"快捷方式。

## 5. 测试计划

- service 层：`TestSyncModels_Kinds`（4 kind 断言 false）+ 新增勾选启用保持测试 + 既有幂等/手编保护回归；
- 全量：gofmt / build / vet / `go test -race -cover ./internal/provider/...`，覆盖率不低于现基线。
