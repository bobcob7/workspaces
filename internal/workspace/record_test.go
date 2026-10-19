package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleRecord is a valid record for workspace id stored at root.
func sampleRecord(root, id string) Workspace {
	return Workspace{
		Version:   recordVersion,
		ID:        id,
		Name:      "Fix Login",
		Path:      filepath.Join(root, id),
		CreatedAt: time.Date(2026, 10, 18, 8, 12, 0, 0, time.UTC),
		Repos: []Repo{{
			ID: "example.test/o/n", Spec: "n@main", Path: "n", Upstream: "https://example.test/o/n.git",
			Ref: "main", RefKind: "branch", Commit: "3f2a9c1e4b",
		}},
	}
}

// writeFile writes content to path, creating its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestReadRecord(t *testing.T) {
	t.Parallel()
	recordIn := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fix-login", "workspace.toml")
		writeFile(t, path, content)
		return path
	}
	t.Run("reads what writeRecord wrote", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		ws := sampleRecord("/cfg/workspaces", "fix-login")
		path := filepath.Join(dir, "fix-login", "workspace.toml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, writeRecord(path, &ws))
		got, err := readRecord(path)
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(filepath.Join(t.TempDir(), "x", "workspace.toml"))
		assert.ErrorIs(t, err, errNoRecord)
	})
	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "x", "workspace.toml")
		require.NoError(t, os.MkdirAll(path, 0o750))
		_, err := readRecord(path)
		require.Error(t, err)
		assert.NotErrorIs(t, err, errNoRecord)
	})
	t.Run("bad TOML", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = "))
		assert.ErrorContains(t, err, "parsing")
	})
	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = 1\nid = \"fix-login\"\npath = \"/w/fix-login\"\nextra = 1\n"))
		assert.ErrorContains(t, err, "unknown key extra")
	})
	t.Run("unsupported version", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = 2\nid = \"fix-login\"\npath = \"/w/fix-login\"\n"))
		assert.ErrorContains(t, err, "unsupported version 2")
	})
	t.Run("id that doesn't match the directory", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = 1\nid = \"other\"\npath = \"/w/other\"\n"))
		assert.ErrorContains(t, err, "doesn't match")
	})
	t.Run("relative path", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = 1\nid = \"fix-login\"\npath = \"fix-login\"\n"))
		assert.ErrorContains(t, err, "isn't absolute")
	})
}

func TestWriteRecord(t *testing.T) {
	t.Parallel()
	t.Run("leaves only the record", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, writeRecord(filepath.Join(dir, "workspace.toml"), new(sampleRecord("/w", "x"))))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "workspace.toml", entries[0].Name())
	})
	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		err := writeRecord(filepath.Join(t.TempDir(), "missing", "workspace.toml"), new(sampleRecord("/w", "x")))
		assert.Error(t, err)
	})
	t.Run("can't replace a directory", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "workspace.toml")
		require.NoError(t, os.MkdirAll(filepath.Join(path, "child"), 0o750))
		assert.Error(t, writeRecord(path, new(sampleRecord("/w", "x"))))
	})
}
