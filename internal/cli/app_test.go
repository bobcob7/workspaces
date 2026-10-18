package cli

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
)

// testPaths and testConfig are what newTestApp's config loader returns.
func testPaths() config.Paths {
	return config.NewPaths("/cfg")
}

func testConfig() config.Config {
	return config.Config{Defaults: config.Defaults{Host: "github.com", Owners: []string{"bobcob7"}}}
}

// newTestApp returns an App whose config loader succeeds without touching
// the filesystem.
func newTestApp() (app *App, stdout, stderr *bytes.Buffer) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	configs := &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) {
			return testPaths(), testConfig(), nil
		},
	}
	return New("test", stdout, stderr, configs), stdout, stderr
}

// newTestTree returns the "list" command of a minimal "ws cache list" tree,
// so command paths look like real ones.
func newTestTree() *cobra.Command {
	root := &cobra.Command{Use: "ws"}
	cache := &cobra.Command{Use: "cache"}
	list := &cobra.Command{Use: "list"}
	root.AddCommand(cache)
	cache.AddCommand(list)
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

// requireNotImplemented checks that a stub command reached its RunE body.
func requireNotImplemented(t *testing.T, err error) {
	t.Helper()
	assert.ErrorIs(t, err, errNotImplemented)
}

func TestNew(t *testing.T) {
	app, stdout, stderr := newTestApp()
	assert.Equal(t, "test", app.version)
	assert.Same(t, stdout, app.stdout)
	assert.Same(t, stderr, app.stderr)
	assert.False(t, app.indentJSON, "a buffer isn't a terminal")
	assert.NotNil(t, app.configs)
	t.Run("logging starts off", func(t *testing.T) {
		assert.False(t, app.log.Enabled(t.Context(), slog.LevelError))
	})
	t.Run("logger follows the level", func(t *testing.T) {
		app.level.Set(slog.LevelDebug)
		assert.True(t, app.log.Enabled(t.Context(), slog.LevelDebug))
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
		assert.ElementsMatch(t, []string{"claude", "shell", "restore", "restart", "list", "rm", "cache", "llm"}, names)
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
		require.NoError(t, fn(nil, nil))
		assert.True(t, ran)
	})
	t.Run("passes --config-dir", func(t *testing.T) {
		app, configs := newApp(nil)
		app.global.configDir = "/elsewhere"
		require.NoError(t, app.withConfig(succeed)(nil, nil))
		require.Len(t, configs.LoadCalls(), 1)
		assert.Equal(t, "/elsewhere", configs.LoadCalls()[0].ConfigDir)
	})
	t.Run("returns the command's error", func(t *testing.T) {
		app, _ := newApp(nil)
		assert.ErrorIs(t, app.withConfig(notImplemented)(nil, nil), errNotImplemented)
	})
	t.Run("invalid config is config_invalid", func(t *testing.T) {
		app, _ := newApp(fmt.Errorf("bad: %w", config.ErrInvalid))
		requireCode(t, codeConfigInvalid, app.withConfig(succeed)(nil, nil))
	})
	t.Run("other errors are wrapped without a code", func(t *testing.T) {
		app, _ := newApp(errTest)
		err := app.withConfig(succeed)(nil, nil)
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
		})(nil, nil)
		assert.False(t, ran)
	})
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
