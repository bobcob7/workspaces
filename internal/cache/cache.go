// Package cache keeps one bare clone of each upstream repo and resolves
// repo specs and refs against it (ADRs 0007 and 0008).
package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/repo"
)

// Cache is the repo cache under CONFIG_DIR/cache.
type Cache struct {
	log       *slog.Logger
	git       gitRunner
	root      string
	defaults  config.Defaults
	exclude   func(ctx context.Context, dir string) error
	now       func() time.Time
	cleanOnce sync.Once
	mu        sync.Mutex
	warnings  []string
}

// New returns the cache rooted at root, normally CONFIG_DIR/cache. defaults
// drive short-name resolution.
func New(log *slog.Logger, git gitRunner, root string, defaults config.Defaults) *Cache {
	return &Cache{log: log, git: git, root: root, defaults: defaults, exclude: excludeFromBackup, now: time.Now}
}

// Path is where id's bare repo lives: repos/<host>/<owner>/<name>.git.
func (c *Cache) Path(id repo.ID) string {
	return filepath.Join(c.root, "repos", filepath.FromSlash(id.String())+".git")
}

// Warnings are problems that didn't stop an operation, for the command's
// output (ADR 0016).
func (c *Cache) Warnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.warnings)
}

// warn records a warning. It's safe to call from concurrent operations.
func (c *Cache) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	c.log.Warn("cache warning", slog.String("warning", msg))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warnings = append(c.warnings, msg)
}

func (c *Cache) lockPath(id repo.ID) string {
	return filepath.Join(c.root, "locks", filepath.FromSlash(id.String())+".lock")
}

func (c *Cache) reposDir() string {
	return filepath.Join(c.root, "repos")
}

// tmpDir holds clones in progress and repos being removed.
func (c *Cache) tmpDir() string {
	return filepath.Join(c.root, "tmp")
}

// exists reports whether id is cached: its directory has a HEAD.
func (c *Cache) exists(id repo.ID) bool {
	_, err := os.Stat(filepath.Join(c.Path(id), "HEAD"))
	return err == nil
}

// ensureRoot creates the cache directory if it's missing, and then asks
// Time Machine to skip it (ADR 0011). A failed exclusion is a warning.
func (c *Cache) ensureRoot(ctx context.Context) error {
	if _, err := os.Stat(c.root); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking cache directory: %w", err)
	}
	if err := os.MkdirAll(c.root, 0o750); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	if err := c.exclude(ctx, c.root); err != nil {
		c.warn("couldn't exclude %s from Time Machine backups: %v", c.root, err)
	}
	return nil
}
