package domains

// Domain "aggexec": block D12.3 (aggregation executors) and D12.0 vecgroupchecker:
//   - Part A: StreamAggExec + VecGroupChecker ("stream_agg");
//   - Part B: GetGroupKey ("group_key"), single-threaded HashAggExec ("hash_agg_unparallel"),
//     parallel HashAggExec ("hash_agg_parallel"), and HashAggRuntimeStats ("runtime_stats")
//     (tidb-rs specs/D12.3-agg-executors/spec.md §5.2).
//
// As in the basicexec domain, the child of stream_agg / hash_agg_* is the TableDualExec built
// for the plan's child, with Open, Next and Close replaced: it returns one batch per Next,
// then empty chunks, and with err_at = k fails its k-th Next ("mock child error").
//
// Ops:
//   - "stream_agg": args [aeCfg] -> {"chunks": [[row...]...], "step": ""|"open"|"next"|"close"}
//   - "group_key": args [aeGroupKeyCfg] -> {"keys": [hex...]}
//   - "hash_agg_unparallel": args [aeCfg] -> {"chunks": [[row...]...], "step": ...}
//     (concurrency 1/1, or DISTINCT forcing unparallel; deterministic first-seen group order)
//   - "hash_agg_parallel": args [aeParallelCfg] -> {"rows": [row...], "step": ...}
//     (P, F in 1..8, not both 1; rows sorted canonically for multiset comparison; generator
//     excludes merge-order-dependent non-group-key FIRST_ROW, CI-collated FIRST_ROW, and
//     float SUM/AVG; on "next" error, rows is [] because partial output depends on scheduling)
//   - "runtime_stats": args [aeStatsCfg] -> {"str": s, "clone": s, "merged": s, "tp": int}

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/executor/aggfuncs"
	"github.com/pingcap/tidb/pkg/executor/aggregate"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/aggregation"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannercore "github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/execdetails"
	"github.com/pingcap/tidb/pkg/util/memory"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const aggexecSeed = 20261126

func init() {
	vec.Register(&vec.Domain{
		Name: "aggexec",
		Seed: aggexecSeed,
		Errors: []vec.NamedErr{{Name: "ErrOverflow", Err: types.ErrOverflow}, {Name: "ErrTruncated", Err: types.ErrTruncated},
			{Name: "ErrBadNumber", Err: types.ErrBadNumber}},
		Coverage: aggexecCoverage,
		Blocks:   []string{"D12.3"},
		Files:    []string{aeVGC},
		Exempt:   aggexecExempt(),
		Gzip:     true,
		Generate: aggexecGenerate,
		Exec:     aggexecExec,
	})
}

const aeVGC = "pkg/executor/internal/vecgroupchecker/vec_group_checker.go"

var aggexecCoverage = map[string]vec.Coverage{
	"stream_agg": {Min: 1500, GoFuncs: []string{"executorBuilder.buildStreamAgg", "executorBuilder.buildStreamAggFromChildExec",
		"StreamAggExec.Open", "StreamAggExec.OpenSelf", "StreamAggExec.Close", "StreamAggExec.Next",
		"StreamAggExec.consumeOneGroup", "StreamAggExec.consumeGroupRows", "StreamAggExec.consumeCurGroupRowsAndFetchChild",
		"StreamAggExec.appendResult2Chunk",
		aeVGC + ":NewVecGroupChecker", aeVGC + ":VecGroupChecker.SplitIntoGroups",
		aeVGC + ":VecGroupChecker.getFirstAndLastRowDatum", aeVGC + ":VecGroupChecker.evalGroupItemsAndResolveGroups",
		aeVGC + ":VecGroupChecker.GetNextGroup", aeVGC + ":VecGroupChecker.IsExhausted", aeVGC + ":VecGroupChecker.Reset"}},
	"group_key": {Min: 200, GoFuncs: []string{"GetGroupKey"}},
	"hash_agg_unparallel": {Min: 350, GoFuncs: []string{
		"executorBuilder.buildHashAgg", "executorBuilder.buildHashAggFromChildExec",
		"HashAggExec.Open", "HashAggExec.OpenSelf", "HashAggExec.Close", "HashAggExec.Next",
		"HashAggExec.initForUnparallelExec", "HashAggExec.unparallelExec", "HashAggExec.resetSpillMode",
		"HashAggExec.execute", "HashAggExec.getNextChunk", "HashAggExec.getPartialResults",
	}},
	"hash_agg_parallel": {Min: 350, GoFuncs: []string{
		"HashAggExec.initForParallelExec", "HashAggExec.initPartialWorkers", "HashAggExec.initFinalWorkers",
		"HashAggExec.prepare4ParallelExec", "HashAggExec.parallelExec", "HashAggExec.fetchChildData",
		"HashAggExec.spillIfNeed", "HashAggExec.waitPartialWorkerAndCloseOutputChs",
		"HashAggExec.waitAllWorkersAndCloseFinalOutputCh", "HashAggExec.initRuntimeStats",
		"newBaseHashAggWorker", "baseHashAggWorker.getPartialResultSliceLenConsiderByteAlign",
		"baseHashAggWorker.checkFinishChClosed",
		"HashAggPartialWorker.getChildInput", "HashAggPartialWorker.fetchChunkAndProcess",
		"HashAggPartialWorker.finalizeWorkerProcess", "HashAggPartialWorker.run",
		"HashAggPartialWorker.getPartialResultsOfEachRow", "HashAggPartialWorker.updatePartialResult",
		"HashAggPartialWorker.shuffleIntermData",
		"HashAggFinalWorker.getPartialInput", "HashAggFinalWorker.mergeInputIntoResultMap",
		"HashAggFinalWorker.consumeIntermData", "HashAggFinalWorker.generateResultAndSend",
		"HashAggFinalWorker.sendFinalResult", "HashAggFinalWorker.receiveFinalResultHolder",
		"HashAggFinalWorker.run", "HashAggFinalWorker.cleanup",
		"newSpillHelper", "parallelHashAggSpillHelper.close",
		"parallelHashAggSpillHelper.isSpilledChunksIOEmpty", "parallelHashAggSpillHelper.isNoSpill",
		"parallelHashAggSpillHelper.checkNeedSpill", "parallelHashAggSpillHelper.checkError",
		"getBuffer", "tryRecycleBuffer", "getGroupKeyMemUsage",
		"updateWaitTime", "updateWorkerTime", "updateExecTime",
	}},
	"runtime_stats": {Min: 80, GoFuncs: []string{
		"HashAggRuntimeStats.workerString", "HashAggRuntimeStats.String",
		"HashAggRuntimeStats.Clone", "HashAggRuntimeStats.Merge", "HashAggRuntimeStats.Tp",
		"AggWorkerStat.Clone",
	}},
}

type aeAgg struct {
	Name     string `json:"name"`
	Mode     int    `json:"mode"`
	Distinct bool   `json:"distinct"`
	Args     []int  `json:"args"`
	Ret      afType `json:"ret"`
}

type aeCfg struct {
	Types   []afType              `json:"types"`
	Batches [][][]json.RawMessage `json:"batches"`
	GroupBy []int                 `json:"group_by"`
	Aggs    []aeAgg               `json:"aggs"`
	Out     []afType              `json:"out"`
	Sizes   [2]int                `json:"sizes"`
	Reqs    []int                 `json:"reqs"`
	Vec     bool                  `json:"vec"`
	ErrAt   int                   `json:"err_at"`
}

// aeGenCfg is aeCfg as the generator writes it (batches of drawn values).
type aeGenCfg struct {
	Types   []afType  `json:"types"`
	Batches [][][]any `json:"batches"`
	GroupBy []int     `json:"group_by"`
	Aggs    []aeAgg   `json:"aggs"`
	Out     []afType  `json:"out"`
	Sizes   [2]int    `json:"sizes"`
	Reqs    []int     `json:"reqs"`
	Vec     bool      `json:"vec"`
	ErrAt   int       `json:"err_at"`
}

type aeParallelCfg struct {
	aeCfg
	PartialCon int `json:"partial_con"`
	FinalCon   int `json:"final_con"`
}

type aeParallelGenCfg struct {
	aeGenCfg
	PartialCon int `json:"partial_con"`
	FinalCon   int `json:"final_con"`
}

type aeGroupKeyCfg struct {
	Types    []afType            `json:"types"`
	Rows     [][]json.RawMessage `json:"rows"`
	GroupBy  []int               `json:"group_by"`
	Vec      bool                `json:"vec"`
	TzOffset int                 `json:"tz_offset"`
}

type aeGroupKeyGenCfg struct {
	Types    []afType `json:"types"`
	Rows     [][]any  `json:"rows"`
	GroupBy  []int    `json:"group_by"`
	Vec      bool     `json:"vec"`
	TzOffset int      `json:"tz_offset"`
}

type aeStatsInput struct {
	PCon   int        `json:"p_con"`
	PWall  int64      `json:"p_wall"`
	FCon   int        `json:"f_con"`
	FWall  int64      `json:"f_wall"`
	PStats [][4]int64 `json:"p_stats"`
	FStats [][4]int64 `json:"f_stats"`
}

type aeStatsCfg struct {
	Base  aeStatsInput `json:"base"`
	Other aeStatsInput `json:"other"`
}

// aeSource returns its batches, one per Next, then empty chunks; its errAt-th Next fails.
type aeSource struct {
	*executor.TableDualExec
	batches []*chunk.Chunk
	ptr     int
	nexts   int
	errAt   int
}

func (s *aeSource) Open(context.Context) error { return nil }
func (s *aeSource) Close() error               { return nil }

func (s *aeSource) Next(_ context.Context, req *chunk.Chunk) error {
	req.Reset()
	n := s.nexts
	s.nexts++
	if n == s.errAt {
		return errors.New("mock child error")
	}
	if s.ptr >= len(s.batches) {
		return nil
	}
	b := s.batches[s.ptr]
	req.Append(b, 0, b.NumRows())
	s.ptr++
	return nil
}

func aeFts(ts []afType) []*types.FieldType {
	fts := make([]*types.FieldType, len(ts))
	for i, t := range ts {
		fts[i] = t.ft()
	}
	return fts
}

func aeEncode(c *chunk.Chunk) []any {
	rows := make([]any, c.NumRows())
	for r := range c.NumRows() {
		cells := make([]any, c.NumCols())
		if c.NumCols() > 0 {
			row := c.GetRow(r)
			for col := range cells {
				cells[col] = map[string]any{"null": row.IsNull(col), "raw": hex.EncodeToString(row.GetRaw(col))}
			}
		}
		rows[r] = cells
	}
	return rows
}

func aeEncodeParallel(c *chunk.Chunk, fts []*types.FieldType) []any {
	rows := make([]any, c.NumRows())
	for r := range c.NumRows() {
		cells := make([]any, c.NumCols())
		if c.NumCols() > 0 {
			row := c.GetRow(r)
			for col := range cells {
				if row.IsNull(col) {
					cells[col] = map[string]any{"null": true, "raw": ""}
				} else {
					switch fts[col].GetType() {
					case mysql.TypeNewDecimal:
						cells[col] = map[string]any{"null": false, "raw": row.GetMyDecimal(col).String()}
					case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
						cells[col] = map[string]any{"null": false, "raw": fmt.Sprintf("%d", uint64(row.GetTime(col).CoreTime()))}
					case mysql.TypeDuration:
						cells[col] = map[string]any{"null": false, "raw": fmt.Sprintf("%d", int64(row.GetDuration(col, fts[col].GetDecimal()).Duration))}
					default:
						cells[col] = map[string]any{"null": false, "raw": hex.EncodeToString(row.GetRaw(col))}
					}
				}
			}
		}
		rows[r] = cells
	}
	return rows
}
func aeRowSortKey(row []any) string {
	var sb strings.Builder
	for i, c := range row {
		if i > 0 {
			sb.WriteByte('|')
		}
		m := c.(map[string]any)
		if m["null"].(bool) {
			sb.WriteString("N:")
		} else {
			sb.WriteString("V:")
			sb.WriteString(m["raw"].(string))
		}
	}
	return sb.String()
}

func aeBuildBatches(fts []*types.FieldType, raw [][][]json.RawMessage) []*chunk.Chunk {
	batches := make([]*chunk.Chunk, len(raw))
	for i, rows := range raw {
		c := chunk.NewChunkWithCapacity(fts, len(rows))
		for _, row := range rows {
			for col, v := range row {
				chunkAppend(c, fts[col], col, v, false)
			}
		}
		batches[i] = c
	}
	return batches
}

func aeBuildDescs(child *expression.Schema, aggs []aeAgg) []*aggregation.AggFuncDesc {
	descs := make([]*aggregation.AggFuncDesc, len(aggs))
	for i, a := range aggs {
		d := &aggregation.AggFuncDesc{Mode: aggregation.AggFunctionMode(a.Mode), HasDistinct: a.Distinct}
		d.Name = a.Name
		for _, c := range a.Args {
			d.Args = append(d.Args, child.Columns[c])
		}
		d.RetTp = a.Ret.ft()
		descs[i] = d
	}
	return descs
}

func aeBuildGroupBy(child *expression.Schema, cols []int) []expression.Expression {
	groupBy := make([]expression.Expression, len(cols))
	for i, c := range cols {
		groupBy[i] = child.Columns[c]
	}
	return groupBy
}

func aggexecExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "stream_agg":
		return aeExecStreamAgg(args[0])
	case "group_key":
		return aeExecGroupKey(args[0])
	case "hash_agg_unparallel":
		return aeExecHashAggUnparallel(args[0])
	case "hash_agg_parallel":
		return aeExecHashAggParallel(args[0])
	case "runtime_stats":
		return aeExecRuntimeStats(args[0])
	default:
		panic("aggexec: unknown op " + op)
	}
}

func aeExecStreamAgg(raw json.RawMessage) (any, error) {
	var cfg aeCfg
	mustUnmarshal(raw, &cfg)

	ctx := mock.NewContext()
	vars := ctx.GetSessionVars()
	vars.InitChunkSize = cfg.Sizes[0]
	vars.MaxChunkSize = cfg.Sizes[1]
	vars.EnableVectorizedExpression = cfg.Vec
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)

	fts := aeFts(cfg.Types)
	child := sortexecSchema(fts)
	descs := aeBuildDescs(child, cfg.Aggs)
	groupBy := aeBuildGroupBy(child, cfg.GroupBy)

	sg := &plannercore.PhysicalStreamAgg{}
	sg.AggFuncs = descs
	sg.GroupByItems = groupBy
	// basePhysicalAgg.Init (the plan id and context; Go's tests call it on the plan).
	sg.BasePhysicalPlan = sg.Init(ctx.GetPlanCtx(), beStats, 0).BasePhysicalPlan
	sg.SetSchema(sortexecSchema(aeFts(cfg.Out)))
	sg.SetChildren(beDual(ctx, child, 0))
	b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
	e := b.Build(sg)
	if e == nil {
		return nil, beBuilderErr(b)
	}
	batches := aeBuildBatches(fts, cfg.Batches)
	kids := e.AllChildren()
	kids[0] = &aeSource{TableDualExec: kids[0].(*executor.TableDualExec), batches: batches, errAt: cfg.ErrAt}
	e.SetAllChildren(kids)

	bg := context.Background()
	out := []any{}
	result := func(step string) map[string]any { return map[string]any{"chunks": out, "step": step} }
	if err := e.Open(bg); err != nil {
		return result("open"), err
	}
	chk := e.NewChunk()
	for i := 0; ; i++ {
		if i < len(cfg.Reqs) && cfg.Reqs[i] > 0 {
			chk.SetRequiredRows(cfg.Reqs[i], cfg.Sizes[1])
		}
		if err := e.Next(bg, chk); err != nil {
			_ = e.Close()
			return result("next"), err
		}
		if chk.NumRows() == 0 {
			break
		}
		out = append(out, aeEncode(chk))
	}
	if err := e.Close(); err != nil {
		return result("close"), err
	}
	return result(""), nil
}

func aeExecGroupKey(raw json.RawMessage) (any, error) {
	var cfg aeGroupKeyCfg
	mustUnmarshal(raw, &cfg)

	ctx := mock.NewContext()
	vars := ctx.GetSessionVars()
	vars.EnableVectorizedExpression = cfg.Vec
	loc := time.FixedZone("UTC", cfg.TzOffset)
	vars.StmtCtx.SetTimeZone(loc)
	vars.TimeZone = loc

	fts := aeFts(cfg.Types)
	child := sortexecSchema(fts)
	groupBy := aeBuildGroupBy(child, cfg.GroupBy)

	c := chunk.NewChunkWithCapacity(fts, len(cfg.Rows))
	for _, row := range cfg.Rows {
		for col, v := range row {
			chunkAppend(c, fts[col], col, v, false)
		}
	}

	keys, err := aggregate.GetGroupKey(ctx, c, nil, groupBy)
	if err != nil {
		return map[string]any{"keys": []string{}}, err
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = hex.EncodeToString(k)
	}
	return map[string]any{"keys": out}, nil
}

func aeExecHashAggUnparallel(raw json.RawMessage) (any, error) {
	var cfg aeCfg
	mustUnmarshal(raw, &cfg)

	ctx := mock.NewContext()
	vars := ctx.GetSessionVars()
	vars.InitChunkSize = cfg.Sizes[0]
	vars.MaxChunkSize = cfg.Sizes[1]
	vars.EnableVectorizedExpression = cfg.Vec
	vars.SetHashAggPartialConcurrency(1)
	vars.SetHashAggFinalConcurrency(1)
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)

	fts := aeFts(cfg.Types)
	child := sortexecSchema(fts)
	descs := aeBuildDescs(child, cfg.Aggs)
	groupBy := aeBuildGroupBy(child, cfg.GroupBy)

	hg := &plannercore.PhysicalHashAgg{}
	hg.AggFuncs = descs
	hg.GroupByItems = groupBy
	hg.BasePhysicalPlan = hg.Init(ctx.GetPlanCtx(), beStats, 0).BasePhysicalPlan
	hg.SetSchema(sortexecSchema(aeFts(cfg.Out)))
	hg.SetChildren(beDual(ctx, child, 0))
	b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
	e := b.Build(hg)
	if e == nil {
		return nil, beBuilderErr(b)
	}
	batches := aeBuildBatches(fts, cfg.Batches)
	kids := e.AllChildren()
	kids[0] = &aeSource{TableDualExec: kids[0].(*executor.TableDualExec), batches: batches, errAt: cfg.ErrAt}
	e.SetAllChildren(kids)

	bg := context.Background()
	out := []any{}
	result := func(step string) map[string]any { return map[string]any{"chunks": out, "step": step} }
	if err := e.Open(bg); err != nil {
		return result("open"), err
	}
	chk := e.NewChunk()
	for i := 0; ; i++ {
		if i < len(cfg.Reqs) && cfg.Reqs[i] > 0 {
			chk.SetRequiredRows(cfg.Reqs[i], cfg.Sizes[1])
		}
		if err := e.Next(bg, chk); err != nil {
			_ = e.Close()
			return result("next"), err
		}
		if chk.NumRows() == 0 {
			break
		}
		out = append(out, aeEncode(chk))
	}
	if err := e.Close(); err != nil {
		return result("close"), err
	}
	return result(""), nil
}

func aeExecHashAggParallel(raw json.RawMessage) (any, error) {
	var cfg aeParallelCfg
	mustUnmarshal(raw, &cfg)

	ctx := mock.NewContext()
	vars := ctx.GetSessionVars()
	vars.InitChunkSize = cfg.Sizes[0]
	vars.MaxChunkSize = cfg.Sizes[1]
	vars.EnableVectorizedExpression = cfg.Vec
	vars.SetHashAggPartialConcurrency(cfg.PartialCon)
	vars.SetHashAggFinalConcurrency(cfg.FinalCon)
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)
	vars.StmtCtx.RuntimeStatsColl = execdetails.NewRuntimeStatsColl(nil)

	fts := aeFts(cfg.Types)
	child := sortexecSchema(fts)
	descs := aeBuildDescs(child, cfg.Aggs)
	groupBy := aeBuildGroupBy(child, cfg.GroupBy)

	hg := &plannercore.PhysicalHashAgg{}
	hg.AggFuncs = descs
	hg.GroupByItems = groupBy
	hg.BasePhysicalPlan = hg.Init(ctx.GetPlanCtx(), beStats, 0).BasePhysicalPlan
	outFts := aeFts(cfg.Out)
	hg.SetSchema(sortexecSchema(outFts))
	hg.SetChildren(beDual(ctx, child, 0))
	b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
	e := b.Build(hg)
	if e == nil {
		return nil, beBuilderErr(b)
	}
	batches := aeBuildBatches(fts, cfg.Batches)
	kids := e.AllChildren()
	kids[0] = &aeSource{TableDualExec: kids[0].(*executor.TableDualExec), batches: batches, errAt: cfg.ErrAt}
	e.SetAllChildren(kids)

	bg := context.Background()
	rows := []any{}
	if err := e.Open(bg); err != nil {
		return map[string]any{"rows": []any{}, "step": "open"}, err
	}
	chk := e.NewChunk()
	for i := 0; ; i++ {
		if i < len(cfg.Reqs) && cfg.Reqs[i] > 0 {
			chk.SetRequiredRows(cfg.Reqs[i], cfg.Sizes[1])
		}
		if err := e.Next(bg, chk); err != nil {
			_ = e.Close()
			return map[string]any{"rows": []any{}, "step": "next"}, err
		}
		if chk.NumRows() == 0 {
			break
		}
		rows = append(rows, aeEncodeParallel(chk, outFts)...)
	}
	if err := e.Close(); err != nil {
		return map[string]any{"rows": []any{}, "step": "close"}, err
	}
	sort.Slice(rows, func(i, j int) bool {
		return aeRowSortKey(rows[i].([]any)) < aeRowSortKey(rows[j].([]any))
	})
	return map[string]any{"rows": rows, "step": ""}, nil
}

func aeMakeStats(in aeStatsInput) *aggregate.HashAggRuntimeStats {
	s := &aggregate.HashAggRuntimeStats{
		PartialConcurrency: in.PCon,
		PartialWallTime:    in.PWall,
		FinalConcurrency:   in.FCon,
		FinalWallTime:      in.FWall,
		PartialStats:       make([]*aggregate.AggWorkerStat, 0, len(in.PStats)),
		FinalStats:         make([]*aggregate.AggWorkerStat, 0, len(in.FStats)),
	}
	for _, w := range in.PStats {
		s.PartialStats = append(s.PartialStats, &aggregate.AggWorkerStat{
			TaskNum: w[0], WaitTime: w[1], ExecTime: w[2], WorkerTime: w[3],
		})
	}
	for _, w := range in.FStats {
		s.FinalStats = append(s.FinalStats, &aggregate.AggWorkerStat{
			TaskNum: w[0], WaitTime: w[1], ExecTime: w[2], WorkerTime: w[3],
		})
	}
	return s
}

func aeExecRuntimeStats(raw json.RawMessage) (any, error) {
	var cfg aeStatsCfg
	mustUnmarshal(raw, &cfg)
	s1 := aeMakeStats(cfg.Base)
	s2 := aeMakeStats(cfg.Other)
	str1 := s1.String()
	cloned := s1.Clone().String()
	tp := s1.Tp()
	s1.Merge(s2)
	merged := s1.String()
	return map[string]any{
		"str":    str1,
		"clone":  cloned,
		"merged": merged,
		"tp":     tp,
	}, nil
}

// aeKeyTypes: the group key types (every evaluation type of the group checker); afArgTypes
// without NULL.
var aeKeyTypes = func() []afType {
	var out []afType
	for _, t := range afArgTypes {
		if t.Tp != mysql.TypeNull {
			out = append(out, t)
		}
	}
	return out
}()

type aeGen struct {
	*afGen
	ctx *mock.Context
}

// agg draws a descriptor of name over a column of type at (index col) as the planner does;
// with final, the final half of Split over the columns starting at col. It returns the
// descriptor and the types of the source columns it reads (nil if Go rejects it or Build
// returns nil).
func (g *aeGen) agg(name string, at afType, distinct bool, mode int, col int) (a *aeAgg, cols []afType) {
	defer func() {
		if recover() != nil {
			a, cols = nil, nil
		}
	}()
	ectx := g.ctx.GetExprCtx()
	c := &expression.Column{RetType: at.ft(), Index: col, UniqueID: int64(col + 1)}
	desc, err := aggregation.NewAggFuncDesc(ectx, name, []expression.Expression{c}, distinct)
	if err != nil || desc.RetTp == nil {
		return nil, nil
	}
	desc.WrapCastForAggArgs(ectx)
	for i, arg := range desc.Args {
		if _, ok := arg.(*expression.Column); !ok {
			desc.Args[i] = &expression.Column{RetType: arg.GetType(ectx.GetEvalCtx()), Index: col + i}
		}
	}
	if mode == int(aggregation.FinalMode) {
		ords := []int{col, col + 1}
		_, desc = desc.Split(ords)
	} else {
		desc.Mode = aggregation.AggFunctionMode(mode)
	}
	if aggfuncs.Build(ectx, desc, 0) == nil {
		return nil, nil
	}
	a = &aeAgg{Name: desc.Name, Mode: int(desc.Mode), Distinct: desc.HasDistinct, Ret: afTypeOf(desc.RetTp)}
	for i, arg := range desc.Args {
		ac := arg.(*expression.Column)
		if ac.Index != col+i {
			panic("aggexec: an argument is not the next source column")
		}
		a.Args = append(a.Args, ac.Index)
		cols = append(cols, afTypeOf(arg.GetType(ectx.GetEvalCtx())))
	}
	return a, cols
}

// shape is what a record draws: the number of keys, the mode, the functions (nil: drawn),
// the number of rows (-1: drawn).
type aeShape struct {
	keys  int
	mode  int
	names []string
	rows  int
}

func (g *aeGen) record(s aeShape) *aeGenCfg {
	cfg := &aeGenCfg{GroupBy: []int{}, Aggs: []aeAgg{}, Out: []afType{}, Reqs: []int{}, ErrAt: -1}
	for k := range s.keys {
		cfg.Types = append(cfg.Types, aeKeyTypes[g.intn(len(aeKeyTypes))])
		cfg.GroupBy = append(cfg.GroupBy, k)
	}
	// Sometimes only the second key is grouped on (the first is then a plain column).
	if s.keys == 2 && g.intn(5) == 0 {
		cfg.GroupBy = []int{1}
	}
	names := s.names
	if names == nil {
		for range []int{0, 1, 1, 2, 2, 3}[g.intn(6)] {
			names = append(names, afPartANames[g.intn(len(afPartANames))])
		}
	}
	g.afGen.noNaN = false
	for _, name := range names {
		if name == ast.AggFuncSum || name == ast.AggFuncAvg {
			g.afGen.noNaN = true
		}
		for range 20 {
			at := afArgTypes[g.intn(len(afArgTypes))]
			distinct := s.mode == int(aggregation.CompleteMode) && name != ast.AggFuncCount && g.intn(4) == 0
			a, cols := g.agg(name, at, distinct, s.mode, len(cfg.Types))
			if a == nil {
				continue
			}
			cfg.Aggs = append(cfg.Aggs, *a)
			cfg.Types = append(cfg.Types, cols...)
			cfg.Out = append(cfg.Out, a.Ret)
			break
		}
	}
	if cfg.Types == nil {
		cfg.Types = []afType{}
	}

	maxSize := []int{1, 2, 3, 5, 32, 1024}[g.intn(6)]
	cfg.Sizes = [2]int{min(32, maxSize), maxSize}
	if g.intn(4) == 0 {
		cfg.Sizes[0] = 1
	}
	for range g.intn(4) {
		cfg.Reqs = append(cfg.Reqs, g.intn(maxSize+1))
	}
	cfg.Vec = g.intn(2) == 0

	nrows := s.rows
	if nrows < 0 {
		nrows = []int{0, 1, 2, 3, 5, 9, 17, 40, 100, 300}[g.intn(10)]
	}
	g.afGen.pool = map[int][]any{}
	var key []any
	rows := make([][]any, nrows)
	for r := range rows {
		if key == nil || g.intn(3) == 0 {
			key = make([]any, s.keys)
			for k := range key {
				key[k] = g.value(k, cfg.Types[k])
			}
		}
		row := make([]any, len(cfg.Types))
		copy(row, key)
		for c := s.keys; c < len(cfg.Types); c++ {
			row[c] = g.value(c, cfg.Types[c])
		}
		rows[r] = row
	}
	cfg.Batches = [][][]any{}
	for len(rows) > 0 {
		n := min(len(rows), 1+g.intn(maxSize))
		cfg.Batches = append(cfg.Batches, rows[:n])
		rows = rows[n:]
	}
	if g.intn(12) == 0 {
		cfg.ErrAt = g.intn(len(cfg.Batches) + 1)
	}
	return cfg
}

// aeIsFloatType reports whether t is a float/double type (excluded from parallel SUM/AVG
// because floating-point addition is non-associative across merge orders).
func aeIsFloatType(t afType) bool {
	return t.Tp == mysql.TypeFloat || t.Tp == mysql.TypeDouble
}

// aeIsDeterministicFirstRowKey reports whether a group key type has a single canonical value
// per equivalence class (excluding collations with case-insensitivity or PAD SPACE trailing
// spaces, and DECIMAL with multiple scales).
func aeIsDeterministicFirstRowKey(t afType) bool {
	return t.Tp == mysql.TypeLonglong || t.Tp == mysql.TypeLong || t.Tp == mysql.TypeTiny
}

// aeIsDeterministicMinMaxType reports whether MIN/MAX on t has a unique byte representation
// for equal values (excluding case-insensitive and PAD SPACE string collations, and DECIMAL
// scale ties).
func aeIsDeterministicMinMaxType(t afType) bool {
	if strings.HasSuffix(t.Collate, "_ci") {
		return false
	}
	if t.Tp == mysql.TypeVarchar || t.Tp == mysql.TypeString || t.Tp == mysql.TypeVarString {
		return t.Collate == "binary"
	}
	return true
}

// parallelRecord builds a deterministic record for hash_agg_parallel: excludes non-group-key
// FIRST_ROW, CI-collated FIRST_ROW, float SUM/AVG, and DISTINCT (which forces unparallel).
func (g *aeGen) parallelRecord(s aeShape, pCon, fCon int, allowErr bool) *aeParallelGenCfg {
	cfg := &aeGenCfg{GroupBy: []int{}, Aggs: []aeAgg{}, Out: []afType{}, Reqs: []int{}, ErrAt: -1}
	for k := range s.keys {
		cfg.Types = append(cfg.Types, aeKeyTypes[g.intn(len(aeKeyTypes))])
		cfg.GroupBy = append(cfg.GroupBy, k)
	}

	names := s.names
	if names == nil {
		for range []int{0, 1, 1, 2, 2, 3}[g.intn(6)] {
			names = append(names, afPartANames[g.intn(len(afPartANames))])
		}
	}
	g.afGen.noNaN = true
	for _, name := range names {
		if name == ast.AggFuncFirstRow {
			// Only allow FIRST_ROW on a deterministic group-by column so merge order cannot
			// change which row is picked.
			if len(cfg.GroupBy) == 0 {
				continue
			}
			kCol := cfg.GroupBy[0]
			kt := cfg.Types[kCol]
			if !aeIsDeterministicFirstRowKey(kt) {
				continue
			}
			ectx := g.ctx.GetExprCtx()
			c := &expression.Column{RetType: kt.ft(), Index: kCol, UniqueID: int64(kCol + 1)}
			desc, err := aggregation.NewAggFuncDesc(ectx, name, []expression.Expression{c}, false)
			if err != nil || desc.RetTp == nil {
				continue
			}
			desc.Mode = aggregation.AggFunctionMode(s.mode)
			if aggfuncs.Build(ectx, desc, 0) == nil {
				continue
			}
			a := aeAgg{Name: desc.Name, Mode: int(desc.Mode), Distinct: false, Args: []int{kCol}, Ret: afTypeOf(desc.RetTp)}
			cfg.Aggs = append(cfg.Aggs, a)
			cfg.Out = append(cfg.Out, a.Ret)
			continue
		}
		for range 25 {
			at := afArgTypes[g.intn(len(afArgTypes))]
			if (name == ast.AggFuncMin || name == ast.AggFuncMax) && !aeIsDeterministicMinMaxType(at) {
				continue
			}
			// Avoid float SUM/AVG (order-dependent rounding) and avoid types whose Cast for
			// SUM/AVG turns them into DOUBLE (e.g. strings/JSON/etc.).
			a, cols := g.agg(name, at, false, s.mode, len(cfg.Types))
			if a == nil {
				continue
			}
			if (name == ast.AggFuncSum || name == ast.AggFuncAvg) && (aeIsFloatType(at) || aeIsFloatType(a.Ret)) {
				continue
			}
			// Also ensure no source column of SUM/AVG is float.
			skip := false
			if name == ast.AggFuncSum || name == ast.AggFuncAvg {
				for _, ct := range cols {
					if aeIsFloatType(ct) {
						skip = true
						break
					}
				}
			}
			if skip {
				continue
			}
			cfg.Aggs = append(cfg.Aggs, *a)
			cfg.Types = append(cfg.Types, cols...)
			cfg.Out = append(cfg.Out, a.Ret)
			break
		}
	}
	if cfg.Types == nil {
		cfg.Types = []afType{}
	}

	maxSize := []int{2, 4, 8, 32, 128}[g.intn(5)]
	cfg.Sizes = [2]int{min(32, maxSize), maxSize}
	cfg.Vec = g.intn(2) == 0

	nrows := s.rows
	if nrows < 0 {
		nrows = []int{0, 1, 3, 9, 25, 60, 150}[g.intn(7)]
	}
	g.afGen.pool = map[int][]any{}
	// For non-key DECIMAL columns in parallelRecord, draw bounded decimals so DECIMAL
	// SUM/AVG/MIN/MAX AppendFinalResult2Chunk does not trigger Go TiDB v8.5.8's
	// HashAggFinalWorker.generateResultAndSend bug (swallowing AppendFinalResult2Chunk
	// overflow/truncation errors without appending, which misaligns chunk columns or panics
	// with index out of range; see dulao5/tidb-rs#384 and D12.3 spec §7 item 1).
	rows := make([][]any, nrows)
	// Draw a pool of distinct keys so parallel workers merge overlapping groups.
	keyPoolSize := max(1, min(16, nrows/3+1))
	keyPool := make([][]any, keyPoolSize)
	for i := range keyPool {
		keyPool[i] = make([]any, s.keys)
		for k := range s.keys {
			keyPool[i][k] = g.value(k, cfg.Types[k])
		}
	}
	for r := range rows {
		row := make([]any, len(cfg.Types))
		if s.keys > 0 {
			copy(row, keyPool[g.intn(keyPoolSize)])
		}
		for c := s.keys; c < len(cfg.Types); c++ {
			if cfg.Types[c].Tp == mysql.TypeNewDecimal {
				switch g.intn(8) {
				case 0:
					row[c] = nil
				case 1:
					row[c] = "0"
				case 2:
					row[c] = "-0.5"
				default:
					whole := g.intn(200001) - 100000
					frac := g.intn(10000)
					if whole < 0 {
						row[c] = fmt.Sprintf("-%d.%04d", -whole, frac)
					} else {
						row[c] = fmt.Sprintf("%d.%04d", whole, frac)
					}
				}
			} else {
				row[c] = g.value(c, cfg.Types[c])
			}
		}
		rows[r] = row
	}
	cfg.Batches = [][][]any{}
	for len(rows) > 0 {
		n := min(len(rows), 1+g.intn(maxSize))
		cfg.Batches = append(cfg.Batches, rows[:n])
		rows = rows[n:]
	}
	if allowErr && g.intn(14) == 0 {
		cfg.ErrAt = g.intn(len(cfg.Batches) + 1)
	}
	return &aeParallelGenCfg{
		aeGenCfg:   *cfg,
		PartialCon: pCon,
		FinalCon:   fCon,
	}
}

func (g *aeGen) groupKeyRecord(ts []afType, groupBy []int, nrows int, vecEnabled bool, tzOffset int) *aeGroupKeyGenCfg {
	g.afGen.noNaN = false
	g.afGen.pool = map[int][]any{}
	rows := make([][]any, nrows)
	for r := range rows {
		row := make([]any, len(ts))
		for c, t := range ts {
			row[c] = g.value(c, t)
		}
		rows[r] = row
	}
	return &aeGroupKeyGenCfg{
		Types:    ts,
		Rows:     rows,
		GroupBy:  groupBy,
		Vec:      vecEnabled,
		TzOffset: tzOffset,
	}
}

func (g *aeGen) statsInput() aeStatsInput {
	pCon := []int{0, 1, 2, 5, 8, 20}[g.intn(6)]
	fCon := []int{0, 1, 2, 5, 8, 25}[g.intn(6)]
	randDur := func() int64 {
		scales := []int64{1, 1_000, 1_000_000, 1_000_000_000}
		return int64(g.intn(500)) * scales[g.intn(len(scales))]
	}
	mk := func(n int) [][4]int64 {
		out := make([][4]int64, n)
		for i := range n {
			out[i] = [4]int64{int64(g.intn(50)), randDur(), randDur(), randDur()}
		}
		return out
	}
	return aeStatsInput{
		PCon:   pCon,
		PWall:  randDur(),
		FCon:   fCon,
		FWall:  randDur(),
		PStats: mk(pCon),
		FStats: mk(fCon),
	}
}

func aggexecGenerate(vg *vec.Gen, rng *rand.Rand) {
	collate.SetNewCollationEnabledForTest(true)
	g := &aeGen{afGen: &afGen{rng: rng, cg: &chunkGen{rng: rng}}, ctx: mock.NewContext()}
	complete, final, partial := int(aggregation.CompleteMode), int(aggregation.FinalMode), int(aggregation.Partial1Mode)

	// ---- Part A: stream_agg (exact existing sequence so stream_agg vectors stay byte-identical)

	// Empty input: the DefaultVal row of a final (or complete) aggregation without GROUP BY,
	// none with GROUP BY, with only FIRST_ROW or in a partial mode.
	for _, mode := range []int{complete, final, partial} {
		for _, name := range afPartANames {
			if mode == partial && name == ast.AggFuncAvg {
				continue
			}
			for keys := range 2 {
				vg.Add("empty input", "stream_agg", g.record(aeShape{keys: keys, mode: mode, names: []string{name}, rows: 0}))
			}
		}
	}
	vg.Add("no function, empty input", "stream_agg", g.record(aeShape{keys: 0, mode: complete, names: []string{}, rows: 0}))
	vg.Add("all FIRST_ROW, empty input", "stream_agg", g.record(aeShape{keys: 0, mode: complete,
		names: []string{ast.AggFuncFirstRow, ast.AggFuncFirstRow}, rows: 0}))

	// Every group key type on its own, both evaluation paths (drawn).
	for _, kt := range aeKeyTypes {
		for range 6 {
			cfg := g.record(aeShape{keys: 0, mode: complete, rows: -1})
			// Prepend a key column of type kt.
			cfg = aeWithKey(g, cfg, kt)
			vg.Add("key type", "stream_agg", cfg)
		}
	}

	// No aggregate function (virtual rows), with and without GROUP BY.
	for range 30 {
		vg.Add("no function", "stream_agg", g.record(aeShape{keys: g.intn(3), mode: complete, names: []string{}, rows: -1}))
	}

	// Drawn records: keys, modes, functions, sizes.
	for range 1300 {
		mode := complete
		switch g.intn(10) {
		case 0, 1, 2:
			mode = final
		case 3:
			mode = partial
		}
		var names []string
		if mode == partial {
			for {
				n := afPartANames[g.intn(len(afPartANames))]
				if n != ast.AggFuncAvg {
					names = []string{n}
					break
				}
			}
		}
		vg.Add("drawn", "stream_agg", g.record(aeShape{keys: []int{0, 1, 1, 1, 2, 2}[g.intn(6)], mode: mode, names: names, rows: -1}))
	}

	// ---- Part B1: group_key

	tzOffsets := []int{0, 8 * 3600, -5 * 3600}
	for _, kt := range aeKeyTypes {
		for _, vecOn := range []bool{false, true} {
			for range 3 {
				nrows := []int{0, 1, 5, 16}[g.intn(4)]
				tz := tzOffsets[g.intn(len(tzOffsets))]
				vg.Add("single key", "group_key", g.groupKeyRecord([]afType{kt}, []int{0}, nrows, vecOn, tz))
			}
		}
	}
	// Multi-column group keys including ENUM, DECIMAL, collation strings, TIMESTAMP.
	for range 120 {
		nCols := 2 + g.intn(3)
		ts := make([]afType, nCols)
		gb := make([]int, 0, nCols)
		for c := range nCols {
			ts[c] = aeKeyTypes[g.intn(len(aeKeyTypes))]
			if c == 0 || g.intn(3) != 0 {
				gb = append(gb, c)
			}
		}
		nrows := []int{1, 4, 12, 32}[g.intn(4)]
		vg.Add("multi key", "group_key", g.groupKeyRecord(ts, gb, nrows, g.intn(2) == 0, tzOffsets[g.intn(len(tzOffsets))]))
	}
	// Explicit invalid ENUM (0, "") vs valid ENUM (1, "") case (EnumSetAsIntFlag).
	enumFt := afType{Tp: mysql.TypeEnum, Flen: -1, Decimal: 0, Charset: "utf8mb4", Collate: "utf8mb4_bin", Elems: []string{"", "a"}}
	vg.Add("enum zero vs empty", "group_key", &aeGroupKeyGenCfg{
		Types:    []afType{enumFt},
		Rows:     [][]any{{[]any{"", uint64(0)}}, {[]any{"", uint64(1)}}, {[]any{hex.EncodeToString([]byte("a")), uint64(2)}}, {nil}},
		GroupBy:  []int{0},
		Vec:      true,
		TzOffset: 0,
	})
	vg.Add("enum zero vs empty non-vec", "group_key", &aeGroupKeyGenCfg{
		Types:    []afType{enumFt},
		Rows:     [][]any{{[]any{"", uint64(0)}}, {[]any{"", uint64(1)}}, {[]any{hex.EncodeToString([]byte("a")), uint64(2)}}, {nil}},
		GroupBy:  []int{0},
		Vec:      false,
		TzOffset: 0,
	})

	// ---- Part B1: hash_agg_unparallel

	for _, mode := range []int{complete, final, partial} {
		for _, name := range afPartANames {
			if mode == partial && name == ast.AggFuncAvg {
				continue
			}
			for keys := range 2 {
				vg.Add("empty input", "hash_agg_unparallel", g.record(aeShape{keys: keys, mode: mode, names: []string{name}, rows: 0}))
			}
		}
	}
	vg.Add("no function, empty input", "hash_agg_unparallel", g.record(aeShape{keys: 0, mode: complete, names: []string{}, rows: 0}))
	for range 20 {
		vg.Add("no function", "hash_agg_unparallel", g.record(aeShape{keys: g.intn(3), mode: complete, names: []string{}, rows: -1}))
	}
	for range 340 {
		mode := complete
		switch g.intn(10) {
		case 0, 1, 2:
			mode = final
		case 3:
			mode = partial
		}
		var names []string
		if mode == partial {
			for {
				n := afPartANames[g.intn(len(afPartANames))]
				if n != ast.AggFuncAvg {
					names = []string{n}
					break
				}
			}
		}
		vg.Add("drawn", "hash_agg_unparallel", g.record(aeShape{keys: []int{0, 1, 1, 1, 2, 2}[g.intn(6)], mode: mode, names: names, rows: -1}))
	}

	// ---- Part B2: hash_agg_parallel

	conPairs := [][2]int{{1, 2}, {2, 1}, {2, 2}, {2, 4}, {4, 2}, {3, 3}, {4, 5}, {5, 5}, {8, 4}, {4, 8}}
	// Empty input DefaultVal coverage.
	for _, mode := range []int{complete, final, partial} {
		for _, name := range []string{ast.AggFuncCount, ast.AggFuncSum, ast.AggFuncMin, ast.AggFuncMax} {
			for keys := range 2 {
				cp := conPairs[g.intn(len(conPairs))]
				vg.Add("empty input", "hash_agg_parallel", g.parallelRecord(aeShape{keys: keys, mode: mode, names: []string{name}, rows: 0}, cp[0], cp[1], false))
			}
		}
	}
	// Multiple AVG columns (verifies builder ordinal [p, p+2], spec §7 item 3).
	for range 30 {
		cp := conPairs[g.intn(len(conPairs))]
		vg.Add("multi avg", "hash_agg_parallel", g.parallelRecord(
			aeShape{keys: g.intn(2), mode: complete, names: []string{ast.AggFuncAvg, ast.AggFuncAvg, ast.AggFuncSum, ast.AggFuncCount}, rows: -1},
			cp[0], cp[1], false,
		))
	}
	// No aggregate function (virtual rows).
	for range 20 {
		cp := conPairs[g.intn(len(conPairs))]
		vg.Add("no function", "hash_agg_parallel", g.parallelRecord(
			aeShape{keys: g.intn(3), mode: complete, names: []string{}, rows: -1},
			cp[0], cp[1], false,
		))
	}
	// Drawn parallel records across concurrency pairs, key counts, agg functions, and child errors.
	for range 340 {
		cp := conPairs[g.intn(len(conPairs))]
		vg.Add("drawn", "hash_agg_parallel", g.parallelRecord(
			aeShape{keys: []int{0, 1, 1, 1, 2}[g.intn(5)], mode: complete, names: nil, rows: -1},
			cp[0], cp[1], true,
		))
	}

	// ---- Part B2: runtime_stats

	for i := range 100 {
		vg.Add(fmt.Sprintf("stats-%d", i), "runtime_stats", &aeStatsCfg{
			Base:  g.statsInput(),
			Other: g.statsInput(),
		})
	}
}

// aeWithKey prepends a group key column of type kt to cfg (shifting the other columns) and
// groups on it, with values in runs.
func aeWithKey(g *aeGen, cfg *aeGenCfg, kt afType) *aeGenCfg {
	cfg.Types = append([]afType{kt}, cfg.Types...)
	for i := range cfg.Aggs {
		for j := range cfg.Aggs[i].Args {
			cfg.Aggs[i].Args[j]++
		}
	}
	cfg.GroupBy = []int{0}
	g.afGen.pool = map[int][]any{}
	var key any
	first := true
	for _, b := range cfg.Batches {
		for r := range b {
			if first || g.intn(3) == 0 {
				key = g.value(-1, kt)
				first = false
			}
			b[r] = append([]any{key}, b[r]...)
		}
	}
	return cfg
}
