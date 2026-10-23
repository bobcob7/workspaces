package lifetime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

var (
	// ErrShellRunning means ws rm was asked to remove a shell workspace
	// whose shell, or something started from it, still runs.
	ErrShellRunning = errors.New("the workspace's shell is still running; exit it to remove the workspace")
	// ErrBusy means ws rm was asked to remove a workspace that is still
	// being created or handed to its session.
	ErrBusy = errors.New("the workspace is still being created or handed to its session")
	// ErrSessionElsewhere means ws rm found the workspace's recorded Claude
	// Code session listed in another directory, so it touched neither.
	ErrSessionElsewhere = errors.New("the workspace's Claude Code session is in another directory")
)

// Remove removes a workspace now, for ws rm. An agent's session is
// stopped and removed first, and the workspace is kept if that fails. A
// shell workspace whose shell runs is refused with ErrShellRunning. A
// workspace with no session record is refused with ErrBusy while it's
// locked, and otherwise removed with any session started in it. With
// dryRun, the same checks are made and nothing is changed.
func (m *Manager) Remove(ctx context.Context, ws *workspace.Workspace, dryRun bool) (Removed, error) {
	sess, err := m.store.Session(ws.ID)
	switch {
	case errors.Is(err, workspace.ErrNoSession):
		return m.removeUnrecorded(ctx, ws, dryRun)
	case err != nil:
		return Removed{}, err
	case sess.Kind == workspace.SessionShell:
		return m.removeShell(ws, dryRun)
	}
	return m.removeAgent(ctx, ws, sess.ClaudeSessionID, dryRun)
}

func (m *Manager) removeShell(ws *workspace.Workspace, dryRun bool) (Removed, error) {
	locked, err := m.store.Locked(ws.ID)
	if err != nil {
		return Removed{}, err
	}
	if locked {
		return Removed{}, fmt.Errorf("%w: %s", ErrShellRunning, ws.ID)
	}
	return m.removeWorkspace(ws, "", dryRun)
}

// removeAgent runs `claude stop` and `claude rm` on the session, then
// removes the workspace (ADR 0015). A session Claude Code no longer has
// counts as removed. A session listed in another directory is refused
// first, with nothing changed.
func (m *Manager) removeAgent(ctx context.Context, ws *workspace.Workspace, id string, dryRun bool) (Removed, error) {
	if err := m.checkSessionDir(ctx, ws, id); err != nil {
		return Removed{}, err
	}
	if dryRun {
		return Removed{Workspace: *ws, SessionID: id}, nil
	}
	if err := m.claude.Stop(ctx, id); err != nil && !errors.Is(err, claude.ErrNoSession) {
		return Removed{}, err
	}
	if err := m.claude.Remove(ctx, id); err != nil && !errors.Is(err, claude.ErrNoSession) {
		return Removed{}, err
	}
	return m.removeWorkspace(ws, id, false)
}

// checkSessionDir makes sure the recorded session is this workspace's:
// if Claude Code lists it, its working directory must be one of the
// workspace's session directories as the deletion guard resolves them. A
// session it doesn't list is already gone.
func (m *Manager) checkSessionDir(ctx context.Context, ws *workspace.Workspace, id string) error {
	dir, err := m.store.GuardedDir(ws.ID)
	if err != nil {
		return err
	}
	sessions, err := m.claude.Agents(ctx, claude.ListOptions{All: true})
	if err != nil {
		return err
	}
	s := findSession(sessions, id)
	if s == nil {
		return nil
	}
	return sessionElsewhere(ws, s, dir)
}

// sessionElsewhere fails with ErrSessionElsewhere when the listed session
// s doesn't run in one of workspace ws's session directories, given dir,
// its directory as the deletion guard resolves it.
func sessionElsewhere(ws *workspace.Workspace, s *claude.Session, dir string) error {
	dirs := sessionDirs(ws, dir)
	if got := resolved(s.Cwd); !slices.Contains(dirs, got) {
		return fmt.Errorf("%w: session %s is in %s, not in workspace %s's directory %s; neither was touched", ErrSessionElsewhere, s.ID, got, ws.ID, strings.Join(dirs, " or "))
	}
	return nil
}

// sessionDirs are the directories a session of workspace ws may run in,
// given dir, its directory as the deletion guard resolves it: dir itself,
// and, for a workspace with one repo, that repo's directory, where ws
// claude starts its session (ADR 0028). The repo's directory isn't
// resolved through symlinks, so a repo replaced by a link to elsewhere
// never matches.
func sessionDirs(ws *workspace.Workspace, dir string) []string {
	sub := ws.SessionSubdir()
	if sub == "" {
		return []string{dir}
	}
	return []string{dir, filepath.Join(dir, sub)}
}

// removeUnrecorded removes a workspace with no session record, with any
// session started in it, unless a creation or hand-off still holds its
// lock.
func (m *Manager) removeUnrecorded(ctx context.Context, ws *workspace.Workspace, dryRun bool) (Removed, error) {
	locked, err := m.store.Locked(ws.ID)
	if err != nil {
		return Removed{}, err
	}
	if locked {
		return Removed{}, fmt.Errorf("%w: %s", ErrBusy, ws.ID)
	}
	if dryRun {
		return Removed{Workspace: *ws}, nil
	}
	sessions, err := m.claude.Agents(ctx, claude.ListOptions{All: true})
	if err != nil {
		return Removed{}, err
	}
	if err := m.removeOrphans(ctx, ws, sessions); err != nil {
		return Removed{}, err
	}
	return m.removeWorkspace(ws, "", false)
}

func (m *Manager) removeWorkspace(ws *workspace.Workspace, sessionID string, dryRun bool) (Removed, error) {
	if !dryRun {
		if err := m.store.Remove(ws.ID, workspace.RemoveOptions{}); err != nil {
			return Removed{}, err
		}
	}
	return Removed{Workspace: *ws, SessionID: sessionID}, nil
}
