package claude

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
)

// Attach replaces ws with `claude attach <id>`, which opens the background
// session in this terminal, with ws's stdin, stdout, stderr, and
// environment (ADR 0025). It returns only when claude can't be run; then
// ws carries on. Once claude runs, its exit status is ws's, and leaving
// it keeps the session running. Nothing ws deferred runs after a
// successful Attach: the caller releases what it holds first.
func (c *Client) Attach(ctx context.Context, id string) error {
	if !validID(id) {
		return fmt.Errorf("%w: %q", errBadID, id)
	}
	if err := c.ensureVersion(ctx); err != nil {
		return err
	}
	path, err := exec.LookPath(c.path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	argv := []string{c.path, "attach", id}
	c.log.DebugContext(ctx, "claude", slog.Any("args", argv[1:]), slog.String("exec", path))
	if err := c.replace(path, argv, append([]string{}, c.environ...)); err != nil {
		return fmt.Errorf("running claude attach %s: %w", id, err)
	}
	return nil
}
