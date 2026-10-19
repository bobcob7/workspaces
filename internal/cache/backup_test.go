package cache

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExcludeFromBackup(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	err := excludeFromBackup(t.Context(), missing)
	if runtime.GOOS != "darwin" {
		assert.NoError(t, err, "a no-op off macOS")
		return
	}
	assert.ErrorContains(t, err, "tmutil addexclusion", "tmutil fails on a missing path")
}
