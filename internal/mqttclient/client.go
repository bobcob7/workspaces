// Package mqttclient is the environment agent's MQTT 5 connection, built on
// eclipse/paho.golang's paho package (ADR 0074). The agent reconnects itself,
// so autopaho is not used.
package mqttclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// minSessionExpiry is the shortest Session Expiry Interval the client asks
// for. A broker publishes a will when the will delay passes or the session
// ends, whichever is first, so a session that ends with the connection (0)
// would send the will at once instead of after the will delay.
const minSessionExpiry = 10 * time.Second

// Errors returned by Client.
var (
	// ErrNotConnected is returned when the client has no connection.
	ErrNotConnected = errors.New("mqttclient: not connected")
	// ErrRefused is returned when the broker refuses a publish or a subscription.
	ErrRefused = errors.New("mqttclient: refused by the broker")

	errAlreadyConnected = errors.New("mqttclient: already connected")
	errNoTLSConfig      = errors.New("mqttclient: no TLS config")
)

// Client is an MQTT 5 client. The zero value isn't usable; call New.
type Client struct {
	log *slog.Logger

	mu       sync.Mutex
	conn     *paho.Client
	done     chan struct{}
	handlers []handler
}

// handler is one filter and the function that receives its messages.
type handler struct {
	filter string
	fn     func(envproto.Message)
}

// New returns a client that isn't connected.
func New(log *slog.Logger) *Client {
	return &Client{log: log}
}

// Connect dials opts.Address over TLS and sends CONNECT. It may be called
// again after Done has closed.
func (c *Client) Connect(ctx context.Context, opts envproto.ConnectOptions) error { //nolint:gocritic // hugeParam: the envproto.ConnectOptions value is the fixed API.
	if opts.TLS == nil {
		return errNoTLSConfig
	}
	c.mu.Lock()
	if c.conn != nil && !isClosed(c.done) {
		c.mu.Unlock()
		return errAlreadyConnected
	}
	c.handlers = nil
	c.mu.Unlock()
	dialer := tls.Dialer{Config: opts.TLS}
	raw, err := dialer.DialContext(ctx, "tcp", opts.Address)
	if err != nil {
		return fmt.Errorf("dial %s: %w", opts.Address, err)
	}
	conn := paho.NewClient(paho.ClientConfig{
		Conn:               packets.NewThreadSafeConn(raw),
		OnPublishReceived:  []func(paho.PublishReceived) (bool, error){c.receive},
		OnServerDisconnect: func(d *paho.Disconnect) { c.log.Debug("server disconnected the client", "reason_code", d.ReasonCode) },
		OnClientError:      func(err error) { c.log.Debug("mqtt client error", "error", err) },
	})
	if _, err := conn.Connect(ctx, connectPacket(opts)); err != nil {
		_ = raw.Close()
		return fmt.Errorf("connect: %w", err)
	}
	done := make(chan struct{})
	c.mu.Lock()
	c.conn = conn
	c.done = done
	c.mu.Unlock()
	go func() {
		<-conn.Done()
		close(done)
	}()
	return nil
}

// Publish sends msg and, for QoS 1 and 2, waits for the broker's answer.
func (c *Client) Publish(ctx context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: the envproto.Message value is the fixed API.
	conn, err := c.connection()
	if err != nil {
		return err
	}
	resp, err := conn.Publish(ctx, toPublish(&msg))
	if err != nil {
		return fmt.Errorf("publish %s: %w", msg.Topic, err)
	}
	if resp != nil && resp.ReasonCode >= packets.PubackUnspecifiedError {
		return fmt.Errorf("%w: publish %s, reason code 0x%02x", ErrRefused, msg.Topic, resp.ReasonCode)
	}
	return nil
}

// Subscribe subscribes to filter at QoS 1; fn receives every message on it,
// retained ones first.
func (c *Client) Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error {
	conn, err := c.connection()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.handlers = append(c.handlers, handler{filter: filter, fn: fn})
	c.mu.Unlock()
	ack, err := conn.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: filter, QoS: 1}}})
	if err == nil && ack != nil && slices.ContainsFunc(ack.Reasons, func(r byte) bool { return r >= packets.SubackUnspecifiederror }) {
		err = fmt.Errorf("%w: subscribe %s", ErrRefused, filter)
	}
	if err != nil {
		c.removeHandler(filter)
		return fmt.Errorf("subscribe %s: %w", filter, err)
	}
	return nil
}

// Done closes when the connection ends: a client error, a server
// DISCONNECT or Close. It is nil before the first Connect.
func (c *Client) Done() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

// Close sends a normal DISCONNECT, so the broker discards the will. It does
// nothing on a client that isn't connected.
func (c *Client) Close() error {
	c.mu.Lock()
	conn, done := c.conn, c.done
	c.mu.Unlock()
	if conn == nil || isClosed(done) {
		return nil
	}
	err := conn.Disconnect(&paho.Disconnect{ReasonCode: packets.DisconnectNormalDisconnection})
	<-done
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("disconnect: %w", err)
	}
	return nil
}

// connection returns the live paho client, or ErrNotConnected.
func (c *Client) connection() (*paho.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || isClosed(c.done) {
		return nil, ErrNotConnected
	}
	return c.conn, nil
}

func (c *Client) removeHandler(filter string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := slices.IndexFunc(c.handlers, func(h handler) bool { return h.filter == filter }); i >= 0 {
		c.handlers = slices.Delete(c.handlers, i, i+1)
	}
}

// receive hands an incoming PUBLISH to the handlers whose filter matches.
func (c *Client) receive(pr paho.PublishReceived) (bool, error) {
	msg := fromPublish(pr.Packet)
	c.mu.Lock()
	var fns []func(envproto.Message)
	for _, h := range c.handlers {
		if envproto.Match(h.filter, msg.Topic) {
			fns = append(fns, h.fn)
		}
	}
	c.mu.Unlock()
	for _, fn := range fns {
		fn(msg)
	}
	return len(fns) > 0, nil
}

func isClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// connectPacket is the CONNECT for opts: Clean Start, a Session Expiry
// Interval of at least minSessionExpiry (and the will delay), the keep-alive
// and the will with its Will Delay.
func connectPacket(opts envproto.ConnectOptions) *paho.Connect { //nolint:gocritic // hugeParam: the envproto.ConnectOptions value is the fixed API.
	expiry := seconds(max(minSessionExpiry, opts.WillDelay))
	pc := &paho.Connect{
		ClientID:   opts.ClientID,
		KeepAlive:  uint16(min(seconds(opts.KeepAlive), 1<<16-1)),
		CleanStart: true,
		Properties: &paho.ConnectProperties{SessionExpiryInterval: &expiry},
	}
	if opts.Will == nil {
		return pc
	}
	will := toPublish(opts.Will)
	pc.WillMessage = &paho.WillMessage{Topic: will.Topic, Payload: will.Payload, QoS: will.QoS, Retain: will.Retain}
	pc.WillProperties = &paho.WillProperties{
		MessageExpiry:   will.Properties.MessageExpiry,
		ResponseTopic:   will.Properties.ResponseTopic,
		CorrelationData: will.Properties.CorrelationData,
	}
	if opts.WillDelay > 0 {
		delay := seconds(opts.WillDelay)
		pc.WillProperties.WillDelayInterval = &delay
	}
	return pc
}

// toPublish maps msg to a PUBLISH, with every property it carries.
func toPublish(msg *envproto.Message) *paho.Publish {
	props := &paho.PublishProperties{
		ResponseTopic:   msg.ResponseTopic,
		CorrelationData: msg.CorrelationData,
	}
	if msg.Expiry > 0 {
		expiry := seconds(msg.Expiry)
		props.MessageExpiry = &expiry
	}
	return &paho.Publish{
		QoS:        msg.QoS,
		Retain:     msg.Retain,
		Topic:      msg.Topic,
		Payload:    msg.Payload,
		Properties: props,
	}
}

// fromPublish is the reverse of toPublish.
func fromPublish(p *paho.Publish) envproto.Message {
	msg := envproto.Message{
		Topic:   p.Topic,
		Payload: p.Payload,
		QoS:     p.QoS,
		Retain:  p.Retain,
	}
	if p.Properties == nil {
		return msg
	}
	msg.ResponseTopic = p.Properties.ResponseTopic
	msg.CorrelationData = p.Properties.CorrelationData
	if p.Properties.MessageExpiry != nil {
		msg.Expiry = time.Duration(*p.Properties.MessageExpiry) * time.Second
	}
	return msg
}

// seconds is d in whole seconds, rounded up, so a short non-zero duration
// never becomes 0.
func seconds(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	return uint32((d + time.Second - 1) / time.Second)
}
