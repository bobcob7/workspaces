package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trustFixture is a Claude Code config directory with a resolved workspace
// root trusted in its .claude.json, and a Client using it.
type trustFixture struct {
	client *Client
	config string
	root   string
}

func newTrustFixture(t *testing.T, rootTrusted bool) *trustFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root := filepath.Join(base, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "x", "api"), 0o750))
	configDir := filepath.Join(base, "claude")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	f := &trustFixture{client: New(discardLogger(), "claude", []string{"CLAUDE_CONFIG_DIR=" + configDir}), config: filepath.Join(configDir, ".claude.json"), root: root}
	f.write(t, map[string]any{
		"numStartups":  12,
		"oauthAccount": map[string]any{"emailAddress": "a&b@example.test"},
		"projects": map[string]any{
			root:     map[string]any{"hasTrustDialogAccepted": rootTrusted, "allowedTools": []any{}},
			"/other": map[string]any{"hasTrustDialogAccepted": true, "lastCost": 0.25},
		},
	})
	return f
}

func (f *trustFixture) write(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.config, data, 0o600))
}

func (f *trustFixture) read(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(f.config)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	return got
}

func (f *trustFixture) project(t *testing.T, dir string) map[string]any {
	t.Helper()
	projects, ok := f.read(t)["projects"].(map[string]any)
	require.True(t, ok)
	project, _ := projects[dir].(map[string]any)
	return project
}

func TestConfigFile(t *testing.T) {
	t.Parallel()
	t.Run("CLAUDE_CONFIG_DIR wins", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/c/.claude.json", configFile([]string{"HOME=/h", "CLAUDE_CONFIG_DIR=/c"}))
	})
	t.Run("else the home directory", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/h/.claude.json", configFile([]string{"HOME=/h", "CLAUDE_CONFIG_DIR="}))
	})
	t.Run("else none", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, configFile(nil))
	})
}

func TestClient_Trust(t *testing.T) {
	t.Parallel()
	t.Run("trusts a directory below a trusted parent", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		dir := filepath.Join(f.root, "x", "api")
		require.NoError(t, f.client.Trust(t.Context(), dir, f.root))
		assert.Equal(t, map[string]any{"hasTrustDialogAccepted": true}, f.project(t, dir))
	})
	t.Run("keeps every other key as it was", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		before := f.read(t)
		dir := filepath.Join(f.root, "x", "api")
		require.NoError(t, f.client.Trust(t.Context(), dir, f.root))
		after := f.read(t)
		projects, ok := after["projects"].(map[string]any)
		require.True(t, ok)
		delete(projects, dir)
		assert.Equal(t, before, after)
	})
	t.Run("doesn't escape HTML in values it rewrites", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		root, err := json.Marshal(f.root)
		require.NoError(t, err)
		config := `{"oauthAccount":{"emailAddress":"a&b@example.test"},"projects":{` + string(root) + `:{"hasTrustDialogAccepted":true}}}`
		require.NoError(t, os.WriteFile(f.config, []byte(config), 0o600))
		require.NoError(t, f.client.Trust(t.Context(), filepath.Join(f.root, "x", "api"), f.root))
		data, err := os.ReadFile(f.config)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"a&b@example.test"`)
	})
	t.Run("keeps an existing entry's keys", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		dir := filepath.Join(f.root, "x", "api")
		f.write(t, map[string]any{"projects": map[string]any{
			f.root: map[string]any{"hasTrustDialogAccepted": true},
			dir:    map[string]any{"hasTrustDialogAccepted": false, "lastCost": 1.5},
		}})
		require.NoError(t, f.client.Trust(t.Context(), dir, f.root))
		assert.Equal(t, map[string]any{"hasTrustDialogAccepted": true, "lastCost": 1.5}, f.project(t, dir))
	})
	t.Run("keys both directories with symlinks resolved", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		link := filepath.Join(filepath.Dir(f.root), "link")
		require.NoError(t, os.Symlink(f.root, link))
		require.NoError(t, f.client.Trust(t.Context(), filepath.Join(link, "x", "api"), link))
		assert.Equal(t, true, f.project(t, filepath.Join(f.root, "x", "api"))["hasTrustDialogAccepted"])
	})
	t.Run("keeps the file's permissions", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		require.NoError(t, f.client.Trust(t.Context(), filepath.Join(f.root, "x", "api"), f.root))
		info, err := os.Stat(f.config)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("writes nothing when the parent isn't trusted", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, false)
		before, err := os.ReadFile(f.config)
		require.NoError(t, err)
		err = f.client.Trust(t.Context(), filepath.Join(f.root, "x", "api"), f.root)
		require.ErrorIs(t, err, ErrParentUntrusted)
		after, err := os.ReadFile(f.config)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
	t.Run("writes nothing when the directory is already trusted", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		old := time.Now().Add(-time.Hour)
		require.NoError(t, os.Chtimes(f.config, old, old))
		require.NoError(t, f.client.Trust(t.Context(), f.root, f.root))
		info, err := os.Stat(f.config)
		require.NoError(t, err)
		assert.WithinDuration(t, old, info.ModTime(), time.Second)
	})
	t.Run("fails on a config that isn't JSON", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		require.NoError(t, os.WriteFile(f.config, []byte("{"), 0o600))
		assert.Error(t, f.client.Trust(t.Context(), filepath.Join(f.root, "x", "api"), f.root))
	})
	t.Run("trusts nothing without a config", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		require.NoError(t, os.Remove(f.config))
		require.ErrorIs(t, f.client.Trust(t.Context(), filepath.Join(f.root, "x", "api"), f.root), ErrParentUntrusted)
		assert.NoFileExists(t, f.config)
	})
	t.Run("releases Claude Code's lock", func(t *testing.T) {
		t.Parallel()
		f := newTrustFixture(t, true)
		require.NoError(t, f.client.Trust(t.Context(), filepath.Join(f.root, "x", "api"), f.root))
		assert.NoDirExists(t, f.config+".lock")
	})
}

func TestLockConfig(t *testing.T) {
	t.Parallel()
	t.Run("waits for the holder to let go", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), ".claude.json")
		require.NoError(t, os.Mkdir(path+".lock", 0o700))
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = os.Remove(path + ".lock")
		}()
		start := time.Now()
		unlock, err := lockConfig(t.Context(), path)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
		assert.DirExists(t, path+".lock")
		unlock()
		assert.NoDirExists(t, path+".lock")
	})
	t.Run("takes over a stale lock", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), ".claude.json")
		require.NoError(t, os.Mkdir(path+".lock", 0o700))
		old := time.Now().Add(-time.Minute)
		require.NoError(t, os.Chtimes(path+".lock", old, old))
		unlock, err := lockConfig(t.Context(), path)
		require.NoError(t, err)
		unlock()
	})
	t.Run("gives up when ctx ends", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), ".claude.json")
		require.NoError(t, os.Mkdir(path+".lock", 0o700))
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := lockConfig(ctx, path)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
