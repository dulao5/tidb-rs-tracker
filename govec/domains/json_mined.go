package domains

import (
	"bufio"
	"bytes"
	_ "embed"
	"strconv"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/types"
)

// testdata/json_literals.tsv is the output of
//
//	go run ./cmd/minelits <tidb v8.5.8>/pkg/types/{json_binary,json_binary_functions,json_path_expr}_test.go
//
// (the string and integer literals of every Test function of the three files).
//
//go:embed testdata/json_literals.tsv
var jsonLiteralsTSV string

// jsonLit: a string literal of a Go test, classified. Literals that are neither a path
// (`$...`) nor JSON-like text (first non-space byte one of `{["-0123456789tfn`, or empty)
// are not inputs of any op (struct field names, messages, ...); integers are dropped.
type jsonLit struct {
	fn     string
	s      string
	isPath bool
}

func jsonLiterals() []jsonLit {
	var lits []jsonLit
	sc := bufio.NewScanner(bytes.NewReader([]byte(jsonLiteralsTSV)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		c := strings.SplitN(sc.Text(), "\t", 3)
		if len(c) != 3 || c[1] != "s" {
			continue
		}
		s, err := strconv.Unquote(c[2])
		if err != nil {
			panic(err)
		}
		t := strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(t, "$"):
			lits = append(lits, jsonLit{fn: c[0], s: s, isPath: true})
		case t == "" || strings.ContainsRune(`{["-0123456789tfn`, rune(t[0])):
			lits = append(lits, jsonLit{fn: c[0], s: s})
		}
	}
	return lits
}

// JSONMinedTests: the Go test functions with at least one literal used as an input
// (each must appear as a record src).
func JSONMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, l := range jsonLiterals() {
		if !seen[l.fn] {
			seen[l.fn] = true
			fns = append(fns, l.fn)
		}
	}
	return fns
}

// jsonMinedRecords adds the records of the mined literals, with src = the test function:
// parse / path_parse for every literal, and, within one test function, the doc x doc and
// doc x path combinations the test tables pair up (extract, remove, modify, array_insert,
// compare, contains, merge). It returns the texts and the paths for the random part.
func jsonMinedRecords(g *vec.Gen) (docs, paths []string) {
	type fnLits struct{ docs, paths []string }
	byFn := map[string]*fnLits{}
	var order []string
	for _, l := range jsonLiterals() {
		f := byFn[l.fn]
		if f == nil {
			f = &fnLits{}
			byFn[l.fn] = f
			order = append(order, l.fn)
		}
		if l.isPath {
			g.Add(l.fn, "path_parse", vec.GoString(l.s))
			paths = append(paths, l.s)
			if _, err := types.ParseJSONPathExpr(l.s); err == nil {
				f.paths = append(f.paths, l.s)
			}
			continue
		}
		g.Add(l.fn, "parse", vec.GoString(l.s))
		docs = append(docs, l.s)
		if _, err := types.ParseBinaryJSONFromString(l.s); err == nil {
			f.docs = append(f.docs, l.s)
		}
	}
	const maxDocs, maxPaths = 24, 40
	for _, fn := range order {
		f := byFn[fn]
		ds, ps := f.docs, f.paths
		if len(ds) > maxDocs {
			ds = ds[:maxDocs]
		}
		if len(ps) > maxPaths {
			ps = ps[:maxPaths]
		}
		for i, a := range ds {
			for _, b := range ds {
				g.Add(fn, "compare", vec.GoString(a), vec.GoString(b))
				g.Add(fn, "contains", vec.GoString(a), vec.GoString(b))
			}
			g.Add(fn, "merge", []any{vec.GoString(a), vec.GoString(ds[(i+1)%len(ds)])})
			for _, p := range ps {
				pp := []vec.GoString{vec.GoString(p)}
				g.Add(fn, "extract", vec.GoString(a), pp)
				g.Add(fn, "remove", vec.GoString(a), pp)
				g.Add(fn, "array_insert", vec.GoString(a), pp, vec.GoString(ds[0]))
				for mode := 1; mode <= 3; mode++ {
					g.Add(fn, "modify", vec.GoString(a), pp, []any{vec.GoString(ds[0])}, mode)
				}
			}
		}
	}
	return docs, paths
}
