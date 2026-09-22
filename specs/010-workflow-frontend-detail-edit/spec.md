# Feature Specification: 工作流详情 / 编辑前端（查看 + 更新 + 创建流程重构 + Schema 表单化）

**Feature Branch**: `010-workflow-frontend-detail-edit`

**Created**: 2026-09-22

**Status**: Draft

**Input**: User description: 工作流管理前端五项交互缺口——① 无查看已有工作流入口；② 创建选拖拽应点「创建」后进整页编排；③ 需支持更新与查看详细编排（更新同样双模式）；④ 已有配置按需渲染成 JSON 与画布；⑤ 入参/出参 Schema 应表单化。用户已拍板：详情与编辑分离、独立路由整页、Schema 纯表单、走 spec 010 完整流程。

**前置依赖**: spec 009 工作流管理前端（commit c37a89b——列表页、创建页、画布 / 检查器 / 图配置模型、api 层可复用）；后端 workflow API spec 01~08 已冻结交付 `GET /workflows/:id`（detail 含图组装）与 `PUT /workflows/:id`（整图替换、type 不可变携带即拒、编辑不降级 status）。本篇**纯前端，后端零改动**。

## Clarifications

### Session 2026-09-22

- Q: 创建第二步的初始图配置？ → A: 保留预填示例（用户选 A）——第二步初始 = 智能客服分类示例图，拖拽画布与 JSON 模式一致渲染；不需要时清空重编。
- 默认决策（clarify 扫描补全，无产品分歧）：
  - 脏态守卫覆盖两条通道：路由跳转（守卫）与浏览器刷新 / 关闭（beforeunload）均拦。
  - 详情页展示 task 型入参 / 出参 Schema（只读表）；chat 型不显示。
  - 详情页默认渲染模式 = 画布，可切 JSON。
  - 并发编辑同一工作流：后保存者整图覆盖（后端整图替换语义，无冲突检测，不做提示）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 查看工作流详情（Priority: P1）

管理员在列表页点「查看」→ 进入只读详情页：基础信息（名称 / 描述 / 类型 / 状态 / 创建时间）+ 图编排双模式浏览（JSON 文本 / 拖拽画布渲染，可切换）。画布只读——可平移缩放、可见节点类型着色与起始标识，但不可拖入 / 删除 / 连线 / 编辑配置。

**Why this priority**: 用户验收反馈的第一缺口（「没有查看已有工作流的流程交互」）；查看是高频低风险操作，独立交付即有完整价值。

**Independent Test**: 列表页任意行点「查看」→ 详情页正确渲染该工作流的基础信息与图编排（两种模式切换内容一致）。

**Acceptance Scenarios**:

1. **Given** 列表页存在工作流， **When** 点「查看」， **Then** 进入 `/workflows/:id`，基础信息齐全、图编排默认渲染（画布或 JSON），模式可切换且内容一致。
2. **Given** 详情页画布模式， **When** 尝试拖动节点 / 拖入新节点 / 连线 / 点节点编辑， **Then** 均不生效（只读态），平移缩放可用。
3. **Given** 访问不存在的工作流 id， **When** 直接打开 `/workflows/:id`， **Then** 错误提示并回列表。
4. **Given** 详情页， **When** 点「编辑」按钮， **Then** 进入该工作流编辑页。

---

### User Story 2 - 编辑工作流（Priority: P2）

管理员在列表页（或详情页）点「编辑」→ 进入整页编辑器：服务端配置回填（基础信息 + 图编排，双模式按需渲染）→ 修改（名称 / 描述 / Schema / 图编排）→「保存」PUT 提交 → 成功提示并回详情页。type 不可变（展示但禁改——后端携带即拒）。未保存离开有确认提示（脏态守卫）。

**Why this priority**: 更新是继查看后的核心缺口；后端 PUT 已冻结，纯前端接入。

**Independent Test**: 列表页点「编辑」→ 画布上改一个节点的 Prompt → 保存 → 回详情页看到新值。

**Acceptance Scenarios**:

1. **Given** 已存在的工作流， **When** 进入编辑页， **Then** 服务端配置完整回填：基础信息、图编排（画布 / JSON 双模式）、task 型 Schema 表单。
2. **Given** 编辑页， **When** 修改图编排并保存成功， **Then** 提示成功、回详情页、配置已更新（再查看是新值）。
3. **Given** 编辑页， **When** 保存时名称与他人冲突（409）， **Then** 错误提示、留在编辑页、编辑内容不丢。
4. **Given** 编辑页， **When** type 单选， **Then** 禁用态（不可切换；换型 = 删了重建）。
5. **Given** 编辑页有未保存修改， **When** 点「返回」/ 路由跳转离开， **Then** 弹确认（放弃 / 继续编辑）；无修改时直接离开。
6. **Given** 已发布（published）的工作流， **When** 编辑保存， **Then** 允许且成功（编辑不降级 status）。

---

### User Story 3 - Schema 表单化（Priority: P3）

task 型工作流的入参 / 出参 Schema 从 JSON 文本域改为结构化表单：每行 = 字段名 + 类型下拉（string / number / boolean）+ 必填开关 + 描述，可增删行；前端校验对齐后端规则（name 非空不重名、type 限三值），非法即拦不出请求。创建（第一步表单）与编辑页共用同一组件。

**Why this priority**: 降低 task 型配置门槛；独立组件、创建 / 编辑两处复用，可先行交付。

**Independent Test**: 创建 task 型工作流 → 入参 Schema 表单加两行（含一个故意的重名）→ 提交被前端拦截 → 改正后通过。

**Acceptance Scenarios**:

1. **Given** 创建第一步（或编辑页）类型 = task， **When** 编辑 Schema， **Then** 表单行增删流畅、每行四字段可编辑。
2. **Given** Schema 表单存在重名字段 / 空名 / 非法类型， **When** 提交， **Then** 前端拦截并提示具体行与原因，不发请求。
3. **Given** 编辑已有 task 型工作流， **When** 进入编辑页， **Then** 服务端 Schema 回填为表单行。
4. **Given** 类型 = chat， **When** 查看表单， **Then** Schema 编辑区不显示（chat 型不带 schema）。

---

### User Story 4 - 创建流程重构为两步式（Priority: P4）

创建工作流拆两步：第一步基础表单（名称 / 描述 / 类型 / task 型 Schema 表单）→ 点「创建工作流」进第二步整页编排（JSON / 拖拽模式切换，独立路由页）→「保存并创建」一次 POST 提交，成功跳回列表；「上一步」回表单且内容保留。替代 009 的「表单页内嵌画布」形态。

**Why this priority**: 体验改进（画布整页而非表单内嵌）；依赖 US3 的 Schema 表单与 US2 的整页编排形态，最后交付。

**Independent Test**: 创建 → 第一步填基础信息 → 下一步 → 整页画布拖两个节点连线 → 保存并创建 → 列表出现新行、点查看配置正确。

**Acceptance Scenarios**:

1. **Given** 创建第一步表单填写完成， **When** 点「创建工作流」（下一步）， **Then** 校验通过后进入第二步整页编排页，初始渲染预填智能客服分类示例图（画布与 JSON 两模式一致）。
2. **Given** 第二步整页编排页， **When** 切换 JSON / 拖拽模式， **Then** 内容等价转换（延续 009 双模式语义：非法 JSON 阻断切换、未知键透传）。
3. **Given** 第二步， **When** 点「上一步」， **Then** 回第一步且已填内容（含 Schema 表单行）保留。
4. **Given** 第二步编排完成， **When** 点「保存并创建」， **Then** 一次 POST 提交；成功提示 + 跳回列表；失败留第二步内容不丢。
5. **Given** 第二步直接刷新或直访（无第一步数据）， **When** 页面加载， **Then** 回到第一步（中间态不持久化）。

---

### Edge Cases

- 详情 / 编辑页访问不存在或已删除的工作流 id → 错误提示 + 回列表（404 哨兵）。
- 编辑保存名称冲突（409 WORKFLOW_NAME_CONFLICT）→ 留页、内容不丢（009 既有语义延续）。
- 编辑保存图非法（后端图规则拒：环 / 不可达 / 悬挂边等）→ 错误提示带后端文案定位。
- 编辑页回填的 config 含画布表单未覆盖的未知键 → 透传保留（009 SC-006 语义延续：改已知键不丢未知键）。
- task 型编辑页 Schema 回填含非法历史数据（老数据 type 越界）→ 表单行显示原值并标注非法、保存前要求修正。
- 创建第二步直访 / 刷新 → 回第一步。
- 已发布工作流编辑保存 → 允许（编辑不降级）；生效即时（后端缓存写时删）。
- 脏态守卫仅拦编辑内容有变化时；保存成功或无改动直接离开。
- 画布只读态：双击设起始 / 拖入 / 删除 / 连线 / 检查器编辑全部无效。
- 并发编辑同一工作流：后保存者整图覆盖前者修改（后端整图替换语义，前端不做冲突检测）。
- 编辑页加载中（GET 未返回）→ loading 态；加载失败 → 错误提示 + 回列表。
- 列表页「查看」「编辑」入口与既有「删除 / 发布 / 停用」并存，操作列不过载（必要时收纳）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 列表页操作列 MUST 新增「查看」入口，跳转 `/workflows/:id` 详情页。
- **FR-002**: 列表页操作列 MUST 新增「编辑」入口，跳转 `/workflows/:id/edit` 编辑页。
- **FR-003**: 详情页 MUST 展示完整基础信息（名称 / 描述 / 类型 / 状态 / 创建时间）、task 型入参 / 出参 Schema（只读表，chat 型不显示）与图编排。
- **FR-004**: 详情页图编排 MUST 支持只读双模式渲染（默认画布，可切 JSON 美化文本），两模式内容一致。
- **FR-005**: 详情页画布 MUST 只读：平移缩放可用；拖入 / 删除 / 连线 / 双击设起始 / 检查器编辑全部禁用。
- **FR-006**: 详情页 MUST 提供「编辑」按钮进入编辑页。
- **FR-007**: 编辑页 MUST 为独立路由整页布局（顶部信息与操作栏 + 图编排编辑区铺满）。
- **FR-008**: 编辑页 MUST 服务端回填：基础信息 + 图编排双模式 + task 型 Schema 表单（chat 型无 Schema 区）。
- **FR-009**: 编辑页保存 MUST 走整图替换更新；成功提示并回详情页；失败留在编辑页、内容不丢。
- **FR-010**: 编辑页 type MUST 不可变：单选禁用态（后端携带即拒，前端不得发 type）。
- **FR-011**: 编辑页 MUST 有脏态守卫：有未保存修改时离开需确认（路由跳转与浏览器刷新 / 关闭均拦），无修改直接离开。
- **FR-012**: Schema 编辑 MUST 用四字段行表单（名称 / 类型下拉 string-number-boolean / 必填开关 / 描述）+ 增删行；前端校验对齐后端（name 非空不重名、type 限三值），非法拦截不发请求。
- **FR-013**: 创建流程 MUST 两步式：第一步基础表单（含 Schema 表单）→ 第二步整页编排（独立路由、JSON / 拖拽切换，初始 = 预填智能客服分类示例图、双模式一致渲染）→「保存并创建」一次提交。
- **FR-014**: 第二步 MUST 支持「上一步」返回第一步且已填内容保留。
- **FR-015**: 创建第二步与编辑页 MUST 共用同一整页编排形态；复用 009 画布 / 检查器 / 图配置模型（改造而非重写）。
- **FR-016**: 双模式语义 MUST 延续 009：模式切换等价转换、非法 JSON 阻断切换、未知 config 键往返透传、外键字符串保形。
- **FR-017**: 文档同步：手测文档增补本篇场景节 + web/README.md 目录结构更新。

### Key Entities *(include if feature involves data)*

- **WorkflowDetail**: 单个工作流完整视图——基础信息（id / name / description / type / status / created_at）+ 图编排（start_node_key / nodes[] / edges[]）+ task 型 input_schema / output_schema。来源为服务端详情接口；本篇前端消费（009 已定义列表摘要视图）。
- **SchemaField**: task 型 I/O 字段——name（非空不重名）/ type（string / number / boolean）/ required / description。表单行与该结构一一对应。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 自动化门全绿——`cd web && npm run type-check && npm run build` 通过，`go build ./... && go vet ./...` 回归通过，git 变更不触 `internal/` 与 `migrations/`。
- **SC-002**: 同一工作流配置在三种场景（创建第二步、详情只读、编辑回填）下双模式渲染一致。
- **SC-003**: 编辑往返一致——「回填 → 不改直接保存 → 再查看」配置不变（含未知 config 键、连线条件标签、Schema 表单行）。
- **SC-004**: Schema 表单前端拦截率 100%——凡后端 Schema 规则可判的非法输入（空名 / 重名 / 非法类型）均不出请求。
- **SC-005**: 创建两步式全链路 3 分钟内可完成（填表 → 编排 → 提交成功跳列表）。
- **SC-006**: 人工冒烟——docs/testing/workflow-frontend-manual-test.md 增补节全场景走查通过（详情双模式、编辑回填保存、409 / 图非法留页、type 禁改、脏态守卫、两步式、Schema 表单、Edge Cases）。

## Assumptions

- 编辑脏态离开确认、创建第二步刷新回第一步为合理默认交互（行业惯例 + 中间态不持久化），未逐一问询。
- 创建第二步与编辑页的第一步 / 基础信息跨路由传递方式（组件态 / store / 会话存储）属 plan 层实现细节，spec 不锁定。
- 后端 GET / PUT / Schema 校验契约已冻结（internal/workflow/api/schema.go 只读核实），本篇零后端改动、零新增第三方依赖（@vue-flow 已在 009 引入）。
- 列表操作列按钮增至 6 个（查看 / 编辑 / 删除 / 发布或停用）——收纳与否（下拉收纳 vs 平铺）由 plan 视觉层定，spec 不锁定。
- 延续 009 视觉纪律：只引 `--hf-*` 语义 token、空列表 `[]`、空串 `""`。
