-- executions 分区维护（幂等，由 deploy/backup 容器 cron 每日执行）
-- 对应 CLAUDE.md《大表处理策略》分区自动化：
--   1. 月初建下月分区（这里补建「当月 + 下月」，保证任何月份部署/重启都不缺当前分区）
--   2. 删 >90 天的旧分区（在线保留 90 天，历史从 PG 每日备份恢复）
-- 执行：psql -h postgres -U ${POSTGRES_USER} -d hify -f /partition_maintenance.sql

-- 1) 建当月与下月分区（已存在则跳过）
DO $$
DECLARE
    m date;
    pname text;
    pstart date;
    pend date;
BEGIN
    FOR m IN SELECT date_trunc('month', now())::date
             UNION SELECT (date_trunc('month', now()) + interval '1 month')::date
    LOOP
        pstart := date_trunc('month', m)::date;
        pend   := (date_trunc('month', m) + interval '1 month')::date;
        pname  := 'executions_' || to_char(pstart, 'YYYY_MM');
        IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = pname) THEN
            EXECUTE format('CREATE TABLE %I PARTITION OF executions FOR VALUES FROM (%L) TO (%L)',
                           pname, pstart, pend);
            RAISE NOTICE 'created partition %', pname;
        END IF;
    END LOOP;
END $$;

-- 2) 删 >90 天的旧分区（按分区名解析年月判断）
DO $$
DECLARE
    r record;
    pdate date;
BEGIN
    FOR r IN
        SELECT c.relname
        FROM pg_class c
        JOIN pg_inherits i ON i.inhrelid = c.oid
        JOIN pg_class p   ON i.inhparent = p.oid
        WHERE p.relname = 'executions'
          AND c.relname ~ '^executions_[0-9]{4}_[0-9]{2}$'
    LOOP
        pdate := to_date(replace(substring(r.relname FROM '_([0-9]{4}_[0-9]{2})$'), '_', '-'), 'YYYY-MM');
        IF pdate < date_trunc('month', now() - interval '90 days')::date THEN
            EXECUTE format('DROP TABLE %I', r.relname);
            RAISE NOTICE 'dropped expired partition %', r.relname;
        END IF;
    END LOOP;
END $$;
