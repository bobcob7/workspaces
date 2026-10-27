package profile

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSet_Load(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeProfile(t, dir, "security.md", "---\ndescription: Our security\nwhen: off\n---\nOur body.\n")
	writeProfile(t, dir, "rust.md", "---\ndescription: Rust\nmatch: [\"*.rs\"]\n---\nRust body.\n")
	writeProfile(t, dir, "tests.md", "broken\n")
	s := NewSet(dir)
	t.Run("a user file replaces the built-in", func(t *testing.T) {
		t.Parallel()
		p, ok, err := s.Load("security")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "Our security", p.Description)
		assert.Equal(t, WhenOff, p.When)
		assert.False(t, p.Builtin)
	})
	t.Run("a user file adds a profile", func(t *testing.T) {
		t.Parallel()
		p, ok, err := s.Load("rust")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, WhenAuto, p.When)
	})
	t.Run("without a user file the built-in is read", func(t *testing.T) {
		t.Parallel()
		p, ok, err := s.Load("go")
		require.NoError(t, err)
		require.True(t, ok)
		assert.True(t, p.Builtin)
	})
	t.Run("a broken user file is an error, not the built-in", func(t *testing.T) {
		t.Parallel()
		_, ok, err := s.Load("tests")
		require.Error(t, err)
		assert.False(t, ok)
	})
	t.Run("an unknown name is not found", func(t *testing.T) {
		t.Parallel()
		_, ok, err := s.Load("cobol")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestSet_List(t *testing.T) {
	t.Parallel()
	t.Run("built-ins only without a user directory", func(t *testing.T) {
		t.Parallel()
		profiles, broken, err := NewSet(filepath.Join(t.TempDir(), "missing")).List()
		require.NoError(t, err)
		assert.Empty(t, broken)
		assert.Equal(t, []string{"architecture", "claims", "compatibility", "efficiency", "go", "security", "tests", "typescript"}, names(profiles))
	})
	t.Run("user files add, replace, and hide when broken", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeProfile(t, dir, "security.md", "---\ndescription: Our security\n---\nOur body.\n")
		writeProfile(t, dir, "rust.md", "---\ndescription: Rust\nmatch: [\"*.rs\"]\n---\nRust body.\n")
		writeProfile(t, dir, "tests.md", "broken\n")
		profiles, broken, err := NewSet(dir).List()
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(dir, "tests.md")}, broken)
		assert.Equal(t, []string{"architecture", "claims", "compatibility", "efficiency", "go", "rust", "security", "typescript"}, names(profiles))
		for _, p := range profiles {
			if p.Name == "security" {
				assert.False(t, p.Builtin)
				assert.Equal(t, "Our security", p.Description)
			}
		}
	})
}

func names(profiles []Profile) []string {
	out := make([]string, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p.Name)
	}
	return out
}
