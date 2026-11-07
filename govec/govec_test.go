package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dulao5/tidb-rs-tracker/govec/domains"
	"github.com/dulao5/tidb-rs-tracker/govec/vec"
)

// minedGoTests lists, per domain, the Go test functions whose case tables were
// copied into the generator. Each must appear as a record src.
var minedGoTests = map[string][]string{
	"decimal": {
		"TestFromInt", "TestFromUint", "TestToInt", "TestToUint", "TestFromFloat", "TestToFloat",
		"TestToHashKey", "TestRemoveTrailingZeros", "TestRoundWithHalfEven", "TestRoundWithTruncate",
		"TestRoundWithCeil", "TestToString", "TestToBinFromBin", "TestDecimalBinSize",
		"TestCompareMyDecimal", "TestMaxDecimal", "TestNegMyDecimal", "TestAddMyDecimal",
		"TestSubMyDecimal", "TestMulMyDecimal", "TestDivModMyDecimal", "TestMaxOrMinMyDecimal",
		"TestShiftMyDecimal", "TestFromStringMyDecimal", "TestMarshalMyDecimal",
		"BenchmarkMyDecimalToBin", "BenchmarkMyDecimalToHashKey",
	},
	"memory": {
		"TestSetLabel", "TestSetLabel2", "TestConsume", "TestRelease", "TestBufferedConsumeAndRelease",
		"TestOOMAction", "TestAttachTo", "TestDetach", "TestReplaceChild", "TestToString", "TestMaxConsumed",
		"TestGlobalTracker", "TestOOMActionPriority", "TestFormatBytesWithPrune",
	},
}

// oracleStride: the oracle check replays records 0, s, 2s, ... (about 1 in
// 37, ~3% of the records). A prime, so the sample does not lock onto the
// period of a generator loop that emits a fixed number of ops per input.
const oracleStride = 37

func generate(t *testing.T, d *vec.Domain) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := d.WriteAll(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBlockCoverage: every function of the porting blocks listed by the domains
// (tracker data/func_blocks.tsv) is covered by some op's GoFuncs or listed in
// Exempt with a reason, in any of the domains that list the block (a block may
// be split over several domains, e.g. D00.2 over "base" and "config"); every
// GoFuncs/Exempt symbol exists in the domain's blocks.
func TestBlockCoverage(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "data", "func_blocks.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	blockFuncs := map[string][]string{} // block -> symbols
	sc := bufio.NewScanner(f)
	sc.Scan() // header: pkg file symbol start_line block
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) != 5 {
			t.Fatalf("func_blocks.tsv: bad line %q", sc.Text())
		}
		blockFuncs[c[4]] = append(blockFuncs[c[4]], c[2])
	}
	covered := map[string]map[string]bool{} // block -> symbol -> covered by some domain
	exempt := map[string]map[string]bool{}  // block -> symbol -> exempt in some domain
	for _, name := range vec.Names() {
		d := vec.Lookup(name)
		if len(d.Blocks) == 0 {
			t.Errorf("domain %s: no Blocks", name)
		}
		inBlocks := map[string]bool{}
		for _, b := range d.Blocks {
			if len(blockFuncs[b]) == 0 {
				t.Errorf("domain %s: block %s has no functions in func_blocks.tsv", name, b)
			}
			for _, fn := range blockFuncs[b] {
				inBlocks[fn] = true
			}
			if covered[b] == nil {
				covered[b], exempt[b] = map[string]bool{}, map[string]bool{}
			}
		}
		domCovered := map[string]bool{}
		for op, c := range d.Coverage {
			for _, fn := range c.GoFuncs {
				domCovered[fn] = true
				if !inBlocks[fn] {
					t.Errorf("domain %s op %s: GoFuncs %q is not a function of blocks %v", name, op, fn, d.Blocks)
				}
			}
		}
		for fn, why := range d.Exempt {
			if !inBlocks[fn] {
				t.Errorf("domain %s: exempt %q is not a function of blocks %v", name, fn, d.Blocks)
			}
			if domCovered[fn] {
				t.Errorf("domain %s: %q is both covered and exempt", name, fn)
			}
			if why == "" {
				t.Errorf("domain %s: exempt %q has no reason", name, fn)
			}
		}
		for _, b := range d.Blocks {
			for _, fn := range blockFuncs[b] {
				if domCovered[fn] {
					covered[b][fn] = true
				}
				if d.Exempt[fn] != "" {
					exempt[b][fn] = true
				}
			}
		}
		t.Logf("domain %s: %d functions of %v covered, %d exempt", name, len(domCovered), d.Blocks, len(d.Exempt))
	}
	for b, cov := range covered {
		for _, fn := range blockFuncs[b] {
			if !cov[fn] && !exempt[b][fn] {
				t.Errorf("block %s: %s is neither covered by an op nor exempt in any domain", b, fn)
			}
		}
	}
}

func TestDomains(t *testing.T) {
	for _, name := range vec.Names() {
		t.Run(name, func(t *testing.T) { checkDomain(t, vec.Lookup(name)) })
	}
}

func checkDomain(t *testing.T, d *vec.Domain) {
	out1 := generate(t, d)
	out2 := generate(t, d)
	if !bytes.Equal(out1, out2) {
		t.Fatal("generation is not deterministic")
	}

	sc := bufio.NewScanner(bytes.NewReader(out1))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	if !sc.Scan() {
		t.Fatal("empty output")
	}
	var h vec.Header
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
		t.Fatalf("header: %v", err)
	}
	if h.Govec != vec.FormatVersion || h.Domain != d.Name || h.TiDB != vec.TiDBVersion ||
		h.Commit != vec.TiDBCommit || h.Go == "" || h.Seed != d.Seed {
		t.Fatalf("bad header %+v", h)
	}

	counts := map[string]int{}
	srcs := map[string]bool{}
	var recs []vec.Record
	for sc.Scan() {
		var r vec.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("record %d: %v", len(recs)+1, err)
		}
		if _, ok := d.Coverage[r.Op]; !ok {
			t.Errorf("op %q not in coverage map", r.Op)
		}
		counts[r.Op]++
		srcs[r.Src] = true
		recs = append(recs, r)
	}
	if h.Records != len(recs) {
		t.Errorf("header says %d records, got %d", h.Records, len(recs))
	}
	for op, c := range d.Coverage {
		if counts[op] < c.Min {
			t.Errorf("op %q: %d records, want >= %d", op, counts[op], c.Min)
		}
	}
	for _, fn := range minedGoTests[d.Name] {
		if !srcs[fn] {
			t.Errorf("mined Go test %s has no records", fn)
		}
	}

	// Oracle mode must reproduce records from (op, args) alone. Replaying all
	// records would double the test time, so replay a sparse sample: every
	// oracleStride-th record plus the first record of each op, so that every
	// op goes through the oracle path.
	var in bytes.Buffer
	var idx []int
	seen := map[string]bool{}
	for i, r := range recs {
		if i%oracleStride == 0 || !seen[r.Op] {
			seen[r.Op] = true
			idx = append(idx, i)
			in.Write(vec.MustJSON(vec.Request{Op: r.Op, Args: r.Args}))
			in.WriteByte('\n')
		}
	}
	var out bytes.Buffer
	if err := d.Oracle(&in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(idx) {
		t.Fatalf("oracle: %d lines for %d requests", len(lines), len(idx))
	}
	for j, line := range lines {
		var got vec.Record
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatal(err)
		}
		want := recs[idx[j]]
		got.Src = want.Src
		if g, w := string(vec.MustJSON(got)), string(vec.MustJSON(want)); g != w {
			t.Fatalf("oracle mismatch:\n got %s\nwant %s", g, w)
		}
	}

	// Committed vectors of this target must match (only with the pinned
	// toolchain, since the header records the Go version; goos is not
	// compared).
	path := filepath.Join("..", "vectors", vec.TiDBVersion, d.FilePath(vec.Target()))
	committed, err := vec.ReadFile(path)
	if os.IsNotExist(err) {
		t.Logf("%s not committed yet", path)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !vec.SameExceptGOOS(committed, out1) {
		t.Errorf("%s is stale (go %s); run scripts/govec.sh", path, runtime.Version())
	}
}

// TestStorage: the gzip form (README "Storage") is deterministic and reads
// back to the plain JSONL; the committed-file comparison ignores only the
// header's goos.
func TestStorage(t *testing.T) {
	d := *vec.Lookup("decimal")
	plain := generate(t, &d)
	d.Gzip = true
	if d.FileName() != "decimal.jsonl.gz" || d.FilePath("arm64") != "arm64/decimal.jsonl.gz" {
		t.Fatalf("FileName %q FilePath %q", d.FileName(), d.FilePath("arm64"))
	}
	var z1, z2 bytes.Buffer
	if err := d.WriteFile(&z1); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFile(&z2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(z1.Bytes(), z2.Bytes()) {
		t.Fatal("gzip output is not deterministic")
	}
	path := filepath.Join(t.TempDir(), d.FileName())
	if err := os.WriteFile(path, z1.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := vec.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, plain) {
		t.Fatal("gzip file does not read back to the JSONL content")
	}

	other := bytes.Replace(plain, []byte(`"goos":"`+runtime.GOOS+`"`), []byte(`"goos":"plan9"`), 1)
	if bytes.Equal(other, plain) || !vec.SameExceptGOOS(other, plain) {
		t.Fatal("goos must be ignored")
	}
	arch := bytes.Replace(plain, []byte(`"goarch":"`+runtime.GOARCH+`"`), []byte(`"goarch":"mips"`), 1)
	if bytes.Equal(arch, plain) || vec.SameExceptGOOS(arch, plain) {
		t.Fatal("goarch must not be ignored")
	}
	recs := bytes.Replace(plain, []byte(`"op":"add"`), []byte(`"op":"sub"`), 1)
	if vec.SameExceptGOOS(recs, plain) {
		t.Fatal("record changes must not be ignored")
	}
	hdr := bytes.Replace(plain, []byte(`"go":"`), []byte(`"go":"x`), 1)
	if vec.SameExceptGOOS(hdr, plain) {
		t.Fatal("other header changes must not be ignored")
	}
}

// fmaFused reads tracker data/go_fma_sites.tsv: whether gc fuses the
// multiply-adds of MyDecimal.ToFloat64 (mydecimal.go:1118, 1125) on this
// target. Both lines must agree (ToFloat64Model fuses both or neither).
func fmaFused(t *testing.T) bool {
	t.Helper()
	col := map[string]string{"amd64": "amd64_v1", "amd64.v3": "amd64_v3"}[vec.Target()]
	if col == "" {
		col = vec.Target()
	}
	b, err := os.ReadFile(filepath.Join("..", "data", "go_fma_sites.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	hdr := strings.Split(lines[0], "\t")
	ci := -1
	for i, h := range hdr {
		if h == col {
			ci = i
		}
	}
	if ci < 0 {
		t.Fatalf("go_fma_sites.tsv has no column for target %s", vec.Target())
	}
	vals := map[string]string{}
	for _, l := range lines[1:] {
		c := strings.Split(l, "\t")
		if c[1] == "pkg/types/mydecimal.go" && c[3] == "MyDecimal.ToFloat64" {
			vals[c[2]] = c[ci]
		}
	}
	if len(vals) != 2 || vals["1118"] != vals["1125"] {
		t.Fatalf("go_fma_sites.tsv: MyDecimal.ToFloat64 sites %v, want lines 1118 and 1125 alike", vals)
	}
	return vals["1118"] == "yes"
}

// TestFMA: every div_then_to_float64 record equals ToFloat64Model with the
// fusion go_fma_sites.tsv records for this target, and the FMA group has
// at least domains.FMADiverging records whose result depends on fusion. This ties
// the vectors to the assembly scan: if gc changed what it fuses, the model
// with the registry's answer would disagree with the Go output.
func TestFMA(t *testing.T) {
	d := vec.Lookup("decimal")
	fused := fmaFused(t)
	sc := bufio.NewScanner(bytes.NewReader(generate(t, d)))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	sc.Scan()
	n, diverging := 0, 0
	for sc.Scan() {
		var r vec.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Op != "div_then_to_float64" {
			continue
		}
		want, ok := domains.DivToFloat64Model(r.Args, fused)
		if !ok {
			continue
		}
		n++
		if got := r.Out.(string); got != want {
			t.Errorf("%s %s: Go %s, model (fused=%v) %s", r.Op, r.Args, got, fused, want)
		}
		other, _ := domains.DivToFloat64Model(r.Args, !fused)
		if other != want {
			diverging++
		} else if r.Src == "rand/fma" {
			t.Errorf("rand/fma record %s does not depend on fusion", r.Args)
		}
	}
	t.Logf("target %s fused=%v: %d div_then_to_float64 records on the <=12-digit path, %d depend on fusion",
		vec.Target(), fused, n, diverging)
	if diverging < domains.FMADiverging {
		t.Errorf("%d records depend on fusion, want >= %d", diverging, domains.FMADiverging)
	}
}

// TestTargetsDiffer: the committed amd64 and arm64 files have the same
// records apart from the outputs that depend on FMA fusion (ops ending in
// to_float64), and differ in at least domains.FMADiverging of them.
func TestTargetsDiffer(t *testing.T) {
	d := vec.Lookup("decimal")
	read := func(target string) []string {
		b, err := vec.ReadFile(filepath.Join("..", "vectors", vec.TiDBVersion, d.FilePath(target)))
		if err != nil {
			t.Skipf("%s: %v", target, err)
		}
		return strings.Split(string(b), "\n")
	}
	a, b := read("amd64"), read("arm64")
	if len(a) != len(b) {
		t.Fatalf("amd64 %d lines, arm64 %d", len(a), len(b))
	}
	diff := 0
	for i := 1; i < len(a); i++ {
		if a[i] == b[i] {
			continue
		}
		diff++
		var ra, rb vec.Record
		if err := json.Unmarshal([]byte(a[i]), &ra); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal([]byte(b[i]), &rb)
		if ra.Op != rb.Op || !strings.HasSuffix(ra.Op, "to_float64") {
			t.Errorf("line %d differs outside to_float64:\n amd64 %s\n arm64 %s", i+1, a[i], b[i])
		}
	}
	t.Logf("amd64 and arm64 differ in %d records", diff)
	if diff < domains.FMADiverging {
		t.Errorf("amd64 and arm64 differ in %d records, want >= %d", diff, domains.FMADiverging)
	}
}
