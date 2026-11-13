package domains

// P1 close-out (tidb-rs#48): D08.1 / D08.2 / D08.12 functions that the existing cast records already
// execute in Go (checked with go test -coverpkg, one op at a time) but that were listed as
// exempt "until the first signatures land". This file's init runs after the files that
// create these ops (file order: cast_zp1close.go sorts last).

func init() {
	for op, fs := range map[string][]string{
		"cast": {
			// D08.1: ScalarFunction's row evaluators dispatch to the signature's drivers.
			"ScalarFunction.Eval", "ScalarFunction.EvalInt", "ScalarFunction.EvalReal", "ScalarFunction.EvalString",
			"ScalarFunction.EvalDecimal", "ScalarFunction.EvalTime", "ScalarFunction.EvalDuration",
			"ScalarFunction.EvalJSON", "ScalarFunction.EvalVectorFloat32",
			// D08.1: context.go / errors.go helpers of the CAST signatures.
			"errCtx", "location", "sqlMode", "handleInvalidTimeError", "handleAllowedPacketOverflowed",
			// D08.2: baseBuiltinFunc accessors read by every signature.
			"baseBuiltinFunc.getRetTp", "baseBuiltinFunc.vectorized", "baseBuiltinFunc.isChildrenVectorized",
			// D08.12: the statement time of the CAST AS TIME signatures (helper.go, P1 close-out).
			"getStmtTimestamp",
		},
		"arbuild": {"handleDivisionByZeroError", "truncateWarnings", "warningCount"},
		"evexpr": {"ScalarFunction.VecEvalInt", "ScalarFunction.VecEvalReal", "ScalarFunction.VecEvalString",
			"ScalarFunction.VecEvalDecimal", "ScalarFunction.VecEvalTime", "ScalarFunction.VecEvalDuration",
			"ScalarFunction.VecEvalJSON"},
		"vecbuild":  {"ScalarFunction.VecEvalVectorFloat32"},
		"util":      {"baseBuiltinFunc.equal"},
		"mathclone": {"baseBuiltinFunc.cloneFrom"},
		"evnull":    {"isNullHandler", "pkg/expression/constant_fold.go:init"},
	} {
		c, ok := castCoverage[op]
		if !ok {
			panic("cast_zp1close.go: no op " + op)
		}
		c.GoFuncs = append(c.GoFuncs, fs...)
		castCoverage[op] = c
	}
}
