package domains

// Domain "cast", D08.4 batch 2 (appended after every earlier batch; earlier records are
// unchanged): COALESCE (coalesceFunctionClass, the eight builtinCoalesce*Sig signatures)
// and the type inference it shares with the control functions (InferType4ControlFuncs and
// its helpers, block D08.9), through the "arbuild" op of D08.5 (NewFunctionBase /
// NewFunction / NewFunctionTryFold of coalesce(args...), then EvalExpr with vectorization
// off and on).
//   - source "coal": 1 to 5 arguments over columns of every comparison type (numeric,
//     strings of several collations, FLOAT, hybrid, time, duration, JSON, VECTOR, YEAR),
//     constants (NULL of type NULL too) and CASTs.
//   - source "coalfb": arguments that fail or warn on some rows (division by zero, BIGINT
//     overflow, truncated strings), at any position (the first one too), behind columns
//     that are NULL on some rows: Go's vector version evaluates every argument and falls
//     back to the row version (which stops at the first non-NULL argument) when one of
//     them fails or warns, truncating the warnings (D08.1 spec §3.6).
//
// The file name sorts after cast_d085*.go so that its init runs after the one that sets
// castCoverage["arbuild"].

import (
	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

func coalGoFuncs() []string {
	out := []string{"coalesceFunctionClass.getFunction", "InferType4ControlFuncs",
		"setFlenFromArgs", "setDecimalFromArgs", "addCollateAndCharsetAndFlagFromArgs",
		"maxlen", "hasNonBinaryStr", "hasBinaryStr"}
	for _, k := range []struct{ sig, et string }{{"Int", "Int"}, {"Real", "Real"},
		{"Decimal", "Decimal"}, {"String", "String"}, {"Time", "Time"},
		{"Duration", "Duration"}, {"JSON", "JSON"}} {
		n := "builtinCoalesce" + k.sig + "Sig."
		out = append(out, n+"eval"+k.et, n+"vecEval"+k.et, n+"fallbackEval"+k.et,
			n+"vectorized", n+"Clone")
	}
	return append(out, "builtinCoalesceVectorFloat32Sig.evalVectorFloat32",
		"builtinCoalesceVectorFloat32Sig.Clone")
}

func init() {
	c := castCoverage["arbuild"]
	c.GoFuncs = append(c.GoFuncs, coalGoFuncs()...)
	castCoverage["arbuild"] = c
}

// coalArg: an argument recipe for COALESCE over the columns.
func (g *castGen) coalArg(fts []*types.FieldType) any {
	switch k := g.pick(10); {
	case k < 5:
		return []any{"col", g.pick(len(fts))}
	case k < 7:
		c, _ := g.cmpConst()
		return c
	case k < 8:
		return []any{"const", []any{types.KindNull}, sfFtIn(types.NewFieldType(mysql.TypeNull))}
	default:
		var tgt *types.FieldType
		switch g.pick(4) {
		case 0:
			tgt = g.strTarget()
		case 1:
			tgt = g.timeTarget()
		case 2:
			tgt = g.jsonTarget()
		default:
			tgt = g.target()
		}
		return []any{"cast", sfFtIn(tgt), []any{"col", g.pick(len(fts))}}
	}
}

func castGenerateD084b(vg *vec.Gen, g *castGen) {
	// COALESCE over random arguments.
	for i := 0; i < 1500; i++ {
		nc := 1 + g.pick(3)
		fts := []*types.FieldType{}
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < nc; c++ {
			ft, gen := g.cmpCol()
			fts = append(fts, ft)
			gens = append(gens, gen)
			cols = append(cols, sfFtIn(ft))
		}
		// Same-typed arguments half of the time (one result type, no CAST around them).
		args := []any{}
		na := 1 + g.pick(5)
		same := g.pick(2) == 0
		for a := 0; a < na; a++ {
			if same {
				args = append(args, []any{"col", g.pick(nc)})
				continue
			}
			args = append(args, g.coalArg(fts))
		}
		n := g.pick(16)
		vg.Add("coal", "arbuild", []int{0, 0, 1, 2}[g.pick(4)], ast.Coalesce, args, g.arCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// Failing or warning arguments: column 0 is NULL on some rows (so that the row version
	// reaches the later arguments only there), column 1 feeds the failing arguments, column
	// 2 is any comparison column.
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
				return []any{types.KindUint64, []uint64{0, 1, 2, 1<<64 - 1}[g.pick(4)]}
			}
			return []any{types.KindInt64, []int64{0, 1, 2, 1<<63 - 1, -1}[g.pick(5)]}
		}
		ft2, gen2 := g.cmpCol()
		n := 1 + g.pick(12)
		rows := g.arRows([]func() []any{gen0, gen1, gen2}, n)
		// The first rows do not fail, so that the failing row is not row 0.
		for r := 0; r < n/2; r++ {
			rows[r].([]any)[1] = []any{types.KindInt64, int64(1)}
			if flag != 0 {
				rows[r].([]any)[1] = []any{types.KindUint64, uint64(1)}
			}
		}
		na := 2 + g.pick(3)
		args := make([]any, 0, na)
		failAt := g.pick(na)
		for a := 0; a < na; a++ {
			switch {
			case a == failAt:
				args = append(args, g.lgFailing(1, 0))
			case g.pick(3) == 0:
				args = append(args, []any{"col", 2})
			case g.pick(4) == 0:
				c, _ := g.cmpConst()
				args = append(args, c)
			default:
				args = append(args, []any{"col", 0})
			}
		}
		vg.Add("coalfb", "arbuild", []int{0, 0, 1}[g.pick(3)], ast.Coalesce, args, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1), sfFtIn(ft2)}, rows, g.arSel(n))
	}
}
