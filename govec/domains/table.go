package domains

// Domain "table": block D06.4 part 1, the read path of pkg/table and pkg/table/tables
// (TableFromMeta, the column API, column casts and default values, raw row decoding, the
// handle columns, the coprocessor table scan), and expression.GetTimeValue (D08.12, owned
// by D06.4: tidb-rs specs/D06.4-table/tasks.md P1-1).
//
// Values:
//   - a table is its model.TableInfo JSON (json.Marshal); Exec unmarshals it, as TiDB
//     loads tables from TiKV (so `any` defaults are float64 / string / nil);
//   - a datum is the datum2 form (d2DatumIn / d2DatumOut), a field type the d2FtIn form,
//     a handle the kv domain's handle spec;
//   - a context is the cast domain's castCtx array, then the location: null (UTC) or an
//     offset in seconds (time.FixedZone("Z", s));
//   - warnings are ecWarns ([level, msg]);
//   - a TableFromMeta result lists, per column list, the column offsets; per column its
//     String(), IsPKHandleColumn, IsCommonHandleColumn; the indices' names; the record /
//     index prefixes; Type, GetPhysicalID, and whether the table is partitioned / cached.
//
// The write path (record mutations, auto IDs, check constraints, the mutate options,
// index writes) is exempt for good (tidb-rs is read-only); partitioned / cached /
// temporary tables, the index read helpers and SHOW COLUMNS are D06.4 part 2.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/gogo/protobuf/proto"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/autoid"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/table"
	"github.com/pingcap/tidb/pkg/table/tables"
	"github.com/pingcap/tidb/pkg/tablecodec"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/rowcodec"
)

const tableSeed = 20261115

func init() {
	vec.Register(&vec.Domain{
		Name:     "table",
		Seed:     tableSeed,
		Errors:   append(append([]vec.NamedErr{}, datumErrors...), tableErrors...),
		Coverage: tableCoverage,
		Blocks:   []string{"D06.4"},
		Exempt:   tableExempt(),
		Gzip:     true,
		Generate: tableGenerate,
		Exec:     tableExec,
	})
}

var tableErrors = []vec.NamedErr{
	{Name: "ErrColumnCantNull", Err: table.ErrColumnCantNull},
	{Name: "ErrNoDefaultValue", Err: table.ErrNoDefaultValue},
	{Name: "ErrTableStateCantNone", Err: table.ErrTableStateCantNone},
	{Name: "ErrColumnStateCantNone", Err: table.ErrColumnStateCantNone},
	{Name: "ErrIndexStateCantNone", Err: table.ErrIndexStateCantNone},
	{Name: "ErrTruncatedWrongValueForField", Err: table.ErrTruncatedWrongValueForField},
}

var tableCoverage = map[string]vec.Coverage{
	"zero_value":   {Min: 60, GoFuncs: []string{"GetZeroValue"}},
	"optional_fsp": {Min: 8, GoFuncs: []string{"OptionalFsp"}},
	"cast_column": {Min: 2000, GoFuncs: []string{"CastColumnValue", "castColumnValue", "handleZeroDatetime",
		"truncateTrailingSpaces", "convertToIncorrectStringErr"}},
	"default_value": {Min: 1500, GoFuncs: []string{"pkg/table/column.go:GetColDefaultValue", "GetColOriginDefaultValue",
		"GetColOriginDefaultValueWithoutStrictSQLMode", "getColDefaultValue", "getColDefaultValueFromNil",
		"pkg/table/tables/tables.go:GetColDefaultValue"}},
	// expression.GetTimeValue (D08.12, owned by D06.4; its exemption stays in the cast domain
	// until D06.4 is done): the string case column defaults use.
	"time_value": {Min: 300, GoFuncs: []string{"getColDefaultValue"}},
	"table_from_meta": {Min: 400, GoFuncs: []string{"TableFromMeta", "MockTableFromMeta", "buildGeneratedExpr",
		"initTableCommon", "initTableIndices", "initTableCommonWithIndices", "NewIndex", "index.Meta", "index.TableMeta",
		"TableCommon.ResetColumnsCache", "TableCommon.Copy", "TableCommon.getCols", "TableCommon.Cols",
		"TableCommon.VisibleCols", "TableCommon.HiddenCols", "TableCommon.WritableCols", "TableCommon.DeletableCols",
		"TableCommon.FullHiddenColsAndVisibleCols", "TableCommon.Indices", "TableCommon.DeletableIndices",
		"TableCommon.Meta", "TableCommon.GetPhysicalID", "TableCommon.GetPartitionedTable", "TableCommon.RecordPrefix",
		"TableCommon.IndexPrefix", "TableCommon.Allocators", "TableCommon.Type", "TableCommon.RecordKey",
		"ToColumn", "Column.String", "Column.ToInfo", "Column.IsPKHandleColumn", "Column.IsCommonHandleColumn",
		"NewClonableExprNode", "ClonableExprNode.Clone", "ClonableExprNode.Internal",
		"Type.IsNormalTable", "Type.IsVirtualTable", "Type.IsClusterTable",
		// and util/generatedexpr (D00.3): ParseExpression, SimpleResolveName, nameResolver
		"FindPrimaryIndex", "TryGetCommonPkColumnIds", "PrimaryPrefixColumnIDs", "TryGetCommonPkColumns",
		"FindIndexByColName"}},
	"decode_row": {Min: 1500, GoFuncs: []string{"DecodeRawRowData", "containFullColInHandle", "GetChangingColVal",
		"pkg/table/tables/tables.go:GetColDefaultValue"}},
	"find_cols": {Min: 200, GoFuncs: []string{"FindCol", "FindColLowerCase", "FindCols", "FindColumns"}},
	"restored": {Min: 300, GoFuncs: []string{"TryGetHandleRestoredDataWrapper", "TryTruncateRestoredData",
		"ConvertDatumToTailSpaceCount", "maxIndexLen"}},
	"can_skip": {Min: 300, GoFuncs: []string{"CanSkip"}},
	// and util.ColumnsToProto / ColumnToProto (D00.3)
	"table_scan": {Min: 300, GoFuncs: []string{"BuildTableScanFromInfos", "SetPBColumnsDefaultValue"}},
}

// ---------------------------------------------------------------- exemptions

func tableExempt() map[string]string {
	ex := map[string]string{}
	writePath := "write path: tidb-rs is read-only (permanent; D06.4 spec §1.2)"
	part2 := func(why string) string { return "D06.4 part 2: " + why }
	for _, f := range []string{
		// table.go
		"AddRecordOpt.GenerateRecordID", "AddRecordOpt.GetCreateIdxOpt", "AddRecordOpt.IsUpdate",
		"AddRecordOpt.ReserveAutoID", "AllocAutoIncrementValue", "AllocBatchAutoIncrementValue",
		"CheckRowConstraint", "CheckRowConstraintWithDatum", "CommonMutateOptFunc.applyAddRecordOpt",
		"CommonMutateOptFunc.applyCreateIdxOpt", "CommonMutateOptFunc.applyUpdateRecordOpt",
		"DupKeyCheckMode.applyAddRecordOpt", "DupKeyCheckMode.applyCreateIdxOpt", "DupKeyCheckMode.applyUpdateRecordOpt",
		"IndexesLayout.GetIndexLayout", "IndexesLayout.applyRemoveRecordOpt", "NewAddRecordOpt", "NewRemoveRecordOpt",
		"NewUpdateRecordOpt", "PessimisticLazyDupKeyCheckMode.applyAddRecordOpt",
		"PessimisticLazyDupKeyCheckMode.applyCreateIdxOpt", "PessimisticLazyDupKeyCheckMode.applyUpdateRecordOpt",
		"RemoveRecordOpt.GetIndexLayout", "RemoveRecordOpt.GetIndexesLayout", "RemoveRecordOpt.HasIndexesLayout",
		"UpdateRecordOpt.GetAddRecordOpt", "UpdateRecordOpt.GetAddRecordOptKeepRecordID", "UpdateRecordOpt.GetCreateIdxOpt",
		"UpdateRecordOpt.SkipWriteUntouchedIndices", "WithCtx", "WithReserveAutoIDHint.applyAddRecordOpt",
		"commonMutateOpt.Ctx", "commonMutateOpt.DupKeyCheck", "commonMutateOpt.PessimisticLazyDupKeyCheck",
		"getIncrementAndOffset", "isUpdate.applyAddRecordOpt", "skipWriteUntouchedIndices.applyUpdateRecordOpt",
		// index.go
		"CreateIdxOpt.FromBackFill", "CreateIdxOpt.IgnoreAssertion", "IndexKVGenerator.Next", "IndexKVGenerator.Valid",
		"NewCreateIdxOpt", "NewMultiValueIndexKVGenerator", "NewPlainIndexKVGenerator",
		"fromBackfill.applyCreateIdxOpt", "withIgnoreAssertion.applyCreateIdxOpt",
		// column.go
		"CastColumnValueWithStrictMode", "CheckNoDefaultValueForInsert", "CheckOnce", "Column.CheckNotNull",
		"Column.HandleBadNull", "FindOnUpdateCols",
		// tables.go
		"AllocHandle", "AllocHandleIDs", "OverflowShardBits", "TableCommon.AddRecord", "TableCommon.addRecord",
		"TableCommon.UpdateRecord", "TableCommon.updateRecord", "TableCommon.rebuildUpdateRecordIndices",
		"TableCommon.RemoveRecord", "TableCommon.removeRecord", "TableCommon.removeRowData",
		"TableCommon.removeRowIndices", "TableCommon.buildIndexForRow", "TableCommon.addIndices",
		"TableCommon.WritableConstraint", "TableCommon.GetSequenceNextVal", "TableCommon.SetSequenceVal",
		"TableCommon.GetSequenceID", "TableCommon.GetSequenceCommon", "sequenceCommon.GetSequenceBaseEndRound",
		"sequenceCommon.getOffset", "getSequenceAllocator", "getDuplicateError", "genIndexKeyStrs",
		"checkDataForModifyColumn", "GetWritableIndexByName", "asIndex", "addTemporaryTable", "handleTempTableSize",
		"checkTempTableSize",
		// tables/index.go
		"index.castIndexValuesToChangingTypes", "index.Create", "index.create", "index.Delete",
		"index.mayDDLMergingTempIndex", "index.GenIndexKVIter", "GenTempIdxKeyByState", "index.Exist",
		"FetchDuplicatedHandle", "FetchDuplicatedHandleForTempIndexKey", "getKeyInTxn", "index.GenIndexValue",
		"index.MeetPartialCondition", "index.MeetPartialConditionWithChunk", "IsIndexWritable", "FindChangingCol",
		"pkg/table/tables/index.go:init",
	} {
		ex[f] = writePath
	}
	for f, why := range map[string]string{
		"NewColDesc":                            part2("SHOW COLUMNS / DESC (executor D12.12)"),
		"ColDescFieldNames":                     part2("SHOW COLUMNS / DESC (executor D12.12)"),
		"CastValue":                             "the SessionVars entry of castColumnValue (its body is covered by cast_column)",
		"EvalColDefaultExpr":                    part2("needs expression.EvalSimpleAst (the expression rewriter, D09.2); a blocked-by stub in part 1"),
		"getColDefaultExprValue":                part2("needs expression.EvalSimpleAst (the expression rewriter, D09.2); a blocked-by stub in part 1"),
		"FillVirtualColumnValue":                "needs expression.Column with a VirtualExpr built by the planner; tested by tidb-rs unit test table::column::tests::fill_virtual_column_value_cases (end to end with the point-get executor, D12.1)",
		"RowWithCols":                           part2("reads through sessionctx.Context.Txn (the session, D05.3)"),
		"IterRecords":                           part2("iterates kv records (admin / DDL readers)"),
		"BuildPartitionTableScanFromInfos":      part2("partitioned tables"),
		"TempTableFromMeta":                     part2("temporary tables; a blocked-by stub in part 1"),
		"TemporaryTable.GetAutoIDAllocator":     part2("temporary tables"),
		"TemporaryTable.SetModified":            part2("temporary tables"),
		"TemporaryTable.GetModified":            part2("temporary tables"),
		"TemporaryTable.GetStats":               part2("temporary tables"),
		"TemporaryTable.GetSize":                part2("temporary tables"),
		"TemporaryTable.SetSize":                part2("temporary tables"),
		"TemporaryTable.GetMeta":                part2("temporary tables"),
		"NeedRestoredData":                      part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"index.GenIndexKey":                     part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"index.getIndexedValue":                 part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"index.FetchValues":                     part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"fetchIndexRow":                         part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"BuildRowcodecColInfoForIndexColumns":   part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"BuildFieldTypesForIndexColumns":        part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"TryAppendCommonHandleRowcodecColInfos": part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"GenIndexValueFromIndex":                part2("index read helpers (index lookup / index reader paths, D10.2 / D12.1)"),
		"ExtractColumnsFromCondition":           part2("partial-index conditions (planner, D10.2)"),
		"tryDecodeColumnFromCommonHandle":       part2("only called by IterRecords"),
		"pkg/table/tables/tables.go:init":       "sets table.TableFromMeta / MockTableFromMeta and tableutil.TempTableFromMeta; tidb-rs calls package tables directly (table::table_from_meta, covered by table_from_meta)",
		"TableCommon.canSkip":                   writePath + ": called by addRecord (CanSkip is covered by can_skip)",
		"DedupIndexColumns":                     part2("partial-index conditions (planner, D10.2)"),
		"extractColumnsFromExpr":                part2("partial-index conditions (planner, D10.2)"),
	} {
		ex[f] = why
	}
	// Whole files of part 2 / the write path.
	for _, fb := range tableFileExempt {
		for _, f := range funcsOfFile(fb.file) {
			if _, ok := ex[f]; !ok {
				ex[f] = fb.why
			}
		}
	}
	return ex
}

var tableFileExempt = []struct{ file, why string }{
	{"pkg/table/constraint.go", "write path: check constraints are enforced only on writes (permanent; D06.4 spec §1.2)"},
	{"pkg/table/tables/mutation_checker.go", "write path: the mutation checker checks writes (permanent; D06.4 spec §1.2)"},
	{"pkg/table/tables/partition.go", "D06.4 part 2: partitioned tables (newPartitionedTable is a blocked-by stub in part 1)"},
	{"pkg/table/tables/cache.go", "D06.4 part 2: cached tables (newCachedTable is a blocked-by stub in part 1)"},
	{"pkg/table/tables/state_remote.go", "D06.4 part 2: cached tables' remote lock state"},
	{"pkg/table/tblctx/buffers.go", "write path: the mutation buffers (permanent; D06.4 spec §1.2)"},
	{"pkg/table/tblctx/table.go", "write path: the mutate / allocator contexts (permanent; D06.4 spec §1.2)"},
	{"pkg/table/tblsession/table.go", "write path: the session's mutate / allocator contexts (permanent; D06.4 spec §1.2)"},
	{"pkg/table/temptable/ddl.go", "D06.4 part 2: temporary tables"},
	{"pkg/table/temptable/infoschema.go", "D06.4 part 2: temporary tables"},
	{"pkg/table/temptable/interceptor.go", "D06.4 part 2: temporary tables"},
}

// funcsOfFile: the D06.4 functions of a file exempt whole, written "<file>:<Symbol>".
func funcsOfFile(file string) []string {
	var out []string
	for _, f := range d064FileFuncs[file] {
		out = append(out, file+":"+f)
	}
	return out
}

// ---------------------------------------------------------------- execution

func tblIn(raw json.RawMessage) *model.TableInfo {
	var s string
	must(json.Unmarshal(raw, &s))
	t := &model.TableInfo{}
	must(json.Unmarshal([]byte(s), t))
	return t
}

func tblJSON(t *model.TableInfo) string {
	b, err := json.Marshal(t)
	must(err)
	return string(b)
}

func tblLoc(raw json.RawMessage) *gotime.Location {
	if string(raw) == "null" {
		return gotime.UTC
	}
	var s int
	must(json.Unmarshal(raw, &s))
	return gotime.FixedZone("Z", s)
}

// tblCtx: [castCtx, loc].
func tblCtx(raw json.RawMessage) (*exprstatic.ExprContext, *exprstatic.EvalContext) {
	a := exArr(raw)
	return castCtx(a[0], exprstatic.WithLocation(tblLoc(a[1])))
}

func colsOut(cols []*table.Column) []int {
	out := make([]int, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Offset)
	}
	return out
}

// datumOrErr: the datum and the error's cause (mapping rule M-ERR: tidb-rs does not port
// errors.Wrap / Annotate, errors.Cause(err) is err).
func datumOrErr(d types.Datum, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(errors.Cause(err).Error()), "d": d2DatumOut(d)}
	}
	return map[string]any{"d": d2DatumOut(d)}
}

func tableExec(op string, args []json.RawMessage) (out any, err error) {
	switch op {
	case "zero_value":
		ci := &model.ColumnInfo{FieldType: *d2FtIn(exArr(args[0]))}
		return d2DatumOut(table.GetZeroValue(ci)), nil
	case "optional_fsp":
		return table.OptionalFsp(d2FtIn(exArr(args[0]))), nil
	case "cast_column":
		ctx, ev := tblCtx(args[0])
		ci := &model.ColumnInfo{FieldType: *d2FtIn(exArr(args[2])), Name: pmodel.NewCIStr(arg[string](args, 3))}
		d, e := table.CastColumnValue(ctx, d2DatumIn(exArr(args[1])), ci, arg[bool](args, 4), arg[bool](args, 5))
		return map[string]any{"r": datumOrErr(d, e), "w": ecWarns(ev.TruncateWarnings(0))}, nil
	case "default_value":
		ctx, ev := tblCtx(args[0])
		tbl := tblIn(args[1])
		ci := tbl.Columns[arg[int](args, 2)]
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": true}
				}
			}()
			switch arg[int](args, 3) {
			case 0:
				r = datumOrErr(table.GetColDefaultValue(ctx, ci))
			case 1:
				r = datumOrErr(table.GetColOriginDefaultValue(ctx, ci))
			case 2:
				r = datumOrErr(table.GetColOriginDefaultValueWithoutStrictSQLMode(ctx, ci))
			case 3:
				// tables.GetColDefaultValue twice on one cache (the second reads the cache).
				col := table.ToColumn(ci)
				vals := make([]types.Datum, len(tbl.Columns))
				d1, e1 := tables.GetColDefaultValue(ctx, col, vals)
				d2, e2 := tables.GetColDefaultValue(ctx, col, vals)
				r = []any{datumOrErr(d1, e1), datumOrErr(d2, e2)}
			}
		}()
		return map[string]any{"r": r, "w": ecWarns(ev.TruncateWarnings(0))}, nil
	case "time_value":
		ctx, ev := tblCtx(args[0])
		var tz *gotime.Location
		if string(args[4]) != "null" {
			tz = tblLoc(args[4])
		}
		d, e := expression.GetTimeValue(ctx, arg[string](args, 1), arg[byte](args, 2), arg[int](args, 3), tz)
		return map[string]any{"r": datumOrErr(d, e), "w": ecWarns(ev.TruncateWarnings(0))}, nil
	case "table_from_meta":
		tbl := tblIn(args[0])
		var t table.Table
		if arg[bool](args, 1) {
			t = tables.MockTableFromMeta(tbl)
			if t == nil {
				return map[string]any{"nil": true}, nil
			}
		} else {
			t, err = tables.TableFromMeta(autoid.NewAllocators(false), tbl)
			if err != nil {
				return nil, err
			}
		}
		return tableSummary(t), nil
	case "decode_row":
		ctx, ev := tblCtx(args[0])
		tbl := tblIn(args[1])
		t, err := tables.TableFromMeta(autoid.NewAllocators(false), tbl)
		if err != nil {
			return nil, err
		}
		h := handleOfSpec(arg[handleSpec](args, 2))
		var cols []*table.Column
		for _, off := range arg[[]int](args, 3) {
			if off < 0 {
				cols = append(cols, nil)
			} else {
				cols = append(cols, t.Cols()[off])
			}
		}
		value, _ := hex.DecodeString(arg[string](args, 4))
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": true}
				}
			}()
			v, m, err := tables.DecodeRawRowData(ctx, tbl, h, cols, value)
			if err != nil {
				r = map[string]any{"err": vec.GoString(errors.Cause(err).Error())}
				return
			}
			r = map[string]any{"v": datumOuts(v), "m": datumMapOut(m)}
		}()
		return map[string]any{"r": r, "w": ecWarns(ev.TruncateWarnings(0))}, nil
	case "find_cols":
		tbl := tblIn(args[0])
		cols := make([]*table.Column, len(tbl.Columns))
		for i, c := range tbl.Columns {
			cols[i] = table.ToColumn(c)
		}
		names := arg[[]string](args, 1)
		pk := arg[bool](args, 2)
		c1, s1 := table.FindCols(cols, names, pk)
		c2, o2 := table.FindColumns(cols, names, pk)
		return []any{findOut(c1), s1, findOut(c2), o2}, nil
	case "restored":
		tbl := tblIn(args[0])
		row := d2Datums(args[1])
		m := map[int64]types.Datum{}
		if arg[bool](args, 2) {
			for i, c := range tbl.Columns {
				m[c.ID] = row[i]
			}
		}
		idx := tbl.Indices[arg[int](args, 3)]
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": true}
				}
			}()
			rs := tables.TryGetHandleRestoredDataWrapper(tbl, row, m, idx)
			if rs == nil {
				r = nil
			} else {
				r = datumOuts(rs)
			}
		}()
		return r, nil
	case "can_skip":
		tbl := tblIn(args[0])
		col := table.ToColumn(tbl.Columns[arg[int](args, 1)])
		d := d2DatumIn(exArr(args[2]))
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": true}
				}
			}()
			r = tables.CanSkip(tbl, col, &d)
		}()
		return r, nil
	case "table_scan":
		ctx, ev := tblCtx(args[0])
		tbl := tblIn(args[1])
		var cis []*model.ColumnInfo
		for _, off := range arg[[]int](args, 2) {
			if off < 0 {
				cis = append(cis, model.NewExtraHandleColInfo())
			} else {
				cis = append(cis, tbl.Columns[off])
			}
		}
		ts := tables.BuildTableScanFromInfos(tbl, cis, arg[bool](args, 3))
		b, err := proto.Marshal(ts)
		must(err)
		res := map[string]any{"scan": hex.EncodeToString(b)}
		e := tables.SetPBColumnsDefaultValue(ctx, ts.Columns, cis)
		if e != nil {
			res["err"] = vec.GoString(errors.Cause(e).Error())
		}
		var dv []any
		for _, c := range ts.Columns {
			if c.DefaultVal == nil {
				dv = append(dv, nil)
			} else {
				dv = append(dv, hex.EncodeToString(c.DefaultVal))
			}
		}
		res["dv"] = dv
		res["w"] = ecWarns(ev.TruncateWarnings(0))
		return res, nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

func d2Datums(raw json.RawMessage) []types.Datum {
	var ps []json.RawMessage
	must(json.Unmarshal(raw, &ps))
	out := make([]types.Datum, len(ps))
	for i, p := range ps {
		out[i] = d2DatumIn(exArr(p))
	}
	return out
}

func datumMapOut(m map[int64]types.Datum) []any {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, []any{id, d2DatumOut(m[id])})
	}
	return out
}

func findOut(cols []*table.Column) any {
	if cols == nil {
		return nil
	}
	out := make([]any, 0, len(cols))
	for _, c := range cols {
		out = append(out, []any{c.ID, c.Name.O, c.Offset})
	}
	return out
}

// tblTry: f's result, or {"panic": true} if it panics (tidb-rs must panic there too).
func tblTry(f func() any) (r any) {
	defer func() {
		if p := recover(); p != nil {
			r = map[string]any{"panic": true}
		}
	}()
	return f()
}

func tableSummary(t table.Table) map[string]any {
	meta := t.Meta()
	var colsInfo []any
	for _, c := range t.DeletableCols() {
		x := []any{c.Offset, c.String(), c.IsPKHandleColumn(meta), c.IsCommonHandleColumn(meta), c.ToInfo() == c.ColumnInfo}
		if c.GeneratedExpr != nil {
			// The node is rebuilt by the constructor in tidb-rs: only its existence is compared.
			x = append(x, c.GeneratedExpr.Clone() != nil, c.GeneratedExpr.Internal() != nil)
		}
		colsInfo = append(colsInfo, x)
	}
	var idx []any
	for _, i := range t.Indices() {
		idx = append(idx, []any{i.Meta().Name.O, i.TableMeta() == meta})
	}
	tp := t.Type()
	pt, isPhys := t.(table.PhysicalTable)
	_, cached := t.(table.CachedTable)
	out := map[string]any{
		"cols":      colsOut(t.Cols()),
		"visible":   colsOut(t.VisibleCols()),
		"hidden":    colsOut(t.HiddenCols()),
		"writable":  colsOut(t.WritableCols()),
		"deletable": colsOut(t.DeletableCols()),
		"full":      colsOut(t.FullHiddenColsAndVisibleCols()),
		"col_info":  colsInfo,
		"indices":   idx,
		"del_idx":   len(t.DeletableIndices()),
		"rp":        hex.EncodeToString(t.RecordPrefix()),
		"ip":        hex.EncodeToString(t.IndexPrefix()),
		"type":      []bool{tp.IsNormalTable(), tp.IsVirtualTable(), tp.IsClusterTable()},
		"part":      t.GetPartitionedTable() != nil,
		"cached":    cached,
		"allocs":    len(t.Allocators(nil).Allocs),
	}
	if isPhys {
		out["pid"] = pt.GetPhysicalID()
	}
	if tc, ok := t.(*tables.TableCommon); ok {
		cp := tc.Copy()
		out["copy_cols"] = len(cp.Cols())
		out["rk"] = hex.EncodeToString(tc.RecordKey(kv.IntHandle(7)))
	}
	if pk := tables.FindPrimaryIndex(meta); pk != nil {
		out["pk"] = pk.Name.O
	}
	out["pk_ids"] = tblTry(func() any { return tables.TryGetCommonPkColumnIds(meta) })
	out["prefix_ids"] = tblTry(func() any { return tables.PrimaryPrefixColumnIDs(meta) })
	out["pk_cols"] = tblTry(func() any {
		if pkc := tables.TryGetCommonPkColumns(t); pkc != nil {
			return colsOut(pkc)
		}
		return nil
	})
	var byName []any
	for _, c := range meta.Columns {
		if i := tables.FindIndexByColName(t, c.Name.L); i != nil {
			byName = append(byName, i.Meta().Name.O)
		} else {
			byName = append(byName, nil)
		}
	}
	out["by_col"] = byName
	return out
}

// ---------------------------------------------------------------- generation

var tblFtPool = []func(rng *rand.Rand) *types.FieldType{
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeLonglong) },
	func(*rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeLong)
		ft.AddFlag(mysql.UnsignedFlag)
		return ft
	},
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeTiny) },
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeYear) },
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeFloat) },
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeDouble) },
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeNewDecimal)
		ft.SetFlen(10)
		ft.SetDecimal(rng.IntN(4))
		return ft
	},
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldTypeWithCollation(mysql.TypeVarchar, tblPick(rng, []string{"utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "binary", "latin1_bin", "ascii_bin", "utf8_bin", "gbk_bin"}), 1+rng.IntN(20))
		if ft.GetCollate() == "binary" {
			ft.SetCharset("binary")
		}
		return ft
	},
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldTypeWithCollation(mysql.TypeString, tblPick(rng, []string{"utf8mb4_bin", "utf8mb4_general_ci", "binary"}), 1+rng.IntN(8))
		if ft.GetCollate() == "binary" {
			ft.SetCharset("binary")
		}
		return ft
	},
	func(*rand.Rand) *types.FieldType {
		return types.NewFieldTypeWithCollation(mysql.TypeBlob, "utf8mb4_bin", 65535)
	},
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeDatetime)
		ft.SetDecimal(rng.IntN(7))
		return ft
	},
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeTimestamp)
		ft.SetDecimal(rng.IntN(7))
		return ft
	},
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeDate) },
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeDuration)
		ft.SetDecimal(rng.IntN(7))
		return ft
	},
	func(*rand.Rand) *types.FieldType {
		ft := types.NewFieldTypeWithCollation(mysql.TypeEnum, "utf8mb4_bin", 0)
		ft.SetElems([]string{"a", "b", "c"})
		return ft
	},
	func(*rand.Rand) *types.FieldType {
		ft := types.NewFieldTypeWithCollation(mysql.TypeSet, "utf8mb4_bin", 0)
		ft.SetElems([]string{"x", "y"})
		return ft
	},
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeBit)
		ft.SetFlen(1 + rng.IntN(64))
		return ft
	},
	func(*rand.Rand) *types.FieldType { return types.NewFieldType(mysql.TypeJSON) },
	func(rng *rand.Rand) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeTiDBVectorFloat32)
		ft.SetFlen(types.UnspecifiedLength)
		return ft
	},
}

func tblPick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

// tblDefaults: default value candidates by type (as DDL stores them: mostly strings).
func tblDefault(rng *rand.Rand, ft *types.FieldType) any {
	if rng.IntN(5) == 0 {
		return nil
	}
	switch ft.GetType() {
	case mysql.TypeLonglong, mysql.TypeLong, mysql.TypeTiny, mysql.TypeYear:
		return tblPick(rng, []any{"1", "0", "-1", "300", "abc", "2020", "1.5", 3.0, "18446744073709551616"})
	case mysql.TypeFloat, mysql.TypeDouble, mysql.TypeNewDecimal:
		return tblPick(rng, []any{"1.25", "0", "-3.5", "1e3", "x", "123456789.123456"})
	case mysql.TypeVarchar, mysql.TypeString, mysql.TypeBlob:
		return tblPick(rng, []any{"", "abc", "abc   ", "ab\xf0", "很长的字符串很长的字符串很长的字符串", "a b ", "\x00\x01"})
	case mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDate:
		return tblPick(rng, []any{"CURRENT_TIMESTAMP", "current_timestamp", "0000-00-00 00:00:00", "2019-05-06 12:48:49",
			"2019-05-06 12:48:49.123456", "2019-02-30 00:00:00", "2000-00-01", "abc", "CURRENT_DATE", "1970-01-01 00:00:00", "9999-12-31 23:59:59"})
	case mysql.TypeDuration:
		return tblPick(rng, []any{"12:00:00", "-838:59:59", "839:00:00", "x", "1 01:02:03.5"})
	case mysql.TypeEnum:
		return tblPick(rng, []any{"a", "c", "d", "", "2"})
	case mysql.TypeSet:
		return tblPick(rng, []any{"x", "x,y", "z", ""})
	case mysql.TypeBit:
		return tblPick(rng, []any{"\x01", "\x00\xff", "\x07"})
	case mysql.TypeJSON:
		return tblPick(rng, []any{"null", "{}", "[1,2]", "{"})
	case mysql.TypeTiDBVectorFloat32:
		return tblPick(rng, []any{"[1,2]", "[]", "x"})
	}
	return nil
}

var tblGenExprs = []string{"`c0` + 1", "c1", "concat(c0, 'x')", "c0 * 2 + c1", "unknown_col + 1", "(", "lower(c0)",
	"case when c0 > 1 then c1 else c0 end", "c0 between 1 and c1", "c0 in (1, c1)", "c0 is null", "-c0", "cast(c0 as char)",
	"json_extract(c0, '$.a')", "c0 like 'a%'", "c0 regexp 'a'", "row(c0, c1) = row(1, 2)", "c0 collate utf8mb4_bin"}

var tblDefaultExprs = []string{"rand()", "uuid()", "date_format(now(), '%Y-%m')", "upper(substring_index(user(), '@', 1))", "(", "replace(upper(uuid()), '-', '')"}

func tblColumn(rng *rand.Rand, i int) *model.ColumnInfo {
	ft := tblPick(rng, tblFtPool)(rng)
	if rng.IntN(3) == 0 {
		ft.AddFlag(mysql.NotNullFlag)
	}
	if rng.IntN(8) == 0 {
		ft.AddFlag(mysql.NoDefaultValueFlag)
	}
	if rng.IntN(12) == 0 && types.IsTypeInteger(ft.GetType()) {
		ft.AddFlag(mysql.AutoIncrementFlag)
	}
	c := &model.ColumnInfo{
		ID:        int64(i + 1),
		Name:      pmodel.NewCIStr(fmt.Sprintf("c%d", i)),
		Offset:    i,
		FieldType: *ft,
		State:     model.StatePublic,
		Version:   model.CurrLatestColumnInfoVersion,
	}
	if rng.IntN(4) == 0 {
		c.Version = uint64(rng.IntN(3))
	}
	if ft.GetType() == mysql.TypeTimestamp && c.Version < model.ColumnInfoVersion1 {
		// A version-0 TIMESTAMP default is read in timeutil.SystemLocation(), the process
		// time zone: the records would depend on the generating host (tidb-rs#217 review,
		// the replay runs in a non-UTC TZ too). tidb-rs covers that branch with a unit test.
		c.Version = model.ColumnInfoVersion1
	}
	dv := tblDefault(rng, ft)
	if ft.GetType() == mysql.TypeBit && dv != nil {
		must(c.SetDefaultValue(dv))
		must(c.SetOriginDefaultValue(dv))
	} else {
		c.DefaultValue = dv
		if rng.IntN(3) == 0 {
			c.OriginDefaultValue = tblDefault(rng, ft)
		} else {
			c.OriginDefaultValue = dv
		}
	}
	switch rng.IntN(60) {
	case 0:
		c.State = model.StateWriteOnly
	case 1:
		c.State = model.StateDeleteOnly
	case 2:
		c.State = model.StateDeleteReorganization
	case 3:
		c.State = model.StateWriteReorganization
	case 4:
		c.State = model.StateNone
	}
	if rng.IntN(10) == 0 {
		c.Hidden = true
	}
	return c
}

func tblRandTable(rng *rand.Rand, id int64) *model.TableInfo {
	n := 2 + rng.IntN(5)
	t := &model.TableInfo{
		ID:      id,
		Name:    pmodel.NewCIStr(fmt.Sprintf("t%d", id)),
		Charset: "utf8mb4",
		Collate: "utf8mb4_bin",
		State:   model.StatePublic,
		Version: model.CurrLatestTableInfoVersion,
	}
	for i := 0; i < n; i++ {
		t.Columns = append(t.Columns, tblColumn(rng, i))
	}
	if rng.IntN(40) == 0 {
		t.State = model.StateNone
	}
	// generated columns
	if rng.IntN(5) == 0 {
		c := t.Columns[n-1]
		c.GeneratedExprString = tblPick(rng, tblGenExprs)
		c.GeneratedStored = rng.IntN(2) == 0
		c.Dependences = map[string]struct{}{"c0": {}}
	}
	if rng.IntN(8) == 0 {
		c := t.Columns[rng.IntN(n)]
		c.DefaultIsExpr = true
		c.DefaultValue = tblPick(rng, tblDefaultExprs)
	}
	if rng.IntN(10) == 0 && n > 2 {
		c := t.Columns[n-1]
		c.ChangeStateInfo = &model.ChangeStateInfo{DependencyColumnOffset: rng.IntN(n - 1)}
	}
	// keys
	switch rng.IntN(4) {
	case 0: // int handle
		for _, c := range t.Columns {
			if types.IsTypeInteger(c.GetType()) && c.State == model.StatePublic && !c.Hidden && c.GeneratedExprString == "" {
				c.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
				t.PKIsHandle = true
				t.Indices = append(t.Indices, &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("PRIMARY"), Primary: true, Unique: true,
					State: model.StatePublic, Columns: []*model.IndexColumn{{Name: c.Name, Offset: c.Offset, Length: types.UnspecifiedLength}}})
				break
			}
		}
	case 1: // clustered (common handle)
		var pkCols []*model.IndexColumn
		for _, c := range t.Columns {
			if len(pkCols) == 2 || c.GeneratedExprString != "" || c.State != model.StatePublic {
				continue
			}
			switch c.GetType() {
			case mysql.TypeVarchar, mysql.TypeString, mysql.TypeLonglong, mysql.TypeLong, mysql.TypeDatetime, mysql.TypeNewDecimal:
			default:
				continue
			}
			c.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
			length := types.UnspecifiedLength
			if c.GetType() == mysql.TypeVarchar && rng.IntN(3) == 0 {
				length = 1 + rng.IntN(4)
			}
			pkCols = append(pkCols, &model.IndexColumn{Name: c.Name, Offset: c.Offset, Length: length})
		}
		if len(pkCols) > 0 {
			t.IsCommonHandle = true
			t.CommonHandleVersion = uint16(rng.IntN(2))
			t.Indices = append(t.Indices, &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("PRIMARY"), Primary: true, Unique: true,
				State: model.StatePublic, Columns: pkCols})
		}
	}
	for k := 0; k < rng.IntN(3); k++ {
		off := rng.IntN(n)
		st := model.StatePublic
		if rng.IntN(8) == 0 {
			st = tblPick(rng, []model.SchemaState{model.StateNone, model.StateWriteOnly, model.StateDeleteOnly, model.StateWriteReorganization})
		}
		idx := &model.IndexInfo{ID: int64(10 + k), Name: pmodel.NewCIStr(fmt.Sprintf("i%d", k)), Unique: rng.IntN(2) == 0, State: st,
			Columns: []*model.IndexColumn{{Name: t.Columns[off].Name, Offset: off, Length: tblPick(rng, []int{types.UnspecifiedLength, 2, 5})}}}
		t.Indices = append(t.Indices, idx)
	}
	switch rng.IntN(15) {
	case 0:
		// newPartitionExpr builds the partition (and generated column) expressions, which the
		// part-1 stub does not: only tables whose expression Go builds without error.
		if !types.IsTypeInteger(t.Columns[0].GetType()) || t.Columns[0].State != model.StatePublic || t.Columns[0].Hidden {
			break
		}
		gen := false
		for _, c := range t.Columns {
			gen = gen || c.GeneratedExprString != ""
		}
		if gen {
			break
		}
		t.Partition = &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Expr: "`c0`", Num: 2, Enable: true,
			Definitions: []model.PartitionDefinition{{ID: id + 1, Name: pmodel.NewCIStr("p0")}, {ID: id + 2, Name: pmodel.NewCIStr("p1")}}}
	case 1:
		t.TableCacheStatusType = model.TableCacheStatusEnable
	}
	return t
}

func tblCtxArg(rng *rand.Rand) []any {
	modes := []uint64{0, uint64(mysql.ModeStrictTransTables), uint64(mysql.ModeStrictAllTables | mysql.ModeNoZeroDate | mysql.ModeNoZeroInDate),
		uint64(mysql.ModeNoZeroDate), uint64(mysql.ModeNoZeroInDate), uint64(mysql.ModeStrictTransTables | mysql.ModeNoZeroDate)}
	flags := []uint16{uint16(types.StrictFlags), uint16(types.DefaultStmtFlags), uint16(types.StrictFlags.WithTruncateAsWarning(true)),
		uint16(types.StrictFlags.WithIgnoreTruncateErr(true)), uint16(types.StrictFlags.WithAllowNegativeToUnsigned(true)),
		uint16(types.StrictFlags.WithIgnoreZeroInDate(true).WithIgnoreInvalidDateErr(true))}
	lv := make([]int, 7)
	for i := range lv {
		lv[i] = rng.IntN(3)
	}
	// The statement time: 2024-03-10 09:30:15.123456789 UTC, with a random offset.
	ns := int64(1710063015123456789) + rng.Int64N(1<<40)
	var loc any
	switch rng.IntN(4) {
	case 1:
		loc = -8 * 3600
	case 2:
		loc = 9*3600 + 1800
	case 3:
		loc = 5 * 3600
	}
	return []any{[]any{tblPick(rng, flags), lv, 67108864, tblPick(rng, modes), ns}, loc}
}

var tblCastDatums = []types.Datum{
	types.Datum{}, types.NewIntDatum(0), types.NewIntDatum(-1), types.NewIntDatum(1 << 40), types.NewUintDatum(1 << 63),
	types.NewFloat64Datum(-1.5), types.NewFloat64Datum(1e300), types.NewFloat32Datum(2.5),
	types.NewStringDatum("abc"), types.NewStringDatum("abc   "), types.NewStringDatum("123abc"), types.NewStringDatum(""),
	types.NewStringDatum("2019-02-30 10:00:00"), types.NewStringDatum("0000-00-00"), types.NewStringDatum("2000-00-01"),
	types.NewStringDatum("2024-01-01 12:00:00.999999"), types.NewStringDatum("12:30:00"), types.NewStringDatum("{\"a\":1}"),
	types.NewBytesDatum([]byte{0xf0, 0x9f, 0x8c, 0x80}), types.NewBytesDatum([]byte{0xf0, 0x9f, 0x80}), types.NewBytesDatum([]byte{0x32, 0xf0}),
	types.NewBytesDatum([]byte{0xE5, 0xA5, 0xBD, 0x81}), types.NewBinaryLiteralDatum([]byte{0xE5, 0xA5, 0xBD}),
	types.NewDecimalDatum(types.NewDecFromStringForTest("-12.345")), types.NewDecimalDatum(types.NewDecFromStringForTest("99999999999.9")),
	types.NewTimeDatum(types.NewTime(types.FromDate(2019, 5, 6, 12, 48, 49, 0), mysql.TypeDatetime, 0)),
	types.NewTimeDatum(types.ZeroDatetime), types.NewTimeDatum(types.NewTime(types.FromDate(2019, 0, 6, 0, 0, 0, 0), mysql.TypeDate, 0)),
	types.NewDurationDatum(types.Duration{Duration: 3 * gotime.Hour, Fsp: 0}),
	types.NewMysqlEnumDatum(types.Enum{Name: "b", Value: 2}),
}

func tableGenerate(g *vec.Gen, rng *rand.Rand) {
	// zero values and fsp
	for i := range tblFtPool {
		for k := 0; k < 4; k++ {
			ft := tblFtPool[i](rng)
			if k%2 == 1 {
				ft.AddFlag(mysql.UnsignedFlag)
			}
			g.Add("TestGetZeroValue", "zero_value", d2FtInOf(ft))
			if k == 0 {
				g.Add("sys/fsp", "optional_fsp", d2FtInOf(ft))
			}
		}
	}
	// cast_column: the TestCastValue cases, then random.
	for i := 0; i < 2200; i++ {
		ft := tblPick(rng, tblFtPool)(rng)
		if rng.IntN(4) == 0 {
			ft.AddFlag(mysql.UnsignedFlag)
		}
		src := "rand/cast"
		if i < 30 {
			src = "TestCastValue"
		}
		g.Add(src, "cast_column", tblCtxArg(rng), d2DatumInOf(tblPick(rng, tblCastDatums)), d2FtInOf(ft),
			tblPick(rng, []string{"", "c", "名字"}), rng.IntN(5) == 0, rng.IntN(5) == 0)
	}
	// tables
	for i := 0; i < 450; i++ {
		t := tblRandTable(rng, int64(100+i*10))
		js := tblJSON(t)
		src := fmt.Sprintf("rand/t%d", i)
		g.Add(src, "table_from_meta", js, rng.IntN(6) == 0)
		for k := 0; k < 4; k++ {
			off, mode := rng.IntN(len(t.Columns)), rng.IntN(4)
			if mode == 0 && t.Columns[off].DefaultIsExpr {
				// A default expression needs EvalSimpleAst (D09.2): a blocked-by stub in tidb-rs.
				mode = 1
			}
			g.Add(src, "default_value", tblCtxArg(rng), js, off, mode)
		}
		tblGenDecode(g, rng, t, js, src)
		g.Add(src, "find_cols", js, tblFindNames(rng, t), rng.IntN(2) == 0)
		g.Add(src, "can_skip", js, rng.IntN(len(t.Columns)), d2DatumInOf(tblPick(rng, tblCastDatums)))
		var offs []int
		for k := 0; k < len(t.Columns); k++ {
			if rng.IntN(3) > 0 {
				offs = append(offs, k)
			}
		}
		if rng.IntN(4) == 0 {
			offs = append(offs, -1)
		}
		g.Add(src, "table_scan", tblCtxArg(rng), js, offs, rng.IntN(5) == 0)
		if len(t.Indices) > 0 {
			row := tblRow(rng, t)
			g.Add(src, "restored", js, row, rng.IntN(2) == 0, rng.IntN(len(t.Indices)))
		}
	}
	// GetTimeValue with string values
	strs := []string{"CURRENT_TIMESTAMP", "Current_Timestamp", "CURRENT_DATE", "current_date", "0000-00-00 00:00:00",
		"2019-05-06 12:48:49", "2019-05-06 12:48:49.999999", "2019-02-30", "abc", "20190506", "", "2019-05-06 12:48:49 +08:00"}
	tps := []byte{mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDate}
	for i := 0; i < 320; i++ {
		var tz any
		if rng.IntN(2) == 0 {
			tz = tblPick(rng, []any{0, 3600, -7200})
		}
		g.Add("rand/time_value", "time_value", tblCtxArg(rng), tblPick(rng, strs), tblPick(rng, tps), rng.IntN(7), tz)
	}
}

func d2FtInOf(ft *types.FieldType) []any {
	var elems any
	if e := ft.GetElems(); e != nil {
		gs := make([]vec.GoString, len(e))
		for i, s := range e {
			gs[i] = vec.GoString(s)
		}
		elems = gs
	}
	return []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), ft.GetDecimal(), ft.GetCharset(), ft.GetCollate(), elems}
}

func tblFindNames(rng *rand.Rand, t *model.TableInfo) []string {
	var names []string
	for k := 0; k < 1+rng.IntN(3); k++ {
		switch rng.IntN(4) {
		case 0:
			names = append(names, "_tidb_rowid")
		case 1:
			names = append(names, "C0")
		case 2:
			names = append(names, "zz")
		default:
			names = append(names, t.Columns[rng.IntN(len(t.Columns))].Name.L)
		}
	}
	return names
}

// tblDatumFor: a value of the column's type (for rows and handles).
func tblDatumFor(rng *rand.Rand, c *model.ColumnInfo) types.Datum {
	switch c.GetType() {
	case mysql.TypeLonglong, mysql.TypeLong, mysql.TypeTiny, mysql.TypeYear:
		if mysql.HasUnsignedFlag(c.GetFlag()) {
			return types.NewUintDatum(uint64(rng.IntN(1000)))
		}
		return types.NewIntDatum(int64(rng.IntN(2000) - 1000))
	case mysql.TypeFloat:
		return types.NewFloat32Datum(float32(rng.IntN(100)) / 4)
	case mysql.TypeDouble:
		return types.NewFloat64Datum(float64(rng.IntN(100)) / 8)
	case mysql.TypeNewDecimal:
		return types.NewDecimalDatum(types.NewDecFromStringForTest(fmt.Sprintf("%d.%02d", rng.IntN(100), rng.IntN(100))))
	case mysql.TypeVarchar, mysql.TypeString, mysql.TypeBlob:
		s := tblPick(rng, []string{"a", "ab ", "abcdef", "", "Ab", "ABC  "})
		return types.NewCollationStringDatum(s, c.GetCollate())
	case mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDate:
		return types.NewTimeDatum(types.NewTime(types.FromDate(2000+rng.IntN(30), 1+rng.IntN(12), 1+rng.IntN(28), rng.IntN(24), 0, 0, 0), c.GetType(), c.GetDecimal()))
	case mysql.TypeDuration:
		return types.NewDurationDatum(types.Duration{Duration: gotime.Duration(rng.IntN(100)) * gotime.Minute, Fsp: c.GetDecimal()})
	case mysql.TypeEnum:
		return types.NewMysqlEnumDatum(types.Enum{Name: "b", Value: 2})
	case mysql.TypeSet:
		return types.NewMysqlSetDatum(types.Set{Name: "x", Value: 1}, c.GetCollate())
	case mysql.TypeBit:
		return types.NewMysqlBitDatum([]byte{byte(rng.IntN(2))})
	case mysql.TypeJSON:
		return types.NewJSONDatum(types.CreateBinaryJSON(int64(rng.IntN(9))))
	case mysql.TypeTiDBVectorFloat32:
		v, _ := types.ParseVectorFloat32("[1,2]")
		return types.NewVectorFloat32Datum(v)
	}
	return types.Datum{}
}

func tblRow(rng *rand.Rand, t *model.TableInfo) []any {
	var row []any
	for _, c := range t.Columns {
		d := tblDatumFor(rng, c)
		if rng.IntN(8) == 0 {
			d = types.Datum{}
		}
		row = append(row, d2DatumInOf(d))
	}
	return row
}

func tblGenDecode(g *vec.Gen, rng *rand.Rand, t *model.TableInfo, js, src string) {
	for k := 0; k < 4; k++ {
		// the stored row: a random subset of the columns (the others read defaults)
		row := make([]types.Datum, len(t.Columns))
		for i, c := range t.Columns {
			row[i] = tblDatumFor(rng, c)
		}
		var vals []types.Datum
		var ids []int64
		for i, c := range t.Columns {
			if rng.IntN(3) == 0 || (t.PKIsHandle && mysql.HasPriKeyFlag(c.GetFlag())) {
				continue
			}
			if rng.IntN(10) == 0 {
				vals = append(vals, types.Datum{})
			} else {
				vals = append(vals, row[i])
			}
			ids = append(ids, c.ID)
		}
		var enc rowcodec.Encoder
		value, err := tablecodec.EncodeRow(gotime.UTC, vals, ids, nil, nil, nil, &enc)
		if err != nil {
			continue
		}
		var h kv.Handle = kv.IntHandle(rng.Int64N(1000) - 10)
		if t.IsCommonHandle {
			var pk []types.Datum
			for _, ic := range t.Indices[0].Columns {
				d := row[ic.Offset]
				tablecodec.TruncateIndexValue(&d, ic, t.Columns[ic.Offset])
				pk = append(pk, d)
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
		var offs []int
		for i := range t.Columns {
			if rng.IntN(5) > 0 && t.Columns[i].State == model.StatePublic {
				offs = append(offs, i)
			}
		}
		if rng.IntN(6) == 0 {
			offs = append(offs, -1)
		}
		pubOffs := []int{}
		// DecodeRawRowData gets t.Cols() entries: map table offsets to positions in Cols().
		pub := 0
		posOf := map[int]int{}
		for i, c := range t.Columns {
			if c.State == model.StatePublic {
				posOf[i] = pub
				pub++
			}
		}
		for _, o := range offs {
			if o < 0 {
				pubOffs = append(pubOffs, -1)
			} else {
				pubOffs = append(pubOffs, posOf[o])
			}
		}
		g.Add(src, "decode_row", tblCtxArg(rng), js, specOf(h), pubOffs, hex.EncodeToString(value))
	}
}

// d064FileFuncs: the D06.4 functions of the files exempt whole (tableFileExempt), from
// data/func_blocks.tsv.
var d064FileFuncs = map[string][]string{
	"pkg/table/constraint.go":              {"ContainsAutoIncrementCol", "HasForeignKeyRefAction", "IfCheckConstraintExprBoolType", "IsSupportedExpr", "LoadCheckConstraint", "ToConstraint", "buildConstraintExpression", "checkConstraintChecker.Enter", "checkConstraintChecker.Leave", "checkForeignKeyRefActionByFKInfo", "hasSpecifiedCol", "removeInvalidCheckConstraintsInfo"},
	"pkg/table/tables/mutation_checker.go": {"CheckDataConsistency", "CompareIndexAndVal", "checkDataConsistency", "checkHandleConsistency", "checkIndexKeys", "checkRowInsertionConsistency", "collectTableMutationsFromBufferStage", "compareIndexData", "corruptMutations", "getColumnMaps", "getOrBuildColumnMaps", "injectMutationError"},
	"pkg/table/tables/partition.go":        {"FindPartitionByName", "ForKeyPruning.LocateKeyPartition", "ForListColumnPruning.HasDefault", "ForListColumnPruning.LocatePartition", "ForListColumnPruning.LocateRanges", "ForListColumnPruning.RebuildPartitionValueMapAndSorted", "ForListColumnPruning.buildListPartitionValueMapAndSorted", "ForListColumnPruning.buildPartitionValueMapAndSorted", "ForListColumnPruning.genConstExprKey", "ForListColumnPruning.genKey", "ForListPruning.Clone", "ForListPruning.GetDefaultIdx", "ForListPruning.LocatePartition", "ForListPruning.LocatePartitionByRange", "ForListPruning.buildListColumnsPruner", "ForListPruning.buildListPartitionValueMap", "ForListPruning.buildListPruner", "ForListPruning.locateListColumnsPartitionByRow", "ForListPruning.locateListPartitionByRow", "ForRangePruning.Compare", "GetReorganizedPartitionedTable", "ListPartitionGroup.findGroupIdx", "ListPartitionGroup.intersect", "ListPartitionGroup.union", "ListPartitionLocation.IsEmpty", "ListPartitionLocation.findByPartitionIdx", "NewListPartitionLocationHelper", "NewPartitionExprBuildCtx", "NewPartitionTableWithGivenSets", "PartitionExpr.GetPartColumnsForKeyPartition", "PartitionExpr.locateListPartition", "PartitionRecordKey", "btreeListColumnItem.Less", "checkConstraintForExchangePartition", "compareUnsigned", "dataEqRec", "dataForRangeColumnsPruning", "dataForRangePruning", "extractPartitionExprColumns", "findIdxByColUniqueID", "fixOldVersionPartitionInfo", "generateHashPartitionExpr", "generateKeyPartitionExpr", "generateListPartitionExpr", "generateRangePartitionExpr", "getColumnsOffset", "getRangeLocateExprs", "initEvalBuffer", "initEvalBufferType", "initPartition", "lessBtreeListColumnItem", "listPartitionLocationHelper.GetLocation", "listPartitionLocationHelper.Intersect", "listPartitionLocationHelper.Union", "listPartitionLocationHelper.UnionPartitionGroup", "newBtreeListColumnItem", "newBtreeListColumnSearchItem", "newPartitionExpr", "newPartitionedTable", "parseExpr", "parseSimpleExprWithNames", "partition.GetPartitionedTable", "partition.GetPhysicalID", "partitionTableWithGivenSets.AddRecord", "partitionTableWithGivenSets.GetAllPartitionIDs", "partitionTableWithGivenSets.GetPartitionByRow", "partitionTableWithGivenSets.UpdateRecord", "partitionedTable.AddRecord", "partitionedTable.CheckForExchangePartition", "partitionedTable.GetAllPartitionIDs", "partitionedTable.GetPartition", "partitionedTable.GetPartitionByRow", "partitionedTable.GetPartitionColumnIDs", "partitionedTable.GetPartitionColumnNames", "partitionedTable.GetPartitionIdxByRow", "partitionedTable.GetPartitionedTable", "partitionedTable.PartitionExpr", "partitionedTable.RemoveRecord", "partitionedTable.UpdateRecord", "partitionedTable.getPartition", "partitionedTable.locateHashPartition", "partitionedTable.locatePartition", "partitionedTable.locatePartitionCommon", "partitionedTable.locatePartitionIdx", "partitionedTable.locateRangeColumnPartition", "partitionedTable.locateRangePartition", "partitionedTable.locateReorgPartition", "partitionedTableAddRecord", "partitionedTableUpdateRecord", "rangePartitionExprStrings"},
	"pkg/table/tables/cache.go":            {"cachedTable.AddRecord", "cachedTable.Init", "cachedTable.RemoveRecord", "cachedTable.TryReadFromCache", "cachedTable.UpdateLockForRead", "cachedTable.UpdateRecord", "cachedTable.WriteLockAndKeepAlive", "cachedTable.loadDataFromOriginalTable", "cachedTable.lockForWrite", "cachedTable.renew", "cachedTable.renewLease", "cachedTable.updateLockForRead", "leaseFromTS", "newCachedTable", "newMemBuffer", "tokenLimit.PutStateRemoteHandle", "tokenLimit.TakeStateRemoteHandle", "tokenLimit.TakeStateRemoteHandleNoWait", "txnCtxAddCachedTable"},
	"pkg/table/tables/state_remote.go":     {"CachedTableLockType.String", "NewStateRemote", "stateRemoteHandle.Load", "stateRemoteHandle.LockForRead", "stateRemoteHandle.LockForWrite", "stateRemoteHandle.RenewReadLease", "stateRemoteHandle.RenewWriteLease", "stateRemoteHandle.beginTxn", "stateRemoteHandle.commitTxn", "stateRemoteHandle.execSQL", "stateRemoteHandle.loadRow", "stateRemoteHandle.lockForWriteOnce", "stateRemoteHandle.rollbackTxn", "stateRemoteHandle.runInTxn", "stateRemoteHandle.updateRow", "waitForLeaseExpire"},
	"pkg/table/tblctx/buffers.go":          {"CheckRowBuffer.AddColVal", "CheckRowBuffer.GetRowToCheck", "CheckRowBuffer.Reset", "EncodeRowBuffer.AddColVal", "EncodeRowBuffer.EncodeBinlogRowData", "EncodeRowBuffer.Reset", "EncodeRowBuffer.WriteMemBufferEncoded", "MutateBuffers.GetCheckRowBufferWithCap", "MutateBuffers.GetEncodeRowBufferWithCap", "MutateBuffers.GetWriteStmtBufs", "NewMutateBuffers", "ensureCapacityAndReset"},
	"pkg/table/tblctx/table.go":            {"NewTemporaryTableHandler", "TemporaryTableHandler.GetCommittedSize", "TemporaryTableHandler.GetDirtySize", "TemporaryTableHandler.Meta", "TemporaryTableHandler.UpdateTxnDeltaSize"},
	"pkg/table/tblsession/table.go":        {"MutateContext.AddCachedTableHandleToTxn", "MutateContext.AddTemporaryTableToTxn", "MutateContext.AlternativeAllocators", "MutateContext.ConnectionID", "MutateContext.EnableMutationChecker", "MutateContext.GetCachedTableSupport", "MutateContext.GetExchangePartitionDMLSupport", "MutateContext.GetExprCtx", "MutateContext.GetInfoSchemaToCheckExchangeConstraint", "MutateContext.GetMutateBuffers", "MutateContext.GetReservedRowIDAlloc", "MutateContext.GetRowEncodingConfig", "MutateContext.GetRowIDShardGenerator", "MutateContext.GetStatisticsSupport", "MutateContext.GetTemporaryTableSizeLimit", "MutateContext.GetTemporaryTableSupport", "MutateContext.InRestrictedSQL", "MutateContext.TxnAssertionLevel", "MutateContext.UpdatePhysicalTableDelta", "MutateContext.vars", "NewMutateContext"},
	"pkg/table/temptable/ddl.go":           {"GetTemporaryTableDDL", "checkLocalTemporaryExistsAndReturn", "ensureSessionData", "getSessionData", "newTemporaryTableFromTableInfo", "temporaryTableDDL.CreateLocalTemporaryTable", "temporaryTableDDL.DropLocalTemporaryTable", "temporaryTableDDL.TruncateLocalTemporaryTable", "temporaryTableDDL.clearTemporaryTableRecords"},
	"pkg/table/temptable/infoschema.go":    {"AttachLocalTemporaryTableInfoSchema", "DetachLocalTemporaryTableInfoSchema", "ensureLocalTemporaryTables", "getLocalTemporaryTables"},
	"pkg/table/temptable/interceptor.go":   {"NewTemporaryTableSnapshotInterceptor", "SessionSnapshotInterceptor", "TemporaryTableSnapshotInterceptor.OnBatchGet", "TemporaryTableSnapshotInterceptor.OnGet", "TemporaryTableSnapshotInterceptor.OnIter", "TemporaryTableSnapshotInterceptor.OnIterReverse", "TemporaryTableSnapshotInterceptor.batchGetTemporaryTableKeys", "TemporaryTableSnapshotInterceptor.iterTable", "TemporaryTableSnapshotInterceptor.temporaryTableInfoByID", "createUnionIter", "getKeyAccessedTableID", "getRangeAccessedTableID", "getSessionKey", "notTableRange"},
}
