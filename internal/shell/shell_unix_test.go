//go:build unix

package shell

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunner_program(t *testing.T) {
	t.Run("SHELL", func(t *testing.T) {
		sh, args := newTestRunner("SHELL=/bin/zsh").program()
		assert.Equal(t, "/bin/zsh", sh)
		assert.Equal(t, []string{"-i"}, args)
	})
	t.Run("the last SHELL wins", func(t *testing.T) {
		sh, _ := newTestRunner("SHELL=/bin/zsh", "SHELL=/bin/bash").program()
		assert.Equal(t, "/bin/bash", sh)
	})
	t.Run("unset", func(t *testing.T) {
		sh, args := newTestRunner("HOME=/h").program()
		assert.Equal(t, "/bin/sh", sh)
		assert.Equal(t, []string{"-i"}, args)
	})
	t.Run("empty", func(t *testing.T) {
		sh, _ := newTestRunner("SHELL=").program()
		assert.Equal(t, "/bin/sh", sh)
	})
}
