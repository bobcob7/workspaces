package exectest

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteScript(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fake")
	WriteScript(t, path, "#!/bin/sh\necho ran\n")
	out, err := exec.CommandContext(t.Context(), path).Output()
	require.NoError(t, err)
	assert.Equal(t, "ran\n", string(out))
}
