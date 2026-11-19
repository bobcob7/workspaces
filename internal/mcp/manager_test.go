package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const slowConfig = `server: {name: fake, version: 0.0.0-fake}
faults: {slow_initialize: 5s}
tools:
  - name: lookup_service
    input_schema: {type: object}
    annotations: {readOnlyHint: true}
    replies:
      - text: "slow"
`

func TestStartAll(t *testing.T) {
	t.Parallel()
	bin := buildFakeMCP(t)
	slow := filepath.Join(t.TempDir(), "slow.yaml")
	require.NoError(t, os.WriteFile(slow, []byte(slowConfig), 0o600))
	defs := []ServerDef{
		{
			Name: "good", Source: sourceUser, Type: typeHTTP, URL: serveFake(t, fixtureConfig(t, "http.yaml")),
			Headers: map[string]string{"Authorization": "Bearer " + testToken},
		},
		{Name: "slow", Source: sourceUser, Type: typeStdio, Command: bin, Args: []string{"--config", slow}},
	}
	start := time.Now()
	m, secrets := StartAll(t.Context(), defs, StartOptions{Timeout: 200 * time.Millisecond}, testLogger())
	t.Cleanup(func() { _ = m.Close() })
	assert.Less(t, time.Since(start), time.Second)
	assert.Contains(t, secrets, "Bearer "+testToken)
	statuses := m.Status()
	require.Len(t, statuses, 2)
	assert.Equal(t, "connected", statuses[0].State)
	assert.Equal(t, 1, statuses[0].Tools)
	assert.Equal(t, "failed", statuses[1].State)
	require.NotNil(t, statuses[1].Error)
	assert.Equal(t, 0, statuses[1].Tools)
	require.Len(t, m.Tools(), 1)
	res, err := m.Call(t.Context(), "mcp__good__lookup_service", `{"name":"api"}`)
	require.NoError(t, err)
	assert.Equal(t, "api: owner team-a, port 8080", res.Text)
}

func TestManager_Call(t *testing.T) {
	t.Run("an unknown offered name is an error", func(t *testing.T) {
		m, _ := newManager(nil)
		_, err := m.Call(t.Context(), "mcp__nope__tool", "{}")
		require.ErrorContains(t, err, "unknown tool")
	})
}
