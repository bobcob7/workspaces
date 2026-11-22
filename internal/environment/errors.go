package environment

import "errors"

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
)
