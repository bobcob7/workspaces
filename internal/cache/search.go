package cache

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/repo"
)

// Match is a search result: a cached repo and, when searching refs, one of
// its branches or tags.
type Match struct {
	ID  repo.ID
	Ref string
}

// Search returns up to limit cached repos matching query, case-insensitive,
// best first: exact name, name prefix, name substring, then owner or host
// substring. With refs, branches and tags containing query follow. Unlike
// resolution, several matches are never an error.
func (c *Cache) Search(ctx context.Context, query string, limit int, refs bool) ([]Match, error) {
	ids, err := c.IDs()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	type ranked struct {
		Match
		rank int
	}
	var found []ranked
	for _, id := range ids {
		if r, ok := rank(id, q); ok {
			found = append(found, ranked{Match{ID: id}, r})
		}
		if !refs {
			continue
		}
		names, err := c.refNames(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if strings.Contains(strings.ToLower(name), q) {
				found = append(found, ranked{Match{ID: id, Ref: name}, refRank})
			}
		}
	}
	slices.SortStableFunc(found, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), strings.Compare(a.ID.String(), b.ID.String()), strings.Compare(a.Ref, b.Ref))
	})
	matches := make([]Match, 0, min(limit, len(found)))
	for _, f := range found[:min(limit, len(found))] {
		matches = append(matches, f.Match)
	}
	return matches, nil
}

// refRank puts ref matches after every repo match.
const refRank = 4

// rank scores how well id matches the lowercase query q; lower is better.
func rank(id repo.ID, q string) (int, bool) {
	switch {
	case id.Name == q:
		return 0, true
	case strings.HasPrefix(id.Name, q):
		return 1, true
	case strings.Contains(id.Name, q):
		return 2, true
	case strings.Contains(id.Owner, q), strings.Contains(id.Host, q):
		return 3, true
	}
	return 0, false
}

// refNames lists a cached repo's branches and tags.
func (c *Cache) refNames(ctx context.Context, id repo.ID) ([]string, error) {
	out, err := c.git.Run(ctx, c.Path(id), "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/tags")
	if err != nil {
		return nil, fmt.Errorf("listing %s's refs: %w", id, err)
	}
	return strings.Fields(string(out)), nil
}
