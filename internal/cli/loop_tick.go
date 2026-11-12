package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// defaultLoopPoll is how often ws loop tick --watch runs a pass.
const defaultLoopPoll = time.Minute

type loopAction string

const (
	loopStarted    loopAction = "started"
	loopWouldStart loopAction = "would_start"
	loopWaiting    loopAction = "waiting"
	loopPaused     loopAction = "paused"
	loopBusy       loopAction = "busy"
	loopAsleep     loopAction = "asleep"
	loopFailed     loopAction = "failed"
)

// tickJSON is one loop in ws loop tick's output.
type tickJSON struct {
	Name      string         `json:"name"`
	Action    loopAction     `json:"action"`
	Workspace *string        `json:"workspace"`
	Session   *string        `json:"session"`
	NextRunAt *string        `json:"next_run_at"`
	Error     *itemErrorJSON `json:"error"`
	Gate      *gateJSON      `json:"gate"`
}

// loopRunResult is ws loop run's output.
type loopRunResult struct {
	Name      string   `json:"name"`
	Workspace string   `json:"workspace"`
	Session   string   `json:"session"`
	Path      string   `json:"path"`
	Warnings  []string `json:"warnings"`
}

func (a *App) newLoopRunCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "run [--force] <NAME>",
		Short: "Start a run of a loop now",
		Args:  cobra.ExactArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runLoopRun(cmd.Context(), args[0], force)
		})),
	}
	cmd.Flags().BoolVar(&force, "force", false, "start the run without running the loop's script")
	return cmd
}

func (a *App) newLoopTickCmd() *cobra.Command {
	var dryRun, watch bool
	cmd := &cobra.Command{
		Use:   "tick [--dry-run] [--watch]",
		Short: "Start every loop that is due",
		Args:  cobra.NoArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			if !watch {
				return a.runLoopTick(cmd.Context(), dryRun)
			}
			if a.global.json {
				return usageErrorf("--watch prints text only; run ws loop tick --json without --watch")
			}
			return a.watchLoops(cmd.Context(), dryRun)
		})),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would start; start, write, and remove nothing")
	cmd.Flags().BoolVar(&watch, "watch", false, "run a pass every minute until interrupted, printing what started or failed")
	return cmd
}

// loopPass is one pass of ws loop tick: what it learned about the machine
// and the sessions, read once the first loop is due.
type loopPass struct {
	now     time.Time
	dryRun  bool
	read    bool
	awake   bool
	listing lifetime.Listing
	check   claudeCheck
}

// claudeCheck is Claude Code's check, made once and kept for the starts
// that follow it.
type claudeCheck struct {
	done bool
	err  error
}

func (c *claudeCheck) ready(ctx context.Context, client claudeClient) error {
	if !c.done {
		c.done = true
		c.err = client.Check(ctx)
	}
	return c.err
}

// runLoopRun starts a run of the loop now, whether or not it is paused,
// due, or busy. next_run_at stays as it is (ADR 0048, decision 4).
func (a *App) runLoopRun(ctx context.Context, name string, force bool) error {
	unlock, err := a.loops.Lock(ctx)
	if err != nil {
		return domainError(err)
	}
	defer unlock()
	l, err := a.loops.Get(name)
	if err != nil {
		return domainError(err)
	}
	var check claudeCheck
	trigger := loop.TriggerRun
	if force {
		trigger = loop.TriggerForce
	}
	run, ws, startErr := a.attemptRun(ctx, &l, &check, force)
	entry := historyEntry(&l, &run, startErr, trigger, a.now())
	a.recordAttempt(&l, &entry)
	if !errors.Is(startErr, loop.ErrSkipped) {
		l.AddRun(run)
	}
	saveErr := a.loops.Save(&l)
	if err := errors.Join(startErr, saveErr); err != nil {
		return domainError(err)
	}
	result := loopRunResult{Name: l.Name, Workspace: run.Workspace, Session: run.Session, Path: ws.Path, Warnings: a.warnings()}
	if a.global.json {
		return a.writeJSON(result)
	}
	fmt.Fprintf(a.stdout, "%s  %s  %s  %s\n", result.Name, result.Workspace, result.Session, result.Path)
	a.printWarnings()
	return nil
}

// runLoopTick runs one pass and prints its items.
func (a *App) runLoopTick(ctx context.Context, dryRun bool) error {
	items, results, err := a.tick(ctx, dryRun)
	if err != nil {
		return domainError(err)
	}
	return a.finishItems("loops", items, results)
}

// tick runs one pass (ADR 0048, decision 4): it starts every due loop that
// isn't asleep or busy, records each run, and returns one item per loop.
// With dryRun it changes nothing. Its failures are per item; the error is
// for a pass that couldn't read the loops or the sessions.
func (a *App) tick(ctx context.Context, dryRun bool) ([]tickJSON, *itemResults, error) {
	lock := a.loops.Lock
	if dryRun {
		lock = a.loops.RLock
	}
	unlock, err := lock(ctx)
	if err != nil {
		return nil, nil, domainError(err)
	}
	defer unlock()
	loops, warnings, err := a.loops.List()
	if err != nil {
		return nil, nil, domainError(err)
	}
	a.warned = append(a.warned, warnings...)
	p := &loopPass{now: a.now(), dryRun: dryRun}
	if !dryRun && a.hasOpenEntries(loops) {
		if err := a.readPass(ctx, p); err != nil {
			return nil, nil, err
		}
	}
	var results itemResults
	items := make([]tickJSON, 0, len(loops))
	for i := range loops {
		if !dryRun {
			a.settleHistory(&loops[i], p.listing)
		}
		item, err := a.tickLoop(ctx, &loops[i], p, &results)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	return items, &results, nil
}

// tickLoop decides what the pass does with l, starts its run if it is
// due, and records the item's outcome in results. A non-nil error is for
// the whole pass: the loop's record couldn't be read or saved.
func (a *App) tickLoop(ctx context.Context, l *loop.Loop, p *loopPass, results *itemResults) (tickJSON, error) {
	switch {
	case l.Paused:
		return addTick(results, newTickJSON(l, loopPaused), nil), nil
	case !l.Due(p.now):
		return addTick(results, newTickJSON(l, loopWaiting), nil), nil
	}
	if err := a.readPass(ctx, p); err != nil {
		return tickJSON{}, err
	}
	switch {
	case !p.awake:
		return addTick(results, newTickJSON(l, loopAsleep), nil), nil
	case p.busy(l):
		return addTick(results, newTickJSON(l, loopBusy), nil), nil
	case p.dryRun:
		return addTick(results, newTickJSON(l, loopWouldStart), nil), nil
	}
	prev := l.LastGate
	run, _, startErr := a.attemptRun(ctx, l, &p.check, false)
	skipped := errors.Is(startErr, loop.ErrSkipped)
	entry := historyEntry(l, &run, startErr, loop.TriggerTick, p.now)
	a.recordAttempt(l, &entry)
	if !skipped {
		l.AddRun(run)
	}
	if err := l.Advance(p.now); err != nil {
		return tickJSON{}, err
	}
	if err := a.loops.Save(l); err != nil {
		return tickJSON{}, domainError(err)
	}
	item := newTickJSON(l, loopStarted)
	if l.LastGate != prev {
		item.Gate = gateJSONOf(l.LastGate)
	}
	if skipped {
		item.Action = loopSkipped
		return addTick(results, item, nil), nil
	}
	if startErr == nil {
		a.pruneRuns(ctx, l, p.listing)
	}
	if startErr != nil {
		item.Action = loopFailed
	}
	item.Workspace = optional(run.Workspace)
	item.Session = optional(run.Session)
	return addTick(results, item, startErr), nil
}

// readPass reads, once per pass, whether the Mac is fully awake and what
// sessions exist. A failed awake check is a warning, and counts as asleep.
func (a *App) readPass(ctx context.Context, p *loopPass) error {
	if p.read {
		return nil
	}
	p.read = true
	awake, awakeErr := a.awake.Awake(ctx)
	if awakeErr != nil {
		a.warned = append(a.warned, fmt.Sprintf("checking whether the Mac is awake: %v", awakeErr))
	}
	p.awake = awakeErr == nil && awake
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return err
	}
	p.listing = listing
	return nil
}

// busy reports whether l's newest run's agent is still working or starting.
func (p *loopPass) busy(l *loop.Loop) bool {
	r := l.LastRun()
	if r == nil || r.Workspace == "" {
		return false
	}
	e := listedEntry(p.listing, r.Workspace)
	return e != nil && e.Session != nil && (e.Session.State == lifetime.StateWorking || e.Session.State == lifetime.StateStarting)
}

// listedEntry is the listed workspace with id, or nil.
func listedEntry(listing lifetime.Listing, id string) *lifetime.Entry {
	for i := range listing.Workspaces {
		if listing.Workspaces[i].Workspace.ID == id {
			return &listing.Workspaces[i]
		}
	}
	return nil
}

// attemptRun starts a run of l, after Claude Code's check and, unless
// force, l's script. A run that can't start is returned with its error, to
// be recorded. A script that exits non-zero starts nothing, so its error
// has no run to record.
func (a *App) attemptRun(ctx context.Context, l *loop.Loop, check *claudeCheck, force bool) (loop.Run, workspace.Workspace, error) {
	if err := check.ready(ctx, a.claude); err != nil {
		return failedRun(a.now(), err), workspace.Workspace{}, err
	}
	data := loop.PromptData{}
	if !force {
		var err error
		data, err = a.runGate(ctx, l)
		if errors.Is(err, loop.ErrSkipped) {
			return loop.Run{}, workspace.Workspace{}, err
		}
		if err != nil {
			return failedRun(a.now(), err), workspace.Workspace{}, err
		}
	}
	data.Memory = a.memoryValues(l)
	prompt, err := l.RenderPrompt(data)
	if err != nil {
		return failedRun(a.now(), err), workspace.Workspace{}, err
	}
	return a.startLoopRun(ctx, l, prompt)
}

// memoryValues is l's memory as a prompt sees it. A memory that can't be
// read is a warning, and the prompt sees none.
func (a *App) memoryValues(l *loop.Loop) map[string]string {
	mem, err := a.loopData.ReadMemory(l.Name)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("reading the memory of loop %s: %v", l.Name, err))
		return nil
	}
	return mem.Values()
}

// startLoopRun creates the run's workspace and starts its agent, as ws
// claude does, and returns the run to record. A failure is in the Run's
// ErrorCode/ErrorMessage and also returned.
func (a *App) startLoopRun(ctx context.Context, l *loop.Loop, prompt string) (loop.Run, workspace.Workspace, error) {
	ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: "loop-" + l.Name, Specs: parseSpecs(l.Specs), Hidden: l.RunsHidden()})
	if err != nil {
		return failedRun(a.now(), err), workspace.Workspace{}, domainError(err)
	}
	defer lock.Unlock()
	a.writeRunMark(ws.ID, l.Name)
	started, err := a.startAgent(ctx, &ws, loopClaudeArgs(l, prompt))
	if err != nil {
		return failedRun(a.now(), err), ws, err
	}
	return loop.Run{StartedAt: a.now().UTC(), Workspace: ws.ID, Session: started.ID}, ws, nil
}

// failedRun is the run of a start that failed, with its error.
func failedRun(at time.Time, err error) loop.Run {
	ce := asCLIError(domainError(err))
	return loop.Run{StartedAt: at.UTC(), ErrorCode: string(ce.code), ErrorMessage: ce.Error()}
}

// loopClaudeArgs are the arguments after `--` for ws claude: the loop's
// --model and --permission-mode if set, then the run's prompt.
func loopClaudeArgs(l *loop.Loop, prompt string) []string {
	var args []string
	if l.Model != "" {
		args = append(args, "--model", l.Model)
	}
	if l.PermissionMode != "" {
		args = append(args, "--permission-mode", l.PermissionMode)
	}
	return append(args, prompt)
}

// pruneRuns removes the workspaces of l's runs beyond the newest l.Keep,
// oldest first, except those whose agent is working or starting (ADR 0048,
// decision 5). The newest run counts as kept even though the listing was
// read before it started. A failure is a warning.
func (a *App) pruneRuns(ctx context.Context, l *loop.Loop, listing lifetime.Listing) {
	h, err := a.historyOf(l)
	if err != nil {
		a.warnHistory(l.Name, err)
	}
	now := a.now()
	historyChanged := false
	newest := len(l.Runs) - 1
	kept := 0
	for i := newest; i >= 0; i-- {
		r := &l.Runs[i]
		if r.Workspace == "" {
			continue
		}
		e := listedEntry(listing, r.Workspace)
		if e == nil && i != newest {
			continue
		}
		if kept < l.Keep {
			kept++
			continue
		}
		if e == nil || e.Session == nil || e.Session.State == lifetime.StateWorking || e.Session.State == lifetime.StateStarting {
			continue
		}
		if a.removeRun(ctx, l.Name, &h, listing, e, now) {
			historyChanged = true
		}
	}
	if historyChanged {
		a.writeHistory(l.Name, &h)
	}
}

// removeRun settles the history entry of e's run, removes its workspace, and
// marks the entry closed. It reports whether h changed. A failed removal is a
// warning, and the entry stays open.
func (a *App) removeRun(ctx context.Context, name string, h *loop.History, listing lifetime.Listing, e *lifetime.Entry, now time.Time) bool {
	entry := h.FindWorkspace(e.Workspace.ID)
	changed := entry != nil && a.settleEntry(name, entry, listing, now)
	if _, err := a.lifetime.RemoveTree(ctx, &e.Workspace, lifetime.TreeOptions{}); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("removing run %s of loop %s: %v", e.Workspace.ID, name, err))
		return changed
	}
	if entry != nil {
		entry.Closed = true
		return true
	}
	return changed
}

// watchLoops runs a pass every loopPoll until ctx ends. Each pass prints
// only the loops it started or failed, and its failures, on stderr; a
// failed pass doesn't end the watch.
func (a *App) watchLoops(ctx context.Context, dryRun bool) error {
	warned := 0
	for {
		items, _, err := a.tick(ctx, dryRun)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "ws: %v\n", domainError(err))
		}
		a.printChangedLoops(items)
		warned = a.printNewWarnings(warned)
		t := time.NewTimer(a.loopPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// printChangedLoops prints, for --watch, the loops a pass started or failed.
func (a *App) printChangedLoops(items []tickJSON) {
	for i := range items {
		it := &items[i]
		if it.Action == loopFailed {
			fmt.Fprintf(a.stderr, "ws: loop %s: %s\n", it.Name, it.Error.Message)
		}
		if it.Action == loopStarted {
			fmt.Fprintf(a.stdout, "%s  %s\n", formatTime(a.now()), it.text())
		}
	}
}

// newTickJSON is a loop's item before its run, if any, is known.
func newTickJSON(l *loop.Loop, action loopAction) tickJSON {
	item := tickJSON{Name: l.Name, Action: action}
	if !l.Paused {
		item.NextRunAt = optionalTime(l.NextRunAt)
	}
	return item
}

// addTick records item's outcome, err, in results, and returns the item
// with its error.
func addTick(results *itemResults, item tickJSON, err error) tickJSON {
	item.Error = results.add(item.text(), err)
	return item
}

// text is the item's line in text output.
func (t *tickJSON) text() string {
	return fmt.Sprintf("%s  %s  %s  next %s", t.Action, t.Name, orDash(t.Workspace), orDash(t.NextRunAt))
}

// orDash is s, or "-" when there is none.
func orDash(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// historyOf is l's history: its history.json, or, when there is none yet,
// one seeded from l's runs. It writes nothing.
func (a *App) historyOf(l *loop.Loop) (loop.History, error) {
	h, found, err := a.loopData.ReadHistory(l.Name)
	if err != nil {
		return loop.History{}, err
	}
	if !found {
		return loop.SeedHistory(l.Runs), nil
	}
	return h, nil
}

// hasOpenEntries reports whether any of loops has a run to settle. A history
// that can't be read counts as none; settleHistory warns about it.
func (a *App) hasOpenEntries(loops []loop.Loop) bool {
	for i := range loops {
		h, err := a.historyOf(&loops[i])
		if err != nil {
			continue
		}
		for j := range h.Entries {
			if h.Entries[j].Open() {
				return true
			}
		}
	}
	return false
}

// historyEntry is the entry for an attempt: skipped (Gate = l.LastGate),
// start_failed (ErrorCode/Message from run, FinishedAt = StartedAt), or
// running (Workspace, Session from run).
func historyEntry(l *loop.Loop, run *loop.Run, startErr error, trigger loop.Trigger, now time.Time) loop.Entry {
	switch {
	case errors.Is(startErr, loop.ErrSkipped):
		at := now
		if l.LastGate != nil {
			at = l.LastGate.At
		}
		return loop.Entry{Status: loop.StatusSkipped, Trigger: trigger, StartedAt: at, FinishedAt: &at, Gate: l.LastGate}
	case startErr != nil:
		at := run.StartedAt
		return loop.Entry{Status: loop.StatusStartFailed, Trigger: trigger, StartedAt: at, FinishedAt: &at, ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage}
	}
	return loop.Entry{Status: loop.StatusRunning, Trigger: trigger, StartedAt: run.StartedAt, Workspace: run.Workspace, Session: run.Session}
}

// recordAttempt appends e to l's history. A failure is a warning.
func (a *App) recordAttempt(l *loop.Loop, e *loop.Entry) {
	h, err := a.historyOf(l)
	if err != nil {
		a.warnHistory(l.Name, err)
		return
	}
	h.Append(*e)
	a.writeHistory(l.Name, &h)
}

// settleHistory settles l's open runs against listing and their machine
// records, copies new reports, and writes the history if anything changed.
// A failure is a warning.
func (a *App) settleHistory(l *loop.Loop, listing lifetime.Listing) {
	h, err := a.historyOf(l)
	if err != nil {
		a.warnHistory(l.Name, err)
		return
	}
	now := a.now()
	changed := false
	for i := range h.Entries {
		if h.Entries[i].Open() && a.settleEntry(l.Name, &h.Entries[i], listing, now) {
			changed = true
		}
	}
	if changed {
		a.writeHistory(l.Name, &h)
	}
}

// settleEntry applies what listing and e's machine record say to e, and
// reports whether e changed. A new verdict's report is copied out.
func (a *App) settleEntry(name string, e *loop.Entry, listing lifetime.Listing, now time.Time) bool {
	obs, reportPath := a.observe(e, listing)
	newVerdict := obs.Verdict != nil && (e.Verdict == nil || !e.Verdict.RecordedAt.Equal(obs.Verdict.RecordedAt))
	if !e.Settle(obs, now) {
		return false
	}
	if newVerdict && reportPath != "" {
		a.copyReport(name, e, reportPath)
	}
	return true
}

// observe is what listing and the run's machine record say about e, and the
// path of the record's report. A record that can't be read is a warning, and
// leaves the verdict out.
func (a *App) observe(e *loop.Entry, listing lifetime.Listing) (loop.Observation, string) {
	listed := listedEntry(listing, e.Workspace)
	if listed == nil {
		return loop.Observation{}, ""
	}
	obs := loop.Observation{Listed: true}
	if listed.Session != nil {
		obs.State = string(listed.Session.State)
	}
	rec, err := machine.NewStore(a.loopData.StateDir()).Read(e.Workspace, e.Workspace)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("reading the result of run %s: %v", e.Workspace, err))
		return obs, ""
	}
	if rec.Result != nil {
		outcome, _ := rec.Outcome()
		obs.Verdict = &loop.Verdict{Verdict: rec.Result.Verdict, Outcome: string(outcome), RecordedAt: rec.Result.RecordedAt}
	}
	return obs, rec.ReportPath
}

// copyReport saves the report at path as reports/<seq>.md and marks e's
// verdict as kept with it. A failure is a warning, and the verdict is kept
// without its report.
func (a *App) copyReport(name string, e *loop.Entry, path string) {
	report, err := readCapped(path, loop.MaxReportSize)
	if err == nil {
		err = a.loopData.WriteReport(name, e.Seq, report)
	}
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("saving the report of run %d of loop %s: %v", e.Seq, name, err))
		return
	}
	e.Verdict.Report = true
}

// readCapped reads at most limit bytes of the file at path.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// writeRunMark records that the workspace id is a run of loop name. A failure
// is a warning.
func (a *App) writeRunMark(id, name string) {
	mark := loop.RunMark{Loop: name, StartedAt: a.now().UTC()}
	if err := a.loopData.WriteRunMark(id, mark); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("recording run %s of loop %s: %v", id, name, err))
	}
}

// writeHistory writes l's history. A failure is a warning.
func (a *App) writeHistory(name string, h *loop.History) {
	if err := a.loopData.WriteHistory(name, h); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("writing the history of loop %s: %v", name, err))
	}
}

// warnHistory is the warning for a history that couldn't be read.
func (a *App) warnHistory(name string, err error) {
	a.warned = append(a.warned, fmt.Sprintf("reading the history of loop %s: %v", name, err))
}
