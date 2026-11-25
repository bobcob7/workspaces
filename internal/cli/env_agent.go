package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
)

// newEnvAgentCmd is ws env agent, the environment agent's side of ADR 0075.
// It is hidden, as ADR 0073 says.
func (a *App) newEnvAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "agent",
		Short:  "Enroll this machine as a remote environment and run its agent",
		Hidden: true,
		Args:   noArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newEnvAgentEnrollCmd(), a.newEnvAgentRunCmd(), a.newEnvAgentStatusCmd(), a.newEnvAgentUnenrollCmd())
	return cmd
}

func (a *App) newEnvAgentEnrollCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enroll <CODE>",
		Short: "Enroll this machine with the code that ws env add printed",
		Args:  exactArgs(1, "enrollment code"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runEnvAgentEnroll(cmd.Context(), args[0])
		})),
	}
}

func (a *App) newEnvAgentRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "run",
		Short:   "Run this machine's environment agent until it is stopped",
		Args:    noArgs,
		PreRunE: a.noJSON,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runEnvAgentRun(cmd.Context())
		})),
	}
}

func (a *App) newEnvAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which environment this machine is enrolled as",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(_ *cobra.Command, _ []string) error {
			return a.runEnvAgentStatus()
		})),
	}
}

func (a *App) newEnvAgentUnenrollCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unenroll",
		Short: "Forget this machine's enrollment",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(_ *cobra.Command, _ []string) error {
			return a.runEnvAgentUnenroll()
		})),
	}
}

// envAgentEnrollJSON is ws env agent enroll's output.
type envAgentEnrollJSON struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Broker         string   `json:"broker"`
	CertNotAfter   string   `json:"cert_not_after"`
	CAFingerprints []string `json:"ca_fingerprints"`
	Warnings       []string `json:"warnings"`
}

// runEnvAgentEnroll enrolls this machine with code and keeps the identity it
// is given. Nothing is written when enrollment fails.
func (a *App) runEnvAgentEnroll(ctx context.Context, code string) error {
	enroller := envagent.NewEnroller(a.log, envagent.NewFiles(a.paths.Environment), rand.Reader)
	id, err := enroller.Enroll(ctx, code)
	if err != nil {
		return agentError(err)
	}
	if a.global.json {
		return a.writeJSON(envAgentEnrollJSON{
			ID:             id.ID,
			Name:           id.Name,
			Broker:         id.Broker,
			CertNotAfter:   formatTime(id.Cert.NotAfter),
			CAFingerprints: id.Fingerprints(),
			Warnings:       a.warnings(),
		})
	}
	fmt.Fprintf(a.stdout, "Enrolled as %s (%s). Start it with: ws env agent run\n", id.Name, id.ID)
	a.printWarnings()
	return nil
}

// agentNotSentLowWater bounds the unsent data in the agent's socket send queue.
const agentNotSentLowWater = 16384

// agentOptions are the options of the agent that ws env agent run starts.
func agentOptions(version string, renewDays int, workspacesRoot string) envagent.Options {
	opts := envagent.DefaultOptions(version, renewDays)
	opts.WorkspacesRoot = workspacesRoot
	opts.NotSentLowWater = agentNotSentLowWater
	return opts
}

// runEnvAgentRun runs the agent until SIGTERM or an interrupt, logging JSON
// lines to stderr.
func (a *App) runEnvAgentRun(ctx context.Context) error {
	log := slog.New(slog.NewJSONHandler(a.stderr, nil))
	files := envagent.NewFiles(a.paths.Environment)
	id, err := files.Load()
	if err != nil {
		return agentError(err)
	}
	client, err := a.newEnvClient(log)
	if err != nil {
		return newError(codeInternal, err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	probe := agentClaude{claude: claude.New(log, "claude", a.environ)}
	opts := agentOptions(a.version, a.config.Environments.RenewDays(), a.paths.Workspaces)
	ops := newAgentOperations(a, id)
	agent := envagent.NewAgent(log, files, client, agentSnapshots{app: a}, probe, ops, rand.Reader, opts)
	agent.SetLoopTicker(agentLoops{newApp: ops.newApp, root: a.paths.Root})
	socketCtx, stopSocket := context.WithCancel(ctx)
	socketDone := make(chan struct{})
	go func() {
		defer close(socketDone)
		a.serveAgentSocket(socketCtx, log, agent.SocketHandler())
	}()
	err = agent.Run(ctx)
	stopSocket()
	<-socketDone
	return agentError(err)
}

// serveAgentSocket serves handler on the agent socket until ctx ends (ADR
// 0082). A socket that can't listen is logged, and the agent runs without it.
func (a *App) serveAgentSocket(ctx context.Context, log *slog.Logger, handler http.Handler) {
	socket := a.daemonPaths().AgentSocket
	ln, err := daemon.ListenPeer(ctx, socket, log)
	if err != nil {
		log.WarnContext(ctx, "the agent socket isn't listening", "error", err)
		return
	}
	defer func() { _ = os.Remove(socket) }()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.WarnContext(ctx, "the agent socket stopped", "error", err)
	}
}

// envAgentStatusJSON is ws env agent status's output. Without an identity
// every field but enrolled and the lists is null.
type envAgentStatusJSON struct {
	Enrolled       bool     `json:"enrolled"`
	ID             *string  `json:"id"`
	Name           *string  `json:"name"`
	Broker         *string  `json:"broker"`
	CertSerial     *string  `json:"cert_serial"`
	CertNotAfter   *string  `json:"cert_not_after"`
	RenewAt        *string  `json:"renew_at"`
	CAFingerprints []string `json:"ca_fingerprints"`
	Warnings       []string `json:"warnings"`
}

// runEnvAgentStatus shows the identity this machine is enrolled with.
func (a *App) runEnvAgentStatus() error {
	st, err := envagent.StatusOf(envagent.NewFiles(a.paths.Environment), a.config.Environments.RenewDays())
	if err != nil {
		return agentError(err)
	}
	if a.global.json {
		return a.writeJSON(agentStatusOutput(&st, a.warnings()))
	}
	if !st.Enrolled {
		fmt.Fprintln(a.stdout, "This machine isn't enrolled; run ws env agent enroll <code>.")
	} else {
		fmt.Fprintf(a.stdout, "%s (%s) → %s; certificate renews from %s\n", st.Name, st.ID, st.Broker, st.RenewAt.UTC().Format(time.DateOnly))
	}
	a.printWarnings()
	return nil
}

// agentStatusOutput is st as ws env agent status prints it in JSON.
func agentStatusOutput(st *envagent.AgentStatus, warnings []string) envAgentStatusJSON {
	if !st.Enrolled {
		return envAgentStatusJSON{CAFingerprints: []string{}, Warnings: warnings}
	}
	certNotAfter := formatTime(st.CertNotAfter)
	renewAt := formatTime(st.RenewAt)
	return envAgentStatusJSON{
		Enrolled:       true,
		ID:             optionalString(st.ID),
		Name:           optionalString(st.Name),
		Broker:         optionalString(st.Broker),
		CertSerial:     optionalString(st.CertSerial),
		CertNotAfter:   &certNotAfter,
		RenewAt:        &renewAt,
		CAFingerprints: st.CAFingerprints,
		Warnings:       warnings,
	}
}

// envAgentUnenrollJSON is ws env agent unenroll's output.
type envAgentUnenrollJSON struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Unenrolled bool     `json:"unenrolled"`
	Warnings   []string `json:"warnings"`
}

// runEnvAgentUnenroll removes this machine's identity. The controller keeps
// the environment until ws env rm removes it.
func (a *App) runEnvAgentUnenroll() error {
	id, err := envagent.NewFiles(a.paths.Environment).Remove()
	if err != nil {
		return agentError(err)
	}
	if a.global.json {
		return a.writeJSON(envAgentUnenrollJSON{ID: id.ID, Name: id.Name, Unenrolled: true, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "Unenrolled %s; the controller lists it until ws env rm %s.\n", id.Name, id.Name)
	a.printWarnings()
	return nil
}

// agentError maps the agent's errors to error codes (ADR 0075). A failed
// request keeps its cause in details. Anything else is returned as it is.
func agentError(err error) error {
	switch {
	case errors.Is(err, envproto.ErrCodeInvalid):
		return newError(codeUsage, err)
	case errors.Is(err, envagent.ErrAlreadyEnrolled):
		return newError(codeEnvironmentExists, err)
	case errors.Is(err, envagent.ErrNotEnrolled):
		return newError(codeEnvironmentUnknown, err)
	case errors.Is(err, envagent.ErrEnrollmentUsed):
		return newError(codeEnrollmentUsed, err)
	case errors.Is(err, envagent.ErrPinMismatch), errors.Is(err, envagent.ErrEnrollmentFailed), errors.Is(err, envagent.ErrCertificateExpired):
		return newError(codeEnrollmentFailed, err)
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		ce := newError(codeEnrollmentFailed, err)
		ce.details = map[string]any{"cause": urlErr.Err.Error()}
		return ce
	}
	return err
}

// agentSnapshots is the snapshot the agent publishes: every workspace on
// this machine, hidden ones included and unknown directories left out.
type agentSnapshots struct{ app *App }

// Workspaces is the visible workspaces as a JSON array, [] when there are none.
func (s agentSnapshots) Workspaces(ctx context.Context) (json.RawMessage, error) {
	listing, err := s.app.lifetime.List(ctx)
	if err != nil {
		return nil, err
	}
	loopRuns := map[string]*listLoopRunJSON{}
	raised := func(e *lifetime.Entry) bool {
		return attentionOf(s.app.cachedLoopRun(loopRuns, e)) != loop.AttentionNone
	}
	entries, _ := visibleEntries(listing.Workspaces, true, raised)
	return json.Marshal(s.app.listWorkspaces(entries, nil, loopRuns))
}

// Agents is this machine's Claude Code sessions, finished ones included, newest
// started first and at most envproto.MaxSnapshotAgents, [] when there are none.
// A session with no start time sorts last.
func (s agentSnapshots) Agents(ctx context.Context) ([]envproto.SnapshotAgent, error) {
	sessions, err := s.app.claude.Agents(ctx, claude.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	agents := make([]envproto.SnapshotAgent, 0, len(sessions))
	for i := range sessions {
		agents = append(agents, snapshotAgent(&sessions[i]))
	}
	sort.SliceStable(agents, func(i, j int) bool { return startedAfter(agents[i].StartedAt, agents[j].StartedAt) })
	return agents[:min(len(agents), envproto.MaxSnapshotAgents)], nil
}

// Loops is this machine's loops, by name and at most envproto.MaxSnapshotLoops,
// [] when there are none. Their prompts, scripts and vars stay here.
func (s agentSnapshots) Loops(ctx context.Context) ([]envproto.SnapshotLoop, error) {
	loops, err := s.app.readLoops(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(loops, func(i, j int) bool { return loops[i].Name < loops[j].Name })
	loops = loops[:min(len(loops), envproto.MaxSnapshotLoops)]
	out := make([]envproto.SnapshotLoop, 0, len(loops))
	for i := range loops {
		entry, err := snapshotLoop(&loops[i])
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// snapshotLoop maps a loop to its snapshot entry, as ws loop list shows its
// run and gate.
func snapshotLoop(l *loop.Loop) (envproto.SnapshotLoop, error) {
	lastRun, err := json.Marshal(loopRunJSONOf(l.LastRun()))
	if err != nil {
		return envproto.SnapshotLoop{}, err
	}
	lastGate, err := json.Marshal(gateJSONOf(l.LastGate))
	if err != nil {
		return envproto.SnapshotLoop{}, err
	}
	out := envproto.SnapshotLoop{
		Name:     l.Name,
		Title:    l.Title,
		Every:    l.Every,
		Paused:   l.Paused,
		Hidden:   l.RunsHidden(),
		Runner:   l.RunsOn(),
		LastRun:  lastRun,
		LastGate: lastGate,
	}
	if !l.Paused && !l.NextRunAt.IsZero() {
		next := l.NextRunAt
		out.NextRunAt = &next
	}
	return out, nil
}

// startedAfter reports whether a session started at a is newer than one at b;
// a nil start time is the oldest.
func startedAfter(a, b *time.Time) bool {
	if a == nil {
		return false
	}
	return b == nil || a.After(*b)
}

// agentClaude is the agent's probe of claude: its version, or missing, or
// present without a version when claude can't say what it is.
type agentClaude struct{ claude *claude.Client }

// Probe reports whether claude is on PATH and, if it is, its version.
func (c agentClaude) Probe(ctx context.Context) (string, *string) {
	version, err := c.claude.Version(ctx)
	switch {
	case errors.Is(err, claude.ErrNotInstalled):
		return envproto.ClaudeMissing, nil
	case err != nil:
		return envproto.ClaudePresent, nil
	}
	return envproto.ClaudePresent, &version
}

// agentLoops ticks the agent's loops in fresh Apps, as the desktop's tick_forever does (ADR 0080).
type agentLoops struct {
	newApp func(stdout, stderr io.Writer) *App
	root   string
}

// agentLoopStarts caps the loops the agent starts in one pass.
const agentLoopStarts = 2

// Tick runs one pass of ws loop tick and reports whether any item started or
// failed, so the snapshot refreshes. A pass whose output isn't a loop listing
// is an error.
func (l agentLoops) Tick(ctx context.Context) (bool, error) {
	var stdout, stderr bytes.Buffer
	app := l.newApp(&stdout, &stderr)
	args := []string{"--json", "--config-dir", l.root, "loop", "tick", "--quiet", "--max-starts", strconv.Itoa(agentLoopStarts)}
	app.Run(ctx, args)
	var out loopTickOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return false, fmt.Errorf("reading the loop pass: %w: %s", err, stderr.String())
	}
	for _, item := range out.Loops {
		if item.Action == loopStarted || item.Action == loopFailed {
			return true, nil
		}
	}
	return false, nil
}
