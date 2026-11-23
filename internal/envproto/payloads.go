package envproto

import (
	"encoding/json"
	"time"
)

// Protocol is the newest operation protocol this ws speaks (ADR 0073). Reviews and loops make it 3.
const Protocol = 3

// ProtocolReviewsLoops is the protocol review.* and loop.* need (ADR 0078).
const ProtocolReviewsLoops = 3

// Statuses an agent reports in its hello and its last will.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
)

// Claude states an agent reports in its hello.
const (
	ClaudeMissing   = "missing"
	ClaudePresent   = "present"
	ClaudeSignedOut = "signed_out"
)

// Operations on env/<id>/req, env/<id>/up and their replies.
const (
	OpCertRenew = "cert.renew"
	OpCAUpdate  = "ca.update"
)

// EnrollPath is the enrollment listener's route.
const EnrollPath = "/v1/enroll"

// MaxRequestBytes is the general payload ceiling: it caps enrollment bodies,
// daemon call bodies, MQTT broker packets, and snapshot and reply sizes.
const MaxRequestBytes = 256 << 10

// Hello is the retained status an agent publishes on connect (ADR 0073).
type Hello struct {
	Status         string    `json:"status"`
	At             time.Time `json:"at"`
	WSVersion      string    `json:"ws_version"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	Claude         string    `json:"claude"`
	ClaudeVersion  *string   `json:"claude_version"`
	CertSerial     string    `json:"cert_serial"`
	CertNotAfter   time.Time `json:"cert_not_after"`
	CAFingerprints []string  `json:"ca_fingerprints"`
	Protocol       int       `json:"protocol"` // 0 from an agent older than PR 3
	// Conn is a random id the agent makes on each connect and repeats in every
	// hello of that connection. A controller that sees it change knows the
	// agent's terminals died with the old connection. Empty from an older agent.
	Conn string `json:"conn,omitempty"`
}

// OfflinePayload is the Last Will: exactly {"status":"offline"}.
func OfflinePayload() []byte {
	return []byte(`{"status":"` + StatusOffline + `"}`)
}

// Snapshot is what an agent publishes on env/<id>/snapshot. Callers set
// Agents to an empty slice, never nil, so the JSON is [].
type Snapshot struct {
	At         time.Time       `json:"at"`
	Protocol   int             `json:"protocol"`
	Workspaces json.RawMessage `json:"workspaces"` // ws list --json --all's array, no unknown dirs
	Agents     []SnapshotAgent `json:"agents"`
	// Loops is the agent's loops (ADR 0078). No omitempty: an agent with no
	// loops sends [], and a snapshot without the key (an older agent) is null.
	Loops []SnapshotLoop `json:"loops"`
}

// SnapshotAgent is one Claude Code session on the agent's machine.
type SnapshotAgent struct {
	ID        string     `json:"id"`
	SessionID string     `json:"session_id"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Status    string     `json:"status"`
	PID       *int       `json:"pid"`
	StartedAt *time.Time `json:"started_at"`
}

// Request is the body of a request on env/<id>/req or env/<id>/up.
type Request struct {
	Op        string          `json:"op"`
	Args      json.RawMessage `json:"args"`
	RequestID string          `json:"request_id"` // ValidRequestID; "" only from a PR 2 controller
	Deadline  *time.Time      `json:"deadline"`   // null for ca.update
}

// Reply is the body of a reply on env/<id>/res/<req> or the response topic.
type Reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *ReplyError     `json:"error"`
}

// ReplyError is the error part of a Reply, and of ErrorEnvelope.
type ReplyError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// Error returns the message the reply carries, so an operation can fail with a ReplyError.
func (e *ReplyError) Error() string {
	return e.Message
}

// OKReply marshals {ok: true, result, error: null}.
func OKReply(result any) ([]byte, error) {
	return json.Marshal(struct {
		OK     bool        `json:"ok"`
		Result any         `json:"result"`
		Error  *ReplyError `json:"error"`
	}{OK: true, Result: result})
}

// ErrorReply marshals {ok: false, result: null, error: {code, message, details: null}}.
func ErrorReply(code, message string) []byte {
	body, err := json.Marshal(errorReplyBody{
		OK:    false,
		Error: errorReplyError{Code: code, Message: message},
	})
	if err != nil {
		return []byte(`{"ok":false,"result":null,"error":null}`)
	}
	return body
}

// errorReplyBody is ErrorReply's JSON: the same shape as Reply, with a
// null result and details.
type errorReplyBody struct {
	OK     bool            `json:"ok"`
	Result *string         `json:"result"`
	Error  errorReplyError `json:"error"`
}

type errorReplyError struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Details *string `json:"details"`
}

// RenewArgs are the arguments of cert.renew.
type RenewArgs struct {
	CSR string `json:"csr"` // PEM "CERTIFICATE REQUEST"
}

// RenewResult is the result of cert.renew.
type RenewResult struct {
	Cert     string `json:"cert"`      // PEM
	CABundle string `json:"ca_bundle"` // PEM, every trusted root, current first
}

// CAUpdateArgs are the arguments of ca.update.
type CAUpdateArgs struct {
	CABundle string `json:"ca_bundle"`
}

// CAUpdateResult is the result of ca.update.
type CAUpdateResult struct {
	Fingerprints []string `json:"fingerprints"`
}

// EnrollRequest is POST /v1/enroll's body.
type EnrollRequest struct {
	Secret string `json:"secret"`
	CSR    string `json:"csr"`
}

// EnrollResponse is its 200 answer.
type EnrollResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Broker     string `json:"broker"`      // host:port agents dial (public_name and listen's port)
	ServerName string `json:"server_name"` // public_name
	Cert       string `json:"cert"`
	CABundle   string `json:"ca_bundle"`
}

// ErrorEnvelope is every non-200 answer: {"error": {code, message, details}}.
type ErrorEnvelope struct {
	Error ReplyError `json:"error"`
}
