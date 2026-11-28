package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// envStore is the controller's store for this config directory.
func (a *App) envStore() *environment.Store {
	return environment.NewStore(a.paths.Environments, a.paths.State, a.now, rand.Reader)
}

// daemonEnvironments is the daemon's host: envHost around a Controller. It is
// a NopEnvironments without a reason when [environments] has no public_name,
// and one with newEnvBroker's error as its reason when that fails.
func (a *App) daemonEnvironments(log *slog.Logger) daemon.EnvironmentHost {
	env := &a.config.Environments
	if env.PublicName == "" {
		return daemon.NopEnvironments{}
	}
	broker, err := a.newEnvBroker(log)
	if err != nil {
		return daemon.NopEnvironments{Reason: err.Error()}
	}
	settings := environment.Settings{
		Listen:          env.ListenAddr(),
		EnrollListen:    env.EnrollAddr(),
		APIListen:       env.APIAddr(),
		PublicName:      env.PublicName,
		RenewBeforeDays: env.RenewDays(),
	}
	ctrl := environment.NewController(log, a.envStore(), broker, settings, a.now)
	ctrl.SetLocalScopes(localScopes{workspaces: a.workspaces, state: a.paths.State}, envscope.NewLedger(a.paths.Environments))
	ctrl.SetArtifactAPI(a.artifactAPI())
	return envHost{ctrl: ctrl}
}

// envHost adapts *environment.Controller to daemon.EnvironmentHost.
type envHost struct{ ctrl *environment.Controller }

// Start listens for environments.
func (h envHost) Start(ctx context.Context) error { return h.ctrl.Start(ctx) }

// Reload rereads the environments' state.
func (h envHost) Reload(ctx context.Context) error { return h.ctrl.Reload(ctx) }

// Scope runs one scope operation for caller on the controller (ADR 0082).
func (h envHost) Scope(ctx context.Context, caller, op string, args json.RawMessage) envproto.Reply {
	return h.ctrl.Scope(ctx, caller, op, args)
}

// localScopes reads the scopes of this machine's workspaces from their scope
// files (ADR 0082).
type localScopes struct {
	workspaces workspaceStore
	state      string
}

// Scope is workspace id's scope: its scope file, or its own environment alone
// when the file is missing or malformed. environment.ErrWorkspaceNotFound when
// there's no such workspace.
func (l localScopes) Scope(id string) (envscope.Scope, error) {
	ws, err := l.workspaces.Find(id)
	if errors.Is(err, workspace.ErrNotFound) {
		return envscope.Scope{}, environment.ErrWorkspaceNotFound
	}
	if err != nil {
		return envscope.Scope{}, err
	}
	scope, _, err := envscope.Read(l.state, ws.ID, envscope.Ref{ID: envscope.Local, Name: envscope.Local})
	if errors.Is(err, envscope.ErrInvalid) {
		return scope, nil
	}
	return scope, err
}

// Exists reports whether workspace id exists on this machine.
func (l localScopes) Exists(id string) (bool, error) {
	_, err := l.workspaces.Find(id)
	if errors.Is(err, workspace.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Status is the controller's listeners and environments.
func (h envHost) Status() daemon.EnvironmentsStatus {
	st := h.ctrl.Status()
	return environmentsStatus(&st)
}

// Warnings are the controller's warnings.
func (h envHost) Warnings() []string { return h.ctrl.Warnings() }

// Close stops the listeners.
func (h envHost) Close() error { return h.ctrl.Close() }

// Call sends one operation to the environment called name and answers its
// reply.
//
//nolint:gocritic // hugeParam: the CallRequest value is the fixed API.
func (h envHost) Call(ctx context.Context, name string, req daemon.CallRequest) (daemon.CallReply, error) {
	reply, err := h.ctrl.Call(ctx, name, environment.CallRequest{
		Op:        req.Op,
		Args:      req.Args,
		RequestID: req.RequestID,
		Deadline:  req.Deadline,
	})
	if err != nil {
		return daemon.CallReply{}, callHostError(err)
	}
	data, err := json.Marshal(reply)
	if err != nil {
		return daemon.CallReply{}, fmt.Errorf("encoding the reply: %w", err)
	}
	return daemon.CallReply{Reply: data}, nil
}

// OpenTerminal opens a terminal on the environment called name. The stream is
// nil on an error, not a typed nil.
//
//nolint:gocritic // hugeParam: the args value is the fixed API.
func (h envHost) OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (daemon.TerminalStream, envproto.TerminalOpenResult, error) {
	term, result, err := h.ctrl.OpenTerminal(ctx, name, args)
	if err != nil {
		return nil, envproto.TerminalOpenResult{}, callHostError(err)
	}
	return term, result, nil
}

// callHostError is the daemon's HostError for a controller error that a call
// can answer with. Any other error is returned as it is.
func callHostError(err error) error {
	if errors.Is(err, environment.ErrUnknown) {
		return &daemon.HostError{Err: daemon.ErrEnvironmentUnknown, Message: err.Error()}
	}
	if offline, ok := errors.AsType[*environment.OfflineError](err); ok {
		return &daemon.HostError{Err: daemon.ErrEnvironmentOffline, Message: err.Error(), Details: offlineDetails(offline)}
	}
	if outdated, ok := errors.AsType[*environment.OutdatedError](err); ok {
		return &daemon.HostError{Err: daemon.ErrEnvironmentOutdated, Message: err.Error(), Details: map[string]any{
			"environment":   outdated.Environment,
			"op":            outdated.Op,
			"ws_version":    outdated.WSVersion,
			"protocol":      outdated.Protocol,
			"need_protocol": outdated.Need,
		}}
	}
	if errors.Is(err, environment.ErrNotForwarded) {
		return &daemon.HostError{Err: daemon.ErrNotForwarded, Message: err.Error()}
	}
	if errors.Is(err, environment.ErrRequestPending) {
		return &daemon.HostError{Err: daemon.ErrRequestPending, Message: err.Error()}
	}
	return err
}

// offlineDetails is an offline call's details: when the environment was last
// seen is null when never.
func offlineDetails(offline *environment.OfflineError) map[string]any {
	var lastSeen any
	if offline.LastSeen != nil {
		lastSeen = offline.LastSeen.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"environment": offline.Environment,
		"reason":      offline.Reason,
		"last_seen":   lastSeen,
		"request_id":  offline.RequestID,
		"op":          offline.Op,
	}
}

// environmentsStatus is the daemon's view of st.
func environmentsStatus(st *environment.Status) daemon.EnvironmentsStatus {
	out := daemon.EnvironmentsStatus{
		Listening:    st.Listening,
		Listen:       st.Listen,
		EnrollListen: st.EnrollListen,
		APIListen:    st.APIListen,
		Environments: make([]daemon.EnvironmentPresence, 0, len(st.Environments)),
	}
	for _, p := range st.Environments {
		out.Environments = append(out.Environments, daemon.EnvironmentPresence{
			ID:       p.ID,
			Name:     p.Name,
			State:    string(p.State),
			Status:   p.Status,
			LastSeen: p.LastSeen,
			Hello:    environmentHello(p.Hello),
		})
	}
	return out
}

// environmentHello is an online environment's hello as the daemon reports
// it, or nil without one.
func environmentHello(h *envproto.Hello) *daemon.EnvironmentHello {
	if h == nil {
		return nil
	}
	return &daemon.EnvironmentHello{
		WSVersion:     h.WSVersion,
		OS:            h.OS,
		Arch:          h.Arch,
		Claude:        h.Claude,
		ClaudeVersion: h.ClaudeVersion,
		CertNotAfter:  h.CertNotAfter.UTC().Format(time.RFC3339),
		Protocol:      h.Protocol,
	}
}

// noDaemonWarning is the warning when no ws daemon runs for this config
// directory, so remote environments can't connect.
const noDaemonWarning = "no ws daemon runs for this config directory; remote environments connect once one does (ws daemon run, or the desktop app)"

// reloadEnvironments asks a running daemon to reload. With no daemon it
// returns noDaemonWarning; on another error,
// "the ws daemon didn't reload the environments: <err>".
func (a *App) reloadEnvironments(ctx context.Context) []string {
	err := daemon.Dial(a.daemonPaths()).ReloadEnvironments(ctx)
	switch {
	case errors.Is(err, daemon.ErrNotRunning):
		return []string{noDaemonWarning}
	case err != nil:
		return []string{fmt.Sprintf("the ws daemon didn't reload the environments: %v", err)}
	}
	return nil
}
