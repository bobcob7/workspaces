package envagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errRequestMismatch is returned for a request id that was used before for another operation or other arguments.
var errRequestMismatch = errors.New("request id reused for a different request")

// errAbandoned is returned to a duplicate whose original panicked.
var errAbandoned = errors.New("the original request didn't finish")

// requests remembers operations by request id (D4), so an operation the
// controller sends again after a lost reply runs once.
type requests struct {
	mu      sync.Mutex
	entries map[string]*requestEntry
	order   []string      // finished ids, oldest first
	keep    time.Duration // how long a finished reply is remembered
	max     int           // how many finished replies are remembered
	budget  int           // how many bytes of finished replies are remembered
	bytes   int           // bytes of the finished replies held now
	now     func() time.Time
}

// requestEntry is one request id; done closes once reply is set.
type requestEntry struct {
	done     chan struct{}
	sum      string // fingersum of the op and its arguments
	reply    []byte
	finished time.Time
}

// newRequests returns an empty store that remembers up to 512 finished
// replies, 8 MiB of them, for 15 minutes.
func newRequests(now func() time.Time) *requests {
	return &requests{
		entries: make(map[string]*requestEntry),
		keep:    15 * time.Minute,
		max:     512,
		budget:  8 << 20,
		now:     now,
	}
}

// do runs fn once per id and returns its reply: a duplicate of a running id
// waits for it (or ctx) without running anything, a finished one returns the
// stored reply. sum identifies the op and its arguments, and a duplicate
// with another sum gets errRequestMismatch. An empty id isn't remembered,
// and neither is a reply when remember is false, when ctx has ended by the
// time fn returns, or when fn panicked (do then returns an error, and the id is
// free again).
func (r *requests) do(ctx context.Context, id, sum string, remember bool, fn func() []byte) ([]byte, error) {
	if id == "" {
		return runRecovered(fn)
	}
	entry, owner := r.claim(id, sum)
	if !owner {
		if entry.sum != sum {
			return nil, errRequestMismatch
		}
		return entry.wait(ctx)
	}
	reply, err := runRecovered(fn)
	if err != nil {
		r.drop(id, entry, nil)
		return nil, err
	}
	if !remember || ended(ctx) {
		r.drop(id, entry, reply)
		return reply, nil
	}
	entry.reply = reply
	r.finish(id, entry)
	return reply, nil
}

// runRecovered calls fn, turning a panic into an error.
func runRecovered(fn func() []byte) (reply []byte, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("operation panicked: %v", p)
		}
	}()
	return fn(), nil
}

// claim returns id's entry, and whether the caller owns it and so must run
// the operation and finish or drop the entry.
func (r *requests) claim(id, sum string) (*requestEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	if entry, ok := r.entries[id]; ok {
		return entry, false
	}
	entry := &requestEntry{done: make(chan struct{}), sum: sum}
	r.entries[id] = entry
	return entry, true
}

// finish stores entry's reply and wakes the duplicates waiting for it.
func (r *requests) finish(id string, entry *requestEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry.finished = r.now()
	r.order = append(r.order, id)
	r.bytes += len(entry.reply)
	close(entry.done)
	r.prune()
}

// drop releases entry without remembering it, so the id can run again, and
// wakes the duplicates waiting for it with reply.
func (r *requests) drop(id string, entry *requestEntry, reply []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
	entry.reply = reply
	close(entry.done)
}

// prune forgets the finished ids older than keep and the oldest beyond max
// or the byte budget. A running id isn't in order, so it is never forgotten.
// The caller holds r.mu.
func (r *requests) prune() {
	now := r.now()
	for len(r.order) > 0 {
		oldest := r.entries[r.order[0]]
		if len(r.order) <= r.max && r.bytes <= r.budget && now.Sub(oldest.finished) <= r.keep {
			return
		}
		r.bytes -= len(oldest.reply)
		delete(r.entries, r.order[0])
		r.order = r.order[1:]
	}
}

// wait returns the entry's reply once it's finished, or ctx's error first.
func (e *requestEntry) wait(ctx context.Context) ([]byte, error) {
	select {
	case <-e.done:
		return e.result()
	default:
	}
	select {
	case <-e.done:
		return e.result()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// result is the reply of a finished entry; an entry dropped without one was abandoned.
func (e *requestEntry) result() ([]byte, error) {
	if e.reply == nil {
		return nil, errAbandoned
	}
	return e.reply, nil
}

// ended reports whether ctx is done.
func ended(ctx context.Context) bool {
	return ctx.Err() != nil
}
