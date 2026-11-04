// Command govec produces input/output test vectors by running Go TiDB code.
//
//	govec <domain> [-o file]   write the vector file (JSONL) for a domain
//	govec <domain> --oracle    read {"op","args"} lines on stdin, write records on stdout
//	govec list                 list domains
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	_ = fs.Parse(os.Args[2:])

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
	if err := d.WriteAll(w); err != nil {
		fmt.Fprintln(os.Stderr, "govec:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: govec <domain> [-o file] | govec <domain> --oracle | govec list")
	os.Exit(2)
}
