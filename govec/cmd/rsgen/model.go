package main

// D06.1: pkg/meta/model types and their encoding/json behaviour. The structs
// are generated from reflection (field order, json tags, kinds), the struct
// field sets of encoding/json are computed with typeFields (a port of
// encoding/json's typeFields: embedded-struct promotion, dominance, tag
// names), and for each struct rsgen emits an encoder and a decoder that follow
// encoding/json's rules for each field kind. Hand-ported Go methods (Clone,
// String, ...) live next to the generated code in tidb-rs crates/model.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/auth"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	ptypes "github.com/pingcap/tidb/pkg/parser/types"
)

const modelPkg = "github.com/pingcap/tidb/pkg/meta/model"

// parserModelPkg: its named basic types are newtypes in tidb_parser::model (block D02.3).
const parserModelPkg = "github.com/pingcap/tidb/pkg/parser/model"

// msnake: snake case of a model field name ("IDs" is one word).
func msnake(s string) string { return snake(strings.ReplaceAll(s, "IDs", "Ids")) }

var (
	ciStrType     = reflect.TypeOf(pmodel.CIStr{})
	fieldTypeType = reflect.TypeOf(ptypes.FieldType{})
	userType      = reflect.TypeOf(auth.UserIdentity{})
	timeType      = reflect.TypeOf(time.Time{})
	sqlModeType   = reflect.TypeOf(mysql.SQLMode(0))
)

// jsonField is one field of encoding/json's view of a struct.
type jsonField struct {
	name      string
	tag       bool
	index     []int
	typ       reflect.Type
	omitEmpty bool
	quoted    bool
}

// typeFields: port of encoding/json.typeFields (without omitzero, unused here).
func typeFields(t reflect.Type) []jsonField {
	current := []jsonField{}
	next := []jsonField{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	var fields []jsonField
	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					t := sf.Type
					if t.Kind() == reflect.Pointer {
						t = t.Elem()
					}
					if !sf.IsExported() && t.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				if !isValidTag(name) {
					name = ""
				}
				index := append(append([]int{}, f.index...), i)
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				hasOpt := func(o string) bool { return slices.Contains(strings.Split(opts, ","), o) }
				quoted := false
				if hasOpt("string") {
					switch ft.Kind() {
					case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
						reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
						reflect.Float32, reflect.Float64, reflect.String:
						quoted = true
					}
				}
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					fields = append(fields, jsonField{name: name, tag: tagged, index: index, typ: ft,
						omitEmpty: hasOpt("omitempty"), quoted: quoted})
					if count[f.typ] > 1 {
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, jsonField{name: ft.Name(), index: index, typ: ft})
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b jsonField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if len(a.index) != len(b.index) {
			return len(a.index) - len(b.index)
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		fi := fields[i]
		for advance = 1; i+advance < len(fields); advance++ {
			if fields[i+advance].name != fi.name {
				break
			}
		}
		if advance == 1 {
			out = append(out, fi)
			continue
		}
		fs := fields[i : i+advance]
		// dominantField: two at the same depth with the same tag state cancel out.
		if len(fs) > 1 && len(fs[0].index) == len(fs[1].index) && fs[0].tag == fs[1].tag {
			continue
		}
		out = append(out, fs[0])
	}
	fields = out
	slices.SortFunc(fields, func(a, b jsonField) int { return slices.Compare(a.index, b.index) })
	return fields
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// ------------------------------------------------------------------ Rust types

type modelGen struct {
	structs  []reflect.Type          // generated structs, in discovery order
	seen     map[reflect.Type]bool   //
	anonName map[reflect.Type]string // anonymous struct types -> Rust name
	named    map[reflect.Type]bool   // model named basic types used
	hasClone map[reflect.Type]bool   // Go has a Clone method: Clone is hand-written
	leaves   map[string]bool         // non-struct Rust field types that GoCopy copies with Clone
	extUser  bool
}

var goCopyPrimitives = map[string]bool{"bool": true, "i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true, "f64": true, "String": true, "()": true}

// collectLeaves records the leaf types of a field type for GoCopy (go_copy_leaf!).
func (g *modelGen) collectLeaves(t reflect.Type) {
	switch {
	case t.Kind() == reflect.Pointer:
		g.collectLeaves(t.Elem())
		return
	case t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8:
		g.collectLeaves(t.Elem())
		return
	case t.Kind() == reflect.Map:
		g.collectLeaves(t.Elem())
		return
	case t.Kind() == reflect.Struct && t.NumField() > 0 && g.isGenStruct(t):
		return
	case t.Kind() == reflect.Slice:
		return // []byte: Option<Vec<u8>>
	}
	if rt := g.rustType(t); !goCopyPrimitives[rt] {
		g.leaves[rt] = true
	}
}

func (g *modelGen) structName(t reflect.Type) string {
	if n, ok := g.anonName[t]; ok {
		return n
	}
	return t.Name()
}

func (g *modelGen) isGenStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && (t.PkgPath() == modelPkg || g.anonName[t] != "")
}

// goTypeString is reflect.Type.String() (Go's type names in error messages).
func goTypeString(t reflect.Type) string { return t.String() }

func (g *modelGen) walk(t reflect.Type, parent string, field string) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		g.walk(t.Elem(), parent, field)
		return
	case reflect.Map:
		g.walk(t.Key(), parent, field)
		g.walk(t.Elem(), parent, field)
		return
	case reflect.Struct:
	default:
		if t.PkgPath() == modelPkg && t.Name() != "" {
			g.named[t] = true
		}
		return
	}
	if t == ciStrType || t == fieldTypeType || t == timeType {
		return
	}
	if t == userType {
		g.extUser = true
		return
	}
	if t.Name() == "" {
		if t.NumField() == 0 {
			return // struct{}
		}
		if _, ok := g.anonName[t]; !ok {
			g.anonName[t] = parent + field
		}
	} else if t.PkgPath() != modelPkg {
		panic("rsgen model: unexpected struct type " + t.String())
	}
	if g.seen[t] {
		return
	}
	g.seen[t] = true
	g.structs = append(g.structs, t)
	if _, ok := reflect.PointerTo(t).MethodByName("Clone"); ok && t.Name() != "" {
		g.hasClone[t] = true
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		g.walk(f.Type, g.structName(t), f.Name)
	}
}

func (g *modelGen) rustType(t reflect.Type) string {
	switch t {
	case ciStrType:
		return "CIStr"
	case fieldTypeType:
		return "FieldType"
	case userType:
		return "UserIdentity"
	case timeType:
		return "Time"
	case sqlModeType:
		return "SQLMode"
	}
	if (t.PkgPath() == modelPkg || t.PkgPath() == parserModelPkg) && t.Name() != "" && t.Kind() != reflect.Struct {
		return t.Name()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "bool"
	case reflect.Int:
		return "isize"
	case reflect.Int8:
		return "i8"
	case reflect.Int16:
		return "i16"
	case reflect.Int32:
		return "i32"
	case reflect.Int64:
		return "i64"
	case reflect.Uint:
		return "usize"
	case reflect.Uint8:
		return "u8"
	case reflect.Uint16:
		return "u16"
	case reflect.Uint32:
		return "u32"
	case reflect.Uint64:
		return "u64"
	case reflect.Float64:
		return "f64"
	case reflect.String:
		return "String"
	case reflect.Interface:
		if t.NumMethod() == 0 {
			return "AnyValue"
		}
	case reflect.Pointer:
		return "Option<Box<" + g.rustType(t.Elem()) + ">>"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "Option<Vec<u8>>"
		}
		e := t.Elem()
		if e.Kind() == reflect.Pointer {
			e = e.Elem() // []*T: elements held by value (nil elements decode as zero values)
		}
		return "Option<Vec<" + g.rustType(e) + ">>"
	case reflect.Map:
		return "Option<BTreeMap<" + g.rustType(t.Key()) + ", " + g.rustType(t.Elem()) + ">>"
	case reflect.Struct:
		if t.NumField() == 0 {
			return "()"
		}
		if g.isGenStruct(t) {
			return g.structName(t)
		}
	}
	panic("rsgen model: unsupported type " + t.String())
}

// basicKind is the underlying basic kind of a (possibly named) basic type.
func basicKind(t reflect.Type) reflect.Kind { return t.Kind() }

func isNamedBasic(t reflect.Type) bool {
	return ((t.PkgPath() == modelPkg || t.PkgPath() == parserModelPkg) && t.Name() != "" && t.Kind() != reflect.Struct) || t == sqlModeType
}

func intRange(k reflect.Kind) (string, string) {
	switch k {
	case reflect.Int:
		return "isize::MIN as i64", "isize::MAX as i64"
	case reflect.Int8:
		return "i8::MIN as i64", "i8::MAX as i64"
	case reflect.Int16:
		return "i16::MIN as i64", "i16::MAX as i64"
	case reflect.Int32:
		return "i32::MIN as i64", "i32::MAX as i64"
	case reflect.Int64:
		return "i64::MIN", "i64::MAX"
	}
	panic("not an int kind")
}

func uintMax(k reflect.Kind) string {
	switch k {
	case reflect.Uint:
		return "usize::MAX as u64"
	case reflect.Uint8:
		return "u8::MAX as u64"
	case reflect.Uint16:
		return "u16::MAX as u64"
	case reflect.Uint32:
		return "u32::MAX as u64"
	case reflect.Uint64:
		return "u64::MAX"
	}
	panic("not a uint kind")
}

var tmpN int

func tmp(p string) string { tmpN++; return fmt.Sprintf("%s%d", p, tmpN) }

// enc returns Rust statements writing the JSON of *ref (ref: &RustType) to `out`.
func (g *modelGen) enc(ref string, t reflect.Type) string {
	switch t {
	case ciStrType, fieldTypeType:
		return fmt.Sprintf("out.push_str(&%s.marshal_json());", ref)
	case userType:
		return fmt.Sprintf("encode_user_identity(out, %s);", ref)
	case timeType:
		return fmt.Sprintf("crate::gotime::encode_time(out, %s)?;", ref)
	}
	val := "*" + ref
	if isNamedBasic(t) {
		val = ref + ".0"
	}
	switch t.Kind() {
	case reflect.Bool:
		return fmt.Sprintf("out.push_str(if %s { \"true\" } else { \"false\" });", val)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("out.push_str(&(%s).to_string());", val)
	case reflect.Float64:
		return fmt.Sprintf("json::encode_float(out, %s).map_err(Error::plain)?;", val)
	case reflect.String:
		if isNamedBasic(t) {
			return fmt.Sprintf("json::append_string(out, &%s.0);", ref)
		}
		return fmt.Sprintf("json::append_string(out, %s);", ref)
	case reflect.Interface:
		return fmt.Sprintf("%s.encode_json(out)?;", ref)
	case reflect.Pointer:
		x := tmp("p")
		return fmt.Sprintf("match %s { None => out.push_str(\"null\"), Some(%s) => { let %s: &%s = %s; %s } }",
			ref, x, x, g.rustType(t.Elem()), x, g.enc(x, t.Elem()))
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			b := tmp("b")
			return fmt.Sprintf("match %s { None => out.push_str(\"null\"), Some(%s) => { out.push('\"'); out.push_str(&json::base64_std_encode(%s)); out.push('\"'); } }", ref, b, b)
		}
		a, i, x := tmp("a"), tmp("i"), tmp("x")
		e := t.Elem()
		if e.Kind() == reflect.Pointer {
			e = e.Elem()
		}
		return fmt.Sprintf("match %s { None => out.push_str(\"null\"), Some(%s) => { out.push('['); for (%s, %s) in %s.iter().enumerate() { if %s > 0 { out.push(','); } %s } out.push(']'); } }",
			ref, a, i, x, a, i, g.enc(x, e))
	case reflect.Map:
		m, kv, i, k, x := tmp("m"), tmp("kv"), tmp("i"), tmp("k"), tmp("x")
		ks := k + ".to_string()"
		if t.Key().Kind() == reflect.String {
			ks = k + ".clone()"
		}
		return fmt.Sprintf("match %s { None => out.push_str(\"null\"), Some(%s) => { let mut %s: Vec<(String, &%s)> = %s.iter().map(|(%s, %s)| (%s, %s)).collect(); %s.sort_by(|a, b| a.0.cmp(&b.0)); out.push('{'); for (%s, (%s, %s)) in %s.iter().enumerate() { if %s > 0 { out.push(','); } json::append_string(out, %s); out.push(':'); let %s: &%s = %s; %s } out.push('}'); } }",
			ref, m, kv, g.rustType(t.Elem()), m, k, x, ks, x, kv, i, k, x, kv, i, k, x, g.rustType(t.Elem()), x, g.enc(x, t.Elem()))
	case reflect.Struct:
		if t.NumField() == 0 {
			return fmt.Sprintf("{ let _ = %s; out.push_str(\"{}\"); }", ref)
		}
		return fmt.Sprintf("%s.encode_json(out)?;", ref)
	}
	panic("rsgen model: enc " + t.String())
}

// isEmpty: encoding/json.isEmptyValue for omitempty (ref: &RustType).
func (g *modelGen) isEmpty(ref string, t reflect.Type) string {
	if t == timeType || t == ciStrType || t == fieldTypeType || t == userType {
		return "false"
	}
	val := "*" + ref
	if isNamedBasic(t) {
		val = ref + ".0"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "!" + val
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return val + " == 0"
	case reflect.Float64:
		return val + " == 0.0"
	case reflect.String:
		if isNamedBasic(t) {
			return ref + ".0.is_empty()"
		}
		return ref + ".is_empty()"
	case reflect.Interface:
		return ref + ".is_nil()"
	case reflect.Pointer:
		return ref + ".is_none()"
	case reflect.Slice, reflect.Map:
		return ref + ".as_ref().is_none_or(|x| x.is_empty())"
	case reflect.Struct:
		return "false"
	}
	panic("rsgen model: isEmpty " + t.String())
}

// dec returns Rust statements decoding the Raw `v` into the place `place`.
func (g *modelGen) dec(place string, t reflect.Type) string {
	switch t {
	case ciStrType, fieldTypeType:
		return fmt.Sprintf("if let Err(e) = %s.decode_json(v) { d.fail(e); }", place)
	case userType:
		return fmt.Sprintf("decode_user_identity(d, v, &mut %s);", place)
	case timeType:
		return fmt.Sprintf("if let Err(e) = crate::gotime::decode_time(&mut %s, v) { d.fail(e); }", place)
	}
	val := place
	if isNamedBasic(t) {
		val = place + ".0"
	}
	ty := goTypeString(t)
	switch t.Kind() {
	case reflect.Bool:
		if isNamedBasic(t) {
			return fmt.Sprintf("{ let mut n = %s; d.bool_typed(v, &mut n, %q); %s = n; }", val, ty, val)
		}
		return fmt.Sprintf("d.bool(v, &mut %s);", place)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		lo, hi := intRange(t.Kind())
		if t.Kind() == reflect.Int64 {
			return fmt.Sprintf("d.int(v, &mut %s, %s, %s, %q);", val, lo, hi, ty)
		}
		return fmt.Sprintf("{ let mut n = %s as i64; d.int(v, &mut n, %s, %s, %q); %s = n as _; }", val, lo, hi, ty, val)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if t.Kind() == reflect.Uint64 {
			return fmt.Sprintf("d.uint(v, &mut %s, %s, %q);", val, uintMax(t.Kind()), ty)
		}
		return fmt.Sprintf("{ let mut n = %s as u64; d.uint(v, &mut n, %s, %q); %s = n as _; }", val, uintMax(t.Kind()), ty, val)
	case reflect.Float64:
		return fmt.Sprintf("d.float64(v, &mut %s);", place)
	case reflect.String:
		if isNamedBasic(t) {
			return fmt.Sprintf("{ let mut s = %s.0.to_string(); d.string_typed(v, &mut s, %q); %s.0 = s.into(); }", place, ty, place)
		}
		return fmt.Sprintf("d.string(v, &mut %s);", place)
	case reflect.Interface:
		return fmt.Sprintf("%s.decode_json(d, v);", place)
	case reflect.Pointer:
		x := tmp("p")
		if t.Elem() == userType {
			return fmt.Sprintf("match v { Raw::Null => %s = None, _ => { let %s = %s.get_or_insert_with(Default::default); decode_user_identity(d, v, %s); } }",
				place, x, place, x)
		}
		inner := "(**" + x + ")"
		if t.Elem().Kind() == reflect.Struct && t.Elem() != userType {
			inner = x // a method call: auto-deref
		}
		return fmt.Sprintf("match v { Raw::Null => %s = None, _ => { let %s = %s.get_or_insert_with(Default::default); %s } }",
			place, x, place, g.dec(inner, t.Elem()))
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return fmt.Sprintf("d.bytes(v, &mut %s, %q, %q);", place, ty, goTypeString(t.Elem()))
		}
		e := t.Elem()
		pre := ""
		if e.Kind() == reflect.Pointer {
			e = e.Elem()
			pre = "if let Raw::Null = v { *x = Default::default(); return; } "
		}
		return fmt.Sprintf("d.slice(v, &mut %s, %q, |d, v, x: &mut %s| { %s%s });", place, ty, g.rustType(e), pre, g.dec("(*x)", e))
	case reflect.Map:
		key := "|_, k| Some(k.to_owned())"
		if t.Key().Kind() != reflect.String {
			lo, hi := intRange(t.Key().Kind())
			key = fmt.Sprintf("|d, k| d.int_key(k, %s, %s, %q).map(|n| n as _)", lo, hi, goTypeString(t.Key()))
		}
		return fmt.Sprintf("d.map(v, &mut %s, %q, %s, |d, v, x: &mut %s| { %s });", place, ty, key, g.rustType(t.Elem()), g.dec("(*x)", t.Elem()))
	case reflect.Struct:
		if t.NumField() == 0 {
			return fmt.Sprintf("{ let _ = &%s; d.object(v, \"\", \"struct {}\", &[], |_, _, _| {}); }", place)
		}
		return fmt.Sprintf("%s.decode_json(d, v);", place)
	}
	panic("rsgen model: dec " + t.String())
}

func (g *modelGen) fieldPath(t reflect.Type, f jsonField) (guards []reflect.StructField, last reflect.StructField) {
	cur := t
	for k, i := range f.index {
		if cur.Kind() == reflect.Pointer {
			cur = cur.Elem()
		}
		sf := cur.Field(i)
		if k == len(f.index)-1 {
			return guards, sf
		}
		guards = append(guards, sf)
		cur = sf.Type
	}
	panic("empty index")
}

func (g *modelGen) emitStruct(e *emitter, t reflect.Type) {
	name := g.structName(t)
	derive := "Clone, Debug, Default, PartialEq"
	if g.hasClone[t] {
		derive = "Debug, Default, PartialEq" // Clone is the hand port of Go's Clone method
	}
	if t.Name() != "" {
		e.p("\n/// Go `%s`.\n", t.String())
	} else {
		e.p("\n/// The anonymous struct type of Go `%s` (`%s`).\n", name, t.String())
	}
	e.p("#[derive(%s)]\npub struct %s {\n", derive, name)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag != "" {
			e.p("    /// json: `%s`\n", tag)
		}
		e.p("    pub %s: %s,\n", msnake(f.Name), g.rustType(f.Type))
		g.collectLeaves(f.Type)
	}
	e.p("}\n")

	// GoCopy: Go's struct copy `*x`.
	e.p("\nimpl GoCopy for %s {\n    // glue: Go struct copy of %s (generated)\n    fn go_copy(&self) -> Self {\n        Self {\n", name, t.String())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		e.p("            %s: self.%s.go_copy(),\n", msnake(f.Name), msnake(f.Name))
	}
	e.p("        }\n    }\n}\n")

	fields := typeFields(t)
	names := make([]string, len(fields))
	for i, f := range fields {
		if f.quoted {
			panic("rsgen model: ,string fields are not supported: " + t.String() + "." + f.name)
		}
		names[i] = fmt.Sprintf("%q", f.name)
	}
	e.p("\nimpl %s {\n", name)
	e.p("    /// The encoding/json field names (`cachedTypeFields`), in encoding order.\n")
	e.p("    pub const JSON_FIELDS: &'static [&'static str] = &[%s];\n\n", strings.Join(names, ", "))
	// encode
	e.p("    /// `json.Marshal` of the struct (encoding/json's struct encoder).\n")
	e.p("    // glue: encoding/json structEncoder of %s (generated)\n", t.String())
	if len(fields) == 0 {
		e.p("    pub fn encode_json(&self, out: &mut String) -> Result<(), Error> {\n        out.push_str(\"{}\");\n        Ok(())\n    }\n\n")
	} else {
		e.p("    pub fn encode_json(&self, out: &mut String) -> Result<(), Error> {\n        let mut first = true;\n        out.push('{');\n")
	}
	for _, f := range fields {
		guards, last := g.fieldPath(t, f)
		open, close := "", ""
		base := "self"
		for _, gf := range guards {
			x := tmp("e")
			if gf.Type.Kind() == reflect.Pointer {
				open += fmt.Sprintf("if let Some(%s) = &%s.%s { ", x, base, msnake(gf.Name))
				close += "} "
			} else {
				open += fmt.Sprintf("{ let %s = &%s.%s; ", x, base, msnake(gf.Name))
				close += "} "
			}
			base = x
		}
		v := tmp("v")
		cond := ""
		if f.omitEmpty {
			cond = fmt.Sprintf("if !(%s) ", g.isEmpty(v, last.Type))
		}
		e.p("        %s{ let %s = &%s.%s; %s{ if !first { out.push(','); } first = false; out.push_str(%s); %s } } %s\n",
			open, v, base, msnake(last.Name), cond, rustStr(`"`+f.name+`":`), g.enc(v, last.Type), close)
	}
	if len(fields) > 0 {
		e.p("        let _ = first;\n        out.push('}');\n        Ok(())\n    }\n\n")
	}
	// decode
	e.p("    /// `json.Unmarshal` into the struct (encoding/json's object decoder): decodes into the\n    /// existing value, missing members leave fields unchanged.\n")
	e.p("    // glue: encoding/json decodeState.object for %s (generated)\n", t.String())
	sname := t.Name()
	if sname == "" {
		sname = ""
	}
	// Promoted fields: the Go names of the embedded fields on the way (errorContext.FieldStack).
	paths := make([]string, len(fields))
	promoted := false
	for i, f := range fields {
		guards, _ := g.fieldPath(t, f)
		var ns []string
		for _, gf := range guards {
			ns = append(ns, fmt.Sprintf("%q", gf.Name))
		}
		promoted = promoted || len(ns) > 0
		paths[i] = "&[" + strings.Join(ns, ", ") + "]"
	}
	e.p("    pub fn decode_json(&mut self, d: &mut Decoder, v: &Raw) {\n")
	if len(fields) == 0 {
		e.p("        d.object(v, %q, %q, Self::JSON_FIELDS, |_, _, _| {});\n    }\n\n", sname, t.String())
	} else if len(fields) == 1 {
		guards, last := g.fieldPath(t, fields[0])
		if len(guards) > 0 {
			panic("rsgen model: single promoted field")
		}
		e.p("        d.object(v, %q, %q, Self::JSON_FIELDS, |d, i, v| {\n            if i == 0 { %s }\n        });\n    }\n\n", sname, t.String(), g.dec("self."+msnake(last.Name), last.Type))
	}
	if len(fields) <= 1 {
		// done above
	} else if promoted {
		e.p("        const PATHS: &[&[&str]] = &[%s];\n", strings.Join(paths, ", "))
		e.p("        d.object_promoted(v, %q, %q, Self::JSON_FIELDS, PATHS, |d, i, v| match i {\n", sname, t.String())
	} else {
		e.p("        d.object(v, %q, %q, Self::JSON_FIELDS, |d, i, v| match i {\n", sname, t.String())
	}
	for i, f := range fields {
		guards, last := g.fieldPath(t, f)
		base := "self"
		pre := ""
		for _, gf := range guards {
			x := tmp("e")
			if gf.Type.Kind() == reflect.Pointer {
				pre += fmt.Sprintf("let %s = %s.%s.get_or_insert_with(Default::default); ", x, base, msnake(gf.Name))
			} else {
				pre += fmt.Sprintf("let %s = &mut %s.%s; ", x, base, msnake(gf.Name))
			}
			base = x // field access: auto-deref
		}
		if len(fields) <= 1 {
			break
		}
		e.p("            %d => { %s%s }\n", i, pre, g.dec(base+"."+msnake(last.Name), last.Type))
	}
	if len(fields) > 1 {
		e.p("            _ => {}\n        });\n    }\n\n")
	}
	if g.hasClone[t] {
		e.p("    /// Go's struct copy `*x` (every field copied as is, without the Go `Clone` methods of\n    /// the field types; Rust has no shared pointers to keep), the starting point of the\n    /// hand-ported `Clone`.\n    // glue: Go struct copy of %s (generated)\n    pub(crate) fn struct_copy(&self) -> Self {\n        self.go_copy()\n    }\n\n", t.String())
	}
	e.p("    /// `json.Marshal(x)`.\n    // glue: json.Marshal of %s (generated)\n    pub fn marshal_json(&self) -> Result<String, Error> {\n        let mut out = String::new();\n        self.encode_json(&mut out)?;\n        Ok(out)\n    }\n\n", t.String())
	e.p("    /// `json.Unmarshal(data, x)`: a syntax error changes nothing; otherwise the first error.\n    // glue: json.Unmarshal into %s (generated)\n    pub fn unmarshal_json(&mut self, data: &[u8]) -> Result<(), Error> {\n        let v = json::unmarshal_raw(data).map_err(Error::plain)?;\n        let mut d = Decoder::new();\n        self.decode_json(&mut d, &v);\n        d.finish().map_err(|e| Error::plain(e.to_string()))\n    }\n}\n", t.String())
}

// namedBasics returns the named basic types declared in the model package AST,
// with their underlying Rust type, per file.
func modelNamedTypes(p *pkg, file string) []struct{ name, rt string } {
	var out []struct{ name, rt string }
	for _, d := range p.files[file].Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, s := range gd.Specs {
			ts := s.(*ast.TypeSpec)
			obj := p.info.Defs[ts.Name]
			if obj == nil {
				continue
			}
			b, ok := obj.Type().Underlying().(*types.Basic)
			if !ok {
				continue
			}
			rt := rustType(b, "isize")
			if rt == "&str" {
				rt = "String"
			}
			out = append(out, struct{ name, rt string }{ts.Name.Name, rt})
		}
	}
	return out
}

func genModel(modelDir string) {
	g := &modelGen{seen: map[reflect.Type]bool{}, anonName: map[reflect.Type]string{},
		named: map[reflect.Type]bool{}, hasClone: map[reflect.Type]bool{}, leaves: map[string]bool{}}
	for _, v := range []any{model.DBInfo{}, model.TableInfo{}, model.SchemaDiff{}, model.PolicyInfo{},
		model.ResourceGroupInfo{}, model.TableItemID{}, model.StatsLoadItem{}} {
		g.walk(reflect.TypeOf(v), "", "")
	}
	p := load(modelDir)
	files := []string{"db.go", "table.go", "column.go", "index.go", "placement.go", "flags.go", "resource_group.go", "job.go", "reorg.go", "bdr.go"}

	// Constants and named types.
	c := &emitter{}
	var goFiles []string
	for _, f := range files {
		goFiles = append(goFiles, "pkg/meta/model/"+f)
	}
	domainName = "model"
	c.header(goFiles...)
	c.p("use std::borrow::Cow;\n\n#[allow(unused_imports)]\nuse tidb_base::gostd::time::Duration;\n")
	for _, f := range files {
		c.p("\n// ---------------------------------------------------------------- %s\n", f)
		for _, nt := range modelNamedTypes(p, f) {
			newtypes[nt.name] = true
			if nt.rt == "String" {
				stringNewtypes[nt.name] = true
				c.p("\n/// Go `model.%s` (a Go string type; `Cow` so that its constants are `const`).\n#[derive(Clone, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]\npub struct %s(pub Cow<'static, str>);\n", nt.name, nt.name)
				continue
			}
			c.p("\n/// Go `model.%s`.\n#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]\npub struct %s(pub %s);\n", nt.name, nt.name, nt.rt)
		}
		constBlock(c, p, f, "isize", modelConstOverride, modelConstSkip)
	}
	// unsafe.Sizeof constants: go/types cannot size the stubbed imported types; take the run-time values.
	for name, v := range map[string]int64{"EmptyColumnInfoSize": model.EmptyColumnInfoSize} {
		re := regexp.MustCompile(`(const ` + name + `: i64 = )-?\d+;`)
		if !re.Match(c.buf.Bytes()) {
			panic("rsgen model: constant " + name + " not found")
		}
		c.buf = *bytes.NewBuffer(re.ReplaceAll(c.buf.Bytes(), []byte(fmt.Sprintf("${1}%d; // unsafe.Sizeof at run time", v))))
	}
	c.write("consts_gen.rs")

	e := &emitter{}
	e.header(goFiles...)
	e.p("use std::collections::BTreeMap;\n\nuse tidb_base::Error;\nuse tidb_base::gostd::json::{self, Decoder, Raw};\nuse tidb_base::gostd::time::Time;\nuse tidb_parser::auth::UserIdentity;\nuse tidb_parser::model::{CIStr, ColumnChoice, IndexType, PartitionType, RunawayActionType, RunawayWatchType, TableLockType, ViewAlgorithm, ViewCheckOption, ViewSecurity};\nuse tidb_parser::types::FieldType;\n\nuse crate::any::AnyValue;\nuse crate::consts_gen::*;\nuse crate::gocopy::{GoCopy, go_copy_leaf};\n")
	for _, t := range g.structs {
		g.emitStruct(e, t)
	}
	if g.extUser {
		emitUser(e, g)
	}
	var leaves []string
	for l := range g.leaves {
		leaves = append(leaves, l)
	}
	sort.Strings(leaves)
	e.p("\ngo_copy_leaf!(%s);\n", strings.Join(leaves, ", "))
	e.write("types_gen.rs")
	fmt.Fprintf(os.Stderr, "  %d model structs\n", len(g.structs))
}

// emitUser: auth.UserIdentity has no JSON methods: the default struct encoding.
func emitUser(e *emitter, g *modelGen) {
	t := userType
	fields := typeFields(t)
	var names []string
	for _, f := range fields {
		names = append(names, fmt.Sprintf("%q", f.name))
	}
	e.p("\n/// The encoding/json field names of Go `auth.UserIdentity`.\nconst USER_IDENTITY_FIELDS: &[&str] = &[%s];\n", strings.Join(names, ", "))
	e.p("\n/// `json.Marshal` of an `auth.UserIdentity` (no JSON methods: the struct encoding).\n// glue: encoding/json structEncoder of auth.UserIdentity (generated)\npub(crate) fn encode_user_identity(out: &mut String, u: &UserIdentity) {\n    out.push('{');\n")
	for i, f := range fields {
		comma := ","
		if i == 0 {
			comma = ""
		}
		v := "&u." + msnake(t.Field(f.index[0]).Name)
		e.p("    out.push_str(%s);\n    { let v = %s; %s }\n", rustStr(comma+`"`+f.name+`":`), v, strings.ReplaceAll(g.enc("v", f.typ), "?", ""))
	}
	e.p("    out.push('}');\n}\n")
	e.p("\n/// `json.Unmarshal` into an `auth.UserIdentity`.\n// glue: encoding/json decodeState.object for auth.UserIdentity (generated)\npub(crate) fn decode_user_identity(d: &mut Decoder, v: &Raw, u: &mut UserIdentity) {\n    d.object(v, \"UserIdentity\", \"auth.UserIdentity\", USER_IDENTITY_FIELDS, |d, i, v| match i {\n")
	for i, f := range fields {
		e.p("        %d => { %s }\n", i, g.dec("u."+msnake(t.Field(f.index[0]).Name), f.typ))
	}
	e.p("        _ => {}\n    });\n}\n")
}

var modelConstOverride = map[string]string{}

// stringNewtypes: named string types (their constants wrap a Cow::Borrowed).
var stringNewtypes = map[string]bool{}
var modelConstSkip = map[string]bool{}
