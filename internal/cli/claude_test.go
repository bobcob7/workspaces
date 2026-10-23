package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// claudeEnv is an App for ws claude whose workspace store creates
// workspace x with a real, held lock, and whose claude client starts
// session e8cdf5d8 with a prompt.
type claudeEnv struct {
	app      *App
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	store    *workspaceStoreMock
	claude   *claudeClientMock
	ws       workspace.Workspace
	lockPath string
}

func newClaudeEnv(t *testing.T) *claudeEnv {
	t.Helper()
	app, stdout, stderr := newTestApp()
	e := &claudeEnv{
		app:    app,
		stdout: stdout,
		stderr: stderr,
		ws: workspace.Workspace{ID: "x", Path: "/cfg/workspaces/x", Repos: []workspace.Repo{
			{ID: "github.com/bobcob7/buff-bear", Path: "buff-bear", Ref: "main", RefKind: "branch", Commit: "3f2a9c1e"},
		}},
		lockPath: filepath.Join(t.TempDir(), "lock"),
	}
	e.store = &workspaceStoreMock{
		CreateFunc: func(context.Context, workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			lock, ok, err := lockfile.TryExclusiveLock(e.lockPath)
			require.NoError(t, err)
			require.True(t, ok)
			return e.ws, lock, nil
		},
		RecordSessionFunc: func(string, workspace.SessionKind, string) error { return nil },
		RemoveFunc:        func(string, workspace.RemoveOptions) error { return nil },
		WarningsFunc:      func() []string { return nil },
	}
	e.claude = &claudeClientMock{
		CheckFunc: func(context.Context) error { return nil },
		StartFunc: func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{ID: "e8cdf5d8"}, nil
		},
		TrustFunc:  func(context.Context, string, string, func(string, bool) error) error { return nil },
		AttachFunc: func(context.Context, string) error { return nil },
		RemoveFunc: func(context.Context, string) error { return nil },
	}
	app.cache, app.workspaces, app.claude = stubCache(), e.store, e.claude
	app.paths = testPaths()
	return e
}

// idle makes the session start without a prompt.
func (e *claudeEnv) idle() {
	e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
		return claude.Started{ID: "e8cdf5d8", Idle: true}, nil
	}
}

// locked reports whether the workspace's lock is held.
func (e *claudeEnv) locked(t *testing.T) bool {
	t.Helper()
	unlock, ok, err := lockfile.TryExclusive(e.lockPath)
	require.NoError(t, err)
	if ok {
		unlock()
	}
	return !ok
}

// catchSignals makes the App catch signals with signal.Notify, as ws
// does, and returns a function that sends ws SIGHUP and waits until it
// has been caught. Call it only while ws catches SIGHUP: otherwise it
// would end the test binary.
func (e *claudeEnv) catchSignals(t *testing.T) (hangup func()) {
	t.Helper()
	var caught chan<- os.Signal
	e.app.notify = func(c chan<- os.Signal, sig ...os.Signal) {
		caught = c
		signal.Notify(c, sig...)
	}
	return func() {
		require.NotNil(t, caught, "ws catches SIGHUP")
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
		require.Eventually(t, func() bool { return len(caught) > 0 }, 10*time.Second, time.Millisecond)
	}
}

func (e *claudeEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	return e.app.runClaude(t.Context(), &claudeOptions{specs: []string{"buff-bear"}, claudeArgs: args})
}

// fakeClaudeScript writes an executable sh script standing in for claude
// 2.1.303. For --bg it records its arguments, working directory, and
// whether fd 3 is open in dir/out, then runs bgBody.
func fakeClaudeScript(t *testing.T, dir, bgBody string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		`if [ "$1" = --version ]; then echo "2.1.303 (Claude Code)"; exit 0; fi` + "\n" +
		`{ printf '%s\n' "$@"; echo "pwd=$(pwd -P)"; [ -e /dev/fd/3 ] && echo "fd3=open"; } > "` + dir + `/out"` + "\n" +
		bgBody + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

// claudeE2E is a real App with local upstreams, configured in a temp dir,
// whose claude is fakeClaudeScript.
type claudeE2E struct {
	app     *App
	paths   config.Paths
	scratch string
	// claudeJSON is Claude Code's global config, in its own temp
	// CLAUDE_CONFIG_DIR; it trusts the workspace root.
	claudeJSON string
	stdout     *bytes.Buffer
	stderr     *bytes.Buffer
}

func newClaudeE2E(t *testing.T, bgBody string) *claudeE2E {
	t.Helper()
	up := gittest.NewUpstream(t)
	up.Repo(t, "o", "n")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	e := &claudeE2E{paths: config.NewPaths(filepath.Join(root, "cfg")), scratch: filepath.Join(root, "scratch"), claudeJSON: filepath.Join(root, "claude-config", ".claude.json"), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	require.NoError(t, os.MkdirAll(e.scratch, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(e.claudeJSON), 0o700))
	e.trustRoot(t, true)
	environ := append(slices.Clone(up.Environ), "CLAUDE_CONFIG_DIR="+filepath.Dir(e.claudeJSON))
	configs := &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
		return e.paths, config.Config{Defaults: config.Defaults{Host: gittest.Host, Owners: []string{"o"}}}, nil
	}}
	e.app = New("test", nil, e.stdout, e.stderr, configs, environ)
	e.app.claude = claude.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), fakeClaudeScript(t, e.scratch, bgBody), environ)
	return e
}

// trustRoot writes Claude Code's config with the workspace root trusted
// or not.
func (e *claudeE2E) trustRoot(t *testing.T, trusted bool) {
	t.Helper()
	if trusted {
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		return
	}
	writeClaudeJSON(t, e.claudeJSON)
}

// trustedDirs are the directories Claude Code's config trusts.
func (e *claudeE2E) trustedDirs(t *testing.T) []string {
	t.Helper()
	return trustedDirsIn(t, e.claudeJSON)
}

func TestApp_newClaudeCmd(t *testing.T) {
	app, _, _ := newTestApp()
	newCmd := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newClaudeCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("has the create flags", func(t *testing.T) {
		cmd := app.newClaudeCmd()
		assertFlag(t, cmd, "name", "")
		assertFlag(t, cmd, "offline", "false")
	})
	t.Run("accepts no spec before --, for an empty workspace", func(t *testing.T) {
		cmd, args := newCmd(t, "--", "a prompt")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("refuses --settings without a spec", func(t *testing.T) {
		cmd, args := newCmd(t, "--", "--settings", "{}")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("refuses --settings after --", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--", "--settings", "{}", "a prompt")
		err := cmd.Args(cmd, args)
		requireUsageError(t, err)
		assert.ErrorIs(t, err, claude.ErrSettingsArg)
	})
	t.Run("runs", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.newStores = func(config.Paths, config.Config) stores {
			return stores{cache: stubCache(), workspaces: e.store, lifetime: stubLifetime()}
		}
		cmd := e.app.newClaudeCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.ParseFlags([]string{"--name", "Fix Login", "--offline", "buff-bear", "o/api", "--", "--model", "opus", "fix it"}))
		require.NoError(t, cmd.RunE(cmd, cmd.Flags().Args()))
		require.Len(t, e.store.CreateCalls(), 1)
		opts := e.store.CreateCalls()[0].Opts
		assert.Equal(t, "Fix Login", opts.Name)
		assert.True(t, opts.Offline)
		require.Len(t, opts.Specs, 2)
		assert.Equal(t, "o/api", opts.Specs[1].Raw)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, []string{"--model", "opus", "fix it"}, e.claude.StartCalls()[0].Opts.Args)
	})
}

func TestApp_runClaude(t *testing.T) {
	t.Run("holds the lock while the session starts and is recorded, then releases it", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			assert.True(t, e.locked(t), "while claude --bg runs")
			return claude.Started{ID: "e8cdf5d8"}, nil
		}
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error {
			assert.True(t, e.locked(t), "while recording the session")
			return nil
		}
		require.NoError(t, e.run(t, "fix it"))
		assert.False(t, e.locked(t), "released at the end")
	})
	t.Run("records the session after it starts", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.run(t, "fix it"))
		require.Len(t, e.store.RecordSessionCalls(), 1)
		rec := e.store.RecordSessionCalls()[0]
		assert.Equal(t, "x", rec.ID)
		assert.Equal(t, workspace.SessionClaude, rec.Kind)
		assert.Equal(t, "e8cdf5d8", rec.ClaudeSessionID)
		assert.Empty(t, e.store.RemoveCalls())
	})
	t.Run("prints the workspace id, the session id, and the path", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.run(t, "fix it"))
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
		assert.Empty(t, e.stderr.String())
		assert.Empty(t, e.claude.AttachCalls())
	})
	t.Run("checks claude before creating anything", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.CheckFunc = func(context.Context) error { return claude.ErrNotInstalled }
		requireCode(t, codeClaudeMissing, e.run(t, "fix it"))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("a failed creation has its error code and starts nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.CreateFunc = func(context.Context, workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			return workspace.Workspace{}, nil, workspace.ErrExists
		}
		requireCode(t, codeWorkspaceExists, e.run(t, "fix it"))
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("an untrusted workspace is removed", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{}, claude.ErrUntrusted
		}
		requireCode(t, codeClaudeUntrusted, e.run(t, "fix it"))
		require.Len(t, e.store.RemoveCalls(), 1)
		assert.Equal(t, "x", e.store.RemoveCalls()[0].ID)
		assert.Empty(t, e.store.RecordSessionCalls())
		assert.False(t, e.locked(t))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("any other failed start removes the workspace", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{}, &claude.CommandError{Args: []string{"--bg"}, ExitCode: 1, Stderr: "error: bad flag", Err: errTest}
		}
		err := e.run(t, "--bad")
		requireCode(t, codeClaudeFailed, err)
		assert.Contains(t, err.Error(), "error: bad flag")
		assert.Len(t, e.store.RemoveCalls(), 1)
	})
	t.Run("a session that can't be recorded is removed with its workspace", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return errTest }
		err := e.run(t, "fix it")
		require.ErrorIs(t, err, errTest)
		assert.Equal(t, codeInternal, asCLIError(err).code)
		require.Len(t, e.claude.RemoveCalls(), 1)
		assert.Equal(t, "e8cdf5d8", e.claude.RemoveCalls()[0].ID)
		assert.Len(t, e.store.RemoveCalls(), 1)
		assert.False(t, e.locked(t))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("attaches to an idle session on a terminal, with the lock released", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.idle()
		e.app.interactive = true
		e.claude.AttachFunc = func(context.Context, string) error {
			assert.False(t, e.locked(t), "before attaching")
			return nil
		}
		require.NoError(t, e.run(t))
		require.Len(t, e.claude.AttachCalls(), 1)
		assert.Equal(t, "e8cdf5d8", e.claude.AttachCalls()[0].ID)
		assert.Empty(t, e.stdout.String())
		assert.Equal(t, "ws: workspace x at /cfg/workspaces/x; attaching to Claude Code session e8cdf5d8\n", e.stderr.String())
	})
	t.Run("prints instead of attaching after a hangup during the start", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.interactive = true
		hangup := e.catchSignals(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			hangup()
			return claude.Started{ID: "e8cdf5d8", Idle: true}, nil
		}
		require.NoError(t, e.run(t))
		assert.Empty(t, e.claude.AttachCalls())
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
	})
	t.Run("prints when it can't attach", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.idle()
		e.app.interactive = true
		e.claude.AttachFunc = func(context.Context, string) error { return errTest }
		require.NoError(t, e.run(t))
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
		assert.Contains(t, e.stderr.String(), "couldn't attach to session e8cdf5d8; run `claude attach e8cdf5d8`: boom")
	})
	t.Run("prints instead of attaching when interrupted during the start", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.interactive = true
		ctx, cancel := context.WithCancel(t.Context())
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			cancel()
			return claude.Started{ID: "e8cdf5d8", Idle: true}, nil
		}
		require.NoError(t, e.app.runClaude(ctx, &claudeOptions{specs: []string{"buff-bear"}}))
		assert.Empty(t, e.claude.AttachCalls())
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
		assert.False(t, e.locked(t))
	})
	t.Run("prints instead of attaching without a terminal", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.idle()
		require.NoError(t, e.run(t))
		assert.Empty(t, e.claude.AttachCalls())
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
	})
	t.Run("JSON output", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.global.json = true
		e.store.WarningsFunc = func() []string { return []string{"left something"} }
		require.NoError(t, e.run(t, "fix it"))
		var got map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, map[string]any{
			"id":      "x",
			"session": "e8cdf5d8",
			"path":    "/cfg/workspaces/x",
			"repos": []any{map[string]any{
				"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "main", "ref_kind": "branch", "commit": "3f2a9c1e",
			}},
			"warnings": []any{"left something"},
		}, got)
		assert.Empty(t, e.stderr.String())
	})
	t.Run("end to end: starts the session in the workspace's one repo and records it", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, `printf 'backgrounded · e8cdf5d8 · try-it\n  claude agents  list sessions\n'`)
		code := e.app.Run(t.Context(), []string{"claude", "--name", "Try It", "n", "--", "--model", "opus", "fix it"})
		require.Equal(t, 0, code, "stderr: %s", e.stderr.String())
		wsPath := filepath.Join(e.paths.Workspaces, "try-it")
		assert.Equal(t, "try-it  e8cdf5d8  "+wsPath+"\n", e.stdout.String())
		out, err := os.ReadFile(filepath.Join(e.scratch, "out"))
		require.NoError(t, err)
		assert.Equal(t, strings.Join([]string{
			"--bg", "-n", "try-it", "--settings", `{"worktree":{"bgIsolation":"none"}}`, "--model", "opus", "fix it", "pwd=" + filepath.Join(wsPath, "n"),
		}, "\n")+"\n", string(out), "claude ran in the workspace without the lock")
		assert.Equal(t, []string{e.paths.Workspaces, filepath.Join(wsPath, "n")}, e.trustedDirs(t))
		session, err := os.ReadFile(filepath.Join(e.paths.State, "try-it", "session"))
		require.NoError(t, err)
		assert.Contains(t, string(session), `kind = "claude"`)
		assert.Contains(t, string(session), `claude_session_id = "e8cdf5d8"`)
		assert.DirExists(t, filepath.Join(wsPath, "n", ".git"))
		takeLock(t, filepath.Join(e.paths.State, "try-it", "lock")).Unlock()
	})
	t.Run("end to end: no specs starts the session in an empty workspace", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, `printf 'backgrounded · e8cdf5d8 · notes\n'`)
		code := e.app.Run(t.Context(), []string{"--json", "claude", "--name", "notes", "--", "draft notes"})
		require.Equal(t, 0, code, "stderr: %s", e.stderr.String())
		var res claudeResult
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &res))
		assert.Equal(t, "e8cdf5d8", res.Session)
		assert.Empty(t, res.Repos)
		assert.Contains(t, e.stdout.String(), `"repos":[]`)
		entries, err := os.ReadDir(filepath.Join(e.paths.Workspaces, "notes"))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("end to end: a failed start takes back the trust ws added", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, "echo 'error: boom' >&2; exit 1")
		code := e.app.Run(t.Context(), []string{"--json", "claude", "--name", "t", "n", "--", "fix it"})
		assert.Equal(t, exitFailed, code)
		assert.NoDirExists(t, filepath.Join(e.paths.Workspaces, "t"))
		assert.Equal(t, []string{e.paths.Workspaces}, e.trustedDirs(t))
	})
	t.Run("end to end: an untrusted workspace leaves nothing", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, "echo 'Workspace not trusted. Run `claude` in /w once and accept the trust prompt, then retry.' >&2; exit 1")
		code := e.app.Run(t.Context(), []string{"--json", "claude", "--name", "t", "n", "--", "fix it"})
		assert.Equal(t, exitFailed, code)
		assert.Empty(t, e.stdout.String())
		var body struct {
			Error errorBody `json:"error"`
		}
		require.NoError(t, json.Unmarshal(e.stderr.Bytes(), &body))
		assert.Equal(t, codeClaudeUntrusted, body.Error.Code)
		assert.Equal(t, e.paths.Workspaces, body.Error.Details["directory"])
		assert.NoDirExists(t, filepath.Join(e.paths.Workspaces, "t"))
		assert.NoDirExists(t, filepath.Join(e.paths.State, "t"))
	})
}

func TestApp_handOff(t *testing.T) {
	t.Run("catches SIGHUP only while the session starts and is recorded", func(t *testing.T) {
		e := newClaudeEnv(t)
		var caught []os.Signal
		var watching chan<- os.Signal
		e.app.notify = func(c chan<- os.Signal, sig ...os.Signal) { watching, caught = c, sig }
		e.app.stopNotify = func(c chan<- os.Signal) {
			assert.Equal(t, watching, c)
			watching = nil
		}
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			assert.NotNil(t, watching, "while claude --bg runs")
			return claude.Started{ID: "e8cdf5d8"}, nil
		}
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error {
			assert.NotNil(t, watching, "while the session is recorded")
			return nil
		}
		_, hungUp, err := e.app.handOff(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		assert.False(t, hungUp)
		assert.Equal(t, []os.Signal{syscall.SIGHUP}, caught)
		assert.Nil(t, watching, "stopped afterwards")
	})
	t.Run("stops catching after a failed start", func(t *testing.T) {
		e := newClaudeEnv(t)
		stopped := false
		e.app.notify = func(chan<- os.Signal, ...os.Signal) {}
		e.app.stopNotify = func(chan<- os.Signal) { stopped = true }
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{}, claude.ErrUntrusted
		}
		_, _, err := e.app.handOff(t.Context(), &e.ws, nil)
		requireCode(t, codeClaudeUntrusted, err)
		assert.True(t, stopped)
	})
	t.Run("survives a real SIGHUP during the start and reports it", func(t *testing.T) {
		e := newClaudeEnv(t)
		hangup := e.catchSignals(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			hangup()
			return claude.Started{ID: "e8cdf5d8"}, nil
		}
		started, hungUp, err := e.app.handOff(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		assert.True(t, hungUp)
		assert.Equal(t, "e8cdf5d8", started.ID)
		assert.Len(t, e.store.RecordSessionCalls(), 1)
	})
	t.Run("end to end: starts the session in the workspace when Claude Code doesn't trust the root", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, `printf 'backgrounded · e8cdf5d8 · try-it\n'`)
		e.trustRoot(t, false)
		code := e.app.Run(t.Context(), []string{"claude", "--name", "Try It", "n", "--", "fix it"})
		require.Equal(t, 0, code, "stderr: %s", e.stderr.String())
		out, err := os.ReadFile(filepath.Join(e.scratch, "out"))
		require.NoError(t, err)
		assert.Contains(t, string(out), "pwd="+filepath.Join(e.paths.Workspaces, "try-it")+"\n")
		assert.Empty(t, e.trustedDirs(t))
		assert.Empty(t, e.stderr.String())
	})
}

func TestApp_startAgent(t *testing.T) {
	t.Run("trusts the one repo's directory below the workspace root", func(t *testing.T) {
		e := newClaudeEnv(t)
		_, err := e.app.startAgent(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		require.Len(t, e.claude.TrustCalls(), 1)
		assert.Equal(t, "/cfg/workspaces/x/buff-bear", e.claude.TrustCalls()[0].Dir)
		assert.Equal(t, testPaths().Workspaces, e.claude.TrustCalls()[0].Parent)
	})
	t.Run("starts in the workspace, quietly, when the root isn't trusted", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.TrustFunc = func(context.Context, string, string, func(string, bool) error) error {
			return claude.ErrParentUntrusted
		}
		_, err := e.app.startAgent(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, "/cfg/workspaces/x", e.claude.StartCalls()[0].Opts.Dir)
		assert.Empty(t, e.app.warnings())
	})
	t.Run("starts in the workspace, with a warning, when trusting fails", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.TrustFunc = func(context.Context, string, string, func(string, bool) error) error { return errTest }
		_, err := e.app.startAgent(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, "/cfg/workspaces/x", e.claude.StartCalls()[0].Opts.Dir)
		require.Len(t, e.app.warnings(), 1)
		assert.Contains(t, e.app.warnings()[0], "starting the agent in the workspace, not in its repo")
	})
	t.Run("starts the session in the one repo's directory under the workspace's id", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.ws.Name = "Fix Login"
		started, err := e.app.startAgent(t.Context(), &e.ws, []string{"fix it"})
		require.NoError(t, err)
		assert.Equal(t, "e8cdf5d8", started.ID)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, claude.StartOptions{Dir: "/cfg/workspaces/x/buff-bear", Name: "x", Args: []string{"fix it"}}, e.claude.StartCalls()[0].Opts)
	})
	t.Run("starts the session in the workspace with several repos", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.ws.Repos = append(e.ws.Repos, workspace.Repo{ID: "github.com/bobcob7/api", Path: "api"})
		_, err := e.app.startAgent(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, "/cfg/workspaces/x", e.claude.StartCalls()[0].Opts.Dir)
	})
	t.Run("starts the session in an empty workspace", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.ws.Repos = nil
		_, err := e.app.startAgent(t.Context(), &e.ws, nil)
		require.NoError(t, err)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, "/cfg/workspaces/x", e.claude.StartCalls()[0].Opts.Dir)
	})
	t.Run("a failed start removes the workspace and records nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{}, claude.ErrUnexpectedOutput
		}
		_, err := e.app.startAgent(t.Context(), &e.ws, nil)
		requireCode(t, codeClaudeFailed, err)
		assert.Len(t, e.store.RemoveCalls(), 1)
		assert.Empty(t, e.store.RecordSessionCalls())
		assert.Empty(t, e.claude.RemoveCalls())
	})
	t.Run("interrupted before the start removes the workspace and starts nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := e.app.startAgent(ctx, &e.ws, []string{"fix it"})
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, e.claude.StartCalls())
		assert.Len(t, e.store.RemoveCalls(), 1)
		assert.Empty(t, e.store.RecordSessionCalls())
	})
	t.Run("interrupted during the start still records the session", func(t *testing.T) {
		e := newClaudeEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			cancel()
			return claude.Started{ID: "e8cdf5d8"}, nil
		}
		_, err := e.app.startAgent(ctx, &e.ws, []string{"fix it"})
		require.NoError(t, err)
		require.Len(t, e.store.RecordSessionCalls(), 1)
		assert.Empty(t, e.store.RemoveCalls())
	})
}

func TestApp_startError(t *testing.T) {
	t.Run("untrusted names the workspace root to trust", func(t *testing.T) {
		e := newClaudeEnv(t)
		err := e.app.startError(&claude.CommandError{Stderr: "Workspace not trusted. Run `claude` in /cfg/workspaces/x once", Err: claude.ErrUntrusted})
		requireCode(t, codeClaudeUntrusted, err)
		assert.Equal(t, "the workspace was removed: Claude Code doesn't trust it\n"+
			"Trust the workspace root once: run `claude` in \"/cfg/workspaces\", accept the trust prompt, then retry", err.Error())
		ce := asCLIError(err)
		assert.Equal(t, "/cfg/workspaces", ce.details["directory"])
		assert.Equal(t, "Workspace not trusted. Run `claude` in /cfg/workspaces/x once", ce.details["stderr"])
	})
	t.Run("other failures keep their code", func(t *testing.T) {
		e := newClaudeEnv(t)
		requireCode(t, codeClaudeMissing, e.app.startError(claude.ErrNotInstalled))
	})
}

func TestApp_removeSession(t *testing.T) {
	t.Run("removes the session even after ctx ends", func(t *testing.T) {
		e := newClaudeEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		e.claude.RemoveFunc = func(ctx context.Context, _ string) error { return ctx.Err() }
		e.app.removeSession(ctx, "e8cdf5d8")
		require.Len(t, e.claude.RemoveCalls(), 1)
		assert.Empty(t, e.stderr.String())
	})
	t.Run("reports a failure with the command to finish it", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.RemoveFunc = func(context.Context, string) error { return errTest }
		e.app.removeSession(t.Context(), "e8cdf5d8")
		assert.Equal(t, "ws: couldn't remove Claude Code session e8cdf5d8; run `claude rm e8cdf5d8`: boom\n", e.stderr.String())
	})
}

func TestApp_shouldAttach(t *testing.T) {
	idle := claude.Started{ID: "e8cdf5d8", Idle: true}
	newApp := func() *App {
		app, _, _ := newTestApp()
		app.interactive = true
		return app
	}
	t.Run("idle on a terminal", func(t *testing.T) {
		assert.True(t, newApp().shouldAttach(t.Context(), idle, false))
	})
	t.Run("with a prompt", func(t *testing.T) {
		assert.False(t, newApp().shouldAttach(t.Context(), claude.Started{ID: "e8cdf5d8"}, false))
	})
	t.Run("not on a terminal", func(t *testing.T) {
		app := newApp()
		app.interactive = false
		assert.False(t, app.shouldAttach(t.Context(), idle, false))
	})
	t.Run("with --json", func(t *testing.T) {
		app := newApp()
		app.global.json = true
		assert.False(t, app.shouldAttach(t.Context(), idle, false))
	})
	t.Run("interrupted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.False(t, newApp().shouldAttach(ctx, idle, false))
	})
	t.Run("hung up on", func(t *testing.T) {
		assert.False(t, newApp().shouldAttach(t.Context(), idle, true))
	})
}

func TestApp_attach(t *testing.T) {
	t.Run("says where the session is, then attaches", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.AttachFunc = func(context.Context, string) error {
			assert.Equal(t, "ws: workspace x at /cfg/workspaces/x; attaching to Claude Code session e8cdf5d8\n", e.stderr.String(), "before attaching")
			return nil
		}
		assert.True(t, e.app.attach(t.Context(), &e.ws, "e8cdf5d8"))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("says nothing with --quiet", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.global.quiet = true
		assert.True(t, e.app.attach(t.Context(), &e.ws, "e8cdf5d8"))
		assert.Empty(t, e.stderr.String())
	})
	t.Run("couldn't attach", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.claude.AttachFunc = func(context.Context, string) error { return errTest }
		assert.False(t, e.app.attach(t.Context(), &e.ws, "e8cdf5d8"))
		assert.Equal(t, "ws: workspace x at /cfg/workspaces/x; attaching to Claude Code session e8cdf5d8\n"+
			"ws: warning: couldn't attach to session e8cdf5d8; run `claude attach e8cdf5d8`: boom\n", e.stderr.String())
	})
}

func TestApp_printClaudeResult(t *testing.T) {
	t.Run("text, with warnings on stderr", func(t *testing.T) {
		e := newClaudeEnv(t)
		c := stubCache()
		c.WarningsFunc = func() []string { return []string{"cached elsewhere"} }
		e.app.cache = c
		require.NoError(t, e.app.printClaudeResult(&e.ws, "e8cdf5d8"))
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
		assert.Equal(t, "ws: warning: cached elsewhere\n", e.stderr.String())
	})
	t.Run("JSON with no repos or warnings has empty arrays", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.global.json = true
		require.NoError(t, e.app.printClaudeResult(&workspace.Workspace{ID: "x", Path: "/p"}, "e8cdf5d8"))
		assert.JSONEq(t, `{"id":"x","session":"e8cdf5d8","path":"/p","repos":[],"warnings":[]}`, e.stdout.String())
	})
}

func TestSpecsBeforeDash(t *testing.T) {
	parse := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		root := &cobra.Command{Use: "ws"}
		cmd := &cobra.Command{Use: "claude"}
		root.AddCommand(cmd)
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("accepts a spec", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("accepts a spec and claude args", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear", "--", "prompt")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("accepts nothing, for an empty workspace", func(t *testing.T) {
		cmd, args := parse(t)
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear@", "--", "prompt")
		requireCode(t, codeSpecInvalid, specsBeforeDash(cmd, args))
	})
	t.Run("checks only specs before --", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear", "--", "not a spec: @{")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("accepts only claude args, for an empty workspace", func(t *testing.T) {
		cmd, args := parse(t, "--", "prompt")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
}

func TestClaudeArgsAfterDash(t *testing.T) {
	parse := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := &cobra.Command{}
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("accepts claude options and a prompt", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear", "--", "--model", "opus", "fix it")
		assert.NoError(t, claudeArgsAfterDash(cmd, args))
	})
	t.Run("refuses --settings", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear", "--", "--settings={}")
		requireUsageError(t, claudeArgsAfterDash(cmd, args))
	})
	t.Run("nothing after --", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear")
		assert.NoError(t, claudeArgsAfterDash(cmd, args))
	})
}

func TestSplitAtDash(t *testing.T) {
	parse := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{}
		require.NoError(t, cmd.ParseFlags(args))
		return cmd
	}
	t.Run("no dash", func(t *testing.T) {
		cmd := parse(t, "a", "b")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Equal(t, []string{"a", "b"}, before)
		assert.Nil(t, after)
	})
	t.Run("args on both sides", func(t *testing.T) {
		cmd := parse(t, "a", "--", "--model", "opus", "prompt")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Equal(t, []string{"a"}, before)
		assert.Equal(t, []string{"--model", "opus", "prompt"}, after)
	})
	t.Run("nothing before the dash", func(t *testing.T) {
		cmd := parse(t, "--", "prompt")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Empty(t, before)
		assert.Equal(t, []string{"prompt"}, after)
	})
	t.Run("nothing after the dash", func(t *testing.T) {
		cmd := parse(t, "a", "--")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Equal(t, []string{"a"}, before)
		assert.Empty(t, after)
	})
}

func TestAddCreateFlags(t *testing.T) {
	var (
		name    string
		offline bool
	)
	cmd := &cobra.Command{}
	addCreateFlags(cmd, &name, &offline)
	require.NoError(t, cmd.ParseFlags([]string{"--name", "fix-login", "--offline"}))
	assert.Equal(t, "fix-login", name)
	assert.True(t, offline)
}
