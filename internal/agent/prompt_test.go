package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestLoadInstructions(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, dir, name, text string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
	}
	t.Run("reads the repo's then the workspace's files, then the user's", func(t *testing.T) {
		repo, workspace, user := t.TempDir(), t.TempDir(), t.TempDir()
		write(t, repo, "CLAUDE.md", "repo")
		write(t, repo, "AGENTS.md", "agents")
		write(t, workspace, "CLAUDE.md", "workspace")
		userFile := filepath.Join(user, "instructions.md")
		write(t, user, "instructions.md", "user")
		text, err := LoadInstructions(repo, workspace, userFile)
		require.NoError(t, err)
		assert.Equal(t, "repo\n\nagents\n\nworkspace\n\nuser", text)
	})
	t.Run("reads a shared directory once", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "CLAUDE.md", "one")
		text, err := LoadInstructions(dir, dir, "")
		require.NoError(t, err)
		assert.Equal(t, "one", text)
	})
	t.Run("skips missing files and ignores a user file that wasn't given", func(t *testing.T) {
		text, err := LoadInstructions(t.TempDir(), t.TempDir(), "")
		require.NoError(t, err)
		assert.Empty(t, text)
	})
	t.Run("cuts the text at 8 KiB with a marker", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "CLAUDE.md", strings.Repeat("a", 10000))
		text, err := LoadInstructions(dir, t.TempDir(), "")
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(text, "\n"+instructionsCut))
		assert.Len(t, strings.TrimSuffix(text, "\n"+instructionsCut), maxInstructionBytes)
	})
}

func TestStaticToolset(t *testing.T) {
	ts := StaticToolset(fakeTools())
	t.Run("offers its tools unchanged", func(t *testing.T) {
		names := make([]string, 0)
		for _, def := range ts.Definitions() {
			names = append(names, def.Name)
		}
		assert.Equal(t, []string{"write_file", "run", "record_result"}, names)
	})
	t.Run("knows its tools only", func(t *testing.T) {
		assert.True(t, ts.Known("run"))
		assert.False(t, ts.Known("nope"))
	})
	t.Run("calls a tool by name and refuses an unknown one", func(t *testing.T) {
		res, err := ts.Call(t.Context(), "run", `{"command":"true"}`)
		require.NoError(t, err)
		assert.Equal(t, "exit 0", res.Text)
		_, err = ts.Call(context.Background(), "nope", "{}")
		assert.ErrorContains(t, err, `unknown tool "nope"`)
	})
}

func TestRetryWait(t *testing.T) {
	assert.Equal(t, 2*time.Second, retryWait(1, 0))
	assert.Equal(t, 8*time.Second, retryWait(2, 0))
	assert.Equal(t, 30*time.Second, retryWait(3, 0))
	assert.Equal(t, 45*time.Second, retryWait(1, 45*time.Second))
	assert.Equal(t, 60*time.Second, retryWait(1, 90*time.Second))
	assert.Equal(t, 30*time.Second, retryWait(3, 10*time.Second))
}

func TestSystemClock_Sleep(t *testing.T) {
	t.Run("returns the context's error when cancelled first", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := SystemClock{}.Sleep(ctx, time.Hour)
		assert.ErrorIs(t, err, context.Canceled)
	})
	t.Run("returns nil after the duration", func(t *testing.T) {
		assert.NoError(t, SystemClock{}.Sleep(t.Context(), time.Millisecond))
	})
}
