package main

// Config types of block D00.2 part 2: pkg/config.Config and the structs it
// contains (from client-go, logutil, pingcap/log), and client-go's
// config.Config for GetTiKVConfig. Generated from reflection (field names,
// toml/json tags, kinds) and the run-time defaults of config.NewConfig().
// The Rust side gets plain structs plus a small reflection interface
// (GoStruct) that the ported TOML decoder (BurntSushi), JSON encoding and
// MergeConfigItems walk; see tidb-rs crates/base/src/config/reflect.rs.

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/pingcap/tidb/pkg/config"
	tikvcfg "github.com/tikv/client-go/v2/config"
)

var durationType = reflect.TypeOf(time0())

// rustTypeName: Rust name of a Go struct type.
func rustTypeName(t reflect.Type) string {
	switch t.PkgPath() {
	case "github.com/pingcap/tidb/pkg/config":
		return strings.ToUpper(t.Name()[:1]) + t.Name()[1:]
	case "github.com/tikv/client-go/v2/config":
		return "Tikv" + t.Name()
	case "github.com/pingcap/tidb/pkg/util/logutil":
		return "Logutil" + t.Name()
	case "github.com/pingcap/log":
		return "Zaplog" + t.Name()
	}
	panic("rsgen: unexpected struct package " + t.PkgPath())
}

var rustKeywords = map[string]bool{"type": true, "match": true, "loop": true, "use": true, "ref": true, "move": true}

// snakeOverrides names the fields whose acronyms the generic rule splits badly.
var snakeOverrides = map[string]string{
	"SSLCA":                        "ssl_ca",
	"ClusterSSLCA":                 "cluster_ssl_ca",
	"RecordQPSbyDB":                "record_qps_by_db",
	"EnablePProfSQLCPU":            "enable_pprof_sql_cpu",
	"Enable1PC":                    "enable_1pc",
	"Enable32BitsConnectionID":     "enable_32bits_connection_id",
	"TreatOldVersionUTF8AsUTF8MB4": "treat_old_version_utf8_as_utf8mb4",
}

func snake(s string) string {
	if o, ok := snakeOverrides[s]; ok {
		return o
	}
	for _, w := range []string{"TiDB", "TiKV", "TiFlash"} {
		s = strings.ReplaceAll(s, w, w[:1]+strings.ToLower(w[1:]))
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i > 0 && i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])
			if prevLower || nextLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if rustKeywords[out] {
		out += "_"
	}
	return out
}

func tagName(tag string) (name string, skip bool) {
	if tag == "-" {
		return "", true
	}
	name = strings.Split(tag, ",")[0]
	return name, false
}

// fieldKind returns the reflection kind name (ValRef variant) and Rust type.
func fieldKind(t reflect.Type) (string, string) {
	if t == durationType {
		return "Duration", "Duration"
	}
	switch t.String() {
	case "config.nullableBool":
		return "NullableBool", "NullableBool"
	case "config.AtomicBool":
		return "AtomicBool", "AtomicBool"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "Bool", "bool"
	case reflect.Int:
		return "Int", "isize"
	case reflect.Int32:
		return "I32", "i32"
	case reflect.Int64:
		return "I64", "i64"
	case reflect.Uint:
		return "Uint", "usize"
	case reflect.Uint32:
		return "U32", "u32"
	case reflect.Uint64:
		return "U64", "u64"
	case reflect.Float64:
		return "F64", "f64"
	case reflect.String:
		return "Str", "String"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return "StrSlice", "Option<Vec<String>>"
		}
	case reflect.Map:
		if t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String {
			return "StrMap", "Option<BTreeMap<String, String>>"
		}
	case reflect.Struct:
		return "Struct", rustTypeName(t)
	}
	panic("rsgen: unsupported config field type " + t.String())
}

func genConfigTypes() {
	e := &emitter{}
	e.header("pkg/config/config.go")
	e.p("use std::collections::BTreeMap;\n\nuse super::reflect::{AtomicBool, Duration, FieldDesc, GoStruct, NullableBool, ValMut, ValRef};\n")
	var order []reflect.Type
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		if seen[t] || t.String() == "config.nullableBool" || t.String() == "config.AtomicBool" || t == durationType {
			return
		}
		seen[t] = true
		order = append(order, t)
		for i := 0; i < t.NumField(); i++ {
			ft := t.Field(i).Type
			if ft.Kind() == reflect.Struct {
				walk(ft)
			}
		}
	}
	walk(reflect.TypeOf(config.Config{}))
	walk(reflect.TypeOf(tikvcfg.Config{}))
	for _, t := range order {
		e.p("\n/// Go `%s` (%s).\n#[derive(Clone, Debug, Default, PartialEq)]\npub struct %s {\n", t.String(), t.PkgPath(), rustTypeName(t))
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			_, rt := fieldKind(f.Type)
			e.p("    pub %s: %s,\n", snake(f.Name), rt)
		}
		e.p("}\n")
		name := rustTypeName(t)
		e.p("\nimpl %s {\n    /// Fields in Go order (reflection metadata).\n    pub const FIELDS: &'static [FieldDesc] = &[\n", name)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tn, tskip := tagName(f.Tag.Get("toml"))
			jn, jskip := tagName(f.Tag.Get("json"))
			tomlS, jsonS := "None", "None"
			if !tskip {
				if tn == "" {
					tn = f.Name
				}
				tomlS = fmt.Sprintf("Some(%q)", tn)
			}
			if !jskip {
				if jn == "" {
					jn = f.Name
				}
				jsonS = fmt.Sprintf("Some(%q)", jn)
			}
			tagged := f.Tag.Get("toml") != "" && !tskip
			e.p("        FieldDesc { go_name: %q, toml: %s, toml_tagged: %v, json: %s, embedded: %v },\n", f.Name, tomlS, tagged, jsonS, f.Anonymous)
		}
		e.p("    ];\n}\n")
		e.p("\nimpl GoStruct for %s {\n    // glue: reflect.Type.String (generated)\n    fn go_type(&self) -> &'static str {\n        %q\n    }\n    // glue: reflect struct fields (generated)\n    fn fields(&self) -> &'static [FieldDesc] {\n        Self::FIELDS\n    }\n", name, t.String())
		for _, m := range []string{"field", "field_mut"} {
			if m == "field" {
				e.p("    // glue: reflect.Value.Field (generated)\n    fn field(&self, i: usize) -> ValRef<'_> {\n        match i {\n")
			} else {
				e.p("    // glue: reflect.Value.Field, settable (generated)\n    fn field_mut(&mut self, i: usize) -> ValMut<'_> {\n        match i {\n")
			}
			ref := "&"
			if m == "field_mut" {
				ref = "&mut "
			}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				k, _ := fieldKind(f.Type)
				variant := "ValRef"
				if m == "field_mut" {
					variant = "ValMut"
				}
				e.p("            %d => %s::%s(%sself.%s),\n", i, variant, k, ref, snake(f.Name))
			}
			e.p("            _ => panic!(\"field index out of range\"),\n        }\n    }\n")
		}
		e.p("}\n")
	}
	e.write("config/types_gen.rs")
	fmt.Fprintf(os.Stderr, "  %d config types\n", len(order))

	// Defaults.
	d := &emitter{}
	d.header("pkg/config/config.go")
	d.p("use std::collections::BTreeMap;\n\nuse super::reflect::{AtomicBool, Duration, NullableBool};\nuse super::types_gen::*;\n\n")
	d.p("/// `defaultConf` (run-time value of Go `config.NewConfig()` at v8.5.8). `TempStoragePath` is\n/// computed at start-up from the environment in Go (`tempStorageDirName`), here too.\n")
	d.p("// glue: defaultConf (generated from the run-time value)\npub(crate) fn default_conf() -> Config {\n    %s\n}\n", rustValue2(reflect.ValueOf(*config.NewConfig()), "    ", "Config"))
	d.p("\n/// `tikvcfg.DefaultConfig()` (client-go), the default of the global client-go config.\n// glue: tikvcfg.DefaultConfig (generated from the run-time value)\npub(crate) fn tikv_default_config() -> TikvConfig {\n    %s\n}\n", rustValue2(reflect.ValueOf(tikvcfg.DefaultConfig()), "    ", ""))
	d.write("config/defaults_gen.rs")
}

func time0() any { return config.OpenTracingSampler{}.SamplingRefreshInterval }

// rustValue2 renders a Go value as a Rust expression.
func rustValue2(v reflect.Value, ind, path string) string {
	t := v.Type()
	if t == durationType {
		return fmt.Sprintf("Duration(%d)", v.Int())
	}
	switch t.String() {
	case "config.nullableBool":
		return fmt.Sprintf("NullableBool { is_valid: %v, is_true: %v }", v.Field(0).Bool(), v.Field(1).Bool())
	case "config.AtomicBool":
		b := v.Interface().(config.AtomicBool)
		return fmt.Sprintf("AtomicBool::new(%v)", b.Load())
	}
	switch t.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float64:
		s := strconv.FormatFloat(v.Float(), 'g', -1, 64)
		if !strings.ContainsAny(s, ".eEn") {
			s += ".0"
		}
		return s
	case reflect.String:
		if path == "Config.TempStoragePath" {
			return "super::temp_storage_dir_name()"
		}
		return rustStr(v.String()) + ".to_owned()"
	case reflect.Slice:
		if v.IsNil() {
			return "None"
		}
		var parts []string
		for i := 0; i < v.Len(); i++ {
			parts = append(parts, rustStr(v.Index(i).String())+".to_owned()")
		}
		return "Some(vec![" + strings.Join(parts, ", ") + "])"
	case reflect.Map:
		if v.IsNil() {
			return "None"
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("(%s.to_owned(), %s.to_owned())", rustStr(k.String()), rustStr(v.MapIndex(k).String())))
		}
		return "Some(BTreeMap::from([" + strings.Join(parts, ", ") + "]))"
	case reflect.Struct:
		var b strings.Builder
		fmt.Fprintf(&b, "%s {\n", rustTypeName(t))
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			p := ""
			if path != "" {
				p = path + "." + f.Name
			}
			fmt.Fprintf(&b, "%s    %s: %s,\n", ind, snake(f.Name), rustValue2(v.Field(i), ind+"    ", p))
		}
		fmt.Fprintf(&b, "%s}", ind)
		return b.String()
	}
	panic("rsgen: unsupported default value kind " + t.String())
}
