-- +goose Up
-- 节点类型加宽（2026-09-16 拍板）：新增 api（直接 HTTP 调用）/ end（显式终止，可选）。
-- 00016 的 inline CHECK 由 PG 自动命名 workflow_nodes_type_check，此处替换为六值版本；
-- 00016 文件本身不改（迁移只增不改）。表当前零写入方，替换零风险。

ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end'));

COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k} / api={url,method,headers?,body?,timeout_sec?,ssl_verify?} / end={output?}（00017 加宽）；model_id 等引用存在性由 service 经下游 api 校验（jsonb 内无法建 FK）';

-- +goose Down
-- 回滚收紧为四值：若已存在 api/end 行，约束加回会失败（先清理这些行再 down）。

ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval'));

COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k}；model_id 等引用存在性由 service 经下游 api 校验（jsonb 内无法建 FK）';
