package domains

// Domain "rangerbuild": block D10.4 (VS1-25, tidb-rs specs/D10.4-ranger) — building ranges
// from conditions, pkg/util/ranger checker.go, detacher.go, points.go, ranger.go. (The Range
// type and its methods are D00.1, domain "ranger".)
//
// Conditions come from SQL, as in pkg/util/ranger/ranger_test.go: each record's tables are
// built from CREATE TABLE statements (ddl.BuildTableInfoFromAST, an InfoSchema of them), the
// query "select * from <t> where <cond>" is preprocessed and built into a logical plan with
// core.MockContext(), and the Selection's conditions (optionally through PushDownNot) are the
// input. Every record runs with new collations enabled, the time zone UTC and plan cache off.
//
// The record's output carries the built input ("in": the conditions and index columns as
// trees) for tidb-rs to rebuild with descriptor-only signatures (as domain "expr"), and the
// results. A tree node is {"col": [id, uid, index, ft, origName, isHidden, virtualExpr|null]},
// {"corr": [id, uid, ft, origName]}, {"const": [datum (d2DatumIn; a decimal also has its
// MarshalJSON state), ft]} or
// {"f": [name.L, name.O, signature type, ft, [args], charset, collation]}; ft is
// [tp, flag, flen, decimal, charset, collate, elems|null].
//
// A detach result is {"r": ranges, "a": [access], "f": [remained], "eq", "eqin", "dnf",
// "mdnf", "cv": columnValues|null, "w": [warnings]} or {"err": msg, "w": [...]}; a range is
// {"s": String(), "l": [d2DatumOut], "h": [...], "le", "he", "c": [collator name]} (the
// collator's name is the first of rbCollNames whose GetCollator is the same interface value,
// "#derived" for collate.GetBinaryCollator()); a column value is null, {"m": true} or
// {"v": d2DatumOut}. Expressions are StringWithCtx strings.
//
// Records:
//   - "index": args [ddls, table, cond, pushNot, indexPos, opts] -> {"in", "idx":
//     DetachCondAndBuildRangeForIndex, "simple": DetachSimpleCondAndBuildRangeForIndex,
//     "part": DetachCondAndBuildRangeForPartition, "eqin": ExtractEqAndInCondition}. opts is
//     {"np": RegardNULLAsPoint, "ps": OptPrefixIndexSingleScan, "f44389", "f54337",
//     "max": rangeMaxSize}.
//   - "column": args [ddls, table, cond, pushNot, colPos, length, max, opts] -> {"in", "ext":
//     ExtractAccessConditionsForColumn, "cr": BuildColumnRange on them, "det":
//     DetachCondsForColumn, "tr": BuildTableRange on its access conditions}.
//   - "merge_dnf": args [ddls, table, cond, pushNot] -> MergeDNFItems4Col on the items of each
//     DNF condition. The merged groups come out of a Go map (random order): each result is
//     sorted (tidb-rs: ascending UniqueID, M-MAPORDER).
//   - "shard": args [ddls, table, cond, pushNot] -> per index: IsValidShardIndex,
//     ExtractColumnsFromExpr of the first column's virtual expression, AddExpr4EqAndInCondition.
//   - "union": args [ranges (rSpec), mergeConsecutive] -> UnionRanges.
//   - "append": args [pointRanges, ranges, max] -> AppendRanges2PointRanges.
//   - "to_string": args [ranges, colNames] -> RangesToString (RangeSingleColToString).
//   - "cut": args [datum, length, ft] -> CutDatumByPrefixLen, ReachPrefixLen.
//   - "null_range": NullRange().

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/ddl"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/infoschema"
	"github.com/pingcap/tidb/pkg/meta/autoid"
	"github.com/pingcap/tidb/pkg/meta/metabuild"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/planner/util/fixcontrol"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/table"
	"github.com/pingcap/tidb/pkg/table/tables"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/ranger"
)

const rangerbuildSeed = 20261118

// RangerbuildMinedTests: the tests of pkg/util/ranger/ranger_test.go whose cases are records.
func RangerbuildMinedTests() []string {
	return []string{"TestTableRange", "TestIndexRangeForUnsignedAndOverflow", "TestColumnRange",
		"TestIndexRangeForYear", "TestPrefixIndexRangeScan", "TestIndexRange", "TestTableShardIndex",
		"TestShardIndexFuncSuites", "TestRangeFallbackForDetachCondAndBuildRangeForIndex",
		"TestRangeFallbackForBuildTableRange", "TestRangeFallbackForBuildColumnRange", "TestPrefixIndexRange",
		"TestIssue40997", "TestIssue50051", "TestMinAccessCondsForDNFCond", "TestBinCollationRangeForIndex"}
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "rangerbuild",
		Seed:     rangerbuildSeed,
		Coverage: rangerbuildCoverage,
		Blocks:   []string{"D10.4"},
		Exempt: map[string]string{
			"point.String": "the Stringer of a point (debugging); no ranger path prints points: Rust unit test",
		},
		Gzip:     true,
		Generate: rangerbuildGenerate,
		Exec:     rangerbuildExec,
	})
}

var rangerbuildCoverage = map[string]vec.Coverage{
	"index": {Min: 2000, GoFuncs: []string{"DetachCondAndBuildRangeForIndex", "DetachSimpleCondAndBuildRangeForIndex",
		"DetachCondAndBuildRangeForPartition", "detachCondAndBuildRange", "rangeDetacher.detachCondAndBuildRangeForCols",
		"rangeDetacher.detachCNFCondAndBuildRangeForIndex", "rangeDetacher.detachDNFCondAndBuildRangeForIndex",
		"rangeDetacher.buildRangeOnColsByCNFCond", "rangeDetacher.buildCNFIndexRange", "ExtractEqAndInCondition",
		"getPotentialEqOrInColOffset", "getCNFItemRangeResult", "compareCNFItemRangeResult", "mergeTwoCNFRanges",
		"extractBestCNFItemRanges", "unionColumnValues", "excludeToIncludeForIntPoint", "allSinglePoints", "allEqOrIn",
		"extractValueInfo", "isSameValue", "removeConditions", "AppendConditionsIfNotExist", "detachColumnCNFConditions",
		"detachColumnDNFConditions", "points2EqOrInCond", "points2Ranges", "appendPoints2Ranges", "appendPoints2IndexRange",
		"AppendRanges2PointRanges", "estimateMemUsageForPoints2Ranges", "estimateMemUsageForAppendPoints2Ranges",
		"estimateMemUsageForAppendRanges2PointRanges", "getRangesTotalDatumSize", "getPointsTotalDatumSize",
		"convertPointsInPlace", "convertPointInPlace", "validInterval", "UnionRanges", "hasPrefix", "cutPrefixForPoints",
		"CutDatumByPrefixLen", "ReachPrefixLen", "newFieldType", "convertStringFTToBinaryCollate",
		"conditionChecker.check", "conditionChecker.checkScalarFunction", "conditionChecker.checkLikeFunc",
		"conditionChecker.matchColumn", "conditionChecker.checkColumn", "conditionChecker.isFullLengthColumn",
		"builder.build", "builder.buildFromConstant", "builder.buildFromColumn", "builder.buildFromBinOp",
		"builder.buildFromIsTrue", "builder.buildFromIsFalse", "builder.buildFromIn", "builder.newBuildFromPatternLike",
		"builder.buildFromNot", "builder.buildFromScalarFunc", "builder.intersection", "builder.union",
		"builder.mergeSorted", "builder.merge", "handleUnsignedCol", "handleBoundCol", "handleEnumFromBinOp",
		"rangePointCmp", "rangePointEnumCmp", "rangePointEqualValueCmp", "convertPointsToSortKeyInPlace",
		"convertPointToSortKeyInPlace", "getFullRange", "getNotNullFullRange"}},
	"column": {Min: 800, GoFuncs: []string{"ExtractAccessConditionsForColumn", "BuildColumnRange", "DetachCondsForColumn",
		"BuildTableRange", "buildColumnRange", "points2TableRanges"}},
	"merge_dnf": {Min: 150, GoFuncs: []string{"MergeDNFItems4Col"}},
	"shard": {Min: 30, GoFuncs: []string{"AddExpr4EqAndInCondition", "AddGcColumnCond", "AddGcColumn4EqCond",
		"AddGcColumn4InCond", "NeedAddGcColumn4ShardIndex", "NeedAddColumn4EqCond", "NeedAddColumn4InCond",
		"ExtractColumnsFromExpr", "IsValidShardIndex"}},
	"union":      {Min: 300, GoFuncs: []string{"UnionRanges"}},
	"append":     {Min: 150, GoFuncs: []string{"AppendRanges2PointRanges"}},
	"to_string":  {Min: 300, GoFuncs: []string{"RangesToString", "RangeSingleColToString"}},
	"cut":        {Min: 200, GoFuncs: []string{"CutDatumByPrefixLen", "ReachPrefixLen"}},
	"null_range": {Min: 1, GoFuncs: []string{"NullRange"}},
}

// ---------------------------------------------------------------- environment

var (
	rbMu   sync.Mutex
	rbCtx  *mock.Context
	rbISes = map[string]infoschema.InfoSchema{}
)

// rbIS builds (once per DDL list) the InfoSchema of the tables; table IDs are 100, 101, ...
func rbIS(ddls []string) (infoschema.InfoSchema, error) {
	key := strings.Join(ddls, "\x00")
	if is, ok := rbISes[key]; ok {
		return is, nil
	}
	var tbls []*model.TableInfo
	for i, d := range ddls {
		stmt, err := parser.New().ParseOneStmt(d, "", "")
		if err != nil {
			return nil, err
		}
		ti, err := ddl.BuildTableInfoFromAST(metabuild.NewContext(), stmt.(*ast.CreateTableStmt))
		if err != nil {
			return nil, err
		}
		ti.ID = int64(100 + i)
		ti.State = model.StatePublic
		tbls = append(tbls, ti)
	}
	// MockTableFromMeta leaves the generated columns' expressions unparsed; TableFromMeta
	// parses them (expression indexes, e.g. tidb_shard()).
	mock := table.MockTableFromMeta
	table.MockTableFromMeta = func(ti *model.TableInfo) table.Table {
		t, err := tables.TableFromMeta(autoid.NewAllocators(false), ti)
		if err != nil {
			panic(err)
		}
		return t
	}
	is := infoschema.MockInfoSchema(tbls)
	table.MockTableFromMeta = mock
	rbISes[key] = is
	return is, nil
}

type rbOpts struct {
	NP     bool  `json:"np"`
	PS     bool  `json:"ps"`
	F44389 bool  `json:"f44389"`
	F54337 bool  `json:"f54337"`
	Max    int64 `json:"max"`
}

var rbDefaultOpts = rbOpts{NP: true, PS: true}

// rbSession prepares the mock context for one record: fresh statement context, ids from 0.
func rbSession(o rbOpts) *mock.Context {
	collate.SetNewCollationEnabledForTest(true)
	if rbCtx == nil {
		rbCtx = core.MockContext()
	}
	vars := rbCtx.GetSessionVars()
	vars.PlanColumnID.Store(0)
	vars.PlanID.Store(0)
	vars.TimeZone = gotime.UTC
	vars.StmtCtx = stmtctx.NewStmtCtxWithTimeZone(gotime.UTC)
	vars.RegardNULLAsPoint = o.NP
	vars.OptPrefixIndexSingleScan = o.PS
	vars.OptimizerFixControl = map[uint64]string{}
	if o.F44389 {
		vars.OptimizerFixControl[fixcontrol.Fix44389] = "ON"
	}
	if o.F54337 {
		vars.OptimizerFixControl[fixcontrol.Fix54337] = "ON"
	}
	return rbCtx
}

// rbResetStmt gives the ranger call its own statement context (its warnings).
func rbResetStmt(sctx *mock.Context) {
	sctx.GetSessionVars().StmtCtx = stmtctx.NewStmtCtxWithTimeZone(gotime.UTC)
}

// rbSelect plans "select * from <tbl> where <cond>" and returns the conditions, the
// Selection's schema columns and the table.
func rbSelect(sctx *mock.Context, ddls []string, tbl, cond string, pushNot bool) ([]expression.Expression,
	[]*expression.Column, *model.TableInfo, error) {
	is, err := rbIS(ddls)
	if err != nil {
		return nil, nil, nil, err
	}
	stmt, err := parser.New().ParseOneStmt("select * from "+tbl+" where "+cond, "", "")
	if err != nil {
		return nil, nil, nil, err
	}
	nodeW := resolve.NewNodeW(stmt)
	ret := &core.PreprocessorReturn{InfoSchema: is}
	if err := core.Preprocess(context.Background(), sctx, nodeW, core.WithPreprocessorReturn(ret)); err != nil {
		return nil, nil, nil, err
	}
	p, err := core.BuildLogicalPlanForTest(context.Background(), sctx, nodeW, is)
	if err != nil {
		return nil, nil, nil, err
	}
	sel, ok := p.(base.LogicalPlan).Children()[0].(*logicalop.LogicalSelection)
	if !ok {
		return nil, nil, nil, errors.New("no selection")
	}
	ds, ok := sel.Children()[0].(*logicalop.DataSource)
	if !ok {
		return nil, nil, nil, errors.New("no data source")
	}
	conds := make([]expression.Expression, len(sel.Conditions))
	for i, c := range sel.Conditions {
		if pushNot {
			conds[i] = expression.PushDownNot(sctx.GetExprCtx(), c)
		} else {
			conds[i] = c
		}
	}
	return conds, sel.Schema().Columns, ds.TableInfo, nil
}

// ---------------------------------------------------------------- outputs

func rbDump(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		var ve any
		if x.VirtualExpr != nil {
			ve = rbDump(x.VirtualExpr)
		}
		return map[string]any{"col": []any{x.ID, x.UniqueID, x.Index, sfFtIn(x.RetType), vec.GoString(x.OrigName),
			x.IsHidden, ve}}
	case *expression.CorrelatedColumn:
		return map[string]any{"corr": []any{x.ID, x.UniqueID, sfFtIn(x.RetType), vec.GoString(x.OrigName)}}
	case *expression.Constant:
		if x.ParamMarker != nil || x.DeferredExpr != nil {
			panic("rbDump: mutable constant")
		}
		d := d2DatumInOf(x.Value)
		if x.Value.Kind() == types.KindMysqlDecimal {
			// The decimal's state too: constant folding leaves states FromString does not give.
			st, err := x.Value.GetMysqlDecimal().MarshalJSON()
			must(err)
			d = append(d.([]any), string(st))
		}
		return map[string]any{"const": []any{d, sfFtIn(x.RetType)}}
	case *expression.ScalarFunction:
		args := []any{}
		for _, a := range x.GetArgs() {
			args = append(args, rbDump(a))
		}
		chs, coll := x.CharsetAndCollation()
		return map[string]any{"f": []any{x.FuncName.L, x.FuncName.O, reflect.TypeOf(x.Function).Elem().Name(),
			sfFtIn(x.RetType), args, chs, coll}}
	}
	panic(fmt.Sprintf("rbDump %T", e))
}

func rbDumps[T expression.Expression](es []T) []any {
	out := make([]any, 0, len(es))
	for _, e := range es {
		out = append(out, rbDump(e))
	}
	return out
}

func rbStrs(sctx *mock.Context, es []expression.Expression) []vec.GoString {
	ectx := sctx.GetExprCtx().GetEvalCtx()
	out := make([]vec.GoString, 0, len(es))
	for _, e := range es {
		if e == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, vec.GoString(e.StringWithCtx(ectx, errors.RedactLogDisable)))
	}
	return out
}

// rbCollNames: the names a collator is reported by (first match).
var rbCollNames = []string{"binary", "utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci",
	"utf8mb4_0900_bin", "utf8_bin", "utf8_general_ci", "utf8_unicode_ci", "ascii_bin", "latin1_bin", "gbk_bin",
	"gbk_chinese_ci", "gb18030_bin", "gb18030_chinese_ci", "utf8mb4_zh_pinyin_tidb_as_cs"}

func rbCollName(c collate.Collator) any {
	if c == nil {
		return nil
	}
	if c == collate.GetBinaryCollator() {
		return "#derived"
	}
	for _, n := range rbCollNames {
		if collate.GetCollator(n) == c {
			return n
		}
	}
	return "#unknown"
}

func rbRange(r *ranger.Range) any {
	cs := []any{}
	for _, c := range r.Collators {
		cs = append(cs, rbCollName(c))
	}
	return map[string]any{"s": vec.GoString(r.String()), "l": datumOuts(r.LowVal), "h": datumOuts(r.HighVal),
		"le": r.LowExclude, "he": r.HighExclude, "c": cs}
}

func rbRanges(rs ranger.Ranges) []any {
	out := make([]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, rbRange(r))
	}
	return out
}

func rbWarns(sctx *mock.Context) []vec.GoString {
	out := []vec.GoString{}
	for _, w := range sctx.GetSessionVars().StmtCtx.GetWarnings() {
		out = append(out, vec.GoString(w.Err.Error()))
	}
	return out
}

func rbErr(sctx *mock.Context, err error) map[string]any {
	return map[string]any{"err": vec.GoString(err.Error()), "w": rbWarns(sctx)}
}

// rbColValues: the unexported []*valueInfo of DetachRangeResult, read by reflection.
func rbColValues(v reflect.Value) any {
	if v.IsNil() {
		return nil
	}
	out := make([]any, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		p := v.Index(i)
		if p.IsNil() {
			out = append(out, nil)
			continue
		}
		vi := p.Elem()
		if vi.FieldByName("mutable").Bool() {
			out = append(out, map[string]any{"m": true})
			continue
		}
		dp := vi.FieldByName("value")
		d := *(*types.Datum)(dp.UnsafePointer())
		out = append(out, map[string]any{"v": d2DatumOut(d)})
	}
	return out
}

func rbDetach(sctx *mock.Context, res *ranger.DetachRangeResult, err error) any {
	if err != nil {
		return rbErr(sctx, err)
	}
	return map[string]any{"r": rbRanges(res.Ranges), "a": rbStrs(sctx, res.AccessConds),
		"f": rbStrs(sctx, res.RemainedConds), "eq": res.EqCondCount, "eqin": res.EqOrInCount, "dnf": res.IsDNFCond,
		"mdnf": res.MinAccessCondsForDNFCond, "cv": rbColValues(reflect.ValueOf(res.ColumnValues)), "w": rbWarns(sctx)}
}

func rbTry(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": vec.GoString(fmt.Sprint(r))}
		}
	}()
	return f()
}

// ---------------------------------------------------------------- exec

func rangerbuildExec(op string, p []json.RawMessage) (out any, err error) {
	rbMu.Lock()
	defer rbMu.Unlock()
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "index":
		o := arg[rbOpts](p, 5)
		sctx := rbSession(o)
		conds, schemaCols, tbl, err := rbSelect(sctx, arg[[]string](p, 0), arg[string](p, 1), arg[string](p, 2), arg[bool](p, 3))
		if err != nil {
			return map[string]any{"plan": vec.GoString(err.Error())}, nil
		}
		idx := tbl.Indices[arg[int](p, 4)]
		cols, lengths := plannerutil.IndexInfo2PrefixCols(tbl.Columns, schemaCols, idx)
		m := map[string]any{"in": map[string]any{"conds": rbDumps(conds), "cols": rbDumps(cols), "lengths": lengths}}
		if len(cols) == 0 {
			return m, nil
		}
		m["idx"] = rbTry(func() any {
			rbResetStmt(sctx)
			res, err := ranger.DetachCondAndBuildRangeForIndex(sctx.GetRangerCtx(), conds, cols, lengths, o.Max)
			return rbDetach(sctx, res, err)
		})
		m["simple"] = rbTry(func() any {
			rbResetStmt(sctx)
			rs, a, f, err := ranger.DetachSimpleCondAndBuildRangeForIndex(sctx.GetRangerCtx(), conds, cols, lengths, o.Max)
			if err != nil {
				return rbErr(sctx, err)
			}
			return map[string]any{"r": rbRanges(rs), "a": rbStrs(sctx, a), "f": rbStrs(sctx, f), "w": rbWarns(sctx)}
		})
		m["part"] = rbTry(func() any {
			rbResetStmt(sctx)
			res, err := ranger.DetachCondAndBuildRangeForPartition(sctx.GetRangerCtx(), conds, cols, lengths, o.Max)
			return rbDetach(sctx, res, err)
		})
		m["eqin"] = rbTry(func() any {
			rbResetStmt(sctx)
			a, f, n, cv, empty := ranger.ExtractEqAndInCondition(sctx.GetRangerCtx(), conds, cols, lengths)
			if empty {
				return map[string]any{"empty": true}
			}
			return map[string]any{"a": rbStrs(sctx, a), "f": rbStrs(sctx, f), "n": rbStrs(sctx, n),
				"cv": rbColValues(reflect.ValueOf(cv))}
		})
		return m, nil
	case "column":
		o := arg[rbOpts](p, 7)
		sctx := rbSession(o)
		conds, schemaCols, tbl, err := rbSelect(sctx, arg[[]string](p, 0), arg[string](p, 1), arg[string](p, 2), arg[bool](p, 3))
		if err != nil {
			return map[string]any{"plan": vec.GoString(err.Error())}, nil
		}
		col := expression.ColInfo2Col(schemaCols, tbl.Columns[arg[int](p, 4)])
		length, maxSize := arg[int](p, 5), arg[int64](p, 6)
		m := map[string]any{"in": map[string]any{"conds": rbDumps(conds), "col": rbDump(col)}}
		ext := ranger.ExtractAccessConditionsForColumn(sctx.GetRangerCtx(), conds, col)
		m["ext"] = rbStrs(sctx, ext)
		m["cr"] = rbTry(func() any {
			rbResetStmt(sctx)
			rs, a, f, err := ranger.BuildColumnRange(ext, sctx.GetRangerCtx(), col.RetType, length, maxSize)
			if err != nil {
				return rbErr(sctx, err)
			}
			return map[string]any{"r": rbRanges(rs), "a": rbStrs(sctx, a), "f": rbStrs(sctx, f), "w": rbWarns(sctx)}
		})
		access, filter := ranger.DetachCondsForColumn(sctx.GetRangerCtx(), conds, col)
		m["det"] = map[string]any{"a": rbStrs(sctx, access), "f": rbStrs(sctx, filter)}
		m["tr"] = rbTry(func() any {
			rbResetStmt(sctx)
			rs, a, f, err := ranger.BuildTableRange(access, sctx.GetRangerCtx(), col.RetType, maxSize)
			if err != nil {
				return rbErr(sctx, err)
			}
			return map[string]any{"r": rbRanges(rs), "a": rbStrs(sctx, a), "f": rbStrs(sctx, f), "w": rbWarns(sctx)}
		})
		return m, nil
	case "merge_dnf":
		sctx := rbSession(rbDefaultOpts)
		conds, _, _, err := rbSelect(sctx, arg[[]string](p, 0), arg[string](p, 1), arg[string](p, 2), arg[bool](p, 3))
		if err != nil {
			return map[string]any{"plan": vec.GoString(err.Error())}, nil
		}
		res := []any{}
		for _, c := range conds {
			sf, ok := c.(*expression.ScalarFunction)
			if !ok || sf.FuncName.L != ast.LogicOr {
				continue
			}
			merged := rbStrs(sctx, ranger.MergeDNFItems4Col(sctx.GetRangerCtx(), expression.FlattenDNFConditions(sf)))
			slices.Sort(merged)
			res = append(res, merged)
		}
		return map[string]any{"in": map[string]any{"conds": rbDumps(conds)}, "merged": res}, nil
	case "shard":
		sctx := rbSession(rbDefaultOpts)
		conds, schemaCols, tbl, err := rbSelect(sctx, arg[[]string](p, 0), arg[string](p, 1), arg[string](p, 2), arg[bool](p, 3))
		if err != nil {
			return map[string]any{"plan": vec.GoString(err.Error())}, nil
		}
		idxs := []any{}
		for _, idx := range tbl.Indices {
			cols, _ := plannerutil.IndexInfo2PrefixCols(tbl.Columns, schemaCols, idx)
			r := map[string]any{"cols": rbDumps(cols), "valid": ranger.IsValidShardIndex(cols)}
			if len(cols) > 0 {
				sf, _ := cols[0].VirtualExpr.(*expression.ScalarFunction)
				fs := []expression.Expression{}
				for _, c := range ranger.ExtractColumnsFromExpr(sf) {
					fs = append(fs, c)
				}
				r["fields"] = rbStrs(sctx, fs)
				r["add"] = rbTry(func() any {
					nc, err := ranger.AddExpr4EqAndInCondition(sctx.GetRangerCtx(), conds, cols)
					if err != nil {
						return rbErr(sctx, err)
					}
					return rbStrs(sctx, nc)
				})
			}
			idxs = append(idxs, r)
		}
		return map[string]any{"in": map[string]any{"conds": rbDumps(conds)}, "idx": idxs}, nil
	case "union":
		rbSession(rbDefaultOpts)
		rs := rangesIn(p[0])
		return rbTry(func() any {
			rbResetStmt(rbCtx)
			out, err := ranger.UnionRanges(rbCtx.GetRangerCtx(), rs, arg[bool](p, 1))
			if err != nil {
				return rbErr(rbCtx, err)
			}
			return map[string]any{"r": rbRanges(out)}
		}), nil
	case "append":
		pr, rs := rangesIn(p[0]), rangesIn(p[1])
		out, fb := ranger.AppendRanges2PointRanges(pr, rs, arg[int64](p, 2))
		return map[string]any{"r": rbRanges(out), "fb": fb}, nil
	case "to_string":
		rbSession(rbDefaultOpts)
		rs := rangesIn(p[0])
		return rbTry(func() any {
			s, err := ranger.RangesToString(rbCtx.GetSessionVars().StmtCtx, rs, arg[[]string](p, 1))
			if err != nil {
				return map[string]any{"err": vec.GoString(err.Error())}
			}
			return vec.GoString(s)
		}), nil
	case "cut":
		d := d2DatumIn(exArr(p[0]))
		length := arg[int](p, 1)
		ft := d2FtIn(exArr(p[2]))
		reach := ranger.ReachPrefixLen(&d, length, ft)
		cut := ranger.CutDatumByPrefixLen(&d, length, ft)
		return map[string]any{"reach": reach, "cut": cut, "d": d2DatumOut(d)}, nil
	case "null_range":
		return rbRanges(ranger.NullRange()), nil
	}
	return nil, errors.Errorf("rangerbuild: unknown op %s", op)
}

// ---------------------------------------------------------------- generator

// rbCase: the fields of the case tables of ranger_test.go (rangerbuild_cases.go).
type rbCase struct {
	indexPos                 int
	colPos                   int
	length                   int
	childLevel               int
	minAccessCondsForDNFCond int
	tableName                string
	exprStr                  string
	accessConds              string
	filterConds              string
	resultStr                string
}

// rbCheck compares a mined case with the test's expected strings: the harness must give the
// test's results (a mismatch means the generator does not run what the test runs).
func rbCheck(src string, c rbCase, got map[string]any, key string) {
	r, ok := got[key].(map[string]any)
	if !ok {
		panic(fmt.Sprintf("%s %q: no %s result: %v", src, c.exprStr, key, got))
	}
	join := func(v any) string {
		ss := v.([]vec.GoString)
		parts := make([]string, len(ss))
		for i, s := range ss {
			parts[i] = string(s)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	if c.accessConds != "" && join(r["a"]) != c.accessConds {
		panic(fmt.Sprintf("%s %q: access %s, test wants %s", src, c.exprStr, join(r["a"]), c.accessConds))
	}
	if c.filterConds != "" && join(r["f"]) != c.filterConds {
		panic(fmt.Sprintf("%s %q: filter %s, test wants %s", src, c.exprStr, join(r["f"]), c.filterConds))
	}
	if c.resultStr != "" {
		rs := r["r"].([]any)
		parts := make([]string, len(rs))
		for i, x := range rs {
			parts[i] = string(x.(map[string]any)["s"].(vec.GoString))
		}
		if s := "[" + strings.Join(parts, " ") + "]"; s != c.resultStr {
			panic(fmt.Sprintf("%s %q: ranges %s, test wants %s", src, c.exprStr, s, c.resultStr))
		}
	}
}

func rbAddIndex(g *vec.Gen, src string, ddls []string, tbl, cond string, pushNot bool, idx int, o rbOpts) map[string]any {
	g.Add(src, "index", ddls, tbl, cond, pushNot, idx, o)
	out, _ := rangerbuildExec("index", g.Calls[len(g.Calls)-1].Args)
	return out.(map[string]any)
}

func rbAddColumn(g *vec.Gen, src string, ddls []string, tbl, cond string, pushNot bool, col, length int, max int64, o rbOpts) map[string]any {
	g.Add(src, "column", ddls, tbl, cond, pushNot, col, length, max, o)
	out, _ := rangerbuildExec("column", g.Calls[len(g.Calls)-1].Args)
	return out.(map[string]any)
}

func rangerbuildGenerate(g *vec.Gen, rng *rand.Rand) {
	rbGenMined(g)
	rbGenRandom(g, rng)
	rbGenDirect(g, rng)
}

func rbGenMined(g *vec.Gen) {
	unspec := types.UnspecifiedLength
	// TestTableRange: DetachCondsForColumn + BuildTableRange on t.a.
	ddl := []string{"create table t(a int, b int, c int unsigned)"}
	for _, c := range rbTableRange {
		out := rbAddColumn(g, "TestTableRange", ddl, "t", c.exprStr, true, 0, unspec, 0, rbDefaultOpts)
		det := out["det"].(map[string]any)
		tr := out["tr"].(map[string]any)
		rbCheck("TestTableRange", rbCase{exprStr: c.exprStr, accessConds: c.accessConds, filterConds: c.filterConds},
			map[string]any{"x": map[string]any{"a": det["a"], "f": det["f"], "r": tr["r"]}}, "x")
		rbCheck("TestTableRange", rbCase{exprStr: c.exprStr, resultStr: c.resultStr}, out, "tr")
	}
	ddl = []string{`create table t(
	a smallint(5) unsigned,
	decimal_unsigned decimal unsigned,
	float_unsigned float unsigned,
	double_unsigned double unsigned,
	col_int bigint,
	col_float float,
	index idx_a(a),
	index idx_decimal_unsigned(decimal_unsigned),
	index idx_float_unsigned(float_unsigned),
	index idx_double_unsigned(double_unsigned),
	index idx_int(col_int),
	index idx_float(col_float),
	index idx_int_bigint(a, col_int)
)`}
	for _, c := range rbIndexRangeForUnsignedAndOverflow {
		out := rbAddIndex(g, "TestIndexRangeForUnsignedAndOverflow", ddl, "t", c.exprStr, true, c.indexPos, rbDefaultOpts)
		rbCheck("TestIndexRangeForUnsignedAndOverflow", c, out, "idx")
	}
	// TestColumnRange: ExtractAccessConditionsForColumn + BuildColumnRange.
	ddl = []string{"create table t(a int, b double, c float(3, 2), d varchar(3), e bigint unsigned)"}
	for _, c := range rbColumnRange {
		out := rbAddColumn(g, "TestColumnRange", ddl, "t", c.exprStr, true, c.colPos, c.length, 0, rbDefaultOpts)
		cr := out["cr"].(map[string]any)
		rbCheck("TestColumnRange", rbCase{exprStr: c.exprStr, accessConds: c.accessConds},
			map[string]any{"x": map[string]any{"a": out["ext"]}}, "x")
		rbCheck("TestColumnRange", rbCase{exprStr: c.exprStr, resultStr: c.resultStr}, map[string]any{"x": cr}, "x")
	}
	ddl = []string{"CREATE TABLE t (a year(4), key(a))"}
	for _, c := range rbIndexRangeForYear {
		out := rbAddIndex(g, "TestIndexRangeForYear", ddl, "t", c.exprStr, true, c.indexPos, rbDefaultOpts)
		rbCheck("TestIndexRangeForYear", c, out, "idx")
	}
	ddl = []string{"create table t (a varchar(50), b varchar(50), index idx_a(a(2)), index idx_ab(a(2), b(2)))"}
	for _, c := range rbPrefixIndexRangeScan {
		out := rbAddIndex(g, "TestPrefixIndexRangeScan", ddl, "t", c.exprStr, true, c.indexPos, rbDefaultOpts)
		rbCheck("TestPrefixIndexRangeScan", c, out, "idx")
	}
	ddl = []string{`create table t(
	a varchar(50),
	b int,
	c double,
	d varchar(10),
	e binary(10),
	f varchar(10) collate utf8mb4_general_ci,
	g enum('A','B','C') collate utf8mb4_general_ci,
	h varchar(10) collate utf8_bin,
	index idx_ab(a(50), b),
	index idx_cb(c, a),
	index idx_d(d(2)),
	index idx_e(e(2)),
	index idx_f(f),
	index idx_de(d(2), e),
	index idx_g(g),
	index idx_h(h(3))
)`}
	for _, c := range rbIndexRange {
		out := rbAddIndex(g, "TestIndexRange", ddl, "t", c.exprStr, true, c.indexPos, rbDefaultOpts)
		rbCheck("TestIndexRange", c, out, "idx")
	}
	ddl = []string{`create table t(
	a varchar(50),
	b varchar(50),
	c text(50),
	d varbinary(50),
	index idx_a(a(2)),
	index idx_ab(a(2), b(2)),
	index idx_c(c(2)),
	index idx_d(d(2))
)`}
	for _, c := range rbPrefixIndexRange {
		out := rbAddIndex(g, "TestPrefixIndexRange", ddl, "t", c.exprStr, true, c.indexPos, rbOpts{NP: true, PS: true})
		rbCheck("TestPrefixIndexRange", c, out, "idx")
	}
	ddl = []string{`create table t(a int, b int, c int, d int,
		index ia(a), index ib(b), index ic(c), index iabc(a,b,c), index iab(a,b))`}
	for _, c := range rbMinAccessCondsForDNFCond {
		out := rbAddIndex(g, "TestMinAccessCondsForDNFCond", ddl, "t", c.exprStr, true, c.indexPos, rbDefaultOpts)
		rbCheck("TestMinAccessCondsForDNFCond", rbCase{exprStr: c.exprStr, accessConds: c.accessConds}, out, "idx")
		if got := out["idx"].(map[string]any)["mdnf"].(int); got != c.minAccessCondsForDNFCond {
			panic(fmt.Sprintf("TestMinAccessCondsForDNFCond %q: mdnf %d, test wants %d", c.exprStr, got, c.minAccessCondsForDNFCond))
		}
	}
	// TestBinCollationRangeForIndex (DetachSimpleCondAndBuildRangeForIndex / ...ForIndex).
	ddl = []string{"create table t (f varchar(10) collate utf8mb4_general_ci, index idx_f(f))"}
	for _, cond := range []string{"f = cast('abc' as binary)", "f in (cast('a' as binary), cast('B' as binary))",
		"f = cast('a' as binary) and f > 'a'", "f <=> cast('a' as binary)", "f > cast('a' as binary)",
		"f like cast('a%' as binary)"} {
		rbAddIndex(g, "TestBinCollationRangeForIndex", ddl, "t", cond, false, 0, rbDefaultOpts)
	}
	// TestRangeFallback*: the queries with a sweep of rangeMaxSize (the test uses MemUsage()-1).
	quotas := []int64{0, 1, 100, 200, 300, 400, 500, 600, 800, 1000, 1200, 1500, 2000, 3000, 5000}
	ddl = []string{"create table t1 (a int, b int, c int, d int, index idx(a, b, c))"}
	for _, cond := range []string{"a in (10,20,30) and b in (40,50,60) and c >= 70 and c <= 80",
		"a in (10,20,30) and b in (40,50,60)", "a = 10 or a = 20 or a = 30",
		"(a = 10 and b = 40) or (a = 20 and b = 50) or (a = 30 and b = 60)", "(a, b) in ((10, 20), (30, 40)) and c = 50"} {
		for _, q := range quotas {
			rbAddIndex(g, "TestRangeFallbackForDetachCondAndBuildRangeForIndex", ddl, "t1", cond, false, 0,
				rbOpts{NP: true, PS: true, Max: q})
		}
	}
	ddl = []string{"create table t2 (a varchar(10), b varchar(10), c varchar(10), d varchar(10), index idx(a(2), b(2), c(2)))"}
	for _, cond := range []string{"a in ('aaa','bbb','ccc') and b in ('ddd','eee','fff') and c >= 'ggg' and c <= 'iii'",
		"a = 'aaa' or a = 'bbb' or a = 'ccc'", "(a = 'aaa' and b = 'ddd') or (a = 'bbb' and b = 'eee') or (a = 'ccc' and b = 'fff')",
		"(a, b) in (('aaa', 'bbb'), ('ccc', 'ddd')) and c = 'eee'"} {
		for _, q := range quotas {
			rbAddIndex(g, "TestRangeFallbackForDetachCondAndBuildRangeForIndex", ddl, "t2", cond, false, 0,
				rbOpts{NP: true, PS: true, Max: q})
		}
	}
	ddl = []string{"create table t (a int primary key, b int)"}
	for _, q := range quotas {
		rbAddColumn(g, "TestRangeFallbackForBuildTableRange", ddl, "t", "a in (10,20,30,40,50)", false, 0, unspec, q, rbDefaultOpts)
	}
	ddl = []string{"create table t (a varchar(20), b int not null)"}
	for _, q := range quotas {
		rbAddColumn(g, "TestRangeFallbackForBuildColumnRange", ddl, "t", "a in ('aaa','bbb','ccc','ddd','eee')", false, 0, unspec, q, rbDefaultOpts)
		rbAddColumn(g, "TestRangeFallbackForBuildColumnRange", ddl, "t", "b in (10,20,30)", false, 1, unspec, q, rbDefaultOpts)
	}
	// TestIssue40997 / TestIssue50051 (EXPLAIN / query tests): their conditions on the index.
	ddl = []string{`CREATE TABLE t71706696 (
		dt char(8) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
		db_id bigint(20) NOT NULL,
		tbl_id bigint(20) NOT NULL,
		db_name varchar(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
		UNIQUE KEY dt_2 (dt, db_id, tbl_id)
	)`}
	rbAddIndex(g, "TestIssue40997", ddl, "t71706696", `(dt = '20210112' AND db_id = '62812' AND tbl_id > '228892694') OR
		(dt = '20210112' AND db_id = '62813' AND tbl_id <= '226785696') OR (dt = '20210112' AND db_id > '62812' AND db_id < '62813')`,
		true, 0, rbDefaultOpts)
	ddl = []string{"CREATE TABLE tt (c bigint UNSIGNED not null, d int not null, PRIMARY KEY (c,d))",
		"CREATE TABLE t5 (d int not null, c int not null, PRIMARY KEY (d, c))"}
	rbAddIndex(g, "TestIssue50051", ddl, "tt", "c>9223372036854775807 AND c>1", true, 0, rbDefaultOpts)
	rbAddIndex(g, "TestIssue50051", ddl, "t5", "d < 0 and d < 3", true, 0, rbDefaultOpts)
	rbAddIndex(g, "TestIssue50051", ddl, "t5", "d < 18446744073709551615 and d < 3", true, 0, rbDefaultOpts)
	// TestTableShardIndex / TestShardIndexFuncSuites.
	ddl = []string{"create table test3(id int primary key clustered, a int, b int, unique key uk_expr((tidb_shard(a)),a))",
		"create table test33(id int primary key clustered, a int, b int, unique key a(a))",
		"create table test4(id int primary key clustered, a int, b int, unique key uk_expr((tidb_shard(a)),a),unique key uk_b_expr((tidb_shard(b)),b))",
		"create table test5(id int primary key clustered, a int, b int, unique key uk_expr((tidb_shard(a)),a,b))",
		"create table test6(id int primary key clustered, a int, b int, c int, unique key uk_expr((tidb_shard(a)), a))",
		"create table testx(id int primary key clustered, a int, b int, unique key a(a))",
		"create table testy(id int primary key clustered, a int, b int, unique key uk_expr((tidb_shard(b)),a))",
		"create table testz(id int primary key clustered, a int, b int, unique key uk_expr((tidb_shard(a+b)),a))"}
	for _, c := range rbTableShardIndex {
		g.Add("TestTableShardIndex", "shard", ddl, c.tableName, c.exprStr, true)
	}
	for _, cond := range []string{"a = 1 and b = 2", "a in (1, 2, 3)", "a = 1 or a = 2", "a > 1", "b = 1",
		"a = 1 and a = 2", "a in (1) and b in (2)", "a = 1 and b in (1, 2)"} {
		for _, tbl := range []string{"test3", "test5", "test6", "testz"} {
			g.Add("TestShardIndexFuncSuites", "shard", ddl, tbl, cond, true)
		}
	}
}
