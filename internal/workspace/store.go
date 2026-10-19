// Package workspace creates, finds, and removes workspaces: directories of
// repos cloned from the cache, with ws's state about each kept outside it
// (ADRs 0009 and 0012).
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/backup"
)

// Store keeps workspaces under CONFIG_DIR/workspaces and their state under
// CONFIG_DIR/state.
type Store struct {
	log      *slog.Logger
	git      gitRunner
	cache    repoCache
	root     string
	stateDir string
	exclude  func(dir string)
	now      func() time.Time
	suffix   func() (string, error)
	mu       sync.Mutex
	warnings []string
}

// New returns the Store for workspaces under root and their state under
// stateDir, normally CONFIG_DIR/workspaces and CONFIG_DIR/state, cloning
// workspace repos from c.
func New(log *slog.Logger, git gitRunner, c repoCache, root, stateDir string) *Store {
	return &Store{
		log:      log,
		git:      git,
		cache:    c,
		root:     root,
		stateDir: stateDir,
		exclude:  func(dir string) { backup.Exclude(log, dir) },
		now:      time.Now,
		suffix:   randomSuffix,
	}
}

// Warnings are problems that didn't stop an operation, for the command's
// output (ADR 0016).
func (s *Store) Warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.warnings)
}

func (s *Store) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log.Warn("workspace warning", slog.String("warning", msg))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = append(s.warnings, msg)
}

// dir is where workspace id lives.
func (s *Store) dir(id string) string {
	return filepath.Join(s.root, id)
}

// stateOf is workspace id's state directory.
func (s *Store) stateOf(id string) string {
	return filepath.Join(s.stateDir, id)
}

func (s *Store) recordPath(id string) string {
	return filepath.Join(s.stateOf(id), "workspace.toml")
}

func (s *Store) lockPath(id string) string {
	return filepath.Join(s.stateOf(id), "lock")
}

// ensureRoots creates workspaces/ and state/ if they're missing. A new
// workspaces/ is excluded from Time Machine, in the background (ADR 0011).
func (s *Store) ensureRoots() error {
	if err := os.MkdirAll(s.stateDir, 0o750); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	if _, err := os.Stat(s.root); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking workspace directory: %w", err)
	}
	if err := os.MkdirAll(s.root, 0o750); err != nil {
		return fmt.Errorf("creating workspace directory: %w", err)
	}
	s.exclude(s.root)
	return nil
}
