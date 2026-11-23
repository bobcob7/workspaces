package environment

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_Revoked(t *testing.T) {
	t.Run("is an empty set when there is no file", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		revoked, err := store.Revoked()
		require.NoError(t, err)
		assert.NotNil(t, revoked)
		assert.Empty(t, revoked)
	})
	t.Run("ignores comments and blank lines", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, os.MkdirAll(store.dir, 0o700))
		content := "# revoked by ws env\n\nabc123 env-1a2b3c4d 2026-11-22T14:02:00Z\n   \n"
		require.NoError(t, os.WriteFile(filepath.Join(store.dir, revokedFile), []byte(content), 0o600))
		revoked, err := store.Revoked()
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"abc123": true}, revoked)
	})
	t.Run("survives a reread through a new store", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, os.MkdirAll(store.dir, 0o700))
		require.NoError(t, store.revoke("5e0c", "env-1a2b3c4d"))
		require.NoError(t, store.revoke("9f1a", "env-2b3c4d5e"))
		reread := NewStore(store.dir, store.stateDir, fixedClock(), nil)
		revoked, err := reread.Revoked()
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"5e0c": true, "9f1a": true}, revoked)
	})
	t.Run("keeps the comments already in the file when it appends", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, os.MkdirAll(store.dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(store.dir, revokedFile), []byte("# keep me\n"), 0o600))
		require.NoError(t, store.revoke("5e0c", "env-1a2b3c4d"))
		data, err := os.ReadFile(filepath.Join(store.dir, revokedFile))
		require.NoError(t, err)
		assert.Contains(t, string(data), "# keep me\n5e0c env-1a2b3c4d ")
		assertMode(t, filepath.Join(store.dir, revokedFile), 0o600)
	})
}
