package domains

// FMA-dependent decimal records (README "Targets").
//
// MyDecimal.ToFloat64 (mydecimal.go:1095) sums the words as
// `f += float64(x) * pow10off81[...]` (lines 1118 and 1125). gc fuses these
// into one FMA instruction on arm64 (and ppc64le, s390x, riscv64, loong64) but
// not on amd64 (tracker data/go_fma_sites.tsv), so the sum can differ in the
// last bit. `math.Round(f*unit) / unit` hides that unless f*unit lands on a
// .5 boundary, which takes resultFrac < digitsFrac: objects from DecimalDiv
// (digitsFrac is a whole word, resultFrac = frac + fracIncr), e.g.
// DecimalDiv(50.77, 32, 4) = 1.586563 (words [1, 586562500]) gives 1.586563 on
// arm64 and 1.586562 on amd64. Such records differ between the committed
// target files.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/types"
)

const (
	// fmaSample: plain random x/j records before the search keeps only the
	// records whose ToFloat64 depends on fusion.
	fmaSample = 300
	// FMADiverging: records (sample included) whose ToFloat64 depends on
	// fusion; the arm64 and amd64 files differ in at least this many records.
	FMADiverging = 40
	fmaMaxTries  = 20_000_000
)

// decimalFMA: div producers chained to every consumer, the reviewer's
// examples, and seeded random x/j (x with 1-3 decimals, j in [2,160),
// fracIncr 4, the shape of `a/b` with div_precision_increment = 4) chained to
// to_float64 until FMADiverging of them depend on fusion.
func decimalFMA(g *vec.Gen, rng *rand.Rand) {
	ar := sysArith()
	var prods [][]any
	for i := 0; i < len(ar); i += 2 {
		prods = append(prods, []any{ar[i], ar[(i+5)%len(ar)], []int{0, 4, 5, 9}[(i/2)%4]})
	}
	nsys := len(prods)
	for i := 0; i < 10; i++ {
		prods = append(prods, []any{randDecString(rng, 20, 10), randDecString(rng, 20, 10), rng.IntN(10)})
	}
	for k, args := range prods {
		src := "sys/chain"
		if k >= nsys {
			src = "rand/chain"
		}
		for _, c := range chainConsumers {
			g.Add(src, "div"+chainSep+c, append(append([]any{}, args...), chainRest(c, k, ar)...)...)
		}
	}

	op := "div" + chainSep + "to_float64"
	for _, c := range [][2]string{{"50.77", "32"}, {"100.54", "64"}, {"153.81", "96"}, {"193.08", "128"}} {
		g.Add("sys/fma", op, c[0], c[1], 4)
	}
	found := 0
	for n := 0; found < FMADiverging; n++ {
		if n == fmaMaxTries {
			panic(fmt.Sprintf("decimalFMA: only %d diverging records in %d tries", found, n))
		}
		frac := rng.IntN(3) + 1
		x := rng.IntN(int(math.Pow10(3+frac))) + 1
		s := fmt.Sprintf("%d.%0*d", x/int(math.Pow10(frac)), frac, x%int(math.Pow10(frac)))
		j := fmt.Sprint(rng.IntN(158) + 2)
		diverges := divToFloat64Diverges(s, j, 4)
		switch {
		case n < fmaSample:
			g.Add("rand/div", op, s, j, 4)
		case diverges:
			g.Add("rand/fma", op, s, j, 4)
		}
		if diverges {
			found++
		}
	}
}

func divToFloat64Diverges(a, b string, fracIncr int) bool {
	q := divResult(a, b, fracIncr)
	f1, ok := ToFloat64Model(&q, true)
	f0, _ := ToFloat64Model(&q, false)
	return ok && f1 != f0
}

func divResult(a, b string, fracIncr int) types.MyDecimal {
	var x, y, q types.MyDecimal
	_ = x.FromString([]byte(a))
	_ = y.FromString([]byte(b))
	_ = types.DecimalDiv(&x, &y, &q, fracIncr)
	return q
}

// ToFloat64Model replays MyDecimal.ToFloat64 (mydecimal.go:1095-1134) on the
// <=12-digit path with the multiply-adds of lines 1118 and 1125 fused (one
// rounding, math.FMA) or not (the explicit float64 conversion forbids fusion,
// Go spec "Arithmetic operators"). ok is false on the >12-digit path
// (strconv.ParseFloat, no multiply-add).
func ToFloat64Model(d *types.MyDecimal, fused bool) (f float64, ok bool) {
	st := decState(d)
	digitsInt, digitsFrac, resultFrac := int(st[0]), int(st[1]), int(st[2])
	if digitsInt+digitsFrac > 12 {
		return 0, false
	}
	madd := func(f float64, x int32, p float64) float64 {
		if fused {
			return math.FMA(float64(x), p, f)
		}
		return f + float64(float64(x)*p)
	}
	words := st[4:]
	wordsInt := (digitsInt-1)/9 + 1
	idx := 0
	for i := 0; i < digitsInt; i += 9 {
		idx++
		f = madd(f, words[idx-1], math.Pow10((wordsInt-idx)*9))
	}
	fracStart := idx
	for i := 0; i < digitsFrac; i += 9 {
		idx++
		f = madd(f, words[idx-1], math.Pow10(-9*(idx-fracStart)))
	}
	unit := math.Pow10(resultFrac)
	f = math.Round(f*unit) / unit
	if st[3] == 1 {
		f = -f
	}
	return f, true
}

// DivToFloat64Model is ToFloat64Model of the producer result of a
// div_then_to_float64 record, formatted as the record's out.
func DivToFloat64Model(args []json.RawMessage, fused bool) (string, bool) {
	a := argReader{raw: args}
	q := divResult(a.str(0), a.str(1), a.int(2))
	f, ok := ToFloat64Model(&q, fused)
	return FormatFloat(f), ok
}
