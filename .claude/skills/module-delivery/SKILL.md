---
name: module-delivery
description: Hify 新业务模块的完整交付流程——咨询定稿、数据模型、四层实现（model/store/api/service/handler）、组合根装配、前端对接、全流程验收。当用户要开发/新增/交付一个完整业务模块（如 agent、rag、workflow 模块）时使用；已有模块内的单个功能点改动不适用本流程。
argument-hint: [模块名，如 agent]
---

# Hify 模块交付流程

以 provider 模块交付过程沉淀的标准流程。总原则：**小步走，每步有产出物 + 验证，验证不过不进下一步**；标 ⏸ 的决策点必须停下来等用户明确确认，不得自行推进。

各层的职责边界、代码模板、命名规范以仓库 CLAUDE.md《代码组织规范》为准，本流程不重复，只管顺序、产出物、验证与坑。

---
## 总体原则
- 每步有明确产出物，编译或验证通过再进下一步
- 关键设计决策必须等用户确认，不自行拍板
- 先咨询后实现，先后端后前端
---

## Step 1 咨询与设计（不写代码）⏸

梳理三件事：技术选型、数据模型（表结构 + 关系 + 状态机）、边界问题（跨模块依赖、缓存策略、外部调用）。

- **产出物**：
1. 候选方案对比表（2-3 个方案，标明取舍）
2. 数据模型草稿（表名、核心字段、关联关系）
3. 接口清单（Method + Path + 简要说明）
4. 决策记录 `docs/changelog/<module>/`（数据模型决策 + spec，如 db_model.md、xxx_spec.md）；涉及表结构时同步更新 `docs/design/data-model.md`

**流程**：
1. 分析业务需求，列出候选技术方案
2. 给出推荐方案及理由
3. 提出需要用户决策的问题（如：是否需要软删除？JSON 字段还是关联表？）

- **验证方式**：向用户复述决策摘要（表、字段要点、依赖方向、边界取舍，接口设计），**等用户确认"定了"才进 Step 2**
- **注意**：
- 这一步的产出决定后面所有步骤，宁慢勿快；边界问题（缓存失效矩阵、并发写、外部调用放哪层）此时不问清楚，后面返工成本翻倍
- 高频写入的表（如 provider_health）不用 `db.BaseMutable` / `db.BaseSoftDelete`——自声明精简表头（provider_id 主键）或用 `db.BaseAppendOnly`，避免 updated_at / 软删除的写放大
- 敏感字段（如 api_key）不能出现在任何响应 Schema 中——用 `has_api_key: bool` + `api_key_masked`（仅详情）代替，密文与明文都永不外泄
- JSONB 写入前兜底：nil map / nil slice 用 nonNilAny 转空集合（防写成 json null）；响应侧列表字段空返 `[]` 不返 null

---

## Step 2 数据模型落地

- **目标**：数据库 DDL 与设计对齐。

- **产出物**：
  - `migrations/NNNNN_<module>.sql`：标准表头、text+CHECK 枚举、每个外键建索引、COMMENT ON 全覆盖（照 CLAUDE.md《建表执行清单》逐条过）
  - `internal/<module>/service/model.go`：GORM 实体，按表性质 embed `db.BaseMutable` / `db.BaseAppendOnly` / `db.BaseSoftDelete`；**禁止 GORM AutoMigrate**
- **验证方式**：`make migrate-up` && `make migrate-status`；`go build ./...`
- **注意**：append-only 表不要 updated_at；探测/运行时状态独立成表（如 provider_health），与配置表隔离——配置走缓存，状态随时写

---

## Step 3 数据层：service/model.go + store/store.go
- **目标**：ORM 层与数据库表对齐。
- **产出物**：
  - `service/model.go`：核对并补齐 Step 2 落地的 GORM 实体（字段与 DDL 一一对应、`TableName`、mixin embed）
  - `service/service.go`：先落 Store 接口定义（定义在消费方、store 实现它——依赖倒置）与 `New` 构造骨架
  - `store/store.go`：实现 Store 接口 + 编译期断言 `var _ <module>svc.Store = (*Store)(nil)`；`WithTx` 事务包装
- **验证方式**：`go build ./...`；`go test ./internal/<module>/store/...`（sqlmock）
- **注意**：错误原样上抛（可 `%w` 加上下文），不做业务翻译；PG 错误码（23505 唯一 / 23503 FK）翻译留到 Step 5 service 层；只操作本模块声明的表

---

## Step 4 契约层：api/schema.go ⏸
- **目标**：定义请求/响应对象，隔离内部实体。

- **产出物**：
  - `api/schema.go`：Req/Schema 定义——id 字符串化、列表字段空返 `[]` 不返 null；binding tag 管字段格式、`Validate()` 管跨字段规则
  - `api/errors.go`：哨兵错误，code 与 CLAUDE.md 错误码表一一对应
- **验证方式**：`go test ./internal/<module>/api/...`——常量钉住测试（防 DB CHECK / 前端枚举漂移）+ 序列化安全测试（响应永不携带密文/密钥）
- **⏸ 决策点**：契约是前后端共同接口，评审通过再写实现

- **注意事项**：
   - 响应 Schema 不能暴露 auth_config / password 等敏感字段（对齐 Step 1 注意）
   - 列表结果定义 `XxxListResult`（items + page/page_size/total），handler 经 `respond.OKWithOffset` 写 meta（游标分页用 `OKWithCursor`）——`PageResult` 是前端 TS 类型，后端没有这个名字
   - 响应信封一律由 `respond.OK*` / `respond.Fail*` 包装，handler 不手搓 Result 结构
---

## Step 5 接口与业务层：api/api.go + service/service.go
- **目标**：实现核心业务，接口与实现分离。
- **产出物**：
  - `api/api.go`：XxxService 接口定义，签名固定 `(ctx context.Context, req XxxReq) (*XxxSchema, error)`
  - `service/service.go`：实现 api 接口——CRUD + 模块特有能力；schema↔model 转换（toSchema/toModel）、哨兵翻译（含 PG 23505/23503）、事务边界、Cache-Aside 写时删 key
- **验证方式**：`go test ./internal/<module>/service/... -count=1` + `go test -race`（stub 本模块 Store 接口）
- **注意**：
- 依赖外部系统（LLM/探测）的能力先声明 `errNotImplemented`，handler 映射 503，后续批次替换；事务内禁外部调用（见踩坑清单 5）
- 跨模块调用只 import 对方 internal/<domain>/api 包、面向 api.XxxService 接口编程；禁止 import 其他模块的 service（拿 model）或 store 包
- 外部 HTTP 调用（如 LLM API 连通性测试）必须设超时
- 并发外部调用（批量探测、同步等）用 goroutine per item + WaitGroup/errgroup 收尾（对齐 provider StartProber），个位数量级无需线程池；对供应商的并发上限由 platform/llm 的 bulkhead semaphore 统一管——CLAUDE.md 禁止用 worker pool 限制 LLM 并发

## Step 6 HTTP 层：handler/handler.go

- **目标**：暴露 REST 接口，只做参数校验和 Service 调用。
- **产出物**：RegisterRoutes + 薄绑定函数（create/get/list/update/delete + 动作端点）；哨兵 `errors.Is` → 状态码，兜底 `respond.FailFromSentinel`
- **验证方式**：`go test ./internal/<module>/handler/...`（httptest 走绑定函数）——路由此时尚未挂上 gin，curl 冒烟留到 Step 7、全量走查在 Step 8

- **注意**：
1. Update 端点两段绑定 + `json:"-"`（见踩坑清单 7）；一个绑定函数只调一个接口方法
2. 只调用本模块 api 接口（Service），不写业务逻辑，不直接操作 store

---

## Step 7 组合根装配

装配在 `internal/app/server.go`，沿依赖清单 store → service → handler 注入。

- **产出物**：server.go 装配代码 + 路由注册；后台任务（如 StartProber）同处接线
- **验证方式**：
1. `go build ./...` && `go vet ./...`；`make start` 启动无报错；`curl /health`
2. 冒烟一条确认路由已挂上（端点需登录 cookie，获取方式见 Step 8 手测文档；全量 curl 走查在 Step 8）：

```bash
# 列表冒烟——查询参数是 page_size，不是 pageSize
curl -s 'http://localhost:8080/api/v1/{resource}?page=1&page_size=10' | jq .
```

---

## Step 8 后端逐端点验证 ⏸

- **产出物**：手测文档 `docs/testing/<module>-manual-test.md`（每端点的 curl 命令 + 预期响应 + 边界场景）
- **验证方式**：curl 走全量端点（成功 / 校验失败 / 哨兵错误 / 越权边界）；日志观测点（启动/退出/轮询汇总）
- **⏸ 决策点**：后端验收通过、用户确认后才进前端

## Step 9 前端 API 文件
- **目标**：封装后端接口，定义 TypeScript 类型。
- **产出物**：`web/src/api/<module>.ts`

- **内容**：
   - 请求/响应类型定义（与后端 schema 字段对齐）
   - 导出各接口方法（使用 `request.ts` 的 get/post/put/del）

- **验证方式**：`cd web && npm run type-check`（vue-tsc）通过；类型字段与后端 `api/schema.go` 逐一对齐
- **注意**：
   - 前端 `request.ts` 拦截器会自动解包 `response.data.data`，API 方法的返回类型直接写业务数据类型，不需要包 `Result`
   - 列表接口返回类型写 `PageResult`（包含 list/total/page/pageSize），对应后端解包后的 `data` 字段
   - 后端 id 序列化为字符串（防 JS 2^53 丢精度），行数据回传前用 `Number()` 转数值外键（见踩坑清单 8）

---

## Step 10 前端页面对接
- **目标**：页面从 mock 数据源切换到真实 API。
- **产出物**：
  - 对应 view 改造——列表数据源、表单提交、删除/动作操作全部换成 Step 9 的 API 方法；mock 数据源代码清理干净
  - 更新 `views/{module}/XxxList.vue`

- **流程**：
1. 把 HifyTable 的 `api` prop 换成真实 API 方法
2. 表单提交换成 create/update API
3. 删除换成 delete API + useConfirm
4. 按需添加操作按钮（如测试连接）
5. 按需添加状态列（健康状态、关联数量等）

- **验证方式**：`make start`（dev 热重载）后浏览器过一遍页面；网络面板确认请求/响应契约
- **注意**：
   - 视约定见 web/README.md；交互变更同步 design-system.md（token 只增不改）
   - Vite 代理：`/api` → `http://localhost:8080`，前端 baseURL 设为 `/api`，后端路径 `/api/v1/xxx` 完整保留
   - 前端 `env.d.ts` 不要写 `declare module '*.vue' { ... }`，会覆盖 Volar 的真实类型推断，导致组件 ref 的 expose 方法找不到

## Step 11 完整验收 ⏸

后端 curl + 浏览器全流程（含登录态、创建→编辑→删除闭环），向用户报告验收结果，**等用户确认后收尾**（清理 TODO、补文档、准备提交拆分）。

## 踩坑清单（provider 模块 git 沉淀，实现时逐条对照）

1. **GORM `default:true` 零值陷阱（最痛）**：字段带 `default:true` tag 时，显式写入 `false`（零值）会被 GORM 替换成解析后的默认值——sync 导入 Enabled=false 全部变启用。**布尔/数值默认值 tag 一律不加**，创建路径显式设值，DB 列 `DEFAULT` 只作直插 SQL 兜底。修复必须带回归测试（断言 Create 后结构体字段仍为 false，buggy 代码下测试应 RED）。
2. **批量导入类操作的默认值要保守**：sync 导入的模型默认 `enabled=false`（待启用），用户按需勾选；全量默认启用会放出几十上百个不想用的模型。列级更新（如 UpdateModelName）不碰 enabled，防后续 sync 把用户勾选打回。
3. **列表聚合禁止 N+1**：列表页要健康状态/计数等聚合列时，分页后对**当页 id 集合**发 IN 批量查询（`ListXxxByIDs` / `CountXxxByIDs`），不逐行查。
4. **随时被写的列不进缓存**：探测 60s 写库，health 进配置缓存必读到旧值——高频写列从缓存载荷剔除、当页现读，写时删 key 的失效矩阵少一条边。
5. **事务内禁外部调用**：探测 = 解密 → HTTP probe 在事务外，事务内只做 `FOR UPDATE` 锁读 → 状态机纯函数 → upsert（`ON CONFLICT DO UPDATE`），防并发 fail_count 计数竞态（两个并发都读 n 都写 n+1）。
6. **配置类故障不记业务故障**：Key 解密失败（主密钥轮换）不发请求、不动 health 表，以失败结果返回提示重新录入；按 provider 去重只 WARN 一次，防定时任务刷屏。
7. **Update 端点两段绑定 + `json:"-"`**：先 BindUri 拿路径 `:id` 再 BindJSON body；Update 请求的 ID 字段加 `json:"-"`，否则 body 的 id 键会大小写不敏感匹配覆盖路径值。
8. **前端外键转数值**：API 的 id 是字符串（防 JS 2^53 丢精度），前端行数据回传前用 `Number()` 转数值外键，否则查询/提交类型不匹配。
9. **占位实现返回 503 而非 500**：`errNotImplemented` 统一映射 `errs.ErrServiceUnavailable`（503），不误导排障。
10. **外部协议细节**：Claude 探测无 key 也必带 `anthropic-version` 头，否则 400 误判为不可达；探测日志带 source（manual/scheduled）区分人工与定时，轮末汇总 + 启停日志是接线与优雅关闭的最直接证据。
