package domains

// Domain "tablecodec": block D01.11 (pkg/tablecodec, pkg/util/rowcodec,
// pkg/tablecodec/rowindexcodec), read-path scope (scripts/blocks/rules.py: the row and
// index value encoders, the temp-index value codec and the DDL/unistore/TiCDC helpers are
// X). The write-path encoders still run here to build the inputs.
//
// Values:
//   - keys, row values, index values are hex; a datum is the datum2 form (d2DatumIn /
//     d2DatumOut), a field type the d2FtIn form; a handle is the kv domain's handle spec;
//   - a column info (rowcodec.ColInfo) is [id, isPKHandle, virtualGenCol, ft];
//   - a datum map output is [[id, datum], ...] sorted by id (Go map order is random);
//   - a location is null (time.UTC) or an offset in seconds (time.FixedZone);
//   - a table is its model.TableInfo JSON (json.Marshal), an index its offset in Indices;
//   - every decoder runs under its own recover: a panic is {"panic": true} (Rust must
//     panic there too; the message is Go's runtime text).
//
// The literals of tablecodec_test.go, rowcodec_test.go and rowindexcodec_test.go
// (cmd/minelits, testdata/tablecodec_literals.tsv) feed table/index IDs, handles, column
// IDs and string values, recorded with their test function as src.

import (
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/table/tables"
	"github.com/pingcap/tidb/pkg/tablecodec"
	"github.com/pingcap/tidb/pkg/tablecodec/rowindexcodec"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/rowcodec"
)

const tablecodecSeed = 20261110

//go:embed testdata/tablecodec_literals.tsv
var tablecodecLiteralsTSV string

func init() {
	vec.Register(&vec.Domain{
		Name:     "tablecodec",
		Seed:     tablecodecSeed,
		Errors:   datumErrors,
		Coverage: tablecodecCoverage,
		Blocks:   []string{"D01.11"},
		Exempt:   tablecodecExempt,
		Gzip:     true,
		Generate: tablecodecGenerate,
		Exec:     tablecodecExec,
	})
}

var tablecodecExempt = map[string]string{
	"init":                  "sets kv.DecodeTableIDFunc; tidb-rs returns the function from tablecodec::init() (covered through key_decode \"tid_func\")",
	"RowData.Len":           "sort.Interface method; callers sort RowData by column ID before Checksum (covered: checksum op sorts)",
	"RowData.Less":          "sort.Interface method (covered: checksum op sorts)",
	"RowData.Swap":          "sort.Interface method (covered: checksum op sorts)",
	"NewDecoder":            "constructor of the unexported base decoder; the exported decoders build it (covered by row)",
	"FieldTypeFromModelColumn": "FieldType.Clone of a model column (covered: the index op's columns are built with it)",
}

var tablecodecCoverage = map[string]vec.Coverage{
	"keys": {Min: 600, GoFuncs: []string{"TablePrefix", "EncodeRowKey", "EncodeRowKeyWithHandle", "EncodeRecordKey",
		"EncodeTableIndexPrefix", "EncodeTablePrefix", "appendTableRecordPrefix", "appendTableIndexPrefix",
		"GenTableRecordPrefix", "GenTableIndexPrefix", "GenTablePrefix", "GetTableHandleKeyRange",
		"GetTableIndexKeyRange", "EncodeIndexSeekKey"}},
	"key_decode": {Min: 3000, GoFuncs: []string{"DecodeRecordKey", "DecodeRowKey", "DecodeKeyHead", "DecodeIndexKey",
		"DecodeValuesBytesToStrings", "DecodeTableID", "DecodeMetaKey", "IsRecordKey", "IsIndexKey", "IsTableKey",
		"IsTempIndexKey", "IndexKey2TempIndexKey", "hasTablePrefix", "hasRecordPrefixSep", "GetKeyKind"}},
	"encode_value": {Min: 500, GoFuncs: []string{"EncodeValue", "flatten"}},
	"unflatten":    {Min: 1500, GoFuncs: []string{"Unflatten", "DecodeColumnValue", "DecodeColumnValueWithDatum"}},
	"row": {Min: 2000, GoFuncs: []string{"DecodeRowWithMapNew", "DecodeRowWithMap", "DecodeRowToDatumMap",
		"DecodeHandleToDatumMap", "decodeHandleToDatum", "CutRowNew", "IsNewFormat",
		"NewDatumMapDecoder", "DatumMapDecoder.DecodeToDatumMap", "DatumMapDecoder.decodeColDatum",
		"NewChunkDecoder", "ChunkDecoder.DecodeToChunk", "ChunkDecoder.decodeColToChunk",
		"ChunkDecoder.tryAppendHandleColumn", "NewByteDecoder", "BytesDecoder.DecodeToBytes",
		"BytesDecoder.DecodeToBytesNoHandle", "BytesDecoder.decodeToBytesInternal", "BytesDecoder.encodeOldDatum",
		"BytesDecoder.tryDecodeHandle", "fieldType2Flag",
		"row.large", "row.hasChecksum", "row.hasExtraChecksum", "row.getOffsets", "row.getData", "row.fromBytes",
		"row.findColID", "row.ChecksumVersion", "row.GetChecksum", "row.GetExtraChecksum",
		"bytesToU32Slice", "bytes2U16Slice", "decodeInt", "decodeUint"}},
	"checksum": {Min: 300, GoFuncs: []string{"RowData.Checksum", "ColData.Encode", "appendDatumForChecksum",
		"appendLengthValue"}},
	"index": {Min: 3000, GoFuncs: []string{"GenIndexKey", "GetIndexKeyBuf", "TruncateIndexValues", "TruncateIndexValue",
		"DecodeIndexKV", "DecodeIndexKVEx", "DecodeIndexHandle", "DecodeHandleInIndexValue",
		"DecodeIntHandleInIndexValue", "decodeHandleInIndexKey", "SplitIndexValue",
		"splitIndexValueForIndexValueVersion0", "splitIndexValueForClusteredIndexVersion1", "getIndexVersion",
		"CutIndexKeyNew", "CutIndexKeyTo", "decodeIndexKvOldCollation", "decodeIndexKvGeneral",
		"decodeIndexKvForClusteredIndexVersion1", "reEncodeHandle", "reEncodeHandleTo",
		"reEncodeHandleConsiderNewCollation", "decodeRestoredValues", "decodeRestoredValuesV5",
		"buildColumnIDOffsets", "buildRestoredColumn"}},
	"index_raw":     {Min: 1500, GoFuncs: []string{"DecodeIndexKV", "DecodeIndexHandle", "SplitIndexValue"}},
	"truncate":      {Min: 300, GoFuncs: []string{"TruncateIndexValue"}},
	"verify_ranges": {Min: 100, GoFuncs: []string{"VerifyTableIDForRanges"}},
}

// TablecodecMinedTests: the Go test functions whose literals are in testdata/tablecodec_literals.tsv.
func TablecodecMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, l := range parseDatumLiterals(tablecodecLiteralsTSV) {
		if !seen[l.fn] {
			seen[l.fn] = true
			fns = append(fns, l.fn)
		}
	}
	return fns
}

// ---------------------------------------------------------------- helpers

func tcTry(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": true}
		}
	}()
	return f()
}

func tcLoc(p []json.RawMessage, i int) *gotime.Location {
	if string(p[i]) == "null" {
		return gotime.UTC
	}
	return gotime.FixedZone("Z", arg[int](p, i))
}

// tcLocNil: null is a nil *time.Location.
func tcLocNil(p []json.RawMessage, i int) *gotime.Location {
	if string(p[i]) == "null" {
		return nil
	}
	return gotime.FixedZone("Z", arg[int](p, i))
}

func tcHandle(p []json.RawMessage, i int) kv.Handle {
	if string(p[i]) == "null" {
		return nil
	}
	return handleOfSpec(arg[handleSpec](p, i))
}

func tcHandleOut(h kv.Handle) any {
	if h == nil {
		return nil
	}
	if ph, ok := h.(kv.PartitionHandle); ok && ph.Handle == nil {
		return map[string]any{"nil_inner": true, "pid": ph.PartitionID}
	}
	return specOf(h)
}

func tcColInfos(p []json.RawMessage, i int) []rowcodec.ColInfo {
	var out []rowcodec.ColInfo
	for _, q := range rawList(arg[[]json.RawMessage](p, i)) {
		out = append(out, rowcodec.ColInfo{ID: arg[int64](q, 0), IsPKHandle: arg[bool](q, 1),
			VirtualGenCol: arg[bool](q, 2), Ft: d2FtIn(arg[[]json.RawMessage](q, 3))})
	}
	return out
}

// tcColsMap: [[id, ft], ...] -> map[int64]*FieldType.
func tcColsMap(p []json.RawMessage, i int) map[int64]*types.FieldType {
	m := map[int64]*types.FieldType{}
	for _, q := range rawList(arg[[]json.RawMessage](p, i)) {
		m[arg[int64](q, 0)] = d2FtIn(arg[[]json.RawMessage](q, 1))
	}
	return m
}

func tcMapOut(m map[int64]types.Datum) any {
	if m == nil {
		return nil
	}
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	out := []any{}
	for _, id := range ids {
		out = append(out, []any{id, d2DatumOut(m[id])})
	}
	return out
}

func tcBytesOut(bs [][]byte) any {
	if bs == nil {
		return nil
	}
	out := make([]any, len(bs))
	for i, b := range bs {
		if b == nil {
			out[i] = nil
		} else {
			out[i] = hx(b)
		}
	}
	return out
}

func tcStrs(ss []string) any {
	if ss == nil {
		return nil
	}
	out := make([]vec.GoString, len(ss))
	for i, s := range ss {
		out[i] = vec.GoString(s)
	}
	return out
}

func tcErr(err error) any { return d2ErrMsg(err) }

// ---------------------------------------------------------------- exec

func tablecodecExec(op string, p []json.RawMessage) (out any, err error) {
	collate.SetNewCollationEnabledForTest(true)
	defer collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "keys":
		tid, idx := arg[int64](p, 0), arg[int64](p, 1)
		h := tcHandle(p, 2)
		enc := hexArg(p, 3)
		hs, he := tablecodec.GetTableHandleKeyRange(tid)
		is, ie := tablecodec.GetTableIndexKeyRange(tid, idx)
		o := map[string]any{
			"tp": hx(tablecodec.TablePrefix()), "rp": hx(tablecodec.GenTableRecordPrefix(tid)),
			"ip": hx(tablecodec.GenTableIndexPrefix(tid)), "tbp": hx(tablecodec.GenTablePrefix(tid)),
			"etp": hx(tablecodec.EncodeTablePrefix(tid)), "eip": hx(tablecodec.EncodeTableIndexPrefix(tid, idx)),
			"seek": hx(tablecodec.EncodeIndexSeekKey(tid, idx, enc)), "row": hx(tablecodec.EncodeRowKey(tid, enc)),
			"hr": []string{hx(hs), hx(he)}, "ir": []string{hx(is), hx(ie)},
		}
		if h != nil {
			o["rowh"] = hx(tablecodec.EncodeRowKeyWithHandle(tid, h))
			o["rec"] = hx(tablecodec.EncodeRecordKey(tablecodec.GenTableRecordPrefix(tid), h))
		}
		return o, nil
	case "key_decode":
		k := hexArg(p, 0)
		o := map[string]any{}
		o["rec"] = tcTry(func() any {
			tid, h, err := tablecodec.DecodeRecordKey(k)
			return map[string]any{"tid": tid, "h": tcHandleOut(h), "err": tcErr(err)}
		})
		o["row"] = tcTry(func() any {
			h, err := tablecodec.DecodeRowKey(k)
			if err != nil {
				return map[string]any{"err": tcErr(err)}
			}
			return map[string]any{"h": tcHandleOut(h)}
		})
		o["head"] = tcTry(func() any {
			tid, iid, isRec, err := tablecodec.DecodeKeyHead(k)
			return map[string]any{"tid": tid, "iid": iid, "rec": isRec, "err": tcErr(err)}
		})
		o["idx"] = tcTry(func() any {
			tid, iid, vals, err := tablecodec.DecodeIndexKey(k)
			return map[string]any{"tid": tid, "iid": iid, "vals": tcStrs(vals), "err": tcErr(err)}
		})
		o["vals"] = tcTry(func() any {
			vals, err := tablecodec.DecodeValuesBytesToStrings(k)
			return map[string]any{"vals": tcStrs(vals), "err": tcErr(err)}
		})
		o["tid"] = tcTry(func() any { return tablecodec.DecodeTableID(k) })
		o["tid_func"] = tcTry(func() any { return kv.DecodeTableIDFunc(k) })
		o["meta"] = tcTry(func() any {
			key, field, err := tablecodec.DecodeMetaKey(k)
			if err != nil {
				return map[string]any{"err": tcErr(err)}
			}
			return map[string]any{"key": hx(key), "field": hx(field)}
		})
		o["is"] = tcTry(func() any {
			return []bool{tablecodec.IsRecordKey(k), tablecodec.IsIndexKey(k), tablecodec.IsTableKey(k)}
		})
		o["kind"] = int(rowindexcodec.GetKeyKind(k))
		o["temp"] = tcTry(func() any { return tablecodec.IsTempIndexKey(k) })
		o["to_temp"] = tcTry(func() any {
			c := append([]byte(nil), k...)
			tablecodec.IndexKey2TempIndexKey(c)
			return hx(c)
		})
		return o, nil
	case "encode_value":
		loc := tcLocNil(p, 0)
		d := d2DatumIn(arg[[]json.RawMessage](p, 2))
		b, err := tablecodec.EncodeValue(loc, hexArg(p, 1), d)
		return map[string]any{"b": hx(b), "nil": b == nil}, err
	case "unflatten":
		d := d2DatumIn(arg[[]json.RawMessage](p, 0))
		ft := d2FtIn(arg[[]json.RawMessage](p, 1))
		loc := tcLocNil(p, 2)
		data := hexArg(p, 3)
		o := map[string]any{}
		o["u"] = tcTry(func() any {
			r, err := tablecodec.Unflatten(d, ft, loc)
			return map[string]any{"d": d2DatumOut(r), "err": tcErr(err)}
		})
		o["dcv"] = tcTry(func() any {
			r, err := tablecodec.DecodeColumnValue(data, ft, loc)
			return map[string]any{"d": d2DatumOut(r), "err": tcErr(err)}
		})
		o["dcvw"] = tcTry(func() any {
			var r types.Datum
			err := tablecodec.DecodeColumnValueWithDatum(data, ft, loc, &r)
			return map[string]any{"d": d2DatumOut(r), "err": tcErr(err)}
		})
		return o, nil
	case "row":
		return tcRow(p), nil
	case "checksum":
		cols := rawList(arg[[]json.RawMessage](p, 0))
		ds := datumsArg(p, 1)
		loc := tcLocNil(p, 2)
		infos := make([]*model.ColumnInfo, len(cols))
		rd := rowcodec.RowData{}
		var encs []any
		for i, q := range cols {
			infos[i] = &model.ColumnInfo{ID: arg[int64](q, 0), FieldType: *d2FtIn(arg[[]json.RawMessage](q, 1))}
			cd := rowcodec.ColData{ColumnInfo: infos[i], Datum: &ds[i]}
			rd.Cols = append(rd.Cols, cd)
			encs = append(encs, tcTry(func() any {
				b, err := cd.Encode(loc, nil)
				return map[string]any{"b": hx(b), "err": tcErr(err)}
			}))
		}
		sort.Sort(rd)
		sum := tcTry(func() any {
			c, err := rd.Checksum(loc)
			return map[string]any{"sum": c, "err": tcErr(err)}
		})
		return map[string]any{"encs": encs, "sum": sum}, nil
	case "index":
		return tcIndex(p), nil
	case "index_raw":
		return tcIndexDecode(hexArg(p, 0), hexArg(p, 1), arg[int](p, 2), tcColInfos(p, 3), arg[bool](p, 4)), nil
	case "truncate":
		d := d2DatumIn(arg[[]json.RawMessage](p, 0))
		idxCol := &model.IndexColumn{Length: arg[int](p, 1)}
		col := &model.ColumnInfo{}
		col.SetCharset(arg[string](p, 2))
		col.SetCollate(arg[string](p, 3))
		tablecodec.TruncateIndexValue(&d, idxCol, col)
		return d2DatumOut(d), nil
	case "verify_ranges":
		var parts [][]kv.KeyRange
		for _, part := range arg[[][][2]string](p, 0) {
			var rs []kv.KeyRange
			for _, r := range part {
				s, _ := hex.DecodeString(r[0])
				e, _ := hex.DecodeString(r[1])
				rs = append(rs, kv.KeyRange{StartKey: s, EndKey: e})
			}
			parts = append(parts, rs)
		}
		var krs *kv.KeyRanges
		if arg[bool](p, 1) {
			krs = kv.NewPartitionedKeyRanges(parts)
		} else {
			var all []kv.KeyRange
			for _, part := range parts {
				all = append(all, part...)
			}
			krs = kv.NewNonPartitionedKeyRanges(all)
		}
		tids, err := tablecodec.VerifyTableIDForRanges(krs)
		return map[string]any{"tids": tids}, err
	}
	panic("tablecodec: unknown op " + op)
}

// tcRow: [row, cols ([[id, ft]]), loc, handle|null, handleColIDs, colInfos, commitTS,
// defaults (per colInfo: datum|null) | null, defBytes (per colInfo: hex|null) | null, cache].
func tcRow(p []json.RawMessage) any {
	b := hexArg(p, 0)
	cols := tcColsMap(p, 1)
	loc := tcLocNil(p, 2)
	h := tcHandle(p, 3)
	hids := arg[[]int64](p, 4)
	infos := tcColInfos(p, 5)
	commitTS := arg[uint64](p, 6)
	o := map[string]any{}
	o["map"] = tcTry(func() any {
		m, err := tablecodec.DecodeRowToDatumMap(b, cols, loc)
		return map[string]any{"m": tcMapOut(m), "err": tcErr(err)}
	})
	o["hmap"] = tcTry(func() any {
		m, err := tablecodec.DecodeHandleToDatumMap(h, hids, cols, loc, nil)
		return map[string]any{"m": tcMapOut(m), "err": tcErr(err)}
	})
	newFmt := len(b) > 0 && rowcodec.IsNewFormat(b)
	if !newFmt {
		o["v1"] = tcTry(func() any {
			m, err := tablecodec.DecodeRowWithMap(b, cols, loc, nil)
			return map[string]any{"m": tcMapOut(m), "err": tcErr(err)}
		})
		colIDs := map[int64]int{}
		for i, ci := range infos {
			colIDs[ci.ID] = i
		}
		o["cut"] = tcTry(func() any {
			r, err := tablecodec.CutRowNew(b, colIDs)
			return map[string]any{"r": tcBytesOut(r), "err": tcErr(err)}
		})
		return o
	}
	o["v2"] = tcTry(func() any {
		m, err := tablecodec.DecodeRowWithMapNew(b, cols, loc, nil)
		return map[string]any{"m": tcMapOut(m), "err": tcErr(err)}
	})
	o["dm"] = tcTry(func() any {
		dec := rowcodec.NewDatumMapDecoder(infos, loc)
		m, err := dec.DecodeToDatumMap(b, nil)
		c1, ok1 := dec.GetChecksum()
		c2, ok2 := dec.GetExtraChecksum()
		return map[string]any{"m": tcMapOut(m), "err": tcErr(err), "cs": []any{c1, ok1, c2, ok2, dec.ChecksumVersion()}}
	})
	var defs [][]json.RawMessage
	if string(p[7]) != "null" {
		defs = rawList(arg[[]json.RawMessage](p, 7))
	}
	fts := make([]*types.FieldType, len(infos))
	for i, ci := range infos {
		fts[i] = ci.Ft
	}
	o["chk"] = tcTry(func() any {
		var def func(i int, chk *chunk.Chunk) error
		if defs != nil {
			def = func(i int, chk *chunk.Chunk) error {
				if defs[i] == nil || string(defs[i][0]) == "null" {
					chk.AppendNull(i)
					return nil
				}
				d := d2DatumIn(defs[i])
				chk.AppendDatum(i, &d)
				return nil
			}
		}
		chk := chunk.NewChunkWithCapacity(fts, 1)
		dec := rowcodec.NewChunkDecoder(infos, hids, def, loc)
		err := dec.DecodeToChunk(b, commitTS, h, chk)
		if err != nil {
			return map[string]any{"err": tcErr(err)}
		}
		var cells []any
		for c, ft := range fts {
			cells = append(cells, tcTry(func() any {
				if chk.NumRows() == 0 || chk.Column(c).Rows() == 0 {
					return "none"
				}
				if chk.Column(c).IsNull(0) {
					return nil
				}
				return d2DatumOut(chk.GetRow(0).GetDatum(c, ft))
			}))
		}
		return map[string]any{"cells": cells}
	})
	var defBytes []any
	if string(p[8]) != "null" {
		defBytes = arg[[]any](p, 8)
	}
	cache := hexArg(p, 9)
	offs := map[int64]int{}
	for i, ci := range infos {
		offs[ci.ID] = i
	}
	mkBytesDec := func() *rowcodec.BytesDecoder {
		var def func(i int) ([]byte, error)
		if defBytes != nil {
			def = func(i int) ([]byte, error) {
				s, ok := defBytes[i].(string)
				if !ok {
					return nil, nil
				}
				return hex.DecodeString(s)
			}
		}
		return rowcodec.NewByteDecoder(infos, hids, def, loc)
	}
	o["bytes"] = tcTry(func() any {
		r, err := mkBytesDec().DecodeToBytes(offs, h, b, append([]byte(nil), cache...))
		return map[string]any{"r": tcBytesOut(r), "err": tcErr(err)}
	})
	o["bytes_nh"] = tcTry(func() any {
		r, err := mkBytesDec().DecodeToBytesNoHandle(offs, b)
		return map[string]any{"r": tcBytesOut(r), "err": tcErr(err)}
	})
	return o
}

// tcIndex: [tbl JSON, idx offset, phyTblID, datums, handle|null, value, colsLen, columns, loc, src flags].
func tcIndex(p []json.RawMessage) any {
	var tbl model.TableInfo
	if err := json.Unmarshal([]byte(arg[string](p, 0)), &tbl); err != nil {
		panic(err)
	}
	idx := tbl.Indices[arg[int](p, 1)]
	phy := arg[int64](p, 2)
	ds := datumsArg(p, 3)
	h := tcHandle(p, 4)
	value := hexArg(p, 5)
	colsLen := arg[int](p, 6)
	cols := tcColInfos(p, 7)
	loc := tcLoc(p, 8)
	o := map[string]any{}
	var key []byte
	o["gen"] = tcTry(func() any {
		vals := append([]types.Datum(nil), ds...)
		k, distinct, err := tablecodec.GenIndexKey(loc, &tbl, idx, phy, vals, h, nil)
		key = k
		return map[string]any{"k": hx(k), "nil": k == nil, "distinct": distinct, "err": tcErr(err), "vals": datumOuts(vals)}
	})
	if key == nil {
		return o
	}
	o["dec"] = tcIndexDecode(key, value, colsLen, cols, idx.Unique)
	return o
}

func tcIndexDecode(key, value []byte, colsLen int, cols []rowcodec.ColInfo, unique bool) any {
	o := map[string]any{}
	for i, hs := range []tablecodec.HandleStatus{tablecodec.HandleDefault, tablecodec.HandleIsUnsigned, tablecodec.HandleNotNeeded} {
		c := cols
		if hs == tablecodec.HandleNotNeeded && len(c) > colsLen {
			c = c[:colsLen]
		}
		o["kv"+string(rune('0'+i))] = tcTry(func() any {
			r, err := tablecodec.DecodeIndexKV(key, value, colsLen, hs, c)
			return map[string]any{"r": tcBytesOut(r), "err": tcErr(err)}
		})
	}
	o["kvex"] = tcTry(func() any {
		pre := make([][]byte, colsLen, colsLen+len(cols))
		r, err := tablecodec.DecodeIndexKVEx(key, value, colsLen, tablecodec.HandleDefault, cols, []byte{0xaa}, pre)
		return map[string]any{"r": tcBytesOut(r), "err": tcErr(err)}
	})
	o["h"] = tcTry(func() any {
		h, err := tablecodec.DecodeIndexHandle(key, value, colsLen)
		return map[string]any{"h": tcHandleOut(h), "err": tcErr(err)}
	})
	if unique {
		o["hv"] = tcTry(func() any {
			h, err := tablecodec.DecodeHandleInIndexValue(value)
			return map[string]any{"h": tcHandleOut(h), "err": tcErr(err)}
		})
	}
	o["seg"] = tcTry(func() any {
		s := tablecodec.SplitIndexValue(value)
		return []any{tcNilHex(s.CommonHandle), tcNilHex(s.PartitionID), tcNilHex(s.RestoredValues), tcNilHex(s.IntHandle)}
	})
	o["cut"] = tcTry(func() any {
		vs, rest, err := tablecodec.CutIndexKeyNew(key, colsLen)
		return map[string]any{"vs": tcBytesOut(vs), "rest": hx(rest), "err": tcErr(err)}
	})
	return o
}

func tcNilHex(b []byte) any {
	if b == nil {
		return nil
	}
	return hx(b)
}

// ---------------------------------------------------------------- generate

type tcCol struct {
	ft  []any // d2FtIn form
	gen func(rng *rand.Rand, lits []string) []any
}

func tcFt(tp byte, flag uint, flen, dec int, cs, co string, elems []vec.GoString) []any {
	var e any
	if elems != nil {
		e = elems
	}
	return []any{tp, flag, flen, dec, cs, co, e}
}

func tcStrPool(lits []string) []string {
	strs := []string{"", "a", "A", "a ", "a  ", " a", "abc", "ABC", "ä", "Ä", "ß", "ss", "中文", "中文  ", "Ｆ",
		"𠀀", "K", "k", "K", "\x00", "a\x00b", "12345678", "123456789", strings.Repeat("x", 30),
		strings.Repeat("y", 300) + "  ", "\xff\xfe", "aé "}
	return append(strs, lits...)
}

func tcCols() []tcCol {
	un := uint(mysql.UnsignedFlag)
	ints := []int64{0, 1, -1, 127, -128, 32767, -32768, 1 << 31, -(1 << 31), math.MaxInt64, math.MinInt64, 1 << 40}
	intGen := func(rng *rand.Rand, _ []string) []any { return []any{types.KindInt64, ints[rng.IntN(len(ints))]} }
	uintGen := func(rng *rand.Rand, _ []string) []any {
		return []any{types.KindUint64, []uint64{0, 1, 255, 65535, 1 << 32, math.MaxUint64}[rng.IntN(6)]}
	}
	strGen := func(co string) func(rng *rand.Rand, lits []string) []any {
		return func(rng *rand.Rand, lits []string) []any {
			strs := tcStrPool(lits)
			return []any{types.KindString, vec.GoString(strs[rng.IntN(len(strs))]), co}
		}
	}
	var cols []tcCol
	for _, tp := range []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong} {
		cols = append(cols, tcCol{tcFt(tp, 0, -1, 0, "binary", "binary", nil), intGen})
		cols = append(cols, tcCol{tcFt(tp, un, -1, 0, "binary", "binary", nil), uintGen})
	}
	cols = append(cols, tcCol{tcFt(mysql.TypeYear, 0, 4, 0, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
		return []any{types.KindInt64, []int64{0, 1901, 2155, 2024}[rng.IntN(4)]}
	}})
	fl := []float64{0, math.Copysign(0, -1), 1.5, -2.25, 1e30, 3.4e38, math.SmallestNonzeroFloat64}
	cols = append(cols,
		tcCol{tcFt(mysql.TypeFloat, 0, -1, -1, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			return []any{types.KindFloat32, fBits(float64(float32(fl[rng.IntN(len(fl))])))}
		}},
		tcCol{tcFt(mysql.TypeDouble, 0, -1, -1, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			return []any{types.KindFloat64, fBits(fl[rng.IntN(len(fl))])}
		}})
	for _, co := range []string{"utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci",
		"utf8mb4_0900_bin", "utf8_bin", "latin1_bin", "gbk_chinese_ci", "gbk_bin", "ascii_bin"} {
		cs := strings.SplitN(co, "_", 2)[0]
		cols = append(cols, tcCol{tcFt(mysql.TypeVarchar, 0, 64, -1, cs, co, nil), strGen(co)})
		cols = append(cols, tcCol{tcFt(mysql.TypeString, 0, 10, -1, cs, co, nil), strGen(co)})
	}
	cols = append(cols,
		tcCol{tcFt(mysql.TypeVarchar, uint(mysql.BinaryFlag), 64, -1, "binary", "binary", nil), func(rng *rand.Rand, lits []string) []any {
			strs := tcStrPool(lits)
			return []any{types.KindBytes, hx([]byte(strs[rng.IntN(len(strs))]))}
		}},
		tcCol{tcFt(mysql.TypeString, uint(mysql.BinaryFlag), 8, -1, "binary", "binary", nil), strGen("binary")},
		tcCol{tcFt(mysql.TypeBlob, uint(mysql.BinaryFlag), -1, -1, "binary", "binary", nil), strGen("binary")},
		tcCol{tcFt(mysql.TypeBlob, 0, -1, -1, "utf8mb4", "utf8mb4_bin", nil), strGen("utf8mb4_bin")})
	ctx := types.DefaultStmtNoWarningContext
	for _, tp := range []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp} {
		for _, fsp := range []int{0, 3, 6} {
			tp, fsp := tp, fsp
			if tp == mysql.TypeDate && fsp > 0 {
				continue
			}
			cols = append(cols, tcCol{tcFt(tp, 0, -1, fsp, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
				s := []string{"2011-11-10 11:11:11.123456", "0000-00-00", "1970-01-01 00:00:01", "2038-01-19 03:14:07",
					"9999-12-31 23:59:59.999999", "1000-01-01"}[rng.IntN(6)]
				t, err := types.ParseTime(ctx, s, tp, fsp)
				if err != nil {
					t = types.NewTime(types.ZeroCoreTime, tp, fsp)
				}
				return []any{types.KindMysqlTime, rawOfTime(t)}
			}})
		}
	}
	cols = append(cols, tcCol{tcFt(mysql.TypeDuration, 0, -1, 3, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
		return []any{types.KindMysqlDuration, []int64{0, 1e9, -1500e6, 3723e9 + 5e5, 838*3600e9 + 59*60e9 + 59e9}[rng.IntN(5)], 3}
	}})
	for _, fd := range [][2]int{{10, 3}, {65, 30}, {5, 0}} {
		fd := fd
		cols = append(cols, tcCol{tcFt(mysql.TypeNewDecimal, 0, fd[0], fd[1], "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			return []any{types.KindMysqlDecimal, []string{"0", "1.50", "-1.5", "123.4567", "0.000", "99999.999", "-0.001"}[rng.IntN(7)]}
		}})
	}
	ab := []vec.GoString{"a", "b", "C", "ä", "b "}
	cols = append(cols,
		tcCol{tcFt(mysql.TypeEnum, 0, -1, -1, "utf8mb4", "utf8mb4_general_ci", ab), func(rng *rand.Rand, _ []string) []any {
			v := uint64(rng.IntN(7))
			n := ""
			if v >= 1 && v <= 5 {
				n = string(ab[v-1])
			}
			return []any{types.KindMysqlEnum, v, vec.GoString(n), "utf8mb4_general_ci"}
		}},
		tcCol{tcFt(mysql.TypeSet, 0, -1, -1, "utf8mb4", "utf8mb4_bin", ab), func(rng *rand.Rand, _ []string) []any {
			v := uint64(rng.IntN(40))
			return []any{types.KindMysqlSet, v, "", "utf8mb4_bin"}
		}},
		tcCol{tcFt(mysql.TypeBit, un, 10, 0, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			return []any{types.KindMysqlBit, []string{"00", "0001", "03ff", "0102"}[rng.IntN(4)]}
		}},
		tcCol{tcFt(mysql.TypeBit, un, 64, 0, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			return []any{types.KindMysqlBit, []string{"00", "ffffffffffffffff", "8000000000000000"}[rng.IntN(3)]}
		}},
		tcCol{tcFt(mysql.TypeJSON, uint(mysql.BinaryFlag), -1, -1, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			j, _ := types.ParseBinaryJSONFromString([]string{"1", "1.0", "\"a\"", "[1,2]", "{\"a\":1}", "null"}[rng.IntN(6)])
			return []any{types.KindMysqlJSON, j.TypeCode, hx(j.Value)}
		}},
		tcCol{tcFt(mysql.TypeTiDBVectorFloat32, 0, -1, -1, "binary", "binary", nil), func(rng *rand.Rand, _ []string) []any {
			return []any{types.KindVectorFloat32, []string{"[]", "[1]", "[1.5,-2]"}[rng.IntN(3)]}
		}})
	return cols
}

func tcLitStrs(lits []datumLit, fn string) []string {
	var out []string
	for _, l := range lits {
		if l.str != nil && (fn == "" || l.fn == fn) && len(*l.str) < 200 {
			out = append(out, *l.str)
		}
	}
	return out
}

func tcLitInts(lits []datumLit, fn string) []int64 {
	var out []int64
	for _, l := range lits {
		if l.num != nil && l.fn == fn {
			out = append(out, *l.num)
		}
	}
	return out
}

func tcMutate(rng *rand.Rand, b []byte) []byte {
	c := append([]byte(nil), b...)
	switch rng.IntN(5) {
	case 0:
		if len(c) > 0 {
			c = c[:rng.IntN(len(c))]
		}
	case 1:
		if len(c) > 0 {
			c[rng.IntN(len(c))] ^= byte(1 + rng.IntN(255))
		}
	case 2:
		c = append(c, byte(rng.IntN(256)))
	case 3:
		if len(c) > 0 {
			c[rng.IntN(len(c))] = []byte{0, 0x7f, 0x80, 0xff, 1, 3, 125, 126, 127, 128}[rng.IntN(10)]
		}
	default:
		if len(c) > 2 {
			i := rng.IntN(len(c) - 1)
			c[i], c[i+1] = c[i+1], c[i]
		}
	}
	return c
}

func tablecodecGenerate(g *vec.Gen, rng *rand.Rand) {
	lits := parseDatumLiterals(tablecodecLiteralsTSV)
	allLits := tcLitStrs(lits, "")
	cols := tcCols()

	// ---- keys
	tids := []int64{0, 1, -1, 100, 1 << 32, math.MaxInt64, math.MinInt64, 4343}
	handles := func(rng *rand.Rand) handleSpec {
		switch rng.IntN(4) {
		case 0, 1:
			return handleSpec{T: "int", V: []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42}[rng.IntN(6)]}
		case 2:
			enc, _ := codec.EncodeKey(gotime.UTC, nil, types.NewIntDatum(rng.Int64N(1000)), types.NewStringDatum("abc"))
			return specOf(mustCommon(enc))
		default:
			return handleSpec{T: "part", Pid: rng.Int64N(1000), H: &handleSpec{T: "int", V: rng.Int64N(100)}}
		}
	}
	for i := range 700 {
		tid := tids[i%len(tids)]
		if i%3 == 0 {
			tid = rng.Int64()
		}
		var hp any
		if i%5 != 0 {
			hp = handles(rng)
		}
		enc := make([]byte, rng.IntN(20))
		for j := range enc {
			enc[j] = byte(rng.IntN(256))
		}
		g.Add("rand/keys", "keys", tid, rng.Int64N(100)-5, hp, hx(enc))
	}
	for _, fn := range []string{"TestTableCodec", "TestRecordKey", "TestIndexKey", "TestPrefix", "TestRange", "TestCutPrefix", "TestV2TableCodec"} {
		for _, n := range tcLitInts(lits, fn) {
			g.Add(fn, "keys", n, n, handleSpec{T: "int", V: n}, hx([]byte("abc")))
		}
		g.Add(fn, "keys", int64(1), int64(1), nil, "")
	}

	// ---- key_decode: valid keys of every shape, then mutations
	var keyCorpus [][]byte
	addKey := func(src string, k []byte) {
		keyCorpus = append(keyCorpus, k)
		g.Add(src, "key_decode", hx(k))
	}
	for i := range 400 {
		tid := tids[i%len(tids)]
		h := handleOfSpec(handles(rng))
		addKey("sys/key", tablecodec.EncodeRowKeyWithHandle(tid, h))
		addKey("sys/key", tablecodec.EncodeRecordKey(tablecodec.GenTableRecordPrefix(tid), h))
		var ds []types.Datum
		for j := 0; j < 1+rng.IntN(3); j++ {
			c := cols[rng.IntN(len(cols))]
			ds = append(ds, d2DatumIn(rawsOf(c.gen(rng, allLits))))
		}
		vals, err := codec.EncodeKey(gotime.UTC, nil, ds...)
		if err != nil {
			continue
		}
		idxKey := tablecodec.EncodeIndexSeekKey(tid, rng.Int64N(10), vals)
		switch rng.IntN(4) {
		case 0:
			idxKey = append(idxKey, codec.IntHandleFlag)
			idxKey = codec.EncodeInt(idxKey, rng.Int64())
		case 1:
			idxKey = append(idxKey, tablecodec.PartitionIDFlag)
			idxKey = codec.EncodeInt(idxKey, rng.Int64N(1000))
			idxKey = append(idxKey, codec.IntHandleFlag)
			idxKey = codec.EncodeInt(idxKey, rng.Int64())
		}
		addKey("sys/key", idxKey)
		if i%10 == 0 {
			t := append([]byte(nil), idxKey...)
			tablecodec.IndexKey2TempIndexKey(t)
			addKey("sys/key", t)
		}
		if i%8 == 0 {
			// meta keys (structure hash data), raw and API V2 keyspace prefixes
			mk := []byte("m")
			mk = codec.EncodeBytes(mk, []byte("DB:"+string(rune('a'+i%26))))
			mk = codec.EncodeUint(mk, uint64('h'))
			mk = codec.EncodeBytes(mk, []byte("Table:12"))
			addKey("sys/key", mk)
			addKey("sys/key", append([]byte{'x', 0, 0, 1}, tablecodec.EncodeRowKeyWithHandle(tid, h)...))
			addKey("sys/key", append([]byte{'r', 0, 0, 2}, tablecodec.GenTablePrefix(tid)...))
		}
	}
	for _, k := range [][]byte{{}, {'t'}, []byte("t12345678"), []byte("t12345678_r"), []byte("m"), {'x'}, {'x', 1, 2, 3},
		tablecodec.GenTablePrefix(5), tablecodec.EncodeTablePrefix(5), tablecodec.GenTableIndexPrefix(5)} {
		addKey("sys/key", k)
	}
	for _, fn := range []string{"TestDecodeIndexKey", "TestCutKey", "TestCutKeyNew", "TestGetKeyKind", "TestDecodeAutoIDMeta",
		"TestTempIndexKey", "TestTableCodecInvalid"} {
		for _, s := range tcLitStrs(lits, fn) {
			addKey(fn, []byte(s))
			vals, _ := codec.EncodeKey(gotime.UTC, nil, types.NewStringDatum(s))
			addKey(fn, tablecodec.EncodeIndexSeekKey(4, 5, vals))
		}
		for _, n := range tcLitInts(lits, fn) {
			addKey(fn, tablecodec.EncodeRowKeyWithHandle(n, kv.IntHandle(n)))
		}
	}
	n := len(keyCorpus)
	for range 1500 {
		addKey("rand/key", tcMutate(rng, keyCorpus[rng.IntN(n)]))
	}

	// ---- encode_value / unflatten
	pool := codecPool(lits)
	locs := []any{nil, 0, 28800, -18000}
	for i := range 600 {
		d := pool[rng.IntN(len(pool))]
		pre := ""
		if i%7 == 0 {
			pre = "aabb"
		}
		g.Add("rand/encode_value", "encode_value", locs[i%len(locs)], pre, d)
	}
	for i := range 2000 {
		c := cols[rng.IntN(len(cols))]
		var d []any
		if i%9 == 0 {
			d = []any{types.KindNull}
		} else {
			d = c.gen(rng, allLits)
		}
		datum := d2DatumIn(rawsOf(d))
		// The flattened (stored) form, as Unflatten receives it.
		enc, err := tablecodec.EncodeValue(gotime.UTC, nil, datum)
		if err != nil {
			continue
		}
		_, flat, err := codec.DecodeOne(enc)
		if err != nil {
			continue
		}
		data := enc
		if i%5 == 0 {
			data = tcMutate(rng, enc)
		}
		ft := c.ft
		if i%11 == 0 {
			ft = cols[rng.IntN(len(cols))].ft
		}
		loc := locs[i%len(locs)]
		g.Add("rand/unflatten", "unflatten", datumAny(flat), ft, loc, hx(data))
	}
	for _, fn := range []string{"TestDecodeColumnValue", "TestUnflattenDatums", "TestTimeCodec", "TestDecodeBadDecical"} {
		for _, s := range tcLitStrs(lits, fn) {
			g.Add(fn, "unflatten", []any{types.KindString, vec.GoString(s), "utf8mb4_bin"},
				tcFt(mysql.TypeVarchar, 0, 64, -1, "utf8mb4", "utf8mb4_bin", nil), nil, hx([]byte(s)))
		}
		for _, n := range tcLitInts(lits, fn) {
			g.Add(fn, "unflatten", []any{types.KindUint64, uint64(n)},
				tcFt(mysql.TypeDatetime, 0, -1, 0, "binary", "binary", nil), 0, hx(codec.EncodeInt([]byte{3}, n)))
		}
	}

	// ---- rows
	tcGenerateRows(g, rng, cols, lits, allLits)

	// ---- checksum
	for i := range 350 {
		nc := 1 + rng.IntN(5)
		var cs, ds []any
		for j := range nc {
			c := cols[rng.IntN(len(cols))]
			cs = append(cs, []any{int64(j*3 + 1), c.ft})
			if rng.IntN(6) == 0 {
				ds = append(ds, []any{types.KindNull})
			} else if i%13 == 0 {
				ds = append(ds, cols[rng.IntN(len(cols))].gen(rng, allLits)) // type mismatch: recovered panic
			} else {
				ds = append(ds, c.gen(rng, allLits))
			}
		}
		g.Add("rand/checksum", "checksum", cs, ds, locs[i%len(locs)])
	}
	for _, n := range tcLitInts(lits, "TestRowChecksum") {
		g.Add("TestRowChecksum", "checksum", []any{[]any{int64(1), tcFt(mysql.TypeLonglong, 0, -1, 0, "binary", "binary", nil)}},
			[]any{[]any{types.KindInt64, n}}, nil)
	}

	// ---- index
	tcGenerateIndexes(g, rng, cols, lits, allLits)

	// ---- truncate
	for i := range 400 {
		strs := tcStrPool(allLits)
		s := strs[rng.IntN(len(strs))]
		var d []any
		switch i % 4 {
		case 0:
			d = []any{types.KindBytes, hx([]byte(s))}
		case 3:
			d = []any{types.KindInt64, int64(i)}
		default:
			d = []any{types.KindString, vec.GoString(s), "utf8mb4_bin"}
		}
		cs := []string{"utf8mb4", "binary", "ascii", "latin1", "gbk", "utf8"}[rng.IntN(6)]
		g.Add("rand/truncate", "truncate", d, []int{-1, 0, 1, 2, 3, 5, 10}[rng.IntN(7)], cs, cs+"_bin")
	}

	// ---- verify_ranges
	for i := range 150 {
		var parts [][][2]string
		for range 1 + rng.IntN(3) {
			var rs [][2]string
			tid := rng.Int64N(5) + 1
			if rng.IntN(8) == 0 {
				tid = -tid
			}
			for range rng.IntN(4) {
				t := tid
				if rng.IntN(6) == 0 {
					t = tid + 1
				}
				s := tablecodec.EncodeRowKeyWithHandle(t, kv.IntHandle(rng.Int64N(100)))
				if rng.IntN(10) == 0 {
					s = []byte("zzz")
				}
				rs = append(rs, [2]string{hx(s), hx(tablecodec.GenTablePrefix(t + 1))})
			}
			parts = append(parts, rs)
		}
		g.Add("rand/verify_ranges", "verify_ranges", parts, i%3 != 0)
	}
}

func mustCommon(enc []byte) *kv.CommonHandle {
	h, err := kv.NewCommonHandle(enc)
	if err != nil {
		panic(err)
	}
	return h
}

func rawsOf(d []any) []json.RawMessage {
	out := make([]json.RawMessage, len(d))
	for i, x := range d {
		out[i] = rawArg(x)
	}
	return out
}

// datumAny: a datum in the datum2 input form.
func datumAny(d types.Datum) []any {
	switch d.Kind() {
	case types.KindNull:
		return []any{types.KindNull}
	case types.KindInt64:
		return []any{types.KindInt64, d.GetInt64()}
	case types.KindUint64:
		return []any{types.KindUint64, d.GetUint64()}
	case types.KindFloat32:
		return []any{types.KindFloat32, fBits(float64(d.GetFloat32()))}
	case types.KindFloat64:
		return []any{types.KindFloat64, fBits(d.GetFloat64())}
	case types.KindString:
		return []any{types.KindString, vec.GoString(d.GetString()), d.Collation()}
	case types.KindBytes:
		return []any{types.KindBytes, hx(d.GetBytes())}
	case types.KindMysqlDecimal:
		return []any{types.KindMysqlDecimal, d.GetMysqlDecimal().String()}
	case types.KindMysqlJSON:
		j := d.GetMysqlJSON()
		return []any{types.KindMysqlJSON, j.TypeCode, hx(j.Value)}
	case types.KindVectorFloat32:
		return []any{types.KindVectorFloat32, d.GetVectorFloat32().String()}
	}
	panic("datumAny: kind " + string(rune('0'+d.Kind())))
}

func tcGenerateRows(g *vec.Gen, rng *rand.Rand, cols []tcCol, lits []datumLit, allLits []string) {
	locs := []any{nil, 0, 28800, -18000}
	type rowCase struct {
		ids []int64
		cs  []tcCol
		ds  []types.Datum
		raw [][]any
	}
	mkRow := func(nc int, large bool) rowCase {
		var rc rowCase
		used := map[int64]bool{}
		for range nc {
			id := 1 + rng.Int64N(30)
			if large {
				id = 1 + rng.Int64N(600)
			}
			for used[id] {
				id++
			}
			used[id] = true
			c := cols[rng.IntN(len(cols))]
			var d []any
			if rng.IntN(5) == 0 {
				d = []any{types.KindNull}
			} else {
				d = c.gen(rng, allLits)
			}
			rc.ids = append(rc.ids, id)
			rc.cs = append(rc.cs, c)
			rc.raw = append(rc.raw, d)
			rc.ds = append(rc.ds, d2DatumIn(rawsOf(d)))
		}
		return rc
	}
	var corpus [][]any
	emit := func(src string, rc rowCase, b []byte, loc any, h kv.Handle, hids []int64) {
		var colsMap, infos []any
		for i, id := range rc.ids {
			ft := rc.cs[i].ft
			if rng.IntN(15) == 0 {
				ft = cols[rng.IntN(len(cols))].ft
			}
			if rng.IntN(6) != 0 {
				colsMap = append(colsMap, []any{id, ft})
			}
			infos = append(infos, []any{id, rng.IntN(10) == 0, rng.IntN(12) == 0, ft})
		}
		// columns not in the row (default / handle / commit ts / checksum columns)
		extra := []int64{99, model.ExtraCommitTSID, model.ExtraRowChecksumID, model.ExtraHandleID}
		for _, hid := range hids {
			extra = append(extra, hid)
		}
		for _, id := range extra {
			if rng.IntN(3) == 0 {
				c := cols[rng.IntN(len(cols))]
				infos = append(infos, []any{id, id == model.ExtraHandleID, false, c.ft})
				colsMap = append(colsMap, []any{id, c.ft})
			}
		}
		var defs, defBytes any
		if rng.IntN(2) == 0 {
			var ds []any
			for range infos {
				ds = append(ds, nil)
			}
			defs = ds
		}
		if rng.IntN(2) == 0 {
			var bs []any
			for range infos {
				if rng.IntN(3) == 0 {
					bs = append(bs, hx([]byte{3, 1, 2}))
				} else {
					bs = append(bs, nil)
				}
			}
			defBytes = bs
		}
		var hp any
		if h != nil {
			hp = specOf(h)
		}
		cache := ""
		if rng.IntN(4) == 0 {
			cache = "ff"
		}
		if hids == nil {
			hids = []int64{}
		}
		args := []any{hx(b), colsMap, loc, hp, hids, infos, uint64(rng.IntN(3)) * 1000, defs, defBytes, cache}
		corpus = append(corpus, args)
		g.Add(src, "row", args...)
	}
	for i := range 1300 {
		large := i%10 == 0
		rc := mkRow(rng.IntN(8), large)
		loc := locs[i%len(locs)]
		var h kv.Handle
		var hids []int64
		switch rng.IntN(3) {
		case 0:
			h = kv.IntHandle(rng.Int64())
			hids = []int64{int64(1 + rng.IntN(40))}
		case 1:
			enc, _ := codec.EncodeKey(gotime.UTC, nil, types.NewIntDatum(rng.Int64N(100)), types.NewStringDatum("pk"))
			h = mustCommon(enc)
			hids = []int64{int64(1 + rng.IntN(40)), int64(1 + rng.IntN(40))}
		}
		lc := tcLocNil(rawsOf([]any{loc}), 0)
		if rng.IntN(4) == 0 {
			b, err := tablecodec.EncodeOldRow(lc, rc.ds, rc.ids, nil, nil)
			if err == nil {
				emit("rand/row_v1", rc, b, loc, h, hids)
			}
			continue
		}
		var cs rowcodec.Checksum
		if rng.IntN(3) == 0 {
			ch := h
			if ch == nil {
				ch = kv.IntHandle(7)
			}
			cs = rowcodec.RawChecksum{Handle: ch}
		}
		enc := rowcodec.Encoder{Enable: true}
		b, err := enc.Encode(lc, rc.ids, rc.ds, cs, nil)
		if err != nil {
			continue
		}
		if rng.IntN(8) == 0 && len(b) > 0 {
			// a checksum header of another version, with an extra checksum
			b = append([]byte(nil), b...)
			b[1] |= 2
			b = append(b, byte(rng.IntN(4))|8, 1, 2, 3, 4, 5, 6, 7, 8)
		}
		emit("rand/row_v2", rc, b, loc, h, hids)
	}
	// A row whose data exceeds 65535 bytes (large format by size).
	big := rowCase{ids: []int64{1, 2}, cs: []tcCol{cols[20], cols[20]}}
	for _, s := range []string{strings.Repeat("a", 40000), strings.Repeat("b", 40000)} {
		big.ds = append(big.ds, types.NewStringDatum(s))
	}
	enc := rowcodec.Encoder{Enable: true}
	if b, err := enc.Encode(gotime.UTC, big.ids, big.ds, nil, nil); err == nil {
		emit("sys/row_large", big, b, nil, nil, nil)
	}
	for _, fn := range []string{"TestRowCodec", "TestTypesNewRowCodec", "TestNilAndDefault", "TestOldRowCodec",
		"Test65535Bug", "TestColumnEncode", "TestCutRow", "TestDecodeRowWithHandle", "TestEncodeKindNullDatum",
		"TestDecodeDecimalFspNotMatch", "TestVarintCompatibility", "TestCodecUtil", "TestEncodeLargeSmallReuseBug",
		"TestEncodeDecodeRowWithChecksum", "TestDecodeWithCommitTS"} {
		strs := tcLitStrs(lits, fn)
		ints := tcLitInts(lits, fn)
		rc := rowCase{}
		for i, s := range strs {
			rc.ids = append(rc.ids, int64(i+1))
			rc.cs = append(rc.cs, cols[20])
			rc.ds = append(rc.ds, types.NewStringDatum(s))
		}
		for i, n := range ints {
			rc.ids = append(rc.ids, int64(len(strs)+i+1))
			rc.cs = append(rc.cs, cols[8])
			rc.ds = append(rc.ds, types.NewIntDatum(n))
		}
		if len(rc.ids) > 200 {
			rc.ids, rc.cs, rc.ds = rc.ids[:200], rc.cs[:200], rc.ds[:200]
		}
		enc := rowcodec.Encoder{Enable: true}
		if b, err := enc.Encode(gotime.UTC, rc.ids, rc.ds, nil, nil); err == nil {
			emit(fn, rc, b, nil, kv.IntHandle(1), []int64{1})
		}
		if b, err := tablecodec.EncodeOldRow(gotime.UTC, rc.ds, rc.ids, nil, nil); err == nil {
			emit(fn, rc, b, nil, nil, nil)
		}
	}
	// Mutated rows (corrupted counts, offsets, flags, truncation).
	nc := len(corpus)
	for range 900 {
		args := append([]any(nil), corpus[rng.IntN(nc)]...)
		b, _ := hex.DecodeString(args[0].(string))
		if len(b) > 6 && rng.IntN(3) == 0 {
			b = append([]byte(nil), b...)
			switch rng.IntN(4) {
			case 0:
				binary.LittleEndian.PutUint16(b[2:], uint16(rng.IntN(70000)))
			case 1:
				binary.LittleEndian.PutUint16(b[4:], uint16(rng.IntN(70000)))
			case 2:
				b[1] ^= byte(1 + rng.IntN(3))
			default:
				b[len(b)-1-rng.IntN(len(b)-6)] ^= 0xff
			}
		} else {
			b = tcMutate(rng, b)
		}
		args[0] = hx(b)
		g.Add("rand/row_mut", "row", args...)
	}
}

// tcTable builds a table with the given columns, a primary key and one secondary index.
type tcTableCase struct {
	tbl      *model.TableInfo
	colGen   []tcCol
	idxOff   int
	clusterd bool
}

func tcGenerateIndexes(g *vec.Gen, rng *rand.Rand, cols []tcCol, lits []datumLit, allLits []string) {
	locs := []any{nil, 0, 28800}
	var rawCorpus [][]any
	strCols := []int{}
	for i, c := range cols {
		tp := c.ft[0].(byte)
		if tp == mysql.TypeVarchar || tp == mysql.TypeString || tp == mysql.TypeBlob {
			strCols = append(strCols, i)
		}
	}
	indexable := []int{}
	for i, c := range cols {
		tp := c.ft[0].(byte)
		if tp != mysql.TypeJSON && tp != mysql.TypeTiDBVectorFloat32 {
			indexable = append(indexable, i)
		}
	}
	for i := range 1600 {
		// columns: pk columns first, then index columns
		kind := rng.IntN(6) // 0,1: int handle; 2,3: clustered v1; 4: clustered v0; 5: global
		ncols := 1 + rng.IntN(3)
		var tcs []tcCol
		var pick []int
		for range ncols {
			if rng.IntN(2) == 0 {
				pick = append(pick, strCols[rng.IntN(len(strCols))])
			} else {
				pick = append(pick, indexable[rng.IntN(len(indexable))])
			}
		}
		for _, p := range pick {
			tcs = append(tcs, cols[p])
		}
		var pkCols []int
		switch kind {
		case 0:
			// int pk handle
			tcs = append([]tcCol{cols[8]}, tcs...)
			pkCols = []int{0}
		case 2, 3, 4:
			tcs = append([]tcCol{cols[strCols[rng.IntN(len(strCols))]], cols[8]}, tcs...)
			pkCols = []int{0, 1}
			if rng.IntN(2) == 0 {
				pkCols = []int{0}
			}
		}
		tbl := &model.TableInfo{ID: 100 + int64(i), Name: pmodel.NewCIStr("t")}
		for j, c := range tcs {
			ft := d2FtIn(rawsOf(c.ft))
			for _, pc := range pkCols {
				if pc == j {
					ft.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
				}
			}
			tbl.Columns = append(tbl.Columns, &model.ColumnInfo{ID: int64(j + 1), Name: pmodel.NewCIStr("c" + string(rune('a'+j))),
				Offset: j, FieldType: *ft, State: model.StatePublic})
		}
		if kind == 0 {
			tbl.PKIsHandle = true
		}
		if len(pkCols) > 0 && kind != 0 {
			tbl.IsCommonHandle = true
			if kind != 4 {
				tbl.CommonHandleVersion = 1
			}
			pk := &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("primary"), Primary: true, Unique: true, State: model.StatePublic}
			for _, pc := range pkCols {
				l := types.UnspecifiedLength
				if rng.IntN(4) == 0 && tcs[pc].ft[0].(byte) != mysql.TypeLonglong {
					l = 1 + rng.IntN(3)
				}
				pk.Columns = append(pk.Columns, &model.IndexColumn{Name: tbl.Columns[pc].Name, Offset: pc, Length: l})
			}
			tbl.Indices = append(tbl.Indices, pk)
		}
		first := len(tcs) - ncols
		idx := &model.IndexInfo{ID: 2, Name: pmodel.NewCIStr("i"), Unique: rng.IntN(2) == 0, State: model.StatePublic}
		if kind == 5 {
			idx.Global = true
			idx.GlobalIndexVersion = uint8(rng.IntN(2))
		}
		for j := first; j < len(tcs); j++ {
			l := types.UnspecifiedLength
			tp := tcs[j].ft[0].(byte)
			if (tp == mysql.TypeVarchar || tp == mysql.TypeString || tp == mysql.TypeBlob) && rng.IntN(3) == 0 {
				l = 1 + rng.IntN(5)
			}
			if tp == mysql.TypeBlob && l == types.UnspecifiedLength {
				l = 10
			}
			idx.Columns = append(idx.Columns, &model.IndexColumn{Name: tbl.Columns[j].Name, Offset: j, Length: l})
		}
		tbl.Indices = append(tbl.Indices, idx)
		idxOff := len(tbl.Indices) - 1
		tblJSON, err := json.Marshal(tbl)
		if err != nil {
			panic(err)
		}
		// row values
		row := make([]types.Datum, len(tcs))
		rawRow := make([][]any, len(tcs))
		for j, c := range tcs {
			var d []any
			isPk := false
			for _, pc := range pkCols {
				isPk = isPk || pc == j
			}
			if !isPk && rng.IntN(6) == 0 {
				d = []any{types.KindNull}
			} else {
				d = c.gen(rng, allLits)
			}
			if c.ft[0].(byte) == mysql.TypeLonglong && isPk {
				d = []any{types.KindInt64, rng.Int64N(1 << 40)}
			}
			rawRow[j] = d
			row[j] = d2DatumIn(rawsOf(d))
		}
		var h kv.Handle
		var phy int64 = tbl.ID
		switch {
		case kind == 0:
			h = kv.IntHandle(row[0].GetInt64())
		case tbl.IsCommonHandle:
			pkIdx := tbl.Indices[0]
			var pkVals []types.Datum
			for _, ic := range pkIdx.Columns {
				pkVals = append(pkVals, row[ic.Offset])
			}
			tablecodec.TruncateIndexValues(tbl, pkIdx, pkVals)
			enc, err := codec.EncodeKey(gotime.UTC, nil, pkVals...)
			if err != nil {
				continue
			}
			ch, err := kv.NewCommonHandle(enc)
			if err != nil {
				continue
			}
			h = ch
		case kind == 5:
			phy = 1000 + int64(i)
			h = kv.NewPartitionHandle(phy, kv.IntHandle(rng.Int64N(1<<30)))
		default:
			h = kv.IntHandle(rng.Int64N(1 << 30))
		}
		var idxVals []types.Datum
		var rawIdx []any
		for _, ic := range idx.Columns {
			idxVals = append(idxVals, row[ic.Offset])
			rawIdx = append(rawIdx, rawRow[ic.Offset])
		}
		loc := locs[i%len(locs)]
		lc := tcLoc(rawsOf([]any{loc}), 0)
		key, distinct, err := tablecodec.GenIndexKey(lc, tbl, idx, phy, append([]types.Datum(nil), idxVals...), h, nil)
		if err != nil {
			g.Add("rand/index", "index", string(tblJSON), idxOff, phy, rawIdx, specOrNil(h), "", len(idx.Columns), []any{}, loc)
			continue
		}
		trunc := append([]types.Datum(nil), idxVals...)
		tablecodec.TruncateIndexValues(tbl, idx, trunc)
		needRestored := tables.NeedRestoredData(idx.Columns, tbl.Columns)
		var rsData []types.Datum
		if tbl.IsCommonHandle && tbl.CommonHandleVersion == 1 {
			for _, id := range tablecodec.TryGetCommonPkColumnRestoredIds(tbl) {
				d := row[id-1]
				pkIdx := tbl.Indices[0]
				for _, ic := range pkIdx.Columns {
					if ic.Offset == int(id-1) {
						tablecodec.TruncateIndexValue(&d, ic, tbl.Columns[ic.Offset])
					}
				}
				rsData = append(rsData, d)
			}
		}
		untouched := rng.IntN(6) == 0
		pid := int64(0)
		if idx.Global {
			pid = phy
		}
		value, err := tablecodec.GenIndexValuePortal(lc, tbl, idx, needRestored, distinct, untouched, trunc, h, pid, rsData, nil)
		if err != nil {
			continue
		}
		colInfos := tables.BuildRowcodecColInfoForIndexColumns(idx, tbl)
		colInfos = tables.TryAppendCommonHandleRowcodecColInfos(colInfos, tbl)
		if idx.Global {
			colInfos = append(colInfos, rowcodec.ColInfo{ID: model.ExtraPhysTblID, Ft: types.NewFieldType(mysql.TypeLonglong)})
		}
		var cis []any
		for _, ci := range colInfos {
			cis = append(cis, []any{ci.ID, ci.IsPKHandle, ci.VirtualGenCol, d2FtOutAny(ci.Ft)})
		}
		_ = key
		args := []any{string(tblJSON), idxOff, phy, rawIdx, specOrNil(h), hx(value), len(idx.Columns), cis, loc}
		g.Add("rand/index", "index", args...)
		rawCorpus = append(rawCorpus, []any{hx(key), hx(value), len(idx.Columns), cis, idx.Unique})
	}
	for _, fn := range []string{"TestDecodeIndexHandleWithPartitionIDInKeyAndValue", "TestUniqueGlobalIndexKeyWithNullValues",
		"TestUntouchedIndexKValue", "TestTempIndexValueCodec"} {
		for _, n := range tcLitInts(lits, fn) {
			k := tablecodec.EncodeIndexSeekKey(n, 1, codec.EncodeInt([]byte{3}, n))
			k = append(k, tablecodec.PartitionIDFlag)
			k = codec.EncodeInt(k, n)
			k = append(k, codec.IntHandleFlag)
			k = codec.EncodeInt(k, n)
			v := append([]byte{0, tablecodec.PartitionIDFlag}, codec.EncodeInt(nil, n+1)...)
			g.Add(fn, "index_raw", hx(k), hx(v), 1, []any{[]any{int64(1), false, false, tcFt(mysql.TypeLonglong, 0, -1, 0, "binary", "binary", nil)}}, false)
		}
	}
	nr := len(rawCorpus)
	for range 1800 {
		args := append([]any(nil), rawCorpus[rng.IntN(nr)]...)
		k, _ := hex.DecodeString(args[0].(string))
		v, _ := hex.DecodeString(args[1].(string))
		if rng.IntN(2) == 0 {
			v = tcMutate(rng, v)
		} else {
			k = tcMutate(rng, k)
		}
		args[0], args[1] = hx(k), hx(v)
		g.Add("rand/index_raw", "index_raw", args...)
	}
}

func specOrNil(h kv.Handle) any {
	if h == nil {
		return nil
	}
	return specOf(h)
}

// d2FtOutAny: a field type in the d2FtIn input form.
func d2FtOutAny(ft *types.FieldType) []any {
	var e any
	if el := ft.GetElems(); el != nil {
		gs := make([]vec.GoString, len(el))
		for i, s := range el {
			gs[i] = vec.GoString(s)
		}
		e = gs
	}
	return []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), ft.GetDecimal(), ft.GetCharset(), ft.GetCollate(), e}
}
