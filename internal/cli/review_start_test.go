package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/exectest"
	"github.com/bobcob7/workspaces/internal/gh"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// reviewStartEnv is ws claude's test environment with a cache that has
// testCommit, a gh that knows pull request 42, and CONFIG_DIR in a temp
// directory. Creating the workspace runs Prepare, as the store does.
type reviewStartEnv struct {
	*claudeEnv

	cache *repoCacheMock
	gh    *ghClientMock
	files []string
}

func newReviewStartEnv(t *testing.T) *reviewStartEnv {
	t.Helper()
	e := &reviewStartEnv{claudeEnv: newClaudeEnv(t), cache: stubCache(), files: []string{"client.go"}}
	e.app.paths = config.NewPaths(t.TempDir())
	e.app.config = testConfig()
	e.cache.FetchFunc = func(context.Context, repo.ID) error { return nil }
	e.cache.ResolveRefFunc = func(_ context.Context, _ repo.ID, ref string) (cache.Ref, error) {
		return cache.Ref{Name: ref, Kind: cache.RefCommit, Commit: testCommit}, nil
	}
	e.cache.ChangedFilesFunc = func(context.Context, repo.ID, string, string) ([]string, error) { return e.files, nil }
	e.gh = &ghClientMock{PRViewFunc: func(context.Context, string, int) (gh.PR, error) {
		return gh.PR{Number: 42, Title: "Fix login", HeadRefOid: testCommit, HeadRefName: "feat/login", BaseRefName: "main", URL: "https://github.com/bobcob7/buff-bear/pull/42"}, nil
	}}
	e.ws.ID = "review-buff-bear-42-3f2a9c1-d8efb24f"
	e.store.CreateFunc = func(_ context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
		require.NoError(t, os.MkdirAll(filepath.Join(e.app.paths.State, e.ws.ID), 0o750))
		if opts.Prepare != nil {
			if err := opts.Prepare(&e.ws); err != nil {
				return workspace.Workspace{}, nil, err
			}
		}
		lock, ok, err := lockfile.TryExclusiveLock(e.lockPath)
		require.NoError(t, err)
		require.True(t, ok)
		return e.ws, lock, nil
	}
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
		return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
	}
	e.app.cache, e.app.gh = e.cache, e.gh
	e.app.getwd = func() (string, error) { return "/cfg/workspaces/fix-login-d8efb24f/buff-bear", nil }
	return e
}

func (e *reviewStartEnv) start(t *testing.T, opts *reviewStartOptions) error {
	t.Helper()
	if opts.spec == "" {
		opts.spec = "buff-bear"
	}
	return e.app.runReviewStart(t.Context(), opts)
}

// record is the review record start wrote.
func (e *reviewStartEnv) record(t *testing.T) record.Record {
	t.Helper()
	rec, err := record.Read(record.Path(e.app.paths.State, e.ws.ID))
	require.NoError(t, err)
	return rec
}

// claudeArgs are the arguments the review agent was started with.
func (e *reviewStartEnv) claudeArgs(t *testing.T) []string {
	t.Helper()
	require.Len(t, e.claude.StartCalls(), 1)
	return e.claude.StartCalls()[0].Opts.Args
}

// agents decodes the --agents JSON the review agent was started with.
func (e *reviewStartEnv) agents(t *testing.T) map[string]map[string]any {
	t.Helper()
	args := e.claudeArgs(t)
	require.Equal(t, "--agents", args[0])
	var agents map[string]map[string]any
	require.NoError(t, json.Unmarshal([]byte(args[1]), &agents))
	return agents
}

func TestApp_newReviewStartCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newReviewStartCmd()
	for _, name := range []string{"name", "offline", "pr", "profile", "auto", "requester"} {
		assert.NotNil(t, cmd.Flags().Lookup(name), "--%s", name)
	}
	t.Run("needs exactly one spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
		requireUsageError(t, cmd.Args(cmd, []string{"a", "b"}))
	})
}

func TestApp_runReviewStart(t *testing.T) {
	t.Run("--pr reviews the pull request's head", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		require.Len(t, e.gh.PRViewCalls(), 1)
		assert.Equal(t, "github.com/bobcob7/buff-bear", e.gh.PRViewCalls()[0].RepoMoqParam)
		assert.Equal(t, 42, e.gh.PRViewCalls()[0].Number)
		assert.Equal(t, testCommit, e.cache.ResolveRefCalls()[0].Ref)
		changed := e.cache.ChangedFilesCalls()[0]
		assert.Equal(t, "main", changed.Base)
		assert.Equal(t, testCommit, changed.Head)
		create := e.store.CreateCalls()[0].Opts
		assert.Equal(t, "review-buff-bear-42-3f2a9c1", create.Name)
		assert.True(t, create.Offline, "the cache is already fetched")
		assert.True(t, create.Hidden, "review workspaces are hidden")
		require.Len(t, create.Specs, 1)
		assert.Equal(t, "github.com/bobcob7/buff-bear@"+testCommit, create.Specs[0].Raw)
		rec := e.record(t)
		assert.Equal(t, reviewedRepo, rec.Repo)
		assert.Equal(t, testCommit, rec.Ref)
		assert.Equal(t, &record.PR{Number: 42, URL: "https://github.com/bobcob7/buff-bear/pull/42", Base: "main", HeadBranch: "feat/login"}, rec.PR)
		assert.Equal(t, testNow(), rec.StartedAt)
	})
	t.Run("without --pr reviews the spec's ref", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{spec: "buff-bear@main"}))
		assert.Empty(t, e.gh.PRViewCalls())
		assert.Equal(t, "main", e.cache.ResolveRefCalls()[0].Ref)
		assert.Empty(t, e.cache.ChangedFilesCalls()[0].Base)
		assert.Equal(t, "review-buff-bear-3f2a9c1", e.store.CreateCalls()[0].Opts.Name)
		assert.Nil(t, e.record(t).PR)
	})
	t.Run("--pr refuses a spec with a ref", func(t *testing.T) {
		e := newReviewStartEnv(t)
		requireUsageError(t, e.start(t, &reviewStartOptions{pr: 42, spec: "buff-bear@main"}))
		assert.Empty(t, e.gh.PRViewCalls())
	})
	t.Run("--name names the workspace", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, name: "mine"}))
		assert.Equal(t, "mine", e.store.CreateCalls()[0].Opts.Name)
	})
	t.Run("fetches the cache, or adds an uncached repo", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Len(t, e.cache.FetchCalls(), 1)
		e = newReviewStartEnv(t)
		e.cache.ResolveFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{ID: testID(), Upstream: testID().UpstreamURL()}, nil
		}
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Len(t, e.cache.AddCalls(), 1)
		assert.Empty(t, e.cache.FetchCalls())
	})
	t.Run("--offline only looks the repo up", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, offline: true}))
		assert.Len(t, e.cache.LookupCalls(), 1)
		assert.Empty(t, e.cache.ResolveCalls())
		assert.Empty(t, e.cache.FetchCalls())
	})
	t.Run("gh failures have their codes, and nothing is created", func(t *testing.T) {
		for err, code := range map[error]errorCode{
			gh.ErrNotInstalled: codeGhMissing,
			gh.ErrAuth:         codeGhAuth,
			gh.ErrPRNotFound:   codePRNotFound,
		} {
			e := newReviewStartEnv(t)
			e.gh.PRViewFunc = func(context.Context, string, int) (gh.PR, error) { return gh.PR{}, err }
			requireCode(t, code, e.start(t, &reviewStartOptions{pr: 42}))
			assert.Empty(t, e.store.CreateCalls())
		}
	})
	t.Run("a missing ref is ref_not_found", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.cache.ResolveRefFunc = func(context.Context, repo.ID, string) (cache.Ref, error) { return cache.Ref{}, cache.ErrRefNotFound }
		requireCode(t, codeRefNotFound, e.start(t, &reviewStartOptions{spec: "buff-bear@nope"}))
	})
	t.Run("checks claude before anything else", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.claude.CheckFunc = func(context.Context) error { return claude.ErrNotInstalled }
		requireCode(t, codeClaudeMissing, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Empty(t, e.gh.PRViewCalls())
	})
	t.Run("prints the result, the record, and the wait command", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.app.global.json = true
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		var out reviewStartJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.Equal(t, e.ws.ID, out.ID)
		assert.Equal(t, "e8cdf5d8", out.Session)
		assert.Equal(t, "ws review wait --json "+e.ws.ID, out.WaitCommand)
		assert.Equal(t, 1, out.Review.Version)
		assert.Equal(t, "fix-login-d8efb24f", out.Review.Requester)
		assert.Contains(t, e.stdout.String(), `"started_at":"2026-10-19T12:00:00Z"`)
		assert.Contains(t, e.stdout.String(), `"warnings":[]`)
	})
	t.Run("text output names the wait command", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Equal(t, e.ws.ID+"  e8cdf5d8  /cfg/workspaces/x\nwait for the report: ws review wait "+e.ws.ID+"\n", e.stdout.String())
	})
	t.Run("a failed start removes the workspace", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{}, &claude.CommandError{Args: []string{"--bg"}, Err: errTest}
		}
		requireCode(t, codeClaudeFailed, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Len(t, e.store.RemoveCalls(), 1)
	})
}

func TestApp_reviewRequester(t *testing.T) {
	t.Run("defaults to the workspace ws runs in", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Equal(t, "fix-login-d8efb24f", e.record(t).Requester)
		assert.Equal(t, "/cfg/workspaces/fix-login-d8efb24f/buff-bear", e.store.ContainingCalls()[0].Dir)
	})
	t.Run("is the review's parent", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Equal(t, "fix-login-d8efb24f", e.store.CreateCalls()[0].Opts.Parent)
	})
	t.Run("keeps the review's run in the store", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		run, err := store.New(e.app.paths.Reviews).FindRun(e.ws.ID)
		require.NoError(t, err)
		assert.Equal(t, "fix-login-d8efb24f", run.Record.Requester)
	})
	t.Run("drops the run when the agent doesn't start", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.claude.StartFunc = func(context.Context, claude.StartOptions) (claude.Started, error) { return claude.Started{}, errTest }
		require.Error(t, e.start(t, &reviewStartOptions{pr: 42}))
		_, err := store.New(e.app.paths.Reviews).FindRun(e.ws.ID)
		require.ErrorIs(t, err, store.ErrNoRun)
	})
	t.Run("none makes a review with no parent", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, requester: "none"}))
		assert.Empty(t, e.store.CreateCalls()[0].Opts.Parent)
	})
	t.Run("is none outside a workspace", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
		got, err := e.app.reviewRequester("")
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("none is none", func(t *testing.T) {
		e := newReviewStartEnv(t)
		got, err := e.app.reviewRequester("none")
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Empty(t, e.store.ContainingCalls())
	})
	t.Run("an id names the workspace", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: arg + "-d8efb24f"}, nil
		}
		got, err := e.app.reviewRequester("other")
		require.NoError(t, err)
		assert.Equal(t, "other-d8efb24f", got)
	})
	t.Run("an id that names no workspace is workspace_not_found", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
		_, err := e.app.reviewRequester("nope")
		requireCode(t, codeWorkspaceNotFound, err)
	})
	t.Run("a failing lookup is an error", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, errTest }
		_, err := e.app.reviewRequester("")
		assert.ErrorIs(t, err, errTest)
	})
}

func TestApp_chooseProfiles(t *testing.T) {
	t.Run("always profiles and auto ones matching the files", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.files = []string{"cmd/main.go", "web/app.tsx"}
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Equal(t, []record.Profile{
			{Name: "architecture"},
			{Name: "claims"},
			{Name: "go", Auto: true},
			{Name: "security"},
			{Name: "tests"},
			{Name: "typescript", Auto: true},
		}, e.record(t).Profiles)
	})
	t.Run("config.toml sets when and models", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.app.config.Review.Profiles = map[string]config.ReviewProfile{
			"architecture": {When: "off"}, "claims": {When: "off"}, "tests": {When: "off"},
			"security": {Model: "opus"},
		}
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		assert.Equal(t, []record.Profile{{Name: "go", Auto: true}, {Name: "security", Model: "opus"}}, e.record(t).Profiles)
	})
	t.Run("--profile and --auto replace them", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, profiles: []string{"security:opus"}, auto: []string{"go:haiku", "typescript"}}))
		assert.Equal(t, []record.Profile{{Name: "security", Model: "opus"}, {Name: "go", Model: "haiku", Auto: true}}, e.record(t).Profiles)
	})
	t.Run("a user profile joins", func(t *testing.T) {
		e := newReviewStartEnv(t)
		dir := e.app.paths.ReviewProfiles
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "docs.md"), []byte("---\ndescription: Docs\nmatch: [\"*.md\"]\n---\nRead the docs.\n"), 0o600))
		e.files = []string{"README.md"}
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, auto: []string{"docs"}}))
		assert.Equal(t, []record.Profile{{Name: "docs", Auto: true}}, e.record(t).Profiles)
		assert.Contains(t, e.agents(t)["review-docs"]["prompt"], "Read the docs.")
	})
	t.Run("an unknown profile is profile_unknown", func(t *testing.T) {
		e := newReviewStartEnv(t)
		requireCode(t, codeProfileUnknown, e.start(t, &reviewStartOptions{pr: 42, profiles: []string{"nope"}}))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("a malformed choice is a usage error", func(t *testing.T) {
		e := newReviewStartEnv(t)
		requireUsageError(t, e.start(t, &reviewStartOptions{pr: 42, profiles: []string{"go:"}}))
	})
	t.Run("no profile left is a usage error", func(t *testing.T) {
		e := newReviewStartEnv(t)
		requireUsageError(t, e.start(t, &reviewStartOptions{pr: 42, auto: []string{"typescript"}}))
	})
	t.Run("listing the files failing is git_failed", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.cache.ChangedFilesFunc = func(context.Context, repo.ID, string, string) ([]string, error) {
			return nil, &git.CommandError{Args: []string{"diff"}, Err: errTest}
		}
		requireCode(t, codeGitFailed, e.start(t, &reviewStartOptions{pr: 42}))
	})
}

func TestApp_reviewAgentArgs(t *testing.T) {
	t.Run("defines a subagent per profile, model only when set", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, profiles: []string{"security:opus", "tests"}}))
		agents := e.agents(t)
		require.Len(t, agents, 2)
		security := agents["review-security"]
		assert.Equal(t, "opus", security["model"])
		assert.Equal(t, []any{"Read", "Grep", "Glob", "Bash"}, security["tools"])
		assert.Contains(t, security["description"], "security profile")
		prompt, ok := security["prompt"].(string)
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(prompt, "ws review checks a review document"), "starts with ws review prime's guide")
		assert.Contains(t, prompt, "as the security profile")
		assert.NotContains(t, agents["review-tests"], "model")
	})
	t.Run("the claude options go between --agents and the prompt, with the focus in it", func(t *testing.T) {
		e := newReviewStartEnv(t)
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42, claudeArgs: []string{"--model", "sonnet"}, focus: "Check the retry loop."}))
		args := e.claudeArgs(t)
		require.Len(t, args, 5)
		assert.Equal(t, []string{"--model", "sonnet"}, args[2:4])
		prompt := args[4]
		assert.Contains(t, prompt, "- review-architecture\n")
		assert.Contains(t, prompt, "## From the requester\n\nCheck the retry loop.\n")
		assert.Contains(t, e.agents(t)["review-security"]["prompt"], "From the requester: Check the retry loop.")
	})
	t.Run("refuses --agents among the claude options", func(t *testing.T) {
		e := newReviewStartEnv(t)
		requireUsageError(t, e.start(t, &reviewStartOptions{pr: 42, claudeArgs: []string{"--agents={}"}}))
		assert.Empty(t, e.store.CreateCalls())
	})
}

func TestSplitFocus(t *testing.T) {
	t.Run("the last argument is the focus", func(t *testing.T) {
		args, focus := splitFocus([]string{"--model=opus", "look at retries"})
		assert.Equal(t, []string{"--model=opus"}, args)
		assert.Equal(t, "look at retries", focus)
	})
	t.Run("an option last means no focus", func(t *testing.T) {
		args, focus := splitFocus([]string{"--verbose"})
		assert.Equal(t, []string{"--verbose"}, args)
		assert.Empty(t, focus)
	})
	t.Run("nothing after --", func(t *testing.T) {
		args, focus := splitFocus(nil)
		assert.Empty(t, args)
		assert.Empty(t, focus)
	})
}

func TestReviewStart_endToEnd(t *testing.T) {
	t.Run("starts a review of a pull request with fake gh and claude", func(t *testing.T) {
		e := newClaudeE2E(t, `printf 'backgrounded · e8cdf5d8 · review\n'`)
		e.up.Git(t, e.up.Path("o", "n"), "update-ref", "refs/heads/feat/login", "refs/heads/main")
		head := e.up.Commit(t, "o", "n", "refs/heads/feat/login")
		ghPath := filepath.Join(e.scratch, "gh")
		exectest.WriteScript(t, ghPath, "#!/bin/sh\n"+
			`echo "$@" > "`+e.scratch+`/gh-args"`+"\n"+
			`printf '{"number":7,"title":"Fix login","headRefOid":"`+head+`","headRefName":"feat/login","baseRefName":"main","isCrossRepository":false,"url":"https://example.test/o/n/pull/7","body":""}'`+"\n")
		e.app.gh = gh.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), ghPath, os.Environ())
		code := e.app.Run(t.Context(), []string{"--json", "review", "start", "--pr", "7", "--requester", "none", "--profile", "security:opus", "--auto", "go", "n", "--", "--model=sonnet", "Check cancellation."})
		require.Equal(t, 0, code, "stderr: %s", e.stderr.String())
		var out reviewStartJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		assert.True(t, strings.HasPrefix(out.ID, "review-n-7-"+head[:7]+"-"), out.ID)
		assert.Equal(t, "ws review wait --json "+out.ID, out.WaitCommand)
		assert.Equal(t, head, out.Review.Ref)
		assert.Equal(t, []record.Profile{{Name: "security", Model: "opus"}}, out.Review.Profiles, "go matched no file")
		assert.Equal(t, 7, out.Review.PR.Number)
		ghArgs, err := os.ReadFile(filepath.Join(e.scratch, "gh-args"))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(ghArgs), "pr view 7 --repo "+gittest.Host+"/o/n --json "), string(ghArgs))
		claudeOut, err := os.ReadFile(filepath.Join(e.scratch, "out"))
		require.NoError(t, err)
		lines := strings.Split(string(claudeOut), "\n")
		require.Equal(t, "--agents", lines[5])
		var agents map[string]map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[6]), &agents))
		assert.Equal(t, "opus", agents["review-security"]["model"])
		assert.Equal(t, "--model=sonnet", lines[7])
		assert.Contains(t, string(claudeOut), "## From the requester\n\nCheck cancellation.\n")
		rec, err := record.Read(record.Path(e.paths.State, out.ID))
		require.NoError(t, err)
		assert.Equal(t, out.Review, rec)
	})
}
