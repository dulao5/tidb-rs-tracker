package domains

// Domain "cast", D08.5 batch 2 (appended after D08.5 batch 1; earlier records are unchanged):
// AND / OR / XOR, NOT, IS TRUE / IS FALSE (with and without keepNull) and the unary minus,
// through the "arbuild" op of batch 1 (NewFunctionBase / NewFunction / NewFunctionTryFold of
// name(args...), then EvalExpr with vectorization off and on).
//   - source "logic": every name over random columns, constants, CASTs and nested
//     arithmetic / comparison / logic arguments.
//   - source "logicfb": AND / OR / XOR whose second argument fails or warns on some rows
//     (division by zero, BIGINT overflow, truncated strings): Go's vector AND / OR then
//     truncate the warnings since the first argument and fall back to the row version;
//     the row XOR does not evaluate the second argument when the first is NULL, the vector
//     XOR does (D08.1 spec §3.4 (c)).
//   - source "uminus": the unary minus over BIGINT boundaries (signed / unsigned columns and
//     constants: -MinInt64 and unsigned 2^63+1 overflow, unsigned 2^63 is MinInt64, an
//     overflowing constant makes the result DECIMAL), DECIMAL, DOUBLE, time / duration
//     (as DECIMAL) and string arguments.

import (
	"math"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

var lgNames = []string{ast.LogicAnd, ast.LogicOr, ast.LogicXor, ast.UnaryNot, ast.UnaryMinus,
	ast.IsTruthWithoutNull, ast.IsTruthWithNull, ast.IsFalsity}

func lgSigFuncs() []string {
	out := []string{}
	for _, s := range []string{"LogicAnd", "LogicOr", "LogicXor", "UnaryNotReal", "UnaryNotDecimal",
		"UnaryNotInt", "RealIsTrue", "DecimalIsTrue", "IntIsTrue", "RealIsFalse", "DecimalIsFalse",
		"IntIsFalse", "UnaryMinusInt"} {
		n := "builtin" + s + "Sig."
		out = append(out, n+"evalInt", n+"vecEvalInt", n+"vectorized", n+"Clone")
	}
	out = append(out, "builtinUnaryMinusDecimalSig.evalDecimal", "builtinUnaryMinusDecimalSig.vecEvalDecimal",
		"builtinUnaryMinusDecimalSig.vectorized", "builtinUnaryMinusDecimalSig.Clone",
		"builtinUnaryMinusRealSig.evalReal", "builtinUnaryMinusRealSig.vecEvalReal",
		"builtinUnaryMinusRealSig.vectorized", "builtinUnaryMinusRealSig.Clone",
		"builtinUnaryNotJSONSig.evalInt", "builtinUnaryNotJSONSig.Clone",
		"builtinVectorFloat32IsTrueSig.evalInt", "builtinVectorFloat32IsTrueSig.Clone",
		"builtinVectorFloat32IsFalseSig.evalInt", "builtinVectorFloat32IsFalseSig.Clone",
		"builtinLogicAndSig.fallbackEvalInt", "builtinLogicOrSig.fallbackEvalInt",
		"logicAndFunctionClass.getFunction", "logicOrFunctionClass.getFunction",
		"logicXorFunctionClass.getFunction", "isTrueOrFalseFunctionClass.getFunction",
		"isTrueOrFalseFunctionClass.getDisplayName",
		"unaryNotFunctionClass.getFunction", "unaryMinusFunctionClass.getFunction",
		"unaryMinusFunctionClass.handleIntOverflow", "unaryMinusFunctionClass.typeInfer",
		"wrapWithIsTrue")
	return out
}

func init() {
	c := castCoverage["arbuild"]
	c.GoFuncs = append(c.GoFuncs, lgSigFuncs()...)
	castCoverage["arbuild"] = c
}

// lgBoolCol: an integer column holding mostly 0 / 1 / NULL (and a few other values).
func (g *castGen) lgBoolCol() (*types.FieldType, func() []any) {
	var flag uint
	if g.pick(4) == 0 {
		flag |= mysql.NotNullFlag
	}
	ft := castFt([]byte{mysql.TypeLonglong, mysql.TypeTiny}[g.pick(2)], flag, 4, 0)
	return ft, func() []any {
		if flag == 0 && g.pick(4) == 0 {
			return []any{types.KindNull}
		}
		return []any{types.KindInt64, []int64{0, 1, 0, 1, 2, -1}[g.pick(6)]}
	}
}

// lgLeaf: an argument recipe for the logical operators: the arithmetic leaves of batch 1,
// comparisons and nested logical operators over integer columns.
func (g *castGen) lgLeaf(fts []*types.FieldType, ints []int, depth int) any {
	switch k := g.pick(10); {
	case k < 6 || depth > 1 || len(ints) == 0:
		return g.arLeaf(fts, 1)
	case k < 8:
		op := []string{ast.LT, ast.GE, ast.EQ, ast.NE}[g.pick(4)]
		return []any{"fn", op, []any{[]any{"col", ints[g.pick(len(ints))]}, g.arConst()}}
	default:
		op := []string{ast.LogicAnd, ast.LogicOr, ast.LogicXor}[g.pick(3)]
		return []any{"fn", op, []any{g.lgLeaf(fts, ints, depth+1), g.lgLeaf(fts, ints, depth+1)}}
	}
}

// lgFailing: a second argument that fails or warns on some rows: division by zero,
// BIGINT overflow, a truncated string read as a number, or a nested AND / OR of them.
func (g *castGen) lgFailing(col int, depth int) any {
	switch g.pick(5) {
	case 0:
		return []any{"fn", ast.Div, []any{[]any{"col", col}, []any{"col", col}}}
	case 1:
		return []any{"fn", ast.Plus, []any{[]any{"col", col},
			[]any{"const", []any{types.KindInt64, int64(math.MaxInt64)}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}}}
	case 2:
		s := []string{"12abc", "abc", "0x", " 1", "0"}[g.pick(5)]
		return []any{"fn", ast.Plus, []any{[]any{"col", col},
			[]any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(castFt(mysql.TypeVarchar, 0, 64, 0))}}}
	case 3:
		if depth == 0 {
			op := []string{ast.LogicAnd, ast.LogicOr}[g.pick(2)]
			return []any{"fn", op, []any{[]any{"col", col}, g.lgFailing(col, depth+1)}}
		}
		fallthrough
	default:
		return []any{"fn", ast.IntDiv, []any{[]any{"col", col}, []any{"col", col}}}
	}
}

func castGenerateD085b(vg *vec.Gen, g *castGen) {
	// Every name over random arguments.
	for i := 0; i < 2000; i++ {
		nc := 1 + g.pick(3)
		fts := []*types.FieldType{}
		gens := []func() []any{}
		cols := []any{}
		ints := []int{}
		for c := 0; c < nc; c++ {
			var ft *types.FieldType
			var gen func() []any
			if g.pick(2) == 0 {
				ft, gen = g.lgBoolCol()
				ints = append(ints, c)
			} else {
				ft, gen = g.arCol()
			}
			fts = append(fts, ft)
			gens = append(gens, gen)
			cols = append(cols, sfFtIn(ft))
		}
		n := g.pick(16)
		name := lgNames[g.pick(len(lgNames))]
		args := []any{g.lgLeaf(fts, ints, 0)}
		if name == ast.LogicAnd || name == ast.LogicOr || name == ast.LogicXor {
			args = append(args, g.lgLeaf(fts, ints, 0))
		}
		vg.Add("logic", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], name, args, g.arCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// AND / OR / XOR with a failing or warning second argument: column 0 decides (0 / 1 /
	// NULL), column 1 feeds the failing argument (0 for division by zero, large values for
	// overflow); the failing rows are not at row 0.
	for i := 0; i < 600; i++ {
		ft0, gen0 := g.lgBoolCol()
		var flag uint
		if g.pick(3) == 0 {
			flag |= mysql.UnsignedFlag
		}
		ft1 := castFt(mysql.TypeLonglong, flag, 20, 0)
		gen1 := func() []any {
			if g.pick(6) == 0 {
				return []any{types.KindNull}
			}
			if flag != 0 {
				return []any{types.KindUint64, []uint64{0, 1, 2, math.MaxUint64}[g.pick(4)]}
			}
			return []any{types.KindInt64, []int64{0, 1, 2, math.MaxInt64, -1}[g.pick(5)]}
		}
		n := 1 + g.pick(12)
		rows := g.arRows([]func() []any{gen0, gen1}, n)
		for r := 0; r < n/2; r++ {
			rows[r].([]any)[1] = []any{types.KindInt64, int64(1)}
			if flag != 0 {
				rows[r].([]any)[1] = []any{types.KindUint64, uint64(1)}
			}
		}
		name := []string{ast.LogicAnd, ast.LogicOr, ast.LogicXor}[g.pick(3)]
		var a, b any = []any{"col", 0}, g.lgFailing(1, 0)
		if g.pick(4) == 0 {
			a, b = b, a
		}
		vg.Add("logicfb", "arbuild", []int{0, 0, 1}[g.pick(3)], name, []any{a, b}, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1)}, rows, g.arSel(n))
	}
	// The unary minus: BIGINT boundaries (columns and constants), DECIMAL, DOUBLE, time /
	// duration, strings.
	for i := 0; i < 500; i++ {
		var ft *types.FieldType
		var gen func() []any
		switch g.pick(4) {
		case 0, 1:
			var flag uint
			if g.pick(2) == 0 {
				flag |= mysql.UnsignedFlag
			}
			ft = castFt(mysql.TypeLonglong, flag, 20, 0)
			gen = func() []any {
				if g.pick(6) == 0 {
					return []any{types.KindNull}
				}
				if flag != 0 {
					return []any{types.KindUint64, []uint64{0, 1, 1 << 63, (1 << 63) + 1, math.MaxUint64, (1 << 63) - 1}[g.pick(6)]}
				}
				return []any{types.KindInt64, arBigInts[g.pick(len(arBigInts))]}
			}
		default:
			ft, gen = g.arCol()
		}
		n := g.pick(12)
		var a any = []any{"col", 0}
		switch g.pick(4) {
		case 0:
			a = g.arConst()
		case 1:
			v := []uint64{1 << 63, (1 << 63) + 1, math.MaxUint64, 5}[g.pick(4)]
			a = []any{"const", []any{types.KindUint64, v}, sfFtIn(castFt(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0))}
		}
		vg.Add("uminus", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], ast.UnaryMinus, []any{a}, g.arCtx(),
			[]any{sfFtIn(ft)}, g.arRows([]func() []any{gen}, n), g.arSel(n))
	}
}
