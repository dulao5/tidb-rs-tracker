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
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"sort"

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
	Govec   int    `json:"govec"`
	Domain  string `json:"domain"`
	TiDB    string `json:"tidb"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	Seed    uint64 `json:"seed"`
	Records int    `json:"records"`
}

// Err is the stable JSON form of an error returned by the Go code.
type Err struct {
	Name string `json:"name"`           // e.g. "ErrTruncated"; "panic" for a recovered panic; "other" if unknown
	Code int    `json:"code,omitempty"` // MySQL error code when the error is a *terror.Error
	Msg  string `json:"msg"`            // err.Error()
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
	Min     int      // minimum number of records the sanity test requires
	GoFuncs []string // Go functions exercised (for the coverage map)
}

// Domain is one area of TiDB code covered by vectors.
type Domain struct {
	Name     string
	Seed     uint64
	Errors   []NamedErr
	Coverage map[string]Coverage
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
			rec.Err = &Err{Name: "panic", Msg: fmt.Sprint(r)}
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
	e := &Err{Name: "other", Msg: err.Error()}
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
		recs = append(recs, d.Run(c.Op, c.Args, c.Src))
	}
	h := Header{
		Govec: FormatVersion, Domain: d.Name, TiDB: TiDBVersion, Commit: TiDBCommit,
		Go: runtime.Version(), Seed: d.Seed, Records: len(recs),
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
			rec = Record{Args: []json.RawMessage{}, Err: &Err{Name: "bad_request", Msg: err.Error()}}
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
