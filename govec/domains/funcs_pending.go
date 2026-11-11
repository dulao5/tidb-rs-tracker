// Block D08.2 functions no op of the funcs domain covers, with the reason. Shrinks as the
// parts of D08.2 land.

package domains

func funcsExempt() map[string]string {
	m := map[string]string{
		"baseBuiltinFunc.getArgs":              "covered by the expr domain (sf records, D08.1 part 2a)",
		"baseBuiltinFunc.getRetTp":             "covered by the expr domain (sf records, D08.1 part 2a)",
		"baseBuiltinFunc.equal":                "covered by the expr domain (sf/sfpair records, D08.1 part 2a)",
		"baseBuiltinFunc.cloneFrom":            "covered by the expr domain (sf records, D08.1 part 2a)",
		"baseBuiltinFunc.vectorized":           "covered by the expr domain (sf records, D08.1 part 2a)",
		"baseBuiltinFunc.isChildrenVectorized": "covered by the expr domain (sf records, D08.1 part 2a)",
		"baseBuiltinFunc.MemoryUsage":          "covered by the expr domain (sf records, D08.1 part 2a)",
		"baseBuiltinFunc.Clone":                "only panics; never reached (every signature has its own Clone)",
		"deriveCoercibilityForScalarFunc":      "only panics; never called (ScalarFunction's coercibility is set when built)",
		"builtinFuncCache.getCache":            "generic method (no linkname); Rust unit test; exercised by the LIKE/REGEXP records when those families land",
		"builtinFuncCache.getOrInitCache":      "generic method (no linkname); Rust unit test; exercised by the LIKE/REGEXP records when those families land",
		"collationInfo.Hash64":                 "cascades only (base.HashEquals, EXC.B)",
		"collationInfo.Equals":                 "cascades only (base.HashEquals, EXC.B)",
		"baseBuiltinCastFunc.metadata":         "D08.2 part 2: the CAST signatures' base (D08.8)",
		"baseBuiltinCastFunc.cloneFrom":        "D08.2 part 2: the CAST signatures' base (D08.8)",
		"newBaseBuiltinCastFunc":               "D08.2 part 2: the CAST signatures' base (D08.8)",
		"funcParam.setStrVal":                  "vecEval helper of REGEXP/ILIKE (M-EVAL): the Rust vector driver evaluates the arguments; ported with those families",
		"funcParam.setIntVal":                  "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"funcParam.setCol":                     "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"funcParam.getCol":                     "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"funcParam.getStringVal":               "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"funcParam.getIntVal":                  "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"buildStringParam":                     "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"buildIntParam":                        "vecEval helper of REGEXP/ILIKE (M-EVAL)",
		"ParseSimpleExprWithTableInfo":         "needs the planner's expression rewriter (D09.2, P3)",
		"ParseSimpleExpr":                      "needs the planner's expression rewriter (D09.2, P3)",
		"FindFieldName":                        "with ParseSimpleExpr (D09.2, P3)",
		"FindFieldNameIdxByColName":            "with ParseSimpleExpr (D09.2, P3)",
	}
	for _, f := range []string{"init", "FoldConstant", "isNullHandler", "ifFoldHandler", "ifNullFoldHandler",
		"caseWhenHandler", "foldConstant"} {
		m[f] = "D08.2 part 2: constant folding evaluates real signatures (NewFunction, D08.4-D08.9)"
	}
	for _, f := range []string{"basePropConstSolver.getColID", "basePropConstSolver.insertCol",
		"basePropConstSolver.tryToUpdateEQList", "ValidCompareConstantPredicate", "ValidCompareConstantPredicateHelper",
		"validEqualCond", "tryToReplaceCond", "propConstSolver.propagateConstantEQ", "propConstSolver.propagateColumnEQ",
		"propConstSolver.setConds2ConstFalse", "propConstSolver.pickNewEQConds", "propConstSolver.solve", "PropagateConstant",
		"propOuterJoinConstSolver.setConds2ConstFalse", "basePropConstSolver.dealWithPossibleHybridType",
		"propOuterJoinConstSolver.pickEQCondsOnOuterCol", "propOuterJoinConstSolver.pickNewEQConds",
		"propOuterJoinConstSolver.propagateConstantEQ", "propOuterJoinConstSolver.colsFromOuterAndInner",
		"propOuterJoinConstSolver.validColEqualCond", "propOuterJoinConstSolver.deriveConds",
		"propOuterJoinConstSolver.propagateColumnEQ", "propOuterJoinConstSolver.solve", "propagateConstantDNF",
		"PropConstOverOuterJoin", "newPropConstSolver", "propConstSolver.PropagateConstant"} {
		m[f] = "D08.2 part 2: constant propagation builds and folds real signatures (NewFunction, D08.4)"
	}
	for _, f := range []string{"NewGroupingSets", "GroupingSets.Merge", "GroupingSets.MergeOne", "GroupingSets.TargetOne",
		"GroupingSets.NeedCloneColumn", "GroupingSet.IsEmpty", "GroupingSet.AllColIDs", "GroupingSet.ExtractCols",
		"GroupingSet.Clone", "GroupingSet.StringWithCtx", "GroupingSet.MemoryUsage", "GroupingSet.ToPB",
		"GroupingSets.IsEmpty", "GroupingSets.AllSetsColIDs", "GroupingSets.StringWithCtx", "GroupingSets.ToPB",
		"newGroupingSet", "GroupingExprs.IsEmpty", "GroupingExprs.SubSetOf", "GroupingExprs.IDSet", "GroupingExprs.Clone",
		"GroupingExprs.StringWithCtx", "GroupingExprs.MemoryUsage", "RollupGroupingSets", "AdjustNullabilityFromGroupingSets",
		"DeduplicateGbyExpression", "RestoreGbyExpression", "GroupingSets.DistinctSize", "GroupingSets.DistinctSizeWithThreshold"} {
		m[f] = "D08.2 part 3: grouping sets (Expand / ROLLUP), with D08.3 (ToPB) and the GROUPING function (D08.9)"
	}
	return m
}
