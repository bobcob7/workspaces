package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errTest = errors.New("boom")

func TestCliError_Error(t *testing.T) {
	err := &cliError{code: codeInternal, err: errTest}
	assert.Equal(t, "boom", err.Error())
}

func TestCliError_Unwrap(t *testing.T) {
	err := &cliError{code: codeInternal, err: errTest}
	assert.ErrorIs(t, err, errTest)
}

func TestCliError_exitCode(t *testing.T) {
	t.Run("uses its code's exit code", func(t *testing.T) {
		err := &cliError{code: codeRepoNotFound, err: errTest}
		assert.Equal(t, exitNotFound, err.exitCode())
	})
	t.Run("unknown code fails with 1", func(t *testing.T) {
		err := &cliError{code: "made_up", err: errTest}
		assert.Equal(t, exitFailed, err.exitCode())
	})
}

func TestErrorCode_exitCode(t *testing.T) {
	assertExit := func(t *testing.T, want int, codes ...errorCode) {
		t.Helper()
		for _, code := range codes {
			assert.Equal(t, want, code.exitCode(), code)
		}
	}
	t.Run("failures exit 1", func(t *testing.T) {
		assertExit(t, exitFailed, codeFetchFailed, codeCloneFailed, codeGitFailed,
			codeClaudeMissing, codeClaudeUntrusted, codeClaudeFailed, codeInternal, codeConfigInvalid)
	})
	t.Run("usage errors exit 2", func(t *testing.T) {
		assertExit(t, exitUsage, codeUsage, codeSpecInvalid, codeNameInvalid)
	})
	t.Run("not found or ambiguous exits 3", func(t *testing.T) {
		assertExit(t, exitNotFound, codeRepoNotFound, codeRepoAmbiguous, codeRefNotFound, codeWorkspaceNotFound)
	})
	t.Run("refusals exit 4", func(t *testing.T) {
		assertExit(t, exitRefused, codeWorkspaceExists, codeShellRunning)
	})
	t.Run("unknown code exits 1", func(t *testing.T) {
		assert.Equal(t, exitFailed, errorCode("made_up").exitCode())
	})
}

func TestNewError(t *testing.T) {
	err := newError(codeSpecInvalid, errTest)
	assert.Equal(t, codeSpecInvalid, err.code)
	assert.Equal(t, errTest, err.err)
	assert.Nil(t, err.details)
}

func TestUsageErrorf(t *testing.T) {
	err := usageErrorf("bad %s", "input")
	assert.Equal(t, codeUsage, err.code)
	assert.EqualError(t, err, "bad input")
}

func TestAsCLIError(t *testing.T) {
	t.Run("keeps an existing code", func(t *testing.T) {
		original := newError(codeShellRunning, errTest)
		assert.Same(t, original, asCLIError(original))
	})
	t.Run("finds a wrapped cliError", func(t *testing.T) {
		original := newError(codeShellRunning, errTest)
		got := asCLIError(fmt.Errorf("context: %w", original))
		assert.Same(t, original, got)
	})
	t.Run("makes other errors internal", func(t *testing.T) {
		got := asCLIError(errTest)
		require.NotNil(t, got)
		assert.Equal(t, codeInternal, got.code)
		assert.ErrorIs(t, got, errTest)
	})
}
