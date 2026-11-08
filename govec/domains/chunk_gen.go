package domains

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
)

// chunkTypes are the column types the generator draws from.
var chunkTypes = []chunkTypeSpec{
	{mysql.TypeLonglong, 0, "binary", 20},
	{mysql.TypeLonglong, mysql.UnsignedFlag, "binary", 20},
	{mysql.TypeTiny, 0, "binary", 4},
	{mysql.TypeYear, mysql.UnsignedFlag, "binary", 4},
	{mysql.TypeFloat, 0, "binary", 12},
	{mysql.TypeDouble, 0, "binary", 22},
	{mysql.TypeNewDecimal, 0, "binary", 10},
	{mysql.TypeDatetime, 0, "binary", 26},
	{mysql.TypeDate, 0, "binary", 10},
	{mysql.TypeTimestamp, 0, "binary", 19},
	{mysql.TypeDuration, 0, "binary", 3},
	{mysql.TypeVarchar, 0, "utf8mb4_bin", 20},
	{mysql.TypeVarchar, 0, "utf8mb4_general_ci", 255},
	{mysql.TypeString, 0, "utf8mb4_unicode_ci", 2000},
	{mysql.TypeBlob, 0, "binary", 0},
	{mysql.TypeJSON, 0, "binary", 0},
	{mysql.TypeTiDBVectorFloat32, 0, "binary", 0},
	{mysql.TypeEnum, 0, "utf8mb4_bin", 0},
	{mysql.TypeSet, 0, "utf8mb4_bin", 0},
	{mysql.TypeBit, mysql.UnsignedFlag, "binary", 8},
	{mysql.TypeNull, 0, "binary", 0},
}

var chunkJSONTexts = []string{
	`null`, `true`, `false`, `0`, `-1`, `1.5`, `18446744073709551615`, `"abc"`, `""`, `[]`, `{}`,
	`[1,"2",{"a":3}]`, `{"a":1,"b":[true,null]}`, `{"k":"é"}`, `1e300`, `[[[]]]`,
}

var chunkDecStrings = []string{
	"0", "1", "-1", "0.5", "-0.001", "123456789.987654321", "99999999999999999999.9999", "-0.0", "1e5",
	"12.3400", "0.000000001",
}

type chunkGen struct {
	rng   *rand.Rand
	st    *chunkState
	specs map[int][]chunkTypeSpec
	// opaque: the column types are no longer known (after swap steps); only
	// type-independent steps.
	opaque map[int]bool
	steps  [][]any
	dead   bool
	next   int
	lists  []int
	decs   []int
}

func (g *chunkGen) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return g.rng.IntN(n)
}

func (g *chunkGen) chunkIDs() []int {
	var ids []int
	for id := range g.next {
		if _, ok := g.st.chks[id]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func (g *chunkGen) pick() int {
	ids := g.chunkIDs()
	if len(ids) == 0 {
		return -1
	}
	return ids[g.intn(len(ids))]
}

// intraAliased: two slots of c hold the same column.
func (g *chunkGen) intraAliased(id int) bool {
	c := g.st.chks[id]
	for i := range c.NumCols() {
		for j := range i {
			if c.Column(i) == c.Column(j) {
				return true
			}
		}
	}
	return false
}

// shared: a column of c is held by another chunk.
func (g *chunkGen) shared(id int) bool {
	c := g.st.chks[id]
	for oid, o := range g.st.chks {
		if oid == id {
			continue
		}
		for i := range c.NumCols() {
			for j := range o.NumCols() {
				if c.Column(i) == o.Column(j) {
					return true
				}
			}
		}
	}
	return false
}

// typed: the generator knows the column types of id.
func (g *chunkGen) typed(id int) bool {
	return id >= 0 && !g.opaque[id] && len(g.specs[id]) == g.st.chks[id].NumCols()
}

func (g *chunkGen) writable(id int) bool {
	return g.typed(id) && !g.intraAliased(id) && !g.shared(id)
}

func (g *chunkGen) randTypes(n int) []chunkTypeSpec {
	r := make([]chunkTypeSpec, n)
	for i := range r {
		r[i] = chunkTypes[g.intn(len(chunkTypes))]
	}
	return r
}

func (g *chunkGen) value(ft *types.FieldType) any {
	if g.intn(7) == 0 {
		return nil
	}
	switch chunkKind(ft) {
	case "i64":
		return strconv.FormatInt([]int64{0, 1, -1, math.MaxInt64, math.MinInt64, g.rng.Int64() - math.MaxInt64/2, int64(g.intn(100))}[g.intn(7)], 10)
	case "u64":
		return strconv.FormatUint([]uint64{0, 1, math.MaxUint64, g.rng.Uint64(), uint64(g.intn(100))}[g.intn(5)], 10)
	case "f32":
		bits := []uint32{0, 0x80000000, 0x7f800000, 0xff800000, 0x7fc00000, 0x7f800001, math.Float32bits(1.5), g.rng.Uint32()}[g.intn(8)]
		return strconv.FormatUint(uint64(bits), 10)
	case "f64":
		bits := []uint64{0, 1 << 63, 0x7ff0000000000000, 0x7ff8000000000001, math.Float64bits(-2.25), g.rng.Uint64()}[g.intn(6)]
		return strconv.FormatUint(bits, 10)
	case "dec":
		if g.intn(3) == 0 {
			return strconv.FormatFloat((g.rng.Float64()-0.5)*1e6, 'f', g.intn(8), 64)
		}
		return chunkDecStrings[g.intn(len(chunkDecStrings))]
	case "time":
		ct := types.FromDate(1000+g.intn(9000), 1+g.intn(12), 1+g.intn(28), g.intn(24), g.intn(60), g.intn(60), g.intn(1000000))
		tp := []uint8{mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp}[g.intn(3)]
		return []any{strconv.FormatUint(uint64(ct), 10), tp, g.intn(7)}
	case "dur":
		n := (g.rng.Int64N(2*838*3600) - 838*3600) * 1e9
		n += g.rng.Int64N(1e9)
		return []any{strconv.FormatInt(n, 10), g.intn(7)}
	case "json":
		return chunkJSONTexts[g.intn(len(chunkJSONTexts))]
	case "vec":
		bits := make([]uint32, g.intn(5))
		for i := range bits {
			bits[i] = math.Float32bits(float32(g.rng.NormFloat64()))
		}
		return bits
	case "nv":
		return []any{g.bytes(6), g.rng.Uint64N(1 << 20)}
	case "null":
		return nil
	}
	return g.bytes(12)
}

func (g *chunkGen) bytes(max int) string {
	b := make([]byte, g.intn(max+1))
	for i := range b {
		if g.intn(4) == 0 {
			b[i] = byte(g.rng.UintN(256))
		} else {
			b[i] = "abcAB _é"[g.intn(9)]
		}
	}
	return hex.EncodeToString(b)
}

// step runs a step on the generator's state and keeps it; false if it panicked
// (the script ends there).
func (g *chunkGen) step(s ...any) bool {
	if g.dead {
		return false
	}
	raw := make([]json.RawMessage, len(s))
	for i, x := range s {
		raw[i] = vec.MustJSON(x)
	}
	ok := func() (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		chunkStep(g.st, raw)
		return true
	}()
	if ok {
		g.steps = append(g.steps, s)
	} else {
		// The state may be half-updated: end the script before this step.
		g.dead = true
	}
	return ok
}

func (g *chunkGen) newChunk() int {
	id := g.next
	g.next++
	specs := g.randTypes(1 + g.intn(4))
	g.specs[id] = specs
	switch g.intn(4) {
	case 0:
		g.step("new_empty", id, specs)
	case 1:
		g.step("new_cap", id, specs, g.intn(40))
	case 2:
		g.step("new_pool", id, specs, g.intn(40))
	default:
		g.step("new", id, specs, g.intn(40), 1+g.intn(40))
	}
	return id
}

func (g *chunkGen) fill(id int, rows int) {
	c := g.st.chks[id]
	if g.dead || c == nil {
		return
	}
	fts := g.st.fts[id]
	for range rows {
		for col := range c.NumCols() {
			if !g.step("app", id, col, g.value(fts[col])) {
				return
			}
		}
	}
}

func (g *chunkGen) rowsOf(id int, n int) []int {
	nr := g.st.chks[id].NumRows()
	r := make([]int, n)
	for i := range r {
		r[i] = g.intn(nr)
	}
	return r
}

func (g *chunkGen) sameSchema(id int) int {
	d := g.next
	g.next++
	g.specs[d] = g.specs[id]
	g.step("new", d, g.specs[id], g.intn(20), 1+g.intn(30))
	return d
}

func (g *chunkGen) bools(n int) []bool {
	r := make([]bool, n)
	for i := range r {
		r[i] = g.intn(2) == 0
	}
	return r
}

// goSafe runs f, a call into the Go chunk package made while building a step (outside
// step's recover); false if it panicked, and the script ends there like a panicking step.
func (g *chunkGen) goSafe(f func()) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
			g.dead = true
		}
	}()
	f()
	return true
}

// randomStep may run after the script died (a dump ended it): it still draws from rng as
// before, so later scripts of a seed are unchanged, and every step it builds is refused.
func (g *chunkGen) randomStep() bool {
	id := g.pick()
	if id < 0 || g.intn(8) == 0 {
		g.fill(g.newChunk(), g.intn(12))
		return true
	}
	c := g.st.chks[id]
	nr, nc := c.NumRows(), c.NumCols()
	w := g.writable(id)
	sel := c.Sel() != nil
	switch g.intn(46) {
	case 0, 1, 2:
		if w {
			g.fill(id, 1+g.intn(5))
		}
	case 3:
		if w && nc > 0 && !sel {
			for col := range nc {
				g.step("capp", id, col, g.value(g.st.fts[id][col]))
			}
		}
	case 4:
		if w && nc > 0 && !sel {
			n := g.intn(10)
			for col := range nc {
				g.step("app_nn", id, col, n)
			}
		}
	case 5:
		if w && nr > 0 && !sel {
			// AppendCellNTimes on every column keeps the chunk rectangular.
			s := g.pick()
			if s >= 0 && g.typed(s) && g.st.chks[s].NumRows() > 0 && len(g.specs[s]) > 0 {
				d := g.sameSchema(s)
				pos, times := g.intn(g.st.chks[s].NumRows()), 1+g.intn(9)
				for col := range len(g.specs[s]) {
					g.step("app_cell", d, col, s, col, pos, times)
				}
			}
		}
	case 6, 7:
		if nr > 0 && g.typed(id) {
			d := g.sameSchema(id)
			switch g.intn(4) {
			case 0:
				return g.step("app_row", d, id, g.intn(nr))
			case 1:
				return g.step("app_rows", d, id, g.rowsOf(id, g.intn(6)))
			case 2:
				var idxs any
				if g.intn(3) > 0 {
					idxs = func() []int {
						r := make([]int, nc)
						for i := range r {
							r[i] = i
						}
						return r
					}()
				}
				return g.step("app_row_idx", d, id, g.intn(nr), idxs)
			default:
				b := g.intn(nr + 1)
				return g.step("app_range", d, id, b, b+g.intn(nr-b+1))
			}
		}
	case 8:
		if nr > 0 && g.typed(id) && nc > 0 {
			// partial rows: a wider destination whose tail has the source schema.
			d := g.next
			g.next++
			pre := g.randTypes(g.intn(3))
			specs := append(append([]chunkTypeSpec{}, pre...), g.specs[id]...)
			g.specs[d] = specs
			g.step("new", d, specs, g.intn(20), 32)
			switch g.intn(3) {
			case 0:
				return g.step("app_prow", d, len(pre), id, g.intn(nr))
			case 1:
				return g.step("app_prows", d, len(pre), id, g.rowsOf(id, g.intn(5)))
			default:
				return g.step("app_prow_idx", d, len(pre), id, g.intn(nr), []int{nc - 1})
			}
		}
	case 9:
		if w && nr > 0 {
			return g.step("truncate", id, g.intn(nr+1))
		}
	case 10:
		if !g.shared(id) {
			return g.step("reset", id)
		}
	case 11:
		if !g.shared(id) {
			return g.step("grow_reset", id, g.intn(64))
		}
	case 12, 13:
		if w && nr > 0 && !sel {
			var s []int
			for i := range nr {
				if g.intn(3) > 0 {
					s = append(s, i)
				}
			}
			if s == nil {
				s = []int{}
			}
			return g.step("set_sel", id, s)
		}
	case 14:
		if w {
			return g.step("reconstruct", id)
		}
	case 15:
		return g.step("set_req", id, g.intn(50)-5, 1+g.intn(40))
	case 16:
		if nc == 0 || g.intn(2) == 0 {
			return g.step("set_virt", id, g.intn(20))
		}
		return g.step("set_inc", id, g.intn(2) == 0)
	case 17:
		if nc >= 2 && !g.shared(id) && !sel && g.typed(id) {
			// MakeRef between slots of the same type.
			for i := range nc {
				for j := range nc {
					if i != j && g.specs[id][i] == g.specs[id][j] {
						return g.step("make_ref", id, i, j)
					}
				}
			}
		}
	case 18:
		if g.typed(id) && nc > 0 {
			d := g.sameSchema(id)
			col := g.intn(nc)
			return g.step("make_ref_to", d, col, id, col)
		}
	case 19:
		o := g.pick()
		if o >= 0 && o != id && !g.shared(id) && !g.shared(o) {
			g.opaque[id], g.opaque[o] = true, true
			return g.step("swap_cols", id, o)
		}
	case 20:
		if nc > 0 && !g.shared(id) {
			out := g.next
			g.next++
			used := make([]int, 1+g.intn(4))
			specs := make([]chunkTypeSpec, len(used))
			for i := range used {
				used[i] = g.intn(nc)
				specs[i] = chunkTypes[0]
			}
			g.specs[out] = specs
			g.step("new", out, specs, 4, 4)
			g.opaque[id], g.opaque[out] = true, true
			return g.step("swap_helper", id, out, used)
		}
	case 21:
		if w && nc > 0 && !sel {
			col := g.intn(nc)
			ft := g.st.fts[id][col]
			n := g.intn(30)
			if k := chunkKind(ft); k == "i64" || k == "u64" || k == "f32" || k == "f64" || k == "dec" || k == "dur" || k == "time" {
				if nc == 1 {
					return g.step("resize", id, col, k, n, g.intn(2) == 0)
				}
			}
			if chunkKind(ft) == "bytes" && nc == 1 {
				return g.step("reserve", id, col, []string{"str", "bytes", "json", "vec", "set", "enum"}[g.intn(6)], n)
			}
		}
	case 22:
		if w && nc == 1 && !sel {
			ft := g.st.fts[id][0]
			et := map[string]int{"i64": 0, "u64": 0, "f64": 1, "dec": 2, "bytes": 3, "time": 4, "dur": 5, "json": 6, "vec": 8}
			if e, ok := et[chunkKind(ft)]; ok {
				if chunkKind(ft) == "f64" || chunkKind(ft) == "dec" || chunkKind(ft) == "time" || chunkKind(ft) == "dur" || chunkKind(ft) == "json" || chunkKind(ft) == "vec" || chunkKind(ft) == "bytes" || chunkKind(ft) == "i64" || chunkKind(ft) == "u64" {
					return g.step("col_reset", id, 0, e)
				}
			}
		}
	case 23, 24:
		if nc > 0 && w && nr > 0 && !sel {
			col := g.intn(nc)
			if g.intn(2) == 0 {
				return g.step("set_null", id, col, g.intn(nr), g.intn(2) == 0)
			}
			b := g.intn(nr + 1)
			return g.step("set_nulls", id, col, b, b+g.intn(nr-b+1), g.intn(2) == 0)
		}
	case 25:
		if nc > 0 && w && nr > 0 && !sel {
			col := g.intn(nc)
			if chunkKind(g.st.fts[id][col]) == "bytes" {
				r := g.intn(nr)
				var n int
				if !g.goSafe(func() { n = len(c.Column(col).GetBytes(r)) }) {
					return false
				}
				b := make([]byte, n)
				for i := range b {
					b[i] = byte(g.rng.UintN(256))
				}
				return g.step("set_raw", id, col, r, hex.EncodeToString(b))
			}
		}
	case 26:
		if nc > 0 && w && nr > 0 && !sel {
			col := g.intn(nc)
			if c.Column(col) != nil && chunkKind(g.st.fts[id][col]) != "bytes" && chunkKind(g.st.fts[id][col]) != "json" && chunkKind(g.st.fts[id][col]) != "vec" && chunkKind(g.st.fts[id][col]) != "nv" && chunkKind(g.st.fts[id][col]) != "null" {
				var pairs [][2]int
				for o := range nc {
					if o != col && g.intn(2) == 0 {
						pairs = append(pairs, [2]int{id, o})
					}
				}
				return g.step("merge_nulls", id, col, pairs)
			}
		}
	case 27, 28:
		if nc > 0 && nr > 0 && g.typed(id) && !sel {
			col := g.intn(nc)
			d := g.next
			g.next++
			g.specs[d] = []chunkTypeSpec{g.specs[id][col]}
			g.step("new", d, g.specs[d], g.intn(10), 32)
			if g.intn(3) > 0 {
				g.fill(d, g.intn(4))
			}
			into := g.intn(2) == 0
			if g.intn(2) == 0 {
				return g.step("col_copy", d, 0, id, col, into)
			}
			var s any
			if g.intn(4) > 0 {
				s = g.rowsOf(id, g.intn(nr+2))
			}
			return g.step("col_copy_recon", d, 0, id, col, s, into)
		}
	case 29:
		if nc > 0 && w && nr > 0 && !sel {
			col := g.intn(nc)
			k := chunkKind(g.st.fts[id][col])
			r := g.intn(nr)
			switch k {
			case "i64":
				return g.step("view_set", id, col, k, r, strconv.FormatInt(g.rng.Int64(), 10))
			case "u64", "f64", "f32":
				v := g.rng.Uint64()
				if k == "f32" {
					v &= math.MaxUint32
				}
				return g.step("view_set", id, col, k, r, strconv.FormatUint(v, 10))
			case "dur":
				return g.step("view_set", id, col, k, r, strconv.FormatInt(g.rng.Int64N(1e15), 10))
			case "time":
				return g.step("view_set", id, col, k, r, strconv.FormatUint(uint64(types.FromDate(2000+g.intn(100), 1+g.intn(12), 1+g.intn(28), 0, 0, 0, 0)), 10))
			case "dec":
				return g.step("view_set", id, col, k, r, chunkDecStrings[g.intn(len(chunkDecStrings))])
			}
		}
	case 30, 31, 32:
		if nc > 0 && nr > 0 && g.typed(id) {
			return g.step("get", id, g.intn(nr), g.intn(nc))
		}
	case 33:
		if g.typed(id) && nc > 0 && !sel {
			col := g.intn(nc)
			if k := chunkKind(g.st.fts[id][col]); k != "bytes" && k != "json" && k != "vec" && k != "nv" && k != "null" {
				return g.step("view", id, col, k)
			}
		}
	case 34, 35:
		if nc > 0 && nr > 0 && g.typed(id) {
			col := g.intn(nc)
			return g.step("cmp", id, g.intn(nr), col, id, g.intn(nr), col)
		}
	case 36:
		if g.intn(2) == 0 || nr == 0 {
			return g.step("iter", id)
		}
		return g.step("iter_slice", id, g.rowsOf(id, g.intn(6)))
	case 37:
		if nr > 0 {
			return g.step("row", id, g.intn(nr))
		}
	case 38:
		if g.typed(id) {
			// encode, then decode into a new chunk, into an existing one, or through a Decoder
			if c.Sel() != nil {
				return g.step("encode", id)
			}
			var raw string
			if !g.goSafe(func() { raw = hex.EncodeToString(chunk.NewCodec(nil).Encode(c)) }) {
				return false
			}
			switch g.intn(3) {
			case 0:
				d := g.next
				g.next++
				g.specs[d] = g.specs[id]
				return g.step("decode", d, g.specs[id], raw)
			case 1:
				d := g.sameSchema(id)
				return g.step("decode_into", d, raw)
			default:
				dec := g.next
				g.next++
				g.step("dec_new", dec, g.specs[id])
				if !g.step("dec_reset", dec, raw) {
					return false
				}
				for range 4 {
					d := g.sameSchema(id)
					g.step("set_req", d, 1+g.intn(12), 32)
					if g.st.decs[dec].IsFinished() {
						break
					}
					if g.intn(3) == 0 {
						g.step("dec_reuse", dec, d)
					} else {
						g.step("dec_decode", dec, d)
						if g.intn(2) == 0 && !g.st.decs[dec].IsFinished() {
							g.step("dec_decode", dec, d)
						}
					}
					g.step("dump", d)
				}
				return g.step("dec_state", dec)
			}
		}
	case 39, 40:
		if g.typed(id) && nr > 0 {
			l := g.next
			g.next++
			g.step("list_new", l, g.specs[id], 1+g.intn(4), 2+g.intn(6))
			for range 1 + g.intn(12) {
				g.step("list_app", l, id, g.intn(nr))
			}
			if g.intn(2) == 0 && g.writable(id) && !sel {
				cp := g.next
				g.next++
				g.specs[cp] = g.specs[id]
				g.step("copy", cp, id)
				g.step("list_add", l, cp)
				g.step("list_app", l, id, g.intn(nr))
			}
			g.step("list_get", l, 0, 0)
			g.step("list_walk", l)
			g.step("list_iter", l)
			g.step("list_ptr_iter", l, [][2]uint32{{0, 0}, {0, 0}})
			g.step("multi_iter", [][]int{{0, id}, {1, l}, {2, id, 0}}, 0)
			switch g.intn(3) {
			case 0:
				g.step("list_reset", l)
				g.step("list_app", l, id, g.intn(nr))
				a := g.next
				g.next++
				g.specs[a] = g.specs[id]
				g.step("list_alloc", l, a)
			case 1:
				g.step("list_clear", l)
			}
			return g.step("list_walk", l)
		}
	case 41, 42:
		if nc > 0 && nr > 0 && g.typed(id) && !sel {
			d := g.sameSchema(id)
			if g.intn(2) == 0 {
				return g.step("copy_join_direct", id, g.bools(nr), d)
			}
			k := g.intn(nc + 1)
			// Go requires the outer rows to be equal; row 0 repeated satisfies it.
			return g.step("copy_join_outer", id, 0, k, k, nc-k, g.bools(nr), d)
		}
	case 43:
		if nc > 0 && nr > 0 && g.typed(id) && !sel {
			col := g.intn(nc)
			d := g.next
			g.next++
			g.specs[d] = []chunkTypeSpec{g.specs[id][col]}
			g.step("new", d, g.specs[d], 8, 8)
			if g.intn(2) == 0 {
				return g.step("copy_sel_rows", d, 0, id, col, g.bools(nr))
			}
			b := g.intn(nr + 1)
			return g.step("copy_sel_rows_fn", d, 0, id, col, g.bools(nr), b, b+g.intn(nr-b+1))
		}
	case 44:
		if g.typed(id) {
			d := g.next
			g.next++
			switch g.intn(4) {
			case 0:
				g.specs[d] = g.specs[id]
				return g.step("copy", d, id)
			case 1:
				g.specs[d] = g.specs[id]
				return g.step("copy_sel", d, id)
			case 2:
				g.specs[d] = g.specs[id]
				return g.step([]string{"renew", "clone_empty"}[g.intn(2)], d, id, g.intn(64))
			default:
				if nc > 0 {
					idxs := []int{g.intn(nc)}
					g.specs[d] = []chunkTypeSpec{g.specs[id][idxs[0]]}
					return g.step("prune", d, id, idxs)
				}
			}
		}
	case 45:
		if w && nc == 1 && !sel {
			// AppendCellFromRawData: fixed cells are elemLen bytes, var cells a length and bytes.
			ft := g.st.fts[id][0]
			fl := chunk.GetFixedLen(ft)
			var b []byte
			if fl > 0 {
				b = make([]byte, fl)
				for i := range b {
					b[i] = byte(g.rng.UintN(256))
				}
				if chunkKind(ft) == "dec" {
					b[3] = byte(g.intn(2))
				}
			} else {
				n := g.intn(6)
				b = make([]byte, 8+n)
				b[0] = byte(n)
				for i := 8; i < len(b); i++ {
					b[i] = byte('a' + g.intn(26))
				}
			}
			if chunkKind(ft) == "json" || chunkKind(ft) == "vec" || chunkKind(ft) == "nv" {
				return true
			}
			// AppendCellFromRawData does not touch the null bitmap; callers append the bit first.
			if !g.step("app_nb", id, 0, g.intn(3) > 0) {
				return false
			}
			return g.step("app_raw", id, 0, hex.EncodeToString(b), 0)
		}
	}
	return true
}

func chunkRandomScript(rng *rand.Rand, nsteps int) [][]any {
	g := &chunkGen{rng: rng, st: newChunkState(), specs: map[int][]chunkTypeSpec{}, opaque: map[int]bool{}}
	g.fill(g.newChunk(), 1+g.intn(10))
	for range nsteps {
		if !g.randomStep() || g.dead {
			break
		}
		if g.intn(4) == 0 {
			if id := g.pick(); id >= 0 {
				g.step("dump", id)
			}
		}
	}
	for _, id := range g.chunkIDs() {
		g.step("dump", id)
	}
	return g.steps
}

func chunkGenerate(g *vec.Gen, rng *rand.Rand) {
	// type_width: every preset type, plus string lengths around the EstimateTypeWidth thresholds.
	g.Add("sys/width", "type_width", chunkTypes)
	for _, flen := range []int{-1, 0, 1, 31, 32, 33, 999, 1000, 1001, 65535} {
		g.Add("sys/width", "type_width", []chunkTypeSpec{{mysql.TypeVarchar, 0, "utf8mb4_bin", flen}, {mysql.TypeBlob, 0, "binary", flen}})
	}
	for range 40 {
		var specs []chunkTypeSpec
		for range 1 + rng.IntN(5) {
			s := chunkTypes[rng.IntN(len(chunkTypes))]
			s.Flen = rng.IntN(3000) - 10
			specs = append(specs, s)
		}
		g.Add("rand/width", "type_width", specs)
	}

	chunkMinedScripts(g)

	for i := range 1600 {
		steps := chunkRandomScript(rng, 8+rng.IntN(30))
		src := "rand/script"
		if i%4 == 0 {
			src = "rand/script-long"
		}
		g.Add(src, "script", steps)
	}

	// After block D01.1: the Datum / Enum / Set users and mutrow.go.
	chunkDatumMinedScripts(g)
	for range 600 {
		g.Add("rand/datum-script", "script", chunkDatumScript(rng, 6+rng.IntN(30)))
	}
}

// ChunkMinedTests lists the Go tests chunkMinedScripts reproduces.
func ChunkMinedTests() []string {
	return []string{"TestAppendRow", "TestAppendChunk", "TestTruncateTo", "TestChunkSizeControl", "TestSwapColumn",
		"TestAppendSel", "TestMakeRefTo", "TestCodec", "TestColumnCopyReconstructFixedLen", "TestResize", "TestMyDecimal",
		"TestTimeColumn", "TestJSONColumn", "TestCopySelectedJoinRowsDirect", "TestCopySelectedJoinRows",
		"TestMergeInputIdxToOutputIdxes", "TestIterator", "TestList", "TestListMemoryUsage", "TestCompare"}
}

// chunkMinedScripts: scenarios of chunk_test.go, column_test.go, codec_test.go,
// chunk_util_test.go, iterator_test.go and list_test.go, as scripts.
func chunkMinedScripts(g *vec.Gen) {
	i64 := chunkTypes[0]
	f32 := chunkTypes[4]
	str := chunkTypes[11]
	dec := chunkTypes[6]
	dt := chunkTypes[7]
	js := chunkTypes[15]
	script := func(src string, steps ...[]any) { g.Add(src, "script", steps) }
	s := func(a ...any) []any { return a }
	var fill []([]any)
	for i := range 10 {
		fill = append(fill, s("app", 0, 0, strconv.Itoa(i)), s("app", 0, 1, strconv.Itoa(int(math.Float32bits(float32(i))))),
			s("app", 0, 2, hex.EncodeToString([]byte(strconv.Itoa(i)))))
	}
	base := append([][]any{s("new", 0, []chunkTypeSpec{i64, f32, str}, 0, 1024)}, fill...)
	script("TestAppendRow", append(append([][]any{}, base...), s("new", 1, []chunkTypeSpec{i64, f32, str}, 0, 1024),
		s("app_row", 1, 0, 3), s("app_row", 1, 0, 9), s("dump", 1), s("get", 1, 1, 2))...)
	script("TestAppendChunk", append(append([][]any{}, base...), s("new", 1, []chunkTypeSpec{i64, f32, str}, 0, 1024),
		s("app_range", 1, 0, 2, 7), s("app_range", 1, 0, 0, 10), s("dump", 1))...)
	script("TestTruncateTo", append(append([][]any{}, base...), s("app", 0, 0, nil), s("truncate", 0, 5), s("dump", 0),
		s("app", 0, 0, "7"), s("app", 0, 1, nil), s("app", 0, 2, nil), s("dump", 0))...)
	script("TestChunkSizeControl", s("new", 0, []chunkTypeSpec{i64}, 0, 3), s("app", 0, 0, "1"), s("dump", 0),
		s("grow_reset", 0, 3), s("dump", 0), s("renew", 1, 0, 5), s("dump", 1), s("set_req", 0, 2, 3), s("dump", 0))
	script("TestSwapColumn", s("new", 0, []chunkTypeSpec{i64, i64, i64}, 4, 4), s("app", 0, 0, "1"), s("app", 0, 1, "2"),
		s("app", 0, 2, "3"), s("make_ref", 0, 0, 1), s("new", 1, []chunkTypeSpec{i64, i64}, 4, 4), s("app", 1, 0, "9"),
		s("app", 1, 1, "8"), s("swap_helper", 0, 1, []int{0, 0}), s("dump", 0), s("dump", 1))
	script("TestAppendSel", s("new", 0, []chunkTypeSpec{i64}, 4, 1024), s("app", 0, 0, "1"), s("app", 0, 0, "2"),
		s("set_sel", 0, []int{1}), s("app", 0, 0, "3"), s("dump", 0), s("reconstruct", 0), s("dump", 0))
	script("TestMakeRefTo", s("new", 0, []chunkTypeSpec{i64, i64}, 4, 4), s("app", 0, 0, "1"), s("app", 0, 1, "2"),
		s("new", 1, []chunkTypeSpec{i64, i64}, 4, 4), s("make_ref_to", 1, 0, 0, 1), s("make_ref_to", 1, 1, 0, 0),
		s("dump", 1), s("set_sel", 0, []int{0}), s("make_ref_to", 1, 0, 0, 0))
	script("TestCodec", append(append([][]any{}, base...), s("app", 0, 0, nil), s("app", 0, 1, nil), s("app", 0, 2, nil),
		s("encode", 0), s("decode", 1, []chunkTypeSpec{i64, f32, str},
			hex.EncodeToString(func() []byte {
				c := chunk.New(chunkFts([]chunkTypeSpec{i64, f32, str}), 0, 1024)
				for i := range 3 {
					c.AppendInt64(0, int64(i))
					c.AppendNull(1)
					c.AppendString(2, "x")
				}
				return chunk.NewCodec(nil).Encode(c)
			}())), s("dump", 1))...)
	script("TestColumnCopyReconstructFixedLen", append(append([][]any{}, base...), s("new", 1, []chunkTypeSpec{i64}, 0, 32),
		s("col_copy_recon", 1, 0, 0, 0, []int{9, 2, 4}, false), s("dump", 1),
		s("col_copy_recon", 1, 0, 0, 2, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, true), s("dump", 1))...)
	script("TestResize", s("new", 0, []chunkTypeSpec{i64}, 4, 4), s("app", 0, 0, "11"), s("app", 0, 0, "12"),
		s("resize", 0, 0, "i64", 3, true), s("dump", 0), s("resize", 0, 0, "i64", 1, false), s("dump", 0),
		s("resize", 0, 0, "i64", 16, true), s("dump", 0), s("view", 0, 0, "i64"))
	script("TestMyDecimal", s("new", 0, []chunkTypeSpec{dec}, 4, 4), s("app", 0, 0, "1.25"), s("app", 0, 0, nil),
		s("app", 0, 0, "-99.000"), s("view", 0, 0, "dec"), s("view_set", 0, 0, "dec", 1, "7"), s("get", 0, 1, 0), s("dump", 0))
	script("TestTimeColumn", s("new", 0, []chunkTypeSpec{dt}, 4, 4),
		s("app", 0, 0, []any{strconv.FormatUint(uint64(types.FromDate(2024, 2, 29, 23, 59, 59, 999999)), 10), mysql.TypeDatetime, 6}),
		s("get", 0, 0, 0), s("view", 0, 0, "time"), s("dump", 0))
	script("TestJSONColumn", s("new", 0, []chunkTypeSpec{js}, 4, 4), s("app", 0, 0, `{"a":[1,2]}`), s("app", 0, 0, nil),
		s("get", 0, 0, 0), s("cmp", 0, 0, 0, 0, 1, 0), s("dump", 0))
	script("TestCopySelectedJoinRowsDirect", append(append([][]any{}, base...), s("new", 1, []chunkTypeSpec{i64, f32, str}, 0, 1024),
		s("copy_join_direct", 0, []bool{true, false, true, true, false, false, false, false, true, false}, 1), s("dump", 1))...)
	script("TestCopySelectedJoinRows", append(append([][]any{}, base...), s("new", 1, []chunkTypeSpec{i64, f32, str}, 0, 1024),
		s("copy_join_outer", 0, 0, 2, 2, 1, []bool{true, true, false, true, false, false, false, false, true, true}, 1), s("dump", 1))...)
	script("TestMergeInputIdxToOutputIdxes", s("new_empty", 0, []chunkTypeSpec{i64, i64}), s("app", 0, 0, "99"),
		s("make_ref", 0, 0, 1), s("new_empty", 1, []chunkTypeSpec{i64, i64, i64, i64}),
		s("swap_helper", 0, 1, []int{0, 0, 1, 1}), s("dump", 0), s("dump", 1),
		s("get", 1, 0, 0), s("get", 1, 0, 1), s("get", 1, 0, 2), s("get", 1, 0, 3))
	script("TestIterator", append(append([][]any{}, base...), s("iter", 0), s("iter_slice", 0, []int{1, 3, 5}),
		s("list_new", 2, []chunkTypeSpec{i64, f32, str}, 2, 4), s("list_app", 2, 0, 0), s("list_app", 2, 0, 1),
		s("list_app", 2, 0, 2), s("list_iter", 2), s("list_ptr_iter", 2, [][2]uint32{{0, 1}, {1, 0}}),
		s("multi_iter", [][]int{{0, 0}, {1, 2}}, 0), s("multi_iter", [][]int{{0, 0}, {1, 2}}, 1))...)
	script("TestList", append(append([][]any{}, base...), s("list_new", 2, []chunkTypeSpec{i64, f32, str}, 2, 8),
		s("list_app", 2, 0, 0), s("list_app", 2, 0, 1), s("list_app", 2, 0, 2), s("list_walk", 2), s("list_get", 2, 1, 0),
		s("list_reset", 2), s("list_app", 2, 0, 3), s("list_alloc", 2, 5), s("list_clear", 2), s("list_walk", 2))...)
	script("TestListMemoryUsage", append(append([][]any{}, base...), s("list_new", 2, []chunkTypeSpec{i64, f32, str}, 2, 8),
		s("list_app", 2, 0, 0), s("copy", 3, 0), s("list_add", 2, 3), s("list_app", 2, 0, 1), s("list_reset", 2))...)
	script("TestCompare", s("new", 0, []chunkTypeSpec{i64, chunkTypes[1], f32, chunkTypes[5], dec, dt, chunkTypes[10], str,
		chunkTypes[12], js, chunkTypes[16], chunkTypes[17], chunkTypes[19], chunkTypes[20]}, 4, 4),
		s("app", 0, 0, nil), s("app", 0, 1, "1"), s("app", 0, 2, "0"), s("app", 0, 3, "0"), s("app", 0, 4, "0.1"),
		s("app", 0, 5, nil), s("app", 0, 6, []any{"5", 0}), s("app", 0, 7, "61"), s("app", 0, 8, "41"),
		s("app", 0, 9, "1"), s("app", 0, 10, []uint32{0x3f800000}), s("app", 0, 11, []any{"61", 2}), s("app", 0, 12, "0001"),
		s("app", 0, 13, nil),
		s("app", 0, 0, "5"), s("app", 0, 1, "18446744073709551615"), s("app", 0, 2, "2143289344"), s("app", 0, 3, "9221120237041090561"),
		s("app", 0, 4, "-0.1"), s("app", 0, 5, []any{strconv.FormatUint(uint64(types.FromDate(2000, 1, 1, 0, 0, 0, 0)), 10), mysql.TypeDate, 0}),
		s("app", 0, 6, []any{"-5", 0}), s("app", 0, 7, "41"), s("app", 0, 8, "61"), s("app", 0, 9, `"1"`),
		s("app", 0, 10, []uint32{}), s("app", 0, 11, []any{"62", 1}), s("app", 0, 12, "01"), s("app", 0, 13, nil),
		s("cmp", 0, 0, 0, 0, 1, 0), s("cmp", 0, 1, 1, 0, 0, 1), s("cmp", 0, 0, 2, 0, 1, 2), s("cmp", 0, 1, 3, 0, 0, 3),
		s("cmp", 0, 0, 4, 0, 1, 4), s("cmp", 0, 0, 5, 0, 1, 5), s("cmp", 0, 0, 6, 0, 1, 6), s("cmp", 0, 0, 7, 0, 1, 7),
		s("cmp", 0, 0, 8, 0, 1, 8), s("cmp", 0, 0, 9, 0, 1, 9), s("cmp", 0, 0, 10, 0, 1, 10), s("cmp", 0, 0, 11, 0, 1, 11),
		s("cmp", 0, 0, 12, 0, 1, 12), s("cmp", 0, 0, 13, 0, 1, 13), s("dump", 0))
}
