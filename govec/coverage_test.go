package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
)

// goFunc is one row of data/func_blocks.tsv.
type goFunc struct{ file, sym, block string }

// key is the canonical "<file>:<Symbol>" name of a Go function.
func (f goFunc) key() string { return f.file + ":" + f.sym }

func loadFuncBlocks(t *testing.T) []goFunc {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "data", "func_blocks.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []goFunc
	sc := bufio.NewScanner(f)
	sc.Scan() // header: pkg file symbol start_line block
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) != 5 {
			t.Fatalf("func_blocks.tsv: bad line %q", sc.Text())
		}
		out = append(out, goFunc{file: c[1], sym: c[2], block: c[4]})
	}
	return out
}

// loadBlocksDone reads data/blocks_done.tsv: the porting blocks whose
// convergence is closed (every function ported, or exempt for good / owned by
// another block). See the README "Blocks done".
func loadBlocksDone(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "data", "blocks_done.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	done := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Scan() // header: block converge evidence
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) != 3 || !blockIDRe.MatchString(c[0]) || c[1] == "" || c[2] == "" {
			t.Fatalf("blocks_done.tsv: bad line %q", sc.Text())
		}
		done[c[0]] = true
	}
	return done
}

var (
	// blockIDRe: a porting block ID (D08.1, D00.12). Categories such as EXC.B, X
	// or DEF.MPP are not porting blocks.
	blockIDRe = regexp.MustCompile(`\bD\d{2}\.\d+\b`)
	// citation: "<block> spec §n" / "<block> plan §n" cites a design decision of
	// the block, not work of the block.
	citationRe = regexp.MustCompile(`^ (spec|plan)\b`)
	// partRe: "part 2b", "batch 2" — a later part of some block. Allowed only
	// right after a block ID ("D08.1 part 2a"), which the other rules then judge.
	partRe        = regexp.MustCompile(`(?i)\b(part|batch)\s+\d+[a-z]?\b`)
	partAfterIDRe = regexp.MustCompile(`\bD\d{2}\.\d+\s+$`)
)

// reasonProblems checks an exemption reason of a function of block own:
//   - once own is done (data/blocks_done.tsv), the reason must not point at own
//     ("D08.1 part 2b" inside D08.1, "D00.1 T9" inside D00.1, a bare "part 2" or
//     "batch 2"): a block cannot be closed with work parked on itself — port it,
//     exempt it for good, or move it to the block that owns it (and list it in
//     that block's tasks.md). Blocks in progress may park work on a later part;
//   - it must not point at a block that is done: "waits for D08.8" after D08.8
//     closed is stale.
//
// Mentions followed by " spec" or " plan" cite a design decision of that
// block's spec / plan and are allowed.
func reasonProblems(own, why string, done map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range blockIDRe.FindAllStringIndex(why, -1) {
		id := why[m[0]:m[1]]
		if citationRe.MatchString(why[m[1]:]) || seen[id] {
			continue
		}
		seen[id] = true
		if id == own && done[own] {
			out = append(out, "refers to its own block "+id+" (port it, exempt it for good, or move it to the block that owns it)")
		} else if done[id] {
			out = append(out, "refers to block "+id+", which is done (data/blocks_done.tsv): stale")
		}
	}
	for _, m := range partRe.FindAllStringIndex(why, -1) {
		if !done[own] {
			break
		}
		if !partAfterIDRe.MatchString(why[:m[0]]) {
			out = append(out, "refers to "+why[m[0]:m[1]]+" without a block ID (a later part of its own block?)")
		}
	}
	return out
}

func TestReasonProblems(t *testing.T) {
	done := map[string]bool{"D08.8": true, "D08.1": true, "D00.1": true, "D08.12": true, "D08.2": true, "D15.1": true}
	for _, c := range []struct {
		own, why string
		bad      bool
	}{
		{"D08.1", "D08.1 part 2b (evaluation entry points)", true},
		{"D00.1", "waits for the session (D05.3) and the swap of D05.2 (D00.1 T9)", true},
		{"D08.12", "D08.12 batch 2 (EXPLAIN printing), specs/D08.12-util/plan.md", true},
		{"D08.1", "vectorized evaluation: replaced by the vector driver of part 2", true},
		{"D08.2", "D08.2 part 2: the CAST signatures' base (D08.8)", true},
		{"D08.2", "the CAST signatures' base (D08.8)", true},
		{"D08.12", "reads CorrelatedColumn.Data (D08.1 spec §3.2)", false},
		{"D08.12", "tidb-rs does not support extensions (D08.2 spec §4)", false},
		{"D08.12", "inherited by D10.9 (EXPLAIN output)", false},
		{"D08.2", "covered by the expr domain (sf records, D08.1 part 2a)", true}, // D08.1 is done
		{"D08.2", "owned by D08.6: REGEXP helpers; listed in specs/D08.6-string-funcs/tasks.md", false},
		{"D08.1", "cascades only (EXC.B)", false},
		{"D15.1", "released at once in Rust (D15.1 plan §4)", false},
		{"D06.2", "part 2: pkg/meta/autoid", false}, // D06.2 is in progress
		{"D06.2", "D06.2 part 2: IterAllTables", false},
		{"D06.2", "waits for D08.8", true},
	} {
		if got := len(reasonProblems(c.own, c.why, done)) > 0; got != c.bad {
			t.Errorf("reasonProblems(%s, %q) = %v, want bad=%v", c.own, c.why, reasonProblems(c.own, c.why, done), c.bad)
		}
	}
}

// TestBlockCoverage: every function of the porting blocks listed by the domains
// (tracker data/func_blocks.tsv) is covered by some op's GoFuncs or listed in
// Exempt with a reason, in any of the domains that list the block (a block may
// be split over several domains, e.g. D00.2 over "base" and "config"); every
// GoFuncs/Exempt symbol exists in the domain's blocks.
//
// Functions are identified by (file, symbol): a symbol that names functions of
// several files of the domain's blocks (sessionexpr's and exprstatic's
// EvalContext.AppendNote, the init of each file) must be written
// "<file>:<Symbol>"; a bare ambiguous symbol is an error, so covering one file's
// function cannot hide the other's.
//
// Exemption reasons (reasonProblems) must not point at the function's own
// block nor at a block that is done, and a function some domain covers must
// not also be exempt.
func TestBlockCoverage(t *testing.T) {
	all := loadFuncBlocks(t)
	done := loadBlocksDone(t)
	blockFuncs := map[string][]goFunc{}            // block -> functions
	fileFuncs := map[string][]goFunc{}             // file -> functions
	symInBlock := map[string]map[string][]goFunc{} // block -> symbol -> functions
	byKey := map[string]goFunc{}
	for _, f := range all {
		blockFuncs[f.block] = append(blockFuncs[f.block], f)
		fileFuncs[f.file] = append(fileFuncs[f.file], f)
		if symInBlock[f.block] == nil {
			symInBlock[f.block] = map[string][]goFunc{}
		}
		symInBlock[f.block][f.sym] = append(symInBlock[f.block][f.sym], f)
		byKey[f.key()] = f
	}
	listed := map[string]bool{}      // blocks some domain lists
	covered := map[string][]string{} // key -> domains whose ops cover it
	type exemptEntry struct{ domain, why string }
	exempt := map[string][]exemptEntry{} // key -> domains that exempt it
	for _, name := range vec.Names() {
		d := vec.Lookup(name)
		if len(d.Blocks) == 0 {
			t.Errorf("domain %s: no Blocks", name)
		}
		inFiles := map[string]bool{}
		for _, b := range d.Blocks {
			if len(blockFuncs[b]) == 0 {
				t.Errorf("domain %s: block %s has no functions in func_blocks.tsv", name, b)
			}
			listed[b] = true
		}
		for _, file := range d.Files {
			if len(fileFuncs[file]) == 0 {
				t.Errorf("domain %s: file %s has no functions in func_blocks.tsv", name, file)
			}
			inFiles[file] = true
		}
		// resolve maps a GoFuncs/Exempt name to its function key.
		resolve := func(what, n string) (string, bool) {
			if file, _, ok := strings.Cut(n, ":"); ok {
				f, found := byKey[n]
				if !found || !(inFiles[file] || containsStr(d.Blocks, f.block)) {
					t.Errorf("domain %s: %s %q is not a function of blocks %v or files %v", name, what, n, d.Blocks, d.Files)
					return "", false
				}
				return n, true
			}
			var cands []goFunc
			for _, b := range d.Blocks {
				cands = append(cands, symInBlock[b][n]...)
			}
			switch len(cands) {
			case 0:
				t.Errorf("domain %s: %s %q is not a function of blocks %v", name, what, n, d.Blocks)
				return "", false
			case 1:
				return cands[0].key(), true
			}
			var files []string
			for _, c := range cands {
				files = append(files, c.file+" ("+c.block+")")
			}
			t.Errorf("domain %s: %s %q is ambiguous, functions of %s: write \"<file>:%s\"", name, what, n, strings.Join(files, ", "), n)
			return "", false
		}
		domCovered := map[string]bool{}
		for _, op := range sortedKeys(d.Coverage) {
			for _, fn := range d.Coverage[op].GoFuncs {
				if k, ok := resolve("op "+op+" GoFuncs", fn); ok {
					domCovered[k] = true
				}
			}
		}
		domExempt := map[string]bool{}
		for _, fn := range sortedKeys(d.Exempt) {
			why := d.Exempt[fn]
			k, ok := resolve("exempt", fn)
			if !ok {
				continue
			}
			if domExempt[k] {
				t.Errorf("domain %s: %s is exempt twice", name, k)
			}
			domExempt[k] = true
			if domCovered[k] {
				t.Errorf("domain %s: %q is both covered and exempt", name, fn)
			}
			if why == "" {
				t.Errorf("domain %s: exempt %q has no reason", name, fn)
			}
			for _, p := range reasonProblems(byKey[k].block, why, done) {
				t.Errorf("domain %s: exempt %s (block %s): reason %s: %q", name, fn, byKey[k].block, p, why)
			}
			exempt[k] = append(exempt[k], exemptEntry{name, why})
		}
		for _, file := range d.Files {
			for _, f := range fileFuncs[file] {
				if !domCovered[f.key()] && !domExempt[f.key()] {
					t.Errorf("domain %s: %s of file %s is neither covered by an op nor exempt", name, f.sym, file)
				}
			}
		}
		for k := range domCovered {
			if containsStr(d.Blocks, byKey[k].block) {
				covered[k] = append(covered[k], name)
			}
		}
		t.Logf("domain %s: %d functions of %v covered, %d exempt", name, len(domCovered), d.Blocks, len(d.Exempt))
	}
	for b := range listed {
		for _, f := range blockFuncs[b] {
			k := f.key()
			switch {
			case len(covered[k]) == 0 && len(exempt[k]) == 0:
				t.Errorf("block %s: %s (%s) is neither covered by an op nor exempt in any domain", b, f.sym, f.file)
			case len(covered[k]) > 0:
				for _, e := range exempt[k] {
					if !containsStr(vec.Lookup(e.domain).Files, f.file) {
						t.Errorf("block %s: %s (%s) is covered by domain %v and exempt in domain %s: drop the exemption",
							b, f.sym, f.file, covered[k], e.domain)
					}
				}
			}
		}
	}
	for b := range done {
		if len(blockFuncs[b]) == 0 {
			t.Errorf("blocks_done.tsv: block %s has no functions in func_blocks.tsv", b)
		}
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
