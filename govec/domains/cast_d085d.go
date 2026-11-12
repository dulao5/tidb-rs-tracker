package domains

// Domain "cast", D08.5 batch 4 (appended after the bit operators; earlier records are
// unchanged): the math functions of builtin_math.go except CONV and CRC32 (ABS, ROUND,
// TRUNCATE, LN / LOG / LOG2 / LOG10, RAND, POW / POWER, SIGN, SQRT, ACOS / ASIN / ATAN /
// ATAN2, COS / COT / SIN / TAN, DEGREES / RADIANS, EXP, PI).
//   - "mathbuild": the "arbuild" op of batch 1 (NewFunctionBase / NewFunction /
//     NewFunctionTryFold of name(args...), then EvalExpr with vectorization off and on) under
//     its own op name, so that its platform differences (Go's math package fuses
//     multiply-adds on arm64, Exp / Log have assembly on amd64) are registered apart from
//     the arithmetic records. The ctx has a 7th element, the seed of the ExprContext's
//     MysqlRng (castCtx index 6): RAND() reads ctx.Rng(), and without a seed Go would
//     seed it from the clock (exprstatic.NewExprContext).
//   - "mathclone": [variant, name, args, ctx, cols, rows, sel, steps]: the function is
//     built as in mathbuild, then cloned (Expression.Clone, which calls the signature's
//     Clone); steps evaluate the original (0, 2) or the clone (1, 3) with vectorization off
//     (0, 1) or on (2, 3) one after the other. RAND() and RAND(constant) keep their
//     MysqlRng in the signature and their Clone shares it (D08.1 T6c (b)): the clone
//     continues the original's sequence.
//   - "gomath": [fn, x, y]: Go's math function on float64 bit patterns (y: second
//     argument or Ldexp's exponent); out: the result's bit pattern ([frac, exp] for
//     Frexp). The edge inputs of tidb_base::gostd::math.

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
)

func mathGoFuncs() []string {
	return []string{
		"absFunctionClass.getFunction",
		"acosFunctionClass.getFunction",
		"asinFunctionClass.getFunction",
		"atanFunctionClass.getFunction",
		"builtinAbsDecSig.Clone",
		"builtinAbsDecSig.evalDecimal",
		"builtinAbsDecSig.vecEvalDecimal",
		"builtinAbsDecSig.vectorized",
		"builtinAbsIntSig.Clone",
		"builtinAbsIntSig.evalInt",
		"builtinAbsIntSig.vecEvalInt",
		"builtinAbsIntSig.vectorized",
		"builtinAbsRealSig.Clone",
		"builtinAbsRealSig.evalReal",
		"builtinAbsRealSig.vecEvalReal",
		"builtinAbsRealSig.vectorized",
		"builtinAbsUIntSig.Clone",
		"builtinAbsUIntSig.evalInt",
		"builtinAbsUIntSig.vecEvalInt",
		"builtinAbsUIntSig.vectorized",
		"builtinAcosSig.Clone",
		"builtinAcosSig.evalReal",
		"builtinAcosSig.vecEvalReal",
		"builtinAcosSig.vectorized",
		"builtinAsinSig.Clone",
		"builtinAsinSig.evalReal",
		"builtinAsinSig.vecEvalReal",
		"builtinAsinSig.vectorized",
		"builtinAtan1ArgSig.Clone",
		"builtinAtan1ArgSig.evalReal",
		"builtinAtan1ArgSig.vecEvalReal",
		"builtinAtan1ArgSig.vectorized",
		"builtinAtan2ArgsSig.Clone",
		"builtinAtan2ArgsSig.evalReal",
		"builtinAtan2ArgsSig.vecEvalReal",
		"builtinAtan2ArgsSig.vectorized",
		"builtinCosSig.Clone",
		"builtinCosSig.evalReal",
		"builtinCosSig.vecEvalReal",
		"builtinCosSig.vectorized",
		"builtinCotSig.Clone",
		"builtinCotSig.evalReal",
		"builtinCotSig.vecEvalReal",
		"builtinCotSig.vectorized",
		"builtinDegreesSig.Clone",
		"builtinDegreesSig.evalReal",
		"builtinDegreesSig.vecEvalReal",
		"builtinDegreesSig.vectorized",
		"builtinExpSig.Clone",
		"builtinExpSig.evalReal",
		"builtinExpSig.vecEvalReal",
		"builtinExpSig.vectorized",
		"builtinLog10Sig.Clone",
		"builtinLog10Sig.evalReal",
		"builtinLog10Sig.vecEvalReal",
		"builtinLog10Sig.vectorized",
		"builtinLog1ArgSig.Clone",
		"builtinLog1ArgSig.evalReal",
		"builtinLog1ArgSig.vecEvalReal",
		"builtinLog1ArgSig.vectorized",
		"builtinLog2ArgsSig.Clone",
		"builtinLog2ArgsSig.evalReal",
		"builtinLog2ArgsSig.vecEvalReal",
		"builtinLog2ArgsSig.vectorized",
		"builtinLog2Sig.Clone",
		"builtinLog2Sig.evalReal",
		"builtinLog2Sig.vecEvalReal",
		"builtinLog2Sig.vectorized",
		"builtinPISig.Clone",
		"builtinPISig.evalReal",
		"builtinPISig.vecEvalReal",
		"builtinPISig.vectorized",
		"builtinPowSig.Clone",
		"builtinPowSig.evalReal",
		"builtinPowSig.vecEvalReal",
		"builtinPowSig.vectorized",
		"builtinRadiansSig.Clone",
		"builtinRadiansSig.evalReal",
		"builtinRadiansSig.vecEvalReal",
		"builtinRadiansSig.vectorized",
		"builtinRandSig.Clone",
		"builtinRandSig.evalReal",
		"builtinRandSig.vecEvalReal",
		"builtinRandSig.vectorized",
		"builtinRandWithSeedFirstGenSig.Clone",
		"builtinRandWithSeedFirstGenSig.evalReal",
		"builtinRandWithSeedFirstGenSig.vecEvalReal",
		"builtinRandWithSeedFirstGenSig.vectorized",
		"builtinRoundDecSig.Clone",
		"builtinRoundDecSig.evalDecimal",
		"builtinRoundDecSig.vecEvalDecimal",
		"builtinRoundDecSig.vectorized",
		"builtinRoundIntSig.Clone",
		"builtinRoundIntSig.evalInt",
		"builtinRoundIntSig.vecEvalInt",
		"builtinRoundIntSig.vectorized",
		"builtinRoundRealSig.Clone",
		"builtinRoundRealSig.evalReal",
		"builtinRoundRealSig.vecEvalReal",
		"builtinRoundRealSig.vectorized",
		"builtinRoundWithFracDecSig.Clone",
		"builtinRoundWithFracDecSig.evalDecimal",
		"builtinRoundWithFracDecSig.vecEvalDecimal",
		"builtinRoundWithFracDecSig.vectorized",
		"builtinRoundWithFracIntSig.Clone",
		"builtinRoundWithFracIntSig.evalInt",
		"builtinRoundWithFracIntSig.vecEvalInt",
		"builtinRoundWithFracIntSig.vectorized",
		"builtinRoundWithFracRealSig.Clone",
		"builtinRoundWithFracRealSig.evalReal",
		"builtinRoundWithFracRealSig.vecEvalReal",
		"builtinRoundWithFracRealSig.vectorized",
		"builtinSignSig.Clone",
		"builtinSignSig.evalInt",
		"builtinSignSig.vecEvalInt",
		"builtinSignSig.vectorized",
		"builtinSinSig.Clone",
		"builtinSinSig.evalReal",
		"builtinSinSig.vecEvalReal",
		"builtinSinSig.vectorized",
		"builtinSqrtSig.Clone",
		"builtinSqrtSig.evalReal",
		"builtinSqrtSig.vecEvalReal",
		"builtinSqrtSig.vectorized",
		"builtinTanSig.Clone",
		"builtinTanSig.evalReal",
		"builtinTanSig.vecEvalReal",
		"builtinTanSig.vectorized",
		"builtinTruncateDecimalSig.Clone",
		"builtinTruncateDecimalSig.evalDecimal",
		"builtinTruncateDecimalSig.vecEvalDecimal",
		"builtinTruncateDecimalSig.vectorized",
		"builtinTruncateIntSig.Clone",
		"builtinTruncateIntSig.evalInt",
		"builtinTruncateIntSig.vecEvalInt",
		"builtinTruncateIntSig.vectorized",
		"builtinTruncateRealSig.Clone",
		"builtinTruncateRealSig.evalReal",
		"builtinTruncateRealSig.vecEvalReal",
		"builtinTruncateRealSig.vectorized",
		"builtinTruncateUintSig.Clone",
		"builtinTruncateUintSig.evalInt",
		"builtinTruncateUintSig.vecEvalInt",
		"builtinTruncateUintSig.vectorized",
		"calculateDecimal4RoundAndTruncate",
		"cosFunctionClass.getFunction",
		"cotFunctionClass.getFunction",
		"degreesFunctionClass.getFunction",
		"expFunctionClass.getFunction",
		"log10FunctionClass.getFunction",
		"log2FunctionClass.getFunction",
		"logFunctionClass.getFunction",
		"piFunctionClass.getFunction",
		"powFunctionClass.getFunction",
		"radiansFunctionClass.getFunction",
		"randFunctionClass.getFunction",
		"roundFunctionClass.getFunction",
		"signFunctionClass.getFunction",
		"sinFunctionClass.getFunction",
		"sqrtFunctionClass.getFunction",
		"tanFunctionClass.getFunction",
		"truncateFunctionClass.getFunction",
	}
}

func init() {
	castCoverage["mathbuild"] = vec.Coverage{Min: 4000, GoFuncs: mathGoFuncs()}
	castCoverage["mathclone"] = vec.Coverage{Min: 600}
	castCoverage["gomath"] = vec.Coverage{Min: 4000}
}

// mathFn: a math function name and its argument counts (min, max).
type mathFn struct {
	name     string
	min, max int
}

var mathFns = []mathFn{
	{ast.Abs, 1, 1}, {ast.Round, 1, 2}, {ast.Truncate, 2, 2}, {ast.Ln, 1, 1}, {ast.Log, 1, 2},
	{ast.Log2, 1, 1}, {ast.Log10, 1, 1}, {ast.Rand, 0, 1}, {ast.Pow, 2, 2}, {ast.Power, 2, 2},
	{ast.Sign, 1, 1}, {ast.Sqrt, 1, 1}, {ast.Acos, 1, 1}, {ast.Asin, 1, 1}, {ast.Atan, 1, 2},
	{ast.Atan2, 2, 2}, {ast.Cos, 1, 1}, {ast.Cot, 1, 1}, {ast.Degrees, 1, 1}, {ast.Exp, 1, 1},
	{ast.PI, 0, 0}, {ast.Radians, 1, 1}, {ast.Sin, 1, 1}, {ast.Tan, 1, 1},
}

func castExecD085d(op string, p []json.RawMessage) any {
	switch op {
	case "mathbuild":
		return castExecD085("arbuild", p)
	case "mathclone":
		return mathClone(p)
	case "gomath":
		return goMath(p)
	}
	return nil
}

func mathClone(p []json.RawMessage) any {
	ctx, ev := castCtx(p[3])
	fts, input := evInput(p[4], p[5], p[6])
	m := map[string]any{}
	var e, c expression.Expression
	m["res"] = exTry(func() any {
		args := evExprs(ctx, p[2], fts)
		ev.TruncateWarnings(0)
		var err error
		tp := types.NewFieldType(mysql.TypeUnspecified)
		if arg[int](p, 0) == 0 {
			e, err = expression.NewFunctionBase(ctx, arg[string](p, 1), tp, args...)
		} else {
			e, err = expression.NewFunction(ctx, arg[string](p, 1), tp, args...)
		}
		if err != nil {
			e = nil
			return exErr(err)
		}
		c = e.Clone()
		return sfDump(c)
	})
	ev.TruncateWarnings(0)
	if e == nil {
		return m
	}
	tp := e.GetType(ev)
	var steps []int
	must(json.Unmarshal(p[7], &steps))
	out := []any{}
	for _, s := range steps {
		x := e
		if s&1 != 0 {
			x = c
		}
		out = append(out, exTry(func() any {
			col := chunk.NewColumn(tp, input.NumRows())
			err := expression.EvalExpr(ev, s&2 != 0, x, tp.EvalType(), input, col)
			o := map[string]any{"err": evErrOrNil(err), "warns": ecWarns(ev.TruncateWarnings(0))}
			if err == nil {
				o["v"] = evColumn(col, tp, col.Rows())
			}
			return o
		}))
	}
	m["steps"] = out
	return m
}

func goMath(p []json.RawMessage) any {
	x := math.Float64frombits(arg[uint64](p, 1))
	y := math.Float64frombits(arg[uint64](p, 2))
	bits := func(f float64) string { return strconv.FormatUint(math.Float64bits(f), 10) }
	switch arg[string](p, 0) {
	case "exp":
		return bits(math.Exp(x))
	case "log":
		return bits(math.Log(x))
	case "log2":
		return bits(math.Log2(x))
	case "log10":
		return bits(math.Log10(x))
	case "pow":
		return bits(math.Pow(x, y))
	case "sin":
		return bits(math.Sin(x))
	case "cos":
		return bits(math.Cos(x))
	case "tan":
		return bits(math.Tan(x))
	case "asin":
		return bits(math.Asin(x))
	case "acos":
		return bits(math.Acos(x))
	case "atan":
		return bits(math.Atan(x))
	case "atan2":
		return bits(math.Atan2(x, y))
	case "sqrt":
		return bits(math.Sqrt(x))
	case "frexp":
		f, e := math.Frexp(x)
		return []any{bits(f), e}
	case "ldexp":
		return bits(math.Ldexp(x, int(int64(arg[uint64](p, 2)))))
	}
	panic("gomath")
}

// ---------------------------------------------------------------- generation

// mathReals: the edge inputs of the math functions (domain limits, overflow thresholds,
// subnormals, multiples of pi, values around the polynomial ranges).
var mathReals = []float64{0, math.Copysign(0, -1), 1, -1, 0.5, -0.5, 2, -2, 10, 100, 0.1, 1e-5,
	math.Pi, -math.Pi, math.Pi / 2, math.Pi / 4, 3 * math.Pi / 4, 2 * math.Pi, 1e10, -1e10,
	709.782712893384, 709.79, -745.1332191019411, -745.14, -708.4, 1e-300, 5e-324, 2.2250738585072014e-308,
	1.7976931348623157e308, -1.7976931348623157e308, math.Inf(1), math.Inf(-1), math.NaN(),
	0.9999999999999999, 1.0000000000000002, 0.7071067811865476, 0.7071067811865475, 1.5, 2.5, -2.5,
	123.456, -123.456, 1e15, 9.5e15, 4.5, 0.49999999999999994, 1e308, 3.3, 57.29577951308232, 1 << 53}

// mathReal: an edge value or a random float64 (a random mantissa at a chosen exponent).
func (g *castGen) mathReal() float64 {
	switch g.pick(3) {
	case 0:
		e := []int{-1074, -1060, -1022, -1000, -30, -10, -2, -1, 0, 1, 2, 5, 9, 10, 20, 28, 30, 50, 60, 100, 500, 1000, 1023}[g.pick(23)]
		f := math.Ldexp(1+float64(g.rng.Uint64()>>11)/(1<<53), e)
		if g.pick(2) == 0 {
			f = -f
		}
		return f
	case 1:
		return (g.rng.Float64() - 0.5) * []float64{2, 20, 1500, 1e6, 1e20}[g.pick(5)]
	default:
		return mathReals[g.pick(len(mathReals))]
	}
}

// mathCol: a column for the math functions: DOUBLE over mathReal, the arithmetic
// columns of batch 1 (BIGINT signed / unsigned boundaries, DECIMAL, strings, FLOAT, time
// ...), or a small-integer column (ROUND / TRUNCATE digit counts).
func (g *castGen) mathCol() (*types.FieldType, func() []any) {
	switch g.pick(4) {
	case 0, 1:
		ft := castFt(mysql.TypeDouble, 0, []int{22, -1}[g.pick(2)], []int{-1, 2, 5}[g.pick(3)])
		return ft, func() []any {
			if g.pick(8) == 0 {
				return []any{types.KindNull}
			}
			return []any{types.KindFloat64, math.Float64bits(g.mathReal())}
		}
	case 2:
		var flag uint
		if g.pick(3) == 0 {
			flag |= mysql.UnsignedFlag
		}
		ft := castFt(mysql.TypeLonglong, flag, 20, 0)
		return ft, func() []any {
			if g.pick(8) == 0 {
				return []any{types.KindNull}
			}
			v := []int64{0, 1, 2, -1, -2, 3, -3, 5, 10, 18, 19, 20, 30, 31, 40, -18, -19, -20, -40, 1 << 40,
				math.MaxInt64, math.MinInt64}[g.pick(22)]
			if flag != 0 {
				return []any{types.KindUint64, uint64(v)}
			}
			return []any{types.KindInt64, v}
		}
	default:
		return g.arCol()
	}
}

// mathConst: a constant: an edge DOUBLE, a digit count, or the constants of batch 1.
func (g *castGen) mathConst() any {
	switch g.pick(3) {
	case 0:
		return []any{"const", []any{types.KindFloat64, math.Float64bits(g.mathReal())}, sfFtIn(castFt(mysql.TypeDouble, 0, 22, -1))}
	case 1:
		v := []int64{0, 1, 2, -1, -2, 5, 30, 31, -30, 100, -100, math.MaxInt64, math.MinInt64}[g.pick(13)]
		return []any{"const", []any{types.KindInt64, v}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}
	default:
		return g.arConst()
	}
}

// mathLeaf: an argument: a column, a constant, a CAST of a column or a nested math
// function / arithmetic.
func (g *castGen) mathLeaf(fts []*types.FieldType, depth int) any {
	switch k := g.pick(12); {
	case k < 6:
		return []any{"col", g.pick(len(fts))}
	case k < 9:
		return g.mathConst()
	case k < 10 || depth > 0:
		return []any{"cast", sfFtIn(g.target()), []any{"col", g.pick(len(fts))}}
	case k < 11:
		f := mathFns[g.pick(len(mathFns))]
		return []any{"fn", f.name, g.mathArgs(f, fts, depth+1)}
	default:
		return g.arLeaf(fts, depth+1)
	}
}

func (g *castGen) mathArgs(f mathFn, fts []*types.FieldType, depth int) []any {
	n := f.min + g.pick(f.max-f.min+1)
	if depth == 0 && g.pick(40) == 0 {
		n = g.pick(4) // a wrong argument count, sometimes
	}
	args := []any{}
	for i := 0; i < n; i++ {
		args = append(args, g.mathLeaf(fts, depth))
	}
	return args
}

// mathCtx: arCtx plus the MysqlRng seed (castCtx index 6).
func (g *castGen) mathCtx() []any {
	seeds := []int64{0, 1, 2, 3, -1, 42, 12345, math.MaxInt64, math.MinInt64, 1 << 32}
	return append(g.arCtx(), seeds[g.pick(len(seeds))])
}

func (g *castGen) mathCols(nc int) ([]*types.FieldType, []func() []any, []any) {
	fts := []*types.FieldType{}
	gens := []func() []any{}
	cols := []any{}
	for c := 0; c < nc; c++ {
		ft, gen := g.mathCol()
		fts = append(fts, ft)
		gens = append(gens, gen)
		cols = append(cols, sfFtIn(ft))
	}
	return fts, gens, cols
}

func castGenerateD085d(vg *vec.Gen, g *castGen) {
	// Random columns and argument recipes, every math function.
	for i := 0; i < 3000; i++ {
		fts, gens, cols := g.mathCols(1 + g.pick(3))
		f := mathFns[g.pick(len(mathFns))]
		n := g.pick(16)
		vg.Add("math", "mathbuild", []int{0, 0, 1, 2}[g.pick(4)], f.name, g.mathArgs(f, fts, 0), g.mathCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// DOUBLE columns straight into the real functions (the gostd::math ports, warnings,
	// NULL for invalid logarithms / acos / asin, overflow of EXP / POW / COT).
	for i := 0; i < 2000; i++ {
		ft := castFt(mysql.TypeDouble, 0, 22, -1)
		gen := func() []any {
			if g.pick(10) == 0 {
				return []any{types.KindNull}
			}
			return []any{types.KindFloat64, math.Float64bits(g.mathReal())}
		}
		f := mathFns[g.pick(len(mathFns))]
		args := []any{}
		cols := []any{sfFtIn(ft)}
		gens := []func() []any{gen}
		if f.max >= 1 {
			args = append(args, []any{"col", 0})
		}
		if f.min == 2 || (f.max == 2 && g.pick(2) == 0) {
			if g.pick(3) == 0 {
				args = append(args, g.mathConst())
			} else {
				cols = append(cols, sfFtIn(ft))
				gens = append(gens, gen)
				args = append(args, []any{"col", 1})
			}
		}
		n := 1 + g.pick(12)
		vg.Add("mathreal", "mathbuild", []int{0, 0, 0, 1}[g.pick(4)], f.name, args, g.mathCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// ROUND / TRUNCATE over every kind of value with a digit-count column or constant.
	for i := 0; i < 1000; i++ {
		fts, gens, cols := g.mathCols(2)
		name := []string{ast.Round, ast.Truncate}[g.pick(2)]
		var d any = []any{"col", 1}
		if g.pick(3) == 0 {
			d = g.mathConst()
		}
		args := []any{g.mathLeaf(fts[:1], 1), d}
		if name == ast.Round && g.pick(4) == 0 {
			args = args[:1]
		}
		n := g.pick(12)
		vg.Add("mathround", "mathbuild", []int{0, 0, 1, 2}[g.pick(4)], name, args, g.mathCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// Clone: every function, RAND() / RAND(constant) / RAND(column) most of the time.
	for i := 0; i < 800; i++ {
		fts, gens, cols := g.mathCols(1 + g.pick(2))
		f := mathFns[g.pick(len(mathFns))]
		if g.pick(3) != 0 {
			f = mathFn{ast.Rand, 0, 1}
		}
		args := g.mathArgs(f, fts, 1)
		n := g.pick(10)
		steps := []int{}
		for s := 1 + g.pick(6); s > 0; s-- {
			steps = append(steps, g.pick(4))
		}
		vg.Add("mathclone", "mathclone", []int{0, 0, 1}[g.pick(3)], f.name, args, g.mathCtx(), cols,
			g.arRows(gens, n), g.arSel(n), steps)
	}
	// Go's math package on edge inputs.
	fns := []string{"exp", "log", "log2", "log10", "pow", "sin", "cos", "tan", "asin", "acos", "atan", "atan2",
		"sqrt", "frexp", "ldexp"}
	for i := 0; i < 6000; i++ {
		fn := fns[g.pick(len(fns))]
		x, y := g.mathReal(), g.mathReal()
		var yb uint64
		switch fn {
		case "ldexp":
			yb = uint64(int64([]int{0, 1, -1, 52, 1023, 1024, -1022, -1074, -1075, 2000, -2000}[g.pick(11)] + g.pick(5) - 2))
		case "pow":
			switch g.pick(4) {
			case 0:
				y = float64(g.pick(41) - 20)
			case 1:
				y = []float64{0.5, -0.5, 1.5, 1.0 / 3, 1e-10, 1e10, 1 << 63, -(1 << 63), 1e300}[g.pick(9)]
			}
			yb = math.Float64bits(y)
		default:
			yb = math.Float64bits(y)
		}
		if (fn == "asin" || fn == "acos") && g.pick(2) == 0 {
			x = g.rng.Float64()*2 - 1
		}
		vg.Add("gomath", "gomath", fn, math.Float64bits(x), yb)
	}
}
