package domains

// Domain "execdetails": block D14.0 — pkg/util/execdetails (execution details, runtime
// stats, P90 percentiles), pkg/util/tracing/opt_trace.go (the optimizer trace and its JSON),
// and what they embed: client-go util detail types (String / Merge / Clone, FormatBytes),
// influxdata/tdigest (the percentile after 1000 values) and Go's math.Sin / math.Asin
// (tdigest's scale function).
//
// Durations are inputs, never read from the clock. Every map that Go iterates in random
// order only feeds order-independent results; outputs that are maps are written sorted.
//
// Records (a Go panic is the record's error):
//   - "fmt": args [f, n]: f = "fd" (FormatDuration), "cgfd" (client-go FormatDuration),
//     "fb" (client-go FormatBytes), "bts" (client-go BytesToString).
//   - "gomath": args [f, x] (x a float string): math.Sin / math.Asin bits as hex.
//   - "tdigest": args [steps]: ["add", x, w] | ["list", [[mean, weight]...]] (AddCentroidList)
//     | ["gen", kind, n, seed, weights] (n Adds of edGenValue; weights "1" | "int" | "frac")
//     | ["q", q] (Quantile, float string) | ["cent"] (Centroids). out: one entry per step.
//   - "percentile": args [steps]: ["add", d, addr] | ["p", f] | ["max"] | ["min"] | ["size"]
//     | ["sum"] | ["merge", steps] (MergePercentile with a percentile built by `steps`).
//   - "details": args [execDetails] -> String, ToZapFields.
//   - "cgdetails": args [commit|null, lock|null, tikv] -> client-go String / Clone / Merge results.
//   - "sync": args [steps] over a SyncExecDetails: ["merge", ed|null, commit|null] |
//     ["cop", cop|null, copTime] | ["lock", lk] | ["slock", lk] | ["reset"] | ["get"] |
//     ["tasks"] | ["sum"].
//   - "coll": args [steps] over a RuntimeStatsColl: ["basic", id, d, rows] | ["setrows", id, n]
//     | ["reg", id, stats] | ["cop", id, storeType, scan|null, time, summary|null] |
//     ["one", id, storeType, summary] | ["root", id] | ["copstr", id] | ["coprows", id] |
//     ["actrows", id] | ["exists", id] | ["new"] (NewRuntimeStatsColl(reuse)) |
//     ["bcop", id, other|null] (a Clone of the cop stats' basicCopRuntimeStats: String, Tp,
//     then Merge of a clone of plan `other`'s (or of a BasicRuntimeStats: a no-op), String,
//     and the String of a Clone taken before the Merge).
//   - "stats": args [a, b] (RuntimeStats payloads) -> a.String / Tp, Clone().String, after
//     a.Merge(b): a.String and the clone's String (unchanged).
//   - "opttrace": args [steps] over an OptimizeTracer (logical / physical / final), see
//     execOptTrace; out: JSON (or the marshal error) after each step.
//   - "ce": args [records] -> DedupCETrace, JSON.
//   - "codec": args [tp, id] -> CodecPlanName.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/influxdata/tdigest"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/util/execdetails"
	"github.com/pingcap/tidb/pkg/util/tracing"
	"github.com/pingcap/tipb/go-tipb"
	"github.com/tikv/client-go/v2/util"
	"go.uber.org/zap/zapcore"
)

const execdetailsSeed = 20261111

//go:linkname basicCopClone github.com/pingcap/tidb/pkg/util/execdetails.(*basicCopRuntimeStats).Clone
func basicCopClone(p unsafe.Pointer) execdetails.RuntimeStats

func init() {
	vec.Register(&vec.Domain{
		Name:     "execdetails",
		Seed:     execdetailsSeed,
		Coverage: execdetailsCoverage,
		Blocks:   []string{"D14.0"},
		Exempt:   execdetailsExempt(),
		Gzip:     true,
		Generate: execdetailsGenerate,
		Exec:     execdetailsExec,
	})
}

// ExecdetailsMinedTests: Go test scenarios replayed as records.
func ExecdetailsMinedTests() []string {
	return []string{"TestString", "TestCopRuntimeStats", "TestRuntimeStatsWithCommit", "TestRootRuntimeStats",
		"TestFormatDurationForExplain"}
}

var execdetailsCoverage = map[string]vec.Coverage{
	"fmt":        {Min: 200, GoFuncs: []string{"FormatDuration", "getUnit"}},
	"gomath":     {Min: 200},
	"tdigest":    {Min: 100},
	"percentile": {Min: 100, GoFuncs: []string{"Percentile.Add", "Percentile.GetMax", "Percentile.GetMin", "Percentile.GetPercentile", "Percentile.MergePercentile", "Percentile.Size", "Percentile.Sum", "Duration.GetFloat64", "DurationWithAddr.GetFloat64", "Int64.GetFloat64"}},
	"details":    {Min: 100, GoFuncs: []string{"ExecDetails.String", "ExecDetails.ToZapFields"}},
	"cgdetails":  {Min: 100},
	"sync": {Min: 200, GoFuncs: []string{"SyncExecDetails.MergeExecDetails", "SyncExecDetails.MergeCopExecDetails",
		"SyncExecDetails.MergeLockKeysExecDetails", "SyncExecDetails.MergeSharedLockKeysExecDetails", "SyncExecDetails.Reset",
		"SyncExecDetails.GetExecDetails", "SyncExecDetails.CopTasksDetails", "SyncExecDetails.CopTasksSummary",
		"SyncExecDetails.mergeScanDetail", "SyncExecDetails.mergeTimeDetail", "P90Summary.Merge", "P90Summary.Reset",
		"CopTasksDetails.ToZapFields"}},
	"coll": {Min: 300, GoFuncs: []string{"NewRuntimeStatsColl", "RuntimeStatsColl.RegisterStats", "RuntimeStatsColl.GetBasicRuntimeStats",
		"RuntimeStatsColl.GetRootStats", "RuntimeStatsColl.GetPlanActRows", "RuntimeStatsColl.GetCopStats",
		"RuntimeStatsColl.GetCopCountAndRows", "RuntimeStatsColl.RecordCopStats", "RuntimeStatsColl.RecordOneCopTask",
		"RuntimeStatsColl.ExistsRootStats", "RuntimeStatsColl.ExistsCopStats", "getPlanIDFromExecutionSummary",
		"NewRootRuntimeStats", "RootRuntimeStats.GetActRows", "RootRuntimeStats.MergeStats", "RootRuntimeStats.String",
		"BasicRuntimeStats.Record", "BasicRuntimeStats.SetRowNum", "BasicRuntimeStats.GetActRows", "BasicRuntimeStats.GetTime",
		"CopRuntimeStats.GetActRows", "CopRuntimeStats.GetTasks", "CopRuntimeStats.String",
		"basicCopRuntimeStats.mergeExecSummary", "basicCopRuntimeStats.String", "basicCopRuntimeStats.Clone",
		"basicCopRuntimeStats.Merge", "basicCopRuntimeStats.Tp",
		"TiFlashScanContext.Clone", "TiFlashScanContext.Empty", "TiFlashScanContext.Merge", "TiFlashScanContext.String",
		"TiFlashWaitSummary.CanBeIgnored", "TiFlashWaitSummary.Clone", "TiFlashWaitSummary.Merge", "TiFlashWaitSummary.String"}},
	"stats": {Min: 200, GoFuncs: []string{"BasicRuntimeStats.String", "BasicRuntimeStats.Clone", "BasicRuntimeStats.Merge", "BasicRuntimeStats.Tp",
		"RuntimeStatsWithCommit.String", "RuntimeStatsWithCommit.Clone", "RuntimeStatsWithCommit.Merge", "RuntimeStatsWithCommit.Tp",
		"RuntimeStatsWithCommit.MergeCommitDetails", "RuntimeStatsWithCommit.formatBackoff", "RuntimeStatsWithCommit.formatLockKeysDetails",
		"RuntimeStatsWithConcurrencyInfo.String", "RuntimeStatsWithConcurrencyInfo.Clone", "RuntimeStatsWithConcurrencyInfo.Merge",
		"RuntimeStatsWithConcurrencyInfo.Tp", "RuntimeStatsWithConcurrencyInfo.SetConcurrencyInfo", "NewConcurrencyInfo",
		"RURuntimeStats.String", "RURuntimeStats.Clone", "RURuntimeStats.Merge", "RURuntimeStats.Tp"}},
	"opttrace": {Min: 200, GoFuncs: []string{"LogicalOptimizeTracer.AppendRuleTracerBeforeRuleOptimize",
		"LogicalOptimizeTracer.AppendRuleTracerStepToCurrent", "LogicalOptimizeTracer.RecordFinalLogicalPlan",
		"LogicalOptimizeTracer.removeUselessStep", "buildLogicalRuleOptimizeTracerBeforeOptimize", "toFlattenPlanTrace",
		"flattenLogicalPlanTrace", "PlanTrace.AppendChildrenID", "PhysicalOptimizeTracer.AppendCandidate",
		"PhysicalOptimizeTracer.RecordFinalPlanTrace", "PhysicalOptimizeTracer.buildCandidatesInfo",
		"OptimizeTracer.SetFastPlan", "OptimizeTracer.RecordFinalPlan", "NewPhysicalPlanCostDetail",
		"PhysicalPlanCostDetail.AddParam", "PhysicalPlanCostDetail.Exists", "PhysicalPlanCostDetail.GetPlanID",
		"PhysicalPlanCostDetail.GetPlanType", "PhysicalPlanCostDetail.SetDesc"}},
	"ce":    {Min: 50, GoFuncs: []string{"DedupCETrace"}},
	"codec": {Min: 10, GoFuncs: []string{"CodecPlanName"}},
}

// ---------------------------------------------------------------- payload decoding

func edDur(raw json.RawMessage) time.Duration {
	var v int64
	must(json.Unmarshal(raw, &v))
	return time.Duration(v)
}

func edTime(raw json.RawMessage) util.TimeDetail {
	var a [5]int64
	must(json.Unmarshal(raw, &a))
	return util.TimeDetail{ProcessTime: time.Duration(a[0]), SuspendTime: time.Duration(a[1]), WaitTime: time.Duration(a[2]),
		KvReadWallTime: time.Duration(a[3]), TotalRPCWallTime: time.Duration(a[4])}
}

type edScanP struct {
	TK, PK, PKS        int64
	DS, KS, CH, RC, RB uint64
	RD, SD             int64
}

func edScan(raw json.RawMessage) *util.ScanDetail {
	if string(raw) == "null" {
		return nil
	}
	var a []json.RawMessage
	must(json.Unmarshal(raw, &a))
	i := func(k int) int64 { var v int64; must(json.Unmarshal(a[k], &v)); return v }
	u := func(k int) uint64 { var v uint64; must(json.Unmarshal(a[k], &v)); return v }
	return &util.ScanDetail{TotalKeys: i(0), ProcessedKeys: i(1), ProcessedKeysSize: i(2), RocksdbDeleteSkippedCount: u(3),
		RocksdbKeySkippedCount: u(4), RocksdbBlockCacheHitCount: u(5), RocksdbBlockReadCount: u(6), RocksdbBlockReadByte: u(7),
		RocksdbBlockReadDuration: time.Duration(i(8)), GetSnapshotDuration: time.Duration(i(9))}
}

func edWrite(raw json.RawMessage) *util.WriteDetail {
	if string(raw) == "null" {
		return nil
	}
	var a [13]int64
	must(json.Unmarshal(raw, &a))
	d := func(k int) time.Duration { return time.Duration(a[k]) }
	return &util.WriteDetail{StoreBatchWaitDuration: d(0), ProposeSendWaitDuration: d(1), PersistLogDuration: d(2),
		RaftDbWriteLeaderWaitDuration: d(3), RaftDbSyncLogDuration: d(4), RaftDbWriteMemtableDuration: d(5),
		CommitLogDuration: d(6), ApplyBatchWaitDuration: d(7), ApplyLogDuration: d(8), ApplyMutexLockDuration: d(9),
		ApplyWriteLeaderWaitDuration: d(10), ApplyWriteWalDuration: d(11), ApplyWriteMemtableDuration: d(12)}
}

func edTiKV(raw json.RawMessage) util.TiKVExecDetails {
	var m struct{ T, S, W json.RawMessage }
	must(json.Unmarshal(raw, &m))
	var r util.TiKVExecDetails
	if len(m.T) > 0 && string(m.T) != "null" {
		td := edTime(m.T)
		r.TimeDetail = &td
	}
	if len(m.S) > 0 {
		r.ScanDetail = edScan(m.S)
	}
	if len(m.W) > 0 {
		r.WriteDetail = edWrite(m.W)
	}
	return r
}

func edReq(raw json.RawMessage) util.ReqDetailInfo {
	var a []json.RawMessage
	must(json.Unmarshal(raw, &a))
	var r util.ReqDetailInfo
	r.ReqTotalTime = edDur(a[0])
	must(json.Unmarshal(a[1], &r.Region))
	must(json.Unmarshal(a[2], &r.StoreAddr))
	r.ExecDetails = edTiKV(a[3])
	return r
}

type edCommitP struct {
	Gct, Glt, Pw, Wpb, Ct, Llt, Cbt int64
	Lag                             [4]int64
	Pbt, Cbtypes                    []string
	Sp, Cp                          json.RawMessage
	Wk, Ws                          int
	Prn                             int32
	Tr                              int
	Rl                              int64
	Prq                             int
}

func edCommit(raw json.RawMessage) *util.CommitDetails {
	if string(raw) == "null" {
		return nil
	}
	var p edCommitP
	must(json.Unmarshal(raw, &p))
	c := &util.CommitDetails{GetCommitTsTime: time.Duration(p.Gct), GetLatestTsTime: time.Duration(p.Glt),
		LagDetails:   util.CommitTSLagDetails{WaitTime: time.Duration(p.Lag[0]), BackoffCnt: int(p.Lag[1]), FirstLagTS: uint64(p.Lag[2]), WaitUntilTS: uint64(p.Lag[3])},
		PrewriteTime: time.Duration(p.Pw), WaitPrewriteBinlogTime: time.Duration(p.Wpb), CommitTime: time.Duration(p.Ct),
		LocalLatchTime: time.Duration(p.Llt), WriteKeys: p.Wk, WriteSize: p.Ws, PrewriteRegionNum: p.Prn, TxnRetry: p.Tr,
		ResolveLock: util.ResolveLockDetail{ResolveLockTime: p.Rl}, PrewriteReqNum: p.Prq}
	c.Mu.CommitBackoffTime = p.Cbt
	c.Mu.PrewriteBackoffTypes = p.Pbt
	c.Mu.CommitBackoffTypes = p.Cbtypes
	if len(p.Sp) > 0 {
		c.Mu.SlowestPrewrite = edReq(p.Sp)
	}
	if len(p.Cp) > 0 {
		c.Mu.CommitPrimary = edReq(p.Cp)
	}
	return c
}

type edLockP struct {
	Tt            int64
	Rn, Lk        int32
	Aln, Ald, Lwc int
	Rl, Bt        int64
	Btypes        []string
	St            int64
	Sr            uint64
	Sa            string
	Se            json.RawMessage
	Rpct, Rpcc    int64
	Rc            int
}

func edLock(raw json.RawMessage) *util.LockKeysDetails {
	if string(raw) == "null" {
		return nil
	}
	var p edLockP
	must(json.Unmarshal(raw, &p))
	l := &util.LockKeysDetails{TotalTime: time.Duration(p.Tt), RegionNum: p.Rn, LockKeys: p.Lk, AggressiveLockNewCount: p.Aln,
		AggressiveLockDerivedCount: p.Ald, LockedWithConflictCount: p.Lwc, ResolveLock: util.ResolveLockDetail{ResolveLockTime: p.Rl},
		BackoffTime: p.Bt, LockRPCTime: p.Rpct, LockRPCCount: p.Rpcc, RetryCount: p.Rc}
	l.Mu.BackoffTypes = p.Btypes
	l.Mu.SlowestReqTotalTime = time.Duration(p.St)
	l.Mu.SlowestRegion = p.Sr
	l.Mu.SlowestStoreAddr = p.Sa
	if len(p.Se) > 0 {
		l.Mu.SlowestExecDetails = edTiKV(p.Se)
	}
	return l
}

type edExecP struct {
	Bs                  map[string]int64
	Bt                  map[string]int
	Addr                string
	Td                  json.RawMessage
	Commit, Lock, Slock json.RawMessage
	Scan                json.RawMessage
	Cop, Bo, Lkd        int64
	Rc                  int
}

func edExec(raw json.RawMessage) *execdetails.ExecDetails {
	if string(raw) == "null" {
		return nil
	}
	var p edExecP
	must(json.Unmarshal(raw, &p))
	d := &execdetails.ExecDetails{CopTime: time.Duration(p.Cop), BackoffTime: time.Duration(p.Bo),
		LockKeysDuration: time.Duration(p.Lkd), RequestCount: p.Rc}
	if p.Bs != nil {
		d.BackoffSleep = map[string]time.Duration{}
		for k, v := range p.Bs {
			d.BackoffSleep[k] = time.Duration(v)
		}
	}
	d.BackoffTimes = p.Bt
	d.CalleeAddress = p.Addr
	if len(p.Td) > 0 {
		d.TimeDetail = edTime(p.Td)
	}
	if len(p.Commit) > 0 {
		d.CommitDetail = edCommit(p.Commit)
	}
	if len(p.Lock) > 0 {
		d.LockKeysDetail = edLock(p.Lock)
	}
	if len(p.Slock) > 0 {
		d.SharedLockKeysDetail = edLock(p.Slock)
	}
	if len(p.Scan) > 0 {
		d.ScanDetail = edScan(p.Scan)
	}
	return d
}

type edCopP struct {
	Scan, Td json.RawMessage
	Addr     string
	Bo       int64
	Bs       map[string]int64
	Bt       map[string]int
}

func edCop(raw json.RawMessage) *execdetails.CopExecDetails {
	if string(raw) == "null" {
		return nil
	}
	var p edCopP
	must(json.Unmarshal(raw, &p))
	d := &execdetails.CopExecDetails{CalleeAddress: p.Addr, BackoffTime: time.Duration(p.Bo), BackoffTimes: p.Bt}
	if sd := edScan(p.Scan); sd != nil {
		d.ScanDetail = *sd
	}
	d.TimeDetail = edTime(p.Td)
	if p.Bs != nil {
		d.BackoffSleep = map[string]time.Duration{}
		for k, v := range p.Bs {
			d.BackoffSleep[k] = time.Duration(v)
		}
	}
	return d
}

// edSummary: {"tp","rows","iters","id","conc": n|null, "scan": {field: n, "regions": [[id, n]...]} | null,
// "wait": [minTSO, breaker, queue] | null}.
func edSummary(raw json.RawMessage) *tipb.ExecutorExecutionSummary {
	if string(raw) == "null" {
		return nil
	}
	var p struct {
		Tp, Rows, Iters, Conc *uint64
		ID                    *string
		Scan                  map[string]json.RawMessage
		Wait                  *[3]uint64
	}
	must(json.Unmarshal(raw, &p))
	s := &tipb.ExecutorExecutionSummary{TimeProcessedNs: p.Tp, NumProducedRows: p.Rows, NumIterations: p.Iters,
		Concurrency: p.Conc, ExecutorId: p.ID}
	if p.Scan != nil {
		c := &tipb.TiFlashScanContext{}
		u := func(k string) *uint64 {
			raw, ok := p.Scan[k]
			if !ok {
				return nil
			}
			var v uint64
			must(json.Unmarshal(raw, &v))
			return &v
		}
		c.DmfileDataScannedRows, c.DmfileDataSkippedRows = u("dds"), u("dsk")
		c.DmfileMvccScannedRows, c.DmfileMvccSkippedRows = u("dms"), u("dmk")
		c.DmfileLmFilterScannedRows, c.DmfileLmFilterSkippedRows = u("dls"), u("dlk")
		c.TotalDmfileRsCheckMs, c.TotalDmfileReadMs, c.TotalBuildSnapshotMs = u("rsc"), u("rd"), u("bs")
		c.LocalRegions, c.RemoteRegions, c.TotalLearnerReadMs = u("lr"), u("rr"), u("lrm")
		c.DisaggReadCacheHitBytes, c.DisaggReadCacheMissBytes = u("ch"), u("cm")
		c.Segments, c.ReadTasks, c.DeltaRows, c.DeltaBytes = u("seg"), u("rt"), u("dr"), u("db")
		c.MvccInputRows, c.MvccInputBytes, c.MvccOutputRows, c.LmSkipRows = u("mir"), u("mib"), u("mor"), u("lsr")
		c.TotalBuildBitmapMs, c.TotalBuildInputstreamMs, c.StaleReadRegions = u("bb"), u("bi"), u("srr")
		c.MinLocalStreamMs, c.MaxLocalStreamMs, c.MinRemoteStreamMs, c.MaxRemoteStreamMs = u("mnl"), u("mxl"), u("mnr"), u("mxr")
		c.TotalVectorIdxLoadFromS3, c.TotalVectorIdxLoadFromDisk, c.TotalVectorIdxLoadFromCache = u("v3"), u("vd"), u("vc")
		c.TotalVectorIdxLoadTimeMs, c.TotalVectorIdxSearchTimeMs = u("vlt"), u("vst")
		c.TotalVectorIdxSearchVisitedNodes, c.TotalVectorIdxSearchDiscardedNodes = u("vvn"), u("vdn")
		c.TotalVectorIdxReadVecTimeMs, c.TotalVectorIdxReadOthersTimeMs = u("vrv"), u("vro")
		if raw, ok := p.Scan["regions"]; ok {
			var rs [][2]json.RawMessage
			must(json.Unmarshal(raw, &rs))
			for _, r := range rs {
				var id string
				var n uint64
				must(json.Unmarshal(r[0], &id))
				must(json.Unmarshal(r[1], &n))
				c.RegionsOfInstance = append(c.RegionsOfInstance, &tipb.TiFlashRegionNumOfInstance{InstanceId: &id, RegionNum: &n})
			}
		}
		s.DetailInfo = &tipb.ExecutorExecutionSummary_TiflashScanContext{TiflashScanContext: c}
	}
	if p.Wait != nil {
		w := p.Wait
		s.TiflashWaitSummary = &tipb.TiFlashWaitSummary{MinTSOWaitNs: &w[0], PipelineBreakerWaitNs: &w[1], PipelineQueueWaitNs: &w[2]}
	}
	return s
}

// edStats: ["basic", [[d, rows]...], setRows|null] | ["commit", commit|null, lock|null, slock|null, txnCnt,
// [commit...] (MergeCommitDetails)] | ["conc", [[name, num]...]] | ["ru", [rru, wru, wait]|null].
func edStats(raw json.RawMessage) execdetails.RuntimeStats {
	var a []json.RawMessage
	must(json.Unmarshal(raw, &a))
	switch arg[string](a, 0) {
	case "basic":
		s := &execdetails.BasicRuntimeStats{}
		for _, r := range arg[[][2]int64](a, 1) {
			s.Record(time.Duration(r[0]), int(r[1]))
		}
		if n := arg[*int64](a, 2); n != nil {
			s.SetRowNum(*n)
		}
		return s
	case "commit":
		s := &execdetails.RuntimeStatsWithCommit{Commit: edCommit(a[1]), LockKeys: edLock(a[2]), SharedLockKeys: edLock(a[3]),
			TxnCnt: arg[int](a, 4)}
		for _, c := range arg[[]json.RawMessage](a, 5) {
			s.MergeCommitDetails(edCommit(c))
		}
		return s
	case "conc":
		s := &execdetails.RuntimeStatsWithConcurrencyInfo{}
		var infos []*execdetails.ConcurrencyInfo
		for _, c := range arg[[][2]json.RawMessage](a, 1) {
			var name string
			var num int
			must(json.Unmarshal(c[0], &name))
			must(json.Unmarshal(c[1], &num))
			infos = append(infos, execdetails.NewConcurrencyInfo(name, num))
		}
		s.SetConcurrencyInfo(infos...)
		return s
	case "ru":
		r := arg[*[3]json.RawMessage](a, 1)
		if r == nil {
			return &execdetails.RURuntimeStats{}
		}
		var rru, wru float64
		rru = edFloat(r[0])
		wru = edFloat(r[1])
		return &execdetails.RURuntimeStats{RUDetails: util.NewRUDetailsWith(rru, wru, edDur(r[2]))}
	}
	panic("execdetails: unknown stats")
}

func edFloat(raw json.RawMessage) float64 {
	var s string
	must(json.Unmarshal(raw, &s))
	f, err := strconv.ParseFloat(s, 64)
	must(err)
	return f
}

func edFloatOut(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// ---------------------------------------------------------------- output

func edZap(fields []zapcore.Field) [][]any {
	out := [][]any{}
	for _, f := range fields {
		if f.Type == zapcore.StringType {
			out = append(out, []any{f.Key, "s", f.String})
		} else {
			out = append(out, []any{f.Key, "i", f.Integer})
		}
	}
	return out
}

func edSorted[V any](m map[string]V) [][]any {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := [][]any{}
	for _, k := range keys {
		out = append(out, []any{k, m[k]})
	}
	return out
}

func edCommitOut(c *util.CommitDetails) any {
	if c == nil {
		return nil
	}
	// String of the commit detail through RuntimeStatsWithCommit and the fields Merge touches.
	rs := &execdetails.RuntimeStatsWithCommit{Commit: c, TxnCnt: 1}
	return []any{rs.String(), int64(c.GetLatestTsTime), int64(c.LagDetails.WaitTime), c.LagDetails.BackoffCnt,
		c.LagDetails.FirstLagTS, c.LagDetails.WaitUntilTS, c.PrewriteReqNum}
}

func edLockOut(l *util.LockKeysDetails) any {
	if l == nil {
		return nil
	}
	rs := &execdetails.RuntimeStatsWithCommit{LockKeys: l}
	return []any{rs.String(), l.AggressiveLockNewCount, l.AggressiveLockDerivedCount, l.LockedWithConflictCount}
}

func edExecOut(d execdetails.ExecDetails) any {
	var scan any
	if d.ScanDetail != nil {
		s := *d.ScanDetail
		scan = []any{s.String(), s.ProcessedKeysSize}
	}
	return map[string]any{"s": d.String(), "z": edZap(d.ToZapFields()), "scan": scan,
		"commit": edCommitOut(d.CommitDetail), "lock": edLockOut(d.LockKeysDetail), "slock": edLockOut(d.SharedLockKeysDetail),
		"td": d.TimeDetail.String(), "rc": d.RequestCount, "cop": int64(d.CopTime), "bo": int64(d.BackoffTime)}
}

// ---------------------------------------------------------------- exec

func execdetailsExec(op string, p []json.RawMessage) (any, error) {
	switch op {
	case "fmt":
		n := arg[int64](p, 1)
		switch arg[string](p, 0) {
		case "fd":
			return execdetails.FormatDuration(time.Duration(n)), nil
		case "cgfd":
			return util.FormatDuration(time.Duration(n)), nil
		case "fb":
			return util.FormatBytes(n), nil
		case "bts":
			return util.BytesToString(n), nil
		}
	case "gomath":
		x := edFloat(p[1])
		var r float64
		switch arg[string](p, 0) {
		case "sin":
			r = math.Sin(x)
		case "asin":
			r = math.Asin(x)
		}
		return fmt.Sprintf("%016x", math.Float64bits(r)), nil
	case "tdigest":
		return execTDigest(arg[[]json.RawMessage](p, 0)), nil
	case "percentile":
		var pc execdetails.Percentile[execdetails.DurationWithAddr]
		return execPercentile(&pc, arg[[]json.RawMessage](p, 0)), nil
	case "details":
		return edExecOut(*edExec(p[0])), nil
	case "cgdetails":
		return execCG(p), nil
	case "sync":
		return execSync(arg[[]json.RawMessage](p, 0)), nil
	case "coll":
		return execColl(arg[[]json.RawMessage](p, 0)), nil
	case "stats":
		a, b := edStats(p[0]), edStats(p[1])
		out := map[string]any{"a": a.String(), "tp": a.Tp(), "b": b.String()}
		c := a.Clone()
		out["clone"] = c.String()
		a.Merge(b)
		out["merged"] = a.String()
		out["clone2"] = c.String()
		return out, nil
	case "opttrace":
		return execOptTrace(arg[[]json.RawMessage](p, 0)), nil
	case "ce":
		var recs []*tracing.CETraceRecord
		must(json.Unmarshal(p[0], &recs))
		// TableID is json:"-": decode it separately.
		var ids [][]json.RawMessage
		must(json.Unmarshal(p[1], &ids))
		for i, r := range recs {
			must(json.Unmarshal(ids[i][0], &r.TableID))
		}
		out := []any{}
		for _, r := range tracing.DedupCETrace(recs) {
			b, err := json.Marshal(r)
			must(err)
			out = append(out, []any{string(b), r.TableID})
		}
		return out, nil
	case "codec":
		return tracing.CodecPlanName(arg[string](p, 0), arg[int](p, 1)), nil
	}
	panic("execdetails: unknown op " + op)
}

func execTDigest(steps []json.RawMessage) any {
	t := tdigest.New()
	out := []any{}
	for _, raw := range steps {
		var s []json.RawMessage
		must(json.Unmarshal(raw, &s))
		switch arg[string](s, 0) {
		case "add":
			t.Add(edFloat(s[1]), edFloat(s[2]))
			out = append(out, nil)
		case "list":
			var cl tdigest.CentroidList
			for _, c := range arg[[][2]json.RawMessage](s, 1) {
				cl = append(cl, tdigest.Centroid{Mean: edFloat(c[0]), Weight: edFloat(c[1])})
			}
			t.AddCentroidList(cl)
			out = append(out, nil)
		case "gen":
			edBulk(arg[string](s, 1), arg[int](s, 2), arg[uint64](s, 3), func(_ int, x float64, r *edSplitMix) {
				w := 1.0
				switch arg[string](s, 4) {
				case "int":
					w = float64(r.next()%5 + 1)
				case "frac":
					w = float64(r.next()>>11)/(1<<53)*4 + 0.01
				}
				t.Add(x, w)
			})
			out = append(out, nil)
		case "q":
			out = append(out, edFloatOut(t.Quantile(edFloat(s[1]))))
		case "cent":
			cs := [][2]string{}
			for _, c := range t.Centroids() {
				cs = append(cs, [2]string{edFloatOut(c.Mean), edFloatOut(c.Weight)})
			}
			out = append(out, cs)
		}
	}
	return out
}

func execPercentile(pc *execdetails.Percentile[execdetails.DurationWithAddr], steps []json.RawMessage) any {
	out := []any{}
	for _, raw := range steps {
		var s []json.RawMessage
		must(json.Unmarshal(raw, &s))
		switch arg[string](s, 0) {
		case "add":
			pc.Add(execdetails.DurationWithAddr{D: edDur(s[1]), Addr: arg[string](s, 2)})
			out = append(out, nil)
		case "gen":
			edBulk(arg[string](s, 1), arg[int](s, 2), arg[uint64](s, 3), func(_ int, x float64, r *edSplitMix) {
				pc.Add(execdetails.DurationWithAddr{D: time.Duration(x), Addr: "s" + strconv.FormatUint(r.next()%5, 10)})
			})
			out = append(out, nil)
		case "p":
			out = append(out, edFloatOut(pc.GetPercentile(edFloat(s[1]))))
		case "max":
			m := pc.GetMax()
			out = append(out, []any{int64(m.D), m.Addr, edFloatOut(m.GetFloat64())})
		case "min":
			m := pc.GetMin()
			out = append(out, []any{int64(m.D), m.Addr})
		case "size":
			out = append(out, pc.Size())
		case "sum":
			out = append(out, edFloatOut(pc.Sum()))
		case "merge":
			var other execdetails.Percentile[execdetails.DurationWithAddr]
			sub := execPercentile(&other, arg[[]json.RawMessage](s, 1))
			pc.MergePercentile(&other)
			out = append(out, sub)
		case "int64":
			// Int64 / Duration wrappers: GetFloat64.
			out = append(out, []any{edFloatOut(execdetails.Int64(arg[int64](s, 1)).GetFloat64()),
				edFloatOut(execdetails.Duration(arg[int64](s, 1)).GetFloat64())})
		}
	}
	return out
}

// cgdetails: [commitA, commitB, lockA, lockB, tikv, ru]: client-go String / Merge / Clone.
func execCG(p []json.RawMessage) any {
	out := map[string]any{}
	ca, cb := edCommit(p[0]), edCommit(p[1])
	if ca != nil {
		cl := ca.Clone()
		if cb != nil {
			ca.Merge(cb)
		}
		out["commit"] = []any{edCommitOut(ca), edCommitOut(cl)}
	}
	la, lb := edLock(p[2]), edLock(p[3])
	if la != nil {
		cl := la.Clone()
		if lb != nil {
			la.Merge(lb)
		}
		out["lock"] = []any{edLockOut(la), edLockOut(cl)}
	}
	t := edTiKV(p[4])
	out["tikv"] = t.String()
	if t.TimeDetail != nil {
		td := *t.TimeDetail
		td.Merge(t.TimeDetail)
		out["td2"] = td.String()
	}
	if t.ScanDetail != nil {
		sd := *t.ScanDetail
		sd.Merge(t.ScanDetail)
		out["sd2"] = sd.String()
	}
	var r [6]json.RawMessage
	must(json.Unmarshal(p[5], &r))
	a := util.NewRUDetailsWith(edFloat(r[0]), edFloat(r[1]), edDur(r[2]))
	b := util.NewRUDetailsWith(edFloat(r[3]), edFloat(r[4]), edDur(r[5]))
	c := a.Clone()
	a.Merge(b)
	out["ru"] = []any{a.String(), c.String(), edFloatOut(a.RRU()), edFloatOut(a.WRU()), int64(a.RUWaitDuration())}
	return out
}

func edTasksOut(d *execdetails.CopTasksDetails) any {
	if d == nil {
		return nil
	}
	dm := func(m map[string]time.Duration) any {
		if m == nil {
			return nil
		}
		r := map[string]int64{}
		for k, v := range m {
			r[k] = int64(v)
		}
		return edSorted(r)
	}
	var times any
	if d.TotBackoffTimes != nil {
		times = edSorted(d.TotBackoffTimes)
	}
	var addrs any
	if d.MaxBackoffAddress != nil {
		addrs = edSorted(d.MaxBackoffAddress)
	}
	return map[string]any{"n": d.NumCopTasks, "pavg": int64(d.AvgProcessTime), "p90": int64(d.P90ProcessTime),
		"paddr": d.MaxProcessAddress, "pmax": int64(d.MaxProcessTime), "wavg": int64(d.AvgWaitTime), "w90": int64(d.P90WaitTime),
		"waddr": d.MaxWaitAddress, "wmax": int64(d.MaxWaitTime), "bmax": dm(d.MaxBackoffTime), "baddr": addrs,
		"bavg": dm(d.AvgBackoffTime), "b90": dm(d.P90BackoffTime), "btot": dm(d.TotBackoffTime), "btimes": times,
		"z": edZap(d.ToZapFields())}
}

func execSync(steps []json.RawMessage) any {
	var s execdetails.SyncExecDetails
	out := []any{}
	for _, raw := range steps {
		var st []json.RawMessage
		must(json.Unmarshal(raw, &st))
		switch arg[string](st, 0) {
		case "merge":
			s.MergeExecDetails(edExec(st[1]), edCommit(st[2]))
			out = append(out, nil)
		case "cop":
			s.MergeCopExecDetails(edCop(st[1]), edDur(st[2]))
			out = append(out, nil)
		case "lock":
			s.MergeLockKeysExecDetails(edLock(st[1]))
			out = append(out, nil)
		case "slock":
			s.MergeSharedLockKeysExecDetails(edLock(st[1]))
			out = append(out, nil)
		case "reset":
			s.Reset()
			out = append(out, nil)
		case "copgen":
			// n MergeCopExecDetails with generated times, addresses and backoffs.
			edBulk(arg[string](st, 1), arg[int](st, 2), arg[uint64](st, 3), func(_ int, x float64, r *edSplitMix) {
				d := &execdetails.CopExecDetails{CalleeAddress: "tikv-" + strconv.FormatUint(r.next()%4, 10),
					TimeDetail: util.TimeDetail{ProcessTime: time.Duration(x), WaitTime: time.Duration(r.next() % 50000)}}
				if r.next()%3 == 0 {
					k := "bo" + strconv.FormatUint(r.next()%3, 10)
					d.BackoffSleep = map[string]time.Duration{k: time.Duration(r.next() % 100000)}
					d.BackoffTimes = map[string]int{k: int(r.next()%4) + 1}
				}
				s.MergeCopExecDetails(d, time.Duration(r.next()%1000))
			})
			out = append(out, nil)
		case "get":
			out = append(out, edExecOut(s.GetExecDetails()))
		case "tasks":
			d := s.CopTasksDetails()
			if d == nil {
				var nilD *execdetails.CopTasksDetails
				out = append(out, []any{nil, edZap(nilD.ToZapFields())})
			} else {
				out = append(out, []any{edTasksOut(d)})
			}
		case "sum":
			c := s.CopTasksSummary()
			if c == nil {
				out = append(out, nil)
			} else {
				out = append(out, []any{c.NumCopTasks, c.MaxProcessAddress, int64(c.MaxProcessTime), int64(c.TotProcessTime),
					c.MaxWaitAddress, int64(c.MaxWaitTime), int64(c.TotWaitTime)})
			}
		}
	}
	return out
}

func execColl(steps []json.RawMessage) any {
	c := execdetails.NewRuntimeStatsColl(nil)
	out := []any{}
	for _, raw := range steps {
		var st []json.RawMessage
		must(json.Unmarshal(raw, &st))
		switch arg[string](st, 0) {
		case "basic":
			b := c.GetBasicRuntimeStats(arg[int](st, 1))
			b.Record(edDur(st[2]), arg[int](st, 3))
			out = append(out, []any{b.GetActRows(), b.GetTime(), b.String()})
		case "setrows":
			c.GetBasicRuntimeStats(arg[int](st, 1)).SetRowNum(arg[int64](st, 2))
			out = append(out, nil)
		case "reg":
			c.RegisterStats(arg[int](st, 1), edStats(st[2]))
			out = append(out, nil)
		case "cop":
			id := c.RecordCopStats(arg[int](st, 1), kv.StoreType(arg[uint8](st, 2)), edScan(st[3]), edTime(st[4]), edSummary(st[5]))
			out = append(out, id)
		case "one":
			id := c.RecordOneCopTask(arg[int](st, 1), kv.StoreType(arg[uint8](st, 2)), edSummary(st[3]))
			out = append(out, id)
		case "cops":
			// n RecordOneCopTask summaries for plan id with generated process times.
			id, tp := arg[int](st, 1), kv.StoreType(arg[uint8](st, 2))
			edBulk(arg[string](st, 3), arg[int](st, 4), arg[uint64](st, 5), func(_ int, x float64, r *edSplitMix) {
				tpn, rows, iters, conc := uint64(x), r.next()%100, r.next()%10, r.next()%4
				c.RecordOneCopTask(id, tp, &tipb.ExecutorExecutionSummary{TimeProcessedNs: &tpn, NumProducedRows: &rows,
					NumIterations: &iters, Concurrency: &conc})
			})
			out = append(out, nil)
		case "root":
			r := c.GetRootStats(arg[int](st, 1))
			basic, groups := r.MergeStats()
			bs := ""
			if basic != nil {
				bs = basic.String()
			}
			out = append(out, []any{r.String(), r.GetActRows(), bs, len(groups)})
		case "copstr":
			cs := c.GetCopStats(arg[int](st, 1))
			if cs == nil {
				out = append(out, nil)
			} else {
				out = append(out, []any{cs.String(), cs.GetActRows(), cs.GetTasks()})
			}
		case "coprows":
			n, rows := c.GetCopCountAndRows(arg[int](st, 1))
			out = append(out, []any{n, rows})
		case "actrows":
			out = append(out, c.GetPlanActRows(arg[int](st, 1)))
		case "exists":
			id := arg[int](st, 1)
			out = append(out, []any{c.ExistsRootStats(id), c.ExistsCopStats(id)})
		case "new":
			c = execdetails.NewRuntimeStatsColl(c)
			out = append(out, nil)
		case "bcop":
			cs := c.GetCopStats(arg[int](st, 1))
			if cs == nil {
				out = append(out, nil)
				continue
			}
			// CopRuntimeStats.stats (the first field) is the basicCopRuntimeStats.
			a := basicCopClone(unsafe.Pointer(cs))
			r := []any{a.String(), a.Tp()}
			b := a.Clone()
			if other := arg[*int](st, 2); other != nil {
				if os := c.GetCopStats(*other); os != nil {
					a.Merge(basicCopClone(unsafe.Pointer(os)))
				}
			} else {
				a.Merge(&execdetails.BasicRuntimeStats{}) // a different type: no-op
			}
			r = append(r, a.String(), b.String())
			out = append(out, r)
		}
	}
	return out
}

// opttrace steps over an OptimizeTracer t (logical and physical created by "init"):
//
//	["init", physCandidatesNil] | ["before", index, name, plan] | ["step", id, tp, reason, action] |
//	["final", plan] (logical RecordFinalLogicalPlan) | ["cand", plan|null, mapping] | ["pfinal", plan] |
//	["cost", key, id, tp, desc, [[k, v]...], cost] (NewPhysicalPlanCostDetail + AddParam + SetDesc, into
//	Physical.PhysicalPlanCostDetails) | ["fast", plan] | ["rfinal", plan] | ["ids", plan, [id...]]
//	(PlanTrace.AppendChildrenID on a copy).
//
// A plan is [id, tp, info, cost, [children...]]; cost is a float string. A cost param value is
// ["f", float] | ["i", int] | ["s", str] | ["b", bool].
func edPlan(raw json.RawMessage) *tracing.PlanTrace {
	if string(raw) == "null" {
		return nil
	}
	var a []json.RawMessage
	must(json.Unmarshal(raw, &a))
	p := &tracing.PlanTrace{ID: arg[int](a, 0), TP: arg[string](a, 1), ExplainInfo: arg[string](a, 2), Cost: edFloat(a[3])}
	for _, c := range arg[[]json.RawMessage](a, 4) {
		p.Children = append(p.Children, edPlan(c))
	}
	return p
}

func edJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"err": err.Error()}
	}
	return string(b)
}

func execOptTrace(steps []json.RawMessage) any {
	t := &tracing.OptimizeTracer{}
	out := []any{}
	for _, raw := range steps {
		var st []json.RawMessage
		must(json.Unmarshal(raw, &st))
		var r any
		switch arg[string](st, 0) {
		case "init":
			t.Logical = &tracing.LogicalOptimizeTracer{}
			t.Physical = &tracing.PhysicalOptimizeTracer{}
			if !arg[bool](st, 1) {
				t.Physical.Candidates = map[int]*tracing.CandidatePlanTrace{}
				t.Physical.PhysicalPlanCostDetails = map[string]*tracing.PhysicalPlanCostDetail{}
			}
		case "before":
			t.Logical.AppendRuleTracerBeforeRuleOptimize(arg[int](st, 1), arg[string](st, 2), edPlan(st[3]))
		case "step":
			t.Logical.AppendRuleTracerStepToCurrent(arg[int](st, 1), arg[string](st, 2), arg[string](st, 3), arg[string](st, 4))
		case "final":
			t.Logical.RecordFinalLogicalPlan(edPlan(st[1]))
		case "cand":
			t.Physical.AppendCandidate(&tracing.CandidatePlanTrace{PlanTrace: edPlan(st[1]), MappingLogicalPlan: arg[string](st, 2)})
		case "pfinal":
			t.Physical.RecordFinalPlanTrace(edPlan(st[1]))
		case "cost":
			d := tracing.NewPhysicalPlanCostDetail(arg[int](st, 2), arg[string](st, 3))
			d.SetDesc(arg[string](st, 4))
			for _, kv := range arg[[][2]json.RawMessage](st, 5) {
				var k string
				must(json.Unmarshal(kv[0], &k))
				var v []json.RawMessage
				must(json.Unmarshal(kv[1], &v))
				switch arg[string](v, 0) {
				case "f":
					d.AddParam(k, edFloat(v[1]))
				case "i":
					d.AddParam(k, arg[int](v, 1))
				case "s":
					d.AddParam(k, arg[string](v, 1))
				case "b":
					d.AddParam(k, arg[bool](v, 1))
				}
			}
			d.Cost = edFloat(st[6])
			t.Physical.PhysicalPlanCostDetails[arg[string](st, 1)] = d
			r = []any{d.GetPlanID(), d.GetPlanType(), d.Exists("a"), d.Exists("")}
		case "fast":
			t.SetFastPlan(edPlan(st[1]))
		case "rfinal":
			t.RecordFinalPlan(edPlan(st[1]))
		case "ids":
			p := edPlan(st[1])
			p.AppendChildrenID(arg[[]int](st, 2)...)
			p.AppendChildrenID(arg[[]int](st, 2)...)
			r = edJSON(p)
		}
		if r == nil {
			r = edJSON(t)
		}
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------- deterministic bulk inputs

// edSplitMix is splitmix64: bulk steps ("gen", "copgen", "cops") derive their values from it so
// a record stays small; the Rust replay implements the same generator.
type edSplitMix struct{ s uint64 }

func (r *edSplitMix) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// edGenValue: kind "int" (0..10^6 ns), "dup" (0..6, many equal values), "wide" (up to ~3h with a
// random magnitude), "float" (a fraction in [0, 1e3)), "mono" (increasing).
func edGenValue(r *edSplitMix, kind string, i int) float64 {
	switch kind {
	case "int":
		return float64(r.next() % 1000000)
	case "dup":
		return float64(r.next() % 7)
	case "wide":
		v := r.next()
		return float64((v >> 8) % (uint64(1) << (v % 44)))
	case "float":
		return float64(r.next()>>11) / (1 << 53) * 1000
	case "mono":
		return float64(i)*3.5 + float64(r.next()%3)
	}
	panic("edGenValue: " + kind)
}

func init() {
	// Bulk steps of the tdigest / percentile / sync records (see edSplitMix).
	edBulk = func(kind string, n int, seed uint64, f func(i int, x float64, r *edSplitMix)) {
		r := &edSplitMix{s: seed}
		for i := 0; i < n; i++ {
			f(i, edGenValue(r, kind, i), r)
		}
	}
}

var edBulk func(kind string, n int, seed uint64, f func(i int, x float64, r *edSplitMix))
