-- +goose Up
-- providers：模型提供商配置（provider 模块）
CREATE TABLE providers (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name              text NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('openai', 'claude', 'gemini', 'ollama')),
    base_url          text NOT NULL DEFAULT '',
    api_key_encrypted text NOT NULL DEFAULT '',
    enabled           boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_providers_name ON providers (name);

COMMENT ON TABLE providers IS '模型提供商配置：OpenAI/Claude/Gemini/Ollama 统一接入';
COMMENT ON COLUMN providers.kind IS '提供商类型：openai/claude/gemini/ollama';
COMMENT ON COLUMN providers.base_url IS 'API 地址；空串 = 用该类型的默认地址';
COMMENT ON COLUMN providers.api_key_encrypted IS 'API Key 密文（主密钥来自 env）；Ollama 无鉴权存空串';
COMMENT ON COLUMN providers.enabled IS '停用后不再对该提供商发请求';

-- models：提供商下的模型（chat 与 embedding 两类能力）
CREATE TABLE models (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_id    bigint NOT NULL REFERENCES providers (id) ON DELETE RESTRICT,
    name           text NOT NULL,
    capability     text NOT NULL CHECK (capability IN ('chat', 'embedding')),
    context_window integer NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_models_provider ON models (provider_id);
-- 业务唯一键：同一提供商下模型名唯一
CREATE UNIQUE INDEX uq_models_provider_name ON models (provider_id, name);

COMMENT ON TABLE models IS '提供商下的模型：chat 用于对话/工作流 LLM 节点，embedding 用于 RAG 向量化';
COMMENT ON COLUMN models.capability IS '能力类型：chat / embedding';
COMMENT ON COLUMN models.context_window IS '上下文窗口（token 数）；0 = 未知，按 provider 默认处理';

-- +goose Down
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
