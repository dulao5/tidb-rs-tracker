// The D10.5 functions the costver2 domain does not cover (from data/func_blocks.tsv and
// tidb-rs specs/VS1-vertical-slice/functions.tsv; regenerate when the coverage list changes).

package domains

// costver2Exempt: the functions of the domain's block no costver2 record covers, with the reason.
func costver2Exempt() map[string]string {
	rest := "not yet ported: the cost of an operator outside the first vertical slice (index readers, index lookup / merge, joins, aggregation, union, MPP, CTE; tidb-rs specs/D10.5-cost-model spec §2)"
	batch := "not yet ported: the batch point get (D10.8) is outside the first vertical slice (tidb-rs specs/D10.5-cost-model spec §2)"
	v1 := "not ported: only the cost model v1 calls it, and the cost model v1 is exception B5 (EXC.B; tidb-rs specs/D10.5-cost-model spec §2)"
	test := "not ported: only Go tests call it (tidb-rs specs/D10.5-cost-model spec §2)"
	return map[string]string{
		"pkg/planner/core/plan_cost_detail.go:HashJoinCPUCostDetail.desc":              v1,
		"pkg/planner/core/plan_cost_detail.go:HashJoinCPUCostDetail.probeCostDesc":     v1,
		"pkg/planner/core/plan_cost_detail.go:HashJoinDiskCostDetail.desc":             v1,
		"pkg/planner/core/plan_cost_detail.go:HashJoinDiskCostDetail.probeDesc":        v1,
		"pkg/planner/core/plan_cost_detail.go:HashJoinMemoryCostDetail.desc":           v1,
		"pkg/planner/core/plan_cost_detail.go:setBatchPointGetPlanCostDetail":          v1,
		"pkg/planner/core/plan_cost_detail.go:setPhysicalHashJoinCostDetail":           v1,
		"pkg/planner/core/plan_cost_detail.go:setPhysicalIndexReaderCostDetail":        v1,
		"pkg/planner/core/plan_cost_detail.go:setPhysicalTableOrIndexScanCostDetail":   v1,
		"pkg/planner/core/plan_cost_detail.go:setPhysicalTableReaderCostDetail":        v1,
		"pkg/planner/core/plan_cost_detail.go:setPointGetPlanCostDetail":               v1,
		"pkg/planner/core/plan_cost_ver2.go:BatchPointGetPlan.GetPlanCostVer2":         batch,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalApply.GetPlanCostVer2":             rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalCTE.GetPlanCostVer2":               rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalExchangeReceiver.GetPlanCostVer2":  rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalHashAgg.GetPlanCostVer2":           rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalHashJoin.GetPlanCostVer2":          rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalIndexHashJoin.GetPlanCostVer2":     rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalIndexJoin.GetPlanCostVer2":         rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalIndexJoin.getIndexJoinCostVer2":    rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalIndexMergeJoin.GetPlanCostVer2":    rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalIndexMergeReader.GetPlanCostVer2":  rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalMergeJoin.GetPlanCostVer2":         rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalStreamAgg.GetPlanCostVer2":         rest,
		"pkg/planner/core/plan_cost_ver2.go:PhysicalUnionAll.GetPlanCostVer2":          rest,
		"pkg/planner/core/plan_cost_ver2.go:aggCostVer2":                               rest,
		"pkg/planner/core/plan_cost_ver2.go:groupCostVer2":                             rest,
		"pkg/planner/core/plan_cost_ver2.go:hashBuildCostVer2":                         rest,
		"pkg/planner/core/plan_cost_ver2.go:hashProbeCostVer2":                         rest,
		"pkg/planner/core/plan_cost_ver2.go:indexJoinSeekingCostVer2":                  rest,
		"pkg/planner/core/plan_cost_ver2.go:costVer2Factors.tolist":                    test,
	}
}
