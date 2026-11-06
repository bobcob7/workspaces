package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// defaultMachinePoll is how often ws machine wait reads the sessions.
const defaultMachinePoll = 5 * time.Second

// defaultMachineWaitTimeout is ws machine wait's --timeout default.
const defaultMachineWaitTimeout = 60 * time.Minute

type machineWaitOptions struct {
	any     bool
	timeout time.Duration
}

// waitTarget is a child ws machine wait waits on.
type waitTarget struct {
	id   string
	gone bool // named by id after its workspace was removed
	// quiet is whether the previous poll saw it idle or stopped with no
	// busy descendant.
	quiet bool
}

// machineWaitItemJSON is one child in ws machine wait's output.
type machineWaitItemJSON struct {
	ID       string                 `json:"id"`
	Session  *string                `json:"session"`
	Role     *string                `json:"role"`
	Status   string                 `json:"status"`
	Finished bool                   `json:"finished"`
	Outcome  string                 `json:"outcome"`
	Result   *machineResultInfoJSON `json:"result"`
	Error    *itemErrorJSON         `json:"error"`
}

// childState is one poll's view of a target.
type childState struct {
	status   string
	session  string
	quiet    bool // idle or stopped, and no descendant working or starting
	finished bool
	record   machine.Record
}

func (a *App) newMachineWaitCmd() *cobra.Command {
	var opts machineWaitOptions
	cmd := &cobra.Command{
		Use:   "wait [--any] [--timeout <DURATION>] <WORKSPACE>...",
		Short: "Wait for children to finish, then print each one's verdict",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageErrorf("%s needs a workspace", commandName(cmd))
			}
			return nil
		},
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runMachineWait(cmd.Context(), args, &opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.any, "any", false, "return when any one child has finished, not all of them")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultMachineWaitTimeout, "give up after this long, such as 90m")
	return cmd
}

// runMachineWait resolves the children named in args, then polls them until
// they are finished, and prints each one's status and result.
func (a *App) runMachineWait(ctx context.Context, args []string, opts *machineWaitOptions) error {
	if opts.timeout <= 0 {
		return usageErrorf("--timeout must be positive, got %s", opts.timeout)
	}
	if len(args) == 0 {
		return usageErrorf("ws machine wait needs a workspace")
	}
	caller, err := a.caller()
	if err != nil {
		return err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	root := machine.Root(list, caller)
	store := machine.NewStore(a.paths.State)
	targets, err := a.waitTargets(args, list, caller, root, store)
	if err != nil {
		return err
	}
	return a.waitForChildren(ctx, targets, root, store, opts)
}

// waitTargets resolves args, in order, without duplicates. Each must be a
// descendant of caller, or a child removed with its result kept under root.
func (a *App) waitTargets(args []string, list []workspace.Workspace, caller, root string, store *machine.Store) ([]waitTarget, error) {
	below := descendants(list, caller)
	targets := make([]waitTarget, 0, len(args))
	listed := map[string]bool{}
	for _, arg := range args {
		t, err := a.waitTargetOf(arg, below, caller, root, store)
		if err != nil {
			return nil, err
		}
		if listed[t.id] {
			continue
		}
		listed[t.id] = true
		targets = append(targets, t)
	}
	return targets, nil
}

// waitTargetOf resolves one argument of ws machine wait.
func (a *App) waitTargetOf(arg string, below map[string]bool, caller, root string, store *machine.Store) (waitTarget, error) {
	ws, err := a.workspaces.Find(arg)
	switch {
	case err == nil && below[ws.ID]:
		return waitTarget{id: ws.ID}, nil
	case err == nil:
		return waitTarget{}, newError(codeNotDescendant, fmt.Errorf("%s is not a workspace this one started", ws.ID))
	case !errors.Is(err, workspace.ErrNotFound):
		return waitTarget{}, domainError(err)
	}
	rec, readErr := store.Read(root, arg)
	if readErr != nil {
		return waitTarget{}, fmt.Errorf("reading %s's machine record: %w", arg, readErr)
	}
	if rec.Found() && slices.Contains(rec.Ancestors(), caller) {
		return waitTarget{id: arg, gone: true}, nil
	}
	return waitTarget{}, domainError(err)
}

// waitForChildren polls every target until they are finished (or one is,
// with --any), or the timeout passes, then prints them.
func (a *App) waitForChildren(ctx context.Context, targets []waitTarget, root string, store *machine.Store, opts *machineWaitOptions) error {
	timer := time.NewTimer(opts.timeout)
	defer timer.Stop()
	warnedRead := map[string]bool{}
	for {
		listing, err := a.lifetime.List(ctx)
		if err != nil {
			return err
		}
		states := a.pollChildren(targets, listing.Workspaces, root, store, warnedRead)
		if waitDone(states, opts) {
			return a.printWait(targets, states, opts)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for children: %w", ctx.Err())
		case <-timer.C:
			return a.printWait(targets, states, opts)
		case <-time.After(a.machinePoll):
		}
	}
}

// pollChildren is one poll's state for each target, in order. A record that
// can't be read is one warning per target, and an empty record.
func (a *App) pollChildren(targets []waitTarget, entries []lifetime.Entry, root string, store *machine.Store, warnedRead map[string]bool) []childState {
	states := make([]childState, len(targets))
	for i := range targets {
		id := targets[i].id
		rec, err := store.Read(root, id)
		if err != nil {
			if !warnedRead[id] {
				warnedRead[id] = true
				a.warned = append(a.warned, fmt.Sprintf("couldn't read %s's machine record: %v", id, err))
			}
			rec = machine.Record{Child: id}
		}
		states[i] = pollChild(&targets[i], entries, rec)
	}
	return states
}

// pollChild is t's state from entries and its record, and records whether
// it was quiet for the next poll.
func pollChild(t *waitTarget, entries []lifetime.Entry, rec machine.Record) childState {
	entry, found := entryByID(entries, t.id)
	if t.gone || !found {
		return childState{status: "gone", finished: true, record: rec}
	}
	var st childState
	st.record = rec
	if entry.Session == nil {
		st.status = string(lifetime.StateStarting)
	} else {
		st.status = string(entry.Session.State)
		st.session = entry.Session.ID
	}
	idle := st.status == string(lifetime.StateIdle) || st.status == string(lifetime.StateStopped)
	st.quiet = idle && !busyDescendant(entries, t.id)
	st.finished = st.quiet && (rec.Result != nil || t.quiet)
	t.quiet = st.quiet
	return st
}

// entryByID is the entry for workspace id in entries, if there is one.
func entryByID(entries []lifetime.Entry, id string) (lifetime.Entry, bool) {
	for i := range entries {
		if entries[i].Workspace.ID == id {
			return entries[i], true
		}
	}
	return lifetime.Entry{}, false
}

// busyDescendant is whether a descendant of workspace id is working or
// starting, or has no session yet.
func busyDescendant(entries []lifetime.Entry, id string) bool {
	below := descendants(entryWorkspaces(entries), id)
	for i := range entries {
		if !below[entries[i].Workspace.ID] {
			continue
		}
		s := entries[i].Session
		if s == nil || s.State == lifetime.StateWorking || s.State == lifetime.StateStarting {
			return true
		}
	}
	return false
}

// waitDone is whether the wait can return: every child is finished, or with
// --any one is.
func waitDone(states []childState, opts *machineWaitOptions) bool {
	if opts.any {
		return slices.ContainsFunc(states, func(s childState) bool { return s.finished })
	}
	return !slices.ContainsFunc(states, func(s childState) bool { return !s.finished })
}

// printWait prints each target's item, and its error. A child not finished
// at the timeout is wait_timeout, except with --any when another finished.
func (a *App) printWait(targets []waitTarget, states []childState, opts *machineWaitOptions) error {
	anyFinished := slices.ContainsFunc(states, func(s childState) bool { return s.finished })
	var items itemResults
	results := make([]machineWaitItemJSON, 0, len(targets))
	for i := range targets {
		id := targets[i].id
		s := &states[i]
		item := machineWaitItemJSON{
			ID:       id,
			Session:  optional(s.session),
			Role:     optional(s.record.Role()),
			Status:   s.status,
			Finished: s.finished,
			Outcome:  waitOutcome(s),
			Result:   resultInfo(&s.record),
		}
		item.Error = items.add(id, waitItemError(id, s, opts, anyFinished))
		results = append(results, item)
	}
	return a.finishItems("children", results, &items)
}

// waitOutcome is pending until a child finishes, missing when it finished
// with no result, and else its result's outcome.
func waitOutcome(s *childState) string {
	switch {
	case !s.finished:
		return "pending"
	case s.record.Result == nil:
		return "missing"
	}
	outcome, _ := s.record.Outcome()
	return string(outcome)
}

// waitItemError is the error of one child in ws machine wait, or nil.
func waitItemError(id string, s *childState, opts *machineWaitOptions, anyFinished bool) error {
	if s.finished {
		return childFinishedError(id, &s.record)
	}
	if opts.any && anyFinished {
		return nil
	}
	return newError(codeWaitTimeout, fmt.Errorf("%s is still %s after %s; run ws machine wait again to keep waiting", id, s.status, opts.timeout))
}

// childFinishedError is child_failed when a finished child's verdict failed
// or it recorded none, and nil otherwise.
func childFinishedError(id string, rec *machine.Record) error {
	outcome, ok := rec.Outcome()
	switch {
	case !ok:
		return newError(codeChildFailed, fmt.Errorf("%s finished without recording a verdict", id))
	case outcome == machine.OutcomeFail:
		return newError(codeChildFailed, fmt.Errorf("%s finished with verdict %s", id, rec.Result.Verdict))
	}
	return nil
}
