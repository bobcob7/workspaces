package git

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
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

// fakeGit writes a shell script standing in for git and returns its path.
// body runs first; if it doesn't exit, --version reports $FAKE_VERSION,
// default 2.50.1.
func fakeGit(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\n" + body + "\nif [ \"$1\" = --version ]; then echo \"git version ${FAKE_VERSION:-2.50.1}\"; fi\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

// countCalls returns an environment that makes fakeGit scripts append a line
// to a file per --version call, and a function that reads the count.
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

// versionPassthrough skips a body for --version, leaving it to fakeGit.
const versionPassthrough = `[ "$1" = --version ] || `

const countingVersion = `if [ "$1" = --version ]; then echo x >> "$CALLS"; echo "git version 2.50.1"; exit 0; fi`

func TestNewRunner(t *testing.T) {
	log := discardLogger()
	r := NewRunner(log, "git", []string{"A=1"})
	assert.Same(t, log, r.log)
	assert.Equal(t, "git", r.gitPath)
	assert.Equal(t, []string{"A=1"}, r.environ)
}

func TestRunner_Run(t *testing.T) {
	t.Parallel()
	t.Run("returns stdout", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, versionPassthrough+`echo "out:$*"`), nil)
		out, err := r.Run(t.Context(), "/repo", "status")
		require.NoError(t, err)
		assert.Equal(t, "out:-C /repo status\n", string(out))
	})
	t.Run("requires a directory", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, ""), nil)
		_, err := r.Run(t.Context(), "", "status")
		assert.ErrorIs(t, err, errNoDir)
	})
	t.Run("checks the version before the command", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, versionPassthrough+"echo ran"), []string{"FAKE_VERSION=2.20.0"})
		out, err := r.Run(t.Context(), "/repo", "status")
		require.ErrorIs(t, err, ErrTooOld)
		assert.Nil(t, out)
	})
	t.Run("runs the real git", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		r := NewRunner(discardLogger(), "git", os.Environ())
		_, err := r.Run(t.Context(), dir, "init", "-q")
		require.NoError(t, err)
		out, err := r.Run(t.Context(), dir, "rev-parse", "--is-bare-repository")
		require.NoError(t, err)
		assert.Equal(t, "false\n", string(out))
	})
	t.Run("reports the real git's failures", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		r := NewRunner(discardLogger(), "git", os.Environ())
		_, err := r.Run(t.Context(), dir, "init", "-q")
		require.NoError(t, err)
		_, err = r.Run(t.Context(), dir, "rev-parse", "--verify", "-q", "refs/heads/missing")
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, 1, cmdErr.ExitCode)
	})
}

func TestRunner_ensureVersion(t *testing.T) {
	t.Parallel()
	t.Run("checks once", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		r := NewRunner(discardLogger(), fakeGit(t, countingVersion), environ)
		require.NoError(t, r.ensureVersion(t.Context()))
		require.NoError(t, r.ensureVersion(t.Context()))
		assert.Equal(t, 1, count())
	})
	t.Run("remembers a failure", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, ""), []string{"FAKE_VERSION=2.0.0"})
		require.ErrorIs(t, r.ensureVersion(t.Context()), ErrTooOld)
		assert.ErrorIs(t, r.ensureVersion(t.Context()), ErrTooOld)
	})
	t.Run("retries after a cancelled check", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		r := NewRunner(discardLogger(), fakeGit(t, countingVersion), environ)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.Error(t, r.ensureVersion(ctx))
		require.NoError(t, r.ensureVersion(t.Context()))
		assert.Equal(t, 1, count())
	})
}

func TestRunner_checkVersion(t *testing.T) {
	t.Parallel()
	t.Run("accepts the minimum", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, ""), []string{"FAKE_VERSION=2.23.0"})
		assert.NoError(t, r.checkVersion(t.Context()))
	})
	t.Run("rejects older", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, ""), []string{"FAKE_VERSION=2.22.5"})
		err := r.checkVersion(t.Context())
		require.ErrorIs(t, err, ErrTooOld)
		assert.ErrorContains(t, err, "found 2.22.5, need 2.23.0 or newer")
	})
	t.Run("rejects unrecognized output", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, ""), []string{"FAKE_VERSION=banana"})
		assert.ErrorContains(t, r.checkVersion(t.Context()), "unrecognized")
	})
	t.Run("missing git", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), filepath.Join(t.TempDir(), "no-git"), nil)
		assert.ErrorIs(t, r.checkVersion(t.Context()), ErrNotInstalled)
	})
}

func TestRunner_exec(t *testing.T) {
	t.Parallel()
	t.Run("adds -C dir", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, `echo "$*"`), nil)
		out, err := r.exec(t.Context(), "/a b", []string{"status"})
		require.NoError(t, err)
		assert.Equal(t, "-C /a b status\n", string(out))
	})
	t.Run("no -C without a dir", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, `echo "$*"`), nil)
		out, err := r.exec(t.Context(), "", []string{"ls-remote"})
		require.NoError(t, err)
		assert.Equal(t, "ls-remote\n", string(out))
	})
	t.Run("keeps the environment and disables prompts", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, `echo "$KEEP $GIT_TERMINAL_PROMPT"`), []string{"KEEP=yes", "GIT_TERMINAL_PROMPT=1"})
		out, err := r.exec(t.Context(), "/repo", nil)
		require.NoError(t, err)
		assert.Equal(t, "yes 0\n", string(out))
	})
	t.Run("failure is a CommandError", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, "echo 'fatal: nope  ' >&2; exit 128"), nil)
		_, err := r.exec(t.Context(), "/repo", []string{"fetch", "origin"})
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, []string{"fetch", "origin"}, cmdErr.Args)
		assert.Equal(t, "/repo", cmdErr.Dir)
		assert.Equal(t, 128, cmdErr.ExitCode)
		assert.Equal(t, "fatal: nope", cmdErr.Stderr)
	})
	t.Run("missing git at a path", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), filepath.Join(t.TempDir(), "no-git"), nil)
		_, err := r.exec(t.Context(), "/repo", []string{"status"})
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("missing git on PATH", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), "ws-test-no-such-git", []string{"PATH=" + t.TempDir()})
		_, err := r.exec(t.Context(), "/repo", []string{"status"})
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("cancelled context", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(discardLogger(), fakeGit(t, "sleep 5"), nil)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := r.exec(ctx, "/repo", []string{"fetch"})
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("logs the command at debug level", func(t *testing.T) {
		t.Parallel()
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		r := NewRunner(log, fakeGit(t, ""), nil)
		_, err := r.exec(t.Context(), "/repo", []string{"status"})
		require.NoError(t, err)
		assert.Contains(t, logs.String(), `"msg":"git"`)
		assert.Contains(t, logs.String(), `"args":["status"]`)
		assert.Contains(t, logs.String(), `"exit":0`)
	})
}
