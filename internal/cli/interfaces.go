package cli

import (
	"context"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/shell"
	"github.com/bobcob7/workspaces/internal/workspace"
)

//go:generate go tool moq -out moq_test.go . configLoader repoCache workspaceStore shellRunner claudeClient

// configLoader resolves CONFIG_DIR and reads config.toml.
type configLoader interface {
	Load(configDir string) (config.Paths, config.Config, error)
}

// repoCache is the repo cache the ws cache commands work on.
type repoCache interface {
	Resolve(ctx context.Context, sp *repo.Spec) (cache.Resolution, error)
	Lookup(ctx context.Context, sp *repo.Spec) (cache.Resolution, error)
	Add(ctx context.Context, id repo.ID, upstream string) (bool, error)
	FetchEach(ctx context.Context, ids []repo.ID) []error
	IDs() ([]repo.ID, error)
	List(ctx context.Context) ([]cache.Entry, error)
	Search(ctx context.Context, query string, limit int, refs bool) ([]cache.Match, error)
	Remove(ctx context.Context, id repo.ID) error
	Prune(ctx context.Context, keep func(id repo.ID, commit string) (bool, error), dryRun bool) ([]cache.PrunedRef, error)
	Path(id repo.ID) string
	Warnings() []string
}

// workspaceStore is the set of workspaces.
type workspaceStore interface {
	UsesCommit(id, commit string) (bool, error)
	Create(ctx context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error)
	RecordSession(id string, kind workspace.SessionKind, claudeSessionID string) error
	Remove(id string, opts workspace.RemoveOptions) error
	Warnings() []string
}

// shellRunner runs an interactive shell in the foreground and returns its
// exit status.
type shellRunner interface {
	Run(ctx context.Context, sess shell.Session) (int, error)
}

// claudeClient runs the Claude Code CLI: background sessions and the
// background service they run under (ADRs 0015, 0018, 0024).
type claudeClient interface {
	Check(ctx context.Context) error
	Start(ctx context.Context, opts claude.StartOptions) (claude.Started, error)
	Attach(ctx context.Context, id string) error
	Agents(ctx context.Context, opts claude.ListOptions) ([]claude.Session, error)
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	Respawn(ctx context.Context, id string) error
	JobExists(id string) (bool, error)
	DaemonStatus(ctx context.Context) (claude.DaemonStatus, error)
	StopDaemon(ctx context.Context) error
	RunDaemon(ctx context.Context) (int, error)
}
