// Block D08.8 functions no op of the cast domain covers, with the reason. Shrinks as the
// parts of D08.8 land (generated from data/func_blocks.tsv; edit the reasons by hand).

package domains

// castExemptAll: the exemptions of every block of the domain.
func castExemptAll() map[string]string {
	m := castExempt()
	for k, v := range castExemptD084() {
		m[k] = v
	}
	for k, v := range castExemptD085() {
		m[k] = v
	}
	for k, v := range castExemptD0812() {
		m[k] = v
	}
	return m
}

func castExempt() map[string]string {
	return map[string]string{
		"BuildCastFunction4Union":                             "ported; same path as BuildCastFunctionWithCheck with inUnion (the cast records set inUnion)",
		"CanImplicitEvalInt":                                  "ported; true only for DAYNAME (D08.7), which tidb-rs cannot build yet",
		"CanImplicitEvalReal":                                 "ported; true only for DAYNAME (D08.7), which tidb-rs cannot build yet",
		"builtinCastVectorFloat32AsUnsupportedSig.evalString": "unreachable: castAsStringFunctionClass maps a VECTOR argument to builtinCastVectorFloat32AsStringSig",
		"newFakeSctx":                                         "initializer of the package variable fakeSctx; the Rust port uses types.StrictContext (covered through to-array/json2tp)",
		"inCastContext.String":                                "ported (tidb-rs builtin_cast.rs, unit test): nothing sets or reads the key in v8.5.8",
	}
}
