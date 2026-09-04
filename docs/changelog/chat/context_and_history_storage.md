# Chat 对话引擎：上下文管理与历史存储

> 状态：**设计讨论定稿**（2026-09-03）。
> 本文档记录 chat 上下文管理的背景知识、策略选型、历史存储三分角色，
> 以及对「Redis 历史热缓存组装上下文」方案（外部课程 sendMessage 执行链路）的评估结论——**Hify 不采用**。
> 关联文档：[data_flow_and_model.md](./data_flow_and_model.md)（全链路数据流与三张表，表结构以其为准）。

## 1. 背景：LLM 的「上下文」是什么、多轮对话怎么实现

### 1.1 三个常被混淆的概念

| 概念 | 是什么 | 归属 |
|---|---|---|
| 上下文窗口（context window） | 模型单次调用能「看见」的最大 token 数，attention 计算对长度近似 O(n²) | 模型能力边界 |
| 单次调用的输入 messages 数组 | 每次请求**全量重发**的对话内容，`[system, 历史..., 当前消息]` | 请求报文 |
| 对话历史（DB 里的 messages 表） | 应用层落库的原料，拼上下文时从中取最近 K 轮 | 应用数据 |

核心事实：**LLM 无状态**。每次调用都是纯函数 `f(messages) → 回复`，两次调用之间没有任何记忆。所谓「记忆」类产品功能（ChatGPT Memory 等）全是应用层注入。供应商的 prompt caching / KV cache 是性能优化（命中部分不重算），不是语义记忆。

### 1.2 多轮对话 = 应用层重组装循环

```
用户发第 n 条消息
    ▼
① 取最近 K 轮历史（messages 表，keyset 查询）
    ▼
② 拼装：[system prompt] + [K 轮历史] + [当前消息]   ← system 每次取 Agent 配置当前值，不落库
    ▼
③ 发给 LLM（整个数组全量重发）
    ▼
④ 回复落库（messages 追加一行）
    ▼
下一轮回到 ① —— 模型「记得」的，只是我们每次重发给它的
```

成本特性：每轮重发全部历史 → **token 成本随对话推进近似二次增长**。上下文管理策略（§2）本质都是在回答「历史变长后，每次带多少、丢多少、压多少」。

## 2. 上下文管理策略与 Hify 选型

### 2.1 七种常见策略

| # | 策略 | 做法 | 优点 | 缺点 |
|---|---|---|---|---|
| ① | 全量携带 | 历史全发 | 零丢失、实现最简 | 成本二次增长，撞上下文窗口即 400 |
| ② | 滑动窗口截断 | 只带最近 K 轮 | 实现简单、成本线性、行为可预期 | 早期信息丢失（「失忆」） |
| ③ | 滚动摘要 | 旧历史压缩成摘要 + 最近 K 轮原文 | 长对话保真度最好 | 摘要本身要 LLM 调用（额外成本/时延/可能失真），需新存储 |
| ④ | 记忆提取 | 跨会话沉淀用户画像/事实，注入 system | 「认识用户」的体验 | 提取时机、质量、隐私都是工程难题 |
| ⑤ | 历史 RAG 化 | 历史向量化，按相关性召回 | 超长历史也能引用 | 相关 ≠ 时序，召回错乱比丢失更糟；embedding 成本 |
| ⑥ | 结构化状态 | 维护 JSON scratchpad/状态机替代历史 | 状态紧凑、可控 | 只适合结构化任务，自由对话不适用 |
| ⑦ | 供应商托管会话（Threads） | 把会话存供应商侧 | 应用层零负担 | 锁死单一供应商——Hify 多提供商接入，直接排除 |

### 2.2 Hify 一期选择：策略②滑动窗口截断

- 落地即 `agents.max_context_turns`（migrations/00009）：`int NOT NULL DEFAULT 10`，`CHECK (1-100)`；api/service/model 四层已贯通（`agentapi` schema 常量 `MaxContextTurnsMin/Max/Default`）。
- **「轮」= user→assistant 一次交互**，不是消息行数：带工具的一轮 = user + assistant(tool_calls) + tool + assistant，2~5 行。
- **整轮截断**：以 user 消息为锚点整段保留/丢弃，绝不按行数截——把 tool_call 与 tool_result 截散，messages 数组协议非法、模型失忆。
- 道理：内部工具场景对话多在中短长度，截断最便宜、行为最可预期；长对话保真问题等真实出现再升级。

### 2.3 分层 prompt 顺序与升级路径

拼装顺序（stable-first，前缀越稳定越利于供应商 prefix cache 命中）：

```
① system prompt（Agent 配置）
② memory（未来：跨会话记忆，④）
③ summary（未来：滚动摘要，③）
④ 最近 K 轮原文（现在：②）
⑤ RAG 召回块（已有：按相关性插入）
⑥ 当前用户消息
```

升级路径注意：滚动摘要需要**新存储**（conversations 表无 summary 列，加列或新表都行，属二期决策）；记忆提取、历史 RAG 同理。一期只落 ④+⑤。

## 3. 历史存储角色分工（定稿）

### 3.1 三问判据

| 问题 | 答案 → 归宿 |
|---|---|
| 这份数据丢了能不能接受？ | 不能 → PG |
| 它能否从事实源重建？ | 能 → Redis（纯缓存/计数） |
| 取它的方式是按 id 还是按相似度？ | 相似度 → pgvector |

### 3.2 三个存储的角色

| 存储 | 角色 | 放什么 | 不放什么 |
|---|---|---|---|
| **PG** | 正确性（SoT） | conversations / messages / executions / 全部配置表；上下文拼装**直查** `(conversation_id, id)` keyset | — |
| **Redis** | 性能/易失 | auth session、限流/预算计数、语义缓存答案、配置 Cache-Aside | **对话历史**（可从 PG 重建但直查已毫秒级，缓存无收益只有一致性风险） |
| **pgvector** | 相关性 | RAG chunks 向量、语义缓存 query 向量 | 对话历史（结构化时序数据，按 id 取，不需要向量） |

### 3.3 定稿结论

**组装上下文 = 每次直读 PG 的 keyset 查询，无历史缓存层**（即 data_flow_and_model.md §2 的「不存在的表：历史缓存表」）。对话历史是结构化、按 id 时序取的数据，PG + Redis 辅助足够，向量库与历史无关。

## 4. 被评估方案：sendMessage 双线程链路 + Redis 历史热缓存（不采用）

> 来源：外部课程（极客时间）的 sendMessage() 执行链路图 + 据此提出的理解。模式真实存在于生产，评估结论是**在 Hify 规模与语境下不成立**，记录判据供日后复核。

### 4.1 参照线图：sendMessage() 执行链路（Java 语境）

```
┌─ Tomcat 线程（请求处理）─────────────────────────────┐
│ ① 会话：创建 / 查询 session                           │
│ ② 配置：Agent + ModelConfig                           │
│ ③ 取历史：Redis LRANGE（List 热缓存，最近 N 轮）        │
│ ④ 写用户消息：MySQL insert + Redis RPUSH（双写）        │
└──────┬────────────────────────────────────────────────┘
       │ 线程移交（Tomcat 线程立即返回，流式交给专用池）
       ▼
┌─ llmExecutor 线程（异步 LLM 流）──────────────────────┐
│ ⑤ 组装 messages：system + 历史(Redis) + 当前消息       │
│ ⑥ 调 LLM：SSE 流式 ──── chunk ────▶ 前端               │
│     ┆ 流结束                                           │
│     ▼                                                 │
│ ⑦ 写 AI 回复：MySQL insert + Redis RPUSH（双写）        │
│ ⑧ 窗口裁剪：超出 N 轮 → LPOP 按条数裁头                │
│ ⑨ emitter.complete() 关闭 SSE                         │
└───────────────────────────────────────────────────────┘

存储分工（该方案）：MySQL = 全量事实源（仅回源/历史查看/分析用，
不参与组装 LLM 请求）；Redis List = 工作内存（key session:{sessionId}，
LRANGE 读 / RPUSH 写 / LPOP 裁窗口 / TTL 兜底，Cache-Aside 冷启动回源）。
（图中 MySQL 在 Hify 对应 PG。）
```

### 4.2 方案要点（原提案）

- PG：持久化 SoT，存全量对话；服务历史查看、数据分析、缓存失效后回源。**不用于组装 LLM 请求**——理由「每次都 SQL 查询 + 排序太慢」。
- Redis：专门用于组装请求的工作内存，存最近 maxContextTurns 轮，`{role, content}` 数组，读写 O(1)，TTL 2h 每次对话刷新。
- 双写：消息落 PG 的同时 RPUSH 到 Redis；读上下文只读 Redis；过期后从 PG 重新加载（Cache-Aside）。

```java
// 原提案伪代码
List<ChatMessage> history = redis.get(sessionKey);
if (history == null) {   // 冷启动：从 MySQL 加载最近 N 条，回写 Redis
    history = chatMessageMapper.selectRecent(sessionId, maxContextTurns * 2);
    redis.set(sessionKey, history, Duration.ofHours(2));
}
```

### 4.3 成立的部分

- PG 作为全量 SoT 的定位 ✅
- Cache-Aside 冷启动回源、按 size 裁剪（LPOP）而非只靠 TTL ✅
- 「对话历史是结构化时序数据，PG + Redis 够用、不需要向量库」✅

### 4.4 不成立的前提：「PG 查历史太慢」

- **带正确索引的查询没有「排序」步骤**：`WHERE conversation_id = $1 ORDER BY id DESC LIMIT 40` 走 `(conversation_id, id)` 索引 = 索引即序，倒序扫 40 行即停，执行计划里没有 Sort 节点。
- 时延账（单次发消息请求预算）：

| 步骤 | 耗时量级 |
|---|---|
| Redis LRANGE 取历史 | ~0.3ms |
| **PG 索引查询取 K 轮** | **~0.5–2ms** |
| LLM 首字（TTFT） | **500ms–30000ms** |

省掉 PG 查询 = 在 1~30 秒的请求里省 1–2ms，占比 <0.2%；瓶颈的 99.8% 是 LLM。Hify 峰值 3–5 QPS、100 并发流，该查询每秒个位数次。「太慢」只在**万级 QPS 的 C 端产品**（历史读打满读副本时）成立。

### 4.5 真实代价与伪代码三个缺陷

代价：①**双写一致性**——SSE 流写点分散（user 行、流末 assistant 行、工具中间行），PG 成功 RPUSH 失败（panic/断连/重试路径）→ 两边漂移，轻则上下文少一轮，重则 tool_call 与 tool_result 截散、协议非法；②**写放大**——每条消息两系统各写一次，事务边界覆盖不了 Redis；③**缓存命中 ≠ 数据正确**。

伪代码三个缺陷（逐条）：

1. **命中 ≠ 够用**：List 被 LPOP 裁到 10 轮后，管理员把 `max_context_turns` 调到 20 → `get()` 非空（10 轮）永不回源，**静默欠上下文**且无任何报错。修法三选一：命中后校验轮数不足也回源 / key 带 N（`...:n20`）/ **不写时裁剪、组装时才裁**。
2. **`maxContextTurns * 2` 按行数推轮数**：带工具的一轮 2~5 行，按行数截必然把轮截散——messages 协议非法或模型失忆。必须以 user 消息为锚整轮截（同 §2.2）。
3. **双写失败无处理**：正确纪律是 PG 先写（事实源成功才算成功），RPUSH 失败即 `DEL` 缓存 key（宁可下次回源，不留一份错的）。

附带两个命名问题：key `session:{sessionId}` 与 auth 的 `hify:session:{token}` 撞名；Hify 实体是 conversation 不是 session，且全仓库 key 须走 `redisx.Key` 统一 `hify:` 前缀。

### 4.6 语境差异：Java 线程池 vs Go goroutine

参照图是 Java 生态：Tomcat 线程 + `llmExecutor` 线程池 + `SseEmitter`。第二个线程池存在的理由是 **Java 线程 = MB 级栈**，阻塞在 LLM 长连接上会耗尽 servlet 线程。Go 里 100 条阻塞在 netpoller 的 SSE 流 = 100 个代价≈0 的 goroutine，这个问题不存在——CLAUDE.md 明确 LLM 并发控制用 bulkhead 槽位、不用 worker pool。

### 4.7 何时该采用 + Hify 最终决策

采用该方案的判据（满足其一再考虑）：历史读真把 PG / 读副本打满（有监控数据说话）；QPS 上千；有双写对账基建。Hify 三条全不占。

**定稿：组装上下文 = 每次直读 PG 的 `(conversation_id, id)` keyset 查询，无历史缓存**；Redis 在对话链路的角色维持 §3.2 四件（session / 限流预算计数 / 语义缓存答案 / 配置 Cache-Aside）。一人维护的项目，**正确性 bug 比 1ms 重要**——缓存应该是瓶颈出现后的响应，不是预防性接种。
