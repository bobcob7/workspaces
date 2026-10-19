package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noJob is what claude stop, rm, and respawn print for an unknown id in
// Claude Code 2.1.303, with exit status 1.
const noJob = `echo "No job matching '$2'" >&2; exit 1`

func TestClient_Stop(t *testing.T) {
	t.Parallel()
	environ, args := argsFile(t)
	c := New(discardLogger(), fakeClaude(t, recordArgs+`; echo "stopped $2"`), environ)
	require.NoError(t, c.Stop(t.Context(), "f381de70"))
	assert.Equal(t, []string{"stop", "f381de70"}, args())
}

func TestClient_Remove(t *testing.T) {
	t.Parallel()
	environ, args := argsFile(t)
	c := New(discardLogger(), fakeClaude(t, recordArgs+`; echo "removed $2"`), environ)
	require.NoError(t, c.Remove(t.Context(), "f381de70"))
	assert.Equal(t, []string{"rm", "f381de70"}, args())
}

func TestClient_Respawn(t *testing.T) {
	t.Parallel()
	environ, args := argsFile(t)
	c := New(discardLogger(), fakeClaude(t, recordArgs+`; echo "respawned $2"`), environ)
	require.NoError(t, c.Respawn(t.Context(), "f381de70"))
	assert.Equal(t, []string{"respawn", "f381de70"}, args())
}

func TestClient_sessionCommand(t *testing.T) {
	t.Parallel()
	t.Run("succeeds on exit 0 whatever it prints", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo something new"), nil)
		assert.NoError(t, c.sessionCommand(t.Context(), "stop", "f381de70"))
	})
	t.Run("unknown session", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, noJob), nil)
		assert.ErrorIs(t, c.sessionCommand(t.Context(), "rm", "f381de70"), ErrNoSession)
	})
	t.Run("other failure", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo 'kept the worktree' >&2; exit 1"), nil)
		err := c.sessionCommand(t.Context(), "rm", "f381de70")
		require.NotErrorIs(t, err, ErrNoSession)
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, "kept the worktree", cmdErr.Stderr)
	})
	t.Run("rejects an id that is an option", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		c := New(discardLogger(), fakeClaude(t, ""), environ)
		require.ErrorIs(t, c.sessionCommand(t.Context(), "respawn", "--all"), errBadID)
		assert.Zero(t, count(), "claude didn't run")
	})
}

func TestClient_JobExists(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "jobs", "f381de70"), 0o700))
	c := New(discardLogger(), "claude", []string{"HOME=" + home})
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		ok, err := c.JobExists("f381de70")
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("gone", func(t *testing.T) {
		t.Parallel()
		ok, err := c.JobExists("5d8d652c")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("rejects a path", func(t *testing.T) {
		t.Parallel()
		_, err := c.JobExists("../jobs")
		assert.ErrorIs(t, err, errBadID)
	})
	t.Run("under CLAUDE_CONFIG_DIR", func(t *testing.T) {
		t.Parallel()
		cfg := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(cfg, "jobs", "a1b2c3d4"), 0o700))
		c := New(discardLogger(), "claude", []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + cfg})
		ok, err := c.JobExists("a1b2c3d4")
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.JobExists("f381de70")
		require.NoError(t, err)
		assert.False(t, ok, "HOME's jobs directory isn't looked at")
	})
	t.Run("no HOME", func(t *testing.T) {
		t.Parallel()
		_, err := New(discardLogger(), "claude", nil).JobExists("f381de70")
		assert.ErrorIs(t, err, errNoHome)
	})
	t.Run("unreadable jobs directory", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(home, ".claude"), nil, 0o600))
		_, err := New(discardLogger(), "claude", []string{"HOME=" + home}).JobExists("f381de70")
		assert.ErrorContains(t, err, "checking Claude Code session f381de70")
	})
}

func TestValidID(t *testing.T) {
	t.Run("short id", func(t *testing.T) {
		assert.True(t, validID("f381de70"))
	})
	t.Run("empty", func(t *testing.T) {
		assert.False(t, validID(""))
	})
	t.Run("option", func(t *testing.T) {
		assert.False(t, validID("--all"))
	})
	t.Run("path", func(t *testing.T) {
		assert.False(t, validID("a/b"))
	})
	t.Run("backslash", func(t *testing.T) {
		assert.False(t, validID(`a\b`))
	})
	t.Run("dot", func(t *testing.T) {
		assert.False(t, validID("."))
	})
	t.Run("dot dot", func(t *testing.T) {
		assert.False(t, validID(".."))
	})
}
