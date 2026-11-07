package domains

// Domain "memory": block D15.1 (pkg/util/memory: the Tracker tree, the OOM
// action chain; pkg/util/sqlkiller as used by the tracker). Records:
//
//   - "script": a sequence of steps on a set of trackers, actions and killers,
//     identified by small integers. args[0] is the step list; out is one entry
//     per step: {"r": result, "fired": [...]} where "fired" lists the actions
//     that ran during the step ("mock:<aid>:<label>", "log:<aid>:<conn>",
//     "panic:<aid>:<conn>") in order. Errors are {"code","msg"}; a Go panic in
//     a step is {"panic": msg} and ends the script. The steps (see memExec):
//
//     new t label limit | new_global t label limit | init t label limit aid
//     attach c p | attach_global c g | detach t | detach_global t
//     replace p old new | consume t n | release t n | buf_consume t buf n
//     buf_release t buf n | set_limit t n | set_label t label | reset t
//     replace_bytes t n | reset_max t | check_exceed t | check_limit t n
//     get_limit t | string t | count_children t | search_label t label
//     search_more t n | children t | less a b | sess_root t k | session_id t n
//     killer k conn | kill k signal | killer_reset k | handle_kill t
//     killer_handle k | act aid kind arg | act_prio aid inner prio
//     set_action t aid | fallback t aid | fallback_soft t aid | unbind t
//     unbind_hard t aid | finish aid | is_finished aid | get_fallback aid
//     chain t ignoreFinished | trigger_fallback aid t | sess_min n | top1 | dump
//
//     An id of -1 is nil. "act" kinds: mock (a recording action with priority
//     arg; a second trigger runs its fallback, like tracker_test.go's
//     mockAction), log (LogOnExceed, ConnID arg, with a recording log hook),
//     panic (PanicOnExceed with killer arg, ConnID = 100+arg, recording hook).
//   - "format_bytes", "bytes_to_string": args [n].
//
// Each script starts with MemUsageTop1Tracker = nil and the default
// ServerMemoryLimitSessMinSize, EnableGCAwareMemoryTrack = false (GC-aware
// release depends on Go's GC; the Rust port releases at once, see the D15.1
// plan). Order-dependent results (children lists, SearchTrackerConsumedMoreThanNBytes,
// CountAllChildrenMemUse) are sorted.

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/util/memory"
	"github.com/pingcap/tidb/pkg/util/sqlkiller"
)

const memorySeed = 20261115

func init() {
	vec.Register(&vec.Domain{
		Name:     "memory",
		Seed:     memorySeed,
		Coverage: memoryCoverage,
		Blocks:   []string{"D15.1"},
		Exempt:   memoryExempt,
		Gzip:     true,
		Generate: memoryGenerate,
		Exec:     memoryExecOp,
	})
}

var memoryCoverage = map[string]vec.Coverage{
	"script": {Min: 1000, GoFuncs: []string{
		"NewTracker", "NewGlobalTracker", "InitTracker", "NewActionWithPriority",
		"Tracker.AttachTo", "Tracker.AttachToGlobalTracker", "Tracker.Detach", "Tracker.DetachFromGlobalTracker",
		"Tracker.remove", "Tracker.ReplaceChild", "Tracker.Consume", "Tracker.Release", "Tracker.shouldRecordRelease",
		"Tracker.BufferedConsume", "Tracker.BufferedRelease", "Tracker.SetBytesLimit", "Tracker.GetBytesLimit",
		"Tracker.CheckBytesLimit", "Tracker.CheckExceed", "Tracker.SetLabel", "Tracker.Label", "Tracker.Reset",
		"Tracker.ReplaceBytesUsed", "Tracker.ResetMaxConsumed", "Tracker.MaxConsumed", "Tracker.BytesConsumed",
		"Tracker.BytesReleased", "Tracker.String", "Tracker.toString", "Tracker.FormatBytes",
		"Tracker.CountAllChildrenMemUse", "countChildMem", "Tracker.SearchTrackerWithoutLock",
		"Tracker.SearchTrackerConsumedMoreThanNBytes", "Tracker.GetChildrenForTest", "Tracker.LessThan",
		"Tracker.HandleKillSignal", "Tracker.SetActionOnExceed", "Tracker.FallbackOldAndSetNewAction",
		"Tracker.FallbackOldAndSetNewActionForSoftLimit", "Tracker.UnbindActions", "Tracker.UnbindActionFromHardLimit",
		"Tracker.GetFallbackForTest", "reArrangeFallback", "Tracker.getParent", "Tracker.setParent",
		"BaseOOMAction.SetFallback", "BaseOOMAction.GetFallback", "BaseOOMAction.SetFinished",
		"BaseOOMAction.IsFinished", "BaseOOMAction.TriggerFallBackAction", "actionWithPriority.GetPriority",
		"LogOnExceed.Action", "LogOnExceed.GetPriority", "LogOnExceed.SetLogHook",
		"PanicOnExceed.Action", "PanicOnExceed.GetPriority", "PanicOnExceed.SetLogHook",
	}},
	"format_bytes":    {Min: 100, GoFuncs: []string{"FormatBytes", "getByteUnit"}},
	"bytes_to_string": {Min: 50, GoFuncs: []string{"BytesToString"}},
}

var memoryExempt = map[string]string{
	"Tracker.recordRelease":  "GC-aware release (EnableGCAwareMemoryTrack): the released bytes stay until Go's GC runs a finalizer; nondeterministic in Go, released at once in Rust (D15.1 plan §4); Rust unit test",
	"Tracker.release":        "GC-aware release, see Tracker.recordRelease",
	"GetMemTotalIgnoreErr":   "reads the machine's memory: environment-dependent; Rust unit tests",
	"InitMemoryHook":         "reads the machine's cgroup and memory: environment-dependent; Rust unit tests",
	"InstanceMemUsed":        "Go runtime heap statistics (jemalloc in Rust, A1 3.2): no common value",
	"MemTotalCGroup":         "reads the machine's cgroup: environment-dependent",
	"MemTotalNormal":         "reads the machine's memory: environment-dependent; Rust unit tests",
	"MemUsedCGroup":          "reads the machine's cgroup: environment-dependent",
	"MemUsedNormal":          "reads the machine's memory: environment-dependent; Rust unit tests",
	"init":                   "package init: chooses the meminfo hooks (environment-dependent)",
	"memInfoCache.get":       "meminfo cache (time-based); Rust unit tests",
	"memInfoCache.set":       "meminfo cache (time-based); Rust unit tests",
	"memTotalNormal":         "reads the machine's memory: environment-dependent",
	"ReadMemStats":           "Go runtime.ReadMemStats (jemalloc in Rust, A1 3.2): no common value",
	"ForceReadMemStats":      "Go runtime.ReadMemStats (jemalloc in Rust, A1 3.2): no common value",
}

// memMock is tracker_test.go's mockAction with a log: the first trigger marks
// it called, later ones run its (non-finished) fallback.
type memMock struct {
	memory.BaseOOMAction
	id       int
	called   bool
	priority int64
	st       *memState
}

func (a *memMock) Action(t *memory.Tracker) {
	if a.called {
		if fb := a.GetFallback(); fb != nil {
			fb.Action(t)
			return
		}
	}
	a.called = true
	a.st.fired = append(a.st.fired, fmt.Sprintf("mock:%d:%d", a.id, t.Label()))
}

func (a *memMock) GetPriority() int64 { return a.priority }

type memState struct {
	trackers map[int]*memory.Tracker
	actions  map[int]memory.ActionOnExceed
	killers  map[int]*sqlkiller.SQLKiller
	bufs     map[int]*int64
	fired    []string
}

func (st *memState) tid(t *memory.Tracker) any {
	if t == nil {
		return nil
	}
	for id, x := range st.trackers {
		if x == t {
			return id
		}
	}
	return "unknown"
}

func (st *memState) aid(a memory.ActionOnExceed) any {
	if a == nil {
		return nil
	}
	for id, x := range st.actions {
		if x == a {
			return id
		}
	}
	return "unknown"
}

func (st *memState) t(args []int64, i int) *memory.Tracker {
	if args[i] < 0 {
		return nil
	}
	return st.trackers[int(args[i])]
}

func memErr(err error) any {
	if err == nil {
		return nil
	}
	e := map[string]any{"msg": vec.GoString(err.Error())}
	if te, ok := errors.Cause(err).(*errors.Error); ok {
		e["code"] = int(te.Code())
	}
	return e
}

// memStep runs one step; a Go panic propagates to memScript.
func (st *memState) step(op string, a []int64) any {
	switch op {
	case "new":
		st.trackers[int(a[0])] = memory.NewTracker(int(a[1]), a[2])
	case "new_global":
		st.trackers[int(a[0])] = memory.NewGlobalTracker(int(a[1]), a[2])
	case "init":
		t := &memory.Tracker{}
		var act memory.ActionOnExceed
		if a[3] >= 0 {
			act = st.actions[int(a[3])]
		}
		memory.InitTracker(t, int(a[1]), a[2], act)
		st.trackers[int(a[0])] = t
	case "attach":
		st.t(a, 0).AttachTo(st.t(a, 1))
	case "attach_global":
		st.t(a, 0).AttachToGlobalTracker(st.t(a, 1))
	case "detach":
		st.t(a, 0).Detach()
	case "detach_global":
		st.t(a, 0).DetachFromGlobalTracker()
	case "replace":
		st.t(a, 0).ReplaceChild(st.t(a, 1), st.t(a, 2))
	case "consume":
		st.t(a, 0).Consume(a[1])
	case "release":
		st.t(a, 0).Release(a[1])
	case "buf_consume", "buf_release":
		b := st.bufs[int(a[1])]
		if b == nil {
			b = new(int64)
			st.bufs[int(a[1])] = b
		}
		if op == "buf_consume" {
			st.t(a, 0).BufferedConsume(b, a[2])
		} else {
			st.t(a, 0).BufferedRelease(b, a[2])
		}
		return *b
	case "set_limit":
		st.t(a, 0).SetBytesLimit(a[1])
	case "set_label":
		st.t(a, 0).SetLabel(int(a[1]))
	case "reset":
		st.t(a, 0).Reset()
	case "replace_bytes":
		st.t(a, 0).ReplaceBytesUsed(a[1])
	case "reset_max":
		st.t(a, 0).ResetMaxConsumed()
	case "check_exceed":
		return st.t(a, 0).CheckExceed()
	case "check_limit":
		return st.t(a, 0).CheckBytesLimit(a[1])
	case "get_limit":
		return st.t(a, 0).GetBytesLimit()
	case "string":
		return st.t(a, 0).String()
	case "count_children":
		m := st.t(a, 0).CountAllChildrenMemUse()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([][]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, []any{k, m[k]})
		}
		return out
	case "search_label":
		return st.tid(st.t(a, 0).SearchTrackerWithoutLock(int(a[1])))
	case "search_more":
		return st.sortedIDs(st.t(a, 0).SearchTrackerConsumedMoreThanNBytes(a[1]))
	case "children":
		return st.sortedIDs(st.t(a, 0).GetChildrenForTest())
	case "less":
		return st.t(a, 0).LessThan(st.t(a, 1))
	case "sess_root":
		t := st.t(a, 0)
		t.IsRootTrackerOfSess = true
		t.Killer = st.killers[int(a[1])]
	case "session_id":
		st.t(a, 0).SessionID.Store(uint64(a[1]))
	case "killer":
		k := &sqlkiller.SQLKiller{}
		k.ConnID.Store(uint64(a[1]))
		st.killers[int(a[0])] = k
	case "kill":
		st.killers[int(a[0])].SendKillSignal(uint32(a[1]))
	case "killer_reset":
		st.killers[int(a[0])].Reset()
	case "handle_kill":
		st.t(a, 0).HandleKillSignal()
	case "killer_handle":
		return memErr(st.killers[int(a[0])].HandleSignal())
	case "act":
		id := int(a[0])
		switch a[1] {
		case 0: // mock
			st.actions[id] = &memMock{id: id, priority: a[2], st: st}
		case 1: // log
			l := &memory.LogOnExceed{ConnID: uint64(a[2])}
			l.SetLogHook(func(c uint64) { st.fired = append(st.fired, fmt.Sprintf("log:%d:%d", id, c)) })
			st.actions[id] = l
		case 2: // panic
			p := &memory.PanicOnExceed{Killer: st.killers[int(a[2])], ConnID: uint64(100 + a[2])}
			p.SetLogHook(func(c uint64) { st.fired = append(st.fired, fmt.Sprintf("panic:%d:%d", id, c)) })
			st.actions[id] = p
		}
	case "act_prio":
		st.actions[int(a[0])] = memory.NewActionWithPriority(st.actions[int(a[1])], a[2])
	case "set_action":
		var act memory.ActionOnExceed
		if a[1] >= 0 {
			act = st.actions[int(a[1])]
		}
		st.t(a, 0).SetActionOnExceed(act)
	case "fallback":
		st.t(a, 0).FallbackOldAndSetNewAction(st.actions[int(a[1])])
	case "fallback_soft":
		st.t(a, 0).FallbackOldAndSetNewActionForSoftLimit(st.actions[int(a[1])])
	case "unbind":
		st.t(a, 0).UnbindActions()
	case "unbind_hard":
		st.t(a, 0).UnbindActionFromHardLimit(st.actions[int(a[1])])
	case "finish":
		st.actions[int(a[0])].SetFinished()
	case "is_finished":
		return st.actions[int(a[0])].IsFinished()
	case "get_fallback":
		return st.aid(st.actions[int(a[0])].GetFallback())
	case "chain":
		var ids []any
		for cur := st.t(a, 0).GetFallbackForTest(a[1] != 0); cur != nil; cur = cur.GetFallback() {
			ids = append(ids, st.aid(cur))
			if len(ids) > 200 {
				break
			}
		}
		return ids
	case "set_fallback":
		var fb memory.ActionOnExceed
		if a[1] >= 0 {
			fb = st.actions[int(a[1])]
		}
		st.actions[int(a[0])].SetFallback(fb)
	case "trigger_fallback":
		st.actions[int(a[0])].(*memMock).TriggerFallBackAction(st.t(a, 1))
	case "sess_min":
		memory.ServerMemoryLimitSessMinSize.Store(uint64(a[0]))
	case "top1":
		return st.tid(memory.MemUsageTop1Tracker.Load())
	case "dump":
		ids := make([]int, 0, len(st.trackers))
		for id := range st.trackers {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		out := make([][]any, 0, len(ids))
		for _, id := range ids {
			t := st.trackers[id]
			out = append(out, []any{id, t.Label(), t.BytesConsumed(), t.BytesReleased(), t.MaxConsumed(),
				t.GetBytesLimit(), t.CheckExceed(), st.sortedIDs(t.GetChildrenForTest())})
		}
		return out
	default:
		panic("memory: unknown step " + op)
	}
	return nil
}

func (st *memState) sortedIDs(ts []*memory.Tracker) []any {
	ids := make([]int, 0, len(ts))
	for _, t := range ts {
		if id, ok := st.tid(t).(int); ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// memStepOut is one step's record entry.
type memStepOut struct {
	R     any      `json:"r"`
	Fired []string `json:"fired"`
}

func memScript(steps [][]json.RawMessage) (out []any) {
	memory.MemUsageTop1Tracker.Store(nil)
	memory.ServerMemoryLimitSessMinSize.Store(128 << 20)
	memory.EnableGCAwareMemoryTrack.Store(false)
	st := &memState{
		trackers: map[int]*memory.Tracker{},
		actions:  map[int]memory.ActionOnExceed{},
		killers:  map[int]*sqlkiller.SQLKiller{},
		bufs:     map[int]*int64{},
	}
	defer memory.MemUsageTop1Tracker.Store(nil)
	for _, s := range steps {
		var op string
		if err := json.Unmarshal(s[0], &op); err != nil {
			panic(err)
		}
		args := make([]int64, len(s)-1)
		for i, r := range s[1:] {
			if err := json.Unmarshal(r, &args[i]); err != nil {
				panic(err)
			}
		}
		st.fired = nil
		r, perr := func() (r any, perr any) {
			defer func() {
				if p := recover(); p != nil {
					perr = p
				}
			}()
			return st.step(op, args), nil
		}()
		fired := st.fired
		if fired == nil {
			fired = []string{}
		}
		if perr != nil {
			var pv any
			if err, ok := perr.(error); ok {
				pv = map[string]any{"panic": memErr(err)}
			} else {
				pv = map[string]any{"panic": vec.GoString(fmt.Sprint(perr))}
			}
			out = append(out, memStepOut{R: pv, Fired: fired})
			return out
		}
		out = append(out, memStepOut{R: r, Fired: fired})
	}
	return out
}

func memoryExecOp(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "script":
		var steps [][]json.RawMessage
		if err := json.Unmarshal(args[0], &steps); err != nil {
			return nil, err
		}
		return memScript(steps), nil
	case "format_bytes", "bytes_to_string":
		var n int64
		if err := json.Unmarshal(args[0], &n); err != nil {
			return nil, err
		}
		if op == "format_bytes" {
			return memory.FormatBytes(n), nil
		}
		return memory.BytesToString(n), nil
	}
	return nil, fmt.Errorf("memory: unknown op %q", op)
}

// memS builds one step.
func memS(op string, args ...int64) []any {
	r := []any{op}
	for _, a := range args {
		r = append(r, a)
	}
	return r
}

func memoryGenerate(g *vec.Gen, rng *rand.Rand) {
	script := func(src string, steps ...[]any) { g.Add(src, "script", steps) }
	const gb, mb, kb = 1 << 30, 1 << 20, 1 << 10

	// tracker_test.go, as scripts.
	script("TestSetLabel", memS("new", 1, 1, -1), memS("dump"), memS("set_label", 1, 2), memS("dump"))
	script("TestSetLabel2", memS("new", 1, 1, -1), memS("new", 2, 2, -1), memS("attach", 2, 1), memS("consume", 2, 10),
		memS("dump"), memS("set_label", 2, 10), memS("dump"), memS("detach", 2), memS("dump"))
	script("TestConsume", memS("new", 1, 1, -1), memS("consume", 1, 100), memS("consume", 1, 10), memS("consume", 1, -10), memS("dump"))
	script("TestRelease", memS("new_global", 1, int64(memory.LabelForGlobalAnalyzeMemory), -1), memS("new", 2, 1, -1),
		memS("attach_global", 2, 1), memS("consume", 2, 100), memS("dump"), memS("release", 2, 100), memS("dump"))
	script("TestBufferedConsumeAndRelease", memS("new_global", 1, int64(memory.LabelForGlobalAnalyzeMemory), -1), memS("new", 2, 1, -1),
		memS("attach_global", 2, 1), memS("buf_consume", 2, 0, memory.TrackMemWhenExceeds/2), memS("dump"),
		memS("buf_consume", 2, 0, memory.TrackMemWhenExceeds/2), memS("dump"),
		memS("buf_release", 2, 1, memory.TrackMemWhenExceeds/2), memS("dump"),
		memS("buf_release", 2, 1, memory.TrackMemWhenExceeds/2), memS("dump"))
	script("TestOOMAction",
		memS("new", 1, 1, 100), memS("consume", 1, 10000),
		memS("new", 2, 1, 100), memS("act", 1, 0, 0), memS("set_action", 2, 1), memS("consume", 2, 10000),
		memS("act", 2, 0, 0), memS("act", 3, 0, 0), memS("set_action", 2, 2), memS("fallback", 2, 3),
		memS("consume", 2, 10000), memS("consume", 2, 10000),
		memS("new", 3, 1, 100), memS("act", 4, 0, 0), memS("act", 5, 0, 0), memS("act", 6, 0, 0),
		memS("set_action", 3, 4), memS("fallback_soft", 3, 5), memS("fallback_soft", 3, 6),
		memS("consume", 3, 80), memS("consume", 3, 20),
		memS("act", 7, 0, 0), memS("act", 8, 0, 0), memS("act", 9, 0, 0), memS("act", 10, 0, 0), memS("act", 11, 0, 0),
		memS("set_action", 3, 7), memS("fallback", 3, 8), memS("fallback", 3, 9), memS("fallback", 3, 10), memS("fallback", 3, 11),
		memS("chain", 3, 0), memS("get_fallback", 11), memS("finish", 10), memS("get_fallback", 11),
		memS("finish", 9), memS("finish", 8), memS("get_fallback", 11), memS("chain", 3, 1))
	script("TestAttachTo", memS("new", 1, 1, -1), memS("new", 2, 2, -1), memS("new", 3, 3, -1), memS("consume", 3, 100),
		memS("attach", 3, 1), memS("dump"), memS("attach", 3, 2), memS("dump"))
	script("TestDetach", memS("new", 1, 1, -1), memS("new", 2, 2, -1), memS("consume", 2, 100), memS("attach", 2, 1),
		memS("dump"), memS("detach", 2), memS("dump"))
	script("TestReplaceChild", memS("new", 1, 1, -1), memS("consume", 1, 100), memS("new", 2, 2, -1), memS("consume", 2, 500),
		memS("new", 3, 3, -1), memS("attach", 1, 3), memS("dump"), memS("replace", 3, 1, 2), memS("dump"),
		memS("replace", 3, 1, -1), memS("dump"), memS("replace", 3, 2, -1), memS("dump"),
		memS("new", 4, 1, -1), memS("new", 5, 2, -1), memS("new", 6, 3, -1), memS("attach", 5, 4), memS("attach", 6, 5),
		memS("consume", 6, 100), memS("dump"), memS("replace", 5, 6, -1), memS("dump"))
	script("TestToString", memS("new", 1, 1, -1), memS("new", 2, 2, 1000), memS("new", 3, 3, -1), memS("new", 4, 4, -1),
		memS("new", 5, 5, -1), memS("attach", 2, 1), memS("attach", 3, 1), memS("attach", 4, 1), memS("attach", 5, 1),
		memS("consume", 2, 100), memS("consume", 3, 2*kb), memS("consume", 4, 3*mb), memS("consume", 5, 4*gb), memS("string", 1))
	{
		steps := [][]any{memS("new", 1, 1, -1), memS("new", 2, 2, -1), memS("new", 3, 3, -1), memS("new", 4, 4, -1),
			memS("attach", 2, 1), memS("attach", 3, 1), memS("attach", 4, 2)}
		consumed := int64(0)
		for range 30 {
			b := rng.Int64N(1000) - 500
			if consumed+b < 0 {
				b = -consumed
			}
			consumed += b
			steps = append(steps, memS("consume", 1+rng.Int64N(4), b), memS("dump"))
		}
		script("TestMaxConsumed", steps...)
	}
	script("TestGlobalTracker", memS("new_global", 1, 1, -1), memS("new", 2, 2, -1), memS("new", 3, 3, -1),
		memS("consume", 2, 100), memS("consume", 3, 200), memS("attach_global", 2, 1), memS("attach_global", 3, 1), memS("dump"),
		memS("detach_global", 2), memS("detach_global", 3), memS("dump"), memS("new", 4, 4, -1), memS("attach_global", 2, 4))
	script("TestGlobalTracker", memS("new_global", 1, 1, -1), memS("new", 2, 2, -1), memS("new", 3, 3, -1), memS("new", 4, 4, -1),
		memS("consume", 2, 100), memS("attach", 2, 4), memS("dump"), memS("attach_global", 2, 1), memS("dump"),
		memS("attach", 3, 4), memS("detach_global", 3))
	{
		steps := [][]any{memS("new", 1, 1, 100), memS("consume", 1, 10000), memS("new", 2, 1, 1), memS("set_action", 2, -1)}
		perm := rng.Perm(100)
		for i := range 100 {
			steps = append(steps, memS("act", int64(i), 0, int64(i)))
		}
		for _, i := range perm {
			steps = append(steps, memS("fallback", 2, int64(i)))
		}
		steps = append(steps, memS("chain", 2, 0))
		for range 100 {
			steps = append(steps, memS("consume", 2, 100))
		}
		steps = append(steps, memS("chain", 2, 1))
		script("TestOOMActionPriority", steps...)
	}

	// Kill signals, sessions, the Top1 tracker, LOG / CANCEL actions.
	for sig := int64(1); sig <= 5; sig++ {
		script("sys/kill", memS("killer", 0, 42), memS("killer_handle", 0), memS("kill", 0, sig), memS("kill", 0, 1),
			memS("killer_handle", 0), memS("new", 1, int64(memory.LabelForSession), -1), memS("sess_root", 1, 0),
			memS("consume", 1, 10), memS("consume", 1, -5), memS("handle_kill", 1), memS("killer_reset", 0), memS("killer_handle", 0),
			memS("consume", 1, 10), memS("dump"))
	}
	script("sys/session", memS("killer", 0, 7), memS("new", 1, int64(memory.LabelForSession), 1000), memS("sess_root", 1, 0),
		memS("session_id", 1, 7), memS("act", 0, 2, 0), memS("set_action", 1, 0), memS("new", 2, 2, -1), memS("attach", 2, 1),
		memS("consume", 2, 999), memS("consume", 2, 1), memS("consume", 2, 1), memS("killer_handle", 0))
	script("sys/session", memS("killer", 0, 8), memS("new", 1, int64(memory.LabelForSession), 1000), memS("sess_root", 1, 0),
		memS("act", 0, 1, 8), memS("set_action", 1, 0), memS("new", 2, 2, -1), memS("attach", 2, 1),
		memS("consume", 2, 2000), memS("consume", 2, 2000), memS("new", 3, int64(memory.LabelForMemDB), -1), memS("attach", 3, 1),
		memS("consume", 3, 10), memS("detach", 3), memS("chain", 1, 0), memS("detach", 2), memS("chain", 1, 0), memS("dump"))
	script("sys/top1", memS("sess_min", 100), memS("killer", 0, 1), memS("killer", 1, 2),
		memS("new", 1, int64(memory.LabelForSession), -1), memS("sess_root", 1, 0), memS("new", 2, int64(memory.LabelForSession), -1),
		memS("sess_root", 2, 1), memS("top1"), memS("consume", 1, 50), memS("top1"), memS("consume", 1, 100), memS("top1"),
		memS("consume", 2, 120), memS("top1"), memS("consume", 2, 100), memS("top1"), memS("consume", 1, -150), memS("top1"),
		memS("consume", 1, 1000), memS("top1"), memS("less", 1, 2), memS("less", 2, 1), memS("less", -1, 1), memS("less", 1, -1))
	script("sys/limits", memS("new", 1, 1, memory.DefMemQuotaQuery), memS("get_limit", 1),
		memS("check_limit", 1, memory.DefMemQuotaQuery), memS("set_limit", 1, 0), memS("get_limit", 1),
		memS("set_limit", 1, memory.DefMemQuotaQuery), memS("consume", 1, memory.DefMemQuotaQuery*8/10), memS("check_exceed", 1),
		memS("set_limit", 1, 7), memS("check_exceed", 1), memS("set_limit", 1, -5), memS("check_exceed", 1),
		memS("init", 2, 9, 0, -1), memS("dump"), memS("init", 3, 9, memory.DefMemQuotaQuery, -1), memS("get_limit", 3),
		memS("act", 0, 0, 3), memS("init", 4, 9, 123, 0), memS("consume", 4, 99), memS("consume", 4, 24), memS("chain", 4, 0))
	script("sys/soft", memS("new", 1, 1, 101), memS("act", 0, 0, 2), memS("act", 1, 0, 0), memS("set_action", 1, 1),
		memS("fallback_soft", 1, 0), memS("consume", 1, 80), memS("consume", 1, 1), memS("consume", 1, 19), memS("consume", 1, 1),
		memS("unbind", 1), memS("consume", 1, 100), memS("chain", 1, 0))
	script("sys/unbind", memS("new", 1, 1, 10), memS("act", 0, 0, 0), memS("act", 1, 0, 1), memS("act", 2, 0, 2),
		memS("act", 3, 0, 3), memS("set_action", 1, -1), memS("fallback", 1, 0), memS("fallback", 1, 2), memS("fallback", 1, 1),
		memS("fallback", 1, 3), memS("chain", 1, 0), memS("unbind_hard", 1, 2), memS("chain", 1, 0), memS("unbind_hard", 1, 3),
		memS("chain", 1, 0), memS("unbind_hard", 1, 0), memS("chain", 1, 0), memS("unbind_hard", 1, 9), memS("chain", 1, 0))
	script("sys/prio", memS("new", 1, 1, 10), memS("act", 0, 1, 5), memS("act_prio", 1, 0, 9), memS("act", 2, 0, 3),
		memS("set_action", 1, 2), memS("fallback", 1, 1), memS("chain", 1, 0), memS("consume", 1, 20), memS("consume", 1, 20),
		memS("is_finished", 1), memS("finish", 1), memS("is_finished", 0), memS("chain", 1, 1), memS("consume", 1, 20))
	script("sys/trigger", memS("new", 1, 1, -1), memS("act", 0, 0, 1), memS("act", 1, 0, 2), memS("act", 2, 0, 3),
		memS("trigger_fallback", 0, 1), memS("set_fallback", 0, 1), memS("trigger_fallback", 0, 1),
		memS("trigger_fallback", 0, 1), memS("set_fallback", 1, 2), memS("trigger_fallback", 0, 1),
		memS("finish", 1), memS("get_fallback", 0), memS("trigger_fallback", 0, 1), memS("set_fallback", 0, -1),
		memS("get_fallback", 0))
	script("sys/strings", memS("new", 1, 1, 5*gb+3), memS("new", 2, 2, -1), memS("new", 3, 2, 999), memS("new", 4, -3, kb+1),
		memS("attach", 2, 1), memS("attach", 3, 1), memS("attach", 4, 2), memS("consume", 4, 7*mb+12345), memS("consume", 3, 1023),
		memS("consume", 2, 1), memS("string", 1), memS("string", 2), memS("count_children", 1), memS("search_label", 1, 2),
		memS("search_label", 1, 1), memS("search_label", 1, 99), memS("search_label", 2, -3), memS("search_more", 1, 1000),
		memS("search_more", 1, -1), memS("children", 1), memS("children", 4))
	script("sys/reset", memS("new", 1, 1, -1), memS("new", 2, 2, -1), memS("new", 3, 3, -1), memS("attach", 2, 1),
		memS("attach", 3, 2), memS("consume", 3, 500), memS("reset_max", 2), memS("consume", 2, -100), memS("dump"),
		memS("replace_bytes", 3, 50), memS("dump"), memS("reset", 2), memS("dump"), memS("set_label", 3, 7), memS("dump"))

	// Random scripts over a forest of trackers.
	for range 1500 {
		script("rand/script", memRandScript(rng)...)
	}

	// FormatBytes / BytesToString: TestFormatBytesWithPrune and boundaries.
	// The case table of TestFormatBytesWithPrune, through the test's parseByte.
	for _, c := range []string{"0 Bytes", "1 Bytes", "9 Bytes", "10 Bytes", "999 Bytes", "1 KB", "1.123 KB",
		"1.023 KB", "1.003 KB", "10.456 KB", "10.956 KB", "999.056 KB", "999.988 KB", "1.123 MB", "1.023 MB",
		"1.003 MB", "10.456 MB", "10.956 MB", "999.056 MB", "999.988 MB", "1.123 GB", "1.023 GB", "1.003 GB",
		"10.456 GB", "10.956 GB", "9.412345 MB", "10.412345 MB", "5.999 GB", "100.46 KB",
		"18.399999618530273 MB", "9.15999984741211 MB"} {
		g.Add("TestFormatBytesWithPrune", "format_bytes", memParseByte(c))
	}
	for _, n := range []int64{-1, -1024, -5 * gb, kb - 1, kb, kb + 1, mb - 1, mb, mb + 1, gb - 1, gb, gb + 1,
		10 * kb, 10*kb + 1, 10*mb - 1, 10 * mb, 100 * gb, 1 << 62, -(1 << 62), 9223372036854775807} {
		g.Add("sys/bytes", "format_bytes", n)
		g.Add("sys/bytes", "bytes_to_string", n)
	}
	for range 200 {
		n := rng.Int64N(1 << uint(rng.IntN(42)+1))
		g.Add("rand/bytes", "format_bytes", n)
		if rng.IntN(4) == 0 {
			g.Add("rand/bytes", "bytes_to_string", n)
		}
	}
}

// memRandScript builds a random acyclic script: trackers 1..n with labels,
// limits and a forest of parent links, plus actions, a session root, and a
// random mix of the steps.
func memRandScript(rng *rand.Rand) [][]any {
	n := 3 + rng.IntN(4)
	var steps [][]any
	parent := make([]int, n+1) // 0: none
	global := 0
	labels := make([]int64, n+1)
	for i := 1; i <= n; i++ {
		labels[i] = int64(rng.IntN(5) - 2)
		limit := int64(-1)
		switch rng.IntN(4) {
		case 0:
			limit = int64(rng.IntN(5000))
		case 1:
			limit = int64(rng.IntN(300) + 1)
		}
		if i == n && rng.IntN(3) == 0 {
			steps = append(steps, memS("new_global", int64(i), labels[i], limit))
			global = i
		} else if rng.IntN(5) == 0 {
			steps = append(steps, memS("init", int64(i), labels[i], limit, -1))
		} else {
			steps = append(steps, memS("new", int64(i), labels[i], limit))
		}
	}
	// Actions are fresh for every binding (a chain is a linked list: binding
	// one action into two chains, or twice, would make a cycle).
	steps = append(steps, memS("killer", 0, 77))
	nact := 0
	newAct := func() int64 {
		id := int64(nact)
		nact++
		switch rng.IntN(5) {
		case 0:
			steps = append(steps, memS("act", id, 1, id))
		case 1:
			steps = append(steps, memS("act", id, 2, 0))
		default:
			steps = append(steps, memS("act", id, 0, int64(rng.IntN(5))))
		}
		return id
	}
	isAncestor := func(a, b int) bool { // a is an ancestor of b (or equal)
		for x := b; x != 0; x = parent[x] {
			if x == a {
				return true
			}
		}
		return false
	}
	root := 0
	if rng.IntN(3) == 0 {
		root = 1 + rng.IntN(n)
		if root == global {
			root = 0
		} else {
			steps = append(steps, memS("sess_root", int64(root), 0))
		}
	}
	if rng.IntN(2) == 0 {
		steps = append(steps, memS("sess_min", int64(rng.IntN(400))))
	}
	m := 10 + rng.IntN(30)
	for range m {
		t := 1 + rng.IntN(n)
		switch k := rng.IntN(20); {
		case k < 5:
			steps = append(steps, memS("consume", int64(t), int64(rng.IntN(400)-120)))
		case k < 7:
			p := 1 + rng.IntN(n)
			if t == global || isAncestor(t, p) {
				continue
			}
			if p == global {
				steps = append(steps, memS("attach_global", int64(t), int64(p)))
			} else {
				steps = append(steps, memS("attach", int64(t), int64(p)))
			}
			parent[t] = p
		case k == 7:
			if parent[t] == global && global != 0 {
				steps = append(steps, memS("detach_global", int64(t)))
			} else {
				steps = append(steps, memS("detach", int64(t)))
			}
			parent[t] = 0
		case k == 8:
			a := newAct()
			if rng.IntN(2) == 0 {
				steps = append(steps, memS("fallback", int64(t), a))
			} else {
				steps = append(steps, memS("fallback_soft", int64(t), a))
			}
		case k == 9:
			a := int64(-1)
			if rng.IntN(4) != 0 {
				a = newAct()
			}
			steps = append(steps, memS("set_action", int64(t), a))
		case k == 10:
			if nact > 0 {
				steps = append(steps, memS("finish", int64(rng.IntN(nact))))
			}
		case k == 11:
			steps = append(steps, memS("chain", int64(t), int64(rng.IntN(2))))
		case k == 12:
			l := int64(-1)
			if rng.IntN(2) == 0 {
				l = int64(rng.IntN(3000))
			}
			steps = append(steps, memS("set_limit", int64(t), l))
		case k == 13:
			if t != global {
				labels[t] = int64(rng.IntN(5) - 2)
				steps = append(steps, memS("set_label", int64(t), labels[t]))
			}
		case k == 14:
			// ReplaceChild of a child of t with another tracker (or nil).
			var kids []int
			for c := 1; c <= n; c++ {
				if parent[c] == t && t != global {
					kids = append(kids, c)
				}
			}
			if len(kids) == 0 {
				continue
			}
			old := kids[rng.IntN(len(kids))]
			nw := 1 + rng.IntN(n)
			if rng.IntN(3) == 0 || nw == global || isAncestor(nw, t) || parent[nw] != 0 {
				steps = append(steps, memS("replace", int64(t), int64(old), -1))
				parent[old] = 0
			} else {
				steps = append(steps, memS("replace", int64(t), int64(old), int64(nw)))
				parent[old] = 0
				parent[nw] = t
			}
		case k == 15:
			steps = append(steps, memS("replace_bytes", int64(t), int64(rng.IntN(500))))
		case k == 16:
			if t == root || t == global {
				continue
			}
			var hasKids bool
			for c := 1; c <= n; c++ {
				hasKids = hasKids || parent[c] == t
			}
			if hasKids {
				continue
			}
			steps = append(steps, memS("reset", int64(t)))
			parent[t] = 0
		case k == 17:
			steps = append(steps, memS("buf_consume", int64(t), int64(t), int64(rng.IntN(3))*memory.TrackMemWhenExceeds/2))
		case k == 18:
			if rng.IntN(4) == 0 {
				steps = append(steps, memS("kill", 0, int64(rng.IntN(5)+1)))
			} else {
				steps = append(steps, memS("top1"))
			}
		default:
			steps = append(steps, memS("reset_max", int64(t)))
		}
	}
	steps = append(steps, memS("dump"))
	if t := 1 + rng.IntN(n); t != global {
		steps = append(steps, memS("string", int64(t)), memS("count_children", int64(t)))
	}
	return steps
}

// memParseByte is tracker_test.go's parseByte.
func memParseByte(str string) int64 {
	vBuf := make([]byte, 0, len(str))
	uBuf := make([]byte, 0, 2)
	for _, v := range str {
		if (v >= '0' && v <= '9') || v == '.' {
			vBuf = append(vBuf, byte(v))
		} else if v != ' ' {
			uBuf = append(uBuf, byte(v))
		}
	}
	unit := map[string]int64{"GB": 1 << 30, "MB": 1 << 20, "KB": 1 << 10, "Bytes": 1}[string(uBuf)]
	v, err := strconv.ParseFloat(string(vBuf), 64)
	if err != nil || unit == 0 {
		panic(str)
	}
	return int64(v * float64(unit))
}
