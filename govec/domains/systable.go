package domains

// Domain "write_systable": block D06.7, the system-table writes behind SET GLOBAL (tidb-rs
// specs/D05.2-sessionvars/spec.md §6, owner decision tidb-rs #58 (c)).
//
// The oracle is Go itself on a bootstrapped mockstore (session.BootstrapSession): the
// records hold what the real REPLACE executor writes, not a re-implementation of it.
//   - tableinfo: the TableInfo (JSON, as stored in meta) of mysql.global_variables and
//     mysql.tidb, with the database and table IDs.
//   - replace: REPLACE INTO mysql.<table> of one row inside BEGIN OPTIMISTIC (rf 1: on a
//     session of the domain's sysSessionPool, Go's real path, which writes row format 1;
//     rf 2: on a normal session, row format 2), after the
//     given rows were written (committed). The output is the table's rows the statement
//     can read (the index entries of every name it can collide with and their records:
//     "before"), and the transaction's memory buffer over the table's key range before
//     the commit ("muts": key, value (empty = delete), key flags), plus the handle the
//     new record got. The transaction is rolled back and the written rows deleted, so a
//     record does not depend on earlier ones apart from the row-ID allocator's cache.
//   - alloc: a row-ID allocator (autoid.NewAllocator, RowIDAllocType, custom step) over a
//     meta row-ID counter that starts at the given value; a list of Alloc calls, each with
//     its result and the counter afterwards (alloc4Signed).
//   - next_step: autoid.NextStep (the dynamic step of a non-custom allocator).
// tidb-rs replays them with the D06.7 crate (tidb-systable) over an in-memory snapshot.
//
// The name sorts after every other domain on purpose: the records bootstrap a domain in
// the process, whose sysvar cache rebuild runs every SetGlobal hook and keeps running in the
// background (process-wide state other domains read: e.g. the GC tuner's memory-limit
// trigger seen by sysvar's tidb_gogc_tuner_threshold validation). govec_test runs all
// domains in one process in name order; scripts/govec.sh runs one domain per process.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/domain"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta"
	"github.com/pingcap/tidb/pkg/meta/autoid"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/session"
	sessiontypes "github.com/pingcap/tidb/pkg/session/types"
	"github.com/pingcap/tidb/pkg/sessionctx"
	"github.com/pingcap/tidb/pkg/store/mockstore"
	"github.com/pingcap/tidb/pkg/tablecodec"
)

const systableSeed = 5067

func init() {
	vec.Register(&vec.Domain{
		Name:   "write_systable",
		Seed:   systableSeed,
		Blocks: []string{"D06.7"},
		Coverage: map[string]vec.Coverage{
			"tableinfo": {Min: 2},
			"replace": {Min: 60, GoFuncs: []string{"session.replaceGlobalVariablesTableValue",
				"session.SetTiDBTableValue"}},
			"alloc": {Min: 20, GoFuncs: []string{"allocator.alloc4Signed", "allocator.Alloc", "CalcNeededBatchSize",
				"SeekToFirstAutoIDSigned", "validIncrementAndOffset"}},
			"next_step": {Min: 20, GoFuncs: []string{"NextStep"}},
		},
		Exempt: map[string]string{
			"SeekToFirstAutoIDUnSigned":         "unsigned path of CalcNeededBatchSize (with increment > 1): _tidb_rowid is signed",
			"session.getTableValue":             "SELECT through the restricted SQL executor; tidb-rs reads the unique index (replace records cover the index read) and is checked on a cluster (spec §6.6)",
			"session.GetTiDBTableValue":         "getTableValue of mysql.tidb, as above",
			"Domain.LoadSysVarCacheLoop":        "etcd watch / 30 s ticker loop: I/O, checked on a cluster (spec §6.6)",
			"Domain.NotifyUpdateSysVarCache":    "etcd PUT then rebuild: I/O, checked on a cluster (spec §6.6)",
			"Domain.NotifyGlobalConfigChange":   "PD GlobalConfig PUT: I/O, checked on a cluster (spec §6.6)",
			"Domain.fetchTableValues":           "SELECT of mysql.global_variables: tidb-rs decodes the scanned rows; checked on a cluster (spec §6.6)",
			"Domain.rebuildSysVarCache":         "covered by the tidb-rs unit tests of the rebuild against the sysvar registry (sysvar records cover the hooks it runs)",
			"Domain.rebuildSysVarCacheIfNeeded": "as above",
		},
		Gzip:     true,
		Generate: systableGenerate,
		Exec:     systableExec,
	})
}

// systableEnv is the bootstrapped store the records run on (created on first use).
type systableEnvT struct {
	store kv.Storage
	dom   *domain.Domain
	se    sessiontypes.Session
	park  int64 // bootstrap rows parked so far
}

// systableBase is where every replace record starts the table's row-ID allocator.
const systableBase = 1 << 20

// rebase moves the table's row-ID allocator (cache and meta counter) to base. Where
// bootstrap leaves it depends on its concurrency and on wall-clock time (NextStep), and a
// record must not depend on the records before it (the oracle check re-runs single
// records): every replace record starts at systableBase. Rows that outlive a record
// (bootstrap rows it rewrote) are parked far above it.
func (e *systableEnvT) rebase(table string, base int64) {
	t, err := e.dom.InfoSchema().TableByName(context.Background(), pmodel.NewCIStr("mysql"), pmodel.NewCIStr(table))
	if err != nil {
		panic(err)
	}
	if err := t.Allocators(nil).Get(autoid.RowIDAllocType).ForceRebase(base); err != nil {
		panic(err)
	}
}

var (
	systableOnce sync.Once
	systableE    systableEnvT
)

func systableEnv() *systableEnvT {
	systableOnce.Do(func() {
		store, err := mockstore.NewMockStore()
		if err != nil {
			panic(err)
		}
		// As testkit.CreateMockStoreAndDomain.
		session.SetSchemaLease(500 * time.Millisecond)
		session.DisableStats4Test()
		domain.DisablePlanReplayerBackgroundJob4Test()
		domain.DisableDumpHistoricalStats4Test()
		dom, err := session.BootstrapSession(store)
		if err != nil {
			panic(err)
		}
		se, err := session.CreateSession4Test(store)
		if err != nil {
			panic(err)
		}
		systableE = systableEnvT{store: store, dom: dom, se: se}
	})
	return &systableE
}

func (e *systableEnvT) exec(sql string, args ...any) {
	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnSysVar)
	rs, err := e.se.ExecuteInternal(ctx, sql, args...)
	if err != nil {
		panic(fmt.Sprintf("%s: %v", sql, err))
	}
	if rs != nil {
		_ = rs.Close()
	}
}

func (e *systableEnvT) rewriteBootstrapRow(table, name string) (row []string) {
	ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnSysVar)
	cols := "variable_value"
	if table == "tidb" {
		cols = "variable_value, comment"
	}
	rows, _, err := e.se.GetRestrictedSQLExecutor().ExecRestrictedSQL(ctx, nil, "SELECT variable_name, "+cols+" FROM %n.%n WHERE variable_name = %?", "mysql", table, name)
	if err != nil {
		panic(err)
	}
	for _, r := range rows {
		vals := []string{r.GetString(0), r.GetString(1)}
		if table == "tidb" {
			vals = append(vals, r.GetString(2))
		}
		e.exec("DELETE FROM %n.%n WHERE variable_name = %?", "mysql", table, vals[0])
		q, a := systableReplaceSQL(table, vals)
		e.exec(q, a...)
		row = vals
	}
	return row
}

func systableInfo(name string) (dbID int64, info []byte, tblID int64) {
	e := systableEnv()
	is := e.dom.InfoSchema()
	db, ok := is.SchemaByName(pmodel.NewCIStr("mysql"))
	if !ok {
		panic("no mysql schema")
	}
	t, err := is.TableByName(context.Background(), pmodel.NewCIStr("mysql"), pmodel.NewCIStr(name))
	if err != nil {
		panic(err)
	}
	// UpdateTS is the TSO of the bootstrap DDL (wall-clock time): masked.
	m := t.Meta().Clone()
	m.UpdateTS = 0
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return db.ID, b, t.Meta().ID
}

// systableReplaceSQL is the statement Go runs: replaceGlobalVariablesTableValue (two
// columns) or SetTiDBTableValue (three).
func systableReplaceSQL(table string, row []string) (string, []any) {
	if table == "tidb" {
		return "REPLACE INTO mysql.tidb (variable_name, variable_value, comment) VALUES (%?, %?, %?)", []any{row[0], row[1], row[2]}
	}
	return "REPLACE INTO %n.%n (variable_name, variable_value) VALUES (%?, %?)", []any{"mysql", "global_variables", row[0], row[1]}
}

func hexs(b []byte) string { return hex.EncodeToString(b) }

func flagsStr(f kv.KeyFlags) string {
	var s []string
	if f.HasPresumeKeyNotExists() {
		s = append(s, "presume_not_exists")
	}
	if f.HasAssertExists() {
		s = append(s, "assert_exist")
	}
	if f.HasAssertNotExists() {
		s = append(s, "assert_not_exist")
	}
	if f.HasAssertUnknown() {
		s = append(s, "assert_unknown")
	}
	if f.HasNeedConstraintCheckInPrewrite() {
		s = append(s, "constraint_check_in_prewrite")
	}
	if f.HasNeedLocked() {
		s = append(s, "need_locked")
	}
	return strings.Join(s, ",")
}

// systableBefore is what a REPLACE of the given names can read: the unique index entries
// whose name equals one of them after trimming trailing spaces (utf8mb4_bin is PAD SPACE)
// and the records they point to, as committed now.
func systableBefore(tblID int64, names []string) map[string]string {
	e := systableEnv()
	want := map[string]bool{}
	for _, n := range names {
		want[strings.TrimRight(n, " ")] = true
	}
	out := map[string]string{}
	err := kv.RunInNewTxn(kv.WithInternalSourceType(context.Background(), kv.InternalTxnOthers), e.store, false, func(ctx context.Context, txn kv.Transaction) error {
		prefix := tablecodec.EncodeTablePrefix(tblID)
		it, err := txn.Iter(prefix, prefix.PrefixNext())
		if err != nil {
			return err
		}
		defer it.Close()
		var handles []kv.Key
		for it.Valid() && it.Key().HasPrefix(prefix) {
			k := it.Key()
			if tablecodec.IsIndexKey(k) {
				_, _, vals, err := tablecodec.DecodeIndexKey(k)
				if err != nil {
					return err
				}
				if want[strings.TrimRight(vals[0], " ")] {
					out[hexs(k)] = hexs(it.Value())
					h, err := tablecodec.DecodeHandleInIndexValue(it.Value())
					if err != nil {
						return err
					}
					handles = append(handles, tablecodec.EncodeRowKeyWithHandle(tblID, h))
				}
			}
			if err := it.Next(); err != nil {
				return err
			}
		}
		for _, k := range handles {
			v, err := txn.Get(ctx, k)
			if err == nil {
				out[hexs(k)] = hexs(v.Value)
			}
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}

type systableMut struct {
	K     string `json:"k"`
	V     string `json:"v"`
	Flags string `json:"f,omitempty"`
}

func systableExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "tableinfo":
		name := arg[string](args, 0)
		dbID, info, tblID := systableInfo(name)
		return map[string]any{"db_id": dbID, "table_id": tblID, "info": json.RawMessage(info)}, nil
	case "replace":
		e := systableEnv()
		table, init, row := arg[string](args, 0), arg[[][]string](args, 1), arg[[]string](args, 2)
		_, _, tblID := systableInfo(table)
		base := int64(systableBase)
		e.rebase(table, base)
		// A bootstrap row of the name has the handle the (concurrent) bootstrap gave it:
		// write it again with its value so that its handle comes from systableBase.
		boot := e.rewriteBootstrapRow(table, row[0])
		for _, r := range init {
			q, a := systableReplaceSQL(table, r)
			e.exec(q, a...)
		}
		names := []string{row[0]}
		for _, r := range init {
			names = append(names, r[0])
		}
		res := map[string]any{"before": systableBefore(tblID, names)}
		// rf 1 (the real path): a session of the domain's sysSessionPool, as
		// replaceGlobalVariablesTableValue / SetTiDBTableValue use (ExecRestrictedSQL). It
		// never loads the global tidb_row_format_version, so it writes row format 1 with no
		// checksum. rf 2: the test session (row format 2), for the format-2 branch.
		opts := arg[map[string]int64](args, 3)
		var sctx sessionctx.Context = e.se
		if opts["rf"] == 1 {
			res, perr := e.dom.SysSessionPool().Get()
			if perr != nil {
				panic(perr)
			}
			defer e.dom.SysSessionPool().Put(res)
			sctx = res.(sessionctx.Context)
		}
		exec := sctx.GetSQLExecutor()
		ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnSysVar)
		run := func(q string, a ...any) error {
			rs, err := exec.ExecuteInternal(ctx, q, a...)
			if rs != nil {
				_ = rs.Close()
			}
			return err
		}
		if err := run("BEGIN OPTIMISTIC"); err != nil {
			panic(err)
		}
		q, a := systableReplaceSQL(table, row)
		err := run(q, a...)
		var muts []systableMut
		if err == nil {
			txn, terr := sctx.Txn(false)
			if terr != nil {
				panic(terr)
			}
			mb := txn.GetMemBuffer()
			prefix := tablecodec.EncodeTablePrefix(tblID)
			it, ierr := mb.Iter(prefix, prefix.PrefixNext())
			if ierr != nil {
				panic(ierr)
			}
			for it.Valid() {
				k := it.Key().Clone()
				f, _ := mb.GetFlags(k)
				muts = append(muts, systableMut{K: hexs(k), V: hexs(it.Value()), Flags: flagsStr(f)})
				if tablecodec.IsRecordKey(k) && len(it.Value()) > 0 {
					h, derr := tablecodec.DecodeRowKey(k)
					if derr != nil {
						panic(derr)
					}
					res["handle"] = h.IntValue()
					if h.IntValue() <= base {
						panic(fmt.Sprintf("systable: handle %d not above the rebased %d (another allocator?)", h.IntValue(), base))
					}
				}
				if nerr := it.Next(); nerr != nil {
					panic(nerr)
				}
			}
			it.Close()
		}
		res["muts"] = muts
		if err := run("ROLLBACK"); err != nil {
			panic(err)
		}
		for _, r := range init {
			e.exec("DELETE FROM %n.%n WHERE variable_name = %?", "mysql", table, r[0])
		}
		if boot != nil {
			e.exec("DELETE FROM %n.%n WHERE variable_name = %?", "mysql", table, boot[0])
			e.park++
			e.rebase(table, 1<<40+e.park<<8)
			q, a := systableReplaceSQL(table, boot)
			e.exec(q, a...)
		}
		return res, err
	case "alloc":
		e := systableEnv()
		tblID, start, step := arg[int64](args, 0), arg[int64](args, 1), arg[int64](args, 2)
		calls := arg[[][3]int64](args, 3)
		const dbID = 1
		ctx := kv.WithInternalSourceType(context.Background(), kv.InternalTxnMeta)
		if err := kv.RunInNewTxn(ctx, e.store, true, func(_ context.Context, txn kv.Transaction) error {
			acc := meta.NewMutator(txn).GetAutoIDAccessors(dbID, tblID).RowID()
			if err := acc.Del(); err != nil {
				return err
			}
			if start != 0 {
				_, err := acc.Inc(start)
				return err
			}
			return nil
		}); err != nil {
			panic(err)
		}
		alloc := autoid.NewAllocator(systableReq{e.store}, dbID, tblID, false, autoid.RowIDAllocType, autoid.CustomAutoIncCacheOption(step))
		var out []map[string]any
		for _, c := range calls {
			minv, maxv, err := alloc.Alloc(ctx, uint64(c[0]), c[1], c[2])
			var counter int64
			if err := kv.RunInNewTxn(ctx, e.store, false, func(_ context.Context, txn kv.Transaction) error {
				var gerr error
				counter, gerr = meta.NewMutator(txn).GetAutoIDAccessors(dbID, tblID).RowID().Get()
				return gerr
			}); err != nil {
				panic(err)
			}
			out = append(out, map[string]any{"min": minv, "max": maxv, "err": errStr(err), "base": alloc.Base(), "end": alloc.End(), "meta": counter})
		}
		return out, nil
	case "next_step":
		return autoid.NextStep(arg[int64](args, 0), time.Duration(arg[int64](args, 1))), nil
	}
	panic(fmt.Sprintf("systable: unknown op %q", op))
}

// systableReq is the autoid.Requirement of the alloc records (no autoid service: the
// row-ID allocator does not use it).
type systableReq struct{ s kv.Storage }

func (r systableReq) Store() kv.Storage                  { return r.s }
func (systableReq) AutoIDClient() *autoid.ClientDiscover { return nil }

// systableReset drops the store of an earlier generation: each generation (govec_test
// generates twice in one process) starts from a fresh bootstrap, so the row-ID allocator's
// cache, and with it the handles, are the same.
func systableReset() {
	if systableE.dom != nil {
		systableE.se.Close()
		systableE.dom.Close()
		_ = systableE.store.Close()
	}
	systableE = systableEnvT{}
	systableOnce = sync.Once{}
}

func systableGenerate(g *vec.Gen, rng *rand.Rand) {
	systableReset()
	g.Add("sys/tableinfo", "tableinfo", "global_variables")
	g.Add("sys/tableinfo", "tableinfo", "tidb")
	rf1 := map[string]int64{"rf": 1}
	gv := func(init [][]string, row []string) {
		g.Add("sys/replace", "replace", "global_variables", init, row, rf1)
	}
	td := func(init [][]string, row []string) { g.Add("sys/replace", "replace", "tidb", init, row, rf1) }
	// Row format 2 (a session that loaded the global tidb_row_format_version).
	for _, c := range []struct {
		t    string
		init [][]string
		row  []string
	}{
		{"global_variables", nil, []string{"govec_a", "1"}},
		{"global_variables", [][]string{{"govec_a", "1"}}, []string{"govec_a", "2"}},
		{"global_variables", [][]string{{"govec_a", "1"}}, []string{"govec_a", "1"}},
		{"tidb", [][]string{{"govec_t", "true", "c"}}, []string{"govec_t", "false", "c"}},
	} {
		if c.init == nil {
			c.init = [][]string{}
		}
		g.Add("sys/replace_rf2", "replace", c.t, c.init, c.row, map[string]int64{"rf": 2})
	}
	none := [][]string{}
	// New name, existing name (same value: unchanged, no write; other value), case and
	// trailing-space variants of the name (utf8mb4_bin, PAD SPACE) and of the value
	// (binary comparison), empty, long and multi-byte values, bootstrap rows.
	gv(none, []string{"govec_a", "1"})
	gv([][]string{{"govec_a", "1"}}, []string{"govec_a", "1"})
	gv([][]string{{"govec_a", "1"}}, []string{"govec_a", "2"})
	gv([][]string{{"govec_a", "x"}}, []string{"govec_a", "X"})
	gv([][]string{{"govec_a", "x"}}, []string{"govec_a", "x "})
	gv([][]string{{"govec_a", "x"}}, []string{"govec_a ", "x"})
	gv([][]string{{"govec_a", "x"}}, []string{"GOVEC_A", "x"})
	gv([][]string{{"govec_a ", "x"}}, []string{"govec_a", "x"})
	gv([][]string{{"govec_a", ""}}, []string{"govec_a", ""})
	gv(none, []string{"govec_a", ""})
	gv(none, []string{"govec_a", strings.Repeat("v", 16383)})
	gv([][]string{{"govec_a", "1"}}, []string{"govec_a", strings.Repeat("あ", 5000)})
	gv(none, []string{"govec_ü", "ä"})
	gv([][]string{{"govec_a", "1"}, {"govec_b", "2"}}, []string{"govec_b", "3"})
	gv(none, []string{"max_connections", "151"})
	gv(none, []string{"max_connections", "0"})
	gv(none, []string{"tidb_gc_life_time", "10m0s"})
	gv(none, []string{"tidb_mem_quota_query", "1073741824"})
	gv(none, []string{"tidb_mem_quota_query", "2147483648"})
	gv(none, []string{"sql_mode", "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION"})
	gv(none, []string{"sql_mode", ""})
	gv(none, []string{"tidb_row_format_version", "1"})
	td(none, []string{"govec_t", "true", "c"})
	td([][]string{{"govec_t", "true", "c"}}, []string{"govec_t", "true", "c"})
	td([][]string{{"govec_t", "true", "c"}}, []string{"govec_t", "true", "d"})
	td([][]string{{"govec_t", "true", "c"}}, []string{"govec_t", "false", "c"})
	td(none, []string{"tikv_gc_life_time", "10m0s", "All versions within life time will not be collected by GC, at least 10m, in Go format."})
	td(none, []string{"tikv_gc_life_time", "20m0s", "All versions within life time will not be collected by GC, at least 10m, in Go format."})
	td(none, []string{"tikv_gc_enable", "false", "Current GC enable status"})
	td(none, []string{"tikv_gc_auto_concurrency", "true", "Let TiDB pick the concurrency automatically. If set false, tikv_gc_concurrency will be used"})
	td(none, []string{"tikv_gc_scan_lock_mode", "PHYSICAL", "Mode of scanning locks, \"physical\" or \"legacy\""})
	// Random rows (names from a small alphabet so they collide).
	alpha := []string{"a", "b", "A", "a ", "ab", "é"}
	for i := 0; i < 60; i++ {
		var init [][]string
		for j := rng.IntN(3); j > 0; j-- {
			init = append(init, []string{"govec_r" + alpha[rng.IntN(len(alpha))], fmt.Sprint(rng.IntN(3))})
		}
		init = dedupFold(init)
		row := []string{"govec_r" + alpha[rng.IntN(len(alpha))], fmt.Sprint(rng.IntN(3))}
		if i%3 == 0 {
			for k := range init {
				init[k] = append(init[k], "c")
			}
			td(init, append(row, []string{"c", "d"}[rng.IntN(2)]))
		} else {
			gv(init, row)
		}
	}
	// Row-ID allocator: steps, starts near the int64 limit, n > 1.
	tbl := int64(900000)
	for _, c := range []struct {
		start, step int64
		calls       [][3]int64
	}{
		{0, 30000, [][3]int64{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
		{0, 2, [][3]int64{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
		{5, 3, [][3]int64{{1, 1, 1}, {2, 1, 1}, {4, 1, 1}, {1, 1, 1}}},
		{100, 10, [][3]int64{{25, 1, 1}, {1, 1, 1}}},
		{math.MaxInt64 - 10, 4, [][3]int64{{1, 1, 1}, {4, 1, 1}, {4, 1, 1}, {1, 1, 1}}},
		{math.MaxInt64 - 2, 30000, [][3]int64{{1, 1, 1}, {1, 1, 1}, {1, 1, 1}}},
		{math.MaxInt64, 5, [][3]int64{{1, 1, 1}}},
		{0, 5, [][3]int64{{0, 1, 1}, {1, 0, 1}, {1, 1, 70000}}},
		{0, 1, [][3]int64{{1, 1, 1}, {1, 1, 1}}},
	} {
		g.Add("sys/alloc", "alloc", tbl, c.start, c.step, c.calls)
		tbl++
	}
	for i := 0; i < 20; i++ {
		var calls [][3]int64
		for j := 0; j < 1+rng.IntN(6); j++ {
			calls = append(calls, [3]int64{int64(1 + rng.IntN(20)), 1, 1})
		}
		g.Add("sys/alloc", "alloc", tbl, int64(rng.IntN(1000)), int64(1+rng.IntN(40)), calls)
		tbl++
	}
	for _, s := range []int64{1, 1000, 4000, 30000, 2000000, 5000000} {
		for _, d := range []int64{1, int64(time.Millisecond), int64(time.Second), int64(10 * time.Second), int64(30 * time.Second), int64(time.Minute), int64(time.Hour)} {
			g.Add("sys/next_step", "next_step", s, d)
		}
	}
}

// dedupFold drops rows whose name equals an earlier one after trimming trailing spaces
// (they would replace each other while being written).
func dedupFold(rows [][]string) [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, r := range rows {
		k := strings.TrimRight(r[0], " ")
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}
