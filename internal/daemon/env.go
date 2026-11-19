package daemon

import (
	"maps"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/config"
)

// RunnerEnv builds a runner's environment, lowest precedence first: base
// (the daemon's own, captured at start), cfg.Env, secrets, then
// WS_RUNNER=ws, WS_WORKSPACE=<id>, WS_CONFIG_DIR=<configDir> and
// WS_RUNNER_SECRET_NAMES (the secrets' names, comma-separated, for the
// runner's scrub). Later layers replace earlier values of the same name.
func RunnerEnv(base []string, cfg config.Runner, secrets map[string]string, workspaceID, configDir string) []string {
	env := parseEnv(base)
	maps.Copy(env, cfg.Env)
	maps.Copy(env, secrets)
	maps.Copy(env, map[string]string{
		"WS_RUNNER":              config.RunnerWS,
		"WS_WORKSPACE":           workspaceID,
		"WS_CONFIG_DIR":          configDir,
		"WS_RUNNER_SECRET_NAMES": strings.Join(slices.Sorted(maps.Keys(secrets)), ","),
	})
	return formatEnv(env)
}

// parseEnv reads KEY=value entries; a later entry of the same name wins.
func parseEnv(entries []string) map[string]string {
	env := map[string]string{}
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[name] = value
	}
	return env
}

// formatEnv returns env as KEY=value entries sorted by name.
func formatEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for _, name := range slices.Sorted(maps.Keys(env)) {
		out = append(out, name+"="+env[name])
	}
	return out
}
