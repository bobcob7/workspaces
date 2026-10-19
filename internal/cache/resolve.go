package cache

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Source says how a spec was resolved.
type Source string

// The ways a spec resolves (ADR 0007).
const (
	// SourceSpec: the spec named the host itself.
	SourceSpec Source = "spec"
	// SourceCache: a short spec matched a cached repo.
	SourceCache Source = "cache"
	// SourceDefault: an uncached owner/name, on defaults.host.
	SourceDefault Source = "default"
	// SourceProbe: an uncached name, found on defaults.host under one of
	// defaults.owners.
	SourceProbe Source = "probe"
)

// Resolution is a spec resolved to a repo.
type Resolution struct {
	ID       repo.ID
	Upstream string
	Cached   bool
	Source   Source
}

// Resolve finds the repo a spec names (ADR 0007): a host in the spec is used
// directly; a short spec is looked up in the cache, preferring
// defaults.host for owner/name and defaults.owners for a name; an uncached
// owner/name is taken to be on defaults.host, and an uncached name is
// probed on defaults.host under each of defaults.owners. Nothing is cloned
// or fetched.
func (c *Cache) Resolve(ctx context.Context, sp *repo.Spec) (Resolution, error) {
	return c.resolve(ctx, sp, true)
}

// Lookup resolves a spec to a cached repo without going to upstream. A
// spec that doesn't name a cached repo is ErrRepoNotFound.
func (c *Cache) Lookup(ctx context.Context, sp *repo.Spec) (Resolution, error) {
	r, err := c.resolve(ctx, sp, false)
	if err != nil {
		return Resolution{}, err
	}
	if !r.Cached {
		return Resolution{}, fmt.Errorf("%w: %s isn't cached", ErrRepoNotFound, r.ID)
	}
	return r, nil
}

func (c *Cache) resolve(ctx context.Context, sp *repo.Spec, probe bool) (Resolution, error) {
	if id, ok := sp.ID(); ok {
		upstream, _ := sp.Upstream()
		r := Resolution{ID: id, Upstream: upstream, Source: SourceSpec}
		err := c.withUpstream(ctx, &r, sp.Kind == repo.KindURL)
		return r, err
	}
	ids, err := c.IDs()
	if err != nil {
		return Resolution{}, err
	}
	var r Resolution
	if sp.Kind == repo.KindOwnerName {
		r, err = c.resolveOwnerName(ids, sp)
	} else {
		r, err = c.resolveName(ctx, ids, sp, probe)
	}
	if err != nil {
		return Resolution{}, err
	}
	if err := c.withUpstream(ctx, &r, false); err != nil {
		return Resolution{}, err
	}
	return r, nil
}

// resolveOwnerName matches owner/name against the cache on any host,
// preferring defaults.host, and otherwise assumes defaults.host.
func (c *Cache) resolveOwnerName(ids []repo.ID, sp *repo.Spec) (Resolution, error) {
	matches := filterIDs(ids, func(id repo.ID) bool { return id.Owner == sp.Owner && id.Name == sp.Name })
	if len(matches) > 1 {
		matches = preferred(matches, func(id repo.ID) bool { return id.Host == c.defaults.Host })
	}
	switch len(matches) {
	case 0:
		id, err := repo.NewID(c.defaults.Host, sp.Owner, sp.Name)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{ID: id, Upstream: id.UpstreamURL(), Source: SourceDefault}, nil
	case 1:
		return Resolution{ID: matches[0], Source: SourceCache}, nil
	}
	return Resolution{}, &AmbiguousError{Spec: sp.Raw, Candidates: matches}
}

// resolveName matches a bare name against the cache, preferring
// defaults.owners in order, and otherwise probes upstream.
func (c *Cache) resolveName(ctx context.Context, ids []repo.ID, sp *repo.Spec, probe bool) (Resolution, error) {
	matches := filterIDs(ids, func(id repo.ID) bool { return id.Name == sp.Name })
	for _, owner := range c.defaults.Owners {
		if len(matches) < 2 {
			break
		}
		if p := preferred(matches, func(id repo.ID) bool { return id.Owner == owner }); len(p) < len(matches) {
			matches = p
		}
	}
	switch len(matches) {
	case 0:
		if !probe {
			return Resolution{}, fmt.Errorf("%w: no cached repo is named %q", ErrRepoNotFound, sp.Name)
		}
		return c.probe(ctx, sp.Name)
	case 1:
		return Resolution{ID: matches[0], Source: SourceCache}, nil
	}
	return Resolution{}, &AmbiguousError{Spec: sp.Raw, Candidates: matches}
}

// probe tries defaults.host/<owner>/name for each of defaults.owners with
// `git ls-remote`; the first that answers wins. Upstream saying the repo
// isn't there moves on to the next owner. Any other failure, such as an
// unreachable host, stops the probe as ErrFetchFailed, so an outage isn't
// reported as a missing repo.
func (c *Cache) probe(ctx context.Context, name string) (Resolution, error) {
	if err := c.ensureRoot(ctx); err != nil {
		return Resolution{}, err
	}
	for _, owner := range c.defaults.Owners {
		id, err := repo.NewID(c.defaults.Host, owner, name)
		if err != nil {
			return Resolution{}, err
		}
		_, err = c.git.Run(ctx, c.root, "ls-remote", "-q", "--", id.UpstreamURL(), "HEAD")
		switch {
		case err == nil:
			return Resolution{ID: id, Upstream: id.UpstreamURL(), Source: SourceProbe}, nil
		case isGitUnusable(err) || ctx.Err() != nil:
			return Resolution{}, err
		case !repoMissing(err):
			return Resolution{}, fmt.Errorf("%w: looking for %s: %w", ErrFetchFailed, id, err)
		}
		c.log.DebugContext(ctx, "probe missed", slog.String("id", id.String()), slog.Any("err", err))
	}
	return Resolution{}, fmt.Errorf("%w: %q isn't cached, and no owner in defaults.owners has it on %s", ErrRepoNotFound, name, c.defaults.Host)
}

// withUpstream marks a resolution cached or not. A cached repo's upstream is
// its remote.origin.url (ADR 0019); when a URL spec disagrees, the cached
// URL wins and a warning names both.
func (c *Cache) withUpstream(ctx context.Context, r *Resolution, fromURL bool) error {
	if !c.exists(r.ID) {
		return nil
	}
	cached, err := c.upstream(ctx, r.ID)
	if err != nil {
		return err
	}
	if fromURL && cached != r.Upstream {
		c.warn("%s is cached from %s; using that instead of %s", r.ID, cached, r.Upstream)
	}
	r.Cached, r.Upstream = true, cached
	return nil
}

func filterIDs(ids []repo.ID, keep func(repo.ID) bool) []repo.ID {
	var out []repo.ID
	for _, id := range ids {
		if keep(id) {
			out = append(out, id)
		}
	}
	return out
}

// preferred returns the ids that satisfy prefer, or all of them if none do.
func preferred(ids []repo.ID, prefer func(repo.ID) bool) []repo.ID {
	if p := filterIDs(ids, prefer); len(p) > 0 {
		return p
	}
	return slices.Clone(ids)
}
