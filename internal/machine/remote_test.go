package machine

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEnvID = "env-1a2b3c4d"

func TestStore_RemoteDir(t *testing.T) {
	stateDir := t.TempDir()
	s := NewStore(stateDir)
	t.Run("is under the root's remote directory, by environment and child", func(t *testing.T) {
		dir, err := s.RemoteDir("boss", testEnvID, "helper")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(stateDir, "boss", "machine", "remote", testEnvID, "helper"), dir)
	})
	t.Run("refuses a parent-directory child", func(t *testing.T) {
		_, err := s.RemoteDir("boss", testEnvID, "..")
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a child with a path separator", func(t *testing.T) {
		_, err := s.RemoteDir("boss", testEnvID, "a/b")
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a root with a path separator", func(t *testing.T) {
		_, err := s.RemoteDir("..", testEnvID, "helper")
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses an environment id that isn't valid", func(t *testing.T) {
		_, err := s.RemoteDir("boss", "../evil", "helper")
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestStore_WriteRemote(t *testing.T) {
	t.Run("writes result, report and spawn, readable with ReadRemote", func(t *testing.T) {
		s := NewStore(t.TempDir())
		sp := &Spawn{Role: "implementer", Pass: []string{"PASS"}, Fail: []string{"FAIL"}}
		r := &Result{Verdict: "PASS", RecordedAt: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", sp, r, []byte("the report")))
		rec, err := s.ReadRemote("boss", testEnvID, "helper")
		require.NoError(t, err)
		require.NotNil(t, rec.Result)
		assert.Equal(t, "PASS", rec.Result.Verdict)
		assert.Equal(t, "implementer", rec.Role())
		require.NotEmpty(t, rec.ReportPath)
		report, err := os.ReadFile(rec.ReportPath)
		require.NoError(t, err)
		assert.Equal(t, "the report", string(report))
	})
	t.Run("keeps the report file at mode 0600", func(t *testing.T) {
		s := NewStore(t.TempDir())
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "PASS"}, []byte("r")))
		rec, err := s.ReadRemote("boss", testEnvID, "helper")
		require.NoError(t, err)
		info, err := os.Stat(rec.ReportPath)
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("a later result replaces the copy", func(t *testing.T) {
		s := NewStore(t.TempDir())
		t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "FAIL", RecordedAt: t0}, []byte("first")))
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "PASS", RecordedAt: t0.Add(time.Minute)}, []byte("second")))
		rec, err := s.ReadRemote("boss", testEnvID, "helper")
		require.NoError(t, err)
		require.NotNil(t, rec.Result)
		assert.Equal(t, "PASS", rec.Result.Verdict)
		report, err := os.ReadFile(rec.ReportPath)
		require.NoError(t, err)
		assert.Equal(t, "second", string(report))
	})
	t.Run("an older or equal result keeps the copy", func(t *testing.T) {
		s := NewStore(t.TempDir())
		t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "PASS", RecordedAt: t0}, []byte("first")))
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "FAIL", RecordedAt: t0}, []byte("second")))
		rec, err := s.ReadRemote("boss", testEnvID, "helper")
		require.NoError(t, err)
		require.NotNil(t, rec.Result)
		assert.Equal(t, "PASS", rec.Result.Verdict)
		report, err := os.ReadFile(rec.ReportPath)
		require.NoError(t, err)
		assert.Equal(t, "first", string(report))
	})
	t.Run("writes nothing more once a result is there", func(t *testing.T) {
		s := NewStore(t.TempDir())
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "PASS"}, []byte("first")))
		require.NoError(t, s.WriteRemote("boss", testEnvID, "helper", nil, &Result{Verdict: "FAIL"}, []byte("second")))
		rec, err := s.ReadRemote("boss", testEnvID, "helper")
		require.NoError(t, err)
		assert.Equal(t, "PASS", rec.Result.Verdict)
		report, err := os.ReadFile(rec.ReportPath)
		require.NoError(t, err)
		assert.Equal(t, "first", string(report))
	})
}
