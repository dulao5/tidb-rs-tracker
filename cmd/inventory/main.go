// Command inventory enumerates Go unit tests and integration test files of a
// TiDB checkout and writes them as TSV rows for the go_test table.
//
// With -funcs it instead lists every function and method declared in the
// non-test .go files under pkg/ (go_funcs.tsv):
//
//	<pkg> <file> <symbol> <func|method> <start_line> <end_line> <lines> <generated>
//
// symbol is "Func" or "Recv.Method" (type parameters and pointers dropped),
// the naming cmd/covmap uses, so it joins with features.tsv symbols. Lines
// run from the func keyword to the closing brace. generated is 1 for files
// with a "// Code generated ... DO NOT EDIT." header, and for the goyacc
// output pkg/parser/parser.go.
//
// Usage: inventory -repo <tidb root> -commit <sha> > go_test.tsv
//
//	inventory -repo <tidb root> -funcs > go_funcs.tsv
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
	funcs := flag.Bool("funcs", false, "list functions of non-test files under pkg/ instead of tests")
	flag.Parse()
	if *repo != "" && *funcs {
		if err := listFuncs(*repo, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
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

// alwaysGenerated lists generated files without the standard header line.
var alwaysGenerated = map[string]bool{"pkg/parser/parser.go": true}

// listFuncs writes one row per function declaration (with or without a body)
// in the non-test .go files under <repo>/pkg.
func listFuncs(repo string, out *os.File) error {
	w := bufio.NewWriter(out)
	defer w.Flush()
	return filepath.WalkDir(filepath.Join(repo, "pkg"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		gen := b2i(ast.IsGenerated(f) || alwaysGenerated[rel])
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			sym, kind := fn.Name.Name, "func"
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				sym, kind = recvName(fn.Recv.List[0].Type)+"."+sym, "method"
			}
			start, end := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\n",
				filepath.ToSlash(filepath.Dir(rel)), rel, sym, kind, start, end, end-start+1, gen)
		}
		return nil
	})
}

// recvName is the receiver's type name without pointer or type parameters,
// as in cmd/covmap.
func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.ParenExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
