package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
)

// remoteHelperRole is the role of the remote child helper in these tests.
const remoteHelperRole = "implementer"

// childHost is an environment host that answers each scope operation with
// answer, given the operation's name and its raw arguments.
type childHost struct {
	daemon.NopEnvironments
	answer func(op string, args json.RawMessage) envproto.Reply
	mu     sync.Mutex
	calls  []scopeCall
}

// Scope records the call and answers it.
func (h *childHost) Scope(_ context.Context, caller, op string, args json.RawMessage) envproto.Reply {
	h.mu.Lock()
	h.calls = append(h.calls, scopeCall{caller: caller, op: op, args: args})
	h.mu.Unlock()
	return h.answer(op, args)
}

// Calls is the scope operations the host was asked for, oldest first.
func (h *childHost) Calls() []scopeCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]scopeCall(nil), h.calls...)
}

// childOpsOf is the operation names of the calls to scope.child, in order.
func childOpsOf(t *testing.T, calls []scopeCall) []string {
	t.Helper()
	var ops []string
	for _, c := range calls {
		if c.op != "child" {
			continue
		}
		var args envproto.ScopeChildArgs
		require.NoError(t, json.Unmarshal(c.args, &args))
		ops = append(ops, args.Op)
	}
	return ops
}

// remoteHelperChild is the ledger entry of helper, on linux, started by plan.
func remoteHelperChild(t *testing.T) envproto.ScopeChild {
	t.Helper()
	role := remoteHelperRole
	return envproto.ScopeChild{
		Environment: linuxRef.Name,
		Workspace:   "helper",
		Parent:      "plan",
		Role:        &role,
		Entry:       json.RawMessage(`{"id":"helper","path":"/remote/helper","hidden":false,"parent":"plan","repos":[],"session":null}`),
	}
}

// remoteHelperRecord is helper's machine record on linux: PASS with a report.
func remoteHelperRecord(t *testing.T) *envproto.MachineRecordResult {
	t.Helper()
	role := remoteHelperRole
	report := "the helper's report"
	return &envproto.MachineRecordResult{
		Workspace: "helper",
		Role:      &role,
		Result:    &envproto.RecordResult{Verdict: "PASS", Outcome: "pass", RecordedAt: testNow()},
		Report:    &report,
		Progress:  json.RawMessage("null"),
		Rollup:    json.RawMessage("null"),
	}
}

// serveChildHost serves the scope operations of a remote helper child on
// linux, with answer for the scope.child operations.
func serveChildHost(t *testing.T, e *mockedApp, children []envproto.ScopeChild, answer func(childOp string, args envproto.ScopeChildArgs) envproto.Reply) *childHost {
	t.Helper()
	host := &childHost{answer: func(op string, raw json.RawMessage) envproto.Reply {
		switch op {
		case "children":
			return scopeOK(t, envproto.ScopeChildrenResult{Children: children})
		case "child":
			var args envproto.ScopeChildArgs
			require.NoError(t, json.Unmarshal(raw, &args))
			return answer(args.Op, args)
		}
		return envproto.Reply{Error: &envproto.ReplyError{Code: "usage", Message: "unexpected " + op}}
	}}
	serveDaemonWith(t, e.app, host)
	return host
}

// removeHelper is ws machine rm of the remote child helper.
func removeHelper() *machineRmOptions {
	opts := &machineRmOptions{}
	opts.workspaces = []string{"helper"}
	return opts
}

// helperAnswer answers helper's record and status and its logs and removal
// from the given record and logs.
func helperAnswer(t *testing.T, record *envproto.MachineRecordResult) func(string, envproto.ScopeChildArgs) envproto.Reply {
	t.Helper()
	return func(childOp string, _ envproto.ScopeChildArgs) envproto.Reply {
		switch childOp {
		case "record":
			return scopeOK(t, record)
		case "logs":
			return scopeOK(t, envproto.AgentLogsResult{Workspace: "helper", Session: "s-helper", Lines: []string{"one", "two"}})
		case "rm":
			return scopeOK(t, map[string]any{"workspace": "helper", "removed": true})
		}
		return envproto.Reply{Error: &envproto.ReplyError{Code: "usage", Message: "unexpected " + childOp}}
	}
}

func TestApp_remoteChildren(t *testing.T) {
	t.Run("a local scope with no ledger makes no daemon call", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.environ = append(e.app.environ, "WS_DAEMON_AUTOSTART=0")
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		assert.NotContains(t, e.stdout.String(), "environment")
		assert.Empty(t, e.app.warned)
	})
	t.Run("a local scope with a ledger present asks the controller", func(t *testing.T) {
		e := newMachineApp(t)
		require.NoError(t, os.MkdirAll(e.app.paths.Environments, 0o700))
		require.NoError(t, envscope.NewLedger(e.app.paths.Environments).Put(t.Context(), &envscope.Entry{
			Environment: linuxRef.ID, Workspace: "helper", Scope: []string{linuxRef.ID}, CreatedAt: testNow(),
		}))
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		require.NotEmpty(t, host.Calls())
		assert.Equal(t, "children", host.Calls()[0].op)
	})
	t.Run("a remote child is listed with its environment", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		require.NotEmpty(t, host.Calls())
		assert.Equal(t, "children", host.Calls()[0].op)
		assert.Contains(t, e.stdout.String(), `"environment":"linux"`)
		assert.Contains(t, e.stdout.String(), `"id":"helper"`)
	})
	t.Run("an offline environment is a warning and local children still print", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveDaemonWith(t, e.app, &childHost{answer: func(string, json.RawMessage) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeEnvironmentOffline), Message: "linux is offline"}}
		}})
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		assert.Contains(t, e.stdout.String(), `"id":"a"`)
		assert.NotEmpty(t, e.app.warned)
	})
}

func TestRemoteChildrenOf(t *testing.T) {
	scope, err := envscope.New(localRef(), []envscope.Ref{linuxRef})
	require.NoError(t, err)
	raw, err := json.Marshal(envproto.ScopeChildrenResult{Children: []envproto.ScopeChild{
		remoteHelperChild(t),
		{Environment: "elsewhere", Workspace: "stray", Parent: "plan", Entry: json.RawMessage(`{"id":"stray"}`)},
	}})
	require.NoError(t, err)
	children, warnings := remoteChildrenOf(raw, scope)
	require.Len(t, children, 1)
	assert.Equal(t, "helper", children[0].id)
	assert.Equal(t, linuxRef.ID, children[0].envID)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "stray")
	assert.Contains(t, warnings[0], "isn't in your scope")
}

func TestApp_runMachineWait_remoteChild(t *testing.T) {
	t.Run("wait items include remote descendants in a local child's rollup", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		writePassResult(t, e, "a")
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle})
		child := remoteHelperChild(t)
		child.Parent = "a"
		child.Rollup = &envproto.ScopeRollup{Total: 2, Done: 1}
		serveChildHost(t, e, []envproto.ScopeChild{child}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		require.NotNil(t, got.Children[0].Rollup)
		assert.Equal(t, 3, got.Children[0].Rollup.Total)
		assert.Equal(t, 2, got.Children[0].Rollup.Done)
	})
	t.Run("the printed rollup comes from a second listing of the remote children", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		writePassResult(t, e, "a")
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle})
		first := remoteHelperChild(t)
		first.Parent = "a"
		first.Rollup = &envproto.ScopeRollup{Total: 2, Done: 1}
		second := first
		second.Rollup = &envproto.ScopeRollup{Total: 4, Done: 3}
		listings := 0
		host := &childHost{}
		host.answer = func(op string, raw json.RawMessage) envproto.Reply {
			switch op {
			case "children":
				listings++
				if listings == 1 {
					return scopeOK(t, envproto.ScopeChildrenResult{Children: []envproto.ScopeChild{first}})
				}
				return scopeOK(t, envproto.ScopeChildrenResult{Children: []envproto.ScopeChild{second}})
			case "child":
				var args envproto.ScopeChildArgs
				require.NoError(t, json.Unmarshal(raw, &args))
				return helperAnswer(t, remoteHelperRecord(t))(args.Op, args)
			}
			return envproto.Reply{Error: &envproto.ReplyError{Code: "usage", Message: "unexpected " + op}}
		}
		serveDaemonWith(t, e.app, host)
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		require.NotNil(t, got.Children[0].Rollup)
		assert.Equal(t, 5, got.Children[0].Rollup.Total)
		assert.Equal(t, 4, got.Children[0].Rollup.Done)
	})
	t.Run("a finished remote child is copied back and its report kept", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		item := got.Children[0]
		assert.True(t, item.Finished)
		assert.Equal(t, "pass", item.Outcome)
		require.NotNil(t, item.Result)
		require.NotNil(t, item.Result.Report)
		copied := filepath.Join(e.app.paths.State, "boss", "machine", "remote", linuxRef.ID, "helper", "report.md")
		assert.Equal(t, copied, *item.Result.Report)
		report, err := os.ReadFile(copied)
		require.NoError(t, err)
		assert.Equal(t, "the helper's report", string(report))
	})
	t.Run("a finished remote child's progress and rollup are its environment's", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		record := remoteHelperRecord(t)
		record.Progress = json.RawMessage(`{"name":"helper steps","total":3,"done":2,"note":null,"updated_at":null}`)
		record.Rollup = json.RawMessage(`{"total":3,"done":2,"fraction":0.6666666666666666}`)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, record))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		item := got.Children[0]
		require.NotNil(t, item.Progress)
		assert.Equal(t, 3, item.Progress.Total)
		assert.Equal(t, 2, item.Progress.Done)
		require.NotNil(t, item.Rollup)
		assert.Equal(t, 3, item.Rollup.Total)
		assert.Equal(t, 2, item.Rollup.Done)
	})
	t.Run("an unreachable environment with a copied result finishes", func(t *testing.T) {
		e := newWaitApp(t)
		e.app.remotePoll = time.Millisecond
		writeCallerScope(t, e.app.paths.State, linuxRef)
		writeCopy(t, e, []string{"plan", "boss"})
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeEnvironmentOffline), Message: "linux is offline"}}
		})
		start := time.Now()
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(5*time.Second)))
		assert.Less(t, time.Since(start), 3*time.Second)
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		assert.True(t, got.Children[0].Finished)
		assert.Equal(t, "pass", got.Children[0].Outcome)
	})
	t.Run("after the child is removed its copy is still read", func(t *testing.T) {
		e := newWaitApp(t)
		writeCopy(t, e, []string{"plan", "boss"})
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		assert.Equal(t, "pass", got.Children[0].Outcome)
		assert.Equal(t, linuxRef.ID, got.Children[0].Environment)
	})
	t.Run("the environment is the name for a live remote child and local for a local one", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		writePassResult(t, e, "a")
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle})
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper", "a"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 2)
		assert.Equal(t, "linux", got.Children[0].Environment)
		assert.Equal(t, "local", got.Children[1].Environment)
	})
	t.Run("a truncated report is a warning", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		record := remoteHelperRecord(t)
		record.ReportTruncated = true
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, record))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		assert.Contains(t, strings.Join(parseWait(t, e.stdout.String()).Warnings, "\n"), "cut down")
	})
	t.Run("a copy whose ancestors lack the caller is refused", func(t *testing.T) {
		e := newWaitApp(t)
		writeCopy(t, e, []string{"other", "boss"})
		requireCode(t, codeWorkspaceNotFound, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		assert.NotContains(t, e.stdout.String(), "report")
	})
	t.Run("a copy with no ancestors is refused", func(t *testing.T) {
		e := newWaitApp(t)
		writeCopy(t, e, nil)
		requireCode(t, codeWorkspaceNotFound, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
	})
	t.Run("a controller not_descendant for a listed child is refused", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeNotDescendant), Message: "not a descendant"}}
		})
		requireCode(t, codeNotDescendant, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
	})
	t.Run("a wait is asked once per poll interval", func(t *testing.T) {
		e := newWaitApp(t)
		now := testNow()
		e.app.now = func() time.Time { return now }
		e.app.remotePoll = time.Hour
		writeCallerScope(t, e.app.paths.State, linuxRef)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		polls := 0
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
			polls++
			if polls == 5 {
				cancel()
			}
			return lifetime.Listing{}, nil
		}
		var records int
		busy := remoteHelperRecord(t)
		busy.Result = nil
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(op string, args envproto.ScopeChildArgs) envproto.Reply {
			if op == "record" {
				records++
			}
			if op == "status" {
				return scopeOK(t, envproto.AgentStatusResult{Session: "s", Agent: &envproto.SnapshotAgent{State: "working"}})
			}
			return helperAnswer(t, busy)(op, args)
		})
		require.Error(t, e.app.runMachineWait(ctx, []string{"helper"}, waitOptions(time.Minute)))
		assert.GreaterOrEqual(t, polls, 5)
		assert.Equal(t, 1, records)
	})
	t.Run("a failed remote child keeps a spawn record with the verdict in the fail list", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		record := remoteHelperRecord(t)
		record.Result = &envproto.RecordResult{Verdict: "FAIL", Outcome: "fail", RecordedAt: testNow()}
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, record))
		require.Error(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		assert.Equal(t, "fail", got.Children[0].Outcome)
		kept, err := machine.NewStore(e.app.paths.State).ReadRemote("boss", linuxRef.ID, "helper")
		require.NoError(t, err)
		require.NotNil(t, kept.Spawn)
		assert.Equal(t, remoteHelperRole, kept.Spawn.Role)
		assert.Equal(t, []string{"FAIL"}, kept.Spawn.Fail)
		assert.Empty(t, kept.Spawn.Pass)
		assert.Equal(t, []string{"plan", "boss"}, kept.Spawn.Ancestors)
	})
	t.Run("a passing remote child keeps a spawn record with the verdict in the pass list", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		kept, err := machine.NewStore(e.app.paths.State).ReadRemote("boss", linuxRef.ID, "helper")
		require.NoError(t, err)
		require.NotNil(t, kept.Spawn)
		assert.Equal(t, []string{"PASS"}, kept.Spawn.Pass)
		assert.Empty(t, kept.Spawn.Fail)
	})
	t.Run("a remote child with no role keeps no spawn record", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		child := remoteHelperChild(t)
		child.Role = nil
		record := remoteHelperRecord(t)
		record.Role = nil
		serveChildHost(t, e, []envproto.ScopeChild{child}, helperAnswer(t, record))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		kept, err := machine.NewStore(e.app.paths.State).ReadRemote("boss", linuxRef.ID, "helper")
		require.NoError(t, err)
		assert.Nil(t, kept.Spawn)
		require.NotNil(t, kept.Result)
	})
}

// writeCopy keeps a copy of helper on linux whose ancestors are ancestors.
func writeCopy(t *testing.T, e *mockedApp, ancestors []string) {
	t.Helper()
	res := &machine.Result{Verdict: "PASS", Ancestors: ancestors, RecordedAt: testNow()}
	require.NoError(t, machine.NewStore(e.app.paths.State).WriteRemote("boss", linuxRef.ID, "helper", nil, res, []byte("kept")))
}

func TestApp_remoteChildren_access(t *testing.T) {
	t.Run("logs of a child the controller refuses is not_descendant", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeNotDescendant), Message: "not a descendant"}}
		})
		requireCode(t, codeNotDescendant, e.app.runMachineLogs(t.Context(), "helper", &machineLogsOptions{}))
	})
	t.Run("an offline environment's copy is still used by children", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		res := &machine.Result{Verdict: "PASS", Ancestors: []string{"plan", "boss"}, RecordedAt: testNow()}
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteRemote("boss", linuxRef.ID, "helper", nil, res, nil))
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeEnvironmentOffline), Message: "linux is offline"}}
		})
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		assert.NotEmpty(t, e.app.warned)
		assert.Contains(t, e.stdout.String(), `"verdict":"PASS"`)
	})
}

func TestApp_runMachineLogs_remoteChild(t *testing.T) {
	t.Run("the tail is forwarded and the lines printed", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		var gotTail int
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(childOp string, args envproto.ScopeChildArgs) envproto.Reply {
			gotTail = args.Tail
			return helperAnswer(t, remoteHelperRecord(t))(childOp, args)
		})
		require.NoError(t, e.app.runMachineLogs(t.Context(), "helper", &machineLogsOptions{tail: 5}))
		assert.Equal(t, 5, gotTail)
		assert.Contains(t, e.stdout.String(), `"one`)
	})
}

func TestApp_runMachineRm_remoteChild(t *testing.T) {
	t.Run("rm is forwarded as the child's rm", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		require.NoError(t, e.app.runMachineRm(t.Context(), removeHelper()))
		assert.Equal(t, []string{"record", "rm"}, childOpsOf(t, host.Calls()))
	})
	t.Run("--all selects the remote child with no id given", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		var removed []string
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(op string, args envproto.ScopeChildArgs) envproto.Reply {
			if op == "rm" {
				removed = append(removed, args.Child)
			}
			return helperAnswer(t, remoteHelperRecord(t))(op, args)
		})
		opts := &machineRmOptions{all: true}
		require.Empty(t, opts.workspaces)
		require.NoError(t, e.app.runMachineRm(t.Context(), opts))
		assert.Equal(t, []string{"helper"}, removed)
	})
	t.Run("--dry-run removes nothing and names the environment", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.global.json = true
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		opts := removeHelper()
		opts.dryRun = true
		require.NoError(t, e.app.runMachineRm(t.Context(), opts))
		assert.Empty(t, childOpsOf(t, host.Calls()))
		assert.Contains(t, e.stdout.String(), `"environment":"linux"`)
	})
	t.Run("--keep-children is forwarded", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		var got envproto.ScopeChildArgs
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(op string, args envproto.ScopeChildArgs) envproto.Reply {
			if op == "rm" {
				got = args
			}
			return helperAnswer(t, remoteHelperRecord(t))(op, args)
		})
		opts := removeHelper()
		opts.keepChildren = true
		require.NoError(t, e.app.runMachineRm(t.Context(), opts))
		assert.True(t, got.KeepChildren)
	})
	t.Run("a not_descendant reply is refused with its code", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeNotDescendant), Message: "not a descendant"}}
		})
		requireCode(t, codeNotDescendant, e.app.runMachineRm(t.Context(), removeHelper()))
	})
	t.Run("an offline environment is environment_offline and the child stays", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeEnvironmentOffline), Message: "linux is offline"}}
		})
		writeCopy(t, e, []string{"plan", "boss"})
		requireCode(t, codeEnvironmentOffline, e.app.runMachineRm(t.Context(), removeHelper()))
		kept, err := machine.NewStore(e.app.paths.State).ReadRemote("boss", linuxRef.ID, "helper")
		require.NoError(t, err)
		require.NotNil(t, kept.Result)
	})
	t.Run("a failing record keeps the child and reports the error", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeInternal), Message: "record broke"}}
		})
		requireCode(t, codeInternal, e.app.runMachineRm(t.Context(), removeHelper()))
		assert.Equal(t, []string{"record"}, childOpsOf(t, host.Calls()))
	})
	t.Run("a child its environment no longer has is still removed", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(op string, args envproto.ScopeChildArgs) envproto.Reply {
			if op == "record" {
				return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeWorkspaceNotFound), Message: "gone"}}
			}
			return helperAnswer(t, remoteHelperRecord(t))(op, args)
		})
		require.NoError(t, e.app.runMachineRm(t.Context(), removeHelper()))
		assert.Equal(t, []string{"record", "rm"}, childOpsOf(t, host.Calls()))
	})
	t.Run("an unreachable environment still gets the rm", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, func(string, envproto.ScopeChildArgs) envproto.Reply {
			return envproto.Reply{Error: &envproto.ReplyError{Code: string(codeEnvironmentOffline), Message: "linux is offline"}}
		})
		requireCode(t, codeEnvironmentOffline, e.app.runMachineRm(t.Context(), removeHelper()))
		assert.Equal(t, []string{"record", "rm"}, childOpsOf(t, host.Calls()))
	})
	t.Run("--dry-run writes no copy", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		opts := removeHelper()
		opts.dryRun = true
		require.NoError(t, e.app.runMachineRm(t.Context(), opts))
		_, found, err := machine.NewStore(e.app.paths.State).FindRemote("boss", "helper")
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("a finished child's verdict stays readable after rm", func(t *testing.T) {
		e := newWaitApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		var removed atomic.Bool
		host := &childHost{}
		host.answer = func(op string, raw json.RawMessage) envproto.Reply {
			switch op {
			case "children":
				if removed.Load() {
					return scopeOK(t, envproto.ScopeChildrenResult{})
				}
				return scopeOK(t, envproto.ScopeChildrenResult{Children: []envproto.ScopeChild{remoteHelperChild(t)}})
			case "child":
				var args envproto.ScopeChildArgs
				require.NoError(t, json.Unmarshal(raw, &args))
				if args.Op == "rm" {
					removed.Store(true)
				}
				return helperAnswer(t, remoteHelperRecord(t))(args.Op, args)
			}
			return envproto.Reply{Error: &envproto.ReplyError{Code: "usage", Message: "unexpected " + op}}
		}
		serveDaemonWith(t, e.app, host)
		require.NoError(t, e.app.runMachineRm(t.Context(), removeHelper()))
		kept, err := machine.NewStore(e.app.paths.State).ReadRemote("boss", linuxRef.ID, "helper")
		require.NoError(t, err)
		require.NotNil(t, kept.Result)
		assert.Equal(t, "PASS", kept.Result.Verdict)
		e.stdout.Reset()
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		assert.Equal(t, "pass", got.Children[0].Outcome)
	})
}

func TestApp_nestRemoteProgress(t *testing.T) {
	ownSteps := func(t *testing.T, e *mockedApp) {
		t.Helper()
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteProgress("boss", "plan", &machine.Progress{Total: 3, Done: 1}))
	}
	t.Run("remote leaves raise their ancestors' rollups", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		ownSteps(t, e)
		child := remoteHelperChild(t)
		child.Rollup = &envproto.ScopeRollup{Total: 2, Done: 1}
		serveChildHost(t, e, []envproto.ScopeChild{child}, helperAnswer(t, remoteHelperRecord(t)))
		node, err := e.app.callerProgress(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 5, node.Rollup.Total)
		assert.Equal(t, 2, node.Rollup.Done)
		require.NotNil(t, node.Rollup.Fraction)
		assert.InDelta(t, 0.4, *node.Rollup.Fraction, 1e-9)
	})
	t.Run("remote leaves raise their local parent's and the caller's rollups", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteProgress("boss", "a", &machine.Progress{Total: 2, Done: 1}))
		child := remoteHelperChild(t)
		child.Parent = "a"
		child.Rollup = &envproto.ScopeRollup{Total: 2, Done: 1}
		serveChildHost(t, e, []envproto.ScopeChild{child}, helperAnswer(t, remoteHelperRecord(t)))
		node, err := e.app.callerProgress(t.Context())
		require.NoError(t, err)
		var local *progressNodeJSON
		for i := range node.Children {
			if node.Children[i].ID == "a" {
				local = &node.Children[i]
			}
		}
		require.NotNil(t, local)
		require.NotEmpty(t, local.Children)
		assert.Equal(t, "helper", local.Children[len(local.Children)-1].ID)
		assert.Equal(t, 4, local.Rollup.Total)
		assert.Equal(t, 2, local.Rollup.Done)
		assert.Equal(t, 4, node.Rollup.Total)
		assert.Equal(t, 2, node.Rollup.Done)
	})
	t.Run("a remote child with a null rollup adds nothing", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		ownSteps(t, e)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, remoteHelperRecord(t)))
		node, err := e.app.callerProgress(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 3, node.Rollup.Total)
		assert.Equal(t, 1, node.Rollup.Done)
	})
	t.Run("an out-of-range rollup is ignored with a warning", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		ownSteps(t, e)
		child := remoteHelperChild(t)
		child.Rollup = &envproto.ScopeRollup{Total: 2, Done: 3}
		serveChildHost(t, e, []envproto.ScopeChild{child}, helperAnswer(t, remoteHelperRecord(t)))
		node, err := e.app.callerProgress(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 3, node.Rollup.Total)
		assert.Equal(t, 1, node.Rollup.Done)
		assert.Contains(t, e.app.warnings(), "ignoring the rollup of helper on "+linuxRef.Name+": out of range")
	})
	t.Run("a remote child is a leaf under its parent, with its reported progress", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		record := remoteHelperRecord(t)
		record.Progress = json.RawMessage(`{"name":"login","total":3,"done":2,"note":null,"updated_at":"2026-11-24T10:00:00Z"}`)
		record.Rollup = json.RawMessage(`{"total":3,"done":2,"fraction":0.6666666666666666}`)
		serveChildHost(t, e, []envproto.ScopeChild{remoteHelperChild(t)}, helperAnswer(t, record))
		node, err := e.app.callerProgress(t.Context())
		require.NoError(t, err)
		require.Len(t, node.Children, 3)
		leaf := node.Children[len(node.Children)-1]
		assert.Equal(t, "helper", leaf.ID)
		require.NotNil(t, leaf.Progress)
		assert.Equal(t, 2, leaf.Progress.Done)
		assert.Equal(t, 3, leaf.Progress.Total)
	})
}
