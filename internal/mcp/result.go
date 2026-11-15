package mcp

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const imageOmitted = "[image omitted: this runner has no vision]"

// CallResult is one tool call's answer as the model sees it.
type CallResult struct {
	Text    string
	IsError bool
}

func resultOf(res *mcp.CallToolResult) CallResult {
	return CallResult{Text: contentText(res.Content), IsError: res.IsError}
}

func contentText(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, c := range content {
		parts = append(parts, contentPart(c))
	}
	return strings.Join(parts, "\n")
}

func contentPart(c mcp.Content) string {
	switch v := c.(type) {
	case *mcp.TextContent:
		return v.Text
	case *mcp.ImageContent, *mcp.AudioContent:
		return imageOmitted
	case *mcp.ResourceLink:
		return v.URI
	case *mcp.EmbeddedResource:
		if v.Resource == nil {
			return ""
		}
		return v.Resource.Text
	default:
		return ""
	}
}
