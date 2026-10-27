// Package profile reads reviewer profiles: one Markdown file per profile,
// named <name>.md, opening with front matter that carries description, when,
// and match, and closing with a body that prime appends verbatim.
//
// ws builds eight profiles in and reads more, or replacements, from
// CONFIG_DIR/review/profiles/ (ADR 0038). The directory is read-only here:
// never created, and supplied by the caller.
package profile

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// builtinFiles are the profiles ws builds in.
//
//go:embed profiles/*.md
var builtinFiles embed.FS

// maxDescriptionRunes is the limit on description, counted in runes rather
// than bytes.
const maxDescriptionRunes = 120

// frontMatterFence is the line, alone, that opens and closes front matter.
const frontMatterFence = "---"

// reservedName can't name a profile; see validName.
const reservedName = "decisions"

// When says when a profile joins a review (ADR 0038).
type When string

const (
	// WhenAlways joins every review.
	WhenAlways When = "always"
	// WhenAuto joins a review whose files match one of the profile's
	// patterns.
	WhenAuto When = "auto"
	// WhenOff joins only a review that asks for it by name.
	WhenOff When = "off"
)

// ParseWhen reads a when value: always, auto, or off.
func ParseWhen(s string) (When, error) {
	switch w := When(s); w {
	case WhenAlways, WhenAuto, WhenOff:
		return w, nil
	}
	return "", fmt.Errorf("when must be always, auto, or off, not %q", s)
}

// Profile is one reviewer profile: Description is what a listing shows, and
// Body is the file's content after the closing fence, trimmed and never
// otherwise reflowed - callers frame it.
type Profile struct {
	Name        string
	Description string
	// When is always, auto, or off. A file without when is always, or auto
	// when it has match.
	When When
	// Match are the file patterns that pull an auto profile into a review;
	// see Matches.
	Match []string
	Body  string
	// Builtin is set for a profile built into ws, clear for one read from
	// the user's directory.
	Builtin bool
}

// Matches reports whether file, a slash-separated path relative to the
// repository, matches one of the profile's patterns. A pattern without a
// slash matches the file's base name, so "*.go" matches every Go file; one
// with a slash matches the whole path.
func (p *Profile) Matches(file string) bool {
	for _, pattern := range p.Match {
		subject := file
		if !strings.Contains(pattern, "/") {
			subject = path.Base(file)
		}
		if ok, err := path.Match(pattern, subject); err == nil && ok {
			return true
		}
	}
	return false
}

// Reader reads profiles from one directory.
type Reader struct {
	fsys    fs.FS
	label   string
	builtin bool
}

// New constructs a Reader over dir. dir is not required to exist: a missing
// directory is not a construction error, only an empty result for every
// name Load is asked to resolve against it.
func New(dir string) *Reader {
	return &Reader{fsys: os.DirFS(dir), label: dir}
}

// Builtin returns a Reader over the profiles built into ws.
func Builtin() *Reader {
	sub, err := fs.Sub(builtinFiles, "profiles")
	if err != nil {
		panic(fmt.Sprintf("profile: embedded profiles: %v", err)) // a build that embedded nothing is a programmer error
	}
	return &Reader{fsys: sub, label: "built-in profiles", builtin: true}
}

// Load reads one profile by name. ok is false when name is not a valid
// profile name, or names no file in the directory. A non-nil err means the
// file exists but is unreadable or malformed, and is never returned
// alongside ok=true.
//
// name is untrusted, and resolving it never touches the filesystem until it
// has been checked against the name charset, so a name carrying a separator,
// "..", a leading dot, or an absolute path can never reach a path outside
// the directory.
//
// A path is never opened by name+".md" directly: on a case-insensitive
// filesystem the kernel would fold that open onto whatever entry case-folds
// to it, so Load("readme") could silently open README.md even though README
// is not a valid profile name. Load enumerates the directory and only opens
// an entry whose name matches name+".md" byte for byte.
func (r *Reader) Load(name string) (Profile, bool, error) {
	if !validName(name) {
		return Profile{}, false, nil
	}
	names, err := r.fileNames()
	if err != nil {
		return Profile{}, false, err
	}
	if !slices.Contains(names, name+".md") {
		return Profile{}, false, nil
	}
	return r.loadEntry(name, name+".md")
}

// List returns every valid profile in the directory, sorted by name, and
// the file name of every profile that failed to parse. A missing directory
// is an empty list.
func (r *Reader) List() (profiles []Profile, broken []string, err error) {
	names, err := r.fileNames()
	if err != nil {
		return nil, nil, err
	}
	profiles = []Profile{}
	for _, file := range names {
		name, ok := strings.CutSuffix(file, ".md")
		if !ok || !validName(name) {
			continue
		}
		p, _, err := r.loadEntry(name, file)
		if err != nil {
			broken = append(broken, path.Join(r.label, file))
			continue
		}
		profiles = append(profiles, p)
	}
	return profiles, broken, nil
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
func (r *Reader) loadEntry(name, filename string) (Profile, bool, error) {
	raw, err := fs.ReadFile(r.fsys, filename)
	if errors.Is(err, fs.ErrNotExist) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, fmt.Errorf("reading %s: %w", path.Join(r.label, filename), err)
	}
	p, err := parse(name, raw)
	if err != nil {
		return Profile{}, false, fmt.Errorf("parsing %s: %w", path.Join(r.label, filename), err)
	}
	p.Builtin = r.builtin
	return p, true, nil
}

// validName is the name charset: lowercase letters, digits, and hyphens
// only. The charset alone is what makes a separator, "..", a leading dot,
// and an absolute path all invalid. "decisions" is reserved: the store
// keeps decisions.json beside each profile's <name>.json (ADR 0040).
func validName(name string) bool {
	if name == "" || name == reservedName {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// parse decodes one profile file: front matter fenced by two lines that read
// exactly "---", the keys description, when, and match, and a body that is
// everything after the closing fence, trimmed.
func parse(name string, raw []byte) (Profile, error) {
	// CRLF normalizes to LF before splitting: a profile hand-edited on a
	// core.autocrlf=true checkout must parse the same as its LF twin.
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if lines[0] != frontMatterFence {
		return Profile{}, errors.New("missing frontmatter: file must open with \"---\"")
	}
	closeIdx := slices.Index(lines[1:], frontMatterFence)
	if closeIdx == -1 {
		return Profile{}, errors.New("unterminated frontmatter: no closing \"---\"")
	}
	closeIdx++
	p, err := parseFrontMatter(lines[1:closeIdx])
	if err != nil {
		return Profile{}, err
	}
	p.Name = name
	p.Body = strings.TrimSpace(strings.Join(lines[closeIdx+1:], "\n"))
	if p.Body == "" {
		return Profile{}, errors.New("empty body")
	}
	return p, nil
}

// parseFrontMatter decodes the lines between the two fences. description is
// required, non-empty, and at most maxDescriptionRunes runes; when and match
// are optional.
func parseFrontMatter(lines []string) (Profile, error) {
	var p Profile
	seen := map[string]bool{}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Profile{}, fmt.Errorf("malformed frontmatter line %q", line)
		}
		key = strings.TrimSpace(key)
		if seen[key] {
			return Profile{}, fmt.Errorf("duplicate frontmatter key %q", key)
		}
		if err := setKey(&p, key, strings.TrimSpace(value)); err != nil {
			return Profile{}, err
		}
		seen[key] = true
	}
	if !seen["description"] {
		return Profile{}, errors.New("missing required frontmatter key \"description\"")
	}
	if err := defaultWhen(&p, seen["when"]); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// setKey sets one front matter key on p.
func setKey(p *Profile, key, value string) error {
	switch key {
	case "description":
		return setDescription(p, value)
	case "when":
		w, err := ParseWhen(withoutComment(value))
		p.When = w
		return err
	case "match":
		return setMatch(p, withoutComment(value))
	}
	return fmt.Errorf("unknown frontmatter key %q", key)
}

func setDescription(p *Profile, value string) error {
	if value == "" {
		return errors.New("description must not be empty")
	}
	if n := utf8.RuneCountInString(value); n > maxDescriptionRunes {
		return fmt.Errorf("description is %d characters, over the %d limit", n, maxDescriptionRunes)
	}
	p.Description = value
	return nil
}

// setMatch reads match as a list of quoted patterns, ["*.go", "go.mod"].
func setMatch(p *Profile, value string) error {
	var patterns []string
	if err := json.Unmarshal([]byte(value), &patterns); err != nil {
		return fmt.Errorf("match must be a list of quoted patterns, like [\"*.go\"]: %q", value)
	}
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil || pattern == "" {
			return fmt.Errorf("match pattern %q is malformed", pattern)
		}
	}
	p.Match = patterns
	return nil
}

// withoutComment drops a trailing "# comment" from a when or match value.
func withoutComment(value string) string {
	before, _, _ := strings.Cut(value, " #")
	return strings.TrimSpace(before)
}

// defaultWhen fills in when for a file without one, and refuses an auto
// profile that nothing could ever match.
func defaultWhen(p *Profile, given bool) error {
	if !given {
		p.When = WhenAlways
		if len(p.Match) > 0 {
			p.When = WhenAuto
		}
	}
	if p.When == WhenAuto && len(p.Match) == 0 {
		return errors.New("when: auto needs match patterns")
	}
	return nil
}
