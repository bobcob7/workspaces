package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/loop"
)

// maxRemoteLoopPages is the most pages of loop.list ws reads, so a
// misbehaving agent can't keep the loop going until the timeout.
const maxRemoteLoopPages = 1000

// loopEnvOptions are the flags every remote-capable ws loop command shares.
type loopEnvOptions struct {
	env string
	yes bool
}

// addLoopEnvFlag adds --env to cmd.
func addLoopEnvFlag(cmd *cobra.Command, opts *loopEnvOptions) {
	cmd.Flags().StringVar(&opts.env, "env", "", "run it on this environment (ws env list); default: local")
}

// addLoopYesFlag adds --yes to cmd, for the commands that need confirming on
// an environment. Locally it does nothing, so scripts can pass it either way.
func addLoopYesFlag(cmd *cobra.Command, opts *loopEnvOptions) {
	cmd.Flags().BoolVar(&opts.yes, "yes", false, "confirm what an environment asks to confirm; locally it does nothing")
}

// loopEnv returns the environment --env names ("" for local). ws loop is for
// people, so unlike ws review it needs no guard against agents.
func (a *App) loopEnv(opts *loopEnvOptions) (string, error) {
	name, _, err := a.remoteTarget(opts.env)
	return name, err
}

// needsYes is the usage refusal of an operation that needs --yes on env.
func needsYes(env, what string) error {
	return usageErrorf("%s on %s; pass --yes", what, env)
}

// runRemoteLoop sends op with args to env and prints the result plus "environment".
func (a *App) runRemoteLoop(ctx context.Context, env, op string, args any) error {
	object, err := a.callLoop(ctx, env, op, args)
	if err != nil {
		return err
	}
	return a.printRemote(object, env, func() error { return a.printRemoteLoop(object, env, op) })
}

// callLoop calls op on env. An agent's refusal for a missing confirm, which
// the CLI avoids by sending confirm only with --yes, is turned into a message
// that says what to pass.
func (a *App) callLoop(ctx context.Context, env, op string, args any) (map[string]json.RawMessage, error) {
	object, err := a.callRemoteObject(ctx, env, op, args)
	ce, ok := errors.AsType[*cliError](err)
	if !ok || ce.code != codeUsage || ce.details["needs"] != "confirm" {
		return object, err
	}
	refused := newError(codeUsage, fmt.Errorf("%s needs your confirmation on %s; pass --yes", op, env))
	refused.details = ce.details
	return nil, refused
}

// printRemoteLoop prints what a remote loop operation returned, as the local
// command prints it.
func (a *App) printRemoteLoop(object map[string]json.RawMessage, env, op string) error {
	switch op {
	case envproto.OpLoopRun:
		var run loopRunResult
		if err := decodeRemote(object, env, &run); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s  %s  %s  %s\n", run.Name, run.Workspace, run.Session, run.Path)
	case envproto.OpLoopHistory:
		var history loopHistoryJSON
		if err := decodeRemote(object, env, &history); err != nil {
			return err
		}
		for i := range history.Runs {
			fmt.Fprintln(a.stdout, remoteHistoryLine(&history.Runs[i]))
		}
	case envproto.OpLoopHistoryRun:
		var detail loopHistoryDetailJSON
		if err := decodeRemote(object, env, &detail); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, remoteHistoryLine(&detail.Run))
		if detail.ReportText != nil {
			fmt.Fprintf(a.stdout, "\n%s\n", strings.TrimRight(*detail.ReportText, "\n"))
		}
	case envproto.OpLoopHistoryClear:
		return a.printRemoteCleared(object, env, "runs")
	case envproto.OpLoopMemoryClear:
		return a.printRemoteCleared(object, env, "keys")
	case envproto.OpLoopMemoryList, envproto.OpLoopMemoryGet, envproto.OpLoopMemorySet, envproto.OpLoopMemoryRm:
		return a.printRemoteMemory(object, env, op)
	default:
		var l loopResult
		if err := decodeRemote(object, env, &l); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, remoteLoopLine(&l.loopJSON, false))
	}
	return nil
}

// printRemoteCleared prints how many of what a clear removed.
func (a *App) printRemoteCleared(object map[string]json.RawMessage, env, what string) error {
	var cleared loopClearedJSON
	if err := decodeRemote(object, env, &cleared); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%d %s removed\n", cleared.Removed, what)
	return nil
}

// printRemoteMemory prints the result of a memory operation as the local
// command does.
func (a *App) printRemoteMemory(object map[string]json.RawMessage, env, op string) error {
	switch op {
	case envproto.OpLoopMemoryList:
		var list memoryListJSON
		if err := decodeRemote(object, env, &list); err != nil {
			return err
		}
		for _, e := range list.Entries {
			fmt.Fprintf(a.stdout, "%s  rev %d  %s  %s\n", e.Key, e.Rev, e.UpdatedAt, memoryPreview(e.Value))
		}
	case envproto.OpLoopMemoryGet:
		var got memoryEntryResultJSON
		if err := decodeRemote(object, env, &got); err != nil {
			return err
		}
		fmt.Fprint(a.stdout, got.Entry.Value)
	case envproto.OpLoopMemorySet:
		var set memoryEntryResultJSON
		if err := decodeRemote(object, env, &set); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s  rev %d\n", set.Entry.Key, set.Entry.Rev)
	default:
		var removed memoryRemovedJSON
		if err := decodeRemote(object, env, &removed); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s removed\n", removed.Key)
	}
	return nil
}

// remoteLoopLine is a loop as loopLine prints it; stale adds a note.
func remoteLoopLine(l *loopJSON, stale bool) string {
	state, next := "active", "-"
	if l.Paused {
		state = "paused"
	} else if l.NextRunAt != nil {
		next = *l.NextRunAt
	}
	last := "-"
	if l.LastRun != nil && deref(l.LastRun.Workspace) != "" {
		last = deref(l.LastRun.Workspace)
	}
	name := l.Name
	if l.Title != "" {
		name = fmt.Sprintf("%s (%s)", l.Title, l.Name)
	}
	line := fmt.Sprintf("%-9s  every %-4s  %-6s  next %-20s  last %s", name, l.Every, state, next, last)
	if stale {
		line += "  (stale)"
	}
	return line
}

// remoteHistoryLine is a run as historyLine prints it.
func remoteHistoryLine(r *loopHistoryRunJSON) string {
	status := r.Status
	if r.Repeats > 1 {
		status += fmt.Sprintf(" ×%d", r.Repeats)
	}
	duration := "-"
	if r.DurationSeconds != nil {
		duration = fmt.Sprintf("%ds", *r.DurationSeconds)
	}
	result := "-"
	switch {
	case r.Verdict != nil:
		result = r.Verdict.Verdict
	case r.Error != nil:
		result = withMessage(string(r.Error.Code), r.Error.Message)
	}
	return fmt.Sprintf("%-4d  %-12s  %-20s  %-7s  %-24s  %s", r.Seq, status, r.StartedAt, duration, orDash(r.Workspace), result)
}

// loopListEnvJSON is ws loop list --json --env's output (R4).
type loopListEnvJSON struct {
	Loops             []json.RawMessage `json:"loops"`
	Environment       string            `json:"environment"`
	EnvironmentStatus string            `json:"environment_status"`
	LoopsUnknown      bool              `json:"loops_unknown"`
	Warnings          []string          `json:"warnings"`
}

// cachedLoops is the snapshot's loops of env as ws loop list --json prints
// them (R4). presence is the daemon's status of each environment name.
func (a *App) cachedLoops(env string, presence map[string]string) (loopListEnvJSON, error) {
	status := envOffline
	if presence[env] == envOnline {
		status = envOnline
	}
	out := loopListEnvJSON{Loops: []json.RawMessage{}, Environment: env, EnvironmentStatus: status, Warnings: []string{}}
	file, snap, err := a.envStore().Snapshot(env)
	if errors.Is(err, fs.ErrNotExist) {
		out.LoopsUnknown = true
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("reading the snapshot of %s: %w", env, err)
	}
	if snap.Loops == nil {
		out.LoopsUnknown = true
		return out, nil
	}
	stale := status != envOnline || a.snapshotOld(file.ReceivedAt)
	for i := range snap.Loops {
		item, err := summaryLoop(&snap.Loops[i], env, stale)
		if err != nil {
			return out, err
		}
		out.Loops = append(out.Loops, item)
	}
	return out, nil
}

// summaryLoop is a snapshot loop as a loop of ws loop list: the fields the
// snapshot lacks are null, and "summary" says so.
func summaryLoop(l *envproto.SnapshotLoop, env string, stale bool) (json.RawMessage, error) {
	body, err := json.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("encoding loop %s: %w", l.Name, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("encoding loop %s: %w", l.Name, err)
	}
	for _, key := range []string{"prompt", "script", "script_timeout", "vars", "specs", "model", "permission_mode", "endpoint"} {
		fields[key] = json.RawMessage("null")
	}
	fields["summary"] = json.RawMessage("true")
	return withEnvironment(fields, env, stale)
}

// withEnvironment adds "environment" and "stale" to a loop's fields.
func withEnvironment(fields map[string]json.RawMessage, env string, stale bool) (json.RawMessage, error) {
	name, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("encoding the environment name: %w", err)
	}
	fields["environment"] = name
	fields["stale"] = json.RawMessage(strconv.FormatBool(stale))
	item, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding a loop: %w", err)
	}
	return item, nil
}

// runRemoteLoopList lists the loops of env: every page of loop.list when it
// is online, else (or with cached) the snapshot's loops.
func (a *App) runRemoteLoopList(ctx context.Context, env string, cached bool) error {
	presence := a.presence(ctx)
	if cached || presence[env] != envOnline {
		listed, err := a.cachedLoops(env, presence)
		if err != nil {
			return err
		}
		return a.printCachedLoops(&listed)
	}
	object, err := a.remoteLoopPages(ctx, env)
	if err != nil {
		return err
	}
	return a.printRemote(object, env, func() error { return a.printLoopLines(object, env) })
}

// remoteLoopPages reads loop.list from offset 0 until next_offset is null and
// returns one object whose loops are all of them, each with "environment"
// and "stale": false. An offset that doesn't advance is an error.
func (a *App) remoteLoopPages(ctx context.Context, env string) (map[string]json.RawMessage, error) {
	var loops []json.RawMessage
	var last map[string]json.RawMessage
	redacted, offset := 0, 0
	for range maxRemoteLoopPages {
		object, err := a.callRemoteObject(ctx, env, envproto.OpLoopList, envproto.LoopListArgs{Offset: offset})
		if err != nil {
			return nil, err
		}
		var got struct {
			Loops      []map[string]json.RawMessage `json:"loops"`
			NextOffset *int                         `json:"next_offset"`
			Redacted   int                          `json:"redacted"`
		}
		if err = decodeRemote(object, env, &got); err != nil {
			return nil, err
		}
		for _, fields := range got.Loops {
			item, err := withEnvironment(fields, env, false)
			if err != nil {
				return nil, err
			}
			loops = append(loops, item)
		}
		redacted += got.Redacted
		last = object
		if got.NextOffset == nil {
			return finishLoopPages(last, loops, redacted)
		}
		if *got.NextOffset <= offset {
			return nil, newError(codeInternal, fmt.Errorf("environment %s's loop page didn't advance past offset %d", env, offset))
		}
		offset = *got.NextOffset
	}
	return nil, newError(codeInternal, fmt.Errorf("environment %s's loop list has more than %d pages", env, maxRemoteLoopPages))
}

// finishLoopPages is the last page's object with every page's loops and the
// total count of redacted values; the paging keys are dropped.
func finishLoopPages(last map[string]json.RawMessage, loops []json.RawMessage, redacted int) (map[string]json.RawMessage, error) {
	if loops == nil {
		loops = []json.RawMessage{}
	}
	all, err := json.Marshal(loops)
	if err != nil {
		return nil, fmt.Errorf("encoding the loops: %w", err)
	}
	last["loops"] = all
	last["redacted"] = json.RawMessage(strconv.Itoa(redacted))
	for _, key := range []string{"offset", "next_offset", "total"} {
		delete(last, key)
	}
	return last, nil
}

// printLoopLines prints the loops of object, one line each.
func (a *App) printLoopLines(object map[string]json.RawMessage, env string) error {
	var listed struct {
		Loops []loopJSON `json:"loops"`
	}
	if err := decodeRemote(object, env, &listed); err != nil {
		return err
	}
	for i := range listed.Loops {
		fmt.Fprintln(a.stdout, remoteLoopLine(&listed.Loops[i], false))
	}
	return nil
}

// printCachedLoops prints the snapshot's loops as JSON, or as lines with a
// note on stderr when the loops are unknown or stale.
func (a *App) printCachedLoops(listed *loopListEnvJSON) error {
	listed.Warnings = append(listed.Warnings, a.warned...)
	if a.global.json {
		return a.writeJSON(listed)
	}
	for _, raw := range listed.Loops {
		var l struct {
			loopJSON
			Stale bool `json:"stale"`
		}
		if err := json.Unmarshal(raw, &l); err != nil {
			return fmt.Errorf("reading a cached loop: %w", err)
		}
		fmt.Fprintln(a.stdout, remoteLoopLine(&l.loopJSON, l.Stale))
	}
	switch {
	case listed.LoopsUnknown:
		fmt.Fprintf(a.stderr, "ws: warning: %s hasn't reported its loops; its ws may be older than protocol 3\n", listed.Environment)
	case listed.EnvironmentStatus != envOnline:
		fmt.Fprintf(a.stderr, "ws: warning: %s is offline; these loops are from its last snapshot\n", listed.Environment)
	}
	for _, w := range listed.Warnings {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
	return nil
}

// remoteLoopAddSpec is loop.add's arguments for the flags given. Fields not
// given stay nil, so the environment's ws applies its own defaults.
func remoteLoopAddSpec(cmd *cobra.Command, args []string, opts *loopAddOptions) (*envproto.LoopSpec, error) {
	if opts.every == "" {
		return nil, usageErrorf("--every is required")
	}
	if opts.prompt == "" {
		return nil, usageErrorf("--prompt is required")
	}
	set := cmd.Flags().Changed
	hidden, hiddenGiven, err := hiddenFlag(set, opts.hidden, opts.show)
	if err != nil {
		return nil, err
	}
	script, scriptTimeout, vars, err := scriptOptions(opts)
	if err != nil {
		return nil, err
	}
	interval, err := loop.ParseEvery(opts.every)
	if err != nil {
		return nil, domainError(err)
	}
	if err := validSpecs(cmd, args[1:]); err != nil {
		return nil, err
	}
	every := loop.FormatEvery(interval)
	spec := &envproto.LoopSpec{Name: args[0], Every: &every, Prompt: &opts.prompt}
	spec.Title = givenText(strings.TrimSpace(opts.title))
	spec.Model = givenText(opts.model)
	spec.PermissionMode = givenText(opts.permissionMode)
	spec.Endpoint = givenText(opts.endpoint)
	spec.Script = givenText(script)
	if set("runner") {
		spec.Runner = &opts.runner
	}
	if set("keep") {
		spec.Keep = &opts.keep
	}
	if opts.paused {
		spec.Paused = &opts.paused
	}
	if hiddenGiven {
		spec.Hidden = &hidden
	}
	if opts.scriptTimeout != "" {
		spec.ScriptTimeout = &scriptTimeout
	}
	if len(vars) > 0 {
		spec.Vars = &vars
	}
	if len(args) > 1 {
		specs := args[1:]
		spec.Specs = &specs
	}
	return spec, nil
}

// givenText is &s, or nil for an empty s.
func givenText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// remoteLoopEditSpec is loop.edit's arguments: only the flags given.
func remoteLoopEditSpec(cmd *cobra.Command, args []string, opts *loopEditOptions) (*envproto.LoopSpec, error) {
	edit, err := loopEditOf(cmd, args[1:], opts)
	if err != nil {
		return nil, err
	}
	return &envproto.LoopSpec{
		Name: args[0], Title: edit.Title, Every: edit.Every, Prompt: edit.Prompt, Specs: edit.Specs, Keep: edit.Keep,
		Runner: edit.Runner, Endpoint: edit.Endpoint, Model: edit.Model, PermissionMode: edit.PermissionMode,
		Script: edit.Script, ScriptTimeout: edit.ScriptTimeout, Vars: edit.Vars, Hidden: edit.Hidden,
	}, nil
}

// runRemoteLoopSpec sends loop.add or loop.edit with spec. A script, or
// bypassPermissions, runs code on env, so it needs --yes, which sets confirm.
func (a *App) runRemoteLoopSpec(ctx context.Context, env, op string, spec *envproto.LoopSpec, yes bool) error {
	if envproto.NeedsConfirm(op, spec) {
		if !yes {
			return usageErrorf("this runs code on %s as its agent's user; pass --yes", env)
		}
		spec.Confirm = true
	}
	return a.runRemoteLoop(ctx, env, op, spec)
}

// runRemoteLoopAdd sends loop.add for ws loop add's flags.
func (a *App) runRemoteLoopAdd(cmd *cobra.Command, env string, args []string, opts *loopAddOptions, yes bool) error {
	spec, err := remoteLoopAddSpec(cmd, args, opts)
	if err != nil {
		return err
	}
	return a.runRemoteLoopSpec(cmd.Context(), env, envproto.OpLoopAdd, spec, yes)
}

// runRemoteLoopEdit sends loop.edit for ws loop edit's flags.
func (a *App) runRemoteLoopEdit(cmd *cobra.Command, env string, args []string, opts *loopEditOptions, yes bool) error {
	spec, err := remoteLoopEditSpec(cmd, args, opts)
	if err != nil {
		return err
	}
	return a.runRemoteLoopSpec(cmd.Context(), env, envproto.OpLoopEdit, spec, yes)
}

// runRemoteLoopRm sends loop.rm, which deletes the loop's record, history and
// memory, so it needs --yes.
func (a *App) runRemoteLoopRm(ctx context.Context, env, name string, yes bool) error {
	if !yes {
		return needsYes(env, "deleting a loop deletes its history and memory")
	}
	return a.runRemoteLoop(ctx, env, envproto.OpLoopRm, envproto.LoopName{Name: name, Confirm: true})
}

// runRemoteLoopRun sends loop.run.
func (a *App) runRemoteLoopRun(ctx context.Context, env, name string, force bool) error {
	return a.runRemoteLoop(ctx, env, envproto.OpLoopRun, envproto.LoopRunArgs{Name: name, Force: force})
}

// runRemoteLoopHistory sends loop.history, loop.history_run or
// loop.history_clear for ws loop history's flags.
func (a *App) runRemoteLoopHistory(ctx context.Context, env, name string, opts loopHistoryOptions, yes bool) error {
	if err := opts.check(); err != nil {
		return err
	}
	switch {
	case opts.clear:
		if !yes {
			return needsYes(env, "clearing the history deletes its runs and saved reports")
		}
		return a.runRemoteLoop(ctx, env, envproto.OpLoopHistoryClear, envproto.LoopName{Name: name, Confirm: true})
	case opts.run != 0:
		return a.runRemoteLoop(ctx, env, envproto.OpLoopHistoryRun, envproto.LoopHistoryRunArgs{Name: name, Seq: opts.run})
	case opts.limitSet && opts.limit > envproto.MaxHistoryLimit:
		return usageErrorf("--limit must be from 1 to %d on an environment", envproto.MaxHistoryLimit)
	}
	limit := 0
	if opts.limitSet {
		limit = opts.limit
	}
	return a.runRemoteLoop(ctx, env, envproto.OpLoopHistory, envproto.LoopHistoryArgs{Name: name, Limit: limit})
}

// runRemoteMemoryClear sends loop.memory_clear, which needs --yes.
func (a *App) runRemoteMemoryClear(ctx context.Context, env, name string, yes bool) error {
	if !yes {
		return needsYes(env, "clearing the memory removes every key")
	}
	return a.runRemoteLoop(ctx, env, envproto.OpLoopMemoryClear, envproto.LoopName{Name: name, Confirm: true})
}

// runRemoteMemorySet reads the value from the argument, --file or stdin on
// this machine, and sends loop.memory_set.
func (a *App) runRemoteMemorySet(cmd *cobra.Command, env string, args []string, opts *memorySetOptions) error {
	value, err := a.memoryValue(args[2:], opts.file)
	if err != nil {
		return err
	}
	call := envproto.LoopMemorySetArgs{Name: args[0], Key: args[1], Value: value, ExpectRev: expectRevOf(cmd, opts)}
	return a.runRemoteLoop(cmd.Context(), env, envproto.OpLoopMemorySet, call)
}

// expectRevOf is --expect-rev when it was given.
func expectRevOf(cmd *cobra.Command, opts *memorySetOptions) *int {
	if !cmd.Flags().Changed("expect-rev") {
		return nil
	}
	return &opts.expectRev
}
