package domains

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

// timeCtxs: the [flags, zone] contexts the cases rotate through.
var timeCtxs = [][2]any{
	{uint16(types.DefaultStmtFlags), "UTC"},
	{uint16(types.StrictFlags), "slim:Asia_Shanghai"},
	{uint16(types.DefaultStmtFlags | types.FlagIgnoreZeroInDateErr | types.FlagIgnoreInvalidDateErr), "slim:America_New_York"},
	{uint16(types.DefaultStmtFlags), "fat:America_New_York"},
	{uint16(types.StrictFlags | types.FlagIgnoreZeroInDateErr), "slim:Europe_London"},
	{uint16(types.DefaultStmtFlags | types.FlagCastTimeToYearThroughConcat), "slim:Australia_Lord_Howe"},
	{uint16(types.DefaultStmtFlags | types.FlagIgnoreInvalidDateErr), "fixed:UTC+05:30:19800"},
	{uint16(types.StrictFlags), "fat:Europe_Dublin"},
	{uint16(types.DefaultStmtFlags), "slim:Pacific_Chatham"},
	{uint16(types.DefaultStmtFlags | types.FlagIgnoreZeroInDateErr), "fixed::-36000"},
}

var timeTps = []byte{mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDate}

var timeUnits = []string{"MICROSECOND", "SECOND", "MINUTE", "HOUR", "DAY", "WEEK", "MONTH", "QUARTER", "YEAR",
	"SECOND_MICROSECOND", "MINUTE_MICROSECOND", "MINUTE_SECOND", "HOUR_MICROSECOND", "HOUR_SECOND",
	"HOUR_MINUTE", "DAY_MICROSECOND", "DAY_SECOND", "DAY_MINUTE", "DAY_HOUR", "YEAR_MONTH",
	"day", "Year_Month", "microsecond", "", "BAD", "WEEKS"}

// The format specifiers of DATE_FORMAT / STR_TO_DATE, plus some that are literal.
var timeSpecs = []string{"%b", "%M", "%m", "%c", "%D", "%d", "%e", "%j", "%H", "%k", "%h", "%I", "%l", "%i",
	"%p", "%r", "%T", "%S", "%s", "%f", "%U", "%u", "%V", "%v", "%a", "%W", "%w", "%X", "%x", "%Y", "%y",
	"%#", "%.", "%@", "%%", "%z", "%"}

var timeFixedFormats = []string{"%Y-%m-%d", "%Y-%m-%d %H:%i:%s", "%Y-%m-%d %H:%i:%s.%f", "%d/%m/%Y",
	"%b %e %Y %r", "%M %D %Y %T", "%Y%m%d%H%i%s", "%y-%c-%e %l:%i %p", "%j %Y", "%a %W %w %U %u %V %v %X %x",
	"%Y-%m-%d %h:%i:%s %p", "%H:%i:%s", "%#%Y", "%.%m%.%d", "%@%Y", "%Y年%m月%d日"}

func timeGenerate(g *vec.Gen, rng *rand.Rand) {
	ctxN := 0
	ctx := func() []any {
		c := timeCtxs[ctxN%len(timeCtxs)]
		ctxN++
		return []any{c[0], c[1]}
	}
	add := func(src, op string, args ...any) { g.Add(src, op, args...) }
	addc := func(src, op string, args ...any) { g.Add(src, op, append(args, ctx()...)...) }

	// ---- zones
	ids := timeTZIDs()
	for _, id := range ids {
		add("sys/zone", "zone", id)
	}
	zones := append([]string{"UTC", "fixed:UTC+08:00:28800", "fixed::-12600"}, ids...)
	for _, z := range zones {
		for range 120 {
			add("rand/zone", "zone_lookup", z, rng.Int64N(14e9)-5e9)
		}
		for _, y := range []int{1916, 1970, 1987, 2007, 2024, 2037, 2038, 2044, 2099, 2350} {
			for _, mo := range []gotime.Month{3, 4, 9, 10, 11} {
				d := 1 + rng.IntN(28)
				add("sys/zone", "zone_lookup", z, gotime.Date(y, mo, d, rng.IntN(24), 0, 0, 0, gotime.UTC).Unix())
			}
		}
		for range 100 {
			v := []int{1800 + rng.IntN(500), rng.IntN(15) - 1, rng.IntN(34) - 1, rng.IntN(27) - 1,
				rng.IntN(64) - 2, rng.IntN(64) - 2, rng.IntN(2e9) - 5e8}
			if rng.IntN(3) == 0 { // DST-ish hours of transition days
				v = []int{1990 + rng.IntN(70), []int{3, 4, 10, 11}[rng.IntN(4)], 1 + rng.IntN(31), rng.IntN(4),
					rng.IntN(60), 0, 0}
			}
			add("rand/date", "go_date", v, z)
		}
	}

	// ---- daylight saving time transitions: local wall times around each transition
	for _, z := range []string{"slim:America_New_York", "fat:America_New_York", "slim:Europe_London",
		"slim:Australia_Lord_Howe", "fat:Europe_Dublin", "slim:Pacific_Chatham", "fat:Australia_Sydney",
		"slim:America_Santiago", "slim:Africa_Casablanca"} {
		for _, d := range timeDSTDates(timeZone(z)) {
			for _, hm := range []string{"00:30:00", "01:00:00", "01:30:00", "01:59:59.999999", "02:00:00",
				"02:15:00", "02:30:00", "02:45:00.5", "02:59:59", "03:00:00", "03:30:00", "04:00:00"} {
				str := vec.GoString(d + " " + hm)
				for _, flags := range []uint16{uint16(types.DefaultStmtFlags), uint16(types.StrictFlags)} {
					add("sys/dst", "parse_time", str, mysql.TypeTimestamp, 6, flags, z)
				}
				add("sys/dst", "parse_time", str, mysql.TypeDatetime, 0, uint16(types.DefaultStmtFlags), z)
				t, _ := types.ParseTime(types.NewContext(types.DefaultStmtFlags, gotime.UTC, &timeWarns{}),
					string(str), mysql.TypeDatetime, 6)
				raw := rawOfTime(t)
				add("sys/dst", "convert", raw, mysql.TypeTimestamp, uint16(types.DefaultStmtFlags), z)
				add("sys/dst", "overflow", rawOfTime(types.NewTime(t.CoreTime(), mysql.TypeTimestamp, 6)),
					uint16(types.DefaultStmtFlags), z)
				add("sys/dst", "round_frac", raw, 0, uint16(types.DefaultStmtFlags), z)
				add("sys/dst", "convert_tz", raw, z, "UTC")
				add("sys/dst", "convert_tz", raw, "UTC", z)
			}
		}
	}

	// ---- literals of the Go tests
	sampleTimes := timeSampleRaws()
	for i, l := range timeLiterals() {
		if l.str != nil {
			s := vec.GoString(*l.str)
			for _, tp := range timeTps {
				for _, fsp := range []int{-1, 0, 6} {
					addc(l.fn, "parse_time", s, tp, fsp)
				}
			}
			addc(l.fn, "parse_time_float", s, mysql.TypeDatetime, 6)
			addc(l.fn, "parse_time_helper", s, []string{"datetime", "timestamp", "date"}[i%3])
			addc(l.fn, "parse_duration", s, 0)
			addc(l.fn, "parse_duration", s, 6)
			add(l.fn, "date_split", s)
			add(l.fn, "parse_year", s)
			add(l.fn, "format_type", s)
			add(l.fn, "unit_kind", s)
			add(l.fn, "unmarshal_json", sampleTimes[i%len(sampleTimes)], s)
			for k := range 2 {
				t := sampleTimes[(i+k)%len(sampleTimes)]
				add(l.fn, "date_format", t, s)
				addc(l.fn, "compare_string", t, s)
				add(l.fn, "dur_format", timeSampleDur(rng), s)
				addc(l.fn, "str_to_date", t, s, timeFixedFormats[(i+k)%len(timeFixedFormats)])
				addc(l.fn, "str_to_date", t, timeFormatTime(t, rng), s)
			}
			addc(l.fn, "dur_compare_string", timeSampleDur(rng), s)
			u := timeUnits[i%len(timeUnits)]
			add(l.fn, "parse_duration_value", vec.GoString(u), s)
			add(l.fn, "extract_duration_value", vec.GoString(u), s)
			add(l.fn, "parse_duration_value", s, vec.GoString(timeNumStr(rng)))
			add(l.fn, "extract_datetime_num", sampleTimes[i%len(sampleTimes)], s)
			add(l.fn, "extract_duration_num", timeSampleDur(rng), s)
		} else {
			n := *l.num
			for _, tp := range timeTps {
				addc(l.fn, "parse_time_num", n, tp, []int{-1, 0, 3, 6, 7}[i%5])
			}
			addc(l.fn, "parse_num_helper", n, []string{"datetime", "timestamp", "date"}[i%3])
			addc(l.fn, "parse_time_int64", n)
			add(l.fn, "time_from_days", n)
			add(l.fn, "adjust_year", n, i%2 == 0)
			add(l.fn, "from_packed", sampleTimes[i%len(sampleTimes)], uint64(n))
			if n == 0 || (n >= 1901 && n <= 2155) {
				add(l.fn, "parse_time_year", n)
			}
			add(l.fn, "time_props", uint64(n))
		}
	}

	// ---- random strings
	for range 4000 {
		s := vec.GoString(timeRandDateString(rng))
		tp := timeTps[rng.IntN(3)]
		addc("rand/str", "parse_time", s, tp, rng.IntN(9)-2)
		if rng.IntN(4) == 0 {
			addc("rand/str", "parse_time_helper", s, []string{"datetime", "timestamp", "date"}[rng.IntN(3)])
			add("rand/str", "date_split", s)
		}
	}
	for range 400 {
		addc("rand/float", "parse_time_float", vec.GoString(timeRandFloatString(rng)), timeTps[rng.IntN(3)], rng.IntN(7))
	}
	for range 2500 {
		addc("rand/dur", "parse_duration", vec.GoString(timeRandDurString(rng)), rng.IntN(9)-2)
	}
	for range 800 {
		n := timeRandNum(rng)
		addc("rand/num", "parse_time_num", n, timeTps[rng.IntN(3)], rng.IntN(8)-1)
		if rng.IntN(3) == 0 {
			addc("rand/num", "parse_num_helper", n, []string{"datetime", "timestamp", "date"}[rng.IntN(3)])
			addc("rand/num", "parse_time_int64", n)
		}
	}
	for range 300 {
		f := float64(timeRandNum(rng)) + float64(rng.IntN(1000000))/1e6
		if rng.IntN(5) == 0 {
			f = rng.NormFloat64() * 1e12
		}
		addc("rand/float64", "parse_time_float64", strconv.FormatFloat(f, 'g', -1, 64))
		dec := strconv.FormatInt(timeRandNum(rng), 10)
		if rng.IntN(4) != 0 {
			dec += "." + strconv.Itoa(rng.IntN(10000000))
		}
		addc("rand/dec", "parse_time_decimal", vec.GoString(dec))
	}
	for _, f := range []string{"NaN", "+Inf", "-Inf", "0", "-1", "1e19", "20170118.999999", "99991231235959.9999999"} {
		addc("sys/float64", "parse_time_float64", f)
	}

	// ---- random Time values
	for range 1500 {
		t := timeRandRaw(rng)
		add("rand/time", "time_props", t)
		layout := ""
		for range 1 + rng.IntN(6) {
			layout += timeSpecs[rng.IntN(len(timeSpecs))]
			if rng.IntN(2) == 0 {
				layout += []string{" ", "-", ":", "/", "x", "年", "\xff"}[rng.IntN(7)]
			}
		}
		add("rand/time", "date_format", t, vec.GoString(layout))
		addc("rand/time", "convert", t, timeTps[rng.IntN(3)])
		addc("rand/time", "round_frac", t, rng.IntN(9)-2)
		u := timeRandRaw(rng)
		addc("rand/time", "time_sub", t, u)
		addc("rand/time", "time_add", t, timeSampleDur(rng))
		add("rand/time", "convert_tz", t, zones[rng.IntN(len(zones))], zones[rng.IntN(len(zones))])
		add("rand/time", "date_diff", t, u)
		add("rand/time", "timestamp_diff", timeUnits[rng.IntN(10)], t, u)
		addc("rand/time", "overflow", t)
		add("rand/time", "extract_datetime_num", t, vec.GoString(timeUnits[rng.IntN(len(timeUnits))]))
		p, _ := timeFromRaw(t).ToPackedUint()
		add("rand/time", "from_packed", timeRandRaw(rng), p)
		add("rand/time", "from_packed", t, rng.Uint64()>>uint(rng.IntN(64)))
		add("rand/time", "time_from_days", rng.Int64N(3700000)-1000)
		// STR_TO_DATE of the time formatted with a random format, possibly perturbed.
		f := timeFixedFormats[rng.IntN(len(timeFixedFormats))]
		if rng.IntN(2) == 0 {
			f = layout
		}
		str, _ := timeFromRaw(t).DateFormat(f)
		if rng.IntN(4) == 0 && len(str) > 0 {
			b := []byte(str)
			b[rng.IntN(len(b))] = " 0aZ:.-\xff"[rng.IntN(8)]
			str = string(b)
		}
		if rng.IntN(5) == 0 {
			str = "  " + str + []string{"", " ", "x", " PM", " am"}[rng.IntN(5)]
		}
		addc("rand/strtodate", "str_to_date", timeRandRaw(rng), vec.GoString(str), vec.GoString(f))
		add("rand/time", "unmarshal_json", t, vec.GoString(strconv.FormatUint(rng.Uint64(), 10)))
		ts := timeFromRaw(t)
		add("rand/time", "unit_kind", vec.GoString(timeUnits[rng.IntN(len(timeUnits))]))
		add("rand/time", "from_date", []int{ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), ts.Second(),
			ts.Microsecond(), rng.IntN(10) - 2})
	}
	for _, v := range [][]int{{0, 0, 0, 0, 0, 0, 0, -1}, {16384, 0, 0, 0, 0, 0, 0, 7}, {2020, 16, 1, 1, 1, 1, 1, 0},
		{-1, 1, 1, 1, 1, 1, 1, -2}, {9999, 12, 31, 23, 59, 59, 999999, 6}, {1, 1, 32, 24, 60, 60, 1 << 20, 3}} {
		add("sys/from_date", "from_date", v)
	}
	for _, u := range []string{"0", "null", "-1", "1.5", "\"1\"", "true", "[]", "18446744073709551616", "x", " 7 "} {
		add("sys/json", "unmarshal_json", timeSampleRaws()[0], vec.GoString(u))
	}
	for _, u := range append(JSONEdgeCases("1234567890123"), "-0", "0.0", "1e2", "\"\xff\"", "{\"a\":1}",
		"18446744073709551615", "-1", " 1 ", "[\xff]") {
		add("sys/json_edge", "unmarshal_json", timeSampleRaws()[0], vec.GoString(u))
	}

	// ---- format types and splits
	for range 300 {
		f := ""
		for range 1 + rng.IntN(5) {
			f += timeSpecs[rng.IntN(len(timeSpecs))] + []string{"", " ", "-", "x"}[rng.IntN(4)]
		}
		add("rand/fmt", "format_type", vec.GoString(f))
	}
	for range 400 {
		add("rand/year", "parse_year", vec.GoString(timeRandYearString(rng)))
		add("rand/year", "adjust_year", rng.Int64N(3000)-200, rng.IntN(2) == 0)
	}

	// ---- go time.Time based
	for range 700 {
		g := goTimeIn{Unix: rng.Int64N(4e11) - 6.2e10, Nsec: rng.Int64N(1e9), Zone: zones[rng.IntN(len(zones))]}
		if rng.IntN(3) == 0 {
			g.Unix = rng.Int64N(3e9)
		}
		add("rand/gotime", "go_time_ops", g, []int64{rng.Int64N(200) - 100, rng.Int64N(400) - 200,
			rng.Int64N(800) - 400, rng.Int64N(9) - 1})
	}
	for _, d := range [][]int64{{10000 * 365, 0, 0, 0}, {-10000*365 - 1, 0, 0, 0}, {0, 3650001, 0, 6},
		{0, 0, -3650001, 7}, {0, 1, 0, -1}, {1, 1, 0, 0}, {0, 0, 0, 0}} {
		add("sys/gotime", "go_time_ops", goTimeIn{Unix: 1517356800, Nsec: 999999500, Zone: "UTC"}, d)
	}

	// ---- durations
	for range 600 {
		d := timeSampleDur(rng)
		add("rand/dur", "dur_props", d)
		f := ""
		for range 1 + rng.IntN(5) {
			f += timeSpecs[rng.IntN(len(timeSpecs))] + []string{"", ":", " ", "x"}[rng.IntN(4)]
		}
		add("rand/dur", "dur_format", d, vec.GoString(f))
		add("rand/dur", "dur_arith", d, timeSampleDur(rng))
		addc("rand/dur", "dur_compare_string", d, vec.GoString(timeRandDurString(rng)))
		g := goTimeIn{Unix: rng.Int64N(4e9), Nsec: rng.Int64N(1e9), Zone: zones[rng.IntN(len(zones))]}
		addc("rand/dur", "dur_to_time", d, timeTps[rng.IntN(3)], g)
		addc("rand/dur", "dur_to_year", d, g)
		add("rand/dur", "extract_duration_num", d, vec.GoString(timeUnits[rng.IntN(len(timeUnits))]))
	}
	for _, d := range []durIn{{D: 1<<63 - 1, Fsp: 6}, {D: -1 << 63, Fsp: 0}, {D: 0, Fsp: 0}, {D: 1, Fsp: 6}} {
		add("sys/dur", "dur_arith", d, d)
		add("sys/dur", "dur_arith", d, durIn{D: -d.D, Fsp: 3})
		add("sys/dur", "dur_props", durIn{D: d.D / 1000, Fsp: d.Fsp})
	}

	// ---- interval values
	for range 1500 {
		u := timeUnits[rng.IntN(len(timeUnits))]
		v := timeIntervalString(rng)
		add("rand/interval", "parse_duration_value", vec.GoString(u), vec.GoString(v))
		add("rand/interval", "extract_duration_value", vec.GoString(u), vec.GoString(v))
	}
}

// timeDSTDates: the local dates (YYYY-MM-DD) of the zone transitions of loc in a few years.
func timeDSTDates(loc *gotime.Location) []string {
	var out []string
	for _, y := range []int{1999, 2007, 2024, 2037, 2040, 2100} {
		t := gotime.Date(y, 1, 1, 0, 0, 0, 0, loc)
		limit := gotime.Date(y+1, 1, 1, 0, 0, 0, 0, loc)
		for range 6 {
			_, end := t.ZoneBounds()
			if end.IsZero() || !end.Before(limit) {
				break
			}
			out = append(out, end.In(loc).Format("2006-01-02"))
			t = end
		}
	}
	return out
}

// timeSampleRaws: a few fixed Time words (datetime/timestamp/date, fsp 0/3/6).
func timeSampleRaws() []timeRaw {
	mk := func(y, mo, d, h, mi, s, us int, tp byte, fsp int) timeRaw {
		return rawOfTime(types.NewTime(types.FromDate(y, mo, d, h, mi, s, us), tp, fsp))
	}
	return []timeRaw{
		mk(2017, 1, 18, 12, 34, 56, 789012, mysql.TypeDatetime, 6),
		mk(2000, 2, 29, 0, 0, 0, 0, mysql.TypeDate, 0),
		mk(1999, 12, 31, 23, 59, 59, 999999, mysql.TypeTimestamp, 3),
		mk(0, 0, 0, 0, 0, 0, 0, mysql.TypeDatetime, 0),
		mk(2024, 3, 10, 2, 30, 0, 0, mysql.TypeTimestamp, 0),
		mk(9999, 12, 31, 23, 59, 59, 999999, mysql.TypeDatetime, 6),
		mk(2021, 0, 15, 8, 0, 0, 0, mysql.TypeDatetime, 2),
	}
}

func timeRandRaw(rng *rand.Rand) timeRaw {
	y := rng.IntN(10000)
	switch rng.IntN(6) {
	case 0:
		y = 1970 + rng.IntN(70)
	case 1:
		y = rng.IntN(3)
	}
	mo, d := rng.IntN(13), rng.IntN(32)
	if rng.IntN(4) != 0 {
		mo, d = 1+rng.IntN(12), 1+rng.IntN(28)
	}
	h, mi, s := rng.IntN(24), rng.IntN(60), rng.IntN(60)
	if rng.IntN(20) == 0 {
		h, mi, s = rng.IntN(32), rng.IntN(64), rng.IntN(64)
	}
	us := rng.IntN(1000000)
	if rng.IntN(3) == 0 {
		us = 0
	}
	tp := timeTps[rng.IntN(3)]
	fsp := rng.IntN(7)
	if rng.IntN(10) == 0 {
		return rawOfTime(types.NewTime(types.FromDate(y, mo, d, h, mi, s, us), tp, fsp)) | uint64(rng.IntN(16))
	}
	return rawOfTime(types.NewTime(types.FromDate(y, mo, d, h, mi, s, us), tp, fsp))
}

func timeSampleDur(rng *rand.Rand) durIn {
	var d int64
	switch rng.IntN(5) {
	case 0:
		d = rng.Int64N(int64(types.MaxTime)*2) - int64(types.MaxTime)
	case 1:
		d = (rng.Int64N(86400) - 43200) * int64(gotime.Second)
	case 2:
		d = rng.Int64N(1e15) - 5e14
	case 3:
		d = rng.Int64N(int64(types.MaxTime)*2) - int64(types.MaxTime)
		d -= d % int64(gotime.Microsecond)
	default:
		d = int64(rng.Uint64())
	}
	return durIn{D: d, Fsp: rng.IntN(7)}
}

func timeFormatTime(t timeRaw, rng *rand.Rand) vec.GoString {
	s, _ := timeFromRaw(t).DateFormat(timeFixedFormats[rng.IntN(len(timeFixedFormats))])
	return vec.GoString(s)
}

func timeNumStr(rng *rand.Rand) string {
	s := strconv.FormatInt(rng.Int64N(2000000)-1000000, 10)
	if rng.IntN(2) == 0 {
		s += "." + strconv.Itoa(rng.IntN(100000000))
	}
	return s
}

func timeRandNum(rng *rand.Rand) int64 {
	switch rng.IntN(7) {
	case 0:
		return rng.Int64N(1000)
	case 1:
		return rng.Int64N(1000000)
	case 2:
		return 19000101 + rng.Int64N(81000000)
	case 3:
		return 10000101000000 + rng.Int64N(89999999999999)
	case 4:
		return rng.Int64N(1000000000000)
	case 5:
		return -rng.Int64N(100000000000)
	}
	return int64(rng.Uint64())
}

func timeRandYearString(rng *rand.Rand) string {
	switch rng.IntN(5) {
	case 0:
		return strconv.Itoa(rng.IntN(100))
	case 1:
		return strconv.Itoa(1800 + rng.IntN(500))
	case 2:
		return strconv.Itoa(rng.IntN(100000) - 1000)
	case 3:
		return []string{"", " 12", "0x1", "abc", "+70", "-0", "00", "1999 ", "99999"}[rng.IntN(9)]
	}
	return strconv.Itoa(rng.IntN(10))
}

func timeRandDigits(rng *rand.Rand, n int) string {
	var b strings.Builder
	for range n {
		b.WriteByte(byte('0' + rng.IntN(10)))
	}
	return b.String()
}

// timeRandDateString: date-time literals in the shapes parseDatetime handles, and broken ones.
func timeRandDateString(rng *rand.Rand) string {
	seps := []string{"-", "/", ".", ":", "@", "--", " ", "T", "^", "_", ","}
	pick := func(a []string) string { return a[rng.IntN(len(a))] }
	var s string
	switch rng.IntN(6) {
	case 0: // compact digits
		s = timeRandDigits(rng, 1+rng.IntN(15))
	case 1, 2, 3:
		y := []string{timeRandDigits(rng, 4), timeRandDigits(rng, 2), timeRandDigits(rng, 1), timeRandDigits(rng, 3)}[rng.IntN(4)]
		s = y + pick(seps) + fmt.Sprintf("%0*d", 1+rng.IntN(2), rng.IntN(14)) + pick(seps) +
			fmt.Sprintf("%0*d", 1+rng.IntN(2), rng.IntN(33))
		if rng.IntN(3) != 0 {
			s += pick([]string{" ", "T", "  ", "\t", "-", ":"}) + fmt.Sprintf("%02d", rng.IntN(26))
			for range rng.IntN(3) {
				s += pick([]string{":", ".", "-", ":"}) + fmt.Sprintf("%02d", rng.IntN(62))
			}
		}
	case 4: // with fraction and/or timezone
		t := gotime.Unix(rng.Int64N(4e9), 0).UTC()
		s = t.Format("2006-01-02 15:04:05")
		if rng.IntN(2) == 0 {
			s = t.Format("20060102150405")
		}
		if rng.IntN(3) != 0 {
			s += "." + timeRandDigits(rng, 1+rng.IntN(9))
		}
	default: // garbage
		chars := "0123456789-:. TZ+aA\xff"
		var b strings.Builder
		for range rng.IntN(22) {
			b.WriteByte(chars[rng.IntN(len(chars))])
		}
		s = b.String()
	}
	if rng.IntN(4) == 0 {
		s += pick([]string{"Z", "+08:00", "-05:30", "+0800", "+08", "-00:00", "+14:00", "+14:01", "-15:00",
			"+08:0a", "z", " +08:00", "+8:00"})
	}
	if rng.IntN(10) == 0 {
		s = pick([]string{" ", "  ", "\t"}) + s + pick([]string{"", " ", "x", "\n"})
	}
	return s
}

func timeRandFloatString(rng *rand.Rand) string {
	n := timeRandNum(rng)
	if n < 0 {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if rng.IntN(3) != 0 {
		s += "." + timeRandDigits(rng, 1+rng.IntN(7))
	}
	if rng.IntN(15) == 0 {
		s = "0.0" + timeRandDigits(rng, rng.IntN(3))
	}
	return s
}

func timeRandDurString(rng *rand.Rand) string {
	pick := func(a []string) string { return a[rng.IntN(len(a))] }
	var s string
	switch rng.IntN(6) {
	case 0:
		s = fmt.Sprintf("%d:%d:%d", rng.IntN(900), rng.IntN(70), rng.IntN(70))
	case 1:
		s = fmt.Sprintf("%d %d:%02d:%02d", rng.IntN(40), rng.IntN(30), rng.IntN(60), rng.IntN(60))
	case 2:
		s = timeRandDigits(rng, 1+rng.IntN(16))
	case 3:
		s = fmt.Sprintf("%d:%d", rng.IntN(900), rng.IntN(70))
	case 4:
		s = timeRandDateString(rng)
	default:
		chars := "0123456789-: .\t"
		var b strings.Builder
		for range rng.IntN(16) {
			b.WriteByte(chars[rng.IntN(len(chars))])
		}
		s = b.String()
	}
	if rng.IntN(3) == 0 {
		s += "." + timeRandDigits(rng, rng.IntN(9))
	}
	if rng.IntN(5) == 0 {
		s = "-" + pick([]string{"", " "}) + s
	}
	if rng.IntN(8) == 0 {
		s = pick([]string{" ", "\t"}) + s + pick([]string{"", " ", "x", "abc"})
	}
	return s
}

func timeIntervalString(rng *rand.Rand) string {
	pick := func(a []string) string { return a[rng.IntN(len(a))] }
	var parts []string
	for range 1 + rng.IntN(6) {
		parts = append(parts, strconv.Itoa(rng.IntN(1000000)))
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += pick([]string{":", " ", ".", "-", "@", "  "}) + p
	}
	if rng.IntN(6) == 0 {
		s = pick([]string{"-", " -", "+"}) + s
	}
	if rng.IntN(8) == 0 {
		s = pick([]string{"9223372036854775808", "1e5", "abc", "", "1.", ".5", "-.5", "99999999999999999999.9"})
	}
	return s
}
