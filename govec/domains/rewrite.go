package domains

// Domain "rewrite": block D09.2 part 1 (tidb-rs VS1-22, specs/D09.2-rewriter), the planner's
// expression rewriter outside subqueries, pkg/planner/core/expression_rewriter.go.
//
// Op "expr": args [table, expr, params, opts]. table is the JSON of a model.TableInfo (built
// from a CREATE TABLE by ddl.BuildTableInfoFromAST, ID 100, database "test"); expr is the SQL
// text of one expression, parsed as the only field of "select <expr> from <table>" (no
// preprocess); params are the values bound to the "?" markers (d2DatumIn form, written into
// SessionVars.PlanCacheParams); opts is {"nc": new collation, "cache": plan cache on for the
// statement (StmtCtx.EnablePlanCache), "flags": type flags, "levels": [7]err levels, "mode":
// SQL mode}. The schema and names are expression.TableInfo2SchemaAndNames of the table (the
// column UniqueIDs start at 1); the rewrite is rewriteAstExprWithPlanCtx (a TableDual with
// that schema; clause "expression", column privileges per column) on core.MockContext.
//
// out: {"e": tree, "s": StringWithCtx, "vi": visit infos [[priv, db, table, column, err]],
// "w": warnings} or {"err": msg, "w": [...]}; {"pre": msg} when Go cannot parse the
// statement (tidb-rs skips the record). A tree node is
// {"col": [UniqueID, ID, Index, ft, OrigName, coer, rep]},
// {"const": [datum (d2DatumIn form), ft, param marker order | -1, deferred tree | null, coer,
// rep]} or {"f": sfDump's function node}; ft is [tp, flag, flen, decimal, charset, collate,
// elems|null].

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"
	gotime "time"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/ddl"
	"github.com/pingcap/tidb/pkg/errctx"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/meta/metabuild"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/types"
	driver "github.com/pingcap/tidb/pkg/types/parser_driver"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const rewriteSeed = 20261118

func init() {
	vec.Register(&vec.Domain{
		Name:     "rewrite",
		Seed:     rewriteSeed,
		Coverage: rewriteCoverage,
		Blocks:   []string{"D09.2"},
		Exempt:   rewriteExempt(),
		Gzip:     true,
		Generate: rewriteGenerate,
		Exec:     rewriteExec,
	})
}

var rewriteCoverage = map[string]vec.Coverage{
	"expr": {Min: 1500, GoFuncs: []string{"rewriteAstExprWithPlanCtx", "PlanBuilder.rewrite",
		"PlanBuilder.rewriteWithPreprocess", "PlanBuilder.getExpressionRewriter", "rewriteExprNode",
		"expressionRewriter.ctxStackLen", "expressionRewriter.ctxStackPop", "expressionRewriter.ctxStackAppend",
		"expressionRewriter.constructBinaryOpFunction", "expressionRewriter.requirePlanCtx",
		"expressionRewriter.Enter", "expressionRewriter.Leave", "initConstantRepertoire",
		"expressionRewriter.adjustUTF8MB4Collation", "expressionRewriter.newFunctionWithInit",
		"expressionRewriter.newFunction", "expressionRewriter.checkTimePrecision", "expressionRewriter.useCache",
		"expressionRewriter.unaryOpToExpression", "expressionRewriter.binaryOpToExpression",
		"expressionRewriter.notToExpression", "expressionRewriter.isNullToExpression",
		"expressionRewriter.isTrueToScalarFunc", "expressionRewriter.inToExpression",
		"expressionRewriter.deriveCollationForIn", "expressionRewriter.castCollationForIn",
		"expressionRewriter.caseToExpression", "expressionRewriter.patternLikeOrIlikeToExpression",
		"expressionRewriter.regexpToScalarFunc", "expressionRewriter.rowToScalarFunc",
		"expressionRewriter.wrapExpWithCast", "expressionRewriter.betweenToExpression",
		"expressionRewriter.rewriteFuncCall", "expressionRewriter.funcCallToExpression",
		"expressionRewriter.toTable", "expressionRewriter.toParamMarker", "expressionRewriter.clause",
		"expressionRewriter.toColumn", "findFieldNameFromNaturalUsingJoin", "PlanBuilder.appendColNamesToVisitInfo",
		"expressionRewriter.precollectColNames"}},
}

// rewriteExempt: the subquery half of the block (D09.6 in tidb-rs's plan), and the
// branches that are boundary stubs in part 1 (specs/D09.2-rewriter/spec.md §4).
func rewriteExempt() map[string]string {
	ex := map[string]string{}
	subq := "subquery rewriting (D09.2 part 2 with the subquery block D09.6: sub-plans, Apply / semi join); D09.2 spec §4"
	for _, f := range []string{"expressionRewriter.buildSubquery", "expressionRewriter.buildSemiApplyFromEqualSubq",
		"expressionRewriter.handleCompareSubquery", "expressionRewriter.handleOtherComparableSubq",
		"expressionRewriter.buildQuantifierPlan", "expressionRewriter.handleNEAny", "expressionRewriter.handleEQAll",
		"expressionRewriter.handleExistSubquery", "expressionRewriter.popExistsSubPlan",
		"expressionRewriter.handleInSubquery", "isNoDecorrelate", "expressionRewriter.handleScalarSubquery",
		"hasCTEConsumerInSubPlan", "hasLimit"} {
		ex[f] = subq
	}
	for _, f := range []string{"ScalarSubQueryExpr.CanonicalHashCode", "ScalarSubQueryExpr.Clone",
		"ScalarSubQueryExpr.ConstLevel", "ScalarSubQueryExpr.Decorrelate", "ScalarSubQueryExpr.Equal",
		"ScalarSubQueryExpr.Equals", "ScalarSubQueryExpr.Eval", "ScalarSubQueryExpr.EvalDecimal",
		"ScalarSubQueryExpr.EvalDuration", "ScalarSubQueryExpr.EvalInt", "ScalarSubQueryExpr.EvalJSON",
		"ScalarSubQueryExpr.EvalReal", "ScalarSubQueryExpr.EvalString", "ScalarSubQueryExpr.EvalTime",
		"ScalarSubQueryExpr.ExplainInfo", "ScalarSubQueryExpr.ExplainNormalizedInfo", "ScalarSubQueryExpr.GetType",
		"ScalarSubQueryExpr.Hash64", "ScalarSubQueryExpr.HashCode", "ScalarSubQueryExpr.IsCorrelated",
		"ScalarSubQueryExpr.MemoryUsage", "ScalarSubQueryExpr.RemapColumn", "ScalarSubQueryExpr.ResolveIndices",
		"ScalarSubQueryExpr.ResolveIndicesByVirtualExpr", "ScalarSubQueryExpr.String", "ScalarSubQueryExpr.Traverse",
		"ScalarSubQueryExpr.VecEvalDecimal", "ScalarSubQueryExpr.VecEvalDuration", "ScalarSubQueryExpr.VecEvalInt",
		"ScalarSubQueryExpr.VecEvalJSON", "ScalarSubQueryExpr.VecEvalReal", "ScalarSubQueryExpr.VecEvalString",
		"ScalarSubQueryExpr.VecEvalTime", "ScalarSubQueryExpr.Vectorized", "ScalarSubQueryExpr.resolveIndices",
		"ScalarSubQueryExpr.resolveIndicesByVirtualExpr", "ScalarSubQueryExpr.selfEvaluate",
		"ScalarSubqueryEvalCtx.ExplainInfo", "ScalarSubqueryEvalCtx.Schema", "ScalarSubqueryEvalCtx.getColVal",
		"ScalarSubqueryEvalCtx.selfEval"} {
		ex[f] = "the scalar subquery expression (non-correlated subqueries evaluated in the optimizer), D09.2 part 2 with the subquery block D09.6; D09.2 spec §4"
	}
	ex["PlanBuilder.rewriteInsertOnDuplicateUpdate"] = "INSERT ... ON DUPLICATE KEY UPDATE: the write path (X); D09.2 spec §4"
	ex["expressionRewriter.rewriteUserVariable"] = "user variables need the getvar / setvar functions (D08.9): boundary stub; D09.2 spec §4"
	ex["expressionRewriter.rewriteSystemVariable"] = "system variables need the sysvar lookup (D05.1): boundary stub; D09.2 spec §4"
	ex["expressionRewriter.evalDefaultExprForTable"] = "DEFAULT(col) reads column defaults (D09.15 write-path plans): boundary stub; D09.2 spec §4"
	ex["expressionRewriter.evalDefaultExprWithPlanCtx"] = "DEFAULT(col) reads column defaults (D09.15 write-path plans): boundary stub; D09.2 spec §4"
	ex["expressionRewriter.evalFieldDefaultValue"] = "DEFAULT(col) reads column defaults (D09.15 write-path plans): boundary stub; D09.2 spec §4"
	ex["hasCurrentDatetimeDefault"] = "DEFAULT(col) reads column defaults (D09.15 write-path plans): boundary stub; D09.2 spec §4"
	ex["expressionRewriter.funcCallToExpressionWithPlanCtx"] = "GROUPING() needs ROLLUP's Expand (D09.4): boundary stub; D09.2 spec §4"
	ex["expressionRewriter.positionToScalarFunc"] = "ORDER BY / GROUP BY positions: only reached through buildSelect (D09.1 plan vectors); D09.2 spec §4"
	ex["expressionRewriter.collectPrivsForCount"] = "COUNT() is reached through an aggregate map (buildSelect, D09.1 plan vectors); D09.2 spec §4"
	ex["buildSimpleExpr"] = "the plan-less rewriter (ParseSimpleExpr, D08.2 P1-3): D09.2 part 2; D09.2 spec §2"
	ex["evalAstExpr"] = "the plan-less rewriter (ParseSimpleExpr, D08.2 P1-3): D09.2 part 2; D09.2 spec §2"
	ex["evalAstExprWithPlanCtx"] = "evaluates a rewritten expression for LIMIT / SET / SHOW (D09.16): D09.2 part 2; D09.2 spec §2"
	return ex
}

// rwRewrite is core.rewriteAstExprWithPlanCtx (unexported). Its []visitInfo is read through
// rwVisitInfo, a copy of the unexported struct.
//
//go:linkname rwRewrite github.com/pingcap/tidb/pkg/planner/core.rewriteAstExprWithPlanCtx
func rwRewrite(sctx base.PlanContext, expr ast.ExprNode, schema *expression.Schema, names types.NameSlice,
	allowCastArray bool) (expression.Expression, []rwVisitInfo, error)

type rwVisitInfo struct {
	privilege        mysql.PrivilegeType
	db               string
	table            string
	column           string
	err              error
	alterWritable    bool
	dynamicPrivs     []string
	dynamicWithGrant bool
}

type rwOpts struct {
	NC     bool            `json:"nc"`
	Cache  bool            `json:"cache"`
	Flags  uint16          `json:"flags"`
	Levels [7]errctx.Level `json:"levels"`
	Mode   mysql.SQLMode   `json:"mode"`
}

var (
	rwMu  sync.Mutex
	rwCtx *mock.Context
)

func rwSession(o rwOpts, params []types.Datum) *mock.Context {
	collate.SetNewCollationEnabledForTest(o.NC)
	if rwCtx == nil {
		rwCtx = core.MockContext()
	}
	vars := rwCtx.GetSessionVars()
	vars.PlanColumnID.Store(0)
	vars.PlanID.Store(0)
	vars.TimeZone = gotime.UTC
	vars.StmtCtx = stmtctx.NewStmtCtxWithTimeZone(gotime.UTC)
	vars.StmtCtx.SetTypeFlags(types.Flags(o.Flags))
	vars.StmtCtx.SetErrLevels(errctx.LevelMap(o.Levels))
	vars.SQLMode = o.Mode
	if o.Cache {
		vars.StmtCtx.EnablePlanCache()
	}
	vars.PlanCacheParams.Reset()
	vars.PlanCacheParams.Append(params...)
	return rwCtx
}

func rwCoerRep(e expression.Expression) []any {
	return []any{int(e.Coercibility()), int(e.Repertoire())}
}

func rwDump(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		return map[string]any{"col": append([]any{x.UniqueID, x.ID, x.Index, sfFtIn(x.RetType),
			vec.GoString(x.OrigName)}, rwCoerRep(x)...)}
	case *expression.Constant:
		pm := -1
		if x.ParamMarker != nil {
			pm = rwParamOrder(x)
		}
		var def any
		if x.DeferredExpr != nil {
			def = rwDump(x.DeferredExpr)
		}
		return map[string]any{"const": append([]any{d2DatumInOf(x.Value), sfFtIn(x.RetType), pm, def},
			rwCoerRep(x)...)}
	case *expression.ScalarFunction:
		// sfDump's function node, the arguments in this format.
		args := []any{}
		allVec := true
		for _, a := range x.GetArgs() {
			args = append(args, rwDump(a))
			allVec = allVec && a.Vectorized()
		}
		sigVec := true
		if allVec {
			sigVec = x.Vectorized()
		}
		chs, coll := x.CharsetAndCollation()
		return map[string]any{"f": []any{x.FuncName.L, x.FuncName.O, reflect.TypeOf(x.Function).Elem().Name(),
			sfFtIn(x.RetType), args, sigVec, chs, coll, int(x.Function.PbCode()), x.HasCoercibility(),
			int(x.Coercibility()), int(x.Repertoire()), x.IsExplicitCharset()}}
	}
	panic(fmt.Sprintf("rwDump %T", e))
}

// rwParamOrder reads ParamMarker.order (unexported).
func rwParamOrder(c *expression.Constant) int {
	return fpField(c.ParamMarker, "order").(int)
}

// rwMarkers collects the parameter markers of an AST.
type rwMarkers struct{ ms []*driver.ParamMarkerExpr }

func (v *rwMarkers) Enter(n ast.Node) (ast.Node, bool) {
	if m, ok := n.(*driver.ParamMarkerExpr); ok {
		v.ms = append(v.ms, m)
	}
	return n, false
}

func (v *rwMarkers) Leave(n ast.Node) (ast.Node, bool) { return n, true }

func rwWarns(sctx *mock.Context) []vec.GoString {
	out := []vec.GoString{}
	for _, w := range sctx.GetSessionVars().StmtCtx.GetWarnings() {
		out = append(out, vec.GoString(w.Err.Error()))
	}
	return out
}

func rewriteExec(op string, p []json.RawMessage) (out any, err error) {
	rwMu.Lock()
	defer rwMu.Unlock()
	defer collate.SetNewCollationEnabledForTest(true)
	if op != "expr" {
		return nil, errors.Errorf("unknown op %s", op)
	}
	tblJSON := arg[string](p, 0)
	exprSQL := arg[string](p, 1)
	rawParams := arg[[][]json.RawMessage](p, 2)
	o := arg[rwOpts](p, 3)
	ti := &model.TableInfo{}
	must(json.Unmarshal([]byte(tblJSON), ti))
	params := make([]types.Datum, 0, len(rawParams))
	for _, rp := range rawParams {
		params = append(params, d2DatumIn(rp))
	}
	sctx := rwSession(o, params)
	stmt, perr := parser.New().ParseOneStmt("select "+exprSQL+" from "+ti.Name.O, "", "")
	if perr != nil {
		return map[string]any{"pre": vec.GoString(perr.Error())}, nil
	}
	field := stmt.(*ast.SelectStmt).Fields.Fields[0].Expr
	// EXECUTE writes the values into the markers too (SetParameterValuesIntoSCtx).
	pm := &rwMarkers{}
	field.Accept(pm)
	for _, m := range pm.ms {
		if m.Order < len(params) {
			m.Datum = params[m.Order]
			m.InExecute = true
		}
	}
	return rbTry(func() any {
		schema, names, err := expression.TableInfo2SchemaAndNames(sctx.GetExprCtx(), pmodel.NewCIStr("test"), ti)
		must(err)
		e, vis, err := rwRewrite(sctx, field, schema, names, false)
		if err != nil {
			return map[string]any{"err": vec.GoString(err.Error()), "w": rwWarns(sctx)}
		}
		vi := []any{}
		for _, v := range vis {
			msg := ""
			if v.err != nil {
				msg = v.err.Error()
			}
			vi = append(vi, []any{uint64(v.privilege), v.db, v.table, v.column, vec.GoString(msg)})
		}
		s := e.StringWithCtx(sctx.GetExprCtx().GetEvalCtx(), errors.RedactLogDisable)
		return map[string]any{"e": rwDump(e), "s": vec.GoString(s), "vi": vi, "w": rwWarns(sctx)}
	}), nil
}

// ---------------------------------------------------------------- generation

var rwDDLs = []string{
	// sysbench
	"CREATE TABLE sbtest1 (id int NOT NULL AUTO_INCREMENT, k int NOT NULL DEFAULT '0', " +
		"c char(120) NOT NULL DEFAULT '', pad char(60) NOT NULL DEFAULT '', PRIMARY KEY (id), KEY k_1 (k))",
	"CREATE TABLE t (a int, b bigint unsigned, c double, d decimal(10,2), e varchar(20), " +
		"f varchar(20) collate utf8mb4_general_ci, g datetime(3), h time, i date, j enum('x','y'), " +
		"s set('a','b'), l json, m bit(8), n year, o char(10) binary, p blob, q tinyint not null, r float, " +
		"u varchar(10) charset latin1, v varchar(10) charset binary, w timestamp(6) null)",
}

var rwHand = []string{
	// sysbench read-only
	"id = ?", "id between ? and ?", "c", "k", "id", "pad", "sum(k)", "id = 1", "id between 1 and 100",
	"c = ?", "k = ? and id = ?",
	// columns and names
	"a", "t.a", "test.t.a", "T.A", "x", "t2.a", "other.t.a",
	// literals
	"1", "-1", "'abc'", "'中文'", "1.5", "1e3", "1.25e-2", "x'0a'", "0x0a0b", "b'101'", "0b1", "null", "true", "false",
	"18446744073709551615", "9223372036854775808", "-9223372036854775808", "123456789012345678901234567890",
	"'a' 'b'", "N'abc'", "_binary'abc'", "_latin1'abc'",
	// arithmetic and bit ops
	"a + 1", "a - b", "a * c", "a / 2", "a div 2", "a % 3", "a mod 3", "d + 1.25", "c * 2.5", "-a", "- - a", "+a", "~a",
	"a & b", "a | b", "a ^ b", "a << 2", "a >> 1", "1 + 2", "1 / 0", "'1a' + 1", "e + 1", "g + 0", "j + 1", "m + 1",
	// comparison
	"a = 1", "a = '1'", "a = 1.5", "1 = a", "a <=> null", "a != 2", "a <> 2", "a > b", "a >= 1", "a < 1.0", "a <= '2'",
	"e = 'x'", "e = f", "f = 'X'", "e = u", "g = '2020-01-01'", "g > '2020-01-01 10:00:00.123'", "h = '10:00:00'",
	"i = 20200101", "j = 'x'", "j = 1", "s = 'a,b'", "l = '{}'", "m = 1", "n = 2020", "q = 1", "r = 1.5",
	"b = -1", "b > 18446744073709551615", "a = 1e100", "a = '1e1'", "e = 1", "v = 'a'", "w = '2020-01-01'",
	"null = null", "a = null",
	// logic
	"a and b", "a or b", "a xor b", "not a", "!a", "a and 1", "0 or a", "a and null", "1 and 0", "not (a > 1)",
	"a > 1 and b < 2 or c = 3",
	// rows
	"(a, b) = (1, 2)", "(a, b) != (1, 2)", "(a, b) <=> (1, null)", "(a, b) > (1, 2)", "(a, b, c) >= (1, 2, 3)",
	"(a, b) < (1, 2)", "(a, b) <= (1, 2)", "(a, b) = (1, 2, 3)", "(a, b) = 1", "row(1, 2)", "(a, b)",
	"((a, b), c) = ((1, 2), 3)",
	// IN
	"a in (1, 2, 3)", "a in (1)", "a not in (1, 2)", "a in (1, '2', 3.0)", "a in ('1', '2')", "a in (1, null)",
	"a in (b, c)", "e in ('a', 'b')", "e in ('a', f)", "f in ('a', 'B')", "e in (u, 'x')", "(a, b) in ((1, 2), (3, 4))",
	"(a, b) in ((1, 2))", "(a, b) in (1, 2)", "null in (1, 2)", "a in (null)", "j in ('x', 'y')", "j in (1, 2)",
	"g in ('2020-01-01', '2021-01-01')", "d in (1, 2.5)", "b in (-1, 1)", "a in (1.5, 2)", "e in (1, 2)",
	"v in ('a', 'b')", "a in (?, ?)", "e in (?, ?, ?)",
	// BETWEEN
	"a between 1 and 10", "a not between b and 10", "e between 'a' and 'z'", "f between 'a' and e",
	"g between '2020-01-01' and '2021-01-01'", "h between '10:00:00' and '11:00:00'", "i between 20200101 and 20201231",
	"c between 1 and 2.5", "d between 1 and '2'", "j between 'x' and 'y'", "a between 'a' and 'b'",
	"e between 1 and 2", "a between ? and ?", "null between 1 and 2",
	// LIKE / REGEXP
	"e like 'ab%'", "e like 'abc'", "e not like 'a_c' escape '|'", "e ilike 'A%'", "f like 'abc'", "e like f",
	"a like '1%'", "e like null", "e like ?", "v like 'a%'", "e regexp '^a'", "e not regexp 'b$'", "e rlike 'x'",
	// IS
	"a is null", "a is not null", "a is true", "a is not true", "a is false", "a is not false", "e is null",
	"(a, b) is null", "null is null", "q is null",
	// CASE
	"case a when 1 then 'x' when 2 then 'y' else 'z' end", "case when a > 1 then b else c end", "case when a then 1 end",
	"case a when 1 then 2 end", "case e when 'a' then 1 when f then 2 end", "case when null then 1 else 2 end",
	"case when (a, b) = (1, 2) then 1 end", "case (a, b) when 1 then 2 end",
	// CAST
	"cast(a as char)", "cast(e as signed)", "cast(c as decimal(10,2))", "cast(g as date)", "cast(h as time(7))",
	"cast(h as time(6))", "cast(e as binary)", "cast(a as char charset ascii)", "cast(a as json)",
	"cast(e as unsigned)", "cast(d as double)", "cast(a as datetime)", "cast(e as char(3))", "cast((a, b) as char)",
	"convert(e, char)", "convert(e using latin1)", "cast(1.5 as signed)", "cast('abc' as char(2))",
	"cast(a as year)", "cast(a as float)", "cast(e as decimal)", "cast(a as char array)",
	// function calls
	"ifnull(q, 1)", "ifnull(a, 1)", "ifnull(q, null)", "ifnull(j, 'x')", "ifnull(a, 1, 2)", "nullif(a, 1)",
	"nullif(e, 'x')", "nullif((a, b), 1)", "if(a, b, c)", "coalesce(a, b)", "concat(e, f)", "concat(e, 'x')",
	"concat(e, u)", "length(e)", "upper(e)", "abs(a)", "abs(-1)", "a + ?", "e = ?", "now()", "unix_timestamp()",
	"unix_timestamp(g)", "rand()", "current_timestamp()", "sysdate()", "connection_id()", "database()",
	"greatest(a, b)", "least(e, f)", "concat(?, e)", "abs(?)", "length((a, b))", "nosuchfunc(a)",
	"trim(both 'x' from e)", "trim(leading 'x' from e)", "trim(e)", "date_add(g, interval 1 day)",
	"date_sub(i, interval '1-2' year_month)", "extract(hour from h)", "get_format(date, 'usa')",
	"timestampdiff(day, g, i)", "benchmark(1, a + 1)", "position('a' in e)", "substring(e, 1, 2)",
	"round(c, 1)", "sum(a)", "count(*)", "max(a)", "avg(d)", "row_number() over ()",
	// COLLATE
	"e collate utf8mb4_bin", "'abc' collate utf8mb4_general_ci", "e collate latin1_bin", "j collate utf8mb4_bin",
	"a collate utf8mb4_bin", "l collate utf8mb4_bin", "concat(e, 'x') collate utf8mb4_bin", "e collate nosuch",
	"u collate latin1_bin", "e = f collate utf8mb4_bin", "1 collate binary",
	// parentheses
	"(a)", "((a + 1))", "(1)", "((e))",
	// stubs and unsupported nodes
	"@a", "@a := 1", "@@sql_mode", "@@global.sql_mode", "default(a)", "values(a)", "exists (select 1)",
	"a in (select 1)", "(select 1)", "a > any (select 1)", "a = all (select 1)", "a in ((select 1))",
	"match(e) against('x')", "grouping(a)",
}

func rwTables() []string {
	out := []string{}
	for i, d := range rwDDLs {
		stmt, err := parser.New().ParseOneStmt(d, "", "")
		must(err)
		ti, err := ddl.BuildTableInfoFromAST(metabuild.NewContext(), stmt.(*ast.CreateTableStmt))
		must(err)
		ti.ID = int64(100 + i)
		ti.State = model.StatePublic
		b, err := json.Marshal(ti)
		must(err)
		out = append(out, string(b))
	}
	return out
}

// rwDefaultOpts: TiDB's default SQL mode, the statement flags of a SELECT.
var rwDefaultOpts = rwOpts{NC: true, Flags: uint16(types.DefaultStmtFlags), Mode: mysql.ModeStrictTransTables |
	mysql.ModeNoZeroDate | mysql.ModeNoZeroInDate | mysql.ModeErrorForDivisionByZero | mysql.ModeNoEngineSubstitution |
	mysql.ModeOnlyFullGroupBy | mysql.ModeNoAutoCreateUser}

// rwParams: values for n markers.
func rwParams(rng *rand.Rand, n int) [][]any {
	out := [][]any{}
	for i := 0; i < n; i++ {
		var d types.Datum
		switch rng.IntN(8) {
		case 0:
			d = types.NewIntDatum(rng.Int64N(2000) - 1000)
		case 1:
			d = types.NewUintDatum(rng.Uint64())
		case 2:
			d = types.NewFloat64Datum(float64(rng.IntN(1000)) / 8)
		case 3:
			d = types.NewCollationStringDatum([]string{"a", "abc", "1", "x%", "中", "2020-01-01"}[rng.IntN(6)], "utf8mb4_bin")
		case 4:
			d = types.NewDecimalDatum(types.NewDecFromStringForTest([]string{"1.5", "-2.25", "100"}[rng.IntN(3)]))
		case 5:
			d = types.NewDatum(nil)
		default:
			d = types.NewIntDatum(int64(rng.IntN(10)))
		}
		out = append(out, d2DatumInOf(d).([]any))
	}
	return out
}

func rwOptsVariant(rng *rand.Rand) rwOpts {
	o := rwDefaultOpts
	o.NC = rng.IntN(4) != 0
	if rng.IntN(3) == 0 {
		o.Flags = uint16(types.DefaultStmtFlags | types.FlagTruncateAsWarning)
	}
	if rng.IntN(4) == 0 {
		o.Mode = mysql.ModeStrictTransTables
	}
	return o
}

func rwAdd(g *vec.Gen, rng *rand.Rand, src, tbl, e string, o rwOpts) {
	n := strings.Count(e, "?")
	params := rwParams(rng, n)
	if n > 0 {
		o.Cache = rng.IntN(2) == 0
	}
	g.Add(src, "expr", tbl, e, params, o)
}

// The random grammar: atoms of each type, operators, functions.
var (
	rwAtoms = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "q", "r", "u", "n", "1", "0", "-3", "2.5",
		"1e2", "'x'", "'10'", "'2020-01-01'", "null", "true", "?", "x'41'"}
	rwBin = []string{"+", "-", "*", "/", "div", "%", "=", "!=", "<", "<=", ">", ">=", "<=>", "and", "or", "xor",
		"&", "|", "<<"}
	rwFuncs = []string{"abs", "length", "upper", "concat", "ifnull", "nullif", "if", "coalesce", "greatest"}
)

func rwRand(rng *rand.Rand, depth int) string {
	if depth == 0 || rng.IntN(3) == 0 {
		return rwAtoms[rng.IntN(len(rwAtoms))]
	}
	x := func() string { return rwRand(rng, depth-1) }
	return "(" + rwRandNode(rng, x) + ")"
}

// rwRandNode: one composite node over the operands x() (parenthesized by rwRand, so that
// the precedence of IS / LIKE / BETWEEN does not make the text unparsable).
func rwRandNode(rng *rand.Rand, x func() string) string {
	switch rng.IntN(12) {
	case 0, 1, 2, 3:
		return x() + " " + rwBin[rng.IntN(len(rwBin))] + " " + x()
	case 4:
		return []string{"-", "not ", "~"}[rng.IntN(3)] + x()
	case 5:
		return x() + " in (" + x() + ", " + x() + ")"
	case 6:
		return x() + " between " + x() + " and " + x()
	case 7:
		return x() + " is " + []string{"null", "not null", "true", "false"}[rng.IntN(4)]
	case 8:
		return x() + " like " + []string{"'a%'", "'abc'", "'_'"}[rng.IntN(3)]
	case 9:
		return "case when " + x() + " then " + x() + " else " + x() + " end"
	case 10:
		f := rwFuncs[rng.IntN(len(rwFuncs))]
		switch f {
		case "abs", "length", "upper":
			return f + "(" + x() + ")"
		case "if":
			return f + "(" + x() + ", " + x() + ", " + x() + ")"
		default:
			return f + "(" + x() + ", " + x() + ")"
		}
	default:
		return "cast(" + x() + " as " + []string{"signed", "char", "decimal(8,3)", "double", "date"}[rng.IntN(5)] + ")"
	}
}

func rewriteGenerate(g *vec.Gen, rng *rand.Rand) {
	tbls := rwTables()
	for _, e := range rwHand {
		tbl := tbls[1]
		if strings.Contains(e, "id") || e == "c" || e == "k" || e == "pad" || strings.Contains(e, "sum(k)") {
			tbl = tbls[0]
		}
		rwAdd(g, rng, "sys/hand", tbl, e, rwDefaultOpts)
		// The same with new collation off and with the plan cache on.
		o := rwDefaultOpts
		o.NC = false
		rwAdd(g, rng, "sys/hand_oldcoll", tbl, e, o)
	}
	for i := 0; i < 1800; i++ {
		rwAdd(g, rng, "rand/expr", tbls[1], rwRand(rng, 3), rwOptsVariant(rng))
	}
}
