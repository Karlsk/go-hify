package api

import "errors"

// ErrAgentNotFound Agent 不存在（404）。
var ErrAgentNotFound = errors.New("AGENT_NOT_FOUND")
