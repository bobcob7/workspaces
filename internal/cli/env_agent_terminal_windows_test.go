//go:build windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalEnvAllowed_windows(t *testing.T) {
	t.Run("the system variables pass whatever their case", func(t *testing.T) {
		for _, key := range []string{"PATH", "Path", "path", "PATHEXT", "SystemRoot", "SYSTEMROOT", "ProgramFiles(x86)", "USERPROFILE", "TEMP"} {
			assert.True(t, terminalEnvAllowed(key), key)
		}
	})
	t.Run("secrets and everything else are withheld", func(t *testing.T) {
		for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN", "AWS_SECRET_ACCESS_KEY", "USER", "SHELL", "TERM", "", "PATHS"} {
			assert.False(t, terminalEnvAllowed(key), key)
		}
	})
	t.Run("Path keeps its spelling and secrets are dropped", func(t *testing.T) {
		got := envMap(terminalEnv([]string{"Path=C:\\bin", "SystemRoot=C:\\Windows", "GH_TOKEN=secret"}))
		assert.Equal(t, "C:\\bin", got["Path"])
		assert.Equal(t, "C:\\Windows", got["SystemRoot"])
		assert.NotContains(t, got, "GH_TOKEN")
	})
}

func TestExecutableNames(t *testing.T) {
	t.Run("an unset PATHEXT tries the defaults", func(t *testing.T) {
		assert.Equal(t, []string{"claude.COM", "claude.EXE", "claude.BAT", "claude.CMD"}, executableNames("claude", nil))
	})
	t.Run("an empty PATHEXT tries the defaults", func(t *testing.T) {
		assert.Equal(t, []string{"claude.COM", "claude.EXE", "claude.BAT", "claude.CMD"}, executableNames("claude", []string{"PATHEXT="}))
	})
	t.Run("a custom PATHEXT drops empty entries and keeps its order", func(t *testing.T) {
		assert.Equal(t, []string{"claude.EXE", "claude.CMD"}, executableNames("claude", []string{"PATHEXT=.EXE;;.CMD"}))
	})
	t.Run("the last PATHEXT wins, whatever the key's case", func(t *testing.T) {
		env := []string{"PATHEXT=.EXE", "pathext=.CMD"}
		assert.Equal(t, []string{"claude.CMD"}, executableNames("claude", env))
	})
	t.Run("a program that already has a PATHEXT extension keeps its name", func(t *testing.T) {
		assert.Equal(t, []string{"claude.exe"}, executableNames("claude.exe", []string{"PATHEXT=.EXE;.CMD"}))
		assert.Equal(t, []string{"claude.EXE"}, executableNames("claude.EXE", []string{"PATHEXT=.exe;.cmd"}))
	})
}

// assertSamePath compares file paths the way NTFS does: lookPathIn joins the
// PATHEXT spelling (claude.CMD), the file on disk may be claude.cmd.
func assertSamePath(t *testing.T, want, got string) {
	t.Helper()
	assert.True(t, strings.EqualFold(want, got), "paths differ beyond case: want %q, got %q", want, got)
}

func TestLookPathIn_windows(t *testing.T) {
	t.Run("finds claude.cmd on a Path given in mixed case", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude.cmd"), []byte("x"), 0o600))
		got, err := lookPathIn([]string{"Path=" + dir, "PATHEXT=.EXE;.CMD"})
		require.NoError(t, err)
		assertSamePath(t, filepath.Join(dir, "claude.cmd"), got)
	})
	t.Run("prefers the earlier extension in PATHEXT when both exist", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude.cmd"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude.exe"), []byte("x"), 0o600))
		got, err := lookPathIn([]string{"Path=" + dir, "PATHEXT=.EXE;.CMD"})
		require.NoError(t, err)
		assertSamePath(t, filepath.Join(dir, "claude.exe"), got)
	})
	t.Run("an unset PATHEXT finds claude.exe", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude.exe"), []byte("x"), 0o600))
		got, err := lookPathIn([]string{"Path=" + dir})
		require.NoError(t, err)
		assertSamePath(t, filepath.Join(dir, "claude.exe"), got)
	})
	t.Run("a directory named claude.exe is skipped for a later real one", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(first, "claude.exe"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(second, "claude.exe"), []byte("x"), 0o600))
		got, err := lookPathIn([]string{"Path=" + first + string(filepath.ListSeparator) + second})
		require.NoError(t, err)
		assertSamePath(t, filepath.Join(second, "claude.exe"), got)
	})
	t.Run("skips a relative directory", func(t *testing.T) {
		planted := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(planted, "claude.cmd"), []byte("x"), 0o600))
		t.Chdir(planted)
		_, err := lookPathIn([]string{"Path=.", "PATHEXT=.EXE;.CMD"})
		assert.ErrorIs(t, err, exec.ErrNotFound)
	})
	t.Run("an extensionless claude is not found", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("x"), 0o600))
		_, err := lookPathIn([]string{"Path=" + dir, "PATHEXT=.EXE;.CMD"})
		assert.ErrorIs(t, err, exec.ErrNotFound)
	})
}

func TestAgentOperations_TerminalCommand_windows(t *testing.T) {
	t.Run("attach finds claude.exe on a Path environ", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude.exe"), []byte("x"), 0o600))
		ops.app.environ = []string{"Path=" + dir, "HOME=C:\\Users\\agent"}
		cmd, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		require.NoError(t, err)
		assertSamePath(t, filepath.Join(dir, "claude.exe"), cmd.Path)
		assert.Equal(t, "attach", cmd.Args[0])
		assert.Contains(t, cmd.Env, "Path="+dir)
	})
}
