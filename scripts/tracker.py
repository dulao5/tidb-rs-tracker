#!/usr/bin/env python3
"""Tracker data tool. Python 3.9+, standard library only.

  tracker.py export <extraction.db>   extraction DB (scripts/load-raw.sh) -> data/*.tsv
  tracker.py build [-o tracker.db]    data/*.tsv -> SQLite (for Datasette / ad-hoc SQL)
  tracker.py report                   data/*.tsv -> reports/*.md
  tracker.py check                    validate data/, and fail if reports/ is stale

data/ is the source of truth. Every file is a sorted TSV with a header row, so
a re-extraction shows up as a reviewable git diff. Row order and keys never
depend on SQLite row ids.
"""

import argparse
import collections
import json
import os
import sqlite3
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DATA = os.path.join(ROOT, "data")
REPORTS = os.path.join(ROOT, "reports")
SCHEMA = os.path.join(ROOT, "sql", "tracker.sql")

# Links below this share of a test's specificity mass are dropped from
# test_feature.tsv. Every mapped test keeps at least one link at 0.01 and it
# cuts 2.1M links to about 51k. The full mapping is rebuilt from the raw
# coverage release asset.
MIN_WEIGHT = 0.01

# cov_status detail of a passing test that reached nothing beyond package
# init (written by cmd/covmap).
EMPTY_DETAIL = "no functions reached beyond package init"

# file -> (columns, key columns). Rows are sorted by the key columns.
TABLES = {
    "source.tsv": (["key", "value"], ["key"]),
    "scope.tsv": (["pkg", "phase"], ["pkg"]),
    "tests.tsv": (
        ["kind", "pkg", "name", "path", "line", "subtests", "uses_store", "uses_fp", "stmts"],
        # Same-named tests can exist in a package and its external _test
        # package (6 cases); `go test -run` runs both, and coverage maps them
        # together by (pkg, name).
        ["kind", "pkg", "name", "path"],
    ),
    "features.tsv": (["feature", "module", "funcs", "symbols"], ["feature"]),
    "cov_status.tsv": (["pkg", "test", "status", "detail"], ["pkg", "test"]),
    "test_feature.tsv": (["pkg", "test", "feature", "weight"], ["pkg", "test", "feature"]),
}


def die(msg):
    sys.exit("tracker: " + msg)


def write_tsv(path, cols, rows):
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\t".join(cols) + "\n")
        for r in rows:
            f.write("\t".join("" if v is None else str(v) for v in r) + "\n")


def read_tsv(name):
    cols, _ = TABLES[name]
    path = os.path.join(DATA, name)
    with open(path, encoding="utf-8") as f:
        header = f.readline().rstrip("\n").split("\t")
        if header != cols:
            die(f"{name}: header {header} != {cols}")
        rows = []
        for n, line in enumerate(f, 2):
            r = line.rstrip("\n").split("\t")
            if len(r) != len(cols):
                die(f"{name}:{n}: {len(r)} fields, want {len(cols)}")
            rows.append(r)
    return cols, rows


def load_all():
    return {name: read_tsv(name) for name in TABLES}


# ---------------------------------------------------------------- export


def cmd_export(args):
    db = sqlite3.connect(args.db)
    q = db.execute
    commits = [r[0] for r in q("SELECT DISTINCT base_commit FROM go_test")]
    if len(commits) != 1:
        die(f"want one base_commit, got {commits}")
    source = dict(read_tsv("source.tsv")[1]) if os.path.exists(os.path.join(DATA, "source.tsv")) else {}
    source["tidb_commit"] = commits[0]
    source.setdefault("tidb_repo", "https://github.com/pingcap/tidb")
    out = {}
    out["source.tsv"] = sorted(source.items())
    out["tests.tsv"] = q(
        "SELECT kind, pkg, name, path, line, subtests, uses_store, uses_fp, stmts FROM go_test"
    ).fetchall()
    out["features.tsv"] = [
        (name, mod, len(syms), " ".join(sorted(syms)))
        for name, mod, syms in (
            (r[0], r[1], json.loads(r[2]))
            for r in q(
                "SELECT f.name, m.go_pkg, f.go_symbols FROM feature f JOIN module m ON m.id = f.module_id"
            )
        )
    ]
    out["cov_status.tsv"] = q(
        "SELECT t.pkg, t.name, min(c.status), COALESCE(max(c.detail), '') FROM cov_status c"
        " JOIN go_test t ON t.id = c.test_id GROUP BY t.pkg, t.name"
    ).fetchall()
    out["test_feature.tsv"] = [
        (pkg, test, feat, f"{w:.3f}")
        for pkg, test, feat, w in q(
            "SELECT t.pkg, t.name, f.name, max(tf.weight) FROM test_feature tf"
            " JOIN go_test t ON t.id = tf.test_id JOIN feature f ON f.id = tf.feature_id"
            " WHERE tf.weight >= ? GROUP BY t.pkg, t.name, f.name",
            (MIN_WEIGHT,),
        )
    ]
    for name, rows in out.items():
        cols, key = TABLES[name]
        idx = [cols.index(k) for k in key]
        rows = sorted(rows, key=lambda r: [str(r[i]) for i in idx])
        write_tsv(os.path.join(DATA, name), cols, rows)
        print(f"{name}: {len(rows)} rows")
    print("scope.tsv is maintained by hand; not touched")


# ---------------------------------------------------------------- build


def build(path):
    if os.path.exists(path):
        os.remove(path)
    db = sqlite3.connect(path)
    with open(SCHEMA, encoding="utf-8") as f:
        db.executescript(f.read())
    for name, (cols, rows) in load_all().items():
        table = name[: -len(".tsv")]
        rows = [[None if v == "" else v for v in r] for r in rows]
        db.executemany(
            f"INSERT INTO {table} ({', '.join(cols)}) VALUES ({', '.join('?' * len(cols))})", rows
        )
    db.commit()
    return db


def cmd_build(args):
    db = build(args.output)
    n = db.execute("SELECT count(*) FROM tests").fetchone()[0]
    print(f"{args.output}: {n} tests")


# ---------------------------------------------------------------- check


def cmd_check(args):
    data = load_all()
    errors = []
    for name, (cols, rows) in data.items():
        _, key = TABLES[name]
        idx = [cols.index(k) for k in key]
        keys = [tuple(r[i] for i in idx) for r in rows]
        if keys != sorted(keys):
            errors.append(f"{name}: rows not sorted by {key}")
        if len(set(keys)) != len(keys):
            errors.append(f"{name}: duplicate keys")
    unit = {(r[1], r[2]) for r in data["tests.tsv"][1] if r[0] == "unit"}
    feats = {r[0] for r in data["features.tsv"][1]}
    for name in ("cov_status.tsv", "test_feature.tsv"):
        missing = [r for r in data[name][1] if (r[0], r[1]) not in unit]
        if missing:
            errors.append(f"{name}: {len(missing)} rows name unknown tests, e.g. {missing[0][:2]}")
    bad = [r for r in data["test_feature.tsv"][1] if r[2] not in feats]
    if bad:
        errors.append(f"test_feature.tsv: {len(bad)} rows name unknown features, e.g. {bad[0][2]}")
    with tempfile.TemporaryDirectory() as d:
        generated = render_reports(build(os.path.join(d, "t.db")))
    for fname, text in generated.items():
        path = os.path.join(REPORTS, fname)
        old = open(path, encoding="utf-8").read() if os.path.exists(path) else None
        if old != text:
            errors.append(f"reports/{fname} is stale: run scripts/tracker.py report")
    if errors:
        die("\n  ".join(["check failed:"] + errors))
    print("check ok")


# ---------------------------------------------------------------- report


def table(header, rows, align=None):
    align = align or ["l"] + ["r"] * (len(header) - 1)
    sep = ["---:" if a == "r" else "---" for a in align]
    out = ["| " + " | ".join(header) + " |", "| " + " | ".join(sep) + " |"]
    out += ["| " + " | ".join(str(c) for c in r) + " |" for r in rows]
    return "\n".join(out)


def pct(a, b):
    return f"{100 * a / b:.1f}%" if b else "-"


def area(pkg):
    """Group packages into areas for reporting: pkg/executor/join -> pkg/executor."""
    parts = pkg.split("/")
    return "/".join(parts[:2]) if parts[0] in ("pkg", "br", "lightning", "dumpling", "cmd") else parts[0]


def render_reports(db):
    q = lambda sql, *a: db.execute(sql, a).fetchall()
    src = dict(q("SELECT key, value FROM source"))
    commit = src.get("tidb_commit", "?")
    head = (
        "<!-- Generated by scripts/tracker.py report. Do not edit. -->\n\n"
        f"TiDB source: `{commit[:10]}` ({src.get('tidb_repo', '')})\n"
    )

    (unit, sql_level, fp, integ, stmts) = q(
        "SELECT sum(kind='unit'), sum(kind='unit' AND uses_store=1), sum(kind='unit' AND uses_fp=1),"
        " sum(kind='integration'), sum(CASE WHEN kind='integration' THEN stmts END) FROM tests"
    )[0]
    scope_pkgs = q("SELECT count(*) FROM scope WHERE phase = 'P1'")[0][0]
    cov = dict(q("SELECT coverage, count(*) FROM unit_tests WHERE phase = 'P1' GROUP BY coverage"))
    ok, failed, skipped, notrun = (cov.get(k, 0) for k in ("ok", "failed", "skipped", "not run"))
    scope_tests = sum(cov.values())
    empty = q(
        "SELECT count(*) FROM unit_tests u JOIN cov_status c ON c.pkg = u.pkg AND c.test = u.name"
        " WHERE u.phase = 'P1' AND u.coverage = 'ok' AND c.detail = ?",
        EMPTY_DETAIL,
    )[0][0]
    feats, links = q("SELECT (SELECT count(*) FROM features), (SELECT count(*) FROM test_feature)")[0]

    s = ["# Summary", "", head]
    s += ["## Inventory", ""]
    s.append(
        table(
            ["", "count"],
            [
                ["Unit tests (Go `TestXxx`)", unit],
                ["… SQL-level (mock store / testkit)", sql_level],
                ["… using failpoints", fp],
                ["Integration test files (`tests/integrationtest/t`)", integ],
                ["… SQL statements in them", stmts],
                ["Features (v0: one per Go source file)", feats],
                [f"Test → feature links (weight ≥ {MIN_WEIGHT})", links],
            ],
        )
    )
    s += ["", "## Coverage mapping (P1 scope)", ""]
    s.append(
        "P1 is the set of packages relevant to the prototype (`data/scope.tsv`). "
        "Each test in it is run alone with coverage to find the Go functions it reaches. "
        "Tests that failed or produced no coverage in the full run were re-run with failpoints enabled. "
        "A test that still fails stopped early, so its mapping is incomplete (a lower bound). "
        "A skipped test reached nothing under this build (`t.Skip`, e.g. next-gen kernel only).\n"
    )
    s.append(
        table(
            ["", "count", "share"],
            [
                ["Packages in P1", scope_pkgs, ""],
                ["Unit tests in P1 packages", scope_tests, ""],
                ["Coverage run ok", ok, pct(ok, scope_tests)],
                ["… reached no function beyond package init", empty, pct(empty, scope_tests)],
                ["Coverage run failed (mapping incomplete)", failed, pct(failed, scope_tests)],
                ["Skipped (no mapping)", skipped, pct(skipped, scope_tests)],
                ["No coverage outcome", notrun, pct(notrun, scope_tests)],
            ],
        )
    )

    s += ["", "### Failed and skipped P1 tests", ""]
    s.append("Reason recorded per test in `data/cov_status.tsv` (`detail`).\n")
    s.append(
        table(
            ["status", "package", "test", "detail"],
            [
                [st, f"`{pkg}`", f"`{name}`", (detail or "").replace("|", "\\|")]
                for st, pkg, name, detail in q(
                    "SELECT c.status, c.pkg, c.test, c.detail FROM unit_tests u"
                    " JOIN cov_status c ON c.pkg = u.pkg AND c.test = u.name"
                    " WHERE u.phase = 'P1' AND c.status IN ('failed', 'skipped')"
                    " GROUP BY c.pkg, c.test ORDER BY c.status, c.pkg, c.test"
                )
            ],
            ["l", "l", "l", "l"],
        )
    )

    rows = collections.defaultdict(lambda: [0, 0, 0, 0, 0])
    for kind, pkg, store, inscope, status in q(
        "SELECT t.kind, t.pkg, t.uses_store, s.phase IS NOT NULL, c.status FROM tests t"
        " LEFT JOIN scope s ON s.pkg = t.pkg LEFT JOIN cov_status c ON c.pkg = t.pkg AND c.test = t.name"
        " WHERE t.kind = 'unit'"
    ):
        r = rows[area(pkg)]
        r[0] += 1
        r[1] += store
        r[2] += inscope
        r[3] += status == "ok"
        r[4] += status == "failed"
    s += ["", "## Unit tests by area", ""]
    s.append(
        table(
            ["area", "tests", "SQL-level", "in P1", "mapped", "mapping incomplete"],
            [[f"`{a}`"] + v for a, v in sorted(rows.items(), key=lambda kv: (-kv[1][0], kv[0]))],
        )
    )

    f = ["# Features", "", head]
    f.append(
        "Features v0 are Go source files. A test links to a file with a weight: the file's share of the "
        "test's total specificity (sum of `ln(N / tests reaching the function)` over the functions it "
        "reaches). Shared plumbing such as session setup is reached by almost every test and scores near "
        "zero. `tests` below counts links with weight ≥ 0.05, i.e. tests for which the file is a main "
        "subject, not just touched.\n"
    )
    feat_rows = q(
        "SELECT f.module, f.feature, f.funcs, count(tf.test), round(sum(tf.weight), 1) FROM features f"
        " LEFT JOIN test_feature tf ON tf.feature = f.feature AND tf.weight >= 0.05"
        " GROUP BY f.feature"
    )
    by_area = collections.defaultdict(list)
    for mod, feat, funcs, n, mass in feat_rows:
        by_area[area(mod)].append((feat, funcs, n, mass or 0))
    for a in sorted(by_area):
        items = sorted(by_area[a], key=lambda x: (-x[2], x[0]))
        f += [f"## `{a}`", ""]
        f.append(
            table(
                ["feature (file)", "functions", "tests", "weight sum"],
                [[f"`{x[0]}`", x[1], x[2], x[3]] for x in items],
            )
        )
        f.append("")
    return {"summary.md": "\n".join(s) + "\n", "features.md": "\n".join(f).rstrip("\n") + "\n"}


def cmd_report(args):
    with tempfile.TemporaryDirectory() as d:
        out = render_reports(build(os.path.join(d, "t.db")))
    os.makedirs(REPORTS, exist_ok=True)
    for name, text in out.items():
        with open(os.path.join(REPORTS, name), "w", encoding="utf-8", newline="\n") as f:
            f.write(text)
        print(f"reports/{name}")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)
    e = sub.add_parser("export")
    e.add_argument("db")
    e.set_defaults(func=cmd_export)
    b = sub.add_parser("build")
    b.add_argument("-o", "--output", default=os.path.join(ROOT, "tracker.db"))
    b.set_defaults(func=cmd_build)
    sub.add_parser("report").set_defaults(func=cmd_report)
    sub.add_parser("check").set_defaults(func=cmd_check)
    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
