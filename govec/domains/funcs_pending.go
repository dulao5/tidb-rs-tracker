// Block D08.2 functions no op of any domain covers, with the reason. The block is closed
// (data/blocks_done.tsv): each entry is exempt for good, ported without a record of its own
// (Rust unit tests), or owned by another block and listed in that block's tasks.md in
// tidb-rs.

package domains

func funcsExempt() map[string]string {
	const (
		toString = "owned by D08.6: the argument helper of REGEXP_* / ILIKE (builtin_regexp.go, " +
			"builtin_ilike_vec.go), ported with them; listed in tidb-rs specs/D08.6-string-funcs/tasks.md"
		toRewriter = "owned by D09.2: parses an expression string through the planner's expression rewriter; " +
			"listed in tidb-rs specs/D09.2-rewriter/tasks.md"
		toGrouping = "owned by D09.4: grouping sets of ROLLUP / the Expand operator (PlanBuilder.buildExpand, " +
			"LogicalExpand); GroupingSet.ToPB is read by PhysicalExpand.ToPB (D10.9); listed in tidb-rs " +
			"specs/D09.4-agg-window/tasks.md"
		ctrlFold = "ported (constant_fold.rs): the folding handler of IF / IFNULL / CASE WHEN, which no record " +
			"reaches until those functions (D08.9) are registered"
		funcCache = "ported (builtin.rs, unit test builtin_unit.rs): generic method (no linkname into Go); its " +
			"users LIKE / ILIKE / REGEXP (D08.6) and JSON_SCHEMA_VALID (D08.10) record it with their families"
	)
	m := map[string]string{
		"baseBuiltinFunc.getArgs": "ported (eval/mod.rs): a field accessor; Go reads it while building CAST AS REAL " +
			"of DECIMAL and in the IF / IFNULL / CASE vector bodies (D08.9); no op reaches it",
		"baseBuiltinFunc.MemoryUsage": "ported (eval/mod.rs): executed by the expr domain's sf records through " +
			"ScalarFunction.MemoryUsage (go test -coverpkg); the expr domain is registered for its own block only",
		"baseBuiltinFunc.Clone":           "permanent: only panics; never reached (every signature has its own Clone)",
		"deriveCoercibilityForScalarFunc": "permanent: only panics; never called (ScalarFunction's coercibility is set when built)",
		"builtinFuncCache.getCache":       funcCache,
		"builtinFuncCache.getOrInitCache": funcCache,
		"collationInfo.Hash64":            "permanent: cascades only (base.HashEquals, EXC.B)",
		"collationInfo.Equals":            "permanent: cascades only (base.HashEquals, EXC.B)",
		"funcParam.setStrVal":             toString,
		"funcParam.setIntVal":             toString,
		"funcParam.setCol":                toString,
		"funcParam.getCol":                toString,
		"funcParam.getStringVal":          toString,
		"funcParam.getIntVal":             toString,
		"buildStringParam":                toString,
		"buildIntParam":                   toString,
		"ParseSimpleExprWithTableInfo":    toRewriter,
		"ParseSimpleExpr":                 toRewriter,
		"FindFieldName":                   toRewriter,
		"FindFieldNameIdxByColName":       toRewriter,
		"ifFoldHandler":                   ctrlFold,
		"ifNullFoldHandler":               ctrlFold,
		"caseWhenHandler":                 ctrlFold,
	}
	for _, f := range []string{"NewGroupingSets", "GroupingSets.Merge", "GroupingSets.MergeOne", "GroupingSets.TargetOne",
		"GroupingSets.NeedCloneColumn", "GroupingSet.IsEmpty", "GroupingSet.AllColIDs", "GroupingSet.ExtractCols",
		"GroupingSet.Clone", "GroupingSet.StringWithCtx", "GroupingSet.MemoryUsage", "GroupingSet.ToPB",
		"GroupingSets.IsEmpty", "GroupingSets.AllSetsColIDs", "GroupingSets.StringWithCtx", "GroupingSets.ToPB",
		"newGroupingSet", "GroupingExprs.IsEmpty", "GroupingExprs.SubSetOf", "GroupingExprs.IDSet", "GroupingExprs.Clone",
		"GroupingExprs.StringWithCtx", "GroupingExprs.MemoryUsage", "RollupGroupingSets", "AdjustNullabilityFromGroupingSets",
		"DeduplicateGbyExpression", "RestoreGbyExpression", "GroupingSets.DistinctSize", "GroupingSets.DistinctSizeWithThreshold"} {
		m[f] = toGrouping
	}
	return m
}
