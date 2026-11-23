package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// termFrame is one answer of a scripted terminal's Next.
type termFrame struct {
	data []byte
	exit *envproto.TerminalExit
	err  error
}

// termEnv is an App wired to a scripted remote terminal, a stdin pipe, and
// fake raw-mode, size, signal and pause seams.
type termEnv struct {
	app      *App
	stdout   *bytes.Buffer
	term     *remoteTerminalMock
	frames   chan termFrame
	stdinW   *io.PipeWriter
	mu       sync.Mutex
	inputs   [][]byte
	resizes  [][2]int
	closed   int
	raw      int
	restored int
	pauses   []time.Duration
	winch    chan<- os.Signal
	opened   []envproto.TerminalOpenArgs
	size     [2]int
	notified []os.Signal
	stopped  int
}

func newTermEnv(t *testing.T) *termEnv {
	t.Helper()
	e := &termEnv{frames: make(chan termFrame, 16), size: [2]int{100, 30}}
	stdinR, stdinW := io.Pipe()
	e.stdinW = stdinW
	t.Cleanup(func() { _ = stdinW.Close() })
	m := newMockedApp()
	e.app, e.stdout = m.app, m.stdout
	e.app.stdin = stdinR
	e.term = &remoteTerminalMock{
		NextFunc: func() ([]byte, *envproto.TerminalExit, error) {
			f := <-e.frames
			return f.data, f.exit, f.err
		},
		InputFunc: func(p []byte) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.inputs = append(e.inputs, bytes.Clone(p))
			return nil
		},
		ResizeFunc: func(cols, rows int) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.resizes = append(e.resizes, [2]int{cols, rows})
			return nil
		},
		CloseFunc: func() error {
			e.mu.Lock()
			e.closed++
			e.mu.Unlock()
			e.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "closed"}}
			return nil
		},
	}
	opener := &terminalOpenerMock{OpenTerminalFunc: func(_ context.Context, _ string, args envproto.TerminalOpenArgs) (remoteTerminal, envproto.TerminalOpenResult, error) {
		e.mu.Lock()
		e.opened = append(e.opened, args)
		e.mu.Unlock()
		return e.term, envproto.TerminalOpenResult{SID: "0123456789abcdef"}, nil
	}}
	e.app.dialTerminals = func(context.Context) (terminalOpener, error) { return opener, nil }
	e.app.stdinTerminal = func() bool { return true }
	e.app.rawMode = func() (func(), error) {
		e.mu.Lock()
		e.raw++
		e.mu.Unlock()
		return func() {
			e.mu.Lock()
			e.restored++
			e.mu.Unlock()
		}, nil
	}
	e.app.termSize = func() (int, int, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.size[0], e.size[1], nil
	}
	e.app.pause = func(_ context.Context, d time.Duration) {
		e.mu.Lock()
		e.pauses = append(e.pauses, d)
		e.mu.Unlock()
	}
	e.app.notify = func(c chan<- os.Signal, sigs ...os.Signal) {
		e.mu.Lock()
		e.winch = c
		e.notified = append([]os.Signal(nil), sigs...)
		e.mu.Unlock()
	}
	e.app.stopNotify = func(chan<- os.Signal) {
		e.mu.Lock()
		e.stopped++
		e.mu.Unlock()
	}
	return e
}

func (e *termEnv) snapshot() (inputs [][]byte, resizes [][2]int, closed, raw, restored int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slicesCloneBytes(e.inputs), append([][2]int(nil), e.resizes...), e.closed, e.raw, e.restored
}

func slicesCloneBytes(in [][]byte) [][]byte {
	return append([][]byte(nil), in...)
}

func (e *termEnv) inputBytes() []byte {
	inputs, _, _, _, _ := e.snapshot()
	return bytes.Join(inputs, nil)
}

func exited(code int) *envproto.TerminalExit {
	return &envproto.TerminalExit{Code: &code, Reason: "exited"}
}

func terminalShellArgs() envproto.TerminalOpenArgs {
	return envproto.TerminalOpenArgs{Kind: envproto.TermShell, Specs: []string{}}
}

func TestApp_runRemoteTerminal(t *testing.T) {
	t.Run("copies output to stdout", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{data: []byte("hello ")}
		e.frames <- termFrame{data: []byte("world")}
		e.frames <- termFrame{exit: exited(0)}
		exit, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.NoError(t, err)
		assert.Equal(t, "hello world", e.stdout.String())
		require.NotNil(t, exit.Code)
		assert.Equal(t, 0, *exit.Code)
	})
	t.Run("opens with the terminal's size", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: exited(0)}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.NoError(t, err)
		require.Len(t, e.opened, 1)
		assert.Equal(t, 100, e.opened[0].Cols)
		assert.Equal(t, 30, e.opened[0].Rows)
	})
	t.Run("sends stdin in frames of at most 4 KiB", func(t *testing.T) {
		e := newTermEnv(t)
		data := bytes.Repeat([]byte("abcdefghij"), 1000)
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
			done <- err
		}()
		_, err := e.stdinW.Write(data)
		require.NoError(t, err)
		require.Eventually(t, func() bool { return len(e.inputBytes()) == len(data) }, 5*time.Second, time.Millisecond)
		e.frames <- termFrame{exit: exited(0)}
		require.NoError(t, <-done)
		inputs, _, _, _, _ := e.snapshot()
		for _, in := range inputs {
			assert.LessOrEqual(t, len(in), envproto.MaxInputBytes)
		}
		assert.Equal(t, data, bytes.Join(inputs, nil))
	})
	t.Run("paces a large paste", func(t *testing.T) {
		e := newTermEnv(t)
		size := 2 * envproto.InputPaceBytes
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
			done <- err
		}()
		_, err := e.stdinW.Write(bytes.Repeat([]byte("x"), size))
		require.NoError(t, err)
		require.Eventually(t, func() bool { return len(e.inputBytes()) == size }, 5*time.Second, time.Millisecond)
		require.Eventually(t, func() bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			var total time.Duration
			for _, d := range e.pauses {
				total += d
			}
			return total >= 2*time.Second-time.Millisecond
		}, 5*time.Second, time.Millisecond)
		e.frames <- termFrame{exit: exited(0)}
		require.NoError(t, <-done)
		e.mu.Lock()
		defer e.mu.Unlock()
		var total time.Duration
		for _, d := range e.pauses {
			total += d
		}
		assert.Equal(t, 2*time.Second, total)
	})
	t.Run("sends a resize on SIGWINCH", func(t *testing.T) {
		e := newTermEnv(t)
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
			done <- err
		}()
		require.Eventually(t, func() bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.winch != nil
		}, 5*time.Second, time.Millisecond)
		e.mu.Lock()
		e.size = [2]int{120, 40}
		winch := e.winch
		e.mu.Unlock()
		winch <- syscall.SIGWINCH
		require.Eventually(t, func() bool {
			_, resizes, _, _, _ := e.snapshot()
			return len(resizes) > 0
		}, 5*time.Second, time.Millisecond)
		e.frames <- termFrame{exit: exited(0)}
		require.NoError(t, <-done)
		_, resizes, _, _, _ := e.snapshot()
		assert.Equal(t, [2]int{120, 40}, resizes[len(resizes)-1])
	})
	t.Run("restores the terminal on exit", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: exited(3)}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.NoError(t, err)
		_, _, _, raw, restored := e.snapshot()
		assert.Equal(t, 1, raw)
		assert.Equal(t, 1, restored)
	})
	t.Run("restores the terminal when the context ends", func(t *testing.T) {
		e := newTermEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(ctx, "linux-server", terminalShellArgs())
			done <- err
		}()
		require.Eventually(t, func() bool {
			_, _, _, raw, _ := e.snapshot()
			return raw == 1
		}, 5*time.Second, time.Millisecond)
		cancel()
		require.NoError(t, <-done)
		_, _, closed, _, restored := e.snapshot()
		assert.Equal(t, 1, closed)
		assert.Equal(t, 1, restored)
	})
	t.Run("restores the terminal on a SIGTERM", func(t *testing.T) {
		e := newTermEnv(t)
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
			done <- err
		}()
		require.Eventually(t, func() bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.winch != nil
		}, 5*time.Second, time.Millisecond)
		e.mu.Lock()
		winch := e.winch
		e.mu.Unlock()
		winch <- syscall.SIGTERM
		require.NoError(t, <-done)
		_, _, closed, _, restored := e.snapshot()
		assert.Equal(t, 1, closed)
		assert.Equal(t, 1, restored)
	})
	t.Run("restores the terminal on a panic", func(t *testing.T) {
		e := newTermEnv(t)
		e.term.NextFunc = func() ([]byte, *envproto.TerminalExit, error) { panic("boom") }
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.Error(t, err)
		_, _, _, _, restored := e.snapshot()
		assert.Equal(t, 1, restored)
	})
	t.Run("restores the terminal when the control loop panics", func(t *testing.T) {
		e := newTermEnv(t)
		e.term.InputFunc = func([]byte) error { panic("boom") }
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
			done <- err
		}()
		_, err := e.stdinW.Write([]byte("x"))
		require.NoError(t, err)
		require.Error(t, <-done)
		_, _, _, _, restored := e.snapshot()
		assert.Equal(t, 1, restored)
	})
	t.Run("a disconnected exit is environment_offline", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "disconnected"}}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		requireCode(t, codeEnvironmentOffline, err)
		assert.Contains(t, err.Error(), "linux-server went offline; the agent keeps running")
	})
	t.Run("a terminal the agent closed for its input rate is an error", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "input_limit"}}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "input_limit")
	})
	t.Run("a refused open leaves the terminal alone", func(t *testing.T) {
		e := newTermEnv(t)
		e.app.dialTerminals = func(context.Context) (terminalOpener, error) {
			return &terminalOpenerMock{OpenTerminalFunc: func(context.Context, string, envproto.TerminalOpenArgs) (remoteTerminal, envproto.TerminalOpenResult, error) {
				return nil, envproto.TerminalOpenResult{}, newError(codeTerminalLimit, io.ErrClosedPipe)
			}}, nil
		}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		requireCode(t, codeTerminalLimit, err)
		_, _, _, raw, _ := e.snapshot()
		assert.Zero(t, raw)
	})
	t.Run("never prints what it sends", func(t *testing.T) {
		e := newTermEnv(t)
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
			done <- err
		}()
		_, err := e.stdinW.Write([]byte("s3cret-token"))
		require.NoError(t, err)
		require.Eventually(t, func() bool { return len(e.inputBytes()) == len("s3cret-token") }, 5*time.Second, time.Millisecond)
		e.frames <- termFrame{exit: exited(0)}
		require.NoError(t, <-done)
		assert.Empty(t, e.stdout.String())
	})
}

// startRun runs a terminal in the background and waits until the signals are
// registered.
func (e *termEnv) startRun(ctx context.Context) <-chan runResult {
	done := make(chan runResult, 1)
	go func() {
		exit, err := e.app.runRemoteTerminal(ctx, "linux-server", terminalShellArgs())
		done <- runResult{exit: exit, err: err}
	}()
	e.waitForSignals()
	return done
}

type runResult struct {
	exit envproto.TerminalExit
	err  error
}

func (e *termEnv) waitForSignals() {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		ready := e.winch != nil
		e.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *termEnv) signals() chan<- os.Signal {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.winch
}

func waitRun(t *testing.T, done <-chan runResult) runResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the run didn't end")
		return runResult{}
	}
}

func TestApp_runRemoteTerminal_signals(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT} {
		t.Run("a "+sig.String()+" during the resize pause ends the run", func(t *testing.T) {
			e := newTermEnv(t)
			e.app.pause = func(_ context.Context, d time.Duration) {
				if d == resizeCoalesce {
					e.signals() <- sig
				}
			}
			done := e.startRun(t.Context())
			e.signals() <- syscall.SIGWINCH
			r := waitRun(t, done)
			require.NoError(t, r.err)
			assert.Equal(t, "closed", r.exit.Reason)
			_, resizes, closed, _, restored := e.snapshot()
			assert.Empty(t, resizes)
			assert.Equal(t, 1, closed)
			assert.Equal(t, 1, restored)
		})
	}
	t.Run("a SIGWINCH storm sends one resize", func(t *testing.T) {
		e := newTermEnv(t)
		e.app.pause = func(_ context.Context, d time.Duration) {
			if d == resizeCoalesce {
				for range 3 {
					e.signals() <- syscall.SIGWINCH
				}
			}
		}
		done := e.startRun(t.Context())
		e.signals() <- syscall.SIGWINCH
		require.Eventually(t, func() bool {
			_, resizes, _, _, _ := e.snapshot()
			return len(resizes) == 1
		}, 5*time.Second, time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		_, resizes, _, _, _ := e.snapshot()
		assert.Len(t, resizes, 1)
		e.frames <- termFrame{exit: exited(0)}
		require.NoError(t, waitRun(t, done).err)
	})
	t.Run("a failed resize ends the run and restores the terminal", func(t *testing.T) {
		e := newTermEnv(t)
		e.term.ResizeFunc = func(int, int) error { return io.ErrClosedPipe }
		done := e.startRun(t.Context())
		e.signals() <- syscall.SIGWINCH
		r := waitRun(t, done)
		requireCode(t, codeInternal, r.err)
		assert.Contains(t, r.err.Error(), "sending the terminal's size")
		_, _, closed, _, restored := e.snapshot()
		assert.Equal(t, 1, closed)
		assert.Equal(t, 1, restored)
	})
	t.Run("a resize waits exactly the coalescing time", func(t *testing.T) {
		e := newTermEnv(t)
		done := e.startRun(t.Context())
		e.signals() <- syscall.SIGWINCH
		require.Eventually(t, func() bool {
			_, resizes, _, _, _ := e.snapshot()
			return len(resizes) == 1
		}, 5*time.Second, time.Millisecond)
		e.frames <- termFrame{exit: exited(0)}
		require.NoError(t, waitRun(t, done).err)
		e.mu.Lock()
		defer e.mu.Unlock()
		assert.Equal(t, []time.Duration{100 * time.Millisecond}, e.pauses)
	})
	t.Run("registers the four signals and stops them on every return", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: exited(0)}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.NoError(t, err)
		e.mu.Lock()
		defer e.mu.Unlock()
		assert.ElementsMatch(t, []os.Signal{syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT}, e.notified)
		assert.Equal(t, 1, e.stopped)
	})
	t.Run("stops the signals when raw mode fails", func(t *testing.T) {
		e := newTermEnv(t)
		e.app.rawMode = func() (func(), error) { return nil, usageErrorf("no tty") }
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		requireUsageError(t, err)
		e.mu.Lock()
		defer e.mu.Unlock()
		assert.Equal(t, 1, e.stopped)
	})
}

func TestApp_runRemoteTerminal_cancel(t *testing.T) {
	t.Run("waits for the output reader and reports closed", func(t *testing.T) {
		e := newTermEnv(t)
		var nextReturned atomic.Bool
		e.term.NextFunc = func() ([]byte, *envproto.TerminalExit, error) {
			f := <-e.frames
			nextReturned.Store(true)
			return f.data, f.exit, f.err
		}
		e.term.CloseFunc = func() error {
			e.mu.Lock()
			e.closed++
			e.mu.Unlock()
			return nil
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := e.startRun(ctx)
		cancel()
		select {
		case <-done:
			require.FailNow(t, "returned before the reader ended")
		case <-time.After(100 * time.Millisecond):
		}
		assert.False(t, nextReturned.Load())
		e.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "closed"}}
		r := waitRun(t, done)
		require.NoError(t, r.err)
		assert.Equal(t, "closed", r.exit.Reason)
		assert.True(t, nextReturned.Load())
	})
}

func TestApp_currentSize(t *testing.T) {
	cases := []struct {
		name       string
		cols, rows int
		err        error
		want       [2]int
	}{
		{"the terminal's size", 100, 30, nil, [2]int{100, 30}},
		{"the smallest size", 1, 1, nil, [2]int{1, 1}},
		{"the largest size", 1000, 1000, nil, [2]int{1000, 1000}},
		{"an error", 100, 30, io.ErrClosedPipe, [2]int{80, 24}},
		{"zero columns", 0, 30, nil, [2]int{80, 24}},
		{"zero rows", 100, 0, nil, [2]int{80, 24}},
		{"negative columns", -1, 30, nil, [2]int{80, 24}},
		{"negative rows", 100, -1, nil, [2]int{80, 24}},
		{"too many columns", 1001, 30, nil, [2]int{80, 24}},
		{"too many rows", 100, 1001, nil, [2]int{80, 24}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _, _ := newTestApp()
			app.termSize = func() (int, int, error) { return tc.cols, tc.rows, tc.err }
			cols, rows := app.currentSize()
			assert.Equal(t, tc.want, [2]int{cols, rows})
		})
	}
	t.Run("the open args carry 80x24 when the size is unknown", func(t *testing.T) {
		e := newTermEnv(t)
		e.app.termSize = func() (int, int, error) { return 0, 0, io.ErrClosedPipe }
		e.frames <- termFrame{exit: exited(0)}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		require.NoError(t, err)
		require.Len(t, e.opened, 1)
		assert.Equal(t, 80, e.opened[0].Cols)
		assert.Equal(t, 24, e.opened[0].Rows)
	})
}

func TestExitError(t *testing.T) {
	cases := []struct {
		reason string
		code   errorCode
		msg    string
	}{
		{"exited", "", ""},
		{"closed", "", ""},
		{"disconnected", codeEnvironmentOffline, "linux-server went offline; the agent keeps running"},
		{"idle", codeInternal, "linux-server closed the terminal: it was idle for too long"},
		{"input_gap", codeInternal, "linux-server closed the terminal: its input arrived out of order (input_gap)"},
		{"input_limit", codeInternal, "linux-server closed the terminal: the input came faster than it accepts (input_limit)"},
		{"strange", codeInternal, "linux-server closed the terminal: strange"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			err := exitError("linux-server", &envproto.TerminalExit{Reason: tc.reason})
			if tc.code == "" {
				require.NoError(t, err)
				return
			}
			requireCode(t, tc.code, err)
			assert.Equal(t, tc.msg, err.Error())
		})
	}
	t.Run("the daemon's connection ending is internal", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{err: io.EOF}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", terminalShellArgs())
		requireCode(t, codeInternal, err)
		assert.Contains(t, err.Error(), "the connection to the daemon ended")
	})
}
