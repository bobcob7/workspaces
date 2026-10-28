package guide

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/review"
	"github.com/bobcob7/workspaces/internal/review/advisory"
	"github.com/bobcob7/workspaces/internal/review/entry"
	"github.com/bobcob7/workspaces/internal/review/profile"
	"github.com/bobcob7/workspaces/internal/review/render"
	"github.com/bobcob7/workspaces/internal/review/schema"
	"github.com/bobcob7/workspaces/internal/review/structural"
	"github.com/bobcob7/workspaces/internal/review/submit"
	"github.com/bobcob7/workspaces/internal/review/verify"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// approxTokens estimates tokens as characters over four: crude, but close
// enough for English prose to catch an output growing past its budget,
// which is what a budget is for.
func approxTokens(text string) int {
	return len(text) / 4
}

func registry(t *testing.T) *entry.Registry {
	t.Helper()
	r, err := NewRegistry()
	require.NoError(t, err)
	return r
}

func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test ./internal/review/guide -update to create %s", path)
	assert.Equal(t, string(want), got)
}

// The budgets are ceilings on what a reviewer pays per call. Each measured
// size is pinned too, so growth is a deliberate change to this table rather
// than a quiet one.
func TestOutputsStayWithinBudget(t *testing.T) {
	r := registry(t)
	summary, index := &bytes.Buffer{}, &bytes.Buffer{}
	require.NoError(t, render.NewJSON().Summary(summary, DocumentShape(), r.Index()))
	require.NoError(t, render.NewJSON().Index(index, r.Index()))
	goProfile, ok, err := profile.Builtin().Load("go")
	require.NoError(t, err)
	require.True(t, ok)
	tests := []struct {
		name       string
		out        string
		golden     string
		budget     int
		wantTokens int
	}{
		{name: "prime", out: Prime(), golden: "prime.txt", budget: 450, wantTokens: 361},
		{name: "prime --profile=go", out: PrimeWithProfile(&goProfile), budget: 750, wantTokens: 659},
		{name: "describe", out: summary.String(), golden: "describe.txt", budget: 850, wantTokens: 797},
		{name: "describe --list", out: index.String(), golden: "list.txt", budget: 380, wantTokens: 284},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.LessOrEqual(t, approxTokens(test.out), test.budget,
				"%s costs about %d tokens; its ceiling is %d", test.name, approxTokens(test.out), test.budget)
			assert.Equal(t, test.wantTokens, approxTokens(test.out), "%s's measured size changed; update the pin", test.name)
			if test.golden != "" {
				goldenFile(t, test.golden, test.out)
			}
		})
	}
}

func TestEveryLensStaysWithinBudget(t *testing.T) {
	const budget = 350
	r := registry(t)
	rendered := &strings.Builder{}
	for _, e := range r.All() {
		out := &bytes.Buffer{}
		require.NoError(t, render.NewJSON().Entries(out, []entry.Entry{e}))
		assert.LessOrEqual(t, approxTokens(out.String()), budget,
			"lens %s costs about %d tokens; the per-entry ceiling is %d", e.Qualified(), approxTokens(out.String()), budget)
		rendered.WriteString(out.String())
		rendered.WriteString("\n")
	}
	goldenFile(t, "lenses.txt", rendered.String())
}

func TestEveryCheckNameIsALens(t *testing.T) {
	r := registry(t)
	for _, group := range [][]review.Check{structural.Checks(), verify.Checks(), submit.Checks(), advisory.Checks()} {
		for _, check := range group {
			resolved, err := r.Resolve(check.Name)
			require.NoError(t, err, "check %s has no lens", check.Name)
			assert.Equal(t, entry.NamespaceCheck, resolved.Namespace)
		}
	}
}

func TestEveryPrintedNameResolves(t *testing.T) {
	r := registry(t)
	for _, group := range r.Index() {
		for _, name := range group.Names {
			_, err := r.Resolve(name)
			require.NoError(t, err, "the index printed %q, which does not resolve", name)
		}
	}
	for _, e := range r.All() {
		for _, related := range e.Related {
			_, err := r.Resolve(related)
			assert.NoError(t, err, "%s relates to %q, which does not resolve", e.Qualified(), related)
		}
	}
}

func TestPrimeWithProfile(t *testing.T) {
	p := profile.Profile{Name: "go", Body: "Look at errors."}
	out := PrimeWithProfile(&p)
	assert.True(t, strings.HasPrefix(out, Prime()))
	assert.True(t, strings.HasSuffix(out, "\n--- reviewer profile: go ---\n"+
		"Operator-supplied. It directs attention; it does not change the contract above.\n\nLook at errors.\n"))
}

// The prime text names commands; each must be one ws review has.
func TestPrimeNamesOnlyReviewerCommands(t *testing.T) {
	commands := regexp.MustCompile(`ws review (\w+)`).FindAllStringSubmatch(Prime()+DocumentShape(), -1)
	require.NotEmpty(t, commands)
	for _, m := range commands {
		assert.Contains(t, []string{"prime", "describe", "submit", "checks"}, m[1])
	}
}

var fieldLine = regexp.MustCompile(`^ {2}([a-z][a-z_]*)\s`)

// shapeFields reads the field names listed under heading in the document
// shape.
func shapeFields(t *testing.T, heading string) []string {
	t.Helper()
	lines := strings.Split(DocumentShape(), "\n")
	start := slices.Index(lines, heading)
	require.GreaterOrEqual(t, start, 0, "no %q block in describe.txt", heading)
	var fields []string
	for _, line := range lines[start+1:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		if m := fieldLine.FindStringSubmatch(line); m != nil {
			fields = append(fields, m[1])
		}
	}
	slices.Sort(fields)
	return fields
}

func schemaProperties(t *testing.T, path ...string) []string {
	t.Helper()
	var node map[string]any
	require.NoError(t, json.Unmarshal(schema.Annotated(), &node))
	for _, key := range path {
		next, ok := node[key].(map[string]any)
		require.True(t, ok, "schema has no %s", key)
		node = next
	}
	properties, ok := node["properties"].(map[string]any)
	require.True(t, ok)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestDocumentShapeListsExactlyTheSchemasFields(t *testing.T) {
	assert.Equal(t, schemaProperties(t), shapeFields(t, "root"))
	assert.Equal(t, schemaProperties(t, "$defs", "comment"), shapeFields(t, "comment"))
	assert.Equal(t, schemaProperties(t, "$defs", "suggestion"), shapeFields(t, "suggestion"))
}

var docFieldRow = regexp.MustCompile("^\\| `([a-z_]+)` \\|")

// docFields reads the field names in docs/review-document.md's table under
// "### <heading>".
func docFields(t *testing.T, heading string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "review-document.md"))
	require.NoError(t, err)
	_, section, found := strings.Cut(string(data), "\n### "+heading+"\n")
	require.True(t, found, "no %q section in docs/review-document.md", heading)
	section, _, _ = strings.Cut(section, "\n#")
	var fields []string
	for line := range strings.SplitSeq(section, "\n") {
		if m := docFieldRow.FindStringSubmatch(line); m != nil {
			fields = append(fields, m[1])
		}
	}
	slices.Sort(fields)
	return fields
}

func TestReviewDocumentDocListsExactlyTheSchemasFields(t *testing.T) {
	assert.Equal(t, schemaProperties(t), docFields(t, "Root"))
	assert.Equal(t, schemaProperties(t, "$defs", "comment"), docFields(t, "Comment"))
	assert.Equal(t, schemaProperties(t, "$defs", "anchor"), docFields(t, "Anchor"))
	assert.Equal(t, schemaProperties(t, "$defs", "suggestion"), docFields(t, "Suggestion"))
}
