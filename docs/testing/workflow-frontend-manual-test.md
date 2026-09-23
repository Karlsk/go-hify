# Workflow 前端人工冒烟测试

验证对象：`web/` 工作流管理前端——`/workflows` 列表与创建流程（spec 009，JSON/拖拽双模式）+ 详情 / 编辑 / 两步式创建（spec 010 增补，见 §7）+ 拖拽编辑器八项增强（spec 012 增补，见 §8）+ 试运行对话框与双入口（spec 013 增补，见 §9）。
全部为手工浏览器步骤；自动化门槛为 `cd web && npm run type-check && npm run build`（SC-001）。
行为依据：[specs/009-workflow-frontend/spec.md](../../specs/009-workflow-frontend/spec.md)（FR-001~FR-014、SC-004/SC-006、Edge Cases 11 项）与 [specs/010-workflow-frontend-detail-edit/spec.md](../../specs/010-workflow-frontend-detail-edit/spec.md)（FR-001~FR-017、Edge Cases 12 项）与 [specs/012-workflow-editor-enhancements/spec.md](../../specs/012-workflow-editor-enhancements/spec.md)（FR-001~FR-009、SC-001~SC-005）与 [specs/013-agent-bind-trial-run/spec.md](../../specs/013-agent-bind-trial-run/spec.md)（FR-001~FR-008、SC-001~SC-005）。

- 页面入口：侧边栏「工作流管理」（登录后可见；未登录访问跳登录见 §5 EC-11）
- 后端：workflow spec 01~08 已交付（8 端点）；接口层冒烟见 [workflow-manual-test.md](workflow-manual-test.md)
- 本文档只走前端行为；涉及 curl 的前置造数命令在**仓库根目录**执行

## 0. 前置条件

1. 启动（二选一）：
   - dev：`make start ENV=dev` → 浏览器开 `http://localhost:5173`（vite 代理 `/api` 到后端）
   - prod：`make start ENV=prod` → `https://<host>`（nginx 443）
2. 一个可登录账号（`POST /api/v1/auth/register` 或注册页）
3. 模型下拉数据源（§4 llm 节点 + §5 EC-8 需要）：一个 **enabled** provider + 其下至少一个 **enabled** 的 capability=chat 模型（提供商管理页配置，或 [provider-manual-test.md](provider-manual-test.md) §3 造）
4. 「被 Agent 绑定的工作流」（§5 EC-1 / EC-10 需要）：
   1. 用 §2 或 §3 任一模式先建工作流 A（拿到其 id，列表页可见）
   2. 建 Agent 绑定 A（curl 参考 [agent-manual-test.md](agent-manual-test.md) §12：`POST /api/v1/agents` 带 `workflow_id`）
5. task 型工作流（§4 workflow 节点 + §5 EC-9 需要）：§2 创建时类型选「任务型」（I/O schema 可留空）

## 1. 登录与入口

1. 登录后侧边栏出现「工作流管理」菜单项 → 点击 → 进入 `/workflows` 列表页
2. 预期：PageHeader 标题「工作流管理」+ 描述 + 右上角「新建工作流」按钮；表格列 = 名称 / 类型 / 状态 / 创建时间 / 操作

## 2. US1 列表页（六场景）

### 2.1 三态展示

1. 列表存在 draft / published / disabled 三种状态的工作流（不够就按 §3 / §2.2 造）
2. 预期：状态列 tag 三态差异化（草稿 / 已发布 / 已停用）；类型列 chat=「对话型」、task=「任务型」；创建时间可读格式

### 2.2 发布/停用轮转（draft→published→disabled→published）

1. 对 draft 行点「发布」→ 确认 → 预期：状态 tag 即时变「已发布」
2. 对 published 行点「停用」→ 确认框出现**后果文案**（提示绑定该工作流的 Agent 会话将不可用）→ 确认 → 状态变「已停用」
3. 对 disabled 行点「发布」→ 确认 → 状态回到「已发布」（disabled 可重新发布）
4. 预期：每次操作后表格自动刷新、无手动刷新；后端幂等（重复发布同状态不报错）

### 2.3 删除（二次确认）

1. 对无引用的工作流行点「删除」→ 红色确认框（提示不可恢复）→ 确认 → 预期：成功提示 + 行消失
2. 删除时点「取消」→ 行保留

### 2.4 删除被 Agent 绑定的工作流 → 409

1. 对 §0.4 造的「被绑定工作流 A」点「删除」→ 确认
2. 预期：错误提示（拦截器统一弹，WORKFLOW_IN_USE 语义），**行保留**、列表不误删

### 2.5 新建入口

1. 点右上角「新建工作流」→ 预期：跳转 `/workflows/create`，标题「新建工作流」

## 3. US2 JSON 模式创建（六场景）

> spec 010 起创建改为两步式（§7 场景 A）：第一步 `/workflows/create` 只有表单，点「创建工作流」进第二步 `/workflows/create/orchestrate` 后才出现 JSON / 拖拽双模式。本节步骤语义不变，执行位置 = 第二步页面。

### 3.1 预填示例 → 改名提交

1. 进入创建页：名称/描述空、类型默认「对话型」（无 schema 编辑区）
2. 预期：JSON 编辑器**预填智能客服分类示例**（llm→end 线性图，`classify` 起点，`model_id "1"`）
3. 名称填「冒烟-JSON」（描述可空）→ 点「创建工作流」
4. 预期：成功提示 → 跳回列表 → 新行（对话型 / 草稿）
   - 注意：示例里 `model_id: "1"` 需真实存在（§0.3 的模型），否则后端 400——那是模型不存在，不是前端缺陷

### 3.2 名称冲突 409 留页

1. 再建一次同名「冒烟-JSON」→ 提交
2. 预期：错误提示（名称冲突）；**留在创建页**，表单与 JSON 内容不丢

### 3.3 非法 JSON 三处阻断 + 原文不破坏

先把 JSON 改坏（如删一个 `{`），然后依次：

1. 点「格式化」→ 预期：错误提示含语法位置信息，**编辑器原文不变**
2. 切「拖拽」模式 → 预期：阻断切换 + 提示先修复，模式留在 JSON、原文不变
3. 直接提交 → 预期：阻断 + 提示，不发出请求

修好 JSON 后三动作均恢复正常。

### 3.4 task 型 schema 展开

1. 类型切「任务型」→ 预期：展开「入参 Schema / 出参 Schema」两个 JSON 文本域（可留空 = 不携带）
2. 填合法 schema（如 `[{"name":"query","type":"string","required":true}]`）提交 → 成功
3. 切回「对话型」→ schema 区隐藏（chat 型不制造后端 400 机会，详见 §5 EC-6）

### 3.5 名称必填

1. 名称留空（或纯空格）提交 → 预期：表单校验拦截（必填提示），不发请求

## 4. US3 拖拽模式创建（五场景 + SC-006）

> 同 §3：拖拽画布在第二步 `/workflows/create/orchestrate`（整页 fullBleed 形态）。

### 4.1 切画布 = 按当前配置渲染

1. JSON 模式（预填示例未动）→ 切「拖拽」
2. 预期：画布出现 2 个节点（分类节点 / 结束）+ 1 条连线；`classify` 带起始标识（主色边框强调）；左面板五类节点齐全、检查器显示「点击节点编辑配置」提示

### 4.2 空画布起步（不预置节点）+ 拖入连线

1. 切回 JSON → 全选删除文本（或清空为合法空图）→ 切「拖拽」→ 预期：**空画布**（无预置节点）
2. 从左面板拖「LLM 节点」进画布 → 预期：节点出现且**自动成为起始**（首个放入默认）
3. 再拖「结束节点」→ 非起始（无强调边框）
4. 从 LLM 节点右侧锚点拖到结束节点 → 预期：连线出现（箭头）

### 4.3 节点配置（右侧检查器）

1. 点击 LLM 节点 → 预期：右侧面板出现「LLM 节点」+ key（`llm_1`）；「模型」下拉按供应商分组列出 §0.3 造的 chat 模型；Prompt 文本域
2. 选一个模型 + 填 Prompt（如 `将消息分类为 A 或 B: {{input}}`）→ 点击结束节点 → 填 Output（如 `{{reply}}`）
3. 点空白画布 → 预期：检查器回到「点击节点/连线」提示态

### 4.4 连线 condition 标签

1. 点击 LLM→结束 的连线 → 预期：右侧面板切为「连线」表单
2. 填 Condition 标签（如 `A`）→ 预期：连线中点出现标签文本

### 4.5 切回 JSON 核对等价（SC-006 往返一致）

1. 切「JSON」→ 预期：编辑器内容 = 画布编辑结果（`llm_1` 起点、2 节点、连线带 condition、config 内 model_id 为**字符串**）
2. 手工在 LLM 节点 config 里加一个自定义键（如 `"my_key": "x"`）→ 切「拖拽」再切回「JSON」
3. 预期：`my_key` 原样保留（未知键往返透传，详见 §5 EC-7）
4. 名称填「冒烟-拖拽」→ 提交 → 预期：成功跳回列表，新行可见

## 5. Edge Cases 11 项走查（对应 spec Edge Cases 清单）

| # | 场景 | 步骤 | 预期 |
|---|---|---|---|
| EC-1 | 删除被 Agent 绑定的工作流 | §2.4 | 409 提示由拦截器统一弹；行保留不误删 |
| EC-2 | 创建名称冲突 409 | §3.2 | 留创建页，编辑内容不丢 |
| EC-3 | 非法 JSON 三处阻断 | §3.3 | 格式化 / 切拖拽 / 提交全阻断，原文不破坏 |
| EC-4 | 空画布提交 | 清空 JSON → 切拖拽 → 不拖节点直接提交 | 前端阻断（「至少需要一个节点」提示），后端 400 兜底不触发 |
| EC-5 | 删除起始节点迁移 | §4.2 画布删起始节点（选中后 Backspace） | start 迁移到剩余**首个**节点（其获得起始强调边框）；全删后提交被「未指定起始节点」阻断 |
| EC-6 | chat 型隐藏 schema | §3.4 步骤 3 | 对话型不显示 schema 编辑区（后端强不变量：chat 型带非空 schema 即 400） |
| EC-7 | 未知 config 键透传 | §4.5 步骤 2-3 | 模式往返后未知键原样保留；检查器表单只改已知字段 |
| EC-8 | 模型下拉空数据 | 无 provider/模型环境下点开 llm 节点 | 面板提示「先去配模型」，不阻断其他节点编辑与整体提交 |
| EC-9 | 子工作流只列 task 型 | 拖入「子工作流节点」→ 点开下拉 | 只出现 task 型工作流（§0.5 造的），chat 型不出现 |
| EC-10 | 停用被绑工作流 | §2.2 步骤 2 对被绑定工作流执行 | 停用**允许**（仅删除受挡）；确认框文案已提示「绑定会话将不可用」 |
| EC-11 | 未登录跳登录 | 退出登录 → 直访 `/workflows` 与 `/workflows/create` | 路由守卫跳登录页，登录后带回原目标页 |

## 6. 通过标准（SC-004 汇总）

全部勾选即 SC-004 / SC-006 人工项通过：

- [ ] JSON 模式创建成功（§3.1）
- [ ] 拖拽模式创建成功（§4.1~§4.5）
- [ ] 列表三态展示（§2.1）
- [ ] 删除成功（§2.3）
- [ ] 发布/停用轮转 draft→published→disabled→published（§2.2）
- [ ] 409 双场景：WORKFLOW_IN_USE（§2.4）/ WORKFLOW_NAME_CONFLICT（§3.2）
- [ ] 非法 JSON 三处阻断（§3.3）
- [ ] 双模式往返一致（含未知键保留，SC-006）（§4.5）
- [ ] Edge Cases 11 项全过（§5）

## 7. spec 010 增补：详情 / 编辑 / 两步式创建（四场景 + Edge Cases 12 项）

交付页面：`/workflows/:id` 详情（只读）、`/workflows/:id/edit` 编辑（整页 fullBleed）、`/workflows/create` 第一步表单 + `/workflows/create/orchestrate` 第二步整页编排。
前置同 §0；task 型 Schema 非法历史数据（EC-16）需 DB 直改造数（后端 API 造不进去——ValidateSchemaFields 会拒）。

### 7.1 场景 A：创建两步式（US4 / FR-013、FR-014）

1. 列表 →「新建工作流」→ 第一步**只有表单**（名称 / 描述 / 类型；无画布与模式切换）；选「任务型」→ 出现入参 / 出参 Schema 行表单，加两行（含一个故意重名）→ 点「创建工作流」→ 预期：前端拦截，提示含**行号与原因**，不发请求（SC-004）
2. 改正后点「创建工作流」→ 跳 `/workflows/create/orchestrate` 整页编排：初始渲染**预填智能客服分类示例图**（画布默认），切 JSON 两模式内容一致
3. 拖入两个节点、连线、检查器改一个 Prompt → 切 JSON 核对等价（未知键透传）→「保存并创建」→ 成功提示 + 跳回列表，新行出现
4. 重走到第二步 → 点「上一步」→ 第一步内容（含 Schema 行）保留；再进第二步 → 图编辑结果仍在（store 回写）
5. 直接刷新 / 新开标签访问 `/workflows/create/orchestrate` → 回到第一步（草稿内存态）

### 7.2 场景 B：详情只读（US1 / FR-001、FR-003~FR-006）

1. 列表行点「查看」→ `/workflows/:id`：基础信息齐全（名称 / 描述 / 类型 / 状态 / 创建时间），**默认画布**渲染图编排
2. task 型：入参 / 出参 Schema 只读表正确展示；chat 型：无 Schema 区（FR-003）
3. 画布上拖节点 / 拖入 / 连线 → 均无效；平移缩放可用；切 JSON → 美化只读文本与画布一致（FR-004/005）
4. 访问不存在 id（如 `/workflows/999999`）→ 错误提示 + 回列表
5. 点「编辑」→ 进入编辑页（FR-006）

### 7.3 场景 C：编辑保存（US2 / FR-002、FR-007~FR-011）

1. 编辑页 GET 回填：工具栏名称 / 描述、type 禁用单选（锁形提示「类型不可变，换型需删除重建」）、task 型「I/O Schema」抽屉行表单（`input_schema ?? []` 兜底）、图编排画布默认
2. 改一个节点 Prompt →「保存」→ 成功提示 + 回详情页 → 再查看是新值（SC-003 往返一致）
3. **不改直接保存** → 再查看配置不变（含未知 config 键、连线条件标签）
4. 改名与另一工作流重名 → 保存 → 409 提示、留编辑页、内容不丢
5. 画布删边造悬挂 / 环 → 保存 → 后端图规则 400（文案带定位）、留页
6. 有修改时点「返回」/ 切侧边栏菜单 → 确认框（离开 / 留下）；无修改直接离开不弹
7. 有修改时刷新 / 关闭标签 → 浏览器原生离开确认（beforeunload）
8. 已发布工作流编辑保存 → 成功且 status 仍「已发布」（编辑不降级）

### 7.4 场景 D：双模式一致性抽查（SC-002）

同一工作流（建议含 condition 连线与一个未知 config 键）在**创建第二步 / 详情只读 / 编辑回填**三处分别切 JSON ↔ 画布：文本与画布节点 / 边 / 条件标签一一对应。

### 7.5 Edge Cases 12 项走查（spec 010 Edge Cases 清单，编号接续 §5）

| # | 场景 | 步骤 | 预期 |
|---|---|---|---|
| EC-12 | 详情 / 编辑访问不存在 id | 直访 `/workflows/999999` 与 `/workflows/999999/edit` | 错误提示（拦截器统一弹）+ 回列表 |
| EC-13 | 编辑保存名称冲突 409 | §7.3 步骤 4 | 留编辑页，内容不丢 |
| EC-14 | 编辑保存图非法 400 | §7.3 步骤 5 | 后端文案带定位，留页 |
| EC-15 | 未知 config 键透传（编辑） | §7.3 步骤 3 + 检查器改一个已知键再保存 | 未知键原样保留 |
| EC-16 | Schema 非法历史 type | DB 直改一条 task 型工作流 `input_schema`（type 填越界值如 `"int"`）→ 编辑页打开 I/O Schema 抽屉 | 行内类型下拉显示原值字符串不炸；保存前 schemaFieldsError 拦截并标注行号 |
| EC-17 | 第二步直访 / 刷新 | §7.1 步骤 5 | 回第一步 |
| EC-18 | 已发布编辑不降级 | §7.3 步骤 8 | status 保持 published |
| EC-19 | 脏态守卫条件性 | §7.3 步骤 6（无修改不弹）+ 步骤 7（有修改拦）+ 步骤 2（保存成功跳转不弹） | 仅编辑内容有变化时拦 |
| EC-20 | 画布只读禁用集 | §7.2 步骤 3 | 拖入 / 删除 / 连线 / 检查器全禁，平移缩放可用 |
| EC-21 | 并发编辑整图覆盖 | 两个标签开同一工作流编辑页 → 各改一处 → 依次保存 | 后保存者整图覆盖（前端不做冲突检测，后端整图替换语义） |
| EC-22 | 编辑页加载失败 | 停后端（或断网）后进编辑页 | loading 态 → 错误提示 + 回列表 |
| EC-23 | 操作列不过载 | 列表页查看操作列 | 查看 / 编辑 / 发布或停用 / 删除四链接平铺，列宽 220 不换行 |

### 7.6 通过标准增补（SC-006 spec 010 项）

- [ ] 创建两步式全链路（§7.1，含 Schema 校验拦截与草稿回写）
- [ ] 详情只读双模式（§7.2）
- [ ] 编辑保存往返一致（§7.3 步骤 1~3）
- [ ] 脏态守卫双通道（§7.3 步骤 6~7）
- [ ] 双模式三处一致性抽查（§7.4）
- [ ] Edge Cases 12 项全过（§7.5）

## 8. spec 012 增补：拖拽编辑器八项增强（六场景 + 八项缺口逐项 + 载荷回读 + 既有 EC 全数复测）

交付面：纯前端 `web/src/views/workflow/`——`graph.ts` / `CanvasEditor.vue` / `NodeInspector.vue` / **`TemplateField.vue`（新增）** / `GraphModeEditor.vue` / `WorkflowEdit.vue` / `WorkflowOrchestrate.vue`；后端零改动，提交载荷形态不变（SC-003）。
行为依据：[specs/012-workflow-editor-enhancements/spec.md](../../specs/012-workflow-editor-enhancements/spec.md)（FR-001~FR-009、SC-001~SC-005）与 [quickstart.md](../../specs/012-workflow-editor-enhancements/quickstart.md) 场景 1~6（本节即其正式人工用例）。
前置同 §0，追加造数：

1. task 型工作流 T1，`input_schema` 带字段 `q`（type=string）
2. 三个 task 型子工作流候选：S1 `input_schema = [question, top_k]`、S2 `input_schema = [question, lang]`（同名保留验证）、S3 无 schema（提示验证）
3. 一个 chat 型工作流（§8.5 步骤 4 入参出参面板核对）
4. 一张上游含 workflow 节点并绑定 S1 的图（§8.2 步骤 5 子流程出参分组）

### 8.1 场景 A（US1）：可见删除入口 + 节点 Key 改名（缺口 ①③）

1. 拖入 3 节点 A→B→C 并连线 → 选中 B → 检查器点「删除节点」→ 预期：B 与其两条连线消失、A/C 间无悬挂边；切 JSON 图主体无 B（FR-001）
2. 选中起始节点 A →「删除节点」→ 预期：起始迁移到剩余**首个**节点（「起始节点」下拉与强调边框随动）
3. 选中连线 → 检查器连线表单 →「删除连线」→ 预期：连线消失
4. 选中 `llm_1` → Key 改为 `classify` → 预期：节点 / 连线端点 / 起始指向 / 位置随动；切 JSON 旧 key 零残留（含 `edges`）；切回拖拽位置不变（positions 不泄入配置）（FR-003）
5. 与现存 key 冲突（把另一节点改为 `classify`）、或清空 Key → 预期：前端拦截提示、图不变
6. Key 与原值相同 → 预期：无变化（幂等）

### 8.2 场景 B（US2）：变量引用下拉 + 手写混排（缺口 ⑦⑧）

1. T1 图（task 型、`input_schema` 含 q）上游已有节点 → LLM Prompt 点变量下拉 → 预期分组：入参（`{{input}}`、`{{input.q}}`）/ 上游节点（祖先 key）/ 子流程出参（上游 workflow 节点绑定后为 `{{key.field}}`）（FR-007）
2. 文本中间放光标、手写前后缀 → 下拉选 `{{input.q}}` → 预期：插入在**光标处**，前后文本不动
3. 纯手写 `{{任意}}` → 预期：不被清洗 / 校验拦截（手写零干预）
4. chat 型图（无 input_schema）→ 预期：下拉只有 `{{input}}` 与上游引用，无入参展开组
5. 上游 workflow 节点绑定子流程后重开下拉 → 预期：出参分组出现（首次拉取短暂 loading，再次打开缓存命中）
6. 全部模板字段逐一打开下拉核对行为一致（FR-008）：LLM System Prompt / Prompt、end Output、condition Expression、API URL / Body / Header 值、子流程入参值

### 8.3 场景 C（US3）：LLM 双输入框 + API 全要素（缺口 ④⑤）

1. LLM 节点填 System Prompt「你是分类器」+ Prompt「分类：{{input.q}}」→ 切 JSON → 预期 `config` 含 `system_prompt` / `prompt` 两键（FR-004）
2. 清空 System Prompt → 切 JSON → 预期：**不携带** `system_prompt` 键（空 = 缺省，对齐后端 omitempty）
3. API 节点 Method 切 POST → Body 域出现；填 Body 模板 + 加 Header 行 `Content-Type=application/json` + Auth 选 Bearer 填 token → 切 JSON → 预期：`config.headers` 含两行（Authorization 值为 `Bearer <token>`）、`config.body` 为模板文本（FR-005）
4. Auth 切 Basic 填账密 → 预期：headers 仅一条新 Authorization 行（`Basic base64(...)`），旧 Bearer 行无残留；再切「无」→ Authorization 行消失
5. 保存后详情 / 重新编辑回读 → 预期：Auth 预设正确推导为 Basic、Header 行回显（SC-003 回读项见 §8.6）

### 8.4 场景 D（US4）：子工作流入参自动渲染（缺口 ⑥）

1. 父图 workflow 节点选 S1（`[question, top_k]`）→ 预期：渲染两行入参，字段名只读、值域为模板（可下拉填变量）（FR-006）
2. question 填 `{{input.q}}` → 切换到 S2（`[question, lang]`）→ 预期：question 行保留已填值，top_k 行消失、lang 行新建（同名保留）
3. 切到 S3（无 schema）→ 预期：显示提示而非空表单
4. 编辑工作流 W 自身 → 预期：子工作流下拉**不含 W**；创建态（未落库）→ 不排除（无自身可引用）

### 8.5 场景 E（US5）：起始节点下拉与画布内 Schema 配置（缺口 ②）

1. task 型画布：左面板「起始节点」下拉列出全部节点 key → 选择另一节点 → 预期：主色强调边框迁移到该节点、切 JSON `start_node_key` 随动（entry 由下拉直接指定，画布无伪「开始」节点）
2. 面板「入参 / 出参」按钮 → 检查器显示入参 / 出参两段行表单（FR-002）
3. 面板加字段 q → 切两步式第一步（或编辑页 I/O Schema 抽屉）→ 预期：表单已同步出现 q；反向在第一步删 q → 面板同步消失（schema 单源）
4. chat 型画布 → 点「入参 / 出参」→ 预期：显示单一 `{{input}}` 说明，无编辑控件
5. readonly 详情态 → 预期：左面板与检查器不渲染、仅起始节点强调边框可见（SC-004）

### 8.6 载荷回读核对（SC-003：Network 面板看提交体 / 保存后详情回读逐键）

- [ ] 图主体 = `{start_node_key, nodes[], edges[]}`，**无伪节点痕迹**（无 `__start__` / `type: "start"` 节点；`start_node_key` 指向真实节点 key）
- [ ] `config` 键集不越界：llm = `model_id` / `prompt` / `system_prompt`（空则缺省）；api = `method` / `url` / `headers` / `body`（JSON 手写的 `timeout_sec` / `ssl_verify` 等未知键透传保留）；workflow = `workflow_id` / `inputs`；end = `output`；condition = `expression`——**无独立 auth 字段**（Auth 预设落 `headers.Authorization`）
- [ ] task 型携带 `input_schema` / `output_schema`（与面板 / 第一步表单一致）；chat 型二者均不携带
- [ ] POST 带 `type`、PUT 不带 `type`（既有双轨隔离不变）
- [ ] 未知 config 键（手写 `timeout_sec`）拖拽 ↔ JSON 往返 + 保存回读**不丢**

### 8.7 场景 F（US6）：存量回归（EC-1~23 全数复测引用）

1. 按 §5 EC-1~11、§7 EC-12~23 **逐项复测**（编号、步骤、预期以原表为准，此处不复制）：双模式往返一致、未知 config 键透传、脏态守卫双通道、两步式创建、编辑保存链路、只读禁用集（SC-002）
2. 重点往返：含未知 config 键（如手写 `timeout_sec`）的工作流拖拽 ↔ JSON 切换 → 键不丢
3. readonly 详情走一遍**全部新增交互入口**——删除节点 / 删除连线 / 改名与 Key 编辑 / 入参出参面板 / 起始节点下拉 / Auth 与 Headers 行 / 子流程下拉与入参行 / 变量下拉 → 零可点、零可改（SC-004）

### 8.8 八项缺口对号（用户 2026-09-22 反馈 ↔ FR ↔ 用例）

| 缺口 | FR | 用例 |
|---|---|---|
| ① 节点删除只有 Backspace | FR-001 | §8.1 步骤 1~3 |
| ② 需 startnode/endnode 配置入参出参 | FR-002 | §8.5 全节 |
| ③ 节点 key 拖拽界面无法配置 | FR-003 | §8.1 步骤 4~6 |
| ④ LLM 需 system_prompt / user_prompt 两输入 | FR-004 | §8.3 步骤 1~2 |
| ⑤ API 节点无 header / auth 配置表单 | FR-005 | §8.3 步骤 3~5 |
| ⑥ 子工作流选完应自动渲染 input 表单 | FR-006 | §8.4 全节 |
| ⑦ 引用其他 node 字段需手写 `{{var}}` 难用 | FR-007 | §8.2 步骤 1~5 |
| ⑧ 需下拉选字段引用 + 可直接手写表单 | FR-007/008 | §8.2 步骤 2~3、6 |

### 8.9 通过标准增补（SC-001~SC-005）

- [ ] 八项缺口均可在画布 / 检查器完成（SC-001）（§8.1~§8.5）
- [ ] 既有零回归：EC-1~23 全数复测通过（SC-002）（§8.7）
- [ ] 载荷回读逐键核对通过（SC-003）（§8.6）
- [ ] readonly 零新增交互（SC-004）（§8.5 步骤 4、§8.7 步骤 3）
- [ ] 双门禁全绿（SC-005）：`cd web && npm run type-check && npm run build`

## 9. spec 013 增补：试运行对话框与双入口（详情 / 编辑 + 三态入参 + 载荷逐键 + 状态机例外）

交付面：纯前端 `web/src/views/workflow/`——**`WorkflowTrialDialog.vue`（新增）** / `WorkflowDetail.vue` / `WorkflowEdit.vue`，`web/src/api/workflow.ts` 增 `executeWorkflow`（`?trial=true`、timeout 300s 覆盖 axios 默认 30s）；Agent 绑定下拉走 agent 域，见 [agent-manual-test.md](agent-manual-test.md) §13。后端零改动（execute 与 workflow_id 契约 spec 05/06 已冻结交付）。
行为依据：[specs/013-agent-bind-trial-run/spec.md](../../specs/013-agent-bind-trial-run/spec.md)（FR-001~FR-008、SC-001~SC-005）与 [quickstart.md](../../specs/013-agent-bind-trial-run/quickstart.md) 场景 2 / 3 / 4。前置同 §0，追加造数：

1. 一个 chat 型工作流（A 态）
2. 一个 task 型工作流带 input_schema（B 态；建议混排 string / number / boolean 与 description，核对分型控件与行内提示）
3. 一个无 Schema 的 task 型工作流（C 态直传）
4. 一个含条件分支的 task 型工作流（§9.3 步骤 3 分支差异；§4.4 condition 连线即可）

### 9.1 场景 A（US2）：详情页入口与三态入参（FR-004）

1. 列表行「查看」进详情 → PageHeader 操作区「编辑」左侧出现「试运行」按钮 → 点击 → 对话框开、标题「试运行：<名称>」
2. chat 型：单一文本框（placeholder「模拟用户消息」，右下角字数计数，上限 16384）
3. task 型有 input_schema：按字段行渲染——string→文本框 / number→数字框 / boolean→开关；label=字段名、必填带 `*`、description 行内灰字提示
4. task 型无 schema：单一文本框（placeholder「输入文本（无入参 Schema，原样传入）」）
5. 填参执行一次 → 关闭再开 → 输入与结果**全部重置**（每次打开 = 全新试运行）

### 9.2 场景 B（US2/US3）：执行、loading 防重复与校验拦截（FR-005）

1. 填参点「执行」→ 按钮 loading；执行期间连点 → Network **只有一条 POST**（running 短路）；「关闭」按钮同时禁用
2. A / C 态空输入（或纯空白）点执行 → 提示「请输入消息内容」，不发请求
3. B 态必填字段缺失 → 表单校验滚动定位到首个缺失行，不发请求
4. A / C 态文本框 maxlength 截断（计数到顶即止）；B 态字段值组装 JSON 合计超 16384 → 提示「输入超出 16384 字符上限」，不发请求（后端 binding 同值双拦的第二道）

### 9.3 场景 C（US2/US3）：成功结果、轨迹与分支差异（FR-006）

1. 成功执行 → 结果区「输出」多行文本（保留换行、限高滚动）+「执行轨迹」表五列（节点 / 类型 / 状态 / 耗时 / 错误，执行序）+ 标题行「总耗时 N ms」
2. 不关对话框改参再执行 → 结果区整体刷新为新一次输出与轨迹（旧结果不留存）
3. 条件分支工作流两次执行（入参分别命中不同分支）→ 两次轨迹表节点序列**可见差异**、输出不同（US3 独立判据）
4. 轨迹表状态列 tag：succeeded 绿 / 失败红；错误列空值显 `—`

### 9.4 场景 D（US3）：失败两面可读文案（FR-007）

1. **运行失败面**（HTTP 200 + `status:"failed"`）：构造必失败工作流（如 api 节点指向不可达地址、llm 节点模型已被删）→ 执行 → 结果区红色 alert 文案 = 轨迹**执行序最后一条非空 error_msg**（无则「执行失败」）——可读、无裸异常堆栈；轨迹表照常展示（中断点可见、失败行错误列红字）
2. **请求失败面**（信封 reject）：停后端或断网后点「执行」→ 拦截器 toast（瞬时）+ 结果区**持久**红色 alert 展示信封 message——两者并存不遮蔽

### 9.5 载荷 Network 逐键核对（SC-003）

- [ ] `POST /api/v1/workflows/{id}/execute?trial=true`——query 固定 `trial=true`
- [ ] 请求体**仅一个** `input` 键：A / C 态 = 纯文本串；B 态 = JSON 对象文本（如 `{"q":"..."}`，未填的 number 键自然省略）
- [ ] Agent 绑定载荷：创建 / 编辑 `workflow_id` 为数值、解绑缺省该键（详见 [agent-manual-test.md](agent-manual-test.md) §13 第 2 条）

### 9.6 状态机例外：draft / disabled 放行（spec 013 Edge Cases）

1. draft 态工作流（未发布）从详情页「试运行」执行成功——trial 放开状态机限制
2. disabled 态工作流（列表页「停用」后进详情）同样可试运行 → 执行**不被** WORKFLOW_NOT_PUBLISHED 拒绝——trial 是状态机唯一例外，两态都要人工过

### 9.7 场景 E（US2 场景 2/3/5）：编辑页脏态阻断与两步式无入口（FR-002/FR-003）

1. 编辑页干净态（无修改）→ 工具栏「保存」左侧「试运行」→ **直接开对话框**（无确认框）
2. 画布任意改动（拖节点 / 改名，制造脏态）→ 点「试运行」→ 确认框「当前有未保存改动，试运行执行的是已保存版本，请先保存。」：
   - 「取消」→ 留编辑页，对话框不开
   - 「去保存」→ 走既有保存链路：成功跳详情页（可再从详情页试运行）；失败（如重名 409）留页——两分支均不开试运行对话框
3. 「去保存」成功后从详情页开试运行 → 执行的是**新保存版本**（两入口 detail 均来自 GET，「跑已落库版本」由数据源结构性保证）
4. 两步式创建第二步（`/workflows/create/orchestrate`）→ 工具栏**无**「试运行」按钮（FR-003 MUST NOT：未落库无 id）

### 9.8 存量回归引用（SC-004，quickstart 场景 4）

1. 本文档 §5 EC-1~11、§7 EC-12~23、§8.7 按 §8 原步骤复测（编号、预期以原表为准）——试运行为纯增量入口，双模式编辑器 / 脏态守卫 / 两步式创建行为零变化
2. Agent 表单既有字段回归见 [agent-manual-test.md](agent-manual-test.md) §13 第 5 条

### 9.9 通过标准增补（SC-002~SC-005，对应 quickstart 场景 2 / 3 预期表）

- [ ] 三态入参渲染正确 + 重开重置（SC-002）（§9.1）
- [ ] loading 防重复 + 校验拦截不发请求（SC-002）（§9.2）
- [ ] 输出 + 轨迹 + 分支差异（SC-002）（§9.3）
- [ ] 失败两面文案可读、无裸异常（SC-002）（§9.4）
- [ ] 载荷逐键核对（SC-003）（§9.5）
- [ ] draft / disabled 试运行放行（Edge Cases）（§9.6）
- [ ] 编辑页脏态阻断（去保存 / 取消）+ 两步式无入口（FR-002/FR-003）（§9.7）
- [ ] 存量回归引用复测（SC-004）（§9.8）
- [ ] 双门禁全绿（SC-005）：`cd web && npm run type-check && npm run build`
