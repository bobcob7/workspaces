package envagent

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// limit is a token bucket's size and how long it takes to refill completely.
type limit struct {
	capacity float64
	refill   time.Duration
}

// The limits of ADR 0078.
func reviewStartLimit() limit { return limit{capacity: 12, refill: time.Hour} }
func loopRunLimit() limit     { return limit{capacity: 30, refill: time.Hour} }
func loopRunNameLimit() limit { return limit{capacity: 1, refill: time.Minute} }
func loopWriteLimit() limit   { return limit{capacity: 60, refill: time.Hour} }

// maxBuckets is how many buckets the limiter holds before it forgets the full ones.
const maxBuckets = 256

// tokenBucket is the tokens left under one key at a time.
type tokenBucket struct {
	tokens float64
	at     time.Time
}

// limiter holds ADR 0078's token buckets.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	now     func() time.Time
}

// newLimiter returns a limiter with every bucket full.
func newLimiter(now func() time.Time) *limiter {
	return &limiter{buckets: make(map[string]*tokenBucket), now: now}
}

// keyedLimit is a bucket's key and size.
type keyedLimit struct {
	key string
	limit
}

// limitsFor is the buckets op with args draws one token from, none for an
// operation that isn't limited.
func limitsFor(op string, args any) []keyedLimit {
	switch op {
	case envproto.OpReviewStart:
		return []keyedLimit{{"review.start", reviewStartLimit()}}
	case envproto.OpLoopRun:
		out := []keyedLimit{{"loop.run", loopRunLimit()}}
		if run, ok := args.(*envproto.LoopRunArgs); ok {
			out = append(out, keyedLimit{"loop.run:" + run.Name, loopRunNameLimit()})
		}
		return out
	case envproto.OpLoopAdd, envproto.OpLoopEdit, envproto.OpLoopRm:
		return []keyedLimit{{"loop.write", loopWriteLimit()}}
	}
	return nil
}

// allow reports whether op with args may run now, and if not, how long until
// it may. An allowed call takes one token from each of its buckets; a refused
// one takes none.
func (l *limiter) allow(op string, args any) (ok bool, retry time.Duration) {
	limits := limitsFor(op, args)
	if len(limits) == 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.forgetFull(now)
	for _, kl := range limits {
		b := l.refilled(kl, now)
		if b.tokens < 1 {
			retry = max(retry, time.Duration((1-b.tokens)/kl.rate()))
		}
	}
	if retry > 0 {
		return false, retry
	}
	for _, kl := range limits {
		l.buckets[kl.key].tokens--
	}
	return true, 0
}

// rate is the tokens a bucket gains per nanosecond.
func (kl keyedLimit) rate() float64 {
	return kl.capacity / float64(kl.refill)
}

// refilled returns kl's tokenBucket with the tokens it has gained by now, creating it full.
func (l *limiter) refilled(kl keyedLimit, now time.Time) *tokenBucket {
	b, ok := l.buckets[kl.key]
	if !ok {
		b = &tokenBucket{tokens: kl.capacity, at: now}
		l.buckets[kl.key] = b
		return b
	}
	if gained := float64(now.Sub(b.at)) * kl.rate(); gained > 0 {
		b.tokens = math.Min(kl.capacity, b.tokens+gained)
		b.at = now
	}
	return b
}

// forgetFull drops the per-loop buckets that are full again once there are many.
// The caller holds l.mu.
func (l *limiter) forgetFull(now time.Time) {
	if len(l.buckets) <= maxBuckets {
		return
	}
	for key, b := range l.buckets {
		if key != "loop.run" && key != "review.start" && key != "loop.write" && now.Sub(b.at) >= loopRunNameLimit().refill {
			delete(l.buckets, key)
		}
	}
}

// limitedError is the refusal of op, which may run again in retry.
func limitedError(op string, retry time.Duration) *envproto.ReplyError {
	seconds := max(1, int(math.Ceil(retry.Seconds())))
	return &envproto.ReplyError{
		Code:    envproto.CodeOperationLimited,
		Message: fmt.Sprintf("%s is limited; try again in %d seconds", op, seconds),
		Details: map[string]any{"op": op, "retry_after_seconds": seconds},
	}
}
