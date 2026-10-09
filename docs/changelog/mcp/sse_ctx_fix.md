# SSE 传输 Connect ctx 生命周期修复 + 会话复用评估结论

> 状态：**已交付**（2026-10-09，分支 `018-mcp-sse-ctx-fix`）。
> 缘起：README §5.2 设计点 2 要求「会话复用升级」先以真实 MCP server 实测握手开销再定去留——本次实测不仅给出了否决依据，还暴露出 sse 传输在 per-call 模式下的 P0 bug，随测修复。
> 影响面：`internal/mcp/service/mcpclient.go`（connect 签名与生命周期）+ 新回归测试；api 契约、行为语义、其余调用方零改动。

## 1. 现象

注册 sse 传输的 MCP server 后，discover / run / chat 工具调用（全部经 `mcpDialer`）**一律 30s 超时失败**。实测（everything server `@modelcontextprotocol/server-everything`，sse 模式 3002 端口）：discover 9/9 全部 `latency_ms≈30010, success=false`，后端日志 `list tools: context deadline exceeded\nclient is closing: EOF`。

## 2. 根因

go-sdk v1.3.1 的 SSE 传输把 `GET /sse` 长流请求**绑在 Connect 传入的 ctx 上**（`mcp/sse.go:341` `http.NewRequestWithContext(ctx, "GET", ...)`，读 goroutine `defer s.Close()` 随流的退出触发）。而 Hify 的 `connect()` 沿用「`WithTimeout(ctx, connectTimeout)` 派生 cctx + `defer cancel()`」写法——**cancel 在 connect 返回瞬间触发，SSE 读流在会话交到调用方手里之前已经死亡**，后续 `tools/list` 的应答永远收不到，挂到 callTimeout 30s 超时。

隔离复现（probe 二分法）：同一段代码，Connect 传存活 ctx → sse tools/list 1.5ms 正常；复刻 hify 模式（Connect(10s ctx) → 立即 cancel → ListTools）→ sse 立即 `EOF`。

**为什么没被现有测试与手测抓到**：单测用 `InMemoryTransport` 驱动（无 ctx 绑定行为，复现不了）；历次手测只走过 stdio / http——stdio 子进程寿命绑 `Close` 不绑 ctx，streamable-http 每个请求是独立 POST、session 状态在服务端，两者都不受 Connect ctx 取消影响。**只有 SSE 的读通道就是那条 GET 长流**。

## 3. 修复（`mcpclient.go` connect）

- **会话 ctx 与握手超时解耦**：`context.WithCancel(context.WithoutCancel(ctx))` 作会话基座（剥离请求侧取消/截止、保留 ctx values）；`connectTimeout` 语义改由**看门狗 timer**（`time.AfterFunc(timeout, cancel)`）保留——只约束连接+握手段，`Connect` 返回即 `timer.Stop()`，不把 10s deadline 挂到会话余生（CallTool 慢工具是常态，单次操作的时间边界是 callTimeout 30s）。
- **cancel 所有权移交调用方**：`connect` 返回 `(session, cancel, error)`，`discover` / `call` 在 `defer` 里 `Close` 之后 `cancel()`。不能反过来用「把带超时的派生 ctx 传到底」修——那会让操作超 10s 被腰斩。
- **窄竞态兜底**：`timer.Stop()` 后 `sctx.Err() != nil`（看门狗恰在握手成功瞬间触发）→ 按超时处理并 Close，不把死会话交给调用方。
- **请求断连不受影响**：`ListTools` / `CallTool` 的操作 ctx 仍派生自请求 ctx，客户端断开照常及时中止在途操作。
- 超时注入拆出 `connectWithTimeout`（`connect` 的可测形态），单测用 50ms 验证看门狗，真实 10s 不进测试。

## 4. 回归测试（`mcpclient_connect_test.go`，新增）

| 测试 | 守点 | 修复前 |
|---|---|---|
| `TestDiscoverSSEAfterConnectHandoff` | connect 返回后会话必须仍可用（sse 全链路 discover 出工具清单） | ❌ `list tools: calling "tools/list": EOF`（10ms 确定性复现） |
| `TestConnectWatchdogEnforcesHandshakeTimeout` | 握手挂起时在超时点掐断（不丢 connectTimeout 语义） | —（随修复新增） |

测试基建 `fakeSSEServer`：httptest 造最小 MCP-over-SSE server（`GET /sse` 发 endpoint 事件后保持长流、`POST /messages` 收 JSON-RPC 应答走事件流回写、`hang=true` 复现握手挂起）——**SSE 的 ctx 绑定行为只有真 HTTP 传输才有，后续 sse 相关回归测试复用此基建**。

## 5. 实测验证（修复前后，同环境同 server）

| 操作 | 修复前 | 修复后 |
|---|---|---|
| sse discover（server: ev-everything sse 3002） | 9/9 超时（≈30s） | 3/3 成功：10ms / 3ms / 3ms（首轮含 upsert） |
| sse `/run` echo | 同上必失败 | ✅ `Echo: p0-fixed` |
| stdio discover（npx filesystem） | ~1.4s | ~1.7s（同量级，无回归） |
| http discover（everything http 3001） | ~4ms | ~19ms（同量级，无回归） |

全量门禁：`go build ./... && go vet ./... && go test ./... -race -count=1` 43 包全绿；mcp service 覆盖率 86.3%（新增代码全被两个回归测试覆盖——旧 `connect` 此前是零覆盖死角）。

## 6. 会话复用评估结论（设计点 2 关闭依据）

§5.2 设计点 2 要求的「真实 MCP server 实测握手开销」已完成（2026-10-09，macOS 本机，everything/filesystem server，样本 10–20/项、预热弃首）：

| 项 | stdio（npx 起） | http | sse |
|---|---|---|---|
| M4 握手 p50（go-sdk 直测 20 样本） | **1651ms**（npx 解析占 ~1.5s；直连 node 仅 165ms） | 1.9ms | 1.8ms |
| M1 discover p50 | 1468ms | 3.5ms | （修复前全超时，无有效数；修复后 3ms） |
| M2 /run p50 | 1.48s | 6.4ms | — |
| M3 chat 工具间隙（tool_call→tool_result） | 1.53–1.58s | 10–46ms | — |
| M5 进程观察 | 每调用瞬态子进程（ps 采样捕获） | 常驻 | 常驻 |

**结论：会话复用不做。** 依据：

1. **本部署 stdio 不用**（用户确认，主用 sse/http）——stdio 1.5s 的强收益场景不存在；
2. http/sse 本地握手 ~2ms，chat 工具间隙 10–46ms 里握手只占零头（其余是工具执行+落库），会话 pool（singleflight + TTL 生命周期 + 连接快照比对）为此付出 ~300 行复杂度，违背好维护优先；
3. per-call 无状态实现继续成立，且天然规避「复用会话遇 server 重启需要失效重建」一类状态管理问题。

**重开条件**：接入远端/公网 MCP server（RTT + TLS 后每调用握手涨到几十–几百 ms、chat 内 N 次调用放大）时重评。设计草案（dialer 拆分 discoverer/caller、pool 只挂热路径、evict 钩子）已在 2026-10-09 会话中论证过，需要时直接取用。

## 7. 残留

- 无代码残留。测试期 dev 库新增的 server 12/13/14（fs-bench / ev-http / ev-sse）、agent 4/5（m3-stdio / m3-http）为手测数据，无害可留。
- mcp-manual-test.md 未加 sse 冒烟小节——`fakeSSEServer` 已把 sse 回归收进自动化测试，人工清单维持 stdio/http 即可；需要补 sse 手测路径时在 §3 增 everything-server sse 起动示例。
