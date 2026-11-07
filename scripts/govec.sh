#!/usr/bin/env bash
# Regenerate govec vectors, or check that the committed ones are up to date.
#
#   scripts/govec.sh                       regenerate all domains, all committed targets
#   scripts/govec.sh decimal               regenerate one domain
#   scripts/govec.sh --check [dom]         regenerate on this host's target and compare
#   scripts/govec.sh --check --all [dom]   ... on every committed target
#
# Vectors depend on the code-generation target (gc fuses float x*y+z into FMA
# on arm64 and not on amd64, see govec/README.md "Targets"), so there is one
# file per target: vectors/v8.5.8/<target>/<domain>.jsonl.gz, target = amd64
# (GOAMD64=v1, Go's default) or arm64. The host's target runs natively; the
# other is cross-compiled (GOOS=linux, CGO_ENABLED=0) and run in a
# `docker run --platform linux/<arch>` container (Docker Desktop on macOS
# emulates amd64 with Rosetta or QEMU). Override with GOVEC_RUN_<target>, a
# command prefix that runs a linux binary, e.g. GOVEC_RUN_amd64="ssh x86box".
#
# The Go toolchain is pinned by govec/go.mod (go 1.25.12, as TiDB v8.5.8);
# with GOTOOLCHAIN=auto (the default) go downloads it if needed.
# Local dev against the source checkout (opt-in, never commit it):
#   (cd govec && go mod edit -replace github.com/pingcap/tidb=$HOME/src/.../tidb-v858 \
#      -replace github.com/pingcap/tidb/pkg/parser=$HOME/src/.../tidb-v858/pkg/parser)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
outdir="$root/vectors/v8.5.8"
targets=(amd64 arm64) # vec.CommittedTargets
check=0
all=0
while [[ "${1:-}" == --* ]]; do
  case "$1" in
    --check) check=1 ;;
    --all) all=1 ;;
    *) echo "govec.sh: unknown flag $1" >&2; exit 2 ;;
  esac
  shift
done

mkdir -p "$root/bin"
# Upstream fixes applied to the linked TiDB code (govec/fixes/README.md).
GOFLAGS="${GOFLAGS:+$GOFLAGS }$("$root/scripts/govec-fixes.sh")"
export GOFLAGS
native="$root/bin/govec"
(cd "$root/govec" && GOAMD64=v1 go build -o "$native" .)
host=$("$native" target)

# runner <target>: builds and prints the govec binary for the target.
runner() {
  local t=$1
  if [[ "$t" == "$host" ]]; then
    echo "$native"
    return
  fi
  local bin="$root/bin/govec-linux-$t"
  (cd "$root/govec" && GOOS=linux GOARCH="$t" GOAMD64=v1 CGO_ENABLED=0 go build -o "$bin" .)
  echo "$bin"
}
run() { # run <target> <bin> args...
  local t=$1 bin=$2
  shift 2
  if [[ "$t" == "$host" ]]; then
    "$bin" "$@"
  elif [[ -n "$(eval echo "\${GOVEC_RUN_$t:-}")" ]]; then
    eval "\${GOVEC_RUN_$t}" '"$bin" "$@"'
  else
    docker run --rm -i --platform "linux/$t" -v "$root:$root" -w "$root" busybox "$bin" "$@"
  fi
}

domains=("$@")
if [[ ${#domains[@]} -eq 0 ]]; then
  read -r -a domains <<<"$("$native" list | tr '\n' ' ')"
fi

if [[ $check -eq 1 ]]; then
  # govec --check regenerates in memory and compares with the committed file
  # of the same target: decompressed content, header goos excluded.
  [[ $all -eq 1 ]] || targets=("$host")
  status=0
  for t in "${targets[@]}"; do
    bin=$(runner "$t")
    for d in "${domains[@]}"; do
      run "$t" "$bin" "$d" --check "$outdir/$t/$("$native" "$d" --filename | cut -d/ -f2)" || status=1
    done
  done
  exit $status
fi

for t in "${targets[@]}"; do
  bin=$(runner "$t")
  mkdir -p "$outdir/$t"
  for d in "${domains[@]}"; do
    f="$outdir/$t/$("$native" "$d" --filename | cut -d/ -f2)"
    run "$t" "$bin" "$d" -o "$f"
    echo "govec: wrote $f ($(wc -c <"$f") bytes)"
  done
done
