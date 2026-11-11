package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RunStatus is where a run of a loop stands.
type RunStatus string

// Statuses of a run, as ADR 0056 defines them.
const (
	StatusRunning     RunStatus = "running"
	StatusDone        RunStatus = "done"
	StatusSucceeded   RunStatus = "succeeded"
	StatusFailed      RunStatus = "failed"
	StatusStartFailed RunStatus = "start_failed"
	StatusSkipped     RunStatus = "skipped"
	StatusGone        RunStatus = "gone"
)

// Trigger is what started a run.
type Trigger string

// Triggers of a run.
const (
	TriggerTick     Trigger = "tick"
	TriggerRun      Trigger = "run"
	TriggerForce    Trigger = "run_force"
	TriggerImported Trigger = "imported"
)

// Bounds of a loop's history and its saved reports.
const (
	HistoryVersion = 1
	MaxHistory     = 200
	MaxReports     = 50
	MaxReportSize  = 1 << 20
)

// Verdict is the result a run recorded with ws machine result.
type Verdict struct {
	Verdict    string    `json:"verdict"`
	Outcome    string    `json:"outcome"` // "pass" or "fail"
	RecordedAt time.Time `json:"recorded_at"`
	Report     bool      `json:"report"` // reports/<seq>.md was kept
}

// Entry is one attempt to run a loop, as recorded in history.json.
type Entry struct {
	Seq          int        `json:"seq"`
	Status       RunStatus  `json:"status"`
	Trigger      Trigger    `json:"trigger"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Workspace    string     `json:"workspace"`
	Session      string     `json:"session"`
	Closed       bool       `json:"closed"`
	ErrorCode    string     `json:"error_code"`
	ErrorMessage string     `json:"error_message"`
	Gate         *Gate      `json:"gate"`
	Repeats      int        `json:"repeats"`
	Verdict      *Verdict   `json:"verdict"`
}

// History is a loop's runs, as history.json holds them.
type History struct {
	Version int     `json:"version"`
	NextSeq int     `json:"next_seq"`
	Entries []Entry `json:"entries"` // oldest first
}

// Observation is what a tick saw of an open run.
type Observation struct {
	Listed  bool     // the run's workspace is in the listing
	State   string   // its session's lifetime state, "" without a session
	Verdict *Verdict // from its result.toml, nil without one (Report is ignored)
}

// ReadHistory reads the loop's history.json. found is false, and the history
// empty, when there is no file. A version newer than HistoryVersion is
// ErrNewerData.
func (d *DataStore) ReadHistory(name string) (h History, found bool, err error) {
	if err = ValidateName(name); err != nil {
		return History{}, false, err
	}
	path := d.HistoryPath(name)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}, false, nil
	}
	if err != nil {
		return History{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return History{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if h.Version > HistoryVersion {
		return History{}, false, fmt.Errorf("%s: %w", path, ErrNewerData)
	}
	if h.Entries == nil {
		h.Entries = []Entry{}
	}
	return h, true, nil
}

// WriteHistory writes the loop's history.json. It refuses to replace a file
// of a newer version, first drops the reports that trimReports releases and
// any other report whose run is no longer in the history, then writes the
// file atomically.
func (d *DataStore) WriteHistory(name string, h *History) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, _, err := d.ReadHistory(name); err != nil {
		return fmt.Errorf("checking history of loop %s before writing: %w", name, err)
	}
	h.Version = HistoryVersion
	if h.Entries == nil {
		h.Entries = []Entry{}
	}
	if err := d.removeReports(name, h); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding history of loop %s: %w", name, err)
	}
	return writeFileAtomic(d.HistoryPath(name), append(data, '\n'))
}

// removeReports deletes the reports that h no longer keeps.
func (d *DataStore) removeReports(name string, h *History) error {
	for _, seq := range h.trimReports() {
		if err := os.Remove(d.ReportPath(name, seq)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing report %d of loop %s: %w", seq, name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(d.Dir(name), "reports"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading reports of loop %s: %w", name, err)
	}
	for _, entry := range entries {
		seq, ok := reportSeq(entry.Name())
		if !ok || h.Find(seq) != nil {
			continue
		}
		if err := os.Remove(d.ReportPath(name, seq)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing report %d of loop %s: %w", seq, name, err)
		}
	}
	return nil
}

// reportSeq is the run seq of a report file name, <seq>.md.
func reportSeq(fileName string) (int, bool) {
	stem, ok := strings.CutSuffix(fileName, ".md")
	if !ok {
		return 0, false
	}
	seq, err := strconv.Atoi(stem)
	return seq, err == nil
}

// WriteReport writes a run's report to reports/<seq>.md atomically. A report
// over MaxReportSize is ErrInvalid.
func (d *DataStore) WriteReport(name string, seq int, report []byte) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if len(report) > MaxReportSize {
		return fmt.Errorf("%w: report of run %d is over %d bytes", ErrInvalid, seq, MaxReportSize)
	}
	return writeFileAtomic(d.ReportPath(name, seq), report)
}

// ReadReport reads a run's saved report. found is false when there is none.
func (d *DataStore) ReadReport(name string, seq int) (report []byte, found bool, err error) {
	if err = ValidateName(name); err != nil {
		return nil, false, err
	}
	path := d.ReportPath(name, seq)
	report, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	return report, true, nil
}

// Append adds e and returns a pointer to the stored entry. A skipped e
// whose newest entry is skipped with the same gate exit code and message
// is folded into that entry: Repeats goes up and FinishedAt becomes e's
// start. Otherwise e takes the next seq, and the oldest entries past
// MaxHistory are dropped.
//
//nolint:gocritic // hugeParam: Append takes the entry by value, as the history API is written.
func (h *History) Append(e Entry) *Entry {
	if last := h.repeatTarget(e.Status, e.Gate); last != nil {
		last.Repeats++
		finished := e.StartedAt
		last.FinishedAt = &finished
		return last
	}
	e.Seq = h.NextSeq
	h.NextSeq++
	if e.Repeats == 0 {
		e.Repeats = 1
	}
	h.Entries = append(h.Entries, e)
	if extra := len(h.Entries) - MaxHistory; extra > 0 {
		h.Entries = slices.Clone(h.Entries[extra:])
	}
	return &h.Entries[len(h.Entries)-1]
}

// repeatTarget is the newest entry that a skip with this status and gate
// repeats, or nil.
func (h *History) repeatTarget(status RunStatus, gate *Gate) *Entry {
	if status != StatusSkipped || gate == nil || len(h.Entries) == 0 {
		return nil
	}
	last := &h.Entries[len(h.Entries)-1]
	if last.Status != StatusSkipped || last.Gate == nil {
		return nil
	}
	if last.Gate.ExitCode != gate.ExitCode || last.Gate.Message != gate.Message {
		return nil
	}
	return last
}

// Find is the entry with seq, or nil.
func (h *History) Find(seq int) *Entry {
	for i := range h.Entries {
		if h.Entries[i].Seq == seq {
			return &h.Entries[i]
		}
	}
	return nil
}

// FindWorkspace is the newest entry whose workspace is id, or nil.
func (h *History) FindWorkspace(id string) *Entry {
	for i := len(h.Entries) - 1; i >= 0; i-- {
		if h.Entries[i].Workspace == id {
			return &h.Entries[i]
		}
	}
	return nil
}

// trimReports drops the report of every entry past the newest MaxReports
// that keep one. It returns the seqs it dropped.
func (h *History) trimReports() []int {
	var dropped []int
	kept := 0
	for i := len(h.Entries) - 1; i >= 0; i-- {
		e := &h.Entries[i]
		if e.Verdict == nil || !e.Verdict.Report {
			continue
		}
		kept++
		if kept <= MaxReports {
			continue
		}
		e.Verdict.Report = false
		dropped = append(dropped, e.Seq)
	}
	return dropped
}

// Open reports whether e still has a workspace to check.
func (e *Entry) Open() bool {
	return e.Workspace != "" && !e.Closed
}

// Settle applies obs at now by the status rules in reference.md, and
// reports whether e changed.
func (e *Entry) Settle(obs Observation, now time.Time) bool {
	if !e.Open() {
		return false
	}
	if !obs.Listed {
		return e.close(now)
	}
	if obs.Verdict != nil && e.verdictChanged(obs.Verdict) {
		e.recordVerdict(obs.Verdict)
		return true
	}
	if e.Verdict != nil {
		return false
	}
	switch obs.State {
	case "working", "starting":
		return e.setState(StatusRunning, nil)
	case "idle", "stopped":
		finished := e.FinishedAt
		if finished == nil {
			finished = &now
		}
		return e.setState(StatusDone, finished)
	}
	return false
}

// close marks a workspace that is no longer listed as closed. A run still
// running is gone.
func (e *Entry) close(now time.Time) bool {
	e.Closed = true
	if e.Status == StatusRunning {
		e.Status = StatusGone
		e.FinishedAt = &now
	}
	return true
}

func (e *Entry) verdictChanged(v *Verdict) bool {
	return e.Verdict == nil ||
		!e.Verdict.RecordedAt.Equal(v.RecordedAt) ||
		e.Verdict.Verdict != v.Verdict ||
		e.Verdict.Outcome != v.Outcome
}

func (e *Entry) recordVerdict(v *Verdict) {
	stored := *v
	e.Verdict = &stored
	e.Status = StatusFailed
	if v.Outcome == "pass" {
		e.Status = StatusSucceeded
	}
	recorded := v.RecordedAt
	e.FinishedAt = &recorded
}

// setState sets the status and finish time, and reports whether either
// changed.
func (e *Entry) setState(status RunStatus, finished *time.Time) bool {
	if e.Status == status && sameTime(e.FinishedAt, finished) {
		return false
	}
	e.Status = status
	e.FinishedAt = finished
	return true
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// SeedHistory is the history of a loop that has none yet: one entry per
// run, oldest first, all imported. A run that couldn't start is
// start_failed; the others are running.
func SeedHistory(runs []Run) History {
	h := History{Version: HistoryVersion, NextSeq: 1, Entries: []Entry{}}
	for _, r := range runs {
		e := Entry{
			Status:       StatusRunning,
			Trigger:      TriggerImported,
			StartedAt:    r.StartedAt,
			Workspace:    r.Workspace,
			Session:      r.Session,
			ErrorCode:    r.ErrorCode,
			ErrorMessage: r.ErrorMessage,
		}
		if r.ErrorCode != "" {
			e.Status = StatusStartFailed
			finished := r.StartedAt
			e.FinishedAt = &finished
		}
		h.Append(e)
	}
	return h
}
