package domains

// Domain "expr", block D08.1 part 2: ScalarFunction (scalar_function.go), the
// baseBuiltinFunc fields it reads (builtin.go, D08.2), Column.EqualByExprAndID,
// resolveIndicesByVirtualExpr and GetUsedList.
//
// A tree is given as a recipe (leaves + function names) and built with NewFunctionBase
// (no constant folding), so Go chooses the real signatures. Each record returns the built
// tree ("tree": function name, signature type, result type, charset/collation, the sig's
// own vectorized flag where it is observable, arguments) for tidb-rs to rebuild with
// descriptor-only signatures, and the outputs of the plan-side methods.
//
// Records:
//   - "sf": args [recipe, schema uids, remap pairs] -> String, HashCode, CanonicalHashCode,
//     ConstLevel, IsCorrelated, Vectorized, MemoryUsage, GetSingleColumn, ResolveIndices,
//     ResolveIndicesByVirtualExpr, RemapColumn, Decorrelate, Clone/Equal.
//   - "sfpair": args [recipe a, recipe b] -> Equal, ExpressionsSemanticEqual.
//   - "vcol": args [[uid, recipe|null, ftIn]..., used uids, probe [uid, recipe|null, ftIn]] ->
//     GetUsedList, EqualByExprAndID, ResolveIndicesByVirtualExpr of a column.
//   - "sizes": the unsafe.Sizeof values MemoryUsage adds.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"sync"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
)

var exprSfCoverage = map[string]vec.Coverage{
	"sf": {Min: 1500, GoFuncs: []string{"ScalarFunction.String", "ScalarFunction.StringWithCtx",
		"ScalarFunction.HashCode", "ScalarFunction.CanonicalHashCode", "ScalarFunction.CleanHashCode",
		"ReHashCode", "simpleCanonicalizedHashCode", "ScalarFunction.ConstLevel", "ScalarFunction.IsCorrelated",
		"ScalarFunction.Vectorized", "ScalarFunction.MemoryUsage", "ScalarFunction.GetSingleColumn",
		"ScalarFunction.ResolveIndices", "ScalarFunction.resolveIndices", "ScalarFunction.ResolveIndicesByVirtualExpr",
		"ScalarFunction.resolveIndicesByVirtualExpr", "ScalarFunction.RemapColumn", "ScalarFunction.Decorrelate",
		"ScalarFunction.Clone", "ScalarFunction.Equal", "ScalarFunction.GetArgs", "ScalarFunction.GetType",
		"ScalarFunction.GetStaticType", "ScalarFunction.Traverse", "ScalarFunction.Coercibility",
		"ScalarFunction.HasCoercibility", "ScalarFunction.SetCoercibility", "ScalarFunction.CharsetAndCollation",
		"ScalarFunction.SetCharsetAndCollation", "ScalarFunction.Repertoire", "ScalarFunction.SetRepertoire",
		"ScalarFunction.IsExplicitCharset", "ScalarFunction.SetExplicitCharset", "ScalarFuncs2Exprs"}},
	"sfpair": {Min: 500, GoFuncs: []string{"ScalarFunction.Equal", "ExpressionsSemanticEqual"}},
	"vcol": {Min: 300, GoFuncs: []string{"GetUsedList", "Column.EqualByExprAndID", "Column.ResolveIndicesByVirtualExpr",
		"Column.resolveIndicesByVirtualExpr"}},
	"sizes": {Min: 1},
}

func sfBuildCtx() *exprstatic.ExprContext {
	return exprstatic.NewExprContext()
}

// ---------------------------------------------------------------- recipes

// A recipe node: {"col": [uid, index, ftIn, origName]}, {"corr": [uid, ftIn]},
// {"const": [datumIn, ftIn]}, {"f": [name, retFtIn|null, [args]]}.
type sfRecipe = map[string]json.RawMessage

func sfBuild(ctx expression.BuildContext, raw json.RawMessage) (expression.Expression, error) {
	var r sfRecipe
	must(json.Unmarshal(raw, &r))
	if p, ok := r["col"]; ok {
		a := exArr(p)
		return &expression.Column{UniqueID: arg[int64](a, 0), Index: arg[int](a, 1), RetType: d2FtIn(exArr(a[2])),
			OrigName: string(arg[vec.GoString](a, 3))}, nil
	}
	if p, ok := r["corr"]; ok {
		a := exArr(p)
		d := types.Datum{}
		return &expression.CorrelatedColumn{Column: expression.Column{UniqueID: arg[int64](a, 0), RetType: d2FtIn(exArr(a[1]))}, Data: &d}, nil
	}
	if p, ok := r["const"]; ok {
		a := exArr(p)
		return &expression.Constant{Value: d2DatumIn(exArr(a[0])), RetType: d2FtIn(exArr(a[1]))}, nil
	}
	a := exArr(r["f"])
	name := arg[string](a, 0)
	var rt *types.FieldType
	if string(a[1]) != "null" {
		rt = d2FtIn(exArr(a[1]))
	} else {
		rt = types.NewFieldType(mysql.TypeUnspecified)
	}
	var args []expression.Expression
	for _, c := range exArr(a[2]) {
		e, err := sfBuild(ctx, c)
		if err != nil {
			return nil, err
		}
		args = append(args, e)
	}
	return expression.NewFunctionBase(ctx, name, rt, args...)
}

func sfFtIn(ft *types.FieldType) any {
	var elems any
	if ft.GetElems() != nil {
		es := []vec.GoString{}
		for _, e := range ft.GetElems() {
			es = append(es, vec.GoString(e))
		}
		elems = es
	}
	return []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), ft.GetDecimal(), ft.GetCharset(), ft.GetCollate(), elems}
}

// sfDump: the built tree in the recipe format, functions with their signature.
func sfDump(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		var ve any
		if x.VirtualExpr != nil {
			ve = sfDump(x.VirtualExpr)
		}
		return map[string]any{"col": []any{x.UniqueID, x.Index, sfFtIn(x.RetType), vec.GoString(x.OrigName), ve}}
	case *expression.CorrelatedColumn:
		return map[string]any{"corr": []any{x.UniqueID, sfFtIn(x.RetType)}}
	case *expression.Constant:
		return map[string]any{"const": []any{d2DatumInOf(x.Value), sfFtIn(x.RetType)}}
	case *expression.ScalarFunction:
		args := []any{}
		allVec := true
		for _, a := range x.GetArgs() {
			args = append(args, sfDump(a))
			allVec = allVec && a.Vectorized()
		}
		// The signature's own vectorized() is observable only when the arguments are.
		sigVec := true
		if allVec {
			sigVec = x.Vectorized()
		}
		chs, coll := x.CharsetAndCollation()
		return map[string]any{"f": []any{x.FuncName.L, x.FuncName.O, reflect.TypeOf(x.Function).Elem().Name(),
			sfFtIn(x.RetType), args, sigVec, chs, coll, int(x.Function.PbCode()), x.HasCoercibility(),
			int(x.Coercibility()), int(x.Repertoire()), x.IsExplicitCharset()}}
	}
	panic("sfDump")
}

// d2DatumInOf: a datum in d2DatumIn's format (the kinds the recipes use).
func d2DatumInOf(d types.Datum) any {
	switch d.Kind() {
	case types.KindNull:
		return []any{d.Kind()}
	case types.KindInt64:
		return []any{d.Kind(), d.GetInt64()}
	case types.KindUint64:
		return []any{d.Kind(), d.GetUint64()}
	case types.KindFloat64:
		return []any{d.Kind(), math.Float64bits(d.GetFloat64())}
	case types.KindString:
		return []any{d.Kind(), vec.GoString(d.GetString()), d.Collation()}
	case types.KindMysqlDecimal:
		return []any{d.Kind(), d.GetMysqlDecimal().String()}
	case types.KindBytes:
		return []any{d.Kind(), hex.EncodeToString(d.GetBytes())}
	}
	panic(fmt.Sprintf("d2DatumInOf kind %d", d.Kind()))
}

func sfLeafIdx(e expression.Expression) any {
	out := []any{}
	var walk func(e expression.Expression)
	walk = func(e expression.Expression) {
		switch x := e.(type) {
		case *expression.Column:
			out = append(out, []any{x.UniqueID, x.Index})
		case *expression.ScalarFunction:
			for _, a := range x.GetArgs() {
				walk(a)
			}
		}
	}
	walk(e)
	return out
}

func sfSchema(raw json.RawMessage) *expression.Schema {
	cols := []*expression.Column{}
	for _, u := range arg[[]int64]([]json.RawMessage{raw}, 0) {
		cols = append(cols, &expression.Column{UniqueID: u})
	}
	return expression.NewSchema(cols...)
}

// ---------------------------------------------------------------- exec

func exprSfExec(op string, p []json.RawMessage) (out any, err error) {
	collate.SetNewCollationEnabledForTest(true)
	ctx := sfBuildCtx()
	ectx := ctx.GetEvalCtx()
	switch op {
	case "sizes":
		e, err := expression.NewFunctionBase(ctx, ast.Plus, types.NewFieldType(mysql.TypeLonglong),
			&expression.Column{UniqueID: 1, RetType: types.NewFieldType(mysql.TypeLonglong)}, expression.NewOne())
		if err != nil {
			return nil, err
		}
		sf := e.(*expression.ScalarFunction)
		return map[string]any{"sf": unsafe.Sizeof(expression.ScalarFunction{}),
			"base": reflect.TypeOf(sf.Function).Elem().Size(), "once": unsafe.Sizeof(sync.Once{})}, nil
	case "sf":
		e, err := sfBuild(ctx, p[0])
		if err != nil {
			return map[string]any{"build": exErr(err)}, nil
		}
		sf, ok := e.(*expression.ScalarFunction)
		if !ok {
			return map[string]any{"notsf": true}, nil
		}
		m := map[string]any{}
		// MemoryUsage first: Vectorized() creates the signatures' column pools (Go adds them).
		m["mem"] = sf.MemoryUsage()
		m["tree"] = sfDump(sf)
		m["str"] = vec.GoString(sf.String())
		m["hash"] = hex.EncodeToString(sf.HashCode())
		m["canon"] = hex.EncodeToString(sf.CanonicalHashCode())
		m["level"] = int(sf.ConstLevel())
		m["corr"] = sf.IsCorrelated()
		m["vec"] = sf.Vectorized()
		single := []any{}
		for _, rev := range []bool{false, true} {
			c, r := sf.GetSingleColumn(rev)
			if c == nil {
				single = append(single, []any{nil, r})
			} else {
				single = append(single, []any{c.UniqueID, r})
			}
		}
		m["single"] = single
		sch := sfSchema(p[1])
		m["resolve"] = exTry(func() any {
			r, err := sf.ResolveIndices(sch)
			if err != nil {
				return exErr(err)
			}
			return []any{sfLeafIdx(r), hex.EncodeToString(r.HashCode())}
		})
		m["vresolve"] = exTry(func() any {
			r, ok := sf.ResolveIndicesByVirtualExpr(ectx, sch)
			return []any{sfLeafIdx(r), ok}
		})
		remap := map[int64]*expression.Column{}
		for _, pr := range arg[[][2]int64](p, 2) {
			remap[pr[0]] = &expression.Column{UniqueID: pr[1], RetType: types.NewFieldType(mysql.TypeLonglong)}
		}
		m["remap"] = exTry(func() any {
			r, err := sf.RemapColumn(remap)
			if err != nil {
				return exErr(err)
			}
			return []any{vec.GoString(r.StringWithCtx(exprctx.EmptyParamValues, errors.RedactLogDisable)), hex.EncodeToString(r.HashCode())}
		})
		cl := sf.Clone()
		m["clone"] = []any{sf.Equal(ectx, cl), vec.GoString(cl.StringWithCtx(exprctx.EmptyParamValues, errors.RedactLogDisable)), hex.EncodeToString(cl.HashCode())}
		m["decor"] = exTry(func() any {
			d := sf.Clone().Decorrelate(sch)
			return []any{vec.GoString(d.StringWithCtx(exprctx.EmptyParamValues, errors.RedactLogDisable)), d.IsCorrelated(), hex.EncodeToString(d.HashCode())}
		})
		m["trav"] = sf.Traverse(nil2Action{}) == expression.Expression(sf)
		return m, nil
	case "sfpair":
		a, err := sfBuild(ctx, p[0])
		if err != nil {
			return map[string]any{"build": exErr(err)}, nil
		}
		b, err := sfBuild(ctx, p[1])
		if err != nil {
			return map[string]any{"build": exErr(err)}, nil
		}
		return map[string]any{"a": sfDump(a), "b": sfDump(b), "eq": a.Equal(ectx, b),
			"seq": expression.ExpressionsSemanticEqual(a, b)}, nil
	case "vcol":
		mk := func(raw json.RawMessage) (*expression.Column, error) {
			a := exArr(raw)
			c := &expression.Column{UniqueID: arg[int64](a, 0), RetType: d2FtIn(exArr(a[2]))}
			if string(a[1]) != "null" {
				ve, err := sfBuild(ctx, a[1])
				if err != nil {
					return nil, err
				}
				c.VirtualExpr = ve
			}
			return c, nil
		}
		cols := []*expression.Column{}
		for _, raw := range exArr(p[0]) {
			c, err := mk(raw)
			if err != nil {
				return map[string]any{"build": exErr(err)}, nil
			}
			cols = append(cols, c)
		}
		sch := expression.NewSchema(cols...)
		used := []*expression.Column{}
		for _, u := range arg[[]int](p, 1) {
			used = append(used, cols[u])
		}
		probe, err := mk(p[2])
		if err != nil {
			return map[string]any{"build": exErr(err)}, nil
		}
		dump := []any{}
		for _, c := range cols {
			dump = append(dump, sfDump(c))
		}
		eqs := []any{}
		for _, c := range cols {
			eqs = append(eqs, probe.EqualByExprAndID(ectx, c))
		}
		r, ok := probe.ResolveIndicesByVirtualExpr(ectx, sch)
		return map[string]any{"cols": dump, "probe": sfDump(probe), "used": expression.GetUsedList(ectx, used, sch),
			"eqid": eqs, "vres": []any{r.(*expression.Column).Index, ok}}, nil
	}
	return nil, fmt.Errorf("expr: unknown op %q", op)
}

// ---------------------------------------------------------------- generator

type sfGen struct{ rng *rand.Rand }

var sfIntFts = [][]any{
	{mysql.TypeLonglong, 0, 20, 0, "binary", "binary", nil},
	{mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0, "binary", "binary", nil},
	{mysql.TypeLong, mysql.NotNullFlag, 11, 0, "binary", "binary", nil},
}
var sfOtherFts = [][]any{
	{mysql.TypeDouble, 0, 22, -1, "binary", "binary", nil},
	{mysql.TypeNewDecimal, 0, 10, 2, "binary", "binary", nil},
	{mysql.TypeVarchar, 0, 20, 0, "utf8mb4", "utf8mb4_bin", nil},
	{mysql.TypeVarchar, 0, 20, 0, "utf8mb4", "utf8mb4_general_ci", nil},
	{mysql.TypeDatetime, 0, 19, 0, "binary", "binary", nil},
}

func (g *sfGen) ft() []any {
	if g.rng.IntN(2) == 0 {
		return sfIntFts[g.rng.IntN(len(sfIntFts))]
	}
	return sfOtherFts[g.rng.IntN(len(sfOtherFts))]
}

func (g *sfGen) leaf() any {
	switch g.rng.IntN(7) {
	case 0, 1, 2:
		return map[string]any{"col": []any{1 + g.rng.Int64N(6), g.rng.IntN(4), g.ft(), []string{"", "t.a", "b"}[g.rng.IntN(3)]}}
	case 3:
		return map[string]any{"corr": []any{1 + g.rng.Int64N(8), g.ft()}}
	case 4:
		return map[string]any{"const": []any{[]any{types.KindInt64, g.rng.Int64N(200) - 100}, sfIntFts[0]}}
	case 5:
		return map[string]any{"const": []any{[]any{types.KindString, vec.GoString([]string{"a", "x", ""}[g.rng.IntN(3)]), "utf8mb4_bin"},
			[]any{mysql.TypeVarString, 0, 1, 0, "utf8mb4", "utf8mb4_bin", nil}}}
	default:
		return map[string]any{"const": []any{[]any{types.KindNull}, []any{mysql.TypeNull, 0, 0, 0, "binary", "binary", nil}}}
	}
}

var sfFuncs = []struct {
	name  string
	arity int // -1: 2..4
}{
	{ast.Plus, 2}, {ast.Minus, 2}, {ast.Mul, 2}, {ast.UnaryMinus, 1}, {ast.EQ, 2}, {ast.NE, 2}, {ast.LT, 2},
	{ast.LE, 2}, {ast.GT, 2}, {ast.GE, 2}, {ast.LogicAnd, 2}, {ast.LogicOr, 2}, {ast.UnaryNot, 1},
	{ast.In, -1}, {ast.IsNull, 1}, {ast.Concat, -1}, {ast.Length, 1}, {ast.Upper, 1}, {ast.Coalesce, -1},
	{ast.If, 3}, {ast.Ifnull, 2}, {ast.Rand, 0}, {ast.Cast, 1}, {ast.Abs, 1}, {ast.Div, 2},
}

func (g *sfGen) tree(depth int) any {
	if depth == 0 || g.rng.IntN(4) == 0 {
		return g.leaf()
	}
	f := sfFuncs[g.rng.IntN(len(sfFuncs))]
	n := f.arity
	if n < 0 {
		n = 2 + g.rng.IntN(3)
	}
	args := []any{}
	for range n {
		args = append(args, g.tree(depth-1))
	}
	var rt any
	if f.name == ast.Cast {
		rt = g.ft()
	}
	return map[string]any{"f": []any{f.name, rt, args}}
}

// pair: b is a rewrite of a that Go's canonical hash code treats as equal (commuted
// arguments, a >= b <=> b <= a, NOT(a > b) <=> a <= b), or a random tree.
func (g *sfGen) pair() (any, any) {
	x, y := g.tree(2), g.tree(2)
	switch g.rng.IntN(7) {
	case 0:
		op := []string{ast.Plus, ast.Mul, ast.EQ, ast.LogicAnd, ast.LogicOr}[g.rng.IntN(5)]
		return map[string]any{"f": []any{op, nil, []any{x, y}}}, map[string]any{"f": []any{op, nil, []any{y, x}}}
	case 1:
		return map[string]any{"f": []any{ast.GE, nil, []any{x, y}}}, map[string]any{"f": []any{ast.LE, nil, []any{y, x}}}
	case 2:
		return map[string]any{"f": []any{ast.GT, nil, []any{x, y}}}, map[string]any{"f": []any{ast.LT, nil, []any{y, x}}}
	case 3:
		inner := []string{ast.GT, ast.LT, ast.GE, ast.LE}[g.rng.IntN(4)]
		neg := map[string]string{ast.GT: ast.LE, ast.LT: ast.GE, ast.GE: ast.LT, ast.LE: ast.GT}[inner]
		return map[string]any{"f": []any{ast.UnaryNot, nil, []any{map[string]any{"f": []any{inner, nil, []any{x, y}}}}}},
			map[string]any{"f": []any{neg, nil, []any{x, y}}}
	case 4:
		return x, x
	case 5:
		// Same name and arguments, other result type: Equal compares RetType.
		return map[string]any{"f": []any{ast.Cast, g.ft(), []any{x}}}, map[string]any{"f": []any{ast.Cast, g.ft(), []any{x}}}
	}
	return x, y
}

func exprSfGenerate(g *vec.Gen, rng *rand.Rand) {
	// Its own stream, seeded from the domain's (so -seed N varies these records too).
	sg := &sfGen{rng: rand.New(rand.NewPCG(rng.Uint64(), 2))}
	g.Add("sys/sizes", "sizes")
	for range 2000 {
		uids := []int64{}
		for u := int64(1); u <= 8; u++ {
			if sg.rng.IntN(2) == 0 {
				uids = append(uids, u)
			}
		}
		remap := [][2]int64{}
		for u := int64(1); u <= 6; u++ {
			if sg.rng.IntN(3) != 0 {
				remap = append(remap, [2]int64{u, 10 + u})
			}
		}
		g.Add("rand/sf", "sf", sg.tree(1+sg.rng.IntN(3)), uids, remap)
	}
	for range 700 {
		a, b := sg.pair()
		g.Add("rand/sfpair", "sfpair", a, b)
	}
	for range 400 {
		n := 1 + sg.rng.IntN(5)
		cols := []any{}
		shared := sg.tree(2)
		for i := range n {
			var ve any
			switch sg.rng.IntN(3) {
			case 0:
				ve = shared
			case 1:
				ve = sg.tree(2)
			}
			ft := sfIntFts[sg.rng.IntN(2)]
			cols = append(cols, []any{int64(1 + i + sg.rng.IntN(2)), ve, ft})
		}
		used := []int{}
		for i := range n {
			if sg.rng.IntN(2) == 0 {
				used = append(used, i)
			}
		}
		var pve any
		if sg.rng.IntN(2) == 0 {
			pve = shared
		}
		probe := []any{int64(1 + sg.rng.IntN(7)), pve, sfIntFts[sg.rng.IntN(2)]}
		g.Add("rand/vcol", "vcol", cols, used, probe)
	}
}
