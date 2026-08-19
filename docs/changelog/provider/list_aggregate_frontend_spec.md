# Provider 列表聚合列 + 前端真实 API 对接 spec

> 状态：**已实施**（2026-08-19）。澄清确认：① 健康状态 / 模型数数据源 = **后端列表接口扩展**（分页窗口批量现读，2 条 IN 查询，非 N+1）；② 模型抽屉一并接真实 API（后端 5 个模型端点已就绪）。
> 前置：handler 12 端点 + sync + 定时探测均已落地（wiring_sync_spec.md）；前端骨架与组件约定见 web/README.md。

## 1. 需求（任务原话归纳）

ProviderList.vue 从 mock 换真实 API：`api/provider.ts` 建全部方法；表格 / 弹窗 / 删除 / 测试接通；列表新增**健康状态**列（UP 绿 / DOWN 红 / DEGRADED 黄 / UNKNOWN 灰 + 最近延迟 ms）、**模型数**列（点击展开模型列表）、**操作**列含连通性测试。

## 2. 后端：列表响应聚合扩展

### 2.1 契约（api/schema.go）

```go
type ProviderListItemSchema struct {
    ProviderSchema
    Health            *ProviderHealthSchema `json:"health"`              // 无行（从未探测）为 null
    EnabledModelCount int32                 `json:"enabled_model_count"` // models.enabled=true 计数
}
```

`List` 返回 `ProviderListResult{Items []ProviderListItemSchema, Page, PageSize, Total}`；`ProviderDetailSchema` 注释修订——detail 聚合（models + health）与列表聚合是两个形态，原「列表不聚合（N+1 无意义）」表述改为「聚合走分页窗口批量现读」。

### 2.2 填充（service.withAggregates，两个 List 分支共用）

- 缓存载荷**不变**（仍 `[]ProviderSchema`）：health 是 60s 一轮探测的现势数据，进 30min TTL 缓存必读到旧值——与「detail 缓存剔除 health、Get 现读」同一先例。
- 流程：`filterPaginateProviders` 出本页 → ID 字符串 parse 回 uint64（自格式化十进制，安全）→ 两条批量查询：`ListHealthByProviderIDs`（主键 IN，只回存在行，无行归 nil）、`CountEnabledModelsByProviderIDs`（`GROUP BY provider_id` 聚合计数，map 无键 = 0）→ 回填。页大小 ≤100，两查询各一次，无逐行 N+1。
- 空 ids 短路：store 两方法 `len(ids)==0` 直接返回，不发 SQL。

## 3. 前端

### 3.1 api/provider.ts（新建，类型与请求方法唯一事实源）

- 字段名直接用后端 snake_case（对齐 ResultMeta/PageQuery 先例）；`types/index.ts` 的 `ProviderType` mock 联合类型删除。
- **body 外键数值陷阱**：响应 id 是字符串（`json:"id,string"`），但请求 body 的 `provider_id` 是数值（Go uint64 无 `,string` tag，传字符串 400）——回传处 `Number()` 转换。
- 13 个方法：providers 5（list/create/update/delete/testConnection）+ models 5（list/sync/create/update/delete）+ getModelList 带 providerId 路径。

### 3.2 ProviderList.vue

- 列：名称 / 类型（5 kind tag）/ **健康状态**（tag + 延迟小字 `--hf-text-3`）/ **模型数**（link 按钮，唯一入口开抽屉）/ Base URL / 状态（启用/停用）/ 创建时间（RFC3339 → `YYYY-MM-DD HH:mm` 本地时区）/ 操作（测试·编辑·删除）。
- 测试按钮：`testingId` per-row loading；HTTP 200 内 `success` 字段分支——成功 toast `连接成功 · Xms · N 个模型`，失败 toast error_message；finally 刷新列表（探测已写 health）。
- 表单：kind 编辑禁用（后端 UpdateProviderReq 无此字段）；api_key 不回显明文（留空 = 保留 / 填写 = 轮换）；**PUT enabled 陷阱**——编辑表单加「启用」开关并随行状态回显，否则任何编辑都会把 provider 停用（Go bool 零值）；kind 必填 Key 规则用 `formKind` 镜像 ref（`@change` 同步）实现按 kind 分的动态 rules；兼容网关必填 base_url + http(s):// 前缀校验。

### 3.3 ProviderModelsDrawer.vue

- `ProviderRef` 收窄为 `{id: string; name: string}`（ProviderItem 结构化满足）；开真实分页（去掉 `:pagination="false"`）。
- 列：模型 ID / 能力 tag / 来源 tag（手动=info、自动=success）/ 上下文（tokens → K 取整）/ **启用开关** / 删除。
- 启停开关：`v-model` 先翻转视觉态 → PUT 全量回传（`UpdateModelData` 整行可选列必须回传，否则清空；provider_id 走 `Number()`）→ 失败回滚视觉态（错误由拦截器弹）。同步发现的 embedding 行若缺 dim，PUT 会 400（validateModel），属已知边界。
- 同步：toast `同步完成：新增 X，更新 Y`；上游 503 由拦截器弹。
- 手动新增：名称留空取模型 ID（后端 name 必填）；**embedding 必填嵌入维度、chat 不得携带**（对齐 validateModel，`formCapability` 镜像驱动动态 rules；undefined 键不进 JSON body）；上下文 K tokens × 1024 存 tokens。

## 4. 测试与验证（全绿）

- 后端：`go test -race -count=1 ./internal/provider/...` 4 包 ok。新增：store 两查询 sqlmock（IN 正则 + 空短路 + GROUP BY 无表前缀坑）；service `TestProviderList_FillsAggregates`（up/down 混合 + enabled 计数 + 缓存命中分支同样填充）；memStore / handler fake 适配。
- 前端：`npm run type-check` + `npm run build` 通过（chunk >500kB 警告为 Element Plus 单包既有项）。
- 手测：provider-manual-test.md **§9 前端联调走查**（14 场景，含 PUT enabled 陷阱回归、开关失败回滚、拦截器统一弹错）。

## 5. 范围外（defer）

- 抽屉内模型**编辑**表单（价格 / 上下文 / 维度修改——启停开关已覆盖最高频操作）。
- kind / enabled 列表筛选 UI（后端 query 参数已支持）。
- 前端单测 / E2E 基建（本批零新增）。
- chat / agent 模块消费 `health` / `enabled_model_count`（无消费方，暂仅展示）。
