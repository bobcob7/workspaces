package envtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	testServerName = "envtest.example"
	testEnvID      = "env-1a2b3c4d"
	testStatus     = "ws/v1/env/env-1a2b3c4d/status"
)

// pki is a test certificate authority; its key never leaves the test.
type pki struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newPKI(t *testing.T) pki {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return pki{cert: cert, key: key}
}

func (p pki) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(p.cert)
	return pool
}

func (p pki) issue(t *testing.T, cn string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{testServerName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.cert, &key.PublicKey, p.key)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// serverConfig requires a client certificate from p.
func (p pki) serverConfig(t *testing.T) *tls.Config {
	t.Helper()
	return &tls.Config{
		Certificates: []tls.Certificate{p.issue(t, "broker", x509.ExtKeyUsageServerAuth)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    p.pool(),
	}
}

// clientConfig presents a certificate for cn signed by p.
func (p pki) clientConfig(t *testing.T, cn string) *tls.Config {
	t.Helper()
	return &tls.Config{
		Certificates: []tls.Certificate{p.issue(t, cn, x509.ExtKeyUsageClientAuth)},
		RootCAs:      p.pool(),
		ServerName:   testServerName,
	}
}

type fixture struct {
	broker *Broker
	ca     pki
}

func newFixture(t *testing.T, rules envproto.Rules) fixture {
	t.Helper()
	ca := newPKI(t)
	broker := NewBroker()
	_, err := broker.Start(t.Context(), "127.0.0.1:0", ca.serverConfig(t), rules)
	require.NoError(t, err)
	t.Cleanup(func() { _ = broker.Close() })
	return fixture{broker: broker, ca: ca}
}

// connect connects c as the test environment with a certificate from f.ca.
func (f fixture) connect(t *testing.T, c *Client, clientID string, will *envproto.Message) error {
	t.Helper()
	return c.Connect(t.Context(), envproto.ConnectOptions{
		ClientID: clientID,
		TLS:      f.ca.clientConfig(t, testEnvID),
		Will:     will,
	})
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func willMessage() *envproto.Message {
	return &envproto.Message{Topic: testStatus, Payload: envproto.OfflinePayload(), QoS: 1, Retain: true}
}

func TestBroker_Start(t *testing.T) {
	t.Run("returns the address unchanged", func(t *testing.T) {
		b := NewBroker()
		addr, err := b.Start(t.Context(), "127.0.0.1:9999", newPKI(t).serverConfig(t), envproto.Rules{})
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1:9999", addr)
	})
	t.Run("fails a second time", func(t *testing.T) {
		b := NewBroker()
		ca := newPKI(t)
		_, err := b.Start(t.Context(), "a", ca.serverConfig(t), envproto.Rules{})
		require.NoError(t, err)
		_, err = b.Start(t.Context(), "b", ca.serverConfig(t), envproto.Rules{})
		require.Error(t, err)
	})
	t.Run("needs a TLS config", func(t *testing.T) {
		_, err := NewBroker().Start(t.Context(), "a", nil, envproto.Rules{})
		require.ErrorIs(t, err, errNoTLSConfig)
	})
}

func TestBroker_Publish(t *testing.T) {
	t.Run("retained messages reach later subscribers", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus, Payload: []byte("online"), Retain: true}))
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		var got []envproto.Message
		require.NoError(t, c.Subscribe(t.Context(), "ws/v1/env/+/status", func(m envproto.Message) { got = append(got, m) }))
		require.Len(t, got, 1)
		assert.Equal(t, []byte("online"), got[0].Payload)
	})
	t.Run("an empty retained payload clears the topic", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus, Payload: []byte("online"), Retain: true}))
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus, Retain: true}))
		_, ok := f.broker.Retained(testStatus)
		assert.False(t, ok)
	})
	t.Run("a cancelled context publishes nothing", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.Error(t, f.broker.Publish(ctx, envproto.Message{Topic: testStatus}))
	})
}

func TestBroker_Subscribe(t *testing.T) {
	t.Run("delivers retained messages first and stops after unsubscribe", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus, Payload: []byte("a"), Retain: true}))
		var got []string
		unsubscribe, err := f.broker.Subscribe(t.Context(), "ws/v1/env/#", func(m envproto.Message) { got = append(got, m.Topic) })
		require.NoError(t, err)
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus + "x"}))
		unsubscribe()
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus + "y"}))
		assert.Equal(t, []string{testStatus, testStatus + "x"}, got)
	})
}

func TestBroker_Clients(t *testing.T) {
	t.Run("lists connected peers with their client id", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		require.NoError(t, f.connect(t, f.broker.NewClient(), "agent-1", nil))
		peers := f.broker.Clients()
		require.Len(t, peers, 1)
		assert.Equal(t, "env-1a2b3c4d", peers[0].CN)
		assert.Equal(t, "agent-1", peers[0].ClientID)
	})
}

func TestBroker_Disconnect(t *testing.T) {
	t.Run("drops the matching client and publishes its will", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", willMessage()))
		var wills []envproto.Message
		_, err := f.broker.Subscribe(t.Context(), testStatus, func(m envproto.Message) { wills = append(wills, m) })
		require.NoError(t, err)
		serial := f.broker.Clients()[0].Serial

		assert.Equal(t, 1, f.broker.Disconnect(serial))
		assert.True(t, isClosed(c.Done()))
		assert.Empty(t, f.broker.Clients())
		require.Len(t, wills, 1)
		assert.JSONEq(t, `{"status":"offline"}`, string(wills[0].Payload))
	})
	t.Run("returns zero for an unknown serial", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		assert.Equal(t, 0, f.broker.Disconnect("nope"))
	})
}

func TestBroker_Retained(t *testing.T) {
	t.Run("reports whether a topic holds a message", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		_, ok := f.broker.Retained(testStatus)
		assert.False(t, ok)
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus, Payload: []byte("x"), Retain: true}))
		msg, ok := f.broker.Retained(testStatus)
		require.True(t, ok)
		assert.Equal(t, []byte("x"), msg.Payload)
	})
}

func TestBroker_Close(t *testing.T) {
	t.Run("drops clients without publishing wills and refuses new connections", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", willMessage()))
		var wills []envproto.Message
		_, err := f.broker.Subscribe(t.Context(), testStatus, func(m envproto.Message) { wills = append(wills, m) })
		require.NoError(t, err)

		require.NoError(t, f.broker.Close())
		assert.True(t, isClosed(c.Done()))
		assert.Empty(t, wills)
		err = f.connect(t, f.broker.NewClient(), "again", nil)
		require.ErrorIs(t, err, ErrNotServing)
	})
}

func TestClient_Connect(t *testing.T) {
	t.Run("connects with a certificate the broker trusts", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		require.NoError(t, f.connect(t, f.broker.NewClient(), "agent", nil))
		assert.Len(t, f.broker.Clients(), 1)
	})
	t.Run("refuses before the broker starts", func(t *testing.T) {
		c := NewBroker().NewClient()
		err := c.Connect(t.Context(), envproto.ConnectOptions{TLS: newPKI(t).clientConfig(t, "env-1a2b3c4d")})
		require.ErrorIs(t, err, ErrNotServing)
	})
	t.Run("refuses a client without a certificate", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		err := f.broker.NewClient().Connect(t.Context(), envproto.ConnectOptions{
			TLS: &tls.Config{RootCAs: f.ca.pool(), ServerName: testServerName},
		})
		require.ErrorIs(t, err, ErrRefused)
	})
	t.Run("refuses a certificate from another CA", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		other := newPKI(t)
		err := f.broker.NewClient().Connect(t.Context(), envproto.ConnectOptions{
			TLS: other.clientConfig(t, "env-1a2b3c4d"),
		})
		require.ErrorIs(t, err, ErrRefused)
	})
	t.Run("refuses a missing TLS config", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		err := f.broker.NewClient().Connect(t.Context(), envproto.ConnectOptions{})
		require.ErrorIs(t, err, ErrRefused)
	})
	t.Run("refuses when rules.Connect errors", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{
			Connect: func(envproto.Peer) error { return assert.AnError },
		})
		err := f.connect(t, f.broker.NewClient(), "agent", nil)
		require.ErrorIs(t, err, ErrRefused)
		assert.Empty(t, f.broker.Clients())
	})
	t.Run("refuses a client that is already connected", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		assert.Error(t, f.connect(t, c, "agent", nil))
	})
	t.Run("reconnects after a drop with a new Done channel", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		old := c.Done()
		require.NoError(t, c.Close())
		require.True(t, isClosed(old))
		require.NoError(t, f.connect(t, c, "agent", nil))
		assert.False(t, isClosed(c.Done()))
	})
}

func TestClient_Publish(t *testing.T) {
	t.Run("delivers to subscribers", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		var got []envproto.Message
		_, err := f.broker.Subscribe(t.Context(), testStatus, func(m envproto.Message) { got = append(got, m) })
		require.NoError(t, err)
		require.NoError(t, c.Publish(t.Context(), envproto.Message{Topic: testStatus, Payload: []byte("hi")}))
		require.Len(t, got, 1)
	})
	t.Run("an ACL violation drops the client and publishes its will", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{
			Allow: func(_ envproto.Peer, access envproto.Access, topic string) bool {
				return access != envproto.AccessPublish || topic != "forbidden"
			},
		})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", willMessage()))
		var wills []envproto.Message
		_, err := f.broker.Subscribe(t.Context(), testStatus, func(m envproto.Message) { wills = append(wills, m) })
		require.NoError(t, err)

		err = c.Publish(t.Context(), envproto.Message{Topic: "forbidden"})
		require.ErrorIs(t, err, ErrDisconnected)
		assert.True(t, isClosed(c.Done()))
		assert.Len(t, wills, 1)
	})
	t.Run("refuses when not connected", func(t *testing.T) {
		c := NewBroker().NewClient()
		assert.ErrorIs(t, c.Publish(t.Context(), envproto.Message{Topic: testStatus}), ErrNotConnected)
	})
	t.Run("a handler may publish from inside a delivery", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		var echoed int
		_, err := f.broker.Subscribe(t.Context(), "echo/ping", func(envproto.Message) {
			_ = c.Publish(t.Context(), envproto.Message{Topic: "echo/reply"})
		})
		require.NoError(t, err)
		_, err = f.broker.Subscribe(t.Context(), "echo/reply", func(envproto.Message) { echoed++ })
		require.NoError(t, err)
		require.NoError(t, c.Publish(t.Context(), envproto.Message{Topic: "echo/ping"}))
		assert.Equal(t, 1, echoed)
	})
}

func TestClient_Subscribe(t *testing.T) {
	t.Run("an ACL violation drops the client", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{
			Allow: func(_ envproto.Peer, access envproto.Access, _ string) bool {
				return access != envproto.AccessSubscribe
			},
		})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		err := c.Subscribe(t.Context(), "ws/v1/env/+/req", func(envproto.Message) {})
		require.ErrorIs(t, err, ErrDisconnected)
		assert.True(t, isClosed(c.Done()))
	})
	t.Run("a dropped client's subscriptions stop receiving", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		var got int
		require.NoError(t, c.Subscribe(t.Context(), testStatus, func(envproto.Message) { got++ }))
		require.NoError(t, c.Close())
		require.NoError(t, f.broker.Publish(t.Context(), envproto.Message{Topic: testStatus}))
		assert.Equal(t, 0, got)
	})
	t.Run("refuses when not connected", func(t *testing.T) {
		c := NewBroker().NewClient()
		assert.ErrorIs(t, c.Subscribe(t.Context(), testStatus, func(envproto.Message) {}), ErrNotConnected)
	})
}

func TestClient_Done(t *testing.T) {
	t.Run("closes when the broker drops the client", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", nil))
		done := c.Done()
		f.broker.Disconnect(f.broker.Clients()[0].Serial)
		assert.True(t, isClosed(done))
	})
}

func TestClient_Close(t *testing.T) {
	t.Run("disconnects without publishing the will", func(t *testing.T) {
		f := newFixture(t, envproto.Rules{})
		c := f.broker.NewClient()
		require.NoError(t, f.connect(t, c, "agent", willMessage()))
		var wills []envproto.Message
		_, err := f.broker.Subscribe(t.Context(), testStatus, func(m envproto.Message) { wills = append(wills, m) })
		require.NoError(t, err)

		require.NoError(t, c.Close())
		assert.True(t, isClosed(c.Done()))
		assert.Empty(t, wills)
		assert.Empty(t, f.broker.Clients())
	})
	t.Run("does nothing on a client that isn't connected", func(t *testing.T) {
		assert.NoError(t, NewBroker().NewClient().Close())
	})
}
