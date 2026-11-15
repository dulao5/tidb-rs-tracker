package domains

// Random inputs of domain "rangerbuild" (rangerbuild.go): conditions over a wide table whose
// columns cover the types the ranger converts (signed / unsigned integers, floats, decimals,
// strings under binary / _bin / _ci / _unicode_ci / 0900 / gbk / ascii collations, prefix
// indexes, binary, enum, set, year, date / datetime / timestamp / time, bit), constants at the
// type boundaries (fractions on integer columns, out-of-range values, negatives on unsigned
// columns, 2-digit years, MaxUint64, NULL, strings differing in case / trailing spaces), IN
// lists with duplicates and NULLs, LIKE patterns with wildcards and escapes, and AND / OR / NOT
// combinations on one and several index columns; and the direct ops on random ranges.

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

type rbCol struct {
	name, def string
	consts    []string
}

var rbIntConsts = []string{"0", "1", "-1", "2", "10", "1.5", "-1.5", "0.5", "1.4", "1.6", "'1'", "'abc'", "127", "128",
	"-128", "255", "256", "32767", "65535", "2147483647", "2147483648", "-2147483649", "9223372036854775807",
	"9223372036854775808", "-9223372036854775808", "-9223372036854775809", "18446744073709551615",
	"18446744073709551616", "1e20", "-1e20", "NULL", "1.0", "0.0", "-0", "1e1", "99999999999999999999.5", "true", "false"}
var rbFloatConsts = []string{"0", "1", "-1", "1.5", "-1.5", "3.4028234663852886e38", "3.5e38", "-3.5e38", "1e308",
	"'1.5'", "NULL", "0.1", "0.30000000000000004", "1e-30", "2", "18446744073709551615", "-9223372036854775809"}
var rbDecConsts = []string{"0", "1", "-1", "1.5", "1.005", "1.004", "99999999.99", "100000000", "-99999999.99",
	"12345678.125", "0.001", "'1.5'", "NULL", "1e10", "-1e10", "2.50", "18446744073709551615"}
var rbStrConsts = []string{"'a'", "'A'", "'a '", "'ab'", "'abc'", "'ABC'", "'abcd'", "'abcdef'", "''", "' '", "'b'",
	"'中'", "'中文'", "'ß'", "'ss'", "'Å'", "'aaa'", "'zzz'", "1", "1.5", "NULL", "'a\\\\b'", "'it''s'", "0x61", "0x6162",
	"_binary'abc'", "'\\0'", "'ａ'", "'Ä'", "'ä'", "'ǅ'"}
var rbLikeConsts = []string{"'a%'", "'ab%'", "'abc'", "'a_c'", "'a_%'", "'%a'", "'_a'", "'a\\\\%'", "'a\\\\_b%'",
	"'ab\\\\'", "'a%b'", "''", "'%'", "'abc %'", "'a  %'", "'A%'", "'中%'", "'ab_'", "'\\\\%a'"}
var rbTimeConsts = []string{"'2020-01-01'", "'2020-01-01 10:00:00'", "'2020-01-01 10:00:00.123456'", "'2020-02-30'",
	"'0000-00-00'", "'1970-01-01 00:00:01'", "'2038-01-19 03:14:08'", "'9999-12-31 23:59:59'", "20200101",
	"'10:00:00'", "'838:59:59'", "'-838:59:59'", "'839:00:00'", "NULL", "'abc'", "1", "'2020-01-01 10:00:00.5'"}
var rbYearConsts = []string{"0", "1", "69", "70", "99", "100", "1900", "1901", "2000", "2155", "2156", "'0'", "'1'",
	"'70'", "-1", "15698", "1.5", "NULL", "18446744073709551615", "'abc'"}
var rbEnumConsts = []string{"'a'", "'b'", "'c'", "'A'", "'d'", "''", "1", "2", "3", "4", "0", "NULL", "1.5", "'1'"}
var rbBitConsts = []string{"0", "1", "255", "256", "b'1'", "b'11111111'", "b'100000000'", "0x01", "-1", "NULL", "'a'"}

var rbCols = []rbCol{
	{"i", "i int", rbIntConsts},
	{"u", "u int unsigned", rbIntConsts},
	{"ti", "ti tinyint", rbIntConsts},
	{"bu", "bu bigint unsigned", rbIntConsts},
	{"bi", "bi bigint", rbIntConsts},
	{"nn", "nn int not null", rbIntConsts},
	{"d", "d double", rbFloatConsts},
	{"f", "f float", rbFloatConsts},
	{"du", "du double unsigned", rbFloatConsts},
	{"dc", "dc decimal(10,2)", rbDecConsts},
	{"dcu", "dcu decimal(8,3) unsigned", rbDecConsts},
	{"s", "s varchar(20) collate utf8mb4_bin", rbStrConsts},
	{"sg", "sg varchar(20) collate utf8mb4_general_ci", rbStrConsts},
	{"su", "su varchar(20) collate utf8mb4_unicode_ci", rbStrConsts},
	{"s9", "s9 varchar(20) collate utf8mb4_0900_ai_ci", rbStrConsts},
	{"sb", "sb varchar(20) collate utf8mb4_0900_bin", rbStrConsts},
	{"c", "c char(5) collate utf8mb4_bin", rbStrConsts},
	{"cg", "cg char(5) collate utf8mb4_general_ci", rbStrConsts},
	{"bn", "bn binary(5)", rbStrConsts},
	{"vb", "vb varbinary(10)", rbStrConsts},
	{"tx", "tx text collate utf8mb4_general_ci", rbStrConsts},
	{"bl", "bl blob", rbStrConsts},
	{"ac", "ac varchar(10) charset ascii", rbStrConsts},
	{"gb", "gb varchar(10) charset gbk collate gbk_bin", rbStrConsts},
	{"gc", "gc varchar(10) charset gbk collate gbk_chinese_ci", rbStrConsts},
	{"l1", "l1 varchar(10) charset latin1", rbStrConsts},
	{"e", "e enum('a','b','c')", rbEnumConsts},
	{"eg", "eg enum('A','b','C') collate utf8mb4_general_ci", rbEnumConsts},
	{"st", "st set('x','y')", rbEnumConsts},
	{"y", "y year", rbYearConsts},
	{"dt", "dt date", rbTimeConsts},
	{"dtt", "dtt datetime(3)", rbTimeConsts},
	{"ts", "ts timestamp(2) null", rbTimeConsts},
	{"tm", "tm time(1)", rbTimeConsts},
	{"bt", "bt bit(8)", rbBitConsts},
}

// rbIndexes: [name, columns]; prefix lengths in the column list.
var rbIndexes = [][2]string{
	{"x_i", "i"}, {"x_u", "u"}, {"x_ti", "ti"}, {"x_bu", "bu"}, {"x_bi", "bi"}, {"x_nn", "nn"}, {"x_d", "d"},
	{"x_f", "f"}, {"x_du", "du"}, {"x_dc", "dc"}, {"x_dcu", "dcu"}, {"x_s", "s"}, {"x_sp", "s(3)"}, {"x_sg", "sg"},
	{"x_sgp", "sg(2)"}, {"x_su", "su"}, {"x_s9", "s9"}, {"x_sb", "sb"}, {"x_c", "c"}, {"x_cg", "cg"}, {"x_bn", "bn"},
	{"x_bnp", "bn(2)"}, {"x_vb", "vb(3)"}, {"x_tx", "tx(3)"}, {"x_bl", "bl(2)"}, {"x_ac", "ac(2)"}, {"x_gb", "gb"},
	{"x_gc", "gc"}, {"x_l1", "l1(2)"}, {"x_e", "e"}, {"x_eg", "eg"}, {"x_y", "y"}, {"x_dt", "dt"}, {"x_dtt", "dtt"},
	{"x_ts", "ts"}, {"x_tm", "tm"}, {"x_bt", "bt"}, {"x_i_s", "i,s"}, {"x_u_d", "u,d"}, {"x_s_i", "s(4),i"},
	{"x_i_u_f", "i,u,f"}, {"x_nn_i", "nn,i"}, {"x_sg_i", "sg,i"}, {"x_i_sg_d", "i,sg(3),d"}, {"x_y_dt", "y,dt"},
	{"x_e_i", "e,i"}, {"x_bu_bi", "bu,bi"}, {"x_dc_sp", "dc,s(2)"}, {"x_i_i2", "i,ti,bi"},
}

func rbWideDDL() []string {
	defs := []string{}
	for _, c := range rbCols {
		defs = append(defs, c.def)
	}
	for _, x := range rbIndexes {
		defs = append(defs, "index "+x[0]+"("+x[1]+")")
	}
	return []string{"create table r1 (" + strings.Join(defs, ", ") + ")",
		"create table r2 (pk bigint primary key, pu bigint unsigned, v varchar(10), index(pu))",
		"create table r3 (pk bigint unsigned primary key, a int)"}
}

func rbColByName(n string) rbCol {
	for _, c := range rbCols {
		if c.name == n {
			return c
		}
	}
	panic("rbColByName " + n)
}

func rbPick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

// rbAtom: one predicate on column c.
func rbAtom(rng *rand.Rand, c rbCol) string {
	k := func() string { return rbPick(rng, c.consts) }
	switch rng.IntN(20) {
	case 0, 1, 2, 3, 4, 5:
		return fmt.Sprintf("%s %s %s", c.name, rbPick(rng, []string{"=", "!=", "<", "<=", ">", ">=", "<=>", "="}), k())
	case 6, 7:
		return fmt.Sprintf("%s %s %s", k(), rbPick(rng, []string{"=", "<", "<=", ">", ">=", "!="}), c.name)
	case 8, 9, 10:
		n := 1 + rng.IntN(5)
		vs := []string{}
		for i := 0; i < n; i++ {
			v := k()
			vs = append(vs, v)
			if rng.IntN(4) == 0 {
				vs = append(vs, v) // a duplicate
			}
		}
		not := ""
		if rng.IntN(4) == 0 {
			not = "not "
		}
		return fmt.Sprintf("%s %sin (%s)", c.name, not, strings.Join(vs, ", "))
	case 11, 12:
		p := rbPick(rng, rbLikeConsts)
		not := ""
		if rng.IntN(6) == 0 {
			not = "not "
		}
		esc := ""
		if rng.IntN(5) == 0 {
			esc = " escape '|'"
			p = strings.ReplaceAll(p, "\\\\", "|")
		}
		return fmt.Sprintf("%s %slike %s%s", c.name, not, p, esc)
	case 13:
		return c.name + rbPick(rng, []string{" is null", " is not null"})
	case 14:
		return c.name + rbPick(rng, []string{" is true", " is false", " is not true", " is not false"})
	case 15:
		return rbPick(rng, []string{"not ", ""}) + c.name
	case 16:
		return fmt.Sprintf("%s between %s and %s", c.name, k(), k())
	case 17:
		return fmt.Sprintf("not (%s)", rbAtom(rng, c))
	case 18:
		return fmt.Sprintf("%s %s %s", c.name, rbPick(rng, []string{"=", ">", "<"}), rbPick(rng, rbIntConsts))
	}
	return fmt.Sprintf("%s = %s", c.name, k())
}

// rbCond: a CNF of 1-4 items over the index columns (sometimes other columns), with DNFs.
func rbCond(rng *rand.Rand, cols []rbCol) string {
	pick := func() rbCol {
		if rng.IntN(6) == 0 {
			return rbPick(rng, rbCols)
		}
		return rbPick(rng, cols)
	}
	n := 1 + rng.IntN(4)
	items := []string{}
	for i := 0; i < n; i++ {
		switch rng.IntN(6) {
		case 0:
			// a DNF on one column
			c := pick()
			m := 2 + rng.IntN(3)
			ors := []string{}
			for j := 0; j < m; j++ {
				ors = append(ors, rbAtom(rng, c))
			}
			items = append(items, "("+strings.Join(ors, " or ")+")")
		case 1:
			// a DNF of CNFs over the index columns
			m := 2 + rng.IntN(2)
			ors := []string{}
			for j := 0; j < m; j++ {
				ands := []string{}
				for _, c := range cols {
					if rng.IntN(3) != 0 {
						ands = append(ands, rbAtom(rng, c))
					}
				}
				if len(ands) == 0 {
					ands = append(ands, rbAtom(rng, pick()))
				}
				ors = append(ors, "("+strings.Join(ands, " and ")+")")
			}
			items = append(items, "("+strings.Join(ors, " or ")+")")
		default:
			items = append(items, rbAtom(rng, pick()))
		}
	}
	return strings.Join(items, " and ")
}

func rbRandOpts(rng *rand.Rand) rbOpts {
	o := rbDefaultOpts
	if rng.IntN(5) == 0 {
		o.NP = false
	}
	if rng.IntN(4) == 0 {
		o.PS = false
	}
	o.F44389 = rng.IntN(3) == 0
	o.F54337 = rng.IntN(3) == 0
	if rng.IntN(6) == 0 {
		o.Max = rbPick(rng, []int64{1, 100, 300, 600, 1000, 3000})
	}
	return o
}

// rbRejected counts the queries the planner rejected in a row (a broken schema would loop).
var rbRejected int

func rbReject(g *vec.Gen, out map[string]any) bool {
	if _, bad := out["plan"]; !bad {
		rbRejected = 0
		return false
	}
	g.Calls = g.Calls[:len(g.Calls)-1]
	if rbRejected++; rbRejected > 50 {
		panic(fmt.Sprintf("rangerbuild: the planner rejects every query: %v", out["plan"]))
	}
	return true
}

func rbGenRandom(g *vec.Gen, rng *rand.Rand) {
	ddl := rbWideDDL()
	// index records
	for n := 0; n < 2600; {
		xi := rng.IntN(len(rbIndexes))
		var cols []rbCol
		for _, part := range strings.Split(rbIndexes[xi][1], ",") {
			name := part
			if i := strings.IndexByte(part, '('); i >= 0 {
				name = part[:i]
			}
			cols = append(cols, rbColByName(name))
		}
		cond := rbCond(rng, cols)
		out := rbAddIndex(g, "rand/index", ddl, "r1", cond, rng.IntN(5) != 0, xi, rbRandOpts(rng))
		if rbReject(g, out) {
			continue
		}
		n++
	}
	// column records
	for n := 0; n < 1000; {
		ci := rng.IntN(len(rbCols))
		cond := rbCond(rng, []rbCol{rbCols[ci]})
		length := types.UnspecifiedLength
		if rng.IntN(4) == 0 {
			length = rng.IntN(5)
		}
		var max int64
		if rng.IntN(6) == 0 {
			max = rbPick(rng, []int64{1, 100, 300, 1000})
		}
		out := rbAddColumn(g, "rand/column", ddl, "r1", cond, rng.IntN(5) != 0, ci, length, max, rbRandOpts(rng))
		if rbReject(g, out) {
			continue
		}
		n++
	}
	// table ranges on int handles
	for n := 0; n < 200; n++ {
		tbl := rbPick(rng, []string{"r2", "r3"})
		cond := rbCond(rng, []rbCol{{"pk", "", rbIntConsts}})
		out := rbAddColumn(g, "rand/table", ddl, tbl, cond, true, 0, types.UnspecifiedLength, 0, rbDefaultOpts)
		if rbReject(g, out) {
			n--
		}
	}
	// MergeDNFItems4Col: DNFs over several columns
	for n := 0; n < 200; {
		m := 2 + rng.IntN(5)
		ors := []string{}
		for j := 0; j < m; j++ {
			c := rbCols[rng.IntN(6)]
			if rng.IntN(5) == 0 {
				ors = append(ors, fmt.Sprintf("(%s and %s)", rbAtom(rng, c), rbAtom(rng, rbPick(rng, rbCols))))
			} else {
				ors = append(ors, rbAtom(rng, c))
			}
		}
		g.Add("rand/merge_dnf", "merge_dnf", ddl, "r1", strings.Join(ors, " or "), true)
		out, _ := rangerbuildExec("merge_dnf", g.Calls[len(g.Calls)-1].Args)
		if rbReject(g, out.(map[string]any)) {
			continue
		}
		n++
	}
}

// ---------------------------------------------------------------- direct ops

func rbRandDatum(rng *rand.Rand, kind int) []any {
	switch kind {
	case 0:
		return rInt(rbPick(rng, []int64{math.MinInt64, -100, -1, 0, 1, 2, 3, 5, 10, 100, math.MaxInt64}))
	case 1:
		return []any{types.KindUint64, rbPick(rng, []uint64{0, 1, 2, 5, 10, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64})}
	case 2:
		return []any{types.KindString, vec.GoString(rbPick(rng, []string{"", "a", "a ", "ab", "abc", "b", "中", "it's", "a\\b", "x\x00y"})),
			rbPick(rng, []string{"utf8mb4_bin", "utf8mb4_general_ci", "binary"})}
	case 3:
		return []any{types.KindBytes, rbPick(rng, []string{"", "00", "61", "6162", "ff", "ffff"})}
	case 4:
		return []any{types.KindFloat64, fBits(rbPick(rng, []float64{-1.5, 0, 0.1, 1, 1.5, 1e300, math.Inf(1)}))}
	case 5:
		return []any{types.KindMysqlDecimal, rbPick(rng, []string{"0", "1.5", "-1.50", "100", "99999.999"})}
	case 6:
		return []any{types.KindNull}
	case 7:
		return []any{types.KindMinNotNull}
	}
	return []any{types.KindMaxValue}
}

// rbRandRanges: n ranges of width w, kind per column (the same in all ranges).
func rbRandRanges(rng *rand.Rand, n, w int, kinds []int, colls []string) []rSpec {
	out := []rSpec{}
	for i := 0; i < n; i++ {
		s := rSpec{LE: rng.IntN(3) == 0, HE: rng.IntN(3) == 0, C: colls}
		for j := 0; j < w; j++ {
			lo, hi := rbRandDatum(rng, kinds[j]), rbRandDatum(rng, kinds[j])
			if j < w-1 || rng.IntN(3) == 0 {
				hi = lo // a point prefix
			}
			if rng.IntN(8) == 0 {
				lo = rbRandDatum(rng, rbPick(rng, []int{6, 7}))
			}
			if rng.IntN(8) == 0 {
				hi = rbRandDatum(rng, 8)
			}
			s.L = append(s.L, lo)
			s.H = append(s.H, hi)
		}
		out = append(out, s)
	}
	return out
}

func rbGenDirect(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/null_range", "null_range")
	for i := 0; i < 400; i++ {
		w := 1 + rng.IntN(3)
		kinds, colls := []int{}, []string{}
		for j := 0; j < w; j++ {
			kinds = append(kinds, rng.IntN(6))
			colls = append(colls, rbPick(rng, []string{"binary", "utf8mb4_bin", "utf8mb4_general_ci"}))
		}
		rs := rbRandRanges(rng, 1+rng.IntN(6), w, kinds, colls)
		g.Add("rand/union", "union", rs, rng.IntN(2) == 0)
		names := []string{}
		for j := 0; j < w; j++ {
			names = append(names, rbPick(rng, []string{"a", "b", "`c`", "test.t.d"}))
		}
		g.Add("rand/to_string", "to_string", rs, names)
		if i%2 == 0 {
			pts := rbRandRanges(rng, 1+rng.IntN(4), 1, kinds[:1], colls[:1])
			for k := range pts {
				pts[k].H = pts[k].L
				pts[k].LE, pts[k].HE = false, false
			}
			g.Add("rand/append", "append", pts, rs, rbPick(rng, []int64{0, 0, 1, 200, 1000}))
		}
	}
	// cut / reach on strings and bytes under binary / ascii / utf8mb4 charsets
	for i := 0; i < 300; i++ {
		var d []any
		s := rbPick(rng, []string{"", "a", "ab", "abc", "abcd", "中文字", "a中b", "\xff\xfe", "ab\xffcd"})
		if rng.IntN(3) == 0 {
			d = []any{types.KindBytes, fmt.Sprintf("%x", s)}
		} else {
			d = []any{types.KindString, vec.GoString(s), "utf8mb4_bin"}
		}
		cs := rbPick(rng, [][2]string{{"binary", "binary"}, {"ascii", "ascii_bin"}, {"utf8mb4", "utf8mb4_bin"},
			{"utf8mb4", "utf8mb4_general_ci"}, {"latin1", "latin1_bin"}})
		ft := []any{mysql.TypeVarchar, 0, 10, 0, cs[0], cs[1], nil}
		g.Add("rand/cut", "cut", d, rbPick(rng, []int{types.UnspecifiedLength, 0, 1, 2, 3, 4}), ft)
	}
}
