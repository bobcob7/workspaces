package cli

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
)

// newDaemonTestApp is a test App whose config directory is a temporary one,
// and whose socket lives in a short temporary runtime directory.
func newDaemonTestApp(t *testing.T) (app *App, stdout, stderr *bytes.Buffer) {
	t.Helper()
	app, stdout, stderr = newTestApp()
	app.paths = config.NewPaths(t.TempDir())
	app.version = "v0.31.0"
	//nolint:usetesting // socket paths are limited to 108 bytes; t.TempDir's names are too long.
	runtimeDir, err := os.MkdirTemp("", "wsd")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	app.environ = []string{"XDG_RUNTIME_DIR=" + runtimeDir}
	return app, stdout, stderr
}

// serveDaemon runs an in-process managed daemon for app's config directory,
// reporting version as its ws version, until the test ends.
func serveDaemon(t *testing.T, app *App, version string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(daemon.WithVersion(t.Context(), version))
	t.Cleanup(cancel)
	done := make(chan error, 1)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	go func() {
		done <- daemon.Serve(ctx, app.daemonPaths(), daemon.OriginManaged, daemon.NopSupervisor{}, daemon.NopEnvironments{}, log)
	}()
	require.Eventually(t, func() bool {
		_, err := daemon.Dial(app.daemonPaths()).Status(ctx)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return done
}

func decodeJSONObject(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	var obj map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &obj))
	return obj
}

func findSubcommand(t *testing.T, cmd *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, sub := range cmd.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	require.FailNow(t, "no subcommand "+name)
	return nil
}

func TestApp_newDaemonCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newDaemonCmd()
	t.Run("has run, status, and stop", func(t *testing.T) {
		names := []string{}
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		assert.ElementsMatch(t, []string{"run", "status", "stop"}, names)
	})
	t.Run("run refuses --json", func(t *testing.T) {
		app.global.json = true
		t.Cleanup(func() { app.global.json = false })
		run := findSubcommand(t, cmd, "run")
		requireUsageError(t, run.PreRunE(run, nil))
	})
	t.Run("run's --origin defaults to managed", func(t *testing.T) {
		assertFlag(t, findSubcommand(t, cmd, "run"), "origin", daemon.OriginManaged)
	})
	t.Run("stop's --keep-runners defaults to off", func(t *testing.T) {
		assertFlag(t, findSubcommand(t, cmd, "stop"), "keep-runners", "false")
	})
}

func TestApp_runDaemonRun(t *testing.T) {
	t.Run("refuses an origin that isn't transient, managed, or app", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		requireCode(t, codeUsage, app.runDaemonRun(t.Context(), "bogus"))
	})
}

func TestDaemonRunError(t *testing.T) {
	t.Run("a daemon that already runs is daemon_running with its pid", func(t *testing.T) {
		err := daemonRunError(&daemon.RunningError{PID: 4242})
		requireCode(t, codeDaemonRunning, err)
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, 4242, ce.details["pid"])
		assert.Equal(t, exitRefused, ce.exitCode())
	})
	t.Run("other errors pass through", func(t *testing.T) {
		boom := errors.New("boom")
		assert.Equal(t, boom, daemonRunError(boom))
	})
}

func TestApp_runDaemonStatus(t *testing.T) {
	t.Run("not running is a result with running false, and exit 0", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		app.global.json = true
		require.NoError(t, app.runDaemonStatus(t.Context()))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, false, obj["running"])
		assert.Nil(t, obj["pid"])
		assert.Equal(t, app.paths.Root, obj["config_dir"])
	})
	t.Run("a running daemon reports its pid in text", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		serveDaemon(t, app, "v0.31.0")
		require.NoError(t, app.runDaemonStatus(t.Context()))
		assert.Contains(t, stdout.String(), "running: pid ")
		assert.Contains(t, stdout.String(), "ws v0.31.0")
	})
	t.Run("a daemon older than this ws is a warning", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		serveDaemon(t, app, "v0.30.0")
		app.global.json = true
		require.NoError(t, app.runDaemonStatus(t.Context()))
		obj := decodeJSONObject(t, stdout)
		warnings, ok := obj["warnings"].([]any)
		require.True(t, ok)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "run ws daemon stop --keep-runners")
	})
}

func TestApp_runDaemonStop(t *testing.T) {
	t.Run("not running is stopped false, and exit 0", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		app.global.json = true
		require.NoError(t, app.runDaemonStop(t.Context(), false))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, false, obj["stopped"])
		assert.Nil(t, obj["pid"])
	})
	t.Run("a running daemon is stopped, and the command waits for it to exit", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		done := serveDaemon(t, app, "v0.31.0")
		require.NoError(t, app.runDaemonStop(t.Context(), false))
		assert.Contains(t, stdout.String(), "stopped the daemon (pid ")
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("the daemon didn't exit")
		}
	})
}

func TestDaemonUnavailable(t *testing.T) {
	paths := daemon.PathsFor("/cfg", func(string) string { return "" }, 1000)
	err := daemonUnavailable(errors.New("no answer"), paths)
	requireCode(t, codeDaemonUnavailable, err)
	ce, ok := errors.AsType[*cliError](err)
	require.True(t, ok)
	assert.Equal(t, paths.Socket, ce.details["socket"])
	assert.Equal(t, paths.Log, ce.details["log"])
	assert.Equal(t, exitFailed, ce.exitCode())
}

func TestApp_daemonVersionWarnings(t *testing.T) {
	app, _, _ := newTestApp()
	app.version = "v0.31.0"
	older := "v0.30.0"
	newer := "v0.32.0"
	t.Run("warns about a running daemon older than this ws", func(t *testing.T) {
		assert.Len(t, app.daemonVersionWarnings(daemon.Status{Running: true, WSVersion: &older}), 1)
	})
	t.Run("says nothing about a daemon as new as this ws", func(t *testing.T) {
		assert.Empty(t, app.daemonVersionWarnings(daemon.Status{Running: true, WSVersion: &newer}))
	})
	t.Run("says nothing when nothing runs", func(t *testing.T) {
		assert.Empty(t, app.daemonVersionWarnings(daemon.Status{Running: false, WSVersion: &older}))
	})
}

func TestEnvironValue(t *testing.T) {
	environ := []string{"A=1", "XDG_RUNTIME_DIR=/run/user/1000", "B=x=y"}
	t.Run("returns the value of a set variable", func(t *testing.T) {
		assert.Equal(t, "/run/user/1000", environValue(environ, "XDG_RUNTIME_DIR"))
	})
	t.Run("keeps = inside a value", func(t *testing.T) {
		assert.Equal(t, "x=y", environValue(environ, "B"))
	})
	t.Run("returns empty for an unset variable", func(t *testing.T) {
		assert.Empty(t, environValue(environ, "TMPDIR"))
	})
}

func TestEnvironmentsSummary(t *testing.T) {
	t.Run("off when the listeners are closed", func(t *testing.T) {
		assert.Equal(t, "environments: off", environmentsSummary(daemon.EnvironmentsStatus{}))
	})
	t.Run("counts the environments by status", func(t *testing.T) {
		env := daemon.EnvironmentsStatus{
			Listening: true, Listen: "0.0.0.0:8883", EnrollListen: "0.0.0.0:8884",
			Environments: []daemon.EnvironmentPresence{
				{Status: "online"}, {Status: "offline"}, {Status: "pending"},
			},
		}
		assert.Equal(t, "environments: listening on 0.0.0.0:8883, enroll on 0.0.0.0:8884; 1 online, 1 offline, 1 pending", environmentsSummary(env))
	})
}
