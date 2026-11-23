package mqttbroker_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/mqttbroker"
	"github.com/bobcob7/workspaces/internal/mqttclient"
)

var errBadClient = errors.New("bad client")

// syncBuffer is a log sink the broker's goroutines and the test may share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testCA issues certificates for the tests.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

var serials = struct {
	sync.Mutex
	next int64
}{next: 100}

func nextSerial() *big.Int {
	serials.Lock()
	defer serials.Unlock()
	serials.next++
	return big.NewInt(serials.next)
}

// issue returns a certificate signed by ca with the given common name.
func (c *testCA) issue(t *testing.T, cn string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, &key.PublicKey, c.key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: key}
}

// serial is the certificate's serial as the broker reports it.
func serial(t *testing.T, cert *tls.Certificate) string {
	t.Helper()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	return leaf.SerialNumber.Text(16)
}

// testRules lets a client publish and subscribe under env/<its CN>/ and
// refuses the CN "bad".
func testRules() envproto.Rules {
	return envproto.Rules{
		Connect: func(p envproto.Peer) error {
			if p.CN == "bad" {
				return errBadClient
			}
			return nil
		},
		Allow: func(p envproto.Peer, _ envproto.Access, topic string) bool {
			return strings.HasPrefix(topic, "env/"+p.CN+"/")
		},
	}
}

// brokerFixture is a started broker and the CA its clients use.
type brokerFixture struct {
	broker *mqttbroker.Broker
	addr   string
	ca     *testCA
	logs   *syncBuffer
}

func startBroker(t *testing.T) *brokerFixture {
	t.Helper()
	logs := &syncBuffer{}
	ca := newTestCA(t, "test ca")
	server := ca.issue(t, "broker", x509.ExtKeyUsageServerAuth)
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{server},
		ClientCAs:    ca.pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	b := mqttbroker.New(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { assert.NoError(t, b.Close()) })
	addr, err := b.Start(t.Context(), "127.0.0.1:0", cfg, testRules())
	require.NoError(t, err)
	return &brokerFixture{broker: b, addr: addr, ca: ca, logs: logs}
}

// connect connects a client with cn's certificate, claiming claimedID.
func (f *brokerFixture) connect(t *testing.T, cn, claimedID string, will *envproto.Message) (*mqttclient.Client, error) {
	t.Helper()
	cert := f.ca.issue(t, cn, x509.ExtKeyUsageClientAuth)
	return f.connectWith(t, &cert, claimedID, will)
}

func (f *brokerFixture) connectWith(t *testing.T, cert *tls.Certificate, claimedID string, will *envproto.Message) (*mqttclient.Client, error) {
	t.Helper()
	client := mqttclient.New(slog.New(slog.NewTextHandler(&syncBuffer{}, nil)))
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	err := client.Connect(t.Context(), envproto.ConnectOptions{
		Address:   f.addr,
		TLS:       &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: f.ca.pool, Certificates: []tls.Certificate{*cert}, ServerName: "127.0.0.1"},
		ClientID:  claimedID,
		KeepAlive: 30 * time.Second,
		Will:      will,
		WillDelay: time.Second,
	})
	return client, err
}

func receiver() (func(envproto.Message), <-chan envproto.Message) {
	ch := make(chan envproto.Message, 16)
	return func(msg envproto.Message) { ch <- msg }, ch
}

func waitMessage(t *testing.T, ch <-chan envproto.Message) envproto.Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(10 * time.Second):
		require.FailNow(t, "no message arrived")
		return envproto.Message{}
	}
}

func waitClosed(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the connection stayed open")
	}
}

func TestBroker_Start(t *testing.T) {
	t.Run("returns the bound address and a second Start fails", func(t *testing.T) {
		f := startBroker(t)
		host, port, err := net.SplitHostPort(f.addr)
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1", host)
		assert.NotEqual(t, "0", port)
		_, err = f.broker.Start(t.Context(), "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13}, testRules())
		assert.Error(t, err)
	})
	t.Run("a cancelled context or a missing TLS config fails", func(t *testing.T) {
		b := mqttbroker.New(slog.New(slog.NewTextHandler(&syncBuffer{}, nil)))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := b.Start(ctx, "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13}, testRules())
		require.ErrorIs(t, err, context.Canceled)
		_, err = b.Start(t.Context(), "127.0.0.1:0", nil, testRules())
		assert.Error(t, err)
	})
	t.Run("a client without a certificate or with another CA's can't connect", func(t *testing.T) {
		f := startBroker(t)
		client := mqttclient.New(slog.New(slog.NewTextHandler(&syncBuffer{}, nil)))
		err := client.Connect(t.Context(), envproto.ConnectOptions{
			Address:  f.addr,
			TLS:      &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: f.ca.pool, ServerName: "127.0.0.1"},
			ClientID: "a",
		})
		require.Error(t, err)
		other := newTestCA(t, "other ca")
		foreign := other.issue(t, "a", x509.ExtKeyUsageClientAuth)
		_, err = f.connectWith(t, &foreign, "a", nil)
		assert.Error(t, err)
	})
}

func TestBroker_connect(t *testing.T) {
	t.Run("the client id is the certificate CN, whatever the client claims", func(t *testing.T) {
		f := startBroker(t)
		cert := f.ca.issue(t, "a", x509.ExtKeyUsageClientAuth)
		_, err := f.connectWith(t, &cert, "someone-else", nil)
		require.NoError(t, err)
		peers := f.broker.Clients()
		require.Len(t, peers, 1)
		assert.Equal(t, "a", peers[0].ClientID)
		assert.Equal(t, "a", peers[0].CN)
		assert.Equal(t, serial(t, &cert), peers[0].Serial)
		assert.Equal(t, envproto.Fingerprint(f.ca.cert), peers[0].Issuer)
	})
	t.Run("rules.Connect refusing a client keeps it out", func(t *testing.T) {
		f := startBroker(t)
		_, err := f.connect(t, "bad", "bad", nil)
		require.Error(t, err)
		assert.Empty(t, f.broker.Clients())
	})
	t.Run("a will on another client's topic is refused and logged", func(t *testing.T) {
		f := startBroker(t)
		_, err := f.connect(t, "a", "a", &envproto.Message{Topic: "env/b/status", Payload: []byte("offline"), QoS: 1})
		require.Error(t, err)
		assert.Empty(t, f.broker.Clients())
		assert.Contains(t, f.logs.String(), "acl violation")
		assert.Contains(t, f.logs.String(), "env/b/status")
	})
	t.Run("a reconnect of the same CN replaces the old connection", func(t *testing.T) {
		f := startBroker(t)
		first, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		_, err = f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		waitClosed(t, first.Done())
		assert.Len(t, f.broker.Clients(), 1)
	})
}

func TestBroker_ACL(t *testing.T) {
	t.Run("publishing to another client's topic disconnects and logs", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		_ = client.Publish(t.Context(), envproto.Message{Topic: "env/b/status", Payload: []byte("x"), QoS: 1})
		waitClosed(t, client.Done())
		assert.Contains(t, f.logs.String(), "acl violation")
		assert.Contains(t, f.logs.String(), "client=a")
	})
	t.Run("a QoS 0 publish to another client's topic disconnects too", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		_ = client.Publish(t.Context(), envproto.Message{Topic: "env/b/status", Payload: []byte("x")})
		waitClosed(t, client.Done())
	})
	t.Run("subscribing to a wildcard that reaches other clients disconnects", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		_ = client.Subscribe(t.Context(), "env/#", func(envproto.Message) {})
		waitClosed(t, client.Done())
	})
	t.Run("an allowed publish keeps the client connected", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		require.NoError(t, client.Publish(t.Context(), envproto.Message{Topic: "env/a/status", Payload: []byte("x"), QoS: 1}))
		select {
		case <-client.Done():
			require.FailNow(t, "the client was disconnected")
		case <-time.After(200 * time.Millisecond):
		}
	})
}

func TestBroker_Publish(t *testing.T) {
	t.Run("keeps Response Topic, Correlation Data, Message Expiry and Retain to a client", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		fn, got := receiver()
		require.NoError(t, client.Subscribe(t.Context(), "env/a/req", fn))
		sent := envproto.Message{
			Topic:           "env/a/req",
			Payload:         []byte(`{"op":"x"}`),
			QoS:             1,
			ResponseTopic:   "env/a/res/r1",
			CorrelationData: []byte{0, 1, 2, 0xff, 0xfe},
			Expiry:          30 * time.Second,
		}
		require.NoError(t, f.broker.Publish(t.Context(), sent))
		msg := waitMessage(t, got)
		assert.Equal(t, sent.Payload, msg.Payload)
		assert.Equal(t, sent.ResponseTopic, msg.ResponseTopic)
		assert.Equal(t, sent.CorrelationData, msg.CorrelationData)
		assert.InDelta(t, sent.Expiry.Seconds(), msg.Expiry.Seconds(), 2)
	})
	t.Run("a retained message reaches a client that subscribes later", func(t *testing.T) {
		f := startBroker(t)
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: "env/a/req", Payload: []byte("kept"), QoS: 1, Retain: true}))
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		fn, got := receiver()
		require.NoError(t, client.Subscribe(t.Context(), "env/a/req", fn))
		msg := waitMessage(t, got)
		assert.Equal(t, []byte("kept"), msg.Payload)
		assert.True(t, msg.Retain)
	})
	t.Run("fails when the broker isn't running", func(t *testing.T) {
		b := mqttbroker.New(slog.New(slog.NewTextHandler(&syncBuffer{}, nil)))
		assert.Error(t, b.Publish(t.Context(), envproto.Message{Topic: "env/a/req"}))
	})
}

func TestBroker_Subscribe(t *testing.T) {
	t.Run("the controller receives a client's message with its properties", func(t *testing.T) {
		f := startBroker(t)
		fn, got := receiver()
		unsubscribe, err := f.broker.Subscribe(t.Context(), "env/+/res/#", fn)
		require.NoError(t, err)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		sent := envproto.Message{
			Topic:           "env/a/res/r1",
			Payload:         []byte("done"),
			QoS:             1,
			ResponseTopic:   "env/a/req",
			CorrelationData: []byte("corr"),
			Expiry:          20 * time.Second,
		}
		require.NoError(t, client.Publish(t.Context(), sent))
		msg := waitMessage(t, got)
		assert.Equal(t, sent.Topic, msg.Topic)
		assert.Equal(t, sent.Payload, msg.Payload)
		assert.Equal(t, sent.ResponseTopic, msg.ResponseTopic)
		assert.Equal(t, sent.CorrelationData, msg.CorrelationData)
		assert.InDelta(t, sent.Expiry.Seconds(), msg.Expiry.Seconds(), 2)
		unsubscribe()
		require.NoError(t, client.Publish(t.Context(), envproto.Message{Topic: "env/a/res/r2", Payload: []byte("late"), QoS: 1}))
		select {
		case msg := <-got:
			require.FailNow(t, "a message arrived after unsubscribe", string(msg.Payload))
		case <-time.After(200 * time.Millisecond):
		}
	})
	t.Run("retained messages are delivered first", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		require.NoError(t, client.Publish(t.Context(), envproto.Message{Topic: "env/a/status", Payload: []byte("online"), QoS: 1, Retain: true}))
		fn, got := receiver()
		_, err = f.broker.Subscribe(t.Context(), "env/+/status", fn)
		require.NoError(t, err)
		msg := waitMessage(t, got)
		assert.Equal(t, []byte("online"), msg.Payload)
		assert.True(t, msg.Retain)
	})
}

func TestBroker_Disconnect(t *testing.T) {
	t.Run("drops the client with the serial and publishes its will", func(t *testing.T) {
		f := startBroker(t)
		fn, got := receiver()
		_, err := f.broker.Subscribe(t.Context(), "env/a/status", fn)
		require.NoError(t, err)
		cert := f.ca.issue(t, "a", x509.ExtKeyUsageClientAuth)
		client, err := f.connectWith(t, &cert, "a", &envproto.Message{Topic: "env/a/status", Payload: []byte("offline"), QoS: 1, Retain: true})
		require.NoError(t, err)
		other, err := f.connect(t, "b", "b", nil)
		require.NoError(t, err)
		assert.Equal(t, 1, f.broker.Disconnect(serial(t, &cert)))
		waitClosed(t, client.Done())
		msg := waitMessage(t, got)
		assert.Equal(t, []byte("offline"), msg.Payload)
		select {
		case <-other.Done():
			require.FailNow(t, "another client was disconnected")
		default:
		}
		assert.Len(t, f.broker.Clients(), 1)
	})
	t.Run("an unknown serial drops nobody", func(t *testing.T) {
		f := startBroker(t)
		assert.Zero(t, f.broker.Disconnect("ffff"))
	})
}

func TestBroker_Close(t *testing.T) {
	t.Run("closes every connection and refuses new ones", func(t *testing.T) {
		f := startBroker(t)
		client, err := f.connect(t, "a", "a", nil)
		require.NoError(t, err)
		require.NoError(t, f.broker.Close())
		waitClosed(t, client.Done())
		_, err = f.connect(t, "a", "a", nil)
		require.Error(t, err)
		assert.Error(t, f.broker.Publish(t.Context(), envproto.Message{Topic: "env/a/req"}))
	})
	t.Run("a second Close is harmless", func(t *testing.T) {
		f := startBroker(t)
		require.NoError(t, f.broker.Close())
		assert.NoError(t, f.broker.Close())
	})
}
