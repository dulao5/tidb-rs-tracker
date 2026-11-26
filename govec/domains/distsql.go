package domains

// Domain "distsql": block D07.3 part 1 (pkg/distsql: request building, range encoding,
// Select / SelectWithRuntimeStats, TypeChunk / TypeDefault decoding, selectResultRuntimeStats)
// plus the D00.3 leaf utility files pkg/util/ppcpuusage/cpuusages.go and
// pkg/util/resourcegrouptag/resource_group_tag.go.
//
// Records:
//   - "split_int64": args [ranges, keepOrder, desc, isCommonHandle] ->
//     SplitRangesAcrossInt64Boundary, two lists of ranges in {l, h, le, he} form.
//   - "table_ranges": args [mode, tids, isCommonHandle, ranges, tzOffset, strict] ->
//     TableHandleRangesToKVRanges ("handle"), TableRangesToKVRanges ("table"),
//     CommonHandleRangesToKVRanges ("common"), or EncodeIndexKey ("index_key").
//   - "request_builder": args [spec] -> SetEncodeType + RequestBuilder setters + Build +
//     VerifyTxnScope + resourcegrouptag.GetResourceGroupLabelByKey + ResourceGroupTagger
//     (TableId / Label per D07.3 spec §6.2 and #416).
//   - "select_decode": args ["select", spec] -> Select / SelectWithRuntimeStats +
//     GetSelectResultConcurrency + Next / NextRaw + Close over an in-process mock kv.Client
//     (deterministic RespTime, no wall clock); or args ["stats", spec] ->
//     selectResultRuntimeStats.{mergeCopRuntimeStats,Clone,Merge,String,Tp,calcCacheHit}.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/config"
	"github.com/pingcap/tidb/pkg/ddl/placement"
	"github.com/pingcap/tidb/pkg/distsql"
	distsqlctx "github.com/pingcap/tidb/pkg/distsql/context"
	"github.com/pingcap/tidb/pkg/errctx"
	isctx "github.com/pingcap/tidb/pkg/infoschema/context"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/store/copr"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
	"github.com/pingcap/tidb/pkg/util/execdetails"
	"github.com/pingcap/tidb/pkg/util/memory"
	"github.com/pingcap/tidb/pkg/util/ppcpuusage"
	"github.com/pingcap/tidb/pkg/util/ranger"
	"github.com/pingcap/tidb/pkg/util/resourcegrouptag"
	"github.com/pingcap/tidb/pkg/util/sqlkiller"
	"github.com/pingcap/tipb/go-tipb"
	tikvkv "github.com/tikv/client-go/v2/kv"
	"github.com/tikv/client-go/v2/tikv"
	tikvutil "github.com/tikv/client-go/v2/util"
)

const distsqlSeed = 20261126

func init() {
	vec.Register(&vec.Domain{
		Name:     "distsql",
		Seed:     distsqlSeed,
		Coverage: distsqlCoverage,
		Blocks:   []string{"D07.3"},
		Files: []string{
			"pkg/util/ppcpuusage/cpuusages.go",
			"pkg/util/resourcegrouptag/resource_group_tag.go",
		},
		Exempt:   distsqlExempt(),
		Gzip:     true,
		Generate: distsqlGenerate,
		Exec:     distsqlExec,
	})
}

var distsqlCoverage = map[string]vec.Coverage{
	"split_int64": {
		Min:     150,
		GoFuncs: []string{"SplitRangesAcrossInt64Boundary"},
	},
	"table_ranges": {
		Min: 200,
		GoFuncs: []string{
			"TableHandleRangesToKVRanges",
			"TableRangesToKVRanges",
			"tablesRangesToKVRanges",
			"tableRangesToKVRangesWithoutSplit",
			"encodeHandleKey",
			"CommonHandleRangesToKVRanges",
			"EncodeIndexKey",
		},
	},
	"request_builder": {
		Min: 150,
		GoFuncs: []string{
			"SetEncodeType",
			"canUseChunkRPC",
			"checkAlignment",
			"setChunkMemoryLayout",
			"GetSystemEndian",
			"RequestBuilder.Build",
			"RequestBuilder.SetMemTracker",
			"RequestBuilder.SetTableRanges",
			"RequestBuilder.SetHandleRanges",
			"RequestBuilder.SetHandleRangesForTables",
			"RequestBuilder.SetDAGRequest",
			"RequestBuilder.SetKeyRanges",
			"RequestBuilder.SetKeyRangesWithHints",
			"RequestBuilder.SetPartitionKeyRanges",
			"RequestBuilder.SetStartTS",
			"RequestBuilder.SetDesc",
			"RequestBuilder.SetKeepOrder",
			"RequestBuilder.SetStoreType",
			"RequestBuilder.SetAllowBatchCop",
			"RequestBuilder.getIsolationLevel",
			"RequestBuilder.getKVPriority",
			"RequestBuilder.SetFromSessionVars",
			"RequestBuilder.SetPaging",
			"RequestBuilder.SetConcurrency",
			"RequestBuilder.SetCoprRequestRateLimit",
			"RequestBuilder.SetTiDBServerID",
			"RequestBuilder.SetFromInfoSchema",
			"RequestBuilder.SetResourceGroupTagger",
			"RequestBuilder.SetResourceGroupName",
			"RequestBuilder.SetExplicitRequestSourceType",
			"RequestBuilder.verifyTxnScope",
			"RequestBuilder.SetTxnScope",
			"RequestBuilder.SetReadReplicaScope",
			"RequestBuilder.SetIsStaleness",
			"RequestBuilder.SetClosestReplicaReadAdjuster",
			"RequestBuilder.SetConnIDAndConnAlias",
			"VerifyTxnScope",
			"pkg/util/resourcegrouptag/resource_group_tag.go:GetResourceGroupLabelByKey",
		},
	},
	"select_decode": {
		Min: 150,
		GoFuncs: []string{
			"Select",
			"SelectWithRuntimeStats",
			"GetSelectResultConcurrency",
			"selectResult.fetchResp",
			"selectResult.fetchRespWithIntermediateResults",
			"selectResult.Next",
			"selectResult.NextRaw",
			"selectResult.readFromDefault",
			"selectResult.readFromChunk",
			"selectResult.updateCopRuntimeStats",
			"selectResult.readRowsData",
			"selectResult.memConsume",
			"selectResult.Close",
			"selectResult.close",
			"selectResultRuntimeStats.mergeCopRuntimeStats",
			"selectResultRuntimeStats.Clone",
			"selectResultRuntimeStats.Merge",
			"selectResultRuntimeStats.String",
			"selectResultRuntimeStats.Tp",
			"selectResultRuntimeStats.calcCacheHit",
		},
	},
}

// ---------------------------------------------------------------- helpers

func dsqlRangeSpecOf(r *ranger.Range) map[string]any {
	l := make([]any, len(r.LowVal))
	for i := range r.LowVal {
		l[i] = dsqlDatumCompact(&r.LowVal[i])
	}
	h := make([]any, len(r.HighVal))
	for i := range r.HighVal {
		h[i] = dsqlDatumCompact(&r.HighVal[i])
	}
	return map[string]any{"l": l, "h": h, "le": r.LowExclude, "he": r.HighExclude}
}

func dsqlRangesOut(rs []*ranger.Range) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = dsqlRangeSpecOf(r)
	}
	return out
}

func dsqlDatumCompact(d *types.Datum) []any {
	switch d.Kind() {
	case types.KindNull, types.KindMinNotNull, types.KindMaxValue:
		return []any{d.Kind()}
	case types.KindInt64:
		return []any{d.Kind(), d.GetInt64()}
	case types.KindUint64:
		return []any{d.Kind(), d.GetUint64()}
	case types.KindFloat32:
		return []any{d.Kind(), math.Float64bits(float64(d.GetFloat32()))}
	case types.KindFloat64:
		return []any{d.Kind(), math.Float64bits(d.GetFloat64())}
	case types.KindString:
		return []any{d.Kind(), vec.GoString(d.GetString()), d.Collation()}
	case types.KindBytes:
		return []any{d.Kind(), hex.EncodeToString(d.GetBytes())}
	case types.KindMysqlDecimal:
		return []any{d.Kind(), d.GetMysqlDecimal().String()}
	case types.KindMysqlTime:
		return []any{d.Kind(), uint64(d.GetMysqlTime().CoreTime())}
	default:
		return []any{d.Kind(), d.String()}
	}
}

func dsqlLoc(offset int) *time.Location {
	if offset == 0 {
		return time.UTC
	}
	return time.FixedZone("Z", offset)
}

func dsqlErrCtx(strict bool) errctx.Context {
	if strict {
		return errctx.StrictNoWarningContext
	}
	return errctx.NewContextWithLevels(errctx.LevelMap{
		errctx.ErrGroupBadNull:  errctx.LevelIgnore,
		errctx.ErrGroupTruncate: errctx.LevelIgnore,
		errctx.ErrGroupNoDefault: errctx.LevelIgnore,
		errctx.ErrGroupDupKey:   errctx.LevelIgnore,
	}, contextutil.IgnoreWarn)
}

func dsqlFieldType(tp byte, flag uint, flen int, dec int, charset, coll string) *types.FieldType {
	ft := types.NewFieldType(tp)
	ft.SetFlag(flag)
	ft.SetFlen(flen)
	ft.SetDecimal(dec)
	ft.SetCharset(charset)
	ft.SetCollate(coll)
	return ft
}

// ---------------------------------------------------------------- mock InfoSchema for VerifyTxnScope

type dsqlNullIS struct {
	isctx.MetaOnlyInfoSchema
}

func (dsqlNullIS) SchemaMetaVersion() int64                              { return 1 }
func (dsqlNullIS) PlacementBundleByPhysicalTableID(int64) (*placement.Bundle, bool) { return nil, false }

// ---------------------------------------------------------------- mock Client / Response / ResultSubset for select_decode

type dsqlMockStatsSpec struct {
	Callee       string           `json:"callee"`
	RespNs       int64            `json:"respNs"`
	ProcKeys     int64            `json:"procKeys"`
	ProcNs       int64            `json:"procNs"`
	WaitNs       int64            `json:"waitNs"`
	CacheHit     bool             `json:"cacheHit"`
	BackoffKey   string           `json:"boKey,omitempty"`
	BackoffNs    int64            `json:"boNs,omitempty"`
	BackoffMap   map[string]int64 `json:"boMap,omitempty"`
	Summaries    [][3]uint64      `json:"summaries,omitempty"` // [timeNs, rows, iters]
}

type dsqlMockPacket struct {
	DataHex string             `json:"dataHex"`
	Stats   *dsqlMockStatsSpec `json:"stats,omitempty"`
	NextErr string             `json:"nextErr,omitempty"`
}

type dsqlMockSubset struct {
	data     []byte
	respTime time.Duration
	stats    *copr.CopRuntimeStats
}

func (s *dsqlMockSubset) GetData() []byte                               { return s.data }
func (s *dsqlMockSubset) GetStartKey() kv.Key                           { return nil }
func (s *dsqlMockSubset) MemSize() int64                                { return int64(len(s.data)) }
func (s *dsqlMockSubset) RespTime() time.Duration                       { return s.respTime }
func (s *dsqlMockSubset) GetCopRuntimeStats() *copr.CopRuntimeStats     { return s.stats }

type dsqlMockResp struct {
	packets         []dsqlMockPacket
	idx             int
	conc            int
	extraConc       int
	batchNum        uint64
	batchFallback   uint64
	buildElapsedNs  int64
	unconsumed      []*copr.CopRuntimeStats
}

func dsqlBuildCopStats(spec *dsqlMockStatsSpec) *copr.CopRuntimeStats {
	if spec == nil {
		return nil
	}
	cs := &copr.CopRuntimeStats{
		CoprCacheHit: spec.CacheHit,
		CopExecDetails: execdetails.CopExecDetails{
			CalleeAddress: spec.Callee,
			ScanDetail: tikvutil.ScanDetail{
				ProcessedKeys: spec.ProcKeys,
			},
			TimeDetail: tikvutil.TimeDetail{
				ProcessTime: time.Duration(spec.ProcNs),
				WaitTime:    time.Duration(spec.WaitNs),
			},
		},
	}
	if spec.BackoffKey != "" {
		cs.BackoffSleep = map[string]time.Duration{
			spec.BackoffKey: time.Duration(spec.BackoffNs),
		}
	} else if len(spec.BackoffMap) > 0 {
		cs.BackoffSleep = make(map[string]time.Duration, len(spec.BackoffMap))
		for k, v := range spec.BackoffMap {
			cs.BackoffSleep[k] = time.Duration(v)
		}
	}
	return cs
}

func (r *dsqlMockResp) Next(context.Context) (kv.ResultSubset, error) {
	if r.idx >= len(r.packets) {
		return nil, nil
	}
	p := r.packets[r.idx]
	r.idx++
	if p.NextErr != "" {
		return nil, fmt.Errorf("%s", p.NextErr)
	}
	data, err := hex.DecodeString(p.DataHex)
	must(err)
	var respTime time.Duration
	if p.Stats != nil {
		respTime = time.Duration(p.Stats.RespNs)
	}
	return &dsqlMockSubset{
		data:     data,
		respTime: respTime,
		stats:    dsqlBuildCopStats(p.Stats),
	}, nil
}

func (r *dsqlMockResp) Close() error                                         { return nil }
func (r *dsqlMockResp) GetConcurrency() (int, int)                           { return r.conc, r.extraConc }
func (r *dsqlMockResp) GetStoreBatchInfo() (uint64, uint64)                  { return r.batchNum, r.batchFallback }
func (r *dsqlMockResp) GetBuildTaskElapsed() time.Duration                   { return time.Duration(r.buildElapsedNs) }
func (r *dsqlMockResp) CollectUnconsumedCopRuntimeStats() []*copr.CopRuntimeStats { return r.unconsumed }

type dsqlMockClient struct {
	kv.Client
	resp kv.Response
}

func (c *dsqlMockClient) Send(context.Context, *kv.Request, any, *kv.ClientSendOption) kv.Response {
	return c.resp
}

// dsqlWarnRecorder collects warnings in order.
type dsqlWarnRecorder struct {
	warns []string
}

func (w *dsqlWarnRecorder) AppendWarning(err error) {
	if err != nil {
		w.warns = append(w.warns, err.Error())
	}
}
func (w *dsqlWarnRecorder) AppendNote(err error) {
	if err != nil {
		w.warns = append(w.warns, err.Error())
	}
}
func (w *dsqlWarnRecorder) WarningCount() int { return len(w.warns) }
func (w *dsqlWarnRecorder) TruncateWarnings(n int) []contextutil.SQLWarn {
	w.warns = w.warns[:n]
	return nil
}
func (w *dsqlWarnRecorder) CopyWarnings(dst []contextutil.SQLWarn) []contextutil.SQLWarn { return dst }
func (w *dsqlWarnRecorder) SetWarnings([]contextutil.SQLWarn)                            {}

// ---------------------------------------------------------------- request_builder spec

type dsqlExecSpec struct {
	Tp        int32   `json:"tp"`
	Limit     *uint64 `json:"limit,omitempty"`
	ParentIdx *uint32 `json:"parentIdx,omitempty"`
}

type dsqlDAGSpec struct {
	Execs      []dsqlExecSpec `json:"execs"`
	TzOffset   int64          `json:"tzOffset"`
	Flags      uint64         `json:"flags"`
	TzName     string         `json:"tzName"`
	UseSetEnc  bool           `json:"useSetEnc"`
}

type dsqlReqBuilderSpec struct {
	// DistSQLContext fields
	EnableChunkRPC         bool   `json:"enableChunkRPC"`
	DistSQLConcurrency     int    `json:"distSQLConc"`
	WeakConsistency        bool   `json:"weak"`
	RCCheckTS              bool   `json:"rcCheckTS"`
	NotFillCache           bool   `json:"notFillCache"`
	TaskID                 uint64 `json:"taskID"`
	Priority               int    `json:"priority"`
	ReplicaRead            uint8  `json:"replicaRead"`
	EnablePaging           bool   `json:"enablePaging"`
	MinPagingSize          int    `json:"minPaging"`
	MaxPagingSize          int    `json:"maxPaging"`
	StoreBatchSize         int    `json:"storeBatchSize"`
	ResourceGroupName      string `json:"rgName"`
	StoreBusyThresholdMs   int64  `json:"busyMs"`
	TiKVClientReadTimeout  uint64 `json:"readTimeout"`
	InRestrictedSQL        bool   `json:"restricted"`
	ReqSourceType          string `json:"reqSrc"`
	ExplicitReqSourceType  string `json:"explicitReqSrc"`
	ConnID                 uint64 `json:"connID"`
	ConnAlias              string `json:"connAlias"`
	// Builder setters
	RangeMode              string            `json:"rangeMode"` // "none","table","handle","handle_multi","key","key_hints","part_key"
	TableID                int64             `json:"tid"`
	TableIDs               []int64           `json:"tids,omitempty"`
	IsCommonHandle         bool              `json:"commonHandle"`
	Ranges                 []json.RawMessage `json:"ranges,omitempty"`
	RawKeys                [][2]string       `json:"rawKeys,omitempty"`
	Hints                  []int             `json:"hints,omitempty"`
	PartKeys               [][][2]string     `json:"partKeys,omitempty"`
	DAG                    *dsqlDAGSpec      `json:"dag,omitempty"`
	StartTS                uint64            `json:"startTS"`
	Desc                   bool              `json:"desc"`
	KeepOrder              bool              `json:"keepOrder"`
	StoreType              uint8             `json:"storeType"`
	AllowBatchCop          bool              `json:"allowBatchCop"`
	SetFromSession         bool              `json:"setFromSession"`
	OverridePaging         *bool             `json:"overridePaging,omitempty"`
	OverrideConc           *int              `json:"overrideConc,omitempty"`
	HasRateLimit           bool              `json:"hasRateLimit"`
	TiDBServerID           uint64            `json:"serverID"`
	OverrideRGName         *string           `json:"overrideRGName,omitempty"`
	OverrideExplicitSrc    *string           `json:"overrideExplicitSrc,omitempty"`
	TxnScope               string            `json:"txnScope"`
	ReadReplicaScope       string            `json:"readReplicaScope"`
	IsStaleness            bool              `json:"isStaleness"`
	HasAdjuster            bool              `json:"hasAdjuster"`
	HasMemTracker          bool              `json:"hasMemTracker"`
	SetConnID              bool              `json:"setConnID"`
	SetTagger              bool              `json:"setTagger"`
	TagKeyHex              string            `json:"tagKeyHex"`
}

// ---------------------------------------------------------------- select_decode specs

type dsqlColSpec struct {
	Tp      byte   `json:"tp"`
	Flag    uint   `json:"flag"`
	Flen    int    `json:"flen"`
	Dec     int    `json:"dec"`
	Charset string `json:"cs"`
	Collate string `json:"coll"`
}

type dsqlSelectSpec struct {
	Cols           []dsqlColSpec     `json:"cols"`
	Packets        []dsqlMockPacket  `json:"packets"`
	ReqRows        []int             `json:"reqRows"`
	RawMode        bool              `json:"rawMode"`
	WithStats      bool              `json:"withStats"`
	CopPlanIDs     []int             `json:"copPlanIDs,omitempty"`
	RootPlanID     int               `json:"rootPlanID"`
	Conc           int               `json:"conc"`
	ExtraConc      int               `json:"extraConc"`
	BatchNum       uint64            `json:"batchNum"`
	BatchFallback  uint64            `json:"batchFallback"`
	BuildElapsedNs int64             `json:"buildElapsedNs"`
	TzOffset       int               `json:"tzOffset"`
	UseMemTracker  bool              `json:"useMemTracker"`
	CacheEnabled   bool              `json:"cacheEnabled"`
	Unconsumed     []dsqlMockStatsSpec `json:"unconsumed,omitempty"`
}

type dsqlStatsEntrySpec struct {
	Stats  dsqlMockStatsSpec `json:"stats"`
	RespNs int64             `json:"respNs"`
}

type dsqlStatsOpSpec struct {
	A            []dsqlStatsEntrySpec `json:"a"`
	B            []dsqlStatsEntrySpec `json:"b"`
	ConcA        int                  `json:"concA"`
	ExtraA       int                  `json:"extraA"`
	BatchA       uint64               `json:"batchA"`
	FallbackA    uint64               `json:"fallbackA"`
	BuildNsA     int64                `json:"buildNsA"`
	ConcB        int                  `json:"concB"`
	ExtraB       int                  `json:"extraB"`
	BatchB       uint64               `json:"batchB"`
	FallbackB    uint64               `json:"fallbackB"`
	BuildNsB     int64                `json:"buildNsB"`
	CacheEnabled bool                 `json:"cacheEnabled"`
}

// ---------------------------------------------------------------- exec

func distsqlExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "split_int64":
		rs := rangesIn(args[0])
		keepOrder := arg[bool](args, 1)
		desc := arg[bool](args, 2)
		isCommon := arg[bool](args, 3)
		r1, r2 := distsql.SplitRangesAcrossInt64Boundary(rs, keepOrder, desc, isCommon)
		return []any{dsqlRangesOut(r1), dsqlRangesOut(r2)}, nil

	case "table_ranges":
		mode := arg[string](args, 0)
		tids := arg[[]int64](args, 1)
		isCommon := arg[bool](args, 2)
		rs := rangesIn(args[3])
		tzOffset := arg[int](args, 4)
		strict := arg[bool](args, 5)
		dctx := &distsqlctx.DistSQLContext{
			Location: dsqlLoc(tzOffset),
			ErrCtx:   dsqlErrCtx(strict),
		}
		switch mode {
		case "handle":
			krs, err := distsql.TableHandleRangesToKVRanges(dctx, tids, isCommon, rs)
			if err != nil {
				return map[string]any{"err": err.Error()}, nil
			}
			return dsqlKeyRangesOut(krs), nil
		case "table":
			krs := distsql.TableRangesToKVRanges(tids[0], rs)
			return map[string]any{"ranges": coprRangesOut(krs)}, nil
		case "common":
			krs, err := distsql.CommonHandleRangesToKVRanges(dctx, tids, rs)
			if err != nil {
				return map[string]any{"err": err.Error()}, nil
			}
			return dsqlKeyRangesOut(krs), nil
		case "index_key":
			low, high, err := distsql.EncodeIndexKey(dctx, rs[0])
			if err != nil {
				return map[string]any{"err": err.Error()}, nil
			}
			return map[string]any{"low": hex.EncodeToString(low), "high": hex.EncodeToString(high)}, nil
		default:
			panic("table_ranges: unknown mode " + mode)
		}

	case "request_builder":
		var spec dsqlReqBuilderSpec
		must(json.Unmarshal(args[0], &spec))
		return dsqlExecRequestBuilder(&spec), nil

	case "select_decode":
		sub := arg[string](args, 0)
		switch sub {
		case "select":
			var spec dsqlSelectSpec
			must(json.Unmarshal(args[1], &spec))
			return dsqlExecSelect(&spec), nil
		case "stats":
			var spec dsqlStatsOpSpec
			must(json.Unmarshal(args[1], &spec))
			return dsqlExecStats(&spec), nil
		default:
			panic("select_decode: unknown sub " + sub)
		}

	default:
		panic("distsql: unknown op " + op)
	}
}

func dsqlKeyRangesOut(krs *kv.KeyRanges) map[string]any {
	if krs == nil {
		return map[string]any{"partNum": 0, "ranges": [][2]string{}}
	}
	var flat [][2]string
	krs.ForEachPartition(func(rs []kv.KeyRange) {
		flat = append(flat, coprRangesOut(rs)...)
	})
	if flat == nil {
		flat = [][2]string{}
	}
	return map[string]any{
		"partNum": krs.PartitionNum(),
		"ranges":  flat,
	}
}

func dsqlExecRequestBuilder(spec *dsqlReqBuilderSpec) any {
	dctx := &distsqlctx.DistSQLContext{
		Location:                      time.UTC,
		ErrCtx:                        errctx.StrictNoWarningContext,
		EnableChunkRPC:                spec.EnableChunkRPC,
		DistSQLConcurrency:            spec.DistSQLConcurrency,
		WeakConsistency:               spec.WeakConsistency,
		RCCheckTS:                     spec.RCCheckTS,
		NotFillCache:                  spec.NotFillCache,
		TaskID:                        spec.TaskID,
		Priority:                      mysql.PriorityEnum(spec.Priority),
		ReplicaReadType:               kv.ReplicaReadType(spec.ReplicaRead),
		EnablePaging:                  spec.EnablePaging,
		MinPagingSize:                 spec.MinPagingSize,
		MaxPagingSize:                 spec.MaxPagingSize,
		StoreBatchSize:                spec.StoreBatchSize,
		ResourceGroupName:             spec.ResourceGroupName,
		LoadBasedReplicaReadThreshold: time.Duration(spec.StoreBusyThresholdMs) * time.Millisecond,
		TiKVClientReadTimeout:         spec.TiKVClientReadTimeout,
		InRestrictedSQL:               spec.InRestrictedSQL,
		RequestSourceType:             spec.ReqSourceType,
		ExplicitRequestSourceType:     spec.ExplicitReqSourceType,
		ConnectionID:                  spec.ConnID,
		SessionAlias:                  spec.ConnAlias,
	}
	if spec.SetTagger {
		dctx.ResourceGroupTagger = kv.NewResourceGroupTagBuilder()
	}

	var b distsql.RequestBuilder
	if spec.HasMemTracker {
		b.SetMemTracker(memory.NewTracker(-1, -1))
	}
	switch spec.RangeMode {
	case "table":
		rs := make([]*ranger.Range, len(spec.Ranges))
		for i, raw := range spec.Ranges {
			rs[i] = rangeIn(raw)
		}
		b.SetTableRanges(spec.TableID, rs)
	case "handle":
		rs := make([]*ranger.Range, len(spec.Ranges))
		for i, raw := range spec.Ranges {
			rs[i] = rangeIn(raw)
		}
		b.SetHandleRanges(dctx, spec.TableID, spec.IsCommonHandle, rs)
	case "handle_multi":
		rs := make([]*ranger.Range, len(spec.Ranges))
		for i, raw := range spec.Ranges {
			rs[i] = rangeIn(raw)
		}
		b.SetHandleRangesForTables(dctx, spec.TableIDs, spec.IsCommonHandle, rs)
	case "key":
		b.SetKeyRanges(coprRanges(spec.RawKeys))
	case "key_hints":
		b.SetKeyRangesWithHints(coprRanges(spec.RawKeys), spec.Hints)
	case "part_key":
		parts := make([][]kv.KeyRange, len(spec.PartKeys))
		for i, p := range spec.PartKeys {
			parts[i] = coprRanges(p)
		}
		b.SetPartitionKeyRanges(parts)
	}

	if spec.DAG != nil {
		dag := &tipb.DAGRequest{
			TimeZoneOffset: spec.DAG.TzOffset,
			Flags:          spec.DAG.Flags,
			TimeZoneName:   spec.DAG.TzName,
		}
		for _, es := range spec.DAG.Execs {
			e := &tipb.Executor{Tp: tipb.ExecType(es.Tp)}
			if es.Limit != nil {
				e.Limit = &tipb.Limit{Limit: *es.Limit}
			}
			if es.ParentIdx != nil {
				p := *es.ParentIdx
				e.ParentIdx = &p
			}
			dag.Executors = append(dag.Executors, e)
		}
		if spec.DAG.UseSetEnc {
			distsql.SetEncodeType(dctx, dag)
		}
		b.SetDAGRequest(dag)
	}

	b.SetStartTS(spec.StartTS)
	b.SetDesc(spec.Desc)
	b.SetKeepOrder(spec.KeepOrder)
	b.SetStoreType(kv.StoreType(spec.StoreType))
	b.SetAllowBatchCop(spec.AllowBatchCop)
	if spec.SetFromSession {
		b.SetFromSessionVars(dctx)
	}
	if spec.OverridePaging != nil {
		b.SetPaging(*spec.OverridePaging)
	}
	if spec.OverrideConc != nil {
		b.SetConcurrency(*spec.OverrideConc)
	}
	if spec.HasRateLimit {
		b.SetCoprRequestRateLimit(nil)
	}
	if spec.TiDBServerID != 0 {
		b.SetTiDBServerID(spec.TiDBServerID)
	}
	if spec.OverrideRGName != nil {
		b.SetResourceGroupName(*spec.OverrideRGName)
	}
	if spec.OverrideExplicitSrc != nil {
		b.SetExplicitRequestSourceType(*spec.OverrideExplicitSrc)
	}
	if spec.SetTagger && !spec.SetFromSession {
		b.SetResourceGroupTagger(dctx.ResourceGroupTagger)
	}
	if spec.TxnScope != "" {
		b.SetTxnScope(spec.TxnScope)
	}
	if spec.ReadReplicaScope != "" {
		b.SetReadReplicaScope(spec.ReadReplicaScope)
	}
	if spec.IsStaleness {
		b.SetIsStaleness(true)
	}
	if spec.HasAdjuster {
		b.SetClosestReplicaReadAdjuster(func(*kv.Request, int) bool { return false })
	}
	if spec.SetConnID {
		b.SetConnIDAndConnAlias(spec.ConnID, spec.ConnAlias)
	}
	b.SetFromInfoSchema(dsqlNullIS{})

	req, err := b.Build()
	if err != nil {
		return map[string]any{"err": err.Error()}
	}

	labels := make([][2]string, 0, len(req.MatchStoreLabels))
	for _, l := range req.MatchStoreLabels {
		labels = append(labels, [2]string{l.GetKey(), l.GetValue()})
	}

	var tagOut any
	if req.ResourceGroupTagger != nil && spec.TagKeyHex != "" {
		tk := coprUnhex(spec.TagKeyHex)
		rawTag := req.ResourceGroupTagger.EncodeTagWithKey(tk)
		var pbTag tipb.ResourceGroupTag
		must(pbTag.Unmarshal(rawTag))
		// Per D07.3 spec §6.2 & #416: compare TableId and Label (digests empty until D02.2).
		tagOut = []any{pbTag.GetTableId(), int32(pbTag.GetLabel())}
	}
	labelByKey := int32(resourcegrouptag.GetResourceGroupLabelByKey(coprUnhex(spec.TagKeyHex)))
	verifyGlobal := distsql.VerifyTxnScope(kv.GlobalTxnScope, spec.TableID, nil)

	return map[string]any{
		"tp":               req.Tp,
		"startTS":          req.StartTs,
		"dataHex":          hex.EncodeToString(req.Data),
		"conc":             req.Concurrency,
		"keepOrder":        req.KeepOrder,
		"desc":             req.Desc,
		"notFillCache":     req.NotFillCache,
		"cacheable":        req.Cacheable,
		"isoLevel":         int(req.IsolationLevel),
		"priority":         req.Priority,
		"replicaRead":      uint8(req.ReplicaRead),
		"storeType":        uint8(req.StoreType),
		"allowBatchCop":    req.BatchCop,
		"paging":           []any{req.Paging.Enable, req.Paging.MinPagingSize, req.Paging.MaxPagingSize},
		"storeBatchSize":   req.StoreBatchSize,
		"busyMs":           req.StoreBusyThreshold.Milliseconds(),
		"readTimeout":      req.TiKVClientReadTimeout,
		"rgName":           req.ResourceGroupName,
		"txnScope":         req.TxnScope,
		"readReplicaScope": req.ReadReplicaScope,
		"isStaleness":      req.IsStaleness,
		"matchLabels":      labels,
		"kr":               dsqlKeyRangesOut(req.KeyRanges),
		"limitSize":        req.LimitSize,
		"serverID":         req.TiDBServerID,
		"connID":           req.ConnID,
		"connAlias":        req.ConnAlias,
		"reqSrc":           []any{req.RequestSource.RequestSourceInternal, req.RequestSource.RequestSourceType, req.RequestSource.ExplicitRequestSourceType},
		"tag":              tagOut,
		"labelByKey":       labelByKey,
		"verifyGlobal":     verifyGlobal,
		"endian":           int32(distsql.GetSystemEndian()),
	}
}

func dsqlSetCoprCacheCapacity(enabled bool) func() {
	old := config.GetGlobalConfig().TiKVClient.CoprCache.CapacityMB
	cfg := *config.GetGlobalConfig()
	if enabled {
		cfg.TiKVClient.CoprCache.CapacityMB = 1000.0
	} else {
		cfg.TiKVClient.CoprCache.CapacityMB = 0.0
	}
	config.StoreGlobalConfig(&cfg)
	return func() {
		cfg2 := *config.GetGlobalConfig()
		cfg2.TiKVClient.CoprCache.CapacityMB = old
		config.StoreGlobalConfig(&cfg2)
	}
}

func dsqlExecSelect(spec *dsqlSelectSpec) any {
	restore := dsqlSetCoprCacheCapacity(spec.CacheEnabled)
	defer restore()

	fts := make([]*types.FieldType, len(spec.Cols))
	for i, c := range spec.Cols {
		fts[i] = dsqlFieldType(c.Tp, c.Flag, c.Flen, c.Dec, c.Charset, c.Collate)
	}
	unconsumed := make([]*copr.CopRuntimeStats, len(spec.Unconsumed))
	for i := range spec.Unconsumed {
		unconsumed[i] = dsqlBuildCopStats(&spec.Unconsumed[i])
	}
	resp := &dsqlMockResp{
		packets:        spec.Packets,
		conc:           spec.Conc,
		extraConc:      spec.ExtraConc,
		batchNum:       spec.BatchNum,
		batchFallback:  spec.BatchFallback,
		buildElapsedNs: spec.BuildElapsedNs,
		unconsumed:     unconsumed,
	}
	client := &dsqlMockClient{resp: resp}
	warns := &dsqlWarnRecorder{}
	var tracker *memory.Tracker
	if spec.UseMemTracker {
		tracker = memory.NewTracker(-1, -1)
	}
	var coll *execdetails.RuntimeStatsColl
	if spec.WithStats {
		coll = execdetails.NewRuntimeStatsColl(nil)
	}
	dctx := &distsqlctx.DistSQLContext{
		Client:            client,
		WarnHandler:       warns,
		Location:          dsqlLoc(spec.TzOffset),
		ErrCtx:            errctx.StrictNoWarningContext,
		SessionMemTracker: tracker,
		RuntimeStatsColl:  coll,
		SQLKiller:         &sqlkiller.SQLKiller{},
		CPUUsage:          &ppcpuusage.SQLCPUUsages{},
		ExecDetails:       &execdetails.SyncExecDetails{},
		KVVars:            tikvkv.NewVariables(nil),
	}
	kvReq := &kv.Request{
		Tp:          kv.ReqTypeDAG,
		StoreType:   kv.TiKV,
		Concurrency: spec.Conc,
		MemTracker:  tracker,
	}

	var (
		sr  distsql.SelectResult
		err error
	)
	if spec.WithStats {
		sr, err = distsql.SelectWithRuntimeStats(context.Background(), dctx, kvReq, fts, spec.CopPlanIDs, spec.RootPlanID)
	} else {
		sr, err = distsql.Select(context.Background(), dctx, kvReq, fts)
	}
	if err != nil {
		return map[string]any{"err": err.Error()}
	}

	c1, c2, ok := distsql.GetSelectResultConcurrency(sr)
	concOut := []any{c1, c2, ok}

	if spec.RawMode {
		var rawChunks []string
		var rawErr string
		for {
			b, e := sr.NextRaw(context.Background())
			if e != nil {
				rawErr = e.Error()
				break
			}
			if b == nil {
				break
			}
			rawChunks = append(rawChunks, hex.EncodeToString(b))
		}
		_ = sr.Close()
		var memAfter int64
		if tracker != nil {
			memAfter = tracker.BytesConsumed()
		}
		return map[string]any{
			"conc":     concOut,
			"raw":      rawChunks,
			"err":      rawErr,
			"warns":    warns.warns,
			"memAfter": memAfter,
		}
	}

	var batches [][]string
	var nextErr string
	step := 0
	for {
		reqRows := 32
		if len(spec.ReqRows) > 0 {
			if step < len(spec.ReqRows) {
				reqRows = spec.ReqRows[step]
			} else {
				reqRows = spec.ReqRows[len(spec.ReqRows)-1]
			}
		}
		step++
		chk := chunk.NewChunkWithCapacity(fts, 32)
		chk.SetRequiredRows(reqRows, 1024)
		e := sr.Next(context.Background(), chk)
		if e != nil {
			nextErr = e.Error()
			break
		}
		if chk.NumRows() == 0 {
			break
		}
		rows := make([]string, chk.NumRows())
		for r := 0; r < chk.NumRows(); r++ {
			rows[r] = chk.GetRow(r).ToString(fts)
		}
		batches = append(batches, rows)
		if step > 200 {
			panic("select_decode: runaway Next loop")
		}
	}
	_ = sr.Close()
	var memAfter int64
	if tracker != nil {
		memAfter = tracker.BytesConsumed()
	}
	var rootStatsStr string
	var copStatsStrs []string
	if coll != nil && spec.RootPlanID > 0 {
		if rs := coll.GetRootStats(spec.RootPlanID); rs != nil {
			rootStatsStr = rs.String()
		}
		for _, pid := range spec.CopPlanIDs {
			if cs := coll.GetCopStats(pid); cs != nil {
				copStatsStrs = append(copStatsStrs, cs.String())
			} else {
				copStatsStrs = append(copStatsStrs, "")
			}
		}
	}
	return map[string]any{
		"conc":      concOut,
		"batches":   batches,
		"err":       nextErr,
		"warns":     warns.warns,
		"memAfter":  memAfter,
		"rootStats": rootStatsStr,
		"copStats":  copStatsStrs,
		"tikvCPU":   int64(dctx.CPUUsage.GetCPUUsages().TikvCPUTime),
	}
}

//go:linkname dsqlMergeCopRuntimeStats github.com/pingcap/tidb/pkg/distsql.(*selectResultRuntimeStats).mergeCopRuntimeStats
func dsqlMergeCopRuntimeStats(s unsafe.Pointer, copStats *copr.CopRuntimeStats, respTime time.Duration)

// dsqlStatsType reaches unexported *distsql.selectResultRuntimeStats via a helper SelectWithRuntimeStats run.
var dsqlStatsType = func() reflect.Type {
	resp := &dsqlMockResp{
		packets: []dsqlMockPacket{{
			DataHex: hex.EncodeToString(func() []byte {
				sr := &tipb.SelectResponse{}
				b, _ := sr.Marshal()
				return b
			}()),
			Stats: &dsqlMockStatsSpec{Callee: "127.0.0.1:20160", RespNs: 1000},
		}},
		conc: 1,
	}
	coll := execdetails.NewRuntimeStatsColl(nil)
	dctx := &distsqlctx.DistSQLContext{
		Client:           &dsqlMockClient{resp: resp},
		WarnHandler:      &dsqlWarnRecorder{},
		Location:         time.UTC,
		ErrCtx:           errctx.StrictNoWarningContext,
		RuntimeStatsColl: coll,
		SQLKiller:        &sqlkiller.SQLKiller{},
		CPUUsage:         &ppcpuusage.SQLCPUUsages{},
		ExecDetails:      &execdetails.SyncExecDetails{},
		KVVars:           tikvkv.NewVariables(nil),
	}
	sr, err := distsql.SelectWithRuntimeStats(context.Background(), dctx, &kv.Request{Tp: kv.ReqTypeDAG, StoreType: kv.TiKV}, nil, nil, 1)
	must(err)
	chk := chunk.NewChunkWithCapacity(nil, 1)
	_ = sr.Next(context.Background(), chk)
	_ = sr.Close()
	rs := coll.GetRootStats(1)
	// RootRuntimeStats holds `r.groupRss []RuntimeStats`
	v := reflect.ValueOf(rs).Elem().FieldByName("groupRss")
	return v.Index(0).Elem().Type().Elem()
}()

func dsqlMakeStats(entries []dsqlStatsEntrySpec, conc, extra int, batch, fallback uint64, buildNs int64) execdetails.RuntimeStats {
	v := reflect.New(dsqlStatsType)
	p := unsafe.Pointer(v.Pointer())
	el := v.Elem()
	coprField(el, "reqStat").Set(reflect.ValueOf(tikv.NewRegionRequestRuntimeStats()))
	coprField(el, "distSQLConcurrency").SetInt(int64(conc))
	coprField(el, "extraConcurrency").SetInt(int64(extra))
	coprField(el, "storeBatchedNum").SetUint(batch)
	coprField(el, "storeBatchedFallbackNum").SetUint(fallback)
	coprField(el, "buildTaskDuration").SetInt(buildNs)
	for i := range entries {
		cs := dsqlBuildCopStats(&entries[i].Stats)
		dsqlMergeCopRuntimeStats(p, cs, time.Duration(entries[i].RespNs))
	}
	return v.Interface().(execdetails.RuntimeStats)
}

func dsqlExecStats(spec *dsqlStatsOpSpec) any {
	restore := dsqlSetCoprCacheCapacity(spec.CacheEnabled)
	defer restore()

	a := dsqlMakeStats(spec.A, spec.ConcA, spec.ExtraA, spec.BatchA, spec.FallbackA, spec.BuildNsA)
	b := dsqlMakeStats(spec.B, spec.ConcB, spec.ExtraB, spec.BatchB, spec.FallbackB, spec.BuildNsB)
	aStr := a.String()
	aTp := a.Tp()
	c := a.Clone()
	cStrBefore := c.String()
	a.Merge(b)
	aStrAfter := a.String()
	cStrAfter := c.String()
	return map[string]any{
		"a":        aStr,
		"tp":       aTp,
		"clone0":   cStrBefore,
		"merged":   aStrAfter,
		"clone1":   cStrAfter,
	}
}

// ---------------------------------------------------------------- generate

func dsqlEncodePacket(cols []dsqlColSpec, rows [][]types.Datum, encType tipb.EncodeType, tzOffset int, selErr *tipb.Error, warns []*tipb.Error, summaries [][3]uint64, chunkSizes []int) string {
	fts := make([]*types.FieldType, len(cols))
	for i, c := range cols {
		fts[i] = dsqlFieldType(c.Tp, c.Flag, c.Flen, c.Dec, c.Charset, c.Collate)
	}
	sr := &tipb.SelectResponse{
		EncodeType: encType,
		Error:      selErr,
		Warnings:   warns,
	}
	for _, s := range summaries {
		tp, nr, ni := s[0], s[1], s[2]
		sr.ExecutionSummaries = append(sr.ExecutionSummaries, &tipb.ExecutorExecutionSummary{
			TimeProcessedNs: &tp,
			NumProducedRows: &nr,
			NumIterations:   &ni,
		})
	}
	if len(rows) > 0 {
		if len(chunkSizes) == 0 {
			chunkSizes = []int{len(rows)}
		}
		pos := 0
		for _, sz := range chunkSizes {
			if pos >= len(rows) {
				break
			}
			end := pos + sz
			if end > len(rows) {
				end = len(rows)
			}
			sub := rows[pos:end]
			pos = end
			if encType == tipb.EncodeType_TypeChunk {
				chk := chunk.NewChunkWithCapacity(fts, len(sub))
				for _, row := range sub {
					for ci, d := range row {
						chk.AppendDatum(ci, &d)
					}
				}
				cdc := chunk.NewCodec(fts)
				sr.Chunks = append(sr.Chunks, tipb.Chunk{RowsData: cdc.Encode(chk)})
			} else {
				loc := dsqlLoc(tzOffset)
				var buf []byte
				for _, row := range sub {
					var err error
					buf, err = codec.EncodeValue(loc, buf, row...)
					must(err)
				}
				sr.Chunks = append(sr.Chunks, tipb.Chunk{RowsData: buf})
			}
		}
	}
	b, err := sr.Marshal()
	must(err)
	return hex.EncodeToString(b)
}

func dsqlIntDatum(v int64) []any   { return []any{types.KindInt64, v} }
func dsqlUintDatum(v uint64) []any { return []any{types.KindUint64, v} }
func dsqlNullDatum() []any         { return []any{types.KindNull} }
func dsqlMinNotNull() []any        { return []any{types.KindMinNotNull} }
func dsqlMaxVal() []any            { return []any{types.KindMaxValue} }
func dsqlStrDatum(s, coll string) []any {
	return []any{types.KindString, s, coll}
}
func dsqlDecDatum(s string) []any { return []any{types.KindMysqlDecimal, s} }

func dsqlMakeRange(l, h []any, colls []string, le, he bool) map[string]any {
	return map[string]any{"l": l, "h": h, "c": colls, "le": le, "he": he}
}

func distsqlGenerate(g *vec.Gen, rng *rand.Rand) {
	emit := func(op string, args ...any) { g.Add("", op, args...) }
	// 1. split_int64 (target >= 150)
	// Edge cases: empty, signed, unsigned crossing MaxInt64, open/closed boundaries, common handle.
	maxI := uint64(math.MaxInt64)
	splitBoundaryCases := [][]any{
		{},
		{dsqlMakeRange([]any{dsqlIntDatum(math.MinInt64)}, []any{dsqlIntDatum(math.MaxInt64)}, nil, false, false)},
		{dsqlMakeRange([]any{dsqlUintDatum(0)}, []any{dsqlUintDatum(math.MaxUint64)}, nil, false, false)},
		{dsqlMakeRange([]any{dsqlUintDatum(0)}, []any{dsqlUintDatum(maxI)}, nil, false, false)},
		{dsqlMakeRange([]any{dsqlUintDatum(maxI)}, []any{dsqlUintDatum(maxI + 1)}, nil, false, false)},
		{dsqlMakeRange([]any{dsqlUintDatum(maxI)}, []any{dsqlUintDatum(maxI + 1)}, nil, true, false)},
		{dsqlMakeRange([]any{dsqlUintDatum(maxI)}, []any{dsqlUintDatum(maxI + 1)}, nil, false, true)},
		{dsqlMakeRange([]any{dsqlUintDatum(maxI)}, []any{dsqlUintDatum(maxI + 1)}, nil, true, true)},
		{dsqlMakeRange([]any{dsqlUintDatum(maxI + 1)}, []any{dsqlUintDatum(math.MaxUint64)}, nil, false, false)},
		{dsqlMakeRange([]any{dsqlUintDatum(10)}, []any{dsqlUintDatum(maxI + 100)}, nil, false, false)},
		{
			dsqlMakeRange([]any{dsqlUintDatum(1)}, []any{dsqlUintDatum(100)}, nil, false, true),
			dsqlMakeRange([]any{dsqlUintDatum(maxI - 5)}, []any{dsqlUintDatum(maxI + 5)}, nil, false, false),
			dsqlMakeRange([]any{dsqlUintDatum(maxI + 100)}, []any{dsqlUintDatum(maxI + 200)}, nil, true, false),
		},
	}
	for _, rs := range splitBoundaryCases {
		for _, ko := range []bool{false, true} {
			for _, desc := range []bool{false, true} {
				emit("split_int64", rs, ko, desc, false)
			}
		}
		emit("split_int64", rs, true, false, true) // common handle -> no-op
	}
	// Random unsigned / signed multi-range cases.
	uintPts := []uint64{0, 1, 2, 100, maxI - 2, maxI - 1, maxI, maxI + 1, maxI + 2, maxI + 1000, math.MaxUint64 - 1, math.MaxUint64}
	for i := 0; i < 120; i++ {
		n := 1 + rng.IntN(4)
		// pick sorted pairs
		var pts []uint64
		for j := 0; j < n*2; j++ {
			if rng.IntN(3) == 0 {
				pts = append(pts, uintPts[rng.IntN(len(uintPts))])
			} else {
				pts = append(pts, rng.Uint64())
			}
		}
		// sort pts
		for a := 0; a < len(pts); a++ {
			for b := a + 1; b < len(pts); b++ {
				if pts[b] < pts[a] {
					pts[a], pts[b] = pts[b], pts[a]
				}
			}
		}
		var rs []any
		signed := rng.IntN(5) == 0
		for j := 0; j < len(pts); j += 2 {
			if signed {
				lo, hi := int64(pts[j]), int64(pts[j+1])
				if lo > hi {
					lo, hi = hi, lo
				}
				rs = append(rs, dsqlMakeRange([]any{dsqlIntDatum(lo)}, []any{dsqlIntDatum(hi)}, nil, rng.IntN(2) == 0, rng.IntN(2) == 0))
			} else {
				rs = append(rs, dsqlMakeRange([]any{dsqlUintDatum(pts[j])}, []any{dsqlUintDatum(pts[j+1])}, nil, rng.IntN(2) == 0, rng.IntN(2) == 0))
			}
		}
		emit("split_int64", rs, rng.IntN(2) == 0, rng.IntN(2) == 0, rng.IntN(10) == 0)
	}

	// 2. table_ranges (target >= 200)
	intBounds := []int64{math.MinInt64, math.MinInt64 + 1, -100, -1, 0, 1, 100, math.MaxInt64 - 1, math.MaxInt64}
	// Edge int handle ranges (both TableHandleRangesToKVRanges and TableRangesToKVRanges)
	for _, lo := range []int64{math.MinInt64, -10, 0, math.MaxInt64 - 1, math.MaxInt64} {
		for _, hi := range []int64{lo, lo + 1, math.MaxInt64} {
			if hi < lo {
				continue
			}
			for _, le := range []bool{false, true} {
				for _, he := range []bool{false, true} {
					r := dsqlMakeRange([]any{dsqlIntDatum(lo)}, []any{dsqlIntDatum(hi)}, nil, le, he)
					emit("table_ranges", "handle", []int64{42}, false, []any{r}, 0, true)
				}
			}
		}
	}
	// Multi-partition & empty & TableRangesToKVRanges
	emit("table_ranges", "handle", []int64{}, false, []any{}, 0, true)
	emit("table_ranges", "handle", []int64{10, 20, 30}, false, []any{
		dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(5)}, nil, false, false),
		dsqlMakeRange([]any{dsqlIntDatum(10)}, []any{dsqlIntDatum(20)}, nil, true, true),
	}, 0, true)
	emit("table_ranges", "table", []int64{99}, false, []any{
		dsqlMakeRange([]any{dsqlIntDatum(math.MinInt64)}, []any{dsqlIntDatum(math.MaxInt64)}, nil, false, false),
		dsqlMakeRange([]any{dsqlIntDatum(5)}, []any{dsqlIntDatum(5)}, nil, true, false),
	}, 0, true)
	// Random int handle ranges
	for i := 0; i < 60; i++ {
		nt := 1 + rng.IntN(3)
		tids := make([]int64, nt)
		for j := range tids {
			tids[j] = int64(1 + rng.IntN(1000))
		}
		nr := 1 + rng.IntN(3)
		rs := make([]any, nr)
		for j := 0; j < nr; j++ {
			a := intBounds[rng.IntN(len(intBounds))]
			b := intBounds[rng.IntN(len(intBounds))]
			if a > b {
				a, b = b, a
			}
			rs[j] = dsqlMakeRange([]any{dsqlIntDatum(a)}, []any{dsqlIntDatum(b)}, nil, rng.IntN(2) == 0, rng.IntN(2) == 0)
		}
		if nt == 1 && rng.IntN(3) == 0 {
			emit("table_ranges", "table", tids, false, rs, 0, true)
		} else {
			emit("table_ranges", "handle", tids, false, rs, 0, true)
		}
	}
	// Common handle & EncodeIndexKey (INT, VARCHAR with utf8mb4_general_ci / utf8mb4_bin, DECIMAL, TIMESTAMP across TZs, NULL, MinNotNull, MaxValue)
	t1 := types.NewTime(types.FromDate(2025, 6, 15, 10, 20, 30, 0), mysql.TypeTimestamp, 0)
	t2 := types.NewTime(types.FromDate(2025, 6, 15, 18, 0, 0, 0), mysql.TypeTimestamp, 0)
	chDatums := [][]any{
		{dsqlIntDatum(-5), dsqlStrDatum("Abc ", "utf8mb4_general_ci")},
		{dsqlIntDatum(0), dsqlStrDatum("abc", "utf8mb4_general_ci")},
		{dsqlIntDatum(42), dsqlStrDatum("xyz", "utf8mb4_bin")},
		{dsqlDecDatum("-12.34"), []any{types.KindMysqlTime, uint64(t1.CoreTime())}},
		{dsqlDecDatum("99.99"), []any{types.KindMysqlTime, uint64(t2.CoreTime())}},
		{dsqlNullDatum(), dsqlMinNotNull()},
		{dsqlMinNotNull(), dsqlMaxVal()},
	}
	collsList := [][]string{
		{"binary", "utf8mb4_general_ci"},
		{"binary", "utf8mb4_general_ci"},
		{"binary", "utf8mb4_bin"},
		{"binary", "binary"},
		{"binary", "binary"},
		{"binary", "binary"},
		{"binary", "binary"},
	}
	for i := 0; i < len(chDatums); i++ {
		for _, tz := range []int{0, -10800, 28800} {
			r := dsqlMakeRange(chDatums[i], chDatums[i], collsList[i], i%2 == 1, i%3 == 0)
			emit("table_ranges", "common", []int64{101, 102}, true, []any{r}, tz, true)
			emit("table_ranges", "handle", []int64{101}, true, []any{r}, tz, true)
			emit("table_ranges", "index_key", []int64{101}, true, []any{r}, tz, true)
		}
	}
	// Multi-range common handle with random combinations
	for i := 0; i < 60; i++ {
		a := rng.IntN(3)
		b := a + rng.IntN(3-a)
		r := dsqlMakeRange(chDatums[a], chDatums[b], collsList[a], rng.IntN(2) == 0, rng.IntN(2) == 0)
		tzs := []int{0, -10800, 28800}
		tz := tzs[rng.IntN(len(tzs))]
		if rng.IntN(3) == 0 {
			emit("table_ranges", "index_key", []int64{7}, true, []any{r}, tz, rng.IntN(2) == 0)
		} else {
			emit("table_ranges", "common", []int64{7, 8}, true, []any{r}, tz, rng.IntN(2) == 0)
		}
	}

	// 3. request_builder (target >= 150)
	u64Ptr := func(v uint64) *uint64 { return &v }
	u32Ptr := func(v uint32) *uint32 { return &v }
	boolPtr := func(v bool) *bool { return &v }
	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }

	rowKeySample := hex.EncodeToString([]byte("t\x80\x00\x00\x00\x00\x00\x00\x2a_r\x80\x00\x00\x00\x00\x00\x00\x01"))
	idxKeySample := hex.EncodeToString([]byte("t\x80\x00\x00\x00\x00\x00\x00\x2a_i\x80\x00\x00\x00\x00\x00\x00\x01"))
	unkKeySample := hex.EncodeToString([]byte("abcd"))

	// Deterministic targeted cases covering all spec §6.2 conditions:
	// - Zero-value DAGRequest fields 3, 4, 8, 11 (UseSetEnc=false vs true)
	// - Scan -> Limit (99999 vs 100000)
	// - IndexScan -> Limit -> IndexLookup (ParentIdx > 0 vs == 0)
	// - Single TableScan/IndexScan/PartitionTableScan KeepOrder with default conc=15 vs custom conc
	// - WeakConsistency / RCCheckTS / SI
	// - Priorities (NoPriority, Low, High, Delayed)
	// - ReplicaReadClosest + non-global readReplicaScope -> MatchStoreLabels
	// - All range modes (table, handle, handle_multi, key, key_hints, part_key, none)
	// - ResourceGroupTagger on row/index/unknown keys
	targetedRB := []dsqlReqBuilderSpec{
		// Zero-value DAGRequest fields 3/4/8/11
		{
			RangeMode: "none", StoreType: uint8(kv.TiKV), StartTS: 100,
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{{Tp: int32(tipb.ExecType_TypeTableScan)}}, TzOffset: 0, Flags: 0, TzName: "", UseSetEnc: false},
			TagKeyHex: rowKeySample, SetTagger: true,
		},
		// SetEncodeType enabled vs disabled
		{
			EnableChunkRPC: true, DistSQLConcurrency: 15, SetFromSession: true,
			RangeMode: "table", TableID: 42,
			Ranges: []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(10)}, nil, false, false))},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{{Tp: int32(tipb.ExecType_TypeTableScan)}}, UseSetEnc: true},
			KeepOrder: true, StartTS: 456, SetTagger: true, TagKeyHex: rowKeySample,
		},
		// Scan -> Limit 99999 (concurrency drops to PartitionNum=1) vs 100000 (keeps DistSQLConcurrency)
		{
			EnableChunkRPC: false, DistSQLConcurrency: 15, SetFromSession: true,
			RangeMode: "handle", TableID: 42,
			Ranges: []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(100)}, nil, false, false))},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{
				{Tp: int32(tipb.ExecType_TypeTableScan)},
				{Tp: int32(tipb.ExecType_TypeLimit), Limit: u64Ptr(99999)},
			}, UseSetEnc: true},
			TagKeyHex: idxKeySample, SetTagger: true,
		},
		{
			EnableChunkRPC: true, DistSQLConcurrency: 15, SetFromSession: true,
			RangeMode: "handle", TableID: 42,
			Ranges: []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(100)}, nil, false, false))},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{
				{Tp: int32(tipb.ExecType_TypeTableScan)},
				{Tp: int32(tipb.ExecType_TypeLimit), Limit: u64Ptr(100000)},
			}, UseSetEnc: true},
			TagKeyHex: unkKeySample, SetTagger: true,
		},
		// IndexScan -> Limit (ParentIdx=2) -> IndexLookup (conc = PartitionNum)
		{
			EnableChunkRPC: true, DistSQLConcurrency: 15, SetFromSession: true,
			RangeMode: "handle_multi", TableIDs: []int64{10, 20},
			Ranges: []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(50)}, nil, false, false))},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{
				{Tp: int32(tipb.ExecType_TypeIndexScan)},
				{Tp: int32(tipb.ExecType_TypeLimit), Limit: u64Ptr(50), ParentIdx: u32Ptr(2)},
				{Tp: int32(tipb.ExecType_TypeIndexLookUp)},
			}, UseSetEnc: true},
			TagKeyHex: rowKeySample, SetTagger: true,
		},
		// Closest replica read + non-global scope -> matchLabels = [["zone", "dc-west"]]
		{
			EnableChunkRPC: true, DistSQLConcurrency: 15, SetFromSession: true,
			ReplicaRead: uint8(kv.ReplicaReadClosest), ReadReplicaScope: "dc-west",
			TxnScope: kv.GlobalTxnScope, IsStaleness: true, HasAdjuster: true, HasMemTracker: true,
			HasRateLimit: true, AllowBatchCop: true, TiDBServerID: 77,
			SetConnID: true, ConnID: 99, ConnAlias: "sess-a",
			OverrideRGName: strPtr("rg-custom"), OverrideExplicitSrc: strPtr("explicit-src"),
			OverridePaging: boolPtr(true), OverrideConc: intPtr(8),
			RangeMode: "key_hints", RawKeys: [][2]string{{"01", "02"}, {"03", "04"}}, Hints: []int{5, 10},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{{Tp: int32(tipb.ExecType_TypeTableScan)}}, TzOffset: 28800, Flags: 1, TzName: "Asia/Shanghai", UseSetEnc: true},
			TagKeyHex: rowKeySample, SetTagger: true,
		},
		// RCCheckTS forces ReplicaReadLeader + RCCheckTS iso level
		{
			DistSQLConcurrency: 10, SetFromSession: true, RCCheckTS: true, ReplicaRead: uint8(kv.ReplicaReadFollower),
			Priority: int(mysql.HighPriority), NotFillCache: true, TaskID: 98765,
			EnablePaging: true, MinPagingSize: 64, MaxPagingSize: 4096, StoreBatchSize: 4,
			ResourceGroupName: "default", StoreBusyThresholdMs: 250, TiKVClientReadTimeout: 5000,
			InRestrictedSQL: true, ReqSourceType: "internal_test", ExplicitReqSourceType: "exp",
			RangeMode: "part_key", PartKeys: [][][2]string{{{"10", "20"}}, {{"30", "40"}}},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{{Tp: int32(tipb.ExecType_TypePartitionTableScan)}}, UseSetEnc: true},
			KeepOrder: true, Desc: true, TagKeyHex: idxKeySample, SetTagger: true,
		},
		// WeakConsistency -> RC iso level
		{
			DistSQLConcurrency: 15, SetFromSession: true, WeakConsistency: true,
			Priority: int(mysql.LowPriority), RangeMode: "key", RawKeys: [][2]string{{"aa", "bb"}},
			DAG: &dsqlDAGSpec{Execs: []dsqlExecSpec{{Tp: int32(tipb.ExecType_TypeIndexScan)}}, UseSetEnc: false},
			KeepOrder: true, TagKeyHex: unkKeySample,
		},
	}
	for _, s := range targetedRB {
		emit("request_builder", s)
	}
	// Random RequestBuilder combinations
	tagKeys := []string{rowKeySample, idxKeySample, unkKeySample, ""}
	prios := []int{int(mysql.NoPriority), int(mysql.LowPriority), int(mysql.HighPriority), int(mysql.DelayedPriority)}
	rrs := []uint8{uint8(kv.ReplicaReadLeader), uint8(kv.ReplicaReadFollower), uint8(kv.ReplicaReadMixed), uint8(kv.ReplicaReadClosest), uint8(kv.ReplicaReadPreferLeader)}
	scopes := []string{"", kv.GlobalReplicaScope, "dc-east", "dc-west"}
	execTps := []int32{int32(tipb.ExecType_TypeTableScan), int32(tipb.ExecType_TypeIndexScan), int32(tipb.ExecType_TypePartitionTableScan), int32(tipb.ExecType_TypeSelection)}
	for i := 0; i < 160; i++ {
		s := dsqlReqBuilderSpec{
			EnableChunkRPC:        rng.IntN(2) == 0,
			DistSQLConcurrency:    []int{1, 4, 15, 30}[rng.IntN(4)],
			WeakConsistency:       rng.IntN(4) == 0,
			RCCheckTS:             rng.IntN(4) == 0,
			NotFillCache:          rng.IntN(2) == 0,
			TaskID:                uint64(rng.IntN(10000)),
			Priority:              prios[rng.IntN(len(prios))],
			ReplicaRead:           rrs[rng.IntN(len(rrs))],
			EnablePaging:          rng.IntN(2) == 0,
			MinPagingSize:         []int{0, 64, 128}[rng.IntN(3)],
			MaxPagingSize:         []int{0, 1024, 20480}[rng.IntN(3)],
			StoreBatchSize:        []int{0, 1, 4}[rng.IntN(3)],
			ResourceGroupName:     []string{"", "default", "rg1"}[rng.IntN(3)],
			StoreBusyThresholdMs:  int64([]int{0, 100, 500}[rng.IntN(3)]),
			TiKVClientReadTimeout: uint64([]int{0, 1000, 30000}[rng.IntN(3)]),
			InRestrictedSQL:       rng.IntN(3) == 0,
			ReqSourceType:         []string{"", "sql", "internal"}[rng.IntN(3)],
			ExplicitReqSourceType: []string{"", "analyze", "exp"}[rng.IntN(3)],
			ConnID:                uint64(rng.IntN(500)),
			ConnAlias:             []string{"", "a1"}[rng.IntN(2)],
			TableID:               int64(1 + rng.IntN(200)),
			StartTS:               uint64(100 + rng.IntN(100000)),
			Desc:                  rng.IntN(2) == 0,
			KeepOrder:             rng.IntN(2) == 0,
			StoreType:             uint8(kv.TiKV),
			AllowBatchCop:         rng.IntN(2) == 0,
			SetFromSession:        rng.IntN(5) != 0,
			TiDBServerID:          uint64(rng.IntN(10)),
			TxnScope:              []string{"", kv.GlobalTxnScope, "dc-east"}[rng.IntN(3)],
			ReadReplicaScope:      scopes[rng.IntN(len(scopes))],
			IsStaleness:           rng.IntN(3) == 0,
			HasAdjuster:           rng.IntN(4) == 0,
			HasMemTracker:         rng.IntN(2) == 0,
			SetConnID:             rng.IntN(2) == 0,
			SetTagger:             rng.IntN(2) == 0,
			TagKeyHex:             tagKeys[rng.IntN(len(tagKeys))],
		}
		switch rng.IntN(6) {
		case 0:
			s.RangeMode = "none"
			// Avoid Go-only nil-KeyRanges panic on non-global txnScope (tidb-rs#422).
			if s.TxnScope != "" && s.TxnScope != kv.GlobalTxnScope {
				s.TxnScope = kv.GlobalTxnScope
			}
		case 1:
			s.RangeMode = "table"
			s.Ranges = []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(100)}, nil, false, false))}
		case 2:
			s.RangeMode = "handle"
			s.Ranges = []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(-5)}, []any{dsqlIntDatum(50)}, nil, true, false))}
		case 3:
			s.RangeMode = "handle_multi"
			s.TableIDs = []int64{10, 20, 30}
			s.Ranges = []json.RawMessage{mustRaw(dsqlMakeRange([]any{dsqlIntDatum(1)}, []any{dsqlIntDatum(20)}, nil, false, true))}
		case 4:
			s.RangeMode = "key"
			s.RawKeys = [][2]string{{"01", "05"}, {"06", "09"}}
		case 5:
			s.RangeMode = "part_key"
			s.PartKeys = [][][2]string{{{"01", "02"}}, {{"03", "04"}}}
		}
		if rng.IntN(5) != 0 {
			dag := &dsqlDAGSpec{
				TzOffset:  []int64{0, -10800, 28800}[rng.IntN(3)],
				Flags:     []uint64{0, 1, 64}[rng.IntN(3)],
				TzName:    []string{"", "UTC", "Asia/Shanghai"}[rng.IntN(3)],
				UseSetEnc: rng.IntN(2) == 0,
			}
			switch rng.IntN(4) {
			case 0:
				dag.Execs = []dsqlExecSpec{{Tp: execTps[rng.IntN(len(execTps))]}}
			case 1:
				lim := []uint64{0, 1, 50, 99999, 100000, 200000}[rng.IntN(6)]
				dag.Execs = []dsqlExecSpec{
					{Tp: int32(tipb.ExecType_TypeTableScan)},
					{Tp: int32(tipb.ExecType_TypeLimit), Limit: u64Ptr(lim)},
				}
			case 2:
				lim := []uint64{10, 99999, 100000}[rng.IntN(3)]
				pidx := []uint32{0, 2}[rng.IntN(2)]
				dag.Execs = []dsqlExecSpec{
					{Tp: int32(tipb.ExecType_TypeIndexScan)},
					{Tp: int32(tipb.ExecType_TypeLimit), Limit: u64Ptr(lim), ParentIdx: u32Ptr(pidx)},
					{Tp: int32(tipb.ExecType_TypeIndexLookUp)},
				}
			case 3:
				dag.Execs = []dsqlExecSpec{
					{Tp: int32(tipb.ExecType_TypeTableScan)},
					{Tp: int32(tipb.ExecType_TypeSelection)},
				}
			}
			s.DAG = dag
		}
		if rng.IntN(4) == 0 {
			s.OverridePaging = boolPtr(rng.IntN(2) == 0)
		}
		if rng.IntN(4) == 0 {
			s.OverrideConc = intPtr([]int{0, 1, 5, 15, 25}[rng.IntN(5)])
		}
		emit("request_builder", s)
	}

	// 4. select_decode (target >= 150)
	multiCols := []dsqlColSpec{
		{Tp: mysql.TypeLonglong, Flen: 20, Charset: "binary", Collate: "binary"},
		{Tp: mysql.TypeLonglong, Flag: mysql.UnsignedFlag, Flen: 20, Charset: "binary", Collate: "binary"},
		{Tp: mysql.TypeDouble, Flen: 22, Dec: -1, Charset: "binary", Collate: "binary"},
		{Tp: mysql.TypeNewDecimal, Flen: 10, Dec: 2, Charset: "binary", Collate: "binary"},
		{Tp: mysql.TypeVarchar, Flen: 32, Charset: "utf8mb4", Collate: "utf8mb4_bin"},
		{Tp: mysql.TypeTimestamp, Flen: 19, Dec: 0, Charset: "binary", Collate: "binary"},
	}
	makeMultiRows := func(n int) [][]types.Datum {
		rows := make([][]types.Datum, n)
		for i := 0; i < n; i++ {
			if i%5 == 4 {
				rows[i] = []types.Datum{
					types.NewDatum(nil), types.NewDatum(nil), types.NewDatum(nil),
					types.NewDatum(nil), types.NewDatum(nil), types.NewDatum(nil),
				}
				continue
			}
			var dec types.MyDecimal
			_ = dec.FromString([]byte(fmt.Sprintf("%d.%02d", i*3-10, (i*7)%100)))
			tm := types.NewTime(types.FromDate(2025, 1+(i%12), 1+(i%28), i%24, (i*5)%60, (i*11)%60, 0), mysql.TypeTimestamp, 0)
			rows[i] = []types.Datum{
				types.NewIntDatum(int64(i*17 - 100)),
				types.NewUintDatum(uint64(i)*1000 + uint64(math.MaxInt64)),
				types.NewFloat64Datum(float64(i) * 1.25),
				types.NewDecimalDatum(&dec),
				types.NewStringDatum(fmt.Sprintf("row-%d", i)),
				types.NewTimeDatum(tm),
			}
		}
		return rows
	}

	// Targeted select_decode cases: TypeChunk & TypeDefault, 0.8 threshold, multi-packet, warnings, error, raw mode, stats, unconsumed stats
	for _, enc := range []tipb.EncodeType{tipb.EncodeType_TypeChunk, tipb.EncodeType_TypeDefault} {
		for _, reqRows := range [][]int{{1}, {4}, {10}, {17}, {10, 5, 32}, {100}} {
			rows1 := makeMultiRows(15)
			rows2 := makeMultiRows(12)
			p1 := dsqlEncodePacket(multiCols, rows1, enc, -10800, nil, []*tipb.Error{{Code: 1292, Msg: "warn-1"}}, [][3]uint64{{500000, 15, 2}}, []int{6, 9})
			p2 := dsqlEncodePacket(multiCols, rows2, enc, -10800, nil, []*tipb.Error{{Code: 1366, Msg: "warn-2"}}, [][3]uint64{{300000, 12, 1}}, []int{12})
			emit("select_decode", "select", dsqlSelectSpec{
				Cols: multiCols,
				Packets: []dsqlMockPacket{
					{DataHex: p1, Stats: &dsqlMockStatsSpec{Callee: "127.0.0.1:20160", RespNs: 5_000_000, ProcKeys: 100, ProcNs: 2_000_000, WaitNs: 500_000, BackoffKey: "RegionMiss", BackoffNs: 10_000_000}},
					{DataHex: p2, Stats: &dsqlMockStatsSpec{Callee: "127.0.0.1:20161", RespNs: 7_000_000, ProcKeys: 80, ProcNs: 1_500_000, WaitNs: 200_000, CacheHit: true, BackoffKey: "RegionMiss", BackoffNs: 2_000_000}},
				},
				ReqRows:        reqRows,
				WithStats:      true,
				CopPlanIDs:     []int{2},
				RootPlanID:     1,
				Conc:           4,
				ExtraConc:      2,
				BatchNum:       1,
				BatchFallback:  0,
				BuildElapsedNs: 250_000,
				TzOffset:       -10800,
				UseMemTracker:  true,
				CacheEnabled:   true,
			})
		}
		// Error packet + warnings + unconsumed stats + NextRaw
		errPkt := dsqlEncodePacket(multiCols, nil, enc, 0, &tipb.Error{Code: 1105, Msg: "tikv-err"}, []*tipb.Error{{Code: 1000, Msg: "before-err"}}, nil, nil)
		emit("select_decode", "select", dsqlSelectSpec{
			Cols:           multiCols,
			Packets:        []dsqlMockPacket{{DataHex: errPkt}},
			ReqRows:        []int{32},
			WithStats:      true,
			RootPlanID:     1,
			Conc:           2,
			UseMemTracker:  true,
			CacheEnabled:   true,
			Unconsumed:     []dsqlMockStatsSpec{{Callee: "127.0.0.1:20160", ProcKeys: 50, ProcNs: 1_000_000}},
		})
		rawPkt := dsqlEncodePacket(multiCols, makeMultiRows(3), enc, 0, nil, nil, nil, nil)
		emit("select_decode", "select", dsqlSelectSpec{
			Cols:          multiCols,
			Packets:       []dsqlMockPacket{{DataHex: rawPkt}},
			RawMode:       true,
			Conc:          3,
			ExtraConc:     1,
			UseMemTracker: true,
		})
	}
	// Random select_decode cases (both "select" and "stats")
	intOnlyCols := []dsqlColSpec{
		{Tp: mysql.TypeLonglong, Flen: 20, Charset: "binary", Collate: "binary"},
		{Tp: mysql.TypeVarchar, Flen: 16, Charset: "utf8mb4", Collate: "utf8mb4_bin"},
	}
	for i := 0; i < 80; i++ {
		enc := tipb.EncodeType_TypeChunk
		if rng.IntN(2) == 0 {
			enc = tipb.EncodeType_TypeDefault
		}
		nPkt := 1 + rng.IntN(3)
		pkts := make([]dsqlMockPacket, nPkt)
		for p := 0; p < nPkt; p++ {
			nr := rng.IntN(25)
			rows := make([][]types.Datum, nr)
			for r := 0; r < nr; r++ {
				if rng.IntN(6) == 0 {
					rows[r] = []types.Datum{types.NewDatum(nil), types.NewDatum(nil)}
				} else {
					rows[r] = []types.Datum{
						types.NewIntDatum(int64(rng.IntN(2000) - 1000)),
						types.NewStringDatum(fmt.Sprintf("v%d", rng.IntN(100))),
					}
				}
			}
			var sizes []int
			if nr > 3 {
				sizes = []int{nr / 3, nr - nr/3}
			}
			var warns []*tipb.Error
			if rng.IntN(4) == 0 {
				warns = []*tipb.Error{{Code: 1292, Msg: fmt.Sprintf("w%d", p)}}
			}
			var summaries [][3]uint64
			if rng.IntN(2) == 0 {
				summaries = [][3]uint64{{uint64(100000 * (p + 1)), uint64(nr), 1}}
			}
			dataHex := dsqlEncodePacket(intOnlyCols, rows, enc, 0, nil, warns, summaries, sizes)
			var st *dsqlMockStatsSpec
			if rng.IntN(2) == 0 {
				st = &dsqlMockStatsSpec{
					Callee:   "127.0.0.1:20160",
					RespNs:   int64(1 + rng.IntN(50)) * 1_000_000,
					ProcKeys: int64(rng.IntN(500)),
					ProcNs:   int64(rng.IntN(20)) * 1_000_000,
					WaitNs:   int64(rng.IntN(10)) * 1_000_000,
					CacheHit: rng.IntN(3) == 0,
				}
				if rng.IntN(3) == 0 {
					st.BackoffKey = "RegionMiss"
					st.BackoffNs = int64(1+rng.IntN(20)) * 1_000_000
				}
			}
			pkts[p] = dsqlMockPacket{DataHex: dataHex, Stats: st}
		}
		reqRows := []int{1 + rng.IntN(20), 1 + rng.IntN(32)}
		emit("select_decode", "select", dsqlSelectSpec{
			Cols:           intOnlyCols,
			Packets:        pkts,
			ReqRows:        reqRows,
			WithStats:      rng.IntN(2) == 0,
			CopPlanIDs:     []int{2},
			RootPlanID:     1,
			Conc:           1 + rng.IntN(8),
			ExtraConc:      rng.IntN(4),
			BatchNum:       uint64(rng.IntN(3)),
			BatchFallback:  uint64(rng.IntN(2)),
			BuildElapsedNs: int64(rng.IntN(5)) * 1_000_000,
			UseMemTracker:  rng.IntN(2) == 0,
			CacheEnabled:   true,
		})
	}
	// Random runtime stats cases (single-key backoffSleep to avoid Go random map iteration order!)
	for i := 0; i < 65; i++ {
		mkEntries := func(n int) []dsqlStatsEntrySpec {
			out := make([]dsqlStatsEntrySpec, n)
			for j := 0; j < n; j++ {
				st := dsqlMockStatsSpec{
					Callee:   "127.0.0.1:20160",
					ProcKeys: int64(rng.IntN(1000)),
					ProcNs:   int64(rng.IntN(30)) * 1_000_000,
					WaitNs:   int64(rng.IntN(15)) * 1_000_000,
					CacheHit: rng.IntN(2) == 0,
				}
				if rng.IntN(2) == 0 {
					st.BackoffKey = "RegionMiss"
					st.BackoffNs = int64(1+rng.IntN(50)) * 1_000_000
				}
				out[j] = dsqlStatsEntrySpec{
					Stats:  st,
					RespNs: int64(1+rng.IntN(100)) * 1_000_000,
				}
			}
			return out
		}
		emit("select_decode", "stats", dsqlStatsOpSpec{
			A:            mkEntries(rng.IntN(6)),
			B:            mkEntries(rng.IntN(6)),
			ConcA:        rng.IntN(16),
			ExtraA:       rng.IntN(8),
			BatchA:       uint64(rng.IntN(4)),
			FallbackA:    uint64(rng.IntN(3)),
			BuildNsA:     int64(rng.IntN(5)) * 1_000_000,
			ConcB:        rng.IntN(16),
			ExtraB:       rng.IntN(8),
			BatchB:       uint64(rng.IntN(4)),
			FallbackB:    uint64(rng.IntN(3)),
			BuildNsB:     int64(rng.IntN(5)) * 1_000_000,
			CacheEnabled: rng.IntN(2) == 0,
		})
	}
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	must(err)
	return b
}
