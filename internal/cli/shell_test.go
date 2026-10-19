package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/shell"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// shellEnv is an App for ws shell whose workspace store creates workspace
// x with a real, held lock, and whose shell exits 0.
type shellEnv struct {
	app      *App
	stderr   *bytes.Buffer
	store    *workspaceStoreMock
	shell    *shellRunnerMock
	ws       workspace.Workspace
	lockPath string
	lock     *lockfile.Lock
}

func newShellEnv(t *testing.T) *shellEnv {
	t.Helper()
	app, _, stderr := newTestApp()
	e := &shellEnv{
		app:      app,
		stderr:   stderr,
		ws:       workspace.Workspace{ID: "x", Path: "/cfg/workspaces/x"},
		lockPath: filepath.Join(t.TempDir(), "lock"),
	}
	e.store = &workspaceStoreMock{
		CreateFunc: func(context.Context, workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			lock, ok, err := lockfile.TryExclusiveLock(e.lockPath)
			require.NoError(t, err)
			require.True(t, ok)
			e.lock = lock
			return e.ws, lock, nil
		},
		RecordSessionFunc: func(string, workspace.SessionKind, string) error { return nil },
		RemoveFunc:        func(string, workspace.RemoveOptions) error { return nil },
		WarningsFunc:      func() []string { return nil },
	}
	e.shell = &shellRunnerMock{RunFunc: exitWith(0, nil)}
	app.cache, app.workspaces, app.shell = stubCache(), e.store, e.shell
	return e
}

// exitWith is a shell runner's Run that returns status and err after
// running the session's AfterExit, as the real one does.
func exitWith(status int, err error) func(context.Context, shell.Session) (int, error) {
	return func(_ context.Context, sess shell.Session) (int, error) {
		if sess.AfterExit != nil {
			sess.AfterExit()
		}
		return status, err
	}
}

// locked reports whether the workspace's lock is held.
func (e *shellEnv) locked(t *testing.T) bool {
	t.Helper()
	unlock, ok, err := lockfile.TryExclusive(e.lockPath)
	require.NoError(t, err)
	if ok {
		unlock()
	}
	return !ok
}

// fakeShellScript writes an executable sh script that records its
// environment and working directory in dir/out, says it started by
// creating dir/started, waits for dir/go, and exits 9.
func fakeShellScript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-shell")
	script := "#!/bin/sh\n" +
		"out=" + dir + "\n" +
		`{ echo "args=$*"; echo "pwd=$(pwd -P)"; echo "WS_ID=$WS_ID"; echo "WS_WORKSPACE=$WS_WORKSPACE";` +
		` [ -e /dev/fd/3 ] && echo "fd3=open"; [ -d "$WS_WORKSPACE/n/.git" ] && echo "repo=yes"; } > "$out/out"` + "\n" +
		`touch "$out/started"` + "\n" +
		`while [ ! -e "$out/go" ]; do sleep 0.02; done` + "\n" +
		"exit 9\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func TestApp_newShellCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newShellCmd()
	t.Run("has the create flags", func(t *testing.T) {
		assertFlag(t, cmd, "name", "")
		assertFlag(t, cmd, "offline", "false")
	})
	t.Run("needs a spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, []string{"a//b"}))
	})
	t.Run("refuses --json", func(t *testing.T) {
		app.global.json = true
		t.Cleanup(func() { app.global.json = false })
		requireUsageError(t, cmd.PreRunE(cmd, nil))
	})
	t.Run("runs the shell", func(t *testing.T) {
		e := newShellEnv(t)
		e.app.newStores = func(config.Paths, config.Config) (repoCache, workspaceStore) { return stubCache(), e.store }
		cmd := e.app.newShellCmd()
		require.NoError(t, cmd.ParseFlags([]string{"--name", "x", "--offline"}))
		require.NoError(t, cmd.RunE(cmd, []string{"buff-bear"}))
		require.Len(t, e.store.CreateCalls(), 1)
		opts := e.store.CreateCalls()[0].Opts
		assert.Equal(t, "x", opts.Name)
		assert.True(t, opts.Offline)
		require.Len(t, opts.Specs, 1)
		assert.Equal(t, "buff-bear", opts.Specs[0].Raw)
		assert.Len(t, e.shell.RunCalls(), 1)
	})
}

func TestApp_runShell(t *testing.T) {
	t.Run("holds the lock from creation until the workspace is removed", func(t *testing.T) {
		e := newShellEnv(t)
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error {
			assert.True(t, e.locked(t), "while recording the session")
			return nil
		}
		e.shell.RunFunc = func(_ context.Context, sess shell.Session) (int, error) {
			assert.True(t, e.locked(t), "while the shell runs")
			assert.Same(t, e.lock.File(), sess.Lock)
			sess.AfterExit()
			return 0, nil
		}
		e.store.RemoveFunc = func(string, workspace.RemoveOptions) error {
			assert.True(t, e.locked(t), "while removing")
			return nil
		}
		require.NoError(t, e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}}))
		assert.False(t, e.locked(t), "released at the end")
	})
	t.Run("records the session, runs the shell, then removes the workspace", func(t *testing.T) {
		e := newShellEnv(t)
		var steps []string
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error {
			steps = append(steps, "record")
			return nil
		}
		e.shell.RunFunc = func(_ context.Context, sess shell.Session) (int, error) {
			steps = append(steps, "shell")
			sess.AfterExit()
			return 0, nil
		}
		e.store.RemoveFunc = func(id string, opts workspace.RemoveOptions) error {
			steps = append(steps, "remove")
			assert.Equal(t, "x", id)
			assert.Equal(t, workspace.RemoveOptions{}, opts)
			return nil
		}
		require.NoError(t, e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}}))
		assert.Equal(t, []string{"record", "shell", "remove"}, steps)
	})
	t.Run("exits with the shell's status", func(t *testing.T) {
		e := newShellEnv(t)
		e.shell.RunFunc = exitWith(3, nil)
		err := e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}})
		se, ok := errors.AsType[*shellExitError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, 3, se.status)
		assert.Len(t, e.store.RemoveCalls(), 1)
	})
	t.Run("a failed removal is reported and keeps the shell's status", func(t *testing.T) {
		e := newShellEnv(t)
		e.shell.RunFunc = exitWith(5, nil)
		e.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return errTest }
		err := e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}})
		se, ok := errors.AsType[*shellExitError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, 5, se.status)
		assert.Contains(t, e.stderr.String(), "ws: couldn't remove workspace x: boom")
	})
	t.Run("a failed creation has its error code and runs nothing", func(t *testing.T) {
		e := newShellEnv(t)
		e.store.CreateFunc = func(context.Context, workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			return workspace.Workspace{}, nil, workspace.ErrExists
		}
		e.store.WarningsFunc = func() []string { return []string{"left something"} }
		err := e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}})
		requireCode(t, codeWorkspaceExists, err)
		assert.Empty(t, e.shell.RunCalls())
		assert.Empty(t, e.store.RemoveCalls())
		assert.Contains(t, e.stderr.String(), "ws: warning: left something")
	})
	t.Run("a shell that can't start removes the workspace", func(t *testing.T) {
		e := newShellEnv(t)
		e.shell.RunFunc = exitWith(0, errTest)
		err := e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}})
		require.ErrorIs(t, err, errTest)
		_, isExit := errors.AsType[*shellExitError](err)
		assert.False(t, isExit)
		assert.Len(t, e.store.RemoveCalls(), 1)
		assert.False(t, e.locked(t))
	})
	t.Run("a failed session record removes the workspace without a shell", func(t *testing.T) {
		e := newShellEnv(t)
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return errTest }
		err := e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}})
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, e.shell.RunCalls())
		assert.Len(t, e.store.RemoveCalls(), 1)
		assert.False(t, e.locked(t))
	})
	t.Run("end to end: creates the workspace, runs the shell holding its lock, and removes it", func(t *testing.T) {
		t.Parallel()
		up := gittest.NewUpstream(t)
		up.Repo(t, "o", "n")
		root, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		paths := config.NewPaths(filepath.Join(root, "cfg"))
		scratch := filepath.Join(root, "scratch")
		require.NoError(t, os.MkdirAll(scratch, 0o750))
		configs := &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
			return paths, config.Config{Defaults: config.Defaults{Host: gittest.Host, Owners: []string{"o"}}}, nil
		}}
		environ := slices.Concat(up.Environ, []string{"SHELL=" + fakeShellScript(t, scratch)})
		stderr := &bytes.Buffer{}
		app := New("test", nil, &bytes.Buffer{}, stderr, configs, environ)
		exit := make(chan int, 1)
		go func() {
			exit <- app.Run(context.WithoutCancel(t.Context()), []string{"shell", "--name", "Try It", "n"})
		}()
		require.Eventually(t, func() bool {
			_, statErr := os.Stat(filepath.Join(scratch, "started"))
			return statErr == nil
		}, 30*time.Second, 20*time.Millisecond, "the shell started")
		lockPath := filepath.Join(paths.State, "try-it", "lock")
		_, ok, err := lockfile.TryExclusive(lockPath)
		require.NoError(t, err)
		assert.False(t, ok, "the session lock is held while the shell runs")
		assert.FileExists(t, filepath.Join(paths.State, "try-it", "session"))
		require.NoError(t, os.WriteFile(filepath.Join(scratch, "go"), nil, 0o600))
		select {
		case code := <-exit:
			assert.Equal(t, 9, code, "stderr: %s", stderr.String())
		case <-time.After(30 * time.Second):
			t.Fatal("ws shell didn't return")
		}
		wsPath := filepath.Join(paths.Workspaces, "try-it")
		out, err := os.ReadFile(filepath.Join(scratch, "out"))
		require.NoError(t, err)
		assert.Equal(t, "args=-i\npwd="+wsPath+"\nWS_ID=try-it\nWS_WORKSPACE="+wsPath+"\nfd3=open\nrepo=yes\n", string(out))
		assert.NoDirExists(t, wsPath)
		assert.NoDirExists(t, filepath.Join(paths.State, "try-it"))
	})
	t.Run("prints the cache's warnings before the shell", func(t *testing.T) {
		e := newShellEnv(t)
		c := stubCache()
		c.WarningsFunc = func() []string { return []string{"cached elsewhere"} }
		e.app.cache = c
		e.shell.RunFunc = func(context.Context, shell.Session) (int, error) {
			assert.Contains(t, e.stderr.String(), "ws: warning: cached elsewhere")
			return 0, nil
		}
		require.NoError(t, e.app.runShell(t.Context(), &shellOptions{specs: []string{"buff-bear"}}))
	})
}

func TestApp_shellSession(t *testing.T) {
	t.Run("records a shell session and runs the shell in the workspace", func(t *testing.T) {
		e := newShellEnv(t)
		lock, ok, err := lockfile.TryExclusiveLock(e.lockPath)
		require.NoError(t, err)
		require.True(t, ok)
		defer lock.Unlock()
		e.shell.RunFunc = func(context.Context, shell.Session) (int, error) { return 7, nil }
		status, err := e.app.shellSession(t.Context(), &e.ws, lock)
		require.NoError(t, err)
		assert.Equal(t, 7, status)
		require.Len(t, e.store.RecordSessionCalls(), 1)
		rec := e.store.RecordSessionCalls()[0]
		assert.Equal(t, "x", rec.ID)
		assert.Equal(t, workspace.SessionShell, rec.Kind)
		assert.Empty(t, rec.ClaudeSessionID)
		require.Len(t, e.shell.RunCalls(), 1)
		sess := e.shell.RunCalls()[0].Sess
		assert.Equal(t, "/cfg/workspaces/x", sess.Dir)
		assert.Equal(t, []string{"WS_ID=x", "WS_WORKSPACE=/cfg/workspaces/x"}, sess.Env)
		assert.Same(t, lock.File(), sess.Lock)
		assert.Empty(t, e.store.RemoveCalls(), "not before the shell exits")
	})
	t.Run("removes the workspace after the shell exits", func(t *testing.T) {
		e := newShellEnv(t)
		lock, ok, err := lockfile.TryExclusiveLock(e.lockPath)
		require.NoError(t, err)
		require.True(t, ok)
		defer lock.Unlock()
		_, err = e.app.shellSession(t.Context(), &e.ws, lock)
		require.NoError(t, err)
		require.Len(t, e.store.RemoveCalls(), 1)
		assert.Equal(t, "x", e.store.RemoveCalls()[0].ID)
	})
	t.Run("a failed session record removes the workspace and doesn't start the shell", func(t *testing.T) {
		e := newShellEnv(t)
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return errTest }
		_, err := e.app.shellSession(t.Context(), &e.ws, nil)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, e.shell.RunCalls())
		assert.Len(t, e.store.RemoveCalls(), 1)
	})
}

func TestApp_removeAfterShell(t *testing.T) {
	t.Run("removes the workspace through the deletion guard", func(t *testing.T) {
		e := newShellEnv(t)
		e.app.removeAfterShell("x")
		require.Len(t, e.store.RemoveCalls(), 1)
		assert.Equal(t, "x", e.store.RemoveCalls()[0].ID)
		assert.False(t, e.store.RemoveCalls()[0].Opts.AllowOutsideRoot)
		assert.Empty(t, e.stderr.String())
	})
	t.Run("reports a failure", func(t *testing.T) {
		e := newShellEnv(t)
		e.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return workspace.ErrUnsafe }
		e.app.removeAfterShell("x")
		assert.Equal(t, "ws: couldn't remove workspace x: refusing to delete\n", e.stderr.String())
	})
}

func TestApp_printWorkspaceWarnings(t *testing.T) {
	e := newShellEnv(t)
	e.store.WarningsFunc = func() []string { return []string{"a", "b"} }
	e.app.printWorkspaceWarnings()
	assert.Equal(t, "ws: warning: a\nws: warning: b\n", e.stderr.String())
}
