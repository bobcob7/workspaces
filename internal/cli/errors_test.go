package cli

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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
			codeClaudeMissing, codeClaudeUntrusted, codeClaudeFailed, codeInternal, codeConfigInvalid,
			codeChildFailed, codeWaitTimeout, codeEnrollmentFailed, codeEnvironmentOffline, codeEnvironmentOutdated)
	})
	t.Run("usage errors exit 2", func(t *testing.T) {
		assertExit(t, exitUsage, codeUsage, codeSpecInvalid, codeNameInvalid, codeOutsideWorkspace, codeResultInvalid, codeRoleInvalid, codeMemoryInvalid)
	})
	t.Run("not found or ambiguous exits 3", func(t *testing.T) {
		assertExit(t, exitNotFound, codeRepoNotFound, codeRepoAmbiguous, codeRefNotFound, codeWorkspaceNotFound, codeRoleUnknown, codeMemoryNotFound, codeEnvironmentUnknown)
	})
	t.Run("refusals exit 4", func(t *testing.T) {
		assertExit(t, exitRefused, codeWorkspaceExists, codeShellRunning, codeWorkspaceBusy, codeSessionMismatch, codeNotDescendant, codeRoleExists, codeRoleBuiltin,
			codeMemoryConflict, codeMemoryFull, codeNotLoopRun, codeEnvironmentExists, codeEnrollmentUsed, codeCAInUse, codeEnvironmentMismatch)
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

func TestShellExitError_Error(t *testing.T) {
	assert.Equal(t, "the shell exited with status 3", (&shellExitError{status: 3}).Error())
}

func TestErrorCode_known(t *testing.T) {
	t.Run("a code of ws is known", func(t *testing.T) {
		assert.True(t, codeRepoNotFound.known())
		assert.True(t, codeEnvironmentMismatch.known())
	})
	t.Run("any other code is not", func(t *testing.T) {
		assert.False(t, errorCode("made_up").known())
	})
	t.Run("every code in exitCode is known", func(t *testing.T) {
		codes := exitCodeCases(t)
		require.NotEmpty(t, codes)
		for _, code := range codes {
			assert.True(t, code.known(), code)
		}
	})
}

// exitCodeCases is the value of each code named in a case of errorCode's
// exitCode in errors.go, read from its source.
func exitCodeCases(t *testing.T) []errorCode {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	values := map[string]errorCode{}
	var names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			addCodeValues(values, d)
		case *ast.FuncDecl:
			if isErrorCodeExitCode(d) {
				names = append(names, caseNames(d.Body)...)
			}
		}
	}
	codes := make([]errorCode, 0, len(names))
	for _, name := range names {
		code, ok := values[name]
		require.True(t, ok, "no value for %s", name)
		codes = append(codes, code)
	}
	return codes
}

// addCodeValues adds the string constants declared in d to values.
func addCodeValues(values map[string]errorCode, d *ast.GenDecl) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
				values[name.Name] = errorCode(strings.Trim(lit.Value, `"`))
			}
		}
	}
}

// isErrorCodeExitCode reports whether d is errorCode's exitCode method.
func isErrorCodeExitCode(d *ast.FuncDecl) bool {
	if d.Recv == nil || d.Name.Name != "exitCode" {
		return false
	}
	recv, ok := d.Recv.List[0].Type.(*ast.Ident)
	return ok && recv.Name == "errorCode"
}

// caseNames is the identifiers in the case clauses of body.
func caseNames(body *ast.BlockStmt) []string {
	var names []string
	ast.Inspect(body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			if id, ok := expr.(*ast.Ident); ok {
				names = append(names, id.Name)
			}
		}
		return true
	})
	return names
}
