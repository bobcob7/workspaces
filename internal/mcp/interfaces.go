package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:generate go tool moq -out moq_test.go . session

// session is the part of an MCP client session that a Server uses.
type session interface {
	ListTools(ctx context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
	Close() error
}
