package runner

import (
	"crypto/rand"
	"encoding/hex"
)

// NewSessionID returns "rn-" and 8 random lowercase hex digits.
func NewSessionID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf) // crypto/rand.Read never fails (Go 1.24+)
	return SessionPrefix + hex.EncodeToString(buf)
}
