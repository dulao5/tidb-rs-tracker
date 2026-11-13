package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
)

// ownedByRe: an exemption reason that hands the function to another porting
// block ("owned by D09.2: …").
var ownedByRe = regexp.MustCompile(`\bowned by (D\d{2}\.\d+)\b`)

// inheritedHeading is the tasks.md section that lists the work a block took
// over from other blocks (written by the P1 close-out, #48).
const inheritedHeading = "## 从 P1 继承"

// tidbRsDir is the tidb-rs checkout whose specs/<block>/tasks.md the re-homed
// functions must be listed in: $TIDB_RS_DIR, else the sibling checkout
// ../../tidb-rs (relative to govec/).
func tidbRsDir() string {
	if d := os.Getenv("TIDB_RS_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "tidb-rs")
}

// inheritedSection returns the "从 P1 继承" section of specs/<block>-*/tasks.md
// (up to the next "## " heading), or a problem.
func inheritedSection(root, block string) (string, string) {
	dirs, _ := filepath.Glob(filepath.Join(root, "specs", block+"-*"))
	if len(dirs) != 1 {
		return "", "no unique directory specs/" + block + "-* in tidb-rs"
	}
	path := filepath.Join(dirs[0], "tasks.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "cannot read " + path + ": " + err.Error()
	}
	s := string(b)
	i := strings.Index(s, "\n"+inheritedHeading+"\n")
	if i < 0 {
		return "", path + " has no \"" + inheritedHeading + "\" section"
	}
	s = s[i+1+len(inheritedHeading):]
	if j := strings.Index(s, "\n## "); j >= 0 {
		s = s[:j]
	}
	return s, ""
}

// rehomeMissing returns the problems of one exemption: its reason says
// "owned by <B>" and its symbol must appear, in backticks, in the "从 P1 继承"
// section of B's tasks.md. section caches the sections by block.
func rehomeMissing(fn, why string, section func(string) (string, string)) []string {
	var out []string
	sym := fn
	if _, s, ok := strings.Cut(fn, ":"); ok {
		sym = s
	}
	for _, m := range ownedByRe.FindAllStringSubmatch(why, -1) {
		sec, problem := section(m[1])
		switch {
		case problem != "":
			out = append(out, problem)
		case !strings.Contains(sec, "`"+sym+"`"):
			out = append(out, "`"+sym+"` is not listed in the \""+inheritedHeading+"\" section of "+m[1]+"'s tasks.md")
		}
	}
	return out
}

func TestRehomeMissing(t *testing.T) {
	secs := map[string]string{
		"D09.2": "\n\n- [ ] P1-1 （D08.1）`BuildOption`：`WithTableInfo`、`NewValuesFunc`\n",
	}
	section := func(b string) (string, string) {
		if s, ok := secs[b]; ok {
			return s, ""
		}
		return "", "no unique directory specs/" + b + "-* in tidb-rs"
	}
	for _, c := range []struct {
		fn, why string
		bad     bool
	}{
		{"WithTableInfo", "owned by D09.2: a BuildOption", false},
		{"expression.go:NewValuesFunc", "owned by D09.2: the rewriter's ValuesExpr case", false},
		{"WithCastExprTo", "owned by D09.2: a BuildOption", true}, // not listed
		{"Values", "owned by D09.2", true},                        // `Values` is not `NewValuesFunc`
		{"GetTimeValue", "owned by D06.4: column defaults", true}, // no tasks.md
		{"Log", "logs an error (pingcap/log); no result", false},  // not re-homed
		{"WithTableInfo", "waits for D09.2 (owned by D99.9 later)", true},
	} {
		if got := len(rehomeMissing(c.fn, c.why, section)) > 0; got != c.bad {
			t.Errorf("rehomeMissing(%s, %q) = %v, want bad=%v", c.fn, c.why, rehomeMissing(c.fn, c.why, section), c.bad)
		}
	}
}

// TestRehomedListedInTasks: every exempt function whose reason says "owned by
// <block>" is listed in the "从 P1 继承" section of that block's
// specs/<block>-*/tasks.md in tidb-rs, so a hand-off cannot skip the receiving
// block's task list (Lucky, review of tracker#68 / tidb-rs#163). It needs a
// tidb-rs checkout (tidbRsDir); without one it is skipped, except in CI.
func TestRehomedListedInTasks(t *testing.T) {
	root := tidbRsDir()
	if _, err := os.Stat(filepath.Join(root, "specs")); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("no tidb-rs checkout at %s (set TIDB_RS_DIR): %v", root, err)
		}
		t.Skipf("no tidb-rs checkout at %s (set TIDB_RS_DIR)", root)
	}
	type sec struct{ text, problem string }
	cache := map[string]sec{}
	section := func(b string) (string, string) {
		s, ok := cache[b]
		if !ok {
			s.text, s.problem = inheritedSection(root, b)
			cache[b] = s
		}
		return s.text, s.problem
	}
	n := 0
	for _, name := range vec.Names() {
		d := vec.Lookup(name)
		for _, fn := range sortedKeys(d.Exempt) {
			ps := rehomeMissing(fn, d.Exempt[fn], section)
			if ownedByRe.MatchString(d.Exempt[fn]) {
				n++
			}
			for _, p := range ps {
				t.Errorf("domain %s: exempt %s: %s", name, fn, p)
			}
		}
	}
	blocks := make([]string, 0, len(cache))
	for b := range cache {
		blocks = append(blocks, b)
	}
	sort.Strings(blocks)
	t.Logf("%d re-homed exemptions checked against tasks.md of %v (%s)", n, blocks, root)
}
