package domains

// Domain "fastplan": block D10.8 (tidb-rs VS1-11), the point get fast path of
// pkg/planner/core/point_get_plan.go, and the unique-index key encoding of D09.0
// (EncodeUniqueIndexKey / EncodeUniqueIndexValuesForKey, pkg/planner/core/util.go), which
// VS1-11 ports for the point get executor.
//
// Ops:
//
//   - "fast": args [ctx, sess, tables, sql]. A SELECT is parsed, preprocessed against
//     MockInfoSchema(tables) (database "test") and given to core.TryFastPlan. out is
//     {"pre": msg} when parsing or preprocessing fails (tidb-rs skips the record), null
//     when TryFastPlan returns no plan, or the plan: {"tp": "Point_Get", ...} with every
//     field the executor and EXPLAIN read (handle / index values, schema columns with their
//     UniqueID, output names, the privilege inputs, the plan ID, MemoryUsage, the statement
//     tables), {"tp": "Batch_Point_Get"} or {"tp": "TableDual"} (tidb-rs: boundary stubs).
//     "w" lists the statement warnings. ctx is the castCtx form [typeFlags, [7]errLevels,
//     maxAllowedPacket, sqlMode, now] plus a location (the session's expression context);
//     sess is {db, select_limit, stable, fix52592, invisible, autocommit, rc, conn, trace,
//     nc}: the session variables TryFastPlan reads, nc the new-collation switch.
//   - "uidx": args [nc, ctx, tz, table, index, values, tid, handle, pid, partition names]:
//     EncodeUniqueIndexKey (and EncodeUniqueIndexValuesForKey) of the values in a mock
//     session whose statement context has the type flags, error levels and time zone, and
//     whose SQL mode is ctx's. out: the key, the error (ErrNotExist when the values cannot
//     be in the index), the casted values; then what the point get executor reads for the
//     row: an index value holding the handle (and the partition ID of a global index), the
//     handle and partition ID decoded from it (tablecodec.DecodeHandleInIndexValue,
//     SplitIndexValue), the row key, a stored row and the row decoded for the table's public
//     columns (executor.DecodeRowValToChunk), and the inconsistency error of an index entry
//     without its row (consistency.Reporter.ReportLookupInconsistent). tidb-rs replays it
//     through the point get executor.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	gotime "time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/ddl"
	"github.com/pingcap/tidb/pkg/errctx"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/infoschema"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/table/tables"
	"github.com/pingcap/tidb/pkg/tablecodec"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/logutil/consistency"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/rowcodec"
)

const fastplanSeed = 20261117

func init() {
	vec.Register(&vec.Domain{
		Name:     "fastplan",
		Seed:     fastplanSeed,
		Errors:   append(append([]vec.NamedErr{}, datumErrors...), tableErrors...),
		Coverage: fastplanCoverage,
		Blocks:   []string{"D10.8", "D09.0"},
		Exempt:   fastplanExempt(),
		Gzip:     true,
		Generate: fastplanGenerate,
		Exec:     fastplanExec,
	})
}

var fastplanCoverage = map[string]vec.Coverage{
	"fast": {Min: 600, GoFuncs: []string{"TryFastPlan", "tryPointGetPlan", "checkTblIndexForPointPlan",
		"indexIsAvailableByHints", "newPointGetPlan", "checkFastPlanPrivilege", "buildSchemaFromFields",
		"tryExtractRowChecksumColumn", "getSingleTableNameAndAlias", "getNameValuePairs", "checkCanConvertInPointGet",
		"findPKHandle", "getIndexValues", "findInPairs", "findCol", "colInfoToColumn", "getVisitInfo", "getLockWaitTime",
		"pairsToColumnNames", "PointGetPlan.Schema", "PointGetPlan.OutputNames", "PointGetPlan.MemoryUsage",
		"PointGetPlan.StatsInfo", "PointGetPlan.OperatorInfo", "PointGetPlan.StatsCount",
		"PointGetPlan.GetEstRowCountForDisplay"}},
	"uidx": {Min: 1500, GoFuncs: []string{"EncodeUniqueIndexKey", "EncodeUniqueIndexValuesForKey"}},
}

// fpCtx: a mock session whose GetExprCtx is the record's static expression context (as
// pgCtx of the pointget domain).
type fpCtx = pgCtx

// fpSess: the session variables TryFastPlan reads.
type fpSess struct {
	DB          string  `json:"db"`
	SelectLimit *uint64 `json:"select_limit,omitempty"`
	Stable      bool    `json:"stable,omitempty"`
	Fix52592    string  `json:"fix52592,omitempty"`
	Invisible   bool    `json:"invisible,omitempty"`
	NoAuto      bool    `json:"no_autocommit,omitempty"`
	RC          bool    `json:"rc,omitempty"`
	Conn        uint64  `json:"conn,omitempty"`
	Trace       bool    `json:"trace,omitempty"`
	NC          bool    `json:"nc"`
}

func fpSetCollation(nc bool) { collate.SetNewCollationEnabledForTest(nc) }

// fpField reads an unexported field of a struct pointer.
func fpField(p any, name string) any {
	v := reflect.ValueOf(p).Elem().FieldByName(name)
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Interface()
}

func fpCI(s pmodel.CIStr) []string { return []string{s.O, s.L} }

func fpNames(ns types.NameSlice) []any {
	out := []any{}
	for _, n := range ns {
		out = append(out, map[string]any{"db": fpCI(n.DBName), "otbl": fpCI(n.OrigTblName), "tbl": fpCI(n.TblName),
			"ocol": fpCI(n.OrigColName), "col": fpCI(n.ColName)})
	}
	return out
}

func fpPlanOut(sctx fpCtx, p any) any {
	switch x := p.(type) {
	case nil:
		return nil
	case *core.BatchPointGetPlan:
		return map[string]any{"tp": "Batch_Point_Get"}
	case *core.PhysicalTableDual:
		return map[string]any{"tp": "TableDual"}
	case *core.PointGetPlan:
		o := map[string]any{"tp": "Point_Get", "id": x.ID(), "db": fpField(x, "dbName").(string),
			"tbl": x.TblInfo.ID, "unsigned": x.UnsignedHandle, "handle_col_offset": x.HandleColOffset,
			"table_dual": x.IsTableDual, "lock": x.Lock, "lock_wait": x.LockWaitTime,
			"index_values": datumOuts(x.IndexValues), "mem": x.MemoryUsage(), "rows": x.StatsInfo().RowCount,
			"est_rows": x.GetEstRowCountForDisplay(), "stats_count": x.StatsCount(),
			"operator_info": x.OperatorInfo(false), "operator_info_norm": x.OperatorInfo(true),
			"plan_col_id": sctx.GetSessionVars().PlanColumnID.Load()}
		if x.Handle != nil {
			o["handle"] = x.Handle.IntValue()
		}
		if ft := fpField(x, "handleFieldType").(*types.FieldType); ft != nil {
			o["handle_ft"] = d2FtOut(ft)
		}
		if x.IndexInfo != nil {
			o["index"] = x.IndexInfo.Name.L
		}
		fts := []any{}
		for _, ft := range x.ColsFieldType {
			fts = append(fts, d2FtOut(ft))
		}
		o["cols_ft"] = fts
		pns := []string{}
		for _, n := range x.PartitionNames {
			pns = append(pns, n.L)
		}
		o["partition_names"] = pns
		if x.PartitionIdx != nil {
			o["partition_idx"] = *x.PartitionIdx
		}
		cols := []any{}
		for _, c := range x.Schema().Columns {
			cols = append(cols, map[string]any{"id": c.ID, "uid": c.UniqueID, "idx": c.Index, "orig": c.OrigName,
				"ft": d2FtOut(c.RetType)})
		}
		o["schema"] = cols
		o["names"] = fpNames(x.OutputNames())
		o["unfold"] = fpField(x, "unfoldFromWildCard").([]bool)
		where := fpField(x, "colsInWhereClause").([]string)
		if where == nil {
			where = []string{}
		}
		o["where_cols"] = where
		tbls := []any{}
		for _, t := range sctx.GetSessionVars().StmtCtx.Tables {
			tbls = append(tbls, []string{t.DB, t.Table})
		}
		o["stmt_tables"] = tbls
		return o
	}
	return map[string]any{"tp": fmt.Sprintf("%T", p)}
}

func fpWarns(sctx fpCtx) []any {
	out := []any{}
	for _, w := range sctx.GetSessionVars().StmtCtx.GetWarnings() {
		out = append(out, vec.GoString(w.Err.Error()))
	}
	return out
}

func fastplanExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "fast":
		var sess fpSess
		must(json.Unmarshal(args[1], &sess))
		var tjs []string
		must(json.Unmarshal(args[2], &tjs))
		sql := string(arg[vec.GoString](args, 3))
		fpSetCollation(sess.NC)
		defer fpSetCollation(true)
		var tbls []*model.TableInfo
		for _, s := range tjs {
			t := &model.TableInfo{}
			must(json.Unmarshal([]byte(s), t))
			tbls = append(tbls, t)
		}
		ectx, _ := tblCtx(args[0])
		sctx := fpCtx{Context: mock.NewContext(), ectx: ectx}
		vars := sctx.GetSessionVars()
		vars.CurrentDB = sess.DB
		if sess.SelectLimit != nil {
			vars.SelectLimit = *sess.SelectLimit
		}
		vars.EnableStableResultMode = sess.Stable
		if sess.Fix52592 != "" {
			vars.OptimizerFixControl = map[uint64]string{52592: sess.Fix52592}
		}
		vars.OptimizerUseInvisibleIndexes = sess.Invisible
		vars.SetStatusFlag(mysql.ServerStatusAutocommit, !sess.NoAuto)
		if sess.RC {
			vars.TxnCtx.Isolation = ast.ReadCommitted
		}
		vars.ConnectionID = sess.Conn
		vars.StmtCtx.EnableOptimizeTrace = sess.Trace
		stmt, err := parser.New().ParseOneStmt(sql, "", "")
		if err != nil {
			return map[string]any{"pre": vec.GoString(err.Error())}, nil
		}
		nodeW := resolve.NewNodeW(stmt)
		is := infoschema.MockInfoSchema(tbls)
		if err := core.Preprocess(context.Background(), sctx, nodeW,
			core.WithPreprocessorReturn(&core.PreprocessorReturn{InfoSchema: is})); err != nil {
			return map[string]any{"pre": vec.GoString(err.Error())}, nil
		}
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": vec.GoString(fmt.Sprint(p))}
				}
			}()
			p := core.TryFastPlan(sctx, nodeW)
			if p == nil {
				r = nil
				return
			}
			r = fpPlanOut(sctx, p)
		}()
		return map[string]any{"p": r, "w": fpWarns(sctx)}, nil
	case "uidx":
		fpSetCollation(arg[int](args, 0) == 1)
		defer fpSetCollation(true)
		ca := exArr(args[1])
		var lv []int
		must(json.Unmarshal(ca[1], &lv))
		var lm errctx.LevelMap
		for i := range lm {
			lm[i] = errctx.Level(lv[i])
		}
		loc := gotime.FixedZone("Z", arg[int](args, 2))
		tbl := tblIn(args[3])
		idx := tbl.FindIndexByName(arg[string](args, 4))
		var raws [][]json.RawMessage
		must(json.Unmarshal(args[5], &raws))
		tid := arg[int64](args, 6)
		h := handleOfSpec(arg[handleSpec](args, 7))
		var pid *int64
		must(json.Unmarshal(args[8], &pid))
		sctx := mock.NewContext()
		sc := stmtctx.NewStmtCtxWithTimeZone(loc)
		sc.SetTypeFlags(types.Flags(arg[uint16](ca, 0)))
		sc.SetErrLevels(lm)
		sctx.GetSessionVars().StmtCtx = sc
		sctx.GetSessionVars().SQLMode = mysql.SQLMode(arg[uint64](ca, 2))
		vals := func() []types.Datum {
			out := make([]types.Datum, len(raws))
			for i, r := range raws {
				out[i] = d2DatumIn(r)
			}
			return out
		}
		res := map[string]any{}
		errOut := func(e error) any {
			if e == nil {
				return nil
			}
			if kv.ErrNotExist.Equal(e) {
				return "ErrNotExist"
			}
			return vec.GoString(errors.Cause(e).Error())
		}
		panicked := func(f func()) (p any) {
			defer func() {
				if r := recover(); r != nil {
					p = vec.GoString(fmt.Sprint(r))
				}
			}()
			f()
			return nil
		}
		v1 := vals()
		var key []byte
		var kerr error
		if p := panicked(func() { key, kerr = core.EncodeUniqueIndexKey(sctx, tbl, idx, v1, tid) }); p != nil {
			res["key_panic"] = p
			return res, nil
		}
		res["key"] = hex.EncodeToString(key)
		res["key_err"] = errOut(kerr)
		res["key_vals"] = datumOuts(v1)
		v2 := vals()
		var vfk []byte
		var verr error
		if p := panicked(func() { vfk, verr = core.EncodeUniqueIndexValuesForKey(sctx, tbl, idx, v2) }); p != nil {
			res["vfk_panic"] = p
			return res, nil
		}
		res["vfk"] = hex.EncodeToString(vfk)
		res["vfk_err"] = errOut(verr)
		res["w"] = ecWarns(sc.GetWarnings())
		if kerr != nil || verr != nil {
			return res, nil
		}
		rowTID := tid
		var dh kv.Handle
		if tbl.IsCommonHandle && idx.Primary {
			// isCommonHandleRead: the handle is the encoded values, no index entry is read.
			ch, err := kv.NewCommonHandle(vfk)
			if err != nil {
				res["ch_err"] = vec.GoString(err.Error())
				return res, nil
			}
			dh = ch
			res["dh"] = specOf(dh)
		} else {
			var stop bool
			dh, rowTID, stop = fpIndexEntry(res, loc, tbl, idx, v1, h, pid, tid)
			if stop {
				return res, nil
			}
		}
		fpRow(res, loc, tbl, idx, v1, dh, rowTID, uint64(tid)+uint64(len(key)))
		return res, nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// fpIndexEntry: the index value of a unique index (the handle, and the partition ID of a
// global index, in the layout TiDB writes), and the handle and row table ID the point get
// reads from it.
func fpIndexEntry(res map[string]any, loc *gotime.Location, tbl *model.TableInfo, idx *model.IndexInfo, v1 []types.Datum,
	h kv.Handle, pid *int64, tid int64) (kv.Handle, int64, bool) {
	pidV := int64(0)
	if pid != nil {
		pidV = *pid
	}
	// With the new collations a string column of the index needs its original value in the
	// index value (restored data, after the handle); the point get skips it.
	needRestored := tables.NeedRestoredData(idx.Columns, tbl.Columns)
	res["restored"] = needRestored
	ival, err := tablecodec.GenIndexValuePortal(loc, tbl, idx, needRestored, true, false, v1, h, pidV, nil, nil)
	if err != nil {
		res["ival_err"] = vec.GoString(err.Error())
		return nil, 0, true
	}
	res["ival"] = hex.EncodeToString(ival)
	dh, err := tablecodec.DecodeHandleInIndexValue(ival)
	if err != nil {
		res["dh_err"] = vec.GoString(err.Error())
		return nil, 0, true
	}
	res["dh"] = specOf(dh)
	rowTID := tid
	if idx.Global {
		_, p, err := codec.DecodeInt(tablecodec.SplitIndexValue(ival).PartitionID)
		if err != nil {
			res["pid_err"] = vec.GoString(err.Error())
			return nil, 0, true
		}
		res["pid"] = p
		rowTID = p
	}
	ierr := (&consistency.Reporter{Tbl: tbl, Idx: idx, EnableRedactLog: "OFF"}).ReportLookupInconsistent(
		context.Background(), 1, 0, []kv.Handle{dh}, []kv.Handle{dh}, []consistency.RecordData{{}})
	res["inconsistent"] = vec.GoString(ierr.Error())
	return dh, rowTID, false
}

// fpRow: the row key, a stored row (the index columns hold the casted values, the others
// random values) and the row decoded for the table's public columns.
func fpRow(res map[string]any, loc *gotime.Location, tbl *model.TableInfo, idx *model.IndexInfo, v1 []types.Datum,
	dh kv.Handle, rowTID int64, seed uint64) {
	res["rowkey"] = hex.EncodeToString(tablecodec.EncodeRowKeyWithHandle(rowTID, dh))
	{
		rng := rand.New(rand.NewPCG(seed, 7))
		var rv []types.Datum
		var ids []int64
		for _, c := range tbl.Columns {
			if tbl.PKIsHandle && mysql.HasPriKeyFlag(c.GetFlag()) {
				continue
			}
			d := fpDatumFor(rng, c)
			for i, ic := range idx.Columns {
				if ic.Offset == c.Offset {
					d = v1[i]
				}
			}
			rv = append(rv, d)
			ids = append(ids, c.ID)
		}
		enc := rowcodec.Encoder{Enable: true}
		row, err := tablecodec.EncodeRow(loc, rv, ids, nil, nil, nil, &enc)
		if err != nil {
			res["row_err"] = vec.GoString(err.Error())
			return
		}
		res["row"] = hex.EncodeToString(row)
		var cols []pgCol
		for i, c := range tbl.Columns {
			if c.State == model.StatePublic {
				cols = append(cols, pgCol{Off: i})
			}
		}
		schema := pgSchema(tbl, cols)
		ectx, _ := tblCtx(rawArg([]any{[]any{uint16(types.StrictFlags), make([]int, 7), 67108864, 0, 0}, nil}))
		dctx := pgCtx{Context: mock.NewContext(), ectx: ectx}
		dctx.GetSessionVars().TimeZone = gotime.UTC
		rd := executor.NewRowDecoder(dctx, schema, tbl)
		fts := make([]*types.FieldType, len(schema.Columns))
		for i, c := range schema.Columns {
			fts[i] = c.RetType
		}
		chk := chunk.NewChunkWithCapacity(fts, 1)
		if err := executor.DecodeRowValToChunk(dctx, schema, tbl, dh, row, chk, rd); err != nil {
			res["dec_err"] = vec.GoString(errors.Cause(err).Error())
			return
		}
		var ds []any
		for i, ft := range fts {
			ds = append(ds, d2DatumOut(chk.GetRow(0).GetDatum(i, ft)))
		}
		res["dec"] = ds
	}
}

// ---------------------------------------------------------------- "fast" records

// fpTables: the schema of the "fast" records (test.*), from CREATE TABLE statements and
// edits for what DDL cannot state directly (non-public states, partitions with a global
// index, a partial index).
func fpTables() []*model.TableInfo {
	ddls := []string{
		"create table t1 (a int primary key, b int, c varchar(20), d int, e int, unique key ub(b), unique key ucd(c, d), key kd(d))",
		"create table t2 (a bigint unsigned primary key, b char(10) collate utf8mb4_general_ci, c varchar(10) collate utf8mb4_unicode_ci, unique key ub(b), unique key uc(c))",
		"create table t3 (a int, b varchar(10) collate utf8mb4_bin, c int, d varchar(10), unique key ua(a), unique key ub(b(3)), unique key ud(d))",
		"create table t4 (a varchar(10) collate utf8mb4_general_ci, b int, c int, d int, primary key (a, b) clustered, unique key uc(c))",
		"create table t5 (a int primary key, b int as (a + 1), c int, unique key uc(c))",
		"create table t6 (a int primary key, b int, c int, unique key ub(b) invisible, unique key uc(c))",
		"create table t7 (a int primary key, e enum('x','y','z'), bt bit(8), dc decimal(10,2), f double, dt datetime, y year, ts timestamp, unique key ue(e), unique key ubt(bt), unique key udc(dc), unique key uf(f), unique key udt(dt), unique key uy(y), unique key uts(ts))",
		"create table t8 (a int primary key, b binary(5), c char(5) collate binary, d varbinary(5), unique key ub(b), unique key uc(c), unique key ud(d))",
		"create table t9 (a int, b int, c int, unique key ub(b), unique key uc(c))",
		"create table t10 (a int primary key, b int, c int, unique key ub(b))",
		"create table t11 (a int primary key, b int, c int, unique key ub(b), unique key uc(c))",
		"create table t12 (a int, b int, c int, unique key ub(b))",
		"create table t13 (a bigint primary key, b tinyint, c smallint unsigned, d mediumint, unique key ub(b), unique key uc(c), unique key ud(d))",
		"create table t14 (a varchar(10) primary key nonclustered, b int, unique key ub(b))",
		"create table t15 (id int primary key, name varchar(10) collate utf8mb4_0900_ai_ci, k int, unique key un(name), unique key uk(k, name))",
	}
	p := parser.New()
	sctx := mock.NewContext()
	var out []*model.TableInfo
	for i, s := range ddls {
		st, err := p.ParseOneStmt(s, "", "")
		must(err)
		t, err := ddl.MockTableInfo(sctx, st.(*ast.CreateTableStmt), int64(100+i*10))
		must(err)
		t.State = model.StatePublic
		for _, idx := range t.Indices {
			idx.State = model.StatePublic
			idx.ID = int64(len(out)*10 + int(idx.ID))
		}
		switch t.Name.L {
		case "t9":
			// Partitioned by hash(a), ub global.
			t.Partition = &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Expr: "`a`", Num: 2, Enable: true,
				Definitions: []model.PartitionDefinition{{ID: 191, Name: pmodel.NewCIStr("p0")}, {ID: 192, Name: pmodel.NewCIStr("p1")}}}
			t.Indices[0].Global = true
		case "t10":
			t.Columns[2].State = model.StateWriteOnly
		case "t11":
			t.Indices[0].State = model.StateWriteReorganization
		case "t12":
			t.Partition = &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Expr: "`a`", Num: 2, Enable: true,
				Definitions:       []model.PartitionDefinition{{ID: 221, Name: pmodel.NewCIStr("p0")}, {ID: 222, Name: pmodel.NewCIStr("p1")}},
				AddingDefinitions: []model.PartitionDefinition{{ID: 223, Name: pmodel.NewCIStr("p2")}}}
			t.Indices[0].Global = true
		}
		out = append(out, t)
	}
	return out
}

func fastplanGenerate(g *vec.Gen, rng *rand.Rand) {
	tbls := fpTables()
	// The tables a statement names (by word): the record carries only those.
	tjsOf := func(sql string) []string {
		words := map[string]bool{}
		for _, w := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(sql, -1) {
			words[strings.ToLower(w)] = true
		}
		out := []string{}
		for _, t := range tbls {
			if words[t.Name.L] {
				out = append(out, tblJSON(t))
			}
		}
		return out
	}
	ctxOf := func(flags uint16) []any {
		return []any{[]any{flags, make([]int, 7), 67108864, uint64(0), int64(1710063015123456789)}, nil}
	}
	strict := uint16(types.StrictFlags)
	lim := uint64(10)
	sessions := []fpSess{
		{DB: "test", NC: true},
		{DB: "test", NC: false},
		{DB: "other", NC: true},
	}
	special := []fpSess{
		{DB: "test", NC: true, SelectLimit: &lim},
		{DB: "test", NC: true, Stable: true},
		{DB: "test", NC: true, Fix52592: "ON"},
		{DB: "test", NC: true, Fix52592: "OFF"},
		{DB: "test", NC: true, Invisible: true},
		{DB: "test", NC: true, NoAuto: true},
		{DB: "test", NC: true, RC: true},
		{DB: "test", NC: true, RC: true, Conn: 7},
		{DB: "test", NC: true, Conn: 7},
		{DB: "test", NC: true, Trace: true},
	}
	sqls := []string{
		// int handle
		"select * from t1 where a = 1", "select a, b from t1 where a = 1", "select b, a from t1 where 1 = a",
		"select * from t1 where a = '1'", "select * from t1 where a = '1x'", "select * from t1 where a = 1.0",
		"select * from t1 where a = 1.5", "select * from t1 where a = 99999999999", "select * from t1 where a = -1",
		"select * from t1 where a = null", "select * from t1 where a is null", "select * from t1 where a = 1 and b = 2",
		"select * from t1 where a = 1 and a = 2", "select * from t1 where a = 1 or a = 2", "select * from t1 where (a = 1)",
		"select * from t1 where a = b", "select * from t1 where a = 1 + 1", "select * from t1 where a in (1)",
		"select * from t1 where a in (1, 2)", "select * from t1 where a not in (1, 2)", "select * from t1 where (a, b) in ((1, 2))",
		"select * from t1 where a = 1 limit 1", "select * from t1 where a = 1 limit 0", "select * from t1 where a = 1 limit 1, 1",
		"select * from t1 where a = 1 limit 2", "select * from t1 where a = 1 order by a", "select * from t1 where a = 1 group by a",
		"select * from t1 where a = 1 having a > 0", "select distinct a from t1 where a = 1",
		"select * from t1 where a = 1 for update", "select * from t1 where a = 1 for update nowait",
		"select * from t1 where a = 1 for update wait 3", "select * from t1 where a = 1 for share",
		"select * from t1 where a = 1 lock in share mode", "select * from t1 where a = 1 for share nowait",
		"select * from t1 where a = 1 for update skip locked",
		"select a as x, b y from t1 where a = 1", "select t1.a from t1 where t1.a = 1", "select x.a from t1 x where x.a = 1",
		"select t1.a from t1 x where x.a = 1", "select x.* from t1 x where a = 1", "select t1.* from t1 x where a = 1",
		"select *, a from t1 where a = 1", "select test.t1.a from t1 where a = 1", "select a from test.t1 where test.t1.a = 1",
		"select a from t1 where other.t1.a = 1", "select a + 1 from t1 where a = 1", "select count(*) from t1 where a = 1",
		"select a from t1 where a = 1 into outfile '/tmp/x'", "select * from t1, t2 where t1.a = 1",
		"select * from t1 join t2 on t1.a = t2.a where t1.a = 1", "select * from (select * from t1) x where a = 1",
		"select * from t1 where a = (select 1)", "select tidb_row_checksum() from t1 where a = 1",
		"select a, tidb_row_checksum() as ck from t1 where a = 1", "select tidb_row_checksum(1) from t1 where a = 1",
		"select _tidb_rowid from t1 where a = 1", "select * from t1 where _tidb_rowid = 1", "select * from t1 where nosuch = 1",
		"select * from t1 use index(ub) where a = 1", "select * from t1 use index(primary) where a = 1",
		"select * from t1 ignore index(primary) where a = 1", "select * from t1 force index(ub) where b = 1",
		"select * from t1 ignore index(ub) where b = 1", "select * from t1 use index() where a = 1",
		"select * from t1 use index for order by (ub) where a = 1", "select * from t1 ignore index(kd) where a = 1",
		"select /*+ use_index(t1, ub) */ * from t1 where a = 1", "select /*+ ignore_index(t1, primary) */ * from t1 where a = 1",
		"select /*+ use_index(t1, primary) */ * from t1 where a = 1", "select /*+ force_index(t1, ub) */ * from t1 where b = 1",
		"select /*+ ignore_index(test.t1, ub) */ * from t1 where b = 1", "select /*+ ignore_index(x, ub) */ * from t1 x where b = 1",
		"select /*+ max_execution_time(10) */ * from t1 where a = 1",
		// unique indexes
		"select * from t1 where b = 1", "select * from t1 where b = '1'", "select * from t1 where b = null", "select * from t1 where c = 'x' and d = 1",
		"select * from t1 where d = 1 and c = 'x'", "select * from t1 where c = 'x'", "select * from t1 where c = 'x' and d = null",
		"select * from t1 where c = 1 and d = 1", "select * from t1 where c = 'x' and d = 1 and e = 2", "select * from t1 where d = 1",
		"select * from t1 where b = 1 and c = 'x' and d = 1", "select * from t1 where c = 'xxxxxxxxxxxxxxxxxxxxxxxxx' and d = 1",
		"select * from t1 where b = 1 and b = 2", "select * from t1 where b = 99999999999", "select * from t1 where b = 1 and c = 'x'",
		"select * from t2 where a = 1", "select * from t2 where a = -1", "select * from t2 where a = 18446744073709551615",
		"select * from t2 where a = '18446744073709551616'", "select * from t2 where b = 'abc'", "select * from t2 where b = 'ABC  '",
		"select * from t2 where b = 'abc' collate utf8mb4_bin", "select * from t2 where b = x'616263'", "select * from t2 where b = 0x61",
		"select * from t2 where b = 1", "select * from t2 where b = 1.5", "select * from t2 where c = 'ß'", "select * from t2 where c = 'abcdefghijklm'",
		"select * from t2 where b = _binary'abc'", "select * from t2 where b = _latin1'abc'", "select * from t2 where b = true",
		"select * from t3 where a = 1", "select * from t3 where _tidb_rowid = 1", "select * from t3 where _tidb_rowid = '2'",
		"select a, _tidb_rowid from t3 where _tidb_rowid = 1", "select * from t3 where b = 'abc'", "select * from t3 where d = 'abc'",
		"select * from t3 where d = 'ABC'", "select * from t3 where d = 'abc ' ", "select * from t3 where a = 1 and _tidb_rowid = 1",
		"select * from t4 where a = 'x' and b = 1", "select * from t4 where a = 'x'", "select * from t4 where c = 1", "select _tidb_rowid from t4 where c = 1",
		"select * from t4 where _tidb_rowid = 1", "select * from t4 where a = 'X ' and b = '1'",
		"select * from t5 where a = 1", "select a from t5 where a = 1", "select * from t5 where c = 1",
		"select * from t6 where b = 1", "select * from t6 where c = 1", "select * from t6 where a = 1",
		"select * from t7 where e = 'x'", "select * from t7 where e = 'X'", "select * from t7 where e = 'w'", "select * from t7 where e = 1",
		"select * from t7 where e = 4", "select * from t7 where bt = 1", "select * from t7 where bt = b'1'", "select * from t7 where bt = 'a'",
		"select * from t7 where bt = 0x61", "select * from t7 where bt = 300", "select * from t7 where dc = 1.5", "select * from t7 where dc = 1.555",
		"select * from t7 where dc = '1.5'", "select * from t7 where dc = 1", "select * from t7 where f = 1.5", "select * from t7 where f = 1e3",
		"select * from t7 where f = '1.5'", "select * from t7 where dt = '2020-01-02 03:04:05'", "select * from t7 where dt = '2020-01-02'",
		"select * from t7 where dt = 20200102030405", "select * from t7 where dt = 'x'", "select * from t7 where dt = '2020-13-01'",
		"select * from t7 where y = 2020", "select * from t7 where y = 20", "select * from t7 where y = '2020'", "select * from t7 where y = 3000",
		"select * from t7 where ts = '2020-01-02 03:04:05'", "select * from t7 where ts = 0",
		"select * from t8 where b = 'ab'", "select * from t8 where b = 'ab\\0\\0\\0'", "select * from t8 where c = 'ab'", "select * from t8 where d = 'ab'",
		"select * from t8 where b = 'abcdefg'", "select * from t8 where c = 0x6162", "select * from t8 where d = 1",
		"select * from t9 where b = 1", "select * from t9 where c = 1", "select * from t9 where _tidb_rowid = 1", "select * from t9 partition (p0) where b = 1",
		"select * from t9 partition (p1) where c = 1", "select * from t9 where a = 1",
		"select * from t10 where a = 1", "select * from t10 where b = 1",
		"select * from t11 where b = 1", "select * from t11 where c = 1", "select * from t11 where a = 1",
		"select * from t12 where b = 1",
		"select * from t13 where a = 1", "select * from t13 where b = 127", "select * from t13 where b = 128", "select * from t13 where b = -129",
		"select * from t13 where c = -1", "select * from t13 where c = 65535", "select * from t13 where c = 65536", "select * from t13 where d = 8388608",
		"select * from t13 where a = 9223372036854775807", "select * from t13 where a = 9223372036854775808", "select * from t13 where a = -9223372036854775808",
		"select * from t13 where b = '12abc'", "select * from t13 where b = ' 12'", "select * from t13 where b = 12.0", "select * from t13 where b = 1e2",
		"select * from t14 where a = 'x'", "select * from t14 where b = 1", "select * from t14 where _tidb_rowid = 1",
		"select * from t15 where name = 'Ä'", "select * from t15 where name = 'a'", "select * from t15 where k = 1 and name = 'a'",
		"select * from t15 where name = 'A ' and k = 1", "select id, name from t15 where id = 2",
		"select * from nosuch where a = 1", "select * from t1 where a = 1 union select * from t1 where a = 2", "select 1",
		// (UPDATE / DELETE and AS OF need a real session in Go: not recorded; tidb-rs stops
		// point UPDATE / DELETE with blocked-by:X.)
		"select * from t1 tablesample regions() where a = 1", "insert into t1 values (1, 2, 'x', 1, 1)",
		"table t1", "values row(1)",
	}
	for _, sql := range sqls {
		for _, s := range sessions {
			g.Add("sys/fast", "fast", ctxOf(strict), s, tjsOf(sql), sql)
		}
	}
	// The session switches on a sample of the statements.
	for i, sql := range sqls {
		if i%3 != 0 {
			continue
		}
		for _, s := range special {
			g.Add("sys/fast_sess", "fast", ctxOf(strict), s, tjsOf(sql), sql)
		}
	}
	// Truncation as a warning: the conversion of '1x' to an int is not an error.
	for _, sql := range []string{"select * from t1 where a = '1x'", "select * from t1 where b = '1x'", "select * from t13 where b = '12abc'",
		"select * from t7 where dt = 'x'", "select * from t13 where b = 300"} {
		g.Add("sys/fast_flags", "fast", ctxOf(uint16(types.StrictFlags.WithTruncateAsWarning(true))), sessions[0], tjsOf(sql), sql)
		g.Add("sys/fast_flags", "fast", ctxOf(uint16(types.StrictFlags.WithIgnoreTruncateErr(true))), sessions[0], tjsOf(sql), sql)
	}
	// Random equalities on random columns of random tables.
	lits := []string{"1", "0", "-1", "'1'", "'a'", "'A '", "'ab'", "1.5", "1e1", "null", "x'61'", "b'1'", "'2020-01-02'", "true",
		"'x'", "'abcdefghijkl'", "255", "65536", "99999999999999999999", "'Ä'", "'ß'", "_binary'a'", "_utf8mb4'a' collate utf8mb4_bin"}
	for i := 0; i < 900; i++ {
		t := tbls[rng.IntN(len(tbls))]
		n := 1 + rng.IntN(3)
		where := ""
		for k := 0; k < n; k++ {
			c := t.Columns[rng.IntN(len(t.Columns))]
			if rng.IntN(15) == 0 {
				c = &model.ColumnInfo{Name: pmodel.NewCIStr("_tidb_rowid")}
			}
			lit := lits[rng.IntN(len(lits))]
			if k > 0 {
				where += " and "
			}
			if rng.IntN(4) == 0 {
				where += lit + " = " + c.Name.O
			} else {
				where += c.Name.O + " = " + lit
			}
		}
		fields := "*"
		switch rng.IntN(4) {
		case 1:
			fields = t.Columns[rng.IntN(len(t.Columns))].Name.O
		case 2:
			fields = t.Columns[0].Name.O + " as x, *"
		}
		sql := fmt.Sprintf("select %s from %s where %s", fields, t.Name.O, where)
		g.Add(fmt.Sprintf("rand/fast%d", i), "fast", ctxOf(strict), sessions[rng.IntN(2)], tjsOf(sql), sql)
	}
	fastplanGenerateUidx(g, rng)
}

// ---------------------------------------------------------------- "uidx" records

// fpColumn: a random column for the unique-index tables.
func fpColumn(rng *rand.Rand, i int) *model.ColumnInfo {
	type mk func() *types.FieldType
	str := func(tp byte, flen int, cl string) mk {
		return func() *types.FieldType {
			ft := types.NewFieldType(tp)
			ft.SetFlen(flen)
			ft.SetCharset("utf8mb4")
			if cl == "binary" {
				ft.SetCharset("binary")
			}
			ft.SetCollate(cl)
			return ft
		}
	}
	num := func(tp byte, flag uint, flen, dec int) mk {
		return func() *types.FieldType {
			ft := types.NewFieldType(tp)
			ft.SetFlag(flag)
			ft.SetFlen(flen)
			ft.SetDecimal(dec)
			ft.SetCharset("binary")
			ft.SetCollate("binary")
			return ft
		}
	}
	pool := []mk{
		num(mysql.TypeLong, 0, 11, 0), num(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0), num(mysql.TypeTiny, 0, 4, 0),
		str(mysql.TypeVarchar, 8, "utf8mb4_bin"), str(mysql.TypeVarchar, 8, "utf8mb4_general_ci"),
		str(mysql.TypeVarchar, 8, "utf8mb4_unicode_ci"), str(mysql.TypeVarchar, 8, "utf8mb4_0900_ai_ci"),
		str(mysql.TypeString, 4, "utf8mb4_bin"), str(mysql.TypeString, 4, "utf8mb4_general_ci"), str(mysql.TypeString, 4, "binary"),
		str(mysql.TypeVarchar, 6, "binary"),
		func() *types.FieldType {
			ft := str(mysql.TypeEnum, 1, "utf8mb4_bin")()
			ft.SetElems([]string{"a", "b", "c"})
			return ft
		},
		func() *types.FieldType {
			ft := str(mysql.TypeEnum, 1, "utf8mb4_general_ci")()
			ft.SetElems([]string{"a", "B", "c"})
			return ft
		},
		num(mysql.TypeNewDecimal, 0, 6, 2), num(mysql.TypeDouble, 0, 22, -1), num(mysql.TypeDatetime, 0, 22, 2),
		num(mysql.TypeTimestamp, 0, 19, 0), num(mysql.TypeDate, 0, 10, 0), num(mysql.TypeYear, mysql.UnsignedFlag|mysql.ZerofillFlag, 4, 0),
		num(mysql.TypeBit, mysql.UnsignedFlag, 6, 0), num(mysql.TypeDuration, 0, 10, 0),
	}
	ft := tblPick(rng, pool)()
	return &model.ColumnInfo{ID: int64(i + 1), Name: pmodel.NewCIStr(fmt.Sprintf("c%d", i)), Offset: i, FieldType: *ft,
		State: model.StatePublic, Version: model.CurrLatestColumnInfoVersion}
}

// fpDatumFor: a value of c's type (sometimes NULL).
func fpDatumFor(rng *rand.Rand, c *model.ColumnInfo) types.Datum {
	if rng.IntN(10) == 0 && !mysql.HasNotNullFlag(c.GetFlag()) {
		return types.Datum{}
	}
	switch c.GetType() {
	case mysql.TypeLong, mysql.TypeLonglong, mysql.TypeTiny:
		if mysql.HasUnsignedFlag(c.GetFlag()) {
			return types.NewUintDatum(uint64(rng.IntN(100)))
		}
		return types.NewIntDatum(int64(rng.IntN(200) - 100))
	case mysql.TypeYear:
		return types.NewIntDatum(int64(1990 + rng.IntN(30)))
	case mysql.TypeVarchar, mysql.TypeString:
		return types.NewCollationStringDatum(tblPick(rng, []string{"a", "ab", "A", "ab ", "ß", "ss", "Ä"}), c.GetCollate())
	case mysql.TypeEnum:
		return types.NewMysqlEnumDatum(types.Enum{Name: "b", Value: 2})
	case mysql.TypeNewDecimal:
		return types.NewDecimalDatum(types.NewDecFromStringForTest(fmt.Sprintf("%d.%02d", rng.IntN(100), rng.IntN(100))))
	case mysql.TypeDouble:
		return types.NewFloat64Datum(float64(rng.IntN(100)) / 8)
	case mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDate:
		return types.NewTimeDatum(types.NewTime(types.FromDate(2000+rng.IntN(30), 1+rng.IntN(12), 1+rng.IntN(28), rng.IntN(24), 0, 0, 0),
			c.GetType(), c.GetDecimal()))
	case mysql.TypeDuration:
		return types.NewDurationDatum(types.Duration{Duration: gotime.Duration(rng.IntN(100)) * gotime.Minute, Fsp: c.GetDecimal()})
	case mysql.TypeBit:
		return types.NewMysqlBitDatum([]byte{byte(rng.IntN(64))})
	}
	return types.Datum{}
}

// fpForeign: a value of another type than c's, or out of c's range: the planner passes
// values converted to the column type, the executor casts them again (ErrNotExist for a
// value the column cannot hold).
func fpForeign(rng *rand.Rand, c *model.ColumnInfo) types.Datum {
	switch rng.IntN(14) {
	case 0:
		return types.NewIntDatum(int64(rng.IntN(400) - 200))
	case 1:
		return types.NewUintDatum(math.MaxUint64 - uint64(rng.IntN(3)))
	case 2:
		return types.NewIntDatum(math.MinInt64)
	case 3:
		return types.NewStringDatum(tblPick(rng, []string{"12", "1x", "abc", "", "2020-01-02 03:04:05", "b", "B", "d", "1.555", "300"}))
	case 4:
		return types.NewCollationStringDatum(tblPick(rng, []string{"abcdefghijk", "abcd ", "ABCD"}), "utf8mb4_bin")
	case 5:
		return types.NewBytesDatum([]byte(tblPick(rng, []string{"a", "c", "\x00\x01", "zz"})))
	case 6:
		return types.NewBinaryLiteralDatum([]byte(tblPick(rng, []string{"a", "b", "\x01"})))
	case 7:
		return types.NewFloat64Datum(tblPick(rng, []float64{1.5, -0.25, 1e30, 2020}))
	case 8:
		return types.NewDecimalDatum(types.NewDecFromStringForTest(tblPick(rng, []string{"1.5", "12.345", "99999.99", "-0.5"})))
	case 9:
		return types.NewTimeDatum(types.NewTime(types.FromDate(2021, 2, 3, 4, 5, 6, 789000), mysql.TypeDatetime, 6))
	case 10:
		// A TIME converted to a date / datetime takes today's date (the wall clock): not
		// for those columns, the records would depend on the generation day.
		switch c.GetType() {
		case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
			return types.NewIntDatum(20210203)
		}
		return types.NewDurationDatum(types.Duration{Duration: 25 * gotime.Hour, Fsp: 0})
	case 11:
		return types.NewMysqlEnumDatum(types.Enum{Name: "c", Value: 3})
	case 12:
		return types.NewMysqlBitDatum([]byte{0xff, 0x01})
	}
	return types.NewUintDatum(uint64(rng.IntN(70)))
}

// fpUidxTable: a table with a unique index (the second index) whose handle is an int
// primary key, a clustered primary key or _tidb_rowid, sometimes partitioned with a global
// unique index; with a clustered key, the primary index is also read (common handle read).
func fpUidxTable(rng *rand.Rand, id int64) *model.TableInfo {
	n := 3 + rng.IntN(4)
	t := &model.TableInfo{ID: id, Name: pmodel.NewCIStr(fmt.Sprintf("t%d", id)), Charset: "utf8mb4", Collate: "utf8mb4_bin",
		State: model.StatePublic, Version: model.CurrLatestTableInfoVersion}
	for i := 0; i < n; i++ {
		t.Columns = append(t.Columns, fpColumn(rng, i))
	}
	idxCols := func(k int, from []int) []*model.IndexColumn {
		var out []*model.IndexColumn
		for _, o := range from[:k] {
			c := t.Columns[o]
			out = append(out, &model.IndexColumn{Name: c.Name, Offset: o, Length: types.UnspecifiedLength})
		}
		return out
	}
	perm := rng.Perm(n)
	switch rng.IntN(3) {
	case 0: // int handle
		c := t.Columns[perm[0]]
		c.FieldType = *types.NewFieldType(mysql.TypeLonglong)
		c.SetCharset("binary")
		c.SetCollate("binary")
		c.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
		t.PKIsHandle = true
		perm = perm[1:]
	case 1: // clustered
		k := 1 + rng.IntN(2)
		pk := &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("primary"), Primary: true, Unique: true, State: model.StatePublic,
			Columns: idxCols(k, perm), Tp: pmodel.IndexTypeBtree}
		for _, ic := range pk.Columns {
			t.Columns[ic.Offset].AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
		}
		t.IsCommonHandle = true
		t.CommonHandleVersion = 1
		t.Indices = append(t.Indices, pk)
		perm = perm[k:]
	}
	k := 1 + rng.IntN(min(2, len(perm)))
	u := &model.IndexInfo{ID: 2, Name: pmodel.NewCIStr("u"), Unique: true, State: model.StatePublic, Columns: idxCols(k, perm),
		Tp: pmodel.IndexTypeBtree}
	t.Indices = append(t.Indices, u)
	if !t.IsCommonHandle && rng.IntN(4) == 0 {
		t.Partition = &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Expr: "`c0`", Num: 2, Enable: true,
			Definitions: []model.PartitionDefinition{{ID: id + 1, Name: pmodel.NewCIStr("p0")}, {ID: id + 2, Name: pmodel.NewCIStr("p1")}}}
		u.Global = true
		if rng.IntN(2) == 0 {
			u.GlobalIndexVersion = model.GlobalIndexVersionV1
		}
	}
	return t
}

func fastplanGenerateUidx(g *vec.Gen, rng *rand.Rand) {
	zones := []int{0, 8 * 3600, -5 * 3600}
	levels := [][]int{make([]int, 7), {0, 1, 0, 0, 0, 0, 0}, {0, 2, 0, 0, 0, 0, 0}}
	for i := 0; i < 800; i++ {
		t := fpUidxTable(rng, int64(5000+i*10))
		js := tblJSON(t)
		for k := 0; k < 3; k++ {
			nc := 1
			if rng.IntN(4) == 0 {
				nc = 0
			}
			flags := tblPick(rng, []uint16{uint16(types.StrictFlags), uint16(types.StrictFlags.WithTruncateAsWarning(true)),
				uint16(types.StrictFlags.WithIgnoreTruncateErr(true))})
			ctx := []any{flags, tblPick(rng, levels), tblPick(rng, []uint64{0, uint64(mysql.ModeStrictTransTables)})}
			for _, idx := range t.Indices {
				var vals []any
				for _, ic := range idx.Columns {
					c := t.Columns[ic.Offset]
					d := fpDatumFor(rng, c)
					if rng.IntN(3) == 0 {
						d = fpForeign(rng, c)
					}
					vals = append(vals, d2DatumInOf(d))
				}
				var h kv.Handle = kv.IntHandle(rng.Int64N(2000) - 1000)
				if t.IsCommonHandle {
					var pk []types.Datum
					for _, ic := range t.Indices[0].Columns {
						pk = append(pk, fpDatumFor(rng, t.Columns[ic.Offset]))
					}
					b, err := codec.EncodeKey(gotime.UTC, nil, pk...)
					if err != nil {
						continue
					}
					ch, err := kv.NewCommonHandle(b)
					if err != nil {
						continue
					}
					h = ch
				}
				var pid any
				var pnames []string
				if idx.Global {
					pid = t.Partition.Definitions[rng.IntN(2)].ID
					if rng.IntN(3) == 0 {
						pnames = []string{tblPick(rng, []string{"p0", "p1"})}
					}
				}
				g.Add(fmt.Sprintf("rand/uidx%d", i), "uidx", nc, ctx, tblPick(rng, zones), js, idx.Name.L, vals,
					t.ID, specOf(h), pid, pnames)
			}
		}
	}
}

func fastplanExempt() map[string]string {
	batch := "BatchPointGet: not in VS1 (tidb-rs specs/VS1-vertical-slice plan §3, D10.8 row); tidb-rs stub blocked-by:D10.8"
	write := "point UPDATE / DELETE: the write path, block X (tidb-rs stub blocked-by:X)"
	unit := "VS1-11 function without a record (no observable input, a trivial getter, or a boundary stub): covered by tidb-rs unit tests (crates/planner/src/core/point_get_plan/tests.rs)"
	fp := "pkg/planner/core/point_get_plan.go:"
	m := map[string]string{
		fp + "appendVisitInfo4Test": "failpoint test hook, not ported", fp + "setVisitInfo4Test": "failpoint test hook, not ported",
		fp + "buildOrderedList": write, fp + "buildPointDeletePlan": write, fp + "buildPointUpdatePlan": write,
		fp + "checkIfAssignmentListHasSubQuery": write, fp + "isExprHasSubQuery": write, fp + "subQueryChecker.Enter": write,
		fp + "subQueryChecker.Leave": write, fp + "tryDeletePointPlan": write, fp + "tryUpdatePointPlan": write,
		fp + "buildHandleCols":      write + "; ported, unit test",
		fp + "newBatchPointGetPlan": batch, fp + "tryWhereIn2BatchPointGet": batch, fp + "getHashOrKeyPartitionColumnName": batch,
		fp + "getPointGetValue": batch + "; ported, unit test", fp + "isInExplicitPartitions": unit,
	}
	for _, s := range []string{"AppendChildCandidate", "Attach2Task", "Children", "Clone", "Cost", "ExplainInfo",
		"ExplainNormalizedInfo", "ExtractCorrelatedCols", "GetActualProbeCnt", "GetChildReqProps", "GetEstRowCountForDisplay",
		"getPartitionIdxs", "LoadTableStats", "MemoryUsage", "OperatorInfo", "OutputNames", "PrunePartitionsAndValues",
		"ResolveIndices", "SetChild", "SetChildren", "SetCost", "SetOutputNames", "SetProbeParents", "StatsCount", "StatsInfo", "ToPB"} {
		m[fp+"BatchPointGetPlan."+s] = batch
	}
	for _, s := range []string{"AppendChildCandidate", "Attach2Task", "Children", "Clone", "Cost", "ExplainInfo",
		"ExplainNormalizedInfo", "ExtractCorrelatedCols", "GetActualProbeCnt", "GetChildReqProps", "LoadTableStats",
		"PrunePartitions", "ResolveIndices", "SetChild", "SetChildren", "SetCost", "SetOutputNames", "SetProbeParents", "ToPB"} {
		m[fp+"PointGetPlan."+s] = unit
	}
	return m
}
