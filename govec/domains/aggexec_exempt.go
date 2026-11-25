// The D12.3 functions the aggexec domain does not cover (from data/func_blocks.tsv and tidb-rs
// specs/D12.3-agg-executors/spec.md §2, the parts of the block), and the function of the
// vecgroupchecker file (D12.0) no record reaches.

package domains

// aggexecExempt: the functions no record covers, with the reason.
func aggexecExempt() map[string]string {
	c := "D12.3 part C (spilling and restoring of the parallel hash aggregation): not yet ported, blocked by the disk chunk store (D01.10) (tidb-rs D12.3 spec §2.3)"
	closeBase := "runs only when OpenSelf panics (Go recovers the panic, closes the children and panics again); tidb-rs returns the error instead (M-PANIC); ported, covered by tidb-rs unit tests (crates/executor/src/aggregate/tests.rs, open_self_error_closes_the_children)"
	groupCount := "only Go's tests call it; ported, covered by tidb-rs unit tests (crates/executor/src/internal/vecgroupchecker/tests.rs)"
	spillAction := "memory soft-limit spill action / spill-mode path (only triggers when session memory quota is exceeded; Go mock context has no soft-limit spill in govec): ported, covered by tidb-rs unit tests (crates/executor/src/aggregate/tests.rs, hash_agg_unparallel_spill_action_and_boundary and hash_agg_parallel_spill_boundary_and_fallback)"
	workerErr := "runs only when a partial worker hits an evaluation/memory error or panic during parallel execution: ported, covered by tidb-rs unit tests (crates/executor/src/aggregate/tests.rs, hash_agg_parallel_error_panic_quota_and_close_stats)"
	return map[string]string{
		"AggSpillDiskAction.Action":                         spillAction,
		"AggSpillDiskAction.GetPriority":                    spillAction,
		"HashAggExec.ActionSpill":                           spillAction,
		"HashAggExec.actionSpillForParallel":                spillAction,
		"HashAggExec.actionSpillForUnparallel":              spillAction,
		"HashAggExec.spill":                                 c,
		"HashAggExec.spillUnprocessedData":                  spillAction,
		"HashAggFinalWorker.getInputFromDisk":               c,
		"HashAggFinalWorker.restoreDataFromDisk":            c,
		"HashAggPartialWorker.prepareForSpill":              c,
		"HashAggPartialWorker.processError":                 workerErr,
		"HashAggPartialWorker.spillDataToDisk":              c,
		"HashAggPartialWorker.spillDataToDiskImpl":          c,
		"HashAggPartialWorker.spillRemainingDataToDisk":     c,
		"ParallelAggSpillDiskAction.Action":                 spillAction,
		"ParallelAggSpillDiskAction.GetPriority":            spillAction,
		"ParallelAggSpillDiskAction.actionImpl":             spillAction,
		"closeBaseExecutor":                                 closeBase,
		"hasEnoughDataToSpill":                              spillAction,
		"parallelHashAggSpillHelper.addListInDisks":         c,
		"parallelHashAggSpillHelper.getListInDisks":         c,
		"parallelHashAggSpillHelper.getNextPartition":       c,
		"parallelHashAggSpillHelper.processRow":             c,
		"parallelHashAggSpillHelper.restoreFromOneSpillFile": c,
		"parallelHashAggSpillHelper.restoreOnePartition":    c,
		"parallelHashAggSpillHelper.setError":               workerErr,
		"parallelHashAggSpillHelper.setInSpilling":          c,
		"parallelHashAggSpillHelper.setNeedSpill":           spillAction,
		"parallelHashAggSpillHelper.setSpillTriggered":      c,
		"parallelHashAggSpillHelper.waitForTheEndOfSpill":   spillAction,
		"recoveryHashAgg":                                   workerErr,
		aeVGC + ":VecGroupChecker.GroupCount":               groupCount,
	}
}
