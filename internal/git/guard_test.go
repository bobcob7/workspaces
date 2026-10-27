package git

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moduleRoot is the repository root, relative to this package.
const moduleRoot = "../.."

// TestOnlyThisPackageRunsGit is ADR 0003's guard, which ADR 0037 extends to
// the review engine forked from loam-refinery: no Go file outside
// internal/git starts a git process, and nothing under internal/review
// starts any process at all.
func TestOnlyThisPackageRunsGit(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "target" || strings.HasPrefix(d.Name(), ".") && path != moduleRoot) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/git/") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		checked++
		for _, call := range execCalls(file) {
			assert.False(t, startsGit(call), "%s starts git; run it through internal/git (ADR 0003)", fset.Position(call.Pos()))
			if strings.HasPrefix(rel, "internal/review/") {
				assert.Fail(t, "the review engine starts a process", "%s: anchor verification runs git only through internal/git (ADR 0037)", fset.Position(call.Pos()))
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, checked, 100, "the walk found the module's Go files")
}

// execCalls are the file's calls to os/exec's Command and CommandContext,
// under whatever name the file imports os/exec as.
func execCalls(file *ast.File) []*ast.CallExpr {
	name := ""
	for _, imp := range file.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "os/exec" {
			name = "exec"
			if imp.Name != nil {
				name = imp.Name.Name
			}
		}
	}
	if name == "" {
		return nil
	}
	var calls []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == name && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

// startsGit reports whether call names "git" as the program, as a string
// literal argument.
func startsGit(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && s == "git" {
			return true
		}
	}
	return false
}

func TestStartsGit(t *testing.T) {
	parse := func(src string) []*ast.CallExpr {
		file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		require.NoError(t, err)
		return execCalls(file)
	}
	t.Run("finds git under any import name", func(t *testing.T) {
		calls := parse(`package x; import run "os/exec"; func f() { run.CommandContext(nil, "git", "status") }`)
		require.Len(t, calls, 1)
		assert.True(t, startsGit(calls[0]))
	})
	t.Run("another program isn't git", func(t *testing.T) {
		calls := parse(`package x; import "os/exec"; func f() { exec.Command("sh", "-c", "true") }`)
		require.Len(t, calls, 1)
		assert.False(t, startsGit(calls[0]))
	})
	t.Run("a file without os/exec has no calls", func(t *testing.T) {
		assert.Empty(t, parse(`package x; func f() { exec.Command("git") }`))
	})
}
