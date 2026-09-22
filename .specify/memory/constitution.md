<!--
Sync Impact Report
==================
- Version change: 1.0.0 → 1.1.0
  理由：Principle I「明确不做」清单移除「可视化工作流拖拽编排」——范围调整
  （非原则删除），随 CLAUDE.md《不做什么》同条目先行修订而随动同步（治理条款：
  先 CLAUDE.md 后宪法）。触发源：用户 2026-09-21 批准 spec 009 交付 JSON / 拖拽
  双模式前端（FR-013②）。
- Modified principles: I（Simplicity First）——「明确不做」清单范围调整；
  拖拽编排以久经考验的 Vue Flow 落地，仍符合「现成方案优先」。
- Added sections: 无
- Removed sections: 无
- Templates requiring updates: 无（七条原则与治理条款未动，仅清单项调整）
- Follow-up TODOs: 无（受影响文档 CLAUDE.md 已先行修订；web/README.md 随 spec 009 补录）
-->

# Hify Constitution

Hify 是简化版 Dify 的 AI Agent 开发平台：一人开发、本地 Docker Compose 部署、
面向 20-50 人团队、峰值 3-5 QPS、主要压力在流式 SSE 对话。本宪法是
[CLAUDE.md](../../CLAUDE.md) 的治理提炼，约束所有 spec / plan / 任务与实现决策。

## Core Principles

### I. Simplicity First（好维护优先，NON-NEGOTIABLE）

一切决策的前提：一个人开发，好维护优先，不追求大厂级架构。

- 规模匹配：20-50 人、峰值 3-5 QPS、单实例 2C4G 足够；禁止为想象中的规模过度设计。
- 明确不做：多租户/权限体系、插件市场、计费系统、微调、标注、
  WebApp 发布/嵌入组件、多种部署形态。
  （可视化工作流拖拽编排原列「不做」，经用户 2026-09-21 批准后由 spec 009
  交付 JSON / 拖拽双模式，已移出本清单——后端配置契约仍为 JSON 不变。）
- 降级做：RAG 一期只支持 TXT/MD 纯文本（递归分割三级降级 + MD 围栏原子保护）；
  工作流仅 JSON 配置、线性 + 条件分支。
- 任何偏离本原则的复杂度（引入第 4 个项目、非必要抽象层）MUST 在 plan.md 的
  Complexity Tracking 表登记，并写明被否决的更简单替代方案。
- 久经考验的现成方案优先于手工自研（eino / gobreaker / semaphore，不自写状态机）。

### II. 模块化单体与单向依赖（NON-NEGOTIABLE）

模块化单体，按业务域组织（domain-first）：provider / agent / chat / rag / workflow /
mcp / auth 各一个模块，共享基建下沉 platform。业务代码全部放 internal/。

- 依赖只能单向、禁止循环；import 方向 = 依赖方向，由 Go 编译器强制。
  依赖清单以 CLAUDE.md《依赖方向》一节为准，不得新增清单外依赖。
- 跨模块调用三规则：只经目标模块 `api` 接口（组合根注入实现）；只用目标模块
  `api/` 的 schema（禁传 model 或模块内部类型）；循环依赖 = 架构错误，立即重构
  （共用能力下沉 platform），禁止任何绕过。
- 模块内部四层子包 api/service/store/handler，模块内依赖单向：
  handler→api、service→api、store→service（实现 service.Store 接口）。
- chat 处于依赖图最外层，任何业务域不得依赖 chat——chat 将来整体拆成独立服务的
  前提，零被依赖必须始终保持。
- platform 不依赖任何业务域。
- 业务方法第一参数恒为 `ctx context.Context`，全程不出现 gin 类型；
  `*gin.Context` 禁止传入 api 接口 / service。
- store 只操作本模块 model 声明的表；跨模块数据在 service 分次查询后组装，
  禁止跨模块 JOIN。

### III. 统一 LLM 接入层（platform/llm 单点，NON-NEGOTIABLE）

所有 LLM 调用（chat 对话、workflow LLM 节点、provider 连通性探测）一律经
`platform/llm`；超时 / 重试 / 熔断 / bulkhead 逻辑只写在这一层。

- 重试 / 超时 / 熔断逻辑出现在 chat / workflow / provider 即违规（禁止清单）。
- 每供应商 bulkhead 16 槽（semaphore，ctx 感知）；抢槽超时 5s fail-fast 返回
  `ErrProviderBusy`（503），禁止无限排队；禁止用 worker pool 限 LLM 并发。
- 三层超时（TTFT 30s / idle 看门狗 30s / overall 5min）一律
  `WithTimeoutCause` + `context.Cause`；绝不给流式 client 设
  `http.Client.Timeout`。
- 重试只在首 token 之前、默认最多 2 次；首 token 后失败发 `error` SSE 事件关流，
  禁止重试/续传。429 走 Retry-After 路径，不计入熔断失败。
- 熔断用 sony/gobreaker：连续 5 次最终失败打开 30s，半开 1 个探测；只对
  Timeout / Overloaded / Network / ProviderDown 计数。
- 错误分类表（Timeout/RateLimited/Overloaded/Network/InvalidRequest/Auth/
  ProviderDown）决定重试、熔断计数与用户提示，adapter 负责翻译各家错误。

### IV. SSE 流式链路完整性（NON-NEGOTIABLE）

流式对话是核心链路，错任何一条流式就废。

- nginx SSE 路由：`proxy_buffering off` + `proxy_http_version 1.1` +
  `X-Accel-Buffering: no` + `proxy_read_timeout 300s`；SSE 路由绝不开 gzip。
- SSE 走 POST + fetch（带 body 与鉴权头，`credentials: "include"`），
  绝不用 GET / `EventSource`。
- 客户端断连 → 服务端 ctx 取消 → 取消上游 LLM 调用（省 token）。
- 流空闲每 15s 发 `: ping` 注释行心跳；嵌套超时关系任何一层先到上层全部感知。
- 流一旦返回 200，后续错误只能用 `error` 事件表达（`code` + `retryable`），
  禁止改 HTTP 状态码。
- Docker `ulimit nofile 65535`。

### V. 数据库纪律（PG17 + pgvector）

- 表结构演进只走 migrations（goose/golang-migrate + SQL，只增不改、顺序幂等），
  禁止 GORM AutoMigrate。
- 每张新表按 CLAUDE.md《建表执行清单》逐条过：标准表头
  （`bigint GENERATED ALWAYS AS IDENTITY` + `created_at timestamptz`）、
  类型对表（text / timestamptz / jsonb / numeric / boolean，枚举用
  `text + CHECK` 非 PG enum）、每个外键必须单独建索引。
- 大列表默认 keyset（游标）分页，禁用 OFFSET（极小静态配置表例外）；
  LIMIT 服务端封顶；大表不算精确 `COUNT(*)`。
- 向量列建表即建 HNSW（`vector_cosine_ops`，m=16, ef_construction=64）；
  向量表不分区。
- 无软删除：可逆下架用 `enabled` 布尔开关，DELETE = 真删（级联清理，
  兜底 PG 每日备份）。
- `executions` 按月分区（建表即分区）+ 在线保留默认 90 天
  （`EXECUTIONS_RETENTION_DAYS` 可配），由应用内 PartitionMaintainer 维护。
- 事务最小化：只包必须原子化的写，事务内禁止外部调用（HTTP/LLM/阻塞式 Redis）；
  批量写用多 VALUES INSERT，禁循环单行。
- 禁 `SELECT *`；DML 必带 WHERE；参数类型对齐列类型。

### VI. 可观测与成本护栏

- 运行日志（executions 表）：每次 LLM 调用记 provider / model / token / 耗时 /
  错误类——排障唯一线索，不可省略。
- LLM 成本护栏（头号风险项）：每用户限流 + 每日预算熔断，fail-open
  （超支不阻断在线会话，拒绝新会话）+ 80% 告警。
- PG 每日备份保留 7-14 天：唯一不可再生数据，必做项。
- 结构化日志持久化到卷 + rotation；`/health` 探 PG + Redis；
  一期可观测 = /health + 结构化日志，Prometheus/Grafana 列为后期。
- 全仓库 Redis key 统一 `hify:` 前缀（`redisx.Key` 拼接）；
  fail-fast 计数（如 503 率）进结构化日志。

### VII. 统一契约与安全基线

- 所有 API 响应经 `platform/respond` 信封：`{success, data, error, meta}`；
  成功响应头一律 `no-store`。
- 错误码 = 哨兵错误的 `Error()` 字符串（`MODULE_REASON` 命名）；新增码必须先有
  `api/` 包哨兵错误，handler 用 `errors.Is` 映射状态码；禁止裸字符串或原始异常
  上抛；500 类不向前端泄露细节，只回 `INTERNAL_ERROR` + trace_id。
- 字段契约：JSON snake_case（Go 字段保持驼峰，tag 映射）、bigint ID 序列化为
  字符串、时间 RFC 3339 UTC、列表空返回 `[]` 非 `null`、枚举用字符串。
- 密钥管理：Provider API Key 在 DB 内加密存储（主密钥来自 env）；
  所有密钥走 `.env` / Docker secrets，禁止入 Git。
- 认证：最简登录（bcrypt + cookie session 存 Redis，TTL 7 天）；业务 API 一期
  不按用户隔离但必须过登录门槛；身份经 `platform/authctx` 注入 ctx。
- SSE 鉴权用 cookie（fetch + credentials），不走自定义 header 方案。

## 技术与架构约束

- 后端：Go 1.26 + Gin；GORM（CRUD）+ pgvector 召回走 `db.Raw` 原生 SQL；
  多模型统一接入用 cloudwego/eino。
- 数据：PostgreSQL 17（`pgvector/pgvector:pg17` 镜像）+ Redis 7.x（AOF 持久化）。
- 前端：Vue 3 + TypeScript + Element Plus + Vite；SSE 渲染层手写；
  目录结构与设计规范以 [web/README.md](../../web/README.md) 为准，
  已发布设计 token 只增不改。
- 部署：Docker Compose 单机 2C4G，4 常驻容器（nginx / hify / postgres / redis）
  + 1 定时备份容器；唯一对外暴露 nginx 443，其余走 compose 内部网络；
  部署细节以 [deploy/README.md](../../deploy/README.md) 为准。
- 入口精简：`cmd/hify/main.go` ~15 行（加载配置 + 调 app.Run）；装配集中在
  `internal/app/server.go` 的 Run，沿依赖清单自下游而上游构建。
- 命名规范（Go 标准）：包名 = 目录名全小写；跨包 import 用
  `<module><layer>` 别名（`providerapi` / `providersvc` / `providerstore`）；
  缩写词全大写/全小写（`UserID` 禁止 `UserId`）；Getter 不加 `Get` 前缀；
  module 路径 `github.com/Karlsk/go-hify`。
- 接口规范：统一前缀 `/api/v1`；RESTful 资源路径多词 kebab-case；
  非 crud 动作用 `/动词` 子路径；分页按资源增长性选游标或偏移；
  详见 CLAUDE.md《接口规范》。

## 开发工作流与质量门

- 测试与被测代码同包（`*_test.go`）；覆盖率 ≥ 80%；service 测试 stub 本模块
  Store 接口与下游 api 接口，store 测试用 sqlmock，handler 测试用 httptest。
- spec-kit 流程：specify → clarify → plan → tasks → implement；
  plan.md 的 Constitution Check 门禁必须逐条过本宪法，violation 必须在
  Complexity Tracking 表登记。
- handler 薄绑定：一个绑定函数只调一个接口方法；绑定 + 校验合一步走
  `respond.BindJSON`；不含业务逻辑、不直接访问数据库。
- 错误链路三层不吞错：store 原样上抛 → service 翻译成 api 哨兵
  （其余 `%w` 包装）→ handler `errors.Is` 映射状态码。
- 提交消息约定式：`<type>: <description>`（feat / fix / refactor / docs /
  test / chore / perf / ci）。
- 规范变更先改 CLAUDE.md（唯一事实源），再同步本宪法与受影响文档。

## Governance

- 本宪法是 CLAUDE.md 的治理提炼；冲突时以 CLAUDE.md 为准，并在下一版本
  同步修正宪法。
- 修订程序：提案（动机 + 影响面 + 迁移计划）→ 用户显式批准 → 更新版本号与
  Last Amended → 传播到受影响文档（CLAUDE.md、web/README.md、deploy/README.md、
  docs/design/data-model.md）。
- 版本语义：MAJOR = 原则删除或向后不兼容的重定义；MINOR = 新增原则 / 实质
  扩充既有指导；PATCH = 措辞澄清与笔误修正。版本歧义时先陈述理由再定版。
- 合规审查：每次 `/speckit-plan` 的 Constitution Check 是强制门禁；
  code review 按 CLAUDE.md 各节「禁止清单」逐条核对（依赖方向、platform/llm
  单点、SSE 链路、数据库纪律、密钥管理）。
- 原则 MUST 可测试、可判定：凡以「禁止清单」形式表达的规则一律作为 review
  checklist 项，禁止含糊表述（"应该" → MUST / SHOULD 并给出理由）。

**Version**: 1.1.0 | **Ratified**: 2026-09-17 | **Last Amended**: 2026-09-22
