package claude

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// fakeClaude writes a shell script standing in for claude and returns its
// path. --version reports $FAKE_VERSION, default 2.1.303, and appends a
// line to $CALLS when it's set; anything else runs body.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then\n" +
		"  if [ -n \"$CALLS\" ]; then echo x >> \"$CALLS\"; fi\n" +
		"  echo \"${FAKE_VERSION:-2.1.303} (Claude Code)\"\n" +
		"  exit 0\n" +
		"fi\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

// recordArgs is a fakeClaude body that writes its arguments, one per line,
// to $ARGS.
const recordArgs = `printf '%s\n' "$@" > "$ARGS"`

// argsFile returns an environment that makes recordArgs write to a file,
// and a function that reads the recorded arguments.
func argsFile(t *testing.T) (environ []string, read func() []string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "args")
	return []string{"ARGS=" + file}, func() []string {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

// countCalls returns an environment that makes fakeClaude count --version
// calls, and a function that reads the count.
func countCalls(t *testing.T) (environ []string, count func() int) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "calls")
	return []string{"CALLS=" + file}, func() int {
		data, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			return 0
		}
		require.NoError(t, err)
		return strings.Count(string(data), "\n")
	}
}

// holder is a fakeClaude command whose child keeps claude's output open:
// it creates holding, waits until the test ends, or a minute has passed,
// and then creates exited.
type holder struct {
	body    string
	environ []string
	holding string
	exited  string
}

func newHolder(t *testing.T) *holder {
	t.Helper()
	dir := t.TempDir()
	h := &holder{
		body:    `(touch "$HOLDING"; i=0; while [ ! -e "$RELEASE" ] && [ $i -lt 1200 ]; do sleep 0.05; i=$((i+1)); done; touch "$EXITED")`,
		holding: filepath.Join(dir, "holding"),
		exited:  filepath.Join(dir, "exited"),
	}
	release := filepath.Join(dir, "release")
	h.environ = []string{"PATH=/bin:/usr/bin", "HOLDING=" + h.holding, "RELEASE=" + release, "EXITED=" + h.exited}
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o600)
		waitForFile(h.exited)
	})
	return h
}

// waitForFile waits until path exists, for at most a minute.
func waitForFile(path string) {
	for range 1200 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestNew(t *testing.T) {
	log := discardLogger()
	t.Run("keeps its arguments", func(t *testing.T) {
		c := New(log, "claude", []string{"A=1"})
		assert.Same(t, log, c.log)
		assert.Equal(t, "claude", c.path)
		assert.Equal(t, []string{"A=1"}, c.environ)
	})
	t.Run("finds jobs under HOME", func(t *testing.T) {
		c := New(log, "claude", []string{"HOME=/old", "HOME=/home/me"})
		assert.Equal(t, "/home/me/.claude/jobs", c.jobsDir)
	})
	t.Run("no jobs directory without HOME", func(t *testing.T) {
		c := New(log, "claude", nil)
		assert.Empty(t, c.jobsDir)
	})
}

func TestJobsDir(t *testing.T) {
	t.Run("CLAUDE_CONFIG_DIR", func(t *testing.T) {
		assert.Equal(t, "/cfg/jobs", jobsDir([]string{"HOME=/home/me", "CLAUDE_CONFIG_DIR=/cfg"}))
	})
	t.Run("empty CLAUDE_CONFIG_DIR falls back to HOME", func(t *testing.T) {
		assert.Equal(t, "/home/me/.claude/jobs", jobsDir([]string{"HOME=/home/me", "CLAUDE_CONFIG_DIR="}))
	})
	t.Run("HOME", func(t *testing.T) {
		assert.Equal(t, "/home/me/.claude/jobs", jobsDir([]string{"HOME=/home/me"}))
	})
	t.Run("CLAUDE_CONFIG_DIR without HOME", func(t *testing.T) {
		assert.Equal(t, "/cfg/jobs", jobsDir([]string{"CLAUDE_CONFIG_DIR=/cfg"}))
	})
	t.Run("neither", func(t *testing.T) {
		assert.Empty(t, jobsDir([]string{"PATH=/bin"}))
	})
}

func TestClient_run(t *testing.T) {
	t.Parallel()
	t.Run("returns stdout", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, `echo "out:$*"`), nil)
		out, err := c.run(t.Context(), "agents", "--json")
		require.NoError(t, err)
		assert.Equal(t, "out:agents --json\n", string(out))
	})
	t.Run("checks the version before the command", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo ran"), []string{"FAKE_VERSION=2.1.200"})
		out, err := c.run(t.Context(), "agents")
		require.ErrorIs(t, err, ErrTooOld)
		assert.Nil(t, out)
	})
}

func TestClient_ensureVersion(t *testing.T) {
	t.Parallel()
	t.Run("checks once", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		c := New(discardLogger(), fakeClaude(t, ""), environ)
		require.NoError(t, c.ensureVersion(t.Context()))
		require.NoError(t, c.ensureVersion(t.Context()))
		assert.Equal(t, 1, count())
	})
	t.Run("remembers a failure", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, ""), []string{"FAKE_VERSION=1.0.0"})
		require.ErrorIs(t, c.ensureVersion(t.Context()), ErrTooOld)
		assert.ErrorIs(t, c.ensureVersion(t.Context()), ErrTooOld)
	})
	t.Run("retries after a cancelled check", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		c := New(discardLogger(), fakeClaude(t, ""), environ)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.Error(t, c.ensureVersion(ctx))
		require.NoError(t, c.ensureVersion(t.Context()))
		assert.Equal(t, 1, count())
	})
}

func TestClient_checkVersion(t *testing.T) {
	t.Parallel()
	t.Run("accepts the minimum", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, ""), []string{"FAKE_VERSION=2.1.302"})
		assert.NoError(t, c.checkVersion(t.Context()))
	})
	t.Run("rejects older", func(t *testing.T) {
		t.Parallel()
		path := fakeClaude(t, "")
		c := New(discardLogger(), path, []string{"FAKE_VERSION=2.1.84"})
		err := c.checkVersion(t.Context())
		require.ErrorIs(t, err, ErrTooOld)
		assert.ErrorContains(t, err, path+" is 2.1.84, need 2.1.302 or newer")
	})
	t.Run("rejects unrecognized output", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, ""), []string{"FAKE_VERSION=banana"})
		assert.ErrorIs(t, c.checkVersion(t.Context()), ErrUnexpectedOutput)
	})
	t.Run("missing claude", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), filepath.Join(t.TempDir(), "no-claude"), nil)
		assert.ErrorIs(t, c.checkVersion(t.Context()), ErrNotInstalled)
	})
}

func TestClient_exec(t *testing.T) {
	t.Parallel()
	t.Run("runs in dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		c := New(discardLogger(), fakeClaude(t, `echo "$PWD"`), nil)
		out, err := c.exec(t.Context(), dir, nil)
		require.NoError(t, err)
		assert.Equal(t, resolved(dir), resolved(strings.TrimSpace(string(out))))
	})
	t.Run("passes the environment as it is", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, `echo "$KEEP"`), []string{"KEEP=yes"})
		out, err := c.exec(t.Context(), "", nil)
		require.NoError(t, err)
		assert.Equal(t, "yes\n", string(out))
	})
	t.Run("gives no stdin", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, `if read -r line; then echo "got:$line"; else echo none; fi`), nil)
		out, err := c.exec(t.Context(), "", nil)
		require.NoError(t, err)
		assert.Equal(t, "none\n", string(out))
	})
	t.Run("failure is a CommandError", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo partial; echo 'boom  ' >&2; exit 3"), nil)
		_, err := c.exec(t.Context(), "/tmp", []string{"agents", "--json"})
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, []string{"agents", "--json"}, cmdErr.Args)
		assert.Equal(t, "/tmp", cmdErr.Dir)
		assert.Equal(t, 3, cmdErr.ExitCode)
		assert.Equal(t, "partial", cmdErr.Stdout)
		assert.Equal(t, "boom", cmdErr.Stderr)
	})
	t.Run("classifies an untrusted directory", func(t *testing.T) {
		t.Parallel()
		body := "echo 'Workspace not trusted. Run `claude` in /w once and accept the trust prompt, then retry.' >&2; exit 1"
		c := New(discardLogger(), fakeClaude(t, body), nil)
		_, err := c.exec(t.Context(), "", []string{"--bg"})
		assert.ErrorIs(t, err, ErrUntrusted)
	})
	t.Run("missing claude at a path", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), filepath.Join(t.TempDir(), "no-claude"), nil)
		_, err := c.exec(t.Context(), "", []string{"agents"})
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("missing claude on PATH", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), "ws-test-no-such-claude", nil)
		_, err := c.exec(t.Context(), "", []string{"agents"})
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("missing directory isn't missing claude", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, ""), nil)
		_, err := c.exec(t.Context(), filepath.Join(t.TempDir(), "gone"), []string{"--bg"})
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("output held open by a child after success", func(t *testing.T) {
		t.Parallel()
		h := newHolder(t)
		c := New(discardLogger(), fakeClaude(t, h.body+" & echo started"), h.environ)
		out, err := c.exec(t.Context(), "", []string{"--bg"})
		require.NoError(t, err)
		assert.Equal(t, "started\n", string(out))
		assert.NoFileExists(t, h.exited, "exec returned while the child still held its output")
	})
	t.Run("output held open by a child after a failure", func(t *testing.T) {
		t.Parallel()
		h := newHolder(t)
		c := New(discardLogger(), fakeClaude(t, h.body+" & exit 2"), h.environ)
		_, err := c.exec(t.Context(), "", []string{"--bg"})
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, 2, cmdErr.ExitCode)
		assert.NoFileExists(t, h.exited)
	})
	t.Run("cancelled context", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exec sleep 5"), []string{"PATH=/bin:/usr/bin"})
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := c.exec(ctx, "", []string{"agents"})
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("cancelled context with a child holding the output", func(t *testing.T) {
		t.Parallel()
		h := newHolder(t)
		c := New(discardLogger(), fakeClaude(t, h.body+" & wait"), h.environ)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			waitForFile(h.holding)
			cancel()
		}()
		_, err := c.exec(ctx, "", []string{"agents"})
		require.ErrorIs(t, err, context.Canceled)
		require.FileExists(t, h.holding, "the child held the output when ctx ended")
		assert.NoFileExists(t, h.exited, "exec returned while the child still held its output")
	})
	t.Run("logs the command at debug level", func(t *testing.T) {
		t.Parallel()
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		c := New(log, fakeClaude(t, ""), nil)
		_, err := c.exec(t.Context(), "", []string{"agents"})
		require.NoError(t, err)
		assert.Contains(t, logs.String(), `"msg":"claude"`)
		assert.Contains(t, logs.String(), `"args":["agents"]`)
		assert.Contains(t, logs.String(), `"exit":0`)
	})
}

func TestNotInstalled(t *testing.T) {
	t.Run("not on PATH", func(t *testing.T) {
		assert.True(t, notInstalled(&exec.Error{Name: "claude", Err: exec.ErrNotFound}))
	})
	t.Run("no file at the path", func(t *testing.T) {
		assert.True(t, notInstalled(&fs.PathError{Op: "fork/exec", Path: "/x/claude", Err: fs.ErrNotExist}))
	})
	t.Run("no working directory", func(t *testing.T) {
		assert.False(t, notInstalled(&fs.PathError{Op: "chdir", Path: "/w/gone", Err: fs.ErrNotExist}))
	})
	t.Run("not executable", func(t *testing.T) {
		assert.False(t, notInstalled(&fs.PathError{Op: "fork/exec", Path: "/x/claude", Err: fs.ErrPermission}))
	})
}

func TestClient_resolvedPath(t *testing.T) {
	t.Run("a path is kept", func(t *testing.T) {
		assert.Equal(t, "/x/claude", New(discardLogger(), "/x/claude", nil).resolvedPath())
	})
	t.Run("a name not on PATH is kept", func(t *testing.T) {
		assert.Equal(t, "ws-test-no-such-claude", New(discardLogger(), "ws-test-no-such-claude", nil).resolvedPath())
	})
}

func TestLookup(t *testing.T) {
	t.Run("last value wins", func(t *testing.T) {
		assert.Equal(t, "b", lookup([]string{"K=a", "K=b"}, "K"))
	})
	t.Run("missing", func(t *testing.T) {
		assert.Empty(t, lookup([]string{"KEY=a", "noequals"}, "K"))
	})
}

func TestClient_Check(t *testing.T) {
	t.Parallel()
	t.Run("accepts a new enough claude, once", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		c := New(discardLogger(), fakeClaude(t, "exit 1"), environ)
		require.NoError(t, c.Check(t.Context()))
		require.NoError(t, c.Check(t.Context()))
		assert.Equal(t, 1, count())
	})
	t.Run("refuses an old claude", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exit 0"), []string{"FAKE_VERSION=2.1.84"})
		assert.ErrorIs(t, c.Check(t.Context()), ErrTooOld)
	})
	t.Run("claude missing", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), filepath.Join(t.TempDir(), "claude"), nil)
		assert.ErrorIs(t, c.Check(t.Context()), ErrNotInstalled)
	})
}
