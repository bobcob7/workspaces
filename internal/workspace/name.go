package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxIDLen is the longest id a --name produces (ADR 0012).
const maxIDLen = 64

// nameSuffixBytes is how many random bytes a --name id ends with, as hex
// (ADR 0036).
const nameSuffixBytes = 4

// maxBaseLen is the longest sanitized --name, leaving room for "-" and the
// suffix within maxIDLen.
const maxBaseLen = maxIDLen - 1 - 2*nameSuffixBytes

// idAlphabet is what a generated id's random suffix is drawn from.
const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// sanitizeName turns --name into an id (ADR 0012): fold accents with NFKD,
// keep lowercase ASCII letters and digits, replace every other run with one
// "-", trim "-" from both ends, and cut to 55 characters, so the id with
// its suffix fits in 64 (ADR 0036). The result may be empty.
func sanitizeName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(name) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(unicode.ToLower(r))
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > maxBaseLen {
		id = strings.TrimRight(id[:maxBaseLen], "-")
	}
	return id
}

// generateID makes ws-<YYYYMMDD>-<6 random [a-z0-9]> (ADR 0012).
func generateID(now time.Time, suffix string) string {
	return "ws-" + now.Format("20060102") + "-" + suffix
}

// randomSuffix draws six characters from idAlphabet.
func randomSuffix() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating a workspace id: %w", err)
	}
	for i, v := range buf {
		buf[i] = idAlphabet[int(v)%len(idAlphabet)]
	}
	return string(buf), nil
}

// namedID makes <sanitized --name>-<8 random hex> (ADR 0036).
func namedID(base, suffix string) string {
	return base + "-" + suffix
}

// randomHex draws nameSuffixBytes random bytes, as lowercase hex.
func randomHex() (string, error) {
	buf := make([]byte, nameSuffixBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating a workspace id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
