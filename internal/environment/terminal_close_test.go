package environment

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// stallBroker blocks input-topic publishes until their ctx ends while stalled is set.
type stallBroker struct {
	broker
	stalled atomic.Bool
	waiting chan struct{}
}

func (s *stallBroker) Publish(ctx context.Context, msg envproto.Message) error {
	if s.stalled.Load() {
		frame, err := envproto.UnmarshalFrame(msg.Payload)
		if err == nil && frame.Kind == envproto.FrameInput {
			select {
			case s.waiting <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		}
	}
	return s.broker.Publish(ctx, msg)
}

func TestTerminal_CloseDoesNotTrustTheCaller(t *testing.T) {
	t.Run("a cancelled ctx still sends the close frame and terminal.close", func(t *testing.T) {
		f := newTermFixture(t)
		term := f.open(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.NoError(t, term.Close(ctx))
		assert.Contains(t, f.sentOps(), envproto.OpTerminalClose)
		assert.NotEmpty(t, f.sentKinds(envproto.FrameClose))
	})
	t.Run("Close does not queue behind an Input stuck in a publish", func(t *testing.T) {
		f := newTermFixture(t)
		sb := &stallBroker{broker: f.c.broker, waiting: make(chan struct{}, 1)}
		f.c.broker = sb
		term := f.open(t)
		sb.stalled.Store(true)
		inputDone := make(chan error, 1)
		go func() { inputDone <- term.Input(context.WithoutCancel(t.Context()), []byte("x")) }()
		select {
		case <-sb.waiting:
		case <-time.After(termWait):
			require.FailNow(t, "the input publish never started")
		}
		closeDone := make(chan error, 1)
		go func() { closeDone <- term.Close(t.Context()) }()
		select {
		case err := <-closeDone:
			require.NoError(t, err)
		case <-time.After(termWait):
			require.FailNow(t, "Close queued behind the stuck Input")
		}
		select {
		case err := <-inputDone:
			require.Error(t, err)
		case <-time.After(termWait):
			require.FailNow(t, "the stuck Input was not released")
		}
		require.Eventually(t, func() bool { return slices.Contains(f.sentOps(), envproto.OpTerminalClose) }, termWait, time.Millisecond)
		assert.Equal(t, "closed", term.Exit().Reason)
	})
}
