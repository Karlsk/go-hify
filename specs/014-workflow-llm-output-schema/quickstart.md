# Quickstart: LLM 节点输出字段声明与变量下拉展开

**Feature**: [spec.md](./spec.md) | **Date**: 2026-09-23 | **Phase 1**

 runnable 验证场景——后端自动化为主，前端人工走查为辅（manual-test 增补小节为验收文档，不在本篇复制）。

## 前置

- 分支 `014-workflow-llm-output-schema`；基线全绿（build/vet/test）。
- `make migrate-status`：20 条 applied、下一号 00021——本篇**零迁移**，动任何代码前后该状态不变。

## 场景 A：后端自动化（stub llm client + stub store，零真实网络/LLM/PG）

```bash
go test ./internal/workflow/... -race -count=1 -run 'OutputSchema|TestCallLLM|TestValidateLLMOutput|TestBuildJSONDirective' -v
```

预期覆盖（对应 SC-002/003/005）：

1. **序列化往返三态**：LLMConfig 带 OutputSchema / 空数组 / 缺省——仅带值态产出 `output_schema` 键。
2. **Validate**：name 空、重名、type 越枚举 → 拒；无声明存量 config → 零新增拒绝。
3. **注入**：声明非空 → stub client 收到的 user 消息以冻结指令结尾（含全部字段行）；未声明 → 消息与 node_in 与现状逐字节一致。
4. **校验四分支**：合规 JSON（含多余字段）通过；缺必填 / 类型不符 / 非 JSON（纯文本、null、数组、围栏）→ 节点失败，错误含节点 key 与字段名。
5. **嵌套**：子工作流内声明节点同注入同校验（子图独立游走）。

全量门禁：

```bash
go build ./... && go vet ./... && go test ./... -race -count=1
go test ./internal/workflow/... -race -cover   # 覆盖率 ≥80% 维持
```

## 场景 B：前端双门禁

```bash
cd web && npm run type-check && npm run build
```

全绿即过（含 output_schema 类型与 Inspector 改动的类型面）。

## 场景 C：人工冒烟（make start 后，详单见 manual-test 增补小节）

1. 编辑器：llm 节点声明 code（string，必填）/ reason（string，可选）→ 保存 → 详情/编辑回读声明仍在（config 键 `output_schema`）。
2. 下拉：下游任意节点模板字段 → upstream 分组出现 `llm1.code（string·必填）` 等条目 → 点击插入 `{{llm1.code}}`；未声明节点仍只有整体条目。
3. 试运行：声明节点跑通 → 下游下钻取到值；模型回非 JSON → 节点失败、错误文案含节点与字段定位。
4. 回归：既有变量下拉分组、既有表单行为无变化。

## 预期结果汇总

| 场景 | 通过判据 |
|---|---|
| A | 指定 -run 测试全绿 + 全量门禁全绿 + 覆盖率 ≥80% |
| B | type-check && build 零报错 |
| C | 声明持久化 / 条目展开插入 / 校验错误定位 / 零回归四项人工确认 |
