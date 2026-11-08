package domains

// Go side of the D01.8 codec benchmarks: the same workloads as tidb-rs
// crates/codec/benches/codec.rs. The first five follow pkg/util/codec/bench_test.go
// (DecodeOneToChunk resets its chunk every 1024 rows instead of growing it for b.N rows);
// the others are the point-get / index (EncodeKey, DecodeOne) and hash join / aggregation
// (HashChunkRow, HashChunkSelected, SerializeKeys) paths.
//
//	cd govec && go test ./domains -run '^$' -bench Codec -benchtime 2s -count 5

import (
	"bytes"
	"hash"
	"hash/fnv"
	"testing"
	gotime "time"

	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
)

func BenchmarkCodecDecodeWithSize(b *testing.B) {
	values := make([]types.Datum, 0, 100)
	for i := range 100 {
		values = append(values, types.NewDatum(i))
	}
	bs, _ := codec.EncodeValue(gotime.UTC, nil, values...)
	b.ResetTimer()
	for range b.N {
		if _, err := codec.Decode(bs, 100); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecDecodeWithOutSize(b *testing.B) {
	values := make([]types.Datum, 0, 100)
	for i := range 100 {
		values = append(values, types.NewDatum(i))
	}
	bs, _ := codec.EncodeValue(gotime.UTC, nil, values...)
	b.ResetTimer()
	for range b.N {
		if _, err := codec.Decode(bs, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecEncodeIntWithSize(b *testing.B) {
	for range b.N {
		data := make([]byte, 0, 8)
		codecSink = codec.EncodeInt(data, 10)
	}
}

var codecSink []byte

func BenchmarkCodecDecodeDecimal(b *testing.B) {
	dec := &types.MyDecimal{}
	_ = dec.FromFloat64(1211.1211113)
	precision, frac := dec.PrecisionAndFrac()
	raw, _ := codec.EncodeDecimal([]byte{}, dec, precision, frac)
	b.ResetTimer()
	for range b.N {
		if _, _, _, _, err := codec.DecodeDecimal(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecDecodeOneToChunk(b *testing.B) {
	raw := codec.EncodeBytes([]byte{1}, []byte("a"))
	ft := types.NewFieldType(mysql.TypeVarchar)
	chk := chunk.New([]*types.FieldType{ft}, 32, 1024)
	decoder := codec.NewDecoder(chk, gotime.UTC)
	b.ResetTimer()
	for i := range b.N {
		if i%1024 == 0 {
			chk.Reset()
		}
		if _, err := decoder.DecodeOne(raw, 0, ft); err != nil {
			b.Fatal(err)
		}
	}
}

func codecBenchTuple() []types.Datum {
	dec := new(types.MyDecimal)
	_ = dec.FromString([]byte("12345.678"))
	t, _ := types.ParseTime(types.DefaultStmtNoWarningContext, "2011-11-10 11:11:11.123456", mysql.TypeDatetime, 6)
	var d [5]types.Datum
	d[0].SetInt64(-123456789)
	d[1].SetString("Hello, World — ÄÖÜ", "utf8mb4_general_ci")
	d[2].SetMysqlDecimal(dec)
	d[3].SetMysqlTime(t)
	d[4].SetFloat64(3.25)
	return d[:]
}

func BenchmarkCodecEncodeKey(b *testing.B) {
	collate.SetNewCollationEnabledForTest(true)
	ds := codecBenchTuple()
	buf := make([]byte, 0, 128)
	b.ResetTimer()
	for range b.N {
		var err error
		buf, err = codec.EncodeKey(gotime.UTC, buf[:0], ds...)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecDecodeOne(b *testing.B) {
	bs, _ := codec.EncodeValue(gotime.UTC, nil, codecBenchTuple()...)
	b.ResetTimer()
	for range b.N {
		rest := bs
		for len(rest) > 0 {
			var err error
			rest, _, err = codec.DecodeOne(rest)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// codecBenchChunk: 1024 rows of (BIGINT, VARCHAR utf8mb4_general_ci, DECIMAL(10,3)).
func codecBenchChunk() ([]*types.FieldType, *chunk.Chunk) {
	collate.SetNewCollationEnabledForTest(true)
	fts := []*types.FieldType{
		types.NewFieldType(mysql.TypeLonglong),
		types.NewFieldTypeBuilder().SetType(mysql.TypeVarchar).SetFlen(64).SetCharset("utf8mb4").SetCollate("utf8mb4_general_ci").BuildP(),
		types.NewFieldTypeBuilder().SetType(mysql.TypeNewDecimal).SetFlen(10).SetDecimal(3).BuildP(),
	}
	chk := chunk.NewChunkWithCapacity(fts, 1024)
	for i := range 1024 {
		chk.AppendInt64(0, int64(i*7919)-3000000)
		chk.AppendString(1, []string{"apple", "Banana", "cherry pie", "Ärger", "straße"}[i%5])
		dec := new(types.MyDecimal)
		_ = dec.FromInt(int64(i * 37))
		chk.AppendMyDecimal(2, dec)
	}
	return fts, chk
}

func BenchmarkCodecHashChunkRow(b *testing.B) {
	fts, chk := codecBenchChunk()
	ctx := types.DefaultStmtNoWarningContext
	colIdx := []int{0, 1, 2}
	buf := make([]byte, 1)
	var w bytes.Buffer
	b.ResetTimer()
	for range b.N {
		for i := range 1024 {
			w.Reset()
			if err := codec.HashChunkRow(ctx, &w, chk.GetRow(i), fts, colIdx, buf); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkCodecHashChunkSelected(b *testing.B) {
	fts, chk := codecBenchChunk()
	ctx := types.DefaultStmtNoWarningContext
	hs := make([]hash.Hash64, 1024)
	isNull := make([]bool, 1024)
	buf := make([]byte, 1)
	b.ResetTimer()
	for range b.N {
		for i := range hs {
			hs[i] = fnv.New64()
		}
		for c := range fts {
			if err := codec.HashChunkSelected(ctx, hs, chk, fts[c], c, buf, isNull, nil, false); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkCodecSerializeKeys(b *testing.B) {
	fts, chk := codecBenchChunk()
	ctx := types.DefaultStmtNoWarningContext
	used := make([]int, 1024)
	for i := range used {
		used[i] = i
	}
	keys := make([][]byte, 1024)
	nulls := make([]bool, 1024)
	b.ResetTimer()
	for range b.N {
		for i := range keys {
			keys[i] = keys[i][:0]
		}
		for c := range fts {
			if err := codec.SerializeKeys(ctx, chk, fts[c], c, used, nil, nulls, codec.KeepVarColumnLength, keys); err != nil {
				b.Fatal(err)
			}
		}
	}
}
