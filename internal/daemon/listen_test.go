//go:build unix

package daemon

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenPeer(t *testing.T) {
	t.Run("creates a private directory and a socket that accepts this user", func(t *testing.T) {
		dir := filepath.Join(shortTempDir(t), "socks")
		socket := filepath.Join(dir, "0123456789ab-agent.sock")
		ln, err := ListenPeer(t.Context(), socket, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		info, err = os.Stat(socket)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		accepted := make(chan net.Conn, 1)
		go func() {
			if conn, err := ln.Accept(); err == nil {
				accepted <- conn
			}
		}()
		conn, err := net.Dial("unix", socket)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		select {
		case c := <-accepted:
			_ = c.Close()
		case <-time.After(5 * time.Second):
			t.Fatal("this user's connection wasn't accepted")
		}
	})
	t.Run("replaces a stale socket file", func(t *testing.T) {
		dir := shortTempDir(t)
		require.NoError(t, os.Chmod(dir, 0o700))
		socket := filepath.Join(dir, "0123456789ab-agent.sock")
		require.NoError(t, os.WriteFile(socket, nil, 0o600))
		ln, err := ListenPeer(t.Context(), socket, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
	})
}

// shortTempDir is a temporary directory short enough for a unix socket path,
// which t.TempDir's test-name prefix can overflow.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
