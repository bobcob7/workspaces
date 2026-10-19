package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/repo"
)

// cloneRepo sets up one workspace repo at dir from the cache (ADR 0009):
// a hard-linked clone with the cache as remote "cache", upstream as
// "origin" and the push default, origin/* filled from the cache without
// network, and the ref checked out: a branch tracking origin/<branch>, or
// a detached tag or commit.
func (s *Store) cloneRepo(ctx context.Context, dir string, id repo.ID, r *Repo) error {
	if err := s.copyFromCache(ctx, dir, id, r); err != nil {
		return err
	}
	if _, err := s.git.Run(ctx, dir, "config", "remote.pushDefault", "origin"); err != nil {
		return err
	}
	if r.RefKind == string(cache.RefBranch) {
		_, err := s.git.Run(ctx, dir, "switch", "-q", "-C", r.Ref, "--track", "origin/"+r.Ref)
		return err
	}
	_, err := s.git.Run(ctx, dir, "switch", "-q", "--detach", r.Commit)
	return err
}

// copyFromCache does the steps that read the cached repo, under its shared
// lock so no fetch changes it meanwhile (ADR 0008).
func (s *Store) copyFromCache(ctx context.Context, dir string, id repo.ID, r *Repo) error {
	unlock, err := s.cache.SharedLock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := s.git.Run(ctx, filepath.Dir(dir), "clone", "-q", "-o", "cache", "--no-checkout", "--", s.cache.Path(id), dir); err != nil {
		return err
	}
	if _, err := s.git.Run(ctx, dir, "remote", "add", "origin", r.Upstream); err != nil {
		return err
	}
	if _, err := s.git.Run(ctx, dir, "fetch", "-q", "--no-tags", "cache", "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"); err != nil {
		return err
	}
	if r.RefKind != string(cache.RefCommit) {
		return nil
	}
	return s.fetchCommit(ctx, dir, r.Commit)
}

// fetchCommit brings in a commit that only the cache's
// refs/ws/commits/<hash> holds, such as a pull request's (ADR 0009). A
// commit already reachable needs nothing.
func (s *Store) fetchCommit(ctx context.Context, dir, commit string) error {
	_, err := s.git.Run(ctx, dir, "cat-file", "-e", commit+"^{commit}")
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*git.CommandError](err); !ok {
		return err
	}
	if _, err := s.git.Run(ctx, dir, "fetch", "-q", "--no-tags", "cache", "refs/ws/commits/"+commit); err != nil {
		return fmt.Errorf("fetching commit %s from the cache: %w", commit, err)
	}
	return nil
}
