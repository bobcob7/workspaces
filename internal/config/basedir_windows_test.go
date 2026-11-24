package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserBaseDir(t *testing.T) {
	dir, err := UserBaseDir()
	require.NoError(t, err)
	assert.Equal(t, os.Getenv("LOCALAPPDATA"), dir)
}
