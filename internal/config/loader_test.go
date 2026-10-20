package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errNoHome = errors.New("no home")

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func userConfigDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func TestNewLoader(t *testing.T) {
	l := NewLoader(env(map[string]string{envConfigDir: "/x"}), userConfigDir("/base"))
	assert.Equal(t, "/x", l.getenv(envConfigDir))
	base, err := l.userConfigDir()
	require.NoError(t, err)
	assert.Equal(t, "/base", base)
}

func TestLoader_root(t *testing.T) {
	t.Run("flag wins", func(t *testing.T) {
		l := NewLoader(env(map[string]string{envConfigDir: "/env"}), userConfigDir("/base"))
		root, err := l.root("/flag")
		require.NoError(t, err)
		assert.Equal(t, "/flag", root)
	})
	t.Run("then WS_CONFIG_DIR", func(t *testing.T) {
		l := NewLoader(env(map[string]string{envConfigDir: "/env"}), userConfigDir("/base"))
		root, err := l.root("")
		require.NoError(t, err)
		assert.Equal(t, "/env", root)
	})
	t.Run("then the user config dir", func(t *testing.T) {
		l := NewLoader(env(nil), userConfigDir("/base"))
		root, err := l.root("")
		require.NoError(t, err)
		assert.Equal(t, "/base/workspaces", root)
	})
	t.Run("empty WS_CONFIG_DIR counts as unset", func(t *testing.T) {
		l := NewLoader(env(map[string]string{envConfigDir: ""}), userConfigDir("/base"))
		root, err := l.root("")
		require.NoError(t, err)
		assert.Equal(t, "/base/workspaces", root)
	})
	t.Run("empty flag falls through to WS_CONFIG_DIR", func(t *testing.T) {
		l := NewLoader(env(map[string]string{envConfigDir: "/env"}), userConfigDir("/base"))
		root, err := l.root("")
		require.NoError(t, err)
		assert.Equal(t, "/env", root)
	})
	t.Run("relative paths become absolute", func(t *testing.T) {
		l := NewLoader(env(nil), userConfigDir("/base"))
		root, err := l.root("rel")
		require.NoError(t, err)
		wd, err := os.Getwd()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(wd, "rel"), root)
	})
	t.Run("no user config dir is an error", func(t *testing.T) {
		l := NewLoader(env(nil), func() (string, error) { return "", errNoHome })
		_, err := l.root("")
		require.ErrorIs(t, err, errNoHome)
		assert.ErrorContains(t, err, envConfigDir)
	})
}

func TestLoader_Load(t *testing.T) {
	t.Parallel()
	t.Run("creates CONFIG_DIR and the default config", func(t *testing.T) {
		t.Parallel()
		root := filepath.Join(t.TempDir(), "ws")
		paths, cfg, err := NewLoader(env(nil), userConfigDir("")).Load(root)
		require.NoError(t, err)
		assert.Equal(t, NewPaths(root), paths)
		assert.Equal(t, Default(), cfg)
		assert.FileExists(t, paths.ConfigFile)
	})
	t.Run("reads an existing config", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "config.toml"), []byte("[defaults]\nowners = [\"me\"]\n"), 0o600))
		_, cfg, err := NewLoader(env(nil), userConfigDir("")).Load(root)
		require.NoError(t, err)
		assert.Equal(t, []string{"me"}, cfg.Defaults.Owners)
	})
	t.Run("invalid config names the file", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		file := filepath.Join(root, "config.toml")
		require.NoError(t, os.WriteFile(file, []byte("nope = 1\n"), 0o600))
		_, _, err := NewLoader(env(nil), userConfigDir("")).Load(root)
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, file)
	})
	t.Run("root error is returned", func(t *testing.T) {
		t.Parallel()
		_, _, err := NewLoader(env(nil), func() (string, error) { return "", errNoHome }).Load("")
		assert.ErrorIs(t, err, errNoHome)
	})
	t.Run("uncreatable CONFIG_DIR is an error", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		_, _, err := NewLoader(env(nil), userConfigDir("")).Load(filepath.Join(blocker, "ws"))
		assert.Error(t, err)
	})
	t.Run("unreadable config is an error", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, "config.toml"), 0o750))
		_, _, err := NewLoader(env(nil), userConfigDir("")).Load(root)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalid)
	})
}

func TestWriteDefault(t *testing.T) {
	t.Parallel()
	t.Run("writes the default file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, writeDefault(path))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, defaultFile, string(data))
	})
	t.Run("creates missing directories", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "a", "b", "config.toml")
		require.NoError(t, writeDefault(path))
		assert.FileExists(t, path)
	})
	t.Run("never overwrites", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(path, []byte("# mine\n"), 0o600))
		require.NoError(t, writeDefault(path))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "# mine\n", string(data))
	})
	t.Run("leaves no temp files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, writeDefault(filepath.Join(dir, "config.toml")))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "config.toml", entries[0].Name())
	})
	t.Run("read-only directory is an error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		assert.ErrorContains(t, writeDefault(filepath.Join(dir, "config.toml")), "writing default config")
	})
	t.Run("unwritable directory is an error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		blocker := filepath.Join(dir, "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		assert.Error(t, writeDefault(filepath.Join(blocker, "config.toml")))
	})
}
