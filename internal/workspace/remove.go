package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RemoveOptions change how Remove guards deletion.
type RemoveOptions struct {
	// AllowOutsideRoot lets Remove delete a recorded directory that isn't
	// directly under workspaces/ (ADR 0012).
	AllowOutsideRoot bool
}

// Remove deletes workspace id: its directory first, then the Claude Code
// trust ws added for its repo, if any, then its state (ADRs 0012, 0028).
// Its git state is ignored (ADR 0015). The directory is deleted only if
// the deletion guard allows it. A workspace whose directory is already
// gone has just its trust and state removed. Trust that can't be removed
// is a warning.
func (s *Store) Remove(id string, opts RemoveOptions) error {
	ws, err := s.recordByID(id)
	if err != nil {
		return err
	}
	target, gone, err := s.guard(&ws, opts)
	if err != nil {
		return err
	}
	if !gone {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("removing %s: %w", target, err)
		}
	}
	s.untrust(ws.ID)
	if err := os.RemoveAll(s.stateOf(ws.ID)); err != nil {
		return fmt.Errorf("removing state for %s: %w", ws.ID, err)
	}
	return nil
}

// GuardedDir returns the directory Remove would delete for workspace id:
// its recorded path with symlinks resolved, once the deletion guard has
// allowed it (ADR 0012). A workspace whose directory is gone is
// ErrNotFound.
func (s *Store) GuardedDir(id string) (string, error) {
	ws, err := s.recordByID(id)
	if err != nil {
		return "", err
	}
	target, gone, err := s.guard(&ws, RemoveOptions{})
	if err != nil {
		return "", err
	}
	if gone {
		return "", fmt.Errorf("%w: %s doesn't exist", ErrNotFound, ws.Path)
	}
	return target, nil
}

// guard applies ADR 0012's deletion guard to a workspace whose record
// parsed: the directory to delete is the recorded path, resolved through
// symlinks, and it must be workspaces/<id>, also resolved, unless
// AllowOutsideRoot. gone reports that the directory no longer exists.
func (s *Store) guard(ws *Workspace, opts RemoveOptions) (target string, gone bool, err error) {
	target, err = filepath.EvalSymlinks(ws.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("%w %s: %w", ErrUnsafe, ws.Path, err)
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return "", false, fmt.Errorf("%w %s: %w", ErrUnsafe, ws.Path, err)
	}
	if contains(target, root) {
		return "", false, fmt.Errorf("%w %s: it resolves to %s, which holds every workspace", ErrUnsafe, ws.Path, target)
	}
	if opts.AllowOutsideRoot {
		return target, false, nil
	}
	if target != filepath.Join(root, ws.ID) {
		return "", false, fmt.Errorf("%w %s: it resolves to %s, not %s", ErrUnsafe, ws.Path, target, filepath.Join(root, ws.ID))
	}
	return target, false, nil
}

// contains reports whether dir is path or one of its parents.
func contains(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
