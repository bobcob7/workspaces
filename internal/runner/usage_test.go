package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadUsage(t *testing.T) {
	t.Parallel()
	t.Run("reads usage.json", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), UsageName)
		require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"model":"qwen/qwen3.5-9b","endpoint":"gpu","exit":"done","steps":31,"usage":{"requests":33,"prompt_tokens":5,"completion_tokens":6},"wall_seconds":1104}`), fileMode))
		usage, err := ReadUsage(path)
		require.NoError(t, err)
		assert.Equal(t, "gpu", usage.Endpoint)
		assert.Equal(t, 31, usage.Steps)
		assert.Equal(t, 33, usage.Usage.Requests)
		assert.Equal(t, 1104, usage.WallSeconds)
	})
	t.Run("a missing file is not-exist", func(t *testing.T) {
		t.Parallel()
		_, err := ReadUsage(filepath.Join(t.TempDir(), UsageName))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}
