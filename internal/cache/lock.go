package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/bobcob7/workspaces/internal/repo"
)

// lockPoll is how often a waiting lock retries.
const lockPoll = 50 * time.Millisecond

// lock takes id's lock file exclusively with flock, as anything that writes
// to the cached repo must (ADR 0008). It waits until the lock is free or ctx
// is done. The returned function releases it.
func (c *Cache) lock(ctx context.Context, id repo.ID) (func(), error) {
	path := c.lockPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock %s: %w", path, err)
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for lock %s: %w", path, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
}
