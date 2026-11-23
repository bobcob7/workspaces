package environment

import (
	"errors"
	"fmt"
	"time"
)

// Why an OfflineError happened.
const (
	reasonOffline      = "offline"
	reasonNotListening = "not_listening"
	reasonTimeout      = "timeout"
)

var (
	// ErrUnknown is returned for an environment that doesn't exist.
	ErrUnknown = errors.New("no such environment")
	// ErrExists is returned when an environment name is taken.
	ErrExists = errors.New("an environment with that name exists")
	// ErrNameInvalid is returned for a name the rules refuse.
	ErrNameInvalid = errors.New(`an environment name is 1-32 of a-z, 0-9 and "-", starts with a letter, and isn't "local"`)
	// ErrEnrollmentUsed is returned when an enrollment code was already used.
	ErrEnrollmentUsed = errors.New("that code was already used")
	// ErrEnrollmentFailed is returned when an enrollment code or CSR is refused.
	ErrEnrollmentFailed = errors.New("enrollment failed")
	// ErrCAInUse is returned when environments still hold old-CA certificates.
	ErrCAInUse = errors.New("environments still hold certificates from the old CA")
	// ErrNoCA is returned when there is no environments CA yet.
	ErrNoCA = errors.New("there's no environments CA yet; run ws env add first")
	// ErrNoRotation is returned when there is no CA rotation to finish.
	ErrNoRotation = errors.New("there's no CA rotation to finish")
	// ErrOffline is returned when an environment isn't online, the controller
	// isn't listening, or a call got no reply in time.
	ErrOffline = errors.New("the environment is offline")
	// ErrNotForwarded is returned for an operation the controller doesn't send for a caller.
	ErrNotForwarded = errors.New("operation not forwarded")
	// ErrRequestPending is returned when a call's request id is already waiting for a reply.
	ErrRequestPending = errors.New("the request id is already pending")
)

// OutdatedError is an environment whose agent speaks too old a protocol for an op.
type OutdatedError struct {
	Environment string
	Op          string
	WSVersion   string
	Protocol    int
	Need        int
}

// Error names the environment, its ws and protocol, and what the operation needs.
func (e *OutdatedError) Error() string {
	return fmt.Sprintf("environment %s runs ws %s, which speaks protocol %d; %s needs protocol %d",
		e.Environment, e.WSVersion, e.Protocol, e.Op, e.Need)
}

// OfflineError wraps ErrOffline with why: "offline", "not_listening" or "timeout".
type OfflineError struct {
	Environment string
	Reason      string
	LastSeen    *time.Time
	RequestID   string
	Op          string
}

// Error says why the environment couldn't be reached.
func (e *OfflineError) Error() string {
	switch e.Reason {
	case reasonNotListening:
		return fmt.Sprintf("environment %s is offline: the controller isn't listening", e.Environment)
	case reasonTimeout:
		return fmt.Sprintf("environment %s didn't reply to %s in time", e.Environment, e.Op)
	}
	return fmt.Sprintf("environment %s is offline", e.Environment)
}

// Unwrap returns ErrOffline.
func (*OfflineError) Unwrap() error {
	return ErrOffline
}
