// Package envtest is an in-memory MQTT broker for tests of the controller
// and the agent. Never imported outside _test.go files.
//
// A client connects over a real TLS handshake, so certificate checks are
// exercised; after that, messages are routed in memory. The handshake runs
// over memConn, a buffered in-memory connection rather than net.Pipe, because
// a pipe deadlocks when the server sends a TLS alert. memConn ignores
// deadlines, so nothing here may depend on a read or write timing out. Will
// Delay is ignored: a will is published at once.
package envtest

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

var (
	// ErrNotServing is returned when a client connects to a broker that
	// isn't started or has been closed.
	ErrNotServing = errors.New("envtest: broker not started")
	// ErrRefused is returned when a connection fails its handshake or is
	// refused by rules.Connect.
	ErrRefused = errors.New("envtest: connection refused")
	// ErrNotConnected is returned by a client that isn't connected.
	ErrNotConnected = errors.New("envtest: not connected")
	// ErrDisconnected is returned when the broker drops a client that broke
	// an ACL rule.
	ErrDisconnected = errors.New("envtest: disconnected by the broker")

	errAlreadyStarted   = errors.New("envtest: broker already started")
	errAlreadyConnected = errors.New("envtest: client already connected")
	errNoTLSConfig      = errors.New("envtest: no TLS config")
)

// Broker is an in-memory MQTT broker. The zero value isn't usable; call
// NewBroker.
type Broker struct {
	mu        sync.Mutex
	started   bool
	tlsConfig *tls.Config
	rules     envproto.Rules
	clients   []*Client
	subs      []subscription
	retained  map[string]envproto.Message
	nextSub   int
}

// subscription is one filter a handler listens on. client is nil for the
// controller.
type subscription struct {
	id     int
	filter string
	client *Client
	fn     func(envproto.Message)
}

// NewBroker returns a broker that isn't started.
func NewBroker() *Broker {
	return &Broker{retained: map[string]envproto.Message{}}
}

// Start records tlsConfig and rules and returns addr unchanged. A second
// Start fails.
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
	cfg := tlsConfig.Clone()
	// Tickets would be written after the handshake, to a pipe nobody reads.
	cfg.SessionTicketsDisabled = true
	b.started = true
	b.tlsConfig = cfg
	b.rules = rules
	return addr, nil
}

// Publish publishes as the controller: no ACL check.
func (b *Broker) Publish(ctx context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: the envproto.Message value is the fixed API.
	if err := ctx.Err(); err != nil {
		return err
	}
	b.route(&msg)
	return nil
}

// Subscribe subscribes the controller; retained messages matching filter
// are delivered first. The returned func unsubscribes.
func (b *Broker) Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, retained, err := b.addSub(filter, nil, fn)
	if err != nil {
		return nil, err
	}
	deliver(retained, fn)
	return func() { b.removeSub(id) }, nil
}

// Clients lists connected peers, ClientID set.
func (b *Broker) Clients() []envproto.Peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	peers := make([]envproto.Peer, 0, len(b.clients))
	for _, c := range b.clients {
		peers = append(peers, c.peer)
	}
	return peers
}

// Disconnect drops every client with serial, publishing each one's will;
// it returns how many.
func (b *Broker) Disconnect(serial string) int {
	b.mu.Lock()
	var matched []*Client
	for _, c := range b.clients {
		if c.peer.Serial == serial {
			matched = append(matched, c)
		}
	}
	b.mu.Unlock()
	dropped := 0
	for _, c := range matched {
		if b.drop(c, true) {
			dropped++
		}
	}
	return dropped
}

// Retained returns the retained message on topic.
func (b *Broker) Retained(topic string) (envproto.Message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg, ok := b.retained[topic]
	return msg, ok
}

// Close stops the broker; clients' Done channels close, wills aren't
// published. Like the real broker, it forgets its subscriptions and retained
// messages, and Start can run again.
func (b *Broker) Close() error {
	b.mu.Lock()
	b.started = false
	b.subs = nil
	b.retained = map[string]envproto.Message{}
	clients := slices.Clone(b.clients)
	b.mu.Unlock()
	for _, c := range clients {
		b.drop(c, false)
	}
	return nil
}

// NewClient returns an unconnected client of b.
func (b *Broker) NewClient() *Client {
	return &Client{broker: b}
}

// Client is a connection to a Broker, as an agent or the controller's
// test double would hold it.
type Client struct {
	broker *Broker

	// Fields below are guarded by broker.mu.
	connected bool
	peer      envproto.Peer
	will      *envproto.Message
	done      chan struct{}
	conns     []net.Conn
}

// Connect connects c. ErrNotServing before Start. Runs tls.Server (b's config) and
// tls.Client (opts.TLS) handshakes over memConn concurrently; any failure
// is ErrRefused wrapping it. Then PeerFromState(server side), ClientID =
// opts.ClientID, rules.Connect(peer) (error → ErrRefused). Keeps opts.Will.
// A client may Connect again after Done closed; Done is then a new channel.
func (c *Client) Connect(ctx context.Context, opts envproto.ConnectOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	serverCfg, rules, err := c.broker.serving()
	if err != nil {
		return err
	}
	if opts.TLS == nil {
		return fmt.Errorf("%w: %w", ErrRefused, errNoTLSConfig)
	}
	srv, cli, peer, err := handshake(ctx, serverCfg, opts.TLS)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	peer.ClientID = opts.ClientID
	if rules.Connect != nil {
		if err := rules.Connect(peer); err != nil {
			closeAll(srv, cli)
			return fmt.Errorf("%w: %w", ErrRefused, err)
		}
	}
	return c.broker.register(c, peer, opts.Will, []net.Conn{srv, cli})
}

// Publish publishes msg from c. rules.Allow(peer, AccessPublish, topic) false → the client is
// disconnected (will published) and ErrDisconnected is returned.
func (c *Client) Publish(ctx context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: the envproto.Message value is the fixed API.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.admit(envproto.AccessPublish, msg.Topic); err != nil {
		return err
	}
	c.broker.route(&msg)
	return nil
}

// Subscribe subscribes c to filter. rules.Allow(peer, AccessSubscribe, filter) false → as Publish.
// Retained messages matching filter are delivered to fn before it returns.
func (c *Client) Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.admit(envproto.AccessSubscribe, filter); err != nil {
		return err
	}
	_, retained, err := c.broker.addSub(filter, c, fn)
	if err != nil {
		return err
	}
	deliver(retained, fn)
	return nil
}

// Done closes when the broker drops the client or Close is called.
func (c *Client) Done() <-chan struct{} {
	c.broker.mu.Lock()
	defer c.broker.mu.Unlock()
	return c.done
}

// Close is a normal disconnect: no will. It does nothing on a client that
// isn't connected.
func (c *Client) Close() error {
	c.broker.drop(c, false)
	return nil
}

// serving returns the server config and rules, or ErrNotServing.
func (b *Broker) serving() (*tls.Config, envproto.Rules, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		return nil, envproto.Rules{}, ErrNotServing
	}
	return b.tlsConfig, b.rules, nil
}

// admit checks that c is connected and may access topic. A client that
// isn't allowed is dropped with its will.
func (c *Client) admit(access envproto.Access, topic string) error {
	b := c.broker
	b.mu.Lock()
	connected, peer, allow := c.connected, c.peer, b.rules.Allow
	b.mu.Unlock()
	if !connected {
		return ErrNotConnected
	}
	if allow != nil && !allow(peer, access, topic) {
		b.drop(c, true)
		return ErrDisconnected
	}
	return nil
}

// register makes c a connected client, or closes conns and fails if the
// broker stopped or c is already connected.
func (b *Broker) register(c *Client, peer envproto.Peer, will *envproto.Message, conns []net.Conn) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		closeAll(conns...)
		return ErrNotServing
	}
	if c.connected {
		closeAll(conns...)
		return errAlreadyConnected
	}
	c.connected = true
	c.peer = peer
	c.will = will
	c.done = make(chan struct{})
	c.conns = conns
	b.clients = append(b.clients, c)
	return nil
}

// drop disconnects c, closes its Done channel and, if withWill, publishes
// its will. It reports whether c was connected.
func (b *Broker) drop(c *Client, withWill bool) bool {
	b.mu.Lock()
	if !c.connected {
		b.mu.Unlock()
		return false
	}
	c.connected = false
	close(c.done)
	b.clients = slices.DeleteFunc(b.clients, func(other *Client) bool { return other == c })
	b.subs = slices.DeleteFunc(b.subs, func(s subscription) bool { return s.client == c })
	conns := c.conns
	c.conns = nil
	var will envproto.Message
	hasWill := withWill && c.will != nil
	if hasWill {
		will = *c.will
	}
	b.mu.Unlock()
	closeAll(conns...)
	if hasWill {
		b.route(&will)
	}
	return true
}

// addSub registers fn on filter and returns the retained messages it
// should see first. A client that has been dropped can't subscribe.
func (b *Broker) addSub(filter string, c *Client, fn func(envproto.Message)) (int, []envproto.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c != nil && !c.connected {
		return 0, nil, ErrNotConnected
	}
	b.nextSub++
	b.subs = append(b.subs, subscription{id: b.nextSub, filter: filter, client: c, fn: fn})
	return b.nextSub, b.retainedMatching(filter), nil
}

func (b *Broker) removeSub(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = slices.DeleteFunc(b.subs, func(s subscription) bool { return s.id == id })
}

// retainedMatching returns the retained messages matching filter, sorted
// by topic. b.mu must be held.
func (b *Broker) retainedMatching(filter string) []envproto.Message {
	var msgs []envproto.Message
	for topic, msg := range b.retained {
		if envproto.Match(filter, topic) {
			msgs = append(msgs, msg)
		}
	}
	slices.SortFunc(msgs, func(x, y envproto.Message) int {
		return strings.Compare(x.Topic, y.Topic)
	})
	return msgs
}

// route stores or clears a retained message and delivers msg to every
// matching subscriber in subscription order.
func (b *Broker) route(msg *envproto.Message) {
	for _, fn := range b.matching(msg) {
		fn(*msg)
	}
}

// matching applies msg's retain flag and returns the handlers that
// subscribe to its topic. The handlers run after the lock is released, so
// a handler may publish.
func (b *Broker) matching(msg *envproto.Message) []func(envproto.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if msg.Retain {
		if len(msg.Payload) == 0 {
			delete(b.retained, msg.Topic)
		} else {
			b.retained[msg.Topic] = *msg
		}
	}
	var handlers []func(envproto.Message)
	for _, s := range b.subs {
		if envproto.Match(s.filter, msg.Topic) {
			handlers = append(handlers, s.fn)
		}
	}
	return handlers
}

// handshake runs the server and client TLS handshakes concurrently over an
// in-memory pair. Either side's failure closes its end, so the other side
// fails too.
func handshake(ctx context.Context, serverCfg, clientCfg *tls.Config) (net.Conn, net.Conn, envproto.Peer, error) {
	srvRaw, cliRaw := newMemConnPair()
	srvTLS := tls.Server(srvRaw, serverCfg)
	cliTLS := tls.Client(cliRaw, clientCfg)
	results := make(chan error, 2)
	go func() { results <- handshakeSide(ctx, srvTLS, srvRaw) }()
	go func() { results <- handshakeSide(ctx, cliTLS, cliRaw) }()
	if err := errors.Join(<-results, <-results); err != nil {
		closeAll(srvRaw, cliRaw)
		return nil, nil, envproto.Peer{}, err
	}
	state := srvTLS.ConnectionState()
	peer, err := envproto.PeerFromState(&state)
	if err != nil {
		closeAll(srvRaw, cliRaw)
		return nil, nil, envproto.Peer{}, err
	}
	return srvRaw, cliRaw, peer, nil
}

func handshakeSide(ctx context.Context, conn *tls.Conn, raw net.Conn) error {
	if err := conn.HandshakeContext(ctx); err != nil {
		closeAll(raw)
		return err
	}
	return nil
}

// memConn is one end of a buffered in-memory connection. Unlike net.Pipe,
// a write doesn't wait for the peer to read: with net.Pipe a server sending
// a TLS alert and a client still writing its handshake block each other.
type memConn struct {
	in  *memBuffer
	out *memBuffer
}

// memBuffer holds the bytes written to one direction of a memConn.
type memBuffer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   bytes.Buffer
	closed bool
}

func newMemConnPair() (net.Conn, net.Conn) {
	ab, ba := newMemBuffer(), newMemBuffer()
	return &memConn{in: ba, out: ab}, &memConn{in: ab, out: ba}
}

func newMemBuffer() *memBuffer {
	b := &memBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (c *memConn) Read(p []byte) (int, error) { return c.in.read(p) }

func (c *memConn) Write(p []byte) (int, error) { return c.out.write(p) }

// Close ends both directions: the peer reads what is buffered, then EOF.
func (c *memConn) Close() error {
	c.in.close()
	c.out.close()
	return nil
}

// Deadlines are not needed: a handshake ends when one side closes.
func (*memConn) SetDeadline(time.Time) error      { return nil }
func (*memConn) SetReadDeadline(time.Time) error  { return nil }
func (*memConn) SetWriteDeadline(time.Time) error { return nil }
func (*memConn) LocalAddr() net.Addr              { return memAddr{} }
func (*memConn) RemoteAddr() net.Addr             { return memAddr{} }

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "envtest" }

func (b *memBuffer) read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.data.Len() == 0 && !b.closed {
		b.cond.Wait()
	}
	if b.data.Len() == 0 {
		return 0, io.EOF
	}
	return b.data.Read(p)
}

func (b *memBuffer) write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, net.ErrClosed
	}
	n, err := b.data.Write(p)
	b.cond.Broadcast()
	return n, err
}

func (b *memBuffer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
}

// closeAll closes every conn. A pipe end can only fail to close once it is
// already closed, so the errors are dropped.
func closeAll(conns ...net.Conn) {
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func deliver(msgs []envproto.Message, fn func(envproto.Message)) {
	for _, msg := range msgs {
		fn(msg)
	}
}
