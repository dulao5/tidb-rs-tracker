package domains

// Domain "pointget": block D12.1, the row decoding of the point get executor (tidb-rs
// VS1-12): executor.NewRowDecoder and executor.DecodeRowValToChunk, which decode a stored
// row value into a chunk for a plan schema.
//
// One op, decode_row_val. A record is a table (the table domain's random tables, so the
// same column kinds, defaults, states and keys), the schema the point get reads (the
// public columns in random order and subsets, as buildSchemaFromFields builds it, with
// virtual generated columns marked and sometimes _tidb_rowid), a handle (the kv domain's
// handle spec: int or common) and a stored row value:
//
//   - row format v2 (rowcodec.Encoder, small and large: column IDs above 255), sometimes
//     with a checksum, or row format v1 (tablecodec.EncodeOldRow);
//   - each stored row holds a random subset of the columns (the others are "added
//     columns": not in the row, read with their origin default value), and some stored
//     values are NULL (v2: the null column IDs; v1: the NilFlag datum);
//   - handle columns are read from the handle when the row does not hold them (int handle,
//     common handle incl. prefix columns and new-collation columns that need restored
//     data), and from the row when it does.
//
// The record key of a point get (tablecodec.EncodeRowKeyWithHandle of the physical table
// ID and the handle) is not repeated here: the tablecodec domain's "keys" records cover it
// ("rowh"), and tidb-rs replays them through the point get executor too (VS1-12 review:
// one vector set for the handle encoding).
//
// Values: tables, datums, field types, contexts and warnings as in the table domain. The
// session of NewRowDecoder / DecodeRowValToChunk is a mock context whose expression context
// is the record's static context (castCtx + location) and whose session time zone is the
// same location.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/sessionctx"
	"github.com/pingcap/tidb/pkg/tablecodec"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/rowcodec"

	_ "unsafe" // go:linkname
)

const pointgetSeed = 20261116

func init() {
	vec.Register(&vec.Domain{
		Name:     "pointget",
		Seed:     pointgetSeed,
		Errors:   append(append([]vec.NamedErr{}, datumErrors...), tableErrors...),
		Coverage: pointgetCoverage,
		Blocks:   []string{"D12.1"},
		Exempt:   pointgetExempt(),
		Gzip:     true,
		Generate: pointgetGenerate,
		Exec:     pointgetExec,
	})
}

var pointgetCoverage = map[string]vec.Coverage{
	"decode_row_val": {Min: 3000, GoFuncs: []string{"NewRowDecoder", "DecodeRowValToChunk", "decodeOldRowValToChunk",
		"tryDecodeFromHandle", "notPKPrefixCol", "getColInfoByID"}},
	"row_checksum": {Min: 1500, GoFuncs: []string{"fillRowChecksum", "shouldFillRowChecksum"}},
}

// pgFillRowChecksum is executor.fillRowChecksum (unexported; Go 1.23+ restricts pull
// linknames only for the standard library).
//
//go:linkname pgFillRowChecksum github.com/pingcap/tidb/pkg/executor.fillRowChecksum
func pgFillRowChecksum(sctx sessionctx.Context, start, end int, schema *expression.Schema, tblInfo *model.TableInfo,
	values [][]byte, handles []kv.Handle, req *chunk.Chunk, buf []byte) error

// pgCtx: a mock session whose GetExprCtx is the record's static expression context.
type pgCtx struct {
	*mock.Context
	ectx exprctx.ExprContext
}

func (c pgCtx) GetExprCtx() exprctx.ExprContext { return c.ectx }

// pgCol: one schema column. off >= 0 is the table column at that offset (virtual: a
// virtual generated column, VirtualExpr set); off == -1 is _tidb_rowid (ExtraHandleID);
// off == -2 is _tidb_row_checksum (ExtraRowChecksumID, the planner's column of
// buildPointGetChecksumCol: CHAR(MaxBlobWidth) in the default charset).
type pgCol struct {
	Off     int  `json:"off"`
	Virtual bool `json:"virtual,omitempty"`
}

// pgSchema: the plan schema (buildSchemaFromFields / colInfoToColumn: RetType is a clone of
// the column's field type).
func pgSchema(tbl *model.TableInfo, cols []pgCol) *expression.Schema {
	var ecols []*expression.Column
	for i, c := range cols {
		if c.Off == -2 {
			cs, cl := types.DefaultCharsetForType(mysql.TypeString)
			ft := types.NewFieldType(mysql.TypeString)
			ft.SetCharset(cs)
			ft.SetCollate(cl)
			ft.SetFlen(mysql.MaxBlobWidth)
			ft.SetDecimal(0)
			ecols = append(ecols, &expression.Column{
				RetType:  ft,
				ID:       model.ExtraRowChecksumID,
				UniqueID: model.ExtraRowChecksumID,
				Index:    i,
			})
			continue
		}
		if c.Off < 0 {
			ecols = append(ecols, &expression.Column{
				RetType:  types.NewFieldType(mysql.TypeLonglong),
				ID:       model.ExtraHandleID,
				UniqueID: int64(1000 + i),
				Index:    i,
			})
			continue
		}
		ci := tbl.Columns[c.Off]
		ec := &expression.Column{
			RetType:  ci.FieldType.Clone(),
			ID:       ci.ID,
			UniqueID: int64(1000 + i),
			Index:    i,
			OrigName: ci.Name.O,
		}
		if c.Virtual {
			// Only VirtualExpr != nil matters to the decoders (the value is filled later
			// by FillVirtualColumnValue).
			ec.VirtualExpr = expression.NewZero()
		}
		ecols = append(ecols, ec)
	}
	return expression.NewSchema(ecols...)
}

func pointgetExec(op string, args []json.RawMessage) (out any, err error) {
	switch op {
	case "decode_row_val":
		ectx, ev := tblCtx(args[0])
		loc := tblLoc(exArr(args[0])[1])
		tbl := tblIn(args[1])
		var cols []pgCol
		must(json.Unmarshal(args[2], &cols))
		h := handleOfSpec(arg[handleSpec](args, 3))
		val, _ := hex.DecodeString(arg[string](args, 4))
		sctx := pgCtx{Context: mock.NewContext(), ectx: ectx}
		sctx.GetSessionVars().TimeZone = loc
		schema := pgSchema(tbl, cols)
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": true}
				}
			}()
			rd := executor.NewRowDecoder(sctx, schema, tbl)
			fts := make([]*types.FieldType, len(schema.Columns))
			for i, c := range schema.Columns {
				fts[i] = c.RetType
			}
			chk := chunk.NewChunkWithCapacity(fts, 1)
			e := executor.DecodeRowValToChunk(sctx, schema, tbl, h, val, chk, rd)
			lens := make([]int, chk.NumCols())
			for i := range lens {
				lens[i] = chk.Column(i).Rows()
			}
			res := map[string]any{"lens": lens}
			if e != nil {
				res["err"] = vec.GoString(errors.Cause(e).Error())
			}
			// A complete row: every column got exactly one value.
			full := true
			for _, l := range lens {
				full = full && l == 1
			}
			if full {
				row := chk.GetRow(0)
				var ds []any
				for i, ft := range fts {
					ds = append(ds, d2DatumOut(row.GetDatum(i, ft)))
				}
				res["row"] = ds
			}
			r = res
		}()
		return map[string]any{"r": r, "w": ecWarns(ev.TruncateWarnings(0))}, nil
	case "row_checksum":
		// args: [ctx, session time zone (offset s), table, schema, handle, row value]. The
		// checksum column alone, as fillRowChecksum fills it into a fresh chunk (Next
		// calls it after DecodeRowValToChunk, which the decode_row_val op covers).
		ectx, ev := tblCtx(args[0])
		tz := gotime.FixedZone("Z", arg[int](args, 1))
		tbl := tblIn(args[2])
		var cols []pgCol
		must(json.Unmarshal(args[3], &cols))
		h := handleOfSpec(arg[handleSpec](args, 4))
		val, _ := hex.DecodeString(arg[string](args, 5))
		sctx := pgCtx{Context: mock.NewContext(), ectx: ectx}
		sctx.GetSessionVars().TimeZone = tz
		schema := pgSchema(tbl, cols)
		var r any
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = map[string]any{"panic": true}
				}
			}()
			fts := make([]*types.FieldType, len(schema.Columns))
			for i, c := range schema.Columns {
				fts[i] = c.RetType
			}
			chk := chunk.NewChunkWithCapacity(fts, 1)
			e := pgFillRowChecksum(sctx, 0, 1, schema, tbl, [][]byte{val}, []kv.Handle{h}, chk, nil)
			res := map[string]any{}
			if e != nil {
				res["err"] = vec.GoString(errors.Cause(e).Error())
			}
			// The checksum column (the others are untouched, empty).
			var out []any
			for i := range schema.Columns {
				c := chk.Column(i)
				switch {
				case c.Rows() == 0:
					out = append(out, "-")
				case c.IsNull(0):
					out = append(out, nil)
				default:
					out = append(out, c.GetString(0))
				}
			}
			res["cols"] = out
			r = res
		}()
		return map[string]any{"r": r, "w": ecWarns(ev.TruncateWarnings(0))}, nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// pgGenRow: one stored row of t and its handle; nil if Go cannot encode it.
func pgGenRow(rng *rand.Rand, t *model.TableInfo, loc *gotime.Location) (value []byte, h kv.Handle) {
	row := make([]types.Datum, len(t.Columns))
	for i, c := range t.Columns {
		row[i] = tblDatumFor(rng, c)
	}
	// The stored columns: a random subset; the others are "added columns" (origin
	// default). Sometimes all, sometimes none.
	keep := rng.IntN(3)
	var vals []types.Datum
	var ids []int64
	for i, c := range t.Columns {
		switch keep {
		case 0:
			if rng.IntN(3) == 0 {
				continue
			}
		case 1:
			if rng.IntN(8) == 0 {
				continue
			}
		case 2:
			if rng.IntN(20) > 0 {
				continue
			}
		}
		if t.PKIsHandle && mysql.HasPriKeyFlag(c.GetFlag()) && rng.IntN(4) > 0 {
			// TiDB does not store the int handle column in the row.
			continue
		}
		if rng.IntN(7) == 0 {
			vals = append(vals, types.Datum{}) // a stored NULL
		} else {
			vals = append(vals, row[i])
		}
		ids = append(ids, c.ID)
	}
	h = kv.IntHandle(rng.Int64N(2000) - 1000)
	if t.IsCommonHandle {
		var pk []types.Datum
		for _, ic := range t.Indices[0].Columns {
			d := row[ic.Offset]
			tablecodec.TruncateIndexValue(&d, ic, t.Columns[ic.Offset])
			pk = append(pk, d)
		}
		b, err := codec.EncodeKey(gotime.UTC, nil, pk...)
		if err != nil {
			return nil, nil
		}
		ch, err := kv.NewCommonHandle(b)
		if err != nil {
			return nil, nil
		}
		h = ch
	}
	var err error
	switch rng.IntN(5) {
	case 0: // row format v1
		value, err = tablecodec.EncodeOldRow(loc, vals, ids, nil, nil)
	case 1: // v2 with a checksum of the handle
		enc := rowcodec.Encoder{Enable: true}
		value, err = tablecodec.EncodeRow(loc, vals, ids, nil, nil, rowcodec.RawChecksum{Handle: h}, &enc)
	default:
		enc := rowcodec.Encoder{Enable: true}
		value, err = tablecodec.EncodeRow(loc, vals, ids, nil, nil, nil, &enc)
	}
	if err != nil {
		return nil, nil
	}
	return value, h
}

// pgGenSchema: the point get's schema over the public columns.
func pgGenSchema(rng *rand.Rand, t *model.TableInfo) []pgCol {
	var cols []pgCol
	for i, c := range t.Columns {
		if c.State != model.StatePublic || rng.IntN(5) == 0 {
			continue
		}
		cols = append(cols, pgCol{Off: i, Virtual: c.IsGenerated() && !c.GeneratedStored})
	}
	if rng.IntN(3) == 0 {
		rng.Shuffle(len(cols), func(i, j int) { cols[i], cols[j] = cols[j], cols[i] })
	}
	// _tidb_rowid: only tables without a clustered key have it (the planner refuses the
	// name otherwise). With a common handle Go would decode the handle's first column into
	// the BIGINT column of _tidb_rowid (NewRowDecoder takes ExtraHandleID as the handle
	// column): chunk.Column.AppendMyDecimal & co. write the value through unsafe into the
	// 8-byte element buffer, corrupting the heap of the generating process.
	if !t.PKIsHandle && !t.IsCommonHandle && rng.IntN(3) == 0 {
		cols = append(cols, pgCol{Off: -1})
	}
	return cols
}

func pointgetGenerate(g *vec.Gen, rng *rand.Rand) {
	for i := 0; i < 900; i++ {
		t := tblRandTable(rng, int64(1000+i*10))
		if t.State != model.StatePublic {
			t.State = model.StatePublic
		}
		if rng.IntN(5) == 0 {
			// Column IDs above 255: the large form of row format v2.
			for _, c := range t.Columns {
				c.ID += 300
			}
		}
		js := tblJSON(t)
		src := fmt.Sprintf("rand/t%d", i)
		for k := 0; k < 4; k++ {
			ctxArg := tblCtxArg(rng)
			loc := gotime.UTC
			if l := ctxArg[1]; l != nil {
				loc = gotime.FixedZone("Z", l.(int))
			}
			value, h := pgGenRow(rng, t, loc)
			if value == nil {
				continue
			}
			g.Add(src, "decode_row_val", ctxArg, js, pgGenSchema(rng, t), specOf(h), hex.EncodeToString(value))
		}
	}
	pgGenChecksum(g, rng)
}

// pgGenChecksum: row_checksum records, and decode_row_val records whose schema has
// _tidb_row_checksum (the decoders append NULL for it in v2; v1 rows reach Go's
// getColInfoByID(-4) == nil and panic). Int handles and clustered tables, rows that hold
// part of the columns (added columns: origin defaults), v1 rows (checksum NULL), and the
// session time zone UTC or not (TIMESTAMP values are decoded into it and converted back
// to UTC for the checksum).
func pgGenChecksum(g *vec.Gen, rng *rand.Rand) {
	zones := []int{0, 8 * 3600, -3 * 3600, 5*3600 + 1800, -(9*3600 + 1800)}
	for i := 0; i < 450; i++ {
		t := tblRandTable(rng, int64(20000+i*10))
		t.State = model.StatePublic
		if rng.IntN(3) == 0 {
			// A TIMESTAMP column with a non-zero value in most rows.
			c := tblColumn(rng, len(t.Columns))
			c.FieldType = *types.NewFieldType(mysql.TypeTimestamp)
			c.SetDecimal(rng.IntN(7))
			c.Version = model.ColumnInfoVersion2
			c.State = model.StatePublic
			c.Hidden = false
			c.ID = int64(len(t.Columns) + 1)
			c.OriginDefaultValue = tblPick(rng, []any{nil, "2019-05-06 12:48:49", "0000-00-00 00:00:00"})
			c.DefaultValue = c.OriginDefaultValue
			t.Columns = append(t.Columns, c)
		}
		js := tblJSON(t)
		src := fmt.Sprintf("rand/checksum%d", i)
		for k := 0; k < 4; k++ {
			ctxArg := tblCtxArg(rng)
			tz := tblPick(rng, zones)
			value, h := pgGenRow(rng, t, gotime.FixedZone("Z", tz))
			if value == nil {
				continue
			}
			cols := pgGenSchema(rng, t)
			pos := rng.IntN(len(cols) + 1)
			cols = append(cols[:pos], append([]pgCol{{Off: -2}}, cols[pos:]...)...)
			// The session zone of the checksum is the record's: ctxArg's location is the
			// expression context's (origin defaults), tz the session's.
			ctxArg[1] = tz
			g.Add(src, "row_checksum", ctxArg, tz, js, cols, specOf(h), hex.EncodeToString(value))
			if k == 0 {
				g.Add(src, "decode_row_val", ctxArg, js, cols, specOf(h), hex.EncodeToString(value))
			}
		}
	}
}
