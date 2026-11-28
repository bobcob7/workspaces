package envagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// The artifact API's client limits (ADR 0087).
const (
	apiFileName          = "api.toml"
	apiVersion           = 1
	apiDialTimeout       = 10 * time.Second
	apiHandshakeTimeout  = 10 * time.Second
	apiResponseTimeout   = 2 * time.Minute
	apiIdleConns         = 2
	apiKeyRetryWait      = 100 * time.Millisecond
	noEndpointMessage    = "this machine doesn't know the controller's artifact API yet: the controller sends it to ws env agent run when it connects, and only a controller running ws with protocol 5 or later has one; upgrade ws on the controller and check that ws env agent run is connected"
	identityErrorMessage = "can't read this machine's environment identity: %v; run ws env agent status"
)

// ErrNoAPIEndpoint is returned when api.toml is missing: the controller
// hasn't sent api.update (it may run an older ws).
var ErrNoAPIEndpoint = errors.New("this machine doesn't know the controller's artifact API yet")

// apiFile is api.toml.
type apiFile struct {
	Version   int    `toml:"version"`
	Endpoint  string `toml:"endpoint"`
	UpdatedAt string `toml:"updated_at"` // RFC 3339
}

// APIEndpoint reads api.toml's endpoint. ErrNoAPIEndpoint when the file is
// missing; an error for unknown keys, another version, or an endpoint
// envproto.CheckEndpoint refuses.
func (f *Files) APIEndpoint() (string, error) {
	data, err := os.ReadFile(f.path(apiFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoAPIEndpoint
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", apiFileName, err)
	}
	var file apiFile
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", apiFileName, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return "", fmt.Errorf("parsing %s: unknown key %s", apiFileName, undecoded[0])
	}
	if file.Version != apiVersion {
		return "", fmt.Errorf("parsing %s: version %d isn't %d", apiFileName, file.Version, apiVersion)
	}
	if err := envproto.CheckEndpoint(file.Endpoint); err != nil {
		return "", fmt.Errorf("parsing %s: %w", apiFileName, err)
	}
	return file.Endpoint, nil
}

// SaveAPIEndpoint checks endpoint and writes api.toml with writeFileAtomic (0600), with now as updated_at.
func (f *Files) SaveAPIEndpoint(endpoint string, now time.Time) error {
	if err := envproto.CheckEndpoint(endpoint); err != nil {
		return err
	}
	file := apiFile{Version: apiVersion, Endpoint: endpoint, UpdatedAt: now.UTC().Format(time.RFC3339)}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(file); err != nil {
		return fmt.Errorf("encoding %s: %w", apiFileName, err)
	}
	return writeFileAtomic(f.path(apiFileName), buf.Bytes())
}

// APIError is a request that never got an HTTP answer, with why.
type APIError struct {
	Reason   string // envproto.ReasonIdentity, ReasonNoEndpoint, ReasonOffline or ReasonCertificate
	Endpoint string // "" for identity and no_endpoint
	Err      error
}

// Error says what went wrong and what to do, as the agent's artifact_unreachable message.
func (e *APIError) Error() string {
	switch e.Reason {
	case envproto.ReasonIdentity:
		return fmt.Sprintf(identityErrorMessage, e.Err)
	case envproto.ReasonNoEndpoint:
		return noEndpointMessage
	case envproto.ReasonOffline:
		return fmt.Sprintf("can't reach the controller's artifact API at %s: %v; check that the controller's ws daemon is running and that port %s is reachable from this machine", e.Endpoint, e.Err, endpointPort(e.Endpoint))
	case envproto.ReasonCertificate:
		return fmt.Sprintf("the TLS handshake with %s failed: %v; if this machine's certificate expired, get a new code with ws env rotate on the controller and enroll again", e.Endpoint, e.Err)
	}
	return fmt.Sprintf("the artifact API is unreachable (%s): %v", e.Reason, e.Err)
}

// Unwrap returns the underlying error.
func (e *APIError) Unwrap() error {
	return e.Err
}

// endpointPort is the port of endpoint, or endpoint itself when it has none
// (CheckEndpoint has already accepted every endpoint this is called with).
func endpointPort(endpoint string) string {
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	return port
}

// APIClient calls the controller's artifact API with this machine's identity.
type APIClient struct {
	endpoint string
	client   *http.Client
}

// NewAPIClient loads the identity (Files.Load; on errKeyMismatch, as after
// a renewal mid-write, waits 100 ms and loads once more) and the endpoint,
// and builds a client over id.TLSConfig() (its ServerName is agent.toml's
// server_name): dial timeout 10 s, TLS handshake 10 s, response headers
// 2 min, no overall timeout (callers pass a context), at most 2 idle
// connections. *APIError ReasonIdentity or ReasonNoEndpoint on failure.
func NewAPIClient(files *Files) (*APIClient, error) {
	id, err := files.Load()
	if errors.Is(err, errKeyMismatch) {
		time.Sleep(apiKeyRetryWait)
		id, err = files.Load()
	}
	if err != nil {
		return nil, &APIError{Reason: envproto.ReasonIdentity, Err: err}
	}
	endpoint, err := files.APIEndpoint()
	if err != nil {
		return nil, &APIError{Reason: envproto.ReasonNoEndpoint, Err: err}
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: apiDialTimeout}).DialContext,
		TLSClientConfig:       id.TLSConfig(),
		TLSHandshakeTimeout:   apiHandshakeTimeout,
		ResponseHeaderTimeout: apiResponseTimeout,
		MaxIdleConns:          apiIdleConns,
		MaxIdleConnsPerHost:   apiIdleConns,
	}
	return &APIClient{endpoint: endpoint, client: &http.Client{Transport: transport}}, nil
}

// Endpoint is the host:port the client calls.
func (c *APIClient) Endpoint() string {
	return c.endpoint
}

// Do sends method https://<endpoint><path>?<query> with header and body
// (size bytes; -1 unknown, 0 none). A transport failure is *APIError:
// ReasonCertificate for a *net.OpError with Op "remote error", a
// *tls.CertificateVerificationError or an x509 error; ReasonOffline for
// everything else (dial errors, timeouts, resets). Any HTTP answer is
// returned as it is, for the caller to read and close.
func (c *APIClient) Do(ctx context.Context, method, path string, query url.Values, header http.Header, body io.Reader, size int64) (*http.Response, error) {
	target := url.URL{Scheme: "https", Host: c.endpoint, Path: path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	maps.Copy(req.Header, header)
	if size >= 0 {
		req.ContentLength = size
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, transportError(c.endpoint, err)
	}
	return resp, nil
}

// transportError classifies a request that got no HTTP answer.
func transportError(endpoint string, err error) *APIError {
	reason := envproto.ReasonOffline
	if isCertificateError(err) {
		reason = envproto.ReasonCertificate
	}
	return &APIError{Reason: reason, Endpoint: endpoint, Err: err}
}

// isCertificateError reports whether err is a TLS handshake refusing a
// certificate, either side's.
func isCertificateError(err error) bool {
	var remote *net.OpError
	if errors.As(err, &remote) && remote.Op == "remote error" {
		return true
	}
	var verification *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	return errors.As(err, &verification) || errors.As(err, &unknown) ||
		errors.As(err, &invalid) || errors.As(err, &hostname)
}
