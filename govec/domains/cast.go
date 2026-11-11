package domains

// Domain "cast", block D08.8 part 1: CAST to INT, REAL and DECIMAL (builtin_cast.go), built
// with BuildCastFunctionWithCheck and evaluated both ways Go's executors do — row by row
// (EvalInt/EvalReal/EvalDecimal) and over a chunk (VecEvalXxx) — with the values, errors and
// warnings of each, under random type flags and error levels. Plus FoldConstant (D08.2) on a
// constant argument and PropagateType (D08.1) on DECIMAL -> REAL.
//
// The argument is a column (index 0) of an INT/BIGINT/TINYINT [UNSIGNED], DOUBLE, DECIMAL(p,s)
// or VARCHAR type with the rows of the record, or a constant. FLOAT and hybrid (BIT, ENUM, SET)
// arguments are not generated: tidb-rs's vector driver reads such columns as stored (part 2,
// see tidb-rs specs/D08.8-cast/plan.md).
//
// Records:
//   - "cast": [arg, tpIn, inUnion, ctx, rows] -> build (sfDump or error), meta (inUnion of a
//     baseBuiltinCastFunc), bwarns (warnings of building, i.e. of folding), rows (row-mode
//     value and warnings per row), vec (vector-mode values, or the error, and warnings).
//     arg = ["col", ftIn] | ["const", datumIn, ftIn]; ctx = [typeFlags, [7]errLevels].
//   - "wrap": [which, arg, ctx] -> WrapWithCastAsInt/Real/Decimal: sfDump, bwarns.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/errctx"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
)

const castSeed = 20261114

func init() {
	vec.Register(&vec.Domain{
		Name:     "cast",
		Seed:     castSeed,
		Coverage: castCoverage,
		Blocks:   []string{"D08.8", "D08.2", "D08.1"},
		Exempt:   castExempt(),
		Gzip:     true,
		Generate: castGenerate,
		Exec:     castExec,
	})
}

var castSigs = []string{"IntAsInt", "IntAsReal", "IntAsDecimal", "RealAsInt", "RealAsReal", "RealAsDecimal",
	"DecimalAsInt", "DecimalAsReal", "DecimalAsDecimal", "StringAsInt", "StringAsReal", "StringAsDecimal"}

func castSigFuncs() []string {
	out := []string{}
	for _, s := range castSigs {
		et := "Int"
		switch {
		case len(s) > 4 && s[len(s)-4:] == "Real":
			et = "Real"
		case len(s) > 7 && s[len(s)-7:] == "Decimal":
			et = "Decimal"
		}
		out = append(out, "builtinCast"+s+"Sig.eval"+et, "builtinCast"+s+"Sig.vecEval"+et,
			"builtinCast"+s+"Sig.vectorized", "builtinCast"+s+"Sig.Clone")
	}
	return append(out, "builtinCastStringAsIntSig.handleOverflow")
}

var castCoverage = map[string]vec.Coverage{
	"cast": {Min: 2500, GoFuncs: append(castSigFuncs(),
		"castAsIntFunctionClass.getFunction", "castAsRealFunctionClass.getFunction",
		"castAsDecimalFunctionClass.getFunction", "BuildCastFunctionWithCheck", "BuildCastFunction",
		"TryPushCastIntoControlFunctionForHybridType", "setDataTypeDouble", "floatLength", "PropagateType",
		"newBaseBuiltinCastFunc", "baseBuiltinCastFunc.metadata", "baseBuiltinCastFunc.cloneFrom",
		"FoldConstant", "foldConstant")},
	"wrap": {Min: 600, GoFuncs: []string{"WrapWithCastAsInt", "WrapWithCastAsReal", "WrapWithCastAsDecimal",
		"minimalDecimalLenForHoldingInteger"}},
}

// ---------------------------------------------------------------- execution

func castCtx(raw json.RawMessage) (*exprstatic.ExprContext, *exprstatic.EvalContext) {
	a := exArr(raw)
	var lv []int
	must(json.Unmarshal(a[1], &lv))
	var lm errctx.LevelMap
	for i := range lm {
		lm[i] = errctx.Level(lv[i])
	}
	ev := exprstatic.NewEvalContext(
		exprstatic.WithTypeFlags(types.Flags(arg[uint16](a, 0))),
		exprstatic.WithErrLevelMap(lm),
		exprstatic.WithWarnHandler(contextutil.NewStaticWarnHandler(0)),
	)
	return exprstatic.NewExprContext(exprstatic.WithEvalCtx(ev)), ev
}

// castArg: the argument node, its type, and the chunk of the rows (column arguments).
func castArg(raw json.RawMessage, rowsRaw json.RawMessage) (expression.Expression, *chunk.Chunk) {
	a := exArr(raw)
	switch arg[string](a, 0) {
	case "col":
		ft := d2FtIn(exArr(a[1]))
		var rows []json.RawMessage
		if rowsRaw != nil {
			must(json.Unmarshal(rowsRaw, &rows))
		}
		chk := chunk.NewChunkWithCapacity([]*types.FieldType{ft}, len(rows))
		for _, r := range rows {
			d := d2DatumIn(exArr(r))
			chk.AppendDatum(0, &d)
		}
		return &expression.Column{UniqueID: 1, Index: 0, RetType: ft, OrigName: "c"}, chk
	case "const":
		return &expression.Constant{Value: d2DatumIn(exArr(a[1])), RetType: d2FtIn(exArr(a[2]))}, nil
	}
	panic("castArg")
}

// castMeta: baseBuiltinCastFunc.inUnion of the signature, or nil.
func castMeta(e expression.Expression) any {
	sf, ok := e.(*expression.ScalarFunction)
	if !ok {
		return nil
	}
	f := reflect.ValueOf(sf.Function).Elem().FieldByName("inUnion")
	if !f.IsValid() {
		return nil
	}
	return f.Bool()
}

func castRowVal(ctx expression.EvalContext, e expression.Expression, row chunk.Row) any {
	switch e.GetType(ctx).EvalType() {
	case types.ETInt:
		v, n, err := e.EvalInt(ctx, row)
		return exVal(strconv.FormatInt(v, 10), n, err)
	case types.ETReal:
		v, n, err := e.EvalReal(ctx, row)
		return exVal(strconv.FormatUint(math.Float64bits(v), 10), n, err)
	case types.ETDecimal:
		v, n, err := e.EvalDecimal(ctx, row)
		if err != nil || n {
			return exVal(nil, n, err)
		}
		return exVal(decOut(v), false, nil)
	}
	panic("castRowVal")
}

func castVec(ctx *exprstatic.EvalContext, e expression.Expression, chk *chunk.Chunk) any {
	n := chk.NumRows()
	var col *chunk.Column
	var err error
	et := e.GetType(ctx).EvalType()
	switch et {
	case types.ETInt:
		col = chunk.NewColumn(types.NewFieldType(mysql.TypeLonglong), n)
		err = e.VecEvalInt(ctx, chk, col)
	case types.ETReal:
		col = chunk.NewColumn(types.NewFieldType(mysql.TypeDouble), n)
		err = e.VecEvalReal(ctx, chk, col)
	case types.ETDecimal:
		col = chunk.NewColumn(types.NewFieldType(mysql.TypeNewDecimal), n)
		err = e.VecEvalDecimal(ctx, chk, col)
	}
	warns := ecWarns(ctx.TruncateWarnings(0))
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error()), "warns": warns}
	}
	vals := make([]any, 0, n)
	for i := 0; i < n; i++ {
		if col.IsNull(i) {
			vals = append(vals, nil)
			continue
		}
		switch et {
		case types.ETInt:
			vals = append(vals, strconv.FormatInt(col.GetInt64(i), 10))
		case types.ETReal:
			vals = append(vals, strconv.FormatUint(math.Float64bits(col.GetFloat64(i)), 10))
		case types.ETDecimal:
			vals = append(vals, decOut(col.GetDecimal(i)))
		}
	}
	return map[string]any{"v": vals, "warns": warns}
}

func castExec(op string, p []json.RawMessage) (out any, err error) {
	switch op {
	case "cast":
		m := map[string]any{}
		ctx, ev := castCtx(p[3])
		argE, chk := castArg(p[0], p[4])
		tp := d2FtIn(exArr(p[1]))
		var res expression.Expression
		m["build"] = exTry(func() any {
			r, err := expression.BuildCastFunctionWithCheck(ctx, argE, tp, arg[bool](p, 2), false)
			if err != nil {
				return exErr(err)
			}
			res = r
			return sfDump(r)
		})
		m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
		if res == nil {
			return m, nil
		}
		m["meta"] = castMeta(res)
		if chk != nil {
			if _, ok := res.(*expression.ScalarFunction); ok {
				rows := make([]any, 0, chk.NumRows())
				for i := 0; i < chk.NumRows(); i++ {
					v := exTry(func() any { return castRowVal(ev, res, chk.GetRow(i)) })
					rows = append(rows, []any{v, ecWarns(ev.TruncateWarnings(0))})
				}
				m["rows"] = rows
				m["vec"] = exTry(func() any { return castVec(ev, res, chk) })
				ev.TruncateWarnings(0)
			}
		}
		return m, nil
	case "wrap":
		ctx, ev := castCtx(p[2])
		argE, _ := castArg(p[1], nil)
		m := map[string]any{}
		m["res"] = exTry(func() any {
			switch arg[string](p, 0) {
			case "int":
				return sfDump(expression.WrapWithCastAsInt(ctx, argE))
			case "real":
				return sfDump(expression.WrapWithCastAsReal(ctx, argE))
			default:
				return sfDump(expression.WrapWithCastAsDecimal(ctx, argE))
			}
		})
		m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
		return m, nil
	}
	return nil, fmt.Errorf("cast: unknown op %q", op)
}

// ---------------------------------------------------------------- generation

type castGen struct{ rng *rand.Rand }

func (g *castGen) pick(n int) int { return g.rng.IntN(n) }

func castFt(tp byte, flag uint, flen, dec int) *types.FieldType {
	ft := types.NewFieldTypeBuilder().SetType(tp).SetFlag(flag).SetFlen(flen).SetDecimal(dec).BuildP()
	if tp == mysql.TypeVarchar || tp == mysql.TypeVarString {
		ft.SetCharset(charset.CharsetUTF8MB4)
		ft.SetCollate(charset.CollationUTF8MB4)
	} else {
		types.SetBinChsClnFlag(ft)
	}
	return ft
}

// argFt: the type of a column argument.
func (g *castGen) argFt() *types.FieldType {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.NotNullFlag
	}
	switch g.pick(8) {
	case 0, 1:
		if g.pick(2) == 0 {
			flag |= mysql.UnsignedFlag
		}
		return castFt(mysql.TypeLonglong, flag, 20, 0)
	case 2:
		if g.pick(2) == 0 {
			flag |= mysql.UnsignedFlag
		}
		return castFt([]byte{mysql.TypeTiny, mysql.TypeLong, mysql.TypeInt24, mysql.TypeShort}[g.pick(4)], flag, 11, 0)
	case 3, 4:
		return castFt(mysql.TypeDouble, flag, []int{22, -1, 10}[g.pick(3)], []int{-1, 2, 31}[g.pick(3)])
	case 5, 6:
		p := 1 + g.pick(65)
		s := g.pick(min(p, 30) + 1)
		if g.pick(2) == 0 {
			flag |= mysql.UnsignedFlag
		}
		return castFt(mysql.TypeNewDecimal, flag, p, s)
	default:
		ft := castFt(mysql.TypeVarchar, flag, 64, 0)
		if g.pick(3) == 0 {
			ft.SetCharset(charset.CharsetBin)
			ft.SetCollate(charset.CollationBin)
			ft.AddFlag(mysql.BinaryFlag)
		}
		return ft
	}
}

var castStrs = []string{"0", "1", "-1", "123", " 42 ", "\t-7\n", "+5", "1.5", "-1.5", "2.5e3", "1e309", "-1e309",
	"abc", "12abc", "", " ", "-", ".5", "1.2.3", "9223372036854775807", "9223372036854775808",
	"18446744073709551615", "18446744073709551616", "-9223372036854775808", "-9223372036854775809",
	"0x1F", "1e-400", "00012", "-0", "１２", "3.14159265358979323846264338327950288419716939937510",
	"99999999999999999999999999999999999999999999999999999999999999999.5", "  12  34", "1,5"}

func (g *castGen) intVal(unsigned bool) []any {
	edges := []int64{0, 1, -1, 127, -128, 255, 256, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64, 99999}
	v := edges[g.pick(len(edges))]
	if g.pick(2) == 0 {
		v = g.rng.Int64() >> g.pick(64)
		if g.pick(2) == 0 {
			v = -v
		}
	}
	if unsigned {
		return []any{types.KindUint64, uint64(v)}
	}
	return []any{types.KindInt64, v}
}

func (g *castGen) realVal() []any {
	edges := []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1.5, 2.5, -2.5, 1e18, 9.223372036854775807e18,
		9.3e18, -9.3e18, 1.8446744073709552e19, 2e19, 1e300, -1e300, math.MaxFloat64, math.SmallestNonzeroFloat64,
		math.Inf(1), math.Inf(-1), 123.456, -0.0001, 1e-30, 99999.995, 0.125}
	f := edges[g.pick(len(edges))]
	if g.pick(2) == 0 {
		f = (g.rng.Float64() - 0.5) * math.Pow(10, float64(g.pick(40)-10))
	}
	return []any{types.KindFloat64, math.Float64bits(f)}
}

func (g *castGen) decVal() []any {
	n := 1 + g.pick(40)
	b := make([]byte, 0, n+3)
	if g.pick(3) == 0 {
		b = append(b, '-')
	}
	for i := 0; i < n; i++ {
		b = append(b, byte('0'+g.pick(10)))
	}
	if g.pick(2) == 0 {
		k := 1 + g.pick(len(b))
		if k < len(b) && b[k-1] != '-' {
			b = append(b[:k], append([]byte{'.'}, b[k:]...)...)
		}
	}
	return []any{types.KindMysqlDecimal, string(b)}
}

func (g *castGen) strVal(bin bool) []any {
	s := castStrs[g.pick(len(castStrs))]
	if g.pick(3) == 0 {
		s = strconv.FormatInt(g.rng.Int64()>>g.pick(64), 10)
		if g.pick(2) == 0 {
			s = "-" + s
		}
		if g.pick(3) == 0 {
			s += "." + strconv.Itoa(g.pick(1000))
		}
	}
	if bin {
		return []any{types.KindBytes, fmt.Sprintf("%x", s)}
	}
	return []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}
}

func (g *castGen) val(ft *types.FieldType) []any {
	if g.pick(8) == 0 {
		return []any{types.KindNull}
	}
	switch ft.EvalType() {
	case types.ETInt:
		return g.intVal(mysql.HasUnsignedFlag(ft.GetFlag()))
	case types.ETReal:
		return g.realVal()
	case types.ETDecimal:
		return g.decVal()
	}
	return g.strVal(ft.GetCharset() == charset.CharsetBin)
}

// target: the CAST type.
func (g *castGen) target() *types.FieldType {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.UnsignedFlag
	}
	if g.pick(4) == 0 {
		flag |= mysql.NotNullFlag
	}
	switch g.pick(3) {
	case 0:
		return castFt(mysql.TypeLonglong, flag, []int{20, 11, -1}[g.pick(3)], 0)
	case 1:
		switch g.pick(3) {
		case 0:
			return castFt(mysql.TypeDouble, flag, -1, -1)
		case 1:
			return castFt(mysql.TypeDouble, flag, 22, -1)
		default:
			return castFt(mysql.TypeDouble, flag, []int{10, 5, 30}[g.pick(3)], []int{2, 0, 4}[g.pick(3)])
		}
	default:
		fl := []int{-1, 10, 65, 5, 3, 2, 20, 1, 40}[g.pick(9)]
		dc := -1
		switch {
		case g.pick(12) == 0:
			dc = []int{2, 5, 30, 4}[g.pick(4)] // may exceed fl: ErrMBiggerThanD
		case fl > 0:
			dc = g.pick(min(fl, 30) + 1)
		case g.pick(2) == 0:
			dc = []int{0, 2, 30}[g.pick(3)]
		}
		return castFt(mysql.TypeNewDecimal, flag, fl, dc)
	}
}

func (g *castGen) ctx() any {
	lv := make([]int, len(errctx.LevelMap{}))
	for i := range lv {
		lv[i] = g.pick(3)
	}
	return []any{uint16(g.pick(8)), lv}
}

func (g *castGen) arg(withConst bool) (any, *types.FieldType) {
	ft := g.argFt()
	if withConst && g.pick(4) == 0 {
		return []any{"const", g.val(ft), sfFtIn(ft)}, ft
	}
	return []any{"col", sfFtIn(ft)}, ft
}

func castGenerate(vg *vec.Gen, rng *rand.Rand) {
	g := &castGen{rng: rng}
	for i := 0; i < 3000; i++ {
		a, ft := g.arg(true)
		n := 1 + g.pick(24)
		rows := make([]any, 0, n)
		for j := 0; j < n; j++ {
			rows = append(rows, g.val(ft))
		}
		vg.Add("sys", "cast", a, sfFtIn(g.target()), g.pick(4) == 0, g.ctx(), rows)
	}
	// UNION into an UNSIGNED type: negative strings, including "-" alone (the row and vector
	// versions of CAST(string AS DECIMAL) disagree on whether it is negative).
	for i := 0; i < 60; i++ {
		ft := castFt(mysql.TypeVarchar, 0, 64, 0)
		tp := g.target()
		tp.AddFlag(mysql.UnsignedFlag)
		rows := []any{}
		for _, s := range []string{"-", "-1", " -5 ", "-0", "-x", "7", "-1.5e3"} {
			rows = append(rows, []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4})
		}
		g.rng.Shuffle(len(rows), func(a, b int) { rows[a], rows[b] = rows[b], rows[a] })
		vg.Add("union-neg", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(tp), true, g.ctx(), rows)
	}
	for i := 0; i < 700; i++ {
		ft := g.argFt()
		a := []any{"col", sfFtIn(ft)}
		if g.pick(2) == 0 {
			a = []any{"const", g.val(ft), sfFtIn(ft)}
		}
		vg.Add("sys", "wrap", []string{"int", "real", "decimal"}[g.pick(3)], a, g.ctx())
	}
}
