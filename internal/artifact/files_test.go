package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAtomic(t *testing.T) {
	t.Parallel()
	t.Run("writes a 0600 file and leaves no temporary file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "r1")
		require.NoError(t, writeAtomic(dir, path, []byte("hello")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
	t.Run("replaces an existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "artifact.json")
		require.NoError(t, writeAtomic(dir, path, []byte("old")))
		require.NoError(t, writeAtomic(dir, path, []byte("new")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))
	})
	t.Run("syncs the destination directory after the rename", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "r1")
		var synced []string
		sync := func(d string) error {
			_, err := os.Stat(path)
			require.NoError(t, err, "the rename comes before the sync")
			synced = append(synced, d)
			return nil
		}
		require.NoError(t, writeAtomicSync(dir, path, []byte("x"), sync))
		assert.Equal(t, []string{dir}, synced)
	})
	t.Run("is not a failure when the sync after the rename fails", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "r1")
		sync := func(string) error { return errors.New("sync failed") }
		require.NoError(t, writeAtomicSync(dir, path, []byte("x"), sync))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "x", string(data))
	})
	t.Run("fails when the temporary directory is missing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := writeAtomic(filepath.Join(dir, "missing"), filepath.Join(dir, "r1"), []byte("x"))
		require.Error(t, err)
	})
	t.Run("fails and removes the temporary file when the rename fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "occupied")
		require.NoError(t, os.Mkdir(target, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(target, "keep"), []byte("k"), 0o600))
		err := writeAtomic(dir, target, []byte("x"))
		require.Error(t, err)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
}

func TestReadRecord(t *testing.T) {
	t.Parallel()
	t.Run("round-trips a record", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		want := sampleRecord()
		require.NoError(t, writeRecord(dir, dir, &want))
		got, err := readRecord(dir)
		require.NoError(t, err)
		assert.Equal(t, want.ID, got.ID)
		assert.Equal(t, want.Kind, got.Kind)
		assert.Equal(t, want.Latest().SHA256, got.Latest().SHA256)
	})
	t.Run("reports a missing record as not found", func(t *testing.T) {
		t.Parallel()
		_, err := readRecord(t.TempDir())
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("refuses bad JSON as invalid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, recordFile), []byte("{"), 0o600))
		_, err := readRecord(dir)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a record from a newer ws", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, recordFile), []byte(`{"version": 2}`), 0o600))
		_, err := readRecord(dir)
		require.ErrorIs(t, err, ErrNewer)
	})
	t.Run("refuses a record with no revisions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, recordFile), []byte(`{"version": 1, "revisions": []}`), 0o600))
		_, err := readRecord(dir)
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestReadThreads(t *testing.T) {
	t.Parallel()
	t.Run("is no threads when the file is missing", func(t *testing.T) {
		t.Parallel()
		threads, err := readThreads(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, threads)
	})
	t.Run("round-trips threads", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		want := []Thread{{ID: "t1", Comments: []Comment{{ID: "c1", Body: "nit"}}}}
		require.NoError(t, writeThreads(dir, dir, want))
		got, err := readThreads(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "nit", got[0].Comments[0].Body)
	})
	t.Run("refuses bad JSON as invalid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, threadsFile), []byte("[]"), 0o600))
		_, err := readThreads(dir)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a file from a newer ws", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, threadsFile), []byte(`{"version": 2}`), 0o600))
		_, err := readThreads(dir)
		require.ErrorIs(t, err, ErrNewer)
	})
}

func TestRevisionPath(t *testing.T) {
	t.Run("names the revision file by number", func(t *testing.T) {
		assert.Equal(t, filepath.Join("base", "r7"), revisionPath("base", 7))
	})
}

func sampleRecord() Record {
	now := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	rev := newRevision(1, "text/plain; charset=utf-8", []byte("hi"), "", Person(), now)
	return Record{
		Version:     FormatVersion,
		ID:          "art-00000001",
		Name:        "hi.txt",
		Kind:        KindText,
		ContentType: rev.ContentType,
		CreatedAt:   now,
		CreatedBy:   Person(),
		Revisions:   []Revision{rev},
	}
}
