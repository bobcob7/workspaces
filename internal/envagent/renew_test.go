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
		seen := &payloadCapture{}
		unsubscribe, err := fx.broker.Subscribe(t.Context(), "ws/v1/#", func(msg envproto.Message) {
			seen.record(msg.Payload)
		})
		require.NoError(t, err)
		t.Cleanup(unsubscribe)
		opts := testOptions()
		opts.Now = func() time.Time { return fx.id.Cert.NotAfter.AddDate(0, 0, -10) }
		ticks := manualRenewal(&opts)
		runAgent(t, newFixtureAgent(fx, opts, "[]"))
		awaitOnline(t, fx)
		keys := []*ecdsa.PrivateKey{fx.id.Key}
		serial := fx.id.Cert.SerialNumber
		for range 3 {
			ticks <- time.Now()
			require.Eventually(t, func() bool {
				id, loadErr := fx.files.Load()
				return loadErr == nil && id.Cert.SerialNumber.Cmp(serial) != 0
			}, 5*time.Second, 10*time.Millisecond)
			id, loadErr := fx.files.Load()
			require.NoError(t, loadErr)
			keys = append(keys, id.Key)
			serial = id.Cert.SerialNumber
			require.Eventually(t, func() bool {
				hello := fx.hello()
				return hello != nil && hello.CertSerial == serial.Text(16)
			}, 5*time.Second, 10*time.Millisecond)
		}
		payloads := seen.all()
		require.NotEmpty(t, payloads)
		for _, payload := range payloads {
			assert.NotContains(t, string(payload), "PRIVATE KEY")
			for _, key := range keys {
				for name, secret := range privateKeyForms(t, key) {
					assert.False(t, bytes.Contains(payload, secret), "key as %s", name)
				}
			}
		}
	})
	t.Run("renews under the new CA after a rotation", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		opts := testOptions()
		opts.Now = func() time.Time { return fx.id.Cert.NotAfter.AddDate(0, 0, -10) }
		ticks := manualRenewal(&opts)
		runAgent(t, newFixtureAgent(fx, opts, "[]"))
		awaitOnline(t, fx)
		rotation, err := fx.store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, fx.ctrl.Reload(t.Context()))
		require.Eventually(t, func() bool {
			roots, loadErr := fx.files.loadRoots()
			return loadErr == nil && len(roots) == 2
		}, 5*time.Second, 10*time.Millisecond)
		beforeRenewal, err := fx.files.Load()
		require.NoError(t, err)
		ticks <- time.Now()
		require.Eventually(t, func() bool {
			id, loadErr := fx.files.Load()
			return loadErr == nil && id.Cert.SerialNumber.Cmp(beforeRenewal.Cert.SerialNumber) != 0
		}, 5*time.Second, 10*time.Millisecond)
		id, err := fx.files.Load()
		require.NoError(t, err)
		roots, err := fx.files.loadRoots()
		require.NoError(t, err)
		require.Equal(t, rotation.Current.Fingerprint, envproto.Fingerprint(roots[0]))
		require.NoError(t, verifiesUnder(id.Cert, roots[0]))
		assert.Equal(t, fx.id.ID, id.Cert.Subject.CommonName)
		assert.Error(t, verifiesUnder(id.Cert, roots[1]), "signed by the old CA")
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

// privateKeyDER is key as PKCS#8 DER.
func privateKeyDER(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return der
}

// privateKeyForms are the encodings a leaked key could take on the wire: PKCS#8 DER, SEC1 DER and the raw scalar.
func privateKeyForms(t *testing.T, key *ecdsa.PrivateKey) map[string][]byte {
	t.Helper()
	sec1, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	scalar, err := key.Bytes()
	require.NoError(t, err)
	return map[string][]byte{
		"PKCS#8": privateKeyDER(t, key),
		"SEC1":   sec1,
		"scalar": scalar,
	}
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
