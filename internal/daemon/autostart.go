package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/procgroup"
)

// startWait is how long Ensure waits for an auto-started daemon's socket.
const startWait = 5 * time.Second

// Ensure returns a client to a running daemon. When none answers and
// autostart is set, it starts `<exe> daemon run --origin transient`
// detached and waits up to 5 s. A daemon older than minVersion is
// ErrUnavailable. Otherwise the error is ErrUnavailable too.
//
//nolint:gocritic // hugeParam: the contract takes Paths by value.
func Ensure(ctx context.Context, p Paths, exe string, autostart bool, minVersion string) (*Client, error) {
	client := Dial(p)
	st, err := client.Status(ctx)
	if err == nil {
		return checkVersion(client, st, minVersion)
	}
	if !autostart || !errors.Is(err, ErrNotRunning) {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err := startDetached(ctx, p, exe); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return waitForDaemon(ctx, p, minVersion)
}

//nolint:gocritic // hugeParam: the contract takes Paths by value.
func waitForDaemon(ctx context.Context, p Paths, minVersion string) (*Client, error) {
	client := Dial(p)
	deadline := time.NewTimer(startWait)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		st, err := client.Status(ctx)
		if err == nil {
			return checkVersion(client, st, minVersion)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
		case <-deadline.C:
			return nil, fmt.Errorf("%w: no daemon answered on %s within %s", ErrUnavailable, p.Socket, startWait)
		case <-ticker.C:
		}
	}
}

//nolint:gocritic // hugeParam: Status is passed by value, as Client.Status returns it.
func checkVersion(client *Client, st Status, minVersion string) (*Client, error) {
	if st.WSVersion == nil || !Older(*st.WSVersion, minVersion) {
		return client, nil
	}
	return nil, fmt.Errorf("%w: the running ws daemon is older (%s); run ws daemon stop --keep-runners", ErrUnavailable, *st.WSVersion)
}

// startDetached starts the transient daemon the way Claude Code's service
// is started: its own session, stdio on /dev/null, stderr appended to
// daemon.log, and the home directory as its working directory.
//
//nolint:gocritic // hugeParam: the contract takes Paths by value.
func startDetached(ctx context.Context, p Paths, exe string) error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", p.Dir, err)
	}
	log, err := os.OpenFile(p.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", p.Log, err)
	}
	defer func() { _ = log.Close() }()
	cmd := exec.CommandContext(context.WithoutCancel(ctx), exe, "daemon", "run", "--origin", OriginTransient)
	cmd.Stderr = log
	cmd.Dir = homeDir()
	cmd.SysProcAttr = procgroup.Detached()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

// Older reports whether ws version have is older than want. Versions read as
// v<major>.<minor>.<patch>, with an optional suffix; a version that doesn't
// parse, such as dev, is not older than anything.
func Older(have, want string) bool {
	h, okHave := parseSemver(have)
	w, okWant := parseSemver(want)
	if !okHave || !okWant {
		return false
	}
	for i := range h {
		if h[i] != w[i] {
			return h[i] < w[i]
		}
	}
	return false
}

func parseSemver(version string) ([3]int, bool) {
	var out [3]int
	core, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != len(out) {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
