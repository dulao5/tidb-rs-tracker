package domains

// Domain "time": pkg/types/time.go, core_time.go, fsp.go (block D01.3), plus the
// parts of Go's time package they rely on (time.Location lookups, time.Date).
//
// Values:
//   - a types.Time is its raw 64-bit word (the embedded coreTime, with the fsp/type
//     bits) as a JSON number: "raw"; outputs add "s" = t.String().
//   - a types.Duration is {"d": nanoseconds, "fsp": n}; outputs add "s" = d.String().
//   - a time.Time is {"unix", "nsec", "zone"} as input and adds "loc" (Location
//     name) and "s" (String()) as output.
//   - a context is [flags, zone]: types.NewContext(types.Flags(flags), loc, h)
//     where h records the warnings (the record's "warnings").
//   - a zone is an id: "UTC" (time.UTC), "fixed:<name>:<offset>" (time.FixedZone),
//     "slim:<file>" / "fat:<file>" (time.LoadLocationFromTZData of an embedded TZif
//     file: testdata/tz/<slim|fat>/<file>, the name being <file> with '_' -> '/').
//     The vectors start with one "zone" record per TZif id carrying the file, so
//     the Rust replay loads exactly the bytes Go used.
//   - strings are vec.GoString (hex for invalid UTF-8).

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	gotime "time"
	"unsafe"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

const timeSeed = 20261107

//go:embed testdata/tz
var timeTZFS embed.FS

//go:embed testdata/time_literals.tsv
var timeLiteralsTSV string

func init() {
	vec.Register(&vec.Domain{
		Name:     "time",
		Seed:     timeSeed,
		Errors:   timeErrors,
		Coverage: timeCoverage,
		Blocks:   []string{"D01.3"},
		Exempt:   timeExempt,
		Gzip:     true,
		Generate: timeGenerate,
		Exec:     timeExec,
	})
}

var timeErrors = []vec.NamedErr{
	{Name: "ErrWrongValue", Err: types.ErrWrongValue},
	{Name: "ErrTruncatedWrongVal", Err: types.ErrTruncatedWrongVal},
	{Name: "ErrDatetimeFunctionOverflow", Err: types.ErrDatetimeFunctionOverflow},
	{Name: "ErrTimestampInDSTTransition", Err: types.ErrTimestampInDSTTransition},
	{Name: "ErrInvalidYearFormat", Err: types.ErrInvalidYearFormat},
	{Name: "ErrWarnDataOutOfRange", Err: types.ErrWarnDataOutOfRange},
	{Name: "ErrOverflow", Err: types.ErrOverflow},
	{Name: "ErrIncorrectDatetimeValue", Err: types.ErrIncorrectDatetimeValue},
}

var timeExempt = map[string]string{
	"CurrentTime":            "reads the wall clock (time.Now); the same path with a given instant is from_go_time",
	"Duration.ConvertToTime": "reads the wall clock; delegates to the path of ConvertToTimeWithTimestamp (dur_to_time)",
	"Duration.ConvertToYear": "reads the wall clock; delegates to ConvertToYearFromNow (dur_to_year)",
}

var timeCoverage = map[string]vec.Coverage{
	"zone":        {Min: 18},
	"zone_lookup": {Min: 2000},
	"go_date":     {Min: 2000},
	"parse_time": {Min: 5000, GoFuncs: []string{"ParseTime", "parseTime", "parseDatetime", "splitDateTime",
		"GetTimezone", "GetFracIndex", "ParseDateFormat", "isValidSeparator", "scanTimeArgs", "adjustYear",
		"ParseFrac", "CheckFsp", "FromDateChecked", "FromDate", "FromGoTime", "NewTime", "Time.SetType",
		"Time.Check", "checkDatetimeType", "checkDateType", "checkDateRange", "checkMonthDay",
		"checkTimestampType", "adjustTimestampErrForDST", "Time.ConvertTimeZone", "Time.String",
		"Time.DateFormat", "Time.convertDateFormat", "FormatIntWidthN", "Time.IsZero", "Time.Type", "Time.Fsp",
		"Time.getFspTt", "Time.setFspTt", "CoreTime.GoTime", "CoreTime.AdjustedGoTime", "Time.Compare",
		"compareTime", "datetimeToUint64", "CoreTime.String", "isLeapYear"}},
	"parse_time_float": {Min: 300, GoFuncs: []string{"ParseTimeFromFloatString"}},
	"parse_time_helper": {Min: 600, GoFuncs: []string{"ParseDatetime", "ParseTimestamp", "ParseDate",
		"GetFsp"}},
	"parse_time_num": {Min: 500, GoFuncs: []string{"ParseTimeFromNum", "parseDateTimeFromNum", "getTime",
		"Time.SetFsp"}},
	"parse_num_helper": {Min: 100, GoFuncs: []string{"ParseDatetimeFromNum", "ParseTimestampFromNum",
		"ParseDateFromNum"}},
	"parse_time_int64":   {Min: 100, GoFuncs: []string{"ParseTimeFromInt64"}},
	"parse_time_float64": {Min: 100, GoFuncs: []string{"ParseTimeFromFloat64", "Time.CoreTime", "Time.SetCoreTime"}},
	"parse_time_decimal": {Min: 100, GoFuncs: []string{"ParseTimeFromDecimal"}},
	"parse_time_year":    {Min: 5, GoFuncs: []string{"ParseTimeFromYear"}},
	"parse_duration": {Min: 2000, GoFuncs: []string{"ParseDuration", "matchDuration", "isNegativeDuration",
		"matchColon", "matchDayHHMMSS", "matchHHMMSSDelimited", "matchHHMMSSCompact", "hhmmssAddOverflow",
		"checkHHMMSS", "matchFrac", "canFallbackToDateTime", "TruncateOverflowMySQLTime", "Time.ConvertToDuration",
		"Duration.RoundFrac", "Duration.String", "Duration.formatFrac", "splitDuration"}},
	"time_props": {Min: 1000, GoFuncs: []string{"CoreTime.Year", "CoreTime.Month", "CoreTime.Day",
		"CoreTime.Hour", "CoreTime.Minute", "CoreTime.Second", "CoreTime.Microsecond", "CoreTime.getYear",
		"CoreTime.getMonth", "CoreTime.getDay", "CoreTime.getHour", "CoreTime.getMinute", "CoreTime.getSecond",
		"CoreTime.getMicrosecond", "CoreTime.Weekday", "CoreTime.YearWeek", "CoreTime.Week", "CoreTime.YearDay",
		"CoreTime.IsLeapYear", "calcWeek", "calcWeekday", "calcDaysInYear", "calcDaynr", "weekMode",
		"weekBehaviour.test", "Time.Clock", "Time.InvalidZero", "Time.ToNumber", "Time.FillNumber",
		"Time.ToPackedUint", "Time.MarshalJSON"}},
	"date_format":    {Min: 2000, GoFuncs: []string{"abbrDayOfMonth"}},
	"from_packed":    {Min: 200, GoFuncs: []string{"Time.FromPackedUint"}},
	"unmarshal_json": {Min: 20, GoFuncs: []string{"Time.UnmarshalJSON"}},
	"convert":        {Min: 500, GoFuncs: []string{"Time.Convert"}},
	"round_frac":     {Min: 500, GoFuncs: []string{"Time.RoundFrac", "roundTime"}},
	"compare_string": {Min: 200, GoFuncs: []string{"Time.CompareString"}},
	"time_sub":       {Min: 300, GoFuncs: []string{"Time.Sub", "calcTimeTimeDiff", "calcTimeDiffInternal"}},
	"time_add": {Min: 500, GoFuncs: []string{"Time.Add", "calcTimeDurationDiff", "calcTimeFromSec",
		"getDateFromDaynr", "CoreTime.setYear", "CoreTime.setMonth", "CoreTime.setDay", "CoreTime.setHour",
		"CoreTime.setMinute", "CoreTime.setSecond", "CoreTime.setMicrosecond"}},
	"convert_tz":     {Min: 300},
	"date_diff":      {Min: 200, GoFuncs: []string{"DateDiff"}},
	"timestamp_diff": {Min: 500, GoFuncs: []string{"TimestampDiff", "timestampDiff"}},
	"time_from_days": {Min: 200, GoFuncs: []string{"TimeFromDays"}},
	"overflow":       {Min: 300, GoFuncs: []string{"DateTimeIsOverflow"}},
	"str_to_date": {Min: 2000, GoFuncs: []string{"Time.StrToDate", "strToDate", "mysqlTimeFix", "getFormatToken",
		"skipWhiteSpace", "matchDateWithToken", "parseNDigits", "secondsNumeric", "minutesNumeric", "parseSep",
		"time12Hour", "time24Hour", "isAMOrPM", "dayOfMonthNumeric", "hour24Numeric", "hour12Numeric",
		"microSeconds", "yearNumericFourDigits", "yearNumericTwoDigits", "yearNumericNDigits", "dayOfYearNumeric",
		"abbreviatedMonth", "hasCaseInsensitivePrefix", "fullNameMonth", "monthNumeric", "skipAllNums",
		"skipAllPunct", "skipAllAlpha"}},
	"format_type": {Min: 200, GoFuncs: []string{"GetFormatType"}},
	"date_split":  {Min: 1000, GoFuncs: []string{"IsDateFormat", "DateFSP"}},
	"parse_year":  {Min: 200, GoFuncs: []string{"ParseYear"}},
	"adjust_year": {Min: 200, GoFuncs: []string{"AdjustYear"}},
	"go_time_ops": {Min: 500, GoFuncs: []string{"AddDate", "getFixDays", "GetLastDay", "RoundFrac",
		"TruncateFrac"}},
	"dur_format": {Min: 500, GoFuncs: []string{"Duration.DurationFormat", "Duration.convertDateFormat"}},
	"dur_props": {Min: 500, GoFuncs: []string{"Duration.Hour", "Duration.Minute", "Duration.Second",
		"Duration.MicroSecond", "Duration.ToNumber", "Duration.Neg", "Duration.Compare", "NewDuration",
		"MaxMySQLDuration"}},
	"dur_arith":            {Min: 500, GoFuncs: []string{"Duration.Add", "Duration.Sub"}},
	"dur_compare_string":   {Min: 200, GoFuncs: []string{"Duration.CompareString"}},
	"dur_to_time":          {Min: 300, GoFuncs: []string{"Duration.ConvertToTimeWithTimestamp", "mixDateAndDuration"}},
	"dur_to_year":          {Min: 200, GoFuncs: []string{"Duration.ConvertToYearFromNow"}},
	"extract_datetime_num": {Min: 300, GoFuncs: []string{"ExtractDatetimeNum"}},
	"extract_duration_num": {Min: 300, GoFuncs: []string{"ExtractDurationNum"}},
	"parse_duration_value": {Min: 500, GoFuncs: []string{"ParseDurationValue", "parseSingleTimeValue",
		"parseTimeValue", "alignFrac"}},
	"extract_duration_value": {Min: 500, GoFuncs: []string{"ExtractDurationValue",
		"parseAndValidateDurationValue"}},
	"unit_kind": {Min: 30, GoFuncs: []string{"IsClockUnit", "IsDateUnit", "IsMicrosecondUnit"}},
	"from_date": {Min: 100},
}

// ---------------------------------------------------------------- values

type timeRaw = uint64

func timeFromRaw(u uint64) types.Time { return *(*types.Time)(unsafe.Pointer(&u)) }
func rawOfTime(t types.Time) uint64   { return *(*uint64)(unsafe.Pointer(&t)) }

type timeOutT struct {
	Raw uint64       `json:"raw"`
	S   vec.GoString `json:"s"`
}

func timeOut(t types.Time) timeOutT { return timeOutT{Raw: rawOfTime(t), S: vec.GoString(t.String())} }

type durIn struct {
	D   int64 `json:"d"`
	Fsp int   `json:"fsp"`
}

type durOutT struct {
	D   int64        `json:"d"`
	Fsp int          `json:"fsp"`
	S   vec.GoString `json:"s"`
}

func durOut(d types.Duration) durOutT {
	return durOutT{D: int64(d.Duration), Fsp: d.Fsp, S: vec.GoString(d.String())}
}

func durOf(d durIn) types.Duration { return types.Duration{Duration: gotime.Duration(d.D), Fsp: d.Fsp} }

type goTimeIn struct {
	Unix int64  `json:"unix"`
	Nsec int64  `json:"nsec"`
	Zone string `json:"zone"`
}

type goTimeOutT struct {
	Unix int64        `json:"unix"`
	Nsec int          `json:"nsec"`
	Loc  string       `json:"loc"`
	S    vec.GoString `json:"s"`
}

func goTimeOut(t gotime.Time) goTimeOutT {
	return goTimeOutT{Unix: t.Unix(), Nsec: t.Nanosecond(), Loc: t.Location().String(), S: vec.GoString(t.String())}
}

func goTimeOf(g goTimeIn) gotime.Time { return gotime.Unix(g.Unix, g.Nsec).In(timeZone(g.Zone)) }

// ---------------------------------------------------------------- zones

var timeZones = map[string]*gotime.Location{}

// timeTZIDs: the TZif ids, in a fixed order.
func timeTZIDs() []string {
	var ids []string
	for _, kind := range []string{"slim", "fat"} {
		ents, err := timeTZFS.ReadDir("testdata/tz/" + kind)
		if err != nil {
			panic(err)
		}
		for _, e := range ents {
			ids = append(ids, kind+":"+e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

func timeTZData(id string) (name string, data []byte) {
	kind, file, _ := strings.Cut(id, ":")
	b, err := timeTZFS.ReadFile("testdata/tz/" + kind + "/" + file)
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(file, "_", "/"), b
}

func timeZone(id string) *gotime.Location {
	if id == "UTC" {
		return gotime.UTC
	}
	if l, ok := timeZones[id]; ok {
		return l
	}
	var l *gotime.Location
	if strings.HasPrefix(id, "fixed:") {
		rest := strings.TrimPrefix(id, "fixed:")
		i := strings.LastIndexByte(rest, ':')
		off, err := strconv.Atoi(rest[i+1:])
		if err != nil {
			panic(err)
		}
		l = gotime.FixedZone(rest[:i], off)
	} else {
		name, data := timeTZData(id)
		var err error
		if l, err = gotime.LoadLocationFromTZData(name, data); err != nil {
			panic(err)
		}
	}
	timeZones[id] = l
	return l
}

// ---------------------------------------------------------------- context

type timeWarns struct{ ws []error }

func (w *timeWarns) AppendWarning(err error) { w.ws = append(w.ws, err) }
func (w *timeWarns) AppendNote(err error)    { w.ws = append(w.ws, err) }

func timeCtx(args []json.RawMessage, i int) (types.Context, *timeWarns) {
	w := &timeWarns{}
	return types.NewContext(types.Flags(arg[uint16](args, i)), timeZone(arg[string](args, i+1)), w), w
}

func withW(w *timeWarns, out any) any {
	if len(w.ws) == 0 {
		return out
	}
	return vec.WithWarnings{Out: out, Warnings: w.ws}
}

// ---------------------------------------------------------------- exec

type tParsed struct {
	T timeOutT `json:"t"`
}

func timeExec(op string, args []json.RawMessage) (out any, err error) {
	s := func(i int) string { return string(arg[vec.GoString](args, i)) }
	switch op {
	case "zone":
		_, data := timeTZData(arg[string](args, 0))
		l := timeZone(arg[string](args, 0))
		return map[string]any{"data": vec.GoString(data), "name": l.String()}, nil
	case "zone_lookup":
		// The zone in effect at a Unix second: Go's Location.lookup through Time.Zone/ZoneBounds.
		t := gotime.Unix(arg[int64](args, 1), 0).In(timeZone(arg[string](args, 0)))
		name, off := t.Zone()
		start, end := t.ZoneBounds()
		bound := func(b gotime.Time) any {
			if b.IsZero() {
				return nil
			}
			return b.Unix()
		}
		return map[string]any{"name": name, "offset": off, "start": bound(start), "end": bound(end),
			"t": goTimeOut(t)}, nil
	case "go_date":
		v := arg[[]int](args, 0)
		t := gotime.Date(v[0], gotime.Month(v[1]), v[2], v[3], v[4], v[5], v[6], timeZone(arg[string](args, 1)))
		y, m, d := t.Date()
		h, mi, sec := t.Clock()
		return map[string]any{"t": goTimeOut(t), "date": []int{y, int(m), d, h, mi, sec, t.Nanosecond()},
			"wd": int(t.Weekday()), "yd": t.YearDay()}, nil
	case "parse_time", "parse_time_float":
		ctx, w := timeCtx(args, 3)
		f := types.ParseTime
		if op == "parse_time_float" {
			f = types.ParseTimeFromFloatString
		}
		t, err := f(ctx, s(0), arg[byte](args, 1), arg[int](args, 2))
		return withW(w, timeOut(t)), err
	case "parse_time_helper":
		ctx, w := timeCtx(args, 2)
		f := map[string]func(types.Context, string) (types.Time, error){
			"datetime": types.ParseDatetime, "timestamp": types.ParseTimestamp, "date": types.ParseDate,
		}[arg[string](args, 1)]
		t, err := f(ctx, s(0))
		return withW(w, timeOut(t)), err
	case "parse_time_num":
		ctx, w := timeCtx(args, 3)
		t, err := types.ParseTimeFromNum(ctx, arg[int64](args, 0), arg[byte](args, 1), arg[int](args, 2))
		return withW(w, timeOut(t)), err
	case "parse_num_helper":
		ctx, w := timeCtx(args, 2)
		f := map[string]func(types.Context, int64) (types.Time, error){
			"datetime": types.ParseDatetimeFromNum, "timestamp": types.ParseTimestampFromNum,
			"date": types.ParseDateFromNum,
		}[arg[string](args, 1)]
		t, err := f(ctx, arg[int64](args, 0))
		return withW(w, timeOut(t)), err
	case "parse_time_int64":
		ctx, w := timeCtx(args, 1)
		t, err := types.ParseTimeFromInt64(ctx, arg[int64](args, 0))
		return withW(w, timeOut(t)), err
	case "parse_time_float64":
		ctx, w := timeCtx(args, 1)
		t, err := types.ParseTimeFromFloat64(ctx, timeFloat(s(0)))
		return withW(w, timeOut(t)), err
	case "parse_time_decimal":
		ctx, w := timeCtx(args, 1)
		dec := new(types.MyDecimal)
		_ = dec.FromString([]byte(s(0)))
		t, err := types.ParseTimeFromDecimal(ctx, dec)
		return withW(w, timeOut(t)), err
	case "parse_time_year":
		t, err := types.ParseTimeFromYear(arg[int64](args, 0))
		return timeOut(t), err
	case "parse_duration":
		ctx, w := timeCtx(args, 2)
		d, isNull, err := types.ParseDuration(ctx, s(0), arg[int](args, 1))
		return withW(w, map[string]any{"d": durOut(d), "null": isNull}), err
	case "time_props":
		t := timeFromRaw(arg[uint64](args, 0))
		props := map[string]any{
			"core": t.CoreTime().String(), "y": t.Year(), "mo": t.Month(), "d": t.Day(), "h": t.Hour(),
			"mi": t.Minute(), "s": t.Second(), "us": t.Microsecond(), "tp": t.Type(), "fsp": t.Fsp(),
			"zero": t.IsZero(), "inv0": t.InvalidZero(), "leap": t.IsLeapYear(), "yday": t.YearDay(),
			"wd": int(t.Weekday()), "str": vec.GoString(t.String()),
		}
		weeks := make([]int, 8)
		yws := make([][2]int, 8)
		for m := range 8 {
			weeks[m] = t.Week(m)
			y, wk := t.YearWeek(m)
			yws[m] = [2]int{y, wk}
		}
		props["week"], props["yweek"] = weeks, yws
		props["num"] = t.ToNumber().String()
		p, _ := t.ToPackedUint()
		props["packed"] = p
		j, _ := t.MarshalJSON()
		props["json"] = string(j)
		return props, nil
	case "date_format":
		t := timeFromRaw(arg[uint64](args, 0))
		str, err := t.DateFormat(s(1))
		return vec.GoString(str), err
	case "from_packed":
		t := timeFromRaw(arg[uint64](args, 0))
		err := t.FromPackedUint(arg[uint64](args, 1))
		return timeOut(t), err
	case "unmarshal_json":
		t := timeFromRaw(arg[uint64](args, 0))
		err := t.UnmarshalJSON([]byte(s(1)))
		return rawOfTime(t), err
	case "convert":
		ctx, w := timeCtx(args, 2)
		t, err := timeFromRaw(arg[uint64](args, 0)).Convert(ctx, arg[byte](args, 1))
		return withW(w, timeOut(t)), err
	case "round_frac":
		ctx, w := timeCtx(args, 2)
		t, err := timeFromRaw(arg[uint64](args, 0)).RoundFrac(ctx, arg[int](args, 1))
		return withW(w, timeOut(t)), err
	case "compare_string":
		ctx, w := timeCtx(args, 2)
		r, err := timeFromRaw(arg[uint64](args, 0)).CompareString(ctx, s(1))
		return withW(w, r), err
	case "time_sub":
		ctx, w := timeCtx(args, 2)
		a, b := timeFromRaw(arg[uint64](args, 0)), timeFromRaw(arg[uint64](args, 1))
		return withW(w, map[string]any{"d": durOut(a.Sub(ctx, &b)), "cmp": a.Compare(b)}), nil
	case "time_add":
		ctx, w := timeCtx(args, 2)
		t := timeFromRaw(arg[uint64](args, 0))
		r, err := t.Add(ctx, durOf(arg[durIn](args, 1)))
		return withW(w, timeOut(r)), err
	case "convert_tz":
		t := timeFromRaw(arg[uint64](args, 0))
		err := t.ConvertTimeZone(timeZone(arg[string](args, 1)), timeZone(arg[string](args, 2)))
		return timeOut(t), err
	case "date_diff":
		a, b := timeFromRaw(arg[uint64](args, 0)), timeFromRaw(arg[uint64](args, 1))
		return types.DateDiff(a.CoreTime(), b.CoreTime()), nil
	case "timestamp_diff":
		a, b := timeFromRaw(arg[uint64](args, 1)), timeFromRaw(arg[uint64](args, 2))
		return types.TimestampDiff(arg[string](args, 0), a, b), nil
	case "time_from_days":
		return timeOut(types.TimeFromDays(arg[int64](args, 0))), nil
	case "overflow":
		ctx, w := timeCtx(args, 1)
		r, err := types.DateTimeIsOverflow(ctx, timeFromRaw(arg[uint64](args, 0)))
		return withW(w, r), err
	case "str_to_date":
		ctx, w := timeCtx(args, 3)
		t := timeFromRaw(arg[uint64](args, 0))
		ok := t.StrToDate(ctx, s(1), s(2))
		return withW(w, map[string]any{"ok": ok, "t": timeOut(t)}), nil
	case "format_type":
		d, dt := types.GetFormatType(s(0))
		return []bool{d, dt}, nil
	case "date_split":
		str := s(0)
		idx, sign, hour, sep, minute := types.GetTimezone(str)
		seps := types.ParseDateFormat(str)
		var gs []vec.GoString
		if seps != nil {
			gs = make([]vec.GoString, len(seps))
			for i, p := range seps {
				gs[i] = vec.GoString(p)
			}
		}
		return map[string]any{"tz": []any{idx, vec.GoString(sign), vec.GoString(hour), vec.GoString(sep),
			vec.GoString(minute)}, "seps": gs, "fsp": types.GetFsp(str), "frac": types.GetFracIndex(str),
			"is_date": types.IsDateFormat(str), "date_fsp": types.DateFSP(str)}, nil
	case "parse_year":
		y, err := types.ParseYear(s(0))
		return y, err
	case "adjust_year":
		y, err := types.AdjustYear(arg[int64](args, 0), arg[bool](args, 1))
		return y, err
	case "go_time_ops":
		g := goTimeOf(arg[goTimeIn](args, 0))
		v := arg[[]int64](args, 1)
		fsp := int(v[3])
		a, aerr := types.AddDate(v[0], v[1], v[2], g)
		r, rerr := types.RoundFrac(g, fsp)
		tr, terr := types.TruncateFrac(g, fsp)
		errStr := func(e error) any {
			if e == nil {
				return nil
			}
			return vec.GoString(e.Error())
		}
		return map[string]any{"add": goTimeOut(a), "add_err": errStr(aerr), "round": goTimeOut(r),
			"round_err": errStr(rerr), "trunc": goTimeOut(tr), "trunc_err": errStr(terr),
			"last_day": types.GetLastDay(int(v[0]), int(v[1])), "from_go": timeOut(types.NewTime(types.FromGoTime(g), mysql.TypeDatetime, 6))}, nil
	case "dur_format":
		str, err := durOf(arg[durIn](args, 0)).DurationFormat(s(1))
		return vec.GoString(str), err
	case "dur_props":
		d := durOf(arg[durIn](args, 0))
		return map[string]any{"h": d.Hour(), "m": d.Minute(), "s": d.Second(), "us": d.MicroSecond(),
			"num": d.ToNumber().String(), "neg": durOut(d.Neg()), "str": vec.GoString(d.String()),
			"cmp": d.Compare(types.NewDuration(1, 2, 3, 4, 0)),
			"max": durOut(types.MaxMySQLDuration(d.Fsp))}, nil
	case "dur_arith":
		a, b := durOf(arg[durIn](args, 0)), durOf(arg[durIn](args, 1))
		sum, err := a.Add(b)
		diff, derr := a.Sub(b)
		res := map[string]any{"add": durOut(sum), "sub": durOut(diff)}
		if derr != nil {
			res["sub_err"] = vec.GoString(derr.Error())
		}
		return res, err
	case "dur_compare_string":
		ctx, w := timeCtx(args, 2)
		r, err := durOf(arg[durIn](args, 0)).CompareString(ctx, s(1))
		return withW(w, r), err
	case "dur_to_time":
		ctx, w := timeCtx(args, 3)
		t, err := durOf(arg[durIn](args, 0)).ConvertToTimeWithTimestamp(ctx, arg[byte](args, 1),
			goTimeOf(arg[goTimeIn](args, 2)))
		return withW(w, timeOut(t)), err
	case "dur_to_year":
		ctx, w := timeCtx(args, 2)
		y, err := durOf(arg[durIn](args, 0)).ConvertToYearFromNow(ctx, goTimeOf(arg[goTimeIn](args, 1)))
		return withW(w, y), err
	case "extract_datetime_num":
		t := timeFromRaw(arg[uint64](args, 0))
		return types.ExtractDatetimeNum(&t, s(1))
	case "extract_duration_num":
		d := durOf(arg[durIn](args, 0))
		return types.ExtractDurationNum(&d, s(1))
	case "parse_duration_value":
		y, m, d, n, fsp, err := types.ParseDurationValue(s(0), s(1))
		return []int64{y, m, d, n, int64(fsp)}, err
	case "extract_duration_value":
		d, err := types.ExtractDurationValue(s(0), s(1))
		return durOut(d), err
	case "unit_kind":
		u := s(0)
		return []bool{types.IsClockUnit(u), types.IsDateUnit(u), types.IsMicrosecondUnit(u)}, nil
	case "from_date":
		v := arg[[]int](args, 0)
		ct, ok := types.FromDateChecked(v[0], v[1], v[2], v[3], v[4], v[5], v[6])
		return map[string]any{"checked": uint64(ct), "ok": ok,
			"raw": uint64(types.FromDate(v[0], v[1], v[2], v[3], v[4], v[5], v[6])),
			"fsp": timeFsp(v[7])}, nil
	}
	panic("time: unknown op " + op)
}

// timeFsp: CheckFsp and ParseFrac of a fsp (the from_date record carries them).
func timeFsp(fsp int) any {
	f, err := types.CheckFsp(fsp)
	res := map[string]any{"check": f}
	if err != nil {
		res["err"] = err.Error()
	}
	return res
}

// timeFloat parses the float64 of a record (strconv 'g' form, "NaN", "+Inf", "-Inf").
func timeFloat(s string) float64 {
	switch s {
	case "NaN":
		return math.NaN()
	case "+Inf":
		return math.Inf(1)
	case "-Inf":
		return math.Inf(-1)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return f
}

// ---------------------------------------------------------------- literals

type timeLit struct {
	fn  string
	str *string
	num *int64
}

func timeLiterals() []timeLit {
	var lits []timeLit
	sc := bufio.NewScanner(bytes.NewReader([]byte(timeLiteralsTSV)))
	for sc.Scan() {
		c := strings.SplitN(sc.Text(), "\t", 3)
		if len(c) != 3 {
			continue
		}
		switch c[1] {
		case "s":
			v, err := strconv.Unquote(c[2])
			if err != nil {
				panic(err)
			}
			lits = append(lits, timeLit{fn: c[0], str: &v})
		case "i":
			v, err := strconv.ParseInt(c[2], 10, 64)
			if err != nil {
				panic(err)
			}
			lits = append(lits, timeLit{fn: c[0], num: &v})
		}
	}
	return lits
}

// TimeMinedTests: the Go test functions whose literals are in testdata/time_literals.tsv
// (each must appear as a record src).
func TimeMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, l := range timeLiterals() {
		if !seen[l.fn] {
			seen[l.fn] = true
			fns = append(fns, l.fn)
		}
	}
	return fns
}
