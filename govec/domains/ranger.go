package domains

// Domain "ranger": block D00.1 part 2a — the ranger types (pkg/util/ranger types.go, the
// full ranges of points.go). RangerContext.Detach is exempt (a map copy; Rust unit test).
//
// A range is {"l": [datum...], "h": [datum...], "c": [collation...], "le": bool, "he": bool}
// with datums in the codec / datum2 form (d2DatumIn). A sub-result that panics in Go is
// {"panic": true}; an error is {"err": msg}.
//
// Records:
//   - "range": args [range] -> the range's String / Redact, Width, IsPoint (RangerContext
//     with RegardNULLAsPoint false / true), IsPointNullable, IsPointNonNullable,
//     IsOnlyNull, IsFullRange(true / false), MemUsage, PrefixEqualLen, Clone.
//   - "encode": args [range, tz offset or null, "strict" | "ignore"] -> Encode's two keys.
//   - "pair": args [r1, r2] -> Equal, Subset, IntersectRange.
//   - "ranges": args [rs1, rs2] -> Ranges.Subset, IntersectRanges, HasFullRange,
//     Ranges.MemUsage, CloneForPlanCache, Range, Rebuild.
//   - "full": no args -> FullIntRange(false / true), FullRange, FullNotNullRange.

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"math/rand/v2"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/errctx"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
	"github.com/pingcap/tidb/pkg/util/ranger"
	rangerctx "github.com/pingcap/tidb/pkg/util/ranger/context"
)

const rangerSeed = 20261110

// RangerMinedTests: the scenarios of pkg/util/ranger/types_test.go copied as records.
func RangerMinedTests() []string {
	return []string{"TestRange", "TestIsFullRange", "TestRangeMemUsage", "TestIntersectionList",
		"TestIntersectionEmpty", "TestIntersectionSubset", "TestIntersectionOverlap"}
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "ranger",
		Seed:     rangerSeed,
		Coverage: rangerCoverage,
		Blocks:   []string{"D00.1"},
		Exempt: map[string]string{
			"RangerContext.Detach": "copies the struct and the fix-control map with a new ExprCtx (an interface value); Rust unit test",
		},
		Gzip:     true,
		Generate: rangerGenerate,
		Exec:     rangerExec,
	})
}

var rangerCoverage = map[string]vec.Coverage{
	"range": {Min: 1000, GoFuncs: []string{"Range.String", "Range.Redact", "Range.string", "dealWithRedact",
		"formatDatum", "Range.Width", "Range.IsPoint", "Range.isPoint", "Range.IsPointNullable",
		"Range.IsPointNonNullable", "Range.IsOnlyNull", "Range.IsFullRange", "isBoundaryValue", "Range.MemUsage",
		"Range.PrefixEqualLen", "Range.Clone"}},
	"encode": {Min: 300, GoFuncs: []string{"Range.Encode"}},
	"pair": {Min: 1000, GoFuncs: []string{"Range.Equal", "Range.Subset", "checkCollators", "prefix",
		"Range.IntersectRange", "compareLexicographically", "extendBound"}},
	"ranges": {Min: 300, GoFuncs: []string{"Ranges.Subset", "Ranges.IntersectRanges", "HasFullRange",
		"Ranges.MemUsage", "Ranges.CloneForPlanCache", "Ranges.Range", "Ranges.Rebuild"}},
	"full": {Min: 1, GoFuncs: []string{"FullIntRange", "FullRange", "FullNotNullRange"}},
}

type rangeSpec struct {
	L  []json.RawMessage `json:"l"`
	H  []json.RawMessage `json:"h"`
	C  []string          `json:"c"`
	LE bool              `json:"le"`
	HE bool              `json:"he"`
}

func rangeIn(raw json.RawMessage) *ranger.Range {
	var s rangeSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		panic(err)
	}
	r := &ranger.Range{LowExclude: s.LE, HighExclude: s.HE,
		LowVal: make([]types.Datum, 0, len(s.L)), HighVal: make([]types.Datum, 0, len(s.H))}
	for _, q := range rawList(s.L) {
		r.LowVal = append(r.LowVal, d2DatumIn(q))
	}
	for _, q := range rawList(s.H) {
		r.HighVal = append(r.HighVal, d2DatumIn(q))
	}
	for _, c := range s.C {
		r.Collators = append(r.Collators, collate.GetCollator(c))
	}
	return r
}

func rangesIn(raw json.RawMessage) ranger.Ranges {
	var rs []json.RawMessage
	if err := json.Unmarshal(raw, &rs); err != nil {
		panic(err)
	}
	out := make(ranger.Ranges, 0, len(rs))
	for _, r := range rs {
		out = append(out, rangeIn(r))
	}
	return out
}

// rTry runs f; a Go panic is {"panic": true}.
func rTry(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": true}
		}
	}()
	return f()
}

func rErr(err error) any { return map[string]any{"err": vec.GoString(err.Error())} }

func rStr(r *ranger.Range) any {
	if r == nil {
		return nil
	}
	return vec.GoString(r.String())
}

func rStrs(rs ranger.Ranges) any {
	if rs == nil {
		return nil
	}
	out := make([]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, rStr(r))
	}
	return out
}

func rangerExec(op string, p []json.RawMessage) (out any, err error) {
	tc := types.DefaultStmtNoWarningContext
	switch op {
	case "range":
		r := rangeIn(p[0])
		m := map[string]any{}
		m["s"] = rTry(func() any { return vec.GoString(r.String()) })
		m["r_on"] = rTry(func() any { return vec.GoString(r.Redact("ON")) })
		m["r_mk"] = rTry(func() any { return vec.GoString(r.Redact("MARKER")) })
		m["w"] = r.Width()
		for _, null := range []bool{false, true} {
			key := "p0"
			if null {
				key = "p1"
			}
			m[key] = rTry(func() any { return r.IsPoint(&rangerctx.RangerContext{TypeCtx: tc, RegardNULLAsPoint: null}) })
		}
		m["pn"] = rTry(func() any { return r.IsPointNullable(tc) })
		m["pnn"] = rTry(func() any { return r.IsPointNonNullable(tc) })
		m["on"] = rTry(func() any { return r.IsOnlyNull() })
		m["fu"] = rTry(func() any { return r.IsFullRange(true) })
		m["fs"] = rTry(func() any { return r.IsFullRange(false) })
		m["mem"] = r.MemUsage()
		m["pel"] = rTry(func() any {
			n, err := r.PrefixEqualLen(tc)
			if err != nil {
				return rErr(err)
			}
			return n
		})
		m["cl"] = rTry(func() any {
			c := r.Clone()
			return map[string]any{"s": rStr(c), "eq": c.Equal(r), "mem": c.MemUsage()}
		})
		return m, nil
	case "encode":
		r := rangeIn(p[0])
		loc := gotime.UTC
		if string(p[1]) != "null" {
			loc = gotime.FixedZone("Z", arg[int](p, 1))
		}
		ec := errctx.StrictNoWarningContext
		if arg[string](p, 2) == "ignore" {
			var lm errctx.LevelMap
			for i := range lm {
				lm[i] = errctx.LevelIgnore
			}
			ec = errctx.NewContextWithLevels(lm, contextutil.IgnoreWarn)
		}
		return rTry(func() any {
			lo, hi, err := r.Encode(ec, loc, nil, nil)
			if err != nil {
				return rErr(err)
			}
			return map[string]any{"lo": hex.EncodeToString(lo), "hi": hex.EncodeToString(hi)}
		}), nil
	case "pair":
		r1, r2 := rangeIn(p[0]), rangeIn(p[1])
		m := map[string]any{}
		m["eq"] = rTry(func() any { return r1.Equal(r2) })
		m["sub"] = rTry(func() any { return r1.Subset(tc, r2) })
		m["isect"] = rTry(func() any {
			x, err := r1.IntersectRange(tc, r2)
			if err != nil {
				return rErr(err)
			}
			return rStr(x)
		})
		return m, nil
	case "ranges":
		rs1, rs2 := rangesIn(p[0]), rangesIn(p[1])
		m := map[string]any{}
		m["sub"] = rTry(func() any { return rs1.Subset(tc, rs2) })
		m["isect"] = rTry(func() any { return rStrs(rs1.IntersectRanges(tc, rs2)) })
		m["hf_u"] = rTry(func() any { return ranger.HasFullRange(rs1, true) })
		m["hf_s"] = rTry(func() any { return ranger.HasFullRange(rs1, false) })
		m["mem"] = rs1.MemUsage()
		m["cfpc"] = rTry(func() any {
			c := rs1.CloneForPlanCache()
			if c == nil {
				return nil
			}
			return rStrs(c.Range())
		})
		m["rng"] = rStrs(rs1.Range())
		m["rebuild"] = rs1.Rebuild(nil) == nil
		return m, nil
	case "full":
		m := map[string]any{}
		for k, rs := range map[string]ranger.Ranges{"int_s": ranger.FullIntRange(false), "int_u": ranger.FullIntRange(true),
			"full": ranger.FullRange(), "nn": ranger.FullNotNullRange()} {
			m[k] = map[string]any{"s": rStrs(rs), "mem": rs.MemUsage(), "fu": ranger.HasFullRange(rs, true),
				"fs": ranger.HasFullRange(rs, false)}
		}
		return m, nil
	}
	panic("ranger: unknown op " + op)
}

// ---------------------------------------------------------------- generator

type rSpec struct {
	L  [][]any  `json:"l"`
	H  [][]any  `json:"h"`
	C  []string `json:"c"`
	LE bool     `json:"le"`
	HE bool     `json:"he"`
}

func rInt(v int64) []any {
	switch v {
	case math.MinInt64:
		return []any{types.KindMinNotNull}
	case math.MaxInt64:
		return []any{types.KindMaxValue}
	}
	return []any{types.KindInt64, v}
}

// rBuild is buildRange of types_test.go: MinInt64 is -inf, MaxInt64 is +inf.
func rBuild(low, high []int64, le, he bool) rSpec {
	s := rSpec{LE: le, HE: he}
	for _, v := range low {
		s.L = append(s.L, rInt(v))
	}
	for _, v := range high {
		s.H = append(s.H, rInt(v))
	}
	for range low {
		s.C = append(s.C, "binary")
	}
	return s
}

func rangerGenerate(g *vec.Gen, rng *rand.Rand) {
	const minI, maxI = math.MinInt64, math.MaxInt64
	nullD := []any{types.KindNull}
	bin1 := []string{"binary"}
	// TestRange: sampleRanges and the IsPoint cases.
	for _, s := range []rSpec{
		{L: [][]any{rInt(1)}, H: [][]any{rInt(1)}, C: bin1},
		{L: [][]any{rInt(1)}, H: [][]any{rInt(1)}, C: bin1, HE: true},
		{L: [][]any{rInt(1)}, H: [][]any{rInt(2)}, C: bin1, LE: true, HE: true},
		{L: [][]any{{types.KindFloat64, fBits(1.1)}}, H: [][]any{{types.KindFloat64, fBits(1.9)}}, C: bin1, HE: true},
		{L: [][]any{rInt(minI)}, H: [][]any{rInt(1)}, C: bin1, HE: true},
		{L: [][]any{{types.KindString, vec.GoString("abc"), "utf8mb4_bin"}}, H: [][]any{{types.KindString, vec.GoString("abc"), "utf8mb4_bin"}}, C: bin1},
		{L: [][]any{rInt(1)}, H: [][]any{rInt(1), rInt(1)}, C: bin1},
		{L: [][]any{rInt(1)}, H: [][]any{rInt(1)}, C: bin1, LE: true},
	} {
		g.Add("TestRange", "range", s)
	}
	// TestIsFullRange.
	for _, s := range []rSpec{
		{L: [][]any{rInt(minI)}, H: [][]any{rInt(maxI)}, C: bin1},
		{L: [][]any{rInt(maxI)}, H: [][]any{rInt(minI)}, C: bin1},
		{L: [][]any{rInt(1)}, H: [][]any{{types.KindUint64, uint64(math.MaxUint64)}}, C: bin1},
		{L: [][]any{nullD}, H: [][]any{{types.KindUint64, uint64(math.MaxUint64)}}, C: bin1},
		{L: [][]any{nullD}, H: [][]any{nullD}, C: bin1},
		{L: [][]any{{types.KindUint64, uint64(0)}}, H: [][]any{{types.KindUint64, uint64(math.MaxUint64)}}, C: bin1},
	} {
		g.Add("TestIsFullRange", "range", s)
	}
	// TestRangeMemUsage.
	r1 := rSpec{L: [][]any{rInt(0)}, H: [][]any{rInt(1)}, C: bin1}
	r2 := rSpec{L: [][]any{{types.KindString, vec.GoString("abcde"), "utf8mb4_bin"}}, H: [][]any{{types.KindString, vec.GoString("fghij"), "utf8mb4_bin"}}, C: bin1}
	g.Add("TestRangeMemUsage", "range", r1)
	g.Add("TestRangeMemUsage", "range", r2)
	g.Add("TestRangeMemUsage", "ranges", []rSpec{r1, r2}, []rSpec{})
	// TestIntersectionList.
	g.Add("TestIntersectionList", "ranges",
		[]rSpec{rBuild([]int64{100, 0}, []int64{100, maxI}, true, false), rBuild([]int64{100}, []int64{maxI}, true, false)},
		[]rSpec{rBuild([]int64{minI}, []int64{101}, false, true), rBuild([]int64{101, minI}, []int64{101, 10}, false, true)})
	// TestIntersectionEmpty / Subset / Overlap: pairs of buildRange ranges both ways.
	pairs := [][2]rSpec{
		{rBuild([]int64{1}, []int64{5}, false, false), rBuild([]int64{6}, []int64{10}, false, false)},
		{rBuild([]int64{1}, []int64{5}, false, true), rBuild([]int64{5}, []int64{10}, false, false)},
		{rBuild([]int64{1}, []int64{5}, false, false), rBuild([]int64{5}, []int64{10}, true, false)},
		{rBuild([]int64{1, 1}, []int64{1, 5}, false, false), rBuild([]int64{1, 6}, []int64{1, 10}, false, false)},
		{rBuild([]int64{1}, []int64{10}, false, false), rBuild([]int64{3}, []int64{5}, false, false)},
		{rBuild([]int64{1}, []int64{10}, false, false), rBuild([]int64{1, 3}, []int64{1, 5}, false, false)},
		{rBuild([]int64{minI}, []int64{maxI}, false, false), rBuild([]int64{3, minI}, []int64{3, maxI}, false, false)},
		{rBuild([]int64{1}, []int64{5}, false, false), rBuild([]int64{3}, []int64{8}, false, false)},
		{rBuild([]int64{1, 2}, []int64{3, 4}, true, true), rBuild([]int64{2}, []int64{3}, false, false)},
		{rBuild([]int64{1}, []int64{5}, true, true), rBuild([]int64{1}, []int64{5}, false, false)},
	}
	for i, pr := range pairs {
		src := []string{"TestIntersectionEmpty", "TestIntersectionEmpty", "TestIntersectionEmpty", "TestIntersectionEmpty",
			"TestIntersectionSubset", "TestIntersectionSubset", "TestIntersectionSubset", "TestIntersectionOverlap",
			"TestIntersectionOverlap", "TestIntersectionOverlap"}[i]
		g.Add(src, "pair", pr[0], pr[1])
		g.Add(src, "pair", pr[1], pr[0])
	}
	g.Add("sys/full", "full")

	// Random ranges.
	pool := codecPool(parseDatumLiterals(codecLiteralsTSV))
	smallInt := func() []any {
		switch rng.IntN(12) {
		case 0:
			return rInt(minI)
		case 1:
			return rInt(maxI)
		case 2:
			return nullD
		case 3:
			return []any{types.KindUint64, []uint64{0, 1, 3, math.MaxUint64}[rng.IntN(4)]}
		}
		return rInt(int64(rng.IntN(7)) - 3)
	}
	colls := codecColls
	strD := func() []any {
		s := []string{"", "a", "A", "a ", "b", "ab", "ä", "ß", "ss", "中", "\U0002CEA1"}[rng.IntN(11)]
		return []any{types.KindString, vec.GoString(s), colls[rng.IntN(len(colls))]}
	}
	anyD := func() []any {
		switch rng.IntN(10) {
		case 0, 1, 2, 3, 4:
			return smallInt()
		case 5, 6:
			return strD()
		}
		return pool[rng.IntN(len(pool))]
	}
	randRange := func(gen func() []any) rSpec {
		w := 1 + rng.IntN(3)
		s := rSpec{LE: rng.IntN(3) == 0, HE: rng.IntN(3) == 0}
		hw := w
		if rng.IntN(10) == 0 {
			hw = 1 + rng.IntN(3)
		}
		for i := 0; i < w; i++ {
			s.L = append(s.L, gen())
		}
		for i := 0; i < hw; i++ {
			if i < w && rng.IntN(2) == 0 {
				s.H = append(s.H, s.L[i])
			} else {
				s.H = append(s.H, gen())
			}
		}
		nc := max(w, hw)
		if rng.IntN(20) == 0 {
			nc = rng.IntN(nc + 1)
		}
		for i := 0; i < nc; i++ {
			if rng.IntN(3) == 0 {
				s.C = append(s.C, colls[rng.IntN(len(colls))])
			} else {
				s.C = append(s.C, "binary")
			}
		}
		return s
	}
	gens := []func() []any{smallInt, smallInt, strD, anyD}
	for i := range 1500 {
		g.Add("rand/range", "range", randRange(gens[i%len(gens)]))
	}
	for i := range 400 {
		var tz any
		if i%3 == 1 {
			tz = []int{3600, -18000, 19800}[i%3]
		}
		mode := "strict"
		if i%2 == 1 {
			mode = "ignore"
		}
		g.Add("rand/encode", "encode", randRange(gens[i%len(gens)]), tz, mode)
	}
	for i := range 1500 {
		gen := gens[i%len(gens)]
		a := randRange(gen)
		b := randRange(gen)
		if i%5 == 0 {
			b = a
		}
		g.Add("rand/pair", "pair", a, b)
	}
	for i := range 400 {
		gen := gens[i%3]
		n1, n2 := rng.IntN(4), rng.IntN(4)
		rs1, rs2 := []rSpec{}, []rSpec{}
		for range n1 {
			rs1 = append(rs1, randRange(gen))
		}
		for range n2 {
			rs2 = append(rs2, randRange(gen))
		}
		if i%4 == 0 && n1 > 0 {
			rs2 = append(rs2, rs1[0])
		}
		g.Add("rand/ranges", "ranges", rs1, rs2)
	}
}
