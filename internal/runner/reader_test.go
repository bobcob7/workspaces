package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readerFixture is a Reader over a temporary state directory, whose clock is
// now and whose processes all have the start time started.
func readerFixture(t *testing.T, now, started time.Time) Reader {
	t.Helper()
	procs := &processReaderMock{StartTimeFunc: func(int) (time.Time, error) { return started, nil }}
	r := NewReader(t.TempDir(), procs)
	r.now = func() time.Time { return now }
	return r
}

func TestReader_Exists(t *testing.T) {
	t.Parallel()
	t.Run("true once the runner directory exists", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, time.Now(), time.Now())
		require.NoError(t, os.MkdirAll(FilesFor(r.stateDir, "x").Dir, dirMode))
		assert.True(t, r.Exists("x"))
	})
	t.Run("false without one", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, time.Now(), time.Now())
		assert.False(t, r.Exists("x"))
	})
}

func TestReader_State(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	t.Run("starting before any status.json, within the grace period", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, now, now)
		state, pid, st, run := r.State("x", now.Add(-time.Second))
		assert.Equal(t, "starting", state)
		assert.Zero(t, pid)
		assert.Nil(t, st)
		assert.Nil(t, run)
	})
	t.Run("stopped with no status.json after the grace period", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, now, now)
		state, _, _, _ := r.State("x", now.Add(-time.Minute))
		assert.Equal(t, "stopped", state)
	})
	t.Run("working while the recorded process is alive, with the run", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, now, now)
		f := FilesFor(r.stateDir, "x")
		require.NoError(t, os.MkdirAll(f.Dir, dirMode))
		require.NoError(t, f.WriteRun(&Run{Model: "qwen/qwen3.5-9b", Endpoint: "fake"}))
		require.NoError(t, f.WriteStatus(&Status{PID: 42, ProcessStartedAt: now, State: "working", Model: "qwen3.5:9b", Endpoint: "fake-b", UpdatedAt: now}))
		state, pid, st, run := r.State("x", now.Add(-time.Hour))
		assert.Equal(t, "working", state)
		assert.Equal(t, 42, pid)
		require.NotNil(t, st)
		assert.Equal(t, "fake-b", st.Endpoint)
		require.NotNil(t, run)
		assert.Equal(t, "fake", run.Endpoint)
	})
	t.Run("idle once status.json records an exit", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, now, now)
		f := FilesFor(r.stateDir, "x")
		require.NoError(t, os.MkdirAll(f.Dir, dirMode))
		exit := "done"
		require.NoError(t, f.WriteStatus(&Status{PID: 42, Exit: &exit, UpdatedAt: now}))
		state, pid, st, _ := r.State("x", now.Add(-time.Hour))
		assert.Equal(t, "idle", state)
		assert.Zero(t, pid)
		require.NotNil(t, st)
	})
	t.Run("unknown when status.json can't be read", func(t *testing.T) {
		t.Parallel()
		r := readerFixture(t, now, now)
		f := FilesFor(r.stateDir, "x")
		require.NoError(t, os.MkdirAll(f.Dir, dirMode))
		require.NoError(t, os.WriteFile(filepath.Join(f.Dir, statusFile), []byte("{"), fileMode))
		state, _, st, _ := r.State("x", now.Add(-time.Hour))
		assert.Equal(t, "unknown", state)
		assert.Nil(t, st)
	})
}
