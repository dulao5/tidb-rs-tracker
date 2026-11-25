package domains

// Domain "resetstmt": `executor.ResetContextOfStmt` (pkg/executor/select.go, block D12.2),
// ported ahead of VS1-35 by tidb-rs VS1-14 (tidb-rs specs/D05.3-session/spec.md §2.4): the
// session calls it before Compile, and it writes the "previous statement" state of the
// VS1 plan §4.5 / §4.6 (SysWarningCount / SysErrorCount, PrevAffectedRows,
// PrevLastInsertID, PrevFoundInPlanCache) and the statement's type flags and error levels.
// This is the function-level oracle of "warning counts equal to Go" (VS1-14 acceptance).
//
// op `reset`: args [cfg, prev, sql]. cfg = {sql_mode, warnings, notes, errors,
// last_insert_id, found_in_plan_cache, skip_utf8_check}; a fresh mock.Context (a bound
// zero Domain: the expensive query handle is nil) first runs ResetContextOfStmt for the
// previous statement `prev` ("" for none), whose context then gets the warnings, notes and
// errors and LastInsertID; FoundInPlanCache is set; then ResetContextOfStmt runs for `sql`.
// Out: the session and statement-context fields it writes (see resetstmtState).

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/domain"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const resetstmtSeed = 20261115

func init() {
	vec.Register(&vec.Domain{
		Name:     "resetstmt",
		Seed:     resetstmtSeed,
		Coverage: resetstmtCoverage,
		Blocks:   []string{"D12.2"},
		Exempt:   resetstmtExempt(),
		Gzip:     true,
		Generate: resetstmtGenerate,
		Exec:     resetstmtExec,
	})
}

var resetstmtCoverage = map[string]vec.Coverage{
	"reset": {Min: 300, GoFuncs: []string{"ResetContextOfStmt", "isWeakConsistencyRead"}},
}

// resetstmtExempt: the rest of D12.2. The basic executors of tidb-rs VS1-35 are the
// basicexec domain's; the others are not in a part yet.
func resetstmtExempt() map[string]string {
	later := "D12.2, not in a part yet (MAX ONE ROW, UNION ALL, TABLESAMPLE, the memory-table scan)"
	write := "write path: tidb-rs is read-only (N11 D8)"
	out := map[string]string{}
	for _, f := range []string{
		"executorBuilder.buildMaxOneRow", "executorBuilder.buildTableSample", "executorBuilder.buildUnionAll",
		"MaxOneRowExec.Next", "MaxOneRowExec.Open", "Start", "Stop",
		"TableScanExec.Next", "TableScanExec.Open", "TableScanExec.nextChunk4InfoSchema",
		"pkg/executor/select.go:init",
		"UnionExec.Close", "UnionExec.Next", "UnionExec.Open", "UnionExec.initialize", "UnionExec.resultPuller",
		"UnionExec.sendResult", "UnionExec.waitAllFinished",
	} {
		out[f] = later
	}
	for _, f := range []string{
		"executorBuilder.buildSelectLock", "ResetDeleteStmtCtx", "ResetUpdateStmtCtx", "SelectLockExec.Next",
		"SelectLockExec.Open", "doLockKeys", "filterLockTableKeys", "filterTemporaryTableKeys", "newLockCtx",
		"DumpRealOutfile", "SelectIntoExec.Close", "SelectIntoExec.Next", "SelectIntoExec.Open",
		"SelectIntoExec.considerEncloseOpt", "SelectIntoExec.dumpToOutfile", "SelectIntoExec.escapeField",
	} {
		out[f] = write
	}
	return out
}

// resetstmtState is the output of a `reset` record.
func resetstmtState(sctx *mock.Context) map[string]any {
	vars := sctx.GetSessionVars()
	sc := vars.StmtCtx
	levels := sc.ErrLevels()
	lv := make([]int, len(levels))
	for i, l := range levels {
		lv[i] = int(l)
	}
	return map[string]any{
		"sys_warning_count":        vars.SysWarningCount,
		"sys_error_count":          vars.SysErrorCount,
		"prev_affected_rows":       sc.PrevAffectedRows,
		"prev_last_insert_id":      sc.PrevLastInsertID,
		"prev_found_in_plan_cache": vars.PrevFoundInPlanCache,
		"found_in_plan_cache":      vars.FoundInPlanCache,
		"in_select":                sc.InSelectStmt,
		"in_show_warning":          sc.InShowWarning,
		"warnings":                 len(sc.GetWarnings()),
		"warning_count":            sc.WarningCount(),
		"type_flags":               uint64(sc.TypeFlags()),
		"err_levels":               lv,
		"priority":                 int(sc.Priority),
		"not_fill_cache":           sc.NotFillCache,
		"weak_consistency":         sc.WeakConsistency,
		"original_sql":             sc.OriginalSQL,
		"in_restricted_sql":        sc.InRestrictedSQL,
	}
}

func resetstmtParse(sql string) (ast.StmtNode, error) {
	return parser.New().ParseOneStmt(sql, "", "")
}

func resetstmtExec(op string, args []json.RawMessage) (any, error) {
	if op != "reset" {
		return nil, errors.Errorf("unknown op %s", op)
	}
	var cfg struct {
		SQLMode          string `json:"sql_mode"`
		Warnings         int    `json:"warnings"`
		Notes            int    `json:"notes"`
		Errors           int    `json:"errors"`
		LastInsertID     uint64 `json:"last_insert_id"`
		FoundInPlanCache bool   `json:"found_in_plan_cache"`
		SkipUTF8Check    bool   `json:"skip_utf8_check"`
	}
	var prev, sql string
	mustUnmarshal(args[0], &cfg)
	mustUnmarshal(args[1], &prev)
	mustUnmarshal(args[2], &sql)
	sctx := mock.NewContext()
	sctx.BindDomain(&domain.Domain{})
	vars := sctx.GetSessionVars()
	vars.ConnectionID = 1
	mode, err := mysql.GetSQLMode(cfg.SQLMode)
	if err != nil {
		return nil, err
	}
	vars.SQLMode = mode
	vars.SkipUTF8Check = cfg.SkipUTF8Check
	if prev != "" {
		node, err := resetstmtParse(prev)
		if err != nil {
			return nil, err
		}
		node.SetText(nil, prev)
		if err := executor.ResetContextOfStmt(sctx, node); err != nil {
			return nil, err
		}
	}
	sc := vars.StmtCtx
	for i := 0; i < cfg.Warnings; i++ {
		sc.AppendWarning(fmt.Errorf("w%d", i))
	}
	for i := 0; i < cfg.Notes; i++ {
		sc.AppendNote(fmt.Errorf("n%d", i))
	}
	for i := 0; i < cfg.Errors; i++ {
		sc.AppendError(fmt.Errorf("e%d", i))
	}
	sc.LastInsertID = cfg.LastInsertID
	vars.FoundInPlanCache = cfg.FoundInPlanCache
	node, err := resetstmtParse(sql)
	if err != nil {
		return nil, err
	}
	node.SetText(nil, sql)
	if err := executor.ResetContextOfStmt(sctx, node); err != nil {
		return nil, err
	}
	return resetstmtState(sctx), nil
}

// resetstmtStmts are the statements of the Rust AST (SELECT, set operations, SHOW, BEGIN,
// COMMIT, ROLLBACK, USE, SET): the others are a boundary stub there (D02.2).
var resetstmtStmts = []string{
	"SELECT 1",
	"SELECT a FROM t WHERE b = 1",
	"SELECT SQL_NO_CACHE a FROM t",
	"SELECT SQL_CACHE a FROM t",
	"SELECT HIGH_PRIORITY a FROM t",
	"SELECT HIGH_PRIORITY SQL_NO_CACHE a FROM t LIMIT 1",
	"SELECT DISTINCT a FROM t",
	"SELECT 1 UNION SELECT 2",
	"SELECT a FROM t UNION ALL SELECT b FROM u",
	"SHOW WARNINGS",
	"SHOW ERRORS",
	"SHOW DATABASES",
	"SHOW TABLES",
	"BEGIN",
	"COMMIT",
	"ROLLBACK",
	"USE test",
	"SET @a = 1",
	"SET NAMES utf8mb4",
}

var resetstmtModes = []string{
	"", "STRICT_TRANS_TABLES", "ALLOW_INVALID_DATES", "STRICT_ALL_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE",
	"ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION",
	"ALLOW_INVALID_DATES,ERROR_FOR_DIVISION_BY_ZERO",
}

func resetstmtGenerate(g *vec.Gen, rng *rand.Rand) {
	base := map[string]any{"sql_mode": "", "warnings": 0, "notes": 0, "errors": 0,
		"last_insert_id": 0, "found_in_plan_cache": false, "skip_utf8_check": false}
	// Every statement after nothing and after each statement.
	for _, sql := range resetstmtStmts {
		g.Add("sys/reset/first", "reset", base, "", sql)
		for _, prev := range resetstmtStmts {
			g.Add("sys/reset/pair", "reset", base, prev, sql)
		}
	}
	// The counts of the previous statement.
	for _, c := range [][3]int{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {2, 1, 3}, {65, 0, 0}} {
		cfg := map[string]any{"sql_mode": "", "warnings": c[0], "notes": c[1], "errors": c[2],
			"last_insert_id": 0, "found_in_plan_cache": false, "skip_utf8_check": false}
		for _, sql := range []string{"SELECT 1", "SHOW WARNINGS", "SHOW ERRORS", "SET @a = 1"} {
			g.Add("sys/reset/counts", "reset", cfg, "SELECT 1", sql)
		}
	}
	for i := 0; i < 300; i++ {
		cfg := map[string]any{
			"sql_mode":            resetstmtModes[rng.IntN(len(resetstmtModes))],
			"warnings":            rng.IntN(4),
			"notes":               rng.IntN(3),
			"errors":              rng.IntN(3),
			"last_insert_id":      []uint64{0, 0, 7, 1 << 40}[rng.IntN(4)],
			"found_in_plan_cache": rng.IntN(2) == 0,
			"skip_utf8_check":     rng.IntN(4) == 0,
		}
		prev := ""
		if rng.IntN(5) > 0 {
			prev = resetstmtStmts[rng.IntN(len(resetstmtStmts))]
		}
		g.Add(fmt.Sprintf("rand/reset/%d", i), "reset", cfg, prev, resetstmtStmts[rng.IntN(len(resetstmtStmts))])
	}
}
