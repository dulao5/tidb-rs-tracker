package domains

// D05.2 PR E: the SessionVars methods that read the statement context (statement hints,
// read-only flags, LAST_INSERT_ID, MPP warnings), the chunk allocator state behind
// @@last_sql_use_alloc, InitStatementContext, and the StmtHints data type (block D13.0)
// (tidb-rs specs/D05.2-sessionvars/plan.md "PR E").
//   - stmtvars: on a fresh session (as op "methods"), a list of SetSystemVar settings, then a
//     list of calls: statement context fields and hints, the hint getters, GetReplicaRead
//     (which may append a warning), SetLastInsertID, RaiseWarningWhenMPPEnforced, the chunk
//     allocator (SetAlloc / ClearAlloc / allocating through GetChunkAllocator /
//     ExchangeChunkStatus / IsAllocValid / GetUseChunkAlloc / InitChunkAllocSize), and
//     InitStatementContext (the session installs the result, as executor/select.go does).
//     Each call's result is recorded, then the statement context's warnings, extra warnings,
//     LastInsertID and the allocator state, and @@last_sql_use_alloc.
//   - stmthints: StmtHints.Clone and TaskMapNeedBackUp; the original is changed after the
//     clone, which must not see it.

import (
	"context"
	"encoding/json"
	"math/rand/v2"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/hint"
)

// sveFuncs are the Go functions op "stmtvars" covers.
var sveFuncs = []string{
	"SessionVars.GetAllowInSubqToJoinAndAgg", "SessionVars.GetEnableCascadesPlanner", "SessionVars.GetReplicaRead",
	"SessionVars.GetMaxExecutionTime", "SessionVars.SetLastInsertID", "SessionVars.RaiseWarningWhenMPPEnforced",
	"SessionVars.GetChunkAllocator", "SessionVars.ExchangeChunkStatus", "SessionVars.GetUseChunkAlloc",
	"SessionVars.SetAlloc", "SessionVars.IsAllocValid", "SessionVars.ClearAlloc", "SessionVars.InitStatementContext",
}

func sveExec(op string, args []json.RawMessage) (any, error, bool) {
	switch op {
	case "stmtvars":
		resetSysvarAtomics()
		variable.SetEnableAdaptiveReplicaRead(true)
		chunk.InitChunkAllocSize(64, 256)
		defer chunk.InitChunkAllocSize(64, 256)
		defer variable.SetEnableAdaptiveReplicaRead(true)
		vars := freshVars(svCtx{})
		var setErrs []any
		for _, p := range rawList(arg[[]json.RawMessage](args, 0)) {
			setErrs = append(setErrs, errStr(vars.SetSystemVar(arg[string](p, 0), arg[string](p, 1))))
		}
		// The connection's allocator (server/conn.go cc.chunkAlloc), nil until "NewAllocator".
		var connAlloc chunk.Allocator
		var calls []any
		for _, c := range rawList(arg[[]json.RawMessage](args, 1)) {
			calls = append(calls, sveCall(vars, &connAlloc, c))
		}
		var warns, extra []any
		for _, w := range vars.StmtCtx.GetWarnings() {
			warns = append(warns, []any{w.Level, w.Err.Error()})
		}
		for _, w := range vars.StmtCtx.GetExtraWarnings() {
			extra = append(extra, []any{w.Level, w.Err.Error()})
		}
		last, err := vars.GetSessionOrGlobalSystemVar(context.Background(), variable.TiDBUseAlloc)
		out := map[string]any{
			"set": setErrs, "calls": calls, "warns": warns, "extra": extra,
			"lastInsertID":       vars.StmtCtx.LastInsertID,
			"useChunkAlloc":      vars.GetUseChunkAlloc(),
			"isAllocValid":       vars.IsAllocValid(),
			"last_sql_use_alloc": []any{last, errStr(err)},
		}
		return out, nil, true
	case "stmthints":
		h := sveHints(arg[map[string]json.RawMessage](args, 0))
		c := h.Clone()
		// Later writes to the original must not reach the clone.
		if h.SetVars != nil {
			h.SetVars["later"] = "x"
		}
		h.ForceNthPlan = 99
		return map[string]any{
			"orig":     []any{h.TaskMapNeedBackUp(), h.HintedHypoIndexes != nil},
			"clone":    sveHintsDump(c),
			"cloneMap": c.SetVars != nil,
		}, nil, true
	}
	return nil, nil, false
}

// sveHints builds a StmtHints from a field map.
func sveHints(m map[string]json.RawMessage) *hint.StmtHints {
	h := &hint.StmtHints{}
	get := func(k string, p any) {
		if v, ok := m[k]; ok {
			if err := json.Unmarshal(v, p); err != nil {
				panic(err)
			}
		}
	}
	get("MemQuotaQuery", &h.MemQuotaQuery)
	get("MaxExecutionTime", &h.MaxExecutionTime)
	get("ReplicaRead", &h.ReplicaRead)
	get("AllowInSubqToJoinAndAgg", &h.AllowInSubqToJoinAndAgg)
	get("NoIndexMergeHint", &h.NoIndexMergeHint)
	get("StraightJoinOrder", &h.StraightJoinOrder)
	get("EnableCascadesPlanner", &h.EnableCascadesPlanner)
	get("ForceNthPlan", &h.ForceNthPlan)
	get("ResourceGroup", &h.ResourceGroup)
	get("WriteSlowLog", &h.WriteSlowLog)
	get("HasAllowInSubqToJoinAndAggHint", &h.HasAllowInSubqToJoinAndAggHint)
	get("HasMemQuotaHint", &h.HasMemQuotaHint)
	get("HasReplicaReadHint", &h.HasReplicaReadHint)
	get("HasMaxExecutionTime", &h.HasMaxExecutionTime)
	get("HasEnableCascadesPlannerHint", &h.HasEnableCascadesPlannerHint)
	get("HasResourceGroup", &h.HasResourceGroup)
	if _, ok := m["SetVars"]; ok {
		h.SetVars = map[string]string{}
		get("SetVars", &h.SetVars)
	}
	var hypo bool
	get("Hypo", &hypo)
	if hypo {
		h.HintedHypoIndexes = map[string]map[string]map[string]*model.IndexInfo{"db": {"t": {"i": {}}}}
	}
	return h
}

func sveHintsDump(h *hint.StmtHints) map[string]any {
	return map[string]any{
		"MemQuotaQuery": h.MemQuotaQuery, "MaxExecutionTime": h.MaxExecutionTime, "ReplicaRead": h.ReplicaRead,
		"AllowInSubqToJoinAndAgg": h.AllowInSubqToJoinAndAgg, "NoIndexMergeHint": h.NoIndexMergeHint,
		"StraightJoinOrder": h.StraightJoinOrder, "EnableCascadesPlanner": h.EnableCascadesPlanner,
		"ForceNthPlan": h.ForceNthPlan, "ResourceGroup": h.ResourceGroup, "WriteSlowLog": h.WriteSlowLog,
		"HasAllowInSubqToJoinAndAggHint": h.HasAllowInSubqToJoinAndAggHint, "HasMemQuotaHint": h.HasMemQuotaHint,
		"HasReplicaReadHint": h.HasReplicaReadHint, "HasMaxExecutionTime": h.HasMaxExecutionTime,
		"HasEnableCascadesPlannerHint": h.HasEnableCascadesPlannerHint, "HasResourceGroup": h.HasResourceGroup,
		"SetVars": h.SetVars, "Hypo": h.HintedHypoIndexes != nil, "TaskMapNeedBackUp": h.TaskMapNeedBackUp(),
	}
}

// sveFts are the field types of the chunks allocated through GetChunkAllocator.
var sveFts = []*types.FieldType{types.NewFieldType(mysql.TypeLonglong), types.NewFieldType(mysql.TypeVarchar)}

// sveCall runs one call of op "stmtvars" and returns its result (nil for setters).
func sveCall(sv *variable.SessionVars, connAlloc *chunk.Allocator, c []json.RawMessage) any {
	name := arg[string](c, 0)
	b := func(k int) bool { return arg[bool](c, k) }
	s := func(k int) string { return arg[string](c, k) }
	u := func(k int) uint64 { return arg[uint64](c, k) }
	switch name {
	case "sc":
		sc := sv.StmtCtx
		switch s(1) {
		case "IsReadOnly":
			sc.IsReadOnly = b(2)
		case "RCCheckTS":
			sc.RCCheckTS = b(2)
		case "InExplainStmt":
			sc.InExplainStmt = b(2)
		case "HasAllowInSubqToJoinAndAggHint":
			sc.HasAllowInSubqToJoinAndAggHint = b(2)
		case "AllowInSubqToJoinAndAgg":
			sc.AllowInSubqToJoinAndAgg = b(2)
		case "HasEnableCascadesPlannerHint":
			sc.HasEnableCascadesPlannerHint = b(2)
		case "EnableCascadesPlanner":
			sc.EnableCascadesPlanner = b(2)
		case "HasReplicaReadHint":
			sc.HasReplicaReadHint = b(2)
		case "ReplicaRead":
			sc.ReplicaRead = byte(u(2))
		case "HasMaxExecutionTime":
			sc.HasMaxExecutionTime = b(2)
		case "MaxExecutionTime":
			sc.MaxExecutionTime = u(2)
		case "warn":
			sc.AppendWarning(errors.New(s(2)))
		default:
			panic("bad sc field " + s(1))
		}
	case "GetAllowInSubqToJoinAndAgg":
		return sv.GetAllowInSubqToJoinAndAgg()
	case "GetEnableCascadesPlanner":
		return sv.GetEnableCascadesPlanner()
	case "GetReplicaRead":
		return int(sv.GetReplicaRead())
	case "GetMaxExecutionTime":
		return sv.GetMaxExecutionTime()
	case "SetLastInsertID":
		sv.SetLastInsertID(u(1))
	case "RaiseWarningWhenMPPEnforced":
		sv.RaiseWarningWhenMPPEnforced(s(1))
	case "NewAllocator":
		*connAlloc = chunk.NewAllocator()
	case "SetAlloc":
		sv.SetAlloc(*connAlloc)
	case "ClearAlloc":
		// conn.go: ClearAlloc(&cc.chunkAlloc, err != nil); conn_stmt.go / optimizer.go: (nil, false).
		if b(2) {
			sv.ClearAlloc(connAlloc, b(1))
			if *connAlloc == nil {
				return nil
			}
			return (*connAlloc).CheckReuseAllocSize()
		}
		sv.ClearAlloc(nil, false)
	case "Alloc":
		chk := sv.GetChunkAllocator().Alloc(sveFts, 1, 32)
		return []any{chk.Capacity(), chk.RequiredRows(), chk.NumCols()}
	case "ExchangeChunkStatus":
		sv.ExchangeChunkStatus()
	case "IsAllocValid":
		return sv.IsAllocValid()
	case "GetUseChunkAlloc":
		return sv.GetUseChunkAlloc()
	case "InitChunkAllocSize":
		chunk.InitChunkAllocSize(uint32(u(1)), uint32(u(2)))
	case "InitStatementContext":
		sv.StmtCtx = sv.InitStatementContext()
	case "last_sql_use_alloc":
		v, err := sv.GetSessionOrGlobalSystemVar(context.Background(), variable.TiDBUseAlloc)
		return []any{v, errStr(err)}
	default:
		panic("bad call " + name)
	}
	return nil
}

// sveGenerate appends the PR E records (after every earlier record).
func sveGenerate(g *vec.Gen, rng *rand.Rand) {
	r := rand.New(rand.NewPCG(rng.Uint64(), 53))
	settings := [][2][]string{
		{{variable.TiDBReplicaRead}, {"leader", "follower", "closest-adaptive", "closest-replicas", "learner", "prefer-leader"}},
		{{variable.TiDBRCWriteCheckTs}, {"ON", "OFF"}},
		{{variable.TiDBAllowMPPExecution}, {"ON", "OFF"}},
		{{variable.TiDBEnforceMPPExecution}, {"ON", "OFF"}},
		{{variable.TiDBEnableReusechunk}, {"ON", "OFF"}},
		{{variable.MaxExecutionTime}, {"0", "1000"}},
		{{variable.TiDBOptInSubqToJoinAndAgg}, {"ON", "OFF"}},
		{{variable.TiDBEnableCascadesPlanner}, {"ON", "OFF"}},
	}
	scFields := []string{"IsReadOnly", "RCCheckTS", "InExplainStmt", "HasAllowInSubqToJoinAndAggHint",
		"AllowInSubqToJoinAndAgg", "HasEnableCascadesPlannerHint", "EnableCascadesPlanner", "HasReplicaReadHint",
		"HasMaxExecutionTime"}
	randCall := func() []any {
		switch r.IntN(20) {
		case 0, 1, 2:
			return []any{"sc", scFields[r.IntN(len(scFields))], r.IntN(2) == 0}
		case 3:
			return []any{"sc", "ReplicaRead", uint64(r.IntN(7))}
		case 4:
			return []any{"sc", "MaxExecutionTime", []uint64{0, 5, 1 << 40}[r.IntN(3)]}
		case 5:
			return []any{"sc", "warn", []string{"w1", "Ignore replica read hint for non-read-only statement"}[r.IntN(2)]}
		case 6, 7:
			return []any{[]string{"GetAllowInSubqToJoinAndAgg", "GetEnableCascadesPlanner", "GetReplicaRead",
				"GetMaxExecutionTime"}[r.IntN(4)]}
		case 8:
			return []any{"SetLastInsertID", []uint64{0, 7, 1 << 63}[r.IntN(3)]}
		case 9:
			return []any{"RaiseWarningWhenMPPEnforced", []string{"MPP mode may be blocked", "x"}[r.IntN(2)]}
		case 10:
			return []any{"NewAllocator"}
		case 11:
			return []any{"SetAlloc"}
		case 12:
			hasErr := r.IntN(2) == 0
			return []any{"ClearAlloc", hasErr, hasErr || r.IntN(2) == 0}
		case 13, 14:
			return []any{"Alloc"}
		case 15:
			return []any{[]string{"ExchangeChunkStatus", "IsAllocValid", "GetUseChunkAlloc"}[r.IntN(3)]}
		case 16:
			return []any{"InitChunkAllocSize", uint64([]int{0, 1, 64}[r.IntN(3)]), uint64([]int{0, 256}[r.IntN(2)])}
		case 17:
			return []any{"InitStatementContext"}
		default:
			return []any{"last_sql_use_alloc"}
		}
	}
	// Each setting with each hint getter.
	for _, st := range settings {
		for _, v := range st[1] {
			g.Add("sys/stmtvars", "stmtvars", [][2]string{{st[0][0], v}}, [][]any{
				{"GetAllowInSubqToJoinAndAgg"}, {"GetEnableCascadesPlanner"}, {"GetReplicaRead"},
				{"sc", "IsReadOnly", true}, {"GetReplicaRead"}, {"GetMaxExecutionTime"},
				{"RaiseWarningWhenMPPEnforced", "w"}})
		}
	}
	// The connection's statement cycle (server/conn.go + executor/select.go): SetAlloc,
	// InitStatementContext, executors allocate, ExchangeChunkStatus, ClearAlloc; then
	// @@last_sql_use_alloc on the next statement.
	for _, reuse := range []string{"ON", "OFF"} {
		for _, sizes := range [][2]uint64{{64, 256}, {0, 0}, {0, 1}, {1, 0}} {
			for _, allocs := range []int{0, 1, 3} {
				calls := [][]any{{"InitChunkAllocSize", sizes[0], sizes[1]}, {"NewAllocator"}, {"SetAlloc"}, {"InitStatementContext"}}
				for i := 0; i < allocs; i++ {
					calls = append(calls, []any{"Alloc"})
				}
				calls = append(calls, []any{"GetUseChunkAlloc"}, []any{"ExchangeChunkStatus"}, []any{"IsAllocValid"},
					[]any{"ClearAlloc", false, true}, []any{"IsAllocValid"}, []any{"InitStatementContext"},
					[]any{"GetUseChunkAlloc"}, []any{"last_sql_use_alloc"}, []any{"SetAlloc"}, []any{"Alloc"},
					[]any{"ExchangeChunkStatus"}, []any{"last_sql_use_alloc"})
				g.Add("sys/stmtvars", "stmtvars", [][2]string{{variable.TiDBEnableReusechunk, reuse}}, calls)
			}
		}
	}
	// The read-only adjustments of GetReplicaRead, with the hint and the existing warning.
	for _, ro := range []bool{false, true} {
		for _, rc := range []bool{false, true} {
			for _, rcw := range []string{"OFF", "ON"} {
				for _, hinted := range []bool{false, true} {
					g.Add("sys/stmtvars", "stmtvars", [][2]string{{variable.TiDBReplicaRead, "follower"},
						{variable.TiDBRCWriteCheckTs, rcw}}, [][]any{
						{"sc", "IsReadOnly", ro}, {"sc", "RCCheckTS", rc}, {"sc", "HasReplicaReadHint", hinted},
						{"sc", "ReplicaRead", uint64(2)}, {"GetReplicaRead"}, {"GetReplicaRead"}})
				}
			}
		}
	}
	// Random scripts.
	for i := 0; i < 400; i++ {
		var sets [][2]string
		for j := r.IntN(3); j > 0; j-- {
			st := settings[r.IntN(len(settings))]
			sets = append(sets, [2]string{st[0][0], st[1][r.IntN(len(st[1]))]})
		}
		var calls [][]any
		for j := 1 + r.IntN(14); j > 0; j-- {
			calls = append(calls, randCall())
		}
		calls = append(calls, []any{"last_sql_use_alloc"})
		g.Add("sys/stmtvars", "stmtvars", sets, calls)
	}

	// stmthints.
	g.Add("sys/stmthints", "stmthints", map[string]any{})
	g.Add("sys/stmthints", "stmthints", map[string]any{"ForceNthPlan": -1})
	g.Add("sys/stmthints", "stmthints", map[string]any{"SetVars": map[string]string{}})
	for i := 0; i < 100; i++ {
		m := map[string]any{
			"MemQuotaQuery": int64(r.IntN(3)) * 1024, "MaxExecutionTime": uint64(r.IntN(3)) * 10,
			"ReplicaRead": r.IntN(4), "AllowInSubqToJoinAndAgg": r.IntN(2) == 0, "NoIndexMergeHint": r.IntN(2) == 0,
			"StraightJoinOrder": r.IntN(2) == 0, "EnableCascadesPlanner": r.IntN(2) == 0,
			"ForceNthPlan": []int64{-1, 0, 1, 7}[r.IntN(4)], "ResourceGroup": []string{"", "rg"}[r.IntN(2)],
			"WriteSlowLog": r.IntN(2) == 0, "HasAllowInSubqToJoinAndAggHint": r.IntN(2) == 0,
			"HasMemQuotaHint": r.IntN(2) == 0, "HasReplicaReadHint": r.IntN(2) == 0, "HasMaxExecutionTime": r.IntN(2) == 0,
			"HasEnableCascadesPlannerHint": r.IntN(2) == 0, "HasResourceGroup": r.IntN(2) == 0,
			"Hypo": r.IntN(2) == 0,
		}
		if r.IntN(2) == 0 {
			sv := map[string]string{}
			for j := r.IntN(3); j > 0; j-- {
				sv[[]string{"tidb_opt_x", "max_execution_time", "a"}[r.IntN(3)]] = []string{"1", "ON"}[r.IntN(2)]
			}
			m["SetVars"] = sv
		}
		g.Add("sys/stmthints", "stmthints", m)
	}
}
