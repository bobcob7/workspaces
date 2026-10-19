package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// age sets a state directory's modification time to d before testNow.
func age(t *testing.T, s *Store, id string, d time.Duration) {
	t.Helper()
	when := testNow().Add(-d)
	require.NoError(t, os.Chtimes(s.stateOf(id), when, when))
}

func TestStore_CleanLeftovers(t *testing.T) {
	t.Parallel()
	t.Run("removes abandoned and orphaned state, keeps the rest", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "live", testNow())
		orphan := putWorkspace(t, s, "orphan", testNow())
		require.NoError(t, os.RemoveAll(orphan.Path))
		age(t, s, "orphan", time.Hour)
		require.NoError(t, os.MkdirAll(s.stateOf("abandoned"), 0o750))
		age(t, s, "abandoned", time.Hour)
		removed := s.CleanLeftovers()
		assert.ElementsMatch(t, []string{"orphan", "abandoned"}, removed)
		assert.DirExists(t, s.stateOf("live"))
	})
	t.Run("a creation killed mid-clone leaves only state, which goes", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		require.NoError(t, e.store.ensureRoots())
		id, unlock, err := e.store.claim("killed")
		require.NoError(t, err)
		targets, err := e.store.resolveAll(t.Context(), mustSpecs(t, "n"), false)
		require.NoError(t, err)
		building := filepath.Join(e.store.stateOf(id), "building")
		require.NoError(t, os.Mkdir(building, 0o750))
		_, err = e.store.addRepo(t.Context(), building, &targets[0], false)
		require.NoError(t, err)
		unlock()
		age(t, e.store, id, time.Hour)
		assert.NoDirExists(t, e.store.dir(id), "nothing in workspaces/ yet")
		// A git process another test forks may briefly hold a copy of the
		// released lock's descriptor until it execs, so allow a retry.
		require.Eventually(t, func() bool {
			return slices.Equal([]string{id}, e.store.CleanLeftovers())
		}, 2*time.Second, 10*time.Millisecond)
		assert.NoDirExists(t, e.store.stateOf(id))
	})
	t.Run("a creation killed before its final rename leaves only state, which goes", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		require.NoError(t, e.store.ensureRoots())
		id, unlock, err := e.store.claim("killed")
		require.NoError(t, err)
		targets, err := e.store.resolveAll(t.Context(), mustSpecs(t, "n"), false)
		require.NoError(t, err)
		building := filepath.Join(e.store.stateOf(id), "building")
		require.NoError(t, os.Mkdir(building, 0o750))
		r, err := e.store.addRepo(t.Context(), building, &targets[0], false)
		require.NoError(t, err)
		ws := Workspace{Version: recordVersion, ID: id, Path: e.store.dir(id), CreatedAt: testNow(), Repos: []Repo{r}}
		require.NoError(t, writeRecord(e.store.recordPath(id), &ws))
		unlock()
		list, err := e.store.List()
		require.NoError(t, err)
		assert.Empty(t, list, "not listed before cleanup")
		_, err = e.store.Find(id)
		require.ErrorIs(t, err, ErrNotFound)
		age(t, e.store, id, time.Hour)
		require.Eventually(t, func() bool {
			return slices.Equal([]string{id}, e.store.CleanLeftovers())
		}, 2*time.Second, 10*time.Millisecond)
		assert.NoDirExists(t, e.store.stateOf(id))
		assert.NoDirExists(t, e.store.dir(id))
	})
	t.Run("nothing to clean", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, newTestStore(t, nil, nil).CleanLeftovers())
	})
	t.Run("unreadable state directory warns", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.stateDir, "")
		assert.Empty(t, s.CleanLeftovers())
		assert.Len(t, s.Warnings(), 1)
	})
}

func TestStore_isLeftover(t *testing.T) {
	t.Parallel()
	t.Run("complete workspace", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		assert.False(t, s.isLeftover("x"))
	})
	t.Run("record without a directory, abandoned", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		age(t, s, "x", time.Hour)
		assert.True(t, s.isLeftover("x"))
	})
	t.Run("record without a directory, too new", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		age(t, s, "x", time.Second)
		assert.False(t, s.isLeftover("x"), "a creation may be about to move it into place")
	})
	t.Run("record without a directory, locked", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		unlock, ok, err := lockfile.TryExclusive(s.lockPath("x"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		age(t, s, "x", time.Hour)
		assert.False(t, s.isLeftover("x"))
	})
	t.Run("broken record is left alone with a warning", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.recordPath("x"), "version = ")
		age(t, s, "x", time.Hour)
		assert.False(t, s.isLeftover("x"))
		assert.Len(t, s.Warnings(), 1)
	})
	t.Run("incomplete state goes by abandoned", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		age(t, s, "x", time.Hour)
		assert.True(t, s.isLeftover("x"))
	})
}

func TestStore_abandoned(t *testing.T) {
	t.Parallel()
	t.Run("old and unlocked", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		writeFile(t, s.lockPath("x"), "")
		age(t, s, "x", time.Hour)
		assert.True(t, s.abandoned("x"))
	})
	t.Run("old without a lock file", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		age(t, s, "x", time.Hour)
		assert.True(t, s.abandoned("x"))
	})
	t.Run("too new", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		age(t, s, "x", time.Second)
		assert.False(t, s.abandoned("x"))
	})
	t.Run("locked by a creation in progress", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		unlock, ok, err := lockfile.TryExclusive(s.lockPath("x"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		age(t, s, "x", time.Hour)
		assert.False(t, s.abandoned("x"))
	})
	t.Run("gone", func(t *testing.T) {
		t.Parallel()
		assert.False(t, newTestStore(t, nil, nil).abandoned("x"))
	})
}
