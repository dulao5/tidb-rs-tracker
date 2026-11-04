#!/usr/bin/env bash
# Regenerate govec vectors, or check that the committed ones are up to date.
#
#   scripts/govec.sh                 regenerate all domains into vectors/v8.5.8/
#   scripts/govec.sh decimal         regenerate one domain
#   scripts/govec.sh --check [dom]   regenerate into a temp dir and compare bytes
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
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  status=0
  for d in "${domains[@]}"; do
    "$bin" "$d" -o "$tmp/$d.jsonl"
    if ! cmp -s "$tmp/$d.jsonl" "$outdir/$d.jsonl"; then
      echo "govec: $outdir/$d.jsonl is stale; run scripts/govec.sh $d" >&2
      diff <(head -c 2000 "$outdir/$d.jsonl") <(head -c 2000 "$tmp/$d.jsonl") >&2 || true
      status=1
    else
      echo "govec: $d ok ($(wc -l <"$tmp/$d.jsonl") lines)"
    fi
  done
  exit $status
fi

mkdir -p "$outdir"
for d in "${domains[@]}"; do
  "$bin" "$d" -o "$outdir/$d.jsonl"
  echo "govec: wrote $outdir/$d.jsonl ($(wc -l <"$outdir/$d.jsonl") lines, $(wc -c <"$outdir/$d.jsonl") bytes)"
done
