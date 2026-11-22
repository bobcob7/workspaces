package envproto

import (
	"encoding/json"
	"time"
)

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

// MaxRequestBytes caps a request body on the enrollment listener.
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
}

// OfflinePayload is the Last Will: exactly {"status":"offline"}.
func OfflinePayload() []byte {
	return []byte(`{"status":"` + StatusOffline + `"}`)
}

// Snapshot is what an agent publishes on env/<id>/snapshot.
type Snapshot struct {
	At         time.Time       `json:"at"`
	Workspaces json.RawMessage `json:"workspaces"` // ws list --json's "workspaces" array
}

// Request is the body of a request on env/<id>/req or env/<id>/up.
type Request struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
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
	// The value holds only strings and nil pointers and maps, so Marshal
	// cannot fail.
	body, _ := json.Marshal(Reply{
		OK:    false,
		Error: &ReplyError{Code: code, Message: message},
	})
	return body
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
