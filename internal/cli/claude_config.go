package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// claudeOverlays lists the workspaces' config overlays under the state
// directory (ADR 0085). Before the paths are set there are none.
func (a *App) claudeOverlays() ([]claude.Overlay, error) {
	if a.paths.State == "" {
		return nil, nil
	}
	dirs, err := filepath.Glob(filepath.Join(a.paths.State, "*", "claude"))
	if err != nil {
		return nil, err
	}
	var overlays []claude.Overlay
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, claude.OverlayMarker)); err != nil {
			continue
		}
		id := filepath.Base(filepath.Dir(dir))
		overlays = append(overlays, claude.Overlay{ConfigDir: dir, WorkspaceDir: filepath.Join(a.paths.Workspaces, id)})
	}
	return overlays, nil
}

// claudeConfigMode is the flag, else $WS_CLAUDE_CONFIG_DIR, else
// [claude] config_dir, else auto. A bad flag or variable is usage.
func (a *App) claudeConfigMode(flag string) (string, error) {
	if flag != "" {
		return validClaudeConfigMode(flag, "--claude-config-dir")
	}
	if v := envValue(a.environ, "WS_CLAUDE_CONFIG_DIR"); v != "" {
		return validClaudeConfigMode(v, "WS_CLAUDE_CONFIG_DIR")
	}
	return a.config.Claude.Mode(), nil
}

func validClaudeConfigMode(mode, source string) (string, error) {
	if config.ValidClaudeConfigMode(mode) {
		return mode, nil
	}
	return "", usageErrorf("%s must be auto, workspace or shared, not %q", source, mode)
}

// checkClaudeConfigDirFlag refuses --claude-config-dir where the remote
// environment or the ws runner decides the config directory.
func checkClaudeConfigDirFlag(flag string, remote bool, runner string) error {
	switch {
	case flag == "":
		return nil
	case remote:
		return usageErrorf("--claude-config-dir can't be used with --env; the environment's own setting applies")
	case runner == config.RunnerWS:
		return usageErrorf("--claude-config-dir can't be used with --runner ws")
	}
	return nil
}

// checkClaudeConfigFlag is checkClaudeConfigDirFlag, and the flag or
// variable's mode must be valid, before anything is created.
func (a *App) checkClaudeConfigFlag(flag string, remote bool, runner string) error {
	if err := checkClaudeConfigDirFlag(flag, remote, runner); err != nil {
		return err
	}
	_, err := a.claudeConfigMode(flag)
	return err
}

// claudeStart is the config directory and extra environment of the session
// ws starts in ws (ADR 0085). Extra always holds WS_CONFIG_DIR=<CONFIG_DIR>.
// shared: ("", extra). Otherwise it seeds CONFIG_DIR/state/<id>/claude from
// the base, adds the sign-in token from secrets.env, and checks sign-in. Not
// signed in removes the overlay: workspace mode fails claude_signed_out,
// auto warns and shares the base.
func (a *App) claudeStart(ctx context.Context, ws *workspace.Workspace, mode string) (configDir string, extra []string, err error) {
	shared := []string{"WS_CONFIG_DIR=" + a.paths.Root}
	if mode == config.ClaudeConfigShared {
		return "", shared, nil
	}
	dir := filepath.Join(a.paths.State, ws.ID, "claude")
	extra, err = a.overlayStart(ctx, dir)
	if err == nil {
		return dir, extra, nil
	}
	_ = os.RemoveAll(dir)
	if mode == config.ClaudeConfigWorkspace {
		return "", nil, newError(codeClaudeSignedOut, err)
	}
	a.warned = append(a.warned, fmt.Sprintf("Claude Code isn't signed in for a separate config directory (%v); the agent shares %s", err, claude.BaseConfigDir(a.environ)))
	return "", shared, nil
}

// overlayStart seeds dir as an overlay of the base and returns the start
// environment, or the reason Claude Code isn't signed in there.
func (a *App) overlayStart(ctx context.Context, dir string) ([]string, error) {
	globalConfig := claude.GlobalConfigFile(claude.BaseEnviron(a.environ))
	if err := claude.SeedOverlay(dir, claude.BaseConfigDir(a.environ), globalConfig, a.paths.Workspaces); err != nil {
		return nil, err
	}
	extra := append(a.startSecrets(), "WS_CONFIG_DIR="+a.paths.Root)
	signedIn, reason, err := a.claude.SignedIn(ctx, dir, extra)
	if err != nil {
		return nil, err
	}
	if !signedIn {
		return nil, errors.New(reason)
	}
	return extra, nil
}

// startSecrets are the sign-in token and API key from secrets.env that the
// environment doesn't already set (ADR 0085).
func (a *App) startSecrets() []string {
	secrets, warnings, err := config.ReadSecrets(a.paths.Root)
	a.warned = append(a.warned, warnings...)
	if err != nil {
		a.warned = append(a.warned, err.Error())
		return nil
	}
	var env []string
	for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		value, ok := secrets[name]
		if ok && !envHas(a.environ, name) {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// envHas reports whether environ sets name.
func envHas(environ []string, name string) bool {
	for _, kv := range environ {
		if k, _, ok := strings.Cut(kv, "="); ok && k == name {
			return true
		}
	}
	return false
}

// envValue is name's value in environ, the last one set; "" when unset.
func envValue(environ []string, name string) string {
	value := ""
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			value = v
		}
	}
	return value
}
