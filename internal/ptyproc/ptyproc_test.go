//go:build linux || darwin

package ptyproc

import (
	"bytes"
	"io"
	"math"
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
