package domains

// Domain "cast", constant propagation (D08.2 constant_propagation.go; appended after D08.5
// batch 2, earlier records are unchanged):
//   - "cprop": [variant, conds, filters, ctx, cols, nOuter, nullSensitive] -> variant 0:
//     PropagateConstant(ctx, conds); variant 1: PropConstOverOuterJoin(ctx, conds (join),
//     filters, outer schema = cols[:nOuter], inner schema = cols[nOuter:], nullSensitive).
//     "args" (building the recipes), "res" (the result lists, each item dumped with sfDump),
//     "warns". The conditions are column = constant / column = column / comparisons /
//     arithmetic / AND / OR / NOT / IS NULL / <=> over the arithmetic columns of D08.5 batch
//     1 (signed and unsigned integers, YEAR, DOUBLE, DECIMAL, strings of several collations,
//     ENUM / SET, BIT, time) and constants of the column's own type or of other types
//     (YEAR against DOUBLE / DECIMAL / string constants, tidb-rs #99), NULL and constant
//     TRUE / FALSE items. Column i is UniqueID i+1 (as in the "arbuild" recipes).
//   - "arbuild" (D08.5 op), source "isnull": IS NULL over every argument type, evaluated with
//     vectorization off and on (the signatures BuildNotNullExpr builds).

import (
	"encoding/json"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

func cpGoFuncs() []string {
	return []string{
		// constant_propagation.go (D08.2)
		"PropConstOverOuterJoin", "PropagateConstant", "ValidCompareConstantPredicate",
		"ValidCompareConstantPredicateHelper", "basePropConstSolver.dealWithPossibleHybridType",
		"basePropConstSolver.getColID", "basePropConstSolver.insertCol", "basePropConstSolver.tryToUpdateEQList",
		"newPropConstSolver", "propConstSolver.PropagateConstant", "propConstSolver.pickNewEQConds",
		"propConstSolver.propagateColumnEQ", "propConstSolver.propagateConstantEQ",
		"propConstSolver.setConds2ConstFalse", "propConstSolver.solve",
		"propOuterJoinConstSolver.colsFromOuterAndInner", "propOuterJoinConstSolver.deriveConds",
		"propOuterJoinConstSolver.pickEQCondsOnOuterCol", "propOuterJoinConstSolver.pickNewEQConds",
		"propOuterJoinConstSolver.propagateColumnEQ", "propOuterJoinConstSolver.propagateConstantEQ",
		"propOuterJoinConstSolver.setConds2ConstFalse", "propOuterJoinConstSolver.solve",
		"propOuterJoinConstSolver.validColEqualCond", "propagateConstantDNF", "tryToReplaceCond",
		"validEqualCond",
		// expression.go (D08.1)
		"SplitCNFItems", "SplitDNFItems", "splitNormalFormItems", "ComposeCNFCondition",
		"ComposeDNFCondition", "composeConditionWithBinaryOp",
	}
}

func isNullGoFuncs() []string {
	out := []string{"isNullFunctionClass.getFunction", "evalIsNull"}
	for _, s := range []string{"Int", "Decimal", "Real", "Time", "Duration"} {
		n := "builtin" + s + "IsNullSig."
		out = append(out, n+"evalInt", n+"vecEvalInt", n+"vectorized", n+"Clone")
	}
	// builtinStringIsNullSig's vecEvalInt / vectorized live in builtin_string_vec.go (D08.6)
	out = append(out, "builtinStringIsNullSig.evalInt", "builtinStringIsNullSig.Clone")
	return append(out, "builtinVectorFloat32IsNullSig.evalInt", "builtinVectorFloat32IsNullSig.Clone")
}

func init() {
	castCoverage["cprop"] = vec.Coverage{Min: 2000, GoFuncs: cpGoFuncs()}
	// the "arbuild" coverage (cast_d085.go init, which runs after this file's) appends
	// isNullGoFuncs
}

func cpDumpList(es []expression.Expression) any {
	out := []any{}
	for _, e := range es {
		out = append(out, sfDump(e))
	}
	return out
}

func cpWalk(e expression.Expression, f func(expression.Expression)) {
	switch x := e.(type) {
	case *expression.Constant:
		f(x)
	case *expression.ScalarFunction:
		f(x)
		for _, a := range x.GetArgs() {
			cpWalk(a, f)
		}
	}
}

func cpDumpString(e expression.Expression) string {
	b, err := json.Marshal(sfDump(e))
	must(err)
	return string(b)
}

func castExecD082c(op string, p []json.RawMessage) any {
	if op != "cprop" {
		return nil
	}
	ctx, ev := castCtx(p[3])
	fts := []*types.FieldType{}
	for _, f := range exArr(p[4]) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	m := map[string]any{}
	var conds, filters []expression.Expression
	ok := false
	m["args"] = exTry(func() any {
		conds = evExprs(ctx, p[1], fts)
		filters = evExprs(ctx, p[2], fts)
		ev.TruncateWarnings(0)
		ok = true
		return nil
	})
	if !ok {
		return m
	}
	// Go changes nodes of the input conditions in place while it builds new conditions over
	// them (the YEAR constant of RefineComparedConstant, PropagateType on a DECIMAL
	// argument, BuildCastFunction clearing NotNullFlag on the CAST's type); every holder of
	// the node sees the change. tidb-rs changes copies (D08.1 spec §4, D08.4 §4 item 1), so
	// these records are marked "known_deviation" and skipped by the replay (D08.2 spec §4).
	snap := map[expression.Expression]string{}
	for _, c := range append(append([]expression.Expression{}, conds...), filters...) {
		cpWalk(c, func(e expression.Expression) { snap[e] = cpDumpString(e) })
	}
	defer func() {
		for e, d := range snap {
			if cpDumpString(e) != d {
				m["known_deviation"] = "in-place"
				return
			}
		}
	}()
	m["res"] = exTry(func() any {
		if arg[int](p, 0) == 0 {
			return cpDumpList(expression.PropagateConstant(ctx, conds))
		}
		nOuter := arg[int](p, 5)
		var outer, inner []*expression.Column
		for i, ft := range fts {
			c := &expression.Column{UniqueID: int64(i + 1), Index: i, RetType: ft, OrigName: "c"}
			if i < nOuter {
				outer = append(outer, c)
			} else {
				inner = append(inner, c)
			}
		}
		j, f := expression.PropConstOverOuterJoin(ctx, conds, filters, expression.NewSchema(outer...),
			expression.NewSchema(inner...), arg[bool](p, 6))
		return map[string]any{"join": cpDumpList(j), "filter": cpDumpList(f)}
	})
	m["warns"] = ecWarns(ev.TruncateWarnings(0))
	return m
}

// ---------------------------------------------------------------- generation

// cpConstFor: a constant for column i: of the column's own type, from the column's value
// generator (so that equalities hold or clash), or of another type.
func (g *castGen) cpConstFor(fts []*types.FieldType, gens []func() []any, i int) any {
	ft := fts[i]
	switch g.pick(5) {
	case 0, 1:
		v := gens[i]()
		if len(v) > 0 && v[0] == types.KindNull {
			return g.arConst()
		}
		cft := ft.Clone()
		cft.AddFlag(mysql.NotNullFlag)
		return []any{"const", v, sfFtIn(cft)}
	default:
		return g.arConst()
	}
}

// cpCond: a CNF item over the columns.
func (g *castGen) cpCond(fts []*types.FieldType, gens []func() []any, depth int) any {
	col := func() (int, any) {
		i := g.pick(len(fts))
		return i, []any{"col", i}
	}
	switch k := g.pick(20); {
	case k < 6:
		i, c := col()
		con := g.cpConstFor(fts, gens, i)
		if g.pick(3) == 0 {
			return []any{"fn", ast.EQ, []any{con, c}}
		}
		return []any{"fn", ast.EQ, []any{c, con}}
	case k < 10:
		_, a := col()
		_, b := col()
		return []any{"fn", ast.EQ, []any{a, b}}
	case k < 13:
		i, c := col()
		op := []string{ast.LT, ast.LE, ast.GT, ast.GE, ast.NE}[g.pick(5)]
		return []any{"fn", op, []any{c, g.cpConstFor(fts, gens, i)}}
	case k < 14:
		_, a := col()
		op := []string{ast.LT, ast.EQ, ast.GE}[g.pick(3)]
		ar := []string{ast.Plus, ast.Minus, ast.Mul}[g.pick(3)]
		return []any{"fn", op, []any{[]any{"fn", ar, []any{a, g.arConst()}}, g.arConst()}}
	case k < 15:
		// a constant item: TRUE, FALSE or NULL
		ft := castFt(mysql.TypeLonglong, 0, 1, 0)
		switch g.pick(3) {
		case 0:
			return []any{"const", []any{types.KindInt64, int64(1)}, sfFtIn(ft)}
		case 1:
			return []any{"const", []any{types.KindInt64, int64(0)}, sfFtIn(ft)}
		default:
			return []any{"const", []any{types.KindNull}, sfFtIn(types.NewFieldType(mysql.TypeNull))}
		}
	case k < 16:
		_, a := col()
		if g.pick(2) == 0 {
			_, b := col()
			return []any{"fn", ast.NullEQ, []any{a, b}}
		}
		return []any{"fn", ast.IsNull, []any{a}}
	case k < 18 && depth < 2:
		op := []string{ast.LogicOr, ast.LogicOr, ast.LogicAnd}[g.pick(3)]
		return []any{"fn", op, []any{g.cpCond(fts, gens, depth+1), g.cpCond(fts, gens, depth+1)}}
	case k < 19 && depth < 2:
		return []any{"fn", ast.UnaryNot, []any{g.cpCond(fts, gens, depth+1)}}
	default:
		i, c := col()
		return []any{"fn", ast.EQ, []any{c, g.cpConstFor(fts, gens, i)}}
	}
}

// cpCols: n columns; the YEAR / string / ENUM columns come often so that equal types meet.
func (g *castGen) cpCols(n int) ([]*types.FieldType, []func() []any) {
	fts := []*types.FieldType{}
	gens := []func() []any{}
	var last *types.FieldType
	var lastGen func() []any
	for c := 0; c < n; c++ {
		var ft *types.FieldType
		var gen func() []any
		switch {
		case last != nil && g.pick(3) == 0:
			// the same type as the previous column (column equalities propagate only
			// between columns of one type)
			ft, gen = last.Clone(), lastGen
			if g.pick(2) == 0 {
				ft.SetFlag(ft.GetFlag() ^ mysql.NotNullFlag)
			}
		case g.pick(5) == 0:
			ft, gen = g.cmpYearCol()
		default:
			ft, gen = g.arCol()
		}
		fts = append(fts, ft)
		gens = append(gens, gen)
		last, lastGen = ft, gen
	}
	return fts, gens
}

func (g *castGen) cpConds(fts []*types.FieldType, gens []func() []any, max int) []any {
	out := []any{}
	for n := g.pick(max + 1); n > 0; n-- {
		out = append(out, g.cpCond(fts, gens, 0))
	}
	return out
}

func castGenerateD082c(vg *vec.Gen, g *castGen) {
	// PropagateConstant
	for i := 0; i < 1500; i++ {
		fts, gens := g.cpCols(1 + g.pick(4))
		cols := []any{}
		for _, ft := range fts {
			cols = append(cols, sfFtIn(ft))
		}
		vg.Add("sys", "cprop", 0, g.cpConds(fts, gens, 6), []any{}, g.timeCtx(), cols, 0, false)
	}
	// YEAR columns against DOUBLE / DECIMAL / string constants and each other (#99)
	for i := 0; i < 300; i++ {
		ft0, _ := g.cmpYearCol()
		ft1 := ft0.Clone()
		conds := []any{[]any{"fn", ast.EQ, []any{[]any{"col", 0}, []any{"col", 1}}}}
		for n := 1 + g.pick(3); n > 0; n-- {
			op := []string{ast.EQ, ast.LT, ast.GE}[g.pick(3)]
			c, _ := g.cmpConst()
			if g.pick(2) == 0 {
				c = g.arConst()
			}
			conds = append(conds, []any{"fn", op, []any{[]any{"col", g.pick(2)}, c}})
		}
		g.rng.Shuffle(len(conds), func(a, b int) { conds[a], conds[b] = conds[b], conds[a] })
		vg.Add("year", "cprop", 0, conds, []any{}, g.timeCtx(), []any{sfFtIn(ft0), sfFtIn(ft1)}, 0, false)
	}
	// String columns of several collations: column equalities between two columns of one
	// collation, comparisons with a column or constant of another collation (tryToReplaceCond
	// skips a column whose collation differs from the condition's; ColumnSubstitute keeps
	// the old argument when the collation would change).
	cpColls := [][2]string{{"utf8mb4", "utf8mb4_bin"}, {"utf8mb4", "utf8mb4_general_ci"},
		{"utf8mb4", "utf8mb4_unicode_ci"}, {"binary", "binary"}, {"latin1", "latin1_bin"}, {"utf8", "utf8_bin"}}
	strFt := func(c [2]string) *types.FieldType {
		ft := castFt(mysql.TypeVarchar, 0, 64, 0)
		ft.SetCharset(c[0])
		ft.SetCollate(c[1])
		if c[0] == "binary" {
			ft.AddFlag(mysql.BinaryFlag)
		}
		return ft
	}
	strConst := func(c [2]string) any {
		s := []string{"a", "A", "a ", "b", "abc", "1"}[g.pick(6)]
		return []any{"const", []any{types.KindString, vec.GoString(s), c[1]}, sfFtIn(strFt(c))}
	}
	for i := 0; i < 400; i++ {
		c0 := cpColls[g.pick(len(cpColls))]
		c2 := cpColls[g.pick(len(cpColls))]
		fts := []*types.FieldType{strFt(c0), strFt(c0), strFt(c2)}
		cols := []any{sfFtIn(fts[0]), sfFtIn(fts[1]), sfFtIn(fts[2])}
		conds := []any{[]any{"fn", ast.EQ, []any{[]any{"col", 0}, []any{"col", 1}}}}
		for n := 1 + g.pick(3); n > 0; n-- {
			var a any = []any{"col", g.pick(2)}
			var b any = []any{"col", 2}
			if g.pick(2) == 0 {
				b = strConst(cpColls[g.pick(len(cpColls))])
			}
			op := []string{ast.EQ, ast.LT, ast.NE, ast.GE}[g.pick(4)]
			if g.pick(2) == 0 {
				a, b = b, a
			}
			conds = append(conds, []any{"fn", op, []any{a, b}})
		}
		if g.pick(2) == 0 {
			conds = append(conds, []any{"fn", ast.EQ, []any{[]any{"col", g.pick(3)}, strConst(cpColls[g.pick(len(cpColls))])}})
		}
		g.rng.Shuffle(len(conds), func(a, b int) { conds[a], conds[b] = conds[b], conds[a] })
		if g.pick(3) == 0 {
			vg.Add("coll", "cprop", 1, conds, []any{}, g.timeCtx(), cols, 1+g.pick(2), false)
		} else {
			vg.Add("coll", "cprop", 0, conds, []any{}, g.timeCtx(), cols, 0, false)
		}
	}
	// PropConstOverOuterJoin
	for i := 0; i < 1000; i++ {
		n := 2 + g.pick(4)
		fts, gens := g.cpCols(n)
		nOuter := 1 + g.pick(n-1)
		cols := []any{}
		for _, ft := range fts {
			cols = append(cols, sfFtIn(ft))
		}
		join := g.cpConds(fts, gens, 4)
		// an outerCol = innerCol join condition most of the time
		if g.pick(4) != 0 {
			o, in := g.pick(nOuter), nOuter+g.pick(n-nOuter)
			join = append(join, []any{"fn", ast.EQ, []any{[]any{"col", o}, []any{"col", in}}})
		}
		vg.Add("outer", "cprop", 1, join, g.cpConds(fts, gens, 3), g.timeCtx(), cols, nOuter, g.pick(4) == 0)
	}
	// IS NULL over every argument type ("arbuild" of D08.5)
	for i := 0; i < 300; i++ {
		ft, gen := g.arCol()
		n := g.pick(12)
		var a any = []any{"col", 0}
		if g.pick(4) == 0 {
			a = g.arConst()
		}
		vg.Add("isnull", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], ast.IsNull, []any{a}, g.arCtx(),
			[]any{sfFtIn(ft)}, g.arRows([]func() []any{gen}, n), g.arSel(n))
	}
}
