package domains

// Domain "cast", block D08.8 part 1: CAST to INT, REAL and DECIMAL (builtin_cast.go), built
// with BuildCastFunctionWithCheck and evaluated both ways Go's executors do — row by row
// (EvalInt/EvalReal/EvalDecimal) and over a chunk (VecEvalXxx) — with the values, errors and
// warnings of each, under random type flags and error levels. Plus FoldConstant (D08.2) on a
// constant argument and PropagateType (D08.1) on DECIMAL -> REAL.
//
// The argument is a column (index 0) of an INT/BIGINT/TINYINT [UNSIGNED], DOUBLE, DECIMAL(p,s)
// or VARCHAR type with the rows of the record, or a constant. Records with source "conv"
// (appended in part 2) use FLOAT and hybrid (BIT, ENUM, SET) columns, which Go's
// Column.VecEvalInt/Real/String convert before the signature reads them.
//
// Part 2b-1 (appended): CAST to STRING ("str": INT/REAL/DECIMAL/STRING/FLOAT/hybrid
// arguments, CHAR/BINARY/VARCHAR targets of several charsets, explicit charsets, a small
// max_allowed_packet, strict SQL mode), FLOAT -> DOUBLE bit for bit ("dbl", Lucky's #84
// review), the internal to_binary/from_binary functions of HandleBinaryLiteral ("binlit"),
// BuildCastCollationFunction ("collcast") and WrapWithCastAsString ("wrap" string).
//
// Part 2b-2a (appended, govec/domains/cast2b2.go): CAST from DATE/DATETIME/TIMESTAMP, TIME,
// JSON and VECTOR columns ("from", "from-fsp").
//
// Records:
//   - "cast": [arg, tpIn, inUnion, ctx, rows, isExplicitCharset?] -> build (sfDump or error),
//     meta (inUnion of a baseBuiltinCastFunc), bwarns (warnings of building, i.e. of
//     folding), rows (row-mode value and warnings per row), vec (vector-mode values, or the
//     error, and warnings). arg = ["col", ftIn] | ["const", datumIn, ftIn];
//     ctx = [typeFlags, [7]errLevels, maxAllowedPacket?, sqlMode?].
//   - "wrap": [which, arg, ctx] -> WrapWithCastAsInt/Real/Decimal/String: sfDump, bwarns.
//   - "binlit": [arg, charset, collation, funcName, explicitCast, ctx, rows] ->
//     HandleBinaryLiteral: build, bwarns, rows, vec (as "cast").
//   - "collcast": [arg, charset, collation, enumOrSetRealTypeIsStr, ctx] ->
//     BuildCastCollationFunction: sfDump, bwarns.

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

var castStrSigs = []string{"IntAsString", "RealAsString", "DecimalAsString", "StringAsString"}

func castStrSigFuncs() []string {
	out := []string{}
	for _, s := range castStrSigs {
		out = append(out, "builtinCast"+s+"Sig.evalString", "builtinCast"+s+"Sig.vecEvalString",
			"builtinCast"+s+"Sig.vectorized", "builtinCast"+s+"Sig.Clone")
	}
	return out
}

var castCoverage = map[string]vec.Coverage{
	"cast": {Min: 2500, GoFuncs: append(append(castSigFuncs(), castStrSigFuncs()...),
		"castAsIntFunctionClass.getFunction", "castAsRealFunctionClass.getFunction",
		"castAsDecimalFunctionClass.getFunction", "BuildCastFunctionWithCheck", "BuildCastFunction",
		"TryPushCastIntoControlFunctionForHybridType", "setDataTypeDouble", "floatLength", "PropagateType",
		"newBaseBuiltinCastFunc", "baseBuiltinCastFunc.metadata", "baseBuiltinCastFunc.cloneFrom",
		"FoldConstant", "foldConstant",
		// "conv" records: the converting branches of the column's vector evaluation
		"Column.VecEvalInt", "Column.VecEvalReal", "Column.VecEvalString",
		// part 2b-1: CAST to STRING (and the from_binary it builds over a binary string)
		"castAsStringFunctionClass.getFunction", "newBaseBuiltinCastFunc4String", "adjustRetFtForCastString",
		"decimalPrecisionToLength", "padZeroForBinaryType", "newBaseBuiltinFuncWithTp")},
	"wrap": {Min: 600, GoFuncs: []string{"WrapWithCastAsInt", "WrapWithCastAsReal", "WrapWithCastAsDecimal",
		"minimalDecimalLenForHoldingInteger", "WrapWithCastAsString"}},
	"binlit": {Min: 400, GoFuncs: []string{"HandleBinaryLiteral", "isLegacyCharset", "init",
		"BuildToBinaryFunction", "BuildFromBinaryFunction", "tidbToBinaryFunctionClass.getFunction",
		"tidbFromBinaryFunctionClass.getFunction", "formatInvalidChars",
		"builtinInternalToBinarySig.evalString", "builtinInternalToBinarySig.vecEvalString",
		"builtinInternalToBinarySig.vectorized", "builtinInternalToBinarySig.Clone",
		"builtinInternalFromBinarySig.evalString", "builtinInternalFromBinarySig.vecEvalString",
		"builtinInternalFromBinarySig.vectorized", "builtinInternalFromBinarySig.Clone"}},
	"collcast": {Min: 300, GoFuncs: []string{"BuildCastCollationFunction"}},
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
	opts := []exprstatic.EvalCtxOption{
		exprstatic.WithTypeFlags(types.Flags(arg[uint16](a, 0))),
		exprstatic.WithErrLevelMap(lm),
		exprstatic.WithWarnHandler(contextutil.NewStaticWarnHandler(0)),
	}
	if len(a) > 2 {
		opts = append(opts, exprstatic.WithMaxAllowedPacket(arg[uint64](a, 2)),
			exprstatic.WithSQLMode(mysql.SQLMode(arg[uint64](a, 3))))
	}
	ev := exprstatic.NewEvalContext(opts...)
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
	case types.ETString:
		v, n, err := e.EvalString(ctx, row)
		if err != nil || n {
			return exVal(nil, n, err)
		}
		return exVal(vec.GoString(v), false, nil)
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
	case types.ETString:
		col = chunk.NewColumn(types.NewFieldType(mysql.TypeVarString), n)
		err = e.VecEvalString(ctx, chk, col)
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
		case types.ETString:
			vals = append(vals, vec.GoString(col.GetString(i)))
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
			explicit := len(p) > 5 && arg[bool](p, 5)
			r, err := expression.BuildCastFunctionWithCheck(ctx, argE, tp, arg[bool](p, 2), explicit)
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
			case "string":
				return sfDump(expression.WrapWithCastAsString(ctx, argE))
			default:
				return sfDump(expression.WrapWithCastAsDecimal(ctx, argE))
			}
		})
		m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
		return m, nil
	case "binlit":
		ctx, ev := castCtx(p[5])
		argE, chk := castArg(p[0], p[6])
		m := map[string]any{}
		var res expression.Expression
		m["build"] = exTry(func() any {
			ec := &expression.ExprCollation{Charset: arg[string](p, 1), Collation: arg[string](p, 2)}
			res = expression.HandleBinaryLiteral(ctx, argE, ec, arg[string](p, 3), arg[bool](p, 4))
			return sfDump(res)
		})
		m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
		if res != nil && chk != nil {
			if _, ok := res.(*expression.ScalarFunction); ok {
				castEvalRows(m, ev, res, chk)
			}
		}
		return m, nil
	case "collcast":
		ctx, ev := castCtx(p[4])
		argE, _ := castArg(p[0], nil)
		m := map[string]any{}
		m["res"] = exTry(func() any {
			ec := &expression.ExprCollation{Charset: arg[string](p, 1), Collation: arg[string](p, 2)}
			return sfDump(expression.BuildCastCollationFunction(ctx, argE, ec, arg[bool](p, 3)))
		})
		m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
		return m, nil
	}
	return nil, fmt.Errorf("cast: unknown op %q", op)
}

// castEvalRows: the row-mode values and warnings per row, then the vector-mode values.
func castEvalRows(m map[string]any, ev *exprstatic.EvalContext, res expression.Expression, chk *chunk.Chunk) {
	rows := make([]any, 0, chk.NumRows())
	for i := 0; i < chk.NumRows(); i++ {
		v := exTry(func() any { return castRowVal(ev, res, chk.GetRow(i)) })
		rows = append(rows, []any{v, ecWarns(ev.TruncateWarnings(0))})
	}
	m["rows"] = rows
	m["vec"] = exTry(func() any { return castVec(ev, res, chk) })
	ev.TruncateWarnings(0)
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
	// Part 2: FLOAT and hybrid columns (appended; the records above are unchanged).
	for i := 0; i < 800; i++ {
		ft, gen := g.convCol()
		n := 1 + g.pick(24)
		rows := make([]any, 0, n)
		for j := 0; j < n; j++ {
			if g.pick(8) == 0 {
				rows = append(rows, []any{types.KindNull})
			} else {
				rows = append(rows, gen())
			}
		}
		vg.Add("conv", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(g.target()), g.pick(4) == 0, g.ctx(), rows)
	}
	castGenerate2b1(vg, g)
	castGenerate2b2(vg, g)
}

// ---------------------------------------------------------------- part 2b-1

// castCharsets: [charset, collation] pairs of string targets and arguments.
var castCharsets = [][2]string{
	{charset.CharsetUTF8MB4, charset.CollationUTF8MB4},
	{charset.CharsetUTF8MB4, "utf8mb4_general_ci"},
	{charset.CharsetBin, charset.CollationBin},
	{charset.CharsetGBK, "gbk_chinese_ci"},
	{charset.CharsetLatin1, charset.CollationLatin1},
	{charset.CharsetASCII, charset.CollationASCII},
	{charset.CharsetUTF8, charset.CollationUTF8},
}

func (g *castGen) charset() [2]string { return castCharsets[g.pick(len(castCharsets))] }

// strTarget: a CAST(.. AS CHAR/BINARY/VARCHAR ..) type.
func (g *castGen) strTarget() *types.FieldType {
	var flag uint
	if g.pick(4) == 0 {
		flag |= mysql.NotNullFlag
	}
	cs := g.charset()
	tp := []byte{mysql.TypeVarString, mysql.TypeVarString, mysql.TypeString, mysql.TypeVarchar}[g.pick(4)]
	flen := []int{-1, -1, 0, 1, 3, 5, 8, 20, 64}[g.pick(9)]
	ft := types.NewFieldTypeBuilder().SetType(tp).SetFlag(flag).SetFlen(flen).SetDecimal(-1).BuildP()
	ft.SetCharset(cs[0])
	ft.SetCollate(cs[1])
	if cs[0] == charset.CharsetBin {
		ft.AddFlag(mysql.BinaryFlag)
	}
	return ft
}

// castStrsMB: strings for string columns of the charsets above (valid UTF-8 internally).
var castStrsMB = []string{"中文", "a中b", "é", "ü€", "𝄞", "\xff", "ÿ", "abc", "", " x ", "汉字汉字汉字", "ABCdef"}

// strColFt: a string column of a random charset.
func (g *castGen) strColFt() *types.FieldType {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.NotNullFlag
	}
	cs := g.charset()
	tp := []byte{mysql.TypeVarchar, mysql.TypeString, mysql.TypeVarString, mysql.TypeBlob, mysql.TypeTinyBlob,
		mysql.TypeMediumBlob, mysql.TypeLongBlob}[g.pick(7)]
	flen := []int{64, 10, 3, -1, 255}[g.pick(5)]
	ft := types.NewFieldTypeBuilder().SetType(tp).SetFlag(flag).SetFlen(flen).SetDecimal(0).BuildP()
	ft.SetCharset(cs[0])
	ft.SetCollate(cs[1])
	if cs[0] == charset.CharsetBin {
		ft.AddFlag(mysql.BinaryFlag)
	}
	return ft
}

// strColVal: a value of a string column (bytes for binary, possibly invalid in the target).
func (g *castGen) strColVal(ft *types.FieldType) []any {
	if g.pick(8) == 0 {
		return []any{types.KindNull}
	}
	var s string
	switch g.pick(3) {
	case 0:
		s = castStrs[g.pick(len(castStrs))]
	case 1:
		s = castStrsMB[g.pick(len(castStrsMB))]
	default:
		b := make([]byte, g.pick(9))
		for i := range b {
			b[i] = byte(g.rng.IntN(256))
		}
		s = string(b)
	}
	if ft.GetCharset() == charset.CharsetBin {
		return []any{types.KindBytes, fmt.Sprintf("%x", s)}
	}
	return []any{types.KindString, vec.GoString(s), ft.GetCollate()}
}

// strCtx: castCtx with max_allowed_packet and the SQL mode.
func (g *castGen) strCtx() any {
	c := g.ctx().([]any)
	packet := uint64(67108864)
	if g.pick(5) == 0 {
		packet = uint64([]int{0, 1, 4, 7}[g.pick(4)])
	}
	var mode mysql.SQLMode
	if g.pick(2) == 0 {
		mode = mysql.ModeStrictTransTables
	}
	return append(c, packet, uint64(mode))
}

func castGenerate2b1(vg *vec.Gen, g *castGen) {
	// FLOAT -> DOUBLE: the widening must keep every bit (Lucky, tidb-rs #84).
	f32s := []float32{0.1, 16777217, math.MaxFloat32, -math.MaxFloat32, math.SmallestNonzeroFloat32,
		1.1754942e-38, -1.4e-45, 3.4028235e38, 0.3, 1.0 / 3}
	for i := 0; i < 120; i++ {
		ft := castFt(mysql.TypeFloat, 0, []int{12, -1}[g.pick(2)], -1)
		n := 1 + g.pick(12)
		rows := make([]any, 0, n)
		for j := 0; j < n; j++ {
			f := f32s[g.pick(len(f32s))]
			if g.pick(3) == 0 {
				f = math.Float32frombits(g.rng.Uint32() & 0x7f7fffff)
				if g.pick(2) == 0 {
					f = -f
				}
			}
			rows = append(rows, []any{types.KindFloat32, math.Float64bits(float64(f))})
		}
		vg.Add("dbl", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(castFt(mysql.TypeDouble, 0, -1, -1)), false, g.ctx(), rows)
	}
	// CAST to STRING.
	for i := 0; i < 1600; i++ {
		var a any
		var gen func() []any
		switch g.pick(5) {
		case 0:
			ft, gf := g.convCol()
			a, gen = []any{"col", sfFtIn(ft)}, gf
		case 1, 2:
			ft := g.strColFt()
			a, gen = []any{"col", sfFtIn(ft)}, func() []any { return g.strColVal(ft) }
			if g.pick(5) == 0 {
				a, gen = []any{"const", g.strColVal(ft), sfFtIn(ft)}, nil
			}
		default:
			var ft *types.FieldType
			a, ft = g.arg(true)
			if ft.GetType() == mysql.TypeLonglong && g.pick(4) == 0 {
				ft = castFt(mysql.TypeYear, mysql.UnsignedFlag, 4, 0)
				a = []any{"col", sfFtIn(ft)}
			}
			if a.([]any)[0] == "col" {
				gen = func() []any { return g.val(ft) }
			}
		}
		var rows []any
		if gen != nil {
			n := 1 + g.pick(16)
			for j := 0; j < n; j++ {
				rows = append(rows, gen())
			}
		}
		vg.Add("str", "cast", a, sfFtIn(g.strTarget()), g.pick(8) == 0, g.strCtx(), rows, g.pick(4) == 0)
	}
	// HandleBinaryLiteral: to_binary / from_binary.
	funcs := []string{"hex", "hex", "length", "concat", "concat", "cast", "cast", "cast", "like", "lower", "bin", "no_such_func"}
	for i := 0; i < 500; i++ {
		ft := g.strColFt()
		if g.pick(3) == 0 {
			ft = castFt(mysql.TypeNull, 0, 0, 0)
		}
		cs := g.charset()
		n := 1 + g.pick(12)
		rows := make([]any, 0, n)
		for j := 0; j < n; j++ {
			if ft.GetType() == mysql.TypeNull {
				rows = append(rows, []any{types.KindNull})
			} else {
				rows = append(rows, g.strColVal(ft))
			}
		}
		vg.Add("binlit", "binlit", []any{"col", sfFtIn(ft)}, cs[0], cs[1], funcs[g.pick(len(funcs))],
			g.pick(2) == 0, g.strCtx(), rows)
	}
	// BuildCastCollationFunction.
	for i := 0; i < 300; i++ {
		var a any
		switch g.pick(3) {
		case 0:
			ft, _ := g.convCol()
			a = []any{"col", sfFtIn(ft)}
		case 1:
			a, _ = g.arg(true)
		default:
			a = []any{"col", sfFtIn(g.strColFt())}
		}
		cs := g.charset()
		vg.Add("collcast", "collcast", a, cs[0], cs[1], g.pick(2) == 0, g.ctx())
	}
	// WrapWithCastAsString.
	for i := 0; i < 300; i++ {
		var a any
		if g.pick(3) == 0 {
			ft, _ := g.convCol()
			a = []any{"col", sfFtIn(ft)}
		} else {
			a, _ = g.arg(true)
		}
		vg.Add("wrapstr", "wrap", "string", a, g.ctx())
	}
}

var castEnumElems = []string{"a", "b", "10", "-1", "1.5", " 7 ", "x9"}

// convCol: a FLOAT, BIT, ENUM or SET column type and a generator of its values.
func (g *castGen) convCol() (*types.FieldType, func() []any) {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.NotNullFlag
	}
	switch g.pick(4) {
	case 0:
		if g.pick(3) == 0 {
			flag |= mysql.UnsignedFlag
		}
		ft := castFt(mysql.TypeFloat, flag, []int{12, -1, 10}[g.pick(3)], []int{-1, 2, 31}[g.pick(3)])
		return ft, func() []any {
			edges := []float32{0, 0.5, -0.5, 1.5, 2.5, -2.5, 1e10, 3.4e38, -3.4e38, 1.1754944e-38, 16777217, 0.1, 123.456,
				float32(math.Inf(1)), float32(math.Inf(-1)), 9.2233720e18, 1.8446744e19}
			f := edges[g.pick(len(edges))]
			if g.pick(2) == 0 {
				f = float32((g.rng.Float64() - 0.5) * math.Pow(10, float64(g.pick(30)-8)))
			}
			return []any{types.KindFloat32, math.Float64bits(float64(f))}
		}
	case 1:
		bits := []int{1, 8, 16, 63, 64, 5}[g.pick(6)]
		ft := castFt(mysql.TypeBit, flag|mysql.UnsignedFlag, bits, 0)
		return ft, func() []any {
			nb := (bits + 7) / 8
			b := make([]byte, nb)
			v := g.rng.Uint64()
			if bits < 64 {
				v &= (uint64(1) << bits) - 1
			}
			if g.pick(4) == 0 {
				v = []uint64{0, 1}[g.pick(2)]
			}
			for k := nb - 1; k >= 0; k-- {
				b[k] = byte(v)
				v >>= 8
			}
			return []any{types.KindMysqlBit, fmt.Sprintf("%x", b)}
		}
	case 2:
		ft := castFt(mysql.TypeEnum, flag, -1, 0)
		ft.SetCharset(charset.CharsetUTF8MB4)
		ft.SetCollate(charset.CollationUTF8MB4)
		ft.SetElems(castEnumElems)
		return ft, func() []any {
			v := 1 + g.pick(len(castEnumElems))
			return []any{types.KindMysqlEnum, uint64(v), vec.GoString(castEnumElems[v-1]), charset.CollationUTF8MB4}
		}
	default:
		elems := []string{"a", "b", "1", "2"}
		ft := castFt(mysql.TypeSet, flag, -1, 0)
		ft.SetCharset(charset.CharsetUTF8MB4)
		ft.SetCollate(charset.CollationUTF8MB4)
		ft.SetElems(elems)
		return ft, func() []any {
			v := uint64(g.pick(16))
			name := ""
			for k, e := range elems {
				if v&(1<<k) != 0 {
					if name != "" {
						name += ","
					}
					name += e
				}
			}
			return []any{types.KindMysqlSet, v, vec.GoString(name), charset.CollationUTF8MB4}
		}
	}
}
