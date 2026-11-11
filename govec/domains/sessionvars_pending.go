// Code generated from data/func_blocks.tsv (block D05.2). Shrinks as D05.2 T3-T7 land.

package domains

// sessionvarsPending: D05.2 functions not yet covered by the sessionvars domain
// (specs/D05.2-sessionvars/plan.md in tidb-rs).
var sessionvarsPending = []string{
	"NewTracker",
	"NewTxnReadTS",
	"PartitionPruneMode.Update",
	"PartitionPruneMode.Valid",
	"RuntimeFilterModeStringToMode",
	"RuntimeFilterTypeStringToType",
	"SessionVars.ChooseMppVersion",
	// Read the clock (time.Since); the Rust port takes `now` (unit test).
	"SessionVars.GetExecuteDuration",
	"SessionVars.GetTotalCostDuration",
	"SessionVars.IsAutocommit",
	"SessionVars.SetReplicaRead",
	"SessionVars.Status",
	"ToRuntimeFilterType",
	"TransactionContext.AddSavepoint",
	"TransactionContext.DeleteSavepoint",
	"TransactionContext.GetCurrentSavepoint",
	"TransactionContext.ReleaseSavepoint",
	"TransactionContext.RestoreBySavepoint",
	"TransactionContext.RollbackToSavepoint",
	"TxnReadTS.PeakTxnReadTS",
	"ValidTiFlashPreAggMode",
	"cursorHandle.Close",
	"cursorHandle.GetState",
	"cursorHandle.ID",
	"cursorTracker.GetCursor",
	"cursorTracker.NewCursor",
	"cursorTracker.RangeCursor",
	"cursorTracker.remove",
	"parseTSFromNumberOrTime",
	"setReadStaleness",
	"setSnapshotTS",
	"setTiFlashComputeDispatchPolicy",
	"setTxnReadTS",
	"validateReadConsistencyLevel",
}
