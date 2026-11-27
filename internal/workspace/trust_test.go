package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
)

// trustingStore is a test store whose Untrust calls are recorded.
func trustingStore(t *testing.T) (*Store, *claudeTrustMock) {
	t.Helper()
	s := newTestStore(t, nil, nil)
	m := &claudeTrustMock{UntrustFunc: func(string, bool) error { return nil }}
	s.trust = m
	return s, m
}

func TestStore_RecordTrust(t *testing.T) {
	t.Parallel()
	t.Run("writes a record removal reads back", func(t *testing.T) {
		t.Parallel()
		s, _ := trustingStore(t)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordTrust("x", "/cfg/workspaces/x/n", true))
		got, err := s.readTrust("x")
		require.NoError(t, err)
		assert.Equal(t, Trust{Version: formatVersion, Dir: "/cfg/workspaces/x/n", CreatedEntry: true}, got)
	})
	t.Run("an invalid id", func(t *testing.T) {
		t.Parallel()
		s, _ := trustingStore(t)
		assert.ErrorIs(t, s.RecordTrust("../x", "/d", true), ErrNotFound)
	})
}

func TestStore_Remove_trust(t *testing.T) {
	t.Parallel()
	t.Run("takes back the recorded trust", func(t *testing.T) {
		t.Parallel()
		s, m := trustingStore(t)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordTrust("x", "/cfg/workspaces/x/n", false))
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		require.Len(t, m.UntrustCalls(), 1)
		assert.Equal(t, "/cfg/workspaces/x/n", m.UntrustCalls()[0].Dir)
		assert.False(t, m.UntrustCalls()[0].Created)
		assert.NoDirExists(t, s.stateOf("x"))
	})
	t.Run("untrusts while the workspace's overlay still exists", func(t *testing.T) {
		t.Parallel()
		s, m := trustingStore(t)
		putWorkspace(t, s, "x", testNow())
		overlay := filepath.Join(s.stateOf("x"), "claude")
		writeFile(t, filepath.Join(overlay, claude.OverlayMarker), "/base\n")
		require.NoError(t, s.RecordTrust("x", "/cfg/workspaces/x/n", true))
		var overlayThere bool
		m.UntrustFunc = func(string, bool) error {
			_, err := os.Stat(filepath.Join(overlay, claude.OverlayMarker))
			overlayThere = err == nil
			return nil
		}
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		assert.True(t, overlayThere, "Pool.Untrust finds the overlay's config only while its marker exists")
		assert.NoDirExists(t, overlay)
	})
	t.Run("takes back nothing without a record", func(t *testing.T) {
		t.Parallel()
		s, m := trustingStore(t)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		assert.Empty(t, m.UntrustCalls())
	})
	t.Run("a failed untrust is a warning and the workspace still goes", func(t *testing.T) {
		t.Parallel()
		s, m := trustingStore(t)
		m.UntrustFunc = func(string, bool) error { return errors.New("locked") }
		ws := putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordTrust("x", "/cfg/workspaces/x/n", true))
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		assert.NoDirExists(t, ws.Path)
		assert.NoDirExists(t, s.stateOf("x"))
		require.Len(t, s.Warnings(), 1)
		assert.Contains(t, s.Warnings()[0], `remove projects["/cfg/workspaces/x/n"]`)
	})
	t.Run("a corrupt record is a warning and the workspace still goes", func(t *testing.T) {
		t.Parallel()
		s, m := trustingStore(t)
		putWorkspace(t, s, "x", testNow())
		writeFile(t, filepath.Join(s.stateOf("x"), "trust"), "dir = 3")
		require.NoError(t, s.Remove("x", RemoveOptions{}))
		assert.Empty(t, m.UntrustCalls())
		assert.NoDirExists(t, s.stateOf("x"))
		assert.Len(t, s.Warnings(), 1)
	})
	t.Run("a refused removal keeps the trust", func(t *testing.T) {
		t.Parallel()
		s, m := trustingStore(t)
		ws := sampleRecord(s.root, "x")
		ws.Path = t.TempDir()
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		require.NoError(t, writeRecord(s.recordPath("x"), &ws))
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		require.NoError(t, s.RecordTrust("x", "/cfg/workspaces/x/n", true))
		require.ErrorIs(t, s.Remove("x", RemoveOptions{}), ErrUnsafe)
		assert.Empty(t, m.UntrustCalls())
	})
}
