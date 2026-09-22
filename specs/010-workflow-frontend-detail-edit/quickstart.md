# Quickstart: 工作流详情 / 编辑前端（010-workflow-frontend-detail-edit）

端到端验证指南。字段级契约见 [contracts/workflow-frontend-api.md](contracts/workflow-frontend-api.md)，数据流与不变量见 [data-model.md](data-model.md)；本文只给可执行步骤与预期结果。

## 前置

```bash
make start ENV=dev          # 后端 :8080 + PG + Redis（或 docker compose 形态）
cd web && npm run dev       # 前端 :5173（/api 代理到 8080）
```

- 已登录账号（注册开放）；至少一个 enabled 的 chat 模型（llm 节点下拉数据源）。
- 测试数据：一条 chat 型 + 一条 task 型（含 Schema 字段）工作流——可用「新建工作流」先造（两步式本身就是被测对象，见场景 A）。
- 已知工作流 id（列表页 URL 或后端 `docs/testing/workflow-engine-manual-test.md` 造数）。

## 自动化门（SC-001）

```bash
cd web && npm run type-check && npm run build   # 预期：全绿（build chunk 警告为既有非阻断项）
cd .. && go build ./... && go vet ./...          # 预期：全绿（零后端改动回归）
git status --short                               # 预期：无 internal/ 与 migrations/ 路径
```

## 场景走查（人工，SC-002~SC-006）

### A. 创建两步式（US4 / FR-013、FR-014）

1. 列表 →「新建工作流」→ 第一步只有表单（无画布/模式切换）；填名称、选 task 型 → Schema 行表单出现，加两行（含一个故意重名）→ 点「创建工作流」→ **前端拦截并提示具体行与原因**（SC-004）。
2. 改正后进入 → `/workflows/create/orchestrate` 整页编排，初始渲染**预填智能客服分类示例图**（画布默认；切 JSON 两模式内容一致）。
3. 拖入两个节点、连线、在检查器改一个 Prompt → 切 JSON 核对等价（未知键透传）→「保存并创建」→ 成功提示 + 跳回列表，新行出现。
4. 重走一遍到第二步 → 点「上一步」→ 第一步内容（含 Schema 行）保留 → 再进第二步 → 图编辑结果仍在（store 回写）。
5. 直接刷新/新开标签访问 `/workflows/create/orchestrate` → 回到第一步。

### B. 详情只读（US1 / FR-001、FR-003~FR-006）

1. 列表行点「查看」→ `/workflows/:id`：基础信息齐全（名称/描述/类型/状态/创建时间），**默认画布**渲染图编排。
2. task 型：入参/出参 Schema 只读表正确展示；chat 型：无 Schema 区。
3. 画布上尝试拖节点/拖入/连线/双击 → 均无效；平移缩放可用。切 JSON → 美化只读文本，内容与画布一致。
4. 访问不存在 id（如 `/workflows/999999`）→ 错误提示 + 回列表。
5. 点「编辑」→ 进入编辑页。

### C. 编辑保存（US2 / FR-002、FR-007~FR-010）

1. 编辑页 GET 回填：工具栏名称/描述、type 禁用单选、task 型 Schema 抽屉行表单、图编排（画布默认）。
2. 改一个节点 Prompt →「保存」→ 成功提示 + 回详情页 → 再查看是新值（SC-003 往返）。
3. **不改直接保存** → 再查看配置不变（含未知 config 键、连线条件标签）。
4. 改名成与另一工作流重名 → 保存 → 409 提示、留在编辑页、内容不丢。
5. 画布删边造悬挂/环 → 保存 → 后端图规则 400，文案带定位、留页。
6. 有修改时点「返回」/ 切侧边栏菜单 → 确认框（放弃/继续编辑）；无修改直接离开。
7. 有修改时刷新/关闭标签 → 浏览器原生离开确认（beforeunload）。
8. 已发布工作流编辑保存 → 成功且 status 仍 published（编辑不降级）。

### D. 双模式一致性抽查（SC-002）

同一工作流（建议含 condition 连线与一个未知 config 键）在**创建第二步 / 详情只读 / 编辑回填**三处分别切 JSON ↔ 画布，文本与画布节点/边/条件标签一一对应。

## 完成判定

自动化门全绿 + 上列场景全部符合预期 → spec SC-001~SC-006 达成；残余项（真实浏览器矩阵、SSE 无关）无。详细分步清单以 `docs/testing/workflow-frontend-manual-test.md` 增补节（任务 T0xx 交付）为准。
