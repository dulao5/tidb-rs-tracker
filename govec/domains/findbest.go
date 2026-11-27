package domains

// Domain "findbest": FindBestTask and the access paths of the first vertical slice (tidb-rs
// VS1-28, specs/D10.2-find-best-task): the final physical plan of a query after the whole
// DoOptimize (logical optimization, statistics derivation, FindBestTask, postOptimize), and
// the index pruning of the statistics loading point (PruneIndexesByWhereAndOrder).
//
// Records:
//   - "tables": as domain "selectbuild" (the same DDLs plus fbDDLs).
//   - "plan": args [ddls, sql, opts] (opts as optdriver's) -> the query is parsed,
//     preprocessed, walked by the query block hint handler, built, its flags adjusted and
//     run through core.DoOptimize with those flags minus the two statistics loading points
//     (CollectPredicateColumnsPoint, SyncWaitStatsLoadPoint: the mock context has no
//     statistics handle, see optdriver). The mock context gets a mock domain without a
//     statistics handle, so every table has pseudo statistics (as the new stack of tidb-rs).
//     Output {"plan": node, "plan_id", "w": [warnings]}, {"err", "code"}, {"unsupported": tp}
//     (an operator outside the slice in the logical plan) or {"panic": message}.
//     A node is {"tp", "id", "schema": [column strings]} plus, by type (no row counts and no
//     costs, as the plan columns of EXPLAIN without estRows / estCost):
//     TableReader {"store", "tbl": the table plan}, TableScan {"table", "ranges": [range
//     strings], "access", "filter", "keep", "desc"}, PointGet {"table", "handle", "index",
//     "values", "access"}, Selection {"conds"}, Projection {"exprs"}, Sort / TopN {"by"},
//     TopN / Limit {"offset", "count"}, TableDual {"rows"}, any other operator {"other": true}
//     (IndexReader, IndexLookUp, ...: the index paths of tidb-rs are a boundary).
//     Expressions are their StringWithCtx (the text of EXPLAIN's operator info).
//   - "prune": args [ddls, sql, interesting columns (positions in the DataSource's schema),
//     threshold, fill] -> the query is built (no optimization); the DataSource's paths
//     (FullIdxCols filled by IndexInfo2Cols when fill is true, as after fillIndexPath; nil
//     otherwise, as at the statistics loading point) are pruned by
//     rule.PruneIndexesByWhereAndOrder. Output {"kept": [path names], "single": [IsSingleScan
//     of every path afterwards]} or {"err", "code"}.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/domain"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	"github.com/pingcap/tidb/pkg/planner/core/rule"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/util/collate"
	utilhint "github.com/pingcap/tidb/pkg/util/hint"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/ranger"
)

const findbestSeed = 20261123

var fbMu sync.Mutex

func init() {
	vec.Register(&vec.Domain{
		Name:     "findbest",
		Seed:     findbestSeed,
		Coverage: findbestCoverage,
		Blocks:   []string{"D10.2", "D09.13", "D10.6", "D09.0", "D13.1", "D09.7", "D09.14"},
		Exempt:   findbestExempt(),
		Gzip:     true,
		Generate: findbestGenerate,
		Exec:     findbestExec,
	})
}

var findbestCoverage = map[string]vec.Coverage{
	"tables": {Min: 1},
	"plan": {Min: 1500, GoFuncs: []string{
		// find_best_task.go
		"findBestTask", "enumeratePhysicalPlans4Task", "iteratePhysicalPlan4BaseLogical", "compareTaskCost",
		"getTaskPlanCost", "appendCandidate4PhysicalOptimizeOp", "findBestTask4LogicalTableDual",
		"findBestTask4LogicalDataSource", "tryToGetDualTask", "compareBool", "compareIndexBack", "compareGlobalIndex",
		"compareCandidates", "isCandidatesPseudo", "comparePseudo", "compareEqOrIn", "isFullIndexMatch", "matchProperty",
		"getTableCandidate", "getIndexCandidate", "skylinePruning", "candidatePath.hasOnlyEqualPredicatesInDNF",
		"candidatePath.equalPredicateCount", "getPruningInfo", "isPointGetConvertableSchema", "exploreEnforcedPlan",
		"indexCoveringColumn", "isIndexColsCoveringCol", "isIndexCoveringColumns", "isIndexCoveringCondition",
		"isSingleScan", "SplitSelCondsWithVirtualColumn", "splitIndexFilterConditions", "convertToTableScan",
		"convertToPointGet", "convertToIndexScan", "PhysicalTableScan.addPushedDownSelection", "getOriginalPhysicalTableScan",
		"PhysicalTableScan.appendExtraHandleCol",
		"GroupRangesByCols", "PhysicalIndexScan.initSchema", "PhysicalIndexScan.addSelectionConditionForGlobalIndex",
		"PhysicalIndexScan.addPushedDownSelection", "getOriginalPhysicalIndexScan",
		"appendCandidate", "pushDownNot",
		// stats.go
		"deriveStats4DataSource", "fillIndexPath", "deriveIndexPathStats", "deriveTablePathStats",
		"deriveCommonHandleTablePathStats", "detachCondAndBuildRangeForPath", "getGeneralAttributesFromPaths",
		"getGroupNDVs", "initStats", "deriveStatsByFilter", "derivePathStatsAndTryHeuristics",
		// logicalop
		"BaseLogicalPlan.FindBestTask", "BaseLogicalPlan.PreparePossibleProperties", "DataSource.FindBestTask",
		"DataSource.PreparePossibleProperties", "LogicalProjection.PreparePossibleProperties",
		"LogicalSelection.PreparePossibleProperties", "LogicalSort.PreparePossibleProperties",
		"LogicalTopN.PreparePossibleProperties", "LogicalTableDual.FindBestTask",
		"pkg/planner/core/property_cols_prune.go:preparePossibleProperties",
		// the statistics derivation of the slice's operators (D10.6)
		"BaseLogicalPlan.DeriveStats", "BaseLogicalPlan.ExtractColGroups", "BaseLogicalPlan.RecursiveDeriveStats",
		"DataSource.DeriveStats", "LogicalLimit.DeriveStats", "LogicalProjection.DeriveStats",
		"LogicalProjection.ExtractColGroups", "LogicalSelection.DeriveStats", "LogicalTableDual.DeriveStats",
		"LogicalTopN.DeriveStats", "LogicalProjection.getGroupNDVs",
		"AdjustRowCountForTableScanByLimit", "crossEstimateTableRowCount",
		"AdjustRowCountForIndexScanByLimit", "crossEstimateIndexRowCount",
		// D09.0 / D13.1 helpers of the table, index and point get tasks
		"PointGetPlan.Init", "PhysicalIndexScan.Init", "PhysicalIndexReader.Init", "PhysicalIndexLookUpReader.Init",
		"optimizeByShuffle", "isSafePointGetPath4PlanCache", "isSafePointGetPath4PlanCacheScenario1",
		// the index pruning's ShouldPreferIndexMerge (skyline pruning)
		"ShouldPreferIndexMerge",
		// the statistics loading point: the interesting columns and the index pruning
		"CollectColumnStatsUsage", "newColumnStatsUsageCollector",
		"columnStatsUsageCollector.addPredicateColumn", "columnStatsUsageCollector.addPredicateColumnsFromExpressions",
		"columnStatsUsageCollector.collectFromPlan", "columnStatsUsageCollector.collectInterestingColumnsForDataSource",
		"columnStatsUsageCollector.collectPredicateColumnsForDataSource", "columnStatsUsageCollector.updateColMap",
		"columnStatsUsageCollector.updateColMapFromExpressions",
		"CollectPredicateColumnsPoint.Optimize", "CollectPredicateColumnsPoint.Name",
		"CollectPredicateColumnsPoint.pruneIndexesForAllDataSources",
		"CollectPredicateColumnsPoint.collectAndPruneDataSources", "pruneIndexesForDataSource",
	}},
	"prune": {Min: 300, GoFuncs: []string{
		"PruneIndexesByWhereAndOrder", "buildColumnRequirements", "buildFinalResult", "buildOrderingKey",
		"calculateScoreFromCoverage", "findSingleInterestingColumn", "newIndexSelectionState",
		"recordConsecutiveColumns", "scoreAndSort", "scoreIndexPath", "selectIndexes", "shouldAddIndex",
		"shouldAddIndexWithConsecutive", "shouldAddIndexWithoutConsecutive",
	}},
}

// fbDDLs: the selectbuild tables plus a table with many secondary indexes (the index
// pruning keeps at most max(threshold, 10) of them).
var fbDDLs = append(append([]string{}, sbDDLs...),
	"CREATE TABLE tm (a INT, b INT, c INT, d INT, e INT, f VARCHAR(20), KEY ia(a), KEY ib(b), KEY iab(a, b), "+
		"KEY iba(b, a), KEY ic(c), KEY icd(c, d), KEY ide(d, e), KEY ie(e), KEY ifp(f(5)), KEY iaf(a, f), "+
		"KEY ibcd(b, c, d), UNIQUE KEY uae(a, e), KEY iff(f))",
)

// ---------------------------------------------------------------- dumps

func fbStr(sctx *mock.Context, e expression.Expression) any {
	return vec.GoString(e.StringWithCtx(sctx.GetExprCtx().GetEvalCtx(), errors.RedactLogDisable))
}

func fbStrs[T expression.Expression](sctx *mock.Context, es []T) []any {
	out := []any{}
	for _, e := range es {
		out = append(out, fbStr(sctx, e))
	}
	return out
}

func fbBy(sctx *mock.Context, by []*plannerutil.ByItems) []any {
	out := []any{}
	for _, b := range by {
		out = append(out, []any{fbStr(sctx, b.Expr), b.Desc})
	}
	return out
}

// fbGroupedRanges normalizes GroupedRanges deterministically: Go's GroupRangesByCols
// iterates a map in random order, so groups are sorted lexicographically by their
// canonical range string list (M-MAPORDER).
func fbGroupedRanges(groups [][]*ranger.Range) []any {
	if len(groups) == 0 {
		return []any{}
	}
	type entry struct {
		key string
		val []any
	}
	entries := make([]entry, 0, len(groups))
	for _, g := range groups {
		strs := make([]string, 0, len(g))
		vals := make([]any, 0, len(g))
		for _, r := range g {
			s := r.String()
			strs = append(strs, s)
			vals = append(vals, vec.GoString(s))
		}
		entries = append(entries, entry{key: strings.Join(strs, "\x00"), val: vals})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e.val
	}
	return out
}

func fbDump(sctx *mock.Context, p base.PhysicalPlan) map[string]any {
	o := map[string]any{"tp": p.TP(), "id": p.ID(), "schema": fbStrs(sctx, p.Schema().Columns)}
	switch x := p.(type) {
	case *core.PhysicalTableReader:
		o["store"] = x.StoreType.Name()
		o["tbl"] = fbDump(sctx, x.GetTablePlan())
	case *core.PhysicalTableScan:
		o["table"] = x.Table.Name.O
		ranges := []any{}
		for _, r := range x.Ranges {
			ranges = append(ranges, vec.GoString(r.String()))
		}
		o["ranges"] = ranges
		if len(x.GroupedRanges) > 0 || len(x.GroupByColIdxs) > 0 {
			o["groupedRanges"] = fbGroupedRanges(x.GroupedRanges)
			idxs := make([]any, 0, len(x.GroupByColIdxs))
			for _, idx := range x.GroupByColIdxs {
				idxs = append(idxs, idx)
			}
			o["groupByColIdxs"] = idxs
		}
		o["access"] = fbStrs(sctx, x.AccessCondition)
		o["keep"], o["desc"] = x.KeepOrder, x.Desc
	case *core.PhysicalIndexReader:
		idxPlans := make([]any, 0, len(x.IndexPlans))
		for _, ip := range x.IndexPlans {
			idxPlans = append(idxPlans, []any{ip.TP(), ip.ID()})
		}
		o["indexPlans"] = idxPlans
		if len(x.IndexPlans) > 0 {
			o["idx"] = fbDump(sctx, x.IndexPlans[len(x.IndexPlans)-1])
		}
		o["outputCols"] = fbStrs(sctx, x.OutputColumns)
	case *core.PhysicalIndexLookUpReader:
		idxPlans := make([]any, 0, len(x.IndexPlans))
		for _, ip := range x.IndexPlans {
			idxPlans = append(idxPlans, []any{ip.TP(), ip.ID()})
		}
		o["indexPlans"] = idxPlans
		tblPlans := make([]any, 0, len(x.TablePlans))
		for _, tp := range x.TablePlans {
			tblPlans = append(tblPlans, []any{tp.TP(), tp.ID()})
		}
		o["tablePlans"] = tblPlans
		if len(x.IndexPlans) > 0 {
			o["idx"] = fbDump(sctx, x.IndexPlans[len(x.IndexPlans)-1])
		}
		if len(x.TablePlans) > 0 {
			o["tbl"] = fbDump(sctx, x.TablePlans[len(x.TablePlans)-1])
		}
		o["indexLookUpPushDown"] = x.IndexLookUpPushDown
		unoKeys := make([]int, 0, len(x.IndexPlansUnNatureOrders))
		for k := range x.IndexPlansUnNatureOrders {
			unoKeys = append(unoKeys, k)
		}
		sort.Ints(unoKeys)
		uno := make([]any, 0, len(unoKeys))
		for _, k := range unoKeys {
			uno = append(uno, []any{k, x.IndexPlansUnNatureOrders[k]})
		}
		o["indexPlansUnNatureOrders"] = uno
		o["paging"] = x.Paging
		o["keepOrder"] = cvGetField(x, "keepOrder").Bool()
		if x.PushedLimit != nil {
			o["pushedLimit"] = map[string]any{"offset": x.PushedLimit.Offset, "count": x.PushedLimit.Count}
		}
		if x.ExtraHandleCol != nil {
			o["extraHandle"] = vec.GoString(x.ExtraHandleCol.StringWithCtx(sctx.GetExprCtx().GetEvalCtx(), errors.RedactLogDisable))
		}
		o["commonHandleCols"] = fbStrs(sctx, x.CommonHandleCols)
	case *core.PhysicalLocalIndexLookUp:
		offs := make([]any, 0, len(x.IndexHandleOffsets))
		for _, v := range x.IndexHandleOffsets {
			offs = append(offs, v)
		}
		o["indexHandleOffsets"] = offs
	case *core.PhysicalIndexScan:
		o["table"] = x.Table.Name.O
		o["index"] = x.Index.Name.O
		ranges := []any{}
		for _, r := range x.Ranges {
			ranges = append(ranges, vec.GoString(r.String()))
		}
		o["ranges"] = ranges
		o["groupedRanges"] = fbGroupedRanges(x.GroupedRanges)
		idxs := make([]any, 0, len(x.GroupByColIdxs))
		for _, idx := range x.GroupByColIdxs {
			idxs = append(idxs, idx)
		}
		o["groupByColIdxs"] = idxs
		o["access"] = fbStrs(sctx, x.AccessCondition)
		o["keep"], o["desc"] = x.KeepOrder, x.Desc
		o["doubleRead"] = x.DoubleRead
	case *core.PointGetPlan:
		o["table"] = x.TblInfo.Name.O
		if x.Handle != nil {
			o["handle"] = x.Handle.IntValue()
		}
		if x.IndexInfo != nil {
			o["index"] = x.IndexInfo.Name.O
		}
		vals := []any{}
		for _, d := range x.IndexValues {
			s, _ := d.ToString()
			vals = append(vals, vec.GoString(s))
		}
		o["values"] = vals
		o["access"] = fbStrs(sctx, x.AccessConditions)
	case *core.PhysicalSelection:
		o["conds"] = fbStrs(sctx, x.Conditions)
	case *core.PhysicalProjection:
		o["exprs"] = fbStrs(sctx, x.Exprs)
	case *core.PhysicalSort:
		o["by"] = fbBy(sctx, x.ByItems)
	case *core.PhysicalTopN:
		o["by"] = fbBy(sctx, x.ByItems)
		o["offset"], o["count"] = x.Offset, x.Count
	case *core.PhysicalLimit:
		o["offset"], o["count"] = x.Offset, x.Count
	case *core.PhysicalTableDual:
		o["rows"] = x.RowCount
	default:
		o["other"] = true
	}
	children := []any{}
	for _, c := range p.Children() {
		children = append(children, fbDump(sctx, c))
	}
	o["children"] = children
	return o
}

// ---------------------------------------------------------------- exec

func findbestExec(op string, p []json.RawMessage) (out any, err error) {
	fbMu.Lock()
	defer fbMu.Unlock()
	switch op {
	case "tables":
		return odOptimize(op, p)
	case "plan":
		return fbPlan(p)
	case "prune":
		return fbPrune(p)
	}
	return nil, fmt.Errorf("findbest: unknown op %s", op)
}

// fbSession: the shared mock context (rbSession) with a domain without a statistics handle
// (every table gets the pseudo statistics of getStatsTable's first case, as the new stack of
// tidb-rs) and no synchronous statistics loading; the returned function restores the
// shared context for the other domains.
func fbSession() (*mock.Context, func()) {
	sctx := rbSession(rbDefaultOpts)
	oldDomain := sctx.GetDomain()
	vars := sctx.GetSessionVars()
	oldWait := vars.StatsLoadSyncWait.Load()
	sctx.BindDomain(domain.NewMockDomain())
	vars.StatsLoadSyncWait.Store(0)
	return sctx, func() {
		sctx.BindDomain(oldDomain)
		vars.StatsLoadSyncWait.Store(oldWait)
		vars.EnableStableResultMode = false
	}
}

// fbBuild: parse, preprocess, hint handler and build (as optdriver's odOptimize).
func fbBuild(sctx *mock.Context, p []json.RawMessage, o odOpts) (*core.PlanBuilder, base.LogicalPlan, map[string]any) {
	collate.SetNewCollationEnabledForTest(true)
	is, err := rbIS(arg[[]string](p, 0))
	if err != nil {
		return nil, nil, sbErr(err)
	}
	vars := sctx.GetSessionVars()
	vars.SQLMode = o.Mode
	vars.RemoveOrderbyInSubquery = !o.KeepOrder
	vars.EnableStableResultMode = o.Stable
	vars.StmtCtx.UseDynamicPruneMode = o.Dynamic
	stmt, err := parser.New().ParseOneStmt(arg[string](p, 1), "", "")
	if err != nil {
		return nil, nil, sbErr(err)
	}
	nodeW := resolve.NewNodeW(stmt)
	ret := &core.PreprocessorReturn{InfoSchema: is}
	if err := core.Preprocess(context.Background(), sctx, nodeW, core.WithPreprocessorReturn(ret)); err != nil {
		return nil, nil, sbErr(err)
	}
	hintProcessor := utilhint.NewQBHintHandler(vars.StmtCtx)
	nodeW.Node.Accept(hintProcessor)
	builder, _ := core.NewPlanBuilder().Init(sctx.GetPlanCtx(), is, hintProcessor)
	plan, err := builder.Build(context.Background(), nodeW)
	if err != nil {
		return nil, nil, sbErr(err)
	}
	lp, ok := plan.(base.LogicalPlan)
	if !ok {
		return nil, nil, map[string]any{"unsupported": fmt.Sprintf("%T", plan)}
	}
	if s := odSupported(lp); s != "" {
		return nil, nil, map[string]any{"unsupported": s}
	}
	return builder, lp, nil
}

func fbPlan(p []json.RawMessage) (out any, err error) {
	sbMu.Lock()
	defer sbMu.Unlock()
	rbMu.Lock()
	defer rbMu.Unlock()
	o := arg[odOpts](p, 2)
	sctx, restore := fbSession()
	defer restore()
	if o.NoIndexMerge {
		prev := sctx.GetSessionVars().GetEnableIndexMerge()
		sctx.GetSessionVars().SetEnableIndexMerge(false)
		defer sctx.GetSessionVars().SetEnableIndexMerge(prev)
	}
	if o.PushdownPolicy != "" {
		vars := sctx.GetSessionVars()
		prevPolicy := vars.IndexLookUpPushDownPolicy
		prevIso := vars.TxnCtx.Isolation
		vars.IndexLookUpPushDownPolicy = o.PushdownPolicy
		vars.TxnCtx.Isolation = "REPEATABLE-READ"
		defer func() {
			vars.IndexLookUpPushDownPolicy = prevPolicy
			vars.TxnCtx.Isolation = prevIso
		}()
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = map[string]any{"panic": vec.GoString(fmt.Sprint(r))}, nil
		}
	}()
	builder, lp, early := fbBuild(sctx, p, o)
	if early != nil {
		return early, nil
	}
	vars := sctx.GetSessionVars()
	flag := odAdjustOptimizationFlags(builder.GetOptFlag(), lp)
	run := flag &^ (rule.FlagCollectPredicateColumnsPoint | rule.FlagSyncWaitStatsLoadPoint)
	final, _, err := core.DoOptimize(context.Background(), sctx.GetPlanCtx(), run, lp)
	if err != nil {
		return sbErr(err), nil
	}
	warns := []any{}
	for _, w := range vars.StmtCtx.GetWarnings() {
		warns = append(warns, vec.GoString(w.Err.Error()))
	}
	return map[string]any{"plan": fbDump(sctx, final), "plan_id": vars.PlanID.Load(), "w": warns}, nil
}

func fbFirstDataSource(p base.LogicalPlan) *logicalop.DataSource {
	if ds, ok := p.(*logicalop.DataSource); ok {
		return ds
	}
	for _, c := range p.Children() {
		if ds := fbFirstDataSource(c); ds != nil {
			return ds
		}
	}
	return nil
}

func fbPathName(path *plannerutil.AccessPath) string {
	if path.IsTablePath() {
		if path.StoreType == kv.TiFlash {
			return "<table:tiflash>"
		}
		return "<table>"
	}
	return path.Index.Name.O
}

func fbPrune(p []json.RawMessage) (any, error) {
	sbMu.Lock()
	defer sbMu.Unlock()
	rbMu.Lock()
	defer rbMu.Unlock()
	sctx, restore := fbSession()
	defer restore()
	_, lp, early := fbBuild(sctx, p, odOpts{Mode: mysql.ModeOnlyFullGroupBy})
	if early != nil {
		return early, nil
	}
	ds := fbFirstDataSource(lp)
	if ds == nil {
		return map[string]any{"unsupported": "no DataSource"}, nil
	}
	cols := []*expression.Column{}
	for _, i := range arg[[]int](p, 2) {
		if i < ds.Schema().Len() {
			cols = append(cols, ds.Schema().Columns[i])
		}
	}
	paths := ds.AllPossibleAccessPaths
	if arg[bool](p, 4) {
		for _, path := range paths {
			if path.IsTablePath() || path.Index == nil {
				continue
			}
			path.IdxCols, path.IdxColLens, path.FullIdxCols, path.FullIdxColLens =
				plannerutil.IndexInfo2Cols(ds.Columns, ds.Schema().Columns, path.Index)
		}
	}
	_ = sctx
	kept := rule.PruneIndexesByWhereAndOrder(ds, paths, cols, arg[int](p, 3))
	names := []any{}
	for _, path := range kept {
		names = append(names, fbPathName(path))
	}
	single := []any{}
	for _, path := range paths {
		single = append(single, path.IsSingleScan)
	}
	return map[string]any{"kept": names, "single": single}, nil
}

// ---------------------------------------------------------------- generate

// fbVS1: the statements of the first vertical slice on sbtest1 (sysbench read_only without
// aggregation, and the shapes of VS1 plan §1.1). None may stop at an index reader.
var fbVS1 = []string{
	"SELECT c FROM sbtest1 WHERE id=10",
	"SELECT c FROM sbtest1 WHERE id=1",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 5000 AND 5099",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100 ORDER BY c",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 5000 AND 5099 ORDER BY c",
	"SELECT * FROM sbtest1",
	"SELECT * FROM sbtest1 WHERE id > 5",
	"SELECT id, c FROM sbtest1 WHERE id > 5 AND pad = 'a'",
	"SELECT id, c FROM sbtest1 WHERE id < 100 AND c > 'x'",
	"SELECT id + 1, pad FROM sbtest1 WHERE id >= 10",
	"SELECT c FROM sbtest1 LIMIT 10",
	"SELECT c FROM sbtest1 WHERE id > 7 LIMIT 3",
	"SELECT c FROM sbtest1 LIMIT 5, 10",
	"SELECT c FROM sbtest1 ORDER BY c",
	"SELECT c FROM sbtest1 ORDER BY id",
	"SELECT c FROM sbtest1 ORDER BY id DESC LIMIT 5",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100 ORDER BY c LIMIT 10",
	"SELECT c, pad FROM sbtest1 ORDER BY c DESC, pad LIMIT 3",
	"SELECT c FROM sbtest1 WHERE 1 = 0",
	"SELECT c FROM sbtest1 WHERE id = 3 AND id = 4",
	"SELECT 1",
	"SELECT 1 + 2, 'a'",
	"SELECT c FROM sbtest1 WHERE id IN (1)",
	"SELECT c FROM sbtest1 WHERE id = 10 AND pad = 'x'",
	"SELECT id FROM sbtest1 WHERE id BETWEEN 1 AND 100",
	"SELECT pad FROM sbtest1 WHERE id BETWEEN 1 AND 100 ORDER BY id LIMIT 7",
}

func findbestGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/tables", "tables", fbDDLs)
	def := odOpts{Mode: mysql.ModeOnlyFullGroupBy}
	for _, sql := range fbVS1 {
		g.Add("sys/vs1", "plan", fbDDLs, sql, def)
	}
	for _, sql := range sbFixed {
		g.Add("sys/fixed", "plan", fbDDLs, sql, def)
		g.Add("sys/fixed", "plan", fbDDLs, sql, odOpts{KeepOrder: true, Stable: true, Dynamic: true})
	}
	for _, sql := range fbIndexQueries {
		g.Add("sys/index", "plan", fbDDLs, sql, def)
	}
	pushForce := odOpts{Mode: mysql.ModeOnlyFullGroupBy, PushdownPolicy: "force"}
	for _, sql := range []string{
		"SELECT c FROM sbtest1 WHERE k = 5",
		"SELECT * FROM t2 WHERE a = 1",
		"SELECT * FROM tm WHERE a = 1 AND e = 2",
		"SELECT * FROM tc WHERE b = 'xyz' AND a = 2",
		"SELECT c FROM sbtest1 ORDER BY k LIMIT 3",
		"SELECT * FROM tm WHERE b = 1 ORDER BY a LIMIT 2",
	} {
		g.Add("sys/pushdown", "plan", fbDDLs, sql, pushForce)
	}
	noMerge := odOpts{Mode: mysql.ModeOnlyFullGroupBy, NoIndexMerge: true}
	for _, sql := range fbGroupRangeQueries {
		g.Add("sys/index", "plan", fbDDLs, sql, noMerge)
	}
	for n := 0; n < 1500; n++ {
		m := sbModes(rng)
		var sql string
		if rng.IntN(4) == 0 {
			sql = fbRandTM(rng)
		} else {
			sql = sbRandQuery(rng)
		}
		g.Add("rand/query", "plan", fbDDLs, sql,
			odOpts{Mode: m.Mode, KeepOrder: m.KeepOrder, Stable: rng.IntN(3) == 0, Dynamic: rng.IntN(2) == 0})
	}
	fbGenPrune(g, rng)
	// Clustered common-handle table without secondary indexes so single-read TableRangeScan
	// GroupRangesByCols vectors (including the 2048/2049 cap) run without hitting the D10.2
	// double-read boundary on tc's prefix index ip(b(3)).
	tchDDLs := []string{
		"CREATE TABLE tch (a INT, b VARCHAR(10), c INT, PRIMARY KEY (b, a) CLUSTERED)",
	}
	g.Add("sys/tables", "tables", tchDDLs)
	for _, sql := range fbClusteredGroupRangeQueries {
		g.Add("sys/index", "plan", tchDDLs, sql, noMerge)
	}
}

// fbIndexQueries: shapes on tables with secondary indexes (the skyline pruning keeps or
// drops the index paths; an index path left is a boundary in tidb-rs).
var fbIndexQueries = []string{
	"SELECT c FROM sbtest1 WHERE k = 5",
	"SELECT id, k FROM sbtest1 WHERE k > 5 AND k < 10",
	"SELECT k FROM sbtest1 ORDER BY k LIMIT 3",
	"SELECT * FROM t2 WHERE a = 1",
	"SELECT a FROM t2 WHERE b = 'x' AND c = 1.5",
	"SELECT a FROM t2 WHERE d > 3",
	"SELECT * FROM t2 WHERE e < 3",
	"SELECT * FROM tc WHERE c = 4",
	"SELECT * FROM tc WHERE b = 'xyz' AND a = 2",
	"SELECT * FROM tc WHERE b > 'a'",
	"SELECT * FROM ti WHERE b = 3",
	"SELECT * FROM ti WHERE c = 'x'",
	"SELECT * FROM t3 WHERE x = 'q'",
	"SELECT f FROM tm WHERE f = 'abc'",
	"SELECT * FROM tm WHERE a = 1 AND e = 2",
	"SELECT * FROM tm WHERE b = 1 ORDER BY a LIMIT 2",
	"SELECT d FROM tm WHERE c > 2",
	"SELECT * FROM tm",
	"SELECT k FROM sbtest1 WHERE k BETWEEN 1 AND 100",
}

// fbBalancedOr builds a balanced binary OR tree of `terms` (depth O(log n)) so 2048/2049
// distinct point predicates exercise GroupRangesByCols's maxGroupCount=2048 boundary without
// hitting IN (D08.9) or deep parser/rewriter recursion.
func fbBalancedOr(terms []string) string {
	if len(terms) == 1 {
		return terms[0]
	}
	mid := len(terms) / 2
	return "(" + fbBalancedOr(terms[:mid]) + " OR " + fbBalancedOr(terms[mid:]) + ")"
}

func fbIntOrQuery(n int) string {
	terms := make([]string, n)
	for i := 0; i < n; i++ {
		terms[i] = fmt.Sprintf("a = %d", i*2)
	}
	return "SELECT a, b FROM tm WHERE " + fbBalancedOr(terms) + " ORDER BY b LIMIT 10"
}

func fbStrOrQuery(n int) string {
	terms := make([]string, n)
	for i := 0; i < n; i++ {
		terms[i] = fmt.Sprintf("b = 'k%04d'", i)
	}
	return "SELECT * FROM tch WHERE " + fbBalancedOr(terms) + " ORDER BY a LIMIT 10"
}

// fbGroupRangeQueries: multi-point OR queries with index merge disabled so Go and Rust both
// exercise GroupRangesByCols on single-read IndexScan without hitting the D10.7 index-merge
// boundary (including the maxGroupCount = 2048 / 2049 boundary).
var fbGroupRangeQueries = []string{
	"SELECT a, b FROM tm WHERE (a = 1 OR a = 3 OR a = 5) ORDER BY b",
	"SELECT a, b FROM tm WHERE (a = 1 OR a = 3 OR a = 5) ORDER BY b LIMIT 5",
	"SELECT b, c FROM tm WHERE (b = 1 OR b = 4) ORDER BY c LIMIT 4",
	"SELECT * FROM tc WHERE (b = 'a' OR b = 'b') ORDER BY a LIMIT 5",
	fbIntOrQuery(2048),
	fbIntOrQuery(2049),
}

// fbClusteredGroupRangeQueries: same GroupRangesByCols + 2048/2049 boundary on a clustered
// common-handle table with no secondary indexes (`tch`).
var fbClusteredGroupRangeQueries = []string{
	"SELECT * FROM tch WHERE (b = 'a' OR b = 'b') ORDER BY a LIMIT 5",
	fbStrOrQuery(2048),
	fbStrOrQuery(2049),
}

func fbRandTM(rng *rand.Rand) string {
	cols := []string{"a", "b", "c", "d", "e"}
	q := "SELECT * FROM tm"
	if rng.IntN(2) == 0 {
		q = "SELECT " + cols[rng.IntN(5)] + " FROM tm"
	}
	conds := []string{}
	for i := rng.IntN(3); i > 0; i-- {
		c := cols[rng.IntN(5)]
		switch rng.IntN(3) {
		case 0:
			conds = append(conds, c+" = "+strconv.Itoa(rng.IntN(9)))
		case 1:
			conds = append(conds, c+" > "+strconv.Itoa(rng.IntN(9)))
		default:
			conds = append(conds, c+" BETWEEN 1 AND "+strconv.Itoa(1+rng.IntN(9)))
		}
	}
	for i, c := range conds {
		if i == 0 {
			q += " WHERE " + c
		} else {
			q += " AND " + c
		}
	}
	if rng.IntN(3) == 0 {
		q += " ORDER BY " + cols[rng.IntN(5)]
		if rng.IntN(2) == 0 {
			q += " LIMIT " + strconv.Itoa(1+rng.IntN(9))
		}
	}
	return q
}

func fbGenPrune(g *vec.Gen, rng *rand.Rand) {
	type tbl struct {
		name  string
		ncols int
	}
	tables := []tbl{{"tm", 6}, {"t2", 5}, {"sbtest1", 4}, {"tc", 3}, {"ti", 3}}
	thresholds := []int{-1, 0, 1, 2, 3, 5, 10, 20}
	for _, t := range tables {
		for _, th := range thresholds {
			g.Add("sys/prune", "prune", fbDDLs, "SELECT * FROM "+t.name, []int{0}, th, false)
			g.Add("sys/prune", "prune", fbDDLs, "SELECT * FROM "+t.name, []int{0, 1}, th, true)
			g.Add("sys/prune", "prune", fbDDLs, "SELECT * FROM "+t.name, []int{}, th, true)
		}
	}
	for n := 0; n < 300; n++ {
		t := tables[rng.IntN(len(tables))]
		cols := []int{}
		for i := rng.IntN(4); i > 0; i-- {
			cols = append(cols, rng.IntN(t.ncols))
		}
		sel := "*"
		if rng.IntN(3) == 0 {
			sel = "a"
			if t.name == "sbtest1" {
				sel = "k"
			}
		}
		g.Add("rand/prune", "prune", fbDDLs, "SELECT "+sel+" FROM "+t.name, cols,
			thresholds[rng.IntN(len(thresholds))], rng.IntN(2) == 0)
	}
}
