package main

// targetDiffs: the records whose output differs between the committed amd64
// and arm64 files, with the reason (see TestTargetsDiffer). An entry is either
// one call (Args: the record's args as compact JSON) or every differing record
// of an op (Args: "*", with N the exact number of them). An op-wide entry may
// carry a Kind (see diffKind): it then counts only the differing records of that
// kind, and the entry without Kind counts the rest.
type targetDiff struct {
	Domain, Op string
	Args       string
	Kind       string
	N          int
	Why        string
}

var targetDiffs = []targetDiff{
	// cardinality (D10.6 / D11.1, VS1-29): M-FMA in the estimation formulas, fused on arm64
	// only (go_fma_sites.tsv): Histogram.LessRowCountWithBktIdx (histogram.go:555, reached by
	// BetweenRowCount / LessRowCount) and CalcTotalSelectivityForMVIdxPath (selectivity.go:535).
	{Domain: "cardinality", Op: "hist", Args: "*", N: 7,
		Why: "M-FMA: Histogram.LessRowCountWithBktIdx preCount + frac*(...) fused on arm64 only"},
	{Domain: "cardinality", Op: "mv_sel", Args: "*", N: 1,
		Why: "M-FMA: CalcTotalSelectivityForMVIdxPath (sel + t) - t*sel fused on arm64 only"},
	// physops (D10.1, VS1-27): M-FMA in calcPagingCost (task.go:583 and :588, the
	// multiply of `pagingCst *= indexSelectivity` fused with `pagingCst - seekFactor`),
	// fused on arm64 only (go_fma_sites.tsv).
	{Domain: "physops", Op: "paging", Args: "*", N: 98,
		Why: "M-FMA: calcPagingCost seekCnt*seek + expectCnt*cpu and pagingCst*sel - seek fused on arm64 only"},
	// costver2 (D10.5, VS1-30): the v2 formulas call math.Log2 (scanCostVer2's
	// log2(row-size), orderCostVer2's log2(n)): log10.go:36 is fused on arm64 and Log is
	// assembly on amd64 (log_amd64.s), so the last bits of a cost can differ. No line of
	// plan_cost_ver2.go itself is in go_fma_sites.tsv.
	{Domain: "costver2", Op: "plan", Args: "*", N: 12,
		Why: "math.Log2: FMA on arm64 (log10.go:36), assembly Log on amd64"},
	{Domain: "costver2", Op: "formula", Args: "*", N: 8,
		Why: "math.Log2: FMA on arm64 (log10.go:36), assembly Log on amd64"},
	// execdetails (D14.0). M-FMA in the dependencies (not in the TiDB packages, which
	// fuse nothing): influxdata/tdigest's Quantile / weightedAverage (tdigest.go:173,
	// 181, 182, 260) and Go's math.Sin / math.Asin (sin.go:227-238, asin.go:40,
	// atan.go:69-70) are fused on arm64 only. A Percentile switches to the t-digest at
	// 1000 values, so the P80 / P90 / P95 of cop tasks (EXPLAIN ANALYZE, slow log,
	// statement summary) differ in the last bits between platforms.
	{Domain: "execdetails", Op: "tdigest", Args: "*", N: 58,
		Why: "M-FMA: tdigest Quantile / weightedAverage and math.Sin / math.Asin (scale function) fused on arm64 only"},
	{Domain: "execdetails", Op: "percentile", Args: "*", N: 64,
		Why: "M-FMA: Percentile.GetPercentile through the t-digest (>= 1000 values), fused on arm64 only"},
	{Domain: "execdetails", Op: "sync", Args: "*", N: 18,
		Why: "M-FMA: SyncExecDetails.CopTasksDetails P90 through the t-digest (>= 1000 cop tasks), fused on arm64 only"},
	{Domain: "execdetails", Op: "gomath", Args: "*", N: 12,
		Why: "M-FMA: math.Sin / math.Asin polynomial and reduction steps fused on arm64 only"},
	// expr (D08.1 part 1): Constant.EvalInt of a NaN float datum with a hybrid RetType
	// goes through Datum.ToInt64, i.e. int64(NaN).
	// Hardware default NaN of an invalid DOUBLE operation (Inf/Inf, Inf-Inf, 0*Inf in the
	// D08.5 arithmetic records): x86 0xFFF8000000000000 (negative), ARM 0x7FF8000000000000.
	// Go and Rust both use the hardware operation, so each target replays its own file.
	// D08.5 batch 3 (bit operators): 8 "bitop" records CAST a NaN DOUBLE argument to INT
	// (builtinCastRealAsIntSig), i.e. M-F2I: the conversion of NaN differs by target.
	{Domain: "cast", Op: "arbuild", Args: "*", N: 13,
		Why: "5: hardware default NaN of an invalid operation (Inf/Inf, 0*Inf): x86 negative, ARM positive; 8: M-F2I, a NaN DOUBLE cast to INT (bit operators)"},
	// D08.5 batch 4 (math functions): Go's math package is target dependent. Exp is
	// assembly on amd64 (exp_amd64.s, with an FMA path on AVX+FMA CPUs) and arm64
	// (exp_arm64.s); Log is assembly on amd64 only (log_amd64.s, which does not normalize
	// subnormals: Log(5e-324) is -709.09 there, -744.44 elsewhere); Log2 / Log10 / Pow /
	// trig go through them, and gc fuses the pure-Go multiply-adds on arm64 (M-FMA). Sqrt
	// and Pow of a negative base return the hardware default NaN (x86 negative, ARM
	// positive). tidb_base::gostd::math ports each target's code, so each target replays
	// its own file.
	{Domain: "cast", Op: "mathbuild", Args: "*", N: 228,
		Why: "M-FMA and Go's math assembly (Exp amd64/arm64, Log amd64), hardware default NaN of Sqrt / Pow"},
	{Domain: "cast", Op: "mathclone", Args: "*", N: 10,
		Why: "M-FMA and Go's math assembly (Exp amd64/arm64, Log amd64) in the cloned functions"},
	{Domain: "cast", Op: "gomath", Args: "*", N: 306,
		Why: "Go math: 162 Sqrt and 10 Pow hardware default NaN; Exp / Log assembly (amd64 Log of subnormals); arm64 FMA fusing"},
	// D08.1 part 3 (VECTOR functions): the distance functions accumulate in float32
	// (types/vector_functions.go), fused on arm64 only (FMADDS, M-FMA, as the "vector"
	// domain); a NaN result is NULL, so only the rounding shows. VEC_L2_NORM sums float64
	// squares of float32 elements, which are exact, so it does not differ.
	{Domain: "cast", Op: "vecbuild", Args: "*", N: 14,
		Why: "M-FMA: VEC_*_DISTANCE / VEC_NEGATIVE_INNER_PRODUCT float32 sums fused on arm64 only (FMADDS rounding)"},
	{Domain: "expr", Op: "const", Args: "*", N: 3,
		Why: "M-F2I: Constant.EvalInt of a NaN float datum (hybrid RetType) reaches int64(NaN): MinInt64 on amd64, 0 on arm64"},
	// M-FMA: gc fuses the two word sums of MyDecimal.ToFloat64 (mydecimal.go:1118,
	// 1125) into FMA on arm64, not on amd64 (GOAMD64=v1); domains.decimalFMA
	// generates the 44 records whose result depends on it (README "目标平台").
	{Domain: "decimal", Op: "div_then_to_float64", Args: "*", N: 44,
		Why: "M-FMA: MyDecimal.ToFloat64 word sums fused on arm64 only"},

	// M-F2I: float -> integer conversions of out-of-range values and NaN.
	{Domain: "config", Op: "f2i", Args: "*", N: 824,
		Why: "M-F2I: gostd::conv vectors of the target-dependent conversions (boundaries, NaN, ±Inf, random)"},
	{Domain: "config", Op: "load", Args: `["[tikv-client]\ngrpc-keepalive-timeout = 1e300\n"]`,
		Why: "M-F2I: grpc-keepalive-timeout float -> uint conversion overflows"},
	{Domain: "config", Op: "load", Args: `["[tikv-client]\ngrpc-keepalive-timeout = inf\n"]`,
		Why: "M-F2I: grpc-keepalive-timeout float -> uint conversion of +Inf"},
	{Domain: "config", Op: "load", Args: `["tikv-client.grpc-keepalive-timeout = 6.02e+23\n"]`,
		Why: "M-F2I: grpc-keepalive-timeout float -> uint conversion overflows"},
	{Domain: "config", Op: "load", Args: `["[tikv-client]\ngrpc-keepalive-timeout = 1e30"]`,
		Why: "M-F2I: grpc-keepalive-timeout float -> uint conversion overflows"},
	{Domain: "config", Op: "load", Args: `["[tikv-client]\ngrpc-keepalive-timeout = inf"]`,
		Why: "M-F2I: grpc-keepalive-timeout float -> uint conversion of +Inf"},
	{Domain: "time", Op: "parse_time_float64", Args: `["NaN",12,"slim:Pacific_Chatham"]`,
		Why: "M-F2I: int64(NaN) is 0 on arm64 (0000-00-00, no error), MinInt64 on amd64 (Incorrect time value)"},
	{Domain: "time", Op: "parse_time_float64", Args: `["+Inf",28,"fixed::-36000"]`,
		Why: "M-F2I: int64(+Inf) saturates on arm64, MinInt64 on amd64"},
	{Domain: "time", Op: "parse_time_float64", Args: `["1e19",12,"fat:America_New_York"]`,
		Why: "M-F2I: int64(1e19) saturates on arm64, MinInt64 on amd64"},
	{Domain: "parser_types", Op: "duration", Args: `["100000000000d"]`,
		Why: "M-F2I: time.Duration(float) overflows int64: MinInt64 on amd64, saturates on arm64"},
	{Domain: "parser_types", Op: "duration", Args: `["9223372036h"]`,
		Why: "M-F2I: time.Duration(float) overflows int64: MinInt64 on amd64, saturates on arm64"},
	{Domain: "parser_types", Op: "duration", Args: `["106752d"]`,
		Why: "M-F2I: time.Duration(float) overflows int64: MinInt64 on amd64, saturates on arm64"},

	// The distance functions (vector_functions.go:44, 71, 100-102). M-FMA: the float32
	// sums are fused on arm64 only (FMADDS), so rounding differs, and an unfused
	// product that overflows to Inf can make Inf-Inf = NaN on amd64 where the fused
	// sum stays infinite. Not M-FMA: the hardware NaN rules (see diffKind).
	{Domain: "vector", Op: "distance", Args: "*", N: 888,
		Why: "M-FMA: VectorFloat32 distance sums fused on arm64 only (rounding)"},
	{Domain: "vector", Op: "distance", Args: "*", Kind: "nan-vs-number", N: 295,
		Why: "M-FMA: unfused product overflows to Inf on amd64 (Inf-Inf = NaN), the fused sum on arm64 does not"},
	{Domain: "vector", Op: "distance", Args: "*", Kind: "nan-sign", N: 124,
		Why: "hardware default NaN of an invalid operation: x86 0xFFC00000 (negative), ARM 0x7FC00000"},
	{Domain: "vector", Op: "distance", Args: "*", Kind: "nan-payload", N: 544,
		Why: "hardware NaN propagation with two NaN operands: x86 returns the first source, ARM the first SNaN then QNaN (addend, op1, op2)"},

	// datum (D01.1/D01.4). Truncate(±0, dec >= 309): math.Pow10 is +Inf, 0*Inf is the
	// hardware default NaN, so the "truncate" field of float_helper differs in sign.
	{Domain: "datum", Op: "float_helper", Args: "*", N: 8,
		Why: "hardware default NaN of 0*Inf in Truncate: x86 0xFFF8000000000000 (negative), ARM 0x7FF8000000000000"},
	// ConvertFloatToInt(NaN): both bound checks are false, so int64(NaN) is returned.
	{Domain: "datum", Op: "convert_num", Args: "*", N: 10,
		Why: "M-F2I: ConvertFloatToInt(NaN) returns int64(NaN): MinInt64 on amd64, 0 on arm64"},
	// D01.1 part 2: the same int64(NaN) reached through Datum conversions of a NaN
	// float32 / float64 datum (to integer types, YEAR, TIME via ToInt64, ToInt64 itself,
	// and d_convert_recv, which repeats d_convert over every field type).
	{Domain: "datum", Op: "d_convert", Args: "*", N: 16,
		Why: "M-F2I: a NaN float datum converted to an integer, YEAR or TIME type goes through int64(NaN)"},
	{Domain: "datum", Op: "d_to", Args: "*", N: 2,
		Why: "M-F2I: Datum.ToInt64 of a NaN float datum is int64(NaN)"},
	{Domain: "datum", Op: "d_convert_recv", Args: "*", N: 20,
		Why: "M-F2I: d_convert_recv repeats d_convert's NaN float datum to integer / YEAR / TIME conversions (int64(NaN)) over every field type"},
	// aggfuncs (D12.4 part A, the core aggregate functions): a float SUM / AVG that adds
	// +Inf and -Inf (func_sum.go:100, :111, :348; func_avg.go:377, :438, :446, :486) gets
	// the hardware default NaN of an invalid operation: x86 0xFFF8000000000000 (negative),
	// ARM 0x7FF8000000000000. The records have no NaN inputs (afGen.noNaN), so only the
	// sign of that default NaN differs. Go and Rust both use the hardware add, so each
	// target replays its own file. The outputs are chunk cells, so diffKind does not
	// classify them.
	{Domain: "aggfuncs", Op: "update_final", Args: "*", N: 93,
		Why: "hardware default NaN of Inf + -Inf in a float SUM / AVG: x86 negative, ARM positive"},
	{Domain: "aggfuncs", Op: "reset_reuse", Args: "*", N: 57,
		Why: "hardware default NaN of Inf + -Inf in a float SUM / AVG: x86 negative, ARM positive"},
	{Domain: "aggfuncs", Op: "merge", Args: "*", N: 49,
		Why: "hardware default NaN of Inf + -Inf in a float SUM / AVG (Update and MergePartialResult): x86 negative, ARM positive"},
}

// targetsPending: domains whose vectors are committed for one target only so far
// (the other comes from the CI artifact of the PR that adds the domain), with the
// reason. TestTargetsDiffer fails for a domain with one target that is not listed
// here, and for a listed domain that has both targets (remove the entry then).
var targetsPending = map[string]string{}
