package domains

// Domain "aggfuncs": block D12.4 part A, the core aggregate functions of
// pkg/executor/aggfuncs (tidb-rs specs/D12.4-aggfuncs §6.2): Build and the COUNT, SUM, AVG,
// FIRST_ROW, MAX / MIN and BIT_* implementations, run through the exported AggFunc
// interface. The names of the later parts of the block (GROUP_CONCAT, VAR / STDDEV,
// JSON_*AGG, APPROX_*) appear only in "build" records; tidb-rs returns a temporary
// "blocked" for them until those parts are ported, counted by its replay.
//
// A record carries the complete descriptor, so tidb-rs builds the AggFuncDesc directly and
// does not run the type inference of D08.11 (covered by the aggdesc domain):
//
//	desc = {"name", "mode": 0..4 (CompleteMode, FinalMode, Partial1Mode, Partial2Mode,
//	        DedupMode), "distinct": b, "args": [type...] (column i of the input chunks),
//	        "ret": type, "hp": tidb_window_use_high_precision, "dpi": div_precision_increment}
//	type = [tp, flag, flen, decimal, charset, collate, elems]
//
// The generator draws a descriptor with NewAggFuncDesc and WrapCastForAggArgs over one
// column, as the planner does, and replaces each cast it wraps by a column of the cast's
// type (the input chunks then hold values of that type); the final descriptors come from
// Split([0, 1]).
//
// Ops (a batch is a list of rows, each a list of values in the chunk domain's encoding,
// chunkAppend; each batch is one UpdatePartialResult call):
//   - "build": [desc] -> {"type": %T of Build's result ("<nil>" for nil)}.
//   - "update_final": [desc, batches] -> AllocPartialResult, UpdatePartialResult for each
//     batch, AppendFinalResult2Chunk: {"type", "cells", "step"}.
//   - "reset_reuse": [desc, groups] -> one partial result for every group (a list of
//     batches), as the stream aggregation uses it: updates, AppendFinalResult2Chunk,
//     ResetPartialResult: {"type", "groups": [cells...], "step"}.
//   - "merge": [partial desc, final desc, parts, reuse] -> for each part (a list of batches)
//     a fresh partial result of the partial function, its updates and its
//     AppendFinalResult2Chunk, then the final function's MergePartialResult into one partial
//     result of its own, whose AppendFinalResult2Chunk ends the record. With reuse, the whole
//     round runs twice and the final partial result is reset in between (the stale state of
//     sum4Decimal's reset, func_sum.go:202-205): {"type": [partial, final], "partials":
//     [cells...], "first": cells|null, "final": cells, "step"}.
//
// cells are the rows AppendFinalResult2Chunk appended to a chunk of the return type, each
// {"null": b, "raw": hex of Row.GetRaw}; step names the call that returned the record's
// error ("update i", "final", "merge i", "reset"). New collations are enabled (the cluster
// default).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/executor/aggfuncs"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/aggregation"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
)

const aggfuncsSeed = 20261125

func init() {
	vec.Register(&vec.Domain{
		Name: "aggfuncs",
		Seed: aggfuncsSeed,
		Errors: []vec.NamedErr{{Name: "ErrOverflow", Err: types.ErrOverflow}, {Name: "ErrTruncated", Err: types.ErrTruncated},
			{Name: "ErrBadNumber", Err: types.ErrBadNumber}},
		Coverage: aggfuncsCoverage,
		Blocks:   []string{"D12.4"},
		Exempt:   aggfuncsExempt(),
		Gzip:     true,
		Generate: aggfuncsGenerate,
		Exec:     aggfuncsExec,
	})
}

// afMethods lists "<type>.<method>" for each type.
func afMethods(tys []string, methods ...string) []string {
	var out []string
	for _, t := range tys {
		for _, m := range methods {
			out = append(out, t+"."+m)
		}
	}
	return out
}

var (
	afFirstRowTypes = []string{"firstRow4Int", "firstRow4Float32", "firstRow4Float64", "firstRow4Decimal",
		"firstRow4Time", "firstRow4Duration", "firstRow4String", "firstRow4JSON", "firstRow4VectorFloat32",
		"firstRow4Enum", "firstRow4Set"}
	afMaxMinTypes = []string{"maxMin4Int", "maxMin4Uint", "maxMin4Float32", "maxMin4Float64", "maxMin4Decimal",
		"maxMin4String", "maxMin4Time", "maxMin4Duration", "maxMin4JSON", "maxMin4VectorFloat32",
		"maxMin4Enum", "maxMin4Set"}
	afCountOriginalTypes = []string{"countOriginal4Int", "countOriginal4Real", "countOriginal4Decimal",
		"countOriginal4Time", "countOriginal4Duration", "countOriginal4JSON", "countOriginal4VectorFloat32",
		"countOriginal4String"}
	afAllocResetTypes = append(append([]string{"baseCount", "baseSum4Float64", "sum4Decimal", "sum4DistinctFloat64",
		"sum4DistinctDecimal", "baseAvgDecimal", "avgOriginal4DistinctDecimal", "baseAvgFloat64",
		"avgOriginal4DistinctFloat64", "baseBitAggFunc", "bitAndUint64"}, afFirstRowTypes...), afMaxMinTypes...)
	afFinalTypes = append(append([]string{"baseCount", "baseSum4Float64", "sum4Decimal", "sum4DistinctFloat64",
		"sum4DistinctDecimal", "baseAvgDecimal", "avgOriginal4DistinctDecimal", "baseAvgFloat64",
		"avgOriginal4DistinctFloat64", "baseBitAggFunc"}, afFirstRowTypes...), afMaxMinTypes...)
	afUpdateTypes = append(append(append([]string{"countPartial", "baseSum4Float64", "sum4Decimal", "sum4DistinctFloat64", "sum4DistinctDecimal", "avgOriginal4Decimal", "avgPartial4Decimal",
		"avgOriginal4DistinctDecimal", "avgOriginal4Float64HighPrecision", "avgPartial4Float64", "avgOriginal4DistinctFloat64", "bitOrUint64", "bitXorUint64", "bitAndUint64"},
		afCountOriginalTypes...), afFirstRowTypes...), afMaxMinTypes...)
	afMergeTypes = append(append([]string{"countPartial", "baseSum4Float64", "sum4Decimal", "avgPartial4Decimal",
		"avgPartial4Float64", "bitOrUint64", "bitXorUint64", "bitAndUint64"}, afFirstRowTypes...), afMaxMinTypes...)
)

var aggfuncsCoverage = map[string]vec.Coverage{
	"build": {Min: 3000, GoFuncs: []string{"Build", "buildCount", "buildSum", "buildAvg", "buildFirstRow",
		"buildMaxMin", "buildBitOr", "buildBitXor", "buildBitAnd"}},
	"update_final": {Min: 2000, GoFuncs: append(append(afMethods(afAllocResetTypes, "AllocPartialResult"),
		afMethods(afUpdateTypes, "UpdatePartialResult")...), afMethods(afFinalTypes, "AppendFinalResult2Chunk")...)},
	"reset_reuse": {Min: 600, GoFuncs: afMethods(afAllocResetTypes, "ResetPartialResult")},
	"merge":       {Min: 800, GoFuncs: afMethods(afMergeTypes, "MergePartialResult")},
}

// afType is [tp, flag, flen, decimal, charset, collate, elems].
type afType struct {
	Tp      byte
	Flag    uint
	Flen    int
	Decimal int
	Charset string
	Collate string
	Elems   []string
}

func (t afType) MarshalJSON() ([]byte, error) {
	elems := t.Elems
	if elems == nil {
		elems = []string{}
	}
	return json.Marshal([]any{t.Tp, t.Flag, t.Flen, t.Decimal, t.Charset, t.Collate, elems})
}

func (t *afType) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	for i, p := range []any{&t.Tp, &t.Flag, &t.Flen, &t.Decimal, &t.Charset, &t.Collate, &t.Elems} {
		if err := json.Unmarshal(a[i], p); err != nil {
			return err
		}
	}
	return nil
}

func (t afType) ft() *types.FieldType {
	ft := types.NewFieldType(t.Tp)
	ft.SetFlag(t.Flag)
	ft.SetFlen(t.Flen)
	ft.SetDecimal(t.Decimal)
	ft.SetCharset(t.Charset)
	ft.SetCollate(t.Collate)
	ft.SetElems(t.Elems)
	return ft
}

func afTypeOf(ft *types.FieldType) afType {
	return afType{Tp: ft.GetType(), Flag: ft.GetFlag(), Flen: ft.GetFlen(), Decimal: ft.GetDecimal(),
		Charset: ft.GetCharset(), Collate: ft.GetCollate(), Elems: ft.GetElems()}
}

type afDesc struct {
	Name     string   `json:"name"`
	Mode     int      `json:"mode"`
	Distinct bool     `json:"distinct"`
	Args     []afType `json:"args"`
	Ret      afType   `json:"ret"`
	HP       bool     `json:"hp"`
	DPI      int      `json:"dpi"`
}

// make builds the context and the descriptor of d.
func (d afDesc) make() (*exprstatic.ExprContext, *aggregation.AggFuncDesc) {
	ctx := exprstatic.NewExprContext(
		exprstatic.WithEvalCtx(exprstatic.NewEvalContext(exprstatic.WithDivPrecisionIncrement(d.DPI))),
		exprstatic.WithWindowingUseHighPrecision(d.HP))
	args := make([]expression.Expression, len(d.Args))
	for i, a := range d.Args {
		args[i] = &expression.Column{RetType: a.ft(), Index: i}
	}
	desc := &aggregation.AggFuncDesc{Mode: aggregation.AggFunctionMode(d.Mode), HasDistinct: d.Distinct}
	desc.Name = d.Name
	desc.Args = args
	desc.RetTp = d.Ret.ft()
	return ctx, desc
}

func afDescOf(raw json.RawMessage) afDesc {
	var d afDesc
	must(json.Unmarshal(raw, &d))
	return d
}

// afDump encodes a built descriptor (its arguments are columns 0..n-1).
func afDump(ctx *exprstatic.ExprContext, desc *aggregation.AggFuncDesc, hp bool, dpi int) afDesc {
	d := afDesc{Name: desc.Name, Mode: int(desc.Mode), Distinct: desc.HasDistinct, Ret: afTypeOf(desc.RetTp), HP: hp, DPI: dpi}
	for i, a := range desc.Args {
		if c, ok := a.(*expression.Column); !ok || c.Index != i {
			panic(fmt.Sprintf("aggfuncs: argument %d of %s is not column %d", i, desc.Name, i))
		}
		d.Args = append(d.Args, afTypeOf(a.GetType(ctx.GetEvalCtx())))
	}
	return d
}

func afTypeName(f aggfuncs.AggFunc) string { return fmt.Sprintf("%T", f) }

type afBatches = [][][]json.RawMessage

func afChunk(fts []*types.FieldType, batch [][]json.RawMessage) []chunk.Row {
	c := chunk.NewChunkWithCapacity(fts, len(batch))
	for _, row := range batch {
		for col, v := range row {
			chunkAppend(c, fts[col], col, v, false)
		}
	}
	rows := make([]chunk.Row, c.NumRows())
	for i := range rows {
		rows[i] = c.GetRow(i)
	}
	return rows
}

func afUpdate(ctx *exprstatic.ExprContext, f aggfuncs.AggFunc, fts []*types.FieldType, batches afBatches,
	pr aggfuncs.PartialResult) (string, error) {
	for i, b := range batches {
		if _, err := f.UpdatePartialResult(ctx.GetEvalCtx(), afChunk(fts, b), pr); err != nil {
			return "update " + strconv.Itoa(i), err
		}
	}
	return "", nil
}

func afFinal(ctx *exprstatic.ExprContext, f aggfuncs.AggFunc, ret *types.FieldType, pr aggfuncs.PartialResult) ([]any, error) {
	c := chunk.NewChunkWithCapacity([]*types.FieldType{ret}, 1)
	err := f.AppendFinalResult2Chunk(ctx.GetEvalCtx(), pr, c)
	cells := make([]any, c.NumRows())
	for r := range cells {
		row := c.GetRow(r)
		cells[r] = map[string]any{"null": row.IsNull(0), "raw": hex.EncodeToString(row.GetRaw(0))}
	}
	return cells, err
}

func afFts(desc *aggregation.AggFuncDesc, ctx *exprstatic.ExprContext) []*types.FieldType {
	fts := make([]*types.FieldType, len(desc.Args))
	for i, a := range desc.Args {
		fts[i] = a.GetType(ctx.GetEvalCtx())
	}
	return fts
}

func aggfuncsExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	d := afDescOf(args[0])
	ctx, desc := d.make()
	f := aggfuncs.Build(ctx, desc, 0)
	if op == "build" {
		return map[string]any{"type": afTypeName(f)}, nil
	}
	fts := afFts(desc, ctx)
	switch op {
	case "update_final":
		var batches afBatches
		must(json.Unmarshal(args[1], &batches))
		pr, _ := f.AllocPartialResult()
		out := map[string]any{"type": afTypeName(f), "cells": nil, "step": ""}
		if step, err := afUpdate(ctx, f, fts, batches, pr); err != nil {
			out["step"] = step
			return out, err
		}
		cells, err := afFinal(ctx, f, desc.RetTp, pr)
		out["cells"] = cells
		if err != nil {
			out["step"] = "final"
		}
		return out, err
	case "reset_reuse":
		var groups []afBatches
		must(json.Unmarshal(args[1], &groups))
		pr, _ := f.AllocPartialResult()
		outGroups := []any{}
		out := map[string]any{"type": afTypeName(f), "groups": outGroups, "step": ""}
		for gi, g := range groups {
			if step, err := afUpdate(ctx, f, fts, g, pr); err != nil {
				out["groups"], out["step"] = outGroups, fmt.Sprintf("group %d %s", gi, step)
				return out, err
			}
			cells, err := afFinal(ctx, f, desc.RetTp, pr)
			outGroups = append(outGroups, cells)
			if err != nil {
				out["groups"], out["step"] = outGroups, fmt.Sprintf("group %d final", gi)
				return out, err
			}
			f.ResetPartialResult(pr)
		}
		out["groups"] = outGroups
		return out, nil
	case "merge":
		fd := afDescOf(args[1])
		fctx, fdesc := fd.make()
		ff := aggfuncs.Build(fctx, fdesc, 0)
		var parts []afBatches
		must(json.Unmarshal(args[2], &parts))
		var reuse bool
		must(json.Unmarshal(args[3], &reuse))
		out := map[string]any{"type": []string{afTypeName(f), afTypeName(ff)}, "partials": []any{}, "first": nil,
			"final": nil, "step": ""}
		fpr, _ := ff.AllocPartialResult()
		rounds := 1
		if reuse {
			rounds = 2
		}
		partials := []any{}
		for round := range rounds {
			for i, part := range parts {
				pr, _ := f.AllocPartialResult()
				if step, err := afUpdate(ctx, f, fts, part, pr); err != nil {
					out["partials"], out["step"] = partials, fmt.Sprintf("round %d part %d %s", round, i, step)
					return out, err
				}
				cells, err := afFinal(ctx, f, desc.RetTp, pr)
				partials = append(partials, cells)
				if err != nil {
					out["partials"], out["step"] = partials, fmt.Sprintf("round %d part %d final", round, i)
					return out, err
				}
				if _, err := ff.MergePartialResult(fctx.GetEvalCtx(), pr, fpr); err != nil {
					out["partials"], out["step"] = partials, fmt.Sprintf("round %d merge %d", round, i)
					return out, err
				}
			}
			cells, err := afFinal(fctx, ff, fdesc.RetTp, fpr)
			out["partials"] = partials
			if round+1 < rounds {
				out["first"] = cells
				if err != nil {
					out["step"] = fmt.Sprintf("round %d final", round)
					return out, err
				}
				ff.ResetPartialResult(fpr)
				continue
			}
			out["final"] = cells
			if err != nil {
				out["step"] = fmt.Sprintf("round %d final", round)
			}
			return out, err
		}
	}
	return nil, fmt.Errorf("aggfuncs: unknown op %s", op)
}

// ---- generator ---------------------------------------------------------------

var afPartANames = []string{ast.AggFuncCount, ast.AggFuncSum, ast.AggFuncAvg, ast.AggFuncFirstRow, ast.AggFuncMax,
	ast.AggFuncMin, ast.AggFuncBitOr, ast.AggFuncBitXor, ast.AggFuncBitAnd}

// afLaterNames: the names of the parts B1 and B2 (build records only).
var afLaterNames = []string{ast.AggFuncGroupConcat, ast.AggFuncVarPop, ast.AggFuncStddevPop, ast.AggFuncJsonArrayagg,
	ast.AggFuncJsonObjectAgg, ast.AggFuncApproxCountDistinct, ast.AggFuncApproxPercentile, ast.AggFuncVarSamp,
	ast.AggFuncStddevSamp}

var afElems = []string{"a", "B", "c", "b", "é"}

// afArgTypes: the argument types the generator draws from (every evaluation type, BIT,
// ENUM / SET (also with EnumSetAsIntFlag), UNSIGNED, FLOAT and DOUBLE, several decimals,
// fsps and collations).
var afArgTypes = func() []afType {
	bin, b := charset.CharsetBin, charset.CollationBin
	u8, u8b, u8g, u8u := charset.CharsetUTF8MB4, "utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci"
	return []afType{
		{Tp: mysql.TypeLonglong, Flen: 20, Charset: bin, Collate: b},
		{Tp: mysql.TypeLonglong, Flag: mysql.UnsignedFlag, Flen: 20, Charset: bin, Collate: b},
		{Tp: mysql.TypeTiny, Flen: 4, Charset: bin, Collate: b},
		{Tp: mysql.TypeLong, Flag: mysql.UnsignedFlag, Flen: 10, Charset: bin, Collate: b},
		{Tp: mysql.TypeYear, Flag: mysql.UnsignedFlag | mysql.ZerofillFlag, Flen: 4, Charset: bin, Collate: b},
		{Tp: mysql.TypeFloat, Flen: 12, Decimal: -1, Charset: bin, Collate: b},
		{Tp: mysql.TypeDouble, Flen: 22, Decimal: -1, Charset: bin, Collate: b},
		{Tp: mysql.TypeNewDecimal, Flen: 10, Decimal: 0, Charset: bin, Collate: b},
		{Tp: mysql.TypeNewDecimal, Flen: 20, Decimal: 5, Charset: bin, Collate: b},
		{Tp: mysql.TypeNewDecimal, Flen: 65, Decimal: 30, Charset: bin, Collate: b},
		{Tp: mysql.TypeDatetime, Flen: 26, Decimal: 6, Charset: bin, Collate: b},
		{Tp: mysql.TypeDate, Flen: 10, Charset: bin, Collate: b},
		{Tp: mysql.TypeTimestamp, Flen: 19, Charset: bin, Collate: b},
		{Tp: mysql.TypeDuration, Flen: 10, Decimal: 0, Charset: bin, Collate: b},
		{Tp: mysql.TypeDuration, Flen: 13, Decimal: 3, Charset: bin, Collate: b},
		{Tp: mysql.TypeVarchar, Flen: 20, Charset: u8, Collate: u8b},
		{Tp: mysql.TypeVarchar, Flen: 255, Charset: u8, Collate: u8g},
		{Tp: mysql.TypeString, Flen: 20, Charset: u8, Collate: u8u},
		{Tp: mysql.TypeBlob, Flen: 65535, Charset: bin, Collate: b, Flag: mysql.BinaryFlag},
		{Tp: mysql.TypeJSON, Flen: 4294967295, Charset: bin, Collate: b, Flag: mysql.BinaryFlag},
		{Tp: mysql.TypeTiDBVectorFloat32, Flen: -1, Charset: bin, Collate: b},
		{Tp: mysql.TypeEnum, Flen: 1, Charset: u8, Collate: u8b, Elems: afElems},
		{Tp: mysql.TypeEnum, Flen: 1, Charset: u8, Collate: u8g, Elems: afElems},
		{Tp: mysql.TypeEnum, Flag: mysql.EnumSetAsIntFlag, Flen: 1, Charset: u8, Collate: u8b, Elems: afElems},
		{Tp: mysql.TypeSet, Flen: 9, Charset: u8, Collate: u8b, Elems: afElems},
		{Tp: mysql.TypeSet, Flag: mysql.EnumSetAsIntFlag, Flen: 9, Charset: u8, Collate: u8g, Elems: afElems},
		{Tp: mysql.TypeBit, Flag: mysql.UnsignedFlag, Flen: 8, Charset: bin, Collate: b},
		{Tp: mysql.TypeNull, Flen: 0, Charset: bin, Collate: b},
	}
}()

type afGen struct {
	rng  *rand.Rand
	cg   *chunkGen
	pool map[int][]any
	// noNaN: no NaN inputs (the records of SUM and AVG). Which NaN an addition of two NaNs
	// returns depends on the order of its operands in the machine code, which Rust's
	// compiler may swap (fadd is commutative to LLVM); Go never creates NaN data (NaN is
	// rejected when written), so the sums meet NaN only as the default NaN of Inf - Inf,
	// which needs no second NaN. tidb-rs spec D12.4 §7.
	noNaN bool
}

// start resets the pools and the NaN rule for the records of descriptor d.
func (g *afGen) start(d *afDesc) {
	g.pool = map[int][]any{}
	g.noNaN = d.Name == ast.AggFuncSum || d.Name == ast.AggFuncAvg
}

func (g *afGen) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return g.rng.IntN(n)
}

// afDecStrings: boundaries of DECIMAL (carries, 65 digits, -0, fractions past 30 digits).
var afDecStrings = []string{"0", "1", "-1", "0.5", "-0.5", "-0.0", "0.000000001", "123456789.987654321",
	"99999999999999999999999999999999999.999999999999999999999999999999",
	"-99999999999999999999999999999999999.999999999999999999999999999999",
	"99999999999999999999999999999999999999999999999999999999999999999",
	"0.1234567890123456789012345678901234567890", "1.5", "2.5", "-2.5", "0.0001",
}

// afStrings: strings equal in some collations ("a", "A", "a "), not UTF-8, empty.
var afStrings = []string{"", "a", "A", "a ", "b", "é", "e", "ff", "\xff", "abc", "ABC", "z\x00"}

// value draws a value of type t: NULL a seventh of the time, a value of the column's pool
// half of the time (ties, so that DISTINCT and the comparisons see equal values).
func (g *afGen) value(col int, t afType) any {
	if g.intn(7) == 0 {
		return nil
	}
	if g.intn(2) == 0 {
		p := g.pool[col]
		if p == nil {
			p = make([]any, 3)
			for i := range p {
				p[i] = g.fresh(t)
			}
			g.pool[col] = p
		}
		return p[g.intn(len(p))]
	}
	return g.fresh(t)
}

func (g *afGen) fresh(t afType) any {
	ft := t.ft()
	switch chunkKind(ft) {
	case "i64":
		return strconv.FormatInt([]int64{0, 1, -1, 2, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1,
			g.rng.Int64() - math.MaxInt64/2, int64(g.intn(100)) - 50}[g.intn(9)], 10)
	case "u64":
		return strconv.FormatUint([]uint64{0, 1, 2, math.MaxUint64, 1 << 63, math.MaxInt64, g.rng.Uint64(),
			uint64(g.intn(100))}[g.intn(8)], 10)
	case "f32":
		bits := []uint32{0, 0x80000000, 0x7f800000, 0xff800000, 0x7fc00000, math.Float32bits(1.5),
			math.Float32bits(-2.25), math.Float32bits(3.4e38), g.rng.Uint32() &^ 0x40000000}[g.intn(9)]
		for g.noNaN && math.IsNaN(float64(math.Float32frombits(bits))) {
			bits = math.Float32bits(float32(g.intn(1000)) / 4)
		}
		return strconv.FormatUint(uint64(bits), 10)
	case "f64":
		bits := []uint64{0, 1 << 63, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001,
			math.Float64bits(-2.25), math.Float64bits(0.1), math.Float64bits(1.7e308), math.Float64bits(1e16),
			math.Float64bits(float64(g.intn(1000)) / 8)}[g.intn(10)]
		if g.noNaN && math.IsNaN(math.Float64frombits(bits)) {
			bits = math.Float64bits(float64(g.intn(1000)) / 8)
		}
		return strconv.FormatUint(bits, 10)
	case "dec":
		if g.intn(3) == 0 {
			return strconv.FormatFloat((g.rng.Float64()-0.5)*1e6, 'f', g.intn(8), 64)
		}
		return afDecStrings[g.intn(len(afDecStrings))]
	case "nv":
		n := len(t.Elems)
		if t.Tp == mysql.TypeEnum {
			v := g.intn(n + 1)
			name := ""
			if v > 0 {
				name = t.Elems[v-1]
			}
			return []any{hex.EncodeToString([]byte(name)), v}
		}
		v := g.intn(1 << n)
		var names []string
		for i := range n {
			if v&(1<<i) != 0 {
				names = append(names, t.Elems[i])
			}
		}
		return []any{hex.EncodeToString([]byte(strings.Join(names, ","))), v}
	case "bytes":
		if t.Tp == mysql.TypeBit {
			b := make([]byte, 1)
			b[0] = byte(g.intn(256))
			return hex.EncodeToString(b)
		}
		if g.intn(2) == 0 {
			return hex.EncodeToString([]byte(afStrings[g.intn(len(afStrings))]))
		}
	case "time":
		v := g.cg.value(ft)
		for v == nil {
			v = g.cg.value(ft)
		}
		if g.intn(10) == 0 {
			a := v.([]any)
			a[0] = "0" // the zero date
		}
		return v
	}
	v := g.cg.value(ft)
	for v == nil && chunkKind(ft) != "null" {
		v = g.cg.value(ft)
	}
	return v
}

// batches draws the batches of the rows of one partial result: sometimes none, sometimes an
// empty batch, a single row, or only NULLs.
func (g *afGen) batches(ts []afType) [][][]any {
	out := [][][]any{}
	allNull := g.intn(10) == 0
	for range []int{0, 1, 1, 2, 3, 4}[g.intn(6)] {
		n := []int{0, 1, 1, 2, 3, 5, 8}[g.intn(7)]
		rows := make([][]any, n)
		for r := range rows {
			row := make([]any, len(ts))
			for c, t := range ts {
				if allNull {
					row[c] = nil
				} else {
					row[c] = g.value(c, t)
				}
			}
			rows[r] = row
		}
		out = append(out, rows)
	}
	return out
}

// infer builds the descriptor of name over one column of type at as the planner does
// (NewAggFuncDesc, WrapCastForAggArgs), each cast replaced by a column of its type; nil if Go
// rejects the combination.
func afInfer(name string, at afType, distinct bool, hp bool, dpi int) (d *afDesc, split [2]*afDesc) {
	defer func() {
		if recover() != nil {
			d = nil
		}
	}()
	ctx := exprstatic.NewExprContext(
		exprstatic.WithEvalCtx(exprstatic.NewEvalContext(exprstatic.WithDivPrecisionIncrement(dpi))),
		exprstatic.WithWindowingUseHighPrecision(hp))
	col := &expression.Column{RetType: at.ft(), Index: 0, UniqueID: 1}
	desc, err := aggregation.NewAggFuncDesc(ctx, name, []expression.Expression{col}, distinct)
	if err != nil || desc.RetTp == nil {
		return nil, split
	}
	desc.WrapCastForAggArgs(ctx)
	for i, a := range desc.Args {
		if _, ok := a.(*expression.Column); !ok {
			desc.Args[i] = &expression.Column{RetType: a.GetType(ctx.GetEvalCtx()), Index: i}
		}
	}
	dd := afDump(ctx, desc, hp, dpi)
	p, f := desc.Split([]int{0, 1})
	pd, fd := afDump(ctx, p, hp, dpi), afDump(ctx, f, hp, dpi)
	return &dd, [2]*afDesc{&pd, &fd}
}

func afBuilds(d *afDesc) bool {
	ctx, desc := d.make()
	return aggfuncs.Build(ctx, desc, 0) != nil
}

func aggfuncsGenerate(vg *vec.Gen, rng *rand.Rand) {
	g := &afGen{rng: rng, cg: &chunkGen{rng: rng}}
	hps := []bool{true, false}
	dpis := []int{4, 0, 30}

	// build: every name x argument type x DISTINCT x high precision x mode.
	for _, name := range append(append([]string{}, afPartANames...), afLaterNames...) {
		for _, at := range afArgTypes {
			for _, distinct := range []bool{false, true} {
				for _, hp := range hps {
					d, _ := afInfer(name, at, distinct, hp, 4)
					if d == nil {
						continue
					}
					for mode := range 5 {
						dm := *d
						dm.Mode = mode
						vg.Add("build", "build", dm)
					}
				}
			}
		}
	}

	type cand struct{ d, p, f *afDesc }
	var cands []cand
	for _, name := range afPartANames {
		for _, at := range afArgTypes {
			for _, distinct := range []bool{false, true} {
				for _, hp := range hps {
					dpi := 4
					if name == ast.AggFuncAvg {
						dpi = dpis[g.intn(len(dpis))]
					}
					d, s := afInfer(name, at, distinct, hp, dpi)
					if d == nil {
						continue
					}
					cands = append(cands, cand{d, s[0], s[1]})
				}
			}
		}
	}

	// update_final: the complete, partial-1 and final descriptors of every candidate.
	for range 2 {
		for _, c := range cands {
			for _, d := range []*afDesc{c.d, c.p, c.f} {
				if !afBuilds(d) {
					continue
				}
				g.start(d)
				vg.Add("update_final", "update_final", d, g.batches(d.Args))
			}
		}
	}

	// reset_reuse: 2 to 4 groups over one partial result.
	for _, c := range cands {
		for _, d := range []*afDesc{c.d, c.f} {
			if !afBuilds(d) || g.intn(3) == 0 {
				continue
			}
			g.start(d)
			groups := [][][][]any{}
			for range 2 + g.intn(3) {
				groups = append(groups, g.batches(d.Args))
			}
			vg.Add("reset_reuse", "reset_reuse", d, groups)
		}
	}

	// merge: the split pair of every candidate without DISTINCT (the planner splits only
	// those), 0 to 4 parts, with and without the reuse of the final partial result.
	for range 3 {
		for _, c := range cands {
			if c.d.Distinct || !afBuilds(c.p) || !afBuilds(c.f) {
				continue
			}
			g.start(c.p)
			parts := [][][][]any{}
			for range []int{0, 1, 2, 2, 3, 4}[g.intn(6)] {
				parts = append(parts, g.batches(c.p.Args))
			}
			vg.Add("merge", "merge", c.p, c.f, parts, g.intn(3) == 0)
		}
	}
}
