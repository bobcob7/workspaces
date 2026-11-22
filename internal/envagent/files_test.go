package envagent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// newTestKey returns a fresh P-256 key.
func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

// signCertificate issues the certificate template for pub, signed by parent's key.
func signCertificate(t *testing.T, template, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, signer)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

// newTestCA returns a self-signed CA certificate and its key.
func newTestCA(t *testing.T, serial int64) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newTestKey(t)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	return signCertificate(t, template, template, &key.PublicKey, key), key
}

// newTestLeaf returns a certificate for pub, for cn, signed by parent and usable for usage.
func newTestLeaf(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, pub *ecdsa.PublicKey, serial int64, cn string, usage x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	return signCertificate(t, template, parent, pub, parentKey)
}

// testIdentity returns an identity for env-0a1b2c3d, issued by a test CA.
func testIdentity(t *testing.T) Identity {
	t.Helper()
	ca, caKey := newTestCA(t, 1)
	key := newTestKey(t)
	cert := newTestLeaf(t, ca, caKey, &key.PublicKey, 2, "env-0a1b2c3d", x509.ExtKeyUsageClientAuth)
	return Identity{
		ID:         "env-0a1b2c3d",
		Name:       "linux-server",
		Broker:     "broker.example:8883",
		ServerName: "broker.example",
		EnrolledAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC),
		Key:        key,
		Cert:       cert,
		Roots:      []*x509.Certificate{ca},
	}
}

// assertRemoved checks none of the enrolled files is left in files' directory.
func assertRemoved(t *testing.T, files *Files) {
	t.Helper()
	for _, name := range []string{agentFileName, keyFileName, certFileName, rootsFileName} {
		_, err := os.Stat(files.path(name))
		assert.ErrorIs(t, err, os.ErrNotExist, name)
	}
}

// assertNoTempFiles checks dir holds no temp file from an atomic write.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp-", "temp file left behind")
	}
}

// savedFiles returns the files of a directory holding testIdentity.
func savedFiles(t *testing.T) (*Files, Identity) {
	t.Helper()
	files := NewFiles(filepath.Join(t.TempDir(), "environment"))
	id := testIdentity(t)
	require.NoError(t, files.Save(&id))
	return files, id
}

func TestFiles_Load(t *testing.T) {
	t.Parallel()
	t.Run("reports ErrNotEnrolled in an empty directory", func(t *testing.T) {
		t.Parallel()
		_, err := NewFiles(t.TempDir()).Load()
		assert.ErrorIs(t, err, ErrNotEnrolled)
	})
	t.Run("round-trips a saved identity", func(t *testing.T) {
		t.Parallel()
		files, want := savedFiles(t)
		got, err := files.Load()
		require.NoError(t, err)
		assert.Equal(t, want.ID, got.ID)
		assert.Equal(t, want.Name, got.Name)
		assert.Equal(t, want.Broker, got.Broker)
		assert.Equal(t, want.ServerName, got.ServerName)
		assert.True(t, want.EnrolledAt.Equal(got.EnrolledAt))
		assert.True(t, want.Key.Equal(got.Key))
		assert.Equal(t, want.Cert.Raw, got.Cert.Raw)
		require.Len(t, got.Roots, 1)
		assert.Equal(t, want.Roots[0].Raw, got.Roots[0].Raw)
	})
	t.Run("refuses an agent.toml of another version", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		require.NoError(t, os.WriteFile(files.path(agentFileName), []byte("version = 2\n"), fileMode))
		_, err := files.Load()
		require.ErrorContains(t, err, "version 2")
	})
}

func TestFiles_Save(t *testing.T) {
	t.Parallel()
	t.Run("writes four files with mode 0600 in a 0700 directory", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		info, err := os.Stat(files.dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		for _, name := range []string{agentFileName, keyFileName, certFileName, rootsFileName} {
			info, err := os.Stat(files.path(name))
			require.NoError(t, err, name)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), name)
		}
	})
	t.Run("refuses a root count outside one or two", func(t *testing.T) {
		t.Parallel()
		files := NewFiles(t.TempDir())
		require.ErrorContains(t, files.SaveRoots(nil), "need 1 or 2 roots, got 0")
	})
}

func TestFiles_SaveCertificate(t *testing.T) {
	t.Parallel()
	t.Run("replaces the key and certificate", func(t *testing.T) {
		t.Parallel()
		files, id := savedFiles(t)
		ca, caKey := newTestCA(t, 3)
		key := newTestKey(t)
		cert := newTestLeaf(t, ca, caKey, &key.PublicKey, 4, id.ID, x509.ExtKeyUsageClientAuth)
		require.NoError(t, files.SaveCertificate(key, cert))
		got, err := files.Load()
		require.NoError(t, err)
		assert.True(t, key.Equal(got.Key))
		assert.Equal(t, cert.Raw, got.Cert.Raw)
	})
	t.Run("leaves no temp files behind", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		ca, caKey := newTestCA(t, 3)
		key := newTestKey(t)
		cert := newTestLeaf(t, ca, caKey, &key.PublicKey, 4, "env-0a1b2c3d", x509.ExtKeyUsageClientAuth)
		require.NoError(t, files.SaveCertificate(key, cert))
		assertNoTempFiles(t, files.dir)
	})
}

func TestFiles_Remove(t *testing.T) {
	t.Parallel()
	t.Run("deletes the files and returns the identity they held", func(t *testing.T) {
		t.Parallel()
		files, want := savedFiles(t)
		got, err := files.Remove()
		require.NoError(t, err)
		assert.Equal(t, want.ID, got.ID)
		_, err = files.Load()
		assert.ErrorIs(t, err, ErrNotEnrolled)
	})
	t.Run("reports ErrNotEnrolled when nothing is there", func(t *testing.T) {
		t.Parallel()
		_, err := NewFiles(t.TempDir()).Remove()
		assert.ErrorIs(t, err, ErrNotEnrolled)
	})
	t.Run("deletes every file when key.pem is corrupt", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		require.NoError(t, os.WriteFile(files.path(keyFileName), []byte("not a key"), fileMode))
		_, err := files.Remove()
		require.ErrorContains(t, err, "unreadable")
		assertRemoved(t, files)
	})
	t.Run("deletes every file when agent.toml is corrupt", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		require.NoError(t, os.WriteFile(files.path(agentFileName), []byte("version = [\n"), fileMode))
		_, err := files.Remove()
		require.ErrorContains(t, err, "unreadable")
		assertRemoved(t, files)
	})
	t.Run("deletes the other files when ca.pem is missing", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		require.NoError(t, os.Remove(files.path(rootsFileName)))
		_, err := files.Remove()
		require.ErrorContains(t, err, "unreadable")
		assertRemoved(t, files)
	})
	t.Run("deletes stray files without agent.toml and returns no identity", func(t *testing.T) {
		t.Parallel()
		files, _ := savedFiles(t)
		require.NoError(t, os.Remove(files.path(agentFileName)))
		got, err := files.Remove()
		require.NoError(t, err)
		assert.Empty(t, got.ID)
		assertRemoved(t, files)
	})
}

func TestParseRoots(t *testing.T) {
	ca, caKey := newTestCA(t, 1)
	other, _ := newTestCA(t, 2)
	leafKey := newTestKey(t)
	leaf := newTestLeaf(t, ca, caKey, &leafKey.PublicKey, 5, "not-a-ca", x509.ExtKeyUsageClientAuth)
	t.Run("accepts one or two roots", func(t *testing.T) {
		one, err := parseRoots(encodeCert(ca))
		require.NoError(t, err)
		assert.Len(t, one, 1)
		two, err := parseRoots(append(encodeCert(ca), encodeCert(other)...))
		require.NoError(t, err)
		assert.Len(t, two, 2)
	})
	t.Run("refuses no roots and more than two", func(t *testing.T) {
		_, err := parseRoots(nil)
		require.ErrorContains(t, err, "need 1 or 2 roots, got 0")
		three := append(append(encodeCert(ca), encodeCert(other)...), encodeCert(ca)...)
		_, err = parseRoots(three)
		require.ErrorContains(t, err, "need 1 or 2 roots, got 3")
	})
	t.Run("refuses a certificate that isn't a CA", func(t *testing.T) {
		_, err := parseRoots(encodeCert(leaf))
		require.ErrorContains(t, err, "isn't a CA certificate")
	})
	t.Run("refuses a block that isn't a certificate", func(t *testing.T) {
		keyPEM, err := encodeKey(leafKey)
		require.NoError(t, err)
		_, err = parseRoots(keyPEM)
		require.ErrorContains(t, err, "isn't a certificate")
	})
}

func TestIdentity_TLSConfig(t *testing.T) {
	id := testIdentity(t)
	cfg := id.TLSConfig()
	assert.Equal(t, uint16(tls.VersionTLS13), cfg.MinVersion)
	assert.Equal(t, id.ServerName, cfg.ServerName)
	require.Len(t, cfg.Certificates, 1)
	assert.Equal(t, id.Cert, cfg.Certificates[0].Leaf)
	assert.NotNil(t, cfg.RootCAs)
}

func TestIdentity_Fingerprints(t *testing.T) {
	id := testIdentity(t)
	assert.Equal(t, []string{envproto.Fingerprint(id.Roots[0])}, id.Fingerprints())
	id.Roots = nil
	assert.Equal(t, []string{}, id.Fingerprints())
}
