package domains

// Domain "cast", part 2b-3c = D08.1 part 2b (appended after part 2b-3a; earlier records are
// unchanged): the evaluation entry points over a chunk, with vectorized evaluation on and
// off, an optional Sel(), NULLs, and the warnings/errors of the CAST signatures ported so
// far (including the non-vectorized VECTOR -> CHAR signature, which takes the row-based
// fallback).
//
// The input chunk has 1-3 columns ("cols": d2FtIn types, "rows": rows of d2DatumIn values,
// "sel": nil or the selected row indexes). An expression recipe is ["col", i] (column i),
// ["const", datumIn, ftIn] or ["cast", ftIn, recipe] (BuildCastFunctionWithCheck).
//   - "evsuite": [exprs, avoidColumnEvaluator, vecEnabled, ctx, cols, rows, sel] ->
//     NewEvaluatorSuite + Run into a chunk of the expressions' types: vectorizable, the
//     output columns (each to its own length: swapped columns keep their physical rows),
//     the input column lengths afterwards, warnings, error.
//   - "evfilter": [filters, vecEnabled, considerNull, ctx, cols, rows, sel] ->
//     VectorizedFilter / VectorizedFilterConsiderNull (isNull nil or not): selected, isNull,
//     the input Sel() afterwards, warnings, error.
//   - "evbool": [exprs, vecEnabled, ctx, cols, rows, sel] -> VecEvalBool: selected, nulls.
//   - "evexpr": [expr, vecEnabled, evalType, ctx, cols, rows, sel] -> EvalExpr into a column.
//   - "evexec": [exprs, ctx, cols, rows, sel] -> VectorizedExecute.
// Values are dumped by the column's type: BIT as hex, ENUM/SET as [value, name], FLOAT as
// the float32 bits, then by evaluation type as the other cast ops do.
//
// Pooled-buffer warnings: VecEvalBool evaluates each filter into a column of the global
// column pool, and toBool converts a string row with StrToFloat over Column.GetString, which
// aliases the column's bytes (hack.String). The "Truncated incorrect DOUBLE value: '%s'"
// warning keeps that string and formats it lazily, so once the column goes back to the
// pool and is reused (by a later filter, statement or session) the warning shows other
// bytes. Its argument is therefore recorded as '<pooled>' on the paths through VecEvalBool
// ("evbool"; "evfilter" when all filters are vectorized and vectorization is on); tidb-rs
// copies the string (tidb-rs upstream-pitfall issue).

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
)

func init() {
	castCoverage["evsuite"] = vec.Coverage{Min: 1200, GoFuncs: []string{"NewEvaluatorSuite", "EvaluatorSuite.Run",
		"EvaluatorSuite.Vectorizable", "defaultEvaluator.run", "Vectorizable", "checkSequenceFunction",
		"HasGetSetVarFunc", "evalOneVec", "evalOneColumn", "evalOneCell", "executeToInt", "executeToReal",
		"executeToDecimal", "executeToDatetime", "executeToDuration", "executeToJSON", "executeToString",
		"executeToVectorFloat32",
		"EvaluatorSuite.RequiredOptionalEvalProps", "defaultEvaluator.RequiredOptionalEvalProps",
		"GetOptionalEvalPropsForExpr"}}
	castCoverage["evfilter"] = vec.Coverage{Min: 1200, GoFuncs: []string{"VectorizedFilter",
		"VectorizedFilterConsiderNull", "rowBasedFilter", "vectorizedFilter", "VecEvalBool", "toBool", "EvalExpr"}}
	castCoverage["evbool"] = vec.Coverage{Min: 600, GoFuncs: []string{"VecEvalBool", "toBool", "EvalExpr"}}
	castCoverage["evexpr"] = vec.Coverage{Min: 600, GoFuncs: []string{"EvalExpr"}}
	castCoverage["evexec"] = vec.Coverage{Min: 200, GoFuncs: []string{"VectorizedExecute"}}
}

// evInput: the input chunk.
func evInput(colsRaw, rowsRaw, selRaw json.RawMessage) ([]*types.FieldType, *chunk.Chunk) {
	fts := []*types.FieldType{}
	for _, f := range exArr(colsRaw) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	rows := exArr(rowsRaw)
	chk := chunk.NewChunkWithCapacity(fts, len(rows))
	for _, r := range rows {
		for c, d := range exArr(r) {
			dt := d2DatumIn(exArr(d))
			chk.AppendDatum(c, &dt)
		}
	}
	if string(selRaw) != "null" {
		var sel []int
		must(json.Unmarshal(selRaw, &sel))
		chk.SetSel(sel)
	}
	return fts, chk
}

// evExpr: an expression recipe.
func evExpr(ctx *exprstatic.ExprContext, raw json.RawMessage, fts []*types.FieldType) expression.Expression {
	a := exArr(raw)
	switch arg[string](a, 0) {
	case "col":
		i := arg[int](a, 1)
		return &expression.Column{UniqueID: int64(i + 1), Index: i, RetType: fts[i], OrigName: "c"}
	case "const":
		return &expression.Constant{Value: d2DatumIn(exArr(a[1])), RetType: d2FtIn(exArr(a[2]))}
	case "cast":
		e, err := expression.BuildCastFunctionWithCheck(ctx, evExpr(ctx, a[2], fts), d2FtIn(exArr(a[1])), false, false)
		if err != nil {
			panic(err)
		}
		return e
	case "corcol":
		// D08.12: ["corcol", i] -> a correlated column over column i (UniqueID 101+i).
		i := arg[int](a, 1)
		return &expression.CorrelatedColumn{Column: expression.Column{UniqueID: int64(101 + i), Index: i,
			RetType: fts[i], OrigName: "c"}, Data: new(types.Datum)}
	case "vcol":
		// D08.12: ["vcol", i, recipe] -> a virtual (generated) column of column i's type
		// (UniqueID 201+i).
		i := arg[int](a, 1)
		return &expression.Column{UniqueID: int64(201 + i), Index: i, RetType: fts[i], OrigName: "c",
			VirtualExpr: evExpr(ctx, a[2], fts)}
	case "fn":
		// D08.4 batch 1: ["fn", name, [recipes]] -> NewFunctionBase (no folding).
		args := evExprs(ctx, a[2], fts)
		e, err := expression.NewFunctionBase(ctx, arg[string](a, 1), types.NewFieldType(mysql.TypeUnspecified), args...)
		if err != nil {
			panic(err)
		}
		return e
	}
	panic("evExpr")
}

func evExprs(ctx *exprstatic.ExprContext, raw json.RawMessage, fts []*types.FieldType) []expression.Expression {
	out := []expression.Expression{}
	for _, r := range exArr(raw) {
		out = append(out, evExpr(ctx, r, fts))
	}
	return out
}

// evCell: row i of a column of type ft.
func evCell(col *chunk.Column, ft *types.FieldType, i int) any {
	if col.IsNull(i) {
		return nil
	}
	switch ft.GetType() {
	case mysql.TypeBit:
		return hex.EncodeToString(col.GetBytes(i))
	case mysql.TypeEnum:
		e := col.GetEnum(i)
		return []any{e.Value, vec.GoString(e.Name)}
	case mysql.TypeSet:
		s := col.GetSet(i)
		return []any{s.Value, vec.GoString(s.Name)}
	case mysql.TypeFloat:
		return strconv.FormatUint(uint64(math.Float32bits(col.GetFloat32(i))), 10)
	}
	switch ft.EvalType() {
	case types.ETInt:
		if mysql.HasUnsignedFlag(ft.GetFlag()) {
			return strconv.FormatUint(col.GetUint64(i), 10)
		}
		return strconv.FormatInt(col.GetInt64(i), 10)
	case types.ETReal:
		return strconv.FormatUint(math.Float64bits(col.GetFloat64(i)), 10)
	case types.ETDecimal:
		return decOut(col.GetDecimal(i))
	case types.ETString:
		return vec.GoString(col.GetString(i))
	case types.ETDatetime, types.ETTimestamp:
		return castTimeOut(col.GetTime(i))
	case types.ETDuration:
		return int64(col.GetDuration(i, 0).Duration)
	case types.ETJson:
		return castJSONOut(col.GetJSON(i))
	case types.ETVectorFloat32:
		return hex.EncodeToString(col.GetVectorFloat32(i).ZeroCopySerialize())
	}
	panic("evCell")
}

func evColumn(col *chunk.Column, ft *types.FieldType, n int) any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, evCell(col, ft, i))
	}
	return out
}

// evWarns: ecWarns of the context's warnings; pooled: the argument of a "Truncated
// incorrect DOUBLE value" warning is recorded as '<pooled>' (see the file comment).
func evWarns(ev *exprstatic.EvalContext, pooled bool) any {
	ws := ev.TruncateWarnings(0)
	out := make([]any, 0, len(ws))
	for _, w := range ws {
		msg := w.Err.Error()
		const p = "Truncated incorrect DOUBLE value: "
		if i := strings.Index(msg, p); pooled && i >= 0 {
			msg = msg[:i+len(p)] + "'<pooled>'"
		}
		out = append(out, []any{w.Level, vec.GoString(msg)})
	}
	return out
}

// evBools: a bool slice, nil as empty.
func evBools(b []bool) []bool {
	if b == nil {
		return []bool{}
	}
	return b
}

func evErrOrNil(err error) any {
	if err == nil {
		return nil
	}
	return vec.GoString(err.Error())
}

func castExec2b3c(op string, p []json.RawMessage) any {
	switch op {
	case "evsuite":
		ctx, ev := castCtx(p[3])
		fts, input := evInput(p[4], p[5], p[6])
		m := map[string]any{}
		m["res"] = exTry(func() any {
			exprs := evExprs(ctx, p[0], fts)
			ev.TruncateWarnings(0)
			outFts := []*types.FieldType{}
			for _, e := range exprs {
				outFts = append(outFts, e.GetType(ev))
			}
			s := expression.NewEvaluatorSuite(exprs, arg[bool](p, 1))
			props := uint64(s.RequiredOptionalEvalProps())
			output := chunk.NewChunkWithCapacity(outFts, input.NumRows())
			err := s.Run(ev, arg[bool](p, 2), input, output)
			r := map[string]any{"vectorizable": s.Vectorizable(), "props": props, "err": evErrOrNil(err),
				"warns": ecWarns(ev.TruncateWarnings(0))}
			if err == nil {
				cols := []any{}
				for i, ft := range outFts {
					c := output.Column(i)
					cols = append(cols, evColumn(c, ft, c.Rows()))
				}
				r["out"] = cols
				lens := []int{}
				for i := 0; i < input.NumCols(); i++ {
					lens = append(lens, input.Column(i).Rows())
				}
				r["inlens"] = lens
			}
			return r
		})
		return m
	case "evfilter":
		ctx, ev := castCtx(p[3])
		fts, input := evInput(p[4], p[5], p[6])
		m := map[string]any{}
		m["res"] = exTry(func() any {
			filters := evExprs(ctx, p[0], fts)
			ev.TruncateWarnings(0)
			iter := chunk.NewIterator4Chunk(input)
			r := map[string]any{}
			var selected, isNull []bool
			var err error
			if arg[bool](p, 2) {
				selected, isNull, err = expression.VectorizedFilterConsiderNull(ev, arg[bool](p, 1), filters, iter, nil, []bool{})
			} else {
				selected, err = expression.VectorizedFilter(ev, arg[bool](p, 1), filters, iter, nil)
			}
			r["err"] = evErrOrNil(err)
			vecPath := arg[bool](p, 1)
			for _, f := range filters {
				vecPath = vecPath && f.Vectorized()
			}
			r["warns"] = evWarns(ev, vecPath)
			if err == nil {
				// A nil and an empty selected slice are the same to the callers (no rows).
				r["selected"] = evBools(selected)
				if isNull == nil {
					r["isnull"] = nil
				} else {
					r["isnull"] = isNull
				}
			}
			r["sel"] = input.Sel()
			return r
		})
		return m
	case "evbool":
		ctx, ev := castCtx(p[2])
		fts, input := evInput(p[3], p[4], p[5])
		m := map[string]any{}
		m["res"] = exTry(func() any {
			exprs := evExprs(ctx, p[0], fts)
			ev.TruncateWarnings(0)
			selected, nulls, err := expression.VecEvalBool(ev, arg[bool](p, 1), exprs, input, nil, nil)
			r := map[string]any{"err": evErrOrNil(err), "warns": evWarns(ev, true), "sel": input.Sel()}
			if err == nil {
				r["selected"] = evBools(selected)
				r["nulls"] = evBools(nulls)
			}
			return r
		})
		return m
	case "evexpr":
		ctx, ev := castCtx(p[3])
		fts, input := evInput(p[4], p[5], p[6])
		m := map[string]any{}
		m["res"] = exTry(func() any {
			e := evExpr(ctx, p[0], fts)
			ev.TruncateWarnings(0)
			et := types.EvalType(arg[int](p, 2))
			col := chunk.NewColumn(types.NewFieldType(mysql.TypeLonglong), input.NumRows())
			err := expression.EvalExpr(ev, arg[bool](p, 1), e, et, input, col)
			r := map[string]any{"err": evErrOrNil(err), "warns": ecWarns(ev.TruncateWarnings(0))}
			if err == nil {
				// Dump by the evaluation type the caller chose (a hybrid read as int or string).
				ft := evFtOf(et)
				r["v"] = evColumn(col, ft, col.Rows())
			}
			return r
		})
		return m
	case "evexec":
		ctx, ev := castCtx(p[1])
		fts, input := evInput(p[2], p[3], p[4])
		m := map[string]any{}
		m["res"] = exTry(func() any {
			exprs := evExprs(ctx, p[0], fts)
			ev.TruncateWarnings(0)
			outFts := []*types.FieldType{}
			for _, e := range exprs {
				outFts = append(outFts, e.GetType(ev))
			}
			output := chunk.NewChunkWithCapacity(outFts, input.NumRows())
			err := expression.VectorizedExecute(ev, exprs, chunk.NewIterator4Chunk(input), output)
			r := map[string]any{"err": evErrOrNil(err), "warns": ecWarns(ev.TruncateWarnings(0))}
			cols := []any{}
			for i, ft := range outFts {
				c := output.Column(i)
				cols = append(cols, evColumn(c, ft, c.Rows()))
			}
			r["out"] = cols
			return r
		})
		return m
	}
	return nil
}

// evFtOf: a plain type of an evaluation type, for dumping an EvalExpr result.
func evFtOf(et types.EvalType) *types.FieldType {
	switch et {
	case types.ETInt:
		return types.NewFieldType(mysql.TypeLonglong)
	case types.ETReal:
		return types.NewFieldType(mysql.TypeDouble)
	case types.ETDecimal:
		return types.NewFieldType(mysql.TypeNewDecimal)
	case types.ETDatetime, types.ETTimestamp:
		return types.NewFieldType(mysql.TypeDatetime)
	case types.ETDuration:
		return types.NewFieldType(mysql.TypeDuration)
	case types.ETJson:
		return types.NewFieldType(mysql.TypeJSON)
	case types.ETVectorFloat32:
		return types.NewFieldType(mysql.TypeTiDBVectorFloat32)
	}
	return types.NewFieldType(mysql.TypeVarString)
}

// ---------------------------------------------------------------- generation

// evCol: an input column type and its value generator (numeric/string, FLOAT and hybrid,
// time/JSON/VECTOR), with NULLs.
func (g *castGen) evCol() (*types.FieldType, func() []any) {
	var ft *types.FieldType
	var gen func() []any
	switch g.pick(10) {
	case 0, 1, 2:
		ft = g.argFt()
		gen = func() []any { return g.val(ft) }
	case 3, 4, 5:
		ft, gen = g.convCol()
	case 6:
		// An ENUM with an empty element: toBool tells the empty-string error value (0) from
		// the element "" by its index.
		elems := []string{"x", "", "0", "y"}
		ft = castFt(mysql.TypeEnum, 0, -1, 0)
		ft.SetCharset("utf8mb4")
		ft.SetCollate("utf8mb4_bin")
		ft.SetElems(elems)
		gen = func() []any {
			v := g.pick(len(elems) + 1) // 0 is the error value
			name := ""
			if v > 0 {
				name = elems[v-1]
			}
			return []any{types.KindMysqlEnum, v, vec.GoString(name), "utf8mb4_bin"}
		}
	default:
		ft, gen = g.fromCol()
	}
	return ft, func() []any {
		if g.pick(8) == 0 {
			return []any{types.KindNull}
		}
		return gen()
	}
}

// evRecipe: an expression over the columns: the column, a CAST of it, a constant, or a
// CAST of a CAST.
func (g *castGen) evRecipe(fts []*types.FieldType) any {
	i := g.pick(len(fts))
	col := []any{"col", i}
	tgt := func() *types.FieldType {
		switch g.pick(5) {
		case 0:
			return g.strTarget()
		case 1:
			return g.timeTarget()
		case 2:
			return g.jsonTarget()
		default:
			return g.target()
		}
	}
	switch g.pick(10) {
	case 0, 1, 2:
		return col
	case 3:
		ft := g.argFt()
		return []any{"const", g.val(ft), sfFtIn(ft)}
	case 4:
		return []any{"cast", sfFtIn(tgt()), []any{"cast", sfFtIn(tgt()), col}}
	default:
		return []any{"cast", sfFtIn(tgt()), col}
	}
}

// evRows: the input columns, rows and Sel().
func (g *castGen) evRows() ([]*types.FieldType, any, any, any) {
	nc := 1 + g.pick(3)
	fts := []*types.FieldType{}
	gens := []func() []any{}
	cols := []any{}
	for i := 0; i < nc; i++ {
		ft, gen := g.evCol()
		fts = append(fts, ft)
		gens = append(gens, gen)
		cols = append(cols, sfFtIn(ft))
	}
	n := g.pick(21)
	rows := []any{}
	for r := 0; r < n; r++ {
		row := []any{}
		for c := 0; c < nc; c++ {
			row = append(row, gens[c]())
		}
		rows = append(rows, row)
	}
	var sel any
	if g.pick(3) == 0 {
		s := []int{}
		for r := 0; r < n; r++ {
			if g.pick(2) == 0 {
				s = append(s, r)
			}
		}
		sel = s
	}
	return fts, cols, rows, sel
}

func (g *castGen) evRecipes(fts []*types.FieldType, max int) []any {
	k := 1 + g.pick(max)
	out := []any{}
	for i := 0; i < k; i++ {
		out = append(out, g.evRecipe(fts))
	}
	return out
}

func castGenerate2b3c(vg *vec.Gen, g *castGen) {
	for i := 0; i < 1500; i++ {
		fts, cols, rows, sel := g.evRows()
		vg.Add("sys", "evsuite", g.evRecipes(fts, 4), g.pick(3) == 0, g.pick(2) == 0, g.timeCtx(), cols, rows, sel)
	}
	for i := 0; i < 1500; i++ {
		fts, cols, rows, sel := g.evRows()
		vg.Add("sys", "evfilter", g.evRecipes(fts, 3), g.pick(2) == 0, g.pick(2) == 0, g.timeCtx(), cols, rows, sel)
	}
	for i := 0; i < 700; i++ {
		fts, cols, rows, sel := g.evRows()
		vg.Add("sys", "evbool", g.evRecipes(fts, 3), g.pick(2) == 0, g.timeCtx(), cols, rows, sel)
	}
	for i := 0; i < 700; i++ {
		fts, cols, rows, sel := g.evRows()
		r := g.evRecipe(fts)
		// The evaluation type: the expression's own, or, for a hybrid column, int or string.
		var et types.EvalType
		ra := r.([]any)
		switch ra[0] {
		case "col":
			ft := fts[ra[1].(int)]
			et = ft.EvalType()
			if ft.Hybrid() && g.pick(2) == 0 {
				et = []types.EvalType{types.ETInt, types.ETString}[g.pick(2)]
			}
		case "const":
			et = d2FtIn(evRaw(ra[2])).EvalType()
		default:
			et = d2FtIn(evRaw(ra[1])).EvalType()
		}
		vg.Add("sys", "evexpr", r, g.pick(2) == 0, int(et), g.timeCtx(), cols, rows, sel)
	}
	for i := 0; i < 300; i++ {
		fts, cols, rows, sel := g.evRows()
		vg.Add("sys", "evexec", g.evRecipes(fts, 3), g.timeCtx(), cols, rows, sel)
	}
}

// evRaw: a recipe part as the raw JSON the exec side reads.
func evRaw(v any) []json.RawMessage {
	b, err := json.Marshal(v)
	must(err)
	return exArr(b)
}
