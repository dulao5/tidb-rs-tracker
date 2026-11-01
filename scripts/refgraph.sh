#!/bin/sh
# Reference graph of the Go code under pkg/ (cmd/refgraph):
#   data/go_decls.tsv      package-level types, vars and consts
#   data/go_file_refs.tsv  file -> file edges, counts by kind
#   work/go_refs.tsv       function-level edges (not committed; about 1M rows)
# and the go_funcs.tsv rows not found as type-checked declarations in
# work/go_refs_missing.tsv.
#
# Builds the packages with the patched go.mod (scripts/patched-modfile.sh,
# run first if work/modfile/go.mod is missing) to load dependencies from
# export data (no tests; default GOOS/GOARCH). TAGS (default: none) are passed as -tags.
# Usage: refgraph.sh <tidb repo root at tidb_commit>
set -eu
repo=$1
root=$(cd "$(dirname "$0")/.." && pwd)
[ -f "$root/work/modfile/go.mod" ] || "$root/scripts/patched-modfile.sh" "$repo"
mkdir -p "$root/work/bin"
(cd "$root" && go build -o work/bin/refgraph ./cmd/refgraph)
# pkg/parser/goyacc (a tool in the parser module) imports modules whose sums
# are only in pkg/parser/go.sum: add them, in a copy of the modfile.
mf=$root/work/modfile-refgraph
mkdir -p "$mf"
cp "$root/work/modfile/go.mod" "$mf/go.mod"
cat "$root/work/modfile/go.sum" "$repo/pkg/parser/go.sum" | sort -u > "$mf/go.sum"
export CC=${CC:-/usr/bin/clang} TMPDIR=${TMPDIR:-/tmp}
"$root/work/bin/refgraph" -repo "$repo" -modfile "$mf/go.mod" -tags "${TAGS:-}" \
  -decls "$root/data/go_decls.tsv" \
  -funcs "$root/data/go_funcs.tsv" -missing "$root/work/go_refs_missing.tsv" \
  -refs "$root/work/go_refs.tsv" -filerefs "$root/data/go_file_refs.tsv"
wc -l "$root/data/go_decls.tsv" "$root/data/go_file_refs.tsv" "$root/work/go_refs.tsv"
