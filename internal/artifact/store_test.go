package artifact

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testBase = time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)

func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func newTestStore(t *testing.T, now func() time.Time) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "artifacts")
	return NewStore(dir, rand.Reader, now), dir
}

func addText(t *testing.T, s *Store, name, data string) Record {
	t.Helper()
	rec, err := s.Add(t.Context(), &NewArtifact{Name: name, Data: []byte(data), By: Person()})
	require.NoError(t, err)
	return rec
}

func TestStore_Add(t *testing.T) {
	t.Run("stores a record and its first revision", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec, err := s.Add(t.Context(), &NewArtifact{
			Name:  "pitch.md",
			Title: "Pitch",
			Note:  "first draft",
			Data:  []byte("# Pitch\n"),
			By:    Agent("pitch-ab12cd34", "pitch"),
			Root:  "pitch-ab12cd34",
		})
		require.NoError(t, err)
		assert.Regexp(t, `^art-[0-9a-f]{8}$`, rec.ID)
		assert.Equal(t, KindMarkdown, rec.Kind)
		assert.Equal(t, "text/markdown; charset=utf-8", rec.ContentType)
		require.NotNil(t, rec.Title)
		assert.Equal(t, "Pitch", *rec.Title)
		require.NotNil(t, rec.Root)
		assert.Equal(t, "pitch-ab12cd34", *rec.Root)
		require.Len(t, rec.Revisions, 1)
		assert.Equal(t, 1, rec.Revisions[0].Rev)
		require.NotNil(t, rec.Revisions[0].Note)
		assert.Equal(t, "first draft", *rec.Revisions[0].Note)
	})
	t.Run("round-trips through Show", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "hello\n")
		entry, err := s.Show(rec.ID)
		require.NoError(t, err)
		assert.Equal(t, rec.ID, entry.Record.ID)
		assert.Equal(t, testBase, entry.Record.CreatedAt)
		assert.Equal(t, Person(), entry.Record.CreatedBy)
		assert.Nil(t, entry.Record.Root)
		assert.Nil(t, entry.Record.Revisions[0].Note)
		assert.Equal(t, ThreadCounts{}, entry.Threads)
	})
	t.Run("retries a taken id once", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "artifacts")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "art-01020304"), 0o700))
		s := NewStore(dir, bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8}), fixedClock(testBase))
		rec, err := s.Add(t.Context(), &NewArtifact{Name: "a.txt", Data: []byte("a"), By: Person()})
		require.NoError(t, err)
		assert.Equal(t, "art-05060708", rec.ID)
	})
	t.Run("gives up when every id is taken", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "artifacts")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "art-01010101"), 0o700))
		s := NewStore(dir, bytes.NewReader(bytes.Repeat([]byte{1}, 4*idAttempts)), fixedClock(testBase))
		_, err := s.Add(t.Context(), &NewArtifact{Name: "a.txt", Data: []byte("a"), By: Person()})
		require.ErrorIs(t, err, os.ErrExist)
	})
	t.Run("fails when the random source is empty", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "artifacts")
		s := NewStore(dir, bytes.NewReader(nil), fixedClock(testBase))
		_, err := s.Add(t.Context(), &NewArtifact{Name: "a.txt", Data: []byte("a"), By: Person()})
		require.Error(t, err)
		assert.NoDirExists(t, filepath.Join(dir, "art-00000000"))
	})
	t.Run("refuses an invalid name, title, note or body", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		long := strings.Repeat("t", MaxLineRunes+1)
		_, err := s.Add(t.Context(), &NewArtifact{Name: "../x.txt", Data: []byte("a"), By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
		_, err = s.Add(t.Context(), &NewArtifact{Name: "x.txt", Title: long, Data: []byte("a"), By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
		_, err = s.Add(t.Context(), &NewArtifact{Name: "x.txt", Note: "a\nb", Data: []byte("a"), By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
		_, err = s.Add(t.Context(), &NewArtifact{Name: "x.txt", Data: nil, By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses an add past the store quota", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		s.maxStoreBytes = 10
		addText(t, s, "a.txt", "abcdef")
		_, err := s.Add(t.Context(), &NewArtifact{Name: "b.txt", Data: []byte("ghijkl"), By: Person()})
		require.ErrorIs(t, err, ErrFull)
	})
}

func TestStore_Put(t *testing.T) {
	t.Run("appends a revision with its hash and size", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		res, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("two\n"), Note: "more", By: Person()})
		require.NoError(t, err)
		require.Len(t, res.Record.Revisions, 2)
		r2 := res.Record.Revisions[1]
		sum := sha256.Sum256([]byte("two\n"))
		assert.Equal(t, 2, r2.Rev)
		assert.Equal(t, int64(4), r2.Size)
		assert.Equal(t, hex.EncodeToString(sum[:]), r2.SHA256)
		assert.Equal(t, "text/plain; charset=utf-8", res.Record.ContentType)
		assert.Equal(t, 0, res.Anchored)
		assert.Equal(t, 0, res.Detached)
	})
	t.Run("accepts a base revision that is the latest", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("two\n"), BaseRev: 1, By: Person()})
		assert.NoError(t, err)
	})
	t.Run("refuses a stale base revision as a conflict", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("two\n"), By: Person()})
		require.NoError(t, err)
		_, err = s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("three\n"), BaseRev: 1, By: Person()})
		require.ErrorIs(t, err, ErrConflict)
		var conflict *ConflictError
		require.ErrorAs(t, err, &conflict)
		assert.Equal(t, 2, conflict.Latest)
	})
	t.Run("refuses a revision of another kind", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("# two\n"), By: Person()})
		require.NoError(t, err)
		_, err = s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("\x89PNG\r\n\x1a\nbytes"), By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses the revision past the limit", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "r1\n")
		for i := 2; i <= MaxRevisions; i++ {
			_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("r\n"), By: Person()})
			require.NoError(t, err)
		}
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("r\n"), By: Person()})
		require.ErrorIs(t, err, ErrFull)
	})
	t.Run("refuses a revision past the store quota", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		s.maxStoreBytes = 8
		rec := addText(t, s, "notes.txt", "abc\n")
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("defghij\n"), By: Person()})
		require.ErrorIs(t, err, ErrFull)
	})
	t.Run("refuses an unknown or invalid id", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		_, err := s.Put(t.Context(), "art-00000000", &NewRevision{Data: []byte("x"), By: Person()})
		require.ErrorIs(t, err, ErrNotFound)
		_, err = s.Put(t.Context(), "../etc", &NewRevision{Data: []byte("x"), By: Person()})
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("refuses a note that is not one line", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("two\n"), Note: "a\nb", By: Person()})
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestStore_Read(t *testing.T) {
	t.Run("returns a revision's bytes, and the latest by default", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, err := s.Put(t.Context(), rec.ID, &NewRevision{Data: []byte("two\n"), By: Person()})
		require.NoError(t, err)
		_, r1, data1, err := s.Read(rec.ID, 1)
		require.NoError(t, err)
		assert.Equal(t, 1, r1.Rev)
		assert.Equal(t, "one\n", string(data1))
		_, latest, dataLatest, err := s.Read(rec.ID, 0)
		require.NoError(t, err)
		assert.Equal(t, 2, latest.Rev)
		assert.Equal(t, "two\n", string(dataLatest))
	})
	t.Run("refuses a revision that does not exist", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		_, _, _, err := s.Read(rec.ID, 5)
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("refuses an unknown artifact", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		_, _, _, err := s.Read("art-00000000", 0)
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("fails when a revision file is missing", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		require.NoError(t, os.Remove(revisionPath(filepath.Join(dir, rec.ID), 1)))
		_, _, _, err := s.Read(rec.ID, 0)
		require.Error(t, err)
	})
}

func TestStore_List(t *testing.T) {
	t.Run("is empty when the store does not exist yet", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		entries, warnings, err := s.List()
		require.NoError(t, err)
		assert.Empty(t, entries)
		assert.Empty(t, warnings)
	})
	t.Run("orders by latest revision, newest first", func(t *testing.T) {
		t.Parallel()
		clock := &steppingClock{at: testBase}
		s, _ := newTestStore(t, clock.now)
		older := addText(t, s, "a.txt", "a")
		clock.at = testBase.Add(time.Hour)
		newer := addText(t, s, "b.txt", "b")
		entries, warnings, err := s.List()
		require.NoError(t, err)
		assert.Empty(t, warnings)
		require.Len(t, entries, 2)
		assert.Equal(t, newer.ID, entries[0].Record.ID)
		assert.Equal(t, older.ID, entries[1].Record.ID)
	})
	t.Run("skips and names a record it cannot read", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		addText(t, s, "a.txt", "a")
		broken := filepath.Join(dir, "art-0000000a")
		require.NoError(t, os.Mkdir(broken, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(broken, recordFile), []byte(`{"version": 2}`), 0o600))
		entries, warnings, err := s.List()
		require.NoError(t, err)
		assert.Len(t, entries, 1)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "art-0000000a")
	})
}

func TestStore_Show(t *testing.T) {
	t.Run("counts the threads of an artifact", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		threads := []Thread{{ID: "t1"}, {ID: "t2", Resolved: true}}
		require.NoError(t, writeThreads(s.tmpDir(), filepath.Join(dir, rec.ID), threads))
		entry, err := s.Show(rec.ID)
		require.NoError(t, err)
		assert.Equal(t, ThreadCounts{Open: 1, Resolved: 1}, entry.Threads)
		got, err := s.Threads(rec.ID)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
	t.Run("refuses an unknown artifact", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		_, err := s.Show("art-00000000")
		require.ErrorIs(t, err, ErrNotFound)
		_, err = s.Threads("art-00000000")
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_Remove(t *testing.T) {
	t.Run("deletes the artifact and leaves nothing in .tmp", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		require.NoError(t, s.Remove(t.Context(), rec.ID))
		_, err := s.Show(rec.ID)
		require.ErrorIs(t, err, ErrNotFound)
		entries, err := os.ReadDir(filepath.Join(dir, tmpDirName))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("refuses an unknown artifact", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		err := s.Remove(t.Context(), "art-00000000")
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestStore_prepare(t *testing.T) {
	t.Run("removes temporary files older than an hour and keeps recent ones", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, tmpDirName), 0o700))
		stale := filepath.Join(dir, tmpDirName, "stale")
		fresh := filepath.Join(dir, tmpDirName, "fresh")
		require.NoError(t, os.WriteFile(stale, []byte("s"), 0o600))
		require.NoError(t, os.WriteFile(fresh, []byte("f"), 0o600))
		old := testBase.Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(stale, old, old))
		require.NoError(t, os.Chtimes(fresh, testBase, testBase))
		require.NoError(t, s.prepare())
		assert.NoFileExists(t, stale)
		assert.FileExists(t, fresh)
	})
	t.Run("writes owner-only directories and files", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "notes.txt", "one\n")
		dirInfo, err := os.Stat(filepath.Join(dir, rec.ID))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
		fileInfo, err := os.Stat(filepath.Join(dir, rec.ID, recordFile))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
	})
}

// steppingClock returns at each time the test sets it, so records written
// later get later timestamps.
type steppingClock struct {
	at time.Time
}

func (c *steppingClock) now() time.Time {
	return c.at
}
