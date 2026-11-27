package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseConfigDir(t *testing.T) {
	t.Run("CLAUDE_CONFIG_DIR wins over HOME", func(t *testing.T) {
		assert.Equal(t, "/cfg", BaseConfigDir([]string{"CLAUDE_CONFIG_DIR=/cfg", "HOME=/home/u"}))
	})
	t.Run("HOME/.claude when unset", func(t *testing.T) {
		home := t.TempDir()
		assert.Equal(t, filepath.Join(home, ".claude"), BaseConfigDir([]string{"HOME=" + home}))
	})
	t.Run("empty CLAUDE_CONFIG_DIR counts as unset", func(t *testing.T) {
		home := t.TempDir()
		assert.Equal(t, filepath.Join(home, ".claude"), BaseConfigDir([]string{"CLAUDE_CONFIG_DIR=", "HOME=" + home}))
	})
	t.Run("an overlay's marker names the base", func(t *testing.T) {
		base := t.TempDir()
		overlay := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(overlay, OverlayMarker), []byte(base+"\n"), 0o600))
		assert.Equal(t, base, BaseConfigDir([]string{"CLAUDE_CONFIG_DIR=" + overlay, "HOME=/home/u"}))
	})
	t.Run("neither set is empty", func(t *testing.T) {
		assert.Empty(t, BaseConfigDir(nil))
	})
}

func TestBaseEnviron(t *testing.T) {
	t.Run("an overlay's environ gets the base", func(t *testing.T) {
		base := t.TempDir()
		overlay := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(overlay, OverlayMarker), []byte(base+"\n"), 0o600))
		environ := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=" + overlay}
		got := BaseEnviron(environ)
		assert.Equal(t, base, lookup(got, "CLAUDE_CONFIG_DIR"))
		assert.Equal(t, "/bin", lookup(got, "PATH"))
		assert.Equal(t, []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=" + overlay}, environ, "the input is not changed")
	})
	t.Run("a non-overlay environ is unchanged", func(t *testing.T) {
		environ := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/cfg"}
		assert.Equal(t, environ, BaseEnviron(environ))
	})
}

// overlayFixture is a base Claude Code config directory with every entry
// SeedOverlay handles, a workspaces root two levels below a trusted parent,
// and an overlay directory that isn't made yet.
type overlayFixture struct {
	base         string
	overlay      string
	globalConfig string
	root         string
}

func newOverlayFixture(t *testing.T) overlayFixture {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	f := overlayFixture{
		base:         filepath.Join(tmp, "claude"),
		overlay:      filepath.Join(tmp, "state", "ws1", "claude"),
		globalConfig: filepath.Join(tmp, "claude", ".claude.json"),
		root:         filepath.Join(tmp, "workspaces"),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "x", "api"), 0o750))
	for _, dir := range []string{"hooks", "plugins", "projects", "jobs"} {
		require.NoError(t, os.MkdirAll(filepath.Join(f.base, dir), 0o700))
	}
	for name, body := range map[string]string{
		"settings.json":     `{"theme":"dark"}`,
		"CLAUDE.md":         "# user\n",
		"keybindings.json":  "[]",
		".credentials.json": `{"token":"x"}`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(f.base, name), []byte(body), 0o600))
	}
	return f
}

// writeGlobal writes the base's .claude.json with v.
func (f overlayFixture) writeGlobal(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.globalConfig, data, 0o600))
}

// seed runs SeedOverlay for the fixture.
func (f overlayFixture) seed(t *testing.T) {
	t.Helper()
	require.NoError(t, SeedOverlay(f.overlay, f.base, f.globalConfig, f.root))
}

// readOverlayConfig is the overlay's .claude.json as a map.
func (f overlayFixture) readOverlayConfig(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.overlay, ".claude.json"))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	return got
}

// treeHash maps every path under dir to a digest of its contents, or of
// its link target for a symlink, so a change anywhere shows.
func treeHash(t *testing.T, dir string) map[string]string {
	t.Helper()
	hashes := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			hashes[rel] = "link:" + target
			return linkErr
		}
		if d.IsDir() {
			hashes[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		sum := sha256.Sum256(data)
		hashes[rel] = hex.EncodeToString(sum[:])
		return err
	}))
	return hashes
}

func TestSeedOverlay(t *testing.T) {
	t.Run("copies settings, CLAUDE.md and keybindings", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.seed(t)
		for _, name := range []string{"settings.json", "CLAUDE.md", "keybindings.json"} {
			want, err := os.ReadFile(filepath.Join(f.base, name))
			require.NoError(t, err)
			got, err := os.ReadFile(filepath.Join(f.overlay, name))
			require.NoError(t, err)
			assert.Equal(t, want, got, name)
		}
		require.NoError(t, os.WriteFile(filepath.Join(f.overlay, "settings.json"), []byte(`{"theme":"light"}`), 0o600))
		base, err := os.ReadFile(filepath.Join(f.base, "settings.json"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"theme":"dark"}`, string(base), "editing the copy leaves the base")
	})
	t.Run("links credentials, add-on dirs, projects and jobs", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.seed(t)
		for _, name := range []string{".credentials.json", "hooks", "plugins", "projects", "jobs"} {
			target, err := os.Readlink(filepath.Join(f.overlay, name))
			require.NoError(t, err, name)
			assert.Equal(t, filepath.Join(f.base, name), target, name)
		}
	})
	t.Run("skips what base lacks", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		require.NoError(t, os.Remove(filepath.Join(f.base, "keybindings.json")))
		require.NoError(t, os.Remove(filepath.Join(f.base, "plugins")))
		f.seed(t)
		for _, name := range []string{"keybindings.json", "plugins", "agents", "commands"} {
			_, err := os.Lstat(filepath.Join(f.overlay, name))
			assert.ErrorIs(t, err, fs.ErrNotExist, name)
		}
	})
	t.Run("creates projects and jobs in base when missing", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		require.NoError(t, os.RemoveAll(filepath.Join(f.base, "projects")))
		require.NoError(t, os.RemoveAll(filepath.Join(f.base, "jobs")))
		f.seed(t)
		for _, name := range []string{"projects", "jobs"} {
			info, err := os.Stat(filepath.Join(f.base, name))
			require.NoError(t, err, name)
			assert.True(t, info.IsDir(), name)
			target, err := os.Readlink(filepath.Join(f.overlay, name))
			require.NoError(t, err, name)
			assert.Equal(t, filepath.Join(f.base, name), target, name)
		}
	})
	t.Run("keeps only the allowed .claude.json keys and the workspace root's trust", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		parent := filepath.Dir(f.root)
		f.writeGlobal(t, map[string]any{
			"numStartups":  12,
			"userID":       "u-1",
			"theme":        "dark",
			"mcpServers":   map[string]any{"docs": map[string]any{"command": "docs-mcp"}},
			"oauthAccount": map[string]any{"emailAddress": "a@example.test"},
			"projects": map[string]any{
				f.root:       map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{}},
				parent:       map[string]any{"hasTrustDialogAccepted": true, "lastCost": 0.25},
				"/unrelated": map[string]any{"hasTrustDialogAccepted": true},
				filepath.Join(f.root, "x", "api", "deeper"): map[string]any{"hasTrustDialogAccepted": true},
			},
		})
		f.seed(t)
		got := f.readOverlayConfig(t)
		assert.NotContains(t, got, "numStartups")
		assert.Equal(t, "u-1", got["userID"])
		assert.Equal(t, "dark", got["theme"])
		assert.Contains(t, got, "mcpServers")
		assert.Contains(t, got, "oauthAccount")
		projects, ok := got["projects"].(map[string]any)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{f.root, parent}, mapKeys(projects))
	})
	t.Run("drops a workspace root's parent that doesn't trust it", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		parent := filepath.Dir(f.root)
		f.writeGlobal(t, map[string]any{
			"projects": map[string]any{
				f.root: map[string]any{"hasTrustDialogAccepted": true},
				parent: map[string]any{"hasTrustDialogAccepted": false},
			},
		})
		f.seed(t)
		projects, ok := f.readOverlayConfig(t)["projects"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []string{f.root}, mapKeys(projects))
	})
	t.Run("writes {} without a global config", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.seed(t)
		data, err := os.ReadFile(filepath.Join(f.overlay, ".claude.json"))
		require.NoError(t, err)
		assert.JSONEq(t, "{}", string(data))
	})
	t.Run("re-seeding replaces an existing overlay", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.seed(t)
		require.NoError(t, os.WriteFile(filepath.Join(f.overlay, "stale.txt"), []byte("old"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(f.overlay, "settings.json"), []byte("{}"), 0o600))
		f.seed(t)
		_, err := os.Lstat(filepath.Join(f.overlay, "stale.txt"))
		require.ErrorIs(t, err, fs.ErrNotExist)
		settings, err := os.ReadFile(filepath.Join(f.overlay, "settings.json"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"theme":"dark"}`, string(settings))
	})
	t.Run("never changes base", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.writeGlobal(t, map[string]any{"userID": "u-1", "projects": map[string]any{f.root: map[string]any{"hasTrustDialogAccepted": true}}})
		before := treeHash(t, f.base)
		f.seed(t)
		assert.Equal(t, before, treeHash(t, f.base))
	})
	t.Run("the marker holds the base path", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.seed(t)
		data, err := os.ReadFile(filepath.Join(f.overlay, OverlayMarker))
		require.NoError(t, err)
		assert.Equal(t, f.base+"\n", string(data))
		assert.Equal(t, f.base, BaseConfigDir([]string{"CLAUDE_CONFIG_DIR=" + f.overlay}))
	})
	t.Run("modes are 0700 for the directory and 0600 for files", func(t *testing.T) {
		t.Parallel()
		f := newOverlayFixture(t)
		f.writeGlobal(t, map[string]any{"userID": "u-1"})
		f.seed(t)
		dir, err := os.Stat(f.overlay)
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o700), dir.Mode().Perm())
		for _, name := range []string{OverlayMarker, ".claude.json", "settings.json", "CLAUDE.md", "keybindings.json"} {
			info, err := os.Stat(filepath.Join(f.overlay, name))
			require.NoError(t, err, name)
			assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm(), name)
		}
	})
}

// mapKeys is the keys of m, in no order.
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
