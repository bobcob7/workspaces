package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
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
		PublicName:      env.PublicName,
		RenewBeforeDays: env.RenewDays(),
	}
	return envHost{ctrl: environment.NewController(log, a.envStore(), broker, settings, a.now)}
}

// envHost adapts *environment.Controller to daemon.EnvironmentHost.
type envHost struct{ ctrl *environment.Controller }

// Start listens for environments.
func (h envHost) Start(ctx context.Context) error { return h.ctrl.Start(ctx) }

// Reload rereads the environments' state.
func (h envHost) Reload(ctx context.Context) error { return h.ctrl.Reload(ctx) }

// Status is the controller's listeners and environments.
func (h envHost) Status() daemon.EnvironmentsStatus { return environmentsStatus(h.ctrl.Status()) }

// Warnings are the controller's warnings.
func (h envHost) Warnings() []string { return h.ctrl.Warnings() }

// Close stops the listeners.
func (h envHost) Close() error { return h.ctrl.Close() }

// environmentsStatus is the daemon's view of st.
func environmentsStatus(st environment.Status) daemon.EnvironmentsStatus {
	out := daemon.EnvironmentsStatus{
		Listening:    st.Listening,
		Listen:       st.Listen,
		EnrollListen: st.EnrollListen,
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
