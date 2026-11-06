package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/loop"
)

// listedLoops is the loops in a ws loop list result.
func listedLoops(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["loops"].([]any)
	require.True(t, ok)
	loops := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		require.True(t, ok)
		loops = append(loops, m)
	}
	return loops
}

// runLoop runs ws loop with args on app and decodes its JSON stdout, if any.
func runLoop(t *testing.T, app *App, stdout *bytes.Buffer, args ...string) (int, map[string]any) {
	t.Helper()
	stdout.Reset()
	code := app.Run(t.Context(), append([]string{"loop"}, args...))
	var out map[string]any
	if stdout.Len() > 0 {
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &out), stdout.String())
	}
	return code, out
}

func TestApp_loopAdd(t *testing.T) {
	t.Run("adds a loop and prints it as JSON", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "24h", "--prompt", "hi", "buff-bear", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "deps", out["name"])
		assert.Equal(t, "24h", out["every"])
		assert.Equal(t, []any{"buff-bear"}, out["specs"])
		assert.Nil(t, out["model"])
		assert.EqualValues(t, 5, out["keep"])
		assert.Equal(t, false, out["paused"])
		assert.Equal(t, testNow().Add(24*time.Hour).Format(time.RFC3339), out["next_run_at"])
		assert.Nil(t, out["last_run"])
		assert.Equal(t, []any{}, out["warnings"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, "buff-bear", stored.Specs[0])
	})
	t.Run("prints the text line", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		assert.Equal(t, exitOK, app.Run(t.Context(), []string{"loop", "add", "deps", "--every", "1h30m", "--prompt", "hi"}))
		assert.Contains(t, stdout.String(), "deps       every 1h30m  active  next 2026-10-19T13:30:00Z  last -")
	})
	t.Run("a paused loop has no next run", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--paused", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, true, out["paused"])
		assert.Nil(t, out["next_run_at"])
	})
	t.Run("keeps the model and permission mode", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi",
			"--model", "opus", "--permission-mode", "acceptEdits", "--keep", "2", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "opus", out["model"])
		assert.Equal(t, "acceptEdits", out["permission_mode"])
		assert.EqualValues(t, 2, out["keep"])
	})
	t.Run("refuses bad input", func(t *testing.T) {
		tests := []struct {
			name     string
			args     []string
			exit     int
			wantCode string
		}{
			{"period under five minutes", []string{"add", "deps", "--every", "2m", "--prompt", "hi"}, exitUsage, "loop_invalid"},
			{"period that isn't whole minutes", []string{"add", "deps", "--every", "90s", "--prompt", "hi"}, exitUsage, "loop_invalid"},
			{"name with a slash", []string{"add", "a/b", "--every", "1h", "--prompt", "hi"}, exitUsage, "loop_invalid"},
			{"blank prompt", []string{"add", "deps", "--every", "1h", "--prompt", "  "}, exitUsage, "loop_invalid"},
			{"keep over the maximum", []string{"add", "deps", "--every", "1h", "--prompt", "hi", "--keep", "51"}, exitUsage, "loop_invalid"},
			{"bad spec", []string{"add", "deps", "--every", "1h", "--prompt", "hi", "/abs/path"}, exitUsage, "spec_invalid"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				app, stdout, stderr := newTestApp()
				withLoopStore(t, app)
				code := app.Run(t.Context(), append([]string{"loop"}, append(tt.args, "--json")...))
				assert.Equal(t, tt.exit, code)
				assert.Empty(t, stdout.String())
				assert.Contains(t, stderr.String(), `"code":"`+tt.wantCode+`"`)
			})
		}
	})
	t.Run("requires --every and --prompt", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
		}{
			{"missing --every", []string{"add", "deps", "--prompt", "hi"}},
			{"missing --prompt", []string{"add", "deps", "--every", "1h"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				app, _, stderr := newTestApp()
				withLoopStore(t, app)
				code := app.Run(t.Context(), append([]string{"loop"}, append(tt.args, "--json")...))
				assert.Equal(t, exitUsage, code)
				assert.Contains(t, stderr.String(), `"code":"usage"`)
			})
		}
	})
	t.Run("a name already used is refused", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		stdout.Reset()
		code := app.Run(t.Context(), []string{"loop", "add", "deps", "--every", "1h", "--prompt", "hi", "--json"})
		assert.Equal(t, exitRefused, code)
		assert.Contains(t, stderr.String(), `"code":"loop_exists"`)
	})
}

// runLoopCode runs ws loop with args, discarding its output.
func runLoopCode(t *testing.T, app *App, args ...string) int {
	t.Helper()
	return app.Run(t.Context(), append([]string{"loop"}, args...))
}

func TestApp_loopList(t *testing.T) {
	t.Run("lists loops sorted by name", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "b", "--every", "1h", "--prompt", "hi"))
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "a", "--every", "1h", "--prompt", "hi"))
		code, out := runLoop(t, app, stdout, "list", "--json")
		require.Equal(t, exitOK, code)
		loops := listedLoops(t, out)
		require.Len(t, loops, 2)
		assert.Equal(t, "a", loops[0]["name"])
		assert.Equal(t, "b", loops[1]["name"])
		assert.Equal(t, []any{}, out["warnings"])
	})
	t.Run("an empty store lists an empty array", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "list", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, []any{}, out["loops"])
	})
	t.Run("text output is one line per loop", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "24h", "--prompt", "hi"))
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "triage", "--every", "1h", "--prompt", "hi", "--paused"))
		stdout.Reset()
		require.Equal(t, exitOK, runLoopCode(t, app, "list"))
		assert.Equal(t, "deps       every 24h   active  next 2026-10-20T12:00:00Z  last -\n"+
			"triage     every 1h    paused  next -                     last -\n", stdout.String())
	})
}

func TestApp_loopPauseResume(t *testing.T) {
	t.Run("pause then resume", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))

		code, out := runLoop(t, app, stdout, "pause", "deps", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, true, out["paused"])
		assert.Nil(t, out["next_run_at"])

		code, out = runLoop(t, app, stdout, "resume", "deps", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, false, out["paused"])
		assert.NotNil(t, out["next_run_at"])
	})
	t.Run("pausing a paused loop changes nothing", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi", "--paused"))
		code, out := runLoop(t, app, stdout, "pause", "deps", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, true, out["paused"])
	})
	t.Run("resuming a running loop keeps its next run", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		_, before := runLoop(t, app, stdout, "list", "--json")
		_, out := runLoop(t, app, stdout, "resume", "deps", "--json")
		assert.Equal(t, listedLoops(t, before)[0]["next_run_at"], out["next_run_at"])
	})
	t.Run("a missing loop is not found", func(t *testing.T) {
		for _, op := range []string{"pause", "resume"} {
			t.Run(op, func(t *testing.T) {
				app, _, stderr := newTestApp()
				withLoopStore(t, app)
				code := app.Run(t.Context(), []string{"loop", op, "nope", "--json"})
				assert.Equal(t, exitNotFound, code)
				assert.Contains(t, stderr.String(), `"code":"loop_not_found"`)
			})
		}
	})
}

func TestApp_loopRm(t *testing.T) {
	t.Run("removes the loop and prints it as it was", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		code, out := runLoop(t, app, stdout, "rm", "deps", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "deps", out["name"])
		_, list := runLoop(t, app, stdout, "list", "--json")
		assert.Equal(t, []any{}, list["loops"])
	})
	t.Run("a missing loop is not found", func(t *testing.T) {
		app, _, stderr := newTestApp()
		withLoopStore(t, app)
		code := app.Run(t.Context(), []string{"loop", "rm", "nope", "--json"})
		assert.Equal(t, exitNotFound, code)
		assert.Contains(t, stderr.String(), `"code":"loop_not_found"`)
	})
}

func TestLoopLine(t *testing.T) {
	t.Run("an active loop shows its next run", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "24h", NextRunAt: testNow()}
		assert.Equal(t, "deps       every 24h   active  next 2026-10-19T12:00:00Z  last -", loopLine(&l))
	})
	t.Run("a paused loop has no next run", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h", Paused: true}
		assert.Equal(t, "deps       every 1h    paused  next -                     last -", loopLine(&l))
	})
	t.Run("the last run's workspace", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h", Runs: []loop.Run{{Workspace: "loop-deps-1a2b3c4d"}}}
		assert.Contains(t, loopLine(&l), "last loop-deps-1a2b3c4d")
	})
}

func TestLoopJSONOf(t *testing.T) {
	t.Run("specs are never null", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h"}
		assert.Equal(t, []string{}, loopJSONOf(&l).Specs)
	})
	t.Run("a failed run carries its error", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h", Runs: []loop.Run{{
			StartedAt: testNow(), ErrorCode: "claude_missing", ErrorMessage: "claude not found",
		}}}
		last := loopJSONOf(&l).LastRun
		require.NotNil(t, last)
		assert.Nil(t, last.Workspace)
		require.NotNil(t, last.Error)
		assert.Equal(t, codeClaudeMissing, last.Error.Code)
		assert.Equal(t, "claude not found", last.Error.Message)
	})
}
