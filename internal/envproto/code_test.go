package envproto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testSecret(t *testing.T) string {
	t.Helper()
	secret, err := NewSecret(bytes.NewReader(make([]byte, 16)))
	require.NoError(t, err)
	return secret
}

// encodeCode builds a code from raw JSON, bypassing Code.String.
func encodeCode(raw string) string {
	return CodePrefix + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func TestCode_String(t *testing.T) {
	t.Run("round-trips through ParseCode", func(t *testing.T) {
		want := Code{Host: "ws.example:4443", Fingerprint: testFingerprint, Secret: testSecret(t)}
		got, err := ParseCode(want.String())
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
	t.Run("starts with the prefix", func(t *testing.T) {
		code := Code{Host: "h:1", Fingerprint: testFingerprint, Secret: testSecret(t)}
		assert.True(t, strings.HasPrefix(code.String(), CodePrefix))
	})
}

func TestParseCode(t *testing.T) {
	valid := Code{Host: "ws.example:4443", Fingerprint: testFingerprint, Secret: testSecret(t)}

	t.Run("trims surrounding spaces", func(t *testing.T) {
		got, err := ParseCode("  " + valid.String() + "\n")
		require.NoError(t, err)
		assert.Equal(t, valid, got)
	})
	t.Run("refuses each malformed form", func(t *testing.T) {
		body, err := json.Marshal(valid)
		require.NoError(t, err)
		short := Code{Host: valid.Host, Fingerprint: testFingerprint[:63], Secret: valid.Secret}
		shortJSON, err := json.Marshal(short)
		require.NoError(t, err)
		badSecret := Code{Host: valid.Host, Fingerprint: testFingerprint, Secret: base64.RawURLEncoding.EncodeToString(make([]byte, 15))}
		badSecretJSON, err := json.Marshal(badSecret)
		require.NoError(t, err)
		for name, input := range map[string]string{
			"no prefix":           strings.TrimPrefix(valid.String(), CodePrefix),
			"bad base64":          CodePrefix + "!!!",
			"bad json":            encodeCode(`{"h":`),
			"unknown key":         encodeCode(`{"h":"ws.example:4443","f":"` + testFingerprint + `","s":"` + valid.Secret + `","x":1}`),
			"trailing data":       encodeCode(string(body) + " {}"),
			"no port":             encodeCode(`{"h":"ws.example","f":"` + testFingerprint + `","s":"` + valid.Secret + `"}`),
			"port zero":           encodeCode(`{"h":"ws.example:0","f":"` + testFingerprint + `","s":"` + valid.Secret + `"}`),
			"port too high":       encodeCode(`{"h":"ws.example:65536","f":"` + testFingerprint + `","s":"` + valid.Secret + `"}`),
			"no host":             encodeCode(`{"h":":4443","f":"` + testFingerprint + `","s":"` + valid.Secret + `"}`),
			"short fingerprint":   string(shortJSON),
			"secret not 16 bytes": string(badSecretJSON),
		} {
			t.Run(name, func(t *testing.T) {
				_, err := ParseCode(input)
				assert.True(t, errors.Is(err, ErrCodeInvalid), "%v", err)
			})
		}
	})
}

func TestNewSecret(t *testing.T) {
	t.Run("encodes 16 random bytes as 22 url-safe characters", func(t *testing.T) {
		secret, err := NewSecret(bytes.NewReader(make([]byte, 16)))
		require.NoError(t, err)
		assert.Len(t, secret, 22)
		raw, err := base64.RawURLEncoding.DecodeString(secret)
		require.NoError(t, err)
		assert.Len(t, raw, 16)
	})
	t.Run("errors when the source is short", func(t *testing.T) {
		_, err := NewSecret(bytes.NewReader(make([]byte, 8)))
		assert.Error(t, err)
	})
}

func TestHashSecret(t *testing.T) {
	t.Run("is 64 lowercase hex characters", func(t *testing.T) {
		got := HashSecret("secret")
		assert.Len(t, got, 64)
		assert.Equal(t, strings.ToLower(got), got)
	})
	t.Run("is stable for the same secret and differs otherwise", func(t *testing.T) {
		assert.Equal(t, HashSecret("a"), HashSecret("a"))
		assert.NotEqual(t, HashSecret("a"), HashSecret("b"))
	})
}
