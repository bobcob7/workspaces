package envagent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is a clock a test moves by hand.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestRequests_do(t *testing.T) {
	t.Run("runs fn once for two concurrent calls with one id", func(t *testing.T) {
		r := newRequests(time.Now)
		release := make(chan struct{})
		var runs atomic.Int32
		fn := func() []byte {
			runs.Add(1)
			<-release
			return []byte("done")
		}
		replies := make(chan []byte, 2)
		for range 2 {
			go func() {
				reply, err := r.do(t.Context(), "a1", fn)
				assert.NoError(t, err)
				replies <- reply
			}()
		}
		require.Eventually(t, func() bool { return runs.Load() == 1 }, 5*time.Second, time.Millisecond)
		close(release)
		assert.Equal(t, []byte("done"), <-replies)
		assert.Equal(t, []byte("done"), <-replies)
		assert.EqualValues(t, 1, runs.Load())
	})
	t.Run("returns the stored reply after it finished", func(t *testing.T) {
		r := newRequests(time.Now)
		_, err := r.do(t.Context(), "a1", func() []byte { return []byte("first") })
		require.NoError(t, err)
		reply, err := r.do(t.Context(), "a1", func() []byte {
			t.Error("fn ran again for a finished id")
			return []byte("second")
		})
		require.NoError(t, err)
		assert.Equal(t, []byte("first"), reply)
	})
	t.Run("forgets ids after keep", func(t *testing.T) {
		clock := &fakeClock{now: time.Unix(1000, 0)}
		r := newRequests(clock.Now)
		r.keep = time.Minute
		_, err := r.do(t.Context(), "a1", func() []byte { return []byte("first") })
		require.NoError(t, err)
		clock.now = clock.now.Add(time.Minute + time.Second)
		reply, err := r.do(t.Context(), "a1", func() []byte { return []byte("second") })
		require.NoError(t, err)
		assert.Equal(t, []byte("second"), reply)
	})
	t.Run("drops the oldest finished beyond max", func(t *testing.T) {
		r := newRequests(time.Now)
		r.max = 2
		for _, id := range []string{"a1", "b2", "c3"} {
			_, err := r.do(t.Context(), id, func() []byte { return []byte(id) })
			require.NoError(t, err)
		}
		reply, err := r.do(t.Context(), "b2", func() []byte { return []byte("again") })
		require.NoError(t, err)
		assert.Equal(t, []byte("b2"), reply)
		reply, err = r.do(t.Context(), "a1", func() []byte { return []byte("again") })
		require.NoError(t, err)
		assert.Equal(t, []byte("again"), reply)
	})
	t.Run("an empty id isn't remembered", func(t *testing.T) {
		r := newRequests(time.Now)
		var runs atomic.Int32
		fn := func() []byte {
			runs.Add(1)
			return nil
		}
		_, err := r.do(t.Context(), "", fn)
		require.NoError(t, err)
		_, err = r.do(t.Context(), "", fn)
		require.NoError(t, err)
		assert.EqualValues(t, 2, runs.Load())
	})
	t.Run("a waiting duplicate returns ctx's error when ctx ends", func(t *testing.T) {
		r := newRequests(time.Now)
		release := make(chan struct{})
		started := make(chan struct{})
		go func() {
			_, _ = r.do(t.Context(), "a1", func() []byte {
				close(started)
				<-release
				return []byte("done")
			})
		}()
		<-started
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := r.do(ctx, "a1", func() []byte { return nil })
		assert.ErrorIs(t, err, context.Canceled)
		close(release)
	})
}
