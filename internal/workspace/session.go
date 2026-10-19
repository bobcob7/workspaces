package workspace

import (
	"fmt"
	"path/filepath"
	"time"
)

// SessionKind is what kind of session a workspace has (ADR 0015).
type SessionKind string

const (
	// SessionShell is an interactive shell run by ws shell.
	SessionShell SessionKind = "shell"
	// SessionClaude is a Claude Code background session started by ws
	// claude.
	SessionClaude SessionKind = "claude"
)

// Session is a workspace's state/<id>/session record (ADR 0015).
type Session struct {
	Kind SessionKind `toml:"kind"`
	// ClaudeSessionID is the Claude Code session's id; empty for a shell.
	ClaudeSessionID string    `toml:"claude_session_id"`
	StartedAt       time.Time `toml:"started_at"`
}

// RecordSession writes workspace id's session record, started now,
// through a temp file and a rename.
func (s *Store) RecordSession(id string, kind SessionKind, claudeSessionID string) error {
	if !validID(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	sess := Session{Kind: kind, ClaudeSessionID: claudeSessionID, StartedAt: s.now().UTC().Truncate(time.Second)}
	if err := writeTOML(s.sessionPath(id), &sess); err != nil {
		return fmt.Errorf("writing session record for %s: %w", id, err)
	}
	return nil
}

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.stateOf(id), "session")
}
