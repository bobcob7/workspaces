package cli

import (
	"context"
	"errors"

	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// reviewJudge tells the removal pass which reviews are complete: every
// profile in the review's record has a current submission (ADR 0045).
type reviewJudge struct {
	state string
	store *store.Store
}

// Complete reports whether ws is a review whose every profile reported. A
// workspace with no review record isn't one.
func (j reviewJudge) Complete(ctx context.Context, ws *workspace.Workspace) (bool, error) {
	rec, err := record.Read(record.Path(j.state, ws.ID))
	if errors.Is(err, record.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	id, err := repo.ParseID(rec.Repo)
	if err != nil {
		return false, err
	}
	return j.store.Complete(ctx, id, rec.Ref, rec.ProfileNames())
}
