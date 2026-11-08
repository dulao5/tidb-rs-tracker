package domains

// Datum hot operations; the Rust side is tidb-rs crates/types/benches/datum.rs
// (same inputs and loop bodies).

import (
	"testing"

	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
)

var datumSink any

func BenchmarkDatum(b *testing.B) {
	collate.SetNewCollationEnabledForTest(true)
	ctx := types.DefaultStmtNoWarningContext
	bin := collate.GetCollator("utf8mb4_bin")
	ci := collate.GetCollator("utf8mb4_general_ci")
	x := types.NewIntDatum(12345)
	y := types.NewIntDatum(12346)
	b.Run("compare_int", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c, _ := x.Compare(ctx, &y, bin)
			datumSink = c
		}
	})
	s1 := types.NewCollationStringDatum("hello world, tidb", "utf8mb4_bin")
	s2 := types.NewCollationStringDatum("hello world, tidc", "utf8mb4_bin")
	b.Run("compare_string_bin", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c, _ := s1.Compare(ctx, &s2, bin)
			datumSink = c
		}
	})
	b.Run("compare_string_general_ci", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			c, _ := s1.Compare(ctx, &s2, ci)
			datumSink = c
		}
	})
	varchar := types.NewFieldTypeBuilder().SetType(mysql.TypeVarchar).SetFlen(32).SetCharset("utf8mb4").SetCollate("utf8mb4_bin").BuildP()
	b.Run("convert_int_to_varchar", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d, _ := x.ConvertTo(ctx, varchar)
			datumSink = d
		}
	})
	bigint := types.NewFieldType(mysql.TypeLonglong)
	ns := types.NewStringDatum("123456789")
	b.Run("convert_string_to_bigint", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d, _ := ns.ConvertTo(ctx, bigint)
			datumSink = d
		}
	})
	dec := types.NewFieldTypeBuilder().SetType(mysql.TypeNewDecimal).SetFlen(10).SetDecimal(2).BuildP()
	f := types.NewFloat64Datum(12345.678)
	b.Run("convert_float_to_decimal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d, _ := f.ConvertTo(ctx, dec)
			datumSink = d
		}
	})
	md := new(types.MyDecimal)
	_ = md.FromString([]byte("12345.678"))
	dd := types.NewDecimalDatum(md)
	b.Run("decimal_to_int64", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			v, _ := dd.ToInt64(ctx)
			datumSink = v
		}
	})
	b.Run("clone_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			datumSink = s1.Clone()
		}
	})
	b.Run("set_string", func(b *testing.B) {
		var d types.Datum
		for i := 0; i < b.N; i++ {
			d.SetString("hello world, tidb", "utf8mb4_bin")
		}
		datumSink = d
	})
}
