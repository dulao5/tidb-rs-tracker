package domains

// Domain "base": block D00.2 (pkg/parser/mysql, pkg/parser/terror, pkg/errno,
// pkg/util/dbterror, kv.StoreType). Two kinds of records:
//
//   - table dumps (op "table"): the full content of a Go map/slice of the block,
//     e.g. mysql.MySQLErrName, sorted by key. tidb-rs generates these tables
//     from the Go source (govec/cmd/rsgen); the dumps check the generated Rust
//     against what Go has at run time.
//   - function calls: error construction and formatting (fmt.Sprintf with
//     dynamic arguments, redaction modes, terror classes, the registry), SQL
//     modes, privileges, charset and type helpers, FORMAT's en_US formatter.
//
// Typed arguments (the `...interface{}` of fmt and the errors API) are JSON
// objects {"t": <Go type>, "v": <value>}: t is string | int | int8 | int16 |
// int32 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float64 | float32 |
// bool | nil | bytes (v hex) | error (v = message; Go value errors.New(v)).
// Records are produced with errors.RedactLogEnabled = "OFF" unless an op takes
// a redaction mode argument ("OFF" | "ON" | "MARKER"); the mode is restored to
// "OFF" after each call.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/dulao5/tidb-rs-tracker/govec/internal/protos"
	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/errno"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/parser/format"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/util/dbterror"
)

const baseSeed = 20261107

func init() {
	vec.Register(&vec.Domain{
		Name:     "base",
		Seed:     baseSeed,
		Coverage: baseCoverage,
		Blocks:   []string{"D00.2"},
		Exempt:   baseExempt,
		Gzip:     true,
		Generate: baseGenerate,
		Exec:     baseExec,
	})
}

var baseCoverage = map[string]vec.Coverage{
	// args: [table name]   out: [[key, value], ...] sorted by key (see baseTables)
	"table": {Min: 25, GoFuncs: []string{"Message"}},
	// args: [format, [typed args]]   out: fmt.Sprintf result
	"sprintf": {Min: 1500},
	// args: [[typed args]]   out: fmt.Sprint result
	"sprint": {Min: 20},
	// args: [code, [typed args], mode]   out: SQLError {code, state, message, error}
	"new_err": {Min: 500, GoFuncs: []string{"NewErr", "SQLError.Error"}},
	// args: [code, format, [redact pos], [typed args], mode]   out: SQLError
	"new_errf": {Min: 50, GoFuncs: []string{"NewErrf", "SQLError.Error"}},
	// args: [class, code, [typed args], mode]   out: Terr (terror.ErrClass.NewStd + GenWithStackByArgs)
	"terror_new_std": {Min: 300, GoFuncs: []string{"ErrClass.NewStd", "ErrClass.NewStdErr", "ErrClass.initError",
		"code2ErrClassMap.Put", "frozen", "ErrClass.String", "RegisterErrorClass", "newCode2ErrClassMap", "init"}},
	// args: [class, code, [typed args], mode]   out: Terr (dbterror.ErrClass.NewStd)
	"dbterror_new_std": {Min: 300, GoFuncs: []string{"ErrClass.NewStd"}},
	// args: ["<go file>:<name>", [typed args], mode]   out: Terr of the prototype with args
	"prototype": {Min: 395},
	// args: [class, code, message]   out: Terr
	"synthesize": {Min: 20, GoFuncs: []string{"ErrClass.Synthesize"}},
	// args: [class, code, message]   out: SQLError of terror.ToSQLError(Synthesize(...))
	"to_sql_error": {Min: 100, GoFuncs: []string{"ToSQLError", "getMySQLErrorCode", "code2ErrClassMap.Get"}},
	// args: [class, rfc class, code]   out: {equal_class, not_equal_class, get_err_class}
	"err_class": {Min: 50, GoFuncs: []string{"ErrClass.EqualClass", "ErrClass.NotEqualClass", "GetErrClass"}},
	// args: [a, b] each {"kind": "terr"|"plain"|"nil", "class", "code", "msg"}   out: {terror, errors}
	"error_equal": {Min: 30, GoFuncs: []string{"ErrorEqual", "ErrorNotEqual"}},
	// args: [s]   out: string
	"format_sql_mode_str": {Min: 50, GoFuncs: []string{"FormatSQLModeStr"}},
	// args: [s]   out: {mode, err}
	"get_sql_mode": {Min: 50, GoFuncs: []string{"GetSQLMode", "newInvalidModeErr"}},
	// args: [mode int64]   out: [15 bools in method order]
	"sql_mode_has": {Min: 40, GoFuncs: []string{"SQLMode.HasNoZeroDateMode", "SQLMode.HasNoZeroInDateMode",
		"SQLMode.HasErrorForDivisionByZeroMode", "SQLMode.HasOnlyFullGroupBy", "SQLMode.HasStrictMode",
		"SQLMode.HasPipesAsConcatMode", "SQLMode.HasNoUnsignedSubtractionMode", "SQLMode.HasHighNotPrecedenceMode",
		"SQLMode.HasANSIQuotesMode", "SQLMode.HasRealAsFloatMode", "SQLMode.HasPadCharToFullLengthMode",
		"SQLMode.HasNoBackslashEscapesMode", "SQLMode.HasIgnoreSpaceMode", "SQLMode.HasNoAutoCreateUserMode",
		"SQLMode.HasAllowInvalidDatesMode"}},
	// args: [a, b int64]   out: [del, set]
	"sql_mode_set": {Min: 20, GoFuncs: []string{"DelSQLMode", "SetSQLMode"}},
	// args: [s]   out: int
	"str2priority": {Min: 7, GoFuncs: []string{"Str2Priority"}},
	// args: [priority int]   out: restored text; err for unknown values
	"priority_restore": {Min: 5, GoFuncs: []string{"PriorityEnum.Restore"}},
	// args: [status uint16]   out: bool
	"has_cursor_exists_flag": {Min: 5, GoFuncs: []string{"HasCursorExistsFlag"}},
	// args: [flag uint]   out: [16 bools in source order]
	"type_flags": {Min: 40, GoFuncs: []string{"HasDropColumnWithIndexFlag", "HasNotNullFlag", "HasNoDefaultValueFlag",
		"HasAutoIncrementFlag", "HasUnsignedFlag", "HasZerofillFlag", "HasBinaryFlag", "HasPriKeyFlag", "HasUniKeyFlag",
		"HasMultipleKeyFlag", "HasTimestampFlag", "HasOnUpdateNowFlag", "HasParseToJSONFlag", "HasIsBooleanFlag",
		"HasPreventNullInsertFlag", "HasEnumSetAsIntFlag"}},
	// args: [name]   out: {id, utf8}
	"charset": {Min: 40, GoFuncs: []string{"CharsetNameToID", "IsUTF8Charset"}},
	// args: [rune]   out: {graph: IsOneOf(RangeGraph), digit: unicode.IsDigit}
	"unicode": {Min: 2000},
	// args: [col or set string]   out: {col: [p, ok], set: [p, ok]}
	"new_priv": {Min: 40, GoFuncs: []string{"NewPrivFromColumn", "NewPrivFromSetEnum"}},
	// args: [priv uint64]   out: [String, ColumnString, SetString]
	"priv_strings": {Min: 36, GoFuncs: []string{"PrivilegeType.String", "PrivilegeType.ColumnString", "PrivilegeType.SetString"}},
	// args: [list name, priv]   out: bool
	"privileges_has": {Min: 50, GoFuncs: []string{"Privileges.Has"}},
	// args: [tp byte]   out: {int, flen, dec, cast_flen, cast_dec}
	"type_defaults": {Min: 256, GoFuncs: []string{"IsIntegerType", "GetDefaultFieldLengthAndDecimal",
		"GetDefaultFieldLengthAndDecimalForCast"}},
	// args: [plugin]   out: bool
	"auth_clear_text": {Min: 10, GoFuncs: []string{"IsAuthPluginClearText"}},
	// args: [locale, number, precision]   out: string (err / panic)
	"format_locale": {Min: 200, GoFuncs: []string{"GetLocaleFormatFunction", "formatENUS", "formatZHCN", "formatNotSupport"}},
	// args: [[[op, code, user, host], ...]]   out: {global, users, hosts} counts (times dropped)
	"errno_stats": {Min: 10, GoFuncs: []string{"FlushStats", "GlobalStats", "UserStats", "HostStats", "copyMap",
		"initCounters", "IncrementError", "IncrementWarning"}},
	// args: [store type byte]   out: name
	"store_type": {Min: 5, GoFuncs: []string{"StoreType.Name"}},
}

var baseExempt = map[string]string{
	"Call":           "logs a function's error (pingcap/log); no result",
	"Log":            "logs an error (pingcap/log); no result",
	"MustNil":        "log.Fatal: exits the process",
	"RegisterFinish": "freezes the registry for the rest of the process (later registrations panic)",
	"ErrClass.New":   "deprecated; same path as NewStdErr with a literal message (no caller in the read path)",
	// pkg/config: block D00.2 part 2 (separate PR, see specs/D00.2-base/spec.md).
	"AtomicBool.MarshalText": "config (D00.2 part 2)", "AtomicBool.UnmarshalText": "config (D00.2 part 2)",
	"Config.GetTiKVConfig": "config (D00.2 part 2)", "Config.Load": "config (D00.2 part 2)",
	"Config.RemovedVariableCheck": "config (D00.2 part 2)", "Config.UpdateTempStoragePath": "config (D00.2 part 2)",
	"Config.Valid": "config (D00.2 part 2)", "ContainHiddenConfig": "config (D00.2 part 2)",
	"DefaultPessimisticTxn": "config (D00.2 part 2)", "DefaultTrxSummary": "config (D00.2 part 2)",
	"ErrConfigInstanceSection.Error": "config (D00.2 part 2)", "ErrConfigValidationFailed.Error": "config (D00.2 part 2)",
	"GetGlobalConfig": "config (D00.2 part 2)", "GetGlobalKeyspaceName": "config (D00.2 part 2)",
	"GetJSONConfig": "config (D00.2 part 2)", "InitializeConfig": "config (D00.2 part 2)",
	"Log.ToLogConfig": "config (D00.2 part 2)", "Log.getDisableErrorStack": "config (D00.2 part 2)",
	"Log.getDisableTimestamp": "config (D00.2 part 2)", "NewAtomicBool": "config (D00.2 part 2)",
	"NewConfig": "config (D00.2 part 2)", "OpenTracing.ToTracingConfig": "config (D00.2 part 2)",
	"RestoreFunc": "config (D00.2 part 2)", "Security.ClusterSecurity": "config (D00.2 part 2)",
	"StoreGlobalConfig": "config (D00.2 part 2)", "TableLockEnabled": "config (D00.2 part 2)",
	"TrxSummary.Valid": "config (D00.2 part 2)", "UpdateGlobal": "config (D00.2 part 2)",
	"encodeDefTempStorageDir": "config (D00.2 part 2)", "hasRootPrivilege": "config (D00.2 part 2)",
	"initByLDFlags": "config (D00.2 part 2)", "isAllRemovedConfigItems": "config (D00.2 part 2)",
	"nullableBool.MarshalJSON": "config (D00.2 part 2)", "nullableBool.MarshalText": "config (D00.2 part 2)",
	"nullableBool.UnmarshalJSON": "config (D00.2 part 2)", "nullableBool.UnmarshalText": "config (D00.2 part 2)",
	"nullableBool.toBool": "config (D00.2 part 2)", "CloneConf": "config (D00.2 part 2)",
	"FlattenConfigItems": "config (D00.2 part 2)", "GetTxnScopeFromConfig": "config (D00.2 part 2)",
	"MergeConfigItems": "config (D00.2 part 2)", "flatten": "config (D00.2 part 2)",
	"mergeConfigItems": "config (D00.2 part 2)",
}

// ---------------------------------------------------------------- typed args

type typedArg struct {
	T string          `json:"t"`
	V json.RawMessage `json:"v,omitempty"`
}

func ta(t string, v any) typedArg {
	if v == nil {
		return typedArg{T: t}
	}
	return typedArg{T: t, V: vec.MustJSON(v)}
}

func decodeArgs(raw json.RawMessage) []any {
	var tas []typedArg
	if err := json.Unmarshal(raw, &tas); err != nil {
		panic(err)
	}
	out := make([]any, len(tas))
	for i, a := range tas {
		out[i] = decodeArg(a)
	}
	return out
}

func decodeArg(a typedArg) any {
	num := func() json.Number {
		var n json.Number
		if err := json.Unmarshal(a.V, &n); err != nil {
			panic(err)
		}
		return n
	}
	sint := func(bits int) int64 { v, err := strconv.ParseInt(num().String(), 10, bits); must(err); return v }
	uint_ := func(bits int) uint64 { v, err := strconv.ParseUint(num().String(), 10, bits); must(err); return v }
	str := func() string {
		var s string
		must(json.Unmarshal(a.V, &s))
		return s
	}
	switch a.T {
	case "string":
		return str()
	case "int":
		return int(sint(64))
	case "int8":
		return int8(sint(8))
	case "int16":
		return int16(sint(16))
	case "int32":
		return int32(sint(32))
	case "int64":
		return sint(64)
	case "uint":
		return uint(uint_(64))
	case "uint8":
		return uint8(uint_(8))
	case "uint16":
		return uint16(uint_(16))
	case "uint32":
		return uint32(uint_(32))
	case "uint64":
		return uint_(64)
	case "float64":
		f, err := strconv.ParseFloat(str(), 64)
		must(err)
		return f
	case "float32":
		f, err := strconv.ParseFloat(str(), 32)
		must(err)
		return float32(f)
	case "bool":
		var b bool
		must(json.Unmarshal(a.V, &b))
		return b
	case "nil":
		return nil
	case "bytes":
		b, err := hex.DecodeString(str())
		must(err)
		return b
	case "error":
		return errors.New(str())
	}
	panic("base: unknown typed arg " + a.T)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func withRedact(mode string, f func()) {
	errors.RedactLogEnabled.Store(mode)
	defer errors.RedactLogEnabled.Store(errors.RedactLogDisable)
	f()
}

// ---------------------------------------------------------------- outputs

// Terr is a *terror.Error: code, RFC code, message template, redaction
// positions, GetMsg and Error() of the error after GenWithStackByArgs.
type Terr struct {
	Code     int    `json:"code"`
	RFC      string `json:"rfc"`
	Template string `json:"template"`
	Redact   []int  `json:"redact"`
	Msg      string `json:"msg"`
	Error    string `json:"error"`
}

func terrOf(e *errors.Error, gen error) Terr {
	g := errors.Cause(gen).(*errors.Error)
	r := redactPos(e)
	if r == nil {
		r = []int{}
	}
	return Terr{Code: int(e.Code()), RFC: string(e.RFCCode()), Template: e.MessageTemplate(), Redact: r,
		Msg: g.GetMsg(), Error: g.Error()}
}

// SQLErr is a *mysql.SQLError.
type SQLErr struct {
	Code    uint16 `json:"code"`
	State   string `json:"state"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func sqlErrOf(e *mysql.SQLError) SQLErr {
	return SQLErr{e.Code, e.State, e.Message, e.Error()}
}

// ---------------------------------------------------------------- tables

type kvPair [2]any

func sortedPairs[K comparable, V any](m map[K]V, less func(a, b K) bool, val func(V) any) []kvPair {
	var ks []K
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return less(ks[i], ks[j]) })
	out := make([]kvPair, 0, len(ks))
	for _, k := range ks {
		out = append(out, kvPair{k, val(m[k])})
	}
	return out
}

func lessU16(a, b uint16) bool { return a < b }
func lessStr(a, b string) bool { return a < b }
func msgVal(m *mysql.ErrMessage) any {
	r := m.RedactArgPos
	if r == nil {
		r = []int{}
	}
	return []any{m.Raw, r}
}
func id[V any](v V) any { return v }

// baseTables: name -> dump. Values of named integer types are dumped as numbers.
var baseTables = map[string]func() any{
	"mysql.MySQLErrName": func() any { return sortedPairs(mysql.MySQLErrName, lessU16, msgVal) },
	"errno.MySQLErrName": func() any { return sortedPairs(errno.MySQLErrName, lessU16, msgVal) },
	"mysql.MySQLState":   func() any { return sortedPairs(mysql.MySQLState, lessU16, id[string]) },
	"mysql.CharsetIDs":   func() any { return sortedPairs(mysql.CharsetIDs, lessStr, id[uint8]) },
	"mysql.Collations": func() any {
		return sortedPairs(mysql.Collations, func(a, b uint16) bool { return a < b }, id[string])
	},
	"mysql.CollationNames": func() any { return sortedPairs(mysql.CollationNames, lessStr, id[uint16]) },
	"mysql.Command2Str": func() any {
		return sortedPairs(mysql.Command2Str, func(a, b byte) bool { return a < b }, id[string])
	},
	"mysql.DefaultLengthOfMysqlTypes": func() any {
		return sortedPairs(mysql.DefaultLengthOfMysqlTypes, func(a, b byte) bool { return a < b }, id[int])
	},
	"mysql.DefaultLengthOfTimeFraction": func() any {
		return sortedPairs(mysql.DefaultLengthOfTimeFraction, func(a, b int) bool { return a < b }, id[int])
	},
	"mysql.Str2SQLMode": func() any {
		return sortedPairs(mysql.Str2SQLMode, lessStr, func(v mysql.SQLMode) any { return int64(v) })
	},
	"mysql.CombinationSQLMode": func() any { return sortedPairs(mysql.CombinationSQLMode, lessStr, id[[]string]) },
	"mysql.Priority2Str": func() any {
		return sortedPairs(mysql.Priority2Str, func(a, b mysql.PriorityEnum) bool { return a < b }, id[string])
	},
	"mysql.DefaultAuthPlugins": func() any { return mysql.DefaultAuthPlugins },
	"mysql.Priv2Str":           func() any { return privMap(mysql.Priv2Str) },
	"mysql.Priv2SetStr":        func() any { return privMap(mysql.Priv2SetStr) },
	"mysql.Priv2UserCol":       func() any { return privMap(mysql.Priv2UserCol) },
	"mysql.SetStr2Priv": func() any {
		return sortedPairs(mysql.SetStr2Priv, lessStr, func(v mysql.PrivilegeType) any { return uint64(v) })
	},
	"mysql.Col2PrivType": func() any {
		return sortedPairs(mysql.Col2PrivType, lessStr, func(v mysql.PrivilegeType) any { return uint64(v) })
	},
	"mysql.AllGlobalPrivs":        func() any { return privList(mysql.AllGlobalPrivs) },
	"mysql.AllDBPrivs":            func() any { return privList(mysql.AllDBPrivs) },
	"mysql.AllTablePrivs":         func() any { return privList(mysql.AllTablePrivs) },
	"mysql.AllColumnPrivs":        func() any { return privList(mysql.AllColumnPrivs) },
	"mysql.StaticGlobalOnlyPrivs": func() any { return privList(mysql.StaticGlobalOnlyPrivs) },
	"terror.ErrClassToMySQLCodes": func() any { return protos.RegistrySnapshot() },
	"terror.classes": func() any {
		var out []kvPair
		for c := -1; c <= 30; c++ {
			out = append(out, kvPair{c, terror.ErrClass(c).String()})
		}
		return out
	},
	"dbterror.prototypes": func() any {
		var ks []string
		for k := range protos.Vars {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var out []kvPair
		for _, k := range ks {
			e := protos.Vars[k]
			out = append(out, kvPair{k, terrOf(e, e)})
		}
		return out
	},
}

func privMap(m map[mysql.PrivilegeType]string) any {
	return sortedPairs(m, func(a, b mysql.PrivilegeType) bool { return a < b }, id[string])
}

func privList(l mysql.Privileges) any {
	out := make([]uint64, len(l))
	for i, p := range l {
		out[i] = uint64(p)
	}
	return out
}

var privLists = map[string]mysql.Privileges{
	"AllGlobalPrivs": mysql.AllGlobalPrivs, "AllDBPrivs": mysql.AllDBPrivs, "AllTablePrivs": mysql.AllTablePrivs,
	"AllColumnPrivs": mysql.AllColumnPrivs, "StaticGlobalOnlyPrivs": mysql.StaticGlobalOnlyPrivs,
}

// ---------------------------------------------------------------- exec

func arg[T any](args []json.RawMessage, i int) T {
	var v T
	if err := json.Unmarshal(args[i], &v); err != nil {
		panic(fmt.Sprintf("arg %d: %v", i, err))
	}
	return v
}

func baseExec(op string, args []json.RawMessage) (out any, err error) {
	switch op {
	case "table":
		name := arg[string](args, 0)
		f, ok := baseTables[name]
		if !ok {
			panic("base: unknown table " + name)
		}
		return f(), nil
	case "sprintf":
		return fmt.Sprintf(arg[string](args, 0), decodeArgs(args[1])...), nil
	case "sprint":
		return fmt.Sprint(decodeArgs(args[0])...), nil
	case "new_err":
		var e *mysql.SQLError
		withRedact(arg[string](args, 2), func() { e = mysql.NewErr(arg[uint16](args, 0), decodeArgs(args[1])...) })
		return sqlErrOf(e), nil
	case "new_errf":
		var e *mysql.SQLError
		withRedact(arg[string](args, 4), func() {
			e = mysql.NewErrf(arg[uint16](args, 0), arg[string](args, 1), arg[[]int](args, 2), decodeArgs(args[3])...)
		})
		return sqlErrOf(e), nil
	case "terror_new_std", "dbterror_new_std":
		class, code := terror.ErrClass(arg[int](args, 0)), terror.ErrCode(arg[int](args, 1))
		var proto *errors.Error
		if op == "terror_new_std" {
			proto = class.NewStd(code)
		} else {
			proto = dbterror.ErrClass{ErrClass: class}.NewStd(code)
		}
		var t Terr
		withRedact(arg[string](args, 3), func() { t = terrOf(proto, proto.GenWithStackByArgs(decodeArgs(args[2])...)) })
		return t, nil
	case "prototype":
		proto, ok := protos.Vars[arg[string](args, 0)]
		if !ok {
			panic("base: unknown prototype")
		}
		var t Terr
		withRedact(arg[string](args, 2), func() { t = terrOf(proto, proto.GenWithStackByArgs(decodeArgs(args[1])...)) })
		return t, nil
	case "synthesize":
		e := terror.ErrClass(arg[int](args, 0)).Synthesize(terror.ErrCode(arg[int](args, 1)), arg[string](args, 2))
		return terrOf(e, e), nil
	case "to_sql_error":
		e := terror.ErrClass(arg[int](args, 0)).Synthesize(terror.ErrCode(arg[int](args, 1)), arg[string](args, 2))
		return sqlErrOf(terror.ToSQLError(e)), nil
	case "err_class":
		class := terror.ErrClass(arg[int](args, 0))
		e := errors.Normalize("m", errors.RFCCodeText(fmt.Sprintf("%s:%d", arg[string](args, 1), arg[int](args, 2))),
			errors.MySQLErrorCode(arg[int](args, 2)))
		return []any{class.EqualClass(e), class.NotEqualClass(e), int(terror.GetErrClass(e))}, nil
	case "error_equal":
		a, b := mkErr(args[0]), mkErr(args[1])
		return []bool{terror.ErrorEqual(a, b), terror.ErrorNotEqual(a, b), errors.ErrorEqual(a, b)}, nil
	case "format_sql_mode_str":
		return mysql.FormatSQLModeStr(arg[string](args, 0)), nil
	case "get_sql_mode":
		m, e := mysql.GetSQLMode(arg[string](args, 0))
		var se *SQLErr
		if e != nil {
			s := sqlErrOf(e.(*mysql.SQLError))
			se = &s
		}
		return []any{int64(m), se}, nil
	case "sql_mode_has":
		m := mysql.SQLMode(arg[int64](args, 0))
		return []bool{m.HasNoZeroDateMode(), m.HasNoZeroInDateMode(), m.HasErrorForDivisionByZeroMode(),
			m.HasOnlyFullGroupBy(), m.HasStrictMode(), m.HasPipesAsConcatMode(), m.HasNoUnsignedSubtractionMode(),
			m.HasHighNotPrecedenceMode(), m.HasANSIQuotesMode(), m.HasRealAsFloatMode(), m.HasPadCharToFullLengthMode(),
			m.HasNoBackslashEscapesMode(), m.HasIgnoreSpaceMode(), m.HasNoAutoCreateUserMode(),
			m.HasAllowInvalidDatesMode()}, nil
	case "sql_mode_set":
		a, b := mysql.SQLMode(arg[int64](args, 0)), mysql.SQLMode(arg[int64](args, 1))
		return []int64{int64(mysql.DelSQLMode(a, b)), int64(mysql.SetSQLMode(a, b))}, nil
	case "str2priority":
		return int(mysql.Str2Priority(arg[string](args, 0))), nil
	case "priority_restore":
		p := mysql.PriorityEnum(arg[int](args, 0))
		var sb strings.Builder
		e := p.Restore(format.NewRestoreCtx(format.DefaultRestoreFlags, &sb))
		return sb.String(), e
	case "has_cursor_exists_flag":
		return mysql.HasCursorExistsFlag(arg[uint16](args, 0)), nil
	case "type_flags":
		f := arg[uint](args, 0)
		return []bool{mysql.HasDropColumnWithIndexFlag(f), mysql.HasNotNullFlag(f), mysql.HasNoDefaultValueFlag(f),
			mysql.HasAutoIncrementFlag(f), mysql.HasUnsignedFlag(f), mysql.HasZerofillFlag(f), mysql.HasBinaryFlag(f),
			mysql.HasPriKeyFlag(f), mysql.HasUniKeyFlag(f), mysql.HasMultipleKeyFlag(f), mysql.HasTimestampFlag(f),
			mysql.HasOnUpdateNowFlag(f), mysql.HasParseToJSONFlag(f), mysql.HasIsBooleanFlag(f),
			mysql.HasPreventNullInsertFlag(f), mysql.HasEnumSetAsIntFlag(f)}, nil
	case "charset":
		n := arg[string](args, 0)
		return []any{mysql.CharsetNameToID(n), mysql.IsUTF8Charset(n)}, nil
	case "unicode":
		r := rune(arg[int32](args, 0))
		return []bool{unicode.IsOneOf(mysql.RangeGraph, r), unicode.IsDigit(r)}, nil
	case "new_priv":
		s := arg[string](args, 0)
		p1, ok1 := mysql.NewPrivFromColumn(s)
		p2, ok2 := mysql.NewPrivFromSetEnum(s)
		return []any{uint64(p1), ok1, uint64(p2), ok2}, nil
	case "priv_strings":
		p := mysql.PrivilegeType(arg[uint64](args, 0))
		return []string{p.String(), p.ColumnString(), p.SetString()}, nil
	case "privileges_has":
		return privLists[arg[string](args, 0)].Has(mysql.PrivilegeType(arg[uint64](args, 1))), nil
	case "type_defaults":
		tp := arg[byte](args, 0)
		f1, d1 := mysql.GetDefaultFieldLengthAndDecimal(tp)
		f2, d2 := mysql.GetDefaultFieldLengthAndDecimalForCast(tp)
		return []any{mysql.IsIntegerType(tp), f1, d1, f2, d2}, nil
	case "auth_clear_text":
		return mysql.IsAuthPluginClearText(arg[string](args, 0)), nil
	case "format_locale":
		f := mysql.GetLocaleFormatFunction(arg[string](args, 0))
		return f(arg[string](args, 1), arg[string](args, 2))
	case "errno_stats":
		errno.FlushStats()
		for _, step := range arg[[][]any](args, 0) {
			code, user, host := uint16(step[1].(float64)), step[2].(string), step[3].(string)
			if step[0] == "error" {
				errno.IncrementError(code, user, host)
			} else {
				errno.IncrementWarning(code, user, host)
			}
		}
		return statsOut(), nil
	case "store_type":
		return kv.StoreType(arg[uint8](args, 0)).Name(), nil
	}
	panic("base: unknown op " + op)
}

// mkErr builds an error from {"kind","class","code","msg"}.
func mkErr(raw json.RawMessage) error {
	var a struct {
		Kind  string `json:"kind"`
		Class int    `json:"class"`
		Code  int    `json:"code"`
		Msg   string `json:"msg"`
	}
	must(json.Unmarshal(raw, &a))
	switch a.Kind {
	case "terr":
		return terror.ErrClass(a.Class).Synthesize(terror.ErrCode(a.Code), a.Msg)
	case "plain":
		return errors.New(a.Msg)
	}
	return nil
}

type statCounts struct {
	Key  string `json:"key"`
	Code uint16 `json:"code"`
	Err  int    `json:"errors"`
	Warn int    `json:"warnings"`
	Seen bool   `json:"last_seen_set"`
}

func statsOut() []statCounts {
	out := []statCounts{}
	add := func(key string, m map[uint16]*errno.ErrorSummary) {
		var codes []int
		for c := range m {
			codes = append(codes, int(c))
		}
		sort.Ints(codes)
		for _, c := range codes {
			s := m[uint16(c)]
			out = append(out, statCounts{key, uint16(c), s.ErrorCount, s.WarningCount, !s.LastSeen.IsZero()})
		}
	}
	add("global", errno.GlobalStats())
	for _, kind := range []string{"user", "host"} {
		var m map[string]map[uint16]*errno.ErrorSummary
		if kind == "user" {
			m = errno.UserStats()
		} else {
			m = errno.HostStats()
		}
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			add(kind+":"+k, m[k])
		}
	}
	return out
}

// redactPos reads the unexported redactArgsPos of an *errors.Error.
func redactPos(e *errors.Error) []int {
	f := reflect.ValueOf(e).Elem().FieldByName("redactArgsPos")
	var out []int
	for i := 0; i < f.Len(); i++ {
		out = append(out, int(f.Index(i).Int()))
	}
	return out
}
