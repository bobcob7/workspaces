package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putWorkspace writes a record for id pointing at its directory under the
// store's root, and creates that directory.
func putWorkspace(t *testing.T, s *Store, id string, created time.Time) Workspace {
	t.Helper()
	ws := sampleRecord(s.root, id)
	ws.CreatedAt = created
	require.NoError(t, os.MkdirAll(ws.Path, 0o750))
	require.NoError(t, os.MkdirAll(s.stateOf(id), 0o750))
	require.NoError(t, writeRecord(s.recordPath(id), &ws))
	return ws
}

func TestStore_List(t *testing.T) {
	t.Parallel()
	t.Run("oldest first", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		newer := putWorkspace(t, s, "b", testNow())
		older := putWorkspace(t, s, "a", testNow().Add(-time.Hour))
		list, err := s.List()
		require.NoError(t, err)
		assert.Equal(t, []Workspace{older, newer}, list)
	})
	t.Run("ties sort by id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "b", testNow())
		putWorkspace(t, s, "a", testNow())
		list, err := s.List()
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "a", list[0].ID)
	})
	t.Run("skips incomplete state", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("half"), 0o750))
		list, err := s.List()
		require.NoError(t, err)
		assert.Empty(t, list)
		assert.Empty(t, s.Warnings())
	})
	t.Run("skips a record without a directory", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		list, err := s.List()
		require.NoError(t, err)
		assert.Empty(t, list)
		assert.Empty(t, s.Warnings())
	})
	t.Run("warns about a broken record", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.recordPath("bad"), "version = ")
		list, err := s.List()
		require.NoError(t, err)
		assert.Empty(t, list)
		assert.Len(t, s.Warnings(), 1)
	})
	t.Run("no state yet", func(t *testing.T) {
		t.Parallel()
		list, err := newTestStore(t, nil, nil).List()
		require.NoError(t, err)
		assert.Empty(t, list)
	})
	t.Run("unreadable state directory", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.stateDir, "")
		_, err := s.List()
		assert.Error(t, err)
	})
}

func TestStore_Unknown(t *testing.T) {
	t.Parallel()
	t.Run("directories without state", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "known", testNow())
		require.NoError(t, os.MkdirAll(s.dir("stray"), 0o750))
		writeFile(t, filepath.Join(s.root, "CLAUDE.md"), "")
		unknown, err := s.Unknown()
		require.NoError(t, err)
		assert.Equal(t, []string{s.dir("stray")}, unknown)
	})
	t.Run("no workspaces yet", func(t *testing.T) {
		t.Parallel()
		unknown, err := newTestStore(t, nil, nil).Unknown()
		require.NoError(t, err)
		assert.Empty(t, unknown)
	})
	t.Run("unreadable root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.root, "")
		_, err := s.Unknown()
		assert.Error(t, err)
	})
}

func TestStore_Find(t *testing.T) {
	t.Parallel()
	t.Run("by id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login", testNow())
		got, err := s.Find("fix-login")
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("by path", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login", testNow())
		got, err := s.Find(ws.Path)
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).Find("nope")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("broken record", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.recordPath("bad"), "version = ")
		_, err := s.Find("bad")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_byID(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		got, err := s.byID("x")
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("missing", func(t *testing.T) {
		_, err := newTestStore(t, nil, nil).byID("x")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("record without a directory", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		_, err := s.byID("x")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("not an id", func(t *testing.T) {
		_, err := newTestStore(t, nil, nil).byID("a/b")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_recordByID(t *testing.T) {
	t.Run("record without a directory", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		got, err := s.recordByID("x")
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("missing", func(t *testing.T) {
		_, err := newTestStore(t, nil, nil).recordByID("x")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("not an id", func(t *testing.T) {
		_, err := newTestStore(t, nil, nil).recordByID("..")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("broken record", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		writeFile(t, s.recordPath("x"), "version = ")
		_, err := s.recordByID("x")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_complete(t *testing.T) {
	t.Run("record and directory", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		got, err := s.complete("x")
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("no record", func(t *testing.T) {
		_, err := newTestStore(t, nil, nil).complete("x")
		assert.ErrorIs(t, err, errNoRecord)
	})
	t.Run("no directory", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		_, err := s.complete("x")
		assert.ErrorIs(t, err, errNoDir)
	})
	t.Run("directory can't be checked", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		ws := sampleRecord(s.root, "x")
		blocker := filepath.Join(t.TempDir(), "file")
		writeFile(t, blocker, "")
		ws.Path = filepath.Join(blocker, "x")
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		require.NoError(t, writeRecord(s.recordPath("x"), &ws))
		_, err := s.complete("x")
		require.Error(t, err)
		assert.NotErrorIs(t, err, errNoDir)
	})
}

func TestValidID(t *testing.T) {
	t.Run("a name", func(t *testing.T) {
		assert.True(t, validID("fix-login"))
	})
	t.Run("empty", func(t *testing.T) {
		assert.False(t, validID(""))
	})
	t.Run("a path", func(t *testing.T) {
		assert.False(t, validID("a/b"))
	})
	t.Run("dot-dot", func(t *testing.T) {
		assert.False(t, validID(".."))
	})
	t.Run("hidden", func(t *testing.T) {
		assert.False(t, validID(".hidden"))
	})
}

func TestStore_byPath(t *testing.T) {
	t.Parallel()
	t.Run("through a symlink", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(ws.Path, link))
		got, err := s.byPath(link)
		require.NoError(t, err)
		assert.Equal(t, "x", got.ID)
	})
	t.Run("an existing directory that isn't a workspace", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		_, err := s.byPath(t.TempDir())
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("a path that doesn't exist", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).byPath(filepath.Join(t.TempDir(), "missing"))
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("unreadable state", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.stateDir, "")
		_, err := s.byPath(t.TempDir())
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNotFound)
	})
}

func TestResolvePath(t *testing.T) {
	t.Parallel()
	t.Run("resolves symlinks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(dir, link))
		want, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		got, err := resolvePath(link)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
	t.Run("missing path", func(t *testing.T) {
		t.Parallel()
		_, err := resolvePath(filepath.Join(t.TempDir(), "missing"))
		assert.Error(t, err)
	})
}

func TestStateIDs(t *testing.T) {
	t.Parallel()
	t.Run("directories only, without hidden ones", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "a"), 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmp"), 0o750))
		writeFile(t, filepath.Join(dir, "file"), "")
		ids, err := stateIDs(dir)
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, ids)
	})
	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		ids, err := stateIDs(filepath.Join(t.TempDir(), "missing"))
		require.NoError(t, err)
		assert.Empty(t, ids)
	})
}
