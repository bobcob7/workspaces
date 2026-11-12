package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/loop"
)

// addEditLoop adds the loop deps, every hour, with specs buff-bear, and
// returns the app's stdout buffer.
func addEditLoop(t *testing.T, app *App, stdout *bytes.Buffer) {
	t.Helper()
	require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi", "buff-bear"))
	stdout.Reset()
}

func TestApp_loopEdit(t *testing.T) {
	t.Run("changes the period and keeps the creation time", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		addEditLoop(t, app, stdout)
		before, err := store.Get("deps")
		require.NoError(t, err)

		code, out := runLoop(t, app, stdout, "edit", "deps", "--every", "6h", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "6h", out["every"])
		assert.Equal(t, before.CreatedAt.Format(time.RFC3339), out["created_at"])
		assert.Equal(t, []any{}, out["warnings"])
		after, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, 6*time.Hour, mustInterval(t, &after))
		assert.Equal(t, testNow().Add(6*time.Hour), after.NextRunAt)
	})
	t.Run("changes only the given field", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
			want map[string]any // the fields the edit must leave as these values
		}{
			{"prompt", []string{"--prompt", "new prompt"}, map[string]any{"prompt": "new prompt", "specs": []any{"buff-bear"}}},
			{"keep", []string{"--keep", "3"}, map[string]any{"keep": float64(3), "prompt": "hi"}},
			{"model", []string{"--model", "opus"}, map[string]any{"model": "opus", "prompt": "hi"}},
			{"clears the model", []string{"--model", ""}, map[string]any{"model": nil, "prompt": "hi"}},
			{"permission mode", []string{"--permission-mode", "acceptEdits"}, map[string]any{"permission_mode": "acceptEdits", "model": nil}},
			{"specs", []string{"buff-bear", "api"}, map[string]any{"specs": []any{"buff-bear", "api"}, "prompt": "hi"}},
			{"no specs", []string{"--no-specs"}, map[string]any{"specs": []any{}, "prompt": "hi"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				app, stdout, _ := newTestApp()
				withLoopStore(t, app)
				addEditLoop(t, app, stdout)
				args := append([]string{"edit", "deps"}, tt.args...)
				code, out := runLoop(t, app, stdout, append(args, "--json")...)
				require.Equal(t, exitOK, code)
				for key, want := range tt.want {
					assert.Equal(t, want, out[key], key)
				}
			})
		}
	})
	t.Run("sets and clears the title", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		addEditLoop(t, app, stdout)

		code, out := runLoop(t, app, stdout, "edit", "deps", "--title", "Nightly deps bump", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "Nightly deps bump", out["title"])
		assert.Equal(t, "hi", out["prompt"])

		code, out = runLoop(t, app, stdout, "edit", "deps", "--title", "", "--json")
		require.Equal(t, exitOK, code)
		assert.Empty(t, out["title"])
	})
	t.Run("sets and removes the script", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		addEditLoop(t, app, stdout)

		code, out := runLoop(t, app, stdout, "edit", "deps", "--script", "true", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "true", out["script"])
		assert.Equal(t, "1m", out["script_timeout"])

		code, out = runLoop(t, app, stdout, "edit", "deps", "--no-script", "--json")
		require.Equal(t, exitOK, code)
		assert.Nil(t, out["script"])
		assert.Nil(t, out["script_timeout"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Empty(t, stored.Script)
	})
	t.Run("sets the script timeout", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		addEditLoop(t, app, stdout)

		code, out := runLoop(t, app, stdout, "edit", "deps", "--script", "true", "--script-timeout", "45s", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "45s", out["script_timeout"])
	})
	t.Run("replaces the vars", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		addEditLoop(t, app, stdout)

		code, out := runLoop(t, app, stdout, "edit", "deps", "--var", "A=1", "--var", "B=2", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, map[string]any{"A": "1", "B": "2"}, out["vars"])

		code, out = runLoop(t, app, stdout, "edit", "deps", "--no-vars", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, map[string]any{}, out["vars"])
	})
	t.Run("prints the text line without --json", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		addEditLoop(t, app, stdout)

		require.Equal(t, exitOK, app.Run(t.Context(), []string{"loop", "edit", "deps", "--every", "6h"}))
		assert.Contains(t, stdout.String(), "deps       every 6h    active  next 2026-10-19T18:00:00Z  last -")
	})
	t.Run("refuses bad edits and changes nothing", func(t *testing.T) {
		tests := []struct {
			name     string
			args     []string
			exit     int
			wantCode string
		}{
			{"nothing to change", []string{"edit", "deps"}, exitUsage, "usage"},
			{"no script with a script", []string{"edit", "deps", "--no-script", "--script", "x"}, exitUsage, "usage"},
			{"no script with a timeout", []string{"edit", "deps", "--no-script", "--script-timeout", "30s"}, exitUsage, "usage"},
			{"no vars with a var", []string{"edit", "deps", "--no-vars", "--var", "A=1"}, exitUsage, "usage"},
			{"no specs with a spec", []string{"edit", "deps", "--no-specs", "buff-bear"}, exitUsage, "usage"},
			{"period under five minutes", []string{"edit", "deps", "--every", "2m"}, exitUsage, "loop_invalid"},
			{"keep over the maximum", []string{"edit", "deps", "--keep", "51"}, exitUsage, "loop_invalid"},
			{"bad var key", []string{"edit", "deps", "--var", "1A=x"}, exitUsage, "loop_invalid"},
			{"title with a tab", []string{"edit", "deps", "--title", "a\tb"}, exitUsage, "loop_invalid"},
			{"bad spec", []string{"edit", "deps", "/abs/path"}, exitUsage, "spec_invalid"},
			{"script timeout without a script", []string{"edit", "deps", "--script-timeout", "30s"}, exitUsage, "loop_invalid"},
			{"missing loop", []string{"edit", "missing", "--every", "6h"}, exitNotFound, "loop_not_found"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				app, stdout, stderr := newTestApp()
				store := withLoopStore(t, app)
				addEditLoop(t, app, stdout)
				before, err := store.Get("deps")
				require.NoError(t, err)

				code := app.Run(t.Context(), append([]string{"loop"}, append(tt.args, "--json")...))
				assert.Equal(t, tt.exit, code)
				assert.Empty(t, stdout.String())
				assert.Contains(t, stderr.String(), `"code":"`+tt.wantCode+`"`)
				after, err := store.Get("deps")
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	})
}

// mustInterval is the period of l, which the test expects to parse.
func TestApp_loopEditHidden(t *testing.T) {
	t.Run("--show makes a version 2 loop visible and version 3", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		addEditLoop(t, app, stdout)
		l, err := store.Get("deps")
		require.NoError(t, err)
		l.Version = loop.Version2
		l.Hidden = nil
		require.NoError(t, store.Save(&l))

		code, out := runLoop(t, app, stdout, "edit", "deps", "--show", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, false, out["hidden"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, loop.FormatVersion, stored.Version)
		require.NotNil(t, stored.Hidden)
		assert.False(t, *stored.Hidden)
	})
	t.Run("--hidden alone is a valid edit", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		addEditLoop(t, app, stdout)
		require.Equal(t, exitOK, runLoopCode(t, app, "edit", "deps", "--show"))

		code, out := runLoop(t, app, stdout, "edit", "deps", "--hidden", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, true, out["hidden"])
	})
	t.Run("--show with --hidden is a usage error", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		addEditLoop(t, app, stdout)
		assert.Equal(t, exitUsage, runLoopCode(t, app, "edit", "deps", "--show", "--hidden"))
	})
}

func mustInterval(t *testing.T, l *loop.Loop) time.Duration {
	t.Helper()
	d, err := l.Interval()
	require.NoError(t, err)
	return d
}
