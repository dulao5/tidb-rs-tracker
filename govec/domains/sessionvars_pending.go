// Block D05.2 functions no sessionvars op covers, with the reason (generated from
// data/func_blocks.tsv, then edited). The block is done (data/blocks_done.tsv): each entry is
// ported without a record of its own (Rust unit tests), or owned by another block and listed
// in that block's tasks.md in tidb-rs.

package domains

func sessionvarsExempt() map[string]string {
	const (
		toSession = "owned by D05.3: the cursor tracker of the session (pkg/session/cursor, server-side cursors " +
			"of COM_STMT_FETCH); listed in tidb-rs specs/D05.3-session/tasks.md"
		toStore = "owned by D07.1: savepoints restore the transaction's membuffer staging (store driver); listed " +
			"in tidb-rs specs/D07.1-store/tasks.md"
		hook  = "ported (crates/sessionctx/src/variable, unit test): a helper of the SET hooks; no sessionvars op lists it"
		state = "ported (crates/sessionctx/src/variable, unit test): session state no sessionvars op returns"
	)
	return map[string]string{
		"NewTracker":                             toSession,
		"cursorHandle.Close":                     toSession,
		"cursorHandle.GetState":                  toSession,
		"cursorHandle.ID":                        toSession,
		"cursorTracker.GetCursor":                toSession,
		"cursorTracker.NewCursor":                toSession,
		"cursorTracker.RangeCursor":              toSession,
		"cursorTracker.remove":                   toSession,
		"TransactionContext.AddSavepoint":        toStore,
		"TransactionContext.DeleteSavepoint":     toStore,
		"TransactionContext.GetCurrentSavepoint": toStore,
		"TransactionContext.ReleaseSavepoint":    toStore,
		"TransactionContext.RestoreBySavepoint":  toStore,
		"TransactionContext.RollbackToSavepoint": toStore,
		"NewTxnReadTS":                           state,
		"TxnReadTS.PeakTxnReadTS":                state,
		"SessionVars.ChooseMppVersion":           state,
		"SessionVars.IsAutocommit":               state,
		"SessionVars.SetReplicaRead":             state,
		"SessionVars.Status":                     state,
		// Read the clock (time.Since); the Rust port takes `now` (unit test).
		"SessionVars.GetExecuteDuration":   "ported (lazy_text.rs, unit test): reads the clock (time.Since); the Rust port takes `now`",
		"SessionVars.GetTotalCostDuration": "ported (lazy_text.rs, unit test): reads the clock (time.Since); the Rust port takes `now`",
		"PartitionPruneMode.Update":        hook,
		"PartitionPruneMode.Valid":         hook,
		"RuntimeFilterModeStringToMode":    hook,
		"RuntimeFilterTypeStringToType":    hook,
		"ToRuntimeFilterType":              hook,
		"ValidTiFlashPreAggMode":           hook,
		"validateReadConsistencyLevel":     hook,
		"setTiFlashComputeDispatchPolicy":  hook,
		"parseTSFromNumberOrTime":          hook,
		"setReadStaleness":                 hook,
		"setSnapshotTS":                    hook,
		"setTxnReadTS":                     hook,
	}
}
