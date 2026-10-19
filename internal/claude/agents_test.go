package claude

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentsJSON is `claude agents --json --all` output from Claude Code
// 2.1.303: a running background session, a stopped one, and an
// interactive session, which has no short id or state.
const agentsJSON = `[
  {"pid":73215,"id":"f381de70","cwd":"/w/fix-login","kind":"background","startedAt":1792426739161,
   "sessionId":"f381de70-c465-4e01-afa5-8649daf6a784","name":"fix-login","status":"idle","state":"working"},
  {"id":"344c1927","cwd":"/w/docs","kind":"background","startedAt":1785011794506,
   "sessionId":"344c1927-25fd-44fc-921c-76ee780ed1d5","name":"docs","state":"stopped"},
  {"pid":99463,"cwd":"/w","kind":"interactive","startedAt":1792270137159,
   "sessionId":"b3bc89e5-0b48-46b1-bb19-1e302be4d741","name":"w","status":"busy"}
]`

func TestSession_HasProcess(t *testing.T) {
	t.Run("with a pid", func(t *testing.T) {
		assert.True(t, (&Session{PID: 12}).HasProcess())
	})
	t.Run("without one", func(t *testing.T) {
		assert.False(t, (&Session{}).HasProcess())
	})
}

func TestSession_Working(t *testing.T) {
	t.Run("busy with a process", func(t *testing.T) {
		assert.True(t, (&Session{PID: 12, Status: "busy"}).Working())
	})
	t.Run("idle with a process", func(t *testing.T) {
		assert.False(t, (&Session{PID: 12, Status: "idle"}).Working())
	})
	t.Run("idle with state working", func(t *testing.T) {
		assert.False(t, (&Session{PID: 12, Status: "idle", State: StateWorking}).Working())
	})
	t.Run("busy without a process", func(t *testing.T) {
		assert.False(t, (&Session{Status: "busy"}).Working())
	})
}

func TestSession_Starting(t *testing.T) {
	started := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	t.Run("no process, just started", func(t *testing.T) {
		assert.True(t, (&Session{StartedAt: started}).Starting(started.Add(3*time.Second)))
	})
	t.Run("no process, started long ago", func(t *testing.T) {
		assert.False(t, (&Session{StartedAt: started}).Starting(started.Add(time.Minute)))
	})
	t.Run("no process, at the window's end", func(t *testing.T) {
		assert.False(t, (&Session{StartedAt: started}).Starting(started.Add(startingWindow)))
	})
	t.Run("start time slightly ahead of the clock", func(t *testing.T) {
		assert.True(t, (&Session{StartedAt: started}).Starting(started.Add(-time.Second)))
	})
	t.Run("has a process", func(t *testing.T) {
		assert.False(t, (&Session{PID: 12, StartedAt: started}).Starting(started.Add(time.Second)))
	})
	t.Run("no start time", func(t *testing.T) {
		assert.False(t, (&Session{}).Starting(started))
	})
}

func TestClient_Agents(t *testing.T) {
	t.Parallel()
	t.Run("lists active sessions", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; echo '[]'"), environ)
		sessions, err := c.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		assert.Empty(t, sessions)
		assert.Equal(t, []string{"agents", "--json"}, args())
	})
	t.Run("all sessions under a directory", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; echo '[]'"), environ)
		_, err := c.Agents(t.Context(), ListOptions{All: true, Cwd: "/w/a b"})
		require.NoError(t, err)
		assert.Equal(t, []string{"agents", "--json", "--all", "--cwd", "/w/a b"}, args())
	})
	t.Run("parses the output", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "printf '%s' '"+agentsJSON+"'"), nil)
		sessions, err := c.Agents(t.Context(), ListOptions{All: true})
		require.NoError(t, err)
		require.Len(t, sessions, 2)
		assert.Equal(t, "f381de70", sessions[0].ID)
	})
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo 'daemon unreachable' >&2; exit 1"), nil)
		_, err := c.Agents(t.Context(), ListOptions{})
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, "daemon unreachable", cmdErr.Stderr)
	})
}

func TestParseAgents(t *testing.T) {
	t.Run("keeps background sessions", func(t *testing.T) {
		sessions, err := parseAgents([]byte(agentsJSON))
		require.NoError(t, err)
		require.Len(t, sessions, 2)
		assert.Equal(t, "f381de70", sessions[0].ID)
		assert.Equal(t, "344c1927", sessions[1].ID)
	})
	t.Run("empty list", func(t *testing.T) {
		sessions, err := parseAgents([]byte("[]\n"))
		require.NoError(t, err)
		assert.Empty(t, sessions)
	})
	t.Run("ignores unknown fields", func(t *testing.T) {
		sessions, err := parseAgents([]byte(`[{"id":"a1","kind":"background","color":"red"}]`))
		require.NoError(t, err)
		assert.Equal(t, []Session{{ID: "a1"}}, sessions)
	})
	t.Run("rejects a background session without an id", func(t *testing.T) {
		_, err := parseAgents([]byte(`[{"kind":"background","sessionId":"u"}]`))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects other output", func(t *testing.T) {
		_, err := parseAgents([]byte("No sessions"))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects an object", func(t *testing.T) {
		_, err := parseAgents([]byte(`{"sessions":[]}`))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
}

func TestAgentEntry_session(t *testing.T) {
	t.Run("copies every field", func(t *testing.T) {
		e := agentEntry{
			ID: "f381de70", SessionID: "uuid", Name: "fix-login", Cwd: "/w/fix-login", Kind: "background",
			State: "working", Status: "idle", PID: 73215, StartedAt: 1792426739161,
		}
		assert.Equal(t, Session{
			ID: "f381de70", SessionID: "uuid", Name: "fix-login", Cwd: "/w/fix-login",
			State: StateWorking, Status: "idle", PID: 73215,
			StartedAt: time.Date(2026, 10, 19, 16, 18, 59, 161_000_000, time.UTC),
		}, e.session())
	})
	t.Run("no start time", func(t *testing.T) {
		e := agentEntry{ID: "a1"}
		assert.True(t, e.session().StartedAt.IsZero())
	})
}
