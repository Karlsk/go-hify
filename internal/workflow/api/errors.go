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
