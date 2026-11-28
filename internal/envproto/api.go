package envproto

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

// The artifact API's routes and headers (ADR 0087).
const (
	ArtifactsPath       = "/v1/artifacts"
	HeaderWorkspace     = "Ws-Workspace"      // the calling workspace's id, url.QueryEscape'd
	HeaderWorkspaceName = "Ws-Workspace-Name" // its name, url.QueryEscape'd
	HeaderRoot          = "Ws-Root"           // its tree's root on the remote, url.QueryEscape'd
	HeaderRev           = "Ws-Rev"            // content: the revision served, decimal
	HeaderSHA256        = "Ws-Sha256"         // content: its SHA-256, 64 lowercase hex
	HeaderContentType   = "Ws-Content-Type"   // content: the stored content type
	MaxEndpointBytes    = 255
	// MaxAPIJSONBytes caps a JSON request body (comments) and a JSON answer the client reads.
	MaxAPIJSONBytes = 8 << 20
)

// Reasons in artifact_unreachable's details.reason (ADR 0087).
const (
	ReasonIdentity    = "identity"    // this machine's key, certificate or agent.toml can't be read
	ReasonNoEndpoint  = "no_endpoint" // no api.toml: the controller never sent api.update
	ReasonOffline     = "offline"     // the dial failed or timed out
	ReasonCertificate = "certificate" // the TLS handshake refused a certificate
	ReasonRefused     = "refused"     // 403 from the controller's identity check
)

// errEndpoint is wrapped by every CheckEndpoint refusal.
var errEndpoint = errors.New("invalid API endpoint")

// CheckEndpoint is nil for "host:port" with a non-empty host, a decimal
// port from 1 to 65535 and at most MaxEndpointBytes bytes; otherwise an
// error naming what is wrong.
func CheckEndpoint(s string) error {
	if len(s) > MaxEndpointBytes {
		return fmt.Errorf("%w: longer than %d bytes", errEndpoint, MaxEndpointBytes)
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return fmt.Errorf("%w: %q is not host:port", errEndpoint, s)
	}
	if host == "" {
		return fmt.Errorf("%w: %q has no host", errEndpoint, s)
	}
	if !isPortNumber(port) {
		return fmt.Errorf("%w: port %q is not 1 to 65535", errEndpoint, port)
	}
	return nil
}

// isPortNumber reports whether port is decimal digits naming 1 to 65535.
func isPortNumber(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// CommentAddBody is POST /v1/artifacts/{id}/threads' JSON body. Reply and
// Quote aren't both set; Line is 0 or at least 1 and needs Quote.
type CommentAddBody struct {
	Body  string `json:"body"`
	Reply string `json:"reply"`
	Quote string `json:"quote"`
	Line  int    `json:"line"`
}

// CommentResolveBody is POST /v1/artifacts/{id}/threads/{thread}/resolve's body.
type CommentResolveBody struct {
	Reopen bool `json:"reopen"`
}

// ArtifactRemoved is DELETE /v1/artifacts/{id}'s answer.
type ArtifactRemoved struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed"`
}
