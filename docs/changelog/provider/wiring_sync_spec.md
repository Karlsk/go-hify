# 组合根接线 + SyncModels 真实实现 + 手测文档 spec

> 状态：**spec 已定，待实现**（2026-08-19 澄清确认：Sync 落点 1A = provider/service 直连；Prober 暴露 2A = New 返回二元组）。
> 前置：handler 12 端点已落地（handler_spec.md）、StartProber/probeOne 已落地（db_model.md §2.3/§2.3.1）。

## 0. 关键前提澄清（已与用户确认）

- **eino 不提供模型列表**：eino 的 `model.ChatModel` 组件契约只有 `Generate`/`Stream`，无 discovery 概念；vendor SDK 路径要写 4 条集成且 openai_compatible 无 SDK。模型列表同步与连通性探测同类（元数据 GET、不产生 token 消费、同一批端点），沿用 prober 直连模式。
- **错误码前置**：`ErrServiceUnavailable`（errs，503）复用为"上游获取失败"；本批**不新增**哨兵错误。

## 1. service 构造签名小改（两处）

### 1.1 NewProviderService 返回二元组（澄清 2A）

```go
// Prober 定时健康探测窄接口：仅组合根使用（go Prober.StartProber(appCtx) 随关停退出）；
// 业务消费方一律用 api.ProviderService，不需要本接口。
type Prober interface {
    StartProber(ctx context.Context)
}
var _ Prober = (*providerService)(nil) // 编译期断言

func NewProviderService(store Store, cm cacheManager, masterKey []byte) (providerapi.ProviderService, Prober)
```

- 实现不变，仅返回 `svc, svc`（同一实例，两个视角）。
- 调用点更新：service 测试 `newTestService` 等（`ps, _ := NewProviderService(...)`，Prober 侧由 StartProber 既有测试覆盖）。

### 1.2 NewModelService 增 masterKey 与 probe（Sync 所需）

```go
type modelService struct {
    store  Store
    cm     cacheManager
    master []byte      // 同 providerService：API Key 解密（32B，构造期 panic 校验）
    probe  probeClient // 模型列表 GET（复用 NewProbeClient：共享 LLM transport + 10s 超时）
}
func NewModelService(store Store, cm cacheManager, masterKey []byte) providerapi.ModelService
```

## 2. 组合根接线 + graceful shutdown（internal/app/server.go）

### 2.1 provider 接线（替换 §③ 的 TODO 注释块）

```go
providerStore := providerstore.New(gormDB)
cm := cache.New(rdb, cache.DefaultConfig()) // provider-cache（TTL 30min + 写时删 key）
providerSvc, prober := providersvc.NewProviderService(providerStore, cm, cfg.Provider.MasterKey)
modelSvc := providersvc.NewModelService(providerStore, cm, cfg.Provider.MasterKey)
go prober.StartProber(appCtx) // 定时健康探测：随 appCtx 取消退出（间隔 60s，包内常量）
...
providerhandler.New(providerSvc, modelSvc).RegisterRoutes(v1)
```

- llmManager 仍 `_ = llmManager` 保留，注释更新：消费方只剩 chat（provider 探测/同步走直连轻量 GET，不经 Manager）。
- demo 模块保留不动（参照实现，移除是独立决策）。

### 2.2 graceful shutdown（替换 §⑤ 的 `r.Run`）

```go
appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: ":" + cfg.Server.Port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
errCh := make(chan error, 1)
go func() { errCh <- srv.ListenAndServe() }()

select {
case err := <-errCh: // 启动即失败（端口占用等）
    return err
case <-appCtx.Done(): // SIGINT/SIGTERM：先停 HTTP（5s 等在途请求），再关连接池
    slog.Info("shutting down")
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := srv.Shutdown(shutdownCtx); err != nil {
        slog.Warn("http shutdown incomplete", "err", err)
    }
}
// 关闭顺序：HTTP 已停 → prober 随 appCtx 退出（在途探测单轮 ≤10s，极端未收尾仅记一条错误日志）→ 连接池 → 日志（既有 defer logging.Close）
if err := gormDB.Close(); err != nil { slog.Warn("close db", "err", err) }
if err := rdb.Close(); err != nil { slog.Warn("close redis", "err", err) }
return nil // ErrServerClosed 不上抛：Shutdown 触发的返回是正常路径
```

- 范围外：`/health` 探活升级（PG/Redis 连通性 + degraded 503）仍是 TODO，本批不动。

## 3. SyncModels 真实实现（internal/provider/service/sync.go 新文件）

### 3.1 拉取（复用 prober 基建）

- `probeTarget(kind, baseURL, apiKey)` 直接复用（URL/认证头/默认 base 与探测完全一致）。
- client 用 `s.probe`（10s 超时 JSON client）；`io.LimitReader(probeModelLimit)` 读 body。
- 解密失败（主密钥轮换后的旧密文）→ 返回 `fmt.Errorf("%w: API Key 解密失败（主密钥可能已轮换，请重新录入）", errs.ErrInternal)`（手动同步响亮失败；细节进日志、前端只见 INTERNAL_ERROR）。
- HTTP 非 2xx（含 401/403/429/5xx）或网络错误 → `fmt.Errorf("%w: 模型列表获取失败（%v）", errs.ErrServiceUnavailable, 原因)`（503，上游暂不可用）；响应体解析失败（HTML 错误页等）同路径。

### 3.2 解析（每 kind 归一为 `[]discoveredModel{ModelID, DisplayName}`）

| kind | 端点 | 响应形态 | ModelID | DisplayName |
|---|---|---|---|---|
| openai / openai_compatible | `{base}/models` | `{"data":[{"id":...}]}` | `id` | 无 → 取 `id` |
| claude | `{base}/models` | `{"data":[{"id":...,"display_name":...}]}` | `id` | `display_name`（缺省取 id） |
| gemini | `{base}/models` | `{"models":[{"name":"models/gemini-...","displayName":...}]}` | `name` 去掉 `models/` 前缀 | `displayName`（缺省取 id） |
| ollama | `{base}/api/tags` | `{"models":[{"name":"llama3:latest","model":"llama3"}]}` | `name`（**含 tag**，API 调用按全名） | `model`（基础名，缺省取 name） |

- **限制（注释 + 手测文档记录）**：gemini 只取第一页（不跟 pageToken，默认 ≤50 条）；列表不含能力元数据 → 新增行固定 `capability=chat`（embedding 模型人工补录）；列表不含价格/窗口 → 这些列 sync 永不填充。

### 3.3 落库规则（只增改不删，不覆盖手编字段；review 修正后为批量 diff 形态）

一次 `ListModelsByProvider` 载入库内全表建 `model_id → Model` 索引，内存 diff 后按变化集发写（消逐条查重的 N+1：300 模型从 ~600 次往返降到 1 读 + k 写）：

| 库内状态 | 动作 | 计数 |
|---|---|---|
| 无行 | 收进 `toInsert`，`CreateModels` 批量插入（单条多 VALUES）：`Name=DisplayName`、`Capability=chat`、`Enabled=true`、`Source=discovered`、`ExtraParams={}` | Added += 批量成功行数 |
| 有行且 `source=discovered` 且 name 与上游不一致 | `UpdateModelName` 列级 UPDATE：只写 `name`/`updated_at`，WHERE 限定 `source='discovered'`——不用全列 Save，防并发手工编辑被旧快照覆盖 | Updated++ |
| 有行且 `source=manual` | 完全跳过（手编行受保护，含 display_name） | — |
| 有行且 source=discovered 且 name 一致 | 不动 | — |

- 非事务（无跨行原子需求）；批量插入撞 `uq_models_provider_model_id`（与手工创建竞态，23505 整批失败）→ 退回逐条插入、冲突行按"已存在"跳过，不报错。
- `Added+Updated > 0` 时失效 `detail:{providerID}` 缓存（失效矩阵：model 增删改失效 detail；list 是 providers 快照，不涉及）。
- 返回 `ModelSyncResultSchema{Added, Updated}`；列表为空数组是正常结果（返回 0/0）。

## 4. 手测文档（docs/testing/provider-manual-test.md，仿 demo-crud-smoke.md）

章节：

1. **环境准备**：`.env`（`PROVIDER_MASTER_KEY` 用 `openssl rand -base64 32` 生成）、`make migrate-up`、本地起 PG/Redis、`go run ./cmd/hify`、`GET /health` 冒烟。
2. **登录链**：register → login（`curl -c jar` 存 `hify_session`）→ me；后续请求 `-b jar`。
3. **Provider CRUD 走查**：创建（openai / openai_compatible 带 base_url / ollama 三形态）、列表（分页 + kind/enabled 筛选）、详情（models=[]、health=null 起步）、更新（改名冲突 409、轮换 api_key 后 rotated_at 变化、enabled=false）、test-connection。
4. **Model 手动管理**：POST /models（embedding 必填 dim 的 400 分支）、GET/PUT（body 带 `"id":999` 不覆盖路径 id 的回归样例）、DELETE、`GET /providers/:id/models` 分页。
5. **Sync（离线 mock 桩）**：python3 一行 HTTP server 模拟 `/v1/models`（固定 JSON），建 openai_compatible provider 指向它 → sync 断言 `{"added":2,"updated":0}` → 改手编字段再 sync 断言保护 → 桩返回 401 断言 503；附真实 key 路径（可选）。
6. **定时探测观察**：mock 桩先 200 后 500 → 手动 test-connection 后观察 `degraded`，~3 分钟（3 轮失败）后 `down`；恢复 200 后回 `up`（时间线：轮询 60s × 阈值 3）。
7. **错误分支回归表**：404（坏 id）/ 409（name、model_id 冲突、in-use 删除需手工在 agents 表插引用行或标注跳过）/ 400（binding、跨字段、坏 uri）/ 503（sync 上游失败）/ 204 无 body。
8. **回归清单 checklist**（勾选式，供后续批次回归）。

## 5. 测试计划

- **service（sync_test.go 新增）**：5 kind 解析表驱动（gemini 前缀剥离、ollama tag、claude display_name）；added/updated 计数；manual 行保护；discovered 行 name 刷新；23505 竞态跳过；解密失败 → ErrInternal；HTTP 401/网络错误 → ErrServiceUnavailable；空列表 0/0；有改动才失效 detail 缓存（miniredis 断言 key 消失）。
- **service 构造签名回归**：newTestService 等调用点适配二元组。
- **server.go**：本批不写 app 包测试（main 装配层，组合根测试性价比低，惯例由手测文档覆盖冒烟）。
- **既有回归**：model_service_test 的 `Pending503` 用例删除（替换为真实实现用例）；handler 包 8 端点 sync 用例的 503 断言改 200 形态（fake 层不感知实现）。

## 6. 范围外（defer，显式记录）

- `/health` 探活升级（PG/Redis 连通性 + degraded 503）。
- gemini sync 分页（pageToken 跟取）。
- sync 结果的模型能力识别（上游列表不含该元数据）。
- 前端对接（用户明确：单独任务）。
