package shell

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/exectest"
	"github.com/bobcob7/workspaces/internal/lockfile"
)

// fakeShell writes an executable sh script with body and returns its path.
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-shell")
	exectest.WriteScript(t, path, "#!/bin/sh\n"+body+"\n")
	return path
}

// testRunner returns a Runner for environ whose signal registration is
// faked: registered receives the channel Run listens on, and stopped the
// channel it stops listening on.
type testRunner struct {
	*Runner
	stdout     *bytes.Buffer
	registered chan chan<- os.Signal
	stopped    chan chan<- os.Signal
}

func newTestRunner(environ ...string) *testRunner {
	stdout := &bytes.Buffer{}
	tr := &testRunner{
		Runner:     New(slog.New(slog.NewJSONHandler(io.Discard, nil)), environ, nil, stdout, io.Discard),
		stdout:     stdout,
		registered: make(chan chan<- os.Signal, 1),
		stopped:    make(chan chan<- os.Signal, 1),
	}
	tr.notify = func(c chan<- os.Signal, _ ...os.Signal) { tr.registered <- c }
	tr.stop = func(c chan<- os.Signal) { tr.stopped <- c }
	return tr
}

// tempDir is a temp directory with symlinks resolved, as pwd -P prints it.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func TestNew(t *testing.T) {
	stdin, stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	r := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), []string{"A=b"}, stdin, stdout, stderr)
	assert.Equal(t, []string{"A=b"}, r.environ)
	assert.Same(t, stdin, r.stdin)
	assert.Same(t, stdout, r.stdout)
	assert.Same(t, stderr, r.stderr)
	assert.NotNil(t, r.notify)
	assert.NotNil(t, r.stop)
}

func TestRunner_Run(t *testing.T) {
	t.Parallel()
	t.Run("runs $SHELL -i in the directory with the variables", func(t *testing.T) {
		t.Parallel()
		sh := fakeShell(t, `echo "args=$*"; echo "pwd=$(pwd -P)"; echo "PWD=$PWD"; echo "WS_ID=$WS_ID"; echo "KEEP=$KEEP"; exit 7`)
		dir := tempDir(t)
		r := newTestRunner("SHELL="+sh, "WS_ID=outer", "KEEP=kept", "PWD=/elsewhere")
		status, err := r.Run(t.Context(), Session{Dir: dir, Env: []string{"WS_ID=inner"}})
		require.NoError(t, err)
		assert.Equal(t, 7, status)
		assert.Equal(t, "args=-i\npwd="+dir+"\nPWD="+dir+"\nWS_ID=inner\nKEEP=kept\n", r.stdout.String())
	})
	t.Run("passes the lock as fd 3, held by what the shell starts", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "lock")
		lock, ok, err := lockfile.TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		sh := fakeShell(t, `sleep 2 </dev/null >/dev/null 2>&1 &`)
		status, err := newTestRunner("SHELL="+sh).Run(t.Context(), Session{Dir: t.TempDir(), Lock: lock.File()})
		require.NoError(t, err)
		require.Equal(t, 0, status)
		lock.Unlock()
		_, ok, err = lockfile.TryExclusive(path)
		require.NoError(t, err)
		assert.False(t, ok, "held by the shell's background command")
		assert.Eventually(t, func() bool {
			unlock, ok, err := lockfile.TryExclusive(path)
			if err != nil || !ok {
				return false
			}
			unlock()
			return true
		}, 10*time.Second, 20*time.Millisecond, "free once it exits")
	})
	t.Run("a shell killed by a signal", func(t *testing.T) {
		t.Parallel()
		status, err := newTestRunner("SHELL="+fakeShell(t, `kill -KILL $$`)).Run(t.Context(), Session{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, 128+int(syscall.SIGKILL), status)
	})
	t.Run("ignores SIGINT and sends the shell SIGHUP for SIGTERM", func(t *testing.T) {
		t.Parallel()
		r := newTestRunner("SHELL=" + fakeShell(t, `while :; do sleep 0.05; done`))
		go func() {
			c := <-r.registered
			c <- syscall.SIGINT
			c <- syscall.SIGTERM
		}()
		status, err := r.Run(t.Context(), Session{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, 128+int(syscall.SIGHUP), status)
	})
	t.Run("runs AfterExit once the shell has exited, before it stops listening for signals", func(t *testing.T) {
		t.Parallel()
		r := newTestRunner("SHELL=" + fakeShell(t, `exit 2`))
		var stoppedEarly bool
		ran := 0
		status, err := r.Run(t.Context(), Session{Dir: t.TempDir(), AfterExit: func() {
			ran++
			stoppedEarly = len(r.stopped) > 0
		}})
		require.NoError(t, err)
		assert.Equal(t, 2, status)
		assert.Equal(t, 1, ran)
		assert.False(t, stoppedEarly)
	})
	t.Run("the shell starts with SIGINT's default action, not ignored", func(t *testing.T) {
		t.Parallel()
		sh := fakeShell(t, `kill -INT $$; exit 0`)
		r := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), []string{"SHELL=" + sh}, nil, io.Discard, io.Discard)
		status, err := r.Run(t.Context(), Session{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, 128+int(syscall.SIGINT), status)
	})
	t.Run("stops listening for signals once the shell exits", func(t *testing.T) {
		t.Parallel()
		r := newTestRunner("SHELL=" + fakeShell(t, `exit 0`))
		_, err := r.Run(t.Context(), Session{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, <-r.registered, <-r.stopped)
	})
	t.Run("outlives a cancelled context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		status, err := newTestRunner("SHELL="+fakeShell(t, `sleep 0.2; exit 3`)).Run(ctx, Session{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, 3, status)
	})
	t.Run("a shell that can't start", func(t *testing.T) {
		t.Parallel()
		r := newTestRunner("SHELL=" + filepath.Join(t.TempDir(), "missing"))
		_, err := r.Run(t.Context(), Session{Dir: t.TempDir()})
		require.ErrorContains(t, err, "starting shell")
		assert.Equal(t, <-r.registered, <-r.stopped)
	})
	t.Run("runs AfterExit when the shell can't start", func(t *testing.T) {
		t.Parallel()
		r := newTestRunner("SHELL=" + filepath.Join(t.TempDir(), "missing"))
		ran := false
		_, err := r.Run(t.Context(), Session{Dir: t.TempDir(), AfterExit: func() { ran = true }})
		require.Error(t, err)
		assert.True(t, ran)
	})
}

func TestRunner_program(t *testing.T) {
	t.Run("SHELL", func(t *testing.T) {
		assert.Equal(t, "/bin/zsh", newTestRunner("SHELL=/bin/zsh").program())
	})
	t.Run("the last SHELL wins", func(t *testing.T) {
		assert.Equal(t, "/bin/bash", newTestRunner("SHELL=/bin/zsh", "SHELL=/bin/bash").program())
	})
	t.Run("unset", func(t *testing.T) {
		assert.Equal(t, "/bin/sh", newTestRunner("HOME=/h").program())
	})
	t.Run("empty", func(t *testing.T) {
		assert.Equal(t, "/bin/sh", newTestRunner("SHELL=").program())
	})
}

func TestRunner_forward(t *testing.T) {
	t.Parallel()
	t.Run("sends SIGHUP for SIGTERM and nothing for SIGINT", func(t *testing.T) {
		t.Parallel()
		proc := exec.CommandContext(t.Context(), "/bin/sleep", "5")
		require.NoError(t, proc.Start())
		signals := make(chan os.Signal, 2)
		done := make(chan struct{})
		defer close(done)
		go newTestRunner().forward(proc.Process, signals, done)
		signals <- syscall.SIGINT
		signals <- syscall.SIGTERM
		_ = proc.Wait()
		ws, ok := proc.ProcessState.Sys().(syscall.WaitStatus)
		require.True(t, ok)
		assert.Equal(t, syscall.SIGHUP, ws.Signal())
	})
	t.Run("returns when done", func(t *testing.T) {
		t.Parallel()
		done := make(chan struct{})
		returned := make(chan struct{})
		go func() {
			newTestRunner().forward(nil, make(chan os.Signal), done)
			close(returned)
		}()
		close(done)
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Fatal("forward didn't return")
		}
	})
	t.Run("a shell that already exited", func(t *testing.T) {
		t.Parallel()
		proc := exec.CommandContext(t.Context(), "/usr/bin/true")
		require.NoError(t, proc.Run())
		signals := make(chan os.Signal, 1)
		done := make(chan struct{})
		returned := make(chan struct{})
		go func() {
			newTestRunner().forward(proc.Process, signals, done)
			close(returned)
		}()
		signals <- syscall.SIGHUP
		close(done)
		<-returned
	})
}

func TestRunner_pass(t *testing.T) {
	t.Parallel()
	t.Run("sends SIGHUP for SIGHUP", func(t *testing.T) {
		t.Parallel()
		proc := exec.CommandContext(t.Context(), "/bin/sleep", "5")
		require.NoError(t, proc.Start())
		newTestRunner().pass(proc.Process, syscall.SIGHUP)
		_ = proc.Wait()
		ws, ok := proc.ProcessState.Sys().(syscall.WaitStatus)
		require.True(t, ok)
		assert.Equal(t, syscall.SIGHUP, ws.Signal())
	})
	t.Run("sends nothing for SIGQUIT", func(t *testing.T) {
		t.Parallel()
		proc := exec.CommandContext(t.Context(), "/bin/sleep", "0.2")
		require.NoError(t, proc.Start())
		newTestRunner().pass(proc.Process, syscall.SIGQUIT)
		require.NoError(t, proc.Wait())
	})
	t.Run("a shell that already exited", func(t *testing.T) {
		t.Parallel()
		proc := exec.CommandContext(t.Context(), "/usr/bin/true")
		require.NoError(t, proc.Run())
		newTestRunner().pass(proc.Process, syscall.SIGTERM)
	})
}

func TestPassedOn(t *testing.T) {
	t.Run("SIGHUP stays SIGHUP", func(t *testing.T) {
		out, ok := passedOn(syscall.SIGHUP)
		require.True(t, ok)
		assert.Equal(t, syscall.SIGHUP, out)
	})
	t.Run("SIGTERM becomes SIGHUP", func(t *testing.T) {
		out, ok := passedOn(syscall.SIGTERM)
		require.True(t, ok)
		assert.Equal(t, syscall.SIGHUP, out)
	})
	t.Run("SIGINT isn't passed on", func(t *testing.T) {
		_, ok := passedOn(syscall.SIGINT)
		assert.False(t, ok)
	})
	t.Run("SIGQUIT isn't passed on", func(t *testing.T) {
		_, ok := passedOn(syscall.SIGQUIT)
		assert.False(t, ok)
	})
}

func TestExitStatus(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, script string) (*os.ProcessState, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script)
		err := cmd.Run()
		return cmd.ProcessState, err
	}
	t.Run("success", func(t *testing.T) {
		t.Parallel()
		status, err := exitStatus(run(t, "exit 0"))
		require.NoError(t, err)
		assert.Equal(t, 0, status)
	})
	t.Run("exit code", func(t *testing.T) {
		t.Parallel()
		status, err := exitStatus(run(t, "exit 42"))
		require.NoError(t, err)
		assert.Equal(t, 42, status)
	})
	t.Run("killed by a signal", func(t *testing.T) {
		t.Parallel()
		status, err := exitStatus(run(t, "kill -HUP $$"))
		require.NoError(t, err)
		assert.Equal(t, 128+int(syscall.SIGHUP), status)
	})
	t.Run("no process state", func(t *testing.T) {
		t.Parallel()
		_, err := exitStatus(nil, os.ErrClosed)
		assert.ErrorIs(t, err, os.ErrClosed)
	})
}

func TestLookup(t *testing.T) {
	t.Run("last value", func(t *testing.T) {
		assert.Equal(t, "2", lookup([]string{"A=1", "B=x", "A=2"}, "A"))
	})
	t.Run("missing", func(t *testing.T) {
		assert.Empty(t, lookup([]string{"AB=1", "malformed"}, "A"))
	})
	t.Run("value with =", func(t *testing.T) {
		assert.Equal(t, "x=y", lookup([]string{"A=x=y"}, "A"))
	})
}

func TestWithVars(t *testing.T) {
	t.Run("replaces and appends", func(t *testing.T) {
		got := withVars([]string{"A=1", "B=2", "A=3"}, []string{"A=new", "C=4"})
		assert.Equal(t, []string{"B=2", "A=new", "C=4"}, got)
	})
	t.Run("leaves environ alone", func(t *testing.T) {
		environ := []string{"A=1"}
		withVars(environ, []string{"A=2"})
		assert.Equal(t, []string{"A=1"}, environ)
	})
	t.Run("keeps similar names", func(t *testing.T) {
		got := withVars([]string{"AB=1"}, []string{"A=2"})
		assert.Equal(t, "AB=1 A=2", strings.Join(got, " "))
	})
}
