package cache

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bobcob7/workspaces/internal/repo"
)

var (
	// ErrRepoNotFound means a spec matched no repo, cached or upstream.
	ErrRepoNotFound = errors.New("repo not found")
	// ErrRefNotFound means a ref matched no branch, tag, or commit.
	ErrRefNotFound = errors.New("ref not found")
	// ErrCloneFailed wraps a failed first clone into the cache.
	ErrCloneFailed = errors.New("clone failed")
	// ErrFetchFailed wraps a failed fetch of a cached repo.
	ErrFetchFailed = errors.New("fetch failed")
)

// AmbiguousError is a short spec that matches several cached repos
// (ADR 0007).
type AmbiguousError struct {
	Spec       string
	Candidates []repo.ID
}

func (e *AmbiguousError) Error() string {
	ids := make([]string, 0, len(e.Candidates))
	for _, id := range e.Candidates {
		ids = append(ids, id.String())
	}
	return fmt.Sprintf("%q matches several cached repos: %s; name one with owner/name or host/owner/name", e.Spec, strings.Join(ids, ", "))
}
