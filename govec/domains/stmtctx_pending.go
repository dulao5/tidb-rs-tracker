// Code generated from data/func_blocks.tsv (block D00.1). Shrinks as D00.1 part 2 lands.

package domains

// stmtctxExempt: D00.1 functions no op of the stmtctx domain covers, with the reason.
func stmtctxExempt() map[string]string {
	return map[string]string{
		"GenContextID":                                    "a process-wide counter; covered by unit tests",                           // pkg/util/context/context.go
		"AllocateTaskID":                                  "a process-wide counter; covered by unit tests",                           // pkg/sessionctx/stmtctx/stmtctx.go
		"GetStmtLabel":                                    "ast.GetStmtLabel (parser AST, D02.2)",                                    // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.CtxID":                          "a process-wide counter (GenContextID); covered by unit tests",            // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetFlatPlan":                    "opaque any values (plan, ranger and pb contexts); covered by unit tests", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetLockWaitStartTime":           "reads the wall clock; covered by unit tests",                             // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetOrInitBuildPBCtxFromCache":   "opaque any values (plan, ranger and pb contexts); covered by unit tests", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetOrInitDistSQLFromCache":      "distsqlctx.DistSQLContext (D00.1 part 2)",                                // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetOrInitRangerCtxFromCache":    "opaque any values (plan, ranger and pb contexts); covered by unit tests", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetPlan":                        "opaque any values (plan, ranger and pb contexts); covered by unit tests", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetPlanDigest":                  "parser.Digest (D02.2 digester) not ported",                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetResourceGroupTagger":         "parser.Digest (D02.2 digester) not ported",                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetUsedStatsInfo":               "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.InitSQLDigest":                  "parser.Digest (D02.2 digester) not ported",                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.RecordedStatsLoadStatusCnt":     "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.ResetSQLDigest":                 "parser.Digest (D02.2 digester) not ported",                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.RestoreLogicalPlanBuildState":   "model.TableInfo-keyed maps and intset (planner, P3)",                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SQLDigest":                      "parser.Digest (D02.2 digester) not ported",                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SaveLogicalPlanBuildState":      "model.TableInfo-keyed maps and intset (planner, P3)",                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SetFlatPlan":                    "opaque any values (plan, ranger and pb contexts); covered by unit tests", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SetPlan":                        "opaque any values (plan, ranger and pb contexts); covered by unit tests", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SetPlanDigest":                  "parser.Digest (D02.2 digester) not ported",                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.GetUsedInfo":                       "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.Keys":                              "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.RecordUsedInfo":                    "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.Values":                            "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.FormatForExplain":          "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.WriteToSlowLog":            "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.collectFromColOrIdxStatus": "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.recordedColIdxCount":       "used-stats info: statistics types (D11.1/D11.2)",                         // pkg/sessionctx/stmtctx/stmtctx.go
		"WithStmtLabel":                                   "ast.GetStmtLabel (parser AST, D02.2)",                                    // pkg/sessionctx/stmtctx/stmtctx.go
	}
}
