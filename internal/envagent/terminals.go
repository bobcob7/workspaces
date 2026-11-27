package envagent

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/ptyproc"
)

// Reasons a terminal ends, as TerminalExit.Reason names them.
const (
	reasonExited       = "exited"
	reasonClosed       = "closed"
	reasonIdle         = "idle"
	reasonInputGap     = "input_gap"
	reasonInputLimit   = "input_limit"
	reasonDisconnected = "disconnected"
)

const (
	// idleCheck is how often the manager looks for idle terminals.
	idleCheck = 5 * time.Second
	// redrawDelay is how long a redraw holds the pty one row taller.
	redrawDelay = 50 * time.Millisecond
	// redrawLowWater is the queued output under which the agent redraws after a drop.
	redrawLowWater = 256 << 10
	// inputBacklog is how many validated frames wait for a terminal's writer.
	// It holds two seconds of frames at envproto.InputLimitFrames, so a pty
	// that stops reading never turns input inside the rate into input_limit.
	// The byte bucket allows at most envproto.InputLimitBytes of bodies, so the
	// queued bodies stay near that however many frames wait.
	inputBacklog = 2 * envproto.InputLimitFrames
	// finishWait bounds how long finish waits on the broker for the last output
	// and, separately, for the exit frame.
	finishWait = 5 * time.Second
	// frameWireMax is the largest message on a stream topic.
	frameWireMax = envproto.MaxChunkBytes + 10
	// sidBytes is the random bytes in a sid.
	sidBytes = 8
)

// terminals is the agent's open terminals: the streams between a pty and the
// controller (ADR 0074, D2, D6, D10). Its frame handler only enqueues, so it
// never blocks the broker connection.
type terminals struct {
	log         *slog.Logger
	client      mqttClient
	starter     ptyStarter
	supported   func() bool
	commands    terminalCommands
	random      io.Reader
	now         func() time.Time
	after       func(time.Duration) <-chan time.Time
	max         int
	publishWait time.Duration
	redrawDelay time.Duration
	finishWait  time.Duration
	mu          sync.Mutex
	byID        map[string]*terminal
	reserved    int // terminals open or being opened
	envID       string
	pubCtx      context.Context //nolint:containedctx // the session's context without its cancellation, for the exit frames.
}

// newTerminals returns a manager that opens at most max terminals.
func newTerminals(log *slog.Logger, client mqttClient, commands terminalCommands, random io.Reader, opts *Options) *terminals {
	limit := opts.MaxTerminals
	if limit <= 0 {
		limit = envproto.MaxTerminals
	}
	return &terminals{
		log:         log,
		client:      client,
		starter:     ptyprocStarter{},
		supported:   ptyproc.Supported,
		commands:    commands,
		random:      random,
		now:         opts.Now,
		after:       opts.After,
		max:         limit,
		publishWait: max(opts.RequestTimeout, time.Second),
		redrawDelay: redrawDelay,
		finishWait:  finishWait,
		byID:        make(map[string]*terminal),
		pubCtx:      context.Background(),
	}
}

// ptyprocStarter starts processes with ptyproc.
type ptyprocStarter struct{}

// Start runs spec on a new pseudo-terminal.
//
//nolint:gocritic // hugeParam: Start has the ptyStarter signature.
func (ptyprocStarter) Start(spec ptyproc.Spec) (ptyProcess, error) {
	p, err := ptyproc.Start(spec)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// begin starts serving one connection: the terminals publish as environment
// id, and the idle check runs until ctx ends.
func (m *terminals) begin(ctx context.Context, id string) {
	m.mu.Lock()
	m.envID = id
	m.pubCtx = context.WithoutCancel(ctx)
	m.mu.Unlock()
	go m.idleLoop(ctx)
}

// idleLoop closes terminals that have had no frame for envproto.IdleClose.
func (m *terminals) idleLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.after(idleCheck):
			m.closeIdle()
		}
	}
}

// closeIdle closes every terminal whose last frame is older than envproto.IdleClose.
func (m *terminals) closeIdle() {
	now := m.now()
	for _, t := range m.snapshot() {
		if now.Sub(t.lastFrame()) >= envproto.IdleClose {
			t.close(reasonIdle)
		}
	}
}

// snapshot is the open terminals.
func (m *terminals) snapshot() []*terminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*terminal, 0, len(m.byID))
	for _, t := range m.byID {
		out = append(out, t)
	}
	return out
}

// closeAll closes every terminal with reason disconnected and waits until
// each has published its exit frame and stopped its goroutines.
func (m *terminals) closeAll() {
	for _, t := range m.snapshot() {
		t.close(reasonDisconnected)
	}
	for _, t := range m.snapshot() {
		<-t.finished
	}
}

// lookup is the terminal named sid, nil when this agent didn't open it.
func (m *terminals) lookup(sid string) *terminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[sid]
}

// reserve takes one of the agent's terminal slots, reporting false when all are taken.
func (m *terminals) reserve() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reserved >= m.max {
		return false
	}
	m.reserved++
	return true
}

// release gives a slot back.
func (m *terminals) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved--
}

// newSID is 16 lowercase hex characters from the agent's random source, not
// in use by an open terminal.
func (m *terminals) newSID() (string, error) {
	for {
		b := make([]byte, sidBytes)
		if _, err := io.ReadFull(m.random, b); err != nil {
			return "", err
		}
		sid := hex.EncodeToString(b)
		if m.lookup(sid) == nil {
			return sid, nil
		}
	}
}

// open starts the terminal args asks for and returns its result.
func (m *terminals) open(ctx context.Context, args *envproto.TerminalOpenArgs) (json.RawMessage, error) {
	if !m.reserve() {
		return nil, &envproto.ReplyError{Code: "terminal_limit", Message: "this agent already has the most terminals it allows"}
	}
	t, cmd, err := m.start(ctx, args)
	if err != nil {
		m.release()
		return nil, err
	}
	m.mu.Lock()
	m.byID[t.sid] = t
	m.mu.Unlock()
	t.run() //nolint:contextcheck // the terminal outlives the request that opened it.
	return json.Marshal(envproto.TerminalOpenResult{SID: t.sid, Workspace: cmd.Workspace, Session: cmd.Session})
}

// errPlatformUnsupported refuses a terminal on a system without pseudo-terminals
// (ADR 0086: any OS but linux, darwin and Windows 10 1809+).
func errPlatformUnsupported() *envproto.ReplyError {
	return &envproto.ReplyError{Code: "platform_unsupported", Message: "terminals aren't supported on " + runtime.GOOS + " agents: no pseudo-terminals (ADR 0086)"}
}

// start builds the command and starts it on a pty, without registering it.
func (m *terminals) start(ctx context.Context, args *envproto.TerminalOpenArgs) (*terminal, TerminalCommand, error) {
	if !m.supported() {
		return nil, TerminalCommand{}, errPlatformUnsupported()
	}
	cmd, err := m.commands.TerminalCommand(ctx, args)
	if err != nil {
		return nil, TerminalCommand{}, err
	}
	sid, err := m.newSID()
	if err != nil {
		return nil, TerminalCommand{}, err
	}
	proc, err := m.starter.Start(ptyproc.Spec{Path: cmd.Path, Args: cmd.Args, Dir: cmd.Dir, Env: cmd.Env, Cols: args.Cols, Rows: args.Rows})
	if err != nil {
		m.log.WarnContext(ctx, "terminal not started", "error", err)
		if errors.Is(err, ptyproc.ErrUnsupported) {
			return nil, TerminalCommand{}, errPlatformUnsupported()
		}
		return nil, TerminalCommand{}, err
	}
	return newTerminal(m, sid, proc, args.Cols, args.Rows), cmd, nil
}

// closeSID closes the terminal sid with reason closed and waits for its exit
// frame, reporting false when no such terminal is open.
func (m *terminals) closeSID(ctx context.Context, sid string) bool {
	t := m.lookup(sid)
	if t == nil {
		return false
	}
	t.close(reasonClosed)
	select {
	case <-t.finished:
	case <-ctx.Done():
	}
	return true
}

// onStreamIn handles a message on stream/<sid>/in. It runs on the broker
// client's goroutine and only validates and enqueues.
func (m *terminals) onStreamIn(msg *envproto.Message) {
	m.mu.Lock()
	envID := m.envID
	m.mu.Unlock()
	id, rest, ok := envproto.ParseTopic(msg.Topic)
	if !ok || id != envID || len(rest) != 3 || rest[0] != envproto.LeafStream || rest[2] != "in" {
		return
	}
	t := m.lookup(rest[1])
	if t == nil {
		m.log.Debug("stream input for an unknown terminal dropped")
		return
	}
	t.receive(msg.Payload)
}

// terminalOp runs terminal.open or terminal.close.
func (a *Agent) terminalOp(ctx context.Context, op string, args any) (json.RawMessage, error) {
	if a.terms == nil {
		return nil, &envproto.ReplyError{Code: "usage", Message: "terminals aren't available on this agent"}
	}
	switch v := args.(type) {
	case *envproto.TerminalOpenArgs:
		return a.terms.open(ctx, v)
	case *envproto.TerminalCloseArgs:
		return json.Marshal(envproto.TerminalCloseResult{SID: v.SID, Closed: a.terms.closeSID(ctx, v.SID)})
	}
	return nil, &envproto.ReplyError{Code: "usage", Message: "unknown operation " + op}
}

// bucket is a token bucket that refills at rate per second up to capacity.
type bucket struct {
	tokens   float64
	capacity float64
	rate     float64
	at       time.Time
}

// newBucket returns a full bucket.
func newBucket(capacity float64, now time.Time) bucket {
	return bucket{tokens: capacity, capacity: capacity, rate: capacity, at: now}
}

// take removes n tokens, reporting false (and taking none) when fewer remain.
func (b *bucket) take(now time.Time, n float64) bool {
	if elapsed := now.Sub(b.at).Seconds(); elapsed > 0 {
		b.tokens = min(b.capacity, b.tokens+elapsed*b.rate)
		b.at = now
	}
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// terminal is one process on a pty and its streams.
type terminal struct {
	m        *terminals
	sid      string
	proc     ptyProcess
	stop     chan struct{} // closed when the terminal starts to end
	finished chan struct{} // closed when its exit frame is published
	inputs   chan envproto.Frame
	wake     chan struct{}
	readDone chan struct{}
	inDone   chan struct{}
	bg       sync.WaitGroup // the redraw goroutines
	mu       sync.Mutex
	reason   string
	closed   bool
	nextIn   uint64
	last     time.Time
	redrawAt time.Time
	cols     int
	rows     int
	bytesIn  bucket
	framesIn bucket
	queue    [][]byte
	queued   int
	dropped  uint64
}

// newTerminal wraps proc, which has just been started at cols x rows.
func newTerminal(m *terminals, sid string, proc ptyProcess, cols, rows int) *terminal {
	now := m.now()
	return &terminal{
		m:        m,
		sid:      sid,
		proc:     proc,
		stop:     make(chan struct{}),
		finished: make(chan struct{}),
		inputs:   make(chan envproto.Frame, inputBacklog),
		wake:     make(chan struct{}, 1),
		readDone: make(chan struct{}),
		inDone:   make(chan struct{}),
		nextIn:   1,
		last:     now,
		cols:     cols,
		rows:     rows,
		bytesIn:  newBucket(envproto.InputLimitBytes, now),
		framesIn: newBucket(envproto.InputLimitFrames, now),
	}
}

// run starts the terminal's goroutines.
func (t *terminal) run() {
	go t.read()
	go t.write()
	go t.publish()
}

// close starts ending the terminal with reason; the first reason wins. It
// never blocks: the publisher goroutine does the work.
func (t *terminal) close(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.reason = reason
	close(t.stop)
}

// lastFrame is when the last frame arrived.
func (t *terminal) lastFrame() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// receive validates one message from stream/<sid>/in and queues it for the writer.
func (t *terminal) receive(payload []byte) {
	if reason := t.admit(payload); reason != "" {
		t.close(reason)
	}
}

// admit decodes and checks a frame, queueing it for the writer. It returns
// the reason to close the terminal for, or "" when the frame is fine.
func (t *terminal) admit(payload []byte) string {
	if len(payload) > frameWireMax {
		return reasonInputLimit
	}
	f, err := envproto.UnmarshalFrame(payload)
	if err != nil || !inputKind(f.Kind) {
		return reasonInputLimit
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if f.Seq != t.nextIn {
		return reasonInputGap
	}
	t.nextIn++
	now := t.m.now()
	t.last = now
	if !t.framesIn.take(now, 1) || !t.bytesIn.take(now, float64(len(f.Body))) {
		return reasonInputLimit
	}
	f.Body = append([]byte(nil), f.Body...)
	select {
	case t.inputs <- f:
		return ""
	default:
		return reasonInputLimit
	}
}

// inputKind reports whether kind is one a controller may send.
func inputKind(kind envproto.FrameKind) bool {
	switch kind { //nolint:exhaustive // every other kind is an output kind.
	case envproto.FrameInput, envproto.FrameResize, envproto.FrameRedraw, envproto.FramePing, envproto.FrameClose:
		return true
	}
	return false
}

// write applies the queued input frames to the pty, in order.
func (t *terminal) write() {
	defer close(t.inDone)
	for {
		select {
		case <-t.stop:
			return
		case f := <-t.inputs:
			t.apply(f)
		}
	}
}

// apply carries out one validated input frame.
func (t *terminal) apply(f envproto.Frame) {
	switch f.Kind { //nolint:exhaustive // admit refused every output kind.
	case envproto.FrameInput:
		if _, err := t.proc.Write(f.Body); err != nil {
			t.close(reasonExited)
		}
	case envproto.FrameResize:
		t.resize(f.Body)
	case envproto.FrameRedraw:
		t.redraw()
	case envproto.FrameClose:
		t.close(reasonClosed)
	case envproto.FramePing:
	}
}

// resize applies a resize frame's body, closing the terminal on a bad size.
func (t *terminal) resize(body []byte) {
	cols, rows, err := envproto.ParseResize(body)
	if err != nil {
		t.close(reasonInputLimit)
		return
	}
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	t.mu.Unlock()
	if err := t.proc.Resize(cols, rows); err != nil {
		t.m.log.Debug("terminal not resized", "error", err)
	}
}

// redraw makes the program repaint by growing the pty by one row and back,
// at most once per envproto.RedrawEvery. It reports whether it did.
func (t *terminal) redraw() bool {
	t.mu.Lock()
	now := t.m.now()
	if !t.redrawAt.IsZero() && now.Sub(t.redrawAt) < envproto.RedrawEvery {
		t.mu.Unlock()
		return false
	}
	t.redrawAt = now
	cols, rows := t.cols, t.rows
	t.mu.Unlock()
	if err := t.proc.Resize(cols, rows+1); err != nil {
		t.m.log.Debug("terminal not resized", "error", err)
		return true
	}
	t.bg.Go(func() {
		select {
		case <-t.stop:
		case <-time.After(t.m.redrawDelay):
		}
		if err := t.proc.Resize(cols, rows); err != nil {
			t.m.log.Debug("terminal not resized", "error", err)
		}
	})
	return true
}

// read copies the pty's output into the queue until the process ends.
func (t *terminal) read() {
	defer close(t.readDone)
	buf := make([]byte, envproto.MaxChunkBytes)
	for {
		n, err := t.proc.Read(buf)
		if n > 0 {
			t.push(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			t.close(reasonExited)
			return
		}
	}
}

// push queues a chunk, dropping the oldest chunks when the queue would pass
// envproto.MaxQueuedOutput. It never blocks.
func (t *terminal) push(chunk []byte) {
	t.mu.Lock()
	for t.queued+len(chunk) > envproto.MaxQueuedOutput && len(t.queue) > 0 {
		t.dropped += uint64(len(t.queue[0]))
		t.queued -= len(t.queue[0])
		t.queue = t.queue[1:]
	}
	t.queue = append(t.queue, chunk)
	t.queued += len(chunk)
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// pop takes the oldest chunk and the bytes dropped since the last pop.
func (t *terminal) pop() (chunk []byte, dropped uint64, queued int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.queue) == 0 {
		return nil, 0, 0, false
	}
	dropped, t.dropped = t.dropped, 0
	chunk = t.queue[0]
	t.queue = t.queue[1:]
	t.queued -= len(chunk)
	return chunk, dropped, t.queued, true
}

// publish sends the queued output, then, once the terminal ends, the exit
// frame, and unregisters it. It owns the output sequence.
func (t *terminal) publish() {
	defer close(t.finished)
	var seq uint64
	var retry <-chan time.Time
	redrawWanted := false
	for stopped := false; !stopped; {
		select {
		case <-t.stop:
			stopped = true
		case <-t.wake:
			redrawWanted = t.flush(t.m.baseCtx(), &seq, redrawWanted)
		case <-retry:
			retry = nil
		}
		if redrawWanted && t.lowWater() && t.redraw() {
			redrawWanted = false
		} else if redrawWanted && retry == nil {
			retry = t.m.after(envproto.RedrawEvery)
		}
	}
	t.finish(&seq)
}

// lowWater reports whether the queue is under redrawLowWater.
func (t *terminal) lowWater() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.queued < redrawLowWater
}

// flush publishes everything queued, a dropped frame before the first chunk
// after a drop. It returns true when a drop wants a redraw.
func (t *terminal) flush(ctx context.Context, seq *uint64, redrawWanted bool) bool {
	for ctx.Err() == nil {
		chunk, dropped, _, ok := t.pop()
		if dropped > 0 {
			t.send(ctx, seq, envproto.FrameDropped, dropBody(dropped))
			redrawWanted = true
		}
		if !ok {
			return redrawWanted
		}
		t.send(ctx, seq, envproto.FrameData, chunk)
	}
	return redrawWanted
}

// dropBody is a dropped frame's body: the count as a big-endian uint64.
func dropBody(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

// send publishes one output frame at QoS 0. A failure is logged and the
// frame is lost, which the receiver sees as a gap.
func (t *terminal) send(base context.Context, seq *uint64, kind envproto.FrameKind, body []byte) {
	*seq++
	payload, err := envproto.MarshalFrame(envproto.Frame{Kind: kind, Seq: *seq, Body: body})
	if err != nil {
		t.m.log.ErrorContext(base, "terminal frame not encoded", "error", err)
		return
	}
	t.m.mu.Lock()
	envID := t.m.envID
	t.m.mu.Unlock()
	ctx, cancel := context.WithTimeout(base, t.m.publishWait)
	defer cancel()
	topic := envproto.Topic(envID, envproto.LeafStream, t.sid, "out")
	if err := t.m.client.Publish(ctx, envproto.Message{Topic: topic, Payload: payload}); err != nil {
		t.m.log.DebugContext(ctx, "terminal frame not published", "error", err)
	}
}

// baseCtx is the context the terminals publish under.
func (m *terminals) baseCtx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pubCtx
}

// finish ends the process, flushes the output left and publishes the exit
// frame. Each of the two waits at most finishWait on the broker, so a stalled
// broker can't hold the session loop in closeAll for long.
func (t *terminal) finish(seq *uint64) {
	if err := t.proc.Close(); err != nil {
		t.m.log.Debug("terminal not closed", "error", err)
	}
	code := t.proc.Wait()
	<-t.readDone
	<-t.inDone
	t.bg.Wait()
	flushCtx, cancelFlush := context.WithTimeout(t.m.baseCtx(), t.m.finishWait)
	t.flush(flushCtx, seq, false)
	cancelFlush()
	t.mu.Lock()
	reason := t.reason
	t.mu.Unlock()
	body, err := json.Marshal(envproto.TerminalExit{Code: code, Reason: reason})
	if err != nil {
		t.m.log.Error("terminal exit not encoded", "error", err)
	} else {
		exitCtx, cancelExit := context.WithTimeout(t.m.baseCtx(), t.m.finishWait)
		t.send(exitCtx, seq, envproto.FrameExit, body)
		cancelExit()
	}
	t.m.mu.Lock()
	delete(t.m.byID, t.sid)
	t.m.reserved--
	t.m.mu.Unlock()
}
