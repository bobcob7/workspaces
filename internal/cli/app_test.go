package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/exectest"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// testPaths and testConfig are what newTestApp's config loader returns.
func testPaths() config.Paths {
	return config.NewPaths("/cfg")
}

func testConfig() config.Config {
	return config.Config{Defaults: config.Defaults{Host: "github.com", Owners: []string{"bobcob7"}}}
}

// newTestApp returns an App whose config loader and repo cache succeed
// without touching the filesystem.
func newTestApp() (app *App, stdout, stderr *bytes.Buffer) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	configs := &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) {
			return testPaths(), testConfig(), nil
		},
	}
	app = New("test", nil, stdout, stderr, configs, nil)
	s := stores{cache: stubCache(), workspaces: stubWorkspaces(), lifetime: stubLifetime()}
	app.newStores = func(config.Paths, config.Config) stores { return s }
	app.lifetime = s.lifetime
	app.now = testNow
	return app, stdout, stderr
}

// testNow is newTestApp's clock.
func testNow() time.Time {
	return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
}

// stubWorkspaces is a workspace store with no workspaces.
func stubWorkspaces() *workspaceStoreMock {
	return &workspaceStoreMock{
		UsesCommitFunc: func(string, string) (bool, error) { return false, nil },
		WarningsFunc:   func() []string { return nil },
	}
}

// stubLifetime is a lifetime manager with nothing to remove or list.
func stubLifetime() *lifetimeManagerMock {
	return &lifetimeManagerMock{
		SweepFunc: func(context.Context) []lifetime.Removed { return nil },
		ListFunc:  func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil },
		RestoreFunc: func(context.Context, lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			return nil, nil
		},
		RestartFunc: func(context.Context, lifetime.RestartOptions) ([]lifetime.Restarted, error) {
			return nil, nil
		},
		WarningsFunc: func() []string { return nil },
	}
}

// mockedApp is a test App as withConfig leaves it, with its mocks.
type mockedApp struct {
	app      *App
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	cache    *repoCacheMock
	store    *workspaceStoreMock
	lifetime *lifetimeManagerMock
}

func newMockedApp() *mockedApp {
	app, stdout, stderr := newTestApp()
	e := &mockedApp{app: app, stdout: stdout, stderr: stderr, cache: stubCache(), store: stubWorkspaces(), lifetime: stubLifetime()}
	app.cache, app.workspaces, app.lifetime = e.cache, e.store, e.lifetime
	app.newStores = func(config.Paths, config.Config) stores {
		return stores{cache: e.cache, workspaces: e.store, lifetime: e.lifetime}
	}
	return e
}

// cmdWithContext is a command whose Context is the test's.
func cmdWithContext(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	return cmd
}

// testID is the repo stubCache resolves every spec to.
func testID() repo.ID {
	return repo.ID{Host: "github.com", Owner: "bobcob7", Name: "buff-bear"}
}

// stubCache is a repo cache where everything succeeds: every spec resolves
// to testID, which is cached.
func stubCache() *repoCacheMock {
	resolved := func(context.Context, *repo.Spec) (cache.Resolution, error) {
		return cache.Resolution{ID: testID(), Upstream: testID().UpstreamURL(), Cached: true, Source: cache.SourceCache}, nil
	}
	return &repoCacheMock{
		ResolveFunc: resolved,
		LookupFunc:  resolved,
		AddFunc:     func(context.Context, repo.ID, string) (bool, error) { return true, nil },
		FetchEachFunc: func(_ context.Context, ids []repo.ID) []error {
			return make([]error, len(ids))
		},
		IDsFunc:    func() ([]repo.ID, error) { return []repo.ID{testID()}, nil },
		ListFunc:   func(context.Context) ([]cache.Entry, error) { return nil, nil },
		SearchFunc: func(context.Context, string, int, bool) ([]cache.Match, error) { return nil, nil },
		RefsFunc: func(_ context.Context, id repo.ID) (cache.RefListing, error) {
			return cache.RefListing{ID: id, DefaultBranch: "main", Refs: []cache.RefInfo{}}, nil
		},
		RemoveFunc: func(context.Context, repo.ID) error { return nil },
		PruneFunc: func(context.Context, func(repo.ID, string) (bool, error), bool) ([]cache.PrunedRef, error) {
			return nil, nil
		},
		PathFunc:     func(id repo.ID) string { return "/cfg/cache/repos/" + id.String() + ".git" },
		WarningsFunc: func() []string { return nil },
	}
}

// newTestTree returns the "list" command of a minimal "ws cache list" tree,
// so command paths look like real ones.
func newTestTree() *cobra.Command {
	root := &cobra.Command{Use: "ws"}
	cacheCmd := &cobra.Command{Use: "cache"}
	list := &cobra.Command{Use: "list"}
	root.AddCommand(cacheCmd)
	cacheCmd.AddCommand(list)
	return list
}

// requireCode checks that err is a cliError with this code.
func requireCode(t *testing.T, want errorCode, err error) {
	t.Helper()
	ce, ok := errors.AsType[*cliError](err)
	require.True(t, ok, "want *cliError, got %T: %v", err, err)
	require.Equal(t, want, ce.code)
}

func requireUsageError(t *testing.T, err error) {
	t.Helper()
	requireCode(t, codeUsage, err)
}

// assertFlag checks that cmd has the flag, with this default.
func assertFlag(t *testing.T, cmd *cobra.Command, name, def string) {
	t.Helper()
	f := cmd.Flags().Lookup(name)
	require.NotNil(t, f, "--%s", name)
	assert.Equal(t, def, f.DefValue, "--%s default", name)
}

func TestNew(t *testing.T) {
	app, stdout, stderr := newTestApp()
	assert.Equal(t, "test", app.version)
	assert.Same(t, stdout, app.stdout)
	assert.Same(t, stderr, app.stderr)
	assert.False(t, app.indentJSON, "a buffer isn't a terminal")
	assert.NotNil(t, app.configs)
	assert.NotNil(t, app.shell)
	t.Run("builds a real claude client", func(t *testing.T) {
		_, ok := app.claude.(*claude.Client)
		assert.True(t, ok)
	})
	t.Run("logging starts off", func(t *testing.T) {
		assert.False(t, app.log.Enabled(t.Context(), slog.LevelError))
	})
	t.Run("logger follows the level", func(t *testing.T) {
		app.level.Set(slog.LevelDebug)
		assert.True(t, app.log.Enabled(t.Context(), slog.LevelDebug))
	})
	t.Run("builds a real cache, workspace store, and lifetime manager", func(t *testing.T) {
		app := New("test", nil, io.Discard, io.Discard, &configLoaderMock{}, nil)
		s := app.newStores(testPaths(), testConfig())
		_, ok := s.cache.(*cache.Cache)
		assert.True(t, ok)
		_, ok = s.workspaces.(*workspace.Store)
		assert.True(t, ok)
		_, ok = s.lifetime.(*lifetime.Manager)
		assert.True(t, ok)
	})
	t.Run("uses the real clock", func(t *testing.T) {
		app := New("test", nil, io.Discard, io.Discard, &configLoaderMock{}, nil)
		assert.NotNil(t, app.now)
	})
}

func TestApp_Run(t *testing.T) {
	app, stdout, _ := newTestApp()
	code := app.Run(t.Context(), []string{"--version"})
	assert.Equal(t, exitOK, code)
	assert.Contains(t, stdout.String(), "test")
}

func TestApp_execute(t *testing.T) {
	newRoot := func(err error) *cobra.Command {
		return &cobra.Command{
			Use:           "ws",
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE:          func(*cobra.Command, []string) error { return err },
		}
	}
	t.Run("success exits 0 and writes nothing", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		assert.Equal(t, exitOK, app.execute(t.Context(), newRoot(nil), nil))
		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})
	t.Run("coded error uses its exit code", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		code := app.execute(t.Context(), newRoot(newError(codeShellRunning, errTest)), nil)
		assert.Equal(t, exitRefused, code)
		assert.Empty(t, stdout.String())
		assert.NotEmpty(t, stderr.String())
	})
	t.Run("reported error only sets the exit code", func(t *testing.T) {
		app, _, stderr := newTestApp()
		ce := newError(codeFetchFailed, errTest)
		ce.reported = true
		code := app.execute(t.Context(), newRoot(ce), nil)
		assert.Equal(t, exitFailed, code)
		assert.Empty(t, stderr.String())
	})
	t.Run("a shell's exit status is the exit code, with nothing printed", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		code := app.execute(t.Context(), newRoot(asCLIError(&shellExitError{status: 130})), nil)
		assert.Equal(t, 130, code)
		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})
	t.Run("uncoded error is a usage error", func(t *testing.T) {
		app, _, _ := newTestApp()
		code := app.execute(t.Context(), newRoot(errors.New("unknown flag")), nil)
		assert.Equal(t, exitUsage, code)
	})
	t.Run("passes args to the command", func(t *testing.T) {
		app, _, _ := newTestApp()
		var got []string
		root := newRoot(nil)
		root.RunE = func(_ *cobra.Command, args []string) error {
			got = args
			return nil
		}
		app.execute(t.Context(), root, []string{"a", "b"})
		assert.Equal(t, []string{"a", "b"}, got)
	})
}

func TestApp_newRootCmd(t *testing.T) {
	app, _, _ := newTestApp()
	root := app.newRootCmd()
	t.Run("silences cobra's own error output", func(t *testing.T) {
		assert.True(t, root.SilenceUsage)
		assert.True(t, root.SilenceErrors)
	})
	t.Run("has the global flags", func(t *testing.T) {
		for _, name := range []string{"config-dir", "json", "quiet", "verbose"} {
			assert.NotNil(t, root.PersistentFlags().Lookup(name), name)
		}
		assert.NotNil(t, root.PersistentFlags().ShorthandLookup("v"))
	})
	t.Run("has every command", func(t *testing.T) {
		var names []string
		for _, cmd := range root.Commands() {
			names = append(names, cmd.Name())
		}
		assert.ElementsMatch(t, []string{"claude", "shell", "restore", "restart", "list", "rm", "rename", "cache", "review", "llm"}, names)
	})
	t.Run("flag errors are usage errors", func(t *testing.T) {
		requireUsageError(t, root.FlagErrorFunc()(root, errTest))
	})
	t.Run("verbose turns on debug logging", func(t *testing.T) {
		app, _, _ := newTestApp()
		root := app.newRootCmd()
		app.global.verbose = true
		root.PersistentPreRun(root, nil)
		assert.True(t, app.log.Enabled(t.Context(), slog.LevelDebug))
	})
	t.Run("logging stays off without verbose", func(t *testing.T) {
		app, _, _ := newTestApp()
		root := app.newRootCmd()
		root.PersistentPreRun(root, nil)
		assert.False(t, app.log.Enabled(t.Context(), slog.LevelError))
	})
}

func TestRunE(t *testing.T) {
	t.Run("success stays nil", func(t *testing.T) {
		fn := runE(func(*cobra.Command, []string) error { return nil })
		assert.NoError(t, fn(nil, nil))
	})
	t.Run("uncoded error becomes internal", func(t *testing.T) {
		fn := runE(func(*cobra.Command, []string) error { return errTest })
		requireCode(t, codeInternal, fn(nil, nil))
	})
	t.Run("coded error keeps its code", func(t *testing.T) {
		fn := runE(func(*cobra.Command, []string) error { return newError(codeRefNotFound, errTest) })
		requireCode(t, codeRefNotFound, fn(nil, nil))
	})
}

func TestApp_withConfig(t *testing.T) {
	newApp := func(err error) (*App, *configLoaderMock) {
		configs := &configLoaderMock{
			LoadFunc: func(string) (config.Paths, config.Config, error) {
				if err != nil {
					return config.Paths{}, config.Config{}, err
				}
				return testPaths(), testConfig(), nil
			},
		}
		app, _, _ := newTestApp()
		app.configs = configs
		return app, configs
	}
	t.Run("loads the config before running", func(t *testing.T) {
		app, _ := newApp(nil)
		var ran bool
		fn := app.withConfig(func(*cobra.Command, []string) error {
			ran = true
			assert.Equal(t, testPaths(), app.paths)
			assert.Equal(t, testConfig(), app.config)
			return nil
		})
		require.NoError(t, fn(cmdWithContext(t), nil))
		assert.True(t, ran)
	})
	t.Run("builds the stores from the loaded config", func(t *testing.T) {
		app, _ := newApp(nil)
		var gotPaths config.Paths
		var gotConfig config.Config
		s := stores{cache: stubCache(), workspaces: stubWorkspaces(), lifetime: stubLifetime()}
		app.newStores = func(p config.Paths, c config.Config) stores {
			gotPaths, gotConfig = p, c
			return s
		}
		require.NoError(t, app.withConfig(succeed)(cmdWithContext(t), nil))
		assert.Equal(t, testPaths(), gotPaths)
		assert.Equal(t, testConfig(), gotConfig)
		assert.Same(t, s.cache, app.cache)
		assert.Same(t, s.workspaces, app.workspaces)
		assert.Same(t, s.lifetime, app.lifetime)
	})
	t.Run("runs the removal pass before the command", func(t *testing.T) {
		app, _ := newApp(nil)
		m := stubLifetime()
		removed := []lifetime.Removed{{Workspace: workspace.Workspace{ID: "x"}}}
		m.SweepFunc = func(context.Context) []lifetime.Removed { return removed }
		app.newStores = func(config.Paths, config.Config) stores {
			return stores{cache: stubCache(), workspaces: stubWorkspaces(), lifetime: m}
		}
		fn := app.withConfig(func(*cobra.Command, []string) error {
			assert.Len(t, m.SweepCalls(), 1, "before the command")
			return nil
		})
		require.NoError(t, fn(cmdWithContext(t), nil))
		assert.Equal(t, removed, app.swept)
	})
	t.Run("passes --config-dir", func(t *testing.T) {
		app, configs := newApp(nil)
		app.global.configDir = "/elsewhere"
		require.NoError(t, app.withConfig(succeed)(cmdWithContext(t), nil))
		require.Len(t, configs.LoadCalls(), 1)
		assert.Equal(t, "/elsewhere", configs.LoadCalls()[0].ConfigDir)
	})
	t.Run("returns the command's error", func(t *testing.T) {
		app, _ := newApp(nil)
		fail := func(*cobra.Command, []string) error { return errTest }
		assert.ErrorIs(t, app.withConfig(fail)(cmdWithContext(t), nil), errTest)
	})
	t.Run("invalid config is config_invalid", func(t *testing.T) {
		app, _ := newApp(fmt.Errorf("bad: %w", config.ErrInvalid))
		requireCode(t, codeConfigInvalid, app.withConfig(succeed)(cmdWithContext(t), nil))
	})
	t.Run("other errors are wrapped without a code", func(t *testing.T) {
		app, _ := newApp(errTest)
		err := app.withConfig(succeed)(cmdWithContext(t), nil)
		require.ErrorIs(t, err, errTest)
		_, ok := errors.AsType[*cliError](err)
		assert.False(t, ok)
	})
	t.Run("doesn't run the command when loading fails", func(t *testing.T) {
		app, _ := newApp(errTest)
		var ran bool
		_ = app.withConfig(func(*cobra.Command, []string) error {
			ran = true
			return nil
		})(cmdWithContext(t), nil)
		assert.False(t, ran)
	})
}

func TestApp_withConfigUnswept(t *testing.T) {
	app, _, _ := newTestApp()
	m := stubLifetime()
	app.newStores = func(config.Paths, config.Config) stores {
		return stores{cache: stubCache(), workspaces: stubWorkspaces(), lifetime: m}
	}
	var ran bool
	fn := app.withConfigUnswept(func(*cobra.Command, []string) error {
		ran = true
		return nil
	})
	require.NoError(t, fn(cmdWithContext(t), nil))
	assert.True(t, ran)
	assert.Same(t, m, app.lifetime)
	assert.Empty(t, m.SweepCalls())
}

// succeed is a command body that returns no error.
func succeed(*cobra.Command, []string) error {
	return nil
}

func TestApp_noJSON(t *testing.T) {
	cmd := newTestTree()
	t.Run("allows no --json", func(t *testing.T) {
		app := &App{}
		assert.NoError(t, app.noJSON(cmd, nil))
	})
	t.Run("rejects --json", func(t *testing.T) {
		app := &App{global: globalOptions{json: true}}
		err := app.noJSON(cmd, nil)
		requireUsageError(t, err)
		assert.EqualError(t, err, "cache list doesn't support --json")
	})
}

// lifetimeE2E is a real App with local upstreams, configured in a temp
// dir, whose claude is fakeLifetimeClaude and whose Claude Code
// configuration directory is a temp dir of its own.
type lifetimeE2E struct {
	app     *App
	configs configLoader
	environ []string
	paths   config.Paths
	scratch string
	jobs    string
	// claudeJSON is Claude Code's config; ws claude trusts nothing until a
	// test writes it.
	claudeJSON string
	stdout     *bytes.Buffer
	stderr     *bytes.Buffer
}

func newLifetimeE2E(t *testing.T) *lifetimeE2E {
	t.Helper()
	up := gittest.NewUpstream(t)
	up.Repo(t, "o", "n")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	e := &lifetimeE2E{
		paths:   config.NewPaths(filepath.Join(root, "cfg")),
		scratch: filepath.Join(root, "scratch"),
		jobs:    filepath.Join(root, "claude-config", "jobs"),
	}
	require.NoError(t, os.MkdirAll(e.scratch, 0o750))
	require.NoError(t, os.MkdirAll(e.jobs, 0o750))
	e.setAgents(t, "[]")
	e.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
		return e.paths, config.Config{Defaults: config.Defaults{Host: gittest.Host, Owners: []string{"o"}}}, nil
	}}
	e.environ = append(slices.Clone(up.Environ), "CLAUDE_CONFIG_DIR="+filepath.Dir(e.jobs))
	e.claudeJSON = filepath.Join(filepath.Dir(e.jobs), ".claude.json")
	fakeLifetimeClaude(t, e.scratch)
	return e
}

// run runs ws with args in a new App, as a new ws process would, and
// returns its exit code.
func (e *lifetimeE2E) run(t *testing.T, args ...string) int {
	t.Helper()
	e.stdout, e.stderr = &bytes.Buffer{}, &bytes.Buffer{}
	e.app = New("test", nil, e.stdout, e.stderr, e.configs, e.environ)
	e.app.claude = claude.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), filepath.Join(e.scratch, "claude"), e.environ)
	return e.app.Run(t.Context(), args)
}

// setRespawned sets what `claude agents --json` prints once `claude
// respawn` has run.
func (e *lifetimeE2E) setRespawned(t *testing.T, agents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "respawned.json"), []byte(agents), 0o600))
}

// setAgents sets what `claude agents --json` prints.
func (e *lifetimeE2E) setAgents(t *testing.T, agents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "agents.json"), []byte(agents), 0o600))
}

// workspaceID is the id of the one workspace whose state is under
// stateDir and whose id was made from a --name sanitized to base: base, a
// dash, and a random suffix (ADR 0036).
func workspaceID(t *testing.T, stateDir, base string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(stateDir, base+"-*"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "workspaces named %s", base)
	return filepath.Base(matches[0])
}

// assertNoWorkspace checks that no workspace, and no state, is left with
// an id made from a --name sanitized to base.
func assertNoWorkspace(t *testing.T, paths *config.Paths, base string) {
	t.Helper()
	for _, dir := range []string{paths.Workspaces, paths.State} {
		matches, err := filepath.Glob(filepath.Join(dir, base+"-*"))
		require.NoError(t, err)
		assert.Empty(t, matches)
	}
}

// start creates a workspace named name with ws claude, as session id,
// which Claude Code then lists, idle, in the workspace, and returns its
// path.
func (e *lifetimeE2E) start(t *testing.T, name, id string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "next-id"), []byte(id), 0o600))
	require.Equal(t, exitOK, e.run(t, "claude", "--name", name, "n", "--", "fix it"), "stderr: %s", e.stderr.String())
	id, _, ok := strings.Cut(e.stdout.String(), "  ")
	require.True(t, ok, "ws claude printed %q", e.stdout.String())
	// A child that a parallel test forked while creation held the lock
	// holds it too until that child execs.
	require.Eventually(t, func() bool {
		held, err := lockfile.HeldExclusively(filepath.Join(e.paths.State, id, "lock"))
		return err == nil && !held
	}, 5*time.Second, 10*time.Millisecond, "creation lock still held")
	return filepath.Join(e.paths.Workspaces, id)
}

// calls is the stop, rm, and respawn commands the fake claude ran.
func (e *lifetimeE2E) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.scratch, "calls"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return strings.Fields(strings.ReplaceAll(string(data), " ", "_"))
}

// writeClaudeJSON writes Claude Code's config at path, trusting dirs.
func writeClaudeJSON(t *testing.T, path string, dirs ...string) {
	t.Helper()
	projects := map[string]any{}
	for _, dir := range dirs {
		projects[dir] = map[string]any{"hasTrustDialogAccepted": true}
	}
	data, err := json.Marshal(map[string]any{"projects": projects})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// trustedDirsIn are the directories Claude Code's config at path trusts,
// sorted.
func trustedDirsIn(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]map[string]map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	var dirs []string
	for dir, p := range got["projects"] {
		if p["hasTrustDialogAccepted"] == true {
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// takeLock takes the lock at path once it is free. A process that another
// parallel test forks holds a copy of this process's descriptors until it
// execs, so a lock this process has just released can look held for a
// moment.
func takeLock(t *testing.T, path string) *lockfile.Lock {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		l, ok, err := lockfile.TryExclusiveLock(path)
		require.NoError(t, err)
		if ok {
			return l
		}
		select {
		case <-ctx.Done():
			require.FailNow(t, "the lock is still held", path)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// agentJSON is one background session as `claude agents --json` prints
// it, idle with a process.
func agentJSON(id, cwd string) string {
	return fmt.Sprintf(`{"pid":4242,"id":%q,"cwd":%q,"kind":"background","startedAt":%d,"sessionId":"uuid-%s","name":"x","status":"idle","state":"blocked"}`,
		id, cwd, time.Now().Add(-time.Minute).UnixMilli(), id)
}

// stoppedAgentJSON is one background session as `claude agents --json`
// prints it with no process, created an hour ago.
func stoppedAgentJSON(id, cwd string) string {
	return fmt.Sprintf(`{"id":%q,"cwd":%q,"kind":"background","startedAt":%d,"sessionId":"uuid-%s","name":"x","state":"stopped"}`,
		id, cwd, time.Now().Add(-time.Hour).UnixMilli(), id)
}

// fakeLifetimeClaude writes an executable sh script standing in for
// claude 2.1.303: `agents` prints dir/agents.json, `stop`, `rm`, and
// `respawn` append "<command> <id>" to dir/calls, `respawn` then replaces
// dir/agents.json with dir/respawned.json if there is one, and `--bg`
// starts the session in dir/next-id. The background service's pid is in
// dir/daemon-pid while one runs: `daemon stop` and `daemon run` append
// their command to dir/calls, `daemon stop` fails if dir/fail-stop exists
// and otherwise removes the pid, `daemon run` writes its own pid and
// exits, and `daemon status` prints the pid, or fails without one.
func fakeLifetimeClaude(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"dir=" + dir + "\n" +
		`case "$1" in` + "\n" +
		`--version) echo "2.1.303 (Claude Code)" ;;` + "\n" +
		`agents) cat "$dir/agents.json" ;;` + "\n" +
		`stop|rm) echo "$1 $2" >> "$dir/calls" ;;` + "\n" +
		`respawn) echo "$1 $2" >> "$dir/calls"; if [ -f "$dir/respawned.json" ]; then cp "$dir/respawned.json" "$dir/agents.json"; fi ;;` + "\n" +
		`--bg) printf 'backgrounded · %s · x\n' "$(cat "$dir/next-id")" ;;` + "\n" +
		`daemon) case "$2" in` + "\n" +
		`  stop) echo "$1 $2" >> "$dir/calls"; if [ -f "$dir/fail-stop" ]; then echo "stop failed" >&2; exit 1; fi; rm -f "$dir/daemon-pid" ;;` + "\n" +
		`  run) echo "$1 $2" >> "$dir/calls"; echo $$ > "$dir/daemon-pid.tmp"; mv "$dir/daemon-pid.tmp" "$dir/daemon-pid" ;;` + "\n" +
		`  status) [ -f "$dir/daemon-pid" ] || exit 1; printf 'pid:     %s\nversion: 2.1.303\nuptime:  1s\norigin:  foreground\n' "$(cat "$dir/daemon-pid")" ;;` + "\n" +
		`  *) exit 1 ;;` + "\n" +
		`  esac ;;` + "\n" +
		`*) exit 1 ;;` + "\n" +
		"esac\n"
	exectest.WriteScript(t, path, script)
	return path
}
