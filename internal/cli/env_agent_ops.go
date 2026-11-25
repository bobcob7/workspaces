package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/redact"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// agentOperations runs the controller's operations on this machine, each
// through a fresh App running the same command a person would (D1).
type agentOperations struct {
	app *App // the agent's App: paths, config, environ, version
	// newApp builds the App one CLI operation runs in: cli.New with stdin
	// empty and the given streams, environment set to name.
	newApp func(stdout, stderr io.Writer) *App
	// newAppWithStdin is newApp with the given stdin, for a value that must
	// never be read as a flag (loop.memory_set). Nil means newApp.
	newAppWithStdin func(stdin io.Reader, stdout, stderr io.Writer) *App
	name            string                 // the enrolled environment's name
	self            envscope.Ref           // this agent's environment, for the scope files it writes
	locksMu         sync.Mutex             // guards sendLocks
	sendLocks       map[string]*sync.Mutex // the send lock of each Claude Code session id
	redactor        *redact.Redactor       // removes secrets from review.* results
}

// newAgentOperations returns the operations of the agent a, enrolled as id.
func newAgentOperations(a *App, id *envagent.Identity) *agentOperations {
	name := id.Name
	o := &agentOperations{app: a, name: name, self: envscope.Ref{ID: id.ID, Name: name}, redactor: agentRedactor(a)}
	o.newAppWithStdin = func(stdin io.Reader, stdout, stderr io.Writer) *App {
		n := New(a.version, stdin, stdout, stderr, a.configs, a.environ)
		n.environment = name
		return n
	}
	o.newApp = func(stdout, stderr io.Writer) *App {
		return o.newAppWithStdin(strings.NewReader(""), stdout, stderr)
	}
	return o
}

// agentRedactor is the redactor of a's secrets.env values and environment. A
// secrets.env that can't be read is logged and the environment alone is used.
func agentRedactor(a *App) *redact.Redactor {
	log := slog.New(slog.NewJSONHandler(a.stderr, nil))
	secrets, warnings, err := config.ReadSecrets(a.paths.Root)
	if err != nil {
		log.Warn("couldn't read secrets.env; redacting the environment only", "error", err)
	}
	for _, w := range warnings {
		log.Warn("secrets.env", "warning", w)
	}
	return redact.New(redact.SecretValues(secrets, a.environ)...)
}

// Run implements envagent's operationRunner: agent.* drives the workspace's
// Claude Code session, and the workspace operations run ws's own commands.
func (o *agentOperations) Run(ctx context.Context, op string, args any) (json.RawMessage, error) {
	if err := checkKind(args); err != nil {
		return nil, err
	}
	if strings.HasPrefix(op, "agent.") {
		return o.runAgent(ctx, op, args)
	}
	if strings.HasPrefix(op, "review.") {
		return o.runReview(ctx, op, args)
	}
	if strings.HasPrefix(op, "loop.") {
		return o.runLoop(ctx, op, args)
	}
	return o.runWorkspace(ctx, op, args)
}

// runAgent runs an agent.* operation on the Claude Code session of its workspace.
func (o *agentOperations) runAgent(ctx context.Context, op string, args any) (json.RawMessage, error) {
	out, err := o.dispatchAgent(ctx, op, args)
	return out, replyError(err)
}

// dispatchAgent runs the agent.* operation op with its args.
func (o *agentOperations) dispatchAgent(ctx context.Context, op string, args any) (json.RawMessage, error) {
	switch a := args.(type) {
	case *envproto.LogsArgs:
		return o.agentLogs(ctx, a)
	case *envproto.SendArgs:
		return o.agentSend(ctx, a)
	case *envproto.WorkspaceArgs:
		switch op {
		case envproto.OpAgentStatus:
			return o.agentStatus(ctx, a)
		case envproto.OpAgentStop:
			return o.agentStop(ctx, a)
		}
	}
	return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown operation " + op}
}

// replyError is err as an operation's reply error: a ReplyError as it is, and
// an error with a code given the code the CLI gives it. Any other error is
// returned unchanged, so its reply is internal.
func replyError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*envproto.ReplyError](err); ok {
		return err
	}
	ce := codedDomainError(err)
	if ce == nil {
		return err
	}
	return &envproto.ReplyError{Code: string(ce.code), Message: ce.Error(), Details: ce.details}
}

// runWorkspace runs a workspace operation with ws's own command.
func (o *agentOperations) runWorkspace(ctx context.Context, op string, args any) (json.RawMessage, error) {
	root := o.app.paths.Root
	switch a := args.(type) {
	case *envproto.CreateArgs:
		for _, spec := range a.Specs {
			if _, err := repo.ParseSpec(spec); err != nil {
				return nil, &envproto.ReplyError{Code: string(codeSpecInvalid), Message: err.Error()}
			}
		}
		out, err := o.runCLI(ctx, createArgv(root, a))
		if err != nil || (a.Scope == nil && a.Spawn == nil) {
			return out, err
		}
		return o.grantCreated(ctx, root, a, out)
	case *envproto.ListArgs:
		return o.runCLI(ctx, append(cliPrefix(root), "list", "--all"))
	case *envproto.WorkspaceArgs:
		if op == envproto.OpMachineRecord {
			return o.machineRecord(a.Workspace)
		}
	case *envproto.RmArgs:
		argv := append(cliPrefix(root), "rm")
		if a.KeepChildren {
			argv = append(argv, "--keep-children")
		}
		return o.runCLI(ctx, append(argv, "--", a.Workspace))
	case *envproto.RenameArgs:
		return o.runCLI(ctx, append(cliPrefix(root), "rename", "--", a.Workspace, a.Name))
	case *envproto.HideArgs:
		verb := "unhide"
		if a.Hidden {
			verb = "hide"
		}
		return o.runCLI(ctx, append(cliPrefix(root), verb, "--", a.Workspace))
	}
	return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown operation " + op}
}

// grantCreated writes the grant of the workspace that a create with a scope or
// spawn made. When the grant can't be written, the workspace is removed, so a
// child never runs without the scope its parent was given.
func (o *agentOperations) grantCreated(ctx context.Context, root string, args *envproto.CreateArgs, out json.RawMessage) (json.RawMessage, error) {
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil || created.ID == "" {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "the created workspace has no id"}
	}
	if err := o.grantWorkspace(created.ID, args); err != nil {
		_, _ = o.runCLI(ctx, append(cliPrefix(root), "rm", "--", created.ID))
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "the grant couldn't be written: " + err.Error()}
	}
	return out, nil
}

// grantWorkspace writes the scope file of workspace id when args has a scope,
// and its spawn.toml, as its own tree's root, when args has a spawn. The
// files follow the create, so the session may start before them: until the
// scope file exists the workspace is scoped to itself, which only narrows (ADR
// 0082).
func (o *agentOperations) grantWorkspace(id string, args *envproto.CreateArgs) error {
	state := o.app.paths.State
	if args.Scope != nil {
		file, err := o.scopeFile(args.Scope, time.Now())
		if err != nil {
			return err
		}
		if err := envscope.Write(state, id, file); err != nil {
			return fmt.Errorf("writing the scope: %w", err)
		}
	}
	if args.Spawn != nil {
		sp := machine.Spawn{
			Role:      args.Spawn.Role,
			Model:     args.Spawn.Model,
			Pass:      args.Spawn.Pass,
			Fail:      args.Spawn.Fail,
			Vars:      args.Spawn.Vars,
			SpawnedAt: time.Now().UTC(),
		}
		if err := machine.NewStore(state).WriteSpawn(id, id, &sp); err != nil {
			return fmt.Errorf("writing the spawn: %w", err)
		}
	}
	return nil
}

// scopeFile is the scope file of a workspace this agent created for grant:
// this agent as self, source granted, and the grant's parent.
func (o *agentOperations) scopeFile(grant *envproto.ScopeGrant, now time.Time) (*envscope.File, error) {
	refs := make([]envscope.Ref, 0, len(grant.Environments))
	for _, e := range grant.Environments {
		refs = append(refs, envscope.Ref{ID: e.ID, Name: e.Name})
	}
	scope, err := envscope.New(o.self, refs)
	if err != nil {
		return nil, fmt.Errorf("the scope grant: %w", err)
	}
	var parent *envscope.Parent
	if grant.Parent != nil {
		parent = &envscope.Parent{Environment: grant.Parent.Environment, Workspace: grant.Parent.Workspace}
	}
	return envscope.FileFor(scope, "granted", parent, now), nil
}

// machineRecord is what machine.record reads of workspace id on this
// environment: its result and role, its report cut to MaxRecordReportBytes, and
// its progress and rollup as ws machine progress shows them (ADR 0082).
func (o *agentOperations) machineRecord(id string) (json.RawMessage, error) {
	list, err := o.app.workspaces.List()
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	if !slices.ContainsFunc(list, func(w workspace.Workspace) bool { return w.ID == id }) {
		return nil, &envproto.ReplyError{Code: string(codeWorkspaceNotFound), Message: fmt.Sprintf("no workspace %q on this environment", id)}
	}
	root := machine.Root(list, id)
	store := machine.NewStore(o.app.paths.State)
	rec, err := store.Read(root, id)
	if err != nil {
		return nil, fmt.Errorf("reading the record: %w", err)
	}
	tree, err := progressTreeOf(store, root, list, id)
	if err != nil {
		return nil, fmt.Errorf("reading the progress: %w", err)
	}
	node := progressNode(tree)
	out := envproto.MachineRecordResult{Workspace: id, Progress: jsonOrNull(node.Progress), Rollup: jsonOrNull(node.Rollup)}
	if rec.Spawn != nil {
		role := rec.Spawn.Role
		out.Role = &role
	}
	if rec.Result != nil {
		outcome := machine.OutcomeOf(rec.Spawn, rec.Result.Verdict)
		out.Result = &envproto.RecordResult{Verdict: rec.Result.Verdict, Outcome: string(outcome), RecordedAt: rec.Result.RecordedAt}
	}
	if rec.ReportPath != "" {
		report, truncated, readErr := readCappedReport(rec.ReportPath)
		if readErr != nil {
			return nil, fmt.Errorf("reading the report: %w", readErr)
		}
		out.Report, out.ReportTruncated = &report, truncated
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding the record: %w", err)
	}
	return raw, nil
}

// readCappedReport reads the report at path, cut to MaxRecordReportBytes on a
// UTF-8 boundary, and reports whether it was cut.
func readCappedReport(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, envproto.MaxRecordReportBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(data) <= envproto.MaxRecordReportBytes {
		return string(data), false, nil
	}
	cut := envproto.MaxRecordReportBytes
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return string(data[:cut]), true, nil
}

// jsonOrNull is v as JSON, or null when it doesn't encode.
func jsonOrNull(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

// runCLI runs argv in a new App and returns its stdout's one JSON object.
// A non-zero exit is commandFailure's error.
func (o *agentOperations) runCLI(ctx context.Context, argv []string) (json.RawMessage, error) {
	return o.runCLIWithStdin(ctx, nil, argv)
}

// runCLIWithStdin is runCLI with stdin as the command's standard input. A nil
// stdin is the empty input runCLI always gave.
func (o *agentOperations) runCLIWithStdin(ctx context.Context, stdin io.Reader, argv []string) (json.RawMessage, error) {
	var stdout, stderr bytes.Buffer
	app := o.newApp(&stdout, &stderr)
	if stdin != nil && o.newAppWithStdin != nil {
		app = o.newAppWithStdin(stdin, &stdout, &stderr)
	}
	if code := app.Run(ctx, argv); code != exitOK {
		return nil, commandFailure(stdout.Bytes(), stderr.Bytes())
	}
	return commandObject(stdout.Bytes())
}

// commandObject is out as the one JSON object a command printed, or internal.
func commandObject(out []byte) (json.RawMessage, error) {
	out = bytes.TrimSpace(out)
	if !bytes.HasPrefix(out, []byte("{")) || !json.Valid(out) {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "the command printed no JSON object"}
	}
	return json.RawMessage(out), nil
}

// commandFailure is the error of a command that exited non-zero: its
// {"error": {...}} line on stderr, else the first failed item in its stdout
// object (ws rm reports each workspace's error there), else internal with
// stderr's first line.
func commandFailure(stdout, stderr []byte) error {
	if re := stderrError(stderr); re != nil {
		return re
	}
	if re := itemError(stdout); re != nil {
		return re
	}
	return &envproto.ReplyError{Code: string(codeInternal), Message: firstLine(stderr)}
}

// stderrError is the last {"error": {...}} line of stderr with a code, since
// warning lines can precede it, or nil.
func stderrError(stderr []byte) *envproto.ReplyError {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	for _, line := range slices.Backward(lines) {
		var body struct {
			Error *errorBody `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &body); err == nil && body.Error != nil && body.Error.Code != "" {
			return &envproto.ReplyError{Code: string(body.Error.Code), Message: body.Error.Message, Details: body.Error.Details}
		}
	}
	return nil
}

// itemError is the first failed workspace's error in a command's stdout
// object, or nil.
func itemError(stdout []byte) *envproto.ReplyError {
	var body struct {
		Workspaces []struct {
			Error *itemErrorJSON `json:"error"`
		} `json:"workspaces"`
	}
	if json.Unmarshal(bytes.TrimSpace(stdout), &body) == nil {
		for _, w := range body.Workspaces {
			if w.Error != nil && w.Error.Code != "" {
				return &envproto.ReplyError{Code: string(w.Error.Code), Message: w.Error.Message}
			}
		}
	}
	return nil
}

// firstLine is the first non-empty line of out, or "the command failed".
func firstLine(out []byte) string {
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "the command failed"
}

// checkKind is usage for a create of any kind but claude and runner.
func checkKind(args any) error {
	create, ok := args.(*envproto.CreateArgs)
	if !ok || create.Kind == envproto.KindClaude || create.Kind == envproto.KindRunner {
		return nil
	}
	return &envproto.ReplyError{Code: string(codeUsage), Message: "unknown kind " + create.Kind}
}

// cliPrefix is the global flags every operation's command line starts with.
func cliPrefix(configDir string) []string {
	return []string{"--json", "--config-dir", configDir}
}

// createArgv is the ws claude command line for workspace.create. A null
// parent is --parent none, so the agent's own directory never picks one.
func createArgv(configDir string, args *envproto.CreateArgs) []string {
	argv := append(cliPrefix(configDir), "claude")
	if args.Kind == envproto.KindRunner {
		argv = append(argv, "--runner", "ws")
		if args.Endpoint != nil {
			argv = append(argv, "--endpoint", *args.Endpoint)
		}
	}
	if args.Name != nil {
		argv = append(argv, "--name", *args.Name)
	}
	if args.Hidden {
		argv = append(argv, "--hidden")
	}
	if args.Offline {
		argv = append(argv, "--offline")
	}
	parent := requesterNone
	if args.Parent != nil {
		parent = *args.Parent
	}
	argv = append(argv, "--parent", parent)
	argv = append(argv, args.Specs...)
	var after []string
	if args.Prompt != nil {
		after = append(after, *args.Prompt)
	}
	if args.Model != nil {
		after = append(after, "--model", *args.Model)
	}
	if len(after) == 0 {
		return argv
	}
	return append(append(argv, "--"), after...)
}
