package domains

// Domain "pushdown", block D08.3 part 2: tipb.Expr to expressions (distsql_builtin.go).
//
// Records (appended after the part 1 groups):
//   - "pbtps": [] -> the field types of the recipe columns, which PBToExpr resolves column
//     references against (first record of the group).
//   - "pbexpr": [recipe, opts] -> the round trip: ExprToPB of the built tree ("pb", marshalled,
//     or null) and PBToExpr of it ("back": the expression, an error or a panic). Columns
//     resolve against the field types of the recipe columns (pdColFts, by index).
//   - "pbraw": [[pb hex...], loc, nc] -> PBToExprs of hand-made messages: every constant type
//     with valid and malformed values, missing field types, value lists, unknown signatures
//     and expression types.
//
// "back" dumps a column as {"col": [index, ft]}, a constant as {"const": [datum, length,
// frac, ft|null]} and a function as {"f": [name, Go signature type, ft, args, pb code,
// charset, collation, [[field, value]...]]}, the last item being the signature's fields
// beyond baseBuiltinFunc that its struct literal sets (inUnion, keepNull, ...).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tipb/go-tipb"
)

// pdSigFields: the fields of the ported signatures beyond baseBuiltinFunc that a struct
// literal of getSignatureByPB sets.
var pdSigFields = []string{"inUnion", "keepNull", "cmpAsDate", "hasNullable", "constantArgOverflow",
	"cannotConvertStringAsWarning", "maxAllowedPacket"}

func pdPbDump(e expression.Expression) any {
	switch x := e.(type) {
	case *expression.Column:
		return map[string]any{"col": []any{x.Index, sfFtIn(x.RetType)}}
	case *expression.Constant:
		var ft any
		if x.RetType != nil {
			ft = sfFtIn(x.RetType)
		}
		return map[string]any{"const": []any{d2DatumInOf(x.Value), x.Value.Length(), x.Value.Frac(), ft}}
	case *expression.ScalarFunction:
		args := []any{}
		for _, a := range x.GetArgs() {
			args = append(args, pdPbDump(a))
		}
		chs, coll := x.Function.CharsetAndCollation()
		v := reflect.ValueOf(x.Function).Elem()
		fields := []any{}
		for _, name := range pdSigFields {
			if f, ok := v.Type().FieldByName(name); ok {
				fv := v.FieldByIndex(f.Index)
				if fv.Kind() == reflect.Bool {
					fields = append(fields, []any{name, fv.Bool()})
				} else {
					fields = append(fields, []any{name, fv.Uint()})
				}
			}
		}
		return map[string]any{"f": []any{x.FuncName.O, reflect.TypeOf(x.Function).Elem().Name(), sfFtIn(x.RetType),
			args, int(x.Function.PbCode()), chs, coll, fields}}
	}
	panic("pdPbDump")
}

func pdPbCtx(loc *int) *exprstatic.ExprContext {
	l := gotime.UTC
	if loc != nil {
		l = gotime.FixedZone("Z", *loc)
	}
	return exprstatic.NewExprContext(exprstatic.WithEvalCtx(exprstatic.NewEvalContext(exprstatic.WithLocation(l))))
}

func pdPbTps() []*types.FieldType {
	tps := []*types.FieldType{}
	for _, ft := range pdColFts {
		tps = append(tps, d2FtIn(exArr(vec.MustJSON(ft))))
	}
	return tps
}

// pdPbBack: PBToExprs with errors and panics as values.
func pdPbBack(ctx expression.BuildContext, pbs []*tipb.Expr) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": vec.GoString(fmt.Sprint(r))}
		}
	}()
	es, err := expression.PBToExprs(ctx, pbs, pdPbTps())
	if err != nil {
		return map[string]any{"err": vec.GoString(err.Error())}
	}
	l := []any{}
	for _, e := range es {
		l = append(l, pdPbDump(e))
	}
	return l
}

func pdPbExec(op string, p []json.RawMessage) (any, bool) {
	switch op {
	case "pbexpr":
		o := pdOpts(p[1])
		collate.SetNewCollationEnabledForTest(o.NC)
		e, err := pdBuild(pdBuildCtx(), p[0])
		if err != nil {
			return map[string]any{"build": exErr(err)}, true
		}
		pb := expression.NewPBConverter(&pdClient{mode: o.CL}, pdEvalCtx(o)).ExprToPB(e)
		if pb == nil {
			return map[string]any{"pb": nil}, true
		}
		return map[string]any{"pb": pdHex(pb, false), "back": pdPbBack(pdPbCtx(o.Loc), []*tipb.Expr{pb})}, true
	case "pbtps":
		out := []any{}
		for _, ft := range pdPbTps() {
			out = append(out, sfFtIn(ft))
		}
		return out, true
	case "pbraw":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 2))
		var loc *int
		if string(p[1]) != "null" {
			v := arg[int](p, 1)
			loc = &v
		}
		pbs := []*tipb.Expr{}
		for _, h := range arg[[]string](p, 0) {
			b, err := hex.DecodeString(h)
			must(err)
			pb := &tipb.Expr{}
			must(pb.Unmarshal(b))
			pbs = append(pbs, pb)
		}
		return pdPbBack(pdPbCtx(loc), pbs), true
	}
	return nil, false
}

// ---------------------------------------------------------------- generation

func pdPbHex(e *tipb.Expr) string {
	b, err := e.Marshal()
	must(err)
	return hex.EncodeToString(b)
}

func pdPbFt(tp byte, flag uint32, flen, dec int32, collate int32, elems ...string) *tipb.FieldType {
	return &tipb.FieldType{Tp: int32(tp), Flag: flag, Flen: flen, Decimal: dec, Collate: collate, Charset: "binary", Elems: elems}
}

func pdPbConst(tp tipb.ExprType, val []byte, ft *tipb.FieldType) *tipb.Expr {
	return &tipb.Expr{Tp: tp, Val: val, FieldType: ft}
}

// pdPbRaws: hand-made messages.
func pdPbRaws() [][]*tipb.Expr {
	ftInt := pdPbFt(mysql.TypeLonglong, 0, 20, 0, 63)
	ftStr := pdPbFt(mysql.TypeVarchar, 0, 10, 0, -46)
	ftDec := pdPbFt(mysql.TypeNewDecimal, 0, 10, 2, 63)
	ftDT := pdPbFt(mysql.TypeDatetime, 0, 26, 6, 63)
	ftTS := pdPbFt(mysql.TypeTimestamp, 0, 19, 0, 63)
	ftDate := pdPbFt(mysql.TypeDate, 0, 10, 0, 63)
	ftEnum := pdPbFt(mysql.TypeEnum, 0, 1, 0, 46, "a", "b", "c")
	enc := func(f func([]byte) []byte) []byte { return f(nil) }
	dec := func(s string) []byte {
		x := new(types.MyDecimal)
		must(x.FromString([]byte(s)))
		b, err := codec.EncodeDecimal(nil, x, 0, 0)
		must(err)
		return b
	}
	packed := func(y, mo, d, h, mi, s, us int, tp byte, fsp int) []byte {
		v, err := types.NewTime(types.FromDate(y, mo, d, h, mi, s, us), tp, fsp).ToPackedUint()
		must(err)
		return codec.EncodeUint(nil, v)
	}
	jsonVal := func(s string) []byte {
		j, err := types.ParseBinaryJSONFromString(s)
		must(err)
		b, err := codec.EncodeValue(nil, nil, types.NewJSONDatum(j))
		must(err)
		return b
	}
	vecVal := func(s string) []byte {
		v, err := types.ParseVectorFloat32(s)
		must(err)
		return v.ZeroCopySerialize()
	}
	col := func(i int64) *tipb.Expr {
		return &tipb.Expr{Tp: tipb.ExprType_ColumnRef, Val: codec.EncodeInt(nil, i), FieldType: ftInt}
	}
	fn := func(sig tipb.ScalarFuncSig, ft *tipb.FieldType, children ...*tipb.Expr) *tipb.Expr {
		return &tipb.Expr{Tp: tipb.ExprType_ScalarFunc, Sig: sig, FieldType: ft, Children: children}
	}
	valueList := func(ds ...types.Datum) *tipb.Expr {
		b, err := codec.EncodeValue(nil, nil, ds...)
		must(err)
		if len(ds) == 0 {
			b = nil
		}
		return &tipb.Expr{Tp: tipb.ExprType_ValueList, Val: b}
	}
	one := func(e *tipb.Expr) []*tipb.Expr { return []*tipb.Expr{e} }
	cs := [][]*tipb.Expr{
		one(pdPbConst(tipb.ExprType_Null, nil, nil)),
		one(pdPbConst(tipb.ExprType_Int64, enc(func(b []byte) []byte { return codec.EncodeInt(b, -42) }), ftInt)),
		one(pdPbConst(tipb.ExprType_Int64, []byte{1, 2, 3}, ftInt)),
		one(pdPbConst(tipb.ExprType_Int64, nil, ftInt)),
		one(pdPbConst(tipb.ExprType_Int64, codec.EncodeInt(nil, 1), nil)),
		one(pdPbConst(tipb.ExprType_Uint64, codec.EncodeUint(nil, 18446744073709551615), ftInt)),
		one(pdPbConst(tipb.ExprType_Uint64, []byte{9}, ftInt)),
		one(pdPbConst(tipb.ExprType_String, []byte("héllo"), ftStr)),
		one(pdPbConst(tipb.ExprType_String, []byte{}, pdPbFt(mysql.TypeString, 0, 3, 0, 46))),
		one(pdPbConst(tipb.ExprType_String, []byte("x"), pdPbFt(mysql.TypeVarchar, 0, -1, 0, -224))),
		one(pdPbConst(tipb.ExprType_Bytes, []byte("raw"), nil)),
		one(pdPbConst(tipb.ExprType_Bytes, nil, nil)),
		one(pdPbConst(tipb.ExprType_MysqlBit, []byte{0, 5}, nil)),
		one(pdPbConst(tipb.ExprType_Float32, codec.EncodeFloat(nil, 1.25), nil)),
		one(pdPbConst(tipb.ExprType_Float32, codec.EncodeFloat(nil, 3.4e39), nil)),
		one(pdPbConst(tipb.ExprType_Float64, codec.EncodeFloat(nil, -0.1), nil)),
		one(pdPbConst(tipb.ExprType_Float64, []byte{1}, nil)),
		one(pdPbConst(tipb.ExprType_MysqlDecimal, dec("123.450"), ftDec)),
		one(pdPbConst(tipb.ExprType_MysqlDecimal, dec("-0.000000001"), ftDec)),
		one(pdPbConst(tipb.ExprType_MysqlDecimal, []byte{65, 2}, ftDec)),
		one(pdPbConst(tipb.ExprType_MysqlDecimal, dec("1"), nil)),
		one(pdPbConst(tipb.ExprType_MysqlDuration, codec.EncodeInt(nil, int64(90*gotime.Minute+5)), nil)),
		one(pdPbConst(tipb.ExprType_MysqlDuration, []byte{7}, nil)),
		one(pdPbConst(tipb.ExprType_MysqlTime, packed(2024, 2, 29, 23, 30, 1, 123456, mysql.TypeDatetime, 6), ftDT)),
		one(pdPbConst(tipb.ExprType_MysqlTime, packed(2024, 2, 29, 23, 30, 1, 0, mysql.TypeTimestamp, 0), ftTS)),
		one(pdPbConst(tipb.ExprType_MysqlTime, packed(1970, 1, 1, 0, 0, 0, 0, mysql.TypeTimestamp, 0), ftTS)),
		one(pdPbConst(tipb.ExprType_MysqlTime, packed(2038, 1, 19, 3, 14, 8, 0, mysql.TypeTimestamp, 0), ftTS)),
		one(pdPbConst(tipb.ExprType_MysqlTime, packed(1999, 12, 31, 0, 0, 0, 0, mysql.TypeDate, 0), ftDate)),
		one(pdPbConst(tipb.ExprType_MysqlTime, codec.EncodeUint(nil, 0), ftTS)),
		one(pdPbConst(tipb.ExprType_MysqlTime, codec.EncodeUint(nil, (13<<5|1)<<41), ftDT)),
		one(pdPbConst(tipb.ExprType_MysqlTime, []byte{1, 2}, ftDT)),
		one(pdPbConst(tipb.ExprType_MysqlJson, jsonVal(`{"k": [1, 2.5, "s", null]}`), nil)),
		one(pdPbConst(tipb.ExprType_MysqlJson, codec.EncodeInt(nil, 3), nil)),
		one(pdPbConst(tipb.ExprType_MysqlJson, []byte{18}, nil)),
		one(pdPbConst(tipb.ExprType_MysqlEnum, codec.EncodeUint(nil, 2), ftEnum)),
		one(pdPbConst(tipb.ExprType_MysqlEnum, codec.EncodeUint(nil, 0), ftEnum)),
		one(pdPbConst(tipb.ExprType_MysqlEnum, codec.EncodeUint(nil, 9), ftEnum)),
		one(pdPbConst(tipb.ExprType_MysqlEnum, []byte{1}, ftEnum)),
		one(pdPbConst(tipb.ExprType_TiDBVectorFloat32, vecVal("[1,-2.5]"), nil)),
		one(pdPbConst(tipb.ExprType_TiDBVectorFloat32, []byte{1, 0, 0, 0}, nil)),
		one(col(0)),
		one(col(32)),
		one(col(33)),
		one(col(-1)),
		one(&tipb.Expr{Tp: tipb.ExprType_ColumnRef, Val: []byte{0}}),
		one(&tipb.Expr{Tp: tipb.ExprType_MysqlHex, Val: []byte{1}}),
		one(&tipb.Expr{Tp: tipb.ExprType_Count}),
		one(&tipb.Expr{Tp: tipb.ExprType(999)}),
		one(fn(tipb.ScalarFuncSig(9999), ftInt, col(0))),
		one(fn(tipb.ScalarFuncSig_Unspecified, ftInt, col(0))),
		one(fn(tipb.ScalarFuncSig(4574), ftInt)),
		one(fn(tipb.ScalarFuncSig_LTInt, ftInt, col(0), col(1))),
		one(fn(tipb.ScalarFuncSig_LTInt, nil, col(0), col(1))),
		one(fn(tipb.ScalarFuncSig_LTInt, ftInt, col(0), pdPbConst(tipb.ExprType_Int64, []byte{1}, ftInt))),
		one(fn(tipb.ScalarFuncSig_InInt, ftInt, col(0), valueList())),
		one(fn(tipb.ScalarFuncSig_InInt, ftInt, col(0), valueList(types.NewIntDatum(1), types.NewIntDatum(7)))),
		one(fn(tipb.ScalarFuncSig_InString, ftInt, col(8), valueList(types.NewStringDatum("a"), types.NewDatum(nil)))),
		one(fn(tipb.ScalarFuncSig_InInt, ftInt, col(0), &tipb.Expr{Tp: tipb.ExprType_ValueList, Val: []byte{3}})),
		one(fn(tipb.ScalarFuncSig_IntIsTrueWithNull, ftInt, col(0))),
		one(fn(tipb.ScalarFuncSig_RealIsFalseWithNull, ftInt, col(3))),
		one(fn(tipb.ScalarFuncSig_DecimalIsFalseWithNull, ftInt, col(5))),
		one(fn(tipb.ScalarFuncSig_GreatestDate, ftDate, col(20), col(20))),
		one(fn(tipb.ScalarFuncSig_LeastCmpStringAsDate, ftDate, col(8), col(8))),
		one(fn(tipb.ScalarFuncSig_IntervalInt, ftInt, col(0), col(0))),
		one(fn(tipb.ScalarFuncSig_UnaryMinusDecimal, ftDec, col(5))),
		one(fn(tipb.ScalarFuncSig_FromBinary, ftStr, col(12))),
		one(fn(tipb.ScalarFuncSig_CastIntAsString, ftStr, col(0))),
		one(fn(tipb.ScalarFuncSig_Concat, ftStr, col(8), col(8))),
		one(fn(tipb.ScalarFuncSig_AddDateDatetimeInt, ftDT, col(18), col(0), pdPbConst(tipb.ExprType_String, []byte("DAY"), ftStr))),
		one(fn(tipb.ScalarFuncSig_PlusInt, ftInt, fn(tipb.ScalarFuncSig_Concat, ftStr, col(8)), col(0))),
		{col(0), pdPbConst(tipb.ExprType_Int64, []byte{1}, ftInt), col(1)},
		{col(1), fn(tipb.ScalarFuncSig_EQInt, ftInt, col(0), col(1))},
		{},
	}
	return cs
}

func pdPbGenerate(g *vec.Gen, rng *rand.Rand, consts [][2]any, names []string) {
	opts := func(e expression.Expression, nc bool) pdOptsT {
		o := pdRandOpts(rng, e, nc)
		o.BL = nil // the blacklist only gates ExprToPB
		if o.CL == 2 {
			o.CL = 0
		}
		return o
	}
	nc := func() bool { return rng.IntN(5) != 0 }
	// The field types PBToExpr resolves columns against (the recipe columns, by index).
	g.Add("sys/pbtps", "pbtps")
	// Every built-in function whose tree ExprToPB converts.
	for _, name := range names {
		if name == ast.Grouping {
			continue
		}
		got := 0
		for try := 0; try < 60 && got < 8; try++ {
			ncv := nc()
			r := pdRecipe(rng, name, consts, 1)
			if e := pdTryBuild(ncv, r); e != nil {
				g.Add("rand/pbfunc", "pbexpr", r, opts(e, ncv))
				got++
			}
		}
	}
	// CAST from every column type to every target.
	for i := range pdColFts {
		for _, rt := range pdCastTargets {
			r := map[string]any{"f": []any{ast.Cast, rt, []any{pdColRecipe(rng, i)}}}
			ncv := nc()
			if e := pdTryBuild(ncv, r); e != nil {
				g.Add("sys/pbcast", "pbexpr", r, opts(e, ncv))
			}
		}
	}
	// Leaves: columns (by index; by ID with the client without DAG) and constants.
	for rep := 0; rep < 3; rep++ {
		for i := range pdColFts {
			r := pdColRecipe(rng, i)
			g.Add("sys/pbcol", "pbexpr", r, opts(nil, nc()))
		}
		for _, k := range consts {
			g.Add("sys/pbconst", "pbexpr", map[string]any{"const": []any{k[0], k[1]}}, opts(nil, nc()))
		}
	}
	// Hand-made messages.
	locs := []any{nil, 0, 28800, -18000}
	for i, pbs := range pdPbRaws() {
		hs := []string{}
		for _, e := range pbs {
			hs = append(hs, pdPbHex(e))
		}
		for _, nc := range []bool{true, false} {
			g.Add(fmt.Sprintf("sys/pbraw/%d", i), "pbraw", hs, locs[rng.IntN(len(locs))], nc)
		}
	}
}
