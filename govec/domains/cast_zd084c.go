package domains

// Domain "cast", D08.4 batch 3 (appended after every earlier batch; earlier records are
// unchanged): GREATEST / LEAST (greatest/leastFunctionClass, resolveType4Extremum,
// aggregateType, unsupportedJSONComparison, fixFlenAndDecimalForGreatestAndLeast and the
// signatures over every comparison type, the strings compared as DATE / DATETIME
// (doTimeConversionForGL) and the time results converted to DATE / DATETIME), INTERVAL
// (linear and binary search) and ResolveType4Between, through the "arbuild" op of D08.5
// (build, then EvalExpr with vectorization off and on) and "mathclone" (clone, then
// evaluate the original and the clone).
//   - source "gl": 2 to 5 arguments over columns of every comparison type, constants and
//     CASTs (mixed types: the aggregated type picks the signature).
//   - source "gltime": temporal columns mixed with strings (valid and invalid date /
//     datetime strings): the CmpStringAsTime signatures and their conversion warnings.
//   - source "glstr": strings under a case-insensitive collation with values that tie
//     ('a' / 'A'): the row version keeps the earlier one, the vector version the later.
//   - source "glfb": arguments that fail or warn behind columns that are NULL on some rows:
//     the row version stops at the first NULL, the vector version evaluates every column.
//   - source "intv": INTERVAL over int / real columns (NOT NULL or nullable) and constants.
//   - source "glclone" (op "mathclone"): GREATEST / LEAST / INTERVAL cloned; the clone of
//     builtinIntervalIntSig does not copy hasNullable (it binary-searches).
//   - "between": [ctx, cols, [a, b, c]] -> ResolveType4Between(a, b, c) (int EvalType).
//   - T5c (D08.4, Lucky's review of #108): "coalrand" (coalesce(col, rand(seed)), the two
//     modes differ in Go) and "coalfsp" (CAST(COALESCE(t0, t3) AS CHAR) over durations).

import (
	"encoding/json"
	"math"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

func glGoFuncs() []string {
	out := []string{"aggregateType", "resolveType4Extremum", "unsupportedJSONComparison",
		"greatestFunctionClass.getFunction", "leastFunctionClass.getFunction",
		"fixFlenAndDecimalForGreatestAndLeast", "doTimeConversionForGL", "getAccurateTimeTypeForGLRet",
		"intervalFunctionClass.getFunction"}
	for _, gl := range []string{"Greatest", "Least"} {
		for _, k := range []struct{ sig, et string }{{"Int", "Int"}, {"Real", "Real"},
			{"Decimal", "Decimal"}, {"String", "String"}, {"Time", "Time"}, {"Duration", "Duration"},
			{"CmpStringAsTime", "String"}} {
			n := "builtin" + gl + k.sig + "Sig."
			out = append(out, n+"eval"+k.et, n+"vecEval"+k.et, n+"vectorized", n+"Clone")
		}
		out = append(out, "builtin"+gl+"VectorFloat32Sig.evalVectorFloat32", "builtin"+gl+"VectorFloat32Sig.Clone")
	}
	for _, k := range []string{"Int", "Real"} {
		n := "builtinInterval" + k + "Sig."
		out = append(out, n+"evalInt", n+"vecEvalInt", n+"vectorized", n+"Clone", n+"linearSearch", n+"binSearch")
	}
	return out
}

func init() {
	c := castCoverage["arbuild"]
	c.GoFuncs = append(c.GoFuncs, glGoFuncs()...)
	castCoverage["arbuild"] = c
	castCoverage["between"] = vec.Coverage{Min: 300, GoFuncs: []string{"ResolveType4Between"}}
}

func castExecD084c(op string, p []json.RawMessage) any {
	if op != "between" {
		return nil
	}
	ctx, ev := castCtx(p[0])
	fts := []*types.FieldType{}
	for _, f := range exArr(p[1]) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	return map[string]any{"et": exTry(func() any {
		es := evExprs(ctx, p[2], fts)
		ev.TruncateWarnings(0)
		return int(expression.ResolveType4Between(ev, [3]expression.Expression{es[0], es[1], es[2]}))
	})}
}

var glDateStrs = []string{"2024-01-02", "2024-01-02 03:04:05", "2024-01-02 03:04:05.123456", "1999-12-31",
	"0000-00-00", "2024-02-30", "abc", "", "20240102", "2024-1-2", "10:00:00", "9999-12-31 23:59:59",
	"2024-13-01", "1e3", "2024-01-02 25:00:00"}

// glDateStrCol: a VARCHAR column of date / datetime strings, valid or not.
func (g *castGen) glDateStrCol() (*types.FieldType, func() []any) {
	ft := castFt(mysql.TypeVarchar, 0, 64, 0)
	return ft, func() []any {
		if g.pick(7) == 0 {
			return []any{types.KindNull}
		}
		return []any{types.KindString, vec.GoString(glDateStrs[g.pick(len(glDateStrs))]), charset.CollationUTF8MB4}
	}
}

// glCiStrCol: a VARCHAR column under a case-insensitive collation with tying values.
func (g *castGen) glCiStrCol() (*types.FieldType, func() []any) {
	coll := []string{"utf8mb4_general_ci", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci", charset.CollationUTF8MB4}[g.pick(4)]
	ft := castFt(mysql.TypeVarchar, 0, 16, 0)
	ft.SetCollate(coll)
	return ft, func() []any {
		if g.pick(9) == 0 {
			return []any{types.KindNull}
		}
		return []any{types.KindString, vec.GoString([]string{"a", "A", "b", "B", "a ", "ä", "Ä", "ss", "ß"}[g.pick(9)]), coll}
	}
}

func (g *castGen) glCols(nc int, col func() (*types.FieldType, func() []any)) ([]*types.FieldType, []func() []any, []any) {
	fts := []*types.FieldType{}
	gens := []func() []any{}
	cols := []any{}
	for c := 0; c < nc; c++ {
		ft, gen := col()
		fts = append(fts, ft)
		gens = append(gens, gen)
		cols = append(cols, sfFtIn(ft))
	}
	return fts, gens, cols
}

// intvCol: an int or real column for INTERVAL, NOT NULL or nullable.
func (g *castGen) intvCol(real bool) (*types.FieldType, func() []any) {
	var flag uint
	if g.pick(2) == 0 {
		flag |= mysql.NotNullFlag
	}
	if real {
		ft := castFt(mysql.TypeDouble, flag, 22, -1)
		return ft, func() []any {
			if flag == 0 && g.pick(5) == 0 {
				return []any{types.KindNull}
			}
			v := []float64{-1.5, 0, 1, 2.5, 3, 10, 100, 1e20, math.Inf(1)}[g.pick(9)]
			return []any{types.KindFloat64, math.Float64bits(v)}
		}
	}
	if g.pick(3) == 0 {
		flag |= mysql.UnsignedFlag
	}
	ft := castFt(mysql.TypeLonglong, flag, 20, 0)
	return ft, func() []any {
		if flag&mysql.NotNullFlag == 0 && g.pick(5) == 0 {
			return []any{types.KindNull}
		}
		if flag&mysql.UnsignedFlag != 0 {
			return []any{types.KindUint64, []uint64{0, 1, 2, 3, 10, 100, 1 << 63, math.MaxUint64}[g.pick(8)]}
		}
		return []any{types.KindInt64, []int64{-5, -1, 0, 1, 2, 3, 10, 100, math.MinInt64, math.MaxInt64}[g.pick(10)]}
	}
}

func castGenerateD084c(vg *vec.Gen, g *castGen) {
	names := []string{ast.Greatest, ast.Least}
	// GREATEST / LEAST over random arguments.
	for i := 0; i < 2000; i++ {
		fts, gens, cols := g.glCols(1+g.pick(3), g.cmpCol)
		args := []any{}
		same := g.pick(2) == 0
		for a := 2 + g.pick(4); a > 0; a-- {
			if same {
				args = append(args, []any{"col", g.pick(len(fts))})
				continue
			}
			args = append(args, g.coalArg(fts))
		}
		n := g.pick(16)
		vg.Add("gl", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], names[g.pick(2)], args, g.arCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// Temporal columns mixed with date strings: CmpStringAsTime / the DATE results.
	for i := 0; i < 1200; i++ {
		ft0, gen0 := g.fromColTemporal()
		ft1, gen1 := g.glDateStrCol()
		args := []any{}
		for a := 2 + g.pick(3); a > 0; a-- {
			switch g.pick(4) {
			case 0:
				args = append(args, []any{"col", 0})
			case 1, 2:
				args = append(args, []any{"col", 1})
			default:
				s := glDateStrs[g.pick(len(glDateStrs))]
				args = append(args, []any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4},
					sfFtIn(castFt(mysql.TypeVarchar, 0, 64, 0))})
			}
		}
		if g.pick(2) == 0 {
			args[0] = []any{"col", 0}
		}
		n := g.pick(12)
		vg.Add("gltime", "arbuild", []int{0, 0, 1}[g.pick(3)], names[g.pick(2)], args, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1)}, g.arRows([]func() []any{gen0, gen1}, n), g.arSel(n))
	}
	// Ties under a case-insensitive collation.
	for i := 0; i < 400; i++ {
		ft, gen := g.glCiStrCol()
		args := []any{}
		for a := 2 + g.pick(3); a > 0; a-- {
			args = append(args, []any{"col", g.pick(2)})
		}
		n := 1 + g.pick(10)
		vg.Add("glstr", "arbuild", []int{0, 1}[g.pick(2)], names[g.pick(2)], args,
			g.arCtx(), []any{sfFtIn(ft), sfFtIn(ft)}, g.arRows([]func() []any{gen, gen}, n), g.arSel(n))
	}
	// Failing or warning arguments behind columns that are NULL on some rows.
	for i := 0; i < 600; i++ {
		ft0, gen0 := g.lgBoolCol()
		var flag uint
		if g.pick(3) == 0 {
			flag |= mysql.UnsignedFlag
		}
		ft1 := castFt(mysql.TypeLonglong, flag, 20, 0)
		gen1 := func() []any {
			if g.pick(6) == 0 {
				return []any{types.KindNull}
			}
			if flag != 0 {
				return []any{types.KindUint64, []uint64{0, 1, 2, 1<<64 - 1}[g.pick(4)]}
			}
			return []any{types.KindInt64, []int64{0, 1, 2, 1<<63 - 1, -1}[g.pick(5)]}
		}
		n := 1 + g.pick(12)
		rows := g.arRows([]func() []any{gen0, gen1}, n)
		for r := 0; r < n/2; r++ {
			rows[r].([]any)[1] = []any{types.KindInt64, int64(1)}
			if flag != 0 {
				rows[r].([]any)[1] = []any{types.KindUint64, uint64(1)}
			}
		}
		na := 2 + g.pick(3)
		args := make([]any, 0, na)
		failAt := 1 + g.pick(na-1)
		for a := 0; a < na; a++ {
			if a == failAt {
				args = append(args, g.lgFailing(1, 0))
			} else {
				args = append(args, []any{"col", 0})
			}
		}
		fn := names[g.pick(2)]
		if g.pick(3) == 0 {
			fn = ast.Interval
		}
		vg.Add("glfb", "arbuild", []int{0, 0, 1}[g.pick(3)], fn, args, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1)}, rows, g.arSel(n))
	}
	// INTERVAL.
	for i := 0; i < 1200; i++ {
		real := g.pick(3) == 0
		nc := 1 + g.pick(4)
		fts, gens, cols := g.glCols(nc, func() (*types.FieldType, func() []any) { return g.intvCol(real && g.pick(2) == 0) })
		args := []any{}
		for a := 2 + g.pick(5); a > 0; a-- {
			switch g.pick(5) {
			case 0, 1, 2:
				args = append(args, []any{"col", g.pick(len(fts))})
			case 3:
				c, _ := g.cmpConst()
				args = append(args, c)
			default:
				v := []int64{-1, 0, 1, 2, 5, 10, 100, 1000}[g.pick(8)]
				args = append(args, []any{"const", []any{types.KindInt64, v},
					sfFtIn(castFt(mysql.TypeLonglong, mysql.NotNullFlag, 20, 0))})
			}
		}
		n := g.pick(12)
		vg.Add("intv", "arbuild", []int{0, 0, 1}[g.pick(3)], ast.Interval, args, g.arCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// Clone (hasNullable of builtinIntervalIntSig is not copied by its Clone).
	for i := 0; i < 400; i++ {
		var fts []*types.FieldType
		var gens []func() []any
		var cols []any
		fn := []string{ast.Interval, ast.Interval, ast.Greatest, ast.Least}[g.pick(4)]
		if fn == ast.Interval {
			fts, gens, cols = g.glCols(1+g.pick(3), func() (*types.FieldType, func() []any) { return g.intvCol(g.pick(4) == 0) })
		} else {
			fts, gens, cols = g.glCols(1+g.pick(3), g.cmpCol)
		}
		args := []any{}
		for a := 2 + g.pick(4); a > 0; a-- {
			args = append(args, []any{"col", g.pick(len(fts))})
		}
		n := g.pick(10)
		steps := []int{}
		for s := 1 + g.pick(5); s > 0; s-- {
			steps = append(steps, g.pick(4))
		}
		vg.Add("glclone", "mathclone", []int{0, 0, 1}[g.pick(3)], fn, args, g.mathCtx(), cols,
			g.arRows(gens, n), g.arSel(n), steps)
	}
	// ResolveType4Between.
	for i := 0; i < 600; i++ {
		fts, _, cols := g.glCols(1+g.pick(3), g.cmpCol)
		args := []any{}
		for a := 0; a < 3; a++ {
			switch g.pick(3) {
			case 0, 1:
				args = append(args, []any{"col", g.pick(len(fts))})
			default:
				c, _ := g.cmpConst()
				args = append(args, c)
			}
		}
		vg.Add("between", "between", g.arCtx(), cols, args)
	}
	// T5c: coalesce(col, rand(seed)): the vector version evaluates rand() on every row.
	for i := 0; i < 300; i++ {
		ft, gen := g.lgBoolCol()
		seed := []int64{0, 1, 2, 42}[g.pick(4)]
		rnd := []any{"fn", ast.Rand, []any{[]any{"const", []any{types.KindInt64, seed},
			sfFtIn(castFt(mysql.TypeLonglong, mysql.NotNullFlag, 20, 0))}}}
		n := 1 + g.pick(12)
		vg.Add("coalrand", "arbuild", []int{0, 1}[g.pick(2)], ast.Coalesce, []any{[]any{"col", 0}, rnd},
			g.mathCtx(), []any{sfFtIn(ft)}, g.arRows([]func() []any{gen}, n), g.arSel(n))
	}
	// T5c: CAST(COALESCE(t0, t3) AS CHAR) over durations (the result fsp shows).
	for i := 0; i < 2000; i++ {
		fsp0, fsp1 := g.pick(7), g.pick(7)
		ft0 := castFt(mysql.TypeDuration, 0, -1, fsp0)
		ft1 := castFt(mysql.TypeDuration, 0, -1, fsp1)
		mk := func(fsp int) func() []any {
			return func() []any {
				if g.pick(4) == 0 {
					return []any{types.KindNull}
				}
				d := g.rng.Int64N(int64(types.MaxTime)) - g.rng.Int64N(int64(types.MaxTime))
				return []any{types.KindMysqlDuration, d / 1000 * 1000, fsp}
			}
		}
		// COALESCE(CAST(COALESCE(t0, t1) AS CHAR)): the outer COALESCE of one argument returns
		// the string.
		coal := []any{"fn", ast.Coalesce, []any{[]any{"col", 0}, []any{"col", 1}}}
		cast := []any{"cast", sfFtIn(castFt(mysql.TypeVarString, 0, -1, 0)), coal}
		n := 1 + g.pick(10)
		vg.Add("coalfsp", "arbuild", []int{0, 1}[g.pick(2)], ast.Coalesce, []any{cast}, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1)}, g.arRows([]func() []any{mk(fsp0), mk(fsp1)}, n), g.arSel(n))
	}
}

// fromColTemporal: a DATE / DATETIME / TIMESTAMP column (fromCol's temporal case).
func (g *castGen) fromColTemporal() (*types.FieldType, func() []any) {
	for {
		ft, gen := g.fromCol()
		if types.IsTypeTemporal(ft.GetType()) && ft.GetType() != mysql.TypeDuration {
			return ft, gen
		}
	}
}
