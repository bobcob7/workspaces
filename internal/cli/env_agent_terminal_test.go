package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// terminalOps is agentTestOps with fixed binaries, a home and an environment
// holding secret-looking variables the child must never see.
func terminalOps(t *testing.T, session *lifetime.Session) *agentOperations {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700))
	t.Setenv("PATH", bin)
	ops := agentTestOps(agentEntries(session), listingAgents(claudeAgent()))
	ops.app.environ = []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/agent",
		"LANG=C.UTF-8",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-secret",
		"GITHUB_TOKEN=forge-secret",
		"GH_TOKEN=gh-secret",
		"WS_ENVIRONMENT=spoofed",
		"TERM=dumb",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
	}
	return ops
}

func attachArgs(ref string) *envproto.TerminalOpenArgs {
	return &envproto.TerminalOpenArgs{Kind: envproto.TermAttach, Workspace: &ref, Specs: []string{}, Cols: 80, Rows: 24}
}

func shellArgs() *envproto.TerminalOpenArgs {
	return &envproto.TerminalOpenArgs{Kind: envproto.TermShell, Specs: []string{}, Cols: 80, Rows: 24}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestAgentOperations_TerminalCommand(t *testing.T) {
	t.Run("attach uses the record's session id", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		cmd, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(os.Getenv("PATH"), "claude"), cmd.Path)
		assert.Equal(t, []string{"attach", "ab12cd34"}, cmd.Args)
		assert.Equal(t, "/cfg/workspaces/"+agentWS+"/buff-bear", cmd.Dir)
		require.NotNil(t, cmd.Session)
		assert.Equal(t, "ab12cd34", *cmd.Session)
		require.NotNil(t, cmd.Workspace)
		assert.Equal(t, agentWS, *cmd.Workspace)
		assert.Equal(t, "xterm-256color", envMap(cmd.Env)["TERM"])
	})
	t.Run("attach of a shell workspace is refused", func(t *testing.T) {
		ops := terminalOps(t, &lifetime.Session{Kind: lifetime.KindShell})
		_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, "only a Claude agent can be attached", re.Message)
	})
	t.Run("attach of a ws runner is refused", func(t *testing.T) {
		ops := terminalOps(t, &lifetime.Session{Kind: lifetime.KindAgent, ID: "rn-1", Runner: lifetime.RunnerWS})
		_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, "only a Claude agent can be attached", re.Message)
	})
	t.Run("attach of a workspace with no session is refused", func(t *testing.T) {
		ops := terminalOps(t, nil)
		_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
	})
	t.Run("an unknown workspace is workspace_not_found", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		_, err := ops.TerminalCommand(t.Context(), attachArgs("gone"))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "workspace_not_found", re.Code)
	})
	t.Run("a missing claude is claude_missing", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		t.Setenv("PATH", t.TempDir())
		_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "claude_missing", re.Code)
	})
	t.Run("attach refuses a directory outside workspaces", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		e := newMockedApp()
		e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: agentWS, Path: "/etc"}, nil }
		ops.app.workspaces = e.store
		_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
	})
	t.Run("a shell's specs follow --", func(t *testing.T) {
		ops := terminalOps(t, nil)
		name := "scratch"
		args := shellArgs()
		args.Name, args.Offline = &name, true
		args.Specs = []string{"bobcob7/buff-bear", "--evil"}
		cmd, err := ops.TerminalCommand(t.Context(), args)
		require.NoError(t, err)
		self, err := os.Executable()
		require.NoError(t, err)
		assert.Equal(t, self, cmd.Path)
		assert.Equal(t, []string{"--config-dir", "/cfg", "shell", "--name", "scratch", "--offline", "--", "bobcob7/buff-bear", "--evil"}, cmd.Args)
		assert.Nil(t, cmd.Workspace)
		assert.Nil(t, cmd.Session)
	})
	t.Run("a shell names the agent's config dir", func(t *testing.T) {
		ops := terminalOps(t, nil)
		cmd, err := ops.TerminalCommand(t.Context(), shellArgs())
		require.NoError(t, err)
		assert.Equal(t, []string{"--config-dir", "/cfg", "shell", "--"}, cmd.Args)
		assert.Equal(t, "/home/agent", cmd.Dir)
	})
	t.Run("a shell's workspace is made for the environment", func(t *testing.T) {
		ops := terminalOps(t, nil)
		cmd, err := ops.TerminalCommand(t.Context(), shellArgs())
		require.NoError(t, err)
		assert.Equal(t, "linux-server", envMap(cmd.Env)["WS_ENVIRONMENT"])
		assert.Equal(t, "xterm-256color", envMap(cmd.Env)["TERM"])
	})
	t.Run("an attach never gets WS_ENVIRONMENT", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		cmd, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		require.NoError(t, err)
		assert.NotContains(t, envMap(cmd.Env), "WS_ENVIRONMENT")
	})
	t.Run("no secret in the agent's environment reaches the child", func(t *testing.T) {
		for name, args := range map[string]*envproto.TerminalOpenArgs{"attach": attachArgs(agentWS), "shell": shellArgs()} {
			ops := terminalOps(t, claudeSession())
			cmd, err := ops.TerminalCommand(t.Context(), args)
			require.NoError(t, err, name)
			joined := strings.Join(cmd.Env, "\n")
			for _, secret := range []string{"oauth-secret", "forge-secret", "gh-secret", "aws-secret", "spoofed"} {
				assert.NotContains(t, joined, secret, name)
			}
			env := envMap(cmd.Env)
			for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", "AWS_SECRET_ACCESS_KEY"} {
				assert.NotContains(t, env, key, name)
			}
			assert.Equal(t, "/home/agent", env["HOME"], name)
			assert.Equal(t, "/usr/bin:/bin", env["PATH"], name)
		}
	})
}
