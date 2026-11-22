// Package envagent is the environment agent's side of ADR 0075: the files
// that hold its enrollment, the enrollment client and the status that
// ws env agent status prints.
package envagent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// Files on disk, in the agent's environment directory.
const (
	agentFileName = "agent.toml"
	keyFileName   = "key.pem"
	certFileName  = "cert.pem"
	rootsFileName = "ca.pem"
	agentVersion  = 1
	dirMode       = 0o700
	fileMode      = 0o600
)

var (
	// ErrNotEnrolled is returned when this machine has no agent identity.
	ErrNotEnrolled = errors.New("this machine isn't enrolled; run ws env agent enroll <code>")
	// ErrAlreadyEnrolled is returned when enrollment finds an identity already.
	ErrAlreadyEnrolled = errors.New("this machine is already enrolled; run ws env agent unenroll first")
	// ErrPinMismatch is returned when the server's CA doesn't match the code's fingerprint.
	ErrPinMismatch = errors.New("the server's CA doesn't match the code's fingerprint; nothing was sent")
	// ErrEnrollmentUsed is returned when the code was already used.
	ErrEnrollmentUsed = errors.New("that code was already used")
	// ErrEnrollmentFailed is returned when the server refuses the enrollment or answers wrongly.
	ErrEnrollmentFailed = errors.New("enrollment failed")
)

// Identity is an enrolled agent.
type Identity struct {
	ID, Name, Broker, ServerName string
	EnrolledAt                   time.Time
	Key                          *ecdsa.PrivateKey
	Cert                         *x509.Certificate
	Roots                        []*x509.Certificate
}

// TLSConfig is the client TLS configuration for the broker: the agent's
// certificate, and the roots that verify the broker's.
func (id *Identity) TLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	for _, root := range id.Roots {
		pool.AddCert(root)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{id.Cert.Raw}, PrivateKey: id.Key, Leaf: id.Cert}},
		RootCAs:      pool,
		ServerName:   id.ServerName,
	}
}

// Fingerprints lists the roots' fingerprints, current first, for the hello.
func (id *Identity) Fingerprints() []string {
	fingerprints := make([]string, 0, len(id.Roots))
	for _, root := range id.Roots {
		fingerprints = append(fingerprints, envproto.Fingerprint(root))
	}
	return fingerprints
}

// Files is the agent's environment directory (CONFIG_DIR/environment).
type Files struct{ dir string }

// NewFiles returns the files kept in dir.
func NewFiles(dir string) *Files {
	return &Files{dir: dir}
}

// agentFile is agent.toml.
type agentFile struct {
	Version    int    `toml:"version"`
	ID         string `toml:"id"`
	Name       string `toml:"name"`
	Broker     string `toml:"broker"`
	ServerName string `toml:"server_name"`
	EnrolledAt string `toml:"enrolled_at"`
}

// Load reads the enrolled identity. ErrNotEnrolled when agent.toml is missing.
func (f *Files) Load() (Identity, error) {
	id, err := f.loadAgentFile()
	if err != nil {
		return Identity{}, err
	}
	key, err := readKey(f.path(keyFileName))
	if err != nil {
		return Identity{}, err
	}
	cert, err := readCert(f.path(certFileName))
	if err != nil {
		return Identity{}, err
	}
	roots, err := f.loadRoots()
	if err != nil {
		return Identity{}, err
	}
	id.Key, id.Cert, id.Roots = key, cert, roots
	return id, nil
}

// loadAgentFile reads agent.toml's fields.
func (f *Files) loadAgentFile() (Identity, error) {
	data, err := os.ReadFile(f.path(agentFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Identity{}, ErrNotEnrolled
	}
	if err != nil {
		return Identity{}, fmt.Errorf("reading %s: %w", agentFileName, err)
	}
	var file agentFile
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return Identity{}, fmt.Errorf("parsing %s: %w", agentFileName, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Identity{}, fmt.Errorf("parsing %s: unknown key %s", agentFileName, undecoded[0])
	}
	if file.Version != agentVersion {
		return Identity{}, fmt.Errorf("parsing %s: version %d isn't %d", agentFileName, file.Version, agentVersion)
	}
	enrolledAt, err := time.Parse(time.RFC3339, file.EnrolledAt)
	if err != nil {
		return Identity{}, fmt.Errorf("parsing %s: enrolled_at: %w", agentFileName, err)
	}
	return Identity{ID: file.ID, Name: file.Name, Broker: file.Broker, ServerName: file.ServerName, EnrolledAt: enrolledAt}, nil
}

// loadRoots reads ca.pem.
func (f *Files) loadRoots() ([]*x509.Certificate, error) {
	data, err := os.ReadFile(f.path(rootsFileName))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rootsFileName, err)
	}
	roots, err := parseRoots(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rootsFileName, err)
	}
	return roots, nil
}

// Save writes every file of id. agent.toml goes last, so an interrupted
// save doesn't look enrolled.
func (f *Files) Save(id *Identity) error {
	if err := os.MkdirAll(f.dir, dirMode); err != nil {
		return fmt.Errorf("creating %s: %w", f.dir, err)
	}
	if err := os.Chmod(f.dir, dirMode); err != nil { //nolint:gosec // G302: the agent directory is 0700 by design (ADR 0075).
		return fmt.Errorf("setting the mode of %s: %w", f.dir, err)
	}
	if err := f.SaveCertificate(id.Key, id.Cert); err != nil {
		return err
	}
	if err := f.SaveRoots(id.Roots); err != nil {
		return err
	}
	return f.saveAgentFile(id)
}

// saveAgentFile writes agent.toml.
func (f *Files) saveAgentFile(id *Identity) error {
	file := agentFile{
		Version:    agentVersion,
		ID:         id.ID,
		Name:       id.Name,
		Broker:     id.Broker,
		ServerName: id.ServerName,
		EnrolledAt: id.EnrolledAt.UTC().Format(time.RFC3339),
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(file); err != nil {
		return fmt.Errorf("encoding %s: %w", agentFileName, err)
	}
	return writeFileAtomic(f.path(agentFileName), buf.Bytes())
}

// SaveCertificate replaces key.pem and cert.pem, each atomically, key first.
func (f *Files) SaveCertificate(key *ecdsa.PrivateKey, cert *x509.Certificate) error {
	keyPEM, err := encodeKey(key)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(f.path(keyFileName), keyPEM); err != nil {
		return err
	}
	return writeFileAtomic(f.path(certFileName), encodeCert(cert))
}

// SaveRoots replaces ca.pem with roots, which must be one or two CA certificates.
func (f *Files) SaveRoots(roots []*x509.Certificate) error {
	if err := checkRootCount(len(roots)); err != nil {
		return err
	}
	var data []byte
	for _, root := range roots {
		data = append(data, encodeCert(root)...)
	}
	return writeFileAtomic(f.path(rootsFileName), data)
}

// Remove deletes every enrolled file that exists, even when some don't
// parse, and returns the identity they held. ErrNotEnrolled when none of
// them exist. Stray files without agent.toml are deleted and yield an
// empty Identity with no error.
func (f *Files) Remove() (Identity, error) {
	id, loadErr := f.Load()
	removed, err := f.removeFiles()
	if err != nil {
		return Identity{}, err
	}
	if !removed {
		return Identity{}, ErrNotEnrolled
	}
	if loadErr != nil && !errors.Is(loadErr, ErrNotEnrolled) {
		return Identity{}, fmt.Errorf("removed the enrolled files, but the identity they held was unreadable: %w", loadErr)
	}
	return id, nil
}

// removeFiles deletes each enrolled file that exists and reports whether any did.
func (f *Files) removeFiles() (bool, error) {
	names := [...]string{agentFileName, keyFileName, certFileName, rootsFileName}
	removed := false
	for _, name := range names {
		err := os.Remove(f.path(name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("removing %s: %w", name, err)
		}
		removed = true
	}
	return removed, nil
}

// path is the file name inside the agent directory.
func (f *Files) path(name string) string {
	return filepath.Join(f.dir, name)
}

// parseRoots decodes one or two PEM CERTIFICATE blocks, each a CA certificate.
func parseRoots(data []byte) ([]*x509.Certificate, error) {
	var roots []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("a %s block isn't a certificate", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing a root: %w", err)
		}
		if !cert.IsCA {
			return nil, errors.New("a root isn't a CA certificate")
		}
		roots = append(roots, cert)
	}
	if err := checkRootCount(len(roots)); err != nil {
		return nil, err
	}
	return roots, nil
}

// checkRootCount allows one root (no rotation) or two (during one).
func checkRootCount(n int) error {
	if n < 1 || n > 2 {
		return fmt.Errorf("need 1 or 2 roots, got %d", n)
	}
	return nil
}

// readCert reads the single certificate in path.
func readCert(path string) (*x509.Certificate, error) {
	der, err := readPEM(path, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate %s: %w", path, err)
	}
	return cert, nil
}

// readKey reads the PKCS#8 ECDSA private key in path.
func readKey(path string) (*ecdsa.PrivateKey, error) {
	der, err := readPEM(path, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parsing private key %s: %w", path, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key %s isn't ECDSA", path)
	}
	return key, nil
}

// readPEM returns the DER of the first PEM block in path, which must be of type blockType.
func readPEM(path, blockType string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != blockType {
		return nil, fmt.Errorf("reading %s: no %s block", path, blockType)
	}
	return block.Bytes, nil
}

// encodeCert is cert as a PEM CERTIFICATE block.
func encodeCert(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

// encodeKey is key as a PEM PRIVATE KEY block (PKCS#8).
func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// writeFileAtomic writes data to a temp file in path's directory with perm,
// syncs, and renames it into place, so a reader sees the old or the new file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	if err := writeSynced(tmp, data); err != nil {
		_ = os.Remove(tmp.Name()) // the write failed; the temp file is only debris
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name()) // the rename failed; the temp file is only debris
		return fmt.Errorf("renaming into %s: %w", path, err)
	}
	return nil
}

// writeSynced sets perm, writes and syncs f, then closes it.
func writeSynced(f *os.File, data []byte) error {
	if err := f.Chmod(fileMode); err != nil {
		_ = f.Close() // the chmod error is the one to report
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close() // the write error is the one to report
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // the sync error is the one to report
		return err
	}
	return f.Close()
}
