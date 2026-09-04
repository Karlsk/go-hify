#!/bin/sh
# 每日备份（backup 容器 cron 每天 02:00 调用）：pg_dump 自定义格式落 backups 卷，保留 14 天
# 对应 CLAUDE.md《部署架构》backup 容器职责；PGPASSWORD / POSTGRES_USER 由容器环境提供
set -eu

BACKUP_DIR=/backups

# 职责单一：只做备份。executions 分区维护已迁至应用内后台任务
#（internal/platform/logging/partition.go，随 hify 服务每日执行，dev 同样生效）。

pg_dump -h postgres -U "${POSTGRES_USER}" -d hify -Fc \
  -f "${BACKUP_DIR}/hify_$(date +%F_%H%M%S).dump"

# 保留 14 天（CLAUDE.md：PG 每日备份保留 7-14 天）
find "${BACKUP_DIR}" -name 'hify_*.dump' -mtime +14 -delete

echo "[$(date '+%F %T')] backup done"
