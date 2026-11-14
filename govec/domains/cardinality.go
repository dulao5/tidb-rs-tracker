package domains

// Domain "cardinality": blocks D10.6 and D11.1, the VS1-29 part (tidb-rs
// specs/VS1-vertical-slice functions.tsv; specs/D10.6-cardinality, specs/D11.1-statistics) —
// the pseudo estimation (pkg/planner/cardinality pseudo.go, selectivity.go, row_count_column.go,
// row_count_index.go's pseudo path, ndv.go, row_size.go) and the read part of the statistics
// structures (pkg/statistics table.go, column.go, index.go, histogram.go, scalar.go).
//
// Encodings:
//   - a table is the JSON of a model.TableInfo (string);
//   - the query's columns are [[colInfoID, uniqueID]...]; an expression column built from them
//     has ID, UniqueID, Index (position) and the column's FieldType;
//   - a datum is {"k": kind, "v": value}: "null", "i" int64, "u" uint64, "f" float64 (Go float
//     string), "s" string (collation "c", default utf8mb4_bin), "b" bytes (hex), "dec" decimal
//     string, "dt" datetime string (fsp 0), "dur" duration string, "min", "max";
//   - a range is [low datums, high datums, lowExclude, highExclude, collations];
//   - an expression is ["col", position] | ["cor", position] | ["const", datum] |
//     ["f", name, args...] (expression.NewFunctionInternal, result type BIGINT).
//
// Records:
//   - "pseudo_table": args [table, allowTriggerLoading, allowFillHistMeta] -> PseudoTable and
//     the read methods of Table / HistColl / Column / Index / Histogram on it.
//   - "hist_coll": args [table, allowFill, cols] -> GenerateHistCollFromColumnInfo, ID2UniqueID.
//   - "row_count": args [table, allowFill, allowTrigger, cols, kind, id, ranges] ->
//     GetRowCountByColumnRanges ("col"), GetRowCountByIntColumnRanges ("int"),
//     GetRowCountByIndexRanges ("idx") on the query's HistColl.
//   - "col_est": args [table, allowFill, kind, colID, datum] -> ColumnEqualRowCount ("eq"),
//     ColumnGreaterRowCount ("gt") on the pseudo table.
//   - "selectivity": args [table, cols, exprs, defaultStrMatchSelectivity] -> Selectivity
//     (result and number of nodes). Conditions on a column / index of the HistColl build ranges
//     (ranger, D10.4): tidb-rs returns blocked-by:D10.4 for them until VS1-25.
//   - "row_size": args [pseudo, rc, statsCols, cols, enableChunkRPC, isEncodedKey, isForScan,
//     storeType, handleInCols, isUnique] -> GetAvgRowSize, GetIndexAvgRowSize,
//     GetTableAvgRowSize, GetAvgRowSizeDataInDiskByRows. statsCols [[uid, tp, isHandle,
//     totColSize, nullCount]] build Columns with empty histograms; cols [[uid, tp]].
//   - "avg_col": args [tp, isHandle, totColSize, nullCount, count, isKey] -> AvgColSize (key /
//     value), AvgColSizeChunkFormat, AvgColSizeDataInDiskByRows.
//   - "hist": args [tp, buckets, ndv, nullCount, statsVer, topn, values, rc, mc] -> a column
//     histogram built with AppendBucketWithNDV (per-bucket counts) and HistColl.CalcPreScalar,
//     then the estimation methods on each value and pair of values.
//   - "col_stats": args [isIndex, statsVer, ndv, nullCount, count, topn, status, pseudo,
//     canNotTriggerLoad, cid, rc] -> ColumnStatsIsInvalid / IndexStatsIsInvalid and the read
//     methods of Column / Index.
//   - "existence": args [steps] -> the ColAndIdxExistenceMap methods.
//   - "table_ops": args [table] -> Copy, ShallowCopy, DelCol, DelIdx, GetStatsInfo,
//     ReleaseAndPutToPool, GetStatsHealthy, IsOutdated, ... on a pseudo table.
//   - "ndv": args [schema uids, colNDVs, groupNDVs, cols] -> EstimateColsNDVWithMatchedLen and
//     EstimateColsDNVWithMatchedLenFromUniqueIDs.
//   - "mv_sel": args [rc, counts, isIntersection] -> CalcTotalSelectivityForMVIdxPath (table
//     paths; the MV index case needs D10.2).
//   - "topn": args [[hex, count]...] -> TopN.TotalCount, Num, Copy, MemoryUsage.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/cardinality"
	"github.com/pingcap/tidb/pkg/planner/planctx"
	"github.com/pingcap/tidb/pkg/planner/property"
	"github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/statistics"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/ranger"
)

const cardinalitySeed = 20261115

// CardinalityMinedTests: the scenarios of the Go tests copied as records.
func CardinalityMinedTests() []string {
	return []string{"TestPseudoTable"}
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "cardinality",
		Seed:     cardinalitySeed,
		Coverage: cardinalityCoverage,
		Blocks:   []string{"D10.6", "D11.1"},
		Exempt:   cardinalityExempt(),
		Gzip:     true,
		Generate: cardinalityGenerate,
		Exec:     cardinalityExec,
	})
}

var cardinalityCoverage = map[string]vec.Coverage{
	"pseudo_table": {Min: 200, GoFuncs: []string{"PseudoTable", "NewHistogram",
		"NewColAndIndexExistenceMap", "ColAndIdxExistenceMap.InsertCol", "ColAndIdxExistenceMap.InsertIndex",
		"Table.String", "pkg/statistics/column.go:Column.String", "pkg/statistics/index.go:Index.String",
		"Histogram.ToString", "HistColl.ColNum", "HistColl.IdxNum", "HistColl.GetCol", "HistColl.GetIdx",
		"Table.IsInitialized", "pkg/statistics/column.go:Column.IsStatsInitialized", "Table.IsOutdated",
		"HistColl.GetAnalyzeRowCount", "Table.GetStatsHealthy", "Table.IsAnalyzed", "Table.IsEligibleForAnalysis",
		"Table.MeetAutoAnalyzeMinCnt", "Table.MemoryUsage", "pkg/statistics/column.go:Column.MemoryUsage",
		"pkg/statistics/index.go:Index.MemoryUsage", "Histogram.MemoryUsage", "ColumnMemUsage.ItemID",
		"ColumnMemUsage.TotalMemoryUsage", "IndexMemUsage.ItemID", "IndexMemUsage.TotalMemoryUsage",
		"TableMemoryUsage.TotalTrackingMemUsage", "TableMemoryUsage.TotalIdxTrackingMemUsage",
		"TableMemoryUsage.TotalColTrackingMemUsage", "ColumnMemUsage.TrackingMemUsage", "IndexMemUsage.TrackingMemUsage",
		"Table.ColumnByName", "Table.IndexStartWithColumn", "EstimateColumnNDV", "getTotalRowCount",
		"PseudoAvgCountPerValue", "HistColl.ForEachIndexImmutable", "HistColl.ForEachColumnImmutable",
		"HistColl.StableOrderColSlice", "HistColl.StableOrderIdxSlice", "HistColl.GetColSlice", "HistColl.GetIdxSlice",
		"pkg/statistics/column.go:Column.ItemID", "pkg/statistics/index.go:Index.ItemID",
		"HistColl.GetScaledRealtimeAndModifyCnt"}},
	"hist_coll": {Min: 200, GoFuncs: []string{"HistColl.GenerateHistCollFromColumnInfo", "HistColl.ID2UniqueID"}},
	"row_count": {Min: 600, GoFuncs: []string{"GetRowCountByColumnRanges", "GetRowCountByIntColumnRanges",
		"GetRowCountByIndexRanges", "getPseudoRowCountByColumnRanges", "getPseudoRowCountBySignedIntRanges",
		"getPseudoRowCountByUnsignedIntRanges", "getPseudoRowCountByIndexRanges", "ColumnStatsIsInvalid",
		"IndexStatsIsInvalid", "canSkipIndexEstimation", "isFullRangeIncludingNulls"}},
	"col_est":     {Min: 100, GoFuncs: []string{"ColumnEqualRowCount", "ColumnGreaterRowCount"}},
	"selectivity": {Min: 500, GoFuncs: []string{"Selectivity", "pseudoSelectivity", "getConstantColumnID", "isColEqCorCol"}},
	"row_size": {Min: 300, GoFuncs: []string{"GetAvgRowSize", "GetIndexAvgRowSize", "GetTableAvgRowSize",
		"GetAvgRowSizeDataInDiskByRows", "NewHistCollWithColsAndIdxs"}},
	"avg_col": {Min: 200, GoFuncs: []string{"AvgColSize", "AvgColSizeChunkFormat", "AvgColSizeDataInDiskByRows"}},
	"hist": {Min: 300, GoFuncs: []string{"Histogram.AppendBucketWithNDV", "HistColl.CalcPreScalar",
		"Histogram.PreCalculateScalar", "Histogram.LowerToDatum", "Histogram.UpperToDatum", "Histogram.GetLower",
		"Histogram.GetUpper", "Histogram.Len", "Histogram.LessRowCount", "Histogram.LessRowCountWithBktIdx",
		"Histogram.LocateBucket", "Histogram.EqualRowCount", "Histogram.GreaterRowCount", "Histogram.BetweenRowCount",
		"Histogram.TotalRowCount", "Histogram.NotNullCount", "Histogram.BucketCount", "Histogram.BucketToString",
		"ValueToString", "Histogram.Copy", "Histogram.AbsRowCountDifference", "pkg/statistics/scalar.go:calcFraction",
		"Histogram.calcFraction", "convertDatumToScalar", "commonPrefixLength", "convertBytesToScalar",
		"IsLastBucketEndValueUnderrepresented", "pkg/statistics/column.go:Column.TotalRowCount",
		"pkg/statistics/column.go:Column.NotNullCount", "pkg/statistics/column.go:Column.GetIncreaseFactor"}},
	"col_stats": {Min: 300, GoFuncs: []string{"pkg/statistics/column.go:Column.Copy", "pkg/statistics/index.go:Index.Copy",
		"pkg/statistics/column.go:Column.GetEvictedStatus", "pkg/statistics/column.go:Column.GetHistogram",
		"pkg/statistics/column.go:Column.GetStatsVer", "pkg/statistics/column.go:Column.GetTopN",
		"pkg/statistics/column.go:Column.IsAllEvicted", "pkg/statistics/column.go:Column.IsCMSExist",
		"pkg/statistics/column.go:Column.StatsAvailable", "pkg/statistics/column.go:Column.IsAnalyzed",
		"pkg/statistics/index.go:Index.EvictAllStats", "pkg/statistics/index.go:Index.GetEvictedStatus",
		"pkg/statistics/index.go:Index.GetHistogram", "pkg/statistics/index.go:Index.GetIncreaseFactor",
		"pkg/statistics/index.go:Index.GetStatsVer", "pkg/statistics/index.go:Index.GetTopN",
		"pkg/statistics/index.go:Index.IsAllEvicted", "pkg/statistics/index.go:Index.IsCMSExist",
		"pkg/statistics/index.go:Index.IsEvicted", "pkg/statistics/index.go:Index.QueryBytes",
		"pkg/statistics/index.go:Index.TotalRowCount", "pkg/statistics/index.go:Index.isStatsInitialized",
		"pkg/statistics/index.go:Index.IsAnalyzed", "EmptyColumn", "NewStatsFullLoadStatus",
		"NewStatsAllEvictedStatus", "StatsLoadedStatus.Copy", "StatsLoadedStatus.IsStatsInitialized",
		"StatsLoadedStatus.IsLoadNeeded", "StatsLoadedStatus.IsEssentialStatsLoaded", "StatsLoadedStatus.IsAllEvicted",
		"StatsLoadedStatus.IsFullLoad", "StatsLoadedStatus.StatusToString"}},
	"existence": {Min: 100, GoFuncs: []string{"NewColAndIndexExistenceMapWithoutSize", "ColAndIdxExistenceMap.Has",
		"ColAndIdxExistenceMap.HasAnalyzed", "ColAndIdxExistenceMap.DeleteColNotFound",
		"ColAndIdxExistenceMap.DeleteIdxNotFound", "ColAndIdxExistenceMap.IsEmpty", "ColAndIdxExistenceMap.ColNum",
		"ColAndIdxExistenceMap.Clone", "ColAndIdxExistenceMapIsEqual"}},
	"table_ops": {Min: 150, GoFuncs: []string{"Table.Copy", "Table.ShallowCopy", "Table.DelCol", "Table.DelIdx",
		"Table.GetStatsInfo", "Table.ReleaseAndPutToPool", "HistColl.SetCol", "HistColl.SetIdx", "NewHistColl",
		"NewExtendedStatsColl"}},
	"ndv":    {Min: 200, GoFuncs: []string{"EstimateColsNDVWithMatchedLen", "EstimateColsDNVWithMatchedLenFromUniqueIDs"}},
	"mv_sel": {Min: 100, GoFuncs: []string{"CalcTotalSelectivityForMVIdxPath"}},
	"topn":   {Min: 50, GoFuncs: []string{"TopN.TotalCount", "TopN.Num", "TopN.Copy", "TopN.MemoryUsage", "TopN.calculateCount", "TopN.onceCalculateCount", "TopN.calculateCountInternal"}},
}

// ---------------------------------------------------------------- inputs

func cdDatum(raw json.RawMessage) types.Datum {
	var d struct {
		K string          `json:"k"`
		V json.RawMessage `json:"v"`
		C string          `json:"c"`
	}
	mustUnmarshal(raw, &d)
	switch d.K {
	case "null":
		return types.Datum{}
	case "i":
		var v int64
		mustUnmarshal(d.V, &v)
		return types.NewIntDatum(v)
	case "u":
		var v uint64
		mustUnmarshal(d.V, &v)
		return types.NewUintDatum(v)
	case "f":
		var v string
		mustUnmarshal(d.V, &v)
		return types.NewFloat64Datum(parseGoFloat(v))
	case "s":
		var v string
		mustUnmarshal(d.V, &v)
		c := d.C
		if c == "" {
			c = "utf8mb4_bin"
		}
		return types.NewCollationStringDatum(v, c)
	case "b":
		var v string
		mustUnmarshal(d.V, &v)
		b, err := hex.DecodeString(v)
		if err != nil {
			panic(err)
		}
		return types.NewBytesDatum(b)
	case "dec":
		var v string
		mustUnmarshal(d.V, &v)
		dec := new(types.MyDecimal)
		if err := dec.FromString([]byte(v)); err != nil {
			panic(err)
		}
		return types.NewDecimalDatum(dec)
	case "dt":
		var v string
		mustUnmarshal(d.V, &v)
		t, err := types.ParseTime(types.DefaultStmtNoWarningContext, v, mysql.TypeDatetime, 0)
		if err != nil {
			panic(err)
		}
		return types.NewTimeDatum(t)
	case "dur":
		var v string
		mustUnmarshal(d.V, &v)
		dur, _, err := types.ParseDuration(types.DefaultStmtNoWarningContext, v, 0)
		if err != nil {
			panic(err)
		}
		return types.NewDurationDatum(dur)
	case "min":
		return types.MinNotNullDatum()
	case "max":
		return types.MaxValueDatum()
	}
	panic("bad datum " + d.K)
}

func cdDatums(raw json.RawMessage) []types.Datum {
	var ds []json.RawMessage
	mustUnmarshal(raw, &ds)
	out := make([]types.Datum, 0, len(ds))
	for _, d := range ds {
		out = append(out, cdDatum(d))
	}
	return out
}

func cdRanges(raw json.RawMessage) []*ranger.Range {
	var rs [][]json.RawMessage
	mustUnmarshal(raw, &rs)
	out := make([]*ranger.Range, 0, len(rs))
	for _, r := range rs {
		var le, he bool
		var colls []string
		mustUnmarshal(r[2], &le)
		mustUnmarshal(r[3], &he)
		mustUnmarshal(r[4], &colls)
		rg := &ranger.Range{LowVal: cdDatums(r[0]), HighVal: cdDatums(r[1]), LowExclude: le, HighExclude: he}
		for _, c := range colls {
			rg.Collators = append(rg.Collators, collate.GetCollator(c))
		}
		out = append(out, rg)
	}
	return out
}

func cdTable(raw json.RawMessage) *model.TableInfo {
	var s string
	mustUnmarshal(raw, &s)
	t := &model.TableInfo{}
	mustUnmarshal([]byte(s), t)
	return t
}

func cdBool(raw json.RawMessage) bool {
	var b bool
	mustUnmarshal(raw, &b)
	return b
}

func cdInt(raw json.RawMessage) int64 {
	var v int64
	mustUnmarshal(raw, &v)
	return v
}

func cdStr(raw json.RawMessage) string {
	var v string
	mustUnmarshal(raw, &v)
	return v
}

// cdCols: the query's columns [[colInfoID, uniqueID]...] of the table.
func cdCols(t *model.TableInfo, raw json.RawMessage) []*expression.Column {
	var ids [][2]int64
	mustUnmarshal(raw, &ids)
	out := make([]*expression.Column, 0, len(ids))
	for i, p := range ids {
		col := &expression.Column{ID: p[0], UniqueID: p[1], Index: i, RetType: types.NewFieldType(mysql.TypeLonglong)}
		for _, c := range t.Columns {
			if c.ID == p[0] {
				col.RetType = c.FieldType.Clone()
				col.IsHidden = c.Hidden
			}
		}
		out = append(out, col)
	}
	return out
}

func cdFieldType(tp string) *types.FieldType {
	switch tp {
	case "int":
		return types.NewFieldType(mysql.TypeLonglong)
	case "uint":
		ft := types.NewFieldType(mysql.TypeLonglong)
		ft.AddFlag(mysql.UnsignedFlag)
		return ft
	case "double":
		return types.NewFieldType(mysql.TypeDouble)
	case "float":
		return types.NewFieldType(mysql.TypeFloat)
	case "varchar":
		ft := types.NewFieldType(mysql.TypeVarchar)
		ft.SetCharset("utf8mb4")
		ft.SetCollate("utf8mb4_bin")
		ft.SetFlen(32)
		return ft
	case "blob":
		return types.NewFieldType(mysql.TypeBlob)
	case "decimal":
		ft := types.NewFieldType(mysql.TypeNewDecimal)
		ft.SetFlen(10)
		ft.SetDecimal(2)
		return ft
	case "datetime":
		return types.NewFieldType(mysql.TypeDatetime)
	case "duration":
		return types.NewFieldType(mysql.TypeDuration)
	case "tiny":
		return types.NewFieldType(mysql.TypeTiny)
	case "enum":
		ft := types.NewFieldType(mysql.TypeEnum)
		ft.SetElems([]string{"a", "b"})
		return ft
	case "json":
		return types.NewFieldType(mysql.TypeJSON)
	}
	panic("bad type " + tp)
}

func cdExpr(ctx *mock.Context, cols []*expression.Column, raw json.RawMessage) expression.Expression {
	var a []json.RawMessage
	mustUnmarshal(raw, &a)
	switch cdStr(a[0]) {
	case "col":
		return cols[cdInt(a[1])]
	case "cor":
		return &expression.CorrelatedColumn{Column: *cols[cdInt(a[1])], Data: new(types.Datum)}
	case "const":
		d := cdDatum(a[1])
		ft := types.NewFieldType(mysql.TypeLonglong)
		if d.Kind() == types.KindString {
			ft = types.NewFieldType(mysql.TypeVarchar)
			ft.SetCharset("utf8mb4")
			ft.SetCollate(d.Collation())
		} else if d.IsNull() {
			ft = types.NewFieldType(mysql.TypeNull)
		}
		return &expression.Constant{Value: d, RetType: ft}
	case "f":
		args := make([]expression.Expression, 0, len(a)-2)
		for _, x := range a[2:] {
			arg := cdExpr(ctx, cols, x)
			if arg == nil {
				return nil
			}
			args = append(args, arg)
		}
		f := expression.NewFunctionInternal(ctx.GetExprCtx(), cdStr(a[1]), types.NewFieldType(mysql.TypeLonglong), args...)
		if f == nil {
			return nil
		}
		return f
	}
	panic("bad expr")
}

// ---------------------------------------------------------------- outputs

func cdF(f float64) string { return fstr(f) }

func cdErr(err error) any {
	if err == nil {
		return nil
	}
	return map[string]any{"err": err.Error()}
}

func cdHistOut(hg *statistics.Histogram) any {
	return []any{hg.ID, hg.NDV, hg.NullCount, int(hg.Tp.GetType()), uint64(hg.Tp.GetFlag()), hg.Tp.GetCollate(),
		hg.Len(), cdF(hg.TotalRowCount()), cdF(hg.NotNullCount()), hg.TotColSize}
}

func cdColOut(key int64, c *statistics.Column) any {
	var infoID any
	if c.Info != nil {
		infoID = c.Info.ID
	}
	return []any{key, cdHistOut(&c.Histogram), c.IsHandle, c.PhysicalID, infoID, c.IsStatsInitialized(),
		c.IsFullLoad(), c.StatsVer}
}

func cdIdxOut(key int64, idx *statistics.Index) any {
	var infoID any
	if idx.Info != nil {
		infoID = idx.Info.ID
	}
	return []any{key, cdHistOut(&idx.Histogram), idx.PhysicalID, infoID, idx.IsFullLoad(), idx.StatsVer}
}

// cdCollOut: the HistColl in key order.
func cdCollOut(coll *statistics.HistColl) any {
	type kc struct {
		k int64
		c *statistics.Column
	}
	type ki struct {
		k int64
		i *statistics.Index
	}
	var cs []kc
	coll.ForEachColumnImmutable(func(k int64, c *statistics.Column) bool { cs = append(cs, kc{k, c}); return false })
	sort.Slice(cs, func(i, j int) bool { return cs[i].k < cs[j].k })
	var is []ki
	coll.ForEachIndexImmutable(func(k int64, i *statistics.Index) bool { is = append(is, ki{k, i}); return false })
	sort.Slice(is, func(i, j int) bool { return is[i].k < is[j].k })
	cols := []any{}
	for _, c := range cs {
		cols = append(cols, cdColOut(c.k, c.c))
	}
	idxs := []any{}
	for _, i := range is {
		idxs = append(idxs, cdIdxOut(i.k, i.i))
	}
	mapOut := func(m map[int64][]int64) any {
		ks := make([]int64, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
		out := []any{}
		for _, k := range ks {
			v := m[k]
			if v == nil {
				v = []int64{}
			}
			out = append(out, []any{k, v})
		}
		return out
	}
	u2c := map[int64][]int64{}
	for k, v := range coll.UniqueID2colInfoID {
		u2c[k] = []int64{v}
	}
	return map[string]any{
		"pid": coll.PhysicalID, "hpid": coll.HavePhysicalID, "rc": coll.RealtimeCount, "mc": coll.ModifyCount,
		"pseudo": coll.Pseudo, "cntl": coll.CanNotTriggerLoad, "ver": coll.StatsVer, "colnum": coll.ColNum(),
		"idxnum": coll.IdxNum(), "cols": cols, "idxs": idxs, "i2c": mapOut(coll.Idx2ColUniqueIDs),
		"c2i": mapOut(coll.ColUniqueID2IdxIDs), "u2c": mapOut(u2c), "mv": len(coll.MVIdx2Columns),
	}
}

func cdSctx() *mock.Context {
	collate.SetNewCollationEnabledForTest(true)
	// planner/core sets this function variable (the table name of the used-stats record of the
	// slow log) to a lookup through the domain, which the mock context does not have; the
	// record does not affect an estimate (tidb-rs does not record used stats yet, D11.2).
	cardinality.GetTblInfoForUsedStatsByPhysicalID = func(planctx.PlanContext, int64) (string, *model.TableInfo) {
		return "", nil
	}
	return mock.NewContext()
}

// ---------------------------------------------------------------- exec

func cardinalityExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "pseudo_table":
		return cdPseudoTable(args), nil
	case "hist_coll":
		t := cdTable(args[0])
		tbl := statistics.PseudoTable(t, false, cdBool(args[1]))
		cols := cdCols(t, args[2])
		gen := tbl.HistColl.GenerateHistCollFromColumnInfo(t, cols)
		id2 := tbl.HistColl.ID2UniqueID(cols)
		return []any{cdCollOut(gen), cdCollOut(id2)}, nil
	case "row_count":
		t := cdTable(args[0])
		tbl := statistics.PseudoTable(t, cdBool(args[2]), cdBool(args[1]))
		cols := cdCols(t, args[3])
		coll := tbl.HistColl.GenerateHistCollFromColumnInfo(t, cols)
		sctx := cdSctx()
		id := cdInt(args[5])
		rs := cdRanges(args[6])
		var r float64
		var err error
		switch cdStr(args[4]) {
		case "col":
			r, err = cardinality.GetRowCountByColumnRanges(sctx, coll, id, rs)
		case "int":
			r, err = cardinality.GetRowCountByIntColumnRanges(sctx, coll, id, rs)
		case "idx":
			r, err = cardinality.GetRowCountByIndexRanges(sctx, coll, id, rs, nil)
		}
		if err != nil {
			return nil, err
		}
		return cdF(r), nil
	case "col_est":
		t := cdTable(args[0])
		tbl := statistics.PseudoTable(t, false, cdBool(args[1]))
		sctx := cdSctx()
		d := cdDatum(args[4])
		id := cdInt(args[3])
		if cdStr(args[2]) == "gt" {
			return cdF(cardinality.ColumnGreaterRowCount(sctx, tbl, d, id)), nil
		}
		r, err := cardinality.ColumnEqualRowCount(sctx, tbl, d, id)
		if err != nil {
			return nil, err
		}
		return cdF(r), nil
	case "selectivity":
		t := cdTable(args[0])
		tbl := statistics.PseudoTable(t, false, true)
		cols := cdCols(t, args[1])
		coll := tbl.HistColl.GenerateHistCollFromColumnInfo(t, cols)
		sctx := cdSctx()
		sctx.GetSessionVars().DefaultStrMatchSelectivity = parseGoFloat(cdStr(args[3]))
		var es []json.RawMessage
		mustUnmarshal(args[2], &es)
		exprs := make([]expression.Expression, 0, len(es))
		for _, e := range es {
			x := cdExpr(sctx, cols, e)
			if x == nil || (func() bool { sf, ok := x.(*expression.ScalarFunction); return ok && sf == nil })() {
				// NewFunctionInternal returns nil when the function cannot be built.
				return "nilexpr", nil
			}
			exprs = append(exprs, x)
		}
		sel, nodes, err := cardinality.Selectivity(sctx, coll, exprs, nil)
		if err != nil {
			return nil, err
		}
		return []any{cdF(sel), len(nodes)}, nil
	case "row_size":
		return cdRowSize(args), nil
	case "avg_col":
		c := &statistics.Column{IsHandle: cdBool(args[1])}
		c.Histogram = *statistics.NewHistogram(1, 0, cdInt(args[3]), 0, cdFieldType(cdStr(args[0])), 0, cdInt(args[2]))
		n := cdInt(args[4])
		return []any{cdF(cardinality.AvgColSize(c, n, true)), cdF(cardinality.AvgColSize(c, n, false)),
			cdF(cardinality.AvgColSizeChunkFormat(c, n)), cdF(cardinality.AvgColSizeDataInDiskByRows(c, n))}, nil
	case "hist":
		return cdHist(args), nil
	case "col_stats":
		return cdColStats(args), nil
	case "existence":
		return cdExistence(args), nil
	case "table_ops":
		return cdTableOps(args), nil
	case "ndv":
		return cdNDV(args), nil
	case "mv_sel":
		coll := statistics.NewHistColl(1, true, cdInt(args[0]), 0, 0, 0)
		var counts []string
		mustUnmarshal(args[1], &counts)
		paths := make([]*util.AccessPath, 0, len(counts))
		for _, c := range counts {
			paths = append(paths, &util.AccessPath{IsIntHandlePath: true, CountAfterAccess: parseGoFloat(c)})
		}
		return cdF(cardinality.CalcTotalSelectivityForMVIdxPath(coll, paths, cdBool(args[2]))), nil
	case "topn":
		t := cdTopN(args[0])
		c := t.Copy()
		return []any{t.TotalCount(), t.Num(), c.TotalCount(), c.Num(), t.MemoryUsage(), c.MemoryUsage(), t.Equal(c)}, nil
	}
	return nil, fmt.Errorf("unknown op %s", op)
}

func cdTopN(raw json.RawMessage) *statistics.TopN {
	var items [][2]json.RawMessage
	mustUnmarshal(raw, &items)
	if items == nil {
		return nil
	}
	t := &statistics.TopN{}
	for _, it := range items {
		b, err := hex.DecodeString(cdStr(it[0]))
		if err != nil {
			panic(err)
		}
		var c uint64
		mustUnmarshal(it[1], &c)
		t.TopN = append(t.TopN, statistics.TopNMeta{Encoded: b, Count: c})
	}
	return t
}

func cdPseudoTable(args []json.RawMessage) any {
	t := cdTable(args[0])
	tbl := statistics.PseudoTable(t, cdBool(args[1]), cdBool(args[2]))
	h, ok := tbl.GetStatsHealthy()
	mem := tbl.MemoryUsage()
	ndv := []any{}
	byName := []any{}
	startWith := []any{}
	for _, c := range t.Columns {
		ndv = append(ndv, cdF(cardinality.EstimateColumnNDV(tbl, c.ID)))
		col := tbl.ColumnByName(c.Name.L)
		if col == nil {
			byName = append(byName, nil)
		} else {
			byName = append(byName, col.ID)
		}
		// IndexStartWithColumn returns the first match in map order: only unambiguous names.
		n := 0
		tbl.ForEachIndexImmutable(func(_ int64, idx *statistics.Index) bool {
			if idx.Info.Columns[0].Name.L == c.Name.L {
				n++
			}
			return false
		})
		if n <= 1 {
			idx := tbl.IndexStartWithColumn(c.Name.L)
			if idx == nil {
				startWith = append(startWith, nil)
			} else {
				startWith = append(startWith, idx.ID)
			}
		} else {
			startWith = append(startWith, "many")
		}
	}
	ndv = append(ndv, cdF(cardinality.EstimateColumnNDV(tbl, 999)))
	colIDs := []int64{}
	for _, c := range tbl.StableOrderColSlice() {
		colIDs = append(colIDs, c.ID)
	}
	idxIDs := []int64{}
	for _, i := range tbl.StableOrderIdxSlice() {
		idxIDs = append(idxIDs, i.ID)
	}
	scaled := []any{}
	for _, id := range idxIDs {
		rc, mc := tbl.GetScaledRealtimeAndModifyCnt(tbl.GetIdx(id))
		scaled = append(scaled, []int64{rc, mc})
	}
	itemIDs := []any{}
	for _, id := range colIDs {
		itemIDs = append(itemIDs, tbl.GetCol(id).ItemID())
	}
	for _, id := range idxIDs {
		itemIDs = append(itemIDs, tbl.GetIdx(id).ItemID())
	}
	exist := tbl.ColAndIdxExistenceMap
	ex := []any{}
	for _, c := range t.Columns {
		ex = append(ex, []bool{exist.Has(c.ID, false), exist.HasAnalyzed(c.ID, false)})
	}
	for _, i := range t.Indices {
		ex = append(ex, []bool{exist.Has(i.ID, true), exist.HasAnalyzed(i.ID, true)})
	}
	return map[string]any{
		"coll": cdCollOut(&tbl.HistColl), "str": tbl.String(), "healthy": []any{h, ok},
		"init": tbl.IsInitialized(), "outdated": tbl.IsOutdated(), "eligible": tbl.IsEligibleForAnalysis(),
		"meet": tbl.MeetAutoAnalyzeMinCnt(), "analyzed": tbl.IsAnalyzed(), "arc": cdF(tbl.GetAnalyzeRowCount()),
		"mem": []int64{mem.TotalMemUsage, mem.TotalTrackingMemUsage(), mem.TotalColTrackingMemUsage(),
			mem.TotalIdxTrackingMemUsage(), int64(len(mem.ColumnsMemUsage)), int64(len(mem.IndicesMemUsage))},
		"ndv": ndv, "byname": byName, "startwith": startWith, "pavg": cdF(cardinality.PseudoAvgCountPerValue(tbl)),
		"colids": colIDs, "idxids": idxIDs, "slices": []int{len(tbl.GetColSlice()), len(tbl.GetIdxSlice())},
		"scaled": scaled, "items": itemIDs, "exist": ex, "exist_empty": exist.IsEmpty(), "exist_cols": exist.ColNum(),
	}
}

func cdRowSize(args []json.RawMessage) any {
	pseudo := cdBool(args[0])
	rc := cdInt(args[1])
	var statsCols [][5]json.RawMessage
	mustUnmarshal(args[2], &statsCols)
	cols := map[int64]*statistics.Column{}
	for _, sc := range statsCols {
		c := &statistics.Column{IsHandle: cdBool(sc[2])}
		c.Histogram = *statistics.NewHistogram(cdInt(sc[0]), 0, cdInt(sc[4]), 0, cdFieldType(cdStr(sc[1])), 0, cdInt(sc[3]))
		cols[cdInt(sc[0])] = c
	}
	coll := statistics.NewHistCollWithColsAndIdxs(1, true, rc, 0, cols, map[int64]*statistics.Index{})
	coll.Pseudo = pseudo
	var qcols [][2]json.RawMessage
	mustUnmarshal(args[3], &qcols)
	exprCols := make([]*expression.Column, 0, len(qcols))
	for _, q := range qcols {
		exprCols = append(exprCols, &expression.Column{UniqueID: cdInt(q[0]), RetType: cdFieldType(cdStr(q[1]))})
	}
	sctx := cdSctx()
	sctx.GetSessionVars().EnableChunkRPC = cdBool(args[4])
	isKey, isScan := cdBool(args[5]), cdBool(args[6])
	st := kv.StoreType(cdInt(args[7]))
	return []any{cdF(cardinality.GetAvgRowSize(sctx, coll, exprCols, isKey, isScan)),
		cdF(cardinality.GetIndexAvgRowSize(sctx, coll, exprCols, cdBool(args[9]))),
		cdF(cardinality.GetTableAvgRowSize(sctx, coll, exprCols, st, cdBool(args[8]))),
		cdF(cardinality.GetAvgRowSizeDataInDiskByRows(coll, exprCols))}
}

func cdHist(args []json.RawMessage) any {
	tp := cdFieldType(cdStr(args[0]))
	var buckets [][5]json.RawMessage
	mustUnmarshal(args[1], &buckets)
	ndv, nullCount, statsVer := cdInt(args[2]), cdInt(args[3]), cdInt(args[4])
	hg := statistics.NewHistogram(1, ndv, nullCount, 0, tp, len(buckets), 0)
	for _, b := range buckets {
		lo, hi := cdDatum(b[0]), cdDatum(b[1])
		hg.AppendBucketWithNDV(&lo, &hi, cdInt(b[2]), cdInt(b[3]), cdInt(b[4]))
	}
	c := &statistics.Column{Histogram: *hg, StatsVer: statsVer, TopN: cdTopN(args[5]),
		StatsLoadedStatus: statistics.NewStatsFullLoadStatus()}
	coll := statistics.NewHistCollWithColsAndIdxs(1, true, 0, 0, map[int64]*statistics.Column{1: c}, map[int64]*statistics.Index{})
	coll.CalcPreScalar()
	hg = &c.Histogram
	vals := cdDatums(args[6])
	rc, mc := cdInt(args[7]), cdInt(args[8])
	per := []any{}
	for _, v := range vals {
		less := hg.LessRowCount(nil, v)
		eqc, eqm := hg.EqualRowCount(nil, v, false)
		eqcN, eqmN := hg.EqualRowCount(nil, v, true)
		ex, bi, in, m := hg.LocateBucket(nil, v)
		under := cardinality.IsLastBucketEndValueUnderrepresented(nil, hg, v, eqcN, float64(hg.NDV), rc, mc)
		per = append(per, []any{cdF(less), cdF(eqc), eqm, cdF(eqcN), eqmN, cdF(hg.GreaterRowCount(v)),
			[]any{ex, bi, in, m}, under})
	}
	between := []any{}
	for i := 0; i+1 < len(vals); i++ {
		between = append(between, cdF(hg.BetweenRowCount(nil, vals[i], vals[i+1])))
	}
	bounds := []any{}
	for i := 0; i < hg.Len(); i++ {
		lo, _ := hg.GetLower(i).ToString()
		hi, _ := hg.GetUpper(i).ToString()
		bounds = append(bounds, []any{lo, hi, hg.BucketCount(i)})
	}
	cp := hg.Copy()
	return map[string]any{
		"per": per, "between": between, "bounds": bounds, "len": hg.Len(), "tot": cdF(hg.TotalRowCount()),
		"nn": cdF(hg.NotNullCount()), "str": hg.ToString(0), "copy": cp.ToString(0) == hg.ToString(0),
		"ctot": cdF(c.TotalRowCount()), "cnn": cdF(c.NotNullCount()), "inc": cdF(c.GetIncreaseFactor(rc)),
		"abs": cdF(hg.AbsRowCountDifference(rc)),
	}
}

func cdColStats(args []json.RawMessage) any {
	isIndex := cdBool(args[0])
	statsVer, ndv, nullCount, count := cdInt(args[1]), cdInt(args[2]), cdInt(args[3]), cdInt(args[4])
	topn := cdTopN(args[5])
	status := cdStr(args[6])
	coll := statistics.NewHistColl(7, true, cdInt(args[10]), 0, 0, 0)
	coll.Pseudo = cdBool(args[7])
	coll.CanNotTriggerLoad = cdBool(args[8])
	cid := cdInt(args[9])
	sctx := cdSctx()
	tp := types.NewFieldType(mysql.TypeLonglong)
	lo, hi := types.NewIntDatum(1), types.NewIntDatum(100)
	if isIndex {
		// Index histograms hold encoded keys.
		tp = types.NewFieldType(mysql.TypeBlob)
		l, err1 := codec.EncodeKey(nil, nil, lo)
		h, err2 := codec.EncodeKey(nil, nil, hi)
		if err1 != nil || err2 != nil {
			panic("encode")
		}
		lo, hi = types.NewBytesDatum(l), types.NewBytesDatum(h)
	}
	hg := statistics.NewHistogram(cid, ndv, nullCount, 0, tp, 1, 0)
	if count > 0 {
		hg.AppendBucketWithNDV(&lo, &hi, count, 1, 0)
	}
	var st statistics.StatsLoadedStatus
	switch status {
	case "full":
		st = statistics.NewStatsFullLoadStatus()
	case "evicted":
		st = statistics.NewStatsAllEvictedStatus()
	}
	stOut := []any{st.IsStatsInitialized(), st.IsLoadNeeded(), st.IsEssentialStatsLoaded(), st.IsAllEvicted(),
		st.IsFullLoad(), st.StatusToString()}
	if isIndex {
		info := &model.IndexInfo{ID: cid, Name: pmodel.NewCIStr("i"), Columns: []*model.IndexColumn{{Name: pmodel.NewCIStr("a"), Length: types.UnspecifiedLength}}}
		idx := &statistics.Index{Histogram: *hg, StatsVer: statsVer, TopN: topn, Info: info, StatsLoadedStatus: st, PhysicalID: 7}
		var nilIdx *statistics.Index
		out := []any{statistics.IndexStatsIsInvalid(sctx, idx, coll, cid), statistics.IndexStatsIsInvalid(sctx, nil, coll, cid),
			cdF(idx.TotalRowCount()), cdF(idx.GetIncreaseFactor(1000)), idx.IsAllEvicted(), nilIdx.IsAllEvicted(),
			idx.GetEvictedStatus(), idx.IsCMSExist(), idx.IsEvicted(), idx.IsAnalyzed(), idx.ItemID(), idx.String(),
			idx.Copy().String(), idx.GetStatsVer(), idx.GetHistogram().ID, idx.GetTopN().Num(), stOut,
			idx.MemoryUsage().TotalMemoryUsage(), idx.MemoryUsage().TrackingMemUsage()}
		if topn == nil {
			out = append(out, idx.QueryBytes(nil, []byte{1, 2}))
		} else {
			out = append(out, nil)
		}
		idx.EvictAllStats()
		out = append(out, idx.IsAllEvicted(), idx.GetEvictedStatus(), idx.Len())
		return out
	}
	info := &model.ColumnInfo{ID: cid, Name: pmodel.NewCIStr("a"), FieldType: *types.NewFieldType(mysql.TypeLonglong)}
	c := &statistics.Column{Histogram: *hg, StatsVer: statsVer, TopN: topn, Info: info, StatsLoadedStatus: st, PhysicalID: 7}
	var nilCol *statistics.Column
	empty := statistics.EmptyColumn(7, true, info)
	return []any{statistics.ColumnStatsIsInvalid(c, sctx, coll, cid), statistics.ColumnStatsIsInvalid(nil, sctx, coll, cid),
		cdF(c.TotalRowCount()), cdF(c.NotNullCount()), cdF(c.GetIncreaseFactor(1000)), c.IsAllEvicted(),
		nilCol.IsAllEvicted(), c.GetEvictedStatus(), c.IsCMSExist(), c.IsAnalyzed(), c.StatsAvailable(), c.ItemID(),
		c.String(), c.Copy().String(), c.GetStatsVer(), c.GetHistogram().ID, c.GetTopN().Num(), stOut,
		c.MemoryUsage().TotalMemoryUsage(), c.MemoryUsage().TrackingMemUsage(), empty.IsHandle, empty.String()}
}

func cdExistence(args []json.RawMessage) any {
	var steps [][]json.RawMessage
	mustUnmarshal(args[0], &steps)
	m := statistics.NewColAndIndexExistenceMapWithoutSize()
	var clone *statistics.ColAndIdxExistenceMap
	out := []any{}
	for _, s := range steps {
		id := cdInt(s[1])
		switch cdStr(s[0]) {
		case "insc":
			m.InsertCol(id, cdBool(s[2]))
		case "insi":
			m.InsertIndex(id, cdBool(s[2]))
		case "delc":
			m.DeleteColNotFound(id)
		case "deli":
			m.DeleteIdxNotFound(id)
		case "clone":
			clone = m.Clone()
		}
		eq := clone != nil && statistics.ColAndIdxExistenceMapIsEqual(m, clone)
		out = append(out, []any{m.Has(id, false), m.Has(id, true), m.HasAnalyzed(id, false), m.HasAnalyzed(id, true),
			m.IsEmpty(), m.ColNum(), eq})
	}
	return out
}

func cdTableOps(args []json.RawMessage) any {
	t := cdTable(args[0])
	tbl := statistics.PseudoTable(t, false, true)
	out := map[string]any{}
	cp := tbl.Copy()
	sh := tbl.ShallowCopy()
	out["copy"] = []any{cp.String() == tbl.String(), cdCollOut(&cp.HistColl), sh.String() == tbl.String(),
		cdCollOut(&sh.HistColl), statistics.ColAndIdxExistenceMapIsEqual(cp.ColAndIdxExistenceMap, tbl.ColAndIdxExistenceMap)}
	infos := []any{}
	for _, c := range t.Columns {
		for _, need := range []bool{false, true} {
			hg, cms, topn, fms, ok := tbl.GetStatsInfo(c.ID, false, need)
			var hid any
			if hg != nil {
				hid = hg.ID
			}
			infos = append(infos, []any{ok, hid, cms != nil, topn != nil, fms != nil})
		}
	}
	for _, i := range t.Indices {
		hg, _, _, _, ok := tbl.GetStatsInfo(i.ID, true, true)
		var hid any
		if hg != nil {
			hid = hg.ID
		}
		infos = append(infos, []any{ok, hid})
	}
	out["infos"] = infos
	// writes on a shallow copy (the original keeps its maps in tidb-rs; Go's callers copy first)
	if len(t.Columns) > 0 {
		cp.DelCol(t.Columns[0].ID)
	}
	if len(t.Indices) > 0 {
		cp.DelIdx(t.Indices[0].ID)
	}
	cp.SetCol(500, statistics.EmptyColumn(t.ID, false, &model.ColumnInfo{ID: 500, Name: pmodel.NewCIStr("z"), FieldType: *types.NewFieldType(mysql.TypeLonglong)}))
	cp.SetIdx(600, &statistics.Index{Histogram: *statistics.NewHistogram(600, 0, 0, 0, types.NewFieldType(mysql.TypeBlob), 0, 0), Info: &model.IndexInfo{ID: 600}})
	out["del"] = []any{cdCollOut(&cp.HistColl), cp.ColAndIdxExistenceMap.ColNum(), cp.ColAndIdxExistenceMap.IsEmpty()}
	// stats healthy on an analyzed copy
	var healthy []any
	for _, mc := range []int64{0, 1, 3000, 9999, 10000, 20000} {
		h := *tbl
		h.Pseudo = false
		h.LastAnalyzeVersion = 1
		h.ModifyCount = mc
		hh, ok := h.GetStatsHealthy()
		healthy = append(healthy, []any{hh, ok, h.IsOutdated(), h.IsEligibleForAnalysis(), h.MeetAutoAnalyzeMinCnt()})
	}
	out["healthy"] = healthy
	ext := statistics.NewExtendedStatsColl()
	ext.Stats["s1"] = &statistics.ExtendedStatsItem{ColIDs: []int64{1, 2}, ScalarVals: 0.5}
	ext.LastUpdateVersion = 7
	withExt := tbl.ShallowCopy()
	withExt.ExtendedStats = ext
	ce := withExt.Copy()
	out["ext"] = []any{len(ce.ExtendedStats.Stats), ce.ExtendedStats.LastUpdateVersion, ce.ExtendedStats.Stats["s1"] == ext.Stats["s1"]}
	hc := statistics.NewHistColl(9, true, 30, 4, 2, 1)
	out["new"] = cdCollOut(hc)
	tbl.ReleaseAndPutToPool()
	out["release"] = []int{tbl.ColNum(), tbl.IdxNum()}
	return out
}

func cdNDV(args []json.RawMessage) any {
	var uids []int64
	mustUnmarshal(args[0], &uids)
	schemaCols := make([]*expression.Column, 0, len(uids))
	for _, u := range uids {
		schemaCols = append(schemaCols, &expression.Column{UniqueID: u, RetType: types.NewFieldType(mysql.TypeLonglong)})
	}
	schema := expression.NewSchema(schemaCols...)
	profile := &property.StatsInfo{ColNDVs: map[int64]float64{}}
	var ndvs [][2]json.RawMessage
	mustUnmarshal(args[1], &ndvs)
	for _, p := range ndvs {
		profile.ColNDVs[cdInt(p[0])] = parseGoFloat(cdStr(p[1]))
	}
	var groups [][2]json.RawMessage
	mustUnmarshal(args[2], &groups)
	for _, g := range groups {
		var cols []int64
		mustUnmarshal(g[0], &cols)
		profile.GroupNDVs = append(profile.GroupNDVs, property.GroupNDV{Cols: cols, NDV: parseGoFloat(cdStr(g[1]))})
	}
	var q []int64
	mustUnmarshal(args[3], &q)
	qcols := make([]*expression.Column, 0, len(q))
	for _, u := range q {
		qcols = append(qcols, &expression.Column{UniqueID: u, RetType: types.NewFieldType(mysql.TypeLonglong)})
	}
	n1, l1 := cardinality.EstimateColsNDVWithMatchedLen(qcols, schema, profile)
	n2, l2 := cardinality.EstimateColsDNVWithMatchedLenFromUniqueIDs(q, schema, profile)
	return []any{cdF(n1), l1, cdF(n2), l2}
}

// ---------------------------------------------------------------- generate

func cdD(k string, v any) map[string]any { return map[string]any{"k": k, "v": v} }

func cdTableJSON(t *model.TableInfo) string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// cdRandTable builds a random table: columns of a few types, maybe an int handle, unique
// keys, indexes, non-public and hidden columns.
func cdRandTable(rng *rand.Rand) *model.TableInfo {
	names := []string{"a", "b", "c", "d", "e", "f"}
	tps := []string{"int", "uint", "varchar", "double", "decimal", "datetime"}
	t := &model.TableInfo{ID: int64(100 + rng.IntN(50)), Name: pmodel.NewCIStr("t"), State: model.StatePublic}
	n := 1 + rng.IntN(5)
	for i := 0; i < n; i++ {
		ft := cdFieldType(tps[rng.IntN(len(tps))])
		c := &model.ColumnInfo{ID: int64(i + 1), Name: pmodel.NewCIStr(names[i]), Offset: i, FieldType: *ft, State: model.StatePublic}
		if rng.IntN(8) == 0 {
			c.State = model.StateWriteOnly
		}
		if rng.IntN(10) == 0 {
			c.Hidden = true
		}
		if rng.IntN(5) == 0 {
			c.AddFlag(mysql.UniqueKeyFlag)
		}
		t.Columns = append(t.Columns, c)
	}
	if t.Columns[0].GetType() == mysql.TypeLonglong && rng.IntN(2) == 0 {
		t.PKIsHandle = true
		t.Columns[0].AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
	}
	ni := rng.IntN(4)
	for i := 0; i < ni; i++ {
		idx := &model.IndexInfo{ID: int64(i + 1), Name: pmodel.NewCIStr(fmt.Sprintf("i%d", i)), State: model.StatePublic,
			Unique: rng.IntN(3) == 0, Tp: pmodel.IndexTypeBtree}
		if rng.IntN(8) == 0 {
			idx.State = model.StateDeleteOnly
		}
		perm := rng.Perm(n)
		k := 1 + rng.IntN(min(n, 3))
		for _, off := range perm[:k] {
			ic := &model.IndexColumn{Name: t.Columns[off].Name, Offset: off, Length: types.UnspecifiedLength}
			if t.Columns[off].GetType() == mysql.TypeVarchar && rng.IntN(4) == 0 {
				ic.Length = 4
			}
			idx.Columns = append(idx.Columns, ic)
		}
		t.Indices = append(t.Indices, idx)
	}
	return t
}

// cdRandCols: the query's columns, a subset of the table's (uniqueID = 1000 + colID, or a
// shuffled id).
func cdRandCols(rng *rand.Rand, t *model.TableInfo) [][2]int64 {
	out := [][2]int64{}
	for _, c := range t.Columns {
		if rng.IntN(5) == 0 {
			continue
		}
		out = append(out, [2]int64{c.ID, 1000 + c.ID*7})
	}
	return out
}

func cdRandDatum(rng *rand.Rand, tp byte, unsigned bool) any {
	switch rng.IntN(12) {
	case 0:
		return cdD("null", nil)
	case 1:
		return cdD("min", nil)
	case 2:
		return cdD("max", nil)
	}
	switch tp {
	case mysql.TypeLonglong:
		if unsigned {
			return cdD("u", []uint64{0, 1, 5, 100, 1 << 63, 18446744073709551615}[rng.IntN(6)])
		}
		return cdD("i", []int64{-9223372036854775808, -5, 0, 1, 5, 100, 9223372036854775807}[rng.IntN(7)])
	case mysql.TypeVarchar:
		return map[string]any{"k": "s", "v": []string{"", "a", "ab", "b", "A"}[rng.IntN(5)], "c": "utf8mb4_bin"}
	case mysql.TypeDouble:
		return cdD("f", []string{"-1.5", "0", "2.25", "1e300"}[rng.IntN(4)])
	case mysql.TypeNewDecimal:
		return cdD("dec", []string{"-1.50", "0", "2.25", "99999999.99"}[rng.IntN(4)])
	case mysql.TypeDatetime:
		return cdD("dt", []string{"2020-01-01 00:00:00", "1999-12-31 23:59:59", "2020-01-02 00:00:00"}[rng.IntN(3)])
	}
	return cdD("i", int64(rng.IntN(10)))
}

func cdColl(tp byte) string {
	if tp == mysql.TypeVarchar {
		return "utf8mb4_bin"
	}
	return "binary"
}

func cdRandRange(rng *rand.Rand, cols []*model.ColumnInfo) any {
	n := len(cols)
	lo, hi, colls := []any{}, []any{}, []string{}
	point := rng.IntN(2) == 0
	for i, c := range cols {
		uns := mysql.HasUnsignedFlag(c.GetFlag())
		l := cdRandDatum(rng, c.GetType(), uns)
		h := l
		if !point || i == n-1 {
			if rng.IntN(2) == 0 {
				h = cdRandDatum(rng, c.GetType(), uns)
			}
		}
		lo, hi = append(lo, l), append(hi, h)
		colls = append(colls, cdColl(c.GetType()))
	}
	return []any{lo, hi, rng.IntN(4) == 0, rng.IntN(4) == 0, colls}
}

// cdRandExpr: a random condition. IN (D08.9) is rare: tidb-rs cannot build it yet.
//
// A DATETIME column compared with an integer constant is not generated: the comparison
// converts the constant with the expression context's type flags (refineNumericConstantCmpDatetime),
// and the Go mock context and the tidb-rs test context differ there; that is the expression
// block's business (D08.4), not the estimation's.
func cdRandExpr(rng *rand.Rand, t *model.TableInfo, ncols, depth int) any {
	if ncols == 0 || rng.IntN(10) == 0 {
		return []any{"const", []any{cdD("i", 1), cdD("i", 0), cdD("null", nil)}[rng.IntN(3)]}
	}
	pos := rng.IntN(ncols)
	col := []any{"col", pos}
	if cdIsDatetimePos(t, pos) {
		return []any{"f", "isnull", col}
	}
	c := cdD("i", int64(rng.IntN(5)))
	switch rng.IntN(12) {
	case 0, 1, 2:
		if rng.IntN(2) == 0 {
			return []any{"f", "eq", col, []any{"const", c}}
		}
		return []any{"f", "eq", []any{"const", c}, col}
	case 3:
		return []any{"f", []string{"lt", "le", "gt", "ge"}[rng.IntN(4)], col, []any{"const", c}}
	case 4:
		return []any{"f", "eq", col, []any{"cor", rng.IntN(ncols)}}
	case 5:
		return []any{"f", "nulleq", col, []any{"const", c}}
	case 6:
		if rng.IntN(5) == 0 {
			return []any{"f", "in", col, []any{"const", c}}
		}
		return []any{"f", "ge", []any{"const", c}, col}
	case 7:
		if depth > 0 {
			return []any{"f", "or", cdRandExpr(rng, t, ncols, depth-1), cdRandExpr(rng, t, ncols, depth-1)}
		}
		return []any{"f", "ne", col, []any{"const", c}}
	case 8:
		return []any{"f", "isnull", col}
	case 9:
		return []any{"f", "eq", col, []any{"col", rng.IntN(ncols)}}
	case 10:
		if depth > 0 {
			return []any{"f", "not", cdRandExpr(rng, t, ncols, depth-1)}
		}
		return col
	}
	return []any{"const", cdD("i", 1)}
}

// cdIsDatetimePos: whether the query column at pos (cdRandCols order) is a DATETIME column.
func cdIsDatetimePos(t *model.TableInfo, pos int) bool {
	return pos < len(cdPosTypes) && cdPosTypes[pos] == mysql.TypeDatetime
}

// cdPosTypes: the types of the current table's query columns by position (set by the
// generator before it builds the conditions of a table).
var cdPosTypes []byte

func cardinalityGenerate(g *vec.Gen, rng *rand.Rand) {
	// from: pkg/planner/cardinality/row_count_test.go TestPseudoTable
	ti := &model.TableInfo{}
	ti.Columns = append(ti.Columns, &model.ColumnInfo{ID: 1, FieldType: *types.NewFieldType(mysql.TypeLonglong), State: model.StatePublic})
	tj := cdTableJSON(ti)
	g.Add("TestPseudoTable", "pseudo_table", tj, false, false)
	g.Add("TestPseudoTable", "col_est", tj, false, "gt", 1, cdD("i", 100))
	g.Add("TestPseudoTable", "col_est", tj, false, "eq", 1, cdD("i", 1000))
	ti.Columns = append(ti.Columns, &model.ColumnInfo{ID: 2, FieldType: *types.NewFieldType(mysql.TypeLonglong), Hidden: true, State: model.StatePublic})
	g.Add("TestPseudoTable", "pseudo_table", cdTableJSON(ti), false, false)

	// random tables
	for i := 0; i < 250; i++ {
		t := cdRandTable(rng)
		tj := cdTableJSON(t)
		g.Add("rand/pseudo_table", "pseudo_table", tj, rng.IntN(2) == 0, rng.IntN(4) != 0)
		g.Add("rand/hist_coll", "hist_coll", tj, rng.IntN(4) != 0, cdRandCols(rng, t))
		g.Add("rand/table_ops", "table_ops", tj)
		for j := 0; j < 2; j++ {
			c := t.Columns[rng.IntN(len(t.Columns))]
			g.Add("rand/col_est", "col_est", tj, rng.IntN(2) == 0, []string{"eq", "gt"}[rng.IntN(2)], c.ID,
				cdRandDatum(rng, c.GetType(), mysql.HasUnsignedFlag(c.GetFlag())))
		}
		cols := cdRandCols(rng, t)
		// column / int ranges
		for j := 0; j < 3; j++ {
			c := t.Columns[rng.IntN(len(t.Columns))]
			uid := 1000 + c.ID*7
			if rng.IntN(6) == 0 {
				uid = 77
			}
			kind := "col"
			if c.GetType() == mysql.TypeLonglong && rng.IntN(2) == 0 {
				kind = "int"
			}
			rs := []any{}
			for k := rng.IntN(4); k > 0; k-- {
				rs = append(rs, cdRandRange(rng, []*model.ColumnInfo{c}))
			}
			g.Add("rand/row_count", "row_count", tj, rng.IntN(4) != 0, rng.IntN(3) == 0, cols, kind, uid, rs)
		}
		// index ranges
		for _, idx := range t.Indices {
			icols := []*model.ColumnInfo{}
			for _, ic := range idx.Columns {
				icols = append(icols, t.Columns[ic.Offset])
			}
			icols = icols[:1+rng.IntN(len(icols))]
			rs := []any{}
			for k := rng.IntN(4); k > 0; k-- {
				if rng.IntN(5) == 0 {
					lo, hi, cl := []any{}, []any{}, []string{}
					for _, c := range icols {
						lo, hi, cl = append(lo, cdD("null", nil)), append(hi, cdD("max", nil)), append(cl, cdColl(c.GetType()))
					}
					rs = append(rs, []any{lo, hi, false, false, cl})
					continue
				}
				rs = append(rs, cdRandRange(rng, icols))
			}
			g.Add("rand/row_count", "row_count", tj, rng.IntN(4) != 0, rng.IntN(3) == 0, cols, "idx", idx.ID, rs)
		}
		// selectivity
		cdPosTypes = cdPosTypes[:0]
		for _, c := range cols {
			for _, ci := range t.Columns {
				if ci.ID == c[0] {
					cdPosTypes = append(cdPosTypes, ci.GetType())
				}
			}
		}
		for j := 0; j < 3; j++ {
			n := rng.IntN(4)
			es := []any{}
			for k := 0; k < n; k++ {
				es = append(es, cdRandExpr(rng, t, len(cols), 1))
			}
			g.Add("rand/selectivity", "selectivity", tj, cols, es, []string{"0.8", "0.5", "0.1"}[rng.IntN(3)])
		}
		// more than 63 conditions: pseudoSelectivity
		es := []any{}
		for k := 0; k < 64; k++ {
			e := cdRandExpr(rng, t, len(cols), 0)
			if a, ok := e.([]any); ok && len(a) > 1 && a[1] == "in" && rng.IntN(10) != 0 {
				e = []any{"const", cdD("i", 1)}
			}
			es = append(es, e)
		}
		g.Add("rand/selectivity", "selectivity", tj, cols, es, "0.8")
		// conditions without columns of the HistColl
		g.Add("rand/selectivity", "selectivity", tj, [][2]int64{}, []any{cdRandExpr(rng, t, 0, 0)}, "0.8")
		cdPosTypes = cdPosTypes[:0]
	}

	// row sizes
	tps := []string{"int", "uint", "double", "float", "varchar", "blob", "decimal", "datetime", "duration", "tiny", "enum", "json"}
	for i := 0; i < 400; i++ {
		statsCols := [][]any{}
		qcols := [][]any{}
		for k := rng.IntN(4); k > 0; k-- {
			uid := int64(1 + rng.IntN(5))
			tp := tps[rng.IntN(len(tps))]
			if rng.IntN(4) != 0 {
				statsCols = append(statsCols, []any{uid, tp, rng.IntN(5) == 0, []int64{0, 7, 100, 3000}[rng.IntN(4)],
					[]int64{0, 5, 100}[rng.IntN(3)]})
			}
			qcols = append(qcols, []any{uid, tp})
		}
		g.Add("rand/row_size", "row_size", rng.IntN(4) == 0, []int64{0, 100, 1000}[rng.IntN(3)], statsCols, qcols,
			rng.IntN(2) == 0, rng.IntN(2) == 0, rng.IntN(2) == 0, rng.IntN(2), rng.IntN(2) == 0, rng.IntN(2) == 0)
	}
	for i := 0; i < 300; i++ {
		g.Add("rand/avg_col", "avg_col", tps[rng.IntN(len(tps))], rng.IntN(5) == 0, []int64{0, 1, 7, 100, 3000, 12345}[rng.IntN(6)],
			[]int64{0, 5, 100}[rng.IntN(3)], []int64{0, 1, 3, 100, 1000}[rng.IntN(5)])
	}

	// histograms
	histTypes := []struct {
		tp  string
		gen func(i int) any
	}{
		{"int", func(i int) any { return cdD("i", int64(i*10-50)) }},
		{"uint", func(i int) any { return cdD("u", uint64(i*10)) }},
		{"double", func(i int) any { return cdD("f", fstr(float64(i)*2.5-10)) }},
		{"varchar", func(i int) any {
			return cdD("b", hex.EncodeToString([]byte(strings.Repeat("a", 1+i%3)+string(rune('a'+i)))))
		}},
		{"decimal", func(i int) any { return cdD("dec", fmt.Sprintf("%d.%02d", i*3-20, (i+100)%100)) }},
		{"datetime", func(i int) any { return cdD("dt", fmt.Sprintf("2020-01-%02d 0%d:00:00", 1+(i+28)%28, (i+10)%10)) }},
		{"duration", func(i int) any { return cdD("dur", fmt.Sprintf("%02d:%02d:00", (i+24)%24, (i+60)%60)) }},
	}
	for i := 0; i < 350; i++ {
		ht := histTypes[rng.IntN(len(histTypes))]
		nb := rng.IntN(5)
		points := rng.Perm(30)[:2*nb]
		sort.Ints(points)
		buckets := []any{}
		for b := 0; b < nb; b++ {
			lo, hi := points[2*b], points[2*b+1]
			if rng.IntN(5) == 0 {
				hi = lo
			}
			buckets = append(buckets, []any{ht.gen(lo), ht.gen(hi), 1 + rng.IntN(50), rng.IntN(5), rng.IntN(6)})
		}
		vals := []any{}
		for k := 0; k < 5; k++ {
			vals = append(vals, ht.gen(rng.IntN(32)-1))
		}
		var topn any
		if rng.IntN(3) == 0 {
			topn = [][]any{{"0102", 3 + rng.IntN(5)}, {"03", rng.IntN(5)}}
		}
		g.Add("rand/hist", "hist", ht.tp, buckets, int64(rng.IntN(30)), int64(rng.IntN(10)), int64(rng.IntN(3)), topn, vals,
			int64(rng.IntN(500)), int64(rng.IntN(100)))
	}

	// column / index statistics
	for i := 0; i < 400; i++ {
		var topn any
		if rng.IntN(3) == 0 {
			topn = [][]any{{"0a0b", 1 + rng.IntN(9)}}
		}
		g.Add("rand/col_stats", "col_stats", rng.IntN(2) == 0, int64(rng.IntN(3)), int64(rng.IntN(5)), int64(rng.IntN(4)),
			int64([]int{0, 0, 10, 40}[rng.IntN(4)]), topn, []string{"none", "full", "evicted"}[rng.IntN(3)],
			rng.IntN(2) == 0, rng.IntN(2) == 0, int64(rng.IntN(4)-1), int64([]int{0, 10, 1000}[rng.IntN(3)]))
	}

	// existence maps
	for i := 0; i < 150; i++ {
		steps := [][]any{}
		for k := rng.IntN(8); k >= 0; k-- {
			steps = append(steps, []any{[]string{"insc", "insi", "delc", "deli", "clone"}[rng.IntN(5)], int64(rng.IntN(4)), rng.IntN(2) == 0})
		}
		g.Add("rand/existence", "existence", steps)
	}

	// NDV of column groups
	for i := 0; i < 250; i++ {
		schema := rng.Perm(6)[:1+rng.IntN(5)]
		uids := []int64{}
		for _, u := range schema {
			uids = append(uids, int64(u+1))
		}
		ndvs := [][]any{}
		for _, u := range uids {
			if rng.IntN(4) != 0 {
				ndvs = append(ndvs, []any{u, fstr(float64(rng.IntN(100)) / 4)})
			}
		}
		groups := [][]any{}
		for k := rng.IntN(3); k > 0; k-- {
			gc := []int64{}
			for _, u := range rng.Perm(6)[:1+rng.IntN(3)] {
				gc = append(gc, int64(u+1))
			}
			sort.Slice(gc, func(a, b int) bool { return gc[a] < gc[b] })
			groups = append(groups, []any{gc, fstr(float64(rng.IntN(50)) / 2)})
		}
		q := []int64{}
		for _, u := range rng.Perm(7)[:rng.IntN(4)] {
			q = append(q, int64(u+1))
		}
		g.Add("rand/ndv", "ndv", uids, ndvs, groups, q)
	}

	// MV index path selectivity (table paths)
	for i := 0; i < 150; i++ {
		counts := []string{}
		for k := rng.IntN(5); k > 0; k-- {
			counts = append(counts, fstr(rng.Float64()*1500))
		}
		g.Add("rand/mv_sel", "mv_sel", int64([]int{0, 1, 1000, 10000}[rng.IntN(4)]), counts, rng.IntN(2) == 0)
	}

	// TopN
	g.Add("sys/topn", "topn", nil)
	g.Add("sys/topn", "topn", [][]any{})
	for i := 0; i < 60; i++ {
		items := [][]any{}
		for k := rng.IntN(5); k > 0; k-- {
			b := make([]byte, rng.IntN(6))
			for j := range b {
				b[j] = byte(rng.IntN(256))
			}
			items = append(items, []any{hex.EncodeToString(b), rng.IntN(1000)})
		}
		g.Add("rand/topn", "topn", items)
	}
}
