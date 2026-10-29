package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// usageLimitError is the apiError of the entry that ends a turn at a usage
// limit (ADR 0047).
const usageLimitError = "usage_limit_reached"

// transcriptTail is how much of the end of a transcript is read: enough
// for a limit entry and a few tries after it, without reading a long
// conversation whole.
const transcriptTail = 1 << 20

// Limit is a usage limit a session's turn ended at, read from its
// transcript (ADR 0047).
type Limit struct {
	// Type is Claude's rateLimitType, such as "five_hour" or "seven_day".
	// Other values may appear.
	Type     string
	ResetsAt time.Time
	// Tries is how many times the continue message was sent since the
	// session last did anything other than hit a limit.
	Tries int
	// LastTry is when the latest of those was sent; zero with no tries.
	LastTry time.Time
}

// transcriptEntry is the part of a transcript line ws reads.
//
//nolint:tagliatelle // Claude Code's field names, not ws output (ADR 0016).
type transcriptEntry struct {
	Type          string    `json:"type"`
	IsSidechain   bool      `json:"isSidechain"`
	IsMeta        bool      `json:"isMeta"`
	Timestamp     time.Time `json:"timestamp"`
	APIError      string    `json:"apiError"`
	APIErrorParam struct {
		RateLimitInfo struct {
			ResetsAt      int64  `json:"resetsAt"`
			RateLimitType string `json:"rateLimitType"`
		} `json:"rate_limit_info"`
	} `json:"apiErrorParams"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// UsageLimit reads the end of s's transcript and returns the usage limit
// its turn ended at, or nil when its last message isn't a usage-limit
// entry. Subagent entries, meta entries, and lines that aren't messages
// are skipped. Walking back from the limit over further limit entries and
// user messages that are exactly message counts the tries. A missing
// transcript is nil, not an error.
func (c *Client) UsageLimit(s *Session, message string) (*Limit, error) {
	path, err := c.transcriptPath(s)
	if err != nil || path == "" {
		return nil, err
	}
	entries, err := readTail(path)
	if err != nil {
		return nil, fmt.Errorf("reading the transcript of Claude Code session %s: %w", s.ID, err)
	}
	return lastLimit(entries, message), nil
}

// lastLimit is UsageLimit on entries, oldest first.
func lastLimit(entries []transcriptEntry, message string) *Limit {
	var limit *Limit
	for i := len(entries) - 1; i >= 0; i-- {
		e := &entries[i]
		switch {
		case !e.conversational():
			continue
		case e.isLimit() && limit == nil:
			limit = &Limit{Type: e.APIErrorParam.RateLimitInfo.RateLimitType, ResetsAt: time.Unix(e.APIErrorParam.RateLimitInfo.ResetsAt, 0).UTC()}
		case limit == nil:
			return nil
		case e.isLimit():
		case e.Type == "user" && e.text() == message:
			limit.Tries++
			if e.Timestamp.After(limit.LastTry) {
				limit.LastTry = e.Timestamp
			}
		default:
			return limit
		}
	}
	return limit
}

// Recorded reports whether s's transcript has a user message that is
// exactly message, written at or after since.
func (c *Client) Recorded(s *Session, message string, since time.Time) (bool, error) {
	path, err := c.transcriptPath(s)
	if err != nil || path == "" {
		return false, err
	}
	entries, err := readTail(path)
	if err != nil {
		return false, fmt.Errorf("reading the transcript of Claude Code session %s: %w", s.ID, err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := &entries[i]
		if e.conversational() && e.Type == "user" && e.text() == message && !e.Timestamp.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

// conversational reports whether e is a message of the session's own
// conversation: a user or assistant entry outside subagents, not meta.
func (e *transcriptEntry) conversational() bool {
	return (e.Type == "user" || e.Type == "assistant") && !e.IsSidechain && !e.IsMeta
}

func (e *transcriptEntry) isLimit() bool {
	return e.Type == "assistant" && e.APIError == usageLimitError && e.APIErrorParam.RateLimitInfo.ResetsAt > 0
}

// text is the entry's message text: the content when it is a string, else
// its text blocks joined, trimmed.
func (e *transcriptEntry) text() string {
	var s string
	if json.Unmarshal(e.Message.Content, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// readTail parses the whole lines in the last transcriptTail bytes of
// path. Lines that don't parse are skipped: the format is undocumented.
func readTail(path string) ([]transcriptEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := max(info.Size()-transcriptTail, 0)
	data := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(data, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if offset > 0 {
		lines = lines[1:]
	}
	entries := make([]transcriptEntry, 0, len(lines))
	for _, line := range lines {
		var e transcriptEntry
		if line != "" && json.Unmarshal([]byte(line), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// transcriptPath is where Claude Code writes s's transcript:
// projects/<cwd with every character but ASCII letters and digits replaced
// by "-">/<session uuid>.jsonl under its config directory, as the desktop
// app finds it. When that file doesn't exist, the session's file is looked
// for in every project directory, since Claude Code shortens long
// directory names. "" when there is none.
func (c *Client) transcriptPath(s *Session) (string, error) {
	if c.projectsDir == "" {
		return "", errNoHome
	}
	if !validID(s.SessionID) {
		return "", fmt.Errorf("%w: %q", errBadID, s.SessionID)
	}
	name := s.SessionID + ".jsonl"
	path := filepath.Join(c.projectsDir, projectDirName(s.Cwd), name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	matches, err := filepath.Glob(filepath.Join(c.projectsDir, "*", name))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	return matches[0], nil
}

func projectDirName(cwd string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, cwd)
}

// projectsDir is where Claude Code keeps transcripts: $CLAUDE_CONFIG_DIR
// /projects when CLAUDE_CONFIG_DIR is set and not empty, else
// $HOME/.claude/projects, or "" when neither is set.
func projectsDir(environ []string) string {
	if dir := lookup(environ, "CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	if home := lookup(environ, "HOME"); home != "" {
		return filepath.Join(home, ".claude", "projects")
	}
	return ""
}
