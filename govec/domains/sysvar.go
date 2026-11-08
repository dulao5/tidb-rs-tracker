package domains

// Domain "sysvar": the system variable registry and validation of
// pkg/sessionctx/variable (block D05.1, N8 §3.1).
//
//   - registry: the static fields of every registered *SysVar after init (Value
//     of version_compile_os / version_compile_machine is replaced by "<GOOS>" /
//     "<GOARCH>": the Rust port computes them for its own target), plus the
//     results of the field-only methods (SkipInit, SkipSysvarCache, scope tests).
//   - validate / relaxed: SysVar.Validate / ValidateWithRelaxedValidation on a
//     fresh SessionVars (NewSessionVars(nil), TimeZone UTC, the test mock
//     accessor NewMockGlobalAccessor4Tests holding every default), with an
//     optional context (statement type, NoopFuncsMode, global overrides, TLS,
//     allowMPPExecution). The process-wide atomics the validation hooks read are
//     reset to their defaults before every call, so records are independent.
//   - the helpers of varsutil.go, variable.go, removed.go, sequence_state.go,
//     statusvar.go and setvar_affect.go (via the registry's hint flag).
//
// The hooks that need the session state of block D05.2 (SetSession, SetGlobal,
// GetSession, GetGlobal, GetStateValue) are not called here.

import (
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/config"
	"github.com/pingcap/tidb/pkg/privilege/privileges/ldap"
	"github.com/pingcap/tidb/pkg/sessionctx/slowlogrule"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/memory"
)

const sysvarSeed = 5051

//go:embed testdata/sysvar_literals.tsv
var sysvarLiteralsTSV string

func sysvarLiterals() []datumLit { return parseDatumLiterals(sysvarLiteralsTSV) }

// SysvarMinedTests: the Go test functions whose literals are in testdata/sysvar_literals.tsv.
func SysvarMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, c := range sysvarMinedPlan(sysvarNames()) {
		if !seen[c.fn] {
			seen[c.fn] = true
			fns = append(fns, c.fn)
		}
	}
	return fns
}

func init() {
	vec.Register(&vec.Domain{
		Name:     "sysvar",
		Seed:     sysvarSeed,
		Coverage: sysvarCoverage,
		Blocks:   []string{"D05.1"},
		Errors: []vec.NamedErr{
			{Name: "ErrWrongValueForVar", Err: variable.ErrWrongValueForVar},
			{Name: "ErrWrongTypeForVar", Err: variable.ErrWrongTypeForVar},
			{Name: "ErrTruncatedWrongValue", Err: variable.ErrTruncatedWrongValue},
			{Name: "ErrIncorrectScope", Err: variable.ErrIncorrectScope},
			{Name: "ErrUnknownSystemVar", Err: variable.ErrUnknownSystemVar},
			{Name: "ErrReadOnly", Err: variable.ErrReadOnly},
			{Name: "ErrUnsupportedIsolationLevel", Err: variable.ErrUnsupportedIsolationLevel},
			{Name: "ErrFunctionsNoopImpl", Err: variable.ErrFunctionsNoopImpl},
			{Name: "ErrVariableNoLongerSupported", Err: variable.ErrVariableNoLongerSupported},
			{Name: "ErrWarnDeprecatedSyntaxNoReplacement", Err: variable.ErrWarnDeprecatedSyntaxNoReplacement},
			{Name: "ErrWarnDeprecatedSyntaxSimpleMsg", Err: variable.ErrWarnDeprecatedSyntaxSimpleMsg},
			{Name: "ErrInvalidDefaultUTF8MB4Collation", Err: variable.ErrInvalidDefaultUTF8MB4Collation},
		},
		Exempt: map[string]string{
			"init":                 "variable.go init (registry construction) and statusvar.go init (cipher list); covered through registry / status_vars",
			"RegisterStatistics":   "registration of status providers (server, D12); covered through status_vars with the default provider",
			"UnregisterStatistics": "only used in tests",
		},
		Gzip:     true,
		Generate: sysvarGenerate,
		Exec:     sysvarExec,
	})
}

var sysvarCoverage = map[string]vec.Coverage{
	"registry": {Min: 900, GoFuncs: []string{"GetSysVar", "GetSysVars", "SysVar.SkipInit", "SysVar.SkipSysvarCache",
		"SysVar.HasNoneScope", "SysVar.HasSessionScope", "SysVar.HasGlobalScope", "SysVar.HasInstanceScope",
		"setHintUpdatable", "collectAllowFuncName4ExpressionIndex", "BoolToOnOff", "int32ToBoolStr"}},
	"validate": {Min: 20000, GoFuncs: []string{"SysVar.Validate", "SysVar.ValidateFromType", "SysVar.validateScope",
		"SysVar.checkTimeSystemVar", "SysVar.checkDurationSystemVar", "SysVar.checkUInt64SystemVar",
		"SysVar.checkInt64SystemVar", "SysVar.checkEnumSystemVar", "SysVar.checkFloatSystemVar", "SysVar.checkBoolSystemVar",
		"checkCollation", "checkDefaultCollationForUTF8MB4", "checkCharacterSet", "checkReadOnly", "checkIsolationLevel",
		"appendDeprecationWarning", "parseMemoryLimit", "parsePercentage", "parseByteSize", "parseSchemaCacheSize",
		"updatePasswordValidationLength", "ValidAnalyzeSkipColumnTypes", "TidbOptInt64", "TidbOptInt", "tidbOptFloat64", "TiDBOptOn"}},
	"relaxed":      {Min: 3000, GoFuncs: []string{"SysVar.ValidateWithRelaxedValidation"}},
	"set_sys_var":  {Min: 10, GoFuncs: []string{"SetSysVar"}},
	"native_type":  {Min: 300, GoFuncs: []string{"SysVar.GetNativeValType"}},
	"scope_string": {Min: 8, GoFuncs: []string{"ScopeFlag.String"}},
	"opt":          {Min: 450, GoFuncs: []string{"TiDBOptOn", "TiDBOptOnOffWarn", "TiDBOptEnableClustered", "tidbOptAssertionLevel", "tidbOptPositiveInt32", "TidbOptInt", "TidbOptInt64", "TidbOptUint64", "tidbOptFloat64", "trueFalseToOnOff", "OnOffToTrueFalse", "BoolToOnOff", "int32ToBoolStr"}},
	"byte_size":    {Min: 200, GoFuncs: []string{"parseByteSize", "parsePercentage"}},
	"skip_types":   {Min: 50, GoFuncs: []string{"ValidAnalyzeSkipColumnTypes", "ParseAnalyzeSkipColumnTypes"}},
	"removed":      {Min: 20, GoFuncs: []string{"IsRemovedSysVar", "CheckSysVarIsRemoved"}},
	"sequence":     {Min: 20, GoFuncs: []string{"NewSequenceState", "SequenceState.UpdateState", "SequenceState.GetLastValue", "SequenceState.GetAllStates", "SequenceState.SetAllStates"}},
	"order_dep":    {Min: 20, GoFuncs: []string{"OrderByDependency"}},
	"status_vars":  {Min: 1, GoFuncs: []string{"GetStatusVars", "defaultStatusStat.GetScope", "defaultStatusStat.Stats"}},
	"ddl_atomic":   {Min: 12, GoFuncs: []string{"SetDDLReorgWorkerCounter", "GetDDLReorgWorkerCounter", "SetDDLFlashbackConcurrency", "GetDDLFlashbackConcurrency", "SetDDLReorgBatchSize", "GetDDLReorgBatchSize", "SetDDLErrorCountLimit", "GetDDLErrorCountLimit", "SetDDLReorgRowFormat", "GetDDLReorgRowFormat", "SetMaxDeltaSchemaCount", "GetMaxDeltaSchemaCount"}},
	"defaults":     {Min: 1, GoFuncs: []string{"serverMemoryLimitDefaultValue", "mustParseDuration"}},
}

// ---------------------------------------------------------------- context

// svCtx is the optional session context of a validate / relaxed call.
type svCtx struct {
	StmtType string            `json:"stmt,omitempty"`  // StmtCtx.StmtType ("Set" for SET statements)
	Noop     *int              `json:"noop,omitempty"`  // NoopFuncsMode (0 OFF, 1 ON, 2 WARN)
	Globals  map[string]string `json:"g,omitempty"`     // overrides of the mock accessor's global values
	Systems  map[string]string `json:"s,omitempty"`     // session values (SessionVars.systems)
	TLS      bool              `json:"tls,omitempty"`   // TLSConnectionState != nil
	NoMPP    bool              `json:"nompp,omitempty"` // allowMPPExecution = false
}

// initialRedactLog is errors.RedactLogEnabled after init (tidb_redact_log's SetSession
// hook stores the session value into this process-wide flag: tidb-rs #62).
var initialRedactLog = errors.RedactLogEnabled.Load()

// resetSysvarAtomics puts the process-wide state the hooks read or write back to its
// value after init, so that no record depends on an earlier one. The process-wide state
// written by hooks of variable/sysvar.go (v8.5.8):
//   - Validation hooks: the password-validation atomics, GOGCTunerThreshold,
//     RunAutoAnalyze, EnableAutoAnalyzePriorityQueue (reset below);
//   - SetSession hooks: tidb_redact_log stores errors.RedactLogEnabled (reset below;
//     upstream bug tidb-rs #62: the flag redacts the warnings of every session);
//     tidb_constraint_check_in_place_pessimistic increments a metrics counter (not
//     observed by any record);
//   - SetGlobal hooks of GLOBAL-scope variables over variable-package atomics (D05.2 PR B):
//     restored by processGlobalsRestore (captured at init);
//   - SetGlobal hooks of the 27 INSTANCE-scope variables (log, slow log, plugin,
//     memory/expensive-query thresholds, max_connections, DDL/stats owner, statement
//     summary files, service scope…): the sessionvars domain's set_global op restores
//     the previous value right after the call.
//
// The other SetSession hooks write only the session.
func resetSysvarAtomics() {
	errors.RedactLogEnabled.Store(initialRedactLog)
	variable.PasswordValidationLength.Store(8)
	variable.PasswordValidationMixedCaseCount.Store(1)
	variable.PasswordValidtaionNumberCount.Store(1)
	variable.PasswordValidationSpecialCharCount.Store(1)
	variable.GOGCTunerThreshold.Store(variable.DefTiDBGOGCTunerThreshold)
	variable.RunAutoAnalyze.Store(variable.DefTiDBEnableAutoAnalyze)
	variable.EnableAutoAnalyzePriorityQueue.Store(variable.DefTiDBEnableAutoAnalyzePriorityQueue)
	// D05.2 B2: tidb_cloud_storage_uri / tidb_slow_log_rules SetGlobal hooks.
	variable.CloudStorageURI.Store("")
	variable.GlobalSlowLogRules.Store(&slowlogrule.GlobalSlowLogRules{RulesMap: make(map[int64]*slowlogrule.SlowLogRules)})
	processGlobalsRestore()
}

// processGlobalsRestore restores the variable-package atomics written by SetGlobal hooks
// (D05.2 PR B: the GLOBAL-scope set_global records run them) to their values after init.
var processGlobalsRestore = func() func() {
	vAccelerateUserCreationUpdate := variable.AccelerateUserCreationUpdate.Load()
	vConnectAttrsSize := variable.ConnectAttrsSize.Load()
	vEnableAutoAnalyzePriorityQueue := variable.EnableAutoAnalyzePriorityQueue.Load()
	vEnableBatchDML := variable.EnableBatchDML.Load()
	vEnableCheckConstraint := variable.EnableCheckConstraint.Load()
	vEnableFastReorg := variable.EnableFastReorg.Load()
	vEnableForeignKey := variable.EnableForeignKey.Load()
	vEnableHistoricalStatsForCapture := variable.EnableHistoricalStatsForCapture.Load()
	vEnableInstancePlanCache := variable.EnableInstancePlanCache.Load()
	vEnableNoopVariables := variable.EnableNoopVariables.Load()
	vEnableRCReadCheckTS := variable.EnableRCReadCheckTS.Load()
	vEnableRowLevelChecksum := variable.EnableRowLevelChecksum.Load()
	vEnableTTLJob := variable.EnableTTLJob.Load()
	vEnableTmpStorageOnOOM := variable.EnableTmpStorageOnOOM.Load()
	vGCMaxWaitTime := variable.GCMaxWaitTime.Load()
	vIgnoreInlistPlanDigest := variable.IgnoreInlistPlanDigest.Load()
	vMaxUserConnectionsValue := variable.MaxUserConnectionsValue.Load()
	vMemQuotaBindingCache := variable.MemQuotaBindingCache.Load()
	vMemoryUsageAlarmKeepRecordNum := variable.MemoryUsageAlarmKeepRecordNum.Load()
	vMemoryUsageAlarmRatio := variable.MemoryUsageAlarmRatio.Load()
	vOOMAction := variable.OOMAction.Load()
	vPasswordHistory := variable.PasswordHistory.Load()
	vPasswordReuseInterval := variable.PasswordReuseInterval.Load()
	vPasswordValidationLength := variable.PasswordValidationLength.Load()
	vPasswordValidationMixedCaseCount := variable.PasswordValidationMixedCaseCount.Load()
	vPasswordValidtaionNumberCount := variable.PasswordValidtaionNumberCount.Load()
	vPasswordValidationSpecialCharCount := variable.PasswordValidationSpecialCharCount.Load()
	vProcessGeneralLog := variable.ProcessGeneralLog.Load()
	vQueryLogMaxLen := variable.QueryLogMaxLen.Load()
	vRunAutoAnalyze := variable.RunAutoAnalyze.Load()
	vSchemaVersionCacheLimit := variable.SchemaVersionCacheLimit.Load()
	vStatsLoadPseudoTimeout := variable.StatsLoadPseudoTimeout.Load()
	vStatsLoadSyncWait := variable.StatsLoadSyncWait.Load()
	vTiFlashEnablePipelineMode := variable.TiFlashEnablePipelineMode.Load()
	vVarTiDBSuperReadOnly := variable.VarTiDBSuperReadOnly.Load()
	vGOGCTunerThreshold := variable.GOGCTunerThreshold.Load()
	vHistoricalStatsDuration := variable.HistoricalStatsDuration.Load()
	vAdvancerCheckPointLagLimit := variable.AdvancerCheckPointLagLimit.Load()
	vEnablePProfSQLCPU := variable.EnablePProfSQLCPU.Load()
	vForcePriority := atomic.LoadInt32(&variable.ForcePriority)
	vEnableGCAwareMemoryTrack := memory.EnableGCAwareMemoryTrack.Load()
	vServerMemoryLimitSessMinSize := memory.ServerMemoryLimitSessMinSize.Load()
	vServerMemoryLimitOriginText := memory.ServerMemoryLimitOriginText.Load()
	vServerMemoryLimit := memory.ServerMemoryLimit.Load()
	vConfig := *config.GetGlobalConfig()
	vDDLSlowOprThreshold := atomic.LoadUint32(&variable.DDLSlowOprThreshold)
	vExpensiveQueryTimeThreshold := atomic.LoadUint64(&variable.ExpensiveQueryTimeThreshold)
	vExpensiveTxnTimeThreshold := atomic.LoadUint64(&variable.ExpensiveTxnTimeThreshold)
	return func() {
		variable.AccelerateUserCreationUpdate.Store(vAccelerateUserCreationUpdate)
		variable.ConnectAttrsSize.Store(vConnectAttrsSize)
		variable.EnableAutoAnalyzePriorityQueue.Store(vEnableAutoAnalyzePriorityQueue)
		variable.EnableBatchDML.Store(vEnableBatchDML)
		variable.EnableCheckConstraint.Store(vEnableCheckConstraint)
		variable.EnableFastReorg.Store(vEnableFastReorg)
		variable.EnableForeignKey.Store(vEnableForeignKey)
		variable.EnableHistoricalStatsForCapture.Store(vEnableHistoricalStatsForCapture)
		variable.EnableInstancePlanCache.Store(vEnableInstancePlanCache)
		variable.EnableNoopVariables.Store(vEnableNoopVariables)
		variable.EnableRCReadCheckTS.Store(vEnableRCReadCheckTS)
		variable.EnableRowLevelChecksum.Store(vEnableRowLevelChecksum)
		variable.EnableTTLJob.Store(vEnableTTLJob)
		variable.EnableTmpStorageOnOOM.Store(vEnableTmpStorageOnOOM)
		variable.GCMaxWaitTime.Store(vGCMaxWaitTime)
		variable.IgnoreInlistPlanDigest.Store(vIgnoreInlistPlanDigest)
		variable.MaxUserConnectionsValue.Store(vMaxUserConnectionsValue)
		variable.MemQuotaBindingCache.Store(vMemQuotaBindingCache)
		variable.MemoryUsageAlarmKeepRecordNum.Store(vMemoryUsageAlarmKeepRecordNum)
		variable.MemoryUsageAlarmRatio.Store(vMemoryUsageAlarmRatio)
		variable.OOMAction.Store(vOOMAction)
		variable.PasswordHistory.Store(vPasswordHistory)
		variable.PasswordReuseInterval.Store(vPasswordReuseInterval)
		variable.PasswordValidationLength.Store(vPasswordValidationLength)
		variable.PasswordValidationMixedCaseCount.Store(vPasswordValidationMixedCaseCount)
		variable.PasswordValidtaionNumberCount.Store(vPasswordValidtaionNumberCount)
		variable.PasswordValidationSpecialCharCount.Store(vPasswordValidationSpecialCharCount)
		variable.ProcessGeneralLog.Store(vProcessGeneralLog)
		variable.QueryLogMaxLen.Store(vQueryLogMaxLen)
		variable.RunAutoAnalyze.Store(vRunAutoAnalyze)
		variable.SchemaVersionCacheLimit.Store(vSchemaVersionCacheLimit)
		variable.StatsLoadPseudoTimeout.Store(vStatsLoadPseudoTimeout)
		variable.StatsLoadSyncWait.Store(vStatsLoadSyncWait)
		variable.TiFlashEnablePipelineMode.Store(vTiFlashEnablePipelineMode)
		variable.VarTiDBSuperReadOnly.Store(vVarTiDBSuperReadOnly)
		variable.GOGCTunerThreshold.Store(vGOGCTunerThreshold)
		variable.HistoricalStatsDuration.Store(vHistoricalStatsDuration)
		variable.AdvancerCheckPointLagLimit.Store(vAdvancerCheckPointLagLimit)
		variable.EnablePProfSQLCPU.Store(vEnablePProfSQLCPU)
		atomic.StoreInt32(&variable.ForcePriority, vForcePriority)
		memory.EnableGCAwareMemoryTrack.Store(vEnableGCAwareMemoryTrack)
		memory.ServerMemoryLimitSessMinSize.Store(vServerMemoryLimitSessMinSize)
		memory.ServerMemoryLimitOriginText.Store(vServerMemoryLimitOriginText)
		memory.ServerMemoryLimit.Store(vServerMemoryLimit)
		// The global config: INSTANCE hooks write it, other domains may store their own.
		c := vConfig
		config.StoreGlobalConfig(&c)
		resetLDAP(ldap.LDAPSASLAuthImpl.SetSASLAuthMethod, ldap.LDAPSASLAuthImpl)
		resetLDAP(nil, ldap.LDAPSimpleAuthImpl)
		atomic.StoreUint32(&variable.DDLSlowOprThreshold, vDDLSlowOprThreshold)
		atomic.StoreUint64(&variable.ExpensiveQueryTimeThreshold, vExpensiveQueryTimeThreshold)
		atomic.StoreUint64(&variable.ExpensiveTxnTimeThreshold, vExpensiveTxnTimeThreshold)
	}
}()

// ldapSettings is the setter set of the LDAP impls (ldap.ldapAuthImpl is unexported).
type ldapSettings interface {
	SetBindBaseDN(string)
	SetBindRootDN(string)
	SetBindRootPW(string)
	SetSearchAttr(string)
	SetLDAPServerHost(string)
	SetLDAPServerPort(int)
	SetEnableTLS(bool)
	SetCAPath(string) error
	SetInitCapacity(int)
	SetMaxCapacity(int)
}

// resetLDAP puts an LDAP impl back to its zero value (written by the authentication_ldap_*
// SetGlobal hooks).
func resetLDAP(setMethod func(string), impl ldapSettings) {
	if setMethod != nil {
		setMethod("")
	}
	impl.SetInitCapacity(0)
	impl.SetMaxCapacity(0)
	impl.SetBindBaseDN("")
	impl.SetBindRootDN("")
	impl.SetBindRootPW("")
	impl.SetSearchAttr("")
	impl.SetLDAPServerHost("")
	impl.SetLDAPServerPort(0)
	impl.SetEnableTLS(false)
	_ = impl.SetCAPath("")
}

// sysvarMemTotal is the MemTotal of the records (tidb_server_memory_limit accepts a
// percentage of it); fixed so that records do not depend on the host.
const sysvarMemTotal = 64 << 30

func freshVars(c svCtx) *variable.SessionVars {
	memory.MemTotal = func() (uint64, error) { return sysvarMemTotal, nil }
	vars := variable.NewSessionVars(nil)
	acc := variable.NewMockGlobalAccessor4Tests()
	for k, v := range c.Globals {
		if err := acc.SetGlobalSysVarOnly(nil, k, v, false); err != nil {
			panic(err)
		}
	}
	vars.GlobalVarsAccessor = acc
	vars.TimeZone = time.UTC
	vars.StmtCtx.StmtType = c.StmtType
	if c.Noop != nil {
		vars.NoopFuncsMode = *c.Noop
	}
	for k, v := range c.Systems {
		if err := vars.SetSystemVarWithoutValidation(k, v); err != nil {
			panic(err)
		}
	}
	if c.TLS {
		vars.TLSConnectionState = &tlsState
	}
	if c.NoMPP {
		if err := vars.SetSystemVarWithoutValidation(variable.TiDBAllowMPPExecution, variable.Off); err != nil {
			panic(err)
		}
	}
	return vars
}

func sysvarWarnings(vars *variable.SessionVars) []error {
	var ws []error
	for _, w := range vars.StmtCtx.GetWarnings() {
		ws = append(ws, w.Err)
	}
	return ws
}

// ---------------------------------------------------------------- generator

func sysvarNames() []*variable.SysVar {
	m := variable.GetSysVars()
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*variable.SysVar, len(names))
	for i, n := range names {
		out[i] = m[n]
	}
	return out
}

var scopeOf = map[string]variable.ScopeFlag{"session": variable.ScopeSession, "global": variable.ScopeGlobal, "instance": variable.ScopeInstance, "none": variable.ScopeNone}

func sysvarValues(sv *variable.SysVar, rng *rand.Rand) []string {
	def := sv.Value
	switch sv.Name {
	case "version_compile_os", "version_compile_machine":
		// The default depends on the build target: a fixed value keeps the records of
		// every host identical.
		def = "linux"
	}
	vals := []string{def, "", "ON", "OFF", "on", "1", "0", "-1", "abc"}
	switch sv.Type {
	case variable.TypeBool:
		vals = append(vals, "off", "On", "2", "-2", "true", "TRUE", "false", "01", " 1", "1 ", "+1", "9223372036854775808")
	case variable.TypeInt, variable.TypeUnsigned, variable.TypeFloat:
		mn, mx := sv.MinValue, sv.MaxValue
		vals = append(vals, strconv.FormatInt(mn, 10), strconv.FormatUint(mx, 10), "1.5", "-0", "+5", " 5", "1e3",
			"18446744073709551616", "9223372036854775808", "-9223372036854775808", "-9223372036854775809", "0x10", "1_000")
		if mn > math.MinInt64 {
			vals = append(vals, strconv.FormatInt(mn-1, 10))
		}
		if mx < math.MaxUint64 {
			vals = append(vals, strconv.FormatUint(mx+1, 10))
		}
		if mx > 0 && mx <= math.MaxInt64 {
			vals = append(vals, strconv.FormatInt(rng.Int64N(int64(mx))+1, 10))
		}
		if sv.Type == variable.TypeFloat {
			vals = append(vals, "0.5", "NaN", "Inf", "-Inf", "1e400", ".5", "5.", strconv.FormatFloat(rng.Float64()*float64(mx), 'f', -1, 64))
		}
	case variable.TypeEnum:
		for i, p := range sv.PossibleValues {
			vals = append(vals, p, strings.ToLower(p), strconv.Itoa(i))
		}
		vals = append(vals, strconv.Itoa(len(sv.PossibleValues)), " "+sv.Value)
	case variable.TypeDuration:
		vals = append(vals, "1s", "1h", "-1s", "1m30s", "0", "1.5h", "100ms", "1000h", "9999999h", "1d")
	case variable.TypeTime:
		vals = append(vals, "10:00", "10:00 +0800", "23:59 -0700", "25:00", "1:00", "10:00 +08:00", "00:00 +0000",
			"24:00", "23:60", "24:00 +0000", "10:00 +2401", "10:00 +2460", "10:00 +2461", "10:00 x0800", "1:5", "10:0", "10:00 +080", "10:00  +0800", "10:00+0800", "10:00 +0800x", "1:00 -1200", "\"10:00", "日:00")
	case variable.TypeStr:
		vals = append(vals, strings.ToUpper(def), strings.ToLower(def), " "+def, def+",",
			"utf8mb4", "latin1", "binary", "utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "gbk",
			"SYSTEM", "+08:00", "UTC", "Asia/Shanghai", "-13:00", "+14:01",
			"512MB", "1GiB", "100KB", "1TB", "10%", "1024", "1KB", "64MiB", "127", "128",
			"READ-COMMITTED", "REPEATABLE-READ", "SERIALIZABLE", "READ-UNCOMMITTED",
			"tikv", "TiFlash", "tidb,tikv", "tiflash, tikv", "json,text", "BLOB, json", "int",
			"STRICT_TRANS_TABLES", "ansi", "NO_ZERO_DATE,ONLY_FULL_GROUP_BY", "bad_mode", "legacy", "optimized",
			"0.5", "0.86", "51%", "0.51", "cost", "disable", "IN,MIN_MAX", "OFF", "COMPACT", "fast", "-1", "4", "300",
			strings.Repeat("x", 70), "a`b", "日本語", "x ")
	}
	return vals
}

func sysvarGenerate(g *vec.Gen, rng *rand.Rand) {
	all := sysvarNames()
	for _, sv := range all {
		g.Add("sys/registry", "registry", sv.Name)
	}
	noop := []int{0, 1, 2}
	for _, sv := range all {
		vals := sysvarValues(sv, rng)
		scopes := []string{"session", "global"}
		if sv.HasInstanceScope() {
			scopes = append(scopes, "instance")
		}
		if sv.Scope == variable.ScopeNone {
			scopes = append(scopes, "none")
		}
		for _, v := range vals {
			for _, sc := range scopes {
				g.Add("sys/validate", "validate", sv.Name, v, sc, svCtx{})
			}
		}
		for i, v := range vals {
			if i%3 == 0 {
				g.Add("sys/relaxed", "relaxed", sv.Name, v, "session", svCtx{})
				g.Add("sys/relaxed", "relaxed", sv.Name, v, "global", svCtx{})
			}
		}
		// Hooks that read the context: statement type, noop mode, global values, TLS, MPP.
		if sv.Validation != nil {
			for _, v := range vals {
				n := noop[rng.IntN(3)]
				g.Add("sys/validate_ctx", "validate", sv.Name, v, "session", svCtx{StmtType: "Set", Noop: &n})
				g.Add("sys/validate_ctx", "validate", sv.Name, v, "global", svCtx{StmtType: "Set"})
			}
		}
	}
	// Context-dependent hooks.
	on, off, warn := 1, 0, 2
	for _, n := range []string{"tx_read_only", "transaction_read_only", "offline_mode", "super_read_only", "read_only", "sql_auto_is_null"} {
		for _, v := range []string{"ON", "OFF", "1"} {
			for _, nm := range []*int{&on, &off, &warn} {
				g.Add("sys/noop", "validate", n, v, "session", svCtx{Noop: nm})
			}
			for _, gv := range []string{"ON", "OFF", "WARN"} {
				g.Add("sys/noop", "validate", n, v, "global", svCtx{Globals: map[string]string{"tidb_enable_noop_functions": gv}})
			}
		}
		g.Add("sys/noop", "validate", "tidb_enable_noop_functions", "OFF", "session", svCtx{Systems: map[string]string{n: "ON"}})
		g.Add("sys/noop", "validate", "tidb_enable_noop_functions", "OFF", "global", svCtx{Globals: map[string]string{n: "ON"}})
	}
	for _, v := range []string{"SERIALIZABLE", "READ-UNCOMMITTED", "READ-COMMITTED"} {
		for _, n := range []string{"tx_isolation", "transaction_isolation", "tx_isolation_one_shot"} {
			g.Add("sys/iso", "validate", n, v, "session", svCtx{Systems: map[string]string{"tidb_skip_isolation_level_check": "ON"}})
		}
	}
	for _, v := range []string{"ON", "OFF"} {
		g.Add("sys/ctx", "validate", "tidb_super_read_only", v, "global", svCtx{StmtType: "Set", Globals: map[string]string{"tidb_restricted_read_only": "ON"}})
		g.Add("sys/ctx", "validate", "require_secure_transport", v, "global", svCtx{StmtType: "Set", TLS: true})
		g.Add("sys/ctx", "validate", "require_secure_transport", v, "global", svCtx{StmtType: "Set"})
		g.Add("sys/ctx", "validate", "tidb_enforce_mpp", v, "session", svCtx{NoMPP: true})
		g.Add("sys/ctx", "validate", "tidb_enable_plan_replayer_continuous_capture", v, "global", svCtx{Globals: map[string]string{"tidb_enable_historical_stats": "ON"}})
	}
	for _, v := range []string{"1024", "1025", "abc", "0"} {
		g.Add("sys/ctx", "validate", "max_allowed_packet", v, "session", svCtx{StmtType: "Set"})
		g.Add("sys/ctx", "validate", "max_allowed_packet", v, "global", svCtx{StmtType: "Set"})
	}
	for _, mc := range []string{"1", "3", "9"} {
		for _, n := range []string{"validate_password.mixed_case_count", "validate_password.number_count", "validate_password.special_char_count", "validate_password.length"} {
			g.Add("sys/password", "validate", n, mc, "global", svCtx{})
		}
	}
	// Mined literals: values from a Go test function against the variables it names
	// (a literal equal to a variable name) or, failing that, the variable whose Go
	// constant matches the test name.
	sysvarMined(g, all)

	// SetSysVar: the instance overlay.
	for _, p := range [][2]string{{"system_time_zone", "Asia/Tokyo"}, {"hostname", "h1"}, {"port", "4001"}, {"version", "8.0.11-TiDB-x"},
		{"socket", "/tmp/s"}, {"ssl_ca", "ca.pem"}, {"have_ssl", "YES"}, {"tidb_enable_noop_functions", "ON"}, {"license", ""}, {"version_comment", "c"}} {
		g.Add("sys/set_sys_var", "set_sys_var", p[0], p[1])
	}
	for _, sv := range all {
		def := sv.Value
		if sv.Name == "version_compile_os" || sv.Name == "version_compile_machine" {
			def = "linux"
		}
		for _, v := range []string{def, "1", "0", "ON", "abc", "18446744073709551615", "-5"} {
			g.Add("sys/native", "native_type", sv.Name, v)
		}
		if len(g.Calls) > 0 && rng.IntN(3) > 0 {
			continue
		}
	}
	for _, s := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		g.Add("sys/scope", "scope_string", s)
	}
	optVals := []string{"ON", "on", "On", "1", "0", "OFF", "off", "WARN", "warn", "INT_ONLY", "STRICT", "FAST", "fast", "", " 1", "2", "-3",
		"9223372036854775807", "9223372036854775808", "-9223372036854775808", "18446744073709551615", "18446744073709551616",
		"2147483647", "2147483648", "-2147483649", "1.5", "1e3", "NaN", "inf", "-0", "+7", "0x1f", "1_0", "true", "TRUE", "False", "abc"}
	for _, f := range []string{"on", "on_off_warn", "clustered", "assertion", "positive_int32", "int", "int64", "uint64", "float64", "tf_onoff", "onoff_tf", "bool", "int32_bool"} {
		for _, v := range optVals {
			g.Add("sys/opt", "opt", f, v, 42)
		}
	}
	byteVals := []string{"0", "1", "1024", "1KB", "1KiB", "1kb", "512MB", "512MiB", "1GB", "1GiB", "2TB", "1TiB", "1PB", "-1", "+5",
		" 5", "5 ", "5\n", "5KB ", "KB", "", "1.5GB", "18446744073709551615", "18446744073709551616", "17179869184GB", "16777216TB",
		"10%", "50%", "99%", "100%", "0%", "1%%", "-5%", " 5%", "5% ", "5%x", "5 %", "0x10", "010", "1e3", "5MB5", "5MBx"}
	for _, v := range byteVals {
		g.Add("sys/byte_size", "byte_size", v)
	}
	for i := 0; i < 160; i++ {
		units := []string{"", "KB", "KiB", "MB", "MiB", "GB", "GiB", "TB", "TiB", "%", "B", "kb"}
		g.Add("sys/byte_size_rand", "byte_size", strconv.FormatUint(rng.Uint64()>>uint(rng.IntN(64)), 10)+units[rng.IntN(len(units))])
	}
	skipVals := []string{"", "json", "JSON", "json,text", "json, text", " json ,TEXT", "blob,mediumblob,longblob", "int", "json,int",
		"json,,text", ",", "text,text", "longtext", "mediumtext", "JSON,Blob", "json;text", "tinyblob"}
	for _, v := range skipVals {
		g.Add("sys/skip_types", "skip_types", v)
	}
	for i := 0; i < 40; i++ {
		parts := []string{"json", "text", "blob", "int", "", " JSON", "LONGBLOB ", "mediumtext"}
		var b []string
		for j := 0; j <= rng.IntN(4); j++ {
			b = append(b, parts[rng.IntN(len(parts))])
		}
		g.Add("sys/skip_types_rand", "skip_types", strings.Join(b, ","))
	}
	for _, n := range []string{"tidb_enable_alter_placement", "tidb_mem_quota_hashjoin", "tidb_mem_quota_mergejoin", "tidb_mem_quota_sort",
		"tidb_mem_quota_topn", "tidb_mem_quota_indexlookupreader", "tidb_mem_quota_indexlookupjoin", "tidb_enable_global_temporary_table",
		"tidb_slow_log_masking", "placement_checks", "tidb_enable_streaming", "tidb_opt_broadcast_join", "tidb_enable_change_multi_schema",
		"TIDB_ENABLE_STREAMING", "tidb_mem_quota_query", "", "autocommit", "tidb_enable_streaming ", "x", "placement_check"} {
		g.Add("sys/removed", "removed", n)
	}
	for i := 0; i < 30; i++ {
		var steps [][3]int64
		for j := 0; j < rng.IntN(12); j++ {
			steps = append(steps, [3]int64{int64(rng.IntN(4)), int64(rng.IntN(6)) - 1, rng.Int64N(1000) - 500})
		}
		g.Add("sys/sequence", "sequence", steps)
	}
	for i := 0; i < 30; i++ {
		var names []string
		for j := 0; j < rng.IntN(8); j++ {
			names = append(names, all[rng.IntN(len(all))].Name)
		}
		names = append(names, "tidb_snapshot", "unknown_var_x")
		if rng.IntN(2) == 0 {
			names = append(names, "tidb_enable_noop_functions", "tx_read_only", "transaction_isolation")
		}
		g.Add("sys/order_dep", "order_dep", names)
	}
	for _, w := range []string{"reorg_worker", "flashback", "reorg_batch", "err_limit", "row_format", "delta_schema"} {
		for _, v := range []int64{0, 7, -3} {
			g.Add("sys/ddl_atomic", "ddl_atomic", w, v)
		}
	}
	for _, d := range []string{"1s", "10m", "1h30m", "0"} {
		g.Add("sys/defaults", "defaults", d)
	}
	g.Add("sys/status", "status_vars", false)
	g.Add("sys/status", "status_vars", true)
	sysvarWhitespace(g, all)
}

// sscanfSysvars are the variables whose values go through fmt.Sscanf (parseByteSize /
// parsePercentage in varsutil.go), where leading and trailing white space changes the
// result (#45: '5 ' is 5%, '5' is 5 bytes). The Go name constants keep it in sync with
// sysvar.go; generation panics when a name is no longer registered.
var sscanfSysvars = []string{
	variable.TiDBServerMemoryLimit, variable.TiDBServerMemoryLimitSessMinSize, variable.TiDBServerMemoryLimitGCTrigger,
	variable.TiDBInstancePlanCacheMaxMemSize, variable.TiDBSchemaCacheSize,
}

// sysvarWhitespace adds, for every Sscanf-parsed variable and every TypeDuration
// variable, the base inputs wrapped in leading / trailing white space (Lucky's review of
// D05.1, the source of #45).
func sysvarWhitespace(g *vec.Gen, all []*variable.SysVar) {
	wrap := func(x string) []string {
		return []string{" " + x, x + " ", "  " + x, x + "  ", " " + x + " ", "\t" + x, x + "\t", x + "\n", "\n" + x}
	}
	add := func(name string, bases []string) {
		for _, b := range bases {
			for _, v := range wrap(b) {
				g.Add("sys/whitespace", "validate", name, v, "global", svCtx{StmtType: "Set"})
				g.Add("sys/whitespace", "validate", name, v, "session", svCtx{StmtType: "Set"})
			}
		}
	}
	for _, n := range sscanfSysvars {
		if variable.GetSysVar(n) == nil {
			panic("govec sysvar: Sscanf variable not registered: " + n)
		}
		add(n, []string{"5", "0", "512MB", "1GiB", "100KB", "64MiB", "1TB", "10%", "80%", "0.8", "100%", "5x", "5 %"})
	}
	for _, sv := range all {
		if sv.Type == variable.TypeDuration {
			add(sv.Name, []string{sv.Value, "1s", "1h", "100ms", "1m30s", "0"})
		}
	}
}

type minedCase struct{ fn, name, val string }

// sysvarMinedPlan pairs the literals of each mined Go test function with the
// variables it names (a string literal equal to a variable name) or, failing that,
// the variable whose snake-case name is the test name.
func sysvarMinedPlan(all []*variable.SysVar) []minedCase {
	byName := map[string]*variable.SysVar{}
	for _, sv := range all {
		byName[sv.Name] = sv
	}
	type fnLits struct {
		names []string
		vals  []string
	}
	fns := map[string]*fnLits{}
	var order []string
	for _, l := range sysvarLiterals() {
		f, ok := fns[l.fn]
		if !ok {
			f = &fnLits{}
			fns[l.fn] = f
			order = append(order, l.fn)
		}
		var s string
		if l.str != nil {
			s = *l.str
		} else {
			s = strconv.FormatInt(*l.num, 10)
		}
		if _, isVar := byName[strings.ToLower(s)]; isVar && l.str != nil {
			f.names = append(f.names, strings.ToLower(s))
		} else {
			f.vals = append(f.vals, s)
		}
	}
	var out []minedCase
	for _, fn := range order {
		f := fns[fn]
		names := f.names
		if len(names) == 0 {
			if n := testNameToVar(fn, byName); n != "" {
				names = []string{n}
			}
		}
		if len(names) == 0 || len(f.vals) == 0 {
			continue
		}
		names = slices.Compact(slices.Sorted(slices.Values(names)))
		for _, n := range names {
			for _, v := range f.vals {
				out = append(out, minedCase{fn, n, v})
			}
		}
	}
	return out
}

func sysvarMined(g *vec.Gen, all []*variable.SysVar) {
	for _, c := range sysvarMinedPlan(all) {
		for _, sc := range []string{"session", "global"} {
			g.Add(c.fn, "validate", c.name, c.val, sc, svCtx{StmtType: "Set"})
		}
	}
}

func testNameToVar(fn string, byName map[string]*variable.SysVar) string {
	s := strings.TrimPrefix(fn, "Test")
	s = strings.TrimRight(s, "0123456789")
	// CamelCase -> snake, keeping TiDB / TiFlash / TiKV / UTF8MB4 together.
	for _, w := range [][2]string{{"TiDB", "Tidb"}, {"TiFlash", "Tiflash"}, {"TiKV", "Tikv"}, {"UTF8MB4", "Utf8mb4"}, {"GC", "Gc"}, {"DDL", "Ddl"}, {"SQL", "Sql"}, {"PD", "Pd"}, {"TSO", "Tso"}, {"URI", "Uri"}} {
		s = strings.ReplaceAll(s, w[0], w[1])
	}
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	if _, ok := byName[b.String()]; ok {
		return b.String()
	}
	return ""
}

// ---------------------------------------------------------------- exec

func registryOut(sv *variable.SysVar) map[string]any {
	val := sv.Value
	switch sv.Name {
	case "version_compile_os":
		val = "<GOOS>"
	case "version_compile_machine":
		val = "<GOARCH>"
	}
	pv := sv.PossibleValues
	if pv == nil {
		pv = []string{}
	}
	al := sv.Aliases
	if al == nil {
		al = []string{}
	}
	return map[string]any{
		"scope": int(sv.Scope), "value": val, "type": int(sv.Type), "min": sv.MinValue, "max": strconv.FormatUint(sv.MaxValue, 10),
		"acnb": sv.AutoConvertNegativeBool, "ro": sv.ReadOnly, "pv": pv, "ae": sv.AllowEmpty, "aea": sv.AllowEmptyAll,
		"aav": sv.AllowAutoValue, "hint": sv.IsHintUpdatableVerified, "hidden": sv.Hidden, "aliases": al, "dep": sv.Depended,
		"noop": sv.IsNoop, "gcn": sv.GlobalConfigName, "skip_init": sv.SkipInit(), "skip_cache": sv.SkipSysvarCache(),
		"none": sv.HasNoneScope(), "sess": sv.HasSessionScope(), "glob": sv.HasGlobalScope(), "inst": sv.HasInstanceScope(),
		"h_val": sv.Validation != nil, "h_ss": sv.SetSession != nil, "h_sg": sv.SetGlobal != nil, "h_gs": sv.GetSession != nil,
		"h_gg": sv.GetGlobal != nil, "h_gsv": sv.GetStateValue != nil, "h_rdp": sv.RequireDynamicPrivileges != nil,
	}
}

func sysvarExec(op string, args []json.RawMessage) (out any, err error) {
	switch op {
	case "registry":
		sv := variable.GetSysVar(arg[string](args, 0))
		return registryOut(sv), nil
	case "validate", "relaxed":
		resetSysvarAtomics()
		sv := variable.GetSysVar(arg[string](args, 0))
		vars := freshVars(arg[svCtx](args, 3))
		scope := scopeOf[arg[string](args, 2)]
		var v string
		if op == "validate" {
			v, err = sv.Validate(vars, arg[string](args, 1), scope)
		} else {
			v = sv.ValidateWithRelaxedValidation(vars, arg[string](args, 1), scope)
		}
		res := map[string]any{"v": v}
		if n := arg[string](args, 0); strings.HasPrefix(n, "validate_password.") {
			res["pwlen"] = variable.PasswordValidationLength.Load()
			gv, _ := vars.GlobalVarsAccessor.GetGlobalSysVar(variable.ValidatePasswordLength)
			res["pwlen_g"] = gv
		}
		if arg[string](args, 0) == variable.ForeignKeyChecks {
			res["fkc"] = vars.ForeignKeyChecks
		}
		return vec.WithWarnings{Out: res, Warnings: sysvarWarnings(vars)}, err
	case "set_sys_var":
		name := arg[string](args, 0)
		old := variable.GetSysVar(name).Value
		variable.SetSysVar(name, arg[string](args, 1))
		got := variable.GetSysVar(name).Value
		variable.SetSysVar(name, old)
		return got, nil
	case "native_type":
		sv := variable.GetSysVar(arg[string](args, 0))
		d, tp, flag := sv.GetNativeValType(arg[string](args, 1))
		var dv any
		switch d.Kind() {
		case types.KindUint64:
			dv = strconv.FormatUint(d.GetUint64(), 10)
		case types.KindInt64:
			dv = d.GetInt64()
		default:
			dv = d.GetString()
		}
		return map[string]any{"kind": int(d.Kind()), "d": dv, "tp": int(tp), "flag": flag}, nil
	case "scope_string":
		return variable.ScopeFlag(arg[int](args, 0)).String(), nil
	case "opt":
		v := arg[string](args, 1)
		def := arg[int](args, 2)
		switch arg[string](args, 0) {
		case "on":
			return variable.TiDBOptOn(v), nil
		case "on_off_warn":
			return variable.TiDBOptOnOffWarn(v), nil
		case "clustered":
			return int(variable.TiDBOptEnableClustered(v)), nil
		case "assertion":
			return int(svOptAssertionLevel(v)), nil
		case "positive_int32":
			return svOptPositiveInt32(v, def), nil
		case "int":
			return variable.TidbOptInt(v, def), nil
		case "int64":
			return variable.TidbOptInt64(v, int64(def)), nil
		case "uint64":
			return strconv.FormatUint(variable.TidbOptUint64(v, uint64(def)), 10), nil
		case "float64":
			return strconv.FormatFloat(svOptFloat64(v, float64(def)), 'g', -1, 64), nil
		case "tf_onoff":
			return svTrueFalseToOnOff(v), nil
		case "onoff_tf":
			return variable.OnOffToTrueFalse(v), nil
		case "bool":
			return variable.BoolToOnOff(variable.TiDBOptOn(v)), nil
		case "int32_bool":
			n, e := strconv.ParseInt(v, 10, 32)
			if e != nil {
				return nil, nil
			}
			return svInt32ToBoolStr(int32(n)), nil
		}
	case "byte_size":
		s := arg[string](args, 0)
		b, bs := svParseByteSize(s)
		p, ps := svParsePercentage(s)
		return map[string]any{"b": strconv.FormatUint(b, 10), "bs": bs, "p": strconv.FormatUint(p, 10), "ps": ps}, nil
	case "skip_types":
		s := arg[string](args, 0)
		v, e := variable.ValidAnalyzeSkipColumnTypes(s)
		m := variable.ParseAnalyzeSkipColumnTypes(s)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]any{"v": v, "parsed": keys}, e
	case "removed":
		n := arg[string](args, 0)
		return variable.IsRemovedSysVar(n), variable.CheckSysVarIsRemoved(n)
	case "sequence":
		ss := variable.NewSequenceState()
		var outs []any
		for _, st := range arg[[][3]int64](args, 0) {
			switch st[0] {
			case 0:
				ss.UpdateState(st[1], st[2])
				outs = append(outs, nil)
			case 1:
				v, isNull, e := ss.GetLastValue(st[1])
				outs = append(outs, []any{v, isNull, e == nil})
			case 2:
				m := ss.GetAllStates()
				outs = append(outs, sortedSeqMap(m))
			case 3:
				ss.SetAllStates(map[int64]int64{st[1]: st[2], st[1] + 1: st[2] * 2})
				outs = append(outs, nil)
			}
		}
		return map[string]any{"steps": outs, "final": sortedSeqMap(ss.GetAllStates())}, nil
	case "order_dep":
		names := arg[[]string](args, 0)
		m := map[string]string{}
		for _, n := range names {
			m[n] = ""
		}
		got := variable.OrderByDependency(m)
		// Go's map order is random inside each group (M-MAPORDER): record the groups sorted.
		var dep, rest []string
		for _, n := range got {
			if sv := variable.GetSysVar(n); sv != nil && sv.Depended {
				dep = append(dep, n)
			} else {
				rest = append(rest, n)
			}
		}
		ndep := len(dep)
		if !slices.Equal(got[:ndep], dep) {
			return nil, fmt.Errorf("depended variables not first: %v", got)
		}
		sort.Strings(dep)
		sort.Strings(rest)
		return map[string]any{"dep": dep, "rest": rest}, nil
	case "ddl_atomic":
		v := arg[int64](args, 1)
		var before, after int64
		switch arg[string](args, 0) {
		case "reorg_worker":
			before = int64(variable.GetDDLReorgWorkerCounter())
			variable.SetDDLReorgWorkerCounter(int32(v))
			after = int64(variable.GetDDLReorgWorkerCounter())
			variable.SetDDLReorgWorkerCounter(int32(before))
		case "flashback":
			before = int64(variable.GetDDLFlashbackConcurrency())
			variable.SetDDLFlashbackConcurrency(int32(v))
			after = int64(variable.GetDDLFlashbackConcurrency())
			variable.SetDDLFlashbackConcurrency(int32(before))
		case "reorg_batch":
			before = int64(variable.GetDDLReorgBatchSize())
			variable.SetDDLReorgBatchSize(int32(v))
			after = int64(variable.GetDDLReorgBatchSize())
			variable.SetDDLReorgBatchSize(int32(before))
		case "err_limit":
			before = variable.GetDDLErrorCountLimit()
			variable.SetDDLErrorCountLimit(v)
			after = variable.GetDDLErrorCountLimit()
			variable.SetDDLErrorCountLimit(before)
		case "row_format":
			before = variable.GetDDLReorgRowFormat()
			variable.SetDDLReorgRowFormat(v)
			after = variable.GetDDLReorgRowFormat()
			variable.SetDDLReorgRowFormat(before)
		case "delta_schema":
			before = variable.GetMaxDeltaSchemaCount()
			variable.SetMaxDeltaSchemaCount(v)
			after = variable.GetMaxDeltaSchemaCount()
			variable.SetMaxDeltaSchemaCount(before)
		}
		return []int64{before, after}, nil
	case "defaults":
		memory.MemTotal = func() (uint64, error) { return sysvarMemTotal, nil }
		// The Go helpers are unexported: their results through the values they produce.
		return map[string]any{"mem_limit_default": svServerMemoryLimitDefaultValue(), "dur": int64(svMustParseDuration(arg[string](args, 0)))}, nil
	case "status_vars":
		vars := variable.NewSessionVars(nil)
		if arg[bool](args, 0) {
			vars.TLSConnectionState = &tlsState
		}
		m, e := variable.GetStatusVars(vars)
		// Only the default provider's variables: other packages linked into govec
		// (pkg/server: Compression*) register their own providers at init.
		keys := make([]string, 0, len(m))
		for k := range m {
			if strings.HasPrefix(k, "Ssl_") || strings.HasPrefix(k, "Performance_schema_session_connect_attrs_") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var rows []any
		for _, k := range keys {
			rows = append(rows, []any{k, int(m[k].Scope), fmt.Sprint(m[k].Value)})
		}
		return rows, e
	}
	return nil, fmt.Errorf("unknown op %s", op)
}

func sortedSeqMap(m map[int64]int64) [][2]int64 {
	out := make([][2]int64, 0, len(m))
	for k, v := range m {
		out = append(out, [2]int64{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

var _ = collate.GetCollationByName

// A TLS connection state (only its presence matters to the hooks).
var tlsState tls.ConnectionState

//go:linkname svOptAssertionLevel github.com/pingcap/tidb/pkg/sessionctx/variable.tidbOptAssertionLevel
func svOptAssertionLevel(opt string) variable.AssertionLevel

//go:linkname svOptPositiveInt32 github.com/pingcap/tidb/pkg/sessionctx/variable.tidbOptPositiveInt32
func svOptPositiveInt32(opt string, defaultVal int) int

//go:linkname svOptFloat64 github.com/pingcap/tidb/pkg/sessionctx/variable.tidbOptFloat64
func svOptFloat64(opt string, defaultVal float64) float64

//go:linkname svTrueFalseToOnOff github.com/pingcap/tidb/pkg/sessionctx/variable.trueFalseToOnOff
func svTrueFalseToOnOff(str string) string

//go:linkname svInt32ToBoolStr github.com/pingcap/tidb/pkg/sessionctx/variable.int32ToBoolStr
func svInt32ToBoolStr(i int32) string

//go:linkname svParseByteSize github.com/pingcap/tidb/pkg/sessionctx/variable.parseByteSize
func svParseByteSize(s string) (byteSize uint64, normalizedStr string)

//go:linkname svParsePercentage github.com/pingcap/tidb/pkg/sessionctx/variable.parsePercentage
func svParsePercentage(s string) (percentage uint64, normalizedStr string)

//go:linkname svServerMemoryLimitDefaultValue github.com/pingcap/tidb/pkg/sessionctx/variable.serverMemoryLimitDefaultValue
func svServerMemoryLimitDefaultValue() string

//go:linkname svMustParseDuration github.com/pingcap/tidb/pkg/sessionctx/variable.mustParseDuration
func svMustParseDuration(str string) time.Duration
