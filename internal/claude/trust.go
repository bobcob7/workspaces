package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ErrParentUntrusted means Trust was asked to trust a directory none of
// whose parents Claude Code trusts, so nothing was written (ADR 0028).
var ErrParentUntrusted = errors.New("parent directory not trusted by Claude Code")

// Claude Code's lock on its global config: a directory next to the file,
// taken with mkdir, that counts as abandoned once its mtime is older than
// configLockStale (ADR 0028). ws never refreshes the lock's mtime, so
// everything it does under the lock must take well under configLockStale.
const (
	configLockStale = 10 * time.Second
	configLockRetry = 25 * time.Millisecond
	configLockWait  = 5 * time.Second
)

// trustKey is the project key that records an accepted trust dialog.
const trustKey = "hasTrustDialogAccepted"

// configFile is Claude Code's global config, where it keeps each folder's
// trust: $CLAUDE_CONFIG_DIR/.claude.json when CLAUDE_CONFIG_DIR is set and
// not empty, else $HOME/.claude.json, or "" when neither is set.
func configFile(environ []string) string {
	if dir := lookup(environ, "CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	if home := lookup(environ, "HOME"); home != "" {
		return filepath.Join(home, ".claude.json")
	}
	return ""
}

// Trust marks dir as trusted in Claude Code's global config, the way
// Claude Code's docs say to trust a folder by hand, if parent or one of
// its parents is trusted there and dir isn't yet (ADR 0028). Both are
// keyed with symlinks resolved. Without a trusted parent it fails with
// ErrParentUntrusted and writes nothing. When ws is about to add the
// trust, record is called with the resolved dir and whether ws creates
// dir's whole project entry, before the file is written, so Untrust can
// undo exactly that; if record fails, nothing is written. The file is
// changed under Claude Code's own lock, through a temp file and a rename,
// keeping every other key's value (keys are written sorted).
func (c *Client) Trust(ctx context.Context, dir, parent string, record func(dir string, created bool) error) error {
	if c.configFile == "" {
		return errors.New("trusting a directory: neither CLAUDE_CONFIG_DIR nor HOME is set")
	}
	dir, parent = resolved(dir), resolved(parent)
	unlock, err := lockConfig(ctx, c.configFile)
	if err != nil {
		return fmt.Errorf("trusting %s: %w", dir, err)
	}
	defer unlock()
	added, err := trustIn(c.configFile, dir, parent, record)
	if err != nil {
		return fmt.Errorf("trusting %s: %w", dir, err)
	}
	if added {
		c.log.InfoContext(ctx, "trusted directory in Claude Code's config", slog.String("dir", dir), slog.String("config", c.configFile))
	}
	return nil
}

// Untrust undoes a Trust that added dir's trust: it removes dir's whole
// project entry when created, since everything in it then belongs to the
// removed workspace, and otherwise only the trust key (ADR 0028). A
// missing file or entry is already untrusted. It waits for Claude Code's
// lock even once a caller's context has ended, as removal must finish.
func (c *Client) Untrust(dir string, created bool) error {
	if c.configFile == "" {
		return errors.New("untrusting a directory: neither CLAUDE_CONFIG_DIR nor HOME is set")
	}
	unlock, err := lockConfig(context.Background(), c.configFile)
	if err != nil {
		return fmt.Errorf("untrusting %s: %w", dir, err)
	}
	defer unlock()
	cfg, err := readConfig(c.configFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("untrusting %s: %w", dir, err)
	}
	project, ok := cfg.projects[dir]
	if !ok {
		return nil
	}
	if created {
		delete(cfg.projects, dir)
	} else {
		if _, ok := project[trustKey]; !ok {
			return nil
		}
		delete(project, trustKey)
	}
	if err := cfg.write(c.configFile); err != nil {
		return fmt.Errorf("untrusting %s: %w", dir, err)
	}
	c.log.Info("removed directory's trust from Claude Code's config", slog.String("dir", dir), slog.Bool("entry", created))
	return nil
}

// trustIn sets projects[dir].hasTrustDialogAccepted in the config file at
// path, when parent or one of its parents is trusted there, while the
// caller holds the lock. added reports whether it did. A missing file
// trusts nothing.
func trustIn(path, dir, parent string, record func(dir string, created bool) error) (added bool, err error) {
	cfg, err := readConfig(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("%w: %s doesn't exist", ErrParentUntrusted, path)
	}
	if err != nil {
		return false, err
	}
	if !cfg.trustedWithin(parent) {
		return false, fmt.Errorf("%w: %s", ErrParentUntrusted, parent)
	}
	if trusted(cfg.projects[dir]) {
		return false, nil
	}
	created := cfg.projects[dir] == nil
	if err := record(dir, created); err != nil {
		return false, fmt.Errorf("recording the trust: %w", err)
	}
	if created {
		cfg.projects[dir] = map[string]json.RawMessage{}
	}
	cfg.projects[dir][trustKey] = json.RawMessage("true")
	return true, cfg.write(path)
}

// claudeConfig is Claude Code's global config as ws reads it: every value
// kept as raw JSON, with the project entries one level further decoded.
type claudeConfig struct {
	top      map[string]json.RawMessage
	projects map[string]map[string]json.RawMessage
	perm     fs.FileMode
}

func readConfig(path string) (*claudeConfig, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &claudeConfig{perm: info.Mode().Perm()}
	if err = json.Unmarshal(data, &cfg.top); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if raw, ok := cfg.top["projects"]; ok {
		if err = json.Unmarshal(raw, &cfg.projects); err != nil {
			return nil, fmt.Errorf("parsing %s: projects: %w", path, err)
		}
	}
	if cfg.projects == nil {
		cfg.projects = map[string]map[string]json.RawMessage{}
	}
	return cfg, nil
}

// trustedWithin reports whether dir or one of its parents is trusted.
func (cfg *claudeConfig) trustedWithin(dir string) bool {
	for {
		if trusted(cfg.projects[dir]) {
			return true
		}
		up := filepath.Dir(dir)
		if up == dir {
			return false
		}
		dir = up
	}
}

func (cfg *claudeConfig) write(path string) error {
	raw, err := encode(cfg.projects, "")
	if err != nil {
		return err
	}
	cfg.top["projects"] = raw
	out, err := encode(cfg.top, "  ")
	if err != nil {
		return err
	}
	return replaceFile(path, append(out, '\n'), cfg.perm)
}

// encode is json.Marshal without HTML escaping, so values Claude Code
// wrote come back as it wrote them, indented by indent if not empty.
func encode(v any, indent string) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// trusted reports whether a project entry has accepted the trust dialog.
func trusted(project map[string]json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(project[trustKey]), []byte("true"))
}

// replaceFile writes data to path through a temp file and a rename, so
// Claude Code never reads part of it. A symlinked path, as in a dotfiles
// setup, is followed, so the link stays and its target is replaced.
func replaceFile(path string, data []byte, perm fs.FileMode) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".ws-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// lockConfig takes Claude Code's lock on the config file at path: it makes
// the directory path.lock, waiting while another holder has it. A stale
// lock is first renamed to a unique name, so of two waiters that both
// find it stale only one takes it over, and then removed. unlock removes
// the lock.
func lockConfig(ctx context.Context, path string) (unlock func(), err error) {
	lock := path + ".lock"
	deadline := time.Now().Add(configLockWait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			return func() { os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > configLockStale {
			if stale, err := staleName(lock); err == nil && os.Rename(lock, stale) == nil {
				os.RemoveAll(stale)
			}
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("locking %s: %s held for over %s", path, lock, configLockWait)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(configLockRetry):
		}
	}
}

// staleName is a unique name to move a stale lock to before removing it.
func staleName(lock string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return lock + ".stale-" + hex.EncodeToString(b), nil
}
