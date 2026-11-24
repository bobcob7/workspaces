package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// terminalOps is agentTestOps with fixed binaries, a home and an environment
// holding secret-looking variables the child must never see.
func terminalOps(t *testing.T, session *lifetime.Session) *agentOperations {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o700))
	t.Setenv("PATH", t.TempDir())
	ops := agentTestOps(agentEntries(session), listingAgents(claudeAgent()))
	ops.app.environ = []string{
		"PATH=" + bin,
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

// childPath is the PATH the child gets from ops' environment.
func childPath(ops *agentOperations) string {
	return envMap(ops.app.environ)["PATH"]
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
		assert.Equal(t, filepath.Join(childPath(ops), "claude"), cmd.Path)
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
		ops.app.environ = []string{"PATH=" + t.TempDir(), "HOME=/home/agent"}
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
			assert.Equal(t, childPath(ops), env["PATH"], name)
		}
	})
}

// refusedAttach runs an attach and returns its ReplyError.
func refusedAttach(t *testing.T, ops *agentOperations) *envproto.ReplyError {
	t.Helper()
	_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
	re, ok := errors.AsType[*envproto.ReplyError](err)
	require.True(t, ok, "%v", err)
	return re
}

// recordAt is a Find that answers the agent workspace with a record at path.
func recordAt(ops *agentOperations, path string) {
	e := newMockedApp()
	e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: agentWS, Path: path}, nil }
	ops.app.workspaces = e.store
}

func TestAgentOperations_TerminalCommand_attachGate(t *testing.T) {
	t.Run("a non-agent session of any kind is refused", func(t *testing.T) {
		for _, kind := range []lifetime.Kind{lifetime.KindShell, lifetime.Kind("other")} {
			ops := terminalOps(t, &lifetime.Session{Kind: kind, ID: "ab12cd34", Runner: lifetime.RunnerClaude})
			re := refusedAttach(t, ops)
			assert.Equal(t, "usage", re.Code, kind)
			assert.Equal(t, "only a Claude agent can be attached", re.Message, kind)
		}
	})
	t.Run("a session with no id is refused", func(t *testing.T) {
		ops := terminalOps(t, &lifetime.Session{Kind: lifetime.KindAgent, Runner: lifetime.RunnerClaude})
		re := refusedAttach(t, ops)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, "only a Claude agent can be attached", re.Message)
	})
	t.Run("a workspace with no session says why", func(t *testing.T) {
		re := refusedAttach(t, terminalOps(t, nil))
		assert.Equal(t, "only a Claude agent can be attached", re.Message)
	})
	t.Run("the session is the requested workspace's, not another's", func(t *testing.T) {
		other := lifetime.Entry{Workspace: listedWorkspace("other-11111111"), Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "zz99zz99", Runner: lifetime.RunnerClaude}}
		mine := lifetime.Entry{Workspace: listedWorkspace(agentWS), Session: claudeSession()}
		ops := terminalOps(t, nil)
		ops.app.lifetime = listingLifetime(other, mine)
		cmd, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		require.NoError(t, err)
		assert.Equal(t, []string{"attach", "ab12cd34"}, cmd.Args)
		require.NotNil(t, cmd.Session)
		assert.Equal(t, "ab12cd34", *cmd.Session)
	})
	t.Run("a shell workspace never gets another workspace's session", func(t *testing.T) {
		other := lifetime.Entry{Workspace: listedWorkspace("other-11111111"), Session: claudeSession()}
		mine := lifetime.Entry{Workspace: listedWorkspace(agentWS), Session: &lifetime.Session{Kind: lifetime.KindShell}}
		ops := terminalOps(t, nil)
		ops.app.lifetime = listingLifetime(other, mine)
		assert.Equal(t, "only a Claude agent can be attached", refusedAttach(t, ops).Message)
	})
	t.Run("a workspace missing from the listing is refused", func(t *testing.T) {
		other := lifetime.Entry{Workspace: listedWorkspace("other-11111111"), Session: claudeSession()}
		ops := terminalOps(t, nil)
		ops.app.lifetime = listingLifetime(other)
		assert.Equal(t, "only a Claude agent can be attached", refusedAttach(t, ops).Message)
	})
	t.Run("the session directory is confined to workspaces", func(t *testing.T) {
		for path, allowed := range map[string]bool{
			"/cfg/workspaces/a":              true,
			"/cfg/workspaces/..hidden":       true,
			"/cfg/workspaces/..x/y":          true,
			"/cfg":                           false,
			"/cfg/workspaces/..":             false,
			"/cfg/workspaces/a/../../etc":    false,
			"/cfg/workspaces/a/../../../etc": false,
			"/cfg/workspaces-evil/x":         false,
			"/cfg/other":                     false,
			"/etc":                           false,
			"/":                              false,
		} {
			ops := terminalOps(t, claudeSession())
			recordAt(ops, path)
			_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
			if allowed {
				require.NoError(t, err, path)
				continue
			}
			re, ok := errors.AsType[*envproto.ReplyError](err)
			require.True(t, ok, path)
			assert.Equal(t, "usage", re.Code, path)
			assert.Equal(t, "the workspace is outside workspaces/", re.Message, path)
		}
	})
}

func TestWithin(t *testing.T) {
	tests := []struct {
		root, path string
		want       bool
	}{
		{"/cfg/workspaces", "/cfg/workspaces", true},
		{"/cfg/workspaces", "/cfg/workspaces/", true},
		{"/cfg/workspaces/", "/cfg/workspaces/a", true},
		{"/cfg/workspaces", "/cfg/workspaces/a/b", true},
		{"/cfg/workspaces", "/cfg/workspaces/..x", true},
		{"/cfg/workspaces", "/cfg/workspaces/..hidden/y", true},
		{"/cfg/workspaces", "/cfg", false},
		{"/cfg/workspaces", "/cfg/workspaces/..", false},
		{"/cfg/workspaces", "/cfg/workspaces/a/../..", false},
		{"/cfg/workspaces", "/cfg/workspaces/a/../../etc", false},
		{"/cfg/workspaces", "/cfg/workspaces-evil/x", false},
		{"/cfg/workspaces", "/cfg/other", false},
		{"/cfg/workspaces", "/", false},
		{"/cfg/workspaces", "relative/path", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, within(tt.root, tt.path), "%s in %s", tt.path, tt.root)
	}
}

// listingLifetime is a lifetime manager that lists entries.
func listingLifetime(entries ...lifetime.Entry) lifetimeManager {
	e := newMockedApp()
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		return lifetime.Listing{Workspaces: entries}, nil
	}
	return e.lifetime
}

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

func TestAgentOperations_TerminalCommand_errors(t *testing.T) {
	t.Run("a domain error becomes a ReplyError with its code", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		e := newMockedApp()
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
			return lifetime.Listing{}, fmt.Errorf("listing: %w", loop.ErrNotFound)
		}
		ops.app.lifetime = e.lifetime
		re := refusedAttach(t, ops)
		assert.Equal(t, "loop_not_found", re.Code)
	})
	t.Run("an error with no code is passed on as it is", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		boom := errors.New("boom")
		e := newMockedApp()
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, boom }
		ops.app.lifetime = e.lifetime
		_, err := ops.TerminalCommand(t.Context(), attachArgs(agentWS))
		assert.ErrorIs(t, err, boom)
	})
	t.Run("an unknown kind is usage", func(t *testing.T) {
		ops := terminalOps(t, nil)
		_, err := ops.TerminalCommand(t.Context(), &envproto.TerminalOpenArgs{Kind: "bogus"})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, "unknown terminal kind", re.Message)
	})
	t.Run("an attach without a workspace is usage", func(t *testing.T) {
		ops := terminalOps(t, claudeSession())
		_, err := ops.TerminalCommand(t.Context(), &envproto.TerminalOpenArgs{Kind: envproto.TermAttach})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, "attach needs a workspace", re.Message)
	})
}

func TestAgentOperations_TerminalCommand_hostileShellArgv(t *testing.T) {
	hostile := []string{"-x", "--evil", "-", "--", "-rf", "has space", `it's "quoted"`, "line\nbreak", ";rm -rf /", "$(id)", "a;rm -rf", "`id`"}
	for _, value := range hostile {
		t.Run("name "+value, func(t *testing.T) {
			ops := terminalOps(t, nil)
			args := shellArgs()
			args.Name = &value
			cmd, err := ops.TerminalCommand(t.Context(), args)
			require.NoError(t, err)
			assert.Equal(t, []string{"--config-dir", "/cfg", "shell", "--name", value, "--"}, cmd.Args)
		})
		t.Run("spec "+value, func(t *testing.T) {
			ops := terminalOps(t, nil)
			args := shellArgs()
			args.Specs = []string{"bobcob7/buff-bear", value}
			cmd, err := ops.TerminalCommand(t.Context(), args)
			require.NoError(t, err)
			assert.Equal(t, []string{"--config-dir", "/cfg", "shell", "--", "bobcob7/buff-bear", value}, cmd.Args)
		})
	}
	t.Run("every hostile spec is one element after --", func(t *testing.T) {
		ops := terminalOps(t, nil)
		args := shellArgs()
		args.Specs = hostile
		cmd, err := ops.TerminalCommand(t.Context(), args)
		require.NoError(t, err)
		dash := slices.Index(cmd.Args, "--")
		require.Equal(t, 3, dash)
		assert.Equal(t, hostile, cmd.Args[dash+1:])
	})
}

func TestAgentOperations_TerminalCommand_environment(t *testing.T) {
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
	t.Run("an unset HOME fails a shell instead of starting in /", func(t *testing.T) {
		for _, environ := range [][]string{{"PATH=/bin"}, {"PATH=/bin", "HOME="}} {
			ops := terminalOps(t, nil)
			ops.app.environ = environ
			t.Setenv("HOME", "/home/process")
			_, err := ops.TerminalCommand(t.Context(), shellArgs())
			re, ok := errors.AsType[*envproto.ReplyError](err)
			require.True(t, ok)
			assert.Equal(t, "usage", re.Code)
			assert.Contains(t, re.Message, "HOME")
		}
	})
}
