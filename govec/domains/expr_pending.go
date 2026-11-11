// Code generated from data/func_blocks.tsv (block D08.1). Shrinks as D08.1 parts 2 and 3 land.

package domains

// exprExempt: D08.1 functions no op of the expr domain covers, with the reason.
func exprExempt() map[string]string {
	return map[string]string{
		"builtinVecAsTextSig.Clone":                        "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecAsTextSig.evalString":                   "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecCosineDistanceSig.Clone":                "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecCosineDistanceSig.evalReal":             "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecDimsSig.Clone":                          "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecDimsSig.evalInt":                        "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecFromTextSig.Clone":                      "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecFromTextSig.evalVectorFloat32":          "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecL1DistanceSig.Clone":                    "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecL1DistanceSig.evalReal":                 "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecL2DistanceSig.Clone":                    "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecL2DistanceSig.evalReal":                 "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecL2NormSig.Clone":                        "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecL2NormSig.evalReal":                     "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecNegativeInnerProductSig.Clone":          "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecNegativeInnerProductSig.evalReal":       "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecAsTextFunctionClass.getFunction":               "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecCosineDistanceFunctionClass.getFunction":       "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecDimsFunctionClass.getFunction":                 "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecFromTextFunctionClass.getFunction":             "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecL1DistanceFunctionClass.getFunction":           "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecL2DistanceFunctionClass.getFunction":           "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecL2NormFunctionClass.getFunction":               "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"vecNegativeInnerProductFunctionClass.getFunction": "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec.go
		"builtinVecAsTextSig.vecEvalString":                "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecAsTextSig.vectorized":                   "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecCosineDistanceSig.vecEvalReal":          "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecCosineDistanceSig.vectorized":           "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecDimsSig.vecEvalInt":                     "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecDimsSig.vectorized":                     "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecFromTextSig.vecEvalVectorFloat32":       "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecFromTextSig.vectorized":                 "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecL1DistanceSig.vecEvalReal":              "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecL1DistanceSig.vectorized":               "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecL2DistanceSig.vecEvalReal":              "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecL2DistanceSig.vectorized":               "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecL2NormSig.vecEvalReal":                  "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecL2NormSig.vectorized":                   "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecNegativeInnerProductSig.vecEvalReal":    "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"builtinVecNegativeInnerProductSig.vectorized":     "D08.1 part 3 (VECTOR functions, with D08.2 function registration)", // pkg/expression/builtin_vec_vec.go
		"GetColumn":                                    "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"PutColumn":                                    "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"localColumnPool.MemoryUsage":                  "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"localColumnPool.get":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"localColumnPool.put":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"newLocalColumnPool":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"vecEvalIntByRows":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"vecEvalStringByRows":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/builtin_vectorized.go
		"HasAssignSetVarFunc":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"HasGetSetVarFunc":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"Vectorizable":                                 "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"VectorizedExecute":                            "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"VectorizedFilter":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"VectorizedFilterConsiderNull":                 "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"checkSequenceFunction":                        "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"evalOneCell":                                  "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"evalOneColumn":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"evalOneVec":                                   "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToDatetime":                            "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToDecimal":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToDuration":                            "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToInt":                                 "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToJSON":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToReal":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToString":                              "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"executeToVectorFloat32":                       "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"rowBasedFilter":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"vectorizedFilter":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/chunk_executor.go
		"Column.Equals":                                "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/column.go
		"Column.EvalVirtualColumn":                     "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/column.go
		"Column.Hash64":                                "cascades HashEquals only (pkg/planner/cascades/base is EXC.B)",                                                                    // pkg/expression/column.go
		"Column.VecEvalDecimal":                        "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalDuration":                       "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalInt":                            "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalJSON":                           "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalReal":                           "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
		"Column.VecEvalString":                         "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/column.go
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
		"EvaluatorSuite.RequiredOptionalEvalProps":     "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"EvaluatorSuite.Run":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"EvaluatorSuite.Vectorizable":                  "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"GetOptionalEvalPropsForExpr":                  "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"NewEvaluatorSuite":                            "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"defaultEvaluator.RequiredOptionalEvalProps":   "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"defaultEvaluator.run":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/evaluator.go
		"Args2Expressions4Test":                        "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"Assignment.Clone":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"Assignment.MemoryUsage":                       "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"CNFExprs.Clone":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"CNFExprs.Shallow":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"ComposeCNFCondition":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"ComposeDNFCondition":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"EvalExpr":                                     "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"EvaluateExprWithNull":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"FlattenCNFConditions":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"FlattenDNFConditions":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"NewValuesFunc":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"PropagateType":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"SplitCNFItems":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"SplitDNFItems":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"StringifyExpressionsWithCtx":                  "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"VecEvalBool":                                  "vectorized evaluation: replaced by the vector driver of part 2 (N4 §3.3 M-EVAL)",                                                  // pkg/expression/expression.go
		"WithAllowCastArray":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"WithCastExprTo":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"WithInputSchemaAndNames":                      "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"WithTableInfo":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"allocSelSlice":                                "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"allocZeroSlice":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"composeConditionWithBinaryOp":                 "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"deallocateSelSlice":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"deallocateZeroSlice":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"evaluateExprWithNull":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"evaluateExprWithNullInNullRejectCheck":        "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"extractBinaryOpItems":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"implicitEvalReal":                             "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"isColumnInOperand":                            "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"splitNormalFormItems":                         "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"toBool":                                       "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"wrapWithIsTrue":                               "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/expression.go
		"NewFunction":                                  "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"NewFunctionBase":                              "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"NewFunctionInternal":                          "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"NewFunctionTryFold":                           "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"NewFunctionWithInit":                          "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
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
		"defaultScalarFunctionCheck":                   "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"newFunctionImpl":                              "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"typeInferForNull":                             "needs the function class table funcs (D08.2): NewFunction* follow with it",                                                        // pkg/expression/scalar_function.go
		"Schema.MemoryUsage":                           "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/schema.go
		"genVecFromConstExpr":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/vectorized.go
		"ExtractVectorHelper":                          "D08.1 part 2b (evaluation entry points: EvaluatorSuite, VectorizedFilter/VecEvalBool, chunk_executor; with the first signatures)", // pkg/expression/vs_helper.go
	}
}
