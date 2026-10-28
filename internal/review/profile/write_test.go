package profile

import (
	"os"
	"path/filepath"
	"strings"
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
	docs := Profile{Name: "docs", Description: "Docs: accuracy", Match: []string{"*.md", "docs/<x>.txt"}, Body: "Read the docs.\n"}
	t.Run("writes a profile the reader loads back", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "profiles")
		w := NewWriter(dir)
		p, err := w.Add(t.Context(), docs)
		require.NoError(t, err)
		assert.Equal(t, SourceUser, p.Source)
		assert.Equal(t, WhenAuto, p.When)
		loaded, ok, err := NewSet(dir).Load("docs")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "Docs: accuracy", loaded.Description)
		assert.Equal(t, []string{"*.md", "docs/<x>.txt"}, loaded.Match)
		assert.Equal(t, "Read the docs.", loaded.Body)
		assert.Equal(t, []string{"docs.md"}, userFiles(t, dir))
	})
	t.Run("writes when when it's given", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := docs
		p.When = WhenOff
		_, err := NewWriter(dir).Add(t.Context(), p)
		require.NoError(t, err)
		loaded, _, err := New(dir).Load("docs")
		require.NoError(t, err)
		assert.Equal(t, WhenOff, loaded.When)
	})
	t.Run("refuses an invalid name", func(t *testing.T) {
		t.Parallel()
		p := docs
		p.Name = "../Docs"
		_, err := NewWriter(t.TempDir()).Add(t.Context(), p)
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses the reserved name", func(t *testing.T) {
		t.Parallel()
		p := docs
		p.Name = "decisions"
		_, err := NewWriter(t.TempDir()).Add(t.Context(), p)
		require.ErrorIs(t, err, ErrInvalid)
		assert.ErrorContains(t, err, "reserved")
	})
	t.Run("refuses an empty guide", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := docs
		p.Body = " \n\n"
		_, err := NewWriter(dir).Add(t.Context(), p)
		require.ErrorIs(t, err, ErrInvalid)
		require.ErrorContains(t, err, "guide is empty")
		assert.Empty(t, userFiles(t, dir))
	})
	t.Run("refuses a write that wouldn't parse, leaving nothing behind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := docs
		p.Description = strings.Repeat("x", maxDescriptionRunes+1)
		_, err := NewWriter(dir).Add(t.Context(), p)
		require.ErrorIs(t, err, ErrInvalid)
		require.ErrorContains(t, err, "over the 120 limit")
		assert.Empty(t, userFiles(t, dir))
	})
	t.Run("refuses a multi-line description", func(t *testing.T) {
		t.Parallel()
		p := docs
		p.Description = "one\nwhen: off"
		_, err := NewWriter(t.TempDir()).Add(t.Context(), p)
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a built-in's name", func(t *testing.T) {
		t.Parallel()
		p := docs
		p.Name = "go"
		_, err := NewWriter(t.TempDir()).Add(t.Context(), p)
		assert.ErrorIs(t, err, ErrExists)
	})
	t.Run("refuses a name the user has, even broken", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "docs.md", "broken\n")
		_, err := NewWriter(dir).Add(t.Context(), docs)
		require.ErrorIs(t, err, ErrExists)
		raw, err := os.ReadFile(filepath.Join(dir, "docs.md"))
		require.NoError(t, err)
		assert.Equal(t, "broken\n", string(raw))
	})
}

func TestWriter_Edit(t *testing.T) {
	t.Parallel()
	ptr := func(s string) *string { return &s }
	t.Run("editing a built-in writes a full override", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		builtin, _, err := Builtin().Load("go")
		require.NoError(t, err)
		p, err := NewWriter(dir).Edit(t.Context(), "go", &Changes{Description: ptr("Our Go")})
		require.NoError(t, err)
		assert.Equal(t, SourceOverride, p.Source)
		loaded, ok, err := NewSet(dir).Load("go")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceOverride, loaded.Source)
		assert.False(t, loaded.Builtin)
		assert.Equal(t, "Our Go", loaded.Description)
		assert.Equal(t, builtin.When, loaded.When)
		assert.Equal(t, builtin.Match, loaded.Match)
		assert.Equal(t, builtin.Body, loaded.Body)
	})
	t.Run("changes only the fields given", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "docs.md", "---\ndescription: Docs\nmatch: [\"*.md\"]\n---\nRead.\n")
		when := WhenAlways
		match := []string{}
		p, err := NewWriter(dir).Edit(t.Context(), "docs", &Changes{When: &when, Match: &match, Body: ptr("Read closely.")})
		require.NoError(t, err)
		assert.Equal(t, SourceUser, p.Source)
		loaded, _, err := New(dir).Load("docs")
		require.NoError(t, err)
		assert.Equal(t, Profile{Name: "docs", Description: "Docs", When: WhenAlways, Body: "Read closely.", Source: SourceUser}, loaded)
	})
	t.Run("refuses an edit that wouldn't parse, keeping the file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		original := "---\ndescription: Docs\nmatch: [\"*.md\"]\n---\nRead.\n"
		writeProfile(t, dir, "docs.md", original)
		match := []string{}
		_, err := NewWriter(dir).Edit(t.Context(), "docs", &Changes{Match: &match})
		require.ErrorIs(t, err, ErrInvalid)
		require.ErrorContains(t, err, "auto needs match patterns")
		raw, err := os.ReadFile(filepath.Join(dir, "docs.md"))
		require.NoError(t, err)
		assert.Equal(t, original, string(raw))
		assert.Equal(t, []string{"docs.md"}, userFiles(t, dir))
	})
	t.Run("refuses an empty guide", func(t *testing.T) {
		t.Parallel()
		_, err := NewWriter(t.TempDir()).Edit(t.Context(), "go", &Changes{Body: ptr("")})
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses an unknown name", func(t *testing.T) {
		t.Parallel()
		_, err := NewWriter(t.TempDir()).Edit(t.Context(), "cobol", &Changes{})
		assert.ErrorIs(t, err, ErrUnknown)
	})
	t.Run("refuses the reserved name", func(t *testing.T) {
		t.Parallel()
		_, err := NewWriter(t.TempDir()).Edit(t.Context(), "decisions", &Changes{})
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("a broken file is an error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "docs.md", "broken\n")
		_, err := NewWriter(dir).Edit(t.Context(), "docs", &Changes{})
		assert.ErrorIs(t, err, ErrBroken)
	})
}

func TestWriter_Remove(t *testing.T) {
	t.Parallel()
	t.Run("removing an override restores the built-in", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "go.md", "---\ndescription: Our Go\n---\nOurs.\n")
		source, err := NewWriter(dir).Remove(t.Context(), "go")
		require.NoError(t, err)
		assert.Equal(t, SourceOverride, source)
		p, ok, err := NewSet(dir).Load("go")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, SourceBuiltin, p.Source)
	})
	t.Run("removes a user's profile, even broken", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "docs.md", "broken\n")
		source, err := NewWriter(dir).Remove(t.Context(), "docs")
		require.NoError(t, err)
		assert.Equal(t, SourceUser, source)
		assert.Empty(t, userFiles(t, dir))
	})
	t.Run("refuses a built-in", func(t *testing.T) {
		t.Parallel()
		_, err := NewWriter(t.TempDir()).Remove(t.Context(), "security")
		assert.ErrorIs(t, err, ErrBuiltin)
	})
	t.Run("refuses an unknown name", func(t *testing.T) {
		t.Parallel()
		_, err := NewWriter(t.TempDir()).Remove(t.Context(), "cobol")
		assert.ErrorIs(t, err, ErrUnknown)
	})
	t.Run("refuses an invalid name", func(t *testing.T) {
		t.Parallel()
		_, err := NewWriter(t.TempDir()).Remove(t.Context(), "Go")
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestWriter_Path(t *testing.T) {
	assert.Equal(t, filepath.Join("dir", "docs.md"), NewWriter("dir").Path("docs"))
}

func TestFormat(t *testing.T) {
	t.Run("omits when and match when unset", func(t *testing.T) {
		raw, err := Format(&Profile{Description: " Docs ", Body: "\nRead.\n"})
		require.NoError(t, err)
		assert.Equal(t, "---\ndescription: Docs\n---\n\nRead.\n", string(raw))
	})
	t.Run("writes when and match", func(t *testing.T) {
		raw, err := Format(&Profile{Description: "Go", When: WhenAuto, Match: []string{"*.go", "a<b"}, Body: "Go."})
		require.NoError(t, err)
		assert.Equal(t, "---\ndescription: Go\nwhen: auto\nmatch: [\"*.go\",\"a<b\"]\n---\n\nGo.\n", string(raw))
	})
	t.Run("round-trips every built-in", func(t *testing.T) {
		builtins, _, err := Builtin().List()
		require.NoError(t, err)
		for i := range builtins {
			raw, err := Format(&builtins[i])
			require.NoError(t, err)
			p, err := parse(builtins[i].Name, raw)
			require.NoError(t, err)
			assert.Equal(t, builtins[i].Description, p.Description)
			assert.Equal(t, builtins[i].When, p.When)
			assert.Equal(t, builtins[i].Match, p.Match)
			assert.Equal(t, builtins[i].Body, p.Body)
		}
	})
}
