package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const helperConfigEnv = "WS_DAEMON_TEST_CONFIG"

func TestEnsure(t *testing.T) {
	t.Run("autostart off and nothing running is ErrUnavailable", func(t *testing.T) {
		p := testPaths(t)
		_, err := Ensure(t.Context(), p, "/nonexistent/ws", false, "")
		require.ErrorIs(t, err, ErrUnavailable)
	})
	t.Run("a running daemon is returned", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		client, err := Ensure(t.Context(), p, "/nonexistent/ws", true, "")
		require.NoError(t, err)
		assert.NotNil(t, client)
	})
	t.Run("a daemon older than minVersion is ErrUnavailable and says how to replace it", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, version: "v0.30.0", sup: NopSupervisor{}, opts: fastOptions()})
		client, err := Ensure(t.Context(), p, "/nonexistent/ws", true, "v0.31.0")
		require.ErrorIs(t, err, ErrUnavailable)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "run ws daemon stop --keep-runners")
	})
	t.Run("autostart starts a transient daemon that answers within 5 s", func(t *testing.T) {
		configDir := t.TempDir()
		//nolint:usetesting // socket paths are limited to 108 bytes; t.TempDir's names are too long.
		runtimeDir, err := os.MkdirTemp("", "wsd")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
		t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
		t.Setenv(helperConfigEnv, configDir)
		p := PathsFor(configDir, os.Getenv, os.Getuid())
		exe := writeHelperScript(t)
		client, err := Ensure(t.Context(), p, exe, true, "")
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = client.Shutdown(t.Context(), false) })
		st, err := client.Status(t.Context())
		require.NoError(t, err)
		assert.Equal(t, OriginTransient, *st.Origin)
	})
}

// writeHelperScript returns an executable that runs this test binary as the
// daemon, ignoring its arguments.
func writeHelperScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ws")
	body := "#!/bin/sh\nexec '" + os.Args[0] + "' -test.run='^TestHelperDaemon$'\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))
	return script
}

// TestHelperDaemon is the daemon writeHelperScript runs. It is skipped in
// normal runs, where the config directory variable isn't set.
func TestHelperDaemon(t *testing.T) {
	configDir := os.Getenv(helperConfigEnv)
	if configDir == "" {
		t.Skip("only runs as the helper daemon")
	}
	p := PathsFor(configDir, os.Getenv, os.Getuid())
	err := serve(t.Context(), p, OriginTransient, NopSupervisor{}, NopEnvironments{}, discardLog(), options{idle: idleLimit, parentPoll: parentInterval, getppid: os.Getppid})
	require.NoError(t, err)
}
