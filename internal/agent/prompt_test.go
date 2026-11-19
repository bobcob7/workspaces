package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreamble(t *testing.T) {
	t.Run("renders every field", func(t *testing.T) {
		text, err := Preamble(PreambleData{
			Dir:          "/work/repo",
			StepsLeft:    80,
			MinutesLeft:  60,
			Verdicts:     "PASS or FAIL",
			MCP:          true,
			Deferred:     "metrics: query, alerts",
			Unavailable:  []string{"billing", "search"},
			Instructions: "Run make test before you finish.",
		})
		require.NoError(t, err)
		assert.Contains(t, text, "You are a coding agent working in /work/repo, a git repository.")
		assert.Contains(t, text, "and the MCP tools listed.")
		assert.Contains(t, text, "MCP servers that failed to start: billing, search.")
		assert.Contains(t, text, "metrics: query, alerts")
		assert.Contains(t, text, "Budget left: 80 steps, 60 minutes.")
		assert.Contains(t, text, "verdict one of\n  PASS or FAIL")
		assert.Contains(t, text, "Run make test before you finish.")
	})
	t.Run("leaves out the MCP lines without MCP tools", func(t *testing.T) {
		text, err := Preamble(PreambleData{Dir: "/work/repo", StepsLeft: 1, MinutesLeft: 1, Verdicts: "PASS or FAIL"})
		require.NoError(t, err)
		assert.NotContains(t, text, "the MCP tools listed")
		assert.NotContains(t, text, "failed to start")
		assert.NotContains(t, text, "tool_search")
		assert.NotContains(t, text, "Project instructions")
	})
}
