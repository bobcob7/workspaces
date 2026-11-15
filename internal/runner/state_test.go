package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFiles_State(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour)
	live := func(pid int) (time.Time, error) {
		if pid == 51234 {
			return started, nil
		}
		return time.Time{}, errors.New("no such process")
	}
	t.Run("no status recorded 3 s ago is starting", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		state, pid, st := files.State(now, now.Add(-3*time.Second), &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "starting", state)
		assert.Zero(t, pid)
		assert.Nil(t, st)
	})
	t.Run("no status recorded 20 s ago is stopped", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		state, _, _ := files.State(now, now.Add(-20*time.Second), &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "stopped", state)
	})
	t.Run("a live pid with a matching start time is working", func(t *testing.T) {
		files := writeStatusFile(t, &Status{PID: 51234, ProcessStartedAt: started, State: "working", UpdatedAt: now})
		state, pid, st := files.State(now, now, &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "working", state)
		assert.Equal(t, 51234, pid)
		require.NotNil(t, st)
	})
	t.Run("a live pid with another start time is stopped", func(t *testing.T) {
		files := writeStatusFile(t, &Status{PID: 51234, ProcessStartedAt: started.Add(time.Minute), UpdatedAt: now})
		state, pid, _ := files.State(now, now, &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "stopped", state)
		assert.Zero(t, pid)
	})
	t.Run("an exit makes the run idle", func(t *testing.T) {
		exit := "done"
		files := writeStatusFile(t, &Status{PID: 51234, ProcessStartedAt: started, Exit: &exit, UpdatedAt: now})
		state, pid, st := files.State(now, now, &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "idle", state)
		assert.Zero(t, pid)
		assert.NotNil(t, st)
	})
	t.Run("a dead pid after a restart within 3 min is starting", func(t *testing.T) {
		files := writeStatusFile(t, &Status{PID: 999, Restarts: 1, UpdatedAt: now.Add(-time.Minute)})
		state, _, _ := files.State(now, now, &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "starting", state)
	})
	t.Run("a dead pid without a recent restart is stopped", func(t *testing.T) {
		files := writeStatusFile(t, &Status{PID: 999, Restarts: 1, UpdatedAt: now.Add(-10 * time.Minute)})
		state, _, _ := files.State(now, now, &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "stopped", state)
	})
	t.Run("invalid status JSON is unknown", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		require.NoError(t, os.MkdirAll(files.Dir, dirMode))
		require.NoError(t, os.WriteFile(filepath.Join(files.Dir, statusFile), []byte("{"), fileMode))
		state, _, st := files.State(now, now, &processReaderMock{StartTimeFunc: live})
		assert.Equal(t, "unknown", state)
		assert.Nil(t, st)
	})
}

// writeStatusFile writes st as the run's status.json and returns its files.
func writeStatusFile(t *testing.T, st *Status) Files {
	t.Helper()
	files := FilesFor(t.TempDir(), "ws-1")
	require.NoError(t, files.WriteStatus(st))
	return files
}
