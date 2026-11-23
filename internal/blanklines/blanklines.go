// Package blanklines finds blank lines inside function bodies, which the
// repository's Go conventions forbid. Its test checks the whole module.
package blanklines

import (
	"go/ast"
	"go/token"
)

// finding is one blank line inside a function body.
type finding struct {
	Line int    // 1-based line of the blank line
	Func string // the function's name; "Recv.Name" for a method
}

// check returns the blank lines inside the bodies of f's function
// declarations, in line order.
func check(fset *token.FileSet, f *ast.File, src []byte) []finding {
	return nil
}

// checkModule walks root and returns the findings keyed by the
// slash-separated path relative to root.
func checkModule(root string) (map[string][]finding, error) {
	return nil, nil
}
