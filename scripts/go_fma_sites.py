#!/usr/bin/env python3
"""Registry of Go float multiply-add sites that gc fuses into FMA, per site x platform.

Go's spec lets the compiler fuse `x*y + z` into one fused multiply-add (one rounding
instead of two). Whether gc does so depends on the platform (GOARCH, and GOAMD64 on
amd64) and on the individual site, so it can only be read from the generated code, not
from the source shape. tidb-rs ports such a site per target (mapping rule M-FMA in
tidb-rs docs/porting/mappings.md): `f64::mul_add` on targets where Go fuses it, a
separate multiply and add where Go does not.

This script compiles Go packages for each target with `-gcflags=<pkg>=-S`, collects the
fused multiply-add instructions and maps them back to source lines:

    scripts/go_fma_sites.py                          write data/go_fma_sites.tsv (pkg/types)
    scripts/go_fma_sites.py --check                  regenerate and compare (CI)
    scripts/go_fma_sites.py -p github.com/pingcap/tidb/pkg/expression -o -   other packages

The build runs in the govec module (govec/go.mod pins TiDB v8.5.8 and go 1.25.12, the
toolchain of v8.5.8; GOTOOLCHAIN=auto downloads it). Cross-compiling is enough: nothing
is run. The packages must be buildable from that module (add a blank import to govec
if a package is not in its build list yet).

Output: one row per source line fused on at least one target, columns per target
(`yes`/`no`), `instrs` with the mnemonics per fused target, and the source text.
The baseline amd64 target is GOAMD64=v1, Go's default (TiDB v8.5.8's Makefile does
not set GOAMD64); v3 is informational.
"""

import argparse
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MODDIR = os.path.join(ROOT, "govec")
TIDB_MOD = "github.com/pingcap/tidb"
DEFAULT_PKGS = ["github.com/pingcap/tidb/pkg/types"]

# (column, GOARCH, extra env). Column names are the TSV columns (tracker.py TABLES).
TARGETS = [
    ("amd64_v1", "amd64", {"GOAMD64": "v1"}),
    ("amd64_v3", "amd64", {"GOAMD64": "v3"}),
    ("arm64", "arm64", {}),
    ("ppc64le", "ppc64le", {}),
    ("s390x", "s390x", {}),
    ("riscv64", "riscv64", {}),
    ("loong64", "loong64", {}),
]
COLUMNS = ["pkg", "file", "line", "symbol"] + [t[0] for t in TARGETS] + ["instrs", "source"]

# Floating-point fused multiply-add/sub mnemonics in gc's assembly syntax:
#   amd64   VFMADD231SD, VFNMSUB213SS, ... (and packed forms)
#   arm64   FMADDD, FMADDS, FMSUBD, FNMADDD, ...; vector VFMLA, VFMLS
#   ppc64   FMADD, FMADDS, FMSUB, FNMADD, ...; VSX XSMADDADP, XVMADDMDP, ...
#   s390x   FMADD, FMADDS, FMSUB, FMSUBS (WFMADB, ... for vector)
#   riscv64 FMADDD, FMADDS, FMSUBD, FNMADDD, ...
#   loong64 FMADDD, FMADDF, FMSUBD, FNMADDD, ...
# Integer multiply-add (arm64 MADD, loong64 atomic AMADDD...) must not match.
FMA_RE = re.compile(r"^(V?FN?M(ADD|SUB)[A-Z0-9]*|VFML[AS]|XS?V?N?M(ADD|SUB)[A-Z]*|WFN?M(ADD|SUB)[A-Z]*)$")
# `	0x0124 00292 (/path/mydecimal.go:1118)	FMADDD	F1, F0, F2, F0`
INSTR_RE = re.compile(r"^\s+0x[0-9a-f]+\s+\d+\s+\((.+\.go):(\d+)\)\s+(\S+)")
# `github.com/pingcap/tidb/pkg/types.(*MyDecimal).ToFloat64 STEXT size=...`
FUNC_RE = re.compile(r"^(\S+) STEXT\b")


def go_env(var):
    return subprocess.run(["go", "env", var], cwd=MODDIR, capture_output=True, text=True, check=True).stdout.strip()


def module_dir(mod):
    r = subprocess.run(["go", "list", "-m", "-f", "{{.Dir}}", mod], cwd=MODDIR, capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 else ""


def tracker_symbol(sym, pkgpath):
    """`<pkgpath>.(*T).M` -> `T.M`, `<pkgpath>.F` -> `F` (tracker go_funcs.tsv format)."""
    if sym.startswith(pkgpath + "."):
        sym = sym[len(pkgpath) + 1:]
    return re.sub(r"^\(\*?([^)]+)\)\.", r"\1.", sym)


def scan_target(pkgs, goarch, extra_env):
    """-> {(abs_file, line): {"syms": set, "instrs": set}} of fused sites."""
    env = dict(os.environ, GOOS="linux", GOARCH=goarch, CGO_ENABLED="0", **extra_env)
    gcflags = [f"-gcflags={p}=-S" for p in pkgs]  # one flag per package pattern
    r = subprocess.run(["go", "build", *gcflags, *pkgs], cwd=MODDIR, env=env,
                       capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"go_fma_sites: go build GOARCH={goarch} {extra_env} failed:\n{r.stderr[-4000:]}")
    sites = {}
    func = ""
    pkgpath = ""
    for line in r.stderr.splitlines():  # the compiler's -S listing goes to stderr
        if line.startswith("# "):
            pkgpath = line[2:].strip()
            continue
        m = FUNC_RE.match(line)
        if m:
            func = tracker_symbol(m.group(1), pkgpath)
            continue
        m = INSTR_RE.match(line)
        if m and FMA_RE.match(m.group(3)):
            s = sites.setdefault((m.group(1), int(m.group(2))), {"syms": set(), "instrs": set()})
            s["syms"].add(func)
            s["instrs"].add(m.group(3))
    return sites


def rel_path(path, roots):
    for prefix, root in roots:
        if root and path.startswith(root + os.sep):
            return prefix + path[len(root) + 1:]
    return path


def generate(pkgs):
    tidb_dir = module_dir(TIDB_MOD)
    roots = [("", tidb_dir), ("$GOROOT/", go_env("GOROOT")), ("$GOMODCACHE/", go_env("GOMODCACHE"))]
    per_target = {}
    for col, goarch, extra in TARGETS:
        per_target[col] = scan_target(pkgs, goarch, extra)
        print(f"go_fma_sites: {col}: {len(per_target[col])} fused lines", file=sys.stderr)
    rows = []
    for key in sorted({k for s in per_target.values() for k in s}):
        absfile, line = key
        file = rel_path(absfile, roots)
        syms = sorted({sym for s in per_target.values() if key in s for sym in s[key]["syms"]})
        flags = ["yes" if key in per_target[c] else "no" for c, _, _ in TARGETS]
        instrs = " ".join(f"{c}={','.join(sorted(per_target[c][key]['instrs']))}"
                          for c, _, _ in TARGETS if key in per_target[c])
        try:
            with open(absfile, encoding="utf-8") as f:
                src = f.read().splitlines()[line - 1].strip().replace("\t", " ")
        except OSError:
            src = ""
        rows.append([os.path.dirname(file), file, str(line), ",".join(syms), *flags, instrs, src])
    # tracker.py key order: (pkg, file, line) with line compared as text
    rows.sort(key=lambda r: (r[0], r[1], r[2]))
    return rows


def render(rows):
    return "".join("\t".join(r) + "\n" for r in [COLUMNS] + rows)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-p", "--pkg", action="append", help="Go package path (repeatable); default pkg/types")
    ap.add_argument("-o", "--output", default=os.path.join(ROOT, "data", "go_fma_sites.tsv"),
                    help="output TSV (- for stdout)")
    ap.add_argument("--check", action="store_true", help="regenerate and compare with the output file")
    args = ap.parse_args()
    text = render(generate(args.pkg or DEFAULT_PKGS))
    if args.check:
        with open(args.output, encoding="utf-8") as f:
            if f.read() != text:
                sys.exit(f"go_fma_sites: {args.output} is stale; run scripts/go_fma_sites.py")
        print(f"go_fma_sites: {args.output} ok")
    elif args.output == "-":
        sys.stdout.write(text)
    else:
        with open(args.output, "w", encoding="utf-8", newline="\n") as f:
            f.write(text)
        print(f"go_fma_sites: wrote {args.output} ({text.count(chr(10)) - 1} sites)")


if __name__ == "__main__":
    main()
