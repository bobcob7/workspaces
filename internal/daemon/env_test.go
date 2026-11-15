package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bobcob7/workspaces/internal/config"
)

func TestRunnerEnv(t *testing.T) {
	t.Run("later layers replace earlier ones and names are sorted", func(t *testing.T) {
		base := []string{"PATH=/bin", "X=base", "Y=base", "WS_WORKSPACE=old"}
		cfg := config.Runner{Env: map[string]string{"X": "cfg", "Y": "cfg"}}
		secrets := map[string]string{"Y": "secret", "METRICS_TOKEN": "tok"}
		got := RunnerEnv(base, cfg, secrets, "ws-1", "/etc/ws")
		assert.Equal(t, []string{
			"METRICS_TOKEN=tok",
			"PATH=/bin",
			"WS_CONFIG_DIR=/etc/ws",
			"WS_RUNNER=ws",
			"WS_RUNNER_SECRET_NAMES=METRICS_TOKEN,Y",
			"WS_WORKSPACE=ws-1",
			"X=cfg",
			"Y=secret",
		}, got)
	})
	t.Run("ws's own variables beat every layer", func(t *testing.T) {
		base := []string{"WS_RUNNER=base"}
		cfg := config.Runner{Env: map[string]string{"WS_WORKSPACE": "cfg"}}
		secrets := map[string]string{"WS_CONFIG_DIR": "secret"}
		got := RunnerEnv(base, cfg, secrets, "ws-2", "/etc/ws")
		assert.Equal(t, []string{
			"WS_CONFIG_DIR=/etc/ws",
			"WS_RUNNER=ws",
			"WS_RUNNER_SECRET_NAMES=WS_CONFIG_DIR",
			"WS_WORKSPACE=ws-2",
		}, got)
	})
	t.Run("entries without = are dropped", func(t *testing.T) {
		got := RunnerEnv([]string{"JUNK"}, config.Runner{}, nil, "ws-3", "/etc/ws")
		assert.Equal(t, []string{
			"WS_CONFIG_DIR=/etc/ws",
			"WS_RUNNER=ws",
			"WS_RUNNER_SECRET_NAMES=",
			"WS_WORKSPACE=ws-3",
		}, got)
	})
}
