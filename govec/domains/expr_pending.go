// Generated from data/func_blocks.tsv (block D08.1), then edited. The block is closed
// (data/blocks_done.tsv): each entry below is exempt for good (mapping rule, cascades, test
// or write path only), ported without a record of its own (Rust unit tests), or owned by
// another block and listed in that block's tasks.md in tidb-rs.

package domains

// exprExempt: D08.1 functions no op of any domain covers, with the reason.
func exprExempt() map[string]string {
	const (
		pool = "permanent, M-POOL: the sync.Pool column allocator of Go's vectorized evaluation; the Rust vector " +
			"driver keeps its result columns in RpnState (mappings.md M-POOL)"
		selPool = "permanent, M-POOL: the sel / isZero slice pools of VecEvalBool; tidb-rs allocates them by value " +
			"(go: VecEvalBool L2)"
		byRows = "permanent, M-EVAL: the row-by-row fallback Go's vectorized bodies call when a signature has no " +
			"vectorized form; the Rust vector driver is generated from the row body of every signature, so no " +
			"fallback helper exists (N4 §3.3)"
		vecEval = "permanent, M-EVAL: per-type vectorized evaluation of a leaf node; the Rust RPN reads the column / " +
			"constant / correlated value into the vector driver directly (N4 §3.3; column.rs ports only the " +
			"converting VecEvalInt/Real/String)"
		cascades = "permanent: cascades HashEquals only (pkg/planner/cascades/base is EXC.B)"
		intest   = "permanent: built only under intest (the assertion wrapper of ScalarFunction's eval methods when " +
			"intest.EnableAssert); no production path"
		writePath = "permanent: write path only (UPDATE / INSERT ... ON DUPLICATE KEY UPDATE assignments; the DML " +
			"planner and executors are X, served by Go TiDB)"
		toRewriter = "owned by D09.2: a BuildOption of BuildSimpleExpr / ParseSimpleExpr, which the expression " +
			"rewriter implements; listed in tidb-rs specs/D09.2-rewriter/tasks.md"
	)
	m := map[string]string{
		// builtin_vectorized.go
		"GetColumn":                   pool,
		"PutColumn":                   pool,
		"localColumnPool.MemoryUsage": pool,
		"localColumnPool.get":         pool,
		"localColumnPool.put":         pool,
		"newLocalColumnPool":          pool,
		"vecEvalIntByRows":            byRows,
		"vecEvalStringByRows":         byRows,
		// chunk_executor.go
		"HasAssignSetVarFunc": "ported (chunk_executor.rs, unit test): no reachable caller until SETVAR/GETVAR are ported (D08.9)",
		// column.go, constant.go, scalar_function.go
		"Column.Equals":           cascades,
		"Column.Hash64":           cascades,
		"CorrelatedColumn.Equals": cascades,
		"CorrelatedColumn.Hash64": cascades,
		"Constant.Equals":         cascades,
		"Constant.Hash64":         cascades,
		"ScalarFunction.Equals":   cascades,
		"ScalarFunction.Hash64":   cascades,
		"Column.EvalVirtualColumn": "ported (column.rs, unit test expr_unit.rs): one line (VirtualExpr.Eval); its callers are the " +
			"UnionScan / admin executors (D12.1) and table/column.go (D06.4)",
		// context.go
		"assertionEvalContext.GetOptionalPropProvider": intest,
		"wrapEvalAssert": intest,
		"checkEvalCtx": "ported (context.rs): an intest.Assert only (the context's location equals its TypeCtx's); " +
			"no output a record could compare",
		// expression.go
		"Args2Expressions4Test":  "permanent: test helper (testkit, session/testutil)",
		"Assignment.Clone":       writePath,
		"Assignment.MemoryUsage": writePath,
		"CNFExprs.Clone": "ported (expression.rs, unit test expr_unit.rs): copies the list over the shared " +
			"immutable nodes (M-EXPR); its callers are planner rules (P3)",
		"CNFExprs.Shallow": "ported (expression.rs, unit test expr_unit.rs): copies the list; its callers are planner " +
			"rules (P3)",
		"NewValuesFunc": "owned by D09.2: the rewriter's ValuesExpr case is its only caller (VALUES(col) is NULL " +
			"outside INSERT ... ON DUPLICATE KEY UPDATE), and it needs valuesFunctionClass (builtin_other.go, " +
			"D08.9); listed in tidb-rs specs/D09.2-rewriter/tasks.md",
		"StringifyExpressionsWithCtx": "ported (expression.rs, unit test): formats expressions for logs and EXPLAIN",
		"WithAllowCastArray":          toRewriter,
		"WithCastExprTo":              toRewriter,
		"WithInputSchemaAndNames":     toRewriter,
		"WithTableInfo":               toRewriter,
		"allocSelSlice":               selPool,
		"allocZeroSlice":              selPool,
		"deallocateSelSlice":          selPool,
		"deallocateZeroSlice":         selPool,
		"implicitEvalReal":            "ported (expression.rs): reached only through CanImplicitEvalReal (DAYNAME, D08.7)",
		"isColumnInOperand": "ported (expression.rs): the column filter of IsEQCondFromIn; no generated input sets " +
			"InOperand",
		// schema.go
		"Schema.MemoryUsage": "ported (schema.rs; unit test expr_unit.rs against Go's values): memory accounting " +
			"of plans, whose callers are the planner (P3)",
		// vectorized.go
		"genVecFromConstExpr": vecEval,
	}
	for _, t := range []string{"Column", "CorrelatedColumn", "Constant"} {
		for _, et := range []string{"Int", "Real", "String", "Decimal", "Time", "Duration", "JSON", "VectorFloat32"} {
			if t == "Column" && (et == "Int" || et == "Real" || et == "String") {
				continue // ported: the converting cases (column.rs)
			}
			m[t+".VecEval"+et] = vecEval
		}
	}
	return m
}
