package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProfile writes one profile file under dir, failing the test on error.
func writeProfile(t *testing.T, dir, filename, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600))
}

// validFrontMatter builds a minimal, well-formed frontmatter block with the
// given description, ready to have a body appended after it.
func validFrontMatter(description string) string {
	return "---\ndescription: " + description + "\n---\n"
}

func TestReader_Load_Valid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeProfile(t, dir, "backend.md", "---\ndescription: Go services; concurrency, error wrapping, context handling\n---\n\nAnchor concurrency findings at the goroutine that leaks.\n")
	r := New(dir)
	p, ok, err := r.Load("backend")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "backend", p.Name)
	assert.Equal(t, "Go services; concurrency, error wrapping, context handling", p.Description)
	assert.Equal(t, "Anchor concurrency findings at the goroutine that leaks.", p.Body)
}

func TestReader_Load_Outcomes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeProfile(t, dir, "good.md", validFrontMatter("A valid profile")+"Body text.\n")
	writeProfile(t, dir, "broken.md", "---\ndescription: ok\n---\n")
	writeProfile(t, dir, "decisions.md", validFrontMatter("Reserved")+"Body text.\n")
	r := New(dir)
	t.Run("valid name and file", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("good")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "good", p.Name)
	})
	t.Run("name matches no file", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("missing")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, p)
	})
	t.Run("name is not a valid name", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("UPPERCASE")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, p)
	})
	t.Run("decisions is reserved for the store's decisions.json", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("decisions")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, p)
	})
	t.Run("file exists but is malformed", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("broken")
		require.Error(t, err)
		assert.False(t, ok)
		assert.Zero(t, p)
	})
}

func TestReader_Load_PathTraversalRejectedAsBadName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	parent := filepath.Dir(dir)
	secretPath := filepath.Join(parent, "secret.md")
	require.NoError(t, os.WriteFile(secretPath, []byte(validFrontMatter("leaked secret")+"leaked body\n"), 0o600))
	t.Cleanup(func() { os.Remove(secretPath) })
	r := New(dir)
	cases := []struct {
		label string
		name  string
	}{
		{"parent traversal", "../secret"},
		{"embedded separator", "a/b"},
		{"absolute path", "/etc/passwd"},
		{"leading dot", ".hidden"},
		{"uppercase", "UPPER"},
		{"underscore", "has_underscore"},
		{"empty", ""},
		{"space", "has space"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			p, ok, err := r.Load(tc.name)
			require.NoError(t, err)
			assert.False(t, ok)
			assert.Zero(t, p)
		})
	}
}

// TestReader_Load_IdentityNotCaseFold pins refinery-emv.7's acceptance
// criterion directly: on a case-insensitive filesystem, resolving
// "readme.md" by name would let the kernel fold it onto README.md even
// though "README" is not a valid profile name. Load must refuse it rather
// than opening the file the identity check hides.
func TestReader_Load_IdentityNotCaseFold(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeProfile(t, dir, "README.md", "# Not a profile\n")
	r := New(dir)
	p, ok, err := r.Load("readme")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, p)
}

func TestReader_Load_FrontMatterErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label           string
		content         string
		wantErrContains string
	}{
		{"missing frontmatter", "no frontmatter here\n", "missing frontmatter"},
		{"unterminated frontmatter", "---\ndescription: ok\nbody without closing fence\n", "unterminated frontmatter"},
		// The file's only key is unknown - no description line comes
		// first to trip the duplicate-key branch instead, which is what
		// let a mutant that deletes the unknown-key check stay green
		// (refinery-emv.10a): under that mutant "author" is read as the
		// description value and this file parses successfully.
		{"unknown key only", "---\nauthor: someone\n---\nbody\n", `unknown frontmatter key "author"`},
		{"unknown key after description", "---\ndescription: ok\nauthor: someone\n---\nbody\n", `unknown frontmatter key "author"`},
		{"duplicate description key", "---\ndescription: one\ndescription: two\n---\nbody\n", `duplicate frontmatter key "description"`},
		{"missing description key", "---\n---\nbody\n", `missing required frontmatter key "description"`},
		{"empty description value", "---\ndescription: \n---\nbody\n", "description must not be empty"},
		{"malformed line with no colon", "---\ndescription value\n---\nbody\n", "malformed frontmatter line"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeProfile(t, dir, "profile.md", tc.content)
			r := New(dir)
			p, ok, err := r.Load("profile")
			require.Error(t, err)
			assert.False(t, ok)
			assert.Zero(t, p)
			assert.ErrorContains(t, err, tc.wantErrContains)
		})
	}
}

func TestReader_Load_DescriptionRuneLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := New(dir)
	desc120 := strings.Repeat("a", 120)
	desc121 := strings.Repeat("a", 121)
	multiByte120 := strings.Repeat("é", 120)
	writeProfile(t, dir, "at-limit.md", validFrontMatter(desc120)+"Body.\n")
	writeProfile(t, dir, "over-limit.md", validFrontMatter(desc121)+"Body.\n")
	writeProfile(t, dir, "multibyte.md", validFrontMatter(multiByte120)+"Body.\n")
	t.Run("120 runes passes", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("at-limit")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, desc120, p.Description)
	})
	t.Run("121 runes fails", func(t *testing.T) {
		t.Parallel()
		_, ok, err := r.Load("over-limit")
		require.Error(t, err)
		assert.False(t, ok)
	})
	t.Run("120 multi-byte runes passes despite being well over 120 bytes", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("multibyte")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, multiByte120, p.Description)
		assert.Equal(t, 120, utf8.RuneCountInString(p.Description))
	})
}

func TestReader_Load_EmptyBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := New(dir)
	writeProfile(t, dir, "empty.md", "---\ndescription: ok\n---\n")
	writeProfile(t, dir, "whitespace.md", "---\ndescription: ok\n---\n   \n\t\n")
	t.Run("no body at all", func(t *testing.T) {
		t.Parallel()
		_, ok, err := r.Load("empty")
		require.Error(t, err)
		assert.False(t, ok)
	})
	t.Run("whitespace-only body", func(t *testing.T) {
		t.Parallel()
		_, ok, err := r.Load("whitespace")
		require.Error(t, err)
		assert.False(t, ok)
	})
}

func TestReader_Load_BodyTrimming(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := New(dir)
	content := "---\ndescription: ok\n---\n\nFirst line.\n\n  Indented second line.\n\n\n"
	writeProfile(t, dir, "trim.md", content)
	p, ok, err := r.Load("trim")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "First line.\n\n  Indented second line.", p.Body)
}

// TestReader_Load_CRLFParsesSameAsLF is refinery-emv.8's first acceptance
// criterion: a profile hand-edited on a core.autocrlf=true checkout must
// parse identically to its LF twin, not fail on lines[0] == "---\r".
func TestReader_Load_CRLFParsesSameAsLF(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := "---\ndescription: CRLF twin\n---\n\nBody line one.\n\nBody line two.\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	writeProfile(t, dir, "lf.md", lf)
	writeProfile(t, dir, "crlf.md", crlf)
	r := New(dir)
	lfProfile, ok, err := r.Load("lf")
	require.NoError(t, err)
	require.True(t, ok)
	crlfProfile, ok, err := r.Load("crlf")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, lfProfile.Description, crlfProfile.Description)
	assert.Equal(t, lfProfile.Body, crlfProfile.Body)
}

// TestReader_Load_WhitespaceOnlyFrontMatterLineIsBlank is refinery-emv.8's
// second acceptance criterion: a whitespace-only frontmatter line - CRLF's
// empty line becomes exactly this once the "\r" survives a naive split -
// must be treated as blank rather than as a malformed key: value line.
func TestReader_Load_WhitespaceOnlyFrontMatterLineIsBlank(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeProfile(t, dir, "profile.md", "---\ndescription: ok\n   \n\t\n---\nBody.\n")
	r := New(dir)
	p, ok, err := r.Load("profile")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "ok", p.Description)
}

func TestParseWhen(t *testing.T) {
	for _, s := range []string{"always", "auto", "off"} {
		w, err := ParseWhen(s)
		require.NoError(t, err)
		assert.Equal(t, When(s), w)
	}
	_, err := ParseWhen("sometimes")
	require.ErrorContains(t, err, `not "sometimes"`)
}

func TestProfile_Matches(t *testing.T) {
	p := Profile{Match: []string{"*.go", "go.mod", "web/*.ts"}}
	t.Run("a pattern without a slash matches the base name at any depth", func(t *testing.T) {
		assert.True(t, p.Matches("main.go"))
		assert.True(t, p.Matches("internal/cli/app.go"))
		assert.True(t, p.Matches("tools/go.mod"))
	})
	t.Run("a pattern with a slash matches the whole path", func(t *testing.T) {
		assert.True(t, p.Matches("web/app.ts"))
		assert.False(t, p.Matches("web/src/app.ts"))
	})
	t.Run("nothing else matches", func(t *testing.T) {
		assert.False(t, p.Matches("README.md"))
		assert.False(t, p.Matches("go.sum"))
	})
	t.Run("no patterns match nothing", func(t *testing.T) {
		assert.False(t, (&Profile{}).Matches("main.go"))
	})
}

func TestReader_Load_WhenAndMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeProfile(t, dir, "explicit.md", "---\ndescription: d\nwhen: auto          # always | auto | off\nmatch: [\"*.rs\", \"Cargo.toml\"]  # files\n---\nBody.\n")
	writeProfile(t, dir, "plain.md", validFrontMatter("d")+"Body.\n")
	writeProfile(t, dir, "matched.md", "---\ndescription: d\nmatch: [\"*.py\"]\n---\nBody.\n")
	writeProfile(t, dir, "off.md", "---\ndescription: d\nwhen: off\n---\nBody.\n")
	r := New(dir)
	t.Run("when and match are read, comments dropped", func(t *testing.T) {
		t.Parallel()
		p, ok, err := r.Load("explicit")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, WhenAuto, p.When)
		assert.Equal(t, []string{"*.rs", "Cargo.toml"}, p.Match)
		assert.False(t, p.Builtin)
	})
	t.Run("without when or match a profile is always", func(t *testing.T) {
		t.Parallel()
		p, _, err := r.Load("plain")
		require.NoError(t, err)
		assert.Equal(t, WhenAlways, p.When)
		assert.Empty(t, p.Match)
	})
	t.Run("match without when is auto", func(t *testing.T) {
		t.Parallel()
		p, _, err := r.Load("matched")
		require.NoError(t, err)
		assert.Equal(t, WhenAuto, p.When)
	})
	t.Run("off is kept", func(t *testing.T) {
		t.Parallel()
		p, _, err := r.Load("off")
		require.NoError(t, err)
		assert.Equal(t, WhenOff, p.When)
	})
}

func TestReader_Load_WhenAndMatchErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label   string
		content string
		want    string
	}{
		{"unknown when", "---\ndescription: d\nwhen: never\n---\nb\n", "when must be always, auto, or off"},
		{"auto without match", "---\ndescription: d\nwhen: auto\n---\nb\n", "when: auto needs match patterns"},
		{"match not a list", "---\ndescription: d\nmatch: *.go\n---\nb\n", "match must be a list"},
		{"match pattern malformed", "---\ndescription: d\nmatch: [\"[\"]\n---\nb\n", "malformed"},
		{"match pattern empty", "---\ndescription: d\nmatch: [\"\"]\n---\nb\n", "malformed"},
		{"duplicate when", "---\ndescription: d\nwhen: off\nwhen: always\n---\nb\n", `duplicate frontmatter key "when"`},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeProfile(t, dir, "profile.md", tc.content)
			_, ok, err := New(dir).Load("profile")
			require.ErrorContains(t, err, tc.want)
			assert.False(t, ok)
		})
	}
}

func TestReader_List(t *testing.T) {
	t.Parallel()
	t.Run("valid profiles sorted, broken ones named, other files skipped", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "b.md", validFrontMatter("b")+"Body.\n")
		writeProfile(t, dir, "a.md", validFrontMatter("a")+"Body.\n")
		writeProfile(t, dir, "broken.md", "no front matter\n")
		writeProfile(t, dir, "README.md", "# not a profile\n")
		writeProfile(t, dir, "notes.txt", "x")
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.md"), 0o750))
		profiles, broken, err := New(dir).List()
		require.NoError(t, err)
		require.Len(t, profiles, 2)
		assert.Equal(t, "a", profiles[0].Name)
		assert.Equal(t, "b", profiles[1].Name)
		assert.Equal(t, []string{filepath.Join(dir, "broken.md")}, broken)
	})
	t.Run("a missing directory is empty", func(t *testing.T) {
		t.Parallel()
		profiles, broken, err := New(filepath.Join(t.TempDir(), "missing")).List()
		require.NoError(t, err)
		assert.Empty(t, profiles)
		assert.Empty(t, broken)
	})
}

func TestBuiltin(t *testing.T) {
	profiles, broken, err := Builtin().List()
	require.NoError(t, err)
	assert.Empty(t, broken)
	got := map[string]Profile{}
	for _, p := range profiles {
		assert.True(t, p.Builtin, p.Name)
		got[p.Name] = p
	}
	want := map[string]When{
		"architecture": WhenAlways, "claims": WhenAlways, "security": WhenAlways, "tests": WhenAlways,
		"go": WhenAuto, "typescript": WhenAuto,
		"compatibility": WhenOff, "efficiency": WhenOff,
	}
	require.Len(t, got, len(want))
	for name, when := range want {
		assert.Equal(t, when, got[name].When, name)
	}
	goProfile, tsProfile := got["go"], got["typescript"]
	assert.Equal(t, []string{"*.go", "go.mod"}, goProfile.Match)
	assert.Equal(t, []string{"*.ts", "*.tsx"}, tsProfile.Match)
}
