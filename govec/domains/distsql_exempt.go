package domains

func distsqlExempt() map[string]string {
	ex := map[string]string{
		"WithSQLKvExecCounterInterceptor": "ported as L2 no-op (D07.3 spec §7 item 6): Top SQL is not recorded and client-rust has no RPC interceptor; covered by tidb-rs crates/store/src/distsql/tests.rs",
		"pkg/distsql/distsql.go:init":     "not ported (D07.3 spec §3.3): Rust get_system_endian uses compile-time cfg(target_endian), no unsafe runtime init",
	}

	// DEF.MPP (10 functions, D07.3 spec §3.3)
	for _, f := range []string{
		"GenSelectResultFromMPPResponse",
		"SetTiFlashConfVarsInContext",
		"FillDummySummariesForTiFlashTasks",
		"recordExecutionSummariesForTiFlashTasks",
		"RequestBuilder.SetPartitionIDAndRanges",
		"NewSerialSelectResults",
		"serialSelectResults.NextRaw",
		"serialSelectResults.Next",
		"serialSelectResults.IntoIter",
		"serialSelectResults.Close",
	} {
		ex[f] = "not ported (DEF.MPP, D07.3 spec §3.3): TiFlash / MPP path is deferred out of the read-only TiKV scope"
	}

	// Delegated / non-read-only / Admin / Checksum / BR / Lightning (10 functions, D07.3 spec §3.3)
	for _, f := range []string{
		"Analyze",
		"RequestBuilder.SetAnalyzeRequest",
		"Checksum",
		"RequestBuilder.SetChecksumRequest",
		"RequestBuilder.SetIndexRanges",
		"RequestBuilder.SetWrappedKeyRanges",
		"RequestBuilder.SetIndexRangesForTables",
		"IndexRangesToKVRangesForTables",
		"BuildTableRanges",
		"appendRanges",
	} {
		ex[f] = "not ported (X / D12.14, D07.3 spec §3.3): ANALYZE / CHECKSUM / ADMIN / BR / Lightning paths are outside the read-only SQL scope"
	}

	// D07.3 Part 2 (31 functions, D07.3 spec §3.2)
	for _, f := range []string{
		"RequestBuilder.SetTableHandles",
		"RequestBuilder.SetPartitionsAndHandles",
		"TableHandlesToKVRanges",
		"PartitionHandlesToKVRanges",
		"IndexRangesToKVRanges",
		"IndexRangesToKVRangesWithInterruptSignal",
		"indexRangesToKVRangesForTablesWithInterruptSignal",
		"indexRangesToKVWithoutSplit",
		"chunkRowHeap.Len",
		"chunkRowHeap.Less",
		"chunkRowHeap.Swap",
		"chunkRowHeap.Push",
		"chunkRowHeap.Pop",
		"NewSortedSelectResults",
		"sortedSelectResults.updateCachedChunk",
		"sortedSelectResults.initCompareFuncs",
		"sortedSelectResults.buildKeyColumns",
		"sortedSelectResults.lessRow",
		"sortedSelectResults.NextRaw",
		"sortedSelectResults.Next",
		"sortedSelectResults.IntoIter",
		"sortedSelectResults.Close",
		"selectResult.IntoIter",
		"newSelRespChannelIter",
		"selRespChannelIter.Channel",
		"selRespChannelIter.Next",
		"selRespChannelIter.nextChunk",
		"selRespChannelIter.fillChunkFromDefault",
		"newSelectResultIter",
		"selectResultIter.Next",
		"selectResultIter.Close",
	} {
		ex[f] = "D07.3 part 2 (D07.3 spec §3.2): index/handle encoding, sortedSelectResults, and SelectResultIter"
	}

	// D00.3 leaf utility pkg/util/ppcpuusage/cpuusages.go (7 functions, ported whole with D07.3 part 1)
	for _, f := range []string{
		"pkg/util/ppcpuusage/cpuusages.go:CPUUsages.Reset",
		"pkg/util/ppcpuusage/cpuusages.go:SQLCPUUsages.SetCPUUsages",
		"pkg/util/ppcpuusage/cpuusages.go:SQLCPUUsages.MergeTidbCPUTime",
		"pkg/util/ppcpuusage/cpuusages.go:SQLCPUUsages.MergeTikvCPUTime",
		"pkg/util/ppcpuusage/cpuusages.go:SQLCPUUsages.GetCPUUsages",
		"pkg/util/ppcpuusage/cpuusages.go:SQLCPUUsages.AllocNewSQLID",
		"pkg/util/ppcpuusage/cpuusages.go:SQLCPUUsages.ResetCPUTimes",
	} {
		ex[f] = "ported (tidb-rs crates/execdetails/src/ppcpuusage.rs, D07.3 spec §2.2): mutex-guarded CPU usage accumulator; covered by unit tests (cpu_usages_lifecycle)"
	}

	// D00.3 leaf utility pkg/util/resourcegrouptag/resource_group_tag.go (3 remaining functions; GetResourceGroupLabelByKey is in request_builder)
	for _, f := range []string{
		"pkg/util/resourcegrouptag/resource_group_tag.go:EncodeResourceGroupTag",
		"pkg/util/resourcegrouptag/resource_group_tag.go:DecodeResourceGroupTag",
		"pkg/util/resourcegrouptag/resource_group_tag.go:GetFirstKeyFromRequest",
	} {
		ex[f] = "ported (tidb-rs crates/store/src/util/resourcegrouptag.rs, D07.3 spec §2.2, #416): proto tag encode/decode and first-key extractor; covered by unit tests"
	}

	return ex
}
