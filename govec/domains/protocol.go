package domains

// Domain "protocol": the MySQL protocol encoding of the new stack's results and the binary
// parameters of COM_STMT_EXECUTE (tidb-rs `crates/server`, block D03.1 part 1; tidb-rs
// specs/D03.1-server/spec.md). The server's internal packages are reached through the
// export shim govec/fixes/pkg/server/govec_export.go.fixed (no change of behaviour).
//
// ops (byte strings are hex):
//   column  [info, results_charset, with_default]  -> Info.Dump / DumpWithDefault bytes
//   convert [field]                                -> ConvertColumnInfo of a ResultField
//   row     [binary, results_charset, infos, values] -> DumpBinaryRow / DumpTextRow bytes
//   float   [f64 bits, prec, bit_size]             -> AppendFormatFloat
//   params  [n, bound, null_bitmap, types, values, client_charset, flags]
//           -> parseBinaryParams, then expression.ExecBinaryParam on its result
// A value of `row` is null or one of {"i": int64}, {"f32": bits}, {"f64": bits},
// {"dec": s}, {"b": hex}, {"t": [y,mo,d,h,mi,s,us], "tp", "fsp"}, {"d": nanoseconds},
// {"enum"/"set": [name, value]}, {"json": text}, {"vec": [f32 bits]}.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/meta/model"
	pmodel "github.com/pingcap/tidb/pkg/parser/model"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/planner/core/resolve"
	"github.com/pingcap/tidb/pkg/server"
	"github.com/pingcap/tidb/pkg/types"
	"github.com/pingcap/tidb/pkg/util/chunk"
	contextutil "github.com/pingcap/tidb/pkg/util/context"
)

const protocolSeed = 20261116

func init() {
	vec.Register(&vec.Domain{
		Name:     "protocol",
		Seed:     protocolSeed,
		Coverage: protocolCoverage,
		Blocks:   []string{"D08.12"},
		Files:    protocolFiles,
		Exempt:   protocolExempt,
		Gzip:     true,
		Generate: protocolGenerate,
		Exec:     protocolExec,
	})
}

var protocolFiles = []string{
	"pkg/server/internal/dump/dump.go",
	"pkg/server/internal/column/column.go",
	"pkg/server/internal/column/convert.go",
	"pkg/server/internal/column/result_encoder.go",
	"pkg/server/internal/util/util.go",
	"pkg/server/conn_stmt_params.go",
}

const (
	pDump = "pkg/server/internal/dump/dump.go:"
	pCol  = "pkg/server/internal/column/column.go:"
	pEnc  = "pkg/server/internal/column/result_encoder.go:"
	pUtil = "pkg/server/internal/util/util.go:"
)

var protocolCoverage = map[string]vec.Coverage{
	"column": {Min: 300, GoFuncs: []string{pCol + "Info.Dump", pCol + "Info.DumpWithDefault", pCol + "Info.dump",
		pCol + "Info.dumpCharset", pCol + "Info.dumpLength", pCol + "DumpFlag", pCol + "dumpType",
		pEnc + "NewResultEncoder", pEnc + "ResultEncoder.ColumnTypeInfoCharsetID", pEnc + "ResultEncoder.EncodeMeta",
		pEnc + "ResultEncoder.EncodeWith", pEnc + "isStringColumnType", pDump + "LengthEncodedString",
		pDump + "LengthEncodedInt", pDump + "Uint16", pDump + "Uint32"}},
	"convert": {Min: 200, GoFuncs: []string{"pkg/server/internal/column/convert.go:ConvertColumnInfo"}},
	"row": {Min: 1200, GoFuncs: []string{pCol + "DumpTextRow", pCol + "DumpBinaryRow",
		pEnc + "ResultEncoder.UpdateDataEncoding", pEnc + "ResultEncoder.EncodeData", pDump + "BinaryTime",
		pDump + "BinaryDateTime", pDump + "Uint64", pUtil + "AppendFormatFloat"}},
	"float": {Min: 300, GoFuncs: []string{}},
	"params": {Min: 1000, GoFuncs: []string{"pkg/server/conn_stmt_params.go:parseBinaryParams",
		pUtil + "ParseLengthEncodedInt", pUtil + "NewInputDecoder", pUtil + "InputDecoder.DecodeInput",
		"ExecBinaryParam", "binaryDate", "binaryDateTime", "binaryTimestamp", "binaryTimestampWithTZ",
		"binaryDuration", "binaryDurationWithMS"}},
}

var protocolExempt = func() map[string]string {
	unit := "ported (tidb-rs crates/server/src/util.rs, unit tests): length-encoded parsing helpers the " +
		"result encoding and binary parameters do not call"
	http := "permanent: the HTTP status server's CORS handler and Go's test configuration; tidb-rs has its own " +
		"status server (D14.1 spec)"
	m := map[string]string{
		pUtil + "ParseNullTermString":     unit,
		pUtil + "ParseLengthEncodedBytes": unit,
		pUtil + "LengthEncodedIntSize":    unit,
		pUtil + "NewCorsHandler":          http,
		pUtil + "CorsHandler.ServeHTTP":   http,
		pUtil + "NewTestConfig":           http,
		pEnc + "ResultEncoder.Clean": "ported (no-op: the Rust encoder keeps no scratch buffer); nothing to " +
			"record",
	}
	return m
}()

// ---------------------------------------------------------------- generation

var protocolCharsets = []string{"utf8mb4", "", "binary", "latin1", "gbk", "utf8", "ascii"}

// Collation ids of the column charsets: utf8mb4_bin, binary, latin1_bin, gbk_chinese_ci,
// utf8_general_ci, utf8mb4_general_ci, ascii_bin, an unknown id.
var protocolCollations = []uint16{46, 63, 47, 28, 33, 45, 65, 0}

var protocolTypes = []byte{
	mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeYear,
	mysql.TypeFloat, mysql.TypeDouble, mysql.TypeNewDecimal, mysql.TypeString, mysql.TypeVarString,
	mysql.TypeVarchar, mysql.TypeBit, mysql.TypeTinyBlob, mysql.TypeMediumBlob, mysql.TypeLongBlob,
	mysql.TypeBlob, mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp, mysql.TypeDuration,
	mysql.TypeEnum, mysql.TypeSet, mysql.TypeJSON, mysql.TypeTiDBVectorFloat32, mysql.TypeGeometry,
}

var protocolStrings = []string{"", "a", "abc", "中文", "héllo", "\xff\xfe", "😀x", "a\x00b", "Ünïcödé",
	"long string with spaces and some more text to pass 250 bytes " + string(make([]byte, 250))}

func protocolInfo(rng *rand.Rand, tp byte) map[string]any {
	names := []string{"", "c", "名字", "été", "a_long_" + fmt.Sprint(rng.IntN(1000))}
	if rng.IntN(20) == 0 {
		names = append(names, string(make([]byte, 300)))
	}
	pick := func() string { return names[rng.IntN(len(names))] }
	flags := []uint16{0, uint16(mysql.UnsignedFlag), uint16(mysql.BinaryFlag),
		uint16(mysql.NotNullFlag | mysql.PriKeyFlag), uint16(mysql.UnsignedFlag | mysql.BinaryFlag),
		uint16(rng.IntN(1 << 16))}
	decimals := []uint8{0, 1, 2, 3, 6, 31, 4}
	tables := []string{"", "t"}
	var def any
	switch rng.IntN(4) {
	case 0:
		def = "CURRENT_TIMESTAMP"
	case 1:
		def = fmt.Sprint(rng.IntN(100))
	case 2:
		def = "x"
	}
	return map[string]any{
		"default": def, "schema": pick(), "table": tables[rng.IntN(2)], "org_table": pick(), "name": pick(),
		"org_name": pick(), "column_length": uint32(rng.IntN(1 << 20)),
		"charset": protocolCollations[rng.IntN(len(protocolCollations))], "flag": flags[rng.IntN(len(flags))],
		"decimal": decimals[rng.IntN(len(decimals))], "type": tp,
	}
}

var protocolFloats = []float64{0, 1, -1, 1.5, -2.25, 0.1, 123456.789, 1e15, 1e-15, 1e14, 9.99e14, 1e16, 1e-16,
	1.23456789e20, -3.4e38, 3.4e38, 1e300, 5e-324, 1e-7, 100, 12345678901234567890, math.Inf(1), math.NaN(),
	0.000123, 1.0 / 3}

func protocolValue(rng *rand.Rand, tp byte, flag uint16) any {
	if rng.IntN(7) == 0 {
		return nil
	}
	ints := []int64{0, 1, -1, 127, -128, 255, 32767, -32768, 65535, 8388607, -8388608, 2147483647, -2147483648,
		4294967295, math.MaxInt64, math.MinInt64, -2, rng.Int64(), int64(rng.IntN(3000))}
	switch tp {
	case mysql.TypeTiny, mysql.TypeShort, mysql.TypeInt24, mysql.TypeLong, mysql.TypeLonglong, mysql.TypeYear:
		v := ints[rng.IntN(len(ints))]
		if tp == mysql.TypeYear && rng.IntN(2) == 0 {
			v = int64(1901 + rng.IntN(255))
		}
		return map[string]any{"i": strconv.FormatInt(v, 10)}
	case mysql.TypeFloat:
		f := float32(protocolFloats[rng.IntN(len(protocolFloats))])
		if rng.IntN(4) == 0 {
			f = math.Float32frombits(rng.Uint32())
		}
		return map[string]any{"f32": math.Float32bits(f)}
	case mysql.TypeDouble:
		f := protocolFloats[rng.IntN(len(protocolFloats))]
		if rng.IntN(4) == 0 {
			f = math.Float64frombits(rng.Uint64())
		}
		return map[string]any{"f64": strconv.FormatUint(math.Float64bits(f), 10)}
	case mysql.TypeNewDecimal:
		decs := []string{"0", "-0.00", "123.4500", "-99999999999999999999999999999.123456789", "1e5", "0.000001",
			fmt.Sprintf("%d.%d", rng.Int64N(1e12), rng.IntN(1e6))}
		return map[string]any{"dec": decs[rng.IntN(len(decs))]}
	case mysql.TypeDate, mysql.TypeDatetime, mysql.TypeTimestamp:
		fsp := rng.IntN(7)
		if tp == mysql.TypeDate {
			fsp = 0
		}
		t := []int{rng.IntN(10000), rng.IntN(13), rng.IntN(32), rng.IntN(24), rng.IntN(60), rng.IntN(60),
			rng.IntN(1000000)}
		switch rng.IntN(5) {
		case 0:
			t = []int{0, 0, 0, 0, 0, 0, 0}
		case 1:
			t[3], t[4], t[5], t[6] = 0, 0, 0, 0
		case 2:
			t[6] = 0
		}
		return map[string]any{"t": t, "tp": tp, "fsp": fsp}
	case mysql.TypeDuration:
		ns := []int64{0, 1000, -1000, 3600e9, -3600e9, 838*3600e9 + 59*60e9 + 59e9 + 999999000,
			rng.Int64N(838*3600e9) * []int64{1, -1}[rng.IntN(2)], rng.Int64N(86400e9) / 1000 * 1000, 1, 86400e9 * 30}
		return map[string]any{"d": strconv.FormatInt(ns[rng.IntN(len(ns))], 10)}
	case mysql.TypeEnum:
		return map[string]any{"enum": []any{vec.GoString(protocolStrings[rng.IntN(len(protocolStrings))]), rng.IntN(10)}}
	case mysql.TypeSet:
		return map[string]any{"set": []any{[]string{"", "a", "a,b", "中,文"}[rng.IntN(4)], rng.IntN(16)}}
	case mysql.TypeJSON:
		js := []string{`{"a": 1, "b": [true, null, 1.5]}`, `"中文"`, `[]`, `123`, `-1.25e10`, `{"k": "é"}`,
			`[1, "x", {"y": false}]`, `null`}
		return map[string]any{"json": js[rng.IntN(len(js))]}
	case mysql.TypeTiDBVectorFloat32:
		n := rng.IntN(5)
		v := make([]uint32, n)
		for i := range v {
			f := float32(protocolFloats[rng.IntN(len(protocolFloats))])
			if math.IsInf(float64(f), 0) || math.IsNaN(float64(f)) {
				f = 0.5 // CreateVectorFloat32 rejects them
			}
			v[i] = math.Float32bits(f)
		}
		return map[string]any{"vec": v}
	default:
		s := protocolStrings[rng.IntN(len(protocolStrings))]
		if rng.IntN(5) == 0 {
			b := make([]byte, rng.IntN(20))
			for i := range b {
				b[i] = byte(rng.IntN(256))
			}
			s = string(b)
		}
		return map[string]any{"b": hex.EncodeToString([]byte(s))}
	}
}

func protocolField(rng *rand.Rand) map[string]any {
	tp := protocolTypes[rng.IntN(len(protocolTypes)-1)]
	flens := []int{-1, 0, 1, 10, 255, 65535, 1 << 24, 1<<32 - 1, 20}
	decs := []int{-1, 0, 2, 6, 7, 30}
	chs := []string{"utf8mb4", "binary", "latin1", "gbk", "utf8", "ascii", "unknown"}
	names := []string{"", "c", "名字"}
	return map[string]any{
		"name": names[rng.IntN(3)], "as_name": names[rng.IntN(3)], "table": []string{"", "t"}[rng.IntN(2)],
		"table_as": names[rng.IntN(3)], "db": names[rng.IntN(3)], "empty_org_name": rng.IntN(2) == 0,
		"tp": tp, "flag": uint32(rng.IntN(1 << 16)), "flen": flens[rng.IntN(len(flens))],
		"decimal": decs[rng.IntN(len(decs))], "charset": chs[rng.IntN(len(chs))],
	}
}

// protocolParamTypes: every type parseBinaryParams knows, plus unknown ones.
var protocolParamTypes = []byte{mysql.TypeNull, mysql.TypeTiny, mysql.TypeShort, mysql.TypeYear, mysql.TypeInt24,
	mysql.TypeLong, mysql.TypeFloat, mysql.TypeLonglong, mysql.TypeDouble, mysql.TypeDate, mysql.TypeTimestamp,
	mysql.TypeDatetime, mysql.TypeDuration, mysql.TypeNewDecimal, mysql.TypeBlob, mysql.TypeTinyBlob,
	mysql.TypeMediumBlob, mysql.TypeLongBlob, mysql.TypeUnspecified, mysql.TypeVarchar, mysql.TypeVarString,
	mysql.TypeString, mysql.TypeEnum, mysql.TypeSet, mysql.TypeGeometry, mysql.TypeBit, mysql.TypeJSON, 14, 17}

func protocolLenenc(b []byte, n uint64) []byte {
	switch {
	case n <= 250:
		return append(b, byte(n))
	case n <= 0xffff:
		return append(b, 0xfc, byte(n), byte(n>>8))
	case n <= 0xffffff:
		return append(b, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	}
	return append(b, 0xfe, byte(n), byte(n>>8), byte(n>>16), byte(n>>24), byte(n>>32), byte(n>>40),
		byte(n>>48), byte(n>>56))
}

// protocolParamValue appends a well-formed value of type tp (sometimes a broken one).
func protocolParamValue(rng *rand.Rand, tp byte, vals []byte) []byte {
	rnd := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.IntN(256))
		}
		return b
	}
	switch tp {
	case mysql.TypeNull:
		return vals
	case mysql.TypeTiny:
		return append(vals, rnd(1)...)
	case mysql.TypeShort, mysql.TypeYear:
		return append(vals, rnd(2)...)
	case mysql.TypeInt24, mysql.TypeLong, mysql.TypeFloat:
		return append(vals, rnd(4)...)
	case mysql.TypeLonglong, mysql.TypeDouble:
		b := rnd(8)
		if rng.IntN(2) == 0 {
			v := math.Float64bits(protocolFloats[rng.IntN(len(protocolFloats))])
			for i := range b {
				b[i] = byte(v >> (8 * i))
			}
		}
		return append(vals, b...)
	case mysql.TypeDate, mysql.TypeTimestamp, mysql.TypeDatetime:
		n := []int{0, 4, 7, 11, 13, 5}[rng.IntN(6)]
		b := []byte{byte(rng.IntN(256)), byte(rng.IntN(40)), byte(rng.IntN(14)), byte(rng.IntN(33)),
			byte(rng.IntN(25)), byte(rng.IntN(61)), byte(rng.IntN(61))}
		b = append(b, rnd(4)...)
		if rng.IntN(2) == 0 {
			b[7], b[8], b[9], b[10] = byte(rng.IntN(256)), byte(rng.IntN(16)), 0, 0
		}
		tz := []int16{0, 480, -300, 59, -61, -720, 765}[rng.IntN(7)]
		b = append(b, byte(tz), byte(uint16(tz)>>8))
		return append(append(vals, byte(n)), b[:n]...)
	case mysql.TypeDuration:
		n := []int{0, 8, 12, 9}[rng.IntN(4)]
		b := []byte{byte(rng.IntN(3)), byte(rng.IntN(40)), 0, 0, 0, byte(rng.IntN(25)), byte(rng.IntN(61)),
			byte(rng.IntN(61))}
		if rng.IntN(5) == 0 {
			b[0] = 0
		}
		b = append(b, byte(rng.IntN(256)), byte(rng.IntN(16)), 0, 0, 0)
		return append(append(vals, byte(n)), b[:n]...)
	case mysql.TypeNewDecimal:
		decs := []string{"1.5", "-0", "abc", "123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890", "1e3", "", "12.3x", " 7 "}
		s := decs[rng.IntN(len(decs))]
		if rng.IntN(10) == 0 {
			return append(vals, 0xfb)
		}
		return append(protocolLenenc(vals, uint64(len(s))), s...)
	default:
		s := []byte(protocolStrings[rng.IntN(len(protocolStrings))])
		if rng.IntN(4) == 0 {
			s = rnd(rng.IntN(300))
		}
		if rng.IntN(15) == 0 {
			return append(vals, 0xfb)
		}
		return append(protocolLenenc(vals, uint64(len(s))), s...)
	}
}

func protocolGenerate(g *vec.Gen, rng *rand.Rand) {
	// column: every type with each results charset.
	for _, tp := range protocolTypes {
		for _, chs := range protocolCharsets {
			g.Add("gen/column/types", "column", protocolInfo(rng, tp), chs, rng.IntN(3) == 0)
		}
	}
	for i := 0; i < 200; i++ {
		g.Add("gen/column/random", "column", protocolInfo(rng, protocolTypes[rng.IntN(len(protocolTypes))]),
			protocolCharsets[rng.IntN(len(protocolCharsets))], rng.IntN(2) == 0)
	}
	for i := 0; i < 250; i++ {
		g.Add("gen/convert", "convert", protocolField(rng))
	}
	// row: each type alone with every results charset, then random rows.
	for _, tp := range protocolTypes {
		for _, chs := range protocolCharsets {
			for k := 0; k < 4; k++ {
				info := protocolInfo(rng, tp)
				val := protocolValue(rng, tp, info["flag"].(uint16))
				g.Add("gen/row/types", "row", k%2 == 0, chs, []any{info}, []any{val})
			}
		}
	}
	for i := 0; i < 600; i++ {
		n := 1 + rng.IntN(12)
		infos, vals := make([]any, n), make([]any, n)
		for j := range infos {
			tp := protocolTypes[rng.IntN(len(protocolTypes)-1)]
			info := protocolInfo(rng, tp)
			infos[j] = info
			vals[j] = protocolValue(rng, tp, info["flag"].(uint16))
		}
		g.Add("gen/row/random", "row", rng.IntN(2) == 0, protocolCharsets[rng.IntN(len(protocolCharsets))], infos,
			vals)
	}
	// float: AppendFormatFloat at the precisions the dump uses.
	for i := 0; i < 400; i++ {
		f := protocolFloats[rng.IntN(len(protocolFloats))]
		if rng.IntN(3) == 0 {
			f = math.Float64frombits(rng.Uint64())
		}
		bits := []int{32, 64}[rng.IntN(2)]
		if bits == 32 {
			f = float64(float32(f))
		}
		g.Add("gen/float", "float", strconv.FormatUint(math.Float64bits(f), 10), []int{-1, 0, 1, 2, 5, 6, 30}[rng.IntN(7)], bits)
	}
	// params.
	flags := []uint64{uint64(types.DefaultStmtFlags), uint64(types.StrictFlags),
		uint64(types.FlagTruncateAsWarning | types.FlagIgnoreZeroInDateErr | types.FlagIgnoreInvalidDateErr)}
	for _, tp := range protocolParamTypes {
		for k := 0; k < 30; k++ {
			unsigned := byte(0)
			if rng.IntN(2) == 0 {
				unsigned = 0x80
			}
			vals := protocolParamValue(rng, tp, nil)
			g.Add("gen/params/types", "params", 1, []any{nil}, "00", hex.EncodeToString([]byte{tp, unsigned}),
				hex.EncodeToString(vals), protocolCharsets[rng.IntN(len(protocolCharsets))], flags[rng.IntN(len(flags))])
		}
	}
	for i := 0; i < 600; i++ {
		n := 1 + rng.IntN(10)
		bound := make([]any, n)
		nullBitmap := make([]byte, (n+7)/8)
		var tps, vals []byte
		for j := 0; j < n; j++ {
			tp := protocolParamTypes[rng.IntN(len(protocolParamTypes)-2)]
			tps = append(tps, tp, []byte{0, 0x80}[rng.IntN(2)])
			switch rng.IntN(8) {
			case 0:
				nullBitmap[j/8] |= 1 << (j % 8)
			case 1:
				bound[j] = hex.EncodeToString([]byte(protocolStrings[rng.IntN(len(protocolStrings))]))
				if rng.IntN(2) == 0 {
					nullBitmap[j/8] |= 1 << (j % 8)
				}
			default:
				vals = protocolParamValue(rng, tp, vals)
			}
		}
		switch rng.IntN(12) {
		case 0:
			tps = tps[:len(tps)-1] // missing the last flags byte
		case 1:
			if len(vals) > 0 {
				vals = vals[:rng.IntN(len(vals))] // truncated values
			}
		}
		g.Add("gen/params/random", "params", n, bound, hex.EncodeToString(nullBitmap), hex.EncodeToString(tps),
			hex.EncodeToString(vals), protocolCharsets[rng.IntN(len(protocolCharsets))], flags[rng.IntN(len(flags))])
	}
}

// ---------------------------------------------------------------- execution

type protocolInfoArg struct {
	Default      *string `json:"default"`
	Schema       string  `json:"schema"`
	Table        string  `json:"table"`
	OrgTable     string  `json:"org_table"`
	Name         string  `json:"name"`
	OrgName      string  `json:"org_name"`
	ColumnLength uint32  `json:"column_length"`
	Charset      uint16  `json:"charset"`
	Flag         uint16  `json:"flag"`
	Decimal      uint8   `json:"decimal"`
	Type         uint8   `json:"type"`
}

func (a protocolInfoArg) info() server.GovecColumnInfo {
	var def any
	if a.Default != nil {
		def = *a.Default
	}
	return server.GovecColumnInfo{DefaultValue: def, Schema: a.Schema, Table: a.Table, OrgTable: a.OrgTable,
		Name: a.Name, OrgName: a.OrgName, ColumnLength: a.ColumnLength, Charset: a.Charset, Flag: a.Flag,
		Decimal: a.Decimal, Type: a.Type}
}

func protocolErrOut(err error) map[string]any {
	code := 0
	if te, ok := errors.Cause(err).(*errors.Error); ok {
		code = int(te.Code())
	}
	return map[string]any{"err": map[string]any{"code": code, "msg": vec.GoString(err.Error())}}
}

func protocolUnmarshal(raw json.RawMessage, v any) error { return json.Unmarshal(raw, v) }

type protocolValueArg struct {
	I    *string           `json:"i"`
	F32  *uint32           `json:"f32"`
	F64  *string           `json:"f64"`
	Dec  *string           `json:"dec"`
	B    *string           `json:"b"`
	T    []int             `json:"t"`
	Tp   byte              `json:"tp"`
	Fsp  int               `json:"fsp"`
	D    *string           `json:"d"`
	Enum []json.RawMessage `json:"enum"`
	Set  []json.RawMessage `json:"set"`
	JSON *string           `json:"json"`
	Vec  []uint32          `json:"vec"`
}

func protocolAppend(chk *chunk.Chunk, i int, v *protocolValueArg) error {
	switch {
	case v == nil:
		chk.AppendNull(i)
	case v.I != nil:
		n, err := strconv.ParseInt(*v.I, 10, 64)
		if err != nil {
			return err
		}
		chk.AppendInt64(i, n)
	case v.F32 != nil:
		chk.AppendFloat32(i, math.Float32frombits(*v.F32))
	case v.F64 != nil:
		n, err := strconv.ParseUint(*v.F64, 10, 64)
		if err != nil {
			return err
		}
		chk.AppendFloat64(i, math.Float64frombits(n))
	case v.Dec != nil:
		var d types.MyDecimal
		if err := d.FromString([]byte(*v.Dec)); err != nil && err != types.ErrTruncated {
			return err
		}
		chk.AppendMyDecimal(i, &d)
	case v.B != nil:
		b, err := hex.DecodeString(*v.B)
		if err != nil {
			return err
		}
		chk.AppendBytes(i, b)
	case v.T != nil:
		t := v.T
		chk.AppendTime(i, types.NewTime(types.FromDate(t[0], t[1], t[2], t[3], t[4], t[5], t[6]), v.Tp, v.Fsp))
	case v.D != nil:
		n, err := strconv.ParseInt(*v.D, 10, 64)
		if err != nil {
			return err
		}
		chk.AppendDuration(i, types.Duration{Duration: time.Duration(n)})
	case v.Enum != nil || v.Set != nil:
		pair := v.Enum
		if pair == nil {
			pair = v.Set
		}
		var name vec.GoString
		var value uint64
		if err := json.Unmarshal(pair[0], &name); err != nil {
			return err
		}
		if err := json.Unmarshal(pair[1], &value); err != nil {
			return err
		}
		if v.Enum != nil {
			chk.AppendEnum(i, types.Enum{Name: string(name), Value: value})
		} else {
			chk.AppendSet(i, types.Set{Name: string(name), Value: value})
		}
	case v.JSON != nil:
		j, err := types.ParseBinaryJSONFromString(*v.JSON)
		if err != nil {
			return err
		}
		chk.AppendJSON(i, j)
	case v.Vec != nil:
		fs := make([]float32, len(v.Vec))
		for k, b := range v.Vec {
			fs[k] = math.Float32frombits(b)
		}
		vf, err := types.CreateVectorFloat32(fs)
		if err != nil {
			return err
		}
		chk.AppendVectorFloat32(i, vf)
	default:
		return errors.New("empty value")
	}
	return nil
}

// protocolDatum is the comparable form of a datum: its kind and value.
func protocolDatum(d types.Datum) map[string]any {
	out := map[string]any{"k": d.Kind()}
	switch d.Kind() {
	case types.KindInt64:
		out["v"] = strconv.FormatInt(d.GetInt64(), 10)
	case types.KindUint64:
		out["v"] = strconv.FormatUint(d.GetUint64(), 10)
	case types.KindFloat32:
		out["v"] = strconv.FormatUint(uint64(math.Float32bits(float32(d.GetFloat64()))), 10)
	case types.KindFloat64:
		out["v"] = strconv.FormatUint(math.Float64bits(d.GetFloat64()), 10)
	case types.KindString, types.KindBytes:
		out["v"] = hex.EncodeToString(d.GetBytes())
		out["nil"] = d.GetBytes() == nil
	case types.KindMysqlDecimal:
		if d.GetMysqlDecimal() != nil {
			out["v"] = d.GetMysqlDecimal().String()
		}
	case types.KindMysqlTime:
		t := d.GetMysqlTime()
		out["v"] = t.String()
		out["tp"] = t.Type()
		out["fsp"] = t.Fsp()
	case types.KindMysqlDuration:
		dur := d.GetMysqlDuration()
		out["v"] = strconv.FormatInt(int64(dur.Duration), 10)
		out["fsp"] = dur.Fsp
	}
	return out
}

func protocolExec(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "column":
		var info protocolInfoArg
		var chs string
		var withDefault bool
		if err := protocolFirstErr(protocolUnmarshal(args[0], &info), protocolUnmarshal(args[1], &chs),
			protocolUnmarshal(args[2], &withDefault)); err != nil {
			return nil, err
		}
		return hex.EncodeToString(server.GovecDumpColumn(info.info(), chs, withDefault)), nil
	case "convert":
		var f struct {
			Name         string `json:"name"`
			AsName       string `json:"as_name"`
			Table        string `json:"table"`
			TableAs      string `json:"table_as"`
			DB           string `json:"db"`
			EmptyOrgName bool   `json:"empty_org_name"`
			Tp           byte   `json:"tp"`
			Flag         uint   `json:"flag"`
			Flen         int    `json:"flen"`
			Decimal      int    `json:"decimal"`
			Charset      string `json:"charset"`
		}
		if err := protocolUnmarshal(args[0], &f); err != nil {
			return nil, err
		}
		ft := types.NewFieldType(f.Tp)
		ft.SetFlag(f.Flag)
		ft.SetFlen(f.Flen)
		ft.SetDecimal(f.Decimal)
		ft.SetCharset(f.Charset)
		fld := &resolve.ResultField{
			Column:       &model.ColumnInfo{Name: pmodel.NewCIStr(f.Name), FieldType: *ft},
			ColumnAsName: pmodel.NewCIStr(f.AsName),
			EmptyOrgName: f.EmptyOrgName,
			TableAsName:  pmodel.NewCIStr(f.TableAs),
			DBName:       pmodel.NewCIStr(f.DB),
		}
		if f.Table != "" {
			fld.Table = &model.TableInfo{Name: pmodel.NewCIStr(f.Table)}
		}
		ci := server.GovecConvertColumnInfo(fld)
		return map[string]any{"schema": ci.Schema, "table": ci.Table, "org_table": ci.OrgTable, "name": ci.Name,
			"org_name": ci.OrgName, "column_length": ci.ColumnLength, "charset": ci.Charset, "flag": ci.Flag,
			"decimal": ci.Decimal, "type": ci.Type}, nil
	case "row":
		var binary bool
		var chs string
		var infos []protocolInfoArg
		var vals []*protocolValueArg
		if err := protocolFirstErr(protocolUnmarshal(args[0], &binary), protocolUnmarshal(args[1], &chs),
			protocolUnmarshal(args[2], &infos), protocolUnmarshal(args[3], &vals)); err != nil {
			return nil, err
		}
		fts := make([]*types.FieldType, len(infos))
		cols := make([]server.GovecColumnInfo, len(infos))
		for i, a := range infos {
			fts[i] = types.NewFieldType(a.Type)
			fts[i].SetFlag(uint(a.Flag))
			cols[i] = a.info()
		}
		chk := chunk.NewChunkWithCapacity(fts, 1)
		for i, v := range vals {
			if err := protocolAppend(chk, i, v); err != nil {
				return nil, err
			}
		}
		b, err := server.GovecDumpRow(cols, chk.GetRow(0), chs, binary)
		if err != nil {
			return protocolErrOut(err), nil
		}
		return hex.EncodeToString(b), nil
	case "float":
		var bits string
		var prec, size int
		if err := protocolFirstErr(protocolUnmarshal(args[0], &bits), protocolUnmarshal(args[1], &prec),
			protocolUnmarshal(args[2], &size)); err != nil {
			return nil, err
		}
		n, err := strconv.ParseUint(bits, 10, 64)
		if err != nil {
			return nil, err
		}
		return string(server.GovecAppendFormatFloat(math.Float64frombits(n), prec, size)), nil
	case "params":
		var n int
		var bound []*string
		var nullBitmap, tps, vals, chs string
		var flags uint64
		if err := protocolFirstErr(protocolUnmarshal(args[0], &n), protocolUnmarshal(args[1], &bound),
			protocolUnmarshal(args[2], &nullBitmap), protocolUnmarshal(args[3], &tps),
			protocolUnmarshal(args[4], &vals), protocolUnmarshal(args[5], &chs),
			protocolUnmarshal(args[6], &flags)); err != nil {
			return nil, err
		}
		dec := func(s string) []byte { b, _ := hex.DecodeString(s); return b }
		boundParams := make([][]byte, n)
		for i, b := range bound {
			if b != nil {
				boundParams[i] = dec(*b)
				if boundParams[i] == nil {
					boundParams[i] = []byte{}
				}
			}
		}
		params, err := server.GovecParseBinaryParams(n, boundParams, dec(nullBitmap), dec(tps), dec(vals), chs)
		if err != nil {
			return map[string]any{"parse": protocolErrOut(err)}, nil
		}
		ps := make([]any, len(params))
		for i, p := range params {
			ps[i] = map[string]any{"tp": p.Tp, "unsigned": p.IsUnsigned, "null": p.IsNull,
				"val": hex.EncodeToString(p.Val)}
		}
		out := map[string]any{"parse": ps}
		tc := types.NewContext(types.Flags(flags), time.UTC, contextutil.IgnoreWarn)
		exprs, err := expression.ExecBinaryParam(tc, params)
		if err != nil {
			out["exec"] = protocolErrOut(err)
			return out, nil
		}
		cs := make([]any, len(exprs))
		for i, e := range exprs {
			c := e.(*expression.Constant)
			ft := c.RetType
			cs[i] = map[string]any{"datum": protocolDatum(c.Value), "tp": ft.GetType(), "flag": ft.GetFlag(),
				"flen": ft.GetFlen(), "decimal": ft.GetDecimal(), "charset": ft.GetCharset(),
				"collate": ft.GetCollate()}
		}
		out["exec"] = cs
		return out, nil
	}
	return nil, errors.Errorf("unknown op %s", op)
}

func protocolFirstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
