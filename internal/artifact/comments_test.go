package artifact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_AddThread(t *testing.T) {
	t.Run("adds a whole-artifact thread with no anchor", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		thread, comment, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "Looks good", By: Person()})
		require.NoError(t, err)
		assert.Equal(t, "t1", thread.ID)
		assert.Nil(t, thread.Anchor)
		assert.Equal(t, "c1", comment.ID)
		assert.Equal(t, []Comment{comment}, thread.Comments)
		assert.Equal(t, testBase, comment.CreatedAt)
	})
	t.Run("anchors the quote on the latest revision", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\nthe cache is shared\n")
		req := AnchorRequest{Quote: "the cache is shared"}
		thread, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "Why?", By: Person(), Anchor: &req})
		require.NoError(t, err)
		require.NotNil(t, thread.Anchor)
		assert.Equal(t, 1, thread.Anchor.Rev)
		assert.Equal(t, 2, thread.Anchor.LineStart)
		assert.Equal(t, "anchored", thread.Anchor.State)
	})
	t.Run("numbers threads in order", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		thread, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "b", By: Person()})
		require.NoError(t, err)
		assert.Equal(t, "t2", thread.ID)
	})
	t.Run("refuses an anchor on an image", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec, err := s.Add(t.Context(), &NewArtifact{Name: "pic.png", Data: pngMagic, By: Person()})
		require.NoError(t, err)
		req := AnchorRequest{Quote: "IHDR"}
		_, _, err = s.AddThread(t.Context(), rec.ID, &NewThread{Body: "x", By: Person(), Anchor: &req})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a quote that isn't in the text", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		req := AnchorRequest{Quote: "two"}
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "x", By: Person(), Anchor: &req})
		require.ErrorIs(t, err, ErrAnchorNotFound)
	})
	t.Run("refuses an empty body", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: " \n", By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a thread past the limit", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		for range MaxThreads {
			_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "x", By: Person()})
			require.NoError(t, err)
		}
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "x", By: Person()})
		require.ErrorIs(t, err, ErrFull)
	})
	t.Run("refuses an unknown artifact", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		_, _, err := s.AddThread(t.Context(), "art-00000000", &NewThread{Body: "x", By: Person()})
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_Reply(t *testing.T) {
	t.Run("appends a comment to the thread", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		thread, comment, err := s.Reply(t.Context(), rec.ID, "t1", "b", Agent("ws-1", "Ada"))
		require.NoError(t, err)
		assert.Equal(t, "c2", comment.ID)
		require.Len(t, thread.Comments, 2)
		assert.Equal(t, "Ada", *thread.Comments[1].Author.Name)
		threads, err := s.Threads(rec.ID)
		require.NoError(t, err)
		require.Len(t, threads[0].Comments, 2)
	})
	t.Run("names the missing thread", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.Reply(t.Context(), rec.ID, "t9", "b", Person())
		require.ErrorIs(t, err, ErrNotFound)
		assert.Contains(t, err.Error(), "no thread t9 on "+rec.ID)
	})
	t.Run("refuses a comment past the limit", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		for range MaxComments - 1 {
			_, _, err = s.Reply(t.Context(), rec.ID, "t1", "b", Person())
			require.NoError(t, err)
		}
		_, _, err = s.Reply(t.Context(), rec.ID, "t1", "b", Person())
		require.ErrorIs(t, err, ErrFull)
	})
	t.Run("refuses an empty body", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		_, _, err = s.Reply(t.Context(), rec.ID, "t1", "", Person())
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestStore_Resolve(t *testing.T) {
	t.Run("resolves, then reopens, clearing the resolved fields", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		resolved, err := s.Resolve(t.Context(), rec.ID, "t1", Agent("ws-1", "Ada"), false)
		require.NoError(t, err)
		assert.True(t, resolved.Resolved)
		assert.Equal(t, testBase, *resolved.ResolvedAt)
		assert.Equal(t, "Ada", *resolved.ResolvedBy.Name)
		reopened, err := s.Resolve(t.Context(), rec.ID, "t1", Person(), true)
		require.NoError(t, err)
		assert.False(t, reopened.Resolved)
		assert.Nil(t, reopened.ResolvedAt)
		assert.Nil(t, reopened.ResolvedBy)
	})
	t.Run("resolving a resolved thread changes nothing", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		first, err := s.Resolve(t.Context(), rec.ID, "t1", Person(), false)
		require.NoError(t, err)
		again, err := s.Resolve(t.Context(), rec.ID, "t1", Agent("ws-1", "Ada"), false)
		require.NoError(t, err)
		assert.Equal(t, first.ResolvedBy, again.ResolvedBy)
	})
	t.Run("reopening an open thread changes nothing", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, err := s.AddThread(t.Context(), rec.ID, &NewThread{Body: "a", By: Person()})
		require.NoError(t, err)
		thread, err := s.Resolve(t.Context(), rec.ID, "t1", Person(), true)
		require.NoError(t, err)
		assert.False(t, thread.Resolved)
	})
	t.Run("names the missing thread", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, err := s.Resolve(t.Context(), rec.ID, "t3", Person(), false)
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestCheckBody(t *testing.T) {
	require.ErrorIs(t, checkBody(""), ErrInvalid)
	require.ErrorIs(t, checkBody(" \t\n"), ErrInvalid)
	require.ErrorIs(t, checkBody(strings.Repeat("é", MaxBodyRunes+1)), ErrInvalid)
	assert.NoError(t, checkBody(strings.Repeat("é", MaxBodyRunes)))
	assert.NoError(t, checkBody("**bold**\n\nand more"))
}

func TestThreadNamed(t *testing.T) {
	threads := []Thread{{ID: "t1"}, {ID: "t2"}}
	thread, err := threadNamed("art-00000001", threads, "t2")
	require.NoError(t, err)
	assert.Equal(t, "t2", thread.ID)
	thread.Resolved = true
	assert.True(t, threads[1].Resolved)
	_, err = threadNamed("art-00000001", threads, "t3")
	require.ErrorIs(t, err, ErrNotFound)
	assert.EqualError(t, err, "no such artifact: no thread t3 on art-00000001")
}
