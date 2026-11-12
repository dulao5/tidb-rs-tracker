package domains

// Domain "cast", D08.5 batch 1 (appended after D08.4 batch 1; earlier records are unchanged):
// the arithmetic operators + - * / DIV % (the six function classes and their 23 signatures)
// and CEIL / CEILING / FLOOR (two classes, ten signatures).
//   - "arbuild": [variant, name, args, ctx, cols, rows, sel] -> NewFunctionBase (variant 0),
//     NewFunction (1, folding) or NewFunctionTryFold (2) of name(args...): "args" (building
//     the argument recipes), "res" (sfDump or the error), "bwarns", then "row"/"vec":
//     EvalExpr of the result as its own evaluation type with vectorization off / on
//     (values, warnings, error). ctx is timeCtx plus div_precision_increment (castCtx
//     index 5).
//   - "cmpbuild" (D08.4 op), source "ceilcmp": the comparisons whose RefineComparedConstant
//     goes through NewFunctionInternal(Ceil / Floor), which D08.4 batch 1 skipped
//     (cmpReachesCeil): YEAR columns against DOUBLE, short and long DECIMAL and string
//     constants (v8.5.8 `y < 2e0` with y = 1977 is 1: the constant becomes 2002;
//     tidb-rs #99: `y < 2.0` compares with 2), integer columns against non-integral
//     constants. The D08.4 records keep their skip so that they stay byte-identical.
//   - "newfunc" (part 2b-3a op), sources "arfold" / "artryfold": folding of arithmetic on
//     constants; NewFunctionTryFold (fold -1) returns the unfolded function when folding
//     warns (division by zero, truncation; D08.1 T6c (c)).

import (
	"encoding/json"
	"math"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
)

var arNames = []string{ast.Plus, ast.Minus, ast.Mul, ast.Div, ast.IntDiv, ast.Mod}
var cfNames = []string{ast.Ceil, ast.Ceiling, ast.Floor}

func arSigFuncs() []string {
	out := []string{}
	for _, s := range []string{"PlusReal", "PlusDecimal", "PlusInt", "MinusReal", "MinusDecimal", "MinusInt",
		"MultiplyReal", "MultiplyDecimal", "MultiplyIntUnsigned", "MultiplyInt", "DivideReal", "DivideDecimal",
		"IntDivideInt", "IntDivideDecimal", "ModIntUnsignedUnsigned", "ModIntUnsignedSigned",
		"ModIntSignedUnsigned", "ModIntSignedSigned", "ModReal", "ModDecimal"} {
		et := "Int"
		switch {
		case len(s) > 4 && s[len(s)-4:] == "Real":
			et = "Real"
		case len(s) > 7 && s[len(s)-7:] == "Decimal" && s != "IntDivideDecimal":
			et = "Decimal"
		}
		n := "builtinArithmetic" + s + "Sig."
		out = append(out, n+"eval"+et, n+"vecEval"+et, n+"vectorized", n+"Clone")
	}
	for _, s := range []string{"Plus", "Minus", "Multiply"} {
		n := "builtinArithmetic" + s + "VectorFloat32Sig."
		out = append(out, n+"evalVectorFloat32", n+"Clone")
	}
	for _, s := range []string{"Ceil", "Floor"} {
		for _, k := range []struct{ sig, et string }{{"Real", "Real"}, {"IntToInt", "Int"}, {"IntToDec", "Decimal"},
			{"DecToInt", "Int"}, {"DecToDec", "Decimal"}} {
			n := "builtin" + s + k.sig + "Sig."
			out = append(out, n+"eval"+k.et, n+"vecEval"+k.et, n+"vectorized", n+"Clone")
		}
	}
	return append(out,
		"arithmeticPlusFunctionClass.getFunction", "arithmeticMinusFunctionClass.getFunction",
		"arithmeticMultiplyFunctionClass.getFunction", "arithmeticDivideFunctionClass.getFunction",
		"arithmeticIntDivideFunctionClass.getFunction", "arithmeticModFunctionClass.getFunction",
		"arithmeticDivideFunctionClass.setType4DivDecimal", "arithmeticDivideFunctionClass.setType4DivReal",
		"arithmeticModFunctionClass.setType4ModRealOrDecimal", "isConstantBinaryLiteral",
		"numericContextResultType", "setFlenDecimal4RealOrDecimal", "testIfSumOverflowsUll",
		"builtinArithmeticMinusIntSig.overflowCheck", "builtinArithmeticPlusIntSig.plusUU",
		"builtinArithmeticPlusIntSig.plusUS", "builtinArithmeticPlusIntSig.plusSU",
		"builtinArithmeticPlusIntSig.plusSS", "builtinArithmeticIntDivideIntSig.divideUU",
		"builtinArithmeticIntDivideIntSig.divideUS", "builtinArithmeticIntDivideIntSig.divideSU",
		"builtinArithmeticIntDivideIntSig.divideSS",
		"ceilFunctionClass.getFunction", "floorFunctionClass.getFunction", "getEvalTp4FloorAndCeil",
		"setFlag4FloorAndCeil")
}

func init() {
	castCoverage["arbuild"] = vec.Coverage{Min: 3000, GoFuncs: append(arSigFuncs(), isNullGoFuncs()...)}
}

func castExecD085(op string, p []json.RawMessage) any {
	if op != "arbuild" {
		return nil
	}
	ctx, ev := castCtx(p[3])
	fts, input := evInput(p[4], p[5], p[6])
	m := map[string]any{}
	var args []expression.Expression
	m["args"] = exTry(func() any {
		args = evExprs(ctx, p[2], fts)
		ev.TruncateWarnings(0)
		return nil
	})
	if args == nil {
		return m
	}
	var e expression.Expression
	m["res"] = exTry(func() any {
		var err error
		tp := types.NewFieldType(mysql.TypeUnspecified)
		switch arg[int](p, 0) {
		case 0:
			e, err = expression.NewFunctionBase(ctx, arg[string](p, 1), tp, args...)
		case 1:
			e, err = expression.NewFunction(ctx, arg[string](p, 1), tp, args...)
		default:
			e, err = expression.NewFunctionTryFold(ctx, arg[string](p, 1), tp, args...)
		}
		if err != nil {
			e = nil
			return exErr(err)
		}
		return sfDump(e)
	})
	m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
	if e != nil {
		tp := e.GetType(ev)
		for _, mode := range []struct {
			key string
			on  bool
		}{{"row", false}, {"vec", true}} {
			m[mode.key] = exTry(func() any {
				col := chunk.NewColumn(tp, input.NumRows())
				err := expression.EvalExpr(ev, mode.on, e, tp.EvalType(), input, col)
				o := map[string]any{"err": evErrOrNil(err), "warns": ecWarns(ev.TruncateWarnings(0))}
				if err == nil {
					o["v"] = evColumn(col, tp, col.Rows())
				}
				return o
			})
		}
	}
	return m
}

// ---------------------------------------------------------------- generation

// arCtx: timeCtx (type flags, error levels, packet, SQL mode, statement time) with the
// arithmetic SQL modes and a div_precision_increment.
func (g *castGen) arCtx() []any {
	c := g.timeCtx()
	mode := c[3].(uint64)
	if g.pick(3) == 0 {
		mode |= uint64(mysql.ModeNoUnsignedSubtraction)
	}
	if g.pick(2) == 0 {
		mode |= uint64(mysql.ModeErrorForDivisionByZero)
	}
	c[3] = mode
	return append(c, []int{0, 4, 4, 1, 8, 30}[g.pick(6)])
}

var arBigInts = []int64{0, 1, -1, 2, -2, 7, -7, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1,
	1 << 32, -(1 << 32), 1 << 62, 3037000500}
var arBigUints = []uint64{0, 1, 2, 7, math.MaxUint64, math.MaxUint64 - 1, 1 << 63, (1 << 63) - 1, 1 << 32, 4294967296 * 4294967295}
var arReals = []float64{0, 1, -1, 0.5, -0.5, 2.5, 1e308, -1e308, 1.7976931348623157e308, 5e-324, 3, 7,
	math.Inf(1), math.Inf(-1), math.NaN(), 1e154, 1e155}
var arDecs = []string{"0", "1", "-1", "0.5", "-0.5", "2.5", "3", "7", "0.0000001", "1e-30",
	"99999999999999999999999999999999999999999999999999999999999999999",
	"-99999999999999999999999999999999999999999999999999999999999999999",
	"12345678901234567890.123456789012345678901234567890", "9223372036854775807", "9223372036854775808",
	"18446744073709551615", "18446744073709551616", "-9223372036854775808", "-9223372036854775809",
	"0.3333333333333333333333333333", "1977.5", "2.0", "1.999999999"}

// arCol: a column for the arithmetic: BIGINT (signed / unsigned) with the boundary values,
// DOUBLE (with Inf / NaN), DECIMAL (short and long), the generic argument columns
// (smaller ints, strings), hybrid / FLOAT, YEAR, time / JSON.
func (g *castGen) arCol() (*types.FieldType, func() []any) {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.NotNullFlag
	}
	var ft *types.FieldType
	var gen func() []any
	switch g.pick(12) {
	case 0, 1:
		ft = castFt(mysql.TypeLonglong, flag, 20, 0)
		gen = func() []any { return []any{types.KindInt64, arBigInts[g.pick(len(arBigInts))]} }
	case 2, 3:
		ft = castFt(mysql.TypeLonglong, flag|mysql.UnsignedFlag, 20, 0)
		gen = func() []any { return []any{types.KindUint64, arBigUints[g.pick(len(arBigUints))]} }
	case 4:
		ft = castFt(mysql.TypeDouble, flag, []int{22, -1}[g.pick(2)], []int{-1, 2}[g.pick(2)])
		gen = func() []any {
			return []any{types.KindFloat64, math.Float64bits(arReals[g.pick(len(arReals))])}
		}
	case 5, 6:
		p := []int{10, 30, 65, 5, 20}[g.pick(5)]
		s := []int{0, 2, 5, 30, 0}[g.pick(5)]
		if s > p {
			s = p
		}
		if g.pick(3) == 0 {
			flag |= mysql.UnsignedFlag
		}
		ft = castFt(mysql.TypeNewDecimal, flag, p, s)
		gen = func() []any { return []any{types.KindMysqlDecimal, arDecs[g.pick(len(arDecs))]} }
	case 7, 8:
		ft = g.argFt()
		gen = func() []any { return g.val(ft) }
	case 9:
		ft, gen = g.convCol()
	case 10:
		ft, gen = g.cmpYearCol()
	default:
		ft, gen = g.fromCol()
	}
	return ft, func() []any {
		if !mysql.HasNotNullFlag(ft.GetFlag()) && g.pick(6) == 0 {
			return []any{types.KindNull}
		}
		return gen()
	}
}

// arConst: a constant recipe: the boundary values of every kind, strings, NULL.
func (g *castGen) arConst() any {
	switch g.pick(7) {
	case 0:
		ft := castFt(mysql.TypeLonglong, 0, 20, 0)
		return []any{"const", []any{types.KindInt64, arBigInts[g.pick(len(arBigInts))]}, sfFtIn(ft)}
	case 1:
		ft := castFt(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0)
		return []any{"const", []any{types.KindUint64, arBigUints[g.pick(len(arBigUints))]}, sfFtIn(ft)}
	case 2:
		ft := castFt(mysql.TypeDouble, 0, 22, -1)
		return []any{"const", []any{types.KindFloat64, math.Float64bits(arReals[g.pick(len(arReals)-3)])}, sfFtIn(ft)}
	case 3:
		ft := castFt(mysql.TypeNewDecimal, 0, []int{30, 65, 10}[g.pick(3)], []int{5, 30, 2}[g.pick(3)])
		return []any{"const", []any{types.KindMysqlDecimal, arDecs[g.pick(len(arDecs))]}, sfFtIn(ft)}
	case 4:
		ft := types.NewFieldType(mysql.TypeNull)
		return []any{"const", []any{types.KindNull}, sfFtIn(ft)}
	default:
		c, _ := g.cmpConst()
		return c
	}
}

// arLeaf: an argument recipe over the columns: a column, a constant, a CAST, a nested
// arithmetic or CEIL / FLOOR.
func (g *castGen) arLeaf(fts []*types.FieldType, depth int) any {
	switch k := g.pick(12); {
	case k < 6:
		return []any{"col", g.pick(len(fts))}
	case k < 9:
		return g.arConst()
	case k < 10 || depth > 0:
		return []any{"cast", sfFtIn(g.target()), []any{"col", g.pick(len(fts))}}
	case k < 11:
		return []any{"fn", arNames[g.pick(len(arNames))], []any{g.arLeaf(fts, depth+1), g.arLeaf(fts, depth+1)}}
	default:
		return []any{"fn", cfNames[g.pick(len(cfNames))], []any{g.arLeaf(fts, depth+1)}}
	}
}

// arRows: n rows of the column generators (first error rows not at row 0: random rows).
func (g *castGen) arRows(gens []func() []any, n int) []any {
	rows := []any{}
	for r := 0; r < n; r++ {
		row := []any{}
		for _, gen := range gens {
			row = append(row, gen())
		}
		rows = append(rows, row)
	}
	return rows
}

func (g *castGen) arSel(n int) any {
	if g.pick(4) != 0 {
		return nil
	}
	s := []int{}
	for r := 0; r < n; r++ {
		if g.pick(2) == 0 {
			s = append(s, r)
		}
	}
	return s
}

func castGenerateD085(vg *vec.Gen, g *castGen) {
	// Random columns and argument recipes, every arithmetic name and CEIL / FLOOR.
	for i := 0; i < 2400; i++ {
		nc := 1 + g.pick(3)
		fts := []*types.FieldType{}
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < nc; c++ {
			ft, gen := g.arCol()
			fts = append(fts, ft)
			gens = append(gens, gen)
			cols = append(cols, sfFtIn(ft))
		}
		n := g.pick(16)
		rows := g.arRows(gens, n)
		var name string
		var args []any
		if g.pick(5) == 0 {
			name = cfNames[g.pick(len(cfNames))]
			args = []any{g.arLeaf(fts, 0)}
		} else {
			name = arNames[g.pick(len(arNames))]
			args = []any{g.arLeaf(fts, 0), g.arLeaf(fts, 0)}
		}
		vg.Add("sys", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], name, args, g.arCtx(), cols, rows, g.arSel(n))
	}
	// Two BIGINT columns (signed / unsigned): the overflow checks of every flag combination,
	// the error value hidden under NULL (a NULL row's data is 0: 0 - MinInt64, unsigned 0 +
	// -1 overflow) and the first error row not at row 0 (D08.1 T6c (a): PlusInt and
	// MultiplyInt are `checked`).
	for i := 0; i < 900; i++ {
		var fl [2]uint
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < 2; c++ {
			if g.pick(2) == 0 {
				fl[c] |= mysql.UnsignedFlag
			}
			ft := castFt(mysql.TypeLonglong, fl[c], 20, 0)
			cols = append(cols, sfFtIn(ft))
			uns := fl[c] != 0
			gens = append(gens, func() []any {
				if g.pick(5) == 0 {
					return []any{types.KindNull}
				}
				if uns {
					return []any{types.KindUint64, arBigUints[g.pick(len(arBigUints))]}
				}
				return []any{types.KindInt64, arBigInts[g.pick(len(arBigInts))]}
			})
		}
		n := 1 + g.pick(12)
		rows := g.arRows(gens, n)
		// Mostly harmless rows first: the first overflowing row lands at a random index.
		for r := 0; r < n/2; r++ {
			row := rows[r].([]any)
			for c := 0; c < 2; c++ {
				if g.pick(3) != 0 {
					if fl[c] != 0 {
						row[c] = []any{types.KindUint64, uint64(g.pick(5))}
					} else {
						row[c] = []any{types.KindInt64, int64(g.pick(5))}
					}
				}
			}
		}
		var a, b any = []any{"col", 0}, []any{"col", 1}
		if g.pick(4) == 0 {
			b = g.arConst()
		}
		if g.pick(2) == 0 {
			a, b = b, a
		}
		vg.Add("int", "arbuild", []int{0, 0, 1}[g.pick(3)], arNames[g.pick(len(arNames))], []any{a, b},
			g.arCtx(), cols, rows, g.arSel(n))
	}
	// DOUBLE columns with Inf / NaN (not finite sums are errors, products only when
	// infinite; NULL rows with an infinite partner hide the error).
	for i := 0; i < 400; i++ {
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < 2; c++ {
			ft := castFt(mysql.TypeDouble, 0, 22, -1)
			cols = append(cols, sfFtIn(ft))
			gens = append(gens, func() []any {
				if g.pick(5) == 0 {
					return []any{types.KindNull}
				}
				return []any{types.KindFloat64, math.Float64bits(arReals[g.pick(len(arReals))])}
			})
		}
		n := 1 + g.pick(12)
		vg.Add("real", "arbuild", []int{0, 0, 1}[g.pick(3)], arNames[g.pick(len(arNames))],
			[]any{[]any{"col", 0}, []any{"col", 1}}, g.arCtx(), cols, g.arRows(gens, n), g.arSel(n))
	}
	// DECIMAL columns (short and long precision): overflow, division by zero, truncation,
	// div_precision_increment, DIV of large quotients, MOD.
	for i := 0; i < 700; i++ {
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < 2; c++ {
			var flag uint
			if g.pick(4) == 0 {
				flag |= mysql.UnsignedFlag
			}
			p := []int{65, 65, 30, 10, 20}[g.pick(5)]
			s := []int{0, 30, 5, 2, 0}[g.pick(5)]
			if s > p {
				s = p
			}
			ft := castFt(mysql.TypeNewDecimal, flag, p, s)
			cols = append(cols, sfFtIn(ft))
			gens = append(gens, func() []any {
				if g.pick(6) == 0 {
					return []any{types.KindNull}
				}
				return []any{types.KindMysqlDecimal, arDecs[g.pick(len(arDecs))]}
			})
		}
		n := 1 + g.pick(10)
		var a, b any = []any{"col", 0}, []any{"col", 1}
		if g.pick(4) == 0 {
			b = g.arConst()
		}
		vg.Add("dec", "arbuild", []int{0, 0, 1}[g.pick(3)], arNames[g.pick(len(arNames))], []any{a, b},
			g.arCtx(), cols, g.arRows(gens, n), g.arSel(n))
	}
	// CEIL / CEILING / FLOOR over every kind of column and constant: the return type
	// (BIGINT, DECIMAL for long decimals, DOUBLE), unsigned flags, negative decimals.
	for i := 0; i < 700; i++ {
		ft, gen := g.arCol()
		if g.pick(3) == 0 {
			p := []int{17, 18, 19, 20, 65}[g.pick(5)]
			s := []int{0, 1, 2, 30}[g.pick(4)]
			if s > p {
				s = p
			}
			var flag uint
			if g.pick(3) == 0 {
				flag |= mysql.UnsignedFlag
			}
			ft = castFt(mysql.TypeNewDecimal, flag, p, s)
			gen = func() []any {
				if g.pick(6) == 0 {
					return []any{types.KindNull}
				}
				return []any{types.KindMysqlDecimal, arDecs[g.pick(len(arDecs))]}
			}
		}
		n := g.pick(12)
		var a any = []any{"col", 0}
		if g.pick(4) == 0 {
			a = g.arConst()
		}
		vg.Add("ceil", "arbuild", []int{0, 0, 1}[g.pick(3)], cfNames[g.pick(len(cfNames))], []any{a},
			g.arCtx(), []any{sfFtIn(ft)}, g.arRows([]func() []any{gen}, n), g.arSel(n))
	}
	// The comparisons that reach RefineComparedConstant's Ceil / Floor (D08.4 spec §4 item
	// 2, D08.4 T6; Lucky, review of tidb-rs #98): YEAR columns against DOUBLE, short
	// DECIMAL, long DECIMAL and string constants, integer columns against non-integral ones.
	for i := 0; i < 900; i++ {
		var ft *types.FieldType
		var gen func() []any
		switch g.pick(3) {
		case 0, 1:
			ft, gen = g.cmpYearCol()
		default:
			var flag uint
			if g.pick(2) == 0 {
				flag |= mysql.UnsignedFlag
			}
			ft = castFt([]byte{mysql.TypeLonglong, mysql.TypeTiny, mysql.TypeLong}[g.pick(3)], flag, 11, 0)
			gen = func() []any { return g.intVal(mysql.HasUnsignedFlag(ft.GetFlag())) }
		}
		n := g.pick(10)
		rows := []any{}
		for r := 0; r < n; r++ {
			if g.pick(6) == 0 {
				rows = append(rows, []any{[]any{types.KindNull}})
			} else {
				rows = append(rows, []any{gen()})
			}
		}
		var c any
		switch g.pick(4) {
		case 0:
			v := []float64{2.0, 2.5, 1.5, 1977.5, 69.5, 70.5, 2e0, 2019.4, -0.5, 0.5, 99.9}[g.pick(11)]
			c = []any{"const", []any{types.KindFloat64, math.Float64bits(v)}, sfFtIn(castFt(mysql.TypeDouble, 0, 22, -1))}
		case 1:
			v := []string{"2.0", "2.5", "1977.5", "69.5", "1.5", "0.5", "-0.5"}[g.pick(7)]
			c = []any{"const", []any{types.KindMysqlDecimal, v}, sfFtIn(castFt(mysql.TypeNewDecimal, 0, 10, 2))}
		case 2:
			v := []string{"2.0", "2.5", "1977.5", "69.5", "1.5", "0.5", "-0.5",
				"12345678901234567890.5"}[g.pick(8)]
			c = []any{"const", []any{types.KindMysqlDecimal, v}, sfFtIn(castFt(mysql.TypeNewDecimal, 0, 65, 30))}
		default:
			v := []string{"2", "2.0", "2.5", "1977.5", "69.5", "2e0", "0.5", "-0.5"}[g.pick(8)]
			c = []any{"const", []any{types.KindString, vec.GoString(v), charset.CollationUTF8MB4}, sfFtIn(castFt(mysql.TypeVarchar, 0, 64, 0))}
		}
		var a, b any = []any{"col", 0}, c
		if g.pick(2) == 0 {
			a, b = b, a
		}
		vg.Add("ceilcmp", "cmpbuild", g.pick(2), []string{ast.LT, ast.LE, ast.GT, ast.GE}[g.pick(4)], a, b,
			g.timeCtx(), []any{sfFtIn(ft)}, rows, nil)
	}
	// Folding constants: NewFunction (fold 1) keeps the folded constant (or the error);
	// NewFunctionTryFold (fold -1) returns the unfolded function when folding warns, e.g.
	// division by zero at warning level or a truncated string argument (D08.1 T6c (c)).
	for i := 0; i < 600; i++ {
		var name string
		var args []any
		if g.pick(5) == 0 {
			name = cfNames[g.pick(len(cfNames))]
			args = []any{g.arConst()}
		} else {
			name = arNames[g.pick(len(arNames))]
			a, b := g.arConst(), g.arConst()
			if g.pick(3) == 0 {
				b = []any{"const", []any{types.KindInt64, int64(0)}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}
			}
			if g.pick(4) == 0 {
				s := []string{"12abc", "abc", " 1x", "1e999", "1"}[g.pick(5)]
				a = []any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(castFt(mysql.TypeVarchar, 0, 64, 0))}
			}
			args = []any{a, b}
		}
		lv := []int{1, 1, 1, 1, 1, 1, 1}
		if g.pick(4) == 0 {
			lv = []int{0, 0, 0, 0, 0, 0, 0}
		}
		var mode mysql.SQLMode
		if g.pick(2) == 0 {
			mode |= mysql.ModeErrorForDivisionByZero
		}
		ctx := []any{uint16([]int{0, 2, 1, 4}[g.pick(4)]), lv, uint64(67108864), uint64(mode)}
		src := "arfold"
		variant := []int{0, 0, 2, 2, 1, 4}[g.pick(6)]
		if variant == 2 {
			src = "artryfold"
		}
		vg.Add(src, "newfunc", variant, name, sfFtIn(types.NewFieldType(mysql.TypeUnspecified)), args, ctx, "")
	}
}
