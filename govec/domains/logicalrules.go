package domains

// Domain "logicalrules": the logical operators and the basic logical rewrite rules of the
// first vertical slice (tidb-rs VS1-23, specs/D09.14-logical-operators): column pruning and
// projection elimination (D09.7), predicate push down and predicate simplification (D09.8),
// TopN / Limit push down (D09.11), over DataSource, Selection, Projection, Sort, Limit, TopN
// and TableDual (D09.14).
//
// Each record's tables are built from CREATE TABLE statements (as domain "rangerbuild":
// ddl.BuildTableInfoFromAST, table IDs 100, 101, ...), the query is preprocessed and built
// into a logical plan with core.MockContext() (new collations, UTC, plan cache off, plan and
// column ids from 0), and the listed rules run in order on it, each through its
// Optimize method with an optimizer tracer on. Queries whose plan has another operator
// (aggregation, join, window, ...) are not recorded: those operators are not in the slice.
//
// Records:
//   - "rules": args [ddls, sql, rules] (rules from "column_prune", "projection_eliminate",
//     "predicate_push_down", "predicate_simplification", "topn_push_down") ->
//     {"in": plan (full), "plan_id": PlanID after building, "col_id": PlanColumnID after
//     building, "steps": [{"rule": name, "plan": plan (light), "trace": [[id, tp, action,
//     reason]]}]} or {"err": msg} when building fails.
//
// A full plan node is {"tp", "id", "qb", "schema": [tree], "children": [...]} plus, by
// operator: DataSource {"tbl": {"id", "name", "db", "pk_handle", "cols": [[id, name, offset,
// ft]]}, "cols": [column ids of ds.Columns], "handle": handle|null, "unmut": handle|null,
// "pushed": [tree], "all": [tree], "tbl_cols": [tree]}; Selection {"conds": [tree]};
// Projection {"exprs": [tree], "nodelay"}; Limit {"offset", "count"}; Sort {"by": [[tree,
// desc]]}; TopN {"by", "offset", "count"}; TableDual {"rows"}. A tree is domain
// "rangerbuild"'s with [pbCode, coercibility, repertoire] appended to a function node; a handle is {"int": tree} or {"common": [tree]}.
//
// A light plan node has the same keys with light expressions and without "tbl", "qb",
// "tbl_cols": a light expression is ["c", uid, id, index, ft, origName], ["r", uid, ft]
// (correlated column), ["k", StringWithCtx, ft] (constant) or ["f", name.L, signature type,
// ft, [args]]; ft is [tp, flag, flen, decimal, charset, collate, elems|null].

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/planner/util/optimizetrace"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/tracing"
)

const logicalrulesSeed = 20261117

var lrMu sync.Mutex

func init() {
	vec.Register(&vec.Domain{
		Name:     "logicalrules",
		Seed:     logicalrulesSeed,
		Coverage: logicalrulesCoverage,
		Blocks:   []string{"D09.7", "D09.8", "D09.11", "D09.14"},
		Exempt:   logicalrulesExempt(),
		Gzip:     true,
		Generate: logicalrulesGenerate,
		Exec:     logicalrulesExec,
	})
}

var logicalrulesCoverage = map[string]vec.Coverage{
	"rules": {Min: 1500, GoFuncs: []string{
		// D09.14: the operators.
		"NewBaseLogicalPlan", "BaseLogicalPlan.Children", "BaseLogicalPlan.SetChildren", "BaseLogicalPlan.SetChild",
		"BaseLogicalPlan.Schema", "BaseLogicalPlan.ChildLen", "BaseLogicalPlan.Self",
		"LogicalSchemaProducer.Schema", "LogicalSchemaProducer.SetSchema", "LogicalSchemaProducer.InlineProjection",
		"DataSource.Init", "DataSource.NewExtraHandleSchemaCol", "preferKeyColumnFromTable",
		"appendDataSourcePredicatePushDownTraceStep", "AddSelection", "pruneByItems",
		"LogicalSelection.Init", "LogicalSelection.ReplaceExprColumns", "splitSetGetVarFunc",
		"appendSelectionPredicatePushDownTraceStep", "AppendTableDualTraceStep",
		"LogicalProjection.Init", "LogicalProjection.ReplaceExprColumns", "breakDownPredicates",
		"LogicalTableDual.Init", "LogicalSort.Init", "LogicalSort.ReplaceExprColumns", "LogicalTopN.Init",
		"LogicalTopN.ReplaceExprColumns", "LogicalLimit.Init",
		// D09.7: column pruning, projection elimination.
		"ColumnPruner.Optimize", "noZeroColumnLayOut", "BaseLogicalPlan.PruneColumns", "DataSource.PruneColumns",
		"LogicalSelection.PruneColumns", "LogicalProjection.PruneColumns", "LogicalLimit.PruneColumns",
		"LogicalSort.PruneColumns", "LogicalTopN.PruneColumns", "LogicalTableDual.PruneColumns",
		"ProjectionEliminator.Optimize", "ProjectionEliminator.eliminate", "ReplaceColumnOfExpr",
		"pkg/planner/core/rule_eliminate_projection.go:canProjectionBeEliminatedLoose", "appendDupProjEliminateTraceStep",
		"appendProjEliminateTraceStep",
		// D09.8: predicate push down and simplification.
		"PPDSolver.Optimize", "BaseLogicalPlan.PredicatePushDown", "DataSource.PredicatePushDown",
		"LogicalSelection.PredicatePushDown", "LogicalProjection.PredicatePushDown", "LogicalLimit.PredicatePushDown",
		"LogicalTableDual.PredicatePushDown", "addPrefix4ShardIndexes",
		"PredicateSimplification.Optimize", "BaseLogicalPlan.PredicateSimplification", "DataSource.PredicateSimplification",
		"applyPredicateSimplification", "mergeInAndNotEQLists", "pruneEmptyORBranches", "splitCNF",
		"findPredicateType", "updateInPredicate", "updateOrPredicate", "unsatisfiable", "unsatisfiableExpression",
		"comparisonPred",
		// D09.11: TopN / Limit push down.
		"PushDownTopNOptimizer.Optimize", "BaseLogicalPlan.PushDownTopN", "pushDownTopNForBaseLogicalPlan",
		"LogicalLimit.PushDownTopN", "LogicalLimit.convertToTopN", "appendConvertTopNTraceStep",
		"LogicalProjection.PushDownTopN", "LogicalSort.PushDownTopN", "appendSortPassByItemsTraceStep",
		"LogicalTopN.AttachChild", "LogicalTopN.IsLimit", "appendTopNPushDownTraceStep",
	}},
}

// ---------------------------------------------------------------- rules

var lrRules = map[string]base.LogicalOptRule{
	"column_prune":             &core.ColumnPruner{},
	"projection_eliminate":     &core.ProjectionEliminator{},
	"predicate_push_down":      &core.PPDSolver{},
	"predicate_simplification": &core.PredicateSimplification{},
	"topn_push_down":           &core.PushDownTopNOptimizer{},
}

// lrSupported: the plan has only the operators of the slice.
func lrSupported(p base.LogicalPlan) string {
	switch p.(type) {
	case *logicalop.DataSource, *logicalop.LogicalSelection, *logicalop.LogicalProjection, *logicalop.LogicalLimit,
		*logicalop.LogicalSort, *logicalop.LogicalTopN, *logicalop.LogicalTableDual:
	default:
		return p.TP()
	}
	for _, c := range p.Children() {
		if s := lrSupported(c); s != "" {
			return s
		}
	}
	return ""
}

func lrBuild(sctx *mock.Context, ddls []string, sql string) (base.LogicalPlan, error) {
	is, err := rbIS(ddls)
	if err != nil {
		return nil, err
	}
	stmt, err := parser.New().ParseOneStmt(sql, "", "")
	if err != nil {
		return nil, err
	}
	nodeW := resolve.NewNodeW(stmt)
	ret := &core.PreprocessorReturn{InfoSchema: is}
	if err := core.Preprocess(context.Background(), sctx, nodeW, core.WithPreprocessorReturn(ret)); err != nil {
		return nil, err
	}
	p, err := core.BuildLogicalPlanForTest(context.Background(), sctx, nodeW, is)
	if err != nil {
		return nil, err
	}
	lp, ok := p.(base.LogicalPlan)
	if !ok {
		return nil, errors.New("not a logical plan")
	}
	return lp, nil
}

// ---------------------------------------------------------------- dumps

func lrHandle(h plannerutil.HandleCols, light func(expression.Expression) any) any {
	if h == nil {
		return nil
	}
	if h.IsInt() {
		return map[string]any{"int": light(h.GetCol(0))}
	}
	cols := []any{}
	for i := 0; i < h.NumCols(); i++ {
		cols = append(cols, light(h.GetCol(i)))
	}
	return map[string]any{"common": cols}
}

func lrLight(sctx *mock.Context) func(expression.Expression) any {
	ectx := sctx.GetExprCtx().GetEvalCtx()
	var f func(e expression.Expression) any
	f = func(e expression.Expression) any {
		switch x := e.(type) {
		case *expression.Column:
			return []any{"c", x.UniqueID, x.ID, x.Index, sfFtIn(x.RetType), vec.GoString(x.OrigName)}
		case *expression.CorrelatedColumn:
			return []any{"r", x.UniqueID, sfFtIn(x.RetType)}
		case *expression.Constant:
			return []any{"k", vec.GoString(x.StringWithCtx(ectx, errors.RedactLogDisable)), sfFtIn(x.RetType)}
		case *expression.ScalarFunction:
			args := []any{}
			for _, a := range x.GetArgs() {
				args = append(args, f(a))
			}
			return []any{"f", x.FuncName.L, reflect.TypeOf(x.Function).Elem().Name(), sfFtIn(x.RetType), args}
		}
		panic(fmt.Sprintf("lrLight %T", e))
	}
	return f
}

func lrExprs[T expression.Expression](es []T, f func(expression.Expression) any) []any {
	out := make([]any, 0, len(es))
	for _, e := range es {
		out = append(out, f(e))
	}
	return out
}

func lrBy(by []*plannerutil.ByItems, f func(expression.Expression) any) []any {
	out := make([]any, 0, len(by))
	for _, b := range by {
		out = append(out, []any{f(b.Expr), b.Desc})
	}
	return out
}

// lrFull is rbDump with the signature's pb code, coercibility and repertoire appended to a
// function node (the push down checks and collation derivation read them).
func lrFull(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		var ve any
		if x.VirtualExpr != nil {
			ve = lrFull(x.VirtualExpr)
		}
		return map[string]any{"col": []any{x.ID, x.UniqueID, x.Index, sfFtIn(x.RetType), vec.GoString(x.OrigName),
			x.IsHidden, ve}}
	case *expression.ScalarFunction:
		args := []any{}
		for _, a := range x.GetArgs() {
			args = append(args, lrFull(a))
		}
		chs, coll := x.CharsetAndCollation()
		return map[string]any{"f": []any{x.FuncName.L, x.FuncName.O, reflect.TypeOf(x.Function).Elem().Name(),
			sfFtIn(x.RetType), args, chs, coll, int32(x.Function.PbCode()), int(x.Coercibility()), int(x.Repertoire())}}
	}
	return rbDump(e)
}

// lrDump writes a plan node; full is the input form (trees), otherwise the light form.
func lrDump(sctx *mock.Context, p base.LogicalPlan, full bool) map[string]any {
	f := lrLight(sctx)
	if full {
		f = lrFull
	}
	o := map[string]any{"tp": p.TP(), "id": p.ID(), "schema": lrExprs(p.Schema().Columns, f)}
	if full {
		o["qb"] = p.QueryBlockOffset()
	}
	switch x := p.(type) {
	case *logicalop.DataSource:
		ids := []any{}
		for _, c := range x.Columns {
			ids = append(ids, c.ID)
		}
		o["cols"] = ids
		o["handle"] = lrHandle(x.HandleCols, f)
		o["unmut"] = lrHandle(x.UnMutableHandleCols, f)
		o["pushed"] = lrExprs(x.PushedDownConds, f)
		o["all"] = lrExprs(x.AllConds, f)
		if full {
			cols := []any{}
			for _, c := range x.TableInfo.Columns {
				cols = append(cols, []any{c.ID, c.Name.O, c.Offset, sfFtIn(&c.FieldType)})
			}
			o["tbl"] = map[string]any{"id": x.TableInfo.ID, "name": x.TableInfo.Name.O, "db": x.DBName.O,
				"pk_handle": x.TableInfo.PKIsHandle, "cols": cols}
			o["tbl_cols"] = lrExprs(x.TblCols, f)
		}
	case *logicalop.LogicalSelection:
		o["conds"] = lrExprs(x.Conditions, f)
	case *logicalop.LogicalProjection:
		o["exprs"] = lrExprs(x.Exprs, f)
		o["nodelay"] = x.CalculateNoDelay
	case *logicalop.LogicalLimit:
		o["offset"], o["count"] = x.Offset, x.Count
	case *logicalop.LogicalSort:
		o["by"] = lrBy(x.ByItems, f)
	case *logicalop.LogicalTopN:
		o["by"] = lrBy(x.ByItems, f)
		o["offset"], o["count"] = x.Offset, x.Count
	case *logicalop.LogicalTableDual:
		o["rows"] = x.RowCount
	default:
		panic("lrDump: " + p.TP())
	}
	children := []any{}
	for _, c := range p.Children() {
		children = append(children, lrDump(sctx, c, full))
	}
	o["children"] = children
	return o
}

// ---------------------------------------------------------------- exec

func logicalrulesExec(op string, p []json.RawMessage) (out any, err error) {
	lrMu.Lock()
	defer lrMu.Unlock()
	rbMu.Lock()
	defer rbMu.Unlock()
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "rules":
		sctx := rbSession(rbDefaultOpts)
		lp, err := lrBuild(sctx, arg[[]string](p, 0), arg[string](p, 1))
		if err != nil {
			return map[string]any{"err": vec.GoString(err.Error())}, nil
		}
		if s := lrSupported(lp); s != "" {
			return map[string]any{"unsupported": s}, nil
		}
		vars := sctx.GetSessionVars()
		res := map[string]any{"in": lrDump(sctx, lp, true), "plan_id": vars.PlanID.Load(),
			"col_id": vars.PlanColumnID.Load()}
		tracer := &tracing.LogicalOptimizeTracer{}
		opt := optimizetrace.DefaultLogicalOptimizeOption().WithEnableOptimizeTracer(tracer)
		steps := []any{}
		for i, name := range arg[[]string](p, 2) {
			rule := lrRules[name]
			opt.AppendBeforeRuleOptimize(i, name, func() *tracing.PlanTrace { return &tracing.PlanTrace{} })
			lp, _, err = rule.Optimize(context.Background(), lp, opt)
			if err != nil {
				steps = append(steps, map[string]any{"rule": name, "err": vec.GoString(err.Error())})
				break
			}
			trace := []any{}
			for _, s := range tracer.Steps[len(tracer.Steps)-1].Steps {
				trace = append(trace, []any{s.ID, s.TP, vec.GoString(s.Action), vec.GoString(s.Reason)})
			}
			steps = append(steps, map[string]any{"rule": name, "plan": lrDump(sctx, lp, false), "trace": trace})
		}
		res["steps"] = steps
		return res, nil
	}
	return nil, fmt.Errorf("logicalrules: unknown op %s", op)
}

// ---------------------------------------------------------------- generate

var lrAllRules = []string{"column_prune", "projection_eliminate", "predicate_push_down",
	"predicate_simplification", "topn_push_down", "column_prune"}

var lrDDLs = []string{
	"CREATE TABLE sbtest1 (id INT NOT NULL AUTO_INCREMENT, k INT NOT NULL DEFAULT '0', c CHAR(120) NOT NULL DEFAULT '', pad CHAR(60) NOT NULL DEFAULT '', PRIMARY KEY (id), KEY k_1 (k))",
	"CREATE TABLE t2 (a INT, b VARCHAR(20), c DOUBLE, d BIGINT UNSIGNED, e DECIMAL(10,2), KEY ia(a))",
	"CREATE TABLE t3 (x VARCHAR(10) COLLATE utf8mb4_general_ci, y INT NOT NULL, z DATETIME, PRIMARY KEY (x) NONCLUSTERED)",
}

var lrRejected int

func lrAdd(g *vec.Gen, src, sql string, rules []string) bool {
	g.Add(src, "rules", lrDDLs, sql, rules)
	out, _ := logicalrulesExec("rules", g.Calls[len(g.Calls)-1].Args)
	m := out.(map[string]any)
	_, e := m["err"]
	_, u := m["unsupported"]
	if !e && !u {
		lrRejected = 0
		return true
	}
	g.Calls = g.Calls[:len(g.Calls)-1]
	if lrRejected++; lrRejected > 200 {
		panic(fmt.Sprintf("logicalrules: every query rejected: %s %v", sql, m))
	}
	return false
}

// lrFixed: the read-only sysbench statements without aggregation, and hand-picked shapes.
var lrFixed = []string{
	"SELECT c FROM sbtest1 WHERE id=10",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100 ORDER BY c",
	"SELECT c FROM sbtest1 WHERE id IN (1, 5, 9)",
	"SELECT id, k FROM sbtest1 WHERE k > 5 AND k < 10 LIMIT 3",
	"SELECT * FROM sbtest1 ORDER BY k LIMIT 10 OFFSET 5",
	"SELECT * FROM sbtest1 ORDER BY k DESC, id LIMIT 1",
	"SELECT 1 FROM sbtest1",
	"SELECT 1 FROM t2",
	"SELECT 1 FROM t3",
	"SELECT * FROM sbtest1 WHERE 1 = 0",
	"SELECT * FROM sbtest1 WHERE id = 1 AND id = 2",
	"SELECT * FROM sbtest1 WHERE NULL",
	"SELECT * FROM sbtest1 WHERE 1 = 1 AND k = 3",
	"SELECT k FROM sbtest1 WHERE k != 3 AND k IN (1, 2, 3)",
	"SELECT k FROM sbtest1 WHERE k != 3 AND k IN (3, 4)",
	"SELECT k FROM sbtest1 WHERE (k = 1 OR k = 2) AND k = 1",
	"SELECT k FROM sbtest1 WHERE (k > 5 OR k < 2) AND k = 3",
	"SELECT c FROM (SELECT c, k FROM sbtest1 WHERE id > 3) s WHERE k = 2",
	"SELECT x FROM (SELECT k + 1 AS x FROM sbtest1) s WHERE x > 3",
	"SELECT x FROM (SELECT k + 1 AS x, c FROM sbtest1) s ORDER BY x LIMIT 4",
	"SELECT * FROM (SELECT * FROM sbtest1 ORDER BY k) s LIMIT 3",
	"SELECT * FROM (SELECT id, c FROM sbtest1 LIMIT 10) s WHERE id > 2",
	"SELECT a FROM t2 ORDER BY b LIMIT 2",
	"SELECT a, b FROM t2 WHERE a = 1 AND b = 'x' ORDER BY c LIMIT 5",
	"SELECT b FROM t2 ORDER BY 1 LIMIT 3",
	"SELECT a FROM t2 ORDER BY a, a, 1 LIMIT 3",
	"SELECT a FROM t2 ORDER BY rand() LIMIT 3",
	"SELECT y FROM t3 WHERE x = 'A' ORDER BY z",
	"SELECT 1",
	"SELECT 1 FROM dual WHERE 0",
	"SELECT 1 LIMIT 0",
	"SELECT a FROM (SELECT a, b FROM t2) s, (SELECT 1) d",
}

func logicalrulesGenerate(g *vec.Gen, rng *rand.Rand) {
	for _, sql := range lrFixed {
		lrAdd(g, "sys/fixed", sql, lrAllRules)
		for _, r := range lrAllRules[:5] {
			lrAdd(g, "sys/fixed", sql, []string{r})
		}
	}
	for n := 0; n < 1500; {
		sql := lrRandQuery(rng)
		rules := lrAllRules
		if rng.IntN(3) == 0 {
			rules = []string{lrAllRules[rng.IntN(5)]}
		}
		if lrAdd(g, "rand/query", sql, rules) {
			n++
		}
	}
}

type lrTable struct {
	name string
	cols []string
	ints []string
	strs []string
}

var lrTables = []lrTable{
	{"sbtest1", []string{"id", "k", "c", "pad"}, []string{"id", "k"}, []string{"c", "pad"}},
	{"t2", []string{"a", "b", "c", "d", "e"}, []string{"a", "d"}, []string{"b"}},
	{"t3", []string{"x", "y", "z"}, []string{"y"}, []string{"x"}},
}

func lrPick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

func lrAtom(rng *rand.Rand, t lrTable) string {
	switch rng.IntN(10) {
	case 0, 1, 2:
		return fmt.Sprintf("%s %s %d", lrPick(rng, t.ints), lrPick(rng, []string{"=", "!=", "<", ">", "<=", ">="}), rng.IntN(20))
	case 3:
		return fmt.Sprintf("%s BETWEEN %d AND %d", lrPick(rng, t.ints), rng.IntN(10), 5+rng.IntN(20))
	case 4:
		return fmt.Sprintf("%s IN (%d, %d, %d)", lrPick(rng, t.ints), rng.IntN(5), rng.IntN(5), rng.IntN(5))
	case 5:
		c := lrPick(rng, t.ints)
		return fmt.Sprintf("(%s = %d OR %s = %d)", c, rng.IntN(5), c, rng.IntN(5))
	case 6:
		return fmt.Sprintf("%s = '%c'", lrPick(rng, t.strs), 'a'+rune(rng.IntN(3)))
	case 7:
		return fmt.Sprintf("%s IS NULL", lrPick(rng, t.cols))
	case 8:
		return lrPick(rng, []string{"1 = 1", "1 = 0", "NULL", "TRUE"})
	}
	return fmt.Sprintf("%s + 1 > %d", lrPick(rng, t.ints), rng.IntN(10))
}

func lrCond(rng *rand.Rand, t lrTable) string {
	n := 1 + rng.IntN(3)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = lrAtom(rng, t)
	}
	return strings.Join(parts, " AND ")
}

func lrFields(rng *rand.Rand, t lrTable) []string {
	if rng.IntN(5) == 0 {
		return []string{"*"}
	}
	n := 1 + rng.IntN(3)
	fs := []string{}
	for i := 0; i < n; i++ {
		switch rng.IntN(6) {
		case 0:
			fs = append(fs, fmt.Sprintf("%s + %d AS f%d", lrPick(rng, t.ints), rng.IntN(5), i))
		case 1:
			fs = append(fs, fmt.Sprintf("%d AS f%d", rng.IntN(5), i))
		default:
			fs = append(fs, lrPick(rng, t.cols))
		}
	}
	return fs
}

func lrTail(rng *rand.Rand, cols []string) string {
	s := ""
	if rng.IntN(2) == 0 {
		s += " ORDER BY " + lrPick(rng, cols)
		if rng.IntN(2) == 0 {
			s += " DESC"
		}
		if rng.IntN(3) == 0 {
			s += ", " + lrPick(rng, cols)
		}
	}
	if rng.IntN(2) == 0 {
		s += fmt.Sprintf(" LIMIT %d", rng.IntN(10))
		if rng.IntN(3) == 0 {
			s += fmt.Sprintf(" OFFSET %d", rng.IntN(5))
		}
	}
	return s
}

func lrRandQuery(rng *rand.Rand) string {
	t := lrPick(rng, lrTables)
	fields := lrFields(rng, t)
	q := "SELECT " + strings.Join(fields, ", ") + " FROM " + t.name
	if rng.IntN(4) != 0 {
		q += " WHERE " + lrCond(rng, t)
	}
	q += lrTail(rng, t.cols)
	if rng.IntN(3) != 0 {
		return q
	}
	// Wrap it: an outer query over the derived table with all the table's columns.
	inner := "SELECT " + strings.Join(t.cols, ", ") + " FROM " + t.name
	if rng.IntN(2) == 0 {
		inner += " WHERE " + lrCond(rng, t)
	}
	inner += lrTail(rng, t.cols)
	o := lrTable{"s", t.cols, t.ints, t.strs}
	q = "SELECT " + strings.Join(lrFields(rng, o), ", ") + " FROM (" + inner + ") s"
	if rng.IntN(2) == 0 {
		q += " WHERE " + lrCond(rng, o)
	}
	return q + lrTail(rng, t.cols)
}
