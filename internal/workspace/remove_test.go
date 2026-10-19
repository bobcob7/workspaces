package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_Remove(t *testing.T) {
	t.Parallel()
	t.Run("removes the directory and then the state", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		writeFile(t, filepath.Join(ws.Path, "n", "dirty.txt"), "uncommitted")
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		assert.NoDirExists(t, ws.Path)
		assert.NoDirExists(t, s.stateOf("x"))
	})
	t.Run("directory already gone", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.RemoveAll(ws.Path))
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		assert.NoDirExists(t, s.stateOf("x"))
	})
	t.Run("unknown workspace", func(t *testing.T) {
		t.Parallel()
		assert.ErrorIs(t, newTestStore(t, nil, nil).Remove("x", RemoveOptions{}), ErrNotFound)
	})
	t.Run("refused by the guard, keeping everything", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		outside := t.TempDir()
		ws := sampleRecord(s.root, "x")
		ws.Path = outside
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		require.NoError(t, writeRecord(s.recordPath("x"), &ws))
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		require.ErrorIs(t, s.Remove("x", RemoveOptions{}), ErrUnsafe)
		assert.DirExists(t, outside)
		assert.FileExists(t, s.recordPath("x"))
	})
	t.Run("outside the root when allowed", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		outside := filepath.Join(t.TempDir(), "elsewhere")
		require.NoError(t, os.MkdirAll(outside, 0o750))
		ws := sampleRecord(s.root, "x")
		ws.Path = outside
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		require.NoError(t, writeRecord(s.recordPath("x"), &ws))
		require.NoError(t, s.Remove("x", RemoveOptions{AllowOutsideRoot: true}))
		assert.NoDirExists(t, outside)
	})
}

func TestStore_guard(t *testing.T) {
	t.Parallel()
	t.Run("allows workspaces/<id>", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := putWorkspace(t, s, "x", testNow())
		target, gone, err := s.guard(&ws, RemoveOptions{})
		require.NoError(t, err)
		assert.False(t, gone)
		want, err := filepath.EvalSymlinks(ws.Path)
		require.NoError(t, err)
		assert.Equal(t, want, target)
	})
	t.Run("a missing directory is gone", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		_, gone, err := s.guard(new(sampleRecord(s.root, "x")), RemoveOptions{})
		require.NoError(t, err)
		assert.True(t, gone)
	})
	t.Run("refuses a recorded path outside the root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		ws := sampleRecord(s.root, "x")
		ws.Path = t.TempDir()
		_, _, err := s.guard(&ws, RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("refuses another workspace's directory", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		other := putWorkspace(t, s, "other", testNow())
		ws := sampleRecord(s.root, "x")
		ws.Path = other.Path
		_, _, err := s.guard(&ws, RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("refuses a symlink that points outside the root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		require.NoError(t, os.Symlink(t.TempDir(), s.dir("x")))
		_, _, err := s.guard(new(sampleRecord(s.root, "x")), RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("refuses a nested path", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := sampleRecord(s.root, "x")
		ws.Path = filepath.Join(s.root, "x", "deeper")
		require.NoError(t, os.MkdirAll(ws.Path, 0o750))
		_, _, err := s.guard(&ws, RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("refuses the root itself, even outside-root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		ws := sampleRecord(s.root, "x")
		ws.Path = s.root
		_, _, err := s.guard(&ws, RemoveOptions{AllowOutsideRoot: true})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("refuses a parent of the root, even outside-root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		ws := sampleRecord(s.root, "x")
		ws.Path = "/"
		_, _, err := s.guard(&ws, RemoveOptions{AllowOutsideRoot: true})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("refuses .. that climbs out of the root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		ws := sampleRecord(s.root, "x")
		ws.Path = filepath.Join(s.root, "x", "..", "..", "state")
		require.NoError(t, os.MkdirAll(filepath.Join(s.root, "x"), 0o750))
		require.NoError(t, os.MkdirAll(s.stateDir, 0o750))
		_, _, err := s.guard(&ws, RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("missing root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		ws := sampleRecord(s.root, "x")
		ws.Path = t.TempDir()
		_, _, err := s.guard(&ws, RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
	t.Run("a path that can't be resolved", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		blocker := filepath.Join(t.TempDir(), "file")
		writeFile(t, blocker, "")
		ws := sampleRecord(s.root, "x")
		ws.Path = filepath.Join(blocker, "x")
		_, _, err := s.guard(&ws, RemoveOptions{})
		assert.ErrorIs(t, err, ErrUnsafe)
	})
}

func TestContains(t *testing.T) {
	t.Run("same directory", func(t *testing.T) {
		assert.True(t, contains("/a/b", "/a/b"))
	})
	t.Run("parent", func(t *testing.T) {
		assert.True(t, contains("/a", "/a/b"))
	})
	t.Run("filesystem root", func(t *testing.T) {
		assert.True(t, contains("/", "/a/b"))
	})
	t.Run("child", func(t *testing.T) {
		assert.False(t, contains("/a/b/c", "/a/b"))
	})
	t.Run("sibling with a common prefix", func(t *testing.T) {
		assert.False(t, contains("/a/bc", "/a/b"))
	})
	t.Run("name starting with dots", func(t *testing.T) {
		assert.True(t, contains("/a", "/a/..b"))
	})
	t.Run("relative and absolute", func(t *testing.T) {
		assert.False(t, contains("a", "/a"))
	})
}
