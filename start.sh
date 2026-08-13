#!/usr/bin/env bash
#
# start.sh — 一键启动 Hify 本地开发环境（后端 + 前端均在后台）。
#
# 流程：检查 PG/Redis → 构建后端 → 后台启动 → 轮询健康 → 后台启动前端 → 退出。
# 产物：logs/hify.pid、logs/web.pid（供 stop.sh 优雅停止）；
#       logs/hify.start.log（后端日志）、logs/web.dev.log（前端日志）。
# 任一步骤失败即停止并提示，并清理已启动的进程；成功后两个进程独立后台运行。
# 停止：./stop.sh
#
# 前置：项目根目录已有 .env（SERVER_PORT / PG_DSN / REDIS_ADDR / SESSION_SECRET …）。
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

# ---- 输出着色（非 TTY 关闭，避免日志里出现转义码）----
if [[ -t 1 ]]; then
  RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[0;33m'; NC=$'\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; NC=''
fi
info() { printf "%s==>%s %s\n" "$GREEN" "$NC" "$*"; }
warn() { printf "%s!!%s %s\n" "$YELLOW" "$NC" "$*"; }
die()  { printf "%serror:%s %s\n" "$RED" "$NC" "$*" >&2; exit 1; }

LOG_DIR="$ROOT_DIR/logs"
BACKEND_BIN="$ROOT_DIR/bin/hify"
BACKEND_LOG="$LOG_DIR/hify.start.log"
WEB_LOG="$LOG_DIR/web.dev.log"
BACKEND_PIDFILE="$LOG_DIR/hify.pid"
WEB_PIDFILE="$LOG_DIR/web.pid"
WEB_PORT=5173 # 与 web/vite.config.ts 的 server.port 一致；改端口时同步

# 成功完成置 1；否则 EXIT（失败/中断）时清理已启动进程
FINISHED=0
cleanup() {
  [[ "$FINISHED" -eq 1 ]] && return 0
  local pf pid
  for pf in "$BACKEND_PIDFILE" "$WEB_PIDFILE"; do
    [[ -f "$pf" ]] || continue
    pid="$(cat "$pf" 2>/dev/null || true)"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
    rm -f "$pf"
  done
}
trap cleanup EXIT

# ---- 前置工具 ----
command -v go   >/dev/null 2>&1 || die "未找到 go，请先安装 Go 工具链"
command -v npm  >/dev/null 2>&1 || die "未找到 npm，请先安装 Node.js"
command -v curl >/dev/null 2>&1 || die "未找到 curl（健康检查依赖它）"

# ---- 从 .env 读关键配置 ----
[[ -f .env ]] || die ".env 不存在：请先 cp .env.example .env 并填好配置"
env_value() { grep -E "^$1=" .env | head -1 | cut -d= -f2- | tr -d '\r' || true; }

SERVER_PORT="$(env_value SERVER_PORT)"; SERVER_PORT="${SERVER_PORT:-8080}"
PG_DSN="$(env_value PG_DSN)"
[[ -n "$PG_DSN" ]] || die ".env 缺 PG_DSN"
REDIS_ADDR="$(env_value REDIS_ADDR)"; REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"

# 从 pgx DSN 抽 host/port（host=... port=...）
pg_host="$(printf '%s' "$PG_DSN" | grep -oE 'host=[^ ]+' | cut -d= -f2 || true)"; pg_host="${pg_host:-localhost}"
pg_port="$(printf '%s' "$PG_DSN" | grep -oE 'port=[0-9]+' | cut -d= -f2 || true)"; pg_port="${pg_port:-5432}"
redis_host="${REDIS_ADDR%%:*}"
redis_port="${REDIS_ADDR##*:}"

# ---- 1. 检查 PG/Redis（TCP 端口探活；nc 缺失则用 bash 内建 /dev/tcp 兜底）----
check_port() {
  if command -v nc >/dev/null 2>&1; then
    nc -z -w 2 "$1" "$2" 2>/dev/null
  else
    (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null
  fi
}
info "检查 PostgreSQL (${pg_host}:${pg_port})…"
check_port "$pg_host" "$pg_port" || die "PostgreSQL 不可达：${pg_host}:${pg_port} 连不上。请先启动 PG（端口 ${pg_port}）。"
info "检查 Redis (${redis_host}:${redis_port})…"
check_port "$redis_host" "$redis_port" || die "Redis 不可达：${redis_host}:${redis_port} 连不上。请先启动 Redis（端口 ${redis_port}）。"

# ---- 2. 构建后端 ----
info "构建后端 (go build -o bin/hify ./cmd/hify)…"
mkdir -p bin logs
go build -o "$BACKEND_BIN" ./cmd/hify || die "后端构建失败（go build 返回非零）"

# ---- 3. 后台启动后端 ----
info "后台启动后端 → ${BACKEND_LOG}"
: > "$BACKEND_LOG"
nohup "$BACKEND_BIN" >"$BACKEND_LOG" 2>&1 &
BACKEND_PID=$!
echo "$BACKEND_PID" > "$BACKEND_PIDFILE"

# ---- 4. 轮询健康检查 ----
HEALTH_URL="http://localhost:${SERVER_PORT}/health"
info "等待后端就绪（轮询 ${HEALTH_URL}，最多 30s）…"
MAX_WAIT=30
for ((i = 1; i <= MAX_WAIT; i++)); do
  if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
    die "后端进程已退出（多半是配置或 DB 连接问题），日志见 ${BACKEND_LOG}"
  fi
  if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
    info "后端就绪 (HTTP 200)"
    break
  fi
  [[ "$i" -eq "$MAX_WAIT" ]] && die "后端 ${MAX_WAIT}s 内未通过健康检查，日志见 ${BACKEND_LOG}"
  sleep 1
done

# ---- 5. 后台启动前端 ----
# 直接跑 vite（而非 npm run dev）：$! 即 vite 进程本身，stop.sh 的信号能直达，不被 npm wrapper 拦截。
[[ -x "$ROOT_DIR/web/node_modules/.bin/vite" ]] \
  || die "前端依赖未安装：请先在 web/ 下执行 npm install"
info "后台启动前端 → ${WEB_LOG}"
: > "$WEB_LOG"
cd "$ROOT_DIR/web"
nohup ./node_modules/.bin/vite >"$WEB_LOG" 2>&1 &
echo "$!" > "$WEB_PIDFILE"

# 等前端就绪（轮询 WEB_PORT，最多 15s；超时只 warn，不阻断——后端已起，前端日志可自查）
info "等待前端就绪（最多 15s）…"
for ((i = 1; i <= 15; i++)); do
  if curl -fsS "http://localhost:${WEB_PORT}" >/dev/null 2>&1; then
    info "前端就绪 (http://localhost:${WEB_PORT})"
    break
  fi
  [[ "$i" -eq 15 ]] && warn "前端 15s 内未就绪，可能仍在启动，见 ${WEB_LOG}"
  sleep 1
done

# ---- 完成 ----
FINISHED=1
info "启动完成：后端 http://localhost:${SERVER_PORT}  |  前端 http://localhost:${WEB_PORT}"
info "停止：./stop.sh    后端日志：tail -f ${BACKEND_LOG}    前端日志：tail -f ${WEB_LOG}"
exit 0
