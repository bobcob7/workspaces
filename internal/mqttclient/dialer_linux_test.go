//go:build linux

package mqttclient

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func Test_newDialer_notSentLowWater(t *testing.T) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	dial := func(t *testing.T, n int) int {
		t.Helper()
		d := newDialer(envproto.ConnectOptions{NotSentLowWater: n})
		conn, err := d.NetDialer.DialContext(context.Background(), "tcp", ln.Addr().String())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		tcp, ok := conn.(*net.TCPConn)
		require.True(t, ok)
		rc, err := tcp.SyscallConn()
		require.NoError(t, err)
		var got int
		var sockErr error
		require.NoError(t, rc.Control(func(fd uintptr) {
			got, sockErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT)
		}))
		if errors.Is(sockErr, syscall.ENOPROTOOPT) {
			t.Skip("TCP_NOTSENT_LOWAT is not supported here")
		}
		require.NoError(t, sockErr)
		return got
	}
	t.Run("sets TCP_NOTSENT_LOWAT on the dialed socket", func(t *testing.T) {
		assert.Equal(t, 16384, dial(t, 16384))
	})
	t.Run("leaves the OS default when 0", func(t *testing.T) {
		assert.NotEqual(t, 16384, dial(t, 0))
	})
}
