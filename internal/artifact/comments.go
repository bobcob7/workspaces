package artifact

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// NewThread is a thread's first comment and its optional anchor.
type NewThread struct {
	Body   string
	By     Actor
	Anchor *AnchorRequest // nil: about the whole artifact
}

// AddThread anchors (on the latest revision's text) and appends a thread
// t<len+1> with comment c1. An anchor on an image is ErrInvalid.
func (s *Store) AddThread(ctx context.Context, id string, n *NewThread) (Thread, Comment, error) {
	if err := checkBody(n.Body); err != nil {
		return Thread{}, Comment{}, err
	}
	var thread Thread
	var comment Comment
	err := s.lockedThreads(ctx, id, func(dir string, rec *Record, threads []Thread) ([]Thread, error) {
		if len(threads) >= MaxThreads {
			return nil, fmt.Errorf("%w: an artifact keeps at most %d threads", ErrFull, MaxThreads)
		}
		comment = s.newComment("c1", n.Body, n.By)
		thread = Thread{ID: fmt.Sprintf("t%d", len(threads)+1), Comments: []Comment{comment}}
		if n.Anchor != nil {
			anchor, err := s.newAnchor(dir, rec, *n.Anchor)
			if err != nil {
				return nil, err
			}
			thread.Anchor = &anchor
		}
		return append(threads, thread), nil
	})
	return thread, comment, err
}

// Reply appends comment c<len+1> to thread threadID ("t3").
func (s *Store) Reply(ctx context.Context, id, threadID, body string, by Actor) (Thread, Comment, error) {
	if err := checkBody(body); err != nil {
		return Thread{}, Comment{}, err
	}
	var thread Thread
	var comment Comment
	err := s.lockedThreads(ctx, id, func(_ string, _ *Record, threads []Thread) ([]Thread, error) {
		t, err := threadNamed(id, threads, threadID)
		if err != nil {
			return nil, err
		}
		if len(t.Comments) >= MaxComments {
			return nil, fmt.Errorf("%w: a thread keeps at most %d comments", ErrFull, MaxComments)
		}
		comment = s.newComment(fmt.Sprintf("c%d", len(t.Comments)+1), body, by)
		t.Comments = append(t.Comments, comment)
		thread = *t
		return threads, nil
	})
	return thread, comment, err
}

// Resolve sets or, with reopen, clears resolved, resolved_at and resolved_by.
func (s *Store) Resolve(ctx context.Context, id, threadID string, by Actor, reopen bool) (Thread, error) {
	var thread Thread
	err := s.lockedThreads(ctx, id, func(_ string, _ *Record, threads []Thread) ([]Thread, error) {
		t, err := threadNamed(id, threads, threadID)
		if err != nil {
			return nil, err
		}
		if t.Resolved != !reopen {
			s.setResolved(t, !reopen, by)
		}
		thread = *t
		return threads, nil
	})
	return thread, err
}

// newComment is comment id by by at the store's clock.
func (s *Store) newComment(id, body string, by Actor) Comment {
	return Comment{ID: id, Author: by, Body: body, CreatedAt: s.now().UTC().Truncate(time.Second)}
}

// setResolved marks t resolved by by, or open again with resolved fields
// cleared.
func (s *Store) setResolved(t *Thread, resolved bool, by Actor) {
	t.Resolved = resolved
	if !resolved {
		t.ResolvedAt, t.ResolvedBy = nil, nil
		return
	}
	now := s.now().UTC().Truncate(time.Second)
	t.ResolvedAt = &now
	t.ResolvedBy = &by
}

// lockedThreads holds artifact id's lock, reads its record and threads, and
// passes them to edit. The threads edit returns are written back.
func (s *Store) lockedThreads(ctx context.Context, id string, edit func(dir string, rec *Record, threads []Thread) ([]Thread, error)) error {
	dir, err := s.dirOf(id)
	if err != nil {
		return err
	}
	if err = s.prepare(); err != nil {
		return err
	}
	unlock, err := lockArtifact(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	rec, err := readRecord(dir)
	if err != nil {
		return err
	}
	threads, err := readThreads(dir)
	if err != nil {
		return err
	}
	threads, err = edit(dir, &rec, threads)
	if err != nil {
		return err
	}
	return writeThreads(s.tmpDir(), dir, threads)
}

// newAnchor locates req in the latest revision of rec, whose bytes are in
// dir.
func (s *Store) newAnchor(dir string, rec *Record, req AnchorRequest) (Anchor, error) {
	if rec.Kind == KindImage {
		return Anchor{}, fmt.Errorf("%w: an image takes whole-artifact comments", ErrInvalid)
	}
	rev := rec.Latest().Rev
	data, err := os.ReadFile(revisionPath(dir, rev))
	if err != nil {
		return Anchor{}, fmt.Errorf("reading %s r%d: %w", rec.ID, rev, err)
	}
	return Locate(string(data), rev, req)
}

// threadNamed is the thread with id threadID, or ErrNotFound.
func threadNamed(artifactID string, threads []Thread, threadID string) (*Thread, error) {
	for i := range threads {
		if threads[i].ID == threadID {
			return &threads[i], nil
		}
	}
	return nil, fmt.Errorf("%w: no thread %s on %s", ErrNotFound, threadID, artifactID)
}

// checkBody accepts 1 to MaxBodyRunes characters that aren't only whitespace.
func checkBody(body string) error {
	switch {
	case strings.TrimSpace(body) == "":
		return fmt.Errorf("%w: a comment can't be empty", ErrInvalid)
	case utf8.RuneCountInString(body) > MaxBodyRunes:
		return fmt.Errorf("%w: a comment is at most %d characters", ErrInvalid, MaxBodyRunes)
	}
	return nil
}
