package cache

import (
	"context"

	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
)

// lock takes id's lock exclusively, as anything that writes to the cached
// repo must (ADR 0008). The returned function releases it.
func (c *Cache) lock(ctx context.Context, id repo.ID) (func(), error) {
	return lockfile.Exclusive(ctx, c.lockPath(id))
}

// SharedLock takes id's lock shared, for reading the cached repo while
// nothing writes to it, as cloning a workspace from it does (ADR 0008). The
// returned function releases it.
func (c *Cache) SharedLock(ctx context.Context, id repo.ID) (func(), error) {
	return lockfile.Shared(ctx, c.lockPath(id))
}
