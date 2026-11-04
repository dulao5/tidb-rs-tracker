package domains

// Domain "decimal": pkg/types/mydecimal.go (MyDecimal), porting block D01.2.
//
// Argument encoding (JSON):
//   decimal  - string, parsed with (*MyDecimal).FromString; a parse error is
//              ignored and the resulting value is used (as Go would).
//   int64    - JSON number;  uint64 - JSON number
//   float64  - string, strconv.FormatFloat(f, 'g', -1, 64) ("NaN", "+Inf", "-Inf" for specials)
//   bytes    - lowercase hex string
//   round mode - "HalfUp" | "Truncate" | "Ceiling"
//
// Decimal results are encoded as Dec (see below): ToString(), String() and the
// full internal state, so a port can check resultFrac and word layout too.
// The ops, their arguments and outputs are listed in decimalCoverage.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/types"
)

func init() {
	vec.Register(&vec.Domain{
		Name: "decimal",
		Seed: decimalSeed,
		Errors: []vec.NamedErr{
			{Name: "ErrTruncated", Err: types.ErrTruncated},
			{Name: "ErrOverflow", Err: types.ErrOverflow},
			{Name: "ErrDivByZero", Err: types.ErrDivByZero},
			{Name: "ErrBadNumber", Err: types.ErrBadNumber},
			{Name: "ErrTruncatedWrongVal", Err: types.ErrTruncatedWrongVal},
		},
		Coverage: decimalCoverage,
		Generate: decimalGenerate,
		Exec:     decimalExec,
	})
}

// decimalCoverage: op -> minimum record count and Go API exercised.
var decimalCoverage = map[string]vec.Coverage{
	// args: [decimal string]                 out: Dec
	"from_string": {Min: 500, GoFuncs: []string{"(*MyDecimal).FromString", "(*MyDecimal).ToString", "(*MyDecimal).String"}},
	// args: [int64]                          out: Dec
	"from_int": {Min: 30, GoFuncs: []string{"(*MyDecimal).FromInt", "NewDecFromInt"}},
	// args: [uint64]                         out: Dec
	"from_uint": {Min: 30, GoFuncs: []string{"(*MyDecimal).FromUint", "NewDecFromUint"}},
	// args: [float64]                        out: Dec
	"from_float64": {Min: 100, GoFuncs: []string{"(*MyDecimal).FromFloat64"}},
	// args: [decimal]                        out: int64 (valid also when err != null)
	"to_int": {Min: 200, GoFuncs: []string{"(*MyDecimal).ToInt"}},
	// args: [decimal]                        out: uint64
	"to_uint": {Min: 200, GoFuncs: []string{"(*MyDecimal).ToUint"}},
	// args: [decimal]                        out: float64 string (FormatFloat 'g' -1)
	"to_float64": {Min: 200, GoFuncs: []string{"(*MyDecimal).ToFloat64"}},
	// args: [decimal, frac int, mode]        out: Dec    (fresh `to`)
	"round": {Min: 1000, GoFuncs: []string{"(*MyDecimal).Round"}},
	// args: [decimal, frac int, mode]        out: Dec    (d.Round(d, ...), `to` aliases `d`)
	"round_inplace": {Min: 200, GoFuncs: []string{"(*MyDecimal).Round"}},
	// args: [decimal, shift int]             out: Dec (d after Shift)
	"shift": {Min: 300, GoFuncs: []string{"(*MyDecimal).Shift"}},
	// args: [a, b]                           out: Dec
	"add": {Min: 500, GoFuncs: []string{"DecimalAdd"}},
	"sub": {Min: 500, GoFuncs: []string{"DecimalSub"}},
	"mul": {Min: 500, GoFuncs: []string{"DecimalMul"}},
	// args: [a, b, fracIncr int]             out: Dec
	"div": {Min: 500, GoFuncs: []string{"DecimalDiv"}},
	// args: [a, b]                           out: Dec
	"mod": {Min: 500, GoFuncs: []string{"DecimalMod"}},
	// args: [a]                              out: Dec
	"neg": {Min: 50, GoFuncs: []string{"DecimalNeg"}},
	// args: [a, b]                           out: -1|0|1
	"compare": {Min: 500, GoFuncs: []string{"(*MyDecimal).Compare"}},
	// args: [decimal, precision, frac]       out: hex|null
	"to_bin": {Min: 500, GoFuncs: []string{"(*MyDecimal).ToBin", "(*MyDecimal).WriteBin"}},
	// args: [hex, precision, frac]           out: {"n": binSize, "dec": Dec}
	"from_bin": {Min: 300, GoFuncs: []string{"(*MyDecimal).FromBin"}},
	// args: [precision, frac]                out: int
	"bin_size": {Min: 100, GoFuncs: []string{"DecimalBinSize"}},
	// args: [hex]                            out: int
	"peak": {Min: 30, GoFuncs: []string{"DecimalPeak"}},
	// args: [decimal]                        out: hex
	"to_hash_key": {Min: 300, GoFuncs: []string{"(*MyDecimal).ToHashKey"}},
	// args: [decimal]                        out: Props
	"props": {Min: 300, GoFuncs: []string{"(*MyDecimal).GetDigitsInt", "(*MyDecimal).GetDigitsFrac", "(*MyDecimal).IsNegative", "(*MyDecimal).IsZero", "(*MyDecimal).PrecisionAndFrac"}},
	// args: [negative bool, prec, frac]      out: Dec
	"max_or_min": {Min: 50, GoFuncs: []string{"NewMaxOrMinDec"}},
	// args: [decimal]                        out: JSON text (string)
	"marshal_json": {Min: 50, GoFuncs: []string{"(*MyDecimal).MarshalJSON"}},
	// args: [JSON text string]               out: Dec
	"unmarshal_json": {Min: 50, GoFuncs: []string{"(*MyDecimal).UnmarshalJSON"}},
}

// Dec is the JSON form of a MyDecimal result.
type Dec struct {
	S   string    `json:"s"`   // ToString()
	Str string    `json:"str"` // String(): rounded to resultFrac with HalfUp
	St  [13]int32 `json:"st"`  // internal state, see decState
}

// decState packs the unexported MyDecimal fields (read via MarshalJSON, which
// dumps all of them) as
//
//	[digitsInt, digitsFrac, resultFrac, negative(0|1), wordBuf[0..8]...]
func decState(d *types.MyDecimal) [13]int32 {
	j, err := d.MarshalJSON()
	if err != nil {
		panic(err)
	}
	var raw struct {
		DigitsInt  int8
		DigitsFrac int8
		ResultFrac int8
		Negative   bool
		WordBuf    [9]int32
	}
	if err := json.Unmarshal(j, &raw); err != nil {
		panic(err)
	}
	st := [13]int32{int32(raw.DigitsInt), int32(raw.DigitsFrac), int32(raw.ResultFrac)}
	if raw.Negative {
		st[3] = 1
	}
	copy(st[4:], raw.WordBuf[:])
	return st
}

// Props is the output of op "props".
type Props struct {
	DigitsInt  int8 `json:"digits_int"`
	DigitsFrac int8 `json:"digits_frac"`
	Negative   bool `json:"negative"`
	Zero       bool `json:"zero"`
	Precision  int  `json:"precision"` // PrecisionAndFrac
	Frac       int  `json:"frac"`
}

// FromBinOut is the output of op "from_bin".
type FromBinOut struct {
	N   int `json:"n"`
	Dec Dec `json:"dec"`
}

func decOut(d *types.MyDecimal) Dec {
	return Dec{S: string(d.ToString()), Str: d.String(), St: decState(d)}
}

// FormatFloat is the float encoding used in args and outputs.
func FormatFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func parseFloat(s string) float64 {
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
		panic(fmt.Sprintf("bad float arg %q", s))
	}
	return f
}

var roundModes = map[string]types.RoundMode{
	"HalfUp":   types.ModeHalfUp,
	"Truncate": types.ModeTruncate,
	"Ceiling":  types.ModeCeiling,
}

type argReader []json.RawMessage

func (a argReader) get(i int, v any) {
	if i >= len(a) {
		panic(fmt.Sprintf("missing arg %d", i))
	}
	if err := json.Unmarshal(a[i], v); err != nil {
		panic(fmt.Sprintf("arg %d: %v", i, err))
	}
}
func (a argReader) str(i int) (s string) { a.get(i, &s); return }
func (a argReader) int(i int) (n int)    { a.get(i, &n); return }
func (a argReader) i64(i int) (n int64)  { a.get(i, &n); return }
func (a argReader) u64(i int) (n uint64) { a.get(i, &n); return }
func (a argReader) bool(i int) (b bool)  { a.get(i, &b); return }
func (a argReader) f64(i int) float64    { return parseFloat(a.str(i)) }
func (a argReader) dec(i int) *types.MyDecimal {
	d := new(types.MyDecimal)
	_ = d.FromString([]byte(a.str(i))) // value is used even on parse error
	return d
}
func (a argReader) hex(i int) []byte {
	b, err := hex.DecodeString(a.str(i))
	if err != nil {
		panic(fmt.Sprintf("arg %d: %v", i, err))
	}
	return b
}
func (a argReader) mode(i int) types.RoundMode {
	m, ok := roundModes[a.str(i)]
	if !ok {
		panic(fmt.Sprintf("bad round mode %q", a.str(i)))
	}
	return m
}

func hexOrNil(b []byte) any {
	if b == nil {
		return nil
	}
	return hex.EncodeToString(b)
}

func decimalExec(op string, args []json.RawMessage) (any, error) {
	a := argReader(args)
	switch op {
	case "from_string":
		var d types.MyDecimal
		err := d.FromString([]byte(a.str(0)))
		return decOut(&d), err
	case "from_int":
		var d types.MyDecimal
		d.FromInt(a.i64(0))
		return decOut(&d), nil
	case "from_uint":
		var d types.MyDecimal
		d.FromUint(a.u64(0))
		return decOut(&d), nil
	case "from_float64":
		var d types.MyDecimal
		err := d.FromFloat64(a.f64(0))
		return decOut(&d), err
	case "to_int":
		v, err := a.dec(0).ToInt()
		return v, err
	case "to_uint":
		v, err := a.dec(0).ToUint()
		return v, err
	case "to_float64":
		v, err := a.dec(0).ToFloat64()
		return FormatFloat(v), err
	case "round":
		d := a.dec(0)
		var to types.MyDecimal
		err := d.Round(&to, a.int(1), a.mode(2))
		return decOut(&to), err
	case "round_inplace":
		d := a.dec(0)
		err := d.Round(d, a.int(1), a.mode(2))
		return decOut(d), err
	case "shift":
		d := a.dec(0)
		err := d.Shift(a.int(1))
		return decOut(d), err
	case "add", "sub", "mul", "mod":
		x, y := a.dec(0), a.dec(1)
		var to types.MyDecimal
		var err error
		switch op {
		case "add":
			err = types.DecimalAdd(x, y, &to)
		case "sub":
			err = types.DecimalSub(x, y, &to)
		case "mul":
			err = types.DecimalMul(x, y, &to)
		case "mod":
			err = types.DecimalMod(x, y, &to)
		}
		return decOut(&to), err
	case "div":
		x, y := a.dec(0), a.dec(1)
		var to types.MyDecimal
		err := types.DecimalDiv(x, y, &to, a.int(2))
		return decOut(&to), err
	case "neg":
		return decOut(types.DecimalNeg(a.dec(0))), nil
	case "compare":
		return a.dec(0).Compare(a.dec(1)), nil
	case "to_bin":
		b, err := a.dec(0).ToBin(a.int(1), a.int(2))
		return hexOrNil(b), err
	case "from_bin":
		var d types.MyDecimal
		n, err := d.FromBin(a.hex(0), a.int(1), a.int(2))
		return FromBinOut{N: n, Dec: decOut(&d)}, err
	case "bin_size":
		return types.DecimalBinSize(a.int(0), a.int(1))
	case "peak":
		return types.DecimalPeak(a.hex(0))
	case "to_hash_key":
		b, err := a.dec(0).ToHashKey()
		return hexOrNil(b), err
	case "props":
		d := a.dec(0)
		p, f := d.PrecisionAndFrac()
		return Props{
			DigitsInt: d.GetDigitsInt(), DigitsFrac: d.GetDigitsFrac(),
			Negative: d.IsNegative(), Zero: d.IsZero(), Precision: p, Frac: f,
		}, nil
	case "max_or_min":
		return decOut(types.NewMaxOrMinDec(a.bool(0), a.int(1), a.int(2))), nil
	case "marshal_json":
		j, err := a.dec(0).MarshalJSON()
		return string(j), err
	case "unmarshal_json":
		var d types.MyDecimal
		err := d.UnmarshalJSON([]byte(a.str(0)))
		return decOut(&d), err
	}
	panic(fmt.Sprintf("unknown op %q", op))
}
