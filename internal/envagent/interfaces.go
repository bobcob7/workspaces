package envagent

import (
	"context"
	"encoding/json"

	"github.com/bobcob7/workspaces/internal/envproto"
)

//go:generate go tool moq -out moq_test.go . mqttClient snapshotSource claudeProbe operationRunner

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
