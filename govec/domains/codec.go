package domains

// Domain "codec": block D01.8 (pkg/util/codec: number.go, bytes.go, float.go,
// decimal.go, codec.go).
//
// Values:
//   - byte strings (encoded keys/values, raw data) are hex; a remain is hex;
//   - a datum is the datum2 form [kind, payload...] (d2DatumIn) and is output as
//     d2DatumOut; a field type is the d2FtIn form;
//   - float64 arguments are IEEE bits;
//   - a location is null (time.UTC, the singleton EncodeMySQLTime compares with)
//     or an offset in seconds (time.FixedZone("Z", offset));
//   - nc is the new-collation switch the op runs with
//     (collate.SetNewCollationEnabledForTest);
//   - hash.Hash64 / io.Writer / base.Hasher arguments are recorders: the output is
//     the concatenation of the bytes written (hex), not a hash value, so a
//     difference shows which bytes differ.
//
// The chunk ops build a chunk from [fts, rows] with chunk.AppendDatum (rows are
// lists of datums matching the column types; KindNull is a NULL cell).

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"math/rand/v2"
	"strings"
	gotime "time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	"github.com/pingcap/tidb/pkg/util/codec"
	"github.com/pingcap/tidb/pkg/util/collate"
)

const codecSeed = 20261109

//go:embed testdata/codec_literals.tsv
var codecLiteralsTSV string

func init() {
	vec.Register(&vec.Domain{
		Name:     "codec",
		Seed:     codecSeed,
		Errors:   datumErrors,
		Coverage: codecCoverage,
		Blocks:   []string{"D01.8"},
		Exempt:   codecExempt,
		Gzip:     true,
		Generate: codecGenerate,
		Exec:     codecExec,
	})
}

var codecExempt = map[string]string{
	"init":             "sets types.Hash64ForDatum; tidb-rs calls codec::init() at start-up (covered through the hash64 op)",
	"fastReverseBytes": "word-wise complement on amd64; same result as safeReverseBytes (covered through the desc encodings on amd64)",
	"safeReverseBytes": "byte-wise complement on non-amd64; covered through the desc encodings on arm64",
	"NewDecoder":       "constructor; covered by the decoder op",
	"HashChunkColumns": "HashChunkSelected with sel = nil, ignoreNull = false; covered by chunk_selected",
}

var codecCoverage = map[string]vec.Coverage{
	"enc_int": {Min: 500, GoFuncs: []string{"EncodeInt", "EncodeIntDesc", "EncodeIntToCmpUint", "EncodeVarint",
		"EncodeComparableVarint", "DecodeCmpUintToInt"}},
	"enc_uint":  {Min: 300, GoFuncs: []string{"EncodeUint", "EncodeUintDesc", "EncodeUvarint", "EncodeComparableUvarint"}},
	"enc_float": {Min: 200, GoFuncs: []string{"EncodeFloat", "EncodeFloatDesc", "encodeFloatToCmpUint64"}},
	"enc_bytes": {Min: 300, GoFuncs: []string{"EncodeBytes", "EncodeBytesDesc", "EncodeCompactBytes", "EncodedBytesLength",
		"EncodeBytesExt", "reallocBytes", "reverseBytes"}},
	"dec": {Min: 3000, GoFuncs: []string{"DecodeInt", "DecodeIntDesc", "DecodeUint", "DecodeUintDesc", "DecodeVarint",
		"DecodeUvarint", "DecodeComparableVarint", "DecodeComparableUvarint", "DecodeFloat", "DecodeFloatDesc",
		"decodeCmpUintToFloat", "DecodeBytes", "DecodeBytesDesc", "decodeBytes", "DecodeCompactBytes", "DecodeDecimal"}},
	"enc_decimal": {Min: 300, GoFuncs: []string{"EncodeDecimal", "valueSizeOfDecimal"}},
	"encode": {Min: 2000, GoFuncs: []string{"EncodeKey", "EncodeValue", "encode", "preRealloc", "encodeString",
		"encodeBytes", "sizeBytes", "encodeSignedInt", "encodeUnsignedInt", "sizeInt", "EncodeMySQLTime"}},
	"estimate":  {Min: 200, GoFuncs: []string{"EstimateValueSize", "valueSizeOfBytes", "valueSizeOfSignedInt", "valueSizeOfUnsignedInt"}},
	"hash_code": {Min: 200, GoFuncs: []string{"HashCode"}},
	"hash64":    {Min: 50, GoFuncs: []string{"Hash64"}},
	"decode_one": {Min: 3000, GoFuncs: []string{"DecodeOne", "Decode", "CutOne", "peek", "peekBytes", "peekCompactBytes",
		"peekVarint", "peekUvarint", "SetRawValues", "CutColumnID"}},
	"decode_range": {Min: 500, GoFuncs: []string{"DecodeRange"}},
	"mysql_time":   {Min: 150, GoFuncs: []string{"EncodeMySQLTime"}},
	"decode_as":    {Min: 300, GoFuncs: []string{"DecodeAsDateTime", "DecodeAsFloat32"}},
	"chunk_hash": {Min: 1000, GoFuncs: []string{"HashChunkRow", "EqualChunkRow", "EncodeHashChunkRowIdx",
		"ConvertByCollation"}},
	"chunk_selected": {Min: 1000, GoFuncs: []string{"HashChunkSelected"}},
	"serialize_keys": {Min: 1000, GoFuncs: []string{"SerializeKeys"}},
	"hash_group_key": {Min: 400, GoFuncs: []string{"HashGroupKey"}},
	"decoder": {Min: 600, GoFuncs: []string{"Decoder.DecodeOne", "appendIntToChunk", "appendUintToChunk",
		"appendFloatToChunk"}},
	"convert_by_collation": {Min: 100, GoFuncs: []string{"ConvertByCollation", "ConvertByCollationStr"}},
}

// CodecMinedTests: the Go test functions whose literals are in testdata/codec_literals.tsv.
func CodecMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, l := range parseDatumLiterals(codecLiteralsTSV) {
		if !seen[l.fn] {
			seen[l.fn] = true
			fns = append(fns, l.fn)
		}
	}
	return fns
}

// ---------------------------------------------------------------- helpers

func hexArg(p []json.RawMessage, i int) []byte {
	if string(p[i]) == "null" {
		return nil
	}
	b, err := hex.DecodeString(arg[string](p, i))
	if err != nil {
		panic(err)
	}
	return b
}

func locArg(p []json.RawMessage, i int) *gotime.Location {
	if string(p[i]) == "null" {
		return gotime.UTC
	}
	return gotime.FixedZone("Z", arg[int](p, i))
}

func datumsArg(p []json.RawMessage, i int) []types.Datum {
	var ds []types.Datum
	for _, q := range rawList(arg[[]json.RawMessage](p, i)) {
		ds = append(ds, d2DatumIn(q))
	}
	return ds
}

func ftsArg(p []json.RawMessage, i int) []*types.FieldType {
	var fts []*types.FieldType
	for _, q := range rawList(arg[[]json.RawMessage](p, i)) {
		fts = append(fts, d2FtIn(q))
	}
	return fts
}

// chunkArg builds a chunk: fts at i, rows (lists of datums) at i+1.
func chunkArg(p []json.RawMessage, i int) ([]*types.FieldType, *chunk.Chunk) {
	fts := ftsArg(p, i)
	rows := rawList(arg[[]json.RawMessage](p, i+1))
	chk := chunk.NewChunkWithCapacity(fts, len(rows))
	for _, r := range rows {
		for c, q := range rawList(r) {
			d := d2DatumIn(q)
			chk.AppendDatum(c, &d)
		}
	}
	return fts, chk
}

func boolsArg(p []json.RawMessage, i int) []bool {
	if string(p[i]) == "null" {
		return nil
	}
	return arg[[]bool](p, i)
}

// recHash records the bytes written to it (hash.Hash64).
type recHash struct{ b []byte }

func (h *recHash) Write(p []byte) (int, error) { h.b = append(h.b, p...); return len(p), nil }
func (h *recHash) Sum(b []byte) []byte         { return append(b, h.b...) }
func (h *recHash) Reset()                      { h.b = nil }
func (h *recHash) Size() int                   { return 8 }
func (h *recHash) BlockSize() int              { return 1 }
func (h *recHash) Sum64() uint64               { return 0 }

// codecRecHasher records the bytes of HashBytes (base.Hasher).
type codecRecHasher struct {
	b     []byte
	cache []byte
}

func (h *codecRecHasher) HashBool(bool)       { panic("unused") }
func (h *codecRecHasher) HashInt(int)         { panic("unused") }
func (h *codecRecHasher) HashInt64(int64)     { panic("unused") }
func (h *codecRecHasher) HashUint64(uint64)   { panic("unused") }
func (h *codecRecHasher) HashFloat64(float64) { panic("unused") }
func (h *codecRecHasher) HashRune(rune)       { panic("unused") }
func (h *codecRecHasher) HashString(string)   { panic("unused") }
func (h *codecRecHasher) HashByte(byte)       { panic("unused") }
func (h *codecRecHasher) HashBytes(b []byte)  { h.b = append(h.b, b...) }
func (h *codecRecHasher) Reset()              { h.b = nil }
func (h *codecRecHasher) SetCache(b []byte)   { h.cache = b }
func (h *codecRecHasher) Cache() []byte       { return h.cache }
func (h *codecRecHasher) Sum64() uint64       { return 0 }

type remOut struct {
	Rem string `json:"rem"`
	V   any    `json:"v"`
}

func hx(b []byte) string { return hex.EncodeToString(b) }

// codecTry runs f; a panic is recorded as {"panic": true} (the message is Go's runtime
// text, which Rust does not reproduce).
func codecTry(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"panic": true}
		}
	}()
	return f()
}

// ---------------------------------------------------------------- exec

func codecExec(op string, p []json.RawMessage) (out any, err error) {
	collate.SetNewCollationEnabledForTest(true)
	defer collate.SetNewCollationEnabledForTest(true)
	switch op {
	case "enc_int":
		v := arg[int64](p, 0)
		return map[string]any{
			"asc": hx(codec.EncodeInt(nil, v)), "desc": hx(codec.EncodeIntDesc(nil, v)),
			"var": hx(codec.EncodeVarint(nil, v)), "cmpvar": hx(codec.EncodeComparableVarint(nil, v)),
			"cmp": codec.EncodeIntToCmpUint(v), "back": codec.DecodeCmpUintToInt(uint64(v)),
		}, nil
	case "enc_uint":
		v := arg[uint64](p, 0)
		return map[string]any{
			"asc": hx(codec.EncodeUint(nil, v)), "desc": hx(codec.EncodeUintDesc(nil, v)),
			"var": hx(codec.EncodeUvarint(nil, v)), "cmpvar": hx(codec.EncodeComparableUvarint(nil, v)),
		}, nil
	case "enc_float":
		f := bitsF(arg[uint64](p, 0))
		return map[string]any{"asc": hx(codec.EncodeFloat(nil, f)), "desc": hx(codec.EncodeFloatDesc(nil, f))}, nil
	case "enc_bytes":
		// prefix: bytes already in b (EncodeBytesDesc reverses only the appended part).
		pre, data := hexArg(p, 0), hexArg(p, 1)
		cp := func() []byte { return append([]byte(nil), pre...) }
		return map[string]any{
			"asc": hx(codec.EncodeBytes(cp(), data)), "desc": hx(codec.EncodeBytesDesc(cp(), data)),
			"compact": hx(codec.EncodeCompactBytes(cp(), data)), "len": codec.EncodedBytesLength(len(data)),
			"ext": hx(codec.EncodeBytesExt(cp(), data, true)), "ext2": hx(codec.EncodeBytesExt(cp(), data, false)),
		}, nil
	case "dec":
		return codecDec(arg[string](p, 0), hexArg(p, 1))
	case "enc_decimal":
		dec := new(types.MyDecimal)
		_ = dec.FromString([]byte(arg[string](p, 0)))
		prec, frac := arg[int](p, 1), arg[int](p, 2)
		b, err := codec.EncodeDecimal(nil, dec, prec, frac)
		var d types.Datum
		d.SetMysqlDecimal(dec)
		d.SetLength(prec)
		d.SetFrac(frac)
		size, serr := codec.EstimateValueSize(types.DefaultStmtNoWarningContext, d)
		return map[string]any{"b": hx(b), "size": size, "size_err": d2ErrMsg(serr)}, err
	case "encode":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		comparable := arg[bool](p, 1)
		loc := locArg(p, 2)
		pre := hexArg(p, 3)
		ds := datumsArg(p, 4)
		var b []byte
		if comparable {
			b, err = codec.EncodeKey(loc, pre, ds...)
		} else {
			b, err = codec.EncodeValue(loc, pre, ds...)
		}
		return map[string]any{"b": hx(b), "nil": b == nil}, err
	case "mysql_time":
		var b []byte
		b, err = codec.EncodeMySQLTime(locArg(p, 2), timeOfRaw(arg[uint64](p, 0)), arg[byte](p, 1), hexArg(p, 3))
		return map[string]any{"b": hx(b), "nil": b == nil}, err
	case "estimate":
		d := d2DatumIn(arg[[]json.RawMessage](p, 0))
		return codec.EstimateValueSize(types.DefaultStmtNoWarningContext, d)
	case "hash_code":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		d := d2DatumIn(arg[[]json.RawMessage](p, 1))
		return hx(codec.HashCode(hexArg(p, 2), d)), nil
	case "hash64":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		d := d2DatumIn(arg[[]json.RawMessage](p, 1))
		h := &codecRecHasher{}
		codec.Hash64(h, &d)
		return hx(h.b), nil
	case "decode_one":
		// Each function under its own recover: a panic is {"panic": true} (Rust must panic there too).
		b := hexArg(p, 0)
		one := codecTry(func() any {
			rem, d, err := codec.DecodeOne(b)
			return map[string]any{"rem": hx(rem), "d": d2DatumOut(d), "err": d2ErrMsg(err)}
		})
		cut := codecTry(func() any {
			data, crem, cerr := codec.CutOne(b)
			return map[string]any{"data": hx(data), "rem": hx(crem), "err": d2ErrMsg(cerr)}
		})
		all := codecTry(func() any {
			ds, derr := codec.Decode(b, 2)
			return map[string]any{"ds": datumOuts(ds), "err": d2ErrMsg(derr)}
		})
		cid := codecTry(func() any {
			crem2, id, ciderr := codec.CutColumnID(b)
			return map[string]any{"rem": hx(crem2), "id": id, "err": d2ErrMsg(ciderr)}
		})
		raw := codecTry(func() any {
			raws := make([]types.Datum, 2)
			rerr := codec.SetRawValues(b, raws)
			return map[string]any{"ds": datumOuts(raws), "err": d2ErrMsg(rerr)}
		})
		return map[string]any{"one": one, "cut": cut, "all": all, "cid": cid, "raw": raw}, nil
	case "decode_range":
		var tps []byte
		if string(p[2]) != "null" {
			tps = hexArg(p, 2)
		}
		var loc *gotime.Location
		if string(p[3]) != "null" {
			loc = gotime.FixedZone("Z", arg[int](p, 3))
		}
		vals, rem, err := codec.DecodeRange(hexArg(p, 0), arg[int](p, 1), tps, loc)
		return map[string]any{"vals": datumOuts(vals), "rem": hx(rem)}, err
	case "decode_as":
		b := hexArg(p, 0)
		tp := arg[byte](p, 1)
		var rem []byte
		var d types.Datum
		if arg[bool](p, 2) {
			rem, d, err = codec.DecodeAsFloat32(b, tp)
		} else {
			var loc *gotime.Location
			if string(p[3]) != "null" {
				loc = gotime.FixedZone("Z", arg[int](p, 3))
			}
			rem, d, err = codec.DecodeAsDateTime(b, tp, loc)
		}
		return map[string]any{"rem": hx(rem), "d": d2DatumOut(d)}, err
	case "chunk_hash":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		fts, chk := chunkArg(p, 1)
		colIdx := arg[[]int](p, 3)
		tps := make([]*types.FieldType, len(colIdx))
		for i, c := range colIdx {
			tps[i] = fts[c]
		}
		ctx := types.DefaultStmtNoWarningContext
		var rows []string
		var eq []any
		for i := 0; i < chk.NumRows(); i++ {
			var w bytes.Buffer
			buf := make([]byte, 1)
			if err := codec.HashChunkRow(ctx, &w, chk.GetRow(i), tps, colIdx, buf); err != nil {
				return rows, err
			}
			rows = append(rows, hx(w.Bytes()))
			j := (i + 1) % chk.NumRows()
			e, err := codec.EqualChunkRow(ctx, chk.GetRow(i), tps, colIdx, chk.GetRow(j), tps, colIdx)
			eq = append(eq, map[string]any{"eq": e, "err": d2ErrMsg(err)})
		}
		_, merr := codec.EqualChunkRow(ctx, chk.GetRow(0), tps, colIdx, chk.GetRow(0), tps, colIdx[:0])
		return map[string]any{"rows": rows, "eq": eq, "mismatch": d2ErrMsg(merr)}, nil
	case "chunk_selected":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		fts, chk := chunkArg(p, 1)
		col := arg[int](p, 3)
		sel := boolsArg(p, 4)
		ignoreNull := arg[bool](p, 5)
		n := chk.NumRows()
		hs := make([]*recHash, n)
		h := make([]hash.Hash64, n)
		for i := range hs {
			hs[i] = &recHash{}
			h[i] = hs[i]
		}
		isNull := make([]bool, n)
		buf := make([]byte, 1)
		err := codecHashSelected(h, chk, fts[col], col, buf, isNull, sel, ignoreNull)
		var rows []string
		for _, x := range hs {
			rows = append(rows, hx(x.b))
		}
		return map[string]any{"rows": rows, "null": isNull}, err
	case "serialize_keys":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		fts, chk := chunkArg(p, 1)
		col := arg[int](p, 3)
		used := arg[[]int](p, 4)
		filter := boolsArg(p, 5)
		var nullv []bool
		if arg[bool](p, 6) {
			nullv = make([]bool, chk.NumRows())
		}
		mode := codec.SerializeMode(arg[int](p, 7))
		keys := make([][]byte, len(used))
		err := codec.SerializeKeys(types.DefaultStmtNoWarningContext, chk, fts[col], col, used, filter, nullv, mode, keys)
		var ks []string
		for _, k := range keys {
			ks = append(ks, hx(k))
		}
		return map[string]any{"keys": ks, "null": nullv}, err
	case "hash_group_key":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		fts, chk := chunkArg(p, 1)
		loc := locArg(p, 3)
		n := chk.NumRows()
		buf := make([][]byte, n)
		buf, err := codec.HashGroupKey(loc, n, chk.Column(0), buf, fts[0])
		var bs []string
		for _, b := range buf {
			bs = append(bs, hx(b))
		}
		return map[string]any{"buf": bs, "nil": buf == nil}, err
	case "decoder":
		// fts, values: per row, per column an encoded value (hex); loc. Every DecodeOne
		// and every cell read is under its own recover ({"panic": true}).
		fts := ftsArg(p, 0)
		chk := chunk.NewChunkWithCapacity(fts, 4)
		dec := codec.NewDecoder(chk, locArg(p, 2))
		var rems []any
		for _, r := range arg[[][]string](p, 1) {
			for c, v := range r {
				b, _ := hex.DecodeString(v)
				rems = append(rems, codecTry(func() any {
					rem, err := dec.DecodeOne(b, c, fts[c])
					if err != nil {
						return map[string]any{"err": d2ErrMsg(err)}
					}
					return hx(rem)
				}))
			}
		}
		var cols [][]any
		for c, ft := range fts {
			var cells []any
			for i := 0; i < chk.Column(c).Rows(); i++ {
				cells = append(cells, codecTry(func() any {
					if chk.Column(c).IsNull(i) {
						return nil
					}
					return d2DatumOut(chk.GetRow(i).GetDatum(c, ft))
				}))
			}
			cols = append(cols, cells)
		}
		return map[string]any{"rems": rems, "cols": cols}, nil
	case "convert_by_collation":
		collate.SetNewCollationEnabledForTest(arg[bool](p, 0))
		ft := d2FtIn(arg[[]json.RawMessage](p, 1))
		s := arg[vec.GoString](p, 2)
		return map[string]any{
			"b": hx(codec.ConvertByCollation([]byte(s), ft)),
			"s": hx([]byte(codec.ConvertByCollationStr(string(s), ft))),
		}, nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

func codecHashSelected(hh []hash.Hash64, chk *chunk.Chunk, ft *types.FieldType, col int, buf []byte, isNull, sel []bool, ignoreNull bool) error {
	if sel == nil && !ignoreNull {
		return codec.HashChunkColumns(types.DefaultStmtNoWarningContext, hh, chk, ft, col, buf, isNull)
	}
	return codec.HashChunkSelected(types.DefaultStmtNoWarningContext, hh, chk, ft, col, buf, isNull, sel, ignoreNull)
}

func codecDec(fn string, b []byte) (any, error) {
	var rem []byte
	var v any
	var err error
	switch fn {
	case "int":
		var x int64
		rem, x, err = codec.DecodeInt(b)
		v = x
	case "int_desc":
		var x int64
		rem, x, err = codec.DecodeIntDesc(b)
		v = x
	case "uint":
		var x uint64
		rem, x, err = codec.DecodeUint(b)
		v = x
	case "uint_desc":
		var x uint64
		rem, x, err = codec.DecodeUintDesc(b)
		v = x
	case "varint":
		var x int64
		rem, x, err = codec.DecodeVarint(b)
		v = x
	case "uvarint":
		var x uint64
		rem, x, err = codec.DecodeUvarint(b)
		v = x
	case "cmp_varint":
		var x int64
		rem, x, err = codec.DecodeComparableVarint(b)
		v = x
	case "cmp_uvarint":
		var x uint64
		rem, x, err = codec.DecodeComparableUvarint(b)
		v = x
	case "float":
		var x float64
		rem, x, err = codec.DecodeFloat(b)
		v = fBits(x)
	case "float_desc":
		var x float64
		rem, x, err = codec.DecodeFloatDesc(b)
		v = fBits(x)
	case "bytes", "bytes_desc":
		var x []byte
		if fn == "bytes" {
			rem, x, err = codec.DecodeBytes(b, nil)
		} else {
			rem, x, err = codec.DecodeBytesDesc(b, nil)
		}
		v = hx(x)
	case "compact_bytes":
		var x []byte
		rem, x, err = codec.DecodeCompactBytes(b)
		v = hx(x)
	case "decimal":
		var d *types.MyDecimal
		var prec, frac int
		rem, d, prec, frac, err = codec.DecodeDecimal(b)
		var dv any
		if d != nil {
			dv = decOut(d)
		}
		v = map[string]any{"dec": dv, "prec": prec, "frac": frac}
	default:
		panic("codecDec: " + fn)
	}
	return remOut{Rem: hx(rem), V: v}, err
}

// ---------------------------------------------------------------- generator

var codecDecFns = []string{"int", "int_desc", "uint", "uint_desc", "varint", "uvarint", "cmp_varint", "cmp_uvarint",
	"float", "float_desc", "bytes", "bytes_desc", "compact_bytes", "decimal"}

var codecColls = []string{"binary", "utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci",
	"utf8mb4_0900_bin", "gbk_bin", "gbk_chinese_ci", "latin1_bin", "utf8_bin", "ascii_bin", "utf8_general_ci"}

func codecGenerate(g *vec.Gen, rng *rand.Rand) {
	lits := parseDatumLiterals(codecLiteralsTSV)
	ints := []int64{0, 1, -1, 7, 8, 239, 240, 255, 256, -255, -256, 0xffff, -0xffff, 0x10000, 1 << 24, -(1 << 24),
		1<<32 - 1, 1 << 32, -(1 << 32), 1 << 40, 1 << 48, 1 << 56, -(1 << 56), math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1, 63, 64, -64, -65, 127, 128, 8191, 8192}
	for _, l := range lits {
		if l.num != nil {
			ints = append(ints, *l.num)
			g.Add(l.fn, "enc_int", *l.num)
			g.Add(l.fn, "enc_uint", uint64(*l.num))
		}
	}
	for _, v := range ints {
		g.Add("sys/int", "enc_int", v)
		g.Add("sys/int", "enc_uint", uint64(v))
	}
	for range 300 {
		v := int64(rng.Uint64()) >> rng.IntN(64)
		g.Add("rand/int", "enc_int", v)
		g.Add("rand/int", "enc_uint", rng.Uint64()>>rng.IntN(64))
	}
	floats := []float64{0, math.Copysign(0, -1), 1, -1, 0.5, -0.5, math.MaxFloat64, -math.MaxFloat64,
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN(),
		math.Float64frombits(0x7ff8000000000001), math.Float64frombits(0xfff8000000000000), 1e300, 1e-300,
		3.14159, math.MaxFloat32}
	for _, f := range floats {
		g.Add("sys/float", "enc_float", fBits(f))
	}
	for range 200 {
		g.Add("rand/float", "enc_float", rng.Uint64())
	}
	bss := [][]byte{nil, {}, {0}, {1, 2, 3}, {1, 2, 3, 0}, {1, 2, 3, 4, 5, 6, 7, 8}, {1, 2, 3, 4, 5, 6, 7, 8, 9},
		{0xff}, bytes.Repeat([]byte{0xff}, 8), bytes.Repeat([]byte{0}, 16), bytes.Repeat([]byte{0xab}, 17)}
	for _, l := range lits {
		if l.str != nil {
			bss = append(bss, []byte(*l.str))
			g.Add(l.fn, "enc_bytes", "", hx([]byte(*l.str)))
		}
	}
	for i, b := range bss {
		pre := ""
		if i%3 == 1 {
			pre = "abcd"
		}
		g.Add("sys/bytes", "enc_bytes", pre, hx(b))
	}
	for range 300 {
		b := make([]byte, rng.IntN(40))
		for i := range b {
			b[i] = byte(rng.IntN(256))
		}
		g.Add("rand/bytes", "enc_bytes", "", hx(b))
	}
	// Decimals.
	decs := []string{"0", "1", "-1", "0.1", "-0.1", "123.456", "-123.456", "99999999999999999999.999999999",
		"12345678901234567890123456789012345678901234567890123456789012345", "0.000000000000000000000000000001",
		"1e-30", "100", "-100.00", "3.14159265358979323846", "1234567890.1234567890"}
	for _, l := range lits {
		if l.str != nil {
			var d types.MyDecimal
			if d.FromString([]byte(*l.str)) == nil {
				decs = append(decs, *l.str)
				g.Add(l.fn, "enc_decimal", *l.str, 0, 0)
			}
		}
	}
	for _, s := range decs {
		for _, pf := range [][2]int{{0, 0}, {65, 30}, {10, 2}, {5, 5}, {3, 0}, {30, 31}, {66, 0}, {20, 10}} {
			g.Add("sys/decimal", "enc_decimal", s, pf[0], pf[1])
		}
	}

	pool := codecPool(lits)
	// Encodings: single datums and random tuples, both forms, both collation modes.
	var encoded []string
	for i, d := range pool {
		for _, nc := range []bool{true, false} {
			for _, cmp := range []bool{true, false} {
				var loc any
				if i%4 == 0 {
					loc = 28800
				}
				g.Add("sys/datum", "encode", nc, cmp, loc, "", []any{d})
			}
		}
		g.Add("sys/datum", "estimate", d)
		g.Add("sys/datum", "hash_code", i%2 == 0, d, "")
		if i%3 == 0 {
			g.Add("sys/datum", "hash64", true, d)
		}
		// Encoded forms for the decoders.
		b, err := codec.EncodeKey(gotime.UTC, nil, d2DatumIn(rawListOne(d)))
		if err == nil {
			encoded = append(encoded, hx(b))
		}
		b, err = codec.EncodeValue(gotime.UTC, nil, d2DatumIn(rawListOne(d)))
		if err == nil {
			encoded = append(encoded, hx(b))
		}
	}
	for range 1500 {
		n := 1 + rng.IntN(5)
		var ds []any
		for range n {
			ds = append(ds, pool[rng.IntN(len(pool))])
		}
		nc, cmp := rng.IntN(2) == 0, rng.IntN(2) == 0
		pre := ""
		if rng.IntN(5) == 0 {
			pre = "ff00"
		}
		g.Add("rand/datums", "encode", nc, cmp, nil, pre, ds)
		var dds []types.Datum
		for _, d := range ds {
			dds = append(dds, d2DatumIn(rawListOne(d.([]any))))
		}
		if b, err := codec.EncodeKey(gotime.UTC, nil, dds...); err == nil {
			encoded = append(encoded, hx(b))
		}
		if b, err := codec.EncodeValue(gotime.UTC, nil, dds...); err == nil {
			encoded = append(encoded, hx(b))
		}
	}
	// Times through EncodeMySQLTime directly.
	for _, d := range pool {
		if d[0] == types.KindMysqlTime {
			for _, tp := range []byte{mysql.TypeUnspecified, mysql.TypeTimestamp, mysql.TypeDatetime} {
				g.Add("sys/time", "mysql_time", d[1], tp, 28800, "")
				g.Add("sys/time", "mysql_time", d[1], tp, nil, "01")
				g.Add("sys/time", "mysql_time", d[1], tp, -18000, "")
			}
		}
	}
	// Decoders over valid, truncated and mutated encodings.
	mutate := func(s string) []string {
		b, _ := hex.DecodeString(s)
		var out []string
		if len(b) > 0 {
			out = append(out, hx(b[:rng.IntN(len(b))]))
			c := append([]byte(nil), b...)
			c[0] = byte(rng.IntN(256))
			out = append(out, hx(c))
			c = append([]byte(nil), b...)
			c[rng.IntN(len(c))] ^= byte(1 + rng.IntN(255))
			out = append(out, hx(c))
			// Lengths and markers pushed to the limits.
			c = append([]byte(nil), b...)
			if len(c) > 1 {
				c[1] = []byte{0x00, 0x7f, 0x80, 0xff, 0xfe}[rng.IntN(5)]
			}
			out = append(out, hx(c))
		}
		return out
	}
	for i, e := range encoded {
		g.Add("sys/encoded", "decode_one", e)
		for _, m := range mutate(e) {
			g.Add("mut/encoded", "decode_one", m)
		}
		if i%5 == 0 {
			g.Add("sys/encoded", "decode_range", e, 3, nil, nil)
		}
	}
	for _, s := range []string{"", "00", "01", "fa", "fb", "0300", "ff", "0100", "0f", "09ffffffffffffffffffff01",
		"02ff", "0201", "0aff", "14", "1400000001", "06", "0601", "080a"} {
		g.Add("sys/bad", "decode_one", s)
		g.Add("sys/bad", "decode_range", s, 1, nil, nil)
	}
	for _, l := range lits {
		if l.str != nil {
			g.Add(l.fn, "decode_one", hx([]byte(*l.str)))
		}
	}
	// Primitive decoders on the encoded primitives and random bytes.
	var prims []string
	for _, v := range ints {
		prims = append(prims, hx(codec.EncodeInt(nil, v)), hx(codec.EncodeVarint(nil, v)),
			hx(codec.EncodeComparableVarint(nil, v)), hx(codec.EncodeComparableUvarint(nil, uint64(v))))
	}
	for _, b := range bss {
		prims = append(prims, hx(codec.EncodeBytes(nil, b)), hx(codec.EncodeBytesDesc(nil, b)), hx(codec.EncodeCompactBytes(nil, b)))
	}
	for _, s := range decs {
		var d types.MyDecimal
		_ = d.FromString([]byte(s))
		if b, err := codec.EncodeDecimal(nil, &d, 0, 0); err == nil {
			prims = append(prims, hx(b))
		}
	}
	prims = append(prims, "", "00", "ff", "ffffffffffffffffffff02", "8080808080808080808080", "07", "f8", "f9", "ffff",
		"0000000000000000f7", "01020300000000fafa", "0102030000000000f9", "0102030405060708ff", "01020300000001fa")
	for i, s := range prims {
		fns := codecDecFns
		if i%2 == 1 {
			fns = []string{codecDecFns[rng.IntN(len(codecDecFns))], codecDecFns[rng.IntN(len(codecDecFns))]}
		}
		for _, fn := range fns {
			g.Add("sys/prim", "dec", fn, s)
		}
		for _, m := range mutate(s) {
			g.Add("mut/prim", "dec", codecDecFns[rng.IntN(len(codecDecFns))], m)
		}
	}
	// DecodeRange with index column types.
	for range 400 {
		n := 1 + rng.IntN(3)
		var ds []types.Datum
		var tps []byte
		for range n {
			switch rng.IntN(4) {
			case 0:
				t, _ := types.ParseTime(types.DefaultStmtNoWarningContext, "2011-11-10 11:11:11", []byte{mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDate}[rng.IntN(3)], 6)
				var d types.Datum
				d.SetMysqlTime(t)
				ds = append(ds, d)
				tps = append(tps, t.Type())
			case 1:
				ds = append(ds, types.NewFloat64Datum(float64(rng.IntN(1000))/7))
				tps = append(tps, mysql.TypeFloat)
			case 2:
				ds = append(ds, types.NewIntDatum(int64(rng.IntN(1000))-500))
				tps = append(tps, mysql.TypeLonglong)
			default:
				ds = append(ds, types.NewDatum(nil))
				tps = append(tps, []byte{mysql.TypeDatetime, mysql.TypeFloat}[rng.IntN(2)])
			}
		}
		b, _ := codec.EncodeKey(gotime.UTC, nil, ds...)
		tail := [][]byte{nil, {0}, {1}, {250}, {251}, {7}}[rng.IntN(6)]
		b = append(b, tail...)
		var loc any
		if rng.IntN(2) == 0 {
			loc = 28800
		}
		short := tps
		if rng.IntN(6) == 0 && len(tps) > 0 {
			short = tps[:len(tps)-1]
		}
		g.Add("rand/range", "decode_range", hx(b), n, hx(short), loc)
		g.Add("rand/range", "decode_range", hx(b), n, nil, nil)
		if len(b) > 0 {
			g.Add("rand/range", "decode_as", hx(b), tps[0], tps[0] == mysql.TypeFloat, loc)
			g.Add("rand/range", "decode_as", hx(b), mysql.TypeFloat, true, nil)
		}
	}
	codecGenerateChunks(g, rng, pool)
}

// codecPool: datums of every kind (datum2 form).
func codecPool(lits []datumLit) [][]any {
	var pool [][]any
	add := func(d ...any) { pool = append(pool, d) }
	add(types.KindNull)
	add(types.KindMinNotNull)
	add(types.KindMaxValue)
	for _, n := range []int64{0, 1, -1, 239, 240, math.MaxInt64, math.MinInt64, 1 << 40, -(1 << 40), 127, -128} {
		add(types.KindInt64, n)
	}
	for _, u := range []uint64{0, 1, 255, 1 << 63, math.MaxUint64} {
		add(types.KindUint64, u)
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 1.5, -1.5, math.Inf(1), math.Inf(-1), math.NaN(), 1e300} {
		add(types.KindFloat64, fBits(f))
		add(types.KindFloat32, fBits(float64(float32(f))))
	}
	strs := []string{"", "a", "A", "a ", "abc", "ABC", "ä", "Ä", "ß", "ss", "中文", "\x00", "\xff\xfe", "a\x00b",
		"12345678", "123456789", strings.Repeat("x", 30), "Ｆ", "𠀀", "\U0002CEA1", "K", "K"}
	for _, l := range lits {
		if l.str != nil && len(*l.str) < 80 {
			strs = append(strs, *l.str)
		}
	}
	for i, s := range strs {
		add(types.KindString, vec.GoString(s), codecColls[i%len(codecColls)])
		if i%4 == 0 {
			add(types.KindBytes, hx([]byte(s)))
		}
	}
	add(types.KindBytes, nil)
	for _, h := range []string{"", "00", "01", "ff", "0102", "ffffffffffffffff", "010000000000000000"} {
		add(types.KindBinaryLiteral, h)
		add(types.KindMysqlBit, h)
	}
	for _, s := range []string{"0", "1", "-1.5", "123.456", "-0.000001", "99999999999999999999999999999999999.999999999999999999999999999999",
		"1e20", "-123456789012345678901234567890"} {
		add(types.KindMysqlDecimal, s)
	}
	for _, d := range [][2]int64{{0, 0}, {1e9, 0}, {-1500e6, 1}, {3723e9 + 5e5, 6}, {-(838*3600e9 + 59*60e9 + 59e9), 0}} {
		add(types.KindMysqlDuration, d[0], d[1])
	}
	for i, e := range []struct {
		v uint64
		n string
	}{{0, ""}, {1, "a"}, {3, "c"}, {1 << 40, "x"}} {
		add(types.KindMysqlEnum, e.v, vec.GoString(e.n), codecColls[i])
		add(types.KindMysqlSet, e.v, vec.GoString(e.n), codecColls[i+1])
	}
	ctx := types.DefaultStmtNoWarningContext
	for _, s := range []string{"2011-11-10 11:11:11.999999", "0000-00-00 00:00:00", "1970-01-01 00:00:01",
		"2038-01-19 03:14:07", "9999-12-31 23:59:59", "2011-11-10", "1000-01-01 00:00:00"} {
		for _, tp := range []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp} {
			if t, err := types.ParseTime(ctx, s, tp, types.MaxFsp); err == nil {
				add(types.KindMysqlTime, rawOfTime(t))
			}
		}
	}
	// A time whose packed form fails (month 13) and a TIMESTAMP for the zone conversion.
	add(types.KindMysqlTime, rawOfTime(types.NewTime(types.FromDate(2011, 13, 1, 0, 0, 0, 0), mysql.TypeDatetime, 0)))
	add(types.KindMysqlTime, rawOfTime(types.NewTime(types.FromDate(2011, 3, 13, 2, 30, 0, 0), mysql.TypeTimestamp, 0)))
	for _, js := range []string{"null", "true", "0", "-1", "1.5", "\"abc\"", "[1,2]", "{\"a\":1}", "18446744073709551615"} {
		j, err := types.ParseBinaryJSONFromString(js)
		if err != nil {
			panic(err)
		}
		add(types.KindMysqlJSON, j.TypeCode, hx(j.Value))
	}
	for _, v := range []string{"[]", "[1]", "[1.5,-2,0]"} {
		add(types.KindVectorFloat32, v)
	}
	return pool
}

// ---------------------------------------------------------------- chunks

type codecCol struct {
	ft  []any
	gen func(rng *rand.Rand) []any // a non-null datum
}

func codecCols() []codecCol {
	un := uint(mysql.UnsignedFlag)
	ftOf := func(tp byte, flag uint, flen, dec int, cs, co string, elems []vec.GoString) []any {
		var e any
		if elems != nil {
			e = elems
		}
		return []any{tp, flag, flen, dec, cs, co, e}
	}
	ints := []int64{0, 1, -1, 5, -5, math.MaxInt64, math.MinInt64, 1 << 40}
	intGen := func(rng *rand.Rand) []any { return []any{types.KindInt64, ints[rng.IntN(len(ints))]} }
	strs := []string{"", "a", "A", "a ", "ä", "Ä", "ß", "ss", "中文", "abc", "ABC  ", "K", "k"}
	strGen := func(rng *rand.Rand) []any {
		return []any{types.KindString, vec.GoString(strs[rng.IntN(len(strs))]), "utf8mb4_bin"}
	}
	var cols []codecCol
	for _, tp := range []byte{mysql.TypeTiny, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeYear} {
		cols = append(cols, codecCol{ftOf(tp, 0, -1, 0, "binary", "binary", nil), intGen})
		cols = append(cols, codecCol{ftOf(tp, un, -1, 0, "binary", "binary", nil), intGen})
	}
	fl := []float64{0, math.Copysign(0, -1), 1.5, -2.25, math.Inf(1), 1e30}
	cols = append(cols,
		codecCol{ftOf(mysql.TypeFloat, 0, -1, -1, "binary", "binary", nil), func(rng *rand.Rand) []any {
			return []any{types.KindFloat32, fBits(float64(float32(fl[rng.IntN(len(fl))])))}
		}},
		codecCol{ftOf(mysql.TypeDouble, 0, -1, -1, "binary", "binary", nil), func(rng *rand.Rand) []any {
			return []any{types.KindFloat64, fBits(fl[rng.IntN(len(fl))])}
		}})
	for _, co := range []string{"utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci", "binary", "gbk_chinese_ci", "latin1_bin"} {
		cs := strings.SplitN(co, "_", 2)[0]
		cols = append(cols, codecCol{ftOf(mysql.TypeVarchar, 0, 20, -1, cs, co, nil), strGen})
	}
	cols = append(cols, codecCol{ftOf(mysql.TypeBlob, 0, -1, -1, "binary", "binary", nil), strGen})
	ctx := types.DefaultStmtNoWarningContext
	for _, tp := range []byte{mysql.TypeDatetime, mysql.TypeDate, mysql.TypeTimestamp} {
		tp := tp
		cols = append(cols, codecCol{ftOf(tp, 0, -1, 6, "binary", "binary", nil), func(rng *rand.Rand) []any {
			s := []string{"2011-11-10 11:11:11.123456", "0000-00-00", "1970-01-01 00:00:01", "2038-01-19 03:14:07"}[rng.IntN(4)]
			t, err := types.ParseTime(ctx, s, tp, 6)
			if err != nil {
				panic(err)
			}
			return []any{types.KindMysqlTime, rawOfTime(t)}
		}})
	}
	cols = append(cols, codecCol{ftOf(mysql.TypeDuration, 0, -1, 3, "binary", "binary", nil), func(rng *rand.Rand) []any {
		return []any{types.KindMysqlDuration, []int64{0, 1e9, -1500e6, 3723e9}[rng.IntN(4)], 3}
	}})
	decGen := func(rng *rand.Rand) []any {
		return []any{types.KindMysqlDecimal, []string{"0", "1.50", "-1.5", "123.456", "0.000"}[rng.IntN(5)]}
	}
	cols = append(cols, codecCol{ftOf(mysql.TypeNewDecimal, 0, 10, 3, "binary", "binary", nil), decGen})
	ab := []vec.GoString{"a", "b", "C", "ä"}
	enumGen := func(rng *rand.Rand) []any {
		v := uint64(rng.IntN(6))
		n := ""
		if v >= 1 && v <= 4 {
			n = string(ab[v-1])
		}
		return []any{types.KindMysqlEnum, v, vec.GoString(n), "utf8mb4_general_ci"}
	}
	cols = append(cols,
		codecCol{ftOf(mysql.TypeEnum, 0, -1, -1, "utf8mb4", "utf8mb4_general_ci", ab), enumGen},
		codecCol{ftOf(mysql.TypeEnum, uint(mysql.EnumSetAsIntFlag), -1, -1, "utf8mb4", "utf8mb4_bin", ab), enumGen},
		codecCol{ftOf(mysql.TypeSet, 0, -1, -1, "utf8mb4", "utf8mb4_general_ci", ab), func(rng *rand.Rand) []any {
			return []any{types.KindMysqlSet, uint64(rng.IntN(16)), "", "utf8mb4_general_ci"}
		}},
		codecCol{ftOf(mysql.TypeBit, un, 10, 0, "binary", "binary", nil), func(rng *rand.Rand) []any {
			return []any{types.KindMysqlBit, []string{"00", "0001", "03ff", "ffffffffffffffffff"}[rng.IntN(4)]}
		}},
		codecCol{ftOf(mysql.TypeJSON, 0, -1, -1, "utf8mb4", "utf8mb4_bin", nil), func(rng *rand.Rand) []any {
			j, _ := types.ParseBinaryJSONFromString([]string{"1", "1.0", "\"a\"", "[1,2]", "{\"a\":1}", "null"}[rng.IntN(6)])
			return []any{types.KindMysqlJSON, j.TypeCode, hx(j.Value)}
		}},
		codecCol{ftOf(mysql.TypeTiDBVectorFloat32, 0, -1, -1, "binary", "binary", nil), func(rng *rand.Rand) []any {
			return []any{types.KindVectorFloat32, []string{"[]", "[1]", "[1.5,-2]"}[rng.IntN(3)]}
		}},
		codecCol{ftOf(mysql.TypeNull, 0, -1, -1, "binary", "binary", nil), func(*rand.Rand) []any {
			return []any{types.KindNull}
		}})
	return cols
}

func codecGenerateChunks(g *vec.Gen, rng *rand.Rand, pool [][]any) {
	cols := codecCols()
	mkRows := func(cs []codecCol, n int) [][]any {
		var rows [][]any
		for range n {
			var r []any
			for _, c := range cs {
				if rng.IntN(5) == 0 {
					r = append(r, []any{types.KindNull})
				} else {
					r = append(r, c.gen(rng))
				}
			}
			rows = append(rows, r)
		}
		return rows
	}
	fts := func(cs []codecCol) []any {
		var out []any
		for _, c := range cs {
			out = append(out, c.ft)
		}
		return out
	}
	for round := range 40 {
		for ci, c := range cols {
			nc := (round+ci)%2 == 0
			one := []codecCol{c}
			rows := mkRows(one, 2+rng.IntN(6))
			g.Add("rand/chunk", "chunk_hash", nc, fts(one), rows, []int{0})
			var sel any
			if rng.IntN(2) == 0 {
				s := make([]bool, len(rows))
				for i := range s {
					s[i] = rng.IntN(3) != 0
				}
				sel = s
			}
			g.Add("rand/chunk", "chunk_selected", nc, fts(one), rows, 0, sel, rng.IntN(2) == 0)
			used := []int{}
			for i := range rows {
				if rng.IntN(4) != 0 {
					used = append(used, i)
				}
			}
			used = append(used, 0)
			var filter any
			if rng.IntN(2) == 0 {
				f := make([]bool, len(rows))
				for i := range f {
					f[i] = rng.IntN(4) != 0
				}
				filter = f
			}
			g.Add("rand/chunk", "serialize_keys", nc, fts(one), rows, 0, used, filter, true, rng.IntN(3))
			if round%8 == 0 {
				g.Add("rand/chunk", "convert_by_collation", nc, c.ft, vec.GoString("aÄß K"))
			}
		}
		// Multi-column rows: HashChunkRow / EqualChunkRow over several columns.
		var cs []codecCol
		for range 2 + rng.IntN(3) {
			cs = append(cs, cols[rng.IntN(len(cols))])
		}
		rows := mkRows(cs, 3+rng.IntN(4))
		if rng.IntN(2) == 0 {
			rows = append(rows, rows[0]) // an equal pair
		}
		idx := []int{}
		for i := range cs {
			idx = append(idx, i)
		}
		rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
		g.Add("rand/chunk", "chunk_hash", round%2 == 0, fts(cs), rows, idx)
	}
	// HashGroupKey: one column, eval types Int/Real/Decimal/Datetime/Duration/Json/String/Vector.
	for round := range 20 {
		for _, c := range cols {
			tp := c.ft[0].(byte)
			if tp == mysql.TypeFloat || tp == mysql.TypeNull || tp == mysql.TypeEnum || tp == mysql.TypeSet || tp == mysql.TypeBit {
				continue // HashGroupKey reads eval-type buffers: Float32 / Enum / Set / Bit columns are not eval results
			}
			one := []codecCol{c}
			var loc any
			if round%2 == 0 {
				loc = 28800
			}
			g.Add("rand/chunk", "hash_group_key", round%3 != 0, fts(one), mkRows(one, 1+rng.IntN(5)), loc)
		}
	}
	// SerializeKeys with a nil null vector (a NULL cell panics in Go and in Rust).
	for _, c := range cols[:4] {
		one := []codecCol{c}
		g.Add("sys/chunk", "serialize_keys", true, fts(one), mkRows(one, 3), 0, []int{0, 1, 2}, nil, false, 0)
	}
	// Decoder: encoded values per column type, plus mutations.
	for range 60 {
		var cs []codecCol
		for range 1 + rng.IntN(3) {
			cs = append(cs, cols[rng.IntN(len(cols))])
		}
		var rows [][]string
		for range 1 + rng.IntN(4) {
			var r []string
			for _, c := range cs {
				var d types.Datum
				if rng.IntN(5) == 0 {
					d = d2DatumIn(rawListOne([]any{types.KindNull}))
				} else {
					d = d2DatumIn(rawListOne(c.gen(rng)))
				}
				var b []byte
				if rng.IntN(2) == 0 {
					b, _ = codec.EncodeKey(gotime.UTC, nil, d)
				} else {
					b, _ = codec.EncodeValue(gotime.UTC, nil, d)
				}
				if rng.IntN(6) == 0 && len(b) > 1 {
					b = b[:rng.IntN(len(b))]
				}
				r = append(r, hx(b))
			}
			rows = append(rows, r)
		}
		var loc any
		if rng.IntN(2) == 0 {
			loc = 28800
		}
		g.Add("rand/chunk", "decoder", fts(cs), rows, loc)
	}
	// Decoder: an encoded value of another type into a column (int into duration,
	// uint into enum / set / bit / time, float into float32, decimal rounding).
	for _, d := range pool {
		dd := d2DatumIn(rawListOne(d))
		kv, err := codec.EncodeValue(gotime.UTC, nil, dd)
		if err != nil {
			continue
		}
		kk, _ := codec.EncodeKey(gotime.UTC, nil, dd)
		for _, c := range cols {
			if rng.IntN(3) != 0 {
				continue
			}
			tp := c.ft[0].(byte)
			var rows [][]string
			for _, b := range [][]byte{kv, kk} {
				if codecDecoderFits(b[0], tp) {
					rows = append(rows, []string{hx(b)})
				}
			}
			if rows != nil {
				g.Add("cross/chunk", "decoder", []any{c.ft}, rows, 28800)
			}
		}
	}
}

// codecDecoderFits: the encoded flag is one Decoder.DecodeOne meets for a column of type
// tp in TiDB (an int into a DURATION column, a uint into a time / ENUM / SET / BIT column,
// a float into FLOAT, ...). Other pairs append a cell of the wrong width (an int64 into a
// FLOAT column), which is a misuse of the chunk (D01.7), not decoder behaviour.
func codecDecoderFits(flag byte, tp byte) bool {
	isInt := tp == mysql.TypeTiny || tp == mysql.TypeShort || tp == mysql.TypeInt24 || tp == mysql.TypeLong ||
		tp == mysql.TypeLonglong || tp == mysql.TypeYear
	switch flag {
	case 0:
		return true
	case 3, 8: // int, varint
		return isInt || tp == mysql.TypeDuration
	case 4, 9: // uint, uvarint
		return isInt || tp == mysql.TypeDate || tp == mysql.TypeDatetime || tp == mysql.TypeTimestamp ||
			tp == mysql.TypeEnum || tp == mysql.TypeSet || tp == mysql.TypeBit
	case 5:
		return tp == mysql.TypeFloat || tp == mysql.TypeDouble
	case 1, 2:
		return tp == mysql.TypeVarchar || tp == mysql.TypeBlob
	case 6:
		return tp == mysql.TypeNewDecimal
	case 7:
		return tp == mysql.TypeDuration
	case 10:
		return tp == mysql.TypeJSON
	case 20:
		return tp == mysql.TypeTiDBVectorFloat32
	}
	return false
}
