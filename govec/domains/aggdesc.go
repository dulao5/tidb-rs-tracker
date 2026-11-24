package domains

// Domain "aggdesc", block D08.11 parts A and B: the aggregate function descriptors of
// pkg/expression/aggregation (tidb-rs specs/D08.11-aggregation §7) and the ByItems they hold
// (pkg/planner/util/byitem.go, moved to D08.11 with them).
//
// A descriptor is given as a spec and built with NewAggFuncDesc (type inference included) or,
// with "raw", from its fields as the planner and PBExprToAggFuncDesc build it:
//
//	{"n": name, "a": [recipe...], "d": hasDistinct, "m": mode, "ob": [[recipe, desc]...],
//	 "raw": bool, "rt": ftIn|null (raw only), "nc": new collation, "dpi": div_precision_increment
//	 (0: the default)}
//
// A recipe is expr_sf.go's ({"col": [uid, index, ftIn, origName]}, {"const": [datumIn, ftIn]},
// {"f": [name, retFtIn|null, [args]]}, built with NewFunctionBase), a column may carry
// "coer" (its coercibility), and {"castf": [ftIn, recipe]} is BuildCastFunction (a CAST
// scalar function of any type, e.g. FLOAT). A descriptor is dumped as
// {"n", "a": [arg...], "rt": ftOut|null, "m", "d", "ob": [[arg, desc]...], "g": GroupingID}, an
// argument as ["col", uid, index, ft], ["const", datumOut, ft] or ["f", name, ft, [args]].
//
// Records (part A, replayed by tidb-rs crates/expression/tests/govec_aggdesc.rs):
//   - "new": [spec] -> NewAggFuncDesc (newBaseFuncDesc, TypeInfer and every typeInfer4*): the
//     descriptor and its StringWithCtx, or the error.
//   - "wrap": [spec] -> WrapCastForAggArgs.
//   - "notnull": [spec, hasGroupBy, allAggsFirstRow] -> UpdateNotNullFlag4RetType.
//   - "default": [spec] -> GetDefaultValue.
//   - "outer": [spec, schema uids] -> EvalNullValueInOuterJoin.
//   - "split": [spec, ordinals] -> Split (the original, partial and final descriptors).
//   - "avgsum": [retFtIn, avgFtIn] -> TypeInfer4AvgSum; "finalcount": [ftIn] -> TypeInfer4FinalCount.
//   - "string": [spec, redact] -> AggFuncDesc / baseFuncDesc StringWithCtx, ByItems.StringWithCtx,
//     StringifyByItemsWithCtx.
//   - "equal": [spec a, spec b] -> AggFuncDesc.Equal both ways, of a clone, ByItems.Equal.
//   - "mem": [spec] -> AggFuncDesc / baseFuncDesc / ByItems MemoryUsage.
//   - "names": [name] -> NeedCount, NeedValue; "mode": [mode] -> AggFunctionMode.ToString;
//     "allfirst": [[name...]] -> IsAllFirstRow.
//   - "window": [spec] -> NewAggFuncDescForWindowFunc of a WindowFuncDesc built from the spec's
//     fields (rt null: the safety check's NewAggFuncDesc).
//
// Records (part B, replayed by tidb-rs crates/expr-pushdown/tests/govec_aggdesc_pb.rs):
//   - "push": [spec, blacklist] -> CheckAggPushDown for TiKV, TiFlash, TiDB, UnSpecified and
//     CheckAggPushFlash.
//   - "topb": [spec, client mode, group_concat_max_len, storeType] -> AggFuncToPBExpr
//     (marshalled bytes) or the error; the client is pushdown.go's pdClient.
//   - "mode2pb": [mode] -> AggFunctionModeToPB; "pb2mode": [mode|null] -> PBAggFuncModeToAggFuncMode.
//   - "frompb": [pb hex, [ftIn...], nc] -> PBExprToAggFuncDesc.
//   - "tipb": [name, tryWindowDesc] -> baseFuncDesc.GetTiPBExpr.
//
// The context is exprstatic's with the current time fixed (a constant cast to a time type
// folds with today's date).

import (
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/aggregation"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tipb/go-tipb"
)

const aggdescSeed = 20261124

func init() {
	vec.Register(&vec.Domain{
		Name:     "aggdesc",
		Seed:     aggdescSeed,
		Coverage: aggdescCoverage,
		Blocks:   []string{"D08.11"},
		Exempt:   aggdescExempt(),
		Gzip:     true,
		Generate: aggdescGenerate,
		Exec:     aggdescExec,
	})
}

var aggdescCoverage = map[string]vec.Coverage{
	"new": {Min: 1500, GoFuncs: []string{"NewAggFuncDesc", "newBaseFuncDesc", "baseFuncDesc.TypeInfer",
		"baseFuncDesc.typeInfer4Count", "baseFuncDesc.typeInfer4ApproxCountDistinct",
		"baseFuncDesc.typeInfer4ApproxPercentile", "baseFuncDesc.typeInfer4Sum", "baseFuncDesc.typeInfer4Avg",
		"baseFuncDesc.typeInfer4GroupConcat", "baseFuncDesc.typeInfer4MaxMin", "baseFuncDesc.typeInfer4BitFuncs",
		"baseFuncDesc.typeInfer4JsonArrayAgg", "baseFuncDesc.typeInfer4JsonObjectAgg",
		"baseFuncDesc.typeInfer4NumberFuncs", "baseFuncDesc.typeInfer4CumeDist", "baseFuncDesc.typeInfer4Ntile",
		"baseFuncDesc.typeInfer4PercentRank", "baseFuncDesc.typeInfer4LeadLag", "baseFuncDesc.typeInfer4PopOrSamp",
		"AggFuncDesc.StringWithCtx"}},
	"wrap":       {Min: 400, GoFuncs: []string{"baseFuncDesc.WrapCastForAggArgs"}},
	"notnull":    {Min: 200, GoFuncs: []string{"AggFuncDesc.UpdateNotNullFlag4RetType"}},
	"default":    {Min: 100, GoFuncs: []string{"baseFuncDesc.GetDefaultValue"}},
	"outer":      {Min: 200, GoFuncs: []string{"AggFuncDesc.EvalNullValueInOuterJoin", "AggFuncDesc.evalNullValueInOuterJoin4Count", "AggFuncDesc.evalNullValueInOuterJoin4Sum", "AggFuncDesc.evalNullValueInOuterJoin4BitAnd", "AggFuncDesc.evalNullValueInOuterJoin4BitOr"}},
	"split":      {Min: 150, GoFuncs: []string{"AggFuncDesc.Split", "AggFuncDesc.Clone", "baseFuncDesc.clone", "ByItems.Clone"}},
	"avgsum":     {Min: 30, GoFuncs: []string{"baseFuncDesc.TypeInfer4AvgSum"}},
	"finalcount": {Min: 10, GoFuncs: []string{"baseFuncDesc.TypeInfer4FinalCount"}},
	"string": {Min: 150, GoFuncs: []string{"AggFuncDesc.StringWithCtx", "baseFuncDesc.StringWithCtx",
		"ByItems.StringWithCtx", "StringifyByItemsWithCtx"}},
	"equal": {Min: 150, GoFuncs: []string{"AggFuncDesc.Equal", "baseFuncDesc.equal", "AggFuncDesc.Clone",
		"baseFuncDesc.clone", "ByItems.Equal", "ByItems.Clone"}},
	"mem":      {Min: 100, GoFuncs: []string{"AggFuncDesc.MemoryUsage", "baseFuncDesc.MemoryUsage", "ByItems.MemoryUsage"}},
	"names":    {Min: 20, GoFuncs: []string{"NeedCount", "NeedValue"}},
	"mode":     {Min: 5, GoFuncs: []string{"AggFunctionMode.ToString"}},
	"allfirst": {Min: 10, GoFuncs: []string{"IsAllFirstRow"}},
	"window":   {Min: 40, GoFuncs: []string{"NewAggFuncDescForWindowFunc"}},
	"push":     {Min: 300, GoFuncs: []string{"CheckAggPushDown", "checkVectorAggPushDown", "CheckAggPushFlash"}},
	"topb":     {Min: 300, GoFuncs: []string{"AggFuncToPBExpr", "AggFunctionModeToPB", "baseFuncDesc.GetTiPBExpr"}},
	"mode2pb":  {Min: 5, GoFuncs: []string{"AggFunctionModeToPB"}},
	"pb2mode":  {Min: 6, GoFuncs: []string{"PBAggFuncModeToAggFuncMode"}},
	"frompb":   {Min: 140, GoFuncs: []string{"PBExprToAggFuncDesc", "PBAggFuncModeToAggFuncMode", "baseFuncDesc.WrapCastForAggArgs"}},
	"tipb":     {Min: 40, GoFuncs: []string{"baseFuncDesc.GetTiPBExpr"}},
}

func aggdescExempt() map[string]string {
	const cascades = "EXC.B: cascades base.HashEquals (D08.1 spec §4 item 2)"
	const partC = "window function descriptors, part C of the block (D08.11 spec §2.1): a later task"
	return map[string]string{
		"baseFuncDesc.Hash64":                 cascades,
		"baseFuncDesc.Equals":                 cascades,
		"AggFuncDesc.Hash64":                  cascades,
		"AggFuncDesc.Equals":                  cascades,
		"ByItems.Hash64":                      cascades,
		"ByItems.Equals":                      cascades,
		"NewWindowFuncDesc":                   partC,
		"UseDefaultFrame":                     partC,
		"NeedFrame":                           partC,
		"WindowFuncDesc.Clone":                partC,
		"WindowFuncToPBExpr":                  partC,
		"WindowFuncDesc.CanPushDownToTiFlash": partC,
		"ExplainAggFunc":                      "EXPLAIN text, part D of the block (D08.11 spec §2.1): after the expressions' ExplainInfo (D10.9 tasks P1-1)",
	}
}

// ---------------------------------------------------------------- specs

type adSpecT struct {
	N   string            `json:"n"`
	A   []json.RawMessage `json:"a"`
	D   bool              `json:"d"`
	M   int               `json:"m"`
	OB  []json.RawMessage `json:"ob"`
	Raw bool              `json:"raw"`
	RT  json.RawMessage   `json:"rt"`
	NC  bool              `json:"nc"`
	DPI int               `json:"dpi"`
}

func adSpec(raw json.RawMessage) adSpecT {
	var s adSpecT
	must(json.Unmarshal(raw, &s))
	return s
}

// adCtx: the build context of a spec (the time fixed, as pdBuildCtx).
func adCtx(s adSpecT) *exprstatic.ExprContext {
	opts := []exprstatic.EvalCtxOption{
		exprstatic.WithCurrentTime(func() (gotime.Time, error) { return gotime.Unix(1767225600, 0), nil })}
	if s.DPI > 0 {
		opts = append(opts, exprstatic.WithDivPrecisionIncrement(s.DPI))
	}
	return exprstatic.NewExprContext(exprstatic.WithEvalCtx(exprstatic.NewEvalContext(opts...)))
}

func adBuild(ctx expression.BuildContext, raw json.RawMessage) expression.Expression {
	var r sfRecipe
	must(json.Unmarshal(raw, &r))
	if p, ok := r["col"]; ok {
		a := exArr(p)
		c := &expression.Column{UniqueID: arg[int64](a, 0), ID: arg[int64](a, 0), Index: arg[int](a, 1),
			RetType: d2FtIn(exArr(a[2])), OrigName: string(arg[vec.GoString](a, 3))}
		if co, ok := r["coer"]; ok {
			var n int
			must(json.Unmarshal(co, &n))
			c.SetCoercibility(expression.Coercibility(n))
		}
		return c
	}
	if p, ok := r["const"]; ok {
		a := exArr(p)
		return &expression.Constant{Value: d2DatumIn(exArr(a[0])), RetType: d2FtIn(exArr(a[1]))}
	}
	if p, ok := r["castf"]; ok {
		a := exArr(p)
		return expression.BuildCastFunction(ctx, adBuild(ctx, a[1]), d2FtIn(exArr(a[0])))
	}
	a := exArr(r["f"])
	rt := types.NewFieldType(mysql.TypeUnspecified)
	if string(a[1]) != "null" {
		rt = d2FtIn(exArr(a[1]))
	}
	var args []expression.Expression
	for _, c := range exArr(a[2]) {
		args = append(args, adBuild(ctx, c))
	}
	e, err := expression.NewFunctionBase(ctx, arg[string](a, 0), rt, args...)
	must(err)
	return e
}

func adArgs(ctx expression.BuildContext, s adSpecT) []expression.Expression {
	args := []expression.Expression{}
	for _, r := range s.A {
		args = append(args, adBuild(ctx, r))
	}
	return args
}

// adDesc builds the descriptor of a spec.
func adDesc(ctx expression.BuildContext, s adSpecT) (*aggregation.AggFuncDesc, error) {
	collate.SetNewCollationEnabledForTest(s.NC)
	args := adArgs(ctx, s)
	var d *aggregation.AggFuncDesc
	if s.Raw {
		d = &aggregation.AggFuncDesc{HasDistinct: s.D}
		d.Name = s.N
		d.Args = args
		if string(s.RT) != "null" && len(s.RT) > 0 {
			d.RetTp = d2FtIn(exArr(s.RT))
		}
	} else {
		var err error
		if d, err = aggregation.NewAggFuncDesc(ctx, s.N, args, s.D); err != nil {
			return nil, err
		}
	}
	d.Mode = aggregation.AggFunctionMode(s.M)
	for _, r := range s.OB {
		p := exArr(r)
		d.OrderByItems = append(d.OrderByItems, &util.ByItems{Expr: adBuild(ctx, p[0]), Desc: arg[bool](p, 1)})
	}
	return d, nil
}

func adArg(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		return []any{"col", x.UniqueID, x.Index, sfFtIn(x.RetType)}
	case *expression.Constant:
		return []any{"const", d2DatumInOf(x.Value), sfFtIn(x.RetType)}
	case *expression.ScalarFunction:
		args := []any{}
		for _, a := range x.GetArgs() {
			args = append(args, adArg(a))
		}
		return []any{"f", x.FuncName.L, sfFtIn(x.RetType), args}
	}
	panic("adArg")
}

func adDump(d *aggregation.AggFuncDesc) any {
	args := []any{}
	for _, a := range d.Args {
		args = append(args, adArg(a))
	}
	ob := []any{}
	for _, it := range d.OrderByItems {
		ob = append(ob, []any{adArg(it.Expr), it.Desc})
	}
	var rt any
	if d.RetTp != nil {
		rt = sfFtIn(d.RetTp)
	}
	return map[string]any{"n": vec.GoString(d.Name), "a": args, "rt": rt, "m": int(d.Mode), "d": d.HasDistinct,
		"ob": ob, "g": d.GroupingID}
}

// adWindow: a WindowFuncDesc with the descriptor's fields (its StringWithCtx and MemoryUsage
// are baseFuncDesc's).
func adWindow(d *aggregation.AggFuncDesc) *aggregation.WindowFuncDesc {
	w := &aggregation.WindowFuncDesc{}
	w.Name, w.Args, w.RetTp = d.Name, d.Args, d.RetTp
	return w
}

// ---------------------------------------------------------------- exec

func aggdescExec(op string, p []json.RawMessage) (out any, err error) {
	defer expression.DefaultExprPushDownBlacklist.Store(make(map[string]uint32))
	defer collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "new", "wrap", "notnull", "default", "outer", "split", "string", "mem", "push", "topb":
		s := adSpec(p[0])
		ctx := adCtx(s)
		ectx := ctx.GetEvalCtx()
		d, err := adDesc(ctx, s)
		if err != nil {
			return exErr(err), nil
		}
		switch op {
		case "new":
			m := adDump(d).(map[string]any)
			m["s"] = vec.GoString(d.StringWithCtx(ectx, errors.RedactLogDisable))
			return m, nil
		case "wrap":
			d.WrapCastForAggArgs(ctx)
			return adDump(d), nil
		case "notnull":
			if err := d.UpdateNotNullFlag4RetType(arg[bool](p, 1), arg[bool](p, 2)); err != nil {
				return exErr(err), nil
			}
			return adDump(d), nil
		case "default":
			return d2DatumInOf(d.GetDefaultValue()), nil
		case "outer":
			v, ok, err := d.EvalNullValueInOuterJoin(ctx, sfSchema(p[1]))
			if err != nil {
				return exErr(err), nil
			}
			return []any{d2DatumInOf(v), ok}, nil
		case "split":
			partial, final := d.Split(arg[[]int](p, 1))
			return []any{adDump(d), adDump(partial), adDump(final)}, nil
		case "string":
			redact := arg[string](p, 1)
			items := []any{}
			for _, it := range d.OrderByItems {
				items = append(items, vec.GoString(it.StringWithCtx(ectx, redact)))
			}
			return map[string]any{"agg": vec.GoString(d.StringWithCtx(ectx, redact)),
				"base":  vec.GoString(adWindow(d).StringWithCtx(ectx, redact)),
				"items": items, "all": vec.GoString(util.StringifyByItemsWithCtx(ectx, d.OrderByItems))}, nil
		case "mem":
			items := []any{}
			for _, it := range d.OrderByItems {
				items = append(items, it.MemoryUsage())
			}
			return []any{d.MemoryUsage(), adWindow(d).MemoryUsage(), items}, nil
		case "push":
			expression.DefaultExprPushDownBlacklist.Store(pdBlacklist(arg[[][]any](p, 1)))
			push := []any{}
			for _, st := range []kv.StoreType{kv.TiKV, kv.TiFlash, kv.TiDB, kv.UnSpecified} {
				push = append(push, exTry(func() any { return aggregation.CheckAggPushDown(ectx, d, st) }))
			}
			return map[string]any{"push": push, "flash": exTry(func() any { return aggregation.CheckAggPushFlash(ectx, d) })}, nil
		case "topb":
			client := &pdClient{mode: arg[int](p, 1)}
			pctx := expression.NewPushDownContext(ectx, client, false, nil, nil, arg[uint64](p, 2))
			pb, err := aggregation.AggFuncToPBExpr(pctx, d, kv.StoreType(arg[uint8](p, 3)))
			if err != nil {
				return exErr(err), nil
			}
			b, err := pb.Marshal()
			must(err)
			return map[string]any{"pb": hex.EncodeToString(b)}, nil
		}
	case "avgsum":
		d := &aggregation.AggFuncDesc{}
		d.RetTp = d2FtIn(exArr(p[0]))
		d.TypeInfer4AvgSum(d2FtIn(exArr(p[1])))
		return sfFtIn(d.RetTp), nil
	case "finalcount":
		d := &aggregation.AggFuncDesc{}
		d.TypeInfer4FinalCount(d2FtIn(exArr(p[0])))
		return sfFtIn(d.RetTp), nil
	case "equal":
		sa, sb := adSpec(p[0]), adSpec(p[1])
		ctx := adCtx(sa)
		ectx := ctx.GetEvalCtx()
		a, err := adDesc(ctx, sa)
		if err != nil {
			return exErr(err), nil
		}
		b, err := adDesc(ctx, sb)
		if err != nil {
			return exErr(err), nil
		}
		c := a.Clone()
		items := []any{}
		for i := 0; i < len(a.OrderByItems) && i < len(b.OrderByItems); i++ {
			items = append(items, a.OrderByItems[i].Equal(ectx, b.OrderByItems[i]))
		}
		return map[string]any{"ab": a.Equal(ectx, b), "ba": b.Equal(ectx, a), "ca": c.Equal(ectx, a),
			"clone": adDump(c), "items": items}, nil
	case "names":
		n := arg[string](p, 0)
		return []bool{aggregation.NeedCount(n), aggregation.NeedValue(n)}, nil
	case "mode":
		return aggregation.AggFunctionMode(arg[int](p, 0)).ToString(), nil
	case "allfirst":
		var ds []*aggregation.AggFuncDesc
		for _, n := range arg[[]string](p, 0) {
			d := &aggregation.AggFuncDesc{}
			d.Name = n
			ds = append(ds, d)
		}
		return aggregation.IsAllFirstRow(ds), nil
	case "window":
		s := adSpec(p[0])
		ctx := adCtx(s)
		collate.SetNewCollationEnabledForTest(s.NC)
		w := &aggregation.WindowFuncDesc{}
		w.Name, w.Args = s.N, adArgs(ctx, s)
		if string(s.RT) != "null" && len(s.RT) > 0 {
			w.RetTp = d2FtIn(exArr(s.RT))
		}
		d, err := aggregation.NewAggFuncDescForWindowFunc(ctx, w, s.D)
		if err != nil {
			return exErr(err), nil
		}
		return adDump(d), nil
	case "mode2pb":
		return int(*aggregation.AggFunctionModeToPB(aggregation.AggFunctionMode(arg[int](p, 0)))), nil
	case "pb2mode":
		if string(p[0]) == "null" {
			return int(aggregation.PBAggFuncModeToAggFuncMode(nil)), nil
		}
		m := tipb.AggFunctionMode(arg[int32](p, 0))
		return int(aggregation.PBAggFuncModeToAggFuncMode(&m)), nil
	case "frompb":
		b, err := hex.DecodeString(arg[string](p, 0))
		must(err)
		pb := &tipb.Expr{}
		must(pb.Unmarshal(b))
		var fts []*types.FieldType
		for _, f := range exArr(p[1]) {
			fts = append(fts, d2FtIn(exArr(f)))
		}
		collate.SetNewCollationEnabledForTest(arg[bool](p, 2))
		ctx := adCtx(adSpecT{})
		d, err := aggregation.PBExprToAggFuncDesc(ctx, pb, fts)
		if err != nil {
			return exErr(err), nil
		}
		return adDump(d), nil
	case "tipb":
		d := &aggregation.AggFuncDesc{}
		d.Name = arg[string](p, 0)
		return int(d.GetTiPBExpr(arg[bool](p, 1))), nil
	}
	return nil, errors.Errorf("aggdesc: unknown op %s", op)
}

// ---------------------------------------------------------------- generation

var adNames = []string{ast.AggFuncCount, ast.AggFuncSum, ast.AggFuncAvg, ast.AggFuncGroupConcat, ast.AggFuncMax,
	ast.AggFuncMin, ast.AggFuncFirstRow, ast.AggFuncBitAnd, ast.AggFuncBitOr, ast.AggFuncBitXor, ast.AggFuncVarPop,
	ast.AggFuncVarSamp, ast.AggFuncStddevPop, ast.AggFuncStddevSamp, ast.AggFuncJsonArrayagg, ast.AggFuncJsonObjectAgg,
	ast.AggFuncApproxCountDistinct, ast.AggFuncApproxPercentile, ast.WindowFuncRowNumber, ast.WindowFuncRank,
	ast.WindowFuncDenseRank, ast.WindowFuncCumeDist, ast.WindowFuncPercentRank, ast.WindowFuncNtile,
	ast.WindowFuncLead, ast.WindowFuncLag, ast.WindowFuncFirstValue, ast.WindowFuncLastValue, ast.WindowFuncNthValue}

// adAggNames: the functions UpdateNotNullFlag4RetType, the outer-join value and the
// push-down know (aggregate functions of a GROUP BY).
var adAggNames = []string{ast.AggFuncCount, ast.AggFuncSum, ast.AggFuncAvg, ast.AggFuncGroupConcat, ast.AggFuncMax,
	ast.AggFuncMin, ast.AggFuncFirstRow, ast.AggFuncBitAnd, ast.AggFuncBitOr, ast.AggFuncBitXor,
	ast.AggFuncApproxCountDistinct, ast.AggFuncVarPop, ast.AggFuncStddevSamp, ast.AggFuncJsonArrayagg,
	ast.AggFuncJsonObjectAgg}

func adFts() [][]any {
	const nn, un, bin = mysql.NotNullFlag, mysql.UnsignedFlag, mysql.BinaryFlag
	return [][]any{
		pdFt(mysql.TypeLonglong, bin, 20, 0, "binary", "binary"),
		pdFt(mysql.TypeLonglong, bin|nn, 20, 0, "binary", "binary"),
		pdFt(mysql.TypeLonglong, bin|un, 20, 0, "binary", "binary"),
		pdFt(mysql.TypeTiny, bin, 4, 0, "binary", "binary"),
		pdFt(mysql.TypeShort, bin|nn, 6, 0, "binary", "binary"),
		pdFt(mysql.TypeInt24, bin, 9, 0, "binary", "binary"),
		pdFt(mysql.TypeLong, bin, 11, 0, "binary", "binary"),
		pdFt(mysql.TypeYear, bin|un, 4, 0, "binary", "binary"),
		pdFt(mysql.TypeBit, un, 8, 0, "binary", "binary"),
		pdFt(mysql.TypeBit, un|nn, 64, 0, "binary", "binary"),
		pdFt(mysql.TypeFloat, bin, 12, -1, "binary", "binary"),
		pdFt(mysql.TypeDouble, bin, 22, -1, "binary", "binary"),
		pdFt(mysql.TypeDouble, bin|nn, 10, 3, "binary", "binary"),
		pdFt(mysql.TypeNewDecimal, bin, 10, 2, "binary", "binary"),
		pdFt(mysql.TypeNewDecimal, bin|nn, 65, 30, "binary", "binary"),
		pdFt(mysql.TypeNewDecimal, bin, 20, 0, "binary", "binary"),
		pdFt(mysql.TypeNewDecimal, bin, -1, -1, "binary", "binary"),
		pdFt(mysql.TypeVarchar, 0, 20, 0, "utf8mb4", "utf8mb4_bin"),
		pdFt(mysql.TypeVarchar, nn, 30, 0, "utf8mb4", "utf8mb4_general_ci"),
		pdFt(mysql.TypeString, 0, 10, 0, "utf8mb4", "utf8mb4_unicode_ci"),
		pdFt(mysql.TypeVarString, bin, 8, 0, "binary", "binary"),
		pdFt(mysql.TypeBlob, bin, 65535, 0, "binary", "binary"),
		pdFt(mysql.TypeBlob, 0, 65535, 0, "utf8mb4", "utf8mb4_bin"),
		pdFt(mysql.TypeVarchar, 0, 20, 0, "latin1", "latin1_bin"),
		pdFt(mysql.TypeEnum, 0, 1, 0, "utf8mb4", "utf8mb4_bin", "a", "b"),
		pdFt(mysql.TypeSet, nn, 3, 0, "utf8mb4", "utf8mb4_bin", "x", "y"),
		pdFt(mysql.TypeDate, bin, 10, 0, "binary", "binary"),
		pdFt(mysql.TypeDatetime, bin, 23, 3, "binary", "binary"),
		pdFt(mysql.TypeTimestamp, bin|nn, 26, 6, "binary", "binary"),
		pdFt(mysql.TypeDuration, bin, 12, 2, "binary", "binary"),
		pdFt(mysql.TypeJSON, bin, 4294967295, 0, "binary", "binary"),
		pdFt(mysql.TypeTiDBVectorFloat32, 0, -1, -1, "binary", "binary"),
		pdFt(mysql.TypeNull, bin, 0, 0, "binary", "binary"),
	}
}

func adCol(uid int, ft []any) any {
	return map[string]any{"col": []any{uid, uid - 1, ft, ""}}
}

func adConst(d []any, ft []any) any { return map[string]any{"const": []any{d, ft}} }

func adConsts() []any {
	intFt := pdFt(mysql.TypeLonglong, mysql.BinaryFlag, 20, 0, "binary", "binary")
	return []any{
		adConst([]any{types.KindInt64, 1}, intFt),
		adConst([]any{types.KindInt64, 50}, intFt),
		adConst([]any{types.KindInt64, -5}, intFt),
		adConst([]any{types.KindUint64, uint64(18446744073709551615)},
			pdFt(mysql.TypeLonglong, mysql.BinaryFlag|mysql.UnsignedFlag, 20, 0, "binary", "binary")),
		adConst([]any{types.KindNull}, pdFt(mysql.TypeNull, mysql.BinaryFlag, 0, 0, "binary", "binary")),
		adConst([]any{types.KindNull}, intFt),
		adConst([]any{types.KindFloat64, 0x4004000000000000}, pdFt(mysql.TypeDouble, mysql.BinaryFlag, 22, -1, "binary", "binary")),
		adConst([]any{types.KindMysqlDecimal, "12.50"}, pdFt(mysql.TypeNewDecimal, mysql.BinaryFlag, 4, 2, "binary", "binary")),
		adConst([]any{types.KindString, "abc", "utf8mb4_bin"}, pdFt(mysql.TypeVarString, 0, 3, 0, "utf8mb4", "utf8mb4_bin")),
		adConst([]any{types.KindString, "75", "utf8mb4_general_ci"}, pdFt(mysql.TypeVarString, 0, 2, 0, "utf8mb4", "utf8mb4_general_ci")),
		adConst([]any{types.KindString, ",", "utf8mb4_bin"}, pdFt(mysql.TypeVarString, 0, 1, 0, "utf8mb4", "utf8mb4_bin")),
		adConst([]any{types.KindBytes, "0102"}, pdFt(mysql.TypeVarString, mysql.BinaryFlag, 2, 0, "binary", "binary")),
	}
}

type adGen struct {
	rng    *rand.Rand
	fts    [][]any
	consts []any
	uid    int
}

func (g *adGen) col() any {
	g.uid++
	return adCol(g.uid, g.fts[g.rng.IntN(len(g.fts))])
}

// leaf: a column (mostly), a constant or a function of them.
func (g *adGen) leaf() any {
	switch r := g.rng.IntN(20); {
	case r < 13:
		return g.col()
	case r < 16:
		return g.consts[g.rng.IntN(len(g.consts))]
	case r < 18:
		g.uid++
		ft := g.fts[g.rng.IntN(13)] // a number column
		return map[string]any{"f": []any{"plus", nil, []any{adCol(g.uid, ft), g.consts[g.rng.IntN(3)]}}}
	default:
		g.uid++
		return map[string]any{"castf": []any{pdFt(mysql.TypeFloat, mysql.BinaryFlag, 12, -1, "binary", "binary"),
			adCol(g.uid, g.fts[g.rng.IntN(24)])}} // numbers and strings
	}
}

func (g *adGen) args(name string) []any {
	g.uid = 0
	n := 1
	switch name {
	case ast.AggFuncGroupConcat:
		n = 1 + g.rng.IntN(3)
		a := []any{}
		for i := 0; i < n; i++ {
			a = append(a, g.leaf())
		}
		return append(a, g.consts[10]) // the separator
	case ast.AggFuncApproxPercentile:
		return []any{g.leaf(), g.consts[g.rng.IntN(len(g.consts))]}
	case ast.WindowFuncLead, ast.WindowFuncLag:
		n = 1 + g.rng.IntN(3)
	case ast.WindowFuncNthValue, ast.AggFuncJsonObjectAgg:
		n = 2
	case ast.WindowFuncRowNumber, ast.WindowFuncRank, ast.WindowFuncDenseRank, ast.WindowFuncCumeDist,
		ast.WindowFuncPercentRank:
		n = 0
	case ast.AggFuncCount:
		n = 1 + g.rng.IntN(2)
	}
	a := []any{}
	for i := 0; i < n; i++ {
		a = append(a, g.leaf())
	}
	return a
}

func (g *adGen) spec(name string) map[string]any {
	s := map[string]any{"n": name, "a": g.args(name), "d": g.rng.IntN(3) == 0, "m": 0, "ob": []any{},
		"raw": false, "rt": nil, "nc": g.rng.IntN(5) != 0, "dpi": 0}
	if g.rng.IntN(4) == 0 {
		s["dpi"] = g.rng.IntN(31)
	}
	if name == ast.AggFuncGroupConcat && g.rng.IntN(2) == 0 {
		ob := []any{}
		for i := 0; i <= g.rng.IntN(2); i++ {
			ob = append(ob, []any{g.leaf(), g.rng.IntN(2) == 0})
		}
		s["ob"] = ob
	}
	return s
}

// rawSpec: a descriptor built from its fields with a random return type.
func (g *adGen) rawSpec(name string) map[string]any {
	s := g.spec(name)
	s["raw"] = true
	s["rt"] = g.fts[g.rng.IntN(len(g.fts)-1)] // not the NULL type
	return s
}

func aggdescGenerate(gen *vec.Gen, rng *rand.Rand) {
	g := &adGen{rng: rng, fts: adFts(), consts: adConsts()}
	names := append(append([]string{}, adNames...), "COUNT", "Max", "GROUP_CONCAT", "Row_Number", "LEAD", "foo", "std", "variance")

	// NewAggFuncDesc: every function with every argument type, then random shapes.
	for _, n := range names {
		for _, ft := range g.fts {
			s := g.spec(n)
			a := s["a"].([]any)
			if len(a) > 0 {
				a[0] = adCol(1, ft)
			}
			gen.Add("type/"+n, "new", s)
		}
	}
	for i := 0; i < 600; i++ {
		gen.Add("rand/new", "new", g.spec(names[rng.IntN(len(names))]))
	}
	// approx_percentile: the percentage argument.
	intFt := pdFt(mysql.TypeLonglong, mysql.BinaryFlag, 20, 0, "binary", "binary")
	for _, pct := range append(g.consts, adCol(9, intFt),
		map[string]any{"f": []any{"plus", nil, []any{adConst([]any{types.KindInt64, int64(9223372036854775807)}, intFt), g.consts[0]}}},
		adConst([]any{types.KindInt64, 0}, intFt), adConst([]any{types.KindInt64, 100}, intFt),
		adConst([]any{types.KindInt64, 101}, intFt)) {
		for _, ft := range g.fts {
			gen.Add("percentile", "new", map[string]any{"n": ast.AggFuncApproxPercentile, "a": []any{adCol(1, ft), pct},
				"d": false, "m": 0, "ob": []any{}, "raw": false, "rt": nil, "nc": true, "dpi": 0})
		}
	}
	gen.Add("percentile", "new", map[string]any{"n": ast.AggFuncApproxPercentile, "a": []any{adCol(1, intFt)},
		"d": false, "m": 0, "ob": []any{}, "raw": false, "rt": nil, "nc": true, "dpi": 0})
	// lead / lag with a default whose collation does not merge with the first argument.
	for _, n := range []string{ast.WindowFuncLead, ast.WindowFuncLag} {
		for _, c := range [][2]string{{"utf8mb4_unicode_ci", "utf8mb4_general_ci"}, {"utf8mb4_bin", "utf8mb4_bin"},
			{"utf8mb4_bin", "latin1_bin"}, {"utf8mb4_general_ci", "binary"}} {
			for _, coer := range []int{0, 2} {
				cs0, cs2 := "utf8mb4", "utf8mb4"
				if c[1] == "latin1_bin" {
					cs2 = "latin1"
				} else if c[1] == "binary" {
					cs2 = "binary"
				}
				a0 := map[string]any{"col": []any{1, 0, pdFt(mysql.TypeVarchar, 0, 20, 0, cs0, c[0]), ""}, "coer": coer}
				a2 := map[string]any{"col": []any{3, 2, pdFt(mysql.TypeVarchar, 0, 20, 0, cs2, c[1]), ""}, "coer": coer}
				for _, nc := range []bool{true, false} {
					s := map[string]any{"n": n, "a": []any{a0, g.consts[0], a2}, "d": false, "m": 0, "ob": []any{},
						"raw": false, "rt": nil, "nc": nc, "dpi": 0}
					gen.Add("leadlag", "new", s)
					gen.Add("leadlag", "mem", s)
				}
			}
		}
	}
	// zero arguments.
	for _, n := range adNames {
		gen.Add("noargs", "new", map[string]any{"n": n, "a": []any{}, "d": false, "m": 0, "ob": []any{}, "raw": false,
			"rt": nil, "nc": true, "dpi": 0})
	}

	// WrapCastForAggArgs, from NewAggFuncDesc and from raw descriptors of every return type.
	for i := 0; i < 250; i++ {
		gen.Add("rand/wrap", "wrap", g.spec(names[rng.IntN(len(names))]))
	}
	for _, n := range names {
		for i := 0; i < 8; i++ {
			gen.Add("raw/wrap", "wrap", g.rawSpec(n))
		}
	}

	// UpdateNotNullFlag4RetType.
	for _, n := range names {
		for _, gb := range []bool{false, true} {
			for _, af := range []bool{false, true} {
				gen.Add("notnull", "notnull", g.spec(n), gb, af)
			}
		}
	}
	for i := 0; i < 60; i++ {
		gen.Add("raw/notnull", "notnull", g.rawSpec(names[rng.IntN(len(names))]), rng.IntN(2) == 0, rng.IntN(2) == 0)
	}

	// GetDefaultValue.
	for _, n := range names {
		for i := 0; i < 3; i++ {
			gen.Add("default", "default", g.spec(n))
		}
	}
	for _, n := range []string{ast.AggFuncApproxCountDistinct, ast.AggFuncCount} {
		for _, ft := range g.fts[:len(g.fts)-1] {
			s := g.rawSpec(n)
			s["rt"] = ft
			gen.Add("raw/default", "default", s)
		}
	}

	// EvalNullValueInOuterJoin: arguments over columns 1..3 of the inner side.
	for i := 0; i < 220; i++ {
		n := adAggNames[rng.IntN(10)]
		if rng.IntN(8) == 0 {
			n = adAggNames[rng.IntN(len(adAggNames))]
		}
		s := g.spec(n)
		s["a"] = g.outerArgs(n)
		gen.Add("outer", "outer", s, []int{1, 2, 3}[:1+rng.IntN(3)])
	}

	// Split.
	for _, n := range names {
		for _, m := range []int{0, 1, 2, 3, 4} {
			gen.Add("split", "split", g.splitSpec(n, m), []int{3 + rng.IntN(3), 7})
		}
	}

	// TypeInfer4AvgSum, TypeInfer4FinalCount.
	for _, rt := range g.fts {
		gen.Add("avgsum", "avgsum", rt, g.fts[13])
		gen.Add("avgsum", "avgsum", rt, g.fts[11])
		gen.Add("finalcount", "finalcount", rt)
	}

	// StringWithCtx in the three redact modes.
	for i := 0; i < 180; i++ {
		s := g.spec(names[rng.IntN(len(names))])
		if rng.IntN(2) == 0 {
			s = g.spec(ast.AggFuncGroupConcat)
		}
		gen.Add("string", "string", s, []string{errors.RedactLogDisable, errors.RedactLogEnable, errors.RedactLogMarker}[i%3])
	}

	// Equal / Clone: the same spec, one field changed, or another spec.
	for i := 0; i < 200; i++ {
		a := g.spec(names[rng.IntN(len(names))])
		if rng.IntN(2) == 0 {
			a = g.spec(ast.AggFuncGroupConcat)
		}
		b := map[string]any{}
		for k, v := range a {
			b[k] = v
		}
		switch rng.IntN(6) {
		case 0:
		case 1:
			b["d"] = !a["d"].(bool)
		case 2:
			b["m"] = 1 + rng.IntN(4)
		case 3:
			b["n"] = names[rng.IntN(len(names))]
		case 4:
			if ob := a["ob"].([]any); len(ob) > 0 {
				it := ob[0].([]any)
				b["ob"] = append([]any{[]any{it[0], !it[1].(bool)}}, ob[1:]...)
			}
		default:
			b = g.spec(a["n"].(string))
		}
		gen.Add("equal", "equal", a, b)
	}

	// MemoryUsage.
	for i := 0; i < 120; i++ {
		gen.Add("mem", "mem", g.spec(names[rng.IntN(len(names))]))
	}

	// Names, modes.
	for _, n := range names {
		gen.Add("names", "names", n)
	}
	for m := 0; m <= 4; m++ {
		gen.Add("mode", "mode", m)
	}
	for i := 0; i < 20; i++ {
		ns := []string{}
		for j := 0; j < rng.IntN(4); j++ {
			ns = append(ns, []string{ast.AggFuncFirstRow, ast.AggFuncFirstRow, ast.AggFuncMax, ast.AggFuncCount}[rng.IntN(4)])
		}
		gen.Add("allfirst", "allfirst", ns)
	}

	// NewAggFuncDescForWindowFunc: with a return type, and the safety check (nil RetTp).
	for i := 0; i < 60; i++ {
		var s map[string]any
		if i%2 == 0 {
			s = g.rawSpec(names[rng.IntN(len(names))])
		} else {
			s = g.spec(names[rng.IntN(len(names))])
		}
		gen.Add("window", "window", s)
	}

	// Part B: push-down decisions.
	blacklists := [][][]any{nil, {{"sum", 1}}, {{"count", 2}}, {{"group_concat", 7}}, {{"max", 4}, {"avg", 3}}}
	for _, n := range names {
		for i := 0; i < 6; i++ {
			s := g.spec(n)
			if n == ast.AggFuncGroupConcat && i%2 == 0 {
				s["ob"] = []any{}
			}
			gen.Add("push", "push", s, blacklists[rng.IntN(len(blacklists))])
		}
	}
	for _, n := range []string{ast.AggFuncSum, ast.AggFuncAvg, ast.AggFuncCount, ast.AggFuncMax, ast.AggFuncGroupConcat} {
		for _, ft := range g.fts {
			s := g.spec(n)
			s["a"].([]any)[0] = adCol(1, ft)
			gen.Add("push/types", "push", s, nil)
		}
	}

	// AggFuncToPBExpr, and PBExprToAggFuncDesc of its result.
	for _, n := range names {
		for i := 0; i < 8; i++ {
			s := g.spec(n)
			s["m"] = rng.IntN(5)
			gen.Add("topb", "topb", s, []int{0, 0, 0, 1, 2}[rng.IntN(5)], []uint64{1024, 0, 1 << 32}[rng.IntN(3)],
				[]int{0, 1, 2, 255}[rng.IntN(4)])
		}
	}
	for _, ft := range g.fts {
		for _, n := range []string{ast.AggFuncSum, ast.AggFuncMax, ast.AggFuncGroupConcat, ast.AggFuncCount} {
			s := g.spec(n)
			s["a"].([]any)[0] = adCol(1, ft)
			gen.Add("topb/types", "topb", s, 0, uint64(1024), 1)
		}
	}
	for m := 0; m <= 4; m++ {
		gen.Add("mode2pb", "mode2pb", m)
	}
	gen.Add("pb2mode", "pb2mode", nil)
	for m := 0; m <= 5; m++ {
		gen.Add("pb2mode", "pb2mode", m)
	}
	for _, rec := range g.pbs(rng) {
		gen.Add("frompb", "frompb", rec...)
	}
	for _, n := range names {
		gen.Add("tipb", "tipb", n, false)
		gen.Add("tipb", "tipb", n, true)
	}
}

// outerArgs: arguments of EvalNullValueInOuterJoin over columns 1..3 (some outside the
// schema), constants and functions of them.
func (g *adGen) outerArgs(name string) []any {
	intFt := pdFt(mysql.TypeLonglong, mysql.BinaryFlag, 20, 0, "binary", "binary")
	decFt := pdFt(mysql.TypeNewDecimal, mysql.BinaryFlag, 10, 2, "binary", "binary")
	col := func() any {
		u := 1 + g.rng.IntN(4)
		return adCol(u, [][]any{intFt, decFt}[g.rng.IntN(2)])
	}
	one := func() any {
		switch g.rng.IntN(6) {
		case 0:
			return col()
		case 1:
			return g.consts[g.rng.IntN(8)]
		case 2:
			return map[string]any{"f": []any{"minus", nil, []any{col(), g.consts[g.rng.IntN(3)]}}}
		case 3:
			return map[string]any{"f": []any{"plus", nil, []any{col(), g.consts[g.rng.IntN(3)]}}}
		case 4:
			return map[string]any{"f": []any{"isnull", nil, []any{col()}}}
		default:
			return map[string]any{"f": []any{"coalesce", nil, []any{col(), col(), g.consts[1]}}}
		}
	}
	a := []any{one()}
	if name == ast.AggFuncCount && g.rng.IntN(2) == 0 {
		a = append(a, one())
	}
	if name == ast.AggFuncGroupConcat {
		a = append(a, g.consts[10])
	}
	return a
}

// splitSpec: a descriptor in mode m (group_concat with ORDER BY sometimes).
func (g *adGen) splitSpec(name string, m int) map[string]any {
	s := g.spec(name)
	s["m"] = m
	return s
}

// pbs: PBExprToAggFuncDesc inputs: AggFuncToPBExpr results of random descriptors, and edited
// messages (unknown types, no field type, no mode, an unknown mode, a bad child).
func (g *adGen) pbs(rng *rand.Rand) [][]any {
	fts := []any{g.fts[0], g.fts[11], g.fts[13], g.fts[17], g.fts[27]}
	var out [][]any
	ctxNames := []string{ast.AggFuncCount, ast.AggFuncSum, ast.AggFuncAvg, ast.AggFuncGroupConcat, ast.AggFuncMax,
		ast.AggFuncMin, ast.AggFuncFirstRow, ast.AggFuncBitAnd, ast.AggFuncBitOr, ast.AggFuncBitXor,
		ast.AggFuncApproxCountDistinct, ast.AggFuncVarPop, ast.AggFuncJsonArrayagg}
	for i := 0; i < 160; i++ {
		n := ctxNames[rng.IntN(len(ctxNames))]
		s := adSpec(vec.MustJSON(g.spec(n)))
		// columns refer to the field types by index
		s.A = nil
		for j := 0; j < 1+rng.IntN(2); j++ {
			k := rng.IntN(len(fts))
			s.A = append(s.A, vec.MustJSON(adCol(k+1, fts[k].([]any))))
		}
		if n == ast.AggFuncGroupConcat {
			s.A = append(s.A, vec.MustJSON(g.consts[10]))
		}
		ctx := adCtx(s)
		d, err := adDesc(ctx, s)
		if err != nil {
			continue
		}
		d.Mode = aggregation.AggFunctionMode(rng.IntN(5))
		pb, err := aggregation.AggFuncToPBExpr(expression.NewPushDownContext(ctx.GetEvalCtx(), &pdClient{}, false, nil, nil, 1024), d, kv.TiKV)
		if err != nil {
			continue
		}
		switch rng.IntN(8) {
		case 0:
			pb.Tp = []tipb.ExprType{tipb.ExprType_StddevPop, tipb.ExprType_JsonArrayAgg, tipb.ExprType_RowNumber,
				tipb.ExprType(3021), tipb.ExprType(9999), tipb.ExprType_Null}[rng.IntN(6)]
		case 1:
			pb.AggFuncMode = nil
		case 2:
			m := tipb.AggFunctionMode(7)
			pb.AggFuncMode = &m
		case 3:
			pb.FieldType = nil
		case 4:
			if len(pb.Children) > 0 {
				pb.Children[0].Val = []byte{1}
			}
		}
		b, err := pb.Marshal()
		must(err)
		out = append(out, []any{hex.EncodeToString(b), fts, s.NC})
	}
	return out
}
