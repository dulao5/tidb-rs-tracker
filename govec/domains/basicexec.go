package domains

// Domain "basicexec": the basic executors of pkg/executor/select.go (block D12.2, tidb-rs
// VS1-35): the limit executor, the selection executor and the table dual executor, built
// by the executor builder from physical plans and run Open-Next-Close as Go's
// executor_required_rows_test.go runs them.
//
// The child of the limit and the selection is a data source that returns, on each Next,
// min(required rows of the chunk, rows left) rows (Go's requiredRowsDataSource). It is the
// table dual executor the builder built for the plan's child, with Open, Next and Close
// replaced (the executor interface is internal to pkg/executor, so the data source embeds
// an executor of the package; only the replaced methods run).
//
// Ops:
//   - limit: args [cfg], cfg = {max, total, offset, count, cols, required}. The source has
//     two BIGINT columns, a = i and b = -i for row i; the limit's schema is the source
//     columns `cols` (a subset: the limit's inline projection when it is not all of them).
//     Each Next of the limit runs on a chunk whose required rows are required[k]
//     (chunk.SetRequiredRows(required[k], max)).
//   - selection: args [cfg], cfg = {max, total, vals, filters, required}. The source has
//     two BIGINT columns, a = i and b = vals[i % len(vals)] (null for a JSON null); the
//     filters compare b with a constant ({fn, val}, fn one of eq ne lt le gt ge nulleq
//     isnull) and are ANDed (the plan's Conditions).
//   - adjust_required_rows: args [begin, end, cursor, required, max]: a limit with
//     Offset = begin and Count = end - begin (so end = Offset + Count, wrapping), its
//     cursor set to `cursor`, adjusts a chunk whose required rows are `required`; out is
//     the chunk's required rows after LimitExec.adjustRequiredRows.
//   - table_dual: args [row_count, ncols, max, nexts]: the table dual executor of a plan
//     with that row count and ncols BIGINT columns, opened and run `nexts` times; a row
//     count other than 0 and 1 is the builder's error.
//
// `max` is the session's MaxChunkSize (InitChunkSize stays at its default, 32): the
// executors read it when they are built. Out of limit / selection: `next` is, for each
// Next, the values of the first output column (null for NULL), and `ds` the number of
// rows the source returned on each of its Next calls (Go's expectedRowsDS).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/domain"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannercore "github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/property"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/disk"
	"github.com/pingcap/tidb/pkg/util/memory"
	"github.com/pingcap/tidb/pkg/util/mock"

	_ "unsafe" // go:linkname
)

const basicexecSeed = 20261124

func init() {
	vec.Register(&vec.Domain{
		Name:     "basicexec",
		Seed:     basicexecSeed,
		Coverage: basicexecCoverage,
		Blocks:   []string{"D12.2"},
		Exempt:   basicexecExempt(),
		Gzip:     true,
		Generate: basicexecGenerate,
		Exec:     basicexecExec,
	})
}

var basicexecCoverage = map[string]vec.Coverage{
	"limit": {Min: 300, GoFuncs: []string{"executorBuilder.buildLimit", "LimitExec.Open", "LimitExec.Next",
		"LimitExec.Close"}},
	"selection": {Min: 300, GoFuncs: []string{"executorBuilder.buildSelection", "newSelectionExecutorContext",
		"SelectionExec.Open", "SelectionExec.open", "SelectionExec.Next", "SelectionExec.Close"}},
	"adjust_required_rows": {Min: 300, GoFuncs: []string{"LimitExec.adjustRequiredRows"}},
	"table_dual":           {Min: 10, GoFuncs: []string{"executorBuilder.buildTableDual", "TableDualExec.Open", "TableDualExec.Next"}},
}

// basicexecExempt: the functions of tidb-rs VS1-35 no op covers.
func basicexecExempt() map[string]string {
	proj := "concurrency and panic recovery, no input/output table: Go's projection tests are ported as " +
		"tidb-rs unit tests (D12.2 spec §6)"
	out := map[string]string{}
	for _, f := range []string{
		"executorBuilder.buildProjection", "ProjectionExec.Close", "ProjectionExec.Next", "ProjectionExec.Open",
		"ProjectionExec.drainInputCh", "ProjectionExec.drainOutputCh", "ProjectionExec.isUnparallelExec",
		"ProjectionExec.open", "ProjectionExec.parallelExecute", "ProjectionExec.prepare",
		"ProjectionExec.unParallelExecute", "newProjectionExecutorContext", "projectionInputFetcher.run",
		"projectionWorker.run", "readProjection", "recoveryProjection",
	} {
		out[f] = proj
	}
	out["SelectionExec.unBatchedNext"] = "runs only for filters that are not vectorizable (a user variable " +
		"assignment, D08.9 in tidb-rs): a tidb-rs unit test (D12.2 spec §6)"
	out["checkMaxExecutionTimeExceeded"] = "depends on the wall clock: a tidb-rs unit test (D12.2 spec §6)"
	out["globalPanicOnExceed.Action"] = "the server memory limit action (installed by select.go:init): " +
		"a tidb-rs unit test (D12.2 spec §6)"
	out["globalPanicOnExceed.GetPriority"] = "a constant: a tidb-rs unit test (D12.2 spec §6)"
	out["setOptionForTopSQL"] = "sets Top SQL options on a kv snapshot, nothing an executor returns " +
		"(Top SQL is D14.2 in tidb-rs)"
	return out
}

// beAdjust is LimitExec.adjustRequiredRows (unexported).
//
//go:linkname beAdjust github.com/pingcap/tidb/pkg/executor.(*LimitExec).adjustRequiredRows
func beAdjust(e *executor.LimitExec, chk *chunk.Chunk) *chunk.Chunk

// beCtx is Go's defaultCtx of executor_required_rows_test.go with MaxChunkSize = max.
func beCtx(max int) *mock.Context {
	ctx := mock.NewContext()
	vars := ctx.GetSessionVars()
	vars.InitChunkSize = variable.DefInitChunkSize
	vars.MaxChunkSize = max
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, vars.MemQuotaQuery)
	vars.StmtCtx.DiskTracker = disk.NewTracker(-1, -1)
	vars.SnapshotTS = uint64(1)
	ctx.BindDomain(domain.NewMockDomain())
	return ctx
}

// beSchema: n BIGINT columns (UniqueID i+1, Index i).
func beSchema(n int) *expression.Schema {
	cols := make([]*expression.Column, n)
	for i := range cols {
		cols[i] = &expression.Column{UniqueID: int64(i + 1), Index: i, RetType: types.NewFieldType(mysql.TypeLonglong)}
	}
	return expression.NewSchema(cols...)
}

var beStats = &property.StatsInfo{RowCount: 1}

func beDual(ctx *mock.Context, schema *expression.Schema, rowCount int) *plannercore.PhysicalTableDual {
	d := plannercore.PhysicalTableDual{RowCount: rowCount}.Init(ctx.GetPlanCtx(), beStats, 0)
	d.SetSchema(schema)
	return d
}

// beBuilderErr reads executorBuilder.err (unexported).
func beBuilderErr(b *executor.MockExecutorBuilder) error {
	f := reflect.ValueOf(b).Elem().Field(0).Elem().FieldByName("err")
	v := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	if v.IsNil() {
		return nil
	}
	return v.Interface().(error)
}

// beSource is Go's requiredRowsDataSource: a table dual executor whose Open, Next and
// Close are replaced.
type beSource struct {
	*executor.TableDualExec
	total, count int
	val          func(i, col int) *int64
	returned     []int
}

func (s *beSource) Open(context.Context) error { return nil }
func (s *beSource) Close() error               { return nil }

func (s *beSource) Next(_ context.Context, req *chunk.Chunk) error {
	req.Reset()
	n := 0
	if s.count <= s.total {
		n = min(req.RequiredRows(), s.total-s.count)
		for i := 0; i < n; i++ {
			for c := 0; c < req.NumCols(); c++ {
				if v := s.val(s.count+i, c); v != nil {
					req.AppendInt64(c, *v)
				} else {
					req.AppendNull(c)
				}
			}
		}
		s.count += n
	}
	s.returned = append(s.returned, n)
	return nil
}

// beRun replaces the child of e by a source and runs e: Open, a Next for each required
// rows, Close.
func beRun(e interface {
	Open(context.Context) error
	Next(context.Context, *chunk.Chunk) error
	Close() error
	NewChunk() *chunk.Chunk
}, src *beSource, required []int, max int) (any, error) {
	ctx := context.Background()
	if err := e.Open(ctx); err != nil {
		return nil, err
	}
	chk := e.NewChunk()
	next := [][]*int64{}
	for _, r := range required {
		chk.SetRequiredRows(r, max)
		if err := e.Next(ctx, chk); err != nil {
			return nil, err
		}
		vals := make([]*int64, chk.NumRows())
		for i := range vals {
			row := chk.GetRow(i)
			if !row.IsNull(0) {
				v := row.GetInt64(0)
				vals[i] = &v
			}
		}
		next = append(next, vals)
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	return struct {
		Next [][]*int64 `json:"next"`
		DS   []int      `json:"ds"`
	}{next, src.returned}, nil
}

type beLimitCfg struct {
	Max      int    `json:"max"`
	Total    int    `json:"total"`
	Offset   uint64 `json:"offset"`
	Count    uint64 `json:"count"`
	Cols     []int  `json:"cols"`
	Required []int  `json:"required"`
}

type beFilter struct {
	Fn  string `json:"fn"`
	Val int64  `json:"val"`
}

type beSelCfg struct {
	Max      int        `json:"max"`
	Total    int        `json:"total"`
	Vals     []*int64   `json:"vals"`
	Filters  []beFilter `json:"filters"`
	Required []int      `json:"required"`
}

var beFuncs = map[string]string{"eq": ast.EQ, "ne": ast.NE, "lt": ast.LT, "le": ast.LE, "gt": ast.GT,
	"ge": ast.GE, "nulleq": ast.NullEQ, "isnull": ast.IsNull}

func basicexecExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "limit":
		var cfg beLimitCfg
		mustUnmarshal(args[0], &cfg)
		ctx := beCtx(cfg.Max)
		child := beSchema(2)
		cols := make([]*expression.Column, len(cfg.Cols))
		for i, c := range cfg.Cols {
			cols[i] = child.Columns[c]
		}
		p := plannercore.PhysicalLimit{Offset: cfg.Offset, Count: cfg.Count}.Init(ctx.GetPlanCtx(), beStats, 0)
		p.SetChildren(beDual(ctx, child, 0))
		p.SetSchema(expression.NewSchema(cols...))
		b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
		e := b.Build(p)
		if e == nil {
			return nil, beBuilderErr(b)
		}
		kids := e.AllChildren()
		src := &beSource{returned: []int{}, TableDualExec: kids[0].(*executor.TableDualExec), total: cfg.Total,
			val: func(i, c int) *int64 {
				v := int64(i)
				if c == 1 {
					v = -v
				}
				return &v
			}}
		kids[0] = src
		e.SetAllChildren(kids)
		return beRun(e, src, cfg.Required, cfg.Max)
	case "selection":
		var cfg beSelCfg
		mustUnmarshal(args[0], &cfg)
		ctx := beCtx(cfg.Max)
		child := beSchema(2)
		var conds []expression.Expression
		for _, f := range cfg.Filters {
			fn, ok := beFuncs[f.Fn]
			if !ok {
				return nil, errors.Errorf("unknown filter %s", f.Fn)
			}
			var cond expression.Expression
			var err error
			if fn == ast.IsNull {
				cond, err = expression.NewFunction(ctx.GetExprCtx(), fn, types.NewFieldType(mysql.TypeLonglong),
					child.Columns[1])
			} else {
				cond, err = expression.NewFunction(ctx.GetExprCtx(), fn, types.NewFieldType(mysql.TypeLonglong),
					child.Columns[1], &expression.Constant{Value: types.NewIntDatum(f.Val),
						RetType: types.NewFieldType(mysql.TypeLonglong)})
			}
			if err != nil {
				return nil, err
			}
			conds = append(conds, cond)
		}
		p := plannercore.PhysicalSelection{Conditions: conds}.Init(ctx.GetPlanCtx(), beStats, 0)
		p.SetChildren(beDual(ctx, child, 0))
		b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
		e := b.Build(p)
		if e == nil {
			return nil, beBuilderErr(b)
		}
		kids := e.AllChildren()
		src := &beSource{returned: []int{}, TableDualExec: kids[0].(*executor.TableDualExec), total: cfg.Total,
			val: func(i, c int) *int64 {
				if c == 0 {
					v := int64(i)
					return &v
				}
				return cfg.Vals[i%len(cfg.Vals)]
			}}
		kids[0] = src
		e.SetAllChildren(kids)
		// The selection's schema is its child's (Go's PhysicalSelection.Schema).
		return beRun(e, src, cfg.Required, cfg.Max)
	case "adjust_required_rows":
		var begin, end, cursor uint64
		var required, max int
		mustUnmarshal(args[0], &begin)
		mustUnmarshal(args[1], &end)
		mustUnmarshal(args[2], &cursor)
		mustUnmarshal(args[3], &required)
		mustUnmarshal(args[4], &max)
		ctx := beCtx(max)
		child := beSchema(1)
		p := plannercore.PhysicalLimit{Offset: begin, Count: end - begin}.Init(ctx.GetPlanCtx(), beStats, 0)
		p.SetChildren(beDual(ctx, child, 0))
		p.SetSchema(child)
		b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
		e := b.Build(p)
		if e == nil {
			return nil, beBuilderErr(b)
		}
		l := e.(*executor.LimitExec)
		f := reflect.ValueOf(l).Elem().FieldByName("cursor")
		reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetUint(cursor)
		chk := l.NewChunk()
		chk.SetRequiredRows(required, max)
		return beAdjust(l, chk).RequiredRows(), nil
	case "table_dual":
		var rowCount, ncols, max, nexts int
		mustUnmarshal(args[0], &rowCount)
		mustUnmarshal(args[1], &ncols)
		mustUnmarshal(args[2], &max)
		mustUnmarshal(args[3], &nexts)
		ctx := beCtx(max)
		b := executor.NewMockExecutorBuilderForTest(ctx, nil, nil)
		e := b.Build(beDual(ctx, beSchema(ncols), rowCount))
		if e == nil {
			return nil, beBuilderErr(b)
		}
		bg := context.Background()
		if err := e.Open(bg); err != nil {
			return nil, err
		}
		chk := e.NewChunk()
		type res struct {
			Rows  int  `json:"rows"`
			Cols  int  `json:"cols"`
			Nulls bool `json:"nulls"`
		}
		out := []res{}
		for k := 0; k < nexts; k++ {
			if err := e.Next(bg, chk); err != nil {
				return nil, err
			}
			nulls := true
			for i := 0; i < chk.NumRows(); i++ {
				for c := 0; c < chk.NumCols(); c++ {
					nulls = nulls && chk.GetRow(i).IsNull(c)
				}
			}
			out = append(out, res{chk.NumRows(), chk.NumCols(), nulls})
		}
		if err := e.Close(); err != nil {
			return nil, err
		}
		return out, nil
	}
	return nil, errors.Errorf("unknown op %s", op)
}

func beInt(v int64) *int64 { return &v }

func basicexecGenerate(g *vec.Gen, rng *rand.Rand) {
	const dm = variable.DefMaxChunkSize
	// from: pkg/executor/executor_required_rows_test.go TestLimitRequiredRows
	for _, c := range []struct {
		total, offset, count int
		required             []int
	}{
		{20, 0, 10, []int{3, 5, 1, 500, 500}},
		{20, 0, 25, []int{9, 500}},
		{100, 50, 30, []int{10, 5, 10, 20}},
		{100, 101, 10, []int{10}},
		{dm + 20, dm + 1, 10, []int{3, 3, 3, 100}},
	} {
		g.Add("TestLimitRequiredRows", "limit", beLimitCfg{Max: dm, Total: c.total, Offset: uint64(c.offset),
			Count: uint64(c.count), Cols: []int{0, 1}, Required: c.required})
	}
	// from: pkg/executor/executor_required_rows_test.go TestSelectionRequiredRows (the
	// generator gen01: 0, 1, 0, 1, ... in the filtered column; no filters for the first).
	zeroOne := []*int64{beInt(0), beInt(1)}
	for _, c := range []struct {
		total    int
		filters  []beFilter
		required []int
	}{
		{20, nil, []int{1, 2, 3, 4, 5, 20}},
		{20, []beFilter{{"eq", 0}}, []int{1, 3, 5, 7, 9}},
		{dm + 20, []beFilter{{"eq", 1}}, []int{1, 3, 5, dm}},
	} {
		g.Add("TestSelectionRequiredRows", "selection", beSelCfg{Max: dm, Total: c.total, Vals: zeroOne,
			Filters: c.filters, Required: c.required})
	}

	// Edge cases of the limit: an empty source, LIMIT 0, the largest count (offset + count
	// wraps to a negative int in adjustRequiredRows), the inline projection.
	for _, c := range []beLimitCfg{
		{Max: dm, Total: 0, Offset: 0, Count: 10, Cols: []int{0, 1}, Required: []int{5, 5}},
		{Max: dm, Total: 10, Offset: 0, Count: 0, Cols: []int{0, 1}, Required: []int{5}},
		{Max: dm, Total: 10, Offset: 3, Count: 0, Cols: []int{0, 1}, Required: []int{5}},
		{Max: dm, Total: 30, Offset: 0, Count: math.MaxUint64, Cols: []int{0, 1}, Required: []int{5, 100, 100}},
		{Max: dm, Total: 30, Offset: 7, Count: math.MaxUint64 - 7, Cols: []int{0, 1}, Required: []int{5, 100, 100}},
		{Max: dm, Total: 30, Offset: 4, Count: 10, Cols: []int{1}, Required: []int{3, 100, 100}},
		{Max: dm, Total: 30, Offset: 4, Count: 10, Cols: []int{1, 0}, Required: []int{3, 100, 100}},
		{Max: 1, Total: 5, Offset: 1, Count: 3, Cols: []int{0, 1}, Required: []int{1, 1, 1, 1}},
		{Max: 7, Total: 40, Offset: 10, Count: 20, Cols: []int{1}, Required: []int{0, 3, 7, 7, 7, 7}},
	} {
		g.Add("sys/limit", "limit", c)
	}
	// Edge cases of the selection: every filter function, NULLs, an empty source, no row
	// passes, every row passes, required rows above the chunk size.
	withNull := []*int64{beInt(3), nil, beInt(-1), beInt(3), beInt(0), nil, beInt(7)}
	for _, fn := range []string{"eq", "ne", "lt", "le", "gt", "ge", "nulleq", "isnull"} {
		g.Add("sys/selection/fn", "selection", beSelCfg{Max: dm, Total: 30, Vals: withNull,
			Filters: []beFilter{{fn, 3}}, Required: []int{4, 4, 100, 100}})
	}
	for _, c := range []beSelCfg{
		{Max: dm, Total: 0, Vals: zeroOne, Filters: []beFilter{{"eq", 0}}, Required: []int{5, 5}},
		{Max: dm, Total: 50, Vals: zeroOne, Filters: []beFilter{{"eq", 5}}, Required: []int{5, 5}},
		{Max: dm, Total: 50, Vals: zeroOne, Filters: []beFilter{{"ge", 0}}, Required: []int{5, 100, 100}},
		{Max: dm, Total: 50, Vals: withNull, Filters: []beFilter{{"ge", 0}, {"lt", 7}}, Required: []int{5, 100}},
		{Max: 4, Total: 50, Vals: withNull, Filters: []beFilter{{"ne", 0}}, Required: []int{2, 4, 10, 4, 4, 4, 4, 4}},
		{Max: 1, Total: 9, Vals: withNull, Filters: []beFilter{{"isnull", 0}}, Required: []int{1, 1, 1, 1}},
		{Max: dm, Total: 3 * dm, Vals: zeroOne, Filters: []beFilter{{"eq", 1}}, Required: []int{dm, dm, dm, dm}},
	} {
		g.Add("sys/selection", "selection", c)
	}
	// The table dual: 0 and 1 rows, with and without columns, and the builder's error.
	for _, rc := range []int{0, 1, 2, -1} {
		for _, n := range []int{0, 1, 3} {
			g.Add("sys/table_dual", "table_dual", rc, n, dm, 2)
		}
	}
	// adjustRequiredRows on the boundaries: cursor before, at and after begin; end at and
	// beyond the cursor; counts near the largest uint64; required rows out of range.
	big := []uint64{0, 1, 2, 5, 10, 100, 1023, 1024, 1025, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64 - 1,
		math.MaxUint64}
	for _, begin := range big {
		for _, end := range big {
			for _, cursor := range []uint64{0, 1, 5, 1024, math.MaxInt64, math.MaxUint64} {
				if cursor > end {
					continue
				}
				for _, req := range []int{-1, 0, 1, 3, 1024, 2000} {
					g.Add("sys/adjust", "adjust_required_rows", begin, end, cursor, req, dm)
				}
			}
		}
	}

	// Random runs.
	for i := 0; i < 300; i++ {
		max := []int{1, 2, 5, 32, dm}[rng.IntN(5)]
		total := rng.IntN(3*max + 10)
		offset := uint64(rng.IntN(total + 5))
		count := uint64(rng.IntN(total + 5))
		if rng.IntN(10) == 0 {
			count = math.MaxUint64 - offset
		}
		cols := [][]int{{0, 1}, {0}, {1}, {1, 0}}[rng.IntN(4)]
		required := make([]int, 1+rng.IntN(6))
		for k := range required {
			required[k] = rng.IntN(max+3) - 1
		}
		g.Add(fmt.Sprintf("rand/limit/%d", i), "limit", beLimitCfg{Max: max, Total: total, Offset: offset,
			Count: count, Cols: cols, Required: required})
	}
	fns := []string{"eq", "ne", "lt", "le", "gt", "ge", "nulleq", "isnull"}
	for i := 0; i < 300; i++ {
		max := []int{1, 2, 5, 32, dm}[rng.IntN(5)]
		total := rng.IntN(3*max + 10)
		vals := make([]*int64, 1+rng.IntN(8))
		for k := range vals {
			if rng.IntN(5) > 0 {
				vals[k] = beInt(int64(rng.IntN(7) - 3))
			}
		}
		filters := make([]beFilter, rng.IntN(3))
		for k := range filters {
			filters[k] = beFilter{fns[rng.IntN(len(fns))], int64(rng.IntN(7) - 3)}
		}
		required := make([]int, 1+rng.IntN(6))
		for k := range required {
			required[k] = rng.IntN(max+3) - 1
		}
		g.Add(fmt.Sprintf("rand/selection/%d", i), "selection", beSelCfg{Max: max, Total: total, Vals: vals,
			Filters: filters, Required: required})
	}
	for i := 0; i < 300; i++ {
		begin := uint64(rng.IntN(3000))
		end := begin + uint64(rng.IntN(3000))
		cursor := uint64(rng.IntN(int(end) + 1))
		max := []int{1, 32, dm}[rng.IntN(3)]
		g.Add(fmt.Sprintf("rand/adjust/%d", i), "adjust_required_rows", begin, end, cursor, rng.IntN(max+5)-2, max)
	}
}
