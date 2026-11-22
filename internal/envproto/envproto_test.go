package envproto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testServerName = "envtest.example"

// testCA signs test certificates; its key never leaves the test.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) testCA {
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
	return testCA{cert: cert, key: key}
}

func (ca testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

// issue signs a certificate for cn with the given usage.
func (ca testCA) issue(t *testing.T, cn string, usage x509.ExtKeyUsage) tls.Certificate {
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
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// handshake runs a real TLS handshake over net.Pipe and returns the
// server side's connection state.
func handshake(t *testing.T, server, client *tls.Config) *tls.ConnectionState {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	t.Cleanup(func() {
		_ = srvConn.Close()
		_ = cliConn.Close()
	})
	srv := tls.Server(srvConn, server)
	cli := tls.Client(cliConn, client)
	done := make(chan error, 1)
	go func() { done <- cli.HandshakeContext(t.Context()) }()
	require.NoError(t, srv.HandshakeContext(t.Context()))
	require.NoError(t, <-done)
	state := srv.ConnectionState()
	return &state
}

func TestPeerFromState(t *testing.T) {
	ca := newTestCA(t)
	clientCert := ca.issue(t, "env-1a2b3c4d", x509.ExtKeyUsageClientAuth)
	serverCert := ca.issue(t, "server", x509.ExtKeyUsageServerAuth)

	t.Run("reads the verified leaf and its issuer", func(t *testing.T) {
		state := handshake(t,
			&tls.Config{
				Certificates: []tls.Certificate{serverCert},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    ca.pool(),
			},
			&tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      ca.pool(),
				ServerName:   testServerName,
			},
		)
		peer, err := PeerFromState(state)
		require.NoError(t, err)
		assert.Equal(t, "env-1a2b3c4d", peer.CN)
		assert.Equal(t, clientCert.Leaf.SerialNumber.Text(16), peer.Serial)
		assert.Equal(t, Fingerprint(ca.cert), peer.Issuer)
		assert.Empty(t, peer.ClientID)
	})
	t.Run("errors without a client certificate", func(t *testing.T) {
		state := handshake(t,
			&tls.Config{Certificates: []tls.Certificate{serverCert}},
			&tls.Config{RootCAs: ca.pool(), ServerName: testServerName},
		)
		_, err := PeerFromState(state)
		assert.ErrorIs(t, err, ErrNoVerifiedChain)
	})
	t.Run("errors on a nil state", func(t *testing.T) {
		_, err := PeerFromState(nil)
		assert.ErrorIs(t, err, ErrNoVerifiedChain)
	})
	t.Run("errors when the chain is only the leaf", func(t *testing.T) {
		state := &tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{clientCert.Leaf}},
		}
		_, err := PeerFromState(state)
		assert.ErrorIs(t, err, ErrNoVerifiedChain)
	})
}

func TestFingerprint(t *testing.T) {
	ca := newTestCA(t)

	t.Run("is 64 lowercase hex characters of the DER", func(t *testing.T) {
		sum := sha256.Sum256(ca.cert.Raw)
		assert.Equal(t, hex.EncodeToString(sum[:]), Fingerprint(ca.cert))
	})
	t.Run("differs between certificates", func(t *testing.T) {
		other := newTestCA(t)
		assert.NotEqual(t, Fingerprint(ca.cert), Fingerprint(other.cert))
	})
}
