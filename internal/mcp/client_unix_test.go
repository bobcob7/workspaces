//go:build unix

package mcp

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Close(t *testing.T) {
	t.Parallel()
	bin := buildFakeMCP(t)
	srv, _, err := Start(t.Context(), stdioDef(bin, "stdio.yaml"), StartOptions{Environ: []string{"FAKE_MCP_TOKEN=" + testToken}})
	require.NoError(t, err)
	pid := srv.pid
	require.Positive(t, pid)
	_ = srv.Close()
	assert.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH)
}
