// Block D08.5 functions without vectors after batch 5 (CONV, CRC32), with the reason.

package domains

func castExemptD085() map[string]string {
	return map[string]string{
		"builtinConvSig.vecEvalString": "unreachable in v8.5.8: builtinConvSig.vectorized() is false, so ScalarFunction.Vectorized() never calls isChildrenVectorized and bufAllocator stays nil; a direct VecEvalString nil-dereferences it (D08.5 spec §3.2). tidb-rs ports it as `novec` (the vector driver returns the baseBuiltinFunc error), so Rust has no untested vector path",
	}
}
