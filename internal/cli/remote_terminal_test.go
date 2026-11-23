package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
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
	e.app.notify = func(c chan<- os.Signal, _ ...os.Signal) {
		e.mu.Lock()
		e.winch = c
		e.mu.Unlock()
	}
	e.app.stopNotify = func(chan<- os.Signal) {}
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

func shellArgs() envproto.TerminalOpenArgs {
	return envproto.TerminalOpenArgs{Kind: envproto.TermShell, Specs: []string{}}
}

func TestApp_runRemoteTerminal(t *testing.T) {
	t.Run("copies output to stdout", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{data: []byte("hello ")}
		e.frames <- termFrame{data: []byte("world")}
		e.frames <- termFrame{exit: exited(0)}
		exit, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
		require.NoError(t, err)
		assert.Equal(t, "hello world", e.stdout.String())
		require.NotNil(t, exit.Code)
		assert.Equal(t, 0, *exit.Code)
	})
	t.Run("opens with the terminal's size", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: exited(0)}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
			_, err := e.app.runRemoteTerminal(ctx, "linux-server", shellArgs())
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
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
		require.Error(t, err)
		_, _, _, _, restored := e.snapshot()
		assert.Equal(t, 1, restored)
	})
	t.Run("restores the terminal when the control loop panics", func(t *testing.T) {
		e := newTermEnv(t)
		e.term.InputFunc = func([]byte) error { panic("boom") }
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
		requireCode(t, codeEnvironmentOffline, err)
		assert.Contains(t, err.Error(), "linux-server went offline; the agent keeps running")
	})
	t.Run("a terminal the agent closed for its input rate is an error", func(t *testing.T) {
		e := newTermEnv(t)
		e.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "input_limit"}}
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
		_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
		requireCode(t, codeTerminalLimit, err)
		_, _, _, raw, _ := e.snapshot()
		assert.Zero(t, raw)
	})
	t.Run("never prints what it sends", func(t *testing.T) {
		e := newTermEnv(t)
		done := make(chan error, 1)
		go func() {
			_, err := e.app.runRemoteTerminal(t.Context(), "linux-server", shellArgs())
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
