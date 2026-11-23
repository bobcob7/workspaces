package envagent

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

const fixtureName = "linux-server"

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// startEnrollFixture starts a controller with its enrollment listener on
// loopback and one pending environment, and returns its store and code.
// The code names the listener's bound address, so Add's placeholder host
// is replaced with Rotate before anyone enrolls.
func startEnrollFixture(t *testing.T, log *slog.Logger) (*environment.Store, *environment.Controller, string) {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	store := environment.NewStore(filepath.Join(root, "environments"), filepath.Join(root, "state"), time.Now, rand.Reader)
	ctrl := environment.NewController(log, store, envtest.NewBroker(), environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	_, _, _, err := store.Add(ctx, fixtureName, "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, ctrl.Start(ctx))
	_, code, _, err := store.Rotate(ctx, fixtureName, ctrl.Status().EnrollListen)
	require.NoError(t, err)
	return store, ctrl, code
}

// newFilesAndEnroller returns files in a new directory and an Enroller over them.
func newFilesAndEnroller(t *testing.T) (*Files, *Enroller) {
	t.Helper()
	files := NewFiles(filepath.Join(t.TempDir(), "environment"))
	return files, NewEnroller(discardLog(), files, rand.Reader)
}

// pemCertificate is cert as a PEM CERTIFICATE block.
func pemCertificate(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

// assertModes checks the enrolled files and directory have the modes ADR 0075 asks for.
func assertModes(t *testing.T, files *Files) {
	t.Helper()
	info, err := os.Stat(files.dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	for _, name := range []string{agentFileName, keyFileName, certFileName, rootsFileName} {
		info, err := os.Stat(files.path(name))
		require.NoError(t, err, name)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), name)
	}
}

func TestEnroller_Enroll(t *testing.T) {
	t.Run("enrolls with a code and saves the identity", func(t *testing.T) {
		t.Parallel()
		store, _, code := startEnrollFixture(t, discardLog())
		files, enroller := newFilesAndEnroller(t)
		id, err := enroller.Enroll(t.Context(), code)
		require.NoError(t, err)
		assert.Equal(t, fixtureName, id.Name)
		loaded, err := files.Load()
		require.NoError(t, err)
		assert.Equal(t, id.ID, loaded.ID)
		assertModes(t, files)
		rec, err := store.Get(fixtureName)
		require.NoError(t, err)
		assert.Equal(t, environment.StateEnrolled, rec.State)
		assert.Equal(t, id.ID, rec.ID)
	})
	t.Run("refuses a second enrollment on the same files", func(t *testing.T) {
		t.Parallel()
		_, _, code := startEnrollFixture(t, discardLog())
		_, enroller := newFilesAndEnroller(t)
		_, err := enroller.Enroll(t.Context(), code)
		require.NoError(t, err)
		_, err = enroller.Enroll(t.Context(), code)
		assert.ErrorIs(t, err, ErrAlreadyEnrolled)
	})
	t.Run("refuses a used code on fresh files", func(t *testing.T) {
		t.Parallel()
		_, _, code := startEnrollFixture(t, discardLog())
		_, first := newFilesAndEnroller(t)
		_, err := first.Enroll(t.Context(), code)
		require.NoError(t, err)
		_, second := newFilesAndEnroller(t)
		_, err = second.Enroll(t.Context(), code)
		assert.ErrorIs(t, err, ErrEnrollmentUsed)
	})
	t.Run("sends neither the key's PEM nor its DER", func(t *testing.T) {
		t.Parallel()
		_, _, code := startEnrollFixture(t, discardLog())
		files, enroller := newFilesAndEnroller(t)
		capture := &bodyCapture{}
		enroller.wrapTransport = func(next http.RoundTripper) http.RoundTripper {
			capture.next = next
			return capture
		}
		_, err := enroller.Enroll(t.Context(), code)
		require.NoError(t, err)
		require.Contains(t, string(capture.body), `"csr"`)
		saved, err := files.Load()
		require.NoError(t, err)
		keyPEM, err := os.ReadFile(files.path(keyFileName))
		require.NoError(t, err)
		der, err := x509.MarshalPKCS8PrivateKey(saved.Key)
		require.NoError(t, err)
		quotedPEM, err := json.Marshal(string(keyPEM))
		require.NoError(t, err)
		body := string(capture.body)
		assert.NotContains(t, body, string(keyPEM))
		assert.NotContains(t, body, string(quotedPEM[1:len(quotedPEM)-1]))
		assert.NotContains(t, body, string(der))
		assert.NotContains(t, body, base64.StdEncoding.EncodeToString(der))
	})
	t.Run("refuses a wrong fingerprint before any request is sent", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			requests.Add(1)
		}))
		t.Cleanup(server.Close)
		_, _, code := startEnrollFixture(t, discardLog())
		parsed, err := envproto.ParseCode(code)
		require.NoError(t, err)
		parsed.Host = server.Listener.Addr().String()
		parsed.Fingerprint = strings.Repeat("0", 64)
		files, enroller := newFilesAndEnroller(t)
		_, err = enroller.Enroll(t.Context(), parsed.String())
		require.ErrorIs(t, err, ErrPinMismatch)
		assert.Zero(t, requests.Load())
		_, err = files.Load()
		require.ErrorIs(t, err, ErrNotEnrolled)
	})
	t.Run("refuses a code made before the CA rotated, then accepts one made after", func(t *testing.T) {
		t.Parallel()
		store, ctrl, oldCode := startEnrollFixture(t, discardLog())
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, ctrl.Reload(t.Context()))
		files, enroller := newFilesAndEnroller(t)
		_, err = enroller.Enroll(t.Context(), oldCode)
		require.ErrorIs(t, err, ErrPinMismatch)
		_, err = files.Load()
		require.ErrorIs(t, err, ErrNotEnrolled)
		_, newCode, _, err := store.Rotate(t.Context(), fixtureName, ctrl.Status().EnrollListen)
		require.NoError(t, err)
		_, err = enroller.Enroll(t.Context(), newCode)
		assert.NoError(t, err)
	})
}

func TestEnroller_checkNotEnrolled(t *testing.T) {
	t.Run("is nil on an empty directory", func(t *testing.T) {
		_, enroller := newFilesAndEnroller(t)
		assert.NoError(t, enroller.checkNotEnrolled())
	})
	t.Run("is ErrAlreadyEnrolled once an identity is saved", func(t *testing.T) {
		files, enroller := newFilesAndEnroller(t)
		id := testIdentity(t)
		require.NoError(t, files.Save(&id))
		assert.ErrorIs(t, enroller.checkNotEnrolled(), ErrAlreadyEnrolled)
	})
}

// bodyCapture passes requests to next and keeps the body of the last one.
type bodyCapture struct {
	next http.RoundTripper
	body []byte
}

func (c *bodyCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close() // the body is read in full; nothing more to release.
	c.body = data
	req.Body = io.NopCloser(bytes.NewReader(data))
	return c.next.RoundTrip(req)
}

func TestEnrollBody(t *testing.T) {
	t.Run("sends the secret and the CSR, never the key", func(t *testing.T) {
		key := newTestKey(t)
		_, csrPEM, err := newKeyAndCSR(rand.Reader)
		require.NoError(t, err)
		body, err := enrollBody("s3cret", csrPEM)
		require.NoError(t, err)
		var req envproto.EnrollRequest
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "s3cret", req.Secret)
		assert.Equal(t, string(csrPEM), req.CSR)
		keyPEM, err := encodeKey(key)
		require.NoError(t, err)
		der, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)
		assert.NotContains(t, string(body), base64.StdEncoding.EncodeToString(der))
		assert.NotContains(t, string(body), strings.TrimSpace(string(keyPEM)))
	})
}

func TestNewKeyAndCSR(t *testing.T) {
	t.Run("makes a P-256 key and a CSR for it, named for the agent", func(t *testing.T) {
		key, csrPEM, err := newKeyAndCSR(rand.Reader)
		require.NoError(t, err)
		block, _ := pem.Decode(csrPEM)
		require.NotNil(t, block)
		assert.Equal(t, "CERTIFICATE REQUEST", block.Type)
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		require.NoError(t, err)
		require.NoError(t, csr.CheckSignature())
		assert.Equal(t, certCommonName, csr.Subject.CommonName)
		assert.True(t, key.PublicKey.Equal(csr.PublicKey))
	})
}

func TestDecodeEnrollment(t *testing.T) {
	t.Run("refuses a 409 as a used code", func(t *testing.T) {
		_, err := decodeEnrollment(testResponse(http.StatusConflict, ""))
		assert.ErrorIs(t, err, ErrEnrollmentUsed)
	})
	t.Run("gives ErrEnrollmentFailed with the server's message for other statuses", func(t *testing.T) {
		_, err := decodeEnrollment(testResponse(http.StatusForbidden, `{"error":{"code":"expired","message":"the code expired"}}`))
		require.ErrorIs(t, err, ErrEnrollmentFailed)
		assert.ErrorContains(t, err, "the code expired")
	})
	t.Run("says the server gave no reason when the body has no message", func(t *testing.T) {
		_, err := decodeEnrollment(testResponse(http.StatusInternalServerError, "oops"))
		require.ErrorIs(t, err, ErrEnrollmentFailed)
		assert.ErrorContains(t, err, "the server gave no reason")
	})
	t.Run("gives ErrEnrollmentFailed for a 200 that isn't JSON", func(t *testing.T) {
		_, err := decodeEnrollment(testResponse(http.StatusOK, "{"))
		require.ErrorIs(t, err, ErrEnrollmentFailed)
	})
}

// testResponse is a response with status and body.
func testResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func TestVerifyEnrollment(t *testing.T) {
	id := testIdentity(t)
	t.Run("accepts a certificate for the id and key, chained to the pinned root", func(t *testing.T) {
		answer := envproto.EnrollResponse{ID: id.ID, Name: id.Name, Broker: id.Broker, ServerName: id.ServerName, Cert: pemCertificate(id.Cert), CABundle: pemCertificate(id.Roots[0])}
		got, err := verifyEnrollment(&answer, id.Key, envproto.Fingerprint(id.Roots[0]))
		require.NoError(t, err)
		assert.Equal(t, id.ID, got.ID)
		assert.Equal(t, id.Cert.Raw, got.Cert.Raw)
		assert.Len(t, got.Roots, 1)
	})
	t.Run("refuses a certificate for another id", func(t *testing.T) {
		answer := envproto.EnrollResponse{ID: "env-other", Cert: pemCertificate(id.Cert), CABundle: pemCertificate(id.Roots[0])}
		_, err := verifyEnrollment(&answer, id.Key, envproto.Fingerprint(id.Roots[0]))
		assert.ErrorIs(t, err, ErrEnrollmentFailed)
	})
	t.Run("refuses a certificate for another key", func(t *testing.T) {
		answer := envproto.EnrollResponse{ID: id.ID, Cert: pemCertificate(id.Cert), CABundle: pemCertificate(id.Roots[0])}
		_, err := verifyEnrollment(&answer, newTestKey(t), envproto.Fingerprint(id.Roots[0]))
		assert.ErrorIs(t, err, ErrEnrollmentFailed)
	})
	t.Run("refuses roots without the code's fingerprint", func(t *testing.T) {
		other, _ := newTestCA(t, 9)
		answer := envproto.EnrollResponse{ID: id.ID, Cert: pemCertificate(id.Cert), CABundle: pemCertificate(id.Roots[0])}
		_, err := verifyEnrollment(&answer, id.Key, envproto.Fingerprint(other))
		assert.ErrorIs(t, err, ErrEnrollmentFailed)
	})
	t.Run("refuses a certificate that doesn't chain to the pinned root", func(t *testing.T) {
		other, _ := newTestCA(t, 9)
		answer := envproto.EnrollResponse{ID: id.ID, Cert: pemCertificate(id.Cert), CABundle: pemCertificate(other)}
		_, err := verifyEnrollment(&answer, id.Key, envproto.Fingerprint(other))
		assert.ErrorIs(t, err, ErrEnrollmentFailed)
	})
}

func TestVerifyPin(t *testing.T) {
	ca, caKey := newTestCA(t, 1)
	serverKey := newTestKey(t)
	server := newTestLeaf(t, ca, caKey, &serverKey.PublicKey, 2, "127.0.0.1", x509.ExtKeyUsageServerAuth)
	other, otherKey := newTestCA(t, 3)
	otherServer := newTestLeaf(t, other, otherKey, &serverKey.PublicKey, 4, "127.0.0.1", x509.ExtKeyUsageServerAuth)
	pinned := envproto.Fingerprint(ca)
	t.Run("accepts a server certificate signed by the pinned CA", func(t *testing.T) {
		cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{server, ca}}
		assert.NoError(t, verifyPin(pinned, "127.0.0.1")(cs))
	})
	t.Run("refuses a fingerprint no presented CA has", func(t *testing.T) {
		cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{server, ca}}
		assert.ErrorIs(t, verifyPin(strings.Repeat("0", 64), "127.0.0.1")(cs), ErrPinMismatch)
	})
	t.Run("refuses a host the certificate isn't for", func(t *testing.T) {
		cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{server, ca}}
		assert.ErrorIs(t, verifyPin(pinned, "other.example")(cs), ErrPinMismatch)
	})
	t.Run("refuses a chain without the pinned CA", func(t *testing.T) {
		cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{server}}
		assert.ErrorIs(t, verifyPin(pinned, "127.0.0.1")(cs), ErrPinMismatch)
	})
	t.Run("refuses a server signed by another CA", func(t *testing.T) {
		cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{otherServer, ca}}
		assert.ErrorIs(t, verifyPin(pinned, "127.0.0.1")(cs), ErrPinMismatch)
	})
	t.Run("refuses no certificates", func(t *testing.T) {
		assert.ErrorIs(t, verifyPin(pinned, "127.0.0.1")(tls.ConnectionState{}), ErrPinMismatch)
	})
}
