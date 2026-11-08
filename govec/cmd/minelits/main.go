// Command minelits extracts the string and integer literals of Go test files,
// with the test function each occurs in, as the case corpus of a govec domain.
//
//	go run ./cmd/minelits <tidb>/pkg/types/time_test.go ... > domains/testdata/time_literals.tsv
//
// Output: one line per distinct (func, kind, literal): func \t kind \t value,
// where kind is "s" (value is the strconv.Quote of the string) or "i" (decimal).
//
// With -decls, the package-level `const` / `var` string literals (test fixtures such as
// the /proc files of pkg/util/cgroup's tests) are emitted too, as func "decl:<Name>".
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

func main() {
	type lit struct{ fn, kind, v string }
	seen := map[lit]bool{}
	var out []lit
	args := os.Args[1:]
	decls := len(args) > 0 && args[0] == "-decls"
	if decls {
		args = args[1:]
	}
	for _, path := range args {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, decl := range f.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok && decls && (gd.Tok == token.CONST || gd.Tok == token.VAR) {
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						bl, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || bl.Kind != token.STRING {
							continue
						}
						s, err := strconv.Unquote(bl.Value)
						if err != nil {
							continue
						}
						l := lit{"decl:" + name.Name, "s", strconv.Quote(s)}
						if !seen[l] {
							seen[l] = true
							out = append(out, l)
						}
					}
				}
				continue
			}
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fd.Name.Name, "Test") || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok {
					return true
				}
				var l lit
				switch bl.Kind {
				case token.STRING:
					s, err := strconv.Unquote(bl.Value)
					if err != nil {
						return true
					}
					l = lit{fd.Name.Name, "s", strconv.Quote(s)}
				case token.INT:
					v, err := strconv.ParseInt(bl.Value, 0, 64)
					if err != nil {
						return true
					}
					l = lit{fd.Name.Name, "i", strconv.FormatInt(v, 10)}
				default:
					return true
				}
				if !seen[l] {
					seen[l] = true
					out = append(out, l)
				}
				return true
			})
		}
	}
	for _, l := range out {
		fmt.Printf("%s\t%s\t%s\n", l.fn, l.kind, l.v)
	}
}
