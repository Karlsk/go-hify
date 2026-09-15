-- +goose Up
-- 工作流三表：一份 DSL 拆开存——基本信息 / 节点 / 连线；保存时拆写（事务内整图替换），加载时组装。
-- 先替换 00006 的旧单表 workflows（config jsonb 整图存储 = db_model 决策 #1 否决的形态）：
-- 全仓无代码读写旧表、无 FK 指向它，替换零风险（2026-09-15 用户拍板：替换式重建）。
DROP TABLE IF EXISTS workflows;

CREATE TABLE workflows (
    id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    start_node_key text        NOT NULL,
    status         text        NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft','published','disabled')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_workflows_name ON workflows (name);

CREATE TABLE workflow_nodes (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workflow_id bigint      NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    node_key    text        NOT NULL,
    type        text        NOT NULL CHECK (type IN ('llm','tool','condition','knowledge_retrieval')),
    name        text        NOT NULL DEFAULT '',
    config      jsonb       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- node_key 图内唯一；出边与 {{表达式}} 都引用它；前导列 workflow_id 兼作外键索引
CREATE UNIQUE INDEX uq_workflow_nodes_wf_key ON workflow_nodes (workflow_id, node_key);

CREATE TABLE workflow_edges (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workflow_id     bigint      NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    source_node_key text        NOT NULL,
    target_node_key text        NOT NULL,
    condition       text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_workflow_edges_workflow_id ON workflow_edges (workflow_id);

COMMENT ON TABLE  workflows IS '简版工作流（线性+条件分支）：基本信息+入口；节点/连线分表存，编辑=事务内整图替换';
COMMENT ON COLUMN workflows.start_node_key IS '入口节点 node_key，执行从此开始；保存时图校验保证存在';
COMMENT ON COLUMN workflows.status IS 'draft=新建未发布（不可执行）/ published=可执行 / disabled=停用（图保留，恢复再 publish）；编辑不降级（无版本化，改完即对新执行生效）';
COMMENT ON TABLE  workflow_nodes IS '节点一行一个；config 格式由 type 判别，入库前经 api.ParseNodeConfig 按 type 强校验，jsonb 只兜 JSON 语法';
COMMENT ON COLUMN workflow_nodes.config IS 'llm={model_id,prompt} / tool={tool_id,args} / condition={expression} / knowledge_retrieval={knowledge_base_id,top_k}；model_id 等引用存在性由 service 经下游 api 校验（jsonb 内无法建 FK）';
COMMENT ON TABLE  workflow_edges IS '连线：source→target；condition 非空=匹配 source(condition 节点)的求值结果，NULL=无条件直走';
COMMENT ON COLUMN workflow_edges.condition IS '分支匹配值（true/false/分类标签）；condition 节点出边必填、其余节点出边必 NULL（应用层校验）';

-- +goose Down
DROP TABLE IF EXISTS workflow_edges;
DROP TABLE IF EXISTS workflow_nodes;
DROP TABLE IF EXISTS workflows;

-- 按迁移 00006 原样建回旧单表（对称回滚；再次 Up 会重新替换为新三表）。
CREATE TABLE workflows (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    config      jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_workflows_name ON workflows (name);

COMMENT ON TABLE workflows IS '简版工作流：JSON 配置（线性 + 条件分支），非可视化编排';
COMMENT ON COLUMN workflows.config IS '节点与边的 JSON 配置（jsonb）；整块存取，不加 GIN';
