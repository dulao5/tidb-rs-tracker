package domains

// Domain "ctx2b": block D00.1 part 2b — the expropt providers and readers
// (pkg/expression/expropt except sessionvars.go), the optimizer debug trace
// (pkg/planner/util/debugtrace), PlanCounterTp (pkg/planner/core/base), basicCtxType
// (pkg/sessionctx/context.go) and the statement context's tracker init / detach and
// StatsLoadResult (pkg/sessionctx/stmtctx).
//
// A Go panic is {"panic": true}; an error is {"err": msg}. Every record runs with new
// collations enabled and restores errors.RedactLogEnabled (tidb_redact_log, #62).
//
// Records:
//   - "plancounter": args [init, [x...]] -> IsForce / Empty, then [value, Empty, IsForce]
//     after each Dec(x).
//   - "basicctx": args [n] -> basicCtxType(n).String().
//   - "statsload": args [tableID, id, isIndex, errMsg|null] -> HasError / ErrorMsg.
//   - "tracker": args [memLabel, memLimit, diskLabel, diskLimit, consume, detach] ->
//     InitMemTracker / InitDiskTracker, attach the memory tracker to a parent, consume, then
//     DetachMemDiskTracker when detach is set: labels, limits and consumption.
//   - "expropt": args [key, provider|null] -> the provider's Desc, the reader's
//     RequiredOptionalEvalProps and every reader method's result (or error). provider is the
//     payload of the provider of that key (see ctxProvider); null means no provider.
//   - "debugtrace": args [script] -> the steps' EncodeJSONCommon results and the root's
//     MarshalJSON. A script is [[op, ...]...]: ["step", v], ["named", name, v],
//     ["enter", k] (EnterContextCommon from helper dtEnter<k>), ["leave"],
//     ["record", [v...]] (RecordAnyValuesWithNames), ["encode", v] (EncodeJSONCommon).
//     A value is ["n"], ["b", bool], ["i", int], ["f", float|"NaN"|"+Inf"|"-Inf"], ["s", str],
//     ["a", [v...]], ["m", [[key, v]...]].

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/expression/expropt"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	infoschema "github.com/pingcap/tidb/pkg/infoschema/context"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/auth"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/util/debugtrace"
	"github.com/pingcap/tidb/pkg/sessionctx"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/memory"
	"github.com/pingcap/tidb/pkg/util/mock"
)

const ctx2bSeed = 20261109

// Ctx2bMinedTests: the Go test scenarios replayed as records.
func Ctx2bMinedTests() []string {
	return []string{"TestOptionalEvalPropProviders"}
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "ctx2b",
		Seed:     ctx2bSeed,
		Coverage: ctx2bCoverage,
		Blocks:   []string{"D00.1"},
		Exempt:   ctx2bExempt(),
		Gzip:     true,
		Generate: ctx2bGenerate,
		Exec:     ctx2bExec,
	})
}

var ctx2bCoverage = map[string]vec.Coverage{
	"plancounter": {Min: 100, GoFuncs: []string{"PlanCounterTp.Dec", "PlanCounterTp.Empty", "PlanCounterTp.IsForce"}},
	"basicctx":    {Min: 6, GoFuncs: []string{"basicCtxType.String"}},
	"statsload":   {Min: 50, GoFuncs: []string{"StatsLoadResult.HasError", "StatsLoadResult.ErrorMsg"}},
	"tracker": {Min: 50, GoFuncs: []string{"StatementContext.InitMemTracker", "StatementContext.InitDiskTracker",
		"StatementContext.DetachMemDiskTracker"}},
	"expropt": {Min: 30, GoFuncs: []string{
		"NewAdvisoryLockPropProvider", "AdvisoryLockPropProvider.Desc", "AdvisoryLockPropReader.RequiredOptionalEvalProps",
		"AdvisoryLockPropReader.AdvisoryLockCtx",
		"CurrentUserPropProvider.Desc", "CurrentUserPropReader.RequiredOptionalEvalProps", "CurrentUserPropReader.CurrentUser",
		"CurrentUserPropReader.ActiveRoles", "CurrentUserPropReader.getProvider",
		"DDLOwnerInfoProvider.Desc", "DDLOwnerPropReader.RequiredOptionalEvalProps", "DDLOwnerPropReader.IsDDLOwner",
		"InfoSchemaPropProvider.Desc", "InfoSchemaPropReader.RequiredOptionalEvalProps",
		"InfoSchemaPropReader.GetSessionInfoSchema", "InfoSchemaPropReader.GetDomainInfoSchema",
		"KVStorePropProvider.Desc", "KVStorePropReader.RequiredOptionalEvalProps", "KVStorePropReader.GetKVStore",
		"PrivilegeCheckerProvider.Desc", "PrivilegeCheckerPropReader.RequiredOptionalEvalProps",
		"PrivilegeCheckerPropReader.GetPrivilegeChecker",
		"SequenceOperatorProvider.Desc", "SequenceOperatorPropReader.RequiredOptionalEvalProps",
		"SequenceOperatorPropReader.GetSequenceOperator",
		"SQLExecutorPropProvider.Desc", "SQLExecutorPropReader.RequiredOptionalEvalProps",
		"SQLExecutorPropReader.GetSQLExecutor",
	}},
	"debugtrace": {Min: 300, GoFuncs: []string{"OptimizerDebugTraceRoot.MarshalJSON", "baseDebugTraceContext.MarshalJSON",
		"OptimizerDebugTraceRoot.AppendStepToCurrentContext", "OptimizerDebugTraceRoot.AppendStepWithNameToCurrentContext",
		"GetOrInitDebugTraceRoot", "EncodeJSONCommon", "EnterContextCommon", "LeaveContextCommon",
		"RecordAnyValuesWithNames"}},
}

func ctx2bExec(op string, p []json.RawMessage) (out any, err error) {
	collate.SetNewCollationEnabledForTest(true)
	defer errors.RedactLogEnabled.Store(errors.RedactLogEnabled.Load())
	switch op {
	case "plancounter":
		c := base.PlanCounterTp(arg[int64](p, 0))
		steps := [][]any{}
		m := map[string]any{"force": c.IsForce(), "empty": c.Empty()}
		for _, x := range arg[[]int64](p, 1) {
			c.Dec(x)
			steps = append(steps, []any{int64(c), c.Empty(), c.IsForce()})
		}
		m["steps"] = steps
		return m, nil
	case "basicctx":
		return ctx2bBasicCtxString(arg[int](p, 0)), nil
	case "statsload":
		r := stmtctx.StatsLoadResult{Item: model.TableItemID{TableID: arg[int64](p, 0), ID: arg[int64](p, 1), IsIndex: arg[bool](p, 2)}}
		if msg := arg[*string](p, 3); msg != nil {
			r.Error = errors.New(*msg)
		}
		return map[string]any{"has": r.HasError(), "msg": vec.GoString(r.ErrorMsg())}, nil
	case "tracker":
		sc := stmtctx.NewStmtCtx()
		sc.InitMemTracker(arg[int](p, 0), arg[int64](p, 1))
		sc.InitDiskTracker(arg[int](p, 2), arg[int64](p, 3))
		parent := memory.NewTracker(-100, -1)
		sc.MemTracker.AttachTo(parent)
		dparent := memory.NewTracker(-101, -1)
		sc.DiskTracker.AttachTo(dparent)
		n := arg[int64](p, 4)
		sc.MemTracker.Consume(n)
		sc.DiskTracker.Consume(n / 2)
		m := map[string]any{
			"mlabel": sc.MemTracker.Label(), "mlimit": sc.MemTracker.GetBytesLimit(),
			"dlabel": sc.DiskTracker.Label(), "dlimit": sc.DiskTracker.GetBytesLimit(),
			"pc": parent.BytesConsumed(), "dpc": dparent.BytesConsumed(),
		}
		if arg[bool](p, 5) {
			sc.DetachMemDiskTracker()
			m["pc2"], m["dpc2"] = parent.BytesConsumed(), dparent.BytesConsumed()
			m["mc2"], m["dc2"] = sc.MemTracker.BytesConsumed(), sc.DiskTracker.BytesConsumed()
		}
		return m, nil
	case "expropt":
		return ctx2bExpropt(arg[int](p, 0), p[1]), nil
	case "debugtrace":
		return ctx2bDebugTrace(arg[[]json.RawMessage](p, 0)), nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// ctx2bBasicCtxString calls the unexported basicCtxType.String through the exported keys.
func ctx2bBasicCtxString(n int) string {
	switch n {
	case 1:
		return fmt.Sprint(sessionctx.QueryString)
	case 2:
		return fmt.Sprint(sessionctx.Initing)
	case 3:
		return fmt.Sprint(sessionctx.LastExecuteDDL)
	}
	// Any other value of the unexported type: set it through reflection.
	v := sessionctx.QueryString
	reflect.ValueOf(&v).Elem().SetInt(int64(n))
	return fmt.Sprint(v)
}

// --- expropt ---

type ctxLock struct{ tag uint64 }

func (c ctxLock) GetAdvisoryLock(string, int64) error { return nil }
func (c ctxLock) IsUsedAdvisoryLock(string) uint64    { return c.tag }
func (c ctxLock) ReleaseAdvisoryLock(string) bool     { return false }
func (c ctxLock) ReleaseAllAdvisoryLocks() int        { return 0 }

type ctxPriv struct{ tag bool }

func (c ctxPriv) RequestVerification(db, table, column string, priv mysql.PrivilegeType) bool {
	return c.tag
}
func (c ctxPriv) RequestDynamicVerification(privName string, grantable bool) bool { return c.tag }

type ctxSeq struct{ id int64 }

func (c ctxSeq) GetSequenceID() int64                      { return c.id }
func (c ctxSeq) GetSequenceNextVal() (int64, error)        { return 0, nil }
func (c ctxSeq) SetSequenceVal(int64) (int64, bool, error) { return 0, false, nil }

type ctxExec struct{ expropt.SQLExecutor }

func ctxRes(v any, err error) any {
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	return map[string]any{"ok": v}
}

func ctxUser(u *auth.UserIdentity) any {
	if u == nil {
		return nil
	}
	return []string{u.Username, u.Hostname}
}

// ctx2bExpropt: the provider payload per key —
// CurrentUser: [user|null, [[u, h]...]]; DDLOwner: bool; InfoSchema: true (the provider
// returns nil and records isDomain); KVStore: no provider only; AdvisoryLock: tag;
// PrivilegeChecker: tag; SequenceOperator: [id, errMsg|null]; SQLExecutor: ["ok"] | ["err", msg].
func ctx2bExpropt(key int, payload json.RawMessage) any {
	k := exprctx.OptionalEvalPropKey(key)
	has := string(payload) != "null"
	var provider exprctx.OptionalEvalPropProvider
	m := map[string]any{}
	var seen []any
	switch k {
	case exprctx.OptPropCurrentUser:
		var pl struct {
			User  *[2]string
			Roles [][2]string
		}
		var raw []json.RawMessage
		if has {
			_ = json.Unmarshal(payload, &raw)
			_ = json.Unmarshal(raw[0], &pl.User)
			_ = json.Unmarshal(raw[1], &pl.Roles)
		}
		var user *auth.UserIdentity
		if pl.User != nil {
			user = &auth.UserIdentity{Username: pl.User[0], Hostname: pl.User[1]}
		}
		roles := []*auth.RoleIdentity{}
		for _, r := range pl.Roles {
			roles = append(roles, &auth.RoleIdentity{Username: r[0], Hostname: r[1]})
		}
		pv := expropt.CurrentUserPropProvider(func() (*auth.UserIdentity, []*auth.RoleIdentity) { return user, roles })
		provider = pv
		r := expropt.CurrentUserPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		run := func(ctx exprctx.EvalContext) {
			u, err := r.CurrentUser(ctx)
			m["user"] = ctxRes(ctxUser(u), err)
			rs, err := r.ActiveRoles(ctx)
			out := [][]string{}
			for _, x := range rs {
				out = append(out, []string{x.Username, x.Hostname})
			}
			m["roles"] = ctxRes(out, err)
		}
		defer func() { run(ctx2bEval(has, provider)) }()
	case exprctx.OptPropDDLOwnerInfo:
		var v bool
		if has {
			_ = json.Unmarshal(payload, &v)
		}
		provider = expropt.DDLOwnerInfoProvider(func() bool { return v })
		r := expropt.DDLOwnerPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		defer func() { b, err := r.IsDDLOwner(ctx2bEval(has, provider)); m["owner"] = ctxRes(b, err) }()
	case exprctx.OptPropInfoSchema:
		provider = expropt.InfoSchemaPropProvider(func(isDomain bool) infoschema.MetaOnlyInfoSchema {
			seen = append(seen, isDomain)
			return nil
		})
		r := expropt.InfoSchemaPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		defer func() {
			ctx := ctx2bEval(has, provider)
			is, err := r.GetSessionInfoSchema(ctx)
			m["sess"] = ctxRes(is == nil, err)
			is, err = r.GetDomainInfoSchema(ctx)
			m["dom"] = ctxRes(is == nil, err)
			m["seen"] = seen
		}()
	case exprctx.OptPropKVStore:
		provider = expropt.KVStorePropProvider(nil)
		r := expropt.KVStorePropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		has = false
		defer func() { _, err := r.GetKVStore(ctx2bEval(false, provider)); m["store"] = ctxRes(nil, err) }()
	case exprctx.OptPropAdvisoryLock:
		var tag uint64
		if has {
			_ = json.Unmarshal(payload, &tag)
		}
		provider = expropt.NewAdvisoryLockPropProvider(ctxLock{tag: tag})
		r := expropt.AdvisoryLockPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		defer func() {
			c, err := r.AdvisoryLockCtx(ctx2bEval(has, provider))
			if err != nil {
				m["lock"] = ctxRes(nil, err)
			} else {
				m["lock"] = ctxRes(c.IsUsedAdvisoryLock("x"), nil)
			}
		}()
	case exprctx.OptPropPrivilegeChecker:
		var tag bool
		if has {
			_ = json.Unmarshal(payload, &tag)
		}
		provider = expropt.PrivilegeCheckerProvider(func() expropt.PrivilegeChecker { return ctxPriv{tag: tag} })
		r := expropt.PrivilegeCheckerPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		defer func() {
			c, err := r.GetPrivilegeChecker(ctx2bEval(has, provider))
			if err != nil {
				m["priv"] = ctxRes(nil, err)
			} else {
				m["priv"] = ctxRes(c.RequestDynamicVerification("p", false), nil)
			}
		}()
	case exprctx.OptPropSequenceOperator:
		var pl []json.RawMessage
		var id int64
		var emsg *string
		if has {
			_ = json.Unmarshal(payload, &pl)
			_ = json.Unmarshal(pl[0], &id)
			_ = json.Unmarshal(pl[1], &emsg)
		}
		provider = expropt.SequenceOperatorProvider(func(db, name string) (expropt.SequenceOperator, error) {
			seen = append(seen, db, name)
			if emsg != nil {
				return nil, errors.New(*emsg)
			}
			return ctxSeq{id: id}, nil
		})
		r := expropt.SequenceOperatorPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		defer func() {
			s, err := r.GetSequenceOperator(ctx2bEval(has, provider), "db1", "seq<&>")
			if err != nil {
				m["seq"] = ctxRes(nil, err)
			} else {
				m["seq"] = ctxRes(s.GetSequenceID(), nil)
			}
			m["seen"] = seen
		}()
	case exprctx.OptPropSQLExecutor:
		var emsg *string
		if has {
			var pl []string
			_ = json.Unmarshal(payload, &pl)
			if len(pl) == 2 {
				emsg = &pl[1]
			}
		}
		provider = expropt.SQLExecutorPropProvider(func() (expropt.SQLExecutor, error) {
			if emsg != nil {
				return nil, errors.New(*emsg)
			}
			return ctxExec{}, nil
		})
		r := expropt.SQLExecutorPropReader{}
		m["keys"] = uint64(r.RequiredOptionalEvalProps())
		defer func() { _, err := r.GetSQLExecutor(ctx2bEval(has, provider)); m["exec"] = ctxRes(true, err) }()
	default:
		return map[string]any{"skip": true}
	}
	d := provider.Desc()
	m["desc"] = []any{int(d.Key()), d.Key().String()}
	return m
}

func ctx2bEval(has bool, p exprctx.OptionalEvalPropProvider) exprctx.EvalContext {
	if !has {
		return exprstatic.NewEvalContext()
	}
	return exprstatic.NewEvalContext(exprstatic.WithOptionalProperty(p))
}

// --- debugtrace ---

func dtValue(raw json.RawMessage) any {
	var v []json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil || len(v) == 0 {
		panic(fmt.Sprintf("bad value %s", raw))
	}
	var tag string
	_ = json.Unmarshal(v[0], &tag)
	switch tag {
	case "n":
		return nil
	case "b":
		var b bool
		_ = json.Unmarshal(v[1], &b)
		return b
	case "i":
		var i int64
		_ = json.Unmarshal(v[1], &i)
		return i
	case "f":
		// Floats travel as strings (exact on both sides; serde_json's default parser is not
		// correctly rounded): strconv.FormatFloat(f, 'g', -1, 64), NaN, +Inf, -Inf.
		var s string
		_ = json.Unmarshal(v[1], &s)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			panic(fmt.Sprintf("bad float %q", s))
		}
		return f
	case "s":
		var s string
		_ = json.Unmarshal(v[1], &s)
		return s
	case "a":
		var items []json.RawMessage
		_ = json.Unmarshal(v[1], &items)
		out := make([]any, 0, len(items))
		for _, it := range items {
			out = append(out, dtValue(it))
		}
		return out
	case "m":
		var kvs [][2]json.RawMessage
		_ = json.Unmarshal(v[1], &kvs)
		out := map[string]any{}
		for _, kv := range kvs {
			var k string
			_ = json.Unmarshal(kv[0], &k)
			out[k] = dtValue(kv[1])
		}
		return out
	}
	panic("bad tag " + tag)
}

func dtEnter0(s *mock.Context) { debugtrace.EnterContextCommon(s) }
func dtEnter1(s *mock.Context) { debugtrace.EnterContextCommon(s) }
func dtEnter2(s *mock.Context) { debugtrace.EnterContextCommon(s) }

func ctx2bDebugTrace(script []json.RawMessage) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": true}
		}
	}()
	sctx := mock.NewContext()
	encodes := []any{}
	for _, raw := range script {
		var st []json.RawMessage
		_ = json.Unmarshal(raw, &st)
		var op string
		_ = json.Unmarshal(st[0], &op)
		root := debugtrace.GetOrInitDebugTraceRoot(sctx)
		switch op {
		case "step":
			root.AppendStepToCurrentContext(dtValue(st[1]))
		case "named":
			var name string
			_ = json.Unmarshal(st[1], &name)
			root.AppendStepWithNameToCurrentContext(dtValue(st[2]), name)
		case "enter":
			var k int
			_ = json.Unmarshal(st[1], &k)
			[]func(*mock.Context){dtEnter0, dtEnter1, dtEnter2}[k](sctx)
		case "leave":
			debugtrace.LeaveContextCommon(sctx)
		case "record":
			var items []json.RawMessage
			_ = json.Unmarshal(st[1], &items)
			vals := make([]any, 0, len(items))
			for _, it := range items {
				vals = append(vals, dtValue(it))
			}
			debugtrace.RecordAnyValuesWithNames(sctx, vals...)
		case "encode":
			b, err := debugtrace.EncodeJSONCommon(dtValue(st[1]))
			encodes = append(encodes, dtOut(b, err))
		}
	}
	b, err := debugtrace.GetOrInitDebugTraceRoot(sctx).MarshalJSON()
	return map[string]any{"enc": encodes, "root": dtOut(b, err)}
}

func dtOut(b []byte, err error) any {
	m := map[string]any{"out": vec.GoString(string(b))}
	if err != nil {
		m["err"] = vec.GoString(err.Error())
	}
	return m
}

// --- generator ---

func dtRandScalar(rng *rand.Rand) []any {
	switch rng.IntN(9) {
	case 0:
		return []any{"n"}
	case 1:
		return []any{"b", rng.IntN(2) == 0}
	case 2:
		return []any{"i", rng.Int64N(2000) - 1000}
	case 3:
		return []any{"i", int64(rng.Uint64())}
	case 4:
		fs := []float64{0.0, math.Copysign(0, -1), 1.5, 1e21, 1e-7, 123456789.125, -2.5e-300, math.MaxFloat64}
		return dtFloat(fs[rng.IntN(len(fs))])
	case 5:
		if rng.IntN(8) == 0 {
			return []any{"f", []string{"NaN", "+Inf", "-Inf"}[rng.IntN(3)]}
		}
		return dtFloat(rng.NormFloat64() * 1000)
	default:
		parts := []string{"a", "<", ">", "&", "\"", "\\", " ", " ", "\n", "\t", "\x01", "é", "中", " ", "x"}
		var b strings.Builder
		for range rng.IntN(6) {
			b.WriteString(parts[rng.IntN(len(parts))])
		}
		return []any{"s", b.String()}
	}
}

func dtFloat(f float64) []any { return []any{"f", strconv.FormatFloat(f, 'g', -1, 64)} }

func dtRandValue(rng *rand.Rand, depth int) []any {
	if depth > 2 || rng.IntN(3) != 0 {
		return dtRandScalar(rng)
	}
	if rng.IntN(2) == 0 {
		items := []any{}
		for range rng.IntN(4) {
			items = append(items, dtRandValue(rng, depth+1))
		}
		return []any{"a", items}
	}
	kvs := []any{}
	for range rng.IntN(4) {
		kvs = append(kvs, []any{dtRandKey(rng), dtRandValue(rng, depth+1)})
	}
	return []any{"m", kvs}
}

func dtRandKey(rng *rand.Rand) string {
	keys := []string{"a", "b", "B", "<k>", "&", "", "z", "中", "a "}
	return keys[rng.IntN(len(keys))]
}

func dtRandScript(rng *rand.Rand) []any {
	script := []any{}
	depth := 0
	for range rng.IntN(12) {
		switch rng.IntN(7) {
		case 0, 1:
			script = append(script, []any{"step", dtRandValue(rng, 0)})
		case 2:
			script = append(script, []any{"named", dtRandKey(rng), dtRandValue(rng, 0)})
		case 3:
			script = append(script, []any{"enter", rng.IntN(3)})
			depth++
		case 4:
			if depth > 0 || rng.IntN(4) == 0 {
				script = append(script, []any{"leave"})
				if depth > 0 {
					depth--
				}
			}
		case 5:
			vals := []any{}
			n := rng.IntN(4) * 2
			if rng.IntN(20) == 0 {
				n++ // odd: Go panics with an index out of range
			}
			for i := range n {
				if i%2 == 0 {
					if rng.IntN(6) == 0 {
						vals = append(vals, []any{"i", 7}) // a name that is not a string
					} else {
						vals = append(vals, []any{"s", dtRandKey(rng)})
					}
				} else {
					vals = append(vals, dtRandValue(rng, 0))
				}
			}
			script = append(script, []any{"record", vals})
		case 6:
			script = append(script, []any{"encode", dtRandValue(rng, 0)})
		}
	}
	return script
}

func ctx2bGenerate(g *vec.Gen, rng *rand.Rand) {
	for _, init := range []int64{-1, 0, 1, 2, 5, math.MaxInt64, math.MinInt64, -5} {
		for _, xs := range [][]int64{{}, {1}, {1, 1, 1}, {3}, {math.MaxInt64}, {-1}, {math.MinInt64}, {2, -3}} {
			g.Add("sys/plancounter", "plancounter", init, xs)
		}
	}
	for range 60 {
		xs := []int64{}
		for range rng.IntN(5) {
			xs = append(xs, rng.Int64N(10)-3)
		}
		g.Add("rand/plancounter", "plancounter", rng.Int64N(20)-2, xs)
	}
	for n := -1; n <= 5; n++ {
		g.Add("sys/basicctx", "basicctx", n)
	}
	for _, e := range []any{nil, "boom", "", "<&> ", "中文"} {
		for _, it := range [][3]any{{int64(1), int64(2), false}, {int64(-1), int64(0), true}, {int64(math.MaxInt64), int64(math.MinInt64), true}} {
			g.Add("sys/statsload", "statsload", it[0], it[1], it[2], e)
		}
	}
	for range 40 {
		var e any
		if rng.IntN(2) == 0 {
			e = fmt.Sprintf("err %d", rng.IntN(100))
		}
		g.Add("rand/statsload", "statsload", rng.Int64(), rng.Int64N(1000), rng.IntN(2) == 0, e)
	}
	for _, lim := range []int64{-1, 0, 1, 1 << 30} {
		for _, n := range []int64{0, 7, 1 << 20} {
			for _, det := range []bool{false, true} {
				g.Add("sys/tracker", "tracker", -9, lim, -10, lim/2, n, det)
			}
		}
	}
	for range 30 {
		g.Add("rand/tracker", "tracker", rng.IntN(40)-20, rng.Int64N(1<<20)-1, rng.IntN(40)-20, rng.Int64N(1<<20)-1,
			rng.Int64N(1<<16), rng.IntN(2) == 0)
	}
	// TestOptionalEvalPropProviders: every key with and without a provider.
	for k := 0; k < exprctx.OptPropsCnt; k++ {
		g.Add("TestOptionalEvalPropProviders", "expropt", k, nil)
		g.Add("TestOptionalEvalPropProviders", "expropt", k, ctx2bPayload(exprctx.OptionalEvalPropKey(k), rand.New(rand.NewPCG(1, uint64(k)))))
	}
	for range 60 {
		k := rng.IntN(exprctx.OptPropsCnt)
		var pl any
		if rng.IntN(4) != 0 {
			pl = ctx2bPayload(exprctx.OptionalEvalPropKey(k), rng)
		}
		g.Add("rand/expropt", "expropt", k, pl)
	}
	// debugtrace: fixed shapes, then random scripts.
	fixed := [][]any{
		{},
		{[]any{"step", []any{"n"}}},
		{[]any{"enter", 0}},
		{[]any{"enter", 0}, []any{"leave"}, []any{"enter", 1}, []any{"step", []any{"i", 1}}, []any{"step", []any{"i", 2}}},
		{[]any{"enter", 0}, []any{"step", []any{"f", "NaN"}}},
		{[]any{"enter", 0}, []any{"enter", 1}, []any{"step", []any{"f", "+Inf"}}},
		{[]any{"step", []any{"f", "-Inf"}}},
		{[]any{"named", "<a&b>", []any{"s", "<x>& "}}},
		{[]any{"record", []any{[]any{"s", "a"}, []any{"i", 1}, []any{"s", "a"}, []any{"i", 2}}}},
		{[]any{"record", []any{[]any{"s", "a"}}}},
		{[]any{"leave"}, []any{"leave"}, []any{"step", []any{"b", true}}},
		{[]any{"encode", []any{"m", []any{[]any{"z", []any{"i", 1}}, []any{"a", []any{"f", "0.5"}}}}}},
		{[]any{"encode", []any{"f", "NaN"}}},
	}
	for _, s := range fixed {
		g.Add("sys/debugtrace", "debugtrace", s)
	}
	for range 400 {
		g.Add("rand/debugtrace", "debugtrace", dtRandScript(rng))
	}
}

func ctx2bPayload(k exprctx.OptionalEvalPropKey, rng *rand.Rand) any {
	switch k {
	case exprctx.OptPropCurrentUser:
		var user any
		if rng.IntN(4) != 0 {
			user = []string{fmt.Sprintf("u%d", rng.IntN(9)), "h<&>"}
		}
		roles := []any{}
		for range rng.IntN(3) {
			roles = append(roles, []string{fmt.Sprintf("r%d", rng.IntN(9)), "%"})
		}
		return []any{user, roles}
	case exprctx.OptPropDDLOwnerInfo, exprctx.OptPropPrivilegeChecker:
		return rng.IntN(2) == 0
	case exprctx.OptPropAdvisoryLock:
		return rng.Uint64() >> 1
	case exprctx.OptPropSequenceOperator:
		var e any
		if rng.IntN(3) == 0 {
			e = "no sequence"
		}
		return []any{rng.Int64N(1000), e}
	case exprctx.OptPropSQLExecutor:
		if rng.IntN(3) == 0 {
			return []string{"err", "exec error"}
		}
		return []string{"ok"}
	}
	return true
}
