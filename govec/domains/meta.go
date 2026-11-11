// Domain "meta": block D06.2 part 1, the read side of pkg/structure (the hash / list /
// string layout of TiDB's meta keys) and pkg/meta (meta.Reader: databases, tables,
// policies, resource groups, schema version and diffs, DDL job queue and history, the
// meta autoid accessors), plus pkg/meta/metadef. tidb-rs is read-only: the writers
// (Mutator.Create*/Update*/Drop*/Gen*/Set*, TxStructure.HSet/HInc/LPush/...) are X
// ("DDL 与元数据写入由 Go TiDB 承担", tracker rules), and Go uses them here only to
// build the stored state.
//
// Ops:
//   - "struct": args [prefix hex, pairs, calls]. pairs is the stored KV state
//     ([[key hex, value hex], ...], sorted), built by Go's TxStructure writers on a
//     mockstore transaction plus injected foreign / corrupt keys. calls are read calls
//     on structure.NewStructure(snapshot, nil, prefix); out is one result per call
//     ({"r": result} or {"e": {code, msg}}).
//   - "meta": args [pairs, calls]: the same over meta.NewReader(snapshot) (prefix "m"),
//     state built by meta.Mutator writers plus corruptions (invalid JSON, wrong magic
//     byte, bad list meta, a table value without "id" / name, ...). A call that panics in
//     Go (GetAllNameToIDAndTheMustLoadedTableInfo on a value its regexps do not match)
//     is the only call of its record.
//   - "keys": one pure function of meta.go per record (key builders and parsers, the
//     must-load filter, Unescape, Element).
//   - "metadef": the database-name predicates of pkg/meta/metadef.
//
// The snapshot is an in-memory sorted map (memSnap) with Go's kv.Retriever semantics,
// so Exec does not depend on a store; Rust replays with the same map semantics.
package domains

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta"
	"github.com/pingcap/tidb/pkg/meta/metadef"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/store/mockstore"
	"github.com/pingcap/tidb/pkg/structure"
	"github.com/pingcap/tidb/pkg/types"
)

const metaSeed = 20261111

func init() {
	vec.Register(&vec.Domain{
		Name:     "meta",
		Seed:     metaSeed,
		Coverage: metaCoverage,
		Blocks:   []string{"D06.2"},
		Exempt:   metaExempt,
		Gzip:     true,
		Generate: metaGenerate,
		Exec:     metaExec,
	})
}

var metaCoverage = map[string]vec.Coverage{
	"struct": {Min: 300, GoFuncs: []string{
		"NewStructure", "TxStructure.Get", "TxStructure.GetInt64", "TxStructure.Iterate",
		"TxStructure.HGet", "TxStructure.HGetInt64", "TxStructure.HKeys", "TxStructure.HGetAll",
		"TxStructure.HGetIter", "TxStructure.HGetLen", "TxStructure.HGetLastN",
		"TxStructure.IterateHash", "TxStructure.IterateHashWithBoundedKey",
		"TxStructure.iterReverseHash",
		"NewHashReverseIter", "NewHashReverseIterBeginWithField", "newHashReverseIter",
		"ReverseHashIterator.Next", "ReverseHashIterator.Valid", "ReverseHashIterator.Key",
		"ReverseHashIterator.Value", "ReverseHashIterator.Close",
		"TxStructure.LGetAll", "TxStructure.LLen", "TxStructure.LIndex", "TxStructure.loadListMeta",
		"adjustIndex", "listMeta.IsEmpty",
		"TxStructure.EncodeStringDataKey", "TxStructure.decodeStringDataKey",
		"TxStructure.EncodeHashMetaKey", "TxStructure.encodeHashDataKey",
		"TxStructure.EncodeHashDataKey", "TxStructure.decodeHashDataKey",
		"TxStructure.hashDataKeyPrefix", "TxStructure.encodeListMetaKey",
		"TxStructure.encodeListDataKey",
	}},
	"meta": {Min: 300, GoFuncs: []string{
		"NewReader", "Mutator.GetGlobalID", "Mutator.GetPolicyID", "Mutator.GlobalIDKey",
		"Mutator.policyKey", "Mutator.resourceGroupKey", "Mutator.dbKey", "Mutator.tableKey",
		"Mutator.autoTableIDKey", "Mutator.autoIncrementIDKey", "Mutator.autoRandomTableIDKey",
		"Mutator.sequenceKey", "Mutator.sequenceCycleKey", "DDLJobHistoryKey",
		"Mutator.GetAutoIDAccessors", "Mutator.GetSchemaVersionWithNonEmptyDiff",
		"Mutator.EncodeSchemaDiffKey", "Mutator.GetSchemaVersion", "Mutator.checkDBExists",
		"Mutator.GetBDRRole", "Mutator.CheckDDLTableVersion", "Mutator.GetSystemDBID",
		"Mutator.GetMetadataLock", "Mutator.GetSchemaCacheSize", "Mutator.IterDatabases",
		"Mutator.IterTables", "Mutator.GetMetasByDBID", "isTableInfoMustLoad",
		"Mutator.GetAllNameToIDAndTheMustLoadedTableInfo", "GetTableInfoWithAttributes",
		"Mutator.ListTables", "Mutator.ListDatabases", "Mutator.GetDatabase",
		"Mutator.ListPolicies", "Mutator.GetPolicy", "Mutator.ListResourceGroups",
		"Mutator.GetResourceGroup", "detachMagicByte", "whichMagicType", "Mutator.GetTable",
		"Mutator.CheckTableExists", "Mutator.GetAllDDLJobsInQueue",
		"Mutator.jobIDKey", "Mutator.getHistoryDDLJob", "Mutator.GetHistoryDDLJob",
		"Mutator.GetHistoryDDLCount", "Mutator.GetIngestMaxBatchSplitRanges",
		"Mutator.GetIngestMaxSplitRangesPerSec", "Mutator.GetIngestMaxInflight",
		"Mutator.GetIngestMaxPerSec", "Mutator.GetLastHistoryDDLJobsIterator",
		"Mutator.GetHistoryDDLJobsIterator", "HLastJobIterator.GetLastJobs",
		"Mutator.GetBootstrapVersion", "Mutator.schemaDiffKey", "Mutator.GetSchemaDiff",
		"NewAutoIDAccessors", "autoIDAccessor.Get", "autoIDAccessors.Get",
		"autoIDAccessors.RowID", "autoIDAccessors.IncrementID", "autoIDAccessors.RandomID",
		"autoIDAccessors.SequenceValue", "autoIDAccessors.SequenceCycle",
	}},
	"keys": {Min: 300, GoFuncs: []string{
		"DBkey", "ParseDBKey", "IsDBkey", "AutoTableIDKey", "IsAutoTableIDKey",
		"ParseAutoTableIDKey", "AutoIncrementIDKey", "IsAutoIncrementIDKey",
		"ParseAutoIncrementIDKey", "AutoRandomTableIDKey", "IsAutoRandomTableIDKey",
		"ParseAutoRandomTableIDKey", "TableKey", "IsTableKey", "ParseTableKey", "SequenceKey",
		"IsSequenceKey", "ParseSequenceKey", "IsTableInfoMustLoad", "Unescape",
		"Element.String", "Element.EncodeElement", "DecodeElement",
	}},
	"metadef": {Min: 40, GoFuncs: []string{
		"IsMemOrSysDB", "IsMemDB", "IsSystemRelatedDB", "IsSystemDB", "IsBRRelatedDB",
	}},
}

// Part 2 of D06.2 (specs/D06.2-meta/plan.md): ported later, not exercised here.
var metaExempt = func() map[string]string {
	m := map[string]string{
		"FastUnmarshalTableNameInfo":                     "part 2: needs pkg/util/partialjson (json.Decoder.Token)",
		"Mutator.ListSimpleTables":                       "part 2: FastUnmarshalTableNameInfo",
		"ExtractSchemaAndTableNameFromJob":               "part 2: needs pkg/util/partialjson",
		"IsJobMatch":                                     "part 2: ExtractSchemaAndTableNameFromJob",
		"Mutator.GetLastHistoryDDLJobsIteratorWithFilter": "part 2: IsJobMatch with names",
		"IterAllTables":                                  "part 2: concurrent snapshot scan over kv.Storage",
		"splitRangeInt64Max":                             "part 2: IterAllTables",
		"Mutator.GetRUStats":                             "part 2: RUStats JSON needs kvproto resource_manager Consumption",
		"GetOldestSchemaVersion":                         "part 2: MVCC read through store/helper (D07.1)",
		"NewMutator":                                     "kv.Transaction carrier; the read methods are covered through NewReader (the same Mutator)",
	}
	for _, f := range []string{
		"Context.AppendNote", "Context.AppendWarning", "Context.EnableAutoIncrementInGenerated",
		"Context.GetClusteredIndexDefMode", "Context.GetDefaultCollationForUTF8MB4", "Context.GetExprCtx",
		"Context.GetInfoSchema", "Context.GetPreSplitRegions", "Context.GetSQLMode", "Context.GetShardRowIDBits",
		"Context.PrimaryKeyRequired", "Context.SuppressTooLongIndexErr", "NewContext", "NewNonStrictContext",
		"WithClusteredIndexDefMode", "WithEnableAutoIncrementInGenerated", "WithExprCtx", "WithInfoSchema",
		"WithPreSplitRegions", "WithPrimaryKeyRequired", "WithShardRowIDBits", "WithSuppressTooLongIndexErr",
		"funcCtxOption.applyCtx", "funcOpt",
	} {
		m[f] = "part 2: metabuild.Context (needs the exprctx / infoschema context of D06.3)"
	}
	for _, f := range []string{
		"AllocOptionTableInfoVersion.ApplyOn", "AllocatorRuntimeStats.Clone", "AllocatorRuntimeStats.Merge",
		"AllocatorRuntimeStats.String", "AllocatorRuntimeStats.mergeCommitDetail", "AllocatorType.String",
		"Allocators.Append", "Allocators.Filter", "Allocators.Get", "AutoRandomRangeBitsNormalize",
		"AutoRandomShardBitsNormalize", "CalcSequenceBatchSize", "CustomAutoIncCacheOption.ApplyOn",
		"DecodeCmpUintToInt", "EncodeIntToCmpUint", "GetStep", "IsMemSchemaID", "NewAllocator",
		"NewAllocatorRuntimeStats", "NewAllocators", "NewAllocatorsFromTblInfo", "NewSequenceAllocator",
		"NewShardIDFormat", "SeekToFirstSequenceValue", "SetStep", "ShardIDFormat.Compose",
		"ShardIDFormat.IncrementalBitsCapacity", "ShardIDFormat.IncrementalMask", "TestModifyBaseAndEndInjection",
		"allocator.AllocSeqCache", "allocator.Base", "allocator.End", "allocator.ForceRebase", "allocator.GetType",
		"allocator.NextGlobalAutoID", "allocator.Rebase", "allocator.RebaseSeq", "allocator.Transfer",
		"allocator.alloc4Sequence", "allocator.alloc4Unsigned", "allocator.getIDAccessor",
		"allocator.rebase4Sequence", "allocator.rebase4Signed", "allocator.rebase4Unsigned",
		"getAllocatorStatsFromCtx", "newSinglePointAlloc", "ClientDiscover.GetClient", "ClientDiscover.ResetConn",
		"ClientDiscover.resetConn", "NewClientDiscover", "backoffer.Backoff", "backoffer.Reset",
		"singlePointAlloc.Alloc", "singlePointAlloc.AllocSeqCache", "singlePointAlloc.Base", "singlePointAlloc.End",
		"singlePointAlloc.ForceRebase", "singlePointAlloc.GetType", "singlePointAlloc.NextGlobalAutoID",
		"singlePointAlloc.Rebase", "singlePointAlloc.RebaseSeq", "singlePointAlloc.Transfer", "singlePointAlloc.rebase",
		"NewAllocatorFromTempTblInfo", "inMemoryAllocator.Alloc", "inMemoryAllocator.AllocSeqCache",
		"inMemoryAllocator.Base", "inMemoryAllocator.End", "inMemoryAllocator.ForceRebase", "inMemoryAllocator.GetType",
		"inMemoryAllocator.NextGlobalAutoID", "inMemoryAllocator.Rebase", "inMemoryAllocator.RebaseSeq",
		"inMemoryAllocator.Transfer", "inMemoryAllocator.alloc4Signed", "inMemoryAllocator.alloc4Unsigned",
	} {
		m[f] = "part 2: pkg/meta/autoid (allocators; their writes go to X then)"
	}
	return m
}()

// ---------------------------------------------------------------- in-memory snapshot

type memSnap struct {
	keys [][]byte
	vals [][]byte
}

func newMemSnap(pairs [][2]string) *memSnap {
	s := &memSnap{}
	for _, p := range pairs {
		k, _ := hex.DecodeString(p[0])
		v, _ := hex.DecodeString(p[1])
		s.keys = append(s.keys, k)
		s.vals = append(s.vals, v)
	}
	return s
}

func (s *memSnap) Get(_ context.Context, k kv.Key, _ ...kv.GetOption) (kv.ValueEntry, error) {
	i := sort.Search(len(s.keys), func(i int) bool { return bytes.Compare(s.keys[i], k) >= 0 })
	if i < len(s.keys) && bytes.Equal(s.keys[i], k) {
		return kv.NewValueEntry(s.vals[i], 0), nil
	}
	return kv.ValueEntry{}, kv.ErrNotExist
}

func (s *memSnap) BatchGet(ctx context.Context, keys []kv.Key, _ ...kv.BatchGetOption) (map[string]kv.ValueEntry, error) {
	m := map[string]kv.ValueEntry{}
	for _, k := range keys {
		if v, err := s.Get(ctx, k); err == nil {
			m[string(k)] = v
		}
	}
	return m, nil
}

func (*memSnap) SetOption(int, any) {}

type memIter struct {
	s    *memSnap
	i    int
	step int
	end  func(int) bool
}

func (it *memIter) Valid() bool { return it.i >= 0 && it.i < len(it.s.keys) && !it.end(it.i) }
// Key / Value of an exhausted iterator are nil, as for client-go's snapshot scanner
// (ReverseHashIterator.Next reads Key after Next without checking Valid).
func (it *memIter) Key() kv.Key {
	if !it.Valid() {
		return nil
	}
	return it.s.keys[it.i]
}
func (it *memIter) Value() []byte {
	if !it.Valid() {
		return nil
	}
	return it.s.vals[it.i]
}
func (it *memIter) Next() error { it.i += it.step; return nil }
func (*memIter) Close()         {}

// Iter: the first key >= k, keys < upperBound (nil / empty: unbounded).
func (s *memSnap) Iter(k kv.Key, upperBound kv.Key) (kv.Iterator, error) {
	i := sort.Search(len(s.keys), func(i int) bool { return bytes.Compare(s.keys[i], k) >= 0 })
	return &memIter{s: s, i: i, step: 1, end: func(i int) bool {
		return len(upperBound) > 0 && bytes.Compare(s.keys[i], upperBound) >= 0
	}}, nil
}

// IterReverse: the last key < k (k nil / empty: the last key), keys >= lowerBound.
func (s *memSnap) IterReverse(k, lowerBound kv.Key) (kv.Iterator, error) {
	i := len(s.keys) - 1
	if len(k) > 0 {
		i = sort.Search(len(s.keys), func(i int) bool { return bytes.Compare(s.keys[i], k) >= 0 }) - 1
	}
	return &memIter{s: s, i: i, step: -1, end: func(i int) bool {
		return len(lowerBound) > 0 && bytes.Compare(s.keys[i], lowerBound) < 0
	}}, nil
}

// ---------------------------------------------------------------- exec

type metaCall []json.RawMessage

func mcStr(c metaCall, i int) string {
	var s string
	if err := json.Unmarshal(c[i], &s); err != nil {
		panic(err)
	}
	return s
}

func mcHex(c metaCall, i int) []byte {
	var s *string
	if err := json.Unmarshal(c[i], &s); err != nil {
		panic(err)
	}
	if s == nil {
		return nil
	}
	b, err := hex.DecodeString(*s)
	if err != nil {
		panic(err)
	}
	return b
}

func mcInt(c metaCall, i int) int64 {
	var n int64
	if err := json.Unmarshal(c[i], &n); err != nil {
		panic(err)
	}
	return n
}

type metaErr struct {
	Code int          `json:"code"`
	Msg  vec.GoString `json:"msg"`
}

type metaRes struct {
	R any      `json:"r,omitempty"`
	E *metaErr `json:"e,omitempty"`
}

func metaErrOf(err error) *metaErr {
	if err == nil {
		return nil
	}
	e := &metaErr{Msg: vec.GoString(err.Error())}
	if te, ok := errors.Cause(err).(*errors.Error); ok {
		e.Code = int(te.Code())
	}
	return e
}

func hexList(bs [][]byte) []any {
	out := make([]any, 0, len(bs))
	for _, b := range bs {
		out = append(out, hexOrNil(b))
	}
	return out
}

func pairsOut(ps []structure.HashPair) [][2]string {
	out := make([][2]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, [2]string{hex.EncodeToString(p.Field), hex.EncodeToString(p.Value)})
	}
	return out
}

func metaExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "struct":
		var prefix string
		var pairs [][2]string
		var calls []metaCall
		mustUnmarshal(args[0], &prefix)
		mustUnmarshal(args[1], &pairs)
		mustUnmarshal(args[2], &calls)
		p, _ := hex.DecodeString(prefix)
		t := structure.NewStructure(newMemSnap(pairs), nil, p)
		out := make([]metaRes, 0, len(calls))
		for _, c := range calls {
			r, err := structCall(t, c)
			out = append(out, metaRes{R: r, E: metaErrOf(err)})
		}
		return out, nil
	case "meta":
		var pairs [][2]string
		var calls []metaCall
		mustUnmarshal(args[0], &pairs)
		mustUnmarshal(args[1], &calls)
		m := meta.NewReader(newMemSnap(pairs)).(*meta.Mutator)
		out := make([]metaRes, 0, len(calls))
		for _, c := range calls {
			r, err := metaCallRun(m, c)
			out = append(out, metaRes{R: r, E: metaErrOf(err)})
		}
		return out, nil
	case "keys":
		var c metaCall
		mustUnmarshal(args[0], &c)
		return keysCall(c)
	case "metadef":
		var f, s string
		mustUnmarshal(args[0], &f)
		mustUnmarshal(args[1], &s)
		switch f {
		case "IsMemOrSysDB":
			return metadef.IsMemOrSysDB(s), nil
		case "IsMemDB":
			return metadef.IsMemDB(s), nil
		case "IsSystemRelatedDB":
			return metadef.IsSystemRelatedDB(s), nil
		case "IsSystemDB":
			return metadef.IsSystemDB(s), nil
		case "IsBRRelatedDB":
			return metadef.IsBRRelatedDB(s), nil
		}
	}
	return nil, fmt.Errorf("unknown op %s", op)
}

func mustUnmarshal(b []byte, v any) {
	if err := json.Unmarshal(b, v); err != nil {
		panic(err)
	}
}

// errStop is returned by an iteration callback after n entries ("…_stop" calls).
var errStop = errors.New("stop iteration")

func structCall(t *structure.TxStructure, c metaCall) (any, error) {
	switch mcStr(c, 0) {
	case "get":
		v, err := t.Get(mcHex(c, 1))
		return hexOrNil(v), err
	case "getint64":
		return t.GetInt64(mcHex(c, 1))
	case "iterate":
		stop := mcInt(c, 3)
		out := [][2]string{}
		err := t.Iterate(mcHex(c, 1), mcHex(c, 2), func(k, v []byte) error {
			if stop >= 0 && int64(len(out)) == stop {
				return errStop
			}
			out = append(out, [2]string{hex.EncodeToString(k), hex.EncodeToString(v)})
			return nil
		})
		return out, err
	case "hget":
		v, err := t.HGet(mcHex(c, 1), mcHex(c, 2))
		return hexOrNil(v), err
	case "hgetint64":
		return t.HGetInt64(mcHex(c, 1), mcHex(c, 2))
	case "hkeys":
		ks, err := t.HKeys(mcHex(c, 1))
		return hexList(ks), err
	case "hgetall":
		ps, err := t.HGetAll(mcHex(c, 1))
		return pairsOut(ps), err
	case "hgetiter":
		stop := mcInt(c, 2)
		out := []structure.HashPair{}
		err := t.HGetIter(mcHex(c, 1), func(p structure.HashPair) error {
			if stop >= 0 && int64(len(out)) == stop {
				return errStop
			}
			out = append(out, p)
			return nil
		})
		return pairsOut(out), err
	case "hgetlen":
		return t.HGetLen(mcHex(c, 1))
	case "hgetlastn":
		ps, err := t.HGetLastN(mcHex(c, 1), int(mcInt(c, 2)))
		return pairsOut(ps), err
	case "iteratehash":
		stop := mcInt(c, 2)
		out := [][2]string{}
		err := t.IterateHash(mcHex(c, 1), func(f, v []byte) error {
			if stop >= 0 && int64(len(out)) == stop {
				return errStop
			}
			out = append(out, [2]string{hex.EncodeToString(f), hex.EncodeToString(v)})
			return nil
		})
		return out, err
	case "iterbounded":
		stop := mcInt(c, 3)
		out := [][3]string{}
		err := t.IterateHashWithBoundedKey(mcHex(c, 1), mcHex(c, 2), func(k, f, v []byte) error {
			if stop >= 0 && int64(len(out)) == stop {
				return errStop
			}
			out = append(out, [3]string{hex.EncodeToString(k), hex.EncodeToString(f), hex.EncodeToString(v)})
			return nil
		})
		return out, err
	case "hrev":
		var it *structure.ReverseHashIterator
		var err error
		if field := mcHex(c, 2); field == nil {
			it, err = structure.NewHashReverseIter(t, mcHex(c, 1))
		} else {
			it, err = structure.NewHashReverseIterBeginWithField(t, mcHex(c, 1), field)
		}
		if err != nil {
			return nil, err
		}
		defer it.Close()
		out := []any{}
		for n := mcInt(c, 3); n > 0; n-- {
			if !it.Valid() {
				out = append(out, nil)
				break
			}
			out = append(out, [2]string{hex.EncodeToString(it.Key()), hex.EncodeToString(it.Value())})
			if err := it.Next(); err != nil {
				return out, err
			}
		}
		return out, nil
	case "lgetall":
		vs, err := t.LGetAll(mcHex(c, 1))
		return hexList(vs), err
	case "llen":
		return t.LLen(mcHex(c, 1))
	case "lindex":
		v, err := t.LIndex(mcHex(c, 1), mcInt(c, 2))
		return hexOrNil(v), err
	case "encstr":
		return hex.EncodeToString(t.EncodeStringDataKey(mcHex(c, 1))), nil
	case "enchashmeta":
		return hex.EncodeToString(t.EncodeHashMetaKey(mcHex(c, 1))), nil
	case "enchashdata":
		return hex.EncodeToString(t.EncodeHashDataKey(mcHex(c, 1), mcHex(c, 2))), nil
	}
	panic("unknown struct call " + mcStr(c, 0))
}

type metaTblOut struct {
	J  string `json:"j"`
	DB int64  `json:"db"`
}

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func tblOut(t *model.TableInfo) any {
	if t == nil {
		return nil
	}
	return metaTblOut{J: jsonStr(t), DB: t.DBID}
}

func tblsOut(ts []*model.TableInfo) []any {
	out := make([]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, tblOut(t))
	}
	return out
}

func jsonOrNil[T any](v *T) any {
	if v == nil {
		return nil
	}
	return jsonStr(v)
}

func jsonList[T any](vs []*T) []any {
	out := make([]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, jsonOrNil(v))
	}
	return out
}

func metaCallRun(m *meta.Mutator, c metaCall) (any, error) {
	switch mcStr(c, 0) {
	case "globalid":
		return m.GetGlobalID()
	case "policyid":
		return m.GetPolicyID()
	case "globalidkey":
		return hex.EncodeToString(m.GlobalIDKey()), nil
	case "histkey":
		return hex.EncodeToString(meta.DDLJobHistoryKey(m, mcInt(c, 1))), nil
	case "schemaversion":
		return m.GetSchemaVersion()
	case "schemaversionnonempty":
		return m.GetSchemaVersionWithNonEmptyDiff()
	case "diffkey":
		return hex.EncodeToString(m.EncodeSchemaDiffKey(mcInt(c, 1))), nil
	case "schemadiff":
		d, err := m.GetSchemaDiff(mcInt(c, 1))
		return jsonOrNil(d), err
	case "bdr":
		r, err := m.GetBDRRole()
		return vec.GoString(r), err
	case "ddltablever":
		v, err := m.CheckDDLTableVersion()
		return int(v), err
	case "systemdbid":
		return m.GetSystemDBID()
	case "mdl":
		e, null, err := m.GetMetadataLock()
		return []bool{e, null}, err
	case "schemacachesize":
		s, null, err := m.GetSchemaCacheSize()
		return []any{s, null}, err
	case "iterdbs":
		stop := mcInt(c, 1)
		out := []any{}
		err := m.IterDatabases(func(db *model.DBInfo) error {
			if stop >= 0 && int64(len(out)) == stop {
				return errStop
			}
			out = append(out, jsonStr(db))
			return nil
		})
		return out, err
	case "itertables":
		stop := mcInt(c, 2)
		out := []any{}
		err := m.IterTables(mcInt(c, 1), func(t *model.TableInfo) error {
			if stop >= 0 && int64(len(out)) == stop {
				return errStop
			}
			out = append(out, tblOut(t))
			return nil
		})
		return out, err
	case "metasbydb":
		ps, err := m.GetMetasByDBID(mcInt(c, 1))
		return pairsOut(ps), err
	case "nametoid":
		names, tbls, err := m.GetAllNameToIDAndTheMustLoadedTableInfo(mcInt(c, 1))
		type kvp struct {
			N vec.GoString `json:"n"`
			I int64        `json:"i"`
		}
		ns := make([]kvp, 0, len(names))
		for n, i := range names {
			ns = append(ns, kvp{vec.GoString(n), i})
		}
		sort.Slice(ns, func(a, b int) bool { return ns[a].N < ns[b].N })
		return []any{ns, tblsOut(tbls)}, err
	case "tableswithattrs":
		var attrs []meta.MustLoadFilterAttr
		mustUnmarshal(c[2], &attrs)
		ts, err := meta.GetTableInfoWithAttributes(m, mcInt(c, 1), attrs...)
		return tblsOut(ts), err
	case "listtables":
		ts, err := m.ListTables(context.Background(), mcInt(c, 1))
		return tblsOut(ts), err
	case "listdbs":
		dbs, err := m.ListDatabases()
		return jsonList(dbs), err
	case "getdb":
		db, err := m.GetDatabase(mcInt(c, 1))
		return jsonOrNil(db), err
	case "listpolicies":
		ps, err := m.ListPolicies()
		return jsonList(ps), err
	case "getpolicy":
		p, err := m.GetPolicy(mcInt(c, 1))
		return jsonOrNil(p), err
	case "listrgs":
		gs, err := m.ListResourceGroups()
		return jsonList(gs), err
	case "getrg":
		g, err := m.GetResourceGroup(mcInt(c, 1))
		return jsonOrNil(g), err
	case "gettable":
		t, err := m.GetTable(mcInt(c, 1), mcInt(c, 2))
		if err != nil {
			return nil, err
		}
		return tblOut(t), nil
	case "tableexists":
		return m.CheckTableExists(mcInt(c, 1), mcInt(c, 2))
	case "alljobs":
		var jobs []*model.Job
		var err error
		switch mcStr(c, 1) {
		case "":
			jobs, err = m.GetAllDDLJobsInQueue()
		default:
			jobs, err = m.GetAllDDLJobsInQueue(meta.JobListKeyType(mcStr(c, 1)))
		}
		return jsonList(jobs), err
	case "histjob":
		j, err := m.GetHistoryDDLJob(mcInt(c, 1))
		return jsonOrNil(j), err
	case "histcount":
		return m.GetHistoryDDLCount()
	case "ingest":
		a, an, err := m.GetIngestMaxBatchSplitRanges()
		if err != nil {
			return nil, err
		}
		b, bn, err := m.GetIngestMaxSplitRangesPerSec()
		if err != nil {
			return nil, err
		}
		cc, cn, err := m.GetIngestMaxInflight()
		if err != nil {
			return nil, err
		}
		d, dn, err := m.GetIngestMaxPerSec()
		return []any{a, an, metaFloat(b), bn, cc, cn, metaFloat(d), dn}, err
	case "lastjobs", "histiter":
		var it meta.LastJobIterator
		var err error
		args := 1
		if mcStr(c, 0) == "lastjobs" {
			it, err = m.GetLastHistoryDDLJobsIterator()
		} else {
			it, err = m.GetHistoryDDLJobsIterator(mcInt(c, 1))
			args = 2
		}
		if err != nil {
			return nil, err
		}
		rounds := []any{}
		var reuse []*model.Job
		for _, n := range mustInts(c[args]) {
			jobs, err := it.GetLastJobs(int(n), reuse)
			if err != nil {
				return rounds, err
			}
			rounds = append(rounds, jsonList(jobs))
			reuse = jobs
		}
		return rounds, nil
	case "bootstrap":
		return m.GetBootstrapVersion()
	case "autoids":
		as := m.GetAutoIDAccessors(mcInt(c, 1), mcInt(c, 2))
		g, err := as.Get()
		if err != nil {
			return nil, err
		}
		var out []int64
		out = append(out, g.RowID, g.IncrementID, g.RandomID)
		// Each accessor is read right after it is picked, as every Go caller does: the pickers
		// share one accessor (an earlier pick follows later ones), which tidb-rs does not
		// reproduce (spec §4).
		for _, pick := range []func() meta.AutoIDAccessor{as.RowID, func() meta.AutoIDAccessor { return as.IncrementID(uint16(mcInt(c, 3))) },
			as.RandomID, as.SequenceValue, as.SequenceCycle} {
			v, err := pick().Get()
			if err != nil {
				return out, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	panic("unknown meta call " + mcStr(c, 0))
}

func mustInts(b json.RawMessage) []int64 {
	var ns []int64
	mustUnmarshal(b, &ns)
	return ns
}

// metaFloat keeps the bits of a float64 (the value may be NaN / ±Inf from a stored text).
func metaFloat(f float64) string { return fmt.Sprintf("%016x", math.Float64bits(f)) }

func keysCall(c metaCall) (any, error) {
	switch mcStr(c, 0) {
	case "DBkey":
		return vec.GoString(meta.DBkey(mcInt(c, 1))), nil
	case "AutoTableIDKey":
		return vec.GoString(meta.AutoTableIDKey(mcInt(c, 1))), nil
	case "AutoIncrementIDKey":
		return vec.GoString(meta.AutoIncrementIDKey(mcInt(c, 1))), nil
	case "AutoRandomTableIDKey":
		return vec.GoString(meta.AutoRandomTableIDKey(mcInt(c, 1))), nil
	case "TableKey":
		return vec.GoString(meta.TableKey(mcInt(c, 1))), nil
	case "SequenceKey":
		return vec.GoString(meta.SequenceKey(mcInt(c, 1))), nil
	case "IsDBkey":
		return meta.IsDBkey(mcHex(c, 1)), nil
	case "IsAutoTableIDKey":
		return meta.IsAutoTableIDKey(mcHex(c, 1)), nil
	case "IsAutoIncrementIDKey":
		return meta.IsAutoIncrementIDKey(mcHex(c, 1)), nil
	case "IsAutoRandomTableIDKey":
		return meta.IsAutoRandomTableIDKey(mcHex(c, 1)), nil
	case "IsTableKey":
		return meta.IsTableKey(mcHex(c, 1)), nil
	case "IsSequenceKey":
		return meta.IsSequenceKey(mcHex(c, 1)), nil
	case "ParseDBKey":
		v, err := meta.ParseDBKey(mcHex(c, 1))
		return v, err
	case "ParseAutoTableIDKey":
		v, err := meta.ParseAutoTableIDKey(mcHex(c, 1))
		return v, err
	case "ParseAutoIncrementIDKey":
		v, err := meta.ParseAutoIncrementIDKey(mcHex(c, 1))
		return v, err
	case "ParseAutoRandomTableIDKey":
		v, err := meta.ParseAutoRandomTableIDKey(mcHex(c, 1))
		return v, err
	case "ParseTableKey":
		v, err := meta.ParseTableKey(mcHex(c, 1))
		return v, err
	case "ParseSequenceKey":
		v, err := meta.ParseSequenceKey(mcHex(c, 1))
		return v, err
	case "IsTableInfoMustLoad":
		return meta.IsTableInfoMustLoad(mcHex(c, 1)), nil
	case "Unescape":
		return vec.GoString(meta.Unescape(string(mcHex(c, 1)))), nil
	case "Element":
		e := &meta.Element{ID: mcInt(c, 1), TypeKey: mcHex(c, 2)}
		return []any{vec.GoString(e.String()), hex.EncodeToString(e.EncodeElement())}, nil
	case "DecodeElement":
		e, err := meta.DecodeElement(mcHex(c, 1))
		if err != nil {
			return nil, err
		}
		return []any{e.ID, hex.EncodeToString(e.TypeKey), vec.GoString(e.String())}, nil
	}
	panic("unknown keys call " + mcStr(c, 0))
}

// ---------------------------------------------------------------- generate

func mjs(v any) json.RawMessage { return vec.MustJSON(v) }

// metaStore is the mockstore whose transactions build states (never committed).
var metaStore kv.Storage

func metaTxn() kv.Transaction {
	if metaStore == nil {
		s, err := mockstore.NewMockStore()
		if err != nil {
			panic(err)
		}
		metaStore = s
	}
	txn, err := metaStore.Begin()
	if err != nil {
		panic(err)
	}
	return txn
}

// dumpTxn returns the transaction's buffered pairs, sorted, and rolls it back.
func dumpTxn(txn kv.Transaction) [][2]string {
	defer func() { _ = txn.Rollback() }()
	it, err := txn.GetMemBuffer().Iter(nil, nil)
	if err != nil {
		panic(err)
	}
	defer it.Close()
	var out [][2]string
	for it.Valid() {
		if len(it.Value()) > 0 {
			out = append(out, [2]string{hx(it.Key()), hx(it.Value())})
		}
		if err := it.Next(); err != nil {
			panic(err)
		}
	}
	return out
}

func setPair(pairs [][2]string, k, v []byte) [][2]string {
	hk := hx(k)
	for i := range pairs {
		if pairs[i][0] == hk {
			if v == nil {
				return append(pairs[:i:i], pairs[i+1:]...)
			}
			pairs[i][1] = hx(v)
			return pairs
		}
	}
	if v == nil {
		return pairs
	}
	pairs = append(pairs, [2]string{hk, hx(v)})
	sort.Slice(pairs, func(a, b int) bool { return pairs[a][0] < pairs[b][0] })
	return pairs
}

var metaNames = []string{
	"t", "test", "mysql", "MySQL", "information_schema", "sys", "a b", `a"b`, `a\b`, `x\"y`,
	`q"`, `\`, "表", "Ünï", "__TiDB_BR_Temporary_x", "t1", "T1", "long_" + strings.Repeat("n", 40),
	"metrics_schema", "PERFORMANCE_SCHEMA", "", "x,y", `"O":"z",`,
}

func metaPick[T any](rng *rand.Rand, xs []T) T { return xs[rng.IntN(len(xs))] }

func metaTable(rng *rand.Rand, id int64) *model.TableInfo {
	t := &model.TableInfo{
		ID:      id,
		Name:    pmodel.NewCIStr(metaPick(rng, metaNames)),
		Charset: "utf8mb4",
		Collate: "utf8mb4_bin",
		State:   model.StatePublic,
		Version: model.CurrLatestTableInfoVersion,
	}
	ft := types.NewFieldType(mysql.TypeLonglong)
	t.Columns = []*model.ColumnInfo{{ID: 1, Name: pmodel.NewCIStr("c"), Offset: 0, FieldType: *ft, State: model.StatePublic}}
	if rng.IntN(4) == 0 {
		t.Version = uint16(rng.IntN(6))
	}
	switch rng.IntN(10) {
	case 0:
		t.Partition = &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Num: 2, Enable: true,
			Definitions: []model.PartitionDefinition{{ID: id + 1, Name: pmodel.NewCIStr("p0")}, {ID: id + 2, Name: pmodel.NewCIStr("p1")}}}
	case 1:
		t.ForeignKeys = []*model.FKInfo{{ID: 1, Name: pmodel.NewCIStr("fk"), RefSchema: pmodel.NewCIStr("t"), RefTable: pmodel.NewCIStr("p"),
			RefCols: []pmodel.CIStr{pmodel.NewCIStr("a")}, Cols: []pmodel.CIStr{pmodel.NewCIStr("c")}, State: model.StatePublic, Version: 1}}
	case 2:
		t.TiFlashReplica = &model.TiFlashReplicaInfo{Count: 1}
	case 3:
		t.TempTableType = model.TempTableGlobal
	case 4:
		t.PlacementPolicyRef = &model.PolicyRefInfo{ID: 9, Name: pmodel.NewCIStr("pp")}
	case 5:
		t.TTLInfo = &model.TTLInfo{ColumnName: pmodel.NewCIStr("c"), IntervalExprStr: "1", IntervalTimeUnit: 0, Enable: true, JobInterval: "1h"}
	case 6:
		t.Affinity = &model.TableAffinityInfo{Level: "table"}
	case 7:
		t.Lock = &model.TableLockInfo{Tp: pmodel.TableLockRead, State: model.TableLockStatePublic}
	}
	if rng.IntN(8) == 0 {
		t.ForeignKeys = []*model.FKInfo{}
	}
	return t
}

func metaJob(rng *rand.Rand, id int64) *model.Job {
	j := &model.Job{
		ID:         id,
		Type:       model.ActionType(1 + rng.IntN(20)),
		SchemaID:   rng.Int64N(100),
		TableID:    rng.Int64N(1000),
		SchemaName: metaPick(rng, metaNames),
		TableName:  metaPick(rng, metaNames),
		State:      model.JobState(rng.IntN(8)),
		Query:      metaPick(rng, metaNames),
		Version:    model.JobVersion(1 + rng.IntN(2)),
		Priority:   rng.IntN(5) - 1,
	}
	if rng.IntN(3) == 0 {
		j.BinlogInfo = &model.HistoryInfo{SchemaVersion: rng.Int64N(50), FinishedTS: rng.Uint64N(1 << 40)}
	}
	return j
}

func metaGenerate(g *vec.Gen, rng *rand.Rand) {
	metaGenKeys(g, rng)
	metaGenMetadef(g)
	for i := 0; i < 400; i++ {
		metaGenStruct(g, rng, fmt.Sprintf("rand/%d", i))
	}
	for i := 0; i < 500; i++ {
		metaGenMeta(g, rng, fmt.Sprintf("rand/%d", i))
	}
	metaGenFixed(g)
}

func metaGenMetadef(g *vec.Gen) {
	for _, f := range []string{"IsMemOrSysDB", "IsMemDB", "IsSystemRelatedDB", "IsSystemDB", "IsBRRelatedDB"} {
		for _, s := range []string{"information_schema", "INFORMATION_SCHEMA", "performance_schema", "metrics_schema",
			"mysql", "sys", "test", "", "__TiDB_BR_Temporary_", "__TiDB_BR_Temporary_x", "__tidb_br_temporary_x", "mysql "} {
			g.Add("fixed", "metadef", f, s)
		}
	}
}

func metaGenKeys(g *vec.Gen, rng *rand.Rand) {
	ids := []int64{0, 1, -1, 42, math.MaxInt64, math.MinInt64, 1 << 48, 999999999999}
	for _, f := range []string{"DBkey", "AutoTableIDKey", "AutoIncrementIDKey", "AutoRandomTableIDKey", "TableKey", "SequenceKey"} {
		for _, id := range ids {
			g.Add("fixed", "keys", mjs([]any{f, id}))
		}
	}
	keyInputs := []string{"DB:1", "DB:", "DB:-5", "DB:+5", "DB:x", "DB:007", "DB:9223372036854775807", "DB:9223372036854775808",
		"DB:-9223372036854775809", "DB:1 ", "DB", "db:1", "Table:3", "Table3", "Table:", "Table:abc", "TID:5", "TID:", "IID:6",
		"TARID:7", "SID:8", "SID:-", "SID:+", "Tab", "", "TableX:9", "TID:12345678901234567890", "IID:1_0", "TARID: 3",
		"Table:" + strings.Repeat("9", 19), "Table:" + strings.Repeat("9", 18), "DB:-" + strings.Repeat("9", 18), "SID:１"}
	for _, f := range []string{"IsDBkey", "IsAutoTableIDKey", "IsAutoIncrementIDKey", "IsAutoRandomTableIDKey", "IsTableKey", "IsSequenceKey",
		"ParseDBKey", "ParseAutoTableIDKey", "ParseAutoIncrementIDKey", "ParseAutoRandomTableIDKey", "ParseTableKey", "ParseSequenceKey"} {
		for _, s := range keyInputs {
			g.Add("fixed", "keys", mjs([]any{f, hx([]byte(s))}))
		}
		for i := 0; i < 8; i++ {
			pfx := metaPick(rng, []string{"DB:", "Table:", "TID:", "IID:", "TARID:", "SID:", "Ta", "D"})
			g.Add("rand", "keys", mjs([]any{f, hx([]byte(pfx + metaRandDigits(rng)))}))
		}
	}
	mustLoad := []string{
		`{"id":1,"fk_info":null,"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null}`,
		`{"id":1,"fk_info":[],"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null}`,
		`{"id":1,"fk_info":[{}],"partition":null}`,
		`{"partition":null,"fk_info":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null}`,
		`{"fk_info":null,"partition":{},"Lock":null}`,
		`{"fk_info":null,"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null,"affinity":{"level":"x"}}`,
		`{"fk_info":null,"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":1,"policy_ref_info":null,"ttl_info":null}`,
		`{"fk_info":null,"ttl_info":null,"partition":null}`, ``, `{}`, `"fk_info":null`,
	}
	for _, s := range mustLoad {
		g.Add("fixed", "keys", mjs([]any{"IsTableInfoMustLoad", hx([]byte(s))}))
	}
	for i := 0; i < 60; i++ {
		t := metaTable(rng, int64(i+1))
		b, _ := json.Marshal(t)
		g.Add("rand", "keys", mjs([]any{"IsTableInfoMustLoad", hx(b)}))
	}
	for _, s := range []string{`a\"b`, `a\\b`, `\\"`, `\"\\`, `plain`, `\`, `\\\\"`, "表\\\"", ""} {
		g.Add("fixed", "keys", mjs([]any{"Unescape", hx([]byte(s))}))
	}
	for _, e := range [][]any{{int64(1), "_col_"}, {int64(-1), "_idx_"}, {int64(math.MaxInt64), "_idx_"}, {int64(0), "_ab"}, {int64(7), "_toolong_"}, {int64(3), ""}} {
		g.Add("fixed", "keys", mjs([]any{"Element", e[0], hx([]byte(e[1].(string)))}))
	}
	encs := [][]byte{
		append([]byte("_col_"), 0, 0, 0, 0, 0, 0, 0, 5),
		append([]byte("_idx_"), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff),
		append([]byte("_idx_"), 0, 0, 0, 0, 0, 0, 0, 1, 9),
		append([]byte("_xyz_"), 0, 0, 0, 0, 0, 0, 0, 1),
		[]byte("_col_1234567"), []byte(""), []byte("_col_\x00\x00\x00"),
		append([]byte("\"\\\n\xff_"), 0, 0, 0, 0, 0, 0, 0, 1),
	}
	for _, b := range encs {
		g.Add("fixed", "keys", mjs([]any{"DecodeElement", hx(b)}))
	}
}

func metaRandDigits(rng *rand.Rand) string {
	switch rng.IntN(6) {
	case 0:
		return fmt.Sprint(rng.Int64())
	case 1:
		return fmt.Sprint(-rng.Int64())
	case 2:
		return fmt.Sprint(rng.Uint64())
	case 3:
		return ""
	case 4:
		return fmt.Sprintf("%d%c", rng.IntN(100), "x-+ 9"[rng.IntN(5)])
	}
	return fmt.Sprint(rng.IntN(1000))
}

func metaRandBytes(rng *rand.Rand, max int) []byte {
	n := rng.IntN(max + 1)
	b := make([]byte, n)
	for i := range b {
		switch rng.IntN(4) {
		case 0:
			b[i] = byte(rng.IntN(256))
		default:
			const alpha = "abcHhLlSs01:\x00\xff"
			b[i] = alpha[rng.IntN(len(alpha))]
		}
	}
	return b
}

func metaGenStruct(g *vec.Gen, rng *rand.Rand, src string) {
	prefix := []byte(metaPick(rng, []string{"m", "", "pfx", "\x00", "\xff"}))
	txn := metaTxn()
	t := structure.NewStructure(txn, txn, prefix)
	var hashes, lists, strs [][]byte
	for i := rng.IntN(4); i >= 0; i-- {
		hashes = append(hashes, metaRandBytes(rng, 6))
	}
	for i := rng.IntN(3); i >= 0; i-- {
		lists = append(lists, metaRandBytes(rng, 6))
	}
	for i := rng.IntN(3); i >= 0; i-- {
		strs = append(strs, metaRandBytes(rng, 6))
	}
	for i := rng.IntN(20); i > 0; i-- {
		h := metaPick(rng, hashes)
		f := metaRandBytes(rng, 5)
		var v []byte
		if rng.IntN(3) == 0 {
			v = []byte(fmt.Sprint(rng.Int64N(2000) - 1000))
		} else {
			v = append([]byte{'v'}, metaRandBytes(rng, 6)...)
		}
		if err := t.HSet(h, f, v); err != nil {
			panic(err)
		}
	}
	for i := rng.IntN(12); i > 0; i-- {
		l := metaPick(rng, lists)
		v := append([]byte{'e'}, metaRandBytes(rng, 4)...)
		var err error
		switch rng.IntN(4) {
		case 0:
			err = t.LPush(l, v)
		case 1:
			_, err = t.LPop(l)
		default:
			err = t.RPush(l, v)
		}
		if err != nil {
			panic(err)
		}
	}
	for _, s := range strs {
		if rng.IntN(4) == 0 {
			continue
		}
		v := append([]byte{'s'}, metaRandBytes(rng, 6)...)
		if rng.IntN(2) == 0 {
			v = []byte(metaRandDigits(rng))
			if len(v) == 0 {
				v = []byte("0")
			}
		}
		if err := t.Set(s, v); err != nil {
			panic(err)
		}
	}
	pairs := dumpTxn(txn)
	// Foreign and corrupt keys.
	for i := rng.IntN(4); i > 0; i-- {
		switch rng.IntN(5) {
		case 0: // a hash data key with a garbage tail (field decode error)
			k := append(t.EncodeHashMetaKey(metaPick(rng, hashes))[:0:0], t.EncodeHashDataKey(metaPick(rng, hashes), nil)...)
			k = k[:len(k)-9]
			k = append(k, metaRandBytes(rng, 3)...)
			pairs = setPair(pairs, k, []byte("junk"))
		case 1: // a key under the prefix with another type flag
			pairs = setPair(pairs, t.EncodeHashMetaKey(metaPick(rng, hashes)), []byte("meta"))
		case 2: // bad list meta
			l := metaPick(rng, lists)
			lk := structListMetaKey(prefix, l)
			pairs = setPair(pairs, lk, metaRandBytes(rng, 20))
		case 3: // a dangling list element removed
			l := metaPick(rng, lists)
			for j := int64(-3); j < 3; j++ {
				pairs = setPair(pairs, structListDataKey(prefix, l, j), nil)
				if rng.IntN(2) == 0 {
					break
				}
			}
		case 4: // a non-numeric value in a hash used as an int64
			pairs = setPair(pairs, t.EncodeHashDataKey(metaPick(rng, hashes), []byte("n")), []byte(metaPick(rng, []string{"x", "1e3", " 1", "9223372036854775808", "-0", "+7"})))
		}
	}
	var calls []any
	pickKey := func(xs [][]byte) string {
		if rng.IntN(6) == 0 {
			return hx(metaRandBytes(rng, 4))
		}
		return hx(metaPick(rng, xs))
	}
	for n := 3 + rng.IntN(12); n > 0; n-- {
		h, l, s := pickKey(hashes), pickKey(lists), pickKey(strs)
		var c []any
		switch rng.IntN(20) {
		case 0:
			c = []any{"get", s}
		case 1:
			c = []any{"getint64", s}
		case 2:
			c = []any{"iterate", pickKey(strs), pickKey(strs), int64(rng.IntN(4)) - 1}
		case 3:
			c = []any{"hget", h, hx(metaRandBytes(rng, 5))}
		case 4:
			c = []any{"hgetint64", h, metaPick(rng, []string{hx([]byte("n")), hx(metaRandBytes(rng, 5))})}
		case 5:
			c = []any{"hkeys", h}
		case 6:
			c = []any{"hgetall", h}
		case 7:
			c = []any{"hgetiter", h, int64(rng.IntN(4)) - 1}
		case 8:
			c = []any{"hgetlen", h}
		case 9:
			c = []any{"hgetlastn", h, int64(rng.IntN(5))}
		case 10:
			c = []any{"iteratehash", h, int64(rng.IntN(4)) - 1}
		case 11:
			c = []any{"iterbounded", pickKey(hashes), pickKey(hashes), int64(rng.IntN(5)) - 1}
		case 12:
			var f any
			if rng.IntN(2) == 0 {
				f = hx(metaRandBytes(rng, 5))
			}
			c = []any{"hrev", h, f, int64(rng.IntN(6))}
		case 13:
			c = []any{"lgetall", l}
		case 14:
			c = []any{"llen", l}
		case 15:
			c = []any{"lindex", l, int64(rng.IntN(9)) - 4}
		case 16:
			c = []any{"encstr", hx(metaRandBytes(rng, 10))}
		case 17:
			c = []any{"enchashmeta", hx(metaRandBytes(rng, 10))}
		case 18:
			c = []any{"enchashdata", hx(metaRandBytes(rng, 10)), hx(metaRandBytes(rng, 10))}
		default:
			c = []any{"iterate", s, hx(nil), int64(-1)}
		}
		calls = append(calls, c)
	}
	g.Add(src, "struct", hx(prefix), pairs, calls)
}

func structListMetaKey(prefix, key []byte) []byte {
	t := structure.NewStructure(nil, nil, prefix)
	k := t.EncodeHashMetaKey(key)
	k[len(k)-1] = byte(structure.ListMeta)
	return k
}

func structListDataKey(prefix, key []byte, idx int64) []byte {
	t := structure.NewStructure(nil, nil, prefix)
	k := t.EncodeHashMetaKey(key)
	k[len(k)-1] = byte(structure.ListData)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(idx)^(1<<63))
	return append(k, b[:]...)
}

type metaState struct {
	dbs      []int64
	tables   map[int64][]int64
	policies []int64
	rgs      []int64
	jobs     []int64
	queue    int
	maxVer   int64
}

func metaGenMeta(g *vec.Gen, rng *rand.Rand, src string) {
	txn := metaTxn()
	m := meta.NewMutator(txn)
	st := metaState{tables: map[int64][]int64{}}
	id := int64(1 + rng.IntN(5))
	next := func() int64 { id += int64(1 + rng.IntN(3)); return id }
	for i := rng.IntN(4); i >= 0; i-- {
		dbID := next()
		name := metaPick(rng, metaNames)
		if i == 0 && rng.IntN(2) == 0 {
			name = "mysql"
		}
		must(m.CreateDatabase(&model.DBInfo{ID: dbID, Name: pmodel.NewCIStr(name), Charset: "utf8mb4", Collate: "utf8mb4_bin", State: model.StatePublic}))
		st.dbs = append(st.dbs, dbID)
		for j := rng.IntN(5); j > 0; j-- {
			tid := next()
			t := metaTable(rng, tid)
			if rng.IntN(3) == 0 {
				must(m.CreateTableAndSetAutoID(dbID, t, model.AutoIDGroup{RowID: rng.Int64N(1000), IncrementID: rng.Int64N(1000), RandomID: rng.Int64N(10)}))
			} else {
				must(m.CreateTableOrView(dbID, t))
			}
			st.tables[dbID] = append(st.tables[dbID], tid)
			if rng.IntN(5) == 0 {
				must(m.GetAutoIDAccessors(dbID, tid).SequenceValue().Put(rng.Int64N(100)))
				must(m.GetAutoIDAccessors(dbID, tid).SequenceCycle().Put(rng.Int64N(3)))
			}
		}
	}
	if rng.IntN(2) == 0 {
		_, err := m.GenGlobalIDs(1 + rng.IntN(100))
		must(err)
	}
	for i := rng.IntN(3); i > 0; i-- {
		pid := int64(1 + rng.IntN(5))
		if err := m.CreatePolicy(&model.PolicyInfo{ID: pid, Name: pmodel.NewCIStr(metaPick(rng, metaNames)), State: model.StatePublic,
			PlacementSettings: &model.PlacementSettings{PrimaryRegion: "r1", Regions: "r1,r2", Followers: uint64(rng.IntN(3))}}); err == nil {
			st.policies = append(st.policies, pid)
		}
		_, err := m.GenPlacementPolicyID()
		must(err)
	}
	for i := rng.IntN(3); i > 0; i-- {
		gid := int64(1 + rng.IntN(4))
		name := metaPick(rng, []string{"default", "rg1", "Default", "rg2"})
		if err := m.AddResourceGroup(&model.ResourceGroupInfo{ID: gid, Name: pmodel.NewCIStr(name), State: model.StatePublic,
			ResourceGroupSettings: &model.ResourceGroupSettings{RURate: uint64(rng.IntN(1000)), Priority: uint64(rng.IntN(3)), BurstLimit: int64(rng.IntN(5)) - 1}}); err == nil {
			st.rgs = append(st.rgs, gid)
		}
	}
	for i := rng.IntN(5); i > 0; i-- {
		v, err := m.GenSchemaVersion()
		must(err)
		st.maxVer = v
		if rng.IntN(3) != 0 {
			must(m.SetSchemaDiff(&model.SchemaDiff{Version: v, Type: model.ActionType(1 + rng.IntN(10)), SchemaID: rng.Int64N(10), TableID: rng.Int64N(100)}))
		}
	}
	for i := rng.IntN(6); i > 0; i-- {
		jid := next()
		must(m.AddHistoryDDLJob(metaJob(rng, jid), false))
		st.jobs = append(st.jobs, jid)
	}
	for i := rng.IntN(4); i > 0; i-- {
		lk := meta.DefaultJobListKey
		if rng.IntN(3) == 0 {
			lk = meta.AddIndexJobListKey
		}
		must(m.EnQueueDDLJob(metaJob(rng, next()), lk))
		st.queue++
	}
	if rng.IntN(3) == 0 {
		must(m.SetBDRRole(metaPick(rng, []string{"primary", "secondary", "local_only"})))
	}
	if rng.IntN(3) == 0 {
		must(m.SetDDLTables(meta.DDLTableVersion(rng.IntN(5))))
	}
	if rng.IntN(3) == 0 {
		must(m.SetMetadataLock(rng.IntN(2) == 0))
	}
	if rng.IntN(3) == 0 {
		must(m.SetSchemaCacheSize(rng.Uint64N(1 << 33)))
	}
	if rng.IntN(3) == 0 {
		must(m.FinishBootstrap(rng.Int64N(250)))
	}
	if rng.IntN(3) == 0 {
		must(m.SetIngestMaxBatchSplitRanges(rng.IntN(100)))
		must(m.SetIngestMaxSplitRangesPerSec(rng.Float64() * 100))
		must(m.SetIngestMaxInflight(rng.IntN(100)))
		must(m.SetIngestMaxPerSec(rng.Float64() * 1000))
	}
	pairs := dumpTxn(txn)
	t := structure.NewStructure(nil, nil, []byte("m"))
	// Corruptions.
	for i := rng.IntN(3); i > 0; i-- {
		switch rng.IntN(12) {
		case 0:
			if len(st.dbs) > 0 {
				pairs = setPair(pairs, t.EncodeHashDataKey([]byte("DBs"), meta.DBkey(metaPick(rng, st.dbs))), []byte(metaPick(rng, []string{"{", `{"id":"x"}`, `[]`, `{"db_name":{"O":1}}`})))
			}
		case 1:
			if len(st.dbs) > 0 {
				db := metaPick(rng, st.dbs)
				if ts := st.tables[db]; len(ts) > 0 {
					pairs = setPair(pairs, t.EncodeHashDataKey(meta.DBkey(db), meta.TableKey(metaPick(rng, ts))), []byte(metaPick(rng, []string{
						`{"id":5,"name":{"O":"a","L":"a"},"cols":"x"}`, `{"id":7,"name":{"O":"q\"x","L":"q\"x"},"fk_info":null}`, `{bad`,
					})))
				}
			}
		case 2:
			if len(st.policies) > 0 {
				pairs = setPair(pairs, t.EncodeHashDataKey([]byte("Policies"), []byte(fmt.Sprintf("Policy:%d", metaPick(rng, st.policies)))),
					append([]byte{metaPick(rng, []byte{0x01, 0x3f, 0x40, 0xff})}, []byte(`{"id":1}`)...))
			}
		case 3:
			if len(st.rgs) > 0 {
				pairs = setPair(pairs, t.EncodeHashDataKey([]byte("ResourceGroups"), []byte(fmt.Sprintf("RG:%d", metaPick(rng, st.rgs)))),
					append([]byte{0}, []byte(metaPick(rng, []string{"{", `{"id":3,"name":{"O":"default","L":"default"}}`}))...))
			}
		case 4:
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("SchemaVersionKey")), []byte(metaPick(rng, []string{"x", "12", "-3", "99999999999999999999"})))
		case 5:
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("DDLTableVersion")), []byte(metaPick(rng, []string{"x", "3", "-1", "+2"})))
		case 6:
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("SchemaCacheSize")), []byte(metaPick(rng, []string{"x", "-1", "18446744073709551616", "+5"})))
		case 7:
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("metadataLock")), []byte(metaPick(rng, []string{"1", "0", "11", "true"})))
		case 8:
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("IngestMaxReqPerSec")), []byte(metaPick(rng, []string{"NaN", "inf", "1e309", "x", "0x1p3", "1_0"})))
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("IngestMaxInflight")), []byte(metaPick(rng, []string{"7", "x", "+3", "-0"})))
		case 9:
			if len(st.jobs) > 0 {
				var b [8]byte
				binary.BigEndian.PutUint64(b[:], uint64(metaPick(rng, st.jobs)))
				pairs = setPair(pairs, t.EncodeHashDataKey([]byte("DDLJobHistory"), b[:]), []byte(metaPick(rng, []string{"{", `{"id":"x"}`, `{"id":5,"priority":9}`})))
			}
		case 10:
			pairs = setPair(pairs, structListMetaKey([]byte("m"), []byte("DDLJobList")), metaRandBytes(rng, 17))
		case 11:
			pairs = setPair(pairs, t.EncodeStringDataKey([]byte("BDRRole")), []byte(metaPick(rng, []string{"", "\xff\x00", "PRIMARY"})))
		}
	}
	dbOrRand := func() int64 {
		if len(st.dbs) == 0 || rng.IntN(6) == 0 {
			return rng.Int64N(30)
		}
		return metaPick(rng, st.dbs)
	}
	tableOf := func(db int64) int64 {
		if ts := st.tables[db]; len(ts) > 0 && rng.IntN(5) != 0 {
			return metaPick(rng, ts)
		}
		return rng.Int64N(40)
	}
	var calls []any
	for n := 4 + rng.IntN(14); n > 0; n-- {
		db := dbOrRand()
		var c []any
		switch rng.IntN(31) {
		case 0:
			c = []any{"globalid"}
		case 1:
			c = []any{"policyid"}
		case 2:
			c = []any{"globalidkey"}
		case 3:
			c = []any{"histkey", rng.Int64N(100)}
		case 4:
			c = []any{"schemaversion"}
		case 5:
			c = []any{"schemaversionnonempty"}
		case 6:
			c = []any{"diffkey", rng.Int64N(10)}
		case 7:
			c = []any{"schemadiff", rng.Int64N(st.maxVer + 2)}
		case 8:
			c = []any{"bdr"}
		case 9:
			c = []any{"ddltablever"}
		case 10:
			c = []any{"systemdbid"}
		case 11:
			c = []any{"mdl"}
		case 12:
			c = []any{"schemacachesize"}
		case 13:
			c = []any{"iterdbs", int64(rng.IntN(4)) - 1}
		case 14:
			c = []any{"itertables", db, int64(rng.IntN(4)) - 1}
		case 15:
			c = []any{"metasbydb", db}
		case 16:
			c = []any{"tableswithattrs", db, metaAttrs(rng)}
		case 17:
			c = []any{"listtables", db}
		case 18:
			c = []any{"listdbs"}
		case 19:
			c = []any{"getdb", db}
		case 20:
			c = []any{"listpolicies"}
		case 21:
			c = []any{"getpolicy", rng.Int64N(7)}
		case 22:
			c = []any{"listrgs"}
		case 23:
			c = []any{"getrg", rng.Int64N(6)}
		case 24:
			c = []any{"gettable", db, tableOf(db)}
		case 25:
			c = []any{"tableexists", db, tableOf(db)}
		case 26:
			c = []any{"alljobs", metaPick(rng, []string{"", "", "DDLJobAddIdxList", "nolist"})}
		case 27:
			var jid int64 = rng.Int64N(60)
			if len(st.jobs) > 0 && rng.IntN(3) != 0 {
				jid = metaPick(rng, st.jobs)
			}
			c = []any{metaPick(rng, []string{"histjob", "histjob", "histcount"}), jid}
		case 28:
			c = []any{"ingest"}
		case 29:
			rounds := []int64{int64(rng.IntN(4)), int64(rng.IntN(4))}
			if rng.IntN(2) == 0 {
				c = []any{"lastjobs", rounds}
			} else {
				start := rng.Int64N(60)
				if len(st.jobs) > 0 && rng.IntN(2) == 0 {
					start = metaPick(rng, st.jobs)
				}
				c = []any{"histiter", start, rounds}
			}
		default:
			if rng.IntN(2) == 0 {
				c = []any{"bootstrap"}
			} else {
				c = []any{"autoids", db, tableOf(db), int64(rng.IntN(7))}
			}
		}
		calls = append(calls, c)
	}
	g.Add(src, "meta", pairs, calls)
	// nametoid can panic (regexp miss on a corrupt value): its own record.
	if len(st.dbs) > 0 && rng.IntN(2) == 0 {
		g.Add(src, "meta", pairs, []any{[]any{"nametoid", dbOrRand()}})
	}
}

func metaAttrs(rng *rand.Rand) []meta.MustLoadFilterAttr {
	all := []meta.MustLoadFilterAttr{
		{Attr: `"partition":null`, LoadIfMissing: true}, {Attr: `"Lock":null`, LoadIfMissing: true},
		{Attr: `"tiflash_replica":null`, LoadIfMissing: true}, {Attr: `"temp_table_type":0`, LoadIfMissing: true},
		{Attr: `"policy_ref_info":null`, LoadIfMissing: true}, {Attr: `"ttl_info":null`, LoadIfMissing: true},
		{Attr: `"affinity":{`, LoadIfMissing: false}, {Attr: `"fk_info":null`, LoadIfMissing: true},
		{Attr: `"partition":{`, LoadIfMissing: false}, {Attr: `"id":`, LoadIfMissing: false},
	}
	var out []meta.MustLoadFilterAttr
	for i := rng.IntN(4); i > 0; i-- {
		out = append(out, metaPick(rng, all))
	}
	return out
}

// metaGenFixed: hand-written states for the edge cases random states rarely reach.
func metaGenFixed(g *vec.Gen) {
	t := structure.NewStructure(nil, nil, []byte("m"))
	db := func(id int64, js string) [2]string {
		return [2]string{hx(t.EncodeHashDataKey([]byte("DBs"), meta.DBkey(id))), hx([]byte(js))}
	}
	tbl := func(dbID, id int64, js string) [2]string {
		return [2]string{hx(t.EncodeHashDataKey(meta.DBkey(dbID), meta.TableKey(id))), hx([]byte(js))}
	}
	sorted := func(ps ...[2]string) [][2]string {
		sort.Slice(ps, func(a, b int) bool { return ps[a][0] < ps[b][0] })
		return ps
	}
	base := sorted(
		db(1, `{"id":1,"db_name":{"O":"test","L":"test"},"charset":"utf8mb4","collate":"utf8mb4_bin","state":5}`),
		db(2, `{"id":2,"db_name":{"O":"mysql","L":"mysql"},"state":5}`),
		tbl(1, 10, `{"id":10,"name":{"O":"a\"b\\c","L":"a\"b\\c"},"fk_info":null,"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null}`),
		tbl(1, 11, `{"id":11,"name":{"O":"P","L":"p"},"fk_info":[],"partition":{"type":1},"Lock":null}`),
		tbl(1, 12, `{"name":{"O":"noid","L":"noid"},"fk_info":null}`),
		[2]string{hx(t.EncodeHashDataKey(meta.DBkey(1), []byte("TID:10"))), hx([]byte("77"))},
		[2]string{hx(t.EncodeHashDataKey(meta.DBkey(1), []byte("Tbl:13"))), hx([]byte(`{}`))},
	)
	for _, c := range []string{"listdbs", "systemdbid", "iterdbs"} {
		g.Add("fixed", "meta", base, []any{[]any{c, int64(-1)}})
	}
	for _, c := range [][]any{
		{"itertables", int64(1), int64(-1)}, {"listtables", int64(1)}, {"metasbydb", int64(1)}, {"getdb", int64(1)},
		{"gettable", int64(1), int64(10)}, {"gettable", int64(1), int64(99)}, {"gettable", int64(5), int64(10)},
		{"tableexists", int64(1), int64(12)}, {"tableexists", int64(9), int64(1)}, {"autoids", int64(1), int64(10), int64(4)},
		{"autoids", int64(1), int64(10), int64(5)}, {"tableswithattrs", int64(1), []meta.MustLoadFilterAttr{{Attr: `"partition":{`}}},
	} {
		g.Add("fixed", "meta", base, []any{c})
	}
	// The regexps of GetAllNameToIDAndTheMustLoadedTableInfo: table 12 has no "id" (Go
	// panics: index out of range), so it gets a state of its own without 12, and one with.
	g.Add("fixed", "meta", base, []any{[]any{"nametoid", int64(1)}})
	noid := sorted(base[0], base[1], tbl(1, 10, `{"id":10,"name":{"O":"a\"b\\c","L":"a\"b\\c"},"fk_info":null,"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null}`),
		tbl(1, 11, `{"id":99999999999999999999,"name":{"O":"P","L":"p"},"fk_info":[]}`))
	g.Add("fixed", "meta", noid, []any{[]any{"nametoid", int64(1)}})
	ok := sorted(base[0], tbl(1, 10, `{"id":10,"name":{"O":"a\"b\\c","L":"x\\\"y"},"fk_info":null,"partition":null,"Lock":null,"tiflash_replica":null,"temp_table_type":0,"policy_ref_info":null,"ttl_info":null}`),
		tbl(1, 14, `{"id":14,"name":{"O":"Q","L":"q"},"fk_info":null,"partition":{"type":1},"Lock":null}`),
		tbl(1, 15, `{"id":15 ,"name":{"O":"表","L":"表"},"fk_info":[{"id":1}],"partition":null}`))
	g.Add("fixed", "meta", ok, []any{[]any{"nametoid", int64(1)}, []any{"nametoid", int64(4)}})
	// Empty store.
	var empty [][2]string
	g.Add("fixed", "meta", empty, []any{[]any{"globalid"}, []any{"schemaversion"}, []any{"schemaversionnonempty"}, []any{"listdbs"},
		[]any{"listpolicies"}, []any{"listrgs"}, []any{"getrg", int64(1)}, []any{"getrg", int64(2)}, []any{"getpolicy", int64(1)},
		[]any{"bdr"}, []any{"ddltablever"}, []any{"mdl"}, []any{"schemacachesize"}, []any{"alljobs", ""}, []any{"histcount", int64(0)},
		[]any{"lastjobs", []int64{3}}, []any{"ingest"}, []any{"bootstrap"}, []any{"systemdbid"}, []any{"getdb", int64(1)}})
	// Schema version without a diff.
	sv := sorted([2]string{hx(t.EncodeStringDataKey([]byte("SchemaVersionKey"))), hx([]byte("5"))},
		[2]string{hx(t.EncodeStringDataKey([]byte("Diff:4"))), hx([]byte(`{"version":4}`))},
		[2]string{hx(t.EncodeStringDataKey([]byte("Diff:5"))), hx([]byte(`{"version":"x"}`))})
	g.Add("fixed", "meta", sv, []any{[]any{"schemaversionnonempty"}, []any{"schemadiff", int64(4)}, []any{"schemadiff", int64(5)}})
	sv2 := sorted([2]string{hx(t.EncodeStringDataKey([]byte("SchemaVersionKey"))), hx([]byte("0"))})
	g.Add("fixed", "meta", sv2, []any{[]any{"schemaversionnonempty"}})
}
