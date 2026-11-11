package domains

// Domain "expr": block D08.1 part 1 — the plan-side expression nodes of pkg/expression
// (Column, CorrelatedColumn, Constant, Schema) and their row evaluation, plus the
// collationInfo part of collation.go (D08.2) the nodes embed.
//
// Values reuse the chunk domain's column specs and values (chunkTypeSpec, chunkAppend) and
// the datum domain's datum / field type specs (d2DatumIn, d2FtIn, d2DatumOut). An eval
// result is {"v": ...} / {"null": true} / {"err": msg} / {"panic": true}; the evaluation
// methods called are the ones Go's callers use for the node's type (exEvalSet).
//
// Records:
//   - "col": args [chunk spec, value, eval opts] -> Column.Eval* on a one-row chunk.
//   - "corr": args [chunk spec, datum, eval opts, in schema] -> CorrelatedColumn with Data set.
//   - "const": args [datum, ft, eval opts, param order|null] -> Constant.Eval*, String, HashCode...
//   - "new": args [name, n] -> the New* constructors.
//   - "colmisc": args [uid, id, orig name, hidden, chunk spec, charset] -> Column's plan-side methods.
//   - "schema": args [cols, keys, ukeys, queries, groups] -> Schema and its helpers.
//   - "tbl": args [TableInfo JSON, db name, alloc offset] -> TableInfo2SchemaAndNames.
//   - "evalbool": args [[datum...], eval opts] -> EvalBool over constants.
// Every record runs with new collations enabled.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/expression/exprctx"
	"github.com/pingcap/tidb/pkg/expression/exprstatic"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
)

const exprSeed = 20261112

func init() {
	vec.Register(&vec.Domain{
		Name:     "expr",
		Seed:     exprSeed,
		Coverage: exprCoverage,
		Blocks:   []string{"D08.1"},
		Exempt:   exprExempt(),
		Gzip:     true,
		Generate: exprGenerate,
		Exec:     exprExec,
	})
}

var exprCoverage = map[string]vec.Coverage{
	"col": {Min: 400, GoFuncs: []string{"Column.Eval", "Column.EvalInt", "Column.EvalReal", "Column.EvalString",
		"Column.EvalDecimal", "Column.EvalTime", "Column.EvalDuration", "Column.EvalJSON", "Column.EvalVectorFloat32",
		"Column.GetType", "Column.GetStaticType", "typeCtx"}},
	"corr": {Min: 300, GoFuncs: []string{"CorrelatedColumn.Eval", "CorrelatedColumn.EvalInt", "CorrelatedColumn.EvalReal",
		"CorrelatedColumn.EvalString", "CorrelatedColumn.EvalDecimal", "CorrelatedColumn.EvalTime",
		"CorrelatedColumn.EvalDuration", "CorrelatedColumn.EvalJSON", "CorrelatedColumn.EvalVectorFloat32",
		"CorrelatedColumn.Clone", "CorrelatedColumn.Equal", "CorrelatedColumn.EqualColumn", "CorrelatedColumn.IsCorrelated",
		"CorrelatedColumn.ConstLevel", "CorrelatedColumn.Decorrelate", "CorrelatedColumn.MemoryUsage",
		"CorrelatedColumn.RemapColumn", "CorrelatedColumn.ResolveIndices", "CorrelatedColumn.resolveIndices",
		"CorrelatedColumn.ResolveIndicesByVirtualExpr", "CorrelatedColumn.resolveIndicesByVirtualExpr",
		"CorrelatedColumn.Traverse"}},
	"const": {Min: 1500, GoFuncs: []string{"Constant.Eval", "Constant.EvalInt", "Constant.EvalReal", "Constant.EvalString",
		"Constant.EvalDecimal", "Constant.EvalTime", "Constant.EvalDuration", "Constant.EvalJSON",
		"Constant.EvalVectorFloat32", "Constant.getLazyDatum", "Constant.adjustDecimal", "Constant.GetType",
		"Constant.StringWithCtx", "Constant.Clone", "Constant.Equal", "Constant.IsCorrelated", "Constant.ConstLevel",
		"Constant.Decorrelate", "Constant.HashCode", "Constant.CanonicalHashCode", "Constant.getHashCode",
		"Constant.ResolveIndices", "Constant.resolveIndices", "Constant.ResolveIndicesByVirtualExpr",
		"Constant.resolveIndicesByVirtualExpr", "Constant.RemapColumn", "Constant.Vectorized", "Constant.Coercibility",
		"Constant.MemoryUsage", "Constant.Traverse", "ParamMarker.GetUserVar", "IsBinaryLiteral", "ExprNotNull"}},
	"new": {Min: 40, GoFuncs: []string{"NewOne", "NewSignedOne", "NewZero", "NewSignedZero", "NewUInt64Const",
		"NewUInt64ConstWithFieldType", "NewInt64Const", "NewStrConst", "NewNull", "NewNullWithFieldType"}},
	"colmisc": {Min: 200, GoFuncs: []string{"Column.String", "Column.string", "Column.StringWithCtx", "Column.HashCode",
		"Column.CanonicalHashCode", "Column.CleanHashCode", "Column.Clone", "Column.Equal", "Column.EqualColumn",
		"Column.IsCorrelated", "Column.ConstLevel", "Column.Decorrelate", "Column.Vectorized", "Column.ToInfo",
		"Column.Coercibility", "Column.Repertoire", "Column.MemoryUsage", "Column.Traverse", "Column2Exprs",
		"ColInfo2Col", "GcColumnExprIsTidbShard"}},
	"schema": {Min: 400, GoFuncs: []string{"NewSchema", "Schema.String", "KeyInfo.String", "KeyInfo.Clone", "Schema.Clone",
		"Schema.Equal", "Schema.Len", "Schema.Append", "Schema.SetKeys", "Schema.SetUniqueKeys", "Schema.ColumnIndex",
		"Schema.Contains", "Schema.RetrieveColumn", "Schema.IsUnique", "Schema.ColumnsIndices", "Schema.ColumnsByIndices",
		"Schema.ExtractColGroups", "Schema.GetExtraHandleColumn", "MergeSchema", "ExprReferenceSchema", "ExprFromSchema",
		"Column.ResolveIndices", "Column.resolveIndices", "Column.RemapColumn", "Column.InColumnArray", "SortColumns"}},
	"tbl":      {Min: 300, GoFuncs: []string{"TableInfo2SchemaAndNames", "ColumnInfos2ColumnsAndNames"}},
	"evalbool": {Min: 200, GoFuncs: []string{"EvalBool", "IsEQCondFromIn"}},
}

func exTry(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": true}
		}
	}()
	return f()
}

func exErr(err error) any { return map[string]any{"err": vec.GoString(err.Error())} }

// exVal: an evaluation result.
func exVal(v any, isNull bool, err error) any {
	if err != nil {
		return exErr(err)
	}
	if isNull {
		return map[string]any{"null": true}
	}
	return map[string]any{"v": v}
}

func exDatum(d types.Datum) any {
	h := codec.HashCode(nil, d)
	return []any{d.Kind(), hex.EncodeToString(h), vec.GoString(d.Collation())}
}

// exEvalSet: which Eval* methods Go's callers use for a node of this type and value
// (Constant: kind of the value too).
func exEvalSet(ft *types.FieldType, kind byte, isConst bool) []string {
	et := ft.EvalType()
	set := []string{"eval"}
	add := func(m string) {
		for _, s := range set {
			if s == m {
				return
			}
		}
		set = append(set, m)
	}
	hybrid := ft.Hybrid()
	if et == types.ETInt || hybrid || (isConst && (kind == types.KindString || kind == types.KindBinaryLiteral || kind == types.KindMysqlBit)) {
		add("int")
	}
	if et == types.ETReal || (isConst && (hybrid || kind == types.KindBinaryLiteral || kind == types.KindString)) {
		add("real")
	}
	if et == types.ETString || hybrid || isConst {
		add("string")
	}
	if et == types.ETDecimal || (isConst && (et == types.ETInt || et == types.ETReal || et == types.ETString)) {
		add("decimal")
	}
	if et == types.ETDatetime || et == types.ETTimestamp || (isConst && kind == types.KindMysqlTime) {
		add("time")
	}
	if et == types.ETDuration || (isConst && kind == types.KindMysqlDuration) {
		add("duration")
	}
	if et == types.ETJson || (isConst && kind == types.KindMysqlJSON) {
		add("json")
	}
	if et == types.ETVectorFloat32 || (isConst && kind == types.KindVectorFloat32) {
		add("vector")
	}
	return set
}

func exEvals(ctx expression.EvalContext, e expression.Expression, row chunk.Row, set []string) map[string]any {
	m := map[string]any{}
	for _, which := range set {
		m[which] = exTry(func() any {
			switch which {
			case "eval":
				d, err := e.Eval(ctx, row)
				return exVal(exDatum(d), false, err)
			case "int":
				v, n, err := e.EvalInt(ctx, row)
				return exVal(strconv.FormatInt(v, 10), n, err)
			case "real":
				v, n, err := e.EvalReal(ctx, row)
				return exVal(strconv.FormatUint(math.Float64bits(v), 10), n, err)
			case "string":
				v, n, err := e.EvalString(ctx, row)
				return exVal(hex.EncodeToString([]byte(v)), n, err)
			case "decimal":
				v, n, err := e.EvalDecimal(ctx, row)
				if err != nil || n {
					return exVal(nil, n, err)
				}
				return exVal(decOut(v), false, nil)
			case "time":
				v, n, err := e.EvalTime(ctx, row)
				return exVal(timeOut(v), n, err)
			case "duration":
				v, n, err := e.EvalDuration(ctx, row)
				return exVal([]any{strconv.FormatInt(int64(v.Duration), 10), v.Fsp}, n, err)
			case "json":
				v, n, err := e.EvalJSON(ctx, row)
				return exVal([]any{v.TypeCode, hex.EncodeToString(v.Value)}, n, err)
			case "vector":
				v, n, err := e.EvalVectorFloat32(ctx, row)
				if err != nil || n {
					return exVal(nil, n, err)
				}
				return exVal(v.String(), false, nil)
			}
			panic(which)
		})
	}
	m["warns"] = ecWarns(ctx.TruncateWarnings(0))
	return m
}

func exEvalCtx(raw json.RawMessage) *exprstatic.EvalContext {
	return exprstatic.NewEvalContext(ecEvalOpts(raw)...)
}

func exArr(raw json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		panic(err)
	}
	return a
}

// exSetParamOrder sets the unexported ParamMarker.order.
func exSetParamOrder(p *expression.ParamMarker, order int) {
	f := reflect.ValueOf(p).Elem().FieldByName("order")
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetInt(int64(order))
}

func exFtOut(ft *types.FieldType) any {
	if ft == nil {
		return nil
	}
	return []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), ft.GetDecimal(), ft.GetCharset(), ft.GetCollate()}
}

func exConstMisc(ctx expression.EvalContext, c *expression.Constant) map[string]any {
	m := map[string]any{}
	m["mem"] = exTry(func() any { return c.MemoryUsage() })
	m["level"] = int(c.ConstLevel())
	m["corr"] = c.IsCorrelated()
	m["vec"] = c.Vectorized()
	m["coer"] = exTry(func() any { return int(c.Coercibility()) })
	m["notnull"] = exTry(func() any { return expression.ExprNotNull(ctx, c) })
	m["binlit"] = expression.IsBinaryLiteral(c)
	m["type"] = exTry(func() any { return exFtOut(c.GetType(ctx)) })
	for _, r := range []string{errors.RedactLogDisable, errors.RedactLogEnable, errors.RedactLogMarker} {
		m["s_"+r] = exTry(func() any { return vec.GoString(c.StringWithCtx(ctx, r)) })
	}
	m["hash"] = exTry(func() any { return hex.EncodeToString(c.HashCode()) })
	m["chash"] = exTry(func() any { return hex.EncodeToString(c.Clone().CanonicalHashCode()) })
	m["eq_clone"] = exTry(func() any { return c.Equal(ctx, c.Clone()) })
	m["eq_one"] = exTry(func() any { return c.Equal(ctx, expression.NewOne()) })
	m["eq_col"] = exTry(func() any { return c.Equal(ctx, &expression.Column{RetType: c.RetType}) })
	m["resolve"] = exTry(func() any { e, err := c.ResolveIndices(expression.NewSchema()); return e == c && err == nil })
	m["rvirt"] = exTry(func() any { e, ok := c.ResolveIndicesByVirtualExpr(ctx, expression.NewSchema()); return e == c && ok })
	m["remap"] = exTry(func() any { e, err := c.RemapColumn(nil); return e == c && err == nil })
	m["decor"] = c.Decorrelate(nil) == c
	return m
}

func exColDump(c *expression.Column) any {
	return []any{c.ID, c.UniqueID, c.Index, c.OrigName, c.IsHidden, exFtOut(c.RetType)}
}

// exCols builds columns from [uid, id, isPrefix, origName].
func exCols(raw json.RawMessage) []*expression.Column {
	var specs [][]json.RawMessage
	if err := json.Unmarshal(raw, &specs); err != nil {
		panic(err)
	}
	cols := make([]*expression.Column, len(specs))
	for i, s := range specs {
		cols[i] = &expression.Column{UniqueID: arg[int64](s, 0), ID: arg[int64](s, 1), IsPrefix: arg[bool](s, 2),
			OrigName: arg[string](s, 3), RetType: types.NewFieldType(mysql.TypeLonglong), Index: i}
	}
	return cols
}

func exKeys(cols []*expression.Column, raw json.RawMessage) []expression.KeyInfo {
	var ks [][]int
	if err := json.Unmarshal(raw, &ks); err != nil {
		panic(err)
	}
	out := make([]expression.KeyInfo, 0, len(ks))
	for _, k := range ks {
		ki := expression.KeyInfo{}
		for _, i := range k {
			ki = append(ki, cols[i])
		}
		out = append(out, ki)
	}
	return out
}

func exUIDs(cols []*expression.Column) []int64 {
	out := make([]int64, len(cols))
	for i, c := range cols {
		out[i] = c.UniqueID
	}
	return out
}

func exprExec(op string, p []json.RawMessage) (out any, err error) {
	collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "col", "corr":
		var spec chunkTypeSpec
		must(json.Unmarshal(p[0], &spec))
		ft := spec.ft()
		ctx := exEvalCtx(p[2])
		if op == "col" {
			c := chunk.NewChunkWithCapacity([]*types.FieldType{ft}, 1)
			chunkAppend(c, ft, 0, p[1], false)
			col := &expression.Column{RetType: ft, Index: 0, UniqueID: 1}
			return exEvals(ctx, col, c.GetRow(0), exEvalSet(ft, 0, false)), nil
		}
		d := d2DatumIn(exArr(p[1]))
		cc := &expression.CorrelatedColumn{Column: expression.Column{RetType: ft, UniqueID: 7, OrigName: "t.c"}, Data: &d}
		m := exEvals(ctx, cc, chunk.Row{}, exEvalSet(ft, 0, false))
		inSchema := arg[bool](p, 3)
		sch := expression.NewSchema(&expression.Column{UniqueID: 8})
		if inSchema {
			sch.Append(&expression.Column{UniqueID: 7})
		}
		m["decor"] = exTry(func() any {
			_, isCol := cc.Decorrelate(sch).(*expression.Column)
			return isCol
		})
		m["mem"] = cc.MemoryUsage()
		// tidb-rs keeps Data in the evaluation state (spec §3.2): the replay adds dmem to
		// the column part and checks the sum.
		m["dmem"] = d.MemUsage()
		m["corr"] = cc.IsCorrelated()
		m["level"] = int(cc.ConstLevel())
		m["eq"] = []any{cc.Equal(ctx, cc.Clone()), cc.Equal(ctx, &cc.Column), cc.EqualColumn(&expression.CorrelatedColumn{Column: expression.Column{UniqueID: 8}})}
		m["remap"] = exTry(func() any {
			e, err := cc.RemapColumn(map[int64]*expression.Column{7: {UniqueID: 9, RetType: ft}})
			if err != nil {
				return exErr(err)
			}
			nc := e.(*expression.CorrelatedColumn)
			return []any{nc.UniqueID, nc.Data == cc.Data}
		})
		m["remap_miss"] = exTry(func() any { _, err := cc.RemapColumn(map[int64]*expression.Column{}); return exErr(err) })
		m["resolve"] = exTry(func() any {
			e, err := cc.ResolveIndices(sch)
			e2, ok := cc.ResolveIndicesByVirtualExpr(ctx, sch)
			return []any{e == expression.Expression(cc), err == nil, e2 == expression.Expression(cc), ok}
		})
		m["trav"] = cc.Traverse(nil2Action{}) == expression.Expression(cc)
		return m, nil
	case "const":
		d := d2DatumIn(exArr(p[0]))
		ft := d2FtIn(exArr(p[1]))
		ctx := exEvalCtx(p[2])
		c := &expression.Constant{Value: d, RetType: ft}
		if string(p[3]) != "null" {
			c.ParamMarker = &expression.ParamMarker{}
			exSetParamOrder(c.ParamMarker, arg[int](p, 3))
		}
		kind := d.Kind()
		if c.ParamMarker != nil {
			if pv, err := ctx.GetParamValue(arg[int](p, 3)); err == nil {
				kind = pv.Kind()
			}
		}
		m := exEvals(ctx, c, chunk.Row{}, exEvalSet(ft, kind, true))
		for k, v := range exConstMisc(ctx, c) {
			m[k] = v
		}
		m["trav"] = c.Traverse(nil2Action{}) == expression.Expression(c)
		return m, nil
	case "new":
		var c *expression.Constant
		n := arg[int64](p, 1)
		switch arg[string](p, 0) {
		case "one":
			c = expression.NewOne()
		case "sone":
			c = expression.NewSignedOne()
		case "zero":
			c = expression.NewZero()
		case "szero":
			c = expression.NewSignedZero()
		case "u64":
			c = expression.NewUInt64Const(int(n))
		case "u64ft":
			c = expression.NewUInt64ConstWithFieldType(uint64(n), types.NewFieldType(mysql.TypeLong))
		case "i64":
			c = expression.NewInt64Const(n)
		case "str":
			c = expression.NewStrConst(strconv.FormatInt(n, 10))
		case "null":
			c = expression.NewNull()
		case "nullft":
			c = expression.NewNullWithFieldType(types.NewFieldType(mysql.TypeVarchar))
		}
		ctx := exEvalCtx(json.RawMessage("[]"))
		return map[string]any{"d": exDatum(c.Value), "ft": exFtOut(c.RetType), "misc": exConstMisc(ctx, c)}, nil
	case "colmisc":
		var spec chunkTypeSpec
		must(json.Unmarshal(p[4], &spec))
		col := &expression.Column{UniqueID: arg[int64](p, 0), ID: arg[int64](p, 1), OrigName: arg[string](p, 2),
			IsHidden: arg[bool](p, 3), RetType: spec.ft()}
		if cs := arg[[]string](p, 5); len(cs) == 2 {
			col.SetCharsetAndCollation(cs[0], cs[1])
			col.SetRepertoire(expression.Repertoire(len(cs[0]) % 4))
			col.SetExplicitCharset(len(cs[1])%2 == 0)
		}
		m := map[string]any{}
		m["mem"] = col.MemoryUsage()
		m["str"] = vec.GoString(col.String())
		m["strctx"] = vec.GoString(col.StringWithCtx(exprctx.EmptyParamValues, errors.RedactLogEnable))
		m["hash"] = hex.EncodeToString(col.HashCode())
		m["chash"] = hex.EncodeToString(col.CanonicalHashCode())
		m["mem2"] = col.MemoryUsage()
		cl := col.Clone().(*expression.Column)
		cl.CleanHashCode()
		m["hash_clean"] = hex.EncodeToString(cl.HashCode())
		m["coer"] = int(col.Coercibility())
		m["rep"] = int(col.Repertoire())
		cs, co := col.CharsetAndCollation()
		m["cc"] = []any{cs, co, col.IsExplicitCharset()}
		m["eq"] = []any{col.Equal(nil, cl), col.EqualColumn(&expression.Column{UniqueID: col.UniqueID + 1}),
			col.Equal(nil, &expression.CorrelatedColumn{Column: *col})}
		m["misc"] = []any{col.IsCorrelated(), int(col.ConstLevel()), col.Vectorized(),
			col.Decorrelate(nil) == expression.Expression(col), col.Traverse(nil2Action{}) == expression.Expression(col)}
		info := col.ToInfo()
		m["info"] = []any{info.ID, exFtOut(&info.FieldType)}
		cols := []*expression.Column{{ID: col.ID + 1}, col, {ID: col.ID}}
		m["c2c"] = exTry(func() any { r := expression.ColInfo2Col(cols, info); return r != nil && r.UniqueID == cols[1].UniqueID })
		m["c2e"] = len(expression.Column2Exprs(cols))
		m["shard"] = []any{expression.GcColumnExprIsTidbShard(nil), expression.GcColumnExprIsTidbShard(col)}
		return m, nil
	case "schema":
		cols := exCols(p[0])
		s := expression.NewSchema(cols...)
		s.SetKeys(exKeys(cols, p[1]))
		s.SetUniqueKeys(exKeys(cols, p[2]))
		qs := exCols(p[3])
		m := map[string]any{}
		m["str"] = vec.GoString(s.String())
		m["len"] = s.Len()
		idx := []any{}
		for _, q := range qs {
			r := s.RetrieveColumn(q)
			var ru any
			if r != nil {
				ru = r.UniqueID
			}
			res := exTry(func() any {
				e, err := q.ResolveIndices(s)
				if err != nil {
					return exErr(err)
				}
				return e.(*expression.Column).Index
			})
			idx = append(idx, []any{s.ColumnIndex(q), s.Contains(q), ru, res, q.InColumnArray(cols), expression.ExprReferenceSchema(q, s), expression.ExprFromSchema(q, s)})
		}
		m["q"] = idx
		m["uniq"] = []any{s.IsUnique(true, qs...), s.IsUnique(false, qs...)}
		m["ci"] = s.ColumnsIndices(qs)
		var groups [][]int
		must(json.Unmarshal(p[4], &groups))
		cg := [][]*expression.Column{}
		for _, g := range groups {
			grp := []*expression.Column{}
			for _, i := range g {
				grp = append(grp, qs[i%len(qs)])
			}
			cg = append(cg, grp)
		}
		e, o := s.ExtractColGroups(cg)
		m["cg"] = []any{e, o}
		if ci := s.ColumnsIndices(qs); ci != nil {
			m["cbi"] = exUIDs(s.ColumnsByIndices(ci))
		}
		if h := s.GetExtraHandleColumn(); h != nil {
			m["handle"] = h.UniqueID
		}
		cl := s.Clone()
		m["clone"] = []any{vec.GoString(cl.String()), s.Equal(cl), s.Equal(expression.NewSchema(qs...)), len(cl.UniqueKeys)}
		m["merge"] = []any{
			vec.GoString(expression.MergeSchema(s, expression.NewSchema(qs...)).String()),
			vec.GoString(expression.MergeSchema(nil, s).String()),
			vec.GoString(expression.MergeSchema(s, nil).String()),
			expression.MergeSchema(nil, nil) == nil,
		}
		m["sorted"] = exUIDs(expression.SortColumns(qs))
		m["konst"] = []any{expression.ExprReferenceSchema(expression.NewOne(), s), expression.ExprFromSchema(expression.NewOne(), s)}
		m["remap"] = exTry(func() any {
			mp := map[int64]*expression.Column{}
			for _, c := range cols {
				mp[c.UniqueID] = &expression.Column{UniqueID: c.UniqueID + 100}
			}
			r := []any{}
			for _, q := range qs {
				e, err := q.RemapColumn(mp)
				if err != nil {
					r = append(r, exErr(err))
				} else {
					r = append(r, e.(*expression.Column).UniqueID)
				}
			}
			return r
		})
		s.Append(qs...)
		m["appended"] = s.Len()
		return m, nil
	case "tbl":
		tbl := &model.TableInfo{}
		must(json.Unmarshal([]byte(arg[string](p, 0)), tbl))
		bctx := exprstatic.NewExprContext(exprstatic.WithColumnIDAllocator(exprctx.NewSimplePlanColumnIDAllocator(arg[int64](p, 2))))
		return exTry(func() any {
			s, names, err := expression.TableInfo2SchemaAndNames(bctx, pmodel.NewCIStr(arg[string](p, 1)), tbl)
			if err != nil {
				return exErr(err)
			}
			cols := []any{}
			for _, c := range s.Columns {
				cols = append(cols, exColDump(c))
			}
			ns := []any{}
			for _, n := range names {
				ns = append(ns, vec.GoString(n.String()))
			}
			keys := [][]int64{}
			for _, k := range s.Keys {
				keys = append(keys, exUIDs(k))
			}
			return map[string]any{"str": vec.GoString(s.String()), "cols": cols, "names": ns, "keys": keys}
		}), nil
	case "evalbool":
		ctx := exEvalCtx(p[1])
		var ds [][]json.RawMessage
		must(json.Unmarshal(p[0], &ds))
		exprs := expression.CNFExprs{}
		for _, d := range ds {
			dd := d2DatumIn(d)
			exprs = append(exprs, &expression.Constant{Value: dd, RetType: types.NewFieldType(mysql.TypeVarchar)})
		}
		return exTry(func() any {
			b, n, err := expression.EvalBool(ctx, exprs, chunk.Row{})
			return map[string]any{"r": exVal([]any{b, n}, false, err), "warns": ecWarns(ctx.TruncateWarnings(0)),
				"eqin": expression.IsEQCondFromIn(exprs[0])}
		}), nil
	}
	return nil, fmt.Errorf("unknown op %s", op)
}

// nil2Action is a TraverseAction that returns its input.
type nil2Action struct{}

func (nil2Action) Transform(e expression.Expression) expression.Expression { return e }

var exprEvalOpts = [][][]any{
	{{"warn", "static"}},
	{{"warn", "static"}, {"flags", int(types.StrictFlags.WithTruncateAsWarning(true))}},
	{{"warn", "static"}, {"flags", int(types.DefaultStmtFlags.WithIgnoreTruncateErr(true))}},
	{{"warn", "static"}, {"params", []int64{5, -7}}},
}

func exprGenerate(g *vec.Gen, rng *rand.Rand) {
	cg := &chunkGen{rng: rng}
	colTypes := append(append([]chunkTypeSpec{}, chunkTypes...),
		chunkTypeSpec{mysql.TypeBit, mysql.UnsignedFlag, "binary", 8}, chunkTypeSpec{mysql.TypeBit, mysql.UnsignedFlag, "binary", 64})
	for _, spec := range colTypes {
		ft := spec.ft()
		for range 30 {
			g.Add("rand/col", "col", spec, cg.value(ft), exprEvalOpts[rng.IntN(len(exprEvalOpts))])
		}
		for range 20 {
			v := cg.value(ft)
			g.Add("rand/corr", "corr", spec, chunkDatumOf(ft, v, rng.IntN(3) == 0), exprEvalOpts[rng.IntN(len(exprEvalOpts))], rng.IntN(2) == 0)
		}
	}
	// Constants: a value of one type with the RetType of another (Go plans do this after
	// type inference), parameter markers, and the datum domain's literals.
	fts := []any{}
	for _, spec := range chunkTypes {
		ft := spec.ft()
		elems := any(nil)
		if ft.GetType() == mysql.TypeEnum || ft.GetType() == mysql.TypeSet {
			elems = []string{"a", "b", ""}
		}
		fts = append(fts, []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), []int{0, 2, 4}[rng.IntN(3)], ft.GetCharset(), ft.GetCollate(), elems})
	}
	fts = append(fts, []any{mysql.TypeNull, 0, 0, 0, "binary", "binary", nil}, []any{mysql.TypeBit, mysql.UnsignedFlag, 8, 0, "binary", "binary", nil})
	for range 2500 {
		spec := chunkTypes[rng.IntN(len(chunkTypes))]
		ft := spec.ft()
		d := chunkDatumOf(ft, cg.value(ft), rng.IntN(3) == 0)
		if rng.IntN(8) == 0 {
			d = []any{types.KindBinaryLiteral, hex.EncodeToString([]byte{byte(rng.IntN(256)), byte(rng.IntN(256))})[:2*(1+rng.IntN(2))]}
		}
		var pm any
		if rng.IntN(10) == 0 {
			pm = rng.IntN(3) // 2 is out of range: GetParamValue returns an error
		}
		rt := fts[rng.IntN(len(fts))]
		if rng.IntN(2) == 0 {
			// same type as the value
			rt = []any{ft.GetType(), ft.GetFlag(), ft.GetFlen(), ft.GetDecimal(), ft.GetCharset(), ft.GetCollate(), nil}
		}
		g.Add("rand/const", "const", d, rt, exprEvalOpts[rng.IntN(len(exprEvalOpts))], pm)
	}
	for _, name := range []string{"one", "sone", "zero", "szero", "u64", "u64ft", "i64", "str", "null", "nullft"} {
		for _, n := range []int64{0, 1, -1, 42, math.MaxInt64, math.MinInt64} {
			g.Add("sys/new", "new", name, n)
		}
	}
	css := [][]string{nil, {"utf8mb4", "utf8mb4_bin"}, {"ascii", "ascii_bin"}, {"binary", "binary"}, {"latin1", "latin1_bin"}}
	for range 300 {
		spec := chunkTypes[rng.IntN(len(chunkTypes))]
		name := []string{"", "t.a", "Column#9", "é"}[rng.IntN(4)]
		g.Add("rand/colmisc", "colmisc", rng.Int64N(1<<40)-(1<<39), []int64{0, 1, -1, model.ExtraHandleID, rng.Int64()}[rng.IntN(5)],
			name, rng.IntN(2) == 0, spec, css[rng.IntN(len(css))])
	}
	for range 600 {
		n := rng.IntN(6)
		cols := [][]any{}
		uids := []int64{}
		for i := range n {
			uid := rng.Int64N(8)
			uids = append(uids, uid)
			id := rng.Int64N(4)
			if rng.IntN(5) == 0 {
				id = model.ExtraHandleID
			}
			cols = append(cols, []any{uid, id, rng.IntN(4) == 0, []string{"", "a", fmt.Sprintf("t.c%d", i)}[rng.IntN(3)]})
		}
		randKeys := func() [][]int {
			ks := [][]int{}
			if n == 0 {
				return ks
			}
			for range rng.IntN(3) {
				k := []int{}
				for range 1 + rng.IntN(2) {
					k = append(k, rng.IntN(n))
				}
				ks = append(ks, k)
			}
			return ks
		}
		qs := [][]any{}
		for range 1 + rng.IntN(3) {
			uid := rng.Int64N(9)
			if len(uids) > 0 && rng.IntN(2) == 0 {
				uid = uids[rng.IntN(len(uids))]
			}
			qs = append(qs, []any{uid, rng.Int64N(3), rng.IntN(5) == 0, ""})
		}
		groups := [][]int{}
		for range rng.IntN(3) {
			groups = append(groups, []int{rng.IntN(3), rng.IntN(3)})
		}
		g.Add("rand/schema", "schema", cols, randKeys(), randKeys(), qs, groups)
	}
	exprGenerateTables(g, rng)
	for range 300 {
		ds := [][]any{}
		for range 1 + rng.IntN(3) {
			ds = append(ds, [][]any{
				{types.KindNull}, {types.KindInt64, rng.Int64N(3) - 1}, {types.KindString, []string{"0", "1", "abc", "1e2", " 0.0", ""}[rng.IntN(6)], "utf8mb4_bin"},
				{types.KindFloat64, math.Float64bits([]float64{0, 0.4, -1, math.NaN()}[rng.IntN(4)])},
				{types.KindMysqlDecimal, []string{"0", "0.00", "-1.5"}[rng.IntN(3)]},
			}[rng.IntN(5)])
		}
		g.Add("rand/evalbool", "evalbool", ds, exprEvalOpts[rng.IntN(len(exprEvalOpts))])
	}
}

// exprGenerateTables: the TableInfo texts exported from a v8.5.8 cluster (model domain).
// Tables with virtual generated columns need BuildSimpleExpr (the planner) and are skipped.
func exprGenerateTables(g *vec.Gen, rng *rand.Rand) {
	for _, t := range loadCluster() {
		if t.Type != "TableInfo" {
			continue
		}
		tbl := &model.TableInfo{}
		if json.Unmarshal([]byte(t.JSON), tbl) != nil {
			continue
		}
		virtual := false
		for _, c := range tbl.Columns {
			if c.IsVirtualGenerated() {
				virtual = true
			}
		}
		if virtual {
			continue
		}
		g.Add("cluster/tbl", "tbl", t.JSON, []string{"test", "Db", ""}[rng.IntN(3)], rng.Int64N(100))
	}
}
