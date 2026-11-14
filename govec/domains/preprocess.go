package domains

// Domain "preprocess": block D09.0, the VS1-10 part of pkg/planner/core/preprocess.go (tidb-rs
// specs/VS1-vertical-slice): name resolution and the checks of a SELECT / set operation.
//
// Records:
//   - "pre": args [sql, inPrepare] -> the error of core.Preprocess ({code, msg}) or null.
//     The session's current database is "test"; the InfoSchema is MockInfoSchema with
//     core.MockSignedTable() (test.t) and a table test.person(id int); sql_mode and the noop
//     functions mode are the defaults. Parse errors are {"parse": msg} (the Rust replay
//     skips records its parser rejects as well).
//
// Most cases are the SELECT rows of preprocess_test.go TestValidator; the rest exercise the
// same checks (derived-table aliases, CAST precision, noop functions, CTE names, parameter
// markers, table names).

import (
	"context"
	"encoding/json"
	"math/rand/v2"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/infoschema"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const preprocessSeed = 20261116

// PreprocessMinedTests: the Go tests whose cases are records.
func PreprocessMinedTests() []string { return []string{"TestValidator"} }

func init() {
	vec.Register(&vec.Domain{
		Name:     "preprocess",
		Seed:     preprocessSeed,
		Coverage: preprocessCoverage,
		Blocks:   []string{"D09.0"},
		Exempt:   preprocessExempt,
		Gzip:     true,
		Generate: preprocessGenerate,
		Exec:     preprocessExec,
	})
}

var preprocessCoverage = map[string]vec.Coverage{
	"pre": {Min: 40, GoFuncs: []string{"Preprocess", "preprocessor.Enter", "preprocessor.Leave",
		"preprocessor.checkNonUniqTableAlias", "isTableAliasDuplicate", "preprocessor.checkFuncCastExpr",
		"preprocessor.checkSetOprSelectList", "preprocessor.checkSelectNoopFuncs", "preprocessor.checkGroupBy",
		"preprocessor.handleTableName", "preprocessor.tableByName", "preprocessor.ensureInfoSchema", "InPrepare",
		"WithPreprocessorReturn", "preprocessWith.UpdateCTEConsumerCount", "NewNodeW", "NodeW.GetResolveContext",
		"NewContext"}},
}

func preprocessIS() infoschema.InfoSchema {
	person := &model.TableInfo{ID: 2, Name: pmodel.NewCIStr("person"), State: model.StatePublic,
		Columns: []*model.ColumnInfo{{ID: 1, Name: pmodel.NewCIStr("id"), Offset: 0, State: model.StatePublic,
			FieldType: *types.NewFieldType(mysql.TypeLong)}}}
	return infoschema.MockInfoSchema([]*model.TableInfo{core.MockSignedTable(), person})
}

func preprocessExec(op string, args []json.RawMessage) (any, error) {
	if op != "pre" {
		return nil, errors.Errorf("unknown op %s", op)
	}
	var sql string
	var inPrepare bool
	mustUnmarshal(args[0], &sql)
	mustUnmarshal(args[1], &inPrepare)
	stmt, err := parser.New().ParseOneStmt(sql, "", "")
	if err != nil {
		return map[string]any{"parse": vec.GoString(err.Error())}, nil
	}
	sctx := mock.NewContext()
	sctx.GetSessionVars().CurrentDB = "test"
	var opts []core.PreprocessOpt
	if inPrepare {
		opts = append(opts, core.InPrepare)
	}
	nodeW := resolve.NewNodeW(stmt)
	err = core.Preprocess(context.Background(), sctx, nodeW,
		append(opts, core.WithPreprocessorReturn(&core.PreprocessorReturn{InfoSchema: preprocessIS()}))...)
	if err != nil {
		var code uint16
		if te, ok := errors.Cause(err).(*terror.Error); ok {
			code = uint16(terror.ToSQLError(te).Code)
		}
		return map[string]any{"code": code, "msg": vec.GoString(err.Error())}, nil
	}
	return nil, nil
}

func preprocessGenerate(g *vec.Gen, _ *rand.Rand) {
	// from: pkg/planner/core/preprocess_test.go TestValidator (the SELECT rows)
	for _, c := range []struct {
		sql       string
		inPrepare bool
	}{
		{"select ?", false},
		{"select ?", true},
		{"SELECT * FROM t INTO OUTFILE 'ttt' UNION SELECT * FROM u", false},
		{"select * from ( select 1 ) a, (select 2) a;", false},
		{"select * from ( select 1 ) a, (select 2) b, (select 3) a;", false},
		{"select * from ( select 1 ) a, (select 2) b, (select 3) A;", false},
		{"select * from ( select 1 ) a join (select 2) b join (select 3) a;", false},
		{"select person.id from person inner join person on person.id = person.id;", false},
		{"select * from ( select 1 ) a, (select 2) b;", true},
		{"select * from (select * from ( select 1 ) a join (select 2) b) b join (select 3) a;", false},
		{"select * from (select 1 ) a , (select 2) b, (select * from (select 3) a join (select 4) b) c;", false},
		{"select cast(1.23 as decimal(65,65))", true},
		{"select CONVERT( 2, DECIMAL(62,60) )", true},
		{"select CONVERT( 2, DECIMAL(66,29) )", true},
		{"select CONVERT( 2, DECIMAL(28,29) )", true},
		{"select CONVERT( 2, DECIMAL(30,65) )", true},
		{"select CONVERT( 2, DECIMAL(66,99) )", true},
		{"SELECT CAST(1 AS DATETIME(7));", true},
		{"SELECT CAST(1 AS DATETIME(31));", true},
		{"select * from t tablesample bernoulli();", false},
		{"select * from t tablesample bernoulli(10 rows);", false},
		{"select * from t tablesample bernoulli(23 percent) repeatable (23);", false},
		{"select * from t tablesample system() repeatable (10);", false},
		{"SELECT * FROM t, LATERAL (SELECT t.a) AS dt", false},
		{"SELECT * FROM t LEFT JOIN LATERAL (SELECT t.a) AS dt ON true", false},
	} {
		g.Add("TestValidator", "pre", c.sql, c.inPrepare)
	}
	for _, c := range []struct {
		sql       string
		inPrepare bool
	}{
		{"select 1", false},
		{"select 1 from dual", false},
		{"select * from (select 1) a", false},
		{"select * from (select 1)", false},
		{"select * from (select 1) a, (select 2) b, (select 3) c", false},
		{"select * from ((select 1) a join (select 2) b) join (select 3) a", false},
		{"select * from (select 1) a join ((select 2) b join (select 3) b)", false},
		{"select * from (select * from (select 1) x, (select 2) x) y", false},
		{"select (select 1 from (select 2) a, (select 3) a)", false},
		{"select 1 union select 2", false},
		{"select 1 limit 1 union select 2", false},
		{"select 1 order by 1 union select 2", false},
		{"(select 1 limit 1) union select 2", false},
		{"select 1 union select 2 limit 1", false},
		{"select 1 union (select 2 order by 1) union select 3", false},
		{"select sql_calc_found_rows 1", false},
		{"select 1 from (select 1) a lock in share mode", false},
		{"select 1 from (select 1) a group by 1 desc", false},
		{"select 1 from (select 1) a group by 1", false},
		{"select cast(1 as decimal(10,2))", false},
		{"select cast(1 as decimal(65,30))", false},
		{"select cast(1 as decimal(66,0))", false},
		{"select cast(1 as decimal(5,6))", false},
		{"select cast('a' as decimal(65,31))", false},
		{"select cast(1 as datetime(6))", false},
		{"select cast(1 as datetime(7)) from (select 1) a", false},
		{"select convert(1+2, decimal(70,2))", false},
		{"select ? + ?", false},
		{"select ? + ?", true},
		{"select 1 from (select ?) a", false},
		{"select 1 limit ?", false},
		{"select 1 limit ?", true},
		{"select name_const('a', 1)", false},
		{"select name_const('a', -1)", false},
		{"select name_const(1+1, 1)", false},
		{"select name_const('a')", false},
		{"select test.f(1)", false},
		{"select @a, @b := 1", false},
		{"select * from t", false},
		{"select * from test.t", false},
		{"select * from nodb.t", false},
		{"select * from nosuchtable", false},
		{"select * from t, (select 1) t", false},
		{"select * from t a, t a", false},
		{"select * from t, t", false},
		{"select * from test.t, t", false},
		{"with cte as (select 1) select * from cte", false},
		{"with cte as (select 1) select * from cte, cte", false},
		{"with recursive cte(n) as (select 1 union select n+1 from cte where n < 3) select * from cte", false},
		{"select * from (select 1) a where 1 in (select * from (select 2) b, (select 3) b)", false},
	} {
		g.Add("sys/pre", "pre", c.sql, c.inPrepare)
	}
	// Join shapes: which joins share an alias set (parentIsJoin) — nested and parenthesised
	// joins, NATURAL JOIN, USING, outer joins, joins inside derived tables and subqueries.
	for _, sql := range []string{
		"select * from (select 1) a join (select 2) b on 1 join (select 3) a on 1",
		"select * from (select 1) a left join (select 2) a on 1",
		"select * from (select 1) a right join (select 2) b on 1 left join (select 3) b on 1",
		"select * from (select 1) x join ((select 2) y join (select 3) x on 1) on 1",
		"select * from ((select 1) a join (select 2) b on 1) join ((select 3) c join (select 4) d on 1) on 1",
		"select * from ((select 1) a join (select 2) b on 1) join ((select 3) c join (select 4) a on 1) on 1",
		"select * from (select 1) a, ((select 2) b, (select 3) a)",
		"select * from (select 1) a, ((select 2) b, (select 3) c)",
		"select * from (select 1 x) a natural join (select 1 x) b",
		"select * from (select 1 x) a natural join (select 1 x) a",
		"select * from (select 1 x) a natural left join (select 1 x) b natural join (select 1 x) b",
		"select * from (select 1 x) a join (select 1 x) b using (x)",
		"select * from (select 1 x) a join (select 1 x) a using (x)",
		"select * from (select 1 x) a join (select 1 x) b using (x) join (select 1 x) b using (x)",
		"select * from (select 1 x) a join ((select 1 x) b join (select 1 x) c using (x)) using (x)",
		"select * from (select * from (select 1) a join (select 2) a on 1) z",
		"select * from (select * from (select 1) a join (select 2) b on 1) a join (select 3) b on 1",
		"select (select 1 from (select 1) a join (select 2) b on 1) from (select 3) a join (select 4) b on 1",
		"select * from (select 1) a join (select 2) b on (select 1 from (select 3) a join (select 4) a on 1)",
		"select * from t natural join t",
		"select * from t a natural join t b",
		"select * from t join t b using (a)",
		"select * from (t a join t b on 1) join t a on 1",
		"select * from t a join (t b join t a on 1) on 1",
		"select * from test.t join t on 1",
		"select * from test.t a join test.t a on 1",
	} {
		g.Add("sys/join", "pre", sql, false)
	}
}
