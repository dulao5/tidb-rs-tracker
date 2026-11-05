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
	"strings"

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
		Blocks:   []string{"D01.2"},
		Exempt:   decimalExempt,
		Generate: decimalGenerate,
		Exec:     decimalExec,
	})
}

// decimalBaseCoverage: op -> minimum record count and the Go functions it
// exercises (tracker symbol format, see vec.Coverage). Unexported helpers are
// listed under the ops that reach them. Every Dec output also goes through
// ToString and String (which calls Round), listed under from_string.
var decimalBaseCoverage = map[string]vec.Coverage{
	// args: [decimal string]                 out: Dec
	"from_string": {Min: 500, GoFuncs: []string{"MyDecimal.FromString", "MyDecimal.ToString", "MyDecimal.String",
		"MyDecimal.stringSize", "MyDecimal.removeLeadingZeros", "countLeadingZeroes", "digitsToWords",
		"fixWordCntError", "maxDecimal"}},
	// args: [int64]                          out: Dec
	"from_int": {Min: 30, GoFuncs: []string{"MyDecimal.FromInt", "MyDecimal.FromUint", "NewDecFromInt"}},
	// args: [uint64]                         out: Dec
	"from_uint": {Min: 30, GoFuncs: []string{"MyDecimal.FromUint", "NewDecFromUint"}},
	// args: [float64]                        out: Dec
	"from_float64": {Min: 100, GoFuncs: []string{"MyDecimal.FromFloat64"}},
	// args: [decimal]                        out: int64 (valid also when err != null)
	"to_int": {Min: 200, GoFuncs: []string{"MyDecimal.ToInt"}},
	// args: [decimal]                        out: uint64
	"to_uint": {Min: 200, GoFuncs: []string{"MyDecimal.ToUint"}},
	// args: [decimal]                        out: float64 string (FormatFloat 'g' -1)
	"to_float64": {Min: 200, GoFuncs: []string{"MyDecimal.ToFloat64"}},
	// args: [decimal, frac int, mode]        out: Dec    (fresh `to`)
	"round": {Min: 1000, GoFuncs: []string{"MyDecimal.Round", "add"}},
	// args: [decimal, frac int, mode]        out: Dec    (d.Round(d, ...), `to` aliases `d`)
	"round_inplace": {Min: 200, GoFuncs: []string{"MyDecimal.Round", "add"}},
	// args: [decimal, shift int]             out: Dec (d after Shift)
	"shift": {Min: 300, GoFuncs: []string{"MyDecimal.Shift", "MyDecimal.digitBounds", "MyDecimal.doMiniLeftShift",
		"MyDecimal.doMiniRightShift", "countTrailingZeroes"}},
	// args: [a, b]                           out: Dec
	"add": {Min: 500, GoFuncs: []string{"DecimalAdd", "validateArgs", "doAdd", "doSub", "add", "sub", "zeroMyDecimalWithFrac"}},
	"sub": {Min: 500, GoFuncs: []string{"DecimalSub", "validateArgs", "doAdd", "doSub", "add", "sub", "zeroMyDecimalWithFrac"}},
	"mul": {Min: 500, GoFuncs: []string{"DecimalMul", "validateArgs", "add", "add2", "zeroMyDecimalWithFrac"}},
	// args: [a, b, fracIncr int]             out: Dec
	"div": {Min: 500, GoFuncs: []string{"DecimalDiv", "validateArgs", "doDivMod", "add", "sub2", "zeroMyDecimalWithFrac"}},
	// args: [a, b]                           out: Dec
	"mod": {Min: 500, GoFuncs: []string{"DecimalMod", "validateArgs", "doDivMod", "add", "sub2", "zeroMyDecimalWithFrac"}},
	// Aliasing (Go allows `to` to be one of the inputs; validateArgs copies
	// the aliased input first). The last arg `alias` selects the call:
	//   1: f(x, y, x)   2: f(x, y, y)   3: f(x, x, x) (b must equal a; only a is parsed)
	// args: [a, b, alias] (div: [a, b, fracIncr, alias])   out: Dec (the `to` object)
	"add_alias": {Min: 100, GoFuncs: []string{"DecimalAdd", "validateArgs"}},
	"sub_alias": {Min: 100, GoFuncs: []string{"DecimalSub", "validateArgs"}},
	"mul_alias": {Min: 100, GoFuncs: []string{"DecimalMul", "validateArgs"}},
	"div_alias": {Min: 100, GoFuncs: []string{"DecimalDiv", "validateArgs"}},
	"mod_alias": {Min: 100, GoFuncs: []string{"DecimalMod", "validateArgs"}},
	// args: [a]                              out: Dec
	"neg": {Min: 50, GoFuncs: []string{"DecimalNeg"}},
	// args: [a, b]                           out: -1|0|1
	"compare": {Min: 500, GoFuncs: []string{"MyDecimal.Compare", "doSub"}},
	// args: [decimal, precision, frac]       out: hex|null
	"to_bin": {Min: 500, GoFuncs: []string{"MyDecimal.ToBin", "MyDecimal.WriteBin", "writeWord", "MyDecimal.removeLeadingZeros"}},
	// args: [hex, precision, frac]           out: {"n": binSize, "dec": Dec}
	"from_bin": {Min: 300, GoFuncs: []string{"MyDecimal.FromBin", "readWord", "fixWordCntError"}},
	// args: [precision, frac]                out: int
	"bin_size": {Min: 100, GoFuncs: []string{"DecimalBinSize"}},
	// args: [hex]                            out: int
	"peak": {Min: 30, GoFuncs: []string{"DecimalPeak"}},
	// args: [decimal]                        out: hex
	"to_hash_key": {Min: 300, GoFuncs: []string{"MyDecimal.ToHashKey", "MyDecimal.removeTrailingZeros"}},
	// args: [decimal]                        out: Props
	"props": {Min: 300, GoFuncs: []string{"MyDecimal.GetDigitsInt", "MyDecimal.GetDigitsFrac", "MyDecimal.IsNegative",
		"MyDecimal.IsZero", "MyDecimal.PrecisionAndFrac"}},
	// args: [negative bool, prec, frac]      out: Dec
	"max_or_min": {Min: 50, GoFuncs: []string{"NewMaxOrMinDec"}},
	// args: [decimal]                        out: JSON text (string)
	"marshal_json": {Min: 50, GoFuncs: []string{"MyDecimal.MarshalJSON"}},
	// args: [JSON text string]               out: Dec
	"unmarshal_json": {Min: 50, GoFuncs: []string{"MyDecimal.UnmarshalJSON"}},
}

// Chained ops "<producer>_then_<consumer>": the producer's Dec result (not
// re-parsed from a string) is the consumer's first decimal argument. This
// covers objects whose internal state differs from what FromString yields,
// e.g. digitsInt rounded up to a whole word after FromInt/FromUint/Round and
// arithmetic. args = producer args followed by the consumer's args without its
// first decimal; out = the consumer's out; err = the producer's error if
// non-nil, else the consumer's (both steps always run, as Go callers that
// ignore ErrTruncated do).
var (
	chainProducers = map[string]int{ // op -> number of args
		"from_int": 1, "from_uint": 1, "round": 3, "add": 2, "mul": 2,
	}
	chainConsumers = []string{
		"to_string", "to_bin", "to_hash_key", "props", "round", "round_inplace", "shift",
		"to_int", "to_float64", "compare", "add", "mul", "div",
	}
	chainProducerNames = []string{"from_int", "from_uint", "round", "add", "mul"}
)

const chainSep = "_then_"

// decimalCoverage: base ops + chained ops (GoFuncs = union of both steps).
var decimalCoverage = func() map[string]vec.Coverage {
	m := map[string]vec.Coverage{
		// args: [decimal]   out: {"s": ToString(), "str": String()}; only used as a chain consumer
		"to_string": {Min: 0, GoFuncs: []string{"MyDecimal.ToString", "MyDecimal.String"}},
	}
	for op, c := range decimalBaseCoverage {
		m[op] = c
	}
	for _, p := range chainProducerNames {
		for _, c := range chainConsumers {
			fs := append(append([]string{}, m[p].GoFuncs...), m[c].GoFuncs...)
			m[p+chainSep+c] = vec.Coverage{Min: 20, GoFuncs: fs}
		}
	}
	return m
}()

// decimalExempt lists the D01.2 functions no op covers, with the reason.
var decimalExempt = map[string]string{
	"NewDecFromFloatForTest":  "test-only constructor: FromFloat64 plus log.Panic on error; the value path is from_float64",
	"NewDecFromStringForTest": "test-only constructor: FromString plus log.Panic on error; the value path is from_string",
}

// Dec is the JSON form of a MyDecimal result.
type Dec struct {
	S   string    `json:"s"`   // ToString()
	Str string    `json:"str"` // String(): rounded to resultFrac with HalfUp
	St  [13]int32 `json:"st"`  // internal state, see decState
}

// Str is the output of op "to_string".
type Str struct {
	S   string `json:"s"`
	Str string `json:"str"`
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

// argReader decodes args. In the consumer step of a chained op, first is the
// producer's result and replaces decimal argument 0.
type argReader struct {
	raw   []json.RawMessage
	first *types.MyDecimal
}

func (a argReader) get(i int, v any) {
	if i >= len(a.raw) {
		panic(fmt.Sprintf("missing arg %d", i))
	}
	if err := json.Unmarshal(a.raw[i], v); err != nil {
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
	if i == 0 && a.first != nil {
		d := *a.first
		return &d
	}
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
	if p, c, ok := strings.Cut(op, chainSep); ok {
		return decimalChain(p, c, args)
	}
	return decimalOp(op, argReader{raw: args})
}

// decimalChain runs producer p, then consumer c on p's result (see chainProducers).
func decimalChain(p, c string, args []json.RawMessage) (any, error) {
	n, ok := chainProducers[p]
	if !ok || len(args) < n {
		panic(fmt.Sprintf("bad chained op %s%s%s", p, chainSep, c))
	}
	// The intermediate object is not encoded: ToString/String on it could
	// panic (e.g. after a truncated mul) where the consumer does not.
	first, perr := decimalProduce(p, argReader{raw: args[:n]})
	rest := append([]json.RawMessage{json.RawMessage("null")}, args[n:]...)
	out, err := decimalOp(c, argReader{raw: rest, first: &first})
	if perr != nil {
		err = perr
	}
	return out, err
}

// decimalProduce runs a chain producer op and returns the result object.
func decimalProduce(op string, a argReader) (types.MyDecimal, error) {
	var to types.MyDecimal
	var err error
	switch op {
	case "from_int": // NewDecFromInt(v) is new(MyDecimal).FromInt(v)
		to = *types.NewDecFromInt(a.i64(0))
	case "from_uint":
		to = *types.NewDecFromUint(a.u64(0))
	case "round":
		err = a.dec(0).Round(&to, a.int(1), a.mode(2))
	case "add":
		err = types.DecimalAdd(a.dec(0), a.dec(1), &to)
	case "mul":
		err = types.DecimalMul(a.dec(0), a.dec(1), &to)
	default:
		panic(fmt.Sprintf("not a chain producer: %q", op))
	}
	return to, err
}

func decimalOp(op string, a argReader) (any, error) {
	switch op {
	case "from_int", "from_uint", "round", "add", "mul":
		d, err := decimalProduce(op, a)
		return decOut(&d), err
	case "from_string":
		var d types.MyDecimal
		err := d.FromString([]byte(a.str(0)))
		return decOut(&d), err
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
	case "round_inplace":
		d := a.dec(0)
		err := d.Round(d, a.int(1), a.mode(2))
		return decOut(d), err
	case "shift":
		d := a.dec(0)
		err := d.Shift(a.int(1))
		return decOut(d), err
	case "sub", "mod":
		x, y := a.dec(0), a.dec(1)
		var to types.MyDecimal
		var err error
		if op == "sub" {
			err = types.DecimalSub(x, y, &to)
		} else {
			err = types.DecimalMod(x, y, &to)
		}
		return decOut(&to), err
	case "div":
		x, y := a.dec(0), a.dec(1)
		var to types.MyDecimal
		err := types.DecimalDiv(x, y, &to, a.int(2))
		return decOut(&to), err
	case "add_alias", "sub_alias", "mul_alias", "mod_alias", "div_alias":
		f := map[string]func(x, y, to *types.MyDecimal) error{
			"add_alias": types.DecimalAdd, "sub_alias": types.DecimalSub, "mul_alias": types.DecimalMul,
			"mod_alias": types.DecimalMod,
		}[op]
		ai := 2
		if op == "div_alias" {
			fracIncr := a.int(2)
			f = func(x, y, to *types.MyDecimal) error { return types.DecimalDiv(x, y, to, fracIncr) }
			ai = 3
		}
		x, y := a.dec(0), a.dec(1)
		switch alias := a.int(ai); alias {
		case 1:
			err := f(x, y, x)
			return decOut(x), err
		case 2:
			err := f(x, y, y)
			return decOut(y), err
		case 3:
			err := f(x, x, x)
			return decOut(x), err
		default:
			panic(fmt.Sprintf("bad alias %d", alias))
		}
	case "to_string":
		d := a.dec(0)
		return Str{S: string(d.ToString()), Str: d.String()}, nil
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
