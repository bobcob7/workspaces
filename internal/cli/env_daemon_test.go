package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/mqttbroker"
)

func discardDaemonLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestApp_daemonEnvironments(t *testing.T) {
	t.Run("is NopEnvironments with no reason when [environments] has no public_name", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		assert.Equal(t, daemon.NopEnvironments{}, app.daemonEnvironments(discardDaemonLog()))
	})
	t.Run("the default broker is the mochi adapter, so the host isn't NopEnvironments", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		app.config.Environments = config.Environments{PublicName: "127.0.0.1"}
		broker, err := app.newEnvBroker(discardDaemonLog())
		require.NoError(t, err)
		assert.IsType(t, &mqttbroker.Broker{}, broker)
		assert.NotEqual(t, daemon.NopEnvironments{}, app.daemonEnvironments(discardDaemonLog()))
	})
	t.Run("with a broker and a CA the daemon's status shows the listeners", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		app.newEnvBroker = func(*slog.Logger) (envBroker, error) { return envtest.NewBroker(), nil }
		app.config.Environments = config.Environments{Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", PublicName: "127.0.0.1"}
		_, _, _, err := app.envStore().Add(t.Context(), "linux", "127.0.0.1:8884")
		require.NoError(t, err)
		serveEnvDaemon(t, app)
		require.NoError(t, app.runDaemonStatus(t.Context()))
		assert.Contains(t, stdout.String(), "environments: listening on 127.0.0.1:")
		assert.Contains(t, stdout.String(), "1 pending")
		stdout.Reset()
		app.global.json = true
		require.NoError(t, app.runDaemonStatus(t.Context()))
		obj := decodeJSONObject(t, stdout)
		envs, ok := obj["environments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, true, envs["listening"])
		assert.Len(t, envs["environments"], 1)
	})
	t.Run("with the default broker and a CA the daemon reports listening", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		app.config.Environments = config.Environments{Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", PublicName: "127.0.0.1"}
		_, _, _, err := app.envStore().Add(t.Context(), "linux", "127.0.0.1:8884")
		require.NoError(t, err)
		serveEnvDaemon(t, app)
		app.global.json = true
		require.NoError(t, app.runDaemonStatus(t.Context()))
		envs, ok := decodeJSONObject(t, stdout)["environments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, true, envs["listening"])
	})
}

func TestApp_daemonEnvironments_artifactAPI(t *testing.T) {
	t.Run("serves the artifact API with the app's handler to an enrolled agent", func(t *testing.T) {
		ctl := newArtifactApp(t)
		ctl.app.newEnvBroker = func(*slog.Logger) (envBroker, error) { return envtest.NewBroker(), nil }
		ctl.app.config.Environments = config.Environments{
			Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", APIListen: new("127.0.0.1:0"), PublicName: "127.0.0.1",
		}
		host, ok := ctl.app.daemonEnvironments(discardDaemonLog()).(envHost)
		require.True(t, ok)
		t.Cleanup(func() { _ = host.Close() })
		_, _, _, err := ctl.app.envStore().Add(t.Context(), "linux-server", "127.0.0.1:1")
		require.NoError(t, err)
		require.NoError(t, host.Start(t.Context()))
		_, code, _, err := ctl.app.envStore().Rotate(t.Context(), "linux-server", host.ctrl.Status().EnrollListen)
		require.NoError(t, err)
		agent, _ := newMachineArtifactApp(t)
		require.NoError(t, agent.app.runEnvAgentEnroll(t.Context(), code))
		files := envagent.NewFiles(agent.app.paths.Environment)
		require.NoError(t, files.SaveAPIEndpoint(host.ctrl.APIEndpoint(), time.Now()))
		client, err := envagent.NewAPIClient(files)
		require.NoError(t, err)
		resp, err := client.Do(t.Context(), http.MethodGet, envproto.ArtifactsPath, nil, nil, nil, 0)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.NotEqual(t, http.StatusNotFound, resp.StatusCode, "the controller has no artifact handler")
	})
	t.Run("starts no API listener when api_listen is off", func(t *testing.T) {
		for _, value := range []string{"off", ""} {
			app, _, _ := newDaemonTestApp(t)
			app.newEnvBroker = func(*slog.Logger) (envBroker, error) { return envtest.NewBroker(), nil }
			app.config.Environments = config.Environments{
				Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", APIListen: new(value), PublicName: "127.0.0.1",
			}
			host, ok := app.daemonEnvironments(discardDaemonLog()).(envHost)
			require.True(t, ok)
			_, _, _, err := app.envStore().Add(t.Context(), "linux", "127.0.0.1:8884")
			require.NoError(t, err)
			require.NoError(t, host.Start(t.Context()))
			assert.True(t, host.ctrl.Status().Listening, value)
			assert.Empty(t, host.ctrl.Status().APIListen, value)
			assert.Empty(t, host.ctrl.APIEndpoint(), value)
			require.NoError(t, host.Close())
		}
	})
}

// serveEnvDaemon runs an in-process managed daemon with app's environments
// until the test ends, as ws daemon run does.
func serveEnvDaemon(t *testing.T, app *App) {
	t.Helper()
	serveDaemonWith(t, app, app.daemonEnvironments(discardDaemonLog()))
}

// reloadRecorder is an environment host that counts reload requests.
type reloadRecorder struct {
	daemon.NopEnvironments
	reloads atomic.Int32
}

func (r *reloadRecorder) Reload(context.Context) error {
	r.reloads.Add(1)
	return nil
}

// Call is offline: the recorder serves no environment.
func (*reloadRecorder) Call(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
	return daemon.CallReply{}, daemon.ErrEnvironmentOffline
}

// serveRecordingEnvDaemon runs an in-process managed daemon whose host
// records the reload requests, and returns the host.
func serveRecordingEnvDaemon(t *testing.T, app *App) *reloadRecorder {
	t.Helper()
	host := &reloadRecorder{}
	serveDaemonWith(t, app, host)
	return host
}

// serveDaemonWith runs an in-process managed daemon serving host until the
// test ends.
func serveDaemonWith(t *testing.T, app *App, host daemon.EnvironmentHost) {
	t.Helper()
	ctx, cancel := context.WithCancel(daemon.WithVersion(t.Context(), app.version))
	t.Cleanup(cancel)
	log := discardDaemonLog()
	go func() {
		_ = daemon.Serve(ctx, app.daemonPaths(), daemon.OriginManaged, daemon.NopSupervisor{}, host, log)
	}()
	require.Eventually(t, func() bool {
		_, err := daemon.Dial(app.daemonPaths()).Status(ctx)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestApp_reloadEnvironments(t *testing.T) {
	t.Run("with no daemon, a warning says how to start one", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		warnings := app.reloadEnvironments(t.Context())
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "no ws daemon runs for this config directory")
	})
	t.Run("a running daemon reloads with no warning", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		serveDaemon(t, app, "v0.31.0")
		assert.Empty(t, app.reloadEnvironments(t.Context()))
	})
}

func TestEnvironmentsStatus(t *testing.T) {
	t.Run("converts each environment and its hello", func(t *testing.T) {
		seen := time.Date(2026, 11, 22, 14, 2, 0, 0, time.UTC)
		version := "2.4.1"
		st := environmentsStatus(&environment.Status{
			Listening:    true,
			Listen:       "0.0.0.0:8883",
			EnrollListen: "0.0.0.0:8884",
			APIListen:    "0.0.0.0:8885",
			Environments: []environment.Presence{{
				ID: "env-1a2b3c4d", Name: "linux-server", State: environment.StateEnrolled, Status: envproto.StatusOnline,
				LastSeen: &seen,
				Hello: &envproto.Hello{
					WSVersion: "v0.31.0", OS: "linux", Arch: "amd64", Claude: "present", ClaudeVersion: &version,
					CertNotAfter: time.Date(2027, 2, 20, 14, 5, 11, 0, time.UTC), Protocol: 5,
				},
			}},
		})
		assert.True(t, st.Listening)
		assert.Equal(t, "0.0.0.0:8883", st.Listen)
		assert.Equal(t, "0.0.0.0:8885", st.APIListen)
		require.Len(t, st.Environments, 1)
		env := st.Environments[0]
		assert.Equal(t, "enrolled", env.State)
		assert.Equal(t, envproto.StatusOnline, env.Status)
		assert.Equal(t, &seen, env.LastSeen)
		require.NotNil(t, env.Hello)
		assert.Equal(t, "2027-02-20T14:05:11Z", env.Hello.CertNotAfter)
		assert.Equal(t, "2.4.1", *env.Hello.ClaudeVersion)
		assert.Equal(t, 5, env.Hello.Protocol)
	})
	t.Run("no environments is an empty list, and no hello is null", func(t *testing.T) {
		st := environmentsStatus(&environment.Status{})
		require.NotNil(t, st.Environments)
		assert.Empty(t, st.Environments)
		data, err := json.Marshal(daemon.EnvironmentPresence{})
		require.NoError(t, err)
		assert.Contains(t, string(data), `"hello":null`)
		assert.Nil(t, environmentHello(nil))
	})
}

func TestEnvHost_Call(t *testing.T) {
	callOf := func(op string) daemon.CallRequest {
		return daemon.CallRequest{Op: op, Args: []byte("{}"), RequestID: "0123456789abcdef", Deadline: time.Now().Add(20 * time.Second)}
	}
	newHost := func(t *testing.T) (daemon.EnvironmentHost, environment.Record) {
		t.Helper()
		app, _, _ := newDaemonTestApp(t)
		app.newEnvBroker = func(*slog.Logger) (envBroker, error) { return envtest.NewBroker(), nil }
		app.config.Environments = config.Environments{PublicName: "127.0.0.1"}
		rec := enrollTestEnvironment(t, app)
		return app.daemonEnvironments(discardDaemonLog()), rec
	}
	t.Run("a controller that isn't listening is offline, not listening", func(t *testing.T) {
		host, rec := newHost(t)
		_, err := host.Call(t.Context(), rec.Name, callOf(envproto.OpWorkspaceList))
		require.ErrorIs(t, err, daemon.ErrEnvironmentOffline)
		hostErr, ok := errors.AsType[*daemon.HostError](err)
		require.True(t, ok)
		assert.Equal(t, "not_listening", hostErr.Details["reason"])
		assert.Equal(t, rec.Name, hostErr.Details["environment"])
	})
	t.Run("an unknown environment is ErrEnvironmentUnknown", func(t *testing.T) {
		host, _ := newHost(t)
		_, err := host.Call(t.Context(), "nope", callOf(envproto.OpWorkspaceList))
		require.ErrorIs(t, err, daemon.ErrEnvironmentUnknown)
	})
	t.Run("an operation the controller doesn't send is ErrNotForwarded", func(t *testing.T) {
		host, rec := newHost(t)
		_, err := host.Call(t.Context(), rec.Name, callOf(envproto.OpCertRenew))
		require.ErrorIs(t, err, daemon.ErrNotForwarded)
	})
}

func TestCallHostError(t *testing.T) {
	t.Run("an unknown environment is ErrEnvironmentUnknown", func(t *testing.T) {
		err := callHostError(environment.ErrUnknown)
		require.ErrorIs(t, err, daemon.ErrEnvironmentUnknown)
	})
	t.Run("an offline environment carries its offline details", func(t *testing.T) {
		seen := time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)
		offline := &environment.OfflineError{Environment: "linux", Reason: "timeout", LastSeen: &seen, RequestID: "0123456789abcdef", Op: "workspace.list"}
		err := callHostError(offline)
		require.ErrorIs(t, err, daemon.ErrEnvironmentOffline)
		hostErr, ok := errors.AsType[*daemon.HostError](err)
		require.True(t, ok)
		assert.Equal(t, offline.Error(), hostErr.Error())
		assert.Equal(t, offlineDetails(offline), hostErr.Details)
	})
	t.Run("an outdated environment names its versions and the need", func(t *testing.T) {
		outdated := &environment.OutdatedError{Environment: "linux", Op: "workspace.create", WSVersion: "v0.30.0", Protocol: 1, Need: 2}
		err := callHostError(outdated)
		require.ErrorIs(t, err, daemon.ErrEnvironmentOutdated)
		hostErr, ok := errors.AsType[*daemon.HostError](err)
		require.True(t, ok)
		assert.Equal(t, map[string]any{
			"environment":   "linux",
			"op":            "workspace.create",
			"ws_version":    "v0.30.0",
			"protocol":      1,
			"need_protocol": 2,
		}, hostErr.Details)
	})
	t.Run("a refused operation is ErrNotForwarded", func(t *testing.T) {
		err := callHostError(fmt.Errorf("cert.renew: %w", environment.ErrNotForwarded))
		require.ErrorIs(t, err, daemon.ErrNotForwarded)
	})
	t.Run("a pending request id is ErrRequestPending", func(t *testing.T) {
		err := callHostError(fmt.Errorf("%w: %q", environment.ErrRequestPending, "0123456789abcdef"))
		require.ErrorIs(t, err, daemon.ErrRequestPending)
	})
	t.Run("any other error is returned as it is", func(t *testing.T) {
		other := errors.New("broker gone")
		assert.Same(t, other, callHostError(other))
	})
}

func TestOfflineDetails(t *testing.T) {
	t.Run("a never-seen environment has a null last_seen", func(t *testing.T) {
		details := offlineDetails(&environment.OfflineError{Environment: "linux", Reason: "offline", RequestID: "0123456789abcdef", Op: "workspace.list"})
		assert.Equal(t, map[string]any{
			"environment": "linux",
			"reason":      "offline",
			"last_seen":   nil,
			"request_id":  "0123456789abcdef",
			"op":          "workspace.list",
		}, details)
	})
	t.Run("last_seen is RFC 3339 UTC", func(t *testing.T) {
		seen := time.Date(2026, 11, 22, 4, 30, 0, 0, time.FixedZone("CST", -6*60*60))
		details := offlineDetails(&environment.OfflineError{Environment: "linux", Reason: "offline", LastSeen: &seen})
		assert.Equal(t, "2026-11-22T10:30:00Z", details["last_seen"])
	})
}

func TestEnvHost_OpenTerminal(t *testing.T) {
	args := envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24, Specs: []string{}}
	newHost := func(t *testing.T) (daemon.EnvironmentHost, environment.Record) {
		t.Helper()
		app, _, _ := newDaemonTestApp(t)
		app.newEnvBroker = func(*slog.Logger) (envBroker, error) { return envtest.NewBroker(), nil }
		app.config.Environments = config.Environments{PublicName: "127.0.0.1"}
		rec := enrollTestEnvironment(t, app)
		return app.daemonEnvironments(discardDaemonLog()), rec
	}
	t.Run("an unknown environment is a nil stream and ErrEnvironmentUnknown", func(t *testing.T) {
		host, _ := newHost(t)
		stream, _, err := host.OpenTerminal(t.Context(), "nope", args)
		assert.Nil(t, stream)
		isNil := stream == nil
		assert.True(t, isNil, "the stream is a typed nil")
		require.ErrorIs(t, err, daemon.ErrEnvironmentUnknown)
		_, ok := errors.AsType[*daemon.HostError](err)
		assert.True(t, ok)
	})
	t.Run("an environment that isn't listening is a nil stream and ErrEnvironmentOffline", func(t *testing.T) {
		host, rec := newHost(t)
		stream, _, err := host.OpenTerminal(t.Context(), rec.Name, args)
		assert.Nil(t, stream)
		isNil := stream == nil
		assert.True(t, isNil, "the stream is a typed nil")
		require.ErrorIs(t, err, daemon.ErrEnvironmentOffline)
	})
}
