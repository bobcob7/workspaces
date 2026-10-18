package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPaths(t *testing.T) {
	paths := NewPaths("/cfg")
	assert.Equal(t, Paths{
		Root:       "/cfg",
		ConfigFile: "/cfg/config.toml",
		Cache:      "/cfg/cache",
		Workspaces: "/cfg/workspaces",
		State:      "/cfg/state",
	}, paths)
}
