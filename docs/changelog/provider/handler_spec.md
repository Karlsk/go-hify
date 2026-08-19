# Provider Handler 层落地 spec（"Controller" 批次）

> 状态：**spec 已定，待实现**（2026-08-19 澄清确认：含模型路由一起做；不做组合根接线——与 StartProber 接线同留后续批次）。
> 任务原话是 Java 术语（ProviderController / Result / @Valid），本文 §1 先做概念转换，再定义路由与行为。
> 前置事实：api 契约 6+7 个方法、schema / 哨兵 / binding tag、respond 平台包（Result 信封 + Bind* + OK/Created/OKWithOffset/FailFromSentinel）、demo 模块参照实现均已就位；handler.go 当前为占位文件。

## 1. Java → Go 概念映射

| Java / Spring | Hify Go |
|---|---|
| `ProviderController`（`@RestController`） | `internal/provider/handler` 包：`Handler` struct + `RegisterRoutes(rg *gin.RouterGroup)` |
| `Result<T>` 统一返回体 | `respond.Result` 信封；`respond.OK / Created / OKWithOffset / Fail*` 写出 |
| `@Valid` 入参校验（+ `MethodArgumentNotValidException` → 400） | `respond.BindJSON / BindQuery / BindUri` 合一步：binding tag 管字段格式 + `Req.Validate()` 管跨字段；失败自动 400 + `details.fields` |
| `@PathVariable Long id` | `uri:"id"` tag + `BindUri`（`uint64`；非数字 / 缺失自动 400） |
| `@RequestBody DTO` | api 包 Req（已存在，`json` + binding tag 已备） |
| `@Autowired ProviderService` | `New(svc ...)` 构造注入；组合根把 service 实现喂给 handler |
| `@GetMapping` 等方法注解 | `g.GET / POST / PUT / DELETE` 显式注册 |
| 204 No Content（`void` 删除） | `c.Status(http.StatusNoContent)`（无响应体，符合接口规范） |

## 2. Handler 结构

```go
// handler 持本模块两个 api 接口（均为组合根注入 service 实现）；
// 一个 Handler 挂全部 12 条路由，避免拆两个类型带来的注册顺序心智负担。
type Handler struct {
    providers providerapi.ProviderService
    models    providerapi.ModelService
}

func New(providers providerapi.ProviderService, models providerapi.ModelService) *Handler
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) // providers 组 + models 组
```

规则：handler 只 import 本模块 `api` + `platform/respond` + gin + net/http（CLAUDE.md《模块内部结构》）；绑定函数命名 create / get / list / update / delete + 动作名 testConnection / syncModels；一个绑定函数只调一个接口方法。

## 3. 路由总表（12 端点，均在 `/api/v1` 下、受 auth 中间件保护）

| # | 路由 | 方法与绑定 | 成功响应 | data 载荷 |
|---|---|---|---|---|
| 1 | `POST /providers` | create（BindJSON） | 201 | ProviderSchema |
| 2 | `GET /providers` | list（BindQuery：page/page_size/kind/enabled） | 200 + OffsetMeta | `[]ProviderSchema`（空为 `[]`） |
| 3 | `GET /providers/:id` | get（BindUri） | 200 | ProviderDetailSchema（models + health 聚合） |
| 4 | `PUT /providers/:id` | update（**两段绑定**：先 GetProviderReq 绑 uri → `req.ID` 赋值 → BindJSON） | 200 | ProviderSchema |
| 5 | `DELETE /providers/:id` | delete（BindUri） | 204 无 body | — |
| 6 | `POST /providers/:id/test-connection` | testConnection（BindUri） | 200 | ConnectionTestSchema |
| 7 | `GET /providers/:id/models` | listModels（BindQuery：uri id + 分页） | 200 + OffsetMeta | `[]ModelSchema` |
| 8 | `POST /providers/:id/models/sync` | syncModels（BindUri） | 200（service 占位期 503） | ModelSyncResultSchema |
| 9 | `POST /models` | createModel（BindJSON，provider_id 在 body） | 201 | ModelSchema |
| 10 | `GET /models/:id` | getModel（BindUri） | 200 | ModelSchema |
| 11 | `PUT /models/:id` | updateModel（两段绑定同 #4） | 200 | ModelSchema |
| 12 | `DELETE /models/:id` | deleteModel（BindUri） | 204 无 body | — |

**嵌套 vs 平铺的理由**（由既有 Req 形状反推，零契约改动）：

- **集合作用域挂 provider 下**：`ListModelsReq.ProviderID` 是 `uri:"id"`（为嵌套 list 设计）；sync 天然按 provider。
- **条目作用域平铺 `/models`**：`GetModelReq / DeleteModelReq` 无 ProviderID 字段（纯 model id）；`CreateModelReq / UpdateModelReq` 的 `provider_id` 在 body（`binding:"required"`）——POST /models 直接 BindJSON 即满足，不引入「路径 id 与 body provider_id 不一致」的仲裁问题。
- 两段绑定只在两个 PUT 出现（uri id + body），与 demo 模式一致：`UpdateProviderReq.ID / UpdateModelReq.ID` 无 binding tag 且带 `json:"-"`——后者是防覆盖关键（审查修正）：`encoding/json` 对无 tag 字段按字段名大小写不敏感匹配，裸 `ID` 字段会被 body 的 `{"id":999}` 悄悄改写、令路径失效；`json:"-"` 保证路径 id 权威。demo 的 `UpdateReq.ID` 同步修正。

## 4. 错误映射（handler 显式 `errors.Is` → `respond.Fail`，其余走 `FailFromSentinel`）

| 哨兵 | HTTP | 触发端点 | message（人类可读） |
|---|---|---|---|
| `ErrProviderNotFound` | 404 | 3/4/5/6/8/9（model create 的 provider_id 不存在也翻它） | 提供商不存在 |
| `ErrProviderNameConflict` | 409 | 1/4 | 提供商名称已存在 |
| `ErrModelNotFound` | 404 | 10/11/12 | 模型不存在 |
| `ErrModelIDConflict` | 409 | 9/11 | 同一提供商下模型标识已存在 |
| `ErrModelInUse` | 409 | 5（provider 删被挡）/ 12（model 删被挡） | 模型已被 Agent / 知识库引用，请先解绑 |
| `errs.ErrValidationFailed` | 400 | 经 `FailFromSentinel` 自动映射（见 §5-1） | 参数校验失败 + 具体原因 |
| `errs.ErrServiceUnavailable` | 503 | 8（sync 占位期） | 功能暂未开放 |
| 未识别错误 | 500 | `FailFromSentinel` 兜底：`INTERNAL_ERROR` + trace_id，细节进日志 | — |

绑定 / 跨字段校验失败（400 + `VALIDATION_FAILED` + details.fields）由 `respond.Bind*` 统一写出，handler 不重复处理。

**testConnection 的状态语义**：HTTP 200 = 「探测已执行」；探测结果失败（不可达 / 401 / 解密失败）是**业务结果**不是 HTTP 错误——`data.success=false` + `error_message`，前端据此渲染红灯。只有 NotFound（404）与写库失败（500）是 HTTP 错误。

## 5. service 层配套小改（本批仅有的两处，各一行）

1. **Update 的 `ValidateWithKind` 错误包装**：openai_compatible 清空 base_url 等校验只能发生在 service（handler 拿不到库内 kind），当前返回裸 Validate 错误 → handler 落 500。改为 `fmt.Errorf("%w: %s", errs.ErrValidationFailed, err)`，`FailFromSentinel` 自动映射 400。其余 `req.Validate()` 调用点输入与 handler 预校验完全相同（不可达差异），不动。
2. **SyncModels 占位错误改 503**：`errNotImplemented`（未识别 → 500 误导 + ERROR 日志噪音）改为 `fmt.Errorf("%w: ...", errs.ErrServiceUnavailable)`，删除 `errNotImplemented` 变量。路由先挂上，模型发现批次（platform/llm）落地后自然变 200。

## 6. 测试计划（httptest，仿 demo/handler/handler_test.go）

- `fakeSvc`：内存版实现 ProviderService + ModelService 全方法 + 可注入错误 / 可查调用参数。
- 用例清单（表驱动优先）：
  - 每端点 happy path：状态码 + 信封结构（success/data/error/meta）+ `Cache-Control: no-store`；
  - 校验失败 400 三来源：binding required / 跨字段 Validate（如缺 api_key 的 claude、坏 kind）/ 坏 JSON；bad uri id（`/providers/abc`）400；
  - 哨兵映射：404（provider / model）、409（name / model_id 冲突、in-use 删除）、sync 占位 503；
  - 未识别错误 → 500 + `INTERNAL_ERROR`；
  - 列表：OffsetMeta 字段、空列表 `data:[]` 非 null、page_size>100 拒 400；
  - update 两段绑定：uri id 正确进入 req.ID；
  - testConnection：`success=false` 仍 HTTP 200；
  - delete 204 且无响应体；
  - 详情聚合：data.models 数组 + data.health 对象字段存在。
- 覆盖率 ≥ 80%（handler 包 + 改动后的 service 包回归）。

## 7. 范围外（defer，显式记录）

- **组合根接线**：server.go 补 `providerstore.New → cache.New(rdb, DefaultConfig) → NewProviderService / NewModelService → providerhandler.New(...).RegisterRoutes(v1)`；与 `go StartProber(appCtx)`（依赖 signal graceful shutdown）同批。
- **SyncModels 真实实现**：依赖 platform/llm 模型发现能力，独立批次。
- 前端对接（ProviderList 页面已有 mock，后续换真接口）。
