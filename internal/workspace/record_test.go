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
		Version:   formatVersion,
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
	t.Run("reads a hidden record", func(t *testing.T) {
		t.Parallel()
		got, err := readRecord(recordIn(t, "version = 2\nid = \"fix-login\"\npath = \"/w/fix-login\"\nhidden = true\n"))
		require.NoError(t, err)
		assert.True(t, got.Hidden)
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
	t.Run("a newer version", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = 4\nid = \"fix-login\"\npath = \"/w/fix-login\"\n"))
		assert.ErrorContains(t, err, "written by a newer ws (version 4; this ws reads up to 3); upgrade ws")
	})
	t.Run("a newer version with keys this ws doesn't know", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "version = 4\nid = \"fix-login\"\npath = \"/w/fix-login\"\nextra = 1\n"))
		assert.ErrorContains(t, err, "newer ws")
	})
	t.Run("no version", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(recordIn(t, "id = \"fix-login\"\npath = \"/w/fix-login\"\n"))
		assert.ErrorContains(t, err, "unsupported version 0")
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
	t.Run("a visible workspace is version 1 without hidden", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "workspace.toml")
		ws := sampleRecord("/w", "x")
		ws.Version = 7
		require.NoError(t, writeRecord(path, &ws))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(data), "version = 1\n")
		assert.NotContains(t, string(data), "hidden")
		assert.Equal(t, 1, ws.Version)
	})
	t.Run("a hidden workspace is version 2", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "x", "workspace.toml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		ws := sampleRecord("/w", "x")
		ws.Hidden = true
		require.NoError(t, writeRecord(path, &ws))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(data), "version = 2\n")
		assert.Contains(t, string(data), "hidden = true\n")
		got, err := readRecord(path)
		require.NoError(t, err)
		assert.Equal(t, ws, got)
	})
	t.Run("a workspace with a parent is version 3, hidden or not", func(t *testing.T) {
		t.Parallel()
		for _, hidden := range []bool{false, true} {
			dir := t.TempDir()
			path := filepath.Join(dir, "x", "workspace.toml")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			ws := sampleRecord("/w", "x")
			ws.Parent, ws.Hidden = "fix-login-d8efb24f", hidden
			require.NoError(t, writeRecord(path, &ws))
			got, err := readRecord(path)
			require.NoError(t, err)
			assert.Equal(t, parentVersion, got.Version)
			assert.Equal(t, ws, got)
		}
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

func TestWriteTOML(t *testing.T) {
	t.Parallel()
	t.Run("writes the value and leaves only the file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "x")
		require.NoError(t, writeTOML(path, map[string]string{"a": "b"}))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "a = \"b\"\n", string(data))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
	t.Run("an unencodable value leaves nothing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.Error(t, writeTOML(filepath.Join(dir, "x"), make(chan int)))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestCheckVersion(t *testing.T) {
	t.Parallel()
	t.Run("known", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, checkVersion(1, 1))
	})
	t.Run("newer", func(t *testing.T) {
		t.Parallel()
		assert.ErrorContains(t, checkVersion(3, 1), "written by a newer ws (version 3; this ws reads up to 1); upgrade ws")
	})
	t.Run("a record with a parent read by a ws from before trees", func(t *testing.T) {
		t.Parallel()
		assert.ErrorContains(t, checkVersion(parentVersion, hiddenVersion), "upgrade ws")
	})
	t.Run("a hidden record read by a ws from before hidden", func(t *testing.T) {
		t.Parallel()
		assert.ErrorContains(t, checkVersion(hiddenVersion, visibleVersion), "upgrade ws")
	})
	t.Run("not a version", func(t *testing.T) {
		t.Parallel()
		assert.ErrorContains(t, checkVersion(0, 1), "unsupported version 0")
	})
}

func TestWorkspace_SessionDir(t *testing.T) {
	t.Parallel()
	repo := func(path string) Repo { return Repo{ID: "example.test/o/" + path, Path: path} }
	tests := []struct {
		name  string
		repos []Repo
		want  string
	}{
		{name: "no repos is the workspace", want: "/cfg/workspaces/x"},
		{name: "one repo is its directory", repos: []Repo{repo("api")}, want: "/cfg/workspaces/x/api"},
		{name: "two repos is the workspace", repos: []Repo{repo("api"), repo("web")}, want: "/cfg/workspaces/x"},
		{name: "a repo path leaving the workspace is ignored", repos: []Repo{repo("../y")}, want: "/cfg/workspaces/x"},
		{name: "an absolute repo path is ignored", repos: []Repo{repo("/etc")}, want: "/cfg/workspaces/x"},
		{name: "a repo path of the workspace itself is ignored", repos: []Repo{repo("./")}, want: "/cfg/workspaces/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws := Workspace{Path: "/cfg/workspaces/x", Repos: tt.repos}
			assert.Equal(t, tt.want, ws.SessionDir())
		})
	}
}
