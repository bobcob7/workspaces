package claude

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errTest = errors.New("exit status 1")

func TestCommandError_Error(t *testing.T) {
	t.Run("includes stderr", func(t *testing.T) {
		err := &CommandError{Args: []string{"stop", "abc"}, Stderr: "No job matching 'abc'", Err: errTest}
		assert.Equal(t, "claude stop abc: exit status 1: No job matching 'abc'", err.Error())
	})
	t.Run("without stderr", func(t *testing.T) {
		err := &CommandError{Args: []string{"agents"}, Err: errTest}
		assert.Equal(t, "claude agents: exit status 1", err.Error())
	})
}

func TestCommandError_Unwrap(t *testing.T) {
	err := &CommandError{Args: []string{"agents"}, Err: errTest}
	assert.ErrorIs(t, err, errTest)
}

func TestClassify(t *testing.T) {
	t.Run("untrusted directory", func(t *testing.T) {
		cmdErr := &CommandError{Stderr: "Workspace not trusted. Run `claude` in /w once and accept the trust prompt, then retry.", Err: errTest}
		err := classify(cmdErr)
		require.ErrorIs(t, err, ErrUntrusted)
		var got *CommandError
		require.ErrorAs(t, err, &got)
		assert.Same(t, cmdErr, got)
	})
	t.Run("unknown session", func(t *testing.T) {
		err := classify(&CommandError{Stderr: "No job matching 'f381de70'. Run 'claude agents' to list running sessions.", Err: errTest})
		assert.ErrorIs(t, err, ErrNoSession)
	})
	t.Run("anything else", func(t *testing.T) {
		cmdErr := &CommandError{Stderr: "error: unknown option '--bg'", Err: errTest}
		err := classify(cmdErr)
		assert.Same(t, cmdErr, err)
	})
}
