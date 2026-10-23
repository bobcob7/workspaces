package workspace

import (
	"os"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lockfile"
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
		assert.Equal(t, Session{Version: 1, Kind: SessionShell, StartedAt: testNow()}, got)
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

func TestStore_Session(t *testing.T) {
	t.Parallel()
	t.Run("reads what RecordSession wrote", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordSession("x", SessionClaude, "e8cdf5d8"))
		got, err := s.Session("x")
		require.NoError(t, err)
		assert.Equal(t, Session{Version: 1, Kind: SessionClaude, ClaudeSessionID: "e8cdf5d8", StartedAt: testNow()}, got)
	})
	t.Run("no record", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		_, err := s.Session("x")
		assert.ErrorIs(t, err, ErrNoSession)
	})
	t.Run("a record without a version is version 1", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "kind = \"shell\"\n")
		got, err := s.Session("x")
		require.NoError(t, err)
		assert.Equal(t, Session{Version: 1, Kind: SessionShell}, got)
	})
	t.Run("a newer version", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "version = 2\nkind = \"shell\"\nhost = \"h\"\n")
		_, err := s.Session("x")
		assert.ErrorContains(t, err, "written by a newer ws (version 2; this ws reads up to 1); upgrade ws")
	})
	t.Run("a negative version", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "version = -1\nkind = \"shell\"\n")
		_, err := s.Session("x")
		assert.ErrorContains(t, err, "unsupported version -1")
	})
	t.Run("an invalid id", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).Session("..")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("unparsable", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "kind = ")
		_, err := s.Session("x")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNoSession)
	})
	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "kind = \"shell\"\npid = 3\n")
		_, err := s.Session("x")
		assert.ErrorContains(t, err, "unknown key pid")
	})
	t.Run("unknown kind", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "kind = \"vm\"\n")
		_, err := s.Session("x")
		assert.ErrorContains(t, err, `unknown session kind "vm"`)
	})
	t.Run("an agent without its session id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.sessionPath("x"), "kind = \"claude\"\nclaude_session_id = \"\"\n")
		_, err := s.Session("x")
		assert.ErrorContains(t, err, "without a claude_session_id")
	})
	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.sessionPath("x"), 0o750))
		_, err := s.Session("x")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNoSession)
	})
}

func TestStore_Locked(t *testing.T) {
	t.Parallel()
	t.Run("held", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		unlock, ok, err := lockfile.TryExclusive(s.lockPath("x"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		locked, err := s.Locked("x")
		require.NoError(t, err)
		assert.True(t, locked)
	})
	t.Run("free", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.lockPath("x"), "")
		locked, err := s.Locked("x")
		require.NoError(t, err)
		assert.False(t, locked)
	})
	t.Run("no lock file, without creating one", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		locked, err := s.Locked("x")
		require.NoError(t, err)
		assert.False(t, locked)
		assert.NoDirExists(t, s.stateOf("x"))
	})
	t.Run("an invalid id", func(t *testing.T) {
		t.Parallel()
		_, err := newTestStore(t, nil, nil).Locked("a/b")
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("lock can't be opened", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.lockPath("x"), 0o750))
		_, err := s.Locked("x")
		assert.Error(t, err)
	})
}

func TestStore_Abandoned(t *testing.T) {
	t.Parallel()
	t.Run("no record, unlocked, and old", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		age(t, s, "x", time.Hour)
		abandoned, err := s.Abandoned("x")
		require.NoError(t, err)
		assert.True(t, abandoned)
	})
	t.Run("no record but too new", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		age(t, s, "x", time.Second)
		abandoned, err := s.Abandoned("x")
		require.NoError(t, err)
		assert.False(t, abandoned)
	})
	t.Run("no record but locked", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		unlock, ok, err := lockfile.TryExclusive(s.lockPath("x"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		age(t, s, "x", time.Hour)
		abandoned, err := s.Abandoned("x")
		require.NoError(t, err)
		assert.False(t, abandoned)
	})
	t.Run("no record but the lock can't be checked", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, os.MkdirAll(s.lockPath("x"), 0o750))
		age(t, s, "x", time.Hour)
		abandoned, err := s.Abandoned("x")
		require.NoError(t, err)
		assert.False(t, abandoned)
	})
	t.Run("recorded", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		require.NoError(t, s.RecordSession("x", SessionShell, ""))
		age(t, s, "x", time.Hour)
		abandoned, err := s.Abandoned("x")
		require.NoError(t, err)
		assert.False(t, abandoned)
	})
	t.Run("unreadable record", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		putWorkspace(t, s, "x", testNow())
		writeFile(t, s.sessionPath("x"), "kind = ")
		age(t, s, "x", time.Hour)
		_, err := s.Abandoned("x")
		assert.Error(t, err)
	})
}

func TestSession_check(t *testing.T) {
	t.Run("shell", func(t *testing.T) {
		assert.NoError(t, (&Session{Kind: SessionShell}).check())
	})
	t.Run("agent with its id", func(t *testing.T) {
		assert.NoError(t, (&Session{Kind: SessionClaude, ClaudeSessionID: "e8cdf5d8"}).check())
	})
	t.Run("agent without its id", func(t *testing.T) {
		assert.Error(t, (&Session{Kind: SessionClaude}).check())
	})
	t.Run("unknown kind", func(t *testing.T) {
		assert.Error(t, (&Session{Kind: "vm"}).check())
	})
}

func TestStore_sessionPath(t *testing.T) {
	s := New(discardLogger(), nil, nil, nil, "/cfg/workspaces", "/cfg/state")
	assert.Equal(t, "/cfg/state/fix-login/session", s.sessionPath("fix-login"))
}
