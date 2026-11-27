package domains

// Domain "readexec": block D12.1 Part 2A (TableReaderExecutor, buildTableReader,
// buildNoRangeTableReader, newClosestReadAdjuster, tableResultHandler, closeAll,
// getPhysicalPlanIDs).
//
// Ops:
//   - "closest_read_adjuster": args [replicaRead, threshold, netDataSize, copTaskCount, zone] ->
//     newClosestReadAdjuster: tests nil vs non-nil adjuster, int64(netDataSize/float64(copTaskCount))
//     threshold comparison (including truncation), MatchStoreLabels append, and fallback to leader.
//   - "table_reader": args [spec] -> executorBuilder.buildTableReader -> TableReaderExecutor
//     (Open / Next / Close) with an injected mock selectResultHook: tests signed int handle,
//     unsigned int handle crossing MaxInt64 (keepOrder asc/desc and keepOrder=false), common handle,
//     empty ranges, temporary-table dummy mode, virtual generated columns in dependency order
//     (schema order reversed from column definition order), varying RequiredRows, mid-stream stop,
//     and Open / Next / Close error propagation.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/config"
	"github.com/pingcap/tidb/pkg/distsql"
	distsqlctx "github.com/pingcap/tidb/pkg/distsql/context"
	"github.com/pingcap/tidb/pkg/domain"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/infoschema"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannercore "github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/property"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/statistics"
	"github.com/pingcap/tidb/pkg/tablecodec"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/execdetails"
	"github.com/pingcap/tidb/pkg/util/memory"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/ranger"
	"github.com/pingcap/tipb/go-tipb"

	_ "unsafe" // go:linkname
)

const readexecSeed = 20261126

func init() {
	vec.Register(&vec.Domain{
		Name:     "readexec",
		Seed:     readexecSeed,
		Coverage: readexecCoverage,
		Blocks:   []string{"D12.1"},
		Gzip:     true,
		Generate: readexecGenerate,
		Exec:     readexecExec,
	})
}

var readexecCoverage = map[string]vec.Coverage{
	"closest_read_adjuster": {
		Min:     150,
		GoFuncs: []string{"pkg/executor/builder.go:newClosestReadAdjuster"},
	},
	"table_reader": {
		Min: 180,
		GoFuncs: []string{
			"pkg/executor/builder.go:buildNoRangeTableReader",
			"pkg/executor/builder.go:executorBuilder.buildTableReader",
			"pkg/executor/distsql.go:closeAll",
			"pkg/executor/distsql.go:getPhysicalPlanIDs",
			"pkg/executor/table_reader.go:TableReaderExecutor.Close",
			"pkg/executor/table_reader.go:TableReaderExecutor.Next",
			"pkg/executor/table_reader.go:TableReaderExecutor.Open",
			"pkg/executor/table_reader.go:TableReaderExecutor.Table",
			"pkg/executor/table_reader.go:TableReaderExecutor.buildKVReq",
			"pkg/executor/table_reader.go:TableReaderExecutor.buildKVReqSeparatelyForGroupedRanges",
			"pkg/executor/table_reader.go:TableReaderExecutor.buildRespForGroupedRanges",
			"pkg/executor/table_reader.go:TableReaderExecutor.buildVirtualColumnInfo",
			"pkg/executor/table_reader.go:TableReaderExecutor.setDummy",
			"pkg/executor/table_reader.go:buildVirtualColumnIndex",
			"pkg/executor/table_reader.go:buildVirtualColumnInfo",
			"pkg/executor/table_reader.go:newTableReaderExecutorContext",
			"pkg/executor/table_reader.go:selectResultHook.SelectResult",
			"pkg/executor/table_reader.go:sortAndGetKVRangesFromReqs",
			"pkg/executor/table_reader.go:tableReaderExecutorContext.GetInfoSchema",
			"pkg/executor/table_reader.go:tableResultHandler.Close",
			"pkg/executor/table_reader.go:tableResultHandler.nextChunk",
			"pkg/executor/table_reader.go:tableResultHandler.open",
		},
	},
	"index_reader": {
		Min: 180,
		GoFuncs: []string{
			"pkg/executor/builder.go:buildNoRangeIndexReader",
			"pkg/executor/builder.go:executorBuilder.buildIndexReader",
			"pkg/executor/distsql.go:IndexReaderExecutor.Close",
			"pkg/executor/distsql.go:IndexReaderExecutor.Next",
			"pkg/executor/distsql.go:IndexReaderExecutor.Open",
			"pkg/executor/distsql.go:IndexReaderExecutor.Table",
			"pkg/executor/distsql.go:IndexReaderExecutor.buildKVRangesForIndexReader",
			"pkg/executor/distsql.go:IndexReaderExecutor.buildKVReq",
			"pkg/executor/distsql.go:IndexReaderExecutor.open",
			"pkg/executor/distsql.go:IndexReaderExecutor.setDummy",
			"pkg/executor/distsql.go:buildKeyRanges",
			"pkg/executor/distsql.go:needMergeSort",
			"pkg/executor/distsql.go:newIndexReaderExecutorContext",
		},
	},
	"index_lookup_helpers": {
		Min: 150,
		GoFuncs: []string{
			"pkg/executor/distsql.go:CalculateBatchSize",
			"pkg/executor/distsql.go:getIndexScanMaxInFlight",
			"pkg/executor/distsql.go:getMergeSortIndexScanConcurrency",
			"pkg/executor/distsql.go:GetLackHandles",
			"pkg/executor/builder.go:buildIndexScanOutputOffsets",
			"pkg/executor/distsql.go:IndexLookUpRunTimeStats.String",
			"pkg/executor/distsql.go:IndexLookUpRunTimeStats.Clone",
			"pkg/executor/distsql.go:IndexLookUpRunTimeStats.Merge",
			"pkg/executor/distsql.go:IndexLookUpRunTimeStats.Tp",
		},
	},
	"index_lookup": {
		Min: 180,
		GoFuncs: []string{
			"pkg/executor/builder.go:buildTableReq",
			"pkg/executor/builder.go:buildIndexLookUpPushDownDAGReq",
			"pkg/executor/builder.go:buildIndexReq",
			"pkg/executor/builder.go:buildNoRangeIndexLookUpReader",
			"pkg/executor/builder.go:executorBuilder.buildIndexLookUpReader",
			"pkg/executor/distsql.go:newIndexLookUpExecutorContext",
			"pkg/executor/distsql.go:IndexLookUpExecutor.Table",
			"pkg/executor/distsql.go:IndexLookUpExecutor.setDummy",
			"pkg/executor/distsql.go:IndexLookUpExecutor.Open",
			"pkg/executor/distsql.go:IndexLookUpExecutor.buildTableKeyRanges",
			"pkg/executor/distsql.go:IndexLookUpExecutor.open",
			"pkg/executor/distsql.go:IndexLookUpExecutor.startWorkers",
			"pkg/executor/distsql.go:IndexLookUpExecutor.startIndexWorker",
			"pkg/executor/distsql.go:IndexLookUpExecutor.buildIndexSelectResultForRange",
			"pkg/executor/distsql.go:IndexLookUpExecutor.calculateBatchSize",
			"pkg/executor/distsql.go:IndexLookUpExecutor.startTableWorker",
			"pkg/executor/distsql.go:IndexLookUpExecutor.buildTableReader",
			"pkg/executor/distsql.go:IndexLookUpExecutor.Close",
			"pkg/executor/distsql.go:IndexLookUpExecutor.Next",
			"pkg/executor/distsql.go:IndexLookUpExecutor.getResultTask",
			"pkg/executor/distsql.go:IndexLookUpExecutor.getHandle",
			"pkg/executor/distsql.go:indexWorker.fetchHandles",
			"pkg/executor/distsql.go:indexWorker.fetchHandlesRolling",
			"pkg/executor/distsql.go:indexWorker.extractTaskHandles",
			"pkg/executor/distsql.go:indexWorker.extractLookUpPushDownRowsOrHandles",
			"pkg/executor/distsql.go:tableWorker.pickAndExecTask",
			"pkg/executor/distsql.go:tableWorker.executeTask",
			"pkg/executor/distsql.go:lookupTableTask.Len",
			"pkg/executor/distsql.go:lookupTableTask.Less",
			"pkg/executor/distsql.go:lookupTableTask.Swap",
		},
	},
}

//go:linkname reNewClosestReadAdjuster github.com/pingcap/tidb/pkg/executor.newClosestReadAdjuster
func reNewClosestReadAdjuster(dctx *distsqlctx.DistSQLContext, req *kv.Request, netDataSize float64) kv.CoprRequestAdjuster

// ---------------------------------------------------------------------------
// closest_read_adjuster
// ---------------------------------------------------------------------------

type reLabelOut struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type reAdjusterOut struct {
	NilAdjuster      bool         `json:"nil_adjuster"`
	Adjusted         bool         `json:"adjusted"`
	ReplicaRead      string       `json:"replica_read"`
	MatchStoreLabels []reLabelOut `json:"match_store_labels"`
}

func reParseReplicaRead(s string) kv.ReplicaReadType {
	switch s {
	case "leader":
		return kv.ReplicaReadLeader
	case "follower":
		return kv.ReplicaReadFollower
	case "mixed":
		return kv.ReplicaReadMixed
	case "closest-adaptive":
		return kv.ReplicaReadClosestAdaptive
	case "closest":
		return kv.ReplicaReadClosest
	default:
		panic("unknown replica read: " + s)
	}
}

func reFormatReplicaRead(t kv.ReplicaReadType) string {
	switch t {
	case kv.ReplicaReadLeader:
		return "leader"
	case kv.ReplicaReadFollower:
		return "follower"
	case kv.ReplicaReadMixed:
		return "mixed"
	case kv.ReplicaReadClosestAdaptive:
		return "closest-adaptive"
	case kv.ReplicaReadClosest:
		return "closest"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

func reExecClosestReadAdjuster(args []json.RawMessage) (any, error) {
	var rr, zone string
	var threshold int64
	var netDataSize float64
	var copTaskCount int
	if err := json.Unmarshal(args[0], &rr); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(args[1], &threshold); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(args[2], &netDataSize); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(args[3], &copTaskCount); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(args[4], &zone); err != nil {
		return nil, err
	}

	// Set and restore global config zone label deterministically via UpdateGlobal
	// so tikvcfg's global TxnScope (read by GetTxnScopeFromConfig) stays in sync.
	restore := config.RestoreFunc()
	defer restore()
	config.UpdateGlobal(func(conf *config.Config) {
		newLabels := make(map[string]string, len(conf.Labels)+1)
		for k, v := range conf.Labels {
			newLabels[k] = v
		}
		if zone != "" {
			newLabels["zone"] = zone
		} else {
			delete(newLabels, "zone")
		}
		conf.Labels = newLabels
	})

	dctx := &distsqlctx.DistSQLContext{
		ReplicaClosestReadThreshold: threshold,
	}
	req := &kv.Request{
		ReplicaRead: reParseReplicaRead(rr),
	}
	adj := reNewClosestReadAdjuster(dctx, req, netDataSize)
	if adj == nil {
		return reAdjusterOut{
			NilAdjuster:      true,
			Adjusted:         false,
			ReplicaRead:      reFormatReplicaRead(req.ReplicaRead),
			MatchStoreLabels: []reLabelOut{},
		}, nil
	}
	adjusted := adj(req, copTaskCount)
	labels := make([]reLabelOut, 0, len(req.MatchStoreLabels))
	for _, l := range req.MatchStoreLabels {
		labels = append(labels, reLabelOut{Key: l.Key, Value: l.Value})
	}
	return reAdjusterOut{
		NilAdjuster:      false,
		Adjusted:         adjusted,
		ReplicaRead:      reFormatReplicaRead(req.ReplicaRead),
		MatchStoreLabels: labels,
	}, nil
}

// ---------------------------------------------------------------------------
// table_reader
// ---------------------------------------------------------------------------

// reRangeSpec describes a single ranger.Range in JSON.
// Kind: "int" (int64), "uint" (uint64 encoded as string), "common" ([int64, string]).
type reRangeSpec struct {
	Low         []any `json:"low"`
	High        []any `json:"high"`
	LowExclude  bool  `json:"low_exclude,omitempty"`
	HighExclude bool  `json:"high_exclude,omitempty"`
}

// reStreamSpec scripts one mock SelectResult returned by selectResultHook.
type reStreamSpec struct {
	OpenErr  string     `json:"open_err,omitempty"`
	Rows     [][]*int64 `json:"rows,omitempty"`
	NextErr  string     `json:"next_err,omitempty"`
	ErrAfter int        `json:"err_after,omitempty"` // return NextErr after this many Next calls
	CloseErr string     `json:"close_err,omitempty"`
}

type reTableReaderSpec struct {
	TableID      int64          `json:"table_id"`
	HandleKind   string         `json:"handle_kind"` // "int", "uint", "common"
	VirtualCols  bool           `json:"virtual_cols,omitempty"`
	TempTable    bool           `json:"temp_table,omitempty"`
	SetDummyCall bool           `json:"set_dummy_call,omitempty"`
	KeepOrder    bool           `json:"keep_order"`
	Desc         bool           `json:"desc"`
	Paging       bool           `json:"paging"`
	Concurrency  int            `json:"concurrency"`
	ReplicaRead  string         `json:"replica_read"`
	Threshold    int64          `json:"threshold"`
	MaxChunkSize int            `json:"max_chunk_size"`
	Ranges       []reRangeSpec  `json:"ranges"`
	Streams      []reStreamSpec `json:"streams"`
	RequiredRows []int          `json:"required_rows"`
}

type reKVRangeOut struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type reReqOut struct {
	KeyRanges     []reKVRangeOut `json:"key_ranges"`
	Desc          bool           `json:"desc"`
	KeepOrder     bool           `json:"keep_order"`
	Concurrency   int            `json:"concurrency"`
	Paging        bool           `json:"paging"`
	StartTS       uint64         `json:"start_ts"`
	ReplicaRead   string         `json:"replica_read"`
	OutputOffsets []uint32       `json:"output_offsets"`
	CopPlanIDs    []int          `json:"cop_plan_ids"`
}

type reNextOut struct {
	Rows [][]*int64 `json:"rows,omitempty"`
	Err  string     `json:"err,omitempty"`
}

type reTableReaderOut struct {
	Reqs         []reReqOut     `json:"reqs"`
	KVRanges     []reKVRangeOut `json:"kv_ranges"`
	OpenErr      string         `json:"open_err,omitempty"`
	Nexts        []reNextOut    `json:"nexts"`
	CloseErr     string         `json:"close_err,omitempty"`
	TableIDCheck int64          `json:"table_id_check"`
}

type reMockSelectResult struct {
	spec      reStreamSpec
	numCols   int
	pos       int
	nextCalls int
}

func (m *reMockSelectResult) NextRaw(context.Context) ([]byte, error) { return nil, nil }
func (m *reMockSelectResult) IntoIter([][]*types.FieldType) (distsql.SelectResultIter, error) {
	return nil, errors.New("not implemented")
}
func (m *reMockSelectResult) Close() error {
	if m.spec.CloseErr != "" {
		return errors.New(m.spec.CloseErr)
	}
	return nil
}
func (m *reMockSelectResult) Next(_ context.Context, chk *chunk.Chunk) error {
	chk.Reset()
	if m.spec.NextErr != "" && m.nextCalls >= m.spec.ErrAfter {
		m.nextCalls++
		return errors.New(m.spec.NextErr)
	}
	m.nextCalls++
	if m.pos >= len(m.spec.Rows) {
		return nil
	}
	take := min(chk.RequiredRows(), len(m.spec.Rows)-m.pos)
	for i := 0; i < take; i++ {
		r := m.spec.Rows[m.pos+i]
		for colIdx := 0; colIdx < m.numCols; colIdx++ {
			if colIdx < len(r) && r[colIdx] != nil {
				chk.AppendInt64(colIdx, *r[colIdx])
			} else {
				chk.AppendNull(colIdx)
			}
		}
	}
	m.pos += take
	return nil
}

func reDecodeRangeDatum(handleKind string, colIdx int, v any) types.Datum {
	if handleKind == "uint" {
		var u uint64
		fmt.Sscanf(v.(string), "%d", &u)
		return types.NewUintDatum(u)
	}
	if handleKind == "common" && colIdx == 1 {
		s := v.(string)
		return types.NewStringDatum(s)
	}
	return types.NewIntDatum(int64(v.(float64)))
}

func reBuildRanges(handleKind string, specs []reRangeSpec) []*ranger.Range {
	out := make([]*ranger.Range, 0, len(specs))
	for _, rs := range specs {
		low := make([]types.Datum, len(rs.Low))
		for i, v := range rs.Low {
			low[i] = reDecodeRangeDatum(handleKind, i, v)
		}
		high := make([]types.Datum, len(rs.High))
		for i, v := range rs.High {
			high[i] = reDecodeRangeDatum(handleKind, i, v)
		}
		collators := make([]collate.Collator, len(low))
		for i := range collators {
			collators[i] = collate.GetBinaryCollator()
		}
		out = append(out, &ranger.Range{
			LowVal:      low,
			HighVal:     high,
			LowExclude:  rs.LowExclude,
			HighExclude: rs.HighExclude,
			Collators:   collators,
		})
	}
	return out
}

func reBuildTableAndPlan(sctx *mock.Context, spec *reTableReaderSpec) (*model.TableInfo, *plannercore.PhysicalTableReader) {
	intFT := func(unsigned bool) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeLonglong)
		if unsigned {
			ft.AddFlag(mysql.UnsignedFlag)
		}
		return ft
	}
	strFT := func() *types.FieldType {
		ft := types.NewFieldType(mysql.TypeVarchar)
		ft.SetFlen(64)
		ft.SetCharset(mysql.DefaultCharset)
		ft.SetCollate("utf8mb4_bin")
		return ft
	}

	var cols []*model.ColumnInfo
	var schemaCols []*expression.Column
	var indices []*model.IndexInfo

	switch spec.HandleKind {
	case "int", "uint":
		unsigned := spec.HandleKind == "uint"
		pkFT := intFT(unsigned)
		pkFT.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
		c0 := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("id"), Offset: 0, FieldType: *pkFT, State: model.StatePublic}
		c1 := &model.ColumnInfo{ID: 2, Name: pmodel.NewCIStr("a"), Offset: 1, FieldType: *intFT(false), State: model.StatePublic}
		cols = []*model.ColumnInfo{c0, c1}
		ec0 := &expression.Column{ID: 1, UniqueID: 1, Index: 0, RetType: pkFT.Clone()}
		ec1 := &expression.Column{ID: 2, UniqueID: 2, Index: 1, RetType: intFT(false)}
		if spec.VirtualCols {
			// v1 (offset 2) = a + 10; v2 (offset 3) = v1 * 2.
			// Schema puts v2 BEFORE v1: [id, v2, a, v1], exercising definition-order sort.
			c2 := &model.ColumnInfo{ID: 3, Name: pmodel.NewCIStr("v1"), Offset: 2, FieldType: *intFT(false), State: model.StatePublic, GeneratedExprString: "a + 10", GeneratedStored: false}
			c3 := &model.ColumnInfo{ID: 4, Name: pmodel.NewCIStr("v2"), Offset: 3, FieldType: *intFT(false), State: model.StatePublic, GeneratedExprString: "v1 * 2", GeneratedStored: false}
			cols = append(cols, c2, c3)

			aRef := &expression.Column{ID: 2, UniqueID: 2, Index: 2, RetType: intFT(false)}
			v1Ref := &expression.Column{ID: 3, UniqueID: 3, Index: 3, RetType: intFT(false)}
			ten := &expression.Constant{Value: types.NewIntDatum(10), RetType: intFT(false)}
			two := &expression.Constant{Value: types.NewIntDatum(2), RetType: intFT(false)}
			v1Expr, err := expression.NewFunction(sctx.GetExprCtx(), ast.Plus, intFT(false), aRef, ten)
			if err != nil {
				panic(err)
			}
			v2Expr, err := expression.NewFunction(sctx.GetExprCtx(), ast.Mul, intFT(false), v1Ref, two)
			if err != nil {
				panic(err)
			}
			ec0.Index = 0
			ecV2 := &expression.Column{ID: 4, UniqueID: 4, Index: 1, RetType: intFT(false), VirtualExpr: v2Expr}
			ec1.Index = 2
			ecV1 := &expression.Column{ID: 3, UniqueID: 3, Index: 3, RetType: intFT(false), VirtualExpr: v1Expr}
			schemaCols = []*expression.Column{ec0, ecV2, ec1, ecV1}
		} else {
			schemaCols = []*expression.Column{ec0, ec1}
		}
	case "common":
		c0 := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("k1"), Offset: 0, FieldType: *intFT(false), State: model.StatePublic}
		c1 := &model.ColumnInfo{ID: 2, Name: pmodel.NewCIStr("k2"), Offset: 1, FieldType: *strFT(), State: model.StatePublic}
		c2 := &model.ColumnInfo{ID: 3, Name: pmodel.NewCIStr("v"), Offset: 2, FieldType: *intFT(false), State: model.StatePublic}
		cols = []*model.ColumnInfo{c0, c1, c2}
		indices = []*model.IndexInfo{
			{
				ID:      1,
				Name:    pmodel.NewCIStr("PRIMARY"),
				Primary: true,
				Unique:  true,
				State:   model.StatePublic,
				Columns: []*model.IndexColumn{
					{Name: pmodel.NewCIStr("k1"), Offset: 0, Length: types.UnspecifiedLength},
					{Name: pmodel.NewCIStr("k2"), Offset: 1, Length: types.UnspecifiedLength},
				},
			},
		}
		schemaCols = []*expression.Column{
			{ID: 1, UniqueID: 1, Index: 0, RetType: intFT(false)},
			{ID: 3, UniqueID: 3, Index: 1, RetType: intFT(false)},
		}
	default:
		panic("unknown handle kind: " + spec.HandleKind)
	}

	tblInfo := &model.TableInfo{
		ID:             spec.TableID,
		Name:           pmodel.NewCIStr("t"),
		Columns:        cols,
		Indices:        indices,
		PKIsHandle:     spec.HandleKind == "int" || spec.HandleKind == "uint",
		IsCommonHandle: spec.HandleKind == "common",
		State:          model.StatePublic,
	}
	if spec.TempTable {
		tblInfo.TempTableType = model.TempTableLocal
	}

	schema := expression.NewSchema(schemaCols...)
	stats := &property.StatsInfo{RowCount: 50}
	scan := plannercore.PhysicalTableScan{
		Table:     tblInfo,
		Columns:   cols,
		Ranges:    reBuildRanges(spec.HandleKind, spec.Ranges),
		KeepOrder: spec.KeepOrder,
		Desc:      spec.Desc,
		StoreType: kv.TiKV,
	}.Init(sctx.GetPlanCtx(), 0)
	scan.SetSchema(schema)
	scan.SetStats(stats)
	// Set unexported tblColHists via reflect so GetNetDataSize works identically.
	pseudo := statistics.PseudoTable(tblInfo, false, false)
	fHists := reflect.ValueOf(scan).Elem().FieldByName("tblColHists")
	reflect.NewAt(fHists.Type(), unsafe.Pointer(fHists.UnsafeAddr())).Elem().Set(reflect.ValueOf(&pseudo.HistColl))

	reader := plannercore.PhysicalTableReader{
		TablePlans:     []base.PhysicalPlan{scan},
		StoreType:      kv.TiKV,
		ReadReqType:    plannercore.Cop,
		IsCommonHandle: tblInfo.IsCommonHandle,
	}.Init(sctx.GetPlanCtx(), 0)
	fTP := reflect.ValueOf(reader).Elem().FieldByName("tablePlan")
	reflect.NewAt(fTP.Type(), unsafe.Pointer(fTP.UnsafeAddr())).Elem().Set(reflect.ValueOf(scan))
	reader.SetSchema(schema)
	reader.SetStats(stats)
	return tblInfo, reader
}

func reSetSelectResultHook(tr *executor.TableReaderExecutor, fn func(context.Context, *distsqlctx.DistSQLContext, *kv.Request, []*types.FieldType, []int) (distsql.SelectResult, error)) {
	fHook := reflect.ValueOf(tr).Elem().FieldByName("selectResultHook")
	fFn := fHook.FieldByName("selectResultFunc")
	reflect.NewAt(fFn.Type(), unsafe.Pointer(fFn.UnsafeAddr())).Elem().Set(reflect.ValueOf(fn))
}

func reReadKVRanges(tr *executor.TableReaderExecutor) []reKVRangeOut {
	f := reflect.ValueOf(tr).Elem().FieldByName("kvRanges")
	krs := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().([]kv.KeyRange)
	out := make([]reKVRangeOut, 0, len(krs))
	for _, kr := range krs {
		out = append(out, reKVRangeOut{
			Start: hex.EncodeToString(kr.StartKey),
			End:   hex.EncodeToString(kr.EndKey),
		})
	}
	return out
}

func reReadDagOutputOffsets(tr *executor.TableReaderExecutor) []uint32 {
	f := reflect.ValueOf(tr).Elem().FieldByName("dagPB")
	dag := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().(*tipb.DAGRequest)
	out := make([]uint32, len(dag.OutputOffsets))
	copy(out, dag.OutputOffsets)
	return out
}

//go:linkname reSetDummy github.com/pingcap/tidb/pkg/executor.(*TableReaderExecutor).setDummy
func reSetDummy(e *executor.TableReaderExecutor)

// reSessionCtx wraps mock.Context so GetDistSQLCtx populates DistSQLConcurrency,
// ReplicaReadType, and ReplicaClosestReadThreshold from SessionVars (as real session.GetDistSQLCtx does).
type reSessionCtx struct {
	*mock.Context
	client kv.Client
}

func (c *reSessionCtx) GetClient() kv.Client {
	if c.client != nil {
		return c.client
	}
	return c.Context.GetClient()
}

func (c *reSessionCtx) GetDistSQLCtx() *distsqlctx.DistSQLContext {
	dctx := c.Context.GetDistSQLCtx()
	vars := c.GetSessionVars()
	dctx.DistSQLConcurrency = vars.DistSQLScanConcurrency()
	dctx.ReplicaReadType = vars.GetReplicaRead()
	dctx.ReplicaClosestReadThreshold = vars.ReplicaClosestReadThreshold
	dctx.EnablePaging = vars.EnablePaging
	dctx.MinPagingSize = vars.MinPagingSize
	dctx.MaxPagingSize = vars.MaxPagingSize
	dctx.EnableChunkRPC = vars.EnableChunkRPC
	dctx.RuntimeStatsColl = vars.StmtCtx.RuntimeStatsColl
	if c.client != nil {
		dctx.Client = c.client
	}
	return dctx
}

func reExecTableReader(args []json.RawMessage) (any, error) {
	var spec reTableReaderSpec
	if err := json.Unmarshal(args[0], &spec); err != nil {
		return nil, err
	}

	sctx := &reSessionCtx{Context: mock.NewContext()}
	vars := sctx.GetSessionVars()
	vars.InitChunkSize = variable.DefInitChunkSize
	vars.MaxChunkSize = spec.MaxChunkSize
	vars.EnableChunkRPC = true
	vars.EnablePaging = spec.Paging
	vars.SetDistSQLScanConcurrency(spec.Concurrency)
	vars.SetReplicaRead(reParseReplicaRead(spec.ReplicaRead))
	vars.ReplicaClosestReadThreshold = spec.Threshold
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)
	sctx.BindDomain(domain.NewMockDomain())

	tblInfo, readerPlan := reBuildTableAndPlan(sctx.Context, &spec)
	is := infoschema.MockInfoSchema([]*model.TableInfo{tblInfo})

	b := executor.NewMockExecutorBuilderForTest(sctx, is, nil)
	// Supply snapshot TS directly via executorBuilder's dataReaderBuilder path
	// so mock.Context does not require a full sessiontxn provider.
	ebVal := reflect.ValueOf(b).Elem().FieldByName("executorBuilder").Elem()
	fForDRB := ebVal.FieldByName("forDataReaderBuilder")
	reflect.NewAt(fForDRB.Type(), unsafe.Pointer(fForDRB.UnsafeAddr())).Elem().SetBool(true)
	fDRBTS := ebVal.FieldByName("dataReaderTS")
	reflect.NewAt(fDRBTS.Type(), unsafe.Pointer(fDRBTS.UnsafeAddr())).Elem().SetUint(100)
	built := b.Build(readerPlan)
	if err := beBuilderErr(b); err != nil {
		return nil, err
	}
	tr := built.(*executor.TableReaderExecutor)
	if spec.SetDummyCall {
		reSetDummy(tr)
	}
	tblIDCheck := tr.Table().Meta().ID

	reqs := []reReqOut{}
	streamIdx := 0
	outputOffsets := reReadDagOutputOffsets(tr)
	reSetSelectResultHook(tr, func(_ context.Context, _ *distsqlctx.DistSQLContext, kvReq *kv.Request, fieldTypes []*types.FieldType, copPlanIDs []int) (distsql.SelectResult, error) {
		krs := kvReq.KeyRanges.AppendSelfTo(nil)
		krOut := make([]reKVRangeOut, 0, len(krs))
		for _, kr := range krs {
			krOut = append(krOut, reKVRangeOut{
				Start: hex.EncodeToString(kr.StartKey),
				End:   hex.EncodeToString(kr.EndKey),
			})
		}
		idsCopy := make([]int, len(copPlanIDs))
		copy(idsCopy, copPlanIDs)
		reqs = append(reqs, reReqOut{
			KeyRanges:     krOut,
			Desc:          kvReq.Desc,
			KeepOrder:     kvReq.KeepOrder,
			Concurrency:   kvReq.Concurrency,
			Paging:        kvReq.Paging.Enable,
			StartTS:       kvReq.StartTs,
			ReplicaRead:   reFormatReplicaRead(kvReq.ReplicaRead),
			OutputOffsets: outputOffsets,
			CopPlanIDs:    idsCopy,
		})
		var st reStreamSpec
		if streamIdx < len(spec.Streams) {
			st = spec.Streams[streamIdx]
		}
		streamIdx++
		if st.OpenErr != "" {
			return nil, errors.New(st.OpenErr)
		}
		return &reMockSelectResult{spec: st, numCols: len(fieldTypes)}, nil
	})

	ctx := context.Background()
	out := reTableReaderOut{
		Reqs:         []reReqOut{},
		KVRanges:     []reKVRangeOut{},
		Nexts:        []reNextOut{},
		TableIDCheck: tblIDCheck,
	}
	if err := tr.Open(ctx); err != nil {
		out.Reqs = reqs
		out.KVRanges = reReadKVRanges(tr)
		out.OpenErr = err.Error()
		_ = tr.Close()
		return out, nil
	}
	out.Reqs = reqs
	out.KVRanges = reReadKVRanges(tr)

	chk := tr.NewChunk()
	numCols := chk.NumCols()
	for _, reqRows := range spec.RequiredRows {
		chk.SetRequiredRows(reqRows, spec.MaxChunkSize)
		if err := tr.Next(ctx, chk); err != nil {
			out.Nexts = append(out.Nexts, reNextOut{Err: err.Error()})
			break
		}
		rows := make([][]*int64, chk.NumRows())
		for r := 0; r < chk.NumRows(); r++ {
			row := chk.GetRow(r)
			vals := make([]*int64, numCols)
			for c := 0; c < numCols; c++ {
				if !row.IsNull(c) {
					v := row.GetInt64(c)
					vals[c] = &v
				}
			}
			rows[r] = vals
		}
		out.Nexts = append(out.Nexts, reNextOut{Rows: rows})
	}
	if err := tr.Close(); err != nil {
		out.CloseErr = err.Error()
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// index_reader
// ---------------------------------------------------------------------------

type reIndexReaderSpec struct {
	TableID       int64           `json:"table_id"`
	IndexID       int64           `json:"index_id"` // -1 for common handle, >= 1 for secondary index
	TempTable     bool            `json:"temp_table,omitempty"`
	SetDummyCall  bool            `json:"set_dummy_call,omitempty"`
	KeepOrder     bool            `json:"keep_order"`
	Desc          bool            `json:"desc"`
	Paging        bool            `json:"paging"`
	Concurrency   int             `json:"concurrency"`
	ReplicaRead   string          `json:"replica_read"`
	Threshold     int64           `json:"threshold"`
	MaxChunkSize  int             `json:"max_chunk_size"`
	OutputOffsets []int           `json:"output_offsets"`
	ByItemsDesc   []bool          `json:"by_items_desc,omitempty"`
	Ranges        []reRangeSpec   `json:"ranges,omitempty"`
	GroupedRanges [][]reRangeSpec `json:"grouped_ranges,omitempty"`
	Streams       []reStreamSpec  `json:"streams"`
	RequiredRows  []int           `json:"required_rows"`
}

type reIndexReaderOut struct {
	Reqs         []reReqOut     `json:"reqs"`
	KVRanges     []reKVRangeOut `json:"kv_ranges"`
	OpenErr      string         `json:"open_err,omitempty"`
	Nexts        []reNextOut    `json:"nexts"`
	CloseErr     string         `json:"close_err,omitempty"`
	TableIDCheck int64          `json:"table_id_check"`
	IndexNames   []string       `json:"index_names"`
}

func reSetIndexSelectResultHook(ir *executor.IndexReaderExecutor, fn func(context.Context, *distsqlctx.DistSQLContext, *kv.Request, []*types.FieldType, []int) (distsql.SelectResult, error)) {
	fHook := reflect.ValueOf(ir).Elem().FieldByName("selectResultHook")
	fFn := fHook.FieldByName("selectResultFunc")
	reflect.NewAt(fFn.Type(), unsafe.Pointer(fFn.UnsafeAddr())).Elem().Set(reflect.ValueOf(fn))
}

func reReadIndexKVRanges(ir *executor.IndexReaderExecutor) []reKVRangeOut {
	f := reflect.ValueOf(ir).Elem().FieldByName("kvRanges")
	krs := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().([]kv.KeyRange)
	out := make([]reKVRangeOut, 0, len(krs))
	for _, kr := range krs {
		out = append(out, reKVRangeOut{
			Start: hex.EncodeToString(kr.StartKey),
			End:   hex.EncodeToString(kr.EndKey),
		})
	}
	return out
}

func reReadIndexDagOutputOffsets(ir *executor.IndexReaderExecutor) []uint32 {
	f := reflect.ValueOf(ir).Elem().FieldByName("dagPB")
	dag := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().(*tipb.DAGRequest)
	out := make([]uint32, len(dag.OutputOffsets))
	copy(out, dag.OutputOffsets)
	return out
}

//go:linkname reIndexSetDummy github.com/pingcap/tidb/pkg/executor.(*IndexReaderExecutor).setDummy
func reIndexSetDummy(e *executor.IndexReaderExecutor)

func reBuildIndexTableAndPlan(sctx *mock.Context, spec *reIndexReaderSpec) (*model.TableInfo, *plannercore.PhysicalIndexReader) {
	intFT := func() *types.FieldType {
		return types.NewFieldType(mysql.TypeLonglong)
	}
	pkFT := intFT()
	pkFT.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
	c0 := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("id"), Offset: 0, FieldType: *pkFT, State: model.StatePublic}
	c1 := &model.ColumnInfo{ID: 2, Name: pmodel.NewCIStr("k1"), Offset: 1, FieldType: *intFT(), State: model.StatePublic}
	c2 := &model.ColumnInfo{ID: 3, Name: pmodel.NewCIStr("k2"), Offset: 2, FieldType: *intFT(), State: model.StatePublic}
	cols := []*model.ColumnInfo{c0, c1, c2}

	isCommon := spec.IndexID == -1
	idxName := "idx_k"
	if isCommon {
		idxName = "PRIMARY"
	}
	idxInfo := &model.IndexInfo{
		ID:      spec.IndexID,
		Name:    pmodel.NewCIStr(idxName),
		Table:   pmodel.NewCIStr("t"),
		Primary: isCommon,
		Unique:  isCommon,
		State:   model.StatePublic,
		Columns: []*model.IndexColumn{
			{Name: pmodel.NewCIStr("k1"), Offset: 1, Length: types.UnspecifiedLength},
			{Name: pmodel.NewCIStr("k2"), Offset: 2, Length: types.UnspecifiedLength},
		},
	}

	tblInfo := &model.TableInfo{
		ID:             spec.TableID,
		Name:           pmodel.NewCIStr("t"),
		Columns:        cols,
		Indices:        []*model.IndexInfo{idxInfo},
		PKIsHandle:     !isCommon,
		IsCommonHandle: isCommon,
		State:          model.StatePublic,
	}
	if spec.TempTable {
		tblInfo.TempTableType = model.TempTableLocal
	}

	scanCols := []*expression.Column{
		{ID: 2, UniqueID: 2, Index: 0, RetType: intFT()},
		{ID: 3, UniqueID: 3, Index: 1, RetType: intFT()},
		{ID: 1, UniqueID: 1, Index: 2, RetType: pkFT.Clone()},
	}
	scanSchema := expression.NewSchema(scanCols...)

	outCols := make([]*expression.Column, len(spec.OutputOffsets))
	readerCols := make([]*expression.Column, len(spec.OutputOffsets))
	for i, off := range spec.OutputOffsets {
		sc := scanCols[off]
		outCols[i] = &expression.Column{
			ID:       sc.ID,
			UniqueID: sc.UniqueID,
			Index:    off,
			RetType:  sc.RetType.Clone(),
		}
		readerCols[i] = &expression.Column{
			ID:       sc.ID,
			UniqueID: sc.UniqueID,
			Index:    i,
			RetType:  sc.RetType.Clone(),
		}
	}
	readerSchema := expression.NewSchema(readerCols...)

	byItems := make([]*plannerutil.ByItems, len(spec.ByItemsDesc))
	for i, desc := range spec.ByItemsDesc {
		byItems[i] = &plannerutil.ByItems{
			Expr: readerCols[i],
			Desc: desc,
		}
	}

	var groupedRanges [][]*ranger.Range
	if len(spec.GroupedRanges) > 0 {
		groupedRanges = make([][]*ranger.Range, len(spec.GroupedRanges))
		for i, g := range spec.GroupedRanges {
			groupedRanges[i] = reBuildRanges("int", g)
		}
	}

	stats := &property.StatsInfo{RowCount: 50}
	scan := plannercore.PhysicalIndexScan{
		Table:         tblInfo,
		Index:         idxInfo,
		Columns:       []*model.ColumnInfo{c1, c2, c0},
		Ranges:        reBuildRanges("int", spec.Ranges),
		GroupedRanges: groupedRanges,
		KeepOrder:     spec.KeepOrder,
		Desc:          spec.Desc,
		ByItems:       byItems,
	}.Init(sctx.GetPlanCtx(), 0)
	scan.SetSchema(scanSchema)
	scan.SetStats(stats)
	fDSS := reflect.ValueOf(scan).Elem().FieldByName("dataSourceSchema")
	reflect.NewAt(fDSS.Type(), unsafe.Pointer(fDSS.UnsafeAddr())).Elem().Set(reflect.ValueOf(scanSchema))
	pseudo := statistics.PseudoTable(tblInfo, false, false)
	fHists := reflect.ValueOf(scan).Elem().FieldByName("tblColHists")
	reflect.NewAt(fHists.Type(), unsafe.Pointer(fHists.UnsafeAddr())).Elem().Set(reflect.ValueOf(&pseudo.HistColl))

	reader := plannercore.PhysicalIndexReader{
		IndexPlans:    []base.PhysicalPlan{scan},
		OutputColumns: outCols,
	}.Init(sctx.GetPlanCtx(), 0)
	fIP := reflect.ValueOf(reader).Elem().FieldByName("indexPlan")
	reflect.NewAt(fIP.Type(), unsafe.Pointer(fIP.UnsafeAddr())).Elem().Set(reflect.ValueOf(scan))
	// Set schema directly on physicalSchemaProducer so reader keeps custom OutputColumns and schema.
	fSP := reflect.ValueOf(reader).Elem().FieldByName("physicalSchemaProducer")
	fSch := fSP.FieldByName("schema")
	reflect.NewAt(fSch.Type(), unsafe.Pointer(fSch.UnsafeAddr())).Elem().Set(reflect.ValueOf(readerSchema))
	reader.SetStats(stats)
	return tblInfo, reader
}

func reExecIndexReader(args []json.RawMessage) (any, error) {
	var spec reIndexReaderSpec
	if err := json.Unmarshal(args[0], &spec); err != nil {
		return nil, err
	}

	sctx := &reSessionCtx{Context: mock.NewContext()}
	vars := sctx.GetSessionVars()
	vars.InitChunkSize = variable.DefInitChunkSize
	vars.MaxChunkSize = spec.MaxChunkSize
	vars.EnableChunkRPC = true
	vars.EnablePaging = spec.Paging
	vars.SetDistSQLScanConcurrency(spec.Concurrency)
	vars.SetReplicaRead(reParseReplicaRead(spec.ReplicaRead))
	vars.ReplicaClosestReadThreshold = spec.Threshold
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)
	sctx.BindDomain(domain.NewMockDomain())

	tblInfo, readerPlan := reBuildIndexTableAndPlan(sctx.Context, &spec)
	is := infoschema.MockInfoSchema([]*model.TableInfo{tblInfo})

	b := executor.NewMockExecutorBuilderForTest(sctx, is, nil)
	ebVal := reflect.ValueOf(b).Elem().FieldByName("executorBuilder").Elem()
	fForDRB := ebVal.FieldByName("forDataReaderBuilder")
	reflect.NewAt(fForDRB.Type(), unsafe.Pointer(fForDRB.UnsafeAddr())).Elem().SetBool(true)
	fDRBTS := ebVal.FieldByName("dataReaderTS")
	reflect.NewAt(fDRBTS.Type(), unsafe.Pointer(fDRBTS.UnsafeAddr())).Elem().SetUint(100)
	built := b.Build(readerPlan)
	if err := beBuilderErr(b); err != nil {
		return nil, err
	}
	ir := built.(*executor.IndexReaderExecutor)
	if spec.SetDummyCall {
		reIndexSetDummy(ir)
	}
	tblIDCheck := ir.Table().Meta().ID
	indexNames := append([]string(nil), vars.StmtCtx.IndexNames...)

	reqs := []reReqOut{}
	streamIdx := 0
	outputOffsets := reReadIndexDagOutputOffsets(ir)
	reSetIndexSelectResultHook(ir, func(_ context.Context, _ *distsqlctx.DistSQLContext, kvReq *kv.Request, fieldTypes []*types.FieldType, copPlanIDs []int) (distsql.SelectResult, error) {
		krs := kvReq.KeyRanges.AppendSelfTo(nil)
		krOut := make([]reKVRangeOut, 0, len(krs))
		for _, kr := range krs {
			krOut = append(krOut, reKVRangeOut{
				Start: hex.EncodeToString(kr.StartKey),
				End:   hex.EncodeToString(kr.EndKey),
			})
		}
		idsCopy := make([]int, len(copPlanIDs))
		copy(idsCopy, copPlanIDs)
		reqs = append(reqs, reReqOut{
			KeyRanges:     krOut,
			Desc:          kvReq.Desc,
			KeepOrder:     kvReq.KeepOrder,
			Concurrency:   kvReq.Concurrency,
			Paging:        kvReq.Paging.Enable,
			StartTS:       kvReq.StartTs,
			ReplicaRead:   reFormatReplicaRead(kvReq.ReplicaRead),
			OutputOffsets: outputOffsets,
			CopPlanIDs:    idsCopy,
		})
		var st reStreamSpec
		if streamIdx < len(spec.Streams) {
			st = spec.Streams[streamIdx]
		}
		streamIdx++
		if st.OpenErr != "" {
			return nil, errors.New(st.OpenErr)
		}
		return &reMockSelectResult{spec: st, numCols: len(fieldTypes)}, nil
	})

	ctx := context.Background()
	out := reIndexReaderOut{
		Reqs:         []reReqOut{},
		KVRanges:     []reKVRangeOut{},
		Nexts:        []reNextOut{},
		TableIDCheck: tblIDCheck,
		IndexNames:   indexNames,
	}
	if err := ir.Open(ctx); err != nil {
		out.Reqs = reqs
		out.KVRanges = reReadIndexKVRanges(ir)
		out.OpenErr = err.Error()
		_ = ir.Close()
		return out, nil
	}
	out.Reqs = reqs
	out.KVRanges = reReadIndexKVRanges(ir)

	chk := ir.NewChunk()
	numCols := chk.NumCols()
	for _, reqRows := range spec.RequiredRows {
		chk.SetRequiredRows(reqRows, spec.MaxChunkSize)
		if err := ir.Next(ctx, chk); err != nil {
			out.Nexts = append(out.Nexts, reNextOut{Err: err.Error()})
			break
		}
		rows := make([][]*int64, chk.NumRows())
		for r := 0; r < chk.NumRows(); r++ {
			row := chk.GetRow(r)
			vals := make([]*int64, numCols)
			for c := 0; c < numCols; c++ {
				if !row.IsNull(c) {
					v := row.GetInt64(c)
					vals[c] = &v
				}
			}
			rows[r] = vals
		}
		out.Nexts = append(out.Nexts, reNextOut{Rows: rows})
	}
	if err := ir.Close(); err != nil {
		out.CloseErr = err.Error()
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// index_lookup_helpers (Part 2C)
// ---------------------------------------------------------------------------

//go:linkname reGetIndexScanMaxInFlight github.com/pingcap/tidb/pkg/executor.getIndexScanMaxInFlight
func reGetIndexScanMaxInFlight(distSQLConcurrency int) int

//go:linkname reGetMergeSortIndexScanConcurrency github.com/pingcap/tidb/pkg/executor.getMergeSortIndexScanConcurrency
func reGetMergeSortIndexScanConcurrency(needMerge bool, kvRangesCount int, distSQLConcurrency int) int

//go:linkname reBuildIndexScanOutputOffsets github.com/pingcap/tidb/pkg/executor.buildIndexScanOutputOffsets
func reBuildIndexScanOutputOffsets(p *plannercore.PhysicalIndexScan, columns []*model.IndexColumn, handleLen int) ([]uint32, error)

//go:linkname reIndexLookUpSetDummy github.com/pingcap/tidb/pkg/executor.(*IndexLookUpExecutor).setDummy
func reIndexLookUpSetDummy(e *executor.IndexLookUpExecutor)

type reRuntimeStatsSpec struct {
	FetchHandleTotal         int64 `json:"fetch_handle_total"`
	FetchHandle              int64 `json:"fetch_handle"`
	TaskWait                 int64 `json:"task_wait"`
	TableRowScan             int64 `json:"table_row_scan"`
	TableTaskNum             int64 `json:"table_task_num"`
	Concurrency              int   `json:"concurrency"`
	NextWaitIndexScan        int64 `json:"next_wait_index_scan"`
	NextWaitTableLookUpBuild int64 `json:"next_wait_table_lookup_build"`
	NextWaitTableLookUpResp  int64 `json:"next_wait_table_lookup_resp"`
}

func (s reRuntimeStatsSpec) toGo() *executor.IndexLookUpRunTimeStats {
	return &executor.IndexLookUpRunTimeStats{
		FetchHandleTotal:         s.FetchHandleTotal,
		FetchHandle:              s.FetchHandle,
		TaskWait:                 s.TaskWait,
		TableRowScan:             s.TableRowScan,
		TableTaskNum:             s.TableTaskNum,
		Concurrency:              s.Concurrency,
		NextWaitIndexScan:        time.Duration(s.NextWaitIndexScan),
		NextWaitTableLookUpBuild: time.Duration(s.NextWaitTableLookUpBuild),
		NextWaitTableLookUpResp:  time.Duration(s.NextWaitTableLookUpResp),
	}
}

func reExecIndexLookUpHelpers(args []json.RawMessage) (any, error) {
	var kind string
	if err := json.Unmarshal(args[0], &kind); err != nil {
		return nil, err
	}
	switch kind {
	case "calc_batch_size":
		var estRows, initBatch, maxBatch int
		_ = json.Unmarshal(args[1], &estRows)
		_ = json.Unmarshal(args[2], &initBatch)
		_ = json.Unmarshal(args[3], &maxBatch)
		return executor.CalculateBatchSize(estRows, initBatch, maxBatch), nil
	case "max_in_flight":
		var c int
		_ = json.Unmarshal(args[1], &c)
		return reGetIndexScanMaxInFlight(c), nil
	case "merge_sort_concurrency":
		var needMerge bool
		var kvRangesCount, c int
		_ = json.Unmarshal(args[1], &needMerge)
		_ = json.Unmarshal(args[2], &kvRangesCount)
		_ = json.Unmarshal(args[3], &c)
		return reGetMergeSortIndexScanConcurrency(needMerge, kvRangesCount, c), nil
	case "lack_handles":
		var isCommon bool
		var expVals, obtVals []int64
		_ = json.Unmarshal(args[1], &isCommon)
		_ = json.Unmarshal(args[2], &expVals)
		_ = json.Unmarshal(args[3], &obtVals)
		mkH := func(v int64) kv.Handle {
			if !isCommon {
				return kv.IntHandle(v)
			}
			enc, err := codec.EncodeKey(time.UTC, nil, types.NewIntDatum(v))
			if err != nil {
				panic(err)
			}
			ch, err := kv.NewCommonHandle(enc)
			if err != nil {
				panic(err)
			}
			return ch
		}
		expected := make([]kv.Handle, len(expVals))
		for i, v := range expVals {
			expected[i] = mkH(v)
		}
		hm := kv.NewHandleMap()
		for _, v := range obtVals {
			hm.Set(mkH(v), true)
		}
		lack := executor.GetLackHandles(expected, hm)
		out := make([]string, len(lack))
		for i, h := range lack {
			out[i] = h.String()
		}
		return map[string]any{
			"lack":      out,
			"rem_count": hm.Len(),
		}, nil
	case "index_scan_offsets":
		var numIdxCols, handleLen int
		var byItemColIDs []int64
		var nonColByItem, isPartitioned, isGlobal, dynamicPrune bool
		_ = json.Unmarshal(args[1], &numIdxCols)
		_ = json.Unmarshal(args[2], &handleLen)
		_ = json.Unmarshal(args[3], &byItemColIDs)
		_ = json.Unmarshal(args[4], &nonColByItem)
		_ = json.Unmarshal(args[5], &isPartitioned)
		_ = json.Unmarshal(args[6], &isGlobal)
		_ = json.Unmarshal(args[7], &dynamicPrune)

		sctx := mock.NewContext()
		sctx.GetSessionVars().StmtCtx.UseDynamicPruneMode = dynamicPrune
		if dynamicPrune {
			sctx.GetSessionVars().PartitionPruneMode.Store(string(variable.Dynamic))
		} else {
			sctx.GetSessionVars().PartitionPruneMode.Store(string(variable.Static))
		}
		tblInfo := &model.TableInfo{ID: 10, Name: pmodel.NewCIStr("t")}
		if isPartitioned {
			tblInfo.Partition = &model.PartitionInfo{Enable: true}
		}
		idxCols := make([]*model.IndexColumn, numIdxCols)
		schemaCols := make([]*expression.Column, 0, numIdxCols+handleLen)
		for i := 0; i < numIdxCols; i++ {
			idxCols[i] = &model.IndexColumn{Name: pmodel.NewCIStr(fmt.Sprintf("c%d", i)), Offset: i}
			schemaCols = append(schemaCols, &expression.Column{
				ID:      int64(i + 1),
				Index:   i,
				RetType: types.NewFieldType(mysql.TypeLonglong),
			})
		}
		for i := 0; i < handleLen; i++ {
			schemaCols = append(schemaCols, &expression.Column{
				ID:      int64(100 + i),
				Index:   numIdxCols + i,
				RetType: types.NewFieldType(mysql.TypeLonglong),
			})
		}
		idxInfo := &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("idx"), Global: isGlobal, Columns: idxCols}
		byItems := make([]*plannerutil.ByItems, 0, len(byItemColIDs)+1)
		if nonColByItem {
			byItems = append(byItems, &plannerutil.ByItems{Expr: expression.NewOne()})
		}
		for _, cid := range byItemColIDs {
			byItems = append(byItems, &plannerutil.ByItems{
				Expr: &expression.Column{ID: cid, RetType: types.NewFieldType(mysql.TypeLonglong)},
			})
		}
		scan := plannercore.PhysicalIndexScan{
			Table:   tblInfo,
			Index:   idxInfo,
			ByItems: byItems,
		}.Init(sctx.GetPlanCtx(), 0)
		scan.SetSchema(expression.NewSchema(schemaCols...))
		offs, err := reBuildIndexScanOutputOffsets(scan, idxCols, handleLen)
		if err != nil {
			return map[string]any{"err": err.Error()}, nil
		}
		return map[string]any{"offsets": offs}, nil
	case "runtime_stats":
		var aSpec, bSpec reRuntimeStatsSpec
		_ = json.Unmarshal(args[1], &aSpec)
		_ = json.Unmarshal(args[2], &bSpec)
		a := aSpec.toGo()
		b := bSpec.toGo()
		aStr := a.String()
		cloneStr := a.Clone().String()
		a.Merge(b)
		return map[string]any{
			"a_str":      aStr,
			"clone_str":  cloneStr,
			"merged_str": a.String(),
			"tp":         a.Tp(),
		}, nil
	default:
		return nil, fmt.Errorf("index_lookup_helpers: unknown kind %q", kind)
	}
}

// ---------------------------------------------------------------------------
// index_lookup (Part 2C)
// ---------------------------------------------------------------------------

type reIndexLookUpLimit struct {
	Offset uint64 `json:"offset"`
	Count  uint64 `json:"count"`
}

// reLookUpIndexBatch represents one scripted SelectResponse chunk returned for an index scan.
// For normal mode: Rows has columns matching getRetTpsForIndexReader (optional byItem + handle col(s)).
// For pushdown mode: HitRows goes to main Chunks (channel 1), MissHandles goes to IntermediateOutputs[0] (channel 0).
type reLookUpIndexBatch struct {
	Rows        [][]any `json:"rows,omitempty"`
	HitRows     [][]any `json:"hit_rows,omitempty"`
	MissHandles [][]any `json:"miss_handles,omitempty"`
}

type reLookUpTableEntry struct {
	HandleKey []any `json:"handle_key"` // [int64] for int handle, or [int64, string] for common handle
	Row       []any `json:"row"`        // table output columns: [id, val] or [k1, k2, val]
}

type reIndexLookUpSpec struct {
	TableID             int64                  `json:"table_id"`
	IndexID             int64                  `json:"index_id"`
	IsCommonHandle      bool                   `json:"is_common_handle,omitempty"`
	CommonHandleV0Str   bool                   `json:"common_handle_v0_str,omitempty"`
	TempTable           bool                   `json:"temp_table,omitempty"`
	SetDummyCall        bool                   `json:"set_dummy_call,omitempty"`
	KeepOrder           bool                   `json:"keep_order"`
	Desc                bool                   `json:"desc"`
	IndexPaging         bool                   `json:"index_paging,omitempty"`
	IndexLookUpPushDown bool                   `json:"index_look_up_push_down,omitempty"`
	WeakConsistency     bool                   `json:"weak_consistency,omitempty"`
	ExtraTblPlan        bool                   `json:"extra_tbl_plan,omitempty"`
	IndexLookupSize     int                    `json:"index_lookup_size"`
	IndexLookupConc     int                    `json:"index_lookup_concurrency"`
	DistSQLConc         int                    `json:"dist_sql_concurrency"`
	MaxChunkSize        int                    `json:"max_chunk_size"`
	EstRows             float64                `json:"est_rows"`
	ByItemsDesc         []bool                 `json:"by_items_desc,omitempty"`
	Ranges              []reRangeSpec          `json:"ranges,omitempty"`
	GroupedRanges       [][]reRangeSpec        `json:"grouped_ranges,omitempty"`
	PushedLimit         *reIndexLookUpLimit    `json:"pushed_limit,omitempty"`
	IndexStreams        [][]reLookUpIndexBatch `json:"index_streams,omitempty"` // per index KV request -> list of response packets
	TableEntries        []reLookUpTableEntry   `json:"table_entries,omitempty"`
	IndexErr            string                 `json:"index_err,omitempty"`
	TableErr            string                 `json:"table_err,omitempty"`
	RequiredRows        []int                  `json:"required_rows"`
	CopConc             int                    `json:"cop_conc,omitempty"` // optional CopInfo concurrency reported by index response
}

type reIndexLookUpReqOut struct {
	KeyRanges     []reKVRangeOut `json:"key_ranges"`
	Desc          bool           `json:"desc"`
	KeepOrder     bool           `json:"keep_order"`
	Concurrency   int            `json:"concurrency"`
	Paging        bool           `json:"paging"`
	MinPagingSize uint64         `json:"min_paging_size"`
	Cacheable     bool           `json:"cacheable"`
	OutputOffsets []uint32       `json:"output_offsets"`
}

type reIndexLookUpOut struct {
	IndexReqs    []reIndexLookUpReqOut `json:"index_reqs"`
	TableTaskNum int64                 `json:"table_task_num"`
	Rows         [][]any               `json:"rows"`
	Err          string                `json:"err,omitempty"`
	TableIDCheck int64                 `json:"table_id_check"`
	IndexNames   []string              `json:"index_names"`
	TableIDs     []int64               `json:"table_ids"`
}

type reLookUpResultSubset struct {
	data []byte
}

func (r *reLookUpResultSubset) GetData() []byte         { return r.data }
func (r *reLookUpResultSubset) GetStartKey() kv.Key     { return nil }
func (r *reLookUpResultSubset) MemSize() int64          { return int64(len(r.data)) }
func (r *reLookUpResultSubset) RespTime() time.Duration { return 0 }

type reLookUpMockResp struct {
	packets [][]byte
	idx     int
	errMsg  string
	conc    int
}

func (r *reLookUpMockResp) Next(context.Context) (kv.ResultSubset, error) {
	if r.errMsg != "" {
		msg := r.errMsg
		r.errMsg = ""
		return nil, errors.New(msg)
	}
	if r.idx >= len(r.packets) {
		return nil, nil
	}
	pkt := r.packets[r.idx]
	r.idx++
	return &reLookUpResultSubset{data: pkt}, nil
}

func (r *reLookUpMockResp) Close() error { return nil }

func (r *reLookUpMockResp) GetConcurrency() (int, int) {
	if r.conc > 0 {
		return r.conc, 0
	}
	return 1, 0
}

func (r *reLookUpMockResp) GetStoreBatchInfo() (uint64, uint64) { return 0, 0 }

func reEncodeChunkBytes(fts []*types.FieldType, rows [][]any, v0SortKey bool) []byte {
	chk := chunk.NewChunkWithCapacity(fts, len(rows))
	var coll collate.Collator
	if v0SortKey {
		coll = collate.GetCollator("utf8mb4_general_ci")
	}
	for _, row := range rows {
		for colIdx, ft := range fts {
			if colIdx >= len(row) || row[colIdx] == nil {
				chk.AppendNull(colIdx)
				continue
			}
			if ft.EvalType() == types.ETString {
				s := row[colIdx].(string)
				if v0SortKey && !mysql.HasBinaryFlag(ft.GetFlag()) {
					chk.AppendBytes(colIdx, coll.Key(s))
				} else {
					chk.AppendString(colIdx, s)
				}
			} else {
				chk.AppendInt64(colIdx, int64(row[colIdx].(float64)))
			}
		}
	}
	return chunk.NewCodec(fts).Encode(chk)
}

func reEncodeSelectResp(fts []*types.FieldType, rows [][]any, rowsSortKey bool, interFTs []*types.FieldType, interRows [][]any, interSortKey bool) []byte {
	resp := &tipb.SelectResponse{
		EncodeType: tipb.EncodeType_TypeChunk,
	}
	if len(rows) > 0 {
		resp.Chunks = []tipb.Chunk{{RowsData: reEncodeChunkBytes(fts, rows, rowsSortKey)}}
	}
	if interFTs != nil {
		inter := &tipb.IntermediateOutput{
			EncodeType: tipb.EncodeType_TypeChunk,
		}
		if len(interRows) > 0 {
			inter.Chunks = []tipb.Chunk{{RowsData: reEncodeChunkBytes(interFTs, interRows, interSortKey)}}
		}
		resp.IntermediateOutputs = []*tipb.IntermediateOutput{inter}
	}
	data, err := resp.Marshal()
	if err != nil {
		panic(err)
	}
	return data
}

type reTableRowKV struct {
	key []byte
	row []any
}

type reLookUpMockClient struct {
	kv.Client
	mu          sync.Mutex
	spec        *reIndexLookUpSpec
	indexFTs    []*types.FieldType
	tableFTs    []*types.FieldType
	tableKVs    []reTableRowKV
	indexReqIdx int
	indexReqs   []reIndexLookUpReqOut
}

func (c *reLookUpMockClient) IsRequestTypeSupported(int64, int64) bool { return true }

func (c *reLookUpMockClient) Send(_ context.Context, req *kv.Request, _ any, _ *kv.ClientSendOption) kv.Response {
	var dag tipb.DAGRequest
	if err := dag.Unmarshal(req.Data); err != nil {
		return &reLookUpMockResp{errMsg: err.Error()}
	}
	if len(dag.Executors) == 0 {
		return &reLookUpMockResp{}
	}
	if dag.Executors[0].Tp == tipb.ExecType_TypeIndexScan {
		c.mu.Lock()
		krs := req.KeyRanges.AppendSelfTo(nil)
		krOut := make([]reKVRangeOut, 0, len(krs))
		for _, kr := range krs {
			krOut = append(krOut, reKVRangeOut{
				Start: hex.EncodeToString(kr.StartKey),
				End:   hex.EncodeToString(kr.EndKey),
			})
		}
		offs := append([]uint32(nil), dag.OutputOffsets...)
		c.indexReqs = append(c.indexReqs, reIndexLookUpReqOut{
			KeyRanges:     krOut,
			Desc:          req.Desc,
			KeepOrder:     req.KeepOrder,
			Concurrency:   req.Concurrency,
			Paging:        req.Paging.Enable,
			MinPagingSize: req.Paging.MinPagingSize,
			Cacheable:     req.Cacheable,
			OutputOffsets: offs,
		})
		streamIdx := c.indexReqIdx
		c.indexReqIdx++
		c.mu.Unlock()

		if c.spec.IndexErr != "" {
			return &reLookUpMockResp{errMsg: c.spec.IndexErr, conc: c.spec.CopConc}
		}
		var batches []reLookUpIndexBatch
		if streamIdx < len(c.spec.IndexStreams) {
			batches = c.spec.IndexStreams[streamIdx]
		}
		pkts := make([][]byte, 0, len(batches))
		for _, b := range batches {
			if c.spec.IndexLookUpPushDown {
				pkts = append(pkts, reEncodeSelectResp(c.tableFTs, b.HitRows, false, c.indexFTs, b.MissHandles, c.spec.CommonHandleV0Str))
			} else {
				pkts = append(pkts, reEncodeSelectResp(c.indexFTs, b.Rows, c.spec.CommonHandleV0Str, nil, nil, false))
			}
		}
		return &reLookUpMockResp{packets: pkts, conc: c.spec.CopConc}
	}

	// Table-side lookup request: scan matching tableKVs in req.KeyRanges.
	if c.spec.TableErr != "" {
		return &reLookUpMockResp{errMsg: c.spec.TableErr}
	}
	krs := req.KeyRanges.AppendSelfTo(nil)
	var matched [][]any
	for _, kr := range krs {
		for _, kvRow := range c.tableKVs {
			if bytes.Compare(kvRow.key, kr.StartKey) >= 0 && bytes.Compare(kvRow.key, kr.EndKey) < 0 {
				matched = append(matched, kvRow.row)
			}
		}
	}
	if len(matched) == 0 {
		return &reLookUpMockResp{}
	}
	return &reLookUpMockResp{packets: [][]byte{reEncodeSelectResp(c.tableFTs, matched, false, nil, nil, false)}}
}

func reBuildIndexLookUpTableAndPlan(sctx *mock.Context, spec *reIndexLookUpSpec) (*model.TableInfo, *plannercore.PhysicalIndexLookUpReader, []*types.FieldType, []*types.FieldType) {
	intFT := func() *types.FieldType {
		return types.NewFieldType(mysql.TypeLonglong)
	}
	strFT := func(bin bool) *types.FieldType {
		ft := types.NewFieldType(mysql.TypeVarchar)
		ft.SetFlen(64)
		ft.SetCharset(charset.CharsetUTF8MB4)
		if bin {
			ft.SetCollate(charset.CollationBin)
			ft.AddFlag(mysql.BinaryFlag)
		} else {
			ft.SetCollate("utf8mb4_general_ci")
		}
		return ft
	}

	var cols []*model.ColumnInfo
	var indices []*model.IndexInfo
	var idxScanCols []*expression.Column
	var tblCols []*expression.Column
	var extraHandleCol *expression.Column
	var commonHandleCols []*expression.Column

	if !spec.IsCommonHandle {
		pkFT := intFT()
		pkFT.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
		c0 := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("id"), Offset: 0, FieldType: *pkFT, State: model.StatePublic}
		c1 := &model.ColumnInfo{ID: 2, Name: pmodel.NewCIStr("k"), Offset: 1, FieldType: *intFT(), State: model.StatePublic}
		c2 := &model.ColumnInfo{ID: 3, Name: pmodel.NewCIStr("v"), Offset: 2, FieldType: *intFT(), State: model.StatePublic}
		cols = []*model.ColumnInfo{c0, c1, c2}
		idxInfo := &model.IndexInfo{
			ID:      spec.IndexID,
			Name:    pmodel.NewCIStr("idx_k"),
			Table:   pmodel.NewCIStr("t"),
			State:   model.StatePublic,
			Columns: []*model.IndexColumn{{Name: pmodel.NewCIStr("k"), Offset: 1, Length: types.UnspecifiedLength}},
		}
		indices = []*model.IndexInfo{idxInfo}
		idxScanCols = []*expression.Column{
			{ID: 2, UniqueID: 2, Index: 0, RetType: intFT()},
			{ID: 1, UniqueID: 1, Index: 1, RetType: pkFT.Clone()},
		}
		tblCols = []*expression.Column{
			{ID: 1, UniqueID: 1, Index: 0, RetType: pkFT.Clone()},
			{ID: 3, UniqueID: 3, Index: 1, RetType: intFT()},
		}
		extraHandleCol = tblCols[0]
	} else {
		k1FT := intFT()
		k1FT.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
		k2FT := strFT(!spec.CommonHandleV0Str)
		k2FT.AddFlag(mysql.PriKeyFlag | mysql.NotNullFlag)
		c0 := &model.ColumnInfo{ID: 1, Name: pmodel.NewCIStr("k1"), Offset: 0, FieldType: *k1FT, State: model.StatePublic}
		c1 := &model.ColumnInfo{ID: 2, Name: pmodel.NewCIStr("k2"), Offset: 1, FieldType: *k2FT, State: model.StatePublic}
		c2 := &model.ColumnInfo{ID: 3, Name: pmodel.NewCIStr("k"), Offset: 2, FieldType: *intFT(), State: model.StatePublic}
		c3 := &model.ColumnInfo{ID: 4, Name: pmodel.NewCIStr("v"), Offset: 3, FieldType: *intFT(), State: model.StatePublic}
		cols = []*model.ColumnInfo{c0, c1, c2, c3}
		pkIdx := &model.IndexInfo{
			ID:      1,
			Name:    pmodel.NewCIStr("PRIMARY"),
			Table:   pmodel.NewCIStr("t"),
			Primary: true,
			Unique:  true,
			State:   model.StatePublic,
			Columns: []*model.IndexColumn{
				{Name: pmodel.NewCIStr("k1"), Offset: 0, Length: types.UnspecifiedLength},
				{Name: pmodel.NewCIStr("k2"), Offset: 1, Length: types.UnspecifiedLength},
			},
		}
		secIdx := &model.IndexInfo{
			ID:      spec.IndexID,
			Name:    pmodel.NewCIStr("idx_k"),
			Table:   pmodel.NewCIStr("t"),
			State:   model.StatePublic,
			Columns: []*model.IndexColumn{{Name: pmodel.NewCIStr("k"), Offset: 2, Length: types.UnspecifiedLength}},
		}
		indices = []*model.IndexInfo{pkIdx, secIdx}
		idxScanCols = []*expression.Column{
			{ID: 3, UniqueID: 3, Index: 0, RetType: intFT()},
			{ID: 1, UniqueID: 1, Index: 1, RetType: k1FT.Clone()},
			{ID: 2, UniqueID: 2, Index: 2, RetType: k2FT.Clone()},
		}
		tblCols = []*expression.Column{
			{ID: 1, UniqueID: 1, Index: 0, RetType: k1FT.Clone()},
			{ID: 2, UniqueID: 2, Index: 1, RetType: k2FT.Clone()},
			{ID: 4, UniqueID: 4, Index: 2, RetType: intFT()},
		}
		commonHandleCols = []*expression.Column{tblCols[0], tblCols[1]}
	}

	secIdx := indices[len(indices)-1]
	tblInfo := &model.TableInfo{
		ID:                  spec.TableID,
		Name:                pmodel.NewCIStr("t"),
		Columns:             cols,
		Indices:             indices,
		PKIsHandle:          !spec.IsCommonHandle,
		IsCommonHandle:      spec.IsCommonHandle,
		CommonHandleVersion: 1,
		State:               model.StatePublic,
	}
	if spec.CommonHandleV0Str {
		tblInfo.CommonHandleVersion = 0
	}
	if spec.TempTable {
		tblInfo.TempTableType = model.TempTableLocal
	}

	idxSchema := expression.NewSchema(idxScanCols...)
	tblSchema := expression.NewSchema(tblCols...)

	byItems := make([]*plannerutil.ByItems, len(spec.ByItemsDesc))
	for i, desc := range spec.ByItemsDesc {
		byItems[i] = &plannerutil.ByItems{
			Expr: idxScanCols[i],
			Desc: desc,
		}
	}

	var groupedRanges [][]*ranger.Range
	if len(spec.GroupedRanges) > 0 {
		groupedRanges = make([][]*ranger.Range, len(spec.GroupedRanges))
		for i, g := range spec.GroupedRanges {
			groupedRanges[i] = reBuildRanges("int", g)
		}
	}

	estRows := spec.EstRows
	if estRows <= 0 {
		estRows = 50
	}
	stats := &property.StatsInfo{RowCount: estRows}
	pseudo := statistics.PseudoTable(tblInfo, false, false)

	var isCols []*model.ColumnInfo
	if !spec.IsCommonHandle {
		isCols = []*model.ColumnInfo{cols[1], cols[0]}
	} else {
		isCols = []*model.ColumnInfo{cols[2], cols[0], cols[1]}
	}
	idxScan := plannercore.PhysicalIndexScan{
		Table:         tblInfo,
		Index:         secIdx,
		Columns:       isCols,
		Ranges:        reBuildRanges("int", spec.Ranges),
		GroupedRanges: groupedRanges,
		KeepOrder:     spec.KeepOrder,
		Desc:          spec.Desc,
		ByItems:       byItems,
	}.Init(sctx.GetPlanCtx(), 0)
	idxScan.SetSchema(idxSchema)
	idxScan.SetStats(stats)
	fDSS := reflect.ValueOf(idxScan).Elem().FieldByName("dataSourceSchema")
	reflect.NewAt(fDSS.Type(), unsafe.Pointer(fDSS.UnsafeAddr())).Elem().Set(reflect.ValueOf(idxSchema))
	fHists := reflect.ValueOf(idxScan).Elem().FieldByName("tblColHists")
	reflect.NewAt(fHists.Type(), unsafe.Pointer(fHists.UnsafeAddr())).Elem().Set(reflect.ValueOf(&pseudo.HistColl))

	var tsCols []*model.ColumnInfo
	if !spec.IsCommonHandle {
		tsCols = []*model.ColumnInfo{cols[0], cols[2]}
	} else {
		tsCols = []*model.ColumnInfo{cols[0], cols[1], cols[3]}
	}
	tblScan := plannercore.PhysicalTableScan{
		Table:     tblInfo,
		Columns:   tsCols,
		StoreType: kv.TiKV,
	}.Init(sctx.GetPlanCtx(), 0)
	tblScan.SetSchema(tblSchema)
	tblScan.SetStats(stats)
	fTHists := reflect.ValueOf(tblScan).Elem().FieldByName("tblColHists")
	reflect.NewAt(fTHists.Type(), unsafe.Pointer(fTHists.UnsafeAddr())).Elem().Set(reflect.ValueOf(&pseudo.HistColl))

	tablePlans := []base.PhysicalPlan{tblScan}
	if spec.ExtraTblPlan {
		lim := plannercore.PhysicalLimit{Count: 100000}.Init(sctx.GetPlanCtx(), stats, 0)
		lim.SetSchema(tblSchema)
		lim.SetChildren(tblScan)
		tablePlans = append(tablePlans, lim)
	}

	indexPlans := []base.PhysicalPlan{idxScan}
	var unNatureOrders map[int]int
	if spec.IndexLookUpPushDown {
		localLookup := plannercore.PhysicalLocalIndexLookUp{
			IndexHandleOffsets: []uint32{uint32(len(idxScanCols) - 1)},
		}.Init(sctx.GetPlanCtx(), idxScan, tblScan, 0)
		localLookup.SetSchema(tblSchema)
		indexPlans = []base.PhysicalPlan{idxScan, tblScan, localLookup}
		unNatureOrders = map[int]int{0: 2, 1: 2}
	}

	var pushedLimit *plannercore.PushedDownLimit
	if spec.PushedLimit != nil {
		pushedLimit = &plannercore.PushedDownLimit{
			Offset: spec.PushedLimit.Offset,
			Count:  spec.PushedLimit.Count,
		}
	}

	rawReader := plannercore.PhysicalIndexLookUpReader{
		Paging:              spec.IndexPaging,
		ExtraHandleCol:      extraHandleCol,
		CommonHandleCols:    commonHandleCols,
		PushedLimit:         pushedLimit,
		IndexLookUpPushDown: spec.IndexLookUpPushDown,
	}
	fKO := reflect.ValueOf(&rawReader).Elem().FieldByName("keepOrder")
	reflect.NewAt(fKO.Type(), unsafe.Pointer(fKO.UnsafeAddr())).Elem().SetBool(spec.KeepOrder)
	fIP := reflect.ValueOf(&rawReader).Elem().FieldByName("indexPlan")
	reflect.NewAt(fIP.Type(), unsafe.Pointer(fIP.UnsafeAddr())).Elem().Set(reflect.ValueOf(indexPlans[len(indexPlans)-1]))
	fTP := reflect.ValueOf(&rawReader).Elem().FieldByName("tablePlan")
	reflect.NewAt(fTP.Type(), unsafe.Pointer(fTP.UnsafeAddr())).Elem().Set(reflect.ValueOf(tablePlans[len(tablePlans)-1]))
	reader := rawReader.Init(sctx.GetPlanCtx(), 0, plannerutil.IndexLookUpPushDownNone)
	if spec.IndexLookUpPushDown {
		reader.IndexLookUpPushDown = true
		reader.IndexPlans = indexPlans
		reader.IndexPlansUnNatureOrders = unNatureOrders
	}
	reader.SetStats(stats)

	// Determine effective index-side field types (after single-range byItems strip).
	idxFTs := make([]*types.FieldType, 0, len(byItems)+2)
	if len(spec.GroupedRanges) > 1 && len(byItems) > 0 {
		for _, bi := range byItems {
			idxFTs = append(idxFTs, bi.Expr.GetType(sctx.GetExprCtx().GetEvalCtx()))
		}
	}
	if !spec.IsCommonHandle {
		idxFTs = append(idxFTs, types.NewFieldType(mysql.TypeLonglong))
	} else {
		for _, hc := range commonHandleCols {
			idxFTs = append(idxFTs, hc.RetType)
		}
	}
	tblFTs := make([]*types.FieldType, len(tblCols))
	for i, c := range tblCols {
		tblFTs[i] = c.RetType
	}
	return tblInfo, reader, idxFTs, tblFTs
}

func reSortRowSlice(rows [][]any) {
	slices.SortFunc(rows, func(a, b []any) int {
		for i := 0; i < len(a) && i < len(b); i++ {
			sa := fmt.Sprintf("%v", a[i])
			sb := fmt.Sprintf("%v", b[i])
			if sa < sb {
				return -1
			}
			if sa > sb {
				return 1
			}
		}
		return len(a) - len(b)
	})
}

func reExecIndexLookUp(args []json.RawMessage) (any, error) {
	var spec reIndexLookUpSpec
	if err := json.Unmarshal(args[0], &spec); err != nil {
		return nil, err
	}

	sctx := &reSessionCtx{Context: mock.NewContext()}
	vars := sctx.GetSessionVars()
	vars.InitChunkSize = variable.DefInitChunkSize
	vars.MaxChunkSize = spec.MaxChunkSize
	vars.EnableChunkRPC = true
	vars.EnablePaging = spec.IndexPaging
	vars.IndexLookupSize = spec.IndexLookupSize
	if spec.IndexLookupConc > 0 {
		vars.SetIndexLookupConcurrency(spec.IndexLookupConc)
	}
	if spec.DistSQLConc > 0 {
		vars.SetDistSQLScanConcurrency(spec.DistSQLConc)
	}
	vars.StmtCtx.WeakConsistency = spec.WeakConsistency
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)
	vars.StmtCtx.RuntimeStatsColl = execdetails.NewRuntimeStatsColl(nil)
	sctx.BindDomain(domain.NewMockDomain())

	tblInfo, readerPlan, idxFTs, tblFTs := reBuildIndexLookUpTableAndPlan(sctx.Context, &spec)

	// Pre-encode table entries as (record_key, row) for key-range scanning.
	tableKVs := make([]reTableRowKV, 0, len(spec.TableEntries))
	for _, te := range spec.TableEntries {
		var h kv.Handle
		if !spec.IsCommonHandle {
			h = kv.IntHandle(int64(te.HandleKey[0].(float64)))
		} else {
			d0 := types.NewIntDatum(int64(te.HandleKey[0].(float64)))
			var d1 types.Datum
			if spec.CommonHandleV0Str {
				d1 = types.NewCollationStringDatum(te.HandleKey[1].(string), "utf8mb4_general_ci")
			} else {
				d1 = types.NewStringDatum(te.HandleKey[1].(string))
			}
			enc, err := codec.EncodeKey(time.UTC, nil, d0, d1)
			if err != nil {
				return nil, err
			}
			ch, err := kv.NewCommonHandle(enc)
			if err != nil {
				return nil, err
			}
			h = ch
		}
		rk := tablecodec.EncodeRowKeyWithHandle(spec.TableID, h)
		tableKVs = append(tableKVs, reTableRowKV{key: rk, row: te.Row})
	}
	slices.SortFunc(tableKVs, func(a, b reTableRowKV) int {
		return bytes.Compare(a.key, b.key)
	})

	mockClient := &reLookUpMockClient{
		spec:     &spec,
		indexFTs: idxFTs,
		tableFTs: tblFTs,
		tableKVs: tableKVs,
	}
	sctx.client = mockClient

	is := infoschema.MockInfoSchema([]*model.TableInfo{tblInfo})
	b := executor.NewMockExecutorBuilderForTest(sctx, is, nil)
	ebVal := reflect.ValueOf(b).Elem().FieldByName("executorBuilder").Elem()
	fForDRB := ebVal.FieldByName("forDataReaderBuilder")
	reflect.NewAt(fForDRB.Type(), unsafe.Pointer(fForDRB.UnsafeAddr())).Elem().SetBool(true)
	fDRBTS := ebVal.FieldByName("dataReaderTS")
	reflect.NewAt(fDRBTS.Type(), unsafe.Pointer(fDRBTS.UnsafeAddr())).Elem().SetUint(100)
	built := b.Build(readerPlan)
	if err := beBuilderErr(b); err != nil {
		return nil, err
	}
	ilu := built.(*executor.IndexLookUpExecutor)
	if spec.SetDummyCall {
		reIndexLookUpSetDummy(ilu)
	}
	tblIDCheck := ilu.Table().Meta().ID
	indexNames := append([]string(nil), vars.StmtCtx.IndexNames...)
	tableIDs := append([]int64(nil), vars.StmtCtx.TableIDs...)

	ctx := context.Background()
	out := reIndexLookUpOut{
		IndexReqs:    []reIndexLookUpReqOut{},
		Rows:         [][]any{},
		TableIDCheck: tblIDCheck,
		IndexNames:   indexNames,
		TableIDs:     tableIDs,
	}
	if err := ilu.Open(ctx); err != nil {
		out.Err = err.Error()
		_ = ilu.Close()
		out.IndexReqs = mockClient.indexReqs
		return out, nil
	}

	chk := ilu.NewChunk()
	numCols := chk.NumCols()
	reqIdx := 0
	for {
		reqRows := spec.MaxChunkSize
		if reqIdx < len(spec.RequiredRows) {
			reqRows = spec.RequiredRows[reqIdx]
			reqIdx++
		}
		chk.SetRequiredRows(reqRows, spec.MaxChunkSize)
		if err := ilu.Next(ctx, chk); err != nil {
			out.Err = err.Error()
			break
		}
		if chk.NumRows() == 0 {
			break
		}
		for r := 0; r < chk.NumRows(); r++ {
			row := chk.GetRow(r)
			vals := make([]any, numCols)
			for c := 0; c < numCols; c++ {
				if row.IsNull(c) {
					vals[c] = nil
				} else if tblFTs[c].EvalType() == types.ETString {
					vals[c] = strings.Clone(row.GetString(c))
				} else {
					vals[c] = row.GetInt64(c)
				}
			}
			out.Rows = append(out.Rows, vals)
		}
	}
	if err := ilu.Close(); err != nil && out.Err == "" {
		out.Err = err.Error()
	}

	mockClient.mu.Lock()
	out.IndexReqs = append([]reIndexLookUpReqOut{}, mockClient.indexReqs...)
	mockClient.mu.Unlock()

	if !spec.KeepOrder {
		reSortRowSlice(out.Rows)
	}
	// Read TableTaskNum from registered IndexLookUpRunTimeStats.
	fStats := reflect.ValueOf(ilu).Elem().FieldByName("stats")
	if !fStats.IsNil() {
		st := reflect.NewAt(fStats.Type(), unsafe.Pointer(fStats.UnsafeAddr())).Elem().Interface().(*executor.IndexLookUpRunTimeStats)
		if st != nil {
			out.TableTaskNum = st.TableTaskNum
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Generator & Dispatch
// ---------------------------------------------------------------------------

func reIntPtr(v int64) *int64 { return &v }

func readexecGenerate(g *vec.Gen, r *rand.Rand) {
	// 1. closest_read_adjuster (175 cases: deterministic edge cases + boundary grid)
	rrModes := []string{"leader", "follower", "mixed", "closest-adaptive", "closest"}
	zones := []string{"", "us-east-1", "zone-a"}
	edgeThresholds := []int64{0, 1, 100, 4096, 1 << 20}
	for _, rr := range rrModes {
		for _, thr := range edgeThresholds {
			g.Add("sys/adjuster/mode", "closest_read_adjuster", rr, thr, float64(thr*3), 3, "zone-a")
		}
	}
	// Exact boundary & truncation cases under closest-adaptive.
	for _, thr := range []int64{1, 10, 100, 4096, 65536} {
		for _, cnt := range []int{1, 2, 3, 7, 16} {
			base := float64(thr * int64(cnt))
			for _, delta := range []float64{-1.0, -0.01, 0.0, 0.5, 0.99, 1.0} {
				size := math.Max(0, base+delta)
				g.Add("sys/adjuster/boundary", "closest_read_adjuster", "closest-adaptive", thr, size, cnt, zones[r.IntN(len(zones))])
			}
		}
	}

	// 2. table_reader (~200 cases covering all Part 2A behaviors)
	mkRows := func(n, cols int, base int64) [][]*int64 {
		rows := make([][]*int64, n)
		for i := 0; i < n; i++ {
			row := make([]*int64, cols)
			for c := 0; c < cols; c++ {
				if (i+c)%7 == 0 && c > 0 {
					row[c] = nil
				} else {
					row[c] = reIntPtr(base + int64(i*10+c))
				}
			}
			rows[i] = row
		}
		return rows
	}

	// Unsigned crossing MaxInt64: ascending, descending, and unordered.
	uMax := fmt.Sprintf("%d", uint64(math.MaxUint64))
	uHalfLo := fmt.Sprintf("%d", uint64(math.MaxInt64)-5)
	uHalfHi := fmt.Sprintf("%d", uint64(math.MaxInt64)+5)
	for _, keepOrder := range []bool{true, false} {
		for _, desc := range []bool{false, true} {
			for _, paging := range []bool{false, true} {
				for _, rngs := range [][]reRangeSpec{
					{{Low: []any{"0"}, High: []any{uMax}}},
					{{Low: []any{uHalfLo}, High: []any{uHalfHi}}},
					{
						{Low: []any{"1"}, High: []any{"100"}},
						{Low: []any{fmt.Sprintf("%d", uint64(math.MaxInt64)+10)}, High: []any{fmt.Sprintf("%d", uint64(math.MaxInt64)+20)}},
					},
				} {
					g.Add("sys/table_reader/uint_split", "table_reader", reTableReaderSpec{
						TableID:      101,
						HandleKind:   "uint",
						KeepOrder:    keepOrder,
						Desc:         desc,
						Paging:       paging,
						Concurrency:  15,
						ReplicaRead:  "leader",
						Threshold:    4096,
						MaxChunkSize: 32,
						Ranges:       rngs,
						Streams: []reStreamSpec{
							{Rows: mkRows(4, 2, 100)},
							{Rows: mkRows(3, 2, 200)},
						},
						RequiredRows: []int{2, 2, 2, 2, 2},
					})
				}
			}
		}
	}

	// Virtual generated columns (schema order [id, v2, a, v1] reversed from definition order [id, a, v1, v2]).
	for _, desc := range []bool{false, true} {
		for _, keepOrder := range []bool{false, true} {
			vRows := [][]*int64{
				{reIntPtr(1), nil, reIntPtr(5), nil},
				{reIntPtr(2), nil, reIntPtr(-3), nil},
				{reIntPtr(3), nil, nil, nil},
				{reIntPtr(4), nil, reIntPtr(0), nil},
				{reIntPtr(5), nil, reIntPtr(42), nil},
			}
			g.Add("sys/table_reader/virtual", "table_reader", reTableReaderSpec{
				TableID:      102,
				HandleKind:   "int",
				VirtualCols:  true,
				KeepOrder:    keepOrder,
				Desc:         desc,
				Paging:       false,
				Concurrency:  4,
				ReplicaRead:  "leader",
				Threshold:    4096,
				MaxChunkSize: 32,
				Ranges:       []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}},
				Streams:      []reStreamSpec{{Rows: vRows}},
				RequiredRows: []int{2, 2, 5, 5},
			})
		}
	}

	// Common handle ranges.
	for _, desc := range []bool{false, true} {
		for _, keepOrder := range []bool{false, true} {
			g.Add("sys/table_reader/common", "table_reader", reTableReaderSpec{
				TableID:      103,
				HandleKind:   "common",
				KeepOrder:    keepOrder,
				Desc:         desc,
				Paging:       true,
				Concurrency:  15,
				ReplicaRead:  "leader",
				Threshold:    4096,
				MaxChunkSize: 64,
				Ranges: []reRangeSpec{
					{Low: []any{float64(1), "a"}, High: []any{float64(5), "z"}},
					{Low: []any{float64(10), "aa"}, High: []any{float64(20), "zz"}, LowExclude: true, HighExclude: true},
				},
				Streams:      []reStreamSpec{{Rows: mkRows(5, 2, 50)}},
				RequiredRows: []int{3, 10, 10},
			})
		}
	}

	// Empty ranges, temporary-table dummy mode, setDummy mode (including unsigned desc swap).
	for _, desc := range []bool{false, true} {
		g.Add("sys/table_reader/empty", "table_reader", reTableReaderSpec{
			TableID:      104,
			HandleKind:   "int",
			KeepOrder:    true,
			Desc:         desc,
			Concurrency:  15,
			ReplicaRead:  "leader",
			MaxChunkSize: 32,
			Ranges:       []reRangeSpec{},
			Streams:      []reStreamSpec{{Rows: nil}},
			RequiredRows: []int{10},
		})
		g.Add("sys/table_reader/temp_dummy", "table_reader", reTableReaderSpec{
			TableID:      105,
			HandleKind:   "uint",
			TempTable:    true,
			KeepOrder:    true,
			Desc:         desc,
			Concurrency:  15,
			ReplicaRead:  "leader",
			MaxChunkSize: 32,
			Ranges:       []reRangeSpec{{Low: []any{"0"}, High: []any{uMax}}},
			RequiredRows: []int{10},
		})
		g.Add("sys/table_reader/set_dummy", "table_reader", reTableReaderSpec{
			TableID:      106,
			HandleKind:   "int",
			SetDummyCall: true,
			KeepOrder:    false,
			Desc:         desc,
			Concurrency:  15,
			ReplicaRead:  "leader",
			MaxChunkSize: 32,
			Ranges:       []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(5)}}},
			RequiredRows: []int{5},
		})
	}

	// Error propagation: Open error (1st and 2nd split), Next error (1st and 2nd split), Close error (both streams).
	errSpecs := []reTableReaderSpec{
		{
			TableID: 107, HandleKind: "int", KeepOrder: false, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			Ranges:       []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}},
			Streams:      []reStreamSpec{{OpenErr: "open boom 1"}},
			RequiredRows: []int{5},
		},
		{
			TableID: 108, HandleKind: "uint", KeepOrder: true, Desc: false, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			Ranges:       []reRangeSpec{{Low: []any{"0"}, High: []any{uMax}}},
			Streams:      []reStreamSpec{{Rows: mkRows(2, 2, 1)}, {OpenErr: "open boom 2"}},
			RequiredRows: []int{5},
		},
		{
			TableID: 109, HandleKind: "int", KeepOrder: true, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			Ranges:       []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}},
			Streams:      []reStreamSpec{{Rows: mkRows(4, 2, 1), NextErr: "next boom 1", ErrAfter: 1}},
			RequiredRows: []int{2, 2, 2},
		},
		{
			TableID: 110, HandleKind: "uint", KeepOrder: true, Desc: false, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			Ranges: []reRangeSpec{{Low: []any{"0"}, High: []any{uMax}}},
			Streams: []reStreamSpec{
				{Rows: mkRows(2, 2, 1)},
				{Rows: mkRows(2, 2, 20), NextErr: "next boom 2", ErrAfter: 0},
			},
			RequiredRows: []int{5, 5, 5},
		},
		{
			TableID: 111, HandleKind: "uint", KeepOrder: true, Desc: false, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			Ranges: []reRangeSpec{{Low: []any{"0"}, High: []any{uMax}}},
			Streams: []reStreamSpec{
				{Rows: mkRows(1, 2, 1), CloseErr: "close err first"},
				{Rows: mkRows(1, 2, 20), CloseErr: "close err second"},
			},
			RequiredRows: []int{5, 5, 5},
		},
		{
			TableID: 112, HandleKind: "uint", KeepOrder: true, Desc: true, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			Ranges: []reRangeSpec{{Low: []any{"0"}, High: []any{uMax}}},
			Streams: []reStreamSpec{
				{Rows: mkRows(1, 2, 1)},
				{Rows: mkRows(1, 2, 20), CloseErr: "close err only second"},
			},
			RequiredRows: []int{5, 5, 5},
		},
	}
	for _, sp := range errSpecs {
		g.Add("sys/table_reader/err", "table_reader", sp)
	}

	// Randomized table_reader cases to exceed Min=180 cleanly.
	concurrencies := []int{1, 2, 4, 15}
	maxChunks := []int{4, 16, 32, 64}
	for i := 0; i < 160; i++ {
		hk := []string{"int", "uint", "common"}[r.IntN(3)]
		keepOrder := r.IntN(2) == 0
		desc := r.IntN(2) == 0
		paging := r.IntN(2) == 0
		virt := hk != "common" && r.IntN(4) == 0
		rr := rrModes[r.IntN(len(rrModes))]
		conc := concurrencies[r.IntN(len(concurrencies))]
		maxChk := maxChunks[r.IntN(len(maxChunks))]

		var rngs []reRangeSpec
		switch hk {
		case "int":
			lo := int64(r.IntN(200) - 100)
			hi := lo + int64(r.IntN(200))
			rngs = []reRangeSpec{{Low: []any{float64(lo)}, High: []any{float64(hi)}, LowExclude: r.IntN(2) == 0, HighExclude: r.IntN(2) == 0}}
		case "uint":
			if r.IntN(2) == 0 {
				rngs = []reRangeSpec{{Low: []any{fmt.Sprintf("%d", uint64(r.IntN(50)))}, High: []any{fmt.Sprintf("%d", uint64(math.MaxInt64)+uint64(r.IntN(50)+1))}}}
			} else {
				lo := uint64(r.IntN(100))
				hi := lo + uint64(r.IntN(100))
				rngs = []reRangeSpec{{Low: []any{fmt.Sprintf("%d", lo)}, High: []any{fmt.Sprintf("%d", hi)}}}
			}
		case "common":
			lo := int64(r.IntN(50))
			hi := lo + int64(r.IntN(50)+1)
			rngs = []reRangeSpec{{Low: []any{float64(lo), "a"}, High: []any{float64(hi), "z"}}}
		}

		nCols := 2
		if virt {
			nCols = 4
		}
		streams := []reStreamSpec{
			{Rows: mkRows(r.IntN(10), nCols, int64(i*100))},
			{Rows: mkRows(r.IntN(8), nCols, int64(i*100+50))},
		}
		if virt {
			// Ensure virtual column positions (indices 1 and 3) start NULL from copr mock.
			for si := range streams {
				for ri := range streams[si].Rows {
					streams[si].Rows[ri][1] = nil
					streams[si].Rows[ri][3] = nil
				}
			}
		}
		nNexts := 1 + r.IntN(5)
		reqRows := make([]int, nNexts)
		for j := range reqRows {
			reqRows[j] = 1 + r.IntN(maxChk)
		}
		g.Add(fmt.Sprintf("rand/table_reader/%d", i), "table_reader", reTableReaderSpec{
			TableID:      int64(200 + i),
			HandleKind:   hk,
			VirtualCols:  virt,
			KeepOrder:    keepOrder,
			Desc:         desc,
			Paging:       paging,
			Concurrency:  conc,
			ReplicaRead:  rr,
			Threshold:    4096,
			MaxChunkSize: maxChk,
			Ranges:       rngs,
			Streams:      streams,
			RequiredRows: reqRows,
		})
	}

	// 3. index_reader (~210 cases covering all Part 2B behaviors)
	// 3a. Single-range & multi-range secondary index + common handle (index_id=-1),
	//     desc on/off, keepOrder on/off, custom OutputOffsets.
	offsetVariants := [][]int{{0}, {1, 0}, {0, 1}, {2, 0}, {2, 1, 0}}
	for _, idxID := range []int64{1, -1} {
		for _, desc := range []bool{false, true} {
			for _, keepOrder := range []bool{false, true} {
				for _, offs := range offsetVariants {
					g.Add("sys/index_reader/basic", "index_reader", reIndexReaderSpec{
						TableID:       301,
						IndexID:       idxID,
						KeepOrder:     keepOrder,
						Desc:          desc,
						Paging:        desc,
						Concurrency:   15,
						ReplicaRead:   "leader",
						Threshold:     4096,
						MaxChunkSize:  32,
						OutputOffsets: offs,
						Ranges: []reRangeSpec{
							{Low: []any{float64(10), float64(1)}, High: []any{float64(20), float64(100)}},
							{Low: []any{float64(1), float64(0)}, High: []any{float64(5), float64(50)}, LowExclude: true, HighExclude: true},
						},
						Streams:      []reStreamSpec{{Rows: mkRows(5, len(offs), 100)}},
						RequiredRows: []int{2, 2, 4, 4},
					})
				}
			}
		}
	}

	// 3b. Multi-group / grouped-range with needMergeSort ON (by_items non-empty, >1 KV range)
	//     and OFF (by_items empty, or 1 KV range), ASC & DESC merge, multi-column ByItems.
	mkSortedRows := func(vals []int64, nCols int, desc bool) [][]*int64 {
		rows := make([][]*int64, len(vals))
		for i := range vals {
			idx := i
			if desc {
				idx = len(vals) - 1 - i
			}
			row := make([]*int64, nCols)
			for c := 0; c < nCols; c++ {
				row[c] = reIntPtr(vals[idx]*10 + int64(c))
			}
			rows[i] = row
		}
		return rows
	}
	for _, desc := range []bool{false, true} {
		for _, idxID := range []int64{2, -1} {
			// Grouped ranges with merge sort ON (3 groups -> 3 separate KV requests merged by col 0).
			g.Add("sys/index_reader/merge_sort", "index_reader", reIndexReaderSpec{
				TableID:       302,
				IndexID:       idxID,
				KeepOrder:     true,
				Desc:          desc,
				Paging:        false,
				Concurrency:   4,
				ReplicaRead:   "leader",
				Threshold:     4096,
				MaxChunkSize:  32,
				OutputOffsets: []int{1, 0},
				ByItemsDesc:   []bool{desc},
				GroupedRanges: [][]reRangeSpec{
					{{Low: []any{float64(30), float64(0)}, High: []any{float64(30), float64(100)}}},
					{{Low: []any{float64(10), float64(0)}, High: []any{float64(10), float64(100)}}},
					{{Low: []any{float64(20), float64(0)}, High: []any{float64(20), float64(100)}}},
				},
				Streams: []reStreamSpec{
					{Rows: mkSortedRows([]int64{1, 4, 7}, 2, desc)},
					{Rows: mkSortedRows([]int64{2, 5, 8}, 2, desc)},
					{Rows: mkSortedRows([]int64{3, 6, 9}, 2, desc)},
				},
				RequiredRows: []int{2, 3, 2, 5, 5},
			})
			// Single range with ByItems non-empty -> needMergeSort is FALSE (single KV request).
			g.Add("sys/index_reader/single_range_by_items", "index_reader", reIndexReaderSpec{
				TableID:       303,
				IndexID:       idxID,
				KeepOrder:     true,
				Desc:          desc,
				Paging:        false,
				Concurrency:   4,
				ReplicaRead:   "leader",
				Threshold:     4096,
				MaxChunkSize:  32,
				OutputOffsets: []int{0, 1},
				ByItemsDesc:   []bool{desc},
				Ranges:        []reRangeSpec{{Low: []any{float64(1), float64(0)}, High: []any{float64(10), float64(100)}}},
				Streams:       []reStreamSpec{{Rows: mkSortedRows([]int64{1, 2, 3, 4}, 2, desc)}},
				RequiredRows:  []int{2, 10, 10},
			})
		}
	}

	// 3c. Empty ranges, temp_table dummy, setDummy, mid-stream Close, and Open/Next/Close error propagation.
	for _, desc := range []bool{false, true} {
		g.Add("sys/index_reader/empty", "index_reader", reIndexReaderSpec{
			TableID:       304,
			IndexID:       1,
			KeepOrder:     true,
			Desc:          desc,
			Concurrency:   15,
			ReplicaRead:   "leader",
			MaxChunkSize:  32,
			OutputOffsets: []int{0},
			Ranges:        []reRangeSpec{},
			Streams:       []reStreamSpec{{Rows: nil}},
			RequiredRows:  []int{5},
		})
		g.Add("sys/index_reader/temp_dummy", "index_reader", reIndexReaderSpec{
			TableID:       305,
			IndexID:       1,
			TempTable:     true,
			KeepOrder:     false,
			Desc:          desc,
			Concurrency:   15,
			ReplicaRead:   "leader",
			MaxChunkSize:  32,
			OutputOffsets: []int{0, 2},
			Ranges: []reRangeSpec{
				{Low: []any{float64(20), float64(0)}, High: []any{float64(30), float64(10)}},
				{Low: []any{float64(1), float64(0)}, High: []any{float64(5), float64(10)}},
			},
			RequiredRows: []int{5},
		})
		g.Add("sys/index_reader/set_dummy", "index_reader", reIndexReaderSpec{
			TableID:       306,
			IndexID:       1,
			SetDummyCall:  true,
			KeepOrder:     true,
			Desc:          desc,
			Concurrency:   15,
			ReplicaRead:   "leader",
			MaxChunkSize:  32,
			OutputOffsets: []int{0},
			Ranges:        []reRangeSpec{{Low: []any{float64(1), float64(1)}, High: []any{float64(5), float64(5)}}},
			RequiredRows:  []int{5},
		})
	}
	idxErrSpecs := []reIndexReaderSpec{
		{
			TableID: 307, IndexID: 1, KeepOrder: false, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			OutputOffsets: []int{0},
			Ranges:        []reRangeSpec{{Low: []any{float64(1), float64(1)}, High: []any{float64(10), float64(10)}}},
			Streams:       []reStreamSpec{{OpenErr: "idx open boom 1"}},
			RequiredRows:  []int{5},
		},
		{
			// Mid-open k-th failure in needMergeSort loop.
			TableID: 308, IndexID: 1, KeepOrder: true, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			OutputOffsets: []int{0, 1},
			ByItemsDesc:   []bool{false},
			GroupedRanges: [][]reRangeSpec{
				{{Low: []any{float64(1), float64(0)}, High: []any{float64(1), float64(10)}}},
				{{Low: []any{float64(2), float64(0)}, High: []any{float64(2), float64(10)}}},
				{{Low: []any{float64(3), float64(0)}, High: []any{float64(3), float64(10)}}},
			},
			Streams: []reStreamSpec{
				{Rows: mkSortedRows([]int64{1, 2}, 2, false)},
				{OpenErr: "idx open boom 2"},
				{Rows: mkSortedRows([]int64{3, 4}, 2, false)},
			},
			RequiredRows: []int{5},
		},
		{
			TableID: 309, IndexID: 1, KeepOrder: true, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			OutputOffsets: []int{0, 1},
			Ranges:        []reRangeSpec{{Low: []any{float64(1), float64(1)}, High: []any{float64(10), float64(10)}}},
			Streams:       []reStreamSpec{{Rows: mkRows(4, 2, 1), NextErr: "idx next boom", ErrAfter: 1}},
			RequiredRows:  []int{2, 2, 2},
		},
		{
			TableID: 310, IndexID: 1, KeepOrder: true, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			OutputOffsets: []int{0, 1},
			Ranges:        []reRangeSpec{{Low: []any{float64(1), float64(1)}, High: []any{float64(10), float64(10)}}},
			Streams:       []reStreamSpec{{Rows: mkRows(6, 2, 1), CloseErr: "idx close boom"}},
			// Mid-stream stop: consume only 2 of 6 rows, then Close.
			RequiredRows: []int{2},
		},
		{
			TableID: 311, IndexID: 1, KeepOrder: true, Concurrency: 4, ReplicaRead: "leader", MaxChunkSize: 32,
			OutputOffsets: []int{0, 1},
			ByItemsDesc:   []bool{false},
			GroupedRanges: [][]reRangeSpec{
				{{Low: []any{float64(1), float64(0)}, High: []any{float64(1), float64(10)}}},
				{{Low: []any{float64(2), float64(0)}, High: []any{float64(2), float64(10)}}},
			},
			Streams: []reStreamSpec{
				{Rows: mkSortedRows([]int64{1, 3, 5}, 2, false), CloseErr: "merge close boom 1"},
				{Rows: mkSortedRows([]int64{2, 4, 6}, 2, false)},
			},
			// Mid-stream stop on merged stream: take 2 of 6 rows, then Close.
			RequiredRows: []int{2},
		},
	}
	for _, sp := range idxErrSpecs {
		g.Add("sys/index_reader/err", "index_reader", sp)
	}

	// 3d. Randomized index_reader cases to exceed Min=180 cleanly.
	for i := 0; i < 140; i++ {
		idxID := int64(1 + r.IntN(3))
		if r.IntN(4) == 0 {
			idxID = -1
		}
		keepOrder := r.IntN(2) == 0
		desc := r.IntN(2) == 0
		paging := r.IntN(2) == 0
		rr := rrModes[r.IntN(len(rrModes))]
		conc := concurrencies[r.IntN(len(concurrencies))]
		maxChk := maxChunks[r.IntN(len(maxChunks))]
		offs := offsetVariants[r.IntN(len(offsetVariants))]
		useMerge := r.IntN(3) == 0

		var rngs []reRangeSpec
		var grouped [][]reRangeSpec
		var byItemsDesc []bool
		var streams []reStreamSpec
		if useMerge {
			nGroups := 2 + r.IntN(2)
			byItemsDesc = []bool{desc}
			grouped = make([][]reRangeSpec, nGroups)
			streams = make([]reStreamSpec, nGroups)
			for gi := 0; gi < nGroups; gi++ {
				baseK := int64((nGroups - gi) * 100)
				grouped[gi] = []reRangeSpec{{Low: []any{float64(baseK), float64(0)}, High: []any{float64(baseK), float64(50)}}}
				nRows := r.IntN(6)
				vals := make([]int64, nRows)
				cur := int64(r.IntN(10))
				for ri := 0; ri < nRows; ri++ {
					cur += int64(1 + r.IntN(5))
					vals[ri] = cur
				}
				streams[gi] = reStreamSpec{Rows: mkSortedRows(vals, len(offs), desc)}
			}
		} else {
			nRanges := 1 + r.IntN(3)
			rngs = make([]reRangeSpec, nRanges)
			for ri := 0; ri < nRanges; ri++ {
				lo := int64(r.IntN(200) - 100)
				hi := lo + int64(r.IntN(50))
				rngs[ri] = reRangeSpec{
					Low:         []any{float64(lo), float64(0)},
					High:        []any{float64(hi), float64(100)},
					LowExclude:  r.IntN(2) == 0,
					HighExclude: r.IntN(2) == 0,
				}
			}
			streams = []reStreamSpec{{Rows: mkRows(r.IntN(10), len(offs), int64(i*100))}}
		}
		nNexts := 1 + r.IntN(5)
		reqRows := make([]int, nNexts)
		for j := range reqRows {
			reqRows[j] = 1 + r.IntN(maxChk)
		}
		g.Add(fmt.Sprintf("rand/index_reader/%d", i), "index_reader", reIndexReaderSpec{
			TableID:       int64(400 + i),
			IndexID:       idxID,
			KeepOrder:     keepOrder,
			Desc:          desc,
			Paging:        paging,
			Concurrency:   conc,
			ReplicaRead:   rr,
			Threshold:     4096,
			MaxChunkSize:  maxChk,
			OutputOffsets: offs,
			ByItemsDesc:   byItemsDesc,
			Ranges:        rngs,
			GroupedRanges: grouped,
			Streams:       streams,
			RequiredRows:  reqRows,
		})
	}

	// 4. index_lookup_helpers (Part 2C, >= 150 cases)
	// 4a. CalculateBatchSize
	for _, est := range []int{0, 1, 5, 32, 64, 100, 127, 128, 129, 500, 20000, 50000} {
		for _, initB := range []int{1, 16, 32, 64, 1024} {
			for _, maxB := range []int{32, 100, 20000} {
				g.Add("sys/ilu_helpers/calc_batch", "index_lookup_helpers", "calc_batch_size", est, initB, maxB)
			}
		}
	}
	// 4b. getIndexScanMaxInFlight
	for _, c := range []int{-5, 0, 1, 2, 3, 5, 15, 32, 64} {
		g.Add("sys/ilu_helpers/max_in_flight", "index_lookup_helpers", "max_in_flight", c)
	}
	// 4c. getMergeSortIndexScanConcurrency (including c=1 boundary!)
	for _, needMerge := range []bool{false, true} {
		for _, nRanges := range []int{0, 1, 2, 3, 4, 8, 16, 32, 100} {
			for _, c := range []int{-1, 0, 1, 2, 4, 5, 15} {
				g.Add("sys/ilu_helpers/merge_conc", "index_lookup_helpers", "merge_sort_concurrency", needMerge, nRanges, c)
			}
		}
	}
	// 4d. GetLackHandles (int & common)
	for _, isCommon := range []bool{false, true} {
		g.Add("sys/ilu_helpers/lack_none", "index_lookup_helpers", "lack_handles", isCommon, []int64{1, 2, 3, 4, 5}, []int64{1, 2, 3, 4, 5})
		g.Add("sys/ilu_helpers/lack_some", "index_lookup_helpers", "lack_handles", isCommon, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, []int64{1, 5, 10})
		g.Add("sys/ilu_helpers/lack_all", "index_lookup_helpers", "lack_handles", isCommon, []int64{10, 20, 30}, []int64{})
		g.Add("sys/ilu_helpers/lack_dup", "index_lookup_helpers", "lack_handles", isCommon, []int64{1, 1, 2, 2, 3}, []int64{1, 2})
	}
	// 4e. buildIndexScanOutputOffsets
	for _, isPart := range []bool{false, true} {
		for _, isGlob := range []bool{false, true} {
			for _, dynPrune := range []bool{false, true} {
				for _, byIDs := range [][]int64{{}, {1}, {2, 1}} {
					g.Add("sys/ilu_helpers/offsets", "index_lookup_helpers", "index_scan_offsets", 2, 1, byIDs, false, isPart, isGlob, dynPrune)
				}
			}
		}
	}
	g.Add("sys/ilu_helpers/offsets_common", "index_lookup_helpers", "index_scan_offsets", 2, 2, []int64{1}, false, false, false, false)
	g.Add("sys/ilu_helpers/offsets_noncol_err", "index_lookup_helpers", "index_scan_offsets", 2, 1, []int64{}, true, false, false, false)
	g.Add("sys/ilu_helpers/offsets_missing_err", "index_lookup_helpers", "index_scan_offsets", 2, 1, []int64{999}, false, false, false, false)
	// 4f. IndexLookUpRunTimeStats (including spec §14.5 bug parity: indexScan==0 && tableScan==0 && NextWait>0)
	statsCases := [][2]reRuntimeStatsSpec{
		{
			{FetchHandleTotal: 5e9, FetchHandle: 2e9, TaskWait: 2e9, TableRowScan: 2e9, TableTaskNum: 2, Concurrency: 1, NextWaitIndexScan: 1e9, NextWaitTableLookUpBuild: 2e9, NextWaitTableLookUpResp: 3e9},
			{FetchHandleTotal: 5e9, FetchHandle: 2e9, TaskWait: 2e9, TableRowScan: 2e9, TableTaskNum: 2, Concurrency: 1, NextWaitIndexScan: 1e9, NextWaitTableLookUpBuild: 2e9, NextWaitTableLookUpResp: 3e9},
		},
		{
			{FetchHandle: 0, TableRowScan: 0, NextWaitIndexScan: 1e6, NextWaitTableLookUpBuild: 2e6, NextWaitTableLookUpResp: 3e6, Concurrency: 5},
			{FetchHandle: 0, TableRowScan: 0, NextWaitIndexScan: 0, Concurrency: 5},
		},
		{
			{FetchHandleTotal: 0, FetchHandle: 0, TableRowScan: 3e6, TableTaskNum: 1, Concurrency: 5, NextWaitTableLookUpResp: 1e6},
			{FetchHandleTotal: 10e6, FetchHandle: 4e6, TaskWait: 2e6, TableRowScan: 2e6, TableTaskNum: 2, Concurrency: 5},
		},
		{
			{FetchHandleTotal: 8e6, FetchHandle: 3e6, TaskWait: 1e6, TableRowScan: 0, Concurrency: 3},
			{FetchHandleTotal: 0, FetchHandle: 0, TableRowScan: 0, Concurrency: 3},
		},
	}
	for _, sc := range statsCases {
		g.Add("sys/ilu_helpers/stats", "index_lookup_helpers", "runtime_stats", sc[0], sc[1])
	}

	// 5. index_lookup (Part 2C, >= 180 cases)
	mkIntTable := func(ids []int64) []reLookUpTableEntry {
		entries := make([]reLookUpTableEntry, len(ids))
		for i, id := range ids {
			entries[i] = reLookUpTableEntry{
				HandleKey: []any{float64(id)},
				Row:       []any{float64(id), float64(id * 100)},
			}
		}
		return entries
	}
	mkIntIdxBatch := func(ids []int64) reLookUpIndexBatch {
		rows := make([][]any, len(ids))
		for i, id := range ids {
			rows[i] = []any{float64(id)}
		}
		return reLookUpIndexBatch{Rows: rows}
	}

	// 5a. Basic int handle: keepOrder x desc x indexPaging x concurrency
	for _, keepOrder := range []bool{false, true} {
		for _, desc := range []bool{false, true} {
			for _, paging := range []bool{false, true} {
				ids := []int64{5, 2, 8, 1, 9, 3, 7, 4, 6}
				g.Add("sys/index_lookup/basic_int", "index_lookup", reIndexLookUpSpec{
					TableID:         501,
					IndexID:         1,
					KeepOrder:       keepOrder,
					Desc:            desc,
					IndexPaging:     paging,
					IndexLookupSize: 4,
					IndexLookupConc: 3,
					DistSQLConc:     5,
					MaxChunkSize:    4,
					EstRows:         20,
					Ranges:          []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(100)}}},
					IndexStreams:    [][]reLookUpIndexBatch{{mkIntIdxBatch(ids[:5]), mkIntIdxBatch(ids[5:])}},
					TableEntries:    mkIntTable([]int64{1, 2, 3, 4, 5, 6, 7, 8, 9}),
					RequiredRows:    []int{2, 3, 4, 4},
				})
			}
		}
	}

	// 5b. Spec §12.2 900-row batch size / whole-chunk reuse test:
	//     900 rows, index_lookup_size=100 -> max_chunk_size=1024 yields table_task_num=9,
	//     max_chunk_size=64 yields table_task_num=8!
	{
		all900 := make([]int64, 900)
		for i := range all900 {
			all900[i] = int64(i + 1)
		}
		var batches64 []reLookUpIndexBatch
		for off := 0; off < 900; off += 64 {
			end := min(off+64, 900)
			batches64 = append(batches64, mkIntIdxBatch(all900[off:end]))
		}
		tbl900 := mkIntTable(all900)
		for _, maxChk := range []int{1024, 64} {
			g.Add(fmt.Sprintf("sys/index_lookup/batch900_chk%d", maxChk), "index_lookup", reIndexLookUpSpec{
				TableID:         502,
				IndexID:         1,
				KeepOrder:       true,
				IndexLookupSize: 100,
				IndexLookupConc: 2,
				DistSQLConc:     5,
				MaxChunkSize:    maxChk,
				EstRows:         900,
				Ranges:          []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(1000)}}},
				IndexStreams:    [][]reLookUpIndexBatch{batches64},
				TableEntries:    tbl900,
				RequiredRows:    []int{maxChk},
			})
		}
	}

	// 5c. Common handle (both v1 binary string & v0 non-binary collation string sortKey path in getHandle)
	for _, v0Str := range []bool{false, true} {
		for _, keepOrder := range []bool{false, true} {
			idxRows := [][]any{
				{float64(3), "cc"},
				{float64(1), "aa"},
				{float64(2), "bb"},
				{float64(4), "dd"},
			}
			tblEntries := []reLookUpTableEntry{
				{HandleKey: []any{float64(1), "aa"}, Row: []any{float64(1), "aa", float64(11)}},
				{HandleKey: []any{float64(2), "bb"}, Row: []any{float64(2), "bb", float64(22)}},
				{HandleKey: []any{float64(3), "cc"}, Row: []any{float64(3), "cc", float64(33)}},
				{HandleKey: []any{float64(4), "dd"}, Row: []any{float64(4), "dd", float64(44)}},
			}
			g.Add("sys/index_lookup/common_handle", "index_lookup", reIndexLookUpSpec{
				TableID:           503,
				IndexID:           2,
				IsCommonHandle:    true,
				CommonHandleV0Str: v0Str,
				KeepOrder:         keepOrder,
				IndexLookupSize:   2,
				IndexLookupConc:   2,
				DistSQLConc:       5,
				MaxChunkSize:      8,
				EstRows:           10,
				Ranges:            []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(100)}}},
				IndexStreams:      [][]reLookUpIndexBatch{{{Rows: idxRows}}},
				TableEntries:      tblEntries,
				RequiredRows:      []int{2, 8},
			})
		}
	}

	// 5d. PushedLimit (offset + count, exact boundary, offset > total rows)
	for _, lim := range []reIndexLookUpLimit{{0, 3}, {2, 4}, {5, 10}, {20, 5}, {0, 0}} {
		ids := []int64{10, 20, 30, 40, 50, 60, 70, 80}
		g.Add("sys/index_lookup/pushed_limit", "index_lookup", reIndexLookUpSpec{
			TableID:         504,
			IndexID:         1,
			KeepOrder:       true,
			IndexPaging:     true,
			IndexLookupSize: 4,
			IndexLookupConc: 2,
			DistSQLConc:     5,
			MaxChunkSize:    4,
			EstRows:         8,
			PushedLimit:     &lim,
			Ranges:          []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(100)}}},
			IndexStreams:    [][]reLookUpIndexBatch{{mkIntIdxBatch(ids[:4]), mkIntIdxBatch(ids[4:])}},
			TableEntries:    mkIntTable(ids),
			RequiredRows:    []int{2, 4, 4},
		})
	}

	// 5e. Grouped ranges: rolling mode (!keepOrder / no byItems) with CopConc AND merge-sort mode (keepOrder + byItems, ASC & DESC)
	for _, desc := range []bool{false, true} {
		// Merge-sort mode: 3 groups, each emits [by_col, handle]
		mkMergeBatch := func(pairs [][2]int64) reLookUpIndexBatch {
			rows := make([][]any, len(pairs))
			for i, p := range pairs {
				rows[i] = []any{float64(p[0]), float64(p[1])}
			}
			return reLookUpIndexBatch{Rows: rows}
		}
		var s0, s1, s2 [][2]int64
		if !desc {
			s0 = [][2]int64{{10, 1}, {40, 4}}
			s1 = [][2]int64{{20, 2}, {50, 5}}
			s2 = [][2]int64{{30, 3}, {60, 6}}
		} else {
			s0 = [][2]int64{{40, 4}, {10, 1}}
			s1 = [][2]int64{{50, 5}, {20, 2}}
			s2 = [][2]int64{{60, 6}, {30, 3}}
		}
		g.Add("sys/index_lookup/merge_sort", "index_lookup", reIndexLookUpSpec{
			TableID:         505,
			IndexID:         1,
			KeepOrder:       true,
			Desc:            desc,
			IndexLookupSize: 3,
			IndexLookupConc: 2,
			DistSQLConc:     1, // also exercises c=1 merge-sort concurrency!
			MaxChunkSize:    4,
			EstRows:         6,
			ByItemsDesc:     []bool{desc},
			GroupedRanges: [][]reRangeSpec{
				{{Low: []any{float64(1)}, High: []any{float64(10)}}},
				{{Low: []any{float64(11)}, High: []any{float64(20)}}},
				{{Low: []any{float64(21)}, High: []any{float64(30)}}},
			},
			IndexStreams: [][]reLookUpIndexBatch{
				{mkMergeBatch(s0)},
				{mkMergeBatch(s1)},
				{mkMergeBatch(s2)},
			},
			TableEntries: mkIntTable([]int64{1, 2, 3, 4, 5, 6}),
			RequiredRows: []int{2, 4, 4},
		})
		// Rolling mode (no merge sort) with CopConc > 1
		g.Add("sys/index_lookup/rolling", "index_lookup", reIndexLookUpSpec{
			TableID:         506,
			IndexID:         1,
			KeepOrder:       false,
			Desc:            desc,
			IndexLookupSize: 3,
			IndexLookupConc: 2,
			DistSQLConc:     2,
			MaxChunkSize:    4,
			EstRows:         6,
			CopConc:         2,
			GroupedRanges: [][]reRangeSpec{
				{{Low: []any{float64(1)}, High: []any{float64(10)}}},
				{{Low: []any{float64(11)}, High: []any{float64(20)}}},
				{{Low: []any{float64(21)}, High: []any{float64(30)}}},
			},
			IndexStreams: [][]reLookUpIndexBatch{
				{mkIntIdxBatch([]int64{1, 4})},
				{mkIntIdxBatch([]int64{2, 5})},
				{mkIntIdxBatch([]int64{3, 6})},
			},
			TableEntries: mkIntTable([]int64{1, 2, 3, 4, 5, 6}),
			RequiredRows: []int{2, 4, 4},
		})
	}

	// 5f. indexLookUpPushDown (dual-channel hit + miss, verifying Paging=false & Cacheable=false)
	for _, withLimit := range []bool{false, true} {
		var pl *reIndexLookUpLimit
		if withLimit {
			pl = &reIndexLookUpLimit{Offset: 1, Count: 4}
		}
		g.Add("sys/index_lookup/pushdown", "index_lookup", reIndexLookUpSpec{
			TableID:             507,
			IndexID:             1,
			KeepOrder:           false,
			IndexPaging:         true, // must be forced false in pushdown request!
			IndexLookUpPushDown: true,
			IndexLookupSize:     10,
			IndexLookupConc:     2,
			DistSQLConc:         5,
			MaxChunkSize:        8,
			EstRows:             10,
			PushedLimit:         pl,
			Ranges:              []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(100)}}},
			IndexStreams: [][]reLookUpIndexBatch{{
				{
					HitRows:     [][]any{{float64(1), float64(100)}, {float64(3), float64(300)}},
					MissHandles: [][]any{{float64(2)}, {float64(4)}},
				},
				{
					HitRows:     [][]any{{float64(5), float64(500)}},
					MissHandles: [][]any{{float64(6)}},
				},
			}},
			TableEntries: mkIntTable([]int64{2, 4, 6}),
			RequiredRows: []int{4, 8},
		})
	}

	// 5g. Inconsistent index (8133 error), weakConsistency exemption, extraTblPlan exemption
	for _, weak := range []bool{false, true} {
		for _, extraPlan := range []bool{false, true} {
			g.Add("sys/index_lookup/inconsistent", "index_lookup", reIndexLookUpSpec{
				TableID:         508,
				IndexID:         1,
				KeepOrder:       true,
				WeakConsistency: weak,
				ExtraTblPlan:    extraPlan,
				IndexLookupSize: 10,
				IndexLookupConc: 1,
				DistSQLConc:     5,
				MaxChunkSize:    8,
				EstRows:         5,
				Ranges:          []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}},
				IndexStreams:    [][]reLookUpIndexBatch{{mkIntIdxBatch([]int64{1, 2, 3, 4})}},
				TableEntries:    mkIntTable([]int64{1, 3}), // missing handles 2 & 4
				RequiredRows:    []int{8},
			})
		}
	}

	// 5h. Dummy (temp table + setDummy), empty ranges, index error, table error
	g.Add("sys/index_lookup/temp_dummy", "index_lookup", reIndexLookUpSpec{
		TableID: 509, IndexID: 1, TempTable: true, IndexLookupSize: 10, IndexLookupConc: 2, DistSQLConc: 5, MaxChunkSize: 8,
		Ranges: []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}}, RequiredRows: []int{4},
	})
	g.Add("sys/index_lookup/set_dummy", "index_lookup", reIndexLookUpSpec{
		TableID: 510, IndexID: 1, SetDummyCall: true, IndexLookupSize: 10, IndexLookupConc: 2, DistSQLConc: 5, MaxChunkSize: 8,
		Ranges: []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}}, RequiredRows: []int{4},
	})
	g.Add("sys/index_lookup/empty", "index_lookup", reIndexLookUpSpec{
		TableID: 511, IndexID: 1, KeepOrder: true, IndexLookupSize: 10, IndexLookupConc: 2, DistSQLConc: 5, MaxChunkSize: 8,
		Ranges: []reRangeSpec{}, IndexStreams: [][]reLookUpIndexBatch{{}}, RequiredRows: []int{4},
	})
	g.Add("sys/index_lookup/index_err", "index_lookup", reIndexLookUpSpec{
		TableID: 512, IndexID: 1, KeepOrder: true, IndexLookupSize: 10, IndexLookupConc: 2, DistSQLConc: 5, MaxChunkSize: 8,
		Ranges: []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}}, IndexErr: "idx lookup boom", RequiredRows: []int{4},
	})
	g.Add("sys/index_lookup/table_err", "index_lookup", reIndexLookUpSpec{
		TableID: 513, IndexID: 1, KeepOrder: true, IndexLookupSize: 10, IndexLookupConc: 1, DistSQLConc: 5, MaxChunkSize: 8,
		Ranges:       []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(10)}}},
		IndexStreams: [][]reLookUpIndexBatch{{mkIntIdxBatch([]int64{1, 2})}}, TableErr: "tbl lookup boom", RequiredRows: []int{4},
	})

	// 5i. Randomized index_lookup cases to exceed Min=180 cleanly
	for i := 0; i < 160; i++ {
		keepOrder := r.IntN(2) == 0
		desc := r.IntN(2) == 0
		paging := r.IntN(2) == 0
		isCommon := r.IntN(4) == 0
		pushDown := !keepOrder && !isCommon && r.IntN(5) == 0
		lookupSize := []int{2, 4, 8, 16, 64}[r.IntN(5)]
		lookupConc := 1 + r.IntN(4)
		distConc := []int{1, 2, 5}[r.IntN(3)]
		maxChk := []int{2, 4, 8, 16}[r.IntN(4)]
		nRows := 1 + r.IntN(20)

		var idxStreams [][]reLookUpIndexBatch
		var tblEntries []reLookUpTableEntry
		if !isCommon {
			ids := make([]int64, nRows)
			for j := range ids {
				ids[j] = int64(j + 1)
			}
			// Shuffle handle order to exercise keepOrder sorting.
			r.Shuffle(len(ids), func(a, b int) { ids[a], ids[b] = ids[b], ids[a] })
			tblEntries = mkIntTable(ids)
			if pushDown {
				var hits [][]any
				var misses [][]any
				for idx, id := range ids {
					if idx%2 == 0 {
						hits = append(hits, []any{float64(id), float64(id * 100)})
					} else {
						misses = append(misses, []any{float64(id)})
					}
				}
				idxStreams = [][]reLookUpIndexBatch{{{HitRows: hits, MissHandles: misses}}}
			} else {
				split := max(1, len(ids)/2)
				idxStreams = [][]reLookUpIndexBatch{{mkIntIdxBatch(ids[:split]), mkIntIdxBatch(ids[split:])}}
			}
		} else {
			idxRows := make([][]any, nRows)
			tblEntries = make([]reLookUpTableEntry, nRows)
			for j := 0; j < nRows; j++ {
				s := fmt.Sprintf("k%02d", j)
				id := int64(nRows - j)
				idxRows[j] = []any{float64(id), s}
				tblEntries[j] = reLookUpTableEntry{
					HandleKey: []any{float64(id), s},
					Row:       []any{float64(id), s, float64(id * 10)},
				}
			}
			idxStreams = [][]reLookUpIndexBatch{{{Rows: idxRows}}}
		}

		var pl *reIndexLookUpLimit
		if r.IntN(4) == 0 {
			pl = &reIndexLookUpLimit{Offset: uint64(r.IntN(3)), Count: uint64(1 + r.IntN(nRows))}
		}
		g.Add(fmt.Sprintf("rand/index_lookup/%d", i), "index_lookup", reIndexLookUpSpec{
			TableID:             int64(600 + i),
			IndexID:             2,
			IsCommonHandle:      isCommon,
			CommonHandleV0Str:   isCommon && r.IntN(2) == 0,
			KeepOrder:           keepOrder,
			Desc:                desc,
			IndexPaging:         paging,
			IndexLookUpPushDown: pushDown,
			IndexLookupSize:     lookupSize,
			IndexLookupConc:     lookupConc,
			DistSQLConc:         distConc,
			MaxChunkSize:        maxChk,
			EstRows:             float64(nRows),
			PushedLimit:         pl,
			Ranges:              []reRangeSpec{{Low: []any{float64(1)}, High: []any{float64(1000)}}},
			IndexStreams:        idxStreams,
			TableEntries:        tblEntries,
			RequiredRows:        []int{1 + r.IntN(maxChk), maxChk},
		})
	}
}

func readexecExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "closest_read_adjuster":
		return reExecClosestReadAdjuster(args)
	case "table_reader":
		return reExecTableReader(args)
	case "index_reader":
		return reExecIndexReader(args)
	case "index_lookup_helpers":
		return reExecIndexLookUpHelpers(args)
	case "index_lookup":
		return reExecIndexLookUp(args)
	default:
		return nil, fmt.Errorf("readexec: unknown op %q", op)
	}
}
