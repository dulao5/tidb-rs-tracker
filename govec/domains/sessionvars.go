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
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
)

const sessionvarsSeed = 5052

// sessionVarsExcluded mirrors cmd/rsgen/sessionvars.go (write-path scalar fields, X).
var sessionVarsExcluded = map[string]bool{
	"DDLReorgPriority": true, "DMLBatchSize": true, "RetryLimit": true, "DisableTxnAutoRetry": true,
	"ShardAllocateStep": true, "AllowAutoRandExplicitInsert": true, "AllowRemoveAutoInc": true,
	"BatchInsert": true, "BatchDelete": true, "BatchCommit": true, "BulkDMLEnabled": true,
}

func init() {
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
			"set_global":      {Min: 100, GoFuncs: []string{"SysVar.SetGlobalFromHook"}},
			"set_global_hook": {Min: 100},
			"init":            {Min: 20},
		},
		Exempt:   exempt,
		Gzip:     true,
		Generate: sessionvarsGenerate,
		Exec:     sessionvarsExec,
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
}

var sessionvarsVolatile = map[string]bool{"timestamp": true, "rand_seed1": true, "rand_seed2": true,
	"version_compile_os": true, "version_compile_machine": true, "tidb_config": true,
	// GetGlobal converts int(rate.Inf) (the default limiter): platform-dependent (M-F2I,
	// tidb-rs #27).
	"tidb_slow_log_max_per_sec": true}

func sessionvarsGenerate(g *vec.Gen, rng *rand.Rand) {
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
		f0, s0 := scalarDump(vars), systemsDump(vars)
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
		}
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
