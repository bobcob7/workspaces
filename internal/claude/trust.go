package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ErrParentUntrusted means Trust was asked to trust a directory whose
// parent Claude Code doesn't trust, so nothing was written (ADR 0028).
var ErrParentUntrusted = errors.New("parent directory not trusted by Claude Code")

// Claude Code's lock on its global config: a directory next to the file,
// taken with mkdir, that counts as abandoned once its mtime is older than
// configLockStale (ADR 0028).
const (
	configLockStale = 10 * time.Second
	configLockRetry = 25 * time.Millisecond
	configLockWait  = 5 * time.Second
)

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
// Claude Code's docs say to trust a folder by hand, if parent is trusted
// there and dir isn't yet (ADR 0028). Both are keyed with symlinks
// resolved. Without a trusted parent it fails with ErrParentUntrusted and
// writes nothing. The file is changed under Claude Code's own lock,
// through a temp file and a rename, keeping every other key as it was.
func (c *Client) Trust(ctx context.Context, dir, parent string) error {
	if c.configFile == "" {
		return errors.New("trusting a directory: neither CLAUDE_CONFIG_DIR nor HOME is set")
	}
	dir, parent = resolved(dir), resolved(parent)
	unlock, err := lockConfig(ctx, c.configFile)
	if err != nil {
		return fmt.Errorf("trusting %s: %w", dir, err)
	}
	defer unlock()
	if err := trustIn(c.configFile, dir, parent); err != nil {
		return fmt.Errorf("trusting %s: %w", dir, err)
	}
	c.log.InfoContext(ctx, "trusted directory in Claude Code's config", slog.String("dir", dir), slog.String("config", c.configFile))
	return nil
}

// trustIn sets projects[dir].hasTrustDialogAccepted in the config file at
// path, when projects[parent] has it set, while the caller holds the lock.
// A missing file trusts nothing.
func trustIn(path, dir, parent string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s doesn't exist", ErrParentUntrusted, path)
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	var projects map[string]map[string]json.RawMessage
	if raw, ok := config["projects"]; ok {
		if err = json.Unmarshal(raw, &projects); err != nil {
			return fmt.Errorf("parsing %s: projects: %w", path, err)
		}
	}
	if !trusted(projects[parent]) {
		return fmt.Errorf("%w: %s", ErrParentUntrusted, parent)
	}
	if trusted(projects[dir]) {
		return nil
	}
	if projects[dir] == nil {
		projects[dir] = map[string]json.RawMessage{}
	}
	projects[dir]["hasTrustDialogAccepted"] = json.RawMessage("true")
	raw, err := marshal(projects)
	if err != nil {
		return err
	}
	config["projects"] = raw
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(config); err != nil {
		return err
	}
	return replaceFile(path, out.Bytes(), info.Mode().Perm())
}

// marshal is json.Marshal without HTML escaping, so values Claude Code
// wrote come back as it wrote them.
func marshal(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// trusted reports whether a project entry has accepted the trust dialog.
func trusted(project map[string]json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(project["hasTrustDialogAccepted"]), []byte("true"))
}

// replaceFile writes data to path through a temp file in its directory and
// a rename, so Claude Code never reads part of it.
func replaceFile(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".ws-*")
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
	return os.Rename(tmp.Name(), path)
}

// lockConfig takes Claude Code's lock on the config file at path: it makes
// the directory path.lock, waiting while another holder has it, and
// removing it first when it's stale. unlock removes it.
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
			os.Remove(lock)
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
