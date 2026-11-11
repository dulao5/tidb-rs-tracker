// Code generated from data/func_blocks.tsv (block D00.1 part 2b): the functions of the part 2b
// files no ctx2b op covers, with the reason.

package domains

func ctx2bExempt() map[string]string {
	return map[string]string{
		"DistSQLContext.AppendWarning":                    "DistSQLContext's field types belong to D14.0 (execdetails) and D07.2 (coprocessor client)",                                     // pkg/distsql/context/context.go
		"DistSQLContext.Detach":                           "DistSQLContext's field types belong to D14.0 (execdetails) and D07.2 (coprocessor client)",                                     // pkg/distsql/context/context.go
		"NewSessionVarsProvider":                          "N2: EvalContext is Send + Sync, SessionVars belongs to the session task; how builtins reach SessionVars is decided with D05.3", // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropProvider.Desc":                    "N2: EvalContext is Send + Sync, SessionVars belongs to the session task; how builtins reach SessionVars is decided with D05.3", // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropReader.GetSessionVars":            "N2: EvalContext is Send + Sync, SessionVars belongs to the session task; how builtins reach SessionVars is decided with D05.3", // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropReader.RequiredOptionalEvalProps": "N2: EvalContext is Send + Sync, SessionVars belongs to the session task; how builtins reach SessionVars is decided with D05.3", // pkg/expression/expropt/sessionvars.go
		"EvalContext.IntoStatic":                          "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.RequestDynamicVerification":          "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.RequestVerification":                 "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.Sctx":                                "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"EvalContext.setOptionalProp":                     "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"ExprContext.IntoStatic":                          "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"currentUserProp":                                 "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"getStmtTimestamp":                                "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"infoSchemaProp":                                  "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperator.GetSequenceID":                  "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperator.GetSequenceNextVal":             "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperator.SetSequenceVal":                 "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"sequenceOperatorProp":                            "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"sqlExecutorProp":                                 "waits for the session (D05.3: N2 Session / SessionVars / StmtCtx) and the StatementContext swap of D05.2 (D00.1 T9)",           // pkg/expression/sessionexpr/sessionctx.go
		"BuildPBContext.Detach":                           "accessor / struct copy over interface values (exprctx.BuildContext, kv.Client); Rust unit test (planctx_unit.rs)",              // pkg/planner/planctx/context.go
		"BuildPBContext.GetClient":                        "accessor / struct copy over interface values (exprctx.BuildContext, kv.Client); Rust unit test (planctx_unit.rs)",              // pkg/planner/planctx/context.go
		"BuildPBContext.GetExprCtx":                       "accessor / struct copy over interface values (exprctx.BuildContext, kv.Client); Rust unit test (planctx_unit.rs)",              // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.AdviseTxnWarmup":        "no-op of the empty plan context; Rust unit test (planctx_unit.rs)",                                                             // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.GetReadonlyUserVarMap":  "no-op of the empty plan context; Rust unit test (planctx_unit.rs)",                                                             // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.Reset":                  "no-op of the empty plan context; Rust unit test (planctx_unit.rs)",                                                             // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.SetReadonlyUserVarMap":  "no-op of the empty plan context; Rust unit test (planctx_unit.rs)",                                                             // pkg/planner/planctx/context.go
		"ValidateSnapshotReadTS":                          "needs kv.Storage.GetOracle (the oracle client, D07.1)",                                                                         // pkg/sessionctx/context.go
	}
}
