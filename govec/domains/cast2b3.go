package domains

// Domain "cast", part 2b-3a (appended after part 2b-2b-ii; earlier records are unchanged):
//   - "withtps": [mode, funcName, args, retType, argTps, ctx, charset, collation] ->
//     newBaseBuiltinFuncWithTp (mode 0, argTps are eval types) or
//     newBaseBuiltinFuncWithFieldTypes (mode 1, argTps are field types): the built arguments
//     (with the CAST wrappers, from_binary/to_binary of HandleBinaryLiteral) and the
//     baseBuiltinFunc fields. The collation derivation runs through the cast-wrapping
//     constructors (Lucky, review of tidb-rs #80).
//   - "newfunc": [variant, funcName, retType, args, ctx, currentDB] -> NewFunction (0),
//     NewFunctionBase (1), NewFunctionTryFold (2), NewFunctionWithInit with a nil callback (3)
//     or NewFunctionInternal (4), for the names newFunctionImpl builds itself (cast,
//     to_binary, from_binary) and for unknown names: the result or the error, and the
//     warnings.
//   - "nullinfer": [args, ctx] -> typeInferForNull: the arguments afterwards.
// The arguments are castArg leaves (columns and constants of every type and charset, NULL
// constants).

import (
	"encoding/json"
	_ "unsafe" // go:linkname

	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
)

//go:linkname castNewBaseWithTp github.com/pingcap/tidb/pkg/expression.newBaseBuiltinFuncWithTp
func castNewBaseWithTp(ctx expression.BuildContext, funcName string, args []expression.Expression, retType types.EvalType, argTps ...types.EvalType) (fnBaseMirror, error)

//go:linkname castNewBaseWithFts github.com/pingcap/tidb/pkg/expression.newBaseBuiltinFuncWithFieldTypes
func castNewBaseWithFts(ctx expression.BuildContext, funcName string, args []expression.Expression, retType types.EvalType, argTps ...*types.FieldType) (fnBaseMirror, error)

//go:linkname castTypeInferForNull github.com/pingcap/tidb/pkg/expression.typeInferForNull
func castTypeInferForNull(ctx expression.EvalContext, args []expression.Expression)

func init() {
	castCoverage["withtps"] = vec.Coverage{Min: 1200, GoFuncs: []string{"newBaseBuiltinFuncWithFieldTypes", "newBaseBuiltinFuncWithTp"}}
	castCoverage["newfunc"] = vec.Coverage{Min: 600, GoFuncs: []string{"NewFunction", "NewFunctionBase", "NewFunctionTryFold",
		"NewFunctionWithInit", "NewFunctionInternal", "newFunctionImpl", "defaultScalarFunctionCheck"}}
	castCoverage["nullinfer"] = vec.Coverage{Min: 300, GoFuncs: []string{"typeInferForNull"}}
}

// b3Leaf: a leaf argument recipe and its type: numeric, string (every charset), time,
// JSON columns, constants, NULL constants.
func (g *castGen) b3Leaf() (any, *types.FieldType) {
	switch g.pick(8) {
	case 0, 1:
		return g.arg(true)
	case 2, 3:
		ft := g.strColFt()
		if g.pick(3) == 0 {
			return []any{"const", g.strColVal(ft), sfFtIn(ft)}, ft
		}
		return []any{"col", sfFtIn(ft)}, ft
	case 4:
		ft, _ := g.toArg()
		return []any{"col", sfFtIn(ft)}, ft
	case 5:
		ft, _ := g.toJSONArg()
		return []any{"col", sfFtIn(ft)}, ft
	default:
		ft := types.NewFieldType(mysql.TypeNull)
		return []any{"const", []any{types.KindNull}, sfFtIn(ft)}, ft
	}
}

// b3ArgFt: a declared argument field type for newBaseBuiltinFuncWithFieldTypes: the
// argument's own type (no CAST for DECIMAL/time) or another one.
func (g *castGen) b3ArgFt(own *types.FieldType) *types.FieldType {
	if g.pick(3) == 0 {
		return own.Clone()
	}
	switch g.pick(9) {
	case 0:
		return castFt(mysql.TypeNewDecimal, 0, 1+g.pick(30), g.pick(10))
	case 1:
		return castFt(mysql.TypeDatetime, 0, 19, g.pick(7))
	case 2:
		return castFt(mysql.TypeTimestamp, 0, 19, g.pick(7))
	case 3:
		return castFt(mysql.TypeDuration, 0, 10, g.pick(7))
	case 4:
		return castFt(mysql.TypeDate, 0, 10, 0)
	case 5:
		return castFt(mysql.TypeLonglong, 0, 20, 0)
	case 6:
		return castFt(mysql.TypeDouble, 0, -1, -1)
	case 7:
		return castFt(mysql.TypeVarchar, 0, 64, 0)
	default:
		return castFt(mysql.TypeJSON, mysql.BinaryFlag, -1, 0)
	}
}

var b3EvalTypes = []types.EvalType{types.ETInt, types.ETReal, types.ETDecimal, types.ETString, types.ETDatetime,
	types.ETTimestamp, types.ETDuration, types.ETJson}

func castGenerate2b3(vg *vec.Gen, g *castGen) {
	names := append(append([]string{}, fnDeriveNames...), ast.Concat, ast.Like, ast.Strcmp, ast.Replace)
	for i := 0; i < 1300; i++ {
		mode := g.pick(2)
		n := 1 + g.pick(4)
		args, tps := []any{}, []any{}
		for range n {
			a, ft := g.b3Leaf()
			args = append(args, a)
			if mode == 0 {
				if g.pick(2) == 0 {
					tps = append(tps, int(types.ETString))
				} else {
					tps = append(tps, int(b3EvalTypes[g.pick(len(b3EvalTypes))]))
				}
			} else {
				tps = append(tps, sfFtIn(g.b3ArgFt(ft)))
			}
		}
		cs := castCharsets[g.pick(len(castCharsets))]
		ret := int(b3EvalTypes[g.pick(len(b3EvalTypes))])
		if g.pick(3) == 0 {
			ret = int(types.ETString)
		}
		vg.Add("withtps", "withtps", mode, names[g.pick(len(names))], args, ret, tps, g.strCtx(), cs[0], cs[1])
	}

	targets := func() *types.FieldType {
		switch g.pick(6) {
		case 0:
			return g.target()
		case 1:
			return g.strTarget()
		case 2:
			return g.timeTarget()
		case 3:
			return g.jsonTarget()
		case 4:
			return types.NewFieldType(mysql.TypeUnspecified)
		default:
			return castFt(mysql.TypeLonglong, 0, 20, 0)
		}
	}
	for i := 0; i < 700; {
		variant := g.pick(5)
		name := []string{ast.Cast, ast.Cast, ast.Cast, expression.InternalFuncToBinary, expression.InternalFuncFromBinary,
			"no_such_function"}[g.pick(6)]
		a, _ := g.b3Leaf()
		if name == expression.InternalFuncFromBinary {
			ft := g.strColFt()
			a = []any{"col", sfFtIn(ft)}
			if g.pick(2) == 0 {
				a = []any{"const", g.strColVal(ft), sfFtIn(ft)}
			}
		}
		tp := targets()
		if name == expression.InternalFuncFromBinary {
			tp = g.strTarget()
		}
		db := []string{"", "test"}[g.pick(2)]
		c := g.strCtx()
		if name == ast.Cast && !b3CastBuilds(a, tp, c) {
			continue // Go logs the error and returns a node without a signature
		}
		i++
		vg.Add("newfunc", "newfunc", variant, name, sfFtIn(tp), []any{a}, c, db)
	}

	for range 300 {
		n := 2 + g.pick(4)
		args := []any{}
		for range n {
			a, _ := g.b3Leaf()
			if g.pick(3) == 0 {
				ft := types.NewFieldType(mysql.TypeNull)
				if g.pick(3) == 0 {
					ft.AddFlag(mysql.NotNullFlag)
				}
				a = []any{"const", []any{types.KindNull}, sfFtIn(ft)}
			}
			args = append(args, a)
		}
		vg.Add("nullinfer", "nullinfer", args, g.ctx())
	}
}

func b3CastBuilds(a any, tp *types.FieldType, c any) bool {
	ctx, _ := castCtx(vec.MustJSON(c))
	argE, _ := castArg(vec.MustJSON(a), nil)
	_, err := expression.BuildCastFunctionWithCheck(ctx, argE, tp, false, false)
	return err == nil
}

func b3Ctx(raw json.RawMessage, cs, coll, db string) (*exprstatic.ExprContext, *exprstatic.EvalContext) {
	_, ev := castCtx(raw, exprstatic.WithCurrentDB(db))
	opts := []exprstatic.ExprCtxOption{exprstatic.WithEvalCtx(ev)}
	if cs != "" {
		opts = append(opts, exprstatic.WithCharset(cs, coll))
	}
	return exprstatic.NewExprContext(opts...), ev
}

func castExec2b3(op string, p []json.RawMessage) any {
	switch op {
	case "withtps":
		ctx, ev := b3Ctx(p[5], arg[string](p, 6), arg[string](p, 7), "")
		args := []expression.Expression{}
		for _, a := range exArr(p[2]) {
			e, _ := castArg(a, nil)
			args = append(args, e)
		}
		name := arg[string](p, 1)
		ret := types.EvalType(arg[int](p, 3))
		m := map[string]any{}
		m["bf"] = exTry(func() any {
			var bf fnBaseMirror
			var err error
			if arg[int](p, 0) == 0 {
				ets := []types.EvalType{}
				for _, e := range arg[[]int](p, 4) {
					ets = append(ets, types.EvalType(e))
				}
				bf, err = castNewBaseWithTp(ctx, name, args, ret, ets...)
			} else {
				fts := []*types.FieldType{}
				for _, f := range exArr(p[4]) {
					fts = append(fts, d2FtIn(exArr(f)))
				}
				bf, err = castNewBaseWithFts(ctx, name, args, ret, fts...)
			}
			if err != nil {
				return exErr(err)
			}
			out := fnBaseOut(bf).(map[string]any)
			out["args"] = fnDumps(bf.args)
			return out
		})
		m["bwarns"] = ecWarns(ev.TruncateWarnings(0))
		return m
	case "newfunc":
		ctx, ev := b3Ctx(p[4], "", "", arg[string](p, 5))
		args := []expression.Expression{}
		for _, a := range exArr(p[3]) {
			e, _ := castArg(a, nil)
			args = append(args, e)
		}
		name := arg[string](p, 1)
		tp := d2FtIn(exArr(p[2]))
		m := map[string]any{}
		m["res"] = exTry(func() any {
			var e expression.Expression
			var err error
			switch arg[int](p, 0) {
			case 0:
				e, err = expression.NewFunction(ctx, name, tp, args...)
			case 1:
				e, err = expression.NewFunctionBase(ctx, name, tp, args...)
			case 2:
				e, err = expression.NewFunctionTryFold(ctx, name, tp, args...)
			case 3:
				e, err = expression.NewFunctionWithInit(ctx, name, tp, nil, args...)
			default:
				e = expression.NewFunctionInternal(ctx, name, tp, args...)
			}
			if err != nil {
				return exErr(err)
			}
			if e == nil {
				return nil
			}
			return sfDump(e)
		})
		m["warns"] = ecWarns(ev.TruncateWarnings(0))
		return m
	case "nullinfer":
		_, ev := castCtx(p[1])
		args := []expression.Expression{}
		for _, a := range exArr(p[0]) {
			e, _ := castArg(a, nil)
			args = append(args, e)
		}
		castTypeInferForNull(ev, args)
		return fnDumps(args)
	}
	return nil
}

var (
	_ = charset.CharsetBin
	_ = chunk.NewChunkWithCapacity
	_ = collate.NewCollationEnabled
)
