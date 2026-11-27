//go:build unix

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// overlayEnv is a claudeEnv whose paths are under a temp root, whose base
// config directory holds a .claude.json, and whose mode is auto with
// sign-in succeeding. base is the base config directory.
func overlayEnv(t *testing.T) (e *claudeEnv, root, base string) {
	t.Helper()
	e = newClaudeEnv(t)
	root = t.TempDir()
	base = filepath.Join(root, "base")
	require.NoError(t, os.MkdirAll(base, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(base, ".claude.json"), []byte("{}"), 0o600))
	e.app.paths = config.NewPaths(root)
	e.app.environ = []string{"CLAUDE_CONFIG_DIR=" + base}
	e.app.config.Claude = config.Claude{ConfigDir: config.ClaudeConfigAuto}
	e.claude.SignedInFunc = func(context.Context, string, []string) (bool, string, error) { return true, "", nil }
	return e, root, base
}

func TestApp_claudeConfigMode(t *testing.T) {
	t.Run("the flag beats the variable, the variable beats the key, the key beats auto", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.environ = []string{"WS_CLAUDE_CONFIG_DIR=workspace"}
		app.config.Claude.ConfigDir = config.ClaudeConfigShared
		mode, err := app.claudeConfigMode(config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Equal(t, config.ClaudeConfigAuto, mode)
		mode, err = app.claudeConfigMode("")
		require.NoError(t, err)
		assert.Equal(t, config.ClaudeConfigWorkspace, mode)
		app.environ = nil
		mode, err = app.claudeConfigMode("")
		require.NoError(t, err)
		assert.Equal(t, config.ClaudeConfigShared, mode)
		app.config.Claude = config.Claude{}
		mode, err = app.claudeConfigMode("")
		require.NoError(t, err)
		assert.Equal(t, config.ClaudeConfigAuto, mode)
	})
	t.Run("a bad flag or variable is usage", func(t *testing.T) {
		app, _, _ := newTestApp()
		_, err := app.claudeConfigMode("bogus")
		requireCode(t, codeUsage, err)
		app.environ = []string{"WS_CLAUDE_CONFIG_DIR=bogus"}
		_, err = app.claudeConfigMode("")
		requireCode(t, codeUsage, err)
	})
}

func TestCheckClaudeConfigDirFlag(t *testing.T) {
	t.Run("no flag is always fine", func(t *testing.T) {
		assert.NoError(t, checkClaudeConfigDirFlag("", true, config.RunnerWS))
	})
	t.Run("remote and ws runner are usage", func(t *testing.T) {
		requireCode(t, codeUsage, checkClaudeConfigDirFlag("shared", true, config.RunnerClaude))
		requireCode(t, codeUsage, checkClaudeConfigDirFlag("shared", false, config.RunnerWS))
	})
	t.Run("local claude is fine", func(t *testing.T) {
		assert.NoError(t, checkClaudeConfigDirFlag("shared", false, config.RunnerClaude))
	})
}

func TestApp_claudeStart(t *testing.T) {
	ws := &workspace.Workspace{ID: "x"}
	t.Run("shared starts in the base with WS_CONFIG_DIR", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		dir, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigShared)
		require.NoError(t, err)
		assert.Empty(t, dir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, extra)
		assert.Empty(t, e.claude.SignedInCalls())
	})
	t.Run("auto signed in uses the overlay", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		dir, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		overlay := filepath.Join(root, "state", "x", "claude")
		assert.Equal(t, overlay, dir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, extra)
		require.Len(t, e.claude.SignedInCalls(), 1)
		assert.Equal(t, overlay, e.claude.SignedInCalls()[0].Dir)
		assert.FileExists(t, filepath.Join(overlay, claude.OverlayMarker))
		assert.Empty(t, e.app.warnings())
	})
	t.Run("auto signed out falls back with the warning and removes the overlay", func(t *testing.T) {
		e, root, base := overlayEnv(t)
		e.claude.SignedInFunc = func(context.Context, string, []string) (bool, string, error) {
			return false, "not signed in", nil
		}
		dir, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Empty(t, dir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, extra)
		assert.NoDirExists(t, filepath.Join(root, "state", "x", "claude"))
		require.Len(t, e.app.warnings(), 1)
		assert.Equal(t, "Claude Code isn't signed in for a separate config directory (not signed in); the agent shares "+base, e.app.warnings()[0])
	})
	t.Run("auto with a seed error falls back with the error as the reason", func(t *testing.T) {
		e, _, base := overlayEnv(t)
		require.NoError(t, os.Remove(filepath.Join(base, ".claude.json")))
		require.NoError(t, os.Mkdir(filepath.Join(base, ".claude.json"), 0o700))
		dir, _, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Empty(t, dir)
		require.Len(t, e.app.warnings(), 1)
		assert.Contains(t, e.app.warnings()[0], "Claude Code isn't signed in for a separate config directory (")
	})
	t.Run("workspace signed out is claude_signed_out and removes the overlay", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		e.claude.SignedInFunc = func(context.Context, string, []string) (bool, string, error) {
			return false, "not signed in", nil
		}
		_, _, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigWorkspace)
		requireCode(t, codeClaudeSignedOut, err)
		assert.NoDirExists(t, filepath.Join(root, "state", "x", "claude"))
		assert.Empty(t, e.app.warnings())
	})
	t.Run("a cancelled start is the context error, not claude_signed_out", func(t *testing.T) {
		for _, mode := range []string{config.ClaudeConfigWorkspace, config.ClaudeConfigAuto} {
			e, root, _ := overlayEnv(t)
			ctx, cancel := context.WithCancel(t.Context())
			e.claude.SignedInFunc = func(context.Context, string, []string) (bool, string, error) {
				cancel()
				return false, "", ctx.Err()
			}
			_, _, err := e.app.claudeStart(ctx, ws, mode)
			require.ErrorIs(t, err, context.Canceled, mode)
			assert.NoDirExists(t, filepath.Join(root, "state", "x", "claude"))
			assert.Empty(t, e.app.warnings(), mode)
		}
	})
	t.Run("secrets.env token is passed when the environment lacks it", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		writeSecretsFile(t, root, "CLAUDE_CODE_OAUTH_TOKEN=from-file\n")
		_, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Equal(t, []string{"CLAUDE_CODE_OAUTH_TOKEN=from-file", "WS_CONFIG_DIR=" + root}, extra)
		require.Len(t, e.claude.SignedInCalls(), 1)
		assert.Equal(t, extra, e.claude.SignedInCalls()[0].Extra)
	})
	t.Run("secrets.env API key is passed too", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		writeSecretsFile(t, root, "ANTHROPIC_API_KEY=key-from-file\n")
		_, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Equal(t, []string{"ANTHROPIC_API_KEY=key-from-file", "WS_CONFIG_DIR=" + root}, extra)
	})
	t.Run("an unreadable secrets.env warns and the start goes on without it", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		writeSecretsFile(t, root, "not a secret line\n")
		dir, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "state", "x", "claude"), dir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, extra)
		require.Len(t, e.app.warnings(), 1)
		assert.Contains(t, e.app.warnings()[0], config.SecretsFile)
	})
	t.Run("an overlay that can't be removed is a warning and the fallback still happens", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		stateDir := filepath.Join(root, "state", "x")
		e.claude.SignedInFunc = func(context.Context, string, []string) (bool, string, error) {
			require.NoError(t, os.Chmod(stateDir, 0o500))
			t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })
			return false, "not signed in", nil
		}
		dir, _, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Empty(t, dir)
		require.Len(t, e.app.warnings(), 2)
		assert.Contains(t, e.app.warnings()[0], "removing the overlay")
		assert.Contains(t, e.app.warnings()[1], "isn't signed in")
	})
	t.Run("the environment's token is not duplicated", func(t *testing.T) {
		e, root, base := overlayEnv(t)
		e.app.environ = []string{"CLAUDE_CONFIG_DIR=" + base, "CLAUDE_CODE_OAUTH_TOKEN=from-env"}
		writeSecretsFile(t, root, "CLAUDE_CODE_OAUTH_TOKEN=from-file\n")
		_, extra, err := e.app.claudeStart(t.Context(), ws, config.ClaudeConfigAuto)
		require.NoError(t, err)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, extra)
	})
}

// writeSecretsFile writes secrets.env under root with the owner-only mode
// ReadSecrets requires.
func writeSecretsFile(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, config.SecretsFile), []byte(body), 0o600))
}

func TestApp_claudeOverlays(t *testing.T) {
	t.Run("lists the state directories that hold an overlay marker", func(t *testing.T) {
		app, _, _ := newTestApp()
		root := t.TempDir()
		app.paths = config.NewPaths(root)
		overlay := filepath.Join(root, "state", "a", "claude")
		require.NoError(t, os.MkdirAll(overlay, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(overlay, claude.OverlayMarker), []byte("/base\n"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "state", "b", "claude"), 0o700))
		overlays, err := app.claudeOverlays()
		require.NoError(t, err)
		assert.Equal(t, []claude.Overlay{{ConfigDir: overlay, WorkspaceDir: filepath.Join(root, "workspaces", "a")}}, overlays)
	})
	t.Run("none before the paths are set", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.paths = config.Paths{}
		overlays, err := app.claudeOverlays()
		require.NoError(t, err)
		assert.Empty(t, overlays)
	})
}

func TestApp_runClaude_claudeConfigDir(t *testing.T) {
	t.Run("an overlay session runs under the workspace's config directory with WS_CONFIG_DIR", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		require.NoError(t, e.run(t, "fix it"))
		require.Len(t, e.claude.StartCalls(), 1)
		opts := e.claude.StartCalls()[0].Opts
		assert.Equal(t, filepath.Join(root, "state", "x", "claude"), opts.ConfigDir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, opts.Env)
	})
	t.Run("--claude-config-dir shared runs in the base", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		err := e.app.runClaude(t.Context(), &claudeOptions{specs: []string{"buff-bear"}, claudeArgs: []string{"fix it"}, claudeConfigDir: config.ClaudeConfigShared})
		require.NoError(t, err)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Empty(t, e.claude.StartCalls()[0].Opts.ConfigDir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, e.claude.StartCalls()[0].Opts.Env)
	})
	t.Run("a bad flag is usage and creates no workspace", func(t *testing.T) {
		e, _, _ := overlayEnv(t)
		err := e.app.runClaude(t.Context(), &claudeOptions{specs: []string{"buff-bear"}, claudeArgs: []string{"fix it"}, claudeConfigDir: "bogus"})
		requireCode(t, codeUsage, err)
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("--runner ws with the flag is usage and creates no workspace", func(t *testing.T) {
		e, _, _ := overlayEnv(t)
		err := e.app.runClaude(t.Context(), &claudeOptions{specs: []string{"buff-bear"}, claudeArgs: []string{"fix it"}, runner: config.RunnerWS, claudeConfigDir: config.ClaudeConfigShared})
		requireCode(t, codeUsage, err)
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("--claude-config-dir with --env is usage and creates no workspace", func(t *testing.T) {
		e := newRemoteClaudeEnv(t, remoteCreated, nil)
		err := e.app.runClaude(t.Context(), &claudeOptions{env: "linux-server", specs: []string{"buff-bear"}, claudeArgs: []string{"fix it"}, claudeConfigDir: config.ClaudeConfigShared})
		requireCode(t, codeUsage, err)
		assert.Empty(t, e.caller.CallEnvironmentCalls())
		assert.Empty(t, e.store.CreateCalls())
	})
}

func TestApp_runMachineSpawn_claudeConfigDir(t *testing.T) {
	t.Run("the flag reaches the child's start", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		opts := spawnOptions("fix it")
		opts.claudeConfigDir = config.ClaudeConfigShared
		require.NoError(t, e.app.runMachineSpawn(t.Context(), opts))
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Empty(t, e.claude.StartCalls()[0].Opts.ConfigDir)
		assert.Equal(t, []string{"WS_CONFIG_DIR=" + root}, e.claude.StartCalls()[0].Opts.Env)
	})
	t.Run("without the flag the child runs under the workspace's directory", func(t *testing.T) {
		e, root, _ := overlayEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		require.NoError(t, e.app.runMachineSpawn(t.Context(), spawnOptions("fix it")))
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, filepath.Join(root, "state", "x", "claude"), e.claude.StartCalls()[0].Opts.ConfigDir)
	})
	t.Run("with --runner ws it is usage", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		opts := spawnOptions("fix it")
		opts.runner = config.RunnerWS
		opts.claudeConfigDir = config.ClaudeConfigShared
		requireCode(t, codeUsage, e.app.runMachineSpawn(t.Context(), opts))
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("with --env on a remote environment it is usage and sends nothing", func(t *testing.T) {
		e := roleSpawnEnv(t)
		e.app.global.json = true
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveScopeHost(t, e.app, scopeOK(t, map[string]any{
			"id": "x", "session": "e8cdf5d8", "path": "/cfg/workspaces/x", "parent": nil, "repos": []any{},
			"warnings": []string{}, "runner": "claude", "model": nil, "ws_runner": nil, "scope": []string{"local", "linux"},
		}))
		opts := spawnOptions("fix it")
		opts.target = linuxRef.Name
		opts.claudeConfigDir = config.ClaudeConfigShared
		requireCode(t, codeUsage, e.app.runMachineSpawn(t.Context(), opts))
		assert.Empty(t, e.store.CreateCalls())
		assert.Empty(t, host.Calls())
	})
}

func TestApp_startAgent_claudeConfigDir(t *testing.T) {
	t.Run("a bad mode removes the workspace and starts nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		_, err := e.app.startAgent(t.Context(), &e.ws, []string{"fix it"}, "bogus")
		requireCode(t, codeUsage, err)
		assert.Empty(t, e.claude.StartCalls())
		require.Len(t, e.store.RemoveCalls(), 1)
	})
	t.Run("the session's args reach the start unchanged under an overlay", func(t *testing.T) {
		e, _, _ := overlayEnv(t)
		_, err := e.app.startAgent(t.Context(), &e.ws, []string{"fix it"}, "")
		require.NoError(t, err)
		require.Len(t, e.claude.StartCalls(), 1)
		assert.Equal(t, []string{"fix it"}, e.claude.StartCalls()[0].Opts.Args)
	})
}
