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

### 品牌渐变（克制使用：logo、登录页、关键 CTA）

```css
--hf-gradient-brand: linear-gradient(135deg, var(--hf-primary-500) 0%, var(--hf-accent-500) 100%);
--hf-gradient-brand-hover: linear-gradient(135deg, var(--hf-primary-600) 0%, var(--hf-accent-600) 100%);
```

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

## 文件结构

```
web/src/assets/styles/
├── tokens.css                 # --hf-* 设计系统本体（唯一事实源）
├── theme-element-plus.css     # --el-* 覆盖映射（只引用 tokens，不新增值）
└── main.css                   # @import 上两者 + reset + body 基础样式
```

引入顺序（main.ts）：`element-plus/dist/index.css` → `main.css`（内含 tokens → theme），`:root` 同名变量后写生效。

## 验收

`npm run dev` 后访问 `/design` 预览页：色阶板 / 按钮 / 表单 / 表格 / 状态标签 / 圆角阴影 / 动效演示全部可见即达标；侧边栏在任意页面可见深色效果。

## 一期不做（留扩展位）

- **Dark mode**：token 语义命名已就绪，未来在 `html[data-theme="dark"]` 下覆盖 `--hf-*` 即可。
- 组件级 design token（按钮高度、间距密度）沿用 EP 默认，不自造。
