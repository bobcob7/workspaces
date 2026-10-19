package claude

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// replaced records what Attach asked to replace ws with.
type replaced struct {
	argv0 string
	argv  []string
	envv  []string
}

// recordReplace makes c's replace record its call and return err.
func recordReplace(c *Client, err error) *replaced {
	r := &replaced{}
	c.replace = func(argv0 string, argv, envv []string) error {
		r.argv0, r.argv, r.envv = argv0, argv, envv
		return err
	}
	return r
}

func TestClient_Attach(t *testing.T) {
	t.Parallel()
	t.Run("replaces ws with claude attach and its environment", func(t *testing.T) {
		t.Parallel()
		path := fakeClaude(t, "exit 0")
		c := New(discardLogger(), path, []string{"A=1"})
		r := recordReplace(c, nil)
		require.NoError(t, c.Attach(t.Context(), "f381de70"))
		assert.Equal(t, path, r.argv0)
		assert.Equal(t, []string{path, "attach", "f381de70"}, r.argv)
		assert.Equal(t, []string{"A=1"}, r.envv)
	})
	t.Run("checks the version first", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exit 0"), []string{"FAKE_VERSION=2.1.200"})
		r := recordReplace(c, nil)
		require.ErrorIs(t, c.Attach(t.Context(), "f381de70"), ErrTooOld)
		assert.Empty(t, r.argv)
	})
	t.Run("refuses an id that isn't one", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exit 0"), nil)
		r := recordReplace(c, nil)
		require.ErrorIs(t, c.Attach(t.Context(), "--all"), errBadID)
		assert.Empty(t, r.argv)
	})
	t.Run("claude missing", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), filepath.Join(t.TempDir(), "claude"), nil)
		require.ErrorIs(t, c.Attach(t.Context(), "f381de70"), ErrNotInstalled)
	})
	t.Run("a failed exec is returned", func(t *testing.T) {
		t.Parallel()
		errExec := errors.New("exec format error")
		c := New(discardLogger(), fakeClaude(t, "exit 0"), nil)
		recordReplace(c, errExec)
		require.ErrorIs(t, c.Attach(t.Context(), "f381de70"), errExec)
	})
	t.Run("the process becomes claude attach and exits with its status", func(t *testing.T) {
		t.Parallel()
		out := filepath.Join(t.TempDir(), "args")
		path := fakeClaude(t, `printf '%s\n' "$@" > "$ARGS"; exit 7`)
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAttachHelper$")
		cmd.Env = append(os.Environ(), "WS_ATTACH_HELPER="+path, "ARGS="+out)
		err := cmd.Run()
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, 7, exitErr.ExitCode())
		data, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, "attach\nf381de70\n", string(data))
	})
}

// TestAttachHelper is run by TestClient_Attach in a child process, which
// Attach replaces with the fake claude named by WS_ATTACH_HELPER.
func TestAttachHelper(t *testing.T) {
	path := os.Getenv("WS_ATTACH_HELPER")
	if path == "" {
		t.Skip("run by TestClient_Attach")
	}
	c := New(discardLogger(), path, os.Environ())
	err := c.Attach(t.Context(), "f381de70")
	t.Fatalf("Attach returned: %v", err)
}
