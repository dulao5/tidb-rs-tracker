package domains

// Domain "cast", EvaluateExprWithNull and the null-reject branch of foldConstant (D08.1
// expression.go, D08.2 constant_fold.go; appended at the end, earlier records are unchanged):
//   - "evnull": [cond, ctx, cols, schema, nullReject] -> EvaluateExprWithNull(ctx', S, cond)
//     where S holds the columns whose indexes are in `schema` (column i is UniqueID i+1, as
//     in "cprop") and ctx' is ctx, or exprctx.WithNullRejectCheck(ctx) when nullReject.
//     "args" (building the recipe), "res" (the result, dumped with sfDump), "deferred" (for
//     every constant of the result in pre-order, whether it has a DeferredExpr), "warns".
//     The conditions are the "cprop" conditions (comparisons, arithmetic, AND / OR / NOT,
//     IS NULL, <=>, constant TRUE / FALSE / NULL items) over the same columns, plus
//     deferred constants (["defer", recipe(, stale datum)]: a Constant whose DeferredExpr is
//     the recipe, a function of constants), arithmetic with a NULL constant, INTERVAL /
//     COALESCE over a NULL argument and literal NULLs under AND / OR. With nullReject, a column
//     of S becomes a NULL constant and every function over it and other columns is folded
//     with the other columns replaced by 1 (the "dummy" NewFunctionBase call of
//     foldConstant); AND / OR replace a NULL that came from S by 1 / 0 when another
//     argument is not a constant.
//
// Go changes nodes of the input condition in place while it rebuilds the functions over them:
// PropagateType (ETReal) on a DECIMAL constant shared by the input comparison and the rebuilt
// one (the same change as in "cprop", D08.2 spec §4 item 7). The generator snapshots the
// input like "cprop" and marks such records "known_deviation"; the replay counts them with
// the "cprop" records.

import (
	"encoding/json"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
)

func init() {
	castCoverage["evnull"] = vec.Coverage{Min: 1500, GoFuncs: []string{
		// expression.go (D08.1)
		"EvaluateExprWithNull", "evaluateExprWithNull", "evaluateExprWithNullInNullRejectCheck",
		// constant_fold.go (D08.2): the null-reject branch
		"FoldConstant", "foldConstant",
	}}
}

// evDeferred: ["defer", recipe] -> a Constant whose value is the recipe's (evaluated now)
// and whose DeferredExpr is the recipe; ["defer", recipe, datum]: the same with a stale
// value (what the recipe gave when the constant was built), which folding replaces.
func evDeferred(ctx *exprstatic.ExprContext, a []json.RawMessage, fts []*types.FieldType) expression.Expression {
	e := evExpr(ctx, a[1], fts)
	v, err := e.Eval(ctx.GetEvalCtx(), chunk.Row{})
	if err != nil {
		panic(err)
	}
	if len(a) > 2 {
		v = d2DatumIn(exArr(a[2]))
	}
	return &expression.Constant{Value: v, RetType: e.GetType(ctx.GetEvalCtx()), DeferredExpr: e}
}

// evNullDeferred: for every constant of e in pre-order, whether it has a DeferredExpr.
func evNullDeferred(e expression.Expression) []bool {
	out := []bool{}
	cpWalk(e, func(x expression.Expression) {
		if c, ok := x.(*expression.Constant); ok {
			out = append(out, c.DeferredExpr != nil)
		}
	})
	return out
}

func castExecD082d(op string, p []json.RawMessage) any {
	if op != "evnull" {
		return nil
	}
	ctx, ev := castCtx(p[1])
	fts := []*types.FieldType{}
	for _, f := range exArr(p[2]) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	m := map[string]any{}
	var cond expression.Expression
	ok := false
	m["args"] = exTry(func() any {
		cond = evExpr(ctx, p[0], fts)
		ev.TruncateWarnings(0)
		ok = true
		return nil
	})
	if !ok {
		return m
	}
	snap := map[expression.Expression]string{}
	cpWalk(cond, func(e expression.Expression) { snap[e] = cpDumpString(e) })
	defer func() {
		for e, d := range snap {
			if cpDumpString(e) != d {
				m["known_deviation"] = "in-place"
				return
			}
		}
	}()
	var cols []*expression.Column
	for _, i := range exArr(p[3]) {
		var n int
		must(json.Unmarshal(i, &n))
		cols = append(cols, &expression.Column{UniqueID: int64(n + 1), Index: n, RetType: fts[n], OrigName: "c"})
	}
	schema := expression.NewSchema(cols...)
	var bctx exprctx.BuildContext = ctx
	if arg[bool](p, 4) {
		bctx = exprctx.WithNullRejectCheck(ctx)
	}
	m["res"] = exTry(func() any {
		res, err := expression.EvaluateExprWithNull(bctx, schema, cond)
		if err != nil {
			return ecErr(err)
		}
		return map[string]any{"e": sfDump(res), "deferred": evNullDeferred(res)}
	})
	m["warns"] = ecWarns(ev.TruncateWarnings(0))
	return m
}

// ---------------------------------------------------------------- generation

// evNullCond: a "cprop" condition, sometimes with a deferred constant or a NULL operand.
func (g *castGen) evNullCond(fts []*types.FieldType, gens []func() []any, depth int) any {
	col := func() any { return []any{"col", g.pick(len(fts))} }
	intFt := castFt(mysql.TypeLonglong, 0, 20, 0)
	null := func() any {
		return []any{"const", []any{types.KindNull}, sfFtIn(types.NewFieldType(mysql.TypeNull))}
	}
	small := func() any { return []any{"const", []any{types.KindInt64, int64(g.pick(7) - 3)}, sfFtIn(intFt)} }
	switch k := g.pick(14); {
	case k < 2:
		// a deferred constant over a + b (small integers: the recipe is evaluated when
		// built, an overflow would make the record's building panic), half of the time
		// with a stale value; compared with a column (the comparison refines it) or added
		// to one (kept as an argument)
		var def any = []any{"defer", []any{"fn", ast.Plus, []any{small(), small()}}}
		if g.pick(2) == 0 {
			def = []any{"defer", []any{"fn", ast.Plus, []any{small(), small()}},
				[]any{types.KindInt64, int64(100 + g.pick(5))}}
		}
		op := []string{ast.EQ, ast.LT, ast.GE, ast.NullEQ}[g.pick(4)]
		switch {
		case depth == 0 && g.pick(3) == 0:
			// the deferred constant itself (building a function over it replaces it by
			// a plain constant: only here does EvaluateExprWithNull see it)
			return def
		case k == 0:
			return []any{"fn", op, []any{col(), def}}
		}
		return []any{"fn", op, []any{[]any{"fn", ast.Plus, []any{col(), def}}, small()}}
	case k < 3:
		// (col + NULL) <op> const, (col <op> NULL)
		if g.pick(2) == 0 {
			return []any{"fn", ast.EQ, []any{[]any{"fn", ast.Plus, []any{col(), null()}}, g.arConst()}}
		}
		return []any{"fn", []string{ast.LT, ast.EQ, ast.NE}[g.pick(3)], []any{col(), null()}}
	case k < 4:
		// functions that are not NULL over a NULL argument: INTERVAL(n, col, NULL) (with the
		// column replaced by 1, 0 for n < 1: the null-reject fold's "false" result),
		// INTERVAL(n, NULL, col), COALESCE(NULL, col)
		if g.pick(2) == 0 {
			args := []any{small(), col(), null()}
			if g.pick(3) == 0 {
				args = []any{small(), null(), col()}
			}
			if g.pick(2) == 0 {
				args = append(args, col())
			}
			return []any{"fn", ast.Interval, args}
		}
		return []any{"fn", ast.Coalesce, []any{null(), col()}}
	case k < 5 && depth < 2:
		// a literal NULL (not from the schema) or a constant TRUE / FALSE under AND / OR
		// (whether the other argument is a constant decides if a NULL from the schema is
		// replaced)
		op := []string{ast.LogicAnd, ast.LogicOr}[g.pick(2)]
		c := null()
		if g.pick(2) == 0 {
			c = evNeutral(op)
		}
		args := []any{c, g.evNullCond(fts, gens, depth+1)}
		if g.pick(2) == 0 {
			args[0], args[1] = args[1], args[0]
		}
		return []any{"fn", op, args}
	case k < 6 && depth < 2:
		op := []string{ast.LogicXor, ast.LogicAnd, ast.LogicOr}[g.pick(3)]
		return []any{"fn", op, []any{g.evNullCond(fts, gens, depth+1), g.evNullCond(fts, gens, depth+1)}}
	case k < 7:
		// col1 + col2 > const: two columns, one of which may be in the schema
		return []any{"fn", ast.GT, []any{[]any{"fn", ast.Plus, []any{col(), col()}}, []any{"const",
			[]any{types.KindInt64, int64(g.pick(5))}, sfFtIn(intFt)}}}
	default:
		return g.cpCond(fts, gens, depth)
	}
}

// evNeutral: the constant that does not decide AND (1) / OR (0): with it, the result
// depends on whether a NULL from the schema is replaced.
func evNeutral(op string) any {
	v := int64(1)
	if op == ast.LogicOr {
		v = 0
	}
	return []any{"const", []any{types.KindInt64, v}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}
}

func castGenerateD082d(vg *vec.Gen, g *castGen) {
	for i := 0; i < 2000; i++ {
		fts, gens := g.cpCols(1 + g.pick(4))
		cols := []any{}
		for _, ft := range fts {
			cols = append(cols, sfFtIn(ft))
		}
		schema := []any{}
		for c := range fts {
			if g.pick(2) == 0 {
				schema = append(schema, c)
			}
		}
		cond := g.evNullCond(fts, gens, 0)
		switch g.pick(6) {
		case 0, 1:
			op := []string{ast.LogicAnd, ast.LogicOr}[g.pick(2)]
			cond = []any{"fn", op, []any{cond, g.evNullCond(fts, gens, 0)}}
		case 2:
			op := []string{ast.LogicAnd, ast.LogicOr}[g.pick(2)]
			cond = []any{"fn", op, []any{cond, evNeutral(op)}}
		}
		vg.Add("sys", "evnull", cond, g.timeCtx(), cols, schema, g.pick(3) != 0)
	}
}
