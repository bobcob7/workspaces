package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func TestApp_runMachineEnvs(t *testing.T) {
	t.Run("outside a workspace is outside_workspace", func(t *testing.T) {
		e := newMachineApp(t)
		e.outside()
		requireCode(t, codeOutsideWorkspace, e.app.runMachineEnvs(t.Context()))
	})
	t.Run("a local scope lists this machine alone, with no daemon call", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.environ = append(e.app.environ, "WS_DAEMON_AUTOSTART=0")
		require.NoError(t, e.app.runMachineEnvs(t.Context()))
		assert.JSONEq(t, `{"environments":[{"name":"local","id":"local","state":"online","current":true}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a scope with environments is the controller's list", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		host := serveScopeHost(t, e.app, scopeOK(t, envproto.ScopeEnvsResult{Environments: []envproto.ScopeEnv{
			{Name: "local", ID: "local", State: "online", Current: true},
			{Name: "linux", ID: linuxRef.ID, State: "offline", Current: false},
		}}))
		require.NoError(t, e.app.runMachineEnvs(t.Context()))
		require.Len(t, host.Calls(), 1)
		assert.Equal(t, "envs", host.Calls()[0].op)
		assert.JSONEq(t, `{"environments":[{"name":"local","id":"local","state":"online","current":true},{"name":"linux","id":"env-1a2b3c4d","state":"offline","current":false}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a daemon that isn't running lists the scope file's environments as unknown, with a warning", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.environ = append(e.app.environ, "WS_DAEMON_AUTOSTART=0")
		writeCallerScope(t, e.app.paths.State, linuxRef)
		require.NoError(t, e.app.runMachineEnvs(t.Context()))
		var out struct {
			Environments []struct {
				Name    string `json:"name"`
				ID      string `json:"id"`
				State   string `json:"state"`
				Current bool   `json:"current"`
			} `json:"environments"`
			Warnings []string `json:"warnings"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
		require.Len(t, out.Environments, 2)
		assert.Equal(t, "local", out.Environments[0].Name)
		assert.Equal(t, "linux", out.Environments[1].Name)
		assert.Equal(t, "unknown", out.Environments[1].State)
		assert.Len(t, out.Warnings, 1)
	})
}
