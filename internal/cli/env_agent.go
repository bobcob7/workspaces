package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
)

// errNoMQTTClient is why a ws without an MQTT client can't run an agent. T10 adds one.
var errNoMQTTClient = errors.New("this ws was built without an MQTT client")

// noEnvClient is newEnvClient until T10 adds the paho client.
func noEnvClient(*slog.Logger) (envClient, error) {
	return nil, errNoMQTTClient
}

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
		Args:  exactArgs(1, "an enrollment code"),
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

// runEnvAgentRun runs the agent until SIGTERM or an interrupt, logging JSON
// lines to stderr.
func (a *App) runEnvAgentRun(ctx context.Context) error {
	log := slog.New(slog.NewJSONHandler(a.stderr, nil))
	client, err := a.newEnvClient(log)
	if err != nil {
		return newError(codeInternal, err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	probe := agentClaude{claude: claude.New(log, "claude", a.environ)}
	opts := envagent.DefaultOptions(a.version, a.config.Environments.RenewDays())
	agent := envagent.NewAgent(log, envagent.NewFiles(a.paths.Environment), client, agentSnapshots{app: a}, probe, rand.Reader, opts)
	return agentError(agent.Run(ctx))
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
		return a.writeJSON(agentStatusOutput(st, a.warnings()))
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
func agentStatusOutput(st envagent.AgentStatus, warnings []string) envAgentStatusJSON {
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

// agentSnapshots is the snapshot the agent publishes: the workspaces that
// ws list shows on this machine.
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
	entries, _ := visibleEntries(listing.Workspaces, false, raised)
	return json.Marshal(s.app.listWorkspaces(entries, listing.Unknown, loopRuns))
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
