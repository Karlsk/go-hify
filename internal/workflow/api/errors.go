package api

import "errors"

// ErrWorkflowNotFound 工作流不存在（404）。
var ErrWorkflowNotFound = errors.New("WORKFLOW_NOT_FOUND")
