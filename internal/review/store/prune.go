package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Pruned is a repo's reviews that Prune removed, or would remove.
type Pruned struct {
	ID repo.ID
	// Path is the repo's directory, holding a directory per commit.
	Path string
}

// Prune removes the reviews of every repo keep refuses, all of its
// commits, and then the parent directories that leaves empty (ADR 0039).
// With dryRun it only reports them. Directories that aren't a repo's,
// such as one whose path isn't a repo id, are left alone.
func (s *Store) Prune(keep func(id repo.ID) bool, dryRun bool) ([]Pruned, error) {
	repos, err := s.repos()
	if err != nil {
		return nil, err
	}
	pruned := []Pruned{}
	for _, r := range repos {
		if keep(r.ID) {
			continue
		}
		if !dryRun {
			if err := os.RemoveAll(r.Path); err != nil {
				return pruned, fmt.Errorf("removing the reviews of %s: %w", r.ID, err)
			}
			s.removeEmptyParents(filepath.Dir(r.Path))
		}
		pruned = append(pruned, r)
	}
	return pruned, nil
}

// repos lists the repos with reviews: every directory holding a directory
// named for a full commit, sorted by path.
func (s *Store) repos() ([]Pruned, error) {
	var repos []Pruned
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == s.root {
			return filepath.SkipAll
		}
		if err != nil {
			return err
		}
		if !d.IsDir() || !commitPattern.MatchString(d.Name()) {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(s.root, dir)
		if err != nil {
			return err
		}
		if id, err := repo.ParseID(filepath.ToSlash(rel)); err == nil && !slices.ContainsFunc(repos, func(r Pruned) bool { return r.Path == dir }) {
			repos = append(repos, Pruned{ID: id, Path: dir})
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", s.root, err)
	}
	return repos, nil
}

// removeEmptyParents removes dir and its parents while they're empty,
// stopping at the store's root.
func (s *Store) removeEmptyParents(dir string) {
	for dir != s.root && len(dir) > len(s.root) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
