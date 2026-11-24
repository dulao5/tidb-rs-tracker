package domains

// Domain "sortexec": block D12.5, the in-memory sort and top-n executors (tidb-rs VS1-36):
// sortexec.SortExec in unparallel mode (Open, then IsUnparallel = true and
// InitUnparallelModeForTest, as sort_spill_test.go runs it) and sortexec.TopNExec without
// spill, each over a source returning fixed chunks.
//
// Two ops, "sort" and "topn". A record is:
//
//	[types, batches, by_items, [init_chunk_size, max_chunk_size], reqs]          (sort)
//	[types, batches, by_items, [init_chunk_size, max_chunk_size], reqs, [offset, count]]  (topn)
//
//   - types: the source columns, chunk domain type specs ([tp, flag, collate, flen]);
//   - batches: the chunks the source returns, one per Next, each a list of rows of
//     values in the chunk domain's encoding (chunkAppend; null is NULL);
//   - by_items: {"col": i, "desc": b} (column i of the source), {"const": spec, "desc": b}
//     (a constant of that type: skipped by buildKeyColumns but counted by
//     initCompareFuncs, so a leading constant shifts the compare functions and Desc flags
//     onto the next columns, as Go does), or {"corr": i} (a correlated column: "Get
//     unexpected expression");
//   - reqs: the required rows set on the result chunk before each Next (0 or missing:
//     unchanged);
//   - offset, count: the top-n's PhysicalLimit (decimal strings).
//
// Out: {"chunks": [[row...]...], "step": ""|"open"|"next"|"close"}, each row a list of
// cells {"null": b, "raw": hex of Row.GetRaw}; the error, if any, is the record's error.
// New collations are enabled (the cluster default).
//
// The generator draws values from a small per-column pool half of the time, so sort keys
// tie often (pdqsort's order of equal rows is part of the result), and covers several sort
// keys, mixed ASC / DESC, NULLs, every chunk type (strings in binary, general_ci and
// unicode_ci, decimals, times, durations, JSON, vectors, enums, sets, bits), several
// batches and required rows, count 0, offsets past the end and counts past the end.

import (
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/executor/sortexec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const sortexecSeed = 20261124

func init() {
	vec.Register(&vec.Domain{
		Name:     "sortexec",
		Seed:     sortexecSeed,
		Coverage: sortexecCoverage,
		Blocks:   []string{"D12.5"},
		Exempt:   sortexecExempt(),
		Gzip:     true,
		Generate: sortexecGenerate,
		Exec:     sortexecExec,
	})
}

var sortexecCommon = []string{"SortExec.initCompareFuncs", "SortExec.buildKeyColumns", "SortExec.Close"}

var sortexecCoverage = map[string]vec.Coverage{
	"sort": {Min: 400, GoFuncs: append([]string{"SortExec.Open", "SortExec.Next", "SortExec.InitUnparallelModeForTest",
		"SortExec.appendResultToChunkInUnparallelMode", "SortExec.checkError", "SortExec.closeChannels",
		"SortExec.fetchChunks", "SortExec.fetchChunksUnparallel", "SortExec.handleCurrentPartitionBeforeExit",
		"SortExec.onePartitionSorting", "SortExec.storeChunk", "SortExec.switchToNewSortPartition",
		"newSortPartition", "sortPartition.add", "sortPartition.checkError", "sortPartition.close",
		"sortPartition.getNextSortedRow", "sortPartition.isSpillTriggered",
		"sortPartition.keyColumnsLess", "sortPartition.lessRow", "sortPartition.sort", "sortPartition.sortNoLock",
		"NewDataCursor"}, sortexecCommon...)},
	"topn": {Min: 400, GoFuncs: append([]string{"TopNExec.Open", "TopNExec.Next", "TopNExec.Close",
		"TopNExec.executeTopN", "TopNExec.executeTopNWhenNoSpillTriggered", "TopNExec.fetchChunks",
		"TopNExec.generateTopNResults", "TopNExec.generateTopNResultsWhenNoSpillTriggered", "TopNExec.greaterRow",
		"TopNExec.initBeforeLoadingChunks", "TopNExec.loadChunksUntilTotalLimit",
		"topNChunkHeap.Len", "topNChunkHeap.Less", "topNChunkHeap.Swap",
		"topNChunkHeap.doCompaction", "topNChunkHeap.init", "topNChunkHeap.initPtrs", "topNChunkHeap.initPtrsImpl",
		"topNChunkHeap.keyColumnsCompare", "topNChunkHeap.processChk", "topNChunkHeap.update",
		"newTopNSpillerHelper", "topNSpillHelper.close", "topNSpillHelper.isSpillNeeded",
		"topNSpillHelper.isSpillTriggered", "newTopNWorker"}, sortexecCommon...)},
}

// sortexecBy is one by-item of a record.
type sortexecBy struct {
	Col   *int           `json:"col,omitempty"`
	Const *chunkTypeSpec `json:"const,omitempty"`
	Corr  *int           `json:"corr,omitempty"`
	Desc  bool           `json:"desc,omitempty"`
}

func sortexecSchema(fts []*types.FieldType) *expression.Schema {
	cols := make([]*expression.Column, len(fts))
	for i, ft := range fts {
		cols[i] = &expression.Column{RetType: ft, Index: i, UniqueID: int64(i + 1)}
	}
	return expression.NewSchema(cols...)
}

func sortexecExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	specs := chunkSpecs(args[0])
	fts := chunkFts(specs)
	schema := sortexecSchema(fts)
	var rawBatches [][][]json.RawMessage
	must(json.Unmarshal(args[1], &rawBatches))
	var bys []sortexecBy
	must(json.Unmarshal(args[2], &bys))
	sizes := chunkInts(args[3])
	reqs := chunkInts(args[4])
	topN := op == "topn"
	var offset, count uint64
	if topN {
		lim := chunkArr(args[5])
		offset, count = chunkU64(lim[0]), chunkU64(lim[1])
	}

	sctx := mock.NewContext()
	sctx.GetSessionVars().InitChunkSize = sizes[0]
	sctx.GetSessionVars().MaxChunkSize = sizes[1]

	batches := make([]*chunk.Chunk, len(rawBatches))
	for i, rows := range rawBatches {
		c := chunk.NewChunkWithCapacity(fts, len(rows))
		for _, row := range rows {
			for col, v := range row {
				chunkAppend(c, fts[col], col, v, false)
			}
		}
		batches[i] = c
	}
	byItems := make([]*plannerutil.ByItems, len(bys))
	for i, b := range bys {
		var e expression.Expression
		switch {
		case b.Col != nil:
			e = schema.Columns[*b.Col]
		case b.Const != nil:
			e = &expression.Constant{Value: types.NewIntDatum(1), RetType: b.Const.ft()}
		case b.Corr != nil:
			e = &expression.CorrelatedColumn{Column: *schema.Columns[*b.Corr]}
		}
		byItems[i] = &plannerutil.ByItems{Expr: e, Desc: b.Desc}
	}

	chunks, step, err := sortexec.GovecSortRun(sctx, schema, batches, byItems, topN, offset, count, reqs)
	outChunks := make([]any, len(chunks))
	for i, c := range chunks {
		rows := make([]any, c.NumRows())
		for r := range c.NumRows() {
			row := c.GetRow(r)
			cells := make([]any, c.NumCols())
			for col := range cells {
				cells[col] = map[string]any{"null": row.IsNull(col), "raw": hex.EncodeToString(row.GetRaw(col))}
			}
			rows[r] = cells
		}
		outChunks[i] = rows
	}
	return map[string]any{"chunks": outChunks, "step": step}, err
}

// sortexecTypes are the column types of the records: the chunk domain's types (every type
// GetCompareFunc supports).
var sortexecTypes = chunkTypes

type sortexecGen struct {
	cg   *chunkGen
	rng  *rand.Rand
	pool map[int][]any
}

func (g *sortexecGen) intn(n int) int { return g.cg.intn(n) }

// value draws a value for column col of type ft: from the column's pool of 4 values half
// of the time (ties), otherwise a fresh one.
func (g *sortexecGen) value(col int, ft *types.FieldType) any {
	if g.intn(2) == 0 {
		p := g.pool[col]
		if p == nil {
			p = make([]any, 4)
			for i := range p {
				p[i] = g.cg.value(ft)
			}
			g.pool[col] = p
		}
		return p[g.intn(len(p))]
	}
	return g.cg.value(ft)
}

// record builds the args of one record (without the limit).
func (g *sortexecGen) record(specs []chunkTypeSpec, nrows int, bys []sortexecBy) []any {
	g.pool = map[int][]any{}
	fts := chunkFts(specs)
	init := []int{1, 2, 4, 32}[g.intn(4)]
	maxSize := []int{init, 4, 7, 32, 1024}[g.intn(5)]
	if maxSize < init {
		maxSize = init
	}
	var batches [][][]any
	for left := nrows; left > 0; {
		n := 1 + g.intn(maxSize)
		if n > left {
			n = left
		}
		left -= n
		rows := make([][]any, n)
		for r := range rows {
			row := make([]any, len(specs))
			for c := range row {
				row[c] = g.value(c, fts[c])
			}
			rows[r] = row
		}
		batches = append(batches, rows)
	}
	if batches == nil {
		batches = [][][]any{}
	}
	var reqs []int
	if g.intn(3) == 0 {
		for range 1 + g.intn(6) {
			reqs = append(reqs, g.intn(maxSize+1))
		}
	}
	if reqs == nil {
		reqs = []int{}
	}
	return []any{specs, batches, bys, []int{init, maxSize}, reqs}
}

func sortexecIntp(i int) *int { return &i }

// randomBys: 1 to 3 column keys (repeats allowed), mixed ASC / DESC, sometimes a trailing
// constant.
func (g *sortexecGen) randomBys(ncols int) []sortexecBy {
	var bys []sortexecBy
	for range 1 + g.intn(3) {
		bys = append(bys, sortexecBy{Col: sortexecIntp(g.intn(ncols)), Desc: g.intn(2) == 0})
	}
	if g.intn(8) == 0 {
		s := sortexecTypes[g.intn(len(sortexecTypes))]
		bys = append(bys, sortexecBy{Const: &s, Desc: g.intn(2) == 0})
	}
	return bys
}

func (g *sortexecGen) randomSpecs() []chunkTypeSpec {
	n := 1 + g.intn(4)
	specs := make([]chunkTypeSpec, n)
	for i := range specs {
		specs[i] = sortexecTypes[g.intn(len(sortexecTypes))]
	}
	return specs
}

func (g *sortexecGen) rows() int {
	return []int{0, 1, 2, 3, 5, 10, 33, 100, 300}[g.intn(9)]
}

// limit: offsets and counts around the row count (0, inside, at and past the end).
func (g *sortexecGen) limit(nrows int) []string {
	offset := []int{0, 0, 1, nrows / 10, nrows / 2, nrows, nrows + 3, g.intn(nrows + 1)}[g.intn(8)]
	count := []int{0, 1, 2, 3, nrows / 3, nrows, nrows + 5, 1 + g.intn(nrows+1)}[g.intn(8)]
	return []string{strconv.Itoa(offset), strconv.Itoa(count)}
}

func sortexecGenerate(vg *vec.Gen, rng *rand.Rand) {
	g := &sortexecGen{cg: &chunkGen{rng: rng}, rng: rng}
	add := func(src string, topN bool, specs []chunkTypeSpec, bys []sortexecBy) {
		nrows := g.rows()
		a := g.record(specs, nrows, bys)
		if topN {
			vg.Add(src, "topn", append(a, g.limit(nrows))...)
		} else {
			vg.Add(src, "sort", a...)
		}
	}
	for _, topN := range []bool{false, true} {
		// Random schemas and keys.
		for range 500 {
			specs := g.randomSpecs()
			add("gen random", topN, specs, g.randomBys(len(specs)))
		}
		// Each type as the only key, ASC and DESC, beside a payload column.
		for _, s := range sortexecTypes {
			for _, desc := range []bool{false, true} {
				specs := []chunkTypeSpec{s, {mysql.TypeLonglong, 0, "binary", 20}}
				add("gen type", topN, specs, []sortexecBy{{Col: sortexecIntp(0), Desc: desc}})
			}
		}
		// A leading constant of the key columns' type: Go's compare functions and Desc
		// flags stay indexed by by-item, so they shift onto the column keys.
		for range 30 {
			s := sortexecTypes[g.intn(len(sortexecTypes))]
			specs := []chunkTypeSpec{s, s, s}
			bys := []sortexecBy{{Const: &s, Desc: g.intn(2) == 0}}
			for range 1 + g.intn(3) {
				bys = append(bys, sortexecBy{Col: sortexecIntp(g.intn(3)), Desc: g.intn(2) == 0})
			}
			add("gen leading constant", topN, specs, bys)
		}
		// Errors: a correlated column key, an unsupported key type.
		for range 5 {
			specs := g.randomSpecs()
			bys := g.randomBys(len(specs))
			bys = append(bys, sortexecBy{Corr: sortexecIntp(0)})
			add("gen correlated column", topN, specs, bys)
			geo := chunkTypeSpec{mysql.TypeGeometry, 0, "binary", 0}
			add("gen geometry", topN, specs, append(g.randomBys(len(specs)), sortexecBy{Const: &geo}))
		}
	}
}
