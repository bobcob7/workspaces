package workspace

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_Rename(t *testing.T) {
	t.Parallel()
	t.Run("changes the name and keeps the id and path", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		before := putWorkspace(t, s, "fix-login-d8efb24f", testNow())
		got, err := s.Rename(before.ID, "Fix the login page")
		require.NoError(t, err)
		assert.Equal(t, "Fix the login page", got.Name)
		assert.Equal(t, before.ID, got.ID)
		assert.Equal(t, before.Path, got.Path)
		assert.DirExists(t, before.Path)
	})
	t.Run("writes the record", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login-d8efb24f", testNow())
		_, err := s.Rename(ws.ID, "Fix login")
		require.NoError(t, err)
		got, err := readRecord(s.recordPath(ws.ID))
		require.NoError(t, err)
		assert.Equal(t, "Fix login", got.Name)
	})
	t.Run("renames as many times as asked", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login-d8efb24f", testNow())
		for _, name := range []string{"one", "two", "three"} {
			_, err := s.Rename(ws.ID, name)
			require.NoError(t, err)
		}
		got, err := s.Find(ws.ID)
		require.NoError(t, err)
		assert.Equal(t, "three", got.Name)
	})
	t.Run("trims surrounding space", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x-d8efb24f", testNow())
		got, err := s.Rename(ws.ID, "  This is a Test \n")
		require.NoError(t, err)
		assert.Equal(t, "This is a Test", got.Name)
	})
	t.Run("keeps names an id can't be made from", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x-d8efb24f", testNow())
		got, err := s.Rename(ws.ID, "日本語")
		require.NoError(t, err)
		assert.Equal(t, "日本語", got.Name)
	})
	t.Run("an empty name", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x-d8efb24f", testNow())
		_, err := s.Rename(ws.ID, "  ")
		assert.ErrorIs(t, err, ErrNameInvalid)
	})
	t.Run("a control character", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x-d8efb24f", testNow())
		_, err := s.Rename(ws.ID, "a\nb")
		assert.ErrorIs(t, err, ErrNameInvalid)
	})
	t.Run("an unknown workspace", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).Rename("nope", "x")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_SetHidden(t *testing.T) {
	t.Parallel()
	t.Run("hides and writes the record", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login-d8efb24f", testNow())
		got, err := s.SetHidden(ws.ID, true)
		require.NoError(t, err)
		assert.True(t, got.Hidden)
		read, err := readRecord(s.recordPath(ws.ID))
		require.NoError(t, err)
		assert.True(t, read.Hidden)
		assert.Equal(t, hiddenVersion, read.Version)
	})
	t.Run("shows again as version 1", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login-d8efb24f", testNow())
		_, err := s.SetHidden(ws.ID, true)
		require.NoError(t, err)
		got, err := s.SetHidden(ws.ID, false)
		require.NoError(t, err)
		assert.False(t, got.Hidden)
		read, err := readRecord(s.recordPath(ws.ID))
		require.NoError(t, err)
		assert.Equal(t, ws, read)
	})
	t.Run("setting what's set is a no-op", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "fix-login-d8efb24f", testNow())
		got, err := s.SetHidden(ws.ID, false)
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("an unknown workspace", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).SetHidden("nope", true)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_ClearParent(t *testing.T) {
	t.Parallel()
	t.Run("makes a child a root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := sampleRecord(s.root, "child-d8efb24f")
		ws.Parent = "parent-1f0c33d8"
		require.NoError(t, os.MkdirAll(ws.Path, 0o750))
		require.NoError(t, os.MkdirAll(s.stateOf(ws.ID), 0o750))
		require.NoError(t, writeRecord(s.recordPath(ws.ID), &ws))
		got, err := s.ClearParent(ws.ID)
		require.NoError(t, err)
		assert.Empty(t, got.Parent)
		read, err := readRecord(s.recordPath(ws.ID))
		require.NoError(t, err)
		assert.Empty(t, read.Parent)
		assert.Equal(t, visibleVersion, read.Version)
	})
	t.Run("a root stays as it is", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x-d8efb24f", testNow())
		got, err := s.ClearParent(ws.ID)
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("an unknown workspace", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).ClearParent("nope")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}
