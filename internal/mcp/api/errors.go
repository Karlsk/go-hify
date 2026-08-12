package api

import "errors"

// ErrMCPServerNotFound MCP 服务器不存在（404）。
var ErrMCPServerNotFound = errors.New("MCP_SERVER_NOT_FOUND")
