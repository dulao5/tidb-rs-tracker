// Package vec is the domain-independent part of govec: record format,
// domain registry, deterministic generation and the stdin/stdout oracle.
//
// A domain (decimal, json, time, ...) registers itself with Register from an
// init function in package domains. Everything a domain emits goes through
// Exec, both when generating the committed vector file and in oracle mode, so
// every record can be replayed from its (op, args) alone.
package vec

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/pingcap/errors"
)

// Format version of the record layout. Bump on incompatible change.
const FormatVersion = 1

// TiDB version the vectors are produced from. Must match go.mod.
const (
	TiDBVersion = "v8.5.8"
	TiDBCommit  = "8b857efa2"
)

// Header is the first line of every vector file.
type Header struct {
	Govec  int    `json:"govec"`
	Domain string `json:"domain"`
	TiDB   string `json:"tidb"`
	Commit string `json:"commit"`
	Go     string `json:"go"`
	// Platform the file was generated on. Records may depend on the target
	// (GOARCH, and GOAMD64 on amd64): gc fuses float x*y+z into FMA on some
	// targets and not others (README "Targets"), so there is one committed
	// file per target (Target). GOOS does not change code generation and is
	// the only field SameExceptGOOS ignores.
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	GOAMD64 string `json:"goamd64,omitempty"` // amd64 only
	Seed    uint64 `json:"seed"`
	Records int    `json:"records"`
}

// CommittedTargets are the targets whose vectors are committed, as Target
// names: amd64 at GOAMD64=v1 (Go's default, the baseline) and arm64. They are
// the platforms TiDB v8.5.8 is released for.
var CommittedTargets = []string{"amd64", "arm64"}

// Target names the code-generation target of this binary: GOARCH, with the
// GOAMD64 level appended on amd64 unless it is the baseline v1 ("amd64",
// "amd64.v3", "arm64"). It is the directory of the vector files.
func Target() string {
	if runtime.GOARCH == "amd64" && goamd64 != "v1" {
		return "amd64." + goamd64
	}
	return runtime.GOARCH
}

// GOAMD64 is the GOAMD64 level of this binary ("" off amd64).
func GOAMD64() string { return goamd64 }

// Err is the stable JSON form of an error returned by the Go code.
type Err struct {
	Name string   `json:"name"`           // e.g. "ErrTruncated"; "panic" for a recovered panic; "other" if unknown
	Code int      `json:"code,omitempty"` // MySQL error code when the error is a *terror.Error
	Msg  GoString `json:"msg"`            // err.Error() (bytes; see GoString)
}

// Record is one call: inputs, output value and error. Out is kept even when
// Err is non-nil (MyDecimal often returns a value together with ErrTruncated).
type Record struct {
	Op   string            `json:"op"`
	Args []json.RawMessage `json:"args"`
	Out  any               `json:"out"`
	Err  *Err              `json:"err"`
	// Warnings appended to the statement context (domains that use one,
	// e.g. convert); omitted when empty.
	Warnings []*Err `json:"warnings,omitempty"`
	Src      string `json:"src,omitempty"` // provenance: Go test function or generator group
}

// WithWarnings may be returned as out from Exec to also record warnings.
type WithWarnings struct {
	Out      any
	Warnings []error
}

// NamedErr maps a sentinel error to a stable name.
type NamedErr struct {
	Name string
	Err  error
}

// Coverage describes what an op covers and how many records are required.
type Coverage struct {
	Min int // minimum number of records the sanity test requires
	// Go functions exercised, in the tracker's symbol format (data/go_funcs.tsv,
	// data/func_blocks.tsv): "Func" or "Type.Method", no pointer receiver.
	GoFuncs []string
}

// Domain is one area of TiDB code covered by vectors.
type Domain struct {
	Name     string
	Seed     uint64
	Errors   []NamedErr
	Coverage map[string]Coverage
	// Blocks are the tracker porting blocks (data/func_blocks.tsv) the domain
	// covers. Every function of these blocks must appear in some op's GoFuncs
	// or in Exempt (checked by govec_test.go).
	Blocks []string
	// Files are Go files outside Blocks that tidb-rs ports whole (a D00.3 util
	// file, a leaf file of another block): every function of these files must
	// be covered or exempt too. GoFuncs and Exempt name them "<file>:<Symbol>"
	// (file as in data/func_blocks.tsv).
	Files []string
	// Exempt: Go function (tracker format) -> why no op covers it.
	Exempt map[string]string
	// Gzip: the committed file is <name>.jsonl.gz (see README, "Storage").
	Gzip bool
	// Generate appends calls to g. It must be deterministic given rng.
	Generate func(g *Gen, rng *rand.Rand)
	// Exec runs one op. args are the JSON-encoded arguments.
	Exec func(op string, args []json.RawMessage) (out any, err error)
}

var registry = map[string]*Domain{}

// Register adds a domain; call from init().
func Register(d *Domain) {
	if _, dup := registry[d.Name]; dup {
		panic("govec: duplicate domain " + d.Name)
	}
	registry[d.Name] = d
}

// Lookup returns a registered domain or nil.
func Lookup(name string) *Domain { return registry[name] }

// Names returns registered domain names, sorted.
func Names() []string {
	var ns []string
	for n := range registry {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}

// Call is one pending invocation produced by a generator.
type Call struct {
	Op   string
	Args []json.RawMessage
	Src  string
}

// Gen collects calls in order.
type Gen struct {
	Calls []Call
}

// Add appends a call. Args are JSON-encoded immediately, so Exec sees exactly
// what a replayer will see.
func (g *Gen) Add(src, op string, args ...any) {
	raw := make([]json.RawMessage, len(args))
	for i, a := range args {
		if err := CheckUTF8(a); err != nil {
			panic(fmt.Sprintf("%s %s arg %d: %v", src, op, i, err))
		}
		raw[i] = MustJSON(a)
	}
	g.Calls = append(g.Calls, Call{Op: op, Args: raw, Src: src})
}

// MustJSON encodes v without HTML escaping.
func MustJSON(v any) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// Run executes one call, converting errors and panics to Err.
func (d *Domain) Run(op string, args []json.RawMessage, src string) (rec Record) {
	rec = Record{Op: op, Args: args, Src: src}
	if rec.Args == nil {
		rec.Args = []json.RawMessage{}
	}
	defer func() {
		if r := recover(); r != nil {
			rec.Out = nil
			rec.Err = &Err{Name: "panic", Msg: GoString(fmt.Sprint(r))}
		}
	}()
	out, err := d.Exec(op, args)
	if ww, ok := out.(WithWarnings); ok {
		out = ww.Out
		for _, w := range ww.Warnings {
			rec.Warnings = append(rec.Warnings, d.errOf(w))
		}
	}
	rec.Out = out
	rec.Err = d.errOf(err)
	return rec
}

func (d *Domain) errOf(err error) *Err {
	if err == nil {
		return nil
	}
	e := &Err{Name: "other", Msg: GoString(err.Error())}
	cause := errors.Cause(err)
	te, isTerr := cause.(*errors.Error)
	// Identity first (sentinels returned as-is), then same class+code
	// (errors built with GenWithStackByArgs from a sentinel).
	for _, ne := range d.Errors {
		if cause == ne.Err {
			e.Name = ne.Name
			break
		}
	}
	if e.Name == "other" && isTerr {
		for _, ne := range d.Errors {
			if te.Equal(ne.Err) {
				e.Name = ne.Name
				break
			}
		}
	}
	if isTerr {
		e.Code = int(te.Code())
	}
	return e
}

// Records generates and runs all calls of the domain.
func (d *Domain) Records() (Header, []Record) {
	rng := rand.New(rand.NewPCG(d.Seed, d.Seed^0x9e3779b97f4a7c15))
	g := &Gen{}
	d.Generate(g, rng)
	recs := make([]Record, 0, len(g.Calls))
	for _, c := range g.Calls {
		rec := d.Run(c.Op, c.Args, c.Src)
		if err := CheckUTF8(rec); err != nil {
			panic(fmt.Sprintf("%s %s: %v", c.Src, c.Op, err))
		}
		recs = append(recs, rec)
	}
	h := Header{
		Govec: FormatVersion, Domain: d.Name, TiDB: TiDBVersion, Commit: TiDBCommit,
		Go: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GOAMD64: goamd64,
		Seed: d.Seed, Records: len(recs),
	}
	return h, recs
}

// WriteAll writes header + records as JSONL.
func (d *Domain) WriteAll(w io.Writer) error {
	h, recs := d.Records()
	bw := bufio.NewWriter(w)
	if err := writeLine(bw, h); err != nil {
		return err
	}
	for _, r := range recs {
		if err := writeLine(bw, r); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// FileName is the committed file name of the domain's vectors.
func (d *Domain) FileName() string {
	if d.Gzip {
		return d.Name + ".jsonl.gz"
	}
	return d.Name + ".jsonl"
}

// FilePath is the committed vector file of the domain for a target, relative
// to the vectors/<tidb version> directory: <target>/<FileName>.
func (d *Domain) FilePath(target string) string { return target + "/" + d.FileName() }

// WriteFile writes the vectors to w in the committed form: plain JSONL, or
// for Gzip domains a gzip stream with no name, no mtime and fixed level, so
// that the bytes depend only on the content and the (pinned) Go version.
func (d *Domain) WriteFile(w io.Writer) error {
	if !d.Gzip {
		return d.WriteAll(w)
	}
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	if err := d.WriteAll(zw); err != nil {
		return err
	}
	return zw.Close()
}

// ReadFile returns the JSONL content of a committed file, decompressing
// .gz files.
func ReadFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(path, ".gz") {
		return b, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

var goosRe = regexp.MustCompile(`"goos":"[^"]*",`)

// SameExceptGOOS reports whether two JSONL vector contents are identical
// apart from the goos field of the header. goarch/goamd64 are compared: a
// file only checks against a regeneration on the same target.
func SameExceptGOOS(a, b []byte) bool {
	norm := func(x []byte) []byte {
		i := bytes.IndexByte(x, '\n') + 1 // header line incl. newline (0: whole input)
		if i == 0 {
			i = len(x)
		}
		return append(goosRe.ReplaceAll(x[:i], nil), x[i:]...)
	}
	return bytes.Equal(norm(a), norm(b))
}

func writeLine(w *bufio.Writer, v any) error {
	if _, err := w.Write(MustJSON(v)); err != nil {
		return err
	}
	return w.WriteByte('\n')
}

// Request is one line of oracle input.
type Request struct {
	Op   string            `json:"op"`
	Args []json.RawMessage `json:"args"`
}

// Oracle reads one Request per line from r and writes one Record per line to
// w. Blank lines are skipped; malformed lines yield a record with op "" and
// err name "bad_request".
func (d *Domain) Oracle(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	bw := bufio.NewWriter(w)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req Request
		var rec Record
		if err := json.Unmarshal(line, &req); err != nil {
			rec = Record{Args: []json.RawMessage{}, Err: &Err{Name: "bad_request", Msg: GoString(err.Error())}}
		} else {
			rec = d.Run(req.Op, req.Args, "")
		}
		if err := writeLine(bw, rec); err != nil {
			return err
		}
		// Flush per line so an interactive driver can pipeline.
		if err := bw.Flush(); err != nil {
			return err
		}
	}
	return sc.Err()
}
