package domains

// Domain "kv": pkg/kv (block D07.0, read-path scope; see scripts/blocks/rules.py).
//
// Values:
//   - keys and byte strings are hex;
//   - a handle is a spec: {"t":"int","v":n} | {"t":"common","enc":hex,"offs":[...]}
//     | {"t":"part","pid":n,"h":spec}. A common handle is built by NewCommonHandle from
//     codec.EncodeKey bytes (copied into an exact-capacity slice, so cap == len); its
//     spec carries the column end offsets NewCommonHandle computed (from EncodedCol),
//     because the Rust port builds it from them until codec.CutOne (D01.8) is ported;
//   - handle outputs (handleOut) record every method that does not panic for the kind;
//     Data as int64s for handles without a CommonHandle inside, the datum kinds for the
//     others; String when it is valid UTF-8 ("str"), else only its validity ("str_bad":
//     the Rust port returns a Rust String, lossy for such bytes);
//   - map scripts (handle_map, mem_aware_map) are sequences of steps; Range results are
//     sorted (Go's map order is random, M-MAPORDER);
//   - RunInNewTxn, the cache DB, NextUntil, GetValue and BatchGetValue run against mock
//     storages driven by the script in the args (the same mocks are written in the Rust
//     replay).

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/codec"
	tikvstore "github.com/tikv/client-go/v2/kv"
	"github.com/tikv/client-go/v2/oracle"
	"github.com/tikv/client-go/v2/tikv"
	"github.com/tikv/client-go/v2/util"
)

const kvSeed = 707

//go:embed testdata/kv_literals.tsv
var kvLiteralsTSV string

// kvMinedFuncs: the test functions of pkg/kv whose literals feed the generator (the
// tests of X / DEF.MPP functions are left out: TestIsPoint, TestSetCDCWriteSource,
// TestSetLossyDDLReorgSource, TestMppVersion, TestExchangeCompressionMode).
var kvMinedFuncs = []string{"TestPartialNext", "TestBasicFunc", "TestHandle", "TestPaddingHandle",
	"TestHandleMap", "TestHandleMapWithPartialHandle", "TestMemAwareHandleMapWithPartialHandle",
	"TestKeyRangeDefinition", "TestBackOff", "TestRetryExceedCountError", "TestInnerTxnStartTsBox",
	"TestIsRequestTypeSupported", "TestVersion"}

// KVMinedTests lists the mined test functions (govec_test.go minedGoTests).
func KVMinedTests() []string { return kvMinedFuncs }

func kvLiterals() []datumLit {
	keep := map[string]bool{}
	for _, f := range kvMinedFuncs {
		keep[f] = true
	}
	var out []datumLit
	for _, l := range parseDatumLiterals(kvLiteralsTSV) {
		if keep[l.fn] {
			out = append(out, l)
		}
	}
	return out
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "kv",
		Seed:     kvSeed,
		Coverage: kvCoverage,
		Blocks:   []string{"D07.0"},
		Errors: []vec.NamedErr{
			{Name: "ErrNotExist", Err: kv.ErrNotExist},
			{Name: "ErrTxnRetryable", Err: kv.ErrTxnRetryable},
			{Name: "ErrWriteConflict", Err: kv.ErrWriteConflict},
			{Name: "ErrWriteConflictInTiDB", Err: kv.ErrWriteConflictInTiDB},
			{Name: "ErrLockExpire", Err: kv.ErrLockExpire},
		},
		Exempt: map[string]string{
			"NewResourceGroupTagBuilder":               "parser.Digest (D02.2 digester) not ported",
			"ResourceGroupTagBuilder.SetSQLDigest":     "parser.Digest (D02.2 digester) not ported",
			"ResourceGroupTagBuilder.SetPlanDigest":    "parser.Digest (D02.2 digester) not ported",
			"ResourceGroupTagBuilder.BuildProtoTagger": "tikvrpc.Request (store driver, D07.1)",
			"ResourceGroupTagBuilder.Build":            "tikvrpc.Request (store driver, D07.1)",
			"ResourceGroupTagBuilder.EncodeTagWithKey": "resourcegrouptag.GetResourceGroupLabelByKey needs rowindexcodec (D01.11) and parser.Digest",
			"decodeTableID":                            "only called by EncodeTagWithKey",
			"PrintLongTimeInternalTxn":                 "only logs; called by GetMinInnerTxnStartTS (covered)",
		},
		Gzip:     true,
		Generate: kvGenerate,
		Exec:     kvExec,
	})
}

var kvCoverage = map[string]vec.Coverage{
	"key":            {Min: 300, GoFuncs: []string{"Key.Next", "Key.PrefixNext", "Key.Clone", "Key.String"}},
	"key2":           {Min: 300, GoFuncs: []string{"Key.Cmp", "Key.HasPrefix"}},
	"key_range_mem":  {Min: 50, GoFuncs: []string{"KeyRangeSliceMemUsage"}},
	"handle":         {Min: 300, GoFuncs: handleFuncs},
	"handle_call":    {Min: 100, GoFuncs: []string{"IntHandle.NumCols", "IntHandle.EncodedCol", "CommonHandle.IntValue"}},
	"handle2":        {Min: 500, GoFuncs: []string{"IntHandle.Equal", "IntHandle.Compare", "CommonHandle.Equal", "CommonHandle.Compare", "PartitionHandle.Equal", "PartitionHandle.Compare", "NewPartitionHandle"}},
	"handle_map":     {Min: 100, GoFuncs: []string{"NewHandleMap", "HandleMap.Get", "HandleMap.Set", "HandleMap.Delete", "HandleMap.Len", "HandleMap.MemUsage", "HandleMap.Range", "calcIntsMemUsage", "calcStrsMemUsage"}},
	"mem_aware_map":  {Min: 100, GoFuncs: []string{"NewMemAwareHandleMap", "MemAwareHandleMap.Get", "MemAwareHandleMap.Set", "MemAwareHandleMap.Range", "newMemAwareMap"}},
	"key_ranges":     {Min: 200, GoFuncs: []string{"NewPartitionedKeyRanges", "NewNonPartitionedKeyRanges", "NewPartitionedKeyRangesWithHints", "NewNonParitionedKeyRangesWithHint", "KeyRanges.FirstPartitionRange", "KeyRanges.SetToNonPartitioned", "KeyRanges.AppendSelfTo", "KeyRanges.SortByFunc", "KeyRanges.ForEachPartitionWithErr", "KeyRanges.PartitionNum", "KeyRanges.IsFullySorted"}},
	"req_supported":  {Min: 300, GoFuncs: []string{"RequestTypeSupportedChecker.IsRequestTypeSupported", "RequestTypeSupportedChecker.supportExpr"}},
	"err_check":      {Min: 20, GoFuncs: []string{"IsTxnRetryableError", "IsErrNotFound"}},
	"replica_read":   {Min: 7, GoFuncs: []string{"ReplicaReadType.IsFollowerRead", "ReplicaReadType.IsClosestRead"}},
	"source_type":    {Min: 4, GoFuncs: []string{"GetInternalSourceType"}},
	"txn_scope":      {Min: 6, GoFuncs: []string{"NewDefaultTxnScopeVar", "NewGlobalTxnScopeVar", "NewLocalTxnScopeVar", "newTxnScopeVar", "TxnScopeVar.GetVarValue", "TxnScopeVar.GetTxnScope"}},
	"misc":           {Min: 5, GoFuncs: []string{"NewVersion", "GetNewestMppVersion", "NewValueEntry", "BatchGetToGetOptions"}},
	"back_off":       {Min: 8, GoFuncs: []string{"BackOff"}},
	"run_in_new_txn": {Min: 100, GoFuncs: []string{"RunInNewTxn", "setRequestSourceForInnerTxn", "innerTxnStartTsBox.storeInnerTxnTS", "innerTxnStartTsBox.deleteInnerTxnTS", "innerTxnStartTsBox.getMinStartTS", "GetMinInnerTxnStartTS", "IsTxnRetryableError", "BackOff"}},
	"set_rg":         {Min: 3, GoFuncs: []string{"SetTxnResourceGroup"}},
	"empty":          {Min: 1, GoFuncs: []string{"EmptyIterator.Valid", "EmptyIterator.Key", "EmptyIterator.Value", "EmptyIterator.Next", "EmptyIterator.Close", "EmptyRetriever.Get", "EmptyRetriever.Iter", "EmptyRetriever.IterReverse"}},
	"next_until":     {Min: 50, GoFuncs: []string{"NextUntil"}},
	"get_value":      {Min: 30, GoFuncs: []string{"GetValue", "BatchGetValue"}},
}

var handleFuncs = []string{"IntHandle.Copy", "IntHandle.IsInt", "IntHandle.IntValue", "IntHandle.Next", "IntHandle.Encoded",
	"IntHandle.Len", "IntHandle.Data", "IntHandle.String", "IntHandle.MemUsage", "IntHandle.ExtraMemSize",
	"CommonHandle.Copy", "CommonHandle.IsInt", "CommonHandle.Next", "CommonHandle.Encoded", "CommonHandle.Len",
	"CommonHandle.NumCols", "CommonHandle.EncodedCol", "CommonHandle.MemUsage", "CommonHandle.ExtraMemSize",
	"NewCommonHandle", "CommonHandle.Data", "CommonHandle.String",
	"PartitionHandle.Copy", "PartitionHandle.MemUsage", "PartitionHandle.ExtraMemSize", "NewPartitionHandle"}

// ---------------------------------------------------------------- handles

type handleSpec struct {
	T    string      `json:"t"`
	V    int64       `json:"v,omitempty"`
	Enc  string      `json:"enc,omitempty"`
	Offs []uint16    `json:"offs,omitempty"`
	Pid  int64       `json:"pid,omitempty"`
	H    *handleSpec `json:"h,omitempty"`
}

func specOf(h kv.Handle) handleSpec {
	switch x := h.(type) {
	case kv.IntHandle:
		return handleSpec{T: "int", V: int64(x)}
	case *kv.CommonHandle:
		offs := []uint16{}
		end := uint16(0)
		for i := 0; i < x.NumCols(); i++ {
			end += uint16(len(x.EncodedCol(i)))
			offs = append(offs, end)
		}
		return handleSpec{T: "common", Enc: hex.EncodeToString(x.Encoded()), Offs: offs}
	case kv.PartitionHandle:
		inner := specOf(x.Handle)
		return handleSpec{T: "part", Pid: x.PartitionID, H: &inner}
	}
	panic(fmt.Sprintf("unknown handle %T", h))
}

func handleOfSpec(s handleSpec) kv.Handle {
	switch s.T {
	case "int":
		return kv.IntHandle(s.V)
	case "common":
		b, err := hex.DecodeString(s.Enc)
		if err != nil {
			panic(err)
		}
		// The original encoded bytes (before NewCommonHandle's padding), exact capacity.
		enc := make([]byte, len(b))
		copy(enc, b)
		ch, err := kv.NewCommonHandle(enc)
		if err != nil {
			panic(err)
		}
		return ch
	case "part":
		return kv.NewPartitionHandle(s.Pid, handleOfSpec(*s.H))
	}
	panic("bad handle spec " + s.T)
}

func tryHandle(s handleSpec) (h kv.Handle, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return handleOfSpec(s), true
}

func hasCommon(h kv.Handle) bool {
	switch x := h.(type) {
	case *kv.CommonHandle:
		return true
	case kv.PartitionHandle:
		return hasCommon(x.Handle)
	}
	return false
}

func handleOut(h kv.Handle) map[string]any {
	out := map[string]any{
		"spec":   specOf(h),
		"is_int": h.IsInt(),
		"len":    h.Len(),
		"enc":    hex.EncodeToString(h.Encoded()),
		"mem":    h.MemUsage(),
		"extra":  h.ExtraMemSize(),
	}
	if h.IsInt() {
		out["int"] = h.IntValue()
	} else {
		out["ncols"] = h.NumCols()
		cols := []string{}
		for i := 0; i < h.NumCols(); i++ {
			cols = append(cols, hex.EncodeToString(h.EncodedCol(i)))
		}
		out["cols"] = cols
	}
	if str := h.String(); utf8.ValidString(str) {
		out["str"] = str
	} else {
		out["str_bad"] = true
	}
	d, err := h.Data()
	if err != nil {
		panic(err)
	}
	if !hasCommon(h) {
		var ds []int64
		for _, x := range d {
			ds = append(ds, x.GetInt64())
		}
		out["data"] = ds
	} else {
		ks := []int{}
		for _, x := range d {
			ks = append(ks, int(x.Kind()))
		}
		out["data_kinds"] = ks
	}
	return out
}

// commonEncoded: the codec.EncodeKey bytes of a few datums.
func commonEncoded(rng *rand.Rand, lits []datumLit) string {
	n := 1 + rng.IntN(3)
	var ds []types.Datum
	for i := 0; i < n; i++ {
		switch rng.IntN(4) {
		case 0:
			ds = append(ds, types.NewIntDatum(rng.Int64N(1<<20)-1<<19))
		case 1:
			ds = append(ds, types.NewStringDatum(randLitStr(rng, lits)))
		case 2:
			ds = append(ds, types.NewBytesDatum([]byte{byte(rng.IntN(256)), 0xff, 0}))
		default:
			ds = append(ds, types.NewUintDatum(rng.Uint64()))
		}
	}
	b, err := codec.EncodeKey(time.UTC, nil, ds...)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func randLitStr(rng *rand.Rand, lits []datumLit) string {
	for tries := 0; tries < 10; tries++ {
		l := lits[rng.IntN(len(lits))]
		if l.str != nil {
			return *l.str
		}
	}
	return "abc"
}

func randHandle(rng *rand.Rand, lits []datumLit, depth int) handleSpec {
	switch k := rng.IntN(10); {
	case k < 4:
		vals := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 100, 1 << 32}
		if rng.IntN(2) == 0 {
			return handleSpec{T: "int", V: vals[rng.IntN(len(vals))]}
		}
		return handleSpec{T: "int", V: rng.Int64N(1000) - 500}
	case k < 7:
		enc := commonEncoded(rng, lits)
		if rng.IntN(8) == 0 {
			// short: padded to 9 bytes by NewCommonHandle
			enc = []string{"", "00", "0000", "000000000000"}[rng.IntN(4)]
		}
		return specOf(handleOfSpec(handleSpec{T: "common", Enc: enc}))
	default:
		if depth > 1 {
			return handleSpec{T: "int", V: rng.Int64N(10)}
		}
		inner := randHandle(rng, lits, depth+1)
		return handleSpec{T: "part", Pid: rng.Int64N(4), H: &inner}
	}
}

// ---------------------------------------------------------------- mocks

type kvMockTxn struct {
	kv.Transaction // unused methods panic (nil)
	startTS        uint64
	opts           map[int]any
	commitErr      error
	rolledBack     bool
	committed      bool
}

func (t *kvMockTxn) StartTS() uint64            { return t.startTS }
func (t *kvMockTxn) SetOption(opt int, val any) { t.opts[opt] = val }
func (t *kvMockTxn) Rollback() error            { t.rolledBack = true; return nil }
func (t *kvMockTxn) Commit(context.Context) error {
	t.committed = true
	return t.commitErr
}

type kvMockStore struct {
	kv.Storage
	nextTS  uint64
	plan    []txnStep
	attempt int
	begins  int
	txns    []*kvMockTxn
}

// txnStep: what the attempt-th transaction does: f's error and Commit's error (names).
type txnStep struct {
	F      string `json:"f"`
	Commit string `json:"c"`
	Begin  string `json:"b,omitempty"`
}

func errByName(n string) error {
	switch n {
	case "":
		return nil
	case "retryable":
		return kv.ErrTxnRetryable.FastGenByArgs()
	case "write_conflict":
		return kv.ErrWriteConflict.FastGenByArgs("1", "2", "3", "4", "5", "6", "7")
	case "write_conflict_tidb":
		return kv.ErrWriteConflictInTiDB.FastGenByArgs("x")
	case "not_exist":
		return kv.ErrNotExist
	case "lock_expire":
		return kv.ErrLockExpire.FastGenByArgs()
	default:
		return errors.New(n)
	}
}

func (s *kvMockStore) Begin(...tikv.TxnOption) (kv.Transaction, error) {
	s.begins++
	step := s.plan[min(s.attempt, len(s.plan)-1)]
	if e := errByName(step.Begin); e != nil {
		return nil, e
	}
	s.nextTS += 1 << 18
	t := &kvMockTxn{startTS: s.nextTS, opts: map[int]any{}, commitErr: errByName(step.Commit)}
	s.txns = append(s.txns, t)
	return t, nil
}

func goContext() context.Context { return context.Background() }

func withRequestSource(ctx context.Context, internal bool, source, explicit string) context.Context {
	return context.WithValue(ctx, util.RequestSourceKey, util.RequestSource{
		RequestSourceInternal: internal, RequestSourceType: source, ExplicitRequestSourceType: explicit})
}

// ---------------------------------------------------------------- generator

func kvGenerate(g *vec.Gen, rng *rand.Rand) {
	lits := kvLiterals()
	var keys [][]byte
	var keySrc []string
	for _, l := range lits {
		if l.str != nil {
			keys = append(keys, []byte(*l.str))
			keySrc = append(keySrc, l.fn)
		}
	}
	for _, k := range [][]byte{nil, {}, {0}, {0xff}, {0xff, 0xff}, {1, 0xff}, {0xfe, 0xff, 0xff}, {0, 0}} {
		keys = append(keys, k)
	}
	// All-0xff keys of every length up to 40: PrefixNext appends a byte with Go's growth
	// rule (size classes: 9 -> 24, 17 -> 48, 33 -> 80, ...).
	for n := 1; n <= 40; n++ {
		keys = append(keys, bytesRepeat(0xff, n))
	}
	for len(keys) < 320 {
		n := rng.IntN(12)
		b := make([]byte, n)
		for i := range b {
			switch rng.IntN(4) {
			case 0:
				b[i] = 0xff
			case 1:
				b[i] = 0
			default:
				b[i] = byte(rng.IntN(256))
			}
		}
		keys = append(keys, b)
	}
	// key ops
	for i, k := range keys {
		src := "rand/key"
		if i < len(keySrc) {
			src = keySrc[i]
		}
		g.Add(src, "key", hex.EncodeToString(k))
	}
	for i := 0; i < 400; i++ {
		a, b := keys[rng.IntN(len(keys))], keys[rng.IntN(len(keys))]
		if rng.IntN(3) == 0 && len(a) > 0 {
			b = a[:rng.IntN(len(a)+1)]
		}
		g.Add("rand/key2", "key2", hex.EncodeToString(a), hex.EncodeToString(b))
	}
	for i := 0; i < 60; i++ {
		n := rng.IntN(5)
		var rs [][2]string
		for j := 0; j < n; j++ {
			rs = append(rs, [2]string{hex.EncodeToString(keys[rng.IntN(len(keys))]), hex.EncodeToString(keys[rng.IntN(len(keys))])})
		}
		g.Add("TestKeyRangeDefinition", "key_range_mem", rs)
	}
	// handles
	for _, l := range lits {
		if l.num != nil && (l.fn == "TestHandle" || l.fn == "TestHandleMap" || l.fn == "TestPaddingHandle") {
			g.Add(l.fn, "handle", handleSpec{T: "int", V: *l.num})
		}
		if l.str != nil && l.fn == "TestPaddingHandle" {
			if h, ok := tryHandle(handleSpec{T: "common", Enc: hex.EncodeToString([]byte(*l.str))}); ok {
				g.Add(l.fn, "handle", specOf(h))
			} else {
				// not a codec key: as the value of an int-keyed... record it as a string key op
				g.Add(l.fn, "key", hex.EncodeToString([]byte(*l.str)))
			}
		}
	}
	var handles []handleSpec
	for i := 0; i < 400; i++ {
		h := randHandle(rng, lits, 0)
		handles = append(handles, h)
		g.Add("rand/handle", "handle", h)
	}
	for _, m := range []string{"num_cols", "encoded_col", "int_value"} {
		for i := 0; i < 40; i++ {
			g.Add("rand/handle_call", "handle_call", m, handles[rng.IntN(len(handles))], rng.IntN(3))
		}
	}
	for i := 0; i < 700; i++ {
		a := handles[rng.IntN(len(handles))]
		b := handles[rng.IntN(len(handles))]
		if rng.IntN(4) == 0 {
			b = a
		}
		g.Add("rand/handle2", "handle2", a, b)
	}
	// handle maps
	mapScript := func(n int) []map[string]any {
		var steps []map[string]any
		pool := handles[:20+rng.IntN(len(handles)-20)]
		for j := 0; j < n; j++ {
			h := pool[rng.IntN(len(pool))]
			switch rng.IntN(6) {
			case 0, 1, 2:
				steps = append(steps, map[string]any{"op": "set", "h": h, "v": rng.Int64N(100)})
			case 3:
				steps = append(steps, map[string]any{"op": "del", "h": h})
			default:
				steps = append(steps, map[string]any{"op": "get", "h": h})
			}
		}
		return steps
	}
	for i := 0; i < 120; i++ {
		src := "rand/handle_map"
		if i < 3 {
			src = []string{"TestHandleMap", "TestHandleMapWithPartialHandle", "TestBasicFunc"}[i]
		}
		g.Add(src, "handle_map", mapScript(1+rng.IntN(40)))
	}
	for i := 0; i < 120; i++ {
		src := "rand/mem_aware_map"
		if i == 0 {
			src = "TestMemAwareHandleMapWithPartialHandle"
		}
		vt := []string{"i64", "bool"}[rng.IntN(2)]
		steps := mapScript(1 + rng.IntN(60))
		if i%10 == 0 {
			// many inserts: cross the swiss-table checkpoints
			steps = nil
			for j := 0; j < 2000+rng.IntN(1000); j++ {
				steps = append(steps, map[string]any{"op": "set", "h": handleSpec{T: "int", V: int64(j)}, "v": int64(j)})
			}
			for j := 0; j < 50; j++ {
				steps = append(steps, map[string]any{"op": "set", "h": handles[rng.IntN(len(handles))], "v": int64(j)})
			}
		}
		g.Add(src, "mem_aware_map", vt, steps)
	}
	// key ranges
	for i := 0; i < 240; i++ {
		np := rng.IntN(4)
		maxPer := 4
		if i%4 == 0 {
			// large partitions and many partitions: pdqsort differs from a stable sort
			// only above 12 elements (insertion sort below)
			np = rng.IntN(25)
			maxPer = 50
		}
		parts := make([][][2]string, np)
		var hints [][]int
		withHints := rng.IntN(2) == 0
		for p := range parts {
			n := rng.IntN(maxPer)
			var h []int
			for j := 0; j < n; j++ {
				parts[p] = append(parts[p], [2]string{hex.EncodeToString(keys[rng.IntN(len(keys))]), hex.EncodeToString(keys[rng.IntN(len(keys))])})
				h = append(h, rng.IntN(100))
			}
			if withHints && rng.IntN(3) != 0 {
				hints = append(hints, h)
			}
		}
		mode := []string{"part", "nonpart"}[rng.IntN(2)]
		cmp := []string{"start", "end_desc", "len", "first_byte"}[rng.IntN(4)]
		g.Add("rand/key_ranges", "key_ranges", mode, parts, withHints, hints, cmp, rng.IntN(5))
	}
	// checker
	sub := []int64{0, 10000, 10001, 10002, 10003, 10004, 10005, 1, 2, 101, 102, 104, 121, 3001, 3007, 3010, 3018, 4001, 4011, 4012, 9999, -1, 1<<32 + 1, 1<<32 + 10000, math.MaxInt64, math.MinInt64}
	for _, l := range lits {
		if l.fn == "TestIsRequestTypeSupported" && l.num != nil {
			sub = append(sub, *l.num)
		}
	}
	for _, rt := range []int64{101, 102, 103, 104, 105, 0, -1} {
		for _, s := range sub {
			src := "rand/req"
			if rt == 103 && s == 0 {
				src = "TestIsRequestTypeSupported"
			}
			g.Add(src, "req_supported", rt, s)
		}
	}
	for i := 0; i < 120; i++ {
		g.Add("rand/req", "req_supported", []int64{101, 102, 103, 104}[rng.IntN(4)], rng.Int64N(5000))
	}
	// errors
	for _, n := range []string{"", "retryable", "write_conflict", "write_conflict_tidb", "not_exist", "lock_expire", "other", "wrapped_retryable", "wrapped_not_exist"} {
		g.Add("rand/err", "err_check", n)
		g.Add("rand/err", "err_check", n)
		g.Add("rand/err", "err_check", n)
	}
	for r := 0; r < 8; r++ {
		g.Add("rand/rr", "replica_read", r)
	}
	for _, s := range []string{"", "ddl", "stats", "others"} {
		g.Add("rand/src", "source_type", s)
	}
	g.Add("rand/scope", "txn_scope", "global", "")
	g.Add("rand/scope", "txn_scope", "local", "bj")
	g.Add("rand/scope", "txn_scope", "local", "global")
	g.Add("rand/scope", "txn_scope", "default", "")
	g.Add("rand/scope", "txn_scope", "local", "")
	g.Add("rand/scope", "txn_scope", "local", "zone-1")
	g.Add("TestVersion", "misc", "version", uint64(100))
	g.Add("rand/misc", "misc", "version", uint64(math.MaxUint64))
	g.Add("rand/misc", "misc", "mpp", uint64(0))
	g.Add("rand/misc", "misc", "value_entry", uint64(42))
	g.Add("rand/misc", "misc", "options", uint64(0))
	g.Add("rand/misc", "misc", "options", uint64(1))
	for _, a := range []int{0, 1, 2, 3, 4, 5, 6, 7, 100} {
		src := "rand/back_off"
		if a == 0 {
			src = "TestBackOff"
		}
		g.Add(src, "back_off", a)
	}
	// RunInNewTxn
	names := []string{"", "", "", "retryable", "write_conflict", "write_conflict_tidb", "not_exist", "boom"}
	for i := 0; i < 160; i++ {
		n := 1 + rng.IntN(4)
		var plan []txnStep
		for j := 0; j < n; j++ {
			st := txnStep{F: names[rng.IntN(len(names))], Commit: names[rng.IntN(len(names))]}
			if rng.IntN(20) == 0 {
				st.Begin = "begin failed"
			}
			plan = append(plan, st)
		}
		src := "rand/run_in_new_txn"
		if i == 0 {
			src = "TestRetryExceedCountError"
			plan = []txnStep{{F: "retryable"}}
		}
		if i == 1 {
			src = "TestInnerTxnStartTsBox"
		}
		retryable := rng.IntN(4) != 0
		source := []string{"", "ddl", "meta"}[rng.IntN(3)]
		internal := rng.IntN(3) != 0
		explicit := []string{"", "lightning"}[rng.IntN(2)]
		maxRetry := []int{1, 2, 3, 5}[rng.IntN(4)]
		lower := uint64(rng.IntN(3)) << 18
		g.Add(src, "run_in_new_txn", plan, retryable, source, internal, explicit, maxRetry, lower)
	}
	g.Add("rand/set_rg", "set_rg", "default")
	g.Add("rand/set_rg", "set_rg", "rg1")
	g.Add("rand/set_rg", "set_rg", "")
	g.Add("rand/empty", "empty")
	for i := 0; i < 60; i++ {
		n := rng.IntN(6)
		var ks []string
		for j := 0; j < n; j++ {
			ks = append(ks, hex.EncodeToString(keys[rng.IntN(len(keys))]))
		}
		failAt := rng.IntN(8) - 1
		g.Add("rand/next_until", "next_until", ks, hex.EncodeToString(keys[rng.IntN(len(keys))][:0]), rng.IntN(3), failAt)
	}
	for i := 0; i < 40; i++ {
		n := rng.IntN(5)
		m := map[string]string{}
		var ks []string
		for j := 0; j < n; j++ {
			k := hex.EncodeToString(keys[rng.IntN(len(keys))])
			m[k] = hex.EncodeToString([]byte(fmt.Sprintf("val%d", j)))
			ks = append(ks, k)
		}
		ks = append(ks, hex.EncodeToString([]byte("absent")))
		g.Add("rand/get_value", "get_value", m, ks, rng.IntN(4) == 0)
	}
}

// ---------------------------------------------------------------- exec

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// kvKeySink makes the results of the "key" op escape to the heap.
var kvKeySink []kv.Key

func keyOf(s string) kv.Key {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	if b == nil {
		return kv.Key{}
	}
	k := make(kv.Key, len(b))
	copy(k, b)
	return k
}

func rangesOf(rs [][2]string) []kv.KeyRange {
	out := make([]kv.KeyRange, 0, len(rs))
	for _, r := range rs {
		out = append(out, kv.KeyRange{StartKey: keyOf(r[0]), EndKey: keyOf(r[1])})
	}
	return out
}

func rangesOut(rs []kv.KeyRange) [][2]string {
	out := [][2]string{}
	for _, r := range rs {
		out = append(out, [2]string{hex.EncodeToString(r.StartKey), hex.EncodeToString(r.EndKey)})
	}
	return out
}

type mockSnap struct {
	kv.Snapshot
	vals  map[string][]byte
	big   int
	calls int
}

func (s *mockSnap) Get(_ context.Context, k kv.Key, _ ...kv.GetOption) (kv.ValueEntry, error) {
	s.calls++
	if v, ok := s.vals[string(k)]; ok {
		return kv.NewValueEntry(v, 7), nil
	}
	return kv.ValueEntry{}, kv.ErrNotExist
}

func (s *mockSnap) BatchGet(_ context.Context, keys []kv.Key, _ ...kv.BatchGetOption) (map[string]kv.ValueEntry, error) {
	s.calls++
	if s.big < 0 {
		return nil, errors.New("batch get failed")
	}
	out := map[string]kv.ValueEntry{}
	for _, k := range keys {
		if v, ok := s.vals[string(k)]; ok {
			out[string(k)] = kv.NewValueEntry(v, 7)
		}
	}
	return out, nil
}

type mockIter struct {
	keys   []kv.Key
	pos    int
	failAt int
}

func (it *mockIter) Valid() bool   { return it.pos < len(it.keys) }
func (it *mockIter) Key() kv.Key   { return it.keys[it.pos] }
func (it *mockIter) Value() []byte { return nil }
func (it *mockIter) Next() error {
	if it.pos == it.failAt {
		return errors.New("next failed")
	}
	it.pos++
	return nil
}
func (it *mockIter) Close() {}

func cmpOf(name string) func(i, j kv.KeyRange) int {
	switch name {
	case "start":
		return func(i, j kv.KeyRange) int { return i.StartKey.Cmp(j.StartKey) }
	case "end_desc":
		return func(i, j kv.KeyRange) int { return j.EndKey.Cmp(i.EndKey) }
	case "len":
		return func(i, j kv.KeyRange) int { return len(i.StartKey) - len(j.StartKey) }
	default:
		return func(i, j kv.KeyRange) int {
			a, b := -1, -1
			if len(i.StartKey) > 0 {
				a = int(i.StartKey[0])
			}
			if len(j.StartKey) > 0 {
				b = int(j.StartKey[0])
			}
			return a - b
		}
	}
}

func kvExec(op string, args []json.RawMessage) (out any, err error) {
	switch op {
	case "key":
		k := keyOf(arg[string](args, 0))
		n, p, c := k.Next(), k.PrefixNext(), k.Clone()
		// The results escape, as they do in TiDB (stored in ranges and handles). A result
		// that does not escape may get a 32-byte stack backing store from the Go 1.25
		// compiler (append to a non-escaping slice), and its capacity would be the call
		// site's, not PrefixNext's.
		kvKeySink = append(kvKeySink[:0], n, p, c)
		return map[string]any{"next": hex.EncodeToString(n), "next_cap": cap(n), "pnext": hex.EncodeToString(p),
			"pnext_cap": cap(p), "clone": hex.EncodeToString(c), "clone_cap": cap(c), "str": k.String()}, nil
	case "key2":
		a, b := keyOf(arg[string](args, 0)), keyOf(arg[string](args, 1))
		return map[string]any{"cmp": a.Cmp(b), "prefix": a.HasPrefix(b)}, nil
	case "key_range_mem":
		return kv.KeyRangeSliceMemUsage(rangesOf(arg[[][2]string](args, 0))), nil
	case "handle":
		h := handleOfSpec(arg[handleSpec](args, 0))
		c := h.Copy()
		return map[string]any{"h": handleOut(h), "next": handleOut(h.Next()), "copy": handleOut(c)}, nil
	case "handle_call":
		h := handleOfSpec(arg[handleSpec](args, 1))
		switch arg[string](args, 0) {
		case "num_cols":
			return h.NumCols(), nil
		case "encoded_col":
			return hex.EncodeToString(h.EncodedCol(arg[int](args, 2))), nil
		default:
			return h.IntValue(), nil
		}
	case "handle2":
		a, b := handleOfSpec(arg[handleSpec](args, 0)), handleOfSpec(arg[handleSpec](args, 1))
		res := map[string]any{"eq": a.Equal(b)}
		func() {
			defer func() {
				if r := recover(); r != nil {
					res["cmp_panic"] = fmt.Sprint(r)
				}
			}()
			res["cmp"] = a.Compare(b)
		}()
		return res, nil
	case "handle_map":
		type step struct {
			Op string     `json:"op"`
			H  handleSpec `json:"h"`
			V  int64      `json:"v"`
		}
		m := kv.NewHandleMap()
		var outs []any
		for _, s := range arg[[]step](args, 0) {
			h := handleOfSpec(s.H)
			switch s.Op {
			case "set":
				m.Set(h, s.V)
			case "del":
				m.Delete(h)
			default:
				v, ok := m.Get(h)
				outs = append(outs, []any{v, ok})
			}
			outs = append(outs, []any{m.Len(), m.MemUsage()})
		}
		var all []string
		m.Range(func(h kv.Handle, v any) bool {
			all = append(all, fmt.Sprintf("%s=%v", vec.MustJSON(specOf(h)), v))
			return true
		})
		sort.Strings(all)
		stop := 0
		m.Range(func(kv.Handle, any) bool { stop++; return false })
		return map[string]any{"steps": outs, "range": all, "stop": stop}, nil
	case "mem_aware_map":
		type step struct {
			Op string     `json:"op"`
			H  handleSpec `json:"h"`
			V  int64      `json:"v"`
		}
		vt := arg[string](args, 0)
		var outs []any
		var all []string
		if vt == "i64" {
			m := kv.NewMemAwareHandleMap[int64]()
			for _, s := range arg[[]step](args, 1) {
				h := handleOfSpec(s.H)
				if s.Op == "set" {
					outs = append(outs, m.Set(h, s.V))
				} else {
					v, ok := m.Get(h)
					outs = append(outs, []any{v, ok})
				}
			}
			m.Range(func(h kv.Handle, v int64) bool {
				all = append(all, fmt.Sprintf("%s=%v", vec.MustJSON(specOf(h)), v))
				return true
			})
		} else {
			m := kv.NewMemAwareHandleMap[bool]()
			for _, s := range arg[[]step](args, 1) {
				h := handleOfSpec(s.H)
				if s.Op == "set" {
					outs = append(outs, m.Set(h, s.V%2 == 1))
				} else {
					v, ok := m.Get(h)
					outs = append(outs, []any{v, ok})
				}
			}
			m.Range(func(h kv.Handle, v bool) bool {
				all = append(all, fmt.Sprintf("%s=%v", vec.MustJSON(specOf(h)), v))
				return true
			})
		}
		sort.Strings(all)
		return map[string]any{"steps": outs, "range": all}, nil
	case "key_ranges":
		mode := arg[string](args, 0)
		parts := arg[[][][2]string](args, 1)
		withHints := arg[bool](args, 2)
		hints := arg[[][]int](args, 3)
		var rr *kv.KeyRanges
		var ps [][]kv.KeyRange
		for _, p := range parts {
			ps = append(ps, rangesOf(p))
		}
		switch {
		case mode == "part" && withHints:
			rr = kv.NewPartitionedKeyRangesWithHints(ps, hints)
		case mode == "part":
			rr = kv.NewPartitionedKeyRanges(ps)
		case withHints:
			var h []int
			if len(hints) > 0 {
				h = hints[0]
			}
			var first []kv.KeyRange
			if len(ps) > 0 {
				first = ps[0]
			}
			rr = kv.NewNonParitionedKeyRangesWithHint(first, h)
		default:
			var first []kv.KeyRange
			if len(ps) > 0 {
				first = ps[0]
			}
			rr = kv.NewNonPartitionedKeyRanges(first)
		}
		res := map[string]any{"num": rr.PartitionNum(), "first": rangesOut(rr.FirstPartitionRange()),
			"sorted": rr.IsFullySorted(), "append": rangesOut(rr.AppendSelfTo([]kv.KeyRange{{StartKey: kv.Key("x")}}))}
		failAt := arg[int](args, 5)
		var visits []any
		ferr := rr.ForEachPartitionWithErr(func(r []kv.KeyRange, h []int) error {
			visits = append(visits, []any{rangesOut(r), h, h == nil})
			if len(visits) == failAt {
				return errors.New("stop")
			}
			return nil
		})
		res["visits"] = visits
		res["visit_err"] = ferr != nil
		rr.SortByFunc(cmpOf(arg[string](args, 4)))
		var after []any
		_ = rr.ForEachPartitionWithErr(func(r []kv.KeyRange, _ []int) error {
			after = append(after, rangesOut(r))
			return nil
		})
		res["sorted_ranges"] = after
		res["sorted2"] = rr.IsFullySorted()
		return res, rr.SetToNonPartitioned()
	case "req_supported":
		return kv.RequestTypeSupportedChecker{}.IsRequestTypeSupported(arg[int64](args, 0), arg[int64](args, 1)), nil
	case "err_check":
		var e error
		switch n := arg[string](args, 0); n {
		case "wrapped_retryable":
			e = fmt.Errorf("wrap: %w", kv.ErrTxnRetryable)
		case "wrapped_not_exist":
			e = kv.ErrNotExist.GenWithStackByArgs()
		default:
			e = errByName(n)
		}
		msg := ""
		if e != nil {
			msg = e.Error()
		}
		nf := false
		if e != nil {
			nf = kv.IsErrNotFound(e)
		}
		return map[string]any{"retry": kv.IsTxnRetryableError(e), "not_found": nf, "msg": vec.GoString(msg)}, nil
	case "replica_read":
		r := kv.ReplicaReadType(arg[int](args, 0))
		return []bool{r.IsFollowerRead(), r.IsClosestRead()}, nil
	case "source_type":
		ctx := goContext()
		if s := arg[string](args, 0); s != "" {
			ctx = kv.WithInternalSourceType(ctx, s)
		}
		return kv.GetInternalSourceType(ctx), nil
	case "txn_scope":
		var v kv.TxnScopeVar
		switch arg[string](args, 0) {
		case "global":
			v = kv.NewGlobalTxnScopeVar()
		case "local":
			v = kv.NewLocalTxnScopeVar(arg[string](args, 1))
		default:
			v = kv.NewDefaultTxnScopeVar()
		}
		return []string{v.GetVarValue(), v.GetTxnScope()}, nil
	case "misc":
		switch arg[string](args, 0) {
		case "version":
			return kv.NewVersion(arg[uint64](args, 1)).Ver, nil
		case "mpp":
			return int64(kv.GetNewestMppVersion()), nil
		case "value_entry":
			e := kv.NewValueEntry([]byte("v"), arg[uint64](args, 1))
			return []any{hex.EncodeToString(e.Value), e.CommitTS}, nil
		default:
			var opts []kv.BatchGetOption
			if arg[uint64](args, 1) == 1 {
				opts = append(opts, tikvstore.WithReturnCommitTS())
			}
			return len(kv.BatchGetToGetOptions(opts)), nil
		}
	case "back_off":
		a := uint(arg[int](args, 0))
		upper := int(math.Min(100, math.Pow(2.0, float64(a))))
		d := kv.BackOff(a)
		return map[string]any{"upper": upper, "in_range": d >= 0 && d < upper*int(time.Millisecond)}, nil
	case "run_in_new_txn":
		plan := arg[[]txnStep](args, 0)
		retryable := arg[bool](args, 1)
		source, internal, explicit := arg[string](args, 2), arg[bool](args, 3), arg[string](args, 4)
		maxRetry := uint(arg[int](args, 5))
		lower := arg[uint64](args, 6)
		old := kv.MaxRetryCnt
		kv.MaxRetryCnt = maxRetry
		defer func() { kv.MaxRetryCnt = old }()
		store := &kvMockStore{plan: plan, nextTS: 1 << 18}
		ctx := goContext()
		if source != "" {
			ctx = withRequestSource(ctx, internal, source, explicit)
		}
		var mins []uint64
		now := oracle.GetTimeFromTS(10 << 18)
		rerr := kv.RunInNewTxn(ctx, store, retryable, func(_ context.Context, txn kv.Transaction) error {
			mins = append(mins, kv.GetMinInnerTxnStartTS(now, lower, math.MaxUint64))
			step := plan[min(store.attempt, len(plan)-1)]
			store.attempt++
			return errByName(step.F)
		})
		var txns []any
		for _, t := range store.txns {
			opts := map[string]any{}
			for k, v := range t.opts {
				opts[fmt.Sprint(k)] = fmt.Sprint(v)
			}
			txns = append(txns, map[string]any{"ts": t.startTS, "rb": t.rolledBack, "commit": t.committed, "opts": opts})
		}
		after := kv.GetMinInnerTxnStartTS(now, 0, math.MaxUint64)
		return map[string]any{"begins": store.begins, "txns": txns, "mins": mins, "after": after}, rerr
	case "set_rg":
		t := &kvMockTxn{opts: map[int]any{}}
		kv.SetTxnResourceGroup(t, arg[string](args, 0))
		return fmt.Sprint(t.opts[kv.ResourceGroupName]), nil
	case "empty":
		it := &kv.EmptyIterator{}
		nerr := it.Next()
		it.Close()
		r := &kv.EmptyRetriever{}
		_, gerr := r.Get(goContext(), kv.Key("a"))
		i1, _ := r.Iter(nil, nil)
		i2, _ := r.IterReverse(nil, nil)
		return map[string]any{"valid": it.Valid(), "key": it.Key() == nil, "value": it.Value() == nil,
			"next": nerr.Error(), "get_not_found": kv.IsErrNotFound(gerr), "iter": i1.Valid(), "iter_rev": i2.Valid()}, nil
	case "next_until":
		ks := arg[[]string](args, 0)
		var keys []kv.Key
		for _, k := range ks {
			keys = append(keys, keyOf(k))
		}
		mode := arg[int](args, 2)
		it := &mockIter{keys: keys, failAt: arg[int](args, 3)}
		calls := 0
		err := kv.NextUntil(it, func(k kv.Key) bool {
			calls++
			switch mode {
			case 0:
				return len(k) == 0
			case 1:
				return len(k) > 0 && k[0] == 0xff
			default:
				return false
			}
		})
		return []int{it.pos, calls}, err
	case "get_value":
		m := arg[map[string]string](args, 0)
		snap := &mockSnap{vals: map[string][]byte{}}
		for k, v := range m {
			snap.vals[string(keyOf(k))] = keyOf(v)
		}
		if arg[bool](args, 2) {
			snap.big = -1
		}
		var gets []any
		var keys []kv.Key
		for _, k := range arg[[]string](args, 1) {
			v, err := kv.GetValue(goContext(), snap, keyOf(k))
			gets = append(gets, []any{hex.EncodeToString(v), v == nil, kv.IsErrNotFound(err)})
			keys = append(keys, keyOf(k))
		}
		vals, err := kv.BatchGetValue(goContext(), snap, keys)
		var bv []string
		for k, v := range vals {
			bv = append(bv, hex.EncodeToString([]byte(k))+"="+hex.EncodeToString(v))
		}
		slices.Sort(bv)
		return map[string]any{"gets": gets, "batch": bv}, err
	}
	panic("kv: unknown op " + op)
}
