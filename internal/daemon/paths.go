package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// Paths are a CONFIG_DIR's daemon files. AgentSocket is the environment
// agent's socket, beside Socket (ADR 0082).
type Paths struct{ Dir, Lock, Info, Log, Socket, AgentSocket string }

// PathsFor returns CONFIG_DIR's daemon files. The socket is in
// $XDG_RUNTIME_DIR/ws when that is set, else in ${TMPDIR:-/tmp}/ws-<uid>,
// and is named by the first 12 hex digits of SHA-256 of the absolute
// CONFIG_DIR, so two config directories never share a socket.
func PathsFor(configDir string, getenv func(string) string, uid int) Paths {
	dir := filepath.Join(configDir, "daemon")
	socket := socketPath(configDir, getenv, uid)
	return Paths{
		Dir:         dir,
		Lock:        filepath.Join(dir, "lock"),
		Info:        filepath.Join(dir, "daemon.json"),
		Log:         filepath.Join(dir, "daemon.log"),
		Socket:      socket,
		AgentSocket: strings.TrimSuffix(socket, ".sock") + "-agent.sock",
	}
}

func socketPath(configDir string, getenv func(string) string, uid int) string {
	name := socketName(configDir) + ".sock"
	if runtimeDir := getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "ws", name)
	}
	return filepath.Join(socketBaseDir(getenv, uid), name)
}

func socketName(configDir string) string {
	abs, err := filepath.Abs(configDir)
	if err != nil {
		abs = filepath.Clean(configDir)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:12]
}
