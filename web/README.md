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

> Element Plus 选全量引入而非 unplugin 按需：内部工具不抠 bundle 体积，省掉 auto-imports.d.ts / components.d.ts 的配置与噪音，一人维护下最省心。前端构建产物由 nginx 长缓存托管，体积不进运行时关键路径。

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
    ├── App.vue             # 根布局：左侧 EP 菜单 + 右侧 <router-view />
    ├── assets/
    │   └── styles/main.css # 全局 reset + 字体
    ├── router/index.ts     # 路由表（三条扁平路由 /provider /agent /chat）
    ├── stores/             # Pinia 模块（user/session 等，按业务新增）
    ├── api/                # 各模块接口定义（axios 实例在 utils/request.ts）
    ├── utils/
    │   ├── request.ts      # axios 实例 + Result 信封拆包 + 错误统一处理
    │   └── sse.ts          # fetch + ReadableStream（SSE 流式，不用 EventSource）
    ├── types/
    │   └── index.ts        # Result / ResultError / ResultMeta / PageQuery
    └── views/
        ├── provider/ProviderList.vue
        ├── agent/AgentList.vue
        └── chat/ChatView.vue
```

## 约定（与 CLAUDE.md 对齐）

### 请求与响应信封

- API 统一前缀 `/api/v1`（`request.ts` 的 `baseURL`）。
- 所有响应走后端 `respond.Result` 信封：`{ success, data, error, meta }`。
- `request.ts` 响应拦截器统一拆信封：`success === false` → 取 `error.code` / `error.message` → `ElMessage.error` 提示 + `reject(new RequestError(code, message))`，调用方按 `error.code` 分支。
- `success === true` 自动解包：导出 `get` / `post` / `put` / `del` 四个泛型 helper（`get<T>(url)` 直接返回 `Promise<T>`，即业务 `data`）；解包后 `meta` 不可见，需分页 `meta` 的列表端点将来用独立 `getList` 取 `{ data, meta }`。
- HTTP 主信号（2xx/4xx/5xx）由 axios 错误分支兜底；401 预留跳登录钩子（auth 模块落地后接 `router.push('/login')`）。
- 业务错误码（`PROVIDER_NOT_FOUND` / `BUDGET_EXHAUSTED` / …）见 CLAUDE.md《错误处理》表。

### SSE 流式（对话核心，错一步流式就废）

- **用 `fetch` + `ReadableStream`，绝不用 `EventSource`**——要带请求 body 和鉴权头（CLAUDE.md《认证》《SSE 流式》）。
- 实现在 `utils/sse.ts`：按 SSE 帧协议逐行解析 `event:` / `data:`，以回调上报；心跳注释（`: ping`）跳过。
- `credentials: 'include'` 携带 session cookie。
- 事件协议（`meta` / `delta` / `tool_call` / `tool_result` / `done` / `error`）见 CLAUDE.md《SSE 流式》。
- 客户端断连 → `AbortSignal` 取消 → 后端 ctx 取消 → 上游 LLM 调用取消（省 token）。

### 字段命名与类型（与后端一致）

- JSON 字段 **snake_case**（与 Go schema 的 `json` tag、DB 列名一致）。
- bigint 主键/外键 JSON 里**序列化为字符串**（Go `json:"id,string"`），避免 JS 超过 `2^53` 丢精度。
- 时间 RFC 3339 / UTC，带 `Z`。
- 空值：列表空 → `[]`，字符串空 → `""`，对象未加载 → `null`（前端免空判断）。

### 分页（按资源增长性选型）

- 大列表（`conversations` / `messages` / `executions`）走**游标分页**：`limit` + `cursor`，禁用 OFFSET。
- 极小静态表（`providers` / `agents` / `models` / `mcp_servers` / `knowledge_bases` / `workflows`）走**偏移分页**：`page` + `page_size`，原生适配 Element Plus 分页组件。
- 两种 meta 字段均收敛在 `types/index.ts` 的 `ResultMeta`，对应查询参数在 `PageQuery`。

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
