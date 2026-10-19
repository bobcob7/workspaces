package workspace

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

func TestStore_UsesCommit(t *testing.T) {
	t.Parallel()
	t.Run("a recorded commit is used", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "a", testNow())
		used, err := s.UsesCommit("example.test/o/n", "3f2a9c1e4b")
		require.NoError(t, err)
		assert.True(t, used)
	})
	t.Run("another repo's commit isn't", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "a", testNow())
		used, err := s.UsesCommit("example.test/o/other", "3f2a9c1e4b")
		require.NoError(t, err)
		assert.False(t, used)
	})
	t.Run("no workspaces", func(t *testing.T) {
		t.Parallel()
		used, err := newTestStore(t, nil, nil).UsesCommit("example.test/o/n", "abc")
		require.NoError(t, err)
		assert.False(t, used)
	})
	t.Run("a young creation in progress counts as using it", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("new"), 0o750))
		age(t, s, "new", time.Second)
		used, err := s.UsesCommit("example.test/o/n", "abc")
		require.NoError(t, err)
		assert.True(t, used)
	})
	t.Run("a locked creation in progress counts as using it", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("slow"), 0o750))
		unlock, ok, err := lockfile.TryExclusive(s.lockPath("slow"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		age(t, s, "slow", time.Hour)
		used, err := s.UsesCommit("example.test/o/n", "abc")
		require.NoError(t, err)
		assert.True(t, used)
	})
	t.Run("an abandoned creation doesn't", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("dead"), 0o750))
		age(t, s, "dead", time.Hour)
		used, err := s.UsesCommit("example.test/o/n", "abc")
		require.NoError(t, err)
		assert.False(t, used)
	})
	t.Run("an unreadable record is an error", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.recordPath("bad"), "version = 99\n")
		_, err := s.UsesCommit("example.test/o/n", "abc")
		assert.ErrorContains(t, err, "couldn't be read")
	})
	t.Run("an unreadable state directory is an error", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.WriteFile(s.stateDir, nil, 0o600))
		_, err := s.UsesCommit("example.test/o/n", "abc")
		assert.Error(t, err)
	})
}
