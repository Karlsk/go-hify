# Hify 前端（web/）

> 本文件是 Hify 前端骨架的事实源，被根 [CLAUDE.md](../CLAUDE.md) 引用。
> 后端规范以 CLAUDE.md 为准；前端目录结构、约定、开发命令记录于此，与 CLAUDE.md 的《接口规范》《SSE 流式》《部署架构》保持一致。

## 技术栈

| 依赖 | 版本 | 用途 |
|---|---|---|
| Vue | ^3.5 | UI 框架（`<script setup>` + Composition API） |
| Vite | ^8.2 | 构建 / 开发服务器 |
| TypeScript | ^5.9 | 类型安全 |
| Element Plus | ^2.14 | 组件库（**全量引入**） |
| @element-plus/icons-vue | ^2.3 | 图标（main.ts 全量注册） |
| Vue Router | ^5.2 | 路由 |
| Pinia | ^4.0 | 状态管理 |
| axios | ^1.19 | REST 请求（**SSE 不走 axios**） |
| @vue-flow/core | ^1.48 | 工作流拖拽画布（仅 spec 009 编排页使用） |
| @vue-flow/background | ^1.3 | 画布网格背景 |
| @vue-flow/controls | ^1.1 | 画布缩放 / 居中控件 |

> Element Plus 选全量引入而非 unplugin 按需：内部工具不抠 bundle 体积，省掉 auto-imports.d.ts / components.d.ts 的配置与噪音，一人维护下最省心。前端构建产物由 nginx 长缓存托管，体积不进运行时关键路径。

> Vue Flow 只服务工作流拖拽编排（spec 009 双模式之一）：选久经考验的现成画布而非手写（宪法「现成方案优先」）；样式在 `main.ts` 全局引入（core style/theme-default + controls）。

## 目录结构

```
web/
├── index.html
├── package.json
├── vite.config.ts          # @ 别名 + /api → http://localhost:8080 代理
├── tsconfig.json           # 应用 TS 配置（含 @/* paths）
├── tsconfig.node.json      # vite.config.ts 的 Node 配置
├── env.d.ts                # vite/client 类型引用（import.meta.env）
└── src/
    ├── main.ts             # 挂载 Pinia + Router + ElementPlus + Icons
    ├── App.vue             # 根布局：深色侧边栏 + 顶栏（面包屑/用户区）+ <router-view />
    ├── assets/
    │   └── styles/
    │       ├── tokens.css             # 设计系统 --hf-* token（唯一事实源）
    │       ├── theme-element-plus.css # EP --el-* 变量映射（只映射不新增）
    │       └── main.css               # @import 上两者 + reset + 组件级覆写（渐变主按钮等）
    ├── router/index.ts     # 路由表（meta.title = 面包屑/document.title 来源）
    ├── components/
    │   ├── PageHeader.vue             # 页面标题区：标题 + 描述 + 右侧 actions 插槽
    │   ├── HifyTable.vue              # 通用列表表格（泛型，偏移分页，refresh()）
    │   ├── HifyFormDialog.vue         # 通用表单弹窗（泛型，open(data?) 编辑/新增）
    │   ├── AuthShell.vue              # 登录/注册分栏壳（左品牌深底面板 + 右表单卡片）
    │   └── ProviderModelsDrawer.vue   # 提供商模型列表抽屉（子资源形态：同步/勾选启用/手动增删）
    ├── composables/
    │   ├── useRequest.ts   # 请求三态 { data, loading, error, execute }
    │   ├── useConfirm.ts   # 删除确认全流程：调用即执行，返回 Promise<boolean>
    │   └── useBreakpoint.ts # 响应式断点单例：BREAKPOINTS 常量 + isNarrow(≤1200)/isCompact(≤992)
    ├── stores/
    │   ├── auth.ts          # 会话 store：登录用户唯一事实源（fetchMe 引导 / 401 清理 / 注销）
    │   └── workflowCreateDraft.ts # 两步式创建草稿（内存态）：第一步表单 ↔ 第二步图编排互访保留
    ├── utils/
    │   ├── request.ts      # axios 实例 + Result 信封拆包 + 错误统一处理 + getList
    │   ├── notify.ts       # notifySuccess/Error/Warning（duration 统一 3s）
    │   └── sse.ts          # fetch + ReadableStream（SSE 流式，不用 EventSource）
    ├── types/
    │   └── index.ts        # Result 信封 / PageQuery / PageResult 等公共类型
    ├── api/
    │   ├── auth.ts          # Auth 模块 API 层：登录/注册/注销/me（cookie 会话，token 不经前端）
    │   ├── provider.ts      # Provider 模块 API 层：类型 + 请求方法（对齐后端 api 契约，唯一事实源）
    │   └── workflow.ts      # Workflow 模块 API 层：类型 + 列表/创建/删除/发布/停用/详情/更新（PUT 不带 type）
    └── views/
        ├── auth/
        │   ├── LoginView.vue     # 登录页（bare：无 chrome 分栏布局，回跳 redirect）
        │   └── RegisterView.vue  # 注册页（注册成功即自动登录进入）
        ├── provider/ProviderList.vue
        ├── agent/AgentList.vue
        ├── chat/ChatView.vue
        ├── workflow/
        │   ├── WorkflowList.vue       # 工作流列表：三态 tag / 查看编辑入口 / 删除 / 发布停用（HifyTable 偏移分页）
        │   ├── WorkflowCreate.vue     # 创建第一步：纯表单（名称/描述/类型 + task 型 Schema 行表单）
        │   ├── WorkflowOrchestrate.vue # 创建第二步：整页编排（fullBleed，草稿 store 传递，保存并创建一次 POST）
        │   ├── WorkflowDetail.vue     # 详情页：基础信息 + task 型 Schema 只读表 + 图编排只读双模式
        │   ├── WorkflowEdit.vue       # 编辑页：整页回填 + PUT 保存（type 禁改 / 脏态守卫双通道）
        │   ├── SchemaFieldsEditor.vue # Schema 字段行编辑器：四控件行 + 增删行（v-model SchemaField[]）
        │   ├── GraphModeEditor.vue    # 双模式图编排容器：JSON ↔ 拖拽切换（readonly/fill 透传两子组件）
        │   ├── graph.ts               # 图配置纯逻辑（无 Vue 依赖）：类型 / 解析序列化 / 画布双向转换 / 提交组装 / 详情转换与 Schema 校验
        │   ├── JsonConfigEditor.vue   # JSON 编辑器：textarea + 格式化 + 校验态（readonly 供详情/编辑只读文本）
        │   ├── CanvasEditor.vue       # 拖拽画布：Vue Flow + 左侧五类节点面板（DnD / 连线 / 删除 / 起始标识；readonly 态 + fill 整页形态）
        │   └── NodeInspector.vue      # 右侧检查器：按节点类型分化表单 + 连线 condition 标签编辑
        └── design/
            ├── DesignTokens.vue      # 设计 token 预览页（/design，不进菜单）
            └── ComponentsDemo.vue    # 公共组件演示区（mock 数据，/design 末节）
```

## 设计系统

视觉规范的唯一事实源：[docs/design/design-system.md](../docs/design/design-system.md)（色值表、EP 映射策略、整体布局、设计原则）。风格定位：**浅底 + 科技感点缀**——浅色主体保证表格可读性，深色侧边栏 + 亮色交互元素制造品牌感；主色 Linear 紫 `#5E6AD2`，辅色青色 `#06B6D4`（点缀/数据高亮，不兼语义状态）。整体布局：白顶栏（面包屑 + 用户区）/ 浅灰页面底 / 白卡片三层层次；间距纪律 24（页面边距）/ 20（卡片内边距）/ 16（元素间距）；按钮三式——主要=品牌渐变、次要=白底边框、危险=红。

文件与规则：

- `assets/styles/tokens.css` —— `--hf-*` 语义 token（色彩色阶 / 背景 / 文字 / 边框 / 侧边栏 / 圆角 / 阴影 / 动效 / 字体 / 间距 / z-index）。**改视觉值只动这一个文件。**
- `assets/styles/theme-element-plus.css` —— 把 `--el-*` 映射到 `--hf-*`（含 EP light-N 派生色），EP 组件自动继承主题；本文件不新增视觉决策。
- 业务代码**只引用语义 token**（`var(--hf-bg-page)`），禁止硬编码色值 / 圆角 / 阴影；EP 组件默认即主题化，无需逐个覆盖。
- token 按语义命名（非 `--hf-white` 式字面命名），为将来深色主题留扩展位：`html[data-theme="dark"]` 下覆盖 `--hf-*` 即可。
- 验收：`npm run dev` 后访问 `/design` 预览页（含末节《公共组件》演示：HifyTable 分页 / 空态、HifyFormDialog 新增编辑、useConfirm 删除确认，mock 数据全链路可点）。

## 约定（与 CLAUDE.md 对齐）

### 请求与响应信封

- API 统一前缀 `/api/v1`（`request.ts` 的 `baseURL`）。
- 所有响应走后端 `respond.Result` 信封：`{ success, data, error, meta }`。
- `request.ts` 响应拦截器统一拆信封：`success === false` → 取 `error.code` / `error.message` → `ElMessage.error` 提示 + `reject(new RequestError(code, message))`，调用方按 `error.code` 分支。
- `success === true` 自动解包：导出 `get` / `post` / `put` / `del` 四个泛型 helper（`get<T>(url)` 直接返回 `Promise<T>`，即业务 `data`）；解包后 `meta` 不可见，需分页 `meta` 的列表端点用 `getList<T>(url, params)` 取 `PageResult<T>`（`{ list, total, page, pageSize }`，HifyTable 的数据源）。
- 请求失败的 `ElMessage.error` 统一在拦截器弹，业务层 / composables **不重复弹**；`notify.ts` 只管成功 / 警告 / 业务显式错误。
- HTTP 主信号（2xx/4xx/5xx）由 axios 错误分支兜底；业务错误码（`PROVIDER_NOT_FOUND` / `BUDGET_EXHAUSTED` / …）见 CLAUDE.md《错误处理》表。

### 认证与会话

- 会话走 HttpOnly cookie（`hify_session`，7 天）：axios 已配 `withCredentials`，token 不经前端代码；登录 / 注册 / 注销 / me 走 `api/auth.ts`。
- 身份唯一事实源 = `stores/auth.ts`（Pinia）：路由守卫首次导航调 `ensureReady()`（`fetchMe`，401 静默归 null——未登录是正常态不是错误）。
- 路由守卫（`router/index.ts`）：`meta.bare` = 登录 / 注册（无 chrome + 免登录，已登录访问直接进系统）；`meta.public` = 免登录保留 chrome（现仅 `/design`）；其余路由未登录 → `/login?redirect=<来源页>`（回跳仅接受 `/` 开头站内路径，防 open-redirect）。
- 401 钩子在 `request.ts` 拦截器：清 authStore + 跳登录（带 redirect）；auth 引导 / 登录调用传 `skipAuthHandler` 跳过，冷启动 `fetchMe` 另传 `skipErrorToast` 静默（两个标记经 `declare module 'axios'` 扩展 config 类型）。
- 顶栏用户区：登录后 `el-dropdown`（注销 = best-effort 调后端 + 清本地 + 跳登录，不加确认框）；未登录（免登录页访客）降级为「登录」按钮。
- 依赖方向：`stores → api → request`；router 对 store 只做守卫内**动态** import，模块顶层零业务依赖（避免静态环）。

### SSE 流式（对话核心，错一步流式就废）

- **用 `fetch` + `ReadableStream`，绝不用 `EventSource`**——要带请求 body 和鉴权头（CLAUDE.md《认证》《SSE 流式》）。
- 实现在 `utils/sse.ts`：按 SSE 帧协议逐行解析 `event:` / `data:`，以回调上报；心跳注释（`: ping`）跳过。
- `credentials: 'include'` 携带 session cookie。
- 事件协议（`meta` / `delta` / `tool_call` / `tool_result` / `done` / `error`）见 CLAUDE.md《SSE 流式》。
- 客户端断连 → `AbortSignal` 取消 → 后端 ctx 取消 → 上游 LLM 调用取消（省 token）。

### 字段命名与类型（与后端一致）

- JSON 字段 **snake_case**（与 Go schema 的 `json` tag、DB 列名一致）。
- bigint 主键/外键 JSON 里**序列化为字符串**（Go `json:"id,string"`），避免 JS 超过 `2^53` 丢精度；但**请求 body 的外键是数值**（Go uint64 无 `,string` tag，传字符串会 400）——行数据回传时用 `Number()` 转换（先例：`api/provider.ts`）。
- 时间 RFC 3339 / UTC，带 `Z`。
- 时间 RFC 3339 / UTC，带 `Z`。
- 空值：列表空 → `[]`，字符串空 → `""`，对象未加载 → `null`（前端免空判断）。

### 分页（按资源增长性选型）

- 大列表（`conversations` / `messages` / `executions`）走**游标分页**：`limit` + `cursor`，禁用 OFFSET。
- 极小静态表（`providers` / `agents` / `models` / `mcp_servers` / `knowledge_bases` / `workflows`）走**偏移分页**：`page` + `page_size`，原生适配 Element Plus 分页组件。
- 两种 meta 字段均收敛在 `types/index.ts` 的 `ResultMeta`，对应查询参数在 `PageQuery`。

### 公共组件与 composables

列表页骨架 = `PageHeader` + `HifyTable`；新增/编辑配 `HifyFormDialog`；删除用 `useConfirm` 一行完成全流程；非分页请求用 `useRequest` 管三态。

- `HifyTable<T>`：`columns`（label/prop/width/slot）+ `api` 返回 `PageResult<T>`（直接接 `getList`）；内部管 loading / 偏移分页；增删后 `refresh()`；空态 `el-empty`。定位配置表，游标列表不走它。
- `HifyFormDialog<T>`：`v-model` 显隐 + `open(data?)` 区分编辑/新增；内部持有表单副本，打开重建、关闭自动重置；提交事件 `(form, done)`，父组件调 API 后 `done(true)` 关弹窗 / `done(false)` 停 loading 保持打开。
- `useConfirm({ message, api, ... })` 调用即执行：确认框（红色确认按钮）→ 调 api → `notifySuccess`；取消静默 `false`，api 失败 reject。
- `useRequest(api)` → `{ data, loading, error, execute }`；错误只记状态不重复弹（拦截器已弹）。
- `useBreakpoint()` → `{ width, isNarrow, isCompact }`：断点唯一事实源是 `BREAKPOINTS`（1200 / 992，见 design-system.md《响应式断点》）；`HifyTableColumn.hideBelow: BREAKPOINTS.md` 标记窄屏隐藏的次要列。
- 组件视觉值全走 token；表格卡片 body padding 0 是内容卡片 20px 规则的唯一例外（见 design-system.md《整体布局》）。
- 首个落地正式页：`provider/ProviderList.vue`（已接真实 API，`api/provider.ts` 是类型与请求方法的唯一事实源）；列表带健康状态 / 模型数聚合列，模型管理走 `ProviderModelsDrawer` 抽屉（同步导入默认停用，勾选列 = 启用，PUT 全量回传 + 失败回滚视觉态）。

### 路径别名

- `@/` → `src/`（`vite.config.ts` 的 resolve.alias 与 `tsconfig.json` 的 paths 双向配置，缺一不可）。

## 开发命令

```bash
cd web
npm install        # 安装依赖
npm run dev        # 开发服务器（:5173，/api 代理到后端 :8080）
npm run build      # 类型检查（vue-tsc --noEmit）+ 生产构建到 dist/
npm run preview    # 预览构建产物
npm run type-check # 仅类型检查，不产出
```

> 开发期 `/api` 由 Vite 代理转发到 `http://localhost:8080`（后端 `SERVER_PORT`，见 `.env`）。
> 生产期由 nginx 反代到 `hify:8080` 并透传 SSE，见 CLAUDE.md《部署架构》。
