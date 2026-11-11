package cli

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/loop"
)

// newHistoryEnv is an App with loop deps and three runs in its history: runs
// 1 and 2 succeeded (2 kept its report), and run 3 was skipped twice.
func newHistoryEnv(t *testing.T) (*App, *bytes.Buffer, *loop.DataStore) {
	t.Helper()
	app, stdout, _ := newTestApp()
	store := withLoopStore(t, app)
	data := withLoopData(t, app)
	l := loop.Loop{Version: loop.FormatVersion, Name: "deps", Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
	require.NoError(t, store.Create(&l))
	started := testNow().Add(-48 * time.Hour)
	finished := started.Add(time.Hour)
	secondStart := started.Add(time.Hour)
	secondFinish := secondStart.Add(time.Hour)
	skipped := started.Add(24 * time.Hour)
	h := loop.History{Version: loop.HistoryVersion, NextSeq: 4, Entries: []loop.Entry{
		{
			Seq: 1, Status: loop.StatusSucceeded, Trigger: loop.TriggerTick, StartedAt: started, FinishedAt: &finished,
			Workspace: "loop-deps-aaaa1111", Session: "e8cdf5d8", Repeats: 1,
			Verdict: &loop.Verdict{Verdict: "PASS", Outcome: "pass", RecordedAt: finished},
		},
		{
			Seq: 2, Status: loop.StatusSucceeded, Trigger: loop.TriggerRun, StartedAt: secondStart, FinishedAt: &secondFinish,
			Workspace: "loop-deps-bbbb2222", Session: "e8cdf5d9", Repeats: 1,
			Verdict: &loop.Verdict{Verdict: "PASS", Outcome: "pass", RecordedAt: secondFinish, Report: true},
		},
		{
			Seq: 3, Status: loop.StatusSkipped, Trigger: loop.TriggerTick, StartedAt: skipped, FinishedAt: &skipped, Repeats: 2,
			Gate: &loop.Gate{At: skipped, Outcome: loop.GateSkipped, ExitCode: 1, Message: "no outdated modules"},
		},
	}}
	require.NoError(t, data.WriteHistory("deps", &h))
	require.NoError(t, data.WriteReport("deps", 2, []byte("# Bumped 3 modules\n")))
	return app, stdout, data
}

// jsonObject is v as a JSON object, or the test fails.
func jsonObject(t *testing.T, v any) map[string]any {
	t.Helper()
	obj, ok := v.(map[string]any)
	require.True(t, ok, "%v is not an object", v)
	return obj
}

// historyRuns is the runs in a history listing's output.
func historyRuns(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["runs"].([]any)
	require.True(t, ok, "runs is %v", out["runs"])
	runs := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		runs = append(runs, jsonObject(t, r))
	}
	return runs
}

func TestApp_loopHistoryBlocked(t *testing.T) {
	t.Run("a closed blocked run has a duration", func(t *testing.T) {
		app, stdout, data := newHistoryEnv(t)
		started := testNow().Add(-2 * time.Hour)
		finished := started.Add(90 * time.Minute)
		h := loop.History{Version: loop.HistoryVersion, NextSeq: 2, Entries: []loop.Entry{{
			Seq: 1, Status: loop.StatusBlocked, Trigger: loop.TriggerTick, StartedAt: started, FinishedAt: &finished,
			Workspace: "loop-deps-aaaa1111", Repeats: 1,
			Verdict: &loop.Verdict{Verdict: "BLOCKED", Outcome: "blocked", RecordedAt: finished},
		}}}
		require.NoError(t, data.WriteHistory("deps", &h))

		code, out := runLoop(t, app, stdout, "history", "--json", "deps")
		require.Equal(t, exitOK, code)
		runs := historyRuns(t, out)
		require.Len(t, runs, 1)
		assert.Equal(t, "blocked", runs[0]["status"])
		assert.EqualValues(t, 5400, runs[0]["duration_seconds"])
		assert.Equal(t, "blocked", jsonObject(t, runs[0]["verdict"])["outcome"])
	})
}

func TestApp_loopHistory(t *testing.T) {
	t.Run("lists the newest run first, with its total", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		code, out := runLoop(t, app, stdout, "history", "--json", "deps")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "deps", out["loop"])
		assert.EqualValues(t, 3, out["total"])
		runs := historyRuns(t, out)
		require.Len(t, runs, 3)
		assert.EqualValues(t, 3, runs[0]["seq"])
		assert.Equal(t, "skipped", runs[0]["status"])
		assert.EqualValues(t, 2, runs[0]["repeats"])
		assert.Nil(t, runs[0]["duration_seconds"])
		assert.Nil(t, runs[0]["workspace"])
		assert.Equal(t, false, runs[0]["workspace_exists"])
		assert.EqualValues(t, 1, jsonObject(t, runs[0]["gate"])["exit_code"])
		assert.EqualValues(t, 2, runs[1]["seq"])
		assert.EqualValues(t, 3600, runs[1]["duration_seconds"])
		assert.Equal(t, "PASS", jsonObject(t, runs[1]["verdict"])["verdict"])
		assert.NotNil(t, jsonObject(t, runs[1]["verdict"])["report"])
		assert.Nil(t, jsonObject(t, runs[2]["verdict"])["report"])
	})
	t.Run("limit keeps the newest runs", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		code, out := runLoop(t, app, stdout, "history", "--json", "--limit", "1", "deps")
		require.Equal(t, exitOK, code)
		runs := historyRuns(t, out)
		require.Len(t, runs, 1)
		assert.EqualValues(t, 3, runs[0]["seq"])
		assert.EqualValues(t, 3, out["total"])
	})
	t.Run("run shows one run with its report", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		code, out := runLoop(t, app, stdout, "history", "--json", "--run", "2", "deps")
		require.Equal(t, exitOK, code)
		assert.EqualValues(t, 2, jsonObject(t, out["run"])["seq"])
		assert.Equal(t, "# Bumped 3 modules\n", out["report_text"])
	})
	t.Run("run with no report has a null report_text", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		code, out := runLoop(t, app, stdout, "history", "--json", "--run", "1", "deps")
		require.Equal(t, exitOK, code)
		assert.Nil(t, out["report_text"])
	})
	t.Run("an unknown run is loop_not_found", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		code, _ := runLoop(t, app, stdout, "history", "--json", "--run", "99", "deps")
		assert.Equal(t, exitNotFound, code)
	})
	t.Run("an unknown loop is loop_not_found", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		code, _ := runLoop(t, app, stdout, "history", "--json", "nope")
		assert.Equal(t, exitNotFound, code)
	})
	t.Run("flags that conflict or are out of range are usage errors", func(t *testing.T) {
		tests := map[string][]string{
			"run with clear":   {"--run", "1", "--clear"},
			"run with limit":   {"--run", "1", "--limit", "1"},
			"limit of zero":    {"--limit", "0"},
			"limit over 200":   {"--limit", "201"},
			"negative run":     {"--run", "-1"},
			"clear with limit": {"--clear", "--limit", "1"},
		}
		for name, flags := range tests {
			t.Run(name, func(t *testing.T) {
				app, stdout, _ := newHistoryEnv(t)
				code, _ := runLoop(t, app, stdout, append([]string{"history", "--json"}, append(flags, "deps")...)...)
				assert.Equal(t, exitUsage, code)
			})
		}
	})
	t.Run("text lists one line per run", func(t *testing.T) {
		app, stdout, _ := newHistoryEnv(t)
		require.Equal(t, exitOK, runLoopCode(t, app, "history", "deps"))
		text := stdout.String()
		assert.Contains(t, text, "skipped ×2")
		assert.Contains(t, text, "exit 1: no outdated modules")
		assert.Contains(t, text, "1h")
		assert.Contains(t, text, "PASS")
	})
	t.Run("clear empties the history and deletes the reports", func(t *testing.T) {
		app, stdout, data := newHistoryEnv(t)
		code, out := runLoop(t, app, stdout, "history", "--json", "--clear", "deps")
		require.Equal(t, exitOK, code)
		assert.EqualValues(t, 3, out["removed"])
		_, err := os.Stat(data.ReportPath("deps", 2))
		require.ErrorIs(t, err, os.ErrNotExist)
		code, out = runLoop(t, app, stdout, "history", "--json", "deps")
		require.Equal(t, exitOK, code)
		assert.Empty(t, historyRuns(t, out))
		assert.EqualValues(t, 0, out["total"])
	})
}
