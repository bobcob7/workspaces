package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// Paths are a CONFIG_DIR's daemon files.
type Paths struct{ Dir, Lock, Info, Log, Socket string }

// PathsFor returns CONFIG_DIR's daemon files. The socket is in
// $XDG_RUNTIME_DIR/ws when that is set, else in ${TMPDIR:-/tmp}/ws-<uid>,
// and is named by the first 12 hex digits of SHA-256 of the absolute
// CONFIG_DIR, so two config directories never share a socket.
func PathsFor(configDir string, getenv func(string) string, uid int) Paths {
	dir := filepath.Join(configDir, "daemon")
	return Paths{
		Dir:    dir,
		Lock:   filepath.Join(dir, "lock"),
		Info:   filepath.Join(dir, "daemon.json"),
		Log:    filepath.Join(dir, "daemon.log"),
		Socket: socketPath(configDir, getenv, uid),
	}
}

func socketPath(configDir string, getenv func(string) string, uid int) string {
	name := socketName(configDir) + ".sock"
	if runtimeDir := getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "ws", name)
	}
	tmp := getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	return filepath.Join(tmp, fmt.Sprintf("ws-%d", uid), name)
}

func socketName(configDir string) string {
	abs, err := filepath.Abs(configDir)
	if err != nil {
		abs = filepath.Clean(configDir)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:12]
}
