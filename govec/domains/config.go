package domains

// Domain "config": block D00.2 part 2 (pkg/config, config_util.go, and the
// BurntSushi/toml decoder it uses). Records:
//
//   - "load": Config.Load of a TOML text into a fresh NewConfig(), then
//     Config.Valid; out has the Load error (and its Go type), the
//     undecoded items, ConflictOptions / DeprecatedOptions, the Valid error and
//     the config before and after Valid as a field dump (see confDump). After a
//     decode error only the error is recorded (the partial config depends on
//     Go's map order).
//     ConflictOptions / DeprecatedOptions are reset before each record.
//   - JSON, clone, merge, flatten, nullableBool / AtomicBool text and JSON,
//     the log / tracing / client-go conversions, the global config helpers.
//
// The TOML corpus: the example config.toml and the TOML snippets of
// config_test.go (testdata/config), plus generated mutations: one key of every
// field with values of every TOML type (wrong types, ranges, durations,
// nullable bools), case-folded and unknown keys, removed keys, options moved to
// [instance], Valid limits, and syntax errors (random corruption).
//
// Environment: the default tmp-storage-path depends on os.TempDir() and the
// user ID; dumps and messages carry the placeholder <TEMP_STORAGE_DIR> instead,
// and "temp_storage" paths <TMPDIR> and <UID>. The file name in messages is "govec-config.toml".
//
// Go nondeterminism: BurntSushi decodes TOML tables (Go maps) in random order,
// and ErrConfigInstanceSection.Error / Valid's store list print Go maps. A
// candidate whose record differs across repeated runs is dropped (the Rust port
// iterates in key order; see the D00.2 plan).

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "embed"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/config"
	"github.com/pingcap/tidb/pkg/metrics"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/util/dbterror"
)

const configSeed = 20261108

//go:embed testdata/config/config.toml.example
var configExample string

//go:embed testdata/config/config_test_toml.json
var configTestTOMLJSON []byte

func init() {
	vec.Register(&vec.Domain{
		Name:     "config",
		Seed:     configSeed,
		Coverage: configCoverage,
		Blocks:   []string{"D00.2"},
		Exempt:   configExempt,
		Gzip:     true,
		Generate: configGenerate,
		Exec:     configExec,
	})
}

var configCoverage = map[string]vec.Coverage{
	// args: []   out: dump of NewConfig() (the global config at start-up is the same)
	"default_conf": {Min: 1, GoFuncs: []string{"NewConfig", "pkg/config/config.go:init", "initByLDFlags", "GetGlobalConfig"}},
	// args: []   out: {pessimistic, trx}: dumps of DefaultPessimisticTxn(), DefaultTrxSummary()
	"defaults": {Min: 1, GoFuncs: []string{"DefaultPessimisticTxn", "DefaultTrxSummary", "NewAtomicBool"}},
	// args: [toml]   out: see the domain doc
	"load": {Min: 3000, GoFuncs: []string{"Config.Load", "Config.Valid", "ErrConfigValidationFailed.Error",
		"ErrConfigInstanceSection.Error", "nullableBool.UnmarshalText", "AtomicBool.UnmarshalText",
		"TrxSummary.Valid", "hasRootPrivilege"}},
	// args: [path]   out: Load error of a missing file
	"load_missing": {Min: 1},
	// args: [toml]   out: RemovedVariableCheck error
	"removed_check": {Min: 100, GoFuncs: []string{"Config.RemovedVariableCheck"}},
	// args: [toml]   out: {json, err}: GetJSONConfig with the loaded config as the global
	"json_config": {Min: 100, GoFuncs: []string{"GetJSONConfig", "StoreGlobalConfig", "RestoreFunc",
		"nullableBool.MarshalJSON", "AtomicBool.MarshalText"}},
	// args: [s]   out: bool
	"contain_hidden": {Min: 50, GoFuncs: []string{"ContainHiddenConfig"}},
	// args: [toml]   out: {conf, err}: dump of CloneConf of the loaded config
	"clone": {Min: 100, GoFuncs: []string{"CloneConf", "nullableBool.UnmarshalJSON"}},
	// args: [toml a, toml b]   out: {accepted, rejected, conf}: MergeConfigItems(a, b), dump of a
	"merge": {Min: 200, GoFuncs: []string{"MergeConfigItems", "mergeConfigItems"}},
	// args: [json]   out: json.Marshal(FlattenConfigItems(json.Unmarshal(json)))
	"flatten": {Min: 20, GoFuncs: []string{"FlattenConfigItems", "flatten"}},
	// args: [text]   out: {err, valid, true, text, json}: nullableBool.UnmarshalText then Marshal*
	"nb_text": {Min: 9, GoFuncs: []string{"nullableBool.MarshalText"}},
	// args: [json]   out: {err, valid, true}
	"nb_json": {Min: 9},
	// args: [text]   out: {err, value, text}
	"ab_text": {Min: 9},
	// args: [toml]   out: fields of Log.ToLogConfig()
	"log_config": {Min: 50, GoFuncs: []string{"Log.ToLogConfig", "Log.getDisableTimestamp",
		"Log.getDisableErrorStack", "nullableBool.toBool"}},
	// args: [toml]   out: fields of OpenTracing.ToTracingConfig()
	"tracing_config": {Min: 20, GoFuncs: []string{"OpenTracing.ToTracingConfig"}},
	// args: [toml]   out: dump of GetTiKVConfig()
	"tikv_config": {Min: 20, GoFuncs: []string{"Config.GetTiKVConfig", "Security.ClusterSecurity"}},
	// args: [toml]   out: the global helpers with the loaded config set by UpdateGlobal
	"global": {Min: 20, GoFuncs: []string{"UpdateGlobal", "TableLockEnabled", "GetGlobalKeyspaceName",
		"GetTxnScopeFromConfig"}},
	// args: [host, port, status host, status port, tmp-storage-path ("" = default)]   out: path
	"temp_storage": {Min: 8, GoFuncs: []string{"Config.UpdateTempStoragePath", "encodeDefTempStorageDir"}},
	// args: ["plain", msg] | ["std", code] (dbterror.ClassExecutor.NewStd; codes registered for the class)   out: label
	"error_label": {Min: 5, GoFuncs: []string{"ExecuteErrorToLabel"}},
	// Go runtime and standard library used by the config code (gostd in tidb-rs):
	// args: [float64 bits as decimal string, target type]   out: the conversion T(f) as a
	// decimal string (mapping rule M-F2I: out-of-range results depend on the target).
	"f2i": {Min: 2000},
	// args: []   out: [[r, SimpleFold(r)], ...] for every rune with SimpleFold(r) != r
	"simple_fold_table": {Min: 1},
	// args: []   out: [[r, ToUpper(r), ToLower(r)], ...] for every rune changed by either
	"case_table": {Min: 1},
	// args: [a, b]   out: strings.EqualFold(a, b)
	"equal_fold": {Min: 300},
}

var configExempt = map[string]string{
	"InitializeConfig":        "calls os.Exit and terror.MustNil and writes stderr; the Rust port returns the exit code (unit tests)",
	"isAllRemovedConfigItems": "unexported; only caller is InitializeConfig (exempt); Rust unit tests",
}

// ---------------------------------------------------------------- dump

// confDump writes "Path=value" lines for every exported field (Go field names,
// embedded structs by their type name). Values: %q for strings, %d for
// integers and time.Duration, strconv 'g' for floats, %v for bools and
// AtomicBool, nil / [%q ...] for []string, nil / map[%q:%q ...] (sorted) for
// map[string]string. nullableBool is a struct (IsValid, IsTrue).
func confDump(v reflect.Value, path string, out *[]string) {
	add := func(s string) { *out = append(*out, path+"="+s) }
	if v.Type() == reflect.TypeOf(time.Duration(0)) {
		add(strconv.FormatInt(v.Int(), 10))
		return
	}
	if v.Type() == reflect.TypeOf(config.AtomicBool{}) {
		ab := v.Interface().(config.AtomicBool)
		add(fmt.Sprintf("%v", ab.Load()))
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		add(fmt.Sprintf("%v", v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		add(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		add(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float64:
		add(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	case reflect.String:
		add(fmt.Sprintf("%q", v.String()))
	case reflect.Slice:
		if v.IsNil() {
			add("nil")
			return
		}
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = fmt.Sprintf("%q", v.Index(i).String())
		}
		add("[" + strings.Join(parts, " ") + "]")
	case reflect.Map:
		if v.IsNil() {
			add("nil")
			return
		}
		keys := make([]string, 0, v.Len())
		for _, k := range v.MapKeys() {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%q:%q", k, v.MapIndex(reflect.ValueOf(k)).String())
		}
		add("map[" + strings.Join(parts, " ") + "]")
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			confDump(v.Field(i), path+"."+f.Name, out)
		}
	default:
		panic("confDump: unsupported kind " + v.Kind().String() + " at " + path)
	}
}

var tempStorageDirName = config.NewConfig().TempStoragePath

// envClean replaces the default tmp-storage-path (os.TempDir() and the user
// ID) by a placeholder. Only that exact string is replaced: other paths such
// as "/tmp/tidb" must stay (os.TempDir() is "/tmp" on Linux).
func envClean(s string) string {
	return strings.ReplaceAll(s, tempStorageDirName, "<TEMP_STORAGE_DIR>")
}

// tempClean: a path built by encodeDefTempStorageDir, with os.TempDir() as a
// leading <TMPDIR> and the <uid>_tidb directory as <UID>_tidb.
func tempClean(p string) string {
	if tmp := filepath.Clean(os.TempDir()); strings.HasPrefix(p, tmp+"/") {
		p = "<TMPDIR>" + p[len(tmp):]
	}
	if u, err := user.Current(); err == nil {
		parts := strings.Split(p, "/")
		for i, part := range parts {
			if part == u.Uid+"_tidb" {
				parts[i] = "<UID>_tidb"
			}
		}
		p = strings.Join(parts, "/")
	}
	return p
}

func dump(v any, root string) []vec.GoString {
	var lines []string
	confDump(reflect.Indirect(reflect.ValueOf(v)), root, &lines)
	out := make([]vec.GoString, len(lines))
	for i, l := range lines {
		out[i] = vec.GoString(envClean(l))
	}
	return out
}

// freshConfig is NewConfig() with its slices and maps copied: NewConfig copies
// defaultConf shallowly, so in Go a Load that writes into a default slice of
// sufficient capacity (or the default Labels map) mutates defaultConf itself.
// The Rust port has value semantics; the vectors start from an unaliased copy.
func freshConfig() *config.Config {
	c := config.NewConfig()
	unalias(reflect.ValueOf(c).Elem())
	return c
}

func unalias(v reflect.Value) {
	switch v.Kind() {
	case reflect.Slice:
		if !v.IsNil() {
			n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			reflect.Copy(n, v)
			v.Set(n)
		}
	case reflect.Map:
		if !v.IsNil() {
			n := reflect.MakeMapWithSize(v.Type(), v.Len())
			for _, k := range v.MapKeys() {
				n.SetMapIndex(k, v.MapIndex(k))
			}
			v.Set(n)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				unalias(v.Field(i))
			}
		}
	}
}

// ---------------------------------------------------------------- load

const confName = "govec-config.toml"

var confDir string

// confPath writes data to <tmp>/govec-config.toml and returns the path.
func confPath(data string) string {
	if confDir == "" {
		d, err := os.MkdirTemp("", "govec-config")
		if err != nil {
			panic(err)
		}
		confDir = d
	}
	p := filepath.Join(confDir, confName)
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		panic(err)
	}
	return p
}

func errMsg(err error, path string) any {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if path != "" {
		msg = strings.ReplaceAll(msg, path, confName)
	}
	return vec.GoString(envClean(msg))
}

func sections(s []config.InstanceConfigSection) []any {
	out := []any{}
	for _, sec := range s {
		var keys []string
		for k := range sec.NameMappings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := []any{}
		for _, k := range keys {
			pairs = append(pairs, []string{k, sec.NameMappings[k]})
		}
		out = append(out, map[string]any{"section": sec.SectionName, "mappings": pairs})
	}
	return out
}

// loadConf loads data into a fresh config (Load's error is returned).
func loadConf(data string) (*config.Config, error, string) {
	config.ConflictOptions = nil
	config.DeprecatedOptions = nil
	p := confPath(data)
	c := freshConfig()
	err := c.Load(p)
	return c, err, p
}

func loadRecord(data string) map[string]any {
	c, err, p := loadConf(data)
	out := map[string]any{"load_err": errMsg(err, p)}
	switch e := err.(type) {
	case nil:
		out["load_err_type"] = ""
	case *config.ErrConfigValidationFailed:
		out["load_err_type"] = "validation"
		items := []vec.GoString{}
		for _, it := range e.UndecodedItems {
			items = append(items, vec.GoString(it))
		}
		out["undecoded"] = items
	case *config.ErrConfigInstanceSection:
		out["load_err_type"] = "instance"
	default:
		// A decode error: Go decodes TOML tables in random order, so which keys
		// were set before the error is not reproducible; InitializeConfig
		// panics on it (terror.MustNil). Only the error is recorded.
		out["load_err_type"] = "other"
		return out
	}
	out["conflict"] = sections(config.ConflictOptions)
	out["deprecated"] = sections(config.DeprecatedOptions)
	out["conf"] = dump(c, "Config")
	verr := c.Valid()
	out["valid_err"] = errMsg(verr, p)
	out["conf_valid"] = dump(c, "Config")
	return out
}

// ---------------------------------------------------------------- exec

func configExec(op string, args []json.RawMessage) (any, error) {
	str := func(i int) string {
		var s vec.GoString
		if err := json.Unmarshal(args[i], &s); err != nil {
			panic(err)
		}
		return string(s)
	}
	switch op {
	case "default_conf":
		g := config.GetGlobalConfig()
		return map[string]any{"new": dump(config.NewConfig(), "Config"), "global": dump(g, "Config")}, nil
	case "defaults":
		p := config.DefaultPessimisticTxn()
		t := config.DefaultTrxSummary()
		return map[string]any{"pessimistic": dump(&p, "PessimisticTxn"), "trx": dump(&t, "TrxSummary"),
			"atomic_true": vec.GoString(mustText(config.NewAtomicBool(true)))}, nil
	case "load":
		return loadRecord(str(0)), nil
	case "load_missing":
		c := freshConfig()
		return map[string]any{"err": errMsg(c.Load(str(0)), "")}, nil
	case "removed_check":
		p := confPath(str(0))
		c := freshConfig()
		return map[string]any{"err": errMsg(c.RemovedVariableCheck(p), p)}, nil
	case "json_config":
		c, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		restore := config.RestoreFunc()
		config.StoreGlobalConfig(c)
		s, err := config.GetJSONConfig()
		restore()
		return map[string]any{"json": vec.GoString(envClean(s)), "err": errMsg(err, "")}, nil
	case "contain_hidden":
		return config.ContainHiddenConfig(str(0)), nil
	case "clone":
		c, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		cl, err := config.CloneConf(c)
		out := map[string]any{"err": errMsg(err, "")}
		if err == nil {
			out["conf"] = dump(cl, "Config")
		}
		return out, nil
	case "merge":
		a, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		b, err, p := loadConf(str(1))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		acc, rej := config.MergeConfigItems(a, b)
		if acc == nil {
			acc = []string{}
		}
		if rej == nil {
			rej = []string{}
		}
		return map[string]any{"accepted": acc, "rejected": rej, "conf": dump(a, "Config")}, nil
	case "flatten":
		var m map[string]any
		if err := json.Unmarshal([]byte(str(0)), &m); err != nil {
			panic(err)
		}
		b, err := json.Marshal(config.FlattenConfigItems(m))
		if err != nil {
			panic(err)
		}
		return vec.GoString(envClean(string(b))), nil
	case "nb_text":
		var c config.Config
		b := &c.Log.EnableTimestamp
		err := b.UnmarshalText([]byte(str(0)))
		text, _ := b.MarshalText()
		js, _ := b.MarshalJSON()
		return map[string]any{"err": errMsg(err, ""), "valid": b.IsValid, "true": b.IsTrue,
			"text": vec.GoString(text), "json": vec.GoString(js)}, nil
	case "nb_json":
		var c config.Config
		b := &c.Log.EnableTimestamp
		*b = struct{ IsValid, IsTrue bool }{true, true}
		err := b.UnmarshalJSON([]byte(str(0)))
		return map[string]any{"err": err != nil, "valid": b.IsValid, "true": b.IsTrue}, nil
	case "ab_text":
		b := config.NewAtomicBool(true)
		err := b.UnmarshalText([]byte(str(0)))
		return map[string]any{"err": errMsg(err, ""), "value": b.Load(), "text": vec.GoString(mustText(b))}, nil
	case "log_config":
		c, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		lc := c.Log.ToLogConfig()
		return map[string]any{
			"level": vec.GoString(lc.Level), "format": vec.GoString(lc.Format),
			"disable_timestamp": lc.DisableTimestamp, "file": dump(&lc.File, "File"),
			"disable_error_verbose": lc.DisableErrorVerbose, "timeout": lc.Timeout,
			"slow_query_file": vec.GoString(lc.SlowQueryFile), "general_log_file": vec.GoString(lc.GeneralLogFile),
		}, nil
	case "tracing_config":
		c, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		t := c.OpenTracing.ToTracingConfig()
		return map[string]any{
			"disabled": t.Disabled, "rpc_metrics": t.RPCMetrics,
			"reporter": []any{t.Reporter.QueueSize, int64(t.Reporter.BufferFlushInterval), t.Reporter.LogSpans,
				vec.GoString(t.Reporter.LocalAgentHostPort)},
			"sampler": []any{vec.GoString(t.Sampler.Type), strconv.FormatFloat(t.Sampler.Param, 'g', -1, 64),
				vec.GoString(t.Sampler.SamplingServerURL), t.Sampler.MaxOperations,
				int64(t.Sampler.SamplingRefreshInterval)},
		}, nil
	case "tikv_config":
		c, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		return dump(c.GetTiKVConfig(), "TikvConfig"), nil
	case "global":
		c, err, p := loadConf(str(0))
		if isDecodeErr(err) {
			return map[string]any{"decode_err": errMsg(err, p)}, nil
		}
		restore := config.RestoreFunc()
		config.UpdateGlobal(func(conf *config.Config) { *conf = *c })
		out := map[string]any{
			"table_lock": config.TableLockEnabled(), "delay_clean": config.TableLockDelayClean(),
			"keyspace": vec.GoString(config.GetGlobalKeyspaceName()), "txn_scope": vec.GoString(config.GetTxnScopeFromConfig()),
		}
		restore()
		return out, nil
	case "temp_storage":
		var a []any
		for i := range args {
			var x any
			if err := json.Unmarshal(args[i], &x); err != nil {
				panic(err)
			}
			a = append(a, x)
		}
		c := freshConfig()
		c.Host = a[0].(string)
		c.Port = uint(a[1].(float64))
		c.Status.StatusHost = a[2].(string)
		c.Status.StatusPort = uint(a[3].(float64))
		if p := a[4].(string); p != "" {
			c.TempStoragePath = p
		}
		c.UpdateTempStoragePath()
		return vec.GoString(tempClean(c.TempStoragePath)), nil
	case "f2i":
		bits, err := strconv.ParseUint(str(0), 10, 64)
		if err != nil {
			panic(err)
		}
		return f2i(math.Float64frombits(bits), str(1)), nil
	case "simple_fold_table":
		out := [][2]int32{}
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if f := unicode.SimpleFold(r); f != r {
				out = append(out, [2]int32{r, f})
			}
		}
		return out, nil
	case "case_table":
		out := [][3]int32{}
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if u, l := unicode.ToUpper(r), unicode.ToLower(r); u != r || l != r {
				out = append(out, [3]int32{r, u, l})
			}
		}
		return out, nil
	case "equal_fold":
		return strings.EqualFold(str(0), str(1)), nil
	case "error_label":
		var err error
		switch str(0) {
		case "plain":
			err = errors.New(str(1))
		case "std":
			code, _ := strconv.Atoi(str(1))
			err = dbterror.ClassExecutor.NewStd(terror.ErrCode(code))
		}
		return metrics.ExecuteErrorToLabel(err), nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// f2i converts f to the named integer type at run time (a variable, so that gc
// emits the target's conversion instructions; M-F2I).
func f2i(f float64, typ string) string {
	switch typ {
	case "int64":
		return strconv.FormatInt(int64(f), 10)
	case "int":
		return strconv.FormatInt(int64(int(f)), 10)
	case "int32":
		return strconv.FormatInt(int64(int32(f)), 10)
	case "int16":
		return strconv.FormatInt(int64(int16(f)), 10)
	case "int8":
		return strconv.FormatInt(int64(int8(f)), 10)
	case "uint64":
		return strconv.FormatUint(uint64(f), 10)
	case "uint":
		return strconv.FormatUint(uint64(uint(f)), 10)
	case "uint32":
		return strconv.FormatUint(uint64(uint32(f)), 10)
	case "uint16":
		return strconv.FormatUint(uint64(uint16(f)), 10)
	case "uint8":
		return strconv.FormatUint(uint64(uint8(f)), 10)
	}
	panic("f2i: unknown type " + typ)
}

func mustText(b *config.AtomicBool) string {
	t, err := b.MarshalText()
	if err != nil {
		panic(err)
	}
	return string(t)
}
