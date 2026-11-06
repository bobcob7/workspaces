package loop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "loops")
	return NewStore(dir), dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestStoreCreateGetRoundTrip(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	created := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	next := created.Add(24 * time.Hour)
	in := Loop{
		Name:           "deps-bump",
		Every:          "24h",
		Prompt:         "Update the Go and npm dependencies",
		Specs:          []string{"buff-bear", "other"},
		Model:          "opus",
		PermissionMode: "acceptEdits",
		Keep:           3,
		Paused:         true,
		CreatedAt:      created,
		NextRunAt:      next,
		Runs: []Run{{
			StartedAt:    created,
			Workspace:    "loop-deps-bump-1a2b3c4d",
			Session:      "e8cdf5d8",
			ErrorCode:    "",
			ErrorMessage: "",
		}, {
			StartedAt:    next,
			ErrorCode:    "spec_invalid",
			ErrorMessage: "no such repo",
		}},
	}
	require.NoError(t, s.Create(&in))

	got, err := s.Get("deps-bump")
	require.NoError(t, err)
	assert.Equal(t, FormatVersion, got.Version)
	assert.Equal(t, in.Name, got.Name)
	assert.Equal(t, in.Every, got.Every)
	assert.Equal(t, in.Prompt, got.Prompt)
	assert.Equal(t, in.Specs, got.Specs)
	assert.Equal(t, in.Model, got.Model)
	assert.Equal(t, in.PermissionMode, got.PermissionMode)
	assert.Equal(t, in.Keep, got.Keep)
	assert.Equal(t, in.Paused, got.Paused)
	assert.True(t, in.CreatedAt.Equal(got.CreatedAt), "created_at %s", got.CreatedAt)
	assert.True(t, in.NextRunAt.Equal(got.NextRunAt), "next_run_at %s", got.NextRunAt)
	require.Len(t, got.Runs, len(in.Runs))
	for i := range in.Runs {
		assert.True(t, in.Runs[i].StartedAt.Equal(got.Runs[i].StartedAt))
		assert.Equal(t, in.Runs[i].Workspace, got.Runs[i].Workspace)
		assert.Equal(t, in.Runs[i].Session, got.Runs[i].Session)
		assert.Equal(t, in.Runs[i].ErrorCode, got.Runs[i].ErrorCode)
		assert.Equal(t, in.Runs[i].ErrorMessage, got.Runs[i].ErrorMessage)
	}
}

func TestStoreCreateTwiceIsExists(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	l := validLoop()
	require.NoError(t, s.Create(&l))
	err := s.Create(&l)
	assert.ErrorIs(t, err, ErrExists)
}

func TestStoreMissingLoopIsNotFound(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	_, err := s.Get("nope")
	require.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, s.Remove("nope"), ErrNotFound)
}

func TestStoreRejectsBadNames(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	_, err := s.Get("../escape")
	require.ErrorIs(t, err, ErrInvalid)
	bad := validLoop()
	bad.Name = "a/b"
	require.ErrorIs(t, s.Create(&bad), ErrInvalid)
	require.ErrorIs(t, s.Save(&bad), ErrInvalid)
	assert.ErrorIs(t, s.Remove("a/b"), ErrInvalid)
}

func TestStoreRemove(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	l := validLoop()
	require.NoError(t, s.Create(&l))
	require.NoError(t, s.Remove(l.Name))
	assert.NoFileExists(t, filepath.Join(dir, l.Name+".toml"))
	_, err := s.Get(l.Name)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStoreSaveOverwritesAndLeavesNoTemp(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	l := validLoop()
	require.NoError(t, s.Create(&l))
	l.Paused = true
	require.NoError(t, s.Save(&l))

	got, err := s.Get(l.Name)
	require.NoError(t, err)
	assert.True(t, got.Paused)
	assert.NoFileExists(t, filepath.Join(dir, l.Name+".toml.tmp"))
}

func TestStoreSaveWritesTheRecordFile(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	l := validLoop()
	require.NoError(t, s.Save(&l))
	assert.FileExists(t, filepath.Join(dir, "deps-bump.toml"))
	assert.Equal(t, FormatVersion, l.Version)
}

func TestStoreListMissingDirIsEmpty(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	loops, warnings, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, loops)
	assert.Empty(t, warnings)
}

func TestStoreListSortsAndSkipsBadFiles(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	for _, name := range []string{"zeta", "a-b", "a"} {
		l := validLoop()
		l.Name = name
		require.NoError(t, s.Create(&l))
	}
	writeFile(t, dir, "bad.toml", "this is = = not toml")
	writeFile(t, dir, "future.toml", "version = 3\nname = \"future\"\n")
	writeFile(t, dir, ".lock", "")
	writeFile(t, dir, ".hidden.toml", "version = 1\n")
	writeFile(t, dir, "x.toml.tmp", "version = 1\n")

	loops, warnings, err := s.List()
	require.NoError(t, err)
	names := make([]string, 0, len(loops))
	for _, l := range loops {
		names = append(names, l.Name)
	}
	assert.Equal(t, []string{"a", "a-b", "zeta"}, names)

	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], "bad.toml")
	assert.Contains(t, warnings[1], "future.toml")
}

func TestStoreReadRefusesNewerVersionWithoutRewriting(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	writeFile(t, dir, "future.toml", "version = 3\nname = \"future\"\n")
	_, err := s.Get("future")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer ws")
	data, err := os.ReadFile(filepath.Join(dir, "future.toml"))
	require.NoError(t, err)
	assert.Equal(t, "version = 3\nname = \"future\"\n", string(data))
}

func TestStoreVersion2RoundTrip(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	created := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	in := validLoop()
	in.Script = "gh issue list --json number"
	in.ScriptTimeout = "2m"
	in.Vars = map[string]string{"REPO": "buff-bear", "EMPTY": ""}
	in.LastGate = &Gate{
		At:       created,
		Outcome:  GateFailed,
		ExitCode: -1,
		Message:  "timed out after 2m0s",
	}
	require.NoError(t, s.Create(&in))

	got, err := s.Get(in.Name)
	require.NoError(t, err)
	assert.Equal(t, FormatVersion, got.Version)
	assert.Equal(t, in.Script, got.Script)
	assert.Equal(t, in.ScriptTimeout, got.ScriptTimeout)
	assert.Equal(t, in.Vars, got.Vars)
	require.NotNil(t, got.LastGate)
	assert.True(t, created.Equal(got.LastGate.At))
	assert.Equal(t, GateFailed, got.LastGate.Outcome)
	assert.Equal(t, -1, got.LastGate.ExitCode)
	assert.Equal(t, "timed out after 2m0s", got.LastGate.Message)
}

func TestStoreVersion1SaveKeepsVersion(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	l := validLoop()
	l.Version = Version1
	require.NoError(t, s.Save(&l))

	got, err := s.Get(l.Name)
	require.NoError(t, err)
	assert.Equal(t, Version1, got.Version)
}

func TestStoreOmitsEmptyVersion2Keys(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	l := validLoop()
	require.NoError(t, s.Create(&l))

	data, err := os.ReadFile(filepath.Join(dir, l.Name+".toml"))
	require.NoError(t, err)
	content := string(data)
	assert.NotContains(t, content, "script")
	assert.NotContains(t, content, "[vars]")
	assert.NotContains(t, content, "[last_gate]")
}

func TestStoreListSkipsVersion1WithVersion2Keys(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	writeFile(t, dir, "old.toml", "version = 1\nname = \"old\"\nevery = \"24h\"\nprompt = \"x\"\nkeep = 5\nscript = \"true\"\n")

	loops, warnings, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, loops)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "old.toml")
	assert.Contains(t, warnings[0], "version 1 record has version 2 keys")
}

func TestStoreLockCreatesDirAndUnlocks(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t)
	unlock, err := s.Lock(t.Context())
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, ".lock"))
	unlock()

	shared, err := s.RLock(t.Context())
	require.NoError(t, err)
	shared()
}
