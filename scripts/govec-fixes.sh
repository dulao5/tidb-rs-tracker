#!/usr/bin/env bash
# Prepare the TiDB modules govec links with the upstream fixes of govec/fixes
# applied (not in v8.5.8; govec/fixes/README.md) and print the go flag that
# selects them:
#   export GOFLAGS="$(scripts/govec-fixes.sh)"
#
# A `go build -overlay` cannot replace files beneath GOMODCACHE, so each module
# is copied to work/ (redone only when the module versions or a fix change),
# the *.fixed files are copied over it, and work/govec-modfile/go.mod is
# govec/go.mod plus a replace of each module by its copy. govec/go.mod itself
# is not touched. pkg/parser is a separate module
# (github.com/pingcap/tidb/pkg/parser, not in the tidb module's zip), so
# fixes/pkg/parser/<path>.fixed go to its copy work/govec-tidb-parser/<path>
# and every other fixes/<path>.fixed to work/govec-tidb/<path> (about 120 MB).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
mf=$root/work/govec-modfile
fixes=$root/govec/fixes
stamp_all=$(find "$fixes" -name '*.fixed' -print0 | sort -z | xargs -0 shasum | shasum | cut -c1-16)
mkdir -p "$mf"
cp "$root/govec/go.mod" "$mf/go.mod"
cp "$root/govec/go.sum" "$mf/go.sum"

# prepare <module> <copy dir> <fixes subdir>: copies the module to the copy dir, applies
# the *.fixed files under fixes/<fixes subdir>, and appends its replace to the modfile.
prepare() {
  local mod=$1 dst=$2 sub=$3 src ver stamp f rel
  # `go list -m -f {{.Dir}}` prints nothing when the module is not in the module cache yet
  # (fresh CI cache), so download it first and refuse to go on without a directory.
  (cd "$root/govec" && GOFLAGS= go mod download "$mod")
  src=$(cd "$root/govec" && GOFLAGS= go list -m -f '{{.Dir}}' "$mod")
  ver=$(cd "$root/govec" && GOFLAGS= go list -m -f '{{.Version}}' "$mod")
  if [[ -z "$src" || ! -d "$src" ]]; then
    echo "govec-fixes.sh: no module directory for $mod" >&2
    exit 1
  fi
  stamp=$(echo "$ver $sub $stamp_all" | shasum | cut -c1-16)
  if [[ "$(cat "$dst/.govec-fixes-stamp" 2>/dev/null)" != "$stamp" ]]; then
    rm -rf "$dst" && mkdir -p "$(dirname "$dst")"
    cp -R "$src" "$dst" && chmod -R u+w "$dst"
    while IFS= read -r f; do
      rel=${f#"$fixes/$sub"}
      # The tidb module's zip has no pkg/parser (a module of its own).
      [[ -z "$sub" && "$rel" == pkg/parser/* ]] && continue
      cp "$f" "$dst/${rel%.fixed}"
    done < <([[ ! -d "$fixes/$sub" ]] || find "$fixes/$sub" -name '*.fixed' | sort)
    echo "$stamp" >"$dst/.govec-fixes-stamp"
  fi
  printf '\n// scripts/govec-fixes.sh: %s %s with the upstream fixes of govec/fixes\nreplace %s => %s\n' \
    "$mod" "$ver" "$mod" "$dst" >>"$mf/go.mod"
}
prepare github.com/pingcap/tidb "$root/work/govec-tidb" ""
prepare github.com/pingcap/tidb/pkg/parser "$root/work/govec-tidb-parser" pkg/parser/
echo "-modfile=$mf/go.mod"
