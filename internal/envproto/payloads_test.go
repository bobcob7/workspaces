package envproto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfflinePayload(t *testing.T) {
	t.Run("is exactly the offline status", func(t *testing.T) {
		assert.Equal(t, []byte(`{"status":"offline"}`), OfflinePayload())
	})
}

func TestOKReply(t *testing.T) {
	t.Run("marshals the result with a null error", func(t *testing.T) {
		body, err := OKReply(map[string]string{"cert": "pem"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true,"result":{"cert":"pem"},"error":null}`, string(body))
	})
	t.Run("marshals a nil result as null", func(t *testing.T) {
		body, err := OKReply(nil)
		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true,"result":null,"error":null}`, string(body))
	})
}

func TestErrorReply(t *testing.T) {
	t.Run("marshals the error with null result and details", func(t *testing.T) {
		assert.JSONEq(t,
			`{"ok":false,"result":null,"error":{"code":"environment_unknown","message":"no such environment","details":null}}`,
			string(ErrorReply("environment_unknown", "no such environment")),
		)
	})
}

func TestHello_MarshalJSON(t *testing.T) {
	t.Run("uses snake_case keys and null for an absent version", func(t *testing.T) {
		body, err := json.Marshal(Hello{Status: StatusOnline, Claude: ClaudeMissing, CAFingerprints: []string{}})
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(body, &fields))
		assert.Equal(t, "online", fields["status"])
		assert.Equal(t, "missing", fields["claude"])
		assert.Contains(t, fields, "claude_version")
		assert.Nil(t, fields["claude_version"])
		assert.Contains(t, fields, "cert_not_after")
		assert.Contains(t, fields, "ca_fingerprints")
	})
}

func TestRequest_Unmarshal(t *testing.T) {
	t.Run("reads op and args", func(t *testing.T) {
		var req Request
		require.NoError(t, json.Unmarshal([]byte(`{"op":"cert.renew","args":{"csr":"x"}}`), &req))
		assert.Equal(t, OpCertRenew, req.Op)
		assert.JSONEq(t, `{"csr":"x"}`, string(req.Args))
	})
}

func TestRenewArgs_Unmarshal(t *testing.T) {
	t.Run("reads the csr", func(t *testing.T) {
		var args RenewArgs
		require.NoError(t, json.Unmarshal([]byte(`{"csr":"pem"}`), &args))
		assert.Equal(t, "pem", args.CSR)
	})
}

func TestErrorEnvelope_Marshal(t *testing.T) {
	t.Run("wraps the error under error", func(t *testing.T) {
		body, err := json.Marshal(ErrorEnvelope{Error: ReplyError{Code: "enrollment_used", Message: "used"}})
		require.NoError(t, err)
		assert.JSONEq(t, `{"error":{"code":"enrollment_used","message":"used","details":null}}`, string(body))
	})
}
