# Agent 知识库绑定 CRUD 上线

**日期**: 2026-09-14
**状态**: 已完成
**关联**: [rag_injection_spec.md](../chat/rag_injection_spec.md) §3 前置 A

## 变更概述

agent_knowledge_bases 表（迁移 00004）的 CRUD 全链路上线：agent 创建/更新可绑 KB、详情含 KB 绑定 id、列表含 kb_count 聚合列。

## 改动范围

| 层 | 文件 | 变更 |
|---|---|---|
| api | agent/api/schema.go | CreateAgentReq/UpdateAgentReq 增 `knowledge_base_ids`；AgentDetailSchema 增字段；AgentListItem 增 `kb_count`；MaxKBBindings=10；validateAgent 增 kb 重复检查 |
| api | agent/api/errors.go | 新增 `ErrKnowledgeBaseNotFound`（FK 23503 翻译，与 ragapi 同码各持一份） |
| service | agent/service/model.go | 新增 `AgentKnowledgeBase` model（复合 PK，双向 CASCADE） |
| service | agent/service/service.go | Store 接口增 4 方法；Create/Update WithTx 内增 CreateKBs（FK → ErrKnowledgeBaseNotFound）；Get 增 ListKBIDsByAgent；withAggregates 增 CountKBsByAgentIDs；toDetailSchema 签名加 kbIDs |
| store | agent/store/store.go | 实现 ListKBIDsByAgent / CountKBsByAgentIDs / DeleteKBsByAgent / CreateKBs |
| handler | agent/handler/handler.go | failAgent 增 ErrKnowledgeBaseNotFound → 404 映射 |
| 前端 | web/src/api/agent.ts | AgentItem 增 kb_count；AgentDetail 增 knowledge_base_ids；AgentSaveData 增字段 |
| 前端 | web/src/views/agent/AgentList.vue | 列表增知识库数列；编辑弹窗增「知识库绑定」tab（el-select multiple，数据源 getKnowledgeBaseList） |

## 设计决策

- **agent ↛ rag 依赖方向**：KB 存在性校验不走 rag api，靠 FK 23503 翻译 ErrKnowledgeBaseNotFound（与 ErrToolNotFound 同款先例）。
- **MaxKBBindings=10**：对齐 ragapi.MaxRetrieveKBs（检索路径硬限）。
- **empty [] 不返 null**：ToolIDs / KnowledgeBaseIDs 一律 make 初始化，序列化成 `[]`（接口规范《空值约定》）。

## 测试覆盖

- agent/api: 85.0%（schema 验证、哨兵码、JSON 序列化钉值）
- agent/service: 82.6%（Create/Get/Update 的 KB 绑定 + FK 翻译 + 列表 kb_count 聚合）
- agent/store: 77.9%（KB 四方法 sqlmock 测试）
- agent/handler: 87.7%（ErrKnowledgeBaseNotFound → 404 映射）
