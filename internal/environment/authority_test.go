package environment

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// fixedClock returns a clock stuck at the current time. The certificates a
// test makes are then valid against the real clock that TLS checks.
func fixedClock() func() time.Time {
	now := time.Now().UTC().Truncate(time.Second)
	return func() time.Time { return now }
}

func newTestAuthority(t *testing.T, now func() time.Time) *authority {
	t.Helper()
	return newAuthority(filepath.Join(t.TempDir(), "ca"), now, rand.Reader)
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

func newTestCSR(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "ignored"},
	}, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: csrPEMType, Bytes: der})
}

// newTestClient issues a client certificate for env-1 from a's current CA.
func newTestClient(t *testing.T, a *authority) tls.Certificate {
	t.Helper()
	key := newTestKey(t)
	cert, err := a.signClient(newTestCSR(t, key), "env-1")
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, want, info.Mode().Perm(), path)
}

func mustCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	cert, err := readCert(path)
	require.NoError(t, err)
	return cert
}

func TestAuthority_exists(t *testing.T) {
	t.Run("reports false before ensure and true after", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		before, err := a.exists()
		require.NoError(t, err)
		_, err = a.ensure()
		require.NoError(t, err)
		after, err := a.exists()
		require.NoError(t, err)
		assert.False(t, before)
		assert.True(t, after)
	})
}

func TestAuthority_ensure(t *testing.T) {
	t.Run("creates the CA and listener files with their modes", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		created, err := a.ensure()
		require.NoError(t, err)
		require.True(t, created)
		_, err = a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		assertMode(t, a.dir, 0o700)
		assertMode(t, a.path(caKeyFile), 0o600)
		assertMode(t, a.path(caCertFile), 0o644)
		assertMode(t, a.path(serverKeyFile), 0o600)
		assertMode(t, a.path(serverCertFile), 0o644)
	})
	t.Run("tightens an existing CA directory to 0700", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		require.NoError(t, os.Chmod(a.dir, 0o755))
		_, err = a.ensure()
		require.NoError(t, err)
		assertMode(t, a.dir, 0o700)
	})
	t.Run("is a no-op the second time", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		before, err := os.ReadFile(a.path(caCertFile))
		require.NoError(t, err)
		created, err := a.ensure()
		require.NoError(t, err)
		after, err := os.ReadFile(a.path(caCertFile))
		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, before, after)
	})
	t.Run("finishes a rotation interrupted after ca.crt moved to old.crt", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		previous, err := a.currentCert()
		require.NoError(t, err)
		_, err = a.writeCA(pendingKeyFile, pendingCertFile)
		require.NoError(t, err)
		require.NoError(t, os.Rename(a.path(caCertFile), a.path(oldCertFile)))
		pending := mustCert(t, a.path(pendingCertFile))
		created, err := a.ensure()
		require.NoError(t, err)
		current := mustCert(t, a.path(caCertFile))
		newKey, err := readKey(a.path(caKeyFile))
		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, pending.Raw, current.Raw)
		assert.True(t, newKey.PublicKey.Equal(pending.PublicKey))
		assert.Equal(t, previous.Raw, mustCert(t, a.path(oldCertFile)).Raw)
		_, err = os.Stat(a.path(pendingCertFile))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("removes a stale pending key when there is no CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		require.NoError(t, os.MkdirAll(a.dir, 0o700))
		stale, err := encodeKey(newTestKey(t))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(a.path(pendingKeyFile), stale, 0o600))
		created, err := a.ensure()
		require.NoError(t, err)
		assert.True(t, created)
		_, err = os.Stat(a.path(pendingKeyFile))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("makes a CA valid for 365 days", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		ca, err := a.currentCert()
		require.NoError(t, err)
		assert.Equal(t, a.now().Add(-certBackdate).Unix(), ca.NotBefore.Unix())
		assert.Equal(t, a.now().Add(caLifetime).Unix(), ca.NotAfter.Unix())
		assert.True(t, ca.IsCA)
		assert.True(t, ca.MaxPathLenZero)
		assert.Equal(t, x509.KeyUsageCertSign|x509.KeyUsageCRLSign, ca.KeyUsage)
	})
}

func TestAuthority_installPending(t *testing.T) {
	t.Run("refuses a pending certificate for another key and moves nothing", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, err = a.writeCA(pendingKeyFile, pendingCertFile)
		require.NoError(t, err)
		other, err := encodeKey(newTestKey(t))
		require.NoError(t, err)
		require.NoError(t, writeFileAtomic(a.path(pendingKeyFile), other, 0o600))
		before, err := os.ReadFile(a.path(caCertFile))
		require.NoError(t, err)
		err = a.installPending()
		require.ErrorIs(t, err, errPendingMismatch)
		after, err := os.ReadFile(a.path(caCertFile))
		require.NoError(t, err)
		assert.Equal(t, before, after)
		_, err = os.Stat(a.path(pendingCertFile))
		assert.NoError(t, err)
		_, err = os.Stat(a.path(pendingKeyFile))
		assert.NoError(t, err)
	})
}

func TestAuthority_trust(t *testing.T) {
	t.Run("returns errNoCA without a CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.trust()
		assert.ErrorIs(t, err, errNoCA)
	})
	t.Run("trusts only the current CA before a rotation", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		tr, err := a.trust()
		require.NoError(t, err)
		ca, err := a.currentCert()
		require.NoError(t, err)
		assert.Nil(t, tr.old)
		assert.Equal(t, []string{envproto.Fingerprint(ca)}, tr.fingerprints())
	})
}

func TestAuthority_rotate(t *testing.T) {
	t.Run("returns errNoCA without a CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, _, _, err := a.rotate()
		assert.ErrorIs(t, err, errNoCA)
	})
	t.Run("keeps the previous CA as old and replaces its key", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		previous, err := a.currentCert()
		require.NoError(t, err)
		previousKey, err := readKey(a.path(caKeyFile))
		require.NoError(t, err)
		current, old, dropped, err := a.rotate()
		require.NoError(t, err)
		newKey, err := readKey(a.path(caKeyFile))
		require.NoError(t, err)
		assert.Nil(t, dropped)
		assert.Equal(t, previous.Raw, old.Raw)
		assert.Equal(t, current.Raw, mustCert(t, a.path(caCertFile)).Raw)
		assert.True(t, newKey.PublicKey.Equal(current.PublicKey))
		assert.False(t, previousKey.Equal(newKey))
		_, err = os.Stat(a.path(pendingKeyFile))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("removes the listeners' certificate", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, err = a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		_, _, _, err = a.rotate()
		require.NoError(t, err)
		_, err = os.Stat(a.path(serverCertFile))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("keeps at most two roots when rotated twice", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		original, err := a.currentCert()
		require.NoError(t, err)
		_, _, _, err = a.rotate()
		require.NoError(t, err)
		current, _, dropped, err := a.rotate()
		require.NoError(t, err)
		tr, err := a.trust()
		require.NoError(t, err)
		newestKey, err := readKey(a.path(caKeyFile))
		require.NoError(t, err)
		assert.Equal(t, envproto.Fingerprint(original), envproto.Fingerprint(dropped))
		assert.Len(t, tr.fingerprints(), 2)
		assert.Equal(t, envproto.Fingerprint(current), tr.fingerprints()[0])
		assert.True(t, newestKey.PublicKey.Equal(current.PublicKey))
	})
}

func TestAuthority_dropOld(t *testing.T) {
	t.Run("returns nil when there is no old.crt", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		dropped, err := a.dropOld()
		require.NoError(t, err)
		assert.Nil(t, dropped)
	})
	t.Run("removes old.crt and returns its certificate once", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		previous, err := a.currentCert()
		require.NoError(t, err)
		_, _, _, err = a.rotate()
		require.NoError(t, err)
		dropped, err := a.dropOld()
		require.NoError(t, err)
		again, err := a.dropOld()
		require.NoError(t, err)
		assert.Equal(t, previous.Raw, dropped.Raw)
		assert.Nil(t, again)
	})
}

func TestAuthority_signClient(t *testing.T) {
	t.Run("issues the given CN whatever the request says", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		cert, err := a.signClient(newTestCSR(t, newTestKey(t)), "env-2")
		require.NoError(t, err)
		ca, err := a.currentCert()
		require.NoError(t, err)
		assert.Equal(t, "env-2", cert.Subject.CommonName)
		assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, cert.ExtKeyUsage)
		assert.Equal(t, x509.KeyUsageDigitalSignature, cert.KeyUsage)
		assert.NoError(t, cert.CheckSignatureFrom(ca))
	})
	t.Run("refuses an empty common name", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, err = a.signClient(newTestCSR(t, newTestKey(t)), "")
		assert.ErrorIs(t, err, errEmptyCN)
	})
	t.Run("refuses an RSA request", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		_, err = a.signClient(newTestCSR(t, rsaKey), "env-1")
		assert.ErrorIs(t, err, errCSRInvalid)
	})
	t.Run("refuses a P-384 request", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		require.NoError(t, err)
		_, err = a.signClient(newTestCSR(t, key), "env-1")
		assert.ErrorIs(t, err, errCSRInvalid)
	})
	t.Run("refuses a request whose signature is wrong", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, err = a.signClient(tamperedCSR(t, newTestCSR(t, newTestKey(t))), "env-1")
		assert.ErrorIs(t, err, errCSRInvalid)
	})
	t.Run("refuses input that is not a certificate request", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, err = a.signClient([]byte("not pem"), "env-1")
		assert.ErrorIs(t, err, errCSRInvalid)
	})
	t.Run("returns errNoCA without a CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.signClient(newTestCSR(t, newTestKey(t)), "env-1")
		assert.ErrorIs(t, err, errNoCA)
	})
}

func tamperedCSR(t *testing.T, csrPEM []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(csrPEM)
	require.NotNil(t, block)
	block.Bytes[len(block.Bytes)-1] ^= 0xff
	return pem.EncodeToMemory(block)
}

func TestAuthority_serverCertificate(t *testing.T) {
	t.Run("makes an IP certificate signed by the CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		cert, err := a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		require.NoError(t, err)
		ca, err := a.currentCert()
		require.NoError(t, err)
		assert.NoError(t, leaf.VerifyHostname("127.0.0.1"))
		require.Len(t, leaf.IPAddresses, 1)
		assert.True(t, leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")))
		assert.Equal(t, [][]byte{leaf.Raw, ca.Raw}, cert.Certificate)
	})
	t.Run("makes a DNS certificate for a host name", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		cert, err := a.serverCertificate("vega.example")
		require.NoError(t, err)
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		require.NoError(t, err)
		assert.Equal(t, []string{"vega.example"}, leaf.DNSNames)
		assert.Empty(t, leaf.IPAddresses)
	})
	t.Run("reuses the certificate while it is valid", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		first, err := a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		second, err := a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		assert.Equal(t, first.Certificate, second.Certificate)
	})
	t.Run("makes a new certificate within 30 days of expiry", func(t *testing.T) {
		t.Parallel()
		at := time.Now().UTC().Truncate(time.Second)
		a := newAuthority(t.TempDir(), func() time.Time { return at }, rand.Reader)
		_, err := a.ensure()
		require.NoError(t, err)
		first, err := a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		ca, err := a.currentCert()
		require.NoError(t, err)
		at = ca.NotAfter.Add(-29 * 24 * time.Hour)
		second, err := a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		assert.NotEqual(t, first.Certificate[0], second.Certificate[0])
	})
	t.Run("is issued by the new CA after a rotation", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.ensure()
		require.NoError(t, err)
		_, err = a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		current, _, _, err := a.rotate()
		require.NoError(t, err)
		cert, err := a.serverCertificate("127.0.0.1")
		require.NoError(t, err)
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		require.NoError(t, err)
		assert.NoError(t, leaf.CheckSignatureFrom(current))
	})
	t.Run("returns errNoCA without a CA", func(t *testing.T) {
		t.Parallel()
		a := newTestAuthority(t, fixedClock())
		_, err := a.serverCertificate("127.0.0.1")
		assert.ErrorIs(t, err, errNoCA)
	})
}

func TestCaExpiryWarning(t *testing.T) {
	expiry := time.Date(2027, 11, 22, 0, 0, 0, 0, time.UTC)
	ca := &x509.Certificate{NotAfter: expiry}
	t.Run("is empty 31 days before expiry with a 30-day window", func(t *testing.T) {
		assert.Empty(t, caExpiryWarning(ca, expiry.Add(-31*24*time.Hour), 30))
	})
	t.Run("warns at exactly 30 days before expiry", func(t *testing.T) {
		warning := caExpiryWarning(ca, expiry.Add(-30*24*time.Hour), 30)
		assert.Equal(t, "the environments CA expires on 2027-11-22, within 30 days; run ws env ca rotate", warning)
	})
	t.Run("warns 29 days before expiry", func(t *testing.T) {
		assert.NotEmpty(t, caExpiryWarning(ca, expiry.Add(-29*24*time.Hour), 30))
	})
	t.Run("follows a changed window", func(t *testing.T) {
		at := expiry.Add(-40 * 24 * time.Hour)
		assert.Empty(t, caExpiryWarning(ca, at, 30))
		assert.NotEmpty(t, caExpiryWarning(ca, at, 45))
	})
}
