package fakeollama

import (
	"slices"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

// truncateToContext drops the oldest messages that aren't system messages
// until the prompt fits in limit tokens, as Ollama does when a conversation
// exceeds the context window. The last message is always kept.
func truncateToContext(req openaiwire.ChatRequest, limit int) openaiwire.ChatRequest {
	msgs := slices.Clone(req.Messages)
	for promptTokens(msgs) > limit {
		i := slices.IndexFunc(msgs[:len(msgs)-1], isNotSystem)
		if i < 0 {
			break
		}
		msgs = slices.Delete(msgs, i, i+1)
	}
	req.Messages = msgs
	return req
}

func isNotSystem(m openaiwire.Message) bool {
	return m.Role != openaiwire.RoleSystem
}
