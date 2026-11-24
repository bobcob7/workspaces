package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/privfile"
)

func TestSocketBaseDir(t *testing.T) {
	t.Parallel()
	dir := socketBaseDir(func(string) string { return "" }, 0)
	rel, err := filepath.Rel(os.TempDir(), dir)
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(rel, ".."))
}

func TestPrepareSocketDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ws")
	require.NoError(t, prepareSocketDir(dir))
	assert.NoError(t, privfile.CheckPrivate(dir))
}
