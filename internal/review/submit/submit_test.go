package submit

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/review"
	"github.com/bobcob7/workspaces/internal/review/advisory"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/schema"
	"github.com/bobcob7/workspaces/internal/review/structural"
	"github.com/bobcob7/workspaces/internal/review/validate"
	"github.com/bobcob7/workspaces/internal/review/verify"
)

const absentCommit = "0123456789abcdef0123456789abcdef01234567"

func logger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// newChecker wires a Checker the way ws review submit does.
func newChecker(t *testing.T) *Checker {
	t.Helper()
	v, err := schema.NewValidator()
	require.NoError(t, err)
	runner := git.NewRunner(logger(), "git", verify.PlainEnv(os.Environ()))
	return New(validate.New(structural.New(v, logger()), advisory.New(logger(), advisory.All()), validate.NewGitFinder(runner, logger()), logger()))
}

// repo makes a repository with a 20-line client.go committed, and returns
// its directory and commit.
func repo(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	runner := git.NewRunner(logger(), "git", os.Environ())
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=test@example.test", "-c", "user.name=test", "-c", "commit.gpgsign=false", "commit", "-qm", "init"},
	} {
		_, err := runner.Run(t.Context(), dir, args...)
		require.NoError(t, err)
	}
	out, err := runner.Run(t.Context(), dir, "rev-parse", "HEAD")
	require.NoError(t, err)
	return dir, strings.TrimSpace(string(out))
}

func rec(commit string) *record.Record {
	return &record.Record{Repo: "example.test/o/r", Ref: commit, Profiles: []record.Profile{{Name: "security"}, {Name: "go", Auto: true}}}
}

// doc is a valid review; fields override or, with a nil value, remove its
// top-level fields.
func doc(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	d := map[string]any{
		"version": "1",
		"verdict": "request_changes",
		"summary": "The retry loop drops the caller's deadline, so cancellation never reaches the client.",
		"profile": "security",
		"comments": []any{map[string]any{
			"id": "dropped-context-1", "priority": 8, "category": "correctness",
			"body":    "c.do ignores the caller's ctx, so cancellation never propagates to the request.",
			"anchors": []any{map[string]any{"file": "client.go", "line": 3, "end_line": 5}},
			"suggestions": []any{map[string]any{
				"summary": "Pass the caller's ctx through to c.do", "effort": "trivial", "scope": "line",
				"pros": []any{"Deadlines propagate"}, "cons": []any{"Callers see a change"},
			}},
		}},
	}
	for k, v := range fields {
		if v == nil {
			delete(d, k)
			continue
		}
		d[k] = v
	}
	data, err := json.Marshal(d)
	require.NoError(t, err)
	return data
}

func names(result *review.Result) []string {
	out := []string{}
	for _, d := range result.Diagnostics {
		if d.Severity == review.SeverityError {
			out = append(out, d.Name)
		}
	}
	return out
}

func TestChecker_Check(t *testing.T) {
	t.Parallel()
	dir, commit := repo(t)
	c := newChecker(t)
	t.Run("fills in a missing ref from the record", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(commit), dir, doc(t, map[string]any{"ref": nil}))
		require.NoError(t, err)
		assert.Equal(t, StatusValid, out.Status, names(out.Result))
		assert.Equal(t, "security", out.Profile)
		stored, err := review.Parse(out.Document)
		require.NoError(t, err)
		assert.Equal(t, commit, stored.Ref.Value)
		assert.Equal(t, 1, out.Result.Verification.Verified)
	})
	t.Run("keeps a matching ref's source as given", func(t *testing.T) {
		t.Parallel()
		source := doc(t, map[string]any{"ref": commit})
		out, err := c.Check(t.Context(), rec(commit), dir, source)
		require.NoError(t, err)
		assert.Equal(t, StatusValid, out.Status, names(out.Result))
		assert.Equal(t, source, out.Document)
	})
	t.Run("a different ref is invalid", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(commit), dir, doc(t, map[string]any{"ref": absentCommit}))
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, out.Status)
		assert.Contains(t, names(out.Result), "review-ref")
		assert.Contains(t, out.Result.Lenses(), "review-ref")
	})
	t.Run("a missing profile is invalid", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(commit), dir, doc(t, map[string]any{"profile": nil}))
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, out.Status)
		assert.Equal(t, []string{"review-profile"}, names(out.Result))
		assert.Contains(t, out.Result.Diagnostics[0].Message, `"security", "go"`)
	})
	t.Run("a profile not in the record is invalid", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(commit), dir, doc(t, map[string]any{"profile": "tests"}))
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, out.Status)
		assert.Equal(t, []string{"review-profile"}, names(out.Result))
		assert.Equal(t, "tests", out.Profile)
	})
	t.Run("a structural error is invalid", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(commit), dir, doc(t, map[string]any{"summary": "short"}))
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, out.Status)
		assert.False(t, out.Result.Valid)
	})
	t.Run("an anchor past the end of the file is invalid", func(t *testing.T) {
		t.Parallel()
		d := doc(t, nil)
		d = []byte(strings.Replace(string(d), `"line":3`, `"line":30`, 1))
		out, err := c.Check(t.Context(), rec(commit), dir, d)
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, out.Status)
		assert.Contains(t, names(out.Result), "anchor-line-out-of-range")
	})
	t.Run("input that isn't a JSON object is invalid, and every check is skipped", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(commit), dir, []byte("here is my review: {"))
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, out.Status)
		assert.Equal(t, []string{"document-unparseable"}, names(out.Result))
		skipped := []string{}
		for _, s := range out.Result.Skipped {
			skipped = append(skipped, s.Name)
		}
		assert.Contains(t, skipped, "review-profile")
		assert.Contains(t, skipped, "schema")
		assert.NotContains(t, skipped, "document-unparseable")
	})
	t.Run("a commit the repository doesn't have is unverifiable", func(t *testing.T) {
		t.Parallel()
		out, err := c.Check(t.Context(), rec(absentCommit), dir, doc(t, map[string]any{"ref": nil}))
		require.NoError(t, err)
		assert.Equal(t, StatusUnverifiable, out.Status)
		assert.Contains(t, names(out.Result), "ref-unknown")
	})
}

func TestChecker_Check_DivergedWorkingTree(t *testing.T) {
	t.Parallel()
	dir, commit := repo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.go"), []byte("edited\n"), 0o600))
	out, err := newChecker(t).Check(t.Context(), rec(commit), dir, doc(t, map[string]any{"profile": "tests"}))
	require.NoError(t, err)
	assert.Equal(t, StatusUnverifiable, out.Status)
	assert.Equal(t, []string{"anchor-worktree-diverged"}, names(out.Result), "the precondition reports only itself")
}

func TestWithRef(t *testing.T) {
	out, err := withRef([]byte(`{"a":1.50,"b":12345678901234567890}`), "abc")
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1.50,"b":12345678901234567890,"ref":"abc"}`, string(out))
	assert.Contains(t, string(out), "12345678901234567890", "numbers are kept as written")
	_, err = withRef([]byte(`[1]`), "abc")
	require.Error(t, err)
}

func TestStatus(t *testing.T) {
	t.Run("a precondition is unverifiable", func(t *testing.T) {
		assert.Equal(t, StatusUnverifiable, status(&review.Result{Precondition: true}, true))
	})
	t.Run("valid", func(t *testing.T) {
		assert.Equal(t, StatusValid, status(&review.Result{Valid: true}, false))
	})
	t.Run("verification-required is unverifiable", func(t *testing.T) {
		assert.Equal(t, StatusUnverifiable, status(&review.Result{Diagnostics: []review.Diagnostic{{Name: "verification-required"}}}, false))
	})
	t.Run("a wrong record outranks an unverifiable ref", func(t *testing.T) {
		assert.Equal(t, StatusInvalid, status(&review.Result{Diagnostics: []review.Diagnostic{{Name: "ref-unknown"}}}, true))
	})
}
