package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaudeEnv makes this test binary act as claude (fakeClaudeMain).
const fakeClaudeEnv = "WS_TEST_FAKE_CLAUDE"

// TestMain runs the tests, or, when fakeClaudeEnv is set, acts as claude:
// Windows can't run the #!/bin/sh fakes the other tests use.
func TestMain(m *testing.M) {
	if os.Getenv(fakeClaudeEnv) == "1" {
		os.Exit(fakeClaudeMain(os.Stdin, os.Stdout, os.Args[1:])) //nolint:forbidigo // A test binary run as claude exits with the fake's code.
	}
	os.Exit(m.Run()) //nolint:forbidigo // TestMain exits with the tests' code.
}

// fakeClaudeMain is fakeAttach for Windows: --version prints a supported
// version; attach records its arguments to $ARGS, draws the prompt unless
// $NO_PROMPT is set, reads a line, appends it to $TRANSCRIPT as a user
// entry, and waits to be closed.
func fakeClaudeMain(stdin io.Reader, stdout io.Writer, args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintln(stdout, "2.1.303 (Claude Code)")
		return 0
	}
	if err := os.WriteFile(os.Getenv("ARGS"), []byte(strings.Join(args, "\n")+"\n"), 0o600); err != nil {
		return 2
	}
	if os.Getenv("NO_PROMPT") == "" {
		_, _ = fmt.Fprint(stdout, promptGlyph+" ")
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil {
		return 3
	}
	entry, err := json.Marshal(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339), "message": map[string]string{"content": strings.TrimRight(line, "\r\n")}})
	if err != nil {
		return 4
	}
	f, err := os.OpenFile(os.Getenv("TRANSCRIPT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 5
	}
	_, _ = f.Write(append(entry, '\n'))
	_ = f.Close()
	time.Sleep(30 * time.Second)
	return 0
}

// windowsSendClient is a Client whose claude is this test binary acting as
// fakeClaudeMain, the session it sends to, and its recorded arguments.
func windowsSendClient(t *testing.T, extra ...string) (c *Client, s *Session, args func() []string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cfg := t.TempDir()
	s = &Session{ID: "d12c2de1", SessionID: "d12c2de1-edad-4531-93c1-885ee54e3448", Cwd: `C:\w\x`}
	transcript := filepath.Join(cfg, "projects", "C--w-x", s.SessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcript), 0o700))
	argsEnv, args := argsFile(t)
	env := append(os.Environ(), argsEnv...)
	env = append(env, fakeClaudeEnv+"=1", "CLAUDE_CONFIG_DIR="+cfg, "TRANSCRIPT="+transcript)
	c = New(discardLogger(), exe, append(env, extra...))
	c.sendTimings = fastTimings
	return c, s, args
}

func TestClient_Send_windows(t *testing.T) {
	t.Run("types the message at the prompt and returns once it's recorded", func(t *testing.T) {
		c, s, args := windowsSendClient(t)
		c.sendTimings.prompt = time.Minute
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		started := time.Now()
		require.NoError(t, c.Send(ctx, s, testMessage))
		assert.Equal(t, []string{"attach", "d12c2de1"}, args())
		assert.Less(t, time.Since(started), 25*time.Second, "hang-up didn't end the fake's 30 s sleep")
	})
	t.Run("a message that isn't recorded is ErrNotDelivered", func(t *testing.T) {
		c, s, _ := windowsSendClient(t, "TRANSCRIPT="+filepath.Join(t.TempDir(), "elsewhere.jsonl"))
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		require.ErrorIs(t, c.Send(ctx, s, testMessage), ErrNotDelivered)
	})
	t.Run("a missing claude is ErrNotInstalled", func(t *testing.T) {
		c := New(discardLogger(), filepath.Join(t.TempDir(), "claude.exe"), os.Environ())
		c.checked = true
		s := &Session{ID: "d12c2de1"}
		require.ErrorIs(t, c.Send(t.Context(), s, testMessage), ErrNotInstalled)
	})
}
