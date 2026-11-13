// Generated from data/func_blocks.tsv (block D00.1 part 2b), then edited: the functions of
// the part 2b files no ctx2b op covers, with the reason. The block is closed
// (data/blocks_done.tsv): each entry is ported (unit tests), or owned by another block and
// listed in that block's tasks.md in tidb-rs.

package domains

// sessionexprFuncs: every function of pkg/expression/sessionexpr/sessionctx.go. Its methods
// share their names with exprstatic's (EvalContext.AppendNote, ...), so they are written
// "<file>:<Symbol>".
var sessionexprFuncs = []string{
	"EvalContext.AllParamValues", "EvalContext.AppendNote", "EvalContext.AppendWarning", "EvalContext.CopyWarnings",
	"EvalContext.CtxID", "EvalContext.CurrentDB", "EvalContext.CurrentTime", "EvalContext.ErrCtx",
	"EvalContext.GetDefaultWeekFormatMode", "EvalContext.GetDivPrecisionIncrement", "EvalContext.GetMaxAllowedPacket",
	"EvalContext.GetOptionalPropProvider", "EvalContext.GetOptionalPropSet", "EvalContext.GetParamValue",
	"EvalContext.GetTiDBRedactLog", "EvalContext.GetUserVarsReader", "EvalContext.GetWarnHandler",
	"EvalContext.IntoStatic", "EvalContext.Location", "EvalContext.RequestDynamicVerification",
	"EvalContext.RequestVerification", "EvalContext.SQLMode", "EvalContext.Sctx", "EvalContext.TruncateWarnings",
	"EvalContext.TypeCtx", "EvalContext.WarningCount", "EvalContext.setOptionalProp", "ExprContext.AllocPlanColumnID",
	"ExprContext.ConnectionID", "ExprContext.GetBlockEncryptionMode", "ExprContext.GetCharsetInfo",
	"ExprContext.GetDefaultCollationForUTF8MB4", "ExprContext.GetEvalCtx", "ExprContext.GetGroupConcatMaxLen",
	"ExprContext.GetLastPlanColumnID", "ExprContext.GetNoopFuncsMode", "ExprContext.GetPlanCacheTracker",
	"ExprContext.GetStaticConvertibleEvalContext", "ExprContext.GetSysdateIsNow",
	"ExprContext.GetWindowingUseHighPrecision", "ExprContext.IntoStatic", "ExprContext.IsConstantPropagateCheck",
	"ExprContext.IsInNullRejectCheck", "ExprContext.IsReadonlyUserVar", "ExprContext.IsUseCache", "ExprContext.Rng",
	"ExprContext.SetSkipPlanCache", "NewEvalContext", "NewExprContext", "currentUserProp", "getStmtTimestamp",
	"infoSchemaProp", "sequenceOperator.GetSequenceID", "sequenceOperator.GetSequenceNextVal",
	"sequenceOperator.SetSequenceVal", "sequenceOperatorProp", "sqlExecutorProp",
}

func ctx2bExempt() map[string]string {
	const (
		toSession = "owned by D05.3: pkg/expression/sessionexpr is the ExprContext / EvalContext over a live " +
			"session (SessionVars, StmtCtx, InfoSchema, sequences, SQL executor); tidb-rs builds it with the " +
			"session (N2); listed in tidb-rs specs/D05.3-session/tasks.md"
		toSessionVars = "owned by D05.3: the SessionVars provider of the builtins (N2: EvalContext is Send + Sync, " +
			"SessionVars belongs to the session task); listed in tidb-rs specs/D05.3-session/tasks.md"
		toDistSQL = "owned by D07.3: DistSQLContext's fields are D14.0 runtime stats and the D07.2 coprocessor " +
			"client; listed in tidb-rs specs/D07.3-distsql/tasks.md"
		pbCtx    = "ported (planctx.rs): accessor / struct copy over interface values (exprctx.BuildContext, kv.Client); Rust unit test (planctx_unit.rs)"
		emptyCtx = "ported (planctx.rs): no-op of the empty plan context; Rust unit test (planctx_unit.rs)"
	)
	m := map[string]string{
		"DistSQLContext.AppendWarning":                    toDistSQL,     // pkg/distsql/context/context.go
		"DistSQLContext.Detach":                           toDistSQL,     // pkg/distsql/context/context.go
		"NewSessionVarsProvider":                          toSessionVars, // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropProvider.Desc":                    toSessionVars, // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropReader.GetSessionVars":            toSessionVars, // pkg/expression/expropt/sessionvars.go
		"SessionVarsPropReader.RequiredOptionalEvalProps": toSessionVars, // pkg/expression/expropt/sessionvars.go
		"BuildPBContext.Detach":                           pbCtx,         // pkg/planner/planctx/context.go
		"BuildPBContext.GetClient":                        pbCtx,         // pkg/planner/planctx/context.go
		"BuildPBContext.GetExprCtx":                       pbCtx,         // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.AdviseTxnWarmup":        emptyCtx,      // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.GetReadonlyUserVarMap":  emptyCtx,      // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.Reset":                  emptyCtx,      // pkg/planner/planctx/context.go
		"EmptyPlanContextExtended.SetReadonlyUserVarMap":  emptyCtx,      // pkg/planner/planctx/context.go
		"ValidateSnapshotReadTS": "owned by D07.1: needs kv.Storage.GetOracle (the oracle client of the store driver); " +
			"listed in tidb-rs specs/D07.1-store/tasks.md", // pkg/sessionctx/context.go
	}
	for _, f := range sessionexprFuncs {
		m["pkg/expression/sessionexpr/sessionctx.go:"+f] = toSession
	}
	return m
}
