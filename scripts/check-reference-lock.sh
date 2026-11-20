#!/usr/bin/env bash
# govec links the Go TiDB commit pinned in tidb-rs's tidb-reference.lock (the tree every Go
# source of the port comes from): the pseudo-versions of github.com/pingcap/tidb and
# github.com/pingcap/tidb/pkg/parser in govec/go.mod must end in the lock's commit.
#
#   scripts/check-reference-lock.sh <tidb-rs checkout>
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
lock=${1:?usage: $0 <tidb-rs checkout>}/tidb-reference.lock
[ -f "$lock" ] || { echo "check-reference-lock: no $lock" >&2; exit 1; }
commit=$(sed -n 's/^commit=//p' "$lock")
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { echo "check-reference-lock: malformed commit in $lock" >&2; exit 1; }
status=0
for mod in github.com/pingcap/tidb github.com/pingcap/tidb/pkg/parser; do
  ver=$(awk -v m="$mod" '$1 == m && $2 ~ /^v/ { print $2; exit }' "$root/govec/go.mod")
  if [ "${ver##*-}" = "${commit:0:12}" ]; then
    echo "ok: $mod $ver = ${commit:0:12}"
  else
    echo "check-reference-lock: govec/go.mod requires $mod ${ver:-(none)}, tidb-reference.lock pins $commit" >&2
    status=1
  fi
done
exit $status
