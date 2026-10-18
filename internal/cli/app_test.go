package cli

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestApp() (app *App, stdout, stderr *bytes.Buffer) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	return New("test", stdout, stderr), stdout, stderr
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
