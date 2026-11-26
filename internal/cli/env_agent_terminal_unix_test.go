//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalEnvAllowed(t *testing.T) {
	allowed := []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LANGUAGE", "TMPDIR",
		"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
		"LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_COLLATE", "LC_TIME", "LC_NUMERIC", "LC_MONETARY",
		"LC_PAPER", "LC_NAME", "LC_ADDRESS", "LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION",
	}
	blocked := []string{
		"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "FORGEJO_TOKEN", "AWS_ACCESS_KEY_ID",
		"WS_CONFIG_DIR", "WS_TOKEN", "WS_ENVIRONMENT", "SSH_AUTH_SOCK", "DOCKER_HOST", "LD_PRELOAD",
		"TERM", "XDG_TOKEN", "XDG_SECRET", "LC_SECRET", "LC_TOKEN", "LC_", "", "path", "HOMEX", "PATHS",
	}
	for _, key := range allowed {
		assert.True(t, terminalEnvAllowed(key), key)
	}
	for _, key := range blocked {
		assert.False(t, terminalEnvAllowed(key), key)
	}
	var environ []string
	for _, key := range append(append([]string{}, allowed...), blocked...) {
		environ = append(environ, key+"=v")
	}
	got := envMap(terminalEnv(environ))
	for _, key := range allowed {
		assert.Equal(t, "v", got[key], key)
	}
	for _, key := range blocked {
		if key != "TERM" && key != "" {
			assert.NotContains(t, got, key)
		}
	}
	assert.Equal(t, "xterm-256color", got["TERM"])
}

func TestAgentOperations_TerminalCommand_environment_unix(t *testing.T) {
	t.Run("claude is found on the child's PATH, not the agent's", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		agentBin := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(agentBin, "claude"), []byte("#!/bin/sh\n"), 0o700))
		t.Setenv("PATH", agentBin)
		cmd, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(childPath(ops), "claude"), cmd.Path)
		assert.NotEqual(t, filepath.Join(agentBin, "claude"), cmd.Path)
	})
	t.Run("a claude on the agent's PATH only is missing", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		agentBin := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(agentBin, "claude"), []byte("#!/bin/sh\n"), 0o700))
		t.Setenv("PATH", agentBin)
		ops.app.environ = []string{"PATH=" + t.TempDir(), "HOME=/home/agent"}
		assert.Equal(t, "claude_missing", refusedAttach(t, ops).Code)
	})
	t.Run("a relative or non-executable claude is not used", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("x"), 0o600))
		_, err := lookPathIn([]string{"PATH=" + dir + ":relative:."})
		require.Error(t, err)
		planted := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(planted, "claude"), []byte("#!/bin/sh\n"), 0o700))
		t.Chdir(planted)
		_, err = lookPathIn([]string{"PATH=.:"})
		require.Error(t, err)
		_, err = lookPathIn([]string{"HOME=/x"})
		assert.Error(t, err)
	})
	t.Run("a Path variable alone is not the search path", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o700))
		_, err := lookPathIn([]string{"Path=" + dir})
		require.Error(t, err)
		_, err = lookPathIn([]string{"path=" + dir})
		assert.Error(t, err)
	})
}
