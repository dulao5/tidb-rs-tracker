package domains

// Domain "cast", part 2b-2b-ii (appended after part 2b-2b-i; earlier records are unchanged):
// CAST to JSON from every argument type ("to-json", constant arguments "to-json-const"),
// CAST(json AS type ARRAY) ("to-array": array elements and scalars of every JSON type code,
// supported and unsupported element types, non-JSON arguments), CAST to VECTOR ("to-vec":
// strings, vector columns and the unsupported argument types),
// WrapWithCastAsJSON/VectorFloat32 ("wrap-json") and ConvertJSON2Tp ("json2tp").

import (
	"encoding/hex"
	"fmt"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

var castJSONSigs = []string{"IntAsJSON", "RealAsJSON", "DecimalAsJSON", "StringAsJSON", "TimeAsJSON",
	"DurationAsJSON", "JSONAsJSON"}

func castJSONSigFuncs() []string {
	out := []string{"castAsJSONFunctionClass.getFunction", "castAsArrayFunctionClass.getFunction",
		"castAsArrayFunctionClass.verifyArgs", "castAsVectorFloat32FunctionClass.getFunction",
		"castJSONAsArrayFunctionSig.evalJSON", "castJSONAsArrayFunctionSig.Clone", "convertJSON2Tp",
		"builtinCastVectorFloat32AsUnsupportedSig.evalJSON",
		"builtinCastUnsupportedAsVectorFloat32Sig.evalVectorFloat32", "builtinCastUnsupportedAsVectorFloat32Sig.Clone",
		"builtinCastStringAsVectorFloat32Sig.evalVectorFloat32", "builtinCastStringAsVectorFloat32Sig.Clone",
		"builtinCastVectorFloat32AsVectorFloat32Sig.evalVectorFloat32", "builtinCastVectorFloat32AsVectorFloat32Sig.Clone"}
	for _, s := range castJSONSigs {
		out = append(out, "builtinCast"+s+"Sig.evalJSON", "builtinCast"+s+"Sig.vecEvalJSON",
			"builtinCast"+s+"Sig.vectorized", "builtinCast"+s+"Sig.Clone")
	}
	return out
}

func init() {
	c := castCoverage["cast"]
	c.GoFuncs = append(c.GoFuncs, castJSONSigFuncs()...)
	castCoverage["cast"] = c
	w := castCoverage["wrap"]
	w.GoFuncs = append(w.GoFuncs, "WrapWithCastAsJSON", "WrapWithCastAsVectorFloat32")
	castCoverage["wrap"] = w
	castCoverage["json2tp"] = vec.Coverage{Min: 300, GoFuncs: []string{"ConvertJSON2Tp", "convertJSON2Tp"}}
}

// castJSONOut: a JSON value as [TypeCode, hex(Value)].
func castJSONOut(j types.BinaryJSON) any {
	return []any{j.TypeCode, hex.EncodeToString(j.Value)}
}

func castJSONIn(s string) []any {
	j, err := types.ParseBinaryJSONFromString(s)
	if err != nil {
		panic(err)
	}
	return []any{types.KindMysqlJSON, j.TypeCode, hex.EncodeToString(j.Value)}
}

// castJSONTarget: CAST(.. AS JSON).
func (g *castGen) jsonTarget() *types.FieldType {
	var flag uint = mysql.BinaryFlag
	if g.pick(4) == 0 {
		flag |= mysql.NotNullFlag
	}
	return castFt(mysql.TypeJSON, flag, -1, 0)
}

var castJSONTexts = []string{`{"a":1}`, `[1,"x",null]`, `"str"`, `123`, `-1.5e3`, `true`, `null`, `{`, `[1,`, ``,
	`  [ ]  `, `"é"`, `{"a":{"b":[1,2,{"c":3}]}}`, `1e400`, `abc`, `'x'`, `9223372036854775808`}

// toJSONArg: an argument column for CAST(.. AS JSON): every type, plus BOOLEAN, UNSIGNED and
// YEAR integers, binary strings (BINARY(n) keeps its padding in the opaque value) and JSON
// texts.
func (g *castGen) toJSONArg() (*types.FieldType, func() []any) {
	switch g.pick(8) {
	case 0:
		ft := castFt(mysql.TypeTiny, []uint{mysql.IsBooleanFlag, mysql.UnsignedFlag, 0}[g.pick(3)], 1, 0)
		return ft, func() []any { return []any{types.KindInt64, int64(g.pick(3) - 1)} }
	case 1:
		if g.pick(2) == 0 {
			// YEAR without the UNSIGNED flag: CAST(year AS JSON) is still an unsigned JSON number.
			ft := castFt(mysql.TypeYear, 0, 4, 0)
			return ft, func() []any { return []any{types.KindInt64, int64([]int{0, 1901, 2024, 2155}[g.pick(4)])} }
		}
		ft := castFt(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0)
		return ft, func() []any {
			return []any{types.KindInt64, []int64{0, 1, -1, 1 << 62, -9223372036854775808}[g.pick(5)]}
		}
	case 2:
		tp := []byte{mysql.TypeString, mysql.TypeVarString, mysql.TypeBlob, mysql.TypeVarchar}[g.pick(4)]
		ft := castFt(tp, mysql.BinaryFlag, []int{4, 8, 0, -1}[g.pick(4)], 0)
		ft.SetCharset(charset.CharsetBin)
		ft.SetCollate(charset.CollationBin)
		return ft, func() []any {
			b := make([]byte, g.pick(6))
			for i := range b {
				b[i] = byte(g.pick(256))
			}
			return []any{types.KindBytes, hex.EncodeToString(b)}
		}
	case 3:
		ft := castFt(mysql.TypeVarchar, 0, 64, 0)
		return ft, func() []any {
			return []any{types.KindString, vec.GoString(castJSONTexts[g.pick(len(castJSONTexts))]), charset.CollationUTF8MB4}
		}
	default:
		return g.toArg()
	}
}

// arrayTarget: CAST(.. AS type ARRAY): supported element types and the unsupported ones.
func (g *castGen) arrayTarget() *types.FieldType {
	var ft *types.FieldType
	switch g.pick(11) {
	case 0:
		ft = castFt(mysql.TypeLonglong, 0, 20, 0)
	case 1:
		ft = castFt(mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0)
	case 2, 3:
		cs := [][2]string{{charset.CharsetUTF8MB4, charset.CollationUTF8MB4}, {charset.CharsetBin, charset.CollationBin},
			{charset.CharsetLatin1, charset.CollationLatin1}}[g.pick(3)]
		ft = castFt(mysql.TypeVarchar, 0, []int{3, 10, types.UnspecifiedLength}[g.pick(3)], 0)
		ft.SetCharset(cs[0])
		ft.SetCollate(cs[1])
		if cs[0] == charset.CharsetBin {
			ft.AddFlag(mysql.BinaryFlag)
		}
	case 4:
		ft = castFt(mysql.TypeDate, 0, mysql.MaxDateWidth, 0)
	case 5:
		ft = castFt(mysql.TypeDatetime, 0, mysql.MaxDatetimeWidthNoFsp, g.pick(7))
	case 6:
		ft = castFt(mysql.TypeDuration, 0, mysql.MaxDurationWidthNoFsp, g.pick(7))
	case 7:
		ft = castFt(mysql.TypeDouble, 0, -1, -1)
	case 8:
		ft = castFt(mysql.TypeTimestamp, 0, mysql.MaxDatetimeWidthNoFsp, 0)
	default:
		ft = castFt([]byte{mysql.TypeYear, mysql.TypeJSON, mysql.TypeFloat, mysql.TypeNewDecimal}[g.pick(4)], 0, -1, 0)
	}
	if g.pick(4) == 0 {
		ft.AddFlag(mysql.NotNullFlag)
	}
	return ft
}

var castArrayJSONs = []string{`[1,2,3]`, `[-1,18446744073709551615]`, `["a","bcdefghijklmnop","é"]`, `[1,"a"]`, `[]`,
	`1`, `"x"`, `{"a":1}`, `[1.5,2]`, `[true]`, `[null]`, `[[1]]`, `9223372036854775807`, `-5`, `[0.0]`}

// arrayCol: JSON values for CAST(.. ARRAY): texts, and arrays of dates, datetimes and
// durations (CreateBinaryJSON of Time/Duration values).
func (g *castGen) arrayCol() (*types.FieldType, func() []any) {
	ft := castFt(mysql.TypeJSON, mysql.BinaryFlag, -1, 0)
	return ft, func() []any {
		if g.pick(3) == 0 {
			_, gen := g.jsonTimeCol()
			v := gen()
			j := types.BinaryJSON{TypeCode: v[1].(byte), Value: must2(hex.DecodeString(v[2].(string)))}
			arr := types.CreateBinaryJSON([]any{j, j})
			if g.pick(2) == 0 {
				return v
			}
			return []any{types.KindMysqlJSON, arr.TypeCode, hex.EncodeToString(arr.Value)}
		}
		return castJSONIn(castArrayJSONs[g.pick(len(castArrayJSONs))])
	}
}

func must2[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

var castVecStrs = []string{"[1,2,3]", "[]", "[1.5]", "[1,2]", "[1e39]", "[nan]", "abc", "[1,2,3,4,5]", " [ 1 , 2 ] ",
	"[-0.0]", "[1,]", "[3.4e38,-3.4e38]", ""}

// vecTarget: CAST(.. AS VECTOR[(dims)]).
func (g *castGen) vecTarget() *types.FieldType {
	return castFt(mysql.TypeTiDBVectorFloat32, 0, []int{types.UnspecifiedLength, 0, 1, 2, 3}[g.pick(5)], 0)
}

func castGenerate2b2c(vg *vec.Gen, g *castGen) {
	rows := func(gen func() []any, n int) []any {
		out := make([]any, 0, n)
		for j := 0; j < n; j++ {
			if g.pick(8) == 0 {
				out = append(out, []any{types.KindNull})
			} else {
				out = append(out, gen())
			}
		}
		return out
	}
	for i := 0; i < 1500; i++ {
		ft, gen := g.toJSONArg()
		vg.Add("to-json", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(g.jsonTarget()), false, g.timeCtx(), rows(gen, 1+g.pick(16)))
	}
	// Constant arguments (CAST to JSON is not folded: the build dump shows the function).
	for i := 0; i < 200; i++ {
		ft, gen := g.toJSONArg()
		vg.Add("to-json-const", "cast", []any{"const", gen(), sfFtIn(ft)}, sfFtIn(g.jsonTarget()), false, g.timeCtx(), nil)
	}
	for i := 0; i < 600; i++ {
		ft, gen := g.arrayCol()
		if g.pick(10) == 0 {
			ft, gen = g.toArg() // verifyArgs: a non-JSON argument
		}
		vg.Add("to-array", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(g.arrayTarget()), false, g.timeCtx(),
			rows(gen, 1+g.pick(8)), false, true)
	}
	for i := 0; i < 600; i++ {
		var ft *types.FieldType
		var gen func() []any
		switch g.pick(3) {
		case 0:
			ft = castFt(mysql.TypeVarchar, 0, 64, 0)
			gen = func() []any {
				return []any{types.KindString, vec.GoString(castVecStrs[g.pick(len(castVecStrs))]), charset.CollationUTF8MB4}
			}
		case 1:
			ft = castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)
			gen = func() []any {
				n := g.pick(4)
				s := "["
				for k := 0; k < n; k++ {
					if k > 0 {
						s += ","
					}
					s += fmt.Sprint(g.pick(100) - 50)
				}
				return []any{types.KindVectorFloat32, s + "]"}
			}
		default:
			ft, gen = g.toArg()
		}
		vg.Add("to-vec", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(g.vecTarget()), false, g.timeCtx(), rows(gen, 1+g.pick(8)))
	}
	for i := 0; i < 300; i++ {
		var ft *types.FieldType
		var gen func() []any
		if g.pick(3) == 0 {
			ft, gen = g.toJSONArg()
			if g.pick(2) == 0 {
				ft.AddFlag(mysql.ParseToJSONFlag)
			}
		} else {
			ft, gen = g.toArg()
		}
		a := []any{"col", sfFtIn(ft)}
		if g.pick(2) == 0 {
			a = []any{"const", gen(), sfFtIn(ft)}
		}
		vg.Add("wrap-json", "wrap", []string{"json", "vector"}[g.pick(2)], a, g.timeCtx())
	}
	// ConvertJSON2Tp on scalars mostly (an array or object is always ErrInvalidJSONForFuncIndex).
	scalars := []string{`1`, `-5`, `18446744073709551615`, `9223372036854775807`, `1.5`, `"x"`, `"abcdefghijk"`,
		`"é"`, `true`, `null`, `0`, `-0.0`, `"2024-01-02"`, `1e300`}
	for i := 0; i < 300; i++ {
		var v []any
		switch g.pick(4) {
		case 0, 1:
			v = castJSONIn(scalars[g.pick(len(scalars))])
		case 2:
			_, gen := g.jsonTimeCol()
			v = gen()
		default:
			_, gen := g.arrayCol()
			v = gen()
		}
		vg.Add("json2tp", "json2tp", v, sfFtIn(g.arrayTarget()))
	}
}
