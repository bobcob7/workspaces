package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/gate"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// fixedAwake is an awakeReader that always answers the same.
type fixedAwake bool

func (f fixedAwake) Awake(context.Context) (bool, error) {
	return bool(f), nil
}

// tickEnv is an App whose loops live in a temp dir. Its workspace store
// creates workspace loop-deps-1a2b3c4d, and its claude client starts
// session e8cdf5d8.
type tickEnv struct {
	*mockedApp
	loops  *loop.Store
	data   *loop.DataStore
	claude *claudeClientMock
}

func newTickEnv(t *testing.T) *tickEnv {
	t.Helper()
	e := &tickEnv{mockedApp: newMockedApp()}
	e.loops = withLoopStore(t, e.app)
	e.data = withLoopData(t, e.app)
	lockPath := filepath.Join(t.TempDir(), "lock")
	e.store.CreateFunc = func(context.Context, workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
		lock, ok, err := lockfile.TryExclusiveLock(lockPath)
		require.NoError(t, err)
		require.True(t, ok)
		return workspace.Workspace{ID: "loop-deps-1a2b3c4d", Path: "/cfg/workspaces/loop-deps-1a2b3c4d"}, lock, nil
	}
	e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return nil }
	e.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return nil }
	e.claude = &claudeClientMock{
		CheckFunc: func(context.Context) error { return nil },
		StartFunc: func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{ID: "e8cdf5d8"}, nil
		},
	}
	e.app.claude = e.claude
	e.app.awake = fixedAwake(true)
	e.app.loopPoll = time.Millisecond
	return e
}

// addDueLoop adds loop deps, due a minute ago, with the runs given.
func (e *tickEnv) addDueLoop(t *testing.T, runs ...loop.Run) loop.Loop {
	t.Helper()
	l := loop.Loop{
		Name:      "deps",
		Every:     "1h",
		Prompt:    "Do it",
		Keep:      5,
		CreatedAt: testNow().Add(-2 * time.Hour),
		NextRunAt: testNow().Add(-time.Minute),
		Runs:      runs,
	}
	require.NoError(t, e.loops.Create(&l))
	return l
}

// addGatedLoop adds loop deps, due a minute ago, with script "check", the
// prompt given, and vars.
func (e *tickEnv) addGatedLoop(t *testing.T, prompt string, vars map[string]string) loop.Loop {
	t.Helper()
	l := loop.Loop{
		Name:      "deps",
		Every:     "1h",
		Prompt:    prompt,
		Keep:      5,
		CreatedAt: testNow().Add(-2 * time.Hour),
		NextRunAt: testNow().Add(-time.Minute),
		Script:    "check",
		Vars:      vars,
	}
	require.NoError(t, e.loops.Create(&l))
	return l
}

func (e *tickEnv) loopRecord(t *testing.T) loop.Loop {
	t.Helper()
	l, err := e.loops.Get("deps")
	require.NoError(t, err)
	return l
}

func TestApp_loopTick(t *testing.T) {
	t.Run("starts a due loop and records its run", func(t *testing.T) {
		e := newTickEnv(t)
		l := e.addDueLoop(t)
		l.Model = "opus"
		require.NoError(t, e.loops.Save(&l))
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code, e.stderr.String())
		items := listedLoops(t, out)
		require.Len(t, items, 1)
		assert.Equal(t, "started", items[0]["action"])
		assert.Equal(t, "loop-deps-1a2b3c4d", items[0]["workspace"])
		assert.Equal(t, "e8cdf5d8", items[0]["session"])
		assert.Equal(t, formatTime(testNow().Add(59*time.Minute)), items[0]["next_run_at"])
		assert.Nil(t, items[0]["error"])
		require.Len(t, e.store.CreateCalls(), 1)
		assert.Equal(t, "loop-deps", e.store.CreateCalls()[0].Opts.Name)
		assert.Empty(t, e.store.CreateCalls()[0].Opts.Parent)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, []string{"--model", "opus", "Do it"}, e.claude.StartCalls()[0].Opts.Args)
		rec := e.loopRecord(t)
		require.Len(t, rec.Runs, 1)
		assert.Equal(t, "loop-deps-1a2b3c4d", rec.Runs[0].Workspace)
	})
	t.Run("waits for a loop that isn't due", func(t *testing.T) {
		e := newTickEnv(t)
		l := e.addDueLoop(t)
		l.NextRunAt = testNow().Add(time.Hour)
		require.NoError(t, e.loops.Save(&l))
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code)
		items := listedLoops(t, out)
		assert.Equal(t, "waiting", items[0]["action"])
		assert.Empty(t, e.lifetime.ListCalls())
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("reports a paused loop with no next run", func(t *testing.T) {
		e := newTickEnv(t)
		l := e.addDueLoop(t)
		l.Paused = true
		require.NoError(t, e.loops.Save(&l))
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code)
		items := listedLoops(t, out)
		assert.Equal(t, "paused", items[0]["action"])
		assert.Nil(t, items[0]["next_run_at"])
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("leaves a loop asleep until the Mac is awake", func(t *testing.T) {
		e := newTickEnv(t)
		before := e.addDueLoop(t)
		e.app.awake = fixedAwake(false)
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code)
		items := listedLoops(t, out)
		assert.Equal(t, "asleep", items[0]["action"])
		assert.Empty(t, e.claude.StartCalls())
		after := e.loopRecord(t)
		assert.True(t, before.NextRunAt.Equal(after.NextRunAt))
		assert.Empty(t, after.Runs)
	})
	t.Run("waits for a loop whose last run is still working", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t, loop.Run{StartedAt: testNow().Add(-time.Hour), Workspace: "loop-deps-old", Session: "s1"})
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
			return lifetime.Listing{Workspaces: []lifetime.Entry{{
				Workspace: workspace.Workspace{ID: "loop-deps-old"},
				Session:   &lifetime.Session{State: lifetime.StateWorking},
			}}}, nil
		}
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code)
		items := listedLoops(t, out)
		assert.Equal(t, "busy", items[0]["action"])
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("dry run reports what would start and changes nothing", func(t *testing.T) {
		e := newTickEnv(t)
		before := e.addDueLoop(t)
		code, out := runLoop(t, e.app, e.stdout, "tick", "--dry-run", "--json")
		require.Equal(t, 0, code)
		items := listedLoops(t, out)
		assert.Equal(t, "would_start", items[0]["action"])
		assert.Empty(t, e.claude.StartCalls())
		assert.Empty(t, e.store.CreateCalls())
		after := e.loopRecord(t)
		assert.True(t, before.NextRunAt.Equal(after.NextRunAt))
		assert.Empty(t, after.Runs)
	})
	t.Run("a run that can't start is recorded and failed", func(t *testing.T) {
		e := newTickEnv(t)
		before := e.addDueLoop(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{}, &claude.CommandError{ExitCode: 1, Stderr: "boom"}
		}
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		assert.Equal(t, 1, code)
		items := listedLoops(t, out)
		assert.Equal(t, "failed", items[0]["action"])
		require.NotNil(t, items[0]["error"])
		errJSON, ok := items[0]["error"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "claude_failed", errJSON["code"])
		rec := e.loopRecord(t)
		require.Len(t, rec.Runs, 1)
		assert.Equal(t, "claude_failed", rec.Runs[0].ErrorCode)
		assert.Empty(t, rec.Runs[0].Workspace)
		assert.True(t, rec.NextRunAt.After(before.NextRunAt))
	})
	t.Run("removes runs beyond keep, but not the new one", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t,
			loop.Run{StartedAt: testNow().Add(-2 * time.Hour), Workspace: "loop-deps-a", Session: "sa"},
			loop.Run{StartedAt: testNow().Add(-time.Hour), Workspace: "loop-deps-b", Session: "sb"},
		)
		l := e.loopRecord(t)
		l.Keep = 1
		require.NoError(t, e.loops.Save(&l))
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
			idle := func(id string) lifetime.Entry {
				return lifetime.Entry{Workspace: workspace.Workspace{ID: id}, Session: &lifetime.Session{State: lifetime.StateIdle}}
			}
			return lifetime.Listing{Workspaces: []lifetime.Entry{idle("loop-deps-a"), idle("loop-deps-b")}}, nil
		}
		e.lifetime.RemoveTreeFunc = func(context.Context, *workspace.Workspace, lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			return nil, nil
		}
		code, _ := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code, e.stderr.String())
		var removed []string
		for _, call := range e.lifetime.RemoveTreeCalls() {
			removed = append(removed, call.Ws.ID)
		}
		assert.ElementsMatch(t, []string{"loop-deps-a", "loop-deps-b"}, removed)
	})
	t.Run("two passes start one run", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		require.Equal(t, 0, runLoopCode(t, e.app, "tick"))
		require.Equal(t, 0, runLoopCode(t, e.app, "tick"))
		assert.Len(t, e.claude.StartCalls(), 1)
		assert.Len(t, e.loopRecord(t).Runs, 1)
	})
	t.Run("no loops prints an empty list", func(t *testing.T) {
		e := newTickEnv(t)
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code)
		assert.Equal(t, []any{}, out["loops"])
		assert.Equal(t, []any{}, out["warnings"])
	})
	t.Run("watch refuses --json", func(t *testing.T) {
		e := newTickEnv(t)
		var stderr bytes.Buffer
		e.app.stderr = &stderr
		code := runLoopCode(t, e.app, "tick", "--watch", "--json")
		assert.Equal(t, exitUsage, code)
		assert.Contains(t, stderr.String(), `"code":"usage"`)
	})
}

func TestApp_loopRun(t *testing.T) {
	t.Run("starts a paused loop and leaves it paused", func(t *testing.T) {
		e := newTickEnv(t)
		before := e.addDueLoop(t)
		l := e.loopRecord(t)
		l.Paused = true
		require.NoError(t, e.loops.Save(&l))
		code, out := runLoop(t, e.app, e.stdout, "run", "--json", "deps")
		require.Equal(t, 0, code, e.stderr.String())
		assert.Equal(t, "deps", out["name"])
		assert.Equal(t, "loop-deps-1a2b3c4d", out["workspace"])
		assert.Equal(t, "e8cdf5d8", out["session"])
		rec := e.loopRecord(t)
		assert.True(t, rec.Paused)
		assert.True(t, before.NextRunAt.Equal(rec.NextRunAt))
		require.Len(t, rec.Runs, 1)
		assert.Equal(t, "loop-deps-1a2b3c4d", rec.Runs[0].Workspace)
	})
	t.Run("unknown loop is not found", func(t *testing.T) {
		e := newTickEnv(t)
		assert.Equal(t, exitNotFound, runLoopCode(t, e.app, "run", "nope"))
	})
}

func TestApp_loopRunHidden(t *testing.T) {
	// createdWith starts loop deps with args, its runs visible if show, and
	// returns the workspace options the run was created with.
	createdWith := func(t *testing.T, show bool, args ...string) workspace.CreateOptions {
		t.Helper()
		e := newTickEnv(t)
		l := e.addDueLoop(t)
		hidden := !show
		l.Hidden = &hidden
		require.NoError(t, e.loops.Save(&l))
		var created workspace.CreateOptions
		create := e.store.CreateFunc
		e.store.CreateFunc = func(ctx context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			created = opts
			return create(ctx, opts)
		}
		code, _ := runLoop(t, e.app, e.stdout, args...)
		require.Equal(t, 0, code, e.stderr.String())
		return created
	}
	for _, cmd := range [][]string{{"run", "--json", "deps"}, {"tick", "--json"}} {
		t.Run(cmd[0]+" hides a default loop's run workspace", func(t *testing.T) {
			assert.True(t, createdWith(t, false, cmd...).Hidden)
		})
		t.Run(cmd[0]+" shows a --show loop's run workspace", func(t *testing.T) {
			assert.False(t, createdWith(t, true, cmd...).Hidden)
		})
	}
}

func TestLoopClaudeArgs(t *testing.T) {
	tests := []struct {
		name string
		loop loop.Loop
		want []string
	}{
		{name: "prompt only", loop: loop.Loop{Prompt: "p"}, want: []string{"p"}},
		{name: "model", loop: loop.Loop{Model: "opus", Prompt: "p"}, want: []string{"--model", "opus", "p"}},
		{name: "permission mode", loop: loop.Loop{PermissionMode: "acceptEdits", Prompt: "p"}, want: []string{"--permission-mode", "acceptEdits", "p"}},
		{name: "both", loop: loop.Loop{Model: "opus", PermissionMode: "acceptEdits", Prompt: "p"}, want: []string{"--model", "opus", "--permission-mode", "acceptEdits", "p"}},
	}
	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			assert.Equal(t, tests[i].want, loopClaudeArgs(&tests[i].loop, tests[i].loop.Prompt))
		})
	}
}

func TestApp_loopTickGate(t *testing.T) {
	t.Run("a passed script's output renders the prompt", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Fix {{.Stdout}} in {{.Vars.REPO}}", map[string]string{"REPO": "buff-bear"})
		g := &fakeGate{result: gate.Result{ExitCode: 0, Stdout: "#1 Bug"}}
		e.app.gate = g
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code, e.stderr.String())
		items := listedLoops(t, out)
		assert.Equal(t, "started", items[0]["action"])
		require.Len(t, g.requests, 1)
		req := g.requests[0]
		assert.Equal(t, "check", req.Script)
		assert.Equal(t, "/home/me", req.Dir)
		assert.Equal(t, time.Minute, req.Timeout)
		assert.Contains(t, req.Env, "WS_LOOP_VAR_REPO=buff-bear")
		require.Len(t, e.claude.StartCalls(), 1)
		args := e.claude.StartCalls()[0].Opts.Args
		assert.Equal(t, "Fix #1 Bug in buff-bear", args[len(args)-1])
		assert.Equal(t, map[string]any{"outcome": "passed", "exit_code": float64(0), "message": nil}, gateOf(t, items[0]), "gate")
		rec := e.loopRecord(t)
		require.NotNil(t, rec.LastGate)
		assert.Equal(t, loop.GatePassed, rec.LastGate.Outcome)
	})
	t.Run("a script that exits non-zero skips the run", func(t *testing.T) {
		e := newTickEnv(t)
		before := e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 1, Stderr: "no new issues\n"}}
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code, e.stderr.String())
		items := listedLoops(t, out)
		assert.Equal(t, "skipped", items[0]["action"])
		assert.Nil(t, items[0]["error"])
		assert.Empty(t, e.store.CreateCalls())
		assert.Empty(t, e.claude.StartCalls())
		rec := e.loopRecord(t)
		assert.Empty(t, rec.Runs)
		assert.True(t, rec.NextRunAt.Equal(before.NextRunAt.Add(time.Hour)))
		require.NotNil(t, rec.LastGate)
		assert.Equal(t, loop.GateSkipped, rec.LastGate.Outcome)
		assert.Equal(t, 1, rec.LastGate.ExitCode)
		assert.Equal(t, "no new issues", rec.LastGate.Message)
		assert.Equal(t, map[string]any{"outcome": "skipped", "exit_code": float64(1), "message": "no new issues"}, gateOf(t, items[0]), "gate")
	})
	t.Run("a script that times out fails the run", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{err: fmt.Errorf("%w after 1m0s", gate.ErrTimedOut)}
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		assert.Equal(t, exitFailed, code)
		items := listedLoops(t, out)
		assert.Equal(t, "failed", items[0]["action"])
		errJSON, ok := items[0]["error"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "loop_script_failed", errJSON["code"])
		gj := gateOf(t, items[0])
		assert.Nil(t, gj["exit_code"])
		assert.Equal(t, "timed_out", gj["outcome"])
		rec := e.loopRecord(t)
		require.Len(t, rec.Runs, 1)
		assert.Equal(t, "loop_script_failed", rec.Runs[0].ErrorCode)
		require.NotNil(t, rec.LastGate)
		assert.Equal(t, loop.GateTimedOut, rec.LastGate.Outcome)
		assert.Equal(t, -1, rec.LastGate.ExitCode)
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("a script that can't run fails the run", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{err: errors.New("no such file")}
		code, _ := runLoop(t, e.app, e.stdout, "tick", "--json")
		assert.Equal(t, exitFailed, code)
		rec := e.loopRecord(t)
		require.NotNil(t, rec.LastGate)
		assert.Equal(t, loop.GateFailed, rec.LastGate.Outcome)
	})
	t.Run("no script runs for a loop that isn't started", func(t *testing.T) {
		cases := map[string]func(t *testing.T, e *tickEnv){
			"paused": func(t *testing.T, e *tickEnv) {
				t.Helper()
				l := e.loopRecord(t)
				l.Paused = true
				require.NoError(t, e.loops.Save(&l))
			},
			"not due": func(t *testing.T, e *tickEnv) {
				t.Helper()
				l := e.loopRecord(t)
				l.NextRunAt = testNow().Add(time.Hour)
				require.NoError(t, e.loops.Save(&l))
			},
			"asleep": func(_ *testing.T, e *tickEnv) {
				e.app.awake = fixedAwake(false)
			},
			"busy": func(t *testing.T, e *tickEnv) {
				t.Helper()
				l := e.loopRecord(t)
				l.Runs = []loop.Run{{StartedAt: testNow().Add(-time.Hour), Workspace: "loop-deps-old", Session: "s1"}}
				require.NoError(t, e.loops.Save(&l))
				e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
					return lifetime.Listing{Workspaces: []lifetime.Entry{{
						Workspace: workspace.Workspace{ID: "loop-deps-old"},
						Session:   &lifetime.Session{State: lifetime.StateWorking},
					}}}, nil
				}
			},
			"check fails": func(_ *testing.T, e *tickEnv) {
				e.claude.CheckFunc = func(context.Context) error { return errors.New("no claude") }
			},
		}
		for name, setup := range cases {
			t.Run(name, func(t *testing.T) {
				e := newTickEnv(t)
				e.addGatedLoop(t, "Do it", nil)
				g := &fakeGate{}
				e.app.gate = g
				setup(t, e)
				runLoopCode(t, e.app, "tick", "--json")
				assert.Empty(t, g.requests)
			})
		}
		t.Run("dry run", func(t *testing.T) {
			e := newTickEnv(t)
			e.addGatedLoop(t, "Do it", nil)
			g := &fakeGate{}
			e.app.gate = g
			code, out := runLoop(t, e.app, e.stdout, "tick", "--dry-run", "--json")
			require.Equal(t, 0, code)
			assert.Equal(t, "would_start", listedLoops(t, out)[0]["action"])
			assert.Empty(t, g.requests)
		})
	})
	t.Run("a loop without a script has a null gate", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		g := &fakeGate{}
		e.app.gate = g
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code)
		items := listedLoops(t, out)
		assert.Nil(t, items[0]["gate"])
		assert.Empty(t, g.requests)
		assert.Nil(t, e.loopRecord(t).LastGate)
	})
	t.Run("a prompt that doesn't render fails the run after a passed script", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "{{if .Stdout}}{{.Vars.NOPE}}{{end}}x", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 0, Stdout: "y"}}
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		assert.Equal(t, exitUsage, code)
		items := listedLoops(t, out)
		assert.Equal(t, "failed", items[0]["action"])
		errJSON, ok := items[0]["error"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "loop_invalid", errJSON["code"])
		assert.Equal(t, "passed", gateOf(t, items[0])["outcome"])
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("a version 1 prompt is not a template", func(t *testing.T) {
		e := newTickEnv(t)
		l := e.addDueLoop(t)
		l.Prompt = "literal {{ .Stdout }}"
		l.Version = loop.Version1
		l.Hidden = nil
		require.NoError(t, e.loops.Save(&l))
		g := &fakeGate{}
		e.app.gate = g
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code, e.stderr.String())
		assert.Equal(t, "started", listedLoops(t, out)[0]["action"])
		assert.Empty(t, g.requests)
		require.Len(t, e.claude.StartCalls(), 1)
		args := e.claude.StartCalls()[0].Opts.Args
		assert.Equal(t, "literal {{ .Stdout }}", args[len(args)-1])
		assert.Equal(t, loop.Version1, e.loopRecord(t).Version)
	})
	t.Run("a truncated script output is a warning", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 0, StdoutTruncated: true}}
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, 0, code, e.stderr.String())
		assert.Contains(t, out["warnings"], "loop deps: script stdout over 64 KiB was cut")
	})
}

func TestApp_loopRunGate(t *testing.T) {
	t.Run("a skipped script starts nothing and exits refused", func(t *testing.T) {
		e := newTickEnv(t)
		before := e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 1}}
		code := runLoopCode(t, e.app, "run", "--json", "deps")
		assert.Equal(t, exitRefused, code)
		assert.Contains(t, e.stderr.String(), `"code":"loop_skipped"`)
		assert.Empty(t, e.claude.StartCalls())
		rec := e.loopRecord(t)
		assert.Empty(t, rec.Runs)
		require.NotNil(t, rec.LastGate)
		assert.Equal(t, loop.GateSkipped, rec.LastGate.Outcome)
		assert.True(t, before.NextRunAt.Equal(rec.NextRunAt))
	})
	t.Run("force skips the script and keeps last_gate", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Go{{.Stdout}}", nil)
		g := &fakeGate{result: gate.Result{ExitCode: 0, Stdout: "ignored"}}
		e.app.gate = g
		code := runLoopCode(t, e.app, "run", "--force", "--json", "deps")
		require.Equal(t, 0, code, e.stderr.String())
		assert.Empty(t, g.requests)
		require.Len(t, e.claude.StartCalls(), 1)
		args := e.claude.StartCalls()[0].Opts.Args
		assert.Equal(t, "Go", args[len(args)-1])
		assert.Nil(t, e.loopRecord(t).LastGate)
	})
}

// history is the deps loop's history on disk.
func (e *tickEnv) history(t *testing.T) loop.History {
	t.Helper()
	h, _, err := e.data.ReadHistory("deps")
	require.NoError(t, err)
	return h
}

// makeDue moves deps's next run back, so the next pass starts it again.
func (e *tickEnv) makeDue(t *testing.T) {
	t.Helper()
	l := e.loopRecord(t)
	l.NextRunAt = testNow().Add(-time.Minute)
	require.NoError(t, e.loops.Save(&l))
}

// idleListing lists workspace id with an idle agent.
func idleListing(id string) lifetime.Listing {
	return lifetime.Listing{Workspaces: []lifetime.Entry{{
		Workspace: workspace.Workspace{ID: id},
		Session:   &lifetime.Session{Kind: lifetime.KindAgent, ID: "old1", State: lifetime.StateIdle},
	}}}
}

// writeResult records a verdict and report for workspace id, as ws machine
// result does.
func writeResult(t *testing.T, e *tickEnv, id, verdict, report string) {
	t.Helper()
	res := &machine.Result{Verdict: verdict, RecordedAt: testNow()}
	require.NoError(t, machine.NewStore(e.data.StateDir()).WriteResult(id, id, res, []byte(report)))
}

func TestApp_loopTick_history(t *testing.T) {
	t.Run("a started run appends a running entry from tick", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		h := e.history(t)
		require.Len(t, h.Entries, 1)
		assert.Equal(t, loop.StatusRunning, h.Entries[0].Status)
		assert.Equal(t, loop.TriggerTick, h.Entries[0].Trigger)
		assert.Equal(t, "loop-deps-1a2b3c4d", h.Entries[0].Workspace)
		assert.Equal(t, "e8cdf5d8", h.Entries[0].Session)
	})
	t.Run("a skipped gate twice is one entry with two repeats", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 1, Stderr: "no outdated modules"}}
		runLoopCode(t, e.app, "tick", "--json")
		e.makeDue(t)
		runLoopCode(t, e.app, "tick", "--json")
		h := e.history(t)
		require.Len(t, h.Entries, 1)
		assert.Equal(t, loop.StatusSkipped, h.Entries[0].Status)
		assert.Equal(t, 2, h.Entries[0].Repeats)
		require.NotNil(t, h.Entries[0].Gate)
		assert.Equal(t, 1, h.Entries[0].Gate.ExitCode)
		assert.Equal(t, "no outdated modules", h.Entries[0].Gate.Message)
	})
	t.Run("a Claude check that fails records start_failed", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		e.claude.CheckFunc = func(context.Context) error { return claude.ErrNotInstalled }
		runLoopCode(t, e.app, "tick", "--json")
		h := e.history(t)
		require.Len(t, h.Entries, 1)
		assert.Equal(t, loop.StatusStartFailed, h.Entries[0].Status)
		assert.Equal(t, "claude_missing", h.Entries[0].ErrorCode)
		assert.NotNil(t, h.Entries[0].FinishedAt)
	})
	t.Run("a dry run writes no history", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--dry-run", "--json"), e.stderr.String())
		_, found, err := e.data.ReadHistory("deps")
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("a run starts with the loop's memory in its prompt", func(t *testing.T) {
		e := newTickEnv(t)
		l := e.addDueLoop(t)
		l.Prompt = `Seen {{memory "seen"}}`
		require.NoError(t, e.loops.Save(&l))
		_, err := e.data.SetMemory(t.Context(), "deps", "seen", "#412", "", loop.AnyRev, testNow())
		require.NoError(t, err)
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		require.Len(t, e.claude.StartCalls(), 1)
		args := e.claude.StartCalls()[0].Opts.Args
		assert.Equal(t, "Seen #412", args[len(args)-1])
	})
	t.Run("a started run's workspace records its loop", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		mark, found, err := e.data.ReadRunMark("loop-deps-1a2b3c4d")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "deps", mark.Loop)
		assert.True(t, testNow().Equal(mark.StartedAt))
	})
}

func TestApp_loopTick_settle(t *testing.T) {
	// pausedWithRun adds a paused deps whose one run is in workspace
	// loop-deps-old, so a pass settles that run and starts nothing.
	pausedWithRun := func(t *testing.T, e *tickEnv) {
		t.Helper()
		l := e.addDueLoop(t, loop.Run{StartedAt: testNow().Add(-2 * time.Hour), Workspace: "loop-deps-old", Session: "old1"})
		l.Paused = true
		require.NoError(t, e.loops.Save(&l))
	}
	t.Run("an idle run with no record needs attention", func(t *testing.T) {
		e := newTickEnv(t)
		pausedWithRun(t, e)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return idleListing("loop-deps-old"), nil }
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		h := e.history(t)
		require.Len(t, h.Entries, 1)
		assert.Equal(t, loop.StatusNeedsAttention, h.Entries[0].Status)
		assert.Nil(t, h.Entries[0].FinishedAt)
	})
	t.Run("a verdict succeeds the run and saves its report", func(t *testing.T) {
		e := newTickEnv(t)
		pausedWithRun(t, e)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return idleListing("loop-deps-old"), nil }
		writeResult(t, e, "loop-deps-old", "PASS", "# Bumped 3 modules\n")
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		h := e.history(t)
		require.Len(t, h.Entries, 1)
		entry := h.Entries[0]
		assert.Equal(t, loop.StatusSucceeded, entry.Status)
		require.NotNil(t, entry.Verdict)
		assert.Equal(t, "PASS", entry.Verdict.Verdict)
		assert.True(t, entry.Verdict.Report)
		report, found, err := e.data.ReadReport("deps", entry.Seq)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "# Bumped 3 modules\n", string(report))
	})
	t.Run("a run that left the listing is closed, and gone when it was running", func(t *testing.T) {
		e := newTickEnv(t)
		pausedWithRun(t, e)
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		h := e.history(t)
		require.Len(t, h.Entries, 1)
		assert.True(t, h.Entries[0].Closed)
		assert.Equal(t, loop.StatusGone, h.Entries[0].Status)
	})
}

func TestApp_loopTick_prune(t *testing.T) {
	t.Run("a verdict that lands before removal is saved first", func(t *testing.T) {
		e := newTickEnv(t)
		l := e.addDueLoop(t, loop.Run{StartedAt: testNow().Add(-2 * time.Hour), Workspace: "loop-deps-old", Session: "old1"})
		l.Keep = 1
		require.NoError(t, e.loops.Save(&l))
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return idleListing("loop-deps-old"), nil }
		// The verdict lands while the new run starts, after the pass's first settle.
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			writeResult(t, e, "loop-deps-old", "PASS", "# Kept\n")
			return claude.Started{ID: "e8cdf5d8"}, nil
		}
		var removed []string
		var reportKept []bool
		e.lifetime.RemoveTreeFunc = func(_ context.Context, ws *workspace.Workspace, _ lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			removed = append(removed, ws.ID)
			_, found, err := e.data.ReadReport("deps", 1)
			require.NoError(t, err)
			reportKept = append(reportKept, found)
			return nil, nil
		}
		require.Equal(t, 0, runLoopCode(t, e.app, "tick", "--json"), e.stderr.String())
		assert.Equal(t, []string{"loop-deps-old"}, removed)
		assert.Equal(t, []bool{true}, reportKept)
		h := e.history(t)
		require.Len(t, h.Entries, 2)
		old := h.Entries[0]
		assert.Equal(t, loop.StatusSucceeded, old.Status)
		assert.True(t, old.Closed)
		assert.True(t, old.Verdict.Report)
	})
}

// gateOf is a tick item's gate object, without its time.
func gateOf(t *testing.T, item map[string]any) map[string]any {
	t.Helper()
	g, ok := item["gate"].(map[string]any)
	require.True(t, ok, "gate is %v", item["gate"])
	assert.Equal(t, formatTime(testNow()), g["at"])
	delete(g, "at")
	return g
}
