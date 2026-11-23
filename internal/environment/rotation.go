package environment

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// CA is one root as ws env ca shows it.
type CA struct {
	Fingerprint string
	NotAfter    time.Time
}

// CAStatus is the state of the environments CA.
type CAStatus struct {
	Current   *CA      // nil without a CA
	Old       *CA      // nil outside a rotation
	OnCurrent int      // enrolled environments whose cert_ca is the current CA
	OnOld     int      // enrolled environments whose cert_ca is the old CA
	Warnings  []string // caExpiryWarning of the current CA
}

// CARotation is the result of a CA rotation or of finishing one.
type CARotation struct {
	Current  *CA
	Old      *CA
	Dropped  *CA      // the root no longer trusted, or nil
	Stranded []string // names of enrolled environments whose cert_ca is Dropped
}

// EnsureCA finishes a CA rotation that an interruption left half done, and
// reports whether it did. Such a rotation leaves ca.crt absent until the
// pending CA is installed, so CA reads and rotations call this first. It
// never creates a CA: without a CA or a pending one it does nothing.
func (s *Store) EnsureCA(ctx context.Context) (bool, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	exists, err := s.caExists()
	if err != nil || exists {
		return false, err
	}
	pending, err := s.ca.hasPendingCert()
	if err != nil || !pending {
		return false, err
	}
	if _, err := s.ca.ensure(); err != nil {
		return false, err
	}
	return true, nil
}

// CAStatus reports the current and old CA and how many environments hold
// certificates from each.
func (s *Store) CAStatus(renewBeforeDays int) (CAStatus, error) {
	status := CAStatus{Warnings: []string{}}
	exists, err := s.caExists()
	if err != nil || !exists {
		return status, err
	}
	current, err := s.currentCert()
	if err != nil {
		return CAStatus{}, err
	}
	old, err := s.oldCert()
	if err != nil {
		return CAStatus{}, err
	}
	recs, err := s.records()
	if err != nil {
		return CAStatus{}, err
	}
	status.Current = caSummary(current)
	status.Old = caSummary(old)
	status.OnCurrent = len(namesOn(recs, status.Current.Fingerprint))
	if status.Old != nil {
		status.OnOld = len(namesOn(recs, status.Old.Fingerprint))
	}
	if warning := caExpiryWarning(current, s.now(), renewBeforeDays); warning != "" {
		status.Warnings = append(status.Warnings, warning)
	}
	return status, nil
}

// RotateCA makes a new CA. The previous one becomes the old CA and stays
// trusted until FinishCA.
func (s *Store) RotateCA(ctx context.Context) (CARotation, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return CARotation{}, err
	}
	defer unlock()
	if _, err = s.currentCert(); err != nil {
		return CARotation{}, err
	}
	current, previous, dropped, err := s.ca.rotate()
	if err != nil {
		return CARotation{}, err
	}
	result := CARotation{Current: caSummary(current), Old: caSummary(previous), Stranded: []string{}}
	if dropped == nil {
		return result, nil
	}
	result.Dropped = caSummary(dropped)
	recs, err := s.records()
	if err != nil {
		return CARotation{}, err
	}
	result.Stranded = namesOn(recs, envproto.Fingerprint(dropped))
	return result, nil
}

// FinishCA drops the old CA. While environments still hold certificates
// from it, ErrCAInUse is returned unless force is set, and those
// environments are listed in Stranded.
func (s *Store) FinishCA(ctx context.Context, force bool) (CARotation, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return CARotation{}, err
	}
	defer unlock()
	old, err := s.oldCert()
	if err != nil {
		return CARotation{}, err
	}
	if old == nil {
		return CARotation{}, ErrNoRotation
	}
	recs, err := s.records()
	if err != nil {
		return CARotation{}, err
	}
	stranded := namesOn(recs, envproto.Fingerprint(old))
	if len(stranded) > 0 && !force {
		return CARotation{Stranded: stranded}, fmt.Errorf("%w: %d environments", ErrCAInUse, len(stranded))
	}
	current, err := s.currentCert()
	if err != nil {
		return CARotation{}, err
	}
	dropped, err := s.ca.dropOld()
	if err != nil {
		return CARotation{}, err
	}
	return CARotation{Current: caSummary(current), Dropped: caSummary(dropped), Stranded: stranded}, nil
}

// oldCert is the old CA certificate, or nil outside a rotation.
func (s *Store) oldCert() (*x509.Certificate, error) {
	old, _, err := s.ca.optionalCert(oldCertFile)
	return old, err
}

// namesOn returns the names of enrolled environments whose certificate was
// issued by the CA with fingerprint, never nil.
func namesOn(recs []Record, fingerprint string) []string {
	names := []string{}
	for i := range recs {
		if recs[i].State == StateEnrolled && recs[i].CertCA == fingerprint {
			names = append(names, recs[i].Name)
		}
	}
	return names
}

// caSummary describes cert as a CA, or is nil for a nil cert.
func caSummary(cert *x509.Certificate) *CA {
	if cert == nil {
		return nil
	}
	return &CA{Fingerprint: envproto.Fingerprint(cert), NotAfter: cert.NotAfter}
}
