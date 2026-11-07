package domains

// Domain "collate": pkg/util/collate (block D01.9), with the files it relies on
// that tidb-rs ports whole: pkg/util/stringutil/string_util.go (LIKE patterns)
// and pkg/parser/charset/charset.go (the collation registry), plus the GBK
// encoder of encoding_gbk.go (customGBK over x/text's simplifiedchinese.GBK).
//
// Values:
//   - strings are vec.GoString (hex for invalid UTF-8); sort keys and encoded
//     bytes are hex;
//   - most ops take `enabled` first (0/1) and call
//     collate.SetNewCollationEnabledForTest(enabled == 1) before the call, so
//     every record is independent of the previous ones (the setting also switches
//     gbk's default collation in parser/charset);
//   - a collator is reported by its Go type (`%T`), e.g. "*collate.binPaddingCollator";
//   - runes (compiled pattern weights) are integers;
//   - key_digest: SHA-256 over every rune r of [lo, hi) of
//     (len(Key) as 2 bytes LE, Key, len(KeyWithoutTrimRightSpace), KeyWithoutTrimRightSpace,
//     Compare(string(r), string(r-1)) as 1 byte) of string(r) (U+FFFD for the
//     surrogates, like Go's string(rune)); a call that panics contributes 0xFF
//     instead. These records check the generated weight tables over all code points.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dulao5/tidb-rs-tracker/govec/vec"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/util/collate"
	"github.com/pingcap/tidb/pkg/util/stringutil"
)

const collateSeed = 20261109

//go:embed testdata/collate_literals.tsv
var collateLiteralsTSV string

func init() {
	vec.Register(&vec.Domain{
		Name:     "collate",
		Seed:     collateSeed,
		Errors:   collateErrors,
		Coverage: collateCoverage,
		Blocks:   []string{"D01.9"},
		Files:    []string{"pkg/util/stringutil/string_util.go", "pkg/parser/charset/charset.go"},
		Exempt: map[string]string{
			"generateFile": "ucaimpl: code generator of unicode_*_generated.go (tidb-rs: a Rust generic); its output is covered by the unicode_ci and 0900_ai_ci ops",
			"main":         "ucaimpl: code generator of unicode_*_generated.go (tidb-rs: a Rust generic); its output is covered by the unicode_ci and 0900_ai_ci ops",
			"pkg/parser/charset/charset.go:AddCharset":               "mutates the registry (custom charsets in tests only); not called by TiDB",
			"pkg/parser/charset/charset.go:RemoveCharset":            "mutates the registry (custom charsets in tests only); not called by TiDB",
			"pkg/parser/charset/charset.go:AddSupportedCollation":    "mutates the registry; called by init through AddCollation (covered)",
			"pkg/util/stringutil/string_util.go:MemoizeStr":          "takes a Go closure; tidb-rs unit test",
			"pkg/util/stringutil/string_util.go:StringerFunc.String": "takes a Go closure; tidb-rs unit test",
		},
		Gzip:     true,
		Generate: collateGenerate,
		Exec:     collateExec,
	})
}

var collateErrors = []vec.NamedErr{
	{Name: "ErrUnsupportedCollation", Err: collate.ErrUnsupportedCollation},
	{Name: "ErrUnknownCollation", Err: charset.ErrUnknownCollation},
	{Name: "ErrInvalidCharacterString", Err: charset.ErrInvalidCharacterString},
	{Name: "ErrSyntax", Err: stringutil.ErrSyntax},
}

const su = "pkg/util/stringutil/string_util.go:"
const cs = "pkg/parser/charset/charset.go:"

var collateCoverage = map[string]vec.Coverage{
	"compare": {Min: 8000, GoFuncs: []string{"binCollator.Compare", "binPaddingCollator.Compare",
		"generalCICollator.Compare", "unicodeCICollator.Compare", "unicode0900AICICollator.Compare",
		"gbkBinCollator.Compare", "gbkChineseCICollator.Compare", "compareCommon", "truncateTailingSpace",
		"sign", "runeLen", "convertRuneGeneralCI", "gbkChineseCISortKey", "unicode0400Impl.GetWeight",
		"unicode0400Impl.Preprocess", "unicode0900Impl.GetWeight", "unicode0900Impl.Preprocess",
		"convertRuneUnicodeCI0900", "GetCollator", "zhPinyinTiDBASCSCollator.Compare"}},
	"key": {Min: 5000, GoFuncs: []string{"binCollator.Key", "binCollator.KeyWithoutTrimRightSpace",
		"binPaddingCollator.Key", "binPaddingCollator.KeyWithoutTrimRightSpace", "generalCICollator.Key",
		"generalCICollator.KeyWithoutTrimRightSpace", "unicodeCICollator.Key",
		"unicodeCICollator.KeyWithoutTrimRightSpace", "unicode0900AICICollator.Key",
		"unicode0900AICICollator.KeyWithoutTrimRightSpace", "gbkBinCollator.Key",
		"gbkBinCollator.KeyWithoutTrimRightSpace", "gbkChineseCICollator.Key",
		"gbkChineseCICollator.KeyWithoutTrimRightSpace", "zhPinyinTiDBASCSCollator.Key",
		"zhPinyinTiDBASCSCollator.KeyWithoutTrimRightSpace"}},
	"key_digest": {Min: 2000},
	"pattern": {Min: 4000, GoFuncs: []string{"binCollator.Pattern", "derivedBinCollator.Pattern",
		"binPaddingCollator.Pattern", "generalCICollator.Pattern", "unicodeCICollator.Pattern",
		"unicode0900AICICollator.Pattern", "gbkBinCollator.Pattern", "gbkChineseCICollator.Pattern",
		"unicode0400Impl.Pattern", "unicode0900Impl.Pattern", "zhPinyinTiDBASCSCollator.Pattern",
		"binPattern.Compile", "binPattern.DoMatch", "derivedBinPattern.Compile", "derivedBinPattern.DoMatch",
		"ciPattern.Compile", "ciPattern.DoMatch", "gbkChineseCIPattern.Compile", "gbkChineseCIPattern.DoMatch",
		"unicodePattern.Compile", "unicodePattern.DoMatch", "unicode0900AICIPattern.Compile",
		"unicode0900AICIPattern.DoMatch",
		su + "CompilePatternBinary", su + "CompilePattern", su + "CompilePatternInner",
		su + "CompilePatternInnerBinary", su + "matchRune", su + "DoMatchBinary", su + "DoMatch",
		su + "DoMatchCustomized", su + "doMatchInner"}},
	"collator": {Min: 300, GoFuncs: []string{"GetCollator", "GetCollatorByID", "GetBinaryCollator",
		"GetBinaryCollatorSlice", "ConvertAndGetBinCollator", "CanUseRawMemAsKey", "binCollator.Clone",
		"binPaddingCollator.Clone", "generalCICollator.Clone", "unicodeCICollator.Clone",
		"unicode0900AICICollator.Clone", "unicode0400Impl.Clone", "unicode0900Impl.Clone",
		"gbkBinCollator.Clone", "gbkChineseCICollator.Clone", "zhPinyinTiDBASCSCollator.Clone", "init",
		"SetNewCollationEnabledForTest", "switchDefaultCollation", "NewCollationEnabled"}},
	"collation_id": {Min: 400, GoFuncs: []string{"RewriteNewCollationIDIfNeeded",
		"RestoreCollationIDIfNeeded", "CollationID2Name", "ProtoToCollation", cs + "GetCollationByID"}},
	"collation_name": {Min: 350, GoFuncs: []string{"CollationName2ID", "CollationToProto",
		"GetCollationByName", "SubstituteMissingCollationToDefault", "IsDefaultCollationForUTF8MB4",
		"IsCICollation", "ConvertAndGetBinCollation", "IsBinCollation", "IsPadSpaceCollation",
		cs + "GetCollationByName", cs + "utf8Alias"}},
	"compatible": {Min: 200, GoFuncs: []string{"CompatibleCollate"}},
	"supported":  {Min: 2, GoFuncs: []string{"GetSupportedCollations", cs + "GetSupportedCollations"}},
	"charset": {Min: 400, GoFuncs: []string{cs + "GetCharsetInfo", cs + "GetDefaultCollation",
		cs + "GetDefaultCollationLegacy", cs + "ValidCharsetAndCollation", cs + "init", cs + "AddCollation"}},
	"charset_by_id":  {Min: 100, GoFuncs: []string{cs + "GetCharsetInfoByID"}},
	"charsets":       {Min: 2, GoFuncs: []string{cs + "GetSupportedCharsets", cs + "GetDefaultCharsetAndCollate"}},
	"gbk_encode":     {Min: 1000},
	"unquote":        {Min: 300, GoFuncs: []string{su + "Unquote", su + "UnquoteChar"}},
	"unquote_char":   {Min: 300, GoFuncs: []string{su + "UnquoteChar"}},
	"like2regexp":    {Min: 100, GoFuncs: []string{su + "CompileLike2Regexp"}},
	"is_exact_match": {Min: 100, GoFuncs: []string{su + "IsExactMatch"}},
	"string_misc": {Min: 300, GoFuncs: []string{su + "Copy", su + "Escape", su + "GetTailSpaceCount",
		su + "EscapeGlobQuestionMark", su + "LowerOneString", su + "StringerStr.String"}},
	"lower_exclude": {Min: 300, GoFuncs: []string{su + "LowerOneStringExcludeEscapeChar",
		su + "toLowerIfAlphaASCII", su + "toUpperIfAlphaASCII", su + "IsUpperASCII", su + "IsLowerASCII",
		su + "Utf8Len"}},
	"byte_props": {Min: 256, GoFuncs: []string{su + "Utf8Len", su + "IsUpperASCII", su + "IsLowerASCII", su + "IsNumericASCII"}},
	"trim_utf8":  {Min: 100, GoFuncs: []string{su + "TrimUtf8String", su + "ConvertPosInUtf8"}},
	"labels":     {Min: 20, GoFuncs: []string{su + "BuildStringFromLabels"}},
}

// collateNames: the collations the ops run on (all of newCollatorMap, plus
// names the registry does not have).
var collateNames = []string{"binary", "ascii_bin", "latin1_bin", "utf8mb4_bin", "utf8_bin",
	"utf8mb4_0900_bin", "utf8mb4_general_ci", "utf8_general_ci", "utf8mb4_unicode_ci", "utf8_unicode_ci",
	"utf8mb4_0900_ai_ci", "gbk_bin", "gbk_chinese_ci"}

var collateOtherNames = []string{"", "utf8mb4_zh_pinyin_tidb_as_cs", "latin1_swedish_ci", "UTF8MB4_BIN",
	"utf8mb3_bin", "utf8mb3_general_ci", "utf8mb3_unicode_ci", "Utf8mb4_General_CI", "utf8mb4_ja_0900_as_cs",
	"big5_chinese_ci", "nonexistent", "gb18030_bin", "gb18030_chinese_ci", "utf8mb4_0900_as_cs",
	"ascii_general_ci", "binary ", "utf8_bin\x00"}

// randEnabled: new collations enabled (1) four times in five.
func randEnabled(rng *rand.Rand) int {
	if rng.IntN(5) == 0 {
		return 0
	}
	return 1
}

func setEnabled(args []json.RawMessage) {
	collate.SetNewCollationEnabledForTest(arg[int](args, 0) == 1)
}

func goStr(r rune) string { return string(r) }

func typeName(c collate.Collator) string { return fmt.Sprintf("%T", c) }

func collationOut(c *charset.Collation) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{"id": c.ID, "charset": c.CharsetName, "name": c.Name, "default": c.IsDefault,
		"sortlen": c.Sortlen, "pad": c.PadAttribute}
}

func charsetOut(c *charset.Charset) map[string]any {
	if c == nil {
		return nil
	}
	var names []string
	for n, co := range c.Collations {
		if co.IsDefault {
			n += "*"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return map[string]any{"name": c.Name, "default": c.DefaultCollation, "desc": c.Desc, "maxlen": c.Maxlen,
		"collations": names}
}

// keyDigest: see the file comment.
func keyDigest(name string, lo, hi int) string {
	c := collate.GetCollator(name)
	h := sha256.New()
	try := func(f func() []byte) {
		var out []byte
		ok := func() (ok bool) {
			defer func() {
				if recover() != nil {
					ok = false
				}
			}()
			out = f()
			return true
		}()
		if !ok {
			h.Write([]byte{0xFF})
			return
		}
		var l [2]byte
		binary.LittleEndian.PutUint16(l[:], uint16(len(out)))
		h.Write(l[:])
		h.Write(out)
	}
	for r := lo; r < hi; r++ {
		s := goStr(rune(r))
		try(func() []byte { return c.Key(s) })
		try(func() []byte { return c.KeyWithoutTrimRightSpace(s) })
		try(func() []byte { return []byte{byte(int8(c.Compare(s, goStr(rune(r-1)))))} })
	}
	return hex.EncodeToString(h.Sum(nil))
}

func collateExec(op string, args []json.RawMessage) (out any, err error) {
	s := func(i int) string { return string(arg[vec.GoString](args, i)) }
	switch op {
	case "compare":
		setEnabled(args)
		return collate.GetCollator(arg[string](args, 1)).Compare(s(2), s(3)), nil
	case "key":
		setEnabled(args)
		c := collate.GetCollator(arg[string](args, 1))
		k := c.Key(s(2))
		kw := c.KeyWithoutTrimRightSpace(s(2))
		return map[string]any{"key": hex.EncodeToString(k), "key_wo": hex.EncodeToString(kw)}, nil
	case "key_digest":
		collate.SetNewCollationEnabledForTest(true)
		return keyDigest(arg[string](args, 0), arg[int](args, 1), arg[int](args, 2)), nil
	case "pattern":
		setEnabled(args)
		p := collate.GetCollator(arg[string](args, 1)).Pattern()
		p.Compile(s(2), byte(arg[int](args, 3)))
		strs := arg[[]vec.GoString](args, 4)
		res := make([]bool, len(strs))
		for i, x := range strs {
			res[i] = p.DoMatch(string(x))
		}
		w, t := stringutil.CompilePattern(s(2), byte(arg[int](args, 3)))
		wb, tb := stringutil.CompilePatternBinary(s(2), byte(arg[int](args, 3)))
		ws := make([]int, len(w))
		for i, r := range w {
			ws[i] = int(r)
		}
		return map[string]any{"match": res, "weights": ws, "types": hex.EncodeToString(t),
			"bin_weights": hex.EncodeToString(wb), "bin_types": hex.EncodeToString(tb)}, nil
	case "collator":
		setEnabled(args)
		var c collate.Collator
		switch arg[string](args, 1) {
		case "name":
			c = collate.GetCollator(arg[string](args, 2))
		case "id":
			c = collate.GetCollatorByID(arg[int](args, 2))
		case "bin":
			c = collate.ConvertAndGetBinCollator(arg[string](args, 2))
		case "binary":
			c = collate.GetBinaryCollator()
		case "slice":
			sl := collate.GetBinaryCollatorSlice(arg[int](args, 2))
			var ts []string
			for _, x := range sl {
				ts = append(ts, typeName(x))
			}
			return map[string]any{"len": len(sl), "types": ts}, nil
		}
		o := map[string]any{"type": typeName(c), "raw": collate.CanUseRawMemAsKey(c),
			"enabled": collate.NewCollationEnabled()}
		cl := c.Clone()
		o["clone"] = typeName(cl)
		o["clone_raw"] = collate.CanUseRawMemAsKey(cl)
		return o, nil
	case "collation_id":
		setEnabled(args)
		id := arg[int32](args, 1)
		return map[string]any{"rewrite": collate.RewriteNewCollationIDIfNeeded(id),
			"restore": collate.RestoreCollationIDIfNeeded(id), "id2name": collate.CollationID2Name(id),
			"proto2coll": collate.ProtoToCollation(id)}, nil
	case "collation_name":
		setEnabled(args)
		n := arg[string](args, 1)
		o := map[string]any{"name2id": collate.CollationName2ID(n), "to_proto": collate.CollationToProto(n),
			"default_utf8mb4": collate.IsDefaultCollationForUTF8MB4(n), "ci": collate.IsCICollation(n),
			"to_bin": collate.ConvertAndGetBinCollation(n), "bin": collate.IsBinCollation(n),
			"pad_space": collate.IsPadSpaceCollation(n)}
		co, err := collate.GetCollationByName(n)
		o["by_name"] = collationOut(co)
		if err != nil {
			o["by_name_err"] = vec.GoString(err.Error())
		}
		pco, err := charset.GetCollationByName(n)
		o["charset_by_name"] = collationOut(pco)
		if err != nil {
			o["charset_by_name_err"] = vec.GoString(err.Error())
		}
		o["substitute"] = func() (r any) {
			defer func() {
				if recover() != nil {
					r = "panic"
				}
			}()
			return collate.SubstituteMissingCollationToDefault(n)
		}()
		return o, nil
	case "compatible":
		return collate.CompatibleCollate(arg[string](args, 0), arg[string](args, 1)), nil
	case "supported":
		setEnabled(args)
		var ns []map[string]any
		for _, c := range collate.GetSupportedCollations() {
			ns = append(ns, collationOut(c))
		}
		var ps []map[string]any
		for _, c := range charset.GetSupportedCollations() {
			ps = append(ps, collationOut(c))
		}
		return map[string]any{"collate": ns, "charset": ps}, nil
	case "charset":
		setEnabled(args)
		n := arg[string](args, 1)
		o := map[string]any{}
		c, err := charset.GetCharsetInfo(n)
		o["info"] = charsetOut(c)
		if err != nil {
			o["info_err"] = vec.GoString(err.Error())
		}
		d, err := charset.GetDefaultCollation(n)
		o["default"] = d
		if err != nil {
			o["default_err"] = vec.GoString(err.Error())
		}
		dl, err := charset.GetDefaultCollationLegacy(n)
		o["legacy"] = dl
		if err != nil {
			o["legacy_err"] = vec.GoString(err.Error())
		}
		var valid []bool
		for _, co := range arg[[]string](args, 2) {
			valid = append(valid, charset.ValidCharsetAndCollation(n, co))
		}
		o["valid"] = valid
		return o, nil
	case "charset_by_id":
		setEnabled(args)
		cs, co, err := charset.GetCharsetInfoByID(arg[int](args, 1))
		bid, err2 := charset.GetCollationByID(arg[int](args, 1))
		o := map[string]any{"charset": cs, "collation": co, "by_id": collationOut(bid)}
		if err2 != nil {
			o["by_id_err"] = vec.GoString(err2.Error())
		}
		return o, err
	case "charsets":
		setEnabled(args)
		var css []map[string]any
		for _, c := range charset.GetSupportedCharsets() {
			css = append(css, charsetOut(c))
		}
		dc, dco := charset.GetDefaultCharsetAndCollate()
		return map[string]any{"charsets": css, "default": []string{dc, dco}}, nil
	case "gbk_encode":
		b, err := charset.NewCustomGBKEncoder().Bytes([]byte(s(0)))
		return hex.EncodeToString(b), err
	case "unquote":
		t, err := stringutil.Unquote(s(0))
		return vec.GoString(t), err
	case "unquote_char":
		v, tail, err := stringutil.UnquoteChar(s(0), byte(arg[int](args, 1)))
		return map[string]any{"value": hex.EncodeToString(v), "tail": vec.GoString(tail)}, err
	case "like2regexp":
		return vec.GoString(stringutil.CompileLike2Regexp(s(0))), nil
	case "is_exact_match":
		_, t := stringutil.CompilePattern(s(0), byte(arg[int](args, 1)))
		return stringutil.IsExactMatch(t), nil
	case "string_misc":
		x := s(0)
		low := []byte(x)
		stringutil.LowerOneString(low)
		return map[string]any{"copy": vec.GoString(stringutil.Copy(x)),
			"escape":      vec.GoString(stringutil.Escape(x, mysql.SQLMode(0))),
			"escape_ansi": vec.GoString(stringutil.Escape(x, mysql.ModeANSIQuotes)),
			"tail_spaces": stringutil.GetTailSpaceCount(x),
			"glob":        vec.GoString(stringutil.EscapeGlobQuestionMark(x)),
			"lower":       vec.GoString(low), "stringer": vec.GoString(stringutil.StringerStr(x).String())}, nil
	case "lower_exclude":
		b := []byte(s(0))
		e := stringutil.LowerOneStringExcludeEscapeChar(b, byte(arg[int](args, 1)))
		return map[string]any{"str": vec.GoString(b), "escape": int(e)}, nil
	case "byte_props":
		c := byte(arg[int](args, 0))
		return []any{stringutil.Utf8Len(c), stringutil.IsUpperASCII(c), stringutil.IsLowerASCII(c),
			stringutil.IsNumericASCII(c)}, nil
	case "trim_utf8":
		x := s(0)
		n := stringutil.TrimUtf8String(&x, arg[int64](args, 1))
		y := s(0)
		return map[string]any{"trimmed": n, "rest": vec.GoString(x),
			"pos": stringutil.ConvertPosInUtf8(&y, arg[int64](args, 2))}, nil
	case "labels":
		return stringutil.BuildStringFromLabels(arg[map[string]string](args, 0)), nil
	}
	return nil, fmt.Errorf("unknown op %q", op)
}

// ---------------------------------------------------------------- cases

type collateLit struct {
	fn  string
	str *string
	num *int64
}

func collateLiterals() []collateLit {
	var lits []collateLit
	sc := bufio.NewScanner(bytes.NewReader([]byte(collateLiteralsTSV)))
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
			lits = append(lits, collateLit{fn: c[0], str: &v})
		case "i":
			v, err := strconv.ParseInt(c[2], 10, 64)
			if err != nil {
				panic(err)
			}
			lits = append(lits, collateLit{fn: c[0], num: &v})
		}
	}
	return lits
}

// CollateMinedTests: the Go test functions whose literals are in
// testdata/collate_literals.tsv (each must appear as a record src).
func CollateMinedTests() []string {
	seen := map[string]bool{}
	var fns []string
	for _, l := range collateLiterals() {
		if !seen[l.fn] {
			seen[l.fn] = true
			fns = append(fns, l.fn)
		}
	}
	return fns
}

// collatePieces: the alphabet of random strings: ASCII (both cases), spaces,
// Latin-1 and Latin Extended (accents, ß, ligatures), case-fold specials (Kelvin K U+212A,
// Angstrom U+212B, long s, capital sharp s U+1E9E), Greek, Cyrillic,
// combining marks, CJK (in and out of GBK), Hangul, symbols (€ rejected by
// customGBK), U+FFFD (valid), supplementary planes (emoji, CJK Ext B, U+2CEA1
// which indexes past the 0900 table), controls (zero weights in UCA), and
// invalid UTF-8 (lone continuation, overlong, truncated, surrogate, > U+10FFFF).
var collatePieces = []string{
	"a", "A", "b", "B", "z", "Z", "0", "9", " ", " ", "  ", "_", "%", "\\", "!", "'", "\"", "-", ".", "?",
	"é", "É", "è", "ê", "ë", "à", "Á", "ä", "Ä", "ö", "ü", "ß", "ẞ", "æ", "Æ", "ø", "Ø", "ñ", "Ñ", "ç", "Ç", "ı", "İ",
	"ﬀ", "ﬁ", "\u212a", "k", "K", "\u212b", "å", "Å", "\u1e9e", "ǅ", "Ǆ", "ǆ", "Œ", "œ", "ŉ", "ſ", "µ", "Ω", "ω", "Σ", "σ", "ς", "Ά", "ά", "Я", "я", "Ё", "ё",
	"\u0301", "\u0308", "\u20dd", "中", "文", "國", "鿿", "龥", "啊", "丂", "々", "가", "힣", "ア", "ｱ", "Ａ", "ａ",
	"€", "¥", "©", "™", "←", "─", "①", "Ⅻ", "ⅻ", "\ufffd", "\ufeff", "\u200b", "\u0000", "\u0001", "\t", "\n",
	"😀", "𠀀", "𪚥", "\U0002CEA0", "\U0002CEA1", "\U0002CEA2", "\U0010FFFF", "\U000E0001",
	"\x80", "\xbf", "\xc0\x80", "\xc3", "\xe4\xb8", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xff", "\xfe", "\xf0\x9f\x98",
}

func randCollateStr(rng *rand.Rand, max int) string {
	n := rng.IntN(max + 1)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(collatePieces[rng.IntN(len(collatePieces))])
	}
	if rng.IntN(5) == 0 {
		b.WriteString(strings.Repeat(" ", 1+rng.IntN(3)))
	}
	return b.String()
}

// perturb: a string close to s (case flip, accent swap, a piece added or
// removed, trailing space), so that comparisons are often equal or decided late.
func perturb(rng *rand.Rand, s string) string {
	switch rng.IntN(6) {
	case 0:
		return strings.ToUpper(s)
	case 1:
		return strings.ToLower(s)
	case 2:
		return s + " "
	case 3:
		if len(s) > 0 {
			i := rng.IntN(len(s))
			return s[:i] + s[i+1:]
		}
	case 4:
		return s + collatePieces[rng.IntN(len(collatePieces))]
	}
	return s
}

func randPattern(rng *rand.Rand, s string, esc byte) string {
	var b strings.Builder
	for _, r := range s {
		switch rng.IntN(8) {
		case 0:
			b.WriteByte('_')
		case 1:
			b.WriteByte('%')
		case 2:
			if esc != 0 {
				b.WriteByte(esc)
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if rng.IntN(4) == 0 {
		b.WriteByte('%')
	}
	return b.String()
}

var collateEscapes = []int{'\\', '\\', '\\', '!', '_', '%', 'a', 'A', 0, 0x80, 0xff, '|'}

func collateGenerate(g *vec.Gen, rng *rand.Rand) {
	all := append(append([]string{}, collateNames...), "utf8mb4_zh_pinyin_tidb_as_cs")

	// Weight tables over all code points.
	for _, name := range []string{"binary", "utf8mb4_bin", "utf8mb4_0900_bin", "utf8mb4_general_ci",
		"utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci", "gbk_bin", "gbk_chinese_ci"} {
		for lo := 0; lo < 0x110000; lo += 0x1000 {
			g.Add("sys/digest", "key_digest", name, lo, lo+0x1000)
		}
	}

	// Literals of the Go tests.
	var strs []string
	for _, l := range collateLiterals() {
		src := l.fn
		if l.str != nil {
			x := vec.GoString(*l.str)
			strs = append(strs, *l.str)
			for _, en := range []int{1, 0} {
				for _, c := range collateNames {
					g.Add(src, "key", en, c, x)
				}
			}
			// Collation and charset names are UTF-8 in tidb-rs (&str: identifiers of
			// the SQL text); non-UTF-8 literals only go to the string ops.
			if utf8.ValidString(*l.str) {
				g.Add(src, "collation_name", 1, *l.str)
				g.Add(src, "charset", 1, *l.str, collateNames)
			}
			g.Add(src, "gbk_encode", x)
			g.Add(src, "unquote", x)
			if len(*l.str) > 0 {
				g.Add(src, "unquote_char", x, '\'')
			}
			g.Add(src, "like2regexp", x)
			g.Add(src, "is_exact_match", x, '\\')
			g.Add(src, "string_misc", x)
			g.Add(src, "lower_exclude", x, '\\')
		} else {
			g.Add(src, "collation_id", 1, int32(*l.num))
			g.Add(src, "charset_by_id", 1, *l.num)
			g.Add(src, "collator", 1, "id", *l.num)
		}
	}
	// Literal pairs of the same test function, and literals as patterns.
	byFn := map[string][]string{}
	var fnOrder []string
	for _, l := range collateLiterals() {
		if l.str == nil {
			continue
		}
		if _, ok := byFn[l.fn]; !ok {
			fnOrder = append(fnOrder, l.fn)
		}
		byFn[l.fn] = append(byFn[l.fn], *l.str)
	}
	for _, fn := range fnOrder {
		ss := byFn[fn]
		for i := 0; i+1 < len(ss); i++ {
			for _, c := range collateNames {
				g.Add(fn, "compare", 1, c, vec.GoString(ss[i]), vec.GoString(ss[i+1]))
			}
		}
		for i, p := range ss {
			targets := []vec.GoString{}
			for j := 0; j < len(ss) && j < 8; j++ {
				targets = append(targets, vec.GoString(ss[(i+j)%len(ss)]))
			}
			for _, c := range collateNames {
				g.Add(fn, "pattern", 1, c, vec.GoString(p), '\\', targets)
			}
		}
	}

	// Collators, IDs and names.
	for _, en := range []int{1, 0} {
		for _, n := range append(append([]string{}, all...), collateOtherNames...) {
			g.Add("sys", "collator", en, "name", n)
			g.Add("sys", "collator", en, "bin", n)
			g.Add("sys", "collation_name", en, n)
			g.Add("sys", "charset", en, n, append(append([]string{}, collateNames...), collateOtherNames...))
		}
		g.Add("sys", "collator", en, "binary", 0)
		for _, n := range []int{0, 1, 2, 5} {
			g.Add("sys", "collator", en, "slice", n)
		}
		for _, id := range []int{0, 1, 8, 28, 33, 45, 46, 47, 63, 65, 83, 87, 192, 224, 255, 309, 2048, 2049,
			-46, -1, -2048, 100000, -2147483648, 2147483647} {
			g.Add("sys", "collator", en, "id", id)
			g.Add("sys", "collation_id", en, int32(id))
			g.Add("sys", "charset_by_id", en, id)
		}
		for _, cs := range []string{"utf8", "UTF8", "utf8mb3", "utf8mb4", "ascii", "latin1", "binary", "gbk",
			"GBK", "big5", "gb18030", "ucs2", "", "nope", "utf16"} {
			g.Add("sys", "charset", en, cs, append(append([]string{}, collateNames...), collateOtherNames...))
		}
		g.Add("sys", "supported", en)
		g.Add("sys", "charsets", en)
	}
	for i := 0; i < 300; i++ {
		g.Add("rand", "collation_id", rng.IntN(2), int32(rng.IntN(400)-50))
		g.Add("rand", "charset_by_id", rng.IntN(2), rng.IntN(400)-50)
	}
	names := append(append([]string{}, all...), collateOtherNames...)
	for i := 0; i < 200; i++ {
		g.Add("rand", "compatible", names[rng.IntN(len(names))], names[rng.IntN(len(names))])
	}
	for _, a := range names {
		g.Add("sys", "compatible", a, a)
	}

	// Random strings: compare, key, pattern.
	for i := 0; i < 9000; i++ {
		a := randCollateStr(rng, 6)
		b := perturb(rng, a)
		if rng.IntN(3) == 0 {
			b = randCollateStr(rng, 6)
		}
		c := collateNames[i%len(collateNames)]
		g.Add("rand", "compare", randEnabled(rng), c, vec.GoString(a), vec.GoString(b))
	}
	for i := 0; i < 6000; i++ {
		c := collateNames[i%len(collateNames)]
		g.Add("rand", "key", randEnabled(rng), c, vec.GoString(randCollateStr(rng, 8)))
	}
	for i := 0; i < 5000; i++ {
		c := collateNames[i%len(collateNames)]
		esc := collateEscapes[rng.IntN(len(collateEscapes))]
		base := randCollateStr(rng, 5)
		p := randPattern(rng, base, byte(esc))
		var targets []vec.GoString
		for j := 0; j < 6; j++ {
			switch rng.IntN(3) {
			case 0:
				targets = append(targets, vec.GoString(perturb(rng, base)))
			case 1:
				targets = append(targets, vec.GoString(base))
			default:
				targets = append(targets, vec.GoString(randCollateStr(rng, 5)))
			}
		}
		g.Add("rand", "pattern", randEnabled(rng), c, vec.GoString(p), esc, targets)
	}
	// The pinyin collator panics on every method.
	g.Add("sys", "compare", 1, "utf8mb4_zh_pinyin_tidb_as_cs", "a", "b")
	g.Add("sys", "key", 1, "utf8mb4_zh_pinyin_tidb_as_cs", "a")
	g.Add("sys", "pattern", 1, "utf8mb4_zh_pinyin_tidb_as_cs", "a", '\\', []vec.GoString{"a"})
	// U+2CEA1 indexes past the 0900 table (Go panics).
	g.Add("sys", "key", 1, "utf8mb4_0900_ai_ci", "\U0002CEA1")
	g.Add("sys", "compare", 1, "utf8mb4_0900_ai_ci", "\U0002CEA1", "a")

	// GBK encoder: every piece, random strings.
	for _, p := range collatePieces {
		g.Add("sys", "gbk_encode", vec.GoString(p))
	}
	for i := 0; i < 1000; i++ {
		g.Add("rand", "gbk_encode", vec.GoString(randCollateStr(rng, 4)))
	}

	// stringutil.
	quoted := []string{`''`, `""`, "``", `'a'`, `"a"`, `'\'`, `'\''`, `'\\'`, `"\""`, `'\n\t\r\b\Z\0\_\%\x'`,
		`'a''b'`, `"a""b"`, `'a"b'`, `"a'b"`, `'`, `"`, `a`, `'abc`, `'中文'`, "'\xff'", "'\xe4\xb8'", "'\ufffd'",
		"'\\\xff'", `'\'` + "\xc3\xa9'"}
	for _, q := range quoted {
		g.Add("sys", "unquote", vec.GoString(q))
	}
	for i := 0; i < 300; i++ {
		q := []string{"'", "\"", "`"}[rng.IntN(3)]
		body := randCollateStr(rng, 4)
		if rng.IntN(2) == 0 {
			body = strings.ReplaceAll(body, " ", `\`)
		}
		g.Add("rand", "unquote", vec.GoString(q+body+q))
		g.Add("rand", "unquote_char", vec.GoString(`\`+body+"x"), []int{'\'', '"', 0}[rng.IntN(3)])
		g.Add("rand", "string_misc", vec.GoString(randCollateStr(rng, 6)+[]string{"", "`", `"`, "?"}[rng.IntN(4)]))
		g.Add("rand", "lower_exclude", vec.GoString(randCollateStr(rng, 6)), collateEscapes[rng.IntN(len(collateEscapes))])
	}
	for i := 0; i < 100; i++ {
		p := randPattern(rng, randCollateStr(rng, 4), '\\')
		g.Add("rand", "like2regexp", vec.GoString(p))
		g.Add("rand", "is_exact_match", vec.GoString(p), collateEscapes[rng.IntN(len(collateEscapes))])
	}
	for b := 0; b < 256; b++ {
		g.Add("sys", "byte_props", b)
	}
	for i := 0; i < 150; i++ {
		var sb strings.Builder
		for j := rng.IntN(6); j >= 0; j-- {
			p := collatePieces[rng.IntN(len(collatePieces))]
			if utf8.ValidString(p) {
				sb.WriteString(p)
			}
		}
		x := sb.String()
		n := utf8.RuneCountInString(x)
		g.Add("rand", "trim_utf8", vec.GoString(x), rng.IntN(n+1), rng.IntN(len(x)+1))
	}
	for i := 0; i < 20; i++ {
		m := map[string]string{}
		for j := rng.IntN(5); j > 0; j-- {
			m[[]string{"zone", "host", "dc", "rack", "a", "Z", ""}[rng.IntN(7)]] = fmt.Sprint(rng.IntN(100))
		}
		g.Add("rand", "labels", m)
	}
	_ = strs
}
