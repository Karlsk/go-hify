-- +goose Up
-- spec 020 workflow react 节点：第八种节点类型（图内 mini-agent：模型 + mcp server∪tools
-- 并集工具集 + 可调轮数上限，执行经 platform/reactloop Generate 模式）；config COMMENT
-- 同步补 react 形态与 knowledge_retrieval.query 加法键（spec 020 FR-005）/ end.output_fields
--（spec 018 遗漏补注）。CHECK 重建同 00017/00020 同名模式（PG 无法向 CHECK 追加值）。

ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end','workflow','react'));

COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt,system_prompt?,temperature?,output_schema?} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k?,query?} / api={url,method,headers?,body?,timeout_sec?,ssl_verify?} / end={output?,output_fields?} / workflow={workflow_id,inputs} / react={model_id,prompt,system_prompt?,temperature?,server_ids?,tool_ids?,max_rounds?}（00022 加宽）；model_id / workflow_id / knowledge_base_id 等引用存在性由 service 校验（jsonb 内无法建 FK；workflow_id 另有 R11 保存期嵌套校验）；server_ids / tool_ids 为 mcp 并集绑定（执行期动态解析，不预检）';

-- +goose Down
ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end','workflow'));

COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k} / api={url,method,headers?,body?,timeout_sec?,ssl_verify?} / end={output?} / workflow={workflow_id,inputs}（00020 加宽）；model_id / workflow_id 等引用存在性由 service 校验（jsonb 内无法建 FK；workflow_id 另有 R11 保存期嵌套校验）';
