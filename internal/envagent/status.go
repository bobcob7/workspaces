package envagent

import (
	"errors"
	"time"
)

// AgentStatus is what ws env agent status prints.
type AgentStatus struct {
	Enrolled       bool
	ID, Name       string
	Broker         string
	CertSerial     string
	CertNotAfter   time.Time
	RenewAt        time.Time // CertNotAfter minus renewBeforeDays
	CAFingerprints []string
}

// StatusOf reads the identity in files. Without one it is {Enrolled: false}
// and no error.
func StatusOf(files *Files, renewBeforeDays int) (AgentStatus, error) {
	id, err := files.Load()
	if errors.Is(err, ErrNotEnrolled) {
		return AgentStatus{CAFingerprints: []string{}}, nil
	}
	if err != nil {
		return AgentStatus{}, err
	}
	return AgentStatus{
		Enrolled:       true,
		ID:             id.ID,
		Name:           id.Name,
		Broker:         id.Broker,
		CertSerial:     id.Cert.SerialNumber.Text(16),
		CertNotAfter:   id.Cert.NotAfter,
		RenewAt:        id.Cert.NotAfter.AddDate(0, 0, -renewBeforeDays),
		CAFingerprints: id.Fingerprints(),
	}, nil
}
