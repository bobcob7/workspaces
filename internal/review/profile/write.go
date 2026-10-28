package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// Errors a Writer returns, wrapped with the reason (ADR 0042).
var (
	// ErrInvalid is a profile that can't be written: a bad or reserved
	// name, or content that wouldn't parse back, such as an empty guide.
	ErrInvalid = errors.New("invalid profile")
	// ErrExists is an add of a name a profile, built in or not, has.
	ErrExists = errors.New("profile exists")
	// ErrBuiltin is a remove of a built-in that has no override.
	ErrBuiltin = errors.New("profile is built in")
	// ErrUnknown is an edit or remove of a name no profile has.
	ErrUnknown = errors.New("unknown profile")
	// ErrBroken is an edit of a user's file that can't be read or parsed;
	// remove it, or fix it by hand.
	ErrBroken = errors.New("profile file is broken")
)

// lockName is the lock file in the user's directory that every write holds.
// It isn't a .md file, so it's never read as a profile.
const lockName = ".lock"

// Changes are the fields an edit sets; nil leaves a field as it is, and an
// empty Match clears the patterns.
type Changes struct {
	Description *string
	When        *When
	Match       *[]string
	Body        *string
}

// Writer adds, edits, and removes the profiles in the user's directory,
// normally CONFIG_DIR/review/profiles (ADR 0042). Each write goes to a temp
// file, is parsed back, and is renamed into place, under a lock on the
// directory.
type Writer struct {
	dir string
	set *Set
}

// NewWriter returns a Writer over dir, which it creates on the first write.
func NewWriter(dir string) *Writer {
	return &Writer{dir: dir, set: NewSet(dir)}
}

// Path is where the user's file for profile name is, or would be.
func (w *Writer) Path(name string) string {
	return filepath.Join(w.dir, name+".md")
}

// Add writes a new profile. Its name must be valid and no profile's,
// built in or the user's, even one whose file doesn't parse.
func (w *Writer) Add(ctx context.Context, p Profile) (Profile, error) {
	if err := checkName(p.Name); err != nil {
		return Profile{}, err
	}
	unlock, err := w.lock(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer unlock()
	exists, err := w.userFileExists(p.Name)
	if err != nil {
		return Profile{}, err
	}
	if exists || w.set.isBuiltin(p.Name) {
		return Profile{}, fmt.Errorf("%w: %q is already a profile; edit it instead", ErrExists, p.Name)
	}
	return w.write(&p)
}

// Edit applies changes to profile name. A built-in is copied in full to
// the user's directory first, so it becomes an override.
func (w *Writer) Edit(ctx context.Context, name string, changes *Changes) (Profile, error) {
	if err := checkName(name); err != nil {
		return Profile{}, err
	}
	unlock, err := w.lock(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer unlock()
	p, ok, err := w.set.Load(name)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %w", ErrBroken, err)
	}
	if !ok {
		return Profile{}, fmt.Errorf("%w: no profile named %q", ErrUnknown, name)
	}
	changes.apply(&p)
	return w.write(&p)
}

// Remove deletes the user's file for profile name, and returns the source
// it had: for an override, the built-in is back. A built-in without an
// override can't be removed.
func (w *Writer) Remove(ctx context.Context, name string) (Source, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
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
		return "", fmt.Errorf("%w: %q has no edits to remove; set it to off instead", ErrBuiltin, name)
	case !exists:
		return "", fmt.Errorf("%w: no profile named %q", ErrUnknown, name)
	}
	if err := os.Remove(w.Path(name)); err != nil {
		return "", fmt.Errorf("removing profile %s: %w", name, err)
	}
	if builtin {
		return SourceOverride, nil
	}
	return SourceUser, nil
}

func (c *Changes) apply(p *Profile) {
	if c.Description != nil {
		p.Description = *c.Description
	}
	if c.When != nil {
		p.When = *c.When
	}
	if c.Match != nil {
		p.Match = *c.Match
	}
	if c.Body != nil {
		p.Body = *c.Body
	}
}

// checkName refuses a name Load would never resolve.
func checkName(name string) error {
	if name == reservedName {
		return fmt.Errorf("%w: %q is reserved", ErrInvalid, name)
	}
	if !validName(name) {
		return fmt.Errorf("%w: name %q must be lowercase letters, digits, and hyphens", ErrInvalid, name)
	}
	return nil
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
	names, err := w.set.user.fileNames()
	if err != nil {
		return false, err
	}
	return slices.Contains(names, name+".md"), nil
}

// write puts p's file in place through a temp file in the same directory,
// parsed back before the rename so a broken file is never left behind.
func (w *Writer) write(p *Profile) (Profile, error) {
	raw, err := Format(p)
	if err != nil {
		return Profile{}, err
	}
	tmp, err := writeTemp(w.dir, p.Name, raw)
	if err != nil {
		return Profile{}, fmt.Errorf("writing profile %s: %w", p.Name, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	written, err := readBack(p.Name, tmp)
	if err != nil {
		return Profile{}, err
	}
	if err := os.Rename(tmp, w.Path(p.Name)); err != nil {
		return Profile{}, fmt.Errorf("writing profile %s: %w", p.Name, err)
	}
	written.Source = SourceUser
	if w.set.isBuiltin(p.Name) {
		written.Source = SourceOverride
	}
	return written, nil
}

// writeTemp writes raw to a new hidden file in dir and returns its path;
// the name doesn't end in .md, so it's never read as a profile.
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

func readBack(name, path string) (Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("reading back profile %s: %w", name, err)
	}
	p, err := parse(name, raw)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return p, nil
}

// Format renders p as a profile file: front matter with description, when
// when set, and match when there are patterns, then the guide. It refuses
// what front matter can't hold on one line, and an empty guide; anything
// else wrong surfaces when the file is parsed back.
func Format(p *Profile) ([]byte, error) {
	if strings.ContainsAny(p.Description, "\r\n") {
		return nil, fmt.Errorf("%w: description must be one line", ErrInvalid)
	}
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return nil, fmt.Errorf("%w: the guide is empty", ErrInvalid)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\ndescription: %s\n", frontMatterFence, strings.TrimSpace(p.Description))
	if p.When != "" {
		fmt.Fprintf(&b, "when: %s\n", p.When)
	}
	if len(p.Match) > 0 {
		b.WriteString("match: ")
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(p.Match); err != nil {
			return nil, fmt.Errorf("encoding match: %w", err)
		}
	}
	fmt.Fprintf(&b, "%s\n\n%s\n", frontMatterFence, body)
	return b.Bytes(), nil
}
