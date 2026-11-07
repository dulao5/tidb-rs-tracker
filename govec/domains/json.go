package domains

// Domain "json": pkg/types/json_binary.go, json_binary_functions.go,
// json_path_expr.go and json_constants.go (block D01.5, exception A4),
// types.BinaryJSON and types.JSONPathExpression.
//
// Values:
//   - a JSON argument is a value spec ("doc"): a vec.GoString is the text,
//     parsed with ParseBinaryJSONFromString (the record fails on a parse error
//     only for ops whose own input is the text: parse); an object
//     {"k": kind, "v": ...} is built with CreateBinaryJSON from a Go value:
//     "int64" (number), "uint64" (number), "float64" (IEEE bits as a number),
//     "string" (GoString), "nil", "bool", "opaque" ([type code, hex]),
//     "time" ([CoreTime bits, mysql type]), "duration" ([nanoseconds, fsp]),
//     "array" (list of specs), "object" ([[key GoString, spec], ...]);
//   - an output JSON is {"tc": TypeCode, "hex": hex(Value), "s": String()};
//   - paths are GoStrings parsed with ParseJSONPathExpr; a path parse error is
//     the record's error.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

const jsonSeed = 20261108

func init() {
	vec.Register(&vec.Domain{
		Name:     "json",
		Seed:     jsonSeed,
		Coverage: jsonCoverage,
		Blocks:   []string{"D01.5"},
		Errors: []vec.NamedErr{
			{Name: "ErrInvalidJSONText", Err: types.ErrInvalidJSONText},
			{Name: "ErrInvalidJSONPath", Err: types.ErrInvalidJSONPath},
			{Name: "ErrInvalidJSONPathMultipleSelection", Err: types.ErrInvalidJSONPathMultipleSelection},
			{Name: "ErrJSONDocumentTooDeep", Err: types.ErrJSONDocumentTooDeep},
			{Name: "ErrJSONObjectKeyTooLong", Err: types.ErrJSONObjectKeyTooLong},
			{Name: "ErrInvalidJSONPathArrayCell", Err: types.ErrInvalidJSONPathArrayCell},
			{Name: "ErrJSONVacuousPath", Err: types.ErrJSONVacuousPath},
			{Name: "ErrJSONBadOneOrAllArg", Err: types.ErrJSONBadOneOrAllArg},
		},
		Exempt: map[string]string{
			"init":                       "creates the global LRU path cache, which the port does not keep (A4 spec 5.6)",
			"jsonPathExpressionKey.Hash": "the key of that cache",
			"JSONPathExpression.clone":   "copies legs for the cache; the port's Clone is derived",
			"BinaryJSON.UnmarshalJSON":   "covered through ParseBinaryJSONFromString (parse), its only caller in TiDB",
		},
		Gzip:     true,
		Generate: jsonGenerate,
		Exec:     jsonExec,
	})
}

var jsonOutFuncs = []string{"BinaryJSON.String", "BinaryJSON.MarshalJSON", "BinaryJSON.marshalTo",
	"BinaryJSON.marshalFloat64To", "BinaryJSON.marshalArrayTo", "BinaryJSON.marshalObjTo",
	"jsonMarshalStringTo", "jsonMarshalOpaqueTo", "jsonMarshalLiteralTo", "jsonMarshalTimeTo",
	"jsonMarshalDurationTo", "quoteJSONString", "BinaryJSON.ArrayGetElem", "BinaryJSON.objectGetKey",
	"BinaryJSON.objectGetVal", "BinaryJSON.valEntryGet", "BinaryJSON.GetElemCount", "BinaryJSON.GetString",
	"BinaryJSON.GetInt64", "BinaryJSON.GetUint64", "BinaryJSON.GetFloat64", "BinaryJSON.GetOpaque",
	"BinaryJSON.GetTime", "BinaryJSON.GetTimeWithFsp", "BinaryJSON.GetDuration"}

var jsonCreateFuncs = []string{"CreateBinaryJSON", "CreateBinaryJSONWithCheck", "appendBinaryJSON",
	"appendZero", "appendUint32", "appendBinaryNumber", "appendBinaryString", "appendBinaryOpaque",
	"appendBinaryFloat64", "appendBinaryUint64", "appendBinaryUint32", "appendBinaryArray",
	"appendBinaryValElem", "appendBinaryObject", "BinaryJSON.GetElemDepth"}

var jsonPathFuncs = []string{"ParseJSONPathExpr", "parseJSONPathExpr", "parseJSONPathWildcard",
	"parseJSONPathArray", "parseJSONPathMember", "isEcmascriptIdentifier", "validateIndexRange",
	"jsonPathArrayIndexFromStart", "jsonPathArrayIndexFromLast", "jsonPathStream.tryReadString",
	"jsonPathStream.tryReadIndexNumber", "jsonPathStream.tryParseArrayIndex", "unquoteJSONString",
	"decodeEscapedUnicode", "jsonPathStream.read", "jsonPathStream.peek", "jsonPathStream.skip",
	"jsonPathStream.exhausted", "jsonPathStream.readWhile", "jsonPathStream.skipWhiteSpace"}

var jsonExtractFuncs = []string{"BinaryJSON.Extract", "BinaryJSON.extractTo", "BinaryJSON.objectSearchKey",
	"jsonFinished", "JSONPathExpression.popOneLeg", "JSONPathExpression.CouldMatchMultipleValues",
	"jsonPathArrayIndex.getIndexFromStart", "jsonPathArraySelectionAsterisk.getIndexRange",
	"jsonPathArraySelectionIndex.getIndexRange", "jsonPathArraySelectionRange.getIndexRange",
	"jsonPathExpressionFlag.containsAnyAsterisk", "jsonPathExpressionFlag.containsAnyRange",
	"buildBinaryJSONArray", "buildBinaryJSONElements"}

func withFuncs(a []string, b ...string) []string { return append(append([]string{}, a...), b...) }

var jsonCoverage = map[string]vec.Coverage{
	"parse":          {Min: 20000, GoFuncs: withFuncs(append(jsonOutFuncs, jsonCreateFuncs...), "ParseBinaryJSONFromString")},
	"create":         {Min: 3000, GoFuncs: withFuncs(append(jsonOutFuncs, jsonCreateFuncs...), "BinaryJSON.Copy")},
	"type":           {Min: 500, GoFuncs: []string{"BinaryJSON.Type", "BinaryJSON.GetOpaqueFieldType"}},
	"unquote":        {Min: 500, GoFuncs: []string{"BinaryJSON.Unquote", "UnquoteString", "unquoteJSONString", "decodeEscapedUnicode"}},
	"unquote_string": {Min: 500, GoFuncs: []string{"UnquoteString", "unquoteJSONString", "decodeEscapedUnicode"}},
	"hash":           {Min: 2000, GoFuncs: []string{"BinaryJSON.HashValue", "getInt64FractionLength", "getUint64FractionLength"}},
	"compare": {Min: 5000, GoFuncs: []string{"CompareBinaryJSON", "compareFloat64PrecisionLoss", "compareInt64",
		"compareFloat64", "compareUint64", "compareInt64Uint64", "compareFloat64Int64", "compareFloat64Uint64"}},
	"is_zero":    {Min: 300, GoFuncs: []string{"BinaryJSON.IsZero"}},
	"depth":      {Min: 300, GoFuncs: []string{"BinaryJSON.GetElemDepth"}},
	"keys":       {Min: 300, GoFuncs: []string{"BinaryJSON.GetKeys"}},
	"get_value":  {Min: 100, GoFuncs: []string{"BinaryJSON.GetValue"}},
	"peek":       {Min: 500, GoFuncs: []string{"PeekBytesAsJSON"}},
	"path_parse": {Min: 5000, GoFuncs: withFuncs(jsonPathFuncs, "JSONPathExpression.String", "jsonPathArrayIndex.String")},
	"extract":    {Min: 8000, GoFuncs: withFuncs(jsonPathFuncs, jsonExtractFuncs...)},
	"modify": {Min: 5000, GoFuncs: withFuncs(jsonPathFuncs, "BinaryJSON.Modify", "BinaryJSON.extractOne",
		"binaryModifier.set", "binaryModifier.replace", "binaryModifier.insert", "binaryModifier.doInsert",
		"binaryModifier.rebuild", "binaryModifier.rebuildTo", "buildBinaryJSONObject",
		"JSONPathExpression.popOneLastLeg")},
	"array_insert": {Min: 1500, GoFuncs: withFuncs(jsonPathFuncs, "BinaryJSON.ArrayInsert")},
	"remove": {Min: 2000, GoFuncs: withFuncs(jsonPathFuncs, "BinaryJSON.Remove", "binaryModifier.remove",
		"binaryModifier.doRemove")},
	"merge":       {Min: 1500, GoFuncs: []string{"MergeBinaryJSON", "getAdjacentObjects", "mergeBinaryArray", "mergeBinaryObject"}},
	"merge_patch": {Min: 1500, GoFuncs: []string{"MergePatchBinaryJSON", "mergePatchBinaryJSON"}},
	"contains":    {Min: 2000, GoFuncs: []string{"ContainsBinaryJSON"}},
	"overlaps":    {Min: 2000, GoFuncs: []string{"OverlapsBinaryJSON"}},
	"search": {Min: 2000, GoFuncs: withFuncs(jsonPathFuncs, "BinaryJSON.Search", "BinaryJSON.Walk",
		"BinaryJSON.extractToCallback", "JSONPathExpression.pushBackOneArraySelectionLeg",
		"JSONPathExpression.pushBackOneKeyLeg")},
	"walk": {Min: 1000, GoFuncs: withFuncs(jsonPathFuncs, "BinaryJSON.Walk", "BinaryJSON.extractToCallback")},
}

// jsonOut: the output form of a BinaryJSON.
func jsonOut(bj types.BinaryJSON) map[string]any {
	return map[string]any{"tc": bj.TypeCode, "hex": hex.EncodeToString(bj.Value), "s": vec.GoString(bj.String())}
}

// jsonOfSpec builds the BinaryJSON of a value spec (see the file comment).
func jsonOfSpec(raw json.RawMessage) types.BinaryJSON {
	var s vec.GoString
	if !isValueSpec(raw) {
		if err := json.Unmarshal(raw, &s); err != nil {
			panic(err)
		}
		bj, err := types.ParseBinaryJSONFromString(string(s))
		if err != nil {
			panic(fmt.Sprintf("bad JSON doc input %q: %v", s, err))
		}
		return bj
	}
	return types.CreateBinaryJSON(goValueOfSpec(raw))
}

func goValueOfSpec(raw json.RawMessage) any {
	var sp struct {
		K string          `json:"k"`
		V json.RawMessage `json:"v"`
	}
	if !isValueSpec(raw) {
		var s vec.GoString
		if err := json.Unmarshal(raw, &s); err == nil {
			bj, err := types.ParseBinaryJSONFromString(string(s))
			if err != nil {
				panic(fmt.Sprintf("bad JSON doc input %q: %v", s, err))
			}
			return bj
		}
		panic(fmt.Sprintf("bad value spec %s", raw))
	}
	if err := json.Unmarshal(raw, &sp); err != nil {
		panic(err)
	}
	un := func(v any) {
		if err := json.Unmarshal(sp.V, v); err != nil {
			panic(fmt.Sprintf("bad value spec %s: %v", raw, err))
		}
	}
	switch sp.K {
	case "nil":
		return nil
	case "bool":
		var b bool
		un(&b)
		return b
	case "int64":
		var i int64
		un(&i)
		return i
	case "uint64":
		var u uint64
		un(&u)
		return u
	case "float64":
		var u uint64
		un(&u)
		return math.Float64frombits(u)
	case "string":
		var s vec.GoString
		un(&s)
		return string(s)
	case "opaque":
		var a []json.RawMessage
		un(&a)
		tc := arg[byte](a, 0)
		b, err := hex.DecodeString(arg[string](a, 1))
		if err != nil {
			panic(err)
		}
		return types.Opaque{TypeCode: tc, Buf: b}
	case "time":
		var a []uint64
		un(&a)
		return types.NewTime(types.CoreTime(a[0]), byte(a[1]), 6)
	case "duration":
		var a []int64
		un(&a)
		return types.Duration{Duration: time.Duration(a[0]), Fsp: int(a[1])}
	case "array":
		var a []json.RawMessage
		un(&a)
		out := make([]any, len(a))
		for i, e := range a {
			out[i] = goValueOfSpec(e)
		}
		return out
	case "object":
		var a [][2]json.RawMessage
		un(&a)
		m := make(map[string]any, len(a))
		for _, kv := range a {
			var k vec.GoString
			if err := json.Unmarshal(kv[0], &k); err != nil {
				panic(err)
			}
			m[string(k)] = goValueOfSpec(kv[1])
		}
		return m
	}
	panic("unknown value kind " + sp.K)
}

// isValueSpec: raw is a {"k": kind, ...} spec, not a GoString ({"hex": ...} or a string).
func isValueSpec(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	_, ok := m["k"]
	return ok
}

func pathsOf(raw json.RawMessage) ([]types.JSONPathExpression, error) {
	var ps []vec.GoString
	if err := json.Unmarshal(raw, &ps); err != nil {
		panic(err)
	}
	out := make([]types.JSONPathExpression, 0, len(ps))
	for _, p := range ps {
		pe, err := types.ParseJSONPathExpr(string(p))
		if err != nil {
			return nil, err
		}
		out = append(out, pe)
	}
	return out, nil
}

func jsonExec(op string, args []json.RawMessage) (out any, err error) {
	doc := func(i int) types.BinaryJSON { return jsonOfSpec(args[i]) }
	switch op {
	case "parse":
		bj, err := types.ParseBinaryJSONFromString(string(arg[vec.GoString](args, 0)))
		if err != nil {
			return nil, err
		}
		return map[string]any{"j": jsonOut(bj), "type": bj.Type(), "depth": bj.GetElemDepth()}, nil
	case "create":
		bj, err := types.CreateBinaryJSONWithCheck(goValueOfSpec(args[0]))
		if err != nil {
			return nil, err
		}
		c := bj.Copy()
		marshaled, merr := bj.MarshalJSON()
		res := map[string]any{"j": jsonOut(c), "marshal": vec.GoString(marshaled)}
		if merr != nil {
			res["marshal_err"] = vec.GoString(merr.Error())
		}
		return res, nil
	case "type":
		return doc(0).Type(), nil
	case "unquote":
		s, err := doc(0).Unquote()
		return vec.GoString(s), err
	case "unquote_string":
		s, err := types.UnquoteString(string(arg[vec.GoString](args, 0)))
		return vec.GoString(s), err
	case "hash":
		return hex.EncodeToString(doc(0).HashValue(nil)), nil
	case "compare":
		return types.CompareBinaryJSON(doc(0), doc(1)), nil
	case "is_zero":
		return doc(0).IsZero(), nil
	case "depth":
		return doc(0).GetElemDepth(), nil
	case "keys":
		return jsonOut(doc(0).GetKeys()), nil
	case "get_value":
		bj := doc(0)
		switch v := bj.GetValue().(type) {
		case int64:
			return map[string]any{"int64": v}, nil
		case uint64:
			return map[string]any{"uint64": v}, nil
		case float64:
			return map[string]any{"float64": math.Float64bits(v)}, nil
		case []byte:
			return map[string]any{"string": vec.GoString(v)}, nil
		case types.Duration:
			return map[string]any{"duration": []int64{int64(v.Duration), int64(v.Fsp)}}, nil
		case types.Time:
			return map[string]any{"time": vec.GoString(v.String())}, nil
		case nil:
			return nil, nil
		}
		panic("unexpected GetValue type")
	case "peek":
		b, err := hex.DecodeString(arg[string](args, 0))
		if err != nil {
			panic(err)
		}
		return types.PeekBytesAsJSON(b)
	case "path_parse":
		pe, err := types.ParseJSONPathExpr(string(arg[vec.GoString](args, 0)))
		if err != nil {
			return nil, err
		}
		return map[string]any{"s": vec.GoString(pe.String()), "multi": pe.CouldMatchMultipleValues()}, nil
	case "extract":
		bj := doc(0)
		ps, err := pathsOf(args[1])
		if err != nil {
			return nil, err
		}
		ret, found := bj.Extract(ps)
		if !found {
			return map[string]any{"found": false}, nil
		}
		return map[string]any{"found": true, "j": jsonOut(ret)}, nil
	case "modify":
		bj := doc(0)
		ps, err := pathsOf(args[1])
		if err != nil {
			return nil, err
		}
		var specs []json.RawMessage
		if err := json.Unmarshal(args[2], &specs); err != nil {
			panic(err)
		}
		vals := make([]types.BinaryJSON, len(specs))
		for i, s := range specs {
			vals[i] = jsonOfSpec(s)
		}
		ret, err := bj.Modify(ps, vals, types.JSONModifyType(arg[byte](args, 3)))
		if err != nil {
			return nil, err
		}
		return jsonOut(ret), nil
	case "array_insert":
		bj := doc(0)
		ps, err := pathsOf(args[1])
		if err != nil {
			return nil, err
		}
		ret, err := bj.ArrayInsert(ps[0], doc(2))
		if err != nil {
			return nil, err
		}
		return jsonOut(ret), nil
	case "remove":
		bj := doc(0)
		ps, err := pathsOf(args[1])
		if err != nil {
			return nil, err
		}
		ret, err := bj.Remove(ps)
		if err != nil {
			return nil, err
		}
		return jsonOut(ret), nil
	case "merge":
		var specs []json.RawMessage
		if err := json.Unmarshal(args[0], &specs); err != nil {
			panic(err)
		}
		bjs := make([]types.BinaryJSON, len(specs))
		for i, s := range specs {
			bjs[i] = jsonOfSpec(s)
		}
		return jsonOut(types.MergeBinaryJSON(bjs)), nil
	case "merge_patch":
		var specs []json.RawMessage
		if err := json.Unmarshal(args[0], &specs); err != nil {
			panic(err)
		}
		bjs := make([]*types.BinaryJSON, len(specs))
		for i, s := range specs {
			if string(s) == "null" {
				continue
			}
			bj := jsonOfSpec(s)
			bjs[i] = &bj
		}
		ret, err := types.MergePatchBinaryJSON(bjs)
		if err != nil {
			return nil, err
		}
		if ret == nil {
			return nil, nil
		}
		return jsonOut(*ret), nil
	case "contains":
		return types.ContainsBinaryJSON(doc(0), doc(1)), nil
	case "overlaps":
		return types.OverlapsBinaryJSON(doc(0), doc(1)), nil
	case "search":
		bj := doc(0)
		ps, err := pathsOf(args[4])
		if err != nil {
			return nil, err
		}
		res, isNull, err := bj.Search(arg[string](args, 1), string(arg[vec.GoString](args, 2)), arg[byte](args, 3), ps)
		if err != nil {
			return nil, err
		}
		if isNull {
			return map[string]any{"null": true}, nil
		}
		return map[string]any{"null": false, "j": jsonOut(res)}, nil
	case "walk":
		bj := doc(0)
		ps, err := pathsOf(args[1])
		if err != nil {
			return nil, err
		}
		limit := arg[int](args, 2)
		var visited []any
		err = bj.Walk(func(fullpath types.JSONPathExpression, v types.BinaryJSON) (bool, error) {
			visited = append(visited, []any{vec.GoString(fullpath.String()), jsonOut(v)})
			return len(visited) >= limit, nil
		}, ps...)
		return visited, err
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// ---------------------------------------------------------------------------------------------
// Inputs.

// jsonEdgeDocs: hand-written texts for the parser, numbers, strings and depth.
var jsonEdgeDocs = []string{
	``, ` `, `null`, `true`, `false`, `nul`, `tru`, `0`, `-0`, `-0.0`, `0.0`, `1`, `-1`, `01`, `1.`, `.5`, `+1`,
	`1e5`, `1E5`, `1e+5`, `1e-5`, `1.5e3`, `-1.5E-3`, `1e-7`, `1e-6`, `1e20`, `1e21`, `1e22`, `1.5e21`, `123456789e13`,
	`1e308`, `1.7976931348623157e308`, `1e309`, `1e400`, `-1e400`, `1e-320`, `1e-400`, `5e-324`, `2e-324`,
	`9223372036854775807`, `9223372036854775808`, `-9223372036854775808`, `-9223372036854775809`,
	`18446744073709551615`, `18446744073709551616`, `-18446744073709551616`, `4503599627370495`, `4503599627370496`,
	`9007199254740993`, `0.1`, `0.30000000000000004`, `3.0`, `3.00`, `100`, `1.0e2`, `123.456e-10`,
	`""`, `"a"`, `"\""`, `"\\"`, `"\/"`, `"\b\f\n\r\t"`, `"\u0000"`, `"\u001f"`, `"\u007f"`, `"é"`,
	`"  "`, `"😀"`, `"\ud800"`, `"\udc00"`, `"\ud800x"`, `"𝄞"`, `"\u12"`, `"\x"`,
	`"&<>"`, `"<script>"`, `"é中文😀"`, "\"\x7f\"", "\"\x01\"", "\"tab\there\"", `"unterminated`, `"a"b`,
	"\"\xff\"", "\"\xc3\x28\"", "\"\xe2\x80\xa8\"", "\"\xed\xa0\x80\"", "\"a\xf0\x9f\x98\"",
	`[]`, `{}`, `[ ]`, `{ }`, `[1,2,3]`, `[1,]`, `[,1]`, `[1 2]`, `{"a":1,}`, `{"a" 1}`, `{a:1}`, `{"a":1,"a":2}`,
	`{"b":1,"a":2,"c":3}`, `{"a":1,"b":{"a":2,"a":3}}`, `{"":1}`, `{" ":1,"":2}`, `{"\u0000":1}`, `[1] [2]`, `1 2`,
	`{"a":[1,{"b":[true,false,null]}]}`, " \t\n\r[1] \t\n\r", "\v[1]", "[1]\x00", `[1]x`, `{}{}`,
	`[[[[[[[[[[1]]]]]]]]]]`, `{"k": "v", "n": 1.5, "a": [1, "2", 3.0], "o": {"x": null}}`,
}

var jsonPathEdges = []string{
	`$`, ` $ `, `$.a`, `$.a.b`, `$.*`, `$[0]`, `$[1]`, `$[*]`, `$**.a`, `$**[0]`, `$.a**.b`, `$**`, `$.a**`,
	`$[last]`, `$[last-1]`, `$[last - 1]`, `$[last-0]`, `$[ last ]`, `$[0 to 1]`, `$[1 to 0]`, `$[0 to last]`,
	`$[last-2 to last]`, `$[last to 0]`, `$[0 to last-1]`, `$[2 to 5]`, `$[0to1]`, `$[0 to]`, `$[0 to 1`,
	`$."a"`, `$."a b"`, `$."a\"b"`, `$."A"`, `$."\n"`, `$.""`, `$."`, `$."a`, `$.a"`, `$.1a`, `$.a1`, `$._`,
	`$.$`, `$.$a`, `$.é`, "$.\xff", `$. a`, `$ .a`, `$.a .b`, `$[ 0 ]`, `$[-1]`, `$[01]`, `$[4294967295]`,
	`$[4294967296]`, `$[99999999999999999999]`, `$[`, `$]`, `$a`, `a`, ``, ` `, `$.`, `$..a`, `$.*.*`, `$[*][*]`,
	`$[*].a`, `$.a[*]`, `$***`, `$* *`, `$[1]**[0]`, `$.a[last]`, `$.a[0 to last]`, "$ .a", "$\x85.a",
	"$.\"\\ud83d\\ude00\"", `$."\\"`, `$.a.*`, `$[0][0][0]`, `$.b[0].c`, `$**.*`,
}

func jsonGenerate(g *vec.Gen, rng *rand.Rand) {
	// Parse: mined, edge, random and mutated texts.
	docs := []string{}
	for _, s := range jsonMinedDocs {
		g.Add("lit/go-tests", "parse", vec.GoString(s))
		docs = append(docs, s)
	}
	for _, s := range jsonEdgeDocs {
		g.Add("sys/edge", "parse", vec.GoString(s))
		docs = append(docs, s)
	}
	// depth and size limits
	for _, d := range []int{99, 100, 101, 102, 200, 9999, 10000, 10001} {
		g.Add("sys/depth", "parse", vec.GoString(strings.Repeat("[", d)+strings.Repeat("]", d)))
		g.Add("sys/depth", "parse", vec.GoString(strings.Repeat("[", d)+"1"+strings.Repeat("]", d)))
		g.Add("sys/depth", "parse", vec.GoString(strings.Repeat(`{"a":`, d)+"1"+strings.Repeat("}", d)))
	}
	g.Add("sys/depth", "parse", vec.GoString(strings.Repeat("[", 150)+`{"k":1e400}`+strings.Repeat("]", 150)))
	for _, n := range []int{65535, 65536} {
		k := strings.Repeat("k", n)
		g.Add("sys/keylen", "parse", vec.GoString(`{"`+k+`":1}`))
		g.Add("sys/keylen", "parse", vec.GoString(`{"`+k+`":1e400}`))
		g.Add("sys/keylen", "parse", vec.GoString(`{"a":1e400,"`+k+`":1}`))
		g.Add("sys/keylen", "parse", vec.GoString(strings.Repeat("[", 120)+`{"`+k+`":1}`+strings.Repeat("]", 120)))
	}
	var good []string
	for i := 0; i < 15000; i++ {
		s := randJSONText(rng, 0)
		g.Add("rand/text", "parse", vec.GoString(s))
		good = append(good, s)
	}
	for i := 0; i < 8000; i++ {
		base := good[rng.IntN(len(good))]
		if rng.IntN(3) == 0 {
			base = docs[rng.IntN(len(docs))]
		}
		g.Add("rand/mutate", "parse", vec.GoString(mutate(rng, base)))
	}
	// Only texts Go parses are document inputs of the other ops.
	var pool []string
	for _, s := range append(append([]string{}, docs...), good...) {
		if _, err := types.ParseBinaryJSONFromString(s); err == nil {
			pool = append(pool, s)
		}
	}
	pick := func() any {
		if rng.IntN(6) == 0 {
			return randSpec(rng, 0)
		}
		return vec.GoString(pool[rng.IntN(len(pool))])
	}

	// Create from Go values.
	for _, b := range []uint64{0, 1 << 63, math.Float64bits(1), math.Float64bits(-1.5), math.Float64bits(1e21),
		math.Float64bits(1e-7), math.Float64bits(math.Inf(1)), math.Float64bits(math.Inf(-1)),
		math.Float64bits(math.NaN()), math.Float64bits(math.MaxFloat64), math.Float64bits(math.SmallestNonzeroFloat64)} {
		g.Add("sys/float", "create", map[string]any{"k": "float64", "v": b})
		g.Add("sys/float", "create", map[string]any{"k": "array", "v": []any{map[string]any{"k": "float64", "v": b}}})
	}
	for i := 0; i < 3000; i++ {
		g.Add("rand", "create", randSpec(rng, 0))
	}

	for i := 0; i < 600; i++ {
		d := pick()
		g.Add("rand", "type", d)
		g.Add("rand", "unquote", d)
		g.Add("rand", "is_zero", d)
		g.Add("rand", "depth", d)
		g.Add("rand", "get_value", randScalarSpec(rng))
	}
	for i := 0; i < 400; i++ {
		g.Add("rand", "keys", vec.GoString(randObjectText(rng)))
	}
	for _, s := range []string{``, `"`, `""`, `"a"`, `a"`, `"a`, `"\"`, `"\\"`, `"A"`, `"é"`, `"\u12"`,
		`"ሴ5"`, `"\uzzzz"`, `"\ug000"`, `"\u00g0"`, `"\x"`, `"\b\f\n\r\t\/"`, `"\ud800"`, "\"\xff\"", `"\"`, `"ab\"`} {
		g.Add("sys", "unquote_string", vec.GoString(s))
	}
	for i := 0; i < 600; i++ {
		g.Add("rand", "unquote_string", vec.GoString(`"`+randStringBody(rng)+`"`))
	}
	for i := 0; i < 2500; i++ {
		g.Add("rand", "hash", pick())
	}
	for i := 0; i < 6000; i++ {
		a := pick()
		b := pick()
		if rng.IntN(3) == 0 {
			a, b = randScalarSpec(rng), randScalarSpec(rng)
		}
		g.Add("rand", "compare", a, b)
	}
	for i := 0; i < 600; i++ {
		var b []byte
		if rng.IntN(2) == 0 {
			bj, _ := types.ParseBinaryJSONFromString(pool[rng.IntN(len(pool))])
			b = append([]byte{bj.TypeCode}, bj.Value...)
			if rng.IntN(2) == 0 && len(b) > 1 {
				b = b[:1+rng.IntN(len(b)-1)]
			}
		} else {
			b = make([]byte, rng.IntN(12))
			for j := range b {
				b[j] = byte(rng.IntN(256))
			}
			if len(b) > 0 && rng.IntN(2) == 0 {
				b[0] = []byte{1, 3, 4, 9, 10, 11, 12, 13, 14, 15, 16, 17, 2, 0}[rng.IntN(14)]
			}
		}
		g.Add("rand", "peek", hex.EncodeToString(b))
	}

	// Paths.
	paths := []string{}
	for _, p := range jsonMinedPaths {
		g.Add("lit/go-tests", "path_parse", vec.GoString(p))
		paths = append(paths, p)
	}
	for _, p := range jsonPathEdges {
		g.Add("sys/edge", "path_parse", vec.GoString(p))
		paths = append(paths, p)
	}
	for i := 0; i < 5000; i++ {
		p := randPath(rng)
		if rng.IntN(4) == 0 {
			p = mutate(rng, p)
		}
		g.Add("rand", "path_parse", vec.GoString(p))
		paths = append(paths, p)
	}
	pickPaths := func(n int) []vec.GoString {
		out := make([]vec.GoString, n)
		for i := range out {
			if rng.IntN(3) == 0 {
				out[i] = vec.GoString(paths[rng.IntN(len(paths))])
			} else {
				out[i] = vec.GoString(randPath(rng))
			}
		}
		return out
	}
	for i := 0; i < 9000; i++ {
		g.Add("rand", "extract", pick(), pickPaths(1+rng.IntN(3)))
	}
	for i := 0; i < 6000; i++ {
		n := 1 + rng.IntN(3)
		vals := make([]any, n)
		for j := range vals {
			vals[j] = pick()
		}
		if rng.IntN(20) == 0 {
			vals = vals[:len(vals)-1]
		}
		g.Add("rand", "modify", pick(), pickPaths(n), vals, 1+rng.IntN(3))
	}
	for i := 0; i < 1800; i++ {
		g.Add("rand", "array_insert", pick(), pickPaths(1), pick())
	}
	for i := 0; i < 2500; i++ {
		g.Add("rand", "remove", pick(), pickPaths(1+rng.IntN(2)))
	}
	for i := 0; i < 1800; i++ {
		n := 1 + rng.IntN(4)
		docs := make([]any, n)
		for j := range docs {
			docs[j] = pick()
			if rng.IntN(2) == 0 {
				docs[j] = vec.GoString(randObjectText(rng))
			}
		}
		g.Add("rand", "merge", docs)
		patch := make([]any, n)
		for j := range patch {
			patch[j] = docs[j]
			if rng.IntN(8) == 0 {
				patch[j] = nil
			}
		}
		g.Add("rand", "merge_patch", patch)
	}
	for i := 0; i < 2500; i++ {
		a, b := pick(), pick()
		if rng.IntN(2) == 0 {
			bj, _ := types.ParseBinaryJSONFromString(pool[rng.IntN(len(pool))])
			ex, ok := bj.Extract([]types.JSONPathExpression{mustPath("$[0]")})
			if ok {
				a, b = vec.GoString(bj.String()), vec.GoString(ex.String())
			}
		}
		g.Add("rand", "contains", a, b)
		g.Add("rand", "overlaps", a, b)
	}
	for i := 0; i < 2500; i++ {
		ct := []string{"one", "all", "one", "all", "ONE", "x"}[rng.IntN(6)]
		pat := []string{"%", "a%", "%b", "_", "abc", "x%y", "%\\%%", "", "1", "%a_c%"}[rng.IntN(10)]
		if rng.IntN(3) == 0 {
			pat = randStringBody(rng)
		}
		esc := []byte{'\\', '\\', '#', 0}[rng.IntN(4)]
		var ps []vec.GoString
		if rng.IntN(2) == 0 {
			ps = pickPaths(1 + rng.IntN(2))
		}
		g.Add("rand", "search", pick(), ct, vec.GoString(pat), esc, ps)
	}
	for i := 0; i < 1200; i++ {
		var ps []vec.GoString
		if rng.IntN(2) == 0 {
			ps = pickPaths(1 + rng.IntN(2))
		}
		g.Add("rand", "walk", pick(), ps, 1+rng.IntN(40))
	}
}

func mustPath(s string) types.JSONPathExpression {
	pe, err := types.ParseJSONPathExpr(s)
	if err != nil {
		panic(err)
	}
	return pe
}

var jsonKeys = []string{"a", "b", "c", "aa", "ab", "A", "_", "$", "1", "", " ", "key", "k1", "é", "中", "a b", "a.b",
	"\"", "\\", " ", "😀", "z", "Z", "bb", "x"}

func randKey(rng *rand.Rand) string {
	if rng.IntN(8) == 0 {
		return randStringBody(rng)
	}
	return jsonKeys[rng.IntN(len(jsonKeys))]
}

// randStringBody: the inside of a JSON string literal (escapes included).
func randStringBody(rng *rand.Rand) string {
	pieces := []string{"a", "b", "abc", " ", "%", "_", "\\\"", "\\\\", "\\/", "\\b", "\\f", "\\n", "\\r", "\\t",
		"\\u0000", "\\u001f", "\\u0041", "\\u00e9", "\\u2028", "\\u2029", "\\ud83d\\ude00", "\\ud800", "\\udfff",
		"é", "中文", "😀", " ", "<", ">", "&", "\x7f", "\xff", "\xc3\x28", "\xe2\x82", "x", "1", "0"}
	n := rng.IntN(6)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(pieces[rng.IntN(len(pieces))])
	}
	return b.String()
}

var jsonNumbers = []string{"0", "-0", "1", "-1", "2", "3", "3.0", "1.5", "-2.25", "0.1", "1e5", "1E-5", "1e21",
	"1e-7", "1e20", "123456789", "9223372036854775807", "9223372036854775808", "-9223372036854775808",
	"18446744073709551615", "18446744073709551616", "4503599627370496", "9007199254740993", "1e308", "5e-324",
	"0.30000000000000004", "100", "1.0", "2.0", "-0.0", "12345678901234567890", "1e400"}

func randNumber(rng *rand.Rand) string {
	switch rng.IntN(5) {
	case 0:
		return fmt.Sprint(rng.Int64N(2000) - 1000)
	case 1:
		return fmt.Sprint(int64(rng.Uint64()))
	case 2:
		return fmt.Sprint(rng.Uint64())
	case 3:
		return fmt.Sprintf("%g", (rng.Float64()-0.5)*math.Pow(10, float64(rng.IntN(60)-30)))
	}
	return jsonNumbers[rng.IntN(len(jsonNumbers))]
}

func randJSONText(rng *rand.Rand, depth int) string {
	k := rng.IntN(10)
	if depth > 4 {
		k = rng.IntN(5)
	}
	switch k {
	case 0:
		return []string{"null", "true", "false"}[rng.IntN(3)]
	case 1, 2:
		return randNumber(rng)
	case 3, 4:
		return `"` + randStringBody(rng) + `"`
	case 5, 6:
		n := rng.IntN(5)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = randJSONText(rng, depth+1)
		}
		return "[" + strings.Join(parts, ws(rng)+","+ws(rng)) + "]"
	default:
		n := rng.IntN(5)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"` + randKey(rng) + `"` + ws(rng) + ":" + ws(rng) + randJSONText(rng, depth+1)
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
}

// randObjectText: a random object text that Go parses.
func randObjectText(rng *rand.Rand) string {
	for {
		s := randObjectTextAny(rng)
		if _, err := types.ParseBinaryJSONFromString(s); err == nil {
			return s
		}
	}
}

func randObjectTextAny(rng *rand.Rand) string {
	n := rng.IntN(5)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `"` + jsonKeys[rng.IntN(8)] + `":` + randJSONText(rng, 2)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func ws(rng *rand.Rand) string {
	return []string{"", "", "", " ", "\n", "\t ", "\r\n"}[rng.IntN(7)]
}

func randScalarSpec(rng *rand.Rand) any {
	switch rng.IntN(10) {
	case 0:
		return map[string]any{"k": "int64", "v": int64(rng.Uint64())}
	case 1:
		return map[string]any{"k": "uint64", "v": rng.Uint64() >> rng.IntN(64)}
	case 2:
		return map[string]any{"k": "float64", "v": math.Float64bits((rng.Float64() - 0.5) * 1e6)}
	case 3:
		return map[string]any{"k": "string", "v": vec.GoString(randBytes(rng))}
	case 4:
		tcs := []byte{mysql.TypeBlob, mysql.TypeTinyBlob, mysql.TypeString, mysql.TypeVarString, mysql.TypeVarchar,
			mysql.TypeBit, mysql.TypeLonglong, mysql.TypeGeometry, mysql.TypeMediumBlob, mysql.TypeLongBlob, 0, 255}
		return map[string]any{"k": "opaque", "v": []any{tcs[rng.IntN(len(tcs))], hex.EncodeToString(randBytes(rng))}}
	case 5:
		tps := []byte{mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp}
		ct := randCoreTime(rng)
		return map[string]any{"k": "time", "v": []uint64{ct, uint64(tps[rng.IntN(3)])}}
	case 6:
		ns := rng.Int64N(2*838*3600*1e9) - 838*3600*1e9
		return map[string]any{"k": "duration", "v": []int64{ns, int64(rng.IntN(7))}}
	case 7:
		return map[string]any{"k": "bool", "v": rng.IntN(2) == 0}
	case 8:
		return map[string]any{"k": "nil"}
	}
	return map[string]any{"k": "int64", "v": rng.Int64N(20) - 10}
}

func randSpec(rng *rand.Rand, depth int) any {
	switch k := rng.IntN(10); {
	case k < 6 || depth > 3:
		return randScalarSpec(rng)
	case k < 8:
		n := rng.IntN(4)
		a := make([]any, n)
		for i := range a {
			a[i] = randSpec(rng, depth+1)
		}
		return map[string]any{"k": "array", "v": a}
	default:
		n := rng.IntN(4)
		a := make([]any, n)
		for i := range a {
			a[i] = []any{vec.GoString(randKey(rng)), randSpec(rng, depth+1)}
		}
		return map[string]any{"k": "object", "v": a}
	}
}

func randBytes(rng *rand.Rand) []byte {
	b := make([]byte, rng.IntN(10))
	for i := range b {
		if rng.IntN(3) == 0 {
			b[i] = byte(rng.IntN(256))
		} else {
			b[i] = byte('a' + rng.IntN(26))
		}
	}
	return b
}

// randCoreTime: the CoreTime bits of a valid date/datetime.
func randCoreTime(rng *rand.Rand) uint64 {
	t := types.FromDate(rng.IntN(10000), rng.IntN(13), rng.IntN(32), rng.IntN(24), rng.IntN(60), rng.IntN(60),
		rng.IntN(1000000))
	return uint64(t)
}

func randPath(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("$")
	n := rng.IntN(4)
	for i := 0; i < n; i++ {
		switch rng.IntN(9) {
		case 0, 1, 2:
			k := jsonKeys[rng.IntN(len(jsonKeys))]
			if isIdent(k) && rng.IntN(2) == 0 {
				b.WriteString("." + k)
			} else {
				b.WriteString(`."` + strings.ReplaceAll(strings.ReplaceAll(k, `\`, `\\`), `"`, `\"`) + `"`)
			}
		case 3:
			b.WriteString(".*")
		case 4:
			b.WriteString(fmt.Sprintf("[%d]", rng.IntN(5)))
		case 5:
			b.WriteString([]string{"[*]", "[last]", "[last-1]", "[0 to 1]", "[1 to last]", "[last-2 to last]", "[0 to 0]", "[2 to 9]"}[rng.IntN(8)])
		case 6:
			if i+1 < n {
				b.WriteString("**")
			}
		default:
			b.WriteString(fmt.Sprintf("[%d]", rng.IntN(3)))
		}
	}
	return b.String()
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (i != 0 && c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' ||
			c == '$' || c >= 0x80 {
			continue
		}
		return false
	}
	return true
}
