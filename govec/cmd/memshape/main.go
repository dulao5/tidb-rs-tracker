// Command memshape measures Go TiDB v8.5.8's heap for the shape of tidb-rs issue #229
// (spec N13 §5): 10,000 tables x 30 columns x 3 indices, as model.TableInfo and as the
// table objects of tables.TableFromMeta, which point at the TableInfo's columns and
// indices. The Rust side is crates/infoschema/tests/arc_sharing_mem.rs (same shape).
//
//	go run ./cmd/memshape
package main

import (
	"fmt"
	"runtime"

	"github.com/pingcap/tidb/pkg/meta/autoid"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/table"
	"github.com/pingcap/tidb/pkg/table/tables"
	"github.com/pingcap/tidb/pkg/types"
)

const (
	nTables = 10000
	nCols   = 30
	nIdxs   = 3
)

func column(id int64, offset int, tp byte) *model.ColumnInfo {
	ft := types.NewFieldType(tp)
	if tp == mysql.TypeVarchar {
		ft.SetFlen(64)
		ft.SetCharset("utf8mb4")
		ft.SetCollate("utf8mb4_bin")
	}
	return &model.ColumnInfo{ID: id, Name: pmodel.NewCIStr(fmt.Sprintf("c%d", offset)), Offset: offset,
		FieldType: *ft, State: model.StatePublic, Version: 2}
}

// tableInfo: the same shape as tidb-rs tests/common table_info.
func tableInfo(id int64) *model.TableInfo {
	cols := make([]*model.ColumnInfo, 0, nCols)
	for i := 0; i < nCols; i++ {
		tp := byte(mysql.TypeLonglong)
		if i%2 == 1 {
			tp = mysql.TypeVarchar
		}
		cols = append(cols, column(int64(i+1), i, tp))
	}
	idxs := make([]*model.IndexInfo, 0, nIdxs)
	for i := 0; i < nIdxs; i++ {
		var ics []*model.IndexColumn
		for k := 0; k < 1+i%2; k++ {
			off := (2*i + k) % nCols
			ics = append(ics, &model.IndexColumn{Name: cols[off].Name, Offset: off, Length: -1})
		}
		idxs = append(idxs, &model.IndexInfo{ID: int64(i + 1), Name: pmodel.NewCIStr(fmt.Sprintf("i%d", i)),
			Table: pmodel.NewCIStr(fmt.Sprintf("t%d", id)), Columns: ics, State: model.StatePublic})
	}
	return &model.TableInfo{ID: id, Name: pmodel.NewCIStr(fmt.Sprintf("t%d", id)), Charset: "utf8mb4",
		Collate: "utf8mb4_bin", Columns: cols, Indices: idxs, State: model.StatePublic, Version: 5,
		MaxColumnID: nCols, MaxIndexID: nIdxs}
}

func heap() int64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

func main() {
	base := heap()
	infos := make([]*model.TableInfo, nTables)
	for i := range infos {
		infos[i] = tableInfo(int64(100 + i))
	}
	model := heap() - base
	before := heap()
	tbls := make([]table.Table, nTables)
	for i, t := range infos {
		tbl, err := tables.TableFromMeta(autoid.NewAllocators(false), t)
		if err != nil {
			panic(err)
		}
		tbls[i] = tbl
	}
	objects := heap() - before
	n := float64(nTables * nCols)
	mib := func(b int64) float64 { return float64(b) / (1 << 20) }
	fmt.Printf("go memshape: %d tables x %d columns x %d indices\n", nTables, nCols, nIdxs)
	fmt.Printf("  model (TableInfo):  %8.1f MiB (%.0f B/column)\n", mib(model), float64(model)/n)
	fmt.Printf("  table objects:      %8.1f MiB (%.0f B/column incl. per-table parts)\n", mib(objects), float64(objects)/n)
	runtime.KeepAlive(infos)
	runtime.KeepAlive(tbls)
}
