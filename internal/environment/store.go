package environment

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lockfile"
)

const (
	recordVersion = 1
	recordSuffix  = ".toml"
	lockFile      = ".lock"
	idBytes       = 4
	snapshotFile  = "snapshot.json"
)

// namePattern is the environment name rule: a letter, then up to 31 of a-z,
// 0-9 and "-".
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// State is whether an environment has an enrolled agent.
type State string

const (
	// StatePending is an environment whose code hasn't been used yet.
	StatePending State = "pending"
	// StateEnrolled is an environment with a certificate issued to it.
	StateEnrolled State = "enrolled"
)

// Record is environments/<id>.toml.
type Record struct {
	Version      int    `toml:"version"`
	ID           string `toml:"id"`
	Name         string `toml:"name"`
	CreatedAt    string `toml:"created_at"`
	State        State  `toml:"state"`
	SecretHash   string `toml:"secret_hash"`
	EnrolledAt   string `toml:"enrolled_at"`
	CertSerial   string `toml:"cert_serial"`
	CertNotAfter string `toml:"cert_not_after"`
	CertCA       string `toml:"cert_ca"`
}

// Store is the controller's environments: records, CA, revocations and snapshots.
type Store struct {
	dir      string
	stateDir string
	ca       *authority
	now      func() time.Time
	random   io.Reader
}

// NewStore takes dir as CONFIG_DIR/environments and stateDir as
// CONFIG_DIR/state. It does no I/O.
func NewStore(dir, stateDir string, now func() time.Time, random io.Reader) *Store {
	return &Store{
		dir:      dir,
		stateDir: stateDir,
		ca:       newAuthority(filepath.Join(dir, "ca"), now, random),
		now:      now,
		random:   random,
	}
}

// Add makes a pending environment called name and returns its record, its
// enrollment code and whether the CA was created for it.
func (s *Store) Add(ctx context.Context, name, enrollHost string) (rec Record, code string, caCreated bool, err error) {
	if !validName(name) {
		return Record{}, "", false, ErrNameInvalid
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, "", false, err
	}
	defer unlock()
	taken, err := s.nameTaken(name)
	if err != nil {
		return Record{}, "", false, err
	}
	if taken {
		return Record{}, "", false, ErrExists
	}
	caCreated, err = s.ca.ensure()
	if err != nil {
		return Record{}, "", false, err
	}
	rec, code, err = s.addPending(name, enrollHost)
	return rec, code, caCreated, err
}

func (s *Store) nameTaken(name string) (bool, error) {
	_, err := s.Get(name)
	if errors.Is(err, ErrUnknown) {
		return false, nil
	}
	return err == nil, err
}

// addPending writes a pending record for name with a new code. The caller
// holds the lock and has ensured the CA.
func (s *Store) addPending(name, enrollHost string) (Record, string, error) {
	id, err := s.newID()
	if err != nil {
		return Record{}, "", err
	}
	rec := Record{
		Version:   recordVersion,
		ID:        id,
		Name:      name,
		CreatedAt: s.timestamp(),
	}
	code, err := s.setCode(&rec, enrollHost)
	if err != nil {
		return Record{}, "", err
	}
	return rec, code, nil
}

// setCode gives rec a new secret, stores its hash and writes rec as pending.
// It returns the code for the secret.
func (s *Store) setCode(rec *Record, enrollHost string) (string, error) {
	secret, err := envproto.NewSecret(s.random)
	if err != nil {
		return "", err
	}
	fingerprint, err := s.currentFingerprint()
	if err != nil {
		return "", err
	}
	rec.State = StatePending
	rec.SecretHash = envproto.HashSecret(secret)
	rec.EnrolledAt = ""
	rec.CertSerial = ""
	rec.CertNotAfter = ""
	rec.CertCA = ""
	if err := s.writeRecord(rec); err != nil {
		return "", err
	}
	return envproto.Code{Host: enrollHost, Fingerprint: fingerprint, Secret: secret}.String(), nil
}

// Rotate gives an existing environment a new code. An enrolled environment's
// certificate is revoked, and the returned serials list it.
func (s *Store) Rotate(ctx context.Context, name, enrollHost string) (rec Record, code string, revoked []string, err error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, "", nil, err
	}
	defer unlock()
	rec, err = s.Get(name)
	if err != nil {
		return Record{}, "", nil, err
	}
	revoked, err = s.revokeCurrent(&rec)
	if err != nil {
		return Record{}, "", nil, err
	}
	code, err = s.setCode(&rec, enrollHost)
	if err != nil {
		return Record{}, "", nil, err
	}
	return rec, code, revoked, nil
}

// Remove revokes an environment's certificate, deletes its record and its
// snapshot directory, and returns the revoked serials.
func (s *Store) Remove(ctx context.Context, name string) (rec Record, revoked []string, err error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, nil, err
	}
	defer unlock()
	rec, err = s.Get(name)
	if err != nil {
		return Record{}, nil, err
	}
	revoked, err = s.revokeCurrent(&rec)
	if err != nil {
		return Record{}, nil, err
	}
	if err := os.Remove(s.recordPath(rec.ID)); err != nil {
		return Record{}, nil, fmt.Errorf("removing environment %s: %w", name, err)
	}
	if err := os.RemoveAll(s.snapshotDir(name)); err != nil {
		return Record{}, nil, fmt.Errorf("removing the snapshot of %s: %w", name, err)
	}
	return rec, revoked, nil
}

// revokeCurrent revokes rec's certificate when it is enrolled and returns
// the serials it revoked, never nil.
func (s *Store) revokeCurrent(rec *Record) ([]string, error) {
	if rec.State != StateEnrolled {
		return []string{}, nil
	}
	if err := s.revoke(rec.CertSerial, rec.ID); err != nil {
		return nil, err
	}
	return []string{rec.CertSerial}, nil
}

// Get returns the environment called name, or ErrUnknown.
func (s *Store) Get(name string) (Record, error) {
	return s.find(func(rec Record) bool { return rec.Name == name })
}

// ByID returns the environment with id, or ErrUnknown.
func (s *Store) ByID(id string) (Record, error) {
	return s.find(func(rec Record) bool { return rec.ID == id })
}

func (s *Store) find(match func(Record) bool) (Record, error) {
	recs, err := s.records()
	if err != nil {
		return Record{}, err
	}
	for i := range recs {
		if match(recs[i]) {
			return recs[i], nil
		}
	}
	return Record{}, ErrUnknown
}

// List returns every environment sorted by name, [] when there are none.
func (s *Store) List() ([]Record, error) {
	return s.records()
}

// Enroll exchanges an enrollment secret for a certificate. The secret is
// compared with every record's hash in constant time, under the lock, and
// a pending match is signed and recorded as enrolled.
func (s *Store) Enroll(ctx context.Context, secret string, csrPEM []byte, broker, serverName string) (envproto.EnrollResponse, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	defer unlock()
	rec, err := s.matchSecret(secret)
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	cert, err := s.signRecord(&rec, csrPEM)
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	roots, err := s.ca.trust()
	if err != nil {
		return envproto.EnrollResponse{}, err
	}
	return envproto.EnrollResponse{
		ID:         rec.ID,
		Name:       rec.Name,
		Broker:     broker,
		ServerName: serverName,
		Cert:       string(encodeCert(cert)),
		CABundle:   string(roots.bundlePEM()),
	}, nil
}

// matchSecret returns the pending record whose code is secret. It returns
// ErrEnrollmentUsed for an enrolled match and ErrEnrollmentFailed for none.
func (s *Store) matchSecret(secret string) (Record, error) {
	recs, err := s.records()
	if err != nil {
		return Record{}, err
	}
	hash := []byte(envproto.HashSecret(secret))
	var match *Record
	for i := range recs {
		if subtle.ConstantTimeCompare(hash, []byte(recs[i].SecretHash)) == 1 {
			match = &recs[i]
		}
	}
	if match == nil {
		return Record{}, ErrEnrollmentFailed
	}
	if match.State == StateEnrolled {
		return Record{}, ErrEnrollmentUsed
	}
	return *match, nil
}

// signRecord issues rec's certificate from csrPEM and records it as enrolled.
func (s *Store) signRecord(rec *Record, csrPEM []byte) (*x509.Certificate, error) {
	cert, err := s.ca.signClient(csrPEM, rec.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEnrollmentFailed, err)
	}
	fingerprint, err := s.currentFingerprint()
	if err != nil {
		return nil, err
	}
	rec.State = StateEnrolled
	if rec.EnrolledAt == "" {
		rec.EnrolledAt = s.timestamp()
	}
	rec.CertSerial = cert.SerialNumber.Text(16)
	rec.CertNotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	rec.CertCA = fingerprint
	if err := s.writeRecord(rec); err != nil {
		return nil, err
	}
	return cert, nil
}

// Renew signs csrPEM for the enrolled environment id, keeping CN = id.
func (s *Store) Renew(ctx context.Context, id string, csrPEM []byte) (envproto.RenewResult, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return envproto.RenewResult{}, err
	}
	defer unlock()
	rec, err := s.ByID(id)
	if err != nil {
		return envproto.RenewResult{}, err
	}
	if rec.State != StateEnrolled {
		return envproto.RenewResult{}, ErrUnknown
	}
	cert, err := s.signRecord(&rec, csrPEM)
	if err != nil {
		return envproto.RenewResult{}, err
	}
	roots, err := s.ca.trust()
	if err != nil {
		return envproto.RenewResult{}, err
	}
	return envproto.RenewResult{
		Cert:     string(encodeCert(cert)),
		CABundle: string(roots.bundlePEM()),
	}, nil
}

// WriteSnapshot stores the controller's snapshot of the environment called name.
func (s *Store) WriteSnapshot(name string, data []byte) error {
	if !validName(name) {
		return ErrNameInvalid
	}
	if err := os.MkdirAll(s.snapshotDir(name), 0o700); err != nil {
		return fmt.Errorf("creating the snapshot directory of %s: %w", name, err)
	}
	return writeFileAtomic(filepath.Join(s.snapshotDir(name), snapshotFile), data, 0o600)
}

// ReadSnapshot returns the snapshot of name. fs.ErrNotExist when there is none.
func (s *Store) ReadSnapshot(name string) ([]byte, error) {
	if !validName(name) {
		return nil, ErrNameInvalid
	}
	data, err := os.ReadFile(filepath.Join(s.snapshotDir(name), snapshotFile))
	if err != nil {
		return nil, fmt.Errorf("reading the snapshot of %s: %w", name, err)
	}
	return data, nil
}

func (s *Store) caExists() (bool, error) {
	return s.ca.exists()
}

func (s *Store) trust() (trust, error) {
	return s.ca.trust()
}

func (s *Store) serverCertificate(publicName string) (tls.Certificate, error) {
	return s.ca.serverCertificate(publicName)
}

// lock takes the environments lock, which every read-modify-write holds.
func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", s.dir, err)
	}
	unlock, err := lockfile.Exclusive(ctx, filepath.Join(s.dir, lockFile))
	if err != nil {
		return nil, fmt.Errorf("locking environments: %w", err)
	}
	return unlock, nil
}

// records reads every record, sorted by name, [] when there are none.
func (s *Store) records() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.dir, err)
	}
	recs := []Record{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), recordSuffix) {
			continue
		}
		rec, err := readRecord(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	slices.SortFunc(recs, func(a, b Record) int { return strings.Compare(a.Name, b.Name) })
	return recs, nil
}

func (s *Store) recordPath(id string) string {
	return filepath.Join(s.dir, id+recordSuffix)
}

func (s *Store) snapshotDir(name string) string {
	return filepath.Join(s.stateDir, ".environments", name)
}

func (s *Store) writeRecord(rec *Record) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(rec); err != nil {
		return fmt.Errorf("encoding environment %s: %w", rec.ID, err)
	}
	return writeFileAtomic(s.recordPath(rec.ID), buf.Bytes(), 0o600)
}

// newID returns an "env-" id that no record uses yet.
func (s *Store) newID() (string, error) {
	for {
		buf := make([]byte, idBytes)
		if _, err := io.ReadFull(s.random, buf); err != nil {
			return "", fmt.Errorf("reading environment id: %w", err)
		}
		id := "env-" + hex.EncodeToString(buf)
		_, err := os.Stat(s.recordPath(id))
		if errors.Is(err, fs.ErrNotExist) {
			return id, nil
		}
		if err != nil {
			return "", fmt.Errorf("checking environment id %s: %w", id, err)
		}
	}
}

// currentCert is the current CA certificate, or ErrNoCA.
func (s *Store) currentCert() (*x509.Certificate, error) {
	cert, err := s.ca.currentCert()
	if errors.Is(err, errNoCA) {
		return nil, ErrNoCA
	}
	return cert, err
}

func (s *Store) currentFingerprint() (string, error) {
	cert, err := s.currentCert()
	if err != nil {
		return "", err
	}
	return envproto.Fingerprint(cert), nil
}

func (s *Store) timestamp() string {
	return s.now().UTC().Format(time.RFC3339)
}

// validName reports whether name meets the environment name rule.
func validName(name string) bool {
	return namePattern.MatchString(name) && name != "local"
}

// readRecord reads and checks the record at path.
func readRecord(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return decodeRecord(data, path)
}

// decodeRecord parses a record. Unknown keys, a version other than 1 and an
// unknown state are errors.
func decodeRecord(data []byte, path string) (Record, error) {
	var rec Record
	meta, err := toml.Decode(string(data), &rec)
	if err != nil {
		return Record{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if keys := meta.Undecoded(); len(keys) > 0 {
		return Record{}, fmt.Errorf("parsing %s: unknown key %s", path, keys[0])
	}
	if rec.Version != recordVersion {
		return Record{}, fmt.Errorf("parsing %s: unsupported version %d", path, rec.Version)
	}
	if rec.State != StatePending && rec.State != StateEnrolled {
		return Record{}, fmt.Errorf("parsing %s: unknown state %q", path, rec.State)
	}
	return rec, nil
}
