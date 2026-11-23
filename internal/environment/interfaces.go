package environment

import (
	"context"
	"crypto/tls"

	"github.com/bobcob7/workspaces/internal/envproto"
)

//go:generate go tool moq -out moq_test.go . broker

// broker is the MQTT 5 broker the daemon embeds (ADR 0074). envtest.Broker
// and internal/mqttbroker implement it.
type broker interface {
	// Start listens on addr for MQTT over TLS with tlsConfig, checks every
	// client with rules, serves until Close, and returns the address it
	// listens on.
	Start(ctx context.Context, addr string, tlsConfig *tls.Config, rules envproto.Rules) (string, error)
	// Publish and Subscribe act as the in-process controller, unchecked.
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) (func(), error)
	// Clients lists the connected peers.
	Clients() []envproto.Peer
	// Disconnect drops every client whose certificate has serial.
	Disconnect(serial string) int
	Close() error
}
