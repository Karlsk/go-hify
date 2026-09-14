-- +goose Up
-- 软删除退役（决策修订）：可逆下架统一由 enabled 开关承担（可见、可恢复），
-- 删除 = 真删（级联清理）；误删兜底走 PG 每日备份。
-- ⚠️ 迁移前 pre-check：若存在「软删 agent 仍有会话」，下方 DELETE 会被
-- conversations FK RESTRICT 挡住（迁移失败回滚）——先手动处理这些行：
--   SELECT a.id, a.name FROM agents a
--     JOIN conversations c ON c.agent_id = a.id
--    WHERE a.deleted_at IS NOT NULL;

-- documents：历史软删行按当初删除意图收尾（其 chunks 已在软删事务中物理删除，
-- 再清一次属防御）；deleted_at 退役，enabled 上位（深度停用 = 删向量保内容）。
-- idx_documents_active（partial WHERE deleted_at IS NULL）随 DROP COLUMN 被 PG
-- 依赖级联自动删除，无须（也不能）显式 DROP INDEX。
DELETE FROM document_chunks WHERE document_id IN (SELECT id FROM documents WHERE deleted_at IS NOT NULL);
DELETE FROM documents WHERE deleted_at IS NOT NULL;
ALTER TABLE documents DROP COLUMN deleted_at;
ALTER TABLE documents ADD COLUMN enabled boolean NOT NULL DEFAULT true;
-- 单列 kb 索引升级为复合（keyset 列表 WHERE knowledge_base_id = ? AND id < ? ORDER BY id DESC；
-- 最左前缀仍覆盖纯 kb 过滤），名字沿用旧单列索引名
DROP INDEX idx_documents_kb;
CREATE INDEX idx_documents_kb ON documents (knowledge_base_id, id);
COMMENT ON TABLE documents IS '上传文档：TXT/MD 元信息 + 原文（大文本自动 TOAST，查询不要 SELECT *）；无软删——深度停用走 enabled（删向量保内容），删除即真删';
COMMENT ON COLUMN documents.enabled IS '启用开关：false=深度停用（向量分块已物理删除、内容保留，检索不命中），重新启用自动重建索引';

-- agents：历史软删行收尾（绑定行 agent_tools / agent_knowledge_bases 由 FK CASCADE 清理）；
-- deleted_at 退役，可逆下架由既有 enabled 承担（停用 = 新会话被拒，一键恢复）。
-- idx_agents_active（partial WHERE deleted_at IS NULL）同样随 DROP COLUMN 级联自动删除。
DELETE FROM agents WHERE deleted_at IS NOT NULL;
ALTER TABLE agents DROP COLUMN deleted_at;
COMMENT ON TABLE agents IS 'Agent 配置：系统提示词 + 主/备用模型 + 运行参数 + 绑定 MCP 工具与知识库；无软删——停用走 enabled（新会话被拒），删除即真删（绑定级联清理，有历史会话挡删）；不存上下文轮数（归 chat 引擎，token 预算实现）';

-- +goose Down
-- 仅表结构可逆；Up 中清理的历史软删数据不恢复。
ALTER TABLE agents ADD COLUMN deleted_at timestamptz;
CREATE INDEX idx_agents_active ON agents (updated_at, id) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_documents_kb;
CREATE INDEX idx_documents_kb ON documents (knowledge_base_id);
DROP INDEX IF EXISTS idx_documents_active;
ALTER TABLE documents DROP COLUMN IF EXISTS enabled;
ALTER TABLE documents ADD COLUMN deleted_at timestamptz;
CREATE INDEX idx_documents_active ON documents (knowledge_base_id, id) WHERE deleted_at IS NULL;
