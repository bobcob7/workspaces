package lifetime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// removeOrphans runs `claude rm` on every background session whose
// working directory is exactly one of workspace ws's session directories,
// as the deletion guard resolves them (ADRs 0012, 0025, 0028): a session
// left by a `ws claude` killed mid-start, or one whose id couldn't be
// read. Both sides are compared with symlinks resolved, as `ws claude`
// finds its session. A session in any other directory, including one
// below them, is never touched.
func (m *Manager) removeOrphans(ctx context.Context, ws *workspace.Workspace, sessions []claude.Session) error {
	dir, err := m.store.GuardedDir(ws.ID)
	if err != nil {
		return err
	}
	dirs := sessionDirs(ws, dir)
	for i := range sessions {
		s := &sessions[i]
		if !filepath.IsAbs(s.Cwd) || !slices.Contains(dirs, resolved(s.Cwd)) {
			continue
		}
		err := m.claude.Remove(ctx, s.ID)
		if err != nil && !errors.Is(err, claude.ErrNoSession) {
			return fmt.Errorf("removing Claude Code session %s: %w", s.ID, err)
		}
		m.log.InfoContext(ctx, "removed orphaned Claude Code session", slog.String("session", s.ID), slog.String("workspace", ws.ID))
	}
	return nil
}

// resolved is dir with symlinks resolved, or cleaned when it can't be.
func resolved(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}
