---
name: provider-adapter
description: Hify provider 模块接入新供应商的流程——第一步判断能否走 openai_compatible 零代码路径；确需新增 kind 时：分析 API → 加常量与 CHECK → 五处差异点接线 → 测试 → 前端枚举。当用户要"接入/新增某家 LLM 供应商、新增 provider kind、添加模型厂商"时使用。
argument-hint: [供应商名，如 deepseek]
---

# Provider 接入新供应商流程

供应商差异的架构现状：无策略模式注册表，是**集中 switch 分发 + 数据驱动**（eino adapter 构造在 llm/factory.go，认证头/端点/目录解析在 provider 模块三处 switch + 一张 map）。**闭集设计是刻意的**（CLAUDE.md：5 kind + openai_compatible 逃生阀），本流程不改这个架构。

两条铁律：

1. **先判断要不要写代码**——绝大多数供应商走 Step 0 的零代码路径
2. **新增 kind 的编辑点散落 9 处，漏改任何一处编译都不报错**（default 分支兜底返回运行时错误），必须拿 Step 3 的清单逐条勾

## Step 0 决策门：走哪条路径 ⏸

查供应商文档：是否提供 **OpenAI 兼容端点**（DeepSeek / Qwen / Kimi / vLLM / Groq / OpenRouter 等都有）。

- **有兼容端点** → 零代码：控制台建 provider，kind 选 `openai_compatible` + 填 base_url + key，SyncModels 验证即完成。**流程到此结束，不新增 kind**
- **协议不兼容的大厂**（无兼容端点、且 eino 无现成 adapter）→ 继续 Step 1
- **⏸ 决策点**：向用户说明两条路径的判断依据，确认走"新增 kind"后才动代码

## Step 1 分析 API ⏸

- **产出物**：决策记录 `docs/changelog/provider/<vendor>_adapter_spec.md`，五要素：
  1. **认证方式**：header 名与格式（`Authorization: Bearer` / `x-api-key` / `x-goog-api-key` / 无鉴权）；有无**必带的版本头**（Claude 教训：缺 `anthropic-version` 直接 400，误判不可达）
  2. **模型列表端点**：探测用 GET 路径（`/models` / `/api/tags` / 自定义）
  3. **目录响应结构**：`{"data":[...]}` 还是 `{"models":[...]}` 还是新形状；id 与展示名字段
  4. **错误形态**：429 / 401 / 403 / 5xx 的语义，有无 Retry-After
  5. **eino adapter**：cloudwego/eino 有无现成实现？构造参数怎么映射 BaseURL？
- **验证方式**：用 curl 实测端点（真实 key 走 `.env`，不进聊天记录），确认五要素与文档一致；**等用户确认分析结论**
- **注意**：探测路径与对话路径的认证方式可能不同（探测是轻量 GET，对话走 eino adapter），分开分析

## Step 2 加 Kind 常量（牵一发动全身的锚点）

- **产出物**：
  - `internal/provider/api/schema.go`：`KindXxx` 常量 + `validKind` map 加条目 + 报错消息枚举同步
  - `migrations/NNNNN_<desc>.sql`：providers 表 kind 的 CHECK 约束加值——`ALTER TABLE providers DROP CONSTRAINT` + `ADD CONSTRAINT ... CHECK (kind IN (...新值...))`（text+CHECK 的演进优势正在于此，只改 CHECK 不动类型；providers 表极小，无需 CONCURRENTLY）
- **验证方式**：`make migrate-up`；`go test ./internal/provider/api/...`——**TestConstantsPinned 必须先 RED 再 GREEN**（钉住新常量字符串，防 DB CHECK / 前端枚举漂移）

## Step 3 五处差异点接线（清单逐条勾，漏一处运行时才炸）

| # | 文件 | 加什么 |
|---|---|---|
| 1 | `internal/platform/llm/factory.go` `newChatModel` | case 分支：构造 eino adapter（BaseURL 映射注意各家字段类型差异，如 claude 是 `*string`） |
| 2 | `internal/platform/llm/profile.go` | 新 kind 的 Profile（默认复制的除外；先例：Ollama TTFT=120s） |
| 3 | `internal/provider/service/prober.go` `probeTarget` 认证头 switch | header 名 + 必带版本头（无 key 也要带的头单独处理，参考 claude 分支外的 `anthropic-version`） |
| 4 | `internal/provider/service/prober.go` `probeTarget` 端点 switch + `kindDefaultBase` map | 探测路径 + 默认 base URL |
| 5 | `internal/provider/service/sync.go` `parseModels` switch | 目录解析：id 字段 / 展示名字段 / 前缀清洗（参考 gemini 去 `models/` 前缀） |

- **验证方式**：逐条勾完上表后 `go build ./...`（只证明编译，不证明完整——default 兜底意味着漏 case 也能编译）
- **注意**：
  - `countModels` / sync 的包容 struct（`Data` + `Models` 双字段）：响应是 `{data:[]}` 或 `{models:[]}` 形状则**零改动**；新形状才加字段
  - kind 探测超时 10s 起步；冷启动慢的本地服务（Ollama 类）在上游 Profile 调 TTFT

## Step 4 测试验证

- **产出物**：
  - `prober_test.go`：httptest.Server 打桩新 kind——成功 / 401 鉴权失败 / 429 / HTML 错误页全路径
  - `sync_test.go`：Kinds 断言从旧集合扩到含新 kind；`parseDiscovered` 新形状解析
  - `factory_test.go`：新 kind 构造成功 + 未知 kind 拒绝
- **验证方式**：`go test ./internal/provider/... ./internal/platform/llm/... -count=1`；`go test -race`
- **⏸ 决策点**：用真实 key 跑一轮 TestConnection + SyncModels 冒烟（curl 或控制台），用户确认探测/同步/计数都符合预期

## Step 5 前端枚举 ⏸

- **产出物**：`web/src/api/provider.ts`（kind 类型与常量）+ `web/src/views/provider/ProviderList.vue`（表单 kind 下拉、表单校验规则同步新 kind 的 base_url 必填与否）
- **验证方式**：`cd web && npm run type-check`；浏览器走一遍 新建 → 测试连接 → 同步模型 闭环
- **⏸ 决策点**：向用户演示全流程，确认后收尾（docs/changelog 决策记录补实测结论、提交拆分）

## 踩坑提醒（沿用 provider 已沉淀经验）

- 必带版本头（anthropic-version 教训）：分析阶段没查到，探测阶段必误判 400 为不可达
- 429 算可达不算故障（与 LLM 错误分类一致）；429 的 body 非模型列表，计数为 0 不报错
- openai_compatible 的 base_url 必填由 `validateProvider` 兜底——新 kind 若无默认 base，照抄此校验
- 新 kind 若批量导入模型：导入默认 `enabled=false`，用户勾选启用（module-delivery 踩坑清单 2）
