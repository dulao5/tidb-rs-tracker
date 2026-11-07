package domains

// Go side of the tidb-rs JSON benchmarks (crates/types/benches/json.rs): the same
// documents and operations. Run: go test -bench . -run '^$' ./domains/

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pingcap/tidb/pkg/types"
)

func benchSmall() string {
	return `{"id": 12345, "name": "tidb-rs", "tags": ["a", "b", "c"], "score": 3.5, "ok": true}`
}

func benchMedium() string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"key%02d": {"id": %d, "name": "item number %d", "vals": [1, 2.5, "x", null, false], "nested": {"a": %d}}`, i, i, i, i)
	}
	b.WriteString("}")
	return b.String()
}

func BenchmarkJSON(b *testing.B) {
	sm, md := benchSmall(), benchMedium()
	bjMd, _ := types.ParseBinaryJSONFromString(md)
	bjSm, _ := types.ParseBinaryJSONFromString(sm)
	path, _ := types.ParseJSONPathExpr("$.key20.nested.a")
	b.Run("parse_small", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = types.ParseBinaryJSONFromString(sm)
		}
	})
	b.Run("parse_medium", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = types.ParseBinaryJSONFromString(md)
		}
	})
	b.Run("string_medium", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = bjMd.String()
		}
	})
	b.Run("extract_const_path", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = bjMd.Extract([]types.JSONPathExpression{path})
		}
	})
	b.Run("parse_path", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = types.ParseJSONPathExpr("$.key20.nested.a")
		}
	})
	b.Run("compare_medium", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = types.CompareBinaryJSON(bjMd, bjMd)
		}
	})
	b.Run("hash_small", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = bjSm.HashValue(nil)
		}
	})
}
