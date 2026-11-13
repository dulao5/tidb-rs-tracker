package domains

// Domain "cast", D08.1 part 3 (appended at the end; earlier records are unchanged): the
// VECTOR functions of builtin_vec.go / builtin_vec_vec.go and vs_helper.go.
//   - "vecbuild": the "arbuild" op of D08.5 batch 1 under its own op name (NewFunctionBase /
//     NewFunction / NewFunctionTryFold of name(args...), then EvalExpr with vectorization
//     off and on).
//       - source "vec": every function over random vector columns (0-5 dimensions; the
//         elements include 0, subnormal 1e-45, the float32 maximum (whose squares overflow to
//         +Inf, and Inf - Inf is NaN, which is NULL), negative values), equal and different
//         dimensions (the distance error), zero vectors (cosine 0/0 is NaN -> NULL), empty
//         vectors; string columns for VEC_FROM_TEXT (valid, invalid, NaN / Inf texts);
//         vector constants, CASTs, and nested calls (VEC_FROM_TEXT inside the distances).
//       - source "vecfb": a distance whose second argument (VEC_FROM_TEXT of an invalid text)
//         fails on rows where the first is NULL: the row version does not evaluate it there,
//         the vector version does (D08.1 spec §3.7, like LogicXor).
//   - "vsh": ExtractVectorHelper over function recipes (distance functions over a vector
//     column and a vector constant in either order, two columns, two constants, non-vector
//     columns, other functions, plain columns): the helper's name, pb code, vector and column.

import (
	"encoding/json"
	"fmt"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

var vecFnNames = []string{ast.VecDims, ast.VecL1Distance, ast.VecL2Distance, ast.VecNegativeInnerProduct,
	ast.VecCosineDistance, ast.VecL2Norm, ast.VecFromText, ast.VecAsText}

var vecDistNames = []string{ast.VecL1Distance, ast.VecL2Distance, ast.VecNegativeInnerProduct, ast.VecCosineDistance}

func vecGoFuncs() []string {
	out := []string{}
	for _, s := range []struct{ sig, eval, vec string }{
		{"VecDims", "evalInt", "vecEvalInt"}, {"VecL1Distance", "evalReal", "vecEvalReal"},
		{"VecL2Distance", "evalReal", "vecEvalReal"}, {"VecNegativeInnerProduct", "evalReal", "vecEvalReal"},
		{"VecCosineDistance", "evalReal", "vecEvalReal"}, {"VecL2Norm", "evalReal", "vecEvalReal"},
		{"VecFromText", "evalVectorFloat32", "vecEvalVectorFloat32"}, {"VecAsText", "evalString", "vecEvalString"},
	} {
		n := "builtin" + s.sig + "Sig."
		out = append(out, n+"Clone", n+s.eval, n+s.vec, n+"vectorized")
	}
	for _, c := range []string{"vecDims", "vecL1Distance", "vecL2Distance", "vecNegativeInnerProduct",
		"vecCosineDistance", "vecL2Norm", "vecFromText", "vecAsText"} {
		out = append(out, c+"FunctionClass.getFunction")
	}
	return out
}

func init() {
	castCoverage["vecbuild"] = vec.Coverage{Min: 2000, GoFuncs: vecGoFuncs()}
	castCoverage["vsh"] = vec.Coverage{Min: 200, GoFuncs: []string{"ExtractVectorHelper"}}
}

func castExecD081c(op string, p []json.RawMessage) any {
	switch op {
	case "vecbuild":
		return castExecD085("arbuild", p)
	case "vsh":
		return castExecVsh(p)
	}
	return nil
}

// castExecVsh: ["vsh", recipes, ctx, fts] -> per recipe, the helper or null.
func castExecVsh(p []json.RawMessage) any {
	ctx, ev := castCtx(p[1])
	fts := []*types.FieldType{}
	for _, f := range exArr(p[2]) {
		fts = append(fts, d2FtIn(exArr(f)))
	}
	m := map[string]any{}
	var es []expression.Expression
	m["args"] = exTry(func() any {
		es = evExprs(ctx, p[0], fts)
		ev.TruncateWarnings(0)
		return nil
	})
	if es == nil {
		return m
	}
	res := []any{}
	for _, e := range es {
		res = append(res, exTry(func() any {
			h := expression.ExtractVectorHelper(e)
			if h == nil {
				return nil
			}
			return map[string]any{"name": vec.GoString(h.DistanceFnName.O), "pb": int(h.FnPbCode),
				"vec": vec.GoString(h.Vec.String()), "col": sfDump(h.Column)}
		}))
	}
	m["res"] = res
	return m
}

// vecElems: vector elements as text (ParseVectorFloat32 rejects NaN / Inf, so overflow comes
// from the float32 maximum).
var vecElems = []string{"0", "1", "-1", "-1.5", "0.1", "3.4e38", "-3.4e38", "1e-45", "-1e-45", "2.5", "1e19", "123456.789"}

// vecTexts: strings for VEC_FROM_TEXT: valid vectors, spaces, invalid texts, NaN / Inf.
var vecTexts = []string{"[]", "[1]", "[1,2]", "[1,2,3]", " [ 1 , 2 ] ", "[1e-45,3.4e38]", "[-0]", "[0.1,0.2,0.3]",
	"[1,]", "[,1]", "1,2", "[1 2]", "[NaN]", "[Inf]", "[-Inf]", "[1e39]", "abc", "", "[", "[1,2", "[\"1\"]",
	"[1e400]", "[0x10]", "[ ]", "[1,2,3,4,5]"}

func (g *castGen) vecText(dims int) string {
	s := "["
	for i := 0; i < dims; i++ {
		if i > 0 {
			s += ","
		}
		if g.pick(3) == 0 {
			s += fmt.Sprint(g.pick(200) - 100)
		} else {
			s += vecElems[g.pick(len(vecElems))]
		}
	}
	return s + "]"
}

// vecCol: a vector column whose rows have `dims` dimensions (or random dimensions when
// dims < 0), with NULLs and zero vectors.
func (g *castGen) vecCol(dims int) (*types.FieldType, func() []any) {
	ft := castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)
	return ft, func() []any {
		if g.pick(7) == 0 {
			return []any{types.KindNull}
		}
		d := dims
		if d < 0 || g.pick(10) == 0 {
			d = g.pick(6)
		}
		if g.pick(9) == 0 {
			s := "["
			for i := 0; i < d; i++ {
				if i > 0 {
					s += ","
				}
				s += "0"
			}
			return []any{types.KindVectorFloat32, s + "]"}
		}
		return []any{types.KindVectorFloat32, g.vecText(d)}
	}
}

func (g *castGen) vecTextCol() (*types.FieldType, func() []any) {
	ft := castFt(mysql.TypeVarchar, 0, 64, 0)
	return ft, func() []any {
		if g.pick(7) == 0 {
			return []any{types.KindNull}
		}
		s := vecTexts[g.pick(len(vecTexts))]
		if g.pick(3) == 0 {
			s = g.vecText(g.pick(5))
		}
		return []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}
	}
}

func (g *castGen) vecConst(dims int) any {
	ft := castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)
	if g.pick(8) == 0 {
		return []any{"const", []any{types.KindNull}, sfFtIn(ft)}
	}
	return []any{"const", []any{types.KindVectorFloat32, g.vecText(dims)}, sfFtIn(ft)}
}

func castGenerateD081c(vg *vec.Gen, g *castGen) {
	for i := 0; i < 2400; i++ {
		name := vecFnNames[g.pick(len(vecFnNames))]
		dims := g.pick(5)
		if g.pick(6) == 0 {
			dims = -1
		}
		fts := []*types.FieldType{}
		gens := []func() []any{}
		cols := []any{}
		for c := 0; c < 3; c++ {
			var ft *types.FieldType
			var gen func() []any
			switch {
			case c == 2:
				ft, gen = g.vecTextCol()
			case g.pick(10) == 0:
				ft, gen = g.arCol()
			default:
				ft, gen = g.vecCol(dims)
			}
			fts = append(fts, ft)
			gens = append(gens, gen)
			cols = append(cols, sfFtIn(ft))
		}
		arg := func(c int) any {
			switch g.pick(10) {
			case 0:
				d := dims
				if d < 0 || g.pick(4) == 0 {
					d = g.pick(5)
				}
				return g.vecConst(d)
			case 1:
				return []any{"fn", ast.VecFromText, []any{[]any{"col", 2}}}
			case 2:
				return []any{"cast", sfFtIn(castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)), []any{"col", 2}}
			default:
				return []any{"col", c}
			}
		}
		var args []any
		switch name {
		case ast.VecFromText:
			args = []any{[]any{"col", 2}}
			if g.pick(4) == 0 {
				args = []any{[]any{"fn", ast.VecAsText, []any{arg(0)}}}
			}
		case ast.VecDims, ast.VecL2Norm, ast.VecAsText:
			args = []any{arg(0)}
		default:
			args = []any{arg(0), arg(1)}
		}
		n := g.pick(16)
		vg.Add("vec", "vecbuild", []int{0, 0, 1, 2}[g.pick(4)], name, args, g.arCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// A failing second argument on rows where the first is NULL.
	for i := 0; i < 400; i++ {
		dims := g.pick(4)
		ft0, gen0 := g.vecCol(dims)
		ft1 := castFt(mysql.TypeVarchar, 0, 64, 0)
		n := 1 + g.pick(12)
		rows := []any{}
		for r := 0; r < n; r++ {
			v0 := gen0()
			var s string
			if v0[0] == types.KindNull && g.pick(2) == 0 {
				s = []string{"abc", "[1,", "[NaN]", "[1e39]"}[g.pick(4)]
			} else {
				s = g.vecText(dims)
			}
			rows = append(rows, []any{v0, []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}})
		}
		name := vecDistNames[g.pick(len(vecDistNames))]
		vg.Add("vecfb", "vecbuild", []int{0, 0, 1}[g.pick(3)], name,
			[]any{[]any{"col", 0}, []any{"fn", ast.VecFromText, []any{[]any{"col", 1}}}}, g.arCtx(),
			[]any{sfFtIn(ft0), sfFtIn(ft1)}, rows, g.arSel(n))
	}
	// ExtractVectorHelper.
	for i := 0; i < 300; i++ {
		vft := castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)
		fts := []*types.FieldType{vft, vft, castFt(mysql.TypeLonglong, 0, 20, 0), castFt(mysql.TypeVarchar, 0, 64, 0)}
		cols := []any{}
		for _, ft := range fts {
			cols = append(cols, sfFtIn(ft))
		}
		dims := g.pick(4)
		recipes := []any{}
		for k := 0; k < 1+g.pick(4); k++ {
			c := []any{"col", g.pick(2)}
			cn := g.vecConst(dims)
			if cn.([]any)[1].([]any)[0] == types.KindNull {
				cn = []any{"const", []any{types.KindVectorFloat32, g.vecText(dims)}, sfFtIn(vft)}
			}
			var r any
			switch g.pick(10) {
			case 0:
				r = []any{"fn", vecDistNames[g.pick(4)], []any{[]any{"col", 0}, []any{"col", 1}}}
			case 1:
				r = []any{"fn", vecDistNames[g.pick(4)], []any{cn, g.vecConst(dims)}}
			case 2:
				r = []any{"fn", ast.VecL2Norm, []any{c}}
			case 3:
				r = c
			case 4:
				r = []any{"fn", ast.EQ, []any{[]any{"col", 2}, []any{"col", 2}}}
			case 5:
				r = []any{"fn", vecDistNames[g.pick(4)], []any{c, []any{"fn", ast.VecFromText, []any{[]any{"col", 3}}}}}
			case 6:
				r = []any{"fn", vecDistNames[g.pick(4)], []any{cn, c}}
			default:
				r = []any{"fn", vecDistNames[g.pick(4)], []any{c, cn}}
			}
			recipes = append(recipes, r)
		}
		vg.Add("vsh", "vsh", recipes, g.arCtx(), cols)
	}
	// NULL vector constants (appended): a distance of one vector column and one NULL
	// constant reaches GetVectorFloat32 on the NULL datum, which panics ("bad
	// VectorFloat32 value header"); from SQL, ORDER BY vec_l2_distance(a, NULL) LIMIT 1
	// fails with ERROR 1105 (tidb-rs#117). Two NULL constants or two columns stop at the count.
	vft := castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)
	null := []any{"const", []any{types.KindNull}, sfFtIn(vft)}
	for i := 0; i < 300; i++ {
		cols := []any{sfFtIn(vft), sfFtIn(vft)}
		dims := g.pick(4)
		recipes := []any{}
		for k := 0; k < 1+g.pick(3); k++ {
			name := vecDistNames[g.pick(4)]
			c := []any{"col", g.pick(2)}
			var r any
			switch g.pick(6) {
			case 0:
				r = []any{"fn", name, []any{null, c}}
			case 1:
				r = []any{"fn", name, []any{null, null}}
			case 2:
				r = []any{"fn", name, []any{null, g.vecConst(dims)}}
			case 3:
				r = []any{"fn", name, []any{c, g.vecConst(dims)}}
			default:
				r = []any{"fn", name, []any{c, null}}
			}
			recipes = append(recipes, r)
		}
		vg.Add("vshnull", "vsh", recipes, g.arCtx(), cols)
	}
}
