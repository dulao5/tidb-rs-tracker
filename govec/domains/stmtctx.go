package domains

// Domain "stmtctx": block D00.1 part 1 — pkg/errctx, pkg/util/context (warn.go,
// plancache.go, context.go) and the core of pkg/sessionctx/stmtctx (warnings, error
// levels, type flags, push-down flags, row counters, statement cache, stale TSO,
// reference count, reserved row IDs). The other D00.1 files (exprctx, exprstatic,
// expropt, sessionexpr, planctx, ranger types, ...) are part 2 (stmtctx_pending.go).
//
// Records:
//   - "script": args[0] is a list of steps run against one environment: a
//     StaticWarnHandler h, an errctx.Context ec (on h), a PlanCacheTracker pct and
//     RangeFallbackHandler rfh (on h), a StatementContext sc, a ReferenceCount rc and a
//     ReservedRowIDAlloc rr. out is one entry per step; a Go panic is {"panic": msg}
//     and ends the script.
//   - "sqlwarn_marshal": args [level, err] -> json.Marshal(&SQLWarn{...}) as a string.
//   - "sqlwarn_unmarshal": args [json text] -> {level, err} or the error.
//   - "push_down": args [type flags, level map] -> PushDownFlagsWithTypeFlagsAndErrLevels.
//   - "resolve": args [ignore, warn] -> ResolveErrLevel.
//
// Errors are given as small integers (stmtctxErr): e >= 0 is
// terror.ErrClass(stmtctxClasses[e]).NewStdErr(code, errno.MySQLErrName[code]), -1 is errors.New("plain error"), -2 is
// errors.New(""). An error result is {"code": terror code or 0, "msg": Error()}; a
// warning list is [[level, err], ...].

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/errctx"
	"github.com/pingcap/tidb/pkg/errno"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/sessionctx/stmtctx"
	"github.com/pingcap/tidb/pkg/types"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
)

const stmtctxSeed = 20261109

// stmtctxCodes: the error codes of the generated errors (every errctx group, and codes
// in no group).
var stmtctxCodes = []uint16{
	errno.ErrTruncatedWrongValue, errno.ErrDataTooLong, errno.ErrTruncatedWrongValueForField,
	errno.ErrWarnDataOutOfRange, errno.ErrDataOutOfRange, errno.ErrBadNumber, errno.ErrWrongValueForType,
	errno.ErrDatetimeFunctionOverflow, errno.WarnDataTruncated, errno.ErrIncorrectDatetimeValue,
	errno.ErrBadNull, errno.ErrWarnNullToNotnull, errno.ErrNoDefaultForField, errno.ErrDivisionByZero,
	errno.ErrAutoincReadFailed, errno.ErrNoPartitionForGivenValue, errno.ErrRowDoesNotMatchGivenPartitionSet,
	errno.ErrDupEntry,
	errno.ErrUnknown, errno.ErrParse, errno.ErrBadField, errno.ErrInternal,
}

// stmtctxClasses: the error class of each code, the smallest class whose registry
// (terror.ErrClassToMySQLCodes of tidb-server v8.5.8) has the code.
var stmtctxClasses = []int{2, 20, 19, 2, 20, 20, 6, 20, 2, 20, 2, 5, 19, 6, 1, 19, 19, 8, 6, 11, 2, 10}

// StmtctxMinedTests lists the Go tests whose scenarios are scripts here.
func StmtctxMinedTests() []string {
	return []string{"TestContext", "TestSQLWarn", "TestIgnoreWarn", "TestStaticWarnHandler", "TestCopyWarnHandler",
		"TestStatementContextPushDownFLags", "TestNewStmtCtx", "TestSetStmtCtxTypeFlags", "TestResetStmtCtx",
		"TestErrCtx", "TestReservedRowIDAlloc"}
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "stmtctx",
		Seed:     stmtctxSeed,
		Coverage: stmtctxCoverage,
		Blocks:   []string{"D00.1", "D00.1a"},
		Exempt:   stmtctxExempt(),
		Gzip:     true,
		Generate: stmtctxGenerate,
		Exec:     stmtctxExec,
	})
}

var stmtctxCoverage = map[string]vec.Coverage{
	"script": {Min: 500, GoFuncs: []string{
		// errctx
		"Context.LevelMap", "Context.LevelForGroup", "Context.WithStrictErrGroupLevel", "Context.WithErrGroupLevel",
		"Context.WithErrGroupLevels", "Context.AppendWarning", "Context.AppendNote", "Context.HandleError",
		"Context.HandleErrorWithAlias", "NewContext", "NewContextWithLevels", "init",
		// warn.go
		"NewStaticWarnHandler", "NewStaticWarnHandlerWithHandler", "StaticWarnHandler.Reset",
		"StaticWarnHandler.AppendWarning", "StaticWarnHandler.AppendWarnings", "StaticWarnHandler.AppendNote",
		"StaticWarnHandler.AppendError", "StaticWarnHandler.WarningCount", "StaticWarnHandler.TruncateWarnings",
		"StaticWarnHandler.CopyWarnings", "StaticWarnHandler.GetWarnings", "StaticWarnHandler.appendWarningWithLevel",
		"StaticWarnHandler.SetWarnings", "StaticWarnHandler.NumErrorWarnings",
		"ignoreWarn.AppendWarning", "ignoreWarn.AppendNote", "ignoreWarn.WarningCount",
		"ignoreWarn.TruncateWarnings", "ignoreWarn.CopyWarnings",
		"funcWarnAppender.AppendWarning", "funcWarnAppender.AppendNote", "NewFuncWarnAppenderForTest",
		// plancache.go
		"PlanCacheTracker.WarnSkipPlanCache", "PlanCacheTracker.SetSkipPlanCache", "PlanCacheTracker.warnSkipPlanCache",
		"PlanCacheTracker.SetAlwaysWarnSkipCache", "PlanCacheTracker.SetCacheType", "PlanCacheTracker.SetForcePlanCache",
		"PlanCacheTracker.EnablePlanCache", "PlanCacheTracker.Save", "PlanCacheTracker.Restore", "PlanCacheTracker.UseCache",
		"PlanCacheTracker.PlanCacheUnqualified", "NewPlanCacheTracker", "RangeFallbackHandler.RecordRangeFallback",
		"NewRangeFallbackHandler",
		// stmtctx.go
		"ReferenceCount.TryIncrease", "ReferenceCount.Decrease", "ReferenceCount.TryFreeze", "ReferenceCount.UnFreeze",
		"ReservedRowIDAlloc.Reset", "ReservedRowIDAlloc.Consume", "ReservedRowIDAlloc.Exhausted",
		"stmtCtxMu.reset", "staleTSOProvider.reset", "stmtCache.reset",
		"NewStmtCtx", "NewStmtCtxWithTimeZone", "StatementContext.Reset",
		"StatementContext.ResetAlternativeLogicalPlanSignals", "StatementContext.MarkAlternativeLogicalPlanDecorrelatedApply",
		"StatementContext.MarkAlternativeLogicalPlanSameOrderIndexJoin", "StatementContext.MarkAlternativeLogicalPlanPreferCorrelate",
		"StatementContext.TimeZone", "StatementContext.SetTimeZone", "StatementContext.TypeCtx",
		"StatementContext.ErrCtx", "StatementContext.SetErrLevels", "StatementContext.ErrLevels", "StatementContext.ErrGroupLevel",
		"StatementContext.TypeFlags", "StatementContext.SetTypeFlags", "StatementContext.HandleTruncate",
		"StatementContext.HandleError", "StatementContext.HandleErrorWithAlias",
		"StatementContext.GetOrStoreStmtCache", "StatementContext.GetOrEvaluateStmtCache", "StatementContext.ResetInStmtCache",
		"StatementContext.ResetStmtCache", "StatementContext.GetBinaryPlan", "StatementContext.SetBinaryPlan",
		"StatementContext.SetUseChunkAlloc", "StatementContext.ClearUseChunkAlloc", "StatementContext.GetUseChunkAllocStatus",
		"StatementContext.GetEncodedPlan", "StatementContext.SetEncodedPlan", "StatementContext.GetPlanHint",
		"StatementContext.GetIndexForce", "StatementContext.SetPlanHint", "StatementContext.SetIndexForce",
		"StatementContext.SetHintWarning", "StatementContext.SetHintWarningFromError",
		"StatementContext.AddAffectedRows", "StatementContext.SetAffectedRows", "StatementContext.AffectedRows",
		"StatementContext.FoundRows", "StatementContext.AddFoundRows", "StatementContext.RecordRows",
		"StatementContext.AddRecordRows", "StatementContext.DeletedRows", "StatementContext.AddDeletedRows",
		"StatementContext.UpdatedRows", "StatementContext.AddUpdatedRows", "StatementContext.CopiedRows",
		"StatementContext.AddCopiedRows", "StatementContext.TouchedRows", "StatementContext.AddTouchedRows",
		"StatementContext.GetMessage", "StatementContext.SetMessage",
		"StatementContext.GetWarnings", "StatementContext.CopyWarnings", "StatementContext.TruncateWarnings",
		"StatementContext.WarningCount", "StatementContext.NumErrorWarnings", "StatementContext.SetWarnings",
		"StatementContext.AppendWarning", "StatementContext.AppendWarnings", "StatementContext.AppendNote",
		"StatementContext.AppendError", "StatementContext.GetExtraWarnings", "StatementContext.SetExtraWarnings",
		"StatementContext.AppendExtraWarning", "StatementContext.AppendExtraNote", "StatementContext.AppendExtraError",
		"StatementContext.resetMuForRetry", "StatementContext.ResetForRetry", "StatementContext.PushDownFlags",
		"StatementContext.InitFromPBFlagAndTz", "StatementContext.UseDynamicPartitionPrune",
		"StatementContext.SetStaleTSOProviderIfNotExist", "StatementContext.GetStaleTSO",
		"StatementContext.AddSetVarHintRestore", "StatementContext.TypeCtxOrDefault", "newErrCtx",
	}},
	"sqlwarn_marshal":   {Min: 50, GoFuncs: []string{"SQLWarn.MarshalJSON"}},
	"sqlwarn_unmarshal": {Min: 100, GoFuncs: []string{"SQLWarn.UnmarshalJSON"}},
	"push_down":         {Min: 100, GoFuncs: []string{"PushDownFlagsWithTypeFlagsAndErrLevels"}},
	"resolve":           {Min: 4, GoFuncs: []string{"ResolveErrLevel"}},
}

func stmtctxErr(e int) error {
	switch {
	case e == -1:
		return errors.New("plain error")
	case e == -2:
		return errors.New("")
	case e >= 0 && e < len(stmtctxCodes):
		return terror.ErrClass(stmtctxClasses[e]).NewStdErr(terror.ErrCode(stmtctxCodes[e]), errno.MySQLErrName[stmtctxCodes[e]])
	}
	panic(fmt.Sprintf("stmtctx: bad error %d", e))
}

func stmtctxErrOut(err error) any {
	if err == nil {
		return nil
	}
	code := 0
	if te, ok := errors.Cause(err).(*terror.Error); ok {
		code = int(te.Code())
	}
	return map[string]any{"code": code, "msg": err.Error()}
}

func stmtctxWarns(ws []contextutil.SQLWarn) [][]any {
	out := [][]any{}
	for _, w := range ws {
		out = append(out, []any{w.Level, stmtctxErrOut(w.Err)})
	}
	return out
}

func stmtctxLevels(raw []int) errctx.LevelMap {
	var l errctx.LevelMap
	for i := range l {
		if i < len(raw) {
			l[i] = errctx.Level(raw[i])
		}
	}
	return l
}

func stmtctxLevelsOut(l errctx.LevelMap) []int {
	out := make([]int, len(l))
	for i, v := range l {
		out[i] = int(v)
	}
	return out
}

// stmtctxEnv is the environment of a script.
type stmtctxEnv struct {
	h   *contextutil.StaticWarnHandler
	ec  errctx.Context
	pct contextutil.PlanCacheTracker
	rfh contextutil.RangeFallbackHandler
	sc  *stmtctx.StatementContext
	rc  stmtctx.ReferenceCount
	rr  stmtctx.ReservedRowIDAlloc
	// funcLog records what the funcWarnAppender of "ec_func" receives.
	funcLog [][]any
}

func newStmtctxEnv() *stmtctxEnv {
	e := &stmtctxEnv{h: contextutil.NewStaticWarnHandler(0), sc: stmtctx.NewStmtCtx()}
	e.ec = errctx.NewContext(e.h)
	e.pct = contextutil.NewPlanCacheTracker(e.h)
	e.rfh = contextutil.NewRangeFallbackHandler(&e.pct, e.h)
	return e
}

func stmtctxScDump(sc *stmtctx.StatementContext) map[string]any {
	ec, wc := sc.NumErrorWarnings()
	return map[string]any{
		"flags": uint16(sc.TypeFlags()), "levels": stmtctxLevelsOut(sc.ErrLevels()), "pd": sc.PushDownFlags(),
		"warns": stmtctxWarns(sc.GetWarnings()), "extra": stmtctxWarns(sc.GetExtraWarnings()),
		"wcount": sc.WarningCount(), "nerr": []any{ec, wc},
		"rows": []uint64{sc.AffectedRows(), sc.FoundRows(), sc.RecordRows(), sc.DeletedRows(),
			sc.UpdatedRows(), sc.CopiedRows(), sc.TouchedRows()},
		"msg": sc.GetMessage(), "tz": sc.TimeZone().String(),
	}
}

func stmtctxStep(e *stmtctxEnv, s []json.RawMessage) (out any) {
	var op string
	_ = json.Unmarshal(s[0], &op)
	iarg := func(i int) int {
		var v int
		_ = json.Unmarshal(s[i], &v)
		return v
	}
	sarg := func(i int) string {
		var v string
		_ = json.Unmarshal(s[i], &v)
		return v
	}
	larg := func(i int) []int {
		var v []int
		_ = json.Unmarshal(s[i], &v)
		return v
	}
	ec, wc := 0, 0
	_ = ec
	_ = wc
	switch op {
	// ---- StaticWarnHandler h
	case "h_warn":
		e.h.AppendWarning(stmtctxErr(iarg(1)))
	case "h_note":
		e.h.AppendNote(stmtctxErr(iarg(1)))
	case "h_error":
		e.h.AppendError(stmtctxErr(iarg(1)))
	case "h_warns":
		ws := make([]contextutil.SQLWarn, iarg(1))
		for i := range ws {
			ws[i] = contextutil.SQLWarn{Level: contextutil.WarnLevelWarning, Err: stmtctxErr(iarg(2))}
		}
		e.h.AppendWarnings(ws)
	case "h_bulk": // n warnings with one call each (the 65535 limit)
		for i := 0; i < iarg(1); i++ {
			e.h.AppendWarning(stmtctxErr(iarg(2)))
		}
		return e.h.WarningCount()
	case "h_truncate":
		return stmtctxWarns(e.h.TruncateWarnings(iarg(1)))
	case "h_copy":
		return stmtctxWarns(e.h.CopyWarnings(nil))
	case "h_set":
		ws := make([]contextutil.SQLWarn, iarg(1))
		for i := range ws {
			ws[i] = contextutil.SQLWarn{Level: contextutil.WarnLevelNote, Err: stmtctxErr(iarg(2))}
		}
		e.h.SetWarnings(ws)
	case "h_count":
		return e.h.WarningCount()
	case "h_nerr":
		a, b := e.h.NumErrorWarnings()
		return []any{a, b}
	case "h_reset":
		e.h.Reset()
	case "h_get":
		return stmtctxWarns(e.h.GetWarnings())
	case "h_clone": // NewStaticWarnHandlerWithHandler(h), then append to the copy
		h2 := contextutil.NewStaticWarnHandlerWithHandler(e.h)
		h2.AppendNote(stmtctxErr(-1))
		return []any{stmtctxWarns(h2.GetWarnings()), e.h.WarningCount()}
	case "h_clone_nil":
		h2 := contextutil.NewStaticWarnHandlerWithHandler(nil)
		return h2.WarningCount()
	case "ignore":
		w := contextutil.IgnoreWarn
		w.AppendWarning(stmtctxErr(iarg(1)))
		w.AppendNote(stmtctxErr(iarg(1)))
		return []any{w.WarningCount(), len(w.TruncateWarnings(0)), len(w.CopyWarnings(nil))}
	// ---- errctx.Context ec (on h)
	case "ec_new":
		e.ec = errctx.NewContextWithLevels(stmtctxLevels(larg(1)), e.h)
	case "ec_func":
		e.ec = errctx.NewContextWithLevels(stmtctxLevels(larg(1)), contextutil.NewFuncWarnAppenderForTest(func(level string, err error) {
			e.funcLog = append(e.funcLog, []any{level, stmtctxErrOut(err)})
		}))
	case "ec_flog":
		l := e.funcLog
		if l == nil {
			l = [][]any{}
		}
		return l
	case "ec_strict":
		e.ec = e.ec.WithStrictErrGroupLevel()
	case "ec_level":
		e.ec = e.ec.WithErrGroupLevel(errctx.ErrGroup(iarg(1)), errctx.Level(iarg(2)))
	case "ec_levels":
		e.ec = e.ec.WithErrGroupLevels(stmtctxLevels(larg(1)))
	case "ec_map":
		return stmtctxLevelsOut(e.ec.LevelMap())
	case "ec_for":
		return int(e.ec.LevelForGroup(errctx.ErrGroup(iarg(1))))
	case "ec_handle":
		return stmtctxErrOut(e.ec.HandleError(stmtctxErr(iarg(1))))
	case "ec_alias":
		return stmtctxErrOut(e.ec.HandleErrorWithAlias(stmtctxErr(iarg(1)), stmtctxErr(iarg(2)), stmtctxErr(iarg(3))))
	case "ec_warn":
		e.ec.AppendWarning(stmtctxErr(iarg(1)))
	case "ec_note":
		e.ec.AppendNote(stmtctxErr(iarg(1)))
	case "ec_nowarn":
		return stmtctxErrOut(errctx.StrictNoWarningContext.HandleError(stmtctxErr(iarg(1))))
	// ---- PlanCacheTracker pct / RangeFallbackHandler rfh (on h)
	case "pct_skip":
		e.pct.SetSkipPlanCache(sarg(1))
	case "pct_warn":
		e.pct.WarnSkipPlanCache(sarg(1))
	case "pct_type":
		e.pct.SetCacheType(contextutil.PlanCacheType(iarg(1)))
	case "pct_force":
		e.pct.SetForcePlanCache(iarg(1) != 0)
	case "pct_always":
		e.pct.SetAlwaysWarnSkipCache(iarg(1) != 0)
	case "pct_enable":
		e.pct.EnablePlanCache()
	case "pct_state":
		u, t, q, f, a := e.pct.Save()
		return []any{u, int(t), q, f, a, e.pct.UseCache(), e.pct.PlanCacheUnqualified()}
	case "pct_restore":
		e.pct.Restore(iarg(1) != 0, contextutil.PlanCacheType(iarg(2)), sarg(3), iarg(4) != 0, iarg(5) != 0)
	case "rfh":
		e.rfh.RecordRangeFallback(int64(iarg(1)))
	// ---- StatementContext sc
	case "sc_new":
		e.sc = stmtctx.NewStmtCtx()
	case "sc_new_tz":
		e.sc = stmtctx.NewStmtCtxWithTimeZone(time.FixedZone(sarg(1), iarg(2)))
	case "sc_reset":
		return e.sc.Reset()
	case "sc_tz":
		e.sc.SetTimeZone(time.FixedZone(sarg(1), iarg(2)))
	case "sc_flags":
		e.sc.SetTypeFlags(types.Flags(iarg(1)))
	case "sc_levels":
		e.sc.SetErrLevels(stmtctxLevels(larg(1)))
	case "sc_pb":
		e.sc.InitFromPBFlagAndTz(uint64(iarg(1)), time.FixedZone(sarg(2), iarg(3)))
	case "sc_kind": // bit mask of statement kinds
		m := iarg(1)
		e.sc.InInsertStmt, e.sc.InUpdateStmt, e.sc.InDeleteStmt = m&1 != 0, m&2 != 0, m&4 != 0
		e.sc.InSelectStmt, e.sc.InLoadDataStmt, e.sc.InRestrictedSQL = m&8 != 0, m&16 != 0, m&32 != 0
		e.sc.InShowWarning, e.sc.InHandleForeignKeyTrigger = m&64 != 0, m&128 != 0
		e.sc.UseDynamicPruneMode = m&256 != 0
	case "sc_dump":
		return stmtctxScDump(e.sc)
	case "sc_errctx":
		ec := e.sc.ErrCtx()
		tc := e.sc.TypeCtx()
		return []any{stmtctxLevelsOut(ec.LevelMap()), int(e.sc.ErrGroupLevel(errctx.ErrGroup(iarg(1)))),
			uint16(tc.Flags()), stmtctxTCFlags(e.sc.TypeCtxOrDefault()), e.sc.UseDynamicPartitionPrune()}
	case "sc_trunc":
		return stmtctxErrOut(e.sc.HandleTruncate(stmtctxErr(iarg(1))))
	case "sc_handle":
		return stmtctxErrOut(e.sc.HandleError(stmtctxErr(iarg(1))))
	case "sc_alias":
		return stmtctxErrOut(e.sc.HandleErrorWithAlias(stmtctxErr(iarg(1)), stmtctxErr(iarg(2)), stmtctxErr(iarg(3))))
	case "sc_tc_warn": // a warning through the type context (the statement context is its appender)
		tc := e.sc.TypeCtx()
		tc.AppendWarning(stmtctxErr(iarg(1)))
	case "sc_warn":
		e.sc.AppendWarning(stmtctxErr(iarg(1)))
	case "sc_note":
		e.sc.AppendNote(stmtctxErr(iarg(1)))
	case "sc_error":
		e.sc.AppendError(stmtctxErr(iarg(1)))
	case "sc_warns":
		e.sc.AppendWarnings([]contextutil.SQLWarn{{Level: contextutil.WarnLevelError, Err: stmtctxErr(iarg(1))}})
	case "sc_xwarn":
		e.sc.AppendExtraWarning(stmtctxErr(iarg(1)))
	case "sc_xnote":
		e.sc.AppendExtraNote(stmtctxErr(iarg(1)))
	case "sc_xerror":
		e.sc.AppendExtraError(stmtctxErr(iarg(1)))
	case "sc_xset":
		e.sc.SetExtraWarnings([]contextutil.SQLWarn{{Level: contextutil.WarnLevelNote, Err: stmtctxErr(iarg(1))}})
	case "sc_set":
		e.sc.SetWarnings([]contextutil.SQLWarn{{Level: contextutil.WarnLevelError, Err: stmtctxErr(iarg(1))}})
	case "sc_truncw":
		return stmtctxWarns(e.sc.TruncateWarnings(iarg(1)))
	case "sc_copyw":
		return stmtctxWarns(e.sc.CopyWarnings(nil))
	case "sc_rows":
		n := uint64(iarg(2))
		switch iarg(1) {
		case 0:
			e.sc.AddAffectedRows(n)
		case 1:
			e.sc.SetAffectedRows(n)
		case 2:
			e.sc.AddFoundRows(n)
		case 3:
			e.sc.AddRecordRows(n)
		case 4:
			e.sc.AddDeletedRows(n)
		case 5:
			e.sc.AddUpdatedRows(n)
		case 6:
			e.sc.AddCopiedRows(n)
		case 7:
			e.sc.AddTouchedRows(n)
		}
	case "sc_msg":
		e.sc.SetMessage(sarg(1))
	case "sc_hint":
		e.sc.SetHintWarning(sarg(1))
	case "sc_hint_err":
		e.sc.SetHintWarningFromError(stmtctxErr(iarg(1)))
	case "sc_varhint":
		e.sc.AddSetVarHintRestore(sarg(1), sarg(2))
		return maps.Clone(e.sc.SetVarHintRestore) // the map is recorded as it is now
	case "sc_plan":
		e.sc.SetBinaryPlan(sarg(1))
		e.sc.SetEncodedPlan(sarg(2))
		if iarg(3) != 0 {
			e.sc.SetPlanHint(sarg(1))
			e.sc.SetIndexForce()
		}
		if iarg(4) != 0 {
			e.sc.SetUseChunkAlloc()
		} else {
			e.sc.ClearUseChunkAlloc()
		}
		h, ok := e.sc.GetPlanHint()
		return []any{e.sc.GetBinaryPlan(), e.sc.GetEncodedPlan(), h, ok, e.sc.GetIndexForce(), e.sc.GetUseChunkAllocStatus()}
	case "sc_alt":
		switch iarg(1) {
		case 0:
			e.sc.ResetAlternativeLogicalPlanSignals()
		case 1:
			e.sc.MarkAlternativeLogicalPlanDecorrelatedApply()
		case 2:
			e.sc.MarkAlternativeLogicalPlanSameOrderIndexJoin()
		case 3:
			e.sc.MarkAlternativeLogicalPlanPreferCorrelate()
		}
		return []bool{e.sc.AlternativeLogicalPlanDecorrelatedApply, e.sc.AlternativeLogicalPlanSameOrderIndexJoin,
			e.sc.AlternativeLogicalPlanPreferCorrelate}
	case "sc_cache": // GetOrStoreStmtCache(key, value)
		return e.sc.GetOrStoreStmtCache(stmtctx.StmtCacheKey(iarg(1)), iarg(2))
	case "sc_cache_eval": // GetOrEvaluateStmtCache(key, f) with f returning value or an error
		calls := 0
		v, err := e.sc.GetOrEvaluateStmtCache(stmtctx.StmtCacheKey(iarg(1)), func() (any, error) {
			calls++
			if iarg(3) != 0 {
				return nil, stmtctxErr(iarg(3) - 1)
			}
			return iarg(2), nil
		})
		return []any{v, stmtctxErrOut(err), calls}
	case "sc_cache_del":
		e.sc.ResetInStmtCache(stmtctx.StmtCacheKey(iarg(1)))
	case "sc_cache_reset":
		e.sc.ResetStmtCache()
	case "sc_stale_set":
		v, fail := uint64(iarg(1)), iarg(2)
		e.sc.SetStaleTSOProviderIfNotExist(func() (uint64, error) {
			if fail != 0 {
				return 0, stmtctxErr(fail - 1)
			}
			return v, nil
		})
	case "sc_stale_get":
		v, err := e.sc.GetStaleTSO()
		return []any{v, stmtctxErrOut(err)}
	case "sc_retry":
		e.sc.ResetForRetry()
	case "sc_pct": // the embedded plan cache tracker and range fallback handler of sc
		e.sc.EnablePlanCache()
		e.sc.SetCacheType(contextutil.PlanCacheType(iarg(1)))
		e.sc.SetSkipPlanCache(sarg(2))
		e.sc.RecordRangeFallback(int64(iarg(3)))
		return []any{e.sc.UseCache(), e.sc.PlanCacheUnqualified()}
	// ---- ReferenceCount rc / ReservedRowIDAlloc rr
	case "rc":
		var r bool
		switch iarg(1) {
		case 0:
			r = e.rc.TryIncrease()
		case 1:
			e.rc.Decrease()
		case 2:
			r = e.rc.TryFreeze()
		case 3:
			e.rc.UnFreeze()
		}
		return []any{r, int32(e.rc)}
	case "rr_reset":
		e.rr.Reset(int64(iarg(1)), int64(iarg(2)))
		return e.rr.Exhausted()
	case "rr_consume":
		id, ok := e.rr.Consume()
		return []any{id, ok, e.rr.Exhausted()}
	default:
		panic(fmt.Sprintf("stmtctx: unknown step %q", op))
	}
	return nil
}

func stmtctxScript(steps [][]json.RawMessage) []any {
	e := newStmtctxEnv()
	out := []any{}
	for _, s := range steps {
		r, p := func() (r any, p any) {
			defer func() { p = recover() }()
			return stmtctxStep(e, s), nil
		}()
		if p != nil {
			out = append(out, map[string]any{"panic": fmt.Sprint(p)})
			break
		}
		out = append(out, r)
	}
	return out
}

func stmtctxExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "script":
		var steps [][]json.RawMessage
		if err := json.Unmarshal(args[0], &steps); err != nil {
			return nil, err
		}
		return stmtctxScript(steps), nil
	case "sqlwarn_marshal":
		var level string
		var e int
		_ = json.Unmarshal(args[0], &level)
		_ = json.Unmarshal(args[1], &e)
		w := contextutil.SQLWarn{Level: level, Err: stmtctxErr(e)}
		b, err := json.Marshal(&w)
		return string(b), err
	case "sqlwarn_unmarshal":
		var text string
		_ = json.Unmarshal(args[0], &text)
		var w contextutil.SQLWarn
		if err := w.UnmarshalJSON([]byte(text)); err != nil {
			return nil, err
		}
		return []any{w.Level, stmtctxErrOut(w.Err)}, nil
	case "push_down":
		var f int
		var l []int
		_ = json.Unmarshal(args[0], &f)
		_ = json.Unmarshal(args[1], &l)
		return stmtctx.PushDownFlagsWithTypeFlagsAndErrLevels(types.Flags(f), stmtctxLevels(l)), nil
	case "resolve":
		var a, b bool
		_ = json.Unmarshal(args[0], &a)
		_ = json.Unmarshal(args[1], &b)
		return int(errctx.ResolveErrLevel(a, b)), nil
	}
	return nil, fmt.Errorf("stmtctx: unknown op %q", op)
}

// st builds one step.
func st(op string, args ...any) []any { return append([]any{op}, args...) }

func stmtctxGenerate(g *vec.Gen, rng *rand.Rand) {
	script := func(src string, steps ...[]any) { g.Add(src, "script", steps) }
	ne := len(stmtctxCodes)
	// ErrOverflow (types) is ErrDataOutOfRange: index 4.
	const ovf, trunc, dup, unknown = 4, 0, 17, 18

	// Go test scenarios.
	script("TestContext",
		st("ec_func", []int{}), st("ec_alias", ovf, -1, -2), st("ec_level", 0, 1), st("ec_alias", ovf, -1, -2),
		st("ec_flog"), st("ec_map"), st("ec_for", 0), st("ec_for", 5), st("ec_strict"), st("ec_alias", ovf, -1, -2),
		st("ec_map"), st("ec_func", []int{0, 0, 0, 0, 0, 1}), st("ec_map"), st("ec_levels", []int{0, 0, 0, 0, 0, 2}),
		st("ec_map"), st("ec_levels", []int{}), st("ec_map"))
	script("TestIgnoreWarn", st("ignore", ovf), st("ignore", -1))
	script("TestStaticWarnHandler",
		st("h_warn", ovf), st("h_note", -1), st("h_error", dup), st("h_count"), st("h_nerr"), st("h_get"),
		st("h_copy"), st("h_truncate", 1), st("h_get"), st("h_truncate", 5), st("h_set", 2, unknown), st("h_get"),
		st("h_warns", 3, trunc), st("h_count"), st("h_reset"), st("h_count"), st("h_get"))
	script("TestCopyWarnHandler", st("h_warn", ovf), st("h_note", -1), st("h_clone"), st("h_clone_nil"))
	script("TestStatementContextPushDownFLags",
		st("sc_levels", []int{}), st("sc_kind", 1), st("sc_dump"), st("sc_kind", 2), st("sc_dump"), st("sc_kind", 4),
		st("sc_dump"), st("sc_kind", 8), st("sc_dump"), st("sc_kind", 0), st("sc_flags", int(types.DefaultStmtFlags|types.FlagIgnoreTruncateErr)),
		st("sc_dump"), st("sc_new"), st("sc_levels", []int{}), st("sc_flags", int(types.DefaultStmtFlags|types.FlagTruncateAsWarning)), st("sc_dump"),
		st("sc_new"), st("sc_levels", []int{}), st("sc_flags", int(types.DefaultStmtFlags|types.FlagIgnoreZeroInDateErr)), st("sc_dump"),
		st("sc_new"), st("sc_levels", []int{0, 0, 0, 0, 1}), st("sc_dump"), st("sc_new"), st("sc_levels", []int{}),
		st("sc_kind", 16), st("sc_dump"), st("sc_new"), st("sc_levels", []int{}), st("sc_kind", 8),
		st("sc_flags", int(types.DefaultStmtFlags|types.FlagTruncateAsWarning)), st("sc_dump"))
	script("TestNewStmtCtx", st("sc_dump"), st("sc_errctx", 4), st("sc_new_tz", "UTC+8", 8*3600), st("sc_dump"))
	script("TestSetStmtCtxTypeFlags", st("sc_flags", int(types.FlagAllowNegativeToUnsigned|types.FlagSkipASCIICheck)),
		st("sc_errctx", 0), st("sc_flags", int(types.FlagSkipASCIICheck|types.FlagSkipUTF8Check|types.FlagTruncateAsWarning)),
		st("sc_errctx", 0))
	script("TestResetStmtCtx", st("sc_new_tz", "UTC+8", 8*3600), st("sc_flags", int(types.FlagAllowNegativeToUnsigned|types.FlagSkipASCIICheck)),
		st("sc_warn", -1), st("sc_rows", 0, 3), st("sc_msg", "m"), st("sc_reset"), st("sc_dump"))
	script("TestErrCtx", st("sc_handle", 8), st("sc_errctx", 4), st("sc_levels", []int{0, 0, 0, 0, 0, 2}), st("sc_errctx", 5),
		st("sc_flags", int(types.DefaultStmtFlags|types.FlagTruncateAsWarning)), st("sc_handle", 8), st("sc_errctx", 0),
		st("sc_levels", []int{}), st("sc_errctx", 0), st("sc_dump"))
	script("TestReservedRowIDAlloc", st("rr_consume"), st("rr_reset", 12, 15), st("rr_consume"), st("rr_consume"),
		st("rr_consume"), st("rr_consume"))

	// SQLWarn JSON (TestSQLWarn, TestMarshalSQLWarn).
	levels := []string{"Error", "Warning", "Note", "", "warning", "<x>& "}
	for _, l := range levels {
		for e := -2; e < ne; e++ {
			g.Add("TestSQLWarn", "sqlwarn_marshal", l, e)
		}
	}
	unm := []string{
		`{"level":"Warning","err":{"class":20,"code":1292,"message":"m","rfccode":"types:1292"}}`,
		`{"level":"Note","msg":"plain"}`, `{"level":"Error"}`, `{}`, `null`, `{"LEVEL":"x","MSG":"y"}`,
		`{"level":"a","level":"b"}`, `{"level":1}`, `{"msg":2}`, `{"err":null,"msg":"z"}`, `{"err":"s"}`,
		`{"err":{"code":"x"}}`, `{"err":{"class":1}}`, `[1]`, `"s"`, `{"level":"x"} 1`, `{`, ``, `{"level":null}`,
		`{"err":{"rfccode":"","class":20,"code":5}}`, `{"Err":{"code":9},"msg":"both"}`,
	}
	for _, u := range unm {
		g.Add("TestSQLWarn", "sqlwarn_unmarshal", u)
	}
	for e := -2; e < ne; e++ {
		w := contextutil.SQLWarn{Level: levels[e&1], Err: stmtctxErr(e)}
		b, _ := json.Marshal(&w)
		g.Add("sys/roundtrip", "sqlwarn_unmarshal", string(b))
	}
	for i := 0; i < 80; i++ {
		b := []byte(unm[rng.IntN(len(unm))])
		if len(b) > 0 {
			const mut = "{}[]\":,0a "
			b[rng.IntN(len(b))] = mut[rng.IntN(len(mut))]
		}
		g.Add("rand/unmarshal", "sqlwarn_unmarshal", string(b))
	}

	// push-down flags and levels.
	for f := 0; f < 1024; f += 1 + rng.IntN(7) {
		l := []int{rng.IntN(3), rng.IntN(3), rng.IntN(3), rng.IntN(3), rng.IntN(3), rng.IntN(3), rng.IntN(3)}
		g.Add("rand/push_down", "push_down", f, l)
	}
	for _, a := range []bool{false, true} {
		for _, b := range []bool{false, true} {
			g.Add("sys/resolve", "resolve", a, b)
		}
	}

	// The 65535 warning limit.
	script("sys/limit", st("h_bulk", 65534, -1), st("h_warn", ovf), st("h_note", ovf), st("h_error", ovf), st("h_count"),
		st("h_warns", 3, -1), st("h_count"), st("h_nerr"), st("h_truncate", 65536), st("h_count"))
	script("sys/truncate_neg", st("h_warn", ovf), st("h_truncate", -1), st("h_truncate", -5))

	// Random scripts.
	ops := []func() []any{
		func() []any { return st("h_warn", rng.IntN(ne+2)-2) },
		func() []any { return st("h_note", rng.IntN(ne+2)-2) },
		func() []any { return st("h_error", rng.IntN(ne+2)-2) },
		func() []any { return st("h_warns", rng.IntN(4), rng.IntN(ne)) },
		func() []any { return st("h_truncate", rng.IntN(6)) },
		func() []any { return st("h_copy") },
		func() []any { return st("h_set", rng.IntN(3), rng.IntN(ne)) },
		func() []any { return st("h_nerr") },
		func() []any { return st("h_reset") },
		func() []any { return st("h_clone") },
		func() []any { return st("ec_new", randLevels(rng)) },
		func() []any { return st("ec_level", rng.IntN(7), rng.IntN(3)) },
		func() []any { return st("ec_levels", randLevels(rng)) },
		func() []any { return st("ec_strict") },
		func() []any { return st("ec_handle", rng.IntN(ne+2)-2) },
		func() []any { return st("ec_alias", rng.IntN(ne+2)-2, rng.IntN(ne+2)-2, rng.IntN(ne+2)-2) },
		func() []any { return st("ec_for", rng.IntN(7)) },
		func() []any { return st("ec_warn", rng.IntN(ne)) },
		func() []any { return st("ec_note", rng.IntN(ne)) },
		func() []any { return st("ec_nowarn", rng.IntN(ne+2)-2) },
		func() []any { return st("pct_type", rng.IntN(3)) },
		func() []any { return st("pct_enable") },
		func() []any { return st("pct_force", rng.IntN(2)) },
		func() []any { return st("pct_always", rng.IntN(2)) },
		func() []any { return st("pct_skip", fmt.Sprintf("r%d %%s", rng.IntN(9))) },
		func() []any { return st("pct_warn", fmt.Sprintf("w%d", rng.IntN(9))) },
		func() []any { return st("pct_state") },
		func() []any {
			return st("pct_restore", rng.IntN(2), rng.IntN(3), fmt.Sprintf("q%d", rng.IntN(5)), rng.IntN(2), rng.IntN(2))
		},
		func() []any { return st("rfh", rng.IntN(1<<20)) },
		func() []any { return st("sc_kind", rng.IntN(512)) },
		func() []any { return st("sc_flags", rng.IntN(1024)) },
		func() []any { return st("sc_levels", randLevels(rng)) },
		func() []any { return st("sc_pb", rng.IntN(4096), "Z", (rng.IntN(25)-12)*3600) },
		func() []any { return st("sc_tz", "T", (rng.IntN(25)-12)*1800) },
		func() []any { return st("sc_trunc", rng.IntN(ne+2)-2) },
		func() []any { return st("sc_handle", rng.IntN(ne+2)-2) },
		func() []any { return st("sc_alias", rng.IntN(ne+2)-2, rng.IntN(ne+2)-2, rng.IntN(ne+2)-2) },
		func() []any { return st("sc_tc_warn", rng.IntN(ne)) },
		func() []any {
			return st([]string{"sc_warn", "sc_note", "sc_error", "sc_warns"}[rng.IntN(4)], rng.IntN(ne+2)-2)
		},
		func() []any {
			return st([]string{"sc_xwarn", "sc_xnote", "sc_xerror", "sc_xset"}[rng.IntN(4)], rng.IntN(ne+2)-2)
		},
		func() []any { return st("sc_set", rng.IntN(ne)) },
		func() []any { return st("sc_truncw", rng.IntN(5)) },
		func() []any { return st("sc_copyw") },
		func() []any { return st("sc_rows", rng.IntN(8), rng.IntN(100)) },
		func() []any { return st("sc_msg", fmt.Sprintf("m%d", rng.IntN(9))) },
		func() []any { return st("sc_hint", fmt.Sprintf("hint %d %%d", rng.IntN(9))) },
		func() []any { return st("sc_hint_err", rng.IntN(ne+2)-2) },
		func() []any { return st("sc_varhint", fmt.Sprintf("v%d", rng.IntN(4)), fmt.Sprintf("%d", rng.IntN(9))) },
		func() []any {
			return st("sc_plan", fmt.Sprintf("b%d", rng.IntN(3)), fmt.Sprintf("e%d", rng.IntN(3)), rng.IntN(2), rng.IntN(2))
		},
		func() []any { return st("sc_alt", rng.IntN(4)) },
		func() []any { return st("sc_cache", rng.IntN(3), rng.IntN(100)) },
		func() []any { return st("sc_cache_eval", rng.IntN(3), rng.IntN(100), rng.IntN(3)) },
		func() []any { return st("sc_cache_del", rng.IntN(3)) },
		func() []any { return st("sc_cache_reset") },
		func() []any { return st("sc_stale_set", rng.IntN(1000), rng.IntN(3)) },
		func() []any { return st("sc_stale_get") },
		func() []any { return st("sc_retry") },
		func() []any { return st("sc_reset") },
		func() []any { return st("sc_pct", rng.IntN(3), fmt.Sprintf("p%d", rng.IntN(5)), rng.IntN(1000)) },
		func() []any { return st("sc_errctx", rng.IntN(7)) },
		func() []any { return st("sc_dump") },
		func() []any { return st("rc", rng.IntN(4)) },
		func() []any { return st("rr_reset", rng.IntN(20), rng.IntN(20)) },
		func() []any { return st("rr_consume") },
	}
	for i := 0; i < 600; i++ {
		n := 5 + rng.IntN(40)
		steps := make([][]any, 0, n+2)
		for j := 0; j < n; j++ {
			steps = append(steps, ops[rng.IntN(len(ops))]())
		}
		steps = append(steps, st("h_get"), st("sc_dump"), st("pct_state"))
		script("rand/script", steps...)
	}
}

func randLevels(rng *rand.Rand) []int {
	l := make([]int, 7)
	for i := range l {
		l[i] = rng.IntN(3)
	}
	return l
}

func stmtctxTCFlags(tc types.Context) uint16 { return uint16(tc.Flags()) }
