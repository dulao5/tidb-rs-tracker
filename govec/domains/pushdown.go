package domains

// Domain "pushdown", block D08.3 part 1: push-down eligibility (infer_pushdown.go) and the
// conversion of expressions to tipb (expr_to_pb.go).
//
// A tree is given as a recipe (as in expr_sf.go, plus column IDs and correlated values) and
// built with NewFunctionBase (no constant folding), so Go chooses the real signatures of every
// function family, ported or not. Each record returns the built tree ("tree": function name,
// signature type, result type, charset/collation, pb code, the CAST signatures' inUnion) for
// tidb-rs to rebuild with descriptor-only signatures, and the outputs of the push-down code.
//
// The kv.Client is mock.Client (kv.RequestTypeSupportedChecker, as the Go tests and CopClient)
// or a variant that refuses ReqTypeDAG (mode 1: columns by ID, no MysqlTime constants) or
// everything (mode 2).
//
// Records:
//   - "expr": [recipe, opts] -> tree, ExprToPB, ExpressionsToPBList, ProjectionExpressionsToPBList,
//     GroupByItemToPB, SortByItemToPB (marshalled bytes); canFuncBePushed for each store type,
//     scalarExprSupportedByTiKV/Flash/TiDB, canEnumPushdownPreliminarily; for each store type
//     CanExprsPushDownWithExtraInfo with the warnings of the two handlers.
//   - "split": [[recipes], opts, storeType] -> PushDownExprsWithExtraInfo, PushDownExprs,
//     CanExprsPushDown (indices of the pushed / remained expressions, warnings).
//   - "ft": [ftIn, nc, storeType] -> ToPBFieldType, ToPBFieldTypeWithCheck, FieldTypeFromPB of it.
//   - "ftpb": [tp, flag, flen, decimal, collate, charset, elems] -> FieldTypeFromPB.
//   - "enabled": [name, storeType, blacklist] -> IsPushDownEnabled.
//   - "ctx": [fromSessVars, explain, handlers, groupConcatMaxLen] -> NewPushDownContext(FromSessionVars):
//     GetGroupConcatMaxLen, Client, AppendWarning (which handler receives it).
//
// opts: {"nc": new collation, "cl": client mode, "loc": UTC offset or null (time.UTC),
// "bl": blacklist [[name, mask]...], "ex": InExplainStmt, "wh": warn handlers (bit 1 the
// statement's, bit 2 the extra one), "ep": canEnumPush, "desc": SortByItemToPB's desc}.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	gotime "time"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
	"github.com/pingcap/tipb/go-tipb"
)

const pushdownSeed = 20261114

func init() {
	vec.Register(&vec.Domain{
		Name:     "pushdown",
		Seed:     pushdownSeed,
		Coverage: pushdownCoverage,
		Blocks:   []string{"D08.3"},
		Exempt:   pushdownExempt(),
		Gzip:     true,
		Generate: pushdownGenerate,
		Exec:     pushdownExec,
	})
}

var pushdownCoverage = map[string]vec.Coverage{
	"expr": {Min: 4000, GoFuncs: []string{"NewPBConverter", "PbConverter.ExprToPB", "PbConverter.conOrCorColToPBExpr",
		"PbConverter.encodeDatum", "PbConverter.columnToPBExpr", "PbConverter.scalarFuncToPBExpr", "ToPBFieldType",
		"ExpressionsToPBList", "ProjectionExpressionsToPBList", "GroupByItemToPB", "SortByItemToPB",
		"canFuncBePushed", "canScalarFuncPushDown", "canExprPushDown", "scalarExprSupportedByTiDB",
		"scalarExprSupportedByTiKV", "scalarExprSupportedByFlash", "canEnumPushdownPreliminarily",
		"IsPushDownEnabled", "storeTypeMask", "NewPushDownContext", "PushDownContext.EvalCtx",
		"PushDownContext.PbConverter", "PushDownContext.AppendWarning", "CanExprsPushDownWithExtraInfo",
		"PushDownExprsWithExtraInfo"}},
	"split": {Min: 600, GoFuncs: []string{"PushDownExprsWithExtraInfo", "PushDownExprs", "CanExprsPushDown",
		"CanExprsPushDownWithExtraInfo"}},
	"ft":      {Min: 300, GoFuncs: []string{"ToPBFieldType", "ToPBFieldTypeWithCheck", "FieldTypeFromPB"}},
	"ftpb":    {Min: 200, GoFuncs: []string{"FieldTypeFromPB"}},
	"enabled": {Min: 300, GoFuncs: []string{"IsPushDownEnabled", "storeTypeMask", "init"}},
	"ctx": {Min: 10, GoFuncs: []string{"NewPushDownContextFromSessionVars", "NewPushDownContext",
		"PushDownContext.Client", "PushDownContext.GetGroupConcatMaxLen", "PushDownContext.AppendWarning",
		"PushDownContext.EvalCtx"}},
}

func pushdownExempt() map[string]string {
	const part2 = "D08.3 part 2: the PBToExpr direction (distsql_builtin.go), which builds signatures of every " +
		"family by pb code"
	m := map[string]string{}
	for _, f := range []string{"PbTypeToFieldType", "getSignatureByPB", "newDistSQLFunctionBySig", "PBToExprs",
		"PBToExpr", "convertTime", "decodeValueList", "convertInt", "convertUint", "convertString", "convertFloat",
		"convertDecimal", "convertDuration", "convertJSON", "convertVectorFloat32", "convertEnum"} {
		m[f] = part2
	}
	return m
}

// ---------------------------------------------------------------- unexported TiDB code

//go:linkname pdCanFuncBePushed github.com/pingcap/tidb/pkg/expression.canFuncBePushed
func pdCanFuncBePushed(ctx expression.EvalContext, sf *expression.ScalarFunction, storeType kv.StoreType) bool

//go:linkname pdSupportedByTiKV github.com/pingcap/tidb/pkg/expression.scalarExprSupportedByTiKV
func pdSupportedByTiKV(ctx expression.EvalContext, sf *expression.ScalarFunction) bool

//go:linkname pdSupportedByFlash github.com/pingcap/tidb/pkg/expression.scalarExprSupportedByFlash
func pdSupportedByFlash(ctx expression.EvalContext, sf *expression.ScalarFunction) bool

//go:linkname pdSupportedByTiDB github.com/pingcap/tidb/pkg/expression.scalarExprSupportedByTiDB
func pdSupportedByTiDB(ctx expression.EvalContext, sf *expression.ScalarFunction) bool

//go:linkname pdCanEnumPush github.com/pingcap/tidb/pkg/expression.canEnumPushdownPreliminarily
func pdCanEnumPush(sf *expression.ScalarFunction) bool

// ---------------------------------------------------------------- client

// pdClient is mock.Client (kv.RequestTypeSupportedChecker) with two restricted variants.
type pdClient struct{ mode int }

func (*pdClient) Send(context.Context, *kv.Request, any, *kv.ClientSendOption) kv.Response {
	panic("pdClient.Send")
}

func (c *pdClient) IsRequestTypeSupported(reqType, subType int64) bool {
	switch c.mode {
	case 1:
		if reqType == kv.ReqTypeDAG {
			return false
		}
	case 2:
		return false
	}
	return kv.RequestTypeSupportedChecker{}.IsRequestTypeSupported(reqType, subType)
}

// ---------------------------------------------------------------- recipes

// A recipe node: {"col": [uid, index, ftIn, id]}, {"corr": [uid, ftIn, datumIn|null]},
// {"const": [datumIn, ftIn]}, {"f": [name, retFtIn|null, [args]]}.
func pdBuild(ctx expression.BuildContext, raw json.RawMessage) (expression.Expression, error) {
	var r sfRecipe
	must(json.Unmarshal(raw, &r))
	if p, ok := r["col"]; ok {
		a := exArr(p)
		return &expression.Column{UniqueID: arg[int64](a, 0), Index: arg[int](a, 1), RetType: d2FtIn(exArr(a[2])),
			ID: arg[int64](a, 3)}, nil
	}
	if p, ok := r["corr"]; ok {
		a := exArr(p)
		d := types.Datum{}
		if string(a[2]) != "null" {
			d = d2DatumIn(exArr(a[2]))
		}
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
		e, err := pdBuild(ctx, c)
		if err != nil {
			return nil, err
		}
		args = append(args, e)
	}
	return expression.NewFunctionBase(ctx, name, rt, args...)
}

// pdInUnion: baseBuiltinCastFunc.inUnion of a signature that embeds it (its metadata()), or nil.
func pdInUnion(f any) any {
	v := reflect.ValueOf(f).Elem()
	sf, ok := v.Type().FieldByName("inUnion")
	if !ok {
		return nil
	}
	return v.FieldByIndex(sf.Index).Bool()
}

// pdDump: the built tree for tidb-rs to rebuild.
func pdDump(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		return map[string]any{"col": []any{x.UniqueID, x.Index, sfFtIn(x.RetType), x.ID}}
	case *expression.CorrelatedColumn:
		return map[string]any{"corr": []any{x.UniqueID, sfFtIn(x.RetType), d2DatumInOf(*x.Data), x.ID}}
	case *expression.Constant:
		return map[string]any{"const": []any{d2DatumInOf(x.Value), sfFtIn(x.RetType)}}
	case *expression.ScalarFunction:
		args := []any{}
		for _, a := range x.GetArgs() {
			args = append(args, pdDump(a))
		}
		chs, coll := x.CharsetAndCollation()
		return map[string]any{"f": []any{x.FuncName.L, x.FuncName.O, reflect.TypeOf(x.Function).Elem().Name(),
			sfFtIn(x.RetType), args, chs, coll, int(x.Function.PbCode()), pdInUnion(x.Function),
			x.HasCoercibility(), int(x.Coercibility()), int(x.Repertoire()), x.IsExplicitCharset()}}
	}
	panic("pdDump")
}

// ---------------------------------------------------------------- options

type pdOptsT struct {
	NC   bool    `json:"nc"`
	CL   int     `json:"cl"`
	Loc  *int    `json:"loc"`
	BL   [][]any `json:"bl"`
	Ex   bool    `json:"ex"`
	WH   int     `json:"wh"`
	EP   bool    `json:"ep"`
	Desc bool    `json:"desc"`
}

func pdOpts(raw json.RawMessage) pdOptsT {
	var o pdOptsT
	must(json.Unmarshal(raw, &o))
	return o
}

func pdBlacklist(bl [][]any) map[string]uint32 {
	m := make(map[string]uint32)
	for _, e := range bl {
		m[e[0].(string)] = uint32(e[1].(float64))
	}
	return m
}

func pdEvalCtx(o pdOptsT) *exprstatic.EvalContext {
	loc := gotime.UTC
	if o.Loc != nil {
		loc = gotime.FixedZone("Z", *o.Loc)
	}
	return exprstatic.NewEvalContext(exprstatic.WithLocation(loc))
}

// pdHandlers: the statement's and the extra warn handlers (nil interfaces when not set).
func pdHandlers(wh int) (contextutil.WarnAppender, contextutil.WarnAppender, *contextutil.StaticWarnHandler, *contextutil.StaticWarnHandler) {
	var w, x contextutil.WarnAppender
	var ws, xs *contextutil.StaticWarnHandler
	if wh&1 != 0 {
		ws = contextutil.NewStaticWarnHandler(0)
		w = ws
	}
	if wh&2 != 0 {
		xs = contextutil.NewStaticWarnHandler(0)
		x = xs
	}
	return w, x, ws, xs
}

func pdWarns(h *contextutil.StaticWarnHandler) any {
	if h == nil {
		return nil
	}
	return ecWarns(h.GetWarnings())
}

func pdHex(m interface{ Marshal() ([]byte, error) }, isNil bool) any {
	if isNil {
		return nil
	}
	b, err := m.Marshal()
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func pdList(l []*tipb.Expr, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	out := []any{}
	for _, e := range l {
		out = append(out, pdHex(e, e == nil))
	}
	return out
}

var pdStores = []kv.StoreType{kv.TiKV, kv.TiFlash, kv.TiDB, kv.UnSpecified}

// ---------------------------------------------------------------- exec

func pushdownExec(op string, p []json.RawMessage) (out any, err error) {
	defer collate.SetNewCollationEnabledForTest(true)
	defer expression.DefaultExprPushDownBlacklist.Store(make(map[string]uint32))
	switch op {
	case "expr":
		o := pdOpts(p[1])
		collate.SetNewCollationEnabledForTest(o.NC)
		expression.DefaultExprPushDownBlacklist.Store(pdBlacklist(o.BL))
		e, err := pdBuild(exprstatic.NewExprContext(), p[0])
		if err != nil {
			return map[string]any{"build": exErr(err)}, nil
		}
		ectx := pdEvalCtx(o)
		client := &pdClient{mode: o.CL}
		m := map[string]any{"tree": pdDump(e)}
		pb := expression.NewPBConverter(client, ectx).ExprToPB(e)
		m["pb"] = pdHex(pb, pb == nil)
		m["list"] = pdList(expression.ExpressionsToPBList(ectx, []expression.Expression{e}, client))
		m["proj"] = pdList(expression.ProjectionExpressionsToPBList(ectx, []expression.Expression{e}, client))
		gb := expression.GroupByItemToPB(ectx, client, e)
		m["gb"] = pdHex(gb, gb == nil)
		sb := expression.SortByItemToPB(ectx, client, e, o.Desc)
		m["sb"] = pdHex(sb, sb == nil)
		if sf, ok := e.(*expression.ScalarFunction); ok {
			fn := []bool{}
			for _, st := range pdStores {
				fn = append(fn, pdCanFuncBePushed(ectx, sf, st))
			}
			m["fn"] = fn
			m["sup"] = []bool{pdSupportedByTiKV(ectx, sf), pdSupportedByFlash(ectx, sf), pdSupportedByTiDB(ectx, sf)}
			m["enum"] = pdCanEnumPush(sf)
		}
		can := []any{}
		for _, st := range pdStores {
			w, x, ws, xs := pdHandlers(o.WH)
			pctx := expression.NewPushDownContext(ectx, client, o.Ex, w, x, 1024)
			ok := expression.CanExprsPushDownWithExtraInfo(pctx, []expression.Expression{e}, st, o.EP)
			can = append(can, map[string]any{"ok": ok, "w": pdWarns(ws), "x": pdWarns(xs)})
		}
		m["can"] = can
		return m, nil
	case "split":
		o := pdOpts(p[1])
		collate.SetNewCollationEnabledForTest(o.NC)
		expression.DefaultExprPushDownBlacklist.Store(pdBlacklist(o.BL))
		st := kv.StoreType(arg[uint8](p, 2))
		var exprs []expression.Expression
		trees := []any{}
		for _, r := range exArr(p[0]) {
			e, err := pdBuild(exprstatic.NewExprContext(), r)
			if err != nil {
				return map[string]any{"build": exErr(err)}, nil
			}
			exprs = append(exprs, e)
			trees = append(trees, pdDump(e))
		}
		ectx := pdEvalCtx(o)
		client := &pdClient{mode: o.CL}
		idx := func(es []expression.Expression) []int {
			out := []int{}
			for _, e := range es {
				for i, x := range exprs {
					if x == e {
						out = append(out, i)
						break
					}
				}
			}
			return out
		}
		m := map[string]any{"trees": trees}
		w, x, ws, xs := pdHandlers(o.WH)
		pctx := expression.NewPushDownContext(ectx, client, o.Ex, w, x, 1024)
		pushed, remained := expression.PushDownExprsWithExtraInfo(pctx, exprs, st, o.EP)
		m["extra"] = []any{idx(pushed), idx(remained), pdWarns(ws), pdWarns(xs)}
		w, x, ws, xs = pdHandlers(o.WH)
		pctx = expression.NewPushDownContext(ectx, client, o.Ex, w, x, 1024)
		pushed, remained = expression.PushDownExprs(pctx, exprs, st)
		m["plain"] = []any{idx(pushed), idx(remained), pdWarns(ws), pdWarns(xs)}
		w, x, ws, xs = pdHandlers(o.WH)
		pctx = expression.NewPushDownContext(ectx, client, o.Ex, w, x, 1024)
		m["can"] = []any{expression.CanExprsPushDown(pctx, exprs, st), pdWarns(ws), pdWarns(xs)}
		return m, nil
	case "ft":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 1))
		ft := d2FtIn(exArr(p[0]))
		pb := expression.ToPBFieldType(ft)
		m := map[string]any{"pb": pdHex(pb, false)}
		if c, err := expression.ToPBFieldTypeWithCheck(ft, kv.StoreType(arg[uint8](p, 2))); err != nil {
			m["chk"] = map[string]any{"err": vec.GoString(err.Error())}
		} else {
			m["chk"] = pdHex(c, false)
		}
		m["back"] = d2FtOut(expression.FieldTypeFromPB(pb))
		return m, nil
	case "ftpb":
		pb := &tipb.FieldType{Tp: arg[int32](p, 0), Flag: arg[uint32](p, 1), Flen: arg[int32](p, 2),
			Decimal: arg[int32](p, 3), Collate: arg[int32](p, 4), Charset: arg[string](p, 5)}
		if string(p[6]) != "null" {
			pb.Elems = gsSlice(arg[[]vec.GoString](p, 6))
		}
		return d2FtOut(expression.FieldTypeFromPB(pb)), nil
	case "enabled":
		expression.DefaultExprPushDownBlacklist.Store(pdBlacklist(arg[[][]any](p, 2)))
		return expression.IsPushDownEnabled(arg[string](p, 0), kv.StoreType(arg[uint8](p, 1))), nil
	case "ctx":
		fromVars, explain, wh, gcml := arg[bool](p, 0), arg[bool](p, 1), arg[int](p, 2), arg[uint64](p, 3)
		ectx := exprstatic.NewEvalContext()
		client := &pdClient{}
		var pctx expression.PushDownContext
		var ws, xs func() []contextutil.SQLWarn
		if fromVars {
			sv := variable.NewSessionVars(nil)
			sv.StmtCtx.InExplainStmt = explain
			sv.GroupConcatMaxLen = gcml
			pctx = expression.NewPushDownContextFromSessionVars(ectx, sv, client)
			ws, xs = sv.StmtCtx.GetWarnings, sv.StmtCtx.GetExtraWarnings
		} else {
			w, x, wh1, xh1 := pdHandlers(wh)
			pctx = expression.NewPushDownContext(ectx, client, explain, w, x, gcml)
			ws = func() []contextutil.SQLWarn { return pdWarnList(wh1) }
			xs = func() []contextutil.SQLWarn { return pdWarnList(xh1) }
		}
		pctx.AppendWarning(errors.NewNoStackError("pushdown warning"))
		return map[string]any{"gcml": pctx.GetGroupConcatMaxLen(), "client": pctx.Client() == kv.Client(client),
			"ectx": pctx.EvalCtx() == expression.EvalContext(ectx), "w": ecWarns(ws()), "x": ecWarns(xs())}, nil
	}
	panic("pushdown: unknown op " + op)
}

func pdWarnList(h *contextutil.StaticWarnHandler) []contextutil.SQLWarn {
	if h == nil {
		return nil
	}
	return h.GetWarnings()
}

// ---------------------------------------------------------------- generation

func pdFt(tp byte, flag uint, flen, dec int, cs, co string, elems ...string) []any {
	var es any
	if elems != nil {
		g := []vec.GoString{}
		for _, e := range elems {
			g = append(g, vec.GoString(e))
		}
		es = g
	}
	return []any{tp, flag, flen, dec, cs, co, es}
}

var (
	pdFtInt       = pdFt(mysql.TypeLonglong, mysql.BinaryFlag, 20, 0, "binary", "binary")
	pdFtUint      = pdFt(mysql.TypeLonglong, mysql.BinaryFlag|mysql.UnsignedFlag, 20, 0, "binary", "binary")
	pdFtTiny      = pdFt(mysql.TypeTiny, mysql.BinaryFlag|mysql.NotNullFlag, 4, 0, "binary", "binary")
	pdFtDouble    = pdFt(mysql.TypeDouble, mysql.BinaryFlag, 22, -1, "binary", "binary")
	pdFtFloat     = pdFt(mysql.TypeFloat, mysql.BinaryFlag, 12, -1, "binary", "binary")
	pdFtDec       = pdFt(mysql.TypeNewDecimal, mysql.BinaryFlag, 10, 2, "binary", "binary")
	pdFtDecWide   = pdFt(mysql.TypeNewDecimal, mysql.BinaryFlag, 65, 30, "binary", "binary")
	pdFtDecBad    = pdFt(mysql.TypeNewDecimal, mysql.BinaryFlag, 70, 31, "binary", "binary")
	pdFtVarchar   = pdFt(mysql.TypeVarchar, 0, 32, 0, "utf8mb4", "utf8mb4_bin")
	pdFtVarGen    = pdFt(mysql.TypeVarchar, 0, 32, 0, "utf8mb4", "utf8mb4_general_ci")
	pdFtVarUni    = pdFt(mysql.TypeVarchar, 0, 32, 0, "utf8mb4", "utf8mb4_unicode_ci")
	pdFtVar0900   = pdFt(mysql.TypeVarchar, 0, 32, 0, "utf8mb4", "utf8mb4_0900_ai_ci")
	pdFtVarBin    = pdFt(mysql.TypeVarchar, mysql.BinaryFlag, 32, 0, "binary", "binary")
	pdFtVarLatin  = pdFt(mysql.TypeVarchar, 0, 32, 0, "latin1", "latin1_bin")
	pdFtVarGBK    = pdFt(mysql.TypeVarchar, 0, 32, 0, "gbk", "gbk_chinese_ci")
	pdFtChar      = pdFt(mysql.TypeString, 0, 8, 0, "utf8mb4", "utf8mb4_bin")
	pdFtBlob      = pdFt(mysql.TypeBlob, mysql.BinaryFlag, 65535, 0, "binary", "binary")
	pdFtText      = pdFt(mysql.TypeBlob, 0, 65535, 0, "utf8mb4", "utf8mb4_general_ci")
	pdFtDatetime  = pdFt(mysql.TypeDatetime, mysql.BinaryFlag, 19, 0, "binary", "binary")
	pdFtDT3       = pdFt(mysql.TypeDatetime, mysql.BinaryFlag, 23, 3, "binary", "binary")
	pdFtDate      = pdFt(mysql.TypeDate, mysql.BinaryFlag, 10, 0, "binary", "binary")
	pdFtTS        = pdFt(mysql.TypeTimestamp, mysql.BinaryFlag, 19, 0, "binary", "binary")
	pdFtTS6       = pdFt(mysql.TypeTimestamp, mysql.BinaryFlag, 26, 6, "binary", "binary")
	pdFtDur       = pdFt(mysql.TypeDuration, mysql.BinaryFlag, 10, 0, "binary", "binary")
	pdFtDur2      = pdFt(mysql.TypeDuration, mysql.BinaryFlag, 13, 2, "binary", "binary")
	pdFtYear      = pdFt(mysql.TypeYear, mysql.BinaryFlag|mysql.UnsignedFlag|mysql.ZerofillFlag, 4, 0, "binary", "binary")
	pdFtJSON      = pdFt(mysql.TypeJSON, mysql.BinaryFlag, 4294967295, 0, "binary", "binary")
	pdFtEnum      = pdFt(mysql.TypeEnum, 0, 1, 0, "utf8mb4", "utf8mb4_bin", "a", "b", "c")
	pdFtSet       = pdFt(mysql.TypeSet, 0, 5, 0, "utf8mb4", "utf8mb4_bin", "a", "b", "c")
	pdFtBit       = pdFt(mysql.TypeBit, mysql.UnsignedFlag, 8, 0, "binary", "binary")
	pdFtVec       = pdFt(mysql.TypeTiDBVectorFloat32, mysql.BinaryFlag, -1, 0, "binary", "binary")
	pdFtGeo       = pdFt(mysql.TypeGeometry, 0, -1, 0, "binary", "binary")
	pdFtNull      = pdFt(mysql.TypeNull, mysql.BinaryFlag, 0, 0, "binary", "binary")
	pdFtVarStr    = pdFt(mysql.TypeVarString, 0, 5, 0, "utf8mb4", "utf8mb4_bin")
	pdFtLonglongN = pdFt(mysql.TypeLonglong, mysql.BinaryFlag|mysql.NotNullFlag|mysql.IsBooleanFlag, 1, 0, "binary", "binary")
)

var pdColFts = [][]any{pdFtInt, pdFtUint, pdFtTiny, pdFtDouble, pdFtFloat, pdFtDec, pdFtDecWide, pdFtDecBad,
	pdFtVarchar, pdFtVarGen, pdFtVarUni, pdFtVar0900, pdFtVarBin, pdFtVarLatin, pdFtVarGBK, pdFtChar, pdFtBlob,
	pdFtText, pdFtDatetime, pdFtDT3, pdFtDate, pdFtTS, pdFtTS6, pdFtDur, pdFtDur2, pdFtYear, pdFtJSON, pdFtEnum,
	pdFtSet, pdFtBit, pdFtVec, pdFtGeo, pdFtLonglongN}

// pdCastTargets: the result types of CAST recipes.
var pdCastTargets = [][]any{pdFtInt, pdFtUint, pdFtDouble, pdFtFloat, pdFtDec, pdFtDecWide, pdFtDecBad, pdFtVarchar,
	pdFtVarBin, pdFtVarGen, pdFtDatetime, pdFtDT3, pdFtDate, pdFtTS, pdFtDur, pdFtDur2, pdFtJSON, pdFtVec, pdFtYear,
	pdFtTiny}

func pdTime(y, mo, d, h, mi, s, us int, tp byte, fsp int) types.Datum {
	return types.NewTimeDatum(types.NewTime(types.FromDate(y, mo, d, h, mi, s, us), tp, fsp))
}

func pdStrDatum(s, coll string) types.Datum {
	var d types.Datum
	d.SetString(s, coll)
	return d
}

func pdJSONDatum(s string) types.Datum {
	j, err := types.ParseBinaryJSONFromString(s)
	if err != nil {
		panic(err)
	}
	return types.NewJSONDatum(j)
}

func pdVecDatum(s string) types.Datum {
	v, err := types.ParseVectorFloat32(s)
	if err != nil {
		panic(err)
	}
	return types.NewVectorFloat32Datum(v)
}

// pdConsts: constant leaves [datumIn, ftIn].
func pdConsts() [][2]any {
	dec := func(s string) types.Datum {
		x := new(types.MyDecimal)
		must(x.FromString([]byte(s)))
		return types.NewDecimalDatum(x)
	}
	c := func(d types.Datum, ft []any) [2]any { return [2]any{d2DatumInOf(d), ft} }
	var f32 types.Datum
	f32.SetFloat32(1.5)
	return [][2]any{
		c(types.Datum{}, pdFtNull),
		c(types.NewIntDatum(1), pdFtInt),
		c(types.NewIntDatum(-7), pdFtInt),
		c(types.NewIntDatum(0), pdFtLonglongN),
		c(types.NewIntDatum(math.MaxInt64), pdFtInt),
		c(types.NewUintDatum(math.MaxUint64), pdFtUint),
		c(types.NewUintDatum(3), pdFtUint),
		c(types.NewFloat64Datum(2.5), pdFtDouble),
		c(types.NewFloat64Datum(-0.125), pdFtDouble),
		c(f32, pdFtFloat),
		c(pdStrDatum("abc", "utf8mb4_bin"), pdFtVarchar),
		c(pdStrDatum("", "utf8mb4_bin"), pdFtVarchar),
		c(pdStrDatum("Ab", "utf8mb4_general_ci"), pdFtVarGen),
		c(pdStrDatum("\x00\xff", "binary"), pdFtVarBin),
		c(pdStrDatum("2024-02-29 10:11:12", "utf8mb4_bin"), pdFtVarchar),
		c(pdStrDatum("[1,2]", "utf8mb4_bin"), pdFtVarchar),
		c(pdStrDatum("a.*", "utf8mb4_bin"), pdFtVarchar),
		c(types.NewBytesDatum([]byte("xyz")), pdFtVarBin),
		c(types.NewBinaryLiteralDatum([]byte{0x41, 0x42}), pdFtVarBin),
		c(types.NewMysqlBitDatum([]byte{0x05}), pdFtBit),
		c(dec("123.45"), pdFtDec),
		c(dec("-0.000001"), pdFtDecWide),
		c(dec("99999999999999999999999999999999999.999999999999999999999999999999"), pdFtDecWide),
		c(pdTime(2024, 2, 29, 10, 11, 12, 0, mysql.TypeDatetime, 0), pdFtDatetime),
		c(pdTime(2024, 2, 29, 10, 11, 12, 123000, mysql.TypeDatetime, 3), pdFtDT3),
		c(pdTime(1999, 12, 31, 0, 0, 0, 0, mysql.TypeDate, 0), pdFtDate),
		c(pdTime(2020, 1, 1, 0, 30, 0, 0, mysql.TypeTimestamp, 0), pdFtTS),
		c(pdTime(1970, 1, 1, 0, 0, 1, 0, mysql.TypeTimestamp, 6), pdFtTS6),
		c(pdTime(0, 0, 0, 0, 0, 0, 0, mysql.TypeTimestamp, 0), pdFtTS),
		c(pdTime(0, 0, 0, 0, 0, 0, 0, mysql.TypeDatetime, 0), pdFtDatetime),
		c(pdTime(2038, 1, 19, 3, 14, 7, 0, mysql.TypeTimestamp, 0), pdFtTS),
		c(types.NewDurationDatum(types.Duration{Duration: 3*gotime.Hour + 25*gotime.Second, Fsp: 0}), pdFtDur),
		c(types.NewDurationDatum(types.Duration{Duration: -gotime.Hour - 10*gotime.Millisecond, Fsp: 2}), pdFtDur2),
		c(pdJSONDatum(`{"a": [1, "x"]}`), pdFtJSON),
		c(pdJSONDatum(`3`), pdFtJSON),
		c(types.NewMysqlEnumDatum(types.Enum{Name: "b", Value: 2}), pdFtEnum),
		c(types.NewMysqlSetDatum(types.Set{Name: "a,c", Value: 5}, "utf8mb4_bin"), pdFtSet),
		c(pdVecDatum("[1,2.5,-3]"), pdFtVec),
		c(pdVecDatum("[]"), pdFtVec),
	}
}

func pdColRecipe(rng *rand.Rand, i int) any {
	id := int64(i + 1)
	switch rng.IntN(6) {
	case 0:
		id = 0
	case 1:
		id = -1
	}
	return map[string]any{"col": []any{int64(i + 1), i, pdColFts[i], id}}
}

func pdLeaf(rng *rand.Rand, consts [][2]any) any {
	switch r := rng.IntN(10); {
	case r < 6:
		return pdColRecipe(rng, rng.IntN(len(pdColFts)))
	case r < 9:
		k := consts[rng.IntN(len(consts))]
		return map[string]any{"const": []any{k[0], k[1]}}
	default:
		// Correlated columns with one UniqueID share a value (Apply sets them all from the outer
		// row; tidb-rs keeps the values by column, N4 §3.4).
		if rng.IntN(2) == 0 {
			j := rng.IntN(len(consts))
			return map[string]any{"corr": []any{int64(200 + j), consts[j][1], consts[j][0]}}
		}
		i := rng.IntN(len(pdColFts))
		return map[string]any{"corr": []any{int64(100 + i), pdColFts[i], nil}}
	}
}

// pdSmallFuncs: argument trees of nested recipes.
var pdSmallFuncs = []string{ast.Cast, ast.Plus, ast.Upper, ast.Concat, ast.EQ, ast.If, ast.Tan, ast.Left,
	ast.JSONExtract, ast.Date, ast.Hex, ast.Abs, ast.Lower, ast.Length, ast.UnaryMinus, ast.DayName, ast.Coalesce,
	ast.Locate, ast.Truncate, ast.Bin, ast.Floor, ast.Round, ast.ToDays, ast.Reverse}

func pdRecipe(rng *rand.Rand, name string, consts [][2]any, depth int) any {
	n := rng.IntN(5)
	if name == ast.Cast {
		n = 1
	}
	args := []any{}
	for i := 0; i < n; i++ {
		if depth > 0 && rng.IntN(4) == 0 {
			args = append(args, pdRecipe(rng, pdSmallFuncs[rng.IntN(len(pdSmallFuncs))], consts, depth-1))
		} else {
			args = append(args, pdLeaf(rng, consts))
		}
	}
	var rt any
	if name == ast.Cast {
		rt = pdCastTargets[rng.IntN(len(pdCastTargets))]
	}
	return map[string]any{"f": []any{name, rt, args}}
}

// pdTryBuild builds a recipe as Exec will (nil when it fails or panics).
func pdTryBuild(nc bool, r any) (e expression.Expression) {
	defer func() {
		if recover() != nil {
			e = nil
		}
		collate.SetNewCollationEnabledForTest(true)
	}()
	collate.SetNewCollationEnabledForTest(nc)
	e, err := pdBuild(exprstatic.NewExprContext(), vec.MustJSON(r))
	if err != nil {
		return nil
	}
	return e
}

func pdRandOpts(rng *rand.Rand, e expression.Expression, nc bool) pdOptsT {
	o := pdOptsT{NC: nc, Ex: rng.IntN(2) == 0, WH: rng.IntN(4), EP: rng.IntN(3) == 0, Desc: rng.IntN(2) == 0}
	if rng.IntN(6) == 0 {
		o.CL = 1 + rng.IntN(2)
	}
	switch rng.IntN(4) {
	case 0:
		v := (rng.IntN(29) - 14) * 1800
		o.Loc = &v
	case 1:
		v := 0
		o.Loc = &v
	}
	if rng.IntN(6) == 0 {
		var names []string
		if sf, ok := e.(*expression.ScalarFunction); ok {
			names = append(names, sf.FuncName.L, sf.FuncName.L+"."+strings.ToLower(sf.Function.PbCode().String()))
			for _, a := range sf.GetArgs() {
				if af, ok := a.(*expression.ScalarFunction); ok {
					names = append(names, af.FuncName.L)
				}
			}
		}
		names = append(names, "bit", "enum", ast.AggFuncApproxCountDistinct, ast.Plus, ast.EQ, ast.Cast)
		for k := 0; k < 1+rng.IntN(2); k++ {
			o.BL = append(o.BL, []any{names[rng.IntN(len(names))], float64(rng.IntN(8))})
		}
	}
	return o
}

func pushdownGenerate(g *vec.Gen, rng *rand.Rand) {
	consts := pdConsts()
	names := append(expression.GetBuiltinList(), ast.IsTruthWithNull)
	sort.Strings(names)
	nc := func() bool { return rng.IntN(5) != 0 }
	add := func(src string, r any, ncv bool, e expression.Expression) {
		g.Add(src, "expr", r, pdRandOpts(rng, e, ncv))
	}
	// Every built-in function: random argument counts and leaves.
	for _, name := range names {
		if name == ast.Grouping { // metadata of BuiltinGroupingImplSig: D08.9
			continue
		}
		got := 0
		for try := 0; try < 60 && got < 8; try++ {
			ncv := nc()
			r := pdRecipe(rng, name, consts, 0)
			if e := pdTryBuild(ncv, r); e != nil {
				add("rand/func", r, ncv, e)
				got++
			}
		}
	}
	// Nested trees: an argument that cannot be pushed makes the parent not pushable.
	for got, try := 0, 0; got < 1500 && try < 20000; try++ {
		name := names[rng.IntN(len(names))]
		if name == ast.Grouping {
			continue
		}
		ncv := nc()
		r := pdRecipe(rng, name, consts, 2)
		if e := pdTryBuild(ncv, r); e != nil {
			add("rand/nest", r, ncv, e)
			got++
		}
	}
	// CAST from every column type to every target (TiFlash's CAST rules).
	for i := range pdColFts {
		for _, rt := range pdCastTargets {
			r := map[string]any{"f": []any{ast.Cast, rt, []any{pdColRecipe(rng, i)}}}
			ncv := nc()
			if e := pdTryBuild(ncv, r); e != nil {
				add("sys/cast", r, ncv, e)
			}
		}
	}
	// Leaves alone: columns (type checks, ID path) and constants (encodeDatum).
	for rep := 0; rep < 4; rep++ {
		for i := range pdColFts {
			r := pdColRecipe(rng, i)
			add("sys/col", r, nc(), pdTryBuild(true, r))
		}
		for _, k := range consts {
			r := map[string]any{"const": []any{k[0], k[1]}}
			add("sys/const", r, nc(), nil)
			r = map[string]any{"corr": []any{int64(7), k[1], k[0]}}
			add("sys/corr", r, nc(), nil)
		}
	}
	// CONV of a CAST of a hybrid type or a binary literal (scalarExprSupportedByTiKV).
	hy := []any{pdColRecipe(rng, 27), pdColRecipe(rng, 28), pdColRecipe(rng, 29),
		map[string]any{"const": []any{consts[18][0], consts[18][1]}}, pdColRecipe(rng, 8), pdColRecipe(rng, 0)}
	for _, h := range hy {
		for _, rt := range [][]any{pdFtVarchar, pdFtInt, pdFtVarBin} {
			inner := map[string]any{"f": []any{ast.Cast, rt, []any{h}}}
			for _, outer := range []any{inner, map[string]any{"f": []any{ast.Upper, nil, []any{h}}}, h} {
				r := map[string]any{"f": []any{ast.Conv, nil, []any{outer,
					map[string]any{"const": []any{consts[1][0], consts[1][1]}},
					map[string]any{"const": []any{consts[1][0], consts[1][1]}}}}}
				ncv := nc()
				if e := pdTryBuild(ncv, r); e != nil {
					add("sys/conv", r, ncv, e)
				}
			}
		}
	}
	// REGEXP family on binary and non-binary strings; ROUND / RAND by signature.
	strs := []int{8, 9, 12, 13, 16, 17, 0}
	for _, name := range []string{ast.Regexp, ast.RegexpLike, ast.RegexpSubstr, ast.RegexpInStr, ast.RegexpReplace,
		ast.Like, ast.Ilike} {
		for _, a := range strs {
			for _, b := range strs {
				args := []any{pdColRecipe(rng, a), pdColRecipe(rng, b)}
				if name == ast.RegexpReplace {
					args = append(args, map[string]any{"const": []any{consts[10][0], consts[10][1]}})
				}
				if name == ast.Like || name == ast.Ilike {
					args = append(args, map[string]any{"const": []any{d2DatumInOf(types.NewIntDatum('\\')), pdFtInt}})
				}
				r := map[string]any{"f": []any{name, nil, args}}
				ncv := nc()
				if e := pdTryBuild(ncv, r); e != nil {
					add("sys/regexp", r, ncv, e)
				}
			}
		}
	}
	for _, name := range []string{ast.Round, ast.Rand, ast.Truncate, ast.Floor, ast.Ceil, ast.Ceiling} {
		for i := range pdColFts {
			for n := 0; n <= 2; n++ {
				args := []any{}
				for k := 0; k < n; k++ {
					if k == 0 {
						args = append(args, pdColRecipe(rng, i))
					} else {
						args = append(args, map[string]any{"const": []any{consts[1][0], consts[1][1]}})
					}
				}
				if name == ast.Rand && n == 1 && rng.IntN(2) == 0 {
					args[0] = map[string]any{"const": []any{consts[1][0], consts[1][1]}}
				}
				r := map[string]any{"f": []any{name, nil, args}}
				ncv := nc()
				if e := pdTryBuild(ncv, r); e != nil {
					add("sys/sig", r, ncv, e)
				}
			}
		}
	}
	// Split: several conditions at once.
	stores := []uint8{0, 1, 2, 255}
	for got, try := 0, 0; got < 700 && try < 20000; try++ {
		n := 1 + rng.IntN(4)
		rs := []any{}
		ok := true
		ncv := nc()
		var first expression.Expression
		for k := 0; k < n; k++ {
			var r any
			if rng.IntN(5) == 0 {
				r = pdLeaf(rng, consts)
			} else {
				r = pdRecipe(rng, names[rng.IntN(len(names))], consts, 1)
			}
			e := pdTryBuild(ncv, r)
			if e == nil {
				ok = false
				break
			}
			if first == nil {
				first = e
			}
			rs = append(rs, r)
		}
		if !ok || strings.Contains(string(vec.MustJSON(rs)), `"grouping"`) {
			continue
		}
		g.Add("rand/split", "split", rs, pdRandOpts(rng, first, ncv), stores[rng.IntN(len(stores))])
		got++
	}
	// Field types.
	ftStores := []uint8{0, 1, 2, 255}
	for _, nc := range []bool{true, false} {
		for _, ft := range append(append([][]any{}, pdColFts...), pdFtNull, pdFtVarStr,
			pdFt(mysql.TypeNewDecimal, 0, -1, -1, "binary", "binary"),
			pdFt(mysql.TypeNewDecimal, 0, 0, 0, "binary", "binary"),
			pdFt(mysql.TypeNewDecimal, 0, 10, 11, "binary", "binary"),
			pdFt(mysql.TypeVarchar, 0, 10, 0, "utf8mb4", "utf8mb4_zh_pinyin_tidb_as_cs"),
			pdFt(mysql.TypeVarchar, 0, 10, 0, "utf8", "utf8_unicode_ci"),
			pdFt(mysql.TypeVarchar, 0, 10, 0, "ascii", "ascii_bin"),
			pdFt(mysql.TypeVarchar, 0, 10, 0, "gb18030", "gb18030_bin"),
			pdFt(mysql.TypeVarchar, 0, 10, 0, "utf8mb4", "no_such_collation"),
			pdFt(mysql.TypeEnum, 0, 0, 0, "utf8mb4", "utf8mb4_bin"),
		) {
			for _, st := range ftStores {
				g.Add("sys/ft", "ft", ft, nc, st)
			}
		}
	}
	for k := 0; k < 300; k++ {
		var elems any
		if rng.IntN(3) == 0 {
			es := []vec.GoString{}
			// 1..3 elements: Go's nil and empty Elems are one value after a protobuf
			// round trip, and FieldTypeFromPB only ever sees decoded messages.
			for j := 0; j < 1+rng.IntN(3); j++ {
				es = append(es, vec.GoString(fmt.Sprintf("e%d", j)))
			}
			elems = es
		}
		colls := []int32{0, 1, 33, 45, 46, 63, 83, 224, 255, 2048, -1, -33, -45, -46, -63, -224, -255, -2048, 9999}
		css := []string{"", "binary", "utf8mb4", "latin1", "gbk"}
		g.Add("rand/ftpb", "ftpb", int32(rng.IntN(256)), uint32(rng.Uint32()&0xffff), int32(rng.IntN(70)-1),
			int32(rng.IntN(33)-1), colls[rng.IntN(len(colls))], css[rng.IntN(len(css))], elems)
	}
	// Blacklist.
	enNames := []string{ast.Plus, ast.EQ, ast.Cast, "bit", "enum", ast.AggFuncApproxCountDistinct, "lt.ltint",
		ast.Upper, "unknown_fn"}
	for k := 0; k < 400; k++ {
		bl := [][]any{}
		for j := 0; j < rng.IntN(3); j++ {
			bl = append(bl, []any{enNames[rng.IntN(len(enNames))], float64(rng.IntN(8))})
		}
		g.Add("rand/enabled", "enabled", enNames[rng.IntN(len(enNames))], stores[rng.IntN(len(stores))], bl)
	}
	// Comparison and control functions of JSON / DURATION arguments: signatures the TiFlash
	// table excludes by pb code.
	for _, name := range []string{ast.LT, ast.LE, ast.GT, ast.GE, ast.EQ, ast.NE, ast.NullEQ, ast.In, ast.IsNull,
		ast.Coalesce, ast.Ifnull, ast.If, ast.Case, ast.Greatest, ast.Least} {
		for _, c := range []int{26, 23, 24, 0, 8} {
			n := 2
			switch name {
			case ast.IsNull:
				n = 1
			case ast.If, ast.Case, ast.In:
				n = 3
			}
			args := []any{}
			for k := 0; k < n; k++ {
				if (name == ast.If || name == ast.Case) && k == 0 {
					args = append(args, pdColRecipe(rng, 0))
					continue
				}
				args = append(args, pdColRecipe(rng, c))
			}
			r := map[string]any{"f": []any{name, nil, args}}
			ncv := nc()
			if e := pdTryBuild(ncv, r); e != nil {
				add("sys/cmp", r, ncv, e)
			}
		}
	}
	// Push-down contexts.
	for _, fromVars := range []bool{false, true} {
		for _, explain := range []bool{false, true} {
			for wh := 0; wh < 4; wh++ {
				if fromVars && wh > 0 {
					continue
				}
				g.Add("sys/ctx", "ctx", fromVars, explain, wh, uint64(1024+wh))
			}
		}
	}
}
