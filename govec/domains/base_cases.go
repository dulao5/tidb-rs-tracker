package domains

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/internal/protos"
	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/errno"
	"github.com/pingcap/tidb/pkg/parser/mysql"
)

var verbRe = regexp.MustCompile(`%[-+# 0]*(\*|[0-9]+)?(\.(\*|[0-9]+)?)?[a-zA-Z%]`)

var redactModes = []string{"OFF", "ON", "MARKER"}

// sample strings: ASCII, long, multibyte, redaction markers, format chars.
var sampleStrings = []string{
	"", "t1", "Duplicate", "a'b\"c\\d", strings.Repeat("x", 300), "日本語テーブル名", "‹marker›", "100%", "tab\tnl\n",
	strings.Repeat("é", 70), "\x7f\x01", "emoji😀",
	// not UTF-8 (Go strings are bytes: CAST(0xFF41 AS SIGNED) passes them to messages)
	"\xffA", "ab\xdd$", "\xe4\xb8", strings.Repeat("\xc3", 200), "\xef\xbb\xbf\u2028\ufeff",
}

func randStr(rng *rand.Rand) string {
	if rng.IntN(3) == 0 {
		return sampleStrings[rng.IntN(len(sampleStrings))]
	}
	n := rng.IntN(40)
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch rng.IntN(7) {
		case 0:
			b.WriteRune(rune(0x4e00 + rng.IntN(500)))
		case 1:
			b.WriteByte(byte(0x80 + rng.IntN(0x80))) // possibly invalid UTF-8
		default:
			b.WriteByte(byte('a' + rng.IntN(26)))
		}
	}
	return b.String()
}

func randInt(rng *rand.Rand) typedArg {
	vals := []int64{0, 1, -1, 42, 1062, -9223372036854775808, 9223372036854775807, 65535, 1 << 33}
	v := vals[rng.IntN(len(vals))]
	if rng.IntN(2) == 0 {
		v = rng.Int64N(1<<40) - (1 << 39)
	}
	switch rng.IntN(5) {
	case 0:
		return ta("int64", v)
	case 1:
		return ta("uint64", uint64(v))
	case 2:
		return ta("int32", int32(v))
	case 3:
		return ta("uint16", uint16(v))
	}
	return ta("int", v)
}

// randAny: an argument of any supported dynamic type.
func randAny(rng *rand.Rand) typedArg {
	switch rng.IntN(9) {
	case 0, 1, 2:
		return ta("string", randStr(rng))
	case 3, 4:
		return randInt(rng)
	case 5:
		fs := []float64{0, 1.5, -2.25, 1e21, 1e20, 123456789, 0.0001, 0.00001, 3.141592653589793, 1e-7, 100}
		return ta("float64", strconv.FormatFloat(fs[rng.IntN(len(fs))], 'g', -1, 64))
	case 6:
		return ta("bool", rng.IntN(2) == 0)
	case 7:
		return ta("bytes", hex.EncodeToString([]byte(randStr(rng))))
	}
	if rng.IntN(2) == 0 {
		return ta("nil", nil)
	}
	return ta("error", "boom: "+randStr(rng))
}

// argsFor builds arguments matching the verbs of a template (and sometimes
// one too few / too many, or a mismatched type).
func argsFor(rng *rand.Rand, tmpl string) []typedArg {
	var out []typedArg
	for _, v := range verbRe.FindAllString(tmpl, -1) {
		verb := v[len(v)-1]
		if verb == '%' {
			continue
		}
		if strings.Contains(v, "*") {
			for range strings.Count(v, "*") {
				out = append(out, ta("int", rng.IntN(10)))
			}
		}
		switch verb {
		case 's':
			if rng.IntN(8) == 0 {
				out = append(out, randAny(rng))
			} else {
				out = append(out, ta("string", randStr(rng)))
			}
		case 'd':
			if rng.IntN(8) == 0 {
				out = append(out, randAny(rng))
			} else {
				out = append(out, randInt(rng))
			}
		default:
			out = append(out, randAny(rng))
		}
	}
	switch rng.IntN(12) {
	case 0:
		if len(out) > 0 {
			out = out[:len(out)-1]
		}
	case 1:
		out = append(out, randAny(rng))
	}
	if out == nil {
		out = []typedArg{}
	}
	return out
}

func sortedCodes[V any](m map[uint16]V) []uint16 {
	var ks []uint16
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

func baseGenerate(g *vec.Gen, rng *rand.Rand) {
	// Tables.
	var tables []string
	for k := range baseTables {
		tables = append(tables, k)
	}
	sort.Strings(tables)
	for _, t := range tables {
		g.Add("sys/table", "table", t)
	}

	// fmt: every message template of both tables and of the prototypes.
	seen := map[string]bool{}
	var tmpls []string
	addT := func(t string) {
		if !seen[t] {
			seen[t] = true
			tmpls = append(tmpls, t)
		}
	}
	for _, c := range sortedCodes(mysql.MySQLErrName) {
		addT(mysql.MySQLErrName[c].Raw)
	}
	for _, c := range sortedCodes(errno.MySQLErrName) {
		addT(errno.MySQLErrName[c].Raw)
	}
	var pk []string
	for k := range protos.Vars {
		pk = append(pk, k)
	}
	sort.Strings(pk)
	for _, k := range pk {
		addT(protos.Vars[k].MessageTemplate())
	}
	for _, t := range tmpls {
		g.Add("sys/template", "sprintf", t, argsFor(rng, t))
	}
	// fmt: verbs, flags, widths, precisions on every argument type.
	verbs := []string{"%v", "%s", "%d", "%q", "%x", "%X", "%t", "%T", "%#v", "%+v", "%c", "%b", "%o", "%O", "%g", "%e", "%f", "%.2f", "%8.3f", "%-8d|", "%08d", "%+d", "% d", "%5s|", "%-5s|", "%.3s", "%-.3s", "%05s", "%#x", "%# x", "% x", "%.*s", "%*d", "%-*d|", "%.0d", "%10.4v|", "%!", "%", "%z", "%[1]d", "%%", "%.d"}
	for _, v := range verbs {
		for i := 0; i < 12; i++ {
			x := randAny(rng)
			for v == "%+v" && x.T == "error" { // %+v of a pingcap error prints its Go stack
				x = randAny(rng)
			}
			a := []typedArg{x}
			if strings.Contains(v, "*") {
				a = append([]typedArg{ta("int", rng.IntN(12)-3)}, a...)
			}
			g.Add("sys/verbs", "sprintf", "<"+v+">", a)
		}
	}
	sprintfRand(g, rng)
	for i := 0; i < 30; i++ {
		n := rng.IntN(5)
		a := []typedArg{}
		for j := 0; j < n; j++ {
			a = append(a, randAny(rng))
		}
		g.Add("rand/sprint", "sprint", a)
	}

	// mysql.NewErr / NewErrf with redaction.
	codes := sortedCodes(mysql.MySQLErrName)
	for i := 0; i < 600; i++ {
		c := codes[rng.IntN(len(codes))]
		if i < 40 {
			c = uint16(rng.IntN(20000)) // mostly unknown codes: fmt.Sprint path, default state
		}
		raw := ""
		if m, ok := mysql.MySQLErrName[c]; ok {
			raw = m.Raw
		}
		g.Add("rand/new_err", "new_err", c, argsFor(rng, raw), redactModes[rng.IntN(3)])
	}
	// errors with redaction positions, all modes.
	for _, c := range codes {
		if m := mysql.MySQLErrName[c]; len(m.RedactArgPos) > 0 {
			for _, mode := range redactModes {
				g.Add("sys/redact", "new_err", c, argsFor(rng, m.Raw), mode)
			}
		}
	}
	for i := 0; i < 60; i++ {
		f := []string{"%s", "%d %s", "key '%-.64s' %d", "%v and %v", "%s"}[rng.IntN(5)]
		pos := [][]int{nil, {0}, {1}, {0, 1}, {5}}[rng.IntN(5)]
		if pos == nil {
			pos = []int{}
		}
		g.Add("rand/new_errf", "new_errf", codes[rng.IntN(len(codes))], f, pos, argsFor(rng, f), redactModes[rng.IntN(3)])
	}

	// terror / dbterror NewStd on the registered (class, code) pairs.
	reg := protos.RegistrySnapshot()
	type pair struct{ class, code int }
	var pairs []pair
	for _, cc := range reg {
		for _, code := range cc[1].([]int) {
			pairs = append(pairs, pair{cc[0].(int), code})
		}
	}
	for _, p := range pairs {
		if m, ok := mysql.MySQLErrName[uint16(p.code)]; ok {
			g.Add("sys/registry", "terror_new_std", p.class, p.code, argsFor(rng, m.Raw), redactModes[rng.IntN(3)])
		}
		if m, ok := errno.MySQLErrName[uint16(p.code)]; ok {
			g.Add("sys/registry", "dbterror_new_std", p.class, p.code, argsFor(rng, m.Raw), redactModes[rng.IntN(3)])
		}
	}
	for _, k := range pk {
		g.Add("sys/prototype", "prototype", k, argsFor(rng, protos.Vars[k].MessageTemplate()), redactModes[rng.IntN(3)])
	}

	// Synthesize / ToSQLError / classes (including unregistered classes and codes).
	for c := -1; c <= 30; c++ {
		g.Add("sys/synthesize", "synthesize", c, 1000+c, "msg %d")
	}
	for i := 0; i < 150; i++ {
		var class, code int
		if i < len(pairs) && rng.IntN(2) == 0 {
			p := pairs[rng.IntN(len(pairs))]
			class, code = p.class, p.code
		} else {
			class, code = rng.IntN(30)-1, rng.IntN(10000)
		}
		g.Add("rand/to_sql_error", "to_sql_error", class, code, vec.GoString("boom "+randStr(rng)))
	}
	descs := []string{"ddl", "planner", "evaluator", "perfschema", "nope", "", "types", "kv"}
	for i := 0; i < 60; i++ {
		g.Add("rand/err_class", "err_class", rng.IntN(30)-1, descs[rng.IntN(len(descs))], rng.IntN(9000))
	}
	mk := func() map[string]any {
		switch rng.IntN(5) {
		case 0:
			return map[string]any{"kind": "nil"}
		case 1:
			return map[string]any{"kind": "plain", "msg": []string{"x", "y", "[ddl:8200]x"}[rng.IntN(3)]}
		}
		return map[string]any{"kind": "terr", "class": []int{2, 10, 4}[rng.IntN(3)], "code": []int{8200, 1105}[rng.IntN(2)], "msg": []string{"x", "y"}[rng.IntN(2)]}
	}
	for i := 0; i < 60; i++ {
		g.Add("rand/error_equal", "error_equal", mk(), mk())
	}

	// SQL modes.
	modeStrs := []string{"", "ansi", "TRADITIONAL", "strict_trans_tables,ONLY_FULL_GROUP_BY  ", "ANSI,ANSI", ",,REAL_AS_FLOAT,", "foo", "ANSI,foo", "db2,mysql40", mysql.DefaultSQLMode, "  ansi", "ﬀ", "ß,traditional", "ORACLE,POSTGRESQL,MSSQL,MAXDB,MYSQL323"}
	for _, s := range modeStrs {
		g.Add("sys/sqlmode", "format_sql_mode_str", s)
		g.Add("sys/sqlmode", "get_sql_mode", s)
		g.Add("sys/sqlmode", "get_sql_mode", mysql.FormatSQLModeStr(s))
	}
	var modeNames []string
	for k := range mysql.Str2SQLMode {
		modeNames = append(modeNames, k)
	}
	sort.Strings(modeNames)
	for i := 0; i < 40; i++ {
		var parts []string
		for j := rng.IntN(5); j > 0; j-- {
			n := modeNames[rng.IntN(len(modeNames))]
			if rng.IntN(2) == 0 {
				n = strings.ToLower(n)
			}
			parts = append(parts, n)
		}
		s := strings.Join(parts, ",")
		g.Add("rand/sqlmode", "format_sql_mode_str", s)
		g.Add("rand/sqlmode", "get_sql_mode", mysql.FormatSQLModeStr(s))
	}
	for i := 0; i < 40; i++ {
		m := rng.Int64N(1 << 34)
		if i < 34 {
			m = 1 << i
		}
		g.Add("rand/sqlmode", "sql_mode_has", m)
	}
	for i := 0; i < 20; i++ {
		g.Add("rand/sqlmode", "sql_mode_set", rng.Int64N(1<<34), rng.Int64N(1<<34))
	}
	for _, s := range []string{"no_priority", "HIGH_PRIORITY", "low_priority", "delayed", "x", "", "High_Priority"} {
		g.Add("sys/priority", "str2priority", s)
	}
	for p := -1; p <= 5; p++ {
		g.Add("sys/priority", "priority_restore", p)
	}
	for _, s := range []uint16{0, 0x40, 0xffff, 0x3f, 0x41} {
		g.Add("sys/status", "has_cursor_exists_flag", s)
	}
	for i := 0; i < 40; i++ {
		f := uint(1) << i
		if i >= 26 {
			f = uint(rng.Uint64())
		}
		g.Add("rand/flags", "type_flags", f)
	}

	// Charsets, unicode tables.
	var cs []string
	for k := range mysql.CharsetIDs {
		cs = append(cs, k)
	}
	sort.Strings(cs)
	for _, c := range append(cs, "", "UTF8MB4", "nope", "utf8mb3") {
		g.Add("sys/charset", "charset", c)
	}
	for r := int32(0); r < 0x3000; r += 7 {
		g.Add("sys/unicode", "unicode", r)
	}
	for i := 0; i < 1000; i++ {
		g.Add("rand/unicode", "unicode", rng.Int32N(0x110000))
	}

	// Privileges.
	var cols []string
	for k := range mysql.Col2PrivType {
		cols = append(cols, k)
	}
	for k := range mysql.SetStr2Priv {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	for _, c := range append(cols, "", "nope", "select_priv") {
		g.Add("sys/priv", "new_priv", c)
	}
	for i := 0; i < 36; i++ {
		g.Add("sys/priv", "priv_strings", uint64(1)<<i)
	}
	var lists []string
	for k := range privLists {
		lists = append(lists, k)
	}
	sort.Strings(lists)
	for _, l := range lists {
		for i := 0; i < 12; i++ {
			g.Add("rand/priv", "privileges_has", l, uint64(1)<<rng.IntN(36))
		}
	}

	// Type helpers.
	for tp := 0; tp < 256; tp++ {
		g.Add("sys/types", "type_defaults", tp)
	}
	for _, p := range append(mysql.DefaultAuthPlugins, "", "x", "MYSQL_NATIVE_PASSWORD") {
		g.Add("sys/auth", "auth_clear_text", p)
	}

	// FORMAT locales: number strings as FORMAT produces them, and edge cases.
	nums := []string{"0", "-0", "1", "-1", "12", "123", "1234", "12345.678", "-1234567.891", ".5", "-.5", "1e10",
		"123456789012345678901234567890", "1.", "-", "abc", "-abc", "1.2.3", "12.34.56", "00012", "١٢٣", "9..9", "-9..9"}
	precs := []string{"0", "2", "10", "-1", "a", "3a", "٣", "18446744073709551615", "18446744073709551616", " 2"}
	for _, n := range nums {
		for _, p := range precs {
			g.Add("sys/format", "format_locale", "en_US", n, p)
		}
	}
	for i := 0; i < 40; i++ {
		n := strconv.FormatFloat((rng.Float64()-0.5)*1e9, 'f', rng.IntN(6), 64)
		g.Add("rand/format", "format_locale", []string{"en_US", "en_US", "zh_CN", "de_DE"}[rng.IntN(4)], n, strconv.Itoa(rng.IntN(8)))
	}

	// errno summary counters.
	users := []string{"root", "u1", ""}
	for i := 0; i < 12; i++ {
		var steps [][]any
		for j := rng.IntN(8); j > 0; j-- {
			steps = append(steps, []any{[]string{"error", "warning"}[rng.IntN(2)], []int{1062, 1105, 8200}[rng.IntN(3)], users[rng.IntN(3)], fmt.Sprintf("h%d", rng.IntN(2))})
		}
		if steps == nil {
			steps = [][]any{}
		}
		g.Add("rand/errno_stats", "errno_stats", steps)
	}
	for _, t := range []int{0, 1, 2, 3, 254, 255} {
		g.Add("sys/store_type", "store_type", t)
	}
}

// ---------------------------------------------------------------- random fmt

// sprintfRand: random formats and arguments for fmt.Sprintf (after the fuzz in
// the review of tidb-rs PR #6): random flags, widths, precisions, '*' and
// verbs; arguments of every type including non-UTF-8 strings, float32,
// special floats and floats exactly halfway between two shortest
// representations (k/2^n); a few huge '*' widths and precisions (Go allows
// up to 1e6, beyond that %!(BADWIDTH)).
func sprintfRand(g *vec.Gen, rng *rand.Rand) {
	pick := func(xs []string) string { return xs[rng.IntN(len(xs))] }
	randFloat := func() float64 {
		switch rng.IntN(12) {
		case 0:
			return []float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64,
				math.SmallestNonzeroFloat64, 1e21, 1e20, 1e-5, 1e-4, 0.1, 0.5, 1.5, 2.5, 123456789, 1e100}[rng.IntN(17)]
		case 1, 2:
			return math.Float64frombits(rng.Uint64())
		case 3, 4:
			return float64(rng.IntN(2000000)-1000000) / math.Pow10(rng.IntN(12))
		case 5, 6, 7:
			// halfway: k/2^n (exact binary fractions)
			k := float64(rng.Int64N(1 << 53))
			if rng.IntN(2) == 0 {
				k = float64(rng.Int64N(1 << 24))
			}
			f := k / math.Pow(2, float64(rng.IntN(12)))
			if rng.IntN(2) == 0 {
				f = -f
			}
			return f
		default:
			return (rng.Float64() - 0.5) * math.Pow10(rng.IntN(60)-30)
		}
	}
	randStr := func() string {
		n := rng.IntN(12)
		var b strings.Builder
		for i := 0; i < n; i++ {
			switch rng.IntN(20) {
			case 0:
				b.WriteByte(byte(0x80 + rng.IntN(0x80))) // invalid or partial UTF-8
			case 1:
				b.WriteByte(0xff)
			default:
				b.WriteRune([]rune{'a', 'Z', ' ', '\'', '"', '\\', '\n', '\t', 0, 'é', '中', '🙂', '‹', 0x7f, 0x85,
					0x2028, 0xfeff, '%', 0x200b, 0xad, 0xe000, 0x10ffff}[rng.IntN(22)])
			}
		}
		return b.String()
	}
	randArg := func() typedArg {
		switch rng.IntN(10) {
		case 0, 1:
			return ta("string", randStr())
		case 2:
			v := int64(rng.Uint64()>>1) >> uint(rng.IntN(63))
			if rng.IntN(2) == 0 {
				v = -v
			}
			return ta("int", v)
		case 3:
			return ta("int8", int64(int8(rng.IntN(256)-128)))
		case 4:
			return ta("uint64", rng.Uint64()>>uint(rng.IntN(64)))
		case 5:
			return ta("float64", strconv.FormatFloat(randFloat(), 'g', -1, 64))
		case 6:
			return ta("float32", strconv.FormatFloat(float64(float32(randFloat())), 'g', -1, 32))
		case 7:
			return ta("bool", rng.IntN(2) == 0)
		case 8:
			return ta("nil", nil)
		}
		bs := make([]byte, rng.IntN(6))
		for i := range bs {
			bs[i] = byte(rng.IntN(256))
		}
		if rng.IntN(2) == 0 && len(bs) >= 2 {
			copy(bs, "ab")
		}
		return ta("bytes", hex.EncodeToString(bs))
	}
	randSpec := func() string {
		s := "%"
		for _, f := range []string{"-", "+", "#", " ", "0"} {
			if rng.IntN(6) == 0 {
				s += f
			}
		}
		switch rng.IntN(5) {
		case 0:
			s += strconv.Itoa(rng.IntN(25))
		case 1:
			s += "*"
		}
		switch rng.IntN(4) {
		case 0:
			s += "." + strconv.Itoa(rng.IntN(20))
		case 1:
			s += "."
		case 2:
			if rng.IntN(3) == 0 {
				s += ".*"
			}
		}
		if rng.IntN(40) == 0 {
			s += "[" + strconv.Itoa(rng.IntN(4)) + "]"
		}
		verbs := []rune("vvvvsssdddqxXtTcbofeEgGFOU!z")
		return s + string(verbs[rng.IntN(len(verbs))])
	}
	for i := 0; i < 20000; i++ {
		format := ""
		for k := rng.IntN(3) + 1; k > 0; k-- {
			format += pick([]string{"", "x", "'", "[", " "}) + randSpec()
		}
		args := []typedArg{}
		for k := rng.IntN(4); k >= 0; k-- {
			if rng.IntN(4) == 0 { // a small int for '*'
				args = append(args, ta("int", int64(rng.IntN(30)-5)))
				continue
			}
			args = append(args, randArg())
		}
		g.Add("rand/sprintf", "sprintf_rand", format, args)
	}
	// Huge widths and precisions through '*' (Rust's format! panics above 65535).
	for _, w := range []int64{65535, 65536, 70000, 1000000, 1000001, -70000, 2000000} {
		for _, f := range []string{"%*d", "%-*s|", "%.*f", "%*.*e", "%.*s", "%0*x", "%*v"} {
			args := []typedArg{ta("int", w)}
			if strings.Count(f, "*") == 2 {
				args = append(args, ta("int", w))
			}
			args = append(args, randArg())
			g.Add("sys/huge_width", "sprintf_rand", f, args)
		}
	}
}
