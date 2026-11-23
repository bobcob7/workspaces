package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	sup     RunnerSupervisor
	envs    EnvironmentHost // NopEnvironments{} when nil
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
	envs := run.envs
	if envs == nil {
		envs = NopEnvironments{}
	}
	go func() { done <- serve(ctx, p, run.origin, run.sup, envs, discardLog(), run.opts) }()
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
		err := serve(t.Context(), p, OriginManaged, NopSupervisor{}, NopEnvironments{}, discardLog(), fastOptions())
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
		err := serve(t.Context(), testPaths(t), "bogus", NopSupervisor{}, NopEnvironments{}, discardLog(), fastOptions())
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
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
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
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
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

func noopReconcile(context.Context) error { return nil }

func noopWatch(context.Context) {}

func noWarnings() []string { return []string{} }

func TestServe_environments(t *testing.T) {
	t.Parallel()
	listening := func() *EnvironmentHostMock {
		return &EnvironmentHostMock{
			StartFunc:    func(context.Context) error { return nil },
			CloseFunc:    func() error { return nil },
			WarningsFunc: func() []string { return []string{"the CA expires soon"} },
			StatusFunc: func() EnvironmentsStatus {
				return EnvironmentsStatus{
					Listening: true, Listen: "127.0.0.1:8883", EnrollListen: "127.0.0.1:8884",
					Environments: []EnvironmentPresence{{ID: "env-1a2b3c4d", Name: "linux", State: "enrolled", Status: "offline"}},
				}
			},
		}
	}
	t.Run("status carries the environments and their warnings", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, envs: listening(), opts: fastOptions()})
		st, err := Dial(p).Status(t.Context())
		require.NoError(t, err)
		require.NotNil(t, st.Environments)
		assert.True(t, st.Environments.Listening)
		assert.Equal(t, "127.0.0.1:8883", st.Environments.Listen)
		require.Len(t, st.Environments.Environments, 1)
		assert.Equal(t, "offline", st.Environments.Environments[0].Status)
		assert.Equal(t, []string{"the CA expires soon"}, st.Warnings)
	})
	t.Run("status with no environments has an empty list and no warnings", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		st, err := Dial(p).Status(t.Context())
		require.NoError(t, err)
		require.NotNil(t, st.Environments)
		assert.False(t, st.Environments.Listening)
		assert.Empty(t, st.Environments.Environments)
		assert.Equal(t, []string{}, st.Warnings)
	})
	t.Run("a start error is a warning and the daemon keeps running", func(t *testing.T) {
		p := testPaths(t)
		envs := &EnvironmentHostMock{
			StartFunc:    func(context.Context) error { return errors.New("no public name") },
			CloseFunc:    func() error { return nil },
			WarningsFunc: func() []string { return []string{} },
			StatusFunc:   func() EnvironmentsStatus { return EnvironmentsStatus{Environments: []EnvironmentPresence{}} },
		}
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, envs: envs, opts: fastOptions()})
		st, err := Dial(p).Status(t.Context())
		require.NoError(t, err)
		assert.True(t, st.Running)
		assert.Equal(t, []string{"environments didn't start: no public name"}, st.Warnings)
	})
	t.Run("reload calls the environments' Reload", func(t *testing.T) {
		p := testPaths(t)
		var reloads atomic.Int32
		envs := listening()
		envs.ReloadFunc = func(context.Context) error { reloads.Add(1); return nil }
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, envs: envs, opts: fastOptions()})
		require.NoError(t, Dial(p).ReloadEnvironments(t.Context()))
		assert.Equal(t, int32(1), reloads.Load())
	})
	t.Run("a failed reload is the error envelope", func(t *testing.T) {
		p := testPaths(t)
		envs := listening()
		envs.ReloadFunc = func(context.Context) error { return errors.New("reading the CA") }
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, envs: envs, opts: fastOptions()})
		assertRemoteCode(t, Dial(p).ReloadEnvironments(t.Context()), "internal")
	})
	t.Run("closing the environments when the daemon exits", func(t *testing.T) {
		p := testPaths(t)
		var closed atomic.Bool
		envs := listening()
		envs.CloseFunc = func() error { closed.Store(true); return nil }
		done := startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, envs: envs, opts: fastOptions()})
		_, err := Dial(p).Shutdown(t.Context(), false)
		require.NoError(t, err)
		require.NoError(t, awaitExit(t, done))
		assert.True(t, closed.Load())
	})
	t.Run("a transient daemon with listening environments stays", func(t *testing.T) {
		p := testPaths(t)
		done := startDaemon(t, p, daemonRun{origin: OriginTransient, sup: NopSupervisor{}, envs: listening(), opts: fastOptions()})
		select {
		case err := <-done:
			t.Fatalf("exited while its environments listened: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
	})
}

// callRequest is a valid call of op.
func callRequest(op string) CallRequest {
	return CallRequest{
		Op:        op,
		Args:      json.RawMessage(`{}`),
		RequestID: "0123456789abcdef",
		Deadline:  time.Now().Add(time.Minute),
	}
}

// callDaemon runs a daemon whose environments answer calls with fn, and
// returns its paths.
func callDaemon(t *testing.T, fn func(context.Context, string, CallRequest) (CallReply, error)) Paths {
	t.Helper()
	p := testPaths(t)
	host := &EnvironmentHostMock{
		StartFunc:    func(context.Context) error { return nil },
		CloseFunc:    func() error { return nil },
		WarningsFunc: func() []string { return []string{} },
		StatusFunc:   func() EnvironmentsStatus { return EnvironmentsStatus{Environments: []EnvironmentPresence{}} },
		CallFunc:     fn,
	}
	startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, envs: host, opts: fastOptions()})
	return p
}

func TestServer_handleCallEnvironment(t *testing.T) {
	t.Parallel()
	t.Run("a reply is passed through unchanged", func(t *testing.T) {
		const reply = `{"ok":true,"result":{"workspaces":[]},"error":null}`
		var got struct {
			name string
			req  CallRequest
		}
		p := callDaemon(t, func(_ context.Context, name string, req CallRequest) (CallReply, error) {
			got.name, got.req = name, req
			return CallReply{Reply: json.RawMessage(reply)}, nil
		})
		res, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("workspace.list"))
		require.NoError(t, err)
		assert.JSONEq(t, reply, string(res.Reply))
		assert.Equal(t, "linux", got.name)
		assert.Equal(t, "workspace.list", got.req.Op)
		assert.Equal(t, "0123456789abcdef", got.req.RequestID)
	})
	t.Run("an unknown environment is 404 environment_unknown", func(t *testing.T) {
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrEnvironmentUnknown, Message: "no such environment"}
		})
		_, err := Dial(p).CallEnvironment(t.Context(), "nope", callRequest("workspace.list"))
		assertRemoteCode(t, err, "environment_unknown")
	})
	t.Run("offline is 503 environment_offline with its details", func(t *testing.T) {
		details := map[string]any{"environment": "linux", "reason": "offline", "last_seen": nil, "request_id": "0123456789abcdef", "op": "workspace.list"}
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrEnvironmentOffline, Message: "environment linux is offline", Details: details}
		})
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("workspace.list"))
		assertRemoteCode(t, err, "environment_offline")
		remote, ok := errors.AsType[*RemoteError](err)
		require.True(t, ok)
		assert.Equal(t, details, remote.Details)
	})
	t.Run("outdated is 409 environment_outdated with its details", func(t *testing.T) {
		details := map[string]any{"environment": "linux", "op": "workspace.create", "ws_version": "v0.30.0", "protocol": 1, "need_protocol": 2}
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrEnvironmentOutdated, Message: "too old", Details: details}
		})
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("workspace.create"))
		assertRemoteCode(t, err, "environment_outdated")
		remote, ok := errors.AsType[*RemoteError](err)
		require.True(t, ok)
		assert.Equal(t, "v0.30.0", remote.Details["ws_version"])
		assert.EqualValues(t, 2, remote.Details["need_protocol"])
	})
	t.Run("an operation the daemon doesn't forward is 400 usage", func(t *testing.T) {
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrNotForwarded, Message: "cert.renew: operation not forwarded"}
		})
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("cert.renew"))
		assertRemoteCode(t, err, "usage")
	})
	t.Run("a bad request id is 400 usage and the host isn't called", func(t *testing.T) {
		var called atomic.Bool
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			called.Store(true)
			return CallReply{}, nil
		})
		req := callRequest("workspace.list")
		req.RequestID = "not-a-request-id"
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", req)
		assertRemoteCode(t, err, "usage")
		assert.False(t, called.Load())
	})
	t.Run("a request id already pending is 400 usage", func(t *testing.T) {
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrRequestPending, Message: "the request id is already pending: \"0123456789abcdef\""}
		})
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("workspace.list"))
		assertRemoteCode(t, err, "usage")
	})
	t.Run("a body over the call limit is 400 usage with its size message and the host isn't called", func(t *testing.T) {
		var called atomic.Bool
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			called.Store(true)
			return CallReply{}, nil
		})
		req := callRequest("workspace.list")
		req.Args = json.RawMessage(`"` + strings.Repeat("x", int(callBodyBytes)) + `"`)
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", req)
		assertRemoteCode(t, err, "usage")
		remote, ok := errors.AsType[*RemoteError](err)
		require.True(t, ok)
		assert.Equal(t, "the request body is too large", remote.Message)
		assert.False(t, called.Load())
	})
	t.Run("a zero deadline is 400 usage and the host isn't called", func(t *testing.T) {
		var called atomic.Bool
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			called.Store(true)
			return CallReply{}, nil
		})
		req := callRequest("workspace.list")
		req.Deadline = time.Time{}
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", req)
		assertRemoteCode(t, err, "usage")
		assert.False(t, called.Load())
	})
	t.Run("the host's context is the request's", func(t *testing.T) {
		cancelled := make(chan struct{})
		p := callDaemon(t, func(ctx context.Context, _ string, _ CallRequest) (CallReply, error) {
			<-ctx.Done()
			close(cancelled)
			return CallReply{}, ctx.Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(50*time.Millisecond, cancel)
		_, err := Dial(p).CallEnvironment(ctx, "linux", callRequest("workspace.list"))
		require.Error(t, err)
		select {
		case <-cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("the host's context wasn't cancelled with the request")
		}
	})
}
