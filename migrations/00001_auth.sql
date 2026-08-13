-- +goose Up
-- pgvector 扩展：向量召回依赖（幂等）。Down 不删除扩展——其他表可能仍依赖。
CREATE EXTENSION IF NOT EXISTS vector;

-- users：最简登录账号（auth 模块）
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_users_username ON users (username);

COMMENT ON TABLE users IS '最简登录账号：用户名 + 密码哈希；登录 session 在 Redis（data-model.md Redis-only），不建表';
COMMENT ON COLUMN users.username IS '登录名，业务唯一键';
COMMENT ON COLUMN users.password_hash IS 'bcrypt 哈希，禁止存明文';

-- +goose Down
DROP TABLE IF EXISTS users;
