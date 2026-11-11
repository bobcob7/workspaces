package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	historyTestStart = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	historyTestNow   = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
)

func openEntry(status RunStatus) Entry {
	return Entry{Status: status, Trigger: TriggerTick, StartedAt: historyTestStart, Workspace: "loop-deps-1a2b3c4d"}
}

func passVerdict(recordedAt time.Time) *Verdict {
	return &Verdict{Verdict: "PASS", Outcome: "pass", RecordedAt: recordedAt, Report: true}
}

func failVerdict(recordedAt time.Time) *Verdict {
	return &Verdict{Verdict: "FAIL", Outcome: "fail", RecordedAt: recordedAt}
}

func blockedVerdict(recordedAt time.Time) *Verdict {
	return &Verdict{Verdict: VerdictBlocked, Outcome: "blocked", RecordedAt: recordedAt}
}

func TestEntry_Open(t *testing.T) {
	t.Run("an entry with a workspace that isn't closed is open", func(t *testing.T) {
		e := openEntry(StatusRunning)
		assert.True(t, e.Open())
	})
	t.Run("an entry without a workspace is not open", func(t *testing.T) {
		e := Entry{Status: StatusStartFailed}
		assert.False(t, e.Open())
	})
	t.Run("a closed entry is not open", func(t *testing.T) {
		e := openEntry(StatusGone)
		e.Closed = true
		assert.False(t, e.Open())
	})
}

func TestEntry_Settle(t *testing.T) {
	t.Run("rule 1: an entry that isn't open is unchanged", func(t *testing.T) {
		e := Entry{Status: StatusRunning}
		assert.False(t, e.Settle(Observation{Listed: false}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
		assert.False(t, e.Closed)
	})
	t.Run("rule 2: an unlisted running workspace is gone and closed", func(t *testing.T) {
		e := openEntry(StatusRunning)
		assert.True(t, e.Settle(Observation{Listed: false}, historyTestNow))
		assert.Equal(t, StatusGone, e.Status)
		assert.True(t, e.Closed)
		require.NotNil(t, e.FinishedAt)
		assert.True(t, e.FinishedAt.Equal(historyTestNow))
	})
	t.Run("rule 2: an unlisted finished workspace keeps its status", func(t *testing.T) {
		e := openEntry(StatusDone)
		assert.True(t, e.Settle(Observation{Listed: false}, historyTestNow))
		assert.Equal(t, StatusDone, e.Status)
		assert.True(t, e.Closed)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 2: an unlisted run that needed attention is done at now", func(t *testing.T) {
		e := openEntry(StatusNeedsAttention)
		assert.True(t, e.Settle(Observation{Listed: false}, historyTestNow))
		assert.Equal(t, StatusDone, e.Status)
		assert.True(t, e.Closed)
		require.NotNil(t, e.FinishedAt)
		assert.True(t, e.FinishedAt.Equal(historyTestNow))
	})
	t.Run("rule 2: an unlisted blocked run stays blocked and finishes at now", func(t *testing.T) {
		e := openEntry(StatusBlocked)
		assert.True(t, e.Settle(Observation{Listed: false}, historyTestNow))
		assert.Equal(t, StatusBlocked, e.Status)
		assert.True(t, e.Closed)
		require.NotNil(t, e.FinishedAt)
		assert.True(t, e.FinishedAt.Equal(historyTestNow))
	})
	t.Run("rule 3: a new verdict that passes is succeeded and finishes at its time", func(t *testing.T) {
		e := openEntry(StatusRunning)
		recorded := historyTestNow
		assert.True(t, e.Settle(Observation{Listed: true, Verdict: passVerdict(recorded)}, historyTestNow))
		assert.Equal(t, StatusSucceeded, e.Status)
		require.NotNil(t, e.Verdict)
		assert.Equal(t, "PASS", e.Verdict.Verdict)
		require.NotNil(t, e.FinishedAt)
		assert.True(t, e.FinishedAt.Equal(recorded))
	})
	t.Run("rule 3: a re-recorded verdict that fails flips succeeded to failed", func(t *testing.T) {
		e := openEntry(StatusRunning)
		require.True(t, e.Settle(Observation{Listed: true, Verdict: passVerdict(historyTestNow)}, historyTestNow))
		later := historyTestNow.Add(time.Minute)
		assert.True(t, e.Settle(Observation{Listed: true, Verdict: failVerdict(later)}, later))
		assert.Equal(t, StatusFailed, e.Status)
		assert.Equal(t, "FAIL", e.Verdict.Verdict)
		assert.True(t, e.FinishedAt.Equal(later))
	})
	t.Run("rule 4: the same stored verdict changes nothing", func(t *testing.T) {
		e := openEntry(StatusRunning)
		require.True(t, e.Settle(Observation{Listed: true, Verdict: passVerdict(historyTestNow)}, historyTestNow))
		assert.False(t, e.Settle(Observation{Listed: true, Verdict: passVerdict(historyTestNow)}, historyTestNow.Add(time.Hour)))
		assert.Equal(t, StatusSucceeded, e.Status)
	})
	t.Run("rule 4: a stored verdict stops state changes", func(t *testing.T) {
		e := openEntry(StatusRunning)
		require.True(t, e.Settle(Observation{Listed: true, Verdict: passVerdict(historyTestNow)}, historyTestNow))
		assert.False(t, e.Settle(Observation{Listed: true, State: "working"}, historyTestNow))
		assert.Equal(t, StatusSucceeded, e.Status)
	})
	t.Run("rule 4: a stored PASS and an idle session change nothing", func(t *testing.T) {
		e := openEntry(StatusRunning)
		require.True(t, e.Settle(Observation{Listed: true, Verdict: passVerdict(historyTestNow)}, historyTestNow))
		assert.False(t, e.Settle(Observation{Listed: true, State: "idle"}, historyTestNow.Add(time.Hour)))
		assert.Equal(t, StatusSucceeded, e.Status)
	})
	t.Run("rule 3: a new BLOCKED verdict while working is stored and the run keeps running", func(t *testing.T) {
		e := openEntry(StatusRunning)
		assert.True(t, e.Settle(Observation{Listed: true, State: "working", Verdict: blockedVerdict(historyTestNow)}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
		require.NotNil(t, e.Verdict)
		assert.Equal(t, VerdictBlocked, e.Verdict.Verdict)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 5: an idle session after a BLOCKED verdict is blocked with no finish time", func(t *testing.T) {
		e := openEntry(StatusRunning)
		require.True(t, e.Settle(Observation{Listed: true, State: "working", Verdict: blockedVerdict(historyTestNow)}, historyTestNow))
		assert.True(t, e.Settle(Observation{Listed: true, State: "idle", Verdict: blockedVerdict(historyTestNow)}, historyTestNow))
		assert.Equal(t, StatusBlocked, e.Status)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 5: a blocked run whose session works again is running", func(t *testing.T) {
		e := openEntry(StatusBlocked)
		e.Verdict = blockedVerdict(historyTestNow)
		assert.True(t, e.Settle(Observation{Listed: true, State: "working", Verdict: blockedVerdict(historyTestNow)}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 5: an idle session with the same BLOCKED verdict is blocked again", func(t *testing.T) {
		e := openEntry(StatusRunning)
		e.Verdict = blockedVerdict(historyTestNow)
		assert.True(t, e.Settle(Observation{Listed: true, State: "idle", Verdict: blockedVerdict(historyTestNow)}, historyTestNow))
		assert.Equal(t, StatusBlocked, e.Status)
	})
	t.Run("rule 3: a PASS after a BLOCKED verdict succeeds the run at its time", func(t *testing.T) {
		e := openEntry(StatusBlocked)
		e.Verdict = blockedVerdict(historyTestNow)
		recorded := historyTestNow.Add(time.Hour)
		assert.True(t, e.Settle(Observation{Listed: true, State: "idle", Verdict: passVerdict(recorded)}, recorded))
		assert.Equal(t, StatusSucceeded, e.Status)
		require.NotNil(t, e.FinishedAt)
		assert.True(t, e.FinishedAt.Equal(recorded))
	})
	t.Run("rule 5: an idle session with no verdict needs attention with no finish time", func(t *testing.T) {
		e := openEntry(StatusRunning)
		assert.True(t, e.Settle(Observation{Listed: true, State: "idle"}, historyTestNow))
		assert.Equal(t, StatusNeedsAttention, e.Status)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 5: a run that needs attention is running again when its session works", func(t *testing.T) {
		e := openEntry(StatusNeedsAttention)
		assert.True(t, e.Settle(Observation{Listed: true, State: "working"}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 6: a stopped session with no verdict is done at now", func(t *testing.T) {
		e := openEntry(StatusNeedsAttention)
		assert.True(t, e.Settle(Observation{Listed: true, State: "stopped"}, historyTestNow))
		assert.Equal(t, StatusDone, e.Status)
		require.NotNil(t, e.FinishedAt)
		assert.True(t, e.FinishedAt.Equal(historyTestNow))
	})
	t.Run("rule 5: a working agent is running with no finish time", func(t *testing.T) {
		e := openEntry(StatusDone)
		finished := historyTestStart
		e.FinishedAt = &finished
		assert.True(t, e.Settle(Observation{Listed: true, State: "working"}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
		assert.Nil(t, e.FinishedAt)
	})
	t.Run("rule 5: a starting agent is running", func(t *testing.T) {
		e := openEntry(StatusDone)
		assert.True(t, e.Settle(Observation{Listed: true, State: "starting"}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
	})
	t.Run("rule 6: a stopped agent keeps an earlier finish time", func(t *testing.T) {
		e := openEntry(StatusRunning)
		finished := historyTestStart
		e.FinishedAt = &finished
		assert.True(t, e.Settle(Observation{Listed: true, State: "stopped"}, historyTestNow))
		assert.Equal(t, StatusDone, e.Status)
		assert.True(t, e.FinishedAt.Equal(historyTestStart))
	})
	t.Run("rule 6: a stopped agent already done is unchanged", func(t *testing.T) {
		e := openEntry(StatusDone)
		finished := historyTestStart
		e.FinishedAt = &finished
		assert.False(t, e.Settle(Observation{Listed: true, State: "stopped"}, historyTestNow))
	})
	t.Run("rule 7: an unknown state changes nothing", func(t *testing.T) {
		e := openEntry(StatusRunning)
		assert.False(t, e.Settle(Observation{Listed: true, State: "unknown"}, historyTestNow))
		assert.Equal(t, StatusRunning, e.Status)
	})
}

func TestHistory_Append(t *testing.T) {
	t.Run("numbers entries from 1 and counts each once", func(t *testing.T) {
		h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
		for range 3 {
			h.Append(Entry{Status: StatusDone, Trigger: TriggerTick, StartedAt: historyTestStart})
		}
		require.Len(t, h.Entries, 3)
		assert.Equal(t, 1, h.Entries[0].Seq)
		assert.Equal(t, 2, h.Entries[1].Seq)
		assert.Equal(t, 3, h.Entries[2].Seq)
		assert.Equal(t, 4, h.NextSeq)
		assert.Equal(t, 1, h.Entries[2].Repeats)
	})
	t.Run("coalesces a skip with the same exit code and message", func(t *testing.T) {
		h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
		gate := &Gate{Outcome: GateFailed, ExitCode: 1, Message: "no outdated modules"}
		h.Append(Entry{Status: StatusSkipped, Trigger: TriggerTick, StartedAt: historyTestStart, Gate: gate})
		later := historyTestStart.Add(time.Hour)
		h.Append(Entry{Status: StatusSkipped, Trigger: TriggerTick, StartedAt: later, Gate: gate})
		require.Len(t, h.Entries, 1)
		assert.Equal(t, 2, h.Entries[0].Repeats)
		require.NotNil(t, h.Entries[0].FinishedAt)
		assert.True(t, h.Entries[0].FinishedAt.Equal(later))
		assert.Equal(t, 2, h.NextSeq)
	})
	t.Run("keeps a skip with a different message as its own entry", func(t *testing.T) {
		h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
		h.Append(Entry{Status: StatusSkipped, StartedAt: historyTestStart, Gate: &Gate{ExitCode: 1, Message: "a"}})
		h.Append(Entry{Status: StatusSkipped, StartedAt: historyTestStart, Gate: &Gate{ExitCode: 1, Message: "b"}})
		assert.Len(t, h.Entries, 2)
		assert.Equal(t, 3, h.NextSeq)
	})
	t.Run("keeps the newest MaxHistory entries", func(t *testing.T) {
		h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
		for range MaxHistory + 1 {
			h.Append(Entry{Status: StatusRunning, Trigger: TriggerRun, StartedAt: historyTestStart})
		}
		require.Len(t, h.Entries, MaxHistory)
		assert.Equal(t, 2, h.Entries[0].Seq)
		assert.Equal(t, MaxHistory+1, h.Entries[MaxHistory-1].Seq)
	})
	t.Run("returns the stored entry", func(t *testing.T) {
		h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
		e := h.Append(Entry{Status: StatusRunning, StartedAt: historyTestStart})
		e.Workspace = "loop-deps-1a2b3c4d"
		assert.Equal(t, "loop-deps-1a2b3c4d", h.Entries[0].Workspace)
	})
}

func TestHistory_Find(t *testing.T) {
	h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
	h.Append(Entry{Status: StatusDone, Workspace: "loop-deps-1a2b3c4d"})
	h.Append(Entry{Status: StatusDone, Workspace: "loop-deps-5e6f7a8b"})
	t.Run("finds an entry by seq", func(t *testing.T) {
		e := h.Find(2)
		require.NotNil(t, e)
		assert.Equal(t, "loop-deps-5e6f7a8b", e.Workspace)
	})
	t.Run("a seq not in the history is nil", func(t *testing.T) {
		assert.Nil(t, h.Find(99))
	})
	t.Run("finds the newest entry of a workspace", func(t *testing.T) {
		h.Append(Entry{Status: StatusDone, Workspace: "loop-deps-1a2b3c4d"})
		e := h.FindWorkspace("loop-deps-1a2b3c4d")
		require.NotNil(t, e)
		assert.Equal(t, 3, e.Seq)
	})
	t.Run("an unknown workspace is nil", func(t *testing.T) {
		assert.Nil(t, h.FindWorkspace("loop-deps-00000000"))
	})
}

func TestSeedHistory(t *testing.T) {
	t.Run("an empty loop has an empty history", func(t *testing.T) {
		h := SeedHistory(nil)
		assert.Equal(t, HistoryVersion, h.Version)
		assert.Equal(t, 1, h.NextSeq)
		assert.NotNil(t, h.Entries)
		assert.Empty(t, h.Entries)
	})
	t.Run("one imported entry per run, oldest first", func(t *testing.T) {
		runs := []Run{
			{StartedAt: historyTestStart, Workspace: "loop-deps-1a2b3c4d", Session: "e8cdf5d8"},
			{StartedAt: historyTestStart.Add(time.Hour), ErrorCode: "claude_missing", ErrorMessage: "no claude"},
		}
		h := SeedHistory(runs)
		require.Len(t, h.Entries, 2)
		assert.Equal(t, 1, h.Entries[0].Seq)
		assert.Equal(t, 2, h.Entries[1].Seq)
		assert.Equal(t, TriggerImported, h.Entries[0].Trigger)
		assert.Equal(t, StatusRunning, h.Entries[0].Status)
		assert.Equal(t, "loop-deps-1a2b3c4d", h.Entries[0].Workspace)
		assert.Equal(t, "e8cdf5d8", h.Entries[0].Session)
		assert.Equal(t, StatusStartFailed, h.Entries[1].Status)
		assert.Equal(t, "claude_missing", h.Entries[1].ErrorCode)
		require.NotNil(t, h.Entries[1].FinishedAt)
		assert.True(t, h.Entries[1].FinishedAt.Equal(runs[1].StartedAt))
		assert.Equal(t, 3, h.NextSeq)
	})
}

func TestDataStore_ReadHistory(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	t.Run("a missing file is an empty history", func(t *testing.T) {
		h, found, err := d.ReadHistory("deps")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, HistoryVersion, h.Version)
		assert.Equal(t, 1, h.NextSeq)
		assert.NotNil(t, h.Entries)
	})
	t.Run("a version newer than this ws is ErrNewerData", func(t *testing.T) {
		require.NoError(t, writeFileAtomic(d.HistoryPath("deps"), []byte(`{"version":2,"next_seq":1,"entries":[]}`)))
		_, _, err := d.ReadHistory("deps")
		assert.ErrorIs(t, err, ErrNewerData)
	})
	t.Run("bad JSON names the file", func(t *testing.T) {
		require.NoError(t, writeFileAtomic(d.HistoryPath("deps"), []byte("{")))
		_, _, err := d.ReadHistory("deps")
		require.Error(t, err)
		assert.Contains(t, err.Error(), d.HistoryPath("deps"))
	})
	t.Run("a bad name is invalid", func(t *testing.T) {
		_, _, err := d.ReadHistory("../deps")
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestDataStore_WriteHistory(t *testing.T) {
	t.Parallel()
	t.Run("round trips through ReadHistory", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		h := History{NextSeq: 1, Entries: []Entry{}}
		h.Append(Entry{Status: StatusSucceeded, Trigger: TriggerTick, StartedAt: historyTestStart, FinishedAt: &historyTestNow, Workspace: "loop-deps-1a2b3c4d", Verdict: passVerdict(historyTestNow)})
		require.NoError(t, d.WriteHistory("deps", &h))
		got, found, err := d.ReadHistory("deps")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, HistoryVersion, got.Version)
		assert.Equal(t, 2, got.NextSeq)
		require.Len(t, got.Entries, 1)
		assert.Equal(t, StatusSucceeded, got.Entries[0].Status)
		assert.Equal(t, "loop-deps-1a2b3c4d", got.Entries[0].Workspace)
	})
	t.Run("deletes the report of the 51st-newest reported entry", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		h := History{NextSeq: 1, Entries: []Entry{}}
		for range MaxReports + 1 {
			e := h.Append(Entry{Status: StatusSucceeded, Trigger: TriggerTick, StartedAt: historyTestStart, Verdict: passVerdict(historyTestNow)})
			require.NoError(t, d.WriteReport("deps", e.Seq, []byte("report")))
		}
		require.NoError(t, d.WriteHistory("deps", &h))
		oldest := h.Find(1)
		require.NotNil(t, oldest)
		assert.False(t, oldest.Verdict.Report)
		_, found, err := d.ReadReport("deps", 1)
		require.NoError(t, err)
		assert.False(t, found)
		_, found, err = d.ReadReport("deps", 2)
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("deletes a report whose run was dropped from the history", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		require.NoError(t, d.WriteReport("deps", 999, []byte("gone")))
		h := History{NextSeq: 1, Entries: []Entry{}}
		require.NoError(t, d.WriteHistory("deps", &h))
		_, found, err := d.ReadReport("deps", 999)
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("refuses to overwrite a newer version", func(t *testing.T) {
		t.Parallel()
		d := newTestDataStore(t)
		require.NoError(t, writeFileAtomic(d.HistoryPath("deps"), []byte(`{"version":2,"next_seq":1,"entries":[]}`)))
		h := History{NextSeq: 1, Entries: []Entry{}}
		assert.ErrorIs(t, d.WriteHistory("deps", &h), ErrNewerData)
	})
}

func TestDataStore_WriteReport(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	t.Run("writes the report under reports/<seq>.md", func(t *testing.T) {
		require.NoError(t, d.WriteReport("deps", 7, []byte("# Bumped")))
		data, err := os.ReadFile(filepath.Join(d.Dir("deps"), "reports", "7.md"))
		require.NoError(t, err)
		assert.Equal(t, "# Bumped", string(data))
	})
	t.Run("a report over MaxReportSize is invalid", func(t *testing.T) {
		big := []byte(strings.Repeat("x", MaxReportSize+1))
		assert.ErrorIs(t, d.WriteReport("deps", 8, big), ErrInvalid)
	})
}

func TestDataStore_ReadReport(t *testing.T) {
	t.Parallel()
	d := newTestDataStore(t)
	t.Run("a missing report is not found", func(t *testing.T) {
		_, found, err := d.ReadReport("deps", 1)
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("reads back what was written", func(t *testing.T) {
		require.NoError(t, d.WriteReport("deps", 1, []byte("report")))
		report, found, err := d.ReadReport("deps", 1)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "report", string(report))
	})
}
