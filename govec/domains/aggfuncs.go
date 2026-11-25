package domains

// Domain "aggfuncs": block D12.4 parts A and B1, the core and common aggregate functions of
// pkg/executor/aggfuncs (tidb-rs specs/D12.4-aggfuncs §6.2): Build, COUNT (including
// DISTINCT and multi-arg encoding), SUM, AVG, FIRST_ROW, MAX / MIN, BIT_*, GROUP_CONCAT,
// VAR_POP / VAR_SAMP, STDDEV_POP / STDDEV_SAMP, JSON_ARRAYAGG, and JSON_OBJECTAGG, run
// through the exported AggFunc interface. The names of part B2 (APPROX_COUNT_DISTINCT,
// APPROX_PERCENTILE) appear only in "build" records; tidb-rs returns a temporary "blocked"
// for them until part B2 is ported, counted by its replay.
//
// A record carries the complete descriptor, so tidb-rs builds the AggFuncDesc directly and
// does not run the type inference of D08.11 (covered by the aggdesc domain):
//
//	desc = {"name", "mode": 0..4 (CompleteMode, FinalMode, Partial1Mode, Partial2Mode,
//	        DedupMode), "distinct": b, "args": [type...] (column i of the input chunks),
//	        "ret": type, "hp": tidb_window_use_high_precision, "dpi": div_precision_increment,
//	        optional GROUP_CONCAT fields: "has_sep", "sep" (hex), "max_len", "trunc_warn",
//	        "order_by": [{"col", "desc"}], "extra_cols": [type...]}
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
//   - "group_concat": [action, ...] -> "run" (multi-group update/final/reset + warnings +
//     GetTruncated/SetTruncated check), "merge" (partial + final with shared truncated
//     sentinel), "merge_err" (ORDER BY MergePartialResult -> ErrInternal).
//   - "count_distinct_encode": [action, ...] -> "run" (multi-arg and single-arg COUNT(DISTINCT)
//     encoding across all eval types and §8 item 3 edge cases), "write_time" (16-byte raw
//     WriteTime output).
//   - "json_agg": [action, ...] -> "update_final", "reset_reuse", "merge" for JSON_ARRAYAGG
//     and JSON_OBJECTAGG across all value types, duplicate keys, and error paths.
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
	"github.com/pingcap/tidb/pkg/planner/util"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/dbterror/plannererrors"
)

const aggfuncsSeed = 20261125

func init() {
	vec.Register(&vec.Domain{
		Name: "aggfuncs",
		Seed: aggfuncsSeed,
		Errors: []vec.NamedErr{
			{Name: "ErrOverflow", Err: types.ErrOverflow},
			{Name: "ErrTruncated", Err: types.ErrTruncated},
			{Name: "ErrBadNumber", Err: types.ErrBadNumber},
			{Name: "ErrJSONDocumentNULLKey", Err: types.ErrJSONDocumentNULLKey},
			{Name: "ErrInvalidJSONCharset", Err: types.ErrInvalidJSONCharset},
			{Name: "ErrUnsupportedSecondArgumentType", Err: types.ErrUnsupportedSecondArgumentType},
			{Name: "ErrCutValueGroupConcat", Err: expression.ErrCutValueGroupConcat},
			{Name: "ErrInternal", Err: plannererrors.ErrInternal},
		},
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
	afCountDistinctTypes = []string{"countOriginalWithDistinct4Int", "countOriginalWithDistinct4Real",
		"countOriginalWithDistinct4Decimal", "countOriginalWithDistinct4Duration",
		"countOriginalWithDistinct4String", "countOriginalWithDistinct"}
	afVarPopTypes = []string{"varPop4Float64", "varPop4DistinctFloat64"}
	afAllocResetTypes = append(append(append(append([]string{"baseCount", "baseSum4Float64", "sum4Decimal", "sum4DistinctFloat64",
		"sum4DistinctDecimal", "baseAvgDecimal", "avgOriginal4DistinctDecimal", "baseAvgFloat64",
		"avgOriginal4DistinctFloat64", "baseBitAggFunc", "bitAndUint64"}, afFirstRowTypes...), afMaxMinTypes...),
		afCountDistinctTypes...), afVarPopTypes...)
	afFinalTypes = append(append(append(append(append([]string{"baseCount", "baseSum4Float64", "sum4Decimal", "sum4DistinctFloat64",
		"sum4DistinctDecimal", "baseAvgDecimal", "avgOriginal4DistinctDecimal", "baseAvgFloat64",
		"avgOriginal4DistinctFloat64", "baseBitAggFunc"}, afFirstRowTypes...), afMaxMinTypes...),
		afCountDistinctTypes...), afVarPopTypes...),
		"varSamp4Float64", "varSamp4DistinctFloat64", "stdDevPop4Float64", "stdDevPop4DistinctFloat64",
		"stddevSamp4Float64", "stddevSamp4DistinctFloat64")
	afUpdateTypes = append(append(append(append(append([]string{"countPartial", "baseSum4Float64", "sum4Decimal", "sum4DistinctFloat64", "sum4DistinctDecimal", "avgOriginal4Decimal", "avgPartial4Decimal",
		"avgOriginal4DistinctDecimal", "avgOriginal4Float64HighPrecision", "avgPartial4Float64", "avgOriginal4DistinctFloat64", "bitOrUint64", "bitXorUint64", "bitAndUint64"},
		afCountOriginalTypes...), afFirstRowTypes...), afMaxMinTypes...), afCountDistinctTypes...), afVarPopTypes...)
	afMergeTypes = append(append([]string{"countPartial", "baseSum4Float64", "sum4Decimal", "avgPartial4Decimal",
		"avgPartial4Float64", "bitOrUint64", "bitXorUint64", "bitAndUint64", "varPop4Float64"}, afFirstRowTypes...), afMaxMinTypes...)
)

var aggfuncsCoverage = map[string]vec.Coverage{
	"build": {Min: 3000, GoFuncs: []string{"Build", "buildCount", "buildSum", "buildAvg", "buildFirstRow",
		"buildMaxMin", "buildBitOr", "buildBitXor", "buildBitAnd",
		"buildGroupConcat", "buildVarPop", "buildStdDevPop", "buildVarSamp", "buildStddevSamp",
		"buildJSONArrayagg", "buildJSONObjectAgg"}},
	"update_final": {Min: 2500, GoFuncs: append(append(append(afMethods(afAllocResetTypes, "AllocPartialResult"),
		afMethods(afUpdateTypes, "UpdatePartialResult")...), afMethods(afFinalTypes, "AppendFinalResult2Chunk")...),
		"calculateIntermediate")},
	"reset_reuse": {Min: 800, GoFuncs: afMethods(afAllocResetTypes, "ResetPartialResult")},
	"merge":       {Min: 900, GoFuncs: append(afMethods(afMergeTypes, "MergePartialResult"), "calculateMerge")},
	"group_concat": {Min: 80, GoFuncs: []string{
		"baseGroupConcat4String.AppendFinalResult2Chunk",
		"baseGroupConcat4String.handleTruncateError",
		"baseGroupConcat4String.truncatePartialResultIfNeed",
		"groupConcat.AllocPartialResult", "groupConcat.ResetPartialResult",
		"groupConcat.UpdatePartialResult", "groupConcat.MergePartialResult",
		"groupConcat.GetTruncated", "groupConcat.SetTruncated",
		"groupConcatDistinct.AllocPartialResult", "groupConcatDistinct.ResetPartialResult",
		"groupConcatDistinct.UpdatePartialResult", "groupConcatDistinct.GetTruncated",
		"groupConcatDistinct.SetTruncated",
		"groupConcatOrder.AllocPartialResult", "groupConcatOrder.ResetPartialResult",
		"groupConcatOrder.UpdatePartialResult", "groupConcatOrder.MergePartialResult",
		"groupConcatOrder.AppendFinalResult2Chunk", "groupConcatOrder.GetTruncated",
		"groupConcatOrder.SetTruncated",
		"groupConcatDistinctOrder.AllocPartialResult", "groupConcatDistinctOrder.ResetPartialResult",
		"groupConcatDistinctOrder.UpdatePartialResult", "groupConcatDistinctOrder.MergePartialResult",
		"groupConcatDistinctOrder.AppendFinalResult2Chunk",
		"topNRows.Len", "topNRows.Less", "topNRows.Swap", "topNRows.Push", "topNRows.Pop",
		"topNRows.tryToAdd", "topNRows.reset", "topNRows.concat",
	}},
	"count_distinct_encode": {Min: 40, GoFuncs: []string{
		"evalAndEncode", "appendInt64", "appendFloat64", "appendDecimal",
		"WriteTime", "appendTime", "appendDuration",
	}},
	"json_agg": {Min: 80, GoFuncs: []string{
		"jsonArrayagg.AllocPartialResult", "jsonArrayagg.ResetPartialResult",
		"jsonArrayagg.UpdatePartialResult", "jsonArrayagg.MergePartialResult",
		"jsonArrayagg.AppendFinalResult2Chunk",
		"jsonObjectAgg.AllocPartialResult", "jsonObjectAgg.ResetPartialResult",
		"jsonObjectAgg.UpdatePartialResult", "jsonObjectAgg.MergePartialResult",
		"jsonObjectAgg.AppendFinalResult2Chunk",
		"getRealJSONValue",
	}},
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

type afOrderBy struct {
	Col  int  `json:"col"`
	Desc bool `json:"desc"`
}

type afDesc struct {
	Name        string      `json:"name"`
	Mode        int         `json:"mode"`
	Distinct    bool        `json:"distinct"`
	Args        []afType    `json:"args"`
	Ret         afType      `json:"ret"`
	HP          bool        `json:"hp"`
	DPI         int         `json:"dpi"`
	HasSep      bool        `json:"has_sep,omitempty"`
	Sep         string      `json:"sep,omitempty"`
	MaxLen      uint64      `json:"max_len,omitempty"`
	TruncAsWarn bool        `json:"trunc_warn,omitempty"`
	OrderBy     []afOrderBy `json:"order_by,omitempty"`
	ExtraCols   []afType    `json:"extra_cols,omitempty"`
}

// make builds the context and the descriptor of d.
func (d afDesc) make() (*exprstatic.ExprContext, *aggregation.AggFuncDesc) {
	evalOpts := []exprstatic.EvalCtxOption{exprstatic.WithDivPrecisionIncrement(d.DPI)}
	if d.TruncAsWarn {
		evalOpts = append(evalOpts, exprstatic.WithTypeFlags(types.FlagTruncateAsWarning))
	}
	exprOpts := []exprstatic.ExprCtxOption{
		exprstatic.WithEvalCtx(exprstatic.NewEvalContext(evalOpts...)),
		exprstatic.WithWindowingUseHighPrecision(d.HP),
	}
	if d.MaxLen > 0 {
		exprOpts = append(exprOpts, exprstatic.WithGroupConcatMaxLen(d.MaxLen))
	}
	ctx := exprstatic.NewExprContext(exprOpts...)
	allCols := make([]afType, 0, len(d.Args)+len(d.ExtraCols))
	allCols = append(allCols, d.Args...)
	allCols = append(allCols, d.ExtraCols...)
	args := make([]expression.Expression, len(d.Args), len(d.Args)+1)
	for i, a := range d.Args {
		args[i] = &expression.Column{RetType: a.ft(), Index: i}
	}
	if d.HasSep {
		sepBytes, err := hex.DecodeString(d.Sep)
		must(err)
		args = append(args, &expression.Constant{
			Value:   types.NewStringDatum(string(sepBytes)),
			RetType: types.NewFieldType(mysql.TypeString),
		})
	}
	desc := &aggregation.AggFuncDesc{Mode: aggregation.AggFunctionMode(d.Mode), HasDistinct: d.Distinct}
	desc.Name = d.Name
	desc.Args = args
	desc.RetTp = d.Ret.ft()
	if len(d.OrderBy) > 0 {
		desc.OrderByItems = make([]*util.ByItems, len(d.OrderBy))
		for i, o := range d.OrderBy {
			desc.OrderByItems[i] = &util.ByItems{
				Expr: &expression.Column{RetType: allCols[o.Col].ft(), Index: o.Col},
				Desc: o.Desc,
			}
		}
	}
	return ctx, desc
}

func (d afDesc) chunkFts() []*types.FieldType {
	fts := make([]*types.FieldType, 0, len(d.Args)+len(d.ExtraCols))
	for _, a := range d.Args {
		fts = append(fts, a.ft())
	}
	for _, a := range d.ExtraCols {
		fts = append(fts, a.ft())
	}
	return fts
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

type afTruncated interface {
	GetTruncated() *int32
	SetTruncated(*int32)
}

func afWarnings(ctx *exprstatic.ExprContext) []string {
	ws := ctx.GetEvalCtx().(*exprstatic.EvalContext).CopyWarnings(nil)
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Err.Error()
	}
	return out
}

func afExecUpdateFinal(d afDesc, batchesRaw json.RawMessage) (any, error) {
	ctx, desc := d.make()
	f := aggfuncs.Build(ctx, desc, 0)
	fts := d.chunkFts()
	var batches afBatches
	must(json.Unmarshal(batchesRaw, &batches))
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
}

func afExecResetReuse(d afDesc, groupsRaw json.RawMessage) (any, error) {
	ctx, desc := d.make()
	f := aggfuncs.Build(ctx, desc, 0)
	fts := d.chunkFts()
	var groups []afBatches
	must(json.Unmarshal(groupsRaw, &groups))
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
}

func afExecMerge(d afDesc, fd afDesc, partsRaw json.RawMessage, reuseRaw json.RawMessage) (any, error) {
	ctx, desc := d.make()
	f := aggfuncs.Build(ctx, desc, 0)
	fts := d.chunkFts()
	fctx, fdesc := fd.make()
	ff := aggfuncs.Build(fctx, fdesc, 0)
	var parts []afBatches
	must(json.Unmarshal(partsRaw, &parts))
	var reuse bool
	must(json.Unmarshal(reuseRaw, &reuse))
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
	return out, nil
}

func aggfuncsExec(op string, args []json.RawMessage) (any, error) {
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "build":
		d := afDescOf(args[0])
		ctx, desc := d.make()
		f := aggfuncs.Build(ctx, desc, 0)
		return map[string]any{"type": afTypeName(f)}, nil
	case "update_final":
		return afExecUpdateFinal(afDescOf(args[0]), args[1])
	case "reset_reuse":
		return afExecResetReuse(afDescOf(args[0]), args[1])
	case "merge":
		return afExecMerge(afDescOf(args[0]), afDescOf(args[1]), args[2], args[3])
	case "json_agg":
		var action string
		must(json.Unmarshal(args[0], &action))
		switch action {
		case "update_final":
			return afExecUpdateFinal(afDescOf(args[1]), args[2])
		case "reset_reuse":
			return afExecResetReuse(afDescOf(args[1]), args[2])
		case "merge":
			return afExecMerge(afDescOf(args[1]), afDescOf(args[2]), args[3], args[4])
		default:
			return nil, fmt.Errorf("aggfuncs json_agg: unknown action %s", action)
		}
	case "count_distinct_encode":
		var action string
		must(json.Unmarshal(args[0], &action))
		switch action {
		case "run":
			return afExecUpdateFinal(afDescOf(args[1]), args[2])
		case "write_time":
			var tv []json.RawMessage
			must(json.Unmarshal(args[1], &tv))
			var coreStr string
			var tp byte
			var fsp int
			must(json.Unmarshal(tv[0], &coreStr))
			must(json.Unmarshal(tv[1], &tp))
			must(json.Unmarshal(tv[2], &fsp))
			core, err := strconv.ParseUint(coreStr, 10, 64)
			must(err)
			t := types.NewTime(types.CoreTime(core), tp, fsp)
			buf := make([]byte, 16)
			for i := range buf {
				buf[i] = 0xff
			}
			aggfuncs.WriteTime(buf, t)
			return map[string]any{"raw": hex.EncodeToString(buf)}, nil
		default:
			return nil, fmt.Errorf("aggfuncs count_distinct_encode: unknown action %s", action)
		}
	case "group_concat":
		var action string
		must(json.Unmarshal(args[0], &action))
		switch action {
		case "run":
			d := afDescOf(args[1])
			ctx, desc := d.make()
			f := aggfuncs.Build(ctx, desc, 0)
			t, hasTrunc := f.(afTruncated)
			if hasTrunc {
				t.SetTruncated(t.GetTruncated())
			}
			fts := d.chunkFts()
			var groups []afBatches
			must(json.Unmarshal(args[2], &groups))
			pr, _ := f.AllocPartialResult()
			outGroups := []any{}
			out := map[string]any{"type": afTypeName(f), "has_trunc": hasTrunc, "groups": outGroups, "warnings": []string{}, "step": ""}
			for gi, g := range groups {
				if step, err := afUpdate(ctx, f, fts, g, pr); err != nil {
					out["groups"], out["warnings"], out["step"] = outGroups, afWarnings(ctx), fmt.Sprintf("group %d %s", gi, step)
					return out, err
				}
				cells, err := afFinal(ctx, f, desc.RetTp, pr)
				outGroups = append(outGroups, cells)
				if err != nil {
					out["groups"], out["warnings"], out["step"] = outGroups, afWarnings(ctx), fmt.Sprintf("group %d final", gi)
					return out, err
				}
				f.ResetPartialResult(pr)
			}
			out["groups"] = outGroups
			out["warnings"] = afWarnings(ctx)
			return out, nil
		case "merge":
			pd := afDescOf(args[1])
			fd := afDescOf(args[2])
			ctx, pdesc := pd.make()
			_, fdesc := fd.make()
			pf := aggfuncs.Build(ctx, pdesc, 0)
			ff := aggfuncs.Build(ctx, fdesc, 0)
			if pt, ok := pf.(afTruncated); ok {
				if ft, ok := ff.(afTruncated); ok {
					ft.SetTruncated(pt.GetTruncated())
				}
			}
			fts := pd.chunkFts()
			var parts []afBatches
			must(json.Unmarshal(args[3], &parts))
			out := map[string]any{"type": []string{afTypeName(pf), afTypeName(ff)}, "partials": []any{}, "final": nil, "warnings": []string{}, "step": ""}
			fpr, _ := ff.AllocPartialResult()
			partials := []any{}
			for i, part := range parts {
				pr, _ := pf.AllocPartialResult()
				if step, err := afUpdate(ctx, pf, fts, part, pr); err != nil {
					out["partials"], out["warnings"], out["step"] = partials, afWarnings(ctx), fmt.Sprintf("part %d %s", i, step)
					return out, err
				}
				cells, err := afFinal(ctx, pf, pdesc.RetTp, pr)
				partials = append(partials, cells)
				if err != nil {
					out["partials"], out["warnings"], out["step"] = partials, afWarnings(ctx), fmt.Sprintf("part %d final", i)
					return out, err
				}
				if _, err := ff.MergePartialResult(ctx.GetEvalCtx(), pr, fpr); err != nil {
					out["partials"], out["warnings"], out["step"] = partials, afWarnings(ctx), fmt.Sprintf("merge %d", i)
					return out, err
				}
			}
			cells, err := afFinal(ctx, ff, fdesc.RetTp, fpr)
			out["partials"] = partials
			out["final"] = cells
			out["warnings"] = afWarnings(ctx)
			if err != nil {
				out["step"] = "final"
			}
			return out, err
		case "merge_err":
			d := afDescOf(args[1])
			ctx, desc := d.make()
			f := aggfuncs.Build(ctx, desc, 0)
			pr1, _ := f.AllocPartialResult()
			pr2, _ := f.AllocPartialResult()
			_, err := f.MergePartialResult(ctx.GetEvalCtx(), pr1, pr2)
			return map[string]any{"type": afTypeName(f), "step": "merge"}, err
		default:
			return nil, fmt.Errorf("aggfuncs group_concat: unknown action %s", action)
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
	// noNaN: no NaN inputs (SUM, AVG, VAR_*, STDDEV_*). Which NaN an addition of two NaNs
	// returns depends on the order of its operands in the machine code, which Rust's
	// compiler may swap (fadd is commutative to LLVM); Go never creates NaN or Inf data.
	// noInf: also exclude ±Inf inputs for VAR_* / STDDEV_* because +Inf and -Inf in the same
	// group produce a +NaN variance and a -NaN update term (via FNMSUBD on +NaN sum), turning
	// `variance += ...` into an addition of two NaNs of opposite sign (spec D12.4 §7 item 11).
	noNaN bool
	noInf bool
}

// start resets the pools and the NaN/Inf rule for the records of descriptor d.
func (g *afGen) start(d *afDesc) {
	g.pool = map[int][]any{}
	isVar := d.Name == ast.AggFuncVarPop || d.Name == ast.AggFuncVarSamp ||
		d.Name == ast.AggFuncStddevPop || d.Name == ast.AggFuncStddevSamp
	g.noNaN = d.Name == ast.AggFuncSum || d.Name == ast.AggFuncAvg || isVar
	g.noInf = isVar
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
		for f := float64(math.Float32frombits(bits)); (g.noNaN && math.IsNaN(f)) || (g.noInf && math.IsInf(f, 0)); f = float64(math.Float32frombits(bits)) {
			bits = math.Float32bits(float32(g.intn(1000)) / 4)
		}
		return strconv.FormatUint(uint64(bits), 10)
	case "f64":
		bits := []uint64{0, 1 << 63, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001,
			math.Float64bits(-2.25), math.Float64bits(0.1), math.Float64bits(1.7e308), math.Float64bits(1e16),
			math.Float64bits(float64(g.intn(1000)) / 8)}[g.intn(10)]
		if f := math.Float64frombits(bits); (g.noNaN && math.IsNaN(f)) || (g.noInf && math.IsInf(f, 0)) {
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

	// ---- Part B1 (D12.4): COUNT(DISTINCT), GROUP_CONCAT, VAR / STDDEV, JSON_*AGG ----
	afGenPartB1(vg, g)
}

func afGenPartB1(vg *vec.Gen, g *afGen) {
	strBin := afType{Tp: mysql.TypeVarchar, Flen: 255, Charset: charset.CharsetUTF8MB4, Collate: "utf8mb4_bin"}
	strGeneral := afType{Tp: mysql.TypeVarchar, Flen: 255, Charset: charset.CharsetUTF8MB4, Collate: "utf8mb4_general_ci"}
	strUnicode := afType{Tp: mysql.TypeVarchar, Flen: 255, Charset: charset.CharsetUTF8MB4, Collate: "utf8mb4_unicode_ci"}
	intTp := afType{Tp: mysql.TypeLonglong, Flen: 20, Charset: charset.CharsetBin, Collate: charset.CollationBin}
	dblTp := afType{Tp: mysql.TypeDouble, Flen: 22, Decimal: -1, Charset: charset.CharsetBin, Collate: charset.CollationBin}
	jsonTp := afType{Tp: mysql.TypeJSON, Flen: 4294967295, Charset: charset.CharsetBin, Collate: charset.CollationBin, Flag: mysql.BinaryFlag}
	blobTp := afType{Tp: mysql.TypeBlob, Flen: 65535, Charset: charset.CharsetBin, Collate: charset.CollationBin, Flag: mysql.BinaryFlag}

	// 1. Extra build records for multi-arg COUNT(DISTINCT), GROUP_CONCAT (with sep + DISTINCT + ORDER BY),
	// and JSON_OBJECTAGG (2 args).
	for _, at := range []afType{intTp, dblTp, strBin, jsonTp} {
		for _, distinct := range []bool{false, true} {
			for mode := range 5 {
				vg.Add("build", "build", afDesc{
					Name: ast.AggFuncCount, Mode: mode, Distinct: distinct,
					Args: []afType{at, intTp}, Ret: intTp,
				})
			}
		}
	}
	for _, distinct := range []bool{false, true} {
		for _, orderBy := range [][]afOrderBy{nil, {{Col: 0, Desc: false}}, {{Col: 0, Desc: true}}} {
			for mode := range 5 {
				vg.Add("build", "build", afDesc{
					Name: ast.AggFuncGroupConcat, Mode: mode, Distinct: distinct,
					Args: []afType{strBin}, Ret: strBin,
					HasSep: true, Sep: hex.EncodeToString([]byte(",")), MaxLen: 1024, OrderBy: orderBy,
				})
			}
		}
	}
	for _, kt := range []afType{intTp, strBin, dblTp} {
		for _, vt := range []afType{intTp, strBin, jsonTp} {
			for mode := range 5 {
				vg.Add("build", "build", afDesc{
					Name: ast.AggFuncJsonObjectAgg, Mode: mode,
					Args: []afType{kt, vt}, Ret: jsonTp,
				})
			}
		}
	}

	// 2. VAR_POP, VAR_SAMP, STDDEV_POP, STDDEV_SAMP: update_final, reset_reuse, merge,
	// including FMA-sensitive inputs that exercise arm64 FNMSUBD / FMADDD vs amd64 unfused.
	varNames := []string{ast.AggFuncVarPop, ast.AggFuncVarSamp, ast.AggFuncStddevPop, ast.AggFuncStddevSamp}
	type varCand struct{ d, p, f *afDesc }
	var varCands []varCand
	for _, name := range varNames {
		for _, at := range afArgTypes {
			for _, distinct := range []bool{false, true} {
				d, s := afInfer(name, at, distinct, false, 4)
				if d == nil {
					continue
				}
				varCands = append(varCands, varCand{d, s[0], s[1]})
			}
		}
	}
	for _, c := range varCands {
		for _, d := range []*afDesc{c.d, c.p} {
			if !afBuilds(d) {
				continue
			}
			g.start(d)
			vg.Add("update_final", "update_final", d, g.batches(d.Args))
		}
		if afBuilds(c.d) {
			g.start(c.d)
			groups := [][][][]any{}
			for range 2 + g.intn(3) {
				groups = append(groups, g.batches(c.d.Args))
			}
			vg.Add("reset_reuse", "reset_reuse", c.d, groups)
		}
		if !c.d.Distinct && afBuilds(c.p) && afBuilds(c.f) {
			g.start(c.p)
			parts := [][][][]any{}
			for range []int{0, 1, 2, 3, 4}[g.intn(5)] {
				parts = append(parts, g.batches(c.p.Args))
			}
			vg.Add("merge", "merge", c.p, c.f, parts, g.intn(2) == 0)
		}
	}
	// Dedicated FMA-sensitive float sets (high-precision / fractional / large+small) for all 4 VAR/STDDEV funcs.
	fmaSets := [][]float64{
		{0.1, 0.2, 0.3, 0.7, 1.1, 1.3, 1.7, 2.3},
		{1e8 + 0.1, 1e8 + 0.2, 1e8 + 0.3, 1e8 + 0.7, 1e8 + 1.1},
		{1.0 / 3.0, 1.0 / 7.0, 1.0 / 11.0, math.Pi, math.E, math.Sqrt2, -0.3333333333333333},
		{1e-7, 2.3e-7, 3.7e-7, 5.1e-7, 7.9e-7, 1.1e-6},
		{42.0},          // single row: VAR_POP/STDDEV_POP = 0, VAR_SAMP/STDDEV_SAMP = NULL
		{42.0, 42.0},    // duplicate single value
		{-0.0, 0.0, 1.5},
	}
	f64Row := func(v float64) []any { return []any{strconv.FormatUint(math.Float64bits(v), 10)} }
	for _, name := range varNames {
		d, s := afInfer(name, dblTp, false, false, 4)
		dd, _ := afInfer(name, dblTp, true, false, 4)
		for _, set := range fmaSets {
			batch := make([][]any, len(set))
			for i, v := range set {
				batch[i] = f64Row(v)
			}
			vg.Add("update_final", "update_final", d, [][][]any{batch})
			vg.Add("update_final", "update_final", dd, [][][]any{batch, batch})
		}
		// Split merge of FMA-sensitive sets (hits calculateMerge lines 97 & 98).
		for i := 0; i < len(fmaSets); i++ {
			for j := i; j < len(fmaSets) && j < i+3; j++ {
				part1 := make([][]any, len(fmaSets[i]))
				for k, v := range fmaSets[i] {
					part1[k] = f64Row(v)
				}
				part2 := make([][]any, len(fmaSets[j]))
				for k, v := range fmaSets[j] {
					part2[k] = f64Row(v)
				}
				vg.Add("merge", "merge", s[0], s[1], [][][][]any{{part1}, {part2}}, false)
			}
		}
		// Random dense float64 samples for FMA coverage.
		for range 8 {
			var p1, p2 [][]any
			for range 4 + g.intn(8) {
				p1 = append(p1, f64Row((g.rng.Float64()-0.5)*1e5))
			}
			for range 4 + g.intn(8) {
				p2 = append(p2, f64Row((g.rng.Float64()-0.5)*1e5))
			}
			vg.Add("update_final", "update_final", d, [][][]any{p1, p2})
			vg.Add("merge", "merge", s[0], s[1], [][][][]any{{p1}, {p2}}, g.intn(2) == 0)
		}
	}

	// 3. count_distinct_encode: multi-arg COUNT(DISTINCT) across all eval types,
	// spec §8 item 3 (+0.0 vs -0.0, Duration different fsp, Time different types), and WriteTime.
	encTypes := []afType{
		intTp,
		dblTp,
		{Tp: mysql.TypeNewDecimal, Flen: 20, Decimal: 5, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeDatetime, Flen: 26, Decimal: 6, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeDate, Flen: 10, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeTimestamp, Flen: 19, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeDuration, Flen: 13, Decimal: 3, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		jsonTp,
		{Tp: mysql.TypeTiDBVectorFloat32, Flen: -1, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		strBin,
		strGeneral,
		strUnicode,
	}
	for _, at := range encTypes {
		d := afDesc{
			Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true,
			Args: []afType{at, intTp}, Ret: intTp,
		}
		g.start(&d)
		vg.Add("count_distinct_encode", "count_distinct_encode", "run", d, g.batches(d.Args))
	}
	// Multi-arg with 3 columns (mixed types).
	for range 20 {
		t0 := encTypes[g.intn(len(encTypes))]
		t1 := encTypes[g.intn(len(encTypes))]
		t2 := encTypes[g.intn(len(encTypes))]
		d := afDesc{
			Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true,
			Args: []afType{t0, t1, t2}, Ret: intTp,
		}
		g.start(&d)
		vg.Add("count_distinct_encode", "count_distinct_encode", "run", d, g.batches(d.Args))
	}
	// Spec §8 item 3: +0.0 vs -0.0 in single-arg (1) vs multi-arg (2).
	posZero := strconv.FormatUint(0, 10)
	negZero := strconv.FormatUint(1<<63, 10)
	vg.Add("count_distinct_encode", "count_distinct_encode", "run",
		afDesc{Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true, Args: []afType{dblTp}, Ret: intTp},
		[][][]any{{{posZero}, {negZero}}})
	vg.Add("count_distinct_encode", "count_distinct_encode", "run",
		afDesc{Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true, Args: []afType{dblTp, intTp}, Ret: intTp},
		[][][]any{{{posZero, "1"}, {negZero, "1"}}})
	// Spec §8 item 3: Duration with same value and different Fsp (single-arg 1 vs multi-arg 2).
	durTp := afType{Tp: mysql.TypeDuration, Flen: 13, Decimal: 3, Charset: charset.CharsetBin, Collate: charset.CollationBin}
	vg.Add("count_distinct_encode", "count_distinct_encode", "run",
		afDesc{Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true, Args: []afType{durTp}, Ret: intTp},
		[][][]any{{{[]any{"1000000000", 0}}, {[]any{"1000000000", 3}}}})
	vg.Add("count_distinct_encode", "count_distinct_encode", "run",
		afDesc{Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true, Args: []afType{durTp, intTp}, Ret: intTp},
		[][][]any{{{[]any{"1000000000", 0}, "1"}, {[]any{"1000000000", 3}, "1"}}})
	// Spec §8 item 3: Time with different Type (Date vs Datetime) and Fsp, plus direct WriteTime raw bytes.
	timeVals := [][]any{
		{"0", int(mysql.TypeDate), 0},
		{"0", int(mysql.TypeDatetime), 6},
		{strconv.FormatUint(uint64(types.NewTime(types.FromDate(2020, 11, 11, 0, 0, 0, 0), mysql.TypeDate, 0).CoreTime()), 10), int(mysql.TypeDate), 0},
		{strconv.FormatUint(uint64(types.NewTime(types.FromDate(2020, 11, 11, 0, 0, 0, 0), mysql.TypeDatetime, 0).CoreTime()), 10), int(mysql.TypeDatetime), 0},
		{strconv.FormatUint(uint64(types.NewTime(types.FromDate(2020, 11, 11, 12, 34, 56, 789012), mysql.TypeDatetime, 6).CoreTime()), 10), int(mysql.TypeDatetime), 6},
		{strconv.FormatUint(uint64(types.NewTime(types.FromDate(2038, 1, 19, 3, 14, 7, 999999), mysql.TypeTimestamp, 6).CoreTime()), 10), int(mysql.TypeTimestamp), 6},
		{strconv.FormatUint(uint64(types.NewTime(types.FromDate(9999, 12, 31, 23, 59, 59, 500000), mysql.TypeDatetime, 3).CoreTime()), 10), int(mysql.TypeDatetime), 3},
	}
	dtTp := afType{Tp: mysql.TypeDatetime, Flen: 26, Decimal: 6, Charset: charset.CharsetBin, Collate: charset.CollationBin}
	vg.Add("count_distinct_encode", "count_distinct_encode", "run",
		afDesc{Name: ast.AggFuncCount, Mode: int(aggregation.CompleteMode), Distinct: true, Args: []afType{dtTp}, Ret: intTp},
		[][][]any{{{timeVals[2]}, {timeVals[3]}, {timeVals[4]}, {timeVals[2]}, {nil}}})
	for _, tv := range timeVals {
		vg.Add("count_distinct_encode", "count_distinct_encode", "write_time", tv)
	}

	// 4. json_agg: JSON_ARRAYAGG and JSON_OBJECTAGG across all value types, duplicate keys,
	// binary zero-padding, DECIMAL->DOUBLE, and error paths.
	binFixedStr := afType{Tp: mysql.TypeString, Flen: 8, Charset: charset.CharsetBin, Collate: charset.CollationBin, Flag: mysql.BinaryFlag}
	jsonValTypes := []afType{
		intTp,
		{Tp: mysql.TypeLonglong, Flag: mysql.UnsignedFlag, Flen: 20, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeFloat, Flen: 12, Decimal: -1, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		dblTp,
		{Tp: mysql.TypeNewDecimal, Flen: 20, Decimal: 5, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		strBin,
		binFixedStr,
		blobTp,
		{Tp: mysql.TypeBit, Flag: mysql.UnsignedFlag, Flen: 8, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeDate, Flen: 10, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeDatetime, Flen: 26, Decimal: 6, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		{Tp: mysql.TypeTimestamp, Flen: 19, Charset: charset.CharsetBin, Collate: charset.CollationBin},
		durTp,
		jsonTp,
		{Tp: mysql.TypeNull, Flen: 0, Charset: charset.CharsetBin, Collate: charset.CollationBin},
	}
	for _, vt := range jsonValTypes {
		// JSON_ARRAYAGG
		d := afDesc{Name: ast.AggFuncJsonArrayagg, Mode: int(aggregation.CompleteMode), Args: []afType{vt}, Ret: jsonTp}
		p := afDesc{Name: ast.AggFuncJsonArrayagg, Mode: int(aggregation.Partial1Mode), Args: []afType{vt}, Ret: jsonTp}
		f := afDesc{Name: ast.AggFuncJsonArrayagg, Mode: int(aggregation.FinalMode), Args: []afType{jsonTp}, Ret: jsonTp}
		g.start(&d)
		vg.Add("json_agg", "json_agg", "update_final", d, g.batches(d.Args))
		vg.Add("json_agg", "json_agg", "reset_reuse", d, [][][][]any{g.batches(d.Args), g.batches(d.Args)})
		vg.Add("json_agg", "json_agg", "merge", p, f, [][][][]any{g.batches(p.Args), g.batches(p.Args)}, g.intn(2) == 0)

		// JSON_OBJECTAGG with non-NULL keys (string key so no accidental NULL key error).
		od := afDesc{Name: ast.AggFuncJsonObjectAgg, Mode: int(aggregation.CompleteMode), Args: []afType{strBin, vt}, Ret: jsonTp}
		op := afDesc{Name: ast.AggFuncJsonObjectAgg, Mode: int(aggregation.Partial1Mode), Args: []afType{strBin, vt}, Ret: jsonTp}
		of := afDesc{Name: ast.AggFuncJsonObjectAgg, Mode: int(aggregation.FinalMode), Args: []afType{jsonTp}, Ret: jsonTp}
		g.start(&od)
		mkObjBatch := func(keys []string) [][]any {
			rows := make([][]any, len(keys))
			for i, k := range keys {
				rows[i] = []any{hex.EncodeToString([]byte(k)), g.value(1, vt)}
			}
			return rows
		}
		vg.Add("json_agg", "json_agg", "update_final", od, [][][]any{mkObjBatch([]string{"a", "b", "a", "c"}), mkObjBatch([]string{"b", "d"})})
		vg.Add("json_agg", "json_agg", "reset_reuse", od, [][][][]any{{mkObjBatch([]string{"k1", "k2"})}, {}, {mkObjBatch([]string{"k2", "k3", "k2"})}})
		vg.Add("json_agg", "json_agg", "merge", op, of, [][][][]any{{mkObjBatch([]string{"x", "y"})}, {mkObjBatch([]string{"y", "z"})}}, g.intn(2) == 0)
	}
	// JSON error paths: NULL key, binary charset key, unsupported value types (Enum, Set, VectorFloat32).
	objDesc := afDesc{Name: ast.AggFuncJsonObjectAgg, Mode: int(aggregation.CompleteMode), Args: []afType{strBin, intTp}, Ret: jsonTp}
	vg.Add("json_agg", "json_agg", "update_final", objDesc, [][][]any{{{hex.EncodeToString([]byte("ok")), "1"}, {nil, "2"}}})
	objBinKey := afDesc{Name: ast.AggFuncJsonObjectAgg, Mode: int(aggregation.CompleteMode), Args: []afType{blobTp, intTp}, Ret: jsonTp}
	vg.Add("json_agg", "json_agg", "update_final", objBinKey, [][][]any{{{hex.EncodeToString([]byte("k")), "1"}}})
	for _, badTp := range []afType{
		{Tp: mysql.TypeEnum, Flen: 1, Charset: charset.CharsetUTF8MB4, Collate: "utf8mb4_bin", Elems: afElems},
		{Tp: mysql.TypeSet, Flen: 9, Charset: charset.CharsetUTF8MB4, Collate: "utf8mb4_bin", Elems: afElems},
		{Tp: mysql.TypeTiDBVectorFloat32, Flen: -1, Charset: charset.CharsetBin, Collate: charset.CollationBin},
	} {
		ad := afDesc{Name: ast.AggFuncJsonArrayagg, Mode: int(aggregation.CompleteMode), Args: []afType{badTp}, Ret: jsonTp}
		g.start(&ad)
		vg.Add("json_agg", "json_agg", "update_final", ad, [][][]any{{{g.fresh(badTp)}}})
		od := afDesc{Name: ast.AggFuncJsonObjectAgg, Mode: int(aggregation.CompleteMode), Args: []afType{strBin, badTp}, Ret: jsonTp}
		g.start(&od)
		vg.Add("json_agg", "json_agg", "update_final", od, [][][]any{{{hex.EncodeToString([]byte("k")), g.fresh(badTp)}}})
	}

	// 5. group_concat: plain, DISTINCT, ORDER BY, DISTINCT+ORDER BY; separators, max_len,
	// mid-separator truncation, TruncateAsWarning true/false, once-only across groups and
	// partial+final merge, tied rows (heap & pdqsort order), collations, MergePartialResult errors.
	hexS := func(s string) string { return hex.EncodeToString([]byte(s)) }
	seps := []string{",", "", "---", "|"}
	maxLens := []uint64{1, 2, 4, 7, 10, 1024}

	// Plain & DISTINCT across collations, separators, max_len, TruncateAsWarning.
	for _, colTp := range []afType{strBin, strGeneral, strUnicode} {
		for _, distinct := range []bool{false, true} {
			for _, truncWarn := range []bool{false, true} {
				for _, maxLen := range maxLens {
					sep := seps[g.intn(len(seps))]
					d := afDesc{
						Name: ast.AggFuncGroupConcat, Mode: int(aggregation.CompleteMode), Distinct: distinct,
						Args: []afType{colTp}, Ret: strBin,
						HasSep: true, Sep: hexS(sep), MaxLen: maxLen, TruncAsWarn: truncWarn,
					}
					grp1 := [][][]any{
						{{hexS("aaa")}, {hexS("AAA")}, {nil}, {hexS("bbb")}},
						{{hexS("ccc")}, {hexS("a ")}, {hexS("ddd")}},
					}
					grp2 := [][][]any{
						{{hexS("xxx")}, {hexS("yyy")}, {hexS("zzz")}},
					}
					grp3 := [][][]any{{{nil}}}
					vg.Add("group_concat", "group_concat", "run", d, [][][][]any{grp1, grp2, grp3})
				}
			}
		}
	}
	// Multi-arg GROUP_CONCAT (NULL in 2nd arg skips row) + mid-separator truncation ("aaa---bbb---ccc", maxLen=10).
	for _, distinct := range []bool{false, true} {
		d := afDesc{
			Name: ast.AggFuncGroupConcat, Mode: int(aggregation.CompleteMode), Distinct: distinct,
			Args: []afType{strGeneral, strBin}, Ret: strBin,
			HasSep: true, Sep: hexS("---"), MaxLen: 10, TruncAsWarn: true,
		}
		grp := [][][]any{
			{{hexS("a"), hexS("aa")}, {hexS("A"), hexS("aa")}, {hexS("x"), nil}, {hexS("b"), hexS("bb")}, {hexS("c"), hexS("cc")}},
		}
		vg.Add("group_concat", "group_concat", "run", d, [][][][]any{grp})
	}
	// Split merge of plain GROUP_CONCAT with shared truncated sentinel (both TruncateAsWarning false & true).
	for _, truncWarn := range []bool{false, true} {
		for _, maxLen := range []uint64{4, 10, 1024} {
			for _, sep := range []string{",", "---"} {
				pd := afDesc{
					Name: ast.AggFuncGroupConcat, Mode: int(aggregation.Partial1Mode),
					Args: []afType{strBin}, Ret: strBin,
					HasSep: true, Sep: hexS(sep), MaxLen: maxLen, TruncAsWarn: truncWarn,
				}
				fd := afDesc{
					Name: ast.AggFuncGroupConcat, Mode: int(aggregation.FinalMode),
					Args: []afType{strBin}, Ret: strBin,
					HasSep: true, Sep: hexS(sep), MaxLen: maxLen, TruncAsWarn: truncWarn,
				}
				parts := [][][][]any{
					{{{hexS("aaa")}, {hexS("bbb")}}},
					{{{nil}}},
					{{{hexS("ccc")}, {hexS("ddd")}}},
				}
				vg.Add("group_concat", "group_concat", "merge", pd, fd, parts)
			}
		}
	}
	// ORDER BY and DISTINCT + ORDER BY: multi-column ASC/DESC, mid-separator truncation, and
	// many tied rows (separate payload col 0, order keys col 1/2) to pin heap + sort tie order.
	for _, distinct := range []bool{false, true} {
		for _, truncWarn := range []bool{false, true} {
			// Mid-separator truncation example from Go's func_group_concat.go:318:
			// max_len=10, sep="---", 'aaa---bbb---ccc' -> 'aaa---bbb-'
			dMid := afDesc{
				Name: ast.AggFuncGroupConcat, Mode: int(aggregation.CompleteMode), Distinct: distinct,
				Args: []afType{strBin}, Ret: strBin,
				HasSep: true, Sep: hexS("---"), MaxLen: 10, TruncAsWarn: truncWarn,
				OrderBy: []afOrderBy{{Col: 0, Desc: false}},
			}
			vg.Add("group_concat", "group_concat", "run", dMid, [][][][]any{
				{{{hexS("ccc")}, {hexS("aaa")}, {hexS("bbb")}, {hexS("aaa")}}},
			})

			// Tied rows with 2 ORDER BY columns (col 1 ASC, col 2 DESC), distinct payloads in col 0:
			// exercises container/heap eviction order AND sort.Sort(sort.Reverse(h)) tie order!
			for _, maxLen := range []uint64{4, 10, 20, 1024} {
				dTie := afDesc{
					Name: ast.AggFuncGroupConcat, Mode: int(aggregation.CompleteMode), Distinct: distinct,
					Args: []afType{strGeneral}, ExtraCols: []afType{intTp, strUnicode}, Ret: strBin,
					HasSep: true, Sep: hexS(","), MaxLen: maxLen, TruncAsWarn: truncWarn,
					OrderBy: []afOrderBy{{Col: 1, Desc: false}, {Col: 2, Desc: true}},
				}
				var tieRows [][]any
				for i := range 24 {
					key1 := strconv.Itoa(i % 4)
					key2 := hexS([]string{"a", "A", "b", "B"}[(i/4)%4])
					payload := hexS(fmt.Sprintf("r%02d", i))
					if i == 5 {
						payload = hexS("R00") // duplicate under utf8mb4_general_ci
					}
					tieRows = append(tieRows, []any{payload, key1, key2})
				}
				tieRows = append(tieRows, []any{nil, "0", hexS("a")})
				vg.Add("group_concat", "group_concat", "run", dTie, [][][][]any{
					{tieRows[:12], tieRows[12:]},
					{tieRows[:6]},
				})
			}
		}
		// ORDER BY MergePartialResult -> ErrInternal.
		dErr := afDesc{
			Name: ast.AggFuncGroupConcat, Mode: int(aggregation.CompleteMode), Distinct: distinct,
			Args: []afType{strBin}, Ret: strBin,
			HasSep: true, Sep: hexS(","), MaxLen: 1024,
			OrderBy: []afOrderBy{{Col: 0, Desc: true}},
		}
		vg.Add("group_concat", "group_concat", "merge_err", dErr)
	}
}
