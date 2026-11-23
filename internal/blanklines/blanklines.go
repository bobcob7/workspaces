// Package blanklines finds blank lines inside function bodies, which the
// repository's Go conventions forbid. Its test checks the whole module.
package blanklines

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// finding is one blank line inside a function body.
type finding struct {
	Line int    // 1-based line of the blank line
	Func string // the function's name; "Recv.Name" for a method
}

// check returns the blank lines inside the bodies of f's function
// declarations, in line order. A line is blank when it is empty after
// strings.TrimSpace. Lines inside a string literal (a raw string spanning
// lines) and lines inside a block comment don't count. Function literals
// are covered by the declaration that holds them. src is the file's source.
func check(fset *token.FileSet, f *ast.File, src []byte) []finding {
	lines := bytes.Split(src, []byte("\n"))
	skip := multiLineLines(fset, f)
	var out []finding
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		open := fset.Position(fn.Body.Lbrace).Line
		closing := fset.Position(fn.Body.Rbrace).Line
		for line := open + 1; line < closing; line++ {
			if skip[line] || len(bytes.TrimSpace(lines[line-1])) != 0 {
				continue
			}
			out = append(out, finding{Line: line, Func: funcName(fn)})
		}
	}
	return out
}

// multiLineLines returns the lines after the first line of every comment
// and string literal that spans lines.
func multiLineLines(fset *token.FileSet, f *ast.File) map[int]bool {
	skip := map[int]bool{}
	mark := func(start, end token.Pos) {
		for line := fset.Position(start).Line + 1; line <= fset.Position(end).Line; line++ {
			skip[line] = true
		}
	}
	for _, group := range f.Comments {
		for _, c := range group.List {
			mark(c.Pos(), c.End())
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			mark(lit.Pos(), lit.End())
		}
		return true
	})
	return skip
}

// funcName returns the name of fn, qualified by its receiver's type.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// checkModule walks root (skipping .git, node_modules, desktop, testdata
// and any directory starting with "."), parses every .go file with
// parser.ParseComments, skips files for which ast.IsGenerated is true, and
// returns the findings keyed by the slash-separated path relative to root.
// A file that doesn't parse is an error naming it.
func checkModule(root string) (map[string][]finding, error) {
	found := map[string][]finding{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(root, path, d.Name())
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		return checkFile(root, path, found)
	})
	return found, err
}

func skipDir(root, path, name string) error {
	if path == root {
		return nil
	}
	if name == ".git" || name == "node_modules" || name == "desktop" || name == "testdata" || strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	return nil
}

func checkFile(root, path string, found map[string][]finding) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}
	if ast.IsGenerated(f) {
		return nil
	}
	if list := check(fset, f, src); len(list) > 0 {
		found[rel] = list
	}
	return nil
}
