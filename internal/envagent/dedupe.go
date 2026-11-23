package envagent

import (
	"context"
	"sync"
	"time"
)

// requests remembers operations by request id (D4), so an operation the
// controller sends again after a lost reply runs once.
type requests struct {
	mu      sync.Mutex
	entries map[string]*requestEntry
	order   []string      // finished ids, oldest first
	keep    time.Duration // how long a finished reply is remembered
	max     int           // how many finished replies are remembered
	now     func() time.Time
}

// requestEntry is one request id; done closes once reply is set.
type requestEntry struct {
	done     chan struct{}
	reply    []byte
	finished time.Time
}

// newRequests returns an empty store that remembers 512 finished replies for 15 minutes.
func newRequests(now func() time.Time) *requests {
	return &requests{
		entries: make(map[string]*requestEntry),
		keep:    15 * time.Minute,
		max:     512,
		now:     now,
	}
}

// do runs fn once per id and returns its reply: a duplicate of a running id
// waits for it (or ctx), a finished one returns the stored reply. An empty
// id isn't remembered.
func (r *requests) do(ctx context.Context, id string, fn func() []byte) ([]byte, error) {
	if id == "" {
		return fn(), nil
	}
	entry, owner := r.claim(id)
	if !owner {
		return entry.wait(ctx)
	}
	entry.reply = fn()
	r.finish(id, entry)
	return entry.reply, nil
}

// claim returns id's entry, and whether the caller owns it and so must run
// the operation and finish the entry.
func (r *requests) claim(id string) (*requestEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	if entry, ok := r.entries[id]; ok {
		return entry, false
	}
	entry := &requestEntry{done: make(chan struct{})}
	r.entries[id] = entry
	return entry, true
}

// finish stores entry's reply and wakes the duplicates waiting for it.
func (r *requests) finish(id string, entry *requestEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry.finished = r.now()
	r.order = append(r.order, id)
	close(entry.done)
	r.prune()
}

// prune forgets the finished ids older than keep and the oldest beyond max.
// The caller holds r.mu.
func (r *requests) prune() {
	now := r.now()
	for len(r.order) > 0 {
		oldest := r.entries[r.order[0]]
		if len(r.order) <= r.max && now.Sub(oldest.finished) <= r.keep {
			return
		}
		delete(r.entries, r.order[0])
		r.order = r.order[1:]
	}
}

// wait returns the entry's reply once it's finished, or ctx's error first.
func (e *requestEntry) wait(ctx context.Context) ([]byte, error) {
	select {
	case <-e.done:
		return e.reply, nil
	default:
	}
	select {
	case <-e.done:
		return e.reply, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
