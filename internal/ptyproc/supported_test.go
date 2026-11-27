package ptyproc

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSupported(t *testing.T) {
	t.Parallel()
	want := runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	assert.Equal(t, want, Supported(), "pseudo-terminals exist on linux, darwin and windows (ConPTY)")
}
