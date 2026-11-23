package envagent

import (
	"context"
	"encoding/json"
	"io"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/ptyproc"
)

//go:generate go tool moq -out moq_test.go . mqttClient snapshotSource claudeProbe operationRunner terminalCommands ptyStarter ptyProcess loopTicker

// mqttClient is the agent's MQTT 5 connection (envtest.Client in tests,
// internal/mqttclient in ws). Connect may be called again after Done.
type mqttClient interface {
	Connect(ctx context.Context, opts envproto.ConnectOptions) error
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error
	Done() <-chan struct{}
	Close() error
}

// snapshotSource is ws list --json's "workspaces" array on this machine, and
// the Claude Code sessions on it.
type snapshotSource interface {
	Workspaces(ctx context.Context) (json.RawMessage, error)
	// Agents is this machine's Claude Code sessions, [] when there are none.
	Agents(ctx context.Context) ([]envproto.SnapshotAgent, error)
	// Loops is this machine's loops, [] when there are none.
	Loops(ctx context.Context) ([]envproto.SnapshotLoop, error)
}

// loopTicker runs one pass of the agent's loop scheduler.
type loopTicker interface {
	Tick(ctx context.Context) (changed bool, err error)
}

// claudeProbe reports envproto.ClaudeMissing or ClaudePresent, and the version when present.
type claudeProbe interface {
	Probe(ctx context.Context) (state string, version *string)
}

// operationRunner runs one allowed, validated operation with ws's own code
// (internal/cli implements it, D1). args is the pointer DecodeArgs returned.
// result is the reply's result; a *envproto.ReplyError is the failure to reply
// with (its code is a ws error code); any other error is replied as "internal".
type operationRunner interface {
	Run(ctx context.Context, op string, args any) (result json.RawMessage, err error)
}

// terminalCommands builds what a terminal runs (internal/cli implements it).
// It checks the workspace and returns the session id it attached to. The
// operationRunner passed to NewAgent must implement it for terminal.open to work.
type terminalCommands interface {
	TerminalCommand(ctx context.Context, args *envproto.TerminalOpenArgs) (TerminalCommand, error)
}

// TerminalCommand is a program to run in a pty.
type TerminalCommand struct {
	Path      string
	Args      []string
	Dir       string
	Env       []string
	Workspace *string // the attached workspace id, nil for a shell.
	Session   *string // the Claude session id, nil for a shell.
}

// ptyStarter starts a pty process (ptyproc.Start in ws; a fake in tests).
type ptyStarter interface {
	Start(spec ptyproc.Spec) (ptyProcess, error)
}

// ptyProcess is a running process on a pty.
type ptyProcess interface {
	io.ReadWriter
	Resize(cols, rows int) error
	Close() error
	Wait() *int
}
