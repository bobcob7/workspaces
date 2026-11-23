package envagent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// limitClock is a clock the test moves.
type limitClock struct {
	mu  sync.Mutex
	now time.Time
}

func newLimitClock() *limitClock {
	return &limitClock{now: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)}
}

func (c *limitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *limitClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestLimiter_allow(t *testing.T) {
	t.Parallel()
	t.Run("the 13th review.start in an hour is limited", func(t *testing.T) {
		t.Parallel()
		clock := newLimitClock()
		l := newLimiter(clock.Now)
		for i := range 12 {
			ok, _ := l.allow(envproto.OpReviewStart, &envproto.ReviewStartArgs{})
			require.True(t, ok, "start %d", i+1)
			clock.advance(time.Second)
		}
		ok, retry := l.allow(envproto.OpReviewStart, &envproto.ReviewStartArgs{})
		assert.False(t, ok)
		assert.Positive(t, retry)
		assert.LessOrEqual(t, retry, 5*time.Minute)
	})
	t.Run("loop.run of one loop twice in 60 s is limited", func(t *testing.T) {
		t.Parallel()
		clock := newLimitClock()
		l := newLimiter(clock.Now)
		args := &envproto.LoopRunArgs{Name: "nightly"}
		ok, _ := l.allow(envproto.OpLoopRun, args)
		require.True(t, ok)
		clock.advance(30 * time.Second)
		ok, retry := l.allow(envproto.OpLoopRun, args)
		assert.False(t, ok)
		assert.Equal(t, 30*time.Second, retry)
		other, _ := l.allow(envproto.OpLoopRun, &envproto.LoopRunArgs{Name: "weekly"})
		assert.True(t, other, "another loop has its own bucket")
		clock.advance(30 * time.Second)
		ok, _ = l.allow(envproto.OpLoopRun, args)
		assert.True(t, ok)
	})
	t.Run("a refused call takes no token", func(t *testing.T) {
		t.Parallel()
		clock := newLimitClock()
		l := newLimiter(clock.Now)
		args := &envproto.LoopRunArgs{Name: "nightly"}
		ok, _ := l.allow(envproto.OpLoopRun, args)
		require.True(t, ok)
		for range 40 {
			ok, _ = l.allow(envproto.OpLoopRun, args)
			require.False(t, ok)
		}
		clock.advance(time.Minute)
		for i := range 29 {
			ok, _ = l.allow(envproto.OpLoopRun, &envproto.LoopRunArgs{Name: "loop-" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
			require.True(t, ok, "run %d", i)
		}
	})
	t.Run("the 31st loop.run is limited", func(t *testing.T) {
		t.Parallel()
		clock := newLimitClock()
		l := newLimiter(clock.Now)
		for i := range 30 {
			ok, _ := l.allow(envproto.OpLoopRun, &envproto.LoopRunArgs{Name: "loop-" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
			require.True(t, ok, "run %d", i+1)
		}
		ok, _ := l.allow(envproto.OpLoopRun, &envproto.LoopRunArgs{Name: "fresh"})
		assert.False(t, ok)
	})
	t.Run("loop.add, loop.edit and loop.rm share 60 an hour", func(t *testing.T) {
		t.Parallel()
		l := newLimiter(newLimitClock().Now)
		ops := []string{envproto.OpLoopAdd, envproto.OpLoopEdit, envproto.OpLoopRm}
		for i := range 60 {
			ok, _ := l.allow(ops[i%3], nil)
			require.True(t, ok, "write %d", i+1)
		}
		for _, op := range ops {
			ok, _ := l.allow(op, nil)
			assert.False(t, ok, op)
		}
	})
	t.Run("tokens come back over time", func(t *testing.T) {
		t.Parallel()
		clock := newLimitClock()
		l := newLimiter(clock.Now)
		for range 12 {
			ok, _ := l.allow(envproto.OpReviewStart, &envproto.ReviewStartArgs{})
			require.True(t, ok)
		}
		ok, _ := l.allow(envproto.OpReviewStart, &envproto.ReviewStartArgs{})
		require.False(t, ok)
		clock.advance(5 * time.Minute)
		ok, _ = l.allow(envproto.OpReviewStart, &envproto.ReviewStartArgs{})
		assert.True(t, ok)
		ok, _ = l.allow(envproto.OpReviewStart, &envproto.ReviewStartArgs{})
		assert.False(t, ok)
	})
	t.Run("review.status is never limited", func(t *testing.T) {
		t.Parallel()
		l := newLimiter(newLimitClock().Now)
		for range 1000 {
			ok, retry := l.allow(envproto.OpReviewStatus, &envproto.ReviewRef{})
			require.True(t, ok)
			require.Zero(t, retry)
		}
	})
}
