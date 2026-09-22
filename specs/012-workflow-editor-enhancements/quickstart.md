# Quickstart: 工作流拖拽编辑器八项增强

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-22

本篇纯前端，验证 = **双门禁全绿（机器）+ 本文档人工场景（人工）**。无单测基建（软门禁约束不引入测试框架），全部行为验证走 dev 栈人工场景，结果记入 `docs/testing/workflow-frontend-manual-test.md` 增补小节。

---

## 前置

```bash
# 后端 + 前端 dev 栈（spec 009/010 既有起法）
make start ENV=dev        # 或分别起 go 与 web dev server（见 web/README.md）
```

- 需要一个已存在的 task 型工作流（带 input_schema）与一个 chat 型工作流（详情页切换类型核对 US5-2）。
- 需要两个子工作流候选：一个声明 input_schema `[question, top_k]`、一个声明 `[question, lang]`（同名保留验证）、一个无 schema（提示验证）。

## 机器门禁（实现完成的定义）

```bash
cd web && npm run type-check && npm run build
```

预期：vue-tsc 零错误；vite 构建成功（chunk 体积警告非失败）。

---

## 场景 1（US1）：删除入口与 key 改名

1. 拖入 3 节点 A→B→C 连线，选中 B → 检查器点「删除节点」→ B 与其两条连线消失、A→C 间无悬挂边；切 JSON 核对图主体无 B。
2. 删除起始节点 A → 起始迁移到剩余首节点（画布伪节点箭头随动）。
3. 选中连线 → 检查器显示连线信息 → 点删除 → 连线消失。
4. 节点 llm_1 检查器改 Key 为 classify → 节点/连线端点/起始指向/位置随动；切 JSON 核对旧 key 零残留（含 edges、positions 无泄漏——以切回拖拽模式位置不变为证）。
5. 已有 key classify 时把另一节点改为 classify → 前端拦截提示、图不变；清空 Key 同理。
6. Key 与原值相同 → 无变化（幂等）。

## 场景 2（US2）：变量下拉与混排

1. task 型图已配 input_schema 字段 q，拖入 llm（上游有节点）→ Prompt 字段点变量下拉 → 分组列出：入参（`{{input}}`、`{{input.q}}`）、上游节点（祖先 key）、子流程出参（若有上游 workflow 节点：`{{key.field}}`）。
2. Prompt 文本中间放光标，手写前后缀，下拉选 `{{input.q}}` → 插入在光标处，前后文本不动。
3. 纯手写 `{{任意}}` → 不被清洗/校验拦截。
4. chat 型图（无 input_schema）→ 下拉只有 `{{input}}` 与上游引用，无入参展开组。
5. 上游 workflow 节点绑定子流程后再次打开下拉 → 出参分组出现（首次拉取有短暂 loading，再次即缓存命中）。

## 场景 3（US3）：LLM 双输入框与 API 全要素

1. LLM 节点：填 System Prompt「你是分类器」+ Prompt「分类：{{input.q}}」→ 切 JSON 核对 config 含 `system_prompt`/`prompt` 两键；清空 System Prompt → config 不携带该键（空串=缺省）。
2. API 节点 Method 切 POST → Body 域出现；填 Body 模板、加 Header 行 `Content-Type=application/json`、Auth 选 Bearer 填 token → 切 JSON 核对 `config.headers` 含两行（Authorization 值 `Bearer <token>`）、`config.body` 为模板文本。
3. Auth 切 Basic 填账密 → headers 仅一条新 Authorization 行（`Basic base64(...)`）、旧 Bearer 无残留；再切「无」→ Authorization 行消失。
4. 保存工作流 → 详情回读：API 节点 Auth 预设正确推导为 Basic、Header 行回显。

## 场景 4（US4）：子工作流入参自动渲染

1. 父图选子流程（声明 `[question, top_k]`）→ 检查器渲染两行入参：字段名只读、值域可下拉填模板。
2. 给 question 填 `{{input.q}}`，切换到声明 `[question, lang]` 的另一子流程 → question 行保留已填值，top_k 行消失、lang 行新建。
3. 切到无 schema 子流程 → 显示提示而非空表单。
4. 编辑工作流 W 自身 → 子工作流下拉不含 W；创建态（未落库）→ 下拉不排除（无自身可引用）。

## 场景 5（US5）：伪开始节点与画布内 schema 配置

1. task 型画布左端伪「开始」节点常驻（空画布也在），箭头指向当前起始节点；点击 → 检查器显示入参/出参两段行表单。
2. 伪节点面板加字段 q → 切到两步式第一步（或编辑页抽屉）→ 表单已同步出现 q；反向在第一步删 q → 面板同步消失。
3. 保存 → 提交载荷（Network 面板或详情回读）input_schema 含最新字段，图主体无伪节点痕迹。
4. chat 型画布 → 点击伪节点显示单一 input 说明，无编辑。
5. readonly 详情态 → 伪节点可见但点击无面板交互、全部新控件禁用。

## 场景 6（US6）：存量回归（既有 EC 全数复测）

1. 按 `docs/testing/workflow-frontend-manual-test.md` §5 EC-1~11、§7 EC-12~23 逐项复测（双模式往返一致、未知 config 键透传、脏态守卫、两步式创建、编辑保存链路）。
2. 重点往返：含未知 config 键（如手写 `timeout_sec`）的工作流拖拽 ↔ JSON 切换 → 键不丢。
3. readonly 详情走一遍全部新增交互入口 → 零可点。

---

## 对号表（spec SC ↔ 场景）

| SC | 验证点 | 场景 |
|---|---|---|
| SC-001 | 八项缺口画布/检查器可完成 | 场景 1~5 全部 |
| SC-002 | 零回归（含 EC-1~23 复测、未知键透传） | 场景 6 |
| SC-003 | 载荷契约对齐（图主体无伪节点、config 键集不越界、回读逐键） | 场景 3-4、5-3 |
| SC-004 | readonly 零新增交互 | 场景 5-5、6-3 |
| SC-005 | 双门禁全绿 | 「机器门禁」节 |
