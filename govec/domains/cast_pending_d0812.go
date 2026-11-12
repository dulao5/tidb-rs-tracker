// Block D08.12 functions not covered by the "util" records of batch 1, with the reason
// (generated from data/func_blocks.tsv). Shrinks as later batches land.

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
	return map[string]string{
		"Column.ColumnExplainInfo":                    "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Column.ColumnExplainInfoNormalized":          "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Column.ExplainInfo":                          "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Column.ExplainNormalizedInfo":                "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Column.ExplainNormalizedInfo4InList":         "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Constant.ExplainInfo":                        "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Constant.ExplainNormalizedInfo":              "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Constant.ExplainNormalizedInfo4InList":       "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"Constant.format":                             "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"ConstructPositionExpr":                       "D08.12 batch 2: parser driver ParamMarkerExpr / ast.PositionExpr (ORDER BY ?)",
		"ExecBinaryParam":                             "D08.12 batch 2: binary protocol parameters (COM_STMT_EXECUTE), with the protocol layer D03.1",
		"ExplainColumnList":                           "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"ExplainExpressionList":                       "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"GetTimeCurrentTimestamp":                     "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"GetTimeValue":                                "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"IsValidCurrentTimestampExpr":                 "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"NewSQLDigestTextRetriever":                   "needs an SQLExecutor over statements_summary (tidb_decode_sql_digests, D08.9 / D14.2)",
		"ParamMarkerExpression":                       "D08.12 batch 2: needs the parser driver's ParamMarkerExpr (plan cache / prepared statements)",
		"ParamMarkerInPrepareChecker.Enter":           "D08.12 batch 2: AST visitor over the parser driver's ParamMarkerExpr",
		"ParamMarkerInPrepareChecker.Leave":           "D08.12 batch 2: AST visitor over the parser driver's ParamMarkerExpr",
		"PopRowFirstArg":                              "needs ROW() (builtin_other.go, D08.9); GetRowLen / CheckArgsNotMultiColumnRow are covered",
		"PosFromPositionExpr":                         "D08.12 batch 2: parser driver ParamMarkerExpr / ast.PositionExpr (ORDER BY ?)",
		"SQLDigestTextRetriever.RetrieveGlobal":       "needs an SQLExecutor over statements_summary (D08.9 / D14.2)",
		"SQLDigestTextRetriever.RetrieveLocal":        "needs an SQLExecutor over statements_summary (D08.9 / D14.2)",
		"SQLDigestTextRetriever.runFetchDigestQuery":  "needs an SQLExecutor over statements_summary (D08.9 / D14.2)",
		"SQLDigestTextRetriever.runMockQuery":         "needs an SQLExecutor over statements_summary (D08.9 / D14.2)",
		"SQLDigestTextRetriever.updateDigestInfo":     "needs an SQLExecutor over statements_summary (D08.9 / D14.2)",
		"ScalarFunction.ExplainInfo":                  "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"ScalarFunction.ExplainNormalizedInfo":        "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"ScalarFunction.ExplainNormalizedInfo4InList": "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"ScalarFunction.explainInfo":                  "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"SortedExplainExpressionList":                 "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"SortedExplainExpressionListIgnoreInlist":     "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"SortedExplainNormalizedExpressionList":       "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"SortedExplainNormalizedScalarFuncList":       "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"SubstituteCorCol2Constant":                   "D08.12 batch 2: reads CorrelatedColumn.Data, which tidb-rs keeps in the EvalState (D08.1 spec §3.2); the planner caller decides the signature",
		"binaryDate":                                  "D08.12 batch 2: binary protocol parameters, with ExecBinaryParam",
		"binaryDateTime":                              "D08.12 batch 2: binary protocol parameters, with ExecBinaryParam",
		"binaryDuration":                              "D08.12 batch 2: binary protocol parameters, with ExecBinaryParam",
		"binaryDurationWithMS":                        "D08.12 batch 2: binary protocol parameters, with ExecBinaryParam",
		"binaryTimestamp":                             "D08.12 batch 2: binary protocol parameters, with ExecBinaryParam",
		"binaryTimestampWithTZ":                       "D08.12 batch 2: binary protocol parameters, with ExecBinaryParam",
		"boolToInt64":                                 "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"checkPrivileges":                             "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFnContext.ActiveRoles":              "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFnContext.ConnectionInfo":           "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFnContext.CurrentDB":                "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFnContext.EvalArgs":                 "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFnContext.User":                     "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFuncClass.getFunction":              "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFuncSig.Clone":                      "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFuncSig.RequiredOptionalEvalProps":  "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFuncSig.evalInt":                    "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"extensionFuncSig.evalString":                 "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"getStmtTimestamp":                            "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"getTimeCurrentTimeStamp":                     "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"locateStringWithCollation":                   "helper of LOCATE / INSTR (string functions, D08.6)",
		"newExtensionFnContext":                       "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"newExtensionFuncClass":                       "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"pickRandomLocationForTest":                   "D08.12 batch 2 (current-time helpers used by the time functions, D08.7), specs/D08.12-util/plan.md",
		"registerExtensionFunc":                       "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"removeExtensionFunc":                         "pkg/extension functions: tidb-rs does not support extensions (D08.2 spec §4, GetBuiltinList)",
		"sortedExplainExpressionList":                 "D08.12 batch 2 (EXPLAIN printing of expressions), specs/D08.12-util/plan.md",
		"timeZone2int":                                "helper of the time functions (D08.7)",
	}
}
