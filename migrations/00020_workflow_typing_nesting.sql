-- +goose Up
-- spec 08 workflow 分型与子工作流嵌套：workflows 三列 + 节点类型/触发源 CHECK 加值 + parent_run_id。
-- 分型（2026-09-18 拍板）：chat=对话管道终答 / task=string→string 可组合任务函数（可被嵌套）；
-- 存量绑定全为管道用法，回填 'chat' 语义精确。type 不可变（Update 携带即拒 400）。

ALTER TABLE workflows ADD COLUMN type text NOT NULL DEFAULT 'chat'
    CHECK (type IN ('chat','task'));
ALTER TABLE workflows ADD COLUMN input_schema  jsonb;
ALTER TABLE workflows ADD COLUMN output_schema jsonb;

-- 第七种节点类型（00017 同名重建模式：PG 无法向 CHECK 追加值，重建是唯一路径）
ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end','workflow'));

-- 触发源加 'workflow'（00019 inline CHECK 由 PG 自动命名 workflow_runs_trigger_source_check，同模式重建）
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_runs_trigger_source_check;
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_runs_trigger_source_check
    CHECK (trigger_source IN ('console','chat','workflow'));

ALTER TABLE workflow_runs ADD COLUMN parent_run_id bigint;

COMMENT ON COLUMN workflows.type IS '消费路径分型（spec 08）：chat=对话管道终答 / task=string→string 可组合任务函数（仅 task 型可被 sub-workflow 节点引用）；不可变（Update 携带 type 即 400，换型=删了重建）；存量回填 chat';
COMMENT ON COLUMN workflows.input_schema IS 'task 型入参契约（简化形态 [{name,type,required,description}]，type ∈ string/number/boolean）；可空=单一 string 入参；chat 型强制 NULL（service 层强不变量）';
COMMENT ON COLUMN workflows.output_schema IS 'task 型出参契约（形态同 input_schema）；executeChild 收尾校验子终稿（required 缺失/类型不符=图缺陷 400）；可空；chat 型强制 NULL';
COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k} / api={url,method,headers?,body?,timeout_sec?,ssl_verify?} / end={output?} / workflow={workflow_id,inputs}（00020 加宽）；model_id / workflow_id 等引用存在性由 service 校验（jsonb 内无法建 FK；workflow_id 另有 R11 保存期嵌套校验）';
COMMENT ON COLUMN workflow_runs.trigger_source IS '直接触发方式：console / chat / workflow（被 sub-workflow 节点嵌套执行）；与 conversation_id/message_id（因果归属）正交——子 run 也带父透传的会话引用';
COMMENT ON COLUMN workflow_runs.parent_run_id IS '父收尾统一回填的父 run 弱引用（无 FK）；append-only 一次窄 UPDATE 例外（db_model 决策 #16）；子 run trigger_source=workflow、conversation_id/message_id 与父相同（透传）';

-- +goose Down
-- 回滚：若已存在 type='task' 行、'workflow' 节点行或 trigger_source='workflow' run 行，
-- 收紧的约束加回会失败（先清理这些行再 down）。parent_run_id 回填值随列删除一并丢失。

ALTER TABLE workflow_runs DROP COLUMN parent_run_id;

ALTER TABLE workflow_runs DROP CONSTRAINT workflow_runs_trigger_source_check;
ALTER TABLE workflow_runs ADD CONSTRAINT workflow_runs_trigger_source_check
    CHECK (trigger_source IN ('console','chat'));

ALTER TABLE workflow_nodes DROP CONSTRAINT workflow_nodes_type_check;
ALTER TABLE workflow_nodes ADD CONSTRAINT workflow_nodes_type_check
    CHECK (type IN ('llm','tool','condition','knowledge_retrieval','api','end'));

COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k} / api={url,method,headers?,body?,timeout_sec?,ssl_verify?} / end={output?}（00017 加宽）；model_id 等引用存在性由 service 经下游 api 校验（jsonb 内无法建 FK）';

ALTER TABLE workflows DROP COLUMN output_schema;
ALTER TABLE workflows DROP COLUMN input_schema;
ALTER TABLE workflows DROP COLUMN type;
