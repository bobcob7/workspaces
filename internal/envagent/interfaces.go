package envagent

import (
	"context"
	"encoding/json"

	"github.com/bobcob7/workspaces/internal/envproto"
)

//go:generate go tool moq -out moq_test.go . mqttClient snapshotSource claudeProbe

// mqttClient is the agent's MQTT 5 connection (envtest.Client in tests,
// T10's paho adapter in ws). Connect may be called again after Done.
type mqttClient interface {
	Connect(ctx context.Context, opts envproto.ConnectOptions) error
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error
	Done() <-chan struct{}
	Close() error
}

// snapshotSource is ws list --json's "workspaces" array on this machine.
type snapshotSource interface {
	Workspaces(ctx context.Context) (json.RawMessage, error)
}

// claudeProbe reports envproto.ClaudeMissing or ClaudePresent, and the version when present.
type claudeProbe interface {
	Probe(ctx context.Context) (state string, version *string)
}
