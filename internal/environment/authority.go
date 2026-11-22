package environment

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	caKeyFile         = "ca.key"
	caCertFile        = "ca.crt"
	oldCertFile       = "old.crt"
	pendingKeyFile    = "ca.key.new"
	pendingCertFile   = "ca.crt.new"
	serverKeyFile     = "server.key"
	serverCertFile    = "server.crt"
	caLifetime        = 365 * 24 * time.Hour
	clientLifetime    = 90 * 24 * time.Hour
	serverLifetime    = 365 * 24 * time.Hour
	serverRenewWithin = 30 * 24 * time.Hour
	certBackdate      = 5 * time.Minute
	csrPEMType        = "CERTIFICATE REQUEST"
	caNameBytes       = 4
)

var (
	errNoCA       = errors.New("no environments CA")
	errCSRInvalid = errors.New("invalid certificate request")
)

// authority keeps the environments CA in dir and issues certificates from it.
type authority struct {
	dir    string
	now    func() time.Time
	random io.Reader
}

func newAuthority(dir string, now func() time.Time, random io.Reader) *authority {
	return &authority{dir: dir, now: now, random: random}
}

// trust is the set of CA certificates a listener accepts client
// certificates from: the current CA and, during a rotation, the old one.
type trust struct {
	current *x509.Certificate
	old     *x509.Certificate
}

func (t trust) certs() []*x509.Certificate {
	if t.old == nil {
		return []*x509.Certificate{t.current}
	}
	return []*x509.Certificate{t.current, t.old}
}

func (t trust) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range t.certs() {
		pool.AddCert(cert)
	}
	return pool
}

func (t trust) bundlePEM() []byte {
	var bundle []byte
	for _, cert := range t.certs() {
		bundle = append(bundle, encodeCert(cert)...)
	}
	return bundle
}

func (t trust) fingerprints() []string {
	certs := t.certs()
	fingerprints := make([]string, 0, len(certs))
	for _, cert := range certs {
		fingerprints = append(fingerprints, envproto.Fingerprint(cert))
	}
	return fingerprints
}

func (t trust) contains(fingerprint string) bool {
	return slices.Contains(t.fingerprints(), fingerprint)
}

// caExpiryWarning is "" unless now is within renewBeforeDays of ca's expiry.
func caExpiryWarning(ca *x509.Certificate, now time.Time, renewBeforeDays int) string {
	window := time.Duration(renewBeforeDays*24) * time.Hour
	if now.Add(window).Before(ca.NotAfter) {
		return ""
	}
	return fmt.Sprintf("the environments CA expires on %s, within %d days; run ws env ca rotate",
		ca.NotAfter.UTC().Format(time.DateOnly), renewBeforeDays)
}

func (a *authority) path(name string) string {
	return filepath.Join(a.dir, name)
}

func (a *authority) exists() (bool, error) {
	_, err := os.Stat(a.path(caCertFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking for the CA: %w", err)
	}
	return true, nil
}

// ensure creates the CA when ca.crt is missing and reports whether it did.
func (a *authority) ensure() (bool, error) {
	exists, err := a.exists()
	if err != nil || exists {
		return false, err
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return false, fmt.Errorf("creating %s: %w", a.dir, err)
	}
	if _, err := a.writeCA(caKeyFile, caCertFile); err != nil {
		return false, err
	}
	return true, nil
}

// rotate makes a new CA. The previous certificate becomes old.crt, and an
// old.crt that was already there is dropped and returned.
func (a *authority) rotate() (*x509.Certificate, *x509.Certificate, *x509.Certificate, error) {
	previous, err := a.currentCert()
	if err != nil {
		return nil, nil, nil, err
	}
	dropped, err := a.dropOld()
	if err != nil {
		return nil, nil, nil, err
	}
	current, err := a.writeCA(pendingKeyFile, pendingCertFile)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := a.promote(); err != nil {
		return nil, nil, nil, err
	}
	return current, previous, dropped, nil
}

// promote moves the pending CA into place and removes the listeners'
// certificate, so the next serverCertificate is signed by the new CA.
func (a *authority) promote() error {
	if err := os.Rename(a.path(caCertFile), a.path(oldCertFile)); err != nil {
		return fmt.Errorf("keeping the previous CA certificate: %w", err)
	}
	if err := os.Rename(a.path(pendingKeyFile), a.path(caKeyFile)); err != nil {
		return fmt.Errorf("replacing the CA key: %w", err)
	}
	if err := os.Rename(a.path(pendingCertFile), a.path(caCertFile)); err != nil {
		return fmt.Errorf("replacing the CA certificate: %w", err)
	}
	return a.removeServer()
}

func (a *authority) removeServer() error {
	for _, name := range []string{serverKeyFile, serverCertFile} {
		if err := os.Remove(a.path(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	return nil
}

// dropOld removes old.crt and returns the certificate it held, or nil when
// there was none.
func (a *authority) dropOld() (*x509.Certificate, error) {
	old, found, err := a.optionalCert(oldCertFile)
	if err != nil || !found {
		return nil, err
	}
	if err := os.Remove(a.path(oldCertFile)); err != nil {
		return nil, fmt.Errorf("removing the old CA certificate: %w", err)
	}
	return old, nil
}

// signClient issues a client certificate for cn from the CSR's public key.
// The CSR's subject is ignored.
func (a *authority) signClient(csrPEM []byte, cn string) (*x509.Certificate, error) {
	csr, err := parseCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	caCert, caKey, err := a.currentCA()
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial(a.random)
	if err != nil {
		return nil, err
	}
	now := a.now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-certBackdate),
		NotAfter:     now.Add(clientLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return createCert(a.random, template, caCert, csr.PublicKey, caKey)
}

// serverCertificate returns the listeners' certificate for publicName,
// reusing server.crt while it is signed by the current CA, matches the
// name, and is more than 30 days from expiry.
func (a *authority) serverCertificate(publicName string) (tls.Certificate, error) {
	caCert, err := a.currentCert()
	if err != nil {
		return tls.Certificate{}, err
	}
	if cert, ok := a.reuseServer(caCert, publicName); ok {
		return cert, nil
	}
	return a.newServer(caCert, publicName)
}

// reuseServer reports whether server.crt and server.key can be used. Any
// read or parse failure means the certificate is made again.
func (a *authority) reuseServer(caCert *x509.Certificate, publicName string) (tls.Certificate, bool) {
	pair, err := tls.LoadX509KeyPair(a.path(serverCertFile), a.path(serverKeyFile))
	if err != nil {
		return tls.Certificate{}, false
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !serverCertValid(leaf, caCert, publicName, a.now()) {
		return tls.Certificate{}, false
	}
	return serverChain(leaf.Raw, caCert, pair.PrivateKey), true
}

func serverCertValid(leaf, caCert *x509.Certificate, publicName string, now time.Time) bool {
	return leaf.CheckSignatureFrom(caCert) == nil &&
		leaf.VerifyHostname(publicName) == nil &&
		!now.Before(leaf.NotBefore) &&
		now.Add(serverRenewWithin).Before(leaf.NotAfter)
}

func (a *authority) newServer(caCert *x509.Certificate, publicName string) (tls.Certificate, error) {
	caKey, err := readKey(a.path(caKeyFile))
	if err != nil {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), a.random)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating server key: %w", err)
	}
	serial, err := randomSerial(a.random)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := a.now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: publicName},
		NotBefore:    now.Add(-certBackdate),
		NotAfter:     serverNotAfter(now, caCert),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	setServerNames(template, publicName)
	leaf, err := createCert(a.random, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writeKeyPair(a.path(serverKeyFile), a.path(serverCertFile), key, leaf); err != nil {
		return tls.Certificate{}, err
	}
	return serverChain(leaf.Raw, caCert, key), nil
}

func serverNotAfter(now time.Time, caCert *x509.Certificate) time.Time {
	end := now.Add(serverLifetime)
	if caCert.NotAfter.Before(end) {
		return caCert.NotAfter
	}
	return end
}

// setServerNames sets the IP SAN for an address, else the DNS SAN.
func setServerNames(template *x509.Certificate, publicName string) {
	if ip := net.ParseIP(publicName); ip != nil {
		template.IPAddresses = []net.IP{ip}
		return
	}
	template.DNSNames = []string{publicName}
}

// serverChain puts the CA in the chain so clients see it with the leaf.
func serverChain(leafDER []byte, caCert *x509.Certificate, key crypto.PrivateKey) tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{leafDER, caCert.Raw}, PrivateKey: key}
}

// writeCA makes a new CA key and certificate and writes them to keyName and
// certName in the CA directory.
func (a *authority) writeCA(keyName, certName string) (*x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), a.random)
	if err != nil {
		return nil, fmt.Errorf("generating CA key: %w", err)
	}
	cert, err := a.newCACertificate(key)
	if err != nil {
		return nil, err
	}
	if err := writeKeyPair(a.path(keyName), a.path(certName), key, cert); err != nil {
		return nil, err
	}
	return cert, nil
}

func (a *authority) newCACertificate(key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := randomSerial(a.random)
	if err != nil {
		return nil, err
	}
	name := make([]byte, caNameBytes)
	if _, err := io.ReadFull(a.random, name); err != nil {
		return nil, fmt.Errorf("reading CA name: %w", err)
	}
	now := a.now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ws environments CA " + hex.EncodeToString(name)},
		NotBefore:             now.Add(-certBackdate),
		NotAfter:              now.Add(caLifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	return createCert(a.random, template, template, &key.PublicKey, key)
}

// currentCA returns the current CA certificate and its key.
func (a *authority) currentCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	caCert, err := a.currentCert()
	if err != nil {
		return nil, nil, err
	}
	caKey, err := readKey(a.path(caKeyFile))
	if err != nil {
		return nil, nil, err
	}
	return caCert, caKey, nil
}

// currentCert reads ca.crt, or returns errNoCA when it is missing.
func (a *authority) currentCert() (*x509.Certificate, error) {
	cert, err := readCert(a.path(caCertFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNoCA
	}
	return cert, err
}

// optionalCert reads the certificate at name. found is false when the file
// is missing.
func (a *authority) optionalCert(name string) (*x509.Certificate, bool, error) {
	cert, err := readCert(a.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return cert, true, nil
}

// trust reads ca.crt and, when present, old.crt.
func (a *authority) trust() (trust, error) {
	current, err := a.currentCert()
	if err != nil {
		return trust{}, err
	}
	old, _, err := a.optionalCert(oldCertFile)
	if err != nil {
		return trust{}, err
	}
	return trust{current: current, old: old}, nil
}

// parseCSR decodes a PEM certificate request whose signature verifies and
// whose key is ECDSA P-256.
func parseCSR(csrPEM []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != csrPEMType {
		return nil, fmt.Errorf("%w: not a PEM certificate request", errCSRInvalid)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCSRInvalid, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: %w", errCSRInvalid, err)
	}
	key, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: key is not ECDSA P-256", errCSRInvalid)
	}
	return csr, nil
}

func createCert(random io.Reader, template, parent *x509.Certificate, pub any, signer crypto.Signer) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(random, template, parent, pub, signer)
	if err != nil {
		return nil, fmt.Errorf("creating certificate: %w", err)
	}
	return x509.ParseCertificate(der)
}

// writeKeyPair writes key to keyPath (0600) and cert to certPath (0644).
func writeKeyPair(keyPath, certPath string, key *ecdsa.PrivateKey, cert *x509.Certificate) error {
	keyPEM, err := encodeKey(key)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	return writeFileAtomic(certPath, encodeCert(cert), 0o644)
}
