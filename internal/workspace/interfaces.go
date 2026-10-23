package workspace

import (
	"context"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/repo"
)

//go:generate go tool moq -out moq_test.go . gitRunner repoCache claudeTrust

// gitRunner runs git commands (ADR 0003).
type gitRunner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// claudeTrust takes back a directory's trust in Claude Code's config
// (ADR 0028).
type claudeTrust interface {
	Untrust(dir string, created bool) error
}

// repoCache is the repo cache workspaces are cloned from (ADR 0008).
type repoCache interface {
	Resolve(ctx context.Context, sp *repo.Spec) (cache.Resolution, error)
	Lookup(ctx context.Context, sp *repo.Spec) (cache.Resolution, error)
	Add(ctx context.Context, id repo.ID, upstream string) (bool, error)
	Fetch(ctx context.Context, id repo.ID) error
	ResolveRef(ctx context.Context, id repo.ID, ref string) (cache.Ref, error)
	SharedLock(ctx context.Context, id repo.ID) (func(), error)
	Path(id repo.ID) string
}
