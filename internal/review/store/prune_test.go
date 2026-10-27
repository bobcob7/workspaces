package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/repo"
)

func TestStore_Prune(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*Store, repo.ID, repo.ID) {
		t.Helper()
		s := New(filepath.Join(t.TempDir(), "reviews"))
		nested, err := repo.NewID("gitlab.example.com", "group/sub", "app")
		require.NoError(t, err)
		for _, i := range []repo.ID{id(t), nested} {
			_, err := s.Submit(t.Context(), i, commit, "security", []byte(`{}`))
			require.NoError(t, err)
		}
		return s, id(t), nested
	}
	keepOnly := func(kept repo.ID) func(repo.ID) bool {
		return func(i repo.ID) bool { return i == kept }
	}
	t.Run("removes repos keep refuses, and the parents left empty", func(t *testing.T) {
		t.Parallel()
		s, kept, gone := setup(t)
		pruned, err := s.Prune(keepOnly(kept), false)
		require.NoError(t, err)
		assert.Equal(t, []Pruned{{ID: gone, Path: filepath.Join(s.root, "gitlab.example.com", "group", "sub", "app")}}, pruned)
		assert.NoDirExists(t, filepath.Join(s.root, "gitlab.example.com"))
		assert.DirExists(t, s.Dir(kept, commit))
	})
	t.Run("a dry run removes nothing", func(t *testing.T) {
		t.Parallel()
		s, kept, gone := setup(t)
		pruned, err := s.Prune(keepOnly(kept), true)
		require.NoError(t, err)
		require.Len(t, pruned, 1)
		assert.DirExists(t, s.Dir(gone, commit))
	})
	t.Run("keeps a parent other repos still use", func(t *testing.T) {
		t.Parallel()
		s, kept, _ := setup(t)
		other, err := repo.NewID("github.com", "bobcob7", "other")
		require.NoError(t, err)
		_, err = s.Submit(t.Context(), other, commit, "go", []byte(`{}`))
		require.NoError(t, err)
		_, err = s.Prune(keepOnly(kept), false)
		require.NoError(t, err)
		assert.NoDirExists(t, filepath.Join(s.root, "github.com", "bobcob7", "other"))
		assert.DirExists(t, s.Dir(kept, commit))
	})
	t.Run("leaves directories that aren't a repo's", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		stray := filepath.Join(s.root, "stray", commit)
		require.NoError(t, os.MkdirAll(stray, 0o750))
		pruned, err := s.Prune(func(repo.ID) bool { return false }, false)
		require.NoError(t, err)
		assert.Empty(t, pruned)
		assert.DirExists(t, stray)
	})
	t.Run("no reviews directory prunes nothing", func(t *testing.T) {
		t.Parallel()
		s := New(filepath.Join(t.TempDir(), "missing"))
		pruned, err := s.Prune(func(repo.ID) bool { return false }, false)
		require.NoError(t, err)
		assert.Empty(t, pruned)
	})
}
