package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/lockfile"
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

// Session is a workspace's state/<id>/session record (ADR 0027).
type Session struct {
	// Version is the record's format version; a record written without
	// one is version 1.
	Version int         `toml:"version"`
	Kind    SessionKind `toml:"kind"`
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
	sess := Session{Version: formatVersion, Kind: kind, ClaudeSessionID: claudeSessionID, StartedAt: s.now().UTC().Truncate(time.Second)}
	if err := writeTOML(s.sessionPath(id), &sess); err != nil {
		return fmt.Errorf("writing session record for %s: %w", id, err)
	}
	return nil
}

// Session reads workspace id's session record. A workspace without one
// fails with ErrNoSession.
func (s *Store) Session(id string) (Session, error) {
	if !validID(id) {
		return Session{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	path := s.sessionPath(id)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Session{}, fmt.Errorf("%w: %s", ErrNoSession, id)
	}
	if err != nil {
		return Session{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var sess Session
	meta, err := toml.Decode(string(data), &sess)
	if err != nil {
		return Session{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if sess.Version == 0 {
		sess.Version = formatVersion
	}
	if err := checkVersion(sess.Version); err != nil {
		return Session{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Session{}, fmt.Errorf("parsing %s: unknown key %s", path, undecoded[0])
	}
	if err := sess.check(); err != nil {
		return Session{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return sess, nil
}

// check rejects a record ws couldn't have written: an unknown kind, or an
// agent without its Claude session id.
func (sess *Session) check() error {
	switch sess.Kind {
	case SessionShell:
		return nil
	case SessionClaude:
		if sess.ClaudeSessionID == "" {
			return errors.New("claude session without a claude_session_id")
		}
		return nil
	}
	return fmt.Errorf("unknown session kind %q", sess.Kind)
}

// Locked reports whether workspace id's lock is held: by its shell, or by
// a creation or hand-off still in progress (ADRs 0023, 0025). It never
// creates the lock file or the state directory.
func (s *Store) Locked(id string) (bool, error) {
	if !validID(id) {
		return false, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return lockfile.HeldExclusively(s.lockPath(id))
}

// Abandoned reports whether workspace id was left without a session: it
// has no session record, its lock is free, and its state hasn't changed
// for a minute, so no creation is still between moving it into place and
// recording its session (ADR 0023).
func (s *Store) Abandoned(id string) (bool, error) {
	_, err := s.Session(id)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, ErrNoSession) {
		return false, err
	}
	return s.abandoned(id), nil
}

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.stateOf(id), "session")
}
