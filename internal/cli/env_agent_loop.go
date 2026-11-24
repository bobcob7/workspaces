package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// defaultLoopHistory is the history limit used when the controller sends none.
const defaultLoopHistory = 20

// runLoop runs a loop.* operation with ws loop's own command. Every result
// and every error goes through the redactor before it leaves.
func (o *agentOperations) runLoop(ctx context.Context, op string, args any) (json.RawMessage, error) {
	out, err := o.dispatchLoop(ctx, op, args)
	if err != nil {
		return nil, o.redactError(replyError(err))
	}
	return out, nil
}

// dispatchLoop checks the rules that guard the operation, then runs it.
func (o *agentOperations) dispatchLoop(ctx context.Context, op string, args any) (json.RawMessage, error) {
	if err := o.guardLoop(op, args); err != nil {
		return nil, err
	}
	switch a := args.(type) {
	case *envproto.LoopListArgs:
		return o.loopList(ctx, a.Offset)
	case *envproto.LoopSpec:
		return o.loopWrite(ctx, op, a)
	case *envproto.LoopRunArgs:
		return o.runLoopCLI(ctx, loopRunArgv(o.app.paths.Root, a))
	case *envproto.LoopHistoryArgs:
		return o.runLoopCLI(ctx, loopHistoryArgv(o.app.paths.Root, a))
	case *envproto.LoopHistoryRunArgs:
		return o.loopHistoryRun(ctx, a)
	case *envproto.LoopMemoryKey:
		return o.runLoopCLI(ctx, loopMemoryKeyArgv(o.app.paths.Root, op, a))
	case *envproto.LoopMemorySetArgs:
		return o.loopMemorySet(ctx, a)
	case *envproto.LoopName:
		return o.loopNamed(ctx, op, a)
	}
	return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown operation " + op}
}

// guardLoop refuses what envproto.DecodeArgs already refuses, a second time:
// a destructive operation without confirm, and a value that holds the
// redaction marker. Then it refuses a script when this machine forbids them.
func (o *agentOperations) guardLoop(op string, args any) error {
	if envproto.NeedsConfirm(op, args) && !loopConfirmed(args) {
		return needsConfirm(op)
	}
	if (op == envproto.OpLoopAdd || op == envproto.OpLoopEdit || op == envproto.OpLoopMemorySet) && envproto.HasRedacted(args) {
		return &envproto.ReplyError{Code: string(codeUsage), Message: "a value contains " + envproto.RedactedMarker + "; retype it or leave it unchanged"}
	}
	spec, ok := args.(*envproto.LoopSpec)
	if !ok || spec.Script == nil || *spec.Script == "" {
		return nil
	}
	_, cfg, err := o.app.configs.Load(o.app.paths.Root)
	if err != nil {
		return &envproto.ReplyError{Code: string(codeInternal), Message: "couldn't read the configuration: " + err.Error()}
	}
	if !cfg.Environments.LoopScriptsAllowed() {
		return &envproto.ReplyError{Code: string(codeUsage), Message: "scripts from the controller are turned off on " + o.name}
	}
	return nil
}

// needsConfirm is the usage refusal of an operation sent without confirm.
func needsConfirm(op string) error {
	return &envproto.ReplyError{Code: string(codeUsage), Message: op + " needs confirm", Details: map[string]any{"needs": "confirm"}}
}

// loopConfirmed is the confirm field of args.
func loopConfirmed(args any) bool {
	switch a := args.(type) {
	case *envproto.LoopName:
		return a.Confirm
	case *envproto.LoopSpec:
		return a.Confirm
	}
	return false
}

// runLoopCLI runs argv and finishes its stdout object.
func (o *agentOperations) runLoopCLI(ctx context.Context, argv []string) (json.RawMessage, error) {
	return o.runLoopCLIStdin(ctx, strings.NewReader(""), argv)
}

// runLoopCLIStdin is runLoopCLI with stdin as the command's standard input.
func (o *agentOperations) runLoopCLIStdin(ctx context.Context, stdin io.Reader, argv []string) (json.RawMessage, error) {
	out, err := o.runCLIWithStdin(ctx, stdin, argv)
	if err != nil {
		return nil, err
	}
	return o.finishResult(out)
}

// loopWrite runs ws loop add or edit.
func (o *agentOperations) loopWrite(ctx context.Context, op string, spec *envproto.LoopSpec) (json.RawMessage, error) {
	root := o.app.paths.Root
	if op == envproto.OpLoopEdit {
		return o.runLoopCLI(ctx, loopEditArgv(root, spec))
	}
	return o.runLoopCLI(ctx, loopAddArgv(root, spec))
}

// loopNamed runs the operations whose argument is a loop name.
func (o *agentOperations) loopNamed(ctx context.Context, op string, a *envproto.LoopName) (json.RawMessage, error) {
	if op == envproto.OpLoopShow {
		return o.loopShow(ctx, a.Name)
	}
	var verb []string
	switch op {
	case envproto.OpLoopPause:
		verb = []string{"pause"}
	case envproto.OpLoopResume:
		verb = []string{"resume"}
	case envproto.OpLoopRm:
		verb = []string{"rm"}
	case envproto.OpLoopHistoryClear:
		verb = []string{"history", "--clear"}
	case envproto.OpLoopMemoryList:
		return o.loopMemoryList(ctx, a.Name)
	case envproto.OpLoopMemoryClear:
		verb = []string{"memory", "clear"}
	default:
		return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown operation " + op}
	}
	argv := append(append(cliPrefix(o.app.paths.Root), "loop"), verb...)
	return o.runLoopCLI(ctx, append(argv, "--", a.Name))
}

// loopAddArgv is the ws loop add command line for spec: --json --config-dir
// <root> loop add, a flag for each field that is given, "--", the name and
// the specs. The prompt, script and every other text is one --flag=<text>
// element, so it can't be read as a flag.
func loopAddArgv(root string, spec *envproto.LoopSpec) []string {
	argv := append(cliPrefix(root), "loop", "add")
	argv = append(argv, loopSpecFlags(spec)...)
	if spec.Paused != nil && *spec.Paused {
		argv = append(argv, "--paused")
	}
	if spec.Script != nil && *spec.Script != "" {
		argv = append(argv, "--script="+*spec.Script)
	}
	if spec.Vars != nil {
		argv = append(argv, varFlags(*spec.Vars)...)
	}
	argv = append(argv, "--", spec.Name)
	if spec.Specs != nil {
		argv = append(argv, *spec.Specs...)
	}
	return argv
}

// loopEditArgv is the ws loop edit command line for spec. An empty specs
// list is --no-specs, an empty script --no-script and empty vars --no-vars.
func loopEditArgv(root string, spec *envproto.LoopSpec) []string {
	argv := append(cliPrefix(root), "loop", "edit")
	argv = append(argv, loopSpecFlags(spec)...)
	if spec.Script != nil {
		if *spec.Script == "" {
			argv = append(argv, "--no-script")
		} else {
			argv = append(argv, "--script="+*spec.Script)
		}
	}
	if spec.Vars != nil {
		if len(*spec.Vars) == 0 {
			argv = append(argv, "--no-vars")
		} else {
			argv = append(argv, varFlags(*spec.Vars)...)
		}
	}
	if spec.Specs != nil && len(*spec.Specs) == 0 {
		argv = append(argv, "--no-specs")
	}
	argv = append(argv, "--", spec.Name)
	if spec.Specs != nil {
		argv = append(argv, *spec.Specs...)
	}
	return argv
}

// loopSpecFlags are the flags add and edit share, in a fixed order.
func loopSpecFlags(spec *envproto.LoopSpec) []string {
	var flags []string
	text := map[string]*string{
		"title": spec.Title, "every": spec.Every, "prompt": spec.Prompt, "runner": spec.Runner,
		"endpoint": spec.Endpoint, "model": spec.Model, "permission-mode": spec.PermissionMode,
		"script-timeout": spec.ScriptTimeout,
	}
	for _, name := range slices.Sorted(maps.Keys(text)) {
		if text[name] != nil {
			flags = append(flags, "--"+name+"="+*text[name])
		}
	}
	if spec.Keep != nil {
		flags = append(flags, "--keep="+strconv.Itoa(*spec.Keep))
	}
	if spec.Hidden != nil {
		flags = append(flags, "--hidden="+strconv.FormatBool(*spec.Hidden))
	}
	return flags
}

// varFlags is one --var=KEY=VALUE per variable, by key.
func varFlags(vars map[string]string) []string {
	var flags []string
	for _, key := range slices.Sorted(maps.Keys(vars)) {
		flags = append(flags, "--var="+key+"="+vars[key])
	}
	return flags
}

// loopRunArgv is the ws loop run command line.
func loopRunArgv(root string, a *envproto.LoopRunArgs) []string {
	argv := append(cliPrefix(root), "loop", "run")
	if a.Force {
		argv = append(argv, "--force")
	}
	return append(argv, "--", a.Name)
}

// loopHistoryArgv is the ws loop history command line for a list of runs.
func loopHistoryArgv(root string, a *envproto.LoopHistoryArgs) []string {
	limit := a.Limit
	if limit == 0 {
		limit = defaultLoopHistory
	}
	return append(cliPrefix(root), "loop", "history", "--limit="+strconv.Itoa(limit), "--", a.Name)
}

// loopMemoryKeyArgv is the ws loop memory get or rm command line.
func loopMemoryKeyArgv(root, op string, a *envproto.LoopMemoryKey) []string {
	verb := "get"
	var flags []string
	if op == envproto.OpLoopMemoryRm {
		verb = "rm"
		if a.ExpectRev != nil {
			flags = append(flags, "--expect-rev="+strconv.Itoa(*a.ExpectRev))
		}
	}
	argv := append(cliPrefix(root), "loop", "memory", verb)
	return append(append(argv, flags...), "--", a.Name, a.Key)
}

// loopMemorySetArgv is the ws loop memory set command line. The value is not
// on it: --file=- reads it from stdin.
func loopMemorySetArgv(root string, a *envproto.LoopMemorySetArgs) []string {
	argv := append(cliPrefix(root), "loop", "memory", "set", "--file=-")
	if a.ExpectRev != nil {
		argv = append(argv, "--expect-rev="+strconv.Itoa(*a.ExpectRev))
	}
	return append(argv, "--", a.Name, a.Key)
}

// loopMemorySet stores the value, which reaches ws on stdin.
func (o *agentOperations) loopMemorySet(ctx context.Context, a *envproto.LoopMemorySetArgs) (json.RawMessage, error) {
	return o.runLoopCLIStdin(ctx, strings.NewReader(a.Value), loopMemorySetArgv(o.app.paths.Root, a))
}

// redactedFields is the stdout object of argv with every string redacted,
// and the number of replacements.
func (o *agentOperations) redactedFields(ctx context.Context, argv []string) (map[string]json.RawMessage, int, error) {
	out, err := o.runCLI(ctx, argv)
	if err != nil {
		return nil, 0, err
	}
	clean, n, err := o.redactorOrNew().JSON(out)
	if err != nil {
		return nil, 0, &envproto.ReplyError{Code: string(codeInternal), Message: "the loop command printed invalid JSON"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(clean, &fields); err != nil {
		return nil, 0, &envproto.ReplyError{Code: string(codeInternal), Message: "the loop command printed an unexpected object"}
	}
	return fields, n, nil
}

// loopList is one page of ws loop list, redacted before it is measured.
func (o *agentOperations) loopList(ctx context.Context, offset int) (json.RawMessage, error) {
	out, err := o.runCLI(ctx, append(cliPrefix(o.app.paths.Root), "loop", "list"))
	if err != nil {
		return nil, err
	}
	clean, n, err := o.redactorOrNew().JSON(out)
	if err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "loop list printed invalid JSON"}
	}
	page, err := loopPage(clean, offset)
	if err != nil {
		return nil, err
	}
	return o.finishResultCounting(page, n)
}

// loopPage cuts ws loop list --json to the loops from offset within
// envproto.MaxPageBytes, adding offset, next_offset and total. A loop over
// the budget alone is replaced by its SnapshotLoop fields plus "truncated":
// true. The list must already be redacted.
func loopPage(list json.RawMessage, offset int) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(list, &fields); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "loop list printed an unexpected object"}
	}
	var loops []json.RawMessage
	if raw := fields["loops"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &loops); err != nil {
			return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "loop list's loops is not a list"}
		}
	}
	fields["offset"] = mustMarshal(offset)
	fields["total"] = mustMarshal(len(loops))
	fields["next_offset"] = mustMarshal(len(loops))
	fields["loops"] = json.RawMessage("[]")
	budget := envproto.MaxPageBytes - len(mustMarshal(fields)) - resultSlack/4
	page := []json.RawMessage{}
	used := 0
	next := offset
	for ; next < len(loops); next++ {
		item := loops[next]
		if len(item)+1 > budget {
			item = loopSnapshotOf(item)
		}
		if used+len(item)+1 > budget && len(page) > 0 {
			break
		}
		page = append(page, item)
		used += len(item) + 1
	}
	fields["loops"] = mustMarshal(page)
	if next >= len(loops) {
		fields["next_offset"] = json.RawMessage("null")
	} else {
		fields["next_offset"] = mustMarshal(next)
	}
	return mustMarshal(fields), nil
}

// loopSnapshotOf is a loop of ws loop list as its SnapshotLoop fields plus
// "truncated": true: no prompt, script or vars.
func loopSnapshotOf(item json.RawMessage) json.RawMessage {
	var l struct {
		Name      string          `json:"name"`
		Title     string          `json:"title"`
		Every     string          `json:"every"`
		Paused    bool            `json:"paused"`
		Hidden    bool            `json:"hidden"`
		Runner    string          `json:"runner"`
		NextRunAt *time.Time      `json:"next_run_at"`
		LastRun   json.RawMessage `json:"last_run"`
		LastGate  json.RawMessage `json:"last_gate"`
	}
	if err := json.Unmarshal(item, &l); err != nil {
		return mustMarshal(map[string]any{"truncated": true})
	}
	small := envproto.SnapshotLoop{
		Name: l.Name, Title: l.Title, Every: l.Every, Paused: l.Paused, Hidden: l.Hidden, Runner: l.Runner,
		NextRunAt: l.NextRunAt, LastRun: nullIfEmpty(l.LastRun), LastGate: nullIfEmpty(l.LastGate),
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(mustMarshal(small), &fields); err != nil {
		return mustMarshal(map[string]any{"truncated": true})
	}
	fields["truncated"] = json.RawMessage("true")
	return mustMarshal(fields)
}

// nullIfEmpty is raw, or JSON null when raw is empty.
func nullIfEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// loopShow is one loop of ws loop list, in full.
func (o *agentOperations) loopShow(ctx context.Context, name string) (json.RawMessage, error) {
	fields, n, err := o.redactedFields(ctx, append(cliPrefix(o.app.paths.Root), "loop", "list"))
	if err != nil {
		return nil, err
	}
	var loops []json.RawMessage
	if err := json.Unmarshal(fields["loops"], &loops); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "loop list printed unexpected loops"}
	}
	for _, raw := range loops {
		var named struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &named) != nil || named.Name != name {
			continue
		}
		warnings := nullIfEmpty(fields["warnings"])
		return o.finishResultCounting(mustMarshal(map[string]json.RawMessage{"loop": raw, "warnings": warnings}), n)
	}
	return nil, &envproto.ReplyError{Code: string(codeLoopNotFound), Message: "no loop " + name}
}

// loopHistoryRun is one run of ws loop history, its report cut to
// envproto.MaxReportText.
func (o *agentOperations) loopHistoryRun(ctx context.Context, a *envproto.LoopHistoryRunArgs) (json.RawMessage, error) {
	argv := append(cliPrefix(o.app.paths.Root), "loop", "history", "--run="+strconv.Itoa(a.Seq), "--", a.Name)
	fields, n, err := o.redactedFields(ctx, argv)
	if err != nil {
		return nil, err
	}
	if cutField(fields, "report_text", envproto.MaxReportText) {
		fields["report_truncated"] = json.RawMessage("true")
	}
	return o.finishResultCounting(mustMarshal(fields), n)
}

// loopMemoryList is ws loop memory list with each value cut to
// envproto.MaxMemoryPreview, and "value_truncated": true on a cut entry.
func (o *agentOperations) loopMemoryList(ctx context.Context, name string) (json.RawMessage, error) {
	fields, n, err := o.redactedFields(ctx, append(cliPrefix(o.app.paths.Root), "loop", "memory", "list", "--", name))
	if err != nil {
		return nil, err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(fields["entries"], &entries); err != nil {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "loop memory list printed unexpected entries"}
	}
	for _, entry := range entries {
		if cutField(entry, "value", envproto.MaxMemoryPreview) {
			entry["value_truncated"] = json.RawMessage("true")
		}
	}
	fields["entries"] = mustMarshal(entries)
	return o.finishResultCounting(mustMarshal(fields), n)
}

// cutField cuts the string fields[key] to limit bytes and reports whether it did.
func cutField(fields map[string]json.RawMessage, key string, limit int) bool {
	raw, ok := fields[key]
	if !ok {
		return false
	}
	cut := cutString(raw, limit)
	fields[key] = cut
	return !bytes.Equal(raw, cut)
}
