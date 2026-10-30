#!/bin/sh
# Run covmap for each package given. Writes work/$OUT/<pkg with / -> _>.tsv
# (coverage), work/$OUT/status/<pkg>.tsv (per-test outcome) and
# work/$OUT/logs/<pkg>.log.
# Usage: [OUT=covmap] [TESTS=list.tsv] run_covmap.sh <tidb root> pkg/a pkg/b ...
#   OUT    output dir under work/ (default covmap; failpoint re-runs use covmap-fp)
#   TESTS  TSV of <pkg> <test>: run only the listed tests of each package
#   TIMEOUT per-test timeout (covmap -timeout, default 5m)
set -u
repo=$1; shift
root=$(cd "$(dirname "$0")/.." && pwd)
out_dir=$root/work/${OUT:-covmap}
mkdir -p "$out_dir/status" "$out_dir/logs"
(cd "$root" && go build -o "$root/work/bin/covmap" ./cmd/covmap) || exit 1
for p in "$@"; do
  b=$(echo "$p" | tr / _)
  out="$out_dir/$b.tsv"
  [ -s "$out_dir/status/$b.tsv" ] && { echo "skip $p"; continue; }
  only=""
  if [ -n "${TESTS:-}" ]; then
    only=$(awk -F'\t' -v p="$p" '$1 == p { printf "%s%s", sep, $2; sep = "," }' "$TESTS")
    [ -n "$only" ] || { echo "no tests listed for $p"; continue; }
  fi
  # Parallel runners may share a package list: the lock dir makes each package
  # owned by one runner, and the per-process tmp files keep outputs apart.
  mkdir "$out.lock" 2>/dev/null || { echo "busy $p"; continue; }
  echo "== $p $(date +%T)"
  # A short TMPDIR: tests put unix sockets in t.TempDir(), and macOS's default
  # /var/folders/... path exceeds the 104-byte socket path limit.
  (cd /tmp && TMPDIR=/tmp CC=/usr/bin/clang CXX=/usr/bin/clang++ "$root/work/bin/covmap" -repo "$repo" -pkg "$p" \
     -tests "$only" -timeout "${TIMEOUT:-5m}" -status "$out_dir/status/$b.tsv.$$.tmp" \
     > "$out.$$.tmp" 2> "$out_dir/logs/$b.log") &&
    mv "$out.$$.tmp" "$out" && mv "$out_dir/status/$b.tsv.$$.tmp" "$out_dir/status/$b.tsv" ||
    { echo "FAILED $p"; rm -f "$out.$$.tmp" "$out_dir/status/$b.tsv.$$.tmp"; }
  rmdir "$out.lock"
done
