//go:build linux

package cli

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// openPty opens a pty pair without a dependency, and skips the test when the
// system has none. It returns the slave's fd, and the master's.
func openPty(t *testing.T) (slave, master *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	require.NoError(t, unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0))
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	require.NoError(t, err)
	slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("can't open the pty's slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return slave, master
}

func TestMakeRaw(t *testing.T) {
	t.Run("clears the cooked flags and restores them", func(t *testing.T) {
		slave, _ := openPty(t)
		fd := int(slave.Fd())
		before, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		require.NoError(t, err)
		require.NotZero(t, before.Lflag&(unix.ECHO|unix.ICANON|unix.ISIG))
		restore, err := makeRaw(fd)
		require.NoError(t, err)
		raw, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		require.NoError(t, err)
		assert.Zero(t, raw.Lflag&unix.ECHO)
		assert.Zero(t, raw.Lflag&unix.ICANON)
		assert.Zero(t, raw.Lflag&unix.ISIG)
		assert.Zero(t, raw.Oflag&unix.OPOST)
		restore()
		after, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
	t.Run("a file that isn't a terminal is a usage error", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "plain")
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })
		_, err = makeRaw(int(f.Fd()))
		requireUsageError(t, err)
	})
}

func TestTerminalSize(t *testing.T) {
	t.Run("is the size the terminal was given", func(t *testing.T) {
		slave, _ := openPty(t)
		fd := int(slave.Fd())
		require.NoError(t, unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: 132, Row: 43}))
		cols, rows, err := terminalSize(fd)
		require.NoError(t, err)
		assert.Equal(t, 132, cols)
		assert.Equal(t, 43, rows)
	})
	t.Run("a file that isn't a terminal is an error", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "plain")
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })
		_, _, err = terminalSize(int(f.Fd()))
		require.Error(t, err)
	})
}
