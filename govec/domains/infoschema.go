// Domain "infoschema": block D06.3 part 1, infoschema v2 build and cache
// (pkg/infoschema builder.go, builder_misc.go, bundle_builder.go, infoschema_v2.go,
// the infoSchemaMisc part of infoschema.go, sieve.go through the table cache, and
// pkg/infoschema/context). Owner decision: only v2 is ported (v1 exempt).
//
// One op, "build": args [steps]. A record is a scenario on one infoschema.Data, as a
// domain sees it: a full load, then batches of schema diffs (as tryLoadSchemaDiffs
// applies them), full reloads into the same Data, GC, a tiny cache capacity, and
// queries of the newest and of older InfoSchema versions. A step:
//   - {"k":"full","ts":T,"ver":V,"pairs":P}: the meta state P (KV pairs, sorted) at
//     commit ts T; fetchAllSchemasWithTables as domain.go does for v2 (ListDatabases,
//     GetAllNameToIDAndTheMustLoadedTableInfo, ConvertOldVersionUTF8ToUTF8MB4IfNeed,
//     ConvertCharsetCollateToLowerCaseIfNeed), ListPolicies, ListResourceGroups, then
//     NewBuilder(...).InitWithDBInfos(..., V) and Build(T);
//   - {"k":"diffs","ts":T,"pairs":P,"diffs":[SchemaDiff JSON]}: NewBuilder,
//     InitWithOldInfoSchema(latest), WithStore, SetDeltaUpdateBundles, ApplyDiff each
//     (reader on P), Build(T). An ApplyDiff error ends the step (no Build);
//   - {"k":"gc","ver":V}: Data.GCOldVersion(V);
//   - {"k":"cap","n":N}: Data.SetCacheCapacity(N) (N = 1 keeps at most the last table
//     set, whatever the entry sizes: exception A3 makes them differ from Rust's);
//   - {"k":"query","at":I}: the queries on the I-th built InfoSchema (an older version).
//
// Every built InfoSchema is queried right after Build. The out of a step is
// {"ids":[...], "err":..., "q":<queries>} (see isQueries). After an ApplyDiff error the
// step does what the domain does, a full load (at the step's highest diff version), and
// "q" queries it. A panic ends the step ({"panic": msg}).
//
// The store (autoid.Requirement.Store()) is an MVCC map of the states by commit ts:
// GetSnapshot(v) is the newest state with ts <= v (empty before the first); its Begin
// (updateAutoIDForExchangePartition's write, X in tidb-rs) goes to a scratch mockstore.
package domains

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/infoschema"
	infoschemacontext "github.com/pingcap/tidb/pkg/infoschema/context"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta"
	"github.com/pingcap/tidb/pkg/meta/autoid"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/store/mockstore"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/tikv/client-go/v2/tikv"
	"unsafe"
)

// isDrivers is infoschema's registry of memory schemas (`drivers`, a slice of
// *virtualTableDriver). The memory schemas (information_schema, metrics_schema) are
// block D06.6: records are built without them.
//
//go:linkname isDrivers github.com/pingcap/tidb/pkg/infoschema.drivers
var isDrivers []unsafe.Pointer

const infoschemaSeed = 20261114

func init() {
	vec.Register(&vec.Domain{
		Name:     "infoschema",
		Seed:     infoschemaSeed,
		Coverage: infoschemaCoverage,
		Blocks:   []string{"D06.3"},
		Exempt:   infoschemaExempt,
		Gzip:     true,
		Generate: infoschemaGenerate,
		Exec:     infoschemaExec,
	})
}

var infoschemaCoverage = map[string]vec.Coverage{
	"build": {Min: 150, GoFuncs: []string{
		// builder.go (v2 paths)
		"Builder.ApplyDiff", "Builder.applyCreateTables", "equalPlacementPolicy", "applyRefreshMeta",
		"applyTruncateTableOrPartition", "applyDropTableOrPartition", "applyReorganizePartition",
		"applyExchangeTablePartition", "applyRecoverTable", "Builder.applyAffectedOpts", "applyDefaultAction",
		"Builder.getTableIDs", "Builder.updateBundleForTableUpdate", "dropTableForUpdate", "getKeptAllocators",
		"appendAffectedIDs", "Builder.applyCreateSchema", "Builder.buildAllocsForCreateTable", "applyCreateTable",
		"ConvertCharsetCollateToLowerCaseIfNeed", "ConvertOldVersionUTF8ToUTF8MB4IfNeed", "Builder.Build",
		"Builder.InitWithOldInfoSchema", "Builder.initVirtualTables", "Builder.InitWithDBInfos", "tableFromMeta",
		"Builder.createSchemaTablesForDB", "Builder.addDB", "Builder.addTable", "NewBuilder", "Builder.WithStore",
		"tableIDIsValid", "Builder.schemaByID",
		// builder_misc.go
		"applyCreatePolicy", "applyAlterPolicy", "applyDropPolicy", "applyCreateOrAlterResourceGroup",
		"applyDropResourceGroup", "Builder.addTemporaryTable", "Builder.initMisc",
		// bundle_builder.go (v2 paths)
		"policyGetter.GetPolicy", "bundleInfoBuilder.initBundleInfoBuilder", "bundleInfoBuilder.SetDeltaUpdateBundles",
		"bundleInfoBuilder.deleteBundle", "bundleInfoBuilder.markTableBundleShouldUpdate",
		"bundleInfoBuilder.markBundlesReferPolicyShouldUpdate", "bundleInfoBuilder.updateTableBundles",
		// infoschema_v2.go
		"schemaItem.Name", "btreeSet", "NewData", "Data.add", "Data.addDB", "Data.remove",
		"Data.deleteDB", "referredForeignKeysHelper.onItem", "Data.getTableReferredForeignKeys",
		"Data.hasForeignKeyReference", "Data.addReferredForeignKeys", "Data.deleteReferredForeignKeys",
		"gcCollectTableItem", "gcCollectReferredForeignKeyItem", "Data.gcOldFKVersion", "Data.GCOldVersion",
		"Data.resetBeforeFullLoad", "resetByIDBeforeFullLoad", "resetByNameBeforeFullLoad",
		"resetTableInfoResidentBeforeFullLoad", "resetSchemaMapBeforeFullLoad", "resetSchemaID2NameBeforeFullLoad",
		"resetPID2TIDBeforeFullLoad", "resetFKBeforeFullLoad", "compareByID", "compareByName", "compareTableInfoItem",
		"comparePartitionItem", "compareSchemaItem", "compareSchemaByID", "compareReferredForeignKeyItem",
		"NewInfoSchemaV2", "search", "infoschemaV2.base", "infoschemaV2.searchTableItemByID", "infoschemaV2.TableByID",
		"infoschemaV2.TableItemByID", "infoschemaV2.IterateAllTableItems", "infoschemaV2.TableIsCached", "IsSpecialDB",
		"infoschemaV2.EvictTable", "tableByNameHelper.onItem", "infoschemaV2.TableByName", "infoschemaV2.TableInfoByName",
		"infoschemaV2.TableInfoByID", "infoschemaV2.keepAlive", "infoschemaV2.SchemaTableInfos",
		"infoschemaV2.SchemaSimpleTableInfos", "infoschemaV2.FindTableInfoByPartitionID", "infoschemaV2.SchemaByName",
		"infoschemaV2.allSchemas", "infoschemaV2.AllSchemas", "infoschemaV2.AllSchemaNames", "infoschemaV2.SchemaExists",
		"infoschemaV2.searchPartitionItemByPartitionID", "infoschemaV2.TableItemByPartitionID",
		"infoschemaV2.TableIDByPartitionID", "infoschemaV2.FindTableByPartitionID", "infoschemaV2.TableExists",
		"infoschemaV2.SchemaByID", "infoschemaV2.GetTableReferredForeignKeys", "infoschemaV2.loadTableInfo", "IsV2",
		"applyTableUpdate", "applyCreateSchema", "applyDropSchema", "applyRecoverSchema", "Builder.applyRecoverSchemaV2",
		"applyModifySchemaCharsetAndCollate", "applyModifySchemaDefaultPlacement", "applyDropTable", "applyCreateTables",
		"updateInfoSchemaBundles", "oldSchemaInfo", "allocByID", "Builder.applyTableUpdateV2", "Builder.applyDropSchemaV2",
		"Builder.applyDropTableV2", "Builder.applyModifySchemaCharsetAndCollateV2",
		"Builder.applyModifySchemaDefaultPlacementV2", "bundleInfoBuilder.updateInfoSchemaBundlesV2",
		"bundleInfoBuilder.completeUpdateTablesV2", "infoschemaV2.ListTablesWithSpecialAttribute", "Data.CacheCapacity",
		"Data.SetCacheCapacity", "infoschemaV2.CloneAndUpdateTS", "WithRefillOption",
		// infoschema.go (infoSchemaMisc, shared helpers)
		"newInfoSchema", "infoSchema.PolicyByID", "infoSchemaMisc.HasTemporaryTable", "infoSchemaMisc.SchemaMetaVersion",
		"infoSchemaMisc.PolicyByName", "infoSchemaMisc.ResourceGroupByName", "infoSchemaMisc.ResourceGroupByID",
		"infoSchemaMisc.AllResourceGroups", "infoSchemaMisc.CloneResourceGroups", "infoSchemaMisc.AllPlacementPolicies",
		"infoSchemaMisc.ClonePlacementPolicies", "infoSchemaMisc.PlacementBundleByPhysicalTableID",
		"infoSchemaMisc.AllPlacementBundles", "infoSchemaMisc.setResourceGroup", "infoSchemaMisc.deleteResourceGroup",
		"infoSchemaMisc.setPolicy", "infoSchemaMisc.deletePolicy", "HasAutoIncrementColumn", "getTableInfo",
		// sieve.go, through Data.tableCache
		"newSieve", "Sieve.SetStatusHook", "Sieve.SetCapacity", "Sieve.SetCapacityAndWaitEvict", "Sieve.Capacity",
		"Sieve.Set", "Sieve.Get", "Sieve.Remove", "Sieve.removeEntry", "Sieve.evict",
		// context/infoschema.go
		"HasSpecialAttributes",
	}},
}

var infoschemaExempt = func() map[string]string {
	m := map[string]string{}
	// Infoschema v1: not ported (owner decision, tidb-rs specs/D06.3-infoschema/spec.md §1.2).
	v1 := "infoschema v1 is not ported (owner decision, D06.3 spec §1.2: only v2 caching matters, v1 is technical debt)"
	for _, f := range []string{
		"Builder.applyDropSchema", "Builder.applyDropTable", "Builder.applyModifySchemaCharsetAndCollate",
		"Builder.applyModifySchemaDefaultPlacement", "Builder.applyRecoverSchema", "Builder.applyTableUpdate",
		"Builder.copySortedTables", "Builder.copySortedTablesBucket", "Builder.deleteReferredForeignKeys",
		"Builder.getSchemaAndCopyIfNecessary", "Builder.sortAllTablesByID", "tableBucketIdx",
		"bundleInfoBuilder.updateInfoSchemaBundles", "bundleInfoBuilder.completeUpdateTables",
		"sortedTables.searchTable", "infoSchema.base", "infoSchema.SchemaByName", "infoSchema.schemaByName",
		"infoSchema.SchemaExists", "infoSchema.TableByName", "infoSchema.TableInfoByName", "infoSchema.TableExists",
		"infoSchema.SchemaByID", "infoSchema.TableByID", "infoSchema.TableItemByID", "infoSchema.TableInfoByID",
		"infoSchema.FindTableInfoByPartitionID", "infoSchema.SchemaTableInfos", "infoSchema.SchemaSimpleTableInfos",
		"infoSchema.ListTablesWithSpecialAttribute", "infoSchema.AllSchemas", "infoSchema.AllSchemaNames",
		"infoSchema.TableItemByPartitionID", "infoSchema.TableIDByPartitionID", "infoSchema.FindTableByPartitionID",
		"infoSchema.addSchema", "infoSchema.delSchema", "infoSchema.addReferredForeignKeys",
		"infoSchema.deleteReferredForeignKeys", "infoSchema.GetTableReferredForeignKeys",
		"MockInfoSchema", "MockInfoSchemaWithSchemaVer",
	} {
		m[f] = v1
	}
	// A3 (D06.3 spec §5): the Rust layout replaces Go's.
	m["entry.Size"] = "A3 (D06.3 spec §5.3): Rust entry sizes; records only compare eviction where sizes cannot matter (capacity 1)"
	m["Sizeof"] = "A3 (D06.3 spec §5.3): Go reflection walk of Go's memory layout, replaced by the Rust size estimate"
	m["sizeOf"] = "A3 (D06.3 spec §5.3): Go reflection walk of Go's memory layout, replaced by the Rust size estimate"
	for _, f := range []string{"Sieve.Contains", "Sieve.Peek", "Sieve.Len", "Sieve.Size", "Sieve.Purge", "Sieve.Close"} {
		m[f] = "not called by infoschema v2 (tests only); generic and unexported, so unreachable from govec: tidb-rs ports sieve_test.go"
	}
	for _, f := range []string{
		"emptySieveStatusHook.onHit", "emptySieveStatusHook.onMiss", "emptySieveStatusHook.onEvict",
		"emptySieveStatusHook.onUpdateSize", "emptySieveStatusHook.onUpdateLimit",
	} {
		m[f] = "no-op hook; NewData installs the metrics hook (pkg/infoschema/metrics.go, D06.6)"
	}
	m["Data.getVersionByTS"] = "dead in v8.5.8: Data.mu.versionTimestamps is never written, the result is always (0, false)"
	m["Data.getVersionByTSNoLock"] = "dead in v8.5.8: Data.mu.versionTimestamps is never written, the result is always (0, false)"
	m["DBInfoAsInfoSchema.AllSchemas"] = "test helper (DBInfoAsInfoSchema is used mainly in test); ported in tidb-rs crates/infoschema/src/context.rs"
	m["DBInfoAsInfoSchema.SchemaTableInfos"] = "test helper (DBInfoAsInfoSchema is used mainly in test); ported in tidb-rs crates/infoschema/src/context.rs"
	// Part 2 of D06.3 (tidb-rs specs/D06.3-infoschema/plan.md).
	p2 := "D06.3 part 2 (plan.md): the InfoSchema interface and session tables"
	for _, f := range []string{
		"NewSessionTables", "SessionTables.AddTable", "SessionTables.Count", "SessionTables.RemoveTable",
		"SessionTables.SchemaByID", "SessionTables.TableByID", "SessionTables.TableByName", "SessionTables.TableExists",
		"SessionTables.ensureSchema", "SessionTables.schemaTables",
		"SessionExtendedInfoSchema.DetachTemporaryTableInfoSchema", "SessionExtendedInfoSchema.FindTableInfoByPartitionID",
		"SessionExtendedInfoSchema.HasTemporaryTable", "SessionExtendedInfoSchema.SchemaByID",
		"SessionExtendedInfoSchema.TableByID", "SessionExtendedInfoSchema.TableByName",
		"SessionExtendedInfoSchema.TableInfoByID", "SessionExtendedInfoSchema.TableInfoByName",
		"SessionExtendedInfoSchema.UpdateTableInfo", "AllSchemaNames", "FindTableByTblOrPartID", "GetSequenceByName",
		"SchemaByTable", "TableIsSequence", "TableIsView",
	} {
		m[f] = p2
	}
	m["init"] = "D06.3 part 2 (plan.md): registers information_schema, whose tables are D06.6's"
	cache := "D06.3 part 2 (plan.md): InfoCache, with the domain's schema loading (D06.5) as its caller"
	for _, f := range []string{
		"NewCache", "InfoCache.GetAndResetRecentInfoSchemaTS", "InfoCache.ReSize", "InfoCache.Size", "InfoCache.Reset",
		"InfoCache.Upsert", "InfoCache.GetLatest", "InfoCache.Len", "InfoCache.GetEmptySchemaVersions",
		"InfoCache.getSchemaByTimestampNoLock", "InfoCache.GetByVersion", "InfoCache.getByVersionNoLock",
		"InfoCache.GetBySnapshotTS", "InfoCache.Insert", "InfoCache.InsertEmptySchemaVersion", "InfoCache.gcOldVersion",
	} {
		m[f] = cache
	}
	m["Builder.SetSchemaVersion"] = "ported; only the domain's schema filter calls it (domain.tryLoadSchemaDiffs, D06.5)"
	m["RegisterVirtualTable"] = "ported; the memory schemas register through it (D06.6), records are built without them"
	m["Data.addSpecialDB"] = "memory schemas only (registered by D06.6); records are built without them (drivers emptied)"
	m["getTableInfoList"] = "memory schemas only (SchemaTableInfos of a special schema, D06.6); records are built without them"
	return m
}()

// ---------------------------------------------------------------- store

type isMVCC struct {
	ts    uint64
	pairs [][2]string
}

// isStore is the kv.Storage of autoid.Requirement: GetSnapshot reads the newest state
// with ts <= version; Begin goes to a scratch mockstore. Other methods are nil.
type isStore struct {
	kv.Storage
	states []isMVCC
}

var isScratch kv.Storage

func (s *isStore) GetSnapshot(ver kv.Version) kv.Snapshot {
	var pairs [][2]string
	for _, st := range s.states {
		if st.ts <= ver.Ver {
			pairs = st.pairs
		}
	}
	return newMemSnap(pairs)
}

func (s *isStore) Begin(opts ...tikv.TxnOption) (kv.Transaction, error) {
	if isScratch == nil {
		st, err := mockstore.NewMockStore()
		if err != nil {
			panic(err)
		}
		isScratch = st
	}
	return isScratch.Begin(opts...)
}

type isReq struct{ s *isStore }

func (r isReq) Store() kv.Storage                    { return r.s }
func (r isReq) AutoIDClient() *autoid.ClientDiscover { return nil }

// ---------------------------------------------------------------- exec

type isStep struct {
	K     string            `json:"k"`
	TS    uint64            `json:"ts,omitempty"`
	Ver   int64             `json:"ver,omitempty"`
	N     uint64            `json:"n,omitempty"`
	At    int               `json:"at,omitempty"`
	Pairs [][2]string       `json:"pairs,omitempty"`
	Diffs []json.RawMessage `json:"diffs,omitempty"`
}

type isStepOut struct {
	IDs   [][]int64 `json:"ids,omitempty"`
	Err   *metaErr  `json:"err,omitempty"`
	Q     any       `json:"q,omitempty"`
	GC    []int64   `json:"gc,omitempty"`
	Cap   uint64    `json:"cap,omitempty"`
	Panic string    `json:"panic,omitempty"`
}

func infoschemaExec(op string, args []json.RawMessage) (any, error) {
	if op != "build" {
		return nil, fmt.Errorf("unknown op %s", op)
	}
	var steps []isStep
	mustUnmarshal(args[0], &steps)
	saved := isDrivers
	isDrivers = nil
	defer func() { isDrivers = saved }()
	store := &isStore{}
	r := isReq{store}
	data := infoschema.NewData()
	var built []infoschema.InfoSchema
	var latest infoschema.InfoSchema
	out := make([]isStepOut, 0, len(steps))
	for _, st := range steps {
		var o isStepOut
		func() {
			defer func() {
				if p := recover(); p != nil {
					o.Panic = fmt.Sprint(p)
				}
			}()
			switch st.K {
			case "full":
				store.states = append(store.states, isMVCC{st.TS, st.Pairs})
				m := meta.NewReader(newMemSnap(st.Pairs))
				dbs, policies, groups, err := isFetchAll(m)
				if err != nil {
					o.Err = metaErrOf(err)
					return
				}
				b := infoschema.NewBuilder(r, nil, data, true)
				if err := b.InitWithDBInfos(dbs, policies, groups, st.Ver); err != nil {
					o.Err = metaErrOf(err)
					return
				}
				latest = b.Build(st.TS)
				built = append(built, latest)
				o.Q = isQueries(latest)
			case "diffs":
				store.states = append(store.states, isMVCC{st.TS, st.Pairs})
				m := meta.NewReader(newMemSnap(st.Pairs))
				b := infoschema.NewBuilder(r, nil, data, true)
				if err := b.InitWithOldInfoSchema(latest); err != nil {
					o.Err = metaErrOf(err)
					return
				}
				b.WithStore(store).SetDeltaUpdateBundles()
				var maxVer int64
				for _, raw := range st.Diffs {
					diff := &model.SchemaDiff{}
					mustUnmarshal(raw, diff)
					maxVer = max(maxVer, diff.Version)
				}
				for _, raw := range st.Diffs {
					diff := &model.SchemaDiff{}
					mustUnmarshal(raw, diff)
					ids, err := b.ApplyDiff(m, diff)
					if err != nil {
						// The domain falls back to a full load (loadInfoSchema).
						o.Err = metaErrOf(err)
						dbs, policies, groups, err := isFetchAll(m)
						if err != nil {
							panic(err)
						}
						fb := infoschema.NewBuilder(r, nil, data, true)
						if err := fb.InitWithDBInfos(dbs, policies, groups, maxVer); err != nil {
							panic(err)
						}
						latest = fb.Build(st.TS)
						built = append(built, latest)
						o.Q = isQueries(latest)
						return
					}
					if ids == nil {
						ids = []int64{}
					}
					o.IDs = append(o.IDs, ids)
				}
				latest = b.Build(st.TS)
				built = append(built, latest)
				o.Q = isQueries(latest)
			case "gc":
				d, total := data.GCOldVersion(st.Ver)
				o.GC = []int64{int64(d), total}
			case "cap":
				data.SetCacheCapacity(st.N)
				o.Cap = data.CacheCapacity()
			case "query":
				o.Q = isQueries(built[st.At%len(built)])
			}
		}()
		out = append(out, o)
	}
	return out, nil
}

// isFetchAll is domain.fetchAllSchemasWithTables (v2, no schema filter, no repair mode),
// fetchPolicies and fetchResourceGroups.
func isFetchAll(m meta.Reader) ([]*model.DBInfo, []*model.PolicyInfo, []*model.ResourceGroupInfo, error) {
	dbs, err := m.ListDatabases()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, di := range dbs {
		name2ID, tables, err := m.GetAllNameToIDAndTheMustLoadedTableInfo(di.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		di.TableName2ID = name2ID
		for _, tb := range tables {
			infoschema.ConvertOldVersionUTF8ToUTF8MB4IfNeed(tb)
		}
		diTables := make([]*model.TableInfo, 0, len(tables))
		for _, tb := range tables {
			infoschema.ConvertCharsetCollateToLowerCaseIfNeed(tb)
			diTables = append(diTables, tb)
		}
		di.Deprecated.Tables = diTables
	}
	policies, err := m.ListPolicies()
	if err != nil {
		return nil, nil, nil, err
	}
	groups, err := m.ListResourceGroups()
	if err != nil {
		return nil, nil, nil, err
	}
	return dbs, policies, groups, nil
}

// ---------------------------------------------------------------- queries

// The names, IDs and partition IDs a query probes: every name and ID the scenario
// generator ever used (so dropped and future ones are probed too).
var isProbeNames = []string{"d1", "D1", "d2", "db_3", "Db_4", "mysql", "x"}
var isProbeTables = []string{"t", "T", "t1", "t2", "T3", "p", "v", "s", "tt", "x"}

const isMaxProbeID = 200

type isTblOut struct {
	ID   int64        `json:"id"`
	Name string       `json:"name"`
	DBID int64        `json:"dbid"`
	JSON vec.GoString `json:"json,omitempty"`
}

func isTbl(t *model.TableInfo, withJSON bool) *isTblOut {
	if t == nil {
		return nil
	}
	o := &isTblOut{ID: t.ID, Name: t.Name.O, DBID: t.DBID}
	if withJSON {
		b, err := json.Marshal(t)
		if err != nil {
			panic(err)
		}
		o.JSON = vec.GoString(b)
	}
	return o
}

type isDBOut struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Charset string `json:"cs"`
	Collate string `json:"co"`
	Policy  *int64 `json:"pol,omitempty"`
}

func isDB(d *model.DBInfo) *isDBOut {
	if d == nil {
		return nil
	}
	o := &isDBOut{ID: d.ID, Name: d.Name.O, Charset: d.Charset, Collate: d.Collate}
	if d.PlacementPolicyRef != nil {
		id := d.PlacementPolicyRef.ID
		o.Policy = &id
	}
	return o
}

func isFilters() []struct {
	name string
	f    infoschemacontext.SpecialAttributeFilter
} {
	return []struct {
		name string
		f    infoschemacontext.SpecialAttributeFilter
	}{
		{"all", infoschemacontext.AllSpecialAttribute}, {"ttl", infoschemacontext.TTLAttribute},
		{"tiflash", infoschemacontext.TiFlashAttribute}, {"placement", infoschemacontext.PlacementPolicyAttribute},
		{"allplacement", infoschemacontext.AllPlacementPolicyAttribute}, {"lock", infoschemacontext.TableLockAttribute},
		{"partition", infoschemacontext.PartitionAttribute}, {"affinity", infoschemacontext.AffinityAttribute},
	}
}

// isQueries runs the read API of one InfoSchema. Order matters (TableByName refills the
// table cache, TableIsCached reads it), and is the same in the Rust replay.
func isQueries(is infoschema.InfoSchema) any {
	ok, v2 := infoschema.IsV2(is)
	if !ok {
		panic("not v2")
	}
	ctx := context.Background()
	q := map[string]any{}
	q["ver"] = is.SchemaMetaVersion()
	var names []string
	for _, n := range is.AllSchemaNames() {
		if !infoschema.IsSpecialDB(n.L) { // memory schemas: D06.6
			names = append(names, n.O)
		}
	}
	q["names"] = names
	var all []*isDBOut
	for _, d := range is.AllSchemas() {
		if !infoschema.IsSpecialDB(d.Name.L) {
			all = append(all, isDB(d))
		}
	}
	q["all"] = all
	cached0 := []int64{}
	for id := int64(1); id <= isMaxProbeID; id++ {
		if v2.TableIsCached(id) {
			cached0 = append(cached0, id)
		}
	}
	q["cached0"] = cached0
	var schemas, simple, infos []any
	for _, n := range isProbeNames {
		cn := pmodel.NewCIStr(n)
		d, ok := is.SchemaByName(cn)
		schemas = append(schemas, []any{n, isDB(d), ok, is.SchemaExists(cn)})
		var s []any
		for _, t := range must2(is.SchemaSimpleTableInfos(ctx, cn)) {
			s = append(s, []any{t.ID, t.Name.O})
		}
		simple = append(simple, s)
		tis, err := is.SchemaTableInfos(ctx, cn)
		var ts []any
		for _, t := range tis {
			ts = append(ts, isTbl(t, false))
		}
		infos = append(infos, []any{ts, metaErrOf(err)})
	}
	q["schemas"], q["simple"], q["infos"] = schemas, simple, infos
	var byID, items, parts, bundles []any
	for id := int64(1); id <= isMaxProbeID; id++ {
		d, ok := is.SchemaByID(id)
		if ok {
			byID = append(byID, []any{id, isDB(d)})
		}
		if it, ok := is.TableItemByID(id); ok {
			items = append(items, []any{id, it.DBName.O, it.TableName.O})
		}
		if tid, ok := is.TableIDByPartitionID(id); ok {
			it, ok2 := is.TableItemByPartitionID(id)
			tbl, db, def := is.FindTableByPartitionID(id)
			var defID any
			if def != nil {
				defID = def.ID
			}
			var tblID, dbID any
			if tbl != nil {
				tblID = tbl.Meta().ID
			}
			if db != nil {
				dbID = db.ID
			}
			parts = append(parts, []any{id, tid, it.DBName.O, it.TableName.O, ok2, tblID, dbID, defID})
		}
		if _, ok := is.PlacementBundleByPhysicalTableID(id); ok {
			bundles = append(bundles, id)
		}
	}
	q["byid"], q["items"], q["parts"], q["bundles"] = byID, items, parts, bundles
	var tbls []any
	for id := int64(1); id <= isMaxProbeID; id++ {
		if t, ok := is.TableByID(ctx, id); ok {
			ti, _ := is.TableInfoByID(id)
			tbls = append(tbls, []any{id, isTbl(t.Meta(), false), ti.ID})
		}
	}
	q["tbyid"] = tbls
	var refill []any
	rctx := infoschema.WithRefillOption(ctx, true)
	for id := int64(1); id <= isMaxProbeID; id += 3 {
		if t, ok := is.TableByID(rctx, id); ok {
			ti, db, def := is.FindTableInfoByPartitionID(id)
			var dbID, defID any
			if db != nil {
				dbID = db.ID
			}
			if def != nil {
				defID = def.ID
			}
			var tiID any
			if ti != nil {
				tiID = ti.ID
			}
			refill = append(refill, []any{id, t.Meta().ID, tiID, dbID, defID})
		}
	}
	q["refill"] = refill
	var byName, fks []any
	for _, n := range isProbeNames {
		for _, tn := range isProbeTables {
			cn, ct := pmodel.NewCIStr(n), pmodel.NewCIStr(tn)
			t, err := is.TableByName(ctx, cn, ct)
			var to *isTblOut
			var autoInc []any
			if err == nil {
				to = isTbl(t.Meta(), true)
				has, col := infoschema.HasAutoIncrementColumn(t.Meta())
				autoInc = []any{has, col}
			}
			if err == nil || is.SchemaExists(cn) {
				ti, err2 := is.TableInfoByName(cn, ct)
				var tiID any
				if ti != nil {
					tiID = ti.ID
				}
				byName = append(byName, []any{n, tn, to, metaErrOf(err), is.TableExists(cn, ct), tiID, metaErrOf(err2), autoInc})
			}
			var fk []any
			for _, f := range is.GetTableReferredForeignKeys(cn.L, ct.L) {
				var cols []string
				for _, c := range f.Cols {
					cols = append(cols, c.O)
				}
				fk = append(fk, []any{cols, f.ChildSchema.O, f.ChildTable.O, f.ChildFKName.O})
			}
			if len(fk) > 0 {
				fks = append(fks, []any{n, tn, fk})
			}
		}
	}
	q["byname"], q["fks"] = byName, fks
	var special []any
	for _, f := range isFilters() {
		var res []any
		for _, r := range is.ListTablesWithSpecialAttribute(f.f) {
			var ids []int64
			for _, t := range r.TableInfos {
				ids = append(ids, t.ID)
			}
			res = append(res, []any{r.DBName.O, ids})
		}
		special = append(special, []any{f.name, res})
	}
	q["special"] = special
	var iter []any
	v2.IterateAllTableItems(func(t infoschema.TableItem) bool {
		iter = append(iter, []any{t.DBName.O, t.TableName.O})
		return true
	})
	q["iter"] = iter
	var pols []any
	for _, p := range is.AllPlacementPolicies() {
		pols = append(pols, []any{p.Name.O, p.ID})
	}
	sort.Slice(pols, func(i, j int) bool { return pols[i].([]any)[0].(string) < pols[j].([]any)[0].(string) })
	q["policies"] = pols
	var rgs []any
	for _, g := range is.AllResourceGroups() {
		rgs = append(rgs, []any{g.Name.O, g.ID})
	}
	sort.Slice(rgs, func(i, j int) bool { return rgs[i].([]any)[0].(string) < rgs[j].([]any)[0].(string) })
	q["groups"] = rgs
	var lookups []any
	for _, n := range []string{"p1", "P1", "p2", "default", "rg1", "RG2"} {
		p, ok1 := is.PolicyByName(pmodel.NewCIStr(n))
		g, ok2 := is.ResourceGroupByName(pmodel.NewCIStr(n))
		var pid, gid any
		if ok1 {
			pid = p.ID
		}
		if ok2 {
			gid = g.ID
		}
		lookups = append(lookups, []any{n, pid, gid})
	}
	q["lookups"] = lookups
	q["nbundles"] = len(is.AllPlacementBundles())
	q["temp"] = is.HasTemporaryTable()
	cached1 := []int64{}
	for id := int64(1); id <= isMaxProbeID; id++ {
		if v2.TableIsCached(id) {
			cached1 = append(cached1, id)
		}
	}
	q["cached1"] = cached1
	for _, n := range isProbeNames[:3] {
		for _, tn := range isProbeTables[:4] {
			v2.EvictTable(pmodel.NewCIStr(n), pmodel.NewCIStr(tn))
		}
	}
	cached2 := []int64{}
	for id := int64(1); id <= isMaxProbeID; id++ {
		if v2.TableIsCached(id) {
			cached2 = append(cached2, id)
		}
	}
	q["cached2"] = cached2
	c := v2.CloneAndUpdateTS(1)
	q["clone"] = c.SchemaMetaVersion()
	return q
}

// ---------------------------------------------------------------- generator

// isSim is the catalog a scenario builds, written with meta.Mutator into KV states.
type isSim struct {
	rng      *rand.Rand
	pairs    [][2]string
	ver      int64
	ts       uint64
	nextID   int64
	dbs      []*model.DBInfo
	tables   map[int64][]*model.TableInfo // db ID -> tables
	policies []*model.PolicyInfo
	groups   []*model.ResourceGroupInfo
	dropped  []*model.TableInfo // for recover
	droppedD []*model.DBInfo
}

func (s *isSim) id() int64 {
	s.nextID++
	if s.nextID > isMaxProbeID-10 {
		panic("isSim: out of probe IDs")
	}
	return s.nextID
}

// write runs f on a mutator over the current state and keeps the result.
func (s *isSim) write(f func(m *meta.Mutator)) {
	txn := metaTxn()
	for _, p := range s.pairs {
		must(txn.Set(unhex(p[0]), unhex(p[1])))
	}
	f(meta.NewMutator(txn))
	s.pairs = dumpTxn(txn)
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func (s *isSim) newTable(dbID int64, name string) *model.TableInfo {
	id := s.id()
	t := &model.TableInfo{
		ID: id, Name: pmodel.NewCIStr(name), Charset: "utf8mb4", Collate: "utf8mb4_bin",
		State: model.StatePublic, Version: model.CurrLatestTableInfoVersion,
	}
	ft := types.NewFieldType(mysql.TypeLonglong)
	t.Columns = []*model.ColumnInfo{{ID: 1, Name: pmodel.NewCIStr("c"), Offset: 0, FieldType: *ft, State: model.StatePublic}}
	switch s.rng.IntN(14) {
	case 0, 1:
		n := 1 + s.rng.IntN(3)
		t.Partition = &model.PartitionInfo{Type: pmodel.PartitionTypeHash, Expr: "`c`", Num: uint64(n), Enable: true}
		for i := 0; i < n; i++ {
			t.Partition.Definitions = append(t.Partition.Definitions, model.PartitionDefinition{ID: s.id(), Name: pmodel.NewCIStr(fmt.Sprintf("p%d", i))})
		}
		if len(s.policies) > 0 && s.rng.IntN(3) == 0 {
			p := metaPick(s.rng, s.policies)
			t.Partition.Definitions[0].PlacementPolicyRef = &model.PolicyRefInfo{ID: p.ID, Name: p.Name}
		}
	case 2:
		if refs := s.allTables(); len(refs) > 0 {
			ref := metaPick(s.rng, refs)
			db := s.dbOf(ref.ID)
			t.ForeignKeys = []*model.FKInfo{{ID: 1, Name: pmodel.NewCIStr(metaPick(s.rng, []string{"fk", "FK1"})), RefSchema: db.Name, RefTable: ref.Name,
				RefCols: []pmodel.CIStr{pmodel.NewCIStr("c")}, Cols: []pmodel.CIStr{pmodel.NewCIStr("c")}, State: model.StatePublic, Version: s.rng.IntN(2)}}
		}
	case 3:
		t.TiFlashReplica = &model.TiFlashReplicaInfo{Count: 1}
	case 4:
		t.TempTableType = model.TempTableGlobal
	case 5:
		if len(s.policies) > 0 {
			p := metaPick(s.rng, s.policies)
			t.PlacementPolicyRef = &model.PolicyRefInfo{ID: p.ID, Name: p.Name}
		}
	case 6:
		t.TTLInfo = &model.TTLInfo{ColumnName: pmodel.NewCIStr("c"), IntervalExprStr: "1", IntervalTimeUnit: 0, Enable: true, JobInterval: "1h"}
	case 7:
		t.Lock = &model.TableLockInfo{Tp: pmodel.TableLockRead, State: model.TableLockStatePublic}
	case 8:
		t.Version = uint16(s.rng.IntN(3))
		t.Charset, t.Collate = "UTF8", "UTF8_BIN"
		t.Columns[0].SetCharset(metaPick(s.rng, []string{"utf8", "UTF8", "latin1"}))
		t.Columns[0].SetCollate("UTF8_BIN")
		t.Columns[0].Version = uint64(s.rng.IntN(3))
	case 9:
		t.Affinity = &model.TableAffinityInfo{Level: "table"}
	}
	_ = dbID
	return t
}

func (s *isSim) allTables() []*model.TableInfo {
	var out []*model.TableInfo
	for _, d := range s.dbs {
		out = append(out, s.tables[d.ID]...)
	}
	return out
}

func (s *isSim) dbOf(tid int64) *model.DBInfo {
	for _, d := range s.dbs {
		for _, t := range s.tables[d.ID] {
			if t.ID == tid {
				return d
			}
		}
	}
	return nil
}

func (s *isSim) diff(tp model.ActionType, schemaID, tableID int64) *model.SchemaDiff {
	s.ver++
	return &model.SchemaDiff{Version: s.ver, Type: tp, SchemaID: schemaID, TableID: tableID}
}

func cloneTbl(t *model.TableInfo) *model.TableInfo {
	b, _ := json.Marshal(t)
	n := &model.TableInfo{}
	mustUnmarshal(b, n)
	return n
}

// op applies one random catalog change and returns its diff(s).
func (s *isSim) op() []*model.SchemaDiff {
	rng := s.rng
	tables := s.allTables()
	switch k := rng.IntN(30); {
	case k < 3 || len(s.dbs) == 0: // create schema
		name := metaPick(rng, isProbeNames[:5])
		for _, o := range s.dbs {
			if o.Name.L == pmodel.NewCIStr(name).L {
				return nil
			}
		}
		d := &model.DBInfo{ID: s.id(), Name: pmodel.NewCIStr(name), Charset: "utf8mb4", Collate: "utf8mb4_bin", State: model.StatePublic}
		s.write(func(m *meta.Mutator) { must(m.CreateDatabase(d)) })
		s.dbs = append(s.dbs, d)
		return []*model.SchemaDiff{s.diff(model.ActionCreateSchema, d.ID, 0)}
	case k < 4: // drop schema
		i := rng.IntN(len(s.dbs))
		d := s.dbs[i]
		s.write(func(m *meta.Mutator) { must(m.DropDatabase(d.ID)) })
		s.dbs = append(s.dbs[:i:i], s.dbs[i+1:]...)
		s.droppedD = append(s.droppedD, d)
		s.dropped = append(s.dropped, s.tables[d.ID]...)
		delete(s.tables, d.ID)
		return []*model.SchemaDiff{s.diff(model.ActionDropSchema, d.ID, 0)}
	case k < 10 || len(tables) == 0: // create table / view / sequence
		d := metaPick(rng, s.dbs)
		name := s.freeName(d.ID)
		if name == "" {
			return nil
		}
		t := s.newTable(d.ID, name)
		tp := model.ActionCreateTable
		switch rng.IntN(8) {
		case 0:
			t.View = &model.ViewInfo{SelectStmt: "select 1"}
			t.Partition, t.TempTableType = nil, model.TempTableNone
			tp = model.ActionCreateView
		case 1:
			t.Sequence = &model.SequenceInfo{Start: 1, Increment: 1, MinValue: 1, MaxValue: 100, Cache: true, CacheValue: 10}
			t.Partition, t.TempTableType = nil, model.TempTableNone
			tp = model.ActionCreateSequence
		}
		s.write(func(m *meta.Mutator) { must(m.CreateTableOrView(d.ID, t)) })
		s.tables[d.ID] = append(s.tables[d.ID], t)
		return []*model.SchemaDiff{s.diff(tp, d.ID, t.ID)}
	case k < 12: // drop table (or keep it in a non-public state)
		t := metaPick(rng, tables)
		d := s.dbOf(t.ID)
		if rng.IntN(3) == 0 {
			n := cloneTbl(t)
			n.State = model.StateDeleteOnly
			s.write(func(m *meta.Mutator) { must(m.UpdateTable(d.ID, n)) })
			s.replace(d.ID, t.ID, n)
			return []*model.SchemaDiff{s.diff(model.ActionDropTable, d.ID, t.ID)}
		}
		s.write(func(m *meta.Mutator) { must(m.DropTableOrView(d.ID, t.ID)) })
		s.remove(d.ID, t.ID)
		s.dropped = append(s.dropped, t)
		tp := model.ActionDropTable
		if t.IsView() {
			tp = model.ActionDropView
		} else if t.IsSequence() {
			tp = model.ActionDropSequence
		}
		return []*model.SchemaDiff{s.diff(tp, d.ID, t.ID)}
	case k < 14: // truncate table
		t := metaPick(rng, tables)
		d := s.dbOf(t.ID)
		n := cloneTbl(t)
		n.ID = s.id()
		s.write(func(m *meta.Mutator) {
			must(m.DropTableOrView(d.ID, t.ID))
			must(m.CreateTableOrView(d.ID, n))
		})
		s.remove(d.ID, t.ID)
		s.tables[d.ID] = append(s.tables[d.ID], n)
		df := s.diff(model.ActionTruncateTable, d.ID, n.ID)
		df.OldTableID = t.ID
		return []*model.SchemaDiff{df}
	case k < 16: // rename table, maybe to another schema
		t := metaPick(rng, tables)
		from := s.dbOf(t.ID)
		to := metaPick(rng, s.dbs)
		n := cloneTbl(t)
		// Within one schema the table's own name is free: a case-only rename (t -> T)
		// is generated on purpose (D06.3 spec §9 item 4: CIStr-wide dedup).
		except := int64(0)
		if from.ID == to.ID {
			except = t.ID
		}
		name := s.freeNameExcept(to.ID, except)
		if from.ID == to.ID && rng.IntN(3) == 0 {
			if sw := swapCase(t.Name.O); sw != t.Name.O {
				name = sw
			}
		}
		if name == "" {
			return nil
		}
		n.Name = pmodel.NewCIStr(name)
		s.write(func(m *meta.Mutator) {
			must(m.DropTableOrView(from.ID, t.ID))
			must(m.CreateTableOrView(to.ID, n))
		})
		s.remove(from.ID, t.ID)
		s.tables[to.ID] = append(s.tables[to.ID], n)
		tp := model.ActionRenameTable
		if rng.IntN(3) == 0 {
			tp = model.ActionRenameTables
		}
		df := s.diff(tp, to.ID, t.ID)
		df.OldSchemaID, df.OldTableID = from.ID, t.ID
		return []*model.SchemaDiff{df}
	case k < 19: // alter table (default action), attributes change
		t := metaPick(rng, tables)
		d := s.dbOf(t.ID)
		n := s.newTable(d.ID, t.Name.O)
		n.ID, n.Name, n.View, n.Sequence = t.ID, t.Name, t.View, t.Sequence
		if n.Partition != nil && t.Partition == nil && rng.IntN(2) == 0 {
			n.Partition = nil
		}
		if t.Partition != nil && rng.IntN(2) == 0 {
			n.Partition = t.Partition
		}
		s.write(func(m *meta.Mutator) { must(m.UpdateTable(d.ID, n)) })
		s.replace(d.ID, t.ID, n)
		tp := metaPick(rng, []model.ActionType{model.ActionAddColumn, model.ActionAddIndex, model.ActionModifyColumn,
			model.ActionRebaseAutoID, model.ActionRebaseAutoRandomBase, model.ActionModifyTableAutoIDCache,
			model.ActionAlterTablePlacement, model.ActionAddTablePartition, model.ActionMultiSchemaChange,
			model.ActionAlterTTLInfo, model.ActionSetTiFlashReplica, model.ActionLockTable, model.ActionType(200),
			model.ActionAlterSequence, model.ActionRepairTable, model.ActionAlterTablePartitionPlacement})
		df := s.diff(tp, d.ID, t.ID)
		if tp == model.ActionMultiSchemaChange {
			df.SubActionTypes = []model.ActionType{model.ActionAddColumn, metaPick(rng, []model.ActionType{model.ActionRebaseAutoID, model.ActionRebaseAutoRandomBase, model.ActionAddIndex})}
		}
		return []*model.SchemaDiff{df}
	case k < 20: // truncate / drop / reorganize partitions
		var pt *model.TableInfo
		for _, t := range tables {
			if t.Partition != nil {
				pt = t
			}
		}
		if pt == nil {
			return nil
		}
		d := s.dbOf(pt.ID)
		n := cloneTbl(pt)
		old := n.Partition.Definitions[0].ID
		n.Partition.Definitions[0].ID = s.id()
		s.write(func(m *meta.Mutator) { must(m.UpdateTable(d.ID, n)) })
		s.replace(d.ID, pt.ID, n)
		tp := metaPick(rng, []model.ActionType{model.ActionTruncateTablePartition, model.ActionDropTablePartition, model.ActionReorganizePartition})
		df := s.diff(tp, d.ID, pt.ID)
		df.AffectedOpts = []*model.AffectedOption{{SchemaID: d.ID, TableID: n.Partition.Definitions[0].ID, OldTableID: old, OldSchemaID: d.ID}}
		if tp == model.ActionReorganizePartition {
			df.OldTableID = pt.ID
		}
		return []*model.SchemaDiff{df}
	case k < 21: // exchange partition
		var pt, nt *model.TableInfo
		for _, t := range tables {
			if t.Partition != nil && pt == nil {
				pt = t
			} else if t.Partition == nil && t.View == nil && t.Sequence == nil && nt == nil {
				nt = t
			}
		}
		if pt == nil || nt == nil {
			return nil
		}
		pd, nd := s.dbOf(pt.ID), s.dbOf(nt.ID)
		np, nn := cloneTbl(pt), cloneTbl(nt)
		partID := np.Partition.Definitions[0].ID
		np.Partition.Definitions[0].ID, nn.ID = nt.ID, partID
		s.write(func(m *meta.Mutator) {
			must(m.DropTableOrView(nd.ID, nt.ID))
			must(m.CreateTableOrView(nd.ID, nn))
			must(m.UpdateTable(pd.ID, np))
		})
		s.remove(nd.ID, nt.ID)
		s.tables[nd.ID] = append(s.tables[nd.ID], nn)
		s.replace(pd.ID, pt.ID, np)
		if rng.IntN(3) == 0 { // not public yet: OldTableID == TableID
			df := s.diff(model.ActionExchangeTablePartition, nd.ID, partID)
			df.OldTableID, df.OldSchemaID = partID, nd.ID
			df.AffectedOpts = []*model.AffectedOption{{SchemaID: pd.ID, TableID: pt.ID, OldSchemaID: pd.ID, OldTableID: pt.ID}}
			return []*model.SchemaDiff{df}
		}
		df := s.diff(model.ActionExchangeTablePartition, pd.ID, partID)
		df.OldSchemaID, df.OldTableID = nd.ID, nt.ID
		df.AffectedOpts = []*model.AffectedOption{{SchemaID: pd.ID, TableID: pt.ID, OldSchemaID: pd.ID, OldTableID: pt.ID}}
		return []*model.SchemaDiff{df}
	case k < 22: // modify schema charset / placement
		d := metaPick(rng, s.dbs)
		n := *d
		tp := model.ActionModifySchemaCharsetAndCollate
		if len(s.policies) > 0 && rng.IntN(2) == 0 {
			p := metaPick(rng, s.policies)
			n.PlacementPolicyRef = &model.PolicyRefInfo{ID: p.ID, Name: p.Name}
			tp = model.ActionModifySchemaDefaultPlacement
		} else {
			n.Charset, n.Collate = metaPick(rng, []string{"latin1", "utf8mb4"}), metaPick(rng, []string{"latin1_bin", "utf8mb4_general_ci"})
		}
		s.write(func(m *meta.Mutator) { must(m.UpdateDatabase(&n)) })
		*d = n
		return []*model.SchemaDiff{s.diff(tp, d.ID, 0)}
	case k < 24: // placement policies
		if len(s.policies) == 0 || rng.IntN(2) == 0 {
			p := &model.PolicyInfo{ID: int64(1 + rng.IntN(4)), Name: pmodel.NewCIStr(metaPick(rng, []string{"p1", "P1", "p2"})), State: model.StatePublic,
				PlacementSettings: &model.PlacementSettings{PrimaryRegion: "r1", Regions: "r1,r2", Followers: uint64(rng.IntN(3))}}
			for _, o := range s.policies {
				if o.ID == p.ID || o.Name.L == p.Name.L {
					return nil
				}
			}
			s.write(func(m *meta.Mutator) { must(m.CreatePolicy(p)) })
			s.policies = append(s.policies, p)
			return []*model.SchemaDiff{s.diff(model.ActionCreatePlacementPolicy, p.ID, 0)}
		}
		i := rng.IntN(len(s.policies))
		p := s.policies[i]
		if rng.IntN(2) == 0 {
			n := *p
			n.PlacementSettings = &model.PlacementSettings{PrimaryRegion: "r2", Regions: "r2", Followers: 1}
			s.write(func(m *meta.Mutator) { must(m.UpdatePolicy(&n)) })
			s.policies[i] = &n
			return []*model.SchemaDiff{s.diff(model.ActionAlterPlacementPolicy, p.ID, 0)}
		}
		s.write(func(m *meta.Mutator) { must(m.DropPolicy(p.ID)) })
		s.policies = append(s.policies[:i:i], s.policies[i+1:]...)
		return []*model.SchemaDiff{s.diff(model.ActionDropPlacementPolicy, p.ID, 0)}
	case k < 25: // resource groups
		if len(s.groups) == 0 || rng.IntN(2) == 0 {
			g := &model.ResourceGroupInfo{ID: int64(2 + rng.IntN(4)), Name: pmodel.NewCIStr(metaPick(rng, []string{"rg1", "RG2", "rg3"})), State: model.StatePublic,
				ResourceGroupSettings: &model.ResourceGroupSettings{RURate: uint64(rng.IntN(1000)), Priority: 1, BurstLimit: -1}}
			for _, o := range s.groups {
				if o.ID == g.ID || o.Name.L == g.Name.L {
					return nil
				}
			}
			s.write(func(m *meta.Mutator) { must(m.AddResourceGroup(g)) })
			s.groups = append(s.groups, g)
			return []*model.SchemaDiff{s.diff(model.ActionCreateResourceGroup, g.ID, 0)}
		}
		i := rng.IntN(len(s.groups))
		g := s.groups[i]
		if rng.IntN(2) == 0 {
			n := *g
			n.ResourceGroupSettings = &model.ResourceGroupSettings{RURate: 7, Priority: 2, BurstLimit: 3}
			s.write(func(m *meta.Mutator) { must(m.UpdateResourceGroup(&n)) })
			s.groups[i] = &n
			return []*model.SchemaDiff{s.diff(model.ActionAlterResourceGroup, g.ID, 0)}
		}
		s.write(func(m *meta.Mutator) { must(m.DropResourceGroup(g.ID)) })
		s.groups = append(s.groups[:i:i], s.groups[i+1:]...)
		return []*model.SchemaDiff{s.diff(model.ActionDropResourceGroup, g.ID, 0)}
	case k < 26: // recover table / schema
		if len(s.droppedD) > 0 && rng.IntN(2) == 0 {
			d := s.droppedD[len(s.droppedD)-1]
			for _, o := range s.dbs {
				if o.Name.L == d.Name.L {
					return nil
				}
			}
			s.droppedD = s.droppedD[:len(s.droppedD)-1]
			s.write(func(m *meta.Mutator) { must(m.CreateDatabase(d)) })
			s.dbs = append(s.dbs, d)
			df := s.diff(model.ActionRecoverSchema, d.ID, 0)
			for _, t := range s.dropped {
				if rng.IntN(2) == 0 || s.nameTaken(d.ID, t.Name.L) {
					continue
				}
				s.write(func(m *meta.Mutator) { _ = m.CreateTableOrView(d.ID, t) })
				s.tables[d.ID] = append(s.tables[d.ID], t)
				df.AffectedOpts = append(df.AffectedOpts, &model.AffectedOption{SchemaID: d.ID, TableID: t.ID, OldSchemaID: d.ID, OldTableID: t.ID})
			}
			s.dropped = nil
			return []*model.SchemaDiff{df}
		}
		if len(s.dropped) == 0 {
			return nil
		}
		t := s.dropped[len(s.dropped)-1]
		d := metaPick(rng, s.dbs)
		if s.nameTaken(d.ID, t.Name.L) {
			return nil
		}
		s.dropped = s.dropped[:len(s.dropped)-1]
		s.write(func(m *meta.Mutator) { must(m.CreateTableOrView(d.ID, t)) })
		s.tables[d.ID] = append(s.tables[d.ID], t)
		df := s.diff(model.ActionRecoverTable, d.ID, t.ID)
		df.AffectedOpts = []*model.AffectedOption{{SchemaID: d.ID, TableID: t.ID}}
		return []*model.SchemaDiff{df}
	case k < 27: // create tables (batch)
		d := metaPick(rng, s.dbs)
		df := s.diff(model.ActionCreateTables, d.ID, 0)
		for i := 1 + rng.IntN(3); i > 0; i-- {
			name := s.freeName(d.ID)
			if name == "" {
				break
			}
			t := s.newTable(d.ID, name)
			s.write(func(m *meta.Mutator) { must(m.CreateTableOrView(d.ID, t)) })
			s.tables[d.ID] = append(s.tables[d.ID], t)
			df.AffectedOpts = append(df.AffectedOpts, &model.AffectedOption{SchemaID: d.ID, TableID: t.ID, OldSchemaID: d.ID})
		}
		return []*model.SchemaDiff{df}
	case k < 28: // refresh meta (BR), flashback
		if rng.IntN(3) == 0 {
			return []*model.SchemaDiff{s.diff(model.ActionFlashbackCluster, 0, 0)}
		}
		d := metaPick(rng, s.dbs)
		tid := int64(0)
		if ts := s.tables[d.ID]; len(ts) > 0 && rng.IntN(2) == 0 {
			tid = metaPick(rng, ts).ID
		} else if rng.IntN(3) == 0 {
			tid = isMaxProbeID - 1
		}
		return []*model.SchemaDiff{s.diff(model.ActionRefreshMeta, d.ID, tid)}
	default: // a stale diff: the schema or table is gone
		tp := metaPick(rng, []model.ActionType{model.ActionCreateTable, model.ActionCreateSchema, model.ActionAddColumn,
			model.ActionModifySchemaCharsetAndCollate, model.ActionCreatePlacementPolicy, model.ActionCreateResourceGroup,
			model.ActionAlterPlacementPolicy, model.ActionDropSchema, model.ActionDropPlacementPolicy, model.ActionDropResourceGroup})
		return []*model.SchemaDiff{s.diff(tp, isMaxProbeID-2, isMaxProbeID-3)}
	}
}

// Table names are unique in a schema, case-insensitively (as in TiDB). Duplicates would
// also make a full load depend on Go's map order (TableName2ID, M-MAPORDER).
func (s *isSim) nameTaken(dbID int64, l string) bool {
	return s.nameTakenExcept(dbID, l, 0)
}

// nameTakenExcept ignores the table except (a rename within its own schema).
func (s *isSim) nameTakenExcept(dbID int64, l string, except int64) bool {
	for _, t := range s.tables[dbID] {
		if t.Name.L == l && t.ID != except {
			return true
		}
	}
	return false
}

func (s *isSim) freeName(dbID int64) string {
	return s.freeNameExcept(dbID, 0)
}

func (s *isSim) freeNameExcept(dbID, except int64) string {
	for i := 0; i < 4; i++ {
		n := metaPick(s.rng, isProbeTables)
		if !s.nameTakenExcept(dbID, pmodel.NewCIStr(n).L, except) {
			return n
		}
	}
	return ""
}

// swapCase upper-cases a lower-case ASCII name and lower-cases any other.
func swapCase(n string) string {
	if l := strings.ToLower(n); l != n {
		return l
	}
	return strings.ToUpper(n)
}

func (s *isSim) remove(dbID, tid int64) {
	ts := s.tables[dbID]
	for i, t := range ts {
		if t.ID == tid {
			s.tables[dbID] = append(ts[:i:i], ts[i+1:]...)
			return
		}
	}
}

func (s *isSim) replace(dbID, tid int64, n *model.TableInfo) {
	for i, t := range s.tables[dbID] {
		if t.ID == tid {
			s.tables[dbID][i] = n
		}
	}
}

func isScenario(rng *rand.Rand) []isStep {
	s := &isSim{rng: rng, tables: map[int64][]*model.TableInfo{}, nextID: 0}
	var steps []isStep
	// initial catalog
	for i := 1 + rng.IntN(3); i > 0; i-- {
		s.op()
	}
	for i := rng.IntN(4); i > 0; i-- {
		s.op()
	}
	s.ts += 10
	steps = append(steps, isStep{K: "full", TS: s.ts, Ver: s.ver, Pairs: s.pairs})
	built := 1
	for n := 3 + rng.IntN(6); n > 0; n-- {
		switch k := rng.IntN(12); {
		case k < 8:
			var diffs []json.RawMessage
			for i := 1 + rng.IntN(3); i > 0; i-- {
				for _, d := range s.op() {
					b, err := json.Marshal(d)
					must(err)
					diffs = append(diffs, b)
				}
			}
			if len(diffs) == 0 {
				continue
			}
			s.ts += 10
			steps = append(steps, isStep{K: "diffs", TS: s.ts, Pairs: s.pairs, Diffs: diffs})
			built++
		case k < 9:
			s.ts += 10
			steps = append(steps, isStep{K: "full", TS: s.ts, Ver: s.ver, Pairs: s.pairs})
			built++
		case k < 10:
			steps = append(steps, isStep{K: "gc", Ver: s.ver - int64(rng.IntN(3))})
		case k < 11:
			steps = append(steps, isStep{K: "cap", N: metaPick(rng, []uint64{1, 1 << 30})})
		default:
			steps = append(steps, isStep{K: "query", At: rng.IntN(built)})
		}
	}
	return steps
}

func infoschemaGenerate(g *vec.Gen, rng *rand.Rand) {
	for i := 0; i < 220; i++ {
		g.Add(fmt.Sprintf("rand/%d", i), "build", isScenario(rng))
	}
}
