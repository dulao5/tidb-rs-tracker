package domains

// Domain "optdriver": the optimizer driver of the first vertical slice (tidb-rs VS1-24,
// specs/D09.13-optimizer): the logical optimization as the driver runs it, and the post
// passes over physical plans (postOptimize, with InjectExtraProjection and the physical
// projection elimination), plus the small pure helpers of the block (fixcontrol, the
// key-info helpers).
//
// The physical optimization (physicalOptimize: statistics derivation, FindBestTask, cost)
// is not in this domain: it needs the FindBestTask task of tidb-rs (VS1-28). The final
// physical plan of a query (the whole DoOptimize) is recorded by that task's EXPLAIN
// vectors.
//
// Records:
//   - "tables": as domain "selectbuild" (the same DDLs).
//   - "optimize": args [ddls, sql, opts] (opts {"mode", "keep_order", "stable": tidb_enable_
//     stable_result_mode, "dynamic": StmtCtx.UseDynamicPruneMode}) -> the query is parsed,
//     preprocessed, walked by the query block hint handler (hint.NewQBHintHandler, as
//     optimize() does: the SELECTs get their query block offsets), built with that handler,
//     and the builder's flags adjusted (adjustOptimizationFlags); logicalOptimize then runs
//     with those flags minus the two statistics loading points (CollectPredicateColumnsPoint,
//     SyncWaitStatsLoadPoint: they never change the plan of these tables, and the mock
//     context has no statistics handle; tidb-rs runs them as no-ops, see its spec).
//     Output {"flag": adjusted flag, "plan": node, "plan_id", "col_id", "w": [warnings]},
//     {"err", "code"} or {"unsupported": tp} (an operator outside the slice).
//     A plan node is selectbuild's, plus TopN {"by", "offset", "count"}.
//   - "post": args [spec] -> a physical plan built from spec (see odBuild) is run through
//     postOptimize; output the plan: {"tp", "id", "rows", "schema": [expr], "children"} plus
//     Projection {"exprs", "avoid"}, Selection {"conds"}, Sort / TopN {"by"}, TopN / Limit
//     {"offset", "count"}, TableDual {"dual_rows"}; or {"panic": message} when Go panics.
//   - "fix": args [map {key: value}, key, kind ("bool", "int", "float", "str"), default]
//     -> [value, exists, parse error] of Get<Kind> and the value of Get<Kind>WithDefault.
//   - "max_one_row": args [keys [[uid]], unique keys [[uid]], eq ids [uid]] -> bool.
//   - "index_key": args [unique, index column names, columns [[name, not null]]] ->
//     [unique key uids | null, new key uids | null] of CheckIndexCanBeKey over a schema whose
//     column i has unique id 100 + i.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"sync"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	"github.com/pingcap/tidb/pkg/planner/core/rule"
	ruleutil "github.com/pingcap/tidb/pkg/planner/core/rule/util"
	"github.com/pingcap/tidb/pkg/planner/property"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/planner/util/fixcontrol"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	utilhint "github.com/pingcap/tidb/pkg/util/hint"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const optdriverSeed = 20261122

var odMu sync.Mutex

//go:linkname odAdjustOptimizationFlags github.com/pingcap/tidb/pkg/planner/core.adjustOptimizationFlags
func odAdjustOptimizationFlags(flag uint64, logic base.LogicalPlan) uint64

//go:linkname odPostOptimize github.com/pingcap/tidb/pkg/planner/core.postOptimize
func odPostOptimize(ctx context.Context, sctx base.PlanContext, plan base.PhysicalPlan) base.PhysicalPlan

func init() {
	vec.Register(&vec.Domain{
		Name:     "optdriver",
		Seed:     optdriverSeed,
		Coverage: optdriverCoverage,
		Blocks:   []string{"D09.13", "D13.3"},
		Exempt:   optdriverExempt(),
		Gzip:     true,
		Generate: optdriverGenerate,
		Exec:     optdriverExec,
	})
}

var optdriverCoverage = map[string]vec.Coverage{
	"tables": {Min: 1},
	"optimize": {Min: 1500, GoFuncs: []string{
		"logicalOptimize", "LogicalOptimizeTest", "adjustOptimizationFlags", "isLogicalRuleDisabled",
		"checkStableResultMode",
		"ResultReorder.Optimize", "ResultReorder.Name", "ResultReorder.completeSort", "ResultReorder.injectSort",
		"ResultReorder.isInputOrderKeeper", "ResultReorder.extractHandleCol",
		"ConstantPropagationSolver.Optimize", "ConstantPropagationSolver.execOptimize", "ConstantPropagationSolver.Name",
		"NewQBHintHandler", "QBHintHandler.Enter", "QBHintHandler.Leave", "QBHintHandler.checkQueryBlockHints",
		"QBHintHandler.handleViewHints", "QBHintHandler.NewBuildState", "QBHintHandler.MaxSelectStmtOffset",
		"QBHintHandler.HandleUnusedViewHints", "QBHintHandler.GetCurrentStmtHints", "QBHintHandler.isHint4View",
		"ParsePlanHints", "ExtractTableHintsFromStmtNode", "ParseStmtHints",
	}},
	"post": {Min: 300, GoFuncs: []string{
		"postOptimize", "InjectExtraProjection", "NewProjInjector", "projInjector.inject", "InjectProjBelowSort",
		"TurnNominalSortIntoProj", "mergeContinuousSelections", "eliminateUnionScanAndLock",
		"iteratePhysicalPlan", "avoidColumnEvaluatorForProjBelowUnion", "enableParallelApply",
		"handleFineGrainedShuffle", "propagateProbeParents", "countStarRewrite", "countStarRewriteInternal",
		"disableReuseChunkIfNeeded", "tryEnableLateMaterialization", "generateRuntimeFilter",
	}},
	"fix": {Min: 100, GoFuncs: []string{
		"GetBool", "GetBoolWithDefault", "GetInt", "GetIntWithDefault", "GetFloat", "GetFloatWithDefault",
		"GetStr", "GetStrWithDefault",
	}},
	"max_one_row": {Min: 50, GoFuncs: []string{"CheckMaxOneRowCond"}},
	"index_key":   {Min: 50, GoFuncs: []string{"CheckIndexCanBeKey"}},
}

type odOpts struct {
	Mode      mysql.SQLMode `json:"mode"`
	KeepOrder bool          `json:"keep_order"`
	Stable    bool          `json:"stable"`
	Dynamic   bool          `json:"dynamic"`
}

// odDump is sbDump (which dumps the TopN of the logical optimization too).
func odDump(sctx *mock.Context, p base.LogicalPlan) map[string]any {
	return sbDump(sctx, p)
}

func odSupported(p base.LogicalPlan) string {
	switch p.(type) {
	case *logicalop.DataSource, *logicalop.LogicalSelection, *logicalop.LogicalProjection, *logicalop.LogicalLimit,
		*logicalop.LogicalSort, *logicalop.LogicalTableDual, *logicalop.LogicalTopN, *logicalop.LogicalWindow:
	default:
		return p.TP()
	}
	for _, c := range p.Children() {
		if s := odSupported(c); s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- physical plans

// A spec node: {"op": "dual" | "proj" | "sel" | "sort" | "topn" | "nsort" | "limit", "rows":
// stats row count, "exp": the child's expected count (the required property of sort /
// topn / nsort), "cols": dual's column count (BIGINT columns), "exprs" / "conds": [expr
// spec], "by": [[expr spec, desc]], "only_col" (nsort), "offset", "count", "children"}.
// An expr spec is ["col", i] (the child's i-th column), ["const", k] or ["plus", spec, k].
type odSpec struct {
	Op       string            `json:"op"`
	Rows     float64           `json:"rows"`
	Exp      float64           `json:"exp"`
	Cols     int               `json:"cols"`
	Exprs    []json.RawMessage `json:"exprs"`
	By       []json.RawMessage `json:"by"`
	OnlyCol  bool              `json:"only_col"`
	Offset   uint64            `json:"offset"`
	Count    uint64            `json:"count"`
	Children []*odSpec         `json:"children"`
}

func odExpr(sctx *mock.Context, raw json.RawMessage, schema *expression.Schema) expression.Expression {
	var v []json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		panic(err)
	}
	var kind string
	_ = json.Unmarshal(v[0], &kind)
	switch kind {
	case "col":
		var i int
		_ = json.Unmarshal(v[1], &i)
		return schema.Columns[i].Clone()
	case "const":
		var k int64
		_ = json.Unmarshal(v[1], &k)
		return &expression.Constant{Value: types.NewIntDatum(k), RetType: types.NewFieldType(mysql.TypeLonglong)}
	case "plus":
		var k int64
		_ = json.Unmarshal(v[2], &k)
		a := odExpr(sctx, v[1], schema)
		c := &expression.Constant{Value: types.NewIntDatum(k), RetType: types.NewFieldType(mysql.TypeLonglong)}
		return expression.NewFunctionInternal(sctx.GetExprCtx(), ast.Plus, types.NewFieldType(mysql.TypeLonglong), a, c)
	}
	panic("odExpr " + kind)
}

func odStats(rows float64) *property.StatsInfo {
	return &property.StatsInfo{RowCount: rows}
}

func odBuild(sctx *mock.Context, s *odSpec) base.PhysicalPlan {
	children := []base.PhysicalPlan{}
	for _, c := range s.Children {
		children = append(children, odBuild(sctx, c))
	}
	var childSchema *expression.Schema
	if len(children) > 0 {
		childSchema = children[0].Schema()
	}
	prop := &property.PhysicalProperty{ExpectedCnt: s.Exp}
	byItems := func() []*plannerutil.ByItems {
		out := []*plannerutil.ByItems{}
		for _, raw := range s.By {
			var b []json.RawMessage
			_ = json.Unmarshal(raw, &b)
			var desc bool
			_ = json.Unmarshal(b[1], &desc)
			out = append(out, &plannerutil.ByItems{Expr: odExpr(sctx, b[0], childSchema), Desc: desc})
		}
		return out
	}
	var p base.PhysicalPlan
	switch s.Op {
	case "dual":
		d := core.PhysicalTableDual{RowCount: 1}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0)
		cols := []*expression.Column{}
		for i := 0; i < s.Cols; i++ {
			cols = append(cols, &expression.Column{UniqueID: sctx.GetSessionVars().AllocPlanColumnID(),
				RetType: types.NewFieldType(mysql.TypeLonglong), Index: i})
		}
		d.SetSchema(expression.NewSchema(cols...))
		p = d
	case "proj":
		exprs := []expression.Expression{}
		for _, raw := range s.Exprs {
			exprs = append(exprs, odExpr(sctx, raw, childSchema))
		}
		proj := core.PhysicalProjection{Exprs: exprs}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0, prop)
		cols := []*expression.Column{}
		for i, e := range exprs {
			if c, ok := e.(*expression.Column); ok {
				// An identity projection keeps the child's column (eliminable).
				cc := c.Clone().(*expression.Column)
				cc.Index = i
				cols = append(cols, cc)
				continue
			}
			cols = append(cols, &expression.Column{UniqueID: sctx.GetSessionVars().AllocPlanColumnID(),
				RetType: e.GetType(sctx.GetExprCtx().GetEvalCtx()), Index: i})
		}
		proj.SetSchema(expression.NewSchema(cols...))
		p = proj
	case "sel":
		conds := []expression.Expression{}
		for _, raw := range s.Exprs {
			conds = append(conds, odExpr(sctx, raw, childSchema))
		}
		p = core.PhysicalSelection{Conditions: conds}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0, prop)
	case "sort":
		p = core.PhysicalSort{ByItems: byItems()}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0, prop)
	case "topn":
		p = core.PhysicalTopN{ByItems: byItems(), Offset: s.Offset, Count: s.Count}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0, prop)
	case "nsort":
		p = core.NominalSort{ByItems: byItems(), OnlyColumn: s.OnlyCol}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0, prop)
	case "limit":
		l := core.PhysicalLimit{Offset: s.Offset, Count: s.Count}.Init(sctx.GetPlanCtx(), odStats(s.Rows), 0, prop)
		l.SetSchema(childSchema.Clone())
		p = l
	default:
		panic("odBuild " + s.Op)
	}
	if len(children) > 0 {
		p.SetChildren(children...)
	}
	return p
}

func odPhysDump(sctx *mock.Context, p base.PhysicalPlan) map[string]any {
	f := sbExprF(sctx)
	rows := 0.0
	if p.StatsInfo() != nil {
		rows = p.StatsInfo().RowCount
	}
	o := map[string]any{"tp": p.TP(), "id": p.ID(), "rows": strconv.FormatFloat(rows, 'g', -1, 64),
		"schema": lrExprs(p.Schema().Columns, f)}
	switch x := p.(type) {
	case *core.PhysicalProjection:
		o["exprs"] = lrExprs(x.Exprs, f)
		o["avoid"] = x.AvoidColumnEvaluator
	case *core.PhysicalSelection:
		o["conds"] = lrExprs(x.Conditions, f)
	case *core.PhysicalSort:
		o["by"] = lrBy(x.ByItems, f)
	case *core.PhysicalTopN:
		o["by"] = lrBy(x.ByItems, f)
		o["offset"], o["count"] = x.Offset, x.Count
	case *core.PhysicalLimit:
		o["offset"], o["count"] = x.Offset, x.Count
	case *core.NominalSort:
		o["by"] = lrBy(x.ByItems, f)
	case *core.PhysicalTableDual:
		o["dual_rows"] = x.RowCount
	}
	children := []any{}
	for _, c := range p.Children() {
		children = append(children, odPhysDump(sctx, c))
	}
	o["children"] = children
	return o
}

// ---------------------------------------------------------------- exec

func optdriverExec(op string, p []json.RawMessage) (out any, err error) {
	odMu.Lock()
	defer odMu.Unlock()
	switch op {
	case "tables", "optimize":
		return odOptimize(op, p)
	case "post":
		rbMu.Lock()
		defer rbMu.Unlock()
		sctx := rbSession(rbDefaultOpts)
		var s odSpec
		if err := json.Unmarshal(p[0], &s); err != nil {
			return nil, err
		}
		defer func() {
			if r := recover(); r != nil {
				out, err = map[string]any{"panic": fmt.Sprint(r)}, nil
			}
		}()
		plan := odBuild(sctx, &s)
		final := odPostOptimize(context.Background(), sctx.GetPlanCtx(), plan)
		return map[string]any{"plan": odPhysDump(sctx, final),
			"col_id": sctx.GetSessionVars().PlanColumnID.Load(), "plan_id": sctx.GetSessionVars().PlanID.Load()}, nil
	case "fix":
		m := map[uint64]string{}
		for k, v := range arg[map[string]string](p, 0) {
			n, _ := strconv.ParseUint(k, 10, 64)
			m[n] = v
		}
		key := arg[uint64](p, 1)
		switch arg[string](p, 2) {
		case "bool":
			v, ok := fixcontrol.GetBool(m, key)
			return []any{v, ok, false, fixcontrol.GetBoolWithDefault(m, key, arg[bool](p, 3))}, nil
		case "int":
			v, ok, e := fixcontrol.GetInt(m, key)
			return []any{v, ok, e != nil, fixcontrol.GetIntWithDefault(m, key, arg[int64](p, 3))}, nil
		case "float":
			v, ok, e := fixcontrol.GetFloat(m, key)
			return []any{odF(v), ok, e != nil, odF(fixcontrol.GetFloatWithDefault(m, key, arg[float64](p, 3)))}, nil
		case "str":
			v, ok := fixcontrol.GetStr(m, key)
			return []any{v, ok, false, fixcontrol.GetStrWithDefault(m, key, arg[string](p, 3))}, nil
		}
		return nil, fmt.Errorf("fix kind")
	case "max_one_row":
		keys := arg[[][]int64](p, 0)
		uks := arg[[][]int64](p, 1)
		eq := map[int64]struct{}{}
		for _, id := range arg[[]int64](p, 2) {
			eq[id] = struct{}{}
		}
		s := expression.NewSchema()
		for _, k := range keys {
			s.Keys = append(s.Keys, odKey(k))
		}
		for _, k := range uks {
			s.UniqueKeys = append(s.UniqueKeys, odKey(k))
		}
		return ruleutil.CheckMaxOneRowCond(eq, s), nil
	case "index_key":
		unique := arg[bool](p, 0)
		idxCols := arg[[]string](p, 1)
		cols := arg[[][]any](p, 2)
		idx := &model.IndexInfo{Unique: unique}
		for _, c := range idxCols {
			idx.Columns = append(idx.Columns, &model.IndexColumn{Name: pmodel.NewCIStr(c)})
		}
		infos := []*model.ColumnInfo{}
		schemaCols := []*expression.Column{}
		for i, c := range cols {
			ft := types.NewFieldType(mysql.TypeLonglong)
			if c[1].(bool) {
				ft.AddFlag(mysql.NotNullFlag)
			}
			infos = append(infos, &model.ColumnInfo{Name: pmodel.NewCIStr(c[0].(string)), FieldType: *ft})
			schemaCols = append(schemaCols, &expression.Column{UniqueID: int64(100 + i), RetType: ft})
		}
		uk, nk := ruleutil.CheckIndexCanBeKey(idx, infos, expression.NewSchema(schemaCols...))
		return []any{odKeyOut(uk), odKeyOut(nk)}, nil
	}
	return nil, fmt.Errorf("optdriver: unknown op %s", op)
}

func odF(f float64) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func odKey(ids []int64) expression.KeyInfo {
	k := expression.KeyInfo{}
	for _, id := range ids {
		k = append(k, &expression.Column{UniqueID: id})
	}
	return k
}

func odKeyOut(k expression.KeyInfo) any {
	if k == nil {
		return nil
	}
	out := []any{}
	for _, c := range k {
		out = append(out, c.UniqueID)
	}
	return out
}

func odOptimize(op string, p []json.RawMessage) (any, error) {
	sbMu.Lock()
	defer sbMu.Unlock()
	rbMu.Lock()
	defer rbMu.Unlock()
	collate.SetNewCollationEnabledForTest(true)
	ddls := arg[[]string](p, 0)
	is, err := rbIS(ddls)
	if err != nil {
		return nil, err
	}
	if op == "tables" {
		out := []any{}
		for _, t := range sbTableNames(ddls) {
			tbl, err := is.TableByName(context.Background(), pmodel.NewCIStr("test"), pmodel.NewCIStr(t))
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(tbl.Meta())
			if err != nil {
				return nil, err
			}
			out = append(out, string(b))
		}
		return out, nil
	}
	sctx := rbSession(rbDefaultOpts)
	o := arg[odOpts](p, 2)
	vars := sctx.GetSessionVars()
	vars.SQLMode = o.Mode
	vars.RemoveOrderbyInSubquery = !o.KeepOrder
	vars.EnableStableResultMode = o.Stable
	vars.StmtCtx.UseDynamicPruneMode = o.Dynamic
	defer func() { vars.EnableStableResultMode = false }()
	stmt, err := parser.New().ParseOneStmt(arg[string](p, 1), "", "")
	if err != nil {
		return sbErr(err), nil
	}
	nodeW := resolve.NewNodeW(stmt)
	ret := &core.PreprocessorReturn{InfoSchema: is}
	if err := core.Preprocess(context.Background(), sctx, nodeW, core.WithPreprocessorReturn(ret)); err != nil {
		return sbErr(err), nil
	}
	hintProcessor := utilhint.NewQBHintHandler(vars.StmtCtx)
	nodeW.Node.Accept(hintProcessor)
	builder, _ := core.NewPlanBuilder().Init(sctx.GetPlanCtx(), is, hintProcessor)
	plan, err := builder.Build(context.Background(), nodeW)
	if err != nil {
		return sbErr(err), nil
	}
	lp, ok := plan.(base.LogicalPlan)
	if !ok {
		return map[string]any{"unsupported": fmt.Sprintf("%T", plan)}, nil
	}
	if s := odSupported(lp); s != "" {
		return map[string]any{"unsupported": s}, nil
	}
	flag := odAdjustOptimizationFlags(builder.GetOptFlag(), lp)
	run := flag &^ (rule.FlagCollectPredicateColumnsPoint | rule.FlagSyncWaitStatsLoadPoint)
	lp, err = core.LogicalOptimizeTest(context.Background(), run, lp)
	if err != nil {
		return sbErr(err), nil
	}
	if s := odSupported(lp); s != "" {
		return map[string]any{"unsupported": s}, nil
	}
	warns := []any{}
	for _, w := range vars.StmtCtx.GetWarnings() {
		warns = append(warns, vec.GoString(w.Err.Error()))
	}
	return map[string]any{"flag": flag, "plan": odDump(sctx, lp), "plan_id": vars.PlanID.Load(),
		"col_id": vars.PlanColumnID.Load(), "w": warns}, nil
}

// ---------------------------------------------------------------- generate

func optdriverGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/tables", "tables", sbDDLs)
	for _, sql := range sbFixed {
		g.Add("sys/fixed", "optimize", sbDDLs, sql, odOpts{Mode: mysql.ModeOnlyFullGroupBy})
		g.Add("sys/fixed", "optimize", sbDDLs, sql, odOpts{KeepOrder: true, Stable: true, Dynamic: true})
	}
	for n := 0; n < 1500; n++ {
		m := sbModes(rng)
		g.Add("rand/query", "optimize", sbDDLs, sbRandQuery(rng),
			odOpts{Mode: m.Mode, KeepOrder: m.KeepOrder, Stable: rng.IntN(3) == 0, Dynamic: rng.IntN(2) == 0})
	}
	for _, s := range odFixedPlans() {
		g.Add("sys/post", "post", s)
	}
	for n := 0; n < 400; n++ {
		g.Add("rand/post", "post", odRandPlan(rng, 0))
	}
	odGenFix(g, rng)
	odGenKeys(g, rng)
}

func odDual(cols int, rows float64) map[string]any {
	return map[string]any{"op": "dual", "cols": cols, "rows": rows}
}

func odFixedPlans() []map[string]any {
	col := func(i int) []any { return []any{"col", i} }
	plus := func(e any, k int) []any { return []any{"plus", e, k} }
	return []map[string]any{
		odDual(2, 1),
		{"op": "sort", "rows": 10, "exp": 10, "by": []any{[]any{col(0), false}}, "children": []any{odDual(2, 10)}},
		{"op": "sort", "rows": 10, "exp": 10, "by": []any{[]any{plus(col(0), 1), true}, []any{col(1), false}}, "children": []any{odDual(2, 10)}},
		{"op": "topn", "rows": 3, "exp": 5, "offset": 1, "count": 3, "by": []any{[]any{plus(col(1), 2), false}}, "children": []any{odDual(3, 8)}},
		{"op": "nsort", "rows": 4, "exp": 4, "only_col": true, "by": []any{[]any{col(0), false}}, "children": []any{odDual(2, 4)}},
		{"op": "nsort", "rows": 4, "exp": 2, "by": []any{[]any{plus(col(0), 1), false}}, "children": []any{odDual(2, 4)}},
		{"op": "proj", "rows": 5, "exprs": []any{col(0), col(1)}, "children": []any{odDual(2, 5)}},
		{"op": "proj", "rows": 5, "exprs": []any{col(1), col(0)}, "children": []any{odDual(2, 5)}},
		{"op": "proj", "rows": 5, "exprs": []any{col(0)}, "children": []any{
			map[string]any{"op": "proj", "rows": 5, "exprs": []any{col(0), col(1)}, "children": []any{odDual(2, 5)}}}},
		{"op": "sel", "rows": 2, "exprs": []any{plus(col(0), 1)}, "children": []any{
			map[string]any{"op": "sel", "rows": 3, "exprs": []any{col(1)}, "children": []any{
				map[string]any{"op": "sel", "rows": 4, "exprs": []any{col(0)}, "children": []any{odDual(2, 5)}}}}}},
		{"op": "sort", "rows": 5, "exp": 5, "by": []any{[]any{plus(col(0), 3), false}}, "children": []any{
			map[string]any{"op": "proj", "rows": 5, "exprs": []any{col(0), col(0), plus(col(1), 1)}, "children": []any{odDual(2, 5)}}}},
		{"op": "limit", "rows": 2, "offset": 0, "count": 2, "children": []any{
			map[string]any{"op": "topn", "rows": 2, "exp": 2, "count": 2, "by": []any{[]any{plus(col(0), 1), false}}, "children": []any{odDual(2, 9)}}}},
	}
}

// odRandPlan: a random chain of the slice's physical operators over a dual.
func odRandPlan(rng *rand.Rand, depth int) map[string]any {
	ncols := 1 + rng.IntN(3)
	if depth > 3 || rng.IntN(4) == 0 {
		return odDual(ncols, float64(1+rng.IntN(20)))
	}
	child := odRandPlan(rng, depth+1)
	width := odWidth(child)
	expr := func() any {
		c := []any{"col", rng.IntN(width)}
		switch rng.IntN(4) {
		case 0:
			return []any{"plus", c, rng.IntN(5)}
		case 1:
			if rng.IntN(3) == 0 {
				return []any{"const", rng.IntN(3)}
			}
		}
		return c
	}
	by := func() []any {
		n := 1 + rng.IntN(3)
		out := []any{}
		for i := 0; i < n; i++ {
			out = append(out, []any{expr(), rng.IntN(2) == 0})
		}
		return out
	}
	rows := float64(1 + rng.IntN(20))
	switch rng.IntN(6) {
	case 0:
		n := 1 + rng.IntN(3)
		exprs := []any{}
		for i := 0; i < n; i++ {
			exprs = append(exprs, expr())
		}
		if rng.IntN(3) == 0 {
			exprs = []any{}
			for i := 0; i < width; i++ {
				exprs = append(exprs, []any{"col", i})
			}
		}
		return map[string]any{"op": "proj", "rows": rows, "exprs": exprs, "children": []any{child}}
	case 1:
		return map[string]any{"op": "sel", "rows": rows, "exprs": []any{expr()}, "children": []any{child}}
	case 2:
		return map[string]any{"op": "sort", "rows": rows, "exp": rows, "by": by(), "children": []any{child}}
	case 3:
		return map[string]any{"op": "topn", "rows": rows, "exp": float64(1 + rng.IntN(30)), "offset": rng.IntN(3),
			"count": 1 + rng.IntN(5), "by": by(), "children": []any{child}}
	case 4:
		return map[string]any{"op": "nsort", "rows": rows, "exp": float64(1 + rng.IntN(30)), "only_col": rng.IntN(2) == 0,
			"by": by(), "children": []any{child}}
	default:
		return map[string]any{"op": "limit", "rows": rows, "offset": rng.IntN(3), "count": 1 + rng.IntN(5),
			"children": []any{child}}
	}
}

// odWidth: the schema width of a spec node.
func odWidth(s map[string]any) int {
	switch s["op"] {
	case "dual":
		return s["cols"].(int)
	case "proj":
		return len(s["exprs"].([]any))
	}
	return odWidth(s["children"].([]any)[0].(map[string]any))
}

func odGenFix(g *vec.Gen, rng *rand.Rand) {
	vals := []string{"ON", "on", "1", "OFF", "0", "true", "", "12", "-3", "1.5", "1e3", "abc", "9223372036854775808", "1e400", "-1e400", "+Inf", "NaN", " 1"}
	kinds := []string{"bool", "int", "float", "str"}
	defs := map[string]any{"bool": true, "int": int64(7), "float": 2.5, "str": "dflt"}
	for _, k := range kinds {
		g.Add("sys/fix", "fix", map[string]string{}, 44389, k, defs[k])
		for _, v := range vals {
			g.Add("sys/fix", "fix", map[string]string{"44389": v}, 44389, k, defs[k])
			g.Add("sys/fix", "fix", map[string]string{"44389": v}, 54337, k, defs[k])
		}
	}
	for n := 0; n < 40; n++ {
		k := kinds[rng.IntN(len(kinds))]
		g.Add("rand/fix", "fix", map[string]string{"1": vals[rng.IntN(len(vals))]}, 1, k, defs[k])
	}
}

func odGenKeys(g *vec.Gen, rng *rand.Rand) {
	ids := func(n int) []int64 {
		out := []int64{}
		for i := 0; i < n; i++ {
			out = append(out, int64(1+rng.IntN(5)))
		}
		return out
	}
	g.Add("sys/keys", "max_one_row", [][]int64{}, [][]int64{}, []int64{})
	g.Add("sys/keys", "max_one_row", [][]int64{{1}}, [][]int64{}, []int64{1})
	g.Add("sys/keys", "max_one_row", [][]int64{}, [][]int64{{1, 2}}, []int64{2, 1})
	for n := 0; n < 60; n++ {
		keys := [][]int64{}
		for i := rng.IntN(3); i > 0; i-- {
			keys = append(keys, ids(1+rng.IntN(2)))
		}
		uks := [][]int64{}
		for i := rng.IntN(3); i > 0; i-- {
			uks = append(uks, ids(1+rng.IntN(2)))
		}
		g.Add("rand/keys", "max_one_row", keys, uks, ids(rng.IntN(4)))
	}
	names := []string{"a", "b", "c", "d"}
	g.Add("sys/keys", "index_key", false, []string{"a"}, [][]any{{"a", true}})
	g.Add("sys/keys", "index_key", true, []string{"a"}, [][]any{{"a", true}})
	g.Add("sys/keys", "index_key", true, []string{"a", "b"}, [][]any{{"a", true}, {"b", false}})
	g.Add("sys/keys", "index_key", true, []string{"x"}, [][]any{{"a", true}})
	for n := 0; n < 60; n++ {
		idx := []string{}
		for i := 1 + rng.IntN(3); i > 0; i-- {
			idx = append(idx, names[rng.IntN(len(names))])
		}
		cols := [][]any{}
		for i := 1 + rng.IntN(4); i > 0; i-- {
			cols = append(cols, []any{names[rng.IntN(len(names))], rng.IntN(2) == 0})
		}
		g.Add("rand/keys", "index_key", rng.IntN(4) != 0, idx, cols)
	}
}
