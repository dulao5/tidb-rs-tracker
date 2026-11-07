package main

// Tables that only exist at run time in Go: the error registry filled by every
// package's init (terror.ErrClassToMySQLCodes) and the error prototypes of
// util/dbterror. This file links the tidb-server package set so the registry
// is the one tidb-server freezes with terror.RegisterFinish.

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/pingcap/errors"

	"github.com/dulao5/tidb-rs-tracker/govec/internal/protos"
)

func genRegistry() {
	e := &emitter{}
	e.header("pkg/parser/terror/terror.go")
	e.p("use crate::errors::ErrCode;\nuse crate::gomap::GoMap;\nuse crate::terror::ErrClass;\n\n")
	e.p("/// ErrClassToMySQLCodes is the map of ErrClass to code-set.\n///\n")
	e.p("/// Generated from the registry of a process linking the package set of Go\n")
	e.p("/// `cmd/tidb-server` v8.5.8 after init (what `terror.RegisterFinish` freezes).\n")
	e.p("pub static ErrClassToMySQLCodes: GoMap<ErrClass, &[ErrCode]> = GoMap::new(&[\n")
	n := 0
	for _, cc := range protos.RegistrySnapshot() {
		codes := cc[1].([]int)
		var parts []string
		for _, c := range codes {
			parts = append(parts, fmt.Sprint(c))
		}
		n += len(codes)
		e.p("    (ErrClass(%d), &[%s]),\n", cc[0].(int), strings.Join(parts, ", "))
	}
	e.p("]);\n")
	e.write("terror_registry.rs")
	fmt.Fprintf(os.Stderr, "  %d codes\n", n)
}

// redactPos reads the unexported redactArgsPos of a prototype.
func redactPos(e *errors.Error) []int {
	f := reflect.ValueOf(e).Elem().FieldByName("redactArgsPos")
	var out []int
	for i := 0; i < f.Len(); i++ {
		out = append(out, int(f.Index(i).Int()))
	}
	return out
}

// rustPkgPath maps the Go import names used in util/dbterror to Rust paths.
var rustPkgPath = map[string]string{"mysql": "errno", "errno": "errno", "parser_mysql": "mysql"}

func genProtos(tidbDir string) {
	type pf struct{ dir, file, rs, goPath string }
	for _, f := range []pf{
		{"pkg/util/dbterror", "ddl_terror.go", "dbterror/ddl_terror.rs", "pkg/util/dbterror/ddl_terror.go"},
		{"pkg/util/dbterror/exeerrors", "errors.go", "dbterror/exeerrors.rs", "pkg/util/dbterror/exeerrors/errors.go"},
		{"pkg/util/dbterror/plannererrors", "planner_terror.go", "dbterror/plannererrors.rs", "pkg/util/dbterror/plannererrors/planner_terror.go"},
	} {
		p := load(filepath.Join(tidbDir, f.dir))
		e := &emitter{}
		e.header(f.goPath)
		e.p("use std::sync::LazyLock;\n\n#[allow(unused_imports)]\nuse crate::dbterror::*;\nuse crate::errors::Error;\nuse crate::mysql::ErrMessage;\n#[allow(unused_imports)]\nuse crate::{errno, mysql};\n")
		n := 0
		var names []string
		for _, d := range p.files[f.file].Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				for i, id := range vs.Names {
					if !isProto(vs.Values[i]) {
						fmt.Fprintf(os.Stderr, "  skipped %s (not an error prototype)\n", id.Name)
						continue
					}
					call := vs.Values[i].(*ast.CallExpr)
					sel := call.Fun.(*ast.SelectorExpr)
					class := ""
					switch x := sel.X.(type) {
					case *ast.Ident:
						class = x.Name
					case *ast.SelectorExpr:
						class = x.Sel.Name
					}
					key := f.goPath + ":" + id.Name
					proto, ok := protos.Vars[key]
					if !ok {
						panic("rsgen: prototype " + key + " missing from internal/protos/protos_list.go; run rsgen -protos")
					}
					code := codeExpr(call.Args[0], int(proto.Code()))
					e.p("\n")
					rustDoc(e, "", vs.Doc)
					switch sel.Sel.Name {
					case "NewStd":
						e.p("pub static %s: LazyLock<Error> = LazyLock::new(|| %s.new_std(%s));\n", id.Name, class, code)
					case "NewStdErr":
						pos := redactPos(proto)
						var ps []string
						for _, x := range pos {
							ps = append(ps, fmt.Sprint(x))
						}
						e.p("pub static %s: LazyLock<Error> = LazyLock::new(|| {\n    %s.new_std_err(%s, &ErrMessage::new(%s, &[%s]))\n});\n",
							id.Name, class, code, rustStr(proto.MessageTemplate()), strings.Join(ps, ", "))
					default:
						panic("rsgen: prototype " + key + ": unsupported constructor " + sel.Sel.Name)
					}
					n++
					names = append(names, id.Name)
				}
			}
		}
		e.p("\n/// Every prototype of this file by Go name, in source order (for tests and tools).\n")
		e.p("pub static ALL: &[(&str, &LazyLock<Error>)] = &[\n")
		for _, nm := range names {
			e.p("    (%q, &%s),\n", nm, nm)
		}
		e.p("];\n")
		e.write(f.rs)
		fmt.Fprintf(os.Stderr, "  %d prototypes\n", n)
	}
}

// codeExpr renders the code argument: the constant's name when it is a
// selector on a known package, else the literal value.
func codeExpr(x ast.Expr, v int) string {
	if sel, ok := x.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			if rp, ok := rustPkgPath[id.Name]; ok {
				return rp + "::" + sel.Sel.Name
			}
		}
	}
	return fmt.Sprint(v)
}

// genUnicode emits the Go unicode range tables that parser/mysql uses
// (RangeGraph, unicode.IsDigit) as Rust data for gostd::unicode.
func genUnicode() {
	e := &emitter{}
	e.header("$GOROOT/src/unicode/tables.go")
	e.p("use super::{Range16, Range32, RangeTable};\n")
	names := []string{"Nd", "No", "Mn", "Me", "Mc", "Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po", "Sm", "Sc", "Sk", "So", "Lu", "Lt", "Nl", "Ll", "Lm", "Lo"}
	n := 0
	for _, name := range names {
		t := unicode.Categories[name]
		e.p("\n/// `unicode.%s` (Go %s).\npub static %s: RangeTable = RangeTable {\n    r16: &[\n", name, runtime.Version(), name)
		for _, r := range t.R16 {
			e.p("        Range16 { lo: 0x%04x, hi: 0x%04x, stride: %d },\n", r.Lo, r.Hi, r.Stride)
			n++
		}
		e.p("    ],\n    r32: &[\n")
		for _, r := range t.R32 {
			e.p("        Range32 { lo: 0x%x, hi: 0x%x, stride: %d },\n", r.Lo, r.Hi, r.Stride)
			n++
		}
		e.p("    ],\n    latin_offset: %d,\n};\n", t.LatinOffset)
	}
	// strconv.IsPrint as ranges of printable runes (strconv's own tables are
	// unexported; the ranges are computed by calling IsPrint on every rune).
	e.p("\n/// Runes for which Go's `strconv.IsPrint` is true, as inclusive ranges (Go %s).\n", runtime.Version())
	e.p("pub static IS_PRINT: &[(u32, u32)] = &[\n")
	start := -1
	for r := 0; r <= unicode.MaxRune+1; r++ {
		p := r <= unicode.MaxRune && strconv.IsPrint(rune(r))
		if p && start < 0 {
			start = r
		} else if !p && start >= 0 {
			e.p("    (0x%x, 0x%x),\n", start, r-1)
			n++
			start = -1
		}
	}
	e.p("];\n")
	e.write("gostd/unicode_tables.rs")
	fmt.Fprintf(os.Stderr, "  %d ranges\n", n)
}
