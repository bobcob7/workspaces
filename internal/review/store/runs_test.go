package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/review/record"
)

// run is a review record of buff-bear at commit with the profiles,
// started minutes after a fixed time.
func run(minutes int, profiles ...string) record.Record {
	rec := record.Record{
		Repo: "github.com/bobcob7/buff-bear", Ref: commit,
		StartedAt: time.Date(2026, 10, 28, 9, minutes, 0, 0, time.UTC),
		PR:        &record.PR{Number: 51, URL: "https://github.com/bobcob7/buff-bear/pull/51"},
	}
	for _, p := range profiles {
		rec.Profiles = append(rec.Profiles, record.Profile{Name: p})
	}
	return rec
}

func TestStore_SaveRun(t *testing.T) {
	t.Parallel()
	t.Run("keeps the record next to the submissions", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		rec := run(0, "go")
		require.NoError(t, s.SaveRun("review-1", &rec))
		got, err := record.Read(filepath.Join(s.Dir(id(t), commit), "runs", "review-1.toml"))
		require.NoError(t, err)
		assert.Equal(t, rec.PR, got.PR)
	})
	t.Run("isn't read as a submission", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		rec := run(0, "go")
		require.NoError(t, s.SaveRun("review-1", &rec))
		files, err := s.Files(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Empty(t, files)
	})
	t.Run("a bad repo or commit", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		rec := run(0, "go")
		rec.Repo = "nope"
		require.Error(t, s.SaveRun("x", &rec))
		rec = run(0, "go")
		rec.Ref = "main"
		require.Error(t, s.SaveRun("x", &rec))
	})
}

func TestStore_Runs(t *testing.T) {
	t.Parallel()
	t.Run("lists every run, newest first", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		older, newer := run(0, "go"), run(5, "go")
		require.NoError(t, s.SaveRun("older", &older))
		require.NoError(t, s.SaveRun("newer", &newer))
		runs, err := s.Runs(nil)
		require.NoError(t, err)
		require.Len(t, runs, 2)
		assert.Equal(t, "newer", runs[0].ID)
		assert.Equal(t, "older", runs[1].ID)
	})
	t.Run("skips a record that can't be read", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		rec := run(0, "go")
		require.NoError(t, s.SaveRun("good", &rec))
		require.NoError(t, os.WriteFile(s.RunPath(id(t), commit, "bad"), []byte("version = "), 0o600))
		runs, err := s.Runs(nil)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.Equal(t, "good", runs[0].ID)
	})
	t.Run("no reviews directory is no runs", func(t *testing.T) {
		t.Parallel()
		runs, err := New(filepath.Join(t.TempDir(), "missing")).Runs(nil)
		require.NoError(t, err)
		assert.Empty(t, runs)
	})
}

func TestStore_FindRun(t *testing.T) {
	t.Parallel()
	t.Run("finds a run by its workspace id", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		rec := run(0, "go")
		require.NoError(t, s.SaveRun("review-1", &rec))
		got, err := s.FindRun("review-1")
		require.NoError(t, err)
		assert.Equal(t, "review-1", got.ID)
		assert.Equal(t, commit, got.Record.Ref)
	})
	t.Run("none", func(t *testing.T) {
		t.Parallel()
		_, err := New(t.TempDir()).FindRun("nope")
		require.ErrorIs(t, err, ErrNoRun)
	})
}

func TestStore_Complete(t *testing.T) {
	t.Parallel()
	t.Run("every profile has a current submission", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		for _, p := range []string{"go", "security"} {
			_, err := s.Submit(t.Context(), id(t), commit, p, []byte(`{}`))
			require.NoError(t, err)
		}
		ok, err := s.Complete(t.Context(), id(t), commit, []string{"go", "security"})
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("a profile is missing", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		_, err := s.Submit(t.Context(), id(t), commit, "go", []byte(`{}`))
		require.NoError(t, err)
		ok, err := s.Complete(t.Context(), id(t), commit, []string{"go", "security"})
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("nothing submitted", func(t *testing.T) {
		t.Parallel()
		ok, err := New(t.TempDir()).Complete(t.Context(), id(t), commit, []string{"go"})
		require.NoError(t, err)
		assert.False(t, ok)
	})
}
