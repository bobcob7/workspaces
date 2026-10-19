package cli

import (
	"context"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/repo"
)

//go:generate go tool moq -out moq_test.go . configLoader repoCache

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
	Path(id repo.ID) string
	Warnings() []string
}
