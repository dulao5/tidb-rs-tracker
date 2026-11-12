package domains

// Domain "cast", D08.4 batch 1 (appended after part 2b-3c; earlier records are unchanged):
// the comparison operators <, <=, >, >=, =, !=, <=> (compareFunctionClass with its argument
// refining, the 56 signatures, CompareXxx, GetAccurateCmpType, GetCmpFunction).
//   - "cmpbuild": [variant, name, lhs, rhs, ctx, cols, rows, sel] (lhs and rhs are the
//     expression recipes of part 2b-3c; the recipe ["fn", name, [recipes]] builds a nested
//     comparison with NewFunctionBase):
//       "acc": GetAccurateCmpType(lhs, rhs);
//       "cf": the CompareFunc of GetCmpFunction(lhs, rhs) on every physical row, only when
//             both arguments evaluate as the comparison type (a column is read as its own
//             type: CompareReal on an int column reads garbage in Go);
//       "res": NewFunctionBase (variant 0) or NewFunction (variant 1, folding) of
//              name(lhs, rhs), dumped with sfDump (signature, refined arguments), or the
//              error; "bwarns": the build warnings;
//       "row"/"vec": EvalExpr of the result as ETInt with vectorization off / on over the
//              input (values, warnings, error).
//   - "newfunc" (part 2b-3a op) with the comparison names: NewFunction / NewFunctionTryFold
//     fold the comparison of two constants (fold 1 / -1: the TryFold truncation warning).
// Not generated: the `int non-constant <cmp> non-int constant` comparisons with < <= > >= that
// reach RefineComparedConstant's NewFunctionInternal(Ceil / Floor) (math functions, D08.5;
// spec §4 item 2; cmpReachesCeil). The branches before it are generated.

import (
	"encoding/json"
	"math"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
)

var cmpNames = []string{ast.LT, ast.LE, ast.GT, ast.GE, ast.EQ, ast.NE, ast.NullEQ}

func cmpSigFuncs() []string {
	out := []string{}
	for _, op := range []string{"LT", "LE", "GT", "GE", "EQ", "NE", "NullEQ"} {
		for _, t := range []string{"Int", "Real", "Decimal", "String", "Time", "Duration", "JSON", "VectorFloat32"} {
			s := "builtin" + op + t + "Sig."
			out = append(out, s+"evalInt", s+"Clone")
			if t != "VectorFloat32" {
				out = append(out, s+"vecEvalInt", s+"vectorized")
			}
		}
	}
	return out
}

func init() {
	castCoverage["cmpbuild"] = vec.Coverage{Min: 3000, GoFuncs: append(cmpSigFuncs(),
		"compareFunctionClass.getFunction", "compareFunctionClass.generateCmpSigs",
		"compareFunctionClass.refineArgs", "compareFunctionClass.refineArgsByUnsignedFlag",
		"compareFunctionClass.refineNumericConstantCmpDatetime",
		"compareFunctionClass.handleDurationTypeComparisonForNullEq", "compareFunctionClass.getDisplayName",
		"RefineComparedConstant", "tryToConvertConstantInt", "matchRefineRule3Pattern",
		"allowCmpArgsRefining4PlanCache", "GetAccurateCmpType", "getBaseCmpType", "isTemporalColumn",
		"GetCmpFunction", "genCompareString", "compareNull", "CompareInt", "CompareReal", "CompareDecimal",
		"CompareStringWithCollationInfo", "CompareTime", "CompareDuration", "CompareJSON",
		"CompareVectorFloat32", "resOfLT", "resOfLE", "resOfGT", "resOfGE", "resOfEQ", "resOfNE",
		"vecResOfLT", "vecResOfLE", "vecResOfGT", "vecResOfGE", "vecResOfEQ", "vecResOfNE", "vecCompareInt")}
}

func castExecD084(op string, p []json.RawMessage) any {
	if op != "cmpbuild" {
		return nil
	}
	ctx, ev := castCtx(p[4])
	fts, input := evInput(p[5], p[6], p[7])
	m := map[string]any{}
	var l, r expression.Expression
	m["acc"] = exTry(func() any {
		l = evExpr(ctx, p[2], fts)
		r = evExpr(ctx, p[3], fts)
		ev.TruncateWarnings(0)
		return int(expression.GetAccurateCmpType(ev, l, r))
	})
	if l == nil || r == nil {
		return m
	}
	m["cf"] = exTry(func() any {
		acc := expression.GetAccurateCmpType(ev, l, r)
		same := func(e expression.Expression) bool {
			et := e.GetType(ev).EvalType()
			if acc == types.ETDatetime || acc == types.ETTimestamp {
				return et == types.ETDatetime || et == types.ETTimestamp
			}
			return et == acc
		}
		if !same(l) || !same(r) {
			return nil
		}
		f := expression.GetCmpFunction(ctx, l, r)
		out := []any{}
		for i := 0; i < input.NumRows(); i++ {
			row := input.GetRow(i)
			v, isNull, err := f(ev, l, r, row, row)
			if err != nil {
				out = append(out, exErr(err))
			} else {
				out = append(out, []any{v, isNull})
			}
		}
		return out
	})
	m["cfwarns"] = ecWarns(ev.TruncateWarnings(0))
	var e expression.Expression
	m["res"] = exTry(func() any {
		var err error
		tp := types.NewFieldType(mysql.TypeUnspecified)
		if arg[int](p, 0) == 0 {
			e, err = expression.NewFunctionBase(ctx, arg[string](p, 1), tp, l, r)
		} else {
			e, err = expression.NewFunction(ctx, arg[string](p, 1), tp, l, r)
		}
		if err != nil {
			e = nil
			return exErr(err)
		}
		return sfDump(e)
	})
	m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
	if e != nil {
		for _, mode := range []struct {
			key string
			on  bool
		}{{"row", false}, {"vec", true}} {
			m[mode.key] = exTry(func() any {
				col := chunk.NewColumn(types.NewFieldType(mysql.TypeLonglong), input.NumRows())
				err := expression.EvalExpr(ev, mode.on, e, types.ETInt, input, col)
				o := map[string]any{"err": evErrOrNil(err), "warns": ecWarns(ev.TruncateWarnings(0))}
				if err == nil {
					o["v"] = evColumn(col, types.NewFieldType(mysql.TypeLonglong), col.Rows())
				}
				return o
			})
		}
	}
	return m
}

// ---------------------------------------------------------------- generation

// cmpYearCol: a YEAR column (values 0 and 1901..2155, NULLs).
func (g *castGen) cmpYearCol() (*types.FieldType, func() []any) {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.NotNullFlag
	}
	ft := castFt(mysql.TypeYear, flag|mysql.UnsignedFlag|mysql.ZerofillFlag, 4, 0)
	return ft, func() []any {
		if g.pick(6) == 0 {
			return []any{types.KindInt64, int64(0)}
		}
		return []any{types.KindInt64, int64(1901 + g.pick(255))}
	}
}

// cmpCol: a column type for the comparisons: numeric and string (argFt), FLOAT and hybrid,
// time/duration/JSON/VECTOR, YEAR.
func (g *castGen) cmpCol() (*types.FieldType, func() []any) {
	var ft *types.FieldType
	var gen func() []any
	switch g.pick(10) {
	case 0, 1, 2, 3:
		ft = g.argFt()
		gen = func() []any { return g.val(ft) }
	case 4:
		ft, gen = g.convCol()
	case 5:
		ft, gen = g.cmpYearCol()
	default:
		ft, gen = g.fromCol()
	}
	return ft, func() []any {
		if !mysql.HasNotNullFlag(ft.GetFlag()) && g.pick(7) == 0 {
			return []any{types.KindNull}
		}
		return gen()
	}
}

var cmpConstStrs = []string{"1", "1.5", "-1", "0", "2019", "69", "70", "10:00:00", "-838:59:59", "25:00:61",
	"2024-01-02", "2024-01-02 03:04:05.5", "abc", "12abc", "[1]", `{"a":1}`, "", " 12 ", "1e3", "0x10"}

// cmpConst: a constant recipe and its type: special values of every kind next to them.
func (g *castGen) cmpConst() (any, *types.FieldType) {
	var flag uint
	if g.pick(4) == 0 {
		flag |= mysql.NotNullFlag
	}
	switch g.pick(9) {
	case 0, 1:
		v := []int64{-1, 0, 1, 2, 69, 70, 99, 100, 2019, 2155, 256, math.MaxInt64, math.MinInt64}[g.pick(13)]
		ft := castFt(mysql.TypeLonglong, flag, 20, 0)
		return []any{"const", []any{types.KindInt64, v}, sfFtIn(ft)}, ft
	case 2:
		v := []uint64{0, 1, math.MaxUint64, 1 << 63, 18446744073709551615 - 1}[g.pick(5)]
		ft := castFt(mysql.TypeLonglong, flag|mysql.UnsignedFlag, 20, 0)
		return []any{"const", []any{types.KindUint64, v}, sfFtIn(ft)}, ft
	case 3:
		v := []float64{1.5, -1.5, 2.0, 0.5, 1e20, -1e20, 2019.0, 0}[g.pick(8)]
		ft := castFt(mysql.TypeDouble, flag, 22, -1)
		return []any{"const", []any{types.KindFloat64, math.Float64bits(v)}, sfFtIn(ft)}, ft
	case 4:
		v := []string{"1.5", "-1.5", "2", "2019", "0.0", "99999999999999999999.5"}[g.pick(6)]
		ft := castFt(mysql.TypeNewDecimal, flag, 30, 5)
		return []any{"const", []any{types.KindMysqlDecimal, v}, sfFtIn(ft)}, ft
	case 5, 6:
		s := cmpConstStrs[g.pick(len(cmpConstStrs))]
		ft := castFt(mysql.TypeVarchar, flag, 64, 0)
		return []any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(ft)}, ft
	case 7:
		ft := types.NewFieldType(mysql.TypeNull)
		return []any{"const", []any{types.KindNull}, sfFtIn(ft)}, ft
	default:
		ft := g.argFt()
		return []any{"const", g.val(ft), sfFtIn(ft)}, ft
	}
}

// cmpLeaf: an argument recipe over the columns, its evaluation type and whether it is a
// constant.
func (g *castGen) cmpLeaf(fts []*types.FieldType, depth int, reaches func(name string, a, b any) bool) (any, types.EvalType, bool) {
	switch k := g.pick(10); {
	case k < 4:
		i := g.pick(len(fts))
		return []any{"col", i}, fts[i].EvalType(), false
	case k < 7:
		c, ft := g.cmpConst()
		return c, ft.EvalType(), true
	case k < 9 || depth > 0:
		var tgt *types.FieldType
		switch g.pick(4) {
		case 0:
			tgt = g.strTarget()
		case 1:
			tgt = g.timeTarget()
		case 2:
			tgt = g.jsonTarget()
		default:
			tgt = g.target()
		}
		i := g.pick(len(fts))
		return []any{"cast", sfFtIn(tgt), []any{"col", i}}, tgt.EvalType(), false
	default:
		name := cmpNames[g.pick(len(cmpNames))]
		a, _, _ := g.cmpLeaf(fts, depth+1, reaches)
		b, _, _ := g.cmpLeaf(fts, depth+1, reaches)
		if reaches(name, a, b) {
			name = ast.EQ
		}
		return []any{"fn", name, []any{a, b}}, types.ETInt, false
	}
}

// cmpReachesCeil: whether refineArgs refines a constant through NewFunctionInternal(Ceil /
// Floor), which tidb-rs cannot build before the math functions (D08.5): `int non-constant
// <, <=, >, >= non-int constant` where RefineComparedConstant gets past the evaluation, the
// conversion to the int type (no error, no overflow) and the comparison with the original
// value (not equal). The other branches (NULL constant, conversion error, overflow, exact
// equality such as `int_col < 2.0`) are generated: tidb-rs implements them. Decided by
// running the same steps on the built arguments (Lucky, review of tidb-rs #98).
func cmpReachesCeil(ev *exprstatic.EvalContext, name string, l, r expression.Expression) bool {
	switch name {
	case ast.LT, ast.LE, ast.GT, ast.GE:
	default:
		return false
	}
	lt, rt := l.GetType(ev), r.GetType(ev)
	lc, lIsCon := l.(*expression.Constant)
	rc, rIsCon := r.(*expression.Constant)
	if lt.EvalType() == types.ETInt && !lIsCon && rt.EvalType() != types.ETInt && rIsCon {
		return cmpRefineReachesCeil(ev, *lt, rc)
	}
	if rt.EvalType() == types.ETInt && !rIsCon && lt.EvalType() != types.ETInt && lIsCon {
		return cmpRefineReachesCeil(ev, *rt, lc)
	}
	return false
}

// cmpRefineReachesCeil: RefineComparedConstant up to its Ceil / Floor call.
func cmpRefineReachesCeil(ev *exprstatic.EvalContext, target types.FieldType, con *expression.Constant) bool {
	dt, err := con.Eval(ev, chunk.Row{})
	if err != nil {
		return false
	}
	if target.GetType() == mysql.TypeBit {
		target = *types.NewFieldType(mysql.TypeLonglong)
	}
	tc := ev.TypeCtx()
	intDatum, err := dt.ConvertTo(tc.WithFlags(tc.Flags().WithAllowNegativeToUnsigned(false)), &target)
	if err != nil {
		return false
	}
	c, err := intDatum.Compare(tc, &con.Value, collate.GetBinaryCollator())
	return err == nil && c != 0
}

// cmpReachesCeilRecipes: cmpReachesCeil on expression recipes over the columns (an argument
// that cannot be built does not reach it).
func cmpReachesCeilRecipes(ctxRaw, colsRaw any, name string, a, b any) (reached bool) {
	switch name {
	case ast.LT, ast.LE, ast.GT, ast.GE:
	default:
		return false
	}
	defer func() {
		if recover() != nil {
			reached = false
		}
	}()
	ctx, ev := castCtx(cmpJSON(ctxRaw))
	fts, _ := evInput(cmpJSON(colsRaw), json.RawMessage("[]"), json.RawMessage("null"))
	l := evExpr(ctx, cmpJSON(a), fts)
	r := evExpr(ctx, cmpJSON(b), fts)
	return cmpReachesCeil(ev, name, l, r)
}

// cmpReachesCeilLeaves: cmpReachesCeil on castArg leaves (the newfunc records).
func cmpReachesCeilLeaves(ctxRaw any, name string, a, b any) (reached bool) {
	switch name {
	case ast.LT, ast.LE, ast.GT, ast.GE:
	default:
		return false
	}
	defer func() {
		if recover() != nil {
			reached = false
		}
	}()
	_, ev := castCtx(cmpJSON(ctxRaw))
	l, _ := castArg(cmpJSON(a), nil)
	r, _ := castArg(cmpJSON(b), nil)
	return cmpReachesCeil(ev, name, l, r)
}

func cmpJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	must(err)
	return b
}

// cmpRows: the input columns, rows and Sel() for the comparisons.
func (g *castGen) cmpRows() ([]*types.FieldType, any, any, any) {
	nc := 1 + g.pick(3)
	fts := []*types.FieldType{}
	gens := []func() []any{}
	cols := []any{}
	for i := 0; i < nc; i++ {
		ft, gen := g.cmpCol()
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
	if g.pick(4) == 0 {
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

func castGenerateD084(vg *vec.Gen, g *castGen) {
	for i := 0; i < 3000; {
		fts, cols, rows, sel := g.cmpRows()
		name := cmpNames[g.pick(len(cmpNames))]
		ctx := g.timeCtx()
		reaches := func(n string, x, y any) bool { return cmpReachesCeilRecipes(ctx, cols, n, x, y) }
		a, _, _ := g.cmpLeaf(fts, 0, reaches)
		b, _, _ := g.cmpLeaf(fts, 0, reaches)
		if reaches(name, a, b) {
			continue
		}
		i++
		vg.Add("sys", "cmpbuild", g.pick(3)/2, name, a, b, ctx, cols, rows, sel)
	}
	// Two columns of the same kind (the CompareFunc rows; equal values; NULL-safe equality).
	for i := 0; i < 500; i++ {
		ft, gen := g.cmpCol()
		cols := []any{sfFtIn(ft), sfFtIn(ft)}
		n := g.pick(16)
		rows := []any{}
		for r := 0; r < n; r++ {
			v := gen()
			w := v
			if g.pick(2) == 0 {
				w = gen()
			}
			rows = append(rows, []any{v, w})
		}
		name := cmpNames[g.pick(len(cmpNames))]
		vg.Add("same", "cmpbuild", 0, name, []any{"col", 0}, []any{"col", 1}, g.timeCtx(), cols, rows, nil)
	}
	// Signed and unsigned BIGINT columns and constants: CompareInt's unsigned flags (NULL-safe
	// equality too) and refineArgsByUnsignedFlag (unsigned NOT NULL column vs 0 / -1).
	for i := 0; i < 400; i++ {
		var sflag, uflag uint
		if g.pick(2) == 0 {
			sflag |= mysql.NotNullFlag
		}
		if g.pick(2) == 0 {
			uflag |= mysql.NotNullFlag
		}
		sft := castFt(mysql.TypeLonglong, sflag, 20, 0)
		uft := castFt(mysql.TypeLonglong, uflag|mysql.UnsignedFlag, 20, 0)
		svals := []int64{-1, 0, 1, math.MinInt64, math.MaxInt64}
		uvals := []uint64{0, 1, math.MaxUint64, 1 << 63, math.MaxInt64}
		n := g.pick(12)
		rows := []any{}
		for r := 0; r < n; r++ {
			sv := []any{types.KindInt64, svals[g.pick(len(svals))]}
			uv := []any{types.KindUint64, uvals[g.pick(len(uvals))]}
			if sflag == 0 && g.pick(6) == 0 {
				sv = []any{types.KindNull}
			}
			if uflag == 0 && g.pick(6) == 0 {
				uv = []any{types.KindNull}
			}
			rows = append(rows, []any{sv, uv})
		}
		cols := []any{sfFtIn(sft), sfFtIn(uft)}
		var a, b any
		switch g.pick(3) {
		case 0:
			a, b = []any{"col", 0}, []any{"col", 1}
		case 1:
			c := castFt(mysql.TypeLonglong, 0, 20, 0)
			cv := []any{types.KindInt64, []int64{-1, 0, 1}[g.pick(3)]}
			if g.pick(3) == 0 {
				c = castFt(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0)
				cv = []any{types.KindUint64, []uint64{0, 1, math.MaxUint64}[g.pick(3)]}
			}
			a, b = []any{"col", 1}, []any{"const", cv, sfFtIn(c)}
		default:
			c := castFt(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0)
			cv := []any{types.KindUint64, []uint64{0, 1, math.MaxUint64, 1 << 63}[g.pick(4)]}
			a, b = []any{"col", 0}, []any{"const", cv, sfFtIn(c)}
		}
		if g.pick(2) == 0 {
			a, b = b, a
		}
		vg.Add("sign", "cmpbuild", 0, cmpNames[g.pick(len(cmpNames))], a, b, g.timeCtx(), cols, rows, nil)
	}
	// The refining branches before Ceil / Floor (Lucky, review of tidb-rs #98): integer, YEAR
	// and unsigned columns against NULL, overflowing, unconvertible and exactly-integral
	// DOUBLE / DECIMAL / string constants with < <= > >= (the ones that reach Ceil / Floor
	// are skipped, cmpReachesCeil).
	for i := 0; i < 600; {
		var ft *types.FieldType
		var gen func() []any
		switch g.pick(3) {
		case 0:
			ft, gen = g.cmpYearCol()
		case 1:
			var flag uint
			if g.pick(2) == 0 {
				flag |= mysql.NotNullFlag
			}
			if g.pick(2) == 0 {
				flag |= mysql.UnsignedFlag
			}
			ft = castFt([]byte{mysql.TypeLonglong, mysql.TypeTiny, mysql.TypeLong}[g.pick(3)], flag, 11, 0)
			gen = func() []any { return g.intVal(mysql.HasUnsignedFlag(ft.GetFlag())) }
		default:
			ft = castFt(mysql.TypeLonglong, 0, 20, 0)
			gen = func() []any { return []any{types.KindInt64, int64([]int{-1, 0, 1, 2, 7, 2019, 2002}[g.pick(7)])} }
		}
		n := g.pick(10)
		rows := []any{}
		for r := 0; r < n; r++ {
			if !mysql.HasNotNullFlag(ft.GetFlag()) && g.pick(6) == 0 {
				rows = append(rows, []any{[]any{types.KindNull}})
			} else {
				rows = append(rows, []any{gen()})
			}
		}
		cols := []any{sfFtIn(ft)}
		var c any
		switch g.pick(5) {
		case 0:
			v := []float64{2.0, 2019.0, 1e30, -1e30, 0, -1, 7.0, 1977.0}[g.pick(8)]
			c = []any{"const", []any{types.KindFloat64, math.Float64bits(v)}, sfFtIn(castFt(mysql.TypeDouble, 0, 22, -1))}
		case 1:
			v := []string{"2.0", "2019.000", "99999999999999999999", "-1", "7", "1977"}[g.pick(6)]
			c = []any{"const", []any{types.KindMysqlDecimal, v}, sfFtIn(castFt(mysql.TypeNewDecimal, 0, 30, 5))}
		case 2:
			v := []string{"2", "2019", "abc", "12abc", "99999999999999999999", "", "7", "1977"}[g.pick(8)]
			c = []any{"const", []any{types.KindString, vec.GoString(v), charset.CollationUTF8MB4}, sfFtIn(castFt(mysql.TypeVarchar, 0, 64, 0))}
		case 3:
			c = []any{"const", []any{types.KindNull}, sfFtIn(types.NewFieldType(mysql.TypeNull))}
		default:
			c = []any{"const", []any{types.KindNull}, sfFtIn(castFt(mysql.TypeDouble, 0, 22, -1))}
		}
		var a, b any = []any{"col", 0}, c
		if g.pick(2) == 0 {
			a, b = b, a
		}
		name := []string{ast.LT, ast.LE, ast.GT, ast.GE}[g.pick(4)]
		ctx := g.timeCtx()
		if cmpReachesCeilRecipes(ctx, cols, name, a, b) {
			continue
		}
		i++
		vg.Add("refine", "cmpbuild", g.pick(2), name, a, b, ctx, cols, rows, nil)
	}
	// CAST('[1]' AS JSON) = '[1]': 0 in v8.5.8 (generateCmpSigs disables the JSON parsing
	// of the string argument; D08.12 DisableParseJSONFlag4Expr).
	jsonFt := castFt(mysql.TypeJSON, mysql.BinaryFlag, -1, 0)
	strFt := castFt(mysql.TypeVarchar, 0, 64, 0)
	for _, s := range []string{"[1]", `{"a":1}`, "1", `"x"`, "null", "abc"} {
		str := []any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(strFt)}
		for _, name := range cmpNames {
			for variant := 0; variant < 2; variant++ {
				vg.Add("json-parse", "cmpbuild", variant, name, []any{"cast", sfFtIn(jsonFt), str}, str,
					g.timeCtx(), []any{}, []any{}, nil)
			}
		}
	}
	// Folding with warnings: NewFunctionTryFold (fold -1) returns the unfolded function and
	// drops the warnings when folding warns; NewFunction (fold 1) keeps the folded constant.
	strFt2 := castFt(mysql.TypeVarchar, 0, 64, 0)
	for i := 0; i < 200; i++ {
		s := []string{"12abc", "abc", " 1x", "1.5e", "9e999", "1"}[g.pick(6)]
		var a any = []any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(strFt2)}
		var b any
		switch g.pick(3) {
		case 0:
			b = []any{"const", []any{types.KindInt64, int64(g.pick(3))}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}
		case 1:
			b = []any{"const", []any{types.KindFloat64, math.Float64bits(1.5)}, sfFtIn(castFt(mysql.TypeDouble, 0, 22, -1))}
		default:
			b = []any{"const", []any{types.KindMysqlDecimal, "12"}, sfFtIn(castFt(mysql.TypeNewDecimal, 0, 10, 2))}
		}
		if g.pick(2) == 0 {
			a, b = b, a
		}
		lv := []int{1, 1, 1, 1, 1, 1, 1}
		if g.pick(4) == 0 {
			lv = []int{0, 0, 0, 0, 0, 0, 0}
		}
		ctx := []any{uint16([]int{0, 2, 1, 4}[g.pick(4)]), lv, uint64(67108864), uint64(0)}
		vg.Add("tryfold", "newfunc", []int{0, 2}[g.pick(2)], cmpNames[g.pick(len(cmpNames))],
			sfFtIn(types.NewFieldType(mysql.TypeUnspecified)), []any{a, b}, ctx, "")
	}
	// NewFunction / NewFunctionTryFold on constants: fold 1 / -1 of newFunctionImpl (D08.1).
	for i := 0; i < 700; {
		name := cmpNames[g.pick(len(cmpNames))]
		a, aft := g.b3Leaf()
		b, bft := g.b3Leaf()
		if g.pick(2) == 0 {
			c, ft := g.cmpConst()
			a, aft = c, ft
		}
		if g.pick(2) == 0 {
			c, ft := g.cmpConst()
			b, bft = c, ft
		}
		_, _ = aft, bft
		ctx := g.strCtx()
		if cmpReachesCeilLeaves(ctx, name, a, b) {
			continue
		}
		i++
		variant := []int{0, 0, 2, 2, 1, 3, 4}[g.pick(7)]
		vg.Add("cmp", "newfunc", variant, name, sfFtIn(types.NewFieldType(mysql.TypeUnspecified)),
			[]any{a, b}, ctx, "")
	}
}
