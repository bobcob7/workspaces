package envagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/ptyproc"
)

// termEnvID is the environment id of the manager tests.
const termEnvID = "env-00000001"

// fakeProc is a process on a pty: the test writes its output to out and reads
// what the agent wrote to it.
type fakeProc struct {
	r       *io.PipeReader
	out     *io.PipeWriter
	ended   chan struct{}
	endOnce sync.Once
	mu      sync.Mutex
	written bytes.Buffer
	resizes [][2]int
	closed  bool
	exit    *int
	gate    atomic.Pointer[chan struct{}]
	reads   atomic.Int64 // Read calls started.
	writes  atomic.Int64 // Chunks written through writeOutput.
}

// blockWrites makes Write wait until the returned func is called or the process ends.
func (p *fakeProc) blockWrites() (release func()) {
	gate := make(chan struct{})
	p.gate.Store(&gate)
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{r: r, out: w, ended: make(chan struct{})}
}

func (p *fakeProc) Read(b []byte) (int, error) {
	p.reads.Add(1)
	return p.r.Read(b)
}

// writeOutput writes n chunks to the process's output and waits until the
// agent has queued all of them. The agent pushes a chunk after Read returns
// and before the next Read starts, so once Read has started one more time than
// chunks were written, the last push is done; without that the last pushes can
// land after the test acts.
func (p *fakeProc) writeOutput(t *testing.T, chunk []byte, n int) {
	t.Helper()
	for range n {
		_, err := p.out.Write(chunk)
		require.NoError(t, err)
		p.writes.Add(1)
	}
	require.Eventually(t, func() bool { return p.reads.Load() > p.writes.Load() }, 5*time.Second, time.Millisecond)
}

func (p *fakeProc) Write(b []byte) (int, error) {
	if gate := p.gate.Load(); gate != nil {
		select {
		case <-*gate:
		case <-p.ended:
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	return p.written.Write(b)
}

func (p *fakeProc) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]int{cols, rows})
	return nil
}

func (p *fakeProc) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.end()
	return nil
}

func (p *fakeProc) Wait() *int {
	<-p.ended
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit
}

// end ends the process: its output reaches EOF and Wait returns.
func (p *fakeProc) end() {
	p.endOnce.Do(func() {
		_ = p.out.Close()
		_ = p.r.Close()
		close(p.ended)
	})
}

// exitWith simulates the process exiting with code.
func (p *fakeProc) exitWith(code int) {
	p.mu.Lock()
	p.exit = &code
	p.mu.Unlock()
	p.end()
}

func (p *fakeProc) input() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.written.String()
}

func (p *fakeProc) sizes() [][2]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][2]int(nil), p.resizes...)
}

func (p *fakeProc) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// procList is the processes a fake starter has started, in order.
type procList struct {
	mu    sync.Mutex
	procs []*fakeProc
	specs []ptyproc.Spec
}

func (l *procList) starter() *ptyStarterMock {
	return &ptyStarterMock{StartFunc: func(spec ptyproc.Spec) (ptyProcess, error) {
		p := newFakeProc()
		l.mu.Lock()
		defer l.mu.Unlock()
		l.procs = append(l.procs, p)
		l.specs = append(l.specs, spec)
		return p, nil
	}}
}

func (l *procList) at(i int) *fakeProc {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.procs[i]
}

func (l *procList) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.procs)
}

// attachCommands builds an attach command for workspace w1 and session s-1.
func attachCommands() *terminalCommandsMock {
	return &terminalCommandsMock{TerminalCommandFunc: func(context.Context, *envproto.TerminalOpenArgs) (TerminalCommand, error) {
		workspace, session := "w1", "s-1"
		return TerminalCommand{Path: "/bin/claude", Args: []string{"attach", session}, Workspace: &workspace, Session: &session}, nil
	}}
}

func attachArgs() *envproto.TerminalOpenArgs {
	workspace := "w1"
	return &envproto.TerminalOpenArgs{Kind: envproto.TermAttach, Workspace: &workspace, Specs: []string{}, Cols: 80, Rows: 24}
}

// termClock is a clock the test moves.
type termClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *termClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *termClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// termHarness is a terminals manager over a recording client, a fake clock
// and fake processes.
type termHarness struct {
	t     *testing.T
	m     *terminals
	rec   *publishRecorder
	procs *procList
	clock *termClock
	ticks chan time.Time
	gate  atomic.Pointer[chan struct{}]
}

// blockData makes every data frame publish wait until the returned func is called.
func (h *termHarness) blockData() (release func()) {
	gate := make(chan struct{})
	h.gate.Store(&gate)
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func newTermHarness(t *testing.T) *termHarness {
	t.Helper()
	h := &termHarness{
		t:     t,
		rec:   &publishRecorder{},
		procs: &procList{},
		clock: &termClock{now: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)},
		ticks: make(chan time.Time),
	}
	client := connectedMock(func(ctx context.Context, msg envproto.Message) error {
		if gate := h.gate.Load(); gate != nil && isDataFrame(msg.Payload) {
			select {
			case <-*gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		h.rec.add(&msg)
		return nil
	})
	o := testOptions()
	o.Now = h.clock.Now
	o.After = func(d time.Duration) <-chan time.Time {
		if d == idleCheck {
			return h.ticks
		}
		return time.After(d)
	}
	h.m = newTerminals(discardLog(), client, attachCommands(), rand.Reader, &o)
	h.m.starter = h.procs.starter()
	h.m.redrawDelay = time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	h.m.begin(ctx, termEnvID)
	t.Cleanup(func() {
		cancel()
		h.m.closeAll()
	})
	return h
}

func isDataFrame(payload []byte) bool {
	f, err := envproto.UnmarshalFrame(payload)
	return err == nil && f.Kind == envproto.FrameData
}

// open opens an attach terminal and returns its sid and process.
// queueEmpty reports whether sid's output queue holds nothing: the publisher
// has taken every chunk, so with data frames blocked it is stuck on the last.
func (h *termHarness) queueEmpty(sid string) bool {
	h.m.mu.Lock()
	term := h.m.byID[sid]
	h.m.mu.Unlock()
	if term == nil {
		return false
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	return len(term.queue) == 0
}

// overflowOutput blocks data frames, then writes total bytes of output so the
// queue overflows. It writes one chunk first and waits for the publisher to
// take it and stick on the gate, so every drop happens after that and the
// publisher reports them all in one dropped frame once released.
func (h *termHarness) overflowOutput(sid string, proc *fakeProc, total int) {
	h.t.Helper()
	chunk := bytes.Repeat([]byte("y"), envproto.MaxChunkBytes)
	proc.writeOutput(h.t, chunk, 1)
	require.Eventually(h.t, func() bool { return h.queueEmpty(sid) }, 5*time.Second, time.Millisecond)
	proc.writeOutput(h.t, chunk, total/len(chunk)-1)
}

func (h *termHarness) open() (string, *fakeProc) {
	h.t.Helper()
	raw, err := h.m.open(h.t.Context(), attachArgs())
	require.NoError(h.t, err)
	var result envproto.TerminalOpenResult
	require.NoError(h.t, json.Unmarshal(raw, &result))
	return result.SID, h.procs.at(h.procs.count() - 1)
}

// send delivers one input frame to the manager as the broker would.
func (h *termHarness) send(sid string, kind envproto.FrameKind, seq uint64, body []byte) {
	h.t.Helper()
	payload, err := envproto.MarshalFrame(envproto.Frame{Kind: kind, Seq: seq, Body: body})
	require.NoError(h.t, err)
	h.sendRaw(sid, payload)
}

func (h *termHarness) sendRaw(sid string, payload []byte) {
	h.m.onStreamIn(&envproto.Message{Topic: envproto.Topic(termEnvID, envproto.LeafStream, sid, "in"), Payload: payload, QoS: 1})
}

// frames are the output frames published for sid so far.
func (h *termHarness) frames(sid string) []envproto.Frame {
	h.t.Helper()
	var out []envproto.Frame
	for _, msg := range h.rec.on(envproto.Topic(termEnvID, envproto.LeafStream, sid, "out")) {
		f, err := envproto.UnmarshalFrame(msg.Payload)
		require.NoError(h.t, err)
		out = append(out, f)
	}
	return out
}

// exit waits for sid's exit frame and returns its body.
func (h *termHarness) exit(sid string) envproto.TerminalExit {
	h.t.Helper()
	var found *envproto.Frame
	require.Eventually(h.t, func() bool {
		for _, f := range h.frames(sid) {
			if f.Kind == envproto.FrameExit {
				found = &f
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond)
	var exit envproto.TerminalExit
	require.NoError(h.t, json.Unmarshal(found.Body, &exit))
	return exit
}

// dataBytes is the total of the data frames' bodies.
func dataBytes(frames []envproto.Frame) int {
	total := 0
	for _, f := range frames {
		if f.Kind == envproto.FrameData {
			total += len(f.Body)
		}
	}
	return total
}

func TestTerminal_output(t *testing.T) {
	t.Parallel()
	t.Run("output is sent in frames of at most 16 KiB with increasing sequence numbers", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		_, err := proc.out.Write(bytes.Repeat([]byte("x"), 40000))
		require.NoError(t, err)
		require.Eventually(t, func() bool { return dataBytes(h.frames(sid)) == 40000 }, 5*time.Second, 5*time.Millisecond)
		frames := h.frames(sid)
		require.GreaterOrEqual(t, len(frames), 3)
		for i, f := range frames {
			assert.Equal(t, envproto.FrameData, f.Kind)
			assert.LessOrEqual(t, len(f.Body), envproto.MaxChunkBytes)
			assert.Equal(t, uint64(i+1), f.Seq)
		}
	})
	t.Run("a full queue drops the oldest output and sends one dropped frame", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		release := h.blockData()
		sid, proc := h.open()
		const total = 3 << 20
		h.overflowOutput(sid, proc, total)
		release()
		require.Eventually(t, func() bool {
			var dropped uint64
			for _, f := range h.frames(sid) {
				if f.Kind == envproto.FrameDropped {
					dropped = readUint64(f.Body)
				}
			}
			return dropped > 0 && dataBytes(h.frames(sid))+int(dropped) == total
		}, 5*time.Second, 5*time.Millisecond)
		dropFrames := 0
		for i, f := range h.frames(sid) {
			assert.Equal(t, uint64(i+1), f.Seq)
			if f.Kind == envproto.FrameDropped {
				dropFrames++
			}
		}
		assert.Equal(t, 1, dropFrames)
		assert.LessOrEqual(t, dataBytes(h.frames(sid)), envproto.MaxQueuedOutput+8*envproto.MaxChunkBytes)
	})
	t.Run("a drop is followed by one redraw", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		release := h.blockData()
		sid, proc := h.open()
		h.overflowOutput(sid, proc, 3<<20)
		release()
		require.Eventually(t, func() bool { return len(proc.sizes()) == 2 }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, [][2]int{{80, 25}, {80, 24}}, proc.sizes())
		time.Sleep(100 * time.Millisecond)
		assert.Len(t, proc.sizes(), 2)
	})
	t.Run("a process that exits ends the terminal after its output with its code", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		_, err := proc.out.Write([]byte("bye"))
		require.NoError(t, err)
		proc.exitWith(3)
		exit := h.exit(sid)
		assert.Equal(t, "exited", exit.Reason)
		require.NotNil(t, exit.Code)
		assert.Equal(t, 3, *exit.Code)
		frames := h.frames(sid)
		require.Len(t, frames, 2)
		assert.Equal(t, envproto.FrameData, frames[0].Kind)
		assert.Equal(t, envproto.FrameExit, frames[1].Kind)
		assert.Equal(t, uint64(2), frames[1].Seq)
	})
}

func readUint64(b []byte) uint64 {
	var n uint64
	for _, v := range b {
		n = n<<8 | uint64(v)
	}
	return n
}

// rawInput is an input frame's bytes without MarshalFrame's limits.
func rawInput(seq uint64, body []byte) []byte {
	b := make([]byte, 10+len(body))
	b[0] = 1
	b[1] = byte(envproto.FrameInput)
	for i := range 8 {
		b[9-i] = byte(seq >> (8 * i))
	}
	copy(b[10:], body)
	return b
}

func TestTerminal_input(t *testing.T) {
	t.Parallel()
	t.Run("input is written to the pty", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameInput, 1, []byte("ls"))
		h.send(sid, envproto.FrameInput, 2, []byte(" -l"))
		require.Eventually(t, func() bool { return proc.input() == "ls -l" }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("an oversized frame closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.sendRaw(sid, rawInput(1, bytes.Repeat([]byte("a"), envproto.MaxInputBytes+1)))
		assert.Equal(t, "input_limit", h.exit(sid).Reason)
		assert.True(t, proc.isClosed())
		assert.Empty(t, proc.input())
	})
	t.Run("a frame that isn't an input kind closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, _ := h.open()
		h.send(sid, envproto.FrameData, 1, []byte("x"))
		assert.Equal(t, "input_limit", h.exit(sid).Reason)
	})
	t.Run("garbage closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, _ := h.open()
		h.sendRaw(sid, []byte("nope"))
		assert.Equal(t, "input_limit", h.exit(sid).Reason)
	})
	t.Run("input over the rate closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		for i := 1; i <= envproto.InputLimitFrames; i++ {
			h.send(sid, envproto.FrameInput, uint64(i), []byte("a"))
		}
		require.Eventually(t, func() bool { return len(proc.input()) == envproto.InputLimitFrames }, 5*time.Second, 5*time.Millisecond)
		h.send(sid, envproto.FrameInput, uint64(envproto.InputLimitFrames+1), []byte("a"))
		assert.Equal(t, "input_limit", h.exit(sid).Reason)
		assert.True(t, proc.isClosed())
	})
	t.Run("input over the byte rate closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, _ := h.open()
		body := bytes.Repeat([]byte("a"), envproto.MaxInputBytes)
		for i := 1; i <= envproto.InputLimitBytes/envproto.MaxInputBytes+1; i++ {
			h.send(sid, envproto.FrameInput, uint64(i), body)
		}
		assert.Equal(t, "input_limit", h.exit(sid).Reason)
	})
	t.Run("input within the rate is accepted as time passes", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		seq := uint64(0)
		for range 3 {
			for range envproto.InputLimitFrames {
				seq++
				h.send(sid, envproto.FrameInput, seq, []byte("a"))
			}
			require.Eventually(t, func() bool { return len(proc.input()) == int(seq) }, 5*time.Second, 5*time.Millisecond)
			h.clock.advance(time.Second)
		}
		assert.False(t, proc.isClosed())
	})
	t.Run("a burst of InputLimitFrames frames while the pty write is blocked is accepted", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		release := proc.blockWrites()
		defer release()
		for i := 1; i <= envproto.InputLimitFrames; i++ {
			h.send(sid, envproto.FrameInput, uint64(i), []byte("a"))
		}
		assert.False(t, h.m.lookup(sid).isClosing(), "input inside the rate must not close the terminal")
		release()
		require.Eventually(t, func() bool { return len(proc.input()) == envproto.InputLimitFrames }, 5*time.Second, 5*time.Millisecond)
		assert.False(t, proc.isClosed())
	})
	t.Run("a burst of maximum-size frames inside the byte rate is accepted while the pty write is blocked", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		release := proc.blockWrites()
		defer release()
		body := bytes.Repeat([]byte("a"), envproto.MaxInputBytes)
		frames := envproto.InputLimitBytes / envproto.MaxInputBytes
		for i := 1; i <= frames; i++ {
			h.send(sid, envproto.FrameInput, uint64(i), body)
		}
		assert.False(t, h.m.lookup(sid).isClosing())
		release()
		require.Eventually(t, func() bool { return len(proc.input()) == frames*len(body) }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("a queued frame keeps its bytes when the broker's buffer is reused", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		release := proc.blockWrites()
		defer release()
		h.send(sid, envproto.FrameInput, 1, []byte("first"))
		payload, err := envproto.MarshalFrame(envproto.Frame{Kind: envproto.FrameInput, Seq: 2, Body: []byte("second")})
		require.NoError(t, err)
		h.sendRaw(sid, payload)
		for i := range payload[10:] {
			payload[10+i] = 'X'
		}
		release()
		require.Eventually(t, func() bool { return len(proc.input()) == len("firstsecond") }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, "firstsecond", proc.input())
	})
	t.Run("a gap in input closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameInput, 1, []byte("a"))
		h.send(sid, envproto.FrameInput, 3, []byte("rm -rf"))
		assert.Equal(t, "input_gap", h.exit(sid).Reason)
		assert.NotContains(t, proc.input(), "rm")
	})
	t.Run("a repeated sequence number closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, _ := h.open()
		h.send(sid, envproto.FrameInput, 1, []byte("a"))
		h.send(sid, envproto.FrameInput, 1, []byte("a"))
		assert.Equal(t, "input_gap", h.exit(sid).Reason)
	})
	t.Run("resize resizes the pty", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameResize, 1, envproto.ResizeBody(100, 40))
		require.Eventually(t, func() bool { return len(proc.sizes()) == 1 }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, [][2]int{{100, 40}}, proc.sizes())
	})
	t.Run("a resize to size zero closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, _ := h.open()
		h.send(sid, envproto.FrameResize, 1, []byte{0, 0, 0, 24})
		assert.Equal(t, "input_limit", h.exit(sid).Reason)
	})
	t.Run("redraw resizes by one row and back", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameResize, 1, envproto.ResizeBody(100, 40))
		h.send(sid, envproto.FrameRedraw, 2, nil)
		require.Eventually(t, func() bool { return len(proc.sizes()) == 3 }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, [][2]int{{100, 40}, {100, 41}, {100, 40}}, proc.sizes())
	})
	t.Run("a second redraw within a second is ignored", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameRedraw, 1, nil)
		require.Eventually(t, func() bool { return len(proc.sizes()) == 2 }, 5*time.Second, 5*time.Millisecond)
		h.send(sid, envproto.FrameRedraw, 2, nil)
		h.send(sid, envproto.FrameInput, 3, []byte("x"))
		require.Eventually(t, func() bool { return proc.input() == "x" }, 5*time.Second, 5*time.Millisecond)
		assert.Len(t, proc.sizes(), 2)
		h.clock.advance(envproto.RedrawEvery)
		h.send(sid, envproto.FrameRedraw, 4, nil)
		require.Eventually(t, func() bool { return len(proc.sizes()) == 4 }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("no frame for 60 s closes the terminal", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.clock.advance(envproto.IdleClose - time.Second)
		h.send(sid, envproto.FramePing, 1, nil)
		h.clock.advance(envproto.IdleClose - time.Second)
		h.ticks <- h.clock.Now()
		h.ticks <- h.clock.Now()
		assert.False(t, proc.isClosed())
		h.clock.advance(2 * time.Second)
		h.ticks <- h.clock.Now()
		assert.Equal(t, "idle", h.exit(sid).Reason)
		assert.True(t, proc.isClosed())
	})
	t.Run("close sends an exit frame with the reason", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameClose, 1, nil)
		exit := h.exit(sid)
		assert.Equal(t, "closed", exit.Reason)
		assert.True(t, proc.isClosed())
	})
	t.Run("input after the terminal ended is dropped", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sid, proc := h.open()
		h.send(sid, envproto.FrameClose, 1, nil)
		h.exit(sid)
		require.Eventually(t, func() bool { return h.m.lookup(sid) == nil }, 5*time.Second, 5*time.Millisecond)
		h.send(sid, envproto.FrameInput, 2, []byte("late"))
		assert.Empty(t, proc.input())
	})
}

func TestTerminals_closeAll(t *testing.T) {
	t.Parallel()
	t.Run("every terminal gets an exit frame with reason disconnected", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		sidA, procA := h.open()
		sidB, procB := h.open()
		h.m.closeAll()
		assert.True(t, procA.isClosed())
		assert.True(t, procB.isClosed())
		assert.Equal(t, "disconnected", h.exit(sidA).Reason)
		assert.Equal(t, "disconnected", h.exit(sidB).Reason)
		assert.Empty(t, h.m.snapshot())
	})
}

// isClosing reports whether the terminal has started to end.
func (t *terminal) isClosing() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func TestTerminal_finish(t *testing.T) {
	t.Parallel()
	// stopped is a terminal that has not been run: its goroutines are done, so
	// finish can be called directly with the publisher's last state.
	stopped := func(h *termHarness) (*terminal, string) {
		const sid = "0123456789abcdef"
		require.True(h.t, h.m.reserve())
		tm := newTerminal(h.m, sid, newFakeProc(), 80, 24)
		close(tm.readDone)
		close(tm.inDone)
		return tm, sid
	}
	t.Run("output left in the queue is published before the exit frame", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		tm, sid := stopped(h)
		tm.push([]byte("last words"))
		tm.close(reasonExited)
		var seq uint64
		tm.finish(&seq)
		frames := h.frames(sid)
		require.Len(t, frames, 2)
		assert.Equal(t, envproto.FrameData, frames[0].Kind)
		assert.Equal(t, "last words", string(frames[0].Body))
		assert.Equal(t, envproto.FrameExit, frames[1].Kind)
	})
	t.Run("a stalled broker holds finish for a short deadline, not per frame", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		release := h.blockData()
		defer release()
		h.m.publishWait = time.Minute
		h.m.finishWait = 50 * time.Millisecond
		tm, sid := stopped(h)
		for range 4 {
			tm.push([]byte("queued"))
		}
		tm.close(reasonDisconnected)
		done := make(chan struct{})
		go func() {
			var seq uint64
			tm.finish(&seq)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.Fail(t, "finish waited on the stalled broker")
		}
		frames := h.frames(sid)
		require.NotEmpty(t, frames)
		assert.Equal(t, envproto.FrameExit, frames[len(frames)-1].Kind)
	})
}

func TestTerminals_open(t *testing.T) {
	t.Parallel()
	t.Run("a failed TerminalCommand gives its slot back", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		good := h.m.commands
		h.m.commands = &terminalCommandsMock{TerminalCommandFunc: func(context.Context, *envproto.TerminalOpenArgs) (TerminalCommand, error) {
			return TerminalCommand{}, &envproto.ReplyError{Code: "workspace_not_found", Message: "no such workspace"}
		}}
		for range envproto.MaxTerminals + 1 {
			_, err := h.m.open(t.Context(), attachArgs())
			var reply *envproto.ReplyError
			require.ErrorAs(t, err, &reply)
			require.Equal(t, "workspace_not_found", reply.Code)
		}
		h.m.commands = good
		h.open()
	})
	t.Run("a failed Start gives its slot back", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		good := h.m.starter
		h.m.starter = &ptyStarterMock{StartFunc: func(ptyproc.Spec) (ptyProcess, error) { return nil, io.ErrClosedPipe }}
		for range envproto.MaxTerminals + 1 {
			_, err := h.m.open(t.Context(), attachArgs())
			require.ErrorIs(t, err, io.ErrClosedPipe)
		}
		h.m.starter = good
		h.open()
	})
	t.Run("a pty the platform doesn't have is platform_unsupported", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		h.m.starter = &ptyStarterMock{StartFunc: func(ptyproc.Spec) (ptyProcess, error) { return nil, ptyproc.ErrUnsupported }}
		_, err := h.m.open(t.Context(), attachArgs())
		var reply *envproto.ReplyError
		require.ErrorAs(t, err, &reply)
		assert.Equal(t, "platform_unsupported", reply.Code)
	})
	t.Run("an unsupported platform refuses before building the command", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		require.NotNil(t, h.m.supported)
		good := h.m.commands
		h.m.supported = func() bool { return false }
		h.m.commands = &terminalCommandsMock{TerminalCommandFunc: func(context.Context, *envproto.TerminalOpenArgs) (TerminalCommand, error) {
			t.Error("the command was built on a platform without pseudo-terminals")
			return TerminalCommand{}, nil
		}}
		for range envproto.MaxTerminals + 1 {
			_, err := h.m.open(t.Context(), attachArgs())
			var reply *envproto.ReplyError
			require.ErrorAs(t, err, &reply)
			require.Equal(t, "platform_unsupported", reply.Code)
		}
		h.m.supported = func() bool { return true }
		h.m.commands = good
		h.open()
	})
	t.Run("a sid already in use is not handed out again", func(t *testing.T) {
		t.Parallel()
		h := newTermHarness(t)
		same := bytes.Repeat([]byte{0xaa}, sidBytes)
		other := bytes.Repeat([]byte{0xbb}, sidBytes)
		h.m.random = bytes.NewReader(bytes.Join([][]byte{same, same, other}, nil))
		first, _ := h.open()
		second, _ := h.open()
		assert.Equal(t, "aaaaaaaaaaaaaaaa", first)
		assert.Equal(t, "bbbbbbbbbbbbbbbb", second)
	})
}

// terminalOps is an operationRunner that also builds terminal commands.
type terminalOps struct {
	*operationRunnerMock
	*terminalCommandsMock
}

func newTerminalOps(commands *terminalCommandsMock) *terminalOps {
	return &terminalOps{operationRunnerMock: listRunner(), terminalCommandsMock: commands}
}

// termFixture is a running agent whose terminals start fake processes.
type termFixture struct {
	fx    *agentFixture
	agent *Agent
	procs *procList
	ops   *terminalOps
	reqs  atomic.Int64
	run   *runningAgent
}

func newTermFixture(t *testing.T, commands *terminalCommandsMock) *termFixture {
	t.Helper()
	fx := startAgentFixture(t)
	ops := newTerminalOps(commands)
	agent := newOpsAgent(fx, ops, opsOptions())
	require.NotNil(t, agent.terms)
	procs := &procList{}
	agent.terms.starter = procs.starter()
	run := runAgent(t, agent)
	awaitOnline(t, fx)
	return &termFixture{fx: fx, agent: agent, procs: procs, ops: ops, run: run}
}

// ask sends op with args as a fresh request and returns the reply.
func (f *termFixture) ask(t *testing.T, op string, args any) envproto.Reply {
	t.Helper()
	n := f.reqs.Add(1)
	return askAgent(t, f.fx, opRequest(t, fmt.Sprintf("%016x", n), op, args), fmt.Sprintf("t%d", n))
}

// open opens an attach terminal and returns its sid.
func (f *termFixture) open(t *testing.T) string {
	t.Helper()
	reply := f.ask(t, envproto.OpTerminalOpen, attachArgs())
	require.True(t, reply.OK, "%+v", reply.Error)
	var result envproto.TerminalOpenResult
	require.NoError(t, json.Unmarshal(reply.Result, &result))
	return result.SID
}

// sendIn publishes a frame on sid's input topic as the controller would.
func (f *termFixture) sendIn(t *testing.T, sid string, kind envproto.FrameKind, seq uint64, body []byte) {
	t.Helper()
	payload, err := envproto.MarshalFrame(envproto.Frame{Kind: kind, Seq: seq, Body: body})
	require.NoError(t, err)
	require.NoError(t, f.fx.broker.Publish(t.Context(), envproto.Message{
		Topic: envproto.Topic(f.fx.id.ID, envproto.LeafStream, sid, "in"), Payload: payload, QoS: 1,
	}))
}

func TestAgent_terminalOpen(t *testing.T) {
	t.Parallel()
	t.Run("opens an attach terminal and replies its sid", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		reply := f.ask(t, envproto.OpTerminalOpen, attachArgs())
		require.True(t, reply.OK, "%+v", reply.Error)
		var result envproto.TerminalOpenResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.True(t, envproto.ValidSID(result.SID))
		require.NotNil(t, result.Workspace)
		assert.Equal(t, "w1", *result.Workspace)
		require.NotNil(t, result.Session)
		assert.Equal(t, "s-1", *result.Session)
		require.Equal(t, 1, f.procs.count())
		spec := f.procs.specs[0]
		assert.Equal(t, "/bin/claude", spec.Path)
		assert.Equal(t, []string{"attach", "s-1"}, spec.Args)
		assert.Equal(t, 80, spec.Cols)
		assert.Equal(t, 24, spec.Rows)
	})
	t.Run("the sid is 16 hex characters", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		first, second := f.open(t), f.open(t)
		assert.Regexp(t, `^[0-9a-f]{16}$`, first)
		assert.Regexp(t, `^[0-9a-f]{16}$`, second)
		assert.NotEqual(t, first, second)
	})
	t.Run("a workspace outside workspaces/ is refused", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		args := attachArgs()
		outside := "/etc/passwd"
		args.Workspace = &outside
		reply := f.ask(t, envproto.OpTerminalOpen, args)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "outside_workspace", reply.Error.Code)
		assert.Empty(t, f.ops.TerminalCommandCalls())
		assert.Zero(t, f.procs.count())
	})
	t.Run("a workspace with no Claude session is refused", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, &terminalCommandsMock{TerminalCommandFunc: func(context.Context, *envproto.TerminalOpenArgs) (TerminalCommand, error) {
			return TerminalCommand{}, &envproto.ReplyError{Code: "workspace_not_found", Message: "w1 has no Claude session"}
		}})
		reply := f.ask(t, envproto.OpTerminalOpen, attachArgs())
		require.NotNil(t, reply.Error)
		assert.Equal(t, "workspace_not_found", reply.Error.Code)
		assert.Zero(t, f.procs.count())
	})
	t.Run("a ninth terminal is refused with terminal_limit", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		sids := make([]string, 0, envproto.MaxTerminals)
		for range envproto.MaxTerminals {
			sids = append(sids, f.open(t))
		}
		reply := f.ask(t, envproto.OpTerminalOpen, attachArgs())
		require.NotNil(t, reply.Error)
		assert.Equal(t, "terminal_limit", reply.Error.Code)
		assert.Equal(t, envproto.MaxTerminals, f.procs.count())
		closed := f.ask(t, envproto.OpTerminalClose, envproto.TerminalCloseArgs{SID: sids[0]})
		require.True(t, closed.OK)
		again := f.ask(t, envproto.OpTerminalOpen, attachArgs())
		assert.True(t, again.OK, "a closed terminal frees its slot")
	})
	// The agent subscribes to req and never to up, so this documents that an
	// open published on up goes nowhere; no agent code enforces it.
	t.Run("an open on up is never seen by the agent", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		res := envproto.Topic(f.fx.id.ID, envproto.LeafRes, "up1")
		replies := captureMessages(t, f.fx.broker, res)
		require.NoError(t, f.fx.broker.Publish(t.Context(), envproto.Message{
			Topic:           envproto.Topic(f.fx.id.ID, envproto.LeafUp),
			Payload:         opRequest(t, "00000000000000aa", envproto.OpTerminalOpen, attachArgs()),
			QoS:             1,
			ResponseTopic:   res,
			CorrelationData: []byte("up1"),
		}))
		time.Sleep(200 * time.Millisecond)
		assert.Zero(t, f.procs.count())
		assert.Empty(t, f.ops.TerminalCommandCalls())
		assert.Empty(t, replies.all())
	})
	t.Run("an agent whose operations can't build terminals answers usage", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		agent := newOpsAgent(fx, NoOperations{}, opsOptions())
		assert.Nil(t, agent.terms)
		runAgent(t, agent)
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "00000000000000bb", envproto.OpTerminalOpen, attachArgs()), "n1")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
	})
	t.Run("input for an unknown sid is dropped", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		sid := f.open(t)
		f.sendIn(t, "0123456789abcdef", envproto.FrameInput, 1, []byte("stray"))
		f.sendIn(t, sid, envproto.FrameInput, 1, []byte("mine"))
		require.Eventually(t, func() bool { return f.procs.at(0).input() == "mine" }, 5*time.Second, 5*time.Millisecond)
		assert.False(t, f.procs.at(0).isClosed())
	})
	t.Run("a dropped connection closes every terminal", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		f.open(t)
		f.open(t)
		require.Equal(t, 2, f.procs.count())
		require.Equal(t, 1, f.fx.broker.Disconnect(f.fx.id.Cert.SerialNumber.Text(16)))
		require.Eventually(t, func() bool { return f.procs.at(0).isClosed() && f.procs.at(1).isClosed() }, 5*time.Second, 5*time.Millisecond)
		require.Eventually(t, func() bool { return len(f.agent.terms.snapshot()) == 0 }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("stopping the agent closes every terminal with reason disconnected", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		sid := f.open(t)
		out := captureMessages(t, f.fx.broker, envproto.Topic(f.fx.id.ID, envproto.LeafStream, sid, "out"))
		f.run.cancel()
		require.Eventually(t, func() bool { return f.procs.at(0).isClosed() }, 5*time.Second, 5*time.Millisecond)
		require.Eventually(t, func() bool { return len(out.all()) == 1 }, 5*time.Second, 5*time.Millisecond)
		frame, err := envproto.UnmarshalFrame(out.all()[0].Payload)
		require.NoError(t, err)
		require.Equal(t, envproto.FrameExit, frame.Kind)
		var exit envproto.TerminalExit
		require.NoError(t, json.Unmarshal(frame.Body, &exit))
		assert.Equal(t, "disconnected", exit.Reason)
	})
	t.Run("terminal.close of an unknown sid answers closed false", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		reply := f.ask(t, envproto.OpTerminalClose, envproto.TerminalCloseArgs{SID: "0123456789abcdef"})
		require.True(t, reply.OK)
		var result envproto.TerminalCloseResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.False(t, result.Closed)
	})
	t.Run("terminal.close ends a terminal and answers closed true", func(t *testing.T) {
		t.Parallel()
		f := newTermFixture(t, attachCommands())
		sid := f.open(t)
		out := captureMessages(t, f.fx.broker, envproto.Topic(f.fx.id.ID, envproto.LeafStream, sid, "out"))
		reply := f.ask(t, envproto.OpTerminalClose, envproto.TerminalCloseArgs{SID: sid})
		require.True(t, reply.OK)
		var result envproto.TerminalCloseResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.True(t, result.Closed)
		assert.True(t, f.procs.at(0).isClosed())
		require.Len(t, out.all(), 1)
		frame, err := envproto.UnmarshalFrame(out.all()[0].Payload)
		require.NoError(t, err)
		assert.Equal(t, envproto.FrameExit, frame.Kind)
	})
}
