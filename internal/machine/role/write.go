package role

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// lockName is the lock file in the user's directory that every write holds.
// It isn't a .md file, so it's never read as a role.
const lockName = ".lock"

// Changes are the fields an edit sets; nil leaves a field as it is.
type Changes struct {
	Description *string
	Model       *string
	Pass        *[]string
	Fail        *[]string
	Vars        *[]string
	ClaudeArgs  *[]string
	Prompt      *string
}

// Writer adds, edits, and removes the roles in the user's directory,
// normally CONFIG_DIR/machine/roles. Each write is validated, goes to a
// temp file, is parsed back, and is renamed into place, under a lock on
// the directory.
type Writer struct {
	dir string
	set *Set
}

// NewWriter returns a Writer over dir, which the first write creates.
func NewWriter(dir string) *Writer {
	return &Writer{dir: dir, set: NewSet(dir)}
}

// Path is where the user's file for role name is, or would be.
func (w *Writer) Path(name string) string {
	return filepath.Join(w.dir, name+".md")
}

// Add writes a new role. Its name must be valid and no role's, built in or
// the user's, even one whose file doesn't parse.
//
//nolint:gocritic // hugeParam: Role is passed by value, as the Writer contract in ADR 0050 states.
func (w *Writer) Add(ctx context.Context, ro Role) (Role, error) {
	unlock, err := w.lock(ctx)
	if err != nil {
		return Role{}, err
	}
	defer unlock()
	exists, err := w.userFileExists(ro.Name)
	if err != nil {
		return Role{}, err
	}
	if exists || w.set.isBuiltin(ro.Name) {
		return Role{}, fmt.Errorf("%w: %q is already a role; edit it instead", ErrExists, ro.Name)
	}
	return w.write(&ro)
}

// Edit applies changes to role name. A built-in is copied in full to the
// user's directory first, so it becomes an override.
func (w *Writer) Edit(ctx context.Context, name string, changes *Changes) (Role, error) {
	unlock, err := w.lock(ctx)
	if err != nil {
		return Role{}, err
	}
	defer unlock()
	ro, ok, err := w.set.Load(name)
	if err != nil {
		return Role{}, fmt.Errorf("%w: %w", ErrBroken, err)
	}
	if !ok {
		return Role{}, fmt.Errorf("%w: no role named %q", ErrUnknown, name)
	}
	changes.apply(&ro)
	return w.write(&ro)
}

// Remove deletes the user's file for role name, and returns the source it
// had: for an override, the built-in is back. A built-in without an
// override can't be removed.
func (w *Writer) Remove(ctx context.Context, name string) (Source, error) {
	unlock, err := w.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	exists, err := w.userFileExists(name)
	if err != nil {
		return "", err
	}
	builtin := w.set.isBuiltin(name)
	switch {
	case !exists && builtin:
		return "", fmt.Errorf("%w: %q has no edits to remove", ErrBuiltin, name)
	case !exists:
		return "", fmt.Errorf("%w: no role named %q", ErrUnknown, name)
	}
	if err := os.Remove(w.Path(name)); err != nil {
		return "", fmt.Errorf("removing role %s: %w", name, err)
	}
	if builtin {
		return SourceOverride, nil
	}
	return SourceUser, nil
}

func (c *Changes) apply(r *Role) {
	if c.Description != nil {
		r.Description = *c.Description
	}
	if c.Model != nil {
		r.Model = *c.Model
	}
	if c.Pass != nil {
		r.Pass = *c.Pass
	}
	if c.Fail != nil {
		r.Fail = *c.Fail
	}
	if c.Vars != nil {
		r.Vars = *c.Vars
	}
	if c.ClaudeArgs != nil {
		r.ClaudeArgs = *c.ClaudeArgs
	}
	if c.Prompt != nil {
		r.Prompt = *c.Prompt
	}
}

func (w *Writer) lock(ctx context.Context) (func(), error) {
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(w.dir, lockName))
	if err != nil {
		return nil, fmt.Errorf("locking %s: %w", w.dir, err)
	}
	return unlock, nil
}

// userFileExists reports whether the user's directory has name's file,
// matched byte for byte as Reader.Load does.
func (w *Writer) userFileExists(name string) (bool, error) {
	if !validName(name) {
		return false, nil
	}
	names, err := w.set.user.fileNames()
	if err != nil {
		return false, err
	}
	return slices.Contains(names, name+".md"), nil
}

// write validates r, then puts its file in place through a temp file in the
// same directory, parsed back before the rename so a broken file is never
// left behind.
func (w *Writer) write(r *Role) (Role, error) {
	if err := r.Validate(); err != nil {
		return Role{}, err
	}
	raw, err := Format(r)
	if err != nil {
		return Role{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	tmp, err := writeTemp(w.dir, r.Name, raw)
	if err != nil {
		return Role{}, fmt.Errorf("writing role %s: %w", r.Name, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	written, err := readBack(r.Name, tmp)
	if err != nil {
		return Role{}, err
	}
	if err := os.Rename(tmp, w.Path(r.Name)); err != nil {
		return Role{}, fmt.Errorf("writing role %s: %w", r.Name, err)
	}
	written.Source = SourceUser
	if w.set.isBuiltin(r.Name) {
		written.Source = SourceOverride
	}
	return written, nil
}

// writeTemp writes raw to a new hidden file in dir and returns its path;
// the name doesn't end in .md, so it's never read as a role.
func writeTemp(dir, name string, raw []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, "."+name+".md-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func readBack(name, path string) (Role, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Role{}, fmt.Errorf("reading back role %s: %w", name, err)
	}
	return Parse(name, raw)
}
