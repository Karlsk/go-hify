-- +goose Up
-- kind 枚举收编：删除 'openai'，官方 OpenAI 并入 'openai_compatible'（base_url 空 = 默认官方端点）。
-- 动机：eino openai adapter 非 Azure 分支支持 BaseURL 覆盖（此前误判仅 Azure 生效），
-- 「openai」与「openai_compatible」协议同一套、只差 base_url，无需两个 kind。
-- openai_response（Responses API）待 eino-ext 底层 SDK 迁到官方 openai-go 后再引入。

-- 存量数据平移：kind='openai' 的 provider base_url 保持空串 → 默认官方端点，行为不变
UPDATE providers SET kind = 'openai_compatible' WHERE kind = 'openai';

-- CHECK 重建（00002 建的 providers_kind_check 收掉 openai）
ALTER TABLE providers DROP CONSTRAINT providers_kind_check;
ALTER TABLE providers ADD CONSTRAINT providers_kind_check
    CHECK (kind IN ('openai_compatible', 'claude', 'gemini', 'ollama'));

COMMENT ON COLUMN providers.kind IS 'openai_compatible/claude/gemini/ollama；鉴权方式与默认端点由 kind 派生；openai_compatible 的 base_url 空 = 官方 OpenAI 端点';
COMMENT ON COLUMN providers.base_url IS 'API 地址；空串 = kind 默认地址（代码常量表 kindDefaultBase；openai_compatible 默认 https://api.openai.com/v1）';

-- +goose Down
ALTER TABLE providers DROP CONSTRAINT providers_kind_check;
ALTER TABLE providers ADD CONSTRAINT providers_kind_check
    CHECK (kind IN ('openai', 'claude', 'gemini', 'ollama', 'openai_compatible'));
-- 无法区分平移前后的行，Down 只还原枚举不逆转数据（曾用官方 openai 的 provider 需手工改回）
UPDATE providers SET kind = 'openai' WHERE kind = 'openai_compatible' AND base_url = '';
COMMENT ON COLUMN providers.kind IS 'openai/claude/gemini/ollama/openai_compatible；鉴权方式与默认端点由 kind 派生';
COMMENT ON COLUMN providers.base_url IS 'API 地址；空串 = kind 默认地址（代码常量表）；openai_compatible 必填（service 校验）';
