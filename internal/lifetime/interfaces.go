package lifetime

import (
	"context"
	"time"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

//go:generate go tool moq -out moq_test.go . workspaceStore claudeClient processReader reviewJudge

// workspaceStore is the set of workspaces and their state (ADRs 0012,
// 0022, 0023).
type workspaceStore interface {
	CleanLeftovers() []string
	List() ([]workspace.Workspace, error)
	Unknown() ([]string, error)
	Session(id string) (workspace.Session, error)
	Locked(id string) (bool, error)
	Abandoned(id string) (bool, error)
	GuardedDir(id string) (string, error)
	Remove(id string, opts workspace.RemoveOptions) error
	ClearParent(id string) (workspace.Workspace, error)
}

// claudeClient runs the Claude Code CLI (ADR 0024).
type claudeClient interface {
	Agents(ctx context.Context, opts claude.ListOptions) ([]claude.Session, error)
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	Respawn(ctx context.Context, id string) error
	JobExists(id string) (bool, error)
	DaemonStatus(ctx context.Context) (claude.DaemonStatus, error)
	StopDaemon(ctx context.Context) error
	RunDaemon(ctx context.Context) (int, error)
}

// processReader reads when a process started from the operating system
// (ADR 0018's stale sessions).
type processReader interface {
	StartTime(pid int) (time.Time, error)
}

// reviewJudge tells whether a workspace is a review whose every profile
// has reported (ADR 0045). A workspace that isn't a review isn't complete.
type reviewJudge interface {
	Complete(ctx context.Context, ws *workspace.Workspace) (bool, error)
}
