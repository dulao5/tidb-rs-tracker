package domains

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/config"
)

// tomlKey is one leaf of config.Config as BurntSushi sees it.
type tomlKey struct {
	path []string
	kind string // bool int int32 uint uint32 uint64 int64 float string duration strs map nb ab
}

// tomlKeys walks config.Config with BurntSushi's naming: the toml tag or the
// field name; untagged embedded structs are promoted; toml:"-" is skipped.
func tomlKeys(t reflect.Type, prefix []string, out *[]tomlKey, sections *[][]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("toml")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		ft := f.Type
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			tomlKeys(ft, prefix, out, sections)
			continue
		}
		if name == "" {
			name = f.Name
		}
		path := append(append([]string{}, prefix...), name)
		kind := ""
		switch {
		case ft == reflect.TypeOf(time.Duration(0)):
			kind = "duration"
		case ft == reflect.TypeOf(config.AtomicBool{}):
			kind = "ab"
		case ft.Name() == "nullableBool":
			kind = "nb"
		case ft.Kind() == reflect.Struct:
			*sections = append(*sections, path)
			tomlKeys(ft, path, out, sections)
			continue
		case ft.Kind() == reflect.Bool:
			kind = "bool"
		case ft.Kind() == reflect.Int:
			kind = "int"
		case ft.Kind() == reflect.Int32:
			kind = "int32"
		case ft.Kind() == reflect.Int64:
			kind = "int64"
		case ft.Kind() == reflect.Uint:
			kind = "uint"
		case ft.Kind() == reflect.Uint32:
			kind = "uint32"
		case ft.Kind() == reflect.Uint64:
			kind = "uint64"
		case ft.Kind() == reflect.Float64:
			kind = "float"
		case ft.Kind() == reflect.String:
			kind = "string"
		case ft.Kind() == reflect.Slice:
			kind = "strs"
		case ft.Kind() == reflect.Map:
			kind = "map"
		default:
			panic("tomlKeys: " + ft.String())
		}
		*out = append(*out, tomlKey{path, kind})
	}
}

var tomlValues = map[string][]string{
	"bool":     {"true", "false", "1", `"true"`, "1.5", "[true]", "{a = 1}", "1979-05-27T07:32:00Z"},
	"int":      {"0", "1", "-1", "42", "2147483648", "-9223372036854775808", "9223372036854775807", "0x7f", "0o17", "0b101", "1_000", `"1"`, "1.0", "true", "[1]"},
	"int32":    {"0", "-1", "2147483647", "2147483648", "-2147483649", "65536", `"1"`},
	"int64":    {"0", "-1", "9223372036854775807", "123456789012", "1.5", `"x"`},
	"uint":     {"0", "1", "-1", "18", "4294967296", `"5"`, "1.5", "true"},
	"uint32":   {"0", "7", "4294967295", "4294967296", "-1", `"5"`},
	"uint64":   {"0", "1", "-1", "1099511627776", "1099511627777", "1.5"},
	"float":    {"0.5", "1", "-0.0", "1e300", "9007199254740993", "-9007199254740993", "inf", "-inf", "nan", "1.5e-7", "3", `"x"`, "true", "1_000.5", "6.02e+23"},
	"string":   {`"x"`, `'lit'`, "\"\"\"\nmulti\nline\"\"\"", "'''raw\\n'''", `"\u00e9\t"`, "1", "true", `["a"]`, `"AES128-CTR"`, `"a\\b"`, `"\U0001F600"`, `""`},
	"duration": {`"1s"`, `"1h2m3.5s"`, `"1.5µs"`, `"2us"`, `"bad"`, `""`, "1000", "-5", `"-1.5h"`, "1.5", `"9223372036854775807ns"`, `"0"`, `"1"`, `"3m"`},
	"strs":     {`["a", "b"]`, "[]", `"x"`, "[1]", `[["a"]]`, "[{a = 1}]", `["tikv"]`, `["tidb", "tikv", "tiflash"]`, `["tikv", "bad"]`, `[ "a", ]`},
	"map":      {`{zone = "z1"}`, "{}", "1", "{a = 1}", `{zone = "z1", host = "h"}`, `"x"`},
	"nb":       {"true", "false", `"true"`, `"false"`, `"null"`, `""`, `"maybe"`, "1", "0", "1.5", "1979-05-27T07:32:00Z", "[1]", "{a = 1}"},
	"ab":       {"true", "false", `"true"`, `"false"`, `"null"`, `""`, `"maybe"`, "1", "1.5", "[1]"},
}

// keyText writes a key path, quoting parts that are not bare keys.
func keyText(k []string) string {
	parts := make([]string, len(k))
	for i, p := range k {
		bare := p != ""
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				bare = false
			}
		}
		if bare {
			parts[i] = p
		} else {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, ".")
}

// assign writes `path = value` as a top-level key, under a [table] header, as a
// dotted key or (for two-part paths) as an inline table.
func assign(path []string, value string, rng *rand.Rand) string {
	if len(path) == 1 {
		return keyText(path) + " = " + value + "\n"
	}
	switch rng.IntN(4) {
	case 0:
		return keyText(path) + " = " + value + "\n"
	case 1:
		if len(path) == 2 {
			return keyText(path[:1]) + " = {" + keyText(path[1:]) + " = " + value + "}\n"
		}
	}
	return "[" + keyText(path[:len(path)-1]) + "]\n" + keyText(path[len(path)-1:]) + " = " + value + "\n"
}

func foldCase(s string, rng *rand.Rand) string {
	b := []byte(s)
	for i := range b {
		if rng.IntN(2) == 0 {
			b[i] = bytes.ToUpper(b[i : i+1])[0]
		}
	}
	return string(b)
}

var removedConfigKeys = []string{
	"pessimistic-txn.ttl", "pessimistic-txn.enable", "log.file.log-rotate", "log.log-slow-query",
	"txn-local-latches", "txn-local-latches.enabled", "txn-local-latches.capacity", "performance.max-memory",
	"max-txn-time-use", "experimental.allow-auto-random", "enable-redact-log", "enable-streaming",
	"performance.mem-profile-interval", "security.require-secure-transport", "lower-case-table-names",
	"stmt-summary", "stmt-summary.enable", "stmt-summary.enable-internal-query", "stmt-summary.max-stmt-count",
	"stmt-summary.max-sql-length", "stmt-summary.refresh-interval", "stmt-summary.history-size", "enable-batch-dml",
	"mem-quota-query", "log.query-log-max-len", "performance.committer-concurrency", "experimental.enable-global-kill",
	"performance.run-auto-analyze", "prepared-plan-cache.enabled", "prepared-plan-cache.capacity",
	"prepared-plan-cache.memory-guard-ratio", "oom-action", "check-mb4-value-in-utf8", "enable-collect-execution-info",
	"log.enable-slow-log", "log.slow-threshold", "log.record-plan-in-slow-log", "log.expensive-threshold",
	"performance.force-priority", "performance.memory-usage-alarm-ratio", "plugin.load", "plugin.dir",
	"performance.feedback-probability", "performance.query-feedback-limit", "oom-use-tmp-storage",
	"max-server-connections", "run-ddl", "instance.tidb_memory_usage_alarm_ratio", "enable-global-index",
}

var movedToInstance = [][3]string{
	{"", "check-mb4-value-in-utf8", "tidb_check_mb4_value_in_utf8"},
	{"", "enable-collect-execution-info", "tidb_enable_collect_execution_info"},
	{"", "max-server-connections", "max_connections"},
	{"", "run-ddl", "tidb_enable_ddl"},
	{"log", "enable-slow-log", "tidb_enable_slow_log"},
	{"log", "slow-threshold", "tidb_slow_log_threshold"},
	{"log", "record-plan-in-slow-log", "tidb_record_plan_in_slow_log"},
	{"performance", "force-priority", "tidb_force_priority"},
	{"performance", "memory-usage-alarm-ratio", "tidb_memory_usage_alarm_ratio"},
	{"plugin", "load", "plugin_load"},
	{"plugin", "dir", "plugin_dir"},
}

// validLimits: TOML snippets around the limits checked by Config.Valid and the
// client-go / zap checks it calls.
var validLimits = []string{
	`store = "tikv"`, `store = "mocktikv"`, `store = "unistore"`, `store = "bad"`,
	"store = \"mocktikv\"\n[instance]\ntidb_enable_ddl = false", "store = \"tikv\"\n[instance]\ntidb_enable_ddl = false",
	"max-index-length = 3071", "max-index-length = 3072", "max-index-length = 12288", "max-index-length = 12289",
	"index-limit = 63", "index-limit = 64", "index-limit = 512", "index-limit = 513",
	"[log.file]\nmax-size = 4096", "[log.file]\nmax-size = 4097", "[log.file]\nmax-size = -1",
	"table-column-count-limit = 1016", "table-column-count-limit = 1017", "table-column-count-limit = 4096", "table-column-count-limit = 4097",
	"[instance]\nplugin_audit_log_buffer_size = -1", "[instance]\nplugin_audit_log_buffer_size = 104857600", "[instance]\nplugin_audit_log_buffer_size = 104857601",
	"[instance]\nplugin_audit_log_flush_interval = 0", "[instance]\nplugin_audit_log_flush_interval = 1", "[instance]\nplugin_audit_log_flush_interval = 3600", "[instance]\nplugin_audit_log_flush_interval = 3601",
	"[tikv-client]\ngrpc-connection-count = 0", "[tikv-client]\ngrpc-compression-type = \"gzip\"", "[tikv-client]\ngrpc-compression-type = \"zstd\"", "[tikv-client]\ngrpc-compression-type = \"GZIP\"",
	"[tikv-client]\ngrpc-keepalive-timeout = 0.05", "[tikv-client]\ngrpc-keepalive-timeout = 0.049", "[tikv-client]\ngrpc-keepalive-timeout = 0.0500000001",
	"[tikv-client]\ngrpc-keepalive-timeout = 0.01", "[tikv-client]\ngrpc-keepalive-timeout = 3", "[tikv-client]\ngrpc-keepalive-timeout = 1e30",
	"[tikv-client]\ngrpc-keepalive-timeout = -1e30", "[tikv-client]\ngrpc-keepalive-timeout = nan", "[tikv-client]\ngrpc-keepalive-timeout = inf", "[tikv-client]\ngrpc-keepalive-timeout = -0.0",
	"[transaction-summary]\ntransaction-summary-capacity = 5000", "[transaction-summary]\ntransaction-summary-capacity = 5001",
	"[performance]\ntxn-total-size-limit = 1099511627776", "[performance]\ntxn-total-size-limit = 1099511627777",
	"[instance]\ntidb_memory_usage_alarm_ratio = -0.1", "[instance]\ntidb_memory_usage_alarm_ratio = 0", "[instance]\ntidb_memory_usage_alarm_ratio = 1", "[instance]\ntidb_memory_usage_alarm_ratio = 1.1", "[instance]\ntidb_memory_usage_alarm_ratio = nan",
	"[isolation-read]\nengines = []", "[isolation-read]\nengines = [\"tikv\", \"x\"]", "[isolation-read]\nengines = [\"tiflash\"]", "[isolation-read]\nengines = [\"TiKV\"]",
	"[security]\nspilled-file-encryption-method = \"PLAINTEXT\"", "[security]\nspilled-file-encryption-method = \"Aes128-CTR\"", "[security]\nspilled-file-encryption-method = \"bad\"", "[security]\nspilled-file-encryption-method = \"\"",
	"[security]\nspilled-file-encryption-method = \"PLAİNTEXT\"", "[security]\nspilled-file-encryption-method = \"ÀES128-CTR\"",
	"[security]\nskip-grant-table = true",
	"[performance]\nstats-load-concurrency = -1", "[performance]\nstats-load-concurrency = 0", "[performance]\nstats-load-concurrency = 128", "[performance]\nstats-load-concurrency = 129",
	"[performance]\nstats-load-queue-size = 0", "[performance]\nstats-load-queue-size = 1", "[performance]\nstats-load-queue-size = 100000", "[performance]\nstats-load-queue-size = 100001",
	"disaggregated-tiflash = true\nuse-autoscaler = true", "disaggregated-tiflash = true\nuse-autoscaler = true\nautoscaler-type = \"bad\"",
	"disaggregated-tiflash = true\nuse-autoscaler = true\nautoscaler-type = \"gcp\"\nautoscaler-addr = \"\"", "disaggregated-tiflash = true\nuse-autoscaler = true\nautoscaler-type = \"mock\"",
	"disaggregated-tiflash = false\nuse-autoscaler = true\nautoscaler-type = \"bad\"",
	"[log]\nlevel = \"debug\"", "[log]\nlevel = \"INFO\"", "[log]\nlevel = \"Warn\"", "[log]\nlevel = \"warning\"", "[log]\nlevel = \"WARNING\"",
	"[log]\nlevel = \"bad\"", "[log]\nlevel = \"\"", "[log]\nlevel = \"DPANIC\"", "[log]\nlevel = \"fatal\"", "[log]\nlevel = \"İnfo\"", "[log]\nlevel = \"ERROR\"", "[log]\nlevel = \"panic\"",
	"token-limit = 0", "token-limit = 1048576", "token-limit = 1048577", "token-limit = 5",
	"[log]\nenable-error-stack = true\ndisable-error-stack = true", "[log]\nenable-error-stack = false\ndisable-error-stack = false",
	"[log]\nenable-error-stack = true\ndisable-error-stack = false", "[log]\nenable-timestamp = true\ndisable-timestamp = true",
	"[log]\nenable-timestamp = false\ndisable-timestamp = false", "[log]\ndisable-timestamp = true",
}

// lexCases: syntax and value edge cases of the TOML lexer and parser.
var lexCases = []string{
	`host = "unterminated`, "host = \"\"\"x", "host = 'x\n'", `host = "\q"`, `host = "\u12"`, `host = "\uD800"`, `host = "\U00110000"`,
	`host = "\x41"`, `host = "\e"`, "host = \"a\\\n  b\"", "host = \"\"\"a\\\n   b\"\"\"", "host = \"\"\"a \\  \n  b\"\"\"",
	"port = 1__0", "port = 01", "port = +01", "port = -0", "port = +0", "port = 1_", "port = _1", "port = 0x", "port = 0xg", "port = 0b12", "port = 0o8",
	"[performance]\npseudo-estimate-ratio = 1.", "[performance]\npseudo-estimate-ratio = .5", "[performance]\npseudo-estimate-ratio = +nan",
	"[performance]\npseudo-estimate-ratio = -nan", "[performance]\npseudo-estimate-ratio = 1e", "[performance]\npseudo-estimate-ratio = 1.e2",
	"[performance]\npseudo-estimate-ratio = 1e400", "[performance]\npseudo-estimate-ratio = 01.5", "[performance]\npseudo-estimate-ratio = 1_.5",
	"[performance]\npseudo-estimate-ratio = 1e1_0", "[performance]\npseudo-estimate-ratio = 0e0", "[performance]\npseudo-estimate-ratio = -0.0e-0",
	"[log", "[[log]]", "[log]\n[log]", "[log]\nlevel = \"info\"\n[log]", "host = 1\nhost = 2", "log.level = \"info\"\n[log]\nformat = \"text\"",
	"[log]\nformat = \"text\"\n[log.file]\nmax-size = 1\n[log]", "log = 1\n[log]", "[log]\n[log.file]\n[log]\nlevel = \"info\"",
	"log = {level = \"info\",}", "log = {level = \"info\"\n}", "log = {level = \"info\", level = \"warn\"}", "log = {file.max-size = 5}",
	"log = {file = {max-size = 5}}", "log.file = {}", "[isolation-read]\nengines = [\"a\",,\"b\"]", "[isolation-read]\nengines = [\"a\"\n,\"b\",\n# c\n]",
	"host = 1979-05-27", "host = 07:32:00", "host = 1979-05-27 07:32:00", "host = 1979-02-30T00:00:00Z", "host = 1979-05-27T25:00:00Z",
	"host = 1979-05-27T07:32:00.999999999999Z", "host = 1979-05-27t07:32:00z", "host = 1979-05-27T07:32", "host = 1979-5-27",
	"port = 9223372036854775808", "port = -9223372036854775809", "port = 0xffffffffffffffff", "port = 0x7fffffffffffffff",
	"host = tru", "host = \"x\" port = 1", "host = \"x\"#c", "host = \"x\" # comment\n# another", "\ufeffhost = 'x'",
	"\"host\" = \"q\"", "'host' = \"lit\"", "\"\" = 1", "log.\"level\" = \"info\"", "\"log\".level = \"info\"", "log . level = \"info\"",
	"host=\"x\"\r\nport=1\r\n", "host = \"x\"\r", "[log]\r\nlevel = \"debug\"", "a = [\n1,\n2,\n]", "a = [1, \"x\"]",
	"[[a]]\nb = 1\n[[a]]\nb = 2", "[[a]]\n[a.b]\nc = 1", "[a]\nb = 1\n[[a]]", "[[a]]\n[a]", "a = [{b = 1}]\n[[a]]",
	"[a.b]\n[a]\nc = 1", "[a.b]\n[a]\nb = 1", "a.b = 1\n[a]", "a.b.c = 1\na.b = 2", "a = 1\na.b = 2",
	"= 1", "host =", "host", "host = \n1", "[]", "[log.]", "[.log]", "[log..file]", "[ log . file ]\nmax-size = 2",
	"host = \"\\u0000\"", "host = \"\x01\"", "host = \"a\tb\"", "host = 'a\tb'", "host = \"\"\"a\x7fb\"\"\"", "#\x01", "host = '''a''''", "host = \"\"\"a\"\"\"\"\"",
	"host = \"\"\"\"\"\"\"", "host = '''\n'''", "host = \"\"\"\r\nx\"\"\"", "host = { }", "[instance]\ntidb_enable_ddl = 2023-01-01T00:00:00+09:00",
	"[instance]\ntidb_enable_ddl = 2023-01-01T00:00:00.5-00:30", "[log]\nenable-timestamp = 1999-12-31T23:59:59.123Z",
}

func configGenerate(g *vec.Gen, rng *rand.Rand) {
	var keys []tomlKey
	var sections [][]string
	tomlKeys(reflect.TypeOf(config.Config{}), nil, &keys, &sections)

	add := func(src, op string, args ...any) {
		if (op == "load" || op == "removed_check") && !loadDeterministic(string(args[0].(vec.GoString))) {
			return
		}
		// Sanity: the record is the same on a second run.
		var first []byte
		for i := 0; i < 2; i++ {
			raw := make([]json.RawMessage, len(args))
			for j, a := range args {
				raw[j] = vec.MustJSON(a)
			}
			out, err := configExec(op, raw)
			b := vec.MustJSON([]any{out, fmt.Sprint(err)})
			if i == 0 {
				first = b
			} else if !bytes.Equal(first, b) {
				return
			}
		}
		g.Add(src, op, args...)
	}
	gs := func(s string) vec.GoString { return vec.GoString(s) }

	var testTOML []string
	if err := json.Unmarshal(configTestTOMLJSON, &testTOML); err != nil {
		panic(err)
	}

	add("NewConfig", "default_conf")
	add("defaults", "defaults")

	// ---- load: corpus
	add("config.toml.example", "load", gs(configExample))
	for _, t := range testTOML {
		add("config_test.go", "load", gs(t))
	}
	// ---- load: one key, every kind of value
	var loads []string // for the other ops
	for _, k := range keys {
		for _, v := range tomlValues[k.kind] {
			path := k.path
			if rng.IntN(10) == 0 {
				path = append([]string{}, path...)
				path[len(path)-1] = foldCase(path[len(path)-1], rng)
				if len(path) > 1 && rng.IntN(2) == 0 {
					path[0] = foldCase(path[0], rng)
				}
			}
			t := assign(path, v, rng)
			add("key:"+strings.Join(k.path, "."), "load", gs(t))
			if _, err, _ := loadConf(t); !isDecodeErr(err) {
				loads = append(loads, t)
			}
		}
	}
	for _, s := range sections {
		for _, v := range []string{"1", `"x"`, "[1]", "[]", "1979-05-27T07:32:00Z"} {
			add("section:"+strings.Join(s, "."), "load", gs(keyText(s)+" = "+v+"\n"))
		}
	}
	// ---- unknown, case-folded, removed and moved keys
	for _, t := range []string{"unknown-x = 1", "[log]\nunknown = 1", "[nosuch]\na = 1", "[log.file]\nzzz = 2",
		"[log]\nLEVEL = \"warn\"", "[LOG]\nlevel = \"warn\"", "HOST = \"h\"", "[log.FILE]\nMAX-SIZE = 7", "[labels]\nZone = \"z\"", "labels.zone = 1",
		"[performance]\nmax-procs = 2\nunknown-1 = 1\n[log]\nunknown-2 = 2", "[[log]]\nlevel = \"x\"", "[[nosuch]]\na = 1",
		"[nosuch.deeper]\na = {b = 1}", "x.y.z = 1", "[instance]\nunknown_var = 1",
	} {
		add("unknown", "load", gs(t))
	}
	for _, r := range removedConfigKeys {
		parts := strings.Split(r, ".")
		for _, v := range []string{"true", "1", `"x"`} {
			add("removed", "load", gs(assign(parts, v, rng)))
		}
	}
	for _, m := range movedToInstance {
		old := []string{m[1]}
		if m[0] != "" {
			old = []string{m[0], m[1]}
		}
		for _, v := range []string{"true", "1", `"x"`} {
			t := assign(old, v, rng)
			add("moved", "load", gs(t))
			add("moved+instance", "load", gs(t+"[instance]\n"+m[2]+" = "+v+"\n"))
		}
	}
	for i := 0; i < 40; i++ {
		a := movedToInstance[rng.IntN(len(movedToInstance))]
		b := movedToInstance[rng.IntN(len(movedToInstance))]
		var sb strings.Builder
		for _, m := range [][3]string{a, b} {
			if m[0] == "" {
				fmt.Fprintf(&sb, "%s = true\n", m[1])
			}
		}
		for _, m := range [][3]string{a, b} {
			if m[0] != "" {
				fmt.Fprintf(&sb, "[%s]\n%s = true\n", m[0], m[1])
			}
		}
		if rng.IntN(2) == 0 {
			fmt.Fprintf(&sb, "[instance]\n%s = true\n", a[2])
		}
		add("moved-pairs", "load", gs(sb.String()))
	}
	for _, t := range validLimits {
		add("valid", "load", gs(t))
	}
	for _, t := range lexCases {
		add("lex", "load", gs(t))
	}
	// ---- load: random corruption of real snippets
	exLines := strings.Split(configExample, "\n")
	const alphabet = "[]=\"'\n.#{},\\ _-0123456789abcxyzeE+:tTzZ\t\r"
	for i := 0; i < 1200; i++ {
		var base string
		if rng.IntN(3) == 0 {
			base = testTOML[rng.IntN(len(testTOML))]
		} else {
			start := rng.IntN(len(exLines))
			end := min(len(exLines), start+1+rng.IntN(30))
			base = strings.Join(exLines[start:end], "\n")
		}
		b := []byte(base)
		for n := 1 + rng.IntN(3); n > 0 && len(b) > 0; n-- {
			p := rng.IntN(len(b) + 1)
			switch rng.IntN(3) {
			case 0: // delete
				if p < len(b) {
					b = append(b[:p], b[p+1:]...)
				}
			case 1: // insert
				c := alphabet[rng.IntN(len(alphabet))]
				b = append(b[:p], append([]byte{c}, b[p:]...)...)
			case 2: // replace
				if p < len(b) {
					b[p] = alphabet[rng.IntN(len(alphabet))]
				}
			}
		}
		add("corrupt", "load", gs(string(b)))
	}
	// ---- other ops
	add("missing", "load_missing", gs("/nonexistent-govec/govec-config.toml"))
	pick := func() string { return loads[rng.IntN(len(loads))] }
	for _, r := range removedConfigKeys {
		add("removed", "removed_check", gs(assign(strings.Split(r, "."), "true", rng)))
	}
	add("example", "removed_check", gs(configExample))
	for i := 0; i < 60; i++ {
		var sb strings.Builder
		for n := rng.IntN(4); n >= 0; n-- {
			sb.WriteString(assign(strings.Split(removedConfigKeys[rng.IntN(len(removedConfigKeys))], "."), "1", rng))
		}
		add("removed-multi", "removed_check", gs(sb.String()))
	}
	add("example", "json_config", gs(configExample))
	add("nan", "json_config", gs("[performance]\npseudo-estimate-ratio = nan\n"))
	add("inf", "json_config", gs("[tikv-client]\ngrpc-keepalive-timeout = -inf\n"))
	add("html", "json_config", gs("host = \"<a&b>\\u2028\\u0001\"\n[labels]\n\"z<\" = \"&\"\n"))
	for i := 0; i < 150; i++ {
		add("random", "json_config", gs(pick()))
	}
	for _, s := range []string{"performance.index-usage-sync-lease", "PERFORMANCE.INDEX-USAGE-SYNC-LEASE = 1", "index-usage-sync-lease",
		"set log.query-log-max-len", "oom-action", "OOM-ACTION", "stmt-summary", "stmt-summar", "run-ddl", "enable-batch-dml=1",
		"", "x", "plugin.dir", "PLUGIN.LOAD", "İnstance.tidb_memory_usage_alarm_ratio", "instance.tidb_memory_usage_alarm_ratio"} {
		add("hidden", "contain_hidden", gs(s))
	}
	for i := 0; i < 50; i++ {
		k := keys[rng.IntN(len(keys))]
		add("hidden-rand", "contain_hidden", gs(foldCase(strings.Join(k.path, "."), rng)))
	}
	add("example", "clone", gs(configExample))
	add("nan", "clone", gs("[performance]\npseudo-estimate-ratio = nan\n"))
	for i := 0; i < 150; i++ {
		add("random", "clone", gs(pick()))
	}
	dyn := []string{"[performance]\nmax-procs = 3", "[performance]\nmax-memory = 5", "[performance]\ncross-join = false",
		"[performance]\npseudo-estimate-ratio = 0.3", "[performance]\nstmt-count-limit = 7", "[performance]\ntcp-keep-alive = false",
		"[tikv-client]\nstore-limit = 9", "[log]\nlevel = \"warn\"", "[log]\nexpensive-threshold = 11", "[instance]\ntidb_slow_log_threshold = 12",
		"[instance]\ntidb_check_mb4_value_in_utf8 = false", "compatible-kill-query = true", "treat-old-version-utf8-as-utf8mb4 = false",
		"[opentracing]\nenable = true", "[log]\nenable-timestamp = true", "[log]\ndisable-error-stack = false", "[performance]\npseudo-estimate-ratio = nan"}
	for _, d := range dyn {
		add("merge-dyn", "merge", gs(""), gs(d))
		add("merge-dyn-rev", "merge", gs(d), gs(""))
	}
	for i := 0; i < 250; i++ {
		add("merge-rand", "merge", gs(pick()), gs(pick()+dyn[rng.IntN(len(dyn))]+"\n"))
	}
	defJSON, err := config.GetJSONConfig()
	if err != nil {
		panic(err)
	}
	for _, j := range []string{envClean(defJSON), `{}`, `{"a": {"b": {"c": 1}}, "d": [1, {"e": 2}], "f": null}`,
		`{"a": {}, "b": 1e21, "c": 1e-7, "d": 123456789012345678901234, "e": -0.0, "f": 0.000001}`,
		`{"<x>": "a&b\u2028", "y": "\u0001\"\\"}`, `{"k": [[1], {}]}`,
		`{"a": 1.5, "b": 100, "c": 1e20, "d": 12345678.9, "e": 5e-324}`} {
		add("flatten", "flatten", gs(j))
	}
	for i := 0; i < 30; i++ {
		c, _, _ := loadConf(pick())
		restore := config.RestoreFunc()
		config.StoreGlobalConfig(c)
		j, err := config.GetJSONConfig()
		restore()
		if err == nil {
			add("flatten-rand", "flatten", gs(envClean(j)))
		}
	}
	for _, t := range []string{"", "null", "true", "false", "True", "1", "maybe", " true", "nul"} {
		add("nb", "nb_text", gs(t))
		add("ab", "ab_text", gs(t))
	}
	for _, j := range []string{"true", "false", "null", "1", `"true"`, "{}", "[]", " true ", "0.5"} {
		add("nb", "nb_json", gs(j))
	}
	logs := []string{"", "[log]\nlevel = \"warn\"\nformat = \"json\"", "[log]\nenable-timestamp = true", "[log]\nenable-timestamp = false",
		"[log]\ndisable-timestamp = true", "[log]\ndisable-timestamp = false", "[log]\nenable-error-stack = true", "[log]\nenable-error-stack = false",
		"[log]\ndisable-error-stack = true", "[log]\ndisable-error-stack = false", "[log]\nslow-query-file = \"s.log\"\ngeneral-log-file = \"g.log\"\ntimeout = 9",
		"[log.file]\nfilename = \"f.log\"\nmax-size = 10\nmax-days = 2\nmax-backups = 3\ncompression = \"gzip\"\nis-buffered = true\nbuffer-size = 5\nbuffer-flush-interval = \"2s\""}
	for _, a := range []string{"", "[log]\nenable-timestamp = true\n", "[log]\nenable-timestamp = false\n"} {
		for _, b := range []string{"", "disable-timestamp = true\n", "disable-timestamp = false\n", "enable-error-stack = true\n",
			"enable-error-stack = false\n", "disable-error-stack = true\n", "disable-error-stack = false\n"} {
			t := a
			if b != "" {
				if a == "" {
					t = "[log]\n"
				}
				t += b
			}
			logs = append(logs, t)
		}
	}
	for _, t := range logs {
		add("log", "log_config", gs(t))
	}
	for i := 0; i < 30; i++ {
		add("log-rand", "log_config", gs(pick()))
	}
	for _, t := range []string{"", "[opentracing]\nenable = true\nrpc-metrics = true",
		"[opentracing.sampler]\ntype = \"probabilistic\"\nparam = 0.25\nsampling-server-url = \"u\"\nmax-operations = 3\nsampling-refresh-interval = \"5s\"",
		"[opentracing.reporter]\nqueue-size = 7\nbuffer-flush-interval = 1000\nlog-spans = true\nlocal-agent-host-port = \"h:1\""} {
		add("tracing", "tracing_config", gs(t))
	}
	for i := 0; i < 20; i++ {
		add("tracing-rand", "tracing_config", gs(pick()))
	}
	for _, t := range []string{"", "[labels]\nzone = \"z1\"", "[security]\ncluster-ssl-ca = \"ca\"\ncluster-ssl-cert = \"c\"\ncluster-ssl-key = \"k\"\ncluster-verify-cn = [\"a\"]",
		"[performance]\nmax-txn-ttl = 5\nenable-async-batch-get = true", "[pessimistic-txn]\nmax-retry-count = 9", "path = \"pd:2379\"\nenable-forwarding = true\nstores-refresh-interval = 4",
		"[opentracing]\nenable = true", "[pd-client]\npd-server-timeout = 8", "[tikv-client]\ngrpc-connection-count = 2\n[tikv-client.async-commit]\nkeys-limit = 1\n[tikv-client.copr-cache]\ncapacity-mb = 1.5"} {
		add("tikv", "tikv_config", gs(t))
	}
	for i := 0; i < 20; i++ {
		add("tikv-rand", "tikv_config", gs(pick()))
	}
	for _, t := range []string{"", "enable-table-lock = true\ndelay-clean-table-lock = 5\nkeyspace-name = \"ks\"", "[labels]\nzone = \"bj\"", "[labels]\nhost = \"h\""} {
		add("global", "global", gs(t))
	}
	for i := 0; i < 20; i++ {
		add("global-rand", "global", gs(pick()))
	}
	for _, c := range [][]any{{"0.0.0.0", 4000, "0.0.0.0", 10080, ""}, {"127.0.0.1", 4000, "127.16.5.1", 10080, ""},
		{"127.0.0.1", 4000, "127.16.5.1", 15532, ""}, {"0.0.0.0", 4000, "0.0.0.0", 10080, "/data/tmp"},
		{"::1", 5000, "", 0, "rel/dir/"}, {"h", 1, "s", 2, "/a/../b//c/."}, {"", 0, "", 0, "/"}, {"0.0.0.0", 4001, "0.0.0.0", 10080, ""}} {
		g := make([]any, len(c))
		copy(g, c)
		add("temp", "temp_storage", g...)
	}
	for _, e := range [][2]string{{"plain", "x"}, {"plain", ""}, {"std", "1044"}, {"std", "1213"}, {"std", "8003"}, {"std", "8068"}} {
		add("label", "error_label", gs(e[0]), gs(e[1]))
	}
}

// isDecodeErr: Load failed in toml.DecodeFile (the config is partially set,
// in Go's random map order).
func isDecodeErr(err error) bool {
	switch err.(type) {
	case nil, *config.ErrConfigValidationFailed, *config.ErrConfigInstanceSection:
		return false
	}
	return true
}

// loadDeterministic reports whether Go's Load + Valid of data does not depend
// on map iteration order:
//   - at most one leaf of the TOML document fails to decode on its own (with
//     two failing leaves, BurntSushi reports whichever it meets first);
//   - no two keys of one table fold to the same field (last writer wins);
//   - ErrConfigInstanceSection lists at most one mapping per section;
//   - Valid does not fail on the store (it prints the ValidStorage map).
func loadDeterministic(data string) bool {
	var m map[string]any
	if _, err := toml.Decode(data, &m); err != nil {
		return true // a parse error: the first one in the text
	}
	var leaves [][]string
	var walk func(m map[string]any, path []string) bool
	walk = func(m map[string]any, path []string) bool {
		folded := map[string]bool{}
		for k, v := range m {
			f := strings.ToLower(k)
			if folded[f] {
				return false
			}
			folded[f] = true
			p := append(append([]string{}, path...), k)
			if sub, ok := v.(map[string]any); ok && len(sub) > 0 {
				if !walk(sub, p) {
					return false
				}
				continue
			}
			leaves = append(leaves, p)
		}
		return true
	}
	if !walk(m, nil) {
		return false
	}
	failing := 0
	for _, leaf := range leaves {
		one := map[string]any{}
		cur, src := one, m
		for i, k := range leaf {
			if i == len(leaf)-1 {
				cur[k] = src[k]
				break
			}
			next := map[string]any{}
			cur[k] = next
			cur, src = next, src[k].(map[string]any)
		}
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(one); err != nil {
			return false
		}
		c := freshConfig()
		if _, err := toml.Decode(buf.String(), c); err != nil {
			failing++
		}
	}
	if failing > 1 {
		return false
	}
	c, err, _ := loadConf(data)
	if isDecodeErr(err) {
		return true
	}
	for _, secs := range [][]config.InstanceConfigSection{config.ConflictOptions, config.DeprecatedOptions} {
		for _, s := range secs {
			if len(s.NameMappings) > 1 {
				return false
			}
		}
	}
	if verr := c.Valid(); verr != nil && strings.HasPrefix(verr.Error(), "invalid store=") {
		return false
	}
	return true
}
