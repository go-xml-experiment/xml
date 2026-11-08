// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func stdTestInputs(t testing.TB) []string {
	files := []string{"atom_test.go", "marshal_test.go", "read_test.go", "xml_test.go"}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if strings.Contains(s, "<") && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var fold func(e ast.Expr) (string, bool)
	fold = func(e ast.Expr) (string, bool) {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return "", false
			}
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		case *ast.BinaryExpr:
			if v.Op != token.ADD {
				return "", false
			}
			l, ok1 := fold(v.X)
			r, ok2 := fold(v.Y)
			return l + r, ok1 && ok2
		case *ast.ParenExpr:
			return fold(v.X)
		}
		return "", false
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok {
				if s, ok := fold(e); ok {
					add(s)
					if _, bin := e.(*ast.BinaryExpr); bin {
						return false
					}
				}
			}
			return true
		})
	}
	return out
}
