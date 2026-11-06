package role

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// Reader reads roles from one directory.
type Reader struct {
	fsys    fs.FS
	label   string
	builtin bool
}

// New constructs a Reader over dir. dir is not required to exist: a missing
// directory is an empty result, not a construction error.
func New(dir string) *Reader {
	return &Reader{fsys: os.DirFS(dir), label: dir}
}

// Builtin returns a Reader over the roles built into ws.
func Builtin() *Reader {
	sub, err := fs.Sub(builtinFiles, "roles")
	if err != nil {
		panic(fmt.Sprintf("role: embedded roles: %v", err)) // a build that embedded nothing is a programmer error
	}
	return &Reader{fsys: sub, label: "built-in roles", builtin: true}
}

// Load reads one role by name. ok is false when name is not a valid role
// name, or names no file in the directory. A non-nil err means the file
// exists but is unreadable or malformed, and is never returned alongside
// ok=true.
//
// name is untrusted, so it is checked against the name rules before the
// filesystem is touched, and the directory is enumerated so that only an
// entry whose name matches name+".md" byte for byte is opened (see
// profile.Reader.Load for why).
func (r *Reader) Load(name string) (Role, bool, error) {
	if !validName(name) {
		return Role{}, false, nil
	}
	names, err := r.fileNames()
	if err != nil {
		return Role{}, false, err
	}
	if !slices.Contains(names, name+".md") {
		return Role{}, false, nil
	}
	return r.loadEntry(name, name+".md")
}

// List returns every valid role in the directory, sorted by name, and the
// file name of every role that failed to parse. A missing directory is an
// empty list.
func (r *Reader) List() (roles []Role, broken []string, err error) {
	names, err := r.fileNames()
	if err != nil {
		return nil, nil, err
	}
	roles = []Role{}
	for _, file := range names {
		name, ok := strings.CutSuffix(file, ".md")
		if !ok || !validName(name) {
			continue
		}
		ro, _, err := r.loadEntry(name, file)
		if err != nil {
			broken = append(broken, path.Join(r.label, file))
			continue
		}
		roles = append(roles, ro)
	}
	return roles, broken, nil
}

// fileNames lists the regular files in the directory, sorted.
func (r *Reader) fileNames() ([]string, error) {
	entries, err := fs.ReadDir(r.fsys, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", r.label, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// loadEntry reads and parses one directory entry already matched by name.
func (r *Reader) loadEntry(name, filename string) (Role, bool, error) {
	raw, err := fs.ReadFile(r.fsys, filename)
	if errors.Is(err, fs.ErrNotExist) {
		return Role{}, false, nil
	}
	if err != nil {
		return Role{}, false, fmt.Errorf("reading %s: %w", path.Join(r.label, filename), err)
	}
	ro, err := Parse(name, raw)
	if err != nil {
		return Role{}, false, fmt.Errorf("parsing %s: %w", path.Join(r.label, filename), err)
	}
	ro.Source = SourceUser
	if r.builtin {
		ro.Source = SourceBuiltin
	}
	return ro, true, nil
}

// Set is the roles built into ws, with the user's roles added to them or
// replacing them by name (ADR 0050).
type Set struct {
	user    *Reader
	builtin *Reader
}

// NewSet returns the built-in roles overlaid with those in userDir,
// normally CONFIG_DIR/machine/roles.
func NewSet(userDir string) *Set {
	return &Set{user: New(userDir), builtin: Builtin()}
}

// Load reads one role by name: the user's file if there is one, else the
// built-in. A user's file that can't be read or parsed is an error, not a
// fall back to the built-in it was meant to replace.
func (s *Set) Load(name string) (Role, bool, error) {
	ro, ok, err := s.user.Load(name)
	if err != nil {
		return Role{}, false, err
	}
	if !ok {
		return s.builtin.Load(name)
	}
	if s.isBuiltin(name) {
		ro.Source = SourceOverride
	}
	return ro, true, nil
}

// isBuiltin reports whether ws builds in a role named name. The built-ins
// are embedded and tested, so one that can't be read is reported as absent
// rather than as an error.
func (s *Set) isBuiltin(name string) bool {
	_, ok, err := s.builtin.Load(name)
	return ok && err == nil
}

// List returns every role, sorted by name, and the files that failed to
// parse. A user's file that failed to parse hides the built-in of the same
// name, as Load does.
func (s *Set) List() (roles []Role, broken []string, err error) {
	user, broken, err := s.user.List()
	if err != nil {
		return nil, nil, err
	}
	builtin, builtinBroken, err := s.builtin.List()
	if err != nil {
		return nil, nil, err
	}
	broken = append(broken, builtinBroken...)
	roles = user
	for i := range builtin {
		if j := slices.IndexFunc(user, func(u Role) bool { return u.Name == builtin[i].Name }); j >= 0 {
			roles[j].Source = SourceOverride
		}
		if !s.overridden(builtin[i].Name, user, broken) {
			roles = append(roles, builtin[i])
		}
	}
	slices.SortFunc(roles, func(a, b Role) int { return strings.Compare(a.Name, b.Name) })
	return roles, broken, nil
}

// overridden reports whether a user's file, parsed or broken, has name.
func (s *Set) overridden(name string, user []Role, broken []string) bool {
	if slices.ContainsFunc(user, func(ro Role) bool { return ro.Name == name }) {
		return true
	}
	return slices.ContainsFunc(broken, func(file string) bool {
		return strings.HasPrefix(file, s.user.label) && strings.HasSuffix(file, "/"+name+".md")
	})
}
