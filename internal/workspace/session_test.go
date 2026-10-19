package workspace

import (
	"os"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_RecordSession(t *testing.T) {
	t.Parallel()
	t.Run("writes kind and start time", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordSession("x", SessionShell, ""))
		var got Session
		_, err := toml.DecodeFile(s.sessionPath("x"), &got)
		require.NoError(t, err)
		assert.Equal(t, Session{Kind: SessionShell, StartedAt: testNow()}, got)
	})
	t.Run("keeps a Claude session id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordSession("x", SessionClaude, "e8cdf5d8"))
		data, err := os.ReadFile(s.sessionPath("x"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `claude_session_id = "e8cdf5d8"`)
	})
	t.Run("an invalid id", func(t *testing.T) {
		t.Parallel()
		assert.ErrorIs(t, newTestStore(t, nil, nil).RecordSession("../x", SessionShell, ""), ErrNotFound)
	})
	t.Run("no state directory", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, newTestStore(t, nil, nil).RecordSession("x", SessionShell, ""))
	})
}

func TestStore_sessionPath(t *testing.T) {
	s := New(discardLogger(), nil, nil, "/cfg/workspaces", "/cfg/state")
	assert.Equal(t, "/cfg/state/fix-login/session", s.sessionPath("fix-login"))
}
