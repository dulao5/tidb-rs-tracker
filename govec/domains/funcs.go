package domains

// Domain "funcs", block D08.2 part 1: the function registry (`funcs` in builtin.go), building
// a signature's baseBuiltinFunc (result type, collation, NOT NULL flag) and the collation
// derivation of built-in functions (collation.go).
//
// The arguments of a record are leaves (columns, constants) and small function trees, given
// as expr_sf.go recipes; the built arguments are returned as dumps ("args") for tidb-rs to
// rebuild (it does not build functions yet). Explicit coercibility/repertoire overrides are
// applied to the built arguments first (an explicit COLLATE clause sets CoercibilityExplicit).
//
// Records:
//   - "derive": [nc, ctxCharset, ctxCollation, funcName, recipes, retType, argTps, overrides]
//     -> args, deriveCollation (ExprCollation or error or panic).
//   - "newbase": [nc, ctxCharset, ctxCollation, funcName, recipes, ftIn, overrides, withFt]
//     -> args, newBaseBuiltinFunc / newBaseBuiltinFuncWithFieldType: result type, collation
//     info, the collator's key of a probe string.
//   - "retft": [funcName, retType, ec] -> newReturnFieldTypeForBaseBuiltinFunc.
//   - "verify": [name, minArgs, maxArgs, n] -> VerifyArgsWrapper, baseFunctionClass.verifyArgs.
//   - "setflen": [ftIn, which, fsp] -> setDecimalAndFlenForDatetime/Date/Time.
//   - "registry": [] -> RegisteredBuiltinFunctionNames, GetBuiltinList, IsFunctionSupported and
//     GetDisplayName of every name and some probes.
//   - "baseerr": [] -> the errors of baseBuiltinFunc's evalXxx/vecEvalXxx, metadata,
//     RequiredOptionalEvalProps, setPbCode/PbCode.
//   - "deferred": [name, sysdateIsNow] -> IsDeferredFunctions.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tipb/go-tipb"
	uatomic "go.uber.org/atomic"
)

const funcsSeed = 20261113

func init() {
	if err := fnCheckMirror(); err != nil {
		panic("funcs: " + err.Error())
	}
	vec.Register(&vec.Domain{
		Name:     "funcs",
		Seed:     funcsSeed,
		Coverage: funcsCoverage,
		Blocks:   []string{"D08.2"},
		Exempt:   funcsExempt(),
		Gzip:     true,
		Generate: funcsGenerate,
		Exec:     funcsExec,
	})
}

var funcsCoverage = map[string]vec.Coverage{
	"derive": {Min: 3400, GoFuncs: []string{"deriveCollation", "CheckAndDeriveCollationFromExprs",
		"inferCollation", "safeConvert", "fixStringTypeForMaxLength", "isUnicodeCollation", "isBinCollation",
		"getBinCollation", "illegalMixCollationErr", "deriveCoercibilityForColumn", "deriveCoercibilityForConstant",
		"collationInfo.HasCoercibility", "collationInfo.Coercibility", "collationInfo.SetCoercibility",
		"collationInfo.Repertoire", "collationInfo.SetRepertoire", "collationInfo.SetCharsetAndCollation",
		"collationInfo.CharsetAndCollation", "collationInfo.IsExplicitCharset", "collationInfo.SetExplicitCharset"}},
	"newbase": {Min: 1000, GoFuncs: []string{"newBaseBuiltinFunc", "adjustNullFlagForReturnType",
		"newBaseBuiltinFuncWithFieldType", "baseBuiltinFunc.setCollator", "baseBuiltinFunc.collator"}},
	"retft":    {Min: 200, GoFuncs: []string{"newReturnFieldTypeForBaseBuiltinFunc"}},
	"verify":   {Min: 300, GoFuncs: []string{"VerifyArgsWrapper", "baseFunctionClass.verifyArgs", "baseFunctionClass.verifyArgsByCount"}},
	"setflen":  {Min: 200, GoFuncs: []string{"baseBuiltinFunc.setDecimalAndFlenForDatetime", "baseBuiltinFunc.setDecimalAndFlenForDate", "baseBuiltinFunc.setDecimalAndFlenForTime"}},
	"registry": {Min: 1, GoFuncs: []string{"RegisteredBuiltinFunctionNames", "GetBuiltinList", "IsFunctionSupported", "GetDisplayName"}},
	"baseerr": {Min: 1, GoFuncs: []string{"baseBuiltinFunc.evalInt", "baseBuiltinFunc.evalReal", "baseBuiltinFunc.evalString",
		"baseBuiltinFunc.evalDecimal", "baseBuiltinFunc.evalTime", "baseBuiltinFunc.evalDuration", "baseBuiltinFunc.evalJSON",
		"baseBuiltinFunc.evalVectorFloat32", "baseBuiltinFunc.vecEvalInt", "baseBuiltinFunc.vecEvalReal",
		"baseBuiltinFunc.vecEvalString", "baseBuiltinFunc.vecEvalDecimal", "baseBuiltinFunc.vecEvalTime",
		"baseBuiltinFunc.vecEvalDuration", "baseBuiltinFunc.vecEvalJSON", "baseBuiltinFunc.vecEvalVectorFloat32",
		"baseBuiltinFunc.metadata", "baseBuiltinFunc.RequiredOptionalEvalProps", "baseBuiltinFunc.setPbCode",
		"baseBuiltinFunc.PbCode"}},
	"deferred": {Min: 300, GoFuncs: []string{"IsDeferredFunctions"}},
}

// ---------------------------------------------------------------- unexported TiDB code

// fnBaseMirror has the layout of expression.baseBuiltinFunc, checked field by field in init
// (fnCheckMirror). Field 0 is the one listed exception: Go's bufAllocator is a non-empty
// interface (columnBufferAllocator: itab + data) and the mirror's is `any` (type + data);
// same size, different meaning of the first word. The mirror must never read it: no %v,
// ==, reflect.DeepEqual or type switch on a mirror value.
type fnBaseMirror struct {
	bufAllocator           any
	args                   []expression.Expression
	tp                     *types.FieldType
	pbCode                 tipb.ScalarFuncSig
	ctor                   collate.Collator
	childrenVectorized     bool
	childrenVectorizedOnce *sync.Once
	coer                   expression.Coercibility
	coerInit               uatomic.Bool
	repertoire             expression.Repertoire
	charset                string
	collation              string
	isExplicitCharset      bool
}

// fnCheckMirror compares fnBaseMirror with the baseBuiltinFunc embedded in a real signature
// (built through the exported API): every field's name, offset and type, embedded structs of
// pkg/expression flattened, and the size (Lucky, review of tidb-rs #80). bufAllocator is the
// only allowed difference (see fnBaseMirror).
func fnCheckMirror() error {
	ctx := exprstatic.NewExprContext()
	a := &expression.Column{RetType: types.NewFieldType(mysql.TypeLonglong), UniqueID: 1}
	sf, err := expression.NewFunction(ctx, ast.Plus, types.NewFieldType(mysql.TypeLonglong), a, a)
	if err != nil {
		return err
	}
	sig := reflect.ValueOf(sf.(*expression.ScalarFunction).Function).Elem().Type()
	f, ok := sig.FieldByName("baseBuiltinFunc")
	if !ok {
		return fmt.Errorf("%v has no baseBuiltinFunc", sig)
	}
	type field struct {
		name string
		tp   reflect.Type
		off  uintptr
	}
	var flat func(t reflect.Type, base uintptr) []field
	flat = func(t reflect.Type, base uintptr) []field {
		var out []field
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Type.PkgPath() == "github.com/pingcap/tidb/pkg/expression" {
				out = append(out, flat(f.Type, base+f.Offset)...)
				continue
			}
			out = append(out, field{f.Name, f.Type, base + f.Offset})
		}
		return out
	}
	mt := reflect.TypeOf(fnBaseMirror{})
	real, mir := flat(f.Type, 0), flat(mt, 0)
	if len(real) != len(mir) || f.Type.Size() != mt.Size() {
		return fmt.Errorf("fnBaseMirror: %d fields/%d bytes, baseBuiltinFunc %d fields/%d bytes", len(mir), mt.Size(), len(real), f.Type.Size())
	}
	for i := range real {
		r, m := real[i], mir[i]
		if r.name != m.name || r.off != m.off {
			return fmt.Errorf("fnBaseMirror field %d: %s@%d, baseBuiltinFunc %s@%d", i, m.name, m.off, r.name, r.off)
		}
		if r.tp.String() != m.tp.String() && !(r.name == "bufAllocator" && r.tp.Kind() == reflect.Interface && m.tp.Kind() == reflect.Interface && r.tp.Size() == m.tp.Size()) {
			return fmt.Errorf("fnBaseMirror field %s: %v, baseBuiltinFunc %v", r.name, m.tp, r.tp)
		}
	}
	return nil
}

// fnClassMirror has the layout of expression.baseFunctionClass.
type fnClassMirror struct {
	funcName string
	minArgs  int
	maxArgs  int
}

//go:linkname fnDeriveCollation github.com/pingcap/tidb/pkg/expression.deriveCollation
func fnDeriveCollation(ctx expression.BuildContext, funcName string, args []expression.Expression, retType types.EvalType, argTps ...types.EvalType) (*expression.ExprCollation, error)

//go:linkname fnNewBase github.com/pingcap/tidb/pkg/expression.newBaseBuiltinFunc
func fnNewBase(ctx expression.BuildContext, funcName string, args []expression.Expression, tp *types.FieldType) (fnBaseMirror, error)

//go:linkname fnNewBaseWithFt github.com/pingcap/tidb/pkg/expression.newBaseBuiltinFuncWithFieldType
func fnNewBaseWithFt(tp *types.FieldType, args []expression.Expression) (fnBaseMirror, error)

//go:linkname fnRetFt github.com/pingcap/tidb/pkg/expression.newReturnFieldTypeForBaseBuiltinFunc
func fnRetFt(funcName string, retType types.EvalType, ec *expression.ExprCollation) *types.FieldType

//go:linkname fnVerifyArgs github.com/pingcap/tidb/pkg/expression.(*baseFunctionClass).verifyArgs
func fnVerifyArgs(b *fnClassMirror, args []expression.Expression) error

//go:linkname fnSetDatetime github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).setDecimalAndFlenForDatetime
func fnSetDatetime(b *fnBaseMirror, fsp int)

//go:linkname fnSetDate github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).setDecimalAndFlenForDate
func fnSetDate(b *fnBaseMirror)

//go:linkname fnSetTime github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).setDecimalAndFlenForTime
func fnSetTime(b *fnBaseMirror, fsp int)

//go:linkname fnEvalInt github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalInt
func fnEvalInt(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (int64, bool, error)

//go:linkname fnEvalReal github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalReal
func fnEvalReal(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (float64, bool, error)

//go:linkname fnEvalString github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalString
func fnEvalString(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (string, bool, error)

//go:linkname fnEvalDecimal github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalDecimal
func fnEvalDecimal(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (*types.MyDecimal, bool, error)

//go:linkname fnEvalTime github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalTime
func fnEvalTime(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (types.Time, bool, error)

//go:linkname fnEvalDuration github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalDuration
func fnEvalDuration(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (types.Duration, bool, error)

//go:linkname fnEvalJSON github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalJSON
func fnEvalJSON(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (types.BinaryJSON, bool, error)

//go:linkname fnEvalVec github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).evalVectorFloat32
func fnEvalVec(b *fnBaseMirror, ctx expression.EvalContext, row chunk.Row) (types.VectorFloat32, bool, error)

type fnVecEval = func(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalInt github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalInt
func fnVecEvalInt(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalReal github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalReal
func fnVecEvalReal(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalString github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalString
func fnVecEvalString(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalDecimal github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalDecimal
func fnVecEvalDecimal(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalTime github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalTime
func fnVecEvalTime(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalDuration github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalDuration
func fnVecEvalDuration(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalJSON github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalJSON
func fnVecEvalJSON(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnVecEvalVec github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).vecEvalVectorFloat32
func fnVecEvalVec(b *fnBaseMirror, ctx expression.EvalContext, input *chunk.Chunk, result *chunk.Column) error

//go:linkname fnMetadata github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).metadata
func fnMetadata(b *fnBaseMirror) any

//go:linkname fnReqProps github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).RequiredOptionalEvalProps
func fnReqProps(b *fnBaseMirror) uint64

//go:linkname fnSetPbCode github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).setPbCode
func fnSetPbCode(b *fnBaseMirror, c tipb.ScalarFuncSig)

//go:linkname fnPbCode github.com/pingcap/tidb/pkg/expression.(*baseBuiltinFunc).PbCode
func fnPbCode(b *fnBaseMirror) tipb.ScalarFuncSig

// ---------------------------------------------------------------- inputs

// fnCharsets: the (charset, collation) pairs of string leaves and of the connection.
var fnCharsets = [][2]string{
	{"utf8mb4", "utf8mb4_bin"}, {"utf8mb4", "utf8mb4_general_ci"}, {"utf8mb4", "utf8mb4_unicode_ci"},
	{"utf8mb4", "utf8mb4_0900_ai_ci"}, {"utf8mb4", "utf8mb4_0900_bin"}, {"utf8", "utf8_bin"},
	{"utf8", "utf8_general_ci"}, {"utf8", "utf8_unicode_ci"}, {"ascii", "ascii_bin"},
	{"latin1", "latin1_bin"}, {"binary", "binary"}, {"gbk", "gbk_bin"}, {"gbk", "gbk_chinese_ci"},
}

// fnDeriveNames: every function name deriveCollation has a case for, and some without.
var fnDeriveNames = []string{
	ast.Concat, ast.ConcatWS, ast.Lower, ast.Lcase, ast.Reverse, ast.Upper, ast.Ucase, ast.Quote, ast.Coalesce,
	ast.Greatest, ast.Least, ast.Left, ast.Right, ast.Repeat, ast.Trim, ast.LTrim, ast.RTrim, ast.Substr,
	ast.SubstringIndex, ast.Replace, ast.Substring, ast.Mid, ast.Translate, ast.InsertFunc, ast.Lpad, ast.Rpad,
	ast.Elt, ast.ExportSet, ast.MakeSet, ast.FindInSet, ast.Regexp, ast.Field, ast.RegexpReplace, ast.Locate,
	ast.Instr, ast.Position, ast.RegexpLike, ast.RegexpSubstr, ast.RegexpInStr, ast.GE, ast.LE, ast.GT, ast.LT,
	ast.EQ, ast.NE, ast.NullEQ, ast.Strcmp, ast.If, ast.Ifnull, ast.Like, ast.Ilike, ast.In, ast.DateFormat,
	ast.TimeFormat, ast.Cast, ast.Case, ast.Database, ast.User, ast.CurrentUser, ast.Version, ast.CurrentRole,
	ast.TiDBVersion, ast.CurrentResourceGroup, ast.Format, ast.Space, ast.ToBase64, ast.UUID, ast.Hex, ast.MD5,
	ast.SHA, ast.SHA2, ast.SM3, ast.JSONPretty, ast.JSONQuote,
	ast.Plus, ast.Minus, ast.Mul, ast.Div, ast.IntDiv, ast.Mod, ast.IsNull, ast.Abs, ast.JSONExtract, ast.UnaryNot,
	ast.LogicAnd, ast.IsTruthWithNull, ast.Length, "no_such_function",
}

var fnEvalTypes = []types.EvalType{types.ETInt, types.ETReal, types.ETDecimal, types.ETString, types.ETDatetime,
	types.ETTimestamp, types.ETDuration, types.ETJson, types.ETVectorFloat32}

type fnGen struct {
	rng *rand.Rand
	uid int64
}

func (g *fnGen) cs() [2]string { return fnCharsets[g.rng.IntN(len(fnCharsets))] }

// strVal: a string value, sometimes invalid in some charsets.
func (g *fnGen) strVal() string {
	return []string{"", "a", "abc", "ABC ", "é", "中文", "\xff\xfe", "a\x00b", "ß", "😀", "Ab", "ae"}[g.rng.IntN(12)]
}

func (g *fnGen) leafFt() *types.FieldType {
	switch g.rng.IntN(14) {
	case 0, 1, 2, 3:
		c := g.cs()
		tp := []byte{mysql.TypeVarchar, mysql.TypeString, mysql.TypeBlob, mysql.TypeVarString}[g.rng.IntN(4)]
		ft := types.NewFieldTypeBuilder().SetType(tp).SetFlen(g.rng.IntN(300)).SetCharset(c[0]).SetCollate(c[1]).BuildP()
		if c[0] == charset.CharsetBin {
			ft.AddFlag(mysql.BinaryFlag)
		}
		return ft
	case 4:
		c := g.cs()
		ft := types.NewFieldTypeBuilder().SetType(mysql.TypeEnum).SetCharset(c[0]).SetCollate(c[1]).SetElems([]string{"a", "b"}).BuildP()
		return ft
	case 5:
		return types.NewFieldTypeBuilder().SetType(mysql.TypeJSON).SetFlag(mysql.BinaryFlag).SetCharset("binary").SetCollate("binary").BuildP()
	case 6:
		return types.NewFieldTypeBuilder().SetType(mysql.TypeBit).SetFlag(mysql.UnsignedFlag).SetFlen(8).SetCharset("binary").SetCollate("binary").BuildP()
	case 7:
		return types.NewFieldTypeBuilder().SetType(mysql.TypeNull).BuildP()
	case 8:
		return types.NewFieldTypeBuilder().SetType(mysql.TypeDatetime).SetFlag(mysql.BinaryFlag).SetCharset("binary").SetCollate("binary").BuildP()
	case 9:
		return types.NewFieldTypeBuilder().SetType(mysql.TypeDouble).SetFlag(mysql.BinaryFlag).SetCharset("binary").SetCollate("binary").BuildP()
	default:
		ft := types.NewFieldTypeBuilder().SetType(mysql.TypeLonglong).SetFlag(mysql.BinaryFlag).SetFlen(20).SetCharset("binary").SetCollate("binary").BuildP()
		if g.rng.IntN(2) == 0 {
			ft.AddFlag(mysql.NotNullFlag)
		}
		return ft
	}
}

// leaf: a column or a constant recipe.
func (g *fnGen) leaf() any {
	if g.rng.IntN(5) < 3 {
		g.uid++
		return map[string]any{"col": []any{g.uid, g.rng.IntN(4), sfFtIn(g.leafFt()), "c"}}
	}
	switch g.rng.IntN(5) {
	case 0:
		return map[string]any{"const": []any{[]any{types.KindNull}, sfFtIn(types.NewFieldType(mysql.TypeNull))}}
	case 1:
		ft := types.NewFieldTypeBuilder().SetType(mysql.TypeLonglong).SetFlag(mysql.BinaryFlag).SetFlen(20).SetCharset("binary").SetCollate("binary").BuildP()
		return map[string]any{"const": []any{[]any{types.KindInt64, g.rng.Int64N(100)}, sfFtIn(ft)}}
	default:
		c := g.cs()
		s := g.strVal()
		ft := types.NewFieldTypeBuilder().SetType(mysql.TypeVarString).SetFlen(len(s)).SetCharset(c[0]).SetCollate(c[1]).BuildP()
		if c[0] == charset.CharsetBin {
			ft.AddFlag(mysql.BinaryFlag)
			return map[string]any{"const": []any{[]any{types.KindBytes, hex.EncodeToString([]byte(s))}, sfFtIn(ft)}}
		}
		return map[string]any{"const": []any{[]any{types.KindString, vec.GoString(s), c[1]}, sfFtIn(ft)}}
	}
}

// arg: a leaf, or a small function over leaves.
func (g *fnGen) arg() any {
	if g.rng.IntN(6) != 0 {
		return g.leaf()
	}
	name := []string{ast.Concat, ast.Upper, ast.Lower, ast.Coalesce, ast.Plus, ast.Hex, ast.Ifnull}[g.rng.IntN(7)]
	n := 1
	switch name {
	case ast.Concat, ast.Coalesce:
		n = 1 + g.rng.IntN(3)
	case ast.Plus, ast.Ifnull:
		n = 2
	}
	args := []any{}
	for range n {
		args = append(args, g.leaf())
	}
	return map[string]any{"f": []any{name, nil, args}}
}

func (g *fnGen) overrides(n int) []any {
	out := []any{}
	for i := range n {
		if g.rng.IntN(4) == 0 {
			coer, repe := -1, -1
			if g.rng.IntN(3) != 0 {
				coer = g.rng.IntN(7)
			}
			if g.rng.IntN(3) == 0 {
				repe = []int{1, 3}[g.rng.IntN(2)]
			}
			out = append(out, []int{i, coer, repe})
		}
	}
	return out
}

// buildable reports whether all function recipes of args build (generation keeps only those).
func fnBuildable(c [2]string, args []any) bool {
	ctx := exprstatic.NewExprContext(exprstatic.WithCharset(c[0], c[1]))
	for _, a := range args {
		if _, err := sfBuild(ctx, vec.MustJSON(a)); err != nil {
			return false
		}
	}
	return true
}

func funcsGenerate(g *vec.Gen, rng *rand.Rand) {
	fg := &fnGen{rng: rng}
	ets := func(n int) []int {
		out := []int{}
		for range n {
			if rng.IntN(2) == 0 {
				out = append(out, int(types.ETString))
			} else {
				out = append(out, int(fnEvalTypes[rng.IntN(len(fnEvalTypes))]))
			}
		}
		return out
	}
	nargs := func() int { return []int{0, 1, 1, 2, 2, 2, 3, 3, 4, 5}[rng.IntN(10)] }

	for len(g.Calls) < 3600 {
		name := fnDeriveNames[rng.IntN(len(fnDeriveNames))]
		n := nargs()
		args := []any{}
		for range n {
			args = append(args, fg.arg())
		}
		c := fg.cs()
		if !fnBuildable(c, args) {
			continue
		}
		retType := int(fnEvalTypes[rng.IntN(len(fnEvalTypes))])
		if rng.IntN(3) == 0 {
			retType = int(types.ETString)
		}
		g.Add("rand/derive", "derive", rng.IntN(4) != 0, c[0], c[1], name, args, retType, ets(1+rng.IntN(3)), fg.overrides(n))
	}

	// fixStringTypeForMaxLength: a JSON argument with a string of explicit, non-binary collation.
	jsonCol := func() any {
		fg.uid++
		return map[string]any{"col": []any{fg.uid, 0, sfFtIn(types.NewFieldTypeBuilder().SetType(mysql.TypeJSON).SetFlag(mysql.BinaryFlag).SetCharset("binary").SetCollate("binary").BuildP()), "j"}}
	}
	for range 400 {
		name := []string{ast.Reverse, ast.Lower, ast.Upper, ast.SubstringIndex, ast.Trim, ast.Quote, ast.InsertFunc,
			ast.Substr, ast.Repeat, ast.Replace, ast.Concat, ast.ConcatWS, ast.Elt, ast.MakeSet, ast.ExportSet, ast.Left}[rng.IntN(16)]
		n := 2 + rng.IntN(3)
		args := []any{}
		ov := []any{}
		for i := range n {
			if rng.IntN(2) == 0 {
				args = append(args, jsonCol())
			} else {
				args = append(args, fg.leaf())
				if rng.IntN(2) == 0 {
					ov = append(ov, []int{i, 0, -1})
				}
			}
		}
		c := fg.cs()
		g.Add("rand/derive-json", "derive", rng.IntN(4) != 0, c[0], c[1], name, args, int(types.ETString), []int{int(types.ETString)}, ov)
	}

	fts := []*types.FieldType{
		types.NewFieldType(mysql.TypeLonglong), types.NewFieldType(mysql.TypeDouble), types.NewFieldType(mysql.TypeNewDecimal),
		types.NewFieldType(mysql.TypeVarString), types.NewFieldType(mysql.TypeDatetime), types.NewFieldType(mysql.TypeDuration),
		types.NewFieldType(mysql.TypeJSON), types.NewFieldType(mysql.TypeLonglong),
	}
	fts[7].AddFlag(mysql.NotNullFlag)
	for i := 0; i < 1300; {
		name := fnDeriveNames[rng.IntN(len(fnDeriveNames))]
		n := nargs()
		args := []any{}
		for range n {
			args = append(args, fg.arg())
		}
		c := fg.cs()
		if !fnBuildable(c, args) {
			continue
		}
		i++
		ft := fts[rng.IntN(len(fts))].Clone()
		if ft.GetType() == mysql.TypeVarString && rng.IntN(2) == 0 {
			cc := fg.cs()
			ft.SetCharset(cc[0])
			ft.SetCollate(cc[1])
		}
		g.Add("rand/newbase", "newbase", rng.IntN(4) != 0, c[0], c[1], name, args, sfFtIn(ft), fg.overrides(n), rng.IntN(5) == 0)
	}

	for _, name := range append(append([]string{}, fnDeriveNames...), ast.IsTruthWithoutNull, ast.IsFalsity, ast.JSONValid, ast.RegexpLike) {
		for _, et := range fnEvalTypes {
			c := fg.cs()
			g.Add("sys/retft", "retft", name, int(et), []any{rng.IntN(7), []int{1, 3}[rng.IntN(2)], c[0], c[1]})
		}
	}

	names := expression.RegisteredBuiltinFunctionNames()
	for range 400 {
		name := names[rng.IntN(len(names))]
		if rng.IntN(10) == 0 {
			name = "no_such_function"
		}
		mn := rng.IntN(4)
		mx := []int{-1, mn, mn + 1, mn + 2}[rng.IntN(4)]
		g.Add("rand/verify", "verify", name, mn, mx, rng.IntN(6))
	}

	for range 300 {
		ft := []*types.FieldType{types.NewFieldType(mysql.TypeDatetime), types.NewFieldType(mysql.TypeDuration),
			types.NewFieldType(mysql.TypeVarString), types.NewFieldType(mysql.TypeLonglong)}[rng.IntN(4)]
		ft.SetFlen([]int{-1, 0, 10, 19, 26, 40}[rng.IntN(6)])
		ft.SetDecimal([]int{-1, 0, 3, 6, 31}[rng.IntN(5)])
		g.Add("rand/setflen", "setflen", sfFtIn(ft), rng.IntN(3), []int{-1, 0, 1, 3, 6, 7, 31}[rng.IntN(7)])
	}

	g.Add("sys/registry", "registry")
	g.Add("sys/baseerr", "baseerr")

	for _, name := range append(names, "no_such_function") {
		for _, now := range []bool{false, true} {
			g.Add("sys/deferred", "deferred", name, now)
		}
	}
}

// ---------------------------------------------------------------- exec

func fnBuildArgs(ctx expression.BuildContext, recipes json.RawMessage, ov json.RawMessage) []expression.Expression {
	args := []expression.Expression{}
	for _, r := range exArr(recipes) {
		e, err := sfBuild(ctx, r)
		must(err)
		args = append(args, e)
	}
	for _, o := range arg[[][]int]([]json.RawMessage{ov}, 0) {
		if o[1] >= 0 {
			args[o[0]].SetCoercibility(expression.Coercibility(o[1]))
		}
		if o[2] >= 0 {
			args[o[0]].SetRepertoire(expression.Repertoire(o[2]))
		}
	}
	return args
}

func fnDumps(args []expression.Expression) []any {
	out := []any{}
	for _, a := range args {
		out = append(out, sfDump(a))
	}
	return out
}

func fnEc(ec *expression.ExprCollation) any {
	return []any{int(ec.Coer), int(ec.Repe), ec.Charset, ec.Collation}
}

func fnBaseOut(bf fnBaseMirror) any {
	var key any
	if bf.ctor != nil {
		key = hex.EncodeToString(bf.ctor.Key("aB é ß"))
	}
	return map[string]any{"tp": sfFtIn(bf.tp), "cs": bf.charset, "coll": bf.collation, "coerInit": bf.coerInit.Load(),
		"coer": int(bf.coer), "repe": int(bf.repertoire), "explicit": bf.isExplicitCharset, "key": key}
}

func funcsExec(op string, p []json.RawMessage) (out any, err error) {
	switch op {
	case "derive", "newbase":
		prev := collate.NewCollationEnabled()
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		defer collate.SetNewCollationEnabledForTest(prev)
		ctx := exprstatic.NewExprContext(exprstatic.WithCharset(arg[string](p, 1), arg[string](p, 2)))
		name := arg[string](p, 3)
		if op == "derive" {
			args := fnBuildArgs(ctx, p[4], p[7])
			ets := []types.EvalType{}
			for _, e := range arg[[]int](p, 6) {
				ets = append(ets, types.EvalType(e))
			}
			res := exTry(func() any {
				ec, err := fnDeriveCollation(ctx, name, args, types.EvalType(arg[int](p, 5)), ets...)
				if err != nil {
					return exErr(err)
				}
				return fnEc(ec)
			})
			return map[string]any{"args": fnDumps(args), "ec": res}, nil
		}
		args := fnBuildArgs(ctx, p[4], p[6])
		dumps := fnDumps(args)
		ft := d2FtIn(exArr(p[5]))
		res := exTry(func() any {
			var bf fnBaseMirror
			var err error
			if arg[bool](p, 7) {
				bf, err = fnNewBaseWithFt(ft, args)
			} else {
				bf, err = fnNewBase(ctx, name, args, ft)
			}
			if err != nil {
				return exErr(err)
			}
			return fnBaseOut(bf)
		})
		return map[string]any{"args": dumps, "bf": res}, nil
	case "retft":
		e := exArr(p[2])
		ec := &expression.ExprCollation{Coer: expression.Coercibility(arg[int](e, 0)), Repe: expression.Repertoire(arg[int](e, 1)),
			Charset: arg[string](e, 2), Collation: arg[string](e, 3)}
		return exTry(func() any { return sfFtIn(fnRetFt(arg[string](p, 0), types.EvalType(arg[int](p, 1)), ec)) }), nil
	case "verify":
		errOut := func(err error) any {
			if err != nil {
				return exErr(err)
			}
			return nil
		}
		cls := &fnClassMirror{funcName: arg[string](p, 0), minArgs: arg[int](p, 1), maxArgs: arg[int](p, 2)}
		n := arg[int](p, 3)
		return map[string]any{"wrapper": errOut(expression.VerifyArgsWrapper(cls.funcName, n)),
			"args": errOut(fnVerifyArgs(cls, make([]expression.Expression, n)))}, nil
	case "setflen":
		bf := &fnBaseMirror{tp: d2FtIn(exArr(p[0]))}
		fsp := arg[int](p, 2)
		switch arg[int](p, 1) {
		case 0:
			fnSetDatetime(bf, fsp)
		case 1:
			fnSetDate(bf)
		default:
			fnSetTime(bf, fsp)
		}
		return sfFtIn(bf.tp), nil
	case "registry":
		names := expression.RegisteredBuiltinFunctionNames()
		probes := append(append([]string{}, names...), "no_such_function", "GE", "", "'tidb`.(dateliteral")
		sup := []bool{}
		disp := []string{}
		for _, n := range probes {
			sup = append(sup, expression.IsFunctionSupported(n))
			disp = append(disp, expression.GetDisplayName(n))
		}
		return map[string]any{"names": names, "list": expression.GetBuiltinList(), "probes": probes,
			"supported": sup, "display": disp}, nil
	case "baseerr":
		ctx := exprstatic.NewEvalContext()
		bf := &fnBaseMirror{}
		e := func(err error) any { return vec.GoString(err.Error()) }
		_, _, e1 := fnEvalInt(bf, ctx, chunk.Row{})
		_, _, e2 := fnEvalReal(bf, ctx, chunk.Row{})
		_, _, e3 := fnEvalString(bf, ctx, chunk.Row{})
		_, _, e4 := fnEvalDecimal(bf, ctx, chunk.Row{})
		_, _, e5 := fnEvalTime(bf, ctx, chunk.Row{})
		_, _, e6 := fnEvalDuration(bf, ctx, chunk.Row{})
		_, _, e7 := fnEvalJSON(bf, ctx, chunk.Row{})
		_, _, e8 := fnEvalVec(bf, ctx, chunk.Row{})
		vecs := []any{}
		for _, f := range []fnVecEval{fnVecEvalInt, fnVecEvalReal, fnVecEvalString, fnVecEvalDecimal, fnVecEvalTime,
			fnVecEvalDuration, fnVecEvalJSON, fnVecEvalVec} {
			vecs = append(vecs, e(f(bf, ctx, nil, nil)))
		}
		fnSetPbCode(bf, tipb.ScalarFuncSig_PlusInt)
		return map[string]any{"eval": []any{e(e1), e(e2), e(e3), e(e4), e(e5), e(e6), e(e7), e(e8)}, "vec": vecs,
			"metadata": fnMetadata(bf) == nil, "props": fnReqProps(bf), "pb": int(fnPbCode(bf))}, nil
	case "deferred":
		ctx := exprstatic.NewExprContext(exprstatic.WithSysDateIsNow(arg[bool](p, 1)))
		return expression.IsDeferredFunctions(ctx, arg[string](p, 0)), nil
	}
	return nil, fmt.Errorf("unknown op %s", op)
}
