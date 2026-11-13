// Block D08.12 functions no op covers, with the reason (generated from data/func_blocks.tsv,
// then edited). The block is closed (data/blocks_done.tsv): each entry is exempt for good,
// ported without a record of its own (Rust unit tests), or owned by another block and listed
// in that block's tasks.md in tidb-rs.

package domains

func init() {
	// D08.12 functions ported with earlier blocks: exercised by their records. This file's
	// init runs after cast_d082c.go / cast_d084.go, which set these coverages.
	for op, fs := range utEarlierCovered() {
		c := castCoverage[op]
		c.GoFuncs = append(c.GoFuncs, fs...)
		castCoverage[op] = c
	}
}

func castExemptD0812() map[string]string {
	const (
		toExplain = "owned by D10.9: EXPLAIN / plan-digest printing of expressions, called by the plans' " +
			"ExplainInfo (planner/core/explain.go); listed in tidb-rs specs/D10.9-postprocess/tasks.md"
		toBinaryParam = "owned by D03.1: decodes the binary-protocol parameters of COM_STMT_EXECUTE " +
			"(server/conn_stmt.go parseBinaryParams); listed in tidb-rs specs/D03.1-server/tasks.md"
		toPosition = "owned by D09.4: GROUP BY / ORDER BY position items (ast.PositionExpr with a parser-driver " +
			"ParamMarkerExpr: `ORDER BY ?`) resolved by the aggregation builder; listed in tidb-rs " +
			"specs/D09.4-agg-window/tasks.md"
		toPrepareChecker = "owned by D09.4: AST visitor over the parser driver's ParamMarkerExpr, used by the " +
			"window frame builder (buildWindowFunctionFrameBound); listed in tidb-rs specs/D09.4-agg-window/tasks.md"
		toDigestText = "owned by D08.9: the SQL-digest text retriever of TIDB_DECODE_SQL_DIGESTS (builtin_info.go; " +
			"also read by the infoschema reader, D12.13), needs an SQLExecutor over statements_summary; listed in " +
			"tidb-rs specs/D08.9-misc-funcs/tasks.md"
		extension = "permanent: pkg/extension functions; tidb-rs does not support extensions (D08.2 spec §4, " +
			"GetBuiltinList)"
		curTime = "ported (helper.rs, unit test helper_unit.rs): the statement's current time as a DATETIME / " +
			"TIMESTAMP / DATE value; its callers are GetTimeValue (column defaults, D06.4) and DDL, which no " +
			"expression op reaches"
	)
	m := map[string]string{
		"ConstructPositionExpr":             toPosition,
		"PosFromPositionExpr":               toPosition,
		"ExecBinaryParam":                   toBinaryParam,
		"binaryDate":                        toBinaryParam,
		"binaryDateTime":                    toBinaryParam,
		"binaryDuration":                    toBinaryParam,
		"binaryDurationWithMS":              toBinaryParam,
		"binaryTimestamp":                   toBinaryParam,
		"binaryTimestampWithTZ":             toBinaryParam,
		"ParamMarkerInPrepareChecker.Enter": toPrepareChecker,
		"ParamMarkerInPrepareChecker.Leave": toPrepareChecker,
		"ParamMarkerExpression": "owned by D09.2: builds the Constant of a parser-driver ParamMarkerExpr (`?` of " +
			"prepared statements) for the expression rewriter and point-get builder; listed in tidb-rs " +
			"specs/D09.2-rewriter/tasks.md",
		"SubstituteCorCol2Constant": "owned by D10.1: reads CorrelatedColumn.Data, which tidb-rs keeps in the " +
			"EvalState (D08.1 spec §3.2); its callers are the physical plans and the index-lookup executor " +
			"(D12.1), which decide where the values come from; listed in tidb-rs specs/D10.1-physical-plans/tasks.md",
		"NewSQLDigestTextRetriever":                  toDigestText,
		"SQLDigestTextRetriever.RetrieveGlobal":      toDigestText,
		"SQLDigestTextRetriever.RetrieveLocal":       toDigestText,
		"SQLDigestTextRetriever.runFetchDigestQuery": toDigestText,
		"SQLDigestTextRetriever.runMockQuery":        toDigestText,
		"SQLDigestTextRetriever.updateDigestInfo":    toDigestText,
		"PopRowFirstArg": "owned by D08.9: needs ROW() (builtin_other.go); GetRowLen / CheckArgsNotMultiColumnRow " +
			"are covered; listed in tidb-rs specs/D08.9-misc-funcs/tasks.md",
		"locateStringWithCollation": "owned by D08.6: the collation-aware search of LOCATE / INSTR " +
			"(builtin_string.go); listed in tidb-rs specs/D08.6-string-funcs/tasks.md",
		"timeZone2int": "owned by D08.7: the zone offset of the time functions (builtin_time.go); listed in " +
			"tidb-rs specs/D08.7-time-funcs/tasks.md",
		// helper.go
		"boolToInt64": "ported (helper.rs, unit test helper_unit.rs): the Go comparison / LIKE bodies call it; " +
			"the Rust signatures already return the i64 directly",
		"GetTimeCurrentTimestamp": curTime,
		"getTimeCurrentTimeStamp": curTime,
		"GetTimeValue": "owned by D06.4: the time value of a column default (table/column.go getColDefaultValue; " +
			"also the rewriter's DEFAULT handling, D09.2), needs the parser driver's ValueExpr and EvalSimpleAst; " +
			"listed in tidb-rs specs/D06.4-table/tasks.md",
		"IsValidCurrentTimestampExpr": "permanent: DDL only (ddl/add_column.go, modify_column.go: CURRENT_TIMESTAMP " +
			"defaults); DDL is served by Go TiDB",
		"pickRandomLocationForTest": "permanent: intest only (the random zone getStmtTimestamp applies in tests)",
		// extension.go
		"pkg/expression/extension.go:init": extension,
	}
	for _, f := range []string{"Column.ColumnExplainInfo", "Column.ColumnExplainInfoNormalized", "Column.ExplainInfo",
		"Column.ExplainNormalizedInfo", "Column.ExplainNormalizedInfo4InList", "Constant.ExplainInfo",
		"Constant.ExplainNormalizedInfo", "Constant.ExplainNormalizedInfo4InList", "Constant.format",
		"ExplainColumnList", "ExplainExpressionList", "ScalarFunction.ExplainInfo", "ScalarFunction.ExplainNormalizedInfo",
		"ScalarFunction.ExplainNormalizedInfo4InList", "ScalarFunction.explainInfo", "SortedExplainExpressionList",
		"SortedExplainExpressionListIgnoreInlist", "SortedExplainNormalizedExpressionList",
		"SortedExplainNormalizedScalarFuncList", "sortedExplainExpressionList"} {
		m[f] = toExplain
	}
	for _, f := range []string{"checkPrivileges", "extensionFnContext.ActiveRoles", "extensionFnContext.ConnectionInfo",
		"extensionFnContext.CurrentDB", "extensionFnContext.EvalArgs", "extensionFnContext.User",
		"extensionFuncClass.getFunction", "extensionFuncSig.Clone", "extensionFuncSig.RequiredOptionalEvalProps",
		"extensionFuncSig.evalInt", "extensionFuncSig.evalString", "newExtensionFnContext", "newExtensionFuncClass",
		"registerExtensionFunc", "removeExtensionFunc"} {
		m[f] = extension
	}
	return m
}
