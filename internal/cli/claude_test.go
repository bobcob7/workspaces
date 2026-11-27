//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

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
		require.NoError(t, cmd.ParseFlags([]string{"--name", "Fix Login", "--offline", "--hidden", "buff-bear", "o/api", "--", "--model", "opus", "fix it"}))
		require.NoError(t, cmd.RunE(cmd, cmd.Flags().Args()))
		require.Len(t, e.store.CreateCalls(), 1)
		opts := e.store.CreateCalls()[0].Opts
		assert.Equal(t, "Fix Login", opts.Name)
		assert.True(t, opts.Offline)
		assert.True(t, opts.Hidden)
		require.Len(t, opts.Specs, 2)
		assert.Equal(t, "o/api", opts.Specs[1].Raw)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, []string{"--model", "opus", "fix it"}, e.claude.StartCalls()[0].Opts.Args)
	})
}

func TestApp_artifactHints(t *testing.T) {
	t.Run("a reference to an artifact gets the hint line after the prompt", func(t *testing.T) {
		e, id := hintEnv(t)
		prompt := "fix it @artifact:" + id
		require.NoError(t, e.run(t, prompt))
		assert.Equal(t, []string{prompt + "\n\n" + artifact.HintLine([]string{id})}, e.claude.StartCalls()[0].Opts.Args)
	})
	t.Run("an unknown reference is artifact_unknown, and creates no workspace", func(t *testing.T) {
		e, _ := hintEnv(t)
		requireCode(t, codeArtifactUnknown, e.run(t, "fix it @artifact:art-00000000"))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("a prompt without a reference is unchanged", func(t *testing.T) {
		e, _ := hintEnv(t)
		require.NoError(t, e.run(t, "fix it"))
		assert.Equal(t, []string{"fix it"}, e.claude.StartCalls()[0].Opts.Args)
	})
	t.Run("on a remote environment the prompt is unchanged, with a warning", func(t *testing.T) {
		e, id := hintEnv(t)
		args, err := e.app.artifactHints([]string{"fix it @artifact:" + id}, "vega")
		require.NoError(t, err)
		assert.Equal(t, []string{"fix it @artifact:" + id}, args)
		assert.Equal(t, []string{"artifacts aren't reachable from vega yet; @artifact references were sent as plain text"}, e.app.warned)
	})
	t.Run("an enrolled agent passes references with a warning", func(t *testing.T) {
		e, _ := hintEnv(t)
		require.NoError(t, os.MkdirAll(e.app.paths.Environment, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(e.app.paths.Environment, "agent.toml"), []byte("x"), 0o600))
		prompt := "fix it @artifact:art-00000000"
		args, err := e.app.artifactHints([]string{prompt}, "")
		require.NoError(t, err)
		assert.Equal(t, []string{prompt}, args)
		assert.Equal(t, []string{"artifacts aren't reachable on an environment's agent; @artifact references were sent as plain text"}, e.app.warned)
	})
}

func TestApp_runClaude_parent(t *testing.T) {
	inWorkspace := func(e *claudeEnv) {
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
	}
	t.Run("outside a workspace, a root", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.run(t, "fix it"))
		assert.Empty(t, e.store.CreateCalls()[0].Opts.Parent)
	})
	t.Run("locally, no environment is stamped", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.run(t, "fix it"))
		assert.Empty(t, e.store.CreateCalls()[0].Opts.Environment)
	})
	t.Run("inside a workspace, its child", func(t *testing.T) {
		e := newClaudeEnv(t)
		inWorkspace(e)
		require.NoError(t, e.run(t, "fix it"))
		assert.Equal(t, "fix-login-d8efb24f", e.store.CreateCalls()[0].Opts.Parent)
	})
	t.Run("--parent none, a root anywhere", func(t *testing.T) {
		e := newClaudeEnv(t)
		inWorkspace(e)
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{parent: "none", claudeArgs: []string{"fix it"}}))
		assert.Empty(t, e.store.CreateCalls()[0].Opts.Parent)
	})
	t.Run("--parent names one", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{parent: "fix-login-d8efb24f", claudeArgs: []string{"fix it"}}))
		assert.Equal(t, "fix-login-d8efb24f", e.store.CreateCalls()[0].Opts.Parent)
	})
	t.Run("--parent naming no workspace creates nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		requireCode(t, codeWorkspaceNotFound, e.app.runClaude(t.Context(), &claudeOptions{parent: "nope", claudeArgs: []string{"fix it"}}))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("--json prints the parent", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.ws.Parent = "fix-login-d8efb24f"
		e.app.global.json = true
		require.NoError(t, e.run(t, "fix it"))
		assert.Contains(t, e.stdout.String(), `"parent":"fix-login-d8efb24f"`)
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
			"parent":  nil,
			"repos": []any{map[string]any{
				"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "main", "ref_kind": "branch", "commit": "3f2a9c1e",
			}},
			"warnings":  []any{"left something"},
			"runner":    "claude",
			"model":     nil,
			"ws_runner": nil,
		}, got)
		assert.Empty(t, e.stderr.String())
	})
	t.Run("end to end: starts the session in the workspace's one repo and records it", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, `printf 'backgrounded · e8cdf5d8 · try-it\n  claude agents  list sessions\n'`)
		code := e.app.Run(t.Context(), []string{"claude", "--name", "Try It", "n", "--", "--model", "opus", "fix it"})
		require.Equal(t, 0, code, "stderr: %s", e.stderr.String())
		id := workspaceID(t, e.paths.State, "try-it")
		wsPath := filepath.Join(e.paths.Workspaces, id)
		assert.Equal(t, id+"  e8cdf5d8  "+wsPath+"\n", e.stdout.String())
		out, err := os.ReadFile(filepath.Join(e.scratch, "out"))
		require.NoError(t, err)
		assert.Equal(t, strings.Join([]string{
			"--bg", "-n", id, "--settings", `{"worktree":{"bgIsolation":"none"}}`, "--model", "opus", "fix it", "pwd=" + filepath.Join(wsPath, "n"),
		}, "\n")+"\n", string(out), "claude ran in the workspace without the lock")
		assert.Equal(t, []string{e.paths.Workspaces, filepath.Join(wsPath, "n")}, e.trustedDirs(t))
		session, err := os.ReadFile(filepath.Join(e.paths.State, id, "session"))
		require.NoError(t, err)
		assert.Contains(t, string(session), `kind = "claude"`)
		assert.Contains(t, string(session), `claude_session_id = "e8cdf5d8"`)
		assert.DirExists(t, filepath.Join(wsPath, "n", ".git"))
		takeLock(t, filepath.Join(e.paths.State, id, "lock")).Unlock()
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
		assert.Regexp(t, `^notes-[0-9a-f]{8}$`, res.ID)
		entries, err := os.ReadDir(filepath.Join(e.paths.Workspaces, res.ID))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("end to end: a failed start takes back the trust ws added", func(t *testing.T) {
		t.Parallel()
		e := newClaudeE2E(t, "echo 'error: boom' >&2; exit 1")
		code := e.app.Run(t.Context(), []string{"--json", "claude", "--name", "t", "n", "--", "fix it"})
		assert.Equal(t, exitFailed, code)
		assertNoWorkspace(t, &e.paths, "t")
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
		assertNoWorkspace(t, &e.paths, "t")
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
		assert.Contains(t, string(out), "pwd="+filepath.Join(e.paths.Workspaces, workspaceID(t, e.paths.State, "try-it"))+"\n")
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
		require.NoError(t, e.app.printClaudeResult(&e.ws, "e8cdf5d8", nil))
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
		assert.Equal(t, "ws: warning: cached elsewhere\n", e.stderr.String())
	})
	t.Run("JSON with no repos or warnings has empty arrays", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.global.json = true
		require.NoError(t, e.app.printClaudeResult(&workspace.Workspace{ID: "x", Path: "/p"}, "e8cdf5d8", nil))
		assert.JSONEq(t, `{"id":"x","session":"e8cdf5d8","path":"/p","parent":null,"repos":[],"warnings":[],"runner":"claude","model":null,"ws_runner":null}`, e.stdout.String())
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

// noClaude is a Claude Code client that fails the test if it is used.
func noClaude(t *testing.T) *claudeClientMock {
	t.Helper()
	return &claudeClientMock{
		CheckFunc: func(context.Context) error {
			t.Error("ws claude checked Claude Code")
			return errTest
		},
		StartFunc: func(context.Context, claude.StartOptions) (claude.Started, error) {
			t.Error("ws claude started a Claude agent")
			return claude.Started{}, errTest
		},
	}
}

func TestApp_newClaudeCmd_runner(t *testing.T) {
	app, _, _ := newTestApp()
	parse := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newClaudeCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("has the runner flags, defaulting to claude with no endpoint", func(t *testing.T) {
		cmd := app.newClaudeCmd()
		assertFlag(t, cmd, "runner", "claude")
		assertFlag(t, cmd, "endpoint", "")
	})
	t.Run("a ws agent takes a prompt and then --model", func(t *testing.T) {
		cmd, args := parse(t, "--runner", "ws", "--", "write docs", "--model", "qwen9b")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("a ws agent without a prompt is usage", func(t *testing.T) {
		cmd, args := parse(t, "--runner", "ws", "buff-bear")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("a ws agent with anything but --model after the prompt is usage", func(t *testing.T) {
		cmd, args := parse(t, "--runner", "ws", "--", "write docs", "--permission-mode", "auto")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("--endpoint needs a ws agent", func(t *testing.T) {
		cmd, args := parse(t, "--endpoint", "gpu", "--", "fix it")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("an unknown runner is usage", func(t *testing.T) {
		cmd, args := parse(t, "--runner", "gpu", "--", "fix it")
		requireUsageError(t, cmd.Args(cmd, args))
	})
}

func TestApp_runClaude_runner(t *testing.T) {
	run := func(t *testing.T, e *runnerEnv, args ...string) int {
		t.Helper()
		argv := append([]string{"claude", "--json", "--runner", "ws", "--name", "Fix Login"}, args...)
		return e.app.execute(t.Context(), e.app.newRootCmd(), argv)
	}
	t.Run("starts a ws agent under the parent ws claude chooses, and prints it as JSON", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.claude = noClaude(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		code := run(t, e, "--", "fix it", "--model", "qwen9b")
		require.Equal(t, exitOK, code, e.stderr.String())
		var got struct {
			Session  string  `json:"session"`
			Parent   *string `json:"parent"`
			Runner   string  `json:"runner"`
			Model    *string `json:"model"`
			WSRunner *struct {
				ModelAlias    *string `json:"model_alias"`
				Endpoint      string  `json:"endpoint"`
				ContextTokens int     `json:"context_tokens"`
			} `json:"ws_runner"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got), e.stdout.String())
		assert.True(t, strings.HasPrefix(got.Session, "rn-"))
		require.NotNil(t, got.Parent)
		assert.Equal(t, "plan", *got.Parent)
		assert.Equal(t, "ws", got.Runner)
		require.NotNil(t, got.Model)
		assert.Equal(t, "qwen/qwen3.5-9b", *got.Model)
		require.NotNil(t, got.WSRunner)
		require.NotNil(t, got.WSRunner.ModelAlias)
		assert.Equal(t, "qwen9b", *got.WSRunner.ModelAlias)
		assert.Equal(t, "gpu", got.WSRunner.Endpoint)
		assert.Equal(t, 65536, got.WSRunner.ContextTokens)
		require.Len(t, e.store.CreateCalls(), 1)
		assert.Equal(t, "plan", e.store.CreateCalls()[0].Opts.Parent)
		assert.Equal(t, "plan", readRun(t, e, "fix-login-d8efb24f").Parent)
		require.Len(t, e.daemon.StartRunnerCalls(), 1)
	})
	t.Run("--parent none makes it top-level", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.claude = noClaude(t)
		code := run(t, e, "--parent", "none", "--", "fix it")
		require.Equal(t, exitOK, code, e.stderr.String())
		var got struct {
			Parent *string `json:"parent"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got), e.stdout.String())
		assert.Nil(t, got.Parent)
		require.Len(t, e.store.CreateCalls(), 1)
		assert.Empty(t, e.store.CreateCalls()[0].Opts.Parent)
		assert.Empty(t, readRun(t, e, "fix-login-d8efb24f").Parent)
	})
	t.Run("a missing or unusable prompt, or other arguments after it, is usage, and nothing is created", func(t *testing.T) {
		cases := map[string][]string{
			"no prompt":                  nil,
			"a blank prompt":             {"--", "  "},
			"--permission-mode after it": {"--", "fix it", "--permission-mode", "auto"},
		}
		for name, args := range cases {
			t.Run(name, func(t *testing.T) {
				e := newRunnerEnv(t, runnerToml)
				e.app.claude = noClaude(t)
				code := run(t, e, args...)
				assert.Equal(t, exitUsage, code)
				assert.Equal(t, "usage", errorCodeOf(t, e.stderr))
				assert.Empty(t, e.store.CreateCalls())
				assert.Empty(t, e.daemon.PreflightCalls())
			})
		}
	})
	t.Run("--endpoint without --runner ws is usage, and nothing is created", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.claude = noClaude(t)
		code := e.app.execute(t.Context(), e.app.newRootCmd(), []string{"claude", "--json", "--endpoint", "cpu", "--", "fix it"})
		assert.Equal(t, exitUsage, code)
		assert.Equal(t, "usage", errorCodeOf(t, e.stderr))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("a preflight failure is its code, with no workspace and no Claude agent", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.claude = noClaude(t)
		e.daemon.PreflightFunc = func(context.Context, daemon.PreflightRequest) (daemon.PreflightResponse, error) {
			return daemon.PreflightResponse{}, &daemon.RemoteError{Code: "model_not_served", Message: "not served"}
		}
		code := run(t, e, "--", "fix it")
		assert.Equal(t, exitNotFound, code)
		assert.Equal(t, "model_not_served", errorCodeOf(t, e.stderr))
		assert.Empty(t, e.store.CreateCalls())
	})
}

func TestApp_runClaude_untouched(t *testing.T) {
	// ADR 0071: a Claude agent starts exactly as it did before the runner
	// existed, whatever the runner tables say, and never asks the daemon.
	variants := map[string][]string{
		"without --runner":     nil,
		"with --runner claude": {"--runner", "claude"},
	}
	for name, flags := range variants {
		t.Run(name, func(t *testing.T) {
			e := newClaudeEnv(t)
			e.app.dialDaemon = func(context.Context) (runnerDaemon, error) {
				t.Error("the Claude path asked the daemon")
				return nil, errTest
			}
			cfg := testConfig()
			cfg.RunnerErr = errors.New("a broken [runner] table")
			e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
				return config.NewPaths(t.TempDir()), cfg, nil
			}}
			e.app.newStores = func(config.Paths, config.Config) stores {
				return stores{cache: stubCache(), workspaces: e.store, lifetime: stubLifetime()}
			}
			argv := append([]string{"claude", "--json", "--name", "Fix Login"}, flags...)
			argv = append(argv, "--", "fix it", "--model", "opus")
			code := e.app.execute(t.Context(), e.app.newRootCmd(), argv)
			require.Equal(t, exitOK, code, e.stderr.String())
			var got map[string]any
			require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got), e.stdout.String())
			assert.Equal(t, "claude", got["runner"])
			assert.Equal(t, "opus", got["model"])
			assert.Nil(t, got["ws_runner"])
			require.Len(t, e.claude.StartCalls(), 1)
			assert.Equal(t, []string{"fix it", "--model", "opus"}, e.claude.StartCalls()[0].Opts.Args)
		})
	}
	t.Run("prints the same line without --json", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) {
			t.Error("the Claude path asked the daemon")
			return nil, errTest
		}
		e.app.newStores = func(config.Paths, config.Config) stores {
			return stores{cache: stubCache(), workspaces: e.store, lifetime: stubLifetime()}
		}
		code := e.app.execute(t.Context(), e.app.newRootCmd(), []string{"claude", "--name", "Fix Login", "--", "fix it"})
		require.Equal(t, exitOK, code, e.stderr.String())
		assert.Equal(t, "x  e8cdf5d8  /cfg/workspaces/x\n", e.stdout.String())
	})
}

// remoteClaudeEnv is a claudeEnv with environment linux-server, whose
// snapshot lists workspace api-bump, and a caller that answers with reply.
type remoteClaudeEnv struct {
	*claudeEnv
	caller *environmentCallerMock
}

const remoteCreated = `{"ok":true,"result":{"id":"fix-login-1","session":"s1","path":"/srv/ws/fix-login-1","parent":null,"repos":[],"warnings":[],"runner":"claude","model":null,"ws_runner":null},"error":null}`

func newRemoteClaudeEnv(t *testing.T, reply string, err error) *remoteClaudeEnv {
	t.Helper()
	e := &remoteClaudeEnv{claudeEnv: newClaudeEnv(t), caller: callerReturning(reply, err)}
	e.app.paths = config.NewPaths(t.TempDir())
	useCaller(e.app, e.caller)
	rec := addLinuxServer(t, e.app)
	writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
	return e
}

func (e *remoteClaudeEnv) create(t *testing.T) envproto.CreateArgs {
	t.Helper()
	require.Len(t, e.caller.CallEnvironmentCalls(), 1)
	var args envproto.CreateArgs
	require.NoError(t, json.Unmarshal(e.caller.CallEnvironmentCalls()[0].Req.Args, &args))
	return args
}

func TestApp_runClaude_environment(t *testing.T) {
	run := func(e *remoteClaudeEnv, opts claudeOptions) error {
		return e.app.runClaude(t.Context(), &opts)
	}
	t.Run("--env sends workspace.create with the typed args", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		require.NoError(t, run(e, claudeOptions{env: "linux-server", name: "fix", hidden: true, specs: []string{"buff-bear"}, claudeArgs: []string{"fix it", "--model", "opus"}}))
		call := e.caller.CallEnvironmentCalls()[0]
		assert.Equal(t, "linux-server", call.Name)
		assert.Equal(t, envproto.OpWorkspaceCreate, call.Req.Op)
		assert.True(t, envproto.ValidRequestID(call.Req.RequestID))
		assert.Equal(t, testNow().Add(10*time.Minute), call.Req.Deadline)
		assert.JSONEq(t, `{"kind":"claude","specs":["buff-bear"],"name":"fix","parent":null,"hidden":true,"offline":false,"prompt":"fix it","model":"opus","endpoint":null}`, string(call.Req.Args))
		assert.Empty(t, e.store.CreateCalls())
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("--json prints the agent's result with environment", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		e.app.global.json = true
		require.NoError(t, run(e, claudeOptions{env: "linux-server", claudeArgs: []string{"fix it"}}))
		var got map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, "linux-server", got["environment"])
		assert.Equal(t, "fix-login-1", got["id"])
		assert.Equal(t, "/srv/ws/fix-login-1", got["path"])
		assert.Equal(t, []any{}, got["warnings"])
	})
	t.Run("--json keeps the agent's warnings", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, `{"ok":true,"result":{"id":"fix-login-1","session":"s1","path":"/srv/ws/fix-login-1","warnings":["agent warning"]},"error":null}`, nil)
		e.app.global.json = true
		require.NoError(t, run(e, claudeOptions{env: "linux-server", claudeArgs: []string{"fix it"}}))
		var got struct {
			Warnings []string `json:"warnings"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, []string{"agent warning"}, got.Warnings)
	})
	t.Run("--json has an empty warnings list when there are none", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, `{"ok":true,"result":{"id":"fix-login-1","session":"s1","path":"/srv/ws/fix-login-1"},"error":null}`, nil)
		e.app.global.json = true
		require.NoError(t, run(e, claudeOptions{env: "linux-server", claudeArgs: []string{"fix it"}}))
		var got map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, []any{}, got["warnings"])
	})
	t.Run("without --json prints the line with the environment", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		require.NoError(t, run(e, claudeOptions{env: "linux-server", claudeArgs: []string{"fix it"}}))
		assert.Equal(t, "fix-login-1  s1  /srv/ws/fix-login-1  (on linux-server)\n", e.stdout.String())
	})
	t.Run("an agent error keeps its code and exit code", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, `{"ok":false,"result":null,"error":{"code":"repo_not_found","message":"no repo","details":null}}`, nil)
		err := run(e, claudeOptions{env: "linux-server", specs: []string{"nope"}})
		requireCode(t, codeRepoNotFound, err)
		assert.Equal(t, exitNotFound, asCLIError(err).exitCode())
	})
	t.Run("an unknown remote code is internal", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, `{"ok":false,"result":null,"error":{"code":"from_the_future","message":"no","details":null}}`, nil)
		err := run(e, claudeOptions{env: "linux-server"})
		requireCode(t, codeInternal, err)
		assert.Equal(t, "from_the_future", asCLIError(err).details["remote_code"])
	})
	t.Run("a null result is internal", func(t *testing.T) {
		for _, asJSON := range []bool{false, true} {
			e := newRemoteClaudeEnv(t, `{"ok":true,"result":null,"error":null}`, nil)
			e.app.global.json = asJSON
			requireCode(t, codeInternal, run(e, claudeOptions{env: "linux-server", claudeArgs: []string{"fix it"}}))
		}
	})
	t.Run("offline maps to environment_offline with details", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, "", &daemon.RemoteError{Code: "environment_offline", Message: "offline", Details: map[string]any{"environment": "linux-server"}})
		err := run(e, claudeOptions{env: "linux-server"})
		requireCode(t, codeEnvironmentOffline, err)
		assert.Equal(t, map[string]any{"environment": "linux-server"}, asCLIError(err).details)
	})
	t.Run("outdated maps to environment_outdated", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, "", &daemon.RemoteError{Code: "environment_outdated", Message: "old"})
		requireCode(t, codeEnvironmentOutdated, run(e, claudeOptions{env: "linux-server"}))
	})
	t.Run("--env with a local --parent is environment_mismatch and sends nothing", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		err := run(e, claudeOptions{env: "linux-server", parent: "fix-login-d8efb24f"})
		requireCode(t, codeEnvironmentMismatch, err)
		assert.Empty(t, e.caller.CallEnvironmentCalls())
	})
	t.Run("--parent found on one environment runs there without --env", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		require.NoError(t, run(e, claudeOptions{parent: "api-bump", claudeArgs: []string{"fix it"}}))
		assert.Equal(t, "linux-server", e.caller.CallEnvironmentCalls()[0].Name)
		require.NotNil(t, e.create(t).Parent)
		assert.Equal(t, "api-bump", *e.create(t).Parent)
	})
	t.Run("--parent found on two is usage", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		rec, _, _, err := e.app.envStore().Add(t.Context(), "mac-mini", "127.0.0.1:8884")
		require.NoError(t, err)
		writeRemoteSnapshot(t, e.app, &rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		requireUsageError(t, run(e, claudeOptions{parent: "api-bump"}))
		assert.Empty(t, e.caller.CallEnvironmentCalls())
	})
	t.Run("--env inside a workspace is usage, and creates nothing", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		requireUsageError(t, run(e, claudeOptions{env: "linux-server", claudeArgs: []string{"fix it"}}))
		assert.Empty(t, e.caller.CallEnvironmentCalls())
	})
	t.Run("--runner ws --env sends kind runner with the prompt", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		require.NoError(t, run(e, claudeOptions{env: "linux-server", runner: config.RunnerWS, endpoint: "box", claudeArgs: []string{"do it", "--model", "m1"}}))
		args := e.create(t)
		assert.Equal(t, envproto.KindRunner, args.Kind)
		require.NotNil(t, args.Prompt)
		assert.Equal(t, "do it", *args.Prompt)
		assert.Equal(t, "m1", *args.Model)
		assert.Equal(t, "box", *args.Endpoint)
	})
	t.Run("--runner ws without a prompt is usage and sends nothing", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		requireUsageError(t, run(e, claudeOptions{env: "linux-server", runner: config.RunnerWS}))
		assert.Empty(t, e.caller.CallEnvironmentCalls())
	})
	t.Run("arguments other than a prompt and --model after -- are usage", func(t *testing.T) {
		for _, args := range [][]string{{"fix it", "--permission-mode", "plan"}, {"fix it", "extra"}, {"--model"}} {
			e := newRemoteClaudeEnv(t, remoteCreated, nil)
			err := run(e, claudeOptions{env: "linux-server", claudeArgs: args})
			requireUsageError(t, err)
			require.ErrorContains(t, err, "only a prompt and --model can be passed to an agent on an environment")
			assert.Empty(t, e.caller.CallEnvironmentCalls())
		}
	})
	t.Run("--env local runs locally", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		require.NoError(t, run(e, claudeOptions{env: "local", claudeArgs: []string{"fix it"}}))
		assert.Empty(t, e.caller.CallEnvironmentCalls())
		assert.Len(t, e.store.CreateCalls(), 1)
	})
}

// assertNoWorkspace checks that no workspace, and no state, is left with
// an id made from a --name sanitized to base.
func assertNoWorkspace(t *testing.T, paths *config.Paths, base string) {
	t.Helper()
	for _, dir := range []string{paths.Workspaces, paths.State} {
		matches, err := filepath.Glob(filepath.Join(dir, base+"-*"))
		require.NoError(t, err)
		assert.Empty(t, matches)
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

// trustedDirs are the directories Claude Code's config trusts.
func (e *claudeE2E) trustedDirs(t *testing.T) []string {
	t.Helper()
	return trustedDirsIn(t, e.claudeJSON)
}

func TestApp_runClaude_scope(t *testing.T) {
	t.Run("refuses --env-scope inside a workspace", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		opts := &claudeOptions{envScope: []string{linuxRef.Name}}
		requireUsageError(t, e.app.runClaude(t.Context(), opts))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("refuses --env inside a workspace", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		opts := &claudeOptions{env: linuxRef.Name}
		requireUsageError(t, e.app.runClaude(t.Context(), opts))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("grants --env-scope outside a workspace", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{envScope: []string{linuxRef.Name}}))
		scope, file := scopeFileOf(t, e.app.paths.State, "x")
		assert.Equal(t, "granted", file.Source)
		assert.Equal(t, []string{"local", linuxRef.Name}, scope.Names())
	})
	t.Run("uses default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		e.app.config.Environments.DefaultScope = []string{linuxRef.Name}
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{}))
		scope, _ := scopeFileOf(t, e.app.paths.State, "x")
		assert.Equal(t, []string{"local", linuxRef.Name}, scope.Names())
	})
	t.Run("an --env-scope of local alone skips default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		e.app.config.Environments.DefaultScope = []string{linuxRef.Name}
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{envScope: []string{localEnvironment}}))
		require.Len(t, e.store.CreateCalls(), 1)
		_, err := os.Stat(envscope.Path(e.app.paths.State, "x"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("--parent none inside a workspace never reads default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		e.app.config.Environments.DefaultScope = []string{linuxRef.Name}
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{parent: requesterNone}))
		_, err := os.Stat(envscope.Path(e.app.paths.State, "x"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("--parent none inside a workspace inherits the enclosing scope, not default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		addEnvironmentForScope(t, e.app, winRef.Name)
		e.app.config.Environments.DefaultScope = []string{winRef.Name}
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		writeCallerScope(t, e.app.paths.State, linuxRef)
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{parent: requesterNone}))
		scope, file := scopeFileOf(t, e.app.paths.State, "x")
		assert.Equal(t, "inherited", file.Source)
		assert.Equal(t, []string{"local", linuxRef.Name}, scope.Names())
	})
	t.Run("--parent <other> inside a workspace takes the enclosing scope, not the parent's", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		e.store.FindFunc = func(arg string) (workspace.Workspace, error) { return workspace.Workspace{ID: arg}, nil }
		scope, err := envscope.New(localRef(), []envscope.Ref{linuxRef})
		require.NoError(t, err)
		require.NoError(t, envscope.Write(e.app.paths.State, "boss", envscope.FileFor(scope, "granted", nil, time.Now())))
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{parent: "boss"}))
		_, err = os.Stat(envscope.Path(e.app.paths.State, "x"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("unknown --env-scope name is environment_unknown and creates nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		err := e.app.runClaude(t.Context(), &claudeOptions{envScope: []string{"nope"}})
		requireCode(t, codeEnvironmentUnknown, err)
		assert.Empty(t, e.store.CreateCalls())
	})
}

func TestApp_remoteGrant(t *testing.T) {
	t.Run("its own environment alone grants nothing and skips default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		addEnvironmentForScope(t, e.app, winRef.Name)
		e.app.config.Environments.DefaultScope = []string{winRef.Name}
		granted, err := e.app.remoteGrant([]string{linuxRef.Name}, linuxRef.Name)
		require.NoError(t, err)
		assert.Empty(t, granted)
	})
	t.Run("names besides its own are granted", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		addEnvironmentForScope(t, e.app, winRef.Name)
		granted, err := e.app.remoteGrant([]string{linuxRef.Name, winRef.Name}, linuxRef.Name)
		require.NoError(t, err)
		assert.Equal(t, []string{winRef.Name}, granted)
	})
	t.Run("empty names use default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		addEnvironmentForScope(t, e.app, winRef.Name)
		e.app.config.Environments.DefaultScope = []string{winRef.Name}
		granted, err := e.app.remoteGrant(nil, linuxRef.Name)
		require.NoError(t, err)
		assert.Equal(t, []string{winRef.Name}, granted)
	})
	t.Run("inside a workspace nothing is granted", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		addEnvironmentForScope(t, e.app, winRef.Name)
		e.app.config.Environments.DefaultScope = []string{winRef.Name}
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		granted, err := e.app.remoteGrant([]string{linuxRef.Name, winRef.Name}, linuxRef.Name)
		require.NoError(t, err)
		assert.Empty(t, granted)
	})
}

// enrolledClaudeEnv is a claude env with linux and win-rig enrolled, and a
// scope host that answers every scope operation with a created workspace.
func enrolledClaudeEnv(t *testing.T) (*claudeEnv, *scopeHost) {
	t.Helper()
	e := newClaudeEnv(t)
	e.app.paths = config.NewPaths(t.TempDir())
	addEnvironmentForScope(t, e.app, linuxRef.Name)
	addEnvironmentForScope(t, e.app, winRef.Name)
	host := serveScopeHost(t, e.app, scopeOK(t, map[string]any{
		"id": "x", "session": "e8cdf5d8", "path": "/cfg/workspaces/x", "warnings": []string{},
	}))
	return e, host
}

// spawnNarrow is the Narrow of the one scope.spawn host was asked for.
func spawnNarrow(t *testing.T, host *scopeHost) []string {
	t.Helper()
	calls := host.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "spawn", calls[0].op)
	var args envproto.ScopeSpawnArgs
	require.NoError(t, json.Unmarshal(calls[0].args, &args))
	assert.Nil(t, args.Workspace)
	return args.Narrow
}

func TestApp_runRemoteClaude_scope(t *testing.T) {
	t.Run("--env A --env-scope B sends B alone, never the controller", func(t *testing.T) {
		e, host := enrolledClaudeEnv(t)
		opts := &claudeOptions{env: linuxRef.Name, envScope: []string{winRef.Name}}
		require.NoError(t, e.app.runRemoteClaude(t.Context(), opts, linuxRef.Name, ""))
		assert.Equal(t, []string{winRef.Name}, spawnNarrow(t, host))
	})
	t.Run("--env A with default_scope [B] sends B alone", func(t *testing.T) {
		e, host := enrolledClaudeEnv(t)
		e.app.config.Environments.DefaultScope = []string{winRef.Name}
		opts := &claudeOptions{env: linuxRef.Name}
		require.NoError(t, e.app.runRemoteClaude(t.Context(), opts, linuxRef.Name, ""))
		assert.Equal(t, []string{winRef.Name}, spawnNarrow(t, host))
	})
	t.Run("--env A --env-scope local sends local, as asked", func(t *testing.T) {
		e, host := enrolledClaudeEnv(t)
		opts := &claudeOptions{env: linuxRef.Name, envScope: []string{"local"}}
		require.NoError(t, e.app.runRemoteClaude(t.Context(), opts, linuxRef.Name, ""))
		assert.Equal(t, []string{"local"}, spawnNarrow(t, host))
	})
	t.Run("--env A --env-scope A is not wider than A", func(t *testing.T) {
		e, host := enrolledClaudeEnv(t)
		opts := &claudeOptions{env: linuxRef.Name, envScope: []string{linuxRef.Name}}
		_ = e.app.runRemoteClaude(t.Context(), opts, linuxRef.Name, "")
		assert.Empty(t, host.Calls())
	})
	t.Run("default_scope [A] with --env A is not wider than A", func(t *testing.T) {
		e, host := enrolledClaudeEnv(t)
		e.app.config.Environments.DefaultScope = []string{linuxRef.Name}
		_ = e.app.runRemoteClaude(t.Context(), &claudeOptions{env: linuxRef.Name}, linuxRef.Name, "")
		assert.Empty(t, host.Calls())
	})
	t.Run("the unit never reads default_scope for a caller inside a workspace", func(t *testing.T) {
		e, host := enrolledClaudeEnv(t)
		e.app.config.Environments.DefaultScope = []string{winRef.Name}
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		_ = e.app.runRemoteClaude(t.Context(), &claudeOptions{parent: "remote-1"}, linuxRef.Name, "remote-1")
		assert.Empty(t, host.Calls())
	})
}

func TestApp_runClaude_remoteParentInWorkspace(t *testing.T) {
	for _, runner := range []string{"", config.RunnerWS} {
		t.Run("runner "+runner+": a remote --parent is refused inside a workspace", func(t *testing.T) {
			e := newRemoteClaudeEnv(t, remoteCreated, nil)
			e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
			err := e.app.runClaude(t.Context(), &claudeOptions{parent: "api-bump", runner: runner, claudeArgs: []string{"fix it"}})
			requireUsageError(t, err)
			assert.Empty(t, e.caller.CallEnvironmentCalls())
			assert.Empty(t, e.store.CreateCalls())
			assert.Empty(t, e.claude.StartCalls())
		})
	}
	t.Run("outside a workspace a remote --parent still runs on its environment", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		require.NoError(t, e.app.runClaude(t.Context(), &claudeOptions{parent: "api-bump", claudeArgs: []string{"fix it"}}))
		assert.Len(t, e.caller.CallEnvironmentCalls(), 1)
	})
}
