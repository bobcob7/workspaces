package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Entry describes a cached repo for `ws cache list`.
type Entry struct {
	ID            repo.ID
	Path          string
	Upstream      string
	DefaultBranch string
	// LastFetchedAt is the last fetch, or the clone if there's been none.
	LastFetchedAt time.Time
	SizeBytes     int64
	Branches      int
	Tags          int
}

// IDs returns every cached repo's id, sorted. The directory tree is the
// index (ADR 0008): any directory under repos/ whose name ends in .git and
// whose path is a valid id.
func (c *Cache) IDs() ([]repo.ID, error) {
	root := c.reposDir()
	var ids []repo.ID
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || !strings.HasSuffix(d.Name(), ".git") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("reading cache: %w", err)
		}
		if id, ok := idFromPath(filepath.ToSlash(rel)); ok {
			ids = append(ids, id)
		}
		return filepath.SkipDir
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading cache: %w", err)
	}
	slices.SortFunc(ids, func(a, b repo.ID) int { return strings.Compare(a.String(), b.String()) })
	return ids, nil
}

// idFromPath turns "host/owner/…/name.git" back into an id.
func idFromPath(rel string) (repo.ID, bool) {
	segs := strings.Split(strings.TrimSuffix(rel, ".git"), "/")
	if len(segs) < 3 {
		return repo.ID{}, false
	}
	last := len(segs) - 1
	id, err := repo.NewID(segs[0], strings.Join(segs[1:last], "/"), segs[last])
	if err != nil || id.String() != strings.TrimSuffix(rel, ".git") {
		return repo.ID{}, false
	}
	return id, true
}

// List describes every cached repo, sorted by id. Repos are read
// concurrently, at most maxWorkers at a time; the first failure in id order
// is returned.
func (c *Cache) List(ctx context.Context) ([]Entry, error) {
	c.startOp()
	ids, err := c.IDs()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(ids))
	errs := make([]error, len(ids))
	forEach(len(ids), func(i int) {
		entries[i], errs[i] = c.entry(ctx, ids[i])
	})
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func (c *Cache) entry(ctx context.Context, id repo.ID) (Entry, error) {
	path := c.Path(id)
	upstream, err := c.upstream(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	branch, err := c.defaultBranch(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	fetched, err := lastFetched(path)
	if err != nil {
		return Entry{}, err
	}
	size, err := dirSize(path)
	if err != nil {
		return Entry{}, err
	}
	branches, tags, err := c.refCounts(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	return Entry{ID: id, Path: path, Upstream: upstream, DefaultBranch: branch, LastFetchedAt: fetched, SizeBytes: size, Branches: branches, Tags: tags}, nil
}

// upstream is the cached repo's remote.origin.url, the repo's upstream URL
// (ADR 0019).
func (c *Cache) upstream(ctx context.Context, id repo.ID) (string, error) {
	out, err := c.git.Run(ctx, c.Path(id), "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("reading %s's upstream: %w", id, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// defaultBranch is the branch the cached repo's HEAD points at.
func (c *Cache) defaultBranch(ctx context.Context, id repo.ID) (string, error) {
	out, err := c.git.Run(ctx, c.Path(id), "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading %s's default branch: %w", id, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// lastFetched is FETCH_HEAD's modification time, or HEAD's for a repo
// that hasn't been fetched since it was cloned.
func lastFetched(path string) (time.Time, error) {
	for _, name := range []string{"FETCH_HEAD", "HEAD"} {
		info, err := os.Stat(filepath.Join(path, name))
		if err == nil {
			return info.ModTime(), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, fmt.Errorf("reading fetch time: %w", err)
		}
	}
	return time.Time{}, fmt.Errorf("reading fetch time: %s has no HEAD", path)
}

// dirSize adds up the sizes of the files under path.
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measuring %s: %w", path, err)
	}
	return size, nil
}
