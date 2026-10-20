package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// List returns every complete workspace, oldest first: one with a record
// and a workspace directory. State without both is a creation in progress
// or a leftover, and is skipped. A record that can't be read is skipped
// with a warning.
func (s *Store) List() ([]Workspace, error) {
	ids, err := stateIDs(s.stateDir)
	if err != nil {
		return nil, err
	}
	var list []Workspace
	for _, id := range ids {
		ws, err := s.complete(id)
		if errors.Is(err, errNoRecord) || errors.Is(err, errNoDir) {
			continue
		}
		if err != nil {
			s.warn("skipping workspace %s: %v", id, err)
			continue
		}
		list = append(list, ws)
	}
	slices.SortStableFunc(list, func(a, b Workspace) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return list, nil
}

// Unknown returns directories under workspaces/ that no state describes.
// They aren't workspaces, and ws never deletes them (ADR 0012).
func (s *Store) Unknown() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.root, err)
	}
	var unknown []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(s.recordPath(e.Name())); err != nil {
			unknown = append(unknown, s.dir(e.Name()))
		}
	}
	return unknown, nil
}

// Find returns the workspace an argument names: its id, its path, or its
// agent's Claude session id (docs: "Workspace arguments").
func (s *Store) Find(arg string) (Workspace, error) {
	if ws, err := s.byID(arg); err == nil {
		return ws, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Workspace{}, err
	}
	list, err := s.List()
	if err != nil {
		return Workspace{}, err
	}
	if ws, ok := byPath(list, arg); ok {
		return ws, nil
	}
	if ws, ok := s.bySession(list, arg); ok {
		return ws, nil
	}
	return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, arg)
}

// byID returns the complete workspace with this id.
func (s *Store) byID(id string) (Workspace, error) {
	if !validID(id) {
		return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	ws, err := s.complete(id)
	if errors.Is(err, errNoRecord) || errors.Is(err, errNoDir) {
		return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return ws, err
}

// recordByID returns the record of workspace id, whether or not its
// directory exists.
func (s *Store) recordByID(id string) (Workspace, error) {
	if !validID(id) {
		return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	ws, err := readRecord(s.recordPath(id))
	if errors.Is(err, errNoRecord) {
		return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return ws, err
}

// complete reads workspace id's record and checks that its directory
// exists: errNoDir when it doesn't.
func (s *Store) complete(id string) (Workspace, error) {
	ws, err := readRecord(s.recordPath(id))
	if err != nil {
		return Workspace{}, err
	}
	if _, err := os.Lstat(ws.Path); errors.Is(err, fs.ErrNotExist) {
		return Workspace{}, fmt.Errorf("%w: %s", errNoDir, ws.Path)
	} else if err != nil {
		return Workspace{}, fmt.Errorf("checking %s: %w", ws.Path, err)
	}
	return ws, nil
}

// validID reports whether id could name a workspace: a single, non-hidden
// path element.
func validID(id string) bool {
	return id != "" && id == filepath.Base(id) && !strings.HasPrefix(id, ".")
}

// byPath matches a path against every workspace's recorded path, after
// resolving symlinks in both.
func byPath(list []Workspace, path string) (Workspace, bool) {
	want, err := resolvePath(path)
	if err != nil {
		return Workspace{}, false
	}
	for _, ws := range list {
		if got, err := resolvePath(ws.Path); err == nil && got == want {
			return ws, true
		}
	}
	return Workspace{}, false
}

// bySession finds the agent workspace whose session record holds this
// Claude session id.
func (s *Store) bySession(list []Workspace, id string) (Workspace, bool) {
	if id == "" {
		return Workspace{}, false
	}
	for _, ws := range list {
		sess, err := s.Session(ws.ID)
		if err == nil && sess.Kind == SessionClaude && sess.ClaudeSessionID == id {
			return ws, true
		}
	}
	return Workspace{}, false
}

// resolvePath makes path absolute and resolves its symlinks.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return resolved, nil
}

// stateIDs lists the state directories' names, skipping hidden entries.
func stateIDs(stateDir string) ([]string, error) {
	entries, err := os.ReadDir(stateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", stateDir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}
