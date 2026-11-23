package daemon

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Status(t *testing.T) {
	t.Parallel()
	t.Run("nothing listening is ErrNotRunning", func(t *testing.T) {
		_, err := Dial(testPaths(t)).Status(t.Context())
		require.ErrorIs(t, err, ErrNotRunning)
	})
	t.Run("a running daemon answers its runners", func(t *testing.T) {
		p := testPaths(t)
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
			IdleFunc: func() bool { return true },
			RunnersFunc: func() []RunnerStatus {
				return []RunnerStatus{{Workspace: "w", Session: "rn-1", PID: 5, State: "working"}}
			},
		}
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: sup, opts: fastOptions()})
		st, err := Dial(p).Status(t.Context())
		require.NoError(t, err)
		require.Len(t, st.Runners, 1)
		assert.Equal(t, "rn-1", st.Runners[0].Session)
	})
}

func TestClient_StartRunner(t *testing.T) {
	t.Parallel()
	t.Run("returns the pid the supervisor started", func(t *testing.T) {
		p := testPaths(t)
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
			IdleFunc:    func() bool { return true },
			RunnersFunc: func() []RunnerStatus { return nil },
			StartFunc:   func(context.Context, string, bool) (int, error) { return 99, nil },
		}
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: sup, opts: fastOptions()})
		pid, err := Dial(p).StartRunner(t.Context(), "ws-1", true)
		require.NoError(t, err)
		assert.Equal(t, 99, pid)
		calls := sup.StartCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, "ws-1", calls[0].ID)
		assert.True(t, calls[0].Resume)
	})
	t.Run("a daemon that doesn't supervise answers not_supervising", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		_, err := Dial(p).StartRunner(t.Context(), "ws-1", false)
		assertRemoteCode(t, err, "not_supervising")
	})
}

func TestClient_StopRunner(t *testing.T) {
	t.Parallel()
	t.Run("stops the runner through the supervisor", func(t *testing.T) {
		p := testPaths(t)
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
			IdleFunc:    func() bool { return true },
			RunnersFunc: func() []RunnerStatus { return nil },
			StopFunc:    func(context.Context, string) (bool, error) { return true, nil },
		}
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: sup, opts: fastOptions()})
		stopped, err := Dial(p).StopRunner(t.Context(), "ws-1")
		require.NoError(t, err)
		assert.True(t, stopped)
		require.Len(t, sup.StopCalls(), 1)
	})
	t.Run("a runner that had exited reports false", func(t *testing.T) {
		p := testPaths(t)
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
			IdleFunc:    func() bool { return true },
			RunnersFunc: func() []RunnerStatus { return nil },
			StopFunc:    func(context.Context, string) (bool, error) { return false, nil },
		}
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: sup, opts: fastOptions()})
		stopped, err := Dial(p).StopRunner(t.Context(), "ws-1")
		require.NoError(t, err)
		assert.False(t, stopped)
	})
	t.Run("a supervisor error is an internal remote error", func(t *testing.T) {
		p := testPaths(t)
		sup := &RunnerSupervisorMock{
			ReconcileFunc: noopReconcile, WatchFunc: noopWatch, WarningsFunc: noWarnings,
			IdleFunc:    func() bool { return true },
			RunnersFunc: func() []RunnerStatus { return nil },
			StopFunc:    func(context.Context, string) (bool, error) { return false, errors.New("boom") },
		}
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: sup, opts: fastOptions()})
		_, err := Dial(p).StopRunner(t.Context(), "ws-1")
		assertRemoteCode(t, err, "internal")
		assert.Contains(t, err.Error(), "boom")
	})
}

func TestClient_Preflight(t *testing.T) {
	t.Parallel()
	t.Run("a daemon that doesn't supervise answers not_supervising", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		_, err := Dial(p).Preflight(t.Context(), PreflightRequest{Role: "implementer"})
		assertRemoteCode(t, err, "not_supervising")
	})
	t.Run("a body that isn't an object is a usage error", func(t *testing.T) {
		p := testPaths(t)
		startDaemon(t, p, daemonRun{origin: OriginManaged, sup: NopSupervisor{}, opts: fastOptions()})
		err := Dial(p).do(t.Context(), http.MethodPost, "/v1/preflight", "not an object", nil)
		assertRemoteCode(t, err, "usage")
	})
}

func TestClient_Shutdown(t *testing.T) {
	t.Parallel()
	t.Run("a shutdown of a daemon that isn't running is ErrNotRunning", func(t *testing.T) {
		_, err := Dial(testPaths(t)).Shutdown(t.Context(), false)
		require.ErrorIs(t, err, ErrNotRunning)
	})
}

func assertRemoteCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	remote, ok := errors.AsType[*RemoteError](err)
	require.True(t, ok, "want a RemoteError, got %v", err)
	assert.Equal(t, code, remote.Code)
}

func TestClient_ReloadEnvironments(t *testing.T) {
	t.Parallel()
	t.Run("nothing listening is ErrNotRunning", func(t *testing.T) {
		err := Dial(testPaths(t)).ReloadEnvironments(t.Context())
		require.ErrorIs(t, err, ErrNotRunning)
	})
}

func TestClient_CallEnvironment(t *testing.T) {
	t.Parallel()
	t.Run("a 200 answer decodes the reply", func(t *testing.T) {
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{Reply: []byte(`{"ok":true,"result":null,"error":null}`)}, nil
		})
		res, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("workspace.list"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true,"result":null,"error":null}`, string(res.Reply))
	})
	t.Run("a 503 is a RemoteError with environment_offline and its details", func(t *testing.T) {
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrEnvironmentOffline, Details: map[string]any{"reason": "timeout"}}
		})
		_, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest("workspace.list"))
		assertRemoteCode(t, err, "environment_offline")
		remote, ok := errors.AsType[*RemoteError](err)
		require.True(t, ok)
		assert.Equal(t, "timeout", remote.Details["reason"])
	})
}
