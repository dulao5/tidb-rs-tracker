// Command covmap maps each Go test of one TiDB package to the non-test
// functions it covers, net of the coverage produced by package
// initialization alone.
//
// It builds the test binary once with -coverpkg, runs it with an empty
// filter to get the baseline, then once per test. Output is TSV:
//
//	<test pkg dir> <test name> <func file> <func name>
//
// With -status, the outcome of each test's run is written as TSV too:
//
//	<test pkg dir> <test name> <status> <detail>
//
// status is ok (the test passed; its mapping is complete, possibly empty),
// failed (the test failed, panicked or timed out; its mapping is a lower
// bound) or skipped (t.Skip, or not compiled in with these build tags).
//
// Usage: covmap -repo <tidb root> -pkg pkg/tablecodec [-coverpkg ./pkg/...] [-tests A,B] [-status f] [-modfile f]
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const modulePrefix = "github.com/pingcap/tidb/"

// The parameter may have any name or none: func TestX(tt *testing.T), func TestX(*testing.T).
var testFuncRE = regexp.MustCompile(`(?m)^func (Test[A-Z_0-9]\w*)\((?:\w+ )?\*testing\.T\)`)

func main() {
	repo := flag.String("repo", "", "tidb repository root")
	pkg := flag.String("pkg", "", "package dir relative to repo root")
	coverpkg := flag.String("coverpkg", "./pkg/...", "-coverpkg pattern")
	only := flag.String("tests", "", "comma separated test names (default: all in package)")
	tags := flag.String("tags", "intest,deadlock", "build tags (TiDB testkit requires intest)")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-test timeout")
	statusPath := flag.String("status", "", "write each test's run outcome to this TSV file")
	modfile := flag.String("modfile", "", "alternate go.mod for the root module (go -modfile); not used for nested modules")
	flag.Parse()
	if *repo == "" || *pkg == "" {
		log.Fatal("-repo and -pkg are required")
	}

	tmp, err := os.MkdirTemp("", "covmap")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	bin := filepath.Join(tmp, "test.bin")
	// Packages under a nested go.mod (e.g. pkg/parser) must be built from
	// that module's root, covering that module only.
	modDir, target, cover := *repo, "./"+*pkg, *coverpkg
	if sub := nestedModule(*repo, *pkg); sub != "" {
		modDir = filepath.Join(*repo, sub)
		rel, _ := filepath.Rel(sub, *pkg)
		target, cover = "./"+rel, "./..."
	}
	args := []string{"test", "-c", "-o", bin, "-tags=" + *tags, "-cover", "-covermode=set", "-coverpkg=" + cover}
	if *modfile != "" && modDir == *repo {
		args = append(args, "-modfile="+*modfile)
	}
	build := exec.Command("go", append(args, target)...)
	build.Dir = modDir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("build test binary: %v", err)
	}

	tests := strings.FieldsFunc(*only, func(r rune) bool { return r == ',' })
	if len(tests) == 0 {
		if tests, err = listTests(filepath.Join(*repo, *pkg)); err != nil {
			log.Fatal(err)
		}
	}

	pkgDir := filepath.Join(*repo, *pkg)
	baseline, _, err := run(bin, pkgDir, tmp, "^$", *timeout)
	if err != nil {
		// Some packages fail even with no tests selected (e.g. goleak in
		// TestMain flags a goroutine that only the empty run leaves behind).
		// The profile is still usable, so only a missing profile is fatal.
		if baseline == nil {
			log.Fatalf("baseline run: %v", err)
		}
		log.Printf("baseline run (profile kept): %v", err)
	}

	var sw *bufio.Writer
	if *statusPath != "" {
		f, err := os.Create(*statusPath)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		sw = bufio.NewWriter(f)
		defer sw.Flush()
	}

	idx := newFuncIndex(*repo)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, t := range tests {
		start := time.Now()
		blocks, out, err := run(bin, pkgDir, tmp, "^"+t+"$", *timeout)
		if err != nil {
			// Keep going: a failing test still tells us what it touched, but
			// flag it so it is not mistaken for a clean mapping.
			log.Printf("%s: %v", t, err)
		}
		status, detail := outcome(t, out, blocks != nil, err)
		if status == "skipped" {
			detail = clean(skipComment(pkgDir, detail))
		}
		funcs := map[string]struct{}{}
		for b := range blocks {
			if _, ok := baseline[b]; ok {
				continue
			}
			if f := idx.lookup(b); f != "" {
				funcs[f] = struct{}{}
			}
		}
		keys := make([]string, 0, len(funcs))
		for f := range funcs {
			keys = append(keys, f)
		}
		sort.Strings(keys)
		for _, f := range keys {
			fmt.Fprintf(w, "%s\t%s\t%s\n", *pkg, t, f)
		}
		log.Printf("%s: %d funcs in %s", t, len(keys), time.Since(start).Round(time.Millisecond))
		if status == "ok" && len(keys) == 0 && detail == "" {
			detail = "no functions reached beyond package init"
		}
		log.Printf("%s: status %s %s", t, status, detail)
		if sw != nil {
			fmt.Fprintf(sw, "%s\t%s\t%s\t%s\n", *pkg, t, status, detail)
		}
	}
}

// nestedModule returns the repo-relative dir of the closest go.mod strictly
// below the repo root that contains pkg, or "" if pkg is in the root module.
func nestedModule(repo, pkg string) string {
	for d := pkg; d != "." && d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(repo, d, "go.mod")); err == nil {
			return d
		}
	}
	return ""
}

func listTests(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range testFuncRE.FindAllSubmatch(src, -1) {
			out = append(out, string(m[1]))
		}
	}
	sort.Strings(out)
	return out, nil
}

// block identifies a coverage block: "<file rel to repo>:<startLine>".
type block struct {
	file string
	line int
}

// run runs the test binary with a -test.run filter. It returns the covered
// blocks (nil if no profile was written), the combined -test.v output, and
// the run error, if any.
func run(bin, dir, tmp, filter string, timeout time.Duration) (map[block]struct{}, []byte, error) {
	prof := filepath.Join(tmp, "cover.out")
	_ = os.Remove(prof)
	cmd := exec.Command(bin, "-test.run", filter, "-test.count=1", "-test.v",
		"-test.timeout", timeout.String(), "-test.coverprofile", prof)
	cmd.Dir = dir // tests read testdata relative to the package dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	blocks, err := readProfile(prof)
	if err != nil {
		if runErr != nil {
			err = fmt.Errorf("%v (no coverage profile: %v)", runErr, err)
		}
		return nil, out.Bytes(), err
	}
	return blocks, out.Bytes(), runErr
}

// outcome classifies one test run from its -test.v output.
func outcome(test string, out []byte, haveProfile bool, runErr error) (status, detail string) {
	var result, msg, panicMsg string
	lines := strings.Split(string(out), "\n")
	runAt := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "=== RUN   "+test) && strings.TrimSpace(l[len("=== RUN   "):]) == test {
			runAt = i
		}
		for _, r := range []string{"PASS", "FAIL", "SKIP"} {
			if strings.HasPrefix(l, "--- "+r+": "+test+" ") {
				result = r
				msg = testMessage(lines[runAt:i], r == "SKIP")
			}
		}
		if panicMsg == "" && strings.HasPrefix(l, "panic: ") {
			panicMsg = l
		}
	}
	exit := ""
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exit = ee.String()
		} else {
			exit = runErr.Error()
		}
	}
	noProfile := ""
	if !haveProfile {
		noProfile = "no coverage profile; "
	}
	switch {
	case strings.HasPrefix(panicMsg, "panic: test timed out"):
		return "failed", clean(noProfile + "timeout: " + strings.TrimPrefix(panicMsg, "panic: "))
	case result == "SKIP":
		return "skipped", clean(msg)
	case result == "FAIL":
		if panicMsg != "" {
			msg = panicMsg
		}
		return "failed", clean(noProfile + exit + ": " + msg)
	case result == "" && panicMsg != "":
		return "failed", clean(noProfile + exit + ": " + panicMsg)
	case result == "" && runErr == nil && bytes.Contains(out, []byte("no tests to run")):
		return "skipped", "not compiled in (build tags)"
	case result == "":
		return "failed", clean(noProfile + exit + ": no test result")
	case !haveProfile:
		return "failed", clean(noProfile + exit)
	case runErr != nil:
		// The test passed but the process failed afterwards (e.g. a goleak
		// check in TestMain). The profile was written by then, so the
		// mapping is complete.
		return "ok", clean("test passed; process " + exit + " after it")
	}
	return "ok", ""
}

var bareSkipRE = regexp.MustCompile(`^(\S+\.go):(\d+):$`)

// skipComment adds the source comment above a t.Skip() that printed no
// message ("x_test.go:82:"), so the skip still has a reason.
func skipComment(dir, msg string) string {
	m := bareSkipRE.FindStringSubmatch(msg)
	if m == nil {
		return msg
	}
	src, err := os.ReadFile(filepath.Join(dir, m[1]))
	if err != nil {
		return msg
	}
	lines := strings.Split(string(src), "\n")
	n, _ := strconv.Atoi(m[2])
	var comment []string
	for i := n - 2; i >= 0 && i >= n-6; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "//") {
			if len(comment) > 0 || !strings.HasPrefix(l, "if ") {
				break
			}
			continue // the condition guarding the skip
		}
		comment = append([]string{strings.TrimSpace(strings.TrimPrefix(l, "//"))}, comment...)
	}
	if len(comment) == 0 {
		return msg
	}
	return msg + " (t.Skip() without message; comment: " + strings.Join(comment, " ") + ")"
}

var logLineRE = regexp.MustCompile(`^\s+\S+\.go:\d+: `)

// testMessage picks the reason from the log lines a test printed before its
// result line: the last t.Log/t.Skip line for a skip, the first testify
// "Error:" (or else the first t.Error line) for a failure.
func testMessage(lines []string, skip bool) string {
	msg := ""
	for i, l := range lines {
		if !logLineRE.MatchString(l) {
			continue
		}
		m := strings.TrimSpace(l)
		if skip {
			msg = m
			continue
		}
		next := lines[i+1 : min(i+12, len(lines))]
		for j, n := range next {
			if t := strings.TrimSpace(n); strings.HasPrefix(t, "Error:") {
				// "Error: Not equal:" is followed by expected / actual lines.
				for _, x := range next[j+1 : min(j+3, len(next))] {
					if x = strings.TrimSpace(x); x != "" && !strings.HasPrefix(x, "Test:") {
						t += " " + x
					}
				}
				return m + " " + t
			}
		}
		if msg == "" {
			msg = m
		}
	}
	return msg
}

// clean makes s fit one TSV field.
func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func readProfile(path string) (map[block]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[block]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// github.com/pingcap/tidb/pkg/x/y.go:12.34,15.2 3 1
		l := sc.Text()
		if strings.HasPrefix(l, "mode:") || !strings.HasSuffix(l, " 1") {
			continue
		}
		colon := strings.LastIndexByte(l, ':')
		if colon < 0 {
			continue
		}
		dot := strings.IndexByte(l[colon:], '.')
		if dot < 0 {
			continue
		}
		line, err := strconv.Atoi(l[colon+1 : colon+dot])
		if err != nil {
			continue
		}
		out[block{strings.TrimPrefix(l[:colon], modulePrefix), line}] = struct{}{}
	}
	return out, sc.Err()
}

type span struct {
	start, end int
	name       string
}

// funcIndex lazily parses source files to resolve a line to its enclosing
// function, named "Func" or "Recv.Method".
type funcIndex struct {
	repo  string
	files map[string][]span
}

func newFuncIndex(repo string) *funcIndex {
	return &funcIndex{repo: repo, files: map[string][]span{}}
}

func (x *funcIndex) lookup(b block) string {
	spans, ok := x.files[b.file]
	if !ok {
		spans = parseSpans(filepath.Join(x.repo, b.file))
		x.files[b.file] = spans
	}
	for _, s := range spans {
		if b.line >= s.start && b.line <= s.end {
			return b.file + "\t" + s.name
		}
	}
	return ""
}

func parseSpans(path string) []span {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out []span
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			name = recvName(fn.Recv.List[0].Type) + "." + name
		}
		out = append(out, span{fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line, name})
	}
	return out
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
