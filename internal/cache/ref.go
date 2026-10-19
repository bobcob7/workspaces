package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/repo"
)

// RefKind says what a resolved ref is.
type RefKind string

// The kinds of ref, in the order they're tried (ADR 0007).
const (
	RefBranch RefKind = "branch"
	RefTag    RefKind = "tag"
	RefCommit RefKind = "commit"
)

// Ref is a ref resolved in a cached repo.
type Ref struct {
	// Name is the ref as given, or the default branch for an empty ref.
	Name   string
	Kind   RefKind
	Commit string
}

// ResolveRef resolves ref in a cached repo (ADR 0007): a branch, then a
// tag, then a commit. An empty ref is the default branch. A full commit hash
// that isn't cached, such as a pull request's, is fetched from upstream
// into refs/ws/commits/<hash>. Callers fetch the repo first.
func (c *Cache) ResolveRef(ctx context.Context, id repo.ID, ref string) (Ref, error) {
	if !c.exists(id) {
		return Ref{}, fmt.Errorf("%w: %s isn't cached", ErrRepoNotFound, id)
	}
	if ref == "" {
		branch, err := c.defaultBranch(ctx, id)
		if err != nil {
			return Ref{}, err
		}
		ref = branch
	}
	for _, try := range []struct {
		kind RefKind
		rev  string
	}{
		{RefBranch, "refs/heads/" + ref},
		{RefTag, "refs/tags/" + ref},
		{RefCommit, ref},
	} {
		commit, ok, err := c.revParse(ctx, id, try.rev)
		if err != nil {
			return Ref{}, err
		}
		if ok {
			return Ref{Name: ref, Kind: try.kind, Commit: commit}, nil
		}
	}
	return c.fetchedCommit(ctx, id, ref)
}

// fetchedCommit handles a ref the cache doesn't have: a full hash is
// fetched from upstream, anything else isn't found.
func (c *Cache) fetchedCommit(ctx context.Context, id repo.ID, ref string) (Ref, error) {
	if !isHex(ref) {
		return Ref{}, fmt.Errorf("%w: %s has no branch, tag, or commit %q", ErrRefNotFound, id, ref)
	}
	if len(ref) != fullHashLen {
		return Ref{}, fmt.Errorf("%w: %s has no commit %q; give the full %d-character hash to fetch it from upstream", ErrRefNotFound, id, ref, fullHashLen)
	}
	if err := c.fetchCommit(ctx, id, ref); err != nil {
		return Ref{}, fetchCommitError(id, ref, err)
	}
	commit, ok, err := c.revParse(ctx, id, ref)
	if err != nil {
		return Ref{}, err
	}
	if !ok {
		return Ref{}, fmt.Errorf("%w: %s: commit %s wasn't fetched", ErrRefNotFound, id, ref)
	}
	return Ref{Name: ref, Kind: RefCommit, Commit: commit}, nil
}

// fetchCommitError reports upstream not having the commit as ErrRefNotFound,
// and anything else git reports, such as an unreachable host, as
// ErrFetchFailed. Errors that aren't git's, such as a lock that couldn't be
// taken, are returned as they are.
func fetchCommitError(id repo.ID, hash string, err error) error {
	if objectMissing(err) {
		return fmt.Errorf("%w: %s has no commit %s upstream", ErrRefNotFound, id, hash)
	}
	if _, ok := errors.AsType[*git.CommandError](err); ok {
		return fmt.Errorf("%w: %s: fetching commit %s: %w", ErrFetchFailed, id, hash, err)
	}
	return err
}

// fullHashLen is the length of a SHA-1 commit hash.
const fullHashLen = 40

// fetchCommit fetches one commit into refs/ws/commits/<hash>, outside the
// cache's refspecs, so `fetch --prune` keeps it.
func (c *Cache) fetchCommit(ctx context.Context, id repo.ID, hash string) error {
	c.startOp()
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = c.git.Run(ctx, c.Path(id), "fetch", "-q", "origin", hash+":refs/ws/commits/"+hash)
	return err
}

// revParse resolves rev to a commit. A rev that doesn't exist isn't an
// error: it reports false.
func (c *Cache) revParse(ctx context.Context, id repo.ID, rev string) (string, bool, error) {
	out, err := c.git.Run(ctx, c.Path(id), "rev-parse", "--verify", "-q", rev+"^{commit}")
	if err == nil {
		return strings.TrimSpace(string(out)), true, nil
	}
	if cmdErr, ok := errors.AsType[*git.CommandError](err); ok && cmdErr.ExitCode == 1 {
		return "", false, nil
	}
	return "", false, err
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
