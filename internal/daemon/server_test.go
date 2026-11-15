package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPaths returns a config directory's daemon paths, with the socket under
// a short temporary runtime directory (socket paths are length-limited).
func testPaths(t *testing.T) Paths {
	t.Helper()
	//nolint:usetesting // socket paths are limited to 108 bytes; t.TempDir's names are too long.
	runtimeDir, err := os.MkdirTemp("", "wsd")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	configDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "daemon"), 0o700))
	env := map[string]string{"XDG_RUNTIME_DIR": runtimeDir}
	return PathsFor(configDir, func(name string) string { return env[name] }, os.Getuid())
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	require.NoError(t, err)
	return abs
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// daemonRun is what a test starts a daemon with.
type daemonRun struct {
	origin  string
	version string
	sup     Supervisor
	opts    options
}

// startDaemon runs serve in the background and returns when the daemon
// answers. Its context is cancelled at cleanup.
//
//nolint:gocritic // hugeParam: Paths is passed by value, as the daemon API takes it.
func startDaemon(t *testing.T, p Paths, run daemonRun) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(WithVersion(t.Context(), run.version))
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- serve(ctx, p, run.origin, run.sup, discardLog(), run.opts) }()
	require.Eventually(t, func() bool {
		_, err := Dial(p).Status(ctx)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return done
}

func awaitExit(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't exit")
		return nil
	}
}

func fastOptions() options {
	return options{idle: 100 * time.Millisecond, parentPoll: time.Hour, getppid: func() int { return 1 }}
}

func TestServe(t *testing.T) {
	t.Parallel()
	t.Run("a second daemon for the same config directory is ErrRunning with the first's pid", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		err := serve(t.Context(), p, OriginManaged, NopSupervisor{}, discardLog(), fastOptions())
		require.ErrorIs(t, err, ErrRunning)
		running, ok := errors.AsType[*RunningError](err)
		require.True(t, ok)
		assert.Equal(t, os.Getpid(), running.PID)
	})
	t.Run("writes daemon.json and answers status with the ws version from the context", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, version: "v0.31.0", sup: NopSupervisor{}, opts: fastOptions()})
		info, err := readInfo(p.Info)
		require.NoError(t, err)
		assert.Equal(t, os.Getpid(), info.PID)
		assert.Equal(t, "v0.31.0", info.WSVersion)
		st, err := Dial(p).Status(t.Context())
		require.NoError(t, err)
		assert.True(t, st.Running)
		assert.Equal(t, os.Getpid(), *st.PID)
		assert.Equal(t, OriginManaged, *st.Origin)
		assert.Equal(t, p.Socket, st.Socket)
		assert.Equal(t, filepath.Dir(p.Dir), st.ConfigDir)
		assert.Empty(t, st.Runners)
	})
	t.Run("refuses an unknown origin", func(t *testing.T) {
		err := serve(t.Context(), testPaths(t), "bogus", NopSupervisor{}, discardLog(), fastOptions())
		require.Error(t, err)
	})
}

func TestServe_transientIdle(t *testing.T) {
	t.Parallel()
	t.Run("a transient daemon with no runner and no request exits by itself", func(t *testing.T) {
		p := testPaths(t)
		done := startDaemon(t, p, daemonRun{origin: OriginTransient, sup: NopSupervisor{}, opts: fastOptions()})
		require.NoError(t, awaitExit(t, done))
	})
	t.Run("a transient daemon with a live runner stays", func(t *testing.T) {
		p := testPaths(t)
		sup := &SupervisorMock{
			IdleFunc:    func() bool { return false },
			RunnersFunc: func() []RunnerStatus { return []RunnerStatus{{Session: "rn-1"}} },
		}
		done := startDaemon(t, p, daemonRun{origin: OriginTransient, sup: sup, opts: fastOptions()})
		select {
		case err := <-done:
			t.Fatalf("exited with a runner alive: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
	})
	t.Run("a managed daemon never exits by itself", func(t *testing.T) {
		p := testPaths(t)
		done := startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		select {
		case err := <-done:
			t.Fatalf("exited: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
	})
}

func TestServe_app(t *testing.T) {
	t.Parallel()
	t.Run("an app daemon whose parent changed acts as transient and exits", func(t *testing.T) {
		p := testPaths(t)
		var parent atomic.Int64
		parent.Store(10)
		opts := options{idle: 100 * time.Millisecond, parentPoll: 10 * time.Millisecond, getppid: func() int { return int(parent.Load()) }}
		done := startDaemon(t, p, daemonRun{origin: OriginApp, sup: NopSupervisor{}, opts: opts})
		time.Sleep(50 * time.Millisecond)
		parent.Store(1)
		require.NoError(t, awaitExit(t, done))
	})
	t.Run("an app daemon whose parent stays keeps running", func(t *testing.T) {
		p := testPaths(t)
		opts := options{idle: 100 * time.Millisecond, parentPoll: 10 * time.Millisecond, getppid: func() int { return 10 }}
		done := startDaemon(t, p, daemonRun{origin: OriginApp, sup: NopSupervisor{}, opts: opts})
		select {
		case err := <-done:
			t.Fatalf("exited while its parent lived: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
	})
}

func TestServe_shutdown(t *testing.T) {
	t.Parallel()
	t.Run("a shutdown request ends the daemon and reports its pid", func(t *testing.T) {
		p := testPaths(t)
		done := startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		res, err := Dial(p).Shutdown(t.Context(), false)
		require.NoError(t, err)
		assert.True(t, res.Stopped)
		assert.Equal(t, os.Getpid(), *res.PID)
		assert.Empty(t, res.RunnersStopped)
		assert.NotNil(t, res.Warnings)
		require.NoError(t, awaitExit(t, done))
	})
	t.Run("keep_runners reaches the supervisor and lists the kept runners", func(t *testing.T) {
		p := testPaths(t)
		sup := &SupervisorMock{
			IdleFunc:    func() bool { return true },
			RunnersFunc: func() []RunnerStatus { return nil },
			StopAllFunc: func(_ context.Context, keep bool) ([]string, []string) {
				if keep {
					return nil, []string{"rn-1"}
				}
				return []string{"rn-1"}, nil
			},
		}
		done := startDaemon(t, p, daemonRun{origin: OriginManaged, sup: sup, opts: fastOptions()})
		res, err := Dial(p).Shutdown(t.Context(), true)
		require.NoError(t, err)
		assert.Equal(t, []string{"rn-1"}, res.RunnersKept)
		require.NoError(t, awaitExit(t, done))
	})
}

func TestPeerListener_Accept(t *testing.T) {
	t.Parallel()
	t.Run("keeps a connection from the daemon's own uid", func(t *testing.T) {
		p := testPaths(t)
		ln, err := listen(t.Context(), p.Socket)
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()
		accepted := make(chan net.Conn, 1)
		go func() {
			conn, _ := peerListener{Listener: ln, uid: os.Getuid(), log: discardLog()}.Accept()
			accepted <- conn
		}()
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", p.Socket)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		got := <-accepted
		require.NotNil(t, got)
		_ = got.Close()
	})
	t.Run("closes a connection from another uid and keeps waiting", func(t *testing.T) {
		p := testPaths(t)
		ln, err := listen(t.Context(), p.Socket)
		require.NoError(t, err)
		listener := peerListener{Listener: ln, uid: os.Getuid() + 1, log: discardLog()}
		accepted := make(chan error, 1)
		go func() {
			_, acceptErr := listener.Accept()
			accepted <- acceptErr
		}()
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", p.Socket)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		_, readErr := conn.Read(make([]byte, 1))
		require.ErrorIs(t, readErr, io.EOF)
		require.NoError(t, ln.Close())
		require.Error(t, <-accepted)
	})
}
