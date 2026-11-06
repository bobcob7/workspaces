package role

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeUserFile puts content at dir/name, creating dir.
func writeUserFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

const testerFile = "---\ndescription: Tests one thing\npass: [\"PASS\"]\nfail: [\"FAIL\"]\nvars: [\"TASK\"]\n---\n\nTest {{.Vars.TASK}}.\n"

func TestReader_Load(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUserFile(t, dir, "tester.md", testerFile)

	t.Run("loads a user file as user", func(t *testing.T) {
		ro, ok, err := New(dir).Load("tester")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceUser, ro.Source)
		assert.Equal(t, "Test {{.Vars.TASK}}.", ro.Prompt)
	})
	t.Run("reports an absent or invalid name as not found", func(t *testing.T) {
		_, ok, err := New(dir).Load("../tester")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("reports a name whose case differs from the file as not found", func(t *testing.T) {
		_, ok, err := New(dir).Load("Tester")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("treats a missing directory as empty", func(t *testing.T) {
		roles, broken, err := New(filepath.Join(dir, "absent")).List()
		require.NoError(t, err)
		assert.Empty(t, roles)
		assert.Empty(t, broken)
	})
}

func TestSet_Load(t *testing.T) {
	t.Parallel()
	t.Run("a user file with a built-in's name is an override", func(t *testing.T) {
		dir := t.TempDir()
		writeUserFile(t, dir, "reviewer.md", testerFile)
		ro, ok, err := NewSet(dir).Load("reviewer")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceOverride, ro.Source)
		assert.Equal(t, "Tests one thing", ro.Description)
	})
	t.Run("a built-in with no user file is builtin", func(t *testing.T) {
		ro, ok, err := NewSet(t.TempDir()).Load("implementer")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceBuiltin, ro.Source)
	})
	t.Run("an unparsable user file is an error, not the built-in", func(t *testing.T) {
		dir := t.TempDir()
		writeUserFile(t, dir, "verifier.md", "not a role\n")
		_, _, err := NewSet(dir).Load("verifier")
		assert.Error(t, err)
	})
}

func TestSet_List(t *testing.T) {
	t.Parallel()
	t.Run("merges user roles, overrides and built-ins sorted by name", func(t *testing.T) {
		dir := t.TempDir()
		writeUserFile(t, dir, "reviewer.md", testerFile)
		writeUserFile(t, dir, "tester.md", testerFile)
		roles, broken, err := NewSet(dir).List()
		require.NoError(t, err)
		assert.Empty(t, broken)
		var names []string
		sources := map[string]Source{}
		for _, ro := range roles {
			names = append(names, ro.Name)
			sources[ro.Name] = ro.Source
		}
		assert.Equal(t, []string{"implementer", "reviewer", "tester", "verifier"}, names)
		assert.Equal(t, SourceOverride, sources["reviewer"])
		assert.Equal(t, SourceUser, sources["tester"])
		assert.Equal(t, SourceBuiltin, sources["implementer"])
	})
	t.Run("an unparsable user file hides the built-in and is listed as broken", func(t *testing.T) {
		dir := t.TempDir()
		writeUserFile(t, dir, "verifier.md", "not a role\n")
		roles, broken, err := NewSet(dir).List()
		require.NoError(t, err)
		require.Len(t, broken, 1)
		assert.Contains(t, broken[0], "verifier.md")
		for _, ro := range roles {
			assert.NotEqual(t, "verifier", ro.Name)
		}
	})
}
