# Hify 设计系统（Design Tokens）

> 本文件是 Hify 前端视觉规范的事实源，被 [web/README.md](../../web/README.md) 引用。
> 风格定位：**浅底 + 科技感点缀**——浅色主体保证长时间阅读的表格/表单可读性，深色侧边栏与亮色交互元素制造品牌感。参考 Linear、Supabase：干净但不无聊，有设计感但不花哨。

## 设计原则

1. **可读性优先**：管理后台以表格/表单为主，正文对比度满足 WCAG AA（≥ 4.5:1）。
2. **一处定义，处处引用**：所有颜色/圆角/阴影/动效只存在于 CSS 变量，业务代码禁止硬编码视觉值。
3. **双层 token**：`--hf-*` 语义层（设计系统本体，不依赖任何组件库）＋ 映射层覆盖 Element Plus 的 `--el-*`（EP 组件自动继承主题）。手写组件（如聊天 SSE 渲染层）用 `--hf-*`。
4. **语义命名**：token 按用途命名（`--hf-bg-page`），不按字面色值命名（不叫 `--hf-white`）——未来加深色主题只加一组覆盖，不重命名。
5. **克制**：品牌渐变、glow 微光、spring 动效只用于点缀（logo、主 CTA、关键状态），不铺满界面。

## 色彩体系

### 主色（品牌蓝紫，Linear 紫 #5E6AD2）

AI/开发者工具的主流品牌色族。主按钮底用 500（白字对比度 4.67:1，通过 WCAG AA），hover 600，按下 700；50/100 用于浅色选中底。

| Token | 值 | 用途 |
|---|---|---|
| `--hf-primary-50` | `#F1F2FB` | 选中行底 / 最浅背景 |
| `--hf-primary-100` | `#E4E7F9` | 浅色标签底 / hover 底 |
| `--hf-primary-200` | `#CBCFF3` | 边框强调 / 描边 |
| `--hf-primary-300` | `#A8AEEA` | — |
| `--hf-primary-400` | `#8189DE` | 深色背景上的品牌字 / 指示条 |
| `--hf-primary-500` | `#5E6AD2` | **品牌基准** / 主按钮 / 链接图标 |
| `--hf-primary-600` | `#4D57BE` | 按钮 hover / 文字链接 |
| `--hf-primary-700` | `#40479C` | 按钮按下 |
| `--hf-primary-800` | `#363C7D` | — |
| `--hf-primary-900` | `#2F3364` | — |
| `--hf-primary-950` | `#1B1D3C` | 深色侧边栏品牌点缀 |

### 辅色（青色 cyan #06B6D4）

定位为**品牌点缀 / 数据高亮**：在线状态点、数据可视化、代码/数值高亮、工具调用标记。

> **不兼任语义状态色。** 成功/警告/错误用独立语义色（下节），避免薄荷绿与 success 绿撞色混淆语义——这是辅色选青色而非薄荷绿的原因。

色阶 `--hf-accent-50…950`，基准 `--hf-accent: #06B6D4`（Tailwind cyan 色阶）。

### 语义色（状态专用，每个四件套：基准 / hover / 浅底 / 边框）

| 语义 | 基准 | hover | 浅底（soft） | 边框 | 场景 |
|---|---|---|---|---|---|
| success | `#16A34A` | `#15803D` | `#F0FDF4` | `#BBF7D0` | 连通成功 / 已完成 / 在线 |
| warning | `#F59E0B` | `#D97706` | `#FFFBEB` | `#FDE68A` | 预算 80% / 降级 / 处理中 |
| danger | `#EF4444` | `#DC2626` | `#FEF2F2` | `#FECACA` | 失败 / 熔断 / 删除 |
| info | `#64748B` | `#475569` | `#F1F5F9` | `#E2E8F0` | 中性提示 / 草稿 |

命名：`--hf-success` / `--hf-success-hover` / `--hf-success-soft` / `--hf-success-border`（warning / danger / info 同理）。

### 背景色阶（浅色主体）

| Token | 值 | 用途 |
|---|---|---|
| `--hf-bg-page` | `#F7F8FA` | 页面底（el-main） |
| `--hf-bg-container` | `#FFFFFF` | 卡片 / 表格 / 面板 / 输入框 |
| `--hf-bg-subtle` | `#F3F5F8` | hover 底 / 表头底 / 斑马纹 |
| `--hf-bg-muted` | `#ECEFF4` | chip / 骨架屏 / 填充 |
| `--hf-bg-overlay` | `rgba(15, 23, 42, 0.45)` | 弹窗遮罩（冷调，不用纯黑） |

### 文字色阶（冷灰 slate，与蓝紫同调——Linear 质感的关键）

| Token | 值 | 用途 |
|---|---|---|
| `--hf-text-1` | `#1E293B` | 主文字 / 标题 / 表格正文 |
| `--hf-text-2` | `#475569` | 次要文字 / 描述 / 表头 |
| `--hf-text-3` | `#94A3B8` | 弱化 / placeholder / 时间戳 |
| `--hf-text-4` | `#CBD5E1` | 禁用 |
| `--hf-text-inverse` | `#FFFFFF` | 深底上的文字 |
| `--hf-text-link` | `#4D57BE` | 文字链接（= primary-600，保证对比度） |

### 边框

| Token | 值 | 用途 |
|---|---|---|
| `--hf-border-1` | `#E2E8F0` | 默认边框（输入框 / 卡片 / 表格线） |
| `--hf-border-2` | `#EEF2F6` | 浅分割线 |
| `--hf-border-3` | `#CBD5E1` | hover / 强调边框 |

### 深色侧边栏

定位：近纯黑深底，与浅色内容区形成对比；炫酷克制在"渐变文字 + 渐变方块 + 主色竖线"三处，不堆发光/入场动画/毛玻璃。

Token：

| Token | 值 | 用途 |
|---|---|---|
| `--hf-sidebar-bg` | `#13151E` | 侧边栏底（带微蓝紫调，非死黑）·冻结 |
| `--hf-sidebar-bg-hover` | `rgba(255, 255, 255, 0.10)` | 菜单 hover ·v2 改值 |
| `--hf-sidebar-bg-active` | `rgba(255, 255, 255, 0.12)` | 菜单选中背景（微亮）·v2 改值 |
| `--hf-sidebar-indicator` | `var(--hf-primary-500)` | 选中态左侧 3px 主色竖线 ·v2 新增 |
| `--hf-sidebar-text` | `rgba(255, 255, 255, 0.8)` | 菜单默认文字（80% 白）·v2 改值 |
| `--hf-sidebar-text-hover` | `#E4E8F1` | hover 文字 ·冻结 |
| `--hf-sidebar-text-active` | `#FFFFFF` | 选中文字 ·冻结 |
| `--hf-sidebar-text-muted` | `rgba(255, 255, 255, 0.45)` | 副标题 / 版本号 ·v2 新增 |
| `--hf-sidebar-border` | `rgba(255, 255, 255, 0.08)` | 侧边栏内分割线 ·冻结 |

> **v2 演进记录**（侧边栏改造）：冻结 bg / border / hover 文字 / 选中文字四值；仅菜单三态相关 3 个 token 改值（`text` 灰白→80% 白、`bg-hover` 6%→10%、`bg-active` 主色 tint→白 12%）；新增 `indicator` / `text-muted`。除菜单三态外不产生视觉变化。

结构（`App.vue`）：

```
el-aside（220px ↔ 64px，width 过渡 300ms ease-out）
├── 品牌区（56px，border-bottom）
│   ├── 渐变 "H" 圆角方块（26px，--hf-gradient-brand；折叠时独立成 logo 锚点）
│   ├── 品牌名 "Hify" —— 主色渐变文字（background-clip: text）
│   └── 副标题 "AI Agent Platform"（12px，text-muted；折叠隐藏）
├── el-menu（collapse，透明底）
│   模型管理 Setting / Agent 管理 User / 对话 ChatDotRound
└── 底部区（border-top）
    ├── 折叠 / 展开按钮（Fold / Expand 图标，hover 用 bg-hover）
    └── 版本号 v{package.json}（text-muted；折叠隐藏）
```

菜单三态：

| 状态 | 文字 | 背景 | 其他 |
|---|---|---|---|
| 默认 | 80% 白 | transparent | — |
| hover | #E4E8F1（近纯白） | 白 10% | 变色 120ms |
| active | 纯白 | 白 12% | 左 3px 主色竖线（圆角、垂直居中约 60% 高） |

行为：

- 折叠态：宽 64px；品牌区只留方块；菜单 icon-only（EP `collapse`，标题走 tooltip）；底部区只留按钮。
- 版本号经 Vite `define` 从 package.json 构建期注入（`__APP_VERSION__`），起步 `v0.1.0`。

### 品牌渐变（logo、登录页、关键 CTA、主操作按钮·v3 扩展）

```css
--hf-gradient-brand: linear-gradient(135deg, var(--hf-primary-500) 0%, var(--hf-accent-500) 100%);
--hf-gradient-brand-hover: linear-gradient(135deg, var(--hf-primary-600) 0%, var(--hf-accent-600) 100%);
```

## 整体布局（v3）

三层层次：**白顶栏 / 浅灰页面底（`--hf-bg-page`）/ 白卡片（`--hf-shadow-sm` 轻阴影）**。结构（`App.vue`）：

```
el-container（row）
├── el-aside        深色侧边栏（见上节，冻结）
└── el-container（column）
    ├── header .app__header   56px（与侧边栏品牌区等高）：白底 + border-bottom（--hf-border-2）
    │   ├── 左：el-breadcrumb「首页 / 当前页」——当前页 = route.meta.title（router 为唯一来源）
    │   └── 右：用户区 = 28px 品牌渐变圆头像（用户名首字母）+ 用户名；auth 未接入为占位
    └── el-main .app__main    padding 24px（--hf-space-6），底 --hf-bg-page
        └── 每页 = PageHeader（标题 + 描述 + 右侧 actions）→ 内容卡片
```

### 页面标题区（`components/PageHeader.vue`）

- 标题：18px（`--hf-font-size-lg`）/ 600 / `--hf-text-1`；描述：14px / `--hf-text-2`，标题下 4px。
- `actions` 插槽在右侧，与标题顶对齐；按钮间距 8px。PageHeader 与内容间距 16px（`--hf-space-4`）。

### 内容卡片（el-card + 映射）

白底 + 轻阴影 + 圆角、无边框。EP 把 `--el-card-*` 定义在 `.el-card` 选择器自身（不在 `:root`），映射必须写在组件选择器上才打得过：theme-element-plus.css 末尾 `.el-card { --el-card-border-radius: --hf-radius-md; --el-card-padding: --hf-space-5（20px）; --el-card-border-color: transparent }`。阴影由 main.css `.el-card.is-always-shadow { box-shadow: var(--hf-shadow-sm) }` 提供（选择器必须带 `.is-always-shadow`，否则特异性打不过 EP 自身规则）。

### 按钮三式

| 类型 | 实现 | 备注 |
|---|---|---|
| 主要 | `.el-button--primary` 覆写为 `--hf-gradient-brand` + 透明边框；hover/focus → `--hf-gradient-brand-hover` + glow 微光；active → hover 渐变无 glow；disabled → 退回 EP 默认浅色不渐变；**选择器排除 `:not(.is-link):not(.is-text)`**——链接 / 文字按钮（表格行内操作）保持 EP 原生文字链，渐变只用于实心按钮 | v3 演进：渐变范围扩至所有主操作；v4 澄清 link/text 豁免 |
| 次要 | EP 默认按钮：白底 + `--hf-border-1` 边框 | 零改动 |
| 危险 | EP `type="danger"`（映射 `--hf-danger`） | 零改动 |

### 间距纪律

页面边距 24px（`--hf-space-6`，el-main padding）/ 卡片内边距 20px（`--hf-space-5`，`--el-card-padding`）/ 元素间距 16px（`--hf-space-4`，PageHeader 下距、actions 组内 8px 例外）。

> **v3 演进记录**（整体布局）：**0 个 `--hf-*` 改值、0 个新增语义 token**——顶栏 / 面包屑 / 用户区 / PageHeader / 卡片 / 按钮全部落到已有 token。新增仅限 EP 映射（`.el-card` 选择器上的 `--el-card-*` 覆盖；面包屑无需映射，EP 直接走已映射的 `--el-text-color-regular / -placeholder`）与 main.css 组件级覆写（渐变主按钮、卡片阴影、面包屑当前项）。唯一的原则级变化：品牌渐变使用范围由「logo / 登录页 / 关键 CTA」扩展至「所有主操作按钮」（用户显式决策）；渐变仍不用于背景铺色、文字（除 logo）、卡片装饰。

### 登录 / 注册页（`components/AuthShell.vue`，v4 新增）

bare 路由（`meta.bare`）：App.vue 不渲染侧边栏 / 顶栏，整页交给视图自身的分栏布局。

```
AuthShell（flex，100vh）
├── aside 品牌面板 42%（≤992 隐藏）
│   ├── 垂直居中品牌块：渐变 H 方块 48px（--hf-radius-md）+「Hify」渐变字 24px
│   │   + tagline「AI Agent Platform」（--hf-sidebar-text-muted）
│   └── 左下版本号 v{x}（mono / xs / text-muted）
└── main 表单区（flex 1，--hf-bg-page）
    └── 白卡片 400px：padding 40px/32px（--hf-space-10/8）、--hf-radius-xl（该 token 注释
        的既定用途「登录卡片」）、--hf-shadow-lg、--hf-border-2；标题 18px/600 + 副文
        text-3 + el-form（label-position="top"）+ 全宽渐变主按钮
```

- 品牌面板 = `--hf-sidebar-bg` 深底，复刻应用侧边栏语言——登录后进入应用视觉无缝衔接；渐变仍只出现在 logo 方块与品牌字，不铺底。
- ≤992（isCompact）隐藏品牌面板，表单全宽居中。**0 个新增 token**，全部复用既有侧边栏 / 卡片 / 字号 / 间距 token。
- 行为：登录成功回跳 `?redirect=` 来源页（仅接受 `/` 开头站内路径，防 open-redirect）；注册成功自动登录进入。

## 响应式断点

管理台以桌面为主（内部工具），只定义两档宽度断点。CSS 变量不能用于 `@media`（CSS 规范限制），断点值做不成 `--hf-*` token——代码侧唯一事实源是 `web/src/composables/useBreakpoint.ts` 的 `BREAKPOINTS` 常量，本表与其保持同步。

| 断点 | 值 | 生效行为 |
|---|---|---|
| `lg` | 1200px | 侧边栏折叠为 64px 图标模式。**跨界同步**语义：加载时按宽度定档；每次跨越断点瞬间自动重设；两次跨界之间手动折叠 / 展开自由保留 |
| `md` | 992px | 表格次要列隐藏：`HifyTableColumn.hideBelow` 标记（如 ProviderList 的 Base URL / 创建时间两列，取 `BREAKPOINTS.md`） |

规则：

- 新增断点行为**先在本表登记、再写代码**；断点值只增不改（同 token 纪律）。
- 不做移动端适配（对话 / 管理均为桌面场景）。
- 抽屉（520px 固定宽）与弹窗不参与断点体系。

## 圆角

| Token | 值 | 用途 |
|---|---|---|
| `--hf-radius-xs` | `4px` | 标签 / 复选框 |
| `--hf-radius-sm` | `6px` | 按钮 / 输入框 / 菜单项（EP 基准） |
| `--hf-radius-md` | `8px` | 卡片 / 下拉面板 |
| `--hf-radius-lg` | `12px` | 弹窗 / 大面板 |
| `--hf-radius-xl` | `16px` | 登录卡片等特大容器 |
| `--hf-radius-full` | `9999px` | 胶囊 / 头像 / 状态点 |

## 阴影（冷调投影，基于 slate-900 `rgb(15 23 42)`）

| Token | 值 | 用途 |
|---|---|---|
| `--hf-shadow-xs` | `0 1px 2px rgb(15 23 42 / 0.05)` | 卡片贴边感 |
| `--hf-shadow-sm` | `0 1px 3px rgb(15 23 42 / 0.08), 0 1px 2px rgb(15 23 42 / 0.04)` | 卡片默认 |
| `--hf-shadow-md` | `0 4px 12px rgb(15 23 42 / 0.08)` | hover 抬升 / 下拉 |
| `--hf-shadow-lg` | `0 12px 32px rgb(15 23 42 / 0.12)` | 弹窗 |
| `--hf-shadow-xl` | `0 24px 64px rgb(15 23 42 / 0.16)` | 全屏模态 |
| `--hf-shadow-focus` | `0 0 0 3px rgb(94 106 210 / 0.18)` | 输入框聚焦环 |
| `--hf-shadow-focus-accent` | `0 0 0 3px rgb(6 182 212 / 0.18)` | 辅色聚焦环（数据输入场景） |
| `--hf-shadow-glow-primary` | `0 6px 16px rgb(94 106 210 / 0.28)` | 主按钮 hover 微光（科技感来源） |

## 动效

| Token | 值 | 用途 |
|---|---|---|
| `--hf-duration-fast` | `120ms` | hover 变色 / 图标切换 |
| `--hf-duration-base` | `200ms` | 默认过渡 |
| `--hf-duration-slow` | `300ms` | 弹窗 / 折叠面板 / 抽屉 |
| `--hf-ease-out` | `cubic-bezier(0.16, 1, 0.3, 1)` | 入场 / 展开（减速收尾，现代感） |
| `--hf-ease-in-out` | `cubic-bezier(0.45, 0, 0.55, 1)` | 变色 / 小位移 |
| `--hf-ease-spring` | `cubic-bezier(0.34, 1.56, 0.64, 1)` | 小元素弹出（慎用，一次一屏≤1处） |

纪律：

- 只动 `transform` / `opacity`（GPU 合成），不动 width / height / margin 等布局属性。
- 表格行、列表大量元素不做入场动画。
- `prefers-reduced-motion: reduce` 下全部过渡降为 1ms。

## 字体

```css
--hf-font-sans: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue',
  Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif;
--hf-font-mono: 'JetBrains Mono', 'SF Mono', 'Fira Code', ui-monospace, 'Cascadia Code',
  Consolas, monospace;   /* API Key、token 数、代码片段 */
```

字号：`--hf-font-size-xs 12px / -sm 13px / -base 14px / -md 16px / -lg 18px / -xl 20px / -2xl 24px`；字重 `400 / 500 / 600`（不用 700+，管理后台不需要）。

## 间距与层级

- 间距 4px 基数：`--hf-space-1…12`（4 / 8 / 12 / 16 / 20 / 24 / 32 / 40 / 48）。
- z-index 自定义层：`--hf-z-dropdown 1000 / -sticky 1020 / -overlay 1050 / -modal 1060 / -popover 1070 / -toast 1090`；EP 自带 2000+ 体系不冲突。

## Element Plus 映射（theme-element-plus.css）

EP 官方支持 CSS 变量覆盖，映射层把 EP 拉到我们的主题上：

| EP 变量族 | 映射策略 |
|---|---|
| `--el-color-primary` + `light-3/5/7/8/9` + `dark-2` | 按 EP 语义（500 与白/黑按比例混合）计算派生值，基准 `#5E6AD2` |
| `--el-color-success/warning/danger/error/info` 及派生 | 同上，基准见语义色表 |
| `--el-text-color-*` / `--el-border-color*` / `--el-bg-color*` / `--el-fill-color*` | 直指对应 `--hf-*` |
| `--el-table-*` | 表头底 = `bg-subtle`、行 hover = `bg-subtle`、表格线 = `border-2`、正文 = `text-1` |
| `--el-border-radius-base/small` | `6px / 4px` |
| `--el-font-family / -size-*` | 字体族 + 12/13/14/16/18 |
| `--el-transition-duration(-fast)` / `--el-ease-*` | `200ms / 120ms` + hf 曲线 |
| `--el-box-shadow*` / `--el-mask-color` | 冷调阴影与遮罩 |
| `--el-card-*`（v3） | 卡片圆角 / 内边距 / 边框——EP 定义在 `.el-card` 选择器上，映射须写在组件选择器（见《整体布局》） |

## 文件结构

```
web/src/assets/styles/
├── tokens.css                 # --hf-* 设计系统本体（唯一事实源）
├── theme-element-plus.css     # --el-* 覆盖映射（只引用 tokens，不新增值）
└── main.css                   # @import 上两者 + reset + body 基础样式
```

引入顺序（main.ts）：`element-plus/dist/index.css` → `main.css`（内含 tokens → theme），`:root` 同名变量后写生效。

## 验收

`npm run dev` 后访问 `/design` 预览页：色阶板 / 按钮 / 表单 / 表格 / 状态标签 / 圆角阴影 / 动效演示全部可见即达标；侧边栏在任意页面可见深色效果；末节《公共组件》演示区（HifyTable 分页/空态、HifyFormDialog 新增编辑、useConfirm 删除确认，mock 数据）全链路可点。

## 一期不做（留扩展位）

- **Dark mode**：token 语义命名已就绪，未来在 `html[data-theme="dark"]` 下覆盖 `--hf-*` 即可。
- 组件级 design token（按钮高度、间距密度）沿用 EP 默认，不自造。
