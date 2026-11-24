package environment

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// unstarted returns a registered-free terminal whose goroutines are not running.
func (f *termFixture) unstarted() *Terminal {
	return f.c.newTerminal(f.id, "linux-server", testSID)
}

func stoppedWithin(t *testing.T, term *Terminal) {
	t.Helper()
	select {
	case <-term.stopped:
	case <-time.After(termWait):
		require.FailNow(t, "the terminal's goroutines did not stop")
	}
}

func (t *Terminal) queueLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.queue)
}

func (t *Terminal) seen() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastSeq
}

var errPublish = errors.New("publish refused")

// failBroker refuses every close frame.
type failBroker struct {
	broker
	err error
}

func (b *failBroker) Publish(ctx context.Context, msg envproto.Message) error {
	if frame, err := envproto.UnmarshalFrame(msg.Payload); err == nil && frame.Kind == envproto.FrameClose {
		return b.err
	}
	return b.broker.Publish(ctx, msg)
}

func TestController_deliver(t *testing.T) {
	t.Run("onReply runs before the caller is woken", func(t *testing.T) {
		f := newTermFixture(t)
		pc := &pendingCall{env: f.id, reply: make(chan envproto.Reply, 1)}
		woken := -1
		pc.onReply = func(string, envproto.Reply) { woken = len(pc.reply) }
		f.c.mu.Lock()
		f.c.calls["req-1"] = pc
		f.c.mu.Unlock()
		require.True(t, f.c.deliver(f.id, "req-1", envproto.Reply{OK: true}))
		assert.Equal(t, 0, woken, "the reply was already waiting for the caller when onReply ran")
		assert.Len(t, pc.reply, 1)
	})
}

func TestTerminal_Hardening(t *testing.T) {
	t.Run("an exit frame after a gap writes no lost marker and asks no redraw", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "a")
		f.outFrame(t, testSID, envproto.FrameExit, 9, mustJSON(t, envproto.TerminalExit{Reason: "exited"}))
		assert.Equal(t, "a", string(next(t, term)))
		closedWithin(t, term)
		stoppedWithin(t, term)
		assert.Empty(t, f.sentKinds(envproto.FrameRedraw))
	})
	t.Run("the goroutines stop when the agent exits, without Close", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.outFrame(t, testSID, envproto.FrameExit, 1, mustJSON(t, envproto.TerminalExit{Reason: "exited"}))
		closedWithin(t, term)
		stoppedWithin(t, term)
	})
	t.Run("the goroutines stop when the environment goes offline, without Close", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		closedWithin(t, term)
		stoppedWithin(t, term)
	})
	t.Run("Close stops the goroutines though Output was never read", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "unread")
		f.data(t, 2, "unread")
		require.Eventually(t, func() bool { return term.queueLen() > 0 }, termWait, time.Millisecond)
		require.NoError(t, term.Close(t.Context()))
		stoppedWithin(t, term)
		closedWithin(t, term)
	})
	t.Run("closing the controller stops the goroutines of a terminal nobody closes", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 1, "unread")
		f.data(t, 2, "unread")
		require.Eventually(t, func() bool { return term.queueLen() > 0 }, termWait, time.Millisecond)
		require.NoError(t, f.c.Close())
		stoppedWithin(t, term)
	})
	t.Run("exactly MaxQueuedOutput bytes are kept and one more byte drops the oldest chunk", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.unstarted()
		chunks := envproto.MaxQueuedOutput / envproto.MaxChunkBytes
		term.mu.Lock()
		defer term.mu.Unlock()
		for range chunks {
			term.pushLocked(bytes.Repeat([]byte("x"), envproto.MaxChunkBytes), true)
		}
		assert.Zero(t, term.dropped)
		assert.Len(t, term.queue, chunks)
		assert.Equal(t, envproto.MaxQueuedOutput, term.queued)
		term.pushLocked([]byte("y"), true)
		assert.Equal(t, uint64(envproto.MaxChunkBytes), term.dropped)
		assert.Len(t, term.queue, chunks)
		assert.Equal(t, envproto.MaxQueuedOutput-envproto.MaxChunkBytes+1, term.queued)
	})
	t.Run("an evicted marker is not counted as dropped output", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.unstarted()
		term.mu.Lock()
		defer term.mu.Unlock()
		term.pushLocked(envproto.LostMarker(), false)
		term.pushLocked(bytes.Repeat([]byte("p"), 100), true)
		term.pushLocked(bytes.Repeat([]byte("q"), envproto.MaxQueuedOutput-50), true)
		assert.Equal(t, uint64(100), term.dropped, "only the pty bytes")
	})
	t.Run("the frame body is copied, the broker may reuse its buffer", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.unstarted()
		payload, err := envproto.MarshalFrame(envproto.Frame{Kind: envproto.FrameData, Seq: 1, Body: []byte("hello")})
		require.NoError(t, err)
		term.receive(t.Context(), payload)
		for i := range payload {
			payload[i] = 'x'
		}
		term.mu.Lock()
		defer term.mu.Unlock()
		require.Len(t, term.queue, 1)
		assert.Equal(t, "hello", string(term.queue[0].b))
	})
	t.Run("resize accepts 1 and MaxTermSize and refuses 0 and one over, each side", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		top := envproto.MaxTermSize
		for _, size := range [][2]int{{0, 24}, {top + 1, 24}, {80, 0}, {80, top + 1}, {-1, 24}, {80, -1}} {
			require.Error(t, term.Resize(t.Context(), size[0], size[1]), "%v", size)
		}
		assert.Empty(t, f.sentKinds(envproto.FrameResize))
		require.NoError(t, term.Resize(t.Context(), 1, 1))
		require.NoError(t, term.Resize(t.Context(), top, top))
		frames := f.sentKinds(envproto.FrameResize)
		require.Len(t, frames, 2)
		assert.Equal(t, []uint64{1, 2}, []uint64{frames[0].Seq, frames[1].Seq})
	})
	t.Run("a close whose frame cannot be published still reports it", func(t *testing.T) {
		f := newTermFixture(t)
		f.c.broker = &failBroker{broker: f.c.broker, err: errPublish}
		term := f.open(t)
		err := term.Close(t.Context())
		require.Error(t, err)
		require.ErrorContains(t, err, "sending to terminal "+testSID)
		assert.ErrorIs(t, err, errPublish)
	})
	t.Run("a gap inside RedrawEvery gets a trailing redraw when the window ends", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 2, "b")
		awaitFrames(t, f, envproto.FrameRedraw, 1)
		f.data(t, 4, "d")
		redraws := awaitFrames(t, f, envproto.FrameRedraw, 2)
		assert.Less(t, redraws[0].Seq, redraws[1].Seq)
		require.NoError(t, term.Close(t.Context()))
	})
	t.Run("a terminal abandoned after the agent replied ok is closed on the agent", func(t *testing.T) {
		f := newTermFixture(t)
		closes := 0
		abandoned := false
		for range 100 {
			ctx, cancel := context.WithCancel(t.Context())
			f.afterOpen = cancel
			term, _, err := f.c.OpenTerminal(ctx, "linux-server", attachArgs())
			if err == nil {
				require.NoError(t, term.Close(t.Context()))
				closes++
				continue
			}
			abandoned = true
			break
		}
		require.True(t, abandoned, "the open call never ended with the reply registered")
		require.Eventually(t, func() bool {
			n := 0
			for _, op := range f.sentOps() {
				if op == envproto.OpTerminalClose {
					n++
				}
			}
			return n > closes
		}, termWait, time.Millisecond)
		assert.Zero(t, f.c.terminalCount())
	})
	t.Run("gaps inside the window arm one trailing redraw, and it opens a new window", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		f.data(t, 2, "b")
		awaitFrames(t, f, envproto.FrameRedraw, 1)
		f.clock.Add(int64(envproto.RedrawEvery / 2))
		f.data(t, 4, "d")
		require.Eventually(t, func() bool { return term.seen() >= 4 }, termWait, time.Millisecond)
		time.Sleep(termQuietWait)
		f.data(t, 6, "f")
		f.data(t, 8, "h")
		require.Eventually(t, func() bool { return term.seen() >= 8 }, termWait, time.Millisecond)
		f.clock.Add(int64(envproto.RedrawEvery * 7 / 10))
		awaitFrames(t, f, envproto.FrameRedraw, 2)
		assert.Never(t, func() bool { return len(f.sentKinds(envproto.FrameRedraw)) > 2 }, 3*termQuietWait, time.Millisecond, "one timer is armed for the whole window")
		f.data(t, 10, "j")
		require.Eventually(t, func() bool { return term.seen() >= 10 }, termWait, time.Millisecond)
		assert.Never(t, func() bool { return len(f.sentKinds(envproto.FrameRedraw)) > 2 }, termQuietWait, time.Millisecond, "the trailing redraw starts a new window")
		require.NoError(t, term.Close(t.Context()))
	})
	t.Run("an open that is not registered is not closed on the agent", func(t *testing.T) {
		unusable := envproto.Reply{OK: true, Result: mustJSON(t, envproto.TerminalOpenResult{SID: "x"})}
		for name, reply := range map[string]*envproto.Reply{"the sid is taken": nil, "the sid is unusable": &unusable} {
			t.Run(name, func(t *testing.T) {
				f := newTermFixture(t)
				if reply == nil {
					f.open(t)
				} else {
					f.openReply = reply
				}
				ended := false
				for range 200 {
					ctx, cancel := context.WithCancel(t.Context())
					f.afterOpen = cancel
					_, _, err := f.c.OpenTerminal(ctx, "linux-server", attachArgs())
					require.Error(t, err)
					if !strings.Contains(err.Error(), "usable terminal id") {
						ended = true
						break
					}
				}
				require.True(t, ended, "the open call never ended in error after the reply")
				assert.Never(t, func() bool { return slices.Contains(f.sentOps(), envproto.OpTerminalClose) }, termQuietWait, time.Millisecond)
			})
		}
	})
	t.Run("closing the controller drops output nobody reads and stops the goroutines", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		for seq := uint64(1); seq <= 3; seq++ {
			f.data(t, seq, "unread")
		}
		require.Eventually(t, func() bool { return term.queueLen() >= 2 }, termWait, time.Millisecond)
		require.NoError(t, f.c.Close())
		stoppedWithin(t, term)
		closedWithin(t, term)
		assert.Equal(t, "disconnected", term.Exit().Reason)
	})
	t.Run("the close of an abandoned terminal is bounded when the agent never answers", func(t *testing.T) {
		f := newTermFixture(t)
		f.c.abandonedCloseWait = 20 * time.Millisecond
		abandoned := false
		for range 200 {
			ctx, cancel := context.WithCancel(t.Context())
			f.afterOpen = func() {
				f.silentClose.Store(true)
				cancel()
			}
			term, _, err := f.c.OpenTerminal(ctx, "linux-server", attachArgs())
			if err == nil {
				f.silentClose.Store(false)
				require.NoError(t, term.Close(t.Context()))
				continue
			}
			abandoned = true
			break
		}
		require.True(t, abandoned, "the open call never ended with the reply registered")
		require.Eventually(t, func() bool { return slices.Contains(f.sentOps(), envproto.OpTerminalClose) }, termWait, time.Millisecond)
		require.Eventually(t, func() bool {
			f.c.mu.Lock()
			defer f.c.mu.Unlock()
			return len(f.c.calls) == 0
		}, termWait, time.Millisecond, "the abandoned close was left waiting for the agent")
	})
}
