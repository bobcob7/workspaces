package claude

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/bobcob7/workspaces/internal/ptyproc"
)

// windowsAttach is claude attach on a pseudoconsole.
type windowsAttach struct {
	*ptyproc.Proc
}

// startAttach runs claude attach id on a new pseudoconsole (ConPTY, ADR 0085).
func (c *Client) startAttach(_ context.Context, id string) (attachTerm, error) {
	path, err := exec.LookPath(c.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	proc, err := ptyproc.Start(ptyproc.Spec{
		Path: path,
		Args: []string{"attach", id},
		Env:  append(append([]string{}, c.environ...), "TERM=xterm-256color"),
		Cols: ptyCols,
		Rows: ptyRows,
	})
	if err != nil {
		if notInstalled(err) {
			return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
		}
		return nil, fmt.Errorf("running claude attach %s: %w", id, err)
	}
	return windowsAttach{Proc: proc}, nil
}

// hangUp closes the pseudoconsole, which ends claude attach (Close kills it
// after its own grace period, 2 s), and waits for it to exit.
func (a windowsAttach) hangUp(ctx context.Context, log *slog.Logger, _ time.Duration) {
	if err := a.Close(); err != nil {
		log.DebugContext(ctx, "claude attach's terminal didn't close cleanly", slog.Any("error", err))
	}
	a.Wait()
}
