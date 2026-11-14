package domains

// Domain "copr": pkg/store/copr (block D07.2, the coprocessor client without batch cop and
// MPP) and pkg/util/paging (D00.3, ported whole with it).
//
// Part 1 of the block: key ranges, task building, region / bucket splitting, small-task
// concurrency, the paging range arithmetic and the request-field conversions. The
// iterator, its workers and the response handling (part 2) need a TiKV store and are exempt
// below; tidb-rs tests them on a mock store.
//
// Values: keys are hex; a range is [start, end].
//
// Records:
//   - "kr": args [first|null, mid, last|null, steps]: a KeyRanges with those parts
//     (unexported fields, set through reflect), then steps ["split", key, "L"|"R"] (the
//     state becomes the left / right half), ["slice", from, to], ["at", i], ["string"],
//     ["pb"] (ToPBRanges). out: one entry per step; split / slice give the new state's
//     shape {f, m, l, r} (first present, mid length, last present, ToRanges).
//   - "bkr": args [keys] -> BuildKeyRanges (an odd count panics).
//   - "calc": args [ranges, split|null, desc, "remain"|"retry"] -> the ranges of
//     copIteratorWorker.calculateRemain / calculateRetry.
//   - "split": args [cluster, ranges, fn, limit, needLeader, buckets]: fn "locs"
//     (SplitKeyRangesByLocations), "buckets" (SplitKeyRangesByBuckets), "regions"
//     (SplitRegionRanges). out {layout, res}: res is [[regionID, ranges]...] (ranges only
//     for "regions").
//   - "build": args [cluster, ranges, req, hints|null, respChan, ignoreReadTimeout] ->
//     buildCopTasks. out {layout, tasks} (see coprTaskOut).
//   - "small": args [tasks] (each [rowCountHint, batched]) and numcpu -> isSmallTask of each
//     and smallTaskConcurrency.
//   - "reqflags": args [req] -> optRowHint, checkStoreBatchCopr.
//   - "pb": args [priority, isolation] -> priorityToPB, isolationLevelToPB.
//   - "paging": args [f, a, b]: "grow" GrowPagingSize(a, b), "seek" CalculateSeekCnt(a).
//
// A cluster is {splits: [key...], buckets: [[regionIdx, [key...]]...]}: a mocktikv
// cluster bootstrapped with those split keys (testutils.BootstrapWithMultiRegions), bucket
// keys set on the region at that index (version = the region id, as the Go tests do).
// layout is the regions read back through a fresh region cache: [{id, conf, ver, start,
// end, bver, bkeys|null, store, peer: [id, storeID, role]}], what the Rust replay's region
// cache serves.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"sort"
	"time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/kvproto/pkg/coprocessor"
	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/store/copr"
	"github.com/pingcap/tidb/pkg/store/driver/backoff"
	"github.com/pingcap/tidb/pkg/util/paging"
	"github.com/tikv/client-go/v2/testutils"
	"github.com/tikv/client-go/v2/tikv"
	"github.com/tikv/client-go/v2/util"
	"strconv"
)

const coprSeed = 20261114

//go:linkname coprBuildCopTasks github.com/pingcap/tidb/pkg/store/copr.buildCopTasks
func coprBuildCopTasks(bo *backoff.Backoffer, ranges *copr.KeyRanges, opt unsafe.Pointer) ([]unsafe.Pointer, error)

//go:linkname coprIsSmallTask github.com/pingcap/tidb/pkg/store/copr.isSmallTask
func coprIsSmallTask(task unsafe.Pointer) bool

//go:linkname coprSmallTaskConcurrency github.com/pingcap/tidb/pkg/store/copr.smallTaskConcurrency
func coprSmallTaskConcurrency(tasks []unsafe.Pointer, numcpu int) (int, int)

//go:linkname coprCalculateRemain github.com/pingcap/tidb/pkg/store/copr.(*copIteratorWorker).calculateRemain
func coprCalculateRemain(w unsafe.Pointer, ranges *copr.KeyRanges, split *coprocessor.KeyRange, desc bool) *copr.KeyRanges

//go:linkname coprCalculateRetry github.com/pingcap/tidb/pkg/store/copr.(*copIteratorWorker).calculateRetry
func coprCalculateRetry(w unsafe.Pointer, ranges *copr.KeyRanges, split *coprocessor.KeyRange, desc bool) *copr.KeyRanges

//go:linkname coprPriorityToPB github.com/pingcap/tidb/pkg/store/copr.priorityToPB
func coprPriorityToPB(pri int) kvrpcpb.CommandPri

//go:linkname coprIsolationLevelToPB github.com/pingcap/tidb/pkg/store/copr.isolationLevelToPB
func coprIsolationLevelToPB(level kv.IsoLevel) kvrpcpb.IsolationLevel

//go:linkname coprOptRowHint github.com/pingcap/tidb/pkg/store/copr.optRowHint
func coprOptRowHint(req *kv.Request) bool

//go:linkname coprCheckStoreBatchCopr github.com/pingcap/tidb/pkg/store/copr.checkStoreBatchCopr
func coprCheckStoreBatchCopr(req *kv.Request) bool

//go:linkname coprTaskString github.com/pingcap/tidb/pkg/store/copr.(*copTask).String
func coprTaskString(t unsafe.Pointer) string

//go:linkname coprTaskToPBBatchTasks github.com/pingcap/tidb/pkg/store/copr.(*copTask).ToPBBatchTasks
func coprTaskToPBBatchTasks(t unsafe.Pointer) []*coprocessor.StoreBatchTask

// coprBuildOpt mirrors copr.buildCopTaskOpt (v8.5.8 field order and types).
type coprBuildOpt struct {
	req                         *kv.Request
	cache                       *copr.RegionCache
	eventCb                     func(any)
	respChan                    bool
	rowHints                    []int
	elapsed                     *time.Duration
	ignoreTiKVClientReadTimeout bool
}

// coprTaskType is reflect's *copr.copTask, reached through exported signatures:
// CopClient.BuildCopIterator returns *copIterator, whose GetTasks returns []*copTask.
var coprTaskType = func() reflect.Type {
	m, _ := reflect.TypeOf(&copr.CopClient{}).MethodByName("BuildCopIterator")
	it := m.Type.Out(0)
	g, _ := it.MethodByName("GetTasks")
	return g.Type.Out(0).Elem().Elem()
}()

func init() {
	vec.Register(&vec.Domain{
		Name:     "copr",
		Seed:     coprSeed,
		Coverage: coprCoverage,
		Blocks:   []string{"D07.2"},
		Files:    []string{"pkg/util/paging/paging.go"},
		Exempt:   coprExempt(),
		Gzip:     true,
		Generate: coprGenerate,
		Exec:     coprExec,
	})
}

// CoprMinedTests: the Go tests whose cases are replayed as records.
func CoprMinedTests() []string {
	return []string{"TestCopRanges", "TestCopRangeSplit", "TestBuildTasksWithoutBuckets", "TestBuildTasksByBuckets",
		"TestSplitKeyRangesByLocationsWithoutBuckets", "TestSplitKeyRanges", "TestBuildPagingTasks",
		"TestBuildPagingTasksDisablePagingForSmallLimit", "TestCalculateRetry", "TestCalculateRemain",
		"TestBasicSmallTaskConc", "TestBuildCopTasksWithRowCountHint", "TestSmallTaskConcurrencyLimit",
		"TestBatchStoreCoprOnlySendToLeader"}
}

var coprCoverage = map[string]vec.Coverage{
	"kr": {Min: 300, GoFuncs: []string{"NewKeyRanges", "KeyRanges.String", "KeyRanges.Len", "KeyRanges.RefAt",
		"KeyRanges.At", "KeyRanges.Slice", "KeyRanges.Do", "KeyRanges.Split", "KeyRanges.ToRanges", "KeyRanges.ToPBRanges"}},
	"bkr":  {Min: 20, GoFuncs: []string{"BuildKeyRanges"}},
	"calc": {Min: 200, GoFuncs: []string{"copIteratorWorker.calculateRemain", "copIteratorWorker.calculateRetry"}},
	"split": {Min: 300, GoFuncs: []string{"NewRegionCache", "RegionCache.SplitRegionRanges", "RegionCache.SplitKeyRangesByLocations",
		"RegionCache.splitKeyRangesByLocation", "RegionCache.SplitKeyRangesByBuckets",
		"LocationKeyRanges.splitKeyRangesByBuckets", "LocationKeyRanges.getBucketVersion"}},
	"build": {Min: 400, GoFuncs: []string{"buildCopTasks", "newLegacyTaskBuilder", "legacyTaskBuilder.handle",
		"legacyTaskBuilder.regionNum", "legacyTaskBuilder.reverse", "legacyTaskBuilder.build", "newBatchTaskBuilder",
		"batchStoreTaskBuilder.handle", "batchStoreTaskBuilder.regionNum", "batchStoreTaskBuilder.reverse",
		"batchStoreTaskBuilder.build", "RegionCache.BuildBatchTask", "reverseTasks", "copTask.String", "copTask.ToPBBatchTasks"}},
	"small":    {Min: 200, GoFuncs: []string{"isSmallTask", "smallTaskConcurrency"}},
	"reqflags": {Min: 100, GoFuncs: []string{"optRowHint", "checkStoreBatchCopr"}},
	"pb":       {Min: 20, GoFuncs: []string{"priorityToPB", "isolationLevelToPB"}},
	"paging":   {Min: 200, GoFuncs: []string{"pkg/util/paging/paging.go:GrowPagingSize", "pkg/util/paging/paging.go:CalculateSeekCnt"}},
}

func coprExempt() map[string]string {
	ex := map[string]string{
		"buildTiDBMemCopTasks":                  "boundary stub until D06.5 ports infosync.GetAllServerInfo (the TiDB servers of the cluster); covered by a tidb-rs unit test (D07.2 spec §4)",
		"RegionCache.OnSendFailForBatchRegions": "TiFlash only (logs and returns for a TiKV store); its only caller is batch_coprocessor.go (DEF.MPP), not ported (D07.2 spec §1)",
		"Store.GetMPPClient":                    "MPP (DEF.MPP): the MPP client is not ported in the read-only phase (D07.2 spec §1)",
	}
	part2 := []string{
		"CopClient.Send", "CopClient.BuildCopIterator", "copIterator.open", "copIterator.recvFromRespCh", "copIterator.Next",
		"copIterator.Close", "copIterator.GetConcurrency", "copIterator.GetStoreBatchInfo", "copIterator.GetBuildTaskElapsed",
		"copIterator.GetSendRate", "copIterator.GetRequestRateLimit", "copIterator.GetTasks",
		"copIterator.CollectUnconsumedCopRuntimeStats", "copIteratorTaskSender.run", "copIteratorTaskSender.sendToTaskCh",
		"newCopIteratorWorker", "copIteratorWorker.run", "copIteratorWorker.sendToRespCh", "copIteratorWorker.checkRespOOM",
		"copIteratorWorker.handleTask", "copIteratorWorker.handleTaskOnce", "copIteratorWorker.logTimeCopTask",
		"copIteratorWorker.handleCopPagingResult", "copIteratorWorker.handleCopResponse", "copIteratorWorker.handleBatchRemainsOnErr",
		"copIteratorWorker.handleBatchCopResponse", "copIteratorWorker.handleLockErr", "copIteratorWorker.buildCacheKey",
		"copIteratorWorker.handleCopCache", "copIteratorWorker.getLockResolverDetails", "copIteratorWorker.handleCollectExecutionInfo",
		"copIteratorWorker.collectCopRuntimeStats", "copIteratorWorker.collectKVClientRuntimeStats",
		"copIteratorWorker.collectUnconsumedCopRuntimeStats", "copIteratorWorker.handleTiDBSendReqErr", "copIteratorWorker.finished",
		"liteCopIteratorWorker.liteSendReq", "chooseBackoffer", "memTrackerConsumeResp", "appendScanDetail",
		"copResponse.GetData", "copResponse.GetStartKey", "copResponse.GetCopRuntimeStats", "copResponse.MemSize", "copResponse.RespTime",
		"pkg/store/copr/coprocessor.go:init", "copErrorResponse.Next", "copErrorResponse.Close",
		"newRateLimitAction", "rateLimitAction.Action", "rateLimitAction.GetPriority", "rateLimitAction.destroyTokenIfNeeded",
		"rateLimitAction.conditionLock", "rateLimitAction.conditionUnlock", "rateLimitAction.close", "rateLimitAction.setEnabled",
		"rateLimitAction.isEnabled",
		"NewStore", "Store.Close", "Store.nextReplicaReadSeed", "Store.GetClient", "getEndPointType",
		"kvStore.GetRegionCache", "kvStore.CheckVisibility",
	}
	for _, f := range part2 {
		ex[f] = "ported; runs against a TiKV store (iterator, workers, RPC and response handling, store wrapper): unit tests on a mock store in tidb-rs crates/store/tests/copr_iter.rs"
	}
	for _, f := range []string{"kvStore.GetTiKVClient", "tikvClient.Close", "tikvClient.CloseAddr", "tikvClient.SendRequest",
		"tikvClient.SendRequestAsync", "tikvClient.SetEventListener"} {
		ex[f] = "client-go tikv.Client passthrough with error conversion; the TiKV client is client-rust (ADR 0008) and nothing on the read path calls it"
	}
	for _, f := range []string{"pkg/store/copr/metrics/metrics.go:init", "pkg/store/copr/metrics/metrics.go:InitMetricsVars"} {
		ex[f] = "metrics of the coprocessor cache (CoprCacheCounter evict/hit/miss), exception A3 with the cache (block D07.4)"
	}
	return ex
}

// ---------------------------------------------------------------- values

func coprHex(b []byte) string { return hex.EncodeToString(b) }

func coprUnhex(s string) []byte {
	b, err := hex.DecodeString(s)
	must(err)
	return b
}

func coprRange(r [2]string) kv.KeyRange {
	return kv.KeyRange{StartKey: coprUnhex(r[0]), EndKey: coprUnhex(r[1])}
}

func coprRanges(rs [][2]string) []kv.KeyRange {
	out := make([]kv.KeyRange, 0, len(rs))
	for _, r := range rs {
		out = append(out, coprRange(r))
	}
	return out
}

func coprRangesOut(rs []kv.KeyRange) [][2]string {
	out := make([][2]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, [2]string{coprHex(r.StartKey), coprHex(r.EndKey)})
	}
	return out
}

// coprField is a settable view of an unexported field.
func coprField(v reflect.Value, name string) reflect.Value {
	f := v.FieldByName(name)
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

type coprShape struct {
	F bool        `json:"f"`
	M int         `json:"m"`
	L bool        `json:"l"`
	R [][2]string `json:"r"`
}

func coprShapeOf(r *copr.KeyRanges) coprShape {
	v := reflect.ValueOf(r).Elem()
	return coprShape{F: !v.FieldByName("first").IsNil(), M: v.FieldByName("mid").Len(), L: !v.FieldByName("last").IsNil(),
		R: coprRangesOut(r.ToRanges())}
}

type coprCluster struct {
	Splits  []string          `json:"splits"`
	Buckets []json.RawMessage `json:"buckets"`
}

type coprRegionOut struct {
	ID    uint64    `json:"id"`
	Conf  uint64    `json:"conf"`
	Ver   uint64    `json:"ver"`
	Start string    `json:"start"`
	End   string    `json:"end"`
	BVer  uint64    `json:"bver"`
	BKeys []string  `json:"bkeys"`
	Store uint64    `json:"store"`
	Peer  [3]uint64 `json:"peer"`
}

type coprEnv struct {
	cache  *copr.RegionCache
	layout []coprRegionOut
	close  func()
}

// coprOpen starts a mock cluster with the given layout and reads the layout back.
func coprOpen(c coprCluster) *coprEnv {
	mockClient, cluster, pdClient, err := testutils.NewMockTiKV("", nil)
	must(err)
	splits := make([][]byte, 0, len(c.Splits))
	for _, s := range c.Splits {
		splits = append(splits, coprUnhex(s))
	}
	_, regionIDs, _ := testutils.BootstrapWithMultiRegions(cluster, splits...)
	for _, raw := range c.Buckets {
		var b struct {
			Idx  int
			Keys []string
		}
		var arr []json.RawMessage
		must(json.Unmarshal(raw, &arr))
		must(json.Unmarshal(arr[0], &b.Idx))
		must(json.Unmarshal(arr[1], &b.Keys))
		keys := make([][]byte, 0, len(b.Keys))
		for _, k := range b.Keys {
			keys = append(keys, coprUnhex(k))
		}
		cluster.SplitRegionBuckets(regionIDs[b.Idx], keys, regionIDs[b.Idx])
	}
	pdCli := tikv.NewCodecPDClient(tikv.ModeTxn, pdClient)
	cache := copr.NewRegionCache(tikv.NewRegionCache(pdCli))

	// The layout, through a separate cache so that the op starts cold like the Go tests.
	lc := tikv.NewRegionCache(pdCli)
	bo := backoff.NewBackofferWithVars(context.Background(), 3000, nil)
	locs, err := lc.BatchLocateKeyRanges(bo.TiKVBackoffer(), []tikv.KeyRange{{}}, tikv.WithNeedBuckets())
	must(err)
	var layout []coprRegionOut
	for _, l := range locs {
		r := coprRegionOut{ID: l.Region.GetID(), Conf: l.Region.GetConfVer(), Ver: l.Region.GetVer(),
			Start: coprHex(l.StartKey), End: coprHex(l.EndKey), BVer: l.GetBucketVersion()}
		if l.Buckets != nil {
			r.BKeys = []string{}
			for _, k := range l.Buckets.Keys {
				r.BKeys = append(r.BKeys, coprHex(k))
			}
		}
		rpc, err := lc.GetTiKVRPCContext(bo.TiKVBackoffer(), l.Region, 0, 0)
		must(err)
		r.Store = rpc.Store.StoreID()
		r.Peer = [3]uint64{rpc.Peer.GetId(), rpc.Peer.GetStoreId(), uint64(rpc.Peer.GetRole())}
		layout = append(layout, r)
	}
	return &coprEnv{cache: cache, layout: layout, close: func() {
		lc.Close()
		cache.Close()
		pdCli.Close()
		pdClient.Close()
		must(mockClient.Close())
	}}
}

type coprReq struct {
	Tp          int64  `json:"tp"`
	StoreType   int    `json:"st"`
	Paging      bool   `json:"paging"`
	MinPaging   uint64 `json:"minp"`
	MaxPaging   uint64 `json:"maxp"`
	LimitSize   uint64 `json:"limit"`
	KeepOrder   bool   `json:"keep"`
	Desc        bool   `json:"desc"`
	BatchSize   int    `json:"batch"`
	BusyMs      int64  `json:"busy"`
	ReadTimeout uint64 `json:"rt"`
	Internal    bool   `json:"internal"`
	Source      string `json:"source"`
	ReplicaRead int    `json:"rr"`
	Priority    int    `json:"pri"`
}

func (r coprReq) request() *kv.Request {
	req := &kv.Request{Tp: r.Tp, StoreType: kv.StoreType(r.StoreType), KeepOrder: r.KeepOrder, Desc: r.Desc,
		LimitSize: r.LimitSize, StoreBatchSize: r.BatchSize, StoreBusyThreshold: time.Duration(r.BusyMs) * time.Millisecond,
		TiKVClientReadTimeout: r.ReadTimeout, ReplicaRead: kv.ReplicaReadType(r.ReplicaRead), Priority: r.Priority}
	req.Paging.Enable = r.Paging
	req.Paging.MinPagingSize = r.MinPaging
	req.Paging.MaxPagingSize = r.MaxPaging
	req.RequestSource = util.RequestSource{RequestSourceInternal: r.Internal, RequestSourceType: r.Source}
	return req
}

type coprBatchedOut struct {
	ID       uint64          `json:"id"`
	Store    uint64          `json:"store"`
	LoadBase bool            `json:"lb"`
	Region   json.RawMessage `json:"region"`
	Peer     [3]uint64       `json:"peer"`
	Task     coprTaskOut     `json:"task"`
}

type coprTaskOut struct {
	ID        uint64            `json:"id"`
	Region    [3]uint64         `json:"region"`
	BVer      uint64            `json:"bver"`
	Ranges    coprShape         `json:"ranges"`
	Chan      int               `json:"chan"` // -1: nil, else the capacity
	StoreType int               `json:"st"`
	Cmd       int               `json:"cmd"`
	Paging    bool              `json:"paging"`
	PagingSz  uint64            `json:"psize"`
	Hint      int               `json:"hint"`
	Busy      int64             `json:"busy"`
	RT        uint64            `json:"rt"`
	Redirect  *uint64           `json:"redirect"`
	Source    string            `json:"source"`
	Str       string            `json:"str"`
	PB        []json.RawMessage `json:"pb"`
	Batched   []coprBatchedOut  `json:"batched"`
}

func coprPBRanges(rs []*coprocessor.KeyRange) [][2]string {
	out := make([][2]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, [2]string{coprHex(r.Start), coprHex(r.End)})
	}
	return out
}

func coprTaskOf(p unsafe.Pointer) coprTaskOut {
	v := reflect.NewAt(coprTaskType, p).Elem()
	region := coprField(v, "region").Interface().(tikv.RegionVerID)
	out := coprTaskOut{
		ID:        coprField(v, "taskID").Interface().(uint64),
		Region:    [3]uint64{region.GetID(), region.GetConfVer(), region.GetVer()},
		BVer:      coprField(v, "bucketsVer").Interface().(uint64),
		Ranges:    coprShapeOf(coprField(v, "ranges").Interface().(*copr.KeyRanges)),
		Chan:      -1,
		StoreType: int(coprField(v, "storeType").Interface().(kv.StoreType)),
		Cmd:       int(coprField(v, "cmdType").Uint()),
		Paging:    coprField(v, "paging").Bool(),
		PagingSz:  coprField(v, "pagingSize").Uint(),
		Hint:      int(coprField(v, "RowCountHint").Int()),
		Busy:      int64(coprField(v, "busyThreshold").Interface().(time.Duration)),
		RT:        coprField(v, "tikvClientReadTimeout").Uint(),
		Source:    coprField(v, "requestSource").Addr().Interface().(*util.RequestSource).GetRequestSource(),
		Str:       coprTaskString(p),
	}
	if ch := coprField(v, "respChan"); !ch.IsNil() {
		out.Chan = ch.Cap()
	}
	if r := coprField(v, "redirect2Replica"); !r.IsNil() {
		x := r.Elem().Uint()
		out.Redirect = &x
	}
	pbs := coprTaskToPBBatchTasks(p)
	sort.Slice(pbs, func(i, j int) bool { return pbs[i].TaskId < pbs[j].TaskId })
	for _, t := range pbs {
		b, err := json.Marshal([]any{t.RegionId, t.RegionEpoch.GetConfVer(), t.RegionEpoch.GetVersion(),
			[3]uint64{t.Peer.GetId(), t.Peer.GetStoreId(), uint64(t.Peer.GetRole())}, coprPBRanges(t.Ranges), t.TaskId})
		must(err)
		out.PB = append(out.PB, b)
	}
	m := coprField(v, "batchTaskList")
	if !m.IsNil() {
		keys := m.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].Uint() < keys[j].Uint() })
		out.Batched = []coprBatchedOut{}
		for _, k := range keys {
			bt := m.MapIndex(k).Elem()
			bt = reflect.NewAt(bt.Type(), unsafe.Pointer(bt.UnsafeAddr())).Elem()
			ri := coprField(bt, "region").Interface().(coprocessor.RegionInfo)
			rb, err := json.Marshal([]any{ri.RegionId, ri.RegionEpoch.GetConfVer(), ri.RegionEpoch.GetVersion(), coprPBRanges(ri.Ranges)})
			must(err)
			peer := coprField(bt, "peer").Elem()
			out.Batched = append(out.Batched, coprBatchedOut{
				ID:       k.Uint(),
				Store:    coprField(bt, "storeID").Uint(),
				LoadBase: coprField(bt, "loadBasedReplicaRetry").Bool(),
				Region:   rb,
				Peer: [3]uint64{coprField(peer, "Id").Uint(), coprField(peer, "StoreId").Uint(),
					uint64(coprField(peer, "Role").Int())},
				Task: coprTaskOf(coprField(bt, "task").UnsafePointer()),
			})
		}
	}
	return out
}

// ---------------------------------------------------------------- exec

func coprExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "kr":
		return coprExecKR(args), nil
	case "bkr":
		var keys []string
		must(json.Unmarshal(args[0], &keys))
		strs := make([]string, 0, len(keys))
		for _, k := range keys {
			strs = append(strs, string(coprUnhex(k)))
		}
		return coprRangesOut(copr.BuildKeyRanges(strs...)), nil
	case "calc":
		var rs [][2]string
		var split *[2]string
		var desc bool
		var which string
		must(json.Unmarshal(args[0], &rs))
		must(json.Unmarshal(args[1], &split))
		must(json.Unmarshal(args[2], &desc))
		must(json.Unmarshal(args[3], &which))
		var sp *coprocessor.KeyRange
		if split != nil {
			sp = &coprocessor.KeyRange{Start: coprUnhex(split[0]), End: coprUnhex(split[1])}
		}
		var res *copr.KeyRanges
		if which == "remain" {
			res = coprCalculateRemain(nil, copr.NewKeyRanges(coprRanges(rs)), sp, desc)
		} else {
			res = coprCalculateRetry(nil, copr.NewKeyRanges(coprRanges(rs)), sp, desc)
		}
		return coprShapeOf(res), nil
	case "split":
		return coprExecSplit(args)
	case "build":
		return coprExecBuild(args)
	case "small":
		var tasks [][2]int
		var numcpu int
		must(json.Unmarshal(args[0], &tasks))
		must(json.Unmarshal(args[1], &numcpu))
		ptrs := make([]unsafe.Pointer, 0, len(tasks))
		var smalls []bool
		for _, t := range tasks {
			v := reflect.New(coprTaskType)
			coprField(v.Elem(), "RowCountHint").SetInt(int64(t[0]))
			if t[1] > 0 {
				m := reflect.MakeMap(coprField(v.Elem(), "batchTaskList").Type())
				et := m.Type().Elem()
				for i := 0; i < t[1]; i++ {
					m.SetMapIndex(reflect.ValueOf(uint64(i+1)), reflect.New(et.Elem()))
				}
				coprField(v.Elem(), "batchTaskList").Set(m)
			}
			ptrs = append(ptrs, v.UnsafePointer())
			smalls = append(smalls, coprIsSmallTask(v.UnsafePointer()))
		}
		n, conc := coprSmallTaskConcurrency(ptrs, numcpu)
		return []any{smalls, n, conc}, nil
	case "reqflags":
		var r coprReq
		must(json.Unmarshal(args[0], &r))
		req := r.request()
		return []bool{coprOptRowHint(req), coprCheckStoreBatchCopr(req)}, nil
	case "pb":
		var pri, iso int
		must(json.Unmarshal(args[0], &pri))
		must(json.Unmarshal(args[1], &iso))
		return []int32{int32(coprPriorityToPB(pri)), int32(coprIsolationLevelToPB(kv.IsoLevel(iso)))}, nil
	case "paging":
		var f string
		var a, b uint64
		must(json.Unmarshal(args[0], &f))
		must(json.Unmarshal(args[1], &a))
		must(json.Unmarshal(args[2], &b))
		if f == "grow" {
			return paging.GrowPagingSize(a, b), nil
		}
		return FormatFloat(paging.CalculateSeekCnt(a)), nil
	}
	panic("copr: unknown op " + op)
}

func coprExecKR(args []json.RawMessage) any {
	var first, last *[2]string
	var mid [][2]string
	var steps [][]json.RawMessage
	must(json.Unmarshal(args[0], &first))
	must(json.Unmarshal(args[1], &mid))
	must(json.Unmarshal(args[2], &last))
	must(json.Unmarshal(args[3], &steps))
	r := copr.NewKeyRanges(coprRanges(mid))
	v := reflect.ValueOf(r).Elem()
	if first != nil {
		f := coprRange(*first)
		coprField(v, "first").Set(reflect.ValueOf(&f))
	}
	if last != nil {
		l := coprRange(*last)
		coprField(v, "last").Set(reflect.ValueOf(&l))
	}
	var out []any
	for _, st := range steps {
		var name string
		must(json.Unmarshal(st[0], &name))
		switch name {
		case "split":
			var key, side string
			must(json.Unmarshal(st[1], &key))
			must(json.Unmarshal(st[2], &side))
			left, right := r.Split(coprUnhex(key))
			if side == "L" {
				r = left
			} else {
				r = right
			}
			out = append(out, coprShapeOf(r))
		case "slice":
			var from, to int
			must(json.Unmarshal(st[1], &from))
			must(json.Unmarshal(st[2], &to))
			r = r.Slice(from, to)
			out = append(out, coprShapeOf(r))
		case "at":
			var i int
			must(json.Unmarshal(st[1], &i))
			out = append(out, coprRangesOut([]kv.KeyRange{r.At(i)})[0])
		case "string":
			out = append(out, r.String())
		case "pb":
			out = append(out, coprPBRanges(r.ToPBRanges()))
		case "len":
			out = append(out, r.Len())
		}
	}
	return out
}

func coprExecSplit(args []json.RawMessage) (any, error) {
	var c coprCluster
	var rs [][2]string
	var fn string
	var limit int
	var needLeader, buckets bool
	must(json.Unmarshal(args[0], &c))
	must(json.Unmarshal(args[1], &rs))
	must(json.Unmarshal(args[2], &fn))
	must(json.Unmarshal(args[3], &limit))
	must(json.Unmarshal(args[4], &needLeader))
	must(json.Unmarshal(args[5], &buckets))
	env := coprOpen(c)
	defer env.close()
	bo := backoff.NewBackofferWithVars(context.Background(), 3000, nil)
	var res []any
	switch fn {
	case "regions":
		out, err := env.cache.SplitRegionRanges(bo, coprRanges(rs), limit)
		if err != nil {
			return map[string]any{"layout": env.layout}, err
		}
		return map[string]any{"layout": env.layout, "res": coprRangesOut(out)}, nil
	case "locs":
		locs, err := env.cache.SplitKeyRangesByLocations(bo, copr.NewKeyRanges(coprRanges(rs)), limit, needLeader, buckets)
		if err != nil {
			return map[string]any{"layout": env.layout}, err
		}
		for _, l := range locs {
			res = append(res, []any{l.Location.Region.GetID(), coprShapeOf(l.Ranges)})
		}
	case "buckets":
		locs, err := env.cache.SplitKeyRangesByBuckets(bo, copr.NewKeyRanges(coprRanges(rs)))
		if err != nil {
			return map[string]any{"layout": env.layout}, err
		}
		for _, l := range locs {
			res = append(res, []any{l.Location.Region.GetID(), coprShapeOf(l.Ranges)})
		}
	}
	return map[string]any{"layout": env.layout, "res": res}, nil
}

func coprExecBuild(args []json.RawMessage) (any, error) {
	var c coprCluster
	var rs [][2]string
	var r coprReq
	var hints *[]int
	var respChan, ignoreRT bool
	must(json.Unmarshal(args[0], &c))
	must(json.Unmarshal(args[1], &rs))
	must(json.Unmarshal(args[2], &r))
	must(json.Unmarshal(args[3], &hints))
	must(json.Unmarshal(args[4], &respChan))
	must(json.Unmarshal(args[5], &ignoreRT))
	env := coprOpen(c)
	defer env.close()
	bo := backoff.NewBackofferWithVars(context.Background(), 3000, nil)
	opt := &coprBuildOpt{req: r.request(), cache: env.cache, respChan: respChan, ignoreTiKVClientReadTimeout: ignoreRT}
	if hints != nil {
		opt.rowHints = *hints
	}
	tasks, err := coprBuildCopTasks(bo, copr.NewKeyRanges(coprRanges(rs)), unsafe.Pointer(opt))
	if err != nil {
		return map[string]any{"layout": env.layout}, err
	}
	out := []coprTaskOut{}
	for _, t := range tasks {
		out = append(out, coprTaskOf(t))
	}
	return map[string]any{"layout": env.layout, "tasks": out}, nil
}

// ---------------------------------------------------------------- generate

func coprKeys(s ...string) []string {
	out := make([]string, 0, len(s))
	for _, k := range s {
		out = append(out, coprHex([]byte(k)))
	}
	return out
}

func coprRs(s ...string) [][2]string {
	var out [][2]string
	for i := 0; i+1 < len(s); i += 2 {
		out = append(out, [2]string{coprHex([]byte(s[i])), coprHex([]byte(s[i+1]))})
	}
	return out
}

func coprR(a, b string) *[2]string {
	r := [2]string{coprHex([]byte(a)), coprHex([]byte(b))}
	return &r
}

func coprBucket(idx int, keys ...string) json.RawMessage {
	b, err := json.Marshal([]any{idx, coprKeys(keys...)})
	must(err)
	return b
}

// coprRandRanges: n sorted, non-overlapping ranges over "a".."z" (the last may be open).
func coprRandRanges(rng *rand.Rand, n int) [][2]string {
	var cuts []int
	for len(cuts) < 2*n {
		cuts = append(cuts, rng.IntN(26))
	}
	sort.Ints(cuts)
	var out [][2]string
	for i := 0; i+1 < len(cuts); i += 2 {
		a, b := cuts[i], cuts[i+1]
		if a == b {
			b = a + 1
		}
		start := []byte{byte('a' + a)}
		if len(out) > 0 && string(coprUnhex(out[len(out)-1][1])) > string(start) {
			continue
		}
		end := []byte{byte('a' + b)}
		if rng.IntN(4) == 0 {
			end = append(end, byte('a'+rng.IntN(26)))
		}
		out = append(out, [2]string{coprHex(start), coprHex(end)})
	}
	// An open end only on a single range: with several ranges, mocktikv's batch region scan
	// answers an open-ended range with "PD server timeout" (a mock limitation, D07.2 spec §5).
	if len(out) == 1 && rng.IntN(5) == 0 {
		out[len(out)-1][1] = ""
	}
	return out
}

func coprRandCluster(rng *rand.Rand) coprCluster {
	n := rng.IntN(6)
	var splits []string
	seen := map[int]bool{}
	var ks []int
	for len(ks) < n {
		k := 1 + rng.IntN(25)
		if !seen[k] {
			seen[k] = true
			ks = append(ks, k)
		}
	}
	sort.Ints(ks)
	bounds := []string{""}
	for _, k := range ks {
		s := string(rune('a' + k))
		splits = append(splits, coprHex([]byte(s)))
		bounds = append(bounds, s)
	}
	bounds = append(bounds, "")
	c := coprCluster{Splits: splits}
	if rng.IntN(2) == 0 {
		for idx := 0; idx < len(bounds)-1; idx++ {
			if rng.IntN(3) == 0 {
				continue
			}
			start, end := bounds[idx], bounds[idx+1]
			lo := 0
			if start != "" {
				lo = int(start[0] - 'a')
			}
			hi := 26
			if end != "" {
				hi = int(end[0] - 'a')
			}
			var keys []string
			if rng.IntN(4) != 0 {
				keys = append(keys, start)
			}
			for k := lo + 1; k < hi; k++ {
				if rng.IntN(3) == 0 {
					keys = append(keys, string(rune('a'+k)))
				}
			}
			if rng.IntN(4) != 0 {
				keys = append(keys, end)
			}
			c.Buckets = append(c.Buckets, coprBucket(idx, keys...))
		}
	}
	return c
}

func coprRandReq(rng *rand.Rand) coprReq {
	r := coprReq{Tp: kv.ReqTypeDAG, StoreType: int(kv.TiKV), MinPaging: paging.MinPagingSize, MaxPaging: paging.MaxPagingSize}
	if rng.IntN(5) == 0 {
		r.Tp = []int64{kv.ReqTypeAnalyze, kv.ReqTypeChecksum, kv.ReqTypeSelect}[rng.IntN(3)]
	}
	if rng.IntN(6) == 0 {
		r.StoreType = int(kv.TiFlash)
	}
	r.Paging = rng.IntN(2) == 0
	if rng.IntN(4) == 0 {
		r.MaxPaging = []uint64{0, 100, 60000, 1 << 20}[rng.IntN(4)]
	}
	if rng.IntN(3) == 0 {
		r.LimitSize = []uint64{1, 100, 128, 129, 300, 1000}[rng.IntN(6)]
	}
	r.KeepOrder = rng.IntN(2) == 0
	r.Desc = rng.IntN(3) == 0
	if rng.IntN(2) == 0 {
		r.BatchSize = rng.IntN(5)
	}
	if rng.IntN(3) == 0 {
		r.BusyMs = int64(rng.IntN(2000))
	}
	if rng.IntN(3) == 0 {
		r.ReadTimeout = uint64(rng.IntN(5000))
	}
	r.Internal = rng.IntN(5) == 0
	if rng.IntN(3) == 0 {
		r.Source = []string{"", "lightning", "br", "stats"}[rng.IntN(4)]
	}
	if rng.IntN(5) == 0 {
		r.ReplicaRead = 1 + rng.IntN(3)
	}
	r.Priority = rng.IntN(4)
	return r
}

func coprGenerate(g *vec.Gen, rng *rand.Rand) {
	// from: pkg/store/copr/key_ranges_test.go TestCopRanges
	abcdef := coprRs("a", "b", "c", "d", "e", "f")
	all := []json.RawMessage{
		json.RawMessage(`["string"]`), json.RawMessage(`["len"]`), json.RawMessage(`["pb"]`),
		json.RawMessage(`["at",0]`), json.RawMessage(`["at",1]`), json.RawMessage(`["at",2]`),
		json.RawMessage(`["slice",0,3]`),
	}
	g.Add("TestCopRanges", "kr", nil, abcdef, nil, all)
	g.Add("TestCopRanges", "kr", &abcdef[0], abcdef[1:], nil, all)
	g.Add("TestCopRanges", "kr", nil, abcdef[:2], &abcdef[2], all)
	g.Add("TestCopRanges", "kr", &abcdef[0], abcdef[1:2], &abcdef[2], all)
	for i := 0; i <= 3; i++ {
		for j := i; j <= 3; j++ {
			sl := []json.RawMessage{json.RawMessage(`["slice",` + itoa(i) + `,` + itoa(j) + `]`)}
			g.Add("TestCopRanges", "kr", &abcdef[0], abcdef[1:2], &abcdef[2], sl)
			g.Add("TestCopRanges", "kr", nil, abcdef, nil, sl)
		}
	}
	// from: pkg/store/copr/key_ranges_test.go TestCopRangeSplit
	first := coprR("a", "b")
	mid := coprRs("c", "d", "e", "g", "l", "o")
	last := coprR("q", "t")
	for _, k := range []string{"a", "b", "c", "d", "f", "g", "h", "m", "o", "p", "r", "t", "u"} {
		for _, side := range []string{"L", "R"} {
			st := []json.RawMessage{json.RawMessage(`["split","` + coprHex([]byte(k)) + `","` + side + `"]`), json.RawMessage(`["string"]`)}
			g.Add("TestCopRangeSplit", "kr", nil, mid, nil, st)
			g.Add("TestCopRangeSplit", "kr", first, mid, nil, st)
			g.Add("TestCopRangeSplit", "kr", first, mid, last, st)
		}
	}
	// Random scripts: splits and slices of every shape.
	for n := 0; n < 400; n++ {
		mid := coprRandRanges(rng, 1+rng.IntN(5))
		var f, l *[2]string
		if rng.IntN(2) == 0 {
			f = coprR("A", "B")
		}
		if rng.IntN(2) == 0 {
			l = coprR("zz", "zzz")
		}
		size := len(mid)
		if f != nil {
			size++
		}
		if l != nil {
			size++
		}
		var steps []json.RawMessage
		for s := 0; s < 4; s++ {
			switch rng.IntN(4) {
			case 0, 1:
				k := []byte{byte('a' + rng.IntN(26))}
				if rng.IntN(3) == 0 {
					k = append(k, byte('a'+rng.IntN(26)))
				}
				side := "L"
				if rng.IntN(2) == 0 {
					side = "R"
				}
				steps = append(steps, json.RawMessage(`["split","`+coprHex(k)+`","`+side+`"]`))
				size = -1 // unknown afterwards: only splits and reads follow
			case 2:
				if size >= 0 {
					from := rng.IntN(size + 1)
					to := from + rng.IntN(size-from+1)
					steps = append(steps, json.RawMessage(`["slice",`+itoa(from)+`,`+itoa(to)+`]`))
					size = to - from
				}
			case 3:
				steps = append(steps, json.RawMessage(`["string"]`), json.RawMessage(`["pb"]`), json.RawMessage(`["len"]`))
				if size > 0 {
					steps = append(steps, json.RawMessage(`["at",`+itoa(rng.IntN(size))+`]`))
				}
			}
		}
		g.Add("rand/kr", "kr", f, mid, l, steps)
	}

	// BuildKeyRanges
	g.Add("sys/bkr", "bkr", coprKeys())
	g.Add("sys/bkr", "bkr", coprKeys("a", "b"))
	g.Add("sys/bkr", "bkr", coprKeys("a", "b", "c"))
	g.Add("sys/bkr", "bkr", coprKeys("a"))
	for n := 0; n < 20; n++ {
		var ks []string
		for i := 0; i < 2*rng.IntN(5); i++ {
			ks = append(ks, string(rune('a'+rng.IntN(26))))
		}
		g.Add("rand/bkr", "bkr", coprKeys(ks...))
	}

	// from: pkg/store/copr/coprocessor_test.go TestCalculateRetry, TestCalculateRemain
	aceg := coprRs("a", "c", "e", "g")
	for _, c := range []struct {
		which string
		split *[2]string
		desc  bool
	}{
		{"retry", coprR("b", "c"), false}, {"retry", coprR("e", "f"), true}, {"retry", coprR("b", "f"), false},
		{"retry", coprR("b", "f"), true}, {"retry", coprR("a", "g"), false}, {"retry", coprR("a", "g"), true},
		{"retry", nil, false}, {"retry", nil, true},
	} {
		g.Add("TestCalculateRetry", "calc", aceg, c.split, c.desc, c.which)
	}
	for _, c := range []struct {
		split *[2]string
		desc  bool
	}{
		{coprR("a", "b"), false}, {coprR("f", "g"), true}, {coprR("a", "f"), false}, {coprR("b", "g"), true},
		{coprR("a", "g"), false}, {coprR("a", "g"), true}, {nil, false}, {nil, true},
	} {
		g.Add("TestCalculateRemain", "calc", aceg, c.split, c.desc, "remain")
	}
	for n := 0; n < 300; n++ {
		rs := coprRandRanges(rng, 1+rng.IntN(4))
		a := rng.IntN(26)
		b := a + rng.IntN(26-a)
		sp := coprR(string(rune('a'+a)), string(rune('a'+b)))
		if rng.IntN(8) == 0 {
			sp = nil
		}
		which := "remain"
		if rng.IntN(2) == 0 {
			which = "retry"
		}
		g.Add("rand/calc", "calc", rs, sp, rng.IntN(2) == 0, which)
	}

	// from: pkg/store/copr/coprocessor_test.go TestSplitKeyRangesByLocationsWithoutBuckets, TestSplitKeyRanges
	gnt := coprCluster{Splits: coprKeys("g", "n", "t")}
	for _, rs := range [][][2]string{
		coprRs("a", "c"), coprRs("h", "y"), coprRs("h", "n"), coprRs("s", "s"), coprRs("s", "z"), coprRs("t", "t"),
		coprRs("t", "u"), coprRs("u", "z"), coprRs("a", "z"), coprRs("t", "tt"), coprRs("t", "zz"),
		coprRs("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"),
		coprRs("a", "b", "v", "w"), coprRs("a", "c", "d", "e", "g", "n", "p", "q", "y", "z"),
		coprRs("a", "b", "b", "h", "h", "m", "n", "t", "v", "w"), coprRs("a", "c", "c", "n", "n", "t", "t", "z"),
		{{coprHex([]byte("a")), ""}},
	} {
		for _, limit := range []int{copr.UnspecifiedLimit, 0, 1, 2, 3, 5} {
			g.Add("TestSplitKeyRangesByLocationsWithoutBuckets", "split", gnt, rs, "locs", limit, true, false)
		}
		g.Add("TestSplitKeyRanges", "split", gnt, rs, "regions", copr.UnspecifiedLimit, true, false)
		g.Add("TestSplitKeyRanges", "split", gnt, rs, "regions", 3, true, false)
		g.Add("TestSplitKeyRanges", "split", gnt, rs, "buckets", 0, false, true)
	}
	// from: pkg/store/copr/coprocessor_test.go TestBuildTasksByBuckets (layout)
	nx := coprCluster{Splits: coprKeys("n", "x"), Buckets: []json.RawMessage{
		coprBucket(0, "", "c", "g", "k", "n"), coprBucket(1, "n", "t", "x"), coprBucket(2, "x", "")}}
	tikvReq := coprReq{Tp: kv.ReqTypeDAG}
	flashReq := coprReq{Tp: kv.ReqTypeDAG, StoreType: int(kv.TiFlash)}
	for _, rs := range [][][2]string{
		coprRs("a", "b", "c", "d", "h", "i", "k", "n"), coprRs("o", "p", "u", "x"), {{coprHex([]byte("x")), ""}},
		coprRs("a", "b", "c", "d", "h", "i", "k", "n", "o", "p", "u", "x"), coprRs("a", "d", "e", "h"),
		coprRs("a", "c", "d", "f"), coprRs("b", "p", "q", "z"), coprRs("a", "z"), coprRs("c", "d", "e", "f", "g", "h"),
		coprRs("e", "f", "f", "g", "g", "h"), coprRs("n", "o", "t", "u"), coprRs("a", "c", "c", "f"),
	} {
		g.Add("TestBuildTasksByBuckets", "build", nx, rs, tikvReq, nil, true, false)
		g.Add("TestBuildTasksByBuckets", "split", nx, rs, "buckets", 0, false, true)
	}
	// from: pkg/store/copr/coprocessor_test.go TestBuildTasksWithoutBuckets
	for _, rs := range [][][2]string{
		coprRs("a", "c"), coprRs("g", "n"), coprRs("m", "n"), coprRs("a", "k"), coprRs("a", "x"),
		coprRs("a", "b", "b", "c"), coprRs("a", "b", "e", "f"), coprRs("g", "n", "o", "p"), coprRs("h", "k", "m", "p"),
	} {
		g.Add("TestBuildTasksWithoutBuckets", "build", gnt, rs, tikvReq, nil, true, false)
		g.Add("TestBuildTasksWithoutBuckets", "build", gnt, rs, flashReq, nil, true, false)
		desc := tikvReq
		desc.Desc = true
		g.Add("TestBuildTasksWithoutBuckets", "build", gnt, rs, desc, nil, true, false)
	}
	// from: pkg/store/copr/coprocessor_test.go TestBuildPagingTasks, TestBuildPagingTasksDisablePagingForSmallLimit
	pg := coprReq{Tp: kv.ReqTypeDAG, Paging: true, MinPaging: paging.MinPagingSize}
	g.Add("TestBuildPagingTasks", "build", gnt, coprRs("a", "c"), pg, nil, true, false)
	pg.LimitSize = 1
	g.Add("TestBuildPagingTasksDisablePagingForSmallLimit", "build", gnt, coprRs("a", "c"), pg, nil, true, false)
	// from: pkg/store/copr/coprocessor_test.go TestBuildCopTasksWithRowCountHint, TestBatchStoreCoprOnlySendToLeader
	hint := func(h ...int) *[]int { return &h }
	rh := coprRs("a", "c", "d", "e", "h", "x", "y", "z")
	g.Add("TestBuildCopTasksWithRowCountHint", "build", gnt, rh, tikvReq, hint(1, 1, 3, int(copr.CopSmallTaskRow)), false, false)
	g.Add("TestBuildCopTasksWithRowCountHint", "build", gnt, rh, tikvReq, hint(1, 1, 3, 3), false, false)
	g.Add("TestBuildCopTasksWithRowCountHint", "build", gnt, coprRs("a", "z"), tikvReq, hint(10), false, false)
	bt := coprReq{Tp: kv.ReqTypeDAG, BatchSize: 3, BusyMs: 1000}
	g.Add("TestBatchStoreCoprOnlySendToLeader", "build", gnt, rh, bt, hint(1, 1, 3, 3), false, false)
	bt.BatchSize = 0
	g.Add("TestBatchStoreCoprOnlySendToLeader", "build", gnt, rh, bt, hint(1, 1, 3, 3), false, false)

	// Random clusters, ranges and requests.
	for n := 0; n < 450; n++ {
		c := coprRandCluster(rng)
		rs := coprRandRanges(rng, 1+rng.IntN(5))
		r := coprRandReq(rng)
		if r.StoreType == int(kv.TiDB) {
			r.StoreType = int(kv.TiKV)
		}
		var h *[]int
		if rng.IntN(2) == 0 {
			hs := make([]int, len(rs))
			for i := range hs {
				hs[i] = rng.IntN(80) - 5
			}
			if rng.IntN(10) == 0 {
				hs = hs[:len(hs)/2]
			}
			h = &hs
		}
		g.Add("rand/build", "build", c, rs, r, h, rng.IntN(3) != 0, rng.IntN(4) == 0)
	}
	for n := 0; n < 300; n++ {
		c := coprRandCluster(rng)
		rs := coprRandRanges(rng, 1+rng.IntN(5))
		fn := []string{"locs", "buckets", "regions"}[rng.IntN(3)]
		limit := []int{copr.UnspecifiedLimit, copr.UnspecifiedLimit, 0, 1, 2, 4}[rng.IntN(6)]
		g.Add("rand/split", "split", c, rs, fn, limit, rng.IntN(2) == 0, rng.IntN(2) == 0)
	}

	// from: pkg/store/copr/coprocessor_test.go TestBasicSmallTaskConc, TestSmallTaskConcurrencyLimit
	for _, h := range []int{-1, 0, 1, 6, int(copr.CopSmallTaskRow), int(copr.CopSmallTaskRow) + 1} {
		g.Add("TestBasicSmallTaskConc", "small", [][2]int{{h, 0}}, 16)
	}
	g.Add("TestBasicSmallTaskConc", "small", [][2]int{}, 16)
	thousand := make([][2]int, 1000)
	for i := range thousand {
		thousand[i] = [2]int{1, 0}
	}
	g.Add("TestSmallTaskConcurrencyLimit", "small", thousand, 1)
	g.Add("TestSmallTaskConcurrencyLimit", "small", thousand, 0)
	for n := 0; n < 250; n++ {
		ts := make([][2]int, rng.IntN(60))
		for i := range ts {
			ts[i] = [2]int{rng.IntN(100) - 10, 0}
			if rng.IntN(5) == 0 {
				ts[i][1] = 1 + rng.IntN(3)
			}
		}
		g.Add("rand/small", "small", ts, rng.IntN(6)-1)
	}
	for n := 1; n <= 64; n++ {
		ts := make([][2]int, n)
		for i := range ts {
			ts[i] = [2]int{1, 0}
		}
		g.Add("sys/small", "small", ts, 64)
	}

	for n := 0; n < 150; n++ {
		r := coprRandReq(rng)
		if rng.IntN(4) == 0 {
			r.StoreType = int(kv.TiDB)
		}
		g.Add("rand/reqflags", "reqflags", r)
	}
	for pri := -1; pri <= 3; pri++ {
		for iso := -1; iso <= 3; iso++ {
			g.Add("sys/pb", "pb", pri, iso)
		}
	}

	for _, a := range []uint64{0, 1, 64, 127, 128, 129, 1000, 25000, 25001, 49999, 50000, 50001, 1 << 40, 1<<63 + 5, ^uint64(0)} {
		for _, b := range []uint64{0, 100, 49999, 50000, 50001, 100000, ^uint64(0)} {
			g.Add("sys/paging", "paging", "grow", a, b)
		}
		g.Add("sys/paging", "paging", "seek", a, 0)
	}
	for n := 0; n < 100; n++ {
		g.Add("rand/paging", "paging", "seek", rng.Uint64N(100000), 0)
		g.Add("rand/paging", "paging", "grow", rng.Uint64N(200000), rng.Uint64N(200000))
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
