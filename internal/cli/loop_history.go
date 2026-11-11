package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/loop"
)

// Bounds of ws loop history's --limit.
const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = loop.MaxHistory
)

// loopHistoryOptions are ws loop history's flags. limitSet is whether
// --limit was given, since its default is not a choice.
type loopHistoryOptions struct {
	limit    int
	limitSet bool
	run      int
	clear    bool
}

// loopHistoryJSON is ws loop history's output: a loop's newest runs.
type loopHistoryJSON struct {
	Loop     string               `json:"loop"`
	Runs     []loopHistoryRunJSON `json:"runs"`
	Total    int                  `json:"total"`
	Warnings []string             `json:"warnings"`
}

// loopHistoryRunJSON is one run in ws loop history's output.
type loopHistoryRunJSON struct {
	Seq             int              `json:"seq"`
	Status          string           `json:"status"`
	Trigger         string           `json:"trigger"`
	StartedAt       string           `json:"started_at"`
	FinishedAt      *string          `json:"finished_at"`
	DurationSeconds *int64           `json:"duration_seconds"`
	Workspace       *string          `json:"workspace"`
	WorkspaceExists bool             `json:"workspace_exists"`
	Session         *string          `json:"session"`
	Error           *itemErrorJSON   `json:"error"`
	Gate            *gateJSON        `json:"gate"`
	Repeats         int              `json:"repeats"`
	Verdict         *loopVerdictJSON `json:"verdict"`
}

// loopVerdictJSON is a run's verdict in ws loop history's output.
type loopVerdictJSON struct {
	Verdict    string  `json:"verdict"`
	Outcome    string  `json:"outcome"`
	RecordedAt string  `json:"recorded_at"`
	Report     *string `json:"report"`
}

// loopHistoryDetailJSON is ws loop history --run's output.
type loopHistoryDetailJSON struct {
	Loop       string             `json:"loop"`
	Run        loopHistoryRunJSON `json:"run"`
	ReportText *string            `json:"report_text"`
	Warnings   []string           `json:"warnings"`
}

// loopClearedJSON is ws loop history --clear's output.
type loopClearedJSON struct {
	Loop     string   `json:"loop"`
	Removed  int      `json:"removed"`
	Warnings []string `json:"warnings"`
}

func (a *App) newLoopHistoryCmd() *cobra.Command {
	var opts loopHistoryOptions
	cmd := &cobra.Command{
		Use:   "history [--limit <N>] [--run <SEQ> | --clear] <NAME>",
		Short: "Show a loop's runs, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.limitSet = cmd.Flags().Changed("limit")
			return a.runLoopHistory(cmd.Context(), args[0], opts)
		})),
	}
	cmd.Flags().IntVar(&opts.limit, "limit", defaultHistoryLimit, "how many runs to show, 1 to 200")
	cmd.Flags().IntVar(&opts.run, "run", 0, "show one run, with its report")
	cmd.Flags().BoolVar(&opts.clear, "clear", false, "delete the loop's run history and saved reports")
	return cmd
}

// check is the usage error for flags that can't be used together, or are out
// of range.
func (o loopHistoryOptions) check() error {
	switch {
	case o.clear && (o.run != 0 || o.limitSet):
		return usageErrorf("--clear takes no --run or --limit")
	case o.run < 0:
		return usageErrorf("--run must be 1 or more")
	case o.run != 0 && o.limitSet:
		return usageErrorf("--run shows one run, so it takes no --limit")
	case o.limitSet && (o.limit < 1 || o.limit > maxHistoryLimit):
		return usageErrorf("--limit must be from 1 to %d", maxHistoryLimit)
	}
	return nil
}

// runLoopHistory prints a loop's newest runs, or one run with its report, or
// clears the loop's history.
func (a *App) runLoopHistory(ctx context.Context, name string, opts loopHistoryOptions) error {
	if err := opts.check(); err != nil {
		return err
	}
	if opts.clear {
		return a.clearLoopHistory(ctx, name)
	}
	l, err := a.loops.Get(name)
	if err != nil {
		return domainError(err)
	}
	h, err := a.historyOf(&l)
	if err != nil {
		a.warnHistory(name, err)
	}
	if opts.run != 0 {
		return a.showLoopRun(name, &h, opts.run)
	}
	return a.listLoopHistory(name, &h, opts.limit)
}

// listLoopHistory prints the newest limit runs of h.
func (a *App) listLoopHistory(name string, h *loop.History, limit int) error {
	entries := newestEntries(h, limit)
	if a.global.json {
		runs := make([]loopHistoryRunJSON, 0, len(entries))
		for _, e := range entries {
			runs = append(runs, a.historyRunJSONOf(name, e))
		}
		return a.writeJSON(loopHistoryJSON{Loop: name, Runs: runs, Total: len(h.Entries), Warnings: a.warnings()})
	}
	for _, e := range entries {
		fmt.Fprintln(a.stdout, historyLine(e))
	}
	a.printWarnings()
	return nil
}

// showLoopRun prints run seq of h, and its report when it has one.
func (a *App) showLoopRun(name string, h *loop.History, seq int) error {
	e := h.Find(seq)
	if e == nil {
		return newError(codeLoopNotFound, fmt.Errorf("run %d of loop %s not found", seq, name))
	}
	var text *string
	report, found, err := a.loopData.ReadReport(name, seq)
	switch {
	case err != nil:
		a.warned = append(a.warned, fmt.Sprintf("reading the report of run %d of loop %s: %v", seq, name, err))
	case found:
		s := string(report)
		text = &s
	}
	if a.global.json {
		return a.writeJSON(loopHistoryDetailJSON{Loop: name, Run: a.historyRunJSONOf(name, e), ReportText: text, Warnings: a.warnings()})
	}
	fmt.Fprintln(a.stdout, historyLine(e))
	if text != nil {
		fmt.Fprintf(a.stdout, "\n%s\n", strings.TrimRight(*text, "\n"))
	}
	a.printWarnings()
	return nil
}

// clearLoopHistory empties the loop's history, keeping its next seq, and
// deletes its saved reports. It holds the loops lock.
func (a *App) clearLoopHistory(ctx context.Context, name string) error {
	unlock, err := a.loops.Lock(ctx)
	if err != nil {
		return domainError(err)
	}
	defer unlock()
	l, err := a.loops.Get(name)
	if err != nil {
		return domainError(err)
	}
	h, err := a.historyOf(&l)
	if err != nil {
		return domainError(err)
	}
	removed := len(h.Entries)
	h.Entries = []loop.Entry{}
	if err := a.loopData.WriteHistory(name, &h); err != nil {
		return domainError(err)
	}
	if err := os.RemoveAll(filepath.Join(a.loopData.Dir(name), "reports")); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("removing the reports of loop %s: %v", name, err))
	}
	if a.global.json {
		return a.writeJSON(loopClearedJSON{Loop: name, Removed: removed, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "%d runs removed\n", removed)
	a.printWarnings()
	return nil
}

// newestEntries is the newest limit entries of h, newest first.
func newestEntries(h *loop.History, limit int) []*loop.Entry {
	out := make([]*loop.Entry, 0, min(limit, len(h.Entries)))
	for i := len(h.Entries) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, &h.Entries[i])
	}
	return out
}

// historyRunJSONOf is e as ws loop history prints a run.
func (a *App) historyRunJSONOf(name string, e *loop.Entry) loopHistoryRunJSON {
	out := loopHistoryRunJSON{
		Seq:             e.Seq,
		Status:          string(e.Status),
		Trigger:         string(e.Trigger),
		StartedAt:       formatTime(e.StartedAt),
		Workspace:       optional(e.Workspace),
		WorkspaceExists: e.Open(),
		Session:         optional(e.Session),
		Gate:            gateJSONOf(e.Gate),
		Repeats:         e.Repeats,
	}
	if e.FinishedAt != nil {
		finished := formatTime(*e.FinishedAt)
		out.FinishedAt = &finished
	}
	if d, ok := historyDuration(e); ok {
		seconds := int64(d / time.Second)
		out.DurationSeconds = &seconds
	}
	if e.ErrorCode != "" {
		out.Error = &itemErrorJSON{Code: errorCode(e.ErrorCode), Message: e.ErrorMessage}
	}
	if v := e.Verdict; v != nil {
		out.Verdict = &loopVerdictJSON{Verdict: v.Verdict, Outcome: v.Outcome, RecordedAt: formatTime(v.RecordedAt), Report: a.savedReport(name, e.Seq)}
	}
	return out
}

// savedReport is the absolute path of run seq's saved report, or nil when
// there is no file.
func (a *App) savedReport(name string, seq int) *string {
	path := a.loopData.ReportPath(name, seq)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return &path
}

// historyDuration is how long a run took, for the statuses that finish with a
// time; false otherwise.
func historyDuration(e *loop.Entry) (time.Duration, bool) {
	if e.FinishedAt == nil {
		return 0, false
	}
	switch e.Status {
	case loop.StatusDone, loop.StatusSucceeded, loop.StatusFailed, loop.StatusGone, loop.StatusBlocked:
		return e.FinishedAt.Sub(e.StartedAt), true
	case loop.StatusRunning, loop.StatusStartFailed, loop.StatusSkipped, loop.StatusNeedsAttention:
		return 0, false
	}
	return 0, false
}

// historyLine is a run as ws loop history prints it in text.
func historyLine(e *loop.Entry) string {
	status := string(e.Status)
	if e.Repeats > 1 {
		status += fmt.Sprintf(" ×%d", e.Repeats)
	}
	return fmt.Sprintf("%-4d  %-12s  %-20s  %-7s  %-24s  %s",
		e.Seq, status, formatTime(e.StartedAt), historyDurationText(e), orDash(optional(e.Workspace)), historyResult(e))
}

// historyDurationText is a run's duration as ws loop history prints it, or
// "-". It trims the zero units as loop.FormatEvery does, but keeps seconds.
func historyDurationText(e *loop.Entry) string {
	d, ok := historyDuration(e)
	if !ok {
		return "-"
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// historyResult is how a run ended, as ws loop history prints it: its verdict,
// its error, the gate's exit for a skip, or "-".
func historyResult(e *loop.Entry) string {
	switch {
	case e.Verdict != nil:
		return e.Verdict.Verdict
	case e.ErrorCode != "":
		return withMessage(e.ErrorCode, e.ErrorMessage)
	case e.Status == loop.StatusSkipped && e.Gate != nil:
		return withMessage(fmt.Sprintf("exit %d", e.Gate.ExitCode), e.Gate.Message)
	}
	return "-"
}

// withMessage is head, then ": " and msg when msg isn't empty.
func withMessage(head, msg string) string {
	if msg == "" {
		return head
	}
	return head + ": " + msg
}
