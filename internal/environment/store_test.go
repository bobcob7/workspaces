package environment

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const testBroker = "broker.example:8883"

func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	return NewStore(filepath.Join(root, "environments"), filepath.Join(root, "state"), fixedClock(), rand.Reader)
}

// addEnvironment adds a pending environment called name and returns its record and code.
func addEnvironment(t *testing.T, store *Store, name string) (Record, string) {
	t.Helper()
	rec, code, _, err := store.Add(t.Context(), name, testBroker)
	require.NoError(t, err)
	return rec, code
}

// enrollWith exchanges code for a certificate for a fresh key.
func enrollWith(t *testing.T, store *Store, code string) (envproto.EnrollResponse, error) {
	t.Helper()
	parsed, err := envproto.ParseCode(code)
	require.NoError(t, err)
	return store.Enroll(t.Context(), parsed.Secret, newTestCSR(t, newTestKey(t)), parsed.Host, "public.example")
}

// enrolledEnvironment adds an environment called linux-server and enrolls it.
func enrolledEnvironment(t *testing.T, store *Store) Record {
	t.Helper()
	_, code := addEnvironment(t, store, "linux-server")
	_, err := enrollWith(t, store, code)
	require.NoError(t, err)
	rec, err := store.Get("linux-server")
	require.NoError(t, err)
	return rec
}

// secretOf returns the secret in an enrollment code.
func secretOf(t *testing.T, code string) string {
	t.Helper()
	parsed, err := envproto.ParseCode(code)
	require.NoError(t, err)
	return parsed.Secret
}

// parseCertPEM decodes a PEM certificate.
func parseCertPEM(t *testing.T, data string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(data))
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}

func TestStore_Add(t *testing.T) {
	t.Run("makes a pending record and a code that carries the CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		rec, code, caCreated, err := store.Add(t.Context(), "linux-server", testBroker)
		require.NoError(t, err)
		parsed, err := envproto.ParseCode(code)
		require.NoError(t, err)
		ca, err := store.currentCert()
		require.NoError(t, err)
		assert.True(t, caCreated)
		assert.Equal(t, StatePending, rec.State)
		assert.Equal(t, "linux-server", rec.Name)
		assert.Regexp(t, `^env-[0-9a-f]{8}$`, rec.ID)
		assert.Equal(t, testBroker, parsed.Host)
		assert.Equal(t, envproto.Fingerprint(ca), parsed.Fingerprint)
		assert.Equal(t, envproto.HashSecret(parsed.Secret), rec.SecretHash)
	})
	t.Run("reports no new CA when one exists", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "first")
		_, _, caCreated, err := store.Add(t.Context(), "second", testBroker)
		require.NoError(t, err)
		assert.False(t, caCreated)
	})
	t.Run("refuses invalid names", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		for _, name := range []string{"", "local", "Local", "-x", "1abc", "a_b", strings.Repeat("a", 33)} {
			_, _, _, err := store.Add(t.Context(), name, testBroker)
			require.ErrorIs(t, err, ErrNameInvalid, name)
		}
	})
	t.Run("accepts a 32-character name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, _, _, err := store.Add(t.Context(), strings.Repeat("a", 32), testBroker)
		assert.NoError(t, err)
	})
	t.Run("refuses a taken name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		_, _, _, err := store.Add(t.Context(), "linux-server", testBroker)
		require.ErrorIs(t, err, ErrExists)
	})
}

func TestStore_newID(t *testing.T) {
	t.Run("skips an id that a record already uses", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		existing, _ := addEnvironment(t, store, "first")
		raw, err := hex.DecodeString(strings.TrimPrefix(existing.ID, "env-"))
		require.NoError(t, err)
		store.random = io.MultiReader(bytes.NewReader(raw), bytes.NewReader([]byte{1, 2, 3, 4}))
		id, err := store.newID()
		require.NoError(t, err)
		assert.Equal(t, "env-01020304", id)
	})
}

func TestStore_Get(t *testing.T) {
	t.Run("returns the environment by name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		added, _ := addEnvironment(t, store, "linux-server")
		rec, err := store.Get("linux-server")
		require.NoError(t, err)
		assert.Equal(t, added, rec)
	})
	t.Run("is ErrUnknown for a missing name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, err := store.Get("missing")
		require.ErrorIs(t, err, ErrUnknown)
	})
}

func TestStore_ByID(t *testing.T) {
	t.Run("returns the environment by id", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		added, _ := addEnvironment(t, store, "linux-server")
		rec, err := store.ByID(added.ID)
		require.NoError(t, err)
		assert.Equal(t, added, rec)
	})
	t.Run("is ErrUnknown for a missing id", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, err := store.ByID("env-00000000")
		require.ErrorIs(t, err, ErrUnknown)
	})
}

func TestStore_List(t *testing.T) {
	t.Run("is an empty list when there are none", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		recs, err := store.List()
		require.NoError(t, err)
		assert.NotNil(t, recs)
		assert.Empty(t, recs)
	})
	t.Run("sorts by name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "b-box")
		addEnvironment(t, store, "a-box")
		recs, err := store.List()
		require.NoError(t, err)
		names := []string{}
		for _, rec := range recs {
			names = append(names, rec.Name)
		}
		assert.Equal(t, []string{"a-box", "b-box"}, names)
	})
}

func TestStore_Rotate(t *testing.T) {
	t.Run("on an enrolled environment revokes its serial and swaps the code", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		before := enrolledEnvironment(t, store)
		_, newCode, revoked, err := store.Rotate(t.Context(), "linux-server", testBroker)
		require.NoError(t, err)
		assert.Equal(t, []string{before.CertSerial}, revoked)
		revokedSet, err := store.Revoked()
		require.NoError(t, err)
		assert.True(t, revokedSet[before.CertSerial])
		rec, err := store.Get("linux-server")
		require.NoError(t, err)
		assert.Equal(t, StatePending, rec.State)
		assert.Empty(t, rec.CertSerial)
		assert.Empty(t, rec.EnrolledAt)
		_, err = enrollWith(t, store, newCode)
		assert.NoError(t, err)
	})
	t.Run("on an enrolled environment the old code stops working", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, oldCode := addEnvironment(t, store, "linux-server")
		_, err := enrollWith(t, store, oldCode)
		require.NoError(t, err)
		_, _, _, err = store.Rotate(t.Context(), "linux-server", testBroker)
		require.NoError(t, err)
		_, err = enrollWith(t, store, oldCode)
		require.ErrorIs(t, err, ErrEnrollmentFailed)
	})
	t.Run("on a pending environment the old code fails and nothing is revoked", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, oldCode := addEnvironment(t, store, "linux-server")
		_, _, revoked, err := store.Rotate(t.Context(), "linux-server", testBroker)
		require.NoError(t, err)
		assert.Equal(t, []string{}, revoked)
		_, err = enrollWith(t, store, oldCode)
		require.ErrorIs(t, err, ErrEnrollmentFailed)
	})
	t.Run("is ErrUnknown for a missing name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		_, _, _, err := store.Rotate(t.Context(), "missing", testBroker)
		require.ErrorIs(t, err, ErrUnknown)
	})
}

func TestStore_Remove(t *testing.T) {
	t.Run("revokes, deletes the record and the snapshot directory", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolled := enrolledEnvironment(t, store)
		require.NoError(t, store.WriteSnapshot("linux-server", []byte("{}")))
		rec, revoked, err := store.Remove(t.Context(), "linux-server")
		require.NoError(t, err)
		assert.Equal(t, enrolled.ID, rec.ID)
		assert.Equal(t, []string{enrolled.CertSerial}, revoked)
		_, err = store.Get("linux-server")
		require.ErrorIs(t, err, ErrUnknown)
		_, err = os.Stat(store.snapshotDir("linux-server"))
		require.ErrorIs(t, err, fs.ErrNotExist)
		revokedSet, err := store.Revoked()
		require.NoError(t, err)
		assert.True(t, revokedSet[enrolled.CertSerial])
	})
	t.Run("removes a pending environment without revoking", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		_, revoked, err := store.Remove(t.Context(), "linux-server")
		require.NoError(t, err)
		assert.Equal(t, []string{}, revoked)
	})
	t.Run("is ErrUnknown for a missing name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, _, err := store.Remove(t.Context(), "missing")
		require.ErrorIs(t, err, ErrUnknown)
	})
}

func TestStore_Enroll(t *testing.T) {
	t.Run("signs a pending code's CSR with CN = id and records it enrolled", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		added, code := addEnvironment(t, store, "linux-server")
		resp, err := enrollWith(t, store, code)
		require.NoError(t, err)
		cert := parseCertPEM(t, resp.Cert)
		assert.Equal(t, added.ID, cert.Subject.CommonName)
		assert.Equal(t, added.ID, resp.ID)
		assert.Equal(t, "linux-server", resp.Name)
		assert.Equal(t, testBroker, resp.Broker)
		assert.Equal(t, "public.example", resp.ServerName)
		assert.NotEmpty(t, resp.CABundle)
		rec, err := store.Get("linux-server")
		require.NoError(t, err)
		assert.Equal(t, StateEnrolled, rec.State)
		assert.Equal(t, cert.SerialNumber.Text(16), rec.CertSerial)
		assert.NotEmpty(t, rec.EnrolledAt)
	})
	t.Run("a code works once", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, code := addEnvironment(t, store, "linux-server")
		_, err := enrollWith(t, store, code)
		require.NoError(t, err)
		_, err = enrollWith(t, store, code)
		require.ErrorIs(t, err, ErrEnrollmentUsed)
	})
	t.Run("a wrong secret is ErrEnrollmentFailed", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		_, err := store.Enroll(t.Context(), "wrong", newTestCSR(t, newTestKey(t)), testBroker, "public.example")
		require.ErrorIs(t, err, ErrEnrollmentFailed)
	})
	t.Run("a bad CSR is ErrEnrollmentFailed and the code stays usable", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, code := addEnvironment(t, store, "linux-server")
		secret := secretOf(t, code)
		_, err := store.Enroll(t.Context(), secret, []byte("not a csr"), testBroker, "public.example")
		require.ErrorIs(t, err, ErrEnrollmentFailed)
		_, err = enrollWith(t, store, code)
		assert.NoError(t, err)
	})
	t.Run("two concurrent enrollments with one code: exactly one succeeds", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, code := addEnvironment(t, store, "linux-server")
		secret := secretOf(t, code)
		csrs := [][]byte{newTestCSR(t, newTestKey(t)), newTestCSR(t, newTestKey(t))}
		errs := make([]error, len(csrs))
		var wg sync.WaitGroup
		for i := range csrs {
			wg.Go(func() {
				_, errs[i] = store.Enroll(t.Context(), secret, csrs[i], testBroker, "public.example")
			})
		}
		wg.Wait()
		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
			}
		}
		assert.Equal(t, 1, succeeded)
	})
}

func TestStore_Renew(t *testing.T) {
	t.Run("keeps CN = id and records the new certificate", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolled := enrolledEnvironment(t, store)
		result, err := store.Renew(t.Context(), enrolled.ID, newTestCSR(t, newTestKey(t)))
		require.NoError(t, err)
		cert := parseCertPEM(t, result.Cert)
		assert.Equal(t, enrolled.ID, cert.Subject.CommonName)
		rec, err := store.ByID(enrolled.ID)
		require.NoError(t, err)
		assert.Equal(t, cert.SerialNumber.Text(16), rec.CertSerial)
		assert.Equal(t, enrolled.EnrolledAt, rec.EnrolledAt)
	})
	t.Run("the renewed certificate is issued by the new CA after RotateCA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolled := enrolledEnvironment(t, store)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		current, err := store.currentCert()
		require.NoError(t, err)
		result, err := store.Renew(t.Context(), enrolled.ID, newTestCSR(t, newTestKey(t)))
		require.NoError(t, err)
		assert.NoError(t, parseCertPEM(t, result.Cert).CheckSignatureFrom(current))
		assert.Contains(t, result.CABundle, encodeCertString(current))
	})
	t.Run("is ErrUnknown for a pending or missing id", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		pending, _ := addEnvironment(t, store, "linux-server")
		_, err := store.Renew(t.Context(), pending.ID, newTestCSR(t, newTestKey(t)))
		require.ErrorIs(t, err, ErrUnknown)
		_, err = store.Renew(t.Context(), "env-00000000", newTestCSR(t, newTestKey(t)))
		require.ErrorIs(t, err, ErrUnknown)
	})
	t.Run("a bad CSR is ErrEnrollmentFailed", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrolled := enrolledEnvironment(t, store)
		_, err := store.Renew(t.Context(), enrolled.ID, []byte("not a csr"))
		require.ErrorIs(t, err, ErrEnrollmentFailed)
	})
}

func TestStore_WriteSnapshot(t *testing.T) {
	t.Run("writes the snapshot and reads it back", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.WriteSnapshot("linux-server", []byte(`{"workspaces":[]}`)))
		data, err := store.ReadSnapshot("linux-server")
		require.NoError(t, err)
		assert.JSONEq(t, `{"workspaces":[]}`, string(data))
		assertMode(t, filepath.Join(store.snapshotDir("linux-server"), snapshotFile), 0o600)
		assertMode(t, store.snapshotDir("linux-server"), 0o700)
	})
	t.Run("ReadSnapshot is fs.ErrNotExist when there is none", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, err := store.ReadSnapshot("linux-server")
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("refuses an invalid name", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.ErrorIs(t, store.WriteSnapshot("../escape", []byte("{}")), ErrNameInvalid)
		_, err := store.ReadSnapshot("../escape")
		require.ErrorIs(t, err, ErrNameInvalid)
	})
}

func TestStore_caExists(t *testing.T) {
	t.Run("is false before any environment and true after the first add", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		before, err := store.caExists()
		require.NoError(t, err)
		addEnvironment(t, store, "linux-server")
		after, err := store.caExists()
		require.NoError(t, err)
		assert.False(t, before)
		assert.True(t, after)
	})
}

func TestStore_trust(t *testing.T) {
	t.Run("holds the current CA after the first add", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		roots, err := store.trust()
		require.NoError(t, err)
		current, err := store.currentCert()
		require.NoError(t, err)
		assert.Equal(t, []string{envproto.Fingerprint(current)}, roots.fingerprints())
	})
}

func TestStore_serverCertificate(t *testing.T) {
	t.Run("issues a listener certificate for the public name from the current CA", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		pair, err := store.serverCertificate("public.example")
		require.NoError(t, err)
		require.NotEmpty(t, pair.Certificate)
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		require.NoError(t, err)
		assert.NoError(t, leaf.VerifyHostname("public.example"))
	})
}

func TestDecodeRecord(t *testing.T) {
	valid := `version = 1
id = "env-1a2b3c4d"
name = "linux-server"
created_at = "2026-11-22T14:02:00Z"
state = "pending"
secret_hash = "9f86d0"
enrolled_at = ""
cert_serial = ""
cert_not_after = ""
cert_ca = ""
`
	t.Run("reads a valid record", func(t *testing.T) {
		t.Parallel()
		rec, err := decodeRecord([]byte(valid), "env.toml")
		require.NoError(t, err)
		assert.Equal(t, "linux-server", rec.Name)
		assert.Equal(t, StatePending, rec.State)
	})
	t.Run("refuses an unknown key", func(t *testing.T) {
		t.Parallel()
		_, err := decodeRecord([]byte(valid+"extra = 1\n"), "env.toml")
		assert.Error(t, err)
	})
	t.Run("refuses version 2", func(t *testing.T) {
		t.Parallel()
		_, err := decodeRecord([]byte(strings.Replace(valid, "version = 1", "version = 2", 1)), "env.toml")
		assert.Error(t, err)
	})
	t.Run("refuses an unknown state", func(t *testing.T) {
		t.Parallel()
		_, err := decodeRecord([]byte(strings.Replace(valid, `"pending"`, `"gone"`, 1)), "env.toml")
		assert.Error(t, err)
	})
}

// encodeCertString is the PEM of cert as a string, as the responses carry it.
func encodeCertString(cert *x509.Certificate) string {
	return string(encodeCert(cert))
}
