package domains

import (
	"math"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
)

// Exempt D14.0 functions with the reason.
func execdetailsExempt() map[string]string {
	const carrier = "tracing carrier (opentracing spans, runtime/trace regions, the TRACE statement's recorder): decided with ExecCtx / M-GOCTX (D05.3) and the TRACE executor"
	return map[string]string{
		"MergeTiFlashRUConsumption":   "TiFlash RU consumption (DEF.MPP): decodes kvproto resource_manager.Consumption, whose Rust crate pulls grpcio; with the MPP / TiFlash client",
		"CallbackRecorder.RecordSpan": carrier, "ChildSpanFromContxt": carrier, "ContextWithTraceInfo": carrier,
		"NewRecordedTrace": carrier, "Region.End": carrier, "SpanFromContext": carrier, "StartRegion": carrier,
		"StartRegionEx": carrier, "StartRegionWithNewRootSpan": carrier, "TraceInfoFromContext": carrier, "noopSpan": carrier,
	}
}

func edRandDur(rng *rand.Rand) int64 {
	switch rng.IntN(12) {
	case 0:
		return 0
	case 1:
		return rng.Int64N(1000)
	case 2:
		return rng.Int64N(int64(time.Millisecond))
	case 3:
		return rng.Int64N(int64(time.Second))
	case 4:
		return rng.Int64N(int64(time.Minute))
	case 5:
		return rng.Int64N(int64(24 * time.Hour))
	case 6:
		return -rng.Int64N(int64(time.Second))
	case 7:
		u := []int64{int64(time.Microsecond), int64(time.Millisecond), int64(time.Second)}[rng.IntN(3)]
		return 10*u - 1 - rng.Int64N(3) + rng.Int64N(3)
	case 8:
		u := []int64{int64(time.Microsecond), int64(time.Millisecond), int64(time.Second)}[rng.IntN(3)]
		return u*int64(rng.IntN(20)) + u/1000*int64(rng.IntN(1000)) + rng.Int64N(2)
	case 9:
		return []int64{math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, 1, int64(time.Microsecond), int64(time.Microsecond) + 1}[rng.IntN(6)]
	default:
		return rng.Int64N(int64(10 * time.Second))
	}
}

func edPos(rng *rand.Rand) int64 {
	if rng.IntN(4) == 0 {
		return 0
	}
	return edRandDur(rng)
}

func edRandTime(rng *rand.Rand) []int64 {
	return []int64{edPos(rng), edPos(rng), edPos(rng), edPos(rng), edPos(rng)}
}

func edRandU(rng *rand.Rand) uint64 {
	switch rng.IntN(5) {
	case 0:
		return 0
	case 1:
		return rng.Uint64N(10)
	case 2:
		return rng.Uint64N(1 << 20)
	case 3:
		return rng.Uint64N(1 << 40)
	default:
		return rng.Uint64()
	}
}

func edRandScan(rng *rand.Rand) any {
	if rng.IntN(5) == 0 {
		return nil
	}
	if rng.IntN(6) == 0 {
		return []any{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	}
	i := func() int64 {
		if rng.IntN(3) == 0 {
			return 0
		}
		return int64(edRandU(rng) >> 1)
	}
	return []any{i(), i(), i(), edRandU(rng), edRandU(rng), edRandU(rng), edRandU(rng), edRandU(rng) >> 1, edPos(rng), edPos(rng)}
}

func edRandWrite(rng *rand.Rand) any {
	if rng.IntN(3) == 0 {
		return nil
	}
	a := []int64{}
	zero := rng.IntN(5) == 0
	for range 13 {
		if zero {
			a = append(a, 0)
		} else {
			a = append(a, edPos(rng))
		}
	}
	return a
}

func edRandTiKV(rng *rand.Rand) map[string]any {
	m := map[string]any{}
	if rng.IntN(3) != 0 {
		m["t"] = edRandTime(rng)
	}
	if rng.IntN(3) != 0 {
		m["s"] = edRandScan(rng)
	}
	if rng.IntN(3) != 0 {
		m["w"] = edRandWrite(rng)
	}
	return m
}

func edRandReq(rng *rand.Rand) any {
	return []any{edPos(rng), rng.Uint64N(100000), []string{"", "tikv-1:20160", "127.0.0.1:20161", "<x>&"}[rng.IntN(4)], edRandTiKV(rng)}
}

var edBackoffNames = []string{"regionMiss", "tikvRPC", "txnLock", "pdRPC", "regionScheduling", "a", "b"}

func edRandTypes(rng *rand.Rand) []string {
	if rng.IntN(3) == 0 {
		return nil
	}
	var r []string
	for range rng.IntN(5) {
		r = append(r, edBackoffNames[rng.IntN(len(edBackoffNames))])
	}
	return r
}

func edRandCommit(rng *rand.Rand) any {
	if rng.IntN(6) == 0 {
		return nil
	}
	m := map[string]any{"gct": edPos(rng), "glt": edPos(rng), "pw": edPos(rng), "wpb": edPos(rng), "ct": edPos(rng),
		"llt": edPos(rng), "cbt": edPos(rng), "lag": []int64{edPos(rng), rng.Int64N(5), int64(rng.IntN(3)) * rng.Int64N(1<<40), rng.Int64N(1 << 40)},
		"pbt": edRandTypes(rng), "cbtypes": edRandTypes(rng), "wk": rng.IntN(3) * rng.IntN(1000), "ws": rng.IntN(3) * rng.IntN(100000),
		"prn": rng.IntN(3) * rng.IntN(50), "tr": rng.IntN(3), "rl": edPos(rng), "prq": rng.IntN(10)}
	if rng.IntN(2) == 0 {
		m["sp"] = edRandReq(rng)
	}
	if rng.IntN(2) == 0 {
		m["cp"] = edRandReq(rng)
	}
	return m
}

func edRandLock(rng *rand.Rand) any {
	if rng.IntN(20) == 0 {
		return nil
	}
	m := map[string]any{"tt": edPos(rng), "rn": rng.IntN(3) * rng.IntN(100), "lk": rng.IntN(3) * rng.IntN(1000),
		"aln": rng.IntN(5), "ald": rng.IntN(5), "lwc": rng.IntN(5), "rl": edPos(rng), "bt": edPos(rng), "btypes": edRandTypes(rng),
		"st": edPos(rng), "sr": rng.Uint64N(1000), "sa": []string{"", "tikv-3:20160"}[rng.IntN(2)], "rpct": edPos(rng),
		"rpcc": rng.Int64N(3) * rng.Int64N(100), "rc": rng.IntN(4)}
	if rng.IntN(2) == 0 {
		m["se"] = edRandTiKV(rng)
	}
	return m
}

func edRandBackoff(rng *rand.Rand) (map[string]int64, map[string]int) {
	if rng.IntN(3) == 0 {
		return nil, nil
	}
	bs, bt := map[string]int64{}, map[string]int{}
	for range rng.IntN(4) {
		k := edBackoffNames[rng.IntN(len(edBackoffNames))]
		bt[k] = rng.IntN(5) + 1
		if rng.IntN(5) != 0 {
			bs[k] = edPos(rng)
		}
	}
	return bs, bt
}

func edRandExec(rng *rand.Rand) any {
	if rng.IntN(10) == 0 {
		return nil
	}
	bs, bt := edRandBackoff(rng)
	m := map[string]any{"bs": bs, "bt": bt, "addr": []string{"", "tikv-1", "tikv-2", "tikv-3"}[rng.IntN(4)], "td": edRandTime(rng),
		"cop": edPos(rng), "bo": edPos(rng), "lkd": edPos(rng), "rc": rng.IntN(3) * rng.IntN(20)}
	if rng.IntN(3) == 0 {
		m["commit"] = edRandCommit(rng)
	}
	if rng.IntN(4) == 0 {
		m["lock"] = edRandLock(rng)
	}
	if rng.IntN(4) == 0 {
		m["slock"] = edRandLock(rng)
	}
	if rng.IntN(2) == 0 {
		m["scan"] = edRandScan(rng)
	}
	return m
}

func edRandCop(rng *rand.Rand) any {
	if rng.IntN(8) == 0 {
		return nil
	}
	bs, bt := edRandBackoff(rng)
	return map[string]any{"scan": edRandScan(rng), "td": edRandTime(rng), "addr": []string{"tikv-1", "tikv-2", ""}[rng.IntN(3)],
		"bo": edPos(rng), "bs": bs, "bt": bt}
}

var edScanKeys = []string{"dds", "dsk", "dms", "dmk", "dls", "dlk", "rsc", "rd", "bs", "lr", "rr", "lrm", "ch", "cm", "seg", "rt",
	"dr", "db", "mir", "mib", "mor", "lsr", "bb", "bi", "srr", "mnl", "mxl", "mnr", "mxr", "v3", "vd", "vc", "vlt", "vst", "vvn",
	"vdn", "vrv", "vro"}

func edRandSummary(rng *rand.Rand, full bool) any {
	if !full && rng.IntN(6) == 0 {
		return nil
	}
	u := func() *uint64 {
		v := edRandU(rng) % (1 << 40)
		return &v
	}
	m := map[string]any{"tp": u(), "rows": u(), "iters": u()}
	if rng.IntN(2) == 0 {
		m["conc"] = rng.Uint64N(16)
	}
	if rng.IntN(80) == 0 {
		delete(m, []string{"tp", "rows", "iters"}[rng.IntN(3)]) // nil pointer dereference in Go
	}
	switch rng.IntN(5) {
	case 0:
		m["id"] = "TableFullScan_" + strconv.Itoa(rng.IntN(12))
	case 1:
		m["id"] = []string{"", "x", "Selection_abc", "a_b_-3", "_9", "HashAgg_99999999999999999999"}[rng.IntN(6)]
	}
	if rng.IntN(3) == 0 {
		sc := map[string]any{}
		for _, k := range edScanKeys {
			if rng.IntN(3) == 0 {
				sc[k] = rng.Uint64N(1 << uint(rng.IntN(40)+1))
			}
		}
		if rng.IntN(2) == 0 {
			rs := [][]any{}
			for range rng.IntN(4) {
				rs = append(rs, []any{"inst" + strconv.Itoa(rng.IntN(5)), rng.Uint64N(20)})
			}
			sc["regions"] = rs
		}
		m["scan"] = sc
	}
	if rng.IntN(3) == 0 {
		ms := uint64(time.Millisecond)
		m["wait"] = []uint64{rng.Uint64N(3 * ms), rng.Uint64N(3 * ms), rng.Uint64N(3 * ms)}
	}
	return m
}

func edRandStats(rng *rand.Rand) any {
	switch rng.IntN(4) {
	case 0:
		rs := [][]int64{}
		for range rng.IntN(4) {
			rs = append(rs, []int64{edPos(rng), rng.Int64N(1000)})
		}
		var set any
		if rng.IntN(4) == 0 {
			set = rng.Int64N(100)
		}
		return []any{"basic", rs, set}
	case 1:
		cs := []any{}
		for range rng.IntN(3) {
			cs = append(cs, edRandCommit(rng))
		}
		return []any{"commit", edRandCommit(rng), edRandLock(rng), edRandLock(rng), rng.IntN(3), cs}
	case 2:
		cs := [][]any{}
		for range rng.IntN(4) {
			cs = append(cs, []any{[]string{"concurrency", "probe", "build"}[rng.IntN(3)], rng.IntN(5) - 1})
		}
		return []any{"conc", cs}
	default:
		if rng.IntN(15) == 0 {
			return []any{"ru", nil}
		}
		return []any{"ru", []any{edF(rng.Float64() * 100), edF(rng.Float64() * 10), edPos(rng)}}
	}
}

func edF(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func edRandPlan(rng *rand.Rand, depth int, id *int) any {
	*id++
	my := *id
	if rng.IntN(8) == 0 {
		my = rng.IntN(*id) + 1 // a repeated id
	}
	kids := []any{}
	if depth < 3 {
		for range rng.IntN(3) {
			kids = append(kids, edRandPlan(rng, depth+1, id))
		}
	}
	cost := edF(float64(rng.IntN(100000)) / 7)
	if rng.IntN(40) == 0 {
		cost = []string{"NaN", "+Inf", "-0", "1e300", "1e-7"}[rng.IntN(5)]
	}
	return []any{my, []string{"DataSource", "Projection", "Selection", "TableReader", "HashJoin"}[rng.IntN(5)],
		[]string{"", "eq(t.a, 1)", "<x> & \"y\"", "中文"}[rng.IntN(4)], cost, kids}
}

func edRandOptTrace(rng *rand.Rand) []any {
	steps := []any{[]any{"init", rng.IntN(25) == 0}}
	id := 0
	hasBefore := false
	for range rng.IntN(8) + 1 {
		c := rng.IntN(10)
		if (c == 2 || c == 3) && !hasBefore && rng.IntN(20) != 0 {
			c = 0
		}
		switch c {
		case 0, 1:
			hasBefore = true
			steps = append(steps, []any{"before", rng.IntN(20), []string{"column_prune", "predicate_push_down", "<agg>"}[rng.IntN(3)], edRandPlan(rng, 0, &id)})
		case 2, 3:
			steps = append(steps, []any{"step", rng.IntN(20), "Selection", []string{"", "reason <a>"}[rng.IntN(2)], "removed"})
		case 4:
			steps = append(steps, []any{"final", edRandPlan(rng, 0, &id)})
		case 5:
			var p any = edRandPlan(rng, 0, &id)
			if rng.IntN(10) == 0 {
				p = nil
			}
			steps = append(steps, []any{"cand", p, []string{"", "DataSource_1"}[rng.IntN(2)]})
		case 6:
			steps = append(steps, []any{"pfinal", edRandPlan(rng, 0, &id)})
		case 7:
			ps := [][]any{}
			for range rng.IntN(4) {
				var v []any
				switch rng.IntN(5) {
				case 0:
					v = []any{"f", edF(rng.Float64() * 1000)}
				case 1:
					v = []any{"i", rng.IntN(1000)}
				case 2:
					v = []any{"s", []string{"", "x", "<t>"}[rng.IntN(3)]}
				case 3:
					v = []any{"b", rng.IntN(2) == 0}
				default:
					v = []any{"f", []string{"NaN", "1e21", "1e-7", "-0"}[rng.IntN(4)]}
				}
				ps = append(ps, []any{[]string{"a", "b", "rowSize", ""}[rng.IntN(4)], v})
			}
			steps = append(steps, []any{"cost", "k" + strconv.Itoa(rng.IntN(5)), rng.IntN(30), "TableScan", "desc <&>", ps, edF(rng.Float64() * 1e6)})
		case 8:
			steps = append(steps, []any{[]string{"fast", "rfinal"}[rng.IntN(2)], edRandPlan(rng, 0, &id)})
		default:
			ids := []int{}
			for range rng.IntN(5) {
				ids = append(ids, rng.IntN(5))
			}
			steps = append(steps, []any{"ids", edRandPlan(rng, 0, &id), ids})
		}
	}
	return steps
}

func execdetailsGenerate(g *vec.Gen, rng *rand.Rand) {
	mined := map[string]int{}
	srcOf := func(test, def string) string {
		if mined[test] < 5 {
			mined[test]++
			return test
		}
		return def
	}
	// fmt: TestFormatDurationForExplain, boundaries, random.
	for _, s := range []string{"0s", "1ns", "9ns", "10ns", "999ns", "1µs", "1.123µs", "1.023µs", "1.003µs", "10.456µs",
		"10.956µs", "999.056µs", "999.988µs", "1.123ms", "1.023ms", "1.003ms", "10.456ms", "10.956ms", "999.056ms", "999.988ms",
		"1.123s", "1.023s", "1.003s", "10.456s", "10.956s", "16m39.056s", "16m39.988s", "24h16m39.388662s", "9.412345ms",
		"10.412345ms", "5.999s", "100.45µs"} {
		d, err := time.ParseDuration(s)
		must(err)
		g.Add("TestFormatDurationForExplain", "fmt", "fd", int64(d))
	}
	for _, f := range []string{"fd", "cgfd"} {
		for _, n := range []int64{-1, -1000, -1001, 1000, 1001, 9999, 10000, 999999, 1000000, 9999999, 10000000, 9995000, 9994999,
			999999999, 1000000000, 9995000000, math.MaxInt64, math.MinInt64, math.MaxInt64 - 499999999} {
			g.Add("sys/fmt", "fmt", f, n)
		}
		for range 150 {
			g.Add("rand/fmt", "fmt", f, edRandDur(rng))
		}
	}
	for _, f := range []string{"fb", "bts"} {
		for _, n := range []int64{0, 1, -1, 1023, 1024, 1025, 10239, 10240, 1 << 20, 1<<20 + 1, 1 << 30, 1<<30 + 1, 10 << 30, math.MaxInt64, math.MinInt64} {
			g.Add("sys/fmt", "fmt", f, n)
		}
		for range 80 {
			g.Add("rand/fmt", "fmt", f, rng.Int64N(1<<uint(rng.IntN(62)+1)))
		}
	}
	// gomath.
	for _, x := range []float64{0, math.Copysign(0, -1), 1, -1, 0.5, 0.7, 0.70000001, 0.66, 0.6600001, 2.414213562373095, 2.5, 1e-300,
		math.Pi / 2, math.Pi / 4, math.Pi, 3 * math.Pi / 4, 1 << 29, 1<<29 + 0.5, 1e10, 1e300, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(), 1.0000001, -0.9999} {
		g.Add("sys/gomath", "gomath", "sin", edF(x))
		g.Add("sys/gomath", "gomath", "asin", edF(x))
	}
	for range 400 {
		var x float64
		switch rng.IntN(4) {
		case 0:
			x = (rng.Float64()*2 - 1) * math.Pi / 2
		case 1:
			x = (rng.Float64()*2 - 1) * 100
		case 2:
			x = (rng.Float64()*2 - 1) * math.Ldexp(1, rng.IntN(1000))
		default:
			x = rng.Float64()*2 - 1
		}
		g.Add("rand/gomath", "gomath", []string{"sin", "asin"}[rng.IntN(2)], edF(x))
	}
	// tdigest.
	qs := []any{[]any{"q", "0"}, []any{"q", "0.0001"}, []any{"q", "0.5"}, []any{"q", "0.8"}, []any{"q", "0.9"}, []any{"q", "0.95"},
		[]any{"q", "0.999"}, []any{"q", "1"}, []any{"q", "-0.1"}, []any{"q", "1.5"}}
	g.Add("sys/tdigest", "tdigest", []any{[]any{"q", "0.5"}})
	g.Add("sys/tdigest", "tdigest", []any{[]any{"add", "5", "1"}, []any{"q", "0.5"}, []any{"cent"}})
	g.Add("sys/tdigest", "tdigest", []any{[]any{"add", "5", "1"}, []any{"add", "NaN", "1"}, []any{"add", "6", "-1"}, []any{"q", "0.5"}, []any{"cent"}})
	g.Add("sys/tdigest", "tdigest", []any{[]any{"gen", "int", 100, 1, "1"}, []any{"q", "NaN"}})
	for _, kind := range []string{"int", "dup", "wide", "float", "mono"} {
		for _, n := range []int{2, 10, 999, 1000, 1001, 2000, 8001, 9000} {
			w := []string{"1", "int", "frac"}[n%3]
			steps := []any{[]any{"gen", kind, n, uint64(n) * 31, w}}
			if w == "frac" {
				// With fractional weights q*processedWeight can pass the last cumulative
				// weight (they are summed in different orders) and Quantile(1) indexes out
				// of range in Go: keep q below 1 so the record keeps its other results.
				steps = append(steps, qs[:7]...)
			} else {
				steps = append(steps, qs...)
			}
			steps = append(steps, []any{"cent"})
			g.Add("sys/tdigest", "tdigest", steps)
		}
	}
	for range 60 {
		steps := []any{}
		for range rng.IntN(4) + 1 {
			switch rng.IntN(4) {
			case 0:
				steps = append(steps, []any{"add", edF(rng.Float64() * 1000), edF([]float64{1, 2, 0.5, rng.Float64() * 3}[rng.IntN(4)])})
			case 1:
				cl := [][]string{}
				for range rng.IntN(300) {
					cl = append(cl, []string{edF(float64(rng.IntN(500))), edF(float64(rng.IntN(4) + 1))})
				}
				steps = append(steps, []any{"list", cl})
			default:
				steps = append(steps, []any{"gen", []string{"int", "dup", "wide", "float", "mono"}[rng.IntN(5)], rng.IntN(12000), rng.Uint64(),
					[]string{"1", "int", "frac"}[rng.IntN(3)]})
			}
			steps = append(steps, qs[rng.IntN(len(qs)-3)])
		}
		steps = append(steps, []any{"cent"})
		g.Add("rand/tdigest", "tdigest", steps)
	}
	// tdigest: NaN / ±Inf centroids (AddCentroidList does not filter NaN means; Go's math.Min /
	// math.Max propagate NaN) and NaN / ±Inf weights.
	for _, cl := range [][][]string{{{"NaN", "1"}, {"1", "1"}}, {{"1", "1"}, {"NaN", "1"}, {"2", "1"}}, {{"+Inf", "1"}, {"1", "1"}},
		{{"-Inf", "1"}, {"3", "2"}}, {{"1", "NaN"}, {"2", "1"}}, {{"1", "+Inf"}, {"2", "1"}}, {{"5", "0"}, {"6", "0"}}} {
		g.Add("sys/tdigest", "tdigest", []any{[]any{"gen", "int", 50, 3, "1"}, []any{"list", cl}, []any{"q", "0.5"}, []any{"q", "0.01"}, []any{"cent"}})
		g.Add("sys/tdigest", "tdigest", []any{[]any{"list", cl}, []any{"q", "0"}, []any{"q", "0.01"}, []any{"q", "0.5"}, []any{"cent"}})
	}
	// tdigest: AddCentroidList of a big list (exercises the room / skip logic).
	for _, n := range []int{8000, 8001, 8002, 16005} {
		cl := [][]string{}
		for i := range n {
			cl = append(cl, []string{edF(float64(i % 977)), "1"})
		}
		g.Add("sys/tdigest", "tdigest", []any{[]any{"list", cl}, []any{"q", "0"}, []any{"q", "0.01"}, []any{"q", "0.5"}, []any{"cent"}})
	}
	// percentile.
	pq := []any{[]any{"p", "0"}, []any{"p", "0.8"}, []any{"p", "0.9"}, []any{"p", "0.95"}, []any{"max"}, []any{"min"}, []any{"size"}, []any{"sum"}}
	g.Add("sys/percentile", "percentile", []any{[]any{"max"}, []any{"min"}, []any{"size"}, []any{"sum"}, []any{"int64", -5}})
	g.Add("sys/percentile", "percentile", []any{[]any{"p", "0.5"}})
	g.Add("sys/percentile", "percentile", []any{[]any{"add", 5, "a"}, []any{"p", "1"}})
	for _, n := range []int{1, 2, 5, 998, 999, 1000, 1001, 2500} {
		for _, kind := range []string{"int", "dup", "wide"} {
			steps := []any{[]any{"gen", kind, n, uint64(n) + 7}}
			steps = append(steps, pq...)
			g.Add("sys/percentile", "percentile", steps)
		}
	}
	for range 80 {
		steps := []any{[]any{"gen", []string{"int", "dup", "wide", "mono"}[rng.IntN(4)], rng.IntN(1300) + 1, rng.Uint64()}}
		for range rng.IntN(4) + 1 {
			switch rng.IntN(5) {
			case 0:
				steps = append(steps, []any{"add", edRandDur(rng), "a" + strconv.Itoa(rng.IntN(3))})
			case 1:
				sub := []any{[]any{"gen", []string{"int", "dup", "wide"}[rng.IntN(3)], []int{0, 3, 500, 999, 1000, 1500}[rng.IntN(6)], rng.Uint64()}}
				steps = append(steps, []any{"merge", append(sub, pq[rng.IntN(len(pq))])})
			case 2:
				steps = append(steps, []any{"int64", rng.Int64()})
			default:
				steps = append(steps, []any{"gen", []string{"int", "dup", "wide", "mono"}[rng.IntN(4)], rng.IntN(1300), rng.Uint64()})
			}
			steps = append(steps, pq[rng.IntN(len(pq))])
		}
		steps = append(steps, pq...)
		g.Add("rand/percentile", "percentile", steps)
	}
	// details (TestString-like, then random).
	for range 150 {
		src := srcOf("TestString", "rand/details")
		ed := edRandExec(rng)
		if ed == nil {
			ed = map[string]any{}
		}
		g.Add(src, "details", ed)
	}
	// cgdetails.
	for range 150 {
		g.Add("rand/cgdetails", "cgdetails", edRandCommit(rng), edRandCommit(rng), edRandLock(rng), edRandLock(rng), edRandTiKV(rng),
			[]any{edF(rng.Float64() * 100), edF(rng.Float64()), edPos(rng), edF(rng.Float64() * 1e6), edF(float64(rng.IntN(5))), edPos(rng)})
	}
	// sync.
	for range 250 {
		steps := []any{}
		for range rng.IntN(10) + 1 {
			switch rng.IntN(10) {
			case 0, 1:
				steps = append(steps, []any{"merge", edRandExec(rng), edRandCommit(rng)})
			case 2, 3:
				steps = append(steps, []any{"cop", edRandCop(rng), edPos(rng)})
			case 4:
				steps = append(steps, []any{"lock", edRandLock(rng)})
			case 5:
				steps = append(steps, []any{"slock", edRandLock(rng)})
			case 6:
				steps = append(steps, []any{"reset"})
			case 7:
				steps = append(steps, []any{"copgen", []string{"int", "dup", "wide"}[rng.IntN(3)], []int{3, 999, 1000, 1001, 1500}[rng.IntN(5)], rng.Uint64()})
			default:
				steps = append(steps, []any{[]string{"get", "tasks", "sum"}[rng.IntN(3)]})
			}
		}
		steps = append(steps, []any{"get"}, []any{"tasks"}, []any{"sum"})
		g.Add("rand/sync", "sync", steps)
	}
	// coll.
	for range 400 {
		src := srcOf("TestRootRuntimeStats", "")
		if src == "" {
			src = srcOf("TestCopRuntimeStats", "rand/coll")
		}
		steps := []any{}
		ids := []int{1, 2, 3, 12, 99}
		for range rng.IntN(12) + 1 {
			id := ids[rng.IntN(len(ids))]
			st := uint8([]int{0, 0, 1, 2, 255, 7}[rng.IntN(6)])
			switch rng.IntN(14) {
			case 0, 1:
				steps = append(steps, []any{"basic", id, edPos(rng), rng.IntN(100)})
			case 2:
				steps = append(steps, []any{"setrows", id, rng.Int64N(1000)})
			case 3, 4:
				steps = append(steps, []any{"reg", id, edRandStats(rng)})
			case 5, 6:
				steps = append(steps, []any{"cop", id, st, edRandScan(rng), edRandTime(rng), edRandSummary(rng, false)})
			case 7:
				steps = append(steps, []any{"one", id, st, edRandSummary(rng, true)})
			case 8:
				steps = append(steps, []any{"cops", id, st, []string{"int", "dup", "wide"}[rng.IntN(3)], []int{1, 2, 999, 1000, 1001, 2000}[rng.IntN(6)], rng.Uint64()})
			case 9:
				steps = append(steps, []any{"actrows", id})
			case 10:
				steps = append(steps, []any{"new"})
			default:
				steps = append(steps, []any{[]string{"root", "copstr", "coprows", "actrows", "exists"}[rng.IntN(5)], id})
			}
		}
		for _, id := range ids {
			steps = append(steps, []any{"root", id}, []any{"copstr", id}, []any{"exists", id})
		}
		// bcop works on clones that share the percentile's slice and t-digest with the
		// collection's stats (Go's struct copy), so it comes last: nothing reads the
		// collection after it (the Rust clone is deep, block spec §4).
		for range rng.IntN(3) {
			var other any
			if rng.IntN(2) == 0 {
				other = ids[rng.IntN(len(ids))]
			}
			steps = append(steps, []any{"bcop", ids[rng.IntN(len(ids))], other})
		}
		g.Add(src, "coll", steps)
	}
	// stats.
	for range 300 {
		src := srcOf("TestRuntimeStatsWithCommit", "rand/stats")
		a := edRandStats(rng)
		b := edRandStats(rng)
		if rng.IntN(2) == 0 {
			b = edRandStatsLike(rng, a) // the same type more often
		}
		g.Add(src, "stats", a, b)
	}
	// opttrace.
	for range 300 {
		g.Add("rand/opttrace", "opttrace", edRandOptTrace(rng))
	}
	// ce.
	for range 60 {
		recs, ids := []any{}, []any{}
		for range rng.IntN(8) {
			recs = append(recs, map[string]any{"table_name": []string{"t", "t1", "<x>"}[rng.IntN(3)], "type": []string{"Column", "Index"}[rng.IntN(2)],
				"expr": []string{"a>1", "b<2", ""}[rng.IntN(3)], "row_count": rng.Uint64N(4)})
			ids = append(ids, []any{rng.IntN(2)})
		}
		g.Add("rand/ce", "ce", recs, ids)
	}
	// codec.
	for _, c := range [][]any{{"DataSource", 1}, {"", 0}, {"a_b", -5}, {"中", 1 << 40}, {"x", math.MinInt64}} {
		g.Add("sys/codec", "codec", c[0], c[1])
	}
	for range 10 {
		g.Add("rand/codec", "codec", "TableReader", rng.IntN(1000))
	}
}

// edRandStatsLike: a random payload of the same RuntimeStats type as a.
func edRandStatsLike(rng *rand.Rand, a any) any {
	tp := a.([]any)[0]
	for range 50 {
		b := edRandStats(rng)
		if b.([]any)[0] == tp {
			return b
		}
	}
	return a
}
