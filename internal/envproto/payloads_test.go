package envproto

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfflinePayload(t *testing.T) {
	t.Run("is exactly the offline status", func(t *testing.T) {
		assert.JSONEq(t, `{"status":"offline"}`, string(OfflinePayload()))
	})
}

func TestReplyError_Error(t *testing.T) {
	t.Run("is the message the reply carries", func(t *testing.T) {
		var err error = &ReplyError{Code: "usage", Message: "a name is required"}
		assert.EqualError(t, err, "a name is required")
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

func TestHello_Conn(t *testing.T) {
	t.Run("is the conn key, and is left out when empty", func(t *testing.T) {
		body, err := json.Marshal(Hello{Status: StatusOnline, Conn: "abc"})
		require.NoError(t, err)
		assert.Contains(t, string(body), `"conn":"abc"`)
		body, err = json.Marshal(Hello{Status: StatusOnline})
		require.NoError(t, err)
		assert.NotContains(t, string(body), "conn")
	})
	t.Run("an older agent's hello decodes with no connection id", func(t *testing.T) {
		var hello Hello
		require.NoError(t, json.Unmarshal([]byte(`{"status":"online","protocol":3}`), &hello))
		assert.Empty(t, hello.Conn)
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

func TestSnapshot_Marshal(t *testing.T) {
	t.Run("marshals the protocol and an empty agents list", func(t *testing.T) {
		at := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
		body, err := json.Marshal(Snapshot{At: at, Protocol: Protocol, Workspaces: json.RawMessage(`[]`), Agents: []SnapshotAgent{}, Loops: []SnapshotLoop{}})
		require.NoError(t, err)
		assert.JSONEq(t, fmt.Sprintf(`{"at":"2026-11-22T00:00:00Z","protocol":%d,"workspaces":[],"agents":[],"loops":[]}`, Protocol), string(body))
	})
}

func TestRequest_Marshal(t *testing.T) {
	t.Run("marshals a null deadline and keeps the request id", func(t *testing.T) {
		body, err := json.Marshal(Request{Op: OpCAUpdate, Args: json.RawMessage(`{}`), RequestID: "0123456789abcdef"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"op":"ca.update","args":{},"request_id":"0123456789abcdef","deadline":null}`, string(body))
	})
	t.Run("round-trips the request id and the deadline", func(t *testing.T) {
		deadline := time.Date(2026, 11, 22, 1, 2, 3, 0, time.UTC)
		body, err := json.Marshal(Request{Op: OpAgentStatus, Args: json.RawMessage(`{"workspace":"a"}`), RequestID: "fedcba9876543210", Deadline: &deadline})
		require.NoError(t, err)
		var got Request
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, "fedcba9876543210", got.RequestID)
		require.NotNil(t, got.Deadline)
		assert.True(t, deadline.Equal(*got.Deadline))
	})
	t.Run("a PR 2 request has an empty request id and a nil deadline", func(t *testing.T) {
		var req Request
		require.NoError(t, json.Unmarshal([]byte(`{"op":"cert.renew","args":{}}`), &req))
		assert.Empty(t, req.RequestID)
		assert.Nil(t, req.Deadline)
	})
}

func TestProtocol_reviewsAndLoops(t *testing.T) {
	t.Run("is 3, the protocol review and loop operations need", func(t *testing.T) {
		assert.Equal(t, 3, ProtocolReviewsLoops)
		assert.GreaterOrEqual(t, Protocol, ProtocolReviewsLoops)
	})
}

func TestHello_Protocol(t *testing.T) {
	t.Run("marshals the protocol number", func(t *testing.T) {
		body, err := json.Marshal(Hello{Status: StatusOnline, Protocol: Protocol, CAFingerprints: []string{}})
		require.NoError(t, err)
		assert.Contains(t, string(body), fmt.Sprintf(`"protocol":%d`, Protocol))
	})
	t.Run("reads 0 from an agent older than PR 3", func(t *testing.T) {
		var hello Hello
		require.NoError(t, json.Unmarshal([]byte(`{"status":"online"}`), &hello))
		assert.Equal(t, 0, hello.Protocol)
	})
}
