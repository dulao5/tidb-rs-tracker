package domains

// Inputs for domain "decimal". Three groups, in this order:
//   1. mined:  case tables copied from pkg/types/mydecimal_test.go and
//              mydecimal_benchmark_test.go (src = Go test function name)
//   2. sys/*:  systematic boundary cases
//   3. rand/*: seeded random cases (seed in the file header)
// Expected values in the Go tests are NOT copied; Go computes every output.

import (
	"encoding/hex"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/types"
)

const decimalSeed = 20260827 // v8.5.8 commit date

func decimalGenerate(g *vec.Gen, rng *rand.Rand) {
	decimalMined(g)
	decimalSystematic(g)
	decimalRandom(g, rng)
	// Appended after the original groups so that adding them left the
	// existing records (and the rng sequence they consume) unchanged.
	decimalAlias(g, rng)
	decimalChained(g, rng)
	decimalDirty(g, rng)
}

// toBinHex runs ToBin at generation time to build from_bin / peak inputs.
func toBinHex(s string, prec, frac int) (string, bool) {
	var d types.MyDecimal
	_ = d.FromString([]byte(s))
	b, err := d.ToBin(prec, frac)
	if b == nil || (err != nil && err != types.ErrTruncated && err != types.ErrOverflow) {
		return "", false
	}
	return hex.EncodeToString(b), true
}

func precFrac(s string) (int, int) {
	var d types.MyDecimal
	_ = d.FromString([]byte(s))
	return d.PrecisionAndFrac()
}

var roundModeNames = []string{"HalfUp", "Truncate", "Ceiling"}

// ---------------------------------------------------------------- mined

func decimalMined(g *vec.Gen) {
	// from: pkg/types/mydecimal_test.go TestFromInt
	for _, v := range []int64{-12345, -1, 1, -9223372036854775807, -9223372036854775808} {
		g.Add("TestFromInt", "from_int", v)
	}
	// from: pkg/types/mydecimal_test.go TestFromUint
	for _, v := range []uint64{12345, 0, 18446744073709551615} {
		g.Add("TestFromUint", "from_uint", v)
	}
	// from: pkg/types/mydecimal_test.go TestToInt
	for _, s := range []string{"18446744073709551615", "-1", "1", "-1.23", "-9223372036854775807",
		"-9223372036854775808", "9223372036854775808", "-9223372036854775809"} {
		g.Add("TestToInt", "to_int", s)
	}
	// from: pkg/types/mydecimal_test.go TestToUint
	for _, s := range []string{"12345", "0", "18446744073709551615", "18446744073709551616", "-1",
		"1.23", "9999999999999999999999999.000"} {
		g.Add("TestToUint", "to_uint", s)
	}
	// from: pkg/types/mydecimal_test.go TestFromFloat
	for _, f := range []float64{12345, 123.45, -123.45, 0.00012345000098765, 1234500009876.5} {
		g.Add("TestFromFloat", "from_float64", FormatFloat(f))
	}
	// from: pkg/types/mydecimal_test.go TestToFloat
	for _, s := range []string{"12345", "123.45", "-123.45", "0.00012345000098765", "1234500009876.5",
		"1e39", "1e-39", "1e00", "1e001", "-9223372036854775807", "-9223372036854775808",
		"18446744073709551615", "123456789.987654321", "1", "+1", "1e23", "1E23",
		"100000000000000000000000", "123456700", "99999999999999974834176",
		"100000000000000000000001", "100000000000000008388608", "100000000000000016777215",
		"100000000000000016777216", "-1", "-0.1", "-0", "1e-20", "625e-3", "0",
		"22.222222222222222",
		"1.00000000000000011102230246251565404236316680908203125",
		"1.00000000000000011102230246251565404236316680908203124",
		"1.00000000000000011102230246251565404236316680908203126",
		"1.00000000000000033306690738754696212708950042724609375",
		"1090544144181609348671888949248", "1090544144181609348835077142190"} {
		g.Add("TestToFloat", "to_float64", s)
	}
	// from: pkg/types/mydecimal_test.go TestToHashKey (both tables; binNumbers also via ToBin(PrecisionAndFrac()))
	hashGroups := [][]string{
		{"1.1", "1.1000", "1.1000000", "1.10000000000", "01.1", "0001.1", "001.1000000"},
		{"-1.1", "-1.1000", "-1.1000000", "-1.10000000000", "-01.1", "-0001.1", "-001.1000000"},
		{".1", "0.1", "0.10", "000000.1", ".10000", "0000.10000", "000000000000000000.1"},
		{"0", "0000", ".0", ".00000", "00000.00000", "-0", "-0000", "-.0", "-.00000", "-00000.00000"},
		{".123456789123456789", ".1234567891234567890", ".12345678912345678900", ".123456789123456789000", ".1234567891234567890000", "0.123456789123456789",
			".1234567891234567890000000000", "0000000.123456789123456789000"},
		{"12345", "012345", "0012345", "0000012345", "0000000012345", "00000000000012345", "12345.", "12345.00", "12345.000000000", "000012345.0000"},
		{"123E5", "12300000", "00123E5", "000000123E5", "12300000.00000000"},
		{"123E-2", "1.23", "00000001.23", "1.2300000000000000", "000000001.23000000000000"},
	}
	for _, grp := range hashGroups {
		for _, s := range grp {
			g.Add("TestToHashKey", "to_hash_key", s)
		}
	}
	binNumbers := [][]string{
		{"1.1", "0001.1", "01.1"},
		{"-1.1", "-0001.1", "-01.1"},
		{".1", "0.1", "000000.1", "00.1"},
		{"0", "0000", "00", "-0", "-00", "-000000"},
		{".123456789123456789", "0.123456789123456789", "0000.123456789123456789", "0000000.123456789123456789"},
		{"12345", "012345", "000012345", "000000000000012345"},
		{"12300000", "123E5", "00123E5", "0000000000123E5"},
		{"123E-2", "1.23", "000001.23", "0000000000001.23"},
	}
	for _, grp := range binNumbers {
		for _, s := range grp {
			p, f := precFrac(s)
			g.Add("TestToHashKey", "props", s)
			g.Add("TestToHashKey", "to_bin", s, p, f)
		}
	}
	// from: pkg/types/mydecimal_test.go TestRemoveTrailingZeros
	// removeTrailingZeros is unexported; its digitsFrac is the last byte of ToHashKey.
	for _, s := range []string{
		"0", "0.0", ".0", ".00000000", "0.0000", "0000", "0000.0", "0000.000",
		"-0", "-0.0", "-.0", "-.00000000", "-0.0000", "-0000", "-0000.0", "-0000.000",
		"123123123", "213123.", "21312.000", "21321.123", "213.1230000", "213123.000123000",
		"-123123123", "-213123.", "-21312.000", "-21321.123", "-213.1230000", "-213123.000123000",
		"123E5", "12300E-5", "0.00100E1", "0.001230E-3",
		"123987654321.123456789000", "000000000123", "123456789.987654321", "999.999000",
	} {
		g.Add("TestRemoveTrailingZeros", "from_string", s)
		g.Add("TestRemoveTrailingZeros", "to_hash_key", s)
	}
	// from: pkg/types/mydecimal_test.go TestRoundWithHalfEven / TestRoundWithTruncate / TestRoundWithCeil
	// (all three share inputs; "HalfEven" actually calls ModeHalfUp). Same table as BenchmarkRound.
	type roundCase struct {
		in    string
		scale int
	}
	roundTable := []roundCase{
		{"123456789.987654321", 1}, {"15.1", 0}, {"15.5", 0}, {"15.9", 0}, {"-15.1", 0},
		{"-15.5", 0}, {"-15.9", 0}, {"15.1", 1}, {"-15.1", 1}, {"15.17", 1}, {"15.4", -1},
		{"-15.4", -1}, {"5.4", -1}, {".999", 0}, {"999999999", -9},
	}
	for _, c := range roundTable {
		g.Add("TestRoundWithHalfEven", "round", c.in, c.scale, "HalfUp")
		g.Add("TestRoundWithTruncate", "round", c.in, c.scale, "Truncate")
		g.Add("TestRoundWithCeil", "round", c.in, c.scale, "Ceiling")
	}
	// from: pkg/types/mydecimal_test.go TestToString
	for _, s := range []string{"123.123", "123.1230", "00123.123"} {
		g.Add("TestToString", "from_string", s)
	}
	// from: pkg/types/mydecimal_test.go TestToBinFromBin
	type binCase struct {
		in         string
		prec, frac int
	}
	for _, c := range []binCase{
		{"-10.55", 4, 2}, {"0.0123456789012345678912345", 30, 25}, {"12345", 5, 0}, {"12345", 10, 3},
		{"123.45", 10, 3}, {"-123.45", 20, 10}, {".00012345000098765", 15, 14}, {".00012345000098765", 22, 20},
		{".12345000098765", 30, 20}, {"-.000000012345000098765", 30, 20}, {"1234500009876.5", 30, 5},
		{"111111111.11", 10, 2}, {"000000000.01", 7, 3}, {"123.4", 10, 2}, {"1000", 3, 0}, {"0.1", 1, 1},
		{"0.100", 1, 1}, {"0.1000", 1, 1}, {"0.10000", 1, 1}, {"0.100000", 1, 1}, {"0.1000000", 1, 1},
		{"0.10", 1, 1},
		{"0000000000000000000000000000000000000000000.000000000000123000000000000000", 15, 15},
		{"00000000000000000000000000000.00000000000012300", 15, 15},
		{"0000000000000000000000000000000000000000000.0000000000001234000000000000000", 16, 16},
		{"00000000000000000000000000000.000000000000123400", 16, 16},
		{"0.1", 2, 2}, {"0.10", 3, 3}, {"0.1", 3, 1}, {"0.0000000000001234", 32, 17}, {"0.0000000000001234", 20, 20},
	} {
		g.Add("TestToBinFromBin", "to_bin", c.in, c.prec, c.frac)
		if h, ok := toBinHex(c.in, c.prec, c.frac); ok {
			g.Add("TestToBinFromBin", "from_bin", h, c.prec, c.frac)
		}
	}
	zeroBin, _ := toBinHex("0", 1, 0)
	for _, c := range [][2]int{{82, 1}, {-1, 1}, {10, 31}, {10, -1}} {
		g.Add("TestToBinFromBin", "to_bin", "1", c[0], c[1])
		g.Add("TestToBinFromBin", "from_bin", zeroBin, c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestDecimalBinSize
	for _, c := range [][2]int{{3, 1}, {-1, 0}, {3, 5}} {
		g.Add("TestDecimalBinSize", "bin_size", c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestCompareMyDecimal
	for _, c := range [][2]string{{"12", "13"}, {"13", "12"}, {"-10", "10"}, {"10", "-10"}, {"-12", "-13"},
		{"0", "12"}, {"-10", "0"}, {"4", "4"}, {"-1.1", "-1.2"}, {"1.2", "1.1"}, {"1.1", "1.2"}} {
		g.Add("TestCompareMyDecimal", "compare", c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestMaxDecimal (maxDecimal is unexported; NewMaxOrMinDec(false, ...) gives the same value)
	for _, c := range [][2]int{{1, 1}, {1, 0}, {2, 1}, {4, 2}, {6, 3}, {8, 4}, {10, 5}, {12, 6}, {14, 7},
		{16, 8}, {18, 9}, {20, 10}, {20, 20}, {20, 0}, {40, 20}} {
		g.Add("TestMaxDecimal", "max_or_min", false, c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestNegMyDecimal
	for _, s := range []string{"-0.0000000000000000000000000000000000000000000000000017382578996420603",
		"-13890436710184412000000000000000000000000000000000000000000000000000000000000", "0"} {
		g.Add("TestNegMyDecimal", "neg", s)
	}
	// from: pkg/types/mydecimal_test.go TestAddMyDecimal
	for _, c := range [][2]string{
		{".00012345000098765", "123.45"}, {".1", ".45"}, {"1234500009876.5", ".00012345000098765"},
		{"9999909999999.5", ".555"}, {"99999999", "1"}, {"989999999", "1"}, {"999999999", "1"},
		{"12345", "123.45"}, {"-12345", "-123.45"}, {"-12345", "123.45"}, {"12345", "-123.45"},
		{"123.45", "-12345"}, {"-123.45", "12345"}, {"5", "-6.0"},
		{"2" + strings.Repeat("1", 71), strings.Repeat("8", 81)}, {"-1234.1234", "1234.1234"},
	} {
		g.Add("TestAddMyDecimal", "add", c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestSubMyDecimal
	for _, c := range [][2]string{
		{".00012345000098765", "123.45"}, {"1234500009876.5", ".00012345000098765"}, {"9999900000000.5", ".555"},
		{"1111.5551", "1111.555"}, {".555", ".555"}, {"10000000", "1"}, {"1000001000", ".1"},
		{"1000000000", ".1"}, {"12345", "123.45"}, {"-12345", "-123.45"}, {"123.45", "12345"},
		{"-123.45", "-12345"}, {"-12345", "123.45"}, {"12345", "-123.45"}, {"12.12", "12.12"},
	} {
		g.Add("TestSubMyDecimal", "sub", c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestMulMyDecimal
	for _, c := range [][2]string{
		{"12", "10"}, {"-123.456", "98765.4321"}, {"-123456000000", "98765432100000"}, {"123456", "987654321"},
		{"123456", "9876543210"}, {"123", "0.01"}, {"123", "0"},
		{"-0.0000000000000000000000000000000000000000000000000017382578996420603", "-13890436710184412000000000000000000000000000000000000000000000000000000000000"},
		{"1" + strings.Repeat("0", 60), "1" + strings.Repeat("0", 60)},
		{"0.5999991229316", "0.918755041726043"}, {"0.5999991229317", "0.918755041726042"}, {"0.000", "-1"},
	} {
		g.Add("TestMulMyDecimal", "mul", c[0], c[1])
	}
	// from: pkg/types/mydecimal_test.go TestDivModMyDecimal (4 tables: div fracIncr=5, mod, div fracIncr=4, mod)
	for _, c := range [][2]string{
		{"120", "10"}, {"123", "0.01"}, {"120", "100000000000.00000"}, {"123", "0"}, {"0", "0"},
		{"-12193185.1853376", "98765.4321"}, {"121931851853376", "987654321"}, {"0", "987"}, {"1", "3"},
		{"1.000000000000", "3"}, {"1", "1"}, {"0.0123456789012345678912345", "9999999999"},
		{"10.333000000", "12.34500"}, {"10.000000000060", "2"}, {"51", "0.003430"},
	} {
		g.Add("TestDivModMyDecimal", "div", c[0], c[1], 5)
	}
	for _, c := range [][2]string{
		{"234", "10"}, {"234.567", "10.555"}, {"-234.567", "10.555"}, {"234.567", "-10.555"},
		{"99999999999999999999999999999999999999", "3"}, {"51", "0.003430"}, {"0.0000000001", "1.0"}, {"0.000", "0.1"},
		{"1", "2.0"}, {"1.0", "2"}, {"2.23", "3"},
	} {
		g.Add("TestDivModMyDecimal", "mod", c[0], c[1])
	}
	for _, c := range [][2]string{{"1", "1"}, {"1.00", "1"}, {"1", "1.000"}, {"2", "3"}, {"51", "0.003430"}, {"0.000", "0.1"}} {
		g.Add("TestDivModMyDecimal", "div", c[0], c[1], 4)
	}
	// from: pkg/types/mydecimal_test.go TestMaxOrMinMyDecimal
	for _, c := range []struct {
		neg        bool
		prec, frac int
	}{{true, 2, 1}, {false, 1, 1}, {true, 1, 0}, {false, 0, 0}, {false, 4, 2}} {
		g.Add("TestMaxOrMinMyDecimal", "max_or_min", c.neg, c.prec, c.frac)
	}
	// from: pkg/types/mydecimal_test.go TestShiftMyDecimal
	// The first table runs at wordBufLen=9 (default). The second table ran at
	// wordBufLen=2 in Go (unexported global, cannot be set from outside the
	// package); we run those inputs at the default 9, so outputs differ from
	// the Go test's expectations by design.
	type shiftCase struct {
		in    string
		shift int
	}
	for _, c := range []shiftCase{
		{"123.123", 1}, {"123457189.123123456789000", 1}, {"123457189.123123456789000", 8},
		{"123457189.123123456789000", 9}, {"123457189.123123456789000", 10}, {"123457189.123123456789000", 17},
		{"123457189.123123456789000", 18}, {"123457189.123123456789000", 19}, {"123457189.123123456789000", 26},
		{"123457189.123123456789000", 27}, {"123457189.123123456789000", 28},
		{"000000000000000000000000123457189.123123456789000", 26}, {"00000000123457189.123123456789000", 27},
		{"00000000000000000123457189.123123456789000", 28}, {"123", 1}, {"123", 10}, {".123", 1}, {".123", 10},
		{".123", 14}, {"000.000", 1000}, {"000.", 1000}, {".000", 1000}, {"1", 1000}, {"123.123", -1},
		{"123987654321.123456789000", -1}, {"123987654321.123456789000", -2}, {"123987654321.123456789000", -3},
		{"123987654321.123456789000", -8}, {"123987654321.123456789000", -9}, {"123987654321.123456789000", -10},
		{"123987654321.123456789000", -11}, {"123987654321.123456789000", -12}, {"123987654321.123456789000", -13},
		{"123987654321.123456789000", -14}, {"00000087654321.123456789000", -14},
	} {
		g.Add("TestShiftMyDecimal", "shift", c.in, c.shift)
	}
	for _, c := range []shiftCase{
		{"123.123", -2}, {"123.123", -3}, {"123.123", -6}, {"123.123", -7}, {"123.123", -15}, {"123.123", -16},
		{"123.123", -17}, {"123.123", -18}, {"123.123", -19}, {"123.123", -20}, {"123.123", -21},
		{".000000000123", -1}, {".000000000123", -6}, {".000000000123", -7}, {".000000000123", -8},
		{".000000000123", -9}, {".000000000123", 1}, {".000000000123", 8}, {".000000000123", 9},
		{".000000000123", 10}, {".000000000123", 17}, {".000000000123", 18}, {".000000000123", 19},
		{".000000000123", 20}, {".000000000123", 21}, {".000000000123", 22}, {".000000000123", 23},
		{".000000000123", 24}, {".000000000123", 25}, {".000000000123", 26}, {".000000000123", 27},
		{".000000000123", 28}, {"123456789.987654321", -1}, {"123456789.987654321", -2},
		{"123456789.987654321", -8}, {"123456789.987654321", -9}, {"123456789.987654321", -10},
		{"123456789.987654321", -17}, {"123456789.987654321", -18}, {"123456789.987654321", -19},
		{"123456789.987654321", -26}, {"123456789.987654321", -27}, {"123456789.987654321", 1},
		{"123456789.987654321", 2}, {"123456789.987654321", 4}, {"123456789.987654321", 8},
		{"123456789.987654321", 9}, {"123456789.987654321", 10}, {"123456789.987654321", 0},
	} {
		g.Add("TestShiftMyDecimal(wordBufLen=2 table, run at 9)", "shift", c.in, c.shift)
	}
	// from: pkg/types/mydecimal_test.go TestFromStringMyDecimal (second table: wordBufLen=1 in Go, run at 9 here)
	for _, s := range []string{
		"12345", "12345.", "123.45.", "-123.45.", ".00012345000098765", ".12345000098765",
		"-.000000012345000098765", "1234500009876.5", "123E5", "123E-2", "1e1073741823", "-1e1073741823",
		"1e18446744073709551620", "1e", "1e001", "1e00", "1eabc", "1e 1dddd ", "1e - 1", "1e -1",
		"0.00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
		"1asf", "1.1.1.1.1", "1  1", "1  ",
	} {
		g.Add("TestFromStringMyDecimal", "from_string", s)
	}
	for _, s := range []string{"123450000098765", "123450.000098765"} {
		g.Add("TestFromStringMyDecimal(wordBufLen=1 table, run at 9)", "from_string", s)
	}
	// from: pkg/types/mydecimal_test.go TestMarshalMyDecimal
	for _, s := range []string{"12345", "12345.", ".00012345000098765", ".12345000098765", "-.000000012345000098765", "123E-2"} {
		g.Add("TestMarshalMyDecimal", "marshal_json", s)
		var d types.MyDecimal
		_ = d.FromString([]byte(s))
		j, _ := d.MarshalJSON()
		g.Add("TestMarshalMyDecimal", "unmarshal_json", string(j))
	}
	// from: pkg/types/mydecimal_benchmark_test.go benchmarkMyDecimalToBinOrHashCases (BenchmarkMyDecimalToBin / ToHashKey)
	for _, s := range []string{"1.000000000000", "3", "12.000000000", "120", "120000", "100000000000.00000",
		"0.000000001200000000", "98765.4321", "-123.456000000000000000", "0", "0000000000", "0.00000000000"} {
		p, f := precFrac(s)
		g.Add("BenchmarkMyDecimalToBin", "to_bin", s, p, f)
		g.Add("BenchmarkMyDecimalToHashKey", "to_hash_key", s)
	}
}

// ---------------------------------------------------------------- aliasing

// decimalAlias: add/sub/mul/div/mod with `to` aliasing an input (op
// <name>_alias, last arg 1|2|3, see decimalBaseCoverage).
func decimalAlias(g *vec.Gen, rng *rand.Rand) {
	add := func(src, x, y string, aliases ...int) {
		for _, alias := range aliases {
			g.Add(src, "add_alias", x, y, alias)
			g.Add(src, "sub_alias", x, y, alias)
			g.Add(src, "mul_alias", x, y, alias)
			g.Add(src, "div_alias", x, y, 4, alias)
			g.Add(src, "mod_alias", x, y, alias)
		}
	}
	ar := sysArith()
	for i, x := range ar {
		add("sys/alias", x, x, 3) // f(x, x, x)
		// a band of pairs, not the full square: each value meets its neighbours
		// and a few far ones (zero, huge, many fraction digits).
		for _, j := range []int{(i + 1) % len(ar), (i + 7) % len(ar), (i + 19) % len(ar)} {
			add("sys/alias", x, ar[j], 1, 2)
		}
	}
	for i := 0; i < 60; i++ {
		x := randDecString(rng, 30, 20)
		add("rand/alias", x, randDecString(rng, 30, 20), 1, 2)
		if i%4 == 0 {
			add("rand/alias", x, x, 3)
		}
	}
}

// ---------------------------------------------------------------- chained

// chainInts are FromInt/FromUint inputs around word (9-digit) boundaries.
var chainInts = []int64{0, 1, -1, 5, -5, 9, 10, 99, 123456789, 999999999, 1000000000, -1000000000,
	1234567890123, 999999999999999999, 1000000000000000000, -999999999999999999,
	math.MaxInt64, math.MinInt64, math.MaxInt32, math.MinInt32}

// decimalChained: <producer>_then_<consumer> ops (see chainProducers).
func decimalChained(g *vec.Gen, rng *rand.Rand) {
	ar := sysArith()
	type prod struct {
		src  string
		args []any
	}
	var prods = map[string][]prod{}
	for _, v := range chainInts {
		prods["from_int"] = append(prods["from_int"], prod{"sys/chain", []any{v}})
		if v >= 0 {
			prods["from_uint"] = append(prods["from_uint"], prod{"sys/chain", []any{uint64(v)}})
		}
	}
	for _, v := range []uint64{math.MaxUint64, math.MaxUint64 - 1, math.MaxInt64 + 1, 18446744073} {
		prods["from_uint"] = append(prods["from_uint"], prod{"sys/chain", []any{v}})
	}
	for _, s := range []string{"0", "0.5", "-0.5", "1.45", "999999999.5", "-999999999.5", "123456789.123456789",
		"0.000000000500", "9999999999.99", "12345.678", "-12345.678", "1" + strings.Repeat("0", 18)} {
		for _, f := range []int{-2, 0, 1, 5} {
			prods["round"] = append(prods["round"], prod{"sys/chain", []any{s, f, "HalfUp"}})
		}
	}
	for i := 0; i < len(ar); i += 2 {
		x, y := ar[i], ar[(i+5)%len(ar)]
		prods["add"] = append(prods["add"], prod{"sys/chain", []any{x, y}})
		prods["mul"] = append(prods["mul"], prod{"sys/chain", []any{x, y}})
	}
	// seeded random producers
	for i := 0; i < 10; i++ {
		v := int64(rng.Uint64()) >> rng.IntN(64)
		prods["from_int"] = append(prods["from_int"], prod{"rand/chain", []any{v}})
		prods["from_uint"] = append(prods["from_uint"], prod{"rand/chain", []any{rng.Uint64() >> rng.IntN(64)}})
		prods["round"] = append(prods["round"], prod{"rand/chain",
			[]any{randDecString(rng, 30, 20), rng.IntN(16) - 4, roundModeNames[rng.IntN(3)]}})
		prods["add"] = append(prods["add"], prod{"rand/chain", []any{randDecString(rng, 30, 15), randDecString(rng, 30, 15)}})
		prods["mul"] = append(prods["mul"], prod{"rand/chain", []any{randDecString(rng, 20, 10), randDecString(rng, 20, 10)}})
	}

	for _, p := range chainProducerNames {
		for k, pr := range prods[p] {
			for _, c := range chainConsumers {
				var rest []any
				switch c {
				case "to_bin":
					rest = []any{[]int{20, 30, 40, 65, 9, 10}[k%6], []int{0, 2, 5, 0, 0, 9}[k%6]}
				case "round", "round_inplace":
					rest = []any{[]int{-10, -1, 0, 1, 3, 9, 10}[k%7], roundModeNames[k%3]}
				case "shift":
					rest = []any{[]int{-20, -9, -3, -1, 1, 3, 9, 20}[k%8]}
				case "compare", "add", "mul":
					rest = []any{ar[(k*3)%len(ar)]}
				case "div":
					rest = []any{ar[(k*3+1)%len(ar)], 4}
				}
				g.Add(pr.src, p+chainSep+c, append(append([]any{}, pr.args...), rest...)...)
			}
		}
	}
}

// ---------------------------------------------------------------- dirty receivers

// dirtyReceivers are FromString inputs whose state the next op inherits:
// negative, fractional (resultFrac > 0), all nine words used, leftover
// nonzero high words, and FromString's own error states.
var dirtyReceivers = []string{
	"-123.456", "-1", "-0.000000001", "1.5", "0.000000000000000000000000000001",
	"-123456789012345678901234567890.123456789", strings.Repeat("9", 81),
	"-" + strings.Repeat("9", 72) + "." + strings.Repeat("9", 9), "-1e70", "abc", "-",
}

// decimalDirty: dirty_<op> ops (see dirtyOps).
func decimalDirty(g *vec.Gen, rng *rand.Rand) {
	ar := sysArith()
	binArgs := [][]any{}
	for _, c := range []struct {
		s          string
		prec, frac int
	}{{"1.5", 10, 2}, {"-12345.678", 20, 5}, {"0", 5, 0}, {"123456789012345678", 40, 0}, {"-0.000000001", 12, 10}} {
		if h, ok := toBinHex(c.s, c.prec, c.frac); ok {
			binArgs = append(binArgs, []any{h, c.prec, c.frac})
		}
	}
	// invalid precision (FromBin returns before writing) and a bad word
	binArgs = append(binArgs, []any{"80000001", 100, 0}, []any{"7fffffffffff", 10, 0}, []any{"ffffffff", 9, 0})
	args := map[string][][]any{
		"from_string": {{"2"}, {"0"}, {"-0"}, {"0.5"}, {"123456789"}, {"1e3"}, {"1e-40"}, {"abc"}, {""},
			{"99999999999999999999.9"}, {"0.0000000000"}},
		"from_int":     {{int64(0)}, {int64(5)}, {int64(-5)}, {int64(math.MaxInt64)}, {int64(math.MinInt64)}},
		"from_uint":    {{uint64(0)}, {uint64(5)}, {uint64(math.MaxUint64)}},
		"from_float64": {{"0"}, {"1.5"}, {"-2.25"}, {"1e+20"}, {"1e-07"}, {"NaN"}},
		"from_bin":     binArgs,
		"unmarshal_json": {{`{"DigitsInt":1,"DigitsFrac":0,"ResultFrac":0,"Negative":false,"WordBuf":[7,0,0,0,0,0,0,0,0]}`},
			{`{"DigitsInt":1}`}, {`null`}, {`{}`}, {`[1]`}, {`{"WordBuf":[1,2]}`}},
	}
	for _, x := range []string{"0", "1.25", "-7.5", "123.456789", "999999999.999999999"} {
		for _, f := range []int{-1, 0, 2, 10} {
			args["round"] = append(args["round"], []any{x, f, roundModeNames[(f+3)%3]})
		}
	}
	for i := 0; i < len(ar); i += 3 {
		x, y := ar[i], ar[(i+4)%len(ar)]
		for _, op := range []string{"add", "sub", "mul", "mod"} {
			args[op] = append(args[op], []any{x, y})
		}
		args["div"] = append(args["div"], []any{x, y, 4})
	}
	for i := 0; i < 5; i++ {
		x, y := randDecString(rng, 30, 15), randDecString(rng, 30, 15)
		for _, op := range []string{"add", "sub", "mul", "mod"} {
			args[op] = append(args[op], []any{x, y})
		}
		args["div"] = append(args["div"], []any{x, y, rng.IntN(10)})
	}
	for _, op := range dirtyOps {
		for _, r := range dirtyReceivers {
			for _, a := range args[op] {
				g.Add("sys/dirty", dirtyPrefix+op, append([]any{r}, a...)...)
			}
		}
	}
}

// ---------------------------------------------------------------- systematic

// sysStrings is the boundary corpus for single-decimal ops.
func sysStrings() []string {
	n := func(c byte, k int) string { return strings.Repeat(string(c), k) }
	ss := []string{
		// zero forms / negative zero
		"0", "-0", "+0", "0.0", "-0.0", "-0.000000000", "00000000000", ".0", "-.0", "0.", "-0.", "0e10", "-0e-10",
		// small
		"1", "-1", "0.1", "-0.1", "0.5", "-0.5", "1.5", "-1.5", "2.5", "-2.5", "0.05", "0.45", "0.49", "0.4999999999",
		// word boundaries (9 digits per word)
		n('9', 8), n('9', 9), n('9', 10), "1" + n('0', 8), "1" + n('0', 9), "1" + n('0', 17), "1" + n('0', 18),
		n('9', 17), n('9', 18), n('9', 19), n('9', 27), n('9', 28),
		"0." + n('9', 8), "0." + n('9', 9), "0." + n('9', 10), "0." + n('0', 8) + "1", "0." + n('0', 9) + "1",
		"0." + n('0', 17) + "1", n('9', 9) + "." + n('9', 9), "-" + n('9', 9) + "." + n('9', 9),
		"123456789.123456789", "-123456789.123456789", "1234567891.1234567891",
		// MySQL limits: precision 65, scale 30
		n('9', 65), "-" + n('9', 65), n('9', 35) + "." + n('9', 30), "-" + n('9', 35) + "." + n('9', 30),
		"1" + n('0', 64), "0." + n('0', 29) + "1", "0." + n('9', 30), "0." + n('9', 31), "0." + n('0', 30) + "1",
		n('9', 36) + "." + n('9', 30), n('9', 66),
		// internal limit: 9 words = 81 digits
		n('9', 80), n('9', 81), n('9', 82), "1" + n('0', 81), "0." + n('9', 72), "0." + n('9', 73), "0." + n('9', 81),
		n('9', 45) + "." + n('9', 45), "1" + n('0', 40) + "." + n('0', 40) + "1", n('1', 100), "0." + n('1', 100),
		// int64 / uint64 boundaries
		"9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809",
		"18446744073709551615", "18446744073709551616", "-18446744073709551615", "9223372036854775807.5",
		"-9223372036854775808.5", "18446744073709551615.4", "18446744073709551614.5", "0.9999999999999999999",
		"-0.4", "-0.5", "-0.6", "0.4", "0.6",
		// rounding probes
		"1.45", "1.55", "-1.45", "-1.55", "1.449999", "1.500000001", "9.95", "99.95", "999999999.5", "-999999999.5",
		"0.000000000500", "0.0000000005", "12345.678", "-12345.678",
		// exponent forms
		"1e0", "1E1", "1e+5", "1e-5", "1.5e3", "-1.5e-3", "1e65", "1e80", "1e81", "1e82", "1e-30", "1e-31", "1e-80",
		"1e-81", "1e-82", "123.456e2", "123.456e-2", ".5e1", "5.e-1", "1e2147483647", "1e-2147483648", "1e9223372036854775807",
		// whitespace / signs / junk
		" 1", "1 ", "\t1", " -1 ", "+-1", "-+1", "--1", "++1", "", " ", ".", "-", "+", "-.", "e5", "abc", "1x", "0x10",
		"1,5", "1_000", "١", "1.2.3", "１",
	}
	return ss
}

// sysArith is a smaller corpus for the pairwise ops.
func sysArith() []string {
	n := func(c byte, k int) string { return strings.Repeat(string(c), k) }
	return []string{
		"0", "-0", "0.000", "1", "-1", "0.1", "-0.5", "2", "3", "7", "-7", "10", "0.003430",
		"1.000000001", "999999999", "1000000000", "-999999999.999999999", "0.000000001",
		n('9', 18), "1" + n('0', 30), "0." + n('0', 29) + "1", n('9', 35) + "." + n('9', 30),
		n('9', 65), "-" + n('9', 65), n('9', 81), "0." + n('9', 72),
		"9223372036854775807", "-9223372036854775808", "18446744073709551615",
		"123.456", "-98765.4321", "3.14159265358979323846264338327950288",
	}
}

func decimalSystematic(g *vec.Gen) {
	ss := sysStrings()
	for _, s := range ss {
		g.Add("sys/str", "from_string", s)
		g.Add("sys/str", "props", s)
		g.Add("sys/str", "to_hash_key", s)
		g.Add("sys/str", "to_int", s)
		g.Add("sys/str", "to_uint", s)
		g.Add("sys/str", "to_float64", s)
		g.Add("sys/str", "neg", s)
	}
	for _, s := range ss[:60] {
		g.Add("sys/json", "marshal_json", s)
		var d types.MyDecimal
		_ = d.FromString([]byte(s))
		j, _ := d.MarshalJSON()
		g.Add("sys/json", "unmarshal_json", string(j))
	}
	// round: every mode x boundary fracs
	fracs := []int{-82, -10, -9, -1, 0, 1, 2, 8, 9, 10, 30, 31, 82}
	for _, s := range ss {
		for _, f := range fracs {
			g.Add("sys/round", "round", s, f, "HalfUp")
		}
		for _, f := range []int{-9, -1, 0, 1, 9, 30} {
			g.Add("sys/round", "round", s, f, "Truncate")
			g.Add("sys/round", "round", s, f, "Ceiling")
		}
	}
	for _, s := range sysArith() {
		for _, f := range []int{-1, 0, 1, 9, 30} {
			for _, m := range roundModeNames {
				g.Add("sys/round", "round_inplace", s, f, m)
			}
		}
	}
	// shift
	for _, s := range ss {
		for _, k := range []int{-100, -81, -30, -9, -8, -1, 0, 1, 9, 10, 30, 81, 82} {
			g.Add("sys/shift", "shift", s, k)
		}
	}
	// pairwise arithmetic
	as := sysArith()
	for _, x := range as {
		for _, y := range as {
			g.Add("sys/arith", "add", x, y)
			g.Add("sys/arith", "sub", x, y)
			g.Add("sys/arith", "mul", x, y)
			g.Add("sys/arith", "div", x, y, 4)
			g.Add("sys/arith", "mod", x, y)
			g.Add("sys/arith", "compare", x, y)
		}
	}
	for _, x := range as[:12] {
		for _, y := range as[:12] {
			for _, inc := range []int{0, 1, 9, 30, 40} {
				g.Add("sys/div_incr", "div", x, y, inc)
			}
		}
	}
	// to_bin / from_bin over a (precision, frac) grid
	pfs := [][2]int{{1, 0}, {1, 1}, {2, 1}, {9, 0}, {9, 9}, {10, 0}, {10, 1}, {10, 9}, {18, 9}, {18, 0}, {19, 0},
		{19, 10}, {20, 10}, {30, 30}, {38, 10}, {65, 0}, {65, 30}, {65, 5}, {66, 30}, {81, 0}, {81, 30},
		{0, 0}, {5, 6}, {82, 0}, {-1, 0}, {10, -1}, {40, 31}}
	for _, s := range ss[:120] {
		for _, pf := range pfs {
			g.Add("sys/bin", "to_bin", s, pf[0], pf[1])
		}
		p, f := precFrac(s)
		g.Add("sys/bin", "to_bin", s, p, f)
	}
	for _, s := range []string{"0", "-0", "1", "-1", "0.5", "-12345.678", "999999999.999999999", strings.Repeat("9", 65)} {
		for _, pf := range pfs[:21] {
			if h, ok := toBinHex(s, pf[0], pf[1]); ok {
				g.Add("sys/bin", "from_bin", h, pf[0], pf[1])
				// same bytes read with a different layout
				g.Add("sys/bin", "from_bin", h, pf[0]+1, pf[1])
				// DecimalPeak reads precision/frac from the first two bytes
				g.Add("sys/bin", "peak", hex.EncodeToString([]byte{byte(pf[0]), byte(pf[1])})+h)
			}
		}
	}
	for _, h := range []string{"", "00", "80", "7f", "0000", "800000", "ff", "ffffffff", "8000000000"} {
		g.Add("sys/bin", "from_bin", h, 1, 0)
		g.Add("sys/bin", "from_bin", h, 10, 2)
		g.Add("sys/bin", "peak", h)
	}
	for p := -1; p <= 82; p++ {
		for _, f := range []int{-1, 0, 1, 8, 9, 10, 30, 31} {
			g.Add("sys/bin", "bin_size", p, f)
		}
	}
	// int / uint / float sources
	for _, v := range []int64{0, 1, -1, 9, 10, 999999999, 1000000000, -1000000000, 999999999999999999,
		1000000000000000000, -1000000000000000000, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1,
		math.MaxInt32, math.MinInt32} {
		g.Add("sys/int", "from_int", v)
	}
	for _, v := range []uint64{0, 1, 999999999, 1000000000, 999999999999999999, 1000000000000000000,
		math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64, math.MaxUint64 - 1, math.MaxUint32} {
		g.Add("sys/int", "from_uint", v)
	}
	floats := []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 0.2, 0.3, 1.0 / 3, 2.0 / 3, 0.5, 1e-5, 1e-6, 1e-7,
		1e15, 1e16, 1e20, 1e21, 1e22, 1e23, 1e30, 1e40, 1e64, 1e65, 1e66, 1e80, 1e81, 1e82, 1e100, 1e308,
		1e-30, 1e-31, 1e-40, 1e-80, 1e-81, 1e-100, 5e-324, math.SmallestNonzeroFloat64, math.MaxFloat64,
		1 << 53, 1<<53 + 1, 1 << 63, 1 << 64, 123456789.123456789, 0.1 + 0.2, 1.7976931348623157e308,
		2.2250738585072014e-308, math.NaN(), math.Inf(1), math.Inf(-1), 9007199254740993, 1.5, 2.5, -2.5,
		99999999999999999999.0, 0.000001, 0.0000001, 123e-20}
	for _, f := range floats {
		g.Add("sys/float", "from_float64", fmtSpecial(f))
	}
	// max / min
	for _, p := range []int{0, 1, 2, 8, 9, 10, 17, 18, 19, 30, 31, 64, 65} {
		for _, f := range []int{0, 1, 8, 9, 10, 29, 30} {
			if f > p {
				continue
			}
			g.Add("sys/maxmin", "max_or_min", false, p, f)
			g.Add("sys/maxmin", "max_or_min", true, p, f)
		}
	}
	// unmarshal_json: odd states the Go code accepts
	for _, j := range []string{
		`{"DigitsInt":1,"DigitsFrac":0,"ResultFrac":0,"Negative":false,"WordBuf":[5,0,0,0,0,0,0,0,0]}`,
		`{"DigitsInt":1,"DigitsFrac":0,"ResultFrac":0,"Negative":true,"WordBuf":[0,0,0,0,0,0,0,0,0]}`,
		`{"DigitsInt":1,"DigitsFrac":2,"ResultFrac":5,"Negative":false,"WordBuf":[1,500000000,0,0,0,0,0,0,0]}`,
		`{"DigitsInt":0,"DigitsFrac":0,"ResultFrac":0,"Negative":false,"WordBuf":[0,0,0,0,0,0,0,0,0]}`,
		`{}`, `null`, `"1.5"`, `1.5`, `{"DigitsInt":1}`, `not json`,
	} {
		g.Add("sys/json", "unmarshal_json", j)
	}
}

func fmtSpecial(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return FormatFloat(f)
}

// ---------------------------------------------------------------- random

func randDecString(rng *rand.Rand, maxInt, maxFrac int) string {
	var b strings.Builder
	if rng.IntN(2) == 0 {
		b.WriteByte('-')
	}
	ni := rng.IntN(maxInt + 1)
	nf := rng.IntN(maxFrac + 1)
	if ni == 0 {
		b.WriteByte('0')
	}
	for i := 0; i < ni; i++ {
		b.WriteByte(randDigit(rng, i == 0))
	}
	if nf > 0 {
		b.WriteByte('.')
		for i := 0; i < nf; i++ {
			b.WriteByte(randDigit(rng, false))
		}
	}
	return b.String()
}

// randDigit favours 0 and 9 to hit carries and trailing zeros.
func randDigit(rng *rand.Rand, first bool) byte {
	switch r := rng.IntN(10); {
	case r < 2:
		if first {
			return '1'
		}
		return '0'
	case r < 4:
		return '9'
	default:
		return byte('0' + rng.IntN(10))
	}
}

const (
	randSingles = 500 // per-value ops
	randPairs   = 500
)

func decimalRandom(g *vec.Gen, rng *rand.Rand) {
	for i := 0; i < randSingles; i++ {
		s := randDecString(rng, 40, 35)
		g.Add("rand/single", "from_string", s)
		g.Add("rand/single", "to_float64", s)
		g.Add("rand/single", "to_hash_key", s)
		if i%2 == 0 {
			g.Add("rand/single", "props", s)
		}
		p, f := precFrac(s)
		g.Add("rand/single", "to_bin", s, p, f)
		if p <= 65 && f <= 30 {
			if h, ok := toBinHex(s, p, f); ok {
				g.Add("rand/single", "from_bin", h, p, f)
			}
		}
		g.Add("rand/single", "round", s, rng.IntN(40)-10, roundModeNames[rng.IntN(3)])
		g.Add("rand/single", "shift", s, rng.IntN(60)-30)
		if i%3 == 0 {
			g.Add("rand/single", "to_int", s)
			g.Add("rand/single", "to_uint", s)
		}
	}
	// ToFloat64 fast path (<=12 digits) vs slow path; shape copied from
	// BenchmarkToFloat64 genTestDecimals, but seeded.
	for i := 0; i < 300; i++ {
		f := rng.Float64()
		digits := rng.IntN(12) + 1
		offset := rng.IntN(digits) + 1
		f = math.Round(f*math.Pow10(digits)) / math.Pow10(digits-offset)
		g.Add("rand/float(BenchmarkToFloat64)", "to_float64", FormatFloat(f))
	}
	for i := 0; i < 200; i++ {
		g.Add("rand/float", "from_float64", fmtSpecial(math.Float64frombits(rng.Uint64())))
		g.Add("rand/int", "from_int", int64(rng.Uint64()))
		g.Add("rand/int", "from_uint", rng.Uint64())
	}
	for i := 0; i < randPairs; i++ {
		x := randDecString(rng, 30, 20)
		y := randDecString(rng, 30, 20)
		if i%10 == 0 {
			y = x
		}
		g.Add("rand/pair", "add", x, y)
		g.Add("rand/pair", "sub", x, y)
		g.Add("rand/pair", "mul", x, y)
		g.Add("rand/pair", "div", x, y, []int{0, 4, 4, 4, 5, 9}[rng.IntN(6)])
		g.Add("rand/pair", "mod", x, y)
		g.Add("rand/pair", "compare", x, y)
	}
	for i := 0; i < 200; i++ {
		n := rng.IntN(12)
		b := make([]byte, n)
		for j := range b {
			b[j] = byte(rng.Uint32())
		}
		p := rng.IntN(66)
		f := 0
		if p > 0 {
			f = rng.IntN(min(p, 30) + 1)
		}
		g.Add("rand/bin", "from_bin", hex.EncodeToString(b), p, f)
	}
}
