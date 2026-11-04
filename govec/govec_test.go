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

func generate(t *testing.T, d *vec.Domain) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := d.WriteAll(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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

	// Oracle mode must reproduce records from (op, args) alone.
	var in bytes.Buffer
	for i := 0; i < len(recs); i += 37 {
		in.Write(vec.MustJSON(vec.Request{Op: recs[i].Op, Args: recs[i].Args}))
		in.WriteByte('\n')
	}
	var out bytes.Buffer
	if err := d.Oracle(&in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for j, line := range lines {
		var got vec.Record
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatal(err)
		}
		want := recs[j*37]
		got.Src = want.Src
		if g, w := string(vec.MustJSON(got)), string(vec.MustJSON(want)); g != w {
			t.Fatalf("oracle mismatch:\n got %s\nwant %s", g, w)
		}
	}

	// Committed vectors must match (only with the pinned toolchain, since the
	// header records the Go version).
	path := filepath.Join("..", "vectors", vec.TiDBVersion, d.Name+".jsonl")
	committed, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Logf("%s not committed yet", path)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, out1) {
		t.Errorf("%s is stale (go %s); run scripts/govec.sh", path, runtime.Version())
	}
}
