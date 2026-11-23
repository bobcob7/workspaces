package environment

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	statusPending     = "pending"
	enrollReadTimeout = 10 * time.Second
	// enrollMaxHeaderBytes bounds a request's headers on the enrollment listener.
	enrollMaxHeaderBytes = 8 << 10
	caUpdateExpiry       = 30 * time.Second
	requestIDBytes       = 8
	snapshotVersion      = 1
)

// Settings are [environments] as the daemon read it.
type Settings struct {
	Listen          string // "0.0.0.0:8883"
	EnrollListen    string // "0.0.0.0:8884"
	PublicName      string
	RenewBeforeDays int
}

// Presence is one environment as the daemon sees it.
type Presence struct {
	ID       string
	Name     string
	State    State
	Status   string // "pending", "online" or "offline"
	LastSeen *time.Time
	Hello    *envproto.Hello // the last online hello, or nil
}

// Status is the controller's listeners and environments.
type Status struct {
	Listening    bool
	Listen       string // the bound address, "" when not listening
	EnrollListen string
	Environments []Presence // sorted by name; [] when none
}

// heard is what the controller has heard from one enrolled environment.
type heard struct {
	online   bool
	lastSeen time.Time
	hello    *envproto.Hello
}

// snapshotRecord is state/.environments/<name>/snapshot.json.
type snapshotRecord struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	ReceivedAt string          `json:"received_at"`
	Snapshot   json.RawMessage `json:"snapshot"`
}

// Controller runs the broker and the enrollment listener in the daemon.
type Controller struct {
	log      *slog.Logger
	store    *Store
	broker   broker
	settings Settings
	now      func() time.Time
	policy   *policy
	// enrollTimeout bounds how long an enrollment connection may take to send
	// a request, receive its reply, or sit idle.
	enrollTimeout time.Duration

	// lifecycle serializes Start, Reload and Close. mu is never held across
	// a broker call, since the broker's handlers take mu.
	lifecycle sync.Mutex

	state atomic.Pointer[tlsState]

	mu             sync.Mutex
	presence       map[string]heard
	listening      bool
	listenAddr     string
	enrollAddr     string
	enrollServer   *http.Server
	brokerEndpoint string // host:port that enrollment answers give agents
	unsubscribe    []func()
	cancel         context.CancelFunc // ends the context the handlers run on
	startWarnings  []string
}

// NewController returns a controller that isn't listening. It has no side
// effects.
func NewController(log *slog.Logger, store *Store, b broker, settings Settings, now func() time.Time) *Controller {
	c := &Controller{
		log:           log,
		store:         store,
		broker:        b,
		settings:      settings,
		now:           now,
		presence:      map[string]heard{},
		enrollTimeout: enrollReadTimeout,
	}
	c.policy = &policy{store: store, trust: c.currentTrust}
	return c
}

// Start does nothing when there's no CA, and adds a warning instead of
// listening when PublicName is "". Otherwise it loads trust and the server
// certificate, starts the enrollment listener, the broker and the
// subscriptions.
func (c *Controller) Start(ctx context.Context) error {
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	return c.start(ctx)
}

// start is Start without the lifecycle lock.
func (c *Controller) start(ctx context.Context) error {
	if c.isListening() {
		return nil
	}
	c.setStartWarnings(nil)
	exists, err := c.store.caExists()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if c.settings.PublicName == "" {
		c.setStartWarnings([]string{"not listening: [environments] public_name is not set"})
		return nil
	}
	if err := c.listenAll(ctx); err != nil {
		c.setStartWarnings([]string{"not listening: " + err.Error()})
		return err
	}
	return nil
}

// listenAll binds the enrollment listener, starts the broker and its
// subscriptions, then serves enrollment. ctx only bounds the binding: the
// handlers and the enrollment server run on a context the controller owns,
// which lives until Close, so a Reload request that returns doesn't stop them.
func (c *Controller) listenAll(ctx context.Context) error {
	if err := c.loadState(); err != nil {
		return err
	}
	var lc net.ListenConfig
	enrollLn, err := lc.Listen(ctx, "tcp", c.settings.EnrollListen)
	if err != nil {
		return fmt.Errorf("listening for enrollment: %w", err)
	}
	brokerAddr, err := c.broker.Start(ctx, c.settings.Listen, brokerTLS(c.currentState), c.policy.rules())
	if err != nil {
		return errors.Join(fmt.Errorf("starting the broker: %w", err), enrollLn.Close())
	}
	endpoint, err := brokerEndpoint(c.settings.PublicName, brokerAddr)
	if err != nil {
		return errors.Join(err, c.broker.Close(), enrollLn.Close())
	}
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	unsubscribe, err := c.subscribe(ctx, life)
	if err != nil {
		cancel()
		return errors.Join(err, c.broker.Close(), enrollLn.Close())
	}
	srv := &http.Server{
		Handler:           newEnrollHandler(c.log, c.enroll),
		ReadHeaderTimeout: c.enrollTimeout,
		ReadTimeout:       c.enrollTimeout,
		WriteTimeout:      c.enrollTimeout,
		IdleTimeout:       c.enrollTimeout,
		MaxHeaderBytes:    enrollMaxHeaderBytes,
	}
	c.publishListeners(&listeners{
		cancel:      cancel,
		srv:         srv,
		enrollAddr:  enrollLn.Addr().String(),
		brokerAddr:  brokerAddr,
		endpoint:    endpoint,
		unsubscribe: unsubscribe,
	})
	go c.serveEnrollment(life, srv, tls.NewListener(enrollLn, enrollTLS(c.currentState)))
	return nil
}

// listeners is what Start records once its listeners are up.
type listeners struct {
	cancel      context.CancelFunc
	srv         *http.Server
	enrollAddr  string
	brokerAddr  string
	endpoint    string
	unsubscribe []func()
}

// publishListeners records that the listeners are up. Enrollment answers
// read the broker endpoint, so it is set before any request is served.
func (c *Controller) publishListeners(l *listeners) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listening = true
	c.listenAddr = l.brokerAddr
	c.enrollAddr = l.enrollAddr
	c.enrollServer = l.srv
	c.brokerEndpoint = l.endpoint
	c.unsubscribe = l.unsubscribe
	c.cancel = l.cancel
	c.startWarnings = nil
}

// brokerEndpoint is the host:port agents dial: public_name and the broker's port.
func brokerEndpoint(publicName, brokerAddr string) (string, error) {
	_, port, err := net.SplitHostPort(brokerAddr)
	if err != nil {
		return "", fmt.Errorf("reading the broker's port: %w", err)
	}
	return net.JoinHostPort(publicName, port), nil
}

// serveEnrollment serves the enrollment listener until it is closed.
func (c *Controller) serveEnrollment(ctx context.Context, srv *http.Server, ln net.Listener) {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		c.log.ErrorContext(ctx, "enrollment listener stopped", "error", err)
	}
}

// enroll is the enrollment handler's exchange: the store, with the broker endpoint.
func (c *Controller) enroll(ctx context.Context, secret string, csrPEM []byte) (envproto.EnrollResponse, error) {
	c.mu.Lock()
	endpoint := c.brokerEndpoint
	c.mu.Unlock()
	return c.store.Enroll(ctx, secret, csrPEM, endpoint, c.settings.PublicName)
}

// subscribe takes the controller's subscriptions, which the broker doesn't
// check. ctx bounds the subscribing; the handlers run on life.
func (c *Controller) subscribe(ctx, life context.Context) ([]func(), error) {
	routes := []struct {
		filter string
		fn     func(envproto.Message)
	}{
		{envproto.Topic("+", envproto.LeafStatus), func(m envproto.Message) { c.onStatus(life, &m) }},
		{envproto.Topic("+", envproto.LeafSnapshot), func(m envproto.Message) { c.onSnapshot(life, &m) }},
		{envproto.Topic("+", envproto.LeafUp), func(m envproto.Message) { c.onUp(life, &m) }},
		{envproto.Topic("+", envproto.LeafRes, "+"), func(m envproto.Message) { c.onRes(life, &m) }},
	}
	unsubscribe := make([]func(), 0, len(routes))
	for _, r := range routes {
		unsub, err := c.broker.Subscribe(ctx, r.filter, r.fn)
		if err != nil {
			for _, done := range unsubscribe {
				done()
			}
			return nil, fmt.Errorf("subscribing to %s: %w", r.filter, err)
		}
		unsubscribe = append(unsubscribe, unsub)
	}
	return unsubscribe, nil
}

// Reload rereads the CA and server certificate (and Starts when the CA is
// new), swaps the TLS state, disconnects every client policy.connect now
// refuses (revoked, removed, untrusted CA), and sends ca.update to every
// online environment whose hello lacks a trusted root.
func (c *Controller) Reload(ctx context.Context) error {
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	if !c.isListening() {
		return c.start(ctx)
	}
	if err := c.loadState(); err != nil {
		return err
	}
	c.disconnectRefused(ctx)
	c.updateStaleRoots(ctx)
	return nil
}

// disconnectRefused drops every connected client that policy.connect refuses now.
func (c *Controller) disconnectRefused(ctx context.Context) {
	for _, peer := range c.broker.Clients() {
		if err := c.policy.connect(peer); err != nil {
			c.log.InfoContext(ctx, "environment disconnected", "id", peer.CN)
			c.broker.Disconnect(peer.Serial)
		}
	}
}

// updateStaleRoots sends ca.update to every online environment whose last hello lacks a trusted root.
func (c *Controller) updateStaleRoots(ctx context.Context) {
	for _, id := range c.staleEnvironments() {
		c.requestCAUpdate(ctx, id)
	}
}

// staleEnvironments lists the online environments whose hello lacks a trusted root, sorted.
func (c *Controller) staleEnvironments() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for id, h := range c.presence {
		if h.online && h.hello != nil && c.missingRoot(h.hello.CAFingerprints) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// loadState reads the CA and server certificate and swaps the TLS state.
func (c *Controller) loadState() error {
	tr, err := c.store.trust()
	if err != nil {
		return err
	}
	server, err := c.store.serverCertificate(c.settings.PublicName)
	if err != nil {
		return err
	}
	c.state.Store(&tlsState{trust: tr, server: server})
	return nil
}

// currentState is the TLS state the listeners serve with.
func (c *Controller) currentState() *tlsState {
	return c.state.Load()
}

// currentTrust is the set of roots the listeners accept.
func (c *Controller) currentTrust() trust {
	return c.currentState().trust
}

// missingRoot reports whether have lacks a root the controller trusts.
func (c *Controller) missingRoot(have []string) bool {
	for _, fp := range c.currentTrust().fingerprints() {
		if !slices.Contains(have, fp) {
			return true
		}
	}
	return false
}

// Status reports the listeners and every environment.
func (c *Controller) Status() Status {
	c.mu.Lock()
	status := Status{Listening: c.listening, Listen: c.listenAddr, EnrollListen: c.enrollAddr}
	c.mu.Unlock()
	status.Environments = c.presences()
	return status
}

// presences lists every environment with what the controller has heard, [] when there are none.
func (c *Controller) presences() []Presence {
	recs, err := c.store.List()
	if err != nil {
		c.log.Error("listing environments", "error", err)
		return []Presence{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Presence, 0, len(recs))
	for i := range recs {
		out = append(out, c.presenceOf(&recs[i]))
	}
	return out
}

// presenceOf describes rec with what the controller has heard. c.mu must be held.
func (c *Controller) presenceOf(rec *Record) Presence {
	p := Presence{ID: rec.ID, Name: rec.Name, State: rec.State, Status: statusPending}
	if rec.State != StateEnrolled {
		return p
	}
	h := c.presence[rec.ID]
	p.Status = envproto.StatusOffline
	if h.online {
		p.Status = envproto.StatusOnline
	}
	if !h.lastSeen.IsZero() {
		seen := h.lastSeen
		p.LastSeen = &seen
	}
	p.Hello = h.hello
	return p
}

// Warnings are why the controller isn't listening (no public_name, a Start
// error) and caExpiryWarning of the current CA.
func (c *Controller) Warnings() []string {
	c.mu.Lock()
	warnings := slices.Clone(c.startWarnings)
	c.mu.Unlock()
	if warnings == nil {
		warnings = []string{}
	}
	if warning := c.expiryWarning(); warning != "" {
		warnings = append(warnings, warning)
	}
	return warnings
}

// expiryWarning is the CA-expiry warning, or "" without a readable CA.
func (c *Controller) expiryWarning() string {
	cert, err := c.store.currentCert()
	if err != nil {
		return ""
	}
	return caExpiryWarning(cert, c.now(), c.settings.RenewBeforeDays)
}

// Close stops the listeners and the subscriptions, and closes the broker.
func (c *Controller) Close() error {
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	c.mu.Lock()
	if !c.listening {
		c.mu.Unlock()
		return nil
	}
	srv, unsubscribe, cancel := c.enrollServer, c.unsubscribe, c.cancel
	c.listening = false
	c.listenAddr = ""
	c.enrollAddr = ""
	c.enrollServer = nil
	c.brokerEndpoint = ""
	c.unsubscribe = nil
	c.cancel = nil
	c.mu.Unlock()
	cancel()
	for _, unsub := range unsubscribe {
		unsub()
	}
	return errors.Join(srv.Close(), c.broker.Close())
}

// isListening reports whether the listeners are up.
func (c *Controller) isListening() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listening
}

// setStartWarnings replaces why the controller isn't listening.
func (c *Controller) setStartWarnings(warnings []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startWarnings = warnings
}

// known reports whether an environment with id is enrolled or pending.
func (c *Controller) known(id string) bool {
	_, err := c.store.ByID(id)
	return err == nil
}

// onStatus records an environment's hello or last will, and asks it for the
// CA roots it lacks.
func (c *Controller) onStatus(ctx context.Context, msg *envproto.Message) {
	id, _, ok := envproto.ParseTopic(msg.Topic)
	if !ok || !c.known(id) {
		return
	}
	var hello envproto.Hello
	if err := json.Unmarshal(msg.Payload, &hello); err != nil {
		c.log.WarnContext(ctx, "status refused", "id", id)
		return
	}
	if hello.Status != envproto.StatusOnline && hello.Status != envproto.StatusOffline {
		c.log.DebugContext(ctx, "status ignored", "id", id)
		return
	}
	c.recordStatus(id, &hello)
	if hello.Status == envproto.StatusOnline && c.missingRoot(hello.CAFingerprints) {
		c.requestCAUpdate(ctx, id)
	}
}

// recordStatus stores what a status message says about the environment id.
func (c *Controller) recordStatus(id string, hello *envproto.Hello) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.presence[id]
	h.lastSeen = c.now().UTC()
	h.online = hello.Status == envproto.StatusOnline
	if h.online {
		h.hello = hello
	}
	c.presence[id] = h
}

// onSnapshot writes the environment's snapshot to state/.environments/<name>/snapshot.json.
func (c *Controller) onSnapshot(ctx context.Context, msg *envproto.Message) {
	id, _, ok := envproto.ParseTopic(msg.Topic)
	if !ok {
		return
	}
	rec, err := c.store.ByID(id)
	if err != nil {
		return
	}
	var snap *envproto.Snapshot
	if err = json.Unmarshal(msg.Payload, &snap); err != nil || snap == nil {
		c.log.WarnContext(ctx, "snapshot refused", "id", id)
		return
	}
	data, err := json.Marshal(snapshotRecord{
		Version:    snapshotVersion,
		ID:         id,
		Name:       rec.Name,
		ReceivedAt: c.now().UTC().Format(time.RFC3339),
		Snapshot:   msg.Payload,
	})
	if err != nil {
		c.log.ErrorContext(ctx, "snapshot not encoded", "id", id, "error", err)
		return
	}
	if err := c.store.WriteSnapshot(rec.Name, data); err != nil {
		c.log.ErrorContext(ctx, "snapshot not written", "id", id, "error", err)
	}
}

// onUp answers an agent's request on env/<id>/up. It acts and replies only
// when the request's response topic is env/<id>/req.
func (c *Controller) onUp(ctx context.Context, msg *envproto.Message) {
	id, _, ok := envproto.ParseTopic(msg.Topic)
	if !ok || msg.ResponseTopic != envproto.Topic(id, envproto.LeafReq) {
		return
	}
	reply := c.answer(ctx, id, msg.Payload)
	c.publishReply(ctx, msg.ResponseTopic, msg.CorrelationData, reply)
}

// answer runs the request in payload for environment id and returns its reply.
func (c *Controller) answer(ctx context.Context, id string, payload []byte) []byte {
	if len(payload) > envproto.MaxRequestBytes {
		return envproto.ErrorReply("usage", "request too large")
	}
	var req envproto.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return envproto.ErrorReply("usage", "malformed request")
	}
	if req.Op != envproto.OpCertRenew {
		return envproto.ErrorReply("usage", fmt.Sprintf("unknown operation %q", req.Op))
	}
	return c.renew(ctx, id, req.Args)
}

// renew signs the CSR in args for environment id and returns the reply.
func (c *Controller) renew(ctx context.Context, id string, args json.RawMessage) []byte {
	var in envproto.RenewArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return envproto.ErrorReply("usage", "malformed cert.renew arguments")
	}
	result, err := c.store.Renew(ctx, id, []byte(in.CSR))
	if err != nil {
		c.log.WarnContext(ctx, "certificate renewal refused", "id", id, "error", err)
		return envproto.ErrorReply("enrollment_failed", "certificate renewal refused")
	}
	reply, err := envproto.OKReply(result)
	if err != nil {
		c.log.ErrorContext(ctx, "reply not encoded", "id", id, "error", err)
		return envproto.ErrorReply("internal", "reply not encoded")
	}
	c.log.InfoContext(ctx, "certificate renewed", "id", id)
	return reply
}

// publishReply sends a reply on topic with the request's correlation data.
func (c *Controller) publishReply(ctx context.Context, topic string, correlation, payload []byte) {
	err := c.broker.Publish(ctx, envproto.Message{
		Topic:           topic,
		Payload:         payload,
		QoS:             1,
		CorrelationData: correlation,
	})
	if err != nil {
		c.log.WarnContext(ctx, "reply not published", "error", err)
	}
}

// onRes logs whether an environment acknowledged a ca.update.
func (c *Controller) onRes(ctx context.Context, msg *envproto.Message) {
	id, _, ok := envproto.ParseTopic(msg.Topic)
	if !ok {
		return
	}
	var reply envproto.Reply
	if err := json.Unmarshal(msg.Payload, &reply); err != nil {
		c.log.WarnContext(ctx, "reply refused", "id", id)
		return
	}
	if reply.OK {
		c.log.InfoContext(ctx, "ca.update acknowledged", "id", id)
		return
	}
	code := ""
	if reply.Error != nil {
		code = reply.Error.Code
	}
	c.log.WarnContext(ctx, "ca.update refused", "id", id, "code", code)
}

// requestCAUpdate sends a ca.update to id and logs a failure.
func (c *Controller) requestCAUpdate(ctx context.Context, id string) {
	if err := c.sendCAUpdate(ctx, id); err != nil {
		c.log.WarnContext(ctx, "ca.update not sent", "id", id, "error", err)
	}
}

// sendCAUpdate asks id to trust every root the controller trusts. The reply
// comes back on env/<id>/res/<req>.
func (c *Controller) sendCAUpdate(ctx context.Context, id string) error {
	reqID, err := newRequestID()
	if err != nil {
		return err
	}
	payload, err := marshalRequest(envproto.OpCAUpdate, envproto.CAUpdateArgs{
		CABundle: string(c.currentTrust().bundlePEM()),
	})
	if err != nil {
		return err
	}
	return c.broker.Publish(ctx, envproto.Message{
		Topic:           envproto.Topic(id, envproto.LeafReq),
		Payload:         payload,
		QoS:             1,
		ResponseTopic:   envproto.Topic(id, envproto.LeafRes, reqID),
		CorrelationData: []byte(reqID),
		Expiry:          caUpdateExpiry,
	})
}

// marshalRequest encodes {"op": op, "args": args}.
func marshalRequest(op string, args any) ([]byte, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envproto.Request{Op: op, Args: raw})
}

// newRequestID returns 16 random hex characters for a request.
func newRequestID() (string, error) {
	buf := make([]byte, requestIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("reading a request id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
