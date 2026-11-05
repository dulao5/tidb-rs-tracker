#!/usr/bin/env bash
# Regenerate govec vectors, or check that the committed ones are up to date.
#
#   scripts/govec.sh                 regenerate all domains into vectors/v8.5.8/
#   scripts/govec.sh decimal         regenerate one domain
#   scripts/govec.sh --check [dom]   regenerate and compare with the committed files
#
# The Go toolchain is pinned by govec/go.mod (go 1.25.12, as TiDB v8.5.8);
# with GOTOOLCHAIN=auto (the default) go downloads it if needed.
# Local dev against the source checkout (opt-in, never commit it):
#   (cd govec && go mod edit -replace github.com/pingcap/tidb=$HOME/src/.../tidb-v858 \
#      -replace github.com/pingcap/tidb/pkg/parser=$HOME/src/.../tidb-v858/pkg/parser)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
outdir="$root/vectors/v8.5.8"
check=0
if [[ "${1:-}" == "--check" ]]; then
  check=1
  shift
fi

bin="$root/bin/govec"
mkdir -p "$root/bin"
(cd "$root/govec" && go build -o "$bin" .)

domains=("$@")
if [[ ${#domains[@]} -eq 0 ]]; then
  read -r -a domains <<<"$("$bin" list | tr '\n' ' ')"
fi

if [[ $check -eq 1 ]]; then
  # govec --check regenerates in memory and compares with the committed file:
  # decompressed content (for .jsonl.gz), header goos/goarch excluded, so the
  # files committed from darwin/arm64 must reproduce on linux/amd64 (CI).
  status=0
  for d in "${domains[@]}"; do
    "$bin" "$d" --check "$outdir/$("$bin" "$d" --filename)" || status=1
  done
  exit $status
fi

mkdir -p "$outdir"
for d in "${domains[@]}"; do
  f="$outdir/$("$bin" "$d" --filename)"
  "$bin" "$d" -o "$f"
  echo "govec: wrote $f ($(wc -c <"$f") bytes)"
done
