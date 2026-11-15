package fakeollama

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

func wordsOf(n int) string {
	return strings.TrimSpace(strings.Repeat("word ", n))
}

func rolesOf(msgs []openaiwire.Message) []string {
	roles := make([]string, 0, len(msgs))
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	return roles
}

func TestTruncateToContext(t *testing.T) {
	t.Run("drops the oldest messages until the prompt fits", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("system", "be brief"), msg("user", wordsOf(40)), msg("assistant", wordsOf(40)), msg("user", wordsOf(5))}}
		got := truncateToContext(req, 50)
		assert.Equal(t, []string{"system", "assistant", "user"}, rolesOf(got.Messages))
		assert.LessOrEqual(t, promptTokens(got.Messages), 50)
	})
	t.Run("keeps system messages", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("system", wordsOf(10)), msg("user", wordsOf(60)), msg("user", "last")}}
		got := truncateToContext(req, 20)
		assert.Equal(t, []string{"system", "user"}, rolesOf(got.Messages))
		assert.Equal(t, "last", got.Messages[1].Text())
	})
	t.Run("keeps the last message even when it alone is too long", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "first"), msg("user", wordsOf(80))}}
		got := truncateToContext(req, 50)
		require.Len(t, got.Messages, 1)
		assert.Equal(t, wordsOf(80), got.Messages[0].Text())
	})
	t.Run("returns the request unchanged within the limit", func(t *testing.T) {
		req := openaiwire.ChatRequest{Model: "fake-chat", Messages: []openaiwire.Message{msg("user", "one two")}}
		got := truncateToContext(req, 50)
		assert.Equal(t, req, got)
	})
	t.Run("doesn't change the caller's messages", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", wordsOf(40)), msg("user", "last")}}
		truncateToContext(req, 1)
		assert.Len(t, req.Messages, 2)
	})
}
