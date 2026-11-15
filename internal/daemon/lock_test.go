package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTakeLock(t *testing.T) {
	t.Parallel()
	t.Run("a second lock on the same config directory is ErrRunning with the holder's pid", func(t *testing.T) {
		p := testPaths(t)
		require.NoError(t, writeInfo(p.Info, Info{PID: 4242, Origin: OriginManaged}))
		release, err := takeLock(p)
		require.NoError(t, err)
		defer release()
		_, err = takeLock(p)
		require.ErrorIs(t, err, ErrRunning)
		running, ok := errors.AsType[*RunningError](err)
		require.True(t, ok)
		assert.Equal(t, 4242, running.PID)
	})
	t.Run("a released lock can be taken again", func(t *testing.T) {
		p := testPaths(t)
		release, err := takeLock(p)
		require.NoError(t, err)
		release()
		again, err := takeLock(p)
		require.NoError(t, err)
		again()
	})
	t.Run("a holder whose daemon.json is missing has pid 0", func(t *testing.T) {
		p := testPaths(t)
		release, err := takeLock(p)
		require.NoError(t, err)
		defer release()
		_, err = takeLock(p)
		running, ok := errors.AsType[*RunningError](err)
		require.True(t, ok)
		assert.Equal(t, 0, running.PID)
	})
}

func TestWriteInfo(t *testing.T) {
	t.Parallel()
	t.Run("writes daemon.json with mode 0600 and reads it back", func(t *testing.T) {
		p := testPaths(t)
		started := time.Date(2026, 11, 14, 8, 0, 3, 0, time.UTC)
		want := Info{Version: 1, PID: 7, StartedAt: started, Origin: OriginApp, Socket: p.Socket, WSVersion: "v0.31.0"}
		require.NoError(t, writeInfo(p.Info, want))
		got, err := readInfo(p.Info)
		require.NoError(t, err)
		assert.Equal(t, want.PID, got.PID)
		assert.True(t, want.StartedAt.Equal(got.StartedAt))
		assert.Equal(t, want.WSVersion, got.WSVersion)
		info, err := os.Stat(p.Info)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("fails when the directory doesn't exist", func(t *testing.T) {
		err := writeInfo(filepath.Join(t.TempDir(), "missing", "daemon.json"), Info{})
		require.Error(t, err)
	})
}

func TestReadInfo(t *testing.T) {
	t.Parallel()
	t.Run("a missing file is an error", func(t *testing.T) {
		_, err := readInfo(filepath.Join(t.TempDir(), "daemon.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("a file that isn't JSON is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.json")
		require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
		_, err := readInfo(path)
		require.Error(t, err)
	})
}

func TestListen(t *testing.T) {
	t.Parallel()
	t.Run("listens on a 0600 socket in a private directory", func(t *testing.T) {
		p := testPaths(t)
		ln, err := listen(t.Context(), p.Socket)
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()
		info, err := os.Stat(p.Socket)
		require.NoError(t, err)
		assert.Equal(t, os.ModeSocket|0o600, info.Mode())
		dir, err := os.Stat(filepath.Dir(p.Socket))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
	})
	t.Run("replaces a stale socket", func(t *testing.T) {
		p := testPaths(t)
		require.NoError(t, prepareSocketDir(filepath.Dir(p.Socket)))
		require.NoError(t, os.WriteFile(p.Socket, nil, 0o600))
		ln, err := listen(t.Context(), p.Socket)
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()
		info, err := os.Stat(p.Socket)
		require.NoError(t, err)
		assert.Equal(t, os.ModeSocket, info.Mode()&os.ModeSocket)
	})
	t.Run("refuses a socket directory others can open", func(t *testing.T) {
		p := testPaths(t)
		dir := filepath.Dir(p.Socket)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.Chmod(dir, 0o755))
		_, err := listen(t.Context(), p.Socket)
		require.Error(t, err)
	})
}

func TestPrepareSocketDir(t *testing.T) {
	t.Parallel()
	t.Run("creates a 0700 directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "ws")
		require.NoError(t, prepareSocketDir(dir))
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	})
	t.Run("refuses a directory that other users can enter", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o755))
		require.Error(t, prepareSocketDir(dir))
	})
}

func TestOlder(t *testing.T) {
	cases := []struct {
		name       string
		have, want string
		older      bool
	}{
		{"an older patch", "v0.31.0", "v0.31.1", true},
		{"an older minor", "v0.30.9", "v0.31.0", true},
		{"a newer major is not older", "v2.0.0", "v1.9.9", false},
		{"the same version is not older", "v0.31.0", "v0.31.0", false},
		{"a suffix is ignored", "v0.31.0-rc.1", "v0.31.1", true},
		{"dev is not older than anything", "dev", "v0.31.0", false},
		{"an unknown minimum is never exceeded", "v0.31.0", "dev", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.older, Older(tc.have, tc.want))
		})
	}
}
