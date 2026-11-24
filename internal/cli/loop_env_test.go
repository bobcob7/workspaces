package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
)

const loopReply = `{"name":"deps","title":"","every":"1h","prompt":"hi","specs":["buff-bear"],"model":null,"permission_mode":null,"keep":10,` +
	`"paused":false,"hidden":true,"runner":"claude","endpoint":null,"created_at":"2026-10-19T12:00:00Z","next_run_at":"2026-10-19T13:00:00Z",` +
	`"last_run":null,"script":null,"script_timeout":null,"vars":{},"last_gate":null,"warnings":[]}`

// loopEnvRun runs ws loop args on an app whose environment answers reply, and
// returns the exit code and the calls the environment got.
func loopEnvRun(t *testing.T, reply string, args ...string) (code int, e *mockedApp, caller *environmentCallerMock) {
	t.Helper()
	e, caller, _ = newEnvOpApp(t, okReply(reply))
	code = e.app.Run(t.Context(), append([]string{"loop"}, args...))
	return code, e, caller
}

// sentArgs is the operation and arguments of the first call, as a JSON object.
func sentArgs(t *testing.T, caller *environmentCallerMock) (op string, args map[string]any) {
	t.Helper()
	calls := caller.CallEnvironmentCalls()
	require.NotEmpty(t, calls)
	require.NoError(t, json.Unmarshal(calls[0].Req.Args, &args))
	return calls[0].Req.Op, args
}

// nullSpec is the LoopSpec of loop deps with every other field null or false, then set.
func nullSpec(set map[string]any) map[string]any {
	spec := map[string]any{
		"name": "deps", "title": nil, "every": nil, "prompt": nil, "specs": nil, "keep": nil, "runner": nil, "endpoint": nil,
		"model": nil, "permission_mode": nil, "paused": nil, "script": nil, "script_timeout": nil, "vars": nil, "hidden": nil,
		"confirm": false,
	}
	maps.Copy(spec, set)
	return spec
}

func TestApp_runLoopEnv(t *testing.T) {
	t.Run("add --env sends loop.add with the prompt", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "add", "deps", "--every", "1h", "--prompt", "hi", "buff-bear", "--env", "linux-server", "--json")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopAdd, op)
		assert.Equal(t, nullSpec(map[string]any{"every": "1h", "prompt": "hi", "specs": []any{"buff-bear"}}), args)
		var out map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.Equal(t, "linux-server", out["environment"])
		assert.Equal(t, "deps", out["name"])
	})
	t.Run("add --env sends the flags that were given", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "add", "deps", "--every", "90m", "--prompt", "hi", "--title", " Nightly ", "--keep", "3",
			"--runner", "ws", "--endpoint", "alt", "--model", "opus", "--paused", "--show", "--var", "A=1", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		_, args := sentArgs(t, caller)
		assert.Equal(t, nullSpec(map[string]any{
			"every": "1h30m", "prompt": "hi", "title": "Nightly", "keep": float64(3), "runner": "ws", "endpoint": "alt",
			"model": "opus", "paused": true, "hidden": false, "vars": map[string]any{"A": "1"},
		}), args)
		assert.Contains(t, e.stdout.String(), "deps")
	})
	t.Run("add --env checks the flags before sending", func(t *testing.T) {
		for name, args := range map[string][]string{
			"no every":                   {"add", "deps", "--prompt", "hi"},
			"no prompt":                  {"add", "deps", "--every", "1h"},
			"a bad every":                {"add", "deps", "--every", "1s", "--prompt", "hi"},
			"a bad spec":                 {"add", "deps", "--every", "1h", "--prompt", "hi", "not a spec"},
			"hidden + show":              {"add", "deps", "--every", "1h", "--prompt", "hi", "--hidden", "--show"},
			"a timeout without a script": {"add", "deps", "--every", "1h", "--prompt", "hi", "--script-timeout", "5s"},
		} {
			t.Run(name, func(t *testing.T) {
				code, _, caller := loopEnvRun(t, loopReply, append(args, "--env", "linux-server")...)
				assert.NotEqual(t, exitOK, code)
				assert.Empty(t, caller.CallEnvironmentCalls())
			})
		}
	})
	t.Run("add --env with a script needs --yes", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "add", "deps", "--every", "1h", "--prompt", "hi", "--script", "exit 0", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Contains(t, e.stderr.String(), "this runs code on linux-server as its agent's user; pass --yes")
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("add --env with a script and --yes sends confirm", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "add", "deps", "--every", "1h", "--prompt", "hi", "--script", "exit 0",
			"--script-timeout", "30s", "--yes", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		_, args := sentArgs(t, caller)
		assert.Equal(t, nullSpec(map[string]any{
			"every": "1h", "prompt": "hi", "script": "exit 0", "script_timeout": "30s", "confirm": true,
		}), args)
	})
	t.Run("add --env with bypassPermissions needs --yes", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, loopReply, "add", "deps", "--every", "1h", "--prompt", "hi", "--permission-mode", "bypassPermissions", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
		code, _, caller = loopEnvRun(t, loopReply, "add", "deps", "--every", "1h", "--prompt", "hi", "--permission-mode", "bypassPermissions", "--yes", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args := sentArgs(t, caller)
		assert.Equal(t, true, args["confirm"])
	})
	t.Run("add --env without a script sends no confirm even with --yes", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, loopReply, "add", "deps", "--every", "1h", "--prompt", "hi", "--permission-mode", "acceptEdits", "--yes", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args := sentArgs(t, caller)
		assert.Equal(t, false, args["confirm"])
	})
	t.Run("rm --env without --yes is refused locally", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "rm", "deps", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Contains(t, e.stderr.String(), "pass --yes")
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("rm --env --yes sends loop.rm with confirm", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "rm", "deps", "--yes", "--env", "linux-server", "--json")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopRm, op)
		assert.Equal(t, map[string]any{"name": "deps", "confirm": true}, args)
		assert.Contains(t, e.stdout.String(), `"environment":"linux-server"`)
	})
	t.Run("edit --env sends only the given fields", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "edit", "deps", "--every", "2h", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopEdit, op)
		assert.Equal(t, nullSpec(map[string]any{"every": "2h"}), args)
	})
	t.Run("edit --env maps clearing flags", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "edit", "deps", "--no-script", "--no-vars", "--no-specs", "--model", "", "--hidden=false", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		_, args := sentArgs(t, caller)
		assert.Equal(t, nullSpec(map[string]any{"script": "", "vars": map[string]any{}, "specs": []any{}, "model": "", "hidden": false}), args)
	})
	t.Run("edit --env needs a change", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, loopReply, "edit", "deps", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("edit --env with a script needs --yes", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, loopReply, "edit", "deps", "--script", "exit 0", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Contains(t, e.stderr.String(), "pass --yes")
		assert.Empty(t, caller.CallEnvironmentCalls())
		code, _, caller = loopEnvRun(t, loopReply, "edit", "deps", "--script", "exit 0", "--yes", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args := sentArgs(t, caller)
		assert.Equal(t, nullSpec(map[string]any{"script": "exit 0", "confirm": true}), args)
	})
	t.Run("edit --env removing a script needs no --yes", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, loopReply, "edit", "deps", "--no-script", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args := sentArgs(t, caller)
		assert.Equal(t, false, args["confirm"])
	})
	t.Run("pause and resume --env send their operations", func(t *testing.T) {
		for verb, want := range map[string]string{"pause": envproto.OpLoopPause, "resume": envproto.OpLoopResume} {
			code, _, caller := loopEnvRun(t, loopReply, verb, "deps", "--env", "linux-server")
			require.Equal(t, exitOK, code)
			op, args := sentArgs(t, caller)
			assert.Equal(t, want, op)
			assert.Equal(t, map[string]any{"name": "deps", "confirm": false}, args)
		}
	})
	t.Run("run --env sends loop.run with force", func(t *testing.T) {
		reply := `{"name":"deps","workspace":"deps-1","session":"abc","path":"/w/deps-1","warnings":[]}`
		code, e, caller := loopEnvRun(t, reply, "run", "--force", "deps", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopRun, op)
		assert.Equal(t, map[string]any{"name": "deps", "force": true}, args)
		assert.Equal(t, "deps  deps-1  abc  /w/deps-1\n", e.stdout.String())
		code, _, caller = loopEnvRun(t, reply, "run", "deps", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args = sentArgs(t, caller)
		assert.Equal(t, false, args["force"])
	})
	t.Run("history --env sends loop.history with the limit", func(t *testing.T) {
		reply := `{"loop":"deps","runs":[{"seq":2,"status":"done","trigger":"tick","started_at":"2026-10-19T12:00:00Z","finished_at":null,` +
			`"duration_seconds":60,"workspace":"deps-1","workspace_exists":true,"session":null,"error":null,"gate":null,"repeats":1,"verdict":null}],"total":1,"warnings":[]}`
		code, e, caller := loopEnvRun(t, reply, "history", "deps", "--limit", "5", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopHistory, op)
		assert.Equal(t, map[string]any{"name": "deps", "limit": float64(5)}, args)
		assert.Contains(t, e.stdout.String(), "deps-1")
		code, _, caller = loopEnvRun(t, reply, "history", "deps", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args = sentArgs(t, caller)
		assert.InDelta(t, 0, args["limit"], 0)
	})
	t.Run("history --env refuses a limit the environment would", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, `{}`, "history", "deps", "--limit", "101", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("history --run --env sends loop.history_run", func(t *testing.T) {
		reply := `{"loop":"deps","run":{"seq":2,"status":"done","trigger":"tick","started_at":"2026-10-19T12:00:00Z","finished_at":null,"duration_seconds":null,` +
			`"workspace":null,"workspace_exists":false,"session":null,"error":null,"gate":null,"repeats":1,"verdict":null},"report_text":"all good\n","warnings":[]}`
		code, e, caller := loopEnvRun(t, reply, "history", "deps", "--run", "2", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopHistoryRun, op)
		assert.Equal(t, map[string]any{"name": "deps", "seq": float64(2)}, args)
		assert.Contains(t, e.stdout.String(), "all good")
	})
	t.Run("history --clear --env needs --yes", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, `{}`, "history", "deps", "--clear", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
		code, e, caller := loopEnvRun(t, `{"loop":"deps","removed":3,"warnings":[]}`, "history", "deps", "--clear", "--yes", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopHistoryClear, op)
		assert.Equal(t, map[string]any{"name": "deps", "confirm": true}, args)
		assert.Equal(t, "3 runs removed\n", e.stdout.String())
	})
	t.Run("history --env keeps the local flag checks", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, `{}`, "history", "deps", "--clear", "--limit", "5", "--yes", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("memory clear --env needs --yes", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, `{}`, "memory", "clear", "deps", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
		code, e, caller := loopEnvRun(t, `{"loop":"deps","removed":2,"warnings":[]}`, "memory", "clear", "deps", "--yes", "--env", "linux-server")
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopMemoryClear, op)
		assert.Equal(t, map[string]any{"name": "deps", "confirm": true}, args)
		assert.Equal(t, "2 keys removed\n", e.stdout.String())
	})
	t.Run("memory set --env reads stdin", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(`{"loop":"deps","entry":{"key":"notes","value":"from stdin","rev":1,"updated_at":"2026-10-19T12:00:00Z","updated_by":null},"warnings":[]}`))
		e.app.stdin = strings.NewReader("from stdin")
		code := e.app.Run(t.Context(), []string{"loop", "memory", "set", "--file", "-", "deps", "notes", "--env", "linux-server"})
		require.Equal(t, exitOK, code, e.stderr.String())
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopMemorySet, op)
		assert.Equal(t, map[string]any{"name": "deps", "key": "notes", "value": "from stdin", "expect_rev": nil}, args)
		assert.Equal(t, "notes  rev 1\n", e.stdout.String())
	})
	t.Run("memory set --env sends a value argument and --expect-rev", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, `{"loop":"deps","entry":{"key":"k","value":"v","rev":4,"updated_at":"x","updated_by":null},"warnings":[]}`,
			"memory", "set", "--expect-rev", "3", "deps", "k", "v", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		_, args := sentArgs(t, caller)
		assert.Equal(t, map[string]any{"name": "deps", "key": "k", "value": "v", "expect_rev": float64(3)}, args)
	})
	t.Run("memory set --env needs a value", func(t *testing.T) {
		code, _, caller := loopEnvRun(t, `{}`, "memory", "set", "deps", "k", "--env", "linux-server")
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("memory list, get and rm --env send their operations", func(t *testing.T) {
		code, e, caller := loopEnvRun(t, `{"loop":"deps","entries":[{"key":"k","value":"v\nmore","rev":1,"updated_at":"t","updated_by":null}],"bytes":1,"warnings":[]}`,
			"memory", "list", "deps", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		op, args := sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopMemoryList, op)
		assert.Equal(t, map[string]any{"name": "deps", "confirm": false}, args)
		assert.Equal(t, "k  rev 1  t  v\n", e.stdout.String())
		code, e, caller = loopEnvRun(t, `{"loop":"deps","entry":{"key":"k","value":"v","rev":1,"updated_at":"t","updated_by":null},"warnings":[]}`,
			"memory", "get", "deps", "k", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		op, args = sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopMemoryGet, op)
		assert.Equal(t, map[string]any{"name": "deps", "key": "k", "expect_rev": nil}, args)
		assert.Equal(t, "v", e.stdout.String())
		code, e, caller = loopEnvRun(t, `{"loop":"deps","key":"k","removed":true,"warnings":[]}`,
			"memory", "rm", "--expect-rev", "0", "deps", "k", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		op, args = sentArgs(t, caller)
		assert.Equal(t, envproto.OpLoopMemoryRm, op)
		assert.Equal(t, map[string]any{"name": "deps", "key": "k", "expect_rev": float64(0)}, args)
		assert.Equal(t, "k removed\n", e.stdout.String())
	})
	t.Run("an agent's needs-confirm refusal says what to pass", func(t *testing.T) {
		refusal := `{"ok":false,"result":null,"error":{"code":"usage","message":"loop.add needs confirm","details":{"needs":"confirm"}}}`
		e, caller, _ := newEnvOpApp(t, refusal)
		code := e.app.Run(t.Context(), []string{"loop", "pause", "deps", "--env", "linux-server", "--json"})
		assert.Equal(t, exitUsage, code)
		assert.Contains(t, e.stderr.String(), "needs your confirmation on linux-server; pass --yes")
		assert.Contains(t, e.stderr.String(), `"needs":"confirm"`)
		assert.Len(t, caller.CallEnvironmentCalls(), 1)
		_, args := sentArgs(t, caller)
		assert.Equal(t, false, args["confirm"])
	})
	t.Run("another agent error keeps its code and the environment", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{"ok":false,"result":null,"error":{"code":"loop_not_found","message":"no loop deps"}}`)
		code := e.app.Run(t.Context(), []string{"loop", "pause", "deps", "--env", "linux-server"})
		assert.Equal(t, 3, code)
		assert.Contains(t, e.stderr.String(), "no loop deps")
	})
	t.Run("an unknown environment is environment_unknown", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(loopReply))
		code := e.app.Run(t.Context(), []string{"loop", "pause", "deps", "--env", "nowhere"})
		assert.NotEqual(t, exitOK, code)
		assert.Contains(t, e.stderr.String(), "nowhere")
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("--env local runs here", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		caller := callerReturning(okReply(loopReply), nil)
		useCaller(app, caller)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi", "--env", "local"))
		stdout.Reset()
		assert.Equal(t, exitOK, runLoopCode(t, app, "pause", "deps", "--env", "local"))
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("--yes locally does nothing", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		useCaller(app, callerReturning(`{}`, nil))
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi", "--yes"))
		stdout.Reset()
		require.Equal(t, exitOK, runLoopCode(t, app, "rm", "deps", "--yes"))
		_, err := store.Get("deps")
		require.Error(t, err)
	})
	t.Run("a remote text result prints its warnings on stderr", func(t *testing.T) {
		reply := strings.Replace(loopReply, `"warnings":[]`, `"warnings":["slow disk"]`, 1)
		code, e, _ := loopEnvRun(t, reply, "pause", "deps", "--env", "linux-server")
		require.Equal(t, exitOK, code)
		assert.Contains(t, e.stderr.String(), "ws: warning: slow disk")
		assert.Contains(t, e.stdout.String(), "deps")
	})
	t.Run("the commands have the flags", func(t *testing.T) {
		app, _, _ := newTestApp()
		root := app.newRootCmd()
		for _, path := range [][]string{
			{"loop", "add"},
			{"loop", "edit"},
			{"loop", "list"},
			{"loop", "pause"},
			{"loop", "resume"},
			{"loop", "rm"},
			{"loop", "run"},
			{"loop", "history"},
			{"loop", "memory", "list"},
			{"loop", "memory", "get"},
			{"loop", "memory", "set"},
			{"loop", "memory", "rm"},
			{"loop", "memory", "clear"},
		} {
			cmd, _, err := root.Find(path)
			require.NoError(t, err)
			assertFlag(t, cmd, "env", "")
		}
		for _, path := range [][]string{{"loop", "add"}, {"loop", "edit"}, {"loop", "rm"}, {"loop", "history"}, {"loop", "memory", "clear"}} {
			cmd, _, err := root.Find(path)
			require.NoError(t, err)
			assertFlag(t, cmd, "yes", "false")
		}
		tick, _, err := root.Find([]string{"loop", "tick"})
		require.NoError(t, err)
		assert.Nil(t, tick.Flags().Lookup("env"))
		list, _, err := root.Find([]string{"loop", "list"})
		require.NoError(t, err)
		assertFlag(t, list, "cached", "false")
	})
}

// loopsSnapshot stores rec's snapshot, received at receivedAt: protocol and
// the loops array (empty means the snapshot has no loops key).
func loopsSnapshot(t *testing.T, app *App, rec *environment.Record, receivedAt time.Time, protocol int, loops string) {
	t.Helper()
	at := receivedAt.Format(time.RFC3339)
	loopsKey := ""
	if loops != "" {
		loopsKey = `,"loops":` + loops
	}
	data := fmt.Sprintf(`{"version":1,"id":%q,"name":%q,"received_at":%q,"snapshot":{"at":%q,"protocol":%d,"workspaces":[],"agents":[]%s}}`,
		rec.ID, rec.Name, at, at, protocol, loopsKey)
	require.NoError(t, app.envStore().WriteSnapshot(rec.Name, []byte(data)))
}

const snapshotLoops = `[{"name":"deps","title":"Nightly","every":"24h","paused":false,"hidden":true,"runner":"claude","next_run_at":"2026-10-20T00:00:00Z",` +
	`"last_run":{"started_at":"2026-10-19T00:00:00Z","workspace":"deps-1","session":"s","error":null},"last_gate":null},` +
	`{"name":"sweep","title":"","every":"1h","paused":true,"hidden":false,"runner":"ws","next_run_at":null,"last_run":null,"last_gate":null}]`

func TestApp_cachedLoops(t *testing.T) {
	online := map[string]string{"linux-server": envOnline}
	t.Run("reads the snapshot's loops", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		loopsSnapshot(t, app, rec, testNow(), 3, snapshotLoops)
		got, err := app.cachedLoops("linux-server", online)
		require.NoError(t, err)
		assert.Equal(t, "linux-server", got.Environment)
		assert.Equal(t, "online", got.EnvironmentStatus)
		assert.False(t, got.LoopsUnknown)
		require.Len(t, got.Loops, 2)
		var first, second map[string]any
		require.NoError(t, json.Unmarshal(got.Loops[0], &first))
		require.NoError(t, json.Unmarshal(got.Loops[1], &second))
		assert.Equal(t, "deps", first["name"])
		assert.Equal(t, "Nightly", first["title"])
		assert.Equal(t, "24h", first["every"])
		assert.Equal(t, true, first["hidden"])
		assert.Equal(t, "2026-10-20T00:00:00Z", first["next_run_at"])
		assert.JSONEq(t, `{"started_at":"2026-10-19T00:00:00Z","workspace":"deps-1","session":"s","error":null}`, string(mustJSON(t, first["last_run"])))
		assert.Equal(t, "linux-server", first["environment"])
		assert.Equal(t, false, first["stale"])
		assert.Equal(t, "sweep", second["name"])
		assert.Equal(t, true, second["paused"])
		assert.Equal(t, "ws", second["runner"])
	})
	t.Run("an offline environment is stale", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		loopsSnapshot(t, app, rec, testNow(), 3, snapshotLoops)
		got, err := app.cachedLoops("linux-server", map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "offline", got.EnvironmentStatus)
		for _, raw := range got.Loops {
			var l map[string]any
			require.NoError(t, json.Unmarshal(raw, &l))
			assert.Equal(t, true, l["stale"])
		}
	})
	t.Run("an online environment with an old snapshot is stale", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		loopsSnapshot(t, app, rec, testNow().Add(-2*time.Minute), 3, snapshotLoops)
		got, err := app.cachedLoops("linux-server", online)
		require.NoError(t, err)
		assert.Equal(t, "online", got.EnvironmentStatus)
		var l map[string]any
		require.NoError(t, json.Unmarshal(got.Loops[0], &l))
		assert.Equal(t, true, l["stale"])
	})
	t.Run("a protocol-2 snapshot is loops_unknown", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		loopsSnapshot(t, app, rec, testNow(), 2, "")
		got, err := app.cachedLoops("linux-server", online)
		require.NoError(t, err)
		assert.True(t, got.LoopsUnknown)
		assert.Equal(t, []json.RawMessage{}, got.Loops)
	})
	t.Run("an empty loops list is known", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		loopsSnapshot(t, app, rec, testNow(), 3, "[]")
		got, err := app.cachedLoops("linux-server", online)
		require.NoError(t, err)
		assert.False(t, got.LoopsUnknown)
		assert.Empty(t, got.Loops)
	})
	t.Run("no snapshot is loops_unknown", func(t *testing.T) {
		app := newRemoteApp(t)
		addLinuxServer(t, app)
		got, err := app.cachedLoops("linux-server", online)
		require.NoError(t, err)
		assert.True(t, got.LoopsUnknown)
	})
	t.Run("summary loops have null prompts", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		loopsSnapshot(t, app, rec, testNow(), 3, snapshotLoops)
		got, err := app.cachedLoops("linux-server", online)
		require.NoError(t, err)
		var l map[string]any
		require.NoError(t, json.Unmarshal(got.Loops[0], &l))
		for _, key := range []string{"prompt", "script", "script_timeout", "vars", "specs", "model", "permission_mode", "endpoint"} {
			value, present := l[key]
			assert.True(t, present, key)
			assert.Nil(t, value, key)
		}
		assert.Equal(t, true, l["summary"])
	})
}

// mustJSON is v as JSON.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	return body
}

// pagedLoopCaller answers loop.list with the pages in order.
func pagedLoopCaller(pages ...string) *environmentCallerMock {
	next := 0
	return &environmentCallerMock{CallEnvironmentFunc: func(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
		page := pages[min(next, len(pages)-1)]
		next++
		return daemon.CallReply{Reply: json.RawMessage(okReply(page))}, nil
	}}
}

func loopPageJSON(names []string, next any, redacted int) string {
	loops := make([]string, 0, len(names))
	for _, n := range names {
		loops = append(loops, fmt.Sprintf(`{"name":%q,"title":"","every":"1h","prompt":"p","specs":[],"model":null,"permission_mode":null,"keep":10,"paused":false,"hidden":true,`+
			`"runner":"claude","endpoint":null,"created_at":"x","next_run_at":null,"last_run":null,"script":null,"script_timeout":null,"vars":{},"last_gate":null}`, n))
	}
	nextJSON, err := json.Marshal(next)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"loops":[%s],"offset":0,"total":3,"next_offset":%s,"redacted":%d,"warnings":["w"]}`, strings.Join(loops, ","), nextJSON, redacted)
}

func onlinePresence(app *App) {
	app.presence = func(context.Context) map[string]string { return map[string]string{"linux-server": envOnline} }
}

func TestApp_runLoopList(t *testing.T) {
	t.Run("--env online pages through loop.list", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		onlinePresence(e.app)
		caller := pagedLoopCaller(loopPageJSON([]string{"a", "b"}, 2, 1), loopPageJSON([]string{"c"}, nil, 2))
		useCaller(e.app, caller)
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--json"})
		require.Equal(t, exitOK, code, e.stderr.String())
		calls := caller.CallEnvironmentCalls()
		require.Len(t, calls, 2)
		assert.Equal(t, envproto.OpLoopList, calls[0].Req.Op)
		assert.JSONEq(t, `{"offset":0}`, string(calls[0].Req.Args))
		assert.JSONEq(t, `{"offset":2}`, string(calls[1].Req.Args))
		var out struct {
			Loops       []map[string]any `json:"loops"`
			Environment string           `json:"environment"`
			Redacted    int              `json:"redacted"`
			Warnings    []string         `json:"warnings"`
			NextOffset  any              `json:"next_offset"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		require.Len(t, out.Loops, 3)
		for i, name := range []string{"a", "b", "c"} {
			assert.Equal(t, name, out.Loops[i]["name"])
			assert.Equal(t, "linux-server", out.Loops[i]["environment"])
			assert.Equal(t, false, out.Loops[i]["stale"])
			assert.Equal(t, "p", out.Loops[i]["prompt"])
		}
		assert.Equal(t, "linux-server", out.Environment)
		assert.Equal(t, 3, out.Redacted)
		assert.Equal(t, []string{"w"}, out.Warnings)
		assert.Nil(t, out.NextOffset)
	})
	t.Run("--env online prints a line per loop", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		onlinePresence(e.app)
		useCaller(e.app, pagedLoopCaller(loopPageJSON([]string{"a", "b"}, nil, 0)))
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server"})
		require.Equal(t, exitOK, code, e.stderr.String())
		lines := strings.Split(strings.TrimSpace(e.stdout.String()), "\n")
		require.Len(t, lines, 2)
		assert.True(t, strings.HasPrefix(lines[0], "a "))
		assert.Contains(t, e.stderr.String(), "ws: warning: w")
	})
	t.Run("a page that doesn't advance is an error", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		onlinePresence(e.app)
		caller := pagedLoopCaller(loopPageJSON([]string{"a"}, 0, 0))
		useCaller(e.app, caller)
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--json"})
		assert.NotEqual(t, exitOK, code)
		assert.Len(t, caller.CallEnvironmentCalls(), 1)
		assert.Contains(t, e.stderr.String(), "didn't advance")
	})
	t.Run("endless pages stop at the cap", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		onlinePresence(e.app)
		n := 0
		caller := &environmentCallerMock{CallEnvironmentFunc: func(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
			n++
			return daemon.CallReply{Reply: json.RawMessage(okReply(loopPageJSON(nil, n, 0)))}, nil
		}}
		useCaller(e.app, caller)
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--json"})
		assert.NotEqual(t, exitOK, code)
		assert.Len(t, caller.CallEnvironmentCalls(), maxRemoteLoopPages)
	})
	t.Run("--cached reads the snapshot and asks nobody", func(t *testing.T) {
		e, caller, rec := newEnvOpApp(t, `{}`)
		onlinePresence(e.app)
		loopsSnapshot(t, e.app, rec, testNow(), 3, snapshotLoops)
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--cached", "--json"})
		require.Equal(t, exitOK, code, e.stderr.String())
		assert.Empty(t, caller.CallEnvironmentCalls())
		var out loopListEnvJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.Len(t, out.Loops, 2)
		assert.Equal(t, "online", out.EnvironmentStatus)
		assert.False(t, out.LoopsUnknown)
		assert.Equal(t, []string{}, out.Warnings)
	})
	t.Run("an offline environment falls back to the snapshot", func(t *testing.T) {
		e, caller, rec := newEnvOpApp(t, `{}`)
		loopsSnapshot(t, e.app, rec, testNow(), 3, snapshotLoops)
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--json"})
		require.Equal(t, exitOK, code, e.stderr.String())
		assert.Empty(t, caller.CallEnvironmentCalls())
		var out loopListEnvJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.Equal(t, "offline", out.EnvironmentStatus)
		require.Len(t, out.Loops, 2)
		assert.Contains(t, string(out.Loops[0]), `"stale":true`)
	})
	t.Run("a protocol-2 snapshot says the loops are unknown", func(t *testing.T) {
		e, _, rec := newEnvOpApp(t, `{}`)
		loopsSnapshot(t, e.app, rec, testNow(), 2, "")
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--cached", "--json"})
		require.Equal(t, exitOK, code)
		assert.Contains(t, e.stdout.String(), `"loops_unknown":true`)
		e.stdout.Reset()
		e.stderr.Reset()
		code = e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server", "--cached"})
		require.Equal(t, exitOK, code)
		assert.Contains(t, e.stderr.String(), "hasn't reported its loops")
	})
	t.Run("a cached text list marks stale loops and says it is offline", func(t *testing.T) {
		e, _, rec := newEnvOpApp(t, `{}`)
		loopsSnapshot(t, e.app, rec, testNow(), 3, snapshotLoops)
		code := e.app.Run(t.Context(), []string{"loop", "list", "--env", "linux-server"})
		require.Equal(t, exitOK, code, e.stderr.String())
		assert.Contains(t, e.stdout.String(), "Nightly (deps)")
		assert.Contains(t, e.stdout.String(), "(stale)")
		assert.Contains(t, e.stderr.String(), "linux-server is offline")
	})
	t.Run("--cached without --env is a usage error", func(t *testing.T) {
		app, _, _ := newTestApp()
		withLoopStore(t, app)
		assert.Equal(t, exitUsage, runLoopCode(t, app, "list", "--cached"))
	})
	t.Run("without --env only this machine's loops are listed", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		caller := callerReturning(`{}`, nil)
		useCaller(app, caller)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		code, out := runLoop(t, app, stdout, "list", "--json")
		require.Equal(t, exitOK, code)
		assert.Len(t, listedLoops(t, out), 1)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
}
