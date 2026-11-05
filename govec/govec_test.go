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

	_ "github.com/dulao5/tidb-rs-tracker/govec/domains"
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

// TestBlockCoverage: every function of the domain's porting blocks
// (tracker data/func_blocks.tsv) is covered by some op's GoFuncs or listed in
// Exempt with a reason; every GoFuncs/Exempt symbol exists in those blocks.
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
		}
		covered := map[string]bool{}
		for op, c := range d.Coverage {
			for _, fn := range c.GoFuncs {
				covered[fn] = true
				if !inBlocks[fn] {
					t.Errorf("domain %s op %s: GoFuncs %q is not a function of blocks %v", name, op, fn, d.Blocks)
				}
			}
		}
		for fn, why := range d.Exempt {
			if !inBlocks[fn] {
				t.Errorf("domain %s: exempt %q is not a function of blocks %v", name, fn, d.Blocks)
			}
			if covered[fn] {
				t.Errorf("domain %s: %q is both covered and exempt", name, fn)
			}
			if why == "" {
				t.Errorf("domain %s: exempt %q has no reason", name, fn)
			}
		}
		n := 0
		for fn := range inBlocks {
			if !covered[fn] && d.Exempt[fn] == "" {
				t.Errorf("domain %s: %s is neither covered by an op nor exempt", name, fn)
			}
			if covered[fn] {
				n++
			}
		}
		t.Logf("domain %s: %d/%d functions of %v covered, %d exempt", name, n, len(inBlocks), d.Blocks, len(d.Exempt))
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

	// Committed vectors must match (only with the pinned toolchain, since the
	// header records the Go version; goos/goarch are not compared).
	path := filepath.Join("..", "vectors", vec.TiDBVersion, d.FileName())
	committed, err := vec.ReadFile(path)
	if os.IsNotExist(err) {
		t.Logf("%s not committed yet", path)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !vec.SameExceptPlatform(committed, out1) {
		t.Errorf("%s is stale (go %s); run scripts/govec.sh", path, runtime.Version())
	}
}

// TestStorage: the gzip form (README "Storage") is deterministic and reads
// back to the plain JSONL; the committed-file comparison ignores only the
// header's goos/goarch.
func TestStorage(t *testing.T) {
	d := *vec.Lookup("decimal")
	plain := generate(t, &d)
	d.Gzip = true
	if d.FileName() != "decimal.jsonl.gz" {
		t.Fatalf("FileName %q", d.FileName())
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

	other := bytes.Replace(plain, []byte(`"goos":"`+runtime.GOOS+`","goarch":"`+runtime.GOARCH+`"`),
		[]byte(`"goos":"plan9","goarch":"mips"`), 1)
	if bytes.Equal(other, plain) || !vec.SameExceptPlatform(other, plain) {
		t.Fatal("platform fields must be ignored")
	}
	recs := bytes.Replace(plain, []byte(`"op":"add"`), []byte(`"op":"sub"`), 1)
	if vec.SameExceptPlatform(recs, plain) {
		t.Fatal("record changes must not be ignored")
	}
	hdr := bytes.Replace(plain, []byte(`"go":"`), []byte(`"go":"x`), 1)
	if vec.SameExceptPlatform(hdr, plain) {
		t.Fatal("other header changes must not be ignored")
	}
}
