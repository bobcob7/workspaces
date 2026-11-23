package environment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// CallRequest is one operation the CLI asks the controller to send.
type CallRequest struct {
	Op        string
	Args      json.RawMessage
	RequestID string    // envproto.ValidRequestID
	Deadline  time.Time // now + the op's Timeout, set by the caller
}

// pendingCall is a request Call sent and waits on.
type pendingCall struct {
	env         string
	deadline    time.Time
	msg         envproto.Message
	reply       chan envproto.Reply
	republished bool
}

// Call sends req to the environment called name and waits for its reply
// until req.Deadline + the grace period or ctx ends. In order: ErrUnknown for
// no enrolled environment called name; ErrNotForwarded (wrapped with the op)
// unless the op's caller is the controller; an invalid request id; an
// *OfflineError with reason "not_listening" when the controller isn't
// listening, or "offline" when the environment isn't online; an
// *OutdatedError when the agent's protocol is below the op's Since, and
// ErrRequestPending when req.RequestID is already waiting for a reply. Then it
// publishes the request on env/<id>/req with QoS 1, its response topic and
// its correlation data, and waits. If the environment goes offline and back
// online while it waits, the request is republished once. No reply in time
// is an *OfflineError with reason "timeout". The returned Reply is the
// agent's, ok or not.
//
//nolint:gocritic // hugeParam: the CallRequest value is the fixed API.
func (c *Controller) Call(ctx context.Context, name string, req CallRequest) (envproto.Reply, error) {
	rec, err := c.enrolled(name)
	if err != nil {
		return envproto.Reply{}, err
	}
	spec, ok := envproto.LookupOp(req.Op)
	if !ok || spec.Caller != envproto.CallerController {
		return envproto.Reply{}, fmt.Errorf("%s: %w", req.Op, ErrNotForwarded)
	}
	if !envproto.ValidRequestID(req.RequestID) {
		return envproto.Reply{}, fmt.Errorf("request id %q isn't valid", req.RequestID)
	}
	if !c.isListening() {
		return envproto.Reply{}, offlineFor(name, &req, reasonNotListening, nil)
	}
	msg, err := requestMessage(rec.ID, &req, time.Until(req.Deadline))
	if err != nil {
		return envproto.Reply{}, err
	}
	pc := &pendingCall{
		env:      rec.ID,
		deadline: req.Deadline,
		msg:      msg,
		reply:    make(chan envproto.Reply, 1),
	}
	if err := c.admitCall(name, &req, spec.Since, pc); err != nil {
		return envproto.Reply{}, err
	}
	defer c.removeCall(req.RequestID)
	if err := c.broker.Publish(ctx, msg); err != nil {
		return envproto.Reply{}, fmt.Errorf("sending %s to %s: %w", req.Op, name, err)
	}
	timer := time.NewTimer(time.Until(req.Deadline.Add(c.callGrace)))
	defer timer.Stop()
	select {
	case reply := <-pc.reply:
		return reply, nil
	case <-timer.C:
		return envproto.Reply{}, offlineFor(name, &req, reasonTimeout, nil)
	case <-ctx.Done():
		return envproto.Reply{}, ctx.Err()
	}
}

// enrolled returns the enrolled environment called name, or ErrUnknown.
func (c *Controller) enrolled(name string) (Record, error) {
	rec, err := c.store.Get(name)
	if err != nil {
		return Record{}, err
	}
	if rec.State != StateEnrolled {
		return Record{}, ErrUnknown
	}
	return rec, nil
}

// admitCall registers pc as the call waiting for the reply to its request id.
// It refuses, without registering, a request id that is already pending, an
// environment that isn't online, an agent too old for the op, and a deadline
// that has passed. The checks and the registration share one hold of mu, so
// a status change can't land between them.
func (c *Controller) admitCall(name string, req *CallRequest, since int, pc *pendingCall) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, pending := c.calls[req.RequestID]; pending {
		return fmt.Errorf("%w: %q", ErrRequestPending, req.RequestID)
	}
	h := c.presence[pc.env]
	if !h.online {
		return offlineFor(name, req, reasonOffline, lastSeenOf(h))
	}
	if h.hello != nil && h.hello.Protocol < since {
		return &OutdatedError{
			Environment: name,
			Op:          req.Op,
			WSVersion:   h.hello.WSVersion,
			Protocol:    h.hello.Protocol,
			Need:        since,
		}
	}
	if time.Until(pc.deadline) <= 0 {
		return offlineFor(name, req, reasonTimeout, nil)
	}
	c.calls[req.RequestID] = pc
	return nil
}

// removeCall forgets the call waiting for the reply to reqID.
func (c *Controller) removeCall(reqID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.calls, reqID)
}

// deliver hands reply to the pending call with request id reqID that the
// environment id answers, and reports whether there is one.
func (c *Controller) deliver(id, reqID string, reply envproto.Reply) bool {
	c.mu.Lock()
	pc, ok := c.calls[reqID]
	c.mu.Unlock()
	if !ok || pc.env != id {
		return false
	}
	select {
	case pc.reply <- reply:
	default:
		// A second reply to the same request is dropped: the call has its answer.
	}
	return true
}

// republish sends again each pending call to the environment id that hasn't been sent again.
func (c *Controller) republish(ctx context.Context, id string) {
	for _, msg := range c.takeRepublish(id) {
		if err := c.broker.Publish(ctx, msg); err != nil {
			c.log.WarnContext(ctx, "call not republished", "id", id, "error", err)
		}
	}
}

// takeRepublish marks the pending calls to id that haven't been republished
// and returns their messages with the expiry they have left.
func (c *Controller) takeRepublish(id string) []envproto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var msgs []envproto.Message
	for _, pc := range c.calls {
		if pc.env != id || pc.republished {
			continue
		}
		pc.republished = true
		msg := pc.msg
		msg.Expiry = time.Until(pc.deadline)
		if msg.Expiry <= 0 {
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// requestMessage is req on env/<id>/req, answered on env/<id>/res/<request id>.
func requestMessage(id string, req *CallRequest, expiry time.Duration) (envproto.Message, error) {
	deadline := req.Deadline.UTC()
	payload, err := json.Marshal(envproto.Request{
		Op:        req.Op,
		Args:      req.Args,
		RequestID: req.RequestID,
		Deadline:  &deadline,
	})
	if err != nil {
		return envproto.Message{}, fmt.Errorf("encoding %s: %w", req.Op, err)
	}
	return envproto.Message{
		Topic:           envproto.Topic(id, envproto.LeafReq),
		Payload:         payload,
		QoS:             1,
		ResponseTopic:   envproto.Topic(id, envproto.LeafRes, req.RequestID),
		CorrelationData: []byte(req.RequestID),
		Expiry:          expiry,
	}, nil
}

// offlineFor is the OfflineError for req to environment name.
func offlineFor(name string, req *CallRequest, reason string, lastSeen *time.Time) *OfflineError {
	return &OfflineError{
		Environment: name,
		Reason:      reason,
		LastSeen:    lastSeen,
		RequestID:   req.RequestID,
		Op:          req.Op,
	}
}

// lastSeenOf returns when h was last heard from, or nil when never.
func lastSeenOf(h heard) *time.Time {
	if h.lastSeen.IsZero() {
		return nil
	}
	seen := h.lastSeen
	return &seen
}
