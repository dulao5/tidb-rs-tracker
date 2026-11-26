// The D09.1 / D09.4 / D10.2 functions the selectbuild domain does not cover (from
// data/func_blocks.tsv; regenerate when the coverage list changes).

package domains

// selectbuildExempt: the functions of the domain's blocks no selectbuild record covers, with the reason.
func selectbuildExempt() map[string]string {
	const (
		partC = "D09.4 part C (GROUP BY ... WITH ROLLUP / LogicalExpand): not yet ported, builder stops at a boundary stub; tidb-rs specs/D09.4-agg-window/spec.md §3.4"
		partD = "D09.4 part D (correlated aggregates in subqueries, depends on D09.6): not yet ported, builder stops at a boundary stub; tidb-rs specs/D09.4-agg-window/spec.md §3.5"
		excB  = "permanent (EXC.B): only called by legacy cascades planner; tidb-rs does not support cascades planner; tidb-rs specs/D09.4-agg-window/spec.md §3.6"
	)
	return map[string]string{
		"pkg/planner/core/logical_plan_builder.go:CheckParamTypeInt64orUint64":                                         "SELECT builder part (VS1-21) boundary stub: the value of a parameter marker in LIMIT (the plan cache, D13.1); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.BuildDataSourceFromView":                                 "SELECT builder part (VS1-21) boundary stub: views (part 2 of this block); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.addAliasName":                                            "SELECT builder part (VS1-21) boundary stub: CREATE VIEW (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildExpand":                                             partC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.buildProjUponView":                                       "SELECT builder part (VS1-21) boundary stub: views (part 2 of this block); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.checkRecursiveView":                                      "SELECT builder part (VS1-21) boundary stub: views (part 2 of this block); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.extractCorrelatedAggFuncs":                               partD,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.implicitProjectGroupingSetCols":                          partC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.popVisitInfo":                                            "SELECT builder part (VS1-21) function no SELECT reaches (the visit infos of other statements): ported with a tidb-rs unit test; tidb-rs specs/D09.1-select-builder spec §6",
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.replaceGroupingFunc":                                     partC,
		"pkg/planner/core/logical_plan_builder.go:PlanBuilder.resolveCorrelatedAggregates":                             partD,
		"pkg/planner/core/logical_plan_builder.go:addExtraPhysTblIDColumn4DS":                                          "SELECT builder part (VS1-21) boundary stub: partitioned tables (D09.12); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:aggColNameResolver.Enter":                                            partD,
		"pkg/planner/core/logical_plan_builder.go:allColFromAggExprNode":                                               partD,
		"pkg/planner/core/logical_plan_builder.go:appendDynamicVisitInfo":                                              "SELECT builder part (VS1-21) function no SELECT reaches (the visit infos of other statements): ported with a tidb-rs unit test; tidb-rs specs/D09.1-select-builder spec §6",
		"pkg/planner/core/logical_plan_builder.go:appendMultiColumns2VisitInfo":                                        "SELECT builder part (VS1-21) function no SELECT reaches (the visit infos of other statements): ported with a tidb-rs unit test; tidb-rs specs/D09.1-select-builder spec §6",
		"pkg/planner/core/logical_plan_builder.go:buildExpandFieldName":                                                partC,
		"pkg/planner/core/logical_plan_builder.go:collectTableName":                                                    "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.Enter":                                   partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.Leave":                                   partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromGroupBy":                      partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromSelectFields":                 partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromTableRefs":                    partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.collectFromWhere":                        partD,
		"pkg/planner/core/logical_plan_builder.go:correlatedAggregateResolver.resolveSelect":                           partD,
		"pkg/planner/core/logical_plan_builder.go:findColumnNameByUniqueID":                                            "SELECT builder part (VS1-21) boundary stub: the correlated columns of subqueries (D09.6); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:getTableOffset":                                                      "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:resolveGroupingTraverseAction.Transform":                             partC,
		"pkg/planner/core/logical_plan_builder.go:updatableTableListResolver.Enter":                                    "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/logical_plan_builder.go:updatableTableListResolver.Leave":                                    "SELECT builder part (VS1-21) boundary stub: UPDATE / DELETE (D09.15); tidb-rs specs/D09.1-select-builder spec §5",
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.CopyAggHints":                   excB,
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.Equals":                         excB,
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.ExplainInfo":                    "D09.4 part A boundary stub (blocked-by:D10.9, SortedExplainExpressionList / ExplainAggFunc); tidb-rs specs/D09.4-agg-window/spec.md §5.2",
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.ExtractFD":                      "D09.4 part A boundary stub (blocked-by:D09.14, functional dependencies); tidb-rs specs/D09.4-agg-window/spec.md §5.2",
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.Hash64":                         excB,
		"pkg/planner/core/operator/logicalop/logical_aggregation.go:LogicalAggregation.IsCompleteModeAgg":              excB,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.BuildKeyInfo":                             partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.ExtractFD":                                partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenLevelProjections":                      partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenerateGroupingIDIncrementModeNumericSet": partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenerateGroupingIDModeBitAnd":             partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GenerateGroupingMarks":                    partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.GetUsedCols":                              partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.Init":                                     partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.ResolveGroupingFuncArgsInGroupBy":         partC,
		"pkg/planner/core/operator/logicalop/logical_expand.go:LogicalExpand.TrySubstituteExprWithGroupingSetCol":      partC,
		"pkg/planner/core/operator/logicalop/logical_window.go:LogicalWindow.EqualFrame":                               excB,
		"pkg/planner/core/operator/logicalop/logical_window.go:LogicalWindow.EqualOrderBy":                             excB,
		"pkg/planner/core/operator/logicalop/logical_window.go:LogicalWindow.EqualPartitionBy":                         excB,
	}
}
