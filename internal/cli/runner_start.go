package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/machine/role"
	"github.com/bobcob7/workspaces/internal/runner"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// runFileVersion is run.toml's version (reference.md).
const runFileVersion = 1

// runnerLogTail is how much of a runner's log a failed start reports.
const runnerLogTail = 4096

// runnerStart is one ws runner agent to start (ADRs 0064, 0070).
type runnerStart struct {
	Name, Role, Parent string // Parent "" for none (loops, --parent none)
	Endpoint, Model    string // flags; "" for the configured default
	Specs              []string
	Hidden, Offline    bool
	Vars               map[string]string // role vars; "BRANCH" feeds the push guard
	Prompt             string            // the prompt after --, appended to the role's
}

// runnerStarted is a ws runner agent that started.
type runnerStarted struct {
	Workspace workspace.Workspace
	Session   string // rn-…
	Resolved  config.Resolved
	Warnings  []string
}

// runnerLaunch is what a started runner is launched from.
type runnerLaunch struct {
	session  string
	start    *runnerStart
	resolved *config.Resolved
	prompt   string
	ro       *role.Role // nil without a role
}

// startRunnerAgent starts one ws runner agent (ADRs 0064, 0070): it resolves
// the model, has the daemon check it, creates the workspace, writes the run,
// and has the daemon start the runner. ws machine spawn, ws claude and ws
// loop runs share it. A failed check creates nothing; a failure after the
// workspace exists stops the runner and removes the workspace.
//
//nolint:gocritic // hugeParam: the request is built once per start, and read-only.
func (a *App) startRunnerAgent(ctx context.Context, rs runnerStart) (runnerStarted, error) {
	resolved, err := a.config.Resolve(rs.Role, rs.Endpoint, rs.Model)
	if err != nil {
		return runnerStarted{}, a.runnerConfigError(err)
	}
	ro, prompt, err := a.runnerPrompt(&rs, &resolved)
	if err != nil {
		return runnerStarted{}, err
	}
	client, check, err := a.checkRunner(ctx, &rs)
	if err != nil {
		return runnerStarted{}, err
	}
	if check.ContextTokens != nil && *check.ContextTokens > 0 {
		resolved.ContextTokens = *check.ContextTokens
	}
	ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: rs.Name, Specs: parseSpecs(rs.Specs), Offline: rs.Offline, Hidden: rs.Hidden, Parent: rs.Parent})
	if err != nil {
		return runnerStarted{}, domainError(err)
	}
	defer lock.Unlock()
	launch := runnerLaunch{session: runner.NewSessionID(), start: &rs, resolved: &resolved, prompt: prompt, ro: ro}
	if err := a.launchRunner(ctx, client, &ws, &launch); err != nil {
		a.discardRunner(ctx, ws.ID)
		return runnerStarted{}, err
	}
	warnings := slices.Concat(resolved.Warnings, check.Warnings)
	if ro != nil {
		if err := a.writeSpawn(ro, rs.Vars, ws.ID); err != nil {
			warnings = append(warnings, fmt.Sprintf("couldn't record role %s for %s: %v", ro.Name, ws.ID, err))
		}
	}
	a.warned = append(a.warned, warnings...)
	return runnerStarted{Workspace: ws, Session: launch.session, Resolved: resolved, Warnings: warnings}, nil
}

// runnerConfigError is endpoint_unknown, with the endpoints there are, or
// config_invalid for any other configuration problem.
func (a *App) runnerConfigError(err error) error {
	if !errors.Is(err, config.ErrEndpointUnknown) {
		return newError(codeConfigInvalid, err)
	}
	ce := newError(codeEndpointUnknown, err)
	ce.details = map[string]any{"endpoints": slices.Sorted(maps.Keys(a.config.Endpoints))}
	return ce
}

// runnerPrompt is the role, when rs names one, and the prompt the runner
// starts with: the role's, rendered with the vars and the prompt after "--"
// (ADR 0050), else that prompt.
func (a *App) runnerPrompt(rs *runnerStart, resolved *config.Resolved) (*role.Role, string, error) {
	if rs.Role == "" {
		return nil, rs.Prompt, nil
	}
	ro, err := a.loadRole(rs.Role)
	if err != nil {
		return nil, "", err
	}
	prompt, err := ro.RenderFor(rs.Vars, rs.Prompt, config.RunnerWS, resolved.ModelID)
	if err != nil {
		return nil, "", newError(codeUsage, err)
	}
	return &ro, prompt, nil
}

// checkRunner has the daemon check rs's endpoint and model (ADR 0064), which
// starts the daemon when none runs. A failed check is returned with the code
// the daemon answered; the response carries the window and warnings it chose.
func (a *App) checkRunner(ctx context.Context, rs *runnerStart) (runnerDaemon, daemon.PreflightResponse, error) {
	client, err := a.dialDaemon(ctx)
	if err != nil {
		return nil, daemon.PreflightResponse{}, err
	}
	res, err := client.Preflight(ctx, daemon.PreflightRequest{Role: rs.Role, Endpoint: rs.Endpoint, Model: rs.Model})
	if err != nil {
		return nil, daemon.PreflightResponse{}, remoteRunnerError(err)
	}
	return client, res, nil
}

// launchRunner writes ws's run, records its runner session, has the daemon
// start the runner, and waits for the runner to write its status (ADR 0063).
func (a *App) launchRunner(ctx context.Context, client runnerDaemon, ws *workspace.Workspace, l *runnerLaunch) error {
	files := runner.FilesFor(a.paths.State, ws.ID)
	run := a.newRun(l)
	if err := files.WriteRun(&run); err != nil {
		return newError(codeInternal, err)
	}
	if err := a.workspaces.RecordRunnerSession(ws.ID, l.session); err != nil {
		return domainError(err)
	}
	if _, err := client.StartRunner(ctx, ws.ID, false); err != nil {
		return remoteRunnerError(err)
	}
	return awaitRunnerStatus(ctx, files, a.runnerWait)
}

// newRun is the run.toml a spawn writes for a runner agent (reference.md):
// what the resolution chose, with its first endpoint history entry. The
// context size is the preflighted one, which a probe may have reduced.
func (a *App) newRun(l *runnerLaunch) runner.Run {
	r, start := l.resolved, l.start
	now := a.now().UTC().Truncate(time.Second)
	endpoint := r.Endpoint
	run := runner.Run{
		Version:          runFileVersion,
		Session:          l.session,
		ModelAlias:       r.ModelAlias,
		Model:            r.ModelID,
		Endpoint:         r.EndpointName,
		URL:              endpoint.URL,
		API:              endpoint.API,
		ContextTokens:    r.ContextTokens,
		MaxOutput:        r.MaxOutput,
		Temperature:      r.Temperature,
		Reasoning:        r.Reasoning,
		RequestTimeout:   endpoint.RequestTimeout,
		IdleTimeout:      endpoint.IdleTimeout,
		Role:             start.Role,
		Parent:           start.Parent,
		Pass:             slices.Clone(machine.DefaultPass),
		Fail:             slices.Clone(machine.DefaultFail),
		MaxSteps:         r.MaxSteps,
		Timeout:          r.Timeout,
		MaxTokens:        r.MaxTokens,
		Branch:           start.Vars["BRANCH"],
		MCPServers:       r.MCPServers,
		MCPTools:         r.MCPTools,
		MCPDeny:          r.MCPDeny,
		MCPUpfront:       r.MCPUpfront,
		UserInstructions: a.config.Runner.UserInstructions,
		Prompt:           l.prompt,
		TraceID:          llm.NewTraceID(),
		CreatedAt:        now,
		EndpointAlias:    r.EndpointAlias,
		EndpointFlag:     start.Endpoint,
		EndpointHistory: []runner.EndpointEntry{{
			Since:         now,
			Step:          0,
			Reason:        "spawn",
			Endpoint:      r.EndpointName,
			URL:           endpoint.URL,
			API:           endpoint.API,
			Model:         r.ModelID,
			ContextTokens: r.ContextTokens,
		}},
	}
	if l.ro != nil {
		run.Pass, run.Fail = l.ro.Pass, l.ro.Fail
	}
	return run
}

// awaitRunnerStatus waits, for up to wait, until the runner writes its
// status.json. One that doesn't is runner_failed, with the end of its log.
func awaitRunnerStatus(ctx context.Context, files runner.Files, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(runnerPollEvery)
	defer ticker.Stop()
	for {
		if _, err := files.ReadStatus(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return runnerFailed(files, wait)
		case <-ticker.C:
		}
	}
}

// runnerFailed is runner_failed: the runner wrote no status in time.
func runnerFailed(files runner.Files, wait time.Duration) error {
	ce := newError(codeRunnerFailed, fmt.Errorf("the runner wrote no status within %s", wait))
	ce.details = map[string]any{"log": files.Log(), "stderr": logTail(files.Log(), runnerLogTail)}
	return ce
}

// logTail is the last n bytes of the file at path, or "" when it can't be read.
func logTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > n {
		data = data[len(data)-n:]
	}
	return string(data)
}

// discardRunner removes workspace id after its runner failed to start. The
// workspace is kept when the runner can't be stopped, since it may still run.
func (a *App) discardRunner(ctx context.Context, id string) {
	if err := (daemonRunnerControl{app: a}).Stop(context.WithoutCancel(ctx), id); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("kept workspace %s: its runner may still run: %v", id, err))
		return
	}
	a.removeWorkspace(id)
}
