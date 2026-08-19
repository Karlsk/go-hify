# Auth 前端接入 spec（登录 / 注册独立页 + 顶栏注销）

> 状态：**已实施**（2026-08-19）。澄清确认：① 注册成功**自动登录**进入；② 品牌面板**深色底 + 渐变点缀**（复用侧边栏语言）；③ `/design` **免登录**（`meta.public`）。
> 前置：后端 auth 四端点已落地接线（`POST /auth/login` 200 + Set-Cookie、`POST /auth/register` 201、`POST /auth/logout` 204 幂等清 cookie、`GET /auth/me` 200）；session cookie `hify_session`（HttpOnly + SameSite=Lax，TTL 7 天）。
> 背景：后端 session 中间件已生效而前端无守卫——未登录开 `/provider` 连环弹「未登录」toast，本批一并消除。

## 1. 路由与守卫（router/index.ts）

- 新路由 `/login`、`/register`（`views/auth/`），`meta: { title, bare: true }`。
- `RouteMeta` 扩展：`bare`（无 chrome + 免登录）、`public`（免登录保留 chrome，仅 `/design`）。
- `beforeEach`：public 非 bare 放行；其余先 `ensureReady()`（一次 `fetchMe`）——bare 页已登录 → `redirectTarget`（仅接受 `/` 开头站内路径，防 open-redirect）；非 bare 未登录 → `/login?redirect=to.fullPath`。
- 守卫内**动态** `import('@/stores/auth')`：router 顶层零业务依赖，`request → router` 静态边无环。

## 2. API 层与状态

- **api/auth.ts**：`AuthUser` / `LoginData` / `RegisterData` + 4 方法。`login` 传 `skipAuthHandler`（登录页自身 401 = 凭据错误业务结果）；`fetchMe` 另传 `skipErrorToast`（冷启动未登录静默）。
- **stores/auth.ts**（Pinia 首个 store）：`state { user, ready }`；`ensureReady`（401 静默归 null）；`login`（Set 后写身份）；`clear`（只清本地）；`logout`（best-effort 调 API，**失败也清本地**——Redis TTL 7 天自然过期兜底）。getters：`isLoggedIn` / `username` / `avatarLetter`。

## 3. request.ts 401 钩子（填既有占位）

- `declare module 'axios'` 扩展 `skipAuthHandler` / `skipErrorToast` 内部标记。
- 401 且未带 `skipAuthHandler`：动态 import 清 authStore + `router.push({name:'login', query:{redirect: 当前 fullPath}})`（已在 login 页不重复推）；动态 import 避免与 stores 模块环。

## 4. 页面与布局（零新增 token）

- **components/AuthShell.vue**：分栏壳。左 42% 品牌面板（`--hf-sidebar-bg` 深底 + 渐变 H 方块 48px + `Hify` 渐变字 + tagline + 左下版本号；≤992 isCompact 隐藏）；右 `--hf-bg-page` 底居中白卡片（400px、`--hf-radius-xl`（token 注释的既定用途「登录卡片」）、`--hf-shadow-lg`、`--hf-border-2`）。视觉记录见 design-system.md《登录 / 注册页（v4 新增）》。
- **LoginView.vue**：用户名 + 密码（回车提交）；规则对齐后端（username 1-128 / password 8-128）；成功 → toast + 按 `redirect` 校验回跳；401 凭据错误由拦截器弹。
- **RegisterView.vue**：+ 确认密码（前端专用字段，一致性校验）；成功 → 自动 `auth.login` → toast「注册成功，已自动登录」→ push `/`；409 用户名冲突由拦截器弹、表单保持。极端分支（注册成功但登录网络失败）提示已弹、可手动去登录。

## 5. App.vue

- bare 路由不渲染 chrome（`v-if="!route.meta.bare"`，else 分支裸 `<router-view />`）。
- 用户区：占位 `'Admin'` 换 `authStore`（头像首字母 + 用户名）；`el-dropdown`（trigger click）菜单项「退出登录」→ `auth.logout()` + toast + 跳 `/login`，不加确认框（低风险幂等）。
- `user` 为 null（免登录 `/design` 访客）降级为「登录」link 按钮。

## 6. 手测走查清单

1. 未登录访问 `/provider` → 跳 `/login?redirect=/provider`，**无 toast 弹窗**（冷启动静默）；登录后回跳 provider 页。
2. 登录页：凭据错误 401 弹「用户名或密码错误」；密码 <8 位表单校验拦截；回车可提交。
3. 注册：用户名重复 409 弹「用户名已存在」且表单保持；成功自动登录进入；确认密码不一致前端拦截。
4. 顶栏：登录后显示真实用户名 + 头像首字母；下拉「退出登录」→ 回登录页 + toast；再访问受保护页重新要求登录。
5. session 过期（Redis 删 key 或等 TTL）后在页内操作任意 API → 自动踢回登录页（redirect 带当前路径）。
6. `/design` 免登录可达；未登录时顶栏用户区为「登录」按钮。
7. 已登录访问 `/login` → 直接进系统（不显示登录页）。
8. ≤992 窄窗：登录页品牌面板隐藏、表单全宽；其余页行为不变。

## 7. 验证

`npm run type-check` + `npm run build` 通过（本批纯前端，后端零改动）。

## 8. 范围外（defer）

- 「记住我」/ 找回密码（内部工具无此需求）；用户资料编辑；前端单测 / E2E 基建；多标签页登录态同步（BroadcastChannel）；登录失败次数限制（后端事项）。
