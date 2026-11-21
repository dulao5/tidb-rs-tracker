package domains

// Domain "physops": the physical operators, tasks and plan enumeration of the first vertical
// slice (tidb-rs VS1-27, specs/D10.1-physical-plans/spec.md): the pure helpers of
// physical_plans.go / task.go and exhaustPhysicalPlans of LogicalLimit, LogicalTopN,
// LogicalSort, LogicalSelection and LogicalProjection (exhaust_physical_plans.go).
//
// Records:
//   - "read_req": args [n] -> ReadReqType(n).Name().
//   - "expand": args [schema, columns, tableCols, virt] -> {"cols": [ids], "schema": [[id,
//     uid]]}: ExpandVirtualColumn(columns, schema, tableCols). schema is [[id, uid]],
//     columns / tableCols are column ids (ColumnInfo{ID}), virt is [[id, dep]]: a column with
//     id `id` is virtual on the column `dep` (uid 1000+dep, itself virtual when listed).
//   - "match_items": args [sortItems [[uid, desc]], byItems [[kind, uid, desc]]] (kind "c"
//     column, "k" constant 1) -> MatchItems(prop, items).
//   - "paging": args [indexRows, [leafRows], expectCnt, seekFactor, cpuFactor] (row counts as
//     float strings; a factor "" is the default) -> calcPagingCost(ctx, Selection with
//     indexRows rows over table scans with leafRows rows, expectCnt).
//   - "avg_row_size": args [[ft]] -> getAvgRowSize(stats without HistColl, columns of the
//     types); ft is [tp, flag, flen, decimal].
//   - "exhaust": args [case] -> {"plans": [candidate], "hint": bool, "prefer": bool,
//     "warnings": [msg]}: lp.ExhaustPhysicalPlans(prop) of a logical operator over a
//     DataSource or a TableDual (see phExhaustIn). A candidate is {"tp", "nominal", "rows",
//     fields of the operator, "child": the required child property}.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"sync"
	gotime "time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	"github.com/pingcap/tidb/pkg/planner/property"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const physopsSeed = 20261121

//go:linkname phCalcPagingCost github.com/pingcap/tidb/pkg/planner/core.calcPagingCost
func phCalcPagingCost(ctx base.PlanContext, indexPlan base.PhysicalPlan, expectCnt uint64) float64

//go:linkname phGetAvgRowSize github.com/pingcap/tidb/pkg/planner/core.getAvgRowSize
func phGetAvgRowSize(stats *property.StatsInfo, cols []*expression.Column) float64

func init() {
	vec.Register(&vec.Domain{
		Name:     "physops",
		Seed:     physopsSeed,
		Coverage: physopsCoverage,
		Blocks:   []string{"D10.1", "D10.3"},
		Exempt:   physopsExempt(),
		Gzip:     true,
		Generate: physopsGenerate,
		Exec:     physopsExec,
	})
}

var physopsCoverage = map[string]vec.Coverage{
	"read_req":     {Min: 4, GoFuncs: []string{"ReadReqType.Name"}},
	"expand":       {Min: 300, GoFuncs: []string{"ExpandVirtualColumn", "expandVirtualColumn"}},
	"match_items":  {Min: 300, GoFuncs: []string{"MatchItems"}},
	"paging":       {Min: 300, GoFuncs: []string{"calcPagingCost", "extractRows"}},
	"avg_row_size": {Min: 300, GoFuncs: []string{"getAvgRowSize"}},
	"exhaust": {Min: 1000, GoFuncs: []string{
		"LogicalLimit.ExhaustPhysicalPlans", "exhaustPhysicalPlans4LogicalLimit", "getLimitPhysicalPlans",
		"LogicalTopN.ExhaustPhysicalPlans", "exhaustPhysicalPlans4LogicalTopN", "getPhysTopN", "getPhysLimits",
		"canUsePartialOrder4TopN", "checkPartialOrderPattern", "getPhysTopNWithPartialOrderProperty",
		"pushLimitOrTopNForcibly",
		"LogicalSort.ExhaustPhysicalPlans", "exhaustPhysicalPlans4LogicalSort", "getPhysicalSort", "getNominalSort",
		"getNominalSortSimple",
		"LogicalSelection.ExhaustPhysicalPlans", "exhaustPhysicalPlans4LogicalSelection",
		"LogicalProjection.ExhaustPhysicalPlans", "exhaustPhysicalPlans4LogicalProjection",
		"PhysicalLimit.GetPartitionBy",
	}},
}

func phF(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

var (
	phMu  sync.Mutex
	phCtx *mock.Context
)

// phSession: one mock context for all records (core.MockContext builds a mock domain with a
// stats handle, too heavy per record), with the statement context, the plan ids and the
// session variables the records set reset to their defaults.
func phSession() *mock.Context {
	if phCtx == nil {
		phCtx = core.MockContext()
	}
	sv := phCtx.GetSessionVars()
	sv.PlanColumnID.Store(0)
	sv.PlanID.Store(0)
	sv.StmtCtx = stmtctx.NewStmtCtxWithTimeZone(gotime.UTC)
	for name, v := range map[string]string{
		variable.TiDBAllowMPPExecution: "ON",
		variable.TiDBOptSeekFactor:     phF(variable.DefOptSeekFactor),
		variable.TiDBOptCPUFactor:      phF(variable.DefOptCPUFactor),
	} {
		if err := sv.SetSystemVar(name, v); err != nil {
			panic(err)
		}
	}
	sv.LimitPushDownThreshold = variable.DefOptLimitPushDownThreshold
	sv.OptPartialOrderedIndexForTopN = variable.DefTiDBOptPartialOrderedIndexForTopN
	sv.AllowProjectionPushDown = variable.DefOptEnableProjectionPushDown
	return phCtx
}

func phCol(uid, id int64) *expression.Column {
	return &expression.Column{UniqueID: uid, ID: id, RetType: types.NewFieldType(mysql.TypeLonglong)}
}

// ---------------------------------------------------------------- generate

func physopsGenerate(g *vec.Gen, rng *rand.Rand) {
	for n := 0; n < 4; n++ {
		g.Add("sys/read_req", "read_req", n)
	}
	// expand: random schemas over a table of columns 1..8, with virtual columns on smaller
	// ids, and the extra handle / physical table id columns at the end.
	for i := 0; i < 400; i++ {
		nt := 2 + rng.IntN(7)
		tableCols := []int64{}
		for id := 1; id <= nt; id++ {
			tableCols = append(tableCols, int64(id))
		}
		virt := [][]int64{}
		for id := 2; id <= nt; id++ {
			if rng.IntN(3) == 0 {
				virt = append(virt, []int64{int64(id), int64(1 + rng.IntN(id-1))})
			}
		}
		schema := [][]int64{}
		cols := []int64{}
		for id := 1; id <= nt; id++ {
			if rng.IntN(2) == 0 {
				schema = append(schema, []int64{int64(id), 1000 + int64(id)})
				cols = append(cols, int64(id))
			}
		}
		extra := rng.IntN(4)
		if extra&1 != 0 {
			schema = append(schema, []int64{model.ExtraHandleID, 5000})
			cols = append(cols, model.ExtraHandleID)
		}
		if extra&2 != 0 {
			schema = append(schema, []int64{model.ExtraPhysTblID, 5001})
			cols = append(cols, model.ExtraPhysTblID)
		}
		g.Add(fmt.Sprintf("rand/expand/%d", i), "expand", schema, cols, tableCols, virt)
	}
	// match_items.
	for i := 0; i < 400; i++ {
		np, nb := rng.IntN(4), rng.IntN(4)
		sort := [][]any{}
		for j := 0; j < np; j++ {
			sort = append(sort, []any{1 + rng.IntN(3), rng.IntN(2) == 0})
		}
		by := [][]any{}
		for j := 0; j < nb; j++ {
			kind := "c"
			if rng.IntN(5) == 0 {
				kind = "k"
			}
			by = append(by, []any{kind, 1 + rng.IntN(3), rng.IntN(2) == 0})
		}
		g.Add(fmt.Sprintf("rand/match/%d", i), "match_items", sort, by)
	}
	// paging.
	rows := []float64{0, 0.5, 1, 3, 10, 127, 128, 129, 1000, 8192, 50000, 1e6, 1e9}
	for i := 0; i < 400; i++ {
		leaves := []string{}
		total := 0.0
		for j := 0; j < 1+rng.IntN(3); j++ {
			// float64(...) rounds the product: no FMA in the generator (arguments must be
			// the same on every target).
			r := float64(rows[rng.IntN(len(rows))] * (0.5 + rng.Float64()))
			total += r
			leaves = append(leaves, phF(r))
		}
		indexRows := float64(total * (0.05 + rng.Float64()))
		if rng.IntN(4) == 0 {
			indexRows = float64(rows[rng.IntN(len(rows))] * rng.Float64())
		}
		expect := []uint64{0, 1, 10, 128, 129, 1000, 5000, 100000, math.MaxUint32}[rng.IntN(9)]
		factors := []string{"", "0.1", "1.7", "33.3"}
		g.Add(fmt.Sprintf("rand/paging/%d", i), "paging", phF(indexRows), leaves, expect,
			factors[rng.IntN(4)], factors[rng.IntN(4)])
	}
	// avg_row_size.
	tps := []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeFloat,
		mysql.TypeDouble, mysql.TypeNewDecimal, mysql.TypeVarchar, mysql.TypeString, mysql.TypeBlob,
		mysql.TypeDate, mysql.TypeDatetime, mysql.TypeDuration, mysql.TypeJSON, mysql.TypeEnum, mysql.TypeBit}
	for i := 0; i < 400; i++ {
		fts := [][]any{}
		for j := 0; j < 1+rng.IntN(5); j++ {
			flen := []int{-1, 0, 1, 10, 255, 65535, 1 << 20}[rng.IntN(7)]
			fts = append(fts, []any{int(tps[rng.IntN(len(tps))]), 0, flen, rng.IntN(5) - 1})
		}
		g.Add(fmt.Sprintf("rand/avg_row_size/%d", i), "avg_row_size", fts)
	}
	// exhaust.
	for _, c := range phExhaustCases(rng) {
		g.Add(c.src, "exhaust", c.in)
	}
}

// phExhaustIn is one exhaust case: a logical operator over a child, a required property and
// the session variables.
type phExhaustIn struct {
	Op        string     `json:"op"`    // limit, topn, sort, sel, proj
	Child     string     `json:"child"` // ds (TiKV path), ds_flash (TiKV and TiFlash paths), ds_cached, dual, sel_ds (Selection over ds)
	Offset    uint64     `json:"offset"`
	Count     uint64     `json:"count"`
	By        [][]any    `json:"by"`    // [[kind, uid, desc]]
	Exprs     [][]any    `json:"exprs"` // proj: [[kind, uid]] (output column uid 100+i)
	Prefer    bool       `json:"prefer"`
	Rows      string     `json:"rows"`
	PropTp    int        `json:"prop_tp"`
	PropSort  [][]any    `json:"prop_sort"` // [[uid, desc]]
	PropExp   string     `json:"prop_exp"`
	Reject    bool       `json:"reject"`
	PropPart  [][]any    `json:"prop_part"` // partial order sort items or null
	MPP       bool       `json:"mpp"`
	Threshold int64      `json:"threshold"`
	Partial   bool       `json:"partial"`
	ProjPush  bool       `json:"proj_push"`
	Extra     [][]string `json:"extra,omitempty"`
}

type phCase struct {
	src string
	in  phExhaustIn
}

func phExhaustCases(rng *rand.Rand) []phCase {
	out := []phCase{}
	ops := []string{"limit", "topn", "sort", "sel", "proj"}
	children := []string{"ds", "ds_flash", "ds_cached", "dual", "sel_ds"}
	exps := []string{"0", "1", "5", "10", "100", phF(math.MaxFloat64)}
	for i := 0; i < 1500; i++ {
		c := phExhaustIn{
			Op:        ops[rng.IntN(len(ops))],
			Child:     children[rng.IntN(len(children))],
			Offset:    []uint64{0, 0, 1, 5, 50}[rng.IntN(5)],
			Count:     []uint64{0, 1, 10, 60, 100, 1000}[rng.IntN(6)],
			Prefer:    rng.IntN(4) == 0,
			Rows:      []string{"0", "0.5", "1", "20", "1000"}[rng.IntN(5)],
			PropTp:    rng.IntN(4),
			PropExp:   exps[rng.IntN(len(exps))],
			Reject:    rng.IntN(3) == 0,
			MPP:       rng.IntN(2) == 0,
			Threshold: []int64{0, 100, 1000}[rng.IntN(3)],
			Partial:   rng.IntN(3) == 0,
			ProjPush:  rng.IntN(2) == 0,
		}
		nb := rng.IntN(3)
		if c.Op == "topn" || c.Op == "sort" {
			nb = 1 + rng.IntN(2)
		}
		for j := 0; j < nb; j++ {
			kind := "c"
			if rng.IntN(6) == 0 {
				kind = "k"
			}
			c.By = append(c.By, []any{kind, 1 + rng.IntN(3), rng.IntN(2) == 0})
		}
		for j := 0; j < 1+rng.IntN(3); j++ {
			kind := "c"
			if rng.IntN(5) == 0 {
				kind = "k"
			}
			c.Exprs = append(c.Exprs, []any{kind, 1 + rng.IntN(3)})
		}
		// The required order: often the operator's own items, else random. A projection
		// can only be asked for its own output columns (Go indexes Exprs by the position).
		if c.Op == "proj" {
			for j := 0; j < rng.IntN(3); j++ {
				c.PropSort = append(c.PropSort, []any{100 + rng.IntN(len(c.Exprs)), rng.IntN(2) == 0})
			}
		} else if rng.IntN(2) == 0 {
			for _, b := range c.By {
				if b[0] == "c" && rng.IntN(4) != 0 {
					c.PropSort = append(c.PropSort, []any{b[1], b[2]})
				}
			}
		} else {
			for j := 0; j < rng.IntN(3); j++ {
				c.PropSort = append(c.PropSort, []any{1 + rng.IntN(3), rng.IntN(2) == 0})
			}
		}
		if c.Op == "proj" && rng.IntN(4) == 0 {
			c.PropPart = [][]any{{100 + rng.IntN(len(c.Exprs)), rng.IntN(2) == 0}}
		}
		out = append(out, phCase{fmt.Sprintf("rand/exhaust/%d", i), c})
	}
	return out
}

// ---------------------------------------------------------------- exec

func physopsExec(op string, args []json.RawMessage) (out any, err error) {
	phMu.Lock()
	defer phMu.Unlock()
	switch op {
	case "read_req":
		return core.ReadReqType(arg[int](args, 0)).Name(), nil
	case "expand":
		return phExpand(args), nil
	case "match_items":
		sort, by := arg[[][]any](args, 0), arg[[][]any](args, 1)
		prop := &property.PhysicalProperty{SortItems: phSortItems(sort)}
		return core.MatchItems(prop, phByItems(by)), nil
	case "paging":
		return phPaging(args), nil
	case "avg_row_size":
		fts := arg[[][]int](args, 0)
		cols := []*expression.Column{}
		for i, ft := range fts {
			tp := types.NewFieldType(byte(ft[0]))
			tp.SetFlag(uint(ft[1]))
			tp.SetFlen(ft[2])
			tp.SetDecimal(ft[3])
			cols = append(cols, &expression.Column{UniqueID: int64(i + 1), RetType: tp})
		}
		return phF(phGetAvgRowSize(&property.StatsInfo{RowCount: 1}, cols)), nil
	case "exhaust":
		return phExhaust(arg[phExhaustIn](args, 0))
	}
	return nil, fmt.Errorf("physops: unknown op %s", op)
}

func phSortItems(items [][]any) []property.SortItem {
	out := []property.SortItem{}
	for _, it := range items {
		uid := int64(it[0].(float64))
		out = append(out, property.SortItem{Col: phCol(uid, uid), Desc: it[1].(bool)})
	}
	return out
}

func phByItems(items [][]any) []*plannerutil.ByItems {
	out := []*plannerutil.ByItems{}
	for _, it := range items {
		uid := int64(it[1].(float64))
		var e expression.Expression = phCol(uid, uid)
		if it[0] == "k" {
			e = expression.NewOne()
		}
		out = append(out, &plannerutil.ByItems{Expr: e, Desc: it[2].(bool)})
	}
	return out
}

func phExpand(args []json.RawMessage) any {
	schemaIn, colIDs, tableIDs, virt := arg[[][]int64](args, 0), arg[[]int64](args, 1), arg[[]int64](args, 2),
		arg[[][]int64](args, 3)
	deps := map[int64]int64{}
	for _, v := range virt {
		deps[v[0]] = v[1]
	}
	var mk func(id, uid int64) *expression.Column
	mk = func(id, uid int64) *expression.Column {
		c := phCol(uid, id)
		if d, ok := deps[id]; ok {
			c.VirtualExpr = mk(d, 1000+d)
		}
		return c
	}
	schema := expression.NewSchema()
	for _, s := range schemaIn {
		schema.Append(mk(s[0], s[1]))
	}
	info := func(ids []int64) []*model.ColumnInfo {
		out := []*model.ColumnInfo{}
		for _, id := range ids {
			out = append(out, &model.ColumnInfo{ID: id})
		}
		return out
	}
	cols := core.ExpandVirtualColumn(info(colIDs), schema, info(tableIDs))
	outCols := []any{}
	for _, c := range cols {
		if c == nil {
			outCols = append(outCols, nil)
		} else {
			outCols = append(outCols, c.ID)
		}
	}
	outSchema := [][]int64{}
	for _, c := range schema.Columns {
		outSchema = append(outSchema, []int64{c.ID, c.UniqueID})
	}
	return map[string]any{"cols": outCols, "schema": outSchema}
}

func phPaging(args []json.RawMessage) any {
	indexRowsS, leaves, expect := arg[string](args, 0), arg[[]string](args, 1), arg[uint64](args, 2)
	indexRows, _ := strconv.ParseFloat(indexRowsS, 64)
	sctx := phSession()
	for i, name := range []string{variable.TiDBOptSeekFactor, variable.TiDBOptCPUFactor} {
		if v := arg[string](args, 3+i); v != "" {
			if err := sctx.GetSessionVars().SetSystemVar(name, v); err != nil {
				panic(err)
			}
		}
	}
	children := []base.PhysicalPlan{}
	for _, rs := range leaves {
		r, _ := strconv.ParseFloat(rs, 64)
		ts := core.PhysicalTableScan{}.Init(sctx, 0)
		ts.SetStats(&property.StatsInfo{RowCount: r})
		children = append(children, ts)
	}
	sel := core.PhysicalSelection{}.Init(sctx, &property.StatsInfo{RowCount: indexRows}, 0)
	sel.SetChildren(children...)
	return phF(phCalcPagingCost(sctx, sel, expect))
}

func phDataSource(sctx *mock.Context, child string) *logicalop.DataSource {
	tbl := &model.TableInfo{ID: 100}
	if child == "ds_cached" {
		tbl.TableCacheStatusType = model.TableCacheStatusEnable
	}
	paths := []*plannerutil.AccessPath{{StoreType: kv.TiKV}}
	if child == "ds_flash" {
		paths = append(paths, &plannerutil.AccessPath{StoreType: kv.TiFlash})
	}
	ds := logicalop.DataSource{TableInfo: tbl, PossibleAccessPaths: paths}.Init(sctx, 0)
	ds.SetSchema(expression.NewSchema(phCol(1, 1), phCol(2, 2), phCol(3, 3)))
	ds.SetStats(&property.StatsInfo{RowCount: 1000})
	return ds
}

func phExhaust(in phExhaustIn) (any, error) {
	sctx := phSession()
	sv := sctx.GetSessionVars()
	mpp := "OFF"
	if in.MPP {
		mpp = "ON"
	}
	if err := sv.SetSystemVar(variable.TiDBAllowMPPExecution, mpp); err != nil {
		return nil, err
	}
	sv.LimitPushDownThreshold = in.Threshold
	if in.Partial {
		sv.OptPartialOrderedIndexForTopN = "COST"
	}
	sv.AllowProjectionPushDown = in.ProjPush

	var child base.LogicalPlan
	switch in.Child {
	case "dual":
		d := logicalop.LogicalTableDual{}.Init(sctx, 0)
		d.SetSchema(expression.NewSchema(phCol(1, 1), phCol(2, 2), phCol(3, 3)))
		d.SetStats(&property.StatsInfo{RowCount: 1})
		child = d
	case "sel_ds":
		ds := phDataSource(sctx, "ds")
		s := logicalop.LogicalSelection{Conditions: []expression.Expression{phCol(1, 1)}}.Init(sctx, 0)
		s.SetChildren(ds)
		s.SetStats(&property.StatsInfo{RowCount: 500})
		child = s
	default:
		child = phDataSource(sctx, in.Child)
	}
	rows, _ := strconv.ParseFloat(in.Rows, 64)
	propExp, _ := strconv.ParseFloat(in.PropExp, 64)
	stats := &property.StatsInfo{RowCount: rows}
	var lp base.LogicalPlan
	prefer := func() bool { return false }
	switch in.Op {
	case "limit":
		l := logicalop.LogicalLimit{Offset: in.Offset, Count: in.Count, PreferLimitToCop: in.Prefer}.Init(sctx, 0)
		prefer = func() bool { return l.PreferLimitToCop }
		lp = l
	case "topn":
		t := logicalop.LogicalTopN{ByItems: phByItems(in.By), Offset: in.Offset, Count: in.Count,
			PreferLimitToCop: in.Prefer}.Init(sctx, 0)
		prefer = func() bool { return t.PreferLimitToCop }
		lp = t
	case "sort":
		lp = logicalop.LogicalSort{ByItems: phByItems(in.By)}.Init(sctx, 0)
	case "sel":
		conds := []expression.Expression{}
		for _, b := range phByItems(in.By) {
			conds = append(conds, b.Expr)
		}
		lp = logicalop.LogicalSelection{Conditions: conds}.Init(sctx, 0)
	case "proj":
		exprs := []expression.Expression{}
		cols := []*expression.Column{}
		for i, e := range in.Exprs {
			uid := int64(e[1].(float64))
			var x expression.Expression = phCol(uid, uid)
			if e[0] == "k" {
				x = expression.NewOne()
			}
			exprs = append(exprs, x)
			cols = append(cols, phCol(100+int64(i), 0))
		}
		p := logicalop.LogicalProjection{Exprs: exprs}.Init(sctx, 0)
		p.SetSchema(expression.NewSchema(cols...))
		lp = p
	}
	lp.SetChildren(child)
	lp.(interface{ SetStats(*property.StatsInfo) }).SetStats(stats)
	prop := &property.PhysicalProperty{TaskTp: property.TaskType(in.PropTp), SortItems: phSortItems(in.PropSort),
		ExpectedCnt: propExp, RejectSort: in.Reject}
	if in.PropPart != nil {
		ptrs := []*property.SortItem{}
		for _, s := range phSortItems(in.PropPart) {
			s := s
			ptrs = append(ptrs, &s)
		}
		prop.PartialOrderInfo = &property.PartialOrderInfo{SortItems: ptrs}
	}
	plans, hint, err := lp.ExhaustPhysicalPlans(prop)
	if err != nil {
		return nil, err
	}
	outPlans := []any{}
	for _, p := range plans {
		outPlans = append(outPlans, phCandidate(p))
	}
	warns := []string{}
	for _, w := range sv.StmtCtx.GetWarnings() {
		warns = append(warns, w.Err.Error())
	}
	return map[string]any{"plans": outPlans, "hint": hint, "prefer": prefer(), "warnings": warns}, nil
}

func phItems(items []property.SortItem) []any {
	out := []any{}
	for _, s := range items {
		out = append(out, []any{s.Col.UniqueID, s.Desc})
	}
	return out
}

func phBy(by []*plannerutil.ByItems) []any {
	out := []any{}
	for _, b := range by {
		var uid any = nil
		if c, ok := b.Expr.(*expression.Column); ok {
			uid = c.UniqueID
		}
		out = append(out, []any{uid, b.Desc})
	}
	return out
}

func phExprs(es []expression.Expression) []any {
	out := []any{}
	for _, e := range es {
		var uid any = nil
		if c, ok := e.(*expression.Column); ok {
			uid = c.UniqueID
		}
		out = append(out, uid)
	}
	return out
}

func phProp(p *property.PhysicalProperty) any {
	if p == nil {
		return nil
	}
	o := map[string]any{"tp": int(p.TaskTp), "exp": phF(p.ExpectedCnt), "sort": phItems(p.SortItems),
		"reject": p.RejectSort, "cte": int(p.CTEProducerStatus), "advisory": phItems(p.AdvisorySortItems),
		"part": nil, "vec": p.VectorProp.VectorHelper != nil}
	if p.PartialOrderInfo != nil {
		items := []any{}
		for _, s := range p.PartialOrderInfo.SortItems {
			items = append(items, []any{s.Col.UniqueID, s.Desc})
		}
		o["part"] = items
	}
	return o
}

func phSchema(s *expression.Schema) []any {
	out := []any{}
	for _, c := range s.Columns {
		out = append(out, c.UniqueID)
	}
	return out
}

func phCandidate(p base.PhysicalPlan) any {
	o := map[string]any{"tp": p.TP(), "nominal": false, "rows": phF(p.StatsInfo().RowCount),
		"child": phProp(p.GetChildReqProps(0))}
	switch x := p.(type) {
	case *core.PhysicalLimit:
		o["offset"], o["count"] = x.Offset, x.Count
		o["part_by"] = len(x.GetPartitionBy())
		o["schema"] = phSchema(x.Schema())
	case *core.PhysicalTopN:
		o["offset"], o["count"] = x.Offset, x.Count
		o["by"] = phBy(x.ByItems)
	case *core.PhysicalSort:
		o["by"] = phBy(x.ByItems)
	case *core.NominalSort:
		o["nominal"] = true
		o["only"] = x.OnlyColumn
		o["by"] = phBy(x.ByItems)
	case *core.PhysicalSelection:
		o["conds"] = phExprs(x.Conditions)
	case *core.PhysicalProjection:
		o["exprs"] = phExprs(x.Exprs)
		o["schema"] = phSchema(x.Schema())
	}
	return o
}

var _ = unsafe.Sizeof(0)
