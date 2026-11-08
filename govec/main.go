// Command govec produces input/output test vectors by running Go TiDB code.
//
//	govec <domain> [-o file]       write the vector file for a domain (JSONL, or gzip for Gzip domains)
//	govec <domain> -seed N [-o f]  same with another seed (robustness runs; never committed)
//	govec <domain> --filename      print the committed file of this target (<target>/<domain>.jsonl[.gz])
//	govec <domain> --check file    regenerate and compare with a committed file (content, header goos excluded)
//	govec <domain> --oracle        read {"op","args"} lines on stdin, write records on stdout
//	govec list                     list domains
//	govec target                   print this binary's target (amd64, amd64.v3, arm64, ...)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/dulao5/tidb-rs-tracker/govec/domains"
	"github.com/dulao5/tidb-rs-tracker/govec/vec"
)

func main() {
	domains.Quiet()
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		usage()
	}
	name := os.Args[1]
	if name == "target" {
		fmt.Println(vec.Target())
		return
	}
	if name == "list" {
		for _, n := range vec.Names() {
			fmt.Println(n)
		}
		return
	}
	d := vec.Lookup(name)
	if d == nil {
		fmt.Fprintf(os.Stderr, "govec: unknown domain %q (have %v)\n", name, vec.Names())
		os.Exit(2)
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	out := fs.String("o", "-", "output file (- for stdout)")
	oracle := fs.Bool("oracle", false, "oracle mode: ops on stdin, records on stdout")
	check := fs.String("check", "", "compare a fresh run with this committed file")
	filename := fs.Bool("filename", false, "print the committed file name")
	seed := fs.Uint64("seed", 0, "override the domain's seed (robustness runs; never commit the output)")
	_ = fs.Parse(os.Args[2:])
	if *seed != 0 {
		d.Seed = *seed
	}

	if *filename {
		fmt.Println(d.FilePath(vec.Target()))
		return
	}
	if *check != "" {
		os.Exit(checkFile(d, *check))
	}

	if *oracle {
		if err := d.Oracle(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "govec:", err)
			os.Exit(1)
		}
		return
	}
	var w io.Writer = os.Stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "govec:", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if err := d.WriteFile(w); err != nil {
		fmt.Fprintln(os.Stderr, "govec:", err)
		os.Exit(1)
	}
}

// checkFile regenerates d and compares it with the committed file. The
// comparison is on the decompressed JSONL with only the header's goos
// excluded: a file checks against a regeneration on the same target
// (GOARCH/GOAMD64), on any OS.
func checkFile(d *vec.Domain, path string) int {
	committed, err := vec.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "govec:", err)
		return 1
	}
	var fresh bytes.Buffer
	if err := d.WriteAll(&fresh); err != nil {
		fmt.Fprintln(os.Stderr, "govec:", err)
		return 1
	}
	head := func(b []byte) string { h, _, _ := bytes.Cut(b, []byte("\n")); return string(h) }
	if !vec.SameExceptGOOS(committed, fresh.Bytes()) {
		fmt.Fprintf(os.Stderr, "govec: %s is stale; run scripts/govec.sh %s\n  committed: %s\n  fresh:     %s\n",
			path, d.Name, head(committed), head(fresh.Bytes()))
		return 1
	}
	fmt.Printf("govec: %s ok, %d lines; committed header %s, regenerated on %s/%s (target %s)\n",
		d.Name, bytes.Count(committed, []byte("\n")), head(committed), runtime.GOOS, runtime.GOARCH, vec.Target())
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: govec <domain> [-o file | --filename | --check file | --oracle] | govec list | govec target")
	os.Exit(2)
}
