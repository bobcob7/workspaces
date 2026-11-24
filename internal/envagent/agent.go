package envagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/redact"
)

// ErrCertificateExpired is returned by Run when the agent's certificate has
// expired, so it can't connect again.
var ErrCertificateExpired = errors.New("the agent's certificate has expired")

// queueFactor is how many operations may wait per MaxOps slot.
const queueFactor = 8

// inboxSize is how many requests and replies may wait for the session loop.
const inboxSize = 16

// Options are the agent's intervals, its retry policy and its clock.
type Options struct {
	Version         string        // ws's version, for the hello.
	RenewBeforeDays int           // the agent's own [environments] renew_before_days.
	SnapshotCheck   time.Duration // rebuild the snapshot.
	SnapshotEvery   time.Duration // publish even if unchanged.
	RenewCheck      time.Duration // check whether the certificate is due for renewal.
	KeepAlive       time.Duration
	WillDelay       time.Duration
	NotSentLowWater int           // bytes of unsent data the socket send queue may hold; 0 is the OS default.
	RequestTimeout  time.Duration // how long a request waits for its reply.
	WorkspacesRoot  string        // the agent's CONFIG_DIR/workspaces, for CheckWorkspaceRef.
	MaxOps          int           // operations run at once.
	MaxTerminals    int           // terminals open at once; 0 is envproto.MaxTerminals.
	BackoffMin      time.Duration
	BackoffMax      time.Duration
	Now             func() time.Time
	After           func(time.Duration) <-chan time.Time
	Jitter          func() float64   // in [0, 1).
	LoopTick        time.Duration    // how often a connected agent runs a loop pass; 0 is off.
	Redactor        *redact.Redactor // hides secrets in the published loops; nil hides none.
}

// DefaultOptions returns the intervals ADR 0075 asks for.
func DefaultOptions(version string, renewBeforeDays int) Options {
	return Options{
		Version:         version,
		RenewBeforeDays: renewBeforeDays,
		SnapshotCheck:   10 * time.Second,
		SnapshotEvery:   30 * time.Second,
		RenewCheck:      time.Hour,
		KeepAlive:       30 * time.Second,
		WillDelay:       10 * time.Second,
		RequestTimeout:  30 * time.Second,
		MaxOps:          4,
		MaxTerminals:    envproto.MaxTerminals,
		BackoffMin:      time.Second,
		BackoffMax:      time.Minute,
		Now:             time.Now,
		After:           time.After,
		Jitter:          rand.Float64,
		LoopTick:        time.Minute,
	}
}

// Agent is the environment agent's run loop: one connection to the broker
// at a time, its hello and snapshot, renewal and the controller's requests.
type Agent struct {
	log       *slog.Logger
	files     *Files
	client    mqttClient
	snapshots snapshotSource
	claude    claudeProbe
	ops       operationRunner
	requests  *requests
	random    io.Reader
	opts      Options
	inbox     chan envproto.Message
	pending   map[string]chan envproto.Message // replies awaited, by correlation data
	slots     chan struct{}                    // one per operation running, MaxOps in all
	queue     chan struct{}                    // one per operation running or waiting for a slot
	running   sync.WaitGroup                   // the operation goroutines
	terms     *terminals                       // nil when ops can't build terminal commands
	changed   chan struct{}                    // an operation changed the workspaces
	connID    string                           // the current connection's id, in every hello it sends
	runCtx    context.Context                  //nolint:containedctx // the context loop passes run under, so a lost connection doesn't kill one.
	ticker    loopTicker                       // nil when the agent runs no loops.
	ticking   atomic.Bool                      // a loop pass is running.
	passes    sync.WaitGroup                   // the loop pass goroutines.
	limits    *limiter
	// The last snapshot published, for deciding when to publish again.
	workspaces []byte
	agents     []byte
	loops      []byte
	snapshotAt time.Time
}

// NewAgent returns an agent that keeps its identity in files and talks
// through client.
func NewAgent(log *slog.Logger, files *Files, client mqttClient, snapshots snapshotSource, claude claudeProbe, ops operationRunner, random io.Reader, opts Options) *Agent { //nolint:gocritic // hugeParam: Options is passed by value, as the signature has it.
	a := &Agent{
		log:       log,
		files:     files,
		client:    client,
		snapshots: snapshots,
		claude:    claude,
		ops:       ops,
		requests:  newRequests(opts.Now),
		random:    random,
		opts:      opts,
		inbox:     make(chan envproto.Message, inboxSize),
		pending:   make(map[string]chan envproto.Message),
		slots:     make(chan struct{}, max(opts.MaxOps, 1)),
		queue:     make(chan struct{}, max(opts.MaxOps, 1)*queueFactor),
		changed:   make(chan struct{}, 1),
		limits:    newLimiter(opts.Now),
	}
	if commands, ok := ops.(terminalCommands); ok {
		a.terms = newTerminals(log, client, commands, random, &opts)
	}
	return a
}

// Run loads the identity (ErrNotEnrolled when there is none), then connects
// and serves until ctx ends, which returns nil. It reconnects with backoff
// and never gives up, except when the certificate has expired: then it
// returns ErrCertificateExpired without connecting.
func (a *Agent) Run(ctx context.Context) error {
	id, err := a.files.Load()
	if err != nil {
		return err
	}
	a.runCtx = ctx
	defer a.passes.Wait()
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := checkExpiry(&id, a.opts.Now()); err != nil {
			return err
		}
		if err := a.connect(ctx, &id); err != nil {
			a.log.WarnContext(ctx, "connect failed", "attempt", attempt, "error", err)
		} else {
			attempt = 0
			a.serve(ctx, &id)
		}
		if !a.sleep(ctx, a.backoffFor(attempt)) {
			return nil
		}
		attempt++
	}
}

// checkExpiry is ErrCertificateExpired, with what to do about it, when now
// is at or after the certificate's expiry.
func checkExpiry(id *Identity, now time.Time) error {
	if now.Before(id.Cert.NotAfter) {
		return nil
	}
	return fmt.Errorf("%w on %s; run ws env rotate %s on the controller, then ws env agent enroll <code>",
		ErrCertificateExpired, id.Cert.NotAfter.UTC().Format("2006-01-02"), id.Name)
}

// connect dials the broker as id, subscribes to the requests for id, and
// announces the agent with its hello and a snapshot.
func (a *Agent) connect(ctx context.Context, id *Identity) error {
	connID, err := newCorrelation(a.random)
	if err != nil {
		return fmt.Errorf("making the connection id: %w", err)
	}
	a.connID = connID
	err = a.client.Connect(ctx, envproto.ConnectOptions{
		Address:         id.Broker,
		TLS:             id.TLSConfig(),
		ClientID:        id.ID,
		KeepAlive:       a.opts.KeepAlive,
		Will:            &envproto.Message{Topic: envproto.Topic(id.ID, envproto.LeafStatus), Payload: envproto.OfflinePayload(), QoS: 1, Retain: true},
		WillDelay:       a.opts.WillDelay,
		NotSentLowWater: a.opts.NotSentLowWater,
	})
	if err != nil {
		return err
	}
	if err := a.client.Subscribe(ctx, envproto.Topic(id.ID, envproto.LeafReq), func(msg envproto.Message) { a.enqueue(&msg) }); err != nil {
		a.closeClient(ctx)
		return err
	}
	if err := a.subscribeStreams(ctx, id); err != nil {
		a.closeClient(ctx)
		return err
	}
	if err := a.publishHello(ctx, id); err != nil {
		a.closeClient(ctx)
		return err
	}
	a.refreshSnapshot(ctx, id, true)
	return nil
}

// serve runs one connection until ctx ends, when it publishes the offline
// status, or until the broker drops the client.
func (a *Agent) serve(parent context.Context, id *Identity) {
	ctx, cancelOps := context.WithCancel(parent)
	defer cancelOps()
	done := a.client.Done()
	if a.terms != nil {
		a.terms.begin(ctx, id.ID)
	}
	if a.ticker != nil && a.opts.LoopTick > 0 {
		var ticker sync.WaitGroup
		defer ticker.Wait()
		defer cancelOps()
		ticker.Go(func() { a.tickLoops(ctx) })
	}
	snapshotTimer := a.opts.After(a.opts.SnapshotCheck)
	renewTimer := a.opts.After(a.opts.RenewCheck)
	for {
		select {
		case <-ctx.Done():
			a.drainOps(cancelOps)
			a.closeTerminals()
			a.goOffline(ctx, id)
			return
		case <-done:
			a.log.WarnContext(ctx, "connection lost")
			a.drainOps(cancelOps)
			a.closeTerminals()
			a.closeClient(ctx)
			return
		case <-snapshotTimer:
			a.refreshSnapshot(ctx, id, false)
			snapshotTimer = a.opts.After(a.opts.SnapshotCheck)
		case <-renewTimer:
			a.renewIfDue(ctx, id)
			renewTimer = a.opts.After(a.opts.RenewCheck)
		case <-a.changed:
			a.refreshSnapshot(ctx, id, true)
		case msg := <-a.inbox:
			a.handle(ctx, id, &msg)
		}
	}
}

// subscribeStreams subscribes to the terminals' input, when the agent has any.
func (a *Agent) subscribeStreams(ctx context.Context, id *Identity) error {
	if a.terms == nil {
		return nil
	}
	return a.client.Subscribe(ctx, envproto.Topic(id.ID, envproto.LeafStream, "+", "in"), func(msg envproto.Message) { a.terms.onStreamIn(&msg) })
}

// closeTerminals ends every terminal before the connection closes.
func (a *Agent) closeTerminals() {
	if a.terms != nil {
		a.terms.closeAll()
	}
}

// drainOps cancels the operations' context and waits for their goroutines, so
// none replies on a closed client.
func (a *Agent) drainOps(cancel context.CancelFunc) {
	cancel()
	a.running.Wait()
}

// goOffline publishes the retained offline status and closes the connection.
// It runs after ctx ends, so it uses a context that ctx's end doesn't cancel,
// bounded by RequestTimeout so an unreachable broker can't hold up shutdown.
func (a *Agent) goOffline(ctx context.Context, id *Identity) {
	offline, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.opts.RequestTimeout)
	defer cancel()
	err := a.client.Publish(offline, envproto.Message{
		Topic:   envproto.Topic(id.ID, envproto.LeafStatus),
		Payload: envproto.OfflinePayload(),
		QoS:     1,
		Retain:  true,
	})
	if err != nil {
		a.log.WarnContext(ctx, "offline status not published", "error", err)
	}
	a.closeClient(offline)
}

// closeClient closes the connection, logging a failure.
func (a *Agent) closeClient(ctx context.Context) {
	if err := a.client.Close(); err != nil {
		a.log.WarnContext(ctx, "connection not closed", "error", err)
	}
}

// refreshSnapshot publishes the workspaces and agents when they changed or
// SnapshotEvery has passed since the last snapshot, or always when force is
// set. A failure is logged; the next check tries again.
func (a *Agent) refreshSnapshot(ctx context.Context, id *Identity, force bool) {
	raw, err := a.snapshots.Workspaces(ctx)
	if err != nil {
		a.log.WarnContext(ctx, "snapshot not read", "error", err)
		return
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage("[]")
	}
	agents := a.snapshotAgents(ctx)
	agentsJSON, err := json.Marshal(agents)
	if err != nil {
		a.log.ErrorContext(ctx, "snapshot agents not encoded", "error", err)
		return
	}
	loops := a.snapshotLoops(ctx)
	loopsJSON, err := json.Marshal(loops)
	if err != nil {
		a.log.ErrorContext(ctx, "snapshot loops not encoded", "error", err)
		return
	}
	now := a.opts.Now().UTC()
	if !force && bytes.Equal(raw, a.workspaces) && bytes.Equal(agentsJSON, a.agents) && bytes.Equal(loopsJSON, a.loops) && now.Sub(a.snapshotAt) < a.opts.SnapshotEvery {
		return
	}
	payload, err := json.Marshal(envproto.Snapshot{
		At:         now,
		Protocol:   envproto.Protocol,
		Workspaces: raw,
		Agents:     agents,
		Loops:      loops,
	})
	if err != nil {
		a.log.ErrorContext(ctx, "snapshot not encoded", "error", err)
		return
	}
	if len(payload) > envproto.MaxRequestBytes {
		a.log.WarnContext(ctx, "snapshot too large", "bytes", len(payload))
		// Remember it, so the unchanged checks don't warn again until SnapshotEvery.
		a.workspaces, a.agents, a.loops, a.snapshotAt = raw, agentsJSON, loopsJSON, now
		return
	}
	err = a.client.Publish(ctx, envproto.Message{
		Topic:   envproto.Topic(id.ID, envproto.LeafSnapshot),
		Payload: payload,
		QoS:     1,
		Retain:  true,
	})
	if err != nil {
		a.log.WarnContext(ctx, "snapshot not published", "error", err)
		return
	}
	a.workspaces, a.agents, a.loops, a.snapshotAt = raw, agentsJSON, loopsJSON, now
}

// snapshotAgents is the Claude Code sessions to publish. A failing read is
// logged and publishes none, so the workspaces still go out.
func (a *Agent) snapshotAgents(ctx context.Context) []envproto.SnapshotAgent {
	agents, err := a.snapshots.Agents(ctx)
	if err != nil {
		a.log.WarnContext(ctx, "snapshot agents not read", "error", err)
		return []envproto.SnapshotAgent{}
	}
	if agents == nil {
		return []envproto.SnapshotAgent{}
	}
	return agents
}

// snapshotLoops is the loops to publish, redacted. A failing read is logged
// and publishes none, so the workspaces still go out.
func (a *Agent) snapshotLoops(ctx context.Context) []envproto.SnapshotLoop {
	loops, err := a.snapshots.Loops(ctx)
	if err != nil {
		a.log.WarnContext(ctx, "snapshot loops not read", "error", err)
		return []envproto.SnapshotLoop{}
	}
	if len(loops) == 0 {
		return []envproto.SnapshotLoop{}
	}
	return a.redactLoops(ctx, loops)
}

// redactLoops hides the agent's secrets in loops. A loop list that can't be
// redacted is not published.
func (a *Agent) redactLoops(ctx context.Context, loops []envproto.SnapshotLoop) []envproto.SnapshotLoop {
	if a.opts.Redactor == nil {
		return loops
	}
	raw, err := json.Marshal(loops)
	if err == nil {
		raw, _, err = a.opts.Redactor.JSON(raw)
	}
	var out []envproto.SnapshotLoop
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	if err != nil {
		a.log.WarnContext(ctx, "snapshot loops not redacted", "error", err)
		return []envproto.SnapshotLoop{}
	}
	return out
}

// publishHello publishes the retained online hello for id.
func (a *Agent) publishHello(ctx context.Context, id *Identity) error {
	payload, err := json.Marshal(a.hello(ctx, id))
	if err != nil {
		return fmt.Errorf("encoding the hello: %w", err)
	}
	return a.client.Publish(ctx, envproto.Message{
		Topic:   envproto.Topic(id.ID, envproto.LeafStatus),
		Payload: payload,
		QoS:     1,
		Retain:  true,
	})
}

// enqueue hands a message from the request subscription to the session loop
// without blocking the broker. A full inbox drops the message.
func (a *Agent) enqueue(msg *envproto.Message) {
	select {
	case a.inbox <- *msg:
	default:
		a.log.Warn("message dropped: inbox full", "topic", msg.Topic)
	}
}

// sleep waits d, reporting false when ctx ends first.
func (a *Agent) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-a.opts.After(d):
		return true
	}
}

// backoffFor is the wait before the attempt-th retry.
func (a *Agent) backoffFor(attempt int) time.Duration {
	return backoff(attempt, a.opts.BackoffMin, a.opts.BackoffMax, a.opts.Jitter())
}

// backoff is min(maximum, minimum<<attempt) scaled into [d/2, d] by jitter.
func backoff(attempt int, minimum, maximum time.Duration, jitter float64) time.Duration {
	d := minimum
	for i := 0; i < attempt && d < maximum; i++ {
		d *= 2
	}
	if d > maximum {
		d = maximum
	}
	low := d / 2
	return low + time.Duration(jitter*float64(d-low))
}
