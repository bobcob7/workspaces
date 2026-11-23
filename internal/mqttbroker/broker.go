// Package mqttbroker embeds an MQTT 5 broker, mochi-mqtt/server, in the ws
// daemon (ADR 0074). One mTLS listener serves the agents; the controller is
// the module's in-process client. The broker knows nothing about topics: it
// asks envproto.Rules.
package mqttbroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"slices"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	listenerID = "agents"
	// controllerID is the client id of the in-process controller.
	controllerID = "ws-controller"
	// maxPacketSlack is room for a topic and properties around a request body.
	maxPacketSlack = 16 << 10
	// maxClientWritesPending is how many packets the broker queues for a
	// client that doesn't read before it drops QoS 0 messages (about 4 MiB of
	// 16 KiB terminal chunks). mochi's default, 8192, is 128 MiB.
	maxClientWritesPending = 256
	// maxSessionExpiry caps the session expiry a client may ask for.
	maxSessionExpiry = 10 * 60
	// takeOverWait bounds how long a new connection waits for the old one of
	// the same environment to finish.
	takeOverWait = 2 * time.Second
)

var (
	errNotRunning       = errors.New("mqttbroker: not running")
	errAlreadyStarted   = errors.New("mqttbroker: already started")
	errNoTLSConfig      = errors.New("mqttbroker: no TLS config")
	errWillRefused      = errors.New("will topic not allowed")
	errNoClientIdentity = errors.New("no client identity")
)

// Broker is the embedded MQTT broker. The zero value isn't usable; call New.
type Broker struct {
	log *slog.Logger

	mu      sync.Mutex
	started bool
	closed  bool
	srv     *mqtt.Server
	ctl     *mqtt.Client
	rules   envproto.Rules
	live    map[*mqtt.Client]*session
	nextSub int
}

// session is one live connection: who it is and when it is gone.
type session struct {
	peer envproto.Peer
	gone chan struct{}
}

// New returns a broker that isn't started.
func New(log *slog.Logger) *Broker {
	return &Broker{log: log, live: map[*mqtt.Client]*session{}}
}

// Start listens on addr with tlsConfig and serves until Close. It returns the
// address it is bound to.
func (b *Broker) Start(ctx context.Context, addr string, tlsConfig *tls.Config, rules envproto.Rules) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if tlsConfig == nil {
		return "", errNoTLSConfig
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return "", errAlreadyStarted
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen %s: %w", addr, err)
	}
	caps := mqtt.NewDefaultServerCapabilities()
	caps.MaximumPacketSize = envproto.MaxRequestBytes + maxPacketSlack
	caps.MaximumClientWritesPending = maxClientWritesPending
	caps.MaximumSessionExpiryInterval = maxSessionExpiry
	srv := mqtt.New(&mqtt.Options{Capabilities: caps, InlineClient: true, Logger: b.log})
	b.srv, b.rules = srv, rules
	if err := srv.AddHook(&hook{b: b}, nil); err != nil {
		_ = ln.Close()
		return "", fmt.Errorf("add hook: %w", err)
	}
	tlsLn := tls.NewListener(ln, tlsConfig)
	if err := srv.AddListener(listeners.NewNet(listenerID, tlsLn)); err != nil {
		_ = ln.Close()
		return "", fmt.Errorf("add listener: %w", err)
	}
	if err := srv.Serve(); err != nil {
		_ = ln.Close()
		return "", fmt.Errorf("serve: %w", err)
	}
	ctl := srv.NewClient(nil, "inline", controllerID, true)
	ctl.Properties.ProtocolVersion = 5
	b.ctl, b.started = ctl, true
	return ln.Addr().String(), nil
}

// running returns the server and controller client, or errNotRunning.
func (b *Broker) running() (*mqtt.Server, *mqtt.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started || b.closed {
		return nil, nil, errNotRunning
	}
	return b.srv, b.ctl, nil
}

// Publish publishes as the controller: no ACL check. Response Topic,
// Correlation Data, Message Expiry and Retain are kept.
func (b *Broker) Publish(ctx context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: the envproto.Message value is the fixed API.
	if err := ctx.Err(); err != nil {
		return err
	}
	srv, ctl, err := b.running()
	if err != nil {
		return err
	}
	pk := packets.Packet{
		FixedHeader: packets.FixedHeader{Type: packets.Publish, Qos: msg.QoS, Retain: msg.Retain},
		TopicName:   msg.Topic,
		Payload:     msg.Payload,
		PacketID:    uint16(msg.QoS),
		Properties: packets.Properties{
			ResponseTopic:         msg.ResponseTopic,
			CorrelationData:       msg.CorrelationData,
			MessageExpiryInterval: expirySeconds(msg.Expiry),
		},
	}
	if err := srv.InjectPacket(ctl, pk); err != nil {
		return fmt.Errorf("publish %s: %w", msg.Topic, err)
	}
	return nil
}

// Subscribe subscribes the controller to filter, retained messages first.
// The returned func unsubscribes.
func (b *Broker) Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	srv, _, err := b.running()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.nextSub++
	id := b.nextSub
	b.mu.Unlock()
	handler := func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) { fn(messageOf(&pk)) }
	if err := srv.Subscribe(filter, id, handler); err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", filter, err)
	}
	return func() { _ = srv.Unsubscribe(filter, id) }, nil
}

// Clients lists the connected peers, ClientID set.
func (b *Broker) Clients() []envproto.Peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	peers := make([]envproto.Peer, 0, len(b.live))
	for _, s := range b.live {
		peers = append(peers, s.peer)
	}
	return peers
}

// Disconnect drops every client whose certificate has serial, publishing its
// will, and returns how many.
func (b *Broker) Disconnect(serial string) int {
	b.mu.Lock()
	srv := b.srv
	var matched []*mqtt.Client
	for cl, s := range b.live {
		if s.peer.Serial == serial {
			matched = append(matched, cl)
		}
	}
	b.mu.Unlock()
	for _, cl := range matched {
		_ = srv.DisconnectClient(cl, packets.ErrAdministrativeAction)
	}
	return len(matched)
}

// Close stops the listener and every connection. It is safe to call twice.
func (b *Broker) Close() error {
	b.mu.Lock()
	srv, already := b.srv, b.closed
	b.closed = true
	b.mu.Unlock()
	if srv == nil || already {
		return nil
	}
	return srv.Close()
}

// messageOf maps a delivered packet to an envproto.Message.
func messageOf(pk *packets.Packet) envproto.Message {
	return envproto.Message{
		Topic:           pk.TopicName,
		Payload:         bytes.Clone(pk.Payload),
		QoS:             pk.FixedHeader.Qos,
		Retain:          pk.FixedHeader.Retain,
		ResponseTopic:   pk.Properties.ResponseTopic,
		CorrelationData: bytes.Clone(pk.Properties.CorrelationData),
		Expiry:          time.Duration(pk.Properties.MessageExpiryInterval) * time.Second,
	}
}

// track records a live connection.
func (b *Broker) track(cl *mqtt.Client, peer envproto.Peer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.live[cl] = &session{peer: peer, gone: make(chan struct{})}
}

// untrack forgets a connection and wakes whoever waits for it.
func (b *Broker) untrack(cl *mqtt.Client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.live[cl]; ok {
		delete(b.live, cl)
		close(s.gone)
	}
}

// peerOf is the peer of a connection. A client the broker no longer tracks
// still holds a session (deliveries to it are ACL-checked), and its client
// id is its certificate's CN.
func (b *Broker) peerOf(cl *mqtt.Client) envproto.Peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.live[cl]; ok {
		return s.peer
	}
	return envproto.Peer{ClientID: cl.ID, CN: cl.ID}
}

// takeOver ends the old connection of the environment id, if any, before the
// new one is admitted. mochi's own takeover can lose the new client's
// messages (its issue 562), so the old session is removed here.
func (b *Broker) takeOver(id string) {
	b.mu.Lock()
	srv := b.srv
	var old *mqtt.Client
	var gone chan struct{}
	for cl, s := range b.live {
		if s.peer.CN == id {
			old, gone = cl, s.gone
		}
	}
	b.mu.Unlock()
	if old == nil {
		return
	}
	_ = srv.DisconnectClient(old, packets.ErrSessionTakenOver)
	select {
	case <-gone:
	case <-time.After(takeOverWait):
		b.log.Warn("old connection did not end", "client", id)
	}
	if current, ok := srv.Clients.Get(id); ok && current == old {
		srv.UnsubscribeClient(old)
		old.ClearInflights()
		srv.Clients.Delete(id)
	}
}

// hook is the module's hook: identity, ACL and the connection map.
type hook struct {
	mqtt.HookBase
	b *Broker
}

// ID names the hook.
func (*hook) ID() string { return "ws-environments" }

// Provides lists the hook points this hook implements.
func (*hook) Provides(point byte) bool {
	return slices.Contains([]byte{mqtt.OnConnect, mqtt.OnConnectAuthenticate, mqtt.OnACLCheck, mqtt.OnDisconnect}, point)
}

// OnConnect checks the will topic with the publish ACL: mochi publishes a
// will without asking, so a client could otherwise set one on another
// environment's status. An error closes the connection.
func (h *hook) OnConnect(cl *mqtt.Client, pk packets.Packet) error { //nolint:gocritic // hugeParam: the module fixes the hook signature.
	if !pk.Connect.WillFlag {
		return nil
	}
	peer, err := peerOfConn(cl)
	if err != nil {
		return err
	}
	if h.b.rules.Allow != nil && !h.b.rules.Allow(peer, envproto.AccessPublish, pk.Connect.WillTopic) {
		h.b.log.Warn("acl violation", "client", peer.ClientID, "topic", pk.Connect.WillTopic)
		return errWillRefused
	}
	return nil
}

// OnConnectAuthenticate sets the client id to the certificate CN, ignores the
// claimed id, username and password, and asks rules.Connect.
func (h *hook) OnConnectAuthenticate(cl *mqtt.Client, _ packets.Packet) bool { //nolint:gocritic // hugeParam: the module fixes the hook signature.
	peer, err := peerOfConn(cl)
	if err != nil {
		h.b.log.Warn("connection refused", "error", err)
		return false
	}
	if h.b.rules.Connect != nil {
		if err := h.b.rules.Connect(peer); err != nil {
			h.b.log.Warn("connection refused", "client", peer.ClientID, "error", err)
			return false
		}
	}
	cl.ID = peer.ClientID
	cl.Properties.Username = nil
	h.b.takeOver(peer.CN)
	h.b.track(cl, peer)
	return true
}

// OnACLCheck asks rules.Allow. A refusal disconnects the client.
func (h *hook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	access := envproto.AccessSubscribe
	if write {
		access = envproto.AccessPublish
	}
	peer := h.b.peerOf(cl)
	if h.b.rules.Allow == nil || h.b.rules.Allow(peer, access, topic) {
		return true
	}
	h.b.log.Warn("acl violation", "client", peer.ClientID, "topic", topic)
	_ = h.b.srv.DisconnectClient(cl, packets.ErrNotAuthorized)
	return false
}

// OnDisconnect forgets the connection.
func (h *hook) OnDisconnect(cl *mqtt.Client, _ error, _ bool) {
	h.b.untrack(cl)
}

// peerOfConn reads the verified client certificate of a connection. The
// client id is the certificate's CN.
func peerOfConn(cl *mqtt.Client) (envproto.Peer, error) {
	conn, ok := cl.Net.Conn.(*tls.Conn)
	if !ok {
		return envproto.Peer{}, errNoClientIdentity
	}
	state := conn.ConnectionState()
	peer, err := envproto.PeerFromState(&state)
	if err != nil {
		return envproto.Peer{}, fmt.Errorf("client identity: %w", err)
	}
	peer.ClientID = peer.CN
	return peer, nil
}

// expirySeconds is d in whole seconds, rounded up, for a Message Expiry
// Interval; 0 means none.
func expirySeconds(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	return uint32(min((d+time.Second-1)/time.Second, math.MaxUint32))
}
