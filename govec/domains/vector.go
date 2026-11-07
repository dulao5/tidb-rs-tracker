package domains

// Domain "vector": pkg/types/vector.go and vector_functions.go (block D01.6),
// types.VectorFloat32, with the parts of jsoniter that ParseVectorFloat32 uses.
//
// Values:
//   - a vector is its serialized bytes (ZeroCopySerialize) as hex; inputs are
//     deserialized with ZeroCopyDeserializeVectorFloat32, so they may hold NaN
//     and ±Inf elements (CreateVectorFloat32 / ParseVectorFloat32 reject them,
//     the arithmetic does not check its inputs);
//   - an output vector is {"data": hex, "s": String(), "ts": TruncatedString(),
//     "len": Len(), "zero": IsZeroValue(), "size": SerializedSize(),
//     "mem": EstimatedMemUsage()};
//   - float32 inputs (CreateVectorFloat32) and float64 outputs (distances) are
//     their IEEE bits as JSON numbers (NaN and ±Inf are not JSON numbers);
//   - strings are vec.GoString (hex for invalid UTF-8).
//
// The float sums of the distance functions are fused into FMA on arm64 and not
// on amd64 (GOAMD64=v1) (tracker data/go_fma_sites.tsv), so the distance
// records differ between the committed target files (M-FMA; the allow-table in
// targetdiffs_test.go).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/types"
)

const vectorSeed = 31337

func init() {
	vec.Register(&vec.Domain{
		Name:     "vector",
		Seed:     vectorSeed,
		Coverage: vectorCoverage,
		Blocks:   []string{"D01.6"},
		Exempt: map[string]string{
			"init": "the little-endian check runs at init in Go; in Rust it is a compile-time assertion",
		},
		Gzip:     true,
		Generate: vectorGenerate,
		Exec:     vectorExec,
	})
}

// vecOutFuncs: what vectorOut calls.
var vecOutFuncs = []string{"VectorFloat32.ZeroCopySerialize", "VectorFloat32.String",
	"VectorFloat32.TruncatedString", "VectorFloat32.Len", "VectorFloat32.Elements",
	"VectorFloat32.IsZeroValue", "VectorFloat32.SerializedSize", "VectorFloat32.EstimatedMemUsage"}

func withOut(fs ...string) []string { return append(append([]string{}, fs...), vecOutFuncs...) }

var vectorCoverage = map[string]vec.Coverage{
	"parse":        {Min: 3000, GoFuncs: withOut("ParseVectorFloat32", "CheckVectorDimValid", "InitVectorFloat32")},
	"create":       {Min: 300, GoFuncs: withOut("CreateVectorFloat32")},
	"must_create":  {Min: 30, GoFuncs: withOut("MustCreateVectorFloat32")},
	"check_dim":    {Min: 50, GoFuncs: []string{"CheckVectorDimValid"}},
	"check_fit":    {Min: 100, GoFuncs: []string{"VectorFloat32.CheckDimsFitColumn"}},
	"deserialize":  {Min: 300, GoFuncs: withOut("ZeroCopyDeserializeVectorFloat32", "PeekBytesAsVectorFloat32")},
	"serialize_to": {Min: 100, GoFuncs: []string{"VectorFloat32.SerializeTo"}},
	"clone":        {Min: 100, GoFuncs: withOut("VectorFloat32.Clone")},
	"distance": {Min: 3000, GoFuncs: []string{"VectorFloat32.checkIdenticalDims", "VectorFloat32.L2SquaredDistance",
		"VectorFloat32.L2Distance", "VectorFloat32.InnerProduct", "VectorFloat32.NegativeInnerProduct",
		"VectorFloat32.CosineDistance", "VectorFloat32.L1Distance"}},
	"l2_norm":  {Min: 300, GoFuncs: []string{"VectorFloat32.L2Norm"}},
	"arith":    {Min: 1500, GoFuncs: withOut("VectorFloat32.Add", "VectorFloat32.Sub", "VectorFloat32.Mul")},
	"compare":  {Min: 500, GoFuncs: []string{"VectorFloat32.Compare"}},
	"zero_vec": {Min: 1, GoFuncs: withOut()},
}

func vectorOut(v types.VectorFloat32) map[string]any {
	return map[string]any{
		"data": hex.EncodeToString(v.ZeroCopySerialize()),
		"s":    vec.GoString(v.String()),
		"ts":   vec.GoString(v.TruncatedString()),
		"len":  v.Len(),
		"zero": v.IsZeroValue(),
		"size": v.SerializedSize(),
		"mem":  v.EstimatedMemUsage(),
	}
}

// vecHex: the serialized bytes of a vector of these float32 bit patterns.
func vecHex(bits []uint32) string {
	v := types.InitVectorFloat32(len(bits))
	e := v.Elements()
	for i, b := range bits {
		e[i] = math.Float32frombits(b)
	}
	return hex.EncodeToString(v.ZeroCopySerialize())
}

func vecOfHex(s string) types.VectorFloat32 {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	v, rest, err := types.ZeroCopyDeserializeVectorFloat32(b)
	if err != nil || len(rest) != 0 {
		panic(fmt.Sprintf("bad vector input %s: %v", s, err))
	}
	return v
}

func vectorExec(op string, args []json.RawMessage) (out any, err error) {
	vecArg := func(i int) types.VectorFloat32 { return vecOfHex(arg[string](args, i)) }
	switch op {
	case "zero_vec":
		return vectorOut(types.ZeroVectorFloat32), nil
	case "parse":
		v, err := types.ParseVectorFloat32(string(arg[vec.GoString](args, 0)))
		return vectorOut(v), err
	case "create", "must_create":
		bits := arg[[]uint32](args, 0)
		fs := make([]float32, len(bits))
		for i, b := range bits {
			fs[i] = math.Float32frombits(b)
		}
		if op == "must_create" {
			return vectorOut(types.MustCreateVectorFloat32(fs)), nil
		}
		v, err := types.CreateVectorFloat32(fs)
		return vectorOut(v), err
	case "check_dim":
		return nil, types.CheckVectorDimValid(arg[int](args, 0))
	case "check_fit":
		return nil, vecArg(0).CheckDimsFitColumn(arg[int](args, 1))
	case "deserialize":
		b, err := hex.DecodeString(arg[string](args, 0))
		if err != nil {
			panic(err)
		}
		n, perr := types.PeekBytesAsVectorFloat32(b)
		v, rest, err := types.ZeroCopyDeserializeVectorFloat32(b)
		if (perr == nil) != (err == nil) {
			panic("Peek and ZeroCopyDeserialize disagree")
		}
		return map[string]any{"n": n, "vec": vectorOut(v), "rest": hex.EncodeToString(rest)}, err
	case "serialize_to":
		b, err := hex.DecodeString(arg[string](args, 0))
		if err != nil {
			panic(err)
		}
		return hex.EncodeToString(vecArg(1).SerializeTo(b)), nil
	case "clone":
		a := vecArg(0)
		c := a.Clone()
		if len(c.Elements()) > 0 {
			// The clone does not share memory with the original: "orig" is unchanged.
			c.Elements()[0] = 42
		}
		return map[string]any{"orig": vectorOut(a), "clone": vectorOut(c)}, nil
	case "distance":
		a, b := vecArg(1), vecArg(2)
		var d float64
		switch arg[string](args, 0) {
		case "l2_squared":
			d, err = a.L2SquaredDistance(b)
		case "l2":
			d, err = a.L2Distance(b)
		case "inner_product":
			d, err = a.InnerProduct(b)
		case "negative_inner_product":
			d, err = a.NegativeInnerProduct(b)
		case "cosine":
			d, err = a.CosineDistance(b)
		case "l1":
			d, err = a.L1Distance(b)
		default:
			panic("unknown distance " + arg[string](args, 0))
		}
		return math.Float64bits(d), err
	case "l2_norm":
		return math.Float64bits(vecArg(0).L2Norm()), nil
	case "arith":
		a, b := vecArg(1), vecArg(2)
		var r types.VectorFloat32
		switch arg[string](args, 0) {
		case "add":
			r, err = a.Add(b)
		case "sub":
			r, err = a.Sub(b)
		case "mul":
			r, err = a.Mul(b)
		default:
			panic("unknown arith " + arg[string](args, 0))
		}
		return vectorOut(r), err
	case "compare":
		return vecArg(0).Compare(vecArg(1)), nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// vectorLiterals: vector texts of the Go tests (pkg/types/vector_test.go,
// pkg/expression/integration_test/integration_test.go,
// pkg/server/tests/commontest/tidb_test.go, pkg/expression vector tests).
var vectorLiterals = []string{
	`abc`, `null`, `"json_str"`, `123`, `[123`, `123]`, `[123,]`, `[]`, `[1.1, 2.2, 3.3]`, `[-1.1, 4.2]`, `[1.1, 4.2]`,
	`[-1,-1]`, `[-1.1,-1.1]`, `[-11111111111,-11111111111.23456789,-3.1,-5.12456]`, `[-3e38]`,
	`["here", ["I", "am"], "!!!"]`, `[0,0]`, `[0,1]`, `[0,2]`, `[1, 2, 3]`, `[1, 2]`, `[1,0]`, `[1,1,1]`, `[1,1]`,
	`[1,2,3,4,5,6,7,8,9,10,11]`, `[1,2,3,4,5]`, `[1,2,3,4]`, `[1,2,3]`, `[1,2.1,3.3]`, `[1,2.1,inf]`, `[1,2.1,nan]`,
	`[1,2.1,null]`, `[1,2]`, `[1,5,7]`, `[1.1,1.1]`, `[1.4, 4.5, 8.5, 7.7, 6.2]`, `[1]`,
	`[11111111111,11111111111.23456789,3.1,5.12456]`, `[1e37]`, `[2,3,4]`, `[2,3]`, `[2,4]`, `[3,100,12345,10000]`,
	`[3,4]`, `[3.6, 9.7, 2.4, 6.6, 4.9]`, `[3]`, `[3e38]`, `[4, 5, 6]`, `[4, 5, 601]`, `[4, 5, 61]`, `[4,5,6]`,
	`[4.7, 4.9, 2.6, 5.2, 7.4]`, `[4.7,4.9,2.6,5.2,7.4]`, `[4]`, `[5, 3, 2]`, `[7, 8, 9]`, `[7.7, 6.7, 8.3, 7.8, 5.7]`,
	`[8.7, 5.7, 7.7, 9.8, 1.5]`, `[0,0,0]`, `[1,2.0,3.0]`, `[14,16,18]`, `[2,2,2]`, `[2,4,6]`, `[3,3,3]`, `[3,5]`,
	`[4,10,18]`, `[5,7,9]`, `[6,6,6]`, `[8,10,12]`,
}

// vectorEdgeTexts: hand-written texts for the jsoniter paths (fast path,
// slow path, validateFloat, ReadArrayCB, null, trailing data, whitespace).
var vectorEdgeTexts = []string{
	``, ` `, `[`, `]`, `[]`, `[ ]`, ` [ ] `, "\t[\n1\r,\t2\n]\r", `[,]`, `[1,,2]`, `[1 2]`, `[1;2]`, `[[1]]`, `[{}]`,
	`[true]`, `[false]`, `[null]`, `["1"]`, `null`, `nul`, `nulx`, `nullx`, `null]`, `n`, `nil`, `NULL`, `[1]x`,
	`[1]]`, `[1],[2]`, `[1] [2]`, `{}`, `{"a":1}`, `true`,
	`[0]`, `[-0]`, `[00]`, `[01]`, `[-01]`, `[0.0]`, `[0.]`, `[0.e1]`, `[.5]`, `[-.5]`, `[+.5]`, `[+5]`, `[+-5]`,
	`[-+5]`, `[--5]`, `[-]`, `[+]`, `[-,1]`, `[1.]`, `[1.e5]`, `[1.5e]`, `[1.5e+]`, `[1.5e-3]`, `[1.5E3]`, `[1e5]`,
	`[1E-5]`, `[1e+5]`, `[1.2.3]`, `[1-2]`, `[1+2]`, `[1e1e1]`, `[0x10]`, `[1_000]`, `[Infinity]`, `[-Infinity]`,
	`[NaN]`, `[inf]`, `[-inf]`, `[1.0000001]`, `[1.000000]`, `[1.0000000]`, `[0.123456]`, `[0.1234567]`,
	`[123456789012345678]`, `[1234567890123456789]`, `[12345678901234567890]`, `[1844674407370955161]`,
	`[1844674407370955162]`, `[9007199254740991.1]`, `[900719925474099.12]`, `[90071992547409.912]`,
	`[9007199254.740993]`, `[0.9007199254740993]`, `[1e38]`, `[3.4028234e38]`, `[3.4028236e38]`, `[3.5e38]`, `[1e39]`,
	`[-1e39]`, `[1e308]`, `[1.7976931348623157e308]`, `[1.8e308]`, `[1e309]`, `[1e400]`, `[-1e400]`, `[1e-38]`,
	`[1e-45]`, `[1.4e-45]`, `[7e-46]`, `[1e-46]`, `[1e-50]`, `[1e-400]`, `[4.9e-324]`, `[2.5e-324]`, `[1e-500]`,
	`[0.1,0.2,0.3]`, `[1,2,3,4,5,6]`, `[1.5,2.25,3.125,4.0625,5.03125,6.015625]`, "[1,\x80]", "[\xff]", "\xff",
	"[1,2]\xff", "[1,\"\xc3\x28\"]", `[1,2,3`, `[1,2,`, `[1,2`, `[1,`, `[1`, `[-`, `[+`, `[1.`, `[1e`, `[1.5`, `[-1.5`,
	`[0`, `[0.`, `[ 1 , 2 ]`, `[1 ,2]`, `[1, 2 ]`, `[  -1  ]`, `[- 1]`, `[1 .5]`, `[1. 5]`, `[1e 5]`,
	`[1.1234567890123456789012345678901234567890]`, `[0.000000000000000000000000000000000000000000001]`,
}

// vectorBits: interesting float32 bit patterns.
var vectorBits = []uint32{
	0x00000000, 0x80000000, 0x3f800000, 0xbf800000, 0x3f8ccccd, 0x400ccccd, 0x7f7fffff, 0xff7fffff,
	0x00800000, 0x80800000, 0x00000001, 0x80000001, 0x007fffff, 0x7f800000, 0xff800000, 0x7fc00000,
	0xffc00000, 0x7f800001, 0x3eaaaaab, 0x3dcccccd, 0x4b800000, 0x4b800001, 0x5f000000, 0x7e967699,
	0x1e3ce508, 0x3f7fffff, 0x3f800001, 0x40490fdb,
	0xff800001, 0x7fc12345, 0xffd00001, 0x7fbfffff, 0xffbfffff, 0x7f800abc, 0xff812345, 0x7fffffff, 0xffffffff,
	0x7fc00000, 0xffc00000, 0x7f800001, 0x7f800000, 0xff800000, 0x7f7fffff, 0x5f800000, 0xdf800000,
}

// randF32: a float32 from one of several shapes; finite unless special.
func randF32(rng *rand.Rand, special bool) float32 {
	switch k := rng.IntN(10); {
	case k <= 2 && special:
		return math.Float32frombits(vectorBits[rng.IntN(len(vectorBits))])
	case k == 0 || k == 1:
		return float32(rng.IntN(21) - 10)
	case k <= 4:
		return float32(rng.Float64()*2 - 1)
	case k <= 6:
		return float32(math.Round((rng.Float64()*200-100)*100) / 100)
	case k == 7:
		// any finite bit pattern
		for {
			f := math.Float32frombits(rng.Uint32())
			if !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0) {
				return f
			}
		}
	case k == 8:
		return float32(rng.NormFloat64() * math.Pow(10, float64(rng.IntN(60)-30)))
	default:
		return float32(rng.NormFloat64() * 1e18)
	}
}

func randBits(rng *rand.Rand, n int, special bool) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = math.Float32bits(randF32(rng, special))
	}
	return out
}

func randDims(rng *rand.Rand) int {
	switch k := rng.IntN(20); {
	case k == 0:
		return 0
	case k <= 12:
		return 1 + rng.IntN(8)
	case k <= 17:
		return 1 + rng.IntN(64)
	default:
		return 65 + rng.IntN(236)
	}
}

// fmtF32: a float32 as JSON-ish text in one of several spellings.
func fmtF32(rng *rand.Rand, f float32) string {
	switch rng.IntN(6) {
	case 0:
		return fmt.Sprint(f)
	case 1:
		return fmt.Sprintf("%g", float64(f))
	case 2:
		return fmt.Sprintf("%.*f", rng.IntN(9), float64(f))
	case 3:
		return fmt.Sprintf("%.*e", rng.IntN(10), float64(f))
	case 4:
		return fmt.Sprintf("%v", float64(f))
	default:
		return fmt.Sprintf("%.*E", rng.IntN(4), float64(f))
	}
}

var vectorWS = []string{"", "", "", " ", "  ", "\t", "\n", "\r", " \n"}

func randVecText(rng *rand.Rand) string {
	n := randDims(rng)
	if n > 64 && rng.IntN(4) != 0 {
		n = rng.IntN(64)
	}
	var b strings.Builder
	ws := func() {
		if rng.IntN(3) == 0 {
			b.WriteString(vectorWS[rng.IntN(len(vectorWS))])
		}
	}
	ws()
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			ws()
			b.WriteByte(',')
		}
		ws()
		b.WriteString(fmtF32(rng, randF32(rng, false)))
	}
	ws()
	b.WriteByte(']')
	ws()
	return b.String()
}

// mutate: one random edit (insert, delete, replace a byte) from a JSON-ish alphabet.
func mutate(rng *rand.Rand, s string) string {
	alpha := "[],. -+eE0123456789nul\"\t\r\nx\x80"
	b := []byte(s)
	for k := 1 + rng.IntN(3); k > 0; k-- {
		c := alpha[rng.IntN(len(alpha))]
		switch {
		case len(b) == 0 || rng.IntN(3) == 0:
			i := rng.IntN(len(b) + 1)
			b = append(b[:i], append([]byte{c}, b[i:]...)...)
		case rng.IntN(2) == 0:
			i := rng.IntN(len(b))
			b = append(b[:i], b[i+1:]...)
		default:
			b[rng.IntN(len(b))] = c
		}
	}
	return string(b)
}

// deserializeDefined: PeekBytesAsVectorFloat32 computes the size in uint32
// (elements*4 + 4 wraps for elements >= 2^30). When the wrapped size is at
// least 8 and fits in b, Go accepts a vector whose Len exceeds its data and
// Elements reads past the slice (undefined); such inputs are not generated.
// A wrapped size of 0 or 4 panics deterministically (Len / Elements index the
// data), and is kept.
func deserializeDefined(b []byte) bool {
	if len(b) < 4 {
		return true
	}
	elems := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
	wrapped := uint64(uint32(elems*4 + 4))
	if wrapped > uint64(len(b)) {
		return true
	}
	return wrapped == elems*4+4 || wrapped < 8
}

func vectorGenerate(g *vec.Gen, rng *rand.Rand) {
	g.Add("sys", "zero_vec")
	for _, s := range vectorLiterals {
		g.Add("lit/go-tests", "parse", vec.GoString(s))
	}
	for _, s := range vectorEdgeTexts {
		g.Add("sys/edge", "parse", vec.GoString(s))
	}
	// dimension limit through the parser
	for _, n := range []int{16382, 16383, 16384} {
		g.Add("sys/dims", "parse", vec.GoString("["+strings.Repeat("1,", n-1)+"1]"))
	}
	for i := 0; i < 20000; i++ {
		g.Add("rand/text", "parse", vec.GoString(randVecText(rng)))
	}
	for i := 0; i < 15000; i++ {
		base := vectorEdgeTexts[rng.IntN(len(vectorEdgeTexts))]
		if rng.IntN(2) == 0 {
			base = randVecText(rng)
		}
		g.Add("rand/mutate", "parse", vec.GoString(mutate(rng, base)))
	}
	// random digit strings of the fast / slow path boundaries
	for i := 0; i < 5000; i++ {
		ip := fmt.Sprint(rng.Uint64() >> rng.IntN(64))
		s := ip
		if rng.IntN(2) == 0 {
			frac := fmt.Sprint(rng.Uint64() >> rng.IntN(64))
			s += "." + frac[:1+rng.IntN(len(frac))]
		}
		if rng.IntN(4) == 0 {
			s += fmt.Sprintf("e%d", rng.IntN(100)-50)
		}
		if rng.IntN(3) == 0 {
			s = "-" + s
		}
		g.Add("rand/number", "parse", vec.GoString("["+s+"]"))
	}

	// CreateVectorFloat32
	for _, b := range vectorBits {
		g.Add("sys/bits", "create", []uint32{b})
		g.Add("sys/bits", "create", []uint32{0x3f800000, b, 0x40000000})
	}
	g.Add("sys", "create", []uint32{})
	g.Add("sys", "create", []uint32{0x7fc00000, 0x7f800000})
	g.Add("sys", "create", []uint32{0x7f800000, 0x7fc00000})
	for i := 0; i < 300; i++ {
		g.Add("rand", "create", randBits(rng, randDims(rng), rng.IntN(3) == 0))
	}
	for i := 0; i < 30; i++ {
		g.Add("rand", "must_create", randBits(rng, randDims(rng), i%3 == 0))
	}

	for _, d := range []int{-2147483648, -100, -1, 0, 1, 2, 100, 16382, 16383, 16384, 16385, 100000, 2147483647} {
		g.Add("sys", "check_dim", d)
	}
	for i := 0; i < 50; i++ {
		g.Add("rand", "check_dim", rng.IntN(40000)-5000)
	}
	for i := 0; i < 120; i++ {
		n := randDims(rng)
		flen := []int{-1, 0, n, n + 1, n - 1, 16383, rng.IntN(100)}[rng.IntN(7)]
		g.Add("rand", "check_fit", vecHex(randBits(rng, n, false)), flen)
	}

	// Deserialization of arbitrary bytes.
	for _, h := range []string{"", "00", "000000", "00000000", "0000000001", "01000000", "010000000000803f",
		"010000000000803f00", "020000000000803f", "f1fc", "ffffffff", "ffffffff00000000", "00000040"} {
		g.Add("sys", "deserialize", h)
	}
	for i := 0; i < 300; i++ {
		n := randDims(rng)
		b, _ := hex.DecodeString(vecHex(randBits(rng, n, true)))
		switch rng.IntN(4) {
		case 0:
			b = b[:rng.IntN(len(b)+1)]
		case 1:
			extra := make([]byte, rng.IntN(12))
			for j := range extra {
				extra[j] = byte(rng.IntN(256))
			}
			b = append(b, extra...)
		case 2:
			b[rng.IntN(4)] = byte(rng.IntN(256))
		}
		if !deserializeDefined(b) {
			continue
		}
		g.Add("rand", "deserialize", hex.EncodeToString(b))
	}
	for i := 0; i < 100; i++ {
		pre := make([]byte, rng.IntN(6))
		for j := range pre {
			pre[j] = byte(rng.IntN(256))
		}
		g.Add("rand", "serialize_to", hex.EncodeToString(pre), vecHex(randBits(rng, randDims(rng), true)))
	}
	for i := 0; i < 100; i++ {
		g.Add("rand", "clone", vecHex(randBits(rng, randDims(rng), true)))
	}

	// Distances, norms, arithmetic, comparison.
	dists := []string{"l2_squared", "l2", "inner_product", "negative_inner_product", "cosine", "l1"}
	ariths := []string{"add", "sub", "mul"}
	pair := func(i int) (string, string) {
		n := randDims(rng)
		special := i%5 == 0
		a := randBits(rng, n, special)
		switch rng.IntN(8) {
		case 0:
			// different dimensions
			return vecHex(a), vecHex(randBits(rng, randDims(rng), special))
		case 1:
			return vecHex(a), vecHex(a)
		case 2:
			// zero vector of the same dimension
			return vecHex(a), vecHex(make([]uint32, n))
		}
		return vecHex(a), vecHex(randBits(rng, n, special))
	}
	sys := [][2][]uint32{
		{{}, {}}, {{0x3f800000}, {}}, {{0}, {0}}, {{0x3f800000, 0}, {0, 0x3f800000}},
		{{0x7f7fffff}, {0x7f7fffff}}, {{0x7f7fffff}, {0xff7fffff}}, {{0x7f800000}, {0x7f800000}},
		{{0x7f800000}, {0xff800000}}, {{0x7fc00000}, {0x3f800000}}, {{0x00000001}, {0x00000001}},
		{{0x3f800000, 0x40000000}, {0xbf800000, 0xc0000000}}, {{0x5f000000}, {0x5f000000}},
	}
	for _, p := range sys {
		a, b := vecHex(p[0]), vecHex(p[1])
		for _, d := range dists {
			g.Add("sys", "distance", d, a, b)
		}
		for _, o := range ariths {
			g.Add("sys", "arith", o, a, b)
		}
		g.Add("sys", "compare", a, b)
		g.Add("sys", "l2_norm", a)
	}
	for i := 0; i < 30000; i++ {
		a, b := pair(i)
		g.Add("rand", "distance", dists[i%len(dists)], a, b)
	}
	for i := 0; i < 300; i++ {
		g.Add("rand", "l2_norm", vecHex(randBits(rng, randDims(rng), i%5 == 0)))
	}
	for i := 0; i < 15000; i++ {
		a, b := pair(i)
		g.Add("rand", "arith", ariths[i%len(ariths)], a, b)
	}
	for i := 0; i < 5000; i++ {
		a, b := pair(i)
		if rng.IntN(3) == 0 {
			// a prefix of a: equal up to the common length
			ab, _ := hex.DecodeString(a)
			n := (len(ab) - 4) / 4
			m := 0
			if n > 0 {
				m = rng.IntN(n + 1)
			}
			v := types.InitVectorFloat32(m)
			copy(v.Elements(), vecOfHex(a).Elements())
			b = hex.EncodeToString(v.ZeroCopySerialize())
		}
		g.Add("rand", "compare", a, b)
	}
}
