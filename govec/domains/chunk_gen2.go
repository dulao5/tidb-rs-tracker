package domains

// Scripts for the chunk functions built on types.Datum / Enum / Set and for
// mutrow.go (the follow-up of block D01.7 after block D01.1). They are added
// after the other scripts of the domain, so the earlier records of a seed do
// not change.
//
// Steps (see chunkStep): app_datum c col datum | get_datum c r col ft|null |
// datum_row c r | datum_row_buf c r [datum...] | to_string c | row_string c r |
// cmp_datum c r col datum | lower_bound c col datum | upper_bound c col datum |
// mr_values m [value...] | mr_datums m [datum...] | mr_types m types |
// mr_set_row m c r | mr_set_values m [value...] | mr_set_value m col value |
// mr_set_datums m [datum...] | mr_set_datum m col datum | mr_shallow m col c r |
// mr_clone m2 m | mr_dump m | mr_get m col [type] | mr_string m types.
// A datum is datum2's [kind, payload...], a value goValueIn's [type, payload],
// a field type for get_datum d2FtIn's [tp, flag, flen, decimal, charset,
// collate, elems].
//
// Go's ShallowCopyPartialRow makes the MutRow's cells alias the source column
// (a later write to either is seen by the other, TestMutRowShallowCopyPartialRow
// checks it); the Rust port copies the cells (D01.7 spec §4). The generator
// therefore never writes to the source chunk or through the MutRow after a
// shallow copy.

import (
	"encoding/hex"
	"math"
	"math/rand/v2"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

// chunkDatumOf converts a generator value of column type ft (see chunkGen.value)
// to a datum spec; alt picks another datum kind for the same value where one
// exists.
func chunkDatumOf(ft *types.FieldType, v any, alt bool) []any {
	if v == nil {
		return []any{types.KindNull}
	}
	switch chunkKind(ft) {
	case "i64":
		n, _ := strconv.ParseInt(v.(string), 10, 64)
		if alt {
			return []any{types.KindUint64, uint64(n)}
		}
		return []any{types.KindInt64, n}
	case "u64":
		n, _ := strconv.ParseUint(v.(string), 10, 64)
		if alt {
			return []any{types.KindInt64, int64(n)}
		}
		return []any{types.KindUint64, n}
	case "f32":
		b, _ := strconv.ParseUint(v.(string), 10, 32)
		return []any{types.KindFloat32, math.Float64bits(float64(math.Float32frombits(uint32(b))))}
	case "f64":
		b, _ := strconv.ParseUint(v.(string), 10, 64)
		return []any{types.KindFloat64, b}
	case "dec":
		return []any{types.KindMysqlDecimal, v.(string)}
	case "time":
		a := v.([]any)
		ct, _ := strconv.ParseUint(a[0].(string), 10, 64)
		t := types.NewTime(types.CoreTime(ct), a[1].(uint8), a[2].(int))
		return []any{types.KindMysqlTime, rawOfTime(t)}
	case "dur":
		a := v.([]any)
		n, _ := strconv.ParseInt(a[0].(string), 10, 64)
		return []any{types.KindMysqlDuration, n, a[1].(int)}
	case "json":
		j, err := types.ParseBinaryJSONFromString(v.(string))
		chunkMust(err)
		return []any{types.KindMysqlJSON, j.TypeCode, hex.EncodeToString(j.Value)}
	case "vec":
		vv, _, err := types.ZeroCopyDeserializeVectorFloat32(chunkVecBytes(v.([]uint32)))
		chunkMust(err)
		return []any{types.KindVectorFloat32, vv.String()}
	case "nv":
		a := v.([]any)
		name := vec.GoString(chunkHex(a[0].(string)))
		if ft.GetType() == mysql.TypeEnum {
			return []any{types.KindMysqlEnum, a[1], name, ft.GetCollate()}
		}
		return []any{types.KindMysqlSet, a[1], name, ft.GetCollate()}
	case "null":
		return []any{types.KindNull}
	}
	b := v.(string)
	switch {
	case ft.GetType() == mysql.TypeBit:
		return []any{types.KindMysqlBit, b}
	case alt:
		return []any{types.KindBytes, b}
	}
	return []any{types.KindString, vec.GoString(chunkHex(b)), ft.GetCollate()}
}

// chunkGoValueOf converts a generator value to a goValueIn spec.
func chunkGoValueOf(ft *types.FieldType, v any, alt bool) []any {
	if v == nil {
		return []any{"nil"}
	}
	switch chunkKind(ft) {
	case "i64":
		n, _ := strconv.ParseInt(v.(string), 10, 64)
		if alt {
			return []any{"int", n}
		}
		return []any{"int64", n}
	case "u64":
		n, _ := strconv.ParseUint(v.(string), 10, 64)
		return []any{"uint64", n}
	case "f32":
		b, _ := strconv.ParseUint(v.(string), 10, 32)
		return []any{"float32", math.Float64bits(float64(math.Float32frombits(uint32(b))))}
	case "f64":
		b, _ := strconv.ParseUint(v.(string), 10, 64)
		return []any{"float64", b}
	case "dec":
		return []any{"decimal", v.(string)}
	case "time":
		d := chunkDatumOf(ft, v, false)
		return []any{"time", d[1]}
	case "dur":
		d := chunkDatumOf(ft, v, false)
		return []any{"duration", d[1], d[2]}
	case "json":
		d := chunkDatumOf(ft, v, false)
		return []any{"json", d[1], d[2]}
	case "vec":
		d := chunkDatumOf(ft, v, false)
		return []any{"vector", d[1]}
	case "nv":
		a := v.([]any)
		name := vec.GoString(chunkHex(a[0].(string)))
		if ft.GetType() == mysql.TypeEnum {
			return []any{"enum", a[1], name}
		}
		return []any{"set", a[1], name}
	case "null":
		return []any{"nil"}
	}
	b := v.(string)
	switch {
	case ft.GetType() == mysql.TypeBit:
		return []any{"binlit", b}
	case alt:
		return []any{"bytes", b}
	}
	return []any{"string", vec.GoString(chunkHex(b))}
}

// chunkFtArgs is the d2FtIn spec of a chunk type, with an explicit decimal.
func chunkFtArgs(s chunkTypeSpec, decimal int) []any {
	return []any{s.Tp, s.Flag, s.Flen, decimal, "", s.Collate, nil}
}

// chunkDatumScript builds one script over a chunk and MutRows.
func chunkDatumScript(rng *rand.Rand, nsteps int) [][]any {
	g := &chunkGen{rng: rng, st: newChunkState(), specs: map[int][]chunkTypeSpec{}, opaque: map[int]bool{}}
	id := g.newChunk()
	g.fill(id, g.intn(8))
	specs := g.specs[id]
	fts := g.st.fts[id]
	nc := len(specs)
	val := func(col int) any { return g.value(fts[col]) }
	datum := func(col int) []any { return chunkDatumOf(fts[col], val(col), g.intn(5) == 0) }
	other := func() []any {
		// A datum of a random type (ConvertTo is not called: the cell is written as is).
		s := chunkTypes[g.intn(len(chunkTypes))]
		return chunkDatumOf(s.ft(), g.value(s.ft()), false)
	}
	mrs := 0
	newMR := func() int {
		m := mrs
		mrs++
		switch g.intn(3) {
		case 0:
			g.step("mr_types", m, specs)
		case 1:
			var vals [][]any
			for col := range nc {
				vals = append(vals, chunkGoValueOf(fts[col], val(col), g.intn(4) == 0))
			}
			g.step("mr_values", m, vals)
		default:
			var ds [][]any
			for col := range nc {
				ds = append(ds, datum(col))
			}
			g.step("mr_datums", m, ds)
		}
		return m
	}
	m := newMR()
	shallow := false
	for range nsteps {
		if g.dead {
			break
		}
		nr := g.st.chks[id].NumRows()
		switch g.intn(20) {
		case 0, 1, 2:
			if shallow {
				continue
			}
			// Only datums of the column's type: AppendDatum of a wider fixed-size kind
			// (a duration into a FLOAT column) stores 8 bytes into a 4-byte elemBuf in Go
			// (memory corruption, D01.7 spec §4).
			col := g.intn(nc)
			d := datum(col)
			for c := range nc {
				// A row is complete when every column got a value.
				dd := d
				if c != col {
					dd = datum(c)
				}
				g.step("app_datum", id, c, dd)
			}
		case 3, 4:
			if nr > 0 {
				col := g.intn(nc)
				var ft any
				if g.intn(3) == 0 {
					ft = chunkFtArgs(specs[col], []int{-1, 0, 2, 6, 30}[g.intn(5)])
				}
				g.step("get_datum", id, g.intn(nr), col, ft)
			}
		case 5:
			if nr > 0 {
				g.step("datum_row", id, g.intn(nr))
			}
		case 6:
			if nr > 0 {
				var buf [][]any
				for range nc {
					buf = append(buf, other())
				}
				g.step("datum_row_buf", id, g.intn(nr), buf)
			}
		case 7:
			g.step("to_string", id)
		case 8:
			if nr > 0 {
				g.step("row_string", id, g.intn(nr))
			}
		case 9, 10:
			if nr > 0 {
				col := g.intn(nc)
				d := datum(col)
				switch g.intn(8) {
				case 0:
					d = []any{types.KindMinNotNull}
				case 1:
					d = []any{types.KindMaxValue}
				case 2:
					d = []any{types.KindNull}
				}
				g.step("cmp_datum", id, g.intn(nr), col, d)
			}
		case 11:
			if nr > 0 || g.intn(10) == 0 {
				col := g.intn(nc)
				if g.intn(2) == 0 {
					g.step("lower_bound", id, col, datum(col))
				} else {
					g.step("upper_bound", id, col, datum(col))
				}
			}
		case 12:
			if nr > 0 && !shallow {
				g.step("mr_set_row", m, id, g.intn(nr))
			}
		case 13:
			if !shallow {
				col := g.intn(nc)
				v := chunkGoValueOf(fts[col], val(col), g.intn(4) == 0)
				g.step("mr_set_value", m, col, v)
			}
		case 14:
			if !shallow {
				col := g.intn(nc)
				d := datum(col)
				switch g.intn(10) {
				case 0:
					d = other()
				case 1:
					d = []any{[]byte{types.KindMinNotNull, types.KindMaxValue, types.KindNull}[g.intn(3)]}
				}
				g.step("mr_set_datum", m, col, d)
			}
		case 15:
			if !shallow {
				if g.intn(2) == 0 {
					var vals [][]any
					for col := range nc {
						vals = append(vals, chunkGoValueOf(fts[col], val(col), false))
					}
					g.step("mr_set_values", m, vals)
				} else {
					var ds [][]any
					for col := range nc {
						ds = append(ds, datum(col))
					}
					g.step("mr_set_datums", m, ds)
				}
			}
		case 16:
			if nr > 0 && g.intn(3) == 0 && !shallow {
				// After this step the MutRow's cells alias the chunk in Go: only reads.
				m2 := newMR()
				if g.step("mr_shallow", m2, 0, id, g.intn(nr)) {
					m = m2
					shallow = true
				}
			}
		case 17:
			m2 := mrs
			mrs++
			g.step("mr_clone", m2, m)
			if !shallow {
				m = m2
			}
		case 18:
			g.step("mr_dump", m)
		default:
			col := g.intn(nc)
			g.step("mr_get", m, col, []chunkTypeSpec{specs[col]})
			g.step("mr_string", m, specs)
		}
	}
	g.step("mr_dump", m)
	g.step("dump", id)
	return g.steps
}

// chunkDatumMinedScripts: TestMutRow, TestIssue29947, TestMutRowShallowCopyPartialRow
// (up to the writes through the alias), TestGetDecimalDatum, TestToString and
// TestCompare's datum side.
func chunkDatumMinedScripts(g *vec.Gen) {
	s := func(a ...any) []any { return a }
	script := func(src string, steps ...[]any) { g.Add(src, "script", steps) }
	var all []chunkTypeSpec
	for _, t := range chunkTypes {
		if t.Tp != mysql.TypeNull {
			all = append(all, t)
		}
	}
	nul := []any{types.KindNull}
	var nils [][]any
	var gets [][]any
	for i := range all {
		nils = append(nils, s("mr_set_datum", 0, i, nul))
		gets = append(gets, s("mr_get", 0, i, []chunkTypeSpec{all[i]}))
	}
	steps := [][]any{s("mr_types", 0, all), s("mr_dump", 0)}
	steps = append(steps, gets...)
	steps = append(steps, s("mr_values", 1, [][]any{{"string", "abc"}, {"int", 123}}), s("mr_dump", 1),
		s("mr_set_values", 1, [][]any{{"string", "abcd"}, {"int", 456}}), s("mr_dump", 1),
		s("mr_set_datums", 1, [][]any{{types.KindString, "defgh", "utf8mb4_bin"}, {types.KindInt64, 33}}), s("mr_dump", 1),
		s("mr_values", 2, [][]any{{"string", "foobar"}, {"nil"}}), s("new", 9, []chunkTypeSpec{chunkTypes[11], chunkTypes[0]}, 1, 1),
		s("app", 9, 0, hex.EncodeToString([]byte("foobar"))), s("app", 9, 1, nil), s("mr_set_row", 1, 9, 0), s("mr_dump", 1),
		s("mr_values", 3, [][]any{{"json", 4, "01"}, {"time", rawOfTime(types.NewTime(types.FromDate(2000, 1, 1, 1, 0, 0, 0), mysql.TypeDatetime, 6))}}),
		s("mr_dump", 3),
		s("mr_types", 4, []chunkTypeSpec{chunkTypes[10]}), s("mr_set_value", 4, 0, []any{"duration", int64(5025000000000), 0}),
		s("mr_dump", 4), s("mr_set_datum", 4, 0, []any{types.KindMysqlDuration, int64(5025000000000), 0}), s("mr_dump", 4))
	script("TestMutRow", steps...)
	script("TestIssue29947", append(append([][]any{s("mr_types", 0, all), s("mr_dump", 0)}, nils...), s("mr_dump", 0))...)
	vs := chunkTypeSpec{mysql.TypeVarString, 0, "utf8mb4_bin", 20}
	ts := chunkTypes[9]
	script("TestMutRowShallowCopyPartialRow", s("mr_types", 0, []chunkTypeSpec{vs, chunkTypes[0], ts}),
		s("new", 1, []chunkTypeSpec{vs, chunkTypes[0], ts}, 1, 1), s("app", 1, 0, hex.EncodeToString([]byte("abc"))),
		s("app", 1, 1, "123"), s("app", 1, 2, []any{"0", mysql.TypeTimestamp, 0}), s("mr_shallow", 0, 0, 1, 0),
		s("mr_dump", 0), s("mr_string", 0, []chunkTypeSpec{vs, chunkTypes[0], ts}))
	dec42 := chunkTypeSpec{mysql.TypeNewDecimal, 0, "binary", 4}
	script("TestGetDecimalDatum", s("new", 0, []chunkTypeSpec{dec42}, 32, 32), s("app", 0, 0, "1.01"),
		s("get_datum", 0, 0, 0, chunkFtArgs(dec42, 2)), s("get_datum", 0, 0, 0, nil))
	script("TestToString", s("new", 0, []chunkTypeSpec{chunkTypes[4], chunkTypes[5], chunkTypes[13], chunkTypes[8], chunkTypes[0]}, 2, 2),
		s("app", 0, 0, strconv.Itoa(int(math.Float32bits(1)))), s("app", 0, 1, strconv.FormatUint(math.Float64bits(1), 10)),
		s("app", 0, 2, "31"), s("app", 0, 3, []any{"0", mysql.TypeDate, 0}), s("app", 0, 4, "1"),
		s("app", 0, 0, strconv.Itoa(int(math.Float32bits(2)))), s("app", 0, 1, strconv.FormatUint(math.Float64bits(2), 10)),
		s("app", 0, 2, "32"), s("app", 0, 3, []any{"0", mysql.TypeDatetime, 0}), s("app", 0, 4, "2"),
		s("to_string", 0), s("row_string", 0, 1))
}

// ChunkDatumMinedTests lists the Go tests chunkDatumMinedScripts reproduces.
func ChunkDatumMinedTests() []string {
	return []string{"TestMutRow", "TestIssue29947", "TestMutRowShallowCopyPartialRow", "TestGetDecimalDatum", "TestToString"}
}
