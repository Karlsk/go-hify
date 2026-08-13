#!/bin/sh
# 每日备份（backup 容器 cron 每天 02:00 调用）：pg_dump 自定义格式落 backups 卷，保留 14 天
# 对应 CLAUDE.md《部署架构》backup 容器职责；PGPASSWORD / POSTGRES_USER 由容器环境提供
set -eu

BACKUP_DIR=/backups

# executions 分区维护（建当月/下月分区、删 >90 天旧分区，幂等）
psql -h postgres -U "${POSTGRES_USER}" -d hify -f /partition_maintenance.sql

pg_dump -h postgres -U "${POSTGRES_USER}" -d hify -Fc \
  -f "${BACKUP_DIR}/hify_$(date +%F_%H%M%S).dump"

# 保留 14 天（CLAUDE.md：PG 每日备份保留 7-14 天）
find "${BACKUP_DIR}" -name 'hify_*.dump' -mtime +14 -delete

echo "[$(date '+%F %T')] backup done"
