// Command inventory enumerates Go unit tests and integration test files of a
// TiDB checkout and writes them as TSV rows for the go_test table.
//
// Usage: inventory -repo <tidb root> -commit <sha> > go_test.tsv
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type row struct {
	kind, pkg, path, name string
	line, subtests        int
	usesStore, usesFP     bool
	stmts                 int
}

func main() {
	repo := flag.String("repo", "", "tidb repository root")
	commit := flag.String("commit", "", "base commit sha")
	flag.Parse()
	if *repo == "" || *commit == "" {
		log.Fatal("-repo and -commit are required")
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	emit := func(r row) {
		fmt.Fprintf(w, "%s\ttidb\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%s\n",
			r.kind, r.pkg, r.path, r.name, r.line, r.subtests,
			b2i(r.usesStore), b2i(r.usesFP), nullable(r.stmts), *commit)
	}

	for _, dir := range []string{"pkg", "cmd", "br", "lightning", "dumpling"} {
		root := filepath.Join(*repo, dir)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
				return nil
			}
			return unitTests(*repo, p, emit)
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	itDir := filepath.Join(*repo, "tests", "integrationtest", "t")
	err := filepath.WalkDir(itDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".test") {
			return nil
		}
		rel, _ := filepath.Rel(*repo, p)
		n, err := countStmts(p)
		if err != nil {
			return err
		}
		emit(row{
			kind:  "integration",
			pkg:   filepath.Dir(rel),
			path:  rel,
			name:  strings.TrimSuffix(filepath.Base(p), ".test"),
			stmts: n,
		})
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}

func unitTests(repo, path string, emit func(row)) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	rel, _ := filepath.Rel(repo, path)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !isTestFunc(fn) {
			continue
		}
		r := row{
			kind: "unit",
			pkg:  filepath.Dir(rel),
			path: rel,
			name: fn.Name.Name,
			line: fset.Position(fn.Pos()).Line,
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Run":
				if isIdent(sel.X, "t") {
					r.subtests++
				}
			case "CreateMockStore", "CreateMockStoreAndDomain", "NewTestKit":
				r.usesStore = true
			case "Enable", "EnableCall", "EnableWith":
				if isIdent(sel.X, "failpoint") || isIdent(sel.X, "testfailpoint") {
					r.usesFP = true
				}
			}
			return true
		})
		emit(r)
	}
	return nil
}

// isTestFunc reports whether fn has the shape func TestXxx(t *testing.T).
func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return false
	}
	if r, _ := utf8.DecodeRuneInString(name[4:]); unicode.IsLower(r) {
		return false
	}
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && isIdent(sel.X, "testing") && sel.Sel.Name == "T"
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// countStmts counts SQL statements in a mysql-tester .test file: lines ending
// with ';' that are not comments or tester directives.
func countStmts(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "--") {
			continue
		}
		if strings.HasSuffix(l, ";") {
			n++
		}
	}
	return n, sc.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullable(n int) string {
	if n == 0 {
		return "" // loaded as NULL by load.sh
	}
	return fmt.Sprint(n)
}
