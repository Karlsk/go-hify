package api

import "errors"

// ErrWorkflowNotFound 工作流不存在（404）。
var ErrWorkflowNotFound = errors.New("WORKFLOW_NOT_FOUND")

// ErrWorkflowNameConflict 名称冲突（409，uq_workflows_name 23505 翻译）。
var ErrWorkflowNameConflict = errors.New("WORKFLOW_NAME_CONFLICT")

// ErrWorkflowNotPublished 未发布态不可执行（503；execute 时 draft/disabled 被拒）。
var ErrWorkflowNotPublished = errors.New("WORKFLOW_NOT_PUBLISHED")
