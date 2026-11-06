package machine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTime() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

func TestStoreWriteResultRead(t *testing.T) {
	t.Parallel()

	t.Run("round-trips the verdict, ancestors, time and report", func(t *testing.T) {
		t.Parallel()
		store := NewStore(t.TempDir())
		when := testTime()
		report := []byte("all green")
		err := store.WriteResult("boss", "a1", &Result{
			Verdict:    "PASS",
			Ancestors:  []string{"a", "plan", "boss"},
			RecordedAt: when,
		}, report)
		require.NoError(t, err)

		rec, err := store.Read("boss", "a1")
		require.NoError(t, err)
		require.NotNil(t, rec.Result)
		assert.Equal(t, "PASS", rec.Result.Verdict)
		assert.Equal(t, []string{"a", "plan", "boss"}, rec.Result.Ancestors)
		assert.True(t, rec.Result.RecordedAt.Equal(when))
		assert.True(t, rec.Result.Report)
		assert.Equal(t, FormatVersion, rec.Result.Version)

		require.NotEmpty(t, rec.ReportPath)
		assert.True(t, filepath.IsAbs(rec.ReportPath))
		got, err := os.ReadFile(rec.ReportPath)
		require.NoError(t, err)
		assert.Equal(t, report, got)
	})

	t.Run("a second write without a report deletes it", func(t *testing.T) {
		t.Parallel()
		store := NewStore(t.TempDir())
		require.NoError(t, store.WriteResult("boss", "a1", &Result{Verdict: "FAIL"}, []byte("first")))
		require.NoError(t, store.WriteResult("boss", "a1", &Result{Verdict: "PASS"}, nil))

		rec, err := store.Read("boss", "a1")
		require.NoError(t, err)
		assert.Equal(t, "PASS", rec.Result.Verdict)
		assert.False(t, rec.Result.Report)
		assert.Empty(t, rec.ReportPath)
		_, err = os.Stat(filepath.Join(store.Dir("boss", "a1"), "report.md"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("a result without a report is not an error", func(t *testing.T) {
		t.Parallel()
		store := NewStore(t.TempDir())
		require.NoError(t, store.WriteResult("boss", "a1", &Result{Verdict: "PASS"}, nil))
	})
}

func TestStoreWriteSpawnRead(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())
	when := testTime()
	sp := &Spawn{
		Role:      "implementer",
		Model:     "claude-haiku-5-5",
		Pass:      []string{"PASS", "FLAKY"},
		Fail:      []string{"FAIL"},
		Vars:      map[string]string{"task": "T1"},
		Ancestors: []string{"plan", "boss"},
		SpawnedAt: when,
	}
	require.NoError(t, store.WriteSpawn("boss", "a1", sp))
	assert.Equal(t, FormatVersion, sp.Version)

	rec, err := store.Read("boss", "a1")
	require.NoError(t, err)
	require.NotNil(t, rec.Spawn)
	assert.Equal(t, "implementer", rec.Spawn.Role)
	assert.Equal(t, "claude-haiku-5-5", rec.Spawn.Model)
	assert.Equal(t, []string{"PASS", "FLAKY"}, rec.Spawn.Pass)
	assert.Equal(t, []string{"FAIL"}, rec.Spawn.Fail)
	assert.Equal(t, map[string]string{"task": "T1"}, rec.Spawn.Vars)
	assert.Equal(t, []string{"plan", "boss"}, rec.Spawn.Ancestors)
	assert.True(t, rec.Spawn.SpawnedAt.Equal(when))
	assert.Equal(t, FormatVersion, rec.Spawn.Version)
	assert.Nil(t, rec.Result)
	assert.Equal(t, "implementer", rec.Role())
}

func TestStoreRead(t *testing.T) {
	t.Parallel()

	t.Run("a missing directory is an empty record", func(t *testing.T) {
		t.Parallel()
		rec, err := NewStore(t.TempDir()).Read("boss", "x")
		require.NoError(t, err)
		assert.Equal(t, Record{Child: "x"}, rec)
		assert.False(t, rec.Found())
	})

	t.Run("a newer version is ErrNewer in either file", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{"spawn.toml", "result.toml"} {
			store := NewStore(t.TempDir())
			dir := store.Dir("boss", "a1")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("version = 2\n"), 0o600))

			_, err := store.Read("boss", "a1")
			assert.ErrorIs(t, err, ErrNewer, name)
		}
	})

	t.Run("a file that doesn't parse is ErrInvalid", func(t *testing.T) {
		t.Parallel()
		store := NewStore(t.TempDir())
		dir := store.Dir("boss", "a1")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "result.toml"), []byte("verdict = [\n"), 0o600))

		_, err := store.Read("boss", "a1")
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestStoreInvalidComponents(t *testing.T) {
	t.Parallel()

	bad := []string{"", ".", "..", ".x", "../x", "a/b"}
	for _, child := range bad {
		t.Run("rejects child "+child, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			store := NewStore(root)

			_, err := store.Read("boss", child)
			require.ErrorIs(t, err, ErrInvalid)
			require.ErrorIs(t, store.WriteSpawn("boss", child, &Spawn{}), ErrInvalid)
			require.ErrorIs(t, store.WriteResult("boss", child, &Result{}, nil), ErrInvalid)

			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing is created")
		})
	}

	t.Run("rejects a bad root", func(t *testing.T) {
		t.Parallel()
		store := NewStore(t.TempDir())
		assert.ErrorIs(t, store.WriteResult("../boss", "a1", &Result{}, nil), ErrInvalid)
	})
}

func TestStoreFileModes(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())
	require.NoError(t, store.WriteSpawn("boss", "a1", &Spawn{Role: "r"}))
	require.NoError(t, store.WriteResult("boss", "a1", &Result{Verdict: "PASS"}, []byte("report")))

	for _, name := range []string{"spawn.toml", "result.toml", "report.md"} {
		info, err := os.Stat(filepath.Join(store.Dir("boss", "a1"), name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), name)
	}
	info, err := os.Stat(store.Dir("boss", "a1"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm()&0o700)
}
