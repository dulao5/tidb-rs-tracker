package domains

// Domain "plancache", op "types_compat": the parameter type check of the instance plan
// cache's lookup, pkg/planner/core/plan_cache_utils.go checkTypesCompatibility4PC (the
// plan-cache exception A3, first stage: two plans of one key are told apart by their
// parameter types). Args [expected, actual], each a list of field types
// {tp, flag, flen, dec, cs, coll}; out: the bool. The types are those a binary-protocol
// parameter gets (InferParamTypeFromUnderlyingValue) plus DECIMAL precisions, the unsigned
// flag, VARCHAR / VAR_STRING and charsets / collations.

import (
	"encoding/json"
	"math/rand/v2"
	_ "unsafe" // go:linkname

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
)

//go:linkname pcCheckTypesCompatibility github.com/pingcap/tidb/pkg/planner/core.checkTypesCompatibility4PC
func pcCheckTypesCompatibility(expected, actual any) bool

type pcFT struct {
	Tp   byte   `json:"tp"`
	Flag uint   `json:"flag"`
	Flen int    `json:"flen"`
	Dec  int    `json:"dec"`
	Cs   string `json:"cs"`
	Coll string `json:"coll"`
}

func (f pcFT) ft() *types.FieldType {
	t := types.NewFieldType(f.Tp)
	t.SetFlag(f.Flag)
	t.SetFlen(f.Flen)
	t.SetDecimal(f.Dec)
	t.SetCharset(f.Cs)
	t.SetCollate(f.Coll)
	return t
}

func pcFTs(raw json.RawMessage) []*types.FieldType {
	var fs []pcFT
	must(json.Unmarshal(raw, &fs))
	out := make([]*types.FieldType, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.ft())
	}
	return out
}

func plancacheTypesExec(args []json.RawMessage) any {
	return pcCheckTypesCompatibility(pcFTs(args[0]), pcFTs(args[1]))
}

// pcParamTypes: the field types of binary-protocol parameters and their variations.
func pcParamTypes() []pcFT {
	infer := func(v any) pcFT {
		t := types.NewFieldType(mysql.TypeUnspecified)
		types.InferParamTypeFromUnderlyingValue(v, t)
		return pcFT{t.GetType(), t.GetFlag(), t.GetFlen(), t.GetDecimal(), t.GetCharset(), t.GetCollate()}
	}
	d := types.NewDecFromStringForTest("12.34")
	out := []pcFT{
		infer(int64(1)), infer(uint64(1)), infer(float64(1)), infer(float32(1)), infer("a"),
		infer([]byte("a")), infer(nil), infer(d), infer(types.Duration{}),
		infer(types.NewTime(types.ZeroCoreTime, mysql.TypeDatetime, 0)),
	}
	out = append(out,
		pcFT{mysql.TypeLonglong, mysql.UnsignedFlag, 20, 0, "binary", "binary"},
		pcFT{mysql.TypeLong, 0, 11, 0, "binary", "binary"},
		pcFT{mysql.TypeNewDecimal, 0, 10, 2, "binary", "binary"},
		pcFT{mysql.TypeNewDecimal, 0, 8, 2, "binary", "binary"},
		pcFT{mysql.TypeNewDecimal, 0, 12, 2, "binary", "binary"},
		pcFT{mysql.TypeNewDecimal, 0, 10, 4, "binary", "binary"},
		pcFT{mysql.TypeNewDecimal, 0, 10, 0, "binary", "binary"},
		pcFT{mysql.TypeVarchar, 0, -1, -1, "utf8mb4", "utf8mb4_bin"},
		pcFT{mysql.TypeVarString, 0, -1, -1, "utf8mb4", "utf8mb4_bin"},
		pcFT{mysql.TypeVarString, 0, -1, -1, "utf8mb4", "utf8mb4_general_ci"},
		pcFT{mysql.TypeVarString, 0, -1, -1, "utf8", "utf8_bin"},
		pcFT{mysql.TypeVarString, mysql.BinaryFlag, -1, -1, "binary", "binary"},
		pcFT{mysql.TypeString, 0, -1, -1, "utf8mb4", "utf8mb4_bin"},
		pcFT{mysql.TypeDouble, mysql.UnsignedFlag, -1, -1, "binary", "binary"},
	)
	return out
}

func plancacheTypesGenerate(g *vec.Gen, r *rand.Rand) {
	add := func(src string, a, b []pcFT) { g.Add(src, "types_compat", a, b) }
	ts := pcParamTypes()
	// every pair of single parameters (the cached plan's types first)
	for _, a := range ts {
		for _, b := range ts {
			add("sys/pairs", []pcFT{a}, []pcFT{b})
		}
	}
	// no parameters, and different parameter counts
	add("sys/count", []pcFT{}, []pcFT{})
	add("sys/count", []pcFT{ts[0]}, []pcFT{})
	add("sys/count", []pcFT{}, []pcFT{ts[0]})
	add("sys/count", []pcFT{ts[0], ts[0]}, []pcFT{ts[0]})
	// several parameters: random lists of the same length, often equal
	for i := 0; i < 200; i++ {
		n := 1 + r.IntN(4)
		a, b := make([]pcFT, n), make([]pcFT, n)
		for j := range a {
			a[j] = ts[r.IntN(len(ts))]
			if r.IntN(3) == 0 {
				b[j] = ts[r.IntN(len(ts))]
			} else {
				b[j] = a[j]
			}
		}
		add("rand/lists", a, b)
	}
}
