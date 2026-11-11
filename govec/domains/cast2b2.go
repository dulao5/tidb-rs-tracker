package domains

// Domain "cast", part 2b-2 (appended after part 2b-1; earlier records are unchanged): CAST
// from TIME (DATE, DATETIME, TIMESTAMP), DURATION (TIME), JSON and VECTOR columns to INT,
// REAL, DECIMAL and STRING ("from" records), evaluated row by row and over a chunk like the
// other "cast" records. Durations are read by Go's vector versions with the column's
// declared fsp. CAST(TIME AS YEAR) reads the clock unless FlagCastTimeToYearThroughConcat
// is set, so YEAR targets of TIME columns always set it (the clock path is masked).

import (
	"encoding/hex"
	"fmt"
	"strings"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

var castFromSigs = []string{"TimeAsInt", "TimeAsReal", "TimeAsDecimal", "TimeAsString",
	"DurationAsInt", "DurationAsReal", "DurationAsDecimal", "DurationAsString",
	"JSONAsInt", "JSONAsReal", "JSONAsDecimal", "JSONAsString"}

func castFromSigFuncs() []string {
	out := []string{"builtinCastVectorFloat32AsStringSig.evalString", "builtinCastVectorFloat32AsStringSig.Clone"}
	for _, s := range castFromSigs {
		et := "String"
		for _, e := range []string{"Int", "Real", "Decimal"} {
			if strings.HasSuffix(s, "As"+e) {
				et = e
			}
		}
		out = append(out, "builtinCast"+s+"Sig.eval"+et, "builtinCast"+s+"Sig.vecEval"+et,
			"builtinCast"+s+"Sig.vectorized", "builtinCast"+s+"Sig.Clone")
	}
	return out
}

func init() {
	c := castCoverage["cast"]
	c.GoFuncs = append(c.GoFuncs, castFromSigFuncs()...)
	castCoverage["cast"] = c
}

var castJSONs = []string{`null`, `true`, `false`, `0`, `-1`, `1.5`, `-0.0`, `1e300`, `-1e300`, `18446744073709551615`,
	`9223372036854775807`, `-9223372036854775808`, `9223372036854775808`, `"123"`, `"-12.5e2"`, `"abc"`, `"2024-01-02"`,
	`""`, `"1e500"`, `[]`, `[1,2]`, `{}`, `{"a":1}`, `{"b":[true,null,"x"]}`, `0.1`, `123456789012345678901234567890`,
	`"é\n"`, `[{"k":"v"}]`, `12.3456789`, `-0.5`}

// fromCol: a TIME/DATETIME/DATE/TIMESTAMP, TIME (duration), JSON or VECTOR column type and a
// generator of its values.
func (g *castGen) fromCol() (*types.FieldType, func() []any) {
	switch g.pick(5) {
	case 0, 1:
		tp := []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp}[g.pick(3)]
		fsp := g.pick(7)
		if tp == mysql.TypeDate {
			fsp = 0
		}
		ft := castFt(tp, 0, -1, fsp)
		return ft, func() []any {
			var ct types.CoreTime
			switch g.pick(6) {
			case 0:
				ct = types.ZeroCoreTime
			case 1:
				ct = types.FromDate([]int{0, 1, 1000, 1970, 9999}[g.pick(5)], 1+g.pick(12), g.pick(32), g.pick(24), g.pick(60), g.pick(60), g.pick(1000000))
			default:
				ct = types.FromDate(1+g.pick(9999), 1+g.pick(12), 1+g.pick(28), g.pick(24), g.pick(60), g.pick(60), []int{0, 999999, 500000, 123456, g.pick(1000000)}[g.pick(5)])
			}
			if tp == mysql.TypeDate {
				ct = types.FromDate(ct.Year(), ct.Month(), ct.Day(), 0, 0, 0, 0)
			}
			return []any{types.KindMysqlTime, rawOfTime(types.NewTime(ct, tp, fsp))}
		}
	case 2:
		fsp := g.pick(7)
		ft := castFt(mysql.TypeDuration, 0, -1, fsp)
		return ft, func() []any {
			edges := []int64{0, 1, -1, int64(types.MaxTime), -int64(types.MaxTime), int64(gotime.Second) / 2,
				int64(gotime.Hour) * 838, 999999000, -999999000, 1500000000}
			d := edges[g.pick(len(edges))]
			if g.pick(2) == 0 {
				d = g.rng.Int64N(int64(types.MaxTime)) - g.rng.Int64N(int64(types.MaxTime))
			}
			if g.pick(2) == 0 {
				d = d / 1000 * 1000 // microsecond precision, as stored values have
			}
			return []any{types.KindMysqlDuration, d, fsp}
		}
	case 3:
		ft := castFt(mysql.TypeJSON, mysql.BinaryFlag, -1, 0)
		return ft, func() []any {
			j, err := types.ParseBinaryJSONFromString(castJSONs[g.pick(len(castJSONs))])
			if err != nil {
				panic(err)
			}
			return []any{types.KindMysqlJSON, j.TypeCode, hex.EncodeToString(j.Value)}
		}
	default:
		ft := castFt(mysql.TypeTiDBVectorFloat32, 0, -1, 0)
		return ft, func() []any {
			n := g.pick(5)
			s := "["
			for i := 0; i < n; i++ {
				if i > 0 {
					s += ","
				}
				s += []string{"0", "1", "-1.5", "3.4e38", "1e-45", "0.1", "123456.789", fmt.Sprint(g.pick(1000))}[g.pick(8)]
			}
			return []any{types.KindVectorFloat32, s + "]"}
		}
	}
}

func castGenerate2b2(vg *vec.Gen, g *castGen) {
	for i := 0; i < 2400; i++ {
		ft, gen := g.fromCol()
		n := 1 + g.pick(16)
		rows := make([]any, 0, n)
		for j := 0; j < n; j++ {
			if g.pick(8) == 0 {
				rows = append(rows, []any{types.KindNull})
			} else {
				rows = append(rows, gen())
			}
		}
		var tp *types.FieldType
		ctx := g.strCtx().([]any)
		ctx[0] = uint16(g.rng.IntN(1 << 9))
		switch g.pick(3) {
		case 0:
			tp = g.strTarget()
		case 1:
			tp = g.target()
			if ft.GetType() == mysql.TypeDuration && g.pick(4) == 0 {
				tp = castFt(mysql.TypeYear, mysql.UnsignedFlag, 4, 0)
				ctx[0] = ctx[0].(uint16) | uint16(types.FlagCastTimeToYearThroughConcat)
			}
		default:
			tp = g.target()
		}
		if ft.GetType() == mysql.TypeTiDBVectorFloat32 {
			tp = g.strTarget()
		}
		vg.Add("from", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(tp), g.pick(8) == 0, ctx, rows)
	}
	// TIME columns with an unspecified fsp (-1): Duration.ToNumber needs CheckFsp first.
	for i := 0; i < 100; i++ {
		ft := castFt(mysql.TypeDuration, 0, -1, types.UnspecifiedFsp)
		n := 1 + g.pick(8)
		rows := make([]any, 0, n)
		for j := 0; j < n; j++ {
			d := g.rng.Int64N(int64(types.MaxTime)) - g.rng.Int64N(int64(types.MaxTime))
			rows = append(rows, []any{types.KindMysqlDuration, d / 1000 * 1000, types.UnspecifiedFsp})
		}
		vg.Add("from-fsp", "cast", []any{"col", sfFtIn(ft)}, sfFtIn(g.target()), false, g.strCtx(), rows)
	}
}
