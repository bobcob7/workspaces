package shell

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunner_program(t *testing.T) {
	t.Run("COMSPEC", func(t *testing.T) {
		sh, args := newTestRunner(`COMSPEC=C:\Windows\system32\cmd.exe`).program()
		assert.Equal(t, `C:\Windows\system32\cmd.exe`, sh)
		assert.Empty(t, args)
	})
	t.Run("unset", func(t *testing.T) {
		sh, args := newTestRunner("HOME=/h").program()
		assert.Equal(t, "cmd.exe", sh)
		assert.Empty(t, args)
	})
	t.Run("empty", func(t *testing.T) {
		sh, _ := newTestRunner("COMSPEC=").program()
		assert.Equal(t, "cmd.exe", sh)
	})
	t.Run("SHELL is ignored", func(t *testing.T) {
		sh, _ := newTestRunner("SHELL=/usr/bin/bash").program()
		assert.Equal(t, "cmd.exe", sh)
	})
}
