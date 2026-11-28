// The D09.1 / D09.4 / D10.2 / D09.3 functions the selectbuild domain does not cover (from
// data/func_blocks.tsv; regenerate when the coverage list changes).

package domains

// selectbuildExempt: the functions of the domain's blocks no selectbuild record covers, with the reason.
func selectbuildExempt() map[string]string {
	const (
		partC = "D09.4 part C (GROUP BY ... WITH ROLLUP / LogicalExpand): not yet ported, builder stops at a boundary stub; tidb-rs specs/D09.4-agg-window/spec.md §3.4"
		partD = "D09.4 part D (correlated aggregates in subqueries, depends on D09.6): not yet ported, builder stops at a boundary stub; tidb-rs specs/D09.4-agg-window/spec.md §3.5"
		excB  = "permanent (EXC.B): only called by legacy cascades planner; tidb-rs does not support cascades planner; tidb-rs specs/D09.4-agg-window/spec.md §3.6"

		joinB   = "D09.3 part B (LogicalJoin methods of the logical optimization rules): not yet ported, the optimizer stops at a boundary stub before them; tidb-rs specs/D09.3-join-build/spec.md §3.2"
		joinC   = "D09.3 part C (LogicalApply, apply building and LATERAL, with D09.6): not yet ported, builder stops at a boundary stub; tidb-rs specs/D09.3-join-build/spec.md §3.3"
		joinD   = "D09.3 part D (joins of set operations, with D09.5): not yet ported; tidb-rs specs/D09.3-join-build/spec.md §3.4"
		joinE   = "D09.3 part E (MPP join hint checks of the physical enumeration, with D10.3): not yet ported; tidb-rs specs/D09.3-join-build/spec.md §3.5"
		joinExc = "permanent (EXC.B): only called by a cascades planner; tidb-rs does not support cascades planner; tidb-rs specs/D09.3-join-build/spec.md §3.6"
		joinFD  = "D09.3 part A boundary stub (blocked-by:D09.14, functional dependencies); tidb-rs specs/D09.3-join-build/spec.md §5.2"
		joinPPD = "ported (VS1-23, the predicate push down skeleton); only reached from LogicalJoin.PredicatePushDown (D09.8, with D09.3 part B); tidb-rs specs/D09.3-join-build/spec.md §3.2"
	)
	return map[string]string{
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildApplyWithJoinType":                                   joinC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildLateralJoin":                                         joinC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildSemiApply":                                           joinC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildSemiJoin":                                            joinC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildSemiJoinForSetOperator":                              joinD,
		"pkg/planner/core/logical_plan_builder.go:hasMPPJoinHints":                                                      joinE,
		"pkg/planner/core/logical_plan_builder.go:isImmediateLateralTableSource":                                        "D09.3 part A function only called on the LATERAL path (buildJoin, part C): ported with a tidb-rs unit test (lateral_table_source_detection); tidb-rs specs/D09.3-join-build/spec.md §3.1",
		"pkg/planner/core/logical_plan_builder.go:isJoinHintSupportedInMPPMode":                                         joinE,
		"pkg/planner/core/logical_plan_builder.go:unionJoinFieldType":                                                   joinD,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.CanPullUpAgg":                                joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.DeCorColFromEqExpr":                          joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.Equals":                                      joinExc,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.ExplainInfo":                                 joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.ExtractFD":                                   joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.Hash64":                                      joinExc,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.Init":                                        joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.ReplaceExprColumns":                          joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:LogicalApply.getGroupNDVs":                                joinC,
		"pkg/planner/core/operator/logicalop/logical_apply.go:findChildFullSchema":                                      joinC,
		"pkg/planner/core/operator/logicalop/logical_join.go:AppendAddSelectionTraceStep":                               joinPPD,
		"pkg/planner/core/operator/logicalop/logical_join.go:Conds2TableDual":                                           joinPPD,
		"pkg/planner/core/operator/logicalop/logical_join.go:DeriveOtherConditions":                                     joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ColumnSubstituteAll":                           joinC,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.Decorrelate":                                   joinC,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExplainInfo":                                   "D09.3 part A boundary stub (blocked-by:D10.9, the EXPLAIN text); tidb-rs specs/D09.3-join-build/spec.md §5.2",
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExtractFD":                                     joinFD,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExtractFDForInnerJoin":                         joinFD,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExtractFDForOuterJoin":                         joinFD,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExtractFDForSemiJoin":                          joinFD,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExtractJoinKeys":                               joinExc,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.ExtractUsedCols":                               joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.SemiJoinRewrite":                               joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.Shallow":                                       joinExc,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.getGroupNDVs":                                  "D09.3 part A function only called by LogicalJoin.DeriveStats (blocked-by:D10.6, the join part of the cardinality estimation): ported; tidb-rs specs/D09.3-join-build/spec.md §3.1",
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.getProj":                                       joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.outerJoinPropConst":                            joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:LogicalJoin.updateEQCond":                                  joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:addCandidateSelection":                                     joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:appendTopNPushDownJoinTraceStep":                           joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:deriveNotNullExpr":                                         joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:init":                                                      "D09.3 part A package init (intest assertion on the JoinType values, no input): tidb-rs asserts the same values at compile time; tidb-rs specs/D09.3-join-build/spec.md §3.1",
		"pkg/planner/core/operator/logicalop/logical_join.go:mergeOnClausePredicates":                                   joinB,
		"pkg/planner/core/operator/logicalop/logical_join.go:simplifyOuterJoin":                                         joinB,
		"pkg/planner/core/logical_plan_builder.go:CheckParamTypeInt64orUint64":                                          "SELECT builder part (VS1-21) boundary stub: the value of a parameter marker in LIMIT (the plan cache, D13.1); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.BuildDataSourceFromView":                                  "SELECT builder part (VS1-21) boundary stub: views (part 2 of this block); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.addAliasName":                                             "SELECT builder part (VS1-21) boundary stub: CREATE VIEW (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildExpand":                                              partC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildProjUponView":                                        "SELECT builder part (VS1-21) boundary stub: views (part 2 of this block); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.checkRecursiveView":                                       "SELECT builder part (VS1-21) boundary stub: views (part 2 of this block); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.extractCorrelatedAggFuncs":                                partD,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.implicitProjectGroupingSetCols":                           partC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.popVisitInfo":                                             "SELECT builder part (VS1-21) function no SELECT reaches (the visit infos of other statements): ported with a tidb-rs unit test; tidb-rs specs/D09.1-select-builder spec §6",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.replaceGroupingFunc":                                      partC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.resolveCorrelatedAggregates":                              partD,
		"pkg/planner/core/logical_plan_builder.go:addExtraPhysTblIDColumn4DS":                                           "SELECT builder part (VS1-21) boundary stub: partitioned tables (D09.12); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:aggColNameResolver.Enter":                                             partD,
		"pkg/planner/core/logical_plan_builder.go:allColFromAggExprNode":                                                partD,
		"pkg/planner/core/logical_plan_builder.go:appendDynamicVisitInfo":                                               "SELECT builder part (VS1-21) function no SELECT reaches (the visit infos of other statements): ported with a tidb-rs unit test; tidb-rs specs/D09.1-select-builder spec §6",
		"pkg/planner/core/logical_plan_builder.go:appendMultiColumns2VisitInfo":                                         "SELECT builder part (VS1-21) function no SELECT reaches (the visit infos of other statements): ported with a tidb-rs unit test; tidb-rs specs/D09.1-select-builder spec §6",
		"pkg/planner/core/logical_plan_builder.go:buildExpandFieldName":                                                 partC,
		"pkg/planner/core/logical_plan_builder.go:collectTableName":                                                     "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.Enter":                                    partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.Leave":                                    partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromGroupBy":                       partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromSelectFields":                  partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromTableRefs":                     partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromWhere":                         partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.resolveSelect":                            partD,
		"pkg/planner/core/logical_plan_builder.go:findColumnNameByUniqueID":                                             "SELECT builder part (VS1-21) boundary stub: the correlated columns of subqueries (D09.6); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:getTableOffset":                                                       "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:resolveGroupingTraverseAction.Transform":                              partC,
		"pkg/planner/core/logical_plan_builder.go:updatableTableListResolver.Enter":                                     "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:updatableTableListResolver.Leave":                                     "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.CopyAggHints":                    excB,
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.Equals":                          excB,
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.ExplainInfo":                     "D09.4 part A boundary stub (blocked-by:D10.9, SortedExplainExpressionList / ExplainAggFunc); tidb-rs specs/D09.4-agg-window/spec.md §5.2",
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.ExtractFD":                       "D09.4 part A boundary stub (blocked-by:D09.14, functional dependencies); tidb-rs specs/D09.4-agg-window/spec.md §5.2",
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.Hash64":                          excB,
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.IsCompleteModeAgg":               excB,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.BuildKeyInfo":                              partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.ExtractFD":                                 partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenLevelProjections":                       partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenerateGroupingIDIncrementModeNumericSet": partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenerateGroupingIDModeBitAnd":              partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenerateGroupingMarks":                     partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GetUsedCols":                               partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.Init":                                      partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.ResolveGroupingFuncArgsInGroupBy":          partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.TrySubstituteExprWithGroupingSetCol":       partC,
		"pkg/planner/core/operator/logicalop/logical_window.go:LogicalWindow.EqualFrame":                                excB,
		"pkg/planner/core/operator/logicalop/logical_window.go:LogicalWindow.EqualOrderBy":                              excB,
		"pkg/planner/core/operator/logicalop/logical_window.go:LogicalWindow.EqualPartitionBy":                          excB,
	}
}
