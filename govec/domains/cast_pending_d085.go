// Block D08.5 functions of later batches, with the reason: after batch 4 (the math functions)
// only CONV and CRC32 are left (specs/D08.5-arith/plan.md, handoff). Shrinks as the batches
// of D08.5 land (generated from data/func_blocks.tsv).

package domains

func castExemptD085() map[string]string {
	return map[string]string{
		"builtinCRC32Sig.Clone":          "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinCRC32Sig.evalInt":        "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinCRC32Sig.vecEvalInt":     "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinCRC32Sig.vectorized":     "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinConvSig.Clone":           "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinConvSig.conv":            "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinConvSig.evalString":      "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinConvSig.vecEvalString":   "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"builtinConvSig.vectorized":      "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"convFunctionClass.getFunction":  "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
		"crc32FunctionClass.getFunction": "D08.5 later batch (CONV / CRC32, handoff after batch 4), specs/D08.5-arith/plan.md",
	}
}
