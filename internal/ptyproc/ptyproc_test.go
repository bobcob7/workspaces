//go:build linux || darwin

package ptyproc

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	t.Parallel()
	t.Run("runs a program and reads its output", func(t *testing.T) {
		t.Parallel()
		p := startProc(t, "/bin/sh", "-c", "printf hi")
		out, err := io.ReadAll(p)
		require.NoError(t, err)
		assert.Equal(t, "hi", string(out))
	})
	t.Run("write reaches the program", func(t *testing.T) {
		t.Parallel()
		p := startProc(t, "/bin/cat")
		_, err := p.Write([]byte("abc\n"))
		require.NoError(t, err)
		out := readUntil(t, p, "abc\r\nabc")
		assert.Contains(t, out, "abc\r\nabc", "the echo and cat's own output")
	})
	t.Run("resize is seen by the program", func(t *testing.T) {
		t.Parallel()
		p := startProc(t, "/bin/sh", "-c", "read x; stty size")
		require.NoError(t, p.Resize(100, 40))
		_, err := p.Write([]byte("\n"))
		require.NoError(t, err)
		out := readUntil(t, p, "40 100")
		assert.Contains(t, out, "40 100")
	})
	t.Run("close ends a program that ignores input", func(t *testing.T) {
		t.Parallel()
		p := startProc(t, "/bin/sleep", "60")
		started := time.Now()
		closed := make(chan error, 1)
		go func() { closed <- p.Close() }()
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			require.FailNow(t, "Close did not return within 3 s")
		}
		assert.Less(t, time.Since(started), 3*time.Second)
		if code := p.Wait(); code != nil {
			assert.NotZero(t, *code)
		}
	})
	t.Run("the exit code is returned", func(t *testing.T) {
		t.Parallel()
		p := startProc(t, "/bin/sh", "-c", "exit 3")
		code := p.Wait()
		require.NotNil(t, code)
		assert.Equal(t, 3, *code)
	})
}

// TestStartSetWinsizeError swaps a package variable, so it must not run in parallel.
func TestStartSetWinsizeError(t *testing.T) {
	before := childPIDs(t)
	setWinsizeFn = func(*os.File, int, int) error { return errors.New("no window size") }
	t.Cleanup(func() { setWinsizeFn = setWinsize })
	p, err := Start(Spec{Path: "/bin/sh", Args: []string{"-c", "exit 0"}, Dir: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin"}, Cols: 80, Rows: 24})
	require.Error(t, err)
	assert.Nil(t, p)
	assert.Subset(t, before, childPIDs(t), "a child ran after the window size failed")
}

// childPIDs lists the pids of this process's children, read from /proc.
func childPIDs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skip("no /proc")
	}
	self := strconv.Itoa(os.Getpid())
	var pids []string
	for _, entry := range entries {
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		// The command name is in parentheses and may hold spaces, so the fields follow the last ')'.
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) > 1 && fields[1] == self {
			pids = append(pids, entry.Name())
		}
	}
	return pids
}

func TestWinsizeField(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint16(0), winsizeField(-1))
	assert.Equal(t, uint16(40), winsizeField(40))
	assert.Equal(t, uint16(math.MaxUint16), winsizeField(math.MaxUint16+1))
}

// startProc runs path with args on a pty and closes it when the test ends.
func startProc(t *testing.T, path string, args ...string) *Proc {
	t.Helper()
	p, err := Start(Spec{
		Path: path,
		Args: args,
		Dir:  t.TempDir(),
		Env:  []string{"PATH=/usr/bin:/bin"},
		Cols: 80,
		Rows: 24,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// readUntil reads p until its output contains want, or fails after five
// seconds by closing p, which ends the read.
func readUntil(t *testing.T, p *Proc, want string) string {
	t.Helper()
	timeout := time.AfterFunc(5*time.Second, func() { _ = p.Close() })
	defer timeout.Stop()
	var out bytes.Buffer
	buf := make([]byte, 256)
	for !bytes.Contains(out.Bytes(), []byte(want)) {
		n, err := p.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return out.String()
}

func TestEnvironment(t *testing.T) {
	t.Setenv("PTYPROC_SECRET", "leaked")
	for name, env := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name+" env gives an empty environment", func(t *testing.T) {
			p, err := Start(Spec{Path: "/usr/bin/env", Dir: t.TempDir(), Env: env, Cols: 80, Rows: 24})
			require.NoError(t, err)
			t.Cleanup(func() { _ = p.Close() })
			out, err := io.ReadAll(p)
			require.NoError(t, err)
			assert.NotContains(t, string(out), "PTYPROC_SECRET", "the parent environment leaked")
		})
	}
}

func TestInitialSize(t *testing.T) {
	t.Parallel()
	for i := range 50 {
		p, err := Start(Spec{Path: "/bin/sh", Args: []string{"-c", "stty size"}, Dir: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin"}, Cols: 100, Rows: 40})
		require.NoError(t, err)
		out, err := io.ReadAll(p)
		require.NoError(t, err)
		_ = p.Close()
		require.Contains(t, string(out), "40 100", "iteration %d", i)
	}
}

func TestCloseAfterExit(t *testing.T) {
	t.Parallel()
	p := startProc(t, "/bin/sh", "-c", "exit 0")
	require.NotNil(t, p.Wait())
	var signals atomic.Int32
	p.signal = func(int, syscall.Signal) error {
		signals.Add(1)
		return nil
	}
	require.NoError(t, p.Close())
	assert.Zero(t, signals.Load(), "no signal may go to a reaped pid")
}

func TestCloseIsRepeatableAndConcurrent(t *testing.T) {
	t.Parallel()
	p := startProc(t, "/bin/sleep", "60")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = p.Close() })
	}
	wg.Wait()
	assert.NoError(t, p.Close())
}

func TestCloseEscalatesToKill(t *testing.T) {
	t.Parallel()
	p := startProc(t, "/bin/sh", "-c", "trap '' HUP; echo ready; while :; do sleep 1; done")
	readUntil(t, p, "ready")
	started := time.Now()
	require.NoError(t, p.Close())
	assert.GreaterOrEqual(t, time.Since(started), killGrace-100*time.Millisecond)
	assert.Nil(t, p.Wait(), "a signal ended it")
}

func TestNoDescriptorLeak(t *testing.T) {
	before := openFDs(t)
	for range 50 {
		p, err := Start(Spec{Path: "/bin/sh", Args: []string{"-c", "exit 0"}, Dir: t.TempDir(), Env: []string{}, Cols: 80, Rows: 24})
		require.NoError(t, err)
		p.Wait()
		require.NoError(t, p.Close())
	}
	assert.LessOrEqual(t, openFDs(t), before)
}

func TestIOAfterExitAndClose(t *testing.T) {
	t.Parallel()
	p := startProc(t, "/bin/sh", "-c", "exit 0")
	p.Wait()
	assert.NoError(t, p.Resize(90, 30), "Resize after exit still works")
	require.NoError(t, p.Close())
	require.Error(t, p.Resize(90, 30), "Resize after Close fails")
	_, err := p.Write([]byte("x"))
	require.Error(t, err, "Write after Close fails")
}

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc/self/fd")
	}
	return len(entries)
}
