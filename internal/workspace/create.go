package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
)

// generatedIDAttempts is how many random ids Create tries before giving up
// on collisions.
const generatedIDAttempts = 5

// CreateOptions describe a workspace to create.
type CreateOptions struct {
	// Name is --name; empty for a generated id.
	Name  string
	Specs []repo.Spec
	// Offline skips fetching: every repo must already be cached.
	Offline bool
}

// target is a spec resolved to a cached or cacheable repo.
type target struct {
	spec repo.Spec
	res  cache.Resolution
}

// Create resolves the specs, then makes the workspace: its state directory
// first, the repos cloned from the cache, workspace.toml, and finally the
// workspace directory moved into place (ADR 0022). If any step fails,
// everything it made is removed.
//
// The workspace's lock, taken first, is returned still held, so the
// session can take it over without a moment when the workspace looks
// abandoned (ADR 0015). The caller unlocks it once the session no longer
// needs it.
func (s *Store) Create(ctx context.Context, opts CreateOptions) (Workspace, *lockfile.Lock, error) {
	if opts.Name != "" && sanitizeName(opts.Name) == "" {
		return Workspace{}, nil, fmt.Errorf("%w: %q has no letters or digits to make an id from", ErrNameInvalid, opts.Name)
	}
	if err := checkName(opts.Name); err != nil {
		return Workspace{}, nil, err
	}
	targets, err := s.resolveAll(ctx, opts.Specs, opts.Offline)
	if err != nil {
		return Workspace{}, nil, err
	}
	if err = s.ensureRoots(); err != nil {
		return Workspace{}, nil, err
	}
	id, lock, err := s.claim(opts.Name)
	if err != nil {
		return Workspace{}, nil, err
	}
	ws, err := s.build(ctx, id, opts, targets)
	if err != nil {
		s.discard(id)
		lock.Unlock()
		return Workspace{}, nil, err
	}
	return ws, lock, nil
}

// resolveAll resolves every spec before anything is created. Two specs for
// one repo, or two repos with the same name, are ErrSpecConflict: each
// repo's directory in the workspace is its name.
func (s *Store) resolveAll(ctx context.Context, specs []repo.Spec, offline bool) ([]target, error) {
	targets := make([]target, 0, len(specs))
	seen := map[string]string{}
	for i := range specs {
		sp := &specs[i]
		res, err := s.resolve(ctx, sp, offline)
		if err != nil {
			return nil, err
		}
		if prev, ok := seen[res.ID.Name]; ok {
			if prev == res.ID.String() {
				return nil, fmt.Errorf("%w: %s is listed twice", ErrSpecConflict, res.ID)
			}
			return nil, fmt.Errorf("%w: %s and %s would both be checked out as %q", ErrSpecConflict, prev, res.ID, res.ID.Name)
		}
		seen[res.ID.Name] = res.ID.String()
		targets = append(targets, target{spec: *sp, res: res})
	}
	return targets, nil
}

func (s *Store) resolve(ctx context.Context, sp *repo.Spec, offline bool) (cache.Resolution, error) {
	if offline {
		return s.cache.Lookup(ctx, sp)
	}
	return s.cache.Resolve(ctx, sp)
}

// claim reserves the workspace's id by creating its state directory, and
// takes its lock so a concurrent ws doesn't take the unfinished state for
// a leftover. Every id ends with a random suffix (ADRs 0012, 0036), and
// one that's taken is retried.
func (s *Store) claim(name string) (string, *lockfile.Lock, error) {
	for range generatedIDAttempts {
		id, err := s.newID(name)
		if err != nil {
			return "", nil, err
		}
		id, lock, err := s.claimID(id)
		if !errors.Is(err, ErrExists) {
			return id, lock, err
		}
	}
	return "", nil, fmt.Errorf("%w: no free id after %d tries", ErrExists, generatedIDAttempts)
}

// newID is <sanitized name>-<8 random hex> for a --name, or
// ws-<YYYYMMDD>-<6 random [a-z0-9]> without one.
func (s *Store) newID(name string) (string, error) {
	if name == "" {
		suffix, err := s.suffix()
		if err != nil {
			return "", err
		}
		return generateID(s.now(), suffix), nil
	}
	suffix, err := s.nameSuffix()
	if err != nil {
		return "", err
	}
	return namedID(sanitizeName(name), suffix), nil
}

func (s *Store) claimID(id string) (string, *lockfile.Lock, error) {
	if _, err := os.Lstat(s.dir(id)); err == nil {
		return "", nil, fmt.Errorf("%w: %s", ErrExists, s.dir(id))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", nil, fmt.Errorf("checking %s: %w", s.dir(id), err)
	}
	if err := os.Mkdir(s.stateOf(id), 0o750); errors.Is(err, fs.ErrExist) {
		return "", nil, fmt.Errorf("%w: %s", ErrExists, id)
	} else if err != nil {
		return "", nil, fmt.Errorf("creating state for %s: %w", id, err)
	}
	lock, ok, err := lockfile.TryExclusiveLock(s.lockPath(id))
	if err == nil && !ok {
		err = fmt.Errorf("%w: %s is locked", ErrExists, id)
	}
	if err != nil {
		_ = os.RemoveAll(s.stateOf(id))
		return "", nil, err
	}
	return id, lock, nil
}

// build clones the repos into state/<id>/building, writes workspace.toml,
// and then moves the clones into place as the workspace directory. Until
// that last rename, an interrupted creation leaves only state, which the
// leftover cleanup removes; after it, the workspace is complete.
func (s *Store) build(ctx context.Context, id string, opts CreateOptions, targets []target) (Workspace, error) {
	building := filepath.Join(s.stateOf(id), "building")
	if err := os.Mkdir(building, 0o750); err != nil {
		return Workspace{}, fmt.Errorf("creating %s: %w", building, err)
	}
	ws := Workspace{Version: formatVersion, ID: id, Name: strings.TrimSpace(opts.Name), Path: s.dir(id), CreatedAt: s.now().UTC().Truncate(time.Second)}
	for i := range targets {
		r, err := s.addRepo(ctx, building, &targets[i], opts.Offline)
		if err != nil {
			return Workspace{}, err
		}
		ws.Repos = append(ws.Repos, r)
	}
	if err := writeRecord(s.recordPath(id), &ws); err != nil {
		return Workspace{}, err
	}
	if err := os.Rename(building, ws.Path); err != nil {
		return Workspace{}, fmt.Errorf("moving %s into place: %w", id, err)
	}
	return ws, nil
}

// addRepo brings one repo's cache entry up to date, resolves its ref, and
// clones it into dir.
func (s *Store) addRepo(ctx context.Context, dir string, t *target, offline bool) (Repo, error) {
	id := t.res.ID
	if !offline {
		if err := s.refreshCache(ctx, &t.res); err != nil {
			return Repo{}, err
		}
	}
	ref, err := s.cache.ResolveRef(ctx, id, t.spec.Ref)
	if err != nil {
		return Repo{}, err
	}
	r := Repo{ID: id.String(), Spec: t.spec.Raw, Path: id.Name, Upstream: t.res.Upstream, Ref: ref.Name, RefKind: string(ref.Kind), Commit: ref.Commit}
	if err := s.cloneRepo(ctx, filepath.Join(dir, r.Path), id, &r); err != nil {
		return Repo{}, fmt.Errorf("cloning %s into the workspace: %w", id, err)
	}
	return r, nil
}

// refreshCache clones an uncached repo into the cache, or fetches a cached
// one, so the workspace starts from upstream's current state (ADR 0008).
func (s *Store) refreshCache(ctx context.Context, res *cache.Resolution) error {
	if !res.Cached {
		_, err := s.cache.Add(ctx, res.ID, res.Upstream)
		return err
	}
	return s.cache.Fetch(ctx, res.ID)
}

// discard removes what a failed Create made: its state directory, which
// holds the partial clones. Moving them into place is Create's last step,
// so a failed Create never made the workspace directory, and whatever is
// at that path isn't its to delete.
func (s *Store) discard(id string) {
	if err := os.RemoveAll(s.stateOf(id)); err != nil {
		s.warn("couldn't remove %s after a failed creation: %v", s.stateOf(id), err)
	}
}
