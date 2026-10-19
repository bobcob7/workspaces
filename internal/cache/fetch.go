package cache

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Fetch updates a cached repo from upstream, pruning branches and tags
// deleted there (ADR 0008), and follows a change of upstream's default
// branch.
func (c *Cache) Fetch(ctx context.Context, id repo.ID) error {
	c.startOp()
	if !c.exists(id) {
		return fmt.Errorf("%w: %s isn't cached", ErrRepoNotFound, id)
	}
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := c.git.Run(ctx, c.Path(id), "fetch", "-q", "--prune", "origin"); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrFetchFailed, id, err)
	}
	c.updateHead(ctx, id)
	return nil
}

// FetchEach fetches ids concurrently, at most maxWorkers at a time, and
// returns each one's error, or nil, in the order of ids. Per-repo locks keep
// concurrent fetches safe (ADR 0008).
func (c *Cache) FetchEach(ctx context.Context, ids []repo.ID) []error {
	errs := make([]error, len(ids))
	forEach(len(ids), func(i int) {
		errs[i] = c.Fetch(ctx, ids[i])
	})
	return errs
}

// updateHead points the cached repo's HEAD at upstream's default branch.
// `git remote set-head --auto` can't, since the cache's refspecs leave no
// refs/remotes/origin/*. A failure is only a warning: the fetch worked.
func (c *Cache) updateHead(ctx context.Context, id repo.ID) {
	path := c.Path(id)
	out, err := c.git.Run(ctx, path, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		c.warn("couldn't read %s's default branch: %v", id, err)
		return
	}
	branch, ok := parseSymref(string(out))
	if !ok {
		c.warn("couldn't read %s's default branch: upstream has no HEAD branch", id)
		return
	}
	if _, err := c.git.Run(ctx, path, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		c.warn("couldn't set %s's default branch to %s: %v", id, branch, err)
	}
}

// parseSymref reads the branch from `git ls-remote --symref origin HEAD`,
// whose first line is "ref: refs/heads/<branch>\tHEAD".
func parseSymref(out string) (string, bool) {
	for line := range strings.Lines(out) {
		target, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || name != "HEAD" {
			continue
		}
		branch, ok := strings.CutPrefix(target, "ref: refs/heads/")
		return branch, ok && branch != ""
	}
	return "", false
}
