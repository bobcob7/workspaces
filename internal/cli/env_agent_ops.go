package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/repo"
)

// agentOperations runs the controller's operations on this machine, each
// through a fresh App running the same command a person would (D1).
type agentOperations struct {
	app *App // the agent's App: paths, config, environ, version
	// newApp builds the App one CLI operation runs in: cli.New with stdin
	// empty and the given streams, environment set to name.
	newApp func(stdout, stderr io.Writer) *App
	name   string // the enrolled environment's name
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

// Run implements envagent's operationRunner for the workspace operations.
func (o *agentOperations) Run(ctx context.Context, op string, args any) (json.RawMessage, error) {
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
		return o.runCLI(ctx, append(argv, a.Workspace))
	case *envproto.RenameArgs:
		return o.runCLI(ctx, append(cliPrefix(root), "rename", a.Workspace, a.Name))
	case *envproto.HideArgs:
		verb := "unhide"
		if a.Hidden {
			verb = "hide"
		}
		return o.runCLI(ctx, append(cliPrefix(root), verb, a.Workspace))
	}
	return nil, &envproto.ReplyError{Code: string(codeUsage), Message: "unknown operation " + op}
}

// runCLI runs argv in a new App and returns its stdout's one JSON object.
// A non-zero exit with a {"error": {...}} line on stderr is that error as
// *envproto.ReplyError; anything else is internal with stderr's first line.
func (o *agentOperations) runCLI(ctx context.Context, argv []string) (json.RawMessage, error) {
	var stdout, stderr bytes.Buffer
	if code := o.newApp(&stdout, &stderr).Run(ctx, argv); code != exitOK {
		return nil, cliFailure(stderr.Bytes())
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(out) {
		return nil, &envproto.ReplyError{Code: string(codeInternal), Message: "the command printed no JSON object"}
	}
	return json.RawMessage(out), nil
}

// cliFailure is the error a failed command reported on stderr.
func cliFailure(stderr []byte) error {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
	var body struct {
		Error *errorBody `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &body); err == nil && body.Error != nil && body.Error.Code != "" {
		return &envproto.ReplyError{Code: string(body.Error.Code), Message: body.Error.Message, Details: body.Error.Details}
	}
	return &envproto.ReplyError{Code: string(codeInternal), Message: line}
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
