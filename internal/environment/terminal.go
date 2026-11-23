package environment

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// defaultAbandonedCloseWait bounds the close sent for a terminal OpenTerminal abandoned.
const defaultAbandonedCloseWait = 5 * time.Second

// ErrTerminalEnded is returned for input to a terminal that has ended.
var ErrTerminalEnded = errors.New("the terminal has ended")

// termKey names an open terminal: the environment id and the stream id.
type termKey struct {
	env string
	sid string
}

// outEntry is one piece of output waiting for the consumer.
type outEntry struct {
	b    []byte
	data bool // pty bytes, as opposed to a marker
}

// Terminal is one open remote terminal.
type Terminal struct {
	c    *Controller
	log  *slog.Logger
	env  string
	name string
	sid  string

	// ctx ends when Close is called; the goroutines publish on it.
	ctx    context.Context //nolint:containedctx // it bounds the terminal's goroutines, like a server's base context.
	cancel context.CancelFunc
	// endCh closes when the terminal ends, and stopped when its goroutines have returned.
	endCh   chan struct{}
	stopped chan struct{}
	wake    chan struct{}
	redraw  chan struct{}
	out     chan []byte

	// mu guards the fields below. It is never held across a broker call.
	mu       sync.Mutex
	lastSeq  uint64
	queue    []outEntry
	queued   int
	dropped  uint64 // bytes dropped that the next entry's marker announces
	redrawAt time.Time
	trailing *time.Timer // the redraw owed when a gap came within RedrawEvery of the last one.
	exit     envproto.TerminalExit
	ended    bool

	// inMu serializes everything sent on the in topic, so the sequence numbers
	// reach the agent in order.
	inMu  sync.Mutex
	inSeq uint64

	closeOnce sync.Once
	closeErr  error
}

// OpenTerminal sends terminal.open to name and returns the terminal once
// the agent answered. If ctx ends or the call times out after the agent
// replied ok, the terminal is closed on the agent in the background. Errors are Call's (OfflineError, OutdatedError, ...) or
// the agent's reply error.
//
// The terminal is registered when the reply is delivered, before the broker
// handler returns, so output the agent publishes right after its reply is not
// taken for output of an unknown stream.
func (c *Controller) OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (*Terminal, envproto.TerminalOpenResult, error) { //nolint:gocritic // hugeParam: the args value is the fixed API.
	if err := envproto.Validate(&args); err != nil {
		return nil, envproto.TerminalOpenResult{}, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, envproto.TerminalOpenResult{}, fmt.Errorf("encoding terminal.open: %w", err)
	}
	opened := &openedTerminal{}
	reply, err := c.callTerminal(ctx, name, envproto.OpTerminalOpen, raw, func(env string, reply envproto.Reply) {
		opened.register(c, env, name, reply)
	})
	term, result := opened.get()
	if err != nil {
		term.discard()
		if term != nil && result.SID != "" {
			go c.closeAbandoned(context.WithoutCancel(ctx), name, result.SID)
		}
		return nil, envproto.TerminalOpenResult{}, err
	}
	if !reply.OK {
		term.discard()
		return nil, envproto.TerminalOpenResult{}, replyFailure(name, envproto.OpTerminalOpen, reply)
	}
	if term == nil {
		return nil, envproto.TerminalOpenResult{}, fmt.Errorf("environment %s answered terminal.open without a usable terminal id", name)
	}
	return term, result, nil
}

// closeAbandoned asks the agent, best effort, to close a terminal it opened
// but whose open call ended without handing it to the caller.
func (c *Controller) closeAbandoned(parent context.Context, name, sid string) {
	ctx, cancel := context.WithTimeout(parent, c.abandonedCloseWait)
	defer cancel()
	raw, err := json.Marshal(envproto.TerminalCloseArgs{SID: sid})
	if err != nil {
		return
	}
	if _, err := c.callTerminal(ctx, name, envproto.OpTerminalClose, raw, nil); err != nil {
		c.log.DebugContext(ctx, "abandoned terminal not closed", "sid", sid, "error", err)
	}
}

// replyFailure is the error for a reply that isn't ok.
func replyFailure(name, op string, reply envproto.Reply) error {
	if reply.Error == nil {
		return fmt.Errorf("environment %s refused %s", name, op)
	}
	return reply.Error
}

// openedTerminal is the terminal OpenTerminal's reply registered, if any.
type openedTerminal struct {
	mu     sync.Mutex
	done   bool
	term   *Terminal
	result envproto.TerminalOpenResult
}

// register creates and registers the terminal a successful reply names, once.
func (o *openedTerminal) register(c *Controller, env, name string, reply envproto.Reply) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done || !reply.OK {
		return
	}
	o.done = true
	var result envproto.TerminalOpenResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || !envproto.ValidSID(result.SID) {
		c.log.Warn("terminal.open answered without a usable terminal id", "id", env)
		return
	}
	term := c.newTerminal(env, name, result.SID)
	if !c.register(term) {
		term.cancel()
		return
	}
	o.term, o.result = term, result
	term.start()
}

// get returns the registered terminal and the agent's result.
func (o *openedTerminal) get() (*Terminal, envproto.TerminalOpenResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.term, o.result
}

// newTerminal returns a terminal of environment env that isn't registered or running.
func (c *Controller) newTerminal(env, name, sid string) *Terminal {
	ctx, cancel := context.WithCancel(context.Background())
	return &Terminal{
		c:       c,
		log:     c.log,
		env:     env,
		name:    name,
		sid:     sid,
		ctx:     ctx,
		cancel:  cancel,
		endCh:   make(chan struct{}),
		stopped: make(chan struct{}),
		wake:    make(chan struct{}, 1),
		redraw:  make(chan struct{}, 1),
		out:     make(chan []byte),
	}
}

// register adds term to the open terminals, unless one has its key.
func (c *Controller) register(term *Terminal) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := termKey{env: term.env, sid: term.sid}
	if _, taken := c.terminals[key]; taken {
		return false
	}
	c.terminals[key] = term
	return true
}

// unregister removes term from the open terminals.
func (c *Controller) unregister(term *Terminal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.terminals, termKey{env: term.env, sid: term.sid})
}

// terminalCount is how many terminals are open.
func (c *Controller) terminalCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.terminals)
}

// terminalsOf takes the open terminals of environment env, or of every
// environment when env is "".
func (c *Controller) terminalsOf(env string) []*Terminal {
	c.mu.Lock()
	defer c.mu.Unlock()
	var found []*Terminal
	for key, term := range c.terminals {
		if env == "" || key.env == env {
			found = append(found, term)
		}
	}
	return found
}

// endTerminals ends the terminals of environment env ("" for all) as disconnected.
// With stop it also cancels their contexts, which drops output not yet read.
func (c *Controller) endTerminals(env string, stop bool) {
	for _, term := range c.terminalsOf(env) {
		term.finish(envproto.TerminalExit{Reason: "disconnected"})
		if stop {
			term.cancel()
		}
	}
}

// onStreamOut routes a frame the agent published to its terminal. It never
// blocks: the broker's handler may run on the agent's connection.
func (c *Controller) onStreamOut(ctx context.Context, msg *envproto.Message) {
	id, rest, ok := envproto.ParseTopic(msg.Topic)
	if !ok || len(rest) != 3 || rest[0] != envproto.LeafStream || rest[2] != "out" || !envproto.ValidSID(rest[1]) {
		return
	}
	c.mu.Lock()
	term := c.terminals[termKey{env: id, sid: rest[1]}]
	c.mu.Unlock()
	if term == nil {
		c.log.DebugContext(ctx, "output for no open terminal", "id", id, "sid", rest[1])
		return
	}
	term.receive(ctx, msg.Payload)
}

// callTerminal sends op, which only the terminal path may send, to the environment called name.
func (c *Controller) callTerminal(ctx context.Context, name, op string, args json.RawMessage, onReply func(env string, reply envproto.Reply)) (envproto.Reply, error) {
	spec, ok := envproto.LookupOp(op)
	if !ok {
		return envproto.Reply{}, fmt.Errorf("%s: %w", op, ErrNotForwarded)
	}
	reqID, err := newRequestID()
	if err != nil {
		return envproto.Reply{}, err
	}
	req := CallRequest{Op: op, Args: args, RequestID: reqID, Deadline: time.Now().Add(spec.Timeout)}
	return c.call(ctx, name, &req, envproto.CallerTerminal, onReply)
}

// receive handles one frame from the agent.
func (t *Terminal) receive(ctx context.Context, payload []byte) {
	frame, err := envproto.UnmarshalFrame(payload)
	if err != nil {
		t.log.DebugContext(ctx, "terminal frame refused", "sid", t.sid, "error", err)
		return
	}
	if exit := t.apply(ctx, &frame); exit != nil {
		t.finish(*exit)
	}
}

// apply queues what frame says to show and returns the exit it carries, if any.
// A sequence number far ahead of the last one is taken at its word: the agent
// is authenticated, but one huge number makes every later frame look like a
// duplicate, so the stream stays dead until Close or the environment going offline.
func (t *Terminal) apply(ctx context.Context, frame *envproto.Frame) *envproto.TerminalExit {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended || frame.Seq <= t.lastSeq {
		return nil
	}
	gap := frame.Seq > t.lastSeq+1
	t.lastSeq = frame.Seq
	if gap && frame.Kind != envproto.FrameExit {
		t.pushLocked(envproto.LostMarker(), false)
		t.wantRedrawLocked()
	}
	switch frame.Kind {
	case envproto.FrameData:
		t.pushLocked(append([]byte(nil), frame.Body...), true)
	case envproto.FrameDropped:
		t.pushLocked(envproto.DroppedMarker(binary.BigEndian.Uint64(frame.Body)), false)
	case envproto.FrameExit:
		return exitOf(ctx, t.log, frame.Body)
	case envproto.FrameInput, envproto.FrameResize, envproto.FrameRedraw, envproto.FramePing, envproto.FrameClose:
		// Input kinds only travel to the agent; one from it is ignored.
	}
	return nil
}

// exitOf decodes an exit frame's body; a body that isn't JSON is a plain exit.
func exitOf(ctx context.Context, log *slog.Logger, body []byte) *envproto.TerminalExit {
	var exit envproto.TerminalExit
	if err := json.Unmarshal(body, &exit); err != nil {
		log.DebugContext(ctx, "exit frame refused", "error", err)
		return &envproto.TerminalExit{Reason: "exited"}
	}
	return &exit
}

// pushLocked queues b for the consumer, dropping the oldest output over
// MaxQueuedOutput. t.mu must be held.
func (t *Terminal) pushLocked(b []byte, data bool) {
	for t.queued+len(b) > envproto.MaxQueuedOutput && len(t.queue) > 0 {
		oldest := t.queue[0]
		t.queue[0] = outEntry{}
		t.queue = t.queue[1:]
		t.queued -= len(oldest.b)
		if oldest.data {
			t.dropped += uint64(len(oldest.b))
		}
	}
	t.queue = append(t.queue, outEntry{b: b, data: data})
	t.queued += len(b)
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// wantRedrawLocked asks keepAlive for a redraw, at most once per RedrawEvery.
// A gap inside the window gets one trailing redraw when the window ends, so a
// storm followed by silence still ends in a repaired screen. t.mu must be held.
func (t *Terminal) wantRedrawLocked() {
	now := t.c.now()
	if !t.redrawAt.IsZero() && now.Sub(t.redrawAt) < envproto.RedrawEvery {
		if t.trailing == nil {
			wait := envproto.RedrawEvery - now.Sub(t.redrawAt)
			t.trailing = time.AfterFunc(wait, t.trailingRedraw)
		}
		return
	}
	t.redrawAt = now
	t.signalRedraw()
}

// trailingRedraw asks for the redraw a suppressed gap is owed.
func (t *Terminal) trailingRedraw() {
	t.mu.Lock()
	t.trailing = nil
	if t.ended {
		t.mu.Unlock()
		return
	}
	t.redrawAt = t.c.now()
	t.mu.Unlock()
	t.signalRedraw()
}

func (t *Terminal) signalRedraw() {
	select {
	case t.redraw <- struct{}{}:
	default:
	}
}

// start runs the goroutines that move output to the consumer and send pings and redraws.
func (t *Terminal) start() {
	tick, stop := t.c.newTicker(envproto.PingInterval)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		t.pump()
	}()
	go func() {
		defer wg.Done()
		defer stop()
		t.keepAlive(tick)
	}()
	go func() {
		wg.Wait()
		close(t.stopped)
	}()
}

// pump moves queued output into the Output channel, then closes it once the
// terminal has ended and the queue is empty or Close was called.
func (t *Terminal) pump() {
	defer close(t.out)
	for {
		b, more := t.pop()
		if b != nil {
			select {
			case t.out <- b:
			case <-t.ctx.Done():
				return
			}
			continue
		}
		if !more {
			return
		}
		select {
		case <-t.wake:
		case <-t.ctx.Done():
			return
		}
	}
}

// pop returns the next output, announcing dropped bytes first. more is false
// when nothing is queued and the terminal has ended.
func (t *Terminal) pop() (b []byte, more bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dropped > 0 {
		n := t.dropped
		t.dropped = 0
		return envproto.DroppedMarker(n), true
	}
	if len(t.queue) == 0 {
		return nil, !t.ended
	}
	entry := t.queue[0]
	t.queue[0] = outEntry{}
	t.queue = t.queue[1:]
	t.queued -= len(entry.b)
	return entry.b, true
}

// keepAlive sends a ping on every tick and a redraw when one was asked for,
// until the terminal ends.
func (t *Terminal) keepAlive(tick <-chan time.Time) {
	for {
		var kind envproto.FrameKind
		select {
		case <-tick:
			kind = envproto.FramePing
		case <-t.redraw:
			kind = envproto.FrameRedraw
		case <-t.endCh:
			return
		case <-t.ctx.Done():
			return
		}
		if err := t.send(t.ctx, kind, nil); err != nil && !errors.Is(err, ErrTerminalEnded) {
			t.log.WarnContext(t.ctx, "terminal frame not sent", "sid", t.sid, "kind", string(rune(kind)), "error", err)
		}
	}
}

// Output is the bytes to show, in order, markers included; it is closed
// when the terminal ends and its queued output has been read, or on Close.
func (t *Terminal) Output() <-chan []byte {
	return t.out
}

// Exit is the agent's exit frame, or a synthesized one ("disconnected" when
// the environment went offline, "closed" after Close). It is valid once Output
// has closed.
func (t *Terminal) Exit() envproto.TerminalExit {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exit
}

// finish ends the terminal with exit unless it already ended.
func (t *Terminal) finish(exit envproto.TerminalExit) {
	t.mu.Lock()
	if t.ended {
		t.mu.Unlock()
		return
	}
	t.ended = true
	t.exit = exit
	close(t.endCh)
	if t.trailing != nil {
		t.trailing.Stop()
		t.trailing = nil
	}
	t.mu.Unlock()
	t.c.unregister(t)
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// discard drops a terminal OpenTerminal registered but couldn't return.
func (t *Terminal) discard() {
	if t == nil {
		return
	}
	t.finish(envproto.TerminalExit{Reason: "closed"})
	t.cancel()
}

// isEnded reports whether the terminal has ended.
func (t *Terminal) isEnded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ended
}

// send publishes one frame on the in topic, QoS 1, with the next sequence
// number. A publish that fails has spent its sequence number, and a later
// frame would look like a gap, which the agent answers by closing the terminal
// rather than run lost keystrokes; so a failure ends the terminal here too.
func (t *Terminal) send(ctx context.Context, kind envproto.FrameKind, body []byte) error {
	t.inMu.Lock()
	defer t.inMu.Unlock()
	return t.sendLocked(ctx, kind, body)
}

// sendLocked is send with t.inMu held.
func (t *Terminal) sendLocked(ctx context.Context, kind envproto.FrameKind, body []byte) error {
	if t.isEnded() {
		return ErrTerminalEnded
	}
	payload, err := envproto.MarshalFrame(envproto.Frame{Kind: kind, Seq: t.inSeq + 1, Body: body})
	if err != nil {
		return err
	}
	t.inSeq++
	err = t.c.broker.Publish(ctx, envproto.Message{
		Topic:   envproto.Topic(t.env, envproto.LeafStream, t.sid, "in"),
		Payload: payload,
		QoS:     1,
	})
	if err != nil {
		t.finish(envproto.TerminalExit{Reason: "disconnected"})
		return fmt.Errorf("sending to terminal %s: %w", t.sid, err)
	}
	return nil
}

// Input publishes one input frame per MaxInputBytes slice of p at QoS 1,
// waiting for each publish. Concurrent calls don't interleave. A publish that
// fails, including one cut short by ctx, ends the whole terminal, so callers
// must pass a long-lived ctx, not a request-scoped one.
func (t *Terminal) Input(ctx context.Context, p []byte) error {
	t.inMu.Lock()
	defer t.inMu.Unlock()
	for len(p) > 0 {
		n := min(len(p), envproto.MaxInputBytes)
		if err := t.sendLocked(ctx, envproto.FrameInput, p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// Resize publishes a resize frame; a size outside 1 to MaxTermSize is refused.
// Like Input, it ends the terminal when the publish fails, so ctx must be long-lived.
func (t *Terminal) Resize(ctx context.Context, cols, rows int) error {
	if cols < 1 || cols > envproto.MaxTermSize || rows < 1 || rows > envproto.MaxTermSize {
		return fmt.Errorf("a terminal size is 1 to %d: %dx%d", envproto.MaxTermSize, cols, rows)
	}
	return t.send(ctx, envproto.FrameResize, envproto.ResizeBody(cols, rows))
}

// Close sends a close frame and terminal.close, and ends the terminal. It is
// safe to call twice. A terminal that already ended sends nothing.
func (t *Terminal) Close(ctx context.Context) error {
	t.closeOnce.Do(func() { t.closeErr = t.closeNow(ctx) })
	return t.closeErr
}

func (t *Terminal) closeNow(ctx context.Context) error {
	var err error
	if !t.isEnded() {
		err = t.notifyClose(ctx)
	}
	t.finish(envproto.TerminalExit{Reason: "closed"})
	t.cancel()
	return err
}

// notifyClose sends the close frame and the terminal.close request.
func (t *Terminal) notifyClose(ctx context.Context) error {
	frameErr := t.send(ctx, envproto.FrameClose, nil)
	if errors.Is(frameErr, ErrTerminalEnded) {
		return nil
	}
	raw, err := json.Marshal(envproto.TerminalCloseArgs{SID: t.sid})
	if err != nil {
		return err
	}
	reply, callErr := t.c.callTerminal(ctx, t.name, envproto.OpTerminalClose, raw, nil)
	if callErr == nil && !reply.OK {
		callErr = replyFailure(t.name, envproto.OpTerminalClose, reply)
	}
	return errors.Join(frameErr, callErr)
}
