package domains

// Domain "cast", D08.5 batch 3 (appended after constant propagation; earlier records are
// unchanged): the bit operators & | ^ << >> ~ through the "arbuild" op of batch 1
// (NewFunctionBase / NewFunction / NewFunctionTryFold of name(args...), then EvalExpr with
// vectorization off and on).
//   - source "bitop": every operator over random columns (BIGINT signed / unsigned
//     boundaries, the arithmetic leaves of batch 1: DOUBLE, DECIMAL, strings, FLOAT,
//     hybrid, time ...), constants and CASTs; shift counts include 63, 64, 65, huge and
//     negative values.
//   - source "bitfb": a binary bit operator whose second argument fails or warns on some
//     rows while the first is NULL: the row version does not evaluate the second argument
//     on such rows, the vector version does (D08.5 spec §3.2, like LogicXor).

import (
	"math"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

var bitBinNames = []string{ast.And, ast.Or, ast.Xor, ast.LeftShift, ast.RightShift}

func bitSigFuncs() []string {
	out := []string{}
	for _, s := range []string{"BitAnd", "BitOr", "BitXor", "BitNeg", "LeftShift", "RightShift"} {
		n := "builtin" + s + "Sig."
		out = append(out, n+"evalInt", n+"vecEvalInt", n+"vectorized", n+"Clone")
	}
	return append(out, "bitAndFunctionClass.getFunction", "bitOrFunctionClass.getFunction",
		"bitXorFunctionClass.getFunction", "leftShiftFunctionClass.getFunction",
		"rightShiftFunctionClass.getFunction", "bitNegFunctionClass.getFunction")
}

func init() {
	c := castCoverage["arbuild"]
	c.GoFuncs = append(c.GoFuncs, bitSigFuncs()...)
	castCoverage["arbuild"] = c
}

var bitShiftCounts = []int64{0, 1, 7, 31, 62, 63, 64, 65, 127, 1 << 40, -1, -63, -64, math.MinInt64, math.MaxInt64}

// bitIntCol: a BIGINT column (signed or unsigned) over boundary values, or a shift-count
// column.
func (g *castGen) bitIntCol(shift bool) (*types.FieldType, func() []any) {
	var flag uint
	if g.pick(3) == 0 {
		flag |= mysql.UnsignedFlag
	}
	ft := castFt(mysql.TypeLonglong, flag, 20, 0)
	return ft, func() []any {
		if g.pick(6) == 0 {
			return []any{types.KindNull}
		}
		if shift {
			v := bitShiftCounts[g.pick(len(bitShiftCounts))]
			if flag != 0 {
				return []any{types.KindUint64, uint64(v)}
			}
			return []any{types.KindInt64, v}
		}
		if flag != 0 {
			return []any{types.KindUint64, []uint64{0, 1, 0xff, 1 << 63, math.MaxUint64, 0x5555555555555555}[g.pick(6)]}
		}
		return []any{types.KindInt64, arBigInts[g.pick(len(arBigInts))]}
	}
}

func castGenerateD085c(vg *vec.Gen, g *castGen) {
	for i := 0; i < 1500; i++ {
		neg := g.pick(6) == 0
		name := ast.BitNeg
		if !neg {
			name = bitBinNames[g.pick(len(bitBinNames))]
		}
		shift := name == ast.LeftShift || name == ast.RightShift
		fts := []*types.FieldType{}
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < 2; c++ {
			var ft *types.FieldType
			var gen func() []any
			if g.pick(3) == 0 {
				ft, gen = g.arCol()
			} else {
				ft, gen = g.bitIntCol(shift && c == 1)
			}
			fts = append(fts, ft)
			gens = append(gens, gen)
			cols = append(cols, sfFtIn(ft))
		}
		arg := func(c int) any {
			switch g.pick(6) {
			case 0:
				return g.arConst()
			case 1:
				if shift && c == 1 {
					return []any{"const", []any{types.KindInt64, bitShiftCounts[g.pick(len(bitShiftCounts))]}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}
				}
				return g.arLeaf(fts, 1)
			default:
				return []any{"col", c}
			}
		}
		args := []any{arg(0)}
		if !neg {
			args = append(args, arg(1))
		}
		n := g.pick(16)
		vg.Add("bitop", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], name, args, g.arCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// A failing second argument on rows where the first is NULL.
	for i := 0; i < 400; i++ {
		ft0, gen0 := g.bitIntCol(false)
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
		name := bitBinNames[g.pick(len(bitBinNames))]
		vg.Add("bitfb", "arbuild", []int{0, 0, 1}[g.pick(3)], name, []any{[]any{"col", 0}, g.lgFailing(1, 0)}, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1)}, rows, g.arSel(n))
	}
}
