// Command refgraph builds a reference graph of the Go code under pkg/ of a
// TiDB checkout: which functions, interface methods, types, vars and consts
// each function or method references.
//
// It type-checks every non-test package under <repo>/pkg (and under every
// nested module there, e.g. pkg/parser) with golang.org/x/tools/go/packages,
// then walks each function declaration (signature and body, closures
// included) and resolves every identifier through types.Info.Uses:
//
//	func   a function or concrete method (generic instances: their origin)
//	iface  a method of a package-level interface (a call through it); the
//	       interface itself is also recorded, as kind type
//	type   a package-level type
//	var    a package-level var
//	const  a package-level const
//
// Only targets declared in non-test files under <repo>/pkg are kept. Function
// symbols are named as in go_funcs.tsv (Func, Recv.Method); interface
// methods as Iface.Method; types/vars/consts by name.
//
// Outputs (each sorted, with a header row):
//
//	-decls     pkg file name kind start_line end_line lines
//	           package-level types, vars and consts of every non-test .go
//	           file under pkg/ (AST scan like cmd/inventory -funcs: files
//	           excluded by build tags are listed too). No type checking.
//	-refs      from_file from_symbol from_start_line to_file to_symbol kind
//	           function-level edges, deduplicated
//	-filerefs  from_file to_file funcs ifaces types vars_consts
//	           file-level edges: number of distinct function-level edges by
//	           kind (vars_consts = var + const), self edges excluded
//
// With -funcs <go_funcs.tsv>, it reports on stderr how many of its rows were
// found as function declarations in the type-checked packages, and writes
// the missing ones to -missing.
//
// Usage:
//
//	refgraph -repo <tidb> -decls data/go_decls.tsv
//	refgraph -repo <tidb> -modfile work/modfile/go.mod -funcs data/go_funcs.tsv \
//	    -refs work/go_refs.tsv -filerefs data/go_file_refs.tsv
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

func main() {
	repo := flag.String("repo", "", "TiDB repository root")
	mod := flag.String("modfile", "", "go.mod to use instead of <repo>/go.mod (go -modfile)")
	tags := flag.String("tags", "", "comma-separated build tags")
	declsOut := flag.String("decls", "", "write the declarations table here")
	refsOut := flag.String("refs", "", "write function-level edges here")
	fileRefsOut := flag.String("filerefs", "", "write file-level edges here")
	funcsIn := flag.String("funcs", "", "go_funcs.tsv to check coverage against")
	missingOut := flag.String("missing", "", "write go_funcs rows not found as declarations here")
	flag.Parse()
	if *repo == "" {
		log.Fatal("-repo is required")
	}
	root, err := filepath.Abs(*repo)
	if err != nil {
		log.Fatal(err)
	}
	if *declsOut != "" {
		decls, err := scanDecls(root)
		if err != nil {
			log.Fatal(err)
		}
		must(writeTSV(*declsOut, declHeader, declRows(decls)))
		log.Printf("%s: %d declarations", *declsOut, len(decls))
	}
	if *refsOut == "" && *fileRefsOut == "" && *funcsIn == "" {
		return
	}
	pkgs, err := load(root, *mod, *tags)
	if err != nil {
		log.Fatal(err)
	}
	g := analyze(root, pkgs)
	log.Printf("%d packages, %d function declarations, %d edges", len(pkgs), len(g.funcs), len(g.edges))
	if *refsOut != "" {
		must(writeTSV(*refsOut, refHeader, edgeRows(g.edges)))
	}
	if *fileRefsOut != "" {
		must(writeTSV(*fileRefsOut, fileRefHeader, fileRefRows(g.edges)))
	}
	if *funcsIn != "" {
		missing, total, err := coverage(*funcsIn, g.funcs)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("go_funcs coverage: %d of %d rows found (%.2f%%), %d missing",
			total-len(missing), total, 100*float64(total-len(missing))/float64(total), len(missing))
		if *missingOut != "" {
			must(writeTSV(*missingOut, []string{"file", "symbol", "start_line"}, missing))
		}
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------- loading

// load type-checks every non-test package under <root>/pkg, the nested
// modules under pkg/ included (they must be required by the root module,
// e.g. through a replace). Dependencies outside the patterns come from
// export data.
func load(root, mod, tags string) ([]*packages.Package, error) {
	patterns, err := loadPatterns(root)
	if err != nil {
		return nil, err
	}
	var flags []string
	if mod != "" {
		abs, err := filepath.Abs(mod)
		if err != nil {
			return nil, err
		}
		flags = append(flags, "-modfile="+abs)
	}
	if tags != "" {
		flags = append(flags, "-tags="+tags)
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:        root,
		BuildFlags: flags,
		Tests:      false,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	nerr := 0
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			if nerr < 20 {
				log.Printf("%s: %v", p.PkgPath, e)
			}
			nerr++
		}
	})
	if nerr > 0 {
		log.Printf("%d package errors (edges of affected packages may be incomplete)", nerr)
	}
	return pkgs, nil
}

// loadPatterns returns the go list patterns for every package under
// <root>/pkg: ./pkg/..., <module>/... for each nested module, and the
// directories that "..." skips (testdata, _x, .x) if they hold non-test .go
// files (e.g. pkg/testkit/testdata).
func loadPatterns(root string) ([]string, error) {
	patterns := []string{"./pkg/..."}
	type module struct{ dir, path string }
	var mods []module
	var skipped []string
	err := filepath.WalkDir(filepath.Join(root, "pkg"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if n == "testdata" || strings.HasPrefix(n, "_") || strings.HasPrefix(n, ".") {
				skipped = append(skipped, p)
			}
			return nil
		}
		if d.Name() != "go.mod" || filepath.Dir(p) == root {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if path := modfile.ModulePath(data); path != "" {
			mods = append(mods, module{filepath.Dir(p), path})
			patterns = append(patterns, path+"/...")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, dir := range skipped {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		hasSrc := false
		for _, f := range files {
			hasSrc = hasSrc || !strings.HasSuffix(f, "_test.go")
		}
		if !hasSrc {
			continue
		}
		// Import path: in the innermost nested module, else relative to root.
		pattern := "./" + filepath.ToSlash(mustRel(root, dir))
		best := ""
		for _, m := range mods {
			if strings.HasPrefix(dir, m.dir+string(filepath.Separator)) && len(m.dir) > len(best) {
				best = m.dir
				pattern = m.path + "/" + filepath.ToSlash(mustRel(m.dir, dir))
			}
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func mustRel(base, target string) string {
	r, err := filepath.Rel(base, target)
	if err != nil {
		log.Fatal(err)
	}
	return r
}

// ---------------------------------------------------------------- analysis

// funcKey identifies a function declaration as in go_funcs.tsv.
type funcKey struct {
	file, symbol string
	line         int
}

type edge struct {
	from     funcKey
	toFile   string
	toSymbol string
	kind     string // func / iface / type / var / const
}

type graph struct {
	funcs map[funcKey]bool
	edges map[edge]bool
}

func analyze(root string, pkgs []*packages.Package) *graph {
	g := &graph{funcs: map[funcKey]bool{}, edges: map[edge]bool{}}
	pkgDir := filepath.Join(root, "pkg") + string(filepath.Separator)
	// position returns the repo-relative file and line of pos if it is in a
	// non-test file under pkg/, else "". For cgo packages the syntax is
	// cmd/cgo's output: its //line comments give the original position.
	position := func(fset *token.FileSet, pos token.Pos) (string, int) {
		if !pos.IsValid() {
			return "", 0
		}
		at := fset.PositionFor(pos, false)
		if !strings.HasPrefix(at.Filename, pkgDir) {
			at = fset.Position(pos)
		}
		if !strings.HasPrefix(at.Filename, pkgDir) || strings.HasSuffix(at.Filename, "_test.go") {
			return "", 0
		}
		r, _ := filepath.Rel(root, at.Filename)
		return filepath.ToSlash(r), at.Line
	}
	rel := func(fset *token.FileSet, pos token.Pos) string {
		file, _ := position(fset, pos)
		return file
	}
	seen := map[*packages.Package]bool{}
	for _, p := range pkgs {
		if seen[p] || p.TypesInfo == nil {
			continue
		}
		seen[p] = true
		info := p.TypesInfo
		for _, f := range p.Syntax {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				file, line := position(p.Fset, fn.Pos())
				if file == "" {
					continue
				}
				from := funcKey{file, funcSymbol(fn), line}
				g.funcs[from] = true
				add := func(obj types.Object, kind, sym string) {
					if to := rel(p.Fset, obj.Pos()); to != "" {
						g.edges[edge{from, to, sym, kind}] = true
					}
				}
				ast.Inspect(fn, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					switch obj := info.Uses[id].(type) {
					case *types.Func:
						obj = obj.Origin()
						sig, _ := obj.Type().(*types.Signature)
						if sig == nil || sig.Recv() == nil {
							add(obj, "func", obj.Name())
							return true
						}
						recv := sig.Recv().Type()
						if ptr, ok := recv.(*types.Pointer); ok {
							recv = ptr.Elem()
						}
						named, _ := types.Unalias(recv).(*types.Named)
						if types.IsInterface(recv) {
							// Interfaces declared in a function or written
							// inline (x.(interface{ M() })) are part of the
							// function: no edge.
							if named != nil && isPkgLevel(named.Obj()) {
								name := named.Obj().Name()
								add(named.Obj(), "type", name)
								add(obj, "iface", name+"."+obj.Name())
							}
						} else if named != nil {
							add(obj, "func", named.Obj().Name()+"."+obj.Name())
						}
					case *types.TypeName:
						if isPkgLevel(obj) {
							add(obj, "type", obj.Name())
						}
					case *types.Var:
						if !obj.IsField() && isPkgLevel(obj) {
							add(obj, "var", obj.Name())
						}
					case *types.Const:
						if isPkgLevel(obj) {
							add(obj, "const", obj.Name())
						}
					}
					return true
				})
			}
		}
	}
	return g
}

func isPkgLevel(obj types.Object) bool {
	return obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope()
}

// funcSymbol names a function declaration as cmd/inventory -funcs does.
func funcSymbol(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) == 1 {
		return recvName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// recvName is the receiver's type name without pointer or type parameters.
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

// ---------------------------------------------------------------- declarations

type decl struct {
	pkg, file, name, kind string
	start, end            int
}

var declHeader = []string{"pkg", "file", "name", "kind", "start_line", "end_line", "lines"}

// scanDecls lists the package-level types, vars and consts of every non-test
// .go file under <root>/pkg. A spec in a parenthesized group spans its own
// lines; an ungrouped one starts at the type/var/const keyword. Blank (_)
// names are skipped.
func scanDecls(root string) ([]decl, error) {
	var out []decl
	err := filepath.WalkDir(filepath.Join(root, "pkg"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		pkg := filepath.ToSlash(filepath.Dir(rel))
		line := func(pos token.Pos) int { return fset.PositionFor(pos, false).Line }
		for _, dd := range f.Decls {
			gd, ok := dd.(*ast.GenDecl)
			if !ok || gd.Tok == token.IMPORT {
				continue
			}
			for _, spec := range gd.Specs {
				start, end := line(spec.Pos()), line(spec.End())
				if !gd.Lparen.IsValid() {
					start, end = line(gd.Pos()), line(gd.End())
				}
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out = append(out, decl{pkg, rel, s.Name.Name, "type", start, end})
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name != "_" {
							out = append(out, decl{pkg, rel, n.Name, gd.Tok.String(), start, end})
						}
					}
				}
			}
		}
		return nil
	})
	return out, err
}

func declRows(ds []decl) [][]string {
	rows := make([][]string, len(ds))
	for i, d := range ds {
		rows[i] = []string{d.pkg, d.file, d.name, d.kind, itoa(d.start), itoa(d.end), itoa(d.end - d.start + 1)}
	}
	sortRows(rows, 3)
	return rows
}

// ---------------------------------------------------------------- outputs

var refHeader = []string{"from_file", "from_symbol", "from_start_line", "to_file", "to_symbol", "kind"}

func edgeRows(edges map[edge]bool) [][]string {
	rows := make([][]string, 0, len(edges))
	for e := range edges {
		rows = append(rows, []string{e.from.file, e.from.symbol, itoa(e.from.line), e.toFile, e.toSymbol, e.kind})
	}
	sortRows(rows, len(refHeader))
	return rows
}

var fileRefHeader = []string{"from_file", "to_file", "funcs", "ifaces", "types", "vars_consts"}

func fileRefRows(edges map[edge]bool) [][]string {
	type pair struct{ from, to string }
	counts := map[pair]*[4]int{}
	col := map[string]int{"func": 0, "iface": 1, "type": 2, "var": 3, "const": 3}
	for e := range edges {
		if e.from.file == e.toFile {
			continue
		}
		k := pair{e.from.file, e.toFile}
		if counts[k] == nil {
			counts[k] = &[4]int{}
		}
		counts[k][col[e.kind]]++
	}
	rows := make([][]string, 0, len(counts))
	for k, c := range counts {
		rows = append(rows, []string{k.from, k.to, itoa(c[0]), itoa(c[1]), itoa(c[2]), itoa(c[3])})
	}
	sortRows(rows, 2)
	return rows
}

// coverage returns the go_funcs.tsv rows (file, symbol, start_line) that are
// not among the type-checked function declarations, and the row count.
func coverage(path string, funcs map[funcKey]bool) ([][]string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var missing [][]string
	total := 0
	for n := 0; sc.Scan(); n++ {
		r := strings.Split(sc.Text(), "\t")
		if n == 0 || len(r) < 5 {
			continue
		}
		total++
		line, _ := strconv.Atoi(r[4])
		if !funcs[funcKey{r[1], r[2], line}] {
			missing = append(missing, []string{r[1], r[2], r[4]})
		}
	}
	return missing, total, sc.Err()
}

// sortRows sorts by the first n columns as strings (the order tracker.py
// check expects).
func sortRows(rows [][]string, n int) {
	sort.Slice(rows, func(i, j int) bool {
		for k := 0; k < n; k++ {
			if rows[i][k] != rows[j][k] {
				return rows[i][k] < rows[j][k]
			}
		}
		return false
	})
}

func writeTSV(path string, header []string, rows [][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func itoa(n int) string { return strconv.Itoa(n) }
