//go:build ignore

// mine extracts the TOML snippets of TiDB v8.5.8 pkg/config/config_test.go
// (string literals and constant concatenations that look like TOML) into
// config_test_toml.json, the seed corpus of the govec domain "config".
//
//	go run mine.go <tidb>/pkg/config/config_test.go > config_test_toml.json
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

func str(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := str(x.X)
		b, ok2 := str(x.Y)
		return a + b, ok1 && ok2
	case *ast.ParenExpr:
		return str(x.X)
	}
	return "", false
}

func main() {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, os.Args[1], nil, 0)
	if err != nil {
		panic(err)
	}
	seen := map[string]bool{}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		s, ok := str(e)
		if !ok {
			return true
		}
		if strings.Contains(s, "=") && !strings.Contains(s, "BEGIN") && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
		return false // do not descend into the parts of a folded concatenation
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}
