// Code generated from data/func_blocks.tsv (block D00.1). Shrinks as D00.1 part 2 lands.

package domains

// stmtctxExempt: D00.1 functions no op of the stmtctx domain covers, with the reason.
func stmtctxExempt() map[string]string {
	return map[string]string{
		"DistSQLContext.AppendWarning":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/distsql/context/context.go
		"DistSQLContext.Detach":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/distsql/context/context.go
		"AdvisoryLockPropProvider.Desc":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/advisory_lock.go
		"AdvisoryLockPropReader.AdvisoryLockCtx":                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/advisory_lock.go
		"AdvisoryLockPropReader.RequiredOptionalEvalProps":           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/advisory_lock.go
		"NewAdvisoryLockPropProvider":                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/advisory_lock.go
		"CurrentUserPropProvider.Desc":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/current_user.go
		"CurrentUserPropReader.ActiveRoles":                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/current_user.go
		"CurrentUserPropReader.CurrentUser":                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/current_user.go
		"CurrentUserPropReader.RequiredOptionalEvalProps":            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/current_user.go
		"CurrentUserPropReader.getProvider":                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/current_user.go
		"DDLOwnerInfoProvider.Desc":                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/ddlowner.go
		"DDLOwnerPropReader.IsDDLOwner":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/ddlowner.go
		"DDLOwnerPropReader.RequiredOptionalEvalProps":               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/ddlowner.go
		"InfoSchemaPropProvider.Desc":                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/infoschema.go
		"InfoSchemaPropReader.GetDomainInfoSchema":                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/infoschema.go
		"InfoSchemaPropReader.GetSessionInfoSchema":                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/infoschema.go
		"InfoSchemaPropReader.RequiredOptionalEvalProps":             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/infoschema.go
		"KVStorePropProvider.Desc":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/kvstore.go
		"KVStorePropReader.GetKVStore":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/kvstore.go
		"KVStorePropReader.RequiredOptionalEvalProps":                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/kvstore.go
		"PrivilegeCheckerPropReader.GetPrivilegeChecker":             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/priv.go
		"PrivilegeCheckerPropReader.RequiredOptionalEvalProps":       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/priv.go
		"PrivilegeCheckerProvider.Desc":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/priv.go
		"SequenceOperatorPropReader.GetSequenceOperator":             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sequence.go
		"SequenceOperatorPropReader.RequiredOptionalEvalProps":       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sequence.go
		"SequenceOperatorProvider.Desc":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sequence.go
		"NewSessionVarsProvider":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropProvider.Desc":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropReader.GetSessionVars":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropReader.RequiredOptionalEvalProps":            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sessionvars.go
		"SQLExecutorPropProvider.Desc":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sqlexec.go
		"SQLExecutorPropReader.GetSQLExecutor":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sqlexec.go
		"SQLExecutorPropReader.RequiredOptionalEvalProps":            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/sqlexec.go
		"EvalContext.IntoStatic":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.RequestDynamicVerification":                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.RequestVerification":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.Sctx":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.setOptionalProp":                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"ExprContext.IntoStatic":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"currentUserProp":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"getStmtTimestamp":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"infoSchemaProp":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperator.GetSequenceID":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperator.GetSequenceNextVal":                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperator.SetSequenceVal":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperatorProp":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"sqlExecutorProp":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/sessionexpr/sessionctx.go
		"PlanCounterTp.Dec":                                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/core/base/plan_base.go
		"PlanCounterTp.Empty":                                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/core/base/plan_base.go
		"PlanCounterTp.IsForce":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/core/base/plan_base.go
		"BuildPBContext.Detach":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"BuildPBContext.GetClient":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"BuildPBContext.GetExprCtx":                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.AdviseTxnWarmup":                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.GetReadonlyUserVarMap":             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.Reset":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.SetReadonlyUserVarMap":             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/planctx/context.go
		"EncodeJSONCommon":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"EnterContextCommon":                                         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"GetOrInitDebugTraceRoot":                                    "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"LeaveContextCommon":                                         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"OptimizerDebugTraceRoot.AppendStepToCurrentContext":         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"OptimizerDebugTraceRoot.AppendStepWithNameToCurrentContext": "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"OptimizerDebugTraceRoot.MarshalJSON":                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"RecordAnyValuesWithNames":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"baseDebugTraceContext.MarshalJSON":                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/planner/util/debugtrace/base.go
		"ValidateSnapshotReadTS":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/sessionctx/context.go
		"basicCtxType.String":                                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/sessionctx/context.go
		"AllocateTaskID":                                             "a process-wide counter; covered by unit tests",                                                 // pkg/sessionctx/stmtctx/stmtctx.go
		"GetStmtLabel":                                               "ast.GetStmtLabel (parser AST, D02.2)",                                                          // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.CtxID":                                     "a process-wide counter (GenContextID); covered by unit tests",                                  // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.DetachMemDiskTracker":                      "statement memory/disk trackers: wired with the executor (D12.0) and the disk tracker (D01.10)", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetExecDetails":                            "execdetails.ExecDetails (D14.0)",                                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetFlatPlan":                               "opaque any values (plan, ranger and pb contexts); covered by unit tests",                       // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetLockWaitStartTime":                      "reads the wall clock; covered by unit tests",                                                   // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetOrInitBuildPBCtxFromCache":              "opaque any values (plan, ranger and pb contexts); covered by unit tests",                       // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetOrInitDistSQLFromCache":                 "distsqlctx.DistSQLContext (D00.1 part 2)",                                                      // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetOrInitRangerCtxFromCache":               "opaque any values (plan, ranger and pb contexts); covered by unit tests",                       // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetPlan":                                   "opaque any values (plan, ranger and pb contexts); covered by unit tests",                       // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetPlanDigest":                             "parser.Digest (D02.2 digester) not ported",                                                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetResourceGroupTagger":                    "parser.Digest (D02.2 digester) not ported",                                                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.GetUsedStatsInfo":                          "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.InitDiskTracker":                           "statement memory/disk trackers: wired with the executor (D12.0) and the disk tracker (D01.10)", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.InitMemTracker":                            "statement memory/disk trackers: wired with the executor (D12.0) and the disk tracker (D01.10)", // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.InitSQLDigest":                             "parser.Digest (D02.2 digester) not ported",                                                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.RecordedStatsLoadStatusCnt":                "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.ResetSQLDigest":                            "parser.Digest (D02.2 digester) not ported",                                                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.RestoreLogicalPlanBuildState":              "model.TableInfo-keyed maps and intset (planner, P3)",                                           // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SQLDigest":                                 "parser.Digest (D02.2 digester) not ported",                                                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SaveLogicalPlanBuildState":                 "model.TableInfo-keyed maps and intset (planner, P3)",                                           // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SetFlatPlan":                               "opaque any values (plan, ranger and pb contexts); covered by unit tests",                       // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SetPlan":                                   "opaque any values (plan, ranger and pb contexts); covered by unit tests",                       // pkg/sessionctx/stmtctx/stmtctx.go
		"StatementContext.SetPlanDigest":                             "parser.Digest (D02.2 digester) not ported",                                                     // pkg/sessionctx/stmtctx/stmtctx.go
		"StatsLoadResult.ErrorMsg":                                   "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"StatsLoadResult.HasError":                                   "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.GetUsedInfo":                                  "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.Keys":                                         "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.RecordUsedInfo":                               "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfo.Values":                                       "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.FormatForExplain":                     "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.WriteToSlowLog":                       "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.collectFromColOrIdxStatus":            "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"UsedStatsInfoForTable.recordedColIdxCount":                  "used-stats info: statistics types (D11.1/D11.2)",                                               // pkg/sessionctx/stmtctx/stmtctx.go
		"WithStmtLabel":                                              "ast.GetStmtLabel (parser AST, D02.2)",                                                          // pkg/sessionctx/stmtctx/stmtctx.go
		"GenContextID":                                               "a process-wide counter; covered by unit tests",                                                 // pkg/util/context/context.go
	}
}
