package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/runner"
)

var (
	testNow       = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	testStartedAt = testNow.Add(-time.Hour)
)

// fakeClock is the supervisor's clock, moved by the test.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// procTable is the operating system's process table as a test sees it:
// which pids are alive, all of them started at testStartedAt.
type procTable struct {
	alive   map[int]bool
	nextPID int
}

func newProcTable() *procTable {
	return &procTable{alive: map[int]bool{}, nextPID: 100}
}

func (p *procTable) reader() *processReaderMock {
	return &processReaderMock{StartTimeFunc: func(pid int) (time.Time, error) {
		if p.alive[pid] {
			return testStartedAt, nil
		}
		return time.Time{}, errors.New("no such process")
	}}
}

// spawn is a new live pid.
func (p *procTable) spawn() int {
	p.nextPID++
	p.alive[p.nextPID] = true
	return p.nextPID
}

// sessionFixture is one workspace's runner session under a temporary state
// directory, with the clock and process table its supervisor uses.
type sessionFixture struct {
	t     *testing.T
	id    string
	files runner.Files
	clock *fakeClock
	procs *procTable
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	state := t.TempDir()
	files := runner.FilesFor(state, "ws-1")
	run := runner.Run{Version: 1, Session: "rn-1", Role: "implementer", MaxSteps: 80, CreatedAt: testNow.Add(-2 * time.Hour)}
	require.NoError(t, files.WriteRun(&run))
	return &sessionFixture{t: t, id: "ws-1", files: files, clock: &fakeClock{now: testNow}, procs: newProcTable()}
}

func (f *sessionFixture) stateDir() string {
	return filepath.Dir(filepath.Dir(f.files.Dir))
}

// writeStatus writes status.json for a runner at pid, started at testStartedAt.
func (f *sessionFixture) writeStatus(pid int, mutate func(*runner.Status)) {
	f.t.Helper()
	// UpdatedAt is old, so a dead runner isn't held in the restart window.
	st := runner.Status{Session: "rn-1", PID: pid, ProcessStartedAt: testStartedAt, State: "working", UpdatedAt: testStartedAt}
	if mutate != nil {
		mutate(&st)
	}
	require.NoError(f.t, f.files.WriteStatus(&st))
}

// starter starts a runner the way the real one does: a new pid, and a
// status.json that names it, keeping the restarts already counted.
func (f *sessionFixture) starter() *processStarterMock {
	return &processStarterMock{
		StartFunc: func(_ string, _ []string, _, _ string, _ []string) (int, error) {
			pid := f.procs.spawn()
			f.writeStatus(pid, func(st *runner.Status) {
				if prev, err := f.files.ReadStatus(); err == nil {
					st.Restarts = prev.Restarts
				}
			})
			return pid, nil
		},
		SignalFunc: func(int, syscall.Signal) error { return nil },
	}
}

func (f *sessionFixture) supervisor(starter processStarter, opts func(*SupervisorConfig)) *Supervisor {
	cfg := SupervisorConfig{
		StateDir:      f.stateDir(),
		ConfigDir:     f.t.TempDir(),
		Exe:           "ws",
		BaseEnv:       []string{"PATH=/usr/bin"},
		LoadConfig:    func() (config.Config, error) { return config.Config{}, nil },
		Poll:          time.Second,
		Backoff:       []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute},
		ResumeOnStart: true,
		StartWait:     100 * time.Millisecond,
		StopWait:      50 * time.Millisecond,
		Now:           f.clock.Now,
		Store:         &RunnerRecordsMock{},
	}
	if opts != nil {
		opts(&cfg)
	}
	return NewSupervisor(cfg, f.procs.reader(), starter, discardLog())
}

func (f *sessionFixture) readStatus() runner.Status {
	f.t.Helper()
	st, err := f.files.ReadStatus()
	require.NoError(f.t, err)
	return st
}

func TestSupervisor_Reconcile(t *testing.T) {
	t.Parallel()
	t.Run("adopts a live runner", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		sup := f.supervisor(&processStarterMock{}, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		runners := sup.Runners()
		require.Len(t, runners, 1)
		assert.Equal(t, RunnerStatus{Workspace: "ws-1", Session: "rn-1", PID: 7, State: "working", Adopted: true}, runners[0])
		assert.False(t, sup.Idle())
	})
	t.Run("resumes a stopped runner with --resume", func(t *testing.T) {
		f := newSessionFixture(t)
		f.writeStatus(7, nil)
		starter := f.starter()
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		require.Len(t, starter.StartCalls(), 1)
		assert.Contains(t, starter.StartCalls()[0].Args, "--resume")
		assert.Equal(t, 1, f.readStatus().Restarts)
		assert.Equal(t, "working", sup.Runners()[0].State)
	})
	t.Run("leaves a stopped runner when resume_on_start is off", func(t *testing.T) {
		f := newSessionFixture(t)
		f.writeStatus(7, nil)
		starter := f.starter()
		sup := f.supervisor(starter, func(c *SupervisorConfig) { c.ResumeOnStart = false })
		require.NoError(t, sup.Reconcile(t.Context()))
		assert.Empty(t, starter.StartCalls())
		assert.Empty(t, sup.Runners())
	})
	t.Run("leaves a finished runner", func(t *testing.T) {
		f := newSessionFixture(t)
		exit := "stopped"
		f.writeStatus(7, func(st *runner.Status) { st.Exit = &exit })
		starter := f.starter()
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		assert.Empty(t, starter.StartCalls())
		assert.True(t, sup.Idle())
	})
	t.Run("a state directory that doesn't exist is fine", func(t *testing.T) {
		sup := NewSupervisor(SupervisorConfig{StateDir: filepath.Join(t.TempDir(), "missing")}, &processReaderMock{}, &processStarterMock{}, discardLog())
		require.NoError(t, sup.Reconcile(t.Context()))
		assert.True(t, sup.Idle())
	})
}

func TestSupervisor_Start(t *testing.T) {
	t.Parallel()
	t.Run("a workspace without a runner session is workspace_not_found", func(t *testing.T) {
		f := newSessionFixture(t)
		sup := f.supervisor(&processStarterMock{}, nil)
		_, err := sup.Start(t.Context(), "ws-missing", false)
		assertRemoteCode(t, err, "workspace_not_found")
	})
	t.Run("a runner that writes no status is runner_failed with its log", func(t *testing.T) {
		f := newSessionFixture(t)
		starter := &processStarterMock{
			StartFunc:  func(string, []string, string, string, []string) (int, error) { return 5, nil },
			SignalFunc: func(int, syscall.Signal) error { return nil },
		}
		sup := f.supervisor(starter, nil)
		_, err := sup.Start(t.Context(), "ws-1", false)
		assertRemoteCode(t, err, "runner_failed")
		remote, ok := errors.AsType[*RemoteError](err)
		require.True(t, ok)
		assert.Equal(t, f.files.Log(), remote.Details["log"])
		require.Len(t, starter.SignalCalls(), 1)
		assert.Equal(t, syscall.SIGTERM, starter.SignalCalls()[0].Sig)
		assert.Empty(t, sup.Runners())
	})
	t.Run("builds the environment and passes the secret names", func(t *testing.T) {
		f := newSessionFixture(t)
		configDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(configDir, config.SecretsFile), []byte("WS_ENDPOINT_TOKEN_GPU=s3cret\n"), 0o600))
		starter := f.starter()
		sup := f.supervisor(starter, func(c *SupervisorConfig) { c.ConfigDir = configDir })
		pid, err := sup.Start(t.Context(), "ws-1", false)
		require.NoError(t, err)
		assert.Equal(t, 101, pid)
		call := starter.StartCalls()[0]
		assert.Equal(t, []string{"runner", "run", "ws-1"}, call.Args)
		assert.Equal(t, f.files.Dir, call.Dir)
		assert.Equal(t, f.files.Log(), call.LogPath)
		assert.Contains(t, call.Env, "WS_ENDPOINT_TOKEN_GPU=s3cret")
		assert.Contains(t, call.Env, "WS_RUNNER=ws")
		assert.Contains(t, call.Env, "WS_WORKSPACE=ws-1")
		assert.Contains(t, call.Env, "WS_CONFIG_DIR="+configDir)
		assert.Contains(t, call.Env, "WS_RUNNER_SECRET_NAMES=WS_ENDPOINT_TOKEN_GPU")
		assert.Contains(t, call.Env, "PATH=/usr/bin")
	})
	t.Run("a working runner is returned rather than started again", func(t *testing.T) {
		f := newSessionFixture(t)
		starter := f.starter()
		sup := f.supervisor(starter, nil)
		first, err := sup.Start(t.Context(), "ws-1", false)
		require.NoError(t, err)
		second, err := sup.Start(t.Context(), "ws-1", false)
		require.NoError(t, err)
		assert.Equal(t, first, second)
		assert.Len(t, starter.StartCalls(), 1)
	})
	t.Run("a start after a stop clears the stop file before the runner starts", func(t *testing.T) {
		f := newSessionFixture(t)
		starter := f.starter()
		sup := f.supervisor(starter, nil)
		_, err := sup.Start(t.Context(), "ws-1", false)
		require.NoError(t, err)
		stopped, err := sup.Stop(t.Context(), "ws-1")
		require.NoError(t, err)
		require.True(t, stopped)
		require.FileExists(t, filepath.Join(f.files.Dir, "stop"))
		startProcess := starter.StartFunc
		starter.StartFunc = func(exe string, args []string, dir, logPath string, env []string) (int, error) {
			assert.NoFileExists(t, filepath.Join(f.files.Dir, "stop"))
			return startProcess(exe, args, dir, logPath, env)
		}
		_, err = sup.Start(t.Context(), "ws-1", true)
		require.NoError(t, err)
		assert.Contains(t, starter.StartCalls()[1].Args, "--resume")
	})
}

func TestSupervisor_Stop(t *testing.T) {
	t.Parallel()
	t.Run("a workspace without a runner session is workspace_not_found", func(t *testing.T) {
		f := newSessionFixture(t)
		sup := f.supervisor(&processStarterMock{}, nil)
		_, err := sup.Stop(t.Context(), "ws-missing")
		assertRemoteCode(t, err, "workspace_not_found")
	})
	t.Run("a finished runner is left alone and reports false", func(t *testing.T) {
		f := newSessionFixture(t)
		exit := "crashed"
		f.writeStatus(7, func(st *runner.Status) { st.Exit = &exit })
		starter := &processStarterMock{SignalFunc: func(int, syscall.Signal) error { return nil }}
		sup := f.supervisor(starter, nil)
		stopped, err := sup.Stop(t.Context(), "ws-1")
		require.NoError(t, err)
		assert.False(t, stopped)
		assert.Empty(t, starter.SignalCalls())
		assert.NoFileExists(t, filepath.Join(f.files.Dir, "stop"))
	})
	t.Run("a working runner gets the stop file and SIGTERM, and exits", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		exit := "stopped"
		starter := &processStarterMock{SignalFunc: func(int, syscall.Signal) error {
			f.procs.alive[7] = false
			f.writeStatus(7, func(st *runner.Status) { st.Exit = &exit })
			return nil
		}}
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		stopped, err := sup.Stop(t.Context(), "ws-1")
		require.NoError(t, err)
		assert.True(t, stopped)
		assert.FileExists(t, filepath.Join(f.files.Dir, "stop"))
		require.Len(t, starter.SignalCalls(), 1)
		assert.Equal(t, syscall.SIGTERM, starter.SignalCalls()[0].Sig)
		assert.Equal(t, 7, starter.SignalCalls()[0].Pgid)
		assert.True(t, sup.Idle())
	})
	t.Run("a runner still alive after the stop wait is killed and finalized as stopped", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		starter := &processStarterMock{SignalFunc: func(int, syscall.Signal) error { return nil }}
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		stopped, err := sup.Stop(t.Context(), "ws-1")
		require.NoError(t, err)
		assert.True(t, stopped)
		require.Len(t, starter.SignalCalls(), 2)
		assert.Equal(t, syscall.SIGKILL, starter.SignalCalls()[1].Sig)
		st := f.readStatus()
		require.NotNil(t, st.Exit)
		assert.Equal(t, "stopped", *st.Exit)
		assert.FileExists(t, filepath.Join(f.files.Dir, "usage.json"))
	})
	t.Run("a runner that died without an exit is finalized as stopped", func(t *testing.T) {
		f := newSessionFixture(t)
		f.writeStatus(7, nil)
		starter := &processStarterMock{SignalFunc: func(int, syscall.Signal) error { return nil }}
		sup := f.supervisor(starter, nil)
		stopped, err := sup.Stop(t.Context(), "ws-1")
		require.NoError(t, err)
		assert.True(t, stopped)
		assert.Empty(t, starter.SignalCalls())
		st := f.readStatus()
		require.NotNil(t, st.Exit)
		assert.Equal(t, "stopped", *st.Exit)
	})
}

func TestSupervisor_StopAll(t *testing.T) {
	t.Parallel()
	t.Run("keep lists the live runners and signals nothing", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		starter := &processStarterMock{SignalFunc: func(int, syscall.Signal) error { return nil }}
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		stopped, kept := sup.StopAll(t.Context(), true)
		assert.Empty(t, stopped)
		assert.Equal(t, []string{"rn-1"}, kept)
		assert.Empty(t, starter.SignalCalls())
		assert.False(t, sup.Idle())
	})
	t.Run("interrupts the live runners without a stop file", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		starter := &processStarterMock{SignalFunc: func(int, syscall.Signal) error { return nil }}
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		stopped, kept := sup.StopAll(t.Context(), false)
		assert.Equal(t, []string{"rn-1"}, stopped)
		assert.Empty(t, kept)
		require.Len(t, starter.SignalCalls(), 1)
		assert.Equal(t, syscall.SIGTERM, starter.SignalCalls()[0].Sig)
		assert.NoFileExists(t, filepath.Join(f.files.Dir, "stop"))
		assert.True(t, sup.Idle())
	})
}

func TestSupervisor_Watch(t *testing.T) {
	t.Parallel()
	t.Run("a dead runner resumes after each backoff, and the fourth death finalizes it as crashed", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		starter := f.starter()
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		// Each death is observed, then the clock moves past its backoff: 5 s,
		// 30 s, then 2 min. The fourth death has no backoff left.
		waits := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}
		for i, wait := range waits {
			delete(f.procs.alive, sup.Runners()[0].PID)
			sup.poll()
			before := len(starter.StartCalls())
			f.clock.now = f.clock.now.Add(wait - time.Second)
			sup.poll()
			assert.Len(t, starter.StartCalls(), before, "resumed before %s", wait)
			f.clock.now = f.clock.now.Add(time.Second)
			sup.poll()
			assert.Len(t, starter.StartCalls(), before+1, "resumed after %s", wait)
			assert.Equal(t, i+1, f.readStatus().Restarts)
		}
		delete(f.procs.alive, sup.Runners()[0].PID)
		sup.poll()
		assert.True(t, sup.Idle())
		st := f.readStatus()
		require.NotNil(t, st.Exit)
		assert.Equal(t, "crashed", *st.Exit)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "FAIL", *st.Verdict)
		assert.FileExists(t, filepath.Join(f.files.Dir, "usage.json"))
		assert.FileExists(t, filepath.Join(f.files.Dir, "report.md"))
	})
	t.Run("a resumed runner that dies within the restart window is resumed again", func(t *testing.T) {
		f := newSessionFixture(t)
		f.procs.alive[7] = true
		f.writeStatus(7, nil)
		starter := f.starter()
		sup := f.supervisor(starter, nil)
		require.NoError(t, sup.Reconcile(t.Context()))
		// The runner writes status.json as it works, so its UpdatedAt is
		// recent when it dies, as the starter's fixed one is not.
		touch := func() {
			st := f.readStatus()
			st.UpdatedAt = f.clock.now
			require.NoError(t, f.files.WriteStatus(&st))
		}
		delete(f.procs.alive, 7)
		sup.poll()
		f.clock.now = f.clock.now.Add(5 * time.Second)
		sup.poll()
		require.Len(t, starter.StartCalls(), 1)
		touch()
		delete(f.procs.alive, sup.Runners()[0].PID)
		f.clock.now = f.clock.now.Add(time.Second)
		sup.poll()
		f.clock.now = f.clock.now.Add(30 * time.Second)
		sup.poll()
		assert.Len(t, starter.StartCalls(), 2, "the second death was not noticed")
		assert.Equal(t, 2, f.readStatus().Restarts)
	})
	t.Run("returns when its context ends", func(t *testing.T) {
		sup := NewSupervisor(SupervisorConfig{Poll: time.Millisecond}, &processReaderMock{}, &processStarterMock{}, discardLog())
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			sup.Watch(ctx)
			close(done)
		}()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Watch kept running after its context ended")
		}
	})
}

func TestSupervisor_Warnings(t *testing.T) {
	t.Parallel()
	t.Run("secrets.env with a loose mode is named", func(t *testing.T) {
		f := newSessionFixture(t)
		configDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(configDir, config.SecretsFile), []byte("A=b\n"), 0o644))
		sup := f.supervisor(&processStarterMock{}, func(c *SupervisorConfig) { c.ConfigDir = configDir })
		warnings := sup.Warnings()
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "secrets.env ignored")
	})
	t.Run("a config.toml the last start couldn't use is named", func(t *testing.T) {
		f := newSessionFixture(t)
		starter := f.starter()
		sup := f.supervisor(starter, func(c *SupervisorConfig) {
			c.LoadConfig = func() (config.Config, error) { return config.Config{}, errors.New("bad table") }
		})
		_, err := sup.Start(t.Context(), "ws-1", false)
		require.NoError(t, err)
		assert.Equal(t, []string{"config.toml: bad table"}, sup.Warnings())
	})
}

func TestSupervisor_readStatusOrNew(t *testing.T) {
	t.Parallel()
	t.Run("a missing status.json is a new one for the run", func(t *testing.T) {
		f := newSessionFixture(t)
		run := runner.Run{Session: "rn-1", MaxSteps: 80}
		st, err := readStatusOrNew(f.files, &run)
		require.NoError(t, err)
		assert.Equal(t, "rn-1", st.Session)
		assert.Equal(t, 80, st.MaxSteps)
	})
	t.Run("an unreadable status.json is an error", func(t *testing.T) {
		f := newSessionFixture(t)
		require.NoError(t, os.WriteFile(f.files.Dir+"/status.json", []byte("{"), 0o600))
		run := runner.Run{Session: "rn-1"}
		_, err := readStatusOrNew(f.files, &run)
		require.Error(t, err)
	})
}

func TestRunnerArgs(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"runner", "run", "ws-1"}, runnerArgs("ws-1", false))
	assert.Equal(t, []string{"runner", "run", "--resume", "ws-1"}, runnerArgs("ws-1", true))
}

func TestLogTail(t *testing.T) {
	t.Parallel()
	t.Run("a long log keeps its last bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runner.log")
		data := make([]byte, logTailBytes+10)
		copy(data[len(data)-3:], "end")
		require.NoError(t, os.WriteFile(path, data, 0o600))
		tail := logTail(path)
		assert.Len(t, tail, logTailBytes)
		assert.Equal(t, "end", tail[len(tail)-3:])
	})
	t.Run("a missing log is empty", func(t *testing.T) {
		assert.Empty(t, logTail(filepath.Join(t.TempDir(), "missing")))
	})
}
