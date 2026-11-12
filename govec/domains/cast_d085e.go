package domains

// Domain "cast", D08.5 batch 5 (appended at the end; earlier records are unchanged): CONV and
// CRC32, the last two functions of builtin_math.go.
//   - "convbuild": the "arbuild" op of batch 1 under its own op name (NewFunctionBase /
//     NewFunction / NewFunctionTryFold of name(args...), then EvalExpr with vectorization off
//     and on). builtinConvSig.vectorized() is false, so EvalExpr takes the row version in both
//     modes. Its vecEvalString is unreachable in v8.5.8: ScalarFunction.Vectorized() checks
//     vectorized() first, so isChildrenVectorized never runs and bufAllocator stays nil;
//     calling VecEvalString directly nil-dereferences it (tried while writing this op).

import (
	"encoding/hex"
	"encoding/json"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

func convGoFuncs() []string {
	return []string{
		"builtinCRC32Sig.Clone",
		"builtinCRC32Sig.evalInt",
		"builtinCRC32Sig.vecEvalInt",
		"builtinCRC32Sig.vectorized",
		"builtinConvSig.Clone",
		"builtinConvSig.conv",
		"builtinConvSig.evalString",
		"builtinConvSig.vectorized",
		"convFunctionClass.getFunction",
		"crc32FunctionClass.getFunction",
		"getValidPrefix",
	}
}

func init() {
	castCoverage["convbuild"] = vec.Coverage{Min: 2000, GoFuncs: convGoFuncs()}
}

func castExecD085e(op string, p []json.RawMessage) any {
	if op != "convbuild" {
		return nil
	}
	return castExecD085("arbuild", p)
}

// convStrs: strings for CONV's N: digits of several bases, signs, spaces (ASCII and Unicode,
// which strings.TrimSpace removes), the uint64 / int64 limits and beyond, junk, non-ASCII
// digits and letters (Latin-1 and UTF-8 bytes end getValidPrefix).
var convStrs = []string{
	"", "0", "1", "12", "-12", "+12", " 12 ", "\t-ff\n", "FF", "ff", "zz", "ZZ", "1z", "-", "+",
	"+-1", "--1", "-+1", "18446744073709551615", "18446744073709551616", "9223372036854775807",
	"9223372036854775808", "-9223372036854775808", "-9223372036854775809", "ffffffffffffffff",
	"10000000000000000", "-ffffffffffffffff", "1111", "0x1f", "0b101", "1e3", "1.5", "12abc", "abc",
	"1_000", "+0", "-0", " 12", "12 ", "　12", "\u0085-7", "١٢", "１２", "12\xe9", "\xb5",
	"\xff1", "3w5e11264sgsg", "zzzzzzzzzzzzzz", "-zzzzzzzzzzzzz", "777777777777777777777", "7fffffffffffffff",
	"8000000000000000", "-8000000000000000", "1000000000000000000000000000000000000000000000000000000000000000",
}

var convBases = []int64{2, 8, 10, 16, 36, -2, -8, -10, -16, -36, 1, 0, -1, 37, -37, 3, 35, -35, 1 << 40, -(1 << 62)}

// convStrCol: a VARCHAR column of convStrs in a random charset / collation of castCharsets.
func (g *castGen) convStrCol() (*types.FieldType, func() []any) {
	cs := castCharsets[g.pick(len(castCharsets))]
	ft := castFt(mysql.TypeVarchar, 0, 64, 0)
	ft.SetCharset(cs[0])
	ft.SetCollate(cs[1])
	if cs[0] == charset.CharsetBin {
		ft.AddFlag(mysql.BinaryFlag)
	}
	return ft, func() []any {
		if g.pick(8) == 0 {
			return []any{types.KindNull}
		}
		return []any{types.KindString, vec.GoString(convStrs[g.pick(len(convStrs))]), cs[1]}
	}
}

// convBinLit: a binary literal constant (x'..' / b'..'), typed as the parser types it.
func (g *castGen) convBinLit() any {
	b := []string{"", "01", "05", "ff", "0a0b", "8000000000000000", "ffffffffffffffff",
		"010000000000000000", "3132", "414243"}[g.pick(10)]
	raw, _ := hex.DecodeString(b)
	ft := castFt(mysql.TypeVarString, mysql.BinaryFlag, len(raw), 0)
	ft.SetCharset(charset.CharsetBin)
	ft.SetCollate(charset.CollationBin)
	return []any{"const", []any{types.KindBinaryLiteral, b}, sfFtIn(ft)}
}

func (g *castGen) convBase(fts []*types.FieldType) any {
	switch g.pick(10) {
	case 0:
		ft := types.NewFieldType(mysql.TypeNull)
		return []any{"const", []any{types.KindNull}, sfFtIn(ft)}
	case 1:
		if len(fts) > 1 {
			return []any{"col", len(fts) - 1}
		}
	}
	return []any{"const", []any{types.KindInt64, convBases[g.pick(len(convBases))]}, sfFtIn(castFt(mysql.TypeLonglong, 0, 20, 0))}
}

// convArg0: CONV's N: a string column, a column of every kind (hybrid included), a binary
// literal, a CAST of a column or of a binary literal (the two hybrid branches of evalString),
// or a constant.
func (g *castGen) convArg0(fts []*types.FieldType) any {
	strTarget := func() *types.FieldType {
		ft := castFt(mysql.TypeVarString, 0, []int{-1, 2, 64}[g.pick(3)], 0)
		return ft
	}
	switch g.pick(8) {
	case 0, 1, 2:
		return []any{"col", 0}
	case 3:
		return g.convBinLit()
	case 4:
		return []any{"cast", sfFtIn(strTarget()), []any{"col", 0}}
	case 5:
		return []any{"cast", sfFtIn(strTarget()), g.convBinLit()}
	case 6:
		s := convStrs[g.pick(len(convStrs))]
		return []any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(castFt(mysql.TypeVarString, 0, len(s), 0))}
	default:
		return g.mathConst()
	}
}

func castGenerateD085e(vg *vec.Gen, g *castGen) {
	// CONV(N, from, to): N of every form, bases valid and invalid, signed and unsigned.
	for i := 0; i < 2000; i++ {
		var ft0 *types.FieldType
		var gen0 func() []any
		switch g.pick(3) {
		case 0, 1:
			ft0, gen0 = g.convStrCol()
		default:
			ft0, gen0 = g.arCol() // numbers, strings, FLOAT, BIT / ENUM / SET, time, YEAR
		}
		fts := []*types.FieldType{ft0}
		gens := []func() []any{gen0}
		cols := []any{sfFtIn(ft0)}
		if g.pick(4) == 0 {
			bft := castFt(mysql.TypeLonglong, 0, 20, 0)
			fts = append(fts, bft)
			gens = append(gens, func() []any {
				if g.pick(8) == 0 {
					return []any{types.KindNull}
				}
				return []any{types.KindInt64, convBases[g.pick(len(convBases))]}
			})
			cols = append(cols, sfFtIn(bft))
		}
		args := []any{g.convArg0(fts), g.convBase(fts), g.convBase(fts)}
		if g.pick(40) == 0 {
			args = args[:g.pick(3)] // a wrong argument count, sometimes
		}
		n := g.pick(12)
		vg.Add("conv", "convbuild", []int{0, 0, 1, 2}[g.pick(4)], ast.Conv, args, g.mathCtx(), cols,
			g.arRows(gens, n), g.arSel(n))
	}
	// CRC32(expr): strings of every charset, numbers, hybrid, binary literals, constants.
	for i := 0; i < 800; i++ {
		var ft0 *types.FieldType
		var gen0 func() []any
		switch g.pick(3) {
		case 0:
			ft0, gen0 = g.convStrCol()
		case 1:
			ft0 = g.argFt()
			gen0 = func() []any { return g.val(ft0) }
		default:
			ft0, gen0 = g.arCol()
		}
		var a any = []any{"col", 0}
		switch g.pick(6) {
		case 0:
			a = g.convBinLit()
		case 1:
			a = g.mathConst()
		}
		args := []any{a}
		if g.pick(40) == 0 {
			args = append(args, a) // a wrong argument count, sometimes
		}
		n := g.pick(12)
		vg.Add("crc32", "convbuild", []int{0, 0, 1, 2}[g.pick(4)], ast.CRC32, args, g.mathCtx(), []any{sfFtIn(ft0)},
			g.arRows([]func() []any{gen0}, n), g.arSel(n))
	}
	// CONV of a CAST of a hybrid column (BIT / ENUM / SET / FLOAT, evalString's second
	// branch) or of a binary literal, and signed bases at the int64 limits (the clamps).
	for i := 0; i < 400; i++ {
		ft0, gen0 := g.convCol()
		strTarget := castFt(mysql.TypeVarString, 0, []int{-1, 64}[g.pick(2)], 0)
		var a0 any = []any{"cast", sfFtIn(strTarget), []any{"col", 0}}
		if g.pick(4) == 0 {
			a0 = []any{"cast", sfFtIn(strTarget), g.convBinLit()}
		}
		n := 1 + g.pick(10)
		vg.Add("convhybrid", "convbuild", []int{0, 0, 1, 2}[g.pick(4)], ast.Conv,
			[]any{a0, g.convBase(nil), g.convBase(nil)}, g.mathCtx(), []any{sfFtIn(ft0)},
			g.arRows([]func() []any{gen0}, n), g.arSel(n))
	}
	limits := []string{"9223372036854775807", "9223372036854775808", "18446744073709551615",
		"-9223372036854775808", "-9223372036854775809", "-18446744073709551615", "7fffffffffffffff",
		"8000000000000000", "ffffffffffffffff", "-8000000000000000", "-ffffffffffffffff", "1", "-1"}
	for i := 0; i < 300; i++ {
		s := limits[g.pick(len(limits))]
		from := []int64{-10, -16, 10, 16, -36}[g.pick(5)]
		to := []int64{10, -10, 16, -16, 2, -2}[g.pick(6)]
		ft := castFt(mysql.TypeVarString, 0, len(s), 0)
		bft := castFt(mysql.TypeLonglong, 0, 20, 0)
		args := []any{
			[]any{"const", []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}, sfFtIn(ft)},
			[]any{"const", []any{types.KindInt64, from}, sfFtIn(bft)},
			[]any{"const", []any{types.KindInt64, to}, sfFtIn(bft)},
		}
		// one row of an unused column: the chunk needs a column to have a row
		vg.Add("convlimit", "convbuild", []int{0, 1}[g.pick(2)], ast.Conv, args, g.mathCtx(), []any{sfFtIn(bft)},
			[]any{[]any{[]any{types.KindInt64, 0}}}, nil)
	}
}
