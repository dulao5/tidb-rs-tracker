package domains

// Domain "planutil": block D10.10, the VS1-19 part (tidb-rs specs/VS1-vertical-slice) —
// pkg/planner/property (physical property, stats info, task type) and pkg/planner/util
// (access paths, handle columns, index columns, ORDER BY items, misc helpers, cost model v2
// bookkeeping in util/costusage).
//
// Inputs:
//   - a column is [uniqueID, tp, flen]: tp "int" (TypeLonglong) or "str" (TypeVarchar with
//     collation utf8mb4_bin); Index is set where an op says so.
//   - a datum is {"k": "null"|"i"|"u"|"s"|"min"|"max", "v": value} ("s" is a string with
//     collation utf8mb4_bin).
//   - a range is [low datums, high datums, lowExclude, highExclude]; its collators are
//     binary.
//   - a sort item is [uniqueID, desc]; its column is an int column.
//
// Records:
//   - "col2len": args [c1, c2] (each [[id, len]...]) -> CompareCol2Len.
//   - "only_point": args [isIntHandle, indexCols, ranges] -> AccessPath.OnlyPointRange
//     (indexCols < 0: no index).
//   - "full_scan": args [isIntHandle, pkIsHandle, unsigned, ranges] -> IsFullScanRange.
//   - "path_flags": args [isInt, isCommon, hasIndex, storeType, mv, cond] -> the IsXxxPath
//     predicates, IsUndetermined, IsIndexJoinUnapplicable.
//   - "index_cols": args [colInfos [[name, flen]], cols, idxCols [[name, length]]] ->
//     IndexInfo2Cols and the prefix-only / full-only variants.
//   - "prop": args [spec, other spec, schema uniqueIDs, keys] -> the PhysicalProperty
//     predicates, HashCode, MemoryUsage, CloneEssentialFields, IsSubsetOf.
//   - "stats": args [spec] -> StatsInfo methods, DeriveLimitStats, ToString.
//   - "cost": args [trace, factors [[name, value, cost]], div, mul, flags] -> cost model v2
//     helpers.
//   - "handle_int": args [col, a, b, schema] -> IntHandleCols methods; "handle_common": args
//     [tblCols, idxCols [[offset, length]], a, b, schema] -> CommonHandleCols methods; both
//     also build handles from a chunk row of the datums.
//   - "split_cor": args [idxCols, idxColLens, filters, eqOrInCount] -> the access / remained
//     filters of SplitCorColAccessCondFromFilters (as filter positions), whether the plan
//     cache was skipped, and GetCol2LenFromAccessConds / ExtractCol2Len.
//   - "byitems": args [items, other items] -> ByItems methods.
//   - "misc": args [what, ...] -> the remaining helpers (see planutilMisc).
//
// Every record runs with new collations enabled (the planner reads collate.NewCollationEnabled).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	perrors "github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/ast"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/property"
	"github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/planner/util/costusage"
	"github.com/pingcap/tidb/pkg/planner/util/optimizetrace"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/ranger"
)

const planutilSeed = 20261115

//go:linkname puMPPHashCode github.com/pingcap/tidb/pkg/planner/property.(*MPPPartitionColumn).hashCode
func puMPPHashCode(c *property.MPPPartitionColumn) []byte

// PlanutilMinedTests: the Go tests whose cases are records.
func PlanutilMinedTests() []string {
	return []string{"TestCompareCol2Len", "TestOnlyPointRange", "TestIndexInfo2Cols"}
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "planutil",
		Seed:     planutilSeed,
		Coverage: planutilCoverage,
		Blocks:   []string{"D10.10"},
		Exempt:   planutilExempt(),
		Gzip:     true,
		Generate: planutilGenerate,
		Exec:     planutilExec,
	})
}

var planutilCoverage = map[string]vec.Coverage{
	"col2len":    {Min: 200, GoFuncs: []string{"CompareCol2Len", "Col2Len.dominate", "compareLength"}},
	"only_point": {Min: 200, GoFuncs: []string{"AccessPath.OnlyPointRange"}},
	"full_scan":  {Min: 64, GoFuncs: []string{"AccessPath.IsFullScanRange"}},
	"path_flags": {Min: 64, GoFuncs: []string{"AccessPath.IsTablePath", "AccessPath.IsTiKVTablePath",
		"AccessPath.IsTiFlashSimpleTablePath", "AccessPath.IsUndetermined", "AccessPath.IsIndexJoinUnapplicable"}},
	"index_cols": {Min: 200, GoFuncs: []string{"indexCol2Col", "indexInfo2ColsImpl", "IndexInfo2PrefixCols",
		"IndexInfo2FullCols", "IndexInfo2Cols"}},
	"prop": {Min: 300, GoFuncs: []string{"NewPhysicalProperty", "SortItemsFromCols", "PhysicalProperty.HashCode",
		"PhysicalProperty.MemoryUsage", "PhysicalProperty.IsSortItemEmpty", "PhysicalProperty.NeedKeepOrder",
		"PhysicalProperty.GetSortDescForKeepOrder", "PhysicalProperty.GetSortItemsForKeepOrder",
		"PhysicalProperty.AllSameOrder", "PhysicalProperty.IsSortItemAllForPartition", "PhysicalProperty.IsPrefix",
		"PhysicalProperty.AllColsFromSchema", "PhysicalProperty.GetAllPossibleChildTaskTypes",
		"PhysicalProperty.IsFlashProp", "PhysicalProperty.CloneEssentialFields", "PhysicalProperty.IsSubsetOf",
		"MPPPartitionColumn.hashCode", "MPPPartitionColumn.Equal", "MPPPartitionColumn.MemoryUsage",
		"SortItem.MemoryUsage", "PartialOrderInfo.AllSameOrder", "TaskType.String"}},
	"stats": {Min: 200, GoFuncs: []string{"ToString", "StatsInfo.Count", "StatsInfo.Scale", "StatsInfo.ScaleByExpectCnt",
		"StatsInfo.String", "StatsInfo.GetGroupNDV4Cols", "DeriveLimitStats"}},
	"cost": {Min: 200, GoFuncs: []string{"CostVer2.GetCost", "CostVer2.GetTrace", "CostTrace.GetFormula",
		"CostTrace.GetFactorCosts", "NewZeroCostVer2", "HasCostFlag", "TraceCost", "NewCostVer2",
		"CostVer2Factor.String", "SumCostVer2", "DivCostVer2", "MulCostVer2"}},
	"handle_int": {Min: 100, GoFuncs: []string{"NewIntHandleCols", "IntHandleCols.BuildHandleByDatums",
		"IntHandleCols.Compare", "IntHandleCols.NumCols", "IntHandleCols.IsInt", "IntHandleCols.GetCol",
		"IntHandleCols.MemoryUsage", "IntHandleCols.StringWithCtx", "IntHandleCols.GetFieldsTypes",
		"IntHandleCols.ResolveIndices", "IntHandleCols.Clone", "IntHandleCols.BuildHandle",
		"IntHandleCols.BuildHandleFromIndexRow", "IntHandleCols.BuildPartitionHandleFromIndexRow",
		"GetCommonHandleDatum"}},
	"handle_common": {Min: 150, GoFuncs: []string{"NewCommonHandleCols", "NewCommonHandlesColsWithoutColsAlign",
		"CommonHandleCols.buildHandleByDatumsBuffer", "CommonHandleCols.BuildHandleByDatums",
		"CommonHandleCols.Compare", "CommonHandleCols.NumCols", "CommonHandleCols.IsInt", "CommonHandleCols.GetCol",
		"CommonHandleCols.GetColumns", "CommonHandleCols.MemoryUsage", "CommonHandleCols.StringWithCtx",
		"CommonHandleCols.GetFieldsTypes", "CommonHandleCols.ResolveIndices", "CommonHandleCols.Clone",
		"CommonHandleCols.BuildHandle", "CommonHandleCols.BuildHandleFromIndexRow",
		"CommonHandleCols.BuildPartitionHandleFromIndexRow"}},
	"split_cor": {Min: 200, GoFuncs: []string{"AccessPath.SplitCorColAccessCondFromFilters", "isColEqConstant",
		"isColEqCorCol", "isColEqExpr", "AccessPath.GetCol2LenFromAccessConds", "ExtractCol2Len",
		"extractCol2LenFromExpr"}},
	"byitems": {Min: 100, GoFuncs: []string{"ByItems.StringWithCtx", "ByItems.Clone", "ByItems.Equal",
		"ByItems.MemoryUsage", "StringifyByItemsWithCtx"}},
	"misc": {Min: 100, GoFuncs: []string{"EncodeIntAsUint32", "GetMaxSortPrefix", "QueryTimeRange.Condition",
		"QueryTimeRange.MemoryUsage", "MPPPartitionType.ToExchangeType", "GetCollateIDByNameForPartition",
		"GetCollateNameByIDForPartition", "ExplainColumnList", "PhysicalPropMatchResult.Matched", "SortItem.String",
		"SortItem.Clone", "MPPPartitionColumn.Clone", "MPPPartitionColumn.ResolveIndices"}},
}

func planutilExempt() map[string]string {
	ex := map[string]string{
		"SortItem.Hash64": "EXC.B: cascades base.HashEquals (D08.1 spec §4 item 2)",
		"SortItem.Equals": "EXC.B: cascades base.HashEquals (D08.1 spec §4 item 2)",
		"ByItems.Hash64":  "EXC.B: cascades base.HashEquals (D08.1 spec §4 item 2)",
		"ByItems.Equals":  "EXC.B: cascades base.HashEquals (D08.1 spec §4 item 2)",
		"PhysicalProperty.String": "test-only String: Go prints the sort items by reflection, i.e. the *Column " +
			"addresses; not reproducible, Rust unit test",
		"AccessPath.Clone":  "structural copy (the immutable expressions and columns are shared, M-EXPR); Rust unit test",
		"CloneAssignments":  "boundary stub in tidb-rs, blocked-by:D09.15 (expression.Assignment, the write path)",
		"ExtractTableAlias": "boundary stub in tidb-rs, blocked-by:D13.3 (hint.HintedTable)",
		"GetPushDownCtx": "builds a PushDownContext from the session's BuildPBContext (no observable output); " +
			"Rust unit test",
		"GetPushDownCtxFromBuildPBContext": "builds a PushDownContext from a BuildPBContext (no observable output); " +
			"Rust unit test",
		"pkg/planner/util/costusage/cost_misc.go:init": "copies CostFlagTrace into optimizetrace.CostFlagTrace; a " +
			"compile-time assertion in tidb-rs",
		"pkg/planner/util/coretestsdk/testkit.go:GetFieldValue": "test helper (testkit)",
	}
	for _, f := range []string{"CloneFieldNames", "CloneCIStrs", "CloneExprs", "CloneExpressions",
		"CloneExpression2D", "CloneScalarFunctions", "CloneHandleCols", "CloneCols", "CloneColumns",
		"CloneConstants", "CloneConstant2D", "CloneDatums", "CloneDatum2D", "CloneColInfos", "CloneRanges",
		"CloneByItemss", "CloneSortItems", "CloneHandles"} {
		ex[f] = "structural copy of a slice (the immutable expressions and columns are shared, M-EXPR); Rust unit test"
	}
	// The rest of D10.10 is outside the VS1-19 part.
	for _, f := range []string{"WrapCastForAggFuncs", "ExtractCorColumnsBySchema", "ExtractCorColumnsBySchema4LogicalPlan",
		"ExtractCorColumnsBySchema4PhysicalPlan", "ExtractCorrelatedCols4LogicalPlan",
		"ExtractCorrelatedCols4PhysicalPlan", "ExplainByItems", "ExplainPartitionBy", "ExtractConstantCols",
		"ExtractEquivalenceCols", "ExtractNotNullFromConds", "allConstants", "foldNullifiedFunction", "IsNullRejected",
		"nullRejectFoldCtx", "proofFromConstant", "proveNullRejected", "proveNullRejectedIn",
		"proveNullRejectedScalarFunc", "ResetNotNullFlag", "tryFoldNullifiedCoalesceLike", "tryFoldNullifiedConstant",
		"tryFoldNullifiedIf", "tryFoldNullifiedScalarFunc", "tryFoldStaticConstant"} {
		ex[f] = "D10.10 outside the VS1-19 part (tidb-rs specs/VS1-vertical-slice functions.tsv); not yet ported"
	}
	return ex
}

// ---------------------------------------------------------------- inputs

func puDatum(raw json.RawMessage) types.Datum {
	var d struct {
		K string          `json:"k"`
		V json.RawMessage `json:"v"`
	}
	mustUnmarshal(raw, &d)
	switch d.K {
	case "null":
		return types.Datum{}
	case "i":
		var v int64
		mustUnmarshal(d.V, &v)
		return types.NewIntDatum(v)
	case "u":
		var v uint64
		mustUnmarshal(d.V, &v)
		return types.NewUintDatum(v)
	case "s":
		var v string
		mustUnmarshal(d.V, &v)
		return types.NewCollationStringDatum(v, "utf8mb4_bin")
	case "min":
		return types.MinNotNullDatum()
	case "max":
		return types.MaxValueDatum()
	}
	panic("bad datum " + d.K)
}

func puDatums(raw json.RawMessage) []types.Datum {
	var ds []json.RawMessage
	mustUnmarshal(raw, &ds)
	out := make([]types.Datum, 0, len(ds))
	for _, d := range ds {
		out = append(out, puDatum(d))
	}
	return out
}

func puFieldType(tp string, flen int) *types.FieldType {
	switch tp {
	case "int":
		ft := types.NewFieldType(mysql.TypeLonglong)
		if flen > 0 {
			ft.SetFlen(flen)
		}
		return ft
	case "str":
		ft := types.NewFieldType(mysql.TypeVarchar)
		ft.SetCharset("utf8mb4")
		ft.SetCollate("utf8mb4_bin")
		ft.SetFlen(flen)
		return ft
	}
	panic("bad type " + tp)
}

// puCol: [uniqueID, tp, flen].
func puCol(raw json.RawMessage) *expression.Column {
	var a []json.RawMessage
	mustUnmarshal(raw, &a)
	var id int64
	var tp string
	var flen int
	mustUnmarshal(a[0], &id)
	mustUnmarshal(a[1], &tp)
	mustUnmarshal(a[2], &flen)
	return &expression.Column{UniqueID: id, ID: id, RetType: puFieldType(tp, flen)}
}

func puIntCol(id int64) *expression.Column {
	return &expression.Column{UniqueID: id, ID: id, RetType: types.NewFieldType(mysql.TypeLonglong)}
}

func puRanges(raw json.RawMessage) []*ranger.Range {
	var rs [][]json.RawMessage
	mustUnmarshal(raw, &rs)
	out := make([]*ranger.Range, 0, len(rs))
	for _, r := range rs {
		var le, he bool
		mustUnmarshal(r[2], &le)
		mustUnmarshal(r[3], &he)
		low, high := puDatums(r[0]), puDatums(r[1])
		out = append(out, &ranger.Range{LowVal: low, HighVal: high, LowExclude: le, HighExclude: he,
			Collators: collate.GetBinaryCollatorSlice(len(low))})
	}
	return out
}

func puSortItems(raw json.RawMessage) []property.SortItem {
	var a [][2]json.RawMessage
	mustUnmarshal(raw, &a)
	out := make([]property.SortItem, 0, len(a))
	for _, it := range a {
		var id int64
		var desc bool
		mustUnmarshal(it[0], &id)
		mustUnmarshal(it[1], &desc)
		out = append(out, property.SortItem{Col: puIntCol(id), Desc: desc})
	}
	return out
}

func puSortItemsOut(items []property.SortItem) any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, []any{it.Col.UniqueID, it.Desc})
	}
	return out
}

func puF(raw json.RawMessage) float64 {
	var s string
	mustUnmarshal(raw, &s)
	return parseGoFloat(s)
}

func parseGoFloat(s string) float64 {
	switch s {
	case "NaN":
		return math.NaN()
	case "+Inf":
		return math.Inf(1)
	case "-Inf":
		return math.Inf(-1)
	}
	var f float64
	if _, err := fmt.Sscan(s, &f); err != nil {
		panic(err)
	}
	return f
}

func fstr(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// ---------------------------------------------------------------- exec

func planutilExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "col2len":
		r, ok := util.CompareCol2Len(puCol2Len(args[0]), puCol2Len(args[1]))
		return []any{r, ok}, nil
	case "only_point":
		var isInt bool
		var n int
		mustUnmarshal(args[0], &isInt)
		mustUnmarshal(args[1], &n)
		path := &util.AccessPath{IsIntHandlePath: isInt, Ranges: puRanges(args[2])}
		if n >= 0 {
			path.Index = &model.IndexInfo{Columns: make([]*model.IndexColumn, n)}
		}
		return path.OnlyPointRange(types.DefaultStmtNoWarningContext), nil
	case "full_scan":
		var isInt, pkIsHandle, unsigned bool
		mustUnmarshal(args[0], &isInt)
		mustUnmarshal(args[1], &pkIsHandle)
		mustUnmarshal(args[2], &unsigned)
		ft := types.NewFieldType(mysql.TypeLonglong)
		ft.AddFlag(mysql.PriKeyFlag)
		if unsigned {
			ft.AddFlag(mysql.UnsignedFlag)
		}
		tbl := &model.TableInfo{PKIsHandle: pkIsHandle, Columns: []*model.ColumnInfo{{Name: pmodel.NewCIStr("a"), FieldType: *ft}}}
		path := &util.AccessPath{IsIntHandlePath: isInt, Ranges: puRanges(args[3])}
		return path.IsFullScanRange(tbl), nil
	case "path_flags":
		var isInt, isCommon, hasIndex, mv bool
		var st int
		var cond string
		mustUnmarshal(args[0], &isInt)
		mustUnmarshal(args[1], &isCommon)
		mustUnmarshal(args[2], &hasIndex)
		mustUnmarshal(args[3], &st)
		mustUnmarshal(args[4], &mv)
		mustUnmarshal(args[5], &cond)
		path := &util.AccessPath{IsIntHandlePath: isInt, IsCommonHandlePath: isCommon, StoreType: kv.StoreType(st)}
		if hasIndex {
			path.Index = &model.IndexInfo{MVIndex: mv, ConditionExprString: cond}
		}
		return []bool{path.IsTablePath(), path.IsTiKVTablePath(), path.IsTiFlashSimpleTablePath(),
			path.IsUndetermined(), path.IsIndexJoinUnapplicable()}, nil
	case "index_cols":
		return puIndexCols(args), nil
	case "prop":
		return puProp(args), nil
	case "stats":
		return puStats(args[0]), nil
	case "cost":
		return puCost(args), nil
	case "handle_int":
		return puHandleInt(args), nil
	case "handle_common":
		return puHandleCommon(args), nil
	case "split_cor":
		return puSplitCor(args), nil
	case "byitems":
		return puByItems(args), nil
	case "misc":
		return puMisc(args), nil
	}
	return nil, fmt.Errorf("unknown op %s", op)
}

func puCol2Len(raw json.RawMessage) util.Col2Len {
	var a [][2]int64
	mustUnmarshal(raw, &a)
	m := make(util.Col2Len, len(a))
	for _, p := range a {
		m[p[0]] = int(p[1])
	}
	return m
}

func puColOut(c *expression.Column) any {
	if c == nil {
		return nil
	}
	return []any{c.UniqueID, c.IsPrefix}
}

func puColsOut(cs []*expression.Column) any {
	out := make([]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, puColOut(c))
	}
	return out
}

func puInts(xs []int) []int {
	if xs == nil {
		return []int{}
	}
	return xs
}

func puIndexCols(args []json.RawMessage) any {
	var infos [][2]json.RawMessage
	mustUnmarshal(args[0], &infos)
	colInfos := make([]*model.ColumnInfo, 0, len(infos))
	for _, ci := range infos {
		var name string
		var flen int
		mustUnmarshal(ci[0], &name)
		mustUnmarshal(ci[1], &flen)
		colInfos = append(colInfos, &model.ColumnInfo{Name: pmodel.NewCIStr(name), FieldType: *puFieldType("str", flen)})
	}
	var rawCols []json.RawMessage
	mustUnmarshal(args[1], &rawCols)
	cols := make([]*expression.Column, 0, len(rawCols))
	for _, c := range rawCols {
		cols = append(cols, puCol(c))
	}
	var idx [][2]json.RawMessage
	mustUnmarshal(args[2], &idx)
	index := &model.IndexInfo{}
	for _, ic := range idx {
		var name string
		var length int
		mustUnmarshal(ic[0], &name)
		mustUnmarshal(ic[1], &length)
		index.Columns = append(index.Columns, &model.IndexColumn{Name: pmodel.NewCIStr(name), Length: length})
	}
	pc, pl, fc, fl := util.IndexInfo2Cols(colInfos, cols, index)
	pc2, pl2 := util.IndexInfo2PrefixCols(colInfos, cols, index)
	fc2, fl2 := util.IndexInfo2FullCols(colInfos, cols, index)
	return map[string]any{"pc": puColsOut(pc), "pl": puInts(pl), "fc": puColsOut(fc), "fl": puInts(fl),
		"pc2": puColsOut(pc2), "pl2": puInts(pl2), "fc2": puColsOut(fc2), "fl2": puInts(fl2)}
}

// propSpec: the fields of a PhysicalProperty.
type propSpec struct {
	SI  json.RawMessage `json:"si"`
	TT  int             `json:"tt"`
	EC  string          `json:"ec"`
	EN  bool            `json:"en"`
	SP  json.RawMessage `json:"sp"`
	RS  bool            `json:"rs"`
	CTE int             `json:"cte"`
	PO  json.RawMessage `json:"po"`
	AD  json.RawMessage `json:"ad"`
	MT  int             `json:"mt"`
	MC  [][2]int64      `json:"mc"`
}

func puPropIn(raw json.RawMessage) *property.PhysicalProperty {
	var s propSpec
	mustUnmarshal(raw, &s)
	items := puSortItems(s.SI)
	cols := make([]*expression.Column, 0, len(items))
	desc := false
	for i, it := range items {
		cols = append(cols, it.Col)
		if i == 0 {
			desc = it.Desc
		}
	}
	p := property.NewPhysicalProperty(property.TaskType(s.TT), cols, desc, parseGoFloat(s.EC), s.EN)
	for i := range p.SortItems {
		p.SortItems[i].Desc = items[i].Desc
	}
	p.SortItemsForPartition = puSortItems(s.SP)
	p.RejectSort = s.RS
	p.CTEProducerStatus = property.NoCTEOrAllProducerCanMPP
	switch s.CTE {
	case 1:
		p.CTEProducerStatus = property.SomeCTEFailedMpp
	case 2:
		p.CTEProducerStatus = property.AllCTECanMpp
	}
	if string(s.PO) != "null" && len(s.PO) > 0 {
		po := &property.PartialOrderInfo{}
		for _, it := range puSortItems(s.PO) {
			it := it
			po.SortItems = append(po.SortItems, &it)
		}
		p.PartialOrderInfo = po
	}
	p.AdvisorySortItems = puSortItems(s.AD)
	p.MPPPartitionTp = property.MPPPartitionType(s.MT)
	for _, mc := range s.MC {
		p.MPPPartitionCols = append(p.MPPPartitionCols, &property.MPPPartitionColumn{Col: puIntCol(mc[0]), CollateID: int32(mc[1])})
	}
	return p
}

func puProp(args []json.RawMessage) any {
	p := puPropIn(args[0])
	other := puPropIn(args[1])
	var schemaIDs []int64
	mustUnmarshal(args[2], &schemaIDs)
	schemaCols := make([]*expression.Column, 0, len(schemaIDs))
	for _, id := range schemaIDs {
		schemaCols = append(schemaCols, puIntCol(id))
	}
	var keys [][2]int64
	mustUnmarshal(args[3], &keys)
	keyCols := make([]*property.MPPPartitionColumn, 0, len(keys))
	for _, k := range keys {
		keyCols = append(keyCols, &property.MPPPartitionColumn{Col: puIntCol(k[0]), CollateID: int32(k[1])})
	}
	mem0 := p.MemoryUsage()
	same, desc := p.AllSameOrder()
	childTypes := make([]string, 0)
	for _, t := range p.GetAllPossibleChildTaskTypes() {
		childTypes = append(childTypes, t.String())
	}
	var mpp []any
	for i, c := range p.MPPPartitionCols {
		var eq []bool
		for _, k := range keyCols {
			eq = append(eq, c.Equal(k))
		}
		_ = i
		mpp = append(mpp, map[string]any{"hash": hex.EncodeToString(puMPPHashCode(c)), "mem": c.MemoryUsage(), "eq": eq})
	}
	var subset any
	if s := p.IsSubsetOf(keyCols); s != nil {
		subset = s
	}
	var poSame any
	if p.PartialOrderInfo != nil {
		a, b := p.PartialOrderInfo.AllSameOrder()
		poSame = []bool{a, b}
	}
	var siMem []int64
	for _, it := range p.SortItems {
		siMem = append(siMem, it.MemoryUsage())
	}
	hash := hex.EncodeToString(p.HashCode())
	clone := p.CloneEssentialFields()
	return map[string]any{
		"mem0": mem0, "hash": hash, "mem": p.MemoryUsage(), "empty": p.IsSortItemEmpty(), "keep": p.NeedKeepOrder(),
		"desc": p.GetSortDescForKeepOrder(), "keep_items": puSortItemsOut(p.GetSortItemsForKeepOrder()),
		"same": []bool{same, desc}, "allpart": p.IsSortItemAllForPartition(), "prefix": p.IsPrefix(other),
		"fromschema": p.AllColsFromSchema(expression.NewSchema(schemaCols...)), "child": childTypes,
		"flash": p.IsFlashProp(), "clone_hash": hex.EncodeToString(clone.HashCode()), "clone_en": clone.CanAddEnforcer,
		"tt": p.TaskTp.String(), "subset": subset, "mpp": mpp, "po_same": poSame, "si_mem": siMem,
	}
}

// ---------------------------------------------------------------- stats

type statsSpec struct {
	RC     string      `json:"rc"`
	NDV    [][2]string `json:"ndv"`
	SV     uint64      `json:"sv"`
	Groups []groupSpec `json:"groups"`
	Factor string      `json:"factor"`
	Expect string      `json:"expect"`
	Cols   []int64     `json:"cols"`
	Limit  string      `json:"limit"`
}

type groupSpec struct {
	Cols []int64 `json:"cols"`
	NDV  string  `json:"ndv"`
}

func puStatsOut(s *property.StatsInfo) any {
	keys := make([]int64, 0, len(s.ColNDVs))
	for k := range s.ColNDVs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	ndvs := make([]any, 0, len(keys))
	for _, k := range keys {
		ndvs = append(ndvs, []any{k, fstr(s.ColNDVs[k])})
	}
	groups := make([]any, 0, len(s.GroupNDVs))
	for _, g := range s.GroupNDVs {
		groups = append(groups, []any{g.Cols, fstr(g.NDV)})
	}
	return map[string]any{"rc": fstr(s.RowCount), "ndv": ndvs, "sv": s.StatsVersion, "groups": groups}
}

func puStats(raw json.RawMessage) any {
	var sp statsSpec
	mustUnmarshal(raw, &sp)
	s := &property.StatsInfo{RowCount: parseGoFloat(sp.RC), ColNDVs: map[int64]float64{}, StatsVersion: sp.SV}
	for _, p := range sp.NDV {
		id, _ := strconv.ParseInt(p[0], 10, 64)
		s.ColNDVs[id] = parseGoFloat(p[1])
	}
	for _, g := range sp.Groups {
		s.GroupNDVs = append(s.GroupNDVs, property.GroupNDV{Cols: g.Cols, NDV: parseGoFloat(g.NDV)})
	}
	cols := make([]*expression.Column, 0, len(sp.Cols))
	for _, id := range sp.Cols {
		cols = append(cols, puIntCol(id))
	}
	var group any
	if g := s.GetGroupNDV4Cols(cols); g != nil {
		group = []any{g.Cols, fstr(g.NDV)}
	}
	sbe := s.ScaleByExpectCnt(parseGoFloat(sp.Expect))
	return map[string]any{
		"count": s.Count(), "string": vec.GoString(s.String()), "scale": puStatsOut(s.Scale(parseGoFloat(sp.Factor))),
		"sbe": puStatsOut(sbe), "sbe_same": sbe == s, "group": group,
		"limit":    puStatsOut(util.DeriveLimitStats(s, parseGoFloat(sp.Limit))),
		"tostring": vec.GoString(property.ToString(s.GroupNDVs)),
	}
}

// ---------------------------------------------------------------- cost

func puCostOut(c costusage.CostVer2) any {
	out := map[string]any{"cost": fstr(c.GetCost())}
	if t := c.GetTrace(); t != nil {
		names := make([]string, 0, len(t.GetFactorCosts()))
		for n := range t.GetFactorCosts() {
			names = append(names, n)
		}
		sort.Strings(names)
		fs := make([]any, 0, len(names))
		for _, n := range names {
			fs = append(fs, []any{n, fstr(t.GetFactorCosts()[n])})
		}
		out["formula"] = t.GetFormula()
		out["factors"] = fs
	}
	return out
}

func puCost(args []json.RawMessage) any {
	var trace bool
	mustUnmarshal(args[0], &trace)
	var factors [][3]string
	mustUnmarshal(args[1], &factors)
	div, mul := puF(args[2]), puF(args[3])
	var flags [2]uint64
	mustUnmarshal(args[4], &flags)
	option := optimizetrace.NewDefaultPlanCostOption()
	if trace {
		option.WithCostFlag(costusage.CostFlagTrace)
	}
	costs := make([]costusage.CostVer2, 0, len(factors)+1)
	var each, fstrs []any
	for i, f := range factors {
		factor := costusage.CostVer2Factor{Name: f[0], Value: parseGoFloat(f[1])}
		name := f[0]
		c := costusage.NewCostVer2(option, factor, parseGoFloat(f[2]), func() string {
			return fmt.Sprintf("%s*%d", name, i)
		})
		costs = append(costs, c)
		each = append(each, puCostOut(c))
		fstrs = append(fstrs, factor.String())
	}
	costs = append(costs, costusage.NewZeroCostVer2(trace))
	sum := costusage.SumCostVer2(costs...)
	return map[string]any{
		"each": each, "factor_str": fstrs, "sum": puCostOut(sum), "div": puCostOut(costusage.DivCostVer2(sum, div)),
		"mul": puCostOut(costusage.MulCostVer2(sum, mul)), "sum0": puCostOut(costusage.SumCostVer2()),
		"zero": puCostOut(costusage.NewZeroCostVer2(trace)), "has": costusage.HasCostFlag(flags[0], flags[1]),
		"trace": costusage.TraceCost(option), "trace_nil": costusage.TraceCost(nil),
	}
}

// ---------------------------------------------------------------- handles

func puHandleOut(h kv.Handle, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	switch x := h.(type) {
	case kv.IntHandle:
		return map[string]any{"i": x.IntValue()}
	case kv.PartitionHandle:
		return map[string]any{"pid": x.PartitionID, "h": puHandleOut(x.Handle, nil)}
	default:
		return map[string]any{"c": hex.EncodeToString(h.Encoded())}
	}
}

func puCmp(c int, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	return c
}

func puFTs(fts []*types.FieldType) []int {
	out := make([]int, 0, len(fts))
	for _, ft := range fts {
		out = append(out, int(ft.GetType()))
	}
	return out
}

// puRow: a chunk row of the datums (field types from the datums' kinds).
func puRow(ds []types.Datum) chunk.Row {
	return chunk.MutRowFromDatums(ds).ToRow()
}

func puSchema(raw json.RawMessage) *expression.Schema {
	var ids []int64
	mustUnmarshal(raw, &ids)
	cols := make([]*expression.Column, 0, len(ids))
	for _, id := range ids {
		cols = append(cols, puIntCol(id))
	}
	return expression.NewSchema(cols...)
}

func puResolved(hc util.HandleCols, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	idx := make([]int, 0, hc.NumCols())
	for i := 0; i < hc.NumCols(); i++ {
		idx = append(idx, hc.GetCol(i).Index)
	}
	return idx
}

// args [col [uniqueID, index], a, b, schema].
func puHandleInt(args []json.RawMessage) any {
	var c [2]int64
	mustUnmarshal(args[0], &c)
	col := puIntCol(c[0])
	col.Index = int(c[1])
	a, b := puDatums(args[1]), puDatums(args[2])
	hc := util.NewIntHandleCols(col)
	sc := stmtctx.NewStmtCtx()
	h, err := hc.BuildHandleByDatums(sc, a)
	ctors := collate.GetBinaryCollatorSlice(len(a))
	cmp := rTry(func() any { return puCmp(hc.Compare(a, b, ctors, types.DefaultStmtNoWarningContext)) })
	var getCol1 any
	if g := hc.GetCol(1); g != nil {
		getCol1 = g.UniqueID
	}
	row := puRow(a)
	rh, rerr := hc.BuildHandle(sc, row)
	ih, ierr := hc.BuildHandleFromIndexRow(sc, row)
	var ph any
	if len(a) >= 2 {
		p, perr := hc.BuildPartitionHandleFromIndexRow(sc, row)
		ph = puHandleOut(p, perr)
	}
	var common any
	if d := util.GetCommonHandleDatum(hc, row); d != nil {
		common = len(d)
	}
	return map[string]any{
		"h": puHandleOut(h, err), "cmp": cmp, "num": hc.NumCols(), "isint": hc.IsInt(),
		"col0": hc.GetCol(0).UniqueID, "col1": getCol1, "mem": hc.MemoryUsage(),
		"str": vec.GoString(hc.StringWithCtx(exprstatic.NewEvalContext(), perrors.RedactLogDisable)),
		"ft":  puFTs(hc.GetFieldsTypes()), "resolved": puResolved(hc.ResolveIndices(puSchema(args[3]))),
		"clone_mem": hc.Clone().MemoryUsage(), "row_h": puHandleOut(rh, rerr), "idx_h": puHandleOut(ih, ierr),
		"part_h": ph, "common": common,
	}
}

// args [tblCols [[tp, flen]], idxCols [[offset, length]], a, b, schema]. The table columns
// are named c<i>, their expression columns have UniqueID 100+i and Index i.
func puHandleCommon(args []json.RawMessage) any {
	var tcols [][2]json.RawMessage
	mustUnmarshal(args[0], &tcols)
	tbl := &model.TableInfo{IsCommonHandle: true}
	tableColumns := make([]*expression.Column, 0, len(tcols))
	for i, tc := range tcols {
		var tp string
		var flen int
		mustUnmarshal(tc[0], &tp)
		mustUnmarshal(tc[1], &flen)
		ft := puFieldType(tp, flen)
		tbl.Columns = append(tbl.Columns, &model.ColumnInfo{ID: int64(i + 1), Offset: i,
			Name: pmodel.NewCIStr(fmt.Sprintf("c%d", i)), FieldType: *ft})
		tableColumns = append(tableColumns, &expression.Column{UniqueID: int64(100 + i), ID: int64(i + 1),
			Index: i, RetType: ft})
	}
	var icols [][2]int
	mustUnmarshal(args[1], &icols)
	idx := &model.IndexInfo{Primary: true, Unique: true}
	for _, ic := range icols {
		idx.Columns = append(idx.Columns, &model.IndexColumn{Name: pmodel.NewCIStr(fmt.Sprintf("c%d", ic[0])),
			Offset: ic[0], Length: ic[1]})
	}
	a, b := puDatums(args[2]), puDatums(args[3])
	hc := util.NewCommonHandleCols(tbl, idx, tableColumns)
	sc := stmtctx.NewStmtCtx()
	h, err := hc.BuildHandleByDatums(sc, a)
	ctors := make([]collate.Collator, 0, hc.NumCols())
	for _, c := range hc.GetColumns() {
		ctors = append(ctors, collate.GetCollator(c.RetType.GetCollate()))
	}
	cmp := rTry(func() any { return puCmp(hc.Compare(a, b, ctors, types.DefaultStmtNoWarningContext)) })
	cols := make([]int64, 0, hc.NumCols())
	for i := 0; i < hc.NumCols(); i++ {
		cols = append(cols, hc.GetCol(i).UniqueID)
	}
	row := puRow(a)
	rh, rerr := hc.BuildHandle(sc, row)
	// The index row: the handle columns' datums last; the partition row adds a pid.
	var idxRow []types.Datum
	idxRow = append(idxRow, types.NewIntDatum(7))
	for _, c := range hc.GetColumns() {
		idxRow = append(idxRow, a[c.Index])
	}
	ih, ierr := hc.BuildHandleFromIndexRow(sc, puRow(idxRow))
	pRow := append(append([]types.Datum{}, idxRow...), types.NewIntDatum(42))
	ph, perr := hc.BuildPartitionHandleFromIndexRow(sc, puRow(pRow))
	common := util.GetCommonHandleDatum(hc, row)
	commonOut := make([]any, 0, len(common))
	for _, d := range common {
		commonOut = append(commonOut, vec.GoString(fmt.Sprintf("%v", d.GetValue())))
	}
	wo := util.NewCommonHandlesColsWithoutColsAlign(tbl, idx, hc.GetColumns())
	return map[string]any{
		"h": puHandleOut(h, err), "cmp": cmp, "num": hc.NumCols(), "isint": hc.IsInt(), "cols": cols,
		"mem": hc.MemoryUsage(),
		"str": vec.GoString(hc.StringWithCtx(exprstatic.NewEvalContext(), perrors.RedactLogDisable)),
		"ft":  puFTs(hc.GetFieldsTypes()), "resolved": puResolved(hc.ResolveIndices(puSchema(args[4]))),
		"clone_mem": hc.Clone().MemoryUsage(), "row_h": puHandleOut(rh, rerr), "idx_h": puHandleOut(ih, ierr),
		"part_h": puHandleOut(ph, perr), "common": commonOut, "wo_num": wo.NumCols(),
	}
}

// ---------------------------------------------------------------- split_cor

// args [idxCols uniqueIDs, idxColLens, filters, eqOrInCount]; a filter is [kind, uniqueID,
// swap]: kind "const" (col = 1), "cor" (col = correlated column #999), "lt" (col < 1),
// "col" (col = column #998); swap puts the column on the right.
func puSplitCor(args []json.RawMessage) any {
	var ids []int64
	var lens []int
	var filters [][3]json.RawMessage
	var eqOrIn int
	mustUnmarshal(args[0], &ids)
	mustUnmarshal(args[1], &lens)
	mustUnmarshal(args[2], &filters)
	mustUnmarshal(args[3], &eqOrIn)
	sctx := mock.NewContext()
	sctx.GetSessionVars().StmtCtx.EnablePlanCache()
	ctx := sctx.GetExprCtx()
	idxCols := make([]*expression.Column, 0, len(ids))
	for _, id := range ids {
		idxCols = append(idxCols, puIntCol(id))
	}
	fs := make([]expression.Expression, 0, len(filters))
	for _, f := range filters {
		var kind string
		var id int64
		var swap bool
		mustUnmarshal(f[0], &kind)
		mustUnmarshal(f[1], &id)
		mustUnmarshal(f[2], &swap)
		fs = append(fs, puFilter(ctx, kind, id, swap))
	}
	path := &util.AccessPath{IdxCols: idxCols, IdxColLens: lens, TableFilters: fs, AccessConds: fs}
	access, remained := path.SplitCorColAccessCondFromFilters(sctx, eqOrIn)
	pos := func(es []expression.Expression) []int {
		out := make([]int, 0, len(es))
		for _, e := range es {
			for i, f := range fs {
				if e == f {
					out = append(out, i)
					break
				}
			}
		}
		return out
	}
	col2len := func(m util.Col2Len) any {
		keys := make([]int64, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, []any{k, m[k]})
		}
		return out
	}
	tablePath := &util.AccessPath{IsIntHandlePath: true, AccessConds: fs}
	return map[string]any{
		"access": pos(access), "remained": pos(remained), "skip": !sctx.GetSessionVars().StmtCtx.UseCache(),
		"c2l": col2len(path.GetCol2LenFromAccessConds(sctx)), "c2l_table": col2len(tablePath.GetCol2LenFromAccessConds(sctx)),
	}
}

func puFilter(ctx exprctx.BuildContext, kind string, id int64, swap bool) expression.Expression {
	col := puIntCol(id)
	var other expression.Expression
	fn := ast.EQ
	switch kind {
	case "const":
		other = &expression.Constant{Value: types.NewIntDatum(1), RetType: types.NewFieldType(mysql.TypeLonglong)}
	case "lt":
		fn = ast.LT
		other = &expression.Constant{Value: types.NewIntDatum(1), RetType: types.NewFieldType(mysql.TypeLonglong)}
	case "cor":
		other = &expression.CorrelatedColumn{Column: *puIntCol(999), Data: new(types.Datum)}
	case "col":
		other = puIntCol(998)
	}
	a, b := expression.Expression(col), other
	if swap {
		a, b = b, a
	}
	return expression.NewFunctionInternal(ctx, fn, types.NewFieldType(mysql.TypeLonglong), a, b)
}

// ---------------------------------------------------------------- byitems

func puByItemsIn(raw json.RawMessage) []*util.ByItems {
	var a [][2]json.RawMessage
	mustUnmarshal(raw, &a)
	out := make([]*util.ByItems, 0, len(a))
	for _, it := range a {
		var id int64
		var desc bool
		mustUnmarshal(it[0], &id)
		mustUnmarshal(it[1], &desc)
		out = append(out, &util.ByItems{Expr: puIntCol(id), Desc: desc})
	}
	return out
}

func puByItems(args []json.RawMessage) any {
	items, other := puByItemsIn(args[0]), puByItemsIn(args[1])
	ctx := exprstatic.NewEvalContext()
	var strs, eqs []any
	var mem []int64
	for i, it := range items {
		strs = append(strs, vec.GoString(it.StringWithCtx(ctx, perrors.RedactLogDisable)))
		mem = append(mem, it.MemoryUsage())
		if i < len(other) {
			eqs = append(eqs, it.Equal(ctx, other[i]))
		}
		if !it.Clone().Equal(ctx, it) {
			panic("clone differs")
		}
	}
	return map[string]any{"str": strs, "mem": mem, "eq": eqs,
		"all": vec.GoString(util.StringifyByItemsWithCtx(ctx, items))}
}

// ---------------------------------------------------------------- misc

func puMisc(args []json.RawMessage) any {
	var what string
	mustUnmarshal(args[0], &what)
	switch what {
	case "enc32":
		var v int
		mustUnmarshal(args[1], &v)
		return hex.EncodeToString(util.EncodeIntAsUint32([]byte{0xab}, v))
	case "maxsort":
		var sortIDs, allIDs []int64
		mustUnmarshal(args[1], &sortIDs)
		mustUnmarshal(args[2], &allIDs)
		mk := func(ids []int64) []*expression.Column {
			out := make([]*expression.Column, 0, len(ids))
			for _, id := range ids {
				out = append(out, puIntCol(id))
			}
			return out
		}
		return puInts(util.GetMaxSortPrefix(mk(sortIDs), mk(allIDs)))
	case "qtr":
		var t [4]int64
		mustUnmarshal(args[1], &t)
		tr := &util.QueryTimeRange{From: time.Unix(t[0], t[1]).UTC(), To: time.Unix(t[2], t[3]).UTC()}
		return []any{tr.Condition(), tr.MemoryUsage()}
	case "exch":
		var v int
		mustUnmarshal(args[1], &v)
		return int(property.MPPPartitionType(v).ToExchangeType())
	case "collid":
		var name string
		var id int32
		mustUnmarshal(args[1], &name)
		mustUnmarshal(args[2], &id)
		return []any{property.GetCollateIDByNameForPartition(name), property.GetCollateNameByIDForPartition(id)}
	case "explaincols":
		var cols [][2]int64
		mustUnmarshal(args[1], &cols)
		mc := make([]*property.MPPPartitionColumn, 0, len(cols))
		for _, c := range cols {
			mc = append(mc, &property.MPPPartitionColumn{Col: puIntCol(c[0]), CollateID: int32(c[1])})
		}
		var resolved []any
		schema := puSchema(args[2])
		for _, c := range mc {
			r, err := c.ResolveIndices(schema)
			if err != nil {
				resolved = append(resolved, vec.GoString(err.Error()))
			} else {
				resolved = append(resolved, []any{r.Col.Index, r.CollateID})
			}
			cl := c.Clone()
			if !cl.Equal(c) {
				panic("clone differs")
			}
		}
		return []any{vec.GoString(string(property.ExplainColumnList(exprstatic.NewEvalContext(), mc))), resolved}
	case "match":
		var v int
		mustUnmarshal(args[1], &v)
		return property.PhysicalPropMatchResult(v).Matched()
	case "sortitem":
		items := puSortItems(args[1])
		out := make([]any, 0, len(items))
		for _, it := range items {
			c := it.Clone()
			out = append(out, []any{vec.GoString(c.String()), it.MemoryUsage()})
		}
		return out
	case "tasktype":
		var v int
		mustUnmarshal(args[1], &v)
		return property.TaskType(v).String()
	}
	panic("bad misc " + what)
}

// ---------------------------------------------------------------- generate

func puDI(v int64) any  { return map[string]any{"k": "i", "v": v} }
func puDU(v uint64) any { return map[string]any{"k": "u", "v": v} }
func puDS(v string) any { return map[string]any{"k": "s", "v": v} }
func puDNull() any      { return map[string]any{"k": "null"} }
func puDMin() any       { return map[string]any{"k": "min"} }
func puDMax() any       { return map[string]any{"k": "max"} }

func puRange(low, high []any, le, he bool) any { return []any{low, high, le, he} }

func planutilGenerate(g *vec.Gen, rng *rand.Rand) {
	// from: pkg/planner/util/path_test.go TestCompareCol2Len
	for _, tc := range [][2][][2]int64{
		{{{1, -1}, {2, -1}, {3, -1}}, {{1, -1}, {2, 10}}},
		{{{1, 5}}, {{1, 10}, {2, -1}}},
		{{{1, -1}, {2, -1}}, {{1, -1}, {2, 5}, {3, -1}}},
		{{{1, -1}, {2, 10}}, {{1, -1}, {2, 5}, {3, -1}}},
		{{{1, -1}, {2, 10}}, {{1, -1}, {2, 10}}},
		{{{1, -1}, {2, -1}}, {{1, -1}, {2, 10}}},
	} {
		g.Add("TestCompareCol2Len", "col2len", tc[0], tc[1])
	}
	randC2L := func() [][2]int64 {
		n := rng.IntN(4)
		used := map[int64]bool{}
		var out [][2]int64
		for len(out) < n {
			id := int64(1 + rng.IntN(4))
			if used[id] {
				continue
			}
			used[id] = true
			lens := []int64{-1, 3, 5, 10}
			out = append(out, [2]int64{id, lens[rng.IntN(len(lens))]})
		}
		sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
		if out == nil {
			out = [][2]int64{}
		}
		return out
	}
	for i := 0; i < 300; i++ {
		g.Add("rand/col2len", "col2len", randC2L(), randC2L())
	}

	// from: pkg/planner/util/path_test.go TestOnlyPointRange
	nullPoint := puRange([]any{puDNull()}, []any{puDNull()}, false, false)
	onePoint := puRange([]any{puDI(1)}, []any{puDI(1)}, false, false)
	one2Two := puRange([]any{puDI(1)}, []any{puDI(2)}, false, false)
	g.Add("TestOnlyPointRange", "only_point", true, -1, []any{nullPoint, onePoint})
	g.Add("TestOnlyPointRange", "only_point", true, -1, []any{onePoint, one2Two})
	g.Add("TestOnlyPointRange", "only_point", false, 1, []any{onePoint})
	g.Add("TestOnlyPointRange", "only_point", false, 1, []any{nullPoint, onePoint})
	g.Add("TestOnlyPointRange", "only_point", false, 1, []any{onePoint, one2Two})
	g.Add("TestOnlyPointRange", "only_point", false, 2, []any{onePoint})
	randD := func() any {
		switch rng.IntN(6) {
		case 0:
			return puDNull()
		case 1:
			return puDS([]string{"a", "b", "ab"}[rng.IntN(3)])
		default:
			return puDI(int64(rng.IntN(4)))
		}
	}
	randRange := func(n int) any {
		low, high := make([]any, 0, n), make([]any, 0, n)
		for i := 0; i < n; i++ {
			d := randD()
			low = append(low, d)
			if rng.IntN(3) == 0 {
				high = append(high, randD())
			} else {
				high = append(high, d)
			}
		}
		return puRange(low, high, rng.IntN(5) == 0, rng.IntN(5) == 0)
	}
	for i := 0; i < 300; i++ {
		n := 1 + rng.IntN(3)
		rs := make([]any, 0)
		for k := rng.IntN(3); k >= 0; k-- {
			rs = append(rs, randRange(n))
		}
		isInt := rng.IntN(2) == 0
		idx := -1
		if !isInt {
			idx = 1 + rng.IntN(3)
		}
		g.Add("rand/only_point", "only_point", isInt, idx, rs)
	}

	// full_scan: full ranges of each kind and partial ones.
	fulls := []any{
		puRange([]any{puDNull()}, []any{puDMax()}, false, false),
		puRange([]any{puDI(math.MinInt64)}, []any{puDI(math.MaxInt64)}, false, false),
		puRange([]any{puDU(0)}, []any{puDU(math.MaxUint64)}, false, false),
		puRange([]any{puDMin()}, []any{puDMax()}, false, false),
		puRange([]any{puDI(0)}, []any{puDI(math.MaxInt64)}, false, false),
		puRange([]any{puDI(1)}, []any{puDI(5)}, false, true),
		puRange([]any{puDNull()}, []any{puDNull()}, false, false),
	}
	for _, isInt := range []bool{false, true} {
		for _, pk := range []bool{false, true} {
			for _, unsigned := range []bool{false, true} {
				for _, r := range fulls {
					g.Add("sys/full_scan", "full_scan", isInt, pk, unsigned, []any{r})
				}
				g.Add("sys/full_scan", "full_scan", isInt, pk, unsigned, []any{fulls[5], fulls[1]})
				g.Add("sys/full_scan", "full_scan", isInt, pk, unsigned, []any{})
			}
		}
	}

	for m := 0; m < 64; m++ {
		cond := ""
		if m&32 != 0 {
			cond = "a > 1"
		}
		g.Add("sys/path_flags", "path_flags", m&1 != 0, m&2 != 0, m&4 != 0, (m>>3)&1, m&16 != 0, cond)
	}

	// from: pkg/planner/util/column_test.go TestIndexInfo2Cols
	idx012 := [][2]any{{"0", -1}, {"1", -1}, {"2", -1}}
	info := func(names ...string) [][2]any {
		out := make([][2]any, 0, len(names))
		for _, n := range names {
			out = append(out, [2]any{n, 20})
		}
		return out
	}
	col := func(ids ...int64) []any {
		out := make([]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, []any{id, "int", 0})
		}
		return out
	}
	g.Add("TestIndexInfo2Cols", "index_cols", info("0"), col(0), idx012)
	g.Add("TestIndexInfo2Cols", "index_cols", info("1"), col(1), idx012)
	g.Add("TestIndexInfo2Cols", "index_cols", info("0", "1"), col(0, 1), idx012)
	g.Add("TestIndexInfo2Cols", "index_cols", info("0", "2"), col(0, 2), idx012)
	for i := 0; i < 300; i++ {
		names := []string{"a", "b", "c", "d"}
		var infos [][2]any
		var cols []any
		for k, n := range names {
			if rng.IntN(3) == 0 {
				continue
			}
			flen := []int{5, 10, 20}[rng.IntN(3)]
			infos = append(infos, [2]any{n, flen})
			cflen := flen
			if rng.IntN(4) == 0 {
				cflen = []int{5, 10, 20}[rng.IntN(3)]
			}
			cols = append(cols, []any{int64(k + 1), "str", cflen})
		}
		var idx [][2]any
		for k := 1 + rng.IntN(3); k > 0; k-- {
			idx = append(idx, [2]any{names[rng.IntN(4)], []int{-1, 0, 5, 10, 20}[rng.IntN(5)]})
		}
		if infos == nil {
			infos = [][2]any{}
			cols = []any{}
		}
		g.Add("rand/index_cols", "index_cols", infos, cols, idx)
	}

	planutilGenerate2(g, rng)
}

func planutilGenerate2(g *vec.Gen, rng *rand.Rand) {
	randItems := func(max int) [][2]any {
		var out [][2]any
		for k := rng.IntN(max + 1); k > 0; k-- {
			out = append(out, [2]any{int64(1 + rng.IntN(4)), rng.IntN(3) == 0})
		}
		if out == nil {
			out = [][2]any{}
		}
		return out
	}
	floats := []string{"0", "1", "10.5", "1.7976931348623157e+308", "3", "100000", "0.001", "-1"}
	randProp := func() map[string]any {
		var po any
		if rng.IntN(3) == 0 {
			po = randItems(2)
		}
		var mc [][2]int64
		tt := rng.IntN(4)
		if tt == 3 {
			for k := rng.IntN(3); k > 0; k-- {
				mc = append(mc, [2]int64{int64(1 + rng.IntN(4)), []int64{-46, 46, 63}[rng.IntN(3)]})
			}
		}
		if mc == nil {
			mc = [][2]int64{}
		}
		si := randItems(3)
		sp := si
		if rng.IntN(2) == 0 {
			sp = randItems(2)
		}
		return map[string]any{"si": si, "tt": tt, "ec": floats[rng.IntN(len(floats))], "en": rng.IntN(2) == 0,
			"sp": sp, "rs": rng.IntN(2) == 0, "cte": rng.IntN(3), "po": po, "ad": randItems(2),
			"mt": rng.IntN(4), "mc": mc}
	}
	for i := 0; i < 400; i++ {
		p := randProp()
		other := randProp()
		if rng.IntN(2) == 0 {
			other["si"] = p["si"]
		}
		schema := []int64{}
		for id := int64(1); id <= 4; id++ {
			if rng.IntN(4) != 0 {
				schema = append(schema, id)
			}
		}
		keys := [][2]int64{}
		for k := rng.IntN(4); k > 0; k-- {
			keys = append(keys, [2]int64{int64(1 + rng.IntN(4)), []int64{-46, 46, 63}[rng.IntN(3)]})
		}
		g.Add("rand/prop", "prop", p, other, schema, keys)
	}

	ndvFloats := []string{"0", "1", "2.5", "100", "1e+20", "0.5"}
	for i := 0; i < 250; i++ {
		var ndv [][2]string
		for k := rng.IntN(4); k > 0; k-- {
			ndv = append(ndv, [2]string{strconv.Itoa(1 + rng.IntN(5)), ndvFloats[rng.IntN(len(ndvFloats))]})
		}
		if ndv == nil {
			ndv = [][2]string{}
		}
		groups := []any{}
		for k := rng.IntN(3); k > 0; k-- {
			var cols []int64
			for c := int64(1); c <= 4; c++ {
				if rng.IntN(2) == 0 {
					cols = append(cols, c)
				}
			}
			if cols == nil {
				cols = []int64{}
			}
			groups = append(groups, map[string]any{"cols": cols, "ndv": ndvFloats[rng.IntN(len(ndvFloats))]})
		}
		cols := []int64{}
		for k := rng.IntN(4); k > 0; k-- {
			cols = append(cols, int64(1+rng.IntN(4)))
		}
		rc := []string{"0", "0.5", "1", "1.5", "10", "1000", "123.456"}[rng.IntN(7)]
		g.Add("rand/stats", "stats", map[string]any{"rc": rc, "ndv": ndv, "sv": rng.IntN(3), "groups": groups,
			"factor": []string{"0", "0.5", "1", "2"}[rng.IntN(4)], "expect": []string{"0", "1", "5", "2000"}[rng.IntN(4)],
			"cols": cols, "limit": []string{"0", "1", "10", "5000"}[rng.IntN(4)]})
	}

	names := []string{"cpu", "mem", "net", "scan"}
	for i := 0; i < 250; i++ {
		var fs [][3]string
		for k := rng.IntN(4); k > 0; k-- {
			fs = append(fs, [3]string{names[rng.IntN(4)], []string{"1", "0.5", "30", "1.5e-05"}[rng.IntN(4)],
				[]string{"0", "1", "-2", "123.25", "1e+10"}[rng.IntN(5)]})
		}
		if fs == nil {
			fs = [][3]string{}
		}
		g.Add("rand/cost", "cost", rng.IntN(2) == 0, fs, []string{"1", "2", "3", "0.5"}[rng.IntN(4)],
			[]string{"1", "2", "0.25", "10"}[rng.IntN(4)], [2]uint64{uint64(rng.IntN(8)), uint64(1 << rng.IntN(3))})
	}

	for i := 0; i < 150; i++ {
		n := 1 + rng.IntN(3)
		a, b := []any{}, []any{}
		for k := 0; k < n; k++ {
			a = append(a, puDI(int64(rng.IntN(5)-2)))
			b = append(b, puDI(int64(rng.IntN(5)-2)))
		}
		g.Add("rand/handle_int", "handle_int", [2]int64{int64(1 + rng.IntN(4)), int64(rng.IntN(n))}, a, b,
			[]int64{4, 3, 2, 1})
	}

	strs := []string{"", "a", "abc", "abcdefghij", "zz"}
	for i := 0; i < 200; i++ {
		var tcols [][2]any
		var a, b []any
		for k := 0; k < 1+rng.IntN(3); k++ {
			if rng.IntN(2) == 0 {
				tcols = append(tcols, [2]any{"int", 0})
				a = append(a, puDI(int64(rng.IntN(5)-2)))
				b = append(b, puDI(int64(rng.IntN(5)-2)))
			} else {
				tcols = append(tcols, [2]any{"str", 10})
				a = append(a, puDS(strs[rng.IntN(len(strs))]))
				b = append(b, puDS(strs[rng.IntN(len(strs))]))
			}
		}
		var icols [][2]int
		used := map[int]bool{}
		for k := 1 + rng.IntN(len(tcols)); k > 0; k-- {
			off := rng.IntN(len(tcols))
			if used[off] {
				continue
			}
			used[off] = true
			length := -1
			if tcols[off][0] == "str" && rng.IntN(2) == 0 {
				length = 3
			}
			icols = append(icols, [2]int{off, length})
		}
		schema := []int64{102, 101, 100}
		if rng.IntN(3) == 0 {
			schema = []int64{100}
		}
		g.Add("rand/handle_common", "handle_common", tcols, icols, a, b, schema)
	}

	kinds := []string{"const", "cor", "lt", "col"}
	for i := 0; i < 300; i++ {
		n := 1 + rng.IntN(3)
		ids := make([]int64, 0, n)
		lens := make([]int, 0, n)
		for k := 0; k < n; k++ {
			ids = append(ids, int64(k+1))
			lens = append(lens, []int{-1, -1, 3}[rng.IntN(3)])
		}
		var filters [][3]any
		for k := rng.IntN(4); k > 0; k-- {
			filters = append(filters, [3]any{kinds[rng.IntN(4)], int64(1 + rng.IntN(n+1)), rng.IntN(2) == 0})
		}
		if filters == nil {
			filters = [][3]any{}
		}
		g.Add("rand/split_cor", "split_cor", ids, lens, filters, rng.IntN(n))
	}

	for i := 0; i < 120; i++ {
		g.Add("rand/byitems", "byitems", randItems(3), randItems(3))
	}

	for _, v := range []int64{0, 1, -1, 255, 65536, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64} {
		g.Add("sys/misc", "misc", "enc32", v)
	}
	for i := 0; i < 40; i++ {
		var s, a []int64
		for k := rng.IntN(4); k > 0; k-- {
			s = append(s, int64(1+rng.IntN(5)))
		}
		for k := rng.IntN(5); k > 0; k-- {
			a = append(a, int64(1+rng.IntN(5)))
		}
		if s == nil {
			s = []int64{}
		}
		if a == nil {
			a = []int64{}
		}
		g.Add("rand/misc", "misc", "maxsort", s, a)
	}
	for _, t := range [][4]int64{{0, 0, 1, 0}, {1700000000, 123000000, 1700003600, 999999999},
		{1700000000, 100000000, 1700000000, 120000000}, {-1, 0, 253402300799, 0}, {1600000000, 1000000, 1600000001, 0}} {
		g.Add("sys/misc", "misc", "qtr", t)
	}
	for v := -1; v <= 4; v++ {
		g.Add("sys/misc", "misc", "exch", v)
		g.Add("sys/misc", "misc", "match", v)
		g.Add("sys/misc", "misc", "tasktype", v)
	}
	for _, c := range []struct {
		name string
		id   int32
	}{{"utf8mb4_bin", 46}, {"utf8mb4_general_ci", 45}, {"binary", 63}, {"utf8mb4_unicode_ci", 224},
		{"latin1_bin", -47}, {"nope", -46}, {"utf8mb4_0900_ai_ci", 255}, {"gbk_chinese_ci", 28}} {
		g.Add("sys/misc", "misc", "collid", c.name, c.id)
	}
	for i := 0; i < 30; i++ {
		var cols [][2]int64
		for k := rng.IntN(3); k > 0; k-- {
			cols = append(cols, [2]int64{int64(1 + rng.IntN(4)), []int64{-46, 46, 63, 224}[rng.IntN(4)]})
		}
		if cols == nil {
			cols = [][2]int64{}
		}
		g.Add("rand/misc", "misc", "explaincols", cols, []int64{3, 1, 2})
		g.Add("rand/misc", "misc", "sortitem", randItems(3))
	}
}
