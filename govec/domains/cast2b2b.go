package domains

// Domain "cast", part 2b-2b (appended after part 2b-2; earlier records are unchanged): CAST
// to DATETIME, DATE, TIMESTAMP and TIME (duration) from every argument type ("to"), with a
// fixed statement time in the context (CAST(TIME AS DATETIME) and JSON durations read it),
// constant arguments of these types ("to-const", folded at build time), arguments that are a
// CAST whose values carry another fsp than the declared one ("to-fsp": CAST(json AS TIME(n))
// keeps the JSON value's fsp; the outer CAST's vector version reads the declared fsp, its
// row version the value's), and WrapWithCastAsTime/Duration ("wrap" time, duration).

import (
	"encoding/hex"
	"math"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

var castToSigs = []string{"IntAsTime", "RealAsTime", "DecimalAsTime", "StringAsTime", "TimeAsTime",
	"DurationAsTime", "JSONAsTime", "IntAsDuration", "RealAsDuration", "DecimalAsDuration",
	"StringAsDuration", "TimeAsDuration", "DurationAsDuration", "JSONAsDuration"}

func castToSigFuncs() []string {
	out := []string{"castAsTimeFunctionClass.getFunction", "castAsDurationFunctionClass.getFunction",
		"WrapWithCastAsTime", "WrapWithCastAsDuration", "builtinCastVectorFloat32AsUnsupportedSig.Clone",
		"builtinCastVectorFloat32AsUnsupportedSig.evalInt", "builtinCastVectorFloat32AsUnsupportedSig.evalReal",
		"builtinCastVectorFloat32AsUnsupportedSig.evalDecimal", "builtinCastVectorFloat32AsUnsupportedSig.evalTime",
		"builtinCastVectorFloat32AsUnsupportedSig.evalDuration"}
	for _, s := range castToSigs {
		et := "Time"
		if s[len(s)-8:] == "Duration" {
			et = "Duration"
		}
		out = append(out, "builtinCast"+s+"Sig.eval"+et, "builtinCast"+s+"Sig.vecEval"+et,
			"builtinCast"+s+"Sig.vectorized", "builtinCast"+s+"Sig.Clone")
	}
	return out
}

func init() {
	c := castCoverage["cast"]
	c.GoFuncs = append(c.GoFuncs, castToSigFuncs()...)
	castCoverage["cast"] = c
}

// castNows: statement times (unix nanoseconds): an ordinary day, the day before a leap day at
// 23:59:59.999999, a DST-free new year, and the end of 9999.
var castNows = []int64{1709251199999999000, 1704067200000000000, 1731283200123456000, 253402300799000000}

// timeCtx: strCtx plus the statement time.
func (g *castGen) timeCtx() []any {
	c := g.strCtx().([]any)
	if g.pick(3) == 0 {
		c[3] = c[3].(uint64) | uint64(mysql.ModeNoZeroDate)
	}
	return append(c, castNows[g.pick(len(castNows))])
}

// timeTarget: CAST(.. AS DATETIME/DATE/TIMESTAMP/TIME [(fsp)]).
func (g *castGen) timeTarget() *types.FieldType {
	var flag uint
	if g.pick(4) == 0 {
		flag |= mysql.NotNullFlag
	}
	fsp := []int{0, 0, 1, 3, 6, g.pick(7)}[g.pick(6)]
	switch g.pick(4) {
	case 0:
		return castFt(mysql.TypeDate, flag, mysql.MaxDateWidth, 0)
	case 1:
		return castFt(mysql.TypeTimestamp, flag, mysql.MaxDatetimeWidthNoFsp, fsp)
	case 2:
		return castFt(mysql.TypeDuration, flag, mysql.MaxDurationWidthNoFsp, fsp)
	default:
		return castFt(mysql.TypeDatetime, flag, mysql.MaxDatetimeWidthNoFsp, fsp)
	}
}

var castTimeStrs = []string{"2024-01-02 03:04:05.123456", "2024-01-02", "20240102", "240102", "20240102030405",
	"2024-02-30", "0000-00-00", "0000-00-00 00:00:00", "2024-00-00", "9999-12-31 23:59:59.9999995", "12:34:56",
	"838:59:59", "-838:59:59", "839:00:00", "24:00:00", "1:2", "1 2:3:4", "-1 00:00:01.5", "abc", "", " ",
	"2024-01-02T03:04:05", "2024-1-2 3:4:5", "99:99:99", "1.5", "-12", "123456.789", "2024-01-02 25:00:00",
	"10000-01-01", "2024/01/02 03.04.05", "  2024-01-02  ", "11:59:59.9999999", "0:0:0", "-0:0:0.1",
	// Segments Atoi cannot hold: ParseTime returns the *strconv.NumError (only the vector
	// version of CAST(string AS DATETIME) turns it into ErrIncorrectDatetimeValue).
	"99999999999999999999-1-1 1:1:1", "2024-1-99999999999999999999", "2024-01-02T99999999999999999999"}

var castTimeInts = []int64{0, 1, 101, 1231, 101010, 20240102, 240102, 991231, 700101, 20241301, 20240102030405,
	99991231235959, 100000000000000, -1, -20240102, 8385959, 8390000, -8385959, 235959, 60, 2024, 1901, 2155, 69, 70}

// toArg: an argument column of any type and a generator of its values.
func (g *castGen) toArg() (*types.FieldType, func() []any) {
	switch g.pick(12) {
	case 0, 1:
		ft := castFt(mysql.TypeLonglong, 0, 20, 0)
		if g.pick(4) == 0 {
			ft = castFt(mysql.TypeYear, mysql.UnsignedFlag|mysql.ZerofillFlag, 4, 0)
		}
		return ft, func() []any {
			v := castTimeInts[g.pick(len(castTimeInts))]
			if g.pick(4) == 0 {
				v = g.intVal(false)[1].(int64)
			}
			if ft.GetType() == mysql.TypeYear && (v < 0 || v > 2155) {
				v = int64(1901 + g.pick(255))
			}
			return []any{types.KindInt64, v}
		}
	case 2:
		ft := castFt(mysql.TypeDouble, 0, -1, -1)
		return ft, func() []any {
			if g.pick(2) == 0 {
				return g.realVal()
			}
			f := float64(castTimeInts[g.pick(len(castTimeInts))]) + []float64{0, 0.5, 0.123456, 0.9999995, -0.25}[g.pick(5)]
			return []any{types.KindFloat64, math.Float64bits(f)}
		}
	case 3:
		ft := castFt(mysql.TypeNewDecimal, 0, 30, 6)
		return ft, func() []any {
			if g.pick(2) == 0 {
				return g.decVal()
			}
			s := []string{"20240102030405.123456", "20240102", "-1", "0", "0.5", "123456.999999", "8385959.5",
				"991231235959.9999995", "1000.000001"}[g.pick(9)]
			return []any{types.KindMysqlDecimal, s}
		}
	case 4, 5:
		ft := castFt(mysql.TypeVarchar, 0, 64, 0)
		return ft, func() []any {
			s := castTimeStrs[g.pick(len(castTimeStrs))]
			if g.pick(5) == 0 {
				return g.strVal(false)
			}
			return []any{types.KindString, vec.GoString(s), charset.CollationUTF8MB4}
		}
	case 6:
		ft, gen := g.jsonTimeCol()
		return ft, gen
	default:
		return g.fromCol()
	}
}

// jsonTimeCol: JSON values of every type code, including DATE/DATETIME/TIMESTAMP/TIME ones
// (types.CreateBinaryJSON of a Time or Duration) whose fsp is random.
func (g *castGen) jsonTimeCol() (*types.FieldType, func() []any) {
	ft := castFt(mysql.TypeJSON, mysql.BinaryFlag, -1, 0)
	return ft, func() []any {
		var j types.BinaryJSON
		switch g.pick(4) {
		case 0:
			fsp := g.pick(7)
			tp := []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp}[g.pick(3)]
			ct := types.FromDate(1+g.pick(9999), 1+g.pick(12), 1+g.pick(28), g.pick(24), g.pick(60), g.pick(60), g.pick(1000000))
			if tp == mysql.TypeDate {
				ct = types.FromDate(ct.Year(), ct.Month(), ct.Day(), 0, 0, 0, 0)
				fsp = 0
			}
			j = types.CreateBinaryJSON(types.NewTime(ct, tp, fsp))
		case 1:
			fsp := g.pick(7)
			d := g.rng.Int64N(int64(types.MaxTime)) - g.rng.Int64N(int64(types.MaxTime))
			j = types.CreateBinaryJSON(types.Duration{Duration: gotime.Duration(d / 1000 * 1000), Fsp: fsp})
		case 2:
			var err error
			j, err = types.ParseBinaryJSONFromString(`"` + castTimeStrs[g.pick(len(castTimeStrs))] + `"`)
			if err != nil {
				panic(err)
			}
		default:
			var err error
			j, err = types.ParseBinaryJSONFromString(castJSONs[g.pick(len(castJSONs))])
			if err != nil {
				panic(err)
			}
		}
		return []any{types.KindMysqlJSON, j.TypeCode, hex.EncodeToString(j.Value)}
	}
}

func castGenerate2b2b(vg *vec.Gen, g *castGen) {
	rows := func(gen func() []any, n int) []any {
		out := make([]any, 0, n)
		for j := 0; j < n; j++ {
			if g.pick(8) == 0 {
				out = append(out, []any{types.KindNull})
			} else {
				out = append(out, gen())
			}
		}
		return out
	}
	for i := 0; i < 3000; i++ {
		ft, gen := g.toArg()
		tp := g.timeTarget()
		if ft.GetType() == mysql.TypeTiDBVectorFloat32 && g.pick(2) == 0 {
			tp = g.target() // VectorFloat32AsUnsupported's evalInt/Real/Decimal
		}
		vg.Add("to", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(tp), false, g.timeCtx(), rows(gen, 1+g.pick(16)))
	}
	// Constant arguments: folded at build time (the build output dumps the folded constant).
	for i := 0; i < 400; i++ {
		ft, gen := g.toArg()
		vg.Add("to-const", "cast", []any{"const", gen(), sfFtIn(ft)}, sfFtIn(g.timeTarget()), false, g.timeCtx(), nil)
	}
	// CAST(CAST(json AS TIME(n)) AS ...): the inner values keep the JSON duration's fsp.
	for i := 0; i < 300; i++ {
		ft, gen := g.jsonTimeCol()
		inner := castFt(mysql.TypeDuration, 0, mysql.MaxDurationWidthNoFsp, g.pick(7))
		var tp *types.FieldType
		switch g.pick(3) {
		case 0:
			tp = g.strTarget()
		case 1:
			tp = g.timeTarget()
		default:
			tp = g.target()
		}
		vg.Add("to-fsp", "cast", []any{"cast", sfFtIn(inner), []any{"col", sfFtIn(ft)}}, sfFtIn(tp), false, g.timeCtx(), rows(gen, 1+g.pick(16)))
	}
	for i := 0; i < 300; i++ {
		ft, gen := g.toArg()
		a := []any{"col", sfFtIn(ft)}
		if g.pick(2) == 0 {
			a = []any{"const", gen(), sfFtIn(ft)}
		}
		if g.pick(2) == 0 {
			tp := []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp}[g.pick(3)]
			vg.Add("wrap-time", "wrap", "time", a, g.timeCtx(), sfFtIn(types.NewFieldType(tp)))
		} else {
			vg.Add("wrap-time", "wrap", "duration", a, g.timeCtx())
		}
	}
}
