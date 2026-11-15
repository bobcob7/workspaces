package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

func TestContentPart(t *testing.T) {
	tests := []struct {
		name    string
		content mcp.Content
		want    string
	}{
		{"text is itself", &mcp.TextContent{Text: "hello"}, "hello"},
		{"image is a placeholder", &mcp.ImageContent{Data: []byte("x"), MIMEType: "image/png"}, imageOmitted},
		{"audio is a placeholder", &mcp.AudioContent{Data: []byte("x"), MIMEType: "audio/wav"}, imageOmitted},
		{"resource link is its URI", &mcp.ResourceLink{URI: "file:///a"}, "file:///a"},
		{"embedded text resource is its text", &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "x", Text: "body"}}, "body"},
		{"embedded resource without contents is empty", &mcp.EmbeddedResource{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, contentPart(tt.content))
		})
	}
}

func TestContentText(t *testing.T) {
	t.Run("joins the parts with newlines", func(t *testing.T) {
		content := []mcp.Content{&mcp.TextContent{Text: "a"}, &mcp.TextContent{Text: "b"}}
		assert.Equal(t, "a\nb", contentText(content))
	})
	t.Run("no content is empty", func(t *testing.T) {
		assert.Empty(t, contentText(nil))
	})
}
