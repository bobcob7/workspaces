package claude

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	errBadID  = errors.New("invalid session id")
	errNoHome = errors.New("neither CLAUDE_CONFIG_DIR nor HOME is set")
)

// Stop stops a background session with `claude stop`. Its conversation is
// kept, and stopping a stopped session succeeds. An unknown id fails with
// ErrNoSession.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.sessionCommand(ctx, "stop", id)
}

// Remove deletes a background session with `claude rm`, running or not.
// Its transcript stays available through `claude --resume`. An unknown id
// fails with ErrNoSession.
func (c *Client) Remove(ctx context.Context, id string) error {
	return c.sessionCommand(ctx, "rm", id)
}

// Respawn restarts a background session, running or stopped, with `claude
// respawn`, resuming its conversation in a new process. It starts Claude
// Code's background service, with this Client's environment, if none is
// running (ADR 0018). claude runs in a process group of its own, so a
// respawn that has begun ends only with ctx. An unknown id fails with
// ErrNoSession.
func (c *Client) Respawn(ctx context.Context, id string) error {
	if !validID(id) {
		return fmt.Errorf("%w: %q", errBadID, id)
	}
	_, err := c.runApart(ctx, "respawn", id)
	return err
}

// sessionCommand runs `claude <command> <id>`. Its exit status decides
// success; what it prints ("stopped <id>") isn't relied on.
func (c *Client) sessionCommand(ctx context.Context, command, id string) error {
	if !validID(id) {
		return fmt.Errorf("%w: %q", errBadID, id)
	}
	_, err := c.run(ctx, command, id)
	return err
}

// JobExists reports whether Claude Code still has the session's directory,
// <jobs dir>/<id>, one of the two signals that a session is gone
// (ADR 0015).
func (c *Client) JobExists(id string) (bool, error) {
	if !validID(id) {
		return false, fmt.Errorf("%w: %q", errBadID, id)
	}
	if c.jobsDir == "" {
		return false, errNoHome
	}
	_, err := os.Stat(filepath.Join(c.jobsDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking Claude Code session %s: %w", id, err)
	}
	return true, nil
}

// validID reports whether id can be a session id: not empty, not an
// option, and a single path element.
func validID(id string) bool {
	return id != "" && !strings.HasPrefix(id, "-") && !strings.ContainsAny(id, `/\`) && id != "." && id != ".."
}
