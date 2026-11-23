//go:build linux

package mqttclient_test

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/mqttbroker"
	"github.com/bobcob7/workspaces/internal/mqttclient"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// connectedLowWater connects a real client to the real broker over mutual TLS
// with NotSentLowWater n, and returns TCP_NOTSENT_LOWAT of the client's socket.
func connectedLowWater(t *testing.T, n int) int {
	t.Helper()
	dir := t.TempDir()
	store := environment.NewStore(filepath.Join(dir, "environments"), filepath.Join(dir, "state"), time.Now, rand.Reader)
	_, err := store.EnsureCA(t.Context())
	require.NoError(t, err)
	_, _, _, err = store.Add(t.Context(), "bootstrap", "127.0.0.1:1")
	require.NoError(t, err)
	broker := mqttbroker.New(discardLog())
	settings := environment.Settings{Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", PublicName: "127.0.0.1", RenewBeforeDays: 30}
	ctrl := environment.NewController(discardLog(), store, broker, settings, time.Now)
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	require.NoError(t, ctrl.Start(t.Context()))
	_, code, _, err := store.Add(t.Context(), "linux", ctrl.Status().EnrollListen)
	require.NoError(t, err)
	files := envagent.NewFiles(filepath.Join(t.TempDir(), "environment"))
	id, err := envagent.NewEnroller(discardLog(), files, rand.Reader).Enroll(t.Context(), code)
	require.NoError(t, err)
	client := mqttclient.New(discardLog())
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	var dialed net.Conn
	client.SetDialHook(func(c net.Conn) { dialed = c })
	require.NoError(t, client.Connect(t.Context(), envproto.ConnectOptions{
		Address:         ctrl.Status().Listen,
		TLS:             id.TLSConfig(),
		ClientID:        id.ID,
		KeepAlive:       30 * time.Second,
		NotSentLowWater: n,
	}))
	tlsConn, ok := dialed.(*tls.Conn)
	require.True(t, ok)
	tcp, ok := tlsConn.NetConn().(*net.TCPConn)
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

func TestClient_Connect_notSentLowWater(t *testing.T) {
	t.Run("sets TCP_NOTSENT_LOWAT on the real connection to the broker", func(t *testing.T) {
		assert.Equal(t, 16384, connectedLowWater(t, 16384))
	})
	t.Run("leaves the OS default when 0", func(t *testing.T) {
		assert.NotEqual(t, 16384, connectedLowWater(t, 0))
	})
}
