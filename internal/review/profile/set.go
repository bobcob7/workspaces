package profile

import (
	"slices"
	"strings"
)

// Set is the profiles built into ws, with the user's profiles added to them
// or replacing them by name (ADR 0038).
type Set struct {
	user    *Reader
	builtin *Reader
}

// NewSet returns the built-in profiles overlaid with those in userDir,
// normally CONFIG_DIR/review/profiles.
func NewSet(userDir string) *Set {
	return &Set{user: New(userDir), builtin: Builtin()}
}

// Load reads one profile by name: the user's file if there is one, else the
// built-in. A user's file that can't be read or parsed is an error, not a
// fall back to the built-in it was meant to replace.
func (s *Set) Load(name string) (Profile, bool, error) {
	p, ok, err := s.user.Load(name)
	if err != nil || ok {
		return p, ok, err
	}
	return s.builtin.Load(name)
}

// List returns every profile, sorted by name, and the files that failed to
// parse. A user's file that failed to parse hides the built-in of the same
// name, as Load does.
func (s *Set) List() (profiles []Profile, broken []string, err error) {
	user, broken, err := s.user.List()
	if err != nil {
		return nil, nil, err
	}
	builtin, builtinBroken, err := s.builtin.List()
	if err != nil {
		return nil, nil, err
	}
	broken = append(broken, builtinBroken...)
	profiles = user
	for _, p := range builtin {
		if !s.overridden(p.Name, user, broken) {
			profiles = append(profiles, p)
		}
	}
	slices.SortFunc(profiles, func(a, b Profile) int { return strings.Compare(a.Name, b.Name) })
	return profiles, broken, nil
}

func (s *Set) overridden(name string, user []Profile, broken []string) bool {
	if slices.ContainsFunc(user, func(p Profile) bool { return p.Name == name }) {
		return true
	}
	return slices.ContainsFunc(broken, func(file string) bool {
		return strings.HasPrefix(file, s.user.label) && strings.HasSuffix(file, "/"+name+".md")
	})
}
