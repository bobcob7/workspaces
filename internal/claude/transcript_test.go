package claude

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMessage = "carry on"

// limitLine is a usage-limit entry, as Claude Code 2.1.312 writes one, that
// resets at resetsAt (Unix seconds).
func limitLine(resetsAt int64) string {
	return `{"type":"assistant","isSidechain":false,"timestamp":"2026-10-28T23:28:45.723Z","message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"You've hit your 5-hour limit · resets 6:30pm (America/Chicago)"}]},"apiError":"usage_limit_reached","apiErrorParams":{"rate_limit_info":{"status":"rejected","resetsAt":` +
		strconv.FormatInt(resetsAt, 10) + `,"rateLimitType":"five_hour"}},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429}`
}

func userLine(at, text string) string {
	return `{"type":"user","timestamp":"` + at + `","message":{"role":"user","content":"` + text + `"}}`
}

const (
	assistantLine = `{"type":"assistant","timestamp":"2026-10-28T20:00:00Z","message":{"content":[{"type":"text","text":"done"}]}}`
	sidechainLine = `{"type":"assistant","isSidechain":true,"timestamp":"2026-10-28T23:30:00Z","message":{"content":[{"type":"text","text":"sub"}]}}`
	metaLine      = `{"type":"user","isMeta":true,"timestamp":"2026-10-28T23:30:00Z","message":{"content":"caveat"}}`
	systemLine    = `{"type":"system","timestamp":"2026-10-28T23:30:00Z"}`
)

func parse(t *testing.T, lines ...string) []transcriptEntry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	entries, err := readTail(path)
	require.NoError(t, err)
	return entries
}

func TestLastLimit(t *testing.T) {
	t.Parallel()
	resets := time.Date(2026, 10, 28, 23, 30, 0, 0, time.UTC)
	t.Run("a turn that ended at a limit", func(t *testing.T) {
		t.Parallel()
		got := lastLimit(parse(t, userLine("2026-10-28T20:00:00Z", "go"), limitLine(resets.Unix())), testMessage)
		assert.Equal(t, &Limit{Type: "five_hour", ResetsAt: resets}, got)
	})
	t.Run("skips sidechain, meta, and non-message lines after the limit", func(t *testing.T) {
		t.Parallel()
		got := lastLimit(parse(t, limitLine(resets.Unix()), sidechainLine, metaLine, systemLine, "not json"), testMessage)
		assert.Equal(t, &Limit{Type: "five_hour", ResetsAt: resets}, got)
	})
	t.Run("nil when the session did something after the limit", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, lastLimit(parse(t, limitLine(resets.Unix()), userLine("2026-10-28T23:31:00Z", "continue")), testMessage))
		assert.Nil(t, lastLimit(parse(t, limitLine(resets.Unix()), userLine("2026-10-28T23:31:00Z", testMessage)), testMessage))
		assert.Nil(t, lastLimit(parse(t, assistantLine), testMessage))
	})
	t.Run("nil for an empty transcript", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, lastLimit(nil, testMessage))
	})
	t.Run("counts the tries since the session last did anything else", func(t *testing.T) {
		t.Parallel()
		got := lastLimit(parse(t,
			userLine("2026-10-28T19:00:00Z", testMessage),
			assistantLine,
			limitLine(resets.Unix()-3600),
			userLine("2026-10-28T23:31:00Z", testMessage),
			limitLine(resets.Unix()),
			userLine("2026-10-28T23:40:00Z", testMessage),
			limitLine(resets.Unix()),
		), testMessage)
		require.NotNil(t, got)
		assert.Equal(t, 2, got.Tries)
		assert.Equal(t, time.Date(2026, 10, 28, 23, 40, 0, 0, time.UTC), got.LastTry)
		assert.Equal(t, resets, got.ResetsAt)
	})
	t.Run("a person's reply ends the count", func(t *testing.T) {
		t.Parallel()
		got := lastLimit(parse(t,
			userLine("2026-10-28T23:31:00Z", testMessage),
			limitLine(resets.Unix()),
			userLine("2026-10-28T23:35:00Z", "continue"),
			limitLine(resets.Unix()),
		), testMessage)
		require.NotNil(t, got)
		assert.Zero(t, got.Tries)
	})
	t.Run("an entry without resetsAt isn't a limit", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, lastLimit(parse(t, strings.Replace(limitLine(resets.Unix()), `"resetsAt":`+strconv.FormatInt(resets.Unix(), 10)+`,`, "", 1)), testMessage))
	})
}

func TestTranscriptEntry_text(t *testing.T) {
	t.Parallel()
	got := parse(t,
		userLine("2026-10-28T23:31:00Z", "  plain  "),
		`{"type":"user","message":{"content":[{"type":"text","text":"a"},{"type":"tool_result","content":"x"},{"type":"text","text":"b"}]}}`,
		`{"type":"user","message":{"content":42}}`,
	)
	require.Len(t, got, 3)
	assert.Equal(t, "plain", got[0].text())
	assert.Equal(t, "ab", got[1].text())
	assert.Empty(t, got[2].text())
}

func TestReadTail(t *testing.T) {
	t.Parallel()
	t.Run("drops the partial first line of a long transcript", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "t.jsonl")
		long := userLine("2026-10-28T19:00:00Z", strings.Repeat("x", transcriptTail))
		require.NoError(t, os.WriteFile(path, []byte(long+"\n"+assistantLine+"\n"), 0o600))
		entries, err := readTail(path)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "done", entries[0].text())
	})
	t.Run("a missing file is an error", func(t *testing.T) {
		t.Parallel()
		_, err := readTail(filepath.Join(t.TempDir(), "missing.jsonl"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

// transcriptClient is a Client whose Claude config directory is a new
// temporary one, and the session whose transcript it reads.
func transcriptClient(t *testing.T) (*Client, *Session, string) {
	t.Helper()
	cfg := t.TempDir()
	c := New(discardLogger(), "claude", []string{"CLAUDE_CONFIG_DIR=" + cfg})
	s := &Session{ID: "d12c2de1", SessionID: "d12c2de1-edad-4531-93c1-885ee54e3448", Cwd: "/Users/me/Application Support/w.x"}
	return c, s, filepath.Join(cfg, "projects", "-Users-me-Application-Support-w-x", s.SessionID+".jsonl")
}

func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
}

func TestClient_UsageLimit(t *testing.T) {
	t.Parallel()
	resets := time.Date(2026, 10, 28, 23, 30, 0, 0, time.UTC)
	t.Run("reads the transcript for the session's directory", func(t *testing.T) {
		t.Parallel()
		c, s, path := transcriptClient(t)
		writeTranscript(t, path, limitLine(resets.Unix()))
		got, err := c.UsageLimit(s, testMessage)
		require.NoError(t, err)
		assert.Equal(t, &Limit{Type: "five_hour", ResetsAt: resets}, got)
	})
	t.Run("finds the session's transcript in another project directory", func(t *testing.T) {
		t.Parallel()
		c, s, path := transcriptClient(t)
		writeTranscript(t, filepath.Join(filepath.Dir(filepath.Dir(path)), "-Users-me-shortened-1a2b", s.SessionID+".jsonl"), limitLine(resets.Unix()))
		got, err := c.UsageLimit(s, testMessage)
		require.NoError(t, err)
		require.NotNil(t, got)
	})
	t.Run("no transcript is no limit", func(t *testing.T) {
		t.Parallel()
		c, s, _ := transcriptClient(t)
		got, err := c.UsageLimit(s, testMessage)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("an unreadable transcript is an error", func(t *testing.T) {
		t.Parallel()
		c, s, path := transcriptClient(t)
		require.NoError(t, os.MkdirAll(path, 0o700))
		_, err := c.UsageLimit(s, testMessage)
		require.Error(t, err)
	})
	t.Run("refuses a session id that isn't a file name", func(t *testing.T) {
		t.Parallel()
		c, s, _ := transcriptClient(t)
		s.SessionID = "../x"
		_, err := c.UsageLimit(s, testMessage)
		require.ErrorIs(t, err, errBadID)
	})
	t.Run("needs CLAUDE_CONFIG_DIR or HOME", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), "claude", nil)
		_, err := c.UsageLimit(&Session{SessionID: "x"}, testMessage)
		require.ErrorIs(t, err, errNoHome)
	})
}

func TestClient_Recorded(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 28, 23, 31, 0, 0, time.UTC)
	t.Run("a user entry with the message, at or after since", func(t *testing.T) {
		t.Parallel()
		c, s, path := transcriptClient(t)
		writeTranscript(t, path, limitLine(since.Unix()), userLine("2026-10-28T23:31:00Z", testMessage))
		ok, err := c.Recorded(s, testMessage, since)
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("not an earlier copy or another message", func(t *testing.T) {
		t.Parallel()
		c, s, path := transcriptClient(t)
		writeTranscript(t, path, userLine("2026-10-28T23:30:59Z", testMessage), userLine("2026-10-28T23:32:00Z", "other"))
		ok, err := c.Recorded(s, testMessage, since)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("not without a transcript", func(t *testing.T) {
		t.Parallel()
		c, s, _ := transcriptClient(t)
		ok, err := c.Recorded(s, testMessage, since)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestProjectDirName(t *testing.T) {
	assert.Equal(t, "-Users-me-Library-Application-Support-ws-a-b", projectDirName("/Users/me/Library/Application Support/ws/a.b"))
	assert.Equal(t, "-tmp-caf--x", projectDirName("/tmp/café_x"))
}

func TestProjectsDir(t *testing.T) {
	assert.Equal(t, "/c/projects", projectsDir([]string{"HOME=/h", "CLAUDE_CONFIG_DIR=/c"}))
	assert.Equal(t, "/h/.claude/projects", projectsDir([]string{"HOME=/h", "CLAUDE_CONFIG_DIR="}))
	assert.Empty(t, projectsDir(nil))
}
