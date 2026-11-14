package domains

// Domain "sessiontxn": block D05.4 part 1 (pkg/sessiontxn, the transaction context
// providers of the read path).
//
// The providers are a state machine over the session (sessionctx.Context: the lazy
// transaction, the prepared TSO future, the session variables) and the store's TSO: Go's
// tests run them with testkit and a store. tidb-rs ports them against traits that the
// session implements (tidb-rs specs/D05.4-sessiontxn/spec.md §2) and tests them with a
// mock session, store and TSO (crates/sessiontxn/tests/providers.rs); their SQL-level
// coverage comes with the session (VS1-14). The vectors here cover the package's pure
// functions.
//
// Values: an error is [name, msg]; an action is its int value.
//
// read_ts (tidb-rs#219 review, N11 §4.4): random statement sequences on one session (a
// mock.Context over a mockstore whose oracle counts TSO requests), driving the isolation
// providers as the session does (EnterNewTxnBeforeStmt before a statement outside a
// transaction, EnterNewTxnWithBeginStmt for BEGIN; autocommit statements outside a
// transaction use the optimistic provider). Args: [cfg, steps]; cfg = {mode: "optimistic" |
// "rr" | "rc", autocommit, low_res, snapshot}; a step is "begin", "commit", "read" or
// "point" (a read with an autocommit point-get plan). Out: per read, [source, tso requests]
// counted from OnStmtStart to GetStmtReadTS, with source MaxTs (ts = MaxUint64), Snapshot
// (ts = tidb_snapshot), Tso / LowResolution (the statement requested a TSO), else TxnStart.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync/atomic"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/kv"
	"github.com/pingcap/tidb/pkg/meta/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	plannercore "github.com/pingcap/tidb/pkg/planner/core"
	"github.com/pingcap/tidb/pkg/sessionctx/variable"
	"github.com/pingcap/tidb/pkg/sessiontxn"
	"github.com/pingcap/tidb/pkg/sessiontxn/isolation"
	"github.com/pingcap/tidb/pkg/store/mockstore"
	"github.com/pingcap/tidb/pkg/util/mock"
	"github.com/tikv/client-go/v2/oracle"
)

const sessiontxnSeed = 20261116

func init() {
	vec.Register(&vec.Domain{
		Name:     "sessiontxn",
		Seed:     sessiontxnSeed,
		Coverage: sessiontxnCoverage,
		Blocks:   []string{"D05.4"},
		Exempt:   sessiontxnExempt(),
		Gzip:     true,
		Generate: sessiontxnGenerate,
		Exec:     sessiontxnExec,
	})
}

var sessiontxnCoverage = map[string]vec.Coverage{
	"action":          {Min: 3, GoFuncs: []string{"ErrorAction", "RetryReady", "NoIdea"}},
	"constant_future": {Min: 10, GoFuncs: []string{"ConstantFuture.Wait"}},
	// Drives the providers whose functions are exempt as covered by tidb-rs unit tests; the
	// sequences are the oracle of tidb-rs's ReadTsSource (N11 §4.4).
	"read_ts": {Min: 300, GoFuncs: []string{"ConstantFuture.Wait"}},
}

func sessiontxnExempt() map[string]string {
	byUnitTests := "ported; covered by tidb-rs unit tests crates/sessiontxn/tests/providers.rs (the providers need a session and a TSO: mock-session scenarios; SQL-level coverage with the session, VS1-14)"
	writePath := "write path: tidb-rs is read-only; writes and locking reads in transactions are rejected by the session (N11 D8, 1792)"
	part2 := func(why string) string { return "D05.4 part 2: " + why }
	return map[string]string{
		"TrxHistoryRecorder.Clean":                               part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TrxHistoryRecorder.DumpTrxSummary":                      part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TrxHistoryRecorder.OnTrxEnd":                            part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TrxHistoryRecorder.ResizeSummaries":                     part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TrxHistoryRecorder.SetMinDuration":                      part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"digest":                                                 part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"newTrxHistoryRecorder":                                  part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"newTrxSummaries":                                        part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"trxSummaries.dumpTrxSummary":                            part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"trxSummaries.onTrxEnd":                                  part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"trxSummaries.resize":                                    part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"pkg/session/txninfo/txn_info.go:InitMetricsVars":        part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TxnDurationHistogram":                                   part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TxnInfo.ToDatum":                                        part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"TxnStatusEnteringCounter":                               part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"pkg/session/txninfo/txn_info.go:init":                   part2("pkg/session/txninfo: the transaction summary of information_schema (D06.6 / D14.x readers)"),
		"AddAssertEntranceForLockError":                          part2("failpoint test hooks"),
		"AssertTxnManagerInfoSchema":                             part2("failpoint test hooks"),
		"AssertTxnManagerReadTS":                                 part2("failpoint test hooks"),
		"ExecTestHook":                                           part2("failpoint test hooks"),
		"OnStmtRetryCountInc":                                    part2("failpoint test hooks"),
		"RecordAssert":                                           part2("failpoint test hooks"),
		"TsoRequestCountInc":                                     part2("failpoint test hooks"),
		"TsoUseConstantCountInc":                                 part2("failpoint test hooks"),
		"TsoWaitCountInc":                                        part2("failpoint test hooks"),
		"AdviseOptimizeWithPlanAndThenWarmUp":                    byUnitTests,
		"NewTxn":                                                 byUnitTests,
		"NewTxnInStmt":                                           byUnitTests,
		"CommitBeforeEnterNewTxn":                                byUnitTests,
		"GetSnapshotWithTS":                                      byUnitTests,
		"SetTxnAssertionLevel":                                   writePath,
		"basePessimisticTxnContextProvider.OnPessimisticStmtEnd": writePath,
		"basePessimisticTxnContextProvider.OnPessimisticStmtStart":           writePath,
		"basePessimisticTxnContextProvider.cancelFairLockingIfNeeded":        writePath,
		"basePessimisticTxnContextProvider.retryFairLockingIfNeeded":         writePath,
		"baseTxnContextProvider.ActivateTxn":                                 byUnitTests,
		"baseTxnContextProvider.AdviseOptimizeWithPlan":                      byUnitTests,
		"baseTxnContextProvider.AdviseWarmup":                                byUnitTests,
		"baseTxnContextProvider.GetReadReplicaScope":                         byUnitTests,
		"baseTxnContextProvider.GetSnapshotWithStmtForUpdateTS":              byUnitTests,
		"baseTxnContextProvider.GetSnapshotWithStmtReadTS":                   byUnitTests,
		"baseTxnContextProvider.GetStmtForUpdateTS":                          byUnitTests,
		"baseTxnContextProvider.GetStmtReadTS":                               byUnitTests,
		"baseTxnContextProvider.GetTxnInfoSchema":                            byUnitTests,
		"baseTxnContextProvider.GetTxnScope":                                 byUnitTests,
		"baseTxnContextProvider.OnInitialize":                                byUnitTests,
		"baseTxnContextProvider.OnLocalTemporaryTableCreated":                "temporary tables: a blocked-by:D06.4 stub (D06.4 part 2)",
		"baseTxnContextProvider.OnPessimisticStmtEnd":                        byUnitTests,
		"baseTxnContextProvider.OnPessimisticStmtStart":                      byUnitTests,
		"baseTxnContextProvider.OnStmtCommit":                                byUnitTests,
		"baseTxnContextProvider.OnStmtErrorForNextAction":                    byUnitTests,
		"baseTxnContextProvider.OnStmtRetry":                                 byUnitTests,
		"baseTxnContextProvider.OnStmtRollback":                              byUnitTests,
		"baseTxnContextProvider.OnStmtStart":                                 byUnitTests,
		"baseTxnContextProvider.SetOptionsBeforeCommit":                      writePath,
		"baseTxnContextProvider.SetOptionsOnTxnActive":                       byUnitTests,
		"baseTxnContextProvider.forcePrepareConstStartTS":                    byUnitTests,
		"baseTxnContextProvider.getSnapshotByTS":                             byUnitTests,
		"baseTxnContextProvider.getTxnStartTS":                               byUnitTests,
		"baseTxnContextProvider.isBeginStmtWithStaleRead":                    byUnitTests,
		"baseTxnContextProvider.isTidbSnapshotEnabled":                       byUnitTests,
		"baseTxnContextProvider.prepareTxn":                                  byUnitTests,
		"baseTxnContextProvider.prepareTxnWithOracleTS":                      byUnitTests,
		"baseTxnContextProvider.replaceTxnTsFuture":                          byUnitTests,
		"canReuseTxnWhenExplicitBegin":                                       byUnitTests,
		"funcFuture.Wait":                                                    "ported as the UseStartTs variant of the RC statement future (a closure over the provider); covered by tidb-rs unit tests crates/sessiontxn/tests/providers.rs",
		"newOracleFuture":                                                    byUnitTests,
		"temporaryTableKVFilter.IsUnnecessaryKeyValue":                       writePath,
		"OptimisticTxnContextProvider.AdviseOptimizeWithPlan":                byUnitTests,
		"OptimisticTxnContextProvider.GetStmtForUpdateTS":                    byUnitTests,
		"OptimisticTxnContextProvider.GetStmtReadTS":                         byUnitTests,
		"OptimisticTxnContextProvider.ResetForNewTxn":                        byUnitTests,
		"OptimisticTxnContextProvider.onTxnActive":                           writePath,
		"isOptimisticTxnRetryable":                                           writePath,
		"NeedSetRCCheckTSFlag":                                               byUnitTests,
		"NewPessimisticRCTxnContextProvider":                                 byUnitTests,
		"PessimisticRCTxnContextProvider.AdviseOptimizeWithPlan":             byUnitTests,
		"PessimisticRCTxnContextProvider.AdviseWarmup":                       byUnitTests,
		"PessimisticRCTxnContextProvider.GetSnapshotWithStmtForUpdateTS":     writePath,
		"PessimisticRCTxnContextProvider.GetSnapshotWithStmtReadTS":          byUnitTests,
		"PessimisticRCTxnContextProvider.IsCheckTSInWriteStmtMode":           writePath,
		"PessimisticRCTxnContextProvider.OnStmtErrorForNextAction":           byUnitTests,
		"PessimisticRCTxnContextProvider.OnStmtRetry":                        byUnitTests,
		"PessimisticRCTxnContextProvider.OnStmtStart":                        byUnitTests,
		"PessimisticRCTxnContextProvider.getOracleFuture":                    byUnitTests,
		"PessimisticRCTxnContextProvider.getStmtTS":                          byUnitTests,
		"PessimisticRCTxnContextProvider.handleAfterPessimisticLockError":    writePath,
		"PessimisticRCTxnContextProvider.handleAfterQueryError":              byUnitTests,
		"PessimisticRCTxnContextProvider.prepareStmtTS":                      byUnitTests,
		"planSkipGetTsoFromPD":                                               byUnitTests,
		"stmtState.prepareStmt":                                              byUnitTests,
		"NewPessimisticRRTxnContextProvider":                                 byUnitTests,
		"PessimisticRRTxnContextProvider.AdviseOptimizeWithPlan":             byUnitTests,
		"PessimisticRRTxnContextProvider.OnStmtErrorForNextAction":           byUnitTests,
		"PessimisticRRTxnContextProvider.OnStmtRetry":                        byUnitTests,
		"PessimisticRRTxnContextProvider.OnStmtStart":                        byUnitTests,
		"PessimisticRRTxnContextProvider.getForUpdateTs":                     writePath,
		"PessimisticRRTxnContextProvider.handleAfterPessimisticLockError":    writePath,
		"PessimisticRRTxnContextProvider.updateForUpdateTS":                  writePath,
		"notNeedGetLatestTSFromPD":                                           byUnitTests,
		"NewPessimisticSerializableTxnContextProvider":                       byUnitTests,
		"PessimisticSerializableTxnContextProvider.OnStmtErrorForNextAction": byUnitTests,
		"pkg/sessiontxn/isolation/metrics/metrics.go:InitMetricsVars":        part2("isolation metrics (D14.1)"),
		"pkg/sessiontxn/isolation/metrics/metrics.go:init":                   part2("isolation metrics (D14.1)"),
		"AssertStmtStaleness":                                                part2("failpoint test hooks"),
		"GetSessionSnapshotInfoSchema":                                       part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"NewStaleReadProcessor":                                              part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.GetStalenessInfoSchema":                               part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.GetStalenessReadTS":                                   part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.GetStalenessTSEvaluatorForPrepare":                    part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.IsStaleness":                                          part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.OnExecutePrepared":                                    part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.OnSelectTable":                                        part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.init":                                                 part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.setAsNonStaleRead":                                    part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.setEvaluatedEvaluator":                                part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.setEvaluatedTS":                                       part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.setEvaluatedTSWithoutEvaluator":                       part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"baseProcessor.setEvaluatedValues":                                   part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"getTSFromExternalTS":                                                part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"getTsEvaluatorFromReadStaleness":                                    part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"parseAndValidateAsOf":                                               part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"staleReadProcessor.OnExecutePreparedStmt":                           part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"staleReadProcessor.OnSelectTable":                                   part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"staleReadProcessor.evaluateFromStmtTSOrSysVariable":                 part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"staleReadProcessor.evaluateFromTxn":                                 part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"NewStalenessTxnContextProvider":                                     byUnitTests,
		"StalenessTxnContextProvider.ActivateTxn":                            part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.AdviseOptimizeWithPlan":                 byUnitTests,
		"StalenessTxnContextProvider.AdviseWarmup":                           byUnitTests,
		"StalenessTxnContextProvider.GetReadReplicaScope":                    byUnitTests,
		"StalenessTxnContextProvider.GetSnapshotWithStmtForUpdateTS":         writePath,
		"StalenessTxnContextProvider.GetSnapshotWithStmtReadTS":              part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.GetStmtForUpdateTS":                     byUnitTests,
		"StalenessTxnContextProvider.GetStmtReadTS":                          byUnitTests,
		"StalenessTxnContextProvider.GetTxnInfoSchema":                       byUnitTests,
		"StalenessTxnContextProvider.GetTxnScope":                            byUnitTests,
		"StalenessTxnContextProvider.OnInitialize":                           part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.OnLocalTemporaryTableCreated":           "temporary tables: a blocked-by:D06.4 stub (D06.4 part 2)",
		"StalenessTxnContextProvider.OnPessimisticStmtEnd":                   writePath,
		"StalenessTxnContextProvider.OnPessimisticStmtStart":                 writePath,
		"StalenessTxnContextProvider.OnStmtCommit":                           byUnitTests,
		"StalenessTxnContextProvider.OnStmtErrorForNextAction":               part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.OnStmtRetry":                            part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.OnStmtRollback":                         byUnitTests,
		"StalenessTxnContextProvider.OnStmtStart":                            part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.SetOptionsBeforeCommit":                 writePath,
		"StalenessTxnContextProvider.activateStaleTxn":                       part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"StalenessTxnContextProvider.enterNewStaleTxnWithReplaceProvider":    part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"CalculateAsOfTsExpr":                                                part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"CalculateTsWithReadStaleness":                                       part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"GetExternalTimestamp":                                               part2("stale read (needs the domain's InfoSchema at a ts, D06.5)"),
		"IsStmtStaleness":                                                    byUnitTests,
	}
}

func sessiontxnGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys/action", "action", "error", "boom")
	g.Add("sys/action", "action", "retry", "")
	g.Add("sys/action", "action", "noidea", "")
	for _, ts := range []uint64{0, 1, 449_000_000_000_000_000, 1<<63 - 1, 1<<64 - 1} {
		g.Add("sys/constant_future", "constant_future", ts)
	}
	for i := 0; i < 10; i++ {
		g.Add("rand/constant_future", "constant_future", rng.Uint64())
	}
	modes := []string{"optimistic", "rr", "rc"}
	for i := 0; i < 360; i++ {
		cfg := map[string]any{
			"mode":       modes[rng.IntN(3)],
			"autocommit": rng.IntN(3) > 0,
			"low_res":    rng.IntN(6) == 0,
			"snapshot":   rng.IntN(8) == 0,
		}
		var steps []string
		inTxn := false
		for k := 0; k < 3+rng.IntN(8); k++ {
			switch r := rng.IntN(10); {
			case r == 0 && !inTxn:
				steps = append(steps, "begin")
				inTxn = true
			case r == 1 && inTxn:
				steps = append(steps, "commit")
				inTxn = false
			case r < 5:
				steps = append(steps, "point")
			default:
				steps = append(steps, "read")
			}
		}
		g.Add(fmt.Sprintf("rand/read_ts/%d", i), "read_ts", cfg, steps)
	}
}

// countingOracle counts the TSO requests of the providers.
type countingOracle struct {
	oracle.Oracle
	tso, lowRes atomic.Int64
}

func (o *countingOracle) GetTimestampAsync(ctx context.Context, opt *oracle.Option) oracle.Future {
	o.tso.Add(1)
	return o.Oracle.GetTimestampAsync(ctx, opt)
}

func (o *countingOracle) GetLowResolutionTimestampAsync(ctx context.Context, opt *oracle.Option) oracle.Future {
	o.lowRes.Add(1)
	return o.Oracle.GetLowResolutionTimestampAsync(ctx, opt)
}

type countingStore struct {
	kv.Storage
	o *countingOracle
}

func (s *countingStore) GetOracle() oracle.Oracle { return s.o }

var sessiontxnStore kv.Storage

func sessiontxnReadTS(cfg map[string]any, steps []string) (any, error) {
	if sessiontxnStore == nil {
		st, err := mockstore.NewMockStore()
		if err != nil {
			return nil, err
		}
		sessiontxnStore = st
	}
	o := &countingOracle{Oracle: sessiontxnStore.GetOracle()}
	ctx := context.Background()
	var snapshot uint64
	if cfg["snapshot"].(bool) {
		ver, err := sessiontxnStore.CurrentVersion(kv.GlobalTxnScope)
		if err != nil {
			return nil, err
		}
		snapshot = ver.Ver
	}
	// A transaction ends with a fresh mock session (the session's LazyTxn is reset at the
	// end of a transaction; mock.Context keeps a never-activated MaxTS future).
	var sctx *mock.Context
	var vars *variable.SessionVars
	newSctx := func() error {
		sctx = mock.NewContext()
		sctx.Store = &countingStore{Storage: sessiontxnStore, o: o}
		vars = sctx.GetSessionVars()
		vars.ConnectionID = 1 // a user session (UseLowResolutionTSO, NeedSetRCCheckTSFlag)
		vars.SetStatusFlag(mysql.ServerStatusAutocommit, cfg["autocommit"].(bool))
		if cfg["low_res"].(bool) {
			if err := vars.SetSystemVar(variable.TiDBLowResolutionTSO, "ON"); err != nil {
				return err
			}
		}
		vars.SnapshotTS = snapshot
		return nil
	}
	endTxn := func() error {
		if txn, _ := sctx.Txn(false); txn != nil && txn.Valid() {
			if err := sctx.CommitTxn(ctx); err != nil {
				return err
			}
		}
		return newSctx()
	}
	if err := newSctx(); err != nil {
		return nil, err
	}
	newProvider := func(explicit bool) sessiontxn.TxnContextProvider {
		mode := cfg["mode"].(string)
		if !explicit && vars.IsAutocommit() {
			mode = "optimistic"
		}
		switch mode {
		case "rr":
			return isolation.NewPessimisticRRTxnContextProvider(sctx, false)
		case "rc":
			return isolation.NewPessimisticRCTxnContextProvider(sctx, false)
		}
		p := &isolation.OptimisticTxnContextProvider{}
		p.ResetForNewTxn(sctx, false)
		return p
	}
	var p sessiontxn.TxnContextProvider
	var out []any
	for _, step := range steps {
		switch step {
		case "begin":
			p = newProvider(true)
			if err := p.OnInitialize(ctx, sessiontxn.EnterNewTxnWithBeginStmt); err != nil {
				return nil, err
			}
		case "commit":
			if err := endTxn(); err != nil {
				return nil, err
			}
			p = nil
		default:
			if p == nil {
				p = newProvider(false)
				if err := p.OnInitialize(ctx, sessiontxn.EnterNewTxnBeforeStmt); err != nil {
					return nil, err
				}
			}
			tso0, low0 := o.tso.Load(), o.lowRes.Load()
			if err := p.OnStmtStart(ctx, nil); err != nil {
				return nil, err
			}
			if step == "point" {
				plan := &plannercore.PointGetPlan{TblInfo: &model.TableInfo{}}
				if err := p.AdviseOptimizeWithPlan(plan); err != nil {
					return nil, err
				}
			}
			if err := p.AdviseWarmup(); err != nil {
				return nil, err
			}
			ts, err := p.GetStmtReadTS()
			if err != nil {
				return nil, err
			}
			reqs := (o.tso.Load() - tso0) + (o.lowRes.Load() - low0)
			src := "TxnStart"
			switch {
			case ts == math.MaxUint64:
				src = "MaxTs"
			case snapshot != 0 && ts == snapshot:
				src = "Snapshot"
			case reqs > 0 && vars.UseLowResolutionTSO():
				src = "LowResolution"
			case reqs > 0:
				src = "Tso"
			}
			out = append(out, []any{src, reqs})
			// An autocommit statement outside a transaction ends its transaction.
			if !vars.InTxn() {
				if err := endTxn(); err != nil {
					return nil, err
				}
				p = nil
			}
		}
	}
	return out, nil
}

func sessiontxnExec(op string, args []json.RawMessage) (out any, err error) {
	switch op {
	case "action":
		var a sessiontxn.StmtErrorAction
		var e error
		switch arg[string](args, 0) {
		case "error":
			a, e = sessiontxn.ErrorAction(errors.New(arg[string](args, 1)))
		case "retry":
			a, e = sessiontxn.RetryReady()
		default:
			a, e = sessiontxn.NoIdea()
		}
		var msg any
		if e != nil {
			msg = e.Error()
		}
		return []any{int(a), msg}, nil
	case "constant_future":
		return sessiontxn.ConstantFuture(arg[uint64](args, 0)).Wait()
	case "read_ts":
		return sessiontxnReadTS(arg[map[string]any](args, 0), arg[[]string](args, 1))
	}
	return nil, fmt.Errorf("unknown op %q", op)
}
