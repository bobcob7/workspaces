package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/writeonce"
)

// envConfigDir overrides the default CONFIG_DIR (ADR 0011).
const envConfigDir = "WS_CONFIG_DIR"

// Loader resolves CONFIG_DIR and reads config.toml from it.
type Loader struct {
	getenv        func(string) string
	userConfigDir func() (string, error)
}

// NewLoader returns a Loader that reads the environment with getenv and
// finds the platform config directory with userConfigDir, normally
// os.Getenv and os.UserConfigDir.
func NewLoader(getenv func(string) string, userConfigDir func() (string, error)) *Loader {
	return &Loader{getenv: getenv, userConfigDir: userConfigDir}
}

// Load resolves CONFIG_DIR, creates it and a commented config.toml if
// they're missing, and reads the config. configDir is the --config-dir flag,
// empty when unset.
func (l *Loader) Load(configDir string) (Paths, Config, error) {
	root, err := l.root(configDir)
	if err != nil {
		return Paths{}, Config{}, err
	}
	paths := NewPaths(root)
	if err = writeDefault(paths.ConfigFile); err != nil {
		return Paths{}, Config{}, err
	}
	data, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		return Paths{}, Config{}, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := parse(data)
	if err != nil {
		return Paths{}, Config{}, fmt.Errorf("%s: %w", paths.ConfigFile, err)
	}
	return paths, cfg, nil
}

// root picks CONFIG_DIR: the flag, then WS_CONFIG_DIR, then
// os.UserConfigDir()/workspaces. The result is absolute.
func (l *Loader) root(configDir string) (string, error) {
	dir := configDir
	if dir == "" {
		dir = l.getenv(envConfigDir)
	}
	if dir == "" {
		base, err := l.userConfigDir()
		if err != nil {
			return "", fmt.Errorf("finding the config directory (set %s): %w", envConfigDir, err)
		}
		dir = filepath.Join(base, "workspaces")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving config directory %s: %w", dir, err)
	}
	return abs, nil
}

// writeDefault creates path with the commented default config unless it
// exists, through a temp file linked into place, so a concurrent ws never
// sees a partial file and an existing config is never overwritten.
func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := writeonce.File(path, []byte(defaultFile)); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}
	return nil
}
