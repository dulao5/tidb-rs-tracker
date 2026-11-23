package domains

// Domain "plancache": block D13.1 (the plan cache), the part prepared-statement planning
// ports (tidb-rs VS1-15): the cacheability check of a prepared statement,
// pkg/planner/core/plan_cacheable_checker.go IsASTCacheable; and the parameter type check of
// the instance plan cache's lookup (the plan-cache exception A3, first stage, VS1-39),
// checkTypesCompatibility4PC (op "types_compat", plancache_types.go).
//
// Records:
//   - "ast": args [sess, tables, sql] -> {"c": cacheable, "r": reason} of
//     core.CacheableWithCtx for the parsed statement, or {"parse": msg} when Go does not
//     parse it (tidb-rs skips those). sess is {db, param_limit, subquery, static_prune, fix}:
//     the current database, tidb_enable_plan_cache_for_param_limit,
//     tidb_enable_plan_cache_for_subquery, tidb_partition_prune_mode = 'static', and
//     tidb_opt_fix_control entries (44823, 45798, 33031). tables are TableInfo JSON
//     strings, the InfoSchema is MockInfoSchema(tables) (database "test").
//
// The cases follow plan_cacheable_checker_test.go TestCacheable (its AST rows written as SQL;
// the DML rows are kept: tidb-rs has no DML AST and counts them as blocked) and
// TestFixControl44823, plus every reason of cacheableChecker.Enter and checkTableCacheable.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/infoschema"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const plancacheSeed = 20261115

// PlancacheMinedTests: the Go tests whose cases are records.
func PlancacheMinedTests() []string { return []string{"TestCacheable", "TestFixControl44823"} }

func init() {
	vec.Register(&vec.Domain{
		Name:     "plancache",
		Seed:     plancacheSeed,
		Coverage: plancacheCoverage,
		Blocks:   []string{"D13.1"},
		Exempt:   plancacheExempt,
		Gzip:     true,
		Generate: plancacheGenerate,
		Exec:     plancacheExec,
	})
}

var plancacheCoverage = map[string]vec.Coverage{
	"ast": {Min: 60, GoFuncs: []string{"CacheableWithCtx", "IsASTCacheable", "cacheableChecker.Enter",
		"cacheableChecker.Leave", "checkTableCacheable", "getMaxParamLimit", "enablePlanCacheForGeneratedCols"}},
	"types_compat": {Min: 500, GoFuncs: []string{"checkTypesCompatibility4PC"}},
}

type pcSess struct {
	DB          string            `json:"db"`
	ParamLimit  bool              `json:"param_limit"`
	Subquery    bool              `json:"subquery"`
	StaticPrune bool              `json:"static_prune,omitempty"`
	Fix         map[string]string `json:"fix,omitempty"`
}

func pcTables() []*model.TableInfo {
	cols := func(names ...string) []*model.ColumnInfo {
		var out []*model.ColumnInfo
		for i, n := range names {
			out = append(out, &model.ColumnInfo{ID: int64(i + 1), Name: pmodel.NewCIStr(n), Offset: i,
				State: model.StatePublic, FieldType: *types.NewFieldType(mysql.TypeLong)})
		}
		return out
	}
	t1 := &model.TableInfo{ID: 101, Name: pmodel.NewCIStr("t1"), State: model.StatePublic, Columns: cols("a", "b"),
		Partition: &model.PartitionInfo{Type: pmodel.PartitionTypeRange, Expr: "`a`", Enable: true,
			Definitions: []model.PartitionDefinition{
				{ID: 1011, Name: pmodel.NewCIStr("p0"), LessThan: []string{"6"}},
				{ID: 1012, Name: pmodel.NewCIStr("p1"), LessThan: []string{"11"}}}}}
	t2 := &model.TableInfo{ID: 102, Name: pmodel.NewCIStr("t2"), State: model.StatePublic, Columns: cols("a", "b"),
		Partition: &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Expr: "`a`", Enable: true, Num: 2,
			Definitions: []model.PartitionDefinition{
				{ID: 1021, Name: pmodel.NewCIStr("p0")}, {ID: 1022, Name: pmodel.NewCIStr("p1")}}}}
	t3 := &model.TableInfo{ID: 103, Name: pmodel.NewCIStr("t3"), State: model.StatePublic, Columns: cols("a", "b")}
	tg := &model.TableInfo{ID: 104, Name: pmodel.NewCIStr("tg"), State: model.StatePublic, Columns: cols("a", "b")}
	tg.Columns[1].GeneratedExprString = "`a` + 1"
	tt := &model.TableInfo{ID: 105, Name: pmodel.NewCIStr("tt"), State: model.StatePublic, Columns: cols("a"),
		TempTableType: model.TempTableGlobal}
	return []*model.TableInfo{t1, t2, t3, tg, tt}
}

func plancacheExec(op string, args []json.RawMessage) (any, error) {
	if op == "types_compat" {
		return plancacheTypesExec(args), nil
	}
	if op != "ast" {
		return nil, errors.Errorf("unknown op %s", op)
	}
	var sess pcSess
	must(json.Unmarshal(args[0], &sess))
	var tjs []string
	must(json.Unmarshal(args[1], &tjs))
	var sql string
	must(json.Unmarshal(args[2], &sql))
	var tbls []*model.TableInfo
	for _, s := range tjs {
		t := &model.TableInfo{}
		must(json.Unmarshal([]byte(s), t))
		tbls = append(tbls, t)
	}
	stmt, err := parser.New().ParseOneStmt(sql, "", "")
	if err != nil {
		return map[string]any{"parse": vec.GoString(err.Error())}, nil
	}
	sctx := mock.NewContext()
	vars := sctx.GetSessionVars()
	vars.CurrentDB = sess.DB
	vars.EnablePlanCacheForParamLimit = sess.ParamLimit
	vars.EnablePlanCacheForSubquery = sess.Subquery
	if sess.StaticPrune {
		vars.PartitionPruneMode.Store(string(variable.Static))
	} else {
		vars.PartitionPruneMode.Store(string(variable.Dynamic))
	}
	if len(sess.Fix) > 0 {
		vars.OptimizerFixControl = map[uint64]string{}
		for k, v := range sess.Fix {
			var n uint64
			fmt.Sscan(k, &n)
			vars.OptimizerFixControl[n] = v
		}
	}
	_ = context.Background()
	c, r := core.CacheableWithCtx(sctx.GetPlanCtx(), stmt, infoschema.MockInfoSchema(tbls))
	return map[string]any{"c": c, "r": vec.GoString(r)}, nil
}

func plancacheGenerate(g *vec.Gen, r *rand.Rand) {
	plancacheTypesGenerate(g, r)
	var tjs []string
	for _, t := range pcTables() {
		b, err := json.Marshal(t)
		must(err)
		tjs = append(tjs, string(b))
	}
	on := pcSess{DB: "test", ParamLimit: true, Subquery: true}
	off := pcSess{DB: "test"}
	add := func(src string, s pcSess, sql string) { g.Add(src, "ast", s, tjs, sql) }

	// from: plan_cacheable_checker_test.go TestCacheable (the AST rows as SQL)
	for _, sql := range []string{
		"show tables",
		"load data infile '/tmp/t' into table t3",
		"import into t3 from '/tmp/t'",
		"select 1 union select 2",
		"insert into t3 values (1, 2)",
		"insert into t3 select * from t3",
		"delete from t3 where rand()",
		"update t3 set a = 1 where rand()",
		"delete from t3 where exists (select 1)",
		"delete from t3 limit ?",
		"delete /*+ ignore_plan_cache() */ from t3 limit 1",
		"update /*+ ignore_plan_cache() */ t3 set a = 1 limit 1",
		"select * from t3 where rand()",
		"select * from t3 where exists (select 1)",
		"select * from t3 limit ?",
		"select * from t3 limit ?, 10",
		"select * from t3 limit 10",
		"select * from t3 order by ?",
		"select * from t3 order by 1",
		"select /*+ ignore_plan_cache() */ * from t3 order by 1",
		"select sum(a) over (order by a rows between ? preceding and current row) from t3",
		"select * from t1, t2",
		"select * from t3",
	} {
		add("TestCacheable", on, sql)
		add("TestCacheable", off, sql)
	}
	names := make([]string, 0, len(expression.UnCacheableFunctions))
	for n := range expression.UnCacheableFunctions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, fn := range names {
		add("TestCacheable", on, fmt.Sprintf("select * from t3 where %s()", fn))
		add("TestCacheable", on, fmt.Sprintf("select %s(a, 'x') from t3", fn))
	}
	// from: TestFixControl44823 (in-lists over the parameter limit, 44823 = 250 / 0)
	in := func(n int) string {
		return "select * from t3 where a in (" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
	}
	for _, fix := range []map[string]string{nil, {"44823": "250"}, {"44823": "0"}, {"44823": "x"}, {"44823": "-1"}, {"44823": "10"}} {
		s := on
		s.Fix = fix
		for _, n := range []int{10, 200, 201, 250, 251} {
			add("TestFixControl44823", s, in(n))
		}
		add("TestFixControl44823", s, "select * from t3 where a in (?, ?) and b in ("+strings.TrimSuffix(strings.Repeat("?,", 199), ",")+")")
	}

	// Every reason of cacheableChecker.Enter and checkTableCacheable, and the walk order.
	for _, sql := range []string{
		"select @a",
		"select @@autocommit",
		"select a from t3 where a = @a",
		"select (select 1)",
		"select a from t3 where a in (select a from t3)",
		"select a from t3 where a > any (select a from t3)",
		"select a from t3 where not exists (select a from t3 where b = ?)",
		"select a from t3 group by ?",
		"select a from t3 group by a order by ?",
		"select a from t3 group by a having a > ?",
		"select a from t3 order by a limit ?",
		"select a from t3 order by a limit 1, ?",
		"select a from t3 order by a limit ?, ?",
		"select a from t3 limit 1 offset ?",
		"select sum(a) over (order by a rows between 1 preceding and ? following) from t3",
		"select sum(a) over (order by a range between ? preceding and current row) from t3",
		"select sum(a) over w from t3 window w as (order by a rows ? preceding)",
		"select row_number() over (partition by a order by ?) from t3",
		"select group_concat(a order by ?) from t3",
		"select * from tg",
		"select * from tt",
		"select * from t1",
		"select * from t2 where a = ?",
		"select * from nosuch",
		"select * from nodb.t3",
		"select * from test.t3",
		"select * from TEST.T3",
		"with c as (select 1) select * from c",
		"select * from t3, (select a from t3) x",
		"select * from t3 join t1 on t3.a = t1.a",
		"select * from t3 join tt using (a)",
		"select database(), (select 1)",
		"select (select 1), database()",
		"select @a from t1",
		"select * from t1 where a = @a",
		"select version() from tg",
		"select a from tg where a = (select 1)",
		"select ? union select ?",
		"select a from t3 union select a from t1",
		"select a from t3 union all select a from t3 order by ?",
		"(select a from t3 order by ?) union select 1",
		"select a from t3 union select 1 limit ?",
		"select json_object('a', 1)",
		"select coalesce(a, 1) from t3",
		"select convert(a, char) from t3",
		"select convert(a using utf8mb4) from t3",
		"select a like 'x' from t3",
		"select a from t3 where a like ?",
		"select date '2020-01-01'",
		"select timestamp '2020-01-01 00:00:00'",
		"select time '10:00:00'",
		"select CONNECTION_ID()",
		"select aes_encrypt('a', 'b')",
		"select now(), rand(), uuid()",
		"select a from t3 where a = ? for update",
		"select a from t3 for update of t3",
		"select a from t1 for update of t1",
		"select * from t3 as of timestamp now()",
		"select * from t3 partition (p0)",
		"select * from t3 into outfile '/tmp/x'",
		"select ?",
		"select a + ? from t3 where b = ? order by a limit 1",
	} {
		add("sys/reasons", on, sql)
		add("sys/reasons", off, sql)
	}
	for _, fix := range []map[string]string{{"45798": "off"}, {"45798": "on"}, {"33031": "on"}, {"33031": "off"}} {
		s := on
		s.Fix = fix
		for _, sql := range []string{"select * from tg", "select * from t1", "select * from t3", "select * from t2 join tg on t2.a = tg.a"} {
			add("sys/fix", s, sql)
		}
	}
	static := on
	static.StaticPrune = true
	for _, sql := range []string{"select * from t1", "select * from t2", "select * from t3", "select * from t3 where a in (select a from t1)"} {
		add("sys/prune", static, sql)
	}
	nodb := on
	nodb.DB = ""
	for _, sql := range []string{"select * from t3", "select * from test.t3", "select 1"} {
		add("sys/nodb", nodb, sql)
	}
	other := on
	other.DB = "other"
	add("sys/nodb", other, "select * from t3")
}
