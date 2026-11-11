package domains

// D05.2 PR F: the last SessionVars pieces (tidb-rs specs/D05.2-sessionvars/plan.md "PR F"),
// plus the D00.3 files they use (pkg/util/redact, pkg/util/kvcache SimpleLRUCache).
//   - redact: redact.String / WriteRedact / Value / Key (under each RedactLogEnabled mode) /
//     DeRedact (remove or keep) on one input.
//   - lazytext: LazyStmtText.Update (redact mode, SQL, params) then String twice, with an
//     optional SetText in between; also String on a nil receiver.
//   - lru: a SimpleLRUCache (quota 0) of a capacity, a list of operations (put/get/delete/
//     deleteAll/size/keys/values/setCapacity/removeOldest); evictions are logged.
//   - stmtcache: the session's non-prepared plan-cache stmt cache and prepare dedup cache
//     (SessionPlanCacheSize from tidb_session_plan_cache_size) under a list of add/get calls.
//   - parsecfg: SET of the parser-related variables, then BuildParserConfig and
//     GetParseParams.
//   - slowwarn: CollectWarningsForSlowLog on a statement context with warnings and extra
//     warnings (terror errors of several classes, plain errors).
//   - matchers: MatchEqual / matchGE / matchZero / uint64FromNonNegative / ParseString on a
//     threshold and a value of each Go type.
//   - sessstates: a session with user vars, types, status, current DB, last query/DDL info,
//     found rows, sequence values, plan-cache/binding flags, resource group, hypo indexes,
//     affected rows, last insert ID and warnings; EncodeSessionStates, then
//     DecodeSessionStates into a fresh session, and both are dumped.
// GetTotalCostDuration / GetExecuteDuration read the clock (time.Since): exempt from the
// oracle (the Rust port takes `now`, unit-tested).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/sessionctx/sessionstates"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/dbterror"
	"github.com/pingcap/tidb/pkg/util/kvcache"
	"github.com/pingcap/tidb/pkg/util/redact"
)

//go:linkname svfMatchZero github.com/pingcap/tidb/pkg/sessionctx/variable.matchZero
func svfMatchZero(threshold any) bool

//go:linkname svfUint64FromNonNegative github.com/pingcap/tidb/pkg/sessionctx/variable.uint64FromNonNegative
func svfUint64FromNonNegative(v int64) (uint64, bool)

var svfRedactFuncs = []string{"String", "Stringer", "redactStringer.String", "WriteRedact", "Value", "Key",
	"NeedRedact", "InitRedact", "DeRedact"}

var svfLRUFuncs = []string{"NewSimpleLRUCache", "SimpleLRUCache.SetOnEvict", "SimpleLRUCache.Get",
	"SimpleLRUCache.Put", "SimpleLRUCache.Delete", "SimpleLRUCache.DeleteAll", "SimpleLRUCache.Size",
	"SimpleLRUCache.Values", "SimpleLRUCache.Keys", "SimpleLRUCache.SetCapacity", "SimpleLRUCache.RemoveOldest"}

type svfKey string

func (k svfKey) Hash() []byte { return []byte(k) }

func svfThreshold(p []json.RawMessage) any {
	switch arg[string](p, 0) {
	case "u64":
		return arg[uint64](p, 1)
	case "i64":
		return arg[int64](p, 1)
	case "f64":
		return arg[float64](p, 1)
	case "bool":
		return arg[bool](p, 1)
	case "str":
		return arg[string](p, 1)
	}
	return nil
}

func svfWarnErr(p []json.RawMessage) error {
	switch arg[string](p, 0) {
	case "terror":
		// a normalized error: ToSQLError's message
		return dbterror.ClassTypes.NewStd(mysql.ErrTruncatedWrongValue).GenWithStackByArgs("DOUBLE", arg[string](p, 1))
	case "terror2":
		return terror.ClassOptimizer.NewStd(mysql.ErrUnknown).GenWithStack("%s", arg[string](p, 1))
	case "wrapped":
		return errors.Trace(dbterror.ClassTypes.NewStd(mysql.ErrDataTooLong).GenWithStackByArgs(arg[string](p, 1)))
	default:
		return errors.New(arg[string](p, 1))
	}
}

func svfExec(op string, args []json.RawMessage) (any, error, bool) {
	switch op {
	case "redact":
		in := string(arg[vec.GoString](args, 0))
		out := map[string]any{}
		for _, mode := range []string{"MARKER", "OFF", "ON", "bogus"} {
			var b strings.Builder
			redact.WriteRedact(&b, in, mode)
			// Only ON/OFF/MARKER reach the switch (tidb_redact_log is an enum; InitRedact
			// stores ON/OFF): an invalid mode is only passed to String / WriteRedact.
			if mode != "bogus" {
				errors.RedactLogEnabled.Store(mode)
			} else {
				errors.RedactLogEnabled.Store("OFF")
			}
			out[mode] = []any{vec.GoString(redact.String(mode, in)), vec.GoString(fmt.Sprint(redact.Stringer(mode, stringerOf(in)))),
				vec.GoString(b.String()), vec.GoString(redact.Value(in)), redact.Key([]byte(in)), redact.NeedRedact()}
		}
		redact.InitRedact(true)
		out["init"] = redact.NeedRedact()
		redact.InitRedact(false)
		for _, remove := range []bool{false, true} {
			var w bytes.Buffer
			err := redact.DeRedact(remove, strings.NewReader(in), &w, "|")
			out[fmt.Sprint("de", remove)] = []any{vec.GoString(w.String()), err != nil}
		}
		errors.RedactLogEnabled.Store("OFF")
		return out, nil, true
	case "lazytext":
		var s variable.LazyStmtText
		var params *variable.PlanCacheParamList
		if ps := arg[[]int64](args, 2); ps != nil {
			params = variable.NewPlanCacheParamList()
			for _, v := range ps {
				params.Append(types.NewIntDatum(v))
			}
			params.SetForNonPrepCache(arg[bool](args, 3))
		}
		s.Update(arg[string](args, 0), string(arg[vec.GoString](args, 1)), params)
		first := s.String()
		if t := arg[*string](args, 4); t != nil {
			s.SetText(*t)
		}
		var nilText *variable.LazyStmtText
		return []any{vec.GoString(first), vec.GoString(s.String()), nilText.String()}, nil, true
	case "lru":
		c := kvcache.NewSimpleLRUCache(arg[uint](args, 0), 0, 0)
		var evicted []any
		c.SetOnEvict(func(k kvcache.Key, v kvcache.Value) { evicted = append(evicted, []any{string(k.Hash()), v}) })
		var res []any
		for _, o := range rawList(arg[[]json.RawMessage](args, 1)) {
			switch arg[string](o, 0) {
			case "put":
				c.Put(svfKey(arg[string](o, 1)), arg[int64](o, 2))
				res = append(res, nil)
			case "get":
				v, ok := c.Get(svfKey(arg[string](o, 1)))
				res = append(res, []any{v, ok})
			case "delete":
				c.Delete(svfKey(arg[string](o, 1)))
				res = append(res, nil)
			case "deleteAll":
				c.DeleteAll()
				res = append(res, nil)
			case "size":
				res = append(res, c.Size())
			case "keys":
				var ks []string
				for _, k := range c.Keys() {
					ks = append(ks, string(k.Hash()))
				}
				res = append(res, ks)
			case "values":
				res = append(res, c.Values())
			case "setCapacity":
				res = append(res, errStr(c.SetCapacity(arg[uint](o, 1))))
			case "removeOldest":
				k, v, ok := c.RemoveOldest()
				var ks any
				if k != nil {
					ks = string(k.Hash())
				}
				res = append(res, []any{ks, v, ok})
			}
		}
		return map[string]any{"res": res, "evicted": evicted}, nil, true
	case "stmtcache":
		vars := freshVars(svCtx{})
		vars.SessionPlanCacheSize = arg[uint64](args, 0)
		var res []any
		for _, o := range rawList(arg[[]json.RawMessage](args, 1)) {
			k := arg[string](o, 1)
			switch arg[string](o, 0) {
			case "add":
				vars.AddNonPreparedPlanCacheStmt(k, arg[int64](o, 2))
				res = append(res, nil)
			case "get":
				res = append(res, vars.GetNonPreparedPlanCacheStmt(k))
			case "dset":
				vars.SetPrepareStmtDedupCache(k, arg[int64](o, 2))
				res = append(res, nil)
			case "dget":
				res = append(res, vars.GetPrepareStmtDedupCache(k))
			}
		}
		return res, nil, true
	case "parsecfg":
		vars := freshVars(svCtx{})
		var setErrs []any
		for _, p := range rawList(arg[[]json.RawMessage](args, 0)) {
			setErrs = append(setErrs, errStr(vars.SetSystemVar(arg[string](p, 0), arg[string](p, 1))))
		}
		cfg := vars.BuildParserConfig()
		var params []any
		for _, p := range vars.GetParseParams() {
			params = append(params, fmt.Sprintf("%T:%v", p, p))
		}
		return map[string]any{"set": setErrs, "cfg": []any{cfg.EnableWindowFunction,
			cfg.EnableStrictDoubleTypeCheck, cfg.SkipPositionRecording}, "params": params}, nil, true
	case "slowwarn":
		sc := stmtctx.NewStmtCtx()
		for _, w := range rawList(arg[[]json.RawMessage](args, 0)) {
			e := svfWarnErr(w[1:])
			switch arg[string](w, 0) {
			case "warn":
				sc.AppendWarning(e)
			case "note":
				sc.AppendNote(e)
			case "error":
				sc.AppendError(e)
			case "extra":
				sc.AppendExtraWarning(e)
			case "extranote":
				sc.AppendExtraNote(e)
			}
		}
		var out []any
		for _, w := range variable.CollectWarningsForSlowLog(sc) {
			out = append(out, []any{w.Level, vec.GoString(w.Message), w.IsExtra})
		}
		return out, nil, true
	case "matchers":
		t := svfThreshold(arg[[]json.RawMessage](args, 0))
		v := arg[[]json.RawMessage](args, 1)
		var eq, ge any
		switch arg[string](v, 0) {
		case "int":
			eq = variable.MatchEqual(t, int(arg[int64](v, 1)))
		// matchGE is generic (no linkname to an instantiation): reached through the accessors
		// that call it with an int64 (mem_max), a uint64 (conn_id) and a float64 (parse_time).
		case "i64":
			eq = variable.MatchEqual(t, arg[int64](v, 1))
			ge = variable.SlowLogRuleFieldAccessors["mem_max"].Match(nil,
				&variable.SlowQueryLogItems{MemMax: arg[int64](v, 1)}, t)
		case "u64":
			eq = variable.MatchEqual(t, arg[uint64](v, 1))
			sv := freshVars(svCtx{})
			sv.ConnectionID = arg[uint64](v, 1)
			ge = variable.SlowLogRuleFieldAccessors["conn_id"].Match(sv, nil, t)
		case "f64":
			eq = variable.MatchEqual(t, arg[float64](v, 1))
			sv := freshVars(svCtx{})
			sv.DurationParse = time.Duration(arg[float64](v, 1) * float64(time.Second))
			ge = variable.SlowLogRuleFieldAccessors["parse_time"].Match(sv, nil, t)
		case "bool":
			eq = variable.MatchEqual(t, arg[bool](v, 1))
		case "str":
			eq = variable.MatchEqual(t, arg[string](v, 1))
		}
		u, ok := svfUint64FromNonNegative(arg[int64](args, 2))
		ps, perr := variable.ParseString(arg[string](args, 3))
		return []any{eq, ge, svfMatchZero(t), []any{u, ok}, []any{ps, errStr(perr)}}, nil, true
	case "sessstates":
		vars := freshVars(svCtx{})
		m := arg[map[string]json.RawMessage](args, 0)
		get := func(k string, p any) {
			if v, ok := m[k]; ok {
				if err := json.Unmarshal(v, p); err != nil {
					panic(err)
				}
			}
		}
		var uv map[string]int64
		get("uv", &uv)
		for k, v := range uv {
			vars.SetUserVarVal(k, types.NewIntDatum(v))
		}
		var ut []string
		get("ut", &ut)
		for _, k := range ut {
			vars.SetUserVarType(k, types.NewFieldType(mysql.TypeLonglong))
		}
		var status uint32
		get("status", &status)
		vars.SetStatusFlag(uint16(status), true)
		get("db", &vars.CurrentDB)
		get("txn", &vars.LastTxnInfo)
		get("qstart", &vars.LastQueryInfo.StartTS)
		get("qerr", &vars.LastQueryInfo.ErrMsg)
		get("ddlseq", &vars.LastDDLInfo.SeqNum)
		get("ddlq", &vars.LastDDLInfo.Query)
		get("found", &vars.LastFoundRows)
		get("pfc", &vars.PrevFoundInPlanCache)
		get("pfb", &vars.PrevFoundInBinding)
		get("rg", &vars.ResourceGroupName)
		var psid uint32
		get("psid", &psid)
		vars.SetNextPreparedStmtID(psid)
		var seq map[int64]int64
		get("seq", &seq)
		for id, v := range seq {
			vars.SequenceState.UpdateState(id, v)
		}
		var hypo bool
		get("hypo", &hypo)
		if hypo {
			vars.HypoIndexes = map[string]map[string]map[string]*model.IndexInfo{"db": {"t": {"i": {}}}}
			vars.HypoTiFlashReplicas = map[string]map[string]struct{}{"db": {"t": {}}}
		}
		var ar uint64
		get("ar", &ar)
		vars.StmtCtx.SetAffectedRows(ar)
		vars.StmtCtx.PrevAffectedRows = int64(ar)
		get("lid", &vars.StmtCtx.PrevLastInsertID)
		var warns []string
		get("warns", &warns)
		for _, w := range warns {
			vars.StmtCtx.AppendWarning(errors.New(w))
		}
		var st sessionstates.SessionStates
		encErr := vars.EncodeSessionStates(context.Background(), &st)
		other := freshVars(svCtx{})
		decErr := other.DecodeSessionStates(context.Background(), &st)
		return map[string]any{"enc": svfStatesDump(&st), "dec": svfVarsDump(other),
			"err": []any{errStr(encErr), errStr(decErr)}}, nil, true
	}
	return nil, nil, false
}

type stringerOf string

func (s stringerOf) String() string { return string(s) }

func svfSortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func svfStatesDump(st *sessionstates.SessionStates) map[string]any {
	uv := map[string]any{}
	for _, k := range svfSortedKeys(st.UserVars) {
		uv[k] = st.UserVars[k].GetInt64()
	}
	ut := map[string]any{}
	for _, k := range svfSortedKeys(st.UserVarTypes) {
		ut[k] = st.UserVarTypes[k].GetType()
	}
	var warns []any
	for _, w := range st.Warnings {
		warns = append(warns, []any{w.Level, w.Err.Error()})
	}
	var q, d any
	if st.LastQueryInfo != nil {
		q = []any{st.LastQueryInfo.StartTS, st.LastQueryInfo.ErrMsg}
	}
	if st.LastDDLInfo != nil {
		d = []any{st.LastDDLInfo.SeqNum, st.LastDDLInfo.Query}
	}
	return map[string]any{
		"uvNil": st.UserVars == nil, "uv": uv, "ut": ut, "psid": st.PreparedStmtID, "status": st.Status,
		"db": st.CurrentDB, "txn": st.LastTxnInfo, "q": q, "d": d, "found": st.LastFoundRows,
		"fpc": st.FoundInPlanCache, "fb": st.FoundInBinding, "seq": st.SequenceLatestValues,
		"rg": st.ResourceGroupName, "hypo": st.HypoIndexes != nil, "hypoTF": st.HypoTiFlashReplicas != nil,
		"ar": st.LastAffectedRows, "lid": st.LastInsertID, "warns": warns,
	}
}

func svfVarsDump(v *variable.SessionVars) map[string]any {
	st := &sessionstates.SessionStates{}
	_ = v.EncodeSessionStates(context.Background(), st)
	out := svfStatesDump(st)
	// The fields Decode writes that Encode does not read back.
	out["vfpc"] = v.FoundInPlanCache
	out["vfb"] = v.FoundInBinding
	out["affected"] = v.StmtCtx.AffectedRows()
	return out
}

func svfGenerate(g *vec.Gen, rng *rand.Rand) {
	r := rand.New(rand.NewPCG(rng.Uint64(), 54))
	// redact
	inputs := []string{"", "abc", "a‹b›c", "‹x›", "‹‹y››", "‹unterminated", "end›", "m‹a›n‹b›", "\xff\xfe‹z›",
		"line1\nline2‹s›", "‹a›››b", "‹›", "‹‹", "››", "x\r\ny‹q›", "中文‹文›", "\x00‹\x01›",
		"x\xe4\xb8y‹\xe4\xb8›", "\xf0\x9f\x98‹a›"}
	for _, in := range inputs {
		g.Add("sys/redact", "redact", vec.GoString(in))
	}
	for i := 0; i < 60; i++ {
		alpha := []string{"a", "‹", "›", "b", "\n", "\xe4", "中", " "}
		var b strings.Builder
		for j := r.IntN(12); j > 0; j-- {
			b.WriteString(alpha[r.IntN(len(alpha))])
		}
		g.Add("rand/redact", "redact", vec.GoString(b.String()))
	}
	// lazytext
	modes := []string{"OFF", "MARKER", "ON"}
	sqls := []string{"select 1", "select ?", "select ‹x›", "select \xff", ""}
	for i := 0; i < 80; i++ {
		var ps []int64
		if r.IntN(3) > 0 {
			for j := r.IntN(4); j >= 0; j-- {
				ps = append(ps, int64(r.IntN(200))-100)
			}
		}
		var set *string
		if r.IntN(4) == 0 {
			s := "set ‹t›"
			set = &s
		}
		g.Add("rand/lazytext", "lazytext", modes[r.IntN(3)], vec.GoString(sqls[r.IntN(len(sqls))]), ps, r.IntN(3) == 0, set)
	}
	// lru
	keys := []string{"a", "b", "c", "d", "e"}
	randOp := func() []any {
		switch r.IntN(12) {
		case 0, 1, 2, 3:
			return []any{"put", keys[r.IntN(len(keys))], int64(r.IntN(100))}
		case 4, 5:
			return []any{"get", keys[r.IntN(len(keys))]}
		case 6:
			return []any{"delete", keys[r.IntN(len(keys))]}
		case 7:
			return []any{"deleteAll"}
		case 8:
			return []any{"keys"}
		case 9:
			return []any{"values"}
		case 10:
			return []any{"setCapacity", r.IntN(4)}
		default:
			return []any{[]string{"size", "removeOldest"}[r.IntN(2)]}
		}
	}
	for i := 0; i < 150; i++ {
		var ops [][]any
		for j := 1 + r.IntN(20); j > 0; j-- {
			ops = append(ops, randOp())
		}
		g.Add("rand/lru", "lru", 1+r.IntN(4), ops)
	}
	// stmtcache
	for i := 0; i < 60; i++ {
		var ops [][]any
		for j := 1 + r.IntN(12); j > 0; j-- {
			k := keys[r.IntN(len(keys))]
			switch r.IntN(4) {
			case 0:
				ops = append(ops, []any{"add", k, int64(r.IntN(50))})
			case 1:
				ops = append(ops, []any{"get", k})
			case 2:
				ops = append(ops, []any{"dset", k, int64(r.IntN(50))})
			default:
				ops = append(ops, []any{"dget", k})
			}
		}
		g.Add("rand/stmtcache", "stmtcache", 1+r.IntN(3), ops)
	}
	// parsecfg
	pvals := [][2][]string{
		{{variable.TiDBEnableWindowFunction}, {"ON", "OFF"}},
		{{variable.TiDBEnableStrictDoubleTypeCheck}, {"ON", "OFF"}},
		{{variable.CharacterSetConnection}, {"utf8mb4", "latin1", "gbk", "binary"}},
		{{variable.CollationConnection}, {"utf8mb4_bin", "latin1_bin", "gbk_chinese_ci", "utf8mb4_general_ci"}},
		{{variable.CharacterSetClient}, {"utf8mb4", "latin1", "gbk", "ascii"}},
	}
	for i := 0; i < 40; i++ {
		var sets [][2]string
		for _, p := range pvals {
			if r.IntN(2) == 0 {
				sets = append(sets, [2]string{p[0][0], p[1][r.IntN(len(p[1]))]})
			}
		}
		g.Add("rand/parsecfg", "parsecfg", sets)
	}
	// slowwarn
	kinds := []string{"warn", "note", "error", "extra", "extranote"}
	ekinds := []string{"terror", "terror2", "wrapped", "plain"}
	msgs := []string{"x", "1e999", "‹a›", "%d %s"}
	for i := 0; i < 60; i++ {
		var ws [][]any
		for j := r.IntN(5); j > 0; j-- {
			ws = append(ws, []any{kinds[r.IntN(len(kinds))], ekinds[r.IntN(len(ekinds))], msgs[r.IntN(len(msgs))]})
		}
		g.Add("rand/slowwarn", "slowwarn", ws)
	}
	// matchers
	ths := [][]any{{"u64", uint64(0)}, {"u64", uint64(5)}, {"i64", int64(0)}, {"i64", int64(5)}, {"i64", int64(-1)},
		{"f64", 0.0}, {"f64", 2.5}, {"bool", true}, {"bool", false}, {"str", ""}, {"str", "Db1"}}
	vals := [][]any{{"int", int64(5)}, {"int", int64(0)}, {"i64", int64(5)}, {"i64", int64(4)}, {"u64", uint64(5)},
		{"u64", uint64(9)}, {"f64", 2.5}, {"f64", 0.0}, {"bool", true}, {"str", "Db1"}, {"str", "db1"}}
	for _, t := range ths {
		for _, v := range vals {
			g.Add("sys/matchers", "matchers", t, v, int64(r.IntN(5))-2, "s‹1›")
		}
	}
	// sessstates
	for i := 0; i < 60; i++ {
		m := map[string]any{}
		if r.IntN(2) == 0 {
			uv := map[string]int64{}
			for j := r.IntN(3); j >= 0; j-- {
				uv[keys[r.IntN(len(keys))]] = int64(r.IntN(100))
			}
			m["uv"] = uv
		}
		if r.IntN(2) == 0 {
			m["ut"] = []string{keys[r.IntN(len(keys))]}
		}
		m["status"] = []uint32{0, 1, 2, 8}[r.IntN(4)]
		m["db"] = []string{"", "test", "Db1"}[r.IntN(3)]
		m["txn"] = []string{"", "{\"start_ts\":1}"}[r.IntN(2)]
		if r.IntN(2) == 0 {
			m["qstart"] = uint64(r.IntN(3))
			m["qerr"] = "e"
		}
		if r.IntN(2) == 0 {
			m["ddlseq"] = uint64(r.IntN(3))
			m["ddlq"] = "create table t(a int)"
		}
		m["found"] = uint64(r.IntN(4))
		m["pfc"], m["pfb"] = r.IntN(2) == 0, r.IntN(2) == 0
		m["rg"] = []string{"", "default", "rg1"}[r.IntN(3)]
		m["psid"] = uint32(r.IntN(5))
		if r.IntN(2) == 0 {
			m["seq"] = map[int64]int64{int64(r.IntN(3)): int64(r.IntN(100))}
		}
		m["hypo"] = r.IntN(3) == 0
		m["ar"] = uint64(r.IntN(10))
		m["lid"] = uint64(r.IntN(10))
		if r.IntN(2) == 0 {
			m["warns"] = []string{"w1", "w2"}[:1+r.IntN(2)]
		}
		g.Add("rand/sessstates", "sessstates", m)
	}
}
