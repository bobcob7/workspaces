package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newCacheApp returns a test App whose loaded cache is m, as withConfig
// would leave it.
func newCacheApp(m *repoCacheMock) (app *App, stdout, stderr *bytes.Buffer) {
	app, stdout, stderr = newTestApp()
	s := stores{cache: m, workspaces: stubWorkspaces(), lifetime: stubLifetime()}
	app.cache, app.workspaces, app.lifetime = s.cache, s.workspaces, s.lifetime
	app.newStores = func(config.Paths, config.Config) stores { return s }
	return app, stdout, stderr
}

// parsed parses specs for tests.
func parsed(t *testing.T, args ...string) []repo.Spec {
	t.Helper()
	specs := parseSpecs(args)
	require.Len(t, specs, len(args))
	return specs
}

func otherID() repo.ID {
	return repo.ID{Host: "github.com", Owner: "bobcob7", Name: "api"}
}

func TestApp_newCacheCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCacheCmd()
	t.Run("has every operation", func(t *testing.T) {
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		assert.ElementsMatch(t, []string{"add", "update", "list", "search", "refs", "resolve", "path", "remove", "prune"}, names)
	})
	t.Run("refuses an unknown operation", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"frobnicate"}))
	})
	t.Run("an unknown operation exits 2 with nothing on stdout", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		assert.Equal(t, exitUsage, app.Run(t.Context(), []string{"cache", "frobnicate"}))
		assert.Empty(t, stdout.String())
	})
	t.Run("no operation prints help", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		assert.Equal(t, exitOK, app.Run(t.Context(), []string{"cache"}))
		assert.Contains(t, stdout.String(), "Available Commands:")
	})
}

func TestApp_newCacheAddCmd(t *testing.T) {
	t.Run("needs a spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheAddCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("rejects a bad spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheAddCmd()
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, []string{"a//b"}))
	})
	t.Run("adds", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCacheAddCmd()
		require.NoError(t, cmd.RunE(cmd, []string{"buff-bear"}))
		assert.Len(t, m.AddCalls(), 1)
	})
}

func TestApp_newCacheUpdateCmd(t *testing.T) {
	parse := func(t *testing.T, app *App, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newCacheUpdateCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("accepts specs", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app, "buff-bear")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("accepts --all", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app, "--all")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects neither", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app)
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects both", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app, "--all", "buff-bear")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects a bad spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app, "a//b")
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, args))
	})
	t.Run("updates", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd, args := parse(t, app, "--all")
		require.NoError(t, cmd.RunE(cmd, args))
		assert.Len(t, m.FetchEachCalls(), 1)
	})
}

func TestApp_newCacheListCmd(t *testing.T) {
	t.Run("takes no arguments", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheListCmd()
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("lists", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCacheListCmd()
		require.NoError(t, cmd.RunE(cmd, nil))
		assert.Len(t, m.ListCalls(), 1)
	})
}

func TestApp_newCacheSearchCmd(t *testing.T) {
	parse := func(t *testing.T, app *App, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newCacheSearchCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("has --limit and --refs", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheSearchCmd()
		assertFlag(t, cmd, "limit", "20")
		assertFlag(t, cmd, "refs", "false")
	})
	t.Run("needs exactly one query", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app)
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects a limit below 1", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd, args := parse(t, app, "--limit", "0", "bear")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("passes the query and flags", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd, args := parse(t, app, "--limit", "5", "--refs", "bear")
		require.NoError(t, cmd.RunE(cmd, args))
		require.Len(t, m.SearchCalls(), 1)
		call := m.SearchCalls()[0]
		assert.Equal(t, "bear", call.Query)
		assert.Equal(t, 5, call.Limit)
		assert.True(t, call.Refs)
	})
}

func TestApp_newCacheResolveCmd(t *testing.T) {
	t.Run("needs a spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheResolveCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("resolves", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCacheResolveCmd()
		require.NoError(t, cmd.RunE(cmd, []string{"buff-bear", "api"}))
		assert.Len(t, m.ResolveCalls(), 2)
	})
}

func TestApp_newCachePathCmd(t *testing.T) {
	t.Run("needs exactly one spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCachePathCmd()
		requireUsageError(t, cmd.Args(cmd, []string{"a", "b"}))
	})
	t.Run("looks up", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCachePathCmd()
		require.NoError(t, cmd.RunE(cmd, []string{"buff-bear"}))
		assert.Len(t, m.LookupCalls(), 1)
	})
}

func TestApp_newCacheRefsCmd(t *testing.T) {
	t.Run("needs exactly one spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheRefsCmd()
		requireUsageError(t, cmd.Args(cmd, []string{"a", "b"}))
	})
	t.Run("lists the looked-up repo's refs", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCacheRefsCmd()
		require.NoError(t, cmd.RunE(cmd, []string{"buff-bear"}))
		require.Len(t, m.RefsCalls(), 1)
		assert.Equal(t, testID(), m.RefsCalls()[0].ID)
	})
}

func TestApp_newCacheRemoveCmd(t *testing.T) {
	t.Run("needs a spec", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newCacheRemoveCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("removes", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCacheRemoveCmd()
		require.NoError(t, cmd.RunE(cmd, []string{"buff-bear"}))
		assert.Len(t, m.RemoveCalls(), 1)
	})
}

func TestApp_newCachePruneCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCachePruneCmd()
	t.Run("has -n, --dry-run", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
		assert.NotNil(t, cmd.Flags().ShorthandLookup("n"))
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("prunes", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		cmd := app.newCachePruneCmd()
		require.NoError(t, cmd.ParseFlags([]string{"-n"}))
		require.NoError(t, cmd.RunE(cmd, nil))
		require.Len(t, m.PruneCalls(), 1)
		assert.True(t, m.PruneCalls()[0].DryRun)
	})
}

// workspacesOf is the workspace store mock newCacheApp installed.
func workspacesOf(t *testing.T, app *App) *workspaceStoreMock {
	t.Helper()
	m, ok := app.workspaces.(*workspaceStoreMock)
	require.True(t, ok)
	return m
}

func TestApp_cachePrune(t *testing.T) {
	pruned := []cache.PrunedRef{{ID: testID(), Commit: "abc"}}
	pruning := func() *repoCacheMock {
		m := stubCache()
		m.PruneFunc = func(context.Context, func(repo.ID, string) (bool, error), bool) ([]cache.PrunedRef, error) {
			return pruned, nil
		}
		return m
	}
	t.Run("asks the workspaces whether a commit is used", func(t *testing.T) {
		m := pruning()
		app, _, _ := newCacheApp(m)
		workspacesOf(t, app).UsesCommitFunc = func(id, commit string) (bool, error) {
			return id == testID().String() && commit == "used", nil
		}
		require.NoError(t, app.cachePrune(t.Context(), false))
		keep := m.PruneCalls()[0].Keep
		used, err := keep(testID(), "used")
		require.NoError(t, err)
		assert.True(t, used)
		used, err = keep(testID(), "other")
		require.NoError(t, err)
		assert.False(t, used)
	})
	t.Run("text", func(t *testing.T) {
		app, stdout, _ := newCacheApp(pruning())
		require.NoError(t, app.cachePrune(t.Context(), false))
		assert.Equal(t, "pruned  github.com/bobcob7/buff-bear  abc\n", stdout.String())
	})
	t.Run("text for a dry run", func(t *testing.T) {
		app, stdout, _ := newCacheApp(pruning())
		require.NoError(t, app.cachePrune(t.Context(), true))
		assert.Equal(t, "would prune  github.com/bobcob7/buff-bear  abc\n", stdout.String())
	})
	t.Run("JSON", func(t *testing.T) {
		app, stdout, _ := newCacheApp(pruning())
		app.global.json = true
		require.NoError(t, app.cachePrune(t.Context(), false))
		assert.JSONEq(t, `{"refs":[{"id":"github.com/bobcob7/buff-bear","commit":"abc"}],"reviews":[],"warnings":[]}`, stdout.String())
	})
	t.Run("nothing to prune is an empty list", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cachePrune(t.Context(), false))
		assert.JSONEq(t, `{"refs":[],"reviews":[],"warnings":[]}`, stdout.String())
	})
	t.Run("a workspace check failure stops it", func(t *testing.T) {
		m := pruning()
		app, _, _ := newCacheApp(m)
		workspacesOf(t, app).UsesCommitFunc = func(string, string) (bool, error) { return false, errTest }
		require.NoError(t, app.cachePrune(t.Context(), false))
		_, err := m.PruneCalls()[0].Keep(testID(), "abc")
		require.ErrorIs(t, err, errTest)
		assert.ErrorContains(t, err, "not pruning")
	})
	t.Run("prune failure gets a code", func(t *testing.T) {
		m := stubCache()
		m.PruneFunc = func(context.Context, func(repo.ID, string) (bool, error), bool) ([]cache.PrunedRef, error) {
			return nil, &git.CommandError{Args: []string{"update-ref"}, Err: errTest}
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeGitFailed, app.cachePrune(t.Context(), false))
	})
	reviewsOf := func(t *testing.T, app *App) (kept, gone string) {
		t.Helper()
		app.paths = config.NewPaths(t.TempDir())
		other, err := repo.NewID("github.com", "bobcob7", "gone")
		require.NoError(t, err)
		reviews := store.New(app.paths.Reviews)
		for _, id := range []repo.ID{testID(), other} {
			_, err := reviews.Submit(t.Context(), id, testCommit, "go", []byte(`{}`))
			require.NoError(t, err)
		}
		return reviews.Dir(testID(), testCommit), filepath.Join(app.paths.Reviews, "github.com", "bobcob7", "gone")
	}
	t.Run("removes the reviews of repos no longer cached", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		kept, gone := reviewsOf(t, app)
		app.global.json = true
		require.NoError(t, app.cachePrune(t.Context(), false))
		assert.JSONEq(t, `{"refs":[],"reviews":[{"id":"github.com/bobcob7/gone","path":`+strconv.Quote(gone)+`}],"warnings":[]}`, stdout.String())
		assert.NoDirExists(t, gone)
		assert.DirExists(t, kept)
	})
	t.Run("a dry run keeps reviews", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		_, gone := reviewsOf(t, app)
		require.NoError(t, app.cachePrune(t.Context(), true))
		assert.Equal(t, "would prune reviews  github.com/bobcob7/gone  "+gone+"\n", stdout.String())
		assert.DirExists(t, gone)
	})
	t.Run("listing the cache failing stops pruning reviews", func(t *testing.T) {
		m := stubCache()
		m.IDsFunc = func() ([]repo.ID, error) { return nil, errTest }
		app, _, _ := newCacheApp(m)
		_, gone := reviewsOf(t, app)
		require.ErrorIs(t, app.cachePrune(t.Context(), false), errTest)
		assert.DirExists(t, gone)
	})
}

func TestParseSpecs(t *testing.T) {
	t.Run("parses each argument", func(t *testing.T) {
		specs := parseSpecs([]string{"buff-bear@main", "bobcob7/api"})
		require.Len(t, specs, 2)
		assert.Equal(t, "main", specs[0].Ref)
		assert.Equal(t, repo.KindOwnerName, specs[1].Kind)
	})
	t.Run("skips what doesn't parse", func(t *testing.T) {
		assert.Empty(t, parseSpecs([]string{"a//b"}))
	})
}

func TestApp_cacheAdd(t *testing.T) {
	t.Run("prints each id", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		require.NoError(t, app.cacheAdd(t.Context(), parsed(t, "buff-bear")))
		assert.Equal(t, "github.com/bobcob7/buff-bear\n", stdout.String())
	})
	t.Run("adds the resolved repo from its upstream", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		require.NoError(t, app.cacheAdd(t.Context(), parsed(t, "buff-bear")))
		require.Len(t, m.AddCalls(), 1)
		assert.Equal(t, testID(), m.AddCalls()[0].ID)
		assert.Equal(t, testID().UpstreamURL(), m.AddCalls()[0].Upstream)
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cacheAdd(t.Context(), parsed(t, "buff-bear")))
		assert.JSONEq(t, `{"repos":[{"id":"github.com/bobcob7/buff-bear","path":"/cfg/cache/repos/github.com/bobcob7/buff-bear.git","upstream":"https://github.com/bobcob7/buff-bear.git","added":true,"error":null}],"warnings":[]}`, stdout.String())
	})
	t.Run("resolution failure", func(t *testing.T) {
		m := stubCache()
		m.ResolveFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{}, cache.ErrRepoNotFound
		}
		app, stdout, _ := newCacheApp(m)
		requireCode(t, codeRepoNotFound, app.cacheAdd(t.Context(), parsed(t, "nope")))
		assert.Empty(t, stdout.String())
	})
	t.Run("resolves every spec before cloning", func(t *testing.T) {
		m := stubCache()
		m.ResolveFunc = func(_ context.Context, sp *repo.Spec) (cache.Resolution, error) {
			if sp.Name == "nope" {
				return cache.Resolution{}, cache.ErrRepoNotFound
			}
			return cache.Resolution{ID: testID(), Upstream: testID().UpstreamURL()}, nil
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeRepoNotFound, app.cacheAdd(t.Context(), parsed(t, "buff-bear", "nope")))
		assert.Empty(t, m.AddCalls())
	})
	t.Run("a failed clone doesn't stop the others", func(t *testing.T) {
		m := stubCache()
		m.AddFunc = func(_ context.Context, id repo.ID, _ string) (bool, error) {
			if id == testID() {
				return false, cache.ErrCloneFailed
			}
			return true, nil
		}
		m.ResolveFunc = func(_ context.Context, sp *repo.Spec) (cache.Resolution, error) {
			if sp.Name == "api" {
				return cache.Resolution{ID: otherID(), Upstream: otherID().UpstreamURL()}, nil
			}
			return cache.Resolution{ID: testID(), Upstream: testID().UpstreamURL()}, nil
		}
		app, stdout, stderr := newCacheApp(m)
		requireCode(t, codeCloneFailed, app.cacheAdd(t.Context(), parsed(t, "buff-bear", "api")))
		assert.Len(t, m.AddCalls(), 2)
		assert.Equal(t, "github.com/bobcob7/api\n", stdout.String())
		assert.Contains(t, stderr.String(), "clone failed")
	})
}

func TestApp_cacheUpdate(t *testing.T) {
	t.Run("fetches every target", func(t *testing.T) {
		m := stubCache()
		m.IDsFunc = func() ([]repo.ID, error) { return []repo.ID{testID(), otherID()}, nil }
		app, stdout, _ := newCacheApp(m)
		require.NoError(t, app.cacheUpdate(t.Context(), nil, true))
		require.Len(t, m.FetchEachCalls(), 1)
		assert.Equal(t, []repo.ID{testID(), otherID()}, m.FetchEachCalls()[0].Ids)
		assert.Equal(t, "updated  github.com/bobcob7/buff-bear\nupdated  github.com/bobcob7/api\n", stdout.String())
	})
	t.Run("keeps going after a failure and exits with the first", func(t *testing.T) {
		m := stubCache()
		m.IDsFunc = func() ([]repo.ID, error) { return []repo.ID{testID(), otherID()}, nil }
		m.FetchEachFunc = func(context.Context, []repo.ID) []error {
			return []error{fmt.Errorf("%w: offline", cache.ErrFetchFailed), nil}
		}
		app, stdout, stderr := newCacheApp(m)
		err := app.cacheUpdate(t.Context(), nil, true)
		requireCode(t, codeFetchFailed, err)
		ce, _ := err.(*cliError) //nolint:errorlint // requireCode checked the type.
		assert.True(t, ce.reported)
		assert.Equal(t, "updated  github.com/bobcob7/api\n", stdout.String())
		assert.Contains(t, stderr.String(), "offline")
	})
	t.Run("json reports each repo", func(t *testing.T) {
		m := stubCache()
		m.FetchEachFunc = func(context.Context, []repo.ID) []error {
			return []error{fmt.Errorf("%w: offline", cache.ErrFetchFailed)}
		}
		app, stdout, _ := newCacheApp(m)
		app.global.json = true
		requireCode(t, codeFetchFailed, app.cacheUpdate(t.Context(), nil, true))
		assert.JSONEq(t, `{"repos":[{"id":"github.com/bobcob7/buff-bear","error":{"code":"fetch_failed","message":"fetch failed: offline"}}],"warnings":[]}`, stdout.String())
	})
	t.Run("target failure", func(t *testing.T) {
		m := stubCache()
		m.IDsFunc = func() ([]repo.ID, error) { return nil, errTest }
		app, _, _ := newCacheApp(m)
		assert.ErrorIs(t, app.cacheUpdate(t.Context(), nil, true), errTest)
	})
}

func TestApp_updateTargets(t *testing.T) {
	t.Run("all cached repos", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		ids, err := app.updateTargets(t.Context(), nil, true)
		require.NoError(t, err)
		assert.Equal(t, []repo.ID{testID()}, ids)
	})
	t.Run("looks up specs without probing", func(t *testing.T) {
		m := stubCache()
		app, _, _ := newCacheApp(m)
		ids, err := app.updateTargets(t.Context(), parsed(t, "buff-bear"), false)
		require.NoError(t, err)
		assert.Equal(t, []repo.ID{testID()}, ids)
		assert.Len(t, m.LookupCalls(), 1)
		assert.Empty(t, m.ResolveCalls())
	})
	t.Run("uncached spec", func(t *testing.T) {
		m := stubCache()
		m.LookupFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{}, cache.ErrRepoNotFound
		}
		app, _, _ := newCacheApp(m)
		_, err := app.updateTargets(t.Context(), parsed(t, "nope"), false)
		requireCode(t, codeRepoNotFound, err)
	})
	t.Run("listing failure", func(t *testing.T) {
		m := stubCache()
		m.IDsFunc = func() ([]repo.ID, error) { return nil, errTest }
		app, _, _ := newCacheApp(m)
		_, err := app.updateTargets(t.Context(), nil, true)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestItemResults_add(t *testing.T) {
	t.Run("success has no error and keeps the text", func(t *testing.T) {
		var items itemResults
		assert.Nil(t, items.add("done", nil))
		assert.Equal(t, []itemLine{{text: "done"}}, items.lines)
		assert.Nil(t, items.first)
	})
	t.Run("failure gets its error code", func(t *testing.T) {
		var items itemResults
		got := items.add("done", fmt.Errorf("%w: offline", cache.ErrFetchFailed))
		assert.Equal(t, &itemErrorJSON{Code: codeFetchFailed, Message: "fetch failed: offline"}, got)
		requireCode(t, codeFetchFailed, items.first)
	})
	t.Run("keeps the first failure", func(t *testing.T) {
		var items itemResults
		items.add("a", cache.ErrCloneFailed)
		items.add("b", cache.ErrFetchFailed)
		requireCode(t, codeCloneFailed, items.first)
	})
}

func TestApp_finishItems(t *testing.T) {
	newItems := func() *itemResults {
		var items itemResults
		items.add("updated  a/b/c", nil)
		items.add("updated  a/b/d", fmt.Errorf("%w: boom", cache.ErrFetchFailed))
		return &items
	}
	results := []cacheUpdateJSON{{ID: "a/b/c"}}
	t.Run("text puts failures on stderr", func(t *testing.T) {
		app, stdout, stderr := newCacheApp(stubCache())
		requireCode(t, codeFetchFailed, app.finishItems("repos", results, newItems()))
		assert.Equal(t, "updated  a/b/c\n", stdout.String())
		assert.Equal(t, "ws: fetch failed: boom\n", stderr.String())
	})
	t.Run("json writes the results", func(t *testing.T) {
		app, stdout, stderr := newCacheApp(stubCache())
		app.global.json = true
		requireCode(t, codeFetchFailed, app.finishItems("repos", results, newItems()))
		assert.JSONEq(t, `{"repos":[{"id":"a/b/c","error":null}],"warnings":[]}`, stdout.String())
		assert.Empty(t, stderr.String())
	})
	t.Run("the returned failure is already reported", func(t *testing.T) {
		app, _, _ := newCacheApp(stubCache())
		err := app.finishItems("repos", results, newItems())
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.True(t, ce.reported)
	})
	t.Run("no failure", func(t *testing.T) {
		app, _, _ := newCacheApp(stubCache())
		var items itemResults
		items.add("x", nil)
		assert.NoError(t, app.finishItems("repos", results, &items))
	})
}

func TestApp_cacheList(t *testing.T) {
	entry := cache.Entry{
		ID:            testID(),
		Path:          "/cfg/cache/repos/github.com/bobcob7/buff-bear.git",
		Upstream:      "https://github.com/bobcob7/buff-bear.git",
		DefaultBranch: "main",
		LastFetchedAt: time.Date(2026, 10, 17, 10, 48, 0, 500, time.FixedZone("CDT", -5*3600)),
		SizeBytes:     18432000,
		Branches:      14,
		Tags:          6,
	}
	listing := func() *repoCacheMock {
		m := stubCache()
		m.ListFunc = func(context.Context) ([]cache.Entry, error) { return []cache.Entry{entry}, nil }
		return m
	}
	t.Run("text lists ids", func(t *testing.T) {
		app, stdout, _ := newCacheApp(listing())
		require.NoError(t, app.cacheList(t.Context()))
		assert.Equal(t, "github.com/bobcob7/buff-bear\n", stdout.String())
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(listing())
		app.global.json = true
		require.NoError(t, app.cacheList(t.Context()))
		assert.JSONEq(t, `{"repos":[{"id":"github.com/bobcob7/buff-bear","path":"/cfg/cache/repos/github.com/bobcob7/buff-bear.git","upstream":"https://github.com/bobcob7/buff-bear.git","default_branch":"main","last_fetched_at":"2026-10-17T15:48:00Z","size_bytes":18432000,"branches":14,"tags":6}],"warnings":[]}`, stdout.String())
	})
	t.Run("empty json list", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cacheList(t.Context()))
		assert.JSONEq(t, `{"repos":[],"warnings":[]}`, stdout.String())
	})
	t.Run("failure", func(t *testing.T) {
		m := stubCache()
		m.ListFunc = func(context.Context) ([]cache.Entry, error) { return nil, errTest }
		app, _, _ := newCacheApp(m)
		assert.ErrorIs(t, app.cacheList(t.Context()), errTest)
	})
}

func TestApp_cacheSearch(t *testing.T) {
	searching := func() *repoCacheMock {
		m := stubCache()
		m.SearchFunc = func(context.Context, string, int, bool) ([]cache.Match, error) {
			return []cache.Match{{ID: testID()}, {ID: testID(), Ref: "feat/new-thing"}}, nil
		}
		return m
	}
	t.Run("text", func(t *testing.T) {
		app, stdout, _ := newCacheApp(searching())
		require.NoError(t, app.cacheSearch(t.Context(), "bear", 20, true))
		assert.Equal(t, "github.com/bobcob7/buff-bear\ngithub.com/bobcob7/buff-bear@feat/new-thing\n", stdout.String())
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(searching())
		app.global.json = true
		require.NoError(t, app.cacheSearch(t.Context(), "bear", 20, true))
		assert.JSONEq(t, `{"matches":[{"id":"github.com/bobcob7/buff-bear","ref":null},{"id":"github.com/bobcob7/buff-bear","ref":"feat/new-thing"}],"warnings":[]}`, stdout.String())
	})
	t.Run("failure", func(t *testing.T) {
		m := stubCache()
		m.SearchFunc = func(context.Context, string, int, bool) ([]cache.Match, error) { return nil, errTest }
		app, _, _ := newCacheApp(m)
		assert.ErrorIs(t, app.cacheSearch(t.Context(), "bear", 20, false), errTest)
	})
}

func TestApp_cacheResolve(t *testing.T) {
	t.Run("text keeps the ref", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		require.NoError(t, app.cacheResolve(t.Context(), parsed(t, "buff-bear@feat/new-thing", "api")))
		assert.Equal(t, "github.com/bobcob7/buff-bear@feat/new-thing\ngithub.com/bobcob7/buff-bear\n", stdout.String())
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cacheResolve(t.Context(), parsed(t, "buff-bear")))
		assert.JSONEq(t, `{"repos":[{"id":"github.com/bobcob7/buff-bear","ref":null,"upstream":"https://github.com/bobcob7/buff-bear.git","cached":true,"source":"cache"}],"warnings":[]}`, stdout.String())
	})
	t.Run("ambiguous", func(t *testing.T) {
		m := stubCache()
		m.ResolveFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{}, &cache.AmbiguousError{Spec: "bear", Candidates: []repo.ID{testID(), otherID()}}
		}
		app, stdout, _ := newCacheApp(m)
		requireCode(t, codeRepoAmbiguous, app.cacheResolve(t.Context(), parsed(t, "bear")))
		assert.Empty(t, stdout.String())
	})
}

func TestApp_cacheRefs(t *testing.T) {
	listing := func() *repoCacheMock {
		m := stubCache()
		m.RefsFunc = func(_ context.Context, id repo.ID) (cache.RefListing, error) {
			return cache.RefListing{ID: id, DefaultBranch: "main", Refs: []cache.RefInfo{
				{Name: "main", Kind: cache.RefBranch, Commit: "9c04e2a7d1", Subject: "Fix it", Author: "ana", Date: time.Date(2026, 10, 24, 4, 12, 0, 0, time.FixedZone("CDT", -5*3600))},
				{Name: "v1", Kind: cache.RefTag, Commit: "3e8f1a2b", Subject: "Odd", Author: ""},
			}}, nil
		}
		return m
	}
	t.Run("text", func(t *testing.T) {
		app, stdout, _ := newCacheApp(listing())
		require.NoError(t, app.cacheRefs(t.Context(), &parsed(t, "buff-bear")[0]))
		assert.Equal(t, "branch  9c04e2a  main  Fix it\ntag     3e8f1a2  v1  Odd\n", stdout.String())
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(listing())
		app.global.json = true
		require.NoError(t, app.cacheRefs(t.Context(), &parsed(t, "buff-bear")[0]))
		assert.JSONEq(t, `{"id":"github.com/bobcob7/buff-bear","default_branch":"main","refs":[{"name":"main","kind":"branch","commit":"9c04e2a7d1","subject":"Fix it","author":"ana","date":"2026-10-24T09:12:00Z"},{"name":"v1","kind":"tag","commit":"3e8f1a2b","subject":"Odd","author":"","date":null}],"warnings":[]}`, stdout.String())
	})
	t.Run("empty json list", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cacheRefs(t.Context(), &parsed(t, "buff-bear")[0]))
		assert.JSONEq(t, `{"id":"github.com/bobcob7/buff-bear","default_branch":"main","refs":[],"warnings":[]}`, stdout.String())
	})
	t.Run("not cached", func(t *testing.T) {
		m := stubCache()
		m.LookupFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{}, cache.ErrRepoNotFound
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeRepoNotFound, app.cacheRefs(t.Context(), &parsed(t, "nope")[0]))
	})
	t.Run("git failure", func(t *testing.T) {
		m := stubCache()
		m.RefsFunc = func(context.Context, repo.ID) (cache.RefListing, error) {
			return cache.RefListing{}, &git.CommandError{Args: []string{"for-each-ref"}, ExitCode: 128, Stderr: "fatal: bad"}
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeGitFailed, app.cacheRefs(t.Context(), &parsed(t, "buff-bear")[0]))
	})
}

func TestShortCommit(t *testing.T) {
	assert.Equal(t, "9c04e2a", shortCommit("9c04e2a7d1"))
	assert.Equal(t, "abc", shortCommit("abc"))
}

func TestApp_cachePath(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		require.NoError(t, app.cachePath(t.Context(), &parsed(t, "buff-bear")[0]))
		assert.Equal(t, "/cfg/cache/repos/github.com/bobcob7/buff-bear.git\n", stdout.String())
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cachePath(t.Context(), &parsed(t, "buff-bear")[0]))
		assert.JSONEq(t, `{"id":"github.com/bobcob7/buff-bear","path":"/cfg/cache/repos/github.com/bobcob7/buff-bear.git","warnings":[]}`, stdout.String())
	})
	t.Run("not cached", func(t *testing.T) {
		m := stubCache()
		m.LookupFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{}, cache.ErrRepoNotFound
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeRepoNotFound, app.cachePath(t.Context(), &parsed(t, "nope")[0]))
	})
}

func TestApp_cacheRemove(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		m := stubCache()
		app, stdout, _ := newCacheApp(m)
		require.NoError(t, app.cacheRemove(t.Context(), parsed(t, "buff-bear")))
		assert.Equal(t, "github.com/bobcob7/buff-bear\n", stdout.String())
		require.Len(t, m.RemoveCalls(), 1)
		assert.Equal(t, testID(), m.RemoveCalls()[0].ID)
	})
	t.Run("json", func(t *testing.T) {
		app, stdout, _ := newCacheApp(stubCache())
		app.global.json = true
		require.NoError(t, app.cacheRemove(t.Context(), parsed(t, "buff-bear")))
		assert.JSONEq(t, `{"repos":[{"id":"github.com/bobcob7/buff-bear","path":"/cfg/cache/repos/github.com/bobcob7/buff-bear.git","error":null}],"warnings":[]}`, stdout.String())
	})
	t.Run("not cached", func(t *testing.T) {
		m := stubCache()
		m.LookupFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) {
			return cache.Resolution{}, cache.ErrRepoNotFound
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeRepoNotFound, app.cacheRemove(t.Context(), parsed(t, "nope")))
		assert.Empty(t, m.RemoveCalls())
	})
	t.Run("looks up every spec before removing", func(t *testing.T) {
		m := stubCache()
		m.LookupFunc = func(_ context.Context, sp *repo.Spec) (cache.Resolution, error) {
			if sp.Name == "nope" {
				return cache.Resolution{}, cache.ErrRepoNotFound
			}
			return cache.Resolution{ID: testID()}, nil
		}
		app, _, _ := newCacheApp(m)
		requireCode(t, codeRepoNotFound, app.cacheRemove(t.Context(), parsed(t, "buff-bear", "nope")))
		assert.Empty(t, m.RemoveCalls())
	})
	t.Run("a failed removal doesn't stop the others", func(t *testing.T) {
		m := stubCache()
		m.LookupFunc = func(_ context.Context, sp *repo.Spec) (cache.Resolution, error) {
			if sp.Name == "api" {
				return cache.Resolution{ID: otherID()}, nil
			}
			return cache.Resolution{ID: testID()}, nil
		}
		m.RemoveFunc = func(_ context.Context, id repo.ID) error {
			if id == testID() {
				return errTest
			}
			return nil
		}
		app, stdout, _ := newCacheApp(m)
		requireCode(t, codeInternal, app.cacheRemove(t.Context(), parsed(t, "buff-bear", "api")))
		assert.Len(t, m.RemoveCalls(), 2)
		assert.Equal(t, "github.com/bobcob7/api\n", stdout.String())
	})
}

func TestDomainError(t *testing.T) {
	t.Run("adds git's stderr to details", func(t *testing.T) {
		gitErr := &git.CommandError{Args: []string{"ls-remote"}, Stderr: "fatal: unable to access", Err: errTest}
		err := domainError(fmt.Errorf("%w: %w", cache.ErrFetchFailed, gitErr))
		requireCode(t, codeFetchFailed, err)
		ce, _ := err.(*cliError) //nolint:errorlint // requireCode checked the type.
		assert.Equal(t, map[string]any{"stderr": "fatal: unable to access"}, ce.details)
	})
	t.Run("keeps other details", func(t *testing.T) {
		err := domainError(&cache.AmbiguousError{Spec: "bear", Candidates: []repo.ID{testID()}})
		ce, _ := err.(*cliError) //nolint:errorlint // domainError returns a *cliError for coded errors.
		assert.Contains(t, ce.details, "candidates")
	})
	t.Run("adds claude's stderr to details", func(t *testing.T) {
		claudeErr := &claude.CommandError{Args: []string{"--bg"}, Stderr: "error: unknown option", Err: errTest}
		err := domainError(claudeErr)
		requireCode(t, codeClaudeFailed, err)
		ce, _ := err.(*cliError) //nolint:errorlint // requireCode checked the type.
		assert.Equal(t, map[string]any{"stderr": "error: unknown option"}, ce.details)
	})
	t.Run("no details without stderr", func(t *testing.T) {
		err := domainError(fmt.Errorf("%w: %w", cache.ErrFetchFailed, &git.CommandError{Err: errTest}))
		ce, _ := err.(*cliError) //nolint:errorlint // domainError returns a *cliError for coded errors.
		assert.Nil(t, ce.details)
	})
	t.Run("uncoded errors are unchanged", func(t *testing.T) {
		assert.Equal(t, errTest, domainError(errTest))
	})
}

func TestCodedDomainError(t *testing.T) {
	t.Run("ambiguous lists candidates", func(t *testing.T) {
		err := codedDomainError(&cache.AmbiguousError{Spec: "bear", Candidates: []repo.ID{testID(), otherID()}})
		require.Equal(t, codeRepoAmbiguous, err.code)
		assert.Equal(t, map[string]any{"candidates": []string{"github.com/bobcob7/buff-bear", "github.com/bobcob7/api"}}, err.details)
	})
	t.Run("git missing", func(t *testing.T) {
		assert.Equal(t, codeGitFailed, codedDomainError(fmt.Errorf("%w: %w", cache.ErrCloneFailed, git.ErrNotInstalled)).code)
	})
	t.Run("git too old", func(t *testing.T) {
		assert.Equal(t, codeGitFailed, codedDomainError(git.ErrTooOld).code)
	})
	t.Run("bad spec", func(t *testing.T) {
		assert.Equal(t, codeSpecInvalid, codedDomainError(repo.ErrInvalidSpec).code)
	})
	t.Run("repo not found", func(t *testing.T) {
		assert.Equal(t, codeRepoNotFound, codedDomainError(cache.ErrRepoNotFound).code)
	})
	t.Run("ref not found", func(t *testing.T) {
		assert.Equal(t, codeRefNotFound, codedDomainError(cache.ErrRefNotFound).code)
	})
	t.Run("clone failed", func(t *testing.T) {
		assert.Equal(t, codeCloneFailed, codedDomainError(cache.ErrCloneFailed).code)
	})
	t.Run("fetch failed", func(t *testing.T) {
		assert.Equal(t, codeFetchFailed, codedDomainError(cache.ErrFetchFailed).code)
	})
	t.Run("other git failure", func(t *testing.T) {
		assert.Equal(t, codeGitFailed, codedDomainError(&git.CommandError{Args: []string{"config"}, Err: errTest}).code)
	})
	t.Run("workspace name invalid", func(t *testing.T) {
		assert.Equal(t, codeNameInvalid, codedDomainError(workspace.ErrNameInvalid).code)
	})
	t.Run("conflicting specs", func(t *testing.T) {
		assert.Equal(t, codeSpecInvalid, codedDomainError(workspace.ErrSpecConflict).code)
	})
	t.Run("workspace not found", func(t *testing.T) {
		assert.Equal(t, codeWorkspaceNotFound, codedDomainError(workspace.ErrNotFound).code)
	})
	t.Run("not an agent workspace", func(t *testing.T) {
		assert.Equal(t, codeWorkspaceNotFound, codedDomainError(lifetime.ErrNotAgent).code)
	})
	t.Run("workspace exists", func(t *testing.T) {
		assert.Equal(t, codeWorkspaceExists, codedDomainError(workspace.ErrExists).code)
	})
	t.Run("claude --settings from the user", func(t *testing.T) {
		ce := codedDomainError(claude.ErrSettingsArg)
		assert.Equal(t, codeUsage, ce.code)
		assert.Equal(t, exitUsage, ce.exitCode())
	})
	t.Run("claude missing", func(t *testing.T) {
		assert.Equal(t, codeClaudeMissing, codedDomainError(claude.ErrNotInstalled).code)
	})
	t.Run("claude untrusted", func(t *testing.T) {
		err := fmt.Errorf("%w: %w", claude.ErrUntrusted, &claude.CommandError{Args: []string{"--bg"}, Err: errTest})
		assert.Equal(t, codeClaudeUntrusted, codedDomainError(err).code)
	})
	t.Run("claude too old", func(t *testing.T) {
		assert.Equal(t, codeClaudeFailed, codedDomainError(claude.ErrTooOld).code)
	})
	t.Run("claude session missing", func(t *testing.T) {
		assert.Equal(t, codeClaudeFailed, codedDomainError(claude.ErrNoSession).code)
	})
	t.Run("claude output unreadable", func(t *testing.T) {
		assert.Equal(t, codeClaudeFailed, codedDomainError(claude.ErrUnexpectedOutput).code)
	})
	t.Run("other claude failure", func(t *testing.T) {
		assert.Equal(t, codeClaudeFailed, codedDomainError(&claude.CommandError{Args: []string{"agents"}, Err: errTest}).code)
	})
	t.Run("anything else has no code", func(t *testing.T) {
		assert.Nil(t, codedDomainError(errTest))
	})
}

func TestCommandStderr(t *testing.T) {
	t.Run("git", func(t *testing.T) {
		assert.Equal(t, "fatal: x", commandStderr(&git.CommandError{Stderr: "fatal: x", Err: errTest}))
	})
	t.Run("claude", func(t *testing.T) {
		err := fmt.Errorf("%w: %w", claude.ErrUntrusted, &claude.CommandError{Stderr: "Workspace not trusted.", Err: errTest})
		assert.Equal(t, "Workspace not trusted.", commandStderr(err))
	})
	t.Run("neither", func(t *testing.T) {
		assert.Empty(t, commandStderr(errTest))
	})
}

func TestApp_warnings(t *testing.T) {
	t.Run("never nil", func(t *testing.T) {
		app, _, _ := newCacheApp(stubCache())
		assert.Equal(t, []string{}, app.warnings())
	})
	t.Run("the removal pass's, the cache's, and the workspace store's", func(t *testing.T) {
		e := newMockedApp()
		e.cache.WarningsFunc = func() []string { return []string{"cache"} }
		e.lifetime.WarningsFunc = func() []string { return []string{"pass"} }
		e.store.WarningsFunc = func() []string { return []string{"store"} }
		assert.Equal(t, []string{"pass", "cache", "store"}, e.app.warnings())
	})
}

func TestApp_printWarnings(t *testing.T) {
	e := newMockedApp()
	e.cache.WarningsFunc = func() []string { return []string{"one", "two"} }
	e.store.WarningsFunc = func() []string { return []string{"three"} }
	e.app.printWarnings()
	assert.Equal(t, "ws: warning: one\nws: warning: two\nws: warning: three\n", e.stderr.String())
}

func TestFormatTime(t *testing.T) {
	ts := time.Date(2026, 10, 18, 3, 12, 7, 999, time.FixedZone("CDT", -5*3600))
	assert.Equal(t, "2026-10-18T08:12:07Z", formatTime(ts))
}

func TestOptional(t *testing.T) {
	t.Run("empty is nil", func(t *testing.T) {
		assert.Nil(t, optional(""))
	})
	t.Run("value", func(t *testing.T) {
		got := optional("main")
		require.NotNil(t, got)
		assert.Equal(t, "main", *got)
	})
}

func TestWithRef(t *testing.T) {
	t.Run("no ref", func(t *testing.T) {
		assert.Equal(t, "a/b/c", withRef("a/b/c", ""))
	})
	t.Run("ref", func(t *testing.T) {
		assert.Equal(t, "a/b/c@v1", withRef("a/b/c", "v1"))
	})
}
