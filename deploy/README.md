# Hify 单机 Docker Compose 部署（deploy/）

对应 CLAUDE.md《部署架构》：4 个常驻容器 + 1 个定时容器，2C4G 预算内；唯一对外暴露 nginx 443。

```
浏览器 ──HTTPS :443──▶ nginx（前端镜像：TLS/静态/反代/SSE 透传）
                         │ /api/* ──▶ hify:8080（唯一业务进程，非 root）
hify ──▶ postgres(pgvector/pg17) / redis(7, AOF) / 外部 LLM（SSE/NDJSON）
backup 容器（每日 02:00 cron）──▶ pg_dump ──▶ backups 卷（保留 14 天）
```

## 目录

| 文件 | 说明 |
|---|---|
| `docker-compose.yml` | 5 服务编排（网络/卷/mem_limit/nofile/healthcheck） |
| `backend/Dockerfile` | 多阶段：golang:1.26-alpine 编 → alpine 跑（非 root，`-ldflags="-s -w"`） |
| `frontend/Dockerfile` | node:22-alpine 构建 Vue dist → nginx:alpine 服务 |
| `frontend/nginx.conf` | TLS 终结 / `/assets` 长缓存 / index.html no-cache / `/api` 反代 / **SSE 透传三件套** |
| `backup/backup.sh` | 每日 pg_dump + 14 天清理 |
| `.env.example` | 部署配置模板（密码/密钥，禁止入 Git） |
| `certs/` | TLS 证书（正式证书放这；开发用 `make certs` 自签） |

## 快速开始

```bash
# 1. 配置
cp deploy/.env.example deploy/.env      # 填好 POSTGRES_PASSWORD / REDIS_PASSWORD / SESSION_SECRET

# 2. 证书（开发用自签；生产放正式证书到 deploy/certs/ 后跳过）
make certs

# 3. 启动（构建镜像 + up -d）
make start ENV=prod

# 4. 浏览器访问 https://localhost（自签证书需手动信任）
make stop ENV=prod                      # 停止
```

## 环境切换（Makefile ENV 入参）

| | `ENV=dev`（默认） | `ENV=prod` |
|---|---|---|
| `make start` | `./start.sh`（本地 go build + vite dev，热重载） | `docker compose up -d --build` |
| `make stop` | `./stop.sh`（按 PID 文件优雅停止） | `docker compose down` |
| `make build` | 后端无 ldflags（利调试） | 后端 `-ldflags="-s -w"` 瘦身 |

## 卷与持久化

| 卷 | 内容 | 备注 |
|---|---|---|
| `pgdata` | PG 数据 | **唯一不可再生数据** |
| `backups` | 每日 pg_dump（14 天） | 灾难恢复 |
| `redisdata` | AOF 持久化 | 预算计数不能因重启清零 |
| `logs` | 后端结构化日志（/app/logs） | rotation 由后端处理 |

## 说明

- 容器网络内 PG/Redis 地址由 compose 显式注入（`host=postgres` / `redis:6379`），与根目录本地开发 `.env`（host=localhost）互不干扰。
- 资源上限按 2C4G：nginx 128m / hify 1g / postgres 768m / redis 256m / backup 100m；全部 `restart: always` + `ulimit nofile 65535`。
- 表结构演进走 migrations（goose/golang-migrate），暂未接入容器启动流程，后续任务补迁移入口。
