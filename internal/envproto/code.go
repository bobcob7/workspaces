package envproto

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// CodePrefix starts every enrollment code.
const CodePrefix = "ws-enroll-v1."

// ErrCodeInvalid is returned for an enrollment code that doesn't parse.
var ErrCodeInvalid = errors.New("invalid enrollment code")

// Code is the decoded enrollment code (ADR 0075).
type Code struct {
	Host        string `json:"h"` // host:port of the enrollment listener
	Fingerprint string `json:"f"` // the CA's Fingerprint
	Secret      string `json:"s"` // NewSecret's output
}

// String is CodePrefix + base64.RawURLEncoding of the JSON {"h","f","s"}.
func (c Code) String() string {
	// The struct holds only strings, so Marshal cannot fail.
	body, _ := json.Marshal(c)
	return CodePrefix + base64.RawURLEncoding.EncodeToString(body)
}

// ParseCode trims spaces and checks the prefix, base64url, JSON (unknown
// keys refused), that h splits into host and a port 1-65535, f is 64 hex,
// and s decodes (RawURLEncoding) to 16 bytes; else ErrCodeInvalid wrapped
// with the reason.
func ParseCode(s string) (Code, error) {
	body, found := strings.CutPrefix(strings.TrimSpace(s), CodePrefix)
	if !found {
		return Code{}, fmt.Errorf("%w: no %s prefix", ErrCodeInvalid, CodePrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return Code{}, fmt.Errorf("%w: base64: %w", ErrCodeInvalid, err)
	}
	c, err := decodeCodeJSON(raw)
	if err != nil {
		return Code{}, err
	}
	if err := checkCodeFields(c); err != nil {
		return Code{}, err
	}
	return c, nil
}

func decodeCodeJSON(raw []byte) (Code, error) {
	var c Code
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Code{}, fmt.Errorf("%w: json: %w", ErrCodeInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Code{}, fmt.Errorf("%w: json: trailing data", ErrCodeInvalid)
	}
	return c, nil
}

func checkCodeFields(c Code) error {
	if err := checkHost(c.Host); err != nil {
		return err
	}
	if _, err := hex.DecodeString(c.Fingerprint); err != nil || len(c.Fingerprint) != sha256.Size*2 {
		return fmt.Errorf("%w: fingerprint is not 64 hex characters", ErrCodeInvalid)
	}
	secret, err := base64.RawURLEncoding.DecodeString(c.Secret)
	if err != nil || len(secret) != secretBytes {
		return fmt.Errorf("%w: secret is not %d bytes", ErrCodeInvalid, secretBytes)
	}
	return nil
}

func checkHost(hostport string) error {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return fmt.Errorf("%w: host: %w", ErrCodeInvalid, err)
	}
	n, err := strconv.Atoi(port)
	if host == "" || err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%w: host %q needs a host and a port 1-65535", ErrCodeInvalid, hostport)
	}
	return nil
}

const secretBytes = 16

// NewSecret reads 16 bytes from random and returns them RawURLEncoded (22 chars).
func NewSecret(random io.Reader) (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", fmt.Errorf("reading enrollment secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSecret is the lowercase hex SHA-256 of secret's bytes.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
