package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAttach is a fakeClaude body for `claude attach`: it draws the prompt
// unless $NO_PROMPT is set, reads a line from its terminal, appends it to
// $TRANSCRIPT as a user entry unless $NO_RECORD is set, and waits to be
// hung up. Its arguments go to $ARGS and its terminal's name to $TTY.
const fakeAttach = `printf '%s\n' "$@" > "$ARGS"
tty > "$TTY"
[ -n "$NO_PROMPT" ] || printf '\342\235\257 '
IFS= read -r line
[ -n "$NO_RECORD" ] || printf '{"type":"user","timestamp":"%s","message":{"content":"%s"}}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$line" >> "$TRANSCRIPT"
sleep 30`

// fastTimings keep Send's waits short in tests.
var fastTimings = sendTimings{
	prompt: 2 * time.Second,
	settle: 10 * time.Millisecond,
	record: 2 * time.Second,
	poll:   20 * time.Millisecond,
	hangUp: 2 * time.Second,
}

// sendClient is a Client running fakeAttach with a temporary Claude config
// directory, the session it sends to, and that session's transcript.
func sendClient(t *testing.T, extra ...string) (c *Client, s *Session, transcript string, args func() []string) {
	t.Helper()
	cfg := t.TempDir()
	s = &Session{ID: "d12c2de1", SessionID: "d12c2de1-edad-4531-93c1-885ee54e3448", Cwd: "/w/x"}
	transcript = filepath.Join(cfg, "projects", "-w-x", s.SessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcript), 0o700))
	argsEnv, args := argsFile(t)
	env := slices.Concat(argsEnv, []string{"CLAUDE_CONFIG_DIR=" + cfg, "TRANSCRIPT=" + transcript, "TTY=" + filepath.Join(t.TempDir(), "tty")})
	c = New(discardLogger(), fakeClaude(t, fakeAttach), append(env, extra...))
	c.sendTimings = fastTimings
	return c, s, transcript, args
}

func TestClient_Send(t *testing.T) {
	t.Parallel()
	t.Run("types the message at the prompt and returns once it's recorded", func(t *testing.T) {
		t.Parallel()
		c, s, transcript, args := sendClient(t)
		c.sendTimings.prompt = time.Minute
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		require.NoError(t, c.Send(ctx, s, testMessage), "the prompt wasn't seen, or the message not recorded")
		assert.Equal(t, []string{"attach", "d12c2de1"}, args())
		data, err := os.ReadFile(transcript)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"content":"`+testMessage+`"`)
	})
	t.Run("runs claude attach on a terminal", func(t *testing.T) {
		t.Parallel()
		c, s, _, _ := sendClient(t)
		require.NoError(t, c.Send(t.Context(), s, testMessage))
		tty, err := os.ReadFile(lookup(c.environ, "TTY"))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(tty), "/dev/"), "tty printed %q", tty)
	})
	t.Run("types anyway when no prompt is drawn", func(t *testing.T) {
		t.Parallel()
		c, s, _, _ := sendClient(t, "NO_PROMPT=1")
		c.sendTimings.prompt = 100 * time.Millisecond
		require.NoError(t, c.Send(t.Context(), s, testMessage))
	})
	t.Run("a message that isn't recorded is ErrNotDelivered", func(t *testing.T) {
		t.Parallel()
		c, s, _, _ := sendClient(t, "NO_RECORD=1")
		c.sendTimings.record = 200 * time.Millisecond
		err := c.Send(t.Context(), s, testMessage)
		require.ErrorIs(t, err, ErrNotDelivered)
	})
	t.Run("an earlier copy of the message doesn't count", func(t *testing.T) {
		t.Parallel()
		c, s, transcript, _ := sendClient(t, "NO_RECORD=1")
		c.sendTimings.record = 200 * time.Millisecond
		writeTranscript(t, transcript, userLine("2026-10-28T23:31:00Z", testMessage))
		require.ErrorIs(t, c.Send(t.Context(), s, testMessage), ErrNotDelivered)
	})
	t.Run("stops when ctx ends", func(t *testing.T) {
		t.Parallel()
		c, s, _, _ := sendClient(t, "NO_PROMPT=1")
		c.sendTimings.prompt = time.Minute
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		require.ErrorIs(t, c.Send(ctx, s, testMessage), context.DeadlineExceeded)
	})
	t.Run("refuses an id that could be an option", func(t *testing.T) {
		t.Parallel()
		c, s, _, _ := sendClient(t)
		s.ID = "--all"
		require.ErrorIs(t, c.Send(t.Context(), s, testMessage), errBadID)
	})
	t.Run("claude missing is ErrNotInstalled", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), filepath.Join(t.TempDir(), "claude"), nil)
		err := c.Send(t.Context(), &Session{ID: "d12c2de1"}, testMessage)
		require.ErrorIs(t, err, ErrNotInstalled)
	})
}
