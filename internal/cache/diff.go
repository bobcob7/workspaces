package cache

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobcob7/workspaces/internal/repo"
)

// ChangedFiles lists the files a review of head covers (ADR 0038): with a
// base branch, those changed on head since it branched from base,
// `git diff --name-only <base>...<head>`; without one, those head changed
// from its first parent, or every file of a root commit. Paths are
// relative to the repo, slash-separated. It reads the cache as it is.
func (c *Cache) ChangedFiles(ctx context.Context, id repo.ID, base, head string) ([]string, error) {
	c.startOp()
	if !c.exists(id) {
		return nil, fmt.Errorf("%w: %s isn't cached", ErrRepoNotFound, id)
	}
	args, err := c.diffArgs(ctx, id, base, head)
	if err != nil {
		return nil, err
	}
	out, err := c.git.Run(ctx, c.Path(id), args...)
	if err != nil {
		return nil, fmt.Errorf("listing the files %s changed in %s: %w", head, id, err)
	}
	files := []string{}
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// diffArgs is the git command that lists ChangedFiles' files.
func (c *Cache) diffArgs(ctx context.Context, id repo.ID, base, head string) ([]string, error) {
	if base != "" {
		return []string{"diff", "--name-only", "-z", "refs/heads/" + base + "..." + head}, nil
	}
	parent, ok, err := c.revParse(ctx, id, head+"^1")
	if err != nil {
		return nil, err
	}
	if !ok {
		return []string{"diff-tree", "-r", "--name-only", "-z", "--no-commit-id", "--root", head}, nil
	}
	return []string{"diff", "--name-only", "-z", parent, head}, nil
}
