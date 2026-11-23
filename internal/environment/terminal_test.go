package environment

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

const (
	testSID       = "0123456789abcdef"
	termWait      = 5 * time.Second
	termQuietWait = 100 * time.Millisecond
)

// termFixture is an online environment whose fake agent answers terminal.open
// and terminal.close, and records the ops and the frames the controller sends.
type termFixture struct {
	callFixture
	mu     sync.Mutex
	ops    []string
	frames []envproto.Frame
	// openReply replaces the reply to terminal.open when set.
	openReply *envproto.Reply
	clock     atomic.Int64
	tick      chan time.Time
	tickEvery atomic.Int64
	// afterOpen runs in the agent's handler right after it published the open reply.
	afterOpen func()
	other     *envtest.Client
	otherID   string
}

func newTermFixture(t *testing.T) *termFixture {
	t.Helper()
	f := &termFixture{callFixture: newCallFixture(t), tick: make(chan time.Time, 8)}
	f.hear(t, envproto.StatusOnline, envproto.Protocol)
	f.clock.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	f.c.now = func() time.Time { return time.Unix(0, f.clock.Load()).UTC() }
	f.c.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		f.tickEvery.Store(int64(d))
		return f.tick, func() {}
	}
	require.NoError(t, f.agent.Subscribe(t.Context(), envproto.Topic(f.id, envproto.LeafReq), f.answer(t)))
	_, err := f.b.Subscribe(t.Context(), envproto.Topic(f.id, envproto.LeafStream, "+", "in"), f.record(t))
	require.NoError(t, err)
	return f
}

// addOtherEnvironment enrolls and connects a second environment, win-rig.
func (f *termFixture) addOtherEnvironment(t *testing.T) {
	t.Helper()
	rec, cert := enrollAgent(t, f.store, "win-rig")
	f.other = connectAgent(t, f.store, f.b, rec.ID, &cert)
	f.otherID = rec.ID
}

func (f *termFixture) answer(t *testing.T) func(envproto.Message) {
	t.Helper()
	return func(msg envproto.Message) {
		var req envproto.Request
		require.NoError(t, json.Unmarshal(msg.Payload, &req))
		f.mu.Lock()
		f.ops = append(f.ops, req.Op)
		override := f.openReply
		f.mu.Unlock()
		reply := envproto.Reply{OK: true, Result: mustJSON(t, envproto.TerminalCloseResult{SID: testSID, Closed: true})}
		if req.Op == envproto.OpTerminalOpen {
			reply = envproto.Reply{OK: true, Result: mustJSON(t, envproto.TerminalOpenResult{SID: testSID})}
			if override != nil {
				reply = *override
			}
		}
		require.NoError(t, f.agent.Publish(context.WithoutCancel(t.Context()), envproto.Message{
			Topic:           msg.ResponseTopic,
			Payload:         mustJSON(t, reply),
			QoS:             1,
			CorrelationData: msg.CorrelationData,
		}))
		if req.Op == envproto.OpTerminalOpen && f.afterOpen != nil {
			f.afterOpen()
		}
	}
}

func (f *termFixture) record(t *testing.T) func(envproto.Message) {
	t.Helper()
	return func(msg envproto.Message) {
		frame, err := envproto.UnmarshalFrame(msg.Payload)
		require.NoError(t, err)
		assert.Equal(t, byte(1), msg.QoS, "input frames are QoS 1")
		f.mu.Lock()
		defer f.mu.Unlock()
		frame.Body = bytes.Clone(frame.Body)
		f.frames = append(f.frames, frame)
	}
}

func (f *termFixture) sent() []envproto.Frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]envproto.Frame(nil), f.frames...)
}

func (f *termFixture) sentOps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
}

func (f *termFixture) sentKinds(kind envproto.FrameKind) []envproto.Frame {
	var out []envproto.Frame
	for _, fr := range f.sent() {
		if fr.Kind == kind {
			out = append(out, fr)
		}
	}
	return out
}

func attachArgs() envproto.TerminalOpenArgs {
	ws := "ws-1"
	return envproto.TerminalOpenArgs{Kind: envproto.TermAttach, Workspace: &ws, Specs: []string{}, Cols: 80, Rows: 24}
}

func (f *termFixture) open(t *testing.T) *Terminal {
	t.Helper()
	term, res, err := f.c.OpenTerminal(t.Context(), "linux-server", attachArgs())
	require.NoError(t, err)
	require.Equal(t, testSID, res.SID)
	t.Cleanup(func() { _ = term.Close(context.WithoutCancel(t.Context())) })
	return term
}

// outFrom publishes a frame on the out topic of sid, as the agent of client would.
func (f *termFixture) outFrame(t *testing.T, sid string, kind envproto.FrameKind, seq uint64, body []byte) {
	t.Helper()
	f.outFor(t, f.id, sid, kind, seq, body)
}

func (f *termFixture) outFor(t *testing.T, id, sid string, kind envproto.FrameKind, seq uint64, body []byte) {
	t.Helper()
	payload, err := envproto.MarshalFrame(envproto.Frame{Kind: kind, Seq: seq, Body: body})
	require.NoError(t, err)
	agent := f.agent
	if id != f.id {
		agent = f.other
	}
	require.NoError(t, agent.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(id, envproto.LeafStream, sid, "out"), Payload: payload}))
}

func (f *termFixture) data(t *testing.T, seq uint64, s string) {
	t.Helper()
	f.outFrame(t, testSID, envproto.FrameData, seq, []byte(s))
}

func next(t *testing.T, term *Terminal) []byte {
	t.Helper()
	select {
	case b, ok := <-term.Output():
		require.True(t, ok, "Output closed")
		return b
	case <-time.After(termWait):
		require.FailNow(t, "no output")
	}
	return nil
}

func closedWithin(t *testing.T, term *Terminal) {
	t.Helper()
	select {
	case b, ok := <-term.Output():
		require.False(t, ok, "unexpected output %q", b)
	case <-time.After(termWait):
		require.FailNow(t, "Output did not close")
	}
}

func awaitFrames(t *testing.T, f *termFixture, kind envproto.FrameKind, n int) []envproto.Frame {
	t.Helper()
	require.Eventually(t, func() bool { return len(f.sentKinds(kind)) >= n }, termWait, time.Millisecond)
	return f.sentKinds(kind)
}

func TestController_OpenTerminal(t *testing.T) {
	t.Run("opens and streams output in order", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "one")
		f.data(t, 2, "two")
		f.data(t, 3, "three")
		assert.Equal(t, "one", string(next(t, term)))
		assert.Equal(t, "two", string(next(t, term)))
		assert.Equal(t, "three", string(next(t, term)))
		assert.Equal(t, []string{envproto.OpTerminalOpen}, f.sentOps())
	})
	t.Run("output published right after the open reply is not lost", func(t *testing.T) {
		f := newTermFixture(t)
		f.afterOpen = func() { f.data(t, 1, "$ ") }
		term := f.open(t)
		assert.Equal(t, "$ ", string(next(t, term)))
	})
	t.Run("an agent on protocol 1 is outdated", func(t *testing.T) {
		f := newTermFixture(t)
		f.hear(t, envproto.StatusOnline, 1)
		_, _, err := f.c.OpenTerminal(t.Context(), "linux-server", attachArgs())
		var outdated *OutdatedError
		require.ErrorAs(t, err, &outdated)
		assert.Equal(t, 2, outdated.Need)
		assert.Empty(t, f.sentOps())
	})
	t.Run("an offline environment is environment_offline", func(t *testing.T) {
		f := newTermFixture(t)
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		_, _, err := f.c.OpenTerminal(t.Context(), "linux-server", attachArgs())
		var offline *OfflineError
		require.ErrorIs(t, err, ErrOffline)
		require.ErrorAs(t, err, &offline)
		assert.Empty(t, f.sentOps())
	})
	t.Run("an unknown environment is ErrUnknown", func(t *testing.T) {
		f := newTermFixture(t)
		_, _, err := f.c.OpenTerminal(t.Context(), "nope", attachArgs())
		assert.ErrorIs(t, err, ErrUnknown)
	})
	t.Run("arguments that don't validate are refused before anything is sent", func(t *testing.T) {
		f := newTermFixture(t)
		args := attachArgs()
		args.Cols = 0
		_, _, err := f.c.OpenTerminal(t.Context(), "linux-server", args)
		var argsErr *envproto.ArgsError
		require.ErrorAs(t, err, &argsErr)
		assert.Empty(t, f.sentOps())
	})
	t.Run("the agent's error reply is returned", func(t *testing.T) {
		f := newTermFixture(t)
		f.openReply = &envproto.Reply{Error: &envproto.ReplyError{Code: "terminal_limit", Message: "too many"}}
		_, _, err := f.c.OpenTerminal(t.Context(), "linux-server", attachArgs())
		var replyErr *envproto.ReplyError
		require.ErrorAs(t, err, &replyErr)
		assert.Equal(t, "terminal_limit", replyErr.Code)
		assert.Zero(t, f.c.terminalCount())
	})
	t.Run("a reply with no valid sid opens nothing", func(t *testing.T) {
		f := newTermFixture(t)
		f.openReply = &envproto.Reply{OK: true, Result: json.RawMessage(`{"sid":"../x"}`)}
		_, _, err := f.c.OpenTerminal(t.Context(), "linux-server", attachArgs())
		require.Error(t, err)
		assert.Zero(t, f.c.terminalCount())
	})
	t.Run("output for an unknown sid is dropped", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.outFrame(t, "fedcba9876543210", envproto.FrameData, 1, []byte("stray"))
		f.data(t, 1, "mine")
		assert.Equal(t, "mine", string(next(t, term)))
	})
	t.Run("output for a sid of another environment is dropped", func(t *testing.T) {
		f := newTermFixture(t)
		f.addOtherEnvironment(t)
		term := f.open(t)
		f.outFor(t, f.otherID, testSID, envproto.FrameData, 1, []byte("foreign"))
		f.data(t, 1, "mine")
		assert.Equal(t, "mine", string(next(t, term)))
		f.data(t, 2, "again")
		assert.Equal(t, "again", string(next(t, term)))
	})
	t.Run("a gap writes the lost marker and asks one redraw", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "a")
		f.data(t, 3, "c")
		f.data(t, 5, "e")
		assert.Equal(t, "a", string(next(t, term)))
		assert.Equal(t, envproto.LostMarker(), next(t, term))
		assert.Equal(t, "c", string(next(t, term)))
		assert.Equal(t, envproto.LostMarker(), next(t, term))
		assert.Equal(t, "e", string(next(t, term)))
		redraws := awaitFrames(t, f, envproto.FrameRedraw, 1)
		require.NoError(t, term.Close(t.Context()))
		<-term.stopped
		assert.Len(t, f.sentKinds(envproto.FrameRedraw), 1, "the second gap is within RedrawEvery")
		assert.Equal(t, uint64(1), redraws[0].Seq)
	})
	t.Run("a gap after RedrawEvery asks another redraw", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 2, "b")
		awaitFrames(t, f, envproto.FrameRedraw, 1)
		f.clock.Add(int64(envproto.RedrawEvery))
		f.data(t, 4, "d")
		awaitFrames(t, f, envproto.FrameRedraw, 2)
		redraws := f.sentKinds(envproto.FrameRedraw)
		assert.Less(t, redraws[0].Seq, redraws[1].Seq)
		_ = term
	})
	t.Run("a duplicate frame is dropped", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "a")
		f.data(t, 1, "dup")
		f.data(t, 2, "b")
		f.data(t, 1, "old")
		f.data(t, 3, "c")
		assert.Equal(t, "a", string(next(t, term)))
		assert.Equal(t, "b", string(next(t, term)))
		assert.Equal(t, "c", string(next(t, term)))
		assert.Empty(t, f.sentKinds(envproto.FrameRedraw))
	})
	t.Run("a dropped frame writes the dropped marker", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		body := make([]byte, 8)
		binary.BigEndian.PutUint64(body, 4321)
		f.outFrame(t, testSID, envproto.FrameDropped, 1, body)
		f.data(t, 2, "after")
		assert.Equal(t, envproto.DroppedMarker(4321), next(t, term))
		assert.Equal(t, "after", string(next(t, term)))
	})
	t.Run("a consumer that doesn't read loses the oldest output past 1 MiB and sees the marker", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		chunk := bytes.Repeat([]byte("x"), envproto.MaxChunkBytes)
		const frames = 100
		for i := 1; i <= frames; i++ {
			c := bytes.Clone(chunk)
			c[0] = byte(i)
			f.outFrame(t, testSID, envproto.FrameData, uint64(i), c)
		}
		f.outFrame(t, testSID, envproto.FrameExit, frames+1, mustJSON(t, envproto.TerminalExit{Reason: "exited"}))
		var dataBytes int
		var markedDropped uint64
		var lastFirst byte
		markers := 0
		for item := range term.Output() {
			if bytes.HasPrefix(item, []byte("\r\n\x1b[7m[ws: ")) {
				var n uint64
				_, err := fmt.Sscanf(string(item), "\r\n\x1b[7m[ws: %d bytes of output dropped]", &n)
				require.NoError(t, err)
				markedDropped += n
				markers++
				continue
			}
			dataBytes += len(item)
			lastFirst = item[0]
		}
		assert.Positive(t, markers, "the marker is shown")
		assert.Equal(t, frames*envproto.MaxChunkBytes, dataBytes+int(markedDropped), "every byte is delivered or counted as dropped")
		assert.LessOrEqual(t, dataBytes, envproto.MaxQueuedOutput+2*envproto.MaxChunkBytes, "no more than the cap is kept")
		assert.Equal(t, byte(frames), lastFirst, "the newest output is kept")
	})
	t.Run("the handler never blocks the broker", func(t *testing.T) {
		f := newTermFixture(t)
		f.open(t)
		payloads := make([][]byte, 200)
		for i := range payloads {
			payload, err := envproto.MarshalFrame(envproto.Frame{Kind: envproto.FrameData, Seq: uint64(i + 1), Body: []byte("0123456789")})
			require.NoError(t, err)
			payloads[i] = payload
		}
		done := make(chan error, 1)
		go func() {
			for _, payload := range payloads {
				msg := envproto.Message{Topic: envproto.Topic(f.id, envproto.LeafStream, testSID, "out"), Payload: payload}
				if err := f.agent.Publish(context.WithoutCancel(t.Context()), msg); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second):
			require.FailNow(t, "publishing 200 frames blocked behind an unread Output")
		}
	})
	t.Run("an exit frame delivers the queued output, then closes Output", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		code := 3
		f.data(t, 1, "bye")
		f.outFrame(t, testSID, envproto.FrameExit, 2, mustJSON(t, envproto.TerminalExit{Code: &code, Reason: "exited"}))
		assert.Equal(t, "bye", string(next(t, term)))
		closedWithin(t, term)
		exit := term.Exit()
		require.NotNil(t, exit.Code)
		assert.Equal(t, 3, *exit.Code)
		assert.Equal(t, "exited", exit.Reason)
		assert.Zero(t, f.c.terminalCount(), "an ended terminal is unregistered")
		f.data(t, 3, "late")
		assert.ErrorIs(t, term.Input(t.Context(), []byte("x")), ErrTerminalEnded)
	})
	t.Run("going offline ends the terminal", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "last")
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		assert.Equal(t, "last", string(next(t, term)))
		closedWithin(t, term)
		assert.Equal(t, "disconnected", term.Exit().Reason)
		assert.Zero(t, f.c.terminalCount())
		assert.ErrorIs(t, term.Resize(t.Context(), 100, 30), ErrTerminalEnded)
	})
	t.Run("another environment going offline leaves the terminal open", func(t *testing.T) {
		f := newTermFixture(t)
		f.addOtherEnvironment(t)
		term := f.open(t)
		payload, err := json.Marshal(envproto.Hello{Status: envproto.StatusOffline, At: f.store.now().UTC()})
		require.NoError(t, err)
		f.c.onStatus(t.Context(), &envproto.Message{Topic: envproto.Topic(f.otherID, envproto.LeafStatus), Payload: payload})
		f.data(t, 1, "still here")
		assert.Equal(t, "still here", string(next(t, term)))
	})
	t.Run("closing the controller ends the terminals", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		require.NoError(t, f.c.Close())
		closedWithin(t, term)
		assert.Equal(t, "disconnected", term.Exit().Reason)
	})
	t.Run("input is split into 4 KiB frames with sequence numbers", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		p := bytes.Repeat([]byte("k"), 2*envproto.MaxInputBytes+1808)
		require.NoError(t, term.Input(t.Context(), p))
		require.NoError(t, term.Input(t.Context(), nil))
		frames := f.sentKinds(envproto.FrameInput)
		require.Len(t, frames, 3)
		assert.Equal(t, []uint64{1, 2, 3}, []uint64{frames[0].Seq, frames[1].Seq, frames[2].Seq})
		assert.Len(t, frames[0].Body, envproto.MaxInputBytes)
		assert.Len(t, frames[1].Body, envproto.MaxInputBytes)
		assert.Len(t, frames[2].Body, 1808)
		var joined []byte
		for _, fr := range frames {
			joined = append(joined, fr.Body...)
		}
		assert.Equal(t, p, joined)
	})
	t.Run("concurrent input keeps one gap-free sequence in publish order", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		var wg sync.WaitGroup
		errs := make(chan error, 40)
		for range 20 {
			wg.Go(func() {
				errs <- term.Input(t.Context(), bytes.Repeat([]byte("a"), 3*envproto.MaxInputBytes))
				errs <- term.Resize(t.Context(), 100, 40)
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		frames := f.sent()
		require.Len(t, frames, 20*4)
		for i, fr := range frames {
			assert.Equal(t, uint64(i+1), fr.Seq, "frame %d", i)
		}
	})
	t.Run("a resize is a frame and a size out of range is refused without using a sequence number", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		require.Error(t, term.Resize(t.Context(), 0, 24))
		require.Error(t, term.Resize(t.Context(), 80, envproto.MaxTermSize+1))
		require.NoError(t, term.Resize(t.Context(), 120, 40))
		frames := f.sentKinds(envproto.FrameResize)
		require.Len(t, frames, 1)
		assert.Equal(t, uint64(1), frames[0].Seq)
		cols, rows, err := envproto.ParseResize(frames[0].Body)
		require.NoError(t, err)
		assert.Equal(t, [2]int{120, 40}, [2]int{cols, rows})
	})
	t.Run("a failed publish ends the terminal rather than leave a gap", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.Error(t, term.Input(ctx, []byte("rm -rf")))
		require.ErrorIs(t, term.Input(t.Context(), []byte("ls")), ErrTerminalEnded)
		assert.Empty(t, f.sentKinds(envproto.FrameInput))
	})
	t.Run("close sends a close frame and terminal.close", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		require.NoError(t, term.Close(t.Context()))
		require.NoError(t, term.Close(t.Context()), "safe to call twice")
		assert.Len(t, f.sentKinds(envproto.FrameClose), 1)
		assert.Equal(t, []string{envproto.OpTerminalOpen, envproto.OpTerminalClose}, f.sentOps())
		closedWithin(t, term)
		assert.Equal(t, "closed", term.Exit().Reason)
		assert.Zero(t, f.c.terminalCount())
	})
	t.Run("closing an ended terminal sends nothing", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		require.NoError(t, term.Close(t.Context()))
		assert.Empty(t, f.sentKinds(envproto.FrameClose))
		assert.Equal(t, []string{envproto.OpTerminalOpen}, f.sentOps())
	})
	t.Run("ping is sent every interval", func(t *testing.T) {
		f := newTermFixture(t)
		f.open(t)
		assert.Equal(t, int64(envproto.PingInterval), f.tickEvery.Load())
		f.tick <- time.Time{}
		awaitFrames(t, f, envproto.FramePing, 1)
		f.tick <- time.Time{}
		pings := awaitFrames(t, f, envproto.FramePing, 2)
		assert.Equal(t, []uint64{1, 2}, []uint64{pings[0].Seq, pings[1].Seq})
	})
	t.Run("no ping is sent after the terminal ends", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		require.NoError(t, term.Close(t.Context()))
		<-term.stopped
		f.tick <- time.Time{}
		time.Sleep(termQuietWait)
		assert.Empty(t, f.sentKinds(envproto.FramePing))
	})
}
