package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fixture is a tiny module with the same layout as TiDB: packages under
// pkg/, one of them a nested module required through a replace.
func loadFixture(t *testing.T) (string, *graph) {
	t.Helper()
	root, err := filepath.Abs("testdata/fixture")
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := load(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return root, analyze(root, pkgs)
}

func edgeSet(g *graph) map[string]bool {
	s := map[string]bool{}
	for _, r := range edgeRows(g.edges) {
		s[strings.Join(r, " ")] = true
	}
	return s
}

func TestEdges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture has a _windows.go file")
	}
	_, g := loadFixture(t)
	got := edgeSet(g)
	const a, b, sub = "pkg/a/a.go", "pkg/b/b.go", "pkg/sub/sub.go"
	want := []string{
		// call through an interface: the method as iface, and the interface type
		a + " Total 39 " + a + " Shape.Area iface",
		a + " Total 39 " + a + " Shape type",
		// signature types are included
		a + " Describe 47 " + a + " Named type",
		// method of an embedded interface: named by the interface declaring it
		a + " Describe 47 " + a + " Named.Name iface",
		a + " Describe 47 " + a + " Shape.Area iface",
		a + " Describe 47 " + a + " Shape type",
		// closure body belongs to the enclosing function; nested module
		a + " UseClosure 49 " + a + " Default var",
		a + " UseClosure 49 " + a + " Sq.Area func",
		a + " UseClosure 49 " + a + " Max const",
		a + " UseClosure 49 " + sub + " Twice func",
		// method value
		a + " MethodValue 53 " + a + " Sq.Area func",
		a + " MethodValue 53 " + a + " Sq type",
		// generic instances resolve to their origin
		a + " Generic 58 " + a + " List type",
		a + " Generic 58 " + a + " List.Push func",
		a + " Generic 58 " + a + " Map func",
		a + " Generic 58 " + a + " Min const",
		a + " Sq.Area 18 " + a + " Sq type",
		a + " List.Push 29 " + a + " List type",
		// cross package; package-level types of the same file
		b + " Sum 7 " + a + " Total func",
		b + " Sum 7 " + a + " Shape type",
		b + " Sum 7 " + a + " Sq type",
		b + " Sum 7 " + b + " local type",
		// several init functions are told apart by line
		b + " init 16 " + b + " Sum func",
		// a package in a testdata dir is loaded too
		"pkg/a/testdata/helper.go Helper 6 " + a + " Max const",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %q", w)
		}
	}
	for e := range got {
		// no stdlib targets, no local types, no type parameters, no test files
		for _, bad := range []string{" Itoa ", " inner ", "sizer", "Size", ".Len", " T type", " U type", "_test.go"} {
			if strings.Contains(e, bad) {
				t.Errorf("unexpected edge %q", e)
			}
		}
	}
	if !g.funcs[funcKey{b, "init", 14}] || !g.funcs[funcKey{b, "init", 16}] {
		t.Errorf("init functions not found: %v", g.funcs)
	}
	if g.funcs[funcKey{"pkg/b/b_windows.go", "OnlyWindows", 3}] {
		t.Errorf("b_windows.go should be excluded by its build constraint")
	}
}

func TestFileRefs(t *testing.T) {
	_, g := loadFixture(t)
	rows := fileRefRows(g.edges)
	byPair := map[string]string{}
	for _, r := range rows {
		if r[0] == r[1] {
			t.Errorf("self edge %v", r)
		}
		byPair[r[0]+" "+r[1]] = strings.Join(r[2:], " ")
	}
	// Sum -> Total (func), Shape and Sq (type)
	if got := byPair["pkg/b/b.go pkg/a/a.go"]; got != "1 0 2 0" {
		t.Errorf("b.go -> a.go = %q, want funcs=1 ifaces=0 types=2 vars_consts=0", got)
	}
	if got := byPair["pkg/a/a.go pkg/sub/sub.go"]; got != "1 0 0 0" {
		t.Errorf("a.go -> sub.go = %q", got)
	}
	if len(rows) != 3 { // + testdata/helper.go -> a.go
		t.Errorf("got %d file edges, want 3: %v", len(rows), rows)
	}
}

func TestScanDecls(t *testing.T) {
	root, err := filepath.Abs("testdata/fixture")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := scanDecls(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range declRows(ds) {
		got[strings.Join(r, " ")] = true
	}
	for _, w := range []string{
		"pkg/a pkg/a/a.go Shape type 9 9 1",
		"pkg/a pkg/a/a.go Named type 11 14 4",
		"pkg/a pkg/a/a.go Max const 21 21 1",
		"pkg/a pkg/a/a.go Min const 22 22 1",
		"pkg/a pkg/a/a.go Default var 25 25 1",
		"pkg/a pkg/a/a.go List type 27 27 1",
		"pkg/b pkg/b/b.go local type 5 5 1",
	} {
		if !got[w] {
			t.Errorf("missing decl %q", w)
		}
	}
	if len(got) != 8 { // + Sq, and nothing from functions or test files
		t.Errorf("got %d decls, want 8: %v", len(got), got)
	}
}
