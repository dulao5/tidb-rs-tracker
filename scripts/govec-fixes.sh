#!/usr/bin/env bash
# Prepare the TiDB module govec links with the upstream fixes of govec/fixes
# applied (fixed on master, not backported to release-8.5; govec/fixes/README.md)
# and print the go flag that selects it:
#   export GOFLAGS="$(scripts/govec-fixes.sh)"
#
# A `go build -overlay` cannot replace files beneath GOMODCACHE, so the module
# is copied to work/govec-tidb (about 120 MB, redone only when the module
# version or a fix changes), the *.fixed files are copied over it, and
# work/govec-modfile/go.mod is govec/go.mod plus a replace of
# github.com/pingcap/tidb by the copy. govec/go.mod itself is not touched.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
mod=github.com/pingcap/tidb
src=$(cd "$root/govec" && GOFLAGS= go list -m -f '{{.Dir}}' $mod)
ver=$(cd "$root/govec" && GOFLAGS= go list -m -f '{{.Version}}' $mod)
dst=$root/work/govec-tidb
mf=$root/work/govec-modfile
stamp=$( { echo "$ver"; find "$root/govec/fixes" -name '*.fixed' -print0 | sort -z | xargs -0 shasum; } | shasum | cut -c1-16)
if [[ "$(cat "$dst/.govec-fixes-stamp" 2>/dev/null)" != "$stamp" ]]; then
  rm -rf "$dst" && mkdir -p "$(dirname "$dst")"
  cp -R "$src" "$dst" && chmod -R u+w "$dst"
  while IFS= read -r f; do
    rel=${f#"$root/govec/fixes/"}
    cp "$f" "$dst/${rel%.fixed}"
  done < <(find "$root/govec/fixes" -name '*.fixed' | sort)
  echo "$stamp" >"$dst/.govec-fixes-stamp"
fi
mkdir -p "$mf"
cp "$root/govec/go.mod" "$mf/go.mod"
cp "$root/govec/go.sum" "$mf/go.sum"
printf '\n// scripts/govec-fixes.sh: TiDB %s with the upstream fixes of govec/fixes\nreplace %s => %s\n' \
  "$ver" "$mod" "$dst" >>"$mf/go.mod"
echo "-modfile=$mf/go.mod"
