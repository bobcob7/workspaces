// Package writeonce creates files that ws writes once and never
// overwrites, such as config.toml and workspaces/CLAUDE.md.
package writeonce

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File creates path with data unless something is already there. It
// writes a temp file in path's directory and hard-links it into place, so
// a reader never sees part of the file, and a file another process or a
// person put there first is kept. The directory must exist.
func File(path string, data []byte) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
