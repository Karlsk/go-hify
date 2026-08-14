-- +goose Up
-- demo_items：标准 CRUD 参照实现（任务 #6 流程验证；非业务数据）。
-- provider / agent 等真实模块落地时按本表 + internal/demo 四层结构起稿；
-- 真实模块就绪后可整体移除（goose down 回滚本文件即删表）。
CREATE TABLE demo_items (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL,
    status     text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE demo_items IS '标准 CRUD 参照实现（demo），非业务数据；仅用于演示 api/service/store/handler 四层标准流程';
COMMENT ON COLUMN demo_items.name IS '条目名称';
COMMENT ON COLUMN demo_items.status IS '状态枚举：draft=草稿 / active=生效 / archived=归档（text + CHECK，见数据库规范）';

-- +goose Down
DROP TABLE IF EXISTS demo_items;
