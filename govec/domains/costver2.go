package domains

// Domain "costver2": the cost model v2 of the operators of the first vertical slice (tidb-rs
// VS1-30, specs/D10.5-cost-model/spec.md): plan_cost_ver2.go's getPlanCost, GetPlanCostVer2
// of Selection, Projection, TableScan, TableReader, Sort, TopN and PointGet, the formula
// helpers and the factor table.
//
// Records:
//   - "plan": args [cvPlan] -> {"cost": GetPlanCost(root), "ranges": getNumberOfRanges(root),
//     "nodes": [[type, init, cost, formula, {factor: cost}]] (the memo of every node, preorder,
//     the reader's table plan after the reader), "again": GetPlanCost(root) again after the
//     operators' cost factors are doubled (the memo answers unless CostFlagRecalculate)}.
//     Costs are float strings; a trace that is nil is formula "" and factors null.
//   - "factor": args [fn, taskType, cvFactorPlan] -> the factor (Name, Value) of
//     getTask{CPU,Mem,Scan,Net,Request}FactorVer2 for the plan.
//   - "formula": args [fn, rows, x, exprs, factor, trace] -> the CostVer2 of scanCostVer2
//     (x = rowSize), netCostVer2 (x = rowSize), filterCostVer2 (exprs), orderCostVer2 (x = n,
//     exprs are the ByItems) or doubleReadCostVer2 (rows = numTasks).
//
// Float inputs are strings (serde_json's default float parsing is not correctly rounded).
// The generator rounds its own products (float64(...)), so both targets get the same inputs.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"sync"
	gotime "time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/ast"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/property"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/planner/util/costusage"
	"github.com/pingcap/tidb/pkg/planner/util/optimizetrace"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/statistics"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tidb/pkg/util/ranger"
)

const costver2Seed = 20261122

//go:linkname cvGetNumberOfRanges github.com/pingcap/tidb/pkg/planner/core.getNumberOfRanges
func cvGetNumberOfRanges(pp base.PhysicalPlan) int

//go:linkname cvGetTaskCPUFactorVer2 github.com/pingcap/tidb/pkg/planner/core.getTaskCPUFactorVer2
func cvGetTaskCPUFactorVer2(p base.PhysicalPlan, taskType property.TaskType) costusage.CostVer2Factor

//go:linkname cvGetTaskMemFactorVer2 github.com/pingcap/tidb/pkg/planner/core.getTaskMemFactorVer2
func cvGetTaskMemFactorVer2(p base.PhysicalPlan, taskType property.TaskType) costusage.CostVer2Factor

//go:linkname cvGetTaskScanFactorVer2 github.com/pingcap/tidb/pkg/planner/core.getTaskScanFactorVer2
func cvGetTaskScanFactorVer2(p base.PhysicalPlan, storeType kv.StoreType, taskType property.TaskType) costusage.CostVer2Factor

//go:linkname cvGetTaskNetFactorVer2 github.com/pingcap/tidb/pkg/planner/core.getTaskNetFactorVer2
func cvGetTaskNetFactorVer2(p base.PhysicalPlan, taskType property.TaskType) costusage.CostVer2Factor

//go:linkname cvGetTaskRequestFactorVer2 github.com/pingcap/tidb/pkg/planner/core.getTaskRequestFactorVer2
func cvGetTaskRequestFactorVer2(p base.PhysicalPlan, taskType property.TaskType) costusage.CostVer2Factor

//go:linkname cvScanCostVer2 github.com/pingcap/tidb/pkg/planner/core.scanCostVer2
func cvScanCostVer2(option *optimizetrace.PlanCostOption, rows, rowSize float64, scanFactor costusage.CostVer2Factor) costusage.CostVer2

//go:linkname cvNetCostVer2 github.com/pingcap/tidb/pkg/planner/core.netCostVer2
func cvNetCostVer2(option *optimizetrace.PlanCostOption, rows, rowSize float64, netFactor costusage.CostVer2Factor) costusage.CostVer2

//go:linkname cvFilterCostVer2 github.com/pingcap/tidb/pkg/planner/core.filterCostVer2
func cvFilterCostVer2(option *optimizetrace.PlanCostOption, rows float64, filters []expression.Expression, cpuFactor costusage.CostVer2Factor) costusage.CostVer2

//go:linkname cvOrderCostVer2 github.com/pingcap/tidb/pkg/planner/core.orderCostVer2
func cvOrderCostVer2(option *optimizetrace.PlanCostOption, rows, n float64, byItems []*plannerutil.ByItems, cpuFactor costusage.CostVer2Factor) costusage.CostVer2

//go:linkname cvDoubleReadCostVer2 github.com/pingcap/tidb/pkg/planner/core.doubleReadCostVer2
func cvDoubleReadCostVer2(option *optimizetrace.PlanCostOption, numTasks float64, requestFactor costusage.CostVer2Factor) costusage.CostVer2

//go:linkname cvGetTblStats github.com/pingcap/tidb/pkg/planner/core.getTblStats
func cvGetTblStats(copTaskPlan base.PhysicalPlan) *statistics.HistColl

func init() {
	vec.Register(&vec.Domain{
		Name:     "costver2",
		Seed:     costver2Seed,
		Coverage: costver2Coverage,
		Blocks:   []string{"D10.5"},
		Exempt:   costver2Exempt(),
		Gzip:     true,
		Generate: costver2Generate,
		Exec:     costver2Exec,
	})
}

var costver2Coverage = map[string]vec.Coverage{
	"plan": {Min: 1500, GoFuncs: []string{
		"GetPlanCost", "getPlanCost",
		"PhysicalSelection.GetPlanCostVer2", "PhysicalProjection.GetPlanCostVer2",
		"PhysicalTableScan.GetPlanCostVer2", "PhysicalTableReader.GetPlanCostVer2",
		"PhysicalIndexScan.GetPlanCostVer2", "PhysicalIndexReader.GetPlanCostVer2",
		"PhysicalSort.GetPlanCostVer2", "PhysicalTopN.GetPlanCostVer2", "PointGetPlan.GetPlanCostVer2",
		"getNumberOfRanges", "getTableScanPenalty", "getTblStats",
		"scanCostVer2", "netCostVer2", "filterCostVer2",
		"numFunctions", "orderCostVer2",
	}},
	"factor": {Min: 300, GoFuncs: []string{
		"getTaskCPUFactorVer2", "getTaskMemFactorVer2", "getTaskScanFactorVer2", "getTaskNetFactorVer2",
		"getTaskRequestFactorVer2", "isTemporaryTable", "getTableInfo",
	}},
	"formula": {Min: 300, GoFuncs: []string{"doubleReadCostVer2"}},
}

var (
	cvMu  sync.Mutex
	cvCtx *mock.Context
)

// The operators' cost factors the plan records set (and double for "again").
var cvFactorVars = []string{
	"table_full_scan", "table_range_scan", "table_rowid_scan", "table_tiflash_scan", "table_reader",
	"index_scan", "index_reader",
	"sort", "topn",
}

func cvSetFactor(sv *variable.SessionVars, name string, v float64) {
	switch name {
	case "table_full_scan":
		sv.TableFullScanCostFactor = v
	case "table_range_scan":
		sv.TableRangeScanCostFactor = v
	case "table_rowid_scan":
		sv.TableRowIDScanCostFactor = v
	case "table_tiflash_scan":
		sv.TableTiFlashScanCostFactor = v
	case "table_reader":
		sv.TableReaderCostFactor = v
	case "index_scan":
		sv.IndexScanCostFactor = v
	case "index_reader":
		sv.IndexReaderCostFactor = v
	case "sort":
		sv.SortCostFactor = v
	case "topn":
		sv.TopNCostFactor = v
	default:
		panic(name)
	}
}

func cvGetFactor(sv *variable.SessionVars, name string) float64 {
	switch name {
	case "table_full_scan":
		return sv.TableFullScanCostFactor
	case "table_range_scan":
		return sv.TableRangeScanCostFactor
	case "table_rowid_scan":
		return sv.TableRowIDScanCostFactor
	case "table_tiflash_scan":
		return sv.TableTiFlashScanCostFactor
	case "table_reader":
		return sv.TableReaderCostFactor
	case "index_scan":
		return sv.IndexScanCostFactor
	case "index_reader":
		return sv.IndexReaderCostFactor
	case "sort":
		return sv.SortCostFactor
	case "topn":
		return sv.TopNCostFactor
	}
	panic(name)
}

// cvSession: one mock context for all records, with the statement context and the session
// variables the records set reset.
func cvSession() *mock.Context {
	if cvCtx == nil {
		cvCtx = core.MockContext()
	}
	sv := cvCtx.GetSessionVars()
	sv.PlanColumnID.Store(0)
	sv.PlanID.Store(0)
	sv.StmtCtx = stmtctx.NewStmtCtxWithTimeZone(gotime.UTC)
	for name, v := range map[string]string{
		variable.TiDBAllowMPPExecution:   "ON",
		variable.TiDBEnforceMPPExecution: "OFF",
	} {
		if err := sv.SetSystemVar(name, v); err != nil {
			panic(err)
		}
	}
	sv.CostModelVersion = 2
	for _, f := range cvFactorVars {
		cvSetFactor(sv, f, 1)
	}
	sv.SetProjectionConcurrency(variable.ConcurrencyUnset)
	sv.SetDistSQLScanConcurrency(variable.DefDistSQLScanConcurrency)
	sv.SetAllowPreferRangeScan(false)
	sv.MemTracker.SetBytesLimit(variable.DefTiDBMemQuotaQuery)
	variable.EnableTmpStorageOnOOM.Store(variable.DefTiDBEnableTmpStorageOnOOM)
	return cvCtx
}

// cvSetField writes an unexported field of a struct (pointer) by name.
func cvSetField(obj any, name string, val any) {
	f := reflect.ValueOf(obj).Elem().FieldByName(name)
	if !f.IsValid() {
		panic("no field " + name)
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(val))
}

func cvGetField(obj any, name string) reflect.Value {
	f := reflect.ValueOf(obj).Elem().FieldByName(name)
	if !f.IsValid() {
		panic("no field " + name)
	}
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func cvCost(c costusage.CostVer2) []any {
	formula := ""
	var factors map[string]string
	if t := c.GetTrace(); t != nil {
		formula = t.GetFormula()
		factors = map[string]string{}
		for k, v := range t.GetFactorCosts() {
			factors[k] = phF(v)
		}
	}
	return []any{phF(c.GetCost()), formula, factors}
}

// ---------------------------------------------------------------- inputs

// cvOp is an operator above the scan (inside the reader) or above the reader.
type cvOp struct {
	Kind   string   `json:"kind"`  // sel, proj, sort, topn, limit
	Exprs  []string `json:"exprs"` // f (a scalar function), c (a column), k (a constant)
	Count  uint64   `json:"count"`
	Offset uint64   `json:"offset"`
	Rows   string   `json:"rows"`
}

// cvHist is the scan's tblColHists: a HistColl with, when Count >= 0, one fully loaded
// column (id 1) whose histogram has Count rows in one bucket and Null nulls.
type cvHist struct {
	Pseudo bool  `json:"pseudo"`
	Modify int64 `json:"modify"`
	Count  int64 `json:"count"`
	Null   int64 `json:"null"`
}

type cvVars struct {
	Factors     []string `json:"factors"` // cvFactorVars
	ProjConc    int      `json:"proj_conc"`
	ScanConc    int      `json:"scan_conc"`
	EnforceMPP  bool     `json:"enforce_mpp"`
	MemQuota    int64    `json:"mem_quota"`
	TmpOOM      bool     `json:"tmp_oom"`
	PreferRange bool     `json:"prefer_range"`
	IndexForce  bool     `json:"index_force"`
	Version     int      `json:"version"`
}

type cvPlan struct {
	Point     bool     `json:"point"`      // a PointGetPlan alone
	PointCols []int    `json:"point_cols"` // the accessed column types (empty: nil)
	Index     bool     `json:"index"`      // PhysicalIndexScan + optional PhysicalIndexReader
	Unique    bool     `json:"unique"`     // Index.Unique when Index is true
	Store     int      `json:"store"`      // kv.StoreType of the scan and the reader
	Desc      bool     `json:"desc"`
	Temp      bool     `json:"temp"`
	Lookup    bool     `json:"lookup"`     // isChildOfIndexLookUp
	RangeInfo bool     `json:"range_info"` // a non-empty rangeInfo
	PKHandle  bool     `json:"pk_handle"`
	Unsigned  bool     `json:"unsigned"`
	Ranges    []string `json:"ranges"` // int, uint, full, notnull, point
	PartConds bool     `json:"part_conds"`
	Cols      []int    `json:"cols"`     // the table's column types (tblCols); column 1 is the PK when PKHandle
	SchemaN   int      `json:"schema_n"` // the scan's schema: the first SchemaN columns
	Hist      cvHist   `json:"hist"`
	HistStats bool     `json:"hist_stats"` // StatsInfo.HistColl of every node is the scan's tblColHists
	ScanRows  string   `json:"scan_rows"`
	Cop       []cvOp   `json:"cop"`
	Reader    bool     `json:"reader"`
	ReaderRow string   `json:"reader_rows"`
	Root      []cvOp   `json:"root"`
	Task      int      `json:"task"`
	Flags     uint64   `json:"flags"`
	Vars      cvVars   `json:"vars"`
}

type cvFactorPlan struct {
	Kind  string `json:"kind"` // ts, reader, sel_ts, sel_reader, pg
	Desc  bool   `json:"desc"`
	Temp  bool   `json:"temp"`
	Store int    `json:"store"`
}

// ---------------------------------------------------------------- generate

var cvTypes = []int{int(mysql.TypeTiny), int(mysql.TypeLong), int(mysql.TypeLonglong), int(mysql.TypeDouble),
	int(mysql.TypeNewDecimal), int(mysql.TypeVarchar), int(mysql.TypeString), int(mysql.TypeBlob),
	int(mysql.TypeDatetime), int(mysql.TypeDate), int(mysql.TypeJSON)}

func cvRows(rng *rand.Rand) string {
	rows := []float64{0, 0.3, 1, 2, 7, 100, 999, 1000, 1001, 8192, 1e5, 1e6, 3e7}
	r := rows[rng.IntN(len(rows))]
	if rng.IntN(3) == 0 {
		r = float64(r * (0.5 + rng.Float64()))
	}
	return phF(r)
}

func cvExprs(rng *rand.Rand, n int) []string {
	out := []string{}
	for i := 0; i < n; i++ {
		out = append(out, []string{"f", "c", "k"}[rng.IntN(3)])
	}
	return out
}

func cvGenOp(rng *rand.Rand, kinds []string) cvOp {
	op := cvOp{Kind: kinds[rng.IntN(len(kinds))], Rows: cvRows(rng)}
	switch op.Kind {
	case "sel", "proj":
		op.Exprs = cvExprs(rng, 1+rng.IntN(4))
	case "sort", "topn":
		op.Exprs = cvExprs(rng, 1+rng.IntN(3))
	}
	if op.Kind == "topn" || op.Kind == "limit" {
		op.Count = []uint64{0, 1, 10, 9999, 10000, 20000, math.MaxUint64}[rng.IntN(7)]
		op.Offset = []uint64{0, 0, 5, 10001, 1}[rng.IntN(5)]
	}
	return op
}

func cvGenPlan(rng *rand.Rand) cvPlan {
	p := cvPlan{
		Store:     []int{int(kv.TiKV), int(kv.TiKV), int(kv.TiFlash)}[rng.IntN(3)],
		Desc:      rng.IntN(3) == 0,
		Temp:      rng.IntN(8) == 0,
		Lookup:    rng.IntN(8) == 0,
		RangeInfo: rng.IntN(6) == 0,
		PKHandle:  rng.IntN(2) == 0,
		Unsigned:  rng.IntN(3) == 0,
		PartConds: rng.IntN(5) == 0,
		HistStats: rng.IntN(2) == 0,
		ScanRows:  cvRows(rng),
		Reader:    rng.IntN(6) != 0,
		ReaderRow: cvRows(rng),
		Task:      []int{0, 0, 0, 1, 2, 3}[rng.IntN(6)],
		Hist: cvHist{
			Pseudo: rng.IntN(3) == 0,
			Modify: []int64{0, 5, 999, 1000, 5000, 1e6}[rng.IntN(6)],
			Count:  []int64{-1, 0, 1, 10, 1000, 4000, 1e6}[rng.IntN(7)],
			Null:   []int64{0, 0, 3}[rng.IntN(3)],
		},
	}
	for i := 0; i < rng.IntN(3); i++ {
		p.Ranges = append(p.Ranges, []string{"int", "uint", "full", "notnull", "point", "point"}[rng.IntN(6)])
	}
	nc := 1 + rng.IntN(5)
	for i := 0; i < nc; i++ {
		p.Cols = append(p.Cols, cvTypes[rng.IntN(len(cvTypes))])
	}
	if p.PKHandle {
		p.Cols[0] = int(mysql.TypeLonglong)
	}
	p.SchemaN = 1 + rng.IntN(nc)
	for i := 0; i < rng.IntN(3); i++ {
		p.Cop = append(p.Cop, cvGenOp(rng, []string{"sel", "sel", "topn", "limit", "proj"}))
	}
	for i := 0; i < rng.IntN(4); i++ {
		p.Root = append(p.Root, cvGenOp(rng, []string{"sel", "proj", "sort", "topn", "limit"}))
	}
	if rng.IntN(3) == 0 {
		p.Flags |= costusage.CostFlagRecalculate
	}
	if rng.IntN(2) == 0 {
		p.Flags |= costusage.CostFlagTrace
	}
	p.Vars = cvVars{
		ProjConc:    []int{-1, 0, 1, 3, 8}[rng.IntN(5)],
		ScanConc:    []int{1, 3, 15}[rng.IntN(3)],
		EnforceMPP:  rng.IntN(4) == 0,
		MemQuota:    []int64{0, -1, 100, 64 << 10, 1 << 30}[rng.IntN(5)],
		TmpOOM:      rng.IntN(3) != 0,
		PreferRange: rng.IntN(2) == 0,
		IndexForce:  rng.IntN(5) == 0,
		Version:     2,
	}
	for range cvFactorVars {
		p.Vars.Factors = append(p.Vars.Factors, []string{"1", "1", "0.5", "2.25", "100"}[rng.IntN(5)])
	}
	return p
}

func costver2Generate(g *vec.Gen, rng *rand.Rand) {
	for i := 0; i < 2000; i++ {
		p := cvGenPlan(rng)
		if i%10 == 0 {
			// a point get alone
			p = cvPlan{Point: true, ScanRows: cvRows(rng), Task: []int{0, 0, 1}[rng.IntN(3)], Temp: rng.IntN(4) == 0,
				Flags: uint64(rng.IntN(2)) * costusage.CostFlagTrace, Vars: cvVars{Version: 2, ScanConc: 15}}
			for j := 0; j < rng.IntN(4); j++ {
				p.PointCols = append(p.PointCols, cvTypes[rng.IntN(len(cvTypes))])
			}
			for range cvFactorVars {
				p.Vars.Factors = append(p.Vars.Factors, "1")
			}
		}
		g.Add(fmt.Sprintf("rand/plan/%d", i), "plan", p)
	}
	for i := 0; i < 500; i++ {
		p := cvGenPlan(rng)
		p.Index = true
		p.Unique = rng.IntN(2) == 0
		p.Store = int(kv.TiKV)
		p.Lookup = false
		g.Add(fmt.Sprintf("rand/idx_plan/%d", i), "plan", p)
	}
	fns := []string{"cpu", "mem", "scan", "net", "request"}
	kinds := []string{"ts", "reader", "sel_ts", "sel_reader", "is", "ireader", "sel_is", "sel_ireader", "pg"}
	for i := 0; i < 600; i++ {
		fp := cvFactorPlan{Kind: kinds[rng.IntN(len(kinds))], Desc: rng.IntN(2) == 0, Temp: rng.IntN(3) == 0,
			Store: []int{int(kv.TiKV), int(kv.TiFlash)}[rng.IntN(2)]}
		g.Add(fmt.Sprintf("rand/factor/%d", i), "factor", fns[rng.IntN(len(fns))], rng.IntN(4), fp)
	}
	ffns := []string{"scan", "net", "filter", "order", "double_read"}
	xs := []float64{0, 0.5, 1, 2, 3, 7.5, 64, 1000, 1e6, math.Inf(1)}
	factorNames := []string{"tidb_temp_table_factor", "tikv_scan_factor", "tidb_cpu_factor", "tidb_kv_net_factor",
		"tidb_request_factor", "tiflash_mem_factor"}
	for i := 0; i < 500; i++ {
		x := float64(xs[rng.IntN(len(xs))] * (0.5 + rng.Float64()))
		if rng.IntN(3) == 0 {
			x = xs[rng.IntN(len(xs))]
		}
		g.Add(fmt.Sprintf("rand/formula/%d", i), "formula", ffns[rng.IntN(len(ffns))], cvRows(rng), phF(x),
			cvExprs(rng, rng.IntN(4)), factorNames[rng.IntN(len(factorNames))], rng.IntN(2) == 0)
	}
}

// ---------------------------------------------------------------- exec

func costver2Exec(op string, args []json.RawMessage) (out any, err error) {
	cvMu.Lock()
	defer cvMu.Unlock()
	switch op {
	case "plan":
		return cvRunPlan(arg[cvPlan](args, 0))
	case "factor":
		return cvRunFactor(arg[string](args, 0), arg[int](args, 1), arg[cvFactorPlan](args, 2)), nil
	case "formula":
		return cvRunFormula(args), nil
	}
	return nil, fmt.Errorf("costver2: unknown op %s", op)
}

func cvFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return f
}

func cvColumn(uid int64, tp int) *expression.Column {
	return &expression.Column{UniqueID: uid, ID: uid, RetType: types.NewFieldType(byte(tp))}
}

func cvExpr(sctx *mock.Context, kind string, col *expression.Column) expression.Expression {
	switch kind {
	case "f":
		return expression.NewFunctionInternal(sctx.GetExprCtx(), ast.Plus, types.NewFieldType(mysql.TypeLonglong), col, col)
	case "k":
		return expression.NewOne()
	}
	return col
}

func cvRange(kind string) *ranger.Range {
	switch kind {
	case "int":
		return ranger.FullIntRange(false)[0]
	case "uint":
		return ranger.FullIntRange(true)[0]
	case "full":
		return ranger.FullRange()[0]
	case "notnull":
		return ranger.FullNotNullRange()[0]
	}
	return &ranger.Range{LowVal: []types.Datum{types.NewIntDatum(5)}, HighVal: []types.Datum{types.NewIntDatum(5)},
		Collators: collate.GetBinaryCollatorSlice(1)}
}

func cvTable(temp bool, pkHandle, unsigned bool, cols []int) *model.TableInfo {
	tbl := &model.TableInfo{ID: 100, PKIsHandle: pkHandle}
	if temp {
		tbl.TempTableType = model.TempTableGlobal
	}
	for i, tp := range cols {
		ci := &model.ColumnInfo{ID: int64(i + 1), Offset: i, FieldType: *types.NewFieldType(byte(tp))}
		if i == 0 && pkHandle {
			ci.AddFlag(mysql.PriKeyFlag)
			if unsigned {
				ci.AddFlag(mysql.UnsignedFlag)
			}
		}
		tbl.Columns = append(tbl.Columns, ci)
	}
	return tbl
}

func cvHistColl(h cvHist) *statistics.HistColl {
	cols := map[int64]*statistics.Column{}
	if h.Count >= 0 {
		hg := statistics.NewHistogram(1, h.Count, h.Null, 0, types.NewFieldType(mysql.TypeLonglong), 1, 0)
		if h.Count > 0 {
			lo, hi := types.NewIntDatum(1), types.NewIntDatum(100)
			hg.AppendBucketWithNDV(&lo, &hi, h.Count, 1, 0)
		}
		cols[1] = &statistics.Column{Histogram: *hg, StatsVer: 1, StatsLoadedStatus: statistics.NewStatsFullLoadStatus()}
	}
	coll := statistics.NewHistCollWithColsAndIdxs(100, true, 0, h.Modify, cols, map[int64]*statistics.Index{})
	coll.Pseudo = h.Pseudo
	return coll
}

func cvRunPlan(in cvPlan) (any, error) {
	sctx := cvSession()
	sv := sctx.GetSessionVars()
	for i, f := range cvFactorVars {
		cvSetFactor(sv, f, cvFloat(in.Vars.Factors[i]))
	}
	if in.Vars.ProjConc >= 0 {
		sv.SetProjectionConcurrency(in.Vars.ProjConc)
	}
	sv.SetDistSQLScanConcurrency(in.Vars.ScanConc)
	if in.Vars.EnforceMPP {
		if err := sv.SetSystemVar(variable.TiDBEnforceMPPExecution, "ON"); err != nil {
			return nil, err
		}
	}
	sv.MemTracker.SetBytesLimit(in.Vars.MemQuota)
	variable.EnableTmpStorageOnOOM.Store(in.Vars.TmpOOM)
	sv.SetAllowPreferRangeScan(in.Vars.PreferRange)
	if in.Vars.IndexForce {
		sv.StmtCtx.SetIndexForce()
	}
	sv.CostModelVersion = in.Vars.Version

	var root base.PhysicalPlan
	var readerSize any
	if in.Point {
		tbl := cvTable(in.Temp, false, false, []int{int(mysql.TypeLonglong)})
		cols := []*expression.Column{}
		for i, tp := range in.PointCols {
			cols = append(cols, cvColumn(int64(i+1), tp))
		}
		pgv := core.PointGetPlan{TblInfo: tbl}
		cvSetField(&pgv, "schema", expression.NewSchema(cols...))
		pg := pgv.Init(sctx, &property.StatsInfo{RowCount: cvFloat(in.ScanRows)}, 0)
		if len(in.PointCols) > 0 {
			cvSetField(pg, "accessCols", cols)
		}
		root = pg
	} else {
		tbl := cvTable(in.Temp, in.PKHandle, in.Unsigned, in.Cols)
		tblCols := []*expression.Column{}
		for i, tp := range in.Cols {
			c := cvColumn(int64(i+1), tp)
			if i == 0 && in.PKHandle {
				c.RetType.AddFlag(mysql.PriKeyFlag)
				if in.Unsigned {
					c.RetType.AddFlag(mysql.UnsignedFlag)
				}
			}
			tblCols = append(tblCols, c)
		}
		hist := cvHistColl(in.Hist)
		stats := func(rows string) *property.StatsInfo {
			s := &property.StatsInfo{RowCount: cvFloat(rows)}
			if in.HistStats {
				s.HistColl = hist
			}
			return s
		}
		ranges := []*ranger.Range{}
		for _, r := range in.Ranges {
			ranges = append(ranges, cvRange(r))
		}
		var cur base.PhysicalPlan
		if in.Index {
			idxCols := make([]*model.IndexColumn, in.SchemaN)
			for i := 0; i < in.SchemaN; i++ {
				idxCols[i] = &model.IndexColumn{Offset: i, Length: types.UnspecifiedLength}
			}
			idx := &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("idx"), Columns: idxCols, Unique: in.Unique}
			is := core.PhysicalIndexScan{Table: tbl, Index: idx, Desc: in.Desc, Ranges: ranges}.Init(sctx, 0)
			is.SetSchema(expression.NewSchema(tblCols[:in.SchemaN]...))
			is.SetStats(stats(in.ScanRows))
			cvSetField(is, "dataSourceSchema", expression.NewSchema(tblCols[:in.SchemaN]...))
			cvSetField(is, "tblColHists", hist)
			cur = is
		} else {
			ts := core.PhysicalTableScan{Table: tbl, StoreType: kv.StoreType(in.Store), Desc: in.Desc, Ranges: ranges}.Init(sctx, 0)
			ts.SetSchema(expression.NewSchema(tblCols[:in.SchemaN]...))
			ts.SetStats(stats(in.ScanRows))
			ts.SetIsChildOfIndexLookUp(in.Lookup)
			cvSetField(ts, "tblCols", tblCols)
			cvSetField(ts, "tblColHists", hist)
			if in.RangeInfo {
				cvSetField(ts, "rangeInfo", "range decided by [eq(test.t.a, 1)]")
			}
			if in.PartConds {
				ts.PlanPartInfo = &core.PhysPlanPartInfo{PruningConds: []expression.Expression{expression.NewOne()}}
			}
			cur = ts
		}
		build := func(o cvOp) {
			col := cur.Schema().Columns[0]
			exprs := []expression.Expression{}
			for _, e := range o.Exprs {
				exprs = append(exprs, cvExpr(sctx, e, col))
			}
			by := []*plannerutil.ByItems{}
			for i, e := range exprs {
				by = append(by, &plannerutil.ByItems{Expr: e, Desc: i%2 == 1})
			}
			var next base.PhysicalPlan
			switch o.Kind {
			case "sel":
				next = core.PhysicalSelection{Conditions: exprs}.Init(sctx, stats(o.Rows), 0)
			case "proj":
				p := core.PhysicalProjection{Exprs: exprs}.Init(sctx, stats(o.Rows), 0)
				outs := []*expression.Column{}
				for i := range exprs {
					outs = append(outs, cvColumn(int64(200+i), int(mysql.TypeLonglong)))
				}
				p.SetSchema(expression.NewSchema(outs...))
				next = p
			case "sort":
				next = core.PhysicalSort{ByItems: by}.Init(sctx, stats(o.Rows), 0)
			case "topn":
				next = core.PhysicalTopN{ByItems: by, Count: o.Count, Offset: o.Offset}.Init(sctx, stats(o.Rows), 0)
			case "limit":
				l := core.PhysicalLimit{Count: o.Count, Offset: o.Offset}.Init(sctx, stats(o.Rows), 0)
				l.SetSchema(cur.Schema().Clone())
				next = l
			}
			next.SetChildren(cur)
			cur = next
		}
		for _, o := range in.Cop {
			build(o)
		}
		if in.Reader {
			if in.Index {
				r := core.PhysicalIndexReader{}
				cvSetField(&r, "indexPlan", cur)
				reader := r.Init(sctx, 0)
				reader.SetStats(stats(in.ReaderRow))
				readerSize = phF(reader.GetNetDataSize())
				cur = reader
			} else {
				r := core.PhysicalTableReader{StoreType: kv.StoreType(in.Store)}
				cvSetField(&r, "tablePlan", cur)
				reader := r.Init(sctx, 0)
				reader.SetStats(stats(in.ReaderRow))
				readerSize = phF(reader.GetAvgRowSize())
				cur = reader
			}
		} else {
			_ = cvGetTblStats(cur)
		}
		for _, o := range in.Root {
			build(o)
		}
		root = cur
	}

	opt := optimizetrace.NewDefaultPlanCostOption().WithCostFlag(in.Flags)
	taskType := property.TaskType(in.Task)
	cost, err := core.GetPlanCost(root, taskType, opt)
	if err != nil {
		return nil, err
	}
	nodes := []any{}
	cvCollect(root, &nodes)
	for _, f := range cvFactorVars {
		cvSetFactor(sv, f, cvGetFactor(sv, f)*2)
	}
	again, err := core.GetPlanCost(root, taskType, opt)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"cost": phF(cost), "ranges": cvGetNumberOfRanges(root), "nodes": nodes,
		"again": phF(again)}
	if readerSize != nil {
		out["reader_size"] = readerSize
	}
	return out, nil
}

// cvCollect appends [tp, init, cost...] of the memo of p and its subtree (preorder; a
// reader's pushed-down plan after the reader).
func cvCollect(p base.PhysicalPlan, out *[]any) {
	var init bool
	var c costusage.CostVer2
	if pg, ok := p.(*core.PointGetPlan); ok {
		init = cvGetField(pg, "planCostInit").Bool()
		c = cvGetField(pg, "planCostVer2").Interface().(costusage.CostVer2)
	} else {
		init = cvGetField(p, "PlanCostInit").Bool()
		c = cvGetField(p, "PlanCostVer2").Interface().(costusage.CostVer2)
	}
	// The Go type, not TP(): TableScan's TP is in explain.go (tidb-rs D10.9).
	tp := strings.TrimPrefix(fmt.Sprintf("%T", p), "*core.")
	*out = append(*out, append([]any{tp, init}, cvCost(c)...))
	if r, ok := p.(*core.PhysicalTableReader); ok {
		cvCollect(r.GetTablePlan(), out)
	}
	if r, ok := p.(*core.PhysicalIndexReader); ok && len(r.IndexPlans) > 0 {
		cvCollect(r.IndexPlans[len(r.IndexPlans)-1], out)
	}
	for _, ch := range p.Children() {
		cvCollect(ch, out)
	}
}

func cvRunFactor(fn string, task int, fp cvFactorPlan) any {
	sctx := cvSession()
	tbl := cvTable(fp.Temp, false, false, []int{int(mysql.TypeLonglong)})
	col := cvColumn(1, int(mysql.TypeLonglong))
	ts := core.PhysicalTableScan{Table: tbl, StoreType: kv.StoreType(fp.Store), Desc: fp.Desc}.Init(sctx, 0)
	ts.SetSchema(expression.NewSchema(col))
	idx := &model.IndexInfo{ID: 1, Name: pmodel.NewCIStr("idx"), Columns: []*model.IndexColumn{{Offset: 0, Length: types.UnspecifiedLength}}}
	is := core.PhysicalIndexScan{Table: tbl, Index: idx, Desc: fp.Desc}.Init(sctx, 0)
	is.SetSchema(expression.NewSchema(col))
	cvSetField(is, "dataSourceSchema", expression.NewSchema(col))
	mkReader := func(child base.PhysicalPlan) base.PhysicalPlan {
		r := core.PhysicalTableReader{StoreType: kv.StoreType(fp.Store)}
		cvSetField(&r, "tablePlan", child)
		return r.Init(sctx, 0)
	}
	mkIdxReader := func(child base.PhysicalPlan) base.PhysicalPlan {
		r := core.PhysicalIndexReader{}
		cvSetField(&r, "indexPlan", child)
		return r.Init(sctx, 0)
	}
	mkSel := func(child base.PhysicalPlan) base.PhysicalPlan {
		s := core.PhysicalSelection{Conditions: []expression.Expression{col}}.Init(sctx, &property.StatsInfo{RowCount: 1}, 0)
		s.SetChildren(child)
		return s
	}
	var p base.PhysicalPlan
	switch fp.Kind {
	case "ts":
		p = ts
	case "reader":
		p = mkReader(ts)
	case "sel_ts":
		p = mkSel(ts)
	case "sel_reader":
		p = mkSel(mkReader(ts))
	case "is":
		p = is
	case "ireader":
		p = mkIdxReader(is)
	case "sel_is":
		p = mkSel(is)
	case "sel_ireader":
		p = mkSel(mkIdxReader(is))
	case "pg":
		pgv := core.PointGetPlan{TblInfo: tbl}
		cvSetField(&pgv, "schema", expression.NewSchema(col))
		p = pgv.Init(sctx, &property.StatsInfo{RowCount: 1}, 0)
	}
	tt := property.TaskType(task)
	var f costusage.CostVer2Factor
	switch fn {
	case "cpu":
		f = cvGetTaskCPUFactorVer2(p, tt)
	case "mem":
		f = cvGetTaskMemFactorVer2(p, tt)
	case "scan":
		f = cvGetTaskScanFactorVer2(p, kv.StoreType(fp.Store), tt)
	case "net":
		f = cvGetTaskNetFactorVer2(p, tt)
	case "request":
		f = cvGetTaskRequestFactorVer2(p, tt)
	}
	return []any{f.Name, phF(f.Value), f.String()}
}

func cvRunFormula(args []json.RawMessage) any {
	sctx := cvSession()
	fn, rows, x := arg[string](args, 0), cvFloat(arg[string](args, 1)), cvFloat(arg[string](args, 2))
	kinds, name, trace := arg[[]string](args, 3), arg[string](args, 4), arg[bool](args, 5)
	var factor costusage.CostVer2Factor
	for _, f := range cvAllFactors() {
		if f.Name == name {
			factor = f
		}
	}
	opt := optimizetrace.NewDefaultPlanCostOption()
	if trace {
		opt.WithCostFlag(costusage.CostFlagTrace)
	}
	col := cvColumn(1, int(mysql.TypeLonglong))
	exprs := []expression.Expression{}
	by := []*plannerutil.ByItems{}
	for _, k := range kinds {
		e := cvExpr(sctx, k, col)
		exprs = append(exprs, e)
		by = append(by, &plannerutil.ByItems{Expr: e})
	}
	var c costusage.CostVer2
	switch fn {
	case "scan":
		c = cvScanCostVer2(opt, rows, x, factor)
	case "net":
		c = cvNetCostVer2(opt, rows, x, factor)
	case "filter":
		c = cvFilterCostVer2(opt, rows, exprs, factor)
	case "order":
		c = cvOrderCostVer2(opt, rows, x, by, factor)
	case "double_read":
		c = cvDoubleReadCostVer2(opt, rows, factor)
	}
	return cvCost(c)
}

// cvAllFactors: the factors of defaultVer2Factors the formula records use, read through the
// factor functions.
func cvAllFactors() []costusage.CostVer2Factor {
	sctx := cvSession()
	ts := core.PhysicalTableScan{Table: &model.TableInfo{}, StoreType: kv.TiFlash}.Init(sctx, 0)
	temp := core.PhysicalTableScan{Table: &model.TableInfo{TempTableType: model.TempTableGlobal}}.Init(sctx, 0)
	return []costusage.CostVer2Factor{
		cvGetTaskScanFactorVer2(temp, kv.TiKV, property.RootTaskType),
		cvGetTaskScanFactorVer2(ts, kv.TiKV, property.RootTaskType),
		cvGetTaskCPUFactorVer2(ts, property.RootTaskType),
		cvGetTaskNetFactorVer2(ts, property.RootTaskType),
		cvGetTaskRequestFactorVer2(ts, property.RootTaskType),
		cvGetTaskMemFactorVer2(ts, property.MppTaskType),
	}
}
