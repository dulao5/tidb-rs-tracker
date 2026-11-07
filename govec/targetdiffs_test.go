package main

// targetDiffs: the records whose output differs between the committed amd64
// and arm64 files, with the reason (see TestTargetsDiffer). An entry is either
// one call (Args: the record's args as compact JSON) or every differing record
// of an op (Args: "*", with N the exact number of them).
type targetDiff struct {
	Domain, Op string
	Args       string
	N          int
	Why        string
}

var targetDiffs = []targetDiff{
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
}

// targetsPending: domains whose vectors are committed for one target only so far
// (the other comes from the CI artifact of the PR that adds the domain), with the
// reason. TestTargetsDiffer fails for a domain with one target that is not listed
// here, and for a listed domain that has both targets (remove the entry then).
var targetsPending = map[string]string{}
