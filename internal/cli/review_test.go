package cli

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/review/guide"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// reviewedRepo is the repo id newReviewApp's review workspace reviews.
const reviewedRepo = "github.com/bobcob7/buff-bear"

// reviewApp is a test App inside a review workspace: a real git repo at
// a commit, a review record, and CONFIG_DIR under a temp directory.
type reviewApp struct {
	*mockedApp
	paths  config.Paths
	ws     workspace.Workspace
	repo   string
	commit string
}

// newReviewApp returns an App whose working directory is in a review
// workspace that reviews reviewedRepo with security and go.
func newReviewApp(t *testing.T) *reviewApp {
	t.Helper()
	e := &reviewApp{mockedApp: newMockedApp(), paths: config.NewPaths(t.TempDir())}
	e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
		return e.paths, testConfig(), nil
	}}
	e.app.paths = e.paths
	e.ws = workspace.Workspace{ID: "review-buff-bear-3f2a9c1-d8efb24f", Path: filepath.Join(e.paths.Workspaces, "review-buff-bear-3f2a9c1-d8efb24f")}
	e.ws.Repos = []workspace.Repo{{ID: reviewedRepo, Path: "buff-bear"}}
	e.repo = filepath.Join(e.ws.Path, "buff-bear")
	e.commit = commitRepo(t, e.repo)
	e.store.ContainingFunc = func(dir string) (workspace.Workspace, error) {
		if strings.HasPrefix(dir, e.ws.Path) {
			return e.ws, nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.app.getwd = func() (string, error) { return e.repo, nil }
	require.NoError(t, os.MkdirAll(filepath.Join(e.paths.State, e.ws.ID), 0o750))
	rec := record.Record{Repo: reviewedRepo, Ref: e.commit, Profiles: []record.Profile{{Name: "security", Model: "opus"}, {Name: "go", Auto: true}}}
	require.NoError(t, record.Write(record.Path(e.paths.State, e.ws.ID), &rec))
	return e
}

// testCommit is the commit testRecord reviews.
const testCommit = "3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90"

// testRecord is a review of reviewedRepo's pull request 42 at testCommit
// with security and go.
func testRecord() record.Record {
	return record.Record{
		Repo: reviewedRepo, Ref: testCommit, Requester: "fix-login-d8efb24f", StartedAt: testNow(),
		Profiles: []record.Profile{{Name: "security", Model: "opus"}, {Name: "go", Auto: true}},
		PR:       &record.PR{Number: 42, URL: "https://github.com/bobcob7/buff-bear/pull/42", Base: "main", HeadBranch: "feat/login"},
	}
}

// commitRepo makes a git repo at dir with a 20-line client.go committed,
// and returns the commit.
func commitRepo(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.go"), []byte(strings.Repeat("line\n", 20)), 0o600))
	runner := git.NewRunner(e2eLogger(), "git", os.Environ())
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
	return strings.TrimSpace(string(out))
}

// reviewDoc is a valid review under profile, without ref; replace edits
// it as text.
func reviewDoc(profile string, replace ...string) string {
	doc := `{"version":"1","verdict":"request_changes","profile":"` + profile + `",` +
		`"summary":"The retry loop drops the caller's deadline, so cancellation never reaches the client.",` +
		`"comments":[{"id":"dropped-context-1","priority":8,"category":"correctness",` +
		`"body":"c.do ignores the caller's ctx, so cancellation never propagates to the request.",` +
		`"anchors":[{"file":"client.go","line":3,"end_line":5}],` +
		`"suggestions":[{"summary":"Pass the caller's ctx through to c.do","effort":"trivial","scope":"line",` +
		`"pros":["Deadlines propagate"],"cons":["Callers see a change"]}]}]}`
	return strings.NewReplacer(replace...).Replace(doc)
}

// run runs ws with args and stdin, and returns its exit code.
func (e *reviewApp) run(t *testing.T, stdin string, args ...string) int {
	t.Helper()
	e.app.stdin = strings.NewReader(stdin)
	return e.app.Run(t.Context(), args)
}

func TestApp_newReviewCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newReviewCmd()
	t.Run("has the requester's and reviewer's operations", func(t *testing.T) {
		var names []string
		for _, c := range cmd.Commands() {
			names = append(names, c.Name())
		}
		assert.ElementsMatch(t, []string{"start", "wait", "report", "list", "decide", "acted", "profiles", "prime", "describe", "submit"}, names)
	})
	t.Run("refuses an unknown operation", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"nope"}))
	})
}

func TestApp_findReviewWorkspace(t *testing.T) {
	t.Run("finds the workspace and its record", func(t *testing.T) {
		e := newReviewApp(t)
		rw, err := e.app.findReviewWorkspace()
		require.NoError(t, err)
		assert.Equal(t, e.ws.ID, rw.ws.ID)
		assert.Equal(t, e.commit, rw.record.Ref)
	})
	t.Run("outside every workspace is review_not_found", func(t *testing.T) {
		e := newReviewApp(t)
		e.app.getwd = func() (string, error) { return t.TempDir(), nil }
		requireCode(t, codeReviewNotFound, errOf(e.app.findReviewWorkspace()))
	})
	t.Run("a workspace without a review record is review_not_found", func(t *testing.T) {
		e := newReviewApp(t)
		require.NoError(t, os.Remove(record.Path(e.paths.State, e.ws.ID)))
		err := errOf(e.app.findReviewWorkspace())
		requireCode(t, codeReviewNotFound, err)
		assert.ErrorContains(t, err, "isn't a review workspace")
	})
	t.Run("a record from a newer ws is an error", func(t *testing.T) {
		e := newReviewApp(t)
		path := record.Path(e.paths.State, e.ws.ID)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "version = 1", "version = 9", 1)), 0o600))
		err = errOf(e.app.findReviewWorkspace())
		require.ErrorContains(t, err, "newer ws")
		assert.Equal(t, exitFailed, asCLIError(err).exitCode())
	})
	t.Run("a working directory it can't read", func(t *testing.T) {
		e := newReviewApp(t)
		e.app.getwd = func() (string, error) { return "", errors.New("gone") }
		require.ErrorContains(t, errOf(e.app.findReviewWorkspace()), "gone")
	})
}

func errOf(_ any, err error) error {
	return err
}

func TestReviewWorkspace_repo(t *testing.T) {
	rw := reviewWorkspace{
		ws:     workspace.Workspace{ID: "w", Path: string(filepath.Separator) + "ws", Repos: []workspace.Repo{{ID: "github.com/a/x", Path: "x"}, {ID: reviewedRepo, Path: "bb"}}},
		record: record.Record{Repo: reviewedRepo},
	}
	dir, id, err := rw.repo()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(string(filepath.Separator)+"ws", "bb"), dir)
	assert.Equal(t, reviewedRepo, id.String())
	rw.record.Repo = "github.com/a/missing"
	_, _, err = rw.repo()
	require.ErrorContains(t, err, "isn't in workspace w")
}

func TestApp_runReviewPrime(t *testing.T) {
	t.Run("prints the workflow", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "prime"), e.stderr.String())
		assert.Equal(t, guide.Prime(), e.stdout.String())
	})
	t.Run("appends a built-in profile's guide", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "prime", "--profile", "go"), e.stderr.String())
		assert.True(t, strings.HasPrefix(e.stdout.String(), guide.Prime()))
		assert.Contains(t, e.stdout.String(), "--- reviewer profile: go ---")
	})
	t.Run("a user's profile replaces the built-in", func(t *testing.T) {
		e := newReviewApp(t)
		require.NoError(t, os.MkdirAll(e.paths.ReviewProfiles, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(e.paths.ReviewProfiles, "security.md"), []byte("---\ndescription: ours\n---\nOur security guide.\n"), 0o600))
		require.Equal(t, exitOK, e.run(t, "", "review", "prime", "--profile=security"), e.stderr.String())
		assert.True(t, strings.HasSuffix(e.stdout.String(), "Our security guide.\n"))
	})
	t.Run("a profile the review doesn't run is profile_unknown, exit 2", func(t *testing.T) {
		e := newReviewApp(t)
		e.app.global.json = false
		assert.Equal(t, exitUsage, e.run(t, "", "review", "prime", "--profile", "tests"))
		assert.Empty(t, e.stdout.String())
		assert.Contains(t, e.stderr.String(), `profile "tests" isn't one of this review's profiles: security, go`)
		requireCode(t, codeProfileUnknown, e.app.runReviewPrime("tests", true))
	})
	t.Run("a profile no file defines is profile_unknown", func(t *testing.T) {
		e := newReviewApp(t)
		rec := record.Record{Repo: reviewedRepo, Ref: e.commit, Profiles: []record.Profile{{Name: "cobol"}}}
		require.NoError(t, record.Write(record.Path(e.paths.State, e.ws.ID), &rec))
		requireCode(t, codeProfileUnknown, e.app.runReviewPrime("cobol", true))
	})
	t.Run("a broken profile file is config_invalid", func(t *testing.T) {
		e := newReviewApp(t)
		require.NoError(t, os.MkdirAll(e.paths.ReviewProfiles, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(e.paths.ReviewProfiles, "go.md"), []byte("no front matter"), 0o600))
		requireCode(t, codeConfigInvalid, e.app.runReviewPrime("go", true))
	})
	t.Run("outside a review workspace is review_not_found, exit 3", func(t *testing.T) {
		e := newReviewApp(t)
		e.app.getwd = func() (string, error) { return t.TempDir(), nil }
		assert.Equal(t, exitNotFound, e.run(t, "", "review", "prime"))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("refuses --json and arguments", func(t *testing.T) {
		e := newReviewApp(t)
		assert.Equal(t, exitUsage, e.run(t, "", "--json", "review", "prime"))
		assert.Equal(t, exitUsage, e.run(t, "", "review", "prime", "go"))
	})
}

func TestApp_runReviewDescribe(t *testing.T) {
	t.Run("prints the document shape and the index", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "describe"), e.stderr.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.Equal(t, guide.DocumentShape(), out["summary"])
		assert.NotEmpty(t, out["index"])
	})
	t.Run("--list prints the index alone", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "describe", "--list"), e.stderr.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.NotContains(t, out, "summary")
		assert.Contains(t, e.stdout.String(), "review-profile")
	})
	t.Run("--lens opens entries once each, in the order given", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, "", "review", "describe", "--lens=review-ref, priority,review-ref"), e.stderr.String())
		var out struct {
			Entries []struct {
				Name string `json:"name"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		require.Len(t, out.Entries, 2)
		assert.Equal(t, "review-ref", out.Entries[0].Name)
		assert.Equal(t, "comments.priority", out.Entries[1].Name)
	})
	t.Run("an unknown lens is a usage error", func(t *testing.T) {
		e := newReviewApp(t)
		assert.Equal(t, exitUsage, e.run(t, "", "review", "describe", "--lens=nope"))
		assert.Contains(t, e.stderr.String(), `unknown lens "nope"`)
		assert.Empty(t, e.stdout.String())
	})
	t.Run("an empty name is a usage error", func(t *testing.T) {
		e := newReviewApp(t)
		requireUsageError(t, e.app.runReviewDescribe("priority,", true, false))
	})
	t.Run("--lens and --list together are a usage error", func(t *testing.T) {
		e := newReviewApp(t)
		requireUsageError(t, e.app.runReviewDescribe("priority", true, true))
	})
	t.Run("outside a review workspace is review_not_found", func(t *testing.T) {
		e := newReviewApp(t)
		e.app.getwd = func() (string, error) { return t.TempDir(), nil }
		requireCode(t, codeReviewNotFound, e.app.runReviewDescribe("", false, false))
	})
}

func TestResolveLenses(t *testing.T) {
	registry, err := guide.NewRegistry()
	require.NoError(t, err)
	t.Run("an ambiguous name lists its candidates", func(t *testing.T) {
		_, err := resolveLenses(registry, "code")
		requireUsageError(t, err)
		assert.Equal(t, map[string]any{"candidates": []string{"comments.code", "comments.suggestions.code"}}, asCLIError(err).details)
	})
}

func TestApp_runReviewSubmit(t *testing.T) {
	t.Run("stores a review read from stdin, filling in ref", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, reviewDoc("security"), "--json", "review", "submit", "-"), e.stderr.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		want := filepath.Join(e.paths.Reviews, "github.com", "bobcob7", "buff-bear", e.commit, "security.json")
		assert.Equal(t, want, out["path"])
		assert.Nil(t, out["superseded"])
		assert.Equal(t, e.ws.ID, out["workspace"])
		assert.Equal(t, reviewedRepo, out["repo"])
		assert.Equal(t, e.commit, out["ref"])
		assert.Equal(t, "security", out["profile"])
		assert.Equal(t, true, out["valid"])
		assert.Equal(t, []any{}, out["warnings"])
		stored, err := os.ReadFile(want)
		require.NoError(t, err)
		assert.Contains(t, string(stored), `"ref":"`+e.commit+`"`)
	})
	t.Run("a resubmission supersedes the earlier one", func(t *testing.T) {
		e := newReviewApp(t)
		path := filepath.Join(t.TempDir(), "review.json")
		require.NoError(t, os.WriteFile(path, []byte(reviewDoc("go")), 0o600))
		require.Equal(t, exitOK, e.run(t, "", "review", "submit", path), e.stderr.String())
		e.stdout.Reset()
		require.Equal(t, exitOK, e.run(t, "", "--json", "review", "submit", path), e.stderr.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.Equal(t, filepath.Join(e.paths.Reviews, "github.com", "bobcob7", "buff-bear", e.commit, "go.1.json"), out["superseded"])
	})
	t.Run("as text, names the stored file", func(t *testing.T) {
		e := newReviewApp(t)
		require.Equal(t, exitOK, e.run(t, reviewDoc("go"), "review", "submit"), e.stderr.String())
		assert.Contains(t, e.stdout.String(), "stored the go review of "+reviewedRepo+" at "+e.commit[:7]+": ")
	})
	t.Run("an invalid review is review_invalid, exit 1, with diagnostics and lenses in details", func(t *testing.T) {
		e := newReviewApp(t)
		code := e.run(t, reviewDoc("tests", `"line":3`, `"line":30`), "--json", "review", "submit")
		assert.Equal(t, exitFailed, code)
		assert.Empty(t, e.stdout.String())
		var out struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					Diagnostics []struct {
						Name string `json:"name"`
					} `json:"diagnostics"`
					Lenses []string `json:"lenses"`
				} `json:"details"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(e.stderr.Bytes(), &out), e.stderr.String())
		assert.Equal(t, "review_invalid", out.Error.Code)
		names := []string{}
		for _, d := range out.Error.Details.Diagnostics {
			names = append(names, d.Name)
		}
		assert.Contains(t, names, "review-profile")
		assert.Contains(t, names, "anchor-line-out-of-range")
		assert.Contains(t, out.Error.Details.Lenses, "review-profile")
		assert.NoDirExists(t, filepath.Join(e.paths.Reviews, "github.com"))
	})
	t.Run("as text, an invalid review lists its diagnostics after the message", func(t *testing.T) {
		e := newReviewApp(t)
		assert.Equal(t, exitFailed, e.run(t, reviewDoc("tests"), "review", "submit"))
		lines := strings.Split(strings.TrimSpace(e.stderr.String()), "\n")
		require.Len(t, lines, 3, e.stderr.String())
		assert.Equal(t, "ws: the review has 1 error; revise it and resubmit", lines[0])
		assert.True(t, strings.HasPrefix(lines[1], "  error review-profile /profile: "), lines[1])
		assert.Equal(t, "  ws review describe --lens=review-profile explains them", lines[2])
	})
	t.Run("a review of a diverged checkout is review_unverifiable, exit 1", func(t *testing.T) {
		e := newReviewApp(t)
		require.NoError(t, os.WriteFile(filepath.Join(e.repo, "client.go"), []byte("edited\n"), 0o600))
		assert.Equal(t, exitFailed, e.run(t, reviewDoc("security"), "--json", "review", "submit"))
		assert.Contains(t, e.stderr.String(), `"code":"review_unverifiable"`)
		assert.Contains(t, e.stderr.String(), "anchor-worktree-diverged")
	})
	t.Run("a file it can't read is a usage error", func(t *testing.T) {
		e := newReviewApp(t)
		assert.Equal(t, exitUsage, e.run(t, "", "review", "submit", filepath.Join(t.TempDir(), "missing.json")))
	})
	t.Run("more than one path is a usage error", func(t *testing.T) {
		e := newReviewApp(t)
		assert.Equal(t, exitUsage, e.run(t, "", "review", "submit", "a", "b"))
	})
	t.Run("outside a review workspace is review_not_found, exit 3", func(t *testing.T) {
		e := newReviewApp(t)
		e.app.getwd = func() (string, error) { return t.TempDir(), nil }
		assert.Equal(t, exitNotFound, e.run(t, reviewDoc("go"), "--json", "review", "submit"))
		assert.Contains(t, e.stderr.String(), `"code":"review_not_found"`)
	})
	t.Run("a store it can't write is an internal error", func(t *testing.T) {
		e := newReviewApp(t)
		require.NoError(t, os.WriteFile(e.paths.Reviews, nil, 0o600))
		assert.Equal(t, exitFailed, e.run(t, reviewDoc("go"), "--json", "review", "submit"))
		assert.Contains(t, e.stderr.String(), `"code":"internal"`)
	})
}

// A submission is paid for on every loop of a reviewer's revise-and-submit
// cycle, so its output has a budget: a fixed cost for a clean run, and a
// cost per diagnostic.
func TestReviewSubmitStaysWithinBudget(t *testing.T) {
	const (
		cleanBudget         = 250
		perDiagnosticBudget = 80
	)
	clean := newReviewApp(t)
	require.Equal(t, exitOK, clean.run(t, reviewDoc("go"), "--json", "review", "submit"), clean.stderr.String())
	cleanTokens := len(clean.stdout.String()) / 4
	assert.LessOrEqual(t, cleanTokens, cleanBudget)
	flawed := newReviewApp(t)
	require.Equal(t, exitFailed, flawed.run(t, reviewDoc("tests", `"priority":8`, `"priority":12`), "--json", "review", "submit"))
	var out struct {
		Error struct {
			Details struct {
				Diagnostics []any `json:"diagnostics"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(flawed.stderr.Bytes(), &out))
	n := len(out.Error.Details.Diagnostics)
	require.Positive(t, n)
	perDiagnostic := len(flawed.stderr.String()) / 4 / n
	assert.LessOrEqual(t, perDiagnostic, perDiagnosticBudget, "each diagnostic costs about %d tokens", perDiagnostic)
}

func e2eLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
