package environment

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// bufferedConn is one end of an in-memory connection whose writes don't wait
// for the peer to read. net.Pipe deadlocks when a server's TLS alert crosses
// a client's handshake write. This mirrors envtest's memConn, which is
// unexported there.
type bufferedConn struct {
	in  *bufferedPipe
	out *bufferedPipe
}

type bufferedPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   bytes.Buffer
	closed bool
}

type bufferedAddr struct{}

func (bufferedAddr) Network() string { return "mem" }

func (bufferedAddr) String() string { return "environment-test" }

func newBufferedConnPair() (net.Conn, net.Conn) {
	ab, ba := newBufferedPipe(), newBufferedPipe()
	return &bufferedConn{in: ba, out: ab}, &bufferedConn{in: ab, out: ba}
}

func newBufferedPipe() *bufferedPipe {
	p := &bufferedPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.in.read(b) }

func (c *bufferedConn) Write(b []byte) (int, error) { return c.out.write(b) }

func (c *bufferedConn) Close() error {
	c.in.close()
	c.out.close()
	return nil
}

func (*bufferedConn) SetDeadline(time.Time) error      { return nil }
func (*bufferedConn) SetReadDeadline(time.Time) error  { return nil }
func (*bufferedConn) SetWriteDeadline(time.Time) error { return nil }
func (*bufferedConn) LocalAddr() net.Addr              { return bufferedAddr{} }
func (*bufferedConn) RemoteAddr() net.Addr             { return bufferedAddr{} }

func (p *bufferedPipe) read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.data.Len() == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.data.Len() == 0 {
		return 0, io.EOF
	}
	return p.data.Read(b)
}

func (p *bufferedPipe) write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, net.ErrClosed
	}
	n, err := p.data.Write(b)
	p.cond.Broadcast()
	return n, err
}

func (p *bufferedPipe) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
}

type handshakeResult struct {
	client    *tls.Conn
	clientErr error
	serverErr error
}

// handshake runs both ends of a TLS handshake over a buffered connection.
// The client finishes first in TLS 1.3, so a refused client certificate
// shows up as serverErr.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) handshakeResult {
	t.Helper()
	serverNet, clientNet := newBufferedConnPair()
	t.Cleanup(func() {
		_ = serverNet.Close()
		_ = clientNet.Close()
	})
	server := tls.Server(serverNet, serverCfg)
	client := tls.Client(clientNet, clientCfg)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.HandshakeContext(t.Context()) }()
	clientErr := client.HandshakeContext(t.Context())
	return handshakeResult{client: client, clientErr: clientErr, serverErr: <-serverDone}
}

func clientTLS(roots *x509.CertPool, cert *tls.Certificate) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "127.0.0.1"}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return cfg
}

// newTestState makes a CA and a listener certificate for 127.0.0.1 and
// returns what the listeners serve with.
func newTestState(t *testing.T, a *authority) *tlsState {
	t.Helper()
	tr, err := a.trust()
	require.NoError(t, err)
	server, err := a.serverCertificate("127.0.0.1")
	require.NoError(t, err)
	return &tlsState{trust: tr, server: server}
}

func TestBrokerTLS(t *testing.T) {
	t.Run("accepts a client certificate from the current CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		st := newTestState(t, a)
		client := newTestClient(t, a)
		res := handshake(t, brokerTLS(func() *tlsState { return st }), clientTLS(st.trust.pool(), &client))
		assert.NoError(t, res.clientErr)
		assert.NoError(t, res.serverErr)
	})
	t.Run("accepts a client certificate from the old CA while old.crt exists", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		oldClient := newTestClient(t, a)
		_, _, _, err = a.rotate()
		require.NoError(t, err)
		st := newTestState(t, a)
		res := handshake(t, brokerTLS(func() *tlsState { return st }), clientTLS(st.trust.pool(), &oldClient))
		assert.NoError(t, res.clientErr)
		assert.NoError(t, res.serverErr)
	})
	t.Run("accepts a client certificate from the new CA after a rotation", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, _, _, err = a.rotate()
		require.NoError(t, err)
		st := newTestState(t, a)
		client := newTestClient(t, a)
		res := handshake(t, brokerTLS(func() *tlsState { return st }), clientTLS(st.trust.pool(), &client))
		assert.NoError(t, res.clientErr)
		assert.NoError(t, res.serverErr)
	})
	t.Run("refuses a client certificate from a CA dropped after rotation", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		oldClient := newTestClient(t, a)
		_, _, _, err = a.rotate()
		require.NoError(t, err)
		_, err = a.dropOld()
		require.NoError(t, err)
		st := newTestState(t, a)
		res := handshake(t, brokerTLS(func() *tlsState { return st }), clientTLS(st.trust.pool(), &oldClient))
		assert.Error(t, res.serverErr)
	})
	t.Run("refuses a client without a certificate", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		st := newTestState(t, a)
		res := handshake(t, brokerTLS(func() *tlsState { return st }), clientTLS(st.trust.pool(), nil))
		assert.Error(t, res.serverErr)
	})
}

func TestEnrollTLS(t *testing.T) {
	t.Run("accepts a client without a certificate and presents the leaf and the CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		st := newTestState(t, a)
		res := handshake(t, enrollTLS(func() *tlsState { return st }), clientTLS(st.trust.pool(), nil))
		require.NoError(t, res.clientErr)
		assert.NoError(t, res.serverErr)
		peers := res.client.ConnectionState().PeerCertificates
		require.Len(t, peers, 2)
		assert.Equal(t, st.server.Certificate[0], peers[0].Raw)
		assert.Equal(t, st.server.Certificate[1], peers[1].Raw)
	})
}

func TestTrust_bundlePEM(t *testing.T) {
	t.Run("lists the current CA then the old one", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		previous, err := a.currentCert()
		require.NoError(t, err)
		current, _, _, err := a.rotate()
		require.NoError(t, err)
		tr, err := a.trust()
		require.NoError(t, err)
		assert.Equal(t, append(encodeCert(current), encodeCert(previous)...), tr.bundlePEM())
	})
}

func TestTrust_contains(t *testing.T) {
	t.Run("reports whether a fingerprint is trusted", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		tr, err := a.trust()
		require.NoError(t, err)
		assert.True(t, tr.contains(envproto.Fingerprint(tr.current)))
		assert.False(t, tr.contains("00"))
	})
}
