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
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
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
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannercore "github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/property"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/statistics"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
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
	Low          []any `json:"low"`
	High         []any `json:"high"`
	LowExclude   bool  `json:"low_exclude,omitempty"`
	HighExclude  bool  `json:"high_exclude,omitempty"`
}

// reStreamSpec scripts one mock SelectResult returned by selectResultHook.
type reStreamSpec struct {
	OpenErr  string       `json:"open_err,omitempty"`
	Rows     [][]*int64   `json:"rows,omitempty"`
	NextErr  string       `json:"next_err,omitempty"`
	ErrAfter int          `json:"err_after,omitempty"` // return NextErr after this many Next calls
	CloseErr string       `json:"close_err,omitempty"`
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
}

func readexecExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "closest_read_adjuster":
		return reExecClosestReadAdjuster(args)
	case "table_reader":
		return reExecTableReader(args)
	case "index_reader":
		return reExecIndexReader(args)
	default:
		return nil, fmt.Errorf("readexec: unknown op %q", op)
	}
}
