package git

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

var errTest = errors.New("exit status 128")

func TestCommandError_Error(t *testing.T) {
	t.Run("includes stderr", func(t *testing.T) {
		err := &CommandError{Args: []string{"fetch", "origin"}, Stderr: "fatal: no remote", Err: errTest}
		assert.Equal(t, "git fetch origin: exit status 128: fatal: no remote", err.Error())
	})
	t.Run("without stderr", func(t *testing.T) {
		err := &CommandError{Args: []string{"status"}, Err: errTest}
		assert.Equal(t, "git status: exit status 128", err.Error())
	})
}

func TestCommandError_Unwrap(t *testing.T) {
	err := &CommandError{Args: []string{"status"}, Err: errTest}
	assert.ErrorIs(t, err, errTest)
}
