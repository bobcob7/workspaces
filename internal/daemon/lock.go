package daemon

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// Info is daemon.json: what the running daemon says about itself.
type Info struct {
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Origin    string    `json:"origin"` // transient | managed | app
	Socket    string    `json:"socket"`
	WSVersion string    `json:"ws_version"`
}

// RunningError is ErrRunning with the pid of the daemon that holds the lock.
type RunningError struct{ PID int }

func (e *RunningError) Error() string {
	return fmt.Sprintf("%s (pid %d)", ErrRunning, e.PID)
}

// Unwrap makes a RunningError an ErrRunning.
func (*RunningError) Unwrap() error { return ErrRunning }

// takeLock holds the daemon lock until the returned function is called.
//
//nolint:gocritic // hugeParam: the contract takes Paths by value.
func takeLock(p Paths) (func(), error) {
	lock, ok, err := lockfile.TryExclusiveLock(p.Lock)
	if err != nil {
		return nil, fmt.Errorf("taking the daemon lock: %w", err)
	}
	if !ok {
		return nil, &RunningError{PID: holderPID(p.Info)}
	}
	return lock.Unlock, nil
}

// holderPID is the pid in daemon.json, or 0 when it can't be read.
func holderPID(path string) int {
	info, err := readInfo(path)
	if err != nil {
		return 0
	}
	return info.PID
}

func readInfo(path string) (Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return info, nil
}

// writeInfo replaces daemon.json through a temp file and a rename (0600).
//
//nolint:gocritic // hugeParam: Info is passed by value, as its json fields are.
func writeInfo(path string, info Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encoding daemon.json: %w", err)
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// listen prepares the socket's directory and listens on socket, which is
// 0600. A socket left by a daemon that died is replaced: the lock is held.
func listen(ctx context.Context, socket string) (net.Listener, error) {
	if err := prepareSocketDir(filepath.Dir(socket)); err != nil {
		return nil, err
	}
	if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", socket, err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("securing %s: %w", socket, err)
	}
	return ln, nil
}
