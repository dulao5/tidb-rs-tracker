package domains

// D05.2 PR D: the methods of SessionVars, Concurrency, UserVars, PlanCacheParamList and the
// small session types (tidb-rs specs/D05.2-sessionvars/plan.md "PR D").
//   - methods: on a fresh session (freshVars, as op "set"), a list of SetSystemVar settings,
//     then a list of method calls (setters, allocators, prepared statements, isolation,
//     txn read ts, plan replayer keys, MDL tables); each call's result is recorded, then
//     every getter of the session.
//   - uservars: a script over one UserVars (set / unset / set type / get / get type /
//     clone, with later writes to the original that the clone must not see).
//   - paramlist: a script over one PlanCacheParamList (append / reset / set non-prep /
//     get / all / string); an out-of-range get panics like Go.
//   - svmisc: the free functions and small types (IsSecureTransport, RuntimeFilterType /
//     RuntimeFilterMode String, ToTiPBTiFlashPreAggMode, PrepareDedupCacheKey,
//     ReadConsistencyLevel.IsWeak, planCacheStmtKey.Hash, RewritePhaseInfo.Reset,
//     SetEnableAdaptiveReplicaRead / IsAdaptiveReplicaReadEnabled).
// Datums are [kind, value] in (int64, uint64, float64 bits, string+collation, null) and
// {k, s (hex of ToString), coll} out; field types [tp, flag, flen, dec, cs, co].

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/replayer"
)

func svdDatumIn(p []json.RawMessage) types.Datum {
	var d types.Datum
	switch arg[string](p, 0) {
	case "null":
		d.SetNull()
	case "i":
		d.SetInt64(arg[int64](p, 1))
	case "u":
		d.SetUint64(arg[uint64](p, 1))
	case "f":
		d.SetFloat64(math.Float64frombits(arg[uint64](p, 1)))
	case "s":
		d.SetString(string(arg[vec.GoString](p, 1)), arg[string](p, 2))
	case "b":
		b, err := hex.DecodeString(arg[string](p, 1))
		if err != nil {
			panic(err)
		}
		d.SetBytes(b)
	default:
		panic("bad datum")
	}
	return d
}

func svdDatumOut(d types.Datum) map[string]any {
	s, err := d.ToString()
	o := map[string]any{"k": d.Kind(), "s": hex.EncodeToString([]byte(s)), "coll": d.Collation()}
	if err != nil {
		o["err"] = err.Error()
	}
	return o
}

func svdFtIn(p []json.RawMessage) *types.FieldType {
	return types.NewFieldTypeBuilder().SetType(arg[byte](p, 0)).SetFlag(arg[uint](p, 1)).
		SetFlen(arg[int](p, 2)).SetDecimal(arg[int](p, 3)).SetCharset(arg[string](p, 4)).
		SetCollate(arg[string](p, 5)).BuildP()
}

func svdFtOut(ft *types.FieldType) []any {
	return []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), ft.GetDecimal(), ft.GetCharset(), ft.GetCollate()}
}

func f64s(f float64) string { return strconv.FormatUint(math.Float64bits(f), 10) }

// svdTxnReadTS reads the unexported fields of a TxnReadTS (readTS, used).
func svdTxnReadTS(t *variable.TxnReadTS) [2]any {
	type txnReadTS struct {
		readTS uint64
		used   bool
	}
	if t == nil {
		return [2]any{nil, nil}
	}
	r := (*txnReadTS)(unsafe.Pointer(t))
	return [2]any{r.readTS, r.used}
}

// svdUserVarsDump lists the user variables (name -> datum), sorted.
func svdUserVarsDump(uv *variable.UserVars) []any {
	var names []string
	for _, n := range []string{"a", "b", "cd", "A", "Cd"} {
		if _, ok := uv.GetUserVarVal(n); ok {
			names = append(names, n)
		}
	}
	var out []any
	for _, n := range names {
		d, _ := uv.GetUserVarVal(n)
		out = append(out, []any{n, svdDatumOut(d)})
	}
	return out
}

// svdMDLLen is the number of entries of TxnCtx.relatedTableForMDL (-1: nil), read without
// GetRelatedTableForMDL (which would allocate the map).
func svdMDLLen(sv *variable.SessionVars) int {
	f := reflect.ValueOf(sv.TxnCtx).Elem().FieldByName("relatedTableForMDL")
	if f.IsNil() {
		return -1
	}
	m := (*sync.Map)(unsafe.Pointer(f.Pointer()))
	n := 0
	m.Range(func(_, _ any) bool { n++; return true })
	return n
}

// svdGetters records every getter of the session (the mutating IsPessimisticReadConsistency
// last).
func svdGetters(sv *variable.SessionVars) map[string]any {
	engines := []string{}
	for e := range sv.GetIsolationReadEngines() {
		engines = append(engines, e.Name())
	}
	sort.Strings(engines)
	fix := []string{}
	for k, v := range sv.GetOptimizerFixControlMap() {
		fix = append(fix, fmt.Sprintf("%d:%s", k, v))
	}
	sort.Strings(fix)
	rfTypes := []string{}
	for _, t := range sv.GetRuntimeFilterTypes() {
		rfTypes = append(rfTypes, t.String())
	}
	tmp := &model.TableInfo{TempTableType: model.TempTableLocal}
	cs, coll := sv.GetCharsetInfo()
	m := map[string]any{
		"IndexLookupConcurrency":              sv.IndexLookupConcurrency(),
		"IndexLookupJoinConcurrency":          sv.IndexLookupJoinConcurrency(),
		"DistSQLScanConcurrency":              sv.DistSQLScanConcurrency(),
		"AnalyzeDistSQLScanConcurrency":       sv.AnalyzeDistSQLScanConcurrency(),
		"HashJoinConcurrency":                 sv.HashJoinConcurrency(),
		"ProjectionConcurrency":               sv.ProjectionConcurrency(),
		"HashAggPartialConcurrency":           sv.HashAggPartialConcurrency(),
		"HashAggFinalConcurrency":             sv.HashAggFinalConcurrency(),
		"WindowConcurrency":                   sv.WindowConcurrency(),
		"MergeJoinConcurrency":                sv.MergeJoinConcurrency(),
		"StreamAggConcurrency":                sv.StreamAggConcurrency(),
		"IndexMergeIntersectionConcurrency":   sv.IndexMergeIntersectionConcurrency(),
		"IndexSerialScanConcurrency":          sv.IndexSerialScanConcurrency(),
		"UnionConcurrency":                    sv.UnionConcurrency(),
		"IsPlanReplayerCaptureEnabled":        sv.IsPlanReplayerCaptureEnabled(),
		"IsMPPAllowed":                        sv.IsMPPAllowed(),
		"IsTiFlashCopBanned":                  sv.IsTiFlashCopBanned(),
		"IsMPPEnforced":                       sv.IsMPPEnforced(),
		"ChooseMppExchangeCompressionMode":    int(sv.ChooseMppExchangeCompressionMode()),
		"IsPartialOrderedIndexForTopNEnabled": sv.IsPartialOrderedIndexForTopNEnabled(),
		"CheckAndGetTxnScope":                 sv.CheckAndGetTxnScope(),
		"IsDynamicPartitionPruneEnabled":      sv.IsDynamicPartitionPruneEnabled(),
		"IsRowLevelChecksumEnabled":           sv.IsRowLevelChecksumEnabled(),
		"GetAllowPreferRangeScan":             sv.GetAllowPreferRangeScan(),
		"GetEnableIndexMerge":                 sv.GetEnableIndexMerge(),
		"GetEnablePseudoForOutdatedStats":     sv.GetEnablePseudoForOutdatedStats(),
		"IsReplicaReadClosestAdaptive":        sv.IsReplicaReadClosestAdaptive(),
		"GetSplitRegionTimeout":               int64(sv.GetSplitRegionTimeout()),
		"GetIsolationReadEngines":             engines,
		"GetCharsetInfo":                      []string{cs, coll},
		"IsolationLevelForNewTxn":             sv.IsolationLevelForNewTxn(),
		"GetReadableTxnMode":                  sv.GetReadableTxnMode(),
		"GetPrevStmtDigest":                   sv.GetPrevStmtDigest(),
		"GetDivPrecisionIncrement":            sv.GetDivPrecisionIncrement(),
		"GetCPUFactor":                        f64s(sv.GetCPUFactor()),
		"GetCopCPUFactor":                     f64s(sv.GetCopCPUFactor()),
		"GetMemoryFactor":                     f64s(sv.GetMemoryFactor()),
		"GetDiskFactor":                       f64s(sv.GetDiskFactor()),
		"GetConcurrencyFactor":                f64s(sv.GetConcurrencyFactor()),
		"GetNetworkFactor":                    []string{f64s(sv.GetNetworkFactor(nil)), f64s(sv.GetNetworkFactor(tmp)), f64s(sv.GetNetworkFactor(&model.TableInfo{}))},
		"GetScanFactor":                       []string{f64s(sv.GetScanFactor(nil)), f64s(sv.GetScanFactor(tmp)), f64s(sv.GetScanFactor(&model.TableInfo{}))},
		"GetDescScanFactor":                   []string{f64s(sv.GetDescScanFactor(nil)), f64s(sv.GetDescScanFactor(tmp)), f64s(sv.GetDescScanFactor(&model.TableInfo{}))},
		"GetSeekFactor":                       []string{f64s(sv.GetSeekFactor(nil)), f64s(sv.GetSeekFactor(tmp)), f64s(sv.GetSeekFactor(&model.TableInfo{}))},
		"EnableEvalTopNEstimationForStrMatch": sv.EnableEvalTopNEstimationForStrMatch(),
		"GetStrMatchDefaultSelectivity":       f64s(sv.GetStrMatchDefaultSelectivity()),
		"GetNegateStrMatchDefaultSelectivity": f64s(sv.GetNegateStrMatchDefaultSelectivity()),
		"EnableForceInlineCTE":                sv.EnableForceInlineCTE(),
		"IsRuntimeFilterEnabled":              sv.IsRuntimeFilterEnabled(),
		"GetRuntimeFilterTypes":               rfTypes,
		"GetRuntimeFilterMode":                sv.GetRuntimeFilterMode().String(),
		"GetTiKVClientReadTimeout":            sv.GetTiKVClientReadTimeout(),
		"GetOptObjective":                     sv.GetOptObjective(),
		"UseLowResolutionTSO":                 sv.UseLowResolutionTSO(),
		"GetOptimizerFixControlMap":           fix,
		"GetForUpdateTS":                      sv.TxnCtx.GetForUpdateTS(),
		"TxnReadTS":                           svdTxnReadTS(sv.TxnReadTS),
		"SnapshotInfoschema":                  sv.SnapshotInfoschema != nil,
		"ResourceGroupName":                   sv.ResourceGroupName,
		"PreparedStmtCount":                   atomic.LoadInt64(&variable.PreparedStmtCount),
		"PreparedStmts":                       len(sv.PreparedStmts),
		"TxnCtx.Isolation":                    sv.TxnCtx.Isolation,
		"MDL":                                 svdMDLLen(sv),
		"UserVars":                            svdUserVarsDump(sv.UserVars),
		"GetSessionVars":                      sv.GetSessionVars() == sv,
		"Location":                            sv.Location().String(),
	}
	m["IsPessimisticReadConsistency"] = sv.IsPessimisticReadConsistency()
	m["TxnCtx.Isolation.after"] = sv.TxnCtx.Isolation
	return m
}

// svdCall runs one method call of op "methods" and returns its result (nil for setters).
func svdCall(sv *variable.SessionVars, c []json.RawMessage) any {
	name := arg[string](c, 0)
	i := func(k int) int { return arg[int](c, k) }
	b := func(k int) bool { return arg[bool](c, k) }
	s := func(k int) string { return arg[string](c, k) }
	key := func() replayer.PlanReplayerTaskKey {
		return replayer.PlanReplayerTaskKey{SQLDigest: s(1), PlanDigest: s(2)}
	}
	switch name {
	case "SetIndexLookupConcurrency":
		sv.SetIndexLookupConcurrency(i(1))
	case "SetIndexLookupJoinConcurrency":
		sv.SetIndexLookupJoinConcurrency(i(1))
	case "SetDistSQLScanConcurrency":
		sv.SetDistSQLScanConcurrency(i(1))
	case "SetAnalyzeDistSQLScanConcurrency":
		sv.SetAnalyzeDistSQLScanConcurrency(i(1))
	case "SetHashJoinConcurrency":
		sv.SetHashJoinConcurrency(i(1))
	case "SetProjectionConcurrency":
		sv.SetProjectionConcurrency(i(1))
	case "SetHashAggPartialConcurrency":
		sv.SetHashAggPartialConcurrency(i(1))
	case "SetHashAggFinalConcurrency":
		sv.SetHashAggFinalConcurrency(i(1))
	case "SetWindowConcurrency":
		sv.SetWindowConcurrency(i(1))
	case "SetMergeJoinConcurrency":
		sv.SetMergeJoinConcurrency(i(1))
	case "SetStreamAggConcurrency":
		sv.SetStreamAggConcurrency(i(1))
	case "SetIndexMergeIntersectionConcurrency":
		sv.SetIndexMergeIntersectionConcurrency(i(1))
	case "SetIndexSerialScanConcurrency":
		sv.SetIndexSerialScanConcurrency(i(1))
	case "SetAllowInSubqToJoinAndAgg":
		sv.SetAllowInSubqToJoinAndAgg(b(1))
	case "SetAllowPreferRangeScan":
		sv.SetAllowPreferRangeScan(b(1))
	case "SetEnableCascadesPlanner":
		sv.SetEnableCascadesPlanner(b(1))
	case "SetEnableIndexMerge":
		sv.SetEnableIndexMerge(b(1))
	case "SetEnablePseudoForOutdatedStats":
		sv.SetEnablePseudoForOutdatedStats(b(1))
	case "SetPrevStmtDigest":
		sv.SetPrevStmtDigest(s(1))
	case "SetResourceGroupName":
		sv.SetResourceGroupName(s(1))
	case "SetNextPreparedStmtID":
		sv.SetNextPreparedStmtID(arg[uint32](c, 1))
	case "GetNextPreparedStmtID":
		return sv.GetNextPreparedStmtID()
	case "AllocNewPlanID":
		return sv.AllocNewPlanID()
	case "AllocPlanColumnID":
		return sv.AllocPlanColumnID()
	case "SetPlanIDs":
		sv.PlanID.Store(int32(i(1)))
		sv.PlanColumnID.Store(int64(i(2)))
	case "SetStringUserVar":
		sv.SetStringUserVar(s(1), string(arg[vec.GoString](c, 2)), s(3))
		d, ok := sv.GetUserVarVal(strings.ToLower(s(1)))
		if !ok {
			return nil
		}
		return svdDatumOut(d)
	case "IsIsolation":
		return []any{sv.IsIsolation(s(1)), sv.TxnCtx.Isolation}
	case "SetTxnIsolationLevelOneShotStateForNextTxn":
		sv.SetTxnIsolationLevelOneShotStateForNextTxn()
	case "SetInTxn":
		sv.SetInTxn(b(1))
	case "TxnCtx":
		// [TxnCtx, start_ts, is_pessimistic, isolation]
		sv.TxnCtx.StartTS = arg[uint64](c, 1)
		sv.TxnCtx.IsPessimistic = b(2)
		sv.TxnCtx.Isolation = s(3)
	case "SetForUpdateTS":
		sv.TxnCtx.SetForUpdateTS(arg[uint64](c, 1))
	case "TxnCtx.Cleanup":
		sv.TxnCtx.IsStaleness = true
		sv.TxnCtx.EnableMDL = true
		sv.TxnCtx.Cleanup()
		return []any{sv.TxnCtx.IsStaleness, sv.TxnCtx.EnableMDL, svdMDLLen(sv)}
	case "SetTxnReadTS":
		sv.TxnReadTS.SetTxnReadTS(arg[uint64](c, 1))
	case "UseTxnReadTS":
		return sv.TxnReadTS.UseTxnReadTS()
	case "SetSnapshotInfoschema":
		sv.SnapshotInfoschema = struct{}{}
	case "CleanupTxnReadTSIfUsed":
		sv.CleanupTxnReadTSIfUsed()
	case "AddPlanReplayerFinishedTaskKey":
		if sv.PlanReplayerFinishedTaskKey == nil {
			// Go assigns into a nil map (panic); callers check first (tidb-rs spec §4).
			sv.CheckPlanReplayerFinishedTaskKey(key())
		}
		sv.AddPlanReplayerFinishedTaskKey(key())
		return len(sv.PlanReplayerFinishedTaskKey)
	case "CheckPlanReplayerFinishedTaskKey":
		return sv.CheckPlanReplayerFinishedTaskKey(key())
	case "MDL":
		// [MDL, table id, version]: store into GetRelatedTableForMDL, read back the count.
		sv.GetRelatedTableForMDL().Store(arg[int64](c, 1), arg[int64](c, 2))
		return svdMDLLen(sv)
	case "ClearRelatedTableForMDL":
		sv.ClearRelatedTableForMDL()
		return svdMDLLen(sv)
	case "AddPreparedStmt":
		id := arg[uint32](c, 1)
		return errStr(sv.AddPreparedStmt(id, id))
	case "RemovePreparedStmt":
		sv.RemovePreparedStmt(arg[uint32](c, 1))
	case "WithdrawAllPreparedStmt":
		sv.WithdrawAllPreparedStmt()
	case "NamePreparedStmt":
		sv.PreparedStmtNameToID[s(1)] = arg[uint32](c, 2)
	case "GetPreparedStmtByID":
		v, err := sv.GetPreparedStmtByID(arg[uint32](c, 1))
		return []any{v, errStr(err)}
	case "GetPreparedStmtByName":
		v, err := sv.GetPreparedStmtByName(s(1))
		return []any{v, errStr(err)}
	case "SetMaxPreparedStmtCount":
		variable.MaxPreparedStmtCountValue.Store(arg[int64](c, 1))
	case "SetPreparedStmtCount":
		atomic.StoreInt64(&variable.PreparedStmtCount, arg[int64](c, 1))
	case "SetEnableAdaptiveReplicaRead":
		return variable.SetEnableAdaptiveReplicaRead(b(1))
	case "RegisterScalarSubQ":
		sv.RegisterScalarSubQ(i(1))
		return len(sv.MapScalarSubQ)
	case "Field":
		// [Field, name, value]: scalar fields with no SESSION-scope variable.
		switch s(1) {
		case "InRestrictedSQL":
			sv.InRestrictedSQL = b(2)
		case "ConnectionID":
			sv.ConnectionID = arg[uint64](c, 2)
		case "RowEncoder.Enable":
			sv.RowEncoder.Enable = b(2)
		case "EnableRowLevelChecksum":
			sv.EnableRowLevelChecksum = b(2)
		case "TiKVClientReadTimeout":
			sv.TiKVClientReadTimeout = arg[uint64](c, 2)
		default:
			panic("bad field " + s(1))
		}
	default:
		panic("bad call " + name)
	}
	return nil
}

func svdExec(op string, args []json.RawMessage) (any, error, bool) {
	switch op {
	case "methods":
		resetSysvarAtomics()
		atomic.StoreInt64(&variable.PreparedStmtCount, 0)
		variable.MaxPreparedStmtCountValue.Store(variable.DefMaxPreparedStmtCount)
		variable.SetEnableAdaptiveReplicaRead(true)
		vars := freshVars(svCtx{})
		var setErrs []any
		for _, p := range rawList(arg[[]json.RawMessage](args, 0)) {
			setErrs = append(setErrs, errStr(vars.SetSystemVar(arg[string](p, 0), arg[string](p, 1))))
		}
		var calls []any
		for _, c := range rawList(arg[[]json.RawMessage](args, 1)) {
			calls = append(calls, svdCall(vars, c))
		}
		out := map[string]any{"set": setErrs, "calls": calls, "get": svdGetters(vars)}
		atomic.StoreInt64(&variable.PreparedStmtCount, 0)
		variable.MaxPreparedStmtCountValue.Store(variable.DefMaxPreparedStmtCount)
		variable.SetEnableAdaptiveReplicaRead(true)
		return out, nil, true
	case "uservars":
		uv := variable.NewUserVars()
		var clone variable.UserVarsReader
		var res []any
		for _, c := range rawList(arg[[]json.RawMessage](args, 0)) {
			r := uv
			readers := []variable.UserVarsReader{uv}
			if clone != nil {
				readers = append(readers, clone)
			}
			switch arg[string](c, 0) {
			case "set":
				r.SetUserVarVal(arg[string](c, 1), svdDatumIn(arg[[]json.RawMessage](c, 2)))
				res = append(res, nil)
			case "unset":
				r.UnsetUserVar(arg[string](c, 1))
				res = append(res, nil)
			case "settype":
				r.SetUserVarType(arg[string](c, 1), svdFtIn(arg[[]json.RawMessage](c, 2)))
				res = append(res, nil)
			case "clone":
				clone = uv.Clone()
				res = append(res, nil)
			case "get":
				var outs []any
				for _, rd := range readers {
					d, ok := rd.GetUserVarVal(arg[string](c, 1))
					if !ok {
						outs = append(outs, nil)
					} else {
						outs = append(outs, svdDatumOut(d))
					}
				}
				res = append(res, outs)
			case "gettype":
				var outs []any
				for _, rd := range readers {
					ft, ok := rd.GetUserVarType(arg[string](c, 1))
					if !ok {
						outs = append(outs, nil)
					} else {
						outs = append(outs, svdFtOut(ft))
					}
				}
				res = append(res, outs)
			default:
				panic("bad uservars op")
			}
		}
		return res, nil, true
	case "paramlist":
		p := variable.NewPlanCacheParamList()
		var res []any
		for _, c := range rawList(arg[[]json.RawMessage](args, 0)) {
			switch arg[string](c, 0) {
			case "append":
				var ds []types.Datum
				for _, q := range rawList(arg[[]json.RawMessage](c, 1)) {
					ds = append(ds, svdDatumIn(q))
				}
				p.Append(ds...)
				res = append(res, nil)
			case "reset":
				p.Reset()
				res = append(res, nil)
			case "nonprep":
				p.SetForNonPrepCache(arg[bool](c, 1))
				res = append(res, nil)
			case "get":
				res = append(res, svdDatumOut(p.GetParamValue(arg[int](c, 1))))
			case "all":
				var outs []any
				for _, d := range p.AllParamValues() {
					outs = append(outs, svdDatumOut(d))
				}
				res = append(res, outs)
			case "string":
				res = append(res, vec.GoString(p.String()))
			default:
				panic("bad paramlist op")
			}
		}
		return res, nil, true
	case "svmisc":
		c := args
		switch arg[string](c, 0) {
		case "secure":
			ci := &variable.ConnectionInfo{ConnectionType: arg[string](c, 1)}
			return ci.IsSecureTransport(), nil, true
		case "rftype":
			return variable.RuntimeFilterType(arg[int64](c, 1)).String(), nil, true
		case "rfmode":
			return variable.RuntimeFilterMode(arg[int64](c, 1)).String(), nil, true
		case "preagg":
			m, ok := variable.ToTiPBTiFlashPreAggMode(arg[string](c, 1))
			return []any{int32(m), ok}, nil, true
		case "dedup":
			k := variable.PrepareDedupCacheKey(string(arg[vec.GoString](c, 1)), arg[string](c, 2), arg[string](c, 3),
				arg[string](c, 4), mysql.SQLMode(arg[int64](c, 5)))
			return hex.EncodeToString([]byte(k)), nil, true
		case "weak":
			return variable.ReadConsistencyLevel(arg[string](c, 1)).IsWeak(), nil, true
		case "rewrite":
			r := variable.RewritePhaseInfo{DurationRewrite: 1, DurationPreprocessSubQuery: 2, PreprocessSubQueries: 3}
			r.Reset()
			return []any{r.DurationRewrite, r.DurationPreprocessSubQuery, r.PreprocessSubQueries}, nil, true
		case "adaptive":
			defer variable.SetEnableAdaptiveReplicaRead(true)
			var outs []any
			for _, x := range arg[[]bool](c, 1) {
				outs = append(outs, variable.SetEnableAdaptiveReplicaRead(x), variable.IsAdaptiveReplicaReadEnabled())
			}
			return outs, nil, true
		}
		panic("bad svmisc op")
	}
	return nil, nil, false
}

// svdGenerate appends the PR D records (after every earlier record).
func svdGenerate(g *vec.Gen, rng *rand.Rand) {
	r := rand.New(rand.NewPCG(rng.Uint64(), 52))
	concSetters := []string{"SetIndexLookupConcurrency", "SetIndexLookupJoinConcurrency", "SetDistSQLScanConcurrency",
		"SetAnalyzeDistSQLScanConcurrency", "SetHashJoinConcurrency", "SetProjectionConcurrency",
		"SetHashAggPartialConcurrency", "SetHashAggFinalConcurrency", "SetWindowConcurrency",
		"SetMergeJoinConcurrency", "SetStreamAggConcurrency", "SetIndexMergeIntersectionConcurrency",
		"SetIndexSerialScanConcurrency"}
	boolSetters := []string{"SetAllowInSubqToJoinAndAgg", "SetAllowPreferRangeScan", "SetEnableCascadesPlanner",
		"SetEnableIndexMerge", "SetEnablePseudoForOutdatedStats"}
	settings := [][2][]string{
		{{variable.TiDBExecutorConcurrency}, {"1", "5", "16", "256"}},
		{{variable.TiDBHashJoinConcurrency}, {"-1", "1", "8"}},
		{{variable.TiDBIndexLookupConcurrency}, {"-1", "3"}},
		{{variable.TiDBDistSQLScanConcurrency}, {"1", "15", "64"}},
		{{variable.TiDBOptCPUFactor}, {"0", "3.5", "100"}},
		{{variable.TiDBOptNetworkFactor}, {"0", "1.5"}},
		{{variable.TiDBOptScanFactor}, {"2.25"}},
		{{variable.TiDBOptDescScanFactor}, {"4"}},
		{{variable.TiDBOptSeekFactor}, {"7"}},
		{{variable.TiDBOptMemoryFactor}, {"0.001"}},
		{{variable.TiDBOptDiskFactor}, {"1.25"}},
		{{variable.TiDBOptConcurrencyFactor}, {"3"}},
		{{variable.TiDBOptCopCPUFactor}, {"2"}},
		{{variable.TiDBDefaultStrMatchSelectivity}, {"0", "0.8", "0.2", "1"}},
		{{variable.TiDBAllowMPPExecution}, {"ON", "OFF"}},
		{{variable.TiDBEnforceMPPExecution}, {"ON", "OFF"}},
		{{variable.TiDBAllowTiFlashCop}, {"ON", "OFF"}},
		{{variable.MppExchangeCompressionMode}, {"NONE", "FAST", "HIGH_COMPRESSION", "UNSPECIFIED"}},
		{{variable.TiDBOptPartialOrderedIndexForTopN}, {"COST", "DISABLE"}},
		{{variable.TiDBPartitionPruneMode}, {"static", "dynamic"}},
		{{variable.TiDBReplicaRead}, {"leader", "closest-adaptive", "follower"}},
		{{variable.TiDBWaitSplitRegionTimeout}, {"1", "300"}},
		{{variable.TiDBIsolationReadEngines}, {"tikv", "tikv,tiflash", "tidb,tikv"}},
		{{variable.CharacterSetConnection}, {"latin1", "utf8mb4"}},
		{{variable.CollationConnection}, {"utf8mb4_general_ci", "latin1_bin"}},
		{{variable.TxnIsolation}, {"READ-COMMITTED", "REPEATABLE-READ"}},
		{{variable.TxnIsolationOneShot}, {"READ-COMMITTED", "SERIALIZABLE"}},
		{{variable.TiDBTxnMode}, {"", "pessimistic", "optimistic"}},
		{{variable.DivPrecisionIncrement}, {"0", "4", "30"}},
		{{variable.TiDBOptForceInlineCTE}, {"ON", "OFF"}},
		{{variable.TiDBRuntimeFilterModeName}, {"OFF", "LOCAL"}},
		{{variable.TiDBRuntimeFilterTypeName}, {"IN", "MIN_MAX", "IN,MIN_MAX"}},
		{{variable.TiDBOptObjective}, {"moderate", "determinate"}},
		{{variable.TiDBLowResolutionTSO}, {"ON", "OFF"}},
		{{variable.TiDBEnablePlanReplayerCapture}, {"ON", "OFF"}},
		{{variable.TiDBEnablePlanReplayerContinuousCapture}, {"ON", "OFF"}},
		{{variable.TiDBOptFixControl}, {"44822:ON,45132:1000", ""}},
		{{variable.TiDBEnableLocalTxn}, {"ON"}},
		{{variable.TiDBTxnScope}, {"local", "global"}},
		{{variable.TiDBEnableRowLevelChecksum}, {"ON"}},
		{{variable.TiDBRowFormatVersion}, {"1", "2"}},
		{{variable.TiDBTxnReadTS}, {"", "2020-01-01 00:00:00"}},
	}
	randCall := func() []any {
		switch r.IntN(26) {
		case 0, 1:
			return []any{concSetters[r.IntN(len(concSetters))], []int{-1, 0, 1, 4, 32}[r.IntN(5)]}
		case 2:
			return []any{boolSetters[r.IntN(len(boolSetters))], r.IntN(2) == 0}
		case 3:
			return []any{"SetPrevStmtDigest", []string{"", "abc", "d1"}[r.IntN(3)]}
		case 4:
			return []any{"SetResourceGroupName", []string{"default", "rg1"}[r.IntN(2)]}
		case 5:
			return []any{"SetNextPreparedStmtID", []uint32{0, 7, math.MaxUint32}[r.IntN(3)]}
		case 6:
			return []any{"GetNextPreparedStmtID"}
		case 7:
			return []any{"AllocNewPlanID"}
		case 8:
			return []any{"AllocPlanColumnID"}
		case 9:
			return []any{"SetPlanIDs", []int{0, math.MaxInt32, -5}[r.IntN(3)], []int{0, math.MaxInt64, -5}[r.IntN(3)]}
		case 10:
			return []any{"SetStringUserVar", []string{"A", "b", "Cd"}[r.IntN(3)], vec.GoString([]string{"x", "héllo", "\xff"}[r.IntN(3)]), []string{"", "utf8mb4_bin", "latin1_swedish_ci"}[r.IntN(3)]}
		case 11:
			return []any{"IsIsolation", []string{"READ-COMMITTED", "REPEATABLE-READ", "SERIALIZABLE"}[r.IntN(3)]}
		case 12:
			return []any{"SetTxnIsolationLevelOneShotStateForNextTxn"}
		case 13:
			return []any{"SetInTxn", r.IntN(2) == 0}
		case 14:
			return []any{"TxnCtx", uint64(r.IntN(3)) * 100, r.IntN(2) == 0, []string{"", "READ-COMMITTED"}[r.IntN(2)]}
		case 15:
			return []any{"SetForUpdateTS", uint64(r.IntN(4)) * 60}
		case 16:
			return []any{[]string{"SetTxnReadTS", "UseTxnReadTS", "CleanupTxnReadTSIfUsed", "SetSnapshotInfoschema"}[r.IntN(4)], uint64(r.IntN(3)) * 9}
		case 17:
			return []any{[]string{"AddPlanReplayerFinishedTaskKey", "CheckPlanReplayerFinishedTaskKey"}[r.IntN(2)], []string{"s1", "s2"}[r.IntN(2)], []string{"p1", "p2"}[r.IntN(2)]}
		case 18:
			return []any{"MDL", int64(r.IntN(3)), int64(r.IntN(5))}
		case 19:
			return []any{"ClearRelatedTableForMDL"}
		case 20:
			return []any{[]string{"AddPreparedStmt", "RemovePreparedStmt", "GetPreparedStmtByID"}[r.IntN(3)], uint32(r.IntN(4))}
		case 21:
			return []any{[]string{"NamePreparedStmt", "GetPreparedStmtByName"}[r.IntN(2)], []string{"a", "b"}[r.IntN(2)], uint32(r.IntN(4))}
		case 22:
			return []any{[]string{"WithdrawAllPreparedStmt", "TxnCtx.Cleanup"}[r.IntN(2)]}
		case 23:
			return []any{"SetMaxPreparedStmtCount", []int64{-1, 0, 1, 2, 16382}[r.IntN(5)]}
		case 24:
			if r.IntN(2) == 0 {
				return []any{"RegisterScalarSubQ", r.IntN(9)}
			}
			return []any{"SetEnableAdaptiveReplicaRead", r.IntN(2) == 0}
		default:
			f := []string{"InRestrictedSQL", "ConnectionID", "RowEncoder.Enable", "EnableRowLevelChecksum", "TiKVClientReadTimeout"}[r.IntN(5)]
			if f == "ConnectionID" || f == "TiKVClientReadTimeout" {
				return []any{"Field", f, uint64(r.IntN(2))}
			}
			return []any{"Field", f, r.IntN(2) == 0}
		}
	}
	// Each setting alone, then random combinations.
	for _, s := range settings {
		for _, v := range s[1] {
			g.Add("sys/methods", "methods", [][2]string{{s[0][0], v}}, [][]any{})
		}
	}
	for _, c := range concSetters {
		for _, n := range []int{-1, 1, 9} {
			g.Add("sys/methods", "methods", [][2]string{{variable.TiDBExecutorConcurrency, "6"}}, [][]any{{c, n}})
		}
	}
	for k := 0; k < 400; k++ {
		var sets [][2]string
		for j := r.IntN(5); j > 0; j-- {
			s := settings[r.IntN(len(settings))]
			sets = append(sets, [2]string{s[0][0], s[1][r.IntN(len(s[1]))]})
		}
		var calls [][]any
		for j := 1 + r.IntN(10); j > 0; j-- {
			calls = append(calls, randCall())
		}
		g.Add("sys/methods", "methods", sets, calls)
	}
	// Isolation scenarios (one-shot set / use / def; in and out of a transaction).
	for _, inTxn := range []bool{false, true} {
		for _, steps := range [][][]any{
			{},
			{{"SetTxnIsolationLevelOneShotStateForNextTxn"}},
			{{"SetTxnIsolationLevelOneShotStateForNextTxn"}, {"SetTxnIsolationLevelOneShotStateForNextTxn"}},
			{{"SetTxnIsolationLevelOneShotStateForNextTxn"}, {"IsIsolation", "SERIALIZABLE"}},
			{{"TxnCtx", uint64(5), true, ""}, {"SetTxnIsolationLevelOneShotStateForNextTxn"}},
		} {
			calls := append([][]any{{"SetInTxn", inTxn}}, steps...)
			g.Add("sys/methods", "methods", [][2]string{{variable.TxnIsolationOneShot, "READ-COMMITTED"}}, calls)
		}
	}
	// TxnReadTS (nil until tx_read_ts is set): set / use / cleanup, with readTS 0 and not.
	for _, set := range [][][2]string{{}, {{variable.TiDBTxnReadTS, ""}}} {
		for _, ts := range []uint64{0, 9} {
			for _, use := range []bool{false, true} {
				calls := [][]any{{"SetTxnReadTS", ts}}
				if use {
					calls = append(calls, []any{"UseTxnReadTS"})
				}
				calls = append(calls, []any{"SetSnapshotInfoschema"}, []any{"CleanupTxnReadTSIfUsed"}, []any{"UseTxnReadTS"})
				g.Add("sys/methods", "methods", set, calls)
			}
		}
	}
	// Prepared statements against max_prepared_stmt_count and the process-wide count.
	for _, max := range []int64{-1, 0, 1, 2} {
		g.Add("sys/methods", "methods", [][2]string{}, [][]any{{"SetMaxPreparedStmtCount", max},
			{"AddPreparedStmt", 1}, {"AddPreparedStmt", 1}, {"AddPreparedStmt", 2}, {"AddPreparedStmt", 3},
			{"NamePreparedStmt", "x", 2}, {"GetPreparedStmtByName", "x"}, {"GetPreparedStmtByName", "y"},
			{"RemovePreparedStmt", 2}, {"GetPreparedStmtByName", "x"}, {"RemovePreparedStmt", 9}, {"WithdrawAllPreparedStmt"}})
	}
	// Plan replayer keys past planReplayerSessionFinishedTaskKeyLen (128).
	var keys [][]any
	keys = append(keys, []any{"CheckPlanReplayerFinishedTaskKey", "s", "p"})
	for k := 0; k < 130; k++ {
		keys = append(keys, []any{"AddPlanReplayerFinishedTaskKey", fmt.Sprintf("s%d", k), "p"})
	}
	keys = append(keys, []any{"CheckPlanReplayerFinishedTaskKey", "s0", "p"}, []any{"CheckPlanReplayerFinishedTaskKey", "s129", "p"})
	g.Add("sys/methods", "methods", [][2]string{}, keys)

	// uservars scripts.
	datums := [][]any{{"null"}, {"i", -5}, {"u", uint64(math.MaxUint64)}, {"f", math.Float64bits(1.5)},
		{"s", vec.GoString("abc"), "utf8mb4_bin"}, {"s", vec.GoString("\xff\x00"), "binary"}, {"b", "00ff"}}
	fts := [][]any{{mysql.TypeLonglong, 0, 20, 0, "binary", "binary"}, {mysql.TypeVarString, 0, -1, -1, "utf8mb4", "utf8mb4_bin"}}
	names := []string{"a", "A", "b", "ab"}
	for k := 0; k < 300; k++ {
		var steps [][]any
		for j := 1 + r.IntN(12); j > 0; j-- {
			n := names[r.IntN(len(names))]
			switch r.IntN(7) {
			case 0, 1:
				steps = append(steps, []any{"set", n, datums[r.IntN(len(datums))]})
			case 2:
				steps = append(steps, []any{"unset", n})
			case 3:
				steps = append(steps, []any{"settype", n, fts[r.IntN(len(fts))]})
			case 4:
				steps = append(steps, []any{"clone"})
			case 5:
				steps = append(steps, []any{"get", n})
			default:
				steps = append(steps, []any{"gettype", n})
			}
		}
		g.Add("sys/uservars", "uservars", steps)
	}
	// paramlist scripts (a get past the end panics, as in Go).
	for k := 0; k < 200; k++ {
		var steps [][]any
		n := 0
		for j := 1 + r.IntN(8); j > 0; j-- {
			switch r.IntN(6) {
			case 0, 1:
				var ds [][]any
				for m := r.IntN(3); m >= 0; m-- {
					ds = append(ds, datums[r.IntN(len(datums))])
				}
				n += len(ds)
				steps = append(steps, []any{"append", ds})
			case 2:
				steps = append(steps, []any{"reset"})
				n = 0
			case 3:
				steps = append(steps, []any{"nonprep", r.IntN(2) == 0})
			case 4:
				if n > 0 {
					steps = append(steps, []any{"get", r.IntN(n)})
				} else {
					steps = append(steps, []any{"all"})
				}
			default:
				steps = append(steps, []any{"string"})
			}
		}
		steps = append(steps, []any{"all"}, []any{"string"})
		g.Add("sys/paramlist", "paramlist", steps)
	}
	g.Add("sys/paramlist", "paramlist", [][]any{{"append", [][]any{{"i", 1}}}, {"get", 1}})
	g.Add("sys/paramlist", "paramlist", [][]any{{"get", -1}})
	g.Add("sys/paramlist", "paramlist", [][]any{{"get", 0}})

	// svmisc.
	for _, t := range []string{"TCP", "UnixSocket", "SSL/TLS", "", "ssl/tls"} {
		g.Add("sys/svmisc", "svmisc", "secure", t)
	}
	for _, n := range []int64{-1, 0, 1, 2, 3, 4} {
		g.Add("sys/svmisc", "svmisc", "rftype", n)
		g.Add("sys/svmisc", "svmisc", "rfmode", n)
	}
	for _, m := range []string{"force_preagg", "force_streaming", "auto", "AUTO", "", "x"} {
		g.Add("sys/svmisc", "svmisc", "preagg", m)
	}
	for _, sql := range []string{"", "select 1", "select \x00?", "\xff"} {
		for _, mode := range []int64{0, int64(mysql.ModeANSIQuotes | mysql.ModePipesAsConcat), -1} {
			g.Add("sys/svmisc", "svmisc", "dedup", vec.GoString(sql), "utf8mb4", "utf8mb4_bin", "test", mode)
		}
	}
	for _, s := range []string{"weak", "strict", "WEAK", ""} {
		g.Add("sys/svmisc", "svmisc", "weak", s)
	}
	g.Add("sys/svmisc", "svmisc", "rewrite")
	g.Add("sys/svmisc", "svmisc", "adaptive", []bool{true, false, false, true, true})
}

// svdMethodsFuncs are the Go functions op "methods" covers.
var svdMethodsFuncs = []string{
	"Concurrency.AnalyzeDistSQLScanConcurrency", "Concurrency.DistSQLScanConcurrency", "Concurrency.HashAggFinalConcurrency",
	"Concurrency.HashAggPartialConcurrency", "Concurrency.HashJoinConcurrency", "Concurrency.IndexLookupConcurrency",
	"Concurrency.IndexLookupJoinConcurrency", "Concurrency.IndexMergeIntersectionConcurrency",
	"Concurrency.IndexSerialScanConcurrency", "Concurrency.MergeJoinConcurrency", "Concurrency.ProjectionConcurrency",
	"Concurrency.SetAnalyzeDistSQLScanConcurrency", "Concurrency.SetDistSQLScanConcurrency",
	"Concurrency.SetHashAggFinalConcurrency", "Concurrency.SetHashAggPartialConcurrency", "Concurrency.SetHashJoinConcurrency",
	"Concurrency.SetIndexLookupConcurrency", "Concurrency.SetIndexLookupJoinConcurrency",
	"Concurrency.SetIndexMergeIntersectionConcurrency", "Concurrency.SetIndexSerialScanConcurrency",
	"Concurrency.SetMergeJoinConcurrency", "Concurrency.SetProjectionConcurrency", "Concurrency.SetStreamAggConcurrency",
	"Concurrency.SetWindowConcurrency", "Concurrency.StreamAggConcurrency", "Concurrency.UnionConcurrency",
	"Concurrency.WindowConcurrency",
	"TxnReadTS.SetTxnReadTS", "TxnReadTS.UseTxnReadTS",
	"TransactionContext.Cleanup", "TransactionContext.GetForUpdateTS", "TransactionContext.SetForUpdateTS",
	"SessionVars.AddPlanReplayerFinishedTaskKey", "SessionVars.AddPreparedStmt", "SessionVars.AllocNewPlanID",
	"SessionVars.AllocPlanColumnID", "SessionVars.CheckAndGetTxnScope", "SessionVars.CheckPlanReplayerFinishedTaskKey",
	"SessionVars.ChooseMppExchangeCompressionMode", "SessionVars.CleanupTxnReadTSIfUsed", "SessionVars.ClearRelatedTableForMDL",
	"SessionVars.EnableEvalTopNEstimationForStrMatch", "SessionVars.EnableForceInlineCTE", "SessionVars.GetAllowPreferRangeScan",
	"SessionVars.GetCPUFactor", "SessionVars.GetCharsetInfo", "SessionVars.GetConcurrencyFactor", "SessionVars.GetCopCPUFactor",
	"SessionVars.GetDescScanFactor", "SessionVars.GetDiskFactor", "SessionVars.GetDivPrecisionIncrement",
	"SessionVars.GetEnableIndexMerge", "SessionVars.GetEnablePseudoForOutdatedStats", "SessionVars.GetIsolationReadEngines",
	"SessionVars.GetMemoryFactor", "SessionVars.GetNegateStrMatchDefaultSelectivity", "SessionVars.GetNetworkFactor",
	"SessionVars.GetNextPreparedStmtID", "SessionVars.GetOptObjective", "SessionVars.GetOptimizerFixControlMap",
	"SessionVars.GetPreparedStmtByID", "SessionVars.GetPreparedStmtByName", "SessionVars.GetPrevStmtDigest",
	"SessionVars.GetReadableTxnMode", "SessionVars.GetRelatedTableForMDL", "SessionVars.GetRuntimeFilterMode",
	"SessionVars.GetRuntimeFilterTypes", "SessionVars.GetScanFactor", "SessionVars.GetSeekFactor", "SessionVars.GetSessionVars",
	"SessionVars.GetSplitRegionTimeout", "SessionVars.GetStrMatchDefaultSelectivity", "SessionVars.GetTiKVClientReadTimeout",
	"SessionVars.IsDynamicPartitionPruneEnabled", "SessionVars.IsIsolation", "SessionVars.IsMPPAllowed",
	"SessionVars.IsMPPEnforced", "SessionVars.IsPartialOrderedIndexForTopNEnabled", "SessionVars.IsPessimisticReadConsistency",
	"SessionVars.IsPlanReplayerCaptureEnabled", "SessionVars.IsReplicaReadClosestAdaptive", "SessionVars.IsRowLevelChecksumEnabled",
	"SessionVars.IsRuntimeFilterEnabled", "SessionVars.IsTiFlashCopBanned", "SessionVars.IsolationLevelForNewTxn",
	"SessionVars.RemovePreparedStmt", "SessionVars.SetAllowInSubqToJoinAndAgg", "SessionVars.SetAllowPreferRangeScan",
	"SessionVars.SetEnableCascadesPlanner", "SessionVars.SetEnableIndexMerge", "SessionVars.SetEnablePseudoForOutdatedStats",
	"SessionVars.SetNextPreparedStmtID", "SessionVars.SetPrevStmtDigest", "SessionVars.SetResourceGroupName",
	"SessionVars.SetStringUserVar", "SessionVars.SetTxnIsolationLevelOneShotStateForNextTxn", "SessionVars.UseLowResolutionTSO",
	"SessionVars.WithdrawAllPreparedStmt", "SessionVars.initializePlanReplayerFinishedTaskKey",
	"SessionVars.Location", "SessionVars.RegisterScalarSubQ", "SessionVars.SetInTxn", "SessionVars.InTxn",
	"SessionVars.HasStatusFlag", "SessionVars.SetStatusFlag",
}
