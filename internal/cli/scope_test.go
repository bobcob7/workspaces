package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
)

// linuxRef and winRef are two enrolled environments the scope tests use.
var (
	linuxRef = envscope.Ref{ID: "env-1a2b3c4d", Name: "linux"}
	winRef   = envscope.Ref{ID: "env-2b3c4d5e", Name: "win-rig"}
)

// scopeCall is one scope operation an environment host was asked for.
type scopeCall struct {
	caller string
	op     string
	args   json.RawMessage
}

// scopeHost is an environment host that records scope operations and answers
// each with reply.
type scopeHost struct {
	daemon.NopEnvironments
	reply envproto.Reply
	mu    sync.Mutex
	calls []scopeCall
}

// Scope records the call and answers it with the host's reply.
func (h *scopeHost) Scope(_ context.Context, caller, op string, args json.RawMessage) envproto.Reply {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, scopeCall{caller: caller, op: op, args: args})
	return h.reply
}

// Calls is the scope operations the host was asked for, oldest first.
func (h *scopeHost) Calls() []scopeCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

// serveScopeHost serves a scopeHost that answers with reply until the test ends.
func serveScopeHost(t *testing.T, app *App, reply envproto.Reply) *scopeHost {
	t.Helper()
	host := &scopeHost{reply: reply}
	serveDaemonWith(t, app, host)
	return host
}

// scopeOK is a successful scope reply whose result is result as JSON.
func scopeOK(t *testing.T, result any) envproto.Reply {
	t.Helper()
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	return envproto.Reply{OK: true, Result: raw}
}

// writeScopeFile writes the scope file of workspace id under state: a scope of
// this machine and refs, granted by source, with no parent.
func writeScopeFile(t *testing.T, state, id, source string, refs ...envscope.Ref) {
	t.Helper()
	scope, err := envscope.New(envscope.Ref{ID: envscope.Local, Name: envscope.Local}, refs)
	require.NoError(t, err)
	require.NoError(t, envscope.Write(state, id, envscope.FileFor(scope, source, nil, time.Now())))
}

// scopeFileOf reads the scope of workspace id on this machine, and its file.
func scopeFileOf(t *testing.T, state, id string) (envscope.Scope, *envscope.File) {
	t.Helper()
	scope, file, err := envscope.Read(state, id, envscope.Ref{ID: envscope.Local, Name: envscope.Local})
	require.NoError(t, err)
	require.NotNil(t, file)
	return scope, file
}

// errorDetails is the details of err, which must be a cliError's.
func errorDetails(t *testing.T, err error) map[string]any {
	t.Helper()
	ce, ok := errors.AsType[*cliError](err)
	require.True(t, ok, "not a cliError: %v", err)
	return ce.details
}
