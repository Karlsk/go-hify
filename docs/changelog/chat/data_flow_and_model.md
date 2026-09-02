# Chat 对话引擎：全链路数据流与数据模型

> 状态：**设计定稿，DDL 已落库**（2026-09-02）。
> conversations / messages 随 migrations/00006、executions 随 00007 落库；
> 本文档记录全链路数据流、三张表字段明细与设计决策，作为 chat 模块实现的对照文档。
> 表归属总览见 [docs/design/data-model.md](../../design/data-model.md)；建表规范见 CLAUDE.md《数据库规范》。

## 1. 全链路数据流

场景：用户在控制台打开某个 Agent 的对话界面，输入「帮我查一下昨天的订单异常」点发送。

### 1.1 去程：从浏览器到 LLM 请求发出

```
┌─ P0 前端（浏览器）─────────────────────────────────────────────────────┐
│ fetch POST /api/v1/chat/stream                                         │
│   body: {conversation_id?, agent_id, message}                          │
│   credentials: "include"（带 hify_session cookie）                     │
│   Accept: text/event-stream                                            │
│   AbortController 挂在「停止生成」按钮和页面关闭上                       │
│   （不用 EventSource：它不能带 body 和 cookie）                          │
└──────┬─────────────────────────────────────────────────────────────────┘
       │ HTTPS 443
       ▼
┌─ P1 nginx ────────────────────────────────────────────────────────────┐
│ TLS 终结 → /api/* 反代 hify:8080                                        │
│ SSE 生死线：proxy_buffering off / proxy_http_version 1.1                │
│             proxy_read_timeout 300s / X-Accel-Buffering: no / 禁 gzip   │
└──────┬─────────────────────────────────────────────────────────────────┘
       ▼
┌─ P2 Gin 中间件链 ─────────────────────────────────────────────────────┐
│ Recovery（最外层兜底 panic）                                            │
│ → RequestID：生成 trace_id 注入 ctx（贯穿全程日志）                     │
│ → AccessLog（SSE 豁免）                                                │
│ → auth：hify_session cookie → Redis hify:session:{token}               │
│         查不到 → 401，流程到此结束                                      │
│         查到 → authctx 把 UserID/Username 注入 ctx                      │
└──────┬─────────────────────────────────────────────────────────────────┘
       ▼
┌─ P3 handler（chat/handler，薄绑定）───────────────────────────────────┐
│ BindJSON 绑定+校验 → 失败 400 信封                                      │
│ budget 检查：Redis 计数，新会话被拒 → 429（fail-open：Redis 挂则放行）   │
│ ⚠ 关键顺序：所有可能失败的检查都在「写 200 头之前」完成                   │
│ 写 SSE 头（text/event-stream / no-store / X-Accel-Buffering:no）+Flush │
│ → svc.Stream(c.Request.Context(), req)                                 │
│   这个 ctx 自带魔法：客户端断连时它会被自动 cancel                       │
└──────┬─────────────────────────────────────────────────────────────────┘
       ▼
┌─ P4 service 对话引擎（chat/service，编排核心）────────────────────────┐
│ ① 会话管理：conversation_id 缺省 → 建 conversations 行                  │
│    落用户消息到 messages（短事务，绝不持到流结束）                        │
│ ② 取 Agent 配置（经 agent 的 api 接口，Cache-Aside Redis 30min）：      │
│    模型 / 系统提示词 / max_context_turns / 绑定的工具 / 绑定的知识库     │
│ ③ 取 Provider 配置（经 provider 的 api 接口 ResolveLLMConfig）：        │
│    解密 API Key（主密钥来自 env）→ 只交给 platform/llm，不外泄          │
│ ④ 上下文拼装：                                                          │
│    system prompt + 最近 N 轮历史（messages）+ 当前消息                  │
│    + RAG：embedding（独立槽/5s 超时/失败降级直接跳过）                   │
│           → pgvector HNSW 相似度召回 top-k → 注入上下文                 │
│    + 工具定义：绑定的 MCP 工具 → 转成 LLM tool schema                  │
│ ⑤ 进入 Agent 循环 ═══════════════╗                                        │
└──────┬─────────────────────────────────╝                                │
       ▼
┌─ P5 platform/llm（每次 LLM 调用的弹性层）─────────────────────────────┐
│ 抢槽位：semaphore 16/供应商，5s 抢不到 → fail-fast 503                   │
│ 过熔断器：连续 5 次最终失败已打开 → 秒拒「供应商暂不可用」               │
│ 重试循环（≤3 次尝试，仅首 token 前）：                                   │
│   attempt ctx = WithTimeoutCause(overall, TTFT 30s, ErrTTFT)            │
│   eino.Stream() → 定制 transport 发 HTTP（见下）                        │
│   首 chunk 到达 → TTFT 失效 → 切 idle 看门狗（每 chunk 重置 30s）        │
│   overall 5min 最外层兜底「慢滴流」                                     │
│   失败且可重试 → 指数+满抖动退避；429 尊重 Retry-After（封顶 10s）       │
│   首 token 后失败 → 绝不重试，上抛                                      │
│ 错误分类：adapter 把各家错误翻译成统一七类（决定重试/熔断/给用户看什么）  │
└──────┬─────────────────────────────────────────────────────────────────┘
       │ HTTP 长连接（keep-alive 复用，MaxIdleConnsPerHost=16 对齐槽位；
       │ 绝不设 http.Client.Timeout——它会腰斩流）
       ▼
   OpenAI / Claude / Gemini / Ollama
   （四家协议不同：标准 SSE / 事件流 / 帧式 SSE / NDJSON，eino 抹平）
```

### 1.2 回程：token 一个个流回来

```
   LLM 逐 token 吐出
       ▲
       │ eino Recv() 阻塞式收 chunk（goroutine 挂在 netpoller，代价≈0）
┌──────┴─────────────────────────────────────────────────────────────────┐
│ P6 对话引擎逐 chunk 分类（Agent 循环体）                                 │
│   文本 delta → SSE writer 写                                            │
│       "event: delta\ndata: {\"content\":\"...\"}\n\n" + Flush          │
│   tool_call chunk → 发 event: tool_call → 经 mcp api 执行工具           │
│       → 发 event: tool_result → 结果回填上下文                          │
│       → 回到 P5 再调一次 LLM（循环，轮次由模型决定）                     │
│   流空闲 15s → 写 ": ping" 注释行保活                                    │
│ 事件协议：meta（首个，含 conversation_id/message_id）                    │
│           delta / tool_call / tool_result                               │
│           done（usage + finish_reason）/ error（code + retryable）       │
└──────┬──────────────────────────────────────────────────────────────────┘
       ▼
   nginx 透传（不缓冲、不压缩）→ 浏览器
       ▼
┌─ P7 前端渲染 ──────────────────────────────────────────────────────────┐
│ response.body.getReader() 循环 read()                                  │
│ → TextDecoder 解码 → 按空行 \n\n 切事件 → 解析 event:/data: 行           │
│ → delta 逐字 append 进消息气泡（打字机效果）                             │
│ → tool_call/tool_result 渲染成工具卡片 → done 收尾、解锁输入框           │
└─────────────────────────────────────────────────────────────────────────┘
```

### 1.3 流结束：三个出口，收尾工作相同

```
正常 done ／ event: error ／ 客户端断连（fetch abort / 关页面）
       │
       ▼
┌─ P8 收尾（对话引擎）───────────────────────────────────────────────────┐
│ ① 释放 bulkhead 槽位（wrapStream 的 Close，不是函数返回时）              │
│ ② 落库（短连接、不持事务）：AI 回复全文入 messages；                     │
│    conversations.updated_at 更新（会话列表排序依据）                      │
│ ③ executions 落运行日志：每次 LLM 调用一行                              │
│    （provider/model/token/耗时/工具链/错误类）——排障唯一线索             │
│ ④ token 用量 → Redis INCR 预算计数（80% 告警，耗尽拒新会话）             │
└─────────────────────────────────────────────────────────────────────────┘
```

### 1.4 每一步的「为什么」

- **P0-P1 前端与网关**：SSE 端点是 POST（要带 message body）且要带 cookie 鉴权，EventSource 两样都做不到。nginx 四件套（关缓冲、1.1、300s 读超时、禁 gzip）错任何一样，流式就退化成「憋到最后一次性吐出」。
- **P2 中间件**：trace_id 在这里生成，之后每层日志自动携带（ctx 注入）；auth 查的是 Redis 里的 session，不是解 JWT、不是查 PG。
- **P3 handler**：**响应头一旦写出（200），HTTP 状态码就再也没机会改了**。所以 401/429/参数校验全部必须在写头之前完成，之后的一切错误只能走 `event: error`。传下去的是 `c.Request.Context()`——整条取消链的起点。
- **P4 service**：拼上下文（四类素材）、跨模块只经各家的 `api` 接口（agent 配置、provider 密钥经 `providerapi.ModelService.ResolveLLMConfig`、mcp 工具）、RAG 失败降级而不是报错（缓存/检索是增益不是硬依赖）。
- **P5 platform/llm**：对话引擎唯一不亲自做的事。两个反直觉点：抢不到槽**立即 503** 而不是排队（等 30 秒再成功不如让用户马上重试）；`http.Client.Timeout` 被禁止（它管整个 body 读取，会把 5 分钟的流在 30 秒腰斩），超时全靠 ctx 三层。
- **P6 回传**：每写一个事件手动 `Flush()`，否则 Gin 的 writer 缓冲会把打字机效果吃掉。工具循环发生在这里：chunk 流到一半「画风突变」从文本变成 tool_call，引擎暂停吐字、执行工具、结果喂回去、再开一轮 P5——用户观感是「思考中 → 工具卡片 → 继续回答」。
- **P8 收尾**：写库刻意压到流结束后——流式期间 100 条并发流若各持一个 PG 连接/事务，连接池直接打爆（「流式路径只读不写」约定的由来）。

### 1.5 贯穿全程的三条隐形线索

1. **trace_id**：P2 生成 → 每层结构化日志携带 → 出问题拿一个 ID 串起全链路。
2. **ctx 取消链**：客户端断连 ctx ⊂ overall(5min) ⊂ attempt(TTFT/idle)——任何一层先到期，下层全部感知，最终取消上游 LLM 省 token。
3. **槽位生命周期**：P5 抢到 → 整个流（含工具循环的所有轮次）持有 → P8 流终点释放。持有时长≈真实占用时长，这就是 bulkhead 能限住「对供应商在途调用数」的原因。

一句话分工：**前端管渲染、nginx 管透传、handler 管边界、service 管编排、platform/llm 管弹性、LLM 只管生成**——每层只做自己那件事，chat 将来才能整个拆出去。

## 2. 三张表数据模型

三张表增长性和策略完全不同：

| 表 | 性质 | 增长 | 策略 |
|---|---|---|---|
| `conversations` | 可变表（有 updated_at） | 中等 | 常规索引，keyset 分页 |
| `messages` | **append-only** | 次快（一年几十万行） | 监控，~10M 行再分区 |
| `executions` | append-only 日志 | 最快（每次 LLM 调用一行） | **建表即按月分区 + 90 天保留** |

不存在的表：没有「上下文表」（上下文每次现拼）、没有「历史缓存表」（Redis 只放 session/缓存/计数，历史的事实源是 PG）。

### 2.1 conversations —— 会话头

一行 = 一次持续对话，只存元数据不存内容；列表页只查这张小表。归属 chat 模块（`db.BaseMutable`）。

| 列 | 类型 | 约束 | 用途 |
|---|---|---|---|
| `id` | bigint | IDENTITY PK | |
| `user_id` | bigint | NOT NULL, FK→users RESTRICT | 归属用户，列表按它过滤 |
| `agent_id` | bigint | NOT NULL, FK→agents RESTRICT | 创建时绑定，中途不换 |
| `title` | text | NOT NULL DEFAULT '' | 取首条用户消息截断生成，列表展示用 |
| `created_at` / `updated_at` | timestamptz | NOT NULL | `updated_at` 每落一条消息就 touch，是列表排序键 |

索引：`(user_id, updated_at DESC, id DESC)`（列表 keyset）+ `(agent_id)`。

### 2.2 messages —— 事实源，append-only

一行 = 一条消息。**历史不可变**：无 `updated_at`，纠错靠追加新消息不靠改旧行（LLM 的上下文模型就是这样工作的——重写上下文 = 发新消息）。归属 chat 模块（`db.BaseAppendOnly`）。

| 列 | 类型 | 约束 | 用途 |
|---|---|---|---|
| `id` | bigint | IDENTITY PK | 单调递增，**天然就是消息顺序**，也是翻页游标 |
| `conversation_id` | bigint | NOT NULL, FK→conversations **CASCADE** | 会话删→消息级联删（全库唯一两张真子表之一） |
| `role` | text | NOT NULL CHECK (`user`/`assistant`/`tool`) | 三种角色，见下方示例 |
| `content` | text | NOT NULL | 文本内容（user 输入 / assistant 回复 / tool 的结果） |
| `tool_calls` | jsonb | NOT NULL DEFAULT '[]' | **assistant 中间行专用**：`[{tool, args}]` 模型发起的工具调用请求；tool 行的调用关联也由该 jsonb 内的调用标识承载（未单列 tool_call_id / tool_name，见 §3） |
| `created_at` | timestamptz | NOT NULL | append-only 表唯一时间戳 |

索引：`(conversation_id, id)` —— 上下文按序取、会话内翻页，一条索引全包。

**一轮带工具的对话落库长这样**（下一轮拼上下文 = 原样转回 LLM messages 数组，模型就「记得」自己调过什么；截断按轮次整段截，截散了模型会失忆）：

| id | role | content | 关键字段 |
|---|---|---|---|
| 101 | user | 「查下昨天的异常订单」 | |
| 102 | assistant | （空） | `tool_calls: [{id:"call_1", tool:"query_orders", args:{...}}]` |
| 103 | tool | （订单结果 JSON，入库截断） | 调用标识在 tool_calls 关联信息内 |
| 104 | assistant | 「找到 3 个异常订单：…」 | |

### 2.3 executions —— 运行日志（排障唯一线索）

每次 **LLM 调用**一行（不是每条消息——一轮带工具的对话 = 多行）。归属 platform/logging（executions 表归 chat/llm 模块的约定见 CLAUDE.md 包结构注释；DDL 在 migrations/00007）。

| 列 | 类型 | 约束 | 用途 |
|---|---|---|---|
| `id` | bigint | IDENTITY（PK 含分区键 `(id, created_at)`） | |
| `conversation_id` | bigint | NULL，**弱引用不建 FK** | 所属会话；workflow 节点调用为空 |
| `model_id` | bigint | NULL，弱引用不建 FK | 配合 `model_name` 冗余快照 |
| `model_name` | text | NOT NULL DEFAULT '' | 冗余存模型名（模型删除后记录仍可读），写入时同步 |
| `input` / `output` | jsonb | NOT NULL DEFAULT '{}'/…' | 大文本——**查询禁 SELECT \***，不取零成本（TOAST） |
| `tool_chain` | jsonb | NOT NULL DEFAULT '[]' | 工具调用链：每轮 `{tool, args, result}`，与 messages.tool_calls 互补 |
| `prompt_tokens` / `completion_tokens` / `total_tokens` | bigint | NOT NULL DEFAULT 0 | 每次调用的 token 用量（预算计数的持久侧） |
| `duration_ms` | integer | NOT NULL DEFAULT 0 | 每次调用的耗时（LLM 时延记这里，端到端时延前端本地计时） |
| `finish_reason` | text | NOT NULL DEFAULT '' | `stop`/`length`/`tool_use`…；失败流为空串 |
| `error_class` | text | NULL，CHECK 七类 | `Timeout/RateLimited/Overloaded/Network/InvalidRequest/Auth/ProviderDown`；NULL=成功 |
| `created_at` | timestamptz | NOT NULL | 分区键 |

- 按月 `PARTITION BY RANGE (created_at)`；月底由 deploy/backup/partition_maintenance.sql 建下月分区、drop 90 天前的。
- 索引（父表建自动传播分区）：`(conversation_id, created_at)` + `(model_id)`。

### 2.4 DDL 摘录（migrations/00006 / 00007）

```sql
-- 00006（节选，完整含 COMMENT 见迁移文件）
CREATE TABLE conversations (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    agent_id   bigint NOT NULL REFERENCES agents (id) ON DELETE RESTRICT,
    title      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_conversations_user  ON conversations (user_id, updated_at DESC, id DESC);
CREATE INDEX idx_conversations_agent ON conversations (agent_id);

CREATE TABLE messages (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id bigint NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    content         text NOT NULL,
    tool_calls      jsonb NOT NULL DEFAULT '[]',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_messages_conversation ON messages (conversation_id, id);

-- 00007（节选）
CREATE TABLE executions (
    id                bigint GENERATED ALWAYS AS IDENTITY,
    conversation_id   bigint,
    model_id          bigint,
    model_name        text NOT NULL DEFAULT '',
    input             jsonb NOT NULL DEFAULT '{}',
    output            jsonb NOT NULL DEFAULT '{}',
    tool_chain        jsonb NOT NULL DEFAULT '[]',
    prompt_tokens     bigint NOT NULL DEFAULT 0,
    completion_tokens bigint NOT NULL DEFAULT 0,
    total_tokens      bigint NOT NULL DEFAULT 0,
    duration_ms       integer NOT NULL DEFAULT 0,
    finish_reason     text NOT NULL DEFAULT '',
    error_class       text CHECK (error_class IN ('Timeout', 'RateLimited', 'Overloaded', 'Network', 'InvalidRequest', 'Auth', 'ProviderDown')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)  -- 分区键必须在主键里
) PARTITION BY RANGE (created_at);
```

## 3. 设计决策

**命名与形态：**
- 表名不叫 `sessions`：与 auth 的 Redis session（`hify:session:{token}`）撞名，conversations 语义也更准（对话 ≠ 登录会话）。
- 不加 `status` 列（ACTIVE/ARCHIVED）：预留字段违反建表规范；真要归档时加 `archived_at timestamptz` + partial 索引 `WHERE archived_at IS NULL`。
- `latency_ms` 不进 messages：每次 LLM 调用的耗时记 `executions.duration_ms`（一行 = 一次调用）；端到端首字/总时延由前端本地计时，服务端无从准确得知渲染侧体验。

**messages 的取舍（讨论稿 → 落库版的收敛）：**
- 讨论稿曾含 `tool_call_id` / `tool_name` / `model_id` / `input_tokens` / `output_tokens` / `finish_reason` 列；落库版收敛为最小集——**用量、模型、结束原因统一由 executions 承担**（一行 = 一次 LLM 调用，本来就是这些数据的正确粒度），messages 只留对话事实本身；tool 行的调用关联由 tool_calls jsonb 内的调用标识承载，不单列。
- **system 不落库**：系统提示词每次拼上下文时从 Agent 配置取当前值——改提示词立即生效、不占历史；存进消息反而锁死旧提示词。
- **工具循环的每一环都是一行**（见 §2.2 示例）；**工具结果入库要截断**到固定长度——防单行爆炸，也保证下轮拼回的上下文和当初一致。

**executions 的三个决策：**
- **弱引用不建 FK**（conversation_id / model_id）：90 天保留的日志表不能反过来挡住会话/模型删除；引用完整性由业务层哨兵（`MODEL_IN_USE`）挡。分区表建 FK 也麻烦。
- **建表即分区**：时间序列 + append-only + 明确保留期，后来再转分区要整表重写，所以 00007 直接 `PARTITION BY RANGE`。
- 两个「保留」不同义：**PG 每日备份 7-14 天**是灾难恢复（所有数据）；**executions 在线 90 天**是查询窗口——超期 drop 分区，历史仍可从备份恢复。

**读写约定：**
- 流式路径只读不写、不持事务；所有落库压到流结束后的短连接里（P8，防 100 并发流打爆连接池）。
- keyset 分页：会话列表 `(user_id, updated_at DESC, id DESC)`、消息上下文 `(conversation_id, id)`；`LIMIT n+1` 判 has_more，不算精确 COUNT。

## 4. 已知限制

- **openai_compatible 无法自定义端点**：eino-ext openai adapter 的 `ChatModelConfig.BaseURL` 仅 Azure 场景生效（`go doc` 核实），非 Azure 官方端点无自定义入口；`llm` factory 对 `openai_compatible` 维持返回 `ErrUnsupportedKind`。后续支持需自写 openai_compatible 适配（直连 OpenAI 协议端点）。
