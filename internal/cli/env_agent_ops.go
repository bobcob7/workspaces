package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/repo"
)

// agentOperations runs the controller's operations on this machine, each
// through a fresh App running the same command a person would (D1).
type agentOperations struct {
	app *App // the agent's App: paths, config, environ, version
	// newApp builds the App one CLI operation runs in: cli.New with stdin
	// empty and the given streams, environment set to name.
	newApp    func(stdout, stderr io.Writer) *App
	name      string                 // the enrolled environment's name
	locksMu   sync.Mutex             // guards sendLocks
	sendLocks map[string]*sync.Mutex // the send lock of each Claude Code session id
}

// newAgentOperations returns the operations of the agent a, enrolled as name.
func newAgentOperations(a *App, name string) *agentOperations {
	o := &agentOperations{app: a, name: name}
	o.newApp = func(stdout, stderr io.Writer) *App {
		n := New(a.version, strings.NewReader(""), stdout, stderr, a.configs, a.environ)
		n.environment = name
		return n
	}
	return o
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
		return o.runCLI(ctx, createArgv(root, a))
	case *envproto.ListArgs:
		return o.runCLI(ctx, append(cliPrefix(root), "list", "--all"))
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

// runCLI runs argv in a new App and returns its stdout's one JSON object.
// A non-zero exit is commandFailure's error.
func (o *agentOperations) runCLI(ctx context.Context, argv []string) (json.RawMessage, error) {
	var stdout, stderr bytes.Buffer
	if code := o.newApp(&stdout, &stderr).Run(ctx, argv); code != exitOK {
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
