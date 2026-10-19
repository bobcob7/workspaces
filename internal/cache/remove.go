package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Remove deletes a cached repo. Workspaces cloned from it keep working:
// their objects are hard links (ADR 0009).
func (c *Cache) Remove(ctx context.Context, id repo.ID) error {
	c.startOp()
	if !c.exists(id) {
		return fmt.Errorf("%w: %s isn't cached", ErrRepoNotFound, id)
	}
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	path := c.Path(id)
	trash, err := c.moveToTmp(path)
	if err != nil {
		return fmt.Errorf("removing %s: %w", id, err)
	}
	removeEmptyParents(filepath.Dir(path), c.reposDir())
	if err := os.RemoveAll(trash); err != nil {
		return fmt.Errorf("removing %s: %w", id, err)
	}
	return nil
}

// moveToTmp renames path into the cache's temp directory and returns the
// new path, so an interrupted removal never leaves a half-deleted repo that
// still looks cached.
func (c *Cache) moveToTmp(path string) (string, error) {
	tmpRoot := c.tmpDir()
	if err := os.MkdirAll(tmpRoot, 0o750); err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	trash, err := os.MkdirTemp(tmpRoot, "remove-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	dest := filepath.Join(trash, filepath.Base(path))
	if err := os.Rename(path, dest); err != nil {
		_ = os.Remove(trash)
		return "", fmt.Errorf("moving out of the cache: %w", err)
	}
	return trash, nil
}

// removeEmptyParents removes dir and its parents up to, not including,
// stop, while they're empty.
func removeEmptyParents(dir, stop string) {
	for dir != stop && len(dir) > len(stop) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
