package loop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeGoldenLoop() Loop {
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	return Loop{Name: "deps-bump", Every: "24h", Prompt: "bump deps", Keep: DefaultKeep, CreatedAt: now, NextRunAt: now.Add(24 * time.Hour)}
}

func readRecord(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name+".toml"))
	require.NoError(t, err)
	return string(data)
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(data)
}

func TestStore_ClaudeLoopBytes(t *testing.T) {
	t.Parallel()
	t.Run("a created claude loop is written as before runners", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t)
		l := claudeGoldenLoop()
		require.NoError(t, s.Create(&l))
		assert.Equal(t, Version3, l.Version)
		assert.Equal(t, readGolden(t, "claude-created.toml"), readRecord(t, dir, l.Name))
	})
	t.Run("an edited claude loop stays version 3 with the same bytes", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t)
		l := claudeGoldenLoop()
		require.NoError(t, s.Create(&l))
		require.NoError(t, l.Apply(&Edit{Prompt: new("bump more"), Hidden: new(false)}, l.CreatedAt))
		require.NoError(t, s.Save(&l))
		assert.Equal(t, readGolden(t, "claude-edited.toml"), readRecord(t, dir, l.Name))
	})
	t.Run("an explicit claude runner edit keeps version 3", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t)
		l := claudeGoldenLoop()
		require.NoError(t, s.Create(&l))
		require.NoError(t, l.Apply(&Edit{Runner: new(RunnerClaude), Endpoint: new("")}, l.CreatedAt))
		require.NoError(t, s.Save(&l))
		assert.Equal(t, Version3, l.Version)
	})
}

func TestStore_WSLoopVersion(t *testing.T) {
	t.Parallel()
	t.Run("a ws loop is version 4", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t)
		l := claudeGoldenLoop()
		l.Runner = RunnerWS
		require.NoError(t, s.Create(&l))
		assert.Contains(t, readRecord(t, dir, l.Name), "version = 4\n")
	})
	t.Run("setting a runner on a stored claude loop makes it version 4", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t)
		l := claudeGoldenLoop()
		require.NoError(t, s.Create(&l))
		require.NoError(t, l.Apply(&Edit{Runner: new(RunnerWS)}, l.CreatedAt))
		require.NoError(t, s.Save(&l))
		assert.Contains(t, readRecord(t, dir, l.Name), "version = 4\n")
	})
	t.Run("a hidden edit keeps a version 4 record at version 4", func(t *testing.T) {
		t.Parallel()
		l := claudeGoldenLoop()
		l.Version, l.Runner = FormatVersion, RunnerWS
		require.NoError(t, l.Apply(&Edit{Hidden: new(false)}, l.CreatedAt))
		assert.Equal(t, FormatVersion, l.Version)
	})
	t.Run("this ws still reads a version 4 record and refuses a newer one", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t)
		writeFile(t, dir, "new.toml", "version = 4\nname = \"new\"\nevery = \"1h\"\nprompt = \"hi\"\nkeep = 5\nrunner = \"ws\"\n")
		got, err := s.Get("new")
		require.NoError(t, err)
		assert.Equal(t, RunnerWS, got.Runner)
		writeFile(t, dir, "future.toml", "version = 5\nname = \"future\"\n")
		_, err = s.Get("future")
		require.ErrorContains(t, err, "newer ws")
	})
}
