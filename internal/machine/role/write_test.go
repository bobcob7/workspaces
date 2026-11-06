package role

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userFiles lists the files in dir other than the lock, so a test can see
// that no temp file was left behind.
func userFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if e.Name() != lockName {
			files = append(files, e.Name())
		}
	}
	return files
}

func TestWriter_Add(t *testing.T) {
	t.Parallel()
	t.Run("adds a new role that loads as user", func(t *testing.T) {
		dir := t.TempDir()
		w := NewWriter(dir)
		got, err := w.Add(t.Context(), validRole())
		require.NoError(t, err)
		assert.Equal(t, SourceUser, got.Source)
		ro, ok, err := NewSet(dir).Load("tester")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceUser, ro.Source)
		assert.Equal(t, "Test {{.Vars.TASK}}.", ro.Prompt)
		assert.Equal(t, []string{"tester.md"}, userFiles(t, dir))
	})
	t.Run("refuses a built-in's name", func(t *testing.T) {
		w := NewWriter(t.TempDir())
		ro := validRole()
		ro.Name = "implementer"
		_, err := w.Add(t.Context(), ro)
		require.ErrorIs(t, err, ErrExists)
	})
	t.Run("refuses a name that is already a user's", func(t *testing.T) {
		dir := t.TempDir()
		w := NewWriter(dir)
		_, err := w.Add(t.Context(), validRole())
		require.NoError(t, err)
		_, err = w.Add(t.Context(), validRole())
		require.ErrorIs(t, err, ErrExists)
	})
	t.Run("refuses an invalid role and writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		ro := validRole()
		ro.Name = "Bad"
		_, err := NewWriter(dir).Add(t.Context(), ro)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Empty(t, userFiles(t, dir))
	})
}

func TestWriter_Edit(t *testing.T) {
	t.Parallel()
	t.Run("copies a built-in to an override with the change", func(t *testing.T) {
		dir := t.TempDir()
		w := NewWriter(dir)
		model := "opus"
		got, err := w.Edit(t.Context(), "reviewer", &Changes{Model: &model})
		require.NoError(t, err)
		assert.Equal(t, SourceOverride, got.Source)
		assert.Equal(t, "opus", got.Model)
		builtin := builtinRole(t, "reviewer")
		assert.Equal(t, builtin.Prompt, got.Prompt)
		ro, _, err := NewSet(dir).Load("reviewer")
		require.NoError(t, err)
		assert.Equal(t, "opus", ro.Model)
		assert.Equal(t, SourceOverride, ro.Source)
	})
	t.Run("leaves the file unchanged when the edit is invalid", func(t *testing.T) {
		dir := t.TempDir()
		w := NewWriter(dir)
		_, err := w.Add(t.Context(), validRole())
		require.NoError(t, err)
		before, err := os.ReadFile(w.Path("tester"))
		require.NoError(t, err)
		empty := ""
		_, err = w.Edit(t.Context(), "tester", &Changes{Description: &empty})
		require.ErrorIs(t, err, ErrInvalid)
		after, err := os.ReadFile(w.Path("tester"))
		require.NoError(t, err)
		assert.Equal(t, before, after)
		assert.Equal(t, []string{"tester.md"}, userFiles(t, dir))
	})
	t.Run("refuses an unknown name", func(t *testing.T) {
		model := "opus"
		_, err := NewWriter(t.TempDir()).Edit(t.Context(), "nope", &Changes{Model: &model})
		require.ErrorIs(t, err, ErrUnknown)
	})
	t.Run("refuses an unparsable user file", func(t *testing.T) {
		dir := t.TempDir()
		writeUserFile(t, dir, "tester.md", "not a role\n")
		model := "opus"
		_, err := NewWriter(dir).Edit(t.Context(), "tester", &Changes{Model: &model})
		require.ErrorIs(t, err, ErrBroken)
	})
}

func TestWriter_Remove(t *testing.T) {
	t.Parallel()
	t.Run("removing an override restores the built-in", func(t *testing.T) {
		dir := t.TempDir()
		w := NewWriter(dir)
		model := "opus"
		_, err := w.Edit(t.Context(), "reviewer", &Changes{Model: &model})
		require.NoError(t, err)
		source, err := w.Remove(t.Context(), "reviewer")
		require.NoError(t, err)
		assert.Equal(t, SourceOverride, source)
		ro, ok, err := NewSet(dir).Load("reviewer")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceBuiltin, ro.Source)
		assert.Equal(t, "sonnet", ro.Model)
	})
	t.Run("removing a user role returns user", func(t *testing.T) {
		dir := t.TempDir()
		w := NewWriter(dir)
		_, err := w.Add(t.Context(), validRole())
		require.NoError(t, err)
		source, err := w.Remove(t.Context(), "tester")
		require.NoError(t, err)
		assert.Equal(t, SourceUser, source)
		assert.Empty(t, userFiles(t, dir))
	})
	t.Run("refuses a built-in without an override", func(t *testing.T) {
		_, err := NewWriter(t.TempDir()).Remove(t.Context(), "implementer")
		require.ErrorIs(t, err, ErrBuiltin)
	})
	t.Run("refuses an unknown name", func(t *testing.T) {
		_, err := NewWriter(t.TempDir()).Remove(t.Context(), "nope")
		require.ErrorIs(t, err, ErrUnknown)
	})
}
