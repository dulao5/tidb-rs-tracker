#!/bin/sh
# Rebuild the extraction DB work/extract.db from a TiDB checkout and the raw
# coverage in work/covmap: load-raw.sh <tidb repo root>
# Then `scripts/tracker.py export work/extract.db` refreshes data/.
set -eu
repo=$1
root=$(cd "$(dirname "$0")/.." && pwd)
here=$root/work
commit=$(git -C "$repo" rev-parse HEAD)
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
(cd "$root" && go run ./cmd/inventory -repo "$repo" -commit "$commit") > "$tmp"
rm -f "$here/extract.db"
sqlite3 "$here/extract.db" <<SQL
.read $root/sql/extract.sql
CREATE TEMP TABLE raw(kind,repo,pkg,path,name,line,subtests,uses_store,uses_fp,stmts,base_commit);
.mode tabs
.import $tmp raw
INSERT INTO go_test(kind,repo,pkg,path,name,line,subtests,uses_store,uses_fp,stmts,base_commit)
SELECT kind,repo,pkg,path,name,NULLIF(line,'0'),subtests,uses_store,uses_fp,NULLIF(stmts,''),base_commit FROM raw;
SQL
sqlite3 "$here/extract.db" "SELECT kind, count(*) FROM go_test GROUP BY kind;"

# Per-test coverage mappings produced by cmd/covmap, one TSV per package.
for f in "$here"/covmap/*.tsv; do
  [ -e "$f" ] || continue
  sqlite3 "$here/extract.db" <<SQL
CREATE TEMP TABLE cov(pkg,test,file,func);
.mode tabs
.import $f cov
INSERT OR IGNORE INTO go_func(file,name) SELECT DISTINCT file,func FROM cov;
INSERT OR IGNORE INTO test_func(test_id,func_id)
SELECT t.id, f.id FROM cov c
JOIN go_test t ON t.kind='unit' AND t.pkg=c.pkg AND t.name=c.test
JOIN go_func f ON f.file=c.file AND f.name=c.func;
SQL
done
# Coverage-run outcome per test, from the covmap logs ("<Test>: exit status N").
for f in "$here"/covmap/*.tsv; do
  b=$(basename "$f" .tsv)
  pkg=$(head -n 1 "$f" | cut -f1)
  [ -n "$pkg" ] || continue
  grep -oE '(Test[A-Za-z0-9_]+): (exit status [0-9]+|signal: [a-z ]+)' "$here/logs/$b.log" 2>/dev/null |
    awk -v pkg="$pkg" -F': ' '{print pkg "\t" $1 "\t" $2}'
done > "$tmp"
sqlite3 "$here/extract.db" <<SQL
CREATE TEMP TABLE st(pkg,test,detail);
.mode tabs
.import $tmp st
INSERT OR REPLACE INTO cov_status(test_id,status,detail)
SELECT DISTINCT t.id,'failed',s.detail FROM st s JOIN go_test t ON t.kind='unit' AND t.pkg=s.pkg AND t.name=s.test;
INSERT OR IGNORE INTO cov_status(test_id,status) SELECT DISTINCT test_id,'ok' FROM test_func;
SQL
sqlite3 "$here/extract.db" "SELECT 'mapped tests', count(DISTINCT test_id), 'funcs', count(DISTINCT func_id) FROM test_func;"
sqlite3 "$here/extract.db" "SELECT status, count(*) FROM cov_status GROUP BY status;"
sqlite3 "$here/extract.db" < "$root/sql/derive_features.sql"
sqlite3 "$here/extract.db" "SELECT count(*) AS features, (SELECT count(*) FROM test_feature) AS links FROM feature;"
