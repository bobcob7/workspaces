//go:build unix

package privfile

// RestrictDir does nothing on unix: the caller's 0700 mode already applies.
func RestrictDir(string) error { return nil }

// RestrictFile does nothing on unix: the caller's 0600 mode already applies.
func RestrictFile(string) error { return nil }

// CheckPrivate does nothing on unix: the mode bits are the check there.
func CheckPrivate(string) error { return nil }
