# Chat RAG 检索注入上线

**日期**: 2026-09-14
**状态**: 已完成
**关联**: [rag_injection_spec.md](rag_injection_spec.md) §4 主任务 B

## 变更概述

chat 发消息时按 Agent 绑定的知识库自动检索注入 system prompt：用户消息向量化 → rag Retrieve（topK=3，多 KB ≤10）→ 过滤相似度 ≥0.75 → 拼接参考资料段进 system prompt。无绑定不检索；检索失败降级照常对话。

## 改动范围

| 层 | 文件 | 变更 |
|---|---|---|
| service | chat/service/service.go | 新增 ragRetriever 小接口（Retrieve 单方法收窄）；New 签名加第六参；chatService 增 rags 字段 |
| service | chat/service/turn.go | 常量 ragInjectionTopK=3 / ragMinSimilarity=0.75；新增 buildSystemPrompt 私有方法（五分支：无绑定/检索失败降级/全滤返原样/部分滤/拼接）；新增 augmentSystemPrompt 纯函数（模板逐字对齐 spec §4.4）；assembleMessages 签名从 agent 参数改 systemPrompt string |
| 装配 | internal/app/server.go | chatsvc.New 调用加 ragSvc 第六参 |
| 测试 | chat/service/doubles_test.go | 新增 stubRags（记录 calls/lastReq）；newServiceWithAgent 注入 stubRags；chatServiceForTest 聚合 rags 句柄 |
| 测试 | chat/service/turn_test.go | TestAugmentSystemPrompt 表驱动（模板逐字、D3 空 base、chunk 含换行）；TestBuildSystemPrompt 五分支（无绑定零调用、TopK=3 + KB id 数值化、检索失败降级、全滤、D3 空 prompt 有命中）；TestStreamRAGInjection 端到端（增强后 system prompt 发给 LLM） |

## 设计决策

- **D2 降级**：rag Retrieve 任何错误（EmbeddingModelMismatch / ErrProviderBusy / RateLimited / DB 错）一律降级照常对话 + WARN 日志——RAG 不是对话硬依赖。
- **D3 空 prompt 有命中**：仍注入资料段，以「请基于以下参考资料回答用户问题。」开头。
- **assembleMessages 签名改为 systemPrompt string**：将 IO（检索）提到调用方，函数回归纯函数——易于测试、职责清晰。
- **executions.input 自然含增强后 prompt**：排障可见真实输入，符合 executions 定位。

## 测试覆盖

- chat/service: 93.3%（新增 3 个测试函数 + 既有用例平移）

## 模板（逐字对齐需求原文）

```
{Agent 原始 Prompt}

请基于以下参考资料回答用户问题。
如果资料中没有相关信息，直接说"我没有找到相关资料"，不要编造。

【参考资料】
[1] {chunk1内容}
[2] {chunk2内容}
```
