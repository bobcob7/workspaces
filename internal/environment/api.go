package environment

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// The artifact API's listener limits (ADR 0087).
const (
	apiReadHeaderTimeout = 10 * time.Second
	apiReadTimeout       = 2 * time.Minute
	apiWriteTimeout      = 2 * time.Minute
	apiIdleTimeout       = 60 * time.Second
	apiMaxHeaderBytes    = 16 << 10
	// apiMaxInFlight caps the POSTs that run at once, so the controller holds
	// at most that many uploads in memory.
	apiMaxInFlight = 4
)

// errAPIRefused is why the artifact API refused a caller's certificate.
var errAPIRefused = errors.New("the artifact API refused the caller")

// APICaller is the environment an artifact API request comes from: the
// record its client certificate's common name names (ADR 0087).
type APICaller struct {
	EnvironmentID   string
	EnvironmentName string
}

type apiCallerKey struct{}

// APICallerFrom is the caller the identity check stored in ctx.
func APICallerFrom(ctx context.Context) (APICaller, bool) {
	caller, ok := ctx.Value(apiCallerKey{}).(APICaller)
	return caller, ok
}

// WithAPICaller returns ctx carrying caller. The listener uses it; handler tests may too.
func WithAPICaller(ctx context.Context, caller APICaller) context.Context {
	return context.WithValue(ctx, apiCallerKey{}, caller)
}

// SetArtifactAPI sets the handler for /v1/artifacts and below (internal/cli's).
// Call it before Start. Without one, every route answers 404.
func (c *Controller) SetArtifactAPI(h http.Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.artifactAPI = h
}

// APIEndpoint is host:port agents reach the API on: public_name with the
// API listener's bound port; "" while not listening.
func (c *Controller) APIEndpoint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.apiEndpoint
}

// listenAPI binds the artifact API's address, which is set.
func (c *Controller) listenAPI(ctx context.Context, lc *net.ListenConfig) (net.Listener, error) {
	ln, err := lc.Listen(ctx, "tcp", c.settings.APIListen)
	if err != nil {
		return nil, fmt.Errorf("listening for the artifact API: %w", err)
	}
	return ln, nil
}

// newAPIServer is the artifact API's HTTP server, with timeouts that bound each request.
func (c *Controller) newAPIServer() *http.Server {
	return &http.Server{
		Handler:           c.apiHandler(),
		ReadHeaderTimeout: apiReadHeaderTimeout,
		ReadTimeout:       apiReadTimeout,
		WriteTimeout:      apiWriteTimeout,
		IdleTimeout:       apiIdleTimeout,
		MaxHeaderBytes:    apiMaxHeaderBytes,
	}
}

// serveAPI serves the artifact API over mutual TLS until it is closed.
func (c *Controller) serveAPI(ctx context.Context, srv *http.Server, ln net.Listener) {
	if err := srv.Serve(tls.NewListener(ln, brokerTLS(c.currentState))); err != nil && !errors.Is(err, http.ErrServerClosed) {
		c.log.ErrorContext(ctx, "artifact api listener stopped", "error", err)
	}
}

// apiHandler is what the API listener serves: the identity check, the
// in-flight cap, then the artifact handler.
func (c *Controller) apiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isArtifactsPath(r.URL.Path) {
			writeError(w, http.StatusNotFound, "usage", "no such path")
			return
		}
		caller, err := c.identify(r)
		if err != nil {
			c.log.WarnContext(r.Context(), "artifact api refused", "remote", r.RemoteAddr, "cn", peerName(r.TLS), "error", err)
			writeErrorDetails(w, http.StatusForbidden, "artifact_unreachable",
				"the controller refused this machine's certificate: it is revoked or its environment was removed; enroll it again",
				map[string]any{"reason": envproto.ReasonRefused})
			return
		}
		if r.Method == http.MethodPost {
			if !c.acquireAPIPost() {
				writeErrorDetails(w, http.StatusServiceUnavailable, "operation_limited", "the controller is busy with other uploads; try again", nil)
				return
			}
			defer c.releaseAPIPost()
		}
		handler := c.artifactAPIHandler()
		if handler == nil {
			writeError(w, http.StatusNotFound, "usage", "no such path")
			return
		}
		handler.ServeHTTP(w, r.WithContext(WithAPICaller(r.Context(), caller)))
	})
}

// isArtifactsPath reports whether path is the artifact API's route or below it.
func isArtifactsPath(path string) bool {
	return path == envproto.ArtifactsPath || strings.HasPrefix(path, envproto.ArtifactsPath+"/")
}

// artifactAPIHandler is the handler SetArtifactAPI set, or nil.
func (c *Controller) artifactAPIHandler() http.Handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.artifactAPI
}

// acquireAPIPost takes one of the in-flight POST slots without waiting.
func (c *Controller) acquireAPIPost() bool {
	select {
	case c.apiPosts <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseAPIPost frees a slot acquireAPIPost took.
func (c *Controller) releaseAPIPost() {
	<-c.apiPosts
}

// identify is r's caller, from its verified client certificate. It runs
// policy.connect on envproto.PeerFromState(r.TLS) (ClientID = CN), then
// reads the record by id for its name. Any failure is errAPIRefused.
func (c *Controller) identify(r *http.Request) (APICaller, error) {
	peer, err := envproto.PeerFromState(r.TLS)
	if err != nil {
		return APICaller{}, fmt.Errorf("%w: %w", errAPIRefused, err)
	}
	peer.ClientID = peer.CN
	if err = c.policy.connect(peer); err != nil {
		return APICaller{}, fmt.Errorf("%w: %w", errAPIRefused, err)
	}
	rec, err := c.store.ByID(peer.CN)
	if err != nil {
		return APICaller{}, fmt.Errorf("%w: %w", errAPIRefused, err)
	}
	return APICaller{EnvironmentID: rec.ID, EnvironmentName: rec.Name}, nil
}

// peerName is the common name of the certificate a client presented, for logs; "" without one.
func peerName(cs *tls.ConnectionState) string {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return ""
	}
	return cs.PeerCertificates[0].Subject.CommonName
}

// closeAPIServer closes srv, which is nil when no API listener is up.
func closeAPIServer(srv *http.Server) error {
	if srv == nil {
		return nil
	}
	return srv.Close()
}

// closeListeners closes the listeners that were bound, skipping nil ones.
func closeListeners(lns ...net.Listener) error {
	var errs []error
	for _, ln := range lns {
		if ln != nil {
			errs = append(errs, ln.Close())
		}
	}
	return errors.Join(errs...)
}
