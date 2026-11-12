package domains

// Domain "cast", D08.12 batch 1 (expression tree utilities of util.go; appended after
// D08.5 batch 3, earlier records are unchanged):
//   - "util": [kind, exprs, ctx, cols, extra] over expression recipes built from the ported
//     function families (comparisons, logic, arithmetic, IS NULL, CAST), columns, correlated
//     columns (["corcol", i], UniqueID 101+i) and virtual columns (["vcol", i, recipe]).
//     kind "x": the extraction / predicate utilities over the list; "pnot": PushDownNot,
//     EliminateNoPrecisionLossCast, GetExprInsideIsTruth, ContainOuterNot per item; "dnf":
//     extractFiltersFromDNF per OR item (extracted items sorted: Go ranges over a map),
//     DeriveRelaxedFiltersFromDNF per item over the schema of the first extra columns,
//     ExtractFiltersFromDNFs over the list (compared as a sorted multiset); "const": the
//     Get*FromConstant helpers per item; "fmt": GetFormatBytes / GetFormatNanoTime over
//     extra (float64 bits).
//     Records where Go changed a node of the input in place (refineArgs while PushDownNot
//     rebuilds a comparison) are marked "known_deviation" as for "cprop".

import (
	"encoding/json"
	"math"
	"sort"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

//go:linkname utExtractFiltersFromDNF github.com/pingcap/tidb/pkg/expression.extractFiltersFromDNF
func utExtractFiltersFromDNF(ctx expression.BuildContext, dnfFunc *expression.ScalarFunction) ([]expression.Expression, expression.Expression)

func utGoFuncs() []string {
	return []string{
		// util.go (D08.12)
		"Filter", "FilterOutInPlace", "ExtractDependentColumns", "extractDependentColumns",
		"ExtractColumns", "extractColumns", "ExtractAllColumnsFromExpressionsInUsedSlices", "extractColumnsSlices",
		"ExtractCorColumns", "ExtractColumnsFromExpressions", "ExtractEquivalenceColumns", "FindUpperBound",
		"extractEquivalenceColumns", "extractColumnsAndCorColumns", "ExtractConstantEqColumnsOrScalar",
		"extractConstantEqColumnsOrScalar", "ExtractColumnsAndCorColumnsFromExpressions", "ExtractColumnSet",
		"extractColumnSet", "ColumnSubstituteAll", "pushNotAcrossArgs", "noPrecisionLossCastCompatible", "unwrapCast",
		"eliminateCastFunction", "pushNotAcrossExpr", "GetExprInsideIsTruth", "PushDownNot",
		"EliminateNoPrecisionLossCast", "ContainOuterNot", "containOuterNot", "Contains", "ExtractFiltersFromDNFs",
		"extractFiltersFromDNF", "DeriveRelaxedFiltersFromDNF", "GetRowLen", "CheckArgsNotMultiColumnRow",
		"GetFuncArg", "DatumToConstant", "GetStringFromConstant", "GetIntFromConstant", "IsRuntimeConstExpr",
		"CheckNonDeterministic", "CheckFuncInExpr", "IsImmutableFunc", "GetUint64FromConstant",
		"ContainVirtualColumn", "ContainCorrelatedColumn", "jsonUnquoteFunctionBenefitsFromPushedDown",
		"ProjectionBenefitsFromPushedDown", "MaybeOverOptimized4PlanCacheForMultiExpression", "GetFormatBytes",
		"GetFormatNanoTime", "ExprsToStringsForDisplay", "ExprsHasSideEffects", "ExprHasSetVarOrSleep",
		// expression.go (D08.1)
		"extractBinaryOpItems", "FlattenDNFConditions", "FlattenCNFConditions",
	}
}

func init() {
	castCoverage["util"] = vec.Coverage{Min: 2000, GoFuncs: utGoFuncs()}
}

// D08.12 functions ported with earlier blocks and exercised by their records.
func utEarlierCovered() map[string][]string {
	return map[string][]string{
		"cprop": {"ConstExprConsiderPlanCache", "ColumnSubstitute", "ColumnSubstituteImpl", "cowExprRef.Set",
			"cowExprRef.Result", "checkCollationStrictness", "SetExprColumnInOperand", "MaybeOverOptimized4PlanCache",
			"containMutableConst", "RemoveDupExprs", "IsMutableEffectsExpr", "BuildNotNullExpr"},
		"cmpbuild": {"RemoveMutableConst", "DisableParseJSONFlag4Expr"},
	}
}

func utFtKey(ft *types.FieldType) string {
	b, err := json.Marshal(sfFtIn(ft))
	must(err)
	return string(b)
}

func utDump(e expression.Expression) any {
	if e == nil {
		return nil
	}
	return sfDump(e)
}

func utCols(cs []*expression.Column) any {
	out := []any{}
	for _, c := range cs {
		out = append(out, sfDump(c))
	}
	return out
}

func utSorted(es []expression.Expression) any {
	ss := []string{}
	for _, e := range es {
		b, err := json.Marshal(utDump(e))
		must(err)
		ss = append(ss, string(b))
	}
	sort.Strings(ss)
	out := []any{}
	for _, s := range ss {
		out = append(out, json.RawMessage(s))
	}
	return out
}

func castExecD0812(op string, p []json.RawMessage) any {
	if op != "util" {
		return nil
	}
	kind := arg[string](p, 0)
	ctx, ev := castCtx(p[2])
	fts := []*types.FieldType{}
	for _, f := range exArr(p[3]) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	m := map[string]any{}
	ftsBefore := []string{}
	for _, ft := range fts {
		ftsBefore = append(ftsBefore, utFtKey(ft))
	}
	var es []expression.Expression
	ok := false
	m["args"] = exTry(func() any {
		es = evExprs(ctx, p[1], fts)
		ev.TruncateWarnings(0)
		ok = true
		return nil
	})
	if !ok {
		return m
	}
	snap := map[expression.Expression]string{}
	for _, c := range es {
		cpWalk(c, func(e expression.Expression) { snap[e] = cpDumpString(e) })
	}
	// The column recipes share one *FieldType per column index (as columns share the
	// infoschema's); Go changes some of them in place while building or rewriting a function
	// (e.g. the ParseToJSONFlag of a JSON CAST), which every column of that index then sees.
	// tidb-rs copies; such records are marked as for "cprop" (D08.12 spec §4).
	defer func() {
		for i, ft := range fts {
			if utFtKey(ft) != ftsBefore[i] {
				m["known_deviation"] = "in-place"
				return
			}
		}
		for e, d := range snap {
			if cpDumpString(e) != d {
				m["known_deviation"] = "in-place"
				return
			}
		}
	}()
	switch kind {
	case "x":
		utExecX(m, ctx, ev, es, p)
	case "pnot":
		res := []any{}
		for _, e := range es {
			res = append(res, map[string]any{
				"pnot":   exTry(func() any { return utDump(expression.PushDownNot(ctx, e)) }),
				"elim":   exTry(func() any { return utDump(expression.EliminateNoPrecisionLossCast(ctx, e)) }),
				"truth":  utDump(expression.GetExprInsideIsTruth(e)),
				"outnot": expression.ContainOuterNot(e),
			})
		}
		m["res"] = res
	case "dnf":
		nSchema := arg[int](p, 4)
		cols := []*expression.Column{}
		for i := 0; i < nSchema && i < len(fts); i++ {
			cols = append(cols, &expression.Column{UniqueID: int64(i + 1), Index: i, RetType: fts[i], OrigName: "c"})
		}
		schema := expression.NewSchema(cols...)
		res := []any{}
		for _, e := range es {
			r := map[string]any{
				"relax": exTry(func() any { return utDump(expression.DeriveRelaxedFiltersFromDNF(ctx, e, schema)) }),
			}
			if sf, ok := e.(*expression.ScalarFunction); ok && sf.FuncName.L == ast.LogicOr {
				r["dnf"] = exTry(func() any {
					ex, rem := utExtractFiltersFromDNF(ctx, sf)
					return []any{utSorted(ex), utDump(rem)}
				})
			}
			res = append(res, r)
		}
		m["res"] = res
		m["all"] = exTry(func() any {
			return utSorted(expression.ExtractFiltersFromDNFs(ctx, append([]expression.Expression{}, es...)))
		})
	case "const":
		res := []any{}
		for _, e := range es {
			r := map[string]any{}
			r["str"] = exTry(func() any {
				s, isNull, err := expression.GetStringFromConstant(ev, e)
				if err != nil {
					return map[string]any{"err": err.Error()}
				}
				return []any{vec.GoString(s), isNull}
			})
			r["int"] = exTry(func() any {
				v, isNull, err := expression.GetIntFromConstant(ev, e)
				if err != nil {
					return map[string]any{"err": err.Error()}
				}
				return []any{v, isNull}
			})
			r["u64"] = exTry(func() any {
				v, isNull, ok := expression.GetUint64FromConstant(ev, e)
				return []any{v, isNull, ok}
			})
			if c, ok := e.(*expression.Constant); ok {
				r["d2c"] = sfDump(expression.DatumToConstant(c.Value, c.RetType.GetType(), c.RetType.GetFlag()))
			}
			res = append(res, r)
		}
		m["res"] = res
	case "fmt":
		res := []any{}
		for _, raw := range exArr(p[4]) {
			var bits uint64
			must(json.Unmarshal(raw, &bits))
			f := math.Float64frombits(bits)
			res = append(res, []any{expression.GetFormatBytes(f), expression.GetFormatNanoTime(f)})
		}
		m["res"] = res
	default:
		panic("util kind")
	}
	m["warns"] = ecWarns(ev.TruncateWarnings(0))
	return m
}

func utExecX(m map[string]any, ctx expression.BuildContext, ev expression.EvalContext, es []expression.Expression, p []json.RawMessage) {
	even := func(c *expression.Column) bool { return c.UniqueID%2 == 0 }
	isSF := func(e expression.Expression) bool { _, ok := e.(*expression.ScalarFunction); return ok }
	isCol := func(e expression.Expression) bool { _, ok := e.(*expression.Column); return ok }
	per := []any{}
	for _, e := range es {
		col, ub := expression.FindUpperBound(e)
		var colOut any
		if col != nil {
			colOut = sfDump(col)
		}
		cors := []any{}
		for _, c := range expression.ExtractCorColumns(e) {
			cors = append(cors, sfDump(c))
		}
		per = append(per, map[string]any{
			"cols":   utCols(expression.ExtractColumns(e)),
			"dep":    utCols(expression.ExtractDependentColumns(e)),
			"cor":    cors,
			"ub":     []any{colOut, ub},
			"rtc":    expression.IsRuntimeConstExpr(e),
			"nondet": expression.CheckNonDeterministic(e),
			"inplus": expression.CheckFuncInExpr(e, ast.Plus),
			"incast": expression.CheckFuncInExpr(e, ast.Cast),
			"immut":  expression.IsImmutableFunc(e),
			"mut":    expression.IsMutableEffectsExpr(e),
			"sv":     expression.ExprHasSetVarOrSleep(e),
			"rowlen": expression.GetRowLen(e),
			"arg0":   exTry(func() any { return utDump(expression.GetFuncArg(e, 0)) }),
			"cpc":    []any{expression.ConstExprConsiderPlanCache(e, false), expression.ConstExprConsiderPlanCache(e, true)},
		})
	}
	m["per"] = per
	all := map[string]any{}
	all["filter"] = utDumpList(expression.Filter(nil, es, isSF))
	rem, out := expression.FilterOutInPlace(append([]expression.Expression{}, es...), isCol)
	all["fout"] = []any{utDumpList(rem), utDumpList(out)}
	// Only the UniqueIDs: columns of one UniqueID may differ (a function's argument copy with a
	// changed type, e.g. PropagateType), and which of them survives slices.SortFunc (not
	// stable) + CompactFunc is unspecified (D08.12 spec §4).
	all["used"] = utIDs(expression.ExtractAllColumnsFromExpressionsInUsedSlices(nil, nil, es...))
	all["usedeven"] = utIDs(expression.ExtractAllColumnsFromExpressionsInUsedSlices(nil, even, es...))
	all["fromexprs"] = utCols(expression.ExtractColumnsFromExpressions(nil, es, nil))
	all["fromeven"] = utCols(expression.ExtractColumnsFromExpressions(nil, es, even))
	eq := []any{}
	for _, pr := range expression.ExtractEquivalenceColumns(nil, es) {
		eq = append(eq, utDumpList(pr))
	}
	all["eqv"] = eq
	all["colcor"] = utCols(expression.ExtractColumnsAndCorColumnsFromExpressions(nil, es))
	all["consteq"] = exTry(func() any { return utDumpList(expression.ExtractConstantEqColumnsOrScalar(ctx, nil, es)) })
	set := expression.ExtractColumnSet(es...)
	sa := []int{}
	all["set"] = append(sa, set.SortedArray()...)
	if len(es) > 0 {
		all["contains"] = []any{expression.Contains(ev, es[:len(es)-1], es[len(es)-1]), expression.Contains(ev, es, es[0])}
	}
	all["vcol"] = expression.ContainVirtualColumn(es)
	all["corcol"] = expression.ContainCorrelatedColumn(es)
	all["proj"] = []any{expression.ProjectionBenefitsFromPushedDown(es, len(es)), expression.ProjectionBenefitsFromPushedDown(es, len(es)+1)}
	all["side"] = expression.ExprsHasSideEffects(es)
	all["multirow"] = exTry(func() any {
		if err := expression.CheckArgsNotMultiColumnRow(es...); err != nil {
			return map[string]any{"err": err.Error()}
		}
		return nil
	})
	all["disp"] = expression.ExprsToStringsForDisplay(ev, es)
	all["dedup"] = utDumpList(expression.RemoveDupExprs(append([]expression.Expression{}, es...)))
	all["mo"] = expression.MaybeOverOptimized4PlanCacheForMultiExpression(ctx, es...)
	nSchema := arg[int](p, 4)
	if nSchema > 0 && len(es) > 0 {
		cols := []*expression.Column{}
		news := []expression.Expression{}
		for i := 0; i < nSchema; i++ {
			cols = append(cols, &expression.Column{UniqueID: int64(i + 1), Index: i, RetType: exprsFt(p, i), OrigName: "c"})
			news = append(news, es[i%len(es)])
		}
		all["csall"] = exTry(func() any {
			fail, r := expression.ColumnSubstituteAll(ctx, es[0], expression.NewSchema(cols...), news)
			return []any{fail, utDump(r)}
		})
	}
	m["all"] = all
}

func exprsFt(p []json.RawMessage, i int) *types.FieldType {
	return d2FtIn(exArr(exArr(p[3])[i]))
}

func utIDs(cs []*expression.Column) any {
	out := []any{}
	for _, c := range cs {
		out = append(out, c.UniqueID)
	}
	return out
}

func utDumpList(es []expression.Expression) any {
	out := []any{}
	for _, e := range es {
		out = append(out, utDump(e))
	}
	return out
}

// ---------------------------------------------------------------- generation

// utLeaf: a column, a correlated column, a virtual column or a constant.
func (g *castGen) utLeaf(fts []*types.FieldType, gens []func() []any) any {
	i := g.pick(len(fts))
	switch g.pick(10) {
	case 0:
		return []any{"corcol", i}
	case 1:
		// one virtual expression per UniqueID, so that columns of one UniqueID are equal
		// (ExtractAllColumnsFromExpressionsInUsedSlices keeps one of them after an unstable
		// sort)
		return []any{"vcol", i, []any{"fn", ast.IsNull, []any{[]any{"col", i}}}}
	case 2, 3:
		return g.cpConstFor(fts, gens, i)
	default:
		return []any{"col", i}
	}
}

// utCast: CAST(column AS target) where target is a VARCHAR / integer type that may or may
// not lose precision (EliminateNoPrecisionLossCast).
func (g *castGen) utCast(fts []*types.FieldType, i int) any {
	src := fts[i]
	var ft *types.FieldType
	if mysql.IsIntegerType(src.GetType()) {
		tp := []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeLong, mysql.TypeLonglong}[g.pick(4)]
		var flag uint
		if g.pick(2) == 0 {
			flag |= mysql.UnsignedFlag
		}
		ft = castFt(tp, flag, []int{4, 11, 20}[g.pick(3)], 0)
	} else {
		ft = castFt(mysql.TypeVarchar, 0, []int{1, 10, 64, 255}[g.pick(4)], 0)
		cs := [][2]string{{"utf8mb4", "utf8mb4_bin"}, {"utf8mb4", "utf8mb4_general_ci"}, {"utf8", "utf8_general_ci"}, {"binary", "binary"}}[g.pick(4)]
		ft.SetCharset(cs[0])
		ft.SetCollate(cs[1])
	}
	return []any{"cast", sfFtIn(ft), []any{"col", i}}
}

// utCols: integer / VARCHAR columns (so that CASTs can be eliminated) and the arithmetic
// columns of D08.5.
func (g *castGen) utCols(n int) ([]*types.FieldType, []func() []any) {
	fts, gens := g.cpCols(n)
	for i := range fts {
		if g.pick(3) == 0 {
			flen := []int{10, 64, 255}[g.pick(3)]
			ft := castFt(mysql.TypeVarchar, 0, flen, 0)
			cs := [][2]string{{"utf8mb4", "utf8mb4_bin"}, {"utf8mb4", "utf8mb4_general_ci"}, {"utf8", "utf8_general_ci"}}[g.pick(3)]
			ft.SetCharset(cs[0])
			ft.SetCollate(cs[1])
			fts[i] = ft
			gens[i] = func() []any {
				return []any{types.KindString, vec.GoString([]string{"a", "abc", "1"}[g.pick(3)]), cs[1]}
			}
		}
	}
	return fts, gens
}

// utExpr: a condition / expression over the leaves.
func (g *castGen) utExpr(fts []*types.FieldType, gens []func() []any, depth int) any {
	switch k := g.pick(14); {
	case k < 2 || depth > 2:
		return g.utLeaf(fts, gens)
	case k < 5:
		op := []string{ast.LT, ast.LE, ast.GT, ast.GE, ast.EQ, ast.NE, ast.NullEQ}[g.pick(7)]
		var a any = []any{"col", g.pick(len(fts))}
		if g.pick(2) == 0 {
			a = g.utCast(fts, g.pick(len(fts)))
		}
		b := g.utLeaf(fts, gens)
		if g.pick(3) == 0 {
			a, b = b, a
		}
		return []any{"fn", op, []any{a, b}}
	case k < 8:
		op := []string{ast.LogicAnd, ast.LogicOr, ast.LogicOr}[g.pick(3)]
		return []any{"fn", op, []any{g.utExpr(fts, gens, depth+1), g.utExpr(fts, gens, depth+1)}}
	case k < 10:
		return []any{"fn", ast.UnaryNot, []any{g.utExpr(fts, gens, depth+1)}}
	case k < 11:
		op := []string{ast.IsNull, ast.IsTruthWithNull, ast.IsTruthWithoutNull, ast.IsFalsity}[g.pick(4)]
		return []any{"fn", op, []any{g.utExpr(fts, gens, depth+1)}}
	case k < 13:
		ar := []string{ast.Plus, ast.Minus, ast.Mul}[g.pick(3)]
		return []any{"fn", ar, []any{g.utLeaf(fts, gens), g.utLeaf(fts, gens)}}
	default:
		return g.cpCond(fts, gens, depth)
	}
}

// utDNF: an OR of ANDs that share some items (extractFiltersFromDNF).
func (g *castGen) utDNF(fts []*types.FieldType, gens []func() []any) any {
	shared := []any{}
	for n := g.pick(3); n > 0; n-- {
		shared = append(shared, g.cpCond(fts, gens, 1))
	}
	var or any
	for n := 2 + g.pick(3); n > 0; n-- {
		items := append([]any{}, shared...)
		if len(shared) > 0 && g.pick(3) == 0 {
			// a repeated item inside one DNF item counts once (extractFiltersFromDNF)
			items = append(items, shared[g.pick(len(shared))])
		}
		for k := g.pick(3); k > 0; k-- {
			items = append(items, g.cpCond(fts, gens, 1))
		}
		if len(items) == 0 {
			items = append(items, g.cpCond(fts, gens, 1))
		}
		g.rng.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
		and := items[0]
		for _, it := range items[1:] {
			and = []any{"fn", ast.LogicAnd, []any{and, it}}
		}
		if or == nil {
			or = and
		} else {
			or = []any{"fn", ast.LogicOr, []any{or, and}}
		}
	}
	return or
}

func castGenerateD0812(vg *vec.Gen, g *castGen) {
	cols := func(fts []*types.FieldType) []any {
		out := []any{}
		for _, ft := range fts {
			out = append(out, sfFtIn(ft))
		}
		return out
	}
	for i := 0; i < 800; i++ {
		fts, gens := g.utCols(1 + g.pick(4))
		es := []any{}
		for n := 1 + g.pick(5); n > 0; n-- {
			es = append(es, g.utExpr(fts, gens, 0))
		}
		if g.pick(4) == 0 && len(es) > 1 {
			es = append(es, es[g.pick(len(es))])
		}
		vg.Add("x", "util", "x", es, g.timeCtx(), cols(fts), g.pick(len(fts)+1))
	}
	for i := 0; i < 600; i++ {
		fts, gens := g.utCols(1 + g.pick(4))
		es := []any{}
		for n := 1 + g.pick(3); n > 0; n-- {
			es = append(es, g.utExpr(fts, gens, 0))
		}
		vg.Add("pnot", "util", "pnot", es, g.timeCtx(), cols(fts), 0)
	}
	for i := 0; i < 600; i++ {
		fts, gens := g.utCols(2 + g.pick(4))
		es := []any{}
		for n := 1 + g.pick(3); n > 0; n-- {
			if g.pick(4) == 0 {
				es = append(es, g.cpCond(fts, gens, 0))
			} else {
				es = append(es, g.utDNF(fts, gens))
			}
		}
		vg.Add("dnf", "util", "dnf", es, g.timeCtx(), cols(fts), 1+g.pick(len(fts)))
	}
	for i := 0; i < 300; i++ {
		fts, gens := g.utCols(1 + g.pick(2))
		es := []any{}
		for n := 1 + g.pick(4); n > 0; n-- {
			if g.pick(5) == 0 {
				es = append(es, g.utLeaf(fts, gens))
			} else {
				es = append(es, g.arConst())
			}
		}
		vg.Add("const", "util", "const", es, g.timeCtx(), cols(fts), 0)
	}
	// FindUpperBound: column < / <= an integer constant (and other shapes)
	for i := 0; i < 200; i++ {
		fts, gens := g.utCols(1 + g.pick(3))
		es := []any{}
		for n := 1 + g.pick(3); n > 0; n-- {
			c := []any{"col", g.pick(len(fts))}
			v := []int64{0, 1, -1, 100, math.MaxInt64, math.MinInt64}[g.pick(6)]
			k := castFt(mysql.TypeLonglong, mysql.NotNullFlag, 20, 0)
			con := []any{"const", []any{types.KindInt64, v}, sfFtIn(k)}
			op := []string{ast.LT, ast.LE, ast.GT}[g.pick(3)]
			if g.pick(4) == 0 {
				es = append(es, []any{"fn", op, []any{con, c}})
			} else {
				es = append(es, []any{"fn", op, []any{c, con}})
			}
		}
		_ = gens
		vg.Add("ub", "util", "x", es, g.timeCtx(), cols(fts), 0)
	}
	// EliminateNoPrecisionLossCast: CAST(integer / VARCHAR column AS integer / VARCHAR) compared
	// with a constant, in AND / OR, with the column's own type, wider, narrower, other
	// signedness and collations.
	intTps := []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong}
	colls := [][2]string{{"utf8mb4", "utf8mb4_bin"}, {"utf8mb4", "utf8mb4_general_ci"}, {"utf8", "utf8_general_ci"}, {"latin1", "latin1_bin"}}
	for i := 0; i < 300; i++ {
		var src, dst *types.FieldType
		var con any
		if g.pick(2) == 0 {
			var f1, f2 uint
			if g.pick(2) == 0 {
				f1 = mysql.UnsignedFlag
			}
			if g.pick(3) == 0 {
				f2 = f1 ^ mysql.UnsignedFlag
			} else {
				f2 = f1
			}
			src = castFt(intTps[g.pick(len(intTps))], f1, []int{4, 11, 20}[g.pick(3)], 0)
			dst = castFt(intTps[g.pick(len(intTps))], f2, []int{4, 11, 20}[g.pick(3)], 0)
			con = []any{"const", []any{types.KindInt64, int64(g.pick(300) - 100)}, sfFtIn(castFt(mysql.TypeLonglong, mysql.NotNullFlag, 20, 0))}
		} else {
			c1, c2 := colls[g.pick(len(colls))], colls[g.pick(len(colls))]
			src = castFt(mysql.TypeVarchar, 0, []int{10, 64}[g.pick(2)], 0)
			src.SetCharset(c1[0])
			src.SetCollate(c1[1])
			dst = castFt([]byte{mysql.TypeVarchar, mysql.TypeString}[g.pick(2)], 0, []int{5, 10, 64, 255}[g.pick(4)], 0)
			dst.SetCharset(c2[0])
			dst.SetCollate(c2[1])
			c3 := colls[g.pick(len(colls))]
			con = []any{"const", []any{types.KindString, vec.GoString([]string{"a", "abc"}[g.pick(2)]), c3[1]},
				sfFtIn(func() *types.FieldType {
					ft := castFt(mysql.TypeVarchar, 0, 10, 0)
					ft.SetCharset(c3[0])
					ft.SetCollate(c3[1])
					return ft
				}())}
		}
		fts := []*types.FieldType{src}
		var cast any = []any{"cast", sfFtIn(dst), []any{"col", 0}}
		op := []string{ast.EQ, ast.NullEQ, ast.LT, ast.GE, ast.NE}[g.pick(5)]
		var e any = []any{"fn", op, []any{cast, con}}
		if g.pick(2) == 0 {
			e = []any{"fn", op, []any{con, cast}}
		}
		if g.pick(3) == 0 {
			lop := []string{ast.LogicAnd, ast.LogicOr}[g.pick(2)]
			e = []any{"fn", lop, []any{e, []any{"fn", ast.EQ, []any{cast, con}}}}
		}
		vg.Add("elim", "util", "pnot", []any{e}, g.timeCtx(), cols(fts), 0)
	}
	// extractFiltersFromDNF counts an item once per DNF item: (X and Y) or (X and X) or Z
	// must not extract X (X appears twice in one DNF item and not in another).
	for i := 0; i < 100; i++ {
		fts, gens := g.utCols(2 + g.pick(3))
		x := g.cpCond(fts, gens, 1)
		y := g.cpCond(fts, gens, 1)
		z := g.cpCond(fts, gens, 1)
		and := func(a, b any) any { return []any{"fn", ast.LogicAnd, []any{a, b}} }
		or := func(a, b any) any { return []any{"fn", ast.LogicOr, []any{a, b}} }
		items := []any{and(x, y), and(x, x), z}
		if g.pick(2) == 0 {
			items[2] = and(z, y)
		}
		vg.Add("dnfdup", "util", "dnf", []any{or(or(items[0], items[1]), items[2])}, g.timeCtx(), cols(fts), 1+g.pick(len(fts)))
	}
	fl := []float64{0, 1, -1, 1023, 1024, 1536, 1 << 20, 1 << 30, 1 << 40, 1 << 50, 1 << 60, 1e15, 1e18, 1e21,
		999, 1000, 1e6, 6e10, 3.6e12, 8.64e13, 8.64e18, -2.5e9, 0.5, math.Inf(1), math.Inf(-1), math.NaN(),
		99999.999 * (1 << 30), 1e300, 5e-324}
	for i := 0; i < 100; i++ {
		xs := []any{}
		for n := 1 + g.pick(6); n > 0; n-- {
			var f float64
			if g.pick(2) == 0 {
				f = fl[g.pick(len(fl))]
			} else {
				f = math.Ldexp(g.rng.Float64(), g.pick(140)-20)
				if g.pick(3) == 0 {
					f = -f
				}
			}
			xs = append(xs, math.Float64bits(f))
		}
		vg.Add("fmt", "util", "fmt", []any{}, g.timeCtx(), []any{}, xs)
	}
}
