package daemon

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathsFor(t *testing.T) {
	t.Parallel()
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	t.Run("lays out the files under CONFIG_DIR/daemon", func(t *testing.T) {
		cfg := t.TempDir()
		p := PathsFor(cfg, env(nil), 1000)
		assert.Equal(t, filepath.Join(cfg, "daemon"), p.Dir)
		assert.Equal(t, filepath.Join(cfg, "daemon", "lock"), p.Lock)
		assert.Equal(t, filepath.Join(cfg, "daemon", "daemon.json"), p.Info)
		assert.Equal(t, filepath.Join(cfg, "daemon", "daemon.log"), p.Log)
	})
	t.Run("uses XDG_RUNTIME_DIR for the socket when set", func(t *testing.T) {
		p := PathsFor("/cfg", env(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}), 1000)
		assert.Regexp(t, `^/run/user/1000/ws/[0-9a-f]{12}\.sock$`, p.Socket)
	})
	t.Run("falls back to TMPDIR, then /tmp, per uid", func(t *testing.T) {
		withTmp := PathsFor("/cfg", env(map[string]string{"TMPDIR": "/var/tmp"}), 501)
		assert.Regexp(t, `^/var/tmp/ws-501/[0-9a-f]{12}\.sock$`, withTmp.Socket)
		noTmp := PathsFor("/cfg", env(nil), 501)
		assert.Regexp(t, `^/tmp/ws-501/[0-9a-f]{12}\.sock$`, noTmp.Socket)
	})
	t.Run("gives two config directories two sockets", func(t *testing.T) {
		a := PathsFor("/cfg-a", env(nil), 1000)
		b := PathsFor("/cfg-b", env(nil), 1000)
		assert.NotEqual(t, a.Socket, b.Socket)
	})
	t.Run("names the socket by the absolute config directory", func(t *testing.T) {
		rel := PathsFor("cfg", env(nil), 1000)
		abs := PathsFor(mustAbs(t, "cfg"), env(nil), 1000)
		assert.Equal(t, rel.Socket, abs.Socket)
	})
}
