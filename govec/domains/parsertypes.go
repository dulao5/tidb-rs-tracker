package domains

// Domain "parser_types": block D02.3 (pkg/parser/model, pkg/parser/types,
// pkg/parser/auth/auth.go, pkg/parser/duration, and the two pkg/parser/ast pieces
// meta/model uses). Records:
//
//   - "ft": args [spec, strict]. spec is a FieldType built through its setters:
//     {"tp","flag","flen","decimal","charset","collate","elems"(GoString list or
//     null),"bin"(indexes of binary-literal elems),"array"}; strict sets
//     types.TiDBStrictIntegerDisplayWidth for the call. out: the read-only
//     methods (see ftOut).
//   - "ft_storage": args [spec]; out StorageLength() (may panic).
//   - "ft_unmarshal": args [json text (GoString)]; out the decoded FieldType's
//     fields and MarshalJSON(), or the error.
//   - "ft_unmarshal_dirty": args [spec, json text]: UnmarshalJSON into a non-zero
//     FieldType (an error must leave it unchanged).
//   - "ft_equal": args [a, b]; out Equal, PartialEqual(false/true), Equals.
//   - "ft_mutate": args [spec, steps]; steps are [name, int...] applied in order
//     (set_type, set_flag, add_flag, and_flag, toggle_flag, del_flag, set_flen,
//     set_decimal, set_flen_limit, set_decimal_limit, set_array, init,
//     set_elem, set_elem_bin, clean_bin, update_limit old_flen old_decimal old_tp
//     dd df); out the fields after the steps.
//   - "ft_copy": args [spec]; out MarshalJSON of DeepCopy, Clone, ArrayType.
//   - "ft_hash": args [spec]; out the IHasher calls of Hash64.
//   - "ft_mem": args [spec]; out MemoryUsage (elems built with len == cap).
//   - "type_str": args [tp, cs]; out TypeStr, TypeToStr, IsTypeBlob/Char/Vector.
//   - "str_to_type": args [s].
//   - "eval_type": args [et]; out String (may panic), IsStringKind, IsVectorKind.
//   - "cistr": args [s]; out NewCIStr, String, MemoryUsage, json.Marshal.
//   - "cistr_unmarshal": args [o, l, json text]: UnmarshalJSON into {o, l}.
//   - "model_str": args [type, v]: String() of a parser/model enum value.
//   - "priority": args [v]: PriorityValueToName.
//   - "user": args [UserIdentity fields]; out Restore, String, LoginString,
//     GetUserAndHostName. "user_nil": GetUserAndHostName(nil).
//   - "role": args [username, hostname]; out Restore, String.
//   - "duration": args [s]: duration.ParseDuration (int64 ns).
//   - "affinity": args [s]: ast.NormalizeTableAffinityLevel.

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/auth"
	"github.com/pingcap/tidb/pkg/parser/duration"
	"github.com/pingcap/tidb/pkg/parser/format"
	"github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/types"
)

const parserTypesSeed = 20261120

func init() {
	vec.Register(&vec.Domain{
		Name:     "parser_types",
		Seed:     parserTypesSeed,
		Coverage: ptCoverage,
		Blocks:   []string{"D02.3"},
		Exempt: map[string]string{
			"FieldType.FormatAsCastType": "writes RestoreAsCastType's output to an io.Writer; covered through ft.cast",
			"FieldType.GetElem":          "a plain index; covered through ft.elems",
		},
		Gzip:     true,
		Generate: ptGenerate,
		Exec:     ptExec,
	})
}

var ptCoverage = map[string]vec.Coverage{
	"ft": {Min: 3000, GoFuncs: []string{"NewFieldType", "FieldType.SetType", "FieldType.SetFlag",
		"FieldType.SetFlen", "FieldType.SetDecimal", "FieldType.SetCharset", "FieldType.SetCollate",
		"FieldType.SetElems", "FieldType.SetElemWithIsBinaryLit", "FieldType.SetArray",
		"FieldType.CompactStr", "FieldType.InfoSchemaStr", "FieldType.String", "FieldType.Restore",
		"FieldType.RestoreAsCastType", "FieldType.EvalType", "FieldType.Hybrid", "FieldType.IsVarLengthType",
		"FieldType.IsDecimalValid", "HasCharset", "FieldType.MarshalJSON", "FieldType.GetType",
		"FieldType.GetFlag", "FieldType.GetFlen", "FieldType.GetDecimal", "FieldType.GetCharset",
		"FieldType.GetCollate", "FieldType.GetElems", "FieldType.IsArray", "FieldType.GetElemIsBinaryLit"}},
	"ft_storage":         {Min: 500, GoFuncs: []string{"FieldType.StorageLength"}},
	"ft_unmarshal":       {Min: 300, GoFuncs: []string{"FieldType.UnmarshalJSON"}},
	"ft_unmarshal_dirty": {Min: 50, GoFuncs: []string{"FieldType.UnmarshalJSON"}},
	"ft_equal":           {Min: 1000, GoFuncs: []string{"FieldType.Equal", "FieldType.PartialEqual", "FieldType.Equals"}},
	"ft_mutate": {Min: 500, GoFuncs: []string{"FieldType.AddFlag", "FieldType.AndFlag", "FieldType.ToggleFlag",
		"FieldType.DelFlag", "FieldType.SetFlenUnderLimit", "FieldType.SetDecimalUnderLimit", "FieldType.Init",
		"FieldType.SetElem", "FieldType.CleanElemIsBinaryLit", "FieldType.UpdateFlenAndDecimalUnderLimit"}},
	"ft_copy":         {Min: 200, GoFuncs: []string{"FieldType.DeepCopy", "FieldType.Clone", "FieldType.ArrayType"}},
	"ft_hash":         {Min: 100, GoFuncs: []string{"FieldType.Hash64"}},
	"ft_mem":          {Min: 100, GoFuncs: []string{"FieldType.MemoryUsage"}},
	"type_str":        {Min: 256, GoFuncs: []string{"TypeStr", "TypeToStr", "IsTypeBlob", "IsTypeChar", "IsTypeVector"}},
	"str_to_type":     {Min: 40, GoFuncs: []string{"StrToType"}},
	"eval_type":       {Min: 10, GoFuncs: []string{"EvalType.String", "EvalType.IsStringKind", "EvalType.IsVectorKind"}},
	"cistr":           {Min: 50, GoFuncs: []string{"NewCIStr", "CIStr.String", "CIStr.MemoryUsage"}},
	"cistr_unmarshal": {Min: 50, GoFuncs: []string{"CIStr.UnmarshalJSON"}},
	"model_str": {Min: 50, GoFuncs: []string{"TableLockType.String", "ViewAlgorithm.String", "ViewSecurity.String",
		"ViewCheckOption.String", "PartitionType.String", "PrimaryKeyType.String", "IndexType.String",
		"ReferOptionType.String", "RunawayWatchType.String", "RunawayActionType.String", "ColumnChoice.String"}},
	"priority": {Min: 5, GoFuncs: []string{"PriorityValueToName"}},
	"user":     {Min: 30, GoFuncs: []string{"UserIdentity.Restore", "UserIdentity.String", "UserIdentity.LoginString", "GetUserAndHostName"}},
	"user_nil": {Min: 1, GoFuncs: []string{"GetUserAndHostName"}},
	"role":     {Min: 10, GoFuncs: []string{"RoleIdentity.Restore", "RoleIdentity.String"}},
	"duration": {Min: 100, GoFuncs: []string{"ParseDuration", "readFloat"}},
	"affinity": {Min: 10, GoFuncs: []string{"NormalizeTableAffinityLevel"}},
}

// ftSpec is a FieldType built through its setters.
type ftSpec struct {
	Tp      byte           `json:"tp"`
	Flag    uint           `json:"flag"`
	Flen    int            `json:"flen"`
	Decimal int            `json:"decimal"`
	Charset string         `json:"charset"`
	Collate string         `json:"collate"`
	Elems   []vec.GoString `json:"elems"`
	Bin     []int          `json:"bin,omitempty"`
	Array   bool           `json:"array"`
}

func (s ftSpec) build() *types.FieldType {
	ft := types.NewFieldType(s.Tp)
	ft.SetFlag(s.Flag)
	ft.SetFlen(s.Flen)
	ft.SetDecimal(s.Decimal)
	ft.SetCharset(s.Charset)
	ft.SetCollate(s.Collate)
	if s.Elems != nil {
		es := make([]string, len(s.Elems))
		for i, e := range s.Elems {
			es[i] = string(e)
		}
		ft.SetElems(es)
		for _, i := range s.Bin {
			ft.SetElemWithIsBinaryLit(i, es[i], true)
		}
	}
	ft.SetArray(s.Array)
	return ft
}

func gsList(es []string) []vec.GoString {
	if es == nil {
		return nil
	}
	out := make([]vec.GoString, len(es))
	for i, e := range es {
		out[i] = vec.GoString(e)
	}
	return out
}

// ftFields is the state of a FieldType through its getters.
func ftFields(ft *types.FieldType) map[string]any {
	bins := make([]bool, len(ft.GetElems()))
	for i := range bins {
		bins[i] = ft.GetElemIsBinaryLit(i)
	}
	return map[string]any{
		"tp": ft.GetType(), "flag": ft.GetFlag(), "flen": ft.GetFlen(), "decimal": ft.GetDecimal(),
		"charset": ft.GetCharset(), "collate": ft.GetCollate(), "elems": gsList(ft.GetElems()),
		"bin": bins, "array": ft.IsArray(), "json": vec.GoString(mustMarshal(ft)),
	}
}

func mustMarshal(ft *types.FieldType) string {
	b, err := ft.MarshalJSON()
	if err != nil {
		panic(err)
	}
	return string(b)
}

func restoreStr(f func(ctx *format.RestoreCtx)) vec.GoString {
	var sb strings.Builder
	f(format.NewRestoreCtx(format.DefaultRestoreFlags, &sb))
	return vec.GoString(sb.String())
}

func ftOut(ft *types.FieldType) map[string]any {
	out := ftFields(ft)
	out["compact"] = vec.GoString(ft.CompactStr())
	out["info"] = vec.GoString(ft.InfoSchemaStr())
	out["string"] = vec.GoString(ft.String())
	out["restore"] = restoreStr(func(ctx *format.RestoreCtx) {
		if err := ft.Restore(ctx); err != nil {
			panic(err)
		}
	})
	out["cast"] = restoreStr(func(ctx *format.RestoreCtx) { ft.RestoreAsCastType(ctx, false) })
	out["cast_cs"] = restoreStr(func(ctx *format.RestoreCtx) { ft.RestoreAsCastType(ctx, true) })
	out["eval"] = uint8(ft.EvalType())
	out["hybrid"] = ft.Hybrid()
	out["varlen"] = ft.IsVarLengthType()
	out["dec_valid"] = ft.IsDecimalValid()
	out["has_cs"] = types.HasCharset(ft)
	return out
}

type recHasher struct{ calls []string }

func (h *recHasher) HashBool(v bool)       { h.calls = append(h.calls, fmt.Sprintf("bool:%v", v)) }
func (h *recHasher) HashInt(v int)         { h.calls = append(h.calls, fmt.Sprintf("int:%d", v)) }
func (h *recHasher) HashInt64(v int64)     { h.calls = append(h.calls, fmt.Sprintf("int64:%d", v)) }
func (h *recHasher) HashUint64(v uint64)   { h.calls = append(h.calls, fmt.Sprintf("uint64:%d", v)) }
func (h *recHasher) HashFloat64(v float64) { h.calls = append(h.calls, fmt.Sprintf("float64:%v", v)) }
func (h *recHasher) HashRune(v rune)       { h.calls = append(h.calls, fmt.Sprintf("rune:%d", v)) }
func (h *recHasher) HashString(v string)   { h.calls = append(h.calls, "string:"+fmt.Sprintf("%x", v)) }
func (h *recHasher) HashByte(v byte)       { h.calls = append(h.calls, fmt.Sprintf("byte:%d", v)) }
func (h *recHasher) HashBytes(v []byte)    { h.calls = append(h.calls, fmt.Sprintf("bytes:%x", v)) }
func (h *recHasher) Reset()                { h.calls = nil }
func (h *recHasher) Sum64() uint64         { return 0 }

func withStrict(strict bool, f func()) {
	old := types.TiDBStrictIntegerDisplayWidth
	types.TiDBStrictIntegerDisplayWidth = strict
	defer func() { types.TiDBStrictIntegerDisplayWidth = old }()
	f()
}

func modelStr(kind string, v int64) string {
	switch kind {
	case "TableLockType":
		return model.TableLockType(v).String()
	case "ViewAlgorithm":
		x := model.ViewAlgorithm(v)
		return x.String()
	case "ViewSecurity":
		x := model.ViewSecurity(v)
		return x.String()
	case "ViewCheckOption":
		x := model.ViewCheckOption(v)
		return x.String()
	case "PartitionType":
		return model.PartitionType(v).String()
	case "PrimaryKeyType":
		return model.PrimaryKeyType(v).String()
	case "IndexType":
		return model.IndexType(v).String()
	case "ReferOptionType":
		return model.ReferOptionType(v).String()
	case "RunawayWatchType":
		return model.RunawayWatchType(v).String()
	case "RunawayActionType":
		return model.RunawayActionType(v).String()
	case "ColumnChoice":
		return model.ColumnChoice(v).String()
	}
	panic("unknown model enum " + kind)
}

func ptExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "ft":
		var out map[string]any
		withStrict(arg[bool](args, 1), func() { out = ftOut(arg[ftSpec](args, 0).build()) })
		return out, nil
	case "ft_storage":
		return arg[ftSpec](args, 0).build().StorageLength(), nil
	case "ft_unmarshal":
		var ft types.FieldType
		if err := ft.UnmarshalJSON([]byte(arg[vec.GoString](args, 0))); err != nil {
			return nil, err
		}
		return ftFields(&ft), nil
	case "ft_unmarshal_dirty":
		ft := arg[ftSpec](args, 0).build()
		err := ft.UnmarshalJSON([]byte(arg[vec.GoString](args, 1)))
		return ftFields(ft), err
	case "ft_equal":
		a, b := arg[ftSpec](args, 0).build(), arg[ftSpec](args, 1).build()
		return map[string]any{"equal": a.Equal(b), "partial": a.PartialEqual(b, false),
			"partial_unsafe": a.PartialEqual(b, true), "equals": a.Equals(b), "equals_val": a.Equals(*b),
			"equals_nil": a.Equals(nil)}, nil
	case "ft_mutate":
		ft := arg[ftSpec](args, 0).build()
		steps := arg[[][]json.RawMessage](args, 1)
		for _, st := range steps {
			name := arg[string](st, 0)
			n := func(i int) int { return arg[int](st, i) }
			switch name {
			case "set_type":
				ft.SetType(byte(n(1)))
			case "set_flag":
				ft.SetFlag(uint(n(1)))
			case "add_flag":
				ft.AddFlag(uint(n(1)))
			case "and_flag":
				ft.AndFlag(uint(n(1)))
			case "toggle_flag":
				ft.ToggleFlag(uint(n(1)))
			case "del_flag":
				ft.DelFlag(uint(n(1)))
			case "set_flen":
				ft.SetFlen(n(1))
			case "set_decimal":
				ft.SetDecimal(n(1))
			case "set_flen_limit":
				ft.SetFlenUnderLimit(n(1))
			case "set_decimal_limit":
				ft.SetDecimalUnderLimit(n(1))
			case "set_array":
				ft.SetArray(n(1) != 0)
			case "init":
				ft.Init(byte(n(1)))
			case "set_elem":
				ft.SetElem(n(1), string(arg[vec.GoString](st, 2)))
			case "set_elem_bin":
				ft.SetElemWithIsBinaryLit(n(1), string(arg[vec.GoString](st, 2)), arg[bool](st, 3))
			case "clean_bin":
				ft.CleanElemIsBinaryLit()
			case "update_limit":
				old := types.NewFieldType(byte(n(3)))
				old.SetFlen(n(1))
				old.SetDecimal(n(2))
				ft.UpdateFlenAndDecimalUnderLimit(old, n(4), n(5))
			default:
				panic("unknown step " + name)
			}
		}
		return ftFields(ft), nil
	case "ft_copy":
		ft := arg[ftSpec](args, 0).build()
		dc := ft.DeepCopy()
		cl := ft.Clone()
		at := ft.ArrayType()
		return map[string]any{"deep": ftFields(dc), "clone": ftFields(cl), "array_type": ftFields(at)}, nil
	case "ft_hash":
		h := &recHasher{}
		arg[ftSpec](args, 0).build().Hash64(h)
		return h.calls, nil
	case "ft_mem":
		return arg[ftSpec](args, 0).build().MemoryUsage(), nil
	case "type_str":
		tp := arg[byte](args, 0)
		return map[string]any{"str": types.TypeStr(tp), "to_str": types.TypeToStr(tp, arg[string](args, 1)),
			"blob": types.IsTypeBlob(tp), "char": types.IsTypeChar(tp), "vector": types.IsTypeVector(tp)}, nil
	case "str_to_type":
		return types.StrToType(arg[string](args, 0)), nil
	case "eval_type":
		et := types.EvalType(arg[byte](args, 0))
		out := map[string]any{"string_kind": et.IsStringKind(), "vector_kind": et.IsVectorKind()}
		out["string"] = et.String()
		return out, nil
	case "cistr":
		c := model.NewCIStr(arg[string](args, 0))
		b, err := json.Marshal(c)
		if err != nil {
			panic(err)
		}
		return map[string]any{"o": c.O, "l": c.L, "string": c.String(), "mem": c.MemoryUsage(), "json": string(b)}, nil
	case "cistr_unmarshal":
		c := model.CIStr{O: arg[string](args, 0), L: arg[string](args, 1)}
		err := c.UnmarshalJSON([]byte(arg[vec.GoString](args, 2)))
		return map[string]any{"o": c.O, "l": c.L}, err
	case "model_str":
		return modelStr(arg[string](args, 0), arg[int64](args, 1)), nil
	case "priority":
		return model.PriorityValueToName(arg[uint64](args, 0)), nil
	case "user":
		u := arg[auth.UserIdentity](args, 0)
		un, hn := auth.GetUserAndHostName(&u)
		return map[string]any{"restore": restoreStr(func(ctx *format.RestoreCtx) { _ = u.Restore(ctx) }),
			"string": u.String(), "login": u.LoginString(), "get": []string{un, hn}}, nil
	case "user_nil":
		var u *auth.UserIdentity
		un, hn := auth.GetUserAndHostName(u)
		return []string{un, hn, u.String(), u.LoginString()}, nil
	case "role":
		r := auth.RoleIdentity{Username: arg[string](args, 0), Hostname: arg[string](args, 1)}
		return map[string]any{"restore": restoreStr(func(ctx *format.RestoreCtx) { _ = r.Restore(ctx) }),
			"string": r.String()}, nil
	case "duration":
		d, err := duration.ParseDuration(arg[string](args, 0))
		return int64(d), err
	case "affinity":
		s, ok := ast.NormalizeTableAffinityLevel(arg[string](args, 0))
		return []any{s, ok}, nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

var ptAllTypes = []byte{
	mysql.TypeUnspecified, mysql.TypeTiny, mysql.TypeShort, mysql.TypeLong, mysql.TypeFloat,
	mysql.TypeDouble, mysql.TypeNull, mysql.TypeTimestamp, mysql.TypeLonglong, mysql.TypeInt24,
	mysql.TypeDate, mysql.TypeDuration, mysql.TypeDatetime, mysql.TypeYear, mysql.TypeNewDate,
	mysql.TypeVarchar, mysql.TypeBit, mysql.TypeTiDBVectorFloat32, mysql.TypeJSON,
	mysql.TypeNewDecimal, mysql.TypeEnum, mysql.TypeSet, mysql.TypeTinyBlob, mysql.TypeMediumBlob,
	mysql.TypeLongBlob, mysql.TypeBlob, mysql.TypeVarString, mysql.TypeString, mysql.TypeGeometry,
}

var (
	ptFlags = []uint{0, mysql.UnsignedFlag, mysql.ZerofillFlag, mysql.BinaryFlag, mysql.EnumSetAsIntFlag,
		mysql.UnsignedFlag | mysql.ZerofillFlag, mysql.UnsignedFlag | mysql.BinaryFlag,
		mysql.NotNullFlag | mysql.PriKeyFlag, mysql.ZerofillFlag | mysql.BinaryFlag | mysql.UnsignedFlag}
	ptFlens    = []int{-1, 0, 1, 4, 10, 11, 20, 64, 65, 66, 255, 4194304}
	ptDecimals = []int{-1, 0, 1, 2, 5, 6, 7, 30, 31}
	ptCharsets = [][2]string{{"", ""}, {"binary", "binary"}, {"utf8mb4", "utf8mb4_bin"}, {"utf8mb4", "binary"},
		{"latin1", "latin1_bin"}, {"binary", ""}, {"", "utf8mb4_general_ci"}, {"gbk", "gbk_chinese_ci"}}
	ptElems = [][]vec.GoString{nil, {}, {"a"}, {"a", "b", "c"}, {"", "x'y", "a\nb", "c\rd", "e\x00f"},
		{"\xff\xfe", "ok", "\xe2\x82"}, {"日本", "Ω", "`q`"}}
)

func ptRandSpec(rng *rand.Rand) ftSpec {
	s := ftSpec{
		Tp:      ptAllTypes[rng.IntN(len(ptAllTypes))],
		Flag:    ptFlags[rng.IntN(len(ptFlags))],
		Flen:    ptFlens[rng.IntN(len(ptFlens))],
		Decimal: ptDecimals[rng.IntN(len(ptDecimals))],
		Array:   rng.IntN(8) == 0,
	}
	cs := ptCharsets[rng.IntN(len(ptCharsets))]
	s.Charset, s.Collate = cs[0], cs[1]
	if s.Tp == mysql.TypeEnum || s.Tp == mysql.TypeSet || rng.IntN(6) == 0 {
		s.Elems = ptElems[rng.IntN(len(ptElems))]
		if len(s.Elems) > 0 && rng.IntN(3) == 0 {
			s.Bin = []int{rng.IntN(len(s.Elems))}
		}
	}
	return s
}

var ptJSONs = []string{
	`{"Tp":3,"Flag":0,"Flen":11,"Decimal":0,"Charset":"binary","Collate":"binary","Elems":null,"ElemsIsBinaryLit":null,"Array":false}`,
	`{"Tp":254,"Flag":4099,"Flen":255,"Decimal":0,"Charset":"utf8mb4","Collate":"utf8mb4_bin","Elems":["a","b"],"ElemsIsBinaryLit":[false,true],"Array":true}`,
	`{}`, `null`, `[]`, `1`, `"x"`, `true`, `{"Tp":256}`, `{"Tp":-1}`, `{"Tp":1.5}`, `{"Tp":"1"}`,
	`{"Flag":-1}`, `{"Flag":18446744073709551615}`, `{"Flag":18446744073709551616}`,
	`{"Flen":9223372036854775807}`, `{"Flen":9223372036854775808}`, `{"Flen":-9223372036854775808}`,
	`{"Flen":1e2}`, `{"Flen":-0}`, `{"Flen":true}`, `{"Flen":[]}`, `{"Flen":{}}`, `{"Flen":null}`,
	`{"tp":5,"flen":3,"CHARSET":"x","collate":"y"}`, `{"Tp":1,"tp":2,"TP":3}`, `{"Elems":null}`, `{"Elems":[]}`,
	`{"Elems":"a"}`, `{"Elems":[1,"b",null,true]}`, `{"Elems":["a","b"],"Elems":["c"]}`,
	`{"ElemsIsBinaryLit":[true,1,false]}`, `{"Array":1}`, `{"Array":null}`, `{"Array":"true"}`,
	`{"Charset":1,"Collate":"ok"}`, `{"Unknown":1,"Tp":7}`, `{"Elemſ":["k"]}`, `{"Flaſ":1}`,
	`{"Tp":7,"Flen":"bad","Decimal":true,"Charset":"c"}`, ` { "Tp" : 1 } `, `{"Tp":1,}`, `{"Tp":1`,
	`{"Charset":"é\ud800A"}`, "{\"Charset\":\"\xff\"}", `{"Elems":["<>& "]}`,
	`{"Elems":["�"]}`, `{"Tp":0e0}`, `{"Tp":00}`, ``, `{"Tp":1} x`,
}

func ptGenerate(g *vec.Gen, rng *rand.Rand) {
	// Cases of the Go tests (field_type_test.go TestFieldType, model_test.go, duration_test.go, etc_test.go).
	for _, s := range []ftSpec{
		{Tp: mysql.TypeDuration, Flen: -1, Decimal: 5},
		{Tp: mysql.TypeLong, Flen: 5, Decimal: -1, Flag: mysql.UnsignedFlag | mysql.ZerofillFlag},
		{Tp: mysql.TypeFloat, Flen: 12, Decimal: 3}, {Tp: mysql.TypeFloat, Flen: 12, Decimal: -1},
		{Tp: mysql.TypeFloat, Flen: 5, Decimal: -1}, {Tp: mysql.TypeFloat, Flen: 7, Decimal: 3},
		{Tp: mysql.TypeDouble, Flen: 22, Decimal: 3}, {Tp: mysql.TypeDouble, Flen: 22, Decimal: -1},
		{Tp: mysql.TypeDouble, Flen: 5, Decimal: -1}, {Tp: mysql.TypeDouble, Flen: 7, Decimal: 3},
		{Tp: mysql.TypeBlob, Flen: 10, Decimal: -1, Charset: "UTF8", Collate: "UTF8_UNICODE_GI"},
		{Tp: mysql.TypeVarchar, Flen: 10, Decimal: -1, Flag: mysql.BinaryFlag},
		{Tp: mysql.TypeString, Flen: -1, Decimal: -1, Charset: "binary"},
		{Tp: mysql.TypeEnum, Flen: -1, Decimal: -1, Elems: []vec.GoString{"a", "b"}},
		{Tp: mysql.TypeEnum, Flen: -1, Decimal: -1, Elems: []vec.GoString{"'a'", "'b'"}},
		{Tp: mysql.TypeEnum, Flen: -1, Decimal: -1, Elems: []vec.GoString{"a\nb", "a\tb", "a\rb"}},
		{Tp: mysql.TypeEnum, Flen: -1, Decimal: -1, Elems: []vec.GoString{"a\nb", "a'\t\r\nb", "a\rb"}},
		{Tp: mysql.TypeSet, Flen: -1, Decimal: -1, Elems: []vec.GoString{"a", "b"}},
		{Tp: mysql.TypeSet, Flen: -1, Decimal: -1, Elems: []vec.GoString{"'a'", "'b'"}},
		{Tp: mysql.TypeTimestamp, Flen: -1, Decimal: 2}, {Tp: mysql.TypeNewDecimal, Flen: -1, Decimal: -1},
		{Tp: mysql.TypeNull, Flen: -1, Decimal: -1}, {Tp: mysql.TypeBit, Flen: 1, Decimal: -1},
	} {
		g.Add("TestFieldType", "ft", s, false)
	}
	for _, j := range []string{`"aaBB"`, `{"O":"aaBB","L":"aabb"}`} {
		g.Add("TestUnmarshalCIStr", "cistr_unmarshal", "", "", vec.GoString(j))
	}
	for _, d := range []string{"1h", "1h100m", "1d10000m", "1d100h", "1.5d", "1d1.5h", "1d3.555h"} {
		g.Add("TestParseDuration", "duration", d)
	}
	for _, s := range []string{"blob", "binary"} {
		g.Add("TestStrToType", "str_to_type", s)
	}
	// Structured: every type with each flen, decimal, flag and charset (default others).
	for _, tp := range ptAllTypes {
		for _, flen := range ptFlens {
			for _, dec := range ptDecimals {
				s := ftSpec{Tp: tp, Flen: flen, Decimal: dec}
				g.Add("sys/flen_dec", "ft", s, false)
				g.Add("sys/flen_dec", "ft_storage", s)
			}
		}
		for _, fl := range ptFlags {
			for _, strict := range []bool{false, true} {
				g.Add("sys/flags", "ft", ftSpec{Tp: tp, Flag: fl, Flen: 1, Decimal: -1}, strict)
				g.Add("sys/flags", "ft", ftSpec{Tp: tp, Flag: fl, Flen: -1, Decimal: -1}, strict)
			}
		}
		for _, cs := range ptCharsets {
			g.Add("sys/charset", "ft", ftSpec{Tp: tp, Flen: 10, Decimal: -1, Charset: cs[0], Collate: cs[1]}, false)
		}
		for _, el := range ptElems {
			s := ftSpec{Tp: tp, Flen: -1, Decimal: -1, Elems: el}
			if len(el) > 1 {
				s.Bin = []int{1}
			}
			g.Add("sys/elems", "ft", s, false)
			g.Add("sys/elems", "ft_copy", s)
			g.Add("sys/elems", "ft_hash", s)
			g.Add("sys/elems", "ft_mem", s)
		}
		g.Add("sys/array", "ft", ftSpec{Tp: tp, Flen: -1, Decimal: -1, Array: true}, false)
		g.Add("sys/array", "ft_copy", ftSpec{Tp: tp, Flen: -1, Decimal: -1, Array: true})
	}
	// Random FieldTypes.
	for i := 0; i < 3000; i++ {
		g.Add("rand/ft", "ft", ptRandSpec(rng), rng.IntN(3) == 0)
	}
	for i := 0; i < 2000; i++ {
		a := ptRandSpec(rng)
		b := a
		switch rng.IntN(6) {
		case 0:
			b = ptRandSpec(rng)
		case 1:
			b.Flen = ptFlens[rng.IntN(len(ptFlens))]
		case 2:
			b.Decimal = ptDecimals[rng.IntN(len(ptDecimals))]
		case 3:
			b.Flag ^= mysql.UnsignedFlag
		case 4:
			b.Tp = ptAllTypes[rng.IntN(len(ptAllTypes))]
		case 5:
			b.Elems = ptElems[rng.IntN(len(ptElems))]
			b.Bin = nil
		}
		g.Add("rand/equal", "ft_equal", a, b)
	}
	for i := 0; i < 200; i++ {
		s := ptRandSpec(rng)
		g.Add("rand/copy", "ft_copy", s)
		g.Add("rand/hash", "ft_hash", s)
		g.Add("rand/mem", "ft_mem", s)
		g.Add("rand/storage", "ft_storage", s)
	}
	// Mutations.
	stepNames := []string{"set_type", "set_flag", "add_flag", "and_flag", "toggle_flag", "del_flag",
		"set_flen", "set_decimal", "set_flen_limit", "set_decimal_limit", "set_array", "init", "clean_bin",
		"update_limit", "set_elem", "set_elem_bin"}
	ints := []int{-1, 0, 1, 6, 30, 31, 64, 65, 66, 100, 1 << 20}
	for i := 0; i < 800; i++ {
		s := ptRandSpec(rng)
		var steps [][]any
		for k := 0; k < 1+rng.IntN(4); k++ {
			name := stepNames[rng.IntN(len(stepNames))]
			pick := func() int { return ints[rng.IntN(len(ints))] }
			switch name {
			case "set_type", "init":
				steps = append(steps, []any{name, int(ptAllTypes[rng.IntN(len(ptAllTypes))])})
			case "set_flag", "add_flag", "and_flag", "toggle_flag", "del_flag":
				steps = append(steps, []any{name, int(ptFlags[rng.IntN(len(ptFlags))])})
			case "set_array":
				steps = append(steps, []any{name, rng.IntN(2)})
			case "clean_bin":
				steps = append(steps, []any{name})
			case "update_limit":
				steps = append(steps, []any{name, pick(), pick(), int(ptAllTypes[rng.IntN(len(ptAllTypes))]), pick(), pick()})
			case "set_elem", "set_elem_bin":
				if len(s.Elems) == 0 {
					continue
				}
				steps = append(steps, []any{name, rng.IntN(len(s.Elems)), vec.GoString("e\xff"), rng.IntN(2) == 0})
			default:
				steps = append(steps, []any{name, pick()})
			}
		}
		g.Add("rand/mutate", "ft_mutate", s, steps)
	}
	// Decimal limits on a decimal column.
	for _, of := range ints {
		for _, od := range ints {
			for _, dd := range []int{0, 1, 4} {
				for _, df := range []int{0, 1, 10} {
					s := ftSpec{Tp: mysql.TypeNewDecimal, Flen: 10, Decimal: 2}
					g.Add("sys/update_limit", "ft_mutate", s, [][]any{{"update_limit", of, od, int(mysql.TypeNewDecimal), dd, df}})
				}
			}
		}
	}
	// JSON.
	for _, j := range ptJSONs {
		g.Add("sys/json", "ft_unmarshal", vec.GoString(j))
		g.Add("sys/json", "ft_unmarshal_dirty", ftSpec{Tp: 15, Flag: 1, Flen: 5, Decimal: 1, Charset: "c", Collate: "d", Elems: []vec.GoString{"z"}}, vec.GoString(j))
	}
	for i := 0; i < 300; i++ {
		s := ptRandSpec(rng)
		j := mustMarshal(s.build())
		g.Add("rand/json_roundtrip", "ft_unmarshal", vec.GoString(j))
		// Corrupt one byte.
		b := []byte(j)
		const junk = "{}[]\",:01-ae\\ "
		b[rng.IntN(len(b))] = junk[rng.IntN(len(junk))]
		g.Add("rand/json_corrupt", "ft_unmarshal", vec.GoString(b))
	}
	// etc / eval types.
	for tp := 0; tp < 256; tp++ {
		for _, cs := range []string{"", "binary", "utf8mb4"} {
			g.Add("sys/type_str", "type_str", byte(tp), cs)
		}
	}
	for _, s := range []string{"bit", "text", "blob", "binary", "varbinary", "char", "varchar", "tinyblob",
		"mediumblob", "longblob", "tinytext", "int", "bigint", "decimal", "json", "vector", "geometry", "set",
		"enum", "year", "null", "unspecified", "var_string", "var_binary", "", "INT", "binaryblob", "blobblob",
		"time", "timestamp", "date", "datetime", "double", "float", "smallint", "mediumint", "tinyint",
		"longtext", "mediumtext", "x", "text ", "binarybinary", "charchar", "blobbinary", "bitbit"} {
		g.Add("sys/str_to_type", "str_to_type", s)
	}
	for et := 0; et < 12; et++ {
		g.Add("sys/eval_type", "eval_type", byte(et))
	}
	// model.
	for _, s := range []string{"", "a", "ABC", "MiXeD", "Ǆ", "İstanbul", "ΣΑΣ", "ß", "ﬃ", "K", "日本語", "x y", "K",
		"tAbLe_1", "`q`", "\"", "<&>"} {
		g.Add("sys/cistr", "cistr", s)
	}
	for i := 0; i < 40; i++ {
		var sb strings.Builder
		for k := 0; k < rng.IntN(8); k++ {
			rs := []rune("aZ_9ÄöΣςİıǅﬀ€😀")
			sb.WriteRune(rs[rng.IntN(len(rs))])
		}
		g.Add("rand/cistr", "cistr", sb.String())
	}
	for _, j := range []string{`{"O":"Abc","L":"abc"}`, `{"O":"Abc"}`, `{"L":"x"}`, `"Name"`, `"MiXeD"`, `""`,
		`null`, `1`, `true`, `[]`, `{"O":1}`, `{"O":1,"L":"keep"}`, `{"o":"low","l":"lower"}`, `{"O":"a","O":"b"}`,
		`{"O":null,"L":null}`, `{"X":1}`, `{}`, `"ÉtÉ"`, `{"O":"a"`, `"unterminated`, ``,
		`{"O":"a","L":5}`, `["a"]`, `{"O":["a"]}`, "\"\xff\xfe\"", `{"Ο":"greek omicron"}`} {
		for _, init := range [][2]string{{"", ""}, {"Old", "old"}} {
			g.Add("sys/cistr_unmarshal", "cistr_unmarshal", init[0], init[1], vec.GoString(j))
		}
	}
	for _, k := range []string{"TableLockType", "ViewAlgorithm", "ViewSecurity", "ViewCheckOption", "PartitionType",
		"PrimaryKeyType", "IndexType", "ReferOptionType", "RunawayWatchType", "RunawayActionType", "ColumnChoice"} {
		for v := int64(-1); v <= 7; v++ {
			g.Add("sys/model_str", "model_str", k, v)
		}
	}
	for _, v := range []uint64{0, 1, 2, 8, 16, 17, 1 << 63} {
		g.Add("sys/priority", "priority", v)
	}
	// auth.
	users := []auth.UserIdentity{{}, {Username: "root", Hostname: "%"}, {Username: "a`b", Hostname: "h`"},
		{CurrentUser: true}, {Username: "u", Hostname: "h", AuthUsername: "au", AuthHostname: "ah"},
		{Username: "u", Hostname: "h", AuthUsername: "au"}, {Username: "u", Hostname: "h", AuthHostname: "ah"},
		{Username: "日本", Hostname: "localhost", AuthPlugin: "mysql_native_password"}}
	for _, u := range users {
		g.Add("sys/user", "user", u)
	}
	for i := 0; i < 30; i++ {
		parts := []string{"", "u", "root", "a`", "%", "127.0.0.1", "x@y"}
		p := func() string { return parts[rng.IntN(len(parts))] }
		g.Add("rand/user", "user", auth.UserIdentity{Username: p(), Hostname: p(), CurrentUser: rng.IntN(4) == 0,
			AuthUsername: p(), AuthHostname: p()})
	}
	g.Add("sys/user", "user_nil")
	for _, r := range [][2]string{{"r", ""}, {"r", "%"}, {"a`b", "h"}, {"", ""}, {"role", "localhost"}} {
		g.Add("sys/role", "role", r[0], r[1])
	}
	for i := 0; i < 10; i++ {
		g.Add("rand/role", "role", []string{"", "x", "`"}[rng.IntN(3)], []string{"", "h", "%"}[rng.IntN(3)])
	}
	// duration.
	for _, s := range []string{"0", "", "1h", "1d", "1m", "2h30m", "1.5h", "0.5d", "1d2h3m", "10", "1", "h", "1x",
		"1s", "1h1", "1hh", "..1h", "1.2.3h", ".5m", "1.m", "00h", "0h", "1H", "1D", "١h", "1é",
		"100000000000d", "1e3h", "-1h", "+1h", " 1h", "1h ", "1m1m1m", "9223372036h", "106751d", "106752d",
		"0.000000001m", "1d-1h", "١٢h", "1.7976931348623157e308h"} {
		g.Add("sys/duration", "duration", s)
	}
	for i := 0; i < 120; i++ {
		var sb strings.Builder
		for k := 0; k < 1+rng.IntN(4); k++ {
			fmt.Fprintf(&sb, "%d", rng.IntN(1000))
			if rng.IntN(3) == 0 {
				fmt.Fprintf(&sb, ".%d", rng.IntN(100))
			}
			sb.WriteByte("dhmhdmsx"[rng.IntN(8)])
		}
		g.Add("rand/duration", "duration", sb.String())
	}
	for _, s := range []string{"", "none", "NONE", "Table", "partition", "PARTITION", "x", "İ", "nonE ", "table1"} {
		g.Add("sys/affinity", "affinity", s)
	}
}
