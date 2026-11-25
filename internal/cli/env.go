package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// newEnvCmd is ws env: enroll, list, rotate and remove remote environments (ADR 0075).
func (a *App) newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Add, list, rotate and remove remote environments, and rotate their CA",
		Args:  noArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newEnvAddCmd(), a.newEnvListCmd(), a.newEnvRmCmd(), a.newEnvRotateCmd(), a.newEnvCACmd(), a.newEnvAgentCmd())
	return cmd
}

func (a *App) newEnvAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <NAME>",
		Short: "Add a remote environment and print its enrollment code",
		Args:  exactArgs(1, "environment name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runEnvAdd(cmd.Context(), args[0])
		})),
	}
}

func (a *App) newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List remote environments and whether they are online",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runEnvList(cmd.Context())
		})),
	}
}

func (a *App) newEnvRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <NAME>",
		Short: "Remove a remote environment and revoke its certificate",
		Args:  exactArgs(1, "environment name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runEnvRemove(cmd.Context(), args[0])
		})),
	}
}

func (a *App) newEnvRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate <NAME>",
		Short: "Give a remote environment a new enrollment code",
		Args:  exactArgs(1, "environment name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runEnvRotate(cmd.Context(), args[0])
		})),
	}
}

// envAddJSON is ws env add's output.
type envAddJSON struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	State     string   `json:"state"`
	Code      string   `json:"code"`
	CACreated bool     `json:"ca_created"`
	Warnings  []string `json:"warnings"`
}

// envRotateJSON is ws env rotate's output.
type envRotateJSON struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	State          string   `json:"state"`
	Code           string   `json:"code"`
	RevokedSerials []string `json:"revoked_serials"`
	Warnings       []string `json:"warnings"`
}

// envRemoveJSON is ws env rm's output.
type envRemoveJSON struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	RevokedSerials []string `json:"revoked_serials"`
	Warnings       []string `json:"warnings"`
}

// runEnvAdd makes a pending environment called name. The code is printed
// once, on stdout only.
func (a *App) runEnvAdd(ctx context.Context, name string) error {
	enrollHost, err := a.enrollHost()
	if err != nil {
		return err
	}
	rec, code, caCreated, err := a.envStore().Add(ctx, name, enrollHost)
	if err != nil {
		return envError(err)
	}
	a.warned = append(a.warned, a.reloadEnvironments(ctx)...)
	if a.global.json {
		return a.writeJSON(envAddJSON{ID: rec.ID, Name: rec.Name, State: string(rec.State), Code: code, CACreated: caCreated, Warnings: a.warnings()})
	}
	a.printEnrollment(rec.Name, code)
	a.printWarnings()
	return nil
}

// runEnvRotate gives an existing environment a new code, revoking its
// certificate if it was enrolled.
func (a *App) runEnvRotate(ctx context.Context, name string) error {
	enrollHost, err := a.enrollHost()
	if err != nil {
		return err
	}
	rec, code, revoked, err := a.envStore().Rotate(ctx, name, enrollHost)
	if err != nil {
		return envError(err)
	}
	a.warned = append(a.warned, a.reloadEnvironments(ctx)...)
	if a.global.json {
		return a.writeJSON(envRotateJSON{ID: rec.ID, Name: rec.Name, State: string(rec.State), Code: code, RevokedSerials: revoked, Warnings: a.warnings()})
	}
	if len(revoked) > 0 {
		fmt.Fprintf(a.stdout, "Revoked the certificate of %s; it needs the new code.\n", rec.Name)
	}
	a.printEnrollment(rec.Name, code)
	a.printWarnings()
	return nil
}

// runEnvRemove revokes and deletes environment name.
func (a *App) runEnvRemove(ctx context.Context, name string) error {
	rec, revoked, err := a.envStore().Remove(ctx, name)
	if err != nil {
		return envError(err)
	}
	a.warned = append(a.warned, a.reloadEnvironments(ctx)...)
	if a.global.json {
		return a.writeJSON(envRemoveJSON{ID: rec.ID, Name: rec.Name, RevokedSerials: revoked, Warnings: a.warnings()})
	}
	if len(revoked) > 0 {
		fmt.Fprintf(a.stdout, "Removed %s (%s); its certificate is revoked.\n", rec.Name, rec.ID)
	} else {
		fmt.Fprintf(a.stdout, "Removed %s (%s).\n", rec.Name, rec.ID)
	}
	a.printWarnings()
	return nil
}

// enrollHost is where a new code sends its agent: public_name on the
// enrollment listener's port.
func (a *App) enrollHost() (string, error) {
	host, err := a.config.Environments.EnrollHost()
	if err != nil {
		return "", envError(err)
	}
	return host, nil
}

// printEnrollment is the block that tells a person how to enroll name.
func (a *App) printEnrollment(name, code string) {
	fmt.Fprintf(a.stdout, "Code (works once, shown once):\n  %s\n\nOn %s:\n  ws env agent enroll %s\n  ws env agent run            # or install the service: see docs/tools/ws-env.md\n", code, name, code)
}

// envError maps the environment package's errors to error codes. Anything
// else is returned as it is, so it is internal.
func envError(err error) error {
	switch {
	case errors.Is(err, environment.ErrUnknown):
		return newError(codeEnvironmentUnknown, err)
	case errors.Is(err, environment.ErrExists):
		return newError(codeEnvironmentExists, err)
	case errors.Is(err, environment.ErrNameInvalid):
		return newError(codeNameInvalid, err)
	case errors.Is(err, environment.ErrCAInUse):
		return newError(codeCAInUse, err)
	case errors.Is(err, environment.ErrNoCA), errors.Is(err, environment.ErrNoRotation), errors.Is(err, config.ErrNoPublicName):
		return newError(codeUsage, err)
	case errors.Is(err, environment.ErrEnrollmentUsed):
		return newError(codeEnrollmentUsed, err)
	case errors.Is(err, environment.ErrEnrollmentFailed):
		return newError(codeEnrollmentFailed, err)
	}
	return err
}

// envListJSON is ws env list's output.
type envListJSON struct {
	Environments []envJSON `json:"environments"`
	Warnings     []string  `json:"warnings"`
}

// envJSON is one environment in ws env list (docs/tools/ws-env.md).
type envJSON struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	State        environment.State        `json:"state"`
	Status       string                   `json:"status"`
	LastSeen     *string                  `json:"last_seen"`
	CreatedAt    *string                  `json:"created_at"`
	EnrolledAt   *string                  `json:"enrolled_at"`
	CertNotAfter *string                  `json:"cert_not_after"`
	CertCA       *string                  `json:"cert_ca"`
	Hello        *daemon.EnvironmentHello `json:"hello"`
	Workspaces   *int                     `json:"workspaces"`
}

// runEnvList lists the environments with what the running daemon has
// heard from each.
func (a *App) runEnvList(ctx context.Context) error {
	recs, err := a.envStore().List()
	if err != nil {
		return envError(err)
	}
	ca, err := a.envStore().CAStatus(a.config.Environments.RenewDays())
	if err != nil {
		return envError(err)
	}
	presence := a.envPresence(ctx)
	envs := make([]envJSON, 0, len(recs))
	for i := range recs {
		envs = append(envs, a.envEntry(&recs[i], presence, ca))
	}
	if a.global.json {
		return a.writeJSON(envListJSON{Environments: envs, Warnings: a.warnings()})
	}
	a.printEnvTable(envs)
	a.printWarnings()
	return nil
}

// envPresence is the running daemon's presence of each environment, by id.
// Without an answering daemon it is empty, and a warning says why.
func (a *App) envPresence(ctx context.Context) map[string]daemon.EnvironmentPresence {
	st, err := daemon.Dial(a.daemonPaths()).Status(ctx)
	switch {
	case errors.Is(err, daemon.ErrNotRunning):
		a.warned = append(a.warned, noDaemonWarning)
		return nil
	case err != nil:
		a.warned = append(a.warned, fmt.Sprintf("the ws daemon didn't report its environments: %v", err))
		return nil
	}
	out := map[string]daemon.EnvironmentPresence{}
	if st.Environments == nil {
		return out
	}
	for _, p := range st.Environments.Environments {
		out[p.ID] = p
	}
	return out
}

// envEntry is one environment as ws env list shows it. Status is unknown
// unless the daemon reported on it.
func (a *App) envEntry(rec *environment.Record, presence map[string]daemon.EnvironmentPresence, ca environment.CAStatus) envJSON {
	out := envJSON{
		ID:           rec.ID,
		Name:         rec.Name,
		State:        rec.State,
		Status:       "unknown",
		CreatedAt:    optionalString(rec.CreatedAt),
		EnrolledAt:   optionalString(rec.EnrolledAt),
		CertNotAfter: optionalString(rec.CertNotAfter),
		CertCA:       certCA(rec, ca),
		Workspaces:   a.snapshotCount(rec.Name),
	}
	if p, ok := presence[rec.ID]; ok {
		out.Status = p.Status
		out.LastSeen = timeOrNil(p.LastSeen)
		out.Hello = p.Hello
	}
	return out
}

// snapshotCount is how many workspaces name's stored snapshot lists, or nil
// without a snapshot.
func (a *App) snapshotCount(name string) *int {
	_, snap, err := a.envStore().Snapshot(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		a.warned = append(a.warned, err.Error())
		return nil
	}
	count, err := snapshotWorkspaces(snap)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("reading the snapshot of %s: %v", name, err))
		return nil
	}
	return &count
}

// snapshotWorkspaces counts the entries in a snapshot's workspaces array.
// A snapshot with no workspaces key fails to parse rather than counting as zero.
func snapshotWorkspaces(snap envproto.Snapshot) (int, error) {
	var workspaces []json.RawMessage
	if err := json.Unmarshal(snap.Workspaces, &workspaces); err != nil {
		return 0, err
	}
	return len(workspaces), nil
}

// certCA names the CA that signed rec's certificate, "current" or "old", or
// is nil when it isn't one of them.
func certCA(rec *environment.Record, ca environment.CAStatus) *string {
	var label string
	switch {
	case rec.CertCA == "":
		return nil
	case ca.Current != nil && rec.CertCA == ca.Current.Fingerprint:
		label = "current"
	case ca.Old != nil && rec.CertCA == ca.Old.Fingerprint:
		label = "old"
	default:
		return nil
	}
	return &label
}

// printEnvTable prints the environments as a table.
func (a *App) printEnvTable(envs []envJSON) {
	if len(envs) == 0 {
		fmt.Fprintln(a.stdout, "no environments")
		return
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tWS\tPLATFORM\tCLAUDE\tWORKSPACES\tLAST SEEN")
	for i := range envs {
		e := &envs[i]
		ws, platform, claude := helloColumns(e.Hello)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, e.Status, ws, platform, claude, countColumn(e.Workspaces), dashIfNil(e.LastSeen))
	}
	tw.Flush()
}

// helloColumns are the WS, PLATFORM and CLAUDE columns for h, or "-" each
// without a hello.
func helloColumns(h *daemon.EnvironmentHello) (ws, platform, claude string) {
	if h == nil {
		return "-", "-", "-"
	}
	claude = h.Claude
	if h.ClaudeVersion != nil {
		claude += " " + *h.ClaudeVersion
	}
	return h.WSVersion, h.OS + "/" + h.Arch, claude
}

func countColumn(n *int) string {
	if n == nil {
		return "-"
	}
	return strconv.Itoa(*n)
}

func dashIfNil(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// optionalString is s, or nil when it is empty.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timeOrNil is t in RFC 3339, or nil.
func timeOrNil(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}

// strandedWarning says which environments still hold a dropped CA's
// certificates and must be rotated.
func strandedWarning(names []string) string {
	return fmt.Sprintf("%s still hold certificates from a dropped CA and need ws env rotate: %s", plural(len(names), "environment"), strings.Join(names, ", "))
}
