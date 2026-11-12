package cli

import (
	"bytes"
	"encoding/json"
	"slices"
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
	t.Run("adds a trimmed title", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "24h", "--prompt", "hi", "--title", "  Nightly deps bump ", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "Nightly deps bump", out["title"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, "Nightly deps bump", stored.Title)
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
			{"title with a newline", []string{"add", "deps", "--every", "1h", "--prompt", "hi", "--title", "a\nb"}, exitUsage, "loop_invalid"},
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
	t.Run("adds a script with its timeout and vars", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "24h",
			"--script", "gh issue list | grep .", "--var", "REPO=buff-bear", "--var", "Q=a,b=c",
			"--prompt", "Triage {{.Vars.REPO}}: {{.Stdout}}", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "gh issue list | grep .", out["script"])
		assert.Equal(t, "1m", out["script_timeout"])
		assert.Equal(t, map[string]any{"REPO": "buff-bear", "Q": "a,b=c"}, out["vars"])
		assert.Nil(t, out["last_gate"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, loop.FormatVersion, stored.Version)
	})
	t.Run("--script-timeout sets the timeout", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--script", "true",
			"--script-timeout", "90s", "--prompt", "hi", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "1m30s", out["script_timeout"])
	})
	t.Run("a loop without a script or vars prints nulls and an empty object", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi", "--json"))
		code := app.Run(t.Context(), []string{"loop", "list", "--json"})
		require.Equal(t, exitOK, code)
		assert.Contains(t, stdout.String(), `"vars":{}`)
		assert.Contains(t, stdout.String(), `"script":null`)
		assert.Contains(t, stdout.String(), `"script_timeout":null`)
		assert.Contains(t, stdout.String(), `"last_gate":null`)
	})
	t.Run("a blank script is no script", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--script", "   ", "--prompt", "hi", "--json")
		require.Equal(t, exitOK, code)
		assert.Nil(t, out["script"])
		assert.Nil(t, out["script_timeout"])
	})
	t.Run("--script-timeout without --script is a usage error", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		withLoopStore(t, app)
		code := app.Run(t.Context(), []string{"loop", "add", "deps", "--every", "1h", "--script-timeout", "1m", "--prompt", "hi", "--json"})
		assert.Equal(t, exitUsage, code)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), `"code":"usage"`)
	})
	t.Run("refuses bad script options and templates", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
		}{
			{"timeout over ten minutes", []string{"--script", "true", "--script-timeout", "11m", "--prompt", "hi"}},
			{"var key with a dash", []string{"--var", "my-key=x", "--prompt", "hi"}},
			{"var key given twice", []string{"--var", "A=1", "--var", "A=2", "--prompt", "hi"}},
			{"stdout without a script", []string{"--prompt", "{{.Stdout}}"}},
			{"unknown var", []string{"--prompt", "{{.Vars.NOPE}}"}},
			{"template that doesn't parse", []string{"--prompt", "a {{"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				app, stdout, stderr := newTestApp()
				withLoopStore(t, app)
				args := append([]string{"loop", "add", "deps", "--every", "1h"}, tt.args...)
				code := app.Run(t.Context(), append(args, "--json"))
				assert.Equal(t, exitUsage, code)
				assert.Empty(t, stdout.String())
				assert.Contains(t, stderr.String(), `"code":"loop_invalid"`)
			})
		}
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

func TestApp_loopListGate(t *testing.T) {
	t.Run("shows each loop's last gate, with a null exit code when it timed out", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		require.NoError(t, store.Create(&loop.Loop{
			Version: loop.FormatVersion, Name: "new-issues", Every: "1h", Prompt: "hi", Keep: 5,
			Script: "true", ScriptTimeout: "1m", CreatedAt: testNow(), NextRunAt: testNow(),
			LastGate: &loop.Gate{At: testNow(), Outcome: loop.GateSkipped, ExitCode: 1, Message: "no new issues"},
		}))
		require.NoError(t, store.Create(&loop.Loop{
			Version: loop.FormatVersion, Name: "slow", Every: "1h", Prompt: "hi", Keep: 5,
			Script: "sleep 9", ScriptTimeout: "1s", CreatedAt: testNow(), NextRunAt: testNow(),
			LastGate: &loop.Gate{At: testNow(), Outcome: loop.GateTimedOut, ExitCode: -1, Message: "timed out"},
		}))
		code, out := runLoop(t, app, stdout, "list", "--json")
		require.Equal(t, exitOK, code)
		loops := listedLoops(t, out)
		require.Len(t, loops, 2)
		assert.Equal(t, map[string]any{
			"at": testNow().Format(time.RFC3339), "outcome": "skipped", "exit_code": float64(1), "message": "no new issues",
		}, loops[0]["last_gate"])
		assert.Equal(t, map[string]any{
			"at": testNow().Format(time.RFC3339), "outcome": "timed_out", "exit_code": nil, "message": "timed out",
		}, loops[1]["last_gate"])
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
	t.Run("removes the loop's history and memory, so a loop added again starts empty", func(t *testing.T) {
		app, _, _ := newTestApp()
		withLoopStore(t, app)
		data := withLoopData(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		_, err := data.SetMemory(t.Context(), "deps", "seen", "#412", "", loop.AnyRev, testNow())
		require.NoError(t, err)
		require.Equal(t, exitOK, runLoopCode(t, app, "rm", "deps"))
		mem, err := data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Empty(t, mem.Keys())
		_, err = data.SetMemory(t.Context(), "deps", "seen", "#415", "", loop.AnyRev, testNow())
		require.NoError(t, err)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		mem, err = data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Empty(t, mem.Keys())
	})
	t.Run("a missing loop is not found", func(t *testing.T) {
		app, _, stderr := newTestApp()
		withLoopStore(t, app)
		code := app.Run(t.Context(), []string{"loop", "rm", "nope", "--json"})
		assert.Equal(t, exitNotFound, code)
		assert.Contains(t, stderr.String(), `"code":"loop_not_found"`)
	})
}

func TestApp_loopAddHidden(t *testing.T) {
	t.Run("a new loop's runs start hidden", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, true, out["hidden"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, loop.FormatVersion, stored.Version)
		require.NotNil(t, stored.Hidden)
		assert.True(t, *stored.Hidden)
	})
	t.Run("--show makes the runs visible", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--show", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, false, out["hidden"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		require.NotNil(t, stored.Hidden)
		assert.False(t, *stored.Hidden)
	})
	t.Run("--hidden=false is the same as --show", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--hidden=false", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, false, out["hidden"])
	})
	t.Run("--show with --hidden is a usage error", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		code := runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi", "--show", "--hidden")
		assert.Equal(t, exitUsage, code)
		_, err := store.Get("deps")
		require.Error(t, err)
		assert.Empty(t, stdout.String())
	})
	t.Run("a version 2 loop lists as hidden", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		require.Equal(t, exitOK, runLoopCode(t, app, "add", "deps", "--every", "1h", "--prompt", "hi"))
		l, err := store.Get("deps")
		require.NoError(t, err)
		l.Version = loop.Version2
		l.Hidden = nil
		require.NoError(t, store.Save(&l))
		stdout.Reset()
		code, out := runLoop(t, app, stdout, "list", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, true, listedLoops(t, out)[0]["hidden"])
	})
}

func TestHiddenFlag(t *testing.T) {
	changed := func(names ...string) func(string) bool {
		return func(name string) bool { return slices.Contains(names, name) }
	}
	t.Run("neither flag keeps hidden without a value", func(t *testing.T) {
		value, given, err := hiddenFlag(changed(), true, false)
		require.NoError(t, err)
		assert.True(t, value)
		assert.False(t, given)
	})
	t.Run("--show is visible", func(t *testing.T) {
		value, given, err := hiddenFlag(changed("show"), false, true)
		require.NoError(t, err)
		assert.False(t, value)
		assert.True(t, given)
	})
	t.Run("--hidden=false is visible", func(t *testing.T) {
		value, given, err := hiddenFlag(changed("hidden"), false, false)
		require.NoError(t, err)
		assert.False(t, value)
		assert.True(t, given)
	})
	t.Run("--hidden and --show together are a usage error", func(t *testing.T) {
		_, _, err := hiddenFlag(changed("hidden", "show"), true, true)
		assert.ErrorContains(t, err, "give --hidden or --show, not both")
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
	t.Run("the title is shown before the name", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Title: "Nightly deps bump", Every: "1h", NextRunAt: testNow()}
		assert.Equal(t, "Nightly deps bump (deps)  every 1h    active  next 2026-10-19T12:00:00Z  last -", loopLine(&l))
	})
}

func TestLoopJSONOf(t *testing.T) {
	t.Run("specs are never null", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h"}
		assert.Equal(t, []string{}, loopJSONOf(&l).Specs)
	})
	t.Run("vars are never null", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h"}
		assert.Equal(t, map[string]string{}, loopJSONOf(&l).Vars)
	})
	t.Run("the script timeout is the record's, or the default", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h", Script: "true"}
		require.NotNil(t, loopJSONOf(&l).ScriptTimeout)
		assert.Equal(t, "1m", *loopJSONOf(&l).ScriptTimeout)
		l.ScriptTimeout = "not-a-duration"
		assert.Equal(t, "not-a-duration", *loopJSONOf(&l).ScriptTimeout)
		l.Script = ""
		assert.Nil(t, loopJSONOf(&l).ScriptTimeout)
	})
	t.Run("the title is never null", func(t *testing.T) {
		l := loop.Loop{Name: "deps", Every: "1h"}
		assert.Empty(t, loopJSONOf(&l).Title)
		l.Title = "Nightly deps bump"
		assert.Equal(t, "Nightly deps bump", loopJSONOf(&l).Title)
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
