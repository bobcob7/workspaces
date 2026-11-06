// Package role reads and writes ws machine roles: one Markdown file per
// role, <name>.md, with front matter (description, model, pass, fail,
// vars, claude_args) and a prompt template as the body. ws builds three
// in and reads more, or overrides, from CONFIG_DIR/machine/roles/ (ADR 0050).
package role

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/bobcob7/workspaces/internal/loop"
)

// builtinFiles are the roles ws builds in.
//
//go:embed roles/*.md
var builtinFiles embed.FS

// Errors a Reader, Set or Writer returns, wrapped with the reason.
var (
	ErrInvalid = errors.New("invalid role")               // can't be parsed or written
	ErrExists  = errors.New("role exists")                // add of a taken name
	ErrBuiltin = errors.New("role is built in")           // rm of a built-in without override
	ErrUnknown = errors.New("unknown role")               // edit, rm of an unknown name
	ErrBroken  = errors.New("role file is broken")        // edit of a user file that doesn't parse
	ErrRender  = errors.New("role prompt doesn't render") // Render: bad vars or template error
)

// maxDescriptionRunes is the limit on description, counted in runes.
const maxDescriptionRunes = 120

// maxNameLen is the longest role name, in bytes.
const maxNameLen = 64

// frontMatterFence is the line, alone, that opens and closes front matter.
const frontMatterFence = "---"

// verdictPattern is the shape of a verdict: upper case, starting with a
// letter, at most 32 characters.
var verdictPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

// Source says where a role comes from.
type Source string

const (
	// SourceBuiltin is a role built into ws.
	SourceBuiltin Source = "builtin"
	// SourceUser is a role from the user's directory with no built-in of its name.
	SourceUser Source = "user"
	// SourceOverride is a role from the user's directory that replaces the built-in of its name.
	SourceOverride Source = "override"
)

// Role is one role. Prompt is the body after the closing fence, trimmed.
type Role struct {
	Name        string
	Description string
	Model       string
	Pass        []string
	Fail        []string
	Vars        []string
	ClaudeArgs  []string
	Prompt      string
	Source      Source
}

// Parse reads a role file. Front matter is the lines between two "---"
// lines at the top; each is "key: value". description and model are the
// trimmed rest of the line; pass, fail, vars, and claude_args are JSON
// arrays of strings. An unknown or repeated key, or a missing fence, is
// ErrInvalid. Absent pass is ["PASS"], absent fail ["FAIL"], absent vars
// and claude_args []. The result must pass Validate.
func Parse(name string, raw []byte) (Role, error) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if lines[0] != frontMatterFence {
		return Role{}, fmt.Errorf("%w: file must open with %q", ErrInvalid, frontMatterFence)
	}
	closeIdx := slices.Index(lines[1:], frontMatterFence)
	if closeIdx == -1 {
		return Role{}, fmt.Errorf("%w: no closing %q", ErrInvalid, frontMatterFence)
	}
	closeIdx++
	r, err := parseFrontMatter(lines[1:closeIdx])
	if err != nil {
		return Role{}, err
	}
	r.Name = name
	r.Prompt = strings.TrimSpace(strings.Join(lines[closeIdx+1:], "\n"))
	if err := r.Validate(); err != nil {
		return Role{}, err
	}
	return r, nil
}

// parseFrontMatter decodes the lines between the two fences, applying the
// defaults for absent keys.
func parseFrontMatter(lines []string) (Role, error) {
	r := Role{Pass: []string{"PASS"}, Fail: []string{"FAIL"}, Vars: []string{}, ClaudeArgs: []string{}}
	seen := map[string]bool{}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Role{}, fmt.Errorf("%w: malformed front matter line %q", ErrInvalid, line)
		}
		key = strings.TrimSpace(key)
		if seen[key] {
			return Role{}, fmt.Errorf("%w: duplicate front matter key %q", ErrInvalid, key)
		}
		if err := setKey(&r, key, strings.TrimSpace(value)); err != nil {
			return Role{}, err
		}
		seen[key] = true
	}
	return r, nil
}

// setKey sets one front matter key on r.
func setKey(r *Role, key, value string) error {
	switch key {
	case "description":
		r.Description = value
	case "model":
		r.Model = value
	case "pass":
		return setList(&r.Pass, key, value)
	case "fail":
		return setList(&r.Fail, key, value)
	case "vars":
		return setList(&r.Vars, key, value)
	case "claude_args":
		return setList(&r.ClaudeArgs, key, value)
	default:
		return fmt.Errorf("%w: unknown front matter key %q", ErrInvalid, key)
	}
	return nil
}

// setList decodes a JSON array of strings into dst.
func setList(dst *[]string, key, value string) error {
	var list []string
	if err := json.Unmarshal([]byte(value), &list); err != nil {
		return fmt.Errorf("%w: %s must be a JSON array of strings, not %q", ErrInvalid, key, value)
	}
	if list == nil {
		list = []string{}
	}
	*dst = list
	return nil
}

// Format writes a role file Parse reads back to the same Role: model is
// omitted when empty, lists are always written as JSON arrays, then a
// blank line and the prompt with one trailing newline.
func Format(r *Role) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\ndescription: %s\n", frontMatterFence, strings.TrimSpace(r.Description))
	if r.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", r.Model)
	}
	lists := []struct {
		key  string
		list []string
	}{
		{"pass", r.Pass},
		{"fail", r.Fail},
		{"vars", r.Vars},
		{"claude_args", r.ClaudeArgs},
	}
	for _, l := range lists {
		encoded, err := encodeList(l.list)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", l.key, err)
		}
		fmt.Fprintf(&b, "%s: %s\n", l.key, encoded)
	}
	fmt.Fprintf(&b, "%s\n\n%s\n", frontMatterFence, strings.TrimSpace(r.Prompt))
	return b.Bytes(), nil
}

// encodeList is list as a JSON array, "[]" for a nil list.
func encodeList(list []string) (string, error) {
	if list == nil {
		list = []string{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(list); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// Validate checks, each failure ErrInvalid with the reason: the name (see
// checkName); description non-empty, one line, at most 120 runes; model without whitespace or control characters; pass and
// fail each non-empty, every verdict matching ^[A-Z][A-Z0-9_]{0,31}$, no
// duplicates, no verdict in both; vars valid per loop.ValidateVarKey, no
// duplicates, at most loop.MaxVars; no claude_args item equal to
// "--model" or "--settings" or starting with "--model=" or "--settings=";
// a prompt that isn't blank, parses as text/template with
// Option("missingkey=error"), and executes with {"Role": name, "Vars":
// every var mapped to "x"}.
func (r *Role) Validate() error {
	if err := checkName(r.Name); err != nil {
		return err
	}
	if err := r.validateDescription(); err != nil {
		return err
	}
	if strings.ContainsFunc(r.Model, isSpaceOrControl) {
		return fmt.Errorf("%w: model %q has whitespace or control characters", ErrInvalid, r.Model)
	}
	if err := checkVerdicts("pass", r.Pass); err != nil {
		return err
	}
	if err := checkVerdicts("fail", r.Fail); err != nil {
		return err
	}
	if err := checkDisjoint(r.Pass, r.Fail); err != nil {
		return err
	}
	if err := r.validateVars(); err != nil {
		return err
	}
	if err := checkClaudeArgs(r.ClaudeArgs); err != nil {
		return err
	}
	return r.checkPrompt()
}

// validName reports whether name is a role name: see checkName.
func validName(name string) bool {
	return checkName(name) == nil
}

// checkName refuses a name that can't name a role file: the charset of
// profile names, a first character that is a letter or digit, and 1-64
// bytes.
func checkName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("%w: name %q must be 1-%d characters", ErrInvalid, name, maxNameLen)
	}
	first := rune(name[0])
	if (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return fmt.Errorf("%w: name %q must start with a letter or digit", ErrInvalid, name)
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%w: name %q must be lowercase letters, digits, and hyphens", ErrInvalid, name)
		}
	}
	return nil
}

func (r *Role) validateDescription() error {
	if strings.TrimSpace(r.Description) == "" {
		return fmt.Errorf("%w: description must not be empty", ErrInvalid)
	}
	if strings.ContainsAny(r.Description, "\r\n") {
		return fmt.Errorf("%w: description must be one line", ErrInvalid)
	}
	if n := utf8.RuneCountInString(r.Description); n > maxDescriptionRunes {
		return fmt.Errorf("%w: description is %d characters, over the %d limit", ErrInvalid, n, maxDescriptionRunes)
	}
	return nil
}

func (r *Role) validateVars() error {
	if len(r.Vars) > loop.MaxVars {
		return fmt.Errorf("%w: %d vars is more than %d", ErrInvalid, len(r.Vars), loop.MaxVars)
	}
	seen := map[string]bool{}
	for _, key := range r.Vars {
		if err := loop.ValidateVarKey(key); err != nil {
			return fmt.Errorf("%w: vars: %w", ErrInvalid, err)
		}
		if seen[key] {
			return fmt.Errorf("%w: var %q is listed twice", ErrInvalid, key)
		}
		seen[key] = true
	}
	return nil
}

// checkPrompt parses the prompt and executes it with every declared var set.
func (r *Role) checkPrompt() error {
	if strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("%w: prompt must not be blank", ErrInvalid)
	}
	tmpl, err := template.New("prompt").Option("missingkey=error").Parse(r.Prompt)
	if err != nil {
		return fmt.Errorf("%w: prompt: %w", ErrInvalid, err)
	}
	vars := map[string]string{}
	for _, key := range r.Vars {
		vars[key] = "x"
	}
	values := map[string]any{"Role": r.Name, "Vars": vars}
	if err := tmpl.Execute(&bytes.Buffer{}, values); err != nil {
		return fmt.Errorf("%w: prompt: %w", ErrInvalid, err)
	}
	return nil
}

// checkVerdicts requires a non-empty list of well-formed verdicts.
func checkVerdicts(key string, verdicts []string) error {
	if len(verdicts) == 0 {
		return fmt.Errorf("%w: %s must list at least one verdict", ErrInvalid, key)
	}
	seen := map[string]bool{}
	for _, v := range verdicts {
		if !verdictPattern.MatchString(v) {
			return fmt.Errorf("%w: %s verdict %q must be upper case letters, digits, or '_', starting with a letter", ErrInvalid, key, v)
		}
		if seen[v] {
			return fmt.Errorf("%w: %s verdict %q is listed twice", ErrInvalid, key, v)
		}
		seen[v] = true
	}
	return nil
}

// checkDisjoint refuses a verdict that both passes and fails.
func checkDisjoint(pass, fail []string) error {
	for _, v := range pass {
		if slices.Contains(fail, v) {
			return fmt.Errorf("%w: verdict %q is both pass and fail", ErrInvalid, v)
		}
	}
	return nil
}

// checkClaudeArgs refuses the arguments a role can't pass to claude: the
// model is the role's own, and settings would change how claude runs.
func checkClaudeArgs(args []string) error {
	for _, arg := range args {
		switch {
		case arg == "--model", arg == "--settings",
			strings.HasPrefix(arg, "--model="), strings.HasPrefix(arg, "--settings="):
			return fmt.Errorf("%w: claude_args may not contain %q", ErrInvalid, arg)
		}
	}
	return nil
}

func isSpaceOrControl(c rune) bool {
	return unicode.IsSpace(c) || unicode.IsControl(c)
}

// Render is the prompt a child starts with. Every key in vars must be in
// r.Vars ("var %q isn't one of role %s's: A, B"). The template executes
// with {"Role": r.Name, "Vars": vars (an empty map when nil)} and
// missingkey=error. A result blank after TrimSpace is an error. When
// extra isn't blank, the result is rendered + "\n\n" + extra. Every
// error wraps ErrRender.
func (r *Role) Render(vars map[string]string, extra string) (string, error) {
	for _, key := range slices.Sorted(maps.Keys(vars)) {
		if !slices.Contains(r.Vars, key) {
			return "", fmt.Errorf("%w: var %q isn't one of role %s's: %s", ErrRender, key, r.Name, strings.Join(r.Vars, ", "))
		}
	}
	tmpl, err := template.New("prompt").Option("missingkey=error").Parse(r.Prompt)
	if err != nil {
		return "", fmt.Errorf("%w: prompt: %w", ErrRender, err)
	}
	if vars == nil {
		vars = map[string]string{}
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, map[string]any{"Role": r.Name, "Vars": vars}); err != nil {
		return "", fmt.Errorf("%w: prompt: %w", ErrRender, err)
	}
	rendered := strings.TrimSpace(out.String())
	if rendered == "" {
		return "", fmt.Errorf("%w: prompt renders empty", ErrRender)
	}
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return rendered, nil
	}
	return rendered + "\n\n" + extra, nil
}

// Args are the arguments for claude: prompt, then "--model", r.Model when
// r.Model isn't empty, then r.ClaudeArgs, then after.
func (r *Role) Args(prompt string, after []string) []string {
	args := []string{prompt}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	args = append(args, r.ClaudeArgs...)
	return append(args, after...)
}
