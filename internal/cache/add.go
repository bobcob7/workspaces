package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Add clones upstream into the cache as id, unless id is already cached. It
// reports whether it cloned.
func (c *Cache) Add(ctx context.Context, id repo.ID, upstream string) (bool, error) {
	c.startOp()
	if err := c.ensureRoot(); err != nil {
		return false, err
	}
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return false, err
	}
	defer unlock()
	if c.exists(id) {
		return false, nil
	}
	if err := c.clone(ctx, id, upstream); err != nil {
		return false, fmt.Errorf("%w: %s: %w", ErrCloneFailed, id, err)
	}
	return true, nil
}

// clone makes a bare clone with the cache's refspecs in a temp directory and
// renames it into place, so a failed or interrupted clone never looks
// cached (ADR 0008).
func (c *Cache) clone(ctx context.Context, id repo.ID, upstream string) error {
	tmpRoot := c.tmpDir()
	if err := os.MkdirAll(tmpRoot, 0o750); err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	tmp, err := os.MkdirTemp(tmpRoot, "clone-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if _, err := c.git.Run(ctx, tmpRoot, "clone", "--bare", "-q", "--", upstream, tmp); err != nil {
		return err
	}
	if _, err := c.git.Run(ctx, tmp, "config", "remote.origin.fetch", "+refs/heads/*:refs/heads/*"); err != nil {
		return err
	}
	if _, err := c.git.Run(ctx, tmp, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*"); err != nil {
		return err
	}
	final := c.Path(id)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("moving clone into the cache: %w", err)
	}
	return nil
}
