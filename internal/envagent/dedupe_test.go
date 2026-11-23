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
				reply, err := r.do(t.Context(), "a1", "s", true, fn)
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
		_, err := r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("first") })
		require.NoError(t, err)
		reply, err := r.do(t.Context(), "a1", "s", true, func() []byte {
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
		_, err := r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("first") })
		require.NoError(t, err)
		clock.now = clock.now.Add(time.Minute)
		reply, err := r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("second") })
		require.NoError(t, err)
		assert.Equal(t, []byte("first"), reply, "a reply is kept at exactly keep")
		clock.now = clock.now.Add(time.Nanosecond)
		reply, err = r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("second") })
		require.NoError(t, err)
		assert.Equal(t, []byte("second"), reply)
	})
	t.Run("drops the oldest finished beyond max", func(t *testing.T) {
		r := newRequests(time.Now)
		r.max = 2
		for _, id := range []string{"a1", "b2", "c3"} {
			_, err := r.do(t.Context(), id, "s", true, func() []byte { return []byte(id) })
			require.NoError(t, err)
		}
		assert.Len(t, r.order, 2)
		assert.Len(t, r.entries, 2)
		reply, err := r.do(t.Context(), "b2", "s", true, func() []byte { return []byte("again") })
		require.NoError(t, err)
		assert.Equal(t, []byte("b2"), reply)
		reply, err = r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("again") })
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
		_, err := r.do(t.Context(), "", "s", true, fn)
		require.NoError(t, err)
		_, err = r.do(t.Context(), "", "s", true, fn)
		require.NoError(t, err)
		assert.EqualValues(t, 2, runs.Load())
	})
	t.Run("a waiting duplicate returns ctx's error when ctx ends", func(t *testing.T) {
		r := newRequests(time.Now)
		release := make(chan struct{})
		started := make(chan struct{})
		go func() {
			_, _ = r.do(t.Context(), "a1", "s", true, func() []byte {
				close(started)
				<-release
				return []byte("done")
			})
		}()
		<-started
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := r.do(ctx, "a1", "s", true, func() []byte { return nil })
		require.ErrorIs(t, err, context.Canceled)
		close(release)
	})
	t.Run("a running id isn't pruned when another id finishes", func(t *testing.T) {
		clock := &fakeClock{now: time.Unix(1000, 0)}
		r := newRequests(clock.Now)
		r.keep = time.Minute
		r.max = 1
		release := make(chan struct{})
		started := make(chan struct{})
		var runs atomic.Int32
		go func() {
			_, _ = r.do(t.Context(), "slow", "s", true, func() []byte {
				runs.Add(1)
				close(started)
				<-release
				return []byte("slow")
			})
		}()
		<-started
		for _, id := range []string{"a1", "b2", "c3"} {
			clock.now = clock.now.Add(time.Hour)
			_, err := r.do(t.Context(), id, "s", true, func() []byte { return []byte(id) })
			require.NoError(t, err)
		}
		replies := make(chan []byte, 1)
		go func() {
			reply, err := r.do(t.Context(), "slow", "s", true, func() []byte {
				runs.Add(1)
				return []byte("again")
			})
			assert.NoError(t, err)
			replies <- reply
		}()
		close(release)
		assert.Equal(t, []byte("slow"), <-replies)
		assert.EqualValues(t, 1, runs.Load())
	})
	t.Run("a reply from an ended ctx isn't remembered", func(t *testing.T) {
		r := newRequests(time.Now)
		ctx, cancel := context.WithCancel(t.Context())
		reply, err := r.do(ctx, "a1", "s", true, func() []byte {
			cancel()
			return []byte("failed")
		})
		require.NoError(t, err)
		assert.Equal(t, []byte("failed"), reply)
		reply, err = r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("ok") })
		require.NoError(t, err)
		assert.Equal(t, []byte("ok"), reply)
	})
	t.Run("a panic frees the id", func(t *testing.T) {
		r := newRequests(time.Now)
		_, err := r.do(t.Context(), "a1", "s", true, func() []byte { panic("boom") })
		require.Error(t, err)
		reply, err := r.do(t.Context(), "a1", "s", true, func() []byte { return []byte("ok") })
		require.NoError(t, err)
		assert.Equal(t, []byte("ok"), reply)
	})
	t.Run("a duplicate of a dropped entry is told it was abandoned", func(t *testing.T) {
		r := newRequests(time.Now)
		entry, owner := r.claim("a1", "s")
		require.True(t, owner)
		waited := make(chan error, 1)
		go func() {
			_, err := entry.wait(t.Context())
			waited <- err
		}()
		r.drop("a1", entry, nil)
		require.ErrorIs(t, <-waited, errAbandoned)
	})
	t.Run("a reply with remember false isn't stored", func(t *testing.T) {
		r := newRequests(time.Now)
		_, err := r.do(t.Context(), "a1", "s", false, func() []byte { return []byte("secret") })
		require.NoError(t, err)
		reply, err := r.do(t.Context(), "a1", "s", false, func() []byte { return []byte("fresh") })
		require.NoError(t, err)
		assert.Equal(t, []byte("fresh"), reply)
	})
	t.Run("a repeated id with another sum is refused", func(t *testing.T) {
		r := newRequests(time.Now)
		_, err := r.do(t.Context(), "a1", "one", true, func() []byte { return []byte("first") })
		require.NoError(t, err)
		_, err = r.do(t.Context(), "a1", "two", true, func() []byte { return []byte("second") })
		require.ErrorIs(t, err, errRequestMismatch)
	})
	t.Run("the oldest replies go when the byte budget is exceeded", func(t *testing.T) {
		r := newRequests(time.Now)
		r.budget = 10
		for _, id := range []string{"a1", "b2", "c3"} {
			_, err := r.do(t.Context(), id, "s", true, func() []byte { return []byte("123456") })
			require.NoError(t, err)
		}
		assert.Equal(t, 6, r.bytes)
		assert.Len(t, r.order, 1)
		reply, err := r.do(t.Context(), "c3", "s", true, func() []byte { return []byte("again") })
		require.NoError(t, err)
		assert.Equal(t, []byte("123456"), reply)
	})
}
