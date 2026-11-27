package claude

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/ptyproc"
)

// hangUpMargin is how much longer than the hang-up grace the wait for claude
// attach to exit may take before Send stops waiting for it.
const hangUpMargin = 5 * time.Second

// windowsAttach is claude attach on a pseudoconsole.
type windowsAttach struct {
	*ptyproc.Proc
}

// startAttach runs claude attach id on a new pseudoconsole (ConPTY, ADR 0086).
func (c *Client) startAttach(ctx context.Context, id string) (attachTerm, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := exec.LookPath(c.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	if isBatchFile(path) {
		return nil, fmt.Errorf("%w: %s is a batch file, which cmd.exe would parse the session id in; install claude.exe", ErrNotInstalled, path)
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

// isBatchFile reports whether path is a .cmd or .bat script (an npm shim, say),
// which cmd.exe runs instead of CreateProcess.
func isBatchFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".cmd" || ext == ".bat"
}

// hangUp closes the pseudoconsole, which ends claude attach (Close kills it
// after its own grace period, 2 s), and waits for it to exit, for at most the
// grace plus hangUpMargin.
func (a windowsAttach) hangUp(ctx context.Context, log *slog.Logger, grace time.Duration) {
	if err := a.Close(); err != nil {
		log.DebugContext(ctx, "claude attach's terminal didn't close cleanly", slog.Any("error", err))
	}
	exited := make(chan struct{})
	go func() { a.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(grace + hangUpMargin):
		log.DebugContext(ctx, "claude attach didn't exit after its terminal closed")
	}
}
