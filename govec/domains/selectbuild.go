package domains

// Domain "selectbuild": the SELECT builder of the first vertical slice (tidb-rs VS1-21, specs
// D09.1-select-builder): buildSelect and the parts a single-table query block needs — the
// data source with its access paths, the WHERE / HAVING selection, the projection with
// wildcard expansion and output names, ORDER BY with resolveHavingAndOrderBy, LIMIT
// (D09.1, D09.4, the access paths of D10.2, the planbuilder helpers of D09.0).
//
// The tables are built from CREATE TABLE statements (as domain "rangerbuild":
// ddl.BuildTableInfoFromAST, table IDs 100, 101, ..., database "test"). Each query is parsed,
// preprocessed and built with core.MockContext() (new collations, UTC, plan and column ids
// from 0) by NewPlanBuilder().Init(...).Build — what BuildLogicalPlanForTest does — so that
// the builder's visit infos and optimization flags can be read as well.
//
// Records:
//   - "tables": args [ddls] -> [TableInfo JSON] (the tables of the InfoSchema, in DDL order;
//     tidb-rs builds its InfoSchema lookup from them).
//   - "build": args [ddls, sql, opts] (opts {"mode": SQL mode, "keep_order": !tidb_remove_
//     orderby_in_subquery}) -> {"plan": node, "plan_id", "col_id", "opt_flag", "vi": [[priv,
//     db, table, column, err message, err code]], "w": [warnings]}, {"err": message, "code"}
//     when preprocessing or building fails, or {"unsupported": tp} when the plan has an
//     operator outside the slice (aggregation, join, window, ...; tidb-rs stops at a boundary
//     there).
//
// A plan node is {"tp", "id", "qb", "schema": [expr], "names": [[db, table, column,
// orig table, orig column, hidden, not explicit usable, redundant]], "children": [...]} plus,
// by operator: DataSource {"tbl": [id, name], "db", "as", "phys", "cols": [column ids],
// "handle", "unmut", "tbl_cols": [expr], "pushed", "all", "common": [unique ids],
// "common_lens", "paths": [path], "all_paths": [path], "fu", "pst", "uk"}; Selection
// {"conds"}; Projection {"exprs", "nodelay"}; Limit {"offset", "count", "cop"}; Sort {"by":
// [[expr, desc]]}; TableDual {"rows"}. An expr is ["c", uid, id, index, ft, origName,
// hidden], ["r", uid, ft], ["k", StringWithCtx, ft] or ["f", name.L, signature type, ft,
// [args], charset, collation, coercibility, repertoire]; a handle is {"int": expr} or
// {"common": [expr]}; a path is [index name|"", store type, int handle, common handle, [full
// index column unique ids, -1 for none], full index column lengths, uk shard, forced, index
// lookup push down].

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	utilhint "github.com/pingcap/tidb/pkg/util/hint"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const selectbuildSeed = 20261121

var sbMu sync.Mutex

func init() {
	vec.Register(&vec.Domain{
		Name:     "selectbuild",
		Seed:     selectbuildSeed,
		Coverage: selectbuildCoverage,
		Blocks:   []string{"D09.1", "D09.4", "D10.2", "D09.0", "D09.2"},
		Exempt:   selectbuildExempt(),
		Gzip:     true,
		Generate: selectbuildGenerate,
		Exec:     selectbuildExec,
	})
}

var selectbuildCoverage = map[string]vec.Coverage{
	"tables": {Min: 1},
	"build": {Min: 1500, GoFuncs: []string{
		// D09.1: the SELECT builder.
		"PlanBuilder.buildSelect", "PlanBuilder.buildTableRefs", "PlanBuilder.buildResultSetNode",
		"PlanBuilder.buildDataSource", "setPreferredStoreType", "PlanBuilder.buildTableDual",
		"PlanBuilder.buildSelection", "PlanBuilder.buildProjection", "PlanBuilder.buildProjectionField",
		"PlanBuilder.buildProjectionFieldNameFromColumns", "PlanBuilder.buildProjectionFieldNameFromExpressions",
		"getInnerFromParenthesesAndUnaryPlus", "PlanBuilder.preprocessUserVarTypes", "userVarTypeProcessor.Enter",
		"userVarTypeProcessor.Leave", "PlanBuilder.buildSort", "PlanBuilder.buildSortWithCheck",
		"itemTransformer.Enter", "itemTransformer.Leave", "PlanBuilder.buildLimit", "PlanBuilder.unfoldWildStar",
		"unfoldWildStar", "resolveFromSelectFields", "appendVisitInfo", "extractLimitCountOffset", "getUintFromNode",
		// D09.4: HAVING / ORDER BY resolution and the ONLY_FULL_GROUP_BY walk without GROUP BY.
		"PlanBuilder.resolveHavingAndOrderBy", "havingWindowAndOrderbyExprResolver.Enter",
		"havingWindowAndOrderbyExprResolver.Leave", "havingWindowAndOrderbyExprResolver.resolveFromPlan",
		"havingWindowAndOrderbyExprResolver.pushCurClause", "havingWindowAndOrderbyExprResolver.popCurClause",
		"PlanBuilder.checkOnlyFullGroupBy", "PlanBuilder.checkOnlyFullGroupByWithOutGroupClause",
		"colResolverForOnlyFullGroupBy.Enter", "colResolverForOnlyFullGroupBy.Leave",
		// D10.2: the access paths and the handle column helper.
		"getPossibleAccessPaths", "fillContentForTablePath", "genTiFlashPath", "filterPathByIsolationRead",
		"removeIgnoredPaths", "removeGlobalIndexPaths", "checkAutoForceIndexLookUpPushDown",
		"handleColHelper.pushMap", "handleColHelper.popMap",
		// D09.0: the planbuilder helpers.
		"PlanBuilder.Build", "PlanBuilder.detectSelectWindow", "isForUpdateReadSelectLock",
		// D09.2: ORDER BY positions.
		"expressionRewriter.positionToScalarFunc",
	}},
}

type sbOpts struct {
	Mode      mysql.SQLMode `json:"mode"`
	KeepOrder bool          `json:"keep_order"`
}

// ---------------------------------------------------------------- dumps

func sbExprF(sctx *mock.Context) func(expression.Expression) any {
	ectx := sctx.GetExprCtx().GetEvalCtx()
	var f func(e expression.Expression) any
	f = func(e expression.Expression) any {
		switch x := e.(type) {
		case *expression.Column:
			return []any{"c", x.UniqueID, x.ID, x.Index, sfFtIn(x.RetType), vec.GoString(x.OrigName), x.IsHidden}
		case *expression.CorrelatedColumn:
			return []any{"r", x.UniqueID, sfFtIn(x.RetType)}
		case *expression.Constant:
			return []any{"k", vec.GoString(x.StringWithCtx(ectx, errors.RedactLogDisable)), sfFtIn(x.RetType)}
		case *expression.ScalarFunction:
			args := []any{}
			for _, a := range x.GetArgs() {
				args = append(args, f(a))
			}
			chs, coll := x.CharsetAndCollation()
			return []any{"f", x.FuncName.L, reflect.TypeOf(x.Function).Elem().Name(), sfFtIn(x.RetType), args, chs, coll,
				int(x.Coercibility()), int(x.Repertoire())}
		}
		panic(fmt.Sprintf("sbExpr %T", e))
	}
	return f
}

func sbNames(names types.NameSlice) []any {
	out := []any{}
	for _, n := range names {
		out = append(out, []any{vec.GoString(n.DBName.O), vec.GoString(n.TblName.O), vec.GoString(n.ColName.O),
			vec.GoString(n.OrigTblName.O), vec.GoString(n.OrigColName.O), n.Hidden, n.NotExplicitUsable, n.Redundant})
	}
	return out
}

func sbPaths(paths []*plannerutil.AccessPath) []any {
	out := []any{}
	for _, p := range paths {
		name := ""
		if p.Index != nil {
			name = p.Index.Name.O
		}
		cols := []any{}
		for _, c := range p.FullIdxCols {
			if c == nil {
				cols = append(cols, -1)
			} else {
				cols = append(cols, c.UniqueID)
			}
		}
		lens := []any{}
		for _, l := range p.FullIdxColLens {
			lens = append(lens, l)
		}
		out = append(out, []any{name, int(p.StoreType), p.IsIntHandlePath, p.IsCommonHandlePath, cols, lens,
			p.IsUkShardIndexPath, p.Forced, int(p.IndexLookUpPushDownBy)})
	}
	return out
}

func sbDump(sctx *mock.Context, p base.LogicalPlan) map[string]any {
	f := sbExprF(sctx)
	o := map[string]any{"tp": p.TP(), "id": p.ID(), "qb": p.QueryBlockOffset(),
		"schema": lrExprs(p.Schema().Columns, f), "names": sbNames(p.OutputNames())}
	switch x := p.(type) {
	case *logicalop.DataSource:
		ids := []any{}
		for _, c := range x.Columns {
			ids = append(ids, c.ID)
		}
		o["tbl"] = []any{x.TableInfo.ID, x.TableInfo.Name.O}
		o["db"] = x.DBName.O
		var as any
		if x.TableAsName != nil {
			as = x.TableAsName.O
		}
		o["as"] = as
		o["phys"] = x.PhysicalTableID
		o["cols"] = ids
		o["handle"] = lrHandle(x.HandleCols, f)
		o["unmut"] = lrHandle(x.UnMutableHandleCols, f)
		o["tbl_cols"] = lrExprs(x.TblCols, f)
		o["pushed"] = lrExprs(x.PushedDownConds, f)
		o["all"] = lrExprs(x.AllConds, f)
		common := []any{}
		for _, c := range x.CommonHandleCols {
			common = append(common, c.UniqueID)
		}
		o["common"] = common
		lens := []any{}
		for _, l := range x.CommonHandleLens {
			lens = append(lens, l)
		}
		o["common_lens"] = lens
		o["paths"] = sbPaths(x.PossibleAccessPaths)
		o["all_paths"] = sbPaths(x.AllPossibleAccessPaths)
		o["fu"] = x.IsForUpdateRead
		o["pst"] = x.PreferStoreType
		o["uk"] = x.ContainExprPrefixUk
	case *logicalop.LogicalSelection:
		o["conds"] = lrExprs(x.Conditions, f)
	case *logicalop.LogicalProjection:
		o["exprs"] = lrExprs(x.Exprs, f)
		o["nodelay"] = x.CalculateNoDelay
	case *logicalop.LogicalLimit:
		o["offset"], o["count"], o["cop"] = x.Offset, x.Count, x.PreferLimitToCop
	case *logicalop.LogicalSort:
		o["by"] = lrBy(x.ByItems, f)
	case *logicalop.LogicalTableDual:
		o["rows"] = x.RowCount
	case *logicalop.LogicalTopN:
		// Only domain "optdriver" has TopN (the logical optimization makes it).
		o["by"] = lrBy(x.ByItems, f)
		o["offset"], o["count"] = x.Offset, x.Count
	default:
		panic("sbDump: " + p.TP())
	}
	children := []any{}
	for _, c := range p.Children() {
		children = append(children, sbDump(sctx, c))
	}
	o["children"] = children
	return o
}

// sbSupported: the plan has only the operators the SELECT builder of the slice builds.
func sbSupported(p base.LogicalPlan) string {
	switch p.(type) {
	case *logicalop.DataSource, *logicalop.LogicalSelection, *logicalop.LogicalProjection, *logicalop.LogicalLimit,
		*logicalop.LogicalSort, *logicalop.LogicalTableDual:
	default:
		return p.TP()
	}
	for _, c := range p.Children() {
		if s := sbSupported(c); s != "" {
			return s
		}
	}
	return ""
}

func sbErr(err error) map[string]any {
	code := 0
	if te, ok := errors.Cause(err).(*terror.Error); ok {
		code = int(te.Code())
	}
	return map[string]any{"err": vec.GoString(err.Error()), "code": code}
}

// ---------------------------------------------------------------- exec

func selectbuildExec(op string, p []json.RawMessage) (out any, err error) {
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
	switch op {
	case "tables":
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
	case "build":
		sctx := rbSession(rbDefaultOpts)
		o := arg[sbOpts](p, 2)
		vars := sctx.GetSessionVars()
		vars.SQLMode = o.Mode
		vars.RemoveOrderbyInSubquery = !o.KeepOrder
		stmt, err := parser.New().ParseOneStmt(arg[string](p, 1), "", "")
		if err != nil {
			return sbErr(err), nil
		}
		nodeW := resolve.NewNodeW(stmt)
		ret := &core.PreprocessorReturn{InfoSchema: is}
		if err := core.Preprocess(context.Background(), sctx, nodeW, core.WithPreprocessorReturn(ret)); err != nil {
			return sbErr(err), nil
		}
		builder, _ := core.NewPlanBuilder().Init(sctx.GetPlanCtx(), is, utilhint.NewQBHintHandler(nil))
		plan, err := builder.Build(context.Background(), nodeW)
		if err != nil {
			return sbErr(err), nil
		}
		lp, ok := plan.(base.LogicalPlan)
		if !ok {
			return map[string]any{"unsupported": fmt.Sprintf("%T", plan)}, nil
		}
		if s := sbSupported(lp); s != "" {
			return map[string]any{"unsupported": s}, nil
		}
		vi := builder.GetVisitInfo()
		rv := *(*[]rwVisitInfo)(unsafe.Pointer(&vi))
		vis := []any{}
		for _, v := range rv {
			msg, code := "", 0
			if v.err != nil {
				msg = v.err.Error()
				if te, ok := errors.Cause(v.err).(*terror.Error); ok {
					code = int(te.Code())
				}
			}
			vis = append(vis, []any{uint64(v.privilege), v.db, v.table, v.column, vec.GoString(msg), code})
		}
		warns := []any{}
		for _, w := range vars.StmtCtx.GetWarnings() {
			warns = append(warns, vec.GoString(w.Err.Error()))
		}
		return map[string]any{"plan": sbDump(sctx, lp), "plan_id": vars.PlanID.Load(),
			"col_id": vars.PlanColumnID.Load(), "opt_flag": builder.GetOptFlag(), "vi": vis, "w": warns}, nil
	}
	return nil, fmt.Errorf("selectbuild: unknown op %s", op)
}

// sbTableNames: the table name of each CREATE TABLE statement.
func sbTableNames(ddls []string) []string {
	names := []string{}
	for _, d := range ddls {
		f := strings.Fields(d)
		names = append(names, f[2])
	}
	return names
}

// ---------------------------------------------------------------- generate

var sbDDLs = []string{
	"CREATE TABLE sbtest1 (id INT NOT NULL AUTO_INCREMENT, k INT NOT NULL DEFAULT '0', c CHAR(120) NOT NULL DEFAULT '', pad CHAR(60) NOT NULL DEFAULT '', PRIMARY KEY (id), KEY k_1 (k))",
	"CREATE TABLE t2 (a INT, b VARCHAR(20), c DOUBLE, d BIGINT UNSIGNED, e DECIMAL(10,2), KEY ia(a), UNIQUE KEY ubc(b, c))",
	"CREATE TABLE t3 (x VARCHAR(10) COLLATE utf8mb4_general_ci, y INT NOT NULL, z DATETIME, PRIMARY KEY (x) NONCLUSTERED)",
	"CREATE TABLE tc (a INT, b VARCHAR(10), c INT, PRIMARY KEY (b, a) CLUSTERED, UNIQUE KEY uc (c), KEY ip (b(3)))",
	"CREATE TABLE ti (a INT, b INT, c ENUM('x', 'y'), KEY ib (b) INVISIBLE, KEY ic (c))",
	"CREATE TABLE tp (a INT, b INT, KEY ia (a)) PARTITION BY HASH (a) PARTITIONS 4",
	"CREATE TABLE tg (a INT, b INT AS (a + 1) VIRTUAL, KEY ib (b))",
}

func sbModes(rng *rand.Rand) sbOpts {
	o := sbOpts{}
	if rng == nil || rng.IntN(2) == 0 {
		o.Mode = mysql.ModeOnlyFullGroupBy
	}
	if rng != nil && rng.IntN(4) == 0 {
		o.KeepOrder = true
	}
	return o
}

func sbAdd(g *vec.Gen, src, sql string, o sbOpts) {
	g.Add(src, "build", sbDDLs, sql, o)
}

// sbFixed: hand-picked shapes (the sysbench reads without aggregation first), errors and
// the boundaries of the slice.
var sbFixed = []string{
	"SELECT c FROM sbtest1 WHERE id=10",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100",
	"SELECT c FROM sbtest1 WHERE id BETWEEN 1 AND 100 ORDER BY c",
	"SELECT id, k FROM sbtest1 WHERE k > 5 AND k < 10 LIMIT 3",
	"SELECT * FROM sbtest1",
	"SELECT * FROM sbtest1 ORDER BY k LIMIT 10 OFFSET 5",
	"SELECT * FROM sbtest1 ORDER BY k DESC, id LIMIT 1",
	"SELECT sbtest1.* FROM sbtest1",
	"SELECT test.sbtest1.* FROM sbtest1",
	"SELECT s.* FROM sbtest1 s",
	"SELECT *, k FROM sbtest1",
	"SELECT k, * FROM sbtest1",
	"SELECT x.* FROM sbtest1",
	"SELECT 1",
	"SELECT 1 FROM dual",
	"SELECT 1, 'abc', NULL, TRUE, FALSE, 0x1F, b'101', 1.5, -2, 'a' 'b' FROM t2",
	"SELECT  ( 1 + 2 )  , a+1, a + 1 AS x, (a), +a, ((b)) FROM t2",
	"SELECT /*!40001 a */ FROM t2",
	"SELECT a /* c */ + 1 FROM t2",
	"SELECT '  abc' FROM t2",
	"SELECT * FROM t2",
	"SELECT * FROM t3",
	"SELECT * FROM tc",
	"SELECT * FROM ti",
	"SELECT c FROM tc WHERE b = 'x' AND a = 1",
	"SELECT a FROM t2 WHERE 1",
	"SELECT a FROM t2 WHERE 0",
	"SELECT a FROM t2 WHERE NULL",
	"SELECT a FROM t2 WHERE 1 = 1 AND a = 2",
	"SELECT a FROM t2 WHERE 'abc'",
	"SELECT a FROM t2 WHERE b",
	"SELECT a FROM t2 WHERE b AND a",
	"SELECT a FROM t2 WHERE (a > 1 AND b = 'x') AND (c < 2)",
	"SELECT a FROM t2 WHERE a > 1 OR b = 'x'",
	"SELECT a FROM t2 HAVING a > 1",
	"SELECT a FROM t2 HAVING t2.a > 1",
	"SELECT a + 1 AS x FROM t2 HAVING x > 1",
	"SELECT a + 1 FROM t2 HAVING t2.a > 1",
	"SELECT a FROM t2 HAVING b > 1",
	"SELECT a FROM t2 WHERE b = 'x' HAVING a > 1 ORDER BY a LIMIT 2",
	"SELECT a FROM t2 ORDER BY b",
	"SELECT a FROM t2 ORDER BY t2.b, c DESC",
	"SELECT a AS x FROM t2 ORDER BY x",
	"SELECT a AS x FROM t2 ORDER BY x + 1",
	"SELECT a FROM t2 ORDER BY a + b",
	"SELECT a, b FROM t2 ORDER BY 2, 1",
	"SELECT a FROM t2 ORDER BY 1 DESC LIMIT 3",
	"SELECT a FROM t2 ORDER BY 3",
	"SELECT a, b AS a FROM t2 ORDER BY a",
	"SELECT a, a FROM t2 ORDER BY a",
	"SELECT t2.a, a FROM t2 ORDER BY a",
	"SELECT a FROM t2 ORDER BY unknown",
	"SELECT unknown FROM t2",
	"SELECT a FROM t2 WHERE unknown = 1",
	"SELECT a FROM t2 HAVING unknown = 1",
	"SELECT a FROM t2 ORDER BY rand() LIMIT 3",
	"SELECT a FROM t2 LIMIT 0",
	"SELECT a FROM t2 LIMIT 0, 0",
	"SELECT a FROM t2 LIMIT 18446744073709551615 OFFSET 5",
	"SELECT a FROM t2 LIMIT 5, 18446744073709551615",
	"SELECT a FROM t2 ORDER BY b LIMIT 0",
	"SELECT x, y FROM t3 WHERE x = 'A' ORDER BY z",
	"SELECT z FROM t3 WHERE z > 0",
	"SELECT z FROM t3 HAVING z > 0",
	"SELECT z AS w FROM t3 HAVING w > 0",
	"SELECT y FROM t3 HAVING z > 0",
	"SELECT * FROM (SELECT c, k FROM sbtest1 WHERE id > 3) s WHERE k = 2",
	"SELECT x FROM (SELECT k + 1 AS x FROM sbtest1) s WHERE x > 3",
	"SELECT * FROM (SELECT * FROM sbtest1 ORDER BY k) s LIMIT 3",
	"SELECT * FROM (SELECT * FROM sbtest1 ORDER BY k LIMIT 2) s ORDER BY s.id",
	"SELECT s.id FROM (SELECT id, c FROM sbtest1 ORDER BY c LIMIT 10) s ORDER BY sbtest1.id",
	"SELECT s.id FROM (SELECT id, c FROM sbtest1 ORDER BY c LIMIT 10) s ORDER BY s.c",
	"SELECT * FROM (SELECT 1, 1) s",
	"SELECT * FROM (SELECT 1 AS a, 2 AS a) s",
	"SELECT * FROM (SELECT 1 AS a, 2 AS b) AS s",
	"SELECT s.a FROM (SELECT a, b FROM t2) s ORDER BY b",
	"SELECT a FROM (SELECT a FROM t2 ORDER BY b) s",
	"SELECT a FROM (SELECT a, b FROM t2 HAVING b > 0) s",
	"SELECT t.a FROM (SELECT a FROM t2) t",
	"SELECT * FROM t2 t WHERE t.a = 1",
	"SELECT t2.a FROM t2 t",
	"SELECT test.t2.a FROM t2",
	"SELECT a FROM test.t2",
	"SELECT a FROM nosuch",
	"SELECT a FROM t2 FOR UPDATE",
	"SELECT a FROM t2 LOCK IN SHARE MODE",
	"SELECT * FROM tp",
	"SELECT a FROM tp WHERE a = 1",
	"SELECT * FROM tg",
	"SELECT a FROM t2 PARTITION (p0)",
	"SELECT a FROM t2 USE INDEX (ia)",
	"SELECT /*+ USE_INDEX(t2, ia) */ a FROM t2",
	"SELECT COUNT(*) FROM t2",
	"SELECT SUM(a) FROM t2",
	"SELECT a FROM t2 GROUP BY a",
	"SELECT DISTINCT a FROM t2",
	"SELECT a FROM t2 ORDER BY SUM(b)",
	"SELECT a FROM t2 HAVING SUM(b) > 0",
	"SELECT ANY_VALUE(a) FROM t2",
	"SELECT a, ROW_NUMBER() OVER () FROM t2",
	"SELECT a FROM t2 WINDOW w AS (ORDER BY a)",
	"SELECT a FROM t2 HAVING ROW_NUMBER() OVER () > 1",
	"SELECT * FROM t2 JOIN t3",
	"SELECT * FROM t2, t3",
	"SELECT a FROM t2 WHERE a IN (SELECT y FROM t3)",
	"WITH c AS (SELECT 1) SELECT * FROM c",
	"SELECT a FROM t2 UNION SELECT y FROM t3",
	"SELECT @x := 1 FROM t2",
	"SELECT @@sql_mode FROM t2",
	"SELECT NAME_CONST('n', 1) FROM t2",
	"SELECT * FROM t2 WHERE e > 1.5 AND d < 10",
	"SELECT c, COALESCE(c, 1) FROM t2",
	"SELECT a FROM t2 ORDER BY b COLLATE utf8mb4_bin",
	"SELECT x FROM t3 ORDER BY x LIMIT 1",
	"SELECT b, c FROM ti WHERE c = 'x'",
	"SELECT 1 FROM sbtest1",
	"SELECT 1 FROM t2",
	"SELECT 1 FROM t3",
	"SELECT 1 FROM dual WHERE 0",
	"SELECT 1 LIMIT 0",
	"SELECT a FROM (SELECT a, b FROM t2) s, (SELECT 1) d",
}

func selectbuildGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/tables", "tables", sbDDLs)
	for _, sql := range sbFixed {
		sbAdd(g, "sys/fixed", sql, sbOpts{Mode: mysql.ModeOnlyFullGroupBy})
		sbAdd(g, "sys/fixed", sql, sbOpts{KeepOrder: true})
	}
	for n := 0; n < 1500; n++ {
		sbAdd(g, "rand/query", sbRandQuery(rng), sbModes(rng))
	}
}

var sbTables = []lrTable{
	{"sbtest1", []string{"id", "k", "c", "pad"}, []string{"id", "k"}, []string{"c", "pad"}},
	{"t2", []string{"a", "b", "c", "d", "e"}, []string{"a", "d"}, []string{"b"}},
	{"t3", []string{"x", "y", "z"}, []string{"y"}, []string{"x"}},
	{"tc", []string{"a", "b", "c"}, []string{"a", "c"}, []string{"b"}},
	{"ti", []string{"a", "b", "c"}, []string{"a", "b"}, []string{"c"}},
}

func sbFields(rng *rand.Rand, t lrTable, alias string) []string {
	switch rng.IntN(8) {
	case 0:
		return []string{"*"}
	case 1:
		return []string{alias + ".*"}
	}
	n := 1 + rng.IntN(4)
	fs := []string{}
	for i := 0; i < n; i++ {
		switch rng.IntN(9) {
		case 0:
			fs = append(fs, fmt.Sprintf("%s + %d AS f%d", lrPick(rng, t.ints), rng.IntN(5), i))
		case 1:
			fs = append(fs, fmt.Sprintf("%d", rng.IntN(5)))
		case 2:
			fs = append(fs, fmt.Sprintf("%s.%s", alias, lrPick(rng, t.cols)))
		case 3:
			fs = append(fs, fmt.Sprintf("%s * 2", lrPick(rng, t.ints)))
		case 4:
			fs = append(fs, fmt.Sprintf("%s AS %s", lrPick(rng, t.cols), lrPick(rng, t.cols)))
		case 5:
			fs = append(fs, fmt.Sprintf("'s%d'", rng.IntN(3)))
		default:
			fs = append(fs, lrPick(rng, t.cols))
		}
	}
	return fs
}

func sbTail(rng *rand.Rand, cols []string, fields int) string {
	s := ""
	if rng.IntN(8) == 0 {
		s += fmt.Sprintf(" HAVING %s > %d", lrPick(rng, cols), rng.IntN(5))
	}
	if rng.IntN(2) == 0 {
		switch rng.IntN(4) {
		case 0:
			s += fmt.Sprintf(" ORDER BY %d", 1+rng.IntN(fields+1))
		case 1:
			s += fmt.Sprintf(" ORDER BY %s + 1", lrPick(rng, cols))
		default:
			s += " ORDER BY " + lrPick(rng, cols)
		}
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

// sbCond: lrCond with fewer IN lists (the IN function class is not ported in tidb-rs yet,
// D08.9: a record with one stops at that boundary and covers less of the builder).
func sbCond(rng *rand.Rand, t lrTable) string {
	n := 1 + rng.IntN(3)
	parts := make([]string, n)
	for i := range parts {
		a := lrAtom(rng, t)
		for strings.Contains(a, " IN (") && rng.IntN(5) != 0 {
			a = lrAtom(rng, t)
		}
		parts[i] = a
	}
	return strings.Join(parts, " AND ")
}

func sbRandQuery(rng *rand.Rand) string {
	t := lrPick(rng, sbTables)
	alias := t.name
	from := t.name
	if rng.IntN(4) == 0 {
		alias = "q"
		from = t.name + " q"
	}
	fields := sbFields(rng, t, alias)
	q := "SELECT " + strings.Join(fields, ", ") + " FROM " + from
	if rng.IntN(3) != 0 {
		q += " WHERE " + sbCond(rng, t)
	}
	q += sbTail(rng, t.cols, len(fields))
	if rng.IntN(4) != 0 {
		return q
	}
	// Wrap it: an outer query over the derived table with all the table's columns.
	inner := "SELECT " + strings.Join(t.cols, ", ") + " FROM " + t.name
	if rng.IntN(2) == 0 {
		inner += " WHERE " + sbCond(rng, t)
	}
	inner += sbTail(rng, t.cols, len(t.cols))
	o := lrTable{"s", t.cols, t.ints, t.strs}
	fields = sbFields(rng, o, "s")
	q = "SELECT " + strings.Join(fields, ", ") + " FROM (" + inner + ") s"
	if rng.IntN(2) == 0 {
		q += " WHERE " + sbCond(rng, o)
	}
	return q + sbTail(rng, t.cols, len(fields))
}
