// Code generated from data/func_blocks.tsv (block D00.1). Shrinks as D00.1 part 2 lands.

package domains

// stmtctxExempt: D00.1 functions no op of the stmtctx domain covers, with the reason.
func stmtctxExempt() map[string]string {
	return map[string]string{
		"DistSQLContext.AppendWarning":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/distsql/context/context.go
		"DistSQLContext.Detach":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/distsql/context/context.go
		"AssertLocationWithSessionVars":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"ConstantPropagateCheckContext.IsConstantPropagateCheck":     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"CtxWithHandleTruncateErrLevel":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"NewSimplePlanColumnIDAllocator":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"NullRejectCheckExprContext.IsInNullRejectCheck":             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"SimplePlanColumnIDAllocator.AllocPlanColumnID":              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"SimplePlanColumnIDAllocator.GetLastPlanColumnID":            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"WithConstantPropagateCheck":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"WithNullRejectCheck":                                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"innerOverrideBuildContext.GetEvalCtx":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"innerOverrideEvalContext.ErrCtx":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"innerOverrideEvalContext.TypeCtx":                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/context.go
		"OptionalEvalPropDesc.Key":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKey.AsPropKeySet":                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKey.Desc":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKey.String":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKeySet.Add":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKeySet.Contains":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKeySet.IsEmpty":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKeySet.IsFull":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"OptionalEvalPropKeySet.Remove":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/optional.go
		"emptyParamValues.GetParamValue":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprctx/param.go
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
		"OptionalEvalPropProviders.Add":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/optional.go
		"OptionalEvalPropProviders.Contains":                         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/optional.go
		"OptionalEvalPropProviders.Get":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/optional.go
		"OptionalEvalPropProviders.PropKeySet":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/optional.go
		"getPropProvider":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/expropt/optional.go
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
		"EvalContext.AllParamValues":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.AppendNote":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.AppendWarning":                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.Apply":                                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.CopyWarnings":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.CtxID":                                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.CurrentDB":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.CurrentTime":                                    "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.ErrCtx":                                         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetDefaultWeekFormatMode":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetDivPrecisionIncrement":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetMaxAllowedPacket":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetOptionalPropProvider":                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetOptionalPropSet":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetParamValue":                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetTiDBRedactLog":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetUserVarsReader":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.GetWarnHandler":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.LoadSystemVars":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.Location":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.SQLMode":                                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.TruncateWarnings":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.TypeCtx":                                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.WarningCount":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.currentTimeFuncFromStringVal":                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"EvalContext.loadSessionVarsInternal":                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"MakeEvalContextStatic":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"NewEvalContext":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithCurrentDB":                                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithCurrentTime":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithDefaultWeekFormatMode":                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithDivPrecisionIncrement":                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithEnableRedactLog":                                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithErrLevelMap":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithLocation":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithMaxAllowedPacket":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithOptionalProperty":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithParamList":                                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithSQLMode":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithTypeFlags":                                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithUserVarsReader":                                         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"WithWarnHandler":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"newSessionVarsWithSystemVariables":                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"timeOnce.getTime":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/evalctx.go
		"ExprContext.AllocPlanColumnID":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.Apply":                                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.ConnectionID":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetBlockEncryptionMode":                         "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetCharsetInfo":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetDefaultCollationForUTF8MB4":                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetEvalCtx":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetGroupConcatMaxLen":                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetLastPlanColumnID":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetNoopFuncsMode":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetPlanCacheTracker":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetStaticConvertibleEvalContext":                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetStaticEvalCtx":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetSysdateIsNow":                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.GetWindowingUseHighPrecision":                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.IsConstantPropagateCheck":                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.IsInNullRejectCheck":                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.IsReadonlyUserVar":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.IsUseCache":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.LoadSystemVars":                                 "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.Rng":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.SetSkipPlanCache":                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"ExprContext.loadSessionVarsInternal":                        "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"MakeExprContextStatic":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"NewExprContext":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithBlockEncryptionMode":                                    "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithCharset":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithColumnIDAllocator":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithConnectionID":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithDefaultCollationForUTF8MB4":                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithEvalCtx":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithGroupConcatMaxLen":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithNoopFuncsMode":                                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithPlanCacheTracker":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithRng":                                                    "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithSysDateIsNow":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
		"WithWindowingUseHighPrecision":                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/expression/exprstatic/exprctx.go
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
		"FullIntRange":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/points.go
		"FullNotNullRange":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/points.go
		"FullRange":                                                  "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/points.go
		"HasFullRange":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.Clone":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.Encode":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.Equal":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.IntersectRange":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.IsFullRange":                                          "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.IsOnlyNull":                                           "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.IsPoint":                                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.IsPointNonNullable":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.IsPointNullable":                                      "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.MemUsage":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.PrefixEqualLen":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.Redact":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.String":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.Subset":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.Width":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.isPoint":                                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Range.string":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Ranges.CloneForPlanCache":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Ranges.IntersectRanges":                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Ranges.MemUsage":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Ranges.Range":                                               "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Ranges.Rebuild":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"Ranges.Subset":                                              "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"checkCollators":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"compareLexicographically":                                   "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"dealWithRedact":                                             "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"extendBound":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"formatDatum":                                                "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"isBoundaryValue":                                            "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"prefix":                                                     "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/types.go
		"RangerContext.Detach":                                       "D00.1 part 2 (expression/planner contexts; needs the expression framework D08.1)",              // pkg/util/ranger/context/context.go
	}
}
