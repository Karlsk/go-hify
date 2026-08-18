-- +goose Up
-- providers：模型提供商配置（provider 模块）；健康状态在 provider_health，不混本表（缓存隔离）
CREATE TABLE providers (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               text NOT NULL,
    kind               text NOT NULL
                       CHECK (kind IN ('openai', 'claude', 'gemini', 'ollama', 'openai_compatible')),
    base_url           text NOT NULL DEFAULT '',
    auth_config        jsonb NOT NULL DEFAULT '{}'::jsonb,
    api_key_rotated_at timestamptz,
    extra_config       jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled            boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_providers_name ON providers (name);

COMMENT ON TABLE  providers IS '模型提供商配置：五类统一接入；本表低频写，是 Cache-Aside 缓存对象';
COMMENT ON COLUMN providers.kind IS 'openai/claude/gemini/ollama/openai_compatible；鉴权方式与默认端点由 kind 派生';
COMMENT ON COLUMN providers.base_url IS 'API 地址；空串 = kind 默认地址（代码常量表）；openai_compatible 必填（service 校验）';
COMMENT ON COLUMN providers.auth_config IS '鉴权材料：键白名单，密钥只存密文 {"api_key_encrypted": "base64(AES-256-GCM)"}；ollama 为 {}';
COMMENT ON COLUMN providers.api_key_rotated_at IS '密钥最近一次轮换时间';
COMMENT ON COLUMN providers.extra_config IS 'Profile 覆盖与连接差异，白名单键：bulkhead / ttft_seconds / keep_alive(仅 ollama)';

-- models：提供商下的模型
CREATE TABLE models (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_id       bigint NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    name              text NOT NULL,
    model_id          text NOT NULL,
    capability        text NOT NULL CHECK (capability IN ('chat', 'embedding')),
    context_window    bigint,
    max_output_tokens bigint,
    input_price       numeric(12,4),
    output_price      numeric(12,4),
    embedding_dim     integer,
    enabled           boolean NOT NULL DEFAULT true,
    source            text NOT NULL DEFAULT 'manual' CHECK (source IN ('discovered', 'manual')),
    extra_params      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_models_provider_model_id ON models (provider_id, model_id);
-- 按提供商查模型走 uq 最左前缀，不另建单列索引

COMMENT ON TABLE  models IS '提供商下的模型：chat 供对话/工作流，embedding 供 RAG 向量化';
COMMENT ON COLUMN models.name IS '展示名，如 GPT-4o；不参与唯一性';
COMMENT ON COLUMN models.model_id IS '调用时传给 API 的标识，如 gpt-4o；注意 agents.model_id 等外键指向本表 id，与本品列语义不同';
COMMENT ON COLUMN models.capability IS '能力类型：chat / embedding';
COMMENT ON COLUMN models.context_window IS '上下文窗口（token）；NULL = 未知';
COMMENT ON COLUMN models.max_output_tokens IS '单次生成上限（token）；Claude 协议 max_tokens 必填';
COMMENT ON COLUMN models.input_price IS '输入价（USD/百万 token）；NULL = 不计费（本地/未配置），budget 护栏换算用';
COMMENT ON COLUMN models.output_price IS '输出价（USD/百万 token）；NULL = 不计费';
COMMENT ON COLUMN models.embedding_dim IS '嵌入维度（仅 capability=embedding）；建 KB 校验，vector(维度) 不可改';
COMMENT ON COLUMN models.enabled IS '模型级停用：发现拉回的冷门模型可停用，不影响已绑定 agent';
COMMENT ON COLUMN models.source IS 'discovered=自动发现（sync 只增改不删）/ manual=手动录入';
COMMENT ON COLUMN models.extra_params IS '模型级参数，白名单键：think_level（思考档位）等';

-- provider_health：供应商健康（1:1，探测写与配置缓存隔离；含定时探测）
CREATE TABLE provider_health (
    provider_id     bigint PRIMARY KEY REFERENCES providers (id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'unknown'
                    CHECK (status IN ('unknown', 'up', 'degraded', 'down')),
    last_check_at   timestamptz,
    last_success_at timestamptz,
    fail_count      integer NOT NULL DEFAULT 0,
    latency_ms      integer,
    error_message   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE  provider_health IS '探测结果（定时 + 手动）；熔断/槽位等运行时状态在 platform/llm 内存，不落库';
COMMENT ON COLUMN provider_health.status IS 'unknown=从未探测；up=正常；degraded=慢(>3s)或失败未达阈值；down=连续失败≥3';
COMMENT ON COLUMN provider_health.fail_count IS '连续探测失败次数，成功清零';

-- +goose Down
DROP TABLE IF EXISTS provider_health;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
