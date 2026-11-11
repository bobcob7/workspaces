package loop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDataStore(t *testing.T) *DataStore {
	t.Helper()
	return NewDataStore(t.TempDir())
}

func TestDataStore_Paths(t *testing.T) {
	t.Parallel()
	d := NewDataStore("state")
	t.Run("state dir is the one given", func(t *testing.T) {
		assert.Equal(t, "state", d.StateDir())
	})
	t.Run("loop dir is under .loops", func(t *testing.T) {
		assert.Equal(t, filepath.Join("state", ".loops", "deps"), d.Dir("deps"))
	})
	t.Run("history and memory files are in the loop dir", func(t *testing.T) {
		assert.Equal(t, filepath.Join("state", ".loops", "deps", "history.json"), d.HistoryPath("deps"))
		assert.Equal(t, filepath.Join("state", ".loops", "deps", "memory.json"), d.MemoryPath("deps"))
	})
	t.Run("report is reports/<seq>.md", func(t *testing.T) {
		assert.Equal(t, filepath.Join("state", ".loops", "deps", "reports", "42.md"), d.ReportPath("deps", 42))
	})
}

func TestDataStore_RemoveAll(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	t.Run("deletes the loop's data", func(t *testing.T) {
		require.NoError(t, d.WriteReport("deps", 1, []byte("report")))
		require.NoError(t, d.RemoveAll("deps"))
		_, err := os.Stat(d.Dir("deps"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("a missing dir is not an error", func(t *testing.T) {
		assert.NoError(t, d.RemoveAll("never"))
	})
	t.Run("a bad name is invalid", func(t *testing.T) {
		assert.ErrorIs(t, d.RemoveAll("../escape"), ErrInvalid)
	})
}

func TestDataStore_WriteRunMark(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	started := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	t.Run("writes the mark and reads it back", func(t *testing.T) {
		workspace := "loop-deps-bump-1a2b3c4d"
		require.NoError(t, d.WriteRunMark(workspace, RunMark{Loop: "deps-bump", StartedAt: started}))
		got, found, err := d.ReadRunMark(workspace)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, RunMarkVersion, got.Version)
		assert.Equal(t, "deps-bump", got.Loop)
		assert.True(t, got.StartedAt.Equal(started))
	})
	t.Run("leaves only loop.toml in the workspace dir", func(t *testing.T) {
		entries, err := os.ReadDir(filepath.Join(d.StateDir(), "loop-deps-bump-1a2b3c4d"))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "loop.toml", entries[0].Name())
	})
	t.Run("a workspace id that is not one path element is invalid", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "a/b"} {
			assert.ErrorIs(t, d.WriteRunMark(id, RunMark{Loop: "deps"}), ErrInvalid, id)
		}
	})
}

func TestDataStore_ReadRunMark(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	t.Run("a missing mark is not found", func(t *testing.T) {
		_, found, err := d.ReadRunMark("loop-deps-bump-1a2b3c4d")
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("a mark of a newer version is ErrNewerData", func(t *testing.T) {
		workspace := "loop-deps-bump-1a2b3c4d"
		require.NoError(t, writeFileAtomic(d.runMarkPath(workspace), []byte("version = 2\nloop = \"deps\"\n")))
		_, _, err := d.ReadRunMark(workspace)
		assert.ErrorIs(t, err, ErrNewerData)
	})
}
