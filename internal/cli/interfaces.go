package cli

import (
	"github.com/bobcob7/workspaces/internal/config"
)

//go:generate go tool moq -out moq_test.go . configLoader

// configLoader resolves CONFIG_DIR and reads config.toml.
type configLoader interface {
	Load(configDir string) (config.Paths, config.Config, error)
}
