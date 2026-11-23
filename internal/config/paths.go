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
	// Reviews holds submitted reviews (ADR 0039).
	Reviews string
	// ReviewProfiles holds the user's review profiles (ADR 0038).
	ReviewProfiles string
	// Loops holds the agent loops (ADR 0048).
	Loops string
	// MachineRoles holds the user's ws machine roles (ADR 0050).
	MachineRoles string
	// Environments is the controller's state: the CA, the environments'
	// records and the revocations (ADR 0074).
	Environments string
	// Environment is the agent's own files, in an agent's CONFIG_DIR (ADR 0075).
	Environment string
	// Artifacts holds artifacts and their comments (ADR 0078).
	Artifacts string
}

// NewPaths lays out the files and directories under root.
func NewPaths(root string) Paths {
	return Paths{
		Root:       root,
		ConfigFile: filepath.Join(root, "config.toml"),
		Cache:      filepath.Join(root, "cache"),
		Workspaces: filepath.Join(root, "workspaces"),
		State:      filepath.Join(root, "state"),
		Reviews:    filepath.Join(root, "reviews"),
		// review/ is config the user writes; reviews/ is data ws writes.
		ReviewProfiles: filepath.Join(root, "review", "profiles"),
		Loops:          filepath.Join(root, "loops"),
		MachineRoles:   filepath.Join(root, "machine", "roles"),
		Environments:   filepath.Join(root, "environments"),
		Environment:    filepath.Join(root, "environment"),
		Artifacts:      filepath.Join(root, "artifacts"),
	}
}
