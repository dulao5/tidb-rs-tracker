package domains

// Go side of the D01.11 benchmarks (tidb-rs crates/tablecodec/benches/tablecodec.rs, same
// inputs: tablecodecBenchInputs, printed by TestTablecodecBenchInputs with -v).

import (
	"encoding/hex"
	"os"
	"testing"
	gotime "time"

	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/tablecodec"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/rowcodec"
)

type tcBenchIn struct {
	row3, row10, rowV1, idxIntVal, idxCommonVal, idxKey, idxVal []byte
}

func tcBenchFts10() []*types.FieldType {
	fts := []*types.FieldType{
		types.NewFieldType(mysql.TypeLonglong), types.NewFieldType(mysql.TypeLong), types.NewFieldType(mysql.TypeDouble),
		types.NewFieldType(mysql.TypeVarchar), types.NewFieldType(mysql.TypeVarchar), types.NewFieldType(mysql.TypeNewDecimal),
		types.NewFieldType(mysql.TypeDatetime), types.NewFieldType(mysql.TypeDuration), types.NewFieldType(mysql.TypeLonglong),
		types.NewFieldType(mysql.TypeBlob),
	}
	fts[3].SetCollate("utf8mb4_bin")
	fts[4].SetCollate("utf8mb4_general_ci")
	fts[5].SetFlen(10)
	fts[5].SetDecimal(2)
	return fts
}

func tcBenchInputs() tcBenchIn {
	collate.SetNewCollationEnabledForTest(true)
	var in tcBenchIn
	var e rowcodec.Encoder
	in.row3, _ = e.Encode(nil, []int64{-1, 2, 3}, types.MakeDatums(1, "abc", 1.1), nil, nil)
	dt, _ := types.ParseDatetime(types.DefaultStmtNoWarningContext, "2024-05-06 07:08:09")
	dec := types.NewDecFromStringForTest("123.45")
	row10 := []types.Datum{types.NewIntDatum(42), types.NewIntDatum(-7), types.NewFloat64Datum(3.25),
		types.NewStringDatum("hello world"), types.NewStringDatum("Tidb Rust"), types.NewDecimalDatum(dec),
		types.NewTimeDatum(dt), types.NewDurationDatum(types.Duration{Duration: 3723 * gotime.Second}),
		types.NewIntDatum(1 << 40), types.NewBytesDatum([]byte("some blob payload"))}
	var e2 rowcodec.Encoder
	in.row10, _ = e2.Encode(gotime.UTC, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, row10, nil, nil)
	in.rowV1, _ = tablecodec.EncodeOldRow(gotime.UTC, row10, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, nil, nil)
	in.idxIntVal = tablecodec.EncodeHandleInUniqueIndexValue(kv.IntHandle(256), false)
	v := []byte{0, tablecodec.IndexVersionFlag, 1}
	encoded, _ := codec.EncodeKey(gotime.UTC, nil, types.MakeDatums(1, 2)...)
	h, _ := kv.NewCommonHandle(encoded)
	in.idxCommonVal = append(v, h.Encoded()...)
	in.idxCommonVal = append([]byte{0, tablecodec.IndexVersionFlag, 1, tablecodec.CommonHandleFlag, 0, byte(len(h.Encoded()))}, h.Encoded()...)
	// A non-unique general_ci index (restored data, int handle).
	ft := types.NewFieldType(mysql.TypeVarchar)
	ft.SetCharset("utf8mb4")
	ft.SetCollate("utf8mb4_general_ci")
	tbl := &model.TableInfo{ID: 100, Columns: []*model.ColumnInfo{{ID: 1, Offset: 0, FieldType: *ft, State: model.StatePublic}}}
	idx := &model.IndexInfo{ID: 2, Columns: []*model.IndexColumn{{Offset: 0, Length: types.UnspecifiedLength}}, State: model.StatePublic}
	tbl.Indices = []*model.IndexInfo{idx}
	vals := []types.Datum{types.NewCollationStringDatum("Hello World", "utf8mb4_general_ci")}
	in.idxKey, _, _ = tablecodec.GenIndexKey(gotime.UTC, tbl, idx, 100, vals, kv.IntHandle(12345), nil)
	in.idxVal, _ = tablecodec.GenIndexValuePortal(gotime.UTC, tbl, idx, true, false, false, vals, kv.IntHandle(12345), 0, nil, nil)
	return in
}

func TestTablecodecBenchInputs(t *testing.T) {
	if os.Getenv("GOVEC_PRINT_BENCH") == "" {
		t.Skip("set GOVEC_PRINT_BENCH=1 to print the inputs")
	}
	in := tcBenchInputs()
	for _, kv := range []struct {
		n string
		b []byte
	}{{"row3", in.row3}, {"row10", in.row10}, {"row_v1", in.rowV1}, {"idx_int_val", in.idxIntVal},
		{"idx_common_val", in.idxCommonVal}, {"idx_key", in.idxKey}, {"idx_val", in.idxVal}} {
		t.Logf("%s %s", kv.n, hex.EncodeToString(kv.b))
	}
}

func BenchmarkTCEncodeRowKeyWithHandle(b *testing.B) {
	for range b.N {
		tablecodec.EncodeRowKeyWithHandle(100, kv.IntHandle(100))
	}
}

func BenchmarkTCDecodeRowKey(b *testing.B) {
	k := tablecodec.EncodeRowKeyWithHandle(100, kv.IntHandle(100))
	for range b.N {
		_, _ = tablecodec.DecodeRowKey(k)
	}
}

func BenchmarkTCDecodeHandleInIndexValueInt(b *testing.B) {
	in := tcBenchInputs()
	for range b.N {
		_, _ = tablecodec.DecodeHandleInIndexValue(in.idxIntVal)
	}
}

func BenchmarkTCDecodeHandleInIndexValueCommon(b *testing.B) {
	in := tcBenchInputs()
	for range b.N {
		_, _ = tablecodec.DecodeHandleInIndexValue(in.idxCommonVal)
	}
}

func BenchmarkTCDecodeToChunk3(b *testing.B) {
	in := tcBenchInputs()
	tps := []*types.FieldType{types.NewFieldType(mysql.TypeLonglong), types.NewFieldType(mysql.TypeString), types.NewFieldType(mysql.TypeDouble)}
	cols := []rowcodec.ColInfo{{ID: -1, Ft: tps[0]}, {ID: 2, Ft: tps[1]}, {ID: 3, Ft: tps[2]}}
	d := rowcodec.NewChunkDecoder(cols, []int64{-1}, nil, gotime.UTC)
	chk := chunk.NewChunkWithCapacity(tps, 1)
	for range b.N {
		chk.Reset()
		_ = d.DecodeToChunk(in.row3, 0, kv.IntHandle(1), chk)
	}
}

func BenchmarkTCDecodeToChunk10(b *testing.B) {
	in := tcBenchInputs()
	fts := tcBenchFts10()
	cols := make([]rowcodec.ColInfo, len(fts))
	for i, ft := range fts {
		cols[i] = rowcodec.ColInfo{ID: int64(i + 1), Ft: ft}
	}
	d := rowcodec.NewChunkDecoder(cols, nil, nil, gotime.UTC)
	chk := chunk.NewChunkWithCapacity(fts, 1024)
	for i := range b.N {
		if i%1024 == 0 {
			chk.Reset()
		}
		_ = d.DecodeToChunk(in.row10, 0, nil, chk)
	}
}

func BenchmarkTCDecodeToDatumMap10(b *testing.B) {
	in := tcBenchInputs()
	fts := tcBenchFts10()
	cols := map[int64]*types.FieldType{}
	for i, ft := range fts {
		cols[int64(i+1)] = ft
	}
	for range b.N {
		_, _ = tablecodec.DecodeRowToDatumMap(in.row10, cols, gotime.UTC)
	}
}

func BenchmarkTCCutRowNewV1(b *testing.B) {
	in := tcBenchInputs()
	ids := map[int64]int{}
	for i := range 10 {
		ids[int64(i+1)] = i
	}
	for range b.N {
		_, _ = tablecodec.CutRowNew(in.rowV1, ids)
	}
}

func BenchmarkTCDecodeIndexKVRestored(b *testing.B) {
	collate.SetNewCollationEnabledForTest(true)
	in := tcBenchInputs()
	ft := types.NewFieldType(mysql.TypeVarchar)
	ft.SetCharset("utf8mb4")
	ft.SetCollate("utf8mb4_general_ci")
	cols := []rowcodec.ColInfo{{ID: 1, Ft: ft}}
	for range b.N {
		_, _ = tablecodec.DecodeIndexKV(in.idxKey, in.idxVal, 1, tablecodec.HandleDefault, cols)
	}
}

func BenchmarkTCChecksum3(b *testing.B) {
	datums := types.MakeDatums(1, "abc", 1.1)
	cols := []rowcodec.ColData{
		{ColumnInfo: &model.ColumnInfo{ID: 1, FieldType: *types.NewFieldType(mysql.TypeLong)}, Datum: &datums[0]},
		{ColumnInfo: &model.ColumnInfo{ID: 2, FieldType: *types.NewFieldType(mysql.TypeVarchar)}, Datum: &datums[1]},
		{ColumnInfo: &model.ColumnInfo{ID: 3, FieldType: *types.NewFieldType(mysql.TypeDouble)}, Datum: &datums[2]},
	}
	row := rowcodec.RowData{Cols: cols}
	for range b.N {
		_, _ = row.Checksum(gotime.UTC)
	}
}
