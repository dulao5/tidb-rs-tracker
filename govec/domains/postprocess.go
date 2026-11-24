package domains

// Domain "postprocess": the post-processing of a physical plan of the first vertical slice
// (tidb-rs VS1-31, specs/D10.9-postprocess): ResolveIndices (resolve_indices.go), the
// coprocessor DAG of a table reader (ToPB, plan_to_pb.go) and the access objects
// (access_object.go).
//
// Records:
//   - "tables": as domain "findbest", with ppDDLs (findbest's tables plus two tables without
//     secondary indexes, so that their plans stay in the slice: a clustered common handle
//     and the hidden row id).
//   - "plan": args [ddls, sql, opts] (opts as optdriver's) -> the query is planned as in
//     domain "findbest" (core.DoOptimize, which ends with ResolveIndices). Output
//     {"plan": node} or, before a plan exists, {"err", "code"}, {"unsupported": tp} or
//     {"panic": message}. A node is {"tp", "id", "children"} plus, by type:
//     TableReader {"virt": the resolved VirtualExpr of its schema columns that have one,
//     "tbl": the table plan, "pb": [ToPB(TiKV) of every node of TablePlans, in order], "flash":
//     ToPB(TiFlash) of the table plan, "acc": accessObject(sctx)}; TableScan {"cols": the Index
//     of every schema column, "acc": AccessObject()}; PointGet {"virt", "acc"}; Selection
//     {"conds"}; Projection {"exprs"}; Sort / NominalSort {"by"}; TopN {"by", "part",
//     "prefix"}; Limit {"cols", "part", "prefix"}; any other operator {"other": true}.
//     A resolved expression is the list of the Index of its column references in pre-order
//     (a correlated column is "corr"); "part" is the Index of the partition columns, "prefix"
//     the Index of PrefixCol or null. A ToPB result is the hex of the marshalled
//     tipb.Executor or {"err": message}. An access object is {"s": String(), "n":
//     NormalizedString(), "pb": hex of a tipb.ExplainOperator after SetIntoPB}.
//     Selection / Projection / TopN / Limit / Sort outside a table plan also get "pb":
//     their ToPB(TiKV) (the error paths: a sort that is not partial, a child that is a
//     reader), unless a PointGet is below them (PointGetPlan.ToPB returns nil, nil).
//     The schema columns of a TableReader are not dumped: Go shares them with the table
//     plan (TableScan.ResolveIndicesItself writes their Index), tidb-rs copies them (D10.9
//     spec §5).
//   - "missing": args [ddls, sql, opts, k] -> the query is planned as for "plan"; the k-th
//     node in pre-order (a reader, then its table plan, then its children) gets a column
//     reference no child has (UniqueID 1<<20, BIGINT): a condition (Selection), an
//     expression (Projection), a by-item (Sort, NominalSort, TopN) or a schema column
//     (Limit); then that node's ResolveIndices runs. Output {"tp", "err": message | null},
//     {"tp", "skip": true} (another operator) or {"none": true} (fewer than k+1 nodes), or
//     the early outputs of "plan".
//   - "access": args [kind, object] -> the access object built from the fields ("dyn": a
//     list of {"db", "table", "all", "parts", "err"}; "scan": {"db", "table", "idx": [{"name",
//     "cols", "clustered"}], "parts"}; "other": the string). Output as "acc" above.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/rule"
	"github.com/pingcap/tidb/pkg/planner/property"
	plannerutil "github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/pingcap/tipb/go-tipb"
)

const postprocessSeed = 20261124

var ppMu sync.Mutex

//go:linkname ppReaderAccessObject github.com/pingcap/tidb/pkg/planner/core.(*PhysicalTableReader).accessObject
func ppReaderAccessObject(p *core.PhysicalTableReader, sctx base.PlanContext) base.AccessObject

func init() {
	vec.Register(&vec.Domain{
		Name:     "postprocess",
		Seed:     postprocessSeed,
		Coverage: postprocessCoverage,
		Blocks:   []string{"D10.9"},
		Exempt:   postprocessExempt(),
		Gzip:     true,
		Generate: postprocessGenerate,
		Exec:     postprocessExec,
	})
}

var postprocessCoverage = map[string]vec.Coverage{
	"tables": {Min: 1},
	"plan": {Min: 600, GoFuncs: []string{
		// resolve_indices.go
		"PhysicalProjection.ResolveIndices", "PhysicalProjection.ResolveIndicesItself", "refine4NeighbourProj",
		"resolveIndicesForVirtualColumn", "PhysicalTableReader.ResolveIndices", "PhysicalSelection.ResolveIndices",
		"resolveIndicesForSort", "PhysicalSort.ResolveIndices", "PhysicalTopN.ResolveIndices",
		"PhysicalLimit.ResolveIndices", "PhysicalTableScan.ResolveIndices", "PhysicalTableScan.ResolveIndicesItself",
		"physicalSchemaProducer.ResolveIndices", "baseSchemaProducer.ResolveIndices",
		// plan_to_pb.go
		"PhysicalSelection.ToPB", "PhysicalProjection.ToPB", "PhysicalTopN.ToPB", "PhysicalLimit.ToPB",
		"PhysicalTableScan.ToPB", "PhysicalSort.ToPB",
		// access_object.go
		"PhysicalTableScan.AccessObject", "PointGetPlan.AccessObject", "PhysicalTableReader.accessObject",
		"getDynamicAccessPartition", "ScanAccessObject.String", "ScanAccessObject.NormalizedString",
		"ScanAccessObject.SetIntoPB", "IndexAccess.ToPB",
		// the reader's access object outside dynamic pruning, and for a table that is not
		// partitioned (the builder turns dynamic pruning off for a partitioned table without
		// global statistics, so getDynamicAccessPartition always returns nil here)
		"DynamicPartitionAccessObjects.String", "DynamicPartitionAccessObjects.NormalizedString",
		"DynamicPartitionAccessObjects.SetIntoPB",
		// explain.go: the executor id of a table scan
		"PhysicalTableScan.ExplainID",
	}},
	"missing": {Min: 100, GoFuncs: []string{
		"PhysicalSelection.ResolveIndices", "PhysicalProjection.ResolveIndices", "PhysicalSort.ResolveIndices",
		"PhysicalTopN.ResolveIndices", "PhysicalLimit.ResolveIndices", "resolveIndicesForSort",
	}},
	"access": {Min: 30, GoFuncs: []string{
		"DynamicPartitionAccessObject.String", "DynamicPartitionAccessObjects.String",
		"DynamicPartitionAccessObjects.NormalizedString", "DynamicPartitionAccessObjects.SetIntoPB",
		"IndexAccess.ToPB", "ScanAccessObject.String", "ScanAccessObject.NormalizedString",
		"ScanAccessObject.SetIntoPB", "OtherAccessObject.String", "OtherAccessObject.NormalizedString",
		"OtherAccessObject.SetIntoPB",
	}},
}

// ---------------------------------------------------------------- dumps

// ppIdx: the Index of the column references of a resolved expression, in pre-order.
func ppIdx(e expression.Expression) []any {
	out := []any{}
	var walk func(e expression.Expression)
	walk = func(e expression.Expression) {
		switch x := e.(type) {
		case *expression.Column:
			out = append(out, x.Index)
		case *expression.CorrelatedColumn:
			out = append(out, "corr")
		case *expression.ScalarFunction:
			for _, a := range x.GetArgs() {
				walk(a)
			}
		}
	}
	walk(e)
	return out
}

func ppIdxs[T expression.Expression](es []T) []any {
	out := []any{}
	for _, e := range es {
		out = append(out, ppIdx(e))
	}
	return out
}

func ppBy(by []*plannerutil.ByItems) []any {
	out := []any{}
	for _, b := range by {
		out = append(out, ppIdx(b.Expr))
	}
	return out
}

func ppCols(s *expression.Schema) []any {
	out := []any{}
	for _, c := range s.Columns {
		out = append(out, c.Index)
	}
	return out
}

func ppVirt(s *expression.Schema) []any {
	out := []any{}
	for _, c := range s.Columns {
		if c.VirtualExpr != nil {
			out = append(out, ppIdx(c.VirtualExpr))
		}
	}
	return out
}

func ppPart(items []property.SortItem) []any {
	out := []any{}
	for _, it := range items {
		out = append(out, it.Col.Index)
	}
	return out
}

func ppPrefix(c *expression.Column) any {
	if c == nil {
		return nil
	}
	return c.Index
}

func ppPB(e *tipb.Executor, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	b, err := e.Marshal()
	if err != nil {
		return map[string]any{"err": vec.GoString("marshal: " + err.Error())}
	}
	return hex.EncodeToString(b)
}

func ppAcc(a base.AccessObject) any {
	pb := &tipb.ExplainOperator{}
	a.SetIntoPB(pb)
	a.SetIntoPB(nil)
	b, err := pb.Marshal()
	if err != nil {
		panic(err)
	}
	return map[string]any{"s": vec.GoString(a.String()), "n": vec.GoString(a.NormalizedString()),
		"pb": hex.EncodeToString(b)}
}

func ppHasPointGet(p base.PhysicalPlan) bool {
	if _, ok := p.(*core.PointGetPlan); ok {
		return true
	}
	for _, c := range p.Children() {
		if ppHasPointGet(c) {
			return true
		}
	}
	return false
}

func ppDump(sctx *mock.Context, bctx *base.BuildPBContext, p base.PhysicalPlan, root bool) map[string]any {
	o := map[string]any{"tp": p.TP(), "id": p.ID()}
	rootPB := false
	switch x := p.(type) {
	case *core.PhysicalTableReader:
		o["virt"] = ppVirt(x.Schema())
		o["tbl"] = ppDump(sctx, bctx, x.GetTablePlan(), false)
		pbs := []any{}
		for _, tp := range x.TablePlans {
			pbs = append(pbs, ppPB(tp.ToPB(bctx, kv.TiKV)))
		}
		o["pb"] = pbs
		o["flash"] = ppPB(x.GetTablePlan().ToPB(bctx, kv.TiFlash))
		o["acc"] = ppAcc(ppReaderAccessObject(x, sctx.GetPlanCtx()))
	case *core.PhysicalTableScan:
		o["cols"] = ppCols(x.Schema())
		o["acc"] = ppAcc(x.AccessObject())
	case *core.PointGetPlan:
		o["virt"] = ppVirt(x.Schema())
		o["acc"] = ppAcc(x.AccessObject())
	case *core.PhysicalSelection:
		o["conds"] = ppIdxs(x.Conditions)
		rootPB = true
	case *core.PhysicalProjection:
		o["exprs"] = ppIdxs(x.Exprs)
		rootPB = true
	case *core.PhysicalSort:
		o["by"] = ppBy(x.ByItems)
		rootPB = true
	case *core.NominalSort:
		o["by"] = ppBy(x.ByItems)
	case *core.PhysicalTopN:
		o["by"] = ppBy(x.ByItems)
		o["part"] = ppPart(x.PartitionBy)
		o["prefix"] = ppPrefix(x.PrefixCol)
		rootPB = true
	case *core.PhysicalLimit:
		o["cols"] = ppCols(x.Schema())
		o["part"] = ppPart(x.PartitionBy)
		o["prefix"] = ppPrefix(x.PrefixCol)
		rootPB = true
	default:
		o["other"] = true
	}
	if root && rootPB && !ppHasPointGet(p) {
		o["pb"] = ppPB(p.ToPB(bctx, kv.TiKV))
	}
	children := []any{}
	for _, c := range p.Children() {
		children = append(children, ppDump(sctx, bctx, c, root))
	}
	o["children"] = children
	return o
}

// ---------------------------------------------------------------- exec

func postprocessExec(op string, p []json.RawMessage) (out any, err error) {
	ppMu.Lock()
	defer ppMu.Unlock()
	switch op {
	case "tables":
		return odOptimize(op, p)
	case "plan":
		return ppOptimize(p, func(sctx *mock.Context, final base.PhysicalPlan) any {
			return map[string]any{"plan": ppDump(sctx, sctx.GetBuildPBCtx(), final, true)}
		})
	case "missing":
		k := arg[int](p, 3)
		return ppOptimize(p, func(sctx *mock.Context, final base.PhysicalPlan) any {
			return ppMissing(final, k)
		})
	case "access":
		return ppAccess(p), nil
	}
	return nil, fmt.Errorf("postprocess: unknown op %s", op)
}

// ppOptimize: the plan of domain "findbest" (fbPlan), then f.
func ppOptimize(p []json.RawMessage, f func(*mock.Context, base.PhysicalPlan) any) (out any, err error) {
	sbMu.Lock()
	defer sbMu.Unlock()
	rbMu.Lock()
	defer rbMu.Unlock()
	o := arg[odOpts](p, 2)
	sctx, restore := fbSession()
	defer restore()
	defer func() {
		if r := recover(); r != nil {
			out, err = map[string]any{"panic": vec.GoString(fmt.Sprint(r))}, nil
		}
	}()
	builder, lp, early := fbBuild(sctx, p, o)
	if early != nil {
		return early, nil
	}
	flag := odAdjustOptimizationFlags(builder.GetOptFlag(), lp)
	run := flag &^ (rule.FlagCollectPredicateColumnsPoint | rule.FlagSyncWaitStatsLoadPoint)
	final, _, err := core.DoOptimize(context.Background(), sctx.GetPlanCtx(), run, lp)
	if err != nil {
		return sbErr(err), nil
	}
	return f(sctx, final), nil
}

// ppPreorder: a reader, then its table plan, then its children.
func ppPreorder(p base.PhysicalPlan, out []base.PhysicalPlan) []base.PhysicalPlan {
	out = append(out, p)
	if r, ok := p.(*core.PhysicalTableReader); ok {
		out = ppPreorder(r.GetTablePlan(), out)
	}
	for _, c := range p.Children() {
		out = ppPreorder(c, out)
	}
	return out
}

func ppMissingCol() *expression.Column {
	return &expression.Column{UniqueID: 1 << 20, RetType: types.NewFieldType(mysql.TypeLonglong)}
}

func ppMissing(final base.PhysicalPlan, k int) any {
	nodes := ppPreorder(final, nil)
	if k >= len(nodes) {
		return map[string]any{"none": true}
	}
	p := nodes[k]
	miss := ppMissingCol()
	switch x := p.(type) {
	case *core.PhysicalSelection:
		x.Conditions = append(x.Conditions, miss)
	case *core.PhysicalProjection:
		x.Exprs = append(x.Exprs, miss)
	case *core.PhysicalSort:
		x.ByItems = append(x.ByItems, &plannerutil.ByItems{Expr: miss})
	case *core.NominalSort:
		x.ByItems = append(x.ByItems, &plannerutil.ByItems{Expr: miss})
	case *core.PhysicalTopN:
		x.ByItems = append(x.ByItems, &plannerutil.ByItems{Expr: miss})
	case *core.PhysicalLimit:
		cols := append(append([]*expression.Column{}, x.Schema().Columns...), miss)
		x.SetSchema(expression.NewSchema(cols...))
	default:
		return map[string]any{"tp": p.TP(), "skip": true}
	}
	if err := p.ResolveIndices(); err != nil {
		return map[string]any{"tp": p.TP(), "err": vec.GoString(err.Error())}
	}
	return map[string]any{"tp": p.TP(), "err": nil}
}

type ppDynIn struct {
	DB    string   `json:"db"`
	Table string   `json:"table"`
	All   bool     `json:"all"`
	Parts []string `json:"parts"`
	Err   string   `json:"err"`
}

type ppIdxIn struct {
	Name      string   `json:"name"`
	Cols      []string `json:"cols"`
	Clustered bool     `json:"clustered"`
}

type ppScanIn struct {
	DB    string    `json:"db"`
	Table string    `json:"table"`
	Idx   []ppIdxIn `json:"idx"`
	Parts []string  `json:"parts"`
}

// ppSetErr sets the unexported err of a DynamicPartitionAccessObject (Go sets it only when
// the partition pruning fails).
func ppSetErr(d *core.DynamicPartitionAccessObject, s string) {
	f := reflect.ValueOf(d).Elem().FieldByName("err")
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetString(s)
}

func ppAccess(p []json.RawMessage) any {
	switch kind := arg[string](p, 0); kind {
	case "dyn":
		var objs core.DynamicPartitionAccessObjects
		for _, in := range arg[[]ppDynIn](p, 1) {
			d := &core.DynamicPartitionAccessObject{Database: in.DB, Table: in.Table, AllPartitions: in.All,
				Partitions: in.Parts}
			ppSetErr(d, in.Err)
			objs = append(objs, d)
		}
		return ppAcc(objs)
	case "scan":
		in := arg[ppScanIn](p, 1)
		s := &core.ScanAccessObject{Database: in.DB, Table: in.Table, Partitions: in.Parts}
		for _, i := range in.Idx {
			s.Indexes = append(s.Indexes, core.IndexAccess{Name: i.Name, Cols: i.Cols, IsClusteredIndex: i.Clustered})
		}
		return ppAcc(s)
	case "other":
		return ppAcc(core.OtherAccessObject(arg[string](p, 1)))
	default:
		panic("postprocess: access kind " + kind)
	}
}

// ---------------------------------------------------------------- generate

// ppDDLs: findbest's tables plus a clustered common handle (tch) and a table on the hidden
// row id (tn), both without secondary indexes (an index path left after the skyline
// pruning is a boundary in tidb-rs).
var ppDDLs = append(append([]string{}, fbDDLs...),
	"CREATE TABLE tch (a INT, b VARCHAR(10), c INT, PRIMARY KEY (b, a) CLUSTERED)",
	"CREATE TABLE tn (a INT, b VARCHAR(20), c DOUBLE, d BIGINT UNSIGNED, e DECIMAL(10,2))",
)

// ppQueries: the pushed-down shapes (TableScan with keep order / desc, on an int handle, a
// clustered common handle and the hidden row id; Selection, Limit, TopN, Projection pushed
// down), root operators over a reader, point gets, a partitioned table (dynamic access
// objects) and a virtual generated column (resolveIndicesForVirtualColumn).
var ppQueries = []string{
	"SELECT c FROM sbtest1 ORDER BY id DESC",
	"SELECT c FROM sbtest1 ORDER BY id DESC LIMIT 4",
	"SELECT id, c FROM sbtest1 WHERE id > 3 ORDER BY id LIMIT 2, 5",
	"SELECT c, id + 1 FROM sbtest1 WHERE pad > 'q' LIMIT 7",
	"SELECT id * 2, c FROM sbtest1 WHERE id BETWEEN 5 AND 9",
	"SELECT pad FROM sbtest1 WHERE c = 'x' ORDER BY pad DESC LIMIT 2",
	"SELECT c FROM sbtest1 WHERE id = 3 OR id = 9",
	"SELECT c FROM sbtest1 WHERE length(c) > 3",
	"SELECT c FROM sbtest1 WHERE pad LIKE 'a%' ORDER BY c, id",
	"SELECT id, id FROM sbtest1 LIMIT 3",
	"SELECT c, c FROM sbtest1 ORDER BY c LIMIT 3",
	"SELECT * FROM tch",
	"SELECT * FROM tch WHERE b = 'x'",
	"SELECT * FROM tch WHERE b = 'x' AND a = 1",
	"SELECT a, b FROM tch WHERE b > 'm' ORDER BY b, a",
	"SELECT a, b FROM tch ORDER BY b DESC, a DESC LIMIT 3",
	"SELECT c FROM tch WHERE b BETWEEN 'a' AND 'k' AND a > 2 LIMIT 1",
	"SELECT * FROM tn WHERE c > 1.5",
	"SELECT b, e FROM tn WHERE d < 10 ORDER BY e DESC LIMIT 4",
	"SELECT * FROM tn LIMIT 1",
	"SELECT x, y FROM t3 WHERE y > 3",
	"SELECT x FROM t3 WHERE x = 'q'",
	"SELECT y FROM t3 ORDER BY z LIMIT 2",
	"SELECT * FROM tp",
	"SELECT * FROM tp WHERE a = 3",
	"SELECT b FROM tp WHERE a > 1 LIMIT 2",
	"SELECT * FROM tg",
	"SELECT a FROM tg WHERE b > 1",
	"SELECT b FROM tg ORDER BY b LIMIT 2",
	"SELECT 1 FROM sbtest1 WHERE 1 = 0",
	"SELECT c, a FROM tch WHERE c > 2 ORDER BY c LIMIT 3",
	"SELECT b FROM tch ORDER BY b, a LIMIT 3",
	"SELECT * FROM tc",
}

func ppRandQuery(rng *rand.Rand) string {
	type tbl struct {
		name  string
		cols  []string
		where []string
	}
	tables := []tbl{
		{"sbtest1", []string{"id", "c", "pad"}, []string{"id > %d", "id < %d", "id BETWEEN 1 AND %d", "c > 'b%d'", "pad < 'z%d'"}},
		{"tch", []string{"a", "b", "c"}, []string{"b = 'k%d'", "b > 'a%d'", "a > %d", "a + 1 < %d"}},
		{"tn", []string{"a", "b", "c", "d", "e"}, []string{"a > %d", "c > %d", "d < %d", "e <> %d", "b = 'v%d'"}},
	}
	t := tables[rng.IntN(len(tables))]
	sel := []string{}
	for i := 1 + rng.IntN(3); i > 0; i-- {
		c := t.cols[rng.IntN(len(t.cols))]
		if rng.IntN(5) == 0 && c != "b" && c != "c" && c != "pad" {
			c += " + " + strconv.Itoa(rng.IntN(9))
		}
		sel = append(sel, c)
	}
	q := "SELECT "
	for i, s := range sel {
		if i > 0 {
			q += ", "
		}
		q += s
	}
	q += " FROM " + t.name
	for i := rng.IntN(3); i > 0; i-- {
		w := fmt.Sprintf(t.where[rng.IntN(len(t.where))], rng.IntN(20))
		if containsWhere(q) {
			q += " AND " + w
		} else {
			q += " WHERE " + w
		}
	}
	if rng.IntN(2) == 0 {
		q += " ORDER BY " + t.cols[rng.IntN(len(t.cols))]
		if rng.IntN(2) == 0 {
			q += " DESC"
		}
	}
	if rng.IntN(2) == 0 {
		q += " LIMIT "
		if rng.IntN(3) == 0 {
			q += strconv.Itoa(rng.IntN(5)) + ", "
		}
		q += strconv.Itoa(1 + rng.IntN(9))
	}
	return q
}

func containsWhere(q string) bool {
	return strings.Contains(q, " WHERE ")
}

func postprocessGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/tables", "tables", ppDDLs)
	def := odOpts{Mode: mysql.ModeOnlyFullGroupBy}
	dyn := odOpts{Mode: mysql.ModeOnlyFullGroupBy, Dynamic: true}
	fixed := append(append([]string{}, fbVS1...), ppQueries...)
	for _, sql := range fixed {
		g.Add("sys/fixed", "plan", ppDDLs, sql, def)
		g.Add("sys/fixed", "plan", ppDDLs, sql, dyn)
	}
	for n := 0; n < 500; n++ {
		g.Add("rand/query", "plan", ppDDLs, ppRandQuery(rng),
			odOpts{Mode: mysql.ModeOnlyFullGroupBy, KeepOrder: rng.IntN(4) == 0, Dynamic: rng.IntN(2) == 0})
	}
	for _, sql := range fixed {
		for k := 0; k < 6; k++ {
			g.Add("sys/missing", "missing", ppDDLs, sql, def, k)
		}
	}
	ppGenAccess(g, rng)
}

func ppGenAccess(g *vec.Gen, rng *rand.Rand) {
	dyn := func(table string, all bool, parts []string, err string) ppDynIn {
		return ppDynIn{DB: "test", Table: table, All: all, Parts: parts, Err: err}
	}
	g.Add("sys/access", "access", "dyn", []ppDynIn{})
	g.Add("sys/access", "access", "dyn", []ppDynIn{dyn("t", true, nil, "")})
	g.Add("sys/access", "access", "dyn", []ppDynIn{dyn("t", false, nil, "")})
	g.Add("sys/access", "access", "dyn", []ppDynIn{dyn("t", false, []string{"p0", "p2"}, "")})
	g.Add("sys/access", "access", "dyn", []ppDynIn{dyn("t", false, nil, "partition table not found:7")})
	g.Add("sys/access", "access", "dyn", []ppDynIn{dyn("t1", true, nil, ""), dyn("t2", false, []string{"p1"}, ""),
		dyn("t3", false, nil, "partition pruning error:x"), dyn("t4", false, nil, "")})
	g.Add("sys/access", "access", "scan", ppScanIn{})
	g.Add("sys/access", "access", "scan", ppScanIn{DB: "test", Table: "t"})
	g.Add("sys/access", "access", "scan", ppScanIn{DB: "test", Table: "t", Parts: []string{"p0", "p1"},
		Idx: []ppIdxIn{{Name: "PRIMARY", Cols: []string{"b", "a"}, Clustered: true}, {Name: "ic", Cols: []string{"c"}}}})
	g.Add("sys/access", "access", "scan", ppScanIn{Table: "", Parts: []string{"dual"}, Idx: []ppIdxIn{{Name: "i"}}})
	g.Add("sys/access", "access", "other", "")
	g.Add("sys/access", "access", "other", "CTE:c AS d")
	names := []string{"", "t", "p0", "p1", "idx", "a", "b", "`q`", "中"}
	pick := func() string { return names[rng.IntN(len(names))] }
	picks := func() []string {
		var out []string
		for i := rng.IntN(4); i > 0; i-- {
			out = append(out, pick())
		}
		return out
	}
	for n := 0; n < 40; n++ {
		switch rng.IntN(3) {
		case 0:
			var ds []ppDynIn
			for i := rng.IntN(4); i > 0; i-- {
				e := ""
				if rng.IntN(4) == 0 {
					e = "e:" + pick()
				}
				ds = append(ds, ppDynIn{DB: pick(), Table: pick(), All: rng.IntN(3) == 0, Parts: picks(), Err: e})
			}
			g.Add("rand/access", "access", "dyn", ds)
		case 1:
			s := ppScanIn{DB: pick(), Table: pick(), Parts: picks()}
			for i := rng.IntN(3); i > 0; i-- {
				s.Idx = append(s.Idx, ppIdxIn{Name: pick(), Cols: picks(), Clustered: rng.IntN(2) == 0})
			}
			g.Add("rand/access", "access", "scan", s)
		default:
			g.Add("rand/access", "access", "other", pick())
		}
	}
}
