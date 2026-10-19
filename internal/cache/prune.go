package cache

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobcob7/workspaces/internal/repo"
)

// commitRefPrefix holds commits fetched by full hash (ADR 0007).
const commitRefPrefix = "refs/ws/commits/"

// PrunedRef is a refs/ws/commits ref that Prune deleted, or would delete.
type PrunedRef struct {
	ID     repo.ID
	Commit string
}

// Prune deletes refs/ws/commits/<hash> refs in every cached repo unless
// keep reports that a workspace uses that commit. keep runs while the repo's
// exclusive lock is held, so no workspace creation can fetch a commit into
// the repo between keep's answer and the deletion. An error from keep stops
// pruning. With dryRun, Prune only reports what it would delete.
func (c *Cache) Prune(ctx context.Context, keep func(id repo.ID, commit string) (bool, error), dryRun bool) ([]PrunedRef, error) {
	c.startOp()
	ids, err := c.IDs()
	if err != nil {
		return nil, err
	}
	var pruned []PrunedRef
	for _, id := range ids {
		refs, err := c.pruneRepo(ctx, id, keep, dryRun)
		if err != nil {
			return pruned, err
		}
		pruned = append(pruned, refs...)
	}
	return pruned, nil
}

// pruneRepo prunes one cached repo under its exclusive lock.
func (c *Cache) pruneRepo(ctx context.Context, id repo.ID, keep func(repo.ID, string) (bool, error), dryRun bool) ([]PrunedRef, error) {
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	commits, err := c.commitRefs(ctx, id)
	if err != nil {
		return nil, err
	}
	var pruned []PrunedRef
	for _, commit := range commits {
		kept, err := keep(id, commit)
		if err != nil {
			return pruned, err
		}
		if kept {
			continue
		}
		if !dryRun {
			if _, err := c.git.Run(ctx, c.Path(id), "update-ref", "-d", commitRefPrefix+commit); err != nil {
				return pruned, fmt.Errorf("pruning %s commit %s: %w", id, commit, err)
			}
		}
		pruned = append(pruned, PrunedRef{ID: id, Commit: commit})
	}
	return pruned, nil
}

// commitRefs lists the hashes under refs/ws/commits/ in a cached repo.
func (c *Cache) commitRefs(ctx context.Context, id repo.ID) ([]string, error) {
	out, err := c.git.Run(ctx, c.Path(id), "for-each-ref", "--format=%(refname)", commitRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing %s commit refs: %w", id, err)
	}
	var commits []string
	for line := range strings.Lines(string(out)) {
		if commit, ok := strings.CutPrefix(strings.TrimSpace(line), commitRefPrefix); ok {
			commits = append(commits, commit)
		}
	}
	return commits, nil
}
