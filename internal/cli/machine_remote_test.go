package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
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
func remoteHelperRecord(t *testing.T) envproto.MachineRecordResult {
	t.Helper()
	role := remoteHelperRole
	report := "the helper's report"
	return envproto.MachineRecordResult{
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
	return &machineRmOptions{rmOptions: rmOptions{workspaces: []string{"helper"}}}
}

// helperAnswer answers helper's record and status and its logs and removal
// from the given record and logs.
func helperAnswer(t *testing.T, record envproto.MachineRecordResult) func(string, envproto.ScopeChildArgs) envproto.Reply {
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

func TestApp_runMachineWait_remoteChild(t *testing.T) {
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
	t.Run("after the child is removed its copy is still read", func(t *testing.T) {
		e := newWaitApp(t)
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteRemote("boss", linuxRef.ID, "helper", nil, &machine.Result{Verdict: "PASS", RecordedAt: testNow()}, []byte("kept")))
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"helper"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		assert.Equal(t, "pass", got.Children[0].Outcome)
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
		assert.Equal(t, []string{"rm"}, childOpsOf(t, host.Calls()))
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
		requireCode(t, codeEnvironmentOffline, e.app.runMachineRm(t.Context(), removeHelper()))
	})
}
