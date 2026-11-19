package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadInstructions(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, dir, name, text string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
	}
	t.Run("reads the repo's then the workspace's files, then the user's", func(t *testing.T) {
		repo, workspace, user := t.TempDir(), t.TempDir(), t.TempDir()
		write(t, repo, "CLAUDE.md", "repo")
		write(t, repo, "AGENTS.md", "agents")
		write(t, workspace, "CLAUDE.md", "workspace")
		userFile := filepath.Join(user, "instructions.md")
		write(t, user, "instructions.md", "user")
		text, err := LoadInstructions(repo, workspace, userFile)
		require.NoError(t, err)
		assert.Equal(t, "repo\n\nagents\n\nworkspace\n\nuser", text)
	})
	t.Run("reads a shared directory once", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "CLAUDE.md", "one")
		text, err := LoadInstructions(dir, dir, "")
		require.NoError(t, err)
		assert.Equal(t, "one", text)
	})
	t.Run("skips missing files and ignores a user file that wasn't given", func(t *testing.T) {
		text, err := LoadInstructions(t.TempDir(), t.TempDir(), "")
		require.NoError(t, err)
		assert.Empty(t, text)
	})
	t.Run("cuts the text at 8 KiB with a marker", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "CLAUDE.md", strings.Repeat("a", 10000))
		text, err := LoadInstructions(dir, t.TempDir(), "")
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(text, "\n"+instructionsCut))
		assert.Len(t, strings.TrimSuffix(text, "\n"+instructionsCut), maxInstructionBytes)
	})
}
