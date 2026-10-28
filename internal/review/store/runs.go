package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
)

// runsDir is the directory beside a commit's submissions that holds the
// record of each review run of it, by workspace id (ADR 0045). It's a
// directory, so it's never read as a submission.
const runsDir = "runs"

// ErrNoRun means no review run has that workspace id.
var ErrNoRun = errors.New("no review run")

// Run is one review run: the workspace it ran in, and its record.
type Run struct {
	ID     string
	Record record.Record
}

// RunPath is where the record of the review run in workspace wsID, of id
// at commit, is kept.
func (s *Store) RunPath(id repo.ID, commit, wsID string) string {
	return filepath.Join(s.Dir(id, commit), runsDir, wsID+".toml")
}

// SaveRun keeps rec, the record of the review run in workspace wsID, next
// to its submissions, so it outlives the workspace (ADR 0045).
func (s *Store) SaveRun(wsID string, rec *record.Record) error {
	id, err := repo.ParseID(rec.Repo)
	if err != nil {
		return fmt.Errorf("saving review run %s: %w", wsID, err)
	}
	if !commitPattern.MatchString(rec.Ref) {
		return fmt.Errorf("saving review run %s: %q isn't a full commit", wsID, rec.Ref)
	}
	path := s.RunPath(id, rec.Ref, wsID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("saving review run %s: %w", wsID, err)
	}
	return record.Write(path, rec)
}

// DropRun removes the record of a review run that never started. One
// that isn't there is already gone.
func (s *Store) DropRun(wsID string, rec *record.Record) error {
	id, err := repo.ParseID(rec.Repo)
	if err != nil {
		return err
	}
	err = os.Remove(s.RunPath(id, rec.Ref, wsID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// FindRun is the review run of workspace wsID, from any repo and commit.
// None is ErrNoRun.
func (s *Store) FindRun(wsID string) (Run, error) {
	runs, err := s.Runs(func(path string) bool { return filepath.Base(path) == wsID+".toml" })
	if err != nil {
		return Run{}, err
	}
	if len(runs) == 0 {
		return Run{}, fmt.Errorf("%w: %s", ErrNoRun, wsID)
	}
	return runs[0], nil
}

// Runs lists the review runs whose record path keep accepts, nil for all,
// newest first. A record that can't be read is skipped.
func (s *Store) Runs(keep func(path string) bool) ([]Run, error) {
	var runs []Run
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(filepath.Dir(path)) != runsDir || !strings.HasSuffix(path, ".toml") {
			return nil
		}
		if keep != nil && !keep(path) {
			return nil
		}
		rec, err := record.Read(path)
		if err != nil {
			return nil //nolint:nilerr // an unreadable run is skipped, as an unreadable submission is
		}
		runs = append(runs, Run{ID: strings.TrimSuffix(filepath.Base(path), ".toml"), Record: rec})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing review runs: %w", err)
	}
	slices.SortStableFunc(runs, func(a, b Run) int { return b.Record.StartedAt.Compare(a.Record.StartedAt) })
	return runs, nil
}

// Complete reports whether every profile has a current submission for id
// at commit (ADR 0045).
func (s *Store) Complete(ctx context.Context, id repo.ID, commit string, profiles []string) (bool, error) {
	files, err := s.Files(ctx, id, commit)
	if err != nil {
		return false, err
	}
	for _, p := range profiles {
		if !slices.ContainsFunc(files, func(f File) bool { return f.Profile == p && f.Current() }) {
			return false, nil
		}
	}
	return true, nil
}
