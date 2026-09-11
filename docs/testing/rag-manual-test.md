# RAG 模块人工冒烟测试

验证对象：`internal/rag` 全链路（api → service → store → handler + 组合根接线，spec 08 端到端）。
全部为手工步骤，自动化覆盖见各包 `*_test.go`；实现决策见 `docs/changelog/rag/`。

- 接口前缀：`/api/v1`（11 端点：KB 5 + 文档 4 + retrieve 1 + 上传，受登录中间件保护）
- 响应信封：`{success, data, error, meta}`（`platform/respond`）；ID 一律字符串
- 分页形态：KB 列表**偏移分页** `meta={page,page_size,total}`（小配置表例外）；文档列表**游标分页** `meta={limit,has_more,next_cursor}`
- 本文档命令在**仓库根目录**执行

> **端口说明（2026-08-31 环境变更）**：本机 8080 / 5432 被另一项目（agentscope）占用，
> hify 本地 dev 临时迁移到 **SERVER_PORT=8081 + PG 5433**（`.env` 临时改动，不入 Git）。
> 本文档所有命令按 8081 写；标准端口环境替换回 8080 / 5432 即可。

## 0. 前置条件

| 依赖 | 用途 | 检查命令 |
|---|---|---|
| Docker | 临时 PG（pgvector）/ Redis 容器 | `docker version` |
| curl / jq | 接口调用与字段提取 | `curl --version && jq --version` |
| 真实 embedding API key | 检索语义验收（§7）需要真实向量化；任意 OpenAI 兼容供应商（SiliconFlow / OpenAI），模型须能输出 1536 维（见 §3 选型约束） | 自行 export `BASE`/`KEY` |

无真实 key 时可用**本地 stub** 替代（2026-09-11 首轮走查即此形态）：任意 OpenAI 兼容
`POST {base}/embeddings` 服务返回 1536 维确定性向量即可（报文契约见 `platform/llm/embed.go`
`openaiEmbedTarget`：Bearer 头 + `{data:[{index,embedding}],usage:{prompt_tokens}}`；
词元用带符号哈希落维——共享词元对相似度显著高于无关对）。检索语义质量（改写命中）
不在 stub 形态的验证范围，结论需如实标注。

## 1. 启动依赖容器 + 迁移 + 启动服务

```bash
# PG：宿主 5433 → 容器 5432（注释独立成行——行尾注释在 GUI run 对话框里会被当容器参数）
docker run -d --name hify-pg-test \
  -e POSTGRES_USER=hify -e POSTGRES_PASSWORD=hify -e POSTGRES_DB=hify \
  -p 5433:5432 pgvector/pgvector:pg17
docker run -d --name hify-redis-test -p 6379:6379 redis:7-alpine
until docker exec hify-pg-test pg_isready -U hify >/dev/null 2>&1; do sleep 1; done && echo "pg ready"
```

`.env` 核对（本地 dev 临时端口）：

```dotenv
SERVER_PORT=8081
PG_DSN=host=localhost user=hify password=hify dbname=hify port=5433 sslmode=disable
REDIS_ADDR=localhost:6379
```

```bash
make migrate-up && make migrate-status   # 预期 11 条全部 applied（00011_rag_schema_review）
make start                               # 日志落 logs/hify.log
curl -s localhost:8081/health | jq .      # → {"success":true,"data":"Hify is running",...}
```

表结构确认（00011 改名 + 新列）：

```bash
docker exec hify-pg-test psql -U hify -d hify -c '\d document_chunks'
# 关键断言：表名 document_chunks（非 chunks）、列 chunk_index / token_count / embedding vector(1536)、
# 索引 idx_document_chunks_embedding（hnsw, vector_cosine_ops）
```

## 2. 登录链（后续所有请求都要带 cookie）

```bash
curl -s -X POST localhost:8081/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"rag-tester","password":"smoke-test-123"}' | jq .success    # → true（已注册则忽略报错）
curl -s -c /tmp/rag-jar -X POST localhost:8081/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"rag-tester","password":"smoke-test-123"}' | jq .success    # → true
curl -s localhost:8081/api/v1/knowledge-bases | jq .error.code               # → "UNAUTHORIZED"（未带 cookie）
```

## 3. 造前置数据（provider + embedding 模型）

知识库绑定 embedding 模型（建库预检 capability=embedding + **dim=1536**——向量列
`vector(1536)` 钉死；原生维度更高的模型靠请求 `dimensions` 截断到 1536，spec 02 修订）。
`BASE`/`KEY` 手动 export（供应商自选：SiliconFlow Qwen3-Embedding / OpenAI 均可；
key 不落命令历史之外的任何地方）；本地 stub 形态则 `base_url` 指 stub、`api_key` 任意非空值。

**选型约束**：模型必须能输出 1536 维——原生 ≥1536 且支持 `dimensions` 截断（SiliconFlow
`Qwen/Qwen3-Embedding-4B` 原生 2560 ✅、`8B` 4096 ✅、OpenAI `text-embedding-3-small` 原生
1536 ✅）；**原生 <1536 的不行**（bge-m3 1024 无法上采，建库预检 `EMBEDDING_DIM_MISMATCH` 挡）。

```bash
# stub 形态：BASE=http://127.0.0.1:18789 KEY=stub-key（先起 stub，见 §0）
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/providers -H 'Content-Type: application/json' \
  -d "{\"name\":\"openai-embed\",\"kind\":\"openai_compatible\",\"base_url\":\"$BASE\",\"api_key\":\"$KEY\"}" | jq -c '{id: .data.id, name: .data.name}'
# → {"id":"N","name":"openai-embed"}（记下 provider_id=$PID；注意 kind 枚举是 openai_compatible）
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/models -H 'Content-Type: application/json' \
  -d "{\"provider_id\":$PID,\"name\":\"qwen3-embedding\",\"model_id\":\"Qwen/Qwen3-Embedding-4B\",\"capability\":\"embedding\",\"embedding_dim\":1536}" \
  | jq -c '{id: .data.id, capability: .data.capability, dim: .data.embedding_dim}'
# → {"id":"M","capability":"embedding","dim":1536}（记下 embedding_model_id=$MID；
#   embedding_dim 填 1536=输出维度：模型原生 2560 由服务端 dimensions 参数截断）
```

## 4. 建 KB（POST /knowledge-bases）

```bash
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases -H 'Content-Type: application/json' \
  -d "{\"name\":\"产品手册库\",\"description\":\"Hify 产品手册与售后政策\",\"embedding_model_id\":$MID}" | jq .
```

**预期** 201：`data.id="K"`（字符串）、`embedding_model_id="M"`（字符串化）、`enabled=true`、
`document_count=0`、双时间戳 RFC 3339。

预检失败路径（spec 04 端点 1）：

```bash
t() { curl -s -o /tmp/r.json -w '%{http_code} ' -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases \
      -H 'Content-Type: application/json' -d "$1"; jq -c '{code: .error.code, msg: .error.message}' /tmp/r.json; }
t '{"name":"x","embedding_model_id":999}'                # 404 MODEL_NOT_FOUND
t "{\"name\":\"x\",\"embedding_model_id\":$CHAT_MID}"     # 400 EMBEDDING_DIM_MISMATCH（chat 模型，若造过）
t '{"embedding_model_id":1}'                             # 400 VALIDATION_FAILED（缺 name）
# 建第二个同名校验重名：
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases \
  -H 'Content-Type: application/json' -d "{\"name\":\"产品手册库\",\"embedding_model_id\":$MID}"   # → 409
```

## 5. 上传 + 入库管线（202 → pending → processing → ready）

造含**独有事实**的 MD（检索验收锚点）+ 围栏代码块（验证原子不切散）+ 足够长度（多 chunk）：

```bash
cat > /tmp/hify-manual.md <<'EOF'
# Hify 产品手册

## 简介

Hify 是简化版 Dify 的 AI Agent 开发平台，面向 20-50 人团队内部使用。
支持多模型提供商统一接入，包括 OpenAI、Claude、Gemini 与 Ollama。

## 安装部署

安装命令如下：

```bash
make start ENV=dev
make migrate-up
docker compose up -d
```

部署形态为 Docker Compose 单机，Go 单二进制加 alpine 镜像，
PostgreSQL 使用 pgvector 镜像提供向量检索能力。

## 售后政策

Hify 退货流程：7 天内联系客服，运费由公司承担。
超过 7 天的退货申请需支付来回运费，具体以客服确认为准。

## 功能清单

知识库与 RAG 检索支持 TXT 与 Markdown 纯文本文档，
对话引擎支持 SSE 流式响应与多轮上下文管理。
EOF

curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/documents \
  -F 'file=@/tmp/hify-manual.md' -F 'name=产品手册' | jq -c .
# → 202：data.status="pending"、file_type="md"、file_size=实际字节数、chunk_count=0、error_message=""
```

轮询入库状态（真实 embedding，秒级完成）：

```bash
for i in $(seq 1 20); do
  S=$(curl -s -b /tmp/rag-jar localhost:8081/api/v1/documents/$DID | jq -r '.data.status')
  echo "poll $i: $S"; [ "$S" = ready ] || [ "$S" = failed ] && break; sleep 1
done
curl -s -b /tmp/rag-jar localhost:8081/api/v1/documents/$DID \
  | jq -c '{status: .data.status, chunk_count: .data.chunk_count, err: .data.error_message}'
# → {"status":"ready","chunk_count":>1,"err":""}   围栏 bash 块不切散：chunks 内容里 `make start` 行完整
```

上传负向（spec 04 端点 6）：

```bash
echo '%PDF-1.4 dummy' > /tmp/fake.pdf
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/rag-jar -X POST \
  localhost:8081/api/v1/knowledge-bases/$KID/documents -F 'file=@/tmp/fake.pdf'    # → 400（file_type CHECK）
{ head -c 2097153 /dev/zero | tr '\0' 'a'; } > /tmp/too-big.md
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/rag-jar -X POST \
  localhost:8081/api/v1/knowledge-bases/$KID/documents -F 'file=@/tmp/too-big.md'  # → 400（超 2MiB 上限）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/rag-jar -X POST \
  localhost:8081/api/v1/knowledge-bases/999/documents -F 'file=@/tmp/hify-manual.md'  # → 404 KB_NOT_FOUND
```

## 6. 文档列表（游标分页）+ 详情

```bash
curl -s -b /tmp/rag-jar 'localhost:8081/api/v1/knowledge-bases/'$KID'/documents?limit=1' \
  | jq -c '{n: (.data|length), limit: .meta.limit, has_more: .meta.has_more, next: .meta.next_cursor}'
# → {"n":1,"limit":1,"has_more":false,...}（单文档时 has_more=false、next_cursor=null）
curl -s -b /tmp/rag-jar localhost:8081/api/v1/documents/$DID | jq -c '{content_len: (.data.content|length), chunk_count: .data.chunk_count}'
# → 详情含 Content 原文 + chunk_count 直读列
curl -s -b /tmp/rag-jar localhost:8081/api/v1/documents/999999 | jq -c '.error.code'   # → "DOCUMENT_NOT_FOUND"
```

## 7. 检索验收（端点 11，语义改写命中——本模块核心价值证明）

```bash
# 语义改写：问句「退货要自己出运费吗」不含文档任何关键词字面（无「7 天」「联系客服」），靠 embedding 语义命中
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/retrieve \
  -H 'Content-Type: application/json' -d '{"query":"退货要自己出运费吗"}' \
  | jq -c '.data[0] | {doc: .document_name, idx: .chunk_index, sim: .similarity, hit: (.content | contains("运费由公司承担"))}'
# → doc="产品手册"、hit=true、similarity 明显高（余弦相似度 >0.3 量级；含「运费由公司承担」的 chunk 排第一）

# 无关 query 对照：相似度应显著低于上例
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/retrieve \
  -H 'Content-Type: application/json' -d '{"query":"今天天气怎么样"}' \
  | jq -c '.data[0].similarity'    # → 显著更小

# top_k 定制
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/retrieve \
  -H 'Content-Type: application/json' -d '{"query":"部署方式","top_k":2}' | jq -c '.data | length'   # → 2

# disabled 剔除（spec 05 §3）：下架 KB → 同 query 返回 []
curl -s -b /tmp/rag-jar -X PUT localhost:8081/api/v1/knowledge-bases/$KID \
  -H 'Content-Type: application/json' -d "{\"name\":\"产品手册库\",\"enabled\":false,\"embedding_model_id\":$MID}" | jq -c '.data.enabled'
# → false（注意 PUT 整体更新语义：name 等字段一并提交）
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/retrieve \
  -H 'Content-Type: application/json' -d '{"query":"退货要自己出运费吗"}' | jq -c '.data'
# → []（service 静默剔除 disabled，不报错）
# 改回 enabled=true 恢复命中
curl -s -b /tmp/rag-jar -X PUT localhost:8081/api/v1/knowledge-bases/$KID \
  -H 'Content-Type: application/json' -d "{\"name\":\"产品手册库\",\"enabled\":true,\"embedding_model_id\":$MID}" >/dev/null
```

## 8. KB 列表 + name 模糊

```bash
curl -s -b /tmp/rag-jar 'localhost:8081/api/v1/knowledge-bases?page=1&page_size=10' \
  | jq -c '.data[] | {name, model: .embedding_model_name, cnt: .document_count, enabled}'
# → {"name":"产品手册库","model":"text-embedding-3-small","cnt":1,"enabled":true}
curl -s -b /tmp/rag-jar 'localhost:8081/api/v1/knowledge-bases?name=手册' | jq -c '.meta.total'   # → 1（ILIKE 命中）
curl -s -b /tmp/rag-jar 'localhost:8081/api/v1/knowledge-bases?name=不存在' | jq -c '.meta.total' # → 0
```

## 9. 负向矩阵（删除挡删 / processing 撞并发）

```bash
# 删有文档 KB → 409（防误删知识资产）
curl -s -o /tmp/r.json -w '%{http_code} ' -b /tmp/rag-jar -X DELETE localhost:8081/api/v1/knowledge-bases/$KID
jq -c '{code: .error.code}' /tmp/r.json    # → 409 KNOWLEDGE_BASE_IN_USE

# processing 中 reindex → 409。本地 stub 毫秒级完成、窗口太窄抓不住——确定性手法：
# 先停 embedding 源（真实 key 形态可临时改错 base_url 同理），embed 重试退避 ~700ms 拉长 processing 窗口
kill $(pgrep -f embed-stub.py) 2>/dev/null; sleep 0.3
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/documents \
  -F 'file=@/tmp/hify-manual.md' -F 'name=第二份' >/dev/null
DID2=$(curl -s -b /tmp/rag-jar 'localhost:8081/api/v1/knowledge-bases/'$KID'/documents?limit=1' | jq -r '.data[0].id')
curl -s -o /tmp/r.json -w '%{http_code} ' -b /tmp/rag-jar -X POST localhost:8081/api/v1/documents/$DID2/reindex
jq -c '{code: .error.code}' /tmp/r.json    # → 409 DOCUMENT_PROCESSING（pending/processing 撞并发）
# 停源期间上传的文档随后应转 failed（error_message 带批次号，spec 07「批次失败不跳过」），
# 而非卡 processing——这是认领后失败统一 markFailed 的回归点
sleep 3; curl -s -b /tmp/rag-jar localhost:8081/api/v1/documents/$DID2 | jq -c '{status: .data.status, err: (.data.error_message|.[0:40])}'
# → {"status":"failed","err":"embed batch [0:1]: llm Network: ..."}
# 重启 embedding 源后 reindex 该 failed 文档 → ready（治愈路径）
```

## 10. 生命周期（reindex / 软删联动 / Recovery）

```bash
# reindex：事务删 chunks + 置 pending 重跑 → ready 且 chunk_count 与首跑一致
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/documents/$DID/reindex | jq -c '{status: .data.status}'
# → {"status":"pending"}；轮询到 ready 后 chunk_count 与 §5 首跑相同

# 软删文档 → 同事务硬删 chunks（不变量规则 2）；retrieve 不再命中其内容
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/rag-jar -X DELETE localhost:8081/api/v1/documents/$DID   # → 204
docker exec hify-pg-test psql -U hify -d hify -tc \
  "SELECT count(*) FROM document_chunks WHERE document_id = $DID;"    # → 0（孤儿向量零残留）
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/retrieve \
  -H 'Content-Type: application/json' -d '{"query":"退货要自己出运费吗"}' | jq -c '.data | length'
# → 0（该事实只存在于已删文档；若传了第二份则仍可能命中——按实际剩余文档判断）
curl -s -o /dev/null -w '%{http_code}\n' -b /tmp/rag-jar localhost:8081/api/v1/documents/$DID    # → 404（软删不可见）

# Recovery：处理中重启 → 残留 pending/processing 置 failed「服务重启中断」
curl -s -b /tmp/rag-jar -X POST localhost:8081/api/v1/knowledge-bases/$KID/documents \
  -F 'file=@/tmp/hify-manual.md' -F 'name=重启牺牲品' >/dev/null
kill $(cat logs/hify.pid) 2>/dev/null; sleep 2; make start
DID3=$(curl -s -b /tmp/rag-jar 'localhost:8081/api/v1/knowledge-bases/'$KID'/documents?limit=1' | jq -r '.data[0].id')
curl -s -b /tmp/rag-jar localhost:8081/api/v1/documents/$DID3 \
  | jq -c '{status: .data.status, err: .data.error_message}'
# → {"status":"failed","err":"服务重启中断，请重新索引"}（若重启前来得及 ready，重传一份并再次立即 kill）
```

## 11. SQL 侧：召回走 HNSW 索引

小表默认 Seq/Bitmap Scan + Sort 属正常（pgvector_quickstart 坑 1，实测 7 行时规划器
恒不选 HNSW）；验证「ORDER BY 表达式与 vector_cosine_ops 配对、索引可用」需关掉替代路径：

```bash
VEC=$(python3 -c "print('['+','.join(['0.01']*1536)+']')")   # 1536 维查询向量字面量
docker exec hify-pg-test psql -U hify -d hify -At -c "
SET enable_seqscan = off; SET enable_bitmapscan = off; SET enable_sort = off;
EXPLAIN (ANALYZE) SELECT id FROM document_chunks WHERE embedding IS NOT NULL
ORDER BY embedding <=> '$VEC'::vector LIMIT 5;" | grep 'Index Scan'
# → Index Scan using idx_document_chunks_embedding ... Order By: (embedding <=> '...') —— 配对成立
```

## 12. 日志观测点

| 观测点 | 位置 | 预期 |
|---|---|---|
| 启动 Recovery | `logs/hify.log` | 有残留文档时对每个 failed 行记日志；无残留静默；失败仅 WARN 不阻断启动 |
| 管线各环节失败 | `logs/hify.log` | `pipeline: xxx failed` 带 document_id + err（错误链完整） |
| 入库成功 | DB | `documents.status='ready'`、chunk_count>0；无孤儿 chunks |
| 访问日志 | `logs/hify.log` | `"msg":"http request"` 带 method/path/status/trace_id |
| 优雅关停 | stdout | `kill` 后 `hify shutting down` 干净退出 |

## 13. 走查结论（2026-09-11 首轮，stub embedding 形态，全部通过）

- §1-§2：迁移 11 条 applied（00011 表结构 + HNSW/双 btree 索引确认）；登录链 401 正常
- §4 建 KB：201 + 预检失败路径 404 / 400（EMBEDDING_DIM_MISMATCH / VALIDATION_FAILED）/ 409 重名
- §5 管线：202 pending → processing → ready（chunk_count 1 / 7 两档）；负向 pdf 400、
  超 2MiB 400、KB 404；**长文档围栏块整体成独立 chunk 不切散**（7 chunks，fence 完整、
  各块 ≤ 500+80 rune）
- §6 文档游标分页：limit=1 → has_more=true + next_cursor 翻页命中第二篇；详情含 Content
- §7 检索：语义 query top1 hit=true（含「运费由公司承担」+ DocumentName），sim 0.1045
  vs 无关 query 0.0463（2.3×）；top_k 生效；disabled → `[]`、恢复即命中
- §8-§9：列表聚合（embedding_model_name / document_count）；name=手册 ILIKE 命中；
  删有文档 KB 409；processing reindex 409（停 embedding 源拉长窗口确定性复现）
- §10 生命周期：reindex → ready 且 chunk_count 不变；软删 → chunks 零孤儿（SQL count=0）
  且检索不再命中其内容；重启 Recovery → failed「服务重启中断」；embed 失败 → failed
  （error_message 带批次号）；failed 文档 reindex 治愈 → ready
- §11：HNSW 配对证明（关替代路径后 Index Scan using idx_document_chunks_embedding，
  Order By 表达式形态正确）；小表默认 Seq/Bitmap 属预期
- §12：document ready INFO（chunks/prompt_tokens/trace_id）、失败 ERROR 完整 err 链；
  明文凭据零入日志（key 值 / Bearer 头全零命中）

**首轮走查发现并修复（spec 07 §2 环节 9 违规，Critical）**：认领 processing 后的失败
（extractText / resolveEmbedOptions / embedChunks / commitReady）只记日志未 markFailed，
文档永久卡 processing（reindex 恒 409、只能靠重启 Recovery 收尸）。修复：四环节失败统一
`s.markFailed(err.Error())`（消息截断 500）+ 补成功路径 `slog.InfoContext`（记 chunks /
prompt_tokens，spec 同句要求）。单测失败矩阵同步补 `wantMarkFailed` 断言（原测试只断言
markReady 零调用，漏了 markFailed 被触发——注释承诺了、断言没跟上）。
认领前（环节 2 not-found / 环节 3 状态机拒绝）保持仅记日志：0 行语义下 markFailed 会误杀
并发认领方。

**形态说明**：本轮 embedding 为本地确定性哈希 stub（词元重叠即相似）——管线、状态机、
检索编排、HNSW、Recovery 全部真实验证；**真实模型的语义改写质量未验证**，换真实 key 后
建议补一轮 §7（断言「退货要自己出运费吗」top1 含「运费由公司承担」且 sim 显著高于无关
query——stub 形态下该断言靠词元重叠达成，语义含金量不同）。
