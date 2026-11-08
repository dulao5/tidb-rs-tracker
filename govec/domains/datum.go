package domains

// Domain "datum": block D01.1 (pkg/types: binary_literal, enum, set, compare,
// helper, etc; datum.go and field_type*.go follow in part 2) with block D01.4
// (convert.go, overflow.go, truncate.go) ported together, and the charset
// Encoding framework (pkg/parser/charset/encoding*.go, D02.2) that Datum's string
// conversions use.
//
// Values:
//   - float64 arguments and results are their IEEE bits (uint64 JSON numbers);
//   - strings are vec.GoString (hex for invalid UTF-8); a BinaryLiteral is hex;
//   - a context is [flags]: types.NewContext(types.Flags(flags), UTC, warnings);
//     warnings appended to it are recorded;
//   - a BinaryJSON is [type code, hex of Value];
//   - every op runs with new collations enabled (collate.SetNewCollationEnabledForTest(true));
//   - a decimal output is decOut (see decimal.go); a Time / Duration output is
//     timeOut / durOut (see time.go).
//
// errors.Wrap(ErrTruncated, msg) of the Enum parsers is recorded as its cause
// (mapping rule M-ERR: Go callers only look at the cause; tidb-rs does not port
// the wrapper).

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	gotime "time"
	_ "unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
)

const datumSeed = 20261108

//go:embed testdata/datum_literals.tsv
var datumLiteralsTSV string

func init() {
	vec.Register(&vec.Domain{
		Name:     "datum",
		Seed:     datumSeed,
		Errors:   datumErrors,
		Coverage: datumCoverage,
		Blocks:   []string{"D01.4"},
		Files:    datumFiles,
		Exempt:   datumExempt,
		Gzip:     true,
		Generate: datumGenerate,
		Exec:     datumExec,
	})
}

var datumErrors = []vec.NamedErr{
	{Name: "ErrTruncated", Err: types.ErrTruncated},
	{Name: "ErrOverflow", Err: types.ErrOverflow},
	{Name: "ErrTruncatedWrongVal", Err: types.ErrTruncatedWrongVal},
	{Name: "ErrBadNumber", Err: types.ErrBadNumber},
	{Name: "ErrDataTooLong", Err: types.ErrDataTooLong},
	{Name: "ErrWarnDataOutOfRange", Err: types.ErrWarnDataOutOfRange},
	{Name: "ErrIncorrectDatetimeValue", Err: types.ErrIncorrectDatetimeValue},
	{Name: "ErrWrongValue", Err: types.ErrWrongValue},
	{Name: "ErrDatetimeFunctionOverflow", Err: types.ErrDatetimeFunctionOverflow},
	{Name: "ErrInvalidCharacterString", Err: charset.ErrInvalidCharacterString},
}

// The D01.1 files of part 1 (the block itself is listed in part 2, with datum.go).
var datumFiles = []string{
	"pkg/types/binary_literal.go", "pkg/types/enum.go", "pkg/types/set.go", "pkg/types/compare.go",
	"pkg/types/helper.go", "pkg/types/etc.go",
	"pkg/parser/charset/encoding.go", "pkg/parser/charset/encoding_base.go",
	"pkg/parser/charset/encoding_utf8.go", "pkg/parser/charset/encoding_latin1.go",
	"pkg/parser/charset/encoding_ascii.go", "pkg/parser/charset/encoding_bin.go",
	"pkg/parser/charset/encoding_gbk.go",
}

var datumExempt = map[string]string{
	"ToString":                                       "takes Go's dynamic types (any): ported with the Datum part of D01.1 (the GoValue enum)",
	"pkg/types/etc.go:EOFAsNil":                      "io.EOF has no tidb_base::Error counterpart (M-ERR); ported with the Datum part if a caller needs it",
	"pkg/types/etc.go:InvOp2":                        "formats Go's dynamic types (%v %T of any): ported with the Datum part (GoValue)",
	"pkg/types/etc.go:ColumnNeedRestoredData":        "takes model structs; ported in tidb-model and covered by the model domain (need_restored)",
	"pkg/types/etc.go:NeedRestoredData":              "covered by the model domain (need_restored); depends on the global new-collation switch",
	"pkg/parser/charset/encoding_utf8.go:init":       "registers the encoding in Go's encoding map; Rust dispatches with a match",
	"pkg/parser/charset/encoding_latin1.go:init":     "registers the encoding in Go's encoding map; Rust dispatches with a match",
	"pkg/parser/charset/encoding_ascii.go:init":      "registers the encoding in Go's encoding map; Rust dispatches with a match",
	"pkg/parser/charset/encoding_bin.go:init":        "registers the encoding in Go's encoding map; Rust dispatches with a match",
	"pkg/parser/charset/encoding_gbk.go:init":        "registers the encoding in Go's encoding map; Rust dispatches with a match",
	"pkg/parser/charset/encoding_base.go:HackSlice":  "zero-copy cast; the Rust function returns its argument",
	"pkg/parser/charset/encoding_base.go:HackString": "zero-copy cast; the Rust function returns its argument",
}

//go:linkname datumStrToInt github.com/pingcap/tidb/pkg/types.strToInt
func datumStrToInt(str string) (int64, error)

//go:linkname datumTruncateStr github.com/pingcap/tidb/pkg/types.truncateStr
func datumTruncateStr(str string, flen int) string

func bitsF(u uint64) float64 { return math.Float64frombits(u) }
func fBits(f float64) uint64 { return math.Float64bits(f) }

var datumCoverage = map[string]vec.Coverage{
	// overflow.go
	"overflow": {Min: 3000, GoFuncs: []string{"AddUint64", "AddInt64", "AddDuration", "SubDuration", "AddInteger",
		"SubUint64", "SubInt64", "SubUintWithInt", "SubIntWithUint", "MulUint64", "MulInt64", "MulInteger",
		"DivInt64", "DivUintWithInt", "DivIntWithUint"}},
	// helper.go
	"float_helper": {Min: 2000, GoFuncs: []string{"pkg/types/helper.go:RoundFloat", "pkg/types/helper.go:Round",
		"pkg/types/helper.go:Truncate", "pkg/types/helper.go:GetMaxFloat", "pkg/types/helper.go:TruncateFloat",
		"pkg/types/helper.go:TruncateFloatToString"}},
	"length_precision": {Min: 100, GoFuncs: []string{"pkg/types/helper.go:DecimalLength2Precision",
		"pkg/types/helper.go:Precision2LengthNoTruncation"}},
	"helper_str_to_int": {Min: 300, GoFuncs: []string{"pkg/types/helper.go:strToInt", "pkg/types/helper.go:isSpace",
		"pkg/types/helper.go:isDigit", "pkg/types/helper.go:isPunctuation"}},
	// convert.go
	"int_bounds": {Min: 9, GoFuncs: []string{"IntegerUnsignedUpperBound", "IntegerSignedUpperBound", "IntegerSignedLowerBound"}},
	"convert_num": {Min: 3000, GoFuncs: []string{"ConvertFloatToInt", "ConvertIntToInt", "ConvertUintToInt",
		"ConvertIntToUint", "ConvertUintToUint", "ConvertFloatToUint", "pkg/types/etc.go:overflow"}},
	"convert_decimal_to_uint": {Min: 500, GoFuncs: []string{"ConvertDecimalToUint", "convertDecimalStrToUint", "convertScientificNotation"}},
	"str_to_num": {Min: 5000, GoFuncs: []string{"StrToInt", "StrToUint", "StrToFloat", "getValidIntPrefix",
		"roundIntStr", "floatStrToIntStr", "getValidFloatPrefix", "Context.HandleTruncate"}},
	"str_to_time":        {Min: 1000, GoFuncs: []string{"StrToDateTime", "StrToDuration"}},
	"number_to_duration": {Min: 500, GoFuncs: []string{"NumberToDuration"}},
	"convert_json":       {Min: 1000, GoFuncs: []string{"ConvertJSONToInt64", "ConvertJSONToInt", "ConvertJSONToFloat", "ConvertJSONToDecimal"}},
	"truncate_str":       {Min: 30, GoFuncs: []string{"truncateStr"}},
	"handle_truncate":    {Min: 30, GoFuncs: []string{"Context.HandleTruncate"}},
	// binary_literal.go
	"binlit_from_uint": {Min: 140, GoFuncs: []string{"pkg/types/binary_literal.go:NewBinaryLiteralFromUint",
		"pkg/types/binary_literal.go:trimLeadingZeroBytes"}},
	"binlit": {Min: 500, GoFuncs: []string{"pkg/types/binary_literal.go:BinaryLiteral.String",
		"pkg/types/binary_literal.go:BinaryLiteral.ToString", "pkg/types/binary_literal.go:BinaryLiteral.ToBitLiteralString",
		"pkg/types/binary_literal.go:BinaryLiteral.ToInt", "pkg/types/binary_literal.go:BinaryLiteral.Compare"}},
	"parse_bit_hex": {Min: 1000, GoFuncs: []string{"pkg/types/binary_literal.go:ParseBitStr", "pkg/types/binary_literal.go:NewBitLiteral",
		"pkg/types/binary_literal.go:BitLiteral.ToString", "pkg/types/binary_literal.go:ParseHexStr",
		"pkg/types/binary_literal.go:NewHexLiteral", "pkg/types/binary_literal.go:HexLiteral.ToString"}},
	// enum.go / set.go
	"enum": {Min: 500, GoFuncs: []string{"pkg/types/enum.go:ParseEnum", "pkg/types/enum.go:ParseEnumName",
		"pkg/types/enum.go:ParseEnumValue", "pkg/types/enum.go:Enum.Copy", "pkg/types/enum.go:Enum.String",
		"pkg/types/enum.go:Enum.ToNumber"}},
	"set": {Min: 500, GoFuncs: []string{"pkg/types/set.go:ParseSet", "pkg/types/set.go:ParseSetName",
		"pkg/types/set.go:ParseSetValue", "pkg/types/set.go:Set.Copy", "pkg/types/set.go:Set.String",
		"pkg/types/set.go:Set.ToNumber", "pkg/types/set.go:init"}},
	// compare.go
	"vec_compare": {Min: 200, GoFuncs: []string{"pkg/types/compare.go:VecCompareUU", "pkg/types/compare.go:VecCompareII",
		"pkg/types/compare.go:VecCompareUI", "pkg/types/compare.go:VecCompareIU"}},
	"compare_int":    {Min: 1000, GoFuncs: []string{"pkg/types/compare.go:CompareInt"}},
	"compare_string": {Min: 100, GoFuncs: []string{"pkg/types/compare.go:CompareString"}},
	// etc.go
	"type_preds": {Min: 256, GoFuncs: []string{"pkg/types/etc.go:IsTypeVarchar", "pkg/types/etc.go:IsTypeUnspecified",
		"pkg/types/etc.go:IsTypePrefixable", "pkg/types/etc.go:IsTypeFractionable", "pkg/types/etc.go:IsTypeTime",
		"pkg/types/etc.go:IsTypeFloat", "pkg/types/etc.go:IsTypeInteger", "pkg/types/etc.go:IsTypeNumeric",
		"pkg/types/etc.go:IsTemporalWithDate", "pkg/types/etc.go:IsString", "pkg/types/etc.go:IsTypeTemporal"}},
	"ft_preds": {Min: 80, GoFuncs: []string{"pkg/types/etc.go:IsTypeBit", "pkg/types/etc.go:IsBinaryStr",
		"pkg/types/etc.go:IsNonBinaryStr"}},
	"kind": {Min: 30, GoFuncs: []string{"pkg/types/etc.go:IsStringKind", "pkg/types/etc.go:KindStr"}},
	// charset encodings
	"encoding": {Min: 3000, GoFuncs: encodingFuncs},
}

// encodingFuncs: what the "encoding" op calls (every function of the Encoding files).
var encodingFuncs = []string{
	"pkg/parser/charset/encoding_ascii.go:encodingASCII.Foreach",
	"pkg/parser/charset/encoding_ascii.go:encodingASCII.IsValid",
	"pkg/parser/charset/encoding_ascii.go:encodingASCII.Name",
	"pkg/parser/charset/encoding_ascii.go:encodingASCII.Peek",
	"pkg/parser/charset/encoding_ascii.go:encodingASCII.Tp",
	"pkg/parser/charset/encoding_ascii.go:encodingASCII.Transform",
	"pkg/parser/charset/encoding_base.go:beginWithReplacementChar",
	"pkg/parser/charset/encoding_base.go:encodingBase.Foreach",
	"pkg/parser/charset/encoding_base.go:encodingBase.IsValid",
	"pkg/parser/charset/encoding_base.go:encodingBase.MbLen",
	"pkg/parser/charset/encoding_base.go:encodingBase.ToLower",
	"pkg/parser/charset/encoding_base.go:encodingBase.ToUpper",
	"pkg/parser/charset/encoding_base.go:encodingBase.Transform",
	"pkg/parser/charset/encoding_base.go:generateEncodingErr",
	"pkg/parser/charset/encoding_bin.go:encodingBin.Foreach",
	"pkg/parser/charset/encoding_bin.go:encodingBin.IsValid",
	"pkg/parser/charset/encoding_bin.go:encodingBin.Name",
	"pkg/parser/charset/encoding_bin.go:encodingBin.Peek",
	"pkg/parser/charset/encoding_bin.go:encodingBin.Tp",
	"pkg/parser/charset/encoding_bin.go:encodingBin.Transform",
	"pkg/parser/charset/encoding_gbk.go:customGBK.NewDecoder",
	"pkg/parser/charset/encoding_gbk.go:customGBK.NewEncoder",
	"pkg/parser/charset/encoding_gbk.go:customGBKDecoder.Reset",
	"pkg/parser/charset/encoding_gbk.go:customGBKDecoder.Transform",
	"pkg/parser/charset/encoding_gbk.go:customGBKEncoder.Reset",
	"pkg/parser/charset/encoding_gbk.go:customGBKEncoder.Transform",
	"pkg/parser/charset/encoding_gbk.go:encodingGBK.MbLen",
	"pkg/parser/charset/encoding_gbk.go:encodingGBK.Name",
	"pkg/parser/charset/encoding_gbk.go:encodingGBK.Peek",
	"pkg/parser/charset/encoding_gbk.go:encodingGBK.ToLower",
	"pkg/parser/charset/encoding_gbk.go:encodingGBK.ToUpper",
	"pkg/parser/charset/encoding_gbk.go:encodingGBK.Tp",
	"pkg/parser/charset/encoding_gbk.go:NewCustomGBKEncoder",
	"pkg/parser/charset/encoding_latin1.go:encodingLatin1.IsValid",
	"pkg/parser/charset/encoding_latin1.go:encodingLatin1.Name",
	"pkg/parser/charset/encoding_latin1.go:encodingLatin1.Peek",
	"pkg/parser/charset/encoding_latin1.go:encodingLatin1.Tp",
	"pkg/parser/charset/encoding_latin1.go:encodingLatin1.Transform",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.Foreach",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.IsValid",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.MbLen",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.Name",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.Peek",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.Tp",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8.Transform",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8MB3Strict.Foreach",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8MB3Strict.IsValid",
	"pkg/parser/charset/encoding_utf8.go:encodingUTF8MB3Strict.Transform",
	"pkg/parser/charset/encoding.go:CountValidBytes",
	"pkg/parser/charset/encoding.go:CountValidBytesDecode",
	"pkg/parser/charset/encoding.go:FindEncoding",
	"pkg/parser/charset/encoding.go:FindEncodingTakeUTF8AsNoop",
	"pkg/parser/charset/encoding.go:IsSupportedEncoding",
}

// ---------------------------------------------------------------- context

type datumWarns struct{ ws []error }

func (w *datumWarns) AppendWarning(err error) { w.ws = append(w.ws, err) }
func (w *datumWarns) AppendNote(err error)    { w.ws = append(w.ws, err) }

func datumCtx(args []json.RawMessage, i int) (types.Context, *datumWarns) {
	w := &datumWarns{}
	return types.NewContext(types.Flags(arg[uint16](args, i)), gotime.UTC, w), w
}

func dw(w *datumWarns, out any) any {
	if len(w.ws) == 0 {
		return out
	}
	return vec.WithWarnings{Out: out, Warnings: w.ws}
}

func bjOf(args []json.RawMessage, i int) types.BinaryJSON {
	p := arg[[]json.RawMessage](args, i)
	v, err := hex.DecodeString(arg[string](p, 1))
	if err != nil {
		panic(err)
	}
	return types.BinaryJSON{TypeCode: arg[byte](p, 0), Value: v}
}

func bjArg(j types.BinaryJSON) []any { return []any{j.TypeCode, hex.EncodeToString(j.Value)} }

func binLitOf(s string) types.BinaryLiteral {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func gsSlice(ss []vec.GoString) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}

// ---------------------------------------------------------------- exec

func datumExec(op string, args []json.RawMessage) (out any, err error) {
	// The collators depend on the global new-collation switch (other domains
	// toggle it): every record runs with new collations enabled, the default
	// of a new cluster.
	collate.SetNewCollationEnabledForTest(true)
	s := func(i int) string { return string(arg[vec.GoString](args, i)) }
	switch op {
	case "overflow":
		fn := arg[string](args, 0)
		i64 := func(i int) int64 { return arg[int64](args, i) }
		u64 := func(i int) uint64 { return arg[uint64](args, i) }
		switch fn {
		case "AddUint64":
			return types.AddUint64(u64(1), u64(2))
		case "AddInt64":
			return types.AddInt64(i64(1), i64(2))
		case "AddDuration":
			d, err := types.AddDuration(gotime.Duration(i64(1)), gotime.Duration(i64(2)))
			return int64(d), err
		case "SubDuration":
			d, err := types.SubDuration(gotime.Duration(i64(1)), gotime.Duration(i64(2)))
			return int64(d), err
		case "AddInteger":
			return types.AddInteger(u64(1), i64(2))
		case "SubUint64":
			return types.SubUint64(u64(1), u64(2))
		case "SubInt64":
			return types.SubInt64(i64(1), i64(2))
		case "SubUintWithInt":
			return types.SubUintWithInt(u64(1), i64(2))
		case "SubIntWithUint":
			return types.SubIntWithUint(i64(1), u64(2))
		case "MulUint64":
			return types.MulUint64(u64(1), u64(2))
		case "MulInt64":
			return types.MulInt64(i64(1), i64(2))
		case "MulInteger":
			return types.MulInteger(u64(1), i64(2))
		case "DivInt64":
			return types.DivInt64(i64(1), i64(2))
		case "DivUintWithInt":
			return types.DivUintWithInt(u64(1), i64(2))
		case "DivIntWithUint":
			return types.DivIntWithUint(i64(1), u64(2))
		}
		panic("datum: unknown overflow fn " + fn)
	case "float_helper":
		f := bitsF(arg[uint64](args, 0))
		a, b := arg[int](args, 1), arg[int](args, 2)
		tf, terr := types.TruncateFloat(f, a, b)
		var te *vec.Err
		if terr != nil {
			te = &vec.Err{Name: "ErrOverflow", Code: int(errors.Cause(terr).(*errors.Error).Code()), Msg: vec.GoString(terr.Error())}
		}
		return map[string]any{
			"round_float": fBits(types.RoundFloat(f)),
			"round":       fBits(types.Round(f, a)),
			"truncate":    fBits(types.Truncate(f, a)),
			"max_float":   fBits(types.GetMaxFloat(a, b)),
			"tf":          fBits(tf), "tf_err": te,
			"tf_str": types.TruncateFloatToString(f, a),
		}, nil
	case "length_precision":
		l, sc, u := arg[int](args, 0), arg[int](args, 1), arg[bool](args, 2)
		return []int{types.DecimalLength2Precision(l, sc, u), types.Precision2LengthNoTruncation(l, sc, u)}, nil
	case "helper_str_to_int":
		return datumStrToInt(s(0))
	case "int_bounds":
		tp := arg[byte](args, 0)
		return map[string]any{"uu": types.IntegerUnsignedUpperBound(tp), "su": types.IntegerSignedUpperBound(tp),
			"sl": types.IntegerSignedLowerBound(tp)}, nil
	case "convert_num":
		fn := arg[string](args, 0)
		flags := types.Flags(arg[uint16](args, 1))
		tp := arg[byte](args, 5)
		switch fn {
		case "FloatToInt":
			return types.ConvertFloatToInt(bitsF(arg[uint64](args, 2)), arg[int64](args, 3), arg[int64](args, 4), tp)
		case "IntToInt":
			return types.ConvertIntToInt(arg[int64](args, 2), arg[int64](args, 3), arg[int64](args, 4), tp)
		case "UintToInt":
			return types.ConvertUintToInt(arg[uint64](args, 2), arg[int64](args, 4), tp)
		case "IntToUint":
			return types.ConvertIntToUint(flags, arg[int64](args, 2), arg[uint64](args, 4), tp)
		case "UintToUint":
			return types.ConvertUintToUint(arg[uint64](args, 2), arg[uint64](args, 4), tp)
		case "FloatToUint":
			return types.ConvertFloatToUint(flags, bitsF(arg[uint64](args, 2)), arg[uint64](args, 4), tp)
		}
		panic("datum: unknown convert fn " + fn)
	case "convert_decimal_to_uint":
		d := new(types.MyDecimal)
		_ = d.FromString([]byte(s(0)))
		return types.ConvertDecimalToUint(d, arg[uint64](args, 1), arg[byte](args, 2))
	case "str_to_num":
		ctx, w := datumCtx(args, 0)
		str, fc := s(2), arg[bool](args, 3)
		switch arg[string](args, 1) {
		case "int":
			v, err := types.StrToInt(ctx, str, fc)
			return dw(w, v), err
		case "uint":
			v, err := types.StrToUint(ctx, str, fc)
			return dw(w, v), err
		case "float":
			v, err := types.StrToFloat(ctx, str, fc)
			return dw(w, fBits(v)), err
		}
		panic("datum: unknown str_to_num")
	case "str_to_time":
		ctx, w := datumCtx(args, 0)
		str, fsp := s(2), arg[int](args, 3)
		if arg[string](args, 1) == "datetime" {
			t, err := types.StrToDateTime(ctx, str, fsp)
			return dw(w, timeOut(t)), err
		}
		d, t, isDur, err := types.StrToDuration(ctx, str, fsp)
		return dw(w, map[string]any{"d": durOut(d), "t": timeOut(t), "is_dur": isDur}), err
	case "number_to_duration":
		d, err := types.NumberToDuration(arg[int64](args, 0), arg[int](args, 1))
		return durOut(d), err
	case "convert_json":
		ctx, w := datumCtx(args, 0)
		j := bjOf(args, 2)
		switch arg[string](args, 1) {
		case "int64":
			v, err := types.ConvertJSONToInt64(ctx, j, arg[bool](args, 3))
			return dw(w, v), err
		case "int":
			v, err := types.ConvertJSONToInt(ctx, j, arg[bool](args, 3), arg[byte](args, 4))
			return dw(w, v), err
		case "float":
			v, err := types.ConvertJSONToFloat(ctx, j)
			return dw(w, fBits(v)), err
		case "decimal":
			v, err := types.ConvertJSONToDecimal(ctx, j)
			return dw(w, decOut(v)), err
		}
		panic("datum: unknown convert_json")
	case "truncate_str":
		return vec.GoString(datumTruncateStr(s(0), arg[int](args, 1))), nil
	case "handle_truncate":
		ctx, w := datumCtx(args, 0)
		var e error
		switch arg[string](args, 1) {
		case "nil":
		case "plain":
			e = errors.New("plain error")
		case "ErrTruncatedWrongVal":
			e = types.ErrTruncatedWrongVal.GenWithStackByArgs("DOUBLE", "x")
		case "ErrTruncated":
			e = types.ErrTruncated
		case "ErrOverflow":
			e = types.ErrOverflow.GenWithStackByArgs("BIGINT", "1")
		case "ErrBadNumber":
			e = types.ErrBadNumber
		case "ErrDataTooLong":
			e = types.ErrDataTooLong.GenWithStackByArgs("c")
		case "ErrWarnDataOutOfRange":
			e = types.ErrWarnDataOutOfRange.GenWithStackByArgs("c", 1)
		case "ErrIncorrectDatetimeValue":
			e = types.ErrIncorrectDatetimeValue.GenWithStackByArgs("x")
		case "ErrDatetimeFunctionOverflow":
			e = types.ErrDatetimeFunctionOverflow.GenWithStackByArgs("datetime")
		case "ErrWrongValue":
			e = types.ErrWrongValue.GenWithStackByArgs("x", "y")
		case "ErrInvalidCharacterString":
			e = charset.ErrInvalidCharacterString.GenWithStackByArgs("gbk", "x")
		}
		err := ctx.HandleTruncate(e) // before dw: dw reads the warnings
		return dw(w, nil), err
	case "binlit_from_uint":
		return hex.EncodeToString(types.NewBinaryLiteralFromUint(arg[uint64](args, 0), arg[int](args, 1))), nil
	case "binlit":
		ctx, w := datumCtx(args, 0)
		b, b2 := binLitOf(arg[string](args, 1)), binLitOf(arg[string](args, 2))
		v, ierr := b.ToInt(ctx)
		var ie *vec.Err
		if ierr != nil {
			ie = &vec.Err{Name: "ErrTruncatedWrongVal", Code: int(errors.Cause(ierr).(*errors.Error).Code()), Msg: vec.GoString(ierr.Error())}
		}
		return dw(w, map[string]any{
			"s": b.String(), "ts": vec.GoString(b.ToString()),
			"bit": b.ToBitLiteralString(false), "bit_trim": b.ToBitLiteralString(true),
			"int": v, "int_err": ie, "cmp": b.Compare(b2),
		}), nil
	case "parse_bit_hex":
		str := s(1)
		if arg[string](args, 0) == "bit" {
			b, err := types.ParseBitStr(str)
			nb, nerr := types.NewBitLiteral(str)
			if (err == nil) != (nerr == nil) {
				panic("ParseBitStr and NewBitLiteral disagree")
			}
			return map[string]any{"b": hex.EncodeToString(b), "nil": b == nil, "ts": vec.GoString(nb.ToString())}, err
		}
		b, err := types.ParseHexStr(str)
		nh, nerr := types.NewHexLiteral(str)
		if (err == nil) != (nerr == nil) {
			panic("ParseHexStr and NewHexLiteral disagree")
		}
		return map[string]any{"b": hex.EncodeToString(b), "nil": b == nil, "ts": vec.GoString(nh.ToString())}, err
	case "enum":
		elems := gsSlice(arg[[]vec.GoString](args, 1))
		var e types.Enum
		switch arg[string](args, 0) {
		case "parse":
			e, err = types.ParseEnum(elems, s(2), arg[string](args, 3))
		case "name":
			e, err = types.ParseEnumName(elems, s(2), arg[string](args, 3))
		case "value":
			e, err = types.ParseEnumValue(elems, arg[uint64](args, 2))
		}
		if err != nil {
			err = errors.Cause(err)
		}
		c := e.Copy()
		return map[string]any{"name": vec.GoString(e.Name), "value": e.Value, "s": vec.GoString(c.String()), "num": fBits(e.ToNumber())}, err
	case "set":
		elems := gsSlice(arg[[]vec.GoString](args, 1))
		var e types.Set
		switch arg[string](args, 0) {
		case "parse":
			e, err = types.ParseSet(elems, s(2), arg[string](args, 3))
		case "name":
			e, err = types.ParseSetName(elems, s(2), arg[string](args, 3))
		case "value":
			e, err = types.ParseSetValue(elems, arg[uint64](args, 2))
		}
		c := e.Copy()
		return map[string]any{"name": vec.GoString(e.Name), "value": e.Value, "s": vec.GoString(c.String()), "num": fBits(e.ToNumber())}, err
	case "vec_compare":
		n := 0
		res := func() []int64 { return make([]int64, n) }
		switch arg[string](args, 0) {
		case "UU":
			x, y := arg[[]uint64](args, 1), arg[[]uint64](args, 2)
			n = len(x)
			r := res()
			types.VecCompareUU(x, y, r)
			return r, nil
		case "II":
			x, y := arg[[]int64](args, 1), arg[[]int64](args, 2)
			n = len(x)
			r := res()
			types.VecCompareII(x, y, r)
			return r, nil
		case "UI":
			x, y := arg[[]uint64](args, 1), arg[[]int64](args, 2)
			n = len(x)
			r := res()
			types.VecCompareUI(x, y, r)
			return r, nil
		case "IU":
			x, y := arg[[]int64](args, 1), arg[[]uint64](args, 2)
			n = len(x)
			r := res()
			types.VecCompareIU(x, y, r)
			return r, nil
		}
		panic("datum: unknown vec_compare")
	case "compare_int":
		return types.CompareInt(arg[int64](args, 0), arg[bool](args, 1), arg[int64](args, 2), arg[bool](args, 3)), nil
	case "compare_string":
		return types.CompareString(s(0), s(1), arg[string](args, 2)), nil
	case "type_preds":
		tp := arg[byte](args, 0)
		return []bool{types.IsTypeVarchar(tp), types.IsTypeUnspecified(tp), types.IsTypePrefixable(tp),
			types.IsTypeFractionable(tp), types.IsTypeTime(tp), types.IsTypeFloat(tp), types.IsTypeInteger(tp),
			types.IsTypeNumeric(tp), types.IsTemporalWithDate(tp), types.IsString(tp), types.IsTypeTemporal(tp)}, nil
	case "ft_preds":
		ft := types.NewFieldType(arg[byte](args, 0))
		ft.SetCollate(arg[string](args, 1))
		return []bool{types.IsTypeBit(ft), types.IsBinaryStr(ft), types.IsNonBinaryStr(ft)}, nil
	case "kind":
		k := arg[byte](args, 0)
		return map[string]any{"is_string": types.IsStringKind(k), "str": types.KindStr(k)}, nil
	case "encoding":
		return encodingExec(args)
	}
	panic("datum: unknown op " + op)
}

func encodingExec(args []json.RawMessage) (any, error) {
	cs := arg[string](args, 0)
	src, err := hex.DecodeString(arg[string](args, 1))
	if err != nil {
		panic(err)
	}
	e := charset.FindEncoding(cs)
	out := map[string]any{
		"supported": charset.IsSupportedEncoding(cs),
		"noop_name": charset.FindEncodingTakeUTF8AsNoop(cs).Name(),
		"name":      e.Name(), "tp": int(e.Tp()),
		"peek":   hex.EncodeToString(e.Peek(src)),
		"mb_len": e.MbLen(string(src)),
		"valid":  e.IsValid(src),
		"cvb":    charset.CountValidBytes(e, src),
		"cvbd":   charset.CountValidBytesDecode(e, src),
		"upper":  hex.EncodeToString([]byte(e.ToUpper(string(src)))),
		"lower":  hex.EncodeToString([]byte(e.ToLower(string(src)))),
	}
	ops := map[string]charset.Op{"replace_noerr": charset.OpReplaceNoErr, "replace": charset.OpReplace,
		"encode": charset.OpEncode, "encode_noerr": charset.OpEncodeNoErr, "encode_replace": charset.OpEncodeReplace,
		"decode": charset.OpDecode, "decode_noerr": charset.OpDecodeNoErr, "decode_replace": charset.OpDecodeReplace}
	tr := map[string]any{}
	fe := map[string]any{}
	for name, op := range ops {
		b, err := e.Transform(nil, src, op)
		var em any
		if err != nil {
			em = vec.GoString(err.Error())
		}
		tr[name] = map[string]any{"b": hex.EncodeToString(b), "err": em}
		var steps []string
		e.Foreach(src, op, func(from, to []byte, ok bool) bool {
			steps = append(steps, fmt.Sprintf("%x/%x/%v", from, to, ok))
			return len(steps) < 64
		})
		fe[name] = steps
	}
	out["transform"] = tr
	out["foreach"] = fe
	return out, nil
}

// ---------------------------------------------------------------- literals

type datumLit struct {
	fn  string
	str *string
	num *int64
}

func datumLiterals() []datumLit {
	var lits []datumLit
	sc := bufio.NewScanner(bytes.NewReader([]byte(datumLiteralsTSV)))
	for sc.Scan() {
		c := strings.SplitN(sc.Text(), "\t", 3)
		if len(c) != 3 {
			continue
		}
		switch c[1] {
		case "s":
			v, err := strconv.Unquote(c[2])
			if err != nil {
				panic(err)
			}
			lits = append(lits, datumLit{fn: c[0], str: &v})
		case "i":
			v, err := strconv.ParseInt(c[2], 10, 64)
			if err != nil {
				panic(err)
			}
			lits = append(lits, datumLit{fn: c[0], num: &v})
		}
	}
	return lits
}

// DatumMinedTests: the Go test functions whose literals are in testdata/datum_literals.tsv.
func DatumMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, l := range datumLiterals() {
		if !seen[l.fn] {
			seen[l.fn] = true
			fns = append(fns, l.fn)
		}
	}
	return fns
}

// ---------------------------------------------------------------- generator

var datumFlags = []uint16{0,
	uint16(types.StrictFlags.WithTruncateAsWarning(true)),
	uint16(types.StrictFlags.WithIgnoreTruncateErr(true)),
	uint16(types.StrictFlags.WithAllowNegativeToUnsigned(true)),
	uint16(types.StrictFlags.WithTruncateAsWarning(true).WithAllowNegativeToUnsigned(true)),
}

var datumIntTypes = []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong,
	mysql.TypeBit, mysql.TypeEnum, mysql.TypeSet, mysql.TypeYear, mysql.TypeDouble}

var datumCollations = []string{"binary", "utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci",
	"utf8mb4_0900_ai_ci", "gbk_chinese_ci", "latin1_bin", "utf8_general_ci"}

var datumCharsets = []string{"utf8mb4", "utf8", "UTF8", "ascii", "latin1", "binary", "gbk", "GBK", "utf16", "", "gb18030"}

var datumEdgeInts = []int64{0, 1, -1, 2, -2, 9, 10, 59, 60, 99, 100, 127, -128, 255, 256, 32767, -32768, 65535,
	8388607, -8388608, 16777215, 2147483647, -2147483648, 4294967295, 8385959, 8385960, -8385959, -8385960,
	10000000000, 20010101101010, 99991231235959, 1 << 53, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1,
	math.MinInt64 + 1, 1 << 32, -(1 << 32), 3037000499, 3037000500, -3037000500}

var datumEdgeUints = []uint64{0, 1, 2, 255, 65535, 1<<63 - 1, 1 << 63, 1<<63 + 1, math.MaxUint64, math.MaxUint64 - 1,
	1 << 32, 4294967296, 18446744073709551614}

var datumEdgeFloats = []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1.5, -1.5, 2.5, 0.49999999999999994, 1e15, 1e16,
	1e300, -1e300, 9.223372036854775807e18, 9223372036854775808, -9223372036854775808, -9223372036854777856,
	1.8446744073709551615e19, 18446744073709551616, 1.7976931348623157e308, -1.7976931348623157e308, 5e-324,
	math.Inf(1), math.Inf(-1), math.NaN(), 127.5, 128.5, -128.5, 255.5, 65535.5, 2147483647.5, 4294967295.5,
	123.456, -123.456, 0.1, 1.23456789e-5}

var datumEdgeStrings = []string{"", " ", "0", "-0", "+0", "1", "-1", "+1", "1.5", "-1.5", "2.5", "1e3", "1E3", "1e-3",
	"1e+3", ".5", "5.", "-.5", "+.5", "e5", "1e", "1e+", "1.1.1", "1e1.1", "1e5e5", "abc", "12abc", " 12 ", "\t12",
	"12\x00ab", "\x0012", "9223372036854775807", "9223372036854775808", "-9223372036854775808",
	"-9223372036854775809", "18446744073709551615", "18446744073709551616", "1e19", "1e20", "1e21", "1e22",
	"-1e19", "1.8446744073709551615e19", "99999999999999999999999", "1e309", "-1e309", "1e-400", "0x1p3", "inf",
	"nan", "1_000", "123456789012345678901234567890", "-000", "-0001", "+000", "0.4", "0.5", "0.9999", "9.5",
	"99.5", "-9.5", "-99.5", "1e9223372036854775807", "1e-9223372036854775808", "1e99999999999999999999",
	"12:30:45", "-12:30:45", "838:59:59", "838:59:60", "839:00:00", "2001-01-01", "2001-01-01 10:20:30",
	"20010101102030", "010101102030", "20010101102030.5", "11:22:33.4567", "1 2:3:4", "100", "123456",
	"b'0101'", "B'1'", "0b1010", "0b", "b''", "b'2'", "0b12", "x'0A'", "X'abc'", "0x123", "0xg", "x''", "0x",
	"x'GG'", "\xff\xfe", "中文", "a,b", "a,,b", ",", "A,B,C", "1,2", "0x3", "0o7", "0b11", "07", "1_0", "0x_1",
	"0x1_", "0__1"}

func datumGenerate(g *vec.Gen, rng *rand.Rand) {
	lits := datumLiterals()
	var litStrs []string
	var litInts []int64
	for _, l := range lits {
		if l.str != nil {
			litStrs = append(litStrs, *l.str)
		} else {
			litInts = append(litInts, *l.num)
		}
	}
	randInt := func() int64 {
		switch rng.IntN(4) {
		case 0:
			return datumEdgeInts[rng.IntN(len(datumEdgeInts))]
		case 1:
			return int64(rng.Uint64()) >> rng.IntN(64)
		case 2:
			return rng.Int64N(2000) - 1000
		}
		return int64(rng.Uint64())
	}
	randUint := func() uint64 {
		switch rng.IntN(3) {
		case 0:
			return datumEdgeUints[rng.IntN(len(datumEdgeUints))]
		case 1:
			return rng.Uint64() >> rng.IntN(64)
		}
		return uint64(rng.IntN(1000))
	}
	randFloat := func() float64 {
		switch rng.IntN(4) {
		case 0:
			return datumEdgeFloats[rng.IntN(len(datumEdgeFloats))]
		case 1:
			return math.Float64frombits(rng.Uint64())
		case 2:
			return (rng.Float64() - 0.5) * math.Pow(10, float64(rng.IntN(40)-10))
		}
		return float64(randInt()) + float64(rng.IntN(4))/4
	}
	randNumStr := func() string {
		var b strings.Builder
		if rng.IntN(4) == 0 {
			b.WriteByte(" \t+-"[rng.IntN(4)])
		}
		alpha := "0123456789.eE+-  a\x00"
		n := 1 + rng.IntN(25)
		for i := 0; i < n; i++ {
			if rng.IntN(3) == 0 {
				b.WriteByte(alpha[rng.IntN(len(alpha))])
			} else {
				b.WriteByte('0' + byte(rng.IntN(10)))
			}
		}
		return b.String()
	}

	// ---- overflow
	ovFns := []string{"AddUint64", "AddInt64", "AddDuration", "SubDuration", "AddInteger", "SubUint64", "SubInt64",
		"SubUintWithInt", "SubIntWithUint", "MulUint64", "MulInt64", "MulInteger", "DivInt64", "DivUintWithInt", "DivIntWithUint"}
	sig := map[string]string{"AddUint64": "uu", "AddInt64": "ii", "AddDuration": "ii", "SubDuration": "ii",
		"AddInteger": "ui", "SubUint64": "uu", "SubInt64": "ii", "SubUintWithInt": "ui", "SubIntWithUint": "iu",
		"MulUint64": "uu", "MulInt64": "ii", "MulInteger": "ui", "DivInt64": "ii", "DivUintWithInt": "ui", "DivIntWithUint": "iu"}
	ovArg := func(c byte) any {
		if c == 'u' {
			return randUint()
		}
		return randInt()
	}
	for _, fn := range ovFns {
		sg := sig[fn]
		for _, a := range datumEdgeInts {
			for _, b := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 2, -2} {
				var x, y any = a, b
				if sg[0] == 'u' {
					x = uint64(a)
				}
				if sg[1] == 'u' {
					y = uint64(b)
				}
				g.Add("sys/edge", "overflow", fn, x, y)
			}
		}
		for i := 0; i < 250; i++ {
			g.Add("rand", "overflow", fn, ovArg(sg[0]), ovArg(sg[1]))
		}
	}
	for _, fn := range []string{"TestAdd", "TestSub", "TestMul", "TestDiv"} {
		var ints []int64
		for _, l := range lits {
			if l.fn == fn && l.num != nil {
				ints = append(ints, *l.num)
			}
		}
		for i := 0; i < len(ints); i++ {
			for j := 0; j < len(ints); j++ {
				for _, f := range ovFns {
					sg := sig[f]
					var x, y any = ints[i], ints[j]
					if sg[0] == 'u' {
						x = uint64(ints[i])
					}
					if sg[1] == 'u' {
						y = uint64(ints[j])
					}
					g.Add(fn, "overflow", f, x, y)
				}
			}
		}
	}

	// ---- helper
	decs := []int{-400, -330, -310, -309, -308, -20, -5, -1, 0, 1, 2, 5, 15, 16, 17, 30, 308, 309, 323, 324, 400}
	for _, f := range datumEdgeFloats {
		for _, d := range decs {
			g.Add("sys/edge", "float_helper", fBits(f), d, rng.IntN(31))
		}
	}
	for i := 0; i < 1500; i++ {
		flen := rng.IntN(70)
		g.Add("rand", "float_helper", fBits(randFloat()), rng.IntN(60)-20, flen-rng.IntN(flen+1))
	}
	for _, fn := range []string{"TestRoundFloat", "TestRound", "TestTruncate", "TestMaxFloat", "TestTruncateFloat", "TestTruncateFloatToString"} {
		for _, l := range lits {
			if l.fn == fn && l.num != nil {
				g.Add(fn, "float_helper", fBits(float64(*l.num)), int(*l.num%40), 10)
			}
		}
	}
	for l := -2; l < 70; l += 3 {
		for _, sc := range []int{-1, 0, 1, 5, 30} {
			g.Add("sys", "length_precision", l, sc, false)
			g.Add("sys", "length_precision", l, sc, true)
		}
	}
	for _, s := range append(append([]string{}, datumEdgeStrings...), litStrs...) {
		g.Add("sys/str", "helper_str_to_int", vec.GoString(s))
	}

	// ---- convert
	for _, tp := range []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeEnum,
		mysql.TypeBit, mysql.TypeSet, mysql.TypeDouble} {
		g.Add("sys", "int_bounds", tp)
	}
	signedTps := []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeEnum}
	bounds := func(tp byte) (int64, int64, uint64) {
		return types.IntegerSignedLowerBound(tp), types.IntegerSignedUpperBound(tp), types.IntegerUnsignedUpperBound(tp)
	}
	convFns := []string{"FloatToInt", "IntToInt", "UintToInt", "IntToUint", "UintToUint", "FloatToUint"}
	for i := 0; i < 4000; i++ {
		tp := signedTps[rng.IntN(len(signedTps))]
		lo, hi, uhi := bounds(tp)
		fn := convFns[rng.IntN(len(convFns))]
		fl := datumFlags[rng.IntN(len(datumFlags))]
		var v any
		switch fn {
		case "FloatToInt", "FloatToUint":
			v = fBits(randFloat())
		case "IntToInt", "IntToUint":
			v = randInt()
		default:
			v = randUint()
		}
		g.Add("rand", "convert_num", fn, fl, v, lo, hi, tp)
		_ = uhi
	}
	for _, tp := range signedTps {
		lo, hi, uhi := bounds(tp)
		for _, f := range datumEdgeFloats {
			g.Add("sys/edge", "convert_num", "FloatToInt", uint16(0), fBits(f), lo, hi, tp)
			for _, fl := range datumFlags[:4] {
				g.Add("sys/edge", "convert_num", "FloatToUint", fl, fBits(f), lo, uhi, tp)
			}
		}
		for _, v := range datumEdgeInts {
			g.Add("sys/edge", "convert_num", "IntToInt", uint16(0), v, lo, hi, tp)
			g.Add("sys/edge", "convert_num", "IntToUint", datumFlags[0], v, lo, uhi, tp)
			g.Add("sys/edge", "convert_num", "IntToUint", datumFlags[3], v, lo, uhi, tp)
		}
		for _, v := range datumEdgeUints {
			g.Add("sys/edge", "convert_num", "UintToInt", uint16(0), v, lo, hi, tp)
			g.Add("sys/edge", "convert_num", "UintToUint", uint16(0), v, lo, uhi, tp)
		}
	}
	decStrs := append([]string{"0", "1", "0.5", "0.4", "-0.5", "-1", "18446744073709551615", "18446744073709551615.5",
		"18446744073709551614.5", "18446744073709551616", "99999999999999999999.9", "1e5", "1.5e3", "123.456e-2",
		"0.00001", "255.5", "65535.4", "4294967295.5"}, litStrs...)
	for _, s := range decStrs {
		for _, tp := range []byte{mysql.TypeTiny, mysql.TypeLong, mysql.TypeLonglong} {
			_, _, uhi := bounds(tp)
			g.Add("sys/dec", "convert_decimal_to_uint", vec.GoString(s), uhi, tp)
		}
	}
	for i := 0; i < 400; i++ {
		s := fmt.Sprintf("%d.%d", rng.Uint64()>>rng.IntN(64), rng.IntN(100000))
		if rng.IntN(3) == 0 {
			s = "-" + s
		}
		g.Add("rand", "convert_decimal_to_uint", vec.GoString(s), uint64(math.MaxUint64), mysql.TypeLonglong)
	}
	strNum := func(src, s string) {
		for _, kind := range []string{"int", "uint", "float"} {
			for _, fc := range []bool{false, true} {
				g.Add(src, "str_to_num", datumFlags[rng.IntN(3)], kind, vec.GoString(s), fc)
			}
		}
	}
	for _, s := range datumEdgeStrings {
		for _, fl := range datumFlags[:3] {
			for _, kind := range []string{"int", "uint", "float"} {
				for _, fc := range []bool{false, true} {
					g.Add("sys/edge", "str_to_num", fl, kind, vec.GoString(s), fc)
				}
			}
		}
	}
	for _, l := range lits {
		if l.str != nil {
			strNum(l.fn, *l.str)
		}
	}
	for i := 0; i < 2500; i++ {
		strNum("rand", randNumStr())
	}
	timeStrs := append(append([]string{}, datumEdgeStrings...), litStrs...)
	for _, s := range timeStrs {
		for _, fsp := range []int{0, 3, 6} {
			g.Add("sys/str", "str_to_time", datumFlags[1], "duration", vec.GoString(s), fsp)
			g.Add("sys/str", "str_to_time", uint16(0), "datetime", vec.GoString(s), fsp)
		}
	}
	for _, n := range append(append([]int64{}, datumEdgeInts...), litInts...) {
		for _, fsp := range []int{0, 2, 6} {
			g.Add("sys/num", "number_to_duration", n, fsp)
		}
	}
	for i := 0; i < 400; i++ {
		g.Add("rand", "number_to_duration", randInt()%100000000000000, rng.IntN(7))
	}
	// JSON values
	jsonTexts := []string{"null", "true", "false", "0", "-0", "1", "-1", "1.5", "-1.5", "2.5", "1e20", "-1e20", "1e300",
		"9223372036854775807", "9223372036854775808", "18446744073709551615", "18446744073709551616",
		"-9223372036854775808", "-9223372036854775809", "127", "128", "-129", "255", "256", "65535", "65536",
		`"123"`, `"-123"`, `"12.5"`, `"abc"`, `"1e3"`, `""`, `"-"`, `" 12 "`, `"18446744073709551615"`, `"-0"`,
		`"1e400"`, `[]`, `[1]`, `{}`, `{"a":1}`, `0.1`, `123.456`}
	var bjs []types.BinaryJSON
	for _, t := range jsonTexts {
		j, err := types.ParseBinaryJSONFromString(t)
		if err == nil {
			bjs = append(bjs, j)
		}
	}
	for _, l := range lits {
		if l.str != nil {
			if j, err := types.ParseBinaryJSONFromString(*l.str); err == nil {
				bjs = append(bjs, j)
			}
		}
	}
	bjs = append(bjs, types.CreateBinaryJSON(types.NewTime(types.FromDate(2001, 2, 3, 4, 5, 6, 0), mysql.TypeDatetime, 0)),
		types.CreateBinaryJSON(types.Duration{Duration: 3 * gotime.Hour, Fsp: 0}),
		types.CreateBinaryJSON(types.Opaque{TypeCode: mysql.TypeVarString, Buf: []byte("ab")}))
	for _, j := range bjs {
		for _, fl := range datumFlags[:4] {
			g.Add("sys/json", "convert_json", fl, "int64", bjArg(j), false)
			g.Add("sys/json", "convert_json", fl, "int64", bjArg(j), true)
			for _, tp := range []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeLong} {
				g.Add("sys/json", "convert_json", fl, "int", bjArg(j), rng.IntN(2) == 0, tp)
			}
			g.Add("sys/json", "convert_json", fl, "float", bjArg(j))
			g.Add("sys/json", "convert_json", fl, "decimal", bjArg(j))
		}
	}
	for i := 0; i < 300; i++ {
		var j types.BinaryJSON
		switch rng.IntN(4) {
		case 0:
			j = types.CreateBinaryJSON(randInt())
		case 1:
			j = types.CreateBinaryJSON(randUint())
		case 2:
			f := randFloat()
			if math.IsNaN(f) || math.IsInf(f, 0) {
				f = 0
			}
			j = types.CreateBinaryJSON(f)
		default:
			j = types.CreateBinaryJSON(randNumStr())
		}
		g.Add("rand/json", "convert_json", datumFlags[rng.IntN(4)], []string{"int64", "float", "decimal"}[rng.IntN(3)], bjArg(j), rng.IntN(2) == 0)
	}
	for _, s := range []string{"", "a", "abc", "中文", "\xff\xfe"} {
		for _, n := range []int{-1, 0, 1, 2, 3, 10} {
			g.Add("sys", "truncate_str", vec.GoString(s), n)
		}
	}
	for _, name := range []string{"nil", "plain", "ErrTruncatedWrongVal", "ErrTruncated", "ErrOverflow", "ErrBadNumber",
		"ErrDataTooLong", "ErrWarnDataOutOfRange", "ErrIncorrectDatetimeValue", "ErrDatetimeFunctionOverflow",
		"ErrWrongValue", "ErrInvalidCharacterString"} {
		for _, fl := range datumFlags[:3] {
			g.Add("sys", "handle_truncate", fl, name)
		}
	}

	// ---- binary literal
	for _, v := range append(datumEdgeUints, 0x0102, 0xff00, 0x10000) {
		for _, n := range []int{-1, 1, 2, 3, 4, 5, 6, 7, 8} {
			g.Add("sys", "binlit_from_uint", v, n)
		}
	}
	randHex := func() string {
		b := make([]byte, rng.IntN(12))
		for i := range b {
			if rng.IntN(3) == 0 {
				b[i] = 0
			} else {
				b[i] = byte(rng.Uint32())
			}
		}
		return hex.EncodeToString(b)
	}
	for i := 0; i < 600; i++ {
		g.Add("rand", "binlit", datumFlags[rng.IntN(3)], randHex(), randHex())
	}
	bitHex := func(src, s string) {
		g.Add(src, "parse_bit_hex", "bit", vec.GoString(s))
		g.Add(src, "parse_bit_hex", "hex", vec.GoString(s))
	}
	for _, s := range datumEdgeStrings {
		bitHex("sys/edge", s)
	}
	for _, l := range lits {
		if l.str != nil {
			bitHex(l.fn, *l.str)
		}
	}
	for i := 0; i < 500; i++ {
		var b strings.Builder
		b.WriteString([]string{"b'", "B'", "0b", "x'", "X'", "0x", "", "b", "x"}[rng.IntN(9)])
		alpha := "01'2aAfFgG \xff"
		for n := rng.IntN(20); n > 0; n-- {
			if rng.IntN(4) == 0 {
				b.WriteByte(alpha[rng.IntN(len(alpha))])
			} else {
				b.WriteByte("0123456789abcdef"[rng.IntN(16)])
			}
		}
		if rng.IntN(2) == 0 {
			b.WriteByte('\'')
		}
		bitHex("rand", b.String())
	}

	// ---- enum / set
	elemSets := [][]string{{"a", "b", "c"}, {"A", "b", "a"}, {"", "x"}, {"中", "文"}, {"a ", "a"}, {"1", "2", "10"},
		{"k", "K", "ß", "ss"}, {}}
	big := make([]string, 64)
	for i := range big {
		big[i] = fmt.Sprintf("e%d", i)
	}
	elemSets = append(elemSets, big)
	names := []string{"a", "A", "b", "c", "d", "", "x", "1", "2", "3", "0", "10", "0x2", "0b1", "07", "0o7", "1_0", "-1",
		"a,b", "b,a", "a,,b", "A,c", "a ", "a,a", "ss", "ß", "K", "中", "e63", "e0,e63", "18446744073709551615", "4", "7", "8",
		"0x1_", "0x_1", "0__1", "1__0", "_1", "0b1_", "0o_7", "0X1", "0B1", "0O7", "08", "0x", "+1", "18446744073709551616", "0xffffffffffffffff", "0x10000000000000000"}
	for _, l := range lits {
		if l.str != nil && (l.fn == "TestEnum" || l.fn == "TestSet") {
			names = append(names, *l.str)
		}
	}
	for _, el := range elemSets {
		gel := make([]vec.GoString, len(el))
		for i, e := range el {
			gel[i] = vec.GoString(e)
		}
		for _, n := range names {
			for _, coll := range []string{"utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "binary"} {
				g.Add("sys", "enum", "parse", gel, vec.GoString(n), coll)
				g.Add("sys", "set", "parse", gel, vec.GoString(n), coll)
			}
			g.Add("sys", "enum", "name", gel, vec.GoString(n), "utf8mb4_bin")
			g.Add("sys", "set", "name", gel, vec.GoString(n), "utf8mb4_general_ci")
		}
		for _, v := range []uint64{0, 1, 2, 3, 4, 5, 7, 8, 64, 65, 1 << 62, 1<<63 | 1, math.MaxUint64} {
			g.Add("sys", "enum", "value", gel, v)
			if len(el) == 64 || v < 1<<uint(len(el)) || len(el) < 64 {
				g.Add("sys", "set", "value", gel, v)
			}
		}
	}
	for _, fn := range []string{"TestEnum", "TestSet"} {
		g.Add(fn, "enum", "value", []vec.GoString{"a"}, uint64(1))
	}

	// ---- compare
	for i := 0; i < 50; i++ {
		n := rng.IntN(8)
		xu, yu := make([]uint64, n), make([]uint64, n)
		xi, yi := make([]int64, n), make([]int64, n)
		for k := 0; k < n; k++ {
			xu[k], yu[k], xi[k], yi[k] = randUint(), randUint(), randInt(), randInt()
			if rng.IntN(3) == 0 {
				yu[k] = xu[k]
				yi[k] = int64(xu[k])
			}
		}
		g.Add("rand", "vec_compare", "UU", xu, yu)
		g.Add("rand", "vec_compare", "II", xi, yi)
		g.Add("rand", "vec_compare", "UI", xu, yi)
		g.Add("rand", "vec_compare", "IU", xi, yu)
	}
	for _, a := range datumEdgeInts {
		for _, b := range []int64{0, -1, 1, math.MaxInt64, math.MinInt64} {
			for _, u0 := range []bool{false, true} {
				for _, u1 := range []bool{false, true} {
					g.Add("sys", "compare_int", a, u0, b, u1)
				}
			}
		}
	}
	for i := 0; i < 600; i++ {
		g.Add("rand", "compare_int", randInt(), rng.IntN(2) == 0, randInt(), rng.IntN(2) == 0)
	}
	cmpStrs := []string{"", "a", "A", "a ", "b", "ß", "ss", "中", "\xff", "abc", "ABC"}
	for _, x := range cmpStrs {
		for _, y := range cmpStrs {
			g.Add("sys", "compare_string", vec.GoString(x), vec.GoString(y), datumCollations[rng.IntN(len(datumCollations))])
		}
	}
	for _, fn := range []string{"TestCompare", "TestVecCompareIntAndUint"} {
		g.Add(fn, "compare_int", int64(1), false, int64(2), true)
	}

	// ---- etc
	for tp := 0; tp < 256; tp++ {
		g.Add("sys", "type_preds", byte(tp))
	}
	for _, tp := range []byte{mysql.TypeBit, mysql.TypeVarchar, mysql.TypeString, mysql.TypeVarString, mysql.TypeBlob,
		mysql.TypeLonglong, mysql.TypeUnspecified, mysql.TypeJSON, mysql.TypeEnum} {
		for _, coll := range append(datumCollations, "") {
			g.Add("sys", "ft_preds", tp, coll)
		}
	}
	for k := 0; k < 25; k++ {
		g.Add("sys", "kind", byte(k))
	}
	for _, fn := range []string{"TestIsBinaryStr", "TestIsNonBinaryStr", "TestNeedRestoredData"} {
		g.Add(fn, "ft_preds", mysql.TypeVarchar, "utf8mb4_bin")
	}

	// ---- encodings
	encInputs := []string{"", "a", "abc", "ABC", "中文", "\xe4\xb8", "\xff", "\x80", "\x81\x40", "\xa1\xa1", "\xfe\xfe",
		"\x80\x80", "€", "é", "\xc3\xa9", "\xf0\x9f\x98\x80", "\xed\xa0\x80", "\xef\xbf\xbd", "?", "a\x00b", "\xc0\xaf"}
	for _, l := range lits {
		if l.str != nil {
			encInputs = append(encInputs, *l.str)
		}
	}
	for _, cs := range datumCharsets {
		for _, in := range encInputs {
			g.Add("sys", "encoding", cs, hex.EncodeToString([]byte(in)))
		}
	}
	for i := 0; i < 1500; i++ {
		b := make([]byte, rng.IntN(10))
		for k := range b {
			switch rng.IntN(4) {
			case 0:
				b[k] = byte('a' + rng.IntN(26))
			case 1:
				b[k] = byte(0x80 + rng.IntN(0x80))
			default:
				b[k] = byte(rng.Uint32())
			}
		}
		g.Add("rand", "encoding", datumCharsets[rng.IntN(len(datumCharsets))], hex.EncodeToString(b))
	}
	for _, fn := range DatumMinedTests() {
		// every mined test function is a record src (each literal above is attributed to it via its own loop;
		// this marks the functions whose literals only fed shared pools)
		g.Add(fn, "kind", byte(0))
	}
}
