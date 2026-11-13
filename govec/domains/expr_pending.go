// Code generated from data/func_blocks.tsv (block D08.1). Shrinks as D08.1 parts 2 and 3 land.

package domains

// exprExempt: D08.1 functions no op of the expr domain covers, with the reason.
func exprExempt() map[string]string {
	return map[string]string{
		"GetColumn":                                    "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"PutColumn":                                    "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"localColumnPool.MemoryUsage":                  "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"localColumnPool.get":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"localColumnPool.put":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"newLocalColumnPool":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"vecEvalIntByRows":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"vecEvalStringByRows":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"HasAssignSetVarFunc":                          "ported (tidb-rs chunk_executor.rs, unit test): no reachable caller until SETVAR/GETVAR (D08.9) are ported",                        // pkg/expression/chunk_executor.go
		"Column.Equals":                                "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/column.go
		"Column.EvalVirtualColumn":                     "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/column.go
		"Column.Hash64":                                "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/column.go
		"Column.VecEvalDecimal":                        "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalDuration":                       "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalJSON":                           "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalTime":                           "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalVectorFloat32":                  "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.Equals":                      "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/column.go
		"CorrelatedColumn.Hash64":                      "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/column.go
		"CorrelatedColumn.VecEvalDecimal":              "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalDuration":             "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalInt":                  "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalJSON":                 "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalReal":                 "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalString":               "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalTime":                 "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"CorrelatedColumn.VecEvalVectorFloat32":        "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Constant.Equals":                              "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/constant.go
		"Constant.Hash64":                              "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/constant.go
		"Constant.VecEvalDecimal":                      "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalDuration":                     "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalInt":                          "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalJSON":                         "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalReal":                         "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalString":                       "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalTime":                         "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"Constant.VecEvalVectorFloat32":                "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/constant.go
		"assertionEvalContext.GetOptionalPropProvider": "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"checkEvalCtx":                                 "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"errCtx":                                       "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"location":                                     "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"sqlMode":                                      "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"truncateWarnings":                             "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"warningCount":                                 "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"wrapEvalAssert":                               "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/context.go
		"handleAllowedPacketOverflowed":                "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/errors.go
		"handleDivisionByZeroError":                    "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/errors.go
		"handleInvalidTimeError":                       "unexported helper: covered by the built-in function records of D08.2+ (Rust unit test meanwhile)",                                 // pkg/expression/errors.go
		"Args2Expressions4Test":                        "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"Assignment.Clone":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"Assignment.MemoryUsage":                       "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"CNFExprs.Clone":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"CNFExprs.Shallow":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"EvaluateExprWithNull":                         "covered by the cast domain (evnull records, D08.2 part 2)",                                                                        // pkg/expression/expression.go
		"FlattenCNFConditions":                         "covered by the cast domain (util records, D08.12 batch 1)",                                                                        // pkg/expression/expression.go
		"FlattenDNFConditions":                         "covered by the cast domain (util records, D08.12 batch 1)",                                                                        // pkg/expression/expression.go
		"NewValuesFunc":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"PropagateType":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"StringifyExpressionsWithCtx":                  "ported (tidb-rs expression.rs, unit test): formats expressions for logs and EXPLAIN",                                              // pkg/expression/expression.go
		"WithAllowCastArray":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"WithCastExprTo":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"WithInputSchemaAndNames":                      "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"WithTableInfo":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"allocSelSlice":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"allocZeroSlice":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"deallocateSelSlice":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"deallocateZeroSlice":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"evaluateExprWithNull":                         "covered by the cast domain (evnull records, D08.2 part 2)",                                                                        // pkg/expression/expression.go
		"evaluateExprWithNullInNullRejectCheck":        "covered by the cast domain (evnull records, D08.2 part 2)",                                                                        // pkg/expression/expression.go
		"extractBinaryOpItems":                         "covered by the cast domain (util records, D08.12 batch 1)",                                                                        // pkg/expression/expression.go
		"implicitEvalReal":                             "ported (tidb-rs expression.rs): reached only through CanImplicitEvalReal (DAYNAME, D08.7)",                                        // pkg/expression/expression.go
		"isColumnInOperand":                            "ported (tidb-rs expression.rs): the column filter of IsEQCondFromIn (reached from VecEvalBool: cast domain, evbool records)",      // pkg/expression/expression.go
		"ScalarFunction.Equals":                        "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/scalar_function.go
		"ScalarFunction.Eval":                          "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalDecimal":                   "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalDuration":                  "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalInt":                       "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalJSON":                      "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalReal":                      "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalString":                    "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalTime":                      "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.EvalVectorFloat32":             "ported (dispatch to the signature's drivers); records come with the first signatures (D08.2/D08.8)",                               // pkg/expression/scalar_function.go
		"ScalarFunction.Hash64":                        "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalDecimal":                "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalDuration":               "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalInt":                    "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalJSON":                   "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalReal":                   "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalString":                 "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalTime":                   "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"ScalarFunction.VecEvalVectorFloat32":          "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/scalar_function.go
		"Schema.MemoryUsage":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/schema.go
		"genVecFromConstExpr":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/vectorized.go
	}
}
