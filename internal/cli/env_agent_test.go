package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/exectest"
	"github.com/bobcob7/workspaces/internal/gate"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/mqttclient"
)

// startEnvController starts an in-process controller on broker, with its
// enrollment listener on loopback and one pending environment called
// linux-server, and returns the controller and that environment's code.
func startEnvController(t *testing.T, app *App, broker *envtest.Broker) (*environment.Controller, string) {
	t.Helper()
	store := environment.NewStore(app.paths.Environments, app.paths.State, time.Now, rand.Reader)
	ctrl := environment.NewController(discardDaemonLog(), store, broker, environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	_, _, _, err := store.Add(t.Context(), "linux-server", "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, ctrl.Start(t.Context()))
	_, code, _, err := store.Rotate(t.Context(), "linux-server", ctrl.Status().EnrollListen)
	require.NoError(t, err)
	return ctrl, code
}

// enrollAgentTestMachine enrolls app's machine with a code from a new
// controller on broker, and returns the controller.
func enrollAgentTestMachine(t *testing.T, app *App, broker *envtest.Broker) *environment.Controller {
	t.Helper()
	ctrl, code := startEnvController(t, app, broker)
	require.NoError(t, app.runEnvAgentEnroll(t.Context(), code))
	return ctrl
}

// isOnline reports whether ctrl has heard an online hello from any environment.
func isOnline(ctrl *environment.Controller) bool {
	for _, p := range ctrl.Status().Environments {
		if p.Status == "online" {
			return true
		}
	}
	return false
}

// agentErrorCode is the error code of err, or "" when err has none.
func agentErrorCode(err error) errorCode {
	ce, ok := errors.AsType[*cliError](err)
	if !ok {
		return ""
	}
	return ce.code
}

func TestApp_newEnvAgentCmd(t *testing.T) {
	t.Run("is hidden under env, with its four subcommands", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		agent, _, err := app.newEnvCmd().Find([]string{"agent"})
		require.NoError(t, err)
		assert.True(t, agent.Hidden)
		names := []string{}
		for _, c := range agent.Commands() {
			names = append(names, c.Name())
		}
		assert.ElementsMatch(t, []string{"enroll", "run", "status", "unenroll"}, names)
	})
}

func TestApp_runEnvAgentEnroll(t *testing.T) {
	t.Run("a malformed code is usage and writes nothing", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		err := app.runEnvAgentEnroll(t.Context(), "not-a-code")
		assert.Equal(t, codeUsage, agentErrorCode(err))
		_, loadErr := envagent.NewFiles(app.paths.Environment).Load()
		assert.ErrorIs(t, loadErr, envagent.ErrNotEnrolled)
	})
	t.Run("enrolls with a code the controller issued and prints the next step", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		id, err := envagent.NewFiles(app.paths.Environment).Load()
		require.NoError(t, err)
		assert.Equal(t, "linux-server", id.Name)
		assert.Contains(t, stdout.String(), "Enrolled as linux-server (env-")
		assert.Contains(t, stdout.String(), "Start it with: ws env agent run")
	})
	t.Run("json has the identity and the CA it trusts", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		app.global.json = true
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		var out envAgentEnrollJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
		assert.Equal(t, "linux-server", out.Name)
		assert.NotEmpty(t, out.Broker)
		assert.Len(t, out.CAFingerprints, 1)
		assert.Empty(t, out.Warnings)
	})
	t.Run("enrolling again is environment_exists", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		err := app.runEnvAgentEnroll(t.Context(), "ws-enroll-v1.unused")
		assert.Equal(t, codeEnvironmentExists, agentErrorCode(err))
	})
	t.Run("a used code on a fresh machine is enrollment_used", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		_, code := startEnvController(t, app, envtest.NewBroker())
		require.NoError(t, app.runEnvAgentEnroll(t.Context(), code))
		app.paths.Environment = filepath.Join(t.TempDir(), "environment")
		err := app.runEnvAgentEnroll(t.Context(), code)
		assert.Equal(t, codeEnrollmentUsed, agentErrorCode(err))
	})
}

func TestApp_runEnvAgentRun(t *testing.T) {
	t.Run("the default client is the paho adapter", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		client, err := app.newEnvClient(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		assert.IsType(t, &mqttclient.Client{}, client)
	})
	t.Run("connects, the controller sees it online, and cancelling exits 0", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		broker := envtest.NewBroker()
		ctrl := enrollAgentTestMachine(t, app, broker)
		app.newEnvClient = func(*slog.Logger) (envClient, error) { return broker.NewClient(), nil }
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- app.runEnvAgentRun(ctx) }()
		require.Eventually(t, func() bool { return isOnline(ctrl) }, 10*time.Second, 10*time.Millisecond)
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "the agent didn't stop after its context was cancelled")
		}
	})
}

func TestApp_runEnvAgentStatus(t *testing.T) {
	t.Run("not enrolled is enrolled false with null identity fields", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		app.global.json = true
		require.NoError(t, app.runEnvAgentStatus())
		assert.Contains(t, stdout.String(), `"enrolled":false`)
		assert.Contains(t, stdout.String(), `"id":null`)
		assert.Contains(t, stdout.String(), `"ca_fingerprints":[]`)
	})
	t.Run("enrolled shows the identity in JSON", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		stdout.Reset()
		app.global.json = true
		require.NoError(t, app.runEnvAgentStatus())
		var out envAgentStatusJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
		assert.True(t, out.Enrolled)
		require.NotNil(t, out.Name)
		assert.Equal(t, "linux-server", *out.Name)
		assert.NotNil(t, out.CertSerial)
		assert.NotNil(t, out.RenewAt)
		assert.Len(t, out.CAFingerprints, 1)
	})
	t.Run("enrolled prints the summary line", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		stdout.Reset()
		require.NoError(t, app.runEnvAgentStatus())
		assert.Contains(t, stdout.String(), "linux-server (env-")
		assert.Contains(t, stdout.String(), " → 127.0.0.1:")
		assert.Contains(t, stdout.String(), "; certificate renews from ")
	})
}

func TestApp_runEnvAgentUnenroll(t *testing.T) {
	t.Run("not enrolled is environment_unknown", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		assert.Equal(t, codeEnvironmentUnknown, agentErrorCode(app.runEnvAgentUnenroll()))
	})
	t.Run("removes the identity, and a second unenroll is environment_unknown", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		stdout.Reset()
		require.NoError(t, app.runEnvAgentUnenroll())
		assert.Contains(t, stdout.String(), "Unenrolled linux-server; the controller lists it until ws env rm linux-server.")
		assert.Equal(t, codeEnvironmentUnknown, agentErrorCode(app.runEnvAgentUnenroll()))
	})
	t.Run("json names the environment", func(t *testing.T) {
		app, stdout, _ := newEnvTestApp(t)
		enrollAgentTestMachine(t, app, envtest.NewBroker())
		stdout.Reset()
		app.global.json = true
		require.NoError(t, app.runEnvAgentUnenroll())
		var out envAgentUnenrollJSON
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
		assert.True(t, out.Unenrolled)
		assert.Equal(t, "linux-server", out.Name)
	})
}

func TestAgentError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want errorCode
	}{
		{"malformed code", envproto.ErrCodeInvalid, codeUsage},
		{"already enrolled", envagent.ErrAlreadyEnrolled, codeEnvironmentExists},
		{"not enrolled", envagent.ErrNotEnrolled, codeEnvironmentUnknown},
		{"code used", envagent.ErrEnrollmentUsed, codeEnrollmentUsed},
		{"pin mismatch", envagent.ErrPinMismatch, codeEnrollmentFailed},
		{"enrollment refused", envagent.ErrEnrollmentFailed, codeEnrollmentFailed},
		{"certificate expired", envagent.ErrCertificateExpired, codeEnrollmentFailed},
		{"network failure", &url.Error{Op: "Post", URL: "https://ws.example.com:8884/enroll", Err: errTest}, codeEnrollmentFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name+" maps to "+string(tc.want), func(t *testing.T) {
			assert.Equal(t, tc.want, agentErrorCode(agentError(tc.err)))
		})
	}
	t.Run("a network failure keeps its cause", func(t *testing.T) {
		ce, ok := errors.AsType[*cliError](agentError(&url.Error{Op: "Post", URL: "https://x", Err: errTest}))
		require.True(t, ok)
		assert.Equal(t, map[string]any{"cause": errTest.Error()}, ce.details)
	})
	t.Run("other errors are returned as they are", func(t *testing.T) {
		require.ErrorIs(t, agentError(errTest), errTest)
		assert.Empty(t, agentErrorCode(agentError(errTest)))
	})
}

func TestAgentClaude_Probe(t *testing.T) {
	t.Run("claude missing", func(t *testing.T) {
		probe := agentClaude{claude: claude.New(discardDaemonLog(), filepath.Join(t.TempDir(), "claude"), nil)}
		state, version := probe.Probe(t.Context())
		assert.Equal(t, envproto.ClaudeMissing, state)
		assert.Nil(t, version)
	})
	t.Run("claude present with its version", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "claude")
		exectest.WriteScript(t, path, "#!/bin/sh\necho '2.1.303 (Claude Code)'\n")
		probe := agentClaude{claude: claude.New(discardDaemonLog(), path, nil)}
		state, version := probe.Probe(t.Context())
		assert.Equal(t, envproto.ClaudePresent, state)
		require.NotNil(t, version)
		assert.Equal(t, "2.1.303", *version)
	})
	t.Run("claude present without a version it can report", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "claude")
		exectest.WriteScript(t, path, "#!/bin/sh\necho banana\n")
		probe := agentClaude{claude: claude.New(discardDaemonLog(), path, nil)}
		state, version := probe.Probe(t.Context())
		assert.Equal(t, envproto.ClaudePresent, state)
		assert.Nil(t, version)
	})
}

func TestAgentSnapshots_Workspaces(t *testing.T) {
	t.Run("no workspaces is an empty array", func(t *testing.T) {
		app, _, _ := newEnvTestApp(t)
		data, err := agentSnapshots{app: app}.Workspaces(t.Context())
		require.NoError(t, err)
		assert.JSONEq(t, "[]", string(data))
	})
	t.Run("a hidden workspace is included and an unknown directory isn't", func(t *testing.T) {
		hidden := listedWorkspace("quiet")
		hidden.Hidden = true
		e := newListApp([]lifetime.Entry{{Workspace: hidden}}, []string{"/cfg/workspaces/stray"})
		data, err := agentSnapshots{app: e.app}.Workspaces(t.Context())
		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":"quiet"`)
		assert.NotContains(t, string(data), "stray")
	})
}

func TestAgentSnapshots_Loops(t *testing.T) {
	t.Run("maps a loop without its prompt, script or vars", func(t *testing.T) {
		app := newMockedApp().app
		store := withLoopStore(t, app)
		app.loops = store
		l := loop.Loop{Name: "digest", Title: "Daily digest", Every: "1h", Prompt: "secret prompt", Script: "echo secret-script", Vars: map[string]string{"token": "secret-var"}, Keep: 5, CreatedAt: testNow().Add(-time.Hour), NextRunAt: testNow().Add(time.Hour)}
		require.NoError(t, store.Create(&l))
		loops, err := agentSnapshots{app: app}.Loops(t.Context())
		require.NoError(t, err)
		require.Len(t, loops, 1)
		assert.Equal(t, "digest", loops[0].Name)
		assert.Equal(t, "Daily digest", loops[0].Title)
		assert.Equal(t, "1h", loops[0].Every)
		require.NotNil(t, loops[0].NextRunAt)
		data, err := json.Marshal(loops)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "secret")
		assert.NotContains(t, string(data), "prompt")
		assert.NotContains(t, string(data), "script")
		assert.NotContains(t, string(data), "vars")
	})
	t.Run("no loops is []", func(t *testing.T) {
		app := newMockedApp().app
		app.loops = withLoopStore(t, app)
		loops, err := agentSnapshots{app: app}.Loops(t.Context())
		require.NoError(t, err)
		data, err := json.Marshal(loops)
		require.NoError(t, err)
		assert.JSONEq(t, "[]", string(data))
	})
	t.Run("keeps 100 by name", func(t *testing.T) {
		app := newMockedApp().app
		store := withLoopStore(t, app)
		app.loops = store
		for i := range envproto.MaxSnapshotLoops + 50 {
			l := loop.Loop{Name: fmt.Sprintf("loop-%03d", i), Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: testNow().Add(-time.Hour), NextRunAt: testNow().Add(time.Hour)}
			require.NoError(t, store.Create(&l))
		}
		loops, err := agentSnapshots{app: app}.Loops(t.Context())
		require.NoError(t, err)
		require.Len(t, loops, envproto.MaxSnapshotLoops)
		assert.Equal(t, "loop-000", loops[0].Name)
		assert.Equal(t, fmt.Sprintf("loop-%03d", envproto.MaxSnapshotLoops-1), loops[len(loops)-1].Name)
	})
}

func Test_agentOptions(t *testing.T) {
	opts := agentOptions("v1", 30, "/ws")
	assert.Equal(t, 16384, opts.NotSentLowWater)
	assert.Equal(t, "/ws", opts.WorkspacesRoot)
	assert.Equal(t, "v1", opts.Version)
}

func TestAgentLoops_Tick(t *testing.T) {
	t.Run("started is true when an item started", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		started, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.True(t, started)
	})
	t.Run("started is true when an item failed", func(t *testing.T) {
		e := newTickEnv(t)
		e.addDueLoop(t)
		e.claude.CheckFunc = func(context.Context) error { return errors.New("claude is not trusted") }
		started, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.True(t, started)
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("false when nothing is due", func(t *testing.T) {
		e := newTickEnv(t)
		started, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.False(t, started)
	})
	t.Run("false when every loop is paused or waiting", func(t *testing.T) {
		e := newTickEnv(t)
		paused := e.addDueLoop(t)
		paused.Paused = true
		require.NoError(t, e.loops.Save(&paused))
		later := loop.Loop{Name: "later", Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: testNow().Add(-2 * time.Hour), NextRunAt: testNow().Add(time.Hour)}
		require.NoError(t, e.loops.Create(&later))
		started, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.False(t, started)
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("false when a script skips its loop", func(t *testing.T) {
		e := newTickEnv(t)
		e.addGatedLoop(t, "Do it", nil)
		e.app.gate = &fakeGate{result: gate.Result{ExitCode: 1}}
		started, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.False(t, started)
		assert.Empty(t, e.claude.StartCalls())
	})
	t.Run("starts two of three due loops and leaves the third waiting", func(t *testing.T) {
		e := newTickEnv(t)
		addDueLoopsNamed(t, e, "alpha", "bravo", "charlie")
		started, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.True(t, started)
		assert.Len(t, e.claude.StartCalls(), 2)
		var waiting []string
		for _, name := range []string{"alpha", "bravo", "charlie"} {
			l, err := e.loops.Get(name)
			require.NoError(t, err)
			if len(l.Runs) == 0 {
				waiting = append(waiting, name)
				assert.True(t, l.NextRunAt.Equal(testNow().Add(-time.Minute)), "next_run_at of %s", name)
			}
		}
		assert.Len(t, waiting, 1)
	})
	t.Run("runs the pass in the root given, quietly", func(t *testing.T) {
		e := newTickEnv(t)
		var configDirs []string
		e.app.configs = &configLoaderMock{LoadFunc: func(configDir string) (config.Paths, config.Config, error) {
			configDirs = append(configDirs, configDir)
			return testPaths(), testConfig(), nil
		}}
		_, err := agentLoops{newApp: e.writersApp, root: "/cfg"}.Tick(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"/cfg"}, configDirs)
		assert.True(t, e.app.global.quiet)
	})
	t.Run("a pass that can't load its config is an error with stderr", func(t *testing.T) {
		e := newTickEnv(t)
		e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
			return config.Paths{}, config.Config{}, errors.New("no such root")
		}}
		started, err := agentLoops{newApp: e.writersApp, root: "/nowhere"}.Tick(t.Context())
		assert.False(t, started)
		assert.ErrorContains(t, err, "no such root")
	})
	t.Run("a pass whose stdout isn't a loop listing is an error", func(t *testing.T) {
		e := newTickEnv(t)
		newApp := func(stdout, stderr io.Writer) *App {
			fmt.Fprint(stdout, "[]")
			e.app.stdout = io.Discard
			e.app.stderr = stderr
			return e.app
		}
		started, err := agentLoops{newApp: newApp, root: "/cfg"}.Tick(t.Context())
		assert.False(t, started)
		assert.ErrorContains(t, err, "reading the loop pass")
	})
}
