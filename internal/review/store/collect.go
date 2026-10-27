package store

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/collect"
)

// Collect combines every submission for id at commit into one report: each
// profile's submissions oldest first, all but the current one superseded,
// and comment ids qualified as <profile>:<id> for a current submission and
// #<ordinal>:<id> otherwise (ADR 0039). A file that can't be read or isn't
// a JSON object is counted in Unreadable, not fatal.
func (s *Store) Collect(ctx context.Context, id repo.ID, commit string) (*collect.Result, error) {
	result, _, err := s.Report(ctx, id, commit)
	return result, err
}

// collectDir combines dir's submissions. The caller holds dir's lock.
func collectDir(ctx context.Context, id repo.ID, commit, dir string) (*collect.Result, error) {
	files, err := list(dir)
	if err != nil {
		return nil, err
	}
	return assemble(ctx, id, commit, files)
}

func assemble(ctx context.Context, id repo.ID, commit string, files []File) (*collect.Result, error) {
	rows := make([]collect.DigestRow, 0, len(files))
	for i, f := range files {
		// files is already in submission order; At carries that order, and
		// the path is the digest Assemble reads back.
		rows = append(rows, collect.DigestRow{Digest: f.Path, At: time.Unix(int64(i), 0)})
	}
	result, err := collect.Assemble(ctx, rows, fileReader{})
	if err != nil {
		return nil, fmt.Errorf("combining reviews of %s at %s: %w", id, commit, err)
	}
	return result, nil
}

// fileReader reads a submission by its path.
type fileReader struct{}

func (fileReader) ReadReview(_ context.Context, path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return data, nil
}
