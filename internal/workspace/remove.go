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

// Remove deletes workspace id: its directory first, then its state
// (ADR 0012). Its git state is ignored (ADR 0015). The directory is
// deleted only if the deletion guard allows it. A workspace whose
// directory is already gone has just its state removed.
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
	if err := os.RemoveAll(s.stateOf(ws.ID)); err != nil {
		return fmt.Errorf("removing state for %s: %w", ws.ID, err)
	}
	return nil
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
