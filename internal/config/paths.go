package config

import (
	"path/filepath"
)

// Paths are the locations under CONFIG_DIR (ADR 0011).
type Paths struct {
	Root       string
	ConfigFile string
	Cache      string
	Workspaces string
	State      string
}

// NewPaths lays out the files and directories under root.
func NewPaths(root string) Paths {
	return Paths{
		Root:       root,
		ConfigFile: filepath.Join(root, "config.toml"),
		Cache:      filepath.Join(root, "cache"),
		Workspaces: filepath.Join(root, "workspaces"),
		State:      filepath.Join(root, "state"),
	}
}
