#!/bin/sh
# Download the raw per-test coverage named in data/source.tsv into work/:
# raw_asset (work/covmap, the full run) and raw_fp_asset (work/covmap-fp, the
# re-runs with failpoints enabled), each checked against its *_sha256.
# Needs curl, zstd, shasum (or sha256sum).
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
get() { awk -F'\t' -v k="$1" '$1 == k { print $2 }' "$root/data/source.tsv"; }
mkdir -p "$root/work"
for k in raw raw_fp; do
  url=$(get ${k}_asset)
  want=$(get ${k}_sha256)
  [ -n "$url" ] || continue
  f="$root/work/$(basename "$url")"
  [ -s "$f" ] || curl -fL -o "$f" "$url"
  got=$( (sha256sum "$f" 2>/dev/null || shasum -a 256 "$f") | cut -d' ' -f1)
  [ "$got" = "$want" ] || { echo "$(basename "$f"): sha256 mismatch: $got != $want" >&2; exit 1; }
  (cd "$root/work" && zstd -dc "$f" | tar -xf -)
done
echo "work/covmap: $(ls "$root/work/covmap" | wc -l) files"
[ -d "$root/work/covmap-fp" ] && echo "work/covmap-fp: $(ls "$root/work/covmap-fp/status" | wc -l) packages"
exit 0
