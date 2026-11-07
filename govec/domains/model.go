package domains

// Domain "model": block D06.1 part 1 (pkg/meta/model: the schema structs and their
// encoding/json form; db.go, table.go, column.go, index.go, placement.go,
// resource_group.go, flags.go). Records:
//
//   - "roundtrip": args [type, json text]: v := new(type); err := json.Unmarshal(text, v);
//     out {"json": json.Marshal(v), "merr": marshal error}. The err is Unmarshal's. When v
//     holds a nil element of a []*T, out also has "known_deviation": "nil-ptr-elem".
//     Texts: meta exported from a v8.5.8 cluster (testdata/model_cluster.jsonl.gz, made
//     by testdata/model_cluster_ddl.sql through the status port /schema/{db}/{table}),
//     random values of every type, and mutations of both (keys dropped, duplicated,
//     re-cased, unknown keys, wrong value types, numbers out of range, nulls).
//   - "into": args [type, base text, text]: Unmarshal(base) then Unmarshal(text) into the
//     same value (decoding into an existing value), out as "roundtrip".
//   - "marshal_time": args [unix sec, nsec, zone offset]: Marshal of a StatsOptions whose
//     window_start is that time in a fixed zone (Time.MarshalJSON's range errors).
//   - "marshal_nan": args [table text, which]: a column's default set to NaN / +Inf /
//     -Inf; out the marshal error.
//   - "table": args [table text, probes, id]: the TableInfo methods (see tableOut).
//   - "column", "index", "partition", "placement", "resource", "fk", "misc": the other
//     methods; see their Exec cases.

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/auth"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	ptypes "github.com/pingcap/tidb/pkg/parser/types"
)

const modelSeed = 1618

//go:embed testdata/model_cluster.jsonl.gz
var modelClusterGz []byte

func init() {
	vec.Register(&vec.Domain{
		Name:     "model",
		Seed:     modelSeed,
		Coverage: modelCoverage,
		Blocks:   []string{"D06.1"},
		Exempt:   modelExempt,
		Gzip:     true,
		Generate: modelGenerate,
		Exec:     modelExec,
	})
}

var modelExempt = map[string]string{
	"ActionType.String":                       "job.go: D06.1 part 2",
	"Job.Decode":                              "job.go: D06.1 part 2",
	"Job.GetRowCount":                         "job.go: D06.1 part 2",
	"Job.GetWarnings":                         "job.go: D06.1 part 2",
	"Job.MayNeedReorg":                        "job.go: D06.1 part 2",
	"Job.String":                              "job.go: D06.1 part 2",
	"JobState.String":                         "job.go: D06.1 part 2",
	"JobVersion.String":                       "job.go: D06.1 part 2",
	"SchemaState.String":                      "job.go: D06.1 part 2",
	"DDLReorgMeta.GetBatchSizeOrDefault":      "reorg.go: D06.1 part 2",
	"DDLReorgMeta.GetConcurrencyOrDefault":    "reorg.go: D06.1 part 2",
	"DDLReorgMeta.GetMaxWriteSpeedOrDefault":  "reorg.go: D06.1 part 2",
	"ReorgType.String":                        "reorg.go: D06.1 part 2",
	"ColumnNeedRestoredData":                  "pkg/types/etc.go: needs types.NeedRestoredData and the collate package (D01.9, in progress); D06.1 part 2",
	"IndexInfo.ConditionExpr":                 "returns the parser's AST; covered through isIndexConditionCoveredByForeignKeyCols (misc fk_covered)",
	"writeSettingDurationToBuilder":           "covered through ResourceGroupSettings.String (resource)",
	"writeSettingIntegerToBuilder":            "covered through PlacementSettings.String (placement)",
	"writeSettingItemToBuilder":               "covered through PlacementSettings.String (placement)",
	"writeSettingStringToBuilder":             "covered through PlacementSettings.String (placement)",
	"isIndexConditionCoveredByForeignKeyCols": "covered through IsIndexPrefixCoveredForForeignKey (misc)",
}

var modelCoverage = map[string]vec.Coverage{
	"roundtrip":    {Min: 3000},
	"into":         {Min: 200},
	"marshal_nan":  {Min: 3},
	"marshal_time": {Min: 10},
	"table": {Min: 300, GoFuncs: []string{"TableInfo.SepAutoInc", "TableInfo.GetPartitionInfo", "TableInfo.GetUpdateTime",
		"TableInfo.GetPkName", "TableInfo.GetPkColInfo", "TableInfo.GetAutoIncrementColInfo", "TableInfo.IsAutoIncColUnsigned",
		"TableInfo.ContainsAutoRandomBits", "TableInfo.IsAutoRandomBitColUnsigned", "TableInfo.Cols", "TableInfo.FindIndexByName",
		"TableInfo.FindColumnByID", "TableInfo.FindIndexByID", "TableInfo.FindPublicColumnByName", "TableInfo.IsLocked",
		"TableInfo.ClearPlacement", "TableInfo.GetPrimaryKey", "TableInfo.ColumnIsInIndex", "TableInfo.HasClusteredIndex",
		"TableInfo.IsView", "TableInfo.IsSequence", "TableInfo.IsBaseTable", "TableInfo.FindConstraintInfoByName",
		"TableInfo.FindIndexNameByID", "TableInfo.FindColumnNameByID", "TableInfo.GetColumnByID", "TableInfo.GetNonTempColumns",
		"TableInfo.Clone", "TableInfo.MoveColumnInfo", "TSConvert2Time", "TableCacheStatusType.String", "TempTableType.String",
		"TableLockState.String", "TableMode.String", "TiFlashReplicaInfo.IsPartitionAvailable", "SessionInfo.String",
		"FindColumnInfo", "FindColumnInfoByID", "FindIndexInfoByID", "FindFKInfoByName", "GenUniqueChangingColumnName",
		"GenUniqueChangingIndexName", "IndexInfo.HasColumnInIndexColumns", "ColumnInfo.Clone", "IndexInfo.Clone",
		"IndexColumn.Clone", "FKInfo.Clone", "ConstraintInfo.Clone", "PartitionInfo.Clone", "PartitionDefinition.Clone",
		"TTLInfo.Clone", "TableAffinityInfo.Clone", "SoftdeleteInfo.Clone"}},
	"column": {Min: 300, GoFuncs: []string{"ColumnInfo.GetType", "ColumnInfo.GetFlag", "ColumnInfo.GetFlen", "ColumnInfo.GetDecimal",
		"ColumnInfo.GetCharset", "ColumnInfo.GetCollate", "ColumnInfo.GetElems", "ColumnInfo.IsGenerated", "ColumnInfo.IsVirtualGenerated",
		"ColumnInfo.IsChanging", "ColumnInfo.IsRemoving", "ColumnInfo.GetRemovingOriginName", "ColumnInfo.GetChangingOriginName",
		"ColumnInfo.GetOriginDefaultValue", "ColumnInfo.GetDefaultValue", "ColumnInfo.GetTypeDesc"}},
	"column_set": {Min: 100, GoFuncs: []string{"ColumnInfo.SetType", "ColumnInfo.SetFlag", "ColumnInfo.AddFlag", "ColumnInfo.AndFlag",
		"ColumnInfo.ToggleFlag", "ColumnInfo.DelFlag", "ColumnInfo.SetFlen", "ColumnInfo.SetDecimal", "ColumnInfo.SetCharset",
		"ColumnInfo.SetCollate", "ColumnInfo.SetElems", "ColumnInfo.SetOriginDefaultValue", "ColumnInfo.SetDefaultValue"}},
	"index": {Min: 100, GoFuncs: []string{"IndexInfo.IsChanging", "IndexInfo.IsRemoving", "IndexInfo.GetRemovingOriginName",
		"IndexInfo.GetChangingOriginName", "IndexInfo.HasPrefixIndex", "IndexInfo.FindColumnByName", "IndexInfo.IsPublic",
		"IndexInfo.IsTiFlashLocalIndex", "IndexInfo.HasCondition", "FindIndexColumnByName", "GetIdxChangingFieldType"}},
	"partition": {Min: 100, GoFuncs: []string{"PartitionInfo.GetNameByID", "PartitionInfo.GetStateByID", "PartitionInfo.SetStateByID",
		"PartitionInfo.GCPartitionStates", "PartitionInfo.ClearReorgIntermediateInfo", "PartitionInfo.FindPartitionDefinitionByName",
		"PartitionInfo.GetPartitionIDByName", "PartitionInfo.GetDefaultListPartition", "PartitionInfo.CanHaveOverlappingDroppingPartition",
		"PartitionInfo.ReplaceWithOverlappingPartitionIdx", "PartitionInfo.GetOverlappingDroppingPartitionIdx", "PartitionInfo.IsDropping",
		"PartitionInfo.SetOriginalPartitionIDs", "PartitionInfo.IDsInDDLToIgnore", "PartitionDefinition.MemoryUsage"}},
	"placement": {Min: 50, GoFuncs: []string{"PlacementSettings.String", "PlacementSettings.Clone", "PolicyInfo.Clone"}},
	"resource": {Min: 50, GoFuncs: []string{"ResourceGroupSettings.String", "ResourceGroupSettings.Adjust", "ResourceGroupSettings.Clone",
		"ResourceGroupInfo.Clone", "NewResourceGroupSettings"}},
	"fk": {Min: 20, GoFuncs: []string{"FKInfo.String"}},
	"misc": {Min: 100, GoFuncs: []string{"GenRemovingObjName", "FindIndexByColumns", "IsIndexPrefixCovered",
		"FindIndexByColumnsForForeignKey", "IsIndexPrefixCoveredForForeignKey", "NewStatsOptions", "WindowRepeatType.String",
		"TTLInfo.GetJobInterval", "NewTableAffinityInfoWithLevel", "TableItemID.Key", "StatsLoadItem.Key",
		"NewExtraHandleColInfo", "NewExtraPhysTblIDColInfo", "DBInfo.Clone", "DBInfo.Copy", "LessDBInfo"}},
}

var modelTypes = map[string]func() any{
	"DBInfo": func() any { return new(model.DBInfo) }, "TableInfo": func() any { return new(model.TableInfo) },
	"ColumnInfo": func() any { return new(model.ColumnInfo) }, "IndexInfo": func() any { return new(model.IndexInfo) },
	"PartitionInfo": func() any { return new(model.PartitionInfo) }, "PartitionDefinition": func() any { return new(model.PartitionDefinition) },
	"ConstraintInfo": func() any { return new(model.ConstraintInfo) }, "FKInfo": func() any { return new(model.FKInfo) },
	"PolicyInfo": func() any { return new(model.PolicyInfo) }, "PlacementSettings": func() any { return new(model.PlacementSettings) },
	"ResourceGroupInfo": func() any { return new(model.ResourceGroupInfo) }, "ResourceGroupSettings": func() any { return new(model.ResourceGroupSettings) },
	"SchemaDiff": func() any { return new(model.SchemaDiff) }, "StatsOptions": func() any { return new(model.StatsOptions) },
	"TTLInfo": func() any { return new(model.TTLInfo) }, "ViewInfo": func() any { return new(model.ViewInfo) },
	"SequenceInfo": func() any { return new(model.SequenceInfo) }, "TiFlashReplicaInfo": func() any { return new(model.TiFlashReplicaInfo) },
	"TableLockInfo": func() any { return new(model.TableLockInfo) }, "StatsLoadItem": func() any { return new(model.StatsLoadItem) },
}

func marshalOut(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"json": nil, "merr": err.Error()}
	}
	return map[string]any{"json": vec.GoString(b), "merr": ""}
}

// knownNilPtrElem marks a record whose decoded value holds a nil element in a []*T
// slice (a JSON null element): the Rust port holds the elements by value and decodes
// null as the zero value (tidb-rs specs/D06.1-model §4.1). The replay counts such
// records as a known deviation, and fails if one no longer differs.
const knownNilPtrElem = "nil-ptr-elem"

func markDeviation(v any, out map[string]any) map[string]any {
	if hasNilPtrElem(reflect.ValueOf(v)) {
		out["known_deviation"] = knownNilPtrElem
	}
	return out
}

func hasNilPtrElem(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer:
		return !v.IsNil() && hasNilPtrElem(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() && hasNilPtrElem(v.Field(i)) {
				return true
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			if e.Kind() == reflect.Pointer && e.IsNil() {
				return true
			}
			if hasNilPtrElem(e) {
				return true
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if hasNilPtrElem(it.Value()) {
				return true
			}
		}
	}
	return false
}

func mustTable(text vec.GoString) *model.TableInfo {
	t := new(model.TableInfo)
	if err := json.Unmarshal([]byte(text), t); err != nil {
		panic("bad table text: " + err.Error())
	}
	return t
}

func idOf[T any](p *T, id func(*T) int64) any {
	if p == nil {
		return nil
	}
	return id(p)
}

func colNames(cols []*model.ColumnInfo) []any {
	out := make([]any, 0, len(cols))
	for _, c := range cols {
		if c == nil {
			out = append(out, nil)
		} else {
			out = append(out, c.Name.O)
		}
	}
	return out
}

func tableOut(t *model.TableInfo, probes map[string]any, id int64) map[string]any {
	out := map[string]any{}
	out["sep_auto_inc"] = t.SepAutoInc()
	out["partition"] = t.GetPartitionInfo() != nil
	out["update_time"] = t.GetUpdateTime().UnixNano()
	out["pk_name"] = t.GetPkName().O
	out["pk_col"] = idOf(t.GetPkColInfo(), func(c *model.ColumnInfo) int64 { return c.ID })
	out["auto_inc_col"] = idOf(t.GetAutoIncrementColInfo(), func(c *model.ColumnInfo) int64 { return c.ID })
	out["auto_inc_unsigned"] = t.IsAutoIncColUnsigned()
	out["auto_random"] = t.ContainsAutoRandomBits()
	func() {
		defer func() {
			if r := recover(); r != nil {
				out["auto_random_unsigned"] = "panic"
			}
		}()
		out["auto_random_unsigned"] = t.IsAutoRandomBitColUnsigned()
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				out["cols"] = "panic"
			}
		}()
		out["cols"] = colNames(t.Cols())
	}()
	idx := fmt.Sprint(probes["index"])
	out["find_index_by_name"] = idOf(t.FindIndexByName(idx), func(i *model.IndexInfo) int64 { return i.ID })
	out["find_column_by_id"] = idOf(t.FindColumnByID(id), func(c *model.ColumnInfo) int64 { return int64(c.Offset) })
	out["find_index_by_id"] = idOf(t.FindIndexByID(id), func(i *model.IndexInfo) int64 { return i.ID })
	func() {
		defer func() {
			if r := recover(); r != nil {
				out["find_public_column"] = "panic"
			}
		}()
		out["find_public_column"] = idOf(t.FindPublicColumnByName(fmt.Sprint(probes["col"])), func(c *model.ColumnInfo) int64 { return c.ID })
	}()
	out["is_locked"] = t.IsLocked()
	func() {
		defer func() {
			if r := recover(); r != nil {
				out["primary_key"] = "panic"
			}
		}()
		out["primary_key"] = idOf(t.GetPrimaryKey(), func(i *model.IndexInfo) int64 { return i.ID })
	}()
	if len(t.Columns) > 0 {
		out["column_is_in_index"] = t.ColumnIsInIndex(t.Columns[0])
		out["gen_changing_column"] = model.GenUniqueChangingColumnName(t, t.Columns[0])
	}
	if len(t.Indices) > 0 {
		out["gen_changing_index"] = model.GenUniqueChangingIndexName(t, t.Indices[0])
		func() {
			defer func() {
				if r := recover(); r != nil {
					out["has_column_in_index"] = "panic"
				}
			}()
			out["has_column_in_index"] = t.Indices[0].HasColumnInIndexColumns(t, id)
		}()
	}
	out["clustered"] = t.HasClusteredIndex()
	out["view"] = t.IsView()
	out["sequence"] = t.IsSequence()
	out["base"] = t.IsBaseTable()
	out["constraint"] = idOf(t.FindConstraintInfoByName(fmt.Sprint(probes["constraint"])), func(c *model.ConstraintInfo) int64 { return c.ID })
	out["index_name_by_id"] = t.FindIndexNameByID(id)
	out["column_name_by_id"] = t.FindColumnNameByID(id)
	out["column_by_id"] = idOf(t.GetColumnByID(id), func(c *model.ColumnInfo) int64 { return c.ID })
	nt := t.GetNonTempColumns()
	names := colNames(nt)
	sort.Slice(names, func(i, j int) bool { return names[i].(string) < names[j].(string) })
	out["non_temp"] = names
	out["find_column_info"] = idOf(model.FindColumnInfo(t.Columns, fmt.Sprint(probes["col"])), func(c *model.ColumnInfo) int64 { return c.ID })
	out["find_index_info_by_id"] = idOf(model.FindIndexInfoByID(t.Indices, id), func(i *model.IndexInfo) int64 { return i.ID })
	out["find_fk"] = idOf(model.FindFKInfoByName(t.ForeignKeys, fmt.Sprint(probes["fk"])), func(f *model.FKInfo) int64 { return f.ID })
	out["cache_status"] = t.TableCacheStatusType.String()
	out["temp"] = t.TempTableType.String()
	out["mode"] = t.Mode.String()
	if t.Lock != nil {
		out["lock_state"] = t.Lock.State.String()
		var ss []string
		for _, s := range t.Lock.Sessions {
			ss = append(ss, s.String())
		}
		out["lock_sessions"] = ss
	}
	if t.TiFlashReplica != nil {
		out["tiflash_available"] = t.TiFlashReplica.IsPartitionAvailable(id)
	}
	out["clone"] = marshalOut(t.Clone())
	c := t.Clone()
	c.ClearPlacement()
	out["clear_placement"] = marshalOut(c)
	if n := len(t.Columns); n > 0 {
		from, to := int(probes["from"].(float64))%n, int(probes["to"].(float64))%n
		m := t.Clone()
		func() {
			defer func() {
				if r := recover(); r != nil {
					out["move"] = "panic"
				}
			}()
			m.MoveColumnInfo(from, to)
			out["move"] = marshalOut(m)
		}()
	}
	return out
}

func modelExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "roundtrip":
		v := modelTypes[arg[string](args, 0)]()
		err := json.Unmarshal([]byte(arg[vec.GoString](args, 1)), v)
		return markDeviation(v, marshalOut(v)), err
	case "into":
		v := modelTypes[arg[string](args, 0)]()
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 1)), v); err != nil {
			panic("bad base: " + err.Error())
		}
		err := json.Unmarshal([]byte(arg[vec.GoString](args, 2)), v)
		return markDeviation(v, marshalOut(v)), err
	case "marshal_time":
		so := &model.StatsOptions{StatsWindowSettings: &model.StatsWindowSettings{
			WindowStart: time.Unix(arg[int64](args, 0), arg[int64](args, 1)).In(time.FixedZone("", int(arg[int64](args, 2))))}}
		return marshalOut(so), nil
	case "marshal_nan":
		t := mustTable(arg[vec.GoString](args, 0))
		f := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}[arg[int](args, 1)]
		t.Columns[0].DefaultValue = f
		return marshalOut(t), nil
	case "table":
		return tableOut(mustTable(arg[vec.GoString](args, 0)), arg[map[string]any](args, 1), arg[int64](args, 2)), nil
	case "column":
		c := new(model.ColumnInfo)
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 0)), c); err != nil {
			panic(err)
		}
		dv := func(v any) any {
			if s, ok := v.(string); ok {
				return map[string]any{"s": vec.GoString(s)}
			}
			return v
		}
		return map[string]any{"type": c.GetType(), "flag": c.GetFlag(), "flen": c.GetFlen(), "decimal": c.GetDecimal(),
			"charset": c.GetCharset(), "collate": c.GetCollate(), "elems": gsList(c.GetElems()), "generated": c.IsGenerated(),
			"virtual": c.IsVirtualGenerated(), "changing": c.IsChanging(), "removing": c.IsRemoving(),
			"removing_origin": c.GetRemovingOriginName(), "changing_origin": c.GetChangingOriginName(),
			"origin_default": dv(c.GetOriginDefaultValue()), "default": dv(c.GetDefaultValue()), "type_desc": c.GetTypeDesc(),
			"clone": marshalOut(c.Clone())}, nil
	case "column_set":
		c := new(model.ColumnInfo)
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 0)), c); err != nil {
			panic(err)
		}
		steps := arg[[][]json.RawMessage](args, 1)
		var errs []any
		for _, st := range steps {
			n := func(i int) int { return arg[int](st, i) }
			switch arg[string](st, 0) {
			case "type":
				c.SetType(byte(n(1)))
			case "flag":
				c.SetFlag(uint(n(1)))
			case "add":
				c.AddFlag(uint(n(1)))
			case "and":
				c.AndFlag(uint(n(1)))
			case "toggle":
				c.ToggleFlag(uint(n(1)))
			case "del":
				c.DelFlag(uint(n(1)))
			case "flen":
				c.SetFlen(n(1))
			case "decimal":
				c.SetDecimal(n(1))
			case "charset":
				c.SetCharset(arg[string](st, 1))
			case "collate":
				c.SetCollate(arg[string](st, 1))
			case "elems":
				c.SetElems([]string{"a", string(arg[vec.GoString](st, 1))})
			case "origin_default", "default":
				var v any
				switch arg[string](st, 1) {
				case "nil":
				case "str":
					v = string(arg[vec.GoString](st, 2))
				case "int":
					v = arg[int64](st, 2)
				case "float":
					v = arg[float64](st, 2)
				}
				var err error
				if arg[string](st, 0) == "default" {
					err = c.SetDefaultValue(v)
				} else {
					err = c.SetOriginDefaultValue(v)
				}
				if err != nil {
					errs = append(errs, vec.GoString(err.Error()))
				} else {
					errs = append(errs, nil)
				}
			}
		}
		return map[string]any{"col": marshalOut(c), "errs": errs}, nil
	case "index":
		t := mustTable(arg[vec.GoString](args, 0))
		name := arg[string](args, 1)
		var res []any
		for _, idx := range t.Indices {
			ic := idx.FindColumnByName(name)
			i2, ic2 := model.FindIndexColumnByName(idx.Columns, name)
			r := map[string]any{"changing": idx.IsChanging(), "removing": idx.IsRemoving(), "removing_origin": idx.GetRemovingOriginName(),
				"changing_origin": idx.GetChangingOriginName(), "prefix": idx.HasPrefixIndex(), "public": idx.IsPublic(),
				"tiflash": idx.IsTiFlashLocalIndex(), "cond": idx.HasCondition(), "find_col": ic != nil, "find_col_idx": i2,
				"find_col2": ic2 != nil, "clone": marshalOut(idx.Clone())}
			var fts []any
			for _, c := range idx.Columns {
				if c.Offset >= 0 && c.Offset < len(t.Columns) {
					b, _ := model.GetIdxChangingFieldType(c, t.Columns[c.Offset]).MarshalJSON()
					fts = append(fts, vec.GoString(b))
				}
			}
			r["changing_fts"] = fts
			res = append(res, r)
		}
		return res, nil
	case "partition":
		pi := new(model.PartitionInfo)
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 0)), pi); err != nil {
			panic(err)
		}
		id, name, idx := arg[int64](args, 1), arg[string](args, 2), arg[int](args, 3)
		out := map[string]any{"name_by_id": pi.GetNameByID(id), "state_by_id": uint8(pi.GetStateByID(id)),
			"find_def": pi.FindPartitionDefinitionByName(name), "id_by_name": pi.GetPartitionIDByName(name),
			"default_list": pi.GetDefaultListPartition(), "overlapping": pi.CanHaveOverlappingDroppingPartition(),
			"ids_to_ignore": pi.IDsInDDLToIgnore()}
		func() {
			defer func() {
				if r := recover(); r != nil {
					out["overlapping_idx"] = "panic"
				}
			}()
			out["overlapping_idx"] = pi.GetOverlappingDroppingPartitionIdx(idx)
			i1, e1 := pi.ReplaceWithOverlappingPartitionIdx(idx, nil)
			i2, e2 := pi.ReplaceWithOverlappingPartitionIdx(idx, fmt.Errorf("e"))
			out["replace"] = []any{i1, e1 != nil, i2, e2 != nil}
		}()
		func() {
			defer func() {
				if r := recover(); r != nil {
					out["dropping"] = "panic"
				}
			}()
			out["dropping"] = pi.IsDropping(idx)
		}()
		var mem []int64
		for i := range pi.Definitions {
			mem = append(mem, pi.Definitions[i].MemoryUsage())
		}
		out["mem"] = mem
		out["clone"] = marshalOut(pi.Clone())
		p2 := pi.Clone()
		p2.SetStateByID(id, model.SchemaState(arg[int](args, 4)))
		out["set_state"] = marshalOut(p2)
		p2.GCPartitionStates()
		out["gc"] = marshalOut(p2)
		p2.SetOriginalPartitionIDs()
		p2.ClearReorgIntermediateInfo()
		out["clear"] = marshalOut(p2)
		return out, nil
	case "placement":
		p := new(model.PolicyInfo)
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 0)), p); err != nil {
			panic(err)
		}
		out := map[string]any{}
		if p.PlacementSettings != nil {
			out["string"] = p.PlacementSettings.String()
			out["clone_settings"] = marshalOut(p.PlacementSettings.Clone())
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					out["clone"] = "panic"
				}
			}()
			out["clone"] = marshalOut(p.Clone())
		}()
		return out, nil
	case "resource":
		g := new(model.ResourceGroupInfo)
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 0)), g); err != nil {
			panic(err)
		}
		out := map[string]any{"new": marshalOut(model.NewResourceGroupSettings())}
		if g.ResourceGroupSettings != nil {
			out["string"] = g.ResourceGroupSettings.String()
			s := g.ResourceGroupSettings.Clone()
			s.Adjust()
			out["adjust"] = marshalOut(s)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					out["clone"] = "panic"
				}
			}()
			out["clone"] = marshalOut(g.Clone())
		}()
		return out, nil
	case "fk":
		fk := new(model.FKInfo)
		if err := json.Unmarshal([]byte(arg[vec.GoString](args, 0)), fk); err != nil {
			panic(err)
		}
		return map[string]any{"string": fk.String(arg[string](args, 1), arg[string](args, 2)), "clone": marshalOut(fk.Clone())}, nil
	case "misc":
		switch arg[string](args, 0) {
		case "removing_name":
			return model.GenRemovingObjName(arg[string](args, 1)), nil
		case "covered":
			t := mustTable(arg[vec.GoString](args, 1))
			var cols []pmodel.CIStr
			for _, c := range arg[[]string](args, 2) {
				cols = append(cols, pmodel.NewCIStr(c))
			}
			var cov, covFK []bool
			for _, idx := range t.Indices {
				cov = append(cov, model.IsIndexPrefixCovered(t, idx, cols...))
				covFK = append(covFK, model.IsIndexPrefixCoveredForForeignKey(t, idx, cols...))
			}
			return map[string]any{"cov": cov, "cov_fk": covFK,
				"find":    idOf(model.FindIndexByColumns(t, t.Indices, cols...), func(i *model.IndexInfo) int64 { return i.ID }),
				"find_fk": idOf(model.FindIndexByColumnsForForeignKey(t, t.Indices, cols...), func(i *model.IndexInfo) int64 { return i.ID })}, nil
		case "stats_options":
			return marshalOut(model.NewStatsOptions()), nil
		case "window":
			return model.WindowRepeatType(arg[uint8](args, 1)).String(), nil
		case "job_interval":
			ttl := &model.TTLInfo{JobInterval: arg[string](args, 1)}
			d, err := ttl.GetJobInterval()
			return int64(d), err
		case "affinity":
			a, err := model.NewTableAffinityInfoWithLevel(arg[string](args, 1))
			if err != nil {
				return nil, err
			}
			if a == nil {
				return nil, nil
			}
			return marshalOut(a.Clone()), nil
		case "item_key":
			it := model.StatsLoadItem{TableItemID: model.TableItemID{TableID: arg[int64](args, 1), ID: arg[int64](args, 2),
				IsIndex: arg[bool](args, 3)}, FullLoad: arg[bool](args, 4)}
			return []string{it.TableItemID.Key(), it.Key()}, nil
		case "extra_cols":
			return []any{marshalOut(model.NewExtraHandleColInfo()), marshalOut(model.NewExtraPhysTblIDColInfo())}, nil
		case "ts_time":
			return model.TSConvert2Time(arg[uint64](args, 1)).UnixNano(), nil
		case "db":
			a, b := new(model.DBInfo), new(model.DBInfo)
			if err := json.Unmarshal([]byte(arg[vec.GoString](args, 1)), a); err != nil {
				panic(err)
			}
			if err := json.Unmarshal([]byte(arg[vec.GoString](args, 2)), b); err != nil {
				panic(err)
			}
			a.Deprecated.Tables = []*model.TableInfo{{ID: 7, Name: pmodel.NewCIStr("x")}}
			return map[string]any{"clone": marshalOut(a.Clone()), "copy": marshalOut(a.Copy()), "clone_tables": len(a.Clone().Deprecated.Tables),
				"less": model.LessDBInfo(a, b)}, nil
		}
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// ---------------------------------------------------------------- random values

type ptRand struct{ rng *rand.Rand }

var ptStrs = []string{"", "a", "A_b", "<>&", " x ", "日本語", "a\"b\\c", "\x00\x1f", "MiXeD", "_Col$_c_0", "_Tombstone$_x",
	"_Idx$_i_1", "DEFAULT", "1.5", "utf8mb4", "binary", "utf8mb4_bin", "PRIMARY", "ß", "K"}

func (r ptRand) str() string { return ptStrs[r.rng.IntN(len(ptStrs))] }

func (r ptRand) any(depth int) any {
	switch r.rng.IntN(9) {
	case 0:
		return nil
	case 1:
		return r.rng.IntN(2) == 0
	case 2:
		return []float64{0, 1, -1, 0.1, 1e21, 1e20, 1e-7, 1e-6, 123456789, 9007199254740993, 1.5e300, -2.5e-300, 0.000001234}[r.rng.IntN(13)]
	case 3, 4:
		return r.str()
	case 5:
		if depth > 1 {
			return nil
		}
		return []any{r.any(depth + 1), r.any(depth + 1)}
	case 6:
		if depth > 1 {
			return "x"
		}
		return map[string]any{r.str(): r.any(depth + 1), "k": r.any(depth + 1)}
	case 7:
		return int64(r.rng.IntN(1000) - 500)
	default:
		return "CURRENT_TIMESTAMP"
	}
}

func (r ptRand) fill(v reflect.Value, depth int) {
	t := v.Type()
	switch t {
	case reflect.TypeOf(pmodel.CIStr{}):
		if r.rng.IntN(5) == 0 {
			v.Set(reflect.ValueOf(pmodel.CIStr{O: r.str(), L: r.str()}))
		} else {
			v.Set(reflect.ValueOf(pmodel.NewCIStr(r.str())))
		}
		return
	case reflect.TypeOf(ptypes.FieldType{}):
		v.Set(reflect.ValueOf(*ptRandSpec(r.rng).build()))
		return
	case reflect.TypeOf(time.Time{}):
		switch r.rng.IntN(3) {
		case 0:
		case 1:
			v.Set(reflect.ValueOf(time.Unix(r.rng.Int64N(1<<34), r.rng.Int64N(1e9)).UTC()))
		default:
			v.Set(reflect.ValueOf(time.Unix(r.rng.Int64N(1<<34), 0).In(time.FixedZone("", (r.rng.IntN(48)-24)*1800))))
		}
		return
	case reflect.TypeOf(auth.UserIdentity{}):
		v.Set(reflect.ValueOf(auth.UserIdentity{Username: r.str(), Hostname: r.str(), CurrentUser: r.rng.IntN(2) == 0}))
		return
	}
	if t == reflect.TypeOf(mysql.SQLMode(0)) {
		v.SetInt(r.rng.Int64N(1 << 20))
		return
	}
	switch t.Kind() {
	case reflect.Bool:
		v.SetBool(r.rng.IntN(2) == 0)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		vals := []int64{0, 1, -1, 5, 100, int64(1)<<(bits-1) - 1, -(int64(1) << (bits - 1))}
		v.SetInt(vals[r.rng.IntN(len(vals))])
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		bits := t.Bits()
		vals := []uint64{0, 1, 2, 5, 255, uint64(1)<<(bits-1) + (uint64(1)<<(bits-1) - 1)}
		v.SetUint(vals[r.rng.IntN(len(vals))])
	case reflect.Float64:
		v.SetFloat([]float64{0, 0.5, 1e-7, 1e21, 0.99}[r.rng.IntN(5)])
	case reflect.String:
		v.SetString(r.str())
	case reflect.Interface:
		if a := r.any(0); a != nil {
			v.Set(reflect.ValueOf(a))
		}
	case reflect.Pointer:
		if depth > 4 || r.rng.IntN(3) == 0 {
			return
		}
		p := reflect.New(t.Elem())
		r.fill(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Slice:
		switch r.rng.IntN(4) {
		case 0:
			return
		case 1:
			v.Set(reflect.MakeSlice(t, 0, 0))
			return
		}
		if t.Elem().Kind() == reflect.Uint8 {
			b := make([]byte, r.rng.IntN(6))
			for i := range b {
				b[i] = byte(r.rng.IntN(256))
			}
			v.SetBytes(b)
			return
		}
		n := 1 + r.rng.IntN(3)
		if depth > 3 {
			n = 1
		}
		s := reflect.MakeSlice(t, n, n)
		for i := 0; i < n; i++ {
			if t.Elem().Kind() == reflect.Pointer {
				// No nil elements in []*T: the Rust port holds Vec<T> (a null element
				// decodes as the zero value there; see specs/D06.1-model "Deviations").
				p := reflect.New(t.Elem().Elem())
				r.fill(p.Elem(), depth+1)
				s.Index(i).Set(p)
			} else {
				r.fill(s.Index(i), depth+1)
			}
		}
		v.Set(s)
	case reflect.Map:
		if r.rng.IntN(3) == 0 {
			return
		}
		m := reflect.MakeMap(t)
		for i := 0; i < r.rng.IntN(4); i++ {
			k := reflect.New(t.Key()).Elem()
			if t.Key().Kind() == reflect.String {
				k.SetString(r.str())
			} else {
				k.SetInt([]int64{0, 1, 9, 10, 100, -1, -20}[r.rng.IntN(7)])
			}
			e := reflect.New(t.Elem()).Elem()
			r.fill(e, depth+1)
			m.SetMapIndex(k, e)
		}
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				r.fill(v.Field(i), depth+1)
			}
		}
	}
}

// ---------------------------------------------------------------- JSON mutation

// jnode is an ordered JSON tree (numbers kept as literals) for mutations.
type jnode struct {
	kind string // "obj", "arr", "lit"
	keys []string
	kids []*jnode
	lit  string
}

func parseJ(dec *json.Decoder) *jnode {
	tok, err := dec.Token()
	if err != nil {
		panic(err)
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			n := &jnode{kind: "obj"}
			for dec.More() {
				k, _ := dec.Token()
				n.keys = append(n.keys, k.(string))
				n.kids = append(n.kids, parseJ(dec))
			}
			dec.Token()
			return n
		}
		n := &jnode{kind: "arr"}
		for dec.More() {
			n.kids = append(n.kids, parseJ(dec))
		}
		dec.Token()
		return n
	default:
		b, _ := json.Marshal(t)
		if num, ok := t.(json.Number); ok {
			return &jnode{kind: "lit", lit: num.String()}
		}
		return &jnode{kind: "lit", lit: string(b)}
	}
}

func (n *jnode) write(b *bytes.Buffer) {
	switch n.kind {
	case "obj":
		b.WriteByte('{')
		for i, k := range n.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			n.kids[i].write(b)
		}
		b.WriteByte('}')
	case "arr":
		b.WriteByte('[')
		for i, k := range n.kids {
			if i > 0 {
				b.WriteByte(',')
			}
			k.write(b)
		}
		b.WriteByte(']')
	default:
		b.WriteString(n.lit)
	}
}

func (n *jnode) objects(out *[]*jnode) {
	if n.kind == "obj" {
		*out = append(*out, n)
	}
	for _, k := range n.kids {
		k.objects(out)
	}
}

var jWrong = []string{`"s"`, `1`, `-1`, `1.5`, `1e2`, `-0`, `true`, `null`, `[]`, `{}`, `[1]`, `{"O":"x"}`, `18446744073709551616`,
	`9223372036854775808`, `256`, `65536`, `"AAEC"`, `"!!"`, `[1,2,300]`, `1e400`, `"2006-01-02T15:04:05Z"`}

func mutateJSON(text string, rng *rand.Rand) string {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	root := parseJ(dec)
	var objs []*jnode
	root.objects(&objs)
	if len(objs) == 0 {
		return text
	}
	o := objs[rng.IntN(len(objs))]
	switch k := rng.IntN(6); {
	case k == 0 && len(o.keys) > 0: // drop a key
		i := rng.IntN(len(o.keys))
		o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
		o.kids = append(o.kids[:i:i], o.kids[i+1:]...)
	case k == 1 && len(o.keys) > 0: // re-case a key
		i := rng.IntN(len(o.keys))
		o.keys[i] = strings.ToUpper(o.keys[i])
	case k == 2: // unknown key
		o.keys = append(o.keys, "unknown_field")
		o.kids = append(o.kids, &jnode{kind: "lit", lit: jWrong[rng.IntN(len(jWrong))]})
	case k == 3 && len(o.keys) > 0: // duplicate a key with another value
		i := rng.IntN(len(o.keys))
		o.keys = append(o.keys, o.keys[i])
		o.kids = append(o.kids, &jnode{kind: "lit", lit: jWrong[rng.IntN(len(jWrong))]})
	case len(o.keys) > 0: // wrong value type
		i := rng.IntN(len(o.keys))
		o.kids[i] = &jnode{kind: "lit", lit: jWrong[rng.IntN(len(jWrong))]}
	}
	var b bytes.Buffer
	root.write(&b)
	return b.String()
}

// ---------------------------------------------------------------- generate

func dropNil[T any](s []*T) []*T {
	if s == nil {
		return nil
	}
	out := make([]*T, 0, len(s))
	for _, p := range s {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

// sanitizeTable drops nil elements and fixes the column offsets of a random table, so that the
// method ops reach past the first nil dereference.
func sanitizeTable(text string) string {
	t := mustTable(vec.GoString(text))
	t.Columns = dropNil(t.Columns)
	t.Indices = dropNil(t.Indices)
	for i, c := range t.Columns {
		c.Offset = i
	}
	for _, idx := range t.Indices {
		idx.Columns = dropNil(idx.Columns)
		idx.AffectColumn = dropNil(idx.AffectColumn)
		for _, ic := range idx.Columns {
			if len(t.Columns) == 0 {
				ic.Offset = 0
			} else {
				ic.Offset = (ic.Offset%len(t.Columns) + len(t.Columns)) % len(t.Columns)
			}
		}
	}
	t.ForeignKeys = dropNil(t.ForeignKeys)
	t.Constraints = dropNil(t.Constraints)
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type clusterRec struct {
	Type string `json:"type"`
	Src  string `json:"src"`
	JSON string `json:"json"`
}

func loadCluster() []clusterRec {
	zr, err := gzip.NewReader(bytes.NewReader(modelClusterGz))
	if err != nil {
		panic(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	var out []clusterRec
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r clusterRec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			panic(err)
		}
		out = append(out, r)
	}
	return out
}

func modelGenerate(g *vec.Gen, rng *rand.Rand) {
	cluster := loadCluster()
	var tables []string
	for _, c := range cluster {
		g.Add("cluster/"+c.Src, "roundtrip", c.Type, vec.GoString(c.JSON))
		if c.Type == "TableInfo" {
			tables = append(tables, c.JSON)
		}
	}
	r := ptRand{rng}
	names := make([]string, 0, len(modelTypes))
	for n := range modelTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	var randTexts [][2]string
	for i := 0; i < 15000; i++ {
		name := names[i%len(names)]
		v := modelTypes[name]()
		r.fill(reflect.ValueOf(v).Elem(), 0)
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		g.Add("rand/"+name, "roundtrip", name, vec.GoString(b))
		randTexts = append(randTexts, [2]string{name, string(b)})
	}
	// Mutations of the cluster and random texts.
	for i := 0; i < 15000; i++ {
		var name, text string
		if i%2 == 0 {
			c := cluster[rng.IntN(len(cluster))]
			name, text = c.Type, c.JSON
		} else {
			t := randTexts[rng.IntN(len(randTexts))]
			name, text = t[0], t[1]
		}
		g.Add("mut/"+name, "roundtrip", name, vec.GoString(mutateJSON(text, rng)))
	}
	for i := 0; i < 20000; i++ {
		var name, text string
		if i%2 == 0 {
			c := cluster[rng.IntN(len(cluster))]
			name, text = c.Type, c.JSON
		} else {
			t := randTexts[rng.IntN(len(randTexts))]
			name, text = t[0], t[1]
		}
		g.Add("lucky/"+name, "roundtrip", name, vec.GoString(luckyMutate(text, rng)))
	}
	// Decoding into an existing value.
	for i := 0; i < 3000; i++ {
		t := randTexts[rng.IntN(len(randTexts))]
		var other string
		for _, u := range randTexts {
			if u[0] == t[0] && u[1] != t[1] && rng.IntN(5) == 0 {
				other = u[1]
				break
			}
		}
		if other == "" {
			other = mutateJSON(t[1], rng)
		}
		g.Add("into/"+t[0], "into", t[0], vec.GoString(t[1]), vec.GoString(other))
	}
	for i := 0; i < 3; i++ {
		g.Add("sys/nan", "marshal_nan", vec.GoString(tables[0]), i)
	}
	// Methods on the cluster tables and random tables.
	var allTables []string
	allTables = append(allTables, tables...)
	for _, t := range randTexts {
		if t[0] == "TableInfo" {
			allTables = append(allTables, sanitizeTable(t[1]))
		}
	}
	for i, t := range allTables {
		tb := mustTable(vec.GoString(t))
		// The probe id is an argument of its own: an int64 through float64 would make
		// the harness itself depend on the target (M-F2I for MaxInt64).
		id := int64(1 + i%5)
		probe := map[string]any{"index": "primary", "col": "id", "constraint": "chk_b", "fk": "fk1",
			"from": float64(rng.IntN(5)), "to": float64(rng.IntN(5))}
		if len(tb.Indices) > 0 && rng.IntN(2) == 0 {
			probe["index"] = tb.Indices[rng.IntN(len(tb.Indices))].Name.L
		}
		if len(tb.Columns) > 0 {
			c := tb.Columns[rng.IntN(len(tb.Columns))]
			probe["col"] = c.Name.L
			id = c.ID
		}
		g.Add("table", "table", vec.GoString(t), probe, id)
		for _, c := range tb.Columns {
			if rng.IntN(3) == 0 {
				b, _ := json.Marshal(c)
				g.Add("column", "column", vec.GoString(b))
			}
		}
		if len(tb.Indices) > 0 {
			nm := "id"
			if len(tb.Indices[0].Columns) > 0 {
				nm = tb.Indices[0].Columns[0].Name.L
			}
			g.Add("index", "index", vec.GoString(t), nm)
		}
		if tb.Partition != nil {
			b, _ := json.Marshal(tb.Partition)
			for k := 0; k < 3; k++ {
				var id int64 = 1
				name := "p0"
				if len(tb.Partition.Definitions) > 0 {
					d := tb.Partition.Definitions[rng.IntN(len(tb.Partition.Definitions))]
					id, name = d.ID, d.Name.O
				}
				g.Add("partition", "partition", vec.GoString(b), id, name, rng.IntN(6)-1, rng.IntN(7))
			}
		}
		for _, fk := range tb.ForeignKeys {
			b, _ := json.Marshal(fk)
			g.Add("fk", "fk", vec.GoString(b), "d1", tb.Name.O)
			g.Add("fk", "fk", vec.GoString(b), fk.RefSchema.L, "t")
		}
		var cols []string
		for _, c := range tb.Columns {
			if rng.IntN(2) == 0 {
				cols = append(cols, c.Name.O)
			}
		}
		g.Add("misc/covered", "misc", "covered", vec.GoString(t), cols)
	}
	// Random partitions with DDL states (overlapping / dropping paths).
	for i := 0; i < 150; i++ {
		pi := new(model.PartitionInfo)
		r.fill(reflect.ValueOf(pi).Elem(), 0)
		pi.Type = []pmodel.PartitionType{pmodel.PartitionTypeRange, pmodel.PartitionTypeList, pmodel.PartitionTypeHash}[rng.IntN(3)]
		pi.DDLAction = []model.ActionType{model.ActionDropTablePartition, model.ActionTruncateTablePartition, model.ActionAddTablePartition, model.ActionNone}[rng.IntN(4)]
		pi.DDLState = model.SchemaState(rng.IntN(6))
		for k := range pi.Definitions {
			pi.Definitions[k].ID = int64(k + 1)
			if rng.IntN(3) == 0 {
				pi.Definitions[k].InValues = [][]string{{"DEFAULT"}}
			}
		}
		if len(pi.Definitions) > 0 && rng.IntN(2) == 0 {
			pi.DroppingDefinitions = []model.PartitionDefinition{pi.Definitions[rng.IntN(len(pi.Definitions))]}
		}
		b, _ := json.Marshal(pi)
		g.Add("rand/partition", "partition", vec.GoString(b), int64(1+rng.IntN(3)), "p0", rng.IntN(5)-1, rng.IntN(7))
	}
	// Columns: setters.
	for i := 0; i < 200; i++ {
		c := new(model.ColumnInfo)
		r.fill(reflect.ValueOf(c).Elem(), 0)
		if rng.IntN(2) == 0 {
			c.FieldType = *ptypes.NewFieldType(mysql.TypeBit)
		}
		b, _ := json.Marshal(c)
		g.Add("rand/column", "column", vec.GoString(b))
		kinds := []string{"nil", "str", "int", "float"}
		var steps [][]any
		for k := 0; k < 1+rng.IntN(4); k++ {
			switch rng.IntN(8) {
			case 0:
				steps = append(steps, []any{"type", []int{int(mysql.TypeBit), int(mysql.TypeLong), int(mysql.TypeVarchar)}[rng.IntN(3)]})
			case 1:
				steps = append(steps, []any{[]string{"flag", "add", "and", "toggle", "del"}[rng.IntN(5)], []int{0, 1, 32, 64, 128}[rng.IntN(5)]})
			case 2:
				steps = append(steps, []any{[]string{"flen", "decimal"}[rng.IntN(2)], rng.IntN(70) - 1})
			case 3:
				steps = append(steps, []any{[]string{"charset", "collate"}[rng.IntN(2)], r.str()})
			case 4:
				steps = append(steps, []any{"elems", vec.GoString("\x19\xb9")})
			default:
				kd := kinds[rng.IntN(len(kinds))]
				var val any = 0
				switch kd {
				case "str":
					val = vec.GoString([]string{"abc", "\x19\xb9", "", "b'101'"}[rng.IntN(4)])
				case "int":
					val = rng.IntN(100)
				case "float":
					val = 1.5
				}
				steps = append(steps, []any{[]string{"origin_default", "default"}[rng.IntN(2)], kd, val})
			}
		}
		g.Add("rand/column_set", "column_set", vec.GoString(b), steps)
	}
	// TestDefaultValue (column_test.go): a BIT column with a non-UTF-8 default.
	{
		c := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("newBitCol"), FieldType: *ptypes.NewFieldType(mysql.TypeBit)}
		b, _ := json.Marshal(c)
		g.Add("TestDefaultValue", "column_set", vec.GoString(b), [][]any{{"default", "str", vec.GoString("\x19\xb9")}, {"origin_default", "str", vec.GoString("\x19\xb9")}, {"default", "int", 1}})
	}
	// Placement / resource groups.
	for i := 0; i < 80; i++ {
		p := new(model.PolicyInfo)
		r.fill(reflect.ValueOf(p).Elem(), 0)
		b, _ := json.Marshal(p)
		g.Add("rand/placement", "placement", vec.GoString(b))
		rg := new(model.ResourceGroupInfo)
		r.fill(reflect.ValueOf(rg).Elem(), 0)
		if rg.ResourceGroupSettings != nil && rg.ResourceGroupSettings.Runaway != nil {
			rg.ResourceGroupSettings.Runaway.Action = pmodel.RunawayActionType(rng.IntN(5))
			rg.ResourceGroupSettings.Runaway.WatchType = pmodel.RunawayWatchType(rng.IntN(4))
		}
		b, _ = json.Marshal(rg)
		g.Add("rand/resource", "resource", vec.GoString(b))
	}
	for _, ps := range []model.PlacementSettings{
		{PrimaryRegion: "us-east-1", Regions: "us-east-1,us-east-2", Schedule: "EVEN"},
		{LeaderConstraints: "[+region=bj]"},
		{Voters: 1, VoterConstraints: "[+region=us-east-1]", Followers: 2, FollowerConstraints: "[+disk=ssd]", Learners: 3, LearnerConstraints: "[+region=us-east-2]"},
		{Voters: 3, Followers: 2, Learners: 1, Constraints: "{\"+us-east-1\":1,+us-east-2:1}"},
	} {
		b, _ := json.Marshal(&model.PolicyInfo{PlacementSettings: &ps, ID: 1, Name: pmodel.NewCIStr("p")})
		g.Add("TestPlacementSettingsString", "placement", vec.GoString(b))
	}
	// Misc.
	for _, s := range []string{"", "a", "_Tombstone$_a", "_Tombstone$", "x_Tombstone$_"} {
		g.Add("misc", "misc", "removing_name", s)
	}
	g.Add("misc", "misc", "stats_options")
	for w := 0; w < 6; w++ {
		g.Add("misc", "misc", "window", uint8(w))
	}
	for _, s := range []string{"", "1h", "2d", "1.5m", "x", "10", "0", "1h30m"} {
		g.Add("misc", "misc", "job_interval", s)
	}
	for _, s := range []string{"", "none", "TABLE", "partition", "bad", "Partition"} {
		g.Add("misc", "misc", "affinity", s)
	}
	for _, v := range [][4]any{{int64(1), int64(2), true, false}, {int64(-1), int64(0), false, true}} {
		g.Add("misc", "misc", "item_key", v[0], v[1], v[2], v[3])
	}
	g.Add("misc", "misc", "extra_cols")
	for _, ts := range []uint64{0, 1 << 18, 450000000000000000, 1<<63 + 5, 18446744073709551615} {
		g.Add("misc", "misc", "ts_time", ts)
	}
	for i := 0; i < 20; i++ {
		a, b := new(model.DBInfo), new(model.DBInfo)
		r.fill(reflect.ValueOf(a).Elem(), 0)
		r.fill(reflect.ValueOf(b).Elem(), 0)
		ab, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		g.Add("misc/db", "misc", "db", vec.GoString(ab), vec.GoString(bb))
	}
	// time.Time fields (StatsWindowSettings): Time.UnmarshalJSON is parseStrictRFC3339, whose
	// fallback is time.Parse(RFC3339) with the strict re-checks disabled in Go 1.25, and
	// Time.MarshalJSON's range errors.
	for _, ts := range []string{"2024-01-02T03:04:05Z", "2024-01-02T03:04:05.123456789Z", "2024-01-02T03:04:05.1234567891234Z",
		"2024-01-02T03:04:05.5+05:30", "2024-01-02T03:04:05-00:00", "2024-01-02T03:04:05+23:59", "2024-02-29T00:00:00Z",
		"2024-01-02T3:04:05Z", "2024-01-02T03:04:05,5Z", "2024-01-02T03:04:05+24:00", "2024-01-02T03:04:05-24:00",
		"2024-01-02T03:04:05+25:00", "2024-01-02T03:04:05+01:61", "2024-01-02T03:04:05+01:60", "2024-01-02T03:04:05+0100",
		"2024-01-02T03:04:05+01:00:00", "2024-01-02T03:04:05*01:00", "2024-01-02T03:04:05+0a:00", "2024-13-02T03:04:05Z",
		"2024-00-02T03:04:05Z", "2024-02-30T03:04:05Z", "2023-02-29T03:04:05Z", "2024-01-00T03:04:05Z", "2024-01-02T24:00:00Z",
		"2024-01-02T03:60:00Z", "2024-01-02T03:04:60Z", "2024-01-02 03:04:05Z", "2024-01-02T03:04:05", "2024-01-02T03:04",
		"", "s", "2024-01-02T03:04:05Zjunk", "99999-01-01T00:00:00Z", "0000-01-01T00:00:00Z", "日本-01-01T00:00:00Z",
		"2024-01-02T03:04:05.Z", "2024-1-02T03:04:05Z", "-024-01-02T03:04:05Z", "2024-01-02T03:04:05é", "2a24-01-02T03:04:05Z",
		"2024-01-02T03:04:05.1,2Z", "2024-01-02T03:04:05.1+05:30x", "2024-01-02T1:4:05Z", "2024-01-02t03:04:05Z",
		"2024-01-02T03:04:05z", "2024-01-02T03:04:05\x7fZ"} {
		// No JSON escapes in the times: Go parses the raw bytes between the quotes
		// (go.dev/issue/47353), the Rust port the decoded string (spec D06.1 §4).
		b, _ := json.Marshal(map[string]string{"window_start": ts})
		g.Add("sys/time", "roundtrip", "StatsOptions", vec.GoString(b))
	}
	for _, raw := range []string{`1`, `true`, `[]`, `{}`, `null`, `"2024-01-02T03:04:05Z"`} {
		g.Add("sys/time", "roundtrip", "StatsOptions", vec.GoString(`{"window_end":`+raw+`}`))
	}
	for _, c := range [][3]int64{{0, 0, 0}, {253402300799, 999999999, 0}, {253402300800, 0, 0}, {253402300799, 0, 3600},
		{-62135596800, 0, 0}, {-62135596801, 0, 0}, {-62135596800, 0, -60}, {1700000000, 1, 86400}, {1700000000, 1, -86399},
		{1700000000, 0, 23*3600 + 59*60}, {1700000000, 0, 24 * 3600}} {
		g.Add("sys/time", "marshal_time", c[0], c[1], c[2])
	}
	decodeSiteCases(g)
}

// decodeSiteCases: one "into" record per error / early-return site of encoding/json's
// decoder (Go 1.25 decode.go), on a base value with every field set, so that the record
// shows what Go writes, what it keeps and whether it goes on decoding: literalStore
// (null, bool, string, []byte base64, number into interface / int / uint / float and into
// wrong kinds), array (into non-slices, element errors, an UnmarshalJSON error in an
// element, shorter and longer arrays), object (into non-structs, maps: int keys, value
// errors before key errors), struct fields (unknown and duplicate keys, folded keys, an
// UnmarshalJSON error after a saved error, embedded pointers allocated for a promoted
// field), and the UnmarshalJSON methods (CIStr, FieldType, time.Time).
func decodeSiteCases(g *vec.Gen) {
	ft := ptypes.NewFieldType(mysql.TypeLong)
	ft.SetFlen(11)
	ct := ptypes.NewFieldType(mysql.TypeVarchar)
	col := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("Col"), Offset: 2, OriginDefaultValue: "x",
		OriginDefaultValueBit: []byte{1, 2}, DefaultValue: "CURRENT_TIMESTAMP", DefaultValueBit: []byte{1, 2},
		Dependences: map[string]struct{}{"a": {}}, FieldType: *ft, ChangingFieldType: ct, State: model.StatePublic,
		Comment: "cm", Hidden: true, ChangeStateInfo: &model.ChangeStateInfo{DependencyColumnOffset: 3}, Version: 2}
	cb, _ := json.Marshal(col)
	pi := &model.PartitionInfo{Type: pmodel.PartitionTypeRange, Expr: "a", Columns: []pmodel.CIStr{pmodel.NewCIStr("a"), pmodel.NewCIStr("b")},
		Enable: false, Definitions: []model.PartitionDefinition{{ID: 1, Name: pmodel.NewCIStr("p0"), LessThan: []string{"10"}},
			{ID: 2, Name: pmodel.NewCIStr("p1"), LessThan: []string{"MAXVALUE"}}},
		NewPartitionIDs: []int64{1, 2, 3}, DDLChangedIndex: map[int64]bool{1: true}}
	pb, _ := json.Marshal(pi)
	sd := &model.SchemaDiff{Version: 1, SubActionTypes: []model.ActionType{1, 2}}
	sb, _ := json.Marshal(sd)
	pol := &model.PolicyInfo{ID: 1, Name: pmodel.NewCIStr("p"), State: model.StatePublic}
	polb, _ := json.Marshal(pol)
	so := &model.StatsOptions{StatsWindowSettings: &model.StatsWindowSettings{
		WindowStart: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), WindowEnd: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)}, SampleNum: 7}
	sob, _ := json.Marshal(so)
	tb := &model.TableInfo{ID: 9, Name: pmodel.NewCIStr("t"), Columns: []*model.ColumnInfo{col}, Partition: pi, Comment: "tc"}
	tbb, _ := json.Marshal(tb)
	tbn := &model.TableInfo{ID: 9, Name: pmodel.NewCIStr("t")}
	tbnb, _ := json.Marshal(tbn)
	cases := []struct{ site, ty, base, text string }{
		// literalStore: null.
		{"null/primitives", "ColumnInfo", string(cb), `{"id":null,"offset":null,"comment":null,"hidden":null,"state":null,"version":null}`},
		{"null/interface", "ColumnInfo", string(cb), `{"default":null}`},
		{"null/pointer", "ColumnInfo", string(cb), `{"changing_type":null,"change_state_info":null}`},
		{"null/map", "ColumnInfo", string(cb), `{"dependences":null}`},
		{"null/bytes", "ColumnInfo", string(cb), `{"default_bit":null}`},
		{"null/unmarshaler", "ColumnInfo", string(cb), `{"name":null,"type":null}`},
		{"null/slice", "PartitionInfo", string(pb), `{"new_partition_ids":null,"definitions":null}`},
		// literalStore: bool.
		{"bool/wrong-kinds", "ColumnInfo", string(cb), `{"id":true,"comment":false,"dependences":true,"default_bit":true,"change_state_info":true,"version":7}`},
		{"bool/interface", "ColumnInfo", string(cb), `{"default":true,"origin_default":false}`},
		{"bool/unmarshaler", "ColumnInfo", string(cb), `{"comment":"new","name":true,"hidden":false}`},
		// literalStore: string.
		{"string/wrong-kinds", "ColumnInfo", string(cb), `{"id":"5","hidden":"true","dependences":"x","change_state_info":"x","version":"1"}`},
		{"string/slice", "PartitionInfo", string(pb), `{"new_partition_ids":"AQI=","enable":true}`},
		{"string/base64-bad", "ColumnInfo", string(cb), `{"default_bit":"!!","comment":"after"}`},
		{"string/base64-short", "ColumnInfo", string(cb), `{"default_bit":"AQ"}`},
		{"string/base64-newline", "ColumnInfo", string(cb), `{"default_bit":"AQ\n==","origin_default_bit":"A\rQI="}`},
		{"string/base64-empty", "ColumnInfo", string(cb), `{"default_bit":""}`},
		{"string/interface", "ColumnInfo", string(cb), `{"default":"s","origin_default":""}`},
		{"string/named-bytes", "SchemaDiff", string(sb), `{"sub_action_types":"AwQ="}`},
		// literalStore: number.
		{"number/interface-range", "ColumnInfo", string(cb), `{"default":1e400,"origin_default":-1e999,"comment":"after"}`},
		{"number/interface-nested", "ColumnInfo", string(cb), `{"default":[1,1e400,{"a":-1e999}],"origin_default":{"x":1e309}}`},
		{"number/interface-underflow", "ColumnInfo", string(cb), `{"default":1e-400,"origin_default":-0}`},
		{"number/wrong-kinds", "ColumnInfo", string(cb), `{"comment":5,"hidden":0,"dependences":5,"default_bit":5,"change_state_info":5}`},
		{"number/int-range", "ColumnInfo", string(cb), `{"offset":9223372036854775808,"state":256,"version":-1,"id":1.5}`},
		{"number/int-forms", "ColumnInfo", string(cb), `{"offset":1e2,"state":-0,"version":0.0,"id":-9223372036854775808}`},
		{"number/float", "StatsOptions", string(sob), `{"sample_rate":1e400,"sample_num":"x"}`},
		{"number/float-ok", "StatsOptions", string(sob), `{"sample_rate":5e-324}`},
		{"number/named-bytes", "SchemaDiff", string(sb), `{"sub_action_types":[3,300,4]}`},
		// array.
		{"array/wrong-kinds", "ColumnInfo", string(cb), `{"id":[1],"comment":[],"dependences":[],"change_state_info":[1],"default":[1,"a",null]}`},
		{"array/shorter", "PartitionInfo", string(pb), `{"new_partition_ids":[9]}`},
		{"array/longer", "PartitionInfo", string(pb), `{"new_partition_ids":[1,2,3,4,5]}`},
		{"array/empty", "PartitionInfo", string(pb), `{"new_partition_ids":[],"columns":[]}`},
		{"array/element-error", "PartitionInfo", string(pb), `{"new_partition_ids":[7,"x",8]}`},
		{"array/element-unmarshaler-error", "PartitionInfo", string(pb), `{"columns":[{"O":"n","L":"n"},5,{"O":"z"}],"enable":true}`},
		{"array/element-merge", "PartitionInfo", string(pb), `{"definitions":[{"comment":"c"}]}`},
		{"array/element-null-struct", "PartitionInfo", string(pb), `{"definitions":[null,{"id":5}]}`},
		{"array/pointer-elements", "TableInfo", string(tbb), `{"cols":[{"comment":"merged"}]}`},
		// object.
		{"object/wrong-kinds", "ColumnInfo", string(cb), `{"id":{},"comment":{"a":1},"default_bit":{},"default":{"k":[1]}}`},
		{"object/map-merge", "ColumnInfo", string(cb), `{"dependences":{"b":{},"a":{}}}`},
		{"object/map-value-error", "ColumnInfo", string(cb), `{"dependences":{"b":1,"c":{}}}`},
		{"object/map-int-keys", "PartitionInfo", string(pb), `{"ddl_changed_index":{"2":false,"x":true,"+3":true,"-0":true,"9223372036854775808":true,"007":true}}`},
		{"object/map-value-then-key", "PartitionInfo", string(pb), `{"ddl_changed_index":{"x":"s","4":"s"}}`},
		{"object/map-into-nil", "PartitionInfo", `{}`, `{"ddl_changed_index":{}}`},
		{"object/map-from-array", "PartitionInfo", string(pb), `{"ddl_changed_index":[1]}`},
		{"object/pointer-wrong-kind-existing", "TableInfo", string(tbb), `{"partition":"x","comment":"after"}`},
		{"object/pointer-wrong-kind-nil", "TableInfo", string(tbnb), `{"partition":"x","view":5}`},
		// struct fields.
		{"field/unknown", "ColumnInfo", string(cb), `{"nope":{"deep":[1,{"x":1e400}]},"comment":"after"}`},
		{"field/duplicate", "ColumnInfo", string(cb), `{"comment":"a","comment":"b","default":"x","default":1e400}`},
		{"field/fold", "ColumnInfo", string(cb), `{"COMMENT":"u","Comment":"v","ſtate":3}`},
		{"field/saved-then-fatal", "ColumnInfo", string(cb), `{"id":"x","name":5,"comment":"never"}`},
		{"field/embedded-pointer-null", "PolicyInfo", string(polb), `{"primary_region":null}`},
		{"field/embedded-pointer-error", "PolicyInfo", string(polb), `{"followers":"x","id":4}`},
		{"field/embedded-pointer-time", "StatsOptions", `{}`, `{"window_start":null}`},
		// UnmarshalJSON methods.
		{"unmarshaler/cistr-partial", "ColumnInfo", string(cb), `{"name":{"L":"zz","O":5}}`},
		{"unmarshaler/cistr-string", "ColumnInfo", string(cb), `{"name":"AbC"}`},
		{"unmarshaler/fieldtype-error", "ColumnInfo", string(cb), `{"type":{"Tp":"x"},"comment":"never"}`},
		{"unmarshaler/fieldtype-null", "ColumnInfo", string(cb), `{"changing_type":{"Tp":"x"}}`},
		{"unmarshaler/time-parse-error", "StatsOptions", string(sob), `{"window_start":"bad","sample_num":9}`},
		{"unmarshaler/time-not-string", "StatsOptions", string(sob), `{"window_end":5}`},
		{"unmarshaler/time-null", "StatsOptions", string(sob), `{"window_start":null}`},
		{"unmarshaler/time-saved-then-fatal", "StatsOptions", string(sob), `{"sample_num":"x","window_end":"bad","window_start":"2020-01-01T00:00:00Z"}`},
		{"unmarshaler/time-duplicate", "StatsOptions", string(sob), `{"window_start":"2222-06-23T01:50:06-09:00","window_start":"AAEC"}`},
	}
	for _, c := range cases {
		g.Add("site/"+c.site, "into", c.ty, vec.GoString(c.base), vec.GoString(c.text))
	}
}

var jLucky = []string{`1e21`, `1e20`, `999999999999999999999`, `1e-7`, `0.000001`, `1e-6`, `9007199254740993`, `-0`, `-0.0`, `1.0`, `1E+2`, `0.1`,
	`123456789.123456789`, `5e-324`, `1.7976931348623157e308`, `-1e21`, `100`, `"<a href='x'>&amp;</a>"`, `"line sep x"`, `"\ud800"`,
	`"😀"`, `"\^@"`, `"café"`, `"a\"b\\c/"`, `null`, `{}`, `[]`, `[null]`, `[null,{}]`, `true`, `"2026-11-07T1:2:3+24:00"`,
	`"2006-01-02T15:04:05.999999999+08:00"`, `"0001-01-01T00:00:00Z"`, `18446744073709551615`, `-9223372036854775808`, `"K"`, `""`, `"1e21"`}

var luckyIfaceKeys = map[string]bool{"default": true, "origin_default": true, "origin_default_bit": true, "default_bit": true}

func luckyMutate(text string, rng *rand.Rand) string {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	root := parseJ(dec)
	var all []*jnode
	var walk func(n *jnode)
	walk = func(n *jnode) {
		all = append(all, n)
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(root)
	for m := 1 + rng.IntN(3); m > 0; m-- {
		n := all[rng.IntN(len(all))]
		switch k := rng.IntN(5); {
		case k <= 1 && (n.kind == "obj" || n.kind == "arr") && len(n.kids) > 0:
			i := rng.IntN(len(n.kids))
			n.kids[i] = &jnode{kind: "lit", lit: jLucky[rng.IntN(len(jLucky))]}
		case k == 2 && n.kind == "obj":
			// interface{} fields: numbers / strings only
			for i, key := range n.keys {
				if luckyIfaceKeys[key] {
					n.kids[i] = &jnode{kind: "lit", lit: jLucky[rng.IntN(17)]}
				}
			}
			if len(n.keys) > 0 && rng.IntN(2) == 0 {
				n.keys = append(n.keys, "default")
				n.kids = append(n.kids, &jnode{kind: "lit", lit: jLucky[rng.IntN(len(jLucky))]})
			}
		case k == 3 && n.kind == "obj" && len(n.keys) > 0:
			i := rng.IntN(len(n.keys))
			key := []byte(n.keys[i])
			for j := range key {
				if rng.IntN(2) == 0 && key[j] >= 'a' && key[j] <= 'z' {
					key[j] -= 32
				}
			}
			n.keys[i] = strings.NewReplacer("k", "K", "s", "ſ").Replace(string(key))
		case n.kind == "obj" && len(n.keys) > 0:
			i := rng.IntN(len(n.keys))
			n.keys = append(n.keys, n.keys[i])
			n.kids = append(n.kids, &jnode{kind: "lit", lit: jLucky[rng.IntN(len(jLucky))]})
		}
	}
	var b bytes.Buffer
	root.write(&b)
	return b.String()
}
