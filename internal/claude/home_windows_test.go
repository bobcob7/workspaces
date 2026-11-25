package claude

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserHome(t *testing.T) {
	t.Run("HOME beats USERPROFILE", func(t *testing.T) {
		t.Setenv("HOME", `C:\git-home`)
		t.Setenv("USERPROFILE", `C:\Users\me`)
		assert.Equal(t, `C:\git-home`, userHome(os.Environ()))
	})
	t.Run("USERPROFILE when HOME is unset", func(t *testing.T) {
		t.Setenv("HOME", "")
		require.NoError(t, os.Unsetenv("HOME"))
		t.Setenv("USERPROFILE", `C:\Users\me`)
		assert.Equal(t, `C:\Users\me`, userHome(os.Environ()))
	})
}
