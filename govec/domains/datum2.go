package domains

// Domain "datum", part 2 (block D01.1: datum.go, field_type.go,
// field_type_builder.go, field_name.go, datum_eval.go, etc.go InvOp2,
// convert.go ToString).
//
// Values:
//   - a datum input is [kind, payload...] (see datumIn); a datum output is its
//     observable state (datumOut): kind, frac, length, i, collation, b (hex, null
//     for a nil slice) and, for decimal / time kinds, the decimal / time;
//   - a field type is [tp, flag, flen, decimal, charset, collate, elems|null];
//   - the literals of datum_test.go and field_type_test.go (cmd/minelits,
//     testdata/datum2_literals.tsv) feed the string datums.

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	gotime "time"
	"unicode/utf8"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
)

//go:embed testdata/datum2_literals.tsv
var datum2LiteralsTSV string

func datum2Literals() []datumLit { return parseDatumLiterals(datum2LiteralsTSV) }

// The D01.1 functions part 2 leaves to other blocks or does not port.
var datum2Exempt = map[string]string{
	"Datum.Hash64":                "calls Hash64ForDatum, set by the codec package (D01.8); covered there",
	"FieldTypeBuilder.SetElems":   "covered through NewFieldType / the ft decoder (elems are set on every field type input)",
	"FieldTypeBuilder.SetArray":   "a one-line setter of FieldType.SetArray (D02.3)",
	"FieldTypeBuilder.ToggleFlag": "a one-line setter of FieldType.ToggleFlag (D02.3)",
	"FieldTypeBuilder.Build":      "returns the built FieldType; covered by ft_builder",
	"datumsSorter.Len":            "sort.Interface method; covered by d_list sort",
	"datumsSorter.Swap":           "sort.Interface method; covered by d_list sort",
}

var datum2Coverage = map[string]vec.Coverage{
	"d_convert": {Min: 8000, GoFuncs: []string{"Datum.ConvertTo", "Datum.convertToFloat", "ProduceFloatWithSpecifiedTp",
		"Datum.convertToString", "ProduceStrWithSpecifiedTp", "Datum.convertToInt", "Datum.convertToUint",
		"Datum.convertToMysqlTimestamp", "Datum.convertToMysqlTime", "Datum.convertToMysqlDuration",
		"Datum.convertToMysqlDecimal", "ProduceDecWithSpecifiedTp", "Datum.ConvertToMysqlYear",
		"Datum.convertToMysqlBit", "Datum.convertToMysqlEnum", "Datum.convertToMysqlSet",
		"Datum.convertToMysqlJSON", "Datum.convertToVectorFloat32", "invalidConv", "Datum.toSignedInteger",
		"Datum.GetBinaryStringEncoded", "Datum.GetBinaryStringDecoded", "Datum.GetStringWithCheck", "findEncoding",
		"NewIntDatum",
		// the accessors datumIn / datumOut build and read every datum with
		"Datum.Collation", "Datum.Frac", "Datum.GetBytes", "Datum.GetFloat32", "Datum.GetFloat64", "Datum.GetInt64",
		"Datum.GetMysqlDecimal", "Datum.GetMysqlDuration", "Datum.GetMysqlEnum", "Datum.GetMysqlJSON", "Datum.GetMysqlSet",
		"Datum.GetMysqlTime", "Datum.GetString", "Datum.GetUint64", "Datum.GetVectorFloat32", "Datum.IsNull", "Datum.Kind",
		"Datum.Length", "Datum.SetBinaryLiteral", "Datum.SetBytes", "Datum.SetFloat32", "Datum.SetFloat64", "Datum.SetInt64",
		"Datum.SetMysqlBit", "Datum.SetMysqlDecimal", "Datum.SetMysqlDuration", "Datum.SetMysqlEnum", "Datum.SetMysqlJSON",
		"Datum.SetMysqlSet", "Datum.SetMysqlTime", "Datum.SetString", "Datum.SetUint64", "Datum.SetVectorFloat32"}},
	"context": {Min: 100, GoFuncs: []string{"NewContext", "Context.Flags", "Context.WithFlags", "Context.Location",
		"Context.WithLocation", "Context.AppendWarning", "Flags.AllowNegativeToUnsigned", "Flags.CastTimeToYearThroughConcat",
		"Flags.IgnoreInvalidDateErr", "Flags.IgnoreTruncateErr", "Flags.IgnoreZeroDateErr", "Flags.IgnoreZeroInDate",
		"Flags.SkipASCIICheck", "Flags.SkipUTF8Check", "Flags.SkipUTF8MB4Check", "Flags.TruncateAsWarning",
		"Flags.WithAllowNegativeToUnsigned", "Flags.WithCastTimeToYearThroughConcat", "Flags.WithIgnoreInvalidDateErr",
		"Flags.WithIgnoreTruncateErr", "Flags.WithIgnoreZeroDateErr", "Flags.WithIgnoreZeroInDate", "Flags.WithSkipSACIICheck",
		"Flags.WithSkipUTF8Check", "Flags.WithSkipUTF8MB4Check", "Flags.WithTruncateAsWarning"}},
	"d_compare": {Min: 5000, GoFuncs: []string{"Datum.Compare", "Datum.compareInt64", "Datum.compareUint64",
		"Datum.compareFloat64", "Datum.compareString", "Datum.compareMysqlDecimal", "Datum.compareMysqlDuration",
		"Datum.compareMysqlEnum", "Datum.compareBinaryLiteral", "Datum.compareMysqlSet", "Datum.compareMysqlJSON",
		"Datum.compareMysqlTime", "Datum.compareVectorFloat32", "Datum.GetBinaryLiteral4Cmp", "Datum.Equals"}},
	"d_to": {Min: 3000, GoFuncs: []string{"Datum.ToBool", "Datum.ToDecimal", "ConvertDatumToDecimal",
		"Datum.ToInt64", "Datum.ToFloat64", "Datum.ToString", "Datum.ToBytes", "Datum.ToHashKey", "Datum.ToMysqlJSON",
		"Datum.String", "Datum.GetValue", "Datum.TruncatedStringify", "truncateStringIfNeeded", "Datum.MemUsage",
		"Datum.EstimatedMemUsage", "Datum.MarshalJSON", "Datum.Clone", "Datum.Copy", "ToString",
		"Datum.GetInterface", "Datum.GetRaw", "Datum.GetMysqlBit", "Datum.GetBinaryLiteral"}},
	"d_unmarshal": {Min: 300, GoFuncs: []string{"Datum.UnmarshalJSON"}},
	"d_list": {Min: 500, GoFuncs: []string{"SortDatums", "datumsSorter.Less", "DatumsToString", "DatumsToStringSmart",
		"datumsToString", "isPrintable", "DatumsToStrNoErr", "DatumsToStrNoErrSmart", "CloneRow", "EstimatedMemUsage",
		"DatumsContainNull"}},
	"d_bound": {Min: 70, GoFuncs: []string{"GetMaxValue", "GetMinValue"}},
	// d_convert_recv: the receiver after ConvertTo (Go's pointer receiver; convertToMysqlBit sets
	// d.k = KindUint64 on a KindInt64 receiver, visible to callers: upstream-pitfall tidb-rs#34).
	"d_convert_recv": {Min: 3000, GoFuncs: []string{"Datum.ConvertTo", "Datum.convertToMysqlBit"}},
	"d_plus":         {Min: 500, GoFuncs: []string{"ComputePlus", "InvOp2"}},
	"d_new": {Min: 300, GoFuncs: []string{"NewDatum", "MakeDatums", "Datum.SetValueWithDefaultCollation", "Datum.SetValue",
		"Datum.SetInterface", "InferParamTypeFromDatum", "InferParamTypeFromUnderlyingValue",
		"hasVariantFieldLength", "DefaultTypeForValue", "SetBinChsClnFlag"}},
	"d_ctor": {Min: 50, GoFuncs: []string{"Datum.SetNull", "Datum.SetMinNotNull", "MinNotNullDatum", "MaxValueDatum",
		"NewUintDatum", "NewBytesDatum", "NewStringDatum", "NewCollationStringDatum", "NewFloat64Datum",
		"NewFloat32Datum", "NewDurationDatum", "NewTimeDatum", "NewDecimalDatum", "NewJSONDatum", "NewVectorFloat32Datum",
		"NewBinaryLiteralDatum", "NewMysqlBitDatum", "NewMysqlEnumDatum", "NewCollateMysqlEnumDatum", "NewMysqlSetDatum",
		"Datum.SetAutoID", "Datum.SetFloat32FromF64", "Datum.SetBytesAsString", "Datum.SetRaw", "Datum.SetCollation",
		"Datum.SetFrac", "Datum.SetLength"}},
	"ft": {Min: 2000, GoFuncs: []string{"AggFieldType", "mergeFieldType", "mergeTypeFlag", "getFieldTypeIndex",
		"AggregateEvalType", "mergeEvalType", "SetTypeFlag", "TryToFixFlenOfDatetime", "NewFieldType",
		"minFlenAndDecimalForType", "DefaultCharsetForType", "NewFieldTypeWithCollation", "CheckModifyTypeCompatible",
		"needReorgToChange", "checkTypeChangeSupported", "ConvertBetweenCharAndVarchar", "IsVarcharTooBigFieldLength"}},
	"ft_builder": {Min: 50, GoFuncs: []string{"NewFieldTypeBuilder", "FieldTypeBuilder.GetType", "FieldTypeBuilder.GetFlag",
		"FieldTypeBuilder.GetFlen", "FieldTypeBuilder.GetDecimal", "FieldTypeBuilder.GetCharset",
		"FieldTypeBuilder.GetCollate", "FieldTypeBuilder.SetType", "FieldTypeBuilder.SetFlag", "FieldTypeBuilder.AddFlag",
		"FieldTypeBuilder.DelFlag", "FieldTypeBuilder.SetFlen", "FieldTypeBuilder.SetDecimal", "FieldTypeBuilder.SetCharset",
		"FieldTypeBuilder.SetCollate", "FieldTypeBuilder.BuildP"}},
	"field_name": {Min: 50, GoFuncs: []string{"FieldName.String", "FieldName.MemoryUsage", "FieldName.Clone", "NameSlice.Shallow"}},
}

// ---------------------------------------------------------------- values

// datumIn builds a datum: [kind, payload...].
func d2DatumIn(p []json.RawMessage) types.Datum {
	var d types.Datum
	k := arg[byte](p, 0)
	s := func(i int) string { return string(arg[vec.GoString](p, i)) }
	h := func(i int) []byte {
		if string(p[i]) == "null" {
			return nil
		}
		b, err := hex.DecodeString(arg[string](p, i))
		if err != nil {
			panic(err)
		}
		return b
	}
	switch k {
	case types.KindNull:
		d.SetNull()
	case types.KindMinNotNull:
		d.SetMinNotNull()
	case types.KindMaxValue:
		d = types.MaxValueDatum()
	case types.KindInt64:
		d.SetInt64(arg[int64](p, 1))
	case types.KindUint64:
		d.SetUint64(arg[uint64](p, 1))
	case types.KindFloat32:
		d.SetFloat32(float32(bitsF(arg[uint64](p, 1))))
	case types.KindFloat64:
		d.SetFloat64(bitsF(arg[uint64](p, 1)))
	case types.KindString:
		d.SetString(s(1), arg[string](p, 2))
	case types.KindBytes:
		d.SetBytes(h(1))
	case types.KindBinaryLiteral:
		d.SetBinaryLiteral(h(1))
	case types.KindMysqlBit:
		d.SetMysqlBit(h(1))
	case types.KindMysqlDecimal:
		dec := new(types.MyDecimal)
		_ = dec.FromString([]byte(arg[string](p, 1)))
		d.SetMysqlDecimal(dec)
	case types.KindMysqlDuration:
		d.SetMysqlDuration(types.Duration{Duration: gotime.Duration(arg[int64](p, 1)), Fsp: arg[int](p, 2)})
	case types.KindMysqlEnum:
		d.SetMysqlEnum(types.Enum{Value: arg[uint64](p, 1), Name: s(2)}, arg[string](p, 3))
	case types.KindMysqlSet:
		d.SetMysqlSet(types.Set{Value: arg[uint64](p, 1), Name: s(2)}, arg[string](p, 3))
	case types.KindMysqlTime:
		d.SetMysqlTime(timeOfRaw(arg[uint64](p, 1)))
	case types.KindRaw:
		d.SetRaw(h(1))
	case types.KindMysqlJSON:
		d.SetMysqlJSON(types.BinaryJSON{TypeCode: arg[byte](p, 1), Value: h(2)})
	case types.KindVectorFloat32:
		v, err := types.ParseVectorFloat32(arg[string](p, 1))
		if err != nil {
			panic(err)
		}
		d.SetVectorFloat32(v)
	default:
		panic(fmt.Sprintf("datumIn: kind %d", k))
	}
	return d
}

func timeOfRaw(raw uint64) types.Time {
	var t types.Time
	*(*uint64)(unsafe.Pointer(&t)) = raw
	return t
}

type d2DatumOutT struct {
	K    byte         `json:"k"`
	Frac int          `json:"frac"`
	Len  int          `json:"len"`
	I    int64        `json:"i"`
	Coll vec.GoString `json:"coll"`
	B    string       `json:"b"`
	// RawNil: a KindRaw datum's slice is nil. For the other kinds Go's nil-ness of b
	// follows the Go string's data pointer (hack.Slice of an empty string): not compared.
	RawNil bool      `json:"raw_nil,omitempty"`
	Dec    *Dec      `json:"dec,omitempty"`
	Time   *timeOutT `json:"time,omitempty"`
}

func d2DatumOut(d types.Datum) d2DatumOutT {
	o := d2DatumOutT{K: d.Kind(), Frac: d.Frac(), Len: d.Length(), I: d.GetInt64(), Coll: vec.GoString(d.Collation())}
	o.B = hex.EncodeToString(d.GetRaw())
	if d.Kind() == types.KindRaw && d.GetRaw() == nil {
		o.RawNil = true
	}
	switch d.Kind() {
	case types.KindMysqlDecimal:
		x := decOut(d.GetMysqlDecimal())
		o.Dec = &x
	case types.KindMysqlTime:
		x := timeOut(d.GetMysqlTime())
		o.Time = &x
	}
	return o
}

func datumOuts(ds []types.Datum) []d2DatumOutT {
	out := make([]d2DatumOutT, len(ds))
	for i, d := range ds {
		out[i] = d2DatumOut(d)
	}
	return out
}

// ftIn builds a field type: [tp, flag, flen, decimal, charset, collate, elems|null].
func d2FtIn(p []json.RawMessage) *types.FieldType {
	ft := types.NewFieldTypeBuilder().SetType(arg[byte](p, 0)).SetFlag(arg[uint](p, 1)).
		SetFlen(arg[int](p, 2)).SetDecimal(arg[int](p, 3)).SetCharset(arg[string](p, 4)).
		SetCollate(arg[string](p, 5)).BuildP()
	if string(p[6]) != "null" {
		ft.SetElems(gsSlice(arg[[]vec.GoString](p, 6)))
	}
	return ft
}

type d2FtOutT struct {
	Tp    byte           `json:"tp"`
	Flag  uint           `json:"flag"`
	Flen  int            `json:"flen"`
	Dec   int            `json:"dec"`
	Cs    string         `json:"cs"`
	Co    string         `json:"co"`
	Elems []vec.GoString `json:"elems"`
}

func d2FtOut(ft *types.FieldType) d2FtOutT {
	o := d2FtOutT{Tp: ft.GetType(), Flag: ft.GetFlag(), Flen: ft.GetFlen(), Dec: ft.GetDecimal(), Cs: ft.GetCharset(), Co: ft.GetCollate()}
	if e := ft.GetElems(); e != nil {
		o.Elems = make([]vec.GoString, len(e))
		for i, s := range e {
			o.Elems[i] = vec.GoString(s)
		}
	}
	return o
}

func rawArg(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func rawList(p []json.RawMessage) [][]json.RawMessage {
	var out [][]json.RawMessage
	for _, x := range p {
		var q []json.RawMessage
		if err := json.Unmarshal(x, &q); err != nil {
			panic(err)
		}
		out = append(out, q)
	}
	return out
}

func d2ErrMsg(err error) any {
	if err == nil {
		return nil
	}
	return vec.GoString(err.Error())
}

// goValueIn builds a Go dynamic value: [type, payload].
func goValueIn(p []json.RawMessage) any {
	switch arg[string](p, 0) {
	case "nil":
		return nil
	case "bool":
		return arg[bool](p, 1)
	case "int":
		return int(arg[int64](p, 1))
	case "int64":
		return arg[int64](p, 1)
	case "uint64":
		return arg[uint64](p, 1)
	case "float32":
		return float32(bitsF(arg[uint64](p, 1)))
	case "float64":
		return bitsF(arg[uint64](p, 1))
	case "string":
		return string(arg[vec.GoString](p, 1))
	case "bytes":
		if string(p[1]) == "null" {
			return []byte(nil)
		}
		b, _ := hex.DecodeString(arg[string](p, 1))
		return b
	case "decimal":
		dec := new(types.MyDecimal)
		_ = dec.FromString([]byte(arg[string](p, 1)))
		return dec
	case "duration":
		return types.Duration{Duration: gotime.Duration(arg[int64](p, 1)), Fsp: arg[int](p, 2)}
	case "enum":
		return types.Enum{Value: arg[uint64](p, 1), Name: string(arg[vec.GoString](p, 2))}
	case "set":
		return types.Set{Value: arg[uint64](p, 1), Name: string(arg[vec.GoString](p, 2))}
	case "binlit":
		return binLitOf(arg[string](p, 1))
	case "bitlit":
		return types.BitLiteral(binLitOf(arg[string](p, 1)))
	case "hexlit":
		return types.HexLiteral(binLitOf(arg[string](p, 1)))
	case "json":
		v, _ := hex.DecodeString(arg[string](p, 2))
		return types.BinaryJSON{TypeCode: arg[byte](p, 1), Value: v}
	case "time":
		return timeOfRaw(arg[uint64](p, 1))
	case "vector":
		v, err := types.ParseVectorFloat32(arg[string](p, 1))
		if err != nil {
			panic(err)
		}
		return v
	case "slice":
		var out []any
		for _, q := range rawList(arg[[]json.RawMessage](p, 1)) {
			out = append(out, goValueIn(q))
		}
		return out
	}
	panic("goValueIn: " + arg[string](p, 0))
}

// ---------------------------------------------------------------- wall clock

// clockMark replaces a converted value that depends on the wall clock.
const clockMark = "<clock>"

// convertReadsClock reports whether Datum.ConvertTo reads the wall clock: a Duration
// converted to TIMESTAMP / DATETIME / DATE takes today's date (Duration.ConvertToTime:
// gotime.Now()), and to YEAR the current year (Duration.ConvertToYear). The value is
// recorded as clockMark (the replay masks it the same way); err and warnings are kept.
// The date paths themselves are covered with a given instant by the time domain
// (dur_to_time / dur_to_year: ConvertToTimeWithTimestamp, ConvertToYearFromNow).
func convertReadsClock(d types.Datum, ft *types.FieldType) bool {
	if d.Kind() != types.KindMysqlDuration {
		return false
	}
	switch ft.GetType() {
	case mysql.TypeTimestamp, mysql.TypeDatetime, mysql.TypeDate, mysql.TypeNewDate, mysql.TypeYear:
		return true
	}
	return false
}

// ---------------------------------------------------------------- exec

func datum2Exec(op string, args []json.RawMessage) (out any, err error, ok bool) {
	switch op {
	case "d_convert":
		ctx, w := datumCtx(args, 0)
		d := d2DatumIn(arg[[]json.RawMessage](args, 1))
		ft := d2FtIn(arg[[]json.RawMessage](args, 2))
		clock := convertReadsClock(d, ft)
		ret, err := d.ConvertTo(ctx, ft)
		// errors.Wrap(ErrTruncated, msg) of the enum / set conversions is recorded as
		// its cause (mapping rule M-ERR, as for the Enum parsers in part 1).
		if clock {
			return dw(w, clockMark), errors.Cause(err), true
		}
		return dw(w, d2DatumOut(ret)), errors.Cause(err), true
	case "d_convert_recv":
		ctx, w := datumCtx(args, 0)
		d := d2DatumIn(arg[[]json.RawMessage](args, 1))
		ft := d2FtIn(arg[[]json.RawMessage](args, 2))
		clock := convertReadsClock(d, ft)
		ret, err := d.ConvertTo(ctx, ft)
		var retOut any = d2DatumOut(ret)
		if clock {
			retOut = clockMark
		}
		return dw(w, map[string]any{"ret": retOut, "recv": d2DatumOut(d)}), errors.Cause(err), true
	case "d_compare":
		ctx, w := datumCtx(args, 0)
		a := d2DatumIn(arg[[]json.RawMessage](args, 1))
		b := d2DatumIn(arg[[]json.RawMessage](args, 2))
		cmp, err := a.Compare(ctx, &b, collate.GetCollator(arg[string](args, 3)))
		eq := a.Equals(b)
		return dw(w, map[string]any{"cmp": cmp, "eq": eq}), err, true
	case "d_to":
		ctx, w := datumCtx(args, 0)
		d := d2DatumIn(arg[[]json.RawMessage](args, 1))
		fn := arg[string](args, 2)
		var v any
		var e error
		switch fn {
		case "bool":
			v, e = d.ToBool(ctx)
		case "decimal":
			var dec *types.MyDecimal
			dec, e = d.ToDecimal(ctx)
			if dec != nil {
				v = decOut(dec)
			}
		case "int64":
			v, e = d.ToInt64(ctx)
		case "float64":
			var f float64
			f, e = d.ToFloat64(ctx)
			v = fBits(f)
		case "string":
			var s string
			s, e = d.ToString()
			v = vec.GoString(s)
		case "bytes":
			var b []byte
			b, e = d.ToBytes()
			v = hex.EncodeToString(b)
		case "hash_key":
			var b []byte
			b, e = d.ToHashKey()
			v = hex.EncodeToString(b)
		case "json":
			var j types.BinaryJSON
			j, e = d.ToMysqlJSON()
			v = bjArg(j)
		case "str":
			v = vec.GoString(d.String())
		case "trunc":
			v = vec.GoString(d.TruncatedStringify())
		case "value_str":
			var s string
			s, e = types.ToString(d.GetValue())
			v = vec.GoString(s)
		case "mem":
			v = map[string]any{"mem": d.MemUsage() - int64(cap(d.GetRaw())) + int64(len(d.GetRaw())), "est": d.EstimatedMemUsage()}
		case "marshal":
			var b []byte
			b, e = d.MarshalJSON()
			v = vec.GoString(b)
		case "lit4cmp":
			v = hex.EncodeToString(d.GetBinaryLiteral4Cmp())
		case "encoded":
			v = vec.GoString(d.GetBinaryStringEncoded())
		case "decoded":
			var s string
			s, e = d.GetBinaryStringDecoded(ctx.Flags(), arg[string](args, 3))
			v = vec.GoString(s)
		case "checked":
			var s string
			s, e = d.GetStringWithCheck(ctx.Flags(), arg[string](args, 3))
			v = vec.GoString(s)
		case "clone":
			c := d.Clone()
			v = map[string]any{"d": d2DatumOut(*c), "eq": c.Equals(d)}
		default:
			panic("d_to: " + fn)
		}
		return dw(w, v), e, true
	case "d_unmarshal":
		var d types.Datum
		err := d.UnmarshalJSON([]byte(arg[vec.GoString](args, 0)))
		if err != nil {
			return nil, err, true
		}
		return d2DatumOut(d), nil, true
	case "d_list":
		ctx, w := datumCtx(args, 1)
		var ds []types.Datum
		for _, q := range rawList(arg[[]json.RawMessage](args, 2)) {
			ds = append(ds, d2DatumIn(q))
		}
		switch arg[string](args, 0) {
		case "to_string":
			s, err := types.DatumsToString(ds, arg[bool](args, 3))
			return vec.GoString(s), err, true
		case "to_string_smart":
			s, err := types.DatumsToStringSmart(ds, arg[bool](args, 3))
			return vec.GoString(s), err, true
		case "no_err":
			return map[string]any{"s": vec.GoString(types.DatumsToStrNoErr(ds)), "smart": vec.GoString(types.DatumsToStrNoErrSmart(ds))}, nil, true
		case "sort":
			err := types.SortDatums(ctx, ds)
			return dw(w, datumOuts(ds)), err, true
		case "misc":
			c := types.CloneRow(ds)
			return map[string]any{"clone": datumOuts(c), "null": types.DatumsContainNull(ds),
				"est": types.EstimatedMemUsage(ds, arg[int](args, 3))}, nil, true
		}
		panic("d_list")
	case "d_bound":
		ft := d2FtIn(arg[[]json.RawMessage](args, 0))
		return map[string]any{"max": d2DatumOut(types.GetMaxValue(ft)), "min": d2DatumOut(types.GetMinValue(ft))}, nil, true
	case "d_plus":
		a := d2DatumIn(arg[[]json.RawMessage](args, 0))
		b := d2DatumIn(arg[[]json.RawMessage](args, 1))
		ret, err := types.ComputePlus(a, b)
		return d2DatumOut(ret), err, true
	case "d_new":
		v := goValueIn(arg[[]json.RawMessage](args, 0))
		d := types.NewDatum(v)
		var d2 types.Datum
		ft := d2FtIn(arg[[]json.RawMessage](args, 1))
		d2.SetValue(v, ft)
		var tp types.FieldType
		types.InferParamTypeFromDatum(&d, &tp)
		var tp2 types.FieldType
		types.DefaultTypeForValue(v, &tp2, "utf8mb4", "utf8mb4_bin")
		vs, verr := types.ToString(v)
		return map[string]any{"d": d2DatumOut(d), "d2": d2DatumOut(d2), "infer": d2FtOut(&tp), "def": d2FtOut(&tp2),
			"str": vec.GoString(d.String()), "to_string": vec.GoString(vs), "to_string_err": d2ErrMsg(verr)}, nil, true
	case "d_ctor":
		// The constructors and setters not reachable from NewDatum.
		n := arg[int64](args, 0)
		s := string(arg[vec.GoString](args, 1))
		coll := arg[string](args, 2)
		f := bitsF(arg[uint64](args, 3))
		out := map[string]any{}
		out["uint"] = d2DatumOut(types.NewUintDatum(uint64(n)))
		out["bytes"] = d2DatumOut(types.NewBytesDatum([]byte(s)))
		out["string"] = d2DatumOut(types.NewStringDatum(s))
		out["cstring"] = d2DatumOut(types.NewCollationStringDatum(s, coll))
		out["f64"] = d2DatumOut(types.NewFloat64Datum(f))
		out["f32"] = d2DatumOut(types.NewFloat32Datum(float32(f)))
		out["dur"] = d2DatumOut(types.NewDurationDatum(types.Duration{Duration: gotime.Duration(n), Fsp: int(n & 7)}))
		out["time"] = d2DatumOut(types.NewTimeDatum(timeOfRaw(uint64(n))))
		dec := new(types.MyDecimal)
		dec.FromInt(n)
		out["dec"] = d2DatumOut(types.NewDecimalDatum(dec))
		out["json"] = d2DatumOut(types.NewJSONDatum(types.CreateBinaryJSON(s)))
		out["binlit"] = d2DatumOut(types.NewBinaryLiteralDatum(types.BinaryLiteral(s)))
		out["bit"] = d2DatumOut(types.NewMysqlBitDatum(types.BinaryLiteral(s)))
		out["enum"] = d2DatumOut(types.NewMysqlEnumDatum(types.Enum{Name: s, Value: uint64(n)}))
		out["cenum"] = d2DatumOut(types.NewCollateMysqlEnumDatum(types.Enum{Name: s, Value: uint64(n)}, coll))
		out["set"] = d2DatumOut(types.NewMysqlSetDatum(types.Set{Name: s, Value: uint64(n)}, coll))
		out["minnn"] = d2DatumOut(types.MinNotNullDatum())
		out["max"] = d2DatumOut(types.MaxValueDatum())
		var d types.Datum
		d.SetAutoID(n, uint(arg[uint64](args, 4)))
		out["autoid"] = d2DatumOut(d)
		d.SetFloat32FromF64(f)
		out["f32f64"] = d2DatumOut(d)
		d.SetBytesAsString([]byte(s), coll, uint32(n))
		out["bas"] = d2DatumOut(d)
		d.SetRaw([]byte(s))
		out["raw"] = d2DatumOut(d)
		d.SetCollation(coll)
		d.SetFrac(int(n))
		d.SetLength(int(n))
		out["setters"] = d2DatumOut(d)
		d.SetMinNotNull()
		out["setminnn"] = d2DatumOut(d)
		d.SetNull()
		out["setnull"] = d2DatumOut(d)
		return out, nil, true
	case "ft":
		switch arg[string](args, 0) {
		case "agg":
			var fts []*types.FieldType
			for _, q := range rawList(arg[[]json.RawMessage](args, 1)) {
				fts = append(fts, d2FtIn(q))
			}
			flag := arg[uint](args, 2)
			et := types.AggregateEvalType(fts, &flag)
			agg := types.AggFieldType(fts)
			fixed := *agg
			types.TryToFixFlenOfDatetime(&fixed)
			return map[string]any{"agg": d2FtOut(agg), "fixed": d2FtOut(&fixed), "et": et, "flag": flag}, nil, true
		case "new":
			tp := arg[byte](args, 1)
			cs, co := types.DefaultCharsetForType(tp)
			return map[string]any{"ft": d2FtOut(types.NewFieldType(tp)), "cs": cs, "co": co}, nil, true
		case "new_coll":
			return d2FtOut(types.NewFieldTypeWithCollation(arg[byte](args, 1), arg[string](args, 2), arg[int](args, 3))), nil, true
		case "modify":
			canReorg, err := types.CheckModifyTypeCompatible(d2FtIn(arg[[]json.RawMessage](args, 1)), d2FtIn(arg[[]json.RawMessage](args, 2)))
			return canReorg, err, true
		case "char_varchar":
			return types.ConvertBetweenCharAndVarchar(arg[byte](args, 1), arg[byte](args, 2)), nil, true
		case "varchar_big":
			return nil, types.IsVarcharTooBigFieldLength(arg[int](args, 1), string(arg[vec.GoString](args, 2)), arg[string](args, 3)), true
		case "set_flag":
			flag := arg[uint](args, 1)
			types.SetTypeFlag(&flag, arg[uint](args, 2), arg[bool](args, 3))
			return flag, nil, true
		}
		panic("ft")
	case "ft_builder":
		b := types.NewFieldTypeBuilder().SetType(arg[byte](args, 0)).SetFlag(arg[uint](args, 1)).
			AddFlag(arg[uint](args, 2)).DelFlag(arg[uint](args, 3)).SetFlen(arg[int](args, 4)).
			SetDecimal(arg[int](args, 5)).SetCharset(arg[string](args, 6)).SetCollate(arg[string](args, 7))
		return map[string]any{"tp": b.GetType(), "flag": b.GetFlag(), "flen": b.GetFlen(), "dec": b.GetDecimal(),
			"cs": b.GetCharset(), "co": b.GetCollate(), "ft": d2FtOut(b.BuildP())}, nil, true
	case "context":
		f := types.Flags(arg[uint16](args, 0))
		on := arg[bool](args, 1)
		w := &datumWarns{}
		ctx := types.NewContext(f, gotime.UTC, w)
		loc, _ := gotime.LoadLocation(arg[string](args, 2))
		ctx1 := ctx.WithFlags(f.WithTruncateAsWarning(on))
		ctx2 := ctx1.WithLocation(loc)
		ctx2.AppendWarning(types.ErrTruncated)
		get := map[string]bool{"anu": f.AllowNegativeToUnsigned(), "cty": f.CastTimeToYearThroughConcat(),
			"iid": f.IgnoreInvalidDateErr(), "ite": f.IgnoreTruncateErr(), "izd": f.IgnoreZeroDateErr(),
			"izi": f.IgnoreZeroInDate(), "sa": f.SkipASCIICheck(), "su": f.SkipUTF8Check(), "su4": f.SkipUTF8MB4Check(),
			"taw": f.TruncateAsWarning()}
		with := map[string]uint16{"anu": uint16(f.WithAllowNegativeToUnsigned(on)),
			"cty": uint16(f.WithCastTimeToYearThroughConcat(on)), "iid": uint16(f.WithIgnoreInvalidDateErr(on)),
			"ite": uint16(f.WithIgnoreTruncateErr(on)), "izd": uint16(f.WithIgnoreZeroDateErr(on)),
			"izi": uint16(f.WithIgnoreZeroInDate(on)), "sa": uint16(f.WithSkipSACIICheck(on)),
			"su": uint16(f.WithSkipUTF8Check(on)), "su4": uint16(f.WithSkipUTF8MB4Check(on)),
			"taw": uint16(f.WithTruncateAsWarning(on))}
		return map[string]any{"get": get, "with": with, "flags": uint16(ctx2.Flags()), "orig": uint16(ctx.Flags()),
			"loc": ctx2.Location().String(), "loc0": ctx.Location().String(), "warns": len(w.ws)}, nil, true
	case "field_name":
		ci := func(i int) model.CIStr { return model.NewCIStr(arg[string](args, i)) }
		n := &types.FieldName{OrigTblName: ci(0), OrigColName: ci(1), DBName: ci(2), TblName: ci(3), ColName: ci(4),
			Hidden: arg[bool](args, 5)}
		c := n.Clone()
		sl := types.NameSlice{n, types.EmptyName}.Shallow()
		return map[string]any{"s": n.String(), "mem": n.MemoryUsage(), "clone_s": c.String(), "empty": sl[1].String(),
			"len": len(sl)}, nil, true
	}
	return nil, nil, false
}

var _ = ast.ColumnName{}

// ---------------------------------------------------------------- generator

func datum2Generate(g *vec.Gen, rng *rand.Rand) {
	lits := datum2Literals()
	var litStrs []string
	var litInts []int64
	for _, l := range lits {
		if l.str != nil {
			litStrs = append(litStrs, *l.str)
		} else {
			litInts = append(litInts, *l.num)
		}
	}
	colls := []string{"binary", "utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci",
		"gbk_chinese_ci", "latin1_bin", "utf8_bin", "ascii_bin"}
	// A pool of datums of every kind.
	var pool [][]any
	add := func(d ...any) { pool = append(pool, d) }
	add(types.KindNull)
	add(types.KindMinNotNull)
	add(types.KindMaxValue)
	for _, n := range []int64{0, 1, -1, 2, 7, 10, 59, 60, 99, 100, 101, 127, -128, 128, 255, 256, 1000, 1901, 2155,
		2156, 69, 70, 32767, -32768, 65535, 8385959, 8385960, -8385959, 101010, 20111110, 20111110111112,
		10000000000, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64, 1 << 53, -(1 << 53)} {
		add(types.KindInt64, n)
	}
	for _, u := range []uint64{0, 1, 255, 1 << 32, 1<<63 - 1, 1 << 63, math.MaxUint64, 20111110111112} {
		add(types.KindUint64, u)
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1.5, 2.5, -2.5, 1.23456789, 123.456, 1e10, -1e10,
		1e20, 1e300, -1e300, math.MaxFloat32, math.MaxFloat32 * 2, 3.4028236e38, 1e-10, 20111110111112.5,
		math.Inf(1), math.Inf(-1), math.NaN(), 9.223372036854776e18, 1.8446744073709552e19} {
		add(types.KindFloat64, fBits(f))
		add(types.KindFloat32, fBits(f))
	}
	strs := []string{"", " ", "0", "1", "-1", "1.5", "-1.5", "2.5", "1e3", "abc", "a", "A", "ä", "Ä", "中文", "  12  ",
		"12abc", "0x10", "1,2", "a,b", "b", "c", "2011-11-10", "2011-11-10 11:11:11.999999", "11:11:11", "-838:59:59",
		"839:00:00", "20111110", "99999999999", "1e400", "\x00", "a\x00", "\xff\xfe", "\xe4\xb8", "x\xe4\xb8\xadx",
		"{\"a\": 1}", "[1, 2]", "null", "true", "\"str\"", "[1,2,3]", "[1.5,-2]", "0000", "0", "00", "2155", "1901",
		"abcdefghij", "a   ", "abc\t\n", strings.Repeat("x", 70), strings.Repeat("中", 30), "\x81\x40", "\xa1\xa1"}
	strs = append(strs, litStrs...)
	for i, s := range strs {
		add(types.KindString, vec.GoString(s), colls[i%len(colls)])
		if i%3 == 0 {
			add(types.KindBytes, hex.EncodeToString([]byte(s)))
		}
	}
	add(types.KindBytes, nil)
	for _, h := range []string{"", "00", "01", "0000", "ff", "00ff", "0102", "616263", "ffffffffffffffff",
		"010000000000000000", "e4b8ad", "8140"} {
		add(types.KindBinaryLiteral, h)
		add(types.KindMysqlBit, h)
	}
	for _, s := range []string{"0", "1", "-1", "1.5", "-1.5", "2.5", "123.456", "-123.456", "99.9999", "0.000001",
		"1e20", "12345678901234567890.123456789", "-0.5", "0.5", "9223372036854775807", "18446744073709551616",
		"20111110111112", "101010.5"} {
		add(types.KindMysqlDecimal, s)
	}
	for _, d := range [][2]int64{{0, 0}, {1e9, 0}, {1500e6, 1}, {-1500e6, 1}, {3723e9 + 5e5, 6}, {-3723e9, 3},
		{838*3600e9 + 59*60e9 + 59e9, 0}, {43199999999000, 6}, {59999999999, 6}} {
		add(types.KindMysqlDuration, d[0], d[1])
	}
	for i, e := range []struct {
		v uint64
		n string
	}{{0, ""}, {1, "a"}, {2, "b"}, {3, "c"}, {1, "1"}, {2, "x,y"}} {
		add(types.KindMysqlEnum, e.v, vec.GoString(e.n), colls[i%len(colls)])
		add(types.KindMysqlSet, e.v, vec.GoString(e.n), colls[(i+1)%len(colls)])
	}
	ctx := types.DefaultStmtNoWarningContext
	for _, s := range []string{"2011-11-10 11:11:11.999999", "2011-11-10 11:59:59.999999", "0000-00-00 00:00:00",
		"1970-01-01 00:00:01", "2038-01-19 03:14:07", "9999-12-31 23:59:59", "2011-11-10", "1000-01-01"} {
		for _, tp := range []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp} {
			t, err := types.ParseTime(ctx, s, tp, types.MaxFsp)
			if err == nil {
				add(types.KindMysqlTime, rawOfTime(t))
			}
		}
	}
	for _, js := range []string{"null", "true", "false", "0", "1", "-1", "1.5", "\"abc\"", "\"2011-11-10\"",
		"\"11:11:11\"", "[1,2]", "{\"a\":1}", "18446744073709551615", "\"1e3\""} {
		j, err := types.ParseBinaryJSONFromString(js)
		if err != nil {
			panic(err)
		}
		add(types.KindMysqlJSON, j.TypeCode, hex.EncodeToString(j.Value))
	}
	j := types.CreateBinaryJSON(types.Duration{Duration: 3723e9, Fsp: 2})
	add(types.KindMysqlJSON, j.TypeCode, hex.EncodeToString(j.Value))
	for _, v := range []string{"[]", "[1]", "[1,2,3]", "[1.5,-2,0]", "[0,0]"} {
		add(types.KindVectorFloat32, v)
	}
	add(types.KindRaw, "0102")

	// Field types.
	type ftT = []any
	var fts []ftT
	addFt := func(tp byte, flag uint, flen, dec int, cs, co string, elems []vec.GoString) {
		var e any
		if elems != nil {
			e = elems
		}
		fts = append(fts, ftT{tp, flag, flen, dec, cs, co, e})
	}
	un := uint(mysql.UnsignedFlag)
	for _, tp := range []byte{mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong} {
		addFt(tp, 0, -1, -1, "binary", "binary", nil)
		addFt(tp, un, -1, -1, "binary", "binary", nil)
	}
	addFt(mysql.TypeFloat, 0, -1, -1, "binary", "binary", nil)
	addFt(mysql.TypeFloat, 0, 5, 2, "binary", "binary", nil)
	addFt(mysql.TypeFloat, un, 10, 3, "binary", "binary", nil)
	addFt(mysql.TypeDouble, 0, -1, -1, "binary", "binary", nil)
	addFt(mysql.TypeDouble, 0, 6, 2, "binary", "binary", nil)
	addFt(mysql.TypeDouble, un, -1, -1, "binary", "binary", nil)
	addFt(mysql.TypeNewDecimal, 0, -1, -1, "binary", "binary", nil)
	addFt(mysql.TypeNewDecimal, 0, 5, 2, "binary", "binary", nil)
	addFt(mysql.TypeNewDecimal, 0, 3, 5, "binary", "binary", nil)
	addFt(mysql.TypeNewDecimal, un, 10, 0, "binary", "binary", nil)
	addFt(mysql.TypeNewDecimal, 0, 65, 30, "binary", "binary", nil)
	for _, c := range [][2]string{{"utf8mb4", "utf8mb4_bin"}, {"binary", "binary"}, {"utf8", "utf8_bin"},
		{"latin1", "latin1_bin"}, {"ascii", "ascii_bin"}, {"gbk", "gbk_bin"}} {
		addFt(mysql.TypeVarchar, 0, 5, -1, c[0], c[1], nil)
		addFt(mysql.TypeVarchar, 0, -1, -1, c[0], c[1], nil)
		addFt(mysql.TypeString, 0, 4, -1, c[0], c[1], nil)
		addFt(mysql.TypeBlob, 0, 10, -1, c[0], c[1], nil)
	}
	addFt(mysql.TypeString, uint(mysql.BinaryFlag), 6, -1, "binary", "binary", nil)
	addFt(mysql.TypeVarString, 0, 0, -1, "utf8mb4", "utf8mb4_bin", nil)
	addFt(mysql.TypeTinyBlob, 0, 3, -1, "utf8mb4", "utf8mb4_bin", nil)
	for _, tp := range []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp, mysql.TypeDuration} {
		addFt(tp, 0, -1, -1, "binary", "binary", nil)
		addFt(tp, 0, -1, 3, "binary", "binary", nil)
		addFt(tp, 0, -1, 6, "binary", "binary", nil)
	}
	addFt(mysql.TypeYear, 0, 4, 0, "binary", "binary", nil)
	addFt(mysql.TypeYear, un, 4, 0, "binary", "binary", nil)
	ab := []vec.GoString{"a", "b", "c", "x,y"}
	addFt(mysql.TypeEnum, 0, -1, -1, "utf8mb4", "utf8mb4_bin", ab)
	addFt(mysql.TypeEnum, 0, -1, -1, "utf8mb4", "utf8mb4_general_ci", []vec.GoString{"A", "b", "1", "ä"})
	addFt(mysql.TypeSet, 0, -1, -1, "utf8mb4", "utf8mb4_bin", []vec.GoString{"a", "b", "c"})
	addFt(mysql.TypeSet, 0, -1, -1, "utf8mb4", "utf8mb4_general_ci", []vec.GoString{"A", "b", "1"})
	for _, flen := range []int{1, 8, 10, 63, 64} {
		addFt(mysql.TypeBit, un, flen, 0, "binary", "binary", nil)
	}
	addFt(mysql.TypeJSON, 0, -1, -1, "utf8mb4", "utf8mb4_bin", nil)
	addFt(mysql.TypeTiDBVectorFloat32, 0, -1, -1, "binary", "binary", nil)
	addFt(mysql.TypeTiDBVectorFloat32, 0, 2, -1, "binary", "binary", nil)
	addFt(mysql.TypeNull, 0, -1, -1, "binary", "binary", nil)

	flags := datumFlags
	// d_convert: every datum to every field type is too many; each datum meets a
	// fixed subset plus random ones.
	for di, d := range pool {
		for fi, ft := range fts {
			if (di+fi)%5 == 0 || fi < 4 {
				g.Add("sys/convert", "d_convert", flags[(di+fi)%len(flags)], d, ft)
			}
		}
	}
	for i := 0; i < 6000; i++ {
		g.Add("rand", "d_convert", flags[rng.IntN(len(flags))], pool[rng.IntN(len(pool))], fts[rng.IntN(len(fts))])
	}
	// d_convert_recv: every pool datum to every field type, plus the signed integers that
	// convertToMysqlBit reinterprets (the seed-12 d_reverse record of the D01.1 part 2 review:
	// Int64 0 to BIT(32); tidb-rs#34: Int64 -1 to BIT(8) gives ErrDataTooLong, not ErrOverflow).
	for _, d := range pool {
		for _, ft := range fts {
			g.Add("sys/convert_recv", "d_convert_recv", flags[0], d, ft)
		}
	}
	for _, i := range []int64{0, 1, -1, 127, -128, 255, 256, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64} {
		for _, flen := range []int{1, 8, 32, 63, 64} {
			for fi := range flags[:2] {
				g.Add("sys/convert_recv_bit", "d_convert_recv", flags[fi], []any{types.KindInt64, i},
					[]any{mysql.TypeBit, uint(mysql.UnsignedFlag), flen, 0, "binary", "binary", nil})
			}
		}
	}
	for _, l := range lits {
		if l.str != nil {
			for fi := 0; fi < len(fts); fi += 3 {
				g.Add(l.fn, "d_convert", flags[1], []any{types.KindString, vec.GoString(*l.str), "utf8mb4_bin"}, fts[fi])
			}
		} else {
			for fi := 0; fi < len(fts); fi += 4 {
				g.Add(l.fn, "d_convert", flags[1], []any{types.KindInt64, *l.num}, fts[fi])
			}
		}
	}
	// d_compare.
	for i := 0; i < 7000; i++ {
		a, b := pool[rng.IntN(len(pool))], pool[rng.IntN(len(pool))]
		g.Add("rand", "d_compare", flags[rng.IntN(2)], a, b, colls[rng.IntN(len(colls))])
	}
	for i, a := range pool {
		g.Add("sys/self", "d_compare", flags[1], a, a, colls[i%len(colls)])
	}
	// d_to.
	fns := []string{"bool", "decimal", "int64", "float64", "string", "bytes", "hash_key", "json", "str", "trunc",
		"value_str", "mem", "marshal", "lit4cmp", "encoded", "clone"}
	for i, d := range pool {
		for _, fn := range fns {
			g.Add("sys/to", "d_to", flags[i%2], d, fn)
		}
		for _, cs := range []string{"utf8mb4", "utf8", "binary", "gbk", "latin1", "ascii"} {
			g.Add("sys/to", "d_to", flags[i%len(flags)], d, "decoded", cs)
			g.Add("sys/to", "d_to", flags[i%len(flags)], d, "checked", cs)
		}
	}
	// d_unmarshal: the marshaled pool plus mutated / bad texts.
	for _, d := range pool {
		dd := d2DatumIn(rawListOne(d))
		b, err := dd.MarshalJSON()
		if err == nil {
			g.Add("sys/roundtrip", "d_unmarshal", vec.GoString(b))
		}
	}
	for _, s := range []string{`{}`, `null`, `{"k":1,"i":5}`, `{"k":256}`, `{"k":-1}`, `{"k":"1"}`, `{"k":5,"b":"YWJj","collation":"utf8mb4_bin"}`,
		`{"k":5,"b":"!!"}`, `{"k":13,"time":0}`, `{"k":13,"time":"x"}`, `{"k":13}`, `{"k":8,"mydecimal":null}`,
		`{"k":8,"mydecimal":{"DigitsInt":1,"DigitsFrac":0,"ResultFrac":0,"Negative":false,"WordBuf":[5,0,0,0,0,0,0,0,0]}}`,
		`{"k":8,"mydecimal":{"DigitsInt":"x"}}`, `{"K":2,"I":7}`, `{"k":1,"i":1.5}`, `{"k":1,"decimal":70000}`,
		`{"k":1,"length":-1}`, `[1]`, `{"k":1,"i":9223372036854775808}`, `{"k":1,"extra":true,"i":3}`, `{"k":1`,
		`{"k":1,"i":2}x`, `{"k":5,"b":null}`, `{"k":5,"b":""}`, `{"k":1,"collation":5}`} {
		g.Add("sys/bad", "d_unmarshal", vec.GoString(s))
	}
	// d_list.
	for i := 0; i < 400; i++ {
		n := rng.IntN(6)
		var ds []any
		for k := 0; k < n; k++ {
			ds = append(ds, pool[rng.IntN(len(pool))])
		}
		g.Add("rand", "d_list", "to_string", flags[0], ds, rng.IntN(2) == 0)
		g.Add("rand", "d_list", "to_string_smart", flags[0], ds, rng.IntN(2) == 0)
		g.Add("rand", "d_list", "no_err", flags[0], ds)
		g.Add("rand", "d_list", "misc", flags[0], ds, rng.IntN(5))
	}
	// sort: lists of comparable datums (same kind family) and mixed lists.
	for i := 0; i < 400; i++ {
		n := 2 + rng.IntN(60)
		var ds []any
		base := rng.IntN(len(pool))
		for k := 0; k < n; k++ {
			if rng.IntN(3) == 0 {
				ds = append(ds, pool[rng.IntN(len(pool))])
			} else {
				ds = append(ds, pool[(base+rng.IntN(8))%len(pool)])
			}
		}
		g.Add("rand", "d_list", "sort", flags[rng.IntN(2)], ds)
	}
	// d_bound.
	for _, ft := range fts {
		g.Add("sys/bound", "d_bound", ft)
	}
	// d_plus.
	for i := 0; i < 1500; i++ {
		g.Add("rand", "d_plus", pool[rng.IntN(len(pool))], pool[rng.IntN(len(pool))])
	}
	for _, a := range [][]any{{types.KindInt64, int64(math.MaxInt64)}, {types.KindInt64, int64(math.MinInt64)},
		{types.KindUint64, uint64(math.MaxUint64)}, {types.KindInt64, int64(-1)}, {types.KindUint64, uint64(1)},
		{types.KindMysqlDecimal, "99999999999999999999999999999999999999999999999999999999999999999"}} {
		for _, b := range [][]any{{types.KindInt64, int64(1)}, {types.KindInt64, int64(-1)}, {types.KindUint64, uint64(1)},
			{types.KindInt64, int64(math.MinInt64)}, {types.KindMysqlDecimal, "1.5"}} {
			g.Add("sys/plus", "d_plus", a, b)
		}
	}
	// d_new.
	gvs := [][]any{{"nil"}, {"bool", true}, {"bool", false}, {"int", int64(-5)}, {"int64", int64(123456)},
		{"uint64", uint64(math.MaxUint64)}, {"float32", fBits(1.5)}, {"float64", fBits(-2.25)}, {"float64", fBits(1e21)},
		{"string", vec.GoString("abc")}, {"string", vec.GoString("")}, {"bytes", "616263"}, {"bytes", nil},
		{"decimal", "123.4500"}, {"decimal", "-0.001"}, {"duration", int64(3723e9), 2}, {"duration", int64(-1e9), 0},
		{"enum", uint64(2), vec.GoString("b")}, {"set", uint64(3), vec.GoString("a,b")}, {"binlit", "0102"},
		{"bitlit", "ff"}, {"hexlit", "616263"}, {"json", byte(types.JSONTypeCodeInt64), "0100000000000000"},
		{"vector", "[1,2]"}, {"slice", [][]any{{"int", int64(1)}, {"string", vec.GoString("x")}}}, {"slice", [][]any{}}}
	for _, s := range []string{"2011-11-10 11:11:11.999999", "2011-11-10"} {
		for _, tp := range []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp} {
			t, _ := types.ParseTime(ctx, s, tp, types.MaxFsp)
			gvs = append(gvs, []any{"time", rawOfTime(t)})
		}
	}
	for _, s := range litStrs {
		gvs = append(gvs, []any{"string", vec.GoString(s)})
	}
	for _, n := range litInts {
		gvs = append(gvs, []any{"int64", n})
	}
	for i, v := range gvs {
		g.Add("sys/new", "d_new", v, fts[(i*7)%len(fts)])
	}
	for i := 0; i < 300; i++ {
		g.Add("rand", "d_new", []any{"float64", fBits(math.Float64frombits(rng.Uint64()))}, fts[rng.IntN(len(fts))])
		g.Add("rand", "d_new", []any{"int64", int64(rng.Uint64()) >> rng.IntN(64)}, fts[rng.IntN(len(fts))])
	}
	for i := 0; i < 60; i++ {
		n := int64(rng.Uint64()) >> rng.IntN(64)
		if i < 8 {
			n = []int64{0, 1, -1, 7, 255, math.MaxInt64, math.MinInt64, 1 << 40}[i]
		}
		g.Add("sys/ctor", "d_ctor", n, vec.GoString(strs[i%len(strs)]), colls[i%len(colls)], fBits(float64(n)/3),
			[]uint64{0, uint64(mysql.UnsignedFlag)}[i%2])
	}
	// ft.
	for i := 0; i < 1500; i++ {
		n := 1 + rng.IntN(4)
		var l [][]any
		for k := 0; k < n; k++ {
			l = append(l, fts[rng.IntN(len(fts))])
		}
		g.Add("rand", "ft", "agg", l, uint(rng.Uint32()))
	}
	allTypes := []byte{mysql.TypeUnspecified, mysql.TypeTiny, mysql.TypeShort, mysql.TypeLong, mysql.TypeFloat,
		mysql.TypeDouble, mysql.TypeNull, mysql.TypeTimestamp, mysql.TypeLonglong, mysql.TypeInt24, mysql.TypeDate,
		mysql.TypeDuration, mysql.TypeDatetime, mysql.TypeYear, mysql.TypeNewDate, mysql.TypeVarchar, mysql.TypeBit,
		mysql.TypeJSON, mysql.TypeNewDecimal, mysql.TypeEnum, mysql.TypeSet, mysql.TypeTinyBlob, mysql.TypeMediumBlob,
		mysql.TypeLongBlob, mysql.TypeBlob, mysql.TypeVarString, mysql.TypeString, mysql.TypeGeometry,
		mysql.TypeTiDBVectorFloat32, 200}
	for _, a := range allTypes {
		g.Add("sys/ft", "ft", "new", a)
		for _, b := range allTypes {
			for _, ua := range []uint{0, un} {
				g.Add("sys/merge", "ft", "agg", [][]any{{a, ua, -1, -1, "binary", "binary", nil}, {b, un, 3, 2, "binary", "binary", nil}}, uint(0))
			}
			g.Add("sys/ft", "ft", "char_varchar", a, b)
		}
	}
	for _, c := range []string{"utf8mb4_bin", "utf8_general_ci", "binary", "latin1_bin", "gbk_chinese_ci", "nope"} {
		for _, tp := range []byte{mysql.TypeVarchar, mysql.TypeString, mysql.TypeBlob} {
			g.Add("sys/ft", "ft", "new_coll", tp, c, 10)
		}
	}
	for i := 0; i < 1200; i++ {
		g.Add("rand", "ft", "modify", fts[rng.IntN(len(fts))], fts[rng.IntN(len(fts))])
	}
	for _, a := range fts {
		g.Add("sys/modify", "ft", "modify", a, a)
	}
	for _, flen := range []int{-1, 0, 1, 16383, 16384, 21845, 21846, 65535, 65536} {
		for _, cs := range []string{"utf8mb4", "utf8", "latin1", "binary", "gbk", "ascii", "nope"} {
			g.Add("sys/ft", "ft", "varchar_big", flen, vec.GoString("c1"), cs)
		}
	}
	for _, fl := range []uint{0, 1, 32, 128, 0xffff} {
		for _, it := range []uint{1, 32, 128} {
			g.Add("sys/ft", "ft", "set_flag", fl, it, true)
			g.Add("sys/ft", "ft", "set_flag", fl, it, false)
		}
	}
	for i := 0; i < 60; i++ {
		g.Add("sys/builder", "ft_builder", allTypes[i%len(allTypes)], uint(rng.Uint32()&0xfff), uint(1<<rng.IntN(12)),
			uint(1<<rng.IntN(12)), rng.IntN(100)-1, rng.IntN(32)-1, "utf8mb4", "utf8mb4_bin")
	}
	for f := 0; f < 1<<10; f += 7 {
		g.Add("sys/context", "context", uint16(f), f%2 == 0, []string{"UTC", "Asia/Tokyo", "America/New_York"}[f%3])
	}
	for _, n := range [][]any{{"T", "C", "D", "T2", "C2", false}, {"", "", "", "", "c", false}, {"a", "b", "", "t", "c", false},
		{"A", "B", "Db", "", "Col", true}, {"", "", "db", "", "", false}, {"中", "文", "库", "表", "列", false}} {
		g.Add("sys/name", "field_name", n...)
	}
	var u8 []string
	for _, s := range strs {
		if utf8.ValidString(s) {
			u8 = append(u8, s)
		}
	}
	for i := 0; i < 50; i++ {
		g.Add("rand", "field_name", u8[i%len(u8)], u8[(i+1)%len(u8)], u8[(i+2)%len(u8)],
			u8[(i+3)%len(u8)], u8[(i+4)%len(u8)], i%7 == 0)
	}
}

func rawListOne(d []any) []json.RawMessage {
	out := make([]json.RawMessage, len(d))
	for i, x := range d {
		out[i] = rawArg(x)
	}
	return out
}

var _ = strconv.Itoa
