package ptyproc

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// cmdExe is cmd.exe's absolute path: Spec.Path is never searched for.
func cmdExe(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SystemRoot")
	require.NotEmpty(t, root, "SystemRoot is unset")
	return filepath.Join(root, "System32", "cmd.exe")
}

// startCmd runs cmd.exe with args on a pseudoconsole, closes it when the
// test ends, and fails the test if that Close takes longer than 15 s.
func startCmd(t *testing.T, args ...string) *Proc {
	t.Helper()
	p, err := Start(Spec{Path: cmdExe(t), Args: args, Dir: t.TempDir(), Env: os.Environ(), Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { closeWithin(t, p, 15*time.Second) })
	return p
}

// closeWithin calls p.Close and fails the test if it takes longer than d.
func closeWithin(t *testing.T, p *Proc, d time.Duration) {
	t.Helper()
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(d):
		t.Errorf("Close did not return within %s", d)
	}
}

// readWait bounds readAll.
const readWait = 15 * time.Second

// readAll reads p to EOF in the background and fails the test after readWait.
func readAll(t *testing.T, p *Proc) string {
	t.Helper()
	type result struct {
		out []byte
		err error
	}
	got := make(chan result, 1)
	go func() {
		out, err := io.ReadAll(p)
		got <- result{out, err}
	}()
	select {
	case r := <-got:
		require.NoError(t, r.err, "ReadAll ends at io.EOF")
		return string(r.out)
	case <-time.After(readWait):
		require.FailNow(t, "the output never reached EOF", "within %s", readWait)
		return ""
	}
}

// readUntil reads p until its output contains want, failing after d.
func readUntil(t *testing.T, p *Proc, want string, d time.Duration) string {
	t.Helper()
	var out bytes.Buffer
	found := make(chan struct{})
	go func() {
		defer close(found)
		buf := make([]byte, 4096)
		for !strings.Contains(out.String(), want) {
			n, err := p.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-found:
	case <-time.After(d):
		_ = p.Close()
		<-found
		require.FailNow(t, "output never contained "+want, "got %q", out.String())
	}
	return out.String()
}

// waitWithin returns p.Wait's code, failing the test after d.
func waitWithin(t *testing.T, p *Proc, d time.Duration) *int {
	t.Helper()
	got := make(chan *int, 1)
	go func() { got <- p.Wait() }()
	select {
	case code := <-got:
		return code
	case <-time.After(d):
		require.FailNow(t, "Wait did not return", "within %s", d)
		return nil
	}
}

func TestStart_windows(t *testing.T) {
	t.Run("output, exit code 0 and io.EOF", func(t *testing.T) {
		p := startCmd(t, "/c", "echo hello")
		assert.Contains(t, readAll(t, p), "hello")
		code := waitWithin(t, p, 10*time.Second)
		require.NotNil(t, code)
		assert.Equal(t, 0, *code)
	})
	t.Run("the exit code is returned", func(t *testing.T) {
		p := startCmd(t, "/c", "exit 7")
		_ = readAll(t, p)
		code := waitWithin(t, p, 10*time.Second)
		require.NotNil(t, code)
		assert.Equal(t, 7, *code)
	})
	t.Run("input reaches an interactive cmd", func(t *testing.T) {
		p := startCmd(t)
		_, err := p.Write([]byte("echo mark^er\r\n"))
		require.NoError(t, err)
		assert.Contains(t, readUntil(t, p, "marker", 15*time.Second), "marker")
	})
	t.Run("resize is seen by the program", func(t *testing.T) {
		p := startCmd(t)
		require.NoError(t, p.Resize(100, 30))
		_, err := p.Write([]byte("mode con\r\n"))
		require.NoError(t, err)
		// ConPTY may draw a run of spaces as a cursor move (ESC [ n C).
		assert.Regexp(t, `Columns:(?:\s|\x1b\[[0-9;]*[A-Za-z])*100\b`, readUntil(t, p, "Code page", 15*time.Second))
	})
	t.Run("a nil environment holds only SYSTEMROOT", func(t *testing.T) {
		t.Setenv("PTYPROC_SECRET", "leaked")
		p, err := Start(Spec{Path: cmdExe(t), Args: []string{"/c", "set"}, Dir: t.TempDir(), Cols: 200, Rows: 24})
		require.NoError(t, err)
		t.Cleanup(func() { closeWithin(t, p, 15*time.Second) })
		out := readAll(t, p)
		assert.NotContains(t, out, "PTYPROC_SECRET")
		assert.Contains(t, strings.ToUpper(out), "SYSTEMROOT=")
	})
}

func TestClose_windows(t *testing.T) {
	t.Run("ends a running program within seconds, twice", func(t *testing.T) {
		p := startCmd(t, "/c", "ping -n 30 127.0.0.1")
		readUntil(t, p, "127.0.0.1", 15*time.Second)
		started := time.Now()
		closeWithin(t, p, 15*time.Second)
		assert.Less(t, time.Since(started), 15*time.Second)
		code := waitWithin(t, p, 5*time.Second)
		if code != nil {
			assert.NotZero(t, *code)
		}
		closeWithin(t, p, time.Second)
		_, err := p.Write([]byte("x"))
		require.Error(t, err, "Write after Close fails")
		require.Error(t, p.Resize(90, 30), "Resize after Close fails")
	})
	t.Run("ends the program's own children", func(t *testing.T) {
		p := startCmd(t, "/c", "ping -n 60 127.0.0.1")
		readUntil(t, p, "127.0.0.1", 15*time.Second)
		child := childNamed(t, uint32(p.process.Pid), "PING.EXE")
		defer func() { _ = windows.CloseHandle(child) }()
		closeWithin(t, p, 15*time.Second)
		event, err := windows.WaitForSingleObject(child, 10000)
		require.NoError(t, err)
		assert.Equal(t, uint32(windows.WAIT_OBJECT_0), event, "ping.exe outlived Close")
	})
	t.Run("an interactive cmd nobody reads", func(t *testing.T) {
		p := startCmd(t)
		closeWithin(t, p, 15*time.Second)
		waitWithin(t, p, 5*time.Second)
	})
	t.Run("resize after exit, before Close", func(t *testing.T) {
		p := startCmd(t, "/c", "exit 0")
		_ = readAll(t, p)
		waitWithin(t, p, 10*time.Second)
		assert.NoError(t, p.Resize(90, 30))
	})
}

// childNamed opens the child of parent whose image is name, with SYNCHRONIZE access.
func childNamed(t *testing.T, parent uint32, name string) windows.Handle {
	t.Helper()
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	require.NoError(t, err)
	defer func() { _ = windows.CloseHandle(snap) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if entry.ParentProcessID == parent && strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, entry.ProcessID)
			require.NoError(t, err)
			return h
		}
	}
	require.FailNow(t, "no child "+name+" of the process")
	return 0
}

func TestEnvBlock(t *testing.T) {
	block, err := envBlock([]string{"A=1", "B=2"})
	require.NoError(t, err)
	assert.Equal(t, append(windows.StringToUTF16("A=1"), windows.StringToUTF16("B=2")...), append(block[:0:0], block[:len(block)-1]...))
	empty, err := envBlock(nil)
	require.NoError(t, err)
	assert.Equal(t, []uint16{0, 0}, empty)
	_, err = envBlock([]string{"A=\x00"})
	require.Error(t, err)
}

func TestWithSystemRoot(t *testing.T) {
	assert.Equal(t, []string{"SYSTEMROOT=C:\\Windows"}, withSystemRoot(nil, `C:\Windows`))
	assert.Equal(t, []string{"SystemRoot=D:\\W"}, withSystemRoot([]string{"SystemRoot=D:\\W"}, `C:\Windows`))
}

func TestConsoleSize(t *testing.T) {
	assert.Equal(t, windows.Coord{X: 1, Y: 1}, consoleSize(0, -5))
	assert.Equal(t, windows.Coord{X: 120, Y: 40}, consoleSize(120, 40))
	assert.Equal(t, windows.Coord{X: 32767, Y: 32767}, consoleSize(1<<20, 1<<20))
}

func TestDedupEnv(t *testing.T) {
	assert.Equal(t, []string{"b=2", "Path=C:\\new", "=C:=C:\\ws"}, dedupEnv([]string{"PATH=C:\\old", "b=2", "Path=C:\\new", "=C:=C:\\ws"}))
	assert.Empty(t, dedupEnv(nil))
}
