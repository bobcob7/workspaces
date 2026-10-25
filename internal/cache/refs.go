package cache

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/repo"
)

// RefInfo is a branch or tag in a cached repo and the commit it points at.
// For an annotated tag, the commit's fields are the tagged commit's.
type RefInfo struct {
	Name    string
	Kind    RefKind
	Commit  string
	Subject string
	Author  string
	// Date is the commit's committer date, zero if git gave none.
	Date time.Time
}

// RefListing is a cached repo's branches and tags.
type RefListing struct {
	ID            repo.ID
	DefaultBranch string
	Refs          []RefInfo
}

// refsFormat prints each ref's fields, then the same fields peeled, which
// are empty unless the ref is an annotated tag, separated by NULs.
const refsFormat = "%(refname)%00%(objectname)%00%(subject)%00%(authorname)%00%(committerdate:iso-strict)" +
	"%00%(*objectname)%00%(*subject)%00%(*authorname)%00%(*committerdate:iso-strict)"

// refsFields is how many fields refsFormat prints per ref.
const refsFields = 9

// Refs lists a cached repo's branches and tags: branches first, the default
// branch leading and the rest newest first, then tags newest first. It
// reads the cache as it is, without fetching or taking the repo's lock.
func (c *Cache) Refs(ctx context.Context, id repo.ID) (RefListing, error) {
	c.startOp()
	if !c.exists(id) {
		return RefListing{}, fmt.Errorf("%w: %s isn't cached", ErrRepoNotFound, id)
	}
	branch, err := c.defaultBranch(ctx, id)
	if err != nil {
		return RefListing{}, err
	}
	out, err := c.git.Run(ctx, c.Path(id), "for-each-ref", "--format="+refsFormat, "refs/heads", "refs/tags")
	if err != nil {
		return RefListing{}, fmt.Errorf("listing %s's refs: %w", id, err)
	}
	refs := parseRefs(string(out))
	sortRefs(refs, branch)
	return RefListing{ID: id, DefaultBranch: branch, Refs: refs}, nil
}

// parseRefs reads refsFormat's output, skipping lines that aren't a branch
// or tag.
func parseRefs(out string) []RefInfo {
	refs := []RefInfo{}
	for line := range strings.Lines(out) {
		f := strings.Split(strings.TrimSuffix(line, "\n"), "\x00")
		if len(f) != refsFields {
			continue
		}
		ref, ok := refFromName(f[0])
		if !ok {
			continue
		}
		if f[5] != "" {
			f = f[4:]
		}
		ref.Commit, ref.Subject, ref.Author = f[1], f[2], f[3]
		ref.Date, _ = time.Parse(time.RFC3339, f[4])
		refs = append(refs, ref)
	}
	return refs
}

// refFromName gives a full ref name's short name and kind.
func refFromName(full string) (RefInfo, bool) {
	if name, ok := strings.CutPrefix(full, "refs/heads/"); ok {
		return RefInfo{Name: name, Kind: RefBranch}, true
	}
	if name, ok := strings.CutPrefix(full, "refs/tags/"); ok {
		return RefInfo{Name: name, Kind: RefTag}, true
	}
	return RefInfo{}, false
}

// sortRefs orders refs as Refs documents. Ties go by name.
func sortRefs(refs []RefInfo, defaultBranch string) {
	group := func(r *RefInfo) int {
		switch {
		case r.Kind == RefBranch && r.Name == defaultBranch:
			return 0
		case r.Kind == RefBranch:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(refs, func(a, b RefInfo) int {
		return cmp.Or(cmp.Compare(group(&a), group(&b)), b.Date.Compare(a.Date), strings.Compare(a.Name, b.Name))
	})
}

// refCounts counts a cached repo's branches and tags.
func (c *Cache) refCounts(ctx context.Context, id repo.ID) (branches, tags int, err error) {
	out, err := c.git.Run(ctx, c.Path(id), "for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags")
	if err != nil {
		return 0, 0, fmt.Errorf("counting %s's refs: %w", id, err)
	}
	for name := range strings.FieldsSeq(string(out)) {
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			branches++
		case strings.HasPrefix(name, "refs/tags/"):
			tags++
		}
	}
	return branches, tags, nil
}
