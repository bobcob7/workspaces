package envagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// errNoReply is what Up returns when the controller doesn't answer by the
// op's timeout plus the grace.
var errNoReply = errors.New("the controller didn't answer")

// SocketHandler serves the four scope routes of the agent socket (ADR 0082).
// Every other path is not found.
func (a *Agent) SocketHandler() http.Handler {
	mux := http.NewServeMux()
	for _, name := range []string{"envs", "spawn", "children", "child"} {
		mux.HandleFunc("POST /v1/scope/"+name, a.scopeRoute("scope."+name))
	}
	return mux
}

// scopeRoute is the handler of one scope op: it reads the body up to the
// request limit and writes the reply the op's request gets.
func (a *Agent) scopeRoute(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, envproto.MaxRequestBytes))
		if err != nil {
			writeReply(w, envproto.ErrorReply("usage", "the request is too large or unreadable"))
			return
		}
		writeReply(w, a.scopeReply(r.Context(), op, body))
	}
}

// scopeReply checks a scope request's arguments and workspace, then sends it
// to the controller. It returns the reply's body.
func (a *Agent) scopeReply(ctx context.Context, op string, body []byte) []byte {
	args, err := envproto.DecodeArgs(op, body)
	if err != nil {
		return refusalReply(err)
	}
	caller := scopeCaller(args)
	if caller.Workspace == nil {
		return envproto.ErrorReply("usage", "a scope request needs a workspace")
	}
	workspace := *caller.Workspace
	if err := CheckWorkspaceRef(a.opts.WorkspacesRoot, workspace); err != nil {
		return refusalReply(err)
	}
	if _, err := os.Stat(filepath.Join(a.opts.WorkspacesRoot, workspace)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return envproto.ErrorReply("workspace_not_found", fmt.Sprintf("no workspace %q on this environment", workspace))
		}
		return envproto.ErrorReply("internal", "the workspace couldn't be checked")
	}
	reply, err := a.Up(ctx, op, body)
	switch {
	case errors.Is(err, errDisconnected):
		return offlineReply("agent_unreachable")
	case errors.Is(err, errNoReply):
		return offlineReply("no_reply")
	case err != nil:
		return refusalReply(err)
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return envproto.ErrorReply("internal", "reply not encoded")
	}
	return out
}

// scopeCaller is the caller fields of a decoded scope request.
func scopeCaller(args any) *envproto.ScopeCallerArgs {
	switch a := args.(type) {
	case *envproto.ScopeEnvsArgs:
		return &a.ScopeCallerArgs
	case *envproto.ScopeSpawnArgs:
		return &a.ScopeCallerArgs
	case *envproto.ScopeChildrenArgs:
		return &a.ScopeCallerArgs
	case *envproto.ScopeChildArgs:
		return &a.ScopeCallerArgs
	}
	return &envproto.ScopeCallerArgs{}
}

// offlineReply is the environment_offline reply, with the reason it is offline.
func offlineReply(reason string) []byte {
	return refusalReply(&envproto.ReplyError{
		Code:    "environment_offline",
		Message: "the environment's agent didn't answer",
		Details: map[string]any{"reason": reason},
	})
}

// writeReply writes body as the 200 response of the agent socket.
func writeReply(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// Up sends the scope op with args to the controller on env/<id>/up, with the
// response topic env/<id>/req, and waits for its reply until the op's timeout
// plus the grace. It is safe for concurrent use.
func (a *Agent) Up(ctx context.Context, op string, args json.RawMessage) (envproto.Reply, error) {
	spec, ok := envproto.LookupOp(op)
	if !ok || spec.Caller != envproto.CallerAgent {
		return envproto.Reply{}, &envproto.ReplyError{Code: "usage", Message: "not an agent operation: " + op}
	}
	id, gone, err := a.uplink()
	if err != nil {
		return envproto.Reply{}, err
	}
	requestID, err := newCorrelation(a.random)
	if err != nil {
		return envproto.Reply{}, fmt.Errorf("making a request id: %w", err)
	}
	deadline := a.opts.Now().Add(spec.Timeout)
	payload, err := json.Marshal(envproto.Request{Op: op, Args: args, RequestID: requestID, Deadline: &deadline})
	if err != nil {
		return envproto.Reply{}, fmt.Errorf("encoding %s: %w", op, err)
	}
	replies := make(chan envproto.Message, 1)
	a.addPending(requestID, replies)
	defer a.removePending(requestID)
	err = a.client.Publish(ctx, envproto.Message{
		Topic:           envproto.Topic(id, envproto.LeafUp),
		Payload:         payload,
		QoS:             1,
		ResponseTopic:   envproto.Topic(id, envproto.LeafReq),
		CorrelationData: []byte(requestID),
	})
	if err != nil {
		return envproto.Reply{}, fmt.Errorf("publishing %s: %w", op, err)
	}
	select {
	case msg := <-replies:
		var reply envproto.Reply
		if err := json.Unmarshal(msg.Payload, &reply); err != nil {
			return envproto.Reply{}, fmt.Errorf("reading the controller's reply: %w", err)
		}
		return reply, nil
	case <-gone:
		return envproto.Reply{}, errDisconnected
	case <-ctx.Done():
		return envproto.Reply{}, ctx.Err()
	case <-a.opts.After(spec.Timeout + envproto.Grace):
		return envproto.Reply{}, errNoReply
	}
}

// uplink is the id of the connected environment and a channel that closes
// when its connection ends. It is errDisconnected when nothing is connected.
func (a *Agent) uplink() (string, <-chan struct{}, error) {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	if a.upGone == nil {
		return "", nil, errDisconnected
	}
	return a.upID, a.upGone, nil
}

// beginUplink records that the connection to the environment id is up.
func (a *Agent) beginUplink(id string) {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	a.upID = id
	a.upGone = make(chan struct{})
}

// endUplink records that the connection ended, waking the requests waiting on it.
func (a *Agent) endUplink() {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	if a.upGone != nil {
		close(a.upGone)
		a.upGone = nil
	}
}

// addPending registers replies for the request with correlation data correlation.
func (a *Agent) addPending(correlation string, replies chan envproto.Message) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	a.pending[correlation] = replies
}

// removePending forgets the request with correlation data correlation.
func (a *Agent) removePending(correlation string) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	delete(a.pending, correlation)
}

// waiter is the channel of the request waiting for correlation, if any.
func (a *Agent) waiter(correlation string) (chan envproto.Message, bool) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	replies, ok := a.pending[correlation]
	return replies, ok
}
