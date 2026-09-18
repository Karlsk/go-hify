package api

import "errors"

// ErrWorkflowNotFound 工作流不存在（404）。
var ErrWorkflowNotFound = errors.New("WORKFLOW_NOT_FOUND")

// ErrWorkflowNameConflict 名称冲突（409，uq_workflows_name 23505 翻译）。
var ErrWorkflowNameConflict = errors.New("WORKFLOW_NAME_CONFLICT")

// ErrWorkflowNotPublished 未发布态不可执行（503；execute 时 draft/disabled 被拒）。
var ErrWorkflowNotPublished = errors.New("WORKFLOW_NOT_PUBLISHED")

// ErrWorkflowInUse 工作流被 agent 绑定，无法删除（409；agents.workflow_id FK
// RESTRICT 的 23503 翻译，spec 05）。先解绑（PUT agent 体缺省 workflow_id）或删除
// 引用它的 agent 后可删；nodes / edges 随删除 CASCADE 清理。
var ErrWorkflowInUse = errors.New("WORKFLOW_IN_USE")

// ErrWorkflowExecutionFailed 执行引擎环境限制类错误（500，O4 二分法，spec 06）：
// api 节点 SSRF 拦截 / 工作流总时长超 5min。图缺陷类走 errs.ErrValidationFailed
// 既有行（400，message 带 node <key>: 前缀）；失败节点定位统一靠错误 message。
var ErrWorkflowExecutionFailed = errors.New("WORKFLOW_EXECUTION_FAILED")
