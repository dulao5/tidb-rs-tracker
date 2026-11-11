package domains

// Domain "sessionvars": block D05.2, SessionVars and the session hooks.
//   - fresh: the scalar fields of a fresh NewSessionVars(nil), in the order and with
//     the exclusions of rsgen -sessionvars (T2).
//   - set: SetSystemVar / SetSystemVarWithRelaxedValidation /
//     SetSystemVarWithoutValidation / SetSystemVarWithOldValAsRet on a fresh session
//     (the sysvar domain's freshVars: mock accessor, TimeZone UTC, atomics reset), then
//     GetSessionOrGlobalSystemVar of the same name; the output is the error, the
//     warnings, the scalar fields and systems[] entries that changed, and the read-back
//     value (T3, and the SetSession / GetSession hooks of T4).
//   - get: GetSessionOrGlobalSystemVar, GetGlobalSystemVar and
//     GetSessionStatesSystemVar on a fresh session (T3, GetSession / GetGlobal /
//     GetStateValue hooks).
//   - set_global: SET GLOBAL in the read-only phase (owner issue tidb-rs #58, option
//     (a)): Validate(ScopeGlobal); INSTANCE-scope variables then run SetGlobalFromHook as
//     session.SetGlobalSysVar does before returning without persisting; for the others
//     the record says "persist" (Go would write mysql.global_variables; tidb-rs returns
//     "not supported").
//   - init: SetSystemVar of a list of (name, value) in the given order on one session,
//     then every scalar field and systems[] entry (T11: the generator emits each list in
//     two orders, so a hook that depends on the order shows up as a mismatch in one).
// The Rust replay skips a record while a hook it exercises is still pending (tidb-rs
// session_hooks.rs PENDING_*), so records start to count as hooks land.
// Every op starts from resetSysvarAtomics (process-wide state the hooks write, listed
// there), so a record never sees the state an earlier record left (tidb_redact_log: #62).

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/planner/util/fixcontrol"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
)

const sessionvarsSeed = 5052

// sessionVarsExcluded mirrors cmd/rsgen/sessionvars.go (write-path scalar fields, X).
var sessionVarsExcluded = map[string]bool{
	"DDLReorgPriority": true, "DMLBatchSize": true, "RetryLimit": true, "DisableTxnAutoRetry": true,
	"ShardAllocateStep": true, "AllowAutoRandExplicitInsert": true, "AllowRemoveAutoInc": true,
	"BatchInsert": true, "BatchDelete": true, "BatchCommit": true, "BulkDMLEnabled": true,
}

// sessionvarsExternalTS is the generator's stub of the external timestamp the domain keeps
// in PD (variable.SetExternalTimestamp / GetExternalTimestamp are registered by the domain;
// unregistered, tidb_external_ts's hooks call a nil func). The Rust replay registers the
// same stub.
var sessionvarsExternalTS atomic.Uint64

func init() {
	variable.SetExternalTimestamp = func(_ context.Context, ts uint64) error {
		sessionvarsExternalTS.Store(ts)
		return nil
	}
	variable.GetExternalTimestamp = func(context.Context) (uint64, error) {
		return sessionvarsExternalTS.Load(), nil
	}
	// ValidateCloudStorageURI is registered by lightning / BR; the stub rejects two schemes,
	// one with a plain error and one with a *terror.Error (which the SetGlobal hook turns into
	// a plain error with the same text). The Rust replay registers the same stub.
	variable.ValidateCloudStorageURI = func(_ context.Context, uri string) error {
		switch {
		case strings.HasPrefix(uri, "err://"):
			return goerrors.New("unsupported storage: " + uri)
		case strings.HasPrefix(uri, "terr://"):
			return variable.ErrWrongValueForVar.GenWithStackByArgs(variable.TiDBCloudStorageURI, uri)
		}
		return nil
	}
	exempt := map[string]string{}
	for _, n := range sessionvarsPending {
		exempt[n] = "pending: D05.2 T3-T7 (tidb-rs specs/D05.2-sessionvars/plan.md)"
	}
	vec.Register(&vec.Domain{
		Name:   "sessionvars",
		Seed:   sessionvarsSeed,
		Blocks: []string{"D05.2"},
		Coverage: map[string]vec.Coverage{
			"fresh": {Min: 1, GoFuncs: []string{"NewSessionVars", "NewUserVars", "NewPlanCacheParamList"}},
			"set": {Min: 1000, GoFuncs: []string{"SessionVars.SetSystemVar", "SessionVars.SetSystemVarWithOldValAsRet",
				"SessionVars.SetSystemVarWithoutValidation", "SessionVars.SetSystemVarWithRelaxedValidation",
				"SysVar.SetSessionFromHook", "SessionVars.GetSystemVar"}},
			"get": {Min: 900, GoFuncs: []string{"SessionVars.GetSessionOrGlobalSystemVar", "SessionVars.GetGlobalSystemVar",
				"SessionVars.GetSessionStatesSystemVar", "SysVar.GetSessionFromHook", "SysVar.GetGlobalFromHook"}},
			"set_global": {Min: 100, GoFuncs: []string{"SysVar.SetGlobalFromHook"}},
			"set_global_hook": {Min: 100, GoFuncs: []string{"ParseGlobalSlowLogRules", "ParseSessionSlowLogRules",
				"ParseSlowLogFieldValue", "ParseString", "encodeRules", "parseBool", "parseFloat64", "parseInt64",
				"parseSlowLogRuleEntry", "parseSlowLogRuleSet", "parseUint64"}},
			"set_global_hook2": {Min: 8},
			"tidb_table":       {Min: 300, GoFuncs: []string{"getTiDBTableValue", "setTiDBTableValue"}},
			"init":             {Min: 20},
		},
		Exempt:   exempt,
		Fixes:    []vec.Fix{{ID: fix65884, Applied: fix65884Applied}, {ID: fix62892, Applied: fix62892Applied}},
		Gzip:     true,
		Generate: sessionvarsGenerate,
		Exec:     sessionvarsExecClient,
	})
}

// scalarDump renders the scalar fields of sv like the Rust replay does: bools and
// integers in decimal, floats as their IEEE-754 bits, strings as is.
func scalarDump(sv *variable.SessionVars) map[string]string {
	out := map[string]string{}
	var walk func(v reflect.Value, t reflect.Type)
	walk = func(v reflect.Value, t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			sf, fv := t.Field(i), v.Field(i)
			if sf.Anonymous && sf.Type.Kind() == reflect.Struct && sf.Type.PkgPath() == t.PkgPath() {
				walk(fv, sf.Type)
				continue
			}
			if sessionVarsExcluded[sf.Name] {
				continue
			}
			switch sf.Type.Kind() {
			case reflect.Bool:
				out[sf.Name] = strconv.FormatBool(fv.Bool())
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				out[sf.Name] = strconv.FormatInt(fv.Int(), 10)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				out[sf.Name] = strconv.FormatUint(fv.Uint(), 10)
			case reflect.Float64:
				out[sf.Name] = strconv.FormatUint(math.Float64bits(fv.Float()), 10)
			case reflect.Float32:
				out[sf.Name] = strconv.FormatUint(uint64(math.Float32bits(float32(fv.Float()))), 10)
			case reflect.String:
				out[sf.Name] = fv.String()
			}
		}
	}
	walk(reflect.ValueOf(sv).Elem(), reflect.TypeOf(sv).Elem())
	return out
}

// systemsDump reads the unexported systems map of sv.
func systemsDump(sv *variable.SessionVars) map[string]string {
	out := map[string]string{}
	m := reflect.ValueOf(sv).Elem().FieldByName("systems")
	it := m.MapRange()
	for it.Next() {
		out[it.Key().String()] = it.Value().String()
	}
	return out
}

// nonScalarDump renders the non-scalar SessionVars fields that SetSession hooks write
// (D05.2 T6; D05.2 B3): key sets as sorted numbers / names, maps as sorted "k:v".
func nonScalarDump(sv *variable.SessionVars) map[string]string {
	keys := func(m map[kv.StoreType]struct{}) string {
		var out []int
		for k := range m {
			out = append(out, int(k))
		}
		slices.Sort(out)
		return fmt.Sprint(out)
	}
	var fix []string
	for k, v := range sv.OptimizerFixControl {
		fix = append(fix, fmt.Sprintf("%d:%s", k, v))
	}
	slices.Sort(fix)
	var skip []string
	for k := range sv.AnalyzeSkipColumnTypes {
		skip = append(skip, k)
	}
	slices.Sort(skip)
	tz := "<nil>"
	if sv.TimeZone != nil {
		tz = sv.TimeZone.String()
	}
	return map[string]string{
		"kv_vars.backoff_lock_fast": strconv.Itoa(sv.KVVars.BackoffLockFast),
		"kv_vars.back_off_weight":   strconv.Itoa(sv.KVVars.BackOffWeight),
		"isolation_read_engines":    keys(sv.IsolationReadEngines),
		"allow_fallback_to_tikv":    keys(sv.AllowFallbackToTiKV),
		"partition_prune_mode":      sv.PartitionPruneMode.Load(),
		"txn_scope":                 sv.TxnScope.GetVarValue() + "/" + sv.TxnScope.GetTxnScope(),
		"stats_load_sync_wait":      strconv.FormatInt(sv.StatsLoadSyncWait.Load(), 10),
		"optimizer_fix_control":     strings.Join(fix, ","),
		"analyze_skip_column_types": strings.Join(skip, ","),
		"time_zone":                 tz,
	}
}

func diff(before, after map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range after {
		if b, ok := before[k]; !ok || b != v {
			out[k] = v
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out[k] = "<deleted>"
		}
	}
	return out
}

func errStr(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

// sessionvarsVolatile are the variables whose read-back depends on the wall clock
// (timestamp: time.Now when unset), on the session's random generator (rand_seed1/2
// state values) or on the host (version_compile_os / version_compile_machine: the
// build target; tidb_config: the config with host paths): their read-back values are
// recorded as "<volatile>".
// sessionvarsGlobalHookVars are the GLOBAL-scope variables whose SetGlobal hook writes
// only process-wide state that resetSysvarAtomics restores; set_global_hook records run
// their SetGlobal (then GetGlobal) hook. Extend the list as more hooks are ported and
// their written state is added to the restore list (D05.2 PR B).
var sessionvarsGlobalHookVars = []string{
	"authentication_ldap_sasl_auth_method_name",
	"authentication_ldap_sasl_bind_base_dn",
	"authentication_ldap_sasl_bind_root_dn",
	"authentication_ldap_sasl_bind_root_pwd",
	"authentication_ldap_sasl_ca_path",
	"authentication_ldap_sasl_init_pool_size",
	"authentication_ldap_sasl_max_pool_size",
	"authentication_ldap_sasl_server_host",
	"authentication_ldap_sasl_server_port",
	"authentication_ldap_sasl_tls",
	"authentication_ldap_sasl_user_search_attr",
	"authentication_ldap_simple_auth_method_name",
	"authentication_ldap_simple_bind_base_dn",
	"authentication_ldap_simple_bind_root_dn",
	"authentication_ldap_simple_bind_root_pwd",
	"authentication_ldap_simple_ca_path",
	"authentication_ldap_simple_init_pool_size",
	"authentication_ldap_simple_max_pool_size",
	"authentication_ldap_simple_server_host",
	"authentication_ldap_simple_server_port",
	"authentication_ldap_simple_tls",
	"authentication_ldap_simple_user_search_attr",
	"max_user_connections",
	"password_history",
	"password_reuse_interval",
	"performance_schema_session_connect_attrs_size",
	"tidb_accelerate_user_creation_update",
	"tidb_advancer_check_point_lag_limit",
	"tidb_ddl_enable_fast_reorg",
	"tidb_enable_auto_analyze",
	"tidb_enable_auto_analyze_priority_queue",
	"tidb_enable_batch_dml",
	"tidb_enable_check_constraint",
	"tidb_enable_column_tracking",
	"tidb_enable_foreign_key",
	"tidb_enable_gc_aware_memory_track",
	"tidb_enable_historical_stats_for_capture",
	"tidb_enable_instance_plan_cache",
	"tidb_enable_noop_variables",
	"tidb_enable_row_level_checksum",
	"tidb_enable_tiflash_pipeline_model",
	"tidb_enable_tmp_storage_on_oom",
	"tidb_gc_max_wait_time",
	"tidb_historical_stats_duration",
	"tidb_ignore_inlist_plan_digest",
	"tidb_mem_oom_action",
	"tidb_mem_quota_binding_cache",
	"tidb_memory_usage_alarm_keep_record_num",
	"tidb_memory_usage_alarm_ratio",
	"tidb_query_log_max_len",
	"tidb_schema_version_cache_limit",
	"tidb_server_memory_limit_sess_min_size",
	"tidb_stats_load_pseudo_timeout",
	"tidb_stats_load_sync_wait",
	"tidb_super_read_only",
	"tidb_ttl_job_enable",
	"validate_password.length",
	"validate_password.mixed_case_count",
	"validate_password.number_count",
	"validate_password.special_char_count",
	// D05.2 B3: B2 SetGlobal hooks whose written state processGlobalsRestore restores.
	"max_prepared_stmt_count",
	"require_secure_transport",
	"tidb_analyze_column_options",
	"tidb_auto_analyze_concurrency",
	"tidb_auto_analyze_partition_batch_size",
	"tidb_committer_concurrency",
	"tidb_ddl_disk_quota",
	"tidb_ddl_error_count_limit",
	"tidb_ddl_flashback_concurrency",
	"tidb_ddl_reorg_batch_size",
	"tidb_ddl_reorg_max_write_speed",
	"tidb_ddl_reorg_worker_cnt",
	"tidb_enable_dist_task",
	"tidb_enable_fast_create_table",
	"tidb_enable_paging",
	"tidb_generate_binary_plan",
	"tidb_instance_plan_cache_max_size",
	"tidb_instance_plan_cache_reserved_percentage",
	"tidb_load_binding_timeout",
	"tidb_max_auto_analyze_time",
	"tidb_max_delta_schema_count",
	"tidb_opt_fix_control",
	"tidb_partition_prune_mode",
	"tidb_persist_analyze_options",
	"tidb_prepared_plan_cache_memory_guard_ratio",
	"tidb_row_format_version",
	"tidb_store_limit",
	"tidb_table_cache_lease",
	"tidb_top_sql_max_meta_count",
	"tidb_top_sql_max_time_series_count",
	"tidb_ttl_delete_batch_size",
	"tidb_ttl_delete_rate_limit",
	"tidb_ttl_delete_worker_count",
	"tidb_ttl_running_tasks",
	"tidb_ttl_scan_batch_size",
	"tidb_ttl_scan_worker_count",
	"tidb_txn_commit_batch_size",
	"tidb_txn_entry_size_limit",
}

var sessionvarsVolatile = map[string]bool{"timestamp": true, "rand_seed1": true, "rand_seed2": true,
	"version_compile_os": true, "version_compile_machine": true, "tidb_config": true,
	// GetGlobal converts int(rate.Inf) (the default limiter): platform-dependent (M-F2I,
	// tidb-rs #27).
	"tidb_slow_log_max_per_sec": true}

func sessionvarsGenerate(g *vec.Gen, rng *rand.Rand) {
	// tidb_redact_log (fix pingcap/tidb#62892: GLOBAL only, the process-wide flag is stored by
	// SetGlobal). Deferred before the T10 group, so these run last of all and every earlier
	// record keeps its position.
	defer func() {
		for _, v := range []string{"ON", "MARKER", "OFF", "1", "marker"} {
			g.Add("fix "+fix62892, "set", "set", variable.TiDBRedactLog, v)
			g.Add("fix "+fix62892, "set_global_hook", variable.TiDBRedactLog, v)
		}
	}()
	// D05.2 PR C (T10): the tidb_gc_* hooks against mysql.tidb (the generator's in-memory
	// table, tidbTableAccessor), from several table contents. Deferred first, so appended
	// after every other record (the deferred groups below included).
	defer func() {
		for _, c := range sessionvarsGCVars {
			sv := variable.GetSysVar(c.name)
			vals := append([]string{""}, sysvarValues(sv, rng)...)
			vals = append(vals, c.extra...)
			for _, seed := range sessionvarsGCSeeds(c.keys) {
				for _, v := range vals {
					g.Add("sys/tidb_table", "tidb_table", c.name, v, seed)
				}
			}
		}
	}()
	g.Add("sys/fresh", "fresh")
	all := sysvarNames()
	kinds := []string{"set", "relaxed", "noval", "old"}
	for _, sv := range all {
		vals := sysvarValues(sv, rng)
		for i, v := range vals {
			g.Add("sys/set", "set", kinds[i%len(kinds)], sv.Name, v)
			if i%len(kinds) != 0 {
				g.Add("sys/set", "set", "set", sv.Name, v)
			}
		}
		g.Add("sys/get", "get", sv.Name)
	}
	g.Add("sys/get", "get", "no_such_variable")
	g.Add("sys/set", "set", "set", "no_such_variable", "1")
	// Random lists of settings applied in two orders (T11).
	for i := 0; i < 40; i++ {
		var pairs [][2]string
		for j := 0; j < 8; j++ {
			sv := all[rng.IntN(len(all))]
			if !sv.HasSessionScope() {
				continue
			}
			vals := sysvarValues(sv, rng)
			pairs = append(pairs, [2]string{sv.Name, vals[rng.IntN(len(vals))]})
		}
		if len(pairs) == 0 {
			// None of the 8 drawn variables has SESSION scope: nothing to apply (Go would
			// record "args":[null]).
			continue
		}
		g.Add("sys/init", "init", pairs)
		rev := slices.Clone(pairs)
		slices.Reverse(rev)
		g.Add("sys/init", "init", rev)
	}
	// SetGlobal / GetGlobal hooks of GLOBAL-scope variables (their written state is reset).
	// D05.2 B3: literals mined from the Go tests (testdata/sysvar_literals.tsv, cmd/minelits)
	// against the variables a test names, through SetSystemVar (SESSION scope) or the
	// SetGlobal hook (GLOBAL-only variables whose state is restored).
	defer func() {
		globalHook := map[string]bool{}
		for _, n := range sessionvarsGlobalHookVars {
			globalHook[n] = true
		}
		for _, c := range sysvarMinedPlan(all) {
			sv := variable.GetSysVar(c.name)
			switch {
			case sv.HasSessionScope():
				g.Add("sys/mined:"+c.fn, "set", "old", c.name, c.val)
			case globalHook[c.name]:
				g.Add("sys/mined:"+c.fn, "set_global_hook", c.name, c.val)
			}
		}
	}()
	// D05.2 B3: two-step records (Lucky's review of B1).
	defer func() {
		for _, p := range []string{"sasl", "simple"} {
			g.Add("sys/two_step", "set_global_hook2", "authentication_ldap_"+p+"_init_pool_size", []string{"100", "5"})
			g.Add("sys/two_step", "set_global_hook2", "authentication_ldap_"+p+"_max_pool_size", []string{"3", "500"})
			g.Add("sys/two_step", "set_global_hook2", "authentication_ldap_"+p+"_ca_path", []string{"/nonexistent/ca.pem", "/nonexistent/ca.pem"})
			g.Add("sys/two_step", "set_global_hook2", "authentication_ldap_"+p+"_ca_path", []string{"/nonexistent/ca.pem", ""})
		}
	}()
	// D05.2 B3: NaN / Inf against Go's `v < lo || v > hi` range checks in SetGlobal hooks.
	defer func() {
		for _, v := range []string{"NaN", "+Inf", "-Inf", "1e309", "-0"} {
			g.Add("sys/nan", "set_global_hook", "tidb_instance_plan_cache_reserved_percentage", v)
		}
	}()
	for _, name := range sessionvarsGlobalHookVars {
		sv := variable.GetSysVar(name)
		for _, v := range sysvarValues(sv, rng) {
			g.Add("sys/set_global_hook", "set_global_hook", name, v)
		}
	}
	// SET GLOBAL last: an INSTANCE hook may change process-wide state.
	for _, sv := range all {
		vals := sysvarValues(sv, rng)
		for i, v := range vals {
			if i%2 == 0 {
				g.Add("sys/set_global", "set_global", sv.Name, v)
			}
		}
	}
	// tidb_opt_fix_control values with nothing after a colon (fix pingcap/tidb#65884: v8.5.8
	// indexes s[0] and panics; with the fix the value is "", no error, no warning). Appended
	// after the other records so that those are unchanged.
	for _, v := range []string{"1:", "1: ", "1:,2:x", "123:", "44: ,45:on", "1:''"} {
		g.Add("fix "+fix65884, "set", "set", variable.TiDBOptFixControl, v)
		g.Add("fix "+fix65884, "set", "old", variable.TiDBOptFixControl, v)
	}
	// D05.2 B2: tidb_cloud_storage_uri (ast.RedactURL in GetGlobal; ValidateCloudStorageURI in
	// SetGlobal) and tidb_slow_log_rules (T7: the rule parser). Appended last.
	for _, v := range sessionvarsCloudURIs {
		g.Add("sys/cloud_storage_uri", "set_global_hook", variable.TiDBCloudStorageURI, v)
	}
	for _, v := range sessionvarsSlowLogRules {
		g.Add("sys/slow_log_rules", "set", "set", variable.TiDBSlowLogRules, v)
		g.Add("sys/slow_log_rules", "set", "old", variable.TiDBSlowLogRules, v)
		g.Add("sys/slow_log_rules", "set_global_hook", variable.TiDBSlowLogRules, v)
	}
}

// sessionvarsGCVars are the variables whose hooks read and write mysql.tidb, with the
// mysql.tidb keys they use and values beyond sysvarValues.
var sessionvarsGCVars = []struct {
	name  string
	keys  []string
	extra []string
}{
	{variable.TiDBGCEnable, []string{"tikv_gc_enable"}, []string{"true", "FALSE", "on", "Off", "1", "0"}},
	{variable.TiDBGCRunInterval, []string{"tikv_gc_run_interval"}, []string{"10m", "1h30m", "24h", "8760h", "5m"}},
	{variable.TiDBGCLifetime, []string{"tikv_gc_life_time"}, []string{"10m", "72h", "1h0m0s", "9m59s"}},
	{variable.TiDBGCConcurrency, []string{"tikv_gc_auto_concurrency", "tikv_gc_concurrency"}, []string{"-1", "1", "2", "256", "0", "AUTO"}},
	{variable.TiDBGCScanLockMode, []string{"tikv_gc_scan_lock_mode"}, []string{"physical", "LEGACY", "Physical", "x"}},
}

// sessionvarsGCSeeds are the mysql.tidb contents a tidb_gc_* record starts from: empty
// (the default), "true" / "false" in several cases (trueFalseToOnOff), a duration, an
// arbitrary string; for tidb_gc_concurrency the combinations of the auto flag and the
// concurrency.
func sessionvarsGCSeeds(keys []string) []map[string]string {
	if len(keys) == 2 {
		return []map[string]string{
			{},
			{keys[0]: "true"},
			{keys[0]: "false"},
			{keys[0]: "false", keys[1]: "5"},
			{keys[0]: "TRUE", keys[1]: "5"},
			{keys[0]: "ON", keys[1]: "7"},
			{keys[0]: "off", keys[1]: "x"},
			{keys[1]: "3"},
		}
	}
	k := keys[0]
	return []map[string]string{{}, {k: "true"}, {k: "False"}, {k: "TrUe"}, {k: "20m0s"}, {k: "PHYSICAL"}, {k: ""}, {k: "garbage"}}
}

var sessionvarsCloudURIs = []string{
	"", "s3://bucket/prefix?access-key=AK&secret-access-key=SK&region=us-west-2",
	"s3://b/p?access_key=AK&Session_Token=T&endpoint=http%3A%2F%2Fminio%3A9000",
	"S3://b?x=1&ACCESS-KEY=k", "ks3://b?access-key=a&access-key=b",
	"azure://c/p?sas-token=abc&foo=b%20c", "azblob://c?SAS_TOKEN=t&account-name=acct",
	"gcs://b/p?credentials-file=/x/y.json", "local:///tmp/x", "file:///tmp/a%20b",
	"s3://user:pass@host:80/p?a=1#frag", "s3://h:abc/p", "s3://[::1]:9000/b?access-key=x",
	"s3://[1.2.3.4]/b", "s3://[fe80::1%25en0]/b?access-key=x", "not a url %zz", "s3://b/p?a=%zz&access-key=k",
	"s3://b/p?;access-key=k&b=2", "s3://b/p?k=v+w&access-key=a+b", "s3://bücket/ä?access-key=ü",
	"s3:opaque?access-key=k", "s3://b/p?", "s3://b/a%2Fb?access-key=k", "//host/p?access-key=k",
	"err://bucket", "terr://bucket",
}

var sessionvarsSlowLogRules = []string{
	"", "query_time:1", "Query_time: 1.5, Mem_max:100", "conn_id:5,query_time:2;query_time:3",
	"conn_id:5", "db:'test', succ:true", `is_internal:t,resource_group:"rg"`, "unknown:1",
	"query_time:-1", "query_time:nan", "query_time:1e400", "query_time:-0", "mem_max:-5", "total_keys:abc",
	"a:1;b:1;c:1;d:1;e:1;f:1;g:1;h:1;i:1;j:1;k:1", "  ;  ;query_time:1", "no colon here",
	"query_time:  ,mem_max:1", "query_time:1,query_time:2", "conn_id:1,db:x;conn_id:2,db:y;db:z",
	"digest:abc def", "succ:yes", "write_sql_response_total:0.000001", "query_time:1000000",
	"conn_id:18446744073709551615,db:x", "conn_id:18446744073709551616", "  query_time : 0x10",
	"process_keys:1, total_keys:2, write_keys:3, write_size:4, prewrite_region:5, exec_retry_count:6",
	"num_cop_tasks:1,disk_max:2,kv_total:0.5,pd_total:1e-3,backoff_time:2,commit_time:3",
	"session_alias:abc,digest:'x'", ";;;", "query_time:1;", "query_time:1,,mem_max:2",
}

// fix65884: fixcontrol.ParseToMap indexes s[0] after a value's colon without a length
// check, so "123:" panics; master guards it (pingcap/tidb#65884, commit 811a10e115). tidb-rs
// ports the fix; govec links the patched file (govec/fixes).
const fix65884 = "pingcap/tidb#65884"

func fix65884Applied() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	m, warns, err := fixcontrol.ParseToMap("123:")
	return err == nil && len(warns) == 0 && m[123] == ""
}

// sessionvarsExecClient records the error the client sees: the server writes
// errors.Cause(err) (server/conn.go writeError), so a Wrap/Wrapf/Annotate prefix (e.g. the
// LDAP ca_path hook's "read ca certificate at …") only reaches Go's log. tidb-rs ports the
// wrappers away (M-ERR: Cause is the identity), returning the client-visible error.
func sessionvarsExecClient(op string, args []json.RawMessage) (any, error) {
	out, err := sessionvarsExec(op, args)
	if err != nil {
		err = errors.Cause(err)
	}
	canonSlowLogRulesOut(op, args, out)
	return out, err
}

// canonSlowLogRulesOut canonicalises the tidb_slow_log_rules values a record reads back:
// Go builds a rule's conditions and joins the global per-connection rule sets by ranging
// over maps, so RawRules changes between runs (tidb-rs slow_log_rules.rs, M-MAPORDER).
// The Rust replay applies the same canonicalisation to its output.
func canonSlowLogRulesOut(op string, args []json.RawMessage, out any) {
	var name string
	switch op {
	case "set":
		name = arg[string](args, 1)
	case "get", "set_global", "set_global_hook":
		name = arg[string](args, 0)
	default:
		return
	}
	if name != variable.TiDBSlowLogRules {
		return
	}
	w, ok := out.(vec.WithWarnings)
	if !ok {
		return
	}
	res, ok := w.Out.(map[string]any)
	if !ok {
		return
	}
	for _, k := range []string{"get", "sess", "glob", "state", "old", "after"} {
		if v, ok := res[k].(string); ok {
			res[k] = canonSlowLogRules(v)
		}
	}
}

// canonSlowLogRules sorts the conditions of every rule, then the rules.
func canonSlowLogRules(s string) string {
	if s == "" {
		return s
	}
	rules := strings.Split(s, ";")
	for i, r := range rules {
		conds := strings.Split(r, ",")
		slices.Sort(conds)
		rules[i] = strings.Join(conds, ",")
	}
	slices.Sort(rules)
	return strings.Join(rules, ";")
}

func sessionvarsExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "fresh":
		return scalarDump(variable.NewSessionVars(nil)), nil
	case "set":
		resetSysvarAtomics()
		vars := freshVars(svCtx{})
		vars.StmtCtx.StmtType = "Set"
		kind, name, val := arg[string](args, 0), arg[string](args, 1), arg[string](args, 2)
		f0, s0, n0 := scalarDump(vars), systemsDump(vars), nonScalarDump(vars)
		var err error
		res := map[string]any{}
		switch kind {
		case "set":
			err = vars.SetSystemVar(name, val)
		case "relaxed":
			err = vars.SetSystemVarWithRelaxedValidation(name, val)
		case "noval":
			err = vars.SetSystemVarWithoutValidation(name, val)
		case "old":
			var old string
			old, err = vars.SetSystemVarWithOldValAsRet(name, val)
			res["old"] = old
			if sessionvarsVolatile[name] {
				res["old"] = "<volatile>"
			}
		}
		res["fields"] = diff(f0, scalarDump(vars))
		res["sys"] = diff(s0, systemsDump(vars))
		res["ns"] = diff(n0, nonScalarDump(vars))
		if err == nil {
			v, gerr := vars.GetSessionOrGlobalSystemVar(context.Background(), name)
			if sessionvarsVolatile[name] {
				v = "<volatile>"
			}
			res["get"] = v
			res["get_err"] = errStr(gerr)
		}
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, err
	case "get":
		resetSysvarAtomics()
		vars := freshVars(svCtx{})
		name := arg[string](args, 0)
		res := map[string]any{}
		v, err := vars.GetSessionOrGlobalSystemVar(context.Background(), name)
		res["sess"], res["sess_err"] = v, errStr(err)
		v, err = vars.GetGlobalSystemVar(context.Background(), name)
		res["glob"], res["glob_err"] = v, errStr(err)
		v, keep, err := vars.GetSessionStatesSystemVar(name)
		res["state"], res["keep"], res["state_err"] = v, keep, errStr(err)
		if sessionvarsVolatile[name] {
			res["sess"], res["glob"], res["state"] = "<volatile>", "<volatile>", "<volatile>"
		}
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, nil
	case "set_global":
		resetSysvarAtomics()
		vars := freshVars(svCtx{})
		vars.StmtCtx.StmtType = "Set"
		name, val := arg[string](args, 0), arg[string](args, 1)
		sv := variable.GetSysVar(name)
		f0, s0 := scalarDump(vars), systemsDump(vars)
		res := map[string]any{}
		v, err := sv.Validate(vars, val, variable.ScopeGlobal)
		if err == nil {
			if sv.HasInstanceScope() {
				// The hook changes process-wide state (the instance's config): put the
				// previous value back afterwards so that later records, and a second
				// generation, start from the same state.
				prev, perr := sv.GetGlobalFromHook(context.Background(), vars)
				err = sv.SetGlobalFromHook(context.Background(), vars, v, false)
				if err == nil && sv.GetGlobal != nil && !sessionvarsVolatile[name] {
					// The process-wide value the SetGlobal hook stored, read back through
					// GetGlobal (so that a missing SetGlobal effect is caught).
					after, aerr := sv.GetGlobalFromHook(context.Background(), vars)
					res["after"], res["after_err"] = after, errStr(aerr)
				}
				if perr == nil {
					_ = sv.SetGlobalFromHook(context.Background(), freshVars(svCtx{}), prev, false)
				}
			} else {
				res["persist"] = true
			}
		}
		res["fields"] = diff(f0, scalarDump(vars))
		res["sys"] = diff(s0, systemsDump(vars))
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, err
	case "set_global_hook":
		resetSysvarAtomics()
		defer resetSysvarAtomics()
		vars := freshVars(svCtx{})
		vars.StmtCtx.StmtType = "Set"
		name, val := arg[string](args, 0), arg[string](args, 1)
		sv := variable.GetSysVar(name)
		res := map[string]any{}
		v, err := sv.Validate(vars, val, variable.ScopeGlobal)
		if err == nil {
			err = sv.SetGlobalFromHook(context.Background(), vars, v, false)
			if err == nil && sv.GetGlobal != nil {
				after, aerr := sv.GetGlobalFromHook(context.Background(), vars)
				res["after"], res["after_err"] = after, errStr(aerr)
			}
			if name == variable.TiDBRedactLog {
				// fix pingcap/tidb#62892: SetGlobal stores the process-wide flag.
				res["redact"] = errors.RedactLogEnabled.Load()
			}
		}
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, err
	case "set_global_hook2":
		// Two SET GLOBALs of one variable in a row (D05.2 B3, Lucky's review of B1):
		// state left by the first call is visible to the second (LDAP pool capacities,
		// authentication_ldap_*_ca_path, tidb-rs #64).
		resetSysvarAtomics()
		defer resetSysvarAtomics()
		vars := freshVars(svCtx{})
		vars.StmtCtx.StmtType = "Set"
		name := arg[string](args, 0)
		var steps []any
		for _, val := range arg[[]string](args, 1) {
			sv := variable.GetSysVar(name)
			res := map[string]any{}
			v, err := sv.Validate(vars, val, variable.ScopeGlobal)
			if err == nil {
				err = sv.SetGlobalFromHook(context.Background(), vars, v, false)
				if err == nil && sv.GetGlobal != nil {
					after, aerr := sv.GetGlobalFromHook(context.Background(), vars)
					res["after"], res["after_err"] = after, errStr(aerr)
				}
			}
			// The client sees errors.Cause (server/conn.go writeError).
			res["err"] = errStr(errors.Cause(err))
			steps = append(steps, res)
		}
		return vec.WithWarnings{Out: steps, Warnings: sysvarWarnings(vars)}, nil
	case "tidb_table":
		// D05.2 PR C (T10): GetGlobal of a tidb_gc_* variable, then (val != "") Validate and
		// its SetGlobal hook and GetGlobal again, on a session whose accessor holds the given
		// mysql.tidb rows; the output includes mysql.tidb afterwards.
		resetSysvarAtomics()
		defer resetSysvarAtomics()
		name, val := arg[string](args, 0), arg[string](args, 1)
		vars := freshVars(svCtx{StmtType: "Set", TiDB: arg[map[string]string](args, 2)})
		sv := variable.GetSysVar(name)
		res := map[string]any{}
		before, berr := sv.GetGlobalFromHook(context.Background(), vars)
		res["before"], res["before_err"] = before, errStr(berr)
		var err error
		if val != "" {
			var v string
			v, err = sv.Validate(vars, val, variable.ScopeGlobal)
			if err == nil {
				err = sv.SetGlobalFromHook(context.Background(), vars, v, false)
				if err == nil {
					after, aerr := sv.GetGlobalFromHook(context.Background(), vars)
					res["after"], res["after_err"] = after, errStr(aerr)
				}
			}
		}
		res["tidb"] = tidbTableDump(vars)
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, err
	case "init":
		resetSysvarAtomics()
		vars := freshVars(svCtx{})
		vars.StmtCtx.StmtType = "Set"
		var errs []any
		for _, p := range arg[[][2]string](args, 0) {
			errs = append(errs, errStr(vars.SetSystemVar(p[0], p[1])))
		}
		res := map[string]any{"errs": errs, "fields": scalarDump(vars), "sys": systemsDump(vars)}
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, nil
	}
	panic(fmt.Sprintf("sessionvars: unknown op %q", op))
}
