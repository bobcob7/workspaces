package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gate"
)

// Compile-time check that the session's shell is a gate runner.
var _ shellRunner = (*gate.Runner)(nil)

func TestShellRunner(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Shell = gate.New(Environ([]string{"PATH=/usr/bin:/bin"}, false, nil))
	tools := New(cfg)
	require.Len(t, tools, 7)
	res, err := toolByName(t, tools, "run").Call(t.Context(), `{"command":"echo wired"}`)
	require.NoError(t, err)
	assert.Contains(t, res.Text, "exit 0\n--- stdout ---\nwired")
}
