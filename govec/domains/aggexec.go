package domains

// Domain "aggexec": block D12.3 part A, the stream aggregation executor
// (aggregate.StreamAggExec) and the vectorized group checker it uses
// (pkg/executor/internal/vecgroupchecker, block D12.0), built by the executor builder
// (buildStreamAgg, buildStreamAggFromChildExec) from a PhysicalStreamAgg over a source of
// fixed chunks, and run Open-Next-Close (tidb-rs specs/D12.3-agg-executors §5.2).
//
// As in the basicexec domain, the child is the table dual executor the builder built for
// the plan's child, with Open, Next and Close replaced: it returns one batch per Next
// (whole, like govecSource in the sortexec shim), then empty chunks, and with err_at = k
// fails its k-th Next ("mock child error").
//
// One op, "stream_agg": args [cfg], cfg =
//
//	{"types": [type...], "batches": [[row...]...], "group_by": [col...],
//	 "aggs": [{"name", "mode": 0..4, "distinct": b, "args": [col...], "ret": type}...],
//	 "out": [type...], "sizes": [init_chunk_size, max_chunk_size], "reqs": [n...],
//	 "vec": tidb_enable_vectorized_expression, "err_at": k (-1: never)}
//
//   - type: the aggfuncs domain's [tp, flag, flen, decimal, charset, collate, elems];
//   - types / batches: the source's columns and the chunks it returns, rows of values in the
//     chunk domain's encoding (chunkAppend);
//   - group_by: the plan's GroupByItems, columns of the source;
//   - aggs: the plan's AggFuncs, complete descriptors (as in the aggfuncs domain) whose
//     arguments are columns of the source: tidb-rs builds them without the type inference
//     of D08.11. The generator draws them with NewAggFuncDesc and WrapCastForAggArgs as the
//     planner does (each cast replaced by a source column of its type); the final-mode ones
//     are the final half of Split over source columns holding partial results;
//   - out: the plan's schema (one column per function, its return type);
//   - reqs: the required rows set on the result chunk before each Next (0 or missing:
//     unchanged).
//
// Out: {"chunks": [[row...]...], "step": ""|"open"|"next"|"close"}, each row a list of cells
// {"null": b, "raw": hex of Row.GetRaw} (an empty list per virtual row when there is no
// aggregate function); the error, if any, is the record's error. New collations are enabled
// (the cluster default).
//
// The generator draws the group keys in runs (so that groups span batches), from every
// evaluation type of the group checker, with NULLs and ties; zero to three aggregate
// functions of part A of D12.4 (COUNT(DISTINCT) excluded: tidb-rs builds it in a later part
// of D12.4); the complete, final and (with one function) partial-1 modes; empty inputs (the
// DefaultVal row of a final aggregation without GROUP BY); small chunk sizes and required
// rows; both evaluation paths of the group checker; and a child error in the middle.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/executor"
	"github.com/pingcap/tidb/pkg/executor/aggfuncs"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/aggregation"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannercore "github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
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

func aggexecExec(_ string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	var cfg aeCfg
	mustUnmarshal(args[0], &cfg)

	ctx := mock.NewContext()
	vars := ctx.GetSessionVars()
	vars.InitChunkSize = cfg.Sizes[0]
	vars.MaxChunkSize = cfg.Sizes[1]
	vars.EnableVectorizedExpression = cfg.Vec
	vars.StmtCtx.MemTracker = memory.NewTracker(-1, -1)

	fts := aeFts(cfg.Types)
	child := sortexecSchema(fts)
	descs := make([]*aggregation.AggFuncDesc, len(cfg.Aggs))
	for i, a := range cfg.Aggs {
		d := &aggregation.AggFuncDesc{Mode: aggregation.AggFunctionMode(a.Mode), HasDistinct: a.Distinct}
		d.Name = a.Name
		for _, c := range a.Args {
			d.Args = append(d.Args, child.Columns[c])
		}
		d.RetTp = a.Ret.ft()
		descs[i] = d
	}
	groupBy := make([]expression.Expression, len(cfg.GroupBy))
	for i, c := range cfg.GroupBy {
		groupBy[i] = child.Columns[c]
	}

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
	batches := make([]*chunk.Chunk, len(cfg.Batches))
	for i, rows := range cfg.Batches {
		c := chunk.NewChunkWithCapacity(fts, len(rows))
		for _, row := range rows {
			for col, v := range row {
				chunkAppend(c, fts[col], col, v, false)
			}
		}
		batches[i] = c
	}
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

func aggexecGenerate(vg *vec.Gen, rng *rand.Rand) {
	collate.SetNewCollationEnabledForTest(true)
	g := &aeGen{afGen: &afGen{rng: rng, cg: &chunkGen{rng: rng}}, ctx: mock.NewContext()}
	complete, final, partial := int(aggregation.CompleteMode), int(aggregation.FinalMode), int(aggregation.Partial1Mode)

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
