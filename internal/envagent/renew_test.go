package envagent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func TestRenewDue(t *testing.T) {
	t.Parallel()
	const day = 24 * time.Hour
	notAfter := time.Date(2030, 1, 31, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		days int
		want bool
	}{
		{name: "exactly 30 days before expiry", now: notAfter.Add(-30 * day), days: 30, want: true},
		{name: "31 days before expiry", now: notAfter.Add(-31 * day), days: 30, want: false},
		{name: "29 days before expiry", now: notAfter.Add(-29 * day), days: 30, want: true},
		{name: "at expiry", now: notAfter, days: 30, want: false},
		{name: "after expiry", now: notAfter.Add(time.Second), days: 30, want: false},
		{name: "45 days with a changed setting, 40 days before", now: notAfter.Add(-40 * day), days: 45, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, renewDue(notAfter, tc.now, tc.days))
		})
	}
}

func TestAgent_renew(t *testing.T) {
	t.Parallel()
	t.Run("replaces the certificate and key for the same environment", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		before := fx.id
		opts := testOptions()
		opts.Now = func() time.Time { return before.Cert.NotAfter.AddDate(0, 0, -10) }
		runAgent(t, newFixtureAgent(fx, opts, "[]"))
		require.Eventually(t, func() bool {
			id, err := fx.files.Load()
			return err == nil && id.Cert.SerialNumber.Cmp(before.Cert.SerialNumber) != 0
		}, 5*time.Second, 10*time.Millisecond)
		after, err := fx.files.Load()
		require.NoError(t, err)
		assert.Equal(t, fx.id.ID, after.Cert.Subject.CommonName)
		assert.NotEqual(t, privateKeyDER(t, before.Key), privateKeyDER(t, after.Key))
	})
	t.Run("never sends a private key on the broker", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		before := fx.id
		seen := &payloadCapture{}
		unsubscribe, err := fx.broker.Subscribe(t.Context(), "ws/v1/#", func(msg envproto.Message) {
			seen.record(msg.Payload)
		})
		require.NoError(t, err)
		t.Cleanup(unsubscribe)
		opts := testOptions()
		opts.Now = func() time.Time { return before.Cert.NotAfter.AddDate(0, 0, -10) }
		runAgent(t, newFixtureAgent(fx, opts, "[]"))
		require.Eventually(t, func() bool {
			id, loadErr := fx.files.Load()
			return loadErr == nil && id.Cert.SerialNumber.Cmp(before.Cert.SerialNumber) != 0
		}, 5*time.Second, 10*time.Millisecond)
		after, err := fx.files.Load()
		require.NoError(t, err)
		payloads := seen.all()
		require.NotEmpty(t, payloads)
		for _, payload := range payloads {
			assert.NotContains(t, string(payload), "PRIVATE KEY")
			assert.False(t, bytes.Contains(payload, privateKeyDER(t, before.Key)), "the old key")
			assert.False(t, bytes.Contains(payload, privateKeyDER(t, after.Key)), "the new key")
		}
	})
	t.Run("renews under the new CA after a rotation", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		before := fx.id
		opts := testOptions()
		opts.Now = func() time.Time { return before.Cert.NotAfter.AddDate(0, 0, -10) }
		runAgent(t, newFixtureAgent(fx, opts, "[]"))
		require.Eventually(t, func() bool { return fx.hello() != nil }, 5*time.Second, 10*time.Millisecond)
		_, err := fx.store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, fx.ctrl.Reload(t.Context()))
		require.Eventually(t, func() bool {
			roots, err := fx.files.loadRoots()
			if err != nil || len(roots) != 2 {
				return false
			}
			id, err := fx.files.Load()
			if err != nil {
				return false
			}
			return verifiesUnder(id.Cert, roots[0]) == nil
		}, 5*time.Second, 10*time.Millisecond)
	})
}

// payloadCapture records the payloads of the messages a subscription sees.
type payloadCapture struct {
	mu       sync.Mutex
	payloads [][]byte
}

// record appends payload.
func (c *payloadCapture) record(payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payloads = append(c.payloads, payload)
}

// privateKeyDER is key as PKCS#8 DER, the form a leaked key would take on the wire.
func privateKeyDER(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return der
}

// all returns the payloads recorded so far.
func (c *payloadCapture) all() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.payloads...)
}

// verifiesUnder is nil when cert chains to root as a client certificate.
func verifiesUnder(cert, root *x509.Certificate) error {
	pool := x509.NewCertPool()
	pool.AddCert(root)
	_, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err
}
