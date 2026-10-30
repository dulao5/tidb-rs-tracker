#!/bin/sh
# Download the raw per-test coverage named in data/source.tsv into work/covmap.
# Needs curl, zstd, shasum (or sha256sum).
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
get() { awk -F'\t' -v k="$1" '$1 == k { print $2 }' "$root/data/source.tsv"; }
url=$(get raw_asset)
want=$(get raw_sha256)
mkdir -p "$root/work"
f="$root/work/$(basename "$url")"
[ -s "$f" ] || curl -fL -o "$f" "$url"
got=$( (sha256sum "$f" 2>/dev/null || shasum -a 256 "$f") | cut -d' ' -f1)
[ "$got" = "$want" ] || { echo "sha256 mismatch: $got != $want" >&2; exit 1; }
(cd "$root/work" && zstd -dc "$f" | tar -xf -)
echo "work/covmap: $(ls "$root/work/covmap" | wc -l) packages"
