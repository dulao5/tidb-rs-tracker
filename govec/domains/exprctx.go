package domains

// Domain "exprctx": block D00.1 part 2a — pkg/expression/exprctx (context.go, optional.go,
// param.go), the provider set of pkg/expression/expropt (optional.go) and
// pkg/expression/exprstatic (the static EvalContext and ExprContext).
//
// An option list is [[name, value...], ...]:
//   eval options: ["warn", "static"|"ignore"|"nil"], ["sqlmode", n], ["flags", n], ["loc", offset|null],
//     ["levels", [7 levels]], ["db", s], ["time", sec, nsec] / ["time_err"], ["packet", n], ["week", s],
//     ["div", n], ["params", [int...]], ["redact", s], ["props", [key...]]
//   expr options: ["charset", cs, col], ["defcoll", s], ["block", s], ["sysdate", b], ["noop", n],
//     ["connid", n], ["window", b], ["gcml", n], ["alloc", offset], ["rng", seed]
// A Go panic is {"panic": true}; an error is {"err": msg}.
//
// Records:
//   - "keyset": args [set, key] -> AsPropKeySet / String / Desc / Add / Remove / Contains /
//     IsEmpty / IsFull.
//   - "alloc": args [offset, n] -> n AllocPlanColumnID results and GetLastPlanColumnID.
//   - "eval": args [opts, apply opts|null] -> the dump of NewEvalContext(opts) (and of Apply).
//   - "expr": args [eval opts, expr opts, apply opts|null] -> the dump of NewExprContext.
//   - "truncate": args [flags, levels, level] -> CtxWithHandleTruncateErrLevel and the wrappers.
//   - "load": args [[[name, value]...]] -> LoadSystemVars of a default ExprContext.
//   - "static": args [eval opts, expr opts] -> MakeExprContextStatic.
//   - "getprop": no args -> the missing-provider error of getPropProvider; EmptyParamValues.

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/errctx"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/expression/expropt"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
	"github.com/pingcap/tidb/pkg/util/mathutil"
)

const exprctxSeed = 20261111

func init() {
	vec.Register(&vec.Domain{
		Name:     "exprctx",
		Seed:     exprctxSeed,
		Coverage: exprctxCoverage,
		Blocks:   []string{"D00.1"},
		Exempt: map[string]string{
			"AssertLocationWithSessionVars": "an intest.Assert only: no effect in production builds",
		},
		Gzip:     true,
		Generate: exprctxGenerate,
		Exec:     exprctxExec,
	})
}

var exprctxCoverage = map[string]vec.Coverage{
	"keyset": {Min: 300, GoFuncs: []string{"init", "OptionalEvalPropKey.AsPropKeySet", "OptionalEvalPropKey.Desc",
		"OptionalEvalPropKey.String", "OptionalEvalPropDesc.Key", "OptionalEvalPropKeySet.Add",
		"OptionalEvalPropKeySet.Remove", "OptionalEvalPropKeySet.Contains", "OptionalEvalPropKeySet.IsEmpty",
		"OptionalEvalPropKeySet.IsFull"}},
	"alloc": {Min: 20, GoFuncs: []string{"NewSimplePlanColumnIDAllocator", "SimplePlanColumnIDAllocator.AllocPlanColumnID",
		"SimplePlanColumnIDAllocator.GetLastPlanColumnID"}},
	"eval": {Min: 500, GoFuncs: []string{"timeOnce.getTime", "WithWarnHandler", "WithSQLMode", "WithTypeFlags",
		"WithLocation", "WithErrLevelMap", "WithCurrentDB", "WithCurrentTime", "WithMaxAllowedPacket",
		"WithDefaultWeekFormatMode", "WithDivPrecisionIncrement", "WithOptionalProperty", "WithParamList",
		"WithEnableRedactLog", "WithUserVarsReader", "NewEvalContext", "EvalContext.CtxID", "EvalContext.SQLMode",
		"EvalContext.TypeCtx", "EvalContext.ErrCtx", "EvalContext.Location", "EvalContext.AppendWarning",
		"EvalContext.AppendNote", "EvalContext.WarningCount", "EvalContext.TruncateWarnings",
		"EvalContext.CopyWarnings", "EvalContext.CurrentDB", "EvalContext.CurrentTime",
		"EvalContext.GetMaxAllowedPacket", "EvalContext.GetTiDBRedactLog", "EvalContext.GetDefaultWeekFormatMode",
		"EvalContext.GetDivPrecisionIncrement", "EvalContext.GetUserVarsReader", "EvalContext.GetOptionalPropSet",
		"EvalContext.GetOptionalPropProvider", "EvalContext.Apply", "EvalContext.GetParamValue",
		"EvalContext.AllParamValues", "EvalContext.GetWarnHandler", "OptionalEvalPropProviders.Contains",
		"OptionalEvalPropProviders.Get", "OptionalEvalPropProviders.Add", "OptionalEvalPropProviders.PropKeySet"}},
	"expr": {Min: 300, GoFuncs: []string{"WithEvalCtx", "WithCharset", "WithDefaultCollationForUTF8MB4",
		"WithBlockEncryptionMode", "WithSysDateIsNow", "WithNoopFuncsMode", "WithRng", "WithPlanCacheTracker",
		"WithColumnIDAllocator", "WithConnectionID", "WithWindowingUseHighPrecision", "WithGroupConcatMaxLen",
		"NewExprContext", "ExprContext.Apply", "ExprContext.GetEvalCtx", "ExprContext.GetStaticEvalCtx",
		"ExprContext.GetCharsetInfo", "ExprContext.GetDefaultCollationForUTF8MB4", "ExprContext.GetBlockEncryptionMode",
		"ExprContext.GetSysdateIsNow", "ExprContext.GetNoopFuncsMode", "ExprContext.Rng", "ExprContext.IsUseCache",
		"ExprContext.SetSkipPlanCache", "ExprContext.AllocPlanColumnID", "ExprContext.IsInNullRejectCheck",
		"ExprContext.IsConstantPropagateCheck", "ExprContext.ConnectionID", "ExprContext.GetWindowingUseHighPrecision",
		"ExprContext.GetGroupConcatMaxLen", "ExprContext.GetLastPlanColumnID", "ExprContext.GetPlanCacheTracker",
		"ExprContext.GetStaticConvertibleEvalContext", "ExprContext.IsReadonlyUserVar"}},
	"truncate": {Min: 200, GoFuncs: []string{"CtxWithHandleTruncateErrLevel", "innerOverrideEvalContext.TypeCtx",
		"innerOverrideEvalContext.ErrCtx", "innerOverrideBuildContext.GetEvalCtx", "WithNullRejectCheck",
		"NullRejectCheckExprContext.IsInNullRejectCheck", "WithConstantPropagateCheck",
		"ConstantPropagateCheckContext.IsConstantPropagateCheck"}},
	"load": {Min: 300, GoFuncs: []string{"EvalContext.LoadSystemVars", "EvalContext.loadSessionVarsInternal",
		"EvalContext.currentTimeFuncFromStringVal", "newSessionVarsWithSystemVariables", "ExprContext.LoadSystemVars",
		"ExprContext.loadSessionVarsInternal"}},
	"static":  {Min: 100, GoFuncs: []string{"MakeEvalContextStatic", "MakeExprContextStatic"}},
	"getprop": {Min: 1, GoFuncs: []string{"getPropProvider", "emptyParamValues.GetParamValue"}},
}

// ecTry runs f; a Go panic is {"panic": true}.
func ecTry(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": true}
		}
	}()
	return f()
}

func ecErr(err error) any { return map[string]any{"err": vec.GoString(err.Error())} }

// ecProvider is a provider of the expropt type for key k (nil functions: only Desc is used).
func ecProvider(k int) exprctx.OptionalEvalPropProvider {
	switch k {
	case 0:
		return expropt.CurrentUserPropProvider(nil)
	case 1:
		return expropt.NewSessionVarsProvider(nil)
	case 2:
		return expropt.InfoSchemaPropProvider(nil)
	case 3:
		return expropt.KVStorePropProvider(nil)
	case 4:
		return expropt.SQLExecutorPropProvider(nil)
	case 5:
		return expropt.SequenceOperatorProvider(nil)
	case 6:
		return expropt.NewAdvisoryLockPropProvider(nil)
	case 7:
		return expropt.DDLOwnerInfoProvider(nil)
	case 8:
		return expropt.PrivilegeCheckerProvider(nil)
	}
	panic("ecProvider")
}

func ecRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func ecEvalOpts(raw json.RawMessage) []exprstatic.EvalCtxOption {
	var opts [][]json.RawMessage
	if err := json.Unmarshal(raw, &opts); err != nil {
		panic(err)
	}
	var out []exprstatic.EvalCtxOption
	for _, o := range opts {
		switch arg[string](o, 0) {
		case "warn":
			switch arg[string](o, 1) {
			case "static":
				out = append(out, exprstatic.WithWarnHandler(contextutil.NewStaticWarnHandler(0)))
			case "ignore":
				out = append(out, exprstatic.WithWarnHandler(contextutil.IgnoreWarn))
			default:
				out = append(out, exprstatic.WithWarnHandler(nil))
			}
		case "sqlmode":
			out = append(out, exprstatic.WithSQLMode(mysql.SQLMode(arg[int64](o, 1))))
		case "flags":
			out = append(out, exprstatic.WithTypeFlags(types.Flags(arg[uint16](o, 1))))
		case "loc":
			if string(o[1]) == "null" {
				out = append(out, exprstatic.WithLocation(nil))
			} else {
				out = append(out, exprstatic.WithLocation(gotime.FixedZone("Z", arg[int](o, 1))))
			}
		case "levels":
			var lm errctx.LevelMap
			for i, l := range arg[[]int](o, 1) {
				lm[i] = errctx.Level(l)
			}
			out = append(out, exprstatic.WithErrLevelMap(lm))
		case "db":
			out = append(out, exprstatic.WithCurrentDB(arg[string](o, 1)))
		case "time":
			sec, nsec := arg[int64](o, 1), arg[int64](o, 2)
			out = append(out, exprstatic.WithCurrentTime(func() (gotime.Time, error) {
				return gotime.Unix(sec, nsec), nil
			}))
		case "time_err":
			out = append(out, exprstatic.WithCurrentTime(func() (gotime.Time, error) {
				return gotime.Time{}, errors.New("time error")
			}))
		case "packet":
			out = append(out, exprstatic.WithMaxAllowedPacket(arg[uint64](o, 1)))
		case "week":
			out = append(out, exprstatic.WithDefaultWeekFormatMode(arg[string](o, 1)))
		case "div":
			out = append(out, exprstatic.WithDivPrecisionIncrement(arg[int](o, 1)))
		case "params":
			pl := variable.NewPlanCacheParamList()
			for _, v := range arg[[]int64](o, 1) {
				pl.Append(types.NewIntDatum(v))
			}
			out = append(out, exprstatic.WithParamList(pl))
		case "redact":
			out = append(out, exprstatic.WithEnableRedactLog(arg[string](o, 1)))
		case "props":
			var ps []exprctx.OptionalEvalPropProvider
			for _, k := range arg[[]int](o, 1) {
				ps = append(ps, ecProvider(k))
			}
			out = append(out, exprstatic.WithOptionalProperty(ps...))
		default:
			panic("ecEvalOpts: " + string(o[0]))
		}
	}
	return out
}

func ecExprOpts(raw json.RawMessage, evalCtx *exprstatic.EvalContext) []exprstatic.ExprCtxOption {
	var opts [][]json.RawMessage
	if err := json.Unmarshal(raw, &opts); err != nil {
		panic(err)
	}
	var out []exprstatic.ExprCtxOption
	if evalCtx != nil {
		out = append(out, exprstatic.WithEvalCtx(evalCtx))
	}
	for _, o := range opts {
		switch arg[string](o, 0) {
		case "charset":
			out = append(out, exprstatic.WithCharset(arg[string](o, 1), arg[string](o, 2)))
		case "defcoll":
			out = append(out, exprstatic.WithDefaultCollationForUTF8MB4(arg[string](o, 1)))
		case "block":
			out = append(out, exprstatic.WithBlockEncryptionMode(arg[string](o, 1)))
		case "sysdate":
			out = append(out, exprstatic.WithSysDateIsNow(arg[bool](o, 1)))
		case "noop":
			out = append(out, exprstatic.WithNoopFuncsMode(arg[int](o, 1)))
		case "connid":
			out = append(out, exprstatic.WithConnectionID(arg[uint64](o, 1)))
		case "window":
			out = append(out, exprstatic.WithWindowingUseHighPrecision(arg[bool](o, 1)))
		case "gcml":
			out = append(out, exprstatic.WithGroupConcatMaxLen(arg[uint64](o, 1)))
		case "alloc":
			out = append(out, exprstatic.WithColumnIDAllocator(exprctx.NewSimplePlanColumnIDAllocator(arg[int64](o, 1))))
		case "rng":
			out = append(out, exprstatic.WithRng(mathutil.NewWithSeed(arg[int64](o, 1))))
		case "tracker":
			t := contextutil.NewPlanCacheTracker(contextutil.IgnoreWarn)
			if arg[bool](o, 1) {
				t.EnablePlanCache()
			}
			out = append(out, exprstatic.WithPlanCacheTracker(&t))
		default:
			panic("ecExprOpts: " + string(o[0]))
		}
	}
	return out
}

func ecWarns(ws []contextutil.SQLWarn) any {
	out := make([]any, 0, len(ws))
	for _, w := range ws {
		out = append(out, []any{w.Level, vec.GoString(w.Err.Error())})
	}
	return out
}

// ecTime: the current time; hasTime false means time.Now() (only the location is recorded).
func ecTime(ctx exprctx.EvalContext, hasTime bool) any {
	tm, err := ctx.CurrentTime()
	if err != nil {
		return ecErr(err)
	}
	if !hasTime {
		return map[string]any{"now": true, "loc": tm.Location().String()}
	}
	return map[string]any{"unix": tm.Unix(), "ns": tm.Nanosecond(), "loc": tm.Location().String()}
}

// ecEvalDump: the observable state of an EvalContext. warn appends warnings through the
// type context, the error context and the context itself, then reads them back.
func ecEvalDump(ctx exprctx.StaticConvertibleEvalContext, hasTime bool, warn bool) map[string]any {
	m := map[string]any{}
	tc, ec := ctx.TypeCtx(), ctx.ErrCtx()
	m["sqlmode"] = int64(ctx.SQLMode())
	m["flags"] = uint16(tc.Flags())
	m["loc"] = ctx.Location().String()
	m["tloc"] = tc.Location().String()
	lm := ec.LevelMap()
	levels := make([]int, len(lm))
	for i, l := range lm {
		levels[i] = int(l)
	}
	m["levels"] = levels
	m["db"] = vec.GoString(ctx.CurrentDB())
	m["time"] = ecTime(ctx, hasTime)
	m["packet"] = ctx.GetMaxAllowedPacket()
	m["redact"] = ctx.GetTiDBRedactLog()
	m["week"] = ctx.GetDefaultWeekFormatMode()
	m["div"] = ctx.GetDivPrecisionIncrement()
	params := []any{}
	for i := -1; i <= len(ctx.AllParamValues()); i++ {
		d, err := ctx.GetParamValue(i)
		if err != nil {
			params = append(params, ecErr(err))
		} else {
			params = append(params, d.GetInt64())
		}
	}
	m["params"] = params
	m["props"] = uint64(ctx.GetOptionalPropSet())
	got := []any{}
	for k := -1; k <= exprctx.OptPropsCnt; k++ {
		p, ok := ctx.GetOptionalPropProvider(exprctx.OptionalEvalPropKey(k))
		if ok {
			got = append(got, int(p.Desc().Key()))
		} else {
			got = append(got, nil)
		}
	}
	m["prov"] = got
	_, uv := ctx.GetUserVarsReader().GetUserVarVal("a")
	m["uservar"] = uv
	if warn {
		tc.AppendWarning(errors.New("w-type"))
		ec.AppendWarning(errors.New("w-err"))
		ctx.AppendWarning(errors.New("w-ctx"))
		ctx.AppendNote(errors.New("n-ctx"))
		m["wcount"] = ctx.WarningCount()
		m["wcopy"] = ecWarns(ctx.CopyWarnings(nil))
		m["whcount"] = ctx.GetWarnHandler().WarningCount()
		m["wtrunc"] = ecWarns(ctx.TruncateWarnings(1))
		m["wcount2"] = ctx.WarningCount()
	}
	return m
}

func ecHasTime(raw json.RawMessage) bool {
	var opts [][]json.RawMessage
	_ = json.Unmarshal(raw, &opts)
	has := false
	for _, o := range opts {
		switch arg[string](o, 0) {
		case "time", "time_err":
			has = true
		}
	}
	return has
}

func ecExprDump(ctx *exprstatic.ExprContext, warn bool) map[string]any {
	m := map[string]any{}
	cs, col := ctx.GetCharsetInfo()
	m["cs"], m["col"] = cs, col
	m["defcoll"] = ctx.GetDefaultCollationForUTF8MB4()
	m["block"] = ctx.GetBlockEncryptionMode()
	m["sysdate"] = ctx.GetSysdateIsNow()
	m["noop"] = ctx.GetNoopFuncsMode()
	m["connid"] = ctx.ConnectionID()
	m["window"] = ctx.GetWindowingUseHighPrecision()
	m["gcml"] = ctx.GetGroupConcatMaxLen()
	m["nr"] = ctx.IsInNullRejectCheck()
	m["cp"] = ctx.IsConstantPropagateCheck()
	m["ro"] = ctx.IsReadonlyUserVar("a")
	m["same_eval"] = ctx.GetEvalCtx() == exprctx.EvalContext(ctx.GetStaticEvalCtx()) &&
		ctx.GetStaticConvertibleEvalContext() == exprctx.StaticConvertibleEvalContext(ctx.GetStaticEvalCtx())
	m["same_tracker"] = ctx.GetPlanCacheTracker() == ctx.GetPlanCacheTracker()
	if warn {
		ids := []int64{ctx.GetLastPlanColumnID()}
		for range 3 {
			ids = append(ids, ctx.AllocPlanColumnID())
		}
		ids = append(ids, ctx.GetLastPlanColumnID())
		m["ids"] = ids
		m["cache0"] = ctx.IsUseCache()
		ctx.SetSkipPlanCache("some reason")
		m["cache1"] = ctx.IsUseCache()
		m["warns"] = ecWarns(ctx.GetEvalCtx().CopyWarnings(nil))
	}
	return m
}

func ecRng(ctx *exprstatic.ExprContext, seeded bool) any {
	if !seeded {
		return nil
	}
	r := ctx.Rng()
	return []any{r.Gen(), r.Gen()}
}

func ecSeeded(raw json.RawMessage) bool {
	var opts [][]json.RawMessage
	_ = json.Unmarshal(raw, &opts)
	for _, o := range opts {
		if arg[string](o, 0) == "rng" {
			return true
		}
	}
	return false
}

func exprctxExec(op string, p []json.RawMessage) (out any, err error) {
	switch op {
	case "keyset":
		set, key := exprctx.OptionalEvalPropKeySet(arg[uint64](p, 0)), exprctx.OptionalEvalPropKey(arg[int](p, 1))
		m := map[string]any{}
		m["as"] = ecTry(func() any { return uint64(key.AsPropKeySet()) })
		m["str"] = ecTry(func() any { return key.String() })
		m["desc"] = ecTry(func() any { d := key.Desc(); return []any{int(d.Key())} })
		m["add"] = ecTry(func() any { return uint64(set.Add(key)) })
		m["rm"] = ecTry(func() any { return uint64(set.Remove(key)) })
		m["has"] = ecTry(func() any { return set.Contains(key) })
		m["empty"] = set.IsEmpty()
		m["full"] = set.IsFull()
		return m, nil
	case "alloc":
		a := exprctx.NewSimplePlanColumnIDAllocator(arg[int64](p, 0))
		ids := []int64{}
		for range arg[int](p, 1) {
			ids = append(ids, a.AllocPlanColumnID())
		}
		return map[string]any{"ids": ids, "last": a.GetLastPlanColumnID()}, nil
	case "eval":
		ctx := exprstatic.NewEvalContext(ecEvalOpts(p[0])...)
		hasTime := ecHasTime(p[0])
		m := map[string]any{"ctx": ecEvalDump(ctx, hasTime, false)}
		if string(p[1]) != "null" {
			nctx := ctx.Apply(ecEvalOpts(p[1])...)
			m["apply"] = ecEvalDump(nctx, hasTime || ecHasTime(p[1]), false)
			m["new_id"] = nctx.CtxID() != ctx.CtxID()
			m["apply_warn"] = ecEvalDump(nctx, hasTime || ecHasTime(p[1]), true)
		}
		m["warn"] = ecEvalDump(ctx, hasTime, true)
		return m, nil
	case "expr":
		var ev *exprstatic.EvalContext
		if string(p[0]) != "null" {
			ev = exprstatic.NewEvalContext(ecEvalOpts(p[0])...)
		}
		ctx := exprstatic.NewExprContext(ecExprOpts(p[1], ev)...)
		m := map[string]any{"ctx": ecExprDump(ctx, false), "rng": ecRng(ctx, ecSeeded(p[1]))}
		if string(p[2]) != "null" {
			nctx := ctx.Apply(ecExprOpts(p[2], nil)...)
			m["apply"] = ecExprDump(nctx, false)
			m["apply_rng"] = ecRng(nctx, ecSeeded(p[1]) || ecSeeded(p[2]))
			m["apply_warn"] = ecExprDump(nctx, true)
		}
		m["warn"] = ecExprDump(ctx, true)
		return m, nil
	case "truncate":
		var lm errctx.LevelMap
		for i, l := range arg[[]int](p, 1) {
			lm[i] = errctx.Level(l)
		}
		ev := exprstatic.NewEvalContext(exprstatic.WithTypeFlags(types.Flags(arg[uint16](p, 0))), exprstatic.WithErrLevelMap(lm))
		ctx := exprstatic.NewExprContext(exprstatic.WithEvalCtx(ev))
		level := errctx.Level(arg[int](p, 2))
		nctx := exprctx.CtxWithHandleTruncateErrLevel(ctx, level)
		m := map[string]any{}
		m["same"] = nctx == exprctx.BuildContext(ctx)
		nev := nctx.GetEvalCtx()
		ntc, nec := nev.TypeCtx(), nev.ErrCtx()
		m["flags"] = uint16(ntc.Flags())
		nlm := nec.LevelMap()
		levels := make([]int, len(nlm))
		for i, l := range nlm {
			levels[i] = int(l)
		}
		m["levels"] = levels
		m["id_same"] = nev.CtxID() == ev.CtxID()
		m["sqlmode"] = int64(nev.SQLMode())
		m["cs"], _ = nctx.GetCharsetInfo()
		ntc.AppendWarning(errors.New("w1"))
		nev.AppendWarning(errors.New("w2"))
		m["warns"] = ecWarns(ev.CopyWarnings(nil))
		m["nr"] = []bool{exprctx.WithNullRejectCheck(ctx).IsInNullRejectCheck(), exprctx.WithNullRejectCheck(ctx).IsConstantPropagateCheck()}
		m["cp"] = []bool{exprctx.WithConstantPropagateCheck(ctx).IsConstantPropagateCheck(), exprctx.WithConstantPropagateCheck(ctx).IsInNullRejectCheck()}
		m["nr_gcml"] = exprctx.WithNullRejectCheck(ctx).GetGroupConcatMaxLen()
		return m, nil
	case "load":
		var kvs [][]string
		if err := json.Unmarshal(p[0], &kvs); err != nil {
			panic(err)
		}
		sys := map[string]string{}
		hasTime := false
		for _, kv := range kvs {
			sys[kv[0]] = kv[1]
			if kv[0] == "timestamp" {
				hasTime = true
			}
		}
		base := exprstatic.NewExprContext(exprstatic.WithEvalCtx(exprstatic.NewEvalContext(
			exprstatic.WithCurrentTime(func() (gotime.Time, error) { return gotime.Unix(1600000000, 5), nil }))))
		m := map[string]any{}
		nctx, err := base.LoadSystemVars(sys)
		if err != nil {
			m["err"] = vec.GoString(err.Error())
		} else {
			_ = hasTime
			m["expr"] = ecExprDump(nctx, false)
			m["eval"] = ecEvalDump(nctx.GetStaticEvalCtx(), true, false)
		}
		ev := exprstatic.NewEvalContext()
		nev, err := ev.LoadSystemVars(sys)
		if err != nil {
			m["eerr"] = vec.GoString(err.Error())
		} else {
			m["eval2"] = ecEvalDump(nev, hasTime, false)
		}
		return m, nil
	case "static":
		ev := exprstatic.NewEvalContext(ecEvalOpts(p[0])...)
		ctx := exprstatic.NewExprContext(ecExprOpts(p[1], ev)...)
		sctx := exprstatic.MakeExprContextStatic(ctx)
		m := map[string]any{}
		m["expr"] = ecExprDump(sctx, false)
		m["eval"] = ecEvalDump(sctx.GetStaticEvalCtx(), ecHasTime(p[0]), false)
		m["new_id"] = sctx.GetStaticEvalCtx().CtxID() != ev.CtxID()
		m["same_tracker"] = sctx.GetPlanCacheTracker() == ctx.GetPlanCacheTracker()
		m["same_rng"] = sctx.Rng() == ctx.Rng()
		m["last"] = []int64{ctx.GetLastPlanColumnID(), sctx.GetLastPlanColumnID(), sctx.AllocPlanColumnID(), ctx.GetLastPlanColumnID()}
		sctx.GetEvalCtx().AppendWarning(errors.New("w-static"))
		m["shared_warn"] = ev.WarningCount()
		return m, nil
	case "getprop":
		ctx := exprstatic.NewEvalContext()
		m := map[string]any{}
		_, err := expropt.CurrentUserPropReader{}.CurrentUser(ctx)
		m["err"] = vec.GoString(err.Error())
		_, err = exprctx.EmptyParamValues.GetParamValue(0)
		m["param"] = vec.GoString(err.Error())
		return m, nil
	}
	panic("exprctx: unknown op " + op)
}

// ---------------------------------------------------------------- generator

func ecRandEvalOpts(rng *rand.Rand) [][]any {
	var opts [][]any
	n := rng.IntN(6)
	for range n {
		switch rng.IntN(15) {
		case 0:
			opts = append(opts, []any{"warn", []string{"static", "ignore", "nil"}[rng.IntN(3)]})
		case 1:
			opts = append(opts, []any{"sqlmode", []int64{0, 1, 1 << 21, int64(mysql.ModeStrictTransTables | mysql.ModeNoZeroDate), 1<<40 - 1}[rng.IntN(5)]})
		case 2:
			opts = append(opts, []any{"flags", uint16(rng.Uint32() & 0x3fff)})
		case 3:
			if rng.IntN(4) == 0 {
				opts = append(opts, []any{"loc", nil})
			} else {
				opts = append(opts, []any{"loc", []int{0, 3600, -18000, 28800, 19800, 50400}[rng.IntN(6)]})
			}
		case 4:
			lv := make([]int, 7)
			for i := range lv {
				lv[i] = rng.IntN(3)
			}
			opts = append(opts, []any{"levels", lv})
		case 5:
			opts = append(opts, []any{"db", []string{"", "test", "Db1", "中文"}[rng.IntN(4)]})
		case 6:
			if rng.IntN(5) == 0 {
				opts = append(opts, []any{"time_err"})
			} else {
				opts = append(opts, []any{"time", []int64{0, 1600000000, -100, 4102444800}[rng.IntN(4)], rng.Int64N(1e9)})
			}
		case 7:
			opts = append(opts, []any{"packet", []uint64{0, 1024, 67108864, 1 << 30}[rng.IntN(4)]})
		case 8:
			opts = append(opts, []any{"week", []string{"0", "3", "7", "x"}[rng.IntN(4)]})
		case 9:
			opts = append(opts, []any{"div", []int{0, 4, 30, -1}[rng.IntN(4)]})
		case 10:
			ps := []int64{}
			for range rng.IntN(4) {
				ps = append(ps, rng.Int64N(100)-50)
			}
			opts = append(opts, []any{"params", ps})
		case 11:
			opts = append(opts, []any{"redact", []string{"OFF", "ON", "MARKER"}[rng.IntN(3)]})
		case 12, 13:
			ks := []int{}
			for range rng.IntN(5) {
				ks = append(ks, rng.IntN(exprctx.OptPropsCnt))
			}
			opts = append(opts, []any{"props", ks})
		case 14:
			opts = append(opts, []any{"warn", "static"})
		}
	}
	return opts
}

func ecRandExprOpts(rng *rand.Rand) [][]any {
	var opts [][]any
	for range rng.IntN(6) {
		switch rng.IntN(11) {
		case 0:
			pair := [][2]string{{"latin1", "latin1_bin"}, {"utf8mb4", "utf8mb4_general_ci"}, {"binary", "binary"}}[rng.IntN(3)]
			opts = append(opts, []any{"charset", pair[0], pair[1]})
		case 1:
			opts = append(opts, []any{"defcoll", []string{"utf8mb4_0900_ai_ci", "utf8mb4_general_ci"}[rng.IntN(2)]})
		case 2:
			opts = append(opts, []any{"block", []string{"aes-256-cbc", "aes-128-ecb"}[rng.IntN(2)]})
		case 3:
			opts = append(opts, []any{"sysdate", rng.IntN(2) == 0})
		case 4:
			opts = append(opts, []any{"noop", rng.IntN(3)})
		case 5:
			opts = append(opts, []any{"connid", rng.Uint64() >> uint(rng.IntN(64))})
		case 6:
			opts = append(opts, []any{"window", rng.IntN(2) == 0})
		case 7:
			opts = append(opts, []any{"gcml", []uint64{4, 1024, 1 << 32}[rng.IntN(3)]})
		case 8:
			opts = append(opts, []any{"alloc", []int64{0, 7, 1 << 40, -3}[rng.IntN(4)]})
		case 9:
			opts = append(opts, []any{"rng", rng.Int64N(1 << 40)})
		case 10:
			opts = append(opts, []any{"tracker", rng.IntN(2) == 0})
		}
	}
	return opts
}

var ecLoadPool = [][][2]string{
	{{"time_zone", "+08:00"}, {"time_zone", "-05:30"}, {"time_zone", "UTC"}, {"time_zone", "bad zone"}},
	{{"sql_mode", ""}, {"sql_mode", "ANSI"}, {"sql_mode", "STRICT_TRANS_TABLES,NO_ZERO_DATE"}, {"sql_mode", "no_such_mode"}},
	{{"timestamp", "1700000000.25"}, {"timestamp", "100"}, {"timestamp", "1.5e9"}, {"timestamp", "abc"}},
	{{"max_allowed_packet", "1024"}, {"max_allowed_packet", "4194304"}, {"max_allowed_packet", "x"}},
	{{"tidb_redact_log", "ON"}, {"tidb_redact_log", "MARKER"}, {"tidb_redact_log", "OFF"}},
	{{"default_week_format", "3"}, {"default_week_format", "7"}, {"default_week_format", "9"}},
	{{"div_precision_increment", "0"}, {"div_precision_increment", "12"}, {"div_precision_increment", "31"}},
	{{"character_set_connection", "latin1"}, {"character_set_connection", "utf8mb4"}, {"character_set_connection", "gbk"}},
	{{"collation_connection", "latin1_bin"}, {"collation_connection", "utf8mb4_general_ci"}, {"collation_connection", "gbk_bin"}},
	{{"default_collation_for_utf8mb4", "utf8mb4_general_ci"}, {"default_collation_for_utf8mb4", "utf8mb4_0900_ai_ci"}},
	{{"block_encryption_mode", "aes-256-cbc"}, {"block_encryption_mode", "aes-192-ofb"}},
	{{"tidb_sysdate_is_now", "ON"}, {"tidb_sysdate_is_now", "OFF"}},
	{{"tidb_enable_noop_functions", "WARN"}, {"tidb_enable_noop_functions", "ON"}},
	{{"windowing_use_high_precision", "OFF"}, {"windowing_use_high_precision", "ON"}},
	{{"group_concat_max_len", "4096"}, {"group_concat_max_len", "4"}},
	{{"autocommit", "OFF"}, {"no_such_variable", "1"}},
	{{"Time_Zone", "+01:00"}, {"SQL_MODE", "ANSI_QUOTES"}},
}

// ecLoadBad: values that fail SetSystemVar (at most one per record: map order is random).
var ecLoadBad = map[[2]string]bool{
	{"time_zone", "bad zone"}: true, {"sql_mode", "no_such_mode"}: true, {"timestamp", "abc"}: true,
	{"max_allowed_packet", "x"}: true, {"no_such_variable", "1"}: true,
}

func exprctxGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/getprop", "getprop")
	for k := -2; k <= exprctx.OptPropsCnt+2; k++ {
		for _, set := range []uint64{0, 1<<9 - 1, 0x155, 1 << 63, 1<<64 - 1} {
			g.Add("sys/keyset", "keyset", set, k)
		}
	}
	for range 300 {
		g.Add("rand/keyset", "keyset", rng.Uint64()>>uint(rng.IntN(64)), rng.IntN(14)-2)
	}
	for _, off := range []int64{0, 5, -1, 1<<63 - 2} {
		g.Add("sys/alloc", "alloc", off, 3)
	}
	for range 20 {
		g.Add("rand/alloc", "alloc", rng.Int64N(1000), rng.IntN(5))
	}
	g.Add("sys/eval", "eval", [][]any{}, nil)
	g.Add("sys/eval", "eval", [][]any{{"time", 1600000000, 123}}, [][]any{{"loc", 28800}})
	for range 600 {
		var ap any
		if rng.IntN(2) == 0 {
			ap = ecRandEvalOpts(rng)
		}
		g.Add("rand/eval", "eval", ecRandEvalOpts(rng), ap)
	}
	g.Add("sys/expr", "expr", nil, [][]any{}, nil)
	for range 400 {
		var ev, ap any
		if rng.IntN(3) != 0 {
			ev = ecRandEvalOpts(rng)
		}
		if rng.IntN(2) == 0 {
			ap = ecRandExprOpts(rng)
		}
		g.Add("rand/expr", "expr", ev, ecRandExprOpts(rng), ap)
	}
	for f := range 16 {
		for _, lvl := range []int{0, 1, 2} {
			lv := []int{0, 0, 0, 0, 0, 0, 0}
			lv[1] = []int{0, 1, 2}[f%3]
			flags := []uint16{0, 1 << 3, 1 << 4, 1<<3 | 1<<4}[f%4] | uint16(f/4)<<1
			g.Add("sys/truncate", "truncate", flags, lv, lvl)
		}
	}
	for range 200 {
		lv := make([]int, 7)
		for i := range lv {
			lv[i] = rng.IntN(3)
		}
		g.Add("rand/truncate", "truncate", uint16(rng.Uint32()&0x3fff), lv, rng.IntN(3))
	}
	for _, grp := range ecLoadPool {
		for _, kv := range grp {
			g.Add("sys/load", "load", [][2]string{kv})
		}
	}
	for range 400 {
		var kvs [][2]string
		seen := map[int]bool{}
		names := map[string]bool{} // lower-cased: two cases of one name make the result map-order dependent
		bad := false
		for range 1 + rng.IntN(4) {
			gi := rng.IntN(len(ecLoadPool))
			if seen[gi] {
				continue
			}
			kv := ecLoadPool[gi][rng.IntN(len(ecLoadPool[gi]))]
			if names[strings.ToLower(kv[0])] {
				continue
			}
			if ecLoadBad[kv] {
				if bad {
					continue
				}
				bad = true
			}
			seen[gi] = true
			names[strings.ToLower(kv[0])] = true
			kvs = append(kvs, kv)
		}
		g.Add("rand/load", "load", kvs)
	}
	for range 150 {
		ev := ecRandEvalOpts(rng)
		ev = append(ev, []any{"time", 1650000000, 42})
		g.Add("rand/static", "static", ev, ecRandExprOpts(rng))
	}
}
