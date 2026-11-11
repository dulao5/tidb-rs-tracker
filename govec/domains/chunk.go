package domains

// Domain "chunk": block D01.7 (pkg/util/chunk: Chunk, Column, Row, Codec,
// Decoder, List, the iterators, the comparators, the row copy helpers).
//
// One op, "script": args[0] is a list of steps run in order on a state of
// chunks, lists, decoders and swap helpers identified by small integers. out
// is one entry per step (null for a mutation without result, a value or a
// dump otherwise; an error is {"err": msg}; a Go panic is {"panic": msg} and
// ends the script). The steps are listed in chunkStep.
//
// Values are typed by the column's field type (a step "new" lists them as
// [tp, flag, collate, flen]): integers as decimal strings, floats as the
// decimal string of their bits, decimals as strings (FromString), times as
// [core, tp, fsp] (NewTime), durations as [nanos, fsp], byte strings in hex,
// JSON as text (ParseBinaryJSONFromString), vectors as a list of float32 bit
// patterns, enums and sets as [name hex, value]; null is null.
//
// A dump of a chunk is {n, nc, v, cap, req, full, inc, sel, mem, enc, alias,
// cols}: NumRows, NumCols, GetNumVirtualRows, Capacity, RequiredRows, IsFull,
// IsInCompleteChunk, Sel, MemoryUsage, Codec.Encode (hex), for each slot the
// first slot holding the same *Column, and for each column its length and its
// four slices with their capacities (nullBitmap, offsets as little-endian
// bytes, data, elemBuf; read with reflect). The capacities and the bytes past
// the NULL cells are Go's and the Rust port reproduces them (see the D01.7
// spec).
//
// The generator keeps a script's state while it builds it, so every index is
// valid, and it never writes to a column shared by two chunks or, except for
// Reset and the swap steps, by two slots of one chunk (Go writes through the
// alias; the Rust port panics in debug builds, N5a 3.2).

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/memory"
)

const chunkSeed = 20261201

func init() {
	vec.Register(&vec.Domain{
		Name:     "chunk",
		Seed:     chunkSeed,
		Coverage: chunkCoverage,
		Blocks:   []string{"D01.7"},
		Exempt:   chunkExempt,
		Gzip:     true,
		Generate: chunkGenerate,
		Exec:     chunkExecOp,
	})
}

var chunkCoverage = map[string]vec.Coverage{
	"script": {Min: 1000, GoFuncs: []string{
		// types.Datum / Enum / Set users (follow-up after block D01.1)
		"Column.AppendSet", "Column.AppendEnum", "Column.GetEnum", "Column.GetSet", "Row.GetEnum", "Row.GetSet",
		"Chunk.AppendEnum", "Chunk.AppendSet", "Chunk.AppendDatum", "Chunk.ToString", "Row.ToString",
		"Row.GetDatumRow", "Row.GetDatumRowWithBuffer", "Row.GetDatum", "Row.DatumWithBuffer", "Compare",
		"Chunk.LowerBound", "Chunk.UpperBound",
		// mutrow.go
		"MutRow.ToRow", "MutRow.Len", "MutRow.Clone", "MutRowFromValues", "MutRowFromDatums", "MutRowFromTypes",
		"zeroValForType", "makeMutRowColumn", "newMutRowFixedLenColumn", "newMutRowVarLenColumn",
		"makeMutRowUint64Column", "makeMutRowBytesColumn", "cleanColOfMutRow", "MutRow.SetRow", "MutRow.SetValues",
		"MutRow.SetValue", "MutRow.SetDatums", "MutRow.SetDatum", "setMutRowBytes", "setMutRowNameValue",
		"setMutRowJSON", "MutRow.ShallowCopyPartialRow",
		// column.go
		"Column.AppendDuration", "Column.AppendMyDecimal", "Column.appendNameValue", "Column.AppendJSON",
		"Column.AppendVectorFloat32", "DefaultColumnAllocator.NewColumn", "NewEmptyColumn", "NewColumn",
		"newColumn", "newFixedLenColumn", "newVarLenColumn", "getDataMemCap", "getNullBitmapCap", "getOffsetsCap",
		"Column.typeSize", "Column.isFixed", "Column.Reset", "Column.Rows", "Column.reset", "Column.IsNull",
		"Column.CopyConstruct", "Column.AppendNullBitmap", "Column.appendNullBitmap", "Column.AppendCellNTimes",
		"Column.appendMultiSameNullBitmap", "Column.AppendNNulls", "Column.AppendNull", "Column.finishAppendFixed",
		"Column.AppendInt64", "Column.AppendUint64", "Column.AppendFloat32", "Column.AppendFloat64",
		"Column.finishAppendVar", "Column.AppendString", "Column.AppendBytes", "Column.AppendTime", "Column.resize",
		"Column.reserve", "Column.SetNull", "Column.SetNulls", "Column.nullCount", "Column.ResizeInt64",
		"Column.ResizeUint64", "Column.ResizeFloat32", "Column.ResizeFloat64", "Column.ResizeDecimal",
		"Column.ResizeGoDuration", "Column.ResizeTime", "Column.ReserveString", "Column.ReserveBytes",
		"Column.ReserveJSON", "Column.ReserveVectorFloat32", "Column.ReserveSet", "Column.ReserveEnum",
		"Column.castSliceHeader", "Column.Int64s", "Column.Uint64s", "Column.Float32s", "Column.Float64s",
		"Column.GoDurations", "Column.Decimals", "Column.Times", "Column.GetInt64", "Column.GetUint64",
		"Column.GetFloat32", "Column.GetFloat64", "Column.GetDecimal", "Column.GetString", "Column.GetJSON",
		"Column.GetVectorFloat32", "Column.GetBytes", "Column.GetTime", "Column.GetDuration", "Column.getNameValue",
		"Column.GetRaw", "Column.SetRaw", "Column.reconstruct", "Column.CopyReconstruct", "Column.MergeNulls",
		// chunk.go
		"NewEmptyChunk", "NewChunkWithCapacity", "NewChunkFromPoolWithCapacity", "New", "renewWithCapacity",
		"Renew", "renewColumns", "renewEmpty", "Chunk.SetInCompleteChunk", "Chunk.IsInCompleteChunk",
		"Chunk.GetNumVirtualRows", "Chunk.MemoryUsage", "Chunk.RequiredRows", "Chunk.SetRequiredRows",
		"Chunk.IsFull", "Chunk.Prune", "Chunk.MakeRef", "Chunk.MakeRefTo", "Chunk.swapColumn", "Chunk.SwapColumns",
		"Chunk.SetNumVirtualRows", "Chunk.Reset", "Chunk.CopyConstruct", "Chunk.CopyConstructSel",
		"Chunk.GrowAndReset", "reCalcCapacity", "Chunk.Capacity", "Chunk.NumCols", "Chunk.NumRows", "Chunk.GetRow",
		"Chunk.AppendRow", "Chunk.AppendPartialRow", "Chunk.AppendRowByColIdxs", "Chunk.AppendPartialRowByColIdxs",
		"appendCellByCell", "AppendCellFromRawData", "Chunk.Append", "Chunk.TruncateTo", "Chunk.AppendNull",
		"Chunk.AppendInt64", "Chunk.AppendUint64", "Chunk.AppendFloat32", "Chunk.AppendFloat64",
		"Chunk.AppendString", "Chunk.AppendBytes", "Chunk.AppendTime", "Chunk.AppendDuration",
		"Chunk.AppendMyDecimal", "Chunk.AppendJSON", "Chunk.AppendVectorFloat32", "Chunk.appendSel",
		"Chunk.Column", "Chunk.SetCol", "Chunk.Sel", "Chunk.SetSel", "Chunk.CloneEmpty", "Chunk.Reconstruct",
		"Chunk.AppendRows", "Chunk.AppendPartialRows",
		// row.go
		"Row.Chunk", "Row.IsEmpty", "Row.Idx", "Row.Len", "Row.GetInt64", "Row.GetUint64", "Row.GetFloat32",
		"Row.GetFloat64", "Row.GetString", "Row.GetBytes", "Row.GetTime", "Row.GetDuration", "Row.getNameValue",
		"Row.GetMyDecimal", "Row.GetJSON", "Row.GetVectorFloat32", "Row.GetRaw", "Row.IsNull", "Row.CopyConstruct",
		// codec.go
		"NewCodec", "Codec.Encode", "Codec.encodeColumn", "i64SliceToBytes", "Codec.Decode", "Codec.DecodeToChunk",
		"Codec.decodeColumn", "Codec.setAllNotNull", "bytesToI64Slice", "getFixedLen", "NewDecoder",
		"Decoder.Decode", "Decoder.Reset", "Decoder.IsFinished", "Decoder.RemainedRows", "Decoder.ReuseIntermChk",
		"Decoder.decodeColumn",
		// chunk_util.go
		"CopySelectedJoinRowsDirect", "CopySelectedJoinRowsWithSameOuterRows", "CopySelectedRows",
		"CopySelectedRowsWithRowIDFunc", "copySelectedInnerRows", "copySameOuterRows",
		"ColumnSwapHelper.SwapColumns", "ColumnSwapHelper.mergeInputIdxToOutputIdxes", "NewColumnSwapHelper",
		// iterator.go
		"NewIterator4Slice", "Iterator4Slice.Begin", "Iterator4Slice.Next", "Iterator4Slice.Current",
		"Iterator4Slice.End", "Iterator4Slice.ReachEnd", "Iterator4Slice.Len", "Iterator4Slice.Reset",
		"Iterator4Slice.Error", "NewIterator4Chunk", "Iterator4Chunk.Begin", "Iterator4Chunk.Next",
		"Iterator4Chunk.Current", "Iterator4Chunk.End", "Iterator4Chunk.ReachEnd", "Iterator4Chunk.Len",
		"Iterator4Chunk.GetChunk", "Iterator4Chunk.ResetChunk", "Iterator4Chunk.Error", "NewIterator4List",
		"iterator4List.Begin", "iterator4List.Next", "iterator4List.Current", "iterator4List.End",
		"iterator4List.ReachEnd", "iterator4List.Len", "iterator4List.Error", "NewIterator4RowPtr",
		"iterator4RowPtr.Begin", "iterator4RowPtr.Next", "iterator4RowPtr.Current", "iterator4RowPtr.End",
		"iterator4RowPtr.ReachEnd", "iterator4RowPtr.Len", "iterator4RowPtr.Error", "NewMultiIterator",
		"multiIterator.Len", "multiIterator.Begin", "multiIterator.Next", "multiIterator.Current",
		"multiIterator.End", "multiIterator.ReachEnd", "multiIterator.Error",
		// list.go
		"NewListWithMemTracker", "NewList", "List.GetMemTracker", "List.Len", "List.NumChunks", "List.FieldTypes",
		"List.NumRowsOfChunk", "List.GetChunk", "List.AppendRow", "List.Add", "List.AllocChunk", "List.GetRow",
		"List.Reset", "List.Clear", "List.Walk",
		// compare.go
		"GetCompareFunc", "cmpNull", "cmpInt64", "cmpUint64", "genCmpStringFunc", "cmpStringWithCollationInfo",
		"cmpFloat32", "cmpFloat64", "cmpMyDecimal", "cmpTime", "cmpDuration", "cmpNameValue", "cmpBit", "cmpJSON",
		"cmpNullConst", "cmpVectorFloat32",
	}},
	"type_width": {Min: 30, GoFuncs: []string{"GetFixedLen", "EstimateTypeWidth"}},
}

var chunkExempt = map[string]string{
	// Covered through SessionVars.SetAlloc's reuse hook and @@last_sql_use_alloc (domain
	// sessionvars, op stmtvars, D05.2 PR E): the answer only depends on the configured pool sizes.
	"allocator.CheckReuseAllocSize": "covered by sessionvars op stmtvars (reuse hook, @@last_sql_use_alloc)",
	"Column.DestroyDataForTest":  "test helper writing math/rand bytes; Rust unit test",
	"Chunk.Destroy":              "M-POOL: puts the chunk into a sync.Pool (nondeterministic reuse); a no-op in Rust",
	"init":                       "package init: fills allNotNullBitmap (a constant table in Rust)",
	"diskFileReaderWriter.initWithFileName": "spill file (block D01.10: temp files, checksum and encryption layers)",
	"diskFileReaderWriter.getReader":        "spill file (block D01.10)",
	"diskFileReaderWriter.getSectionReader": "spill file (block D01.10)",
	"diskFileReaderWriter.getWriter":        "spill file (block D01.10)",
	"diskFileReaderWriter.write":            "spill file (block D01.10)",
	"NewIterator4RowContainer":              "iterates a RowContainer (block D01.10)",
	"iterator4RowContainer.Len":             "RowContainer (block D01.10)",
	"iterator4RowContainer.setNextPtr":      "RowContainer (block D01.10)",
	"iterator4RowContainer.Begin":           "RowContainer (block D01.10)",
	"iterator4RowContainer.Next":            "RowContainer (block D01.10)",
	"iterator4RowContainer.Current":         "RowContainer (block D01.10)",
	"iterator4RowContainer.End":             "RowContainer (block D01.10)",
	"iterator4RowContainer.ReachEnd":        "RowContainer (block D01.10)",
	"iterator4RowContainer.Error":           "RowContainer (block D01.10)",
	"InitChunkAllocSize":                    "M-POOL: pool sizes; ignored in Rust (Rust unit test)",
	"NewAllocator":                          "M-POOL: Go's pooling allocator; the Rust allocators never reuse (Rust unit test)",
	"NewSyncAllocator":                      "M-POOL (Rust unit test)",
	"syncAllocator.Alloc":                   "M-POOL (Rust unit test)",
	"syncAllocator.CheckReuseAllocSize":     "M-POOL (Rust unit test)",
	"syncAllocator.Reset":                   "M-POOL (Rust unit test)",
	"NewReuseHookAllocator":                 "M-POOL (Rust unit test)",
	"reuseHookAllocator.Alloc":              "M-POOL (Rust unit test)",
	"reuseHookAllocator.CheckReuseAllocSize": "M-POOL (Rust unit test)",
	"reuseHookAllocator.Reset":              "M-POOL (Rust unit test)",
	"NewEmptyAllocator":                     "M-POOL (Rust unit test)",
	"emptyAllocator.Alloc":                  "M-POOL (Rust unit test)",
	"emptyAllocator.CheckReuseAllocSize":    "M-POOL (Rust unit test)",
	"emptyAllocator.Reset":                  "M-POOL (Rust unit test)",
}

// ---- state ----------------------------------------------------------------

type chunkState struct {
	chks    map[int]*chunk.Chunk
	fts     map[int][]*types.FieldType
	lists   map[int]*chunk.List
	ltracks map[int]*memory.Tracker
	decs    map[int]*chunk.Decoder
	dfts    map[int][]*types.FieldType
	mrs     map[int]chunk.MutRow
}

func newChunkState() *chunkState {
	return &chunkState{
		chks: map[int]*chunk.Chunk{}, fts: map[int][]*types.FieldType{},
		lists: map[int]*chunk.List{}, ltracks: map[int]*memory.Tracker{},
		decs: map[int]*chunk.Decoder{}, dfts: map[int][]*types.FieldType{},
		mrs: map[int]chunk.MutRow{},
	}
}

// chunkTypeSpec is [tp, flag, collate, flen].
type chunkTypeSpec struct {
	Tp      byte
	Flag    uint
	Collate string
	Flen    int
}

func (s chunkTypeSpec) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{s.Tp, s.Flag, s.Collate, s.Flen})
}

func (s *chunkTypeSpec) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	if err := json.Unmarshal(a[0], &s.Tp); err != nil {
		return err
	}
	if err := json.Unmarshal(a[1], &s.Flag); err != nil {
		return err
	}
	if err := json.Unmarshal(a[2], &s.Collate); err != nil {
		return err
	}
	return json.Unmarshal(a[3], &s.Flen)
}

func (s chunkTypeSpec) ft() *types.FieldType {
	ft := types.NewFieldType(s.Tp)
	ft.SetFlag(s.Flag)
	ft.SetCollate(s.Collate)
	ft.SetFlen(s.Flen)
	if s.Tp == mysql.TypeDuration {
		ft.SetDecimal(s.Flen % 7)
	}
	return ft
}

func chunkFts(specs []chunkTypeSpec) []*types.FieldType {
	r := make([]*types.FieldType, len(specs))
	for i, s := range specs {
		r[i] = s.ft()
	}
	return r
}

// ---- values ----------------------------------------------------------------

// chunkKind classifies a field type for values.
func chunkKind(ft *types.FieldType) string {
	switch ft.GetType() {
	case mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeYear:
		if mysql.HasUnsignedFlag(ft.GetFlag()) {
			return "u64"
		}
		return "i64"
	case mysql.TypeFloat:
		return "f32"
	case mysql.TypeDouble:
		return "f64"
	case mysql.TypeNewDecimal:
		return "dec"
	case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
		return "time"
	case mysql.TypeDuration:
		return "dur"
	case mysql.TypeJSON:
		return "json"
	case mysql.TypeTiDBVectorFloat32:
		return "vec"
	case mysql.TypeEnum, mysql.TypeSet:
		return "nv"
	case mysql.TypeNull:
		return "null"
	}
	return "bytes"
}

func chunkHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func chunkVecBytes(bits []uint32) []byte {
	b := make([]byte, 4+4*len(bits))
	binary.LittleEndian.PutUint32(b, uint32(len(bits)))
	for i, x := range bits {
		binary.LittleEndian.PutUint32(b[4+4*i:], x)
	}
	return b
}

func chunkU64(raw json.RawMessage) uint64 {
	var s string
	must(json.Unmarshal(raw, &s))
	u, err := strconv.ParseUint(s, 10, 64)
	must(err)
	return u
}

func chunkI64(raw json.RawMessage) int64 {
	var s string
	must(json.Unmarshal(raw, &s))
	i, err := strconv.ParseInt(s, 10, 64)
	must(err)
	return i
}

func chunkMust(err error) {
	if err != nil {
		panic(err)
	}
}

// chunkAppend appends v to column col of c, through the Chunk API (colLevel
// false) or the Column API.
func chunkAppend(c *chunk.Chunk, ft *types.FieldType, col int, v json.RawMessage, colLevel bool) {
	if string(v) == "null" {
		if colLevel {
			c.Column(col).AppendNull()
		} else {
			c.AppendNull(col)
		}
		return
	}
	cc := c.Column(col)
	switch chunkKind(ft) {
	case "i64":
		if colLevel {
			cc.AppendInt64(chunkI64(v))
		} else {
			c.AppendInt64(col, chunkI64(v))
		}
	case "u64":
		if colLevel {
			cc.AppendUint64(chunkU64(v))
		} else {
			c.AppendUint64(col, chunkU64(v))
		}
	case "f32":
		f := math.Float32frombits(uint32(chunkU64(v)))
		if colLevel {
			cc.AppendFloat32(f)
		} else {
			c.AppendFloat32(col, f)
		}
	case "f64":
		f := math.Float64frombits(chunkU64(v))
		if colLevel {
			cc.AppendFloat64(f)
		} else {
			c.AppendFloat64(col, f)
		}
	case "dec":
		var s string
		must(json.Unmarshal(v, &s))
		d := new(types.MyDecimal)
		_ = d.FromString([]byte(s))
		if colLevel {
			cc.AppendMyDecimal(d)
		} else {
			c.AppendMyDecimal(col, d)
		}
	case "time":
		var a []json.RawMessage
		must(json.Unmarshal(v, &a))
		var tp uint8
		var fsp int
		must(json.Unmarshal(a[1], &tp))
		must(json.Unmarshal(a[2], &fsp))
		t := types.NewTime(types.CoreTime(chunkU64(a[0])), tp, fsp)
		if colLevel {
			cc.AppendTime(t)
		} else {
			c.AppendTime(col, t)
		}
	case "dur":
		var a []json.RawMessage
		must(json.Unmarshal(v, &a))
		var fsp int
		must(json.Unmarshal(a[1], &fsp))
		d := types.Duration{Duration: time_Duration(chunkI64(a[0])), Fsp: fsp}
		if colLevel {
			cc.AppendDuration(d)
		} else {
			c.AppendDuration(col, d)
		}
	case "json":
		var s string
		must(json.Unmarshal(v, &s))
		j, err := types.ParseBinaryJSONFromString(s)
		must(err)
		if colLevel {
			cc.AppendJSON(j)
		} else {
			c.AppendJSON(col, j)
		}
	case "vec":
		var bits []uint32
		must(json.Unmarshal(v, &bits))
		vv, _, err := types.ZeroCopyDeserializeVectorFloat32(chunkVecBytes(bits))
		must(err)
		if colLevel {
			cc.AppendVectorFloat32(vv)
		} else {
			c.AppendVectorFloat32(col, vv)
		}
	case "nv":
		var a []json.RawMessage
		must(json.Unmarshal(v, &a))
		var name string
		var val uint64
		must(json.Unmarshal(a[0], &name))
		must(json.Unmarshal(a[1], &val))
		if ft.GetType() == mysql.TypeEnum {
			e := types.Enum{Name: string(chunkHex(name)), Value: val}
			if colLevel {
				cc.AppendEnum(e)
			} else {
				c.AppendEnum(col, e)
			}
		} else {
			s := types.Set{Name: string(chunkHex(name)), Value: val}
			if colLevel {
				cc.AppendSet(s)
			} else {
				c.AppendSet(col, s)
			}
		}
	default:
		var s string
		must(json.Unmarshal(v, &s))
		b := chunkHex(s)
		if colLevel {
			if ft.GetType() == mysql.TypeVarchar {
				cc.AppendString(string(b))
			} else {
				cc.AppendBytes(b)
			}
		} else {
			if ft.GetType() == mysql.TypeVarchar {
				c.AppendString(col, string(b))
			} else {
				c.AppendBytes(col, b)
			}
		}
	}
}

// chunkGet reads a cell through Row.
func chunkGet(r chunk.Row, ft *types.FieldType, col int) any {
	out := map[string]any{"null": r.IsNull(col), "raw": hex.EncodeToString(r.GetRaw(col))}
	if r.IsNull(col) {
		return out
	}
	switch chunkKind(ft) {
	case "i64":
		out["v"] = strconv.FormatInt(r.GetInt64(col), 10)
	case "u64":
		out["v"] = strconv.FormatUint(r.GetUint64(col), 10)
	case "f32":
		out["v"] = strconv.FormatUint(uint64(math.Float32bits(r.GetFloat32(col))), 10)
	case "f64":
		out["v"] = strconv.FormatUint(math.Float64bits(r.GetFloat64(col)), 10)
	case "dec":
		out["v"] = r.GetMyDecimal(col).String()
	case "time":
		out["v"] = r.GetTime(col).String()
	case "dur":
		d := r.GetDuration(col, ft.GetDecimal())
		out["v"] = []any{strconv.FormatInt(int64(d.Duration), 10), d.Fsp, d.String()}
	case "json":
		j := r.GetJSON(col)
		out["v"] = []any{j.TypeCode, hex.EncodeToString(j.Value), vec.GoString(j.String())}
	case "vec":
		out["v"] = r.GetVectorFloat32(col).String()
	case "nv":
		if ft.GetType() == mysql.TypeEnum {
			e := r.GetEnum(col)
			out["v"] = []any{hex.EncodeToString([]byte(e.Name)), strconv.FormatUint(e.Value, 10)}
		} else {
			s := r.GetSet(col)
			out["v"] = []any{hex.EncodeToString([]byte(s.Name)), strconv.FormatUint(s.Value, 10)}
		}
	case "bytes":
		if ft.GetType() == mysql.TypeVarchar {
			out["v"] = hex.EncodeToString([]byte(r.GetString(col)))
		} else {
			out["v"] = hex.EncodeToString(r.GetBytes(col))
		}
	}
	return out
}

// ---- dumps -----------------------------------------------------------------

func chunkSliceDump(v reflect.Value, elem int) (string, int) {
	if v.IsNil() {
		return "", 0
	}
	n := v.Len() * elem
	var b []byte
	if n > 0 {
		b = unsafe.Slice((*byte)(v.UnsafePointer()), n)
	}
	return hex.EncodeToString(b), v.Cap()
}

func chunkColDump(col *chunk.Column) any {
	rv := reflect.ValueOf(col).Elem()
	nb, nbc := chunkSliceDump(rv.FieldByName("nullBitmap"), 1)
	off, oc := chunkSliceDump(rv.FieldByName("offsets"), 8)
	d, dc := chunkSliceDump(rv.FieldByName("data"), 1)
	ebv := rv.FieldByName("elemBuf")
	var eb any
	ebc := 0
	if !ebv.IsNil() {
		s, c := chunkSliceDump(ebv, 1)
		eb, ebc = s, c
	}
	return map[string]any{
		"l": int(rv.FieldByName("length").Int()), "nb": nb, "nbc": nbc, "off": off, "oc": oc,
		"d": d, "dc": dc, "eb": eb, "ebc": ebc,
	}
}

func chunkDump(c *chunk.Chunk) any {
	n := c.NumCols()
	alias := make([]int, n)
	cols := make([]any, n)
	for i := range n {
		alias[i] = i
		for j := range i {
			if c.Column(j) == c.Column(i) {
				alias[i] = j
				break
			}
		}
		cols[i] = chunkColDump(c.Column(i))
	}
	var sel any
	if s := c.Sel(); s != nil {
		sel = s
	}
	return map[string]any{
		"n": c.NumRows(), "nc": n, "v": c.GetNumVirtualRows(), "cap": c.Capacity(), "req": c.RequiredRows(),
		"full": c.IsFull(), "inc": c.IsInCompleteChunk(), "sel": sel, "mem": c.MemoryUsage(),
		"enc": hex.EncodeToString(chunk.NewCodec(nil).Encode(c)), "alias": alias, "cols": cols,
	}
}

func chunkIterDump(it chunk.Iterator) any { return chunkIterDumpX(it, true) }

// chunkIterDumpX: preBegin calls Current before Begin (a multiIterator panics
// there: its current iterator is nil until Begin).
func chunkIterDumpX(it chunk.Iterator, preBegin bool) any {
	var out []any
	out = append(out, it.Len())
	cur := func(r chunk.Row) any {
		if r.IsEmpty() {
			return nil
		}
		return []int{r.Idx(), r.Len()}
	}
	if preBegin {
		out = append(out, cur(it.Current()))
	}
	for r := it.Begin(); r != it.End(); r = it.Next() {
		out = append(out, cur(r), cur(it.Current()))
	}
	out = append(out, cur(it.Current()), cur(it.Next()), cur(it.Current()))
	it.ReachEnd()
	out = append(out, cur(it.Current()), it.Error() == nil)
	return out
}

func chunkListDump(l *chunk.List, tr *memory.Tracker) any {
	var rows []int
	for i := range l.NumChunks() {
		rows = append(rows, l.NumRowsOfChunk(i))
	}
	return map[string]any{"len": l.Len(), "nch": l.NumChunks(), "rows": rows, "mem": tr.BytesConsumed(),
		"nft": len(l.FieldTypes()), "same": l.GetMemTracker() == tr}
}

// ---- steps -----------------------------------------------------------------

func time_Duration(n int64) time.Duration { return time.Duration(n) }

func chunkInts(raw json.RawMessage) []int {
	if string(raw) == "null" {
		return nil
	}
	var r []int
	must(json.Unmarshal(raw, &r))
	if r == nil {
		r = []int{}
	}
	return r
}

func chunkBools(raw json.RawMessage) []bool {
	var r []bool
	must(json.Unmarshal(raw, &r))
	return r
}

func chunkInt(raw json.RawMessage) int {
	var r int
	must(json.Unmarshal(raw, &r))
	return r
}

func chunkBool(raw json.RawMessage) bool {
	var r bool
	must(json.Unmarshal(raw, &r))
	return r
}

func chunkStr(raw json.RawMessage) string {
	var r string
	must(json.Unmarshal(raw, &r))
	return r
}

func chunkSpecs(raw json.RawMessage) []chunkTypeSpec {
	var r []chunkTypeSpec
	must(json.Unmarshal(raw, &r))
	return r
}

// chunkReadSize is the size of the fixed-length read Row.ToString (str) or
// Row.GetDatum makes for a cell of type ft (0: a variable-length read). The
// typed getters (GetInt64, GetTime, ...) check only the index of the first byte
// (&c.data[idx*size]); the read may run past the data's capacity when the cell
// was written with another type (a MutRow cell set to a FLOAT is 4 bytes, a
// BIT cell is variable-length): Go then reads whatever memory follows
// (undefined behaviour, D01.7 spec §4; Rust panics).
func chunkReadSize(ft *types.FieldType, str bool) int {
	switch ft.GetType() {
	case mysql.TypeFloat:
		return 4
	case mysql.TypeNewDecimal:
		return 40
	case mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeYear,
		mysql.TypeDouble, mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDuration:
		return 8
	case mysql.TypeBit:
		if str {
			return 8 // ETInt: GetInt64
		}
	}
	return 0
}

// chunkStrPastCap reports a read past the capacity, in the order Go reads the
// cells (an index past the length comes first: both panic, so it runs).
func chunkStrPastCap(c *chunk.Chunk, fts []*types.FieldType, rows []int, cols []int, str bool) bool {
	for _, idx := range rows {
		for _, col := range cols {
			cc := c.Column(col)
			size := chunkReadSize(fts[col], str)
			if cc.IsNull(idx) || size == 0 {
				continue
			}
			data := reflect.ValueOf(cc).Elem().FieldByName("data")
			off := idx * size
			if off >= data.Len() {
				return false
			}
			if off+size > data.Cap() {
				return true
			}
		}
	}
	return false
}

// chunkSetValuePastCap: MutRow.SetValue stores a Duration, *MyDecimal or Time
// with *(*T)(unsafe.Pointer(&col.data[0])) = x, checking only index 0; a cell
// shorter than T (set before to a FLOAT) is written past its capacity in Go
// (memory corruption, D01.7 spec §4; Rust panics). Reported instead of run (the
// step is skipped on both sides).
func chunkSetValuePastCap(mr chunk.MutRow, col int, v any) bool {
	size := 0
	switch v.(type) {
	case types.Duration:
		size = 8
	case *types.MyDecimal:
		size = types.MyDecimalStructSize
	case types.Time:
		size = 8
	}
	if size == 0 {
		return false
	}
	data := reflect.ValueOf(mr.ToRow().Chunk().Column(col)).Elem().FieldByName("data")
	return data.Len() > 0 && data.Cap() < size
}

func chunkAllCols(c *chunk.Chunk) []int {
	r := make([]int, c.NumCols())
	for i := range r {
		r[i] = i
	}
	return r
}

// chunkArr is a JSON array argument, element by element.
func chunkArr(raw json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	must(json.Unmarshal(raw, &a))
	return a
}

// chunkArrs is a JSON array of arrays.
func chunkArrs(raw json.RawMessage) [][]json.RawMessage {
	var a [][]json.RawMessage
	must(json.Unmarshal(raw, &a))
	return a
}

func chunkErr(err error) any {
	if err != nil {
		return map[string]any{"err": err.Error()}
	}
	return nil
}

// chunkStep runs one step. The steps:
//
//	new c types cap max | new_empty c types | new_cap c types cap | new_pool c types cap
//	renew d s max | clone_empty d s max | copy d s | copy_sel d s | prune d s idxs
//	app c col v | capp c col v | app_nn c col n | app_nb c col b | app_cell c col s scol pos times
//	app_row d s r | app_prow d off s r | app_row_idx d s r idxs | app_prow_idx d off s r idxs
//	app_rows d s rows | app_prows d off s rows | app_range d s b e | app_raw c col hex off
//	truncate c n | reset c | grow_reset c max | reconstruct c | set_sel c sel | set_req c req max
//	set_virt c n | set_inc c b | make_ref c src dst | make_ref_to d dcol s scol | swap_cols a b
//	swap_helper in out used | set_col d dcol s scol
//	resize c col kind n null | reserve c col kind n | col_reset c col et | set_null c col r b
//	set_nulls c col b e null | set_raw c col r hex | merge_nulls c col [[c2,col2],...]
//	col_copy d dcol s scol into | col_copy_recon d dcol s scol sel into | view_set c col kind r v
//	get c r col | view c col kind | cmp c1 r1 col1 c2 r2 col2 | iter c | iter_slice c rows | row c r
//	dump c | encode c | decode d types hex | decode_into c hex
//	dec_new id types | dec_reset id hex | dec_decode id c | dec_reuse id c | dec_state id
//	list_new l types init max | list_app l c r | list_add l c | list_alloc l c | list_reset l
//	list_clear l | list_get l ci ri | list_walk l | list_iter l | list_ptr_iter l ptrs
//	multi_iter [[kind, id, ...], ...] preBegin | copy_join_direct s sel d
//	copy_join_outer s io il oo ol sel d | copy_sel_rows d dcol s scol sel | copy_sel_rows_fn d dcol s scol sel start end
//	Datum / MutRow steps (after block D01.1): see chunk_gen2.go.
func chunkStep(st *chunkState, a []json.RawMessage) any {
	op := chunkStr(a[0])
	ci := func(i int) int { return chunkInt(a[i]) }
	ch := func(i int) *chunk.Chunk { return st.chks[ci(i)] }
	switch op {
	case "new":
		fts := chunkFts(chunkSpecs(a[2]))
		st.chks[ci(1)], st.fts[ci(1)] = chunk.New(fts, ci(3), ci(4)), fts
	case "new_empty":
		fts := chunkFts(chunkSpecs(a[2]))
		st.chks[ci(1)], st.fts[ci(1)] = chunk.NewEmptyChunk(fts), fts
	case "new_cap":
		fts := chunkFts(chunkSpecs(a[2]))
		st.chks[ci(1)], st.fts[ci(1)] = chunk.NewChunkWithCapacity(fts, ci(3)), fts
	case "new_pool":
		fts := chunkFts(chunkSpecs(a[2]))
		st.chks[ci(1)], st.fts[ci(1)] = chunk.NewChunkFromPoolWithCapacity(fts, ci(3)), fts
	case "renew":
		st.chks[ci(1)], st.fts[ci(1)] = chunk.Renew(ch(2), ci(3)), st.fts[ci(2)]
	case "clone_empty":
		st.chks[ci(1)], st.fts[ci(1)] = ch(2).CloneEmpty(ci(3)), st.fts[ci(2)]
	case "copy":
		st.chks[ci(1)], st.fts[ci(1)] = ch(2).CopyConstruct(), st.fts[ci(2)]
	case "copy_sel":
		st.chks[ci(1)], st.fts[ci(1)] = ch(2).CopyConstructSel(), st.fts[ci(2)]
	case "prune":
		idxs := chunkInts(a[3])
		fts := make([]*types.FieldType, len(idxs))
		for i, x := range idxs {
			fts[i] = st.fts[ci(2)][x]
		}
		st.chks[ci(1)], st.fts[ci(1)] = ch(2).Prune(idxs), fts
	case "app", "capp":
		chunkAppend(ch(1), st.fts[ci(1)][ci(2)], ci(2), a[3], op == "capp")
	case "app_nn":
		ch(1).Column(ci(2)).AppendNNulls(ci(3))
	case "app_nb":
		ch(1).Column(ci(2)).AppendNullBitmap(chunkBool(a[3]))
	case "app_cell":
		ch(1).Column(ci(2)).AppendCellNTimes(ch(3).Column(ci(4)), ci(5), ci(6))
	case "app_row":
		ch(1).AppendRow(ch(2).GetRow(ci(3)))
	case "app_prow":
		ch(1).AppendPartialRow(ci(2), ch(3).GetRow(ci(4)))
	case "app_row_idx":
		return ch(1).AppendRowByColIdxs(ch(2).GetRow(ci(3)), chunkInts(a[4]))
	case "app_prow_idx":
		return ch(1).AppendPartialRowByColIdxs(ci(2), ch(3).GetRow(ci(4)), chunkInts(a[5]))
	case "app_rows", "app_prows":
		s, off, ri := 2, 0, 3
		if op == "app_prows" {
			s, off, ri = 3, ci(2), 4
		}
		var rows []chunk.Row
		for _, r := range chunkInts(a[ri]) {
			rows = append(rows, ch(s).GetRow(r))
		}
		if op == "app_rows" {
			ch(1).AppendRows(rows)
		} else {
			ch(1).AppendPartialRows(off, rows)
		}
	case "app_range":
		ch(1).Append(ch(2), ci(3), ci(4))
	case "app_raw":
		b := chunkHex(chunkStr(a[3]))
		b = append(b, make([]byte, 16)...)
		return chunk.AppendCellFromRawData(ch(1).Column(ci(2)), unsafe.Pointer(&b[0]), ci(4))
	case "truncate":
		ch(1).TruncateTo(ci(2))
	case "reset":
		ch(1).Reset()
	case "grow_reset":
		ch(1).GrowAndReset(ci(2))
	case "reconstruct":
		ch(1).Reconstruct()
	case "set_sel":
		ch(1).SetSel(chunkInts(a[2]))
	case "set_req":
		ch(1).SetRequiredRows(ci(2), ci(3))
	case "set_virt":
		ch(1).SetNumVirtualRows(ci(2))
	case "set_inc":
		ch(1).SetInCompleteChunk(chunkBool(a[2]))
	case "make_ref":
		ch(1).MakeRef(ci(2), ci(3))
	case "make_ref_to":
		return chunkErr(ch(1).MakeRefTo(ci(2), ch(3), ci(4)))
	case "swap_cols":
		ch(1).SwapColumns(ch(2))
		st.fts[ci(1)], st.fts[ci(2)] = st.fts[ci(2)], st.fts[ci(1)]
	case "swap_helper":
		used := chunkInts(a[3])
		h := chunk.NewColumnSwapHelper(used)
		return chunkErr(h.SwapColumns(ch(1), ch(2)))
	case "set_col":
		old := ch(1).SetCol(ci(2), ch(3).Column(ci(4)))
		return old != nil
	case "resize":
		c := ch(1).Column(ci(2))
		n, isNull := ci(4), chunkBool(a[5])
		switch chunkStr(a[3]) {
		case "i64":
			c.ResizeInt64(n, isNull)
		case "u64":
			c.ResizeUint64(n, isNull)
		case "f32":
			c.ResizeFloat32(n, isNull)
		case "f64":
			c.ResizeFloat64(n, isNull)
		case "dec":
			c.ResizeDecimal(n, isNull)
		case "dur":
			c.ResizeGoDuration(n, isNull)
		case "time":
			c.ResizeTime(n, isNull)
		}
	case "reserve":
		c := ch(1).Column(ci(2))
		n := ci(4)
		switch chunkStr(a[3]) {
		case "str":
			c.ReserveString(n)
		case "bytes":
			c.ReserveBytes(n)
		case "json":
			c.ReserveJSON(n)
		case "vec":
			c.ReserveVectorFloat32(n)
		case "set":
			c.ReserveSet(n)
		case "enum":
			c.ReserveEnum(n)
		}
	case "col_reset":
		ch(1).Column(ci(2)).Reset(types.EvalType(ci(3)))
	case "set_null":
		ch(1).Column(ci(2)).SetNull(ci(3), chunkBool(a[4]))
	case "set_nulls":
		ch(1).Column(ci(2)).SetNulls(ci(3), ci(4), chunkBool(a[5]))
	case "set_raw":
		ch(1).Column(ci(2)).SetRaw(ci(3), chunkHex(chunkStr(a[4])))
	case "merge_nulls":
		var pairs [][2]int
		must(json.Unmarshal(a[3], &pairs))
		var cols []*chunk.Column
		for _, p := range pairs {
			cols = append(cols, st.chks[p[0]].Column(p[1]))
		}
		ch(1).Column(ci(2)).MergeNulls(cols...)
	case "col_copy":
		if chunkBool(a[5]) {
			ch(3).Column(ci(4)).CopyConstruct(ch(1).Column(ci(2)))
		} else {
			ch(1).SetCol(ci(2), ch(3).Column(ci(4)).CopyConstruct(nil))
		}
	case "col_copy_recon":
		sel := chunkInts(a[5])
		if chunkBool(a[6]) {
			ch(3).Column(ci(4)).CopyReconstruct(sel, ch(1).Column(ci(2)))
		} else {
			ch(1).SetCol(ci(2), ch(3).Column(ci(4)).CopyReconstruct(sel, nil))
		}
	case "view_set":
		c := ch(1).Column(ci(2))
		r := ci(4)
		switch chunkStr(a[3]) {
		case "i64":
			c.Int64s()[r] = chunkI64(a[5])
		case "u64":
			c.Uint64s()[r] = chunkU64(a[5])
		case "f32":
			c.Float32s()[r] = math.Float32frombits(uint32(chunkU64(a[5])))
		case "f64":
			c.Float64s()[r] = math.Float64frombits(chunkU64(a[5]))
		case "dur":
			c.GoDurations()[r] = time_Duration(chunkI64(a[5]))
		case "time":
			c.Times()[r] = types.NewTime(types.CoreTime(chunkU64(a[5])), mysql.TypeDatetime, 0)
		case "dec":
			d := new(types.MyDecimal)
			_ = d.FromString([]byte(chunkStr(a[5])))
			c.Decimals()[r] = *d
		}
	case "get":
		c := ch(1)
		return chunkGet(c.GetRow(ci(2)), st.fts[ci(1)][ci(3)], ci(3))
	case "view":
		c := ch(1).Column(ci(2))
		// The typed views are slice headers with Len = length and Cap = cap(data)/size; when
		// Len > Cap Go reads past the data's capacity (undefined behaviour, D01.7 spec §4
		// item 4: Rust panics). Record that instead of whatever memory Go happens to read.
		size := map[string]int{"i64": 8, "u64": 8, "f32": 4, "f64": 8, "dur": 8, "time": 8, "dec": 40}[chunkStr(a[3])]
		if dc := reflect.ValueOf(c).Elem().FieldByName("data").Cap(); size > 0 && c.Rows()*size > dc {
			return map[string]any{"past_cap": []int{c.Rows(), dc}}
		}
		var out []string
		switch chunkStr(a[3]) {
		case "i64":
			for _, x := range c.Int64s() {
				out = append(out, strconv.FormatInt(x, 10))
			}
		case "u64":
			for _, x := range c.Uint64s() {
				out = append(out, strconv.FormatUint(x, 10))
			}
		case "f32":
			for _, x := range c.Float32s() {
				out = append(out, strconv.FormatUint(uint64(math.Float32bits(x)), 10))
			}
		case "f64":
			for _, x := range c.Float64s() {
				out = append(out, strconv.FormatUint(math.Float64bits(x), 10))
			}
		case "dur":
			for _, x := range c.GoDurations() {
				out = append(out, strconv.FormatInt(int64(x), 10))
			}
		case "time":
			for _, x := range c.Times() {
				out = append(out, x.String())
			}
		case "dec":
			for i := range c.Decimals() {
				out = append(out, c.Decimals()[i].String())
			}
		}
		return []any{out, c.Rows()}
	case "cmp":
		f := chunk.GetCompareFunc(st.fts[ci(1)][ci(3)])
		if f == nil {
			return "nil"
		}
		return f(ch(1).GetRow(ci(2)), ci(3), ch(4).GetRow(ci(5)), ci(6))
	case "iter":
		it := chunk.NewIterator4Chunk(ch(1))
		r := chunkIterDump(it)
		it.ResetChunk(it.GetChunk())
		return r
	case "iter_slice":
		var rows []chunk.Row
		for _, r := range chunkInts(a[2]) {
			rows = append(rows, ch(1).GetRow(r))
		}
		it := chunk.NewIterator4Slice(rows)
		r := chunkIterDump(it)
		it.Reset(rows[:len(rows)/2])
		return []any{r, chunkIterDump(it)}
	case "row":
		r := ch(1).GetRow(ci(2))
		cp := r.CopyConstruct()
		return []any{r.Idx(), r.Len(), r.IsEmpty(), r.Chunk() == ch(1), cp.Idx(), chunkDump(cp.Chunk())}
	case "dump":
		return chunkDump(ch(1))
	case "encode":
		return hex.EncodeToString(chunk.NewCodec(st.fts[ci(1)]).Encode(ch(1)))
	case "decode":
		fts := chunkFts(chunkSpecs(a[2]))
		c, rest := chunk.NewCodec(fts).Decode(chunkHex(chunkStr(a[3])))
		st.chks[ci(1)], st.fts[ci(1)] = c, fts
		return len(rest)
	case "decode_into":
		rest := chunk.NewCodec(st.fts[ci(1)]).DecodeToChunk(chunkHex(chunkStr(a[2])), ch(1))
		return len(rest)
	case "dec_new":
		fts := chunkFts(chunkSpecs(a[2]))
		st.decs[ci(1)], st.dfts[ci(1)] = chunk.NewDecoder(chunk.NewChunkWithCapacity(fts, 0), fts), fts
	case "dec_reset":
		st.decs[ci(1)].Reset(chunkHex(chunkStr(a[2])))
		return st.decs[ci(1)].RemainedRows()
	case "dec_decode":
		st.decs[ci(1)].Decode(ch(2))
		return []any{st.decs[ci(1)].RemainedRows(), st.decs[ci(1)].IsFinished()}
	case "dec_reuse":
		st.decs[ci(1)].ReuseIntermChk(ch(2))
		return st.decs[ci(1)].IsFinished()
	case "dec_state":
		return []any{st.decs[ci(1)].RemainedRows(), st.decs[ci(1)].IsFinished()}
	case "list_new":
		fts := chunkFts(chunkSpecs(a[2]))
		tr := memory.NewTracker(memory.LabelForChunkList, -1)
		st.lists[ci(1)], st.ltracks[ci(1)] = chunk.NewListWithMemTracker(fts, ci(3), ci(4), tr), tr
	case "list_app":
		p := st.lists[ci(1)].AppendRow(ch(2).GetRow(ci(3)))
		return []uint32{p.ChkIdx, p.RowIdx}
	case "list_add":
		st.lists[ci(1)].Add(ch(2))
		delete(st.chks, ci(2))
	case "list_alloc":
		c := st.lists[ci(1)].AllocChunk()
		st.chks[ci(2)], st.fts[ci(2)] = c, st.lists[ci(1)].FieldTypes()
		return chunkDump(c)
	case "list_reset":
		st.lists[ci(1)].Reset()
	case "list_clear":
		st.lists[ci(1)].Clear()
	case "list_get":
		l := st.lists[ci(1)]
		r := l.GetRow(chunk.RowPtr{ChkIdx: uint32(ci(2)), RowIdx: uint32(ci(3))})
		var out []any
		for col, ft := range l.FieldTypes() {
			out = append(out, chunkGet(r, ft, col))
		}
		return []any{out, chunkDump(l.GetChunk(ci(2)))}
	case "list_walk":
		var out [][2]int
		err := st.lists[ci(1)].Walk(func(r chunk.Row) error {
			out = append(out, [2]int{r.Idx(), r.Len()})
			return nil
		})
		return []any{out, err == nil}
	case "list_iter":
		return chunkIterDump(chunk.NewIterator4List(st.lists[ci(1)]))
	case "list_ptr_iter":
		var ptrs []chunk.RowPtr
		var raw [][2]uint32
		must(json.Unmarshal(a[2], &raw))
		for _, p := range raw {
			ptrs = append(ptrs, chunk.RowPtr{ChkIdx: p[0], RowIdx: p[1]})
		}
		return chunkIterDump(chunk.NewIterator4RowPtr(st.lists[ci(1)], ptrs))
	case "multi_iter":
		var specs [][]int
		must(json.Unmarshal(a[1], &specs))
		var its []chunk.Iterator
		for _, s := range specs {
			switch s[0] {
			case 0:
				its = append(its, chunk.NewIterator4Chunk(st.chks[s[1]]))
			case 1:
				its = append(its, chunk.NewIterator4List(st.lists[s[1]]))
			case 2:
				var rows []chunk.Row
				for _, r := range s[2:] {
					rows = append(rows, st.chks[s[1]].GetRow(r))
				}
				its = append(its, chunk.NewIterator4Slice(rows))
			}
		}
		return chunkIterDumpX(chunk.NewMultiIterator(its...), chunkInt(a[2]) != 0)
	case "copy_join_direct":
		ok, err := chunk.CopySelectedJoinRowsDirect(ch(1), chunkBools(a[2]), ch(3))
		return []any{ok, chunkErr(err)}
	case "copy_join_outer":
		ok, err := chunk.CopySelectedJoinRowsWithSameOuterRows(ch(1), ci(2), ci(3), ci(4), ci(5), chunkBools(a[6]), ch(7))
		return []any{ok, chunkErr(err)}
	case "copy_sel_rows":
		chunk.CopySelectedRows(ch(1).Column(ci(2)), ch(3).Column(ci(4)), chunkBools(a[5]))
	case "copy_sel_rows_fn":
		sel := chunkBools(a[5])
		n := len(sel)
		chunk.CopySelectedRowsWithRowIDFunc(ch(1).Column(ci(2)), ch(3).Column(ci(4)), sel, ci(6), ci(7), func(i int) int {
			return n - 1 - i
		})
	// ---- types.Datum / Enum / Set (block D01.1) ----
	case "app_datum":
		d := d2DatumIn(chunkArr(a[3]))
		ch(1).AppendDatum(ci(2), &d)
	case "get_datum":
		ft := st.fts[ci(1)][ci(3)]
		if string(a[4]) != "null" {
			ft = d2FtIn(chunkArr(a[4]))
		}
		return d2DatumOut(ch(1).GetRow(ci(2)).GetDatum(ci(3), ft))
	case "datum_row":
		return datumOuts(ch(1).GetRow(ci(2)).GetDatumRow(st.fts[ci(1)]))
	case "datum_row_buf":
		var buf []types.Datum
		for _, p := range chunkArrs(a[3]) {
			buf = append(buf, d2DatumIn(p))
		}
		return datumOuts(ch(1).GetRow(ci(2)).GetDatumRowWithBuffer(st.fts[ci(1)], buf))
	case "to_string":
		c := ch(1)
		var rows []int
		for i := range c.NumRows() {
			rows = append(rows, c.GetRow(i).Idx())
		}
		if chunkStrPastCap(c, st.fts[ci(1)], rows, chunkAllCols(c), true) {
			return map[string]any{"past_cap": true}
		}
		return vec.GoString(c.ToString(st.fts[ci(1)]))
	case "row_string":
		r := ch(1).GetRow(ci(2))
		if chunkStrPastCap(ch(1), st.fts[ci(1)], []int{r.Idx()}, chunkAllCols(ch(1)), true) {
			return map[string]any{"past_cap": true}
		}
		return vec.GoString(r.ToString(st.fts[ci(1)]))
	case "cmp_datum":
		d := d2DatumIn(chunkArr(a[4]))
		return chunk.Compare(ch(1).GetRow(ci(2)), ci(3), &d)
	case "lower_bound":
		d := d2DatumIn(chunkArr(a[3]))
		i, m := ch(1).LowerBound(ci(2), &d)
		return []any{i, m}
	case "upper_bound":
		d := d2DatumIn(chunkArr(a[3]))
		return ch(1).UpperBound(ci(2), &d)
	// ---- MutRow ----
	case "mr_values":
		var vals []any
		for _, p := range chunkArrs(a[2]) {
			vals = append(vals, goValueIn(p))
		}
		st.mrs[ci(1)] = chunk.MutRowFromValues(vals...)
	case "mr_datums":
		var ds []types.Datum
		for _, p := range chunkArrs(a[2]) {
			ds = append(ds, d2DatumIn(p))
		}
		st.mrs[ci(1)] = chunk.MutRowFromDatums(ds)
	case "mr_types":
		st.mrs[ci(1)] = chunk.MutRowFromTypes(chunkFts(chunkSpecs(a[2])))
	case "mr_set_row":
		st.mrs[ci(1)].SetRow(ch(2).GetRow(ci(3)))
	case "mr_set_values":
		var vals []any
		for _, p := range chunkArrs(a[2]) {
			vals = append(vals, goValueIn(p))
		}
		for i, v := range vals {
			if chunkSetValuePastCap(st.mrs[ci(1)], i, v) {
				return map[string]any{"past_cap": true}
			}
		}
		st.mrs[ci(1)].SetValues(vals...)
	case "mr_set_value":
		v := goValueIn(chunkArr(a[3]))
		if chunkSetValuePastCap(st.mrs[ci(1)], ci(2), v) {
			return map[string]any{"past_cap": true}
		}
		st.mrs[ci(1)].SetValue(ci(2), v)
	case "mr_set_datums":
		var ds []types.Datum
		for _, p := range chunkArrs(a[2]) {
			ds = append(ds, d2DatumIn(p))
		}
		st.mrs[ci(1)].SetDatums(ds...)
	case "mr_set_datum":
		st.mrs[ci(1)].SetDatum(ci(2), d2DatumIn(chunkArr(a[3])))
	case "mr_shallow":
		st.mrs[ci(1)].ShallowCopyPartialRow(ci(2), ch(3).GetRow(ci(4)))
	case "mr_clone":
		st.mrs[ci(1)] = st.mrs[ci(2)].Clone()
	case "mr_dump":
		mr := st.mrs[ci(1)]
		return []any{mr.Len(), chunkDump(mr.ToRow().Chunk())}
	case "mr_get":
		r := st.mrs[ci(1)].ToRow()
		fts := chunkFts(chunkSpecs(a[3]))
		full := make([]*types.FieldType, r.Len())
		full[ci(2)] = fts[0]
		if chunkStrPastCap(r.Chunk(), full, []int{r.Idx()}, []int{ci(2)}, false) {
			return map[string]any{"past_cap": true}
		}
		return []any{d2DatumOut(r.GetDatum(ci(2), fts[0])), hex.EncodeToString(r.GetRaw(ci(2))), r.IsNull(ci(2))}
	case "mr_string":
		r := st.mrs[ci(1)].ToRow()
		fts := chunkFts(chunkSpecs(a[2]))
		if chunkStrPastCap(r.Chunk(), fts, []int{r.Idx()}, chunkAllCols(r.Chunk()), true) {
			return map[string]any{"past_cap": true}
		}
		return vec.GoString(r.ToString(fts))
	default:
		panic(fmt.Sprintf("chunk: unknown step %q", op))
	}
	return nil
}

func chunkScript(steps [][]json.RawMessage) (out []any) {
	st := newChunkState()
	for _, s := range steps {
		var r any
		panicked := func() (msg any) {
			defer func() { msg = recover() }()
			r = chunkStep(st, s)
			return nil
		}()
		if panicked != nil {
			out = append(out, map[string]any{"panic": fmt.Sprint(panicked)})
			return
		}
		out = append(out, r)
	}
	return
}

func chunkExecOp(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "script":
		var steps [][]json.RawMessage
		if err := json.Unmarshal(args[0], &steps); err != nil {
			return nil, err
		}
		return chunkScript(steps), nil
	case "type_width":
		var specs []chunkTypeSpec
		if err := json.Unmarshal(args[0], &specs); err != nil {
			return nil, err
		}
		var out [][2]int
		for _, ft := range chunkFts(specs) {
			out = append(out, [2]int{chunk.GetFixedLen(ft), chunk.EstimateTypeWidth(ft)})
		}
		return out, nil
	}
	return nil, fmt.Errorf("chunk: unknown op %q", op)
}
