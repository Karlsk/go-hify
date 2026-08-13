#!/usr/bin/env bash
#
# stop.sh — 优雅停止 start.sh 启动的后端与前端。
#
# 按 logs/hify.pid、logs/web.pid 找进程：SIGTERM → 等待（最多 GRACE 秒）→ 仍存活则 SIGKILL。
# PID 文件缺失或进程已不在，视为已停止并清理残留文件。
#
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="$ROOT_DIR/logs"
GRACE="${GRACE:-10}" # SIGTERM 后等待秒数，超时才 SIGKILL；可用 GRACE=2 ./stop.sh 覆盖

if [[ -t 1 ]]; then
  RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[0;33m'; NC=$'\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; NC=''
fi
info() { printf "%s==>%s %s\n" "$GREEN" "$NC" "$*"; }
warn() { printf "%s!!%s %s\n" "$YELLOW" "$NC" "$*"; }

# stop_pid <名称> <pidfile>
stop_pid() {
  local name="$1" pidfile="$2"
  local pid

  if [[ ! -f "$pidfile" ]]; then
    warn "${name}：无 PID 文件（未运行或已停止）"
    return 0
  fi
  pid="$(cat "$pidfile" 2>/dev/null | tr -dc '0-9')"
  if [[ -z "$pid" ]]; then
    rm -f "$pidfile"
    warn "${name}：PID 文件为空，已清理"
    return 0
  fi
  if ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$pidfile"
    info "${name} (PID ${pid})：进程已不在，清理 PID 文件"
    return 0
  fi

  info "${name} (PID ${pid})：SIGTERM…"
  kill -TERM "$pid" 2>/dev/null || true
  for ((i = 0; i < GRACE; i++)); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done

  if kill -0 "$pid" 2>/dev/null; then
    warn "${name} (PID ${pid})：${GRACE}s 未退出，SIGKILL"
    kill -KILL "$pid" 2>/dev/null || true
    sleep 1
  else
    info "${name} (PID ${pid})：已停止"
  fi
  rm -f "$pidfile"
}

stop_pid "后端" "$LOG_DIR/hify.pid"
stop_pid "前端" "$LOG_DIR/web.pid"

info "完成"
