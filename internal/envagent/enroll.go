package envagent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	certCommonName  = "ws-environment-agent"
	enrollTimeout   = 30 * time.Second
	maxEnrollAnswer = 64 << 10
)

// Enroller exchanges an enrollment code for the agent's identity.
type Enroller struct {
	log    *slog.Logger
	files  *Files
	random io.Reader
	// wrapTransport, when set, wraps the pinned transport; tests use it to read the request body.
	wrapTransport func(http.RoundTripper) http.RoundTripper
}

// NewEnroller returns an Enroller that saves into files and reads key
// material from random.
func NewEnroller(log *slog.Logger, files *Files, random io.Reader) *Enroller {
	return &Enroller{log: log, files: files, random: random}
}

// Enroll checks the machine isn't enrolled, parses code, makes a key and
// CSR, and POSTs them to the code's host over TLS pinned to the code's
// fingerprint. The answer's certificate must be for the answer's id and
// the new key, and chain to the root with that fingerprint. Then it saves
// the identity. Nothing is written on failure.
func (e *Enroller) Enroll(ctx context.Context, code string) (Identity, error) {
	if err := e.checkNotEnrolled(); err != nil {
		return Identity{}, err
	}
	parsed, err := envproto.ParseCode(code)
	if err != nil {
		return Identity{}, err
	}
	key, csrPEM, err := newKeyAndCSR(e.random)
	if err != nil {
		return Identity{}, err
	}
	answer, err := e.exchangeEnrollment(ctx, parsed, csrPEM)
	if err != nil {
		return Identity{}, err
	}
	id, err := verifyEnrollment(&answer, key, parsed.Fingerprint)
	if err != nil {
		return Identity{}, err
	}
	if err := e.files.Save(&id); err != nil {
		return Identity{}, err
	}
	e.log.InfoContext(ctx, "agent enrolled", "id", id.ID)
	return id, nil
}

// checkNotEnrolled is ErrAlreadyEnrolled when an identity exists, nil when none does.
func (e *Enroller) checkNotEnrolled() error {
	_, err := e.files.Load()
	if errors.Is(err, ErrNotEnrolled) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrAlreadyEnrolled
}

// exchangeEnrollment POSTs the secret and CSR to the code's host, pinned.
func (e *Enroller) exchangeEnrollment(ctx context.Context, code envproto.Code, csrPEM []byte) (envproto.EnrollResponse, error) {
	host, _, err := net.SplitHostPort(code.Host)
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	body, err := enrollBody(code.Secret, csrPEM)
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	tlsTransport := &http.Transport{TLSClientConfig: pinTLS(code.Fingerprint, host)}
	defer tlsTransport.CloseIdleConnections()
	var transport http.RoundTripper = tlsTransport
	if e.wrapTransport != nil {
		transport = e.wrapTransport(transport)
	}
	client := &http.Client{Transport: transport, Timeout: enrollTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+code.Host+envproto.EnrollPath, bytes.NewReader(body))
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return envproto.EnrollResponse{}, fmt.Errorf("posting enrollment: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // a close error leaves nothing to act on
	return decodeEnrollment(resp)
}

// decodeEnrollment reads the server's answer: 200 is the response, 409 is
// ErrEnrollmentUsed, anything else is ErrEnrollmentFailed with its message.
func decodeEnrollment(resp *http.Response) (envproto.EnrollResponse, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEnrollAnswer))
	if err != nil {
		return envproto.EnrollResponse{}, fmt.Errorf("reading the enrollment answer: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var answer envproto.EnrollResponse
		if err := json.Unmarshal(body, &answer); err != nil {
			return envproto.EnrollResponse{}, fmt.Errorf("%w: answer: %w", ErrEnrollmentFailed, err)
		}
		return answer, nil
	case http.StatusConflict:
		return envproto.EnrollResponse{}, ErrEnrollmentUsed
	default:
		return envproto.EnrollResponse{}, fmt.Errorf("%w: %s", ErrEnrollmentFailed, serverMessage(body))
	}
}

// serverMessage is the message in an error envelope, or a fixed text when there is none.
func serverMessage(body []byte) string {
	var envelope envproto.ErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Message == "" {
		return "the server gave no reason"
	}
	return envelope.Error.Message
}

// enrollBody is the JSON EnrollRequest carrying the secret and the CSR. It
// is the only body Enroll sends, so the key never leaves the machine.
func enrollBody(secret string, csrPEM []byte) ([]byte, error) {
	return json.Marshal(envproto.EnrollRequest{Secret: secret, CSR: string(csrPEM)}) //nolint:gosec // G117: the secret is sent to the enrollment listener by design (ADR 0075).
}

// verifyEnrollment checks the server's certificate against the key and the pinned root.
func verifyEnrollment(answer *envproto.EnrollResponse, key *ecdsa.PrivateKey, fingerprint string) (Identity, error) {
	cert, err := decodeCertPEM([]byte(answer.Cert))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: certificate: %w", ErrEnrollmentFailed, err)
	}
	roots, err := parseRoots([]byte(answer.CABundle))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: roots: %w", ErrEnrollmentFailed, err)
	}
	pinned, err := pinnedRoot(roots, fingerprint)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrEnrollmentFailed, err)
	}
	if err := checkCertificate(cert, answer.ID, key, pinned); err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrEnrollmentFailed, err)
	}
	return Identity{
		ID:         answer.ID,
		Name:       answer.Name,
		Broker:     answer.Broker,
		ServerName: answer.ServerName,
		EnrolledAt: time.Now().UTC(),
		Key:        key,
		Cert:       cert,
		Roots:      roots,
	}, nil
}

// decodeCertPEM parses the single PEM CERTIFICATE in data.
func decodeCertPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no CERTIFICATE block")
	}
	return x509.ParseCertificate(block.Bytes)
}

// pinnedRoot is the root whose fingerprint is fingerprint.
func pinnedRoot(roots []*x509.Certificate, fingerprint string) (*x509.Certificate, error) {
	for _, root := range roots {
		if envproto.Fingerprint(root) == fingerprint {
			return root, nil
		}
	}
	return nil, errors.New("no root has the code's fingerprint")
}

// checkCertificate requires cert to be for id and key, and to chain to root as a client certificate.
func checkCertificate(cert *x509.Certificate, id string, key *ecdsa.PrivateKey, root *x509.Certificate) error {
	if err := checkIdentity(cert, id, key); err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("the certificate doesn't chain to the pinned root: %w", err)
	}
	return nil
}

// checkIdentity requires cert to be for id and key.
func checkIdentity(cert *x509.Certificate, id string, key *ecdsa.PrivateKey) error {
	if cert.Subject.CommonName != id {
		return fmt.Errorf("the certificate is for %q, not %s", cert.Subject.CommonName, id)
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return errors.New("the certificate isn't for the agent's key")
	}
	return nil
}

// newKeyAndCSR makes a P-256 key and a PEM CSR for it, with CN ws-environment-agent.
func newKeyAndCSR(random io.Reader) (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, nil, fmt.Errorf("making the agent key: %w", err)
	}
	template := &x509.CertificateRequest{Subject: pkix.Name{CommonName: certCommonName}}
	der, err := x509.CreateCertificateRequest(random, template, key)
	if err != nil {
		return nil, nil, fmt.Errorf("making the certificate request: %w", err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// pinTLS verifies the server during the handshake against the CA with
// fingerprint, before any request byte is sent (ADR 0075). The default chain
// check is replaced, so only the pinned CA can vouch for the server.
func pinTLS(fingerprint, host string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection checks the chain against the pinned CA fingerprint (ADR 0075).
		VerifyConnection:   verifyPin(fingerprint, host),
	}
}

// verifyPin accepts a connection when a presented CA certificate has
// fingerprint and the server's leaf verifies against that CA alone for
// host, as a server certificate. Otherwise it is ErrPinMismatch.
func verifyPin(fingerprint, host string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return ErrPinMismatch
		}
		pinned := presentedCA(cs.PeerCertificates, fingerprint)
		if pinned == nil {
			return ErrPinMismatch
		}
		pool := x509.NewCertPool()
		pool.AddCert(pinned)
		opts := x509.VerifyOptions{DNSName: host, Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
			return fmt.Errorf("%w: %w", ErrPinMismatch, err)
		}
		return nil
	}
}

// presentedCA is the CA certificate among certs with fingerprint, or nil.
func presentedCA(certs []*x509.Certificate, fingerprint string) *x509.Certificate {
	for _, cert := range certs {
		if cert.IsCA && envproto.Fingerprint(cert) == fingerprint {
			return cert
		}
	}
	return nil
}
