#!/bin/sh
# Write work/modfile/go.mod: <tidb>/go.mod plus a replace of every module that
# TiDB's Bazel build patches (build/patches/<bazel repo name>.patch, applied to
# a copy of the module under work/patched/). Pass it to run_covmap.sh as
# MODFILE=work/modfile/go.mod.
#
# At v8.5.8, go.etcd.io/etcd/server/v3 v3.5.15 does not compile against the
# otelgrpc v0.61.0 that go.mod selects (undefined otelgrpc.UnaryServerInterceptor),
# so `go test` fails for every package that embeds etcd; Bazel builds it with
# io_etcd_go_etcd_server_v3.patch. Only that patch is handled here.
# Usage: patched-modfile.sh <tidb repo root>
set -eu
repo=$1
root=$(cd "$(dirname "$0")/.." && pwd)
mod=go.etcd.io/etcd/server/v3
patch=$repo/build/patches/io_etcd_go_etcd_server_v3.patch
ver=$(cd "$repo" && go list -m -f '{{.Version}}' $mod)
src=$(cd "$repo" && go mod download -json $mod@"$ver" | sed -n 's/^[[:space:]]*"Dir": "\(.*\)",$/\1/p')
dst=$root/work/patched/etcd-server-v3
rm -rf "$dst" && mkdir -p "$(dirname "$dst")" "$root/work/modfile"
cp -R "$src" "$dst" && chmod -R u+w "$dst"
(cd "$dst" && patch -p1 < "$patch")
cp "$repo/go.mod" "$root/work/modfile/go.mod"
cp "$repo/go.sum" "$root/work/modfile/go.sum"
printf '\n// tidb-rs-tracker: the copy Bazel builds (%s)\nreplace %s => %s\n' \
  "build/patches/$(basename "$patch")" $mod "$dst" >> "$root/work/modfile/go.mod"
echo "$root/work/modfile/go.mod: $mod $ver patched"
