#!/bin/sh
# Run covmap for each package given; writes work/covmap/<pkg with / -> _>.tsv and work/logs.
# Usage: run_covmap.sh <tidb root> pkg/a pkg/b ...
set -u
repo=$1; shift
root=$(cd "$(dirname "$0")/.." && pwd)
here=$root/work
mkdir -p "$here/covmap" "$here/logs"
(cd "$root" && go build -o "$here/bin/covmap" ./cmd/covmap) || exit 1
for p in "$@"; do
  out="$here/covmap/$(echo "$p" | tr / _).tsv"
  [ -s "$out" ] && { echo "skip $p"; continue; }
  # Parallel runners may share a package list: the lock dir makes each package
  # owned by one runner, and the per-process tmp file keeps outputs apart.
  mkdir "$out.lock" 2>/dev/null || { echo "busy $p"; continue; }
  echo "== $p $(date +%T)"
  (cd /tmp && CC=/usr/bin/clang CXX=/usr/bin/clang++ "$here/bin/covmap" -repo "$repo" -pkg "$p" \
     > "$out.$$.tmp" 2> "$here/logs/$(echo "$p" | tr / _).log") && mv "$out.$$.tmp" "$out" || echo "FAILED $p"
  rmdir "$out.lock"
done
