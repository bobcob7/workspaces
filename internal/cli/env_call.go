package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// requestIDBytes is the random bytes of a request id: 16 hex characters.
const requestIDBytes = 8

// dialEnvironmentCaller is a client to the daemon's call endpoint, started
// when none runs unless WS_DAEMON_AUTOSTART=0.
func (a *App) dialEnvironmentCaller(ctx context.Context) (environmentCaller, error) {
	client, err := a.daemonClient(ctx)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// newRequestID is 16 lowercase hex characters from crypto/rand.
func newRequestID() (string, error) {
	b := make([]byte, requestIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making a request id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// callEnvironment sends op with args to name with a new request id and
// deadline (now + LookupOp(op).Timeout), and returns the reply's result.
// Daemon errors keep their codes: environment_unknown, environment_offline,
// environment_outdated and usage, with their details. An agent's error reply
// keeps its code (an unknown code is internal with details.remote_code) and
// gains details.environment (D9).
func (a *App) callEnvironment(ctx context.Context, name, op string, args any) (json.RawMessage, error) {
	spec, ok := envproto.LookupOp(op)
	if !ok {
		return nil, usageErrorf("unknown operation %s", op)
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encoding the arguments of %s: %w", op, err)
	}
	id, err := newRequestID()
	if err != nil {
		return nil, err
	}
	client, err := a.dialEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := client.CallEnvironment(ctx, name, daemon.CallRequest{Op: op, Args: body, RequestID: id, Deadline: a.now().Add(spec.Timeout)})
	if err != nil {
		return nil, callError(err)
	}
	var reply envproto.Reply
	if err := json.Unmarshal(raw.Reply, &reply); err != nil {
		return nil, newError(codeInternal, fmt.Errorf("reading the reply of %s: %w", name, err))
	}
	if !reply.OK {
		return nil, replyError(name, reply.Error)
	}
	return reply.Result, nil
}

// callError is the error for a failed call to the daemon: an answer from the
// daemon keeps its code, if ws knows it, and its details; an unknown code is
// internal with details.remote_code. Anything else is returned as it is.
func callError(err error) error {
	remote, ok := errors.AsType[*daemon.RemoteError](err)
	if !ok {
		return err
	}
	code := errorCode(remote.Code)
	details := maps.Clone(remote.Details)
	if !code.known() {
		code = codeInternal
		if details == nil {
			details = map[string]any{}
		}
		details["remote_code"] = remote.Code
	}
	ce := newError(code, err)
	ce.details = details
	return ce
}

// replyError is the error for an agent's error reply (D9): its code, or
// internal with details.remote_code when ws doesn't know the code, and
// details.environment.
func replyError(name string, remote *envproto.ReplyError) error {
	if remote == nil {
		return newError(codeInternal, fmt.Errorf("environment %s failed without saying why", name))
	}
	code := errorCode(remote.Code)
	details := map[string]any{}
	maps.Copy(details, remote.Details)
	if !code.known() {
		code = codeInternal
		details["remote_code"] = remote.Code
	}
	details["environment"] = name
	ce := newError(code, errors.New(remote.Message))
	ce.details = details
	return ce
}

// remoteParent applies D2's rules to --env and --parent and returns the
// environment to run on ("" for local) and the parent to pass (an id on that
// environment, or "" for none). It reads snapshots with remoteEntries.
func (a *App) remoteParent(_ context.Context, env, parent string) (runOn, parentID string, err error) {
	switch env {
	case "", localEnvironment:
		return a.parentWithoutEnv(env, parent)
	}
	selected, err := a.selectEnvironments([]string{env})
	if err != nil {
		return "", "", err
	}
	return a.parentOnEnv(selected[0], parent)
}

// parentWithoutEnv is remoteParent for no --env, or --env local. A local
// parent works as today. Otherwise, with no --env, a parent found in exactly
// one environment's snapshot runs the child there; with --env local, a
// remote parent is environment_mismatch. A parent found nowhere is left to
// the local lookup, which reports workspace_not_found.
func (a *App) parentWithoutEnv(env, parent string) (runOn, parentID string, err error) {
	if parent == "" || parent == requesterNone {
		return "", parent, nil
	}
	_, findErr := a.workspaces.Find(parent)
	if findErr == nil {
		return "", parent, nil
	}
	if !errors.Is(findErr, workspace.ErrNotFound) {
		return "", "", domainError(findErr)
	}
	found, err := a.environmentsWith(parent)
	if err != nil {
		return "", "", err
	}
	switch {
	case len(found) == 0:
		return "", parent, nil
	case env == localEnvironment:
		return "", "", newError(codeEnvironmentMismatch, fmt.Errorf("workspace %s is on environment %s, not local", parent, found[0]))
	case len(found) > 1:
		return "", "", usageErrorf("workspace %s is on more than one environment (%s): pass --env", parent, strings.Join(found, ", "))
	}
	return found[0], parent, nil
}

// parentOnEnv is remoteParent for --env naming a remote environment. The
// default parent, the local workspace ws runs in, is dropped with a warning.
// An explicit one must be in the environment's snapshot.
func (a *App) parentOnEnv(name, parent string) (runOn, parentID string, err error) {
	if parent == requesterNone {
		return name, "", nil
	}
	if parent == "" {
		enclosing, enclosingErr := a.enclosingOr("")
		if enclosingErr != nil {
			return "", "", enclosingErr
		}
		if enclosing != "" {
			a.warned = append(a.warned, fmt.Sprintf("not a child of %s: it runs on %s", enclosing, name))
		}
		return name, "", nil
	}
	entries, err := a.remoteEntries(name, nil)
	if err != nil {
		return "", "", fmt.Errorf("reading the snapshot of %s: %w", name, err)
	}
	for i := range entries.entries {
		if deref(entries.entries[i].ID) == parent {
			return name, parent, nil
		}
	}
	if _, findErr := a.workspaces.Find(parent); findErr == nil {
		return "", "", newError(codeEnvironmentMismatch, fmt.Errorf("workspace %s is local, not on environment %s", parent, name))
	} else if !errors.Is(findErr, workspace.ErrNotFound) {
		return "", "", domainError(findErr)
	}
	found, err := a.environmentsWith(parent)
	if err != nil {
		return "", "", err
	}
	if len(found) > 0 {
		return "", "", newError(codeEnvironmentMismatch, fmt.Errorf("workspace %s is on environment %s, not %s", parent, found[0], name))
	}
	return "", "", newError(codeWorkspaceNotFound, fmt.Errorf("no workspace %s on environment %s", parent, name))
}

// environmentsWith is the names of the environments whose snapshots list the
// workspace id,.
func (a *App) environmentsWith(id string) ([]string, error) {
	recs, err := a.envStore().List()
	if err != nil {
		return nil, envError(err)
	}
	var found []string
	for i := range recs {
		entries, err := a.remoteEntries(recs[i].Name, nil)
		if err != nil {
			a.warned = append(a.warned, fmt.Sprintf("couldn't read environment %s's snapshot: %v", recs[i].Name, err))
			continue
		}
		for j := range entries.entries {
			if deref(entries.entries[j].ID) == id {
				found = append(found, recs[i].Name)
				break
			}
		}
	}
	return found, nil
}
