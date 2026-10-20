package writeonce

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFile(t *testing.T) {
	t.Parallel()
	t.Run("creates the file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "f.md")
		require.NoError(t, File(path, []byte("hello\n")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "hello\n", string(data))
	})
	t.Run("never overwrites a file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "f.md")
		require.NoError(t, os.WriteFile(path, []byte("mine\n"), 0o600))
		require.NoError(t, File(path, []byte("theirs\n")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "mine\n", string(data))
	})
	t.Run("never replaces a dangling symlink", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "f.md")
		require.NoError(t, os.Symlink(filepath.Join(dir, "missing"), path))
		require.NoError(t, File(path, []byte("x")))
		target, err := os.Readlink(path)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "missing"), target)
	})
	t.Run("leaves no temp files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, File(filepath.Join(dir, "f.md"), []byte("x")))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "f.md", entries[0].Name())
	})
	t.Run("concurrent writers leave one complete file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "f.md")
		var wg sync.WaitGroup
		for _, content := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n"} {
			wg.Go(func() {
				assert.NoError(t, File(path, []byte(content)))
			})
		}
		wg.Wait()
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, data, 5)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
	t.Run("missing directory is an error", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, File(filepath.Join(t.TempDir(), "no", "f.md"), []byte("x")))
	})
	t.Run("read-only directory is an error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		assert.ErrorContains(t, File(filepath.Join(dir, "f.md"), []byte("x")), "writing")
	})
	t.Run("unreadable path is an error", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		assert.ErrorContains(t, File(filepath.Join(blocker, "f.md"), []byte("x")), "checking")
	})
}
