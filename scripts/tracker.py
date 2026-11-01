#!/usr/bin/env python3
"""Tracker data tool. Python 3.9+, standard library only.

  tracker.py export <extraction.db>   extraction DB (scripts/load-raw.sh) -> data/*.tsv
  tracker.py build [-o tracker.db]    data/*.tsv -> SQLite (for Datasette / ad-hoc SQL)
  tracker.py report                   data/*.tsv -> reports/*.md
  tracker.py check                    validate data/, and fail if reports/ is stale
  tracker.py import-integration <dir> tidb-rs `scripts/integrationtest.sh` output -> data/rs_*.tsv
                                      (statement detail -> work/rs_statements.tsv, not committed)

data/ is the source of truth. Every file is a sorted TSV with a header row, so
a re-extraction shows up as a reviewable git diff. Row order and keys never
depend on SQLite row ids.
"""

import argparse
import collections
import json
import os
import re
import shutil
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
    # Every function declared in a non-test .go file under pkg/ (cmd/inventory -funcs).
    # Duplicate symbols in one file are possible (several init funcs).
    "go_funcs.tsv": (
        ["pkg", "file", "symbol", "kind", "start_line", "end_line", "lines", "generated"],
        ["pkg", "file", "symbol", "start_line"],
    ),
    "cov_status.tsv": (["pkg", "test", "status", "detail"], ["pkg", "test"]),
    "test_feature.tsv": (["pkg", "test", "feature", "weight"], ["pkg", "test", "feature"]),
    # R1: tidb-rs on TiDB's integration tests (tidb-rs `scripts/integrationtest.sh`).
    "rs_run.tsv": (["key", "value"], ["key"]),
    "rs_integration.tsv": (
        ["file", "statements", "reads", "match", "mismatch", "unsupported", "b_error", "a_error",
         "skipped", "write", "b_set_rejected"],
        ["file"],
    ),
    "rs_integration_reason.tsv": (
        ["class", "outcome", "reason", "statements", "files"], ["class", "outcome", "reason"]
    ),
    "rs_integration_mismatch.tsv": (
        ["file", "line", "conn", "class", "kind", "b_rejected", "detail", "sql"], ["file", "line", "conn"]
    ),
}

# Statement outcomes of the integration run. The first four are the scored read outcomes.
RS_SCORED = ["match", "mismatch", "unsupported", "b_error"]
RS_OUTCOMES = RS_SCORED + ["a_error", "skipped", "write"]
RS_STATEMENTS = os.path.join(ROOT, "work", "rs_statements.tsv")


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
    out["go_funcs.tsv"] = q(
        "SELECT pkg, file, name, kind, start_line, end_line, lines, generated FROM go_func_decl"
    ).fetchall()
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


# ---------------------------------------------------------------- import-integration


def _norm(text):
    """Groups messages that differ only in names and numbers."""
    text = re.sub(r"(?<![A-Za-z])'[^']*'", "?", text)
    text = re.sub(r"`[^`]*`", "?", text)
    text = re.sub(r"\b\d+(\.\d+)?\b", "N", text)
    text = re.sub(r"\s+", " ", text).strip()
    return text[:120]


def rs_reason(outcome, kind, detail):
    """Short reason for a statement outcome (rs_integration_reason.tsv); "" = none."""
    if outcome == "match":
        return ""
    if outcome == "unsupported":
        if kind == "login":
            return "B cannot log in as the test's user (no privilege system)"
        m = re.search(r"doesn't yet support '(.*)'", detail)
        return _norm(m.group(1) if m else detail)
    if outcome == "mismatch":
        return kind
    if outcome == "b_error":
        return f"{kind}: {_norm(detail)}" if kind.isdigit() or kind == "client" else _norm(detail)
    if outcome == "a_error":
        code, sep, msg = detail.partition(": ")
        return f"{code}: {_norm(msg)}" if sep and (code.isdigit() or code == "client") else _norm(detail)
    return kind  # skipped: why; write: "session" or ""


def cmd_import_integration(args):
    src = args.dir
    def rows(name):
        with open(os.path.join(src, name), encoding="utf-8") as f:
            header = f.readline().rstrip("\n").split("\t")
            return [dict(zip(header, line.rstrip("\n").split("\t"))) for line in f]
    out = {}
    out["rs_run.tsv"] = [(r["key"], r["value"]) for r in rows("run.tsv")]
    cols = TABLES["rs_integration.tsv"][0]
    out["rs_integration.tsv"] = [[r[c] for c in cols] for r in rows("files.tsv")]
    stmts = rows("statements.tsv")
    # Session statements B had rejected in the connection before each mismatch.
    rejected = {}
    with open(os.path.join(src, "mismatches.jsonl"), encoding="utf-8") as f:
        for line in f:
            if not line.endswith("\n"):
                break  # a run that is still writing
            m = json.loads(line)
            rejected[(m["file"], str(m["line"]), m["conn"])] = len(m.get("b_rejected_session_statements", []))
    reasons = collections.defaultdict(lambda: [0, set()])
    mism = []
    for r in stmts:
        reason = rs_reason(r["outcome"], r["kind"], r["detail"]) or "-"
        e = reasons[(r["class"], r["outcome"], reason)]
        e[0] += 1
        e[1].add(r["file"])
        if r["outcome"] == "mismatch":
            n_rej = rejected.get((r["file"], r["line"], r["conn"]), 0)
            mism.append([r["file"], r["line"], r["conn"], r["class"], r["kind"], n_rej, r["detail"][:300] or "-", r["sql"][:500]])
    out["rs_integration_reason.tsv"] = [[c, o, why, n, len(f)] for (c, o, why), (n, f) in reasons.items()]
    out["rs_integration_mismatch.tsv"] = mism
    for name, data in out.items():
        cols, key = TABLES[name]
        idx = [cols.index(k) for k in key]
        data = sorted(data, key=lambda r: [str(r[i]) for i in idx])
        write_tsv(os.path.join(DATA, name), cols, data)
        print(f"{name}: {len(data)} rows")
    os.makedirs(os.path.dirname(RS_STATEMENTS), exist_ok=True)
    shutil.copyfile(os.path.join(src, "statements.tsv"), RS_STATEMENTS)
    print(f"{os.path.relpath(RS_STATEMENTS, ROOT)}: {len(stmts)} statements (not committed)")


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
    errors += check_go_funcs(data)
    errors += check_integration(data)
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


def check_go_funcs(data):
    """go_funcs.tsv: well-formed rows, and every feature symbol is a declared function."""
    errors = []
    cols, rows = data["go_funcs.tsv"]
    decl = set()
    for r in rows:
        v = dict(zip(cols, r))
        where = f"go_funcs.tsv: {v['file']}:{v['start_line']}"
        try:
            start, end, n = int(v["start_line"]), int(v["end_line"]), int(v["lines"])
        except ValueError:
            errors.append(f"{where}: non-integer line numbers")
            continue
        if v["kind"] not in ("func", "method") or ("." in v["symbol"]) != (v["kind"] == "method"):
            errors.append(f"{where}: kind {v['kind']} does not fit symbol {v['symbol']}")
        if v["generated"] not in ("0", "1"):
            errors.append(f"{where}: generated must be 0 or 1")
        if not (0 < start <= end and n == end - start + 1):
            errors.append(f"{where}: lines != end_line - start_line + 1")
        if not v["file"].endswith(".go") or v["file"].endswith("_test.go") or v["file"].rsplit("/", 1)[0] != v["pkg"]:
            errors.append(f"{where}: file is not a non-test .go file of pkg {v['pkg']}")
        decl.add((v["file"], v["symbol"]))
    if rows:
        for feat, _, _, syms in data["features.tsv"][1]:
            missing = [s for s in syms.split() if (feat, s) not in decl]
            if missing:
                errors.append(f"features.tsv: {feat}: {len(missing)} symbols not in go_funcs.tsv, e.g. {missing[0]}")
    return errors[:50]


def check_integration(data):
    """R1 tables: counts add up, files are known integration tests, reasons match files."""
    errors = []
    integ = {r[3] for r in data["tests.tsv"][1] if r[0] == "integration"}
    cols, rows = data["rs_integration.tsv"]
    totals = collections.Counter()
    for r in rows:
        v = dict(zip(cols, r))
        try:
            n = {c: int(v[c]) for c in cols[1:]}
        except ValueError:
            errors.append(f"rs_integration.tsv: non-integer count in {v['file']}")
            continue
        if f"tests/integrationtest/t/{v['file']}.test" not in integ:
            errors.append(f"rs_integration.tsv: {v['file']} is not an integration test in tests.tsv")
        if n["statements"] != sum(n[o] for o in RS_OUTCOMES):
            errors.append(f"rs_integration.tsv: {v['file']}: statements != sum of outcomes")
        if sum(n[o] for o in RS_SCORED) > n["reads"]:
            errors.append(f"rs_integration.tsv: {v['file']}: more scored reads than reads")
        totals.update(n)
    rcols, rrows = data["rs_integration_reason.tsv"]
    by_outcome = collections.Counter()
    for r in rrows:
        v = dict(zip(rcols, r))
        if v["outcome"] not in RS_OUTCOMES:
            errors.append(f"rs_integration_reason.tsv: unknown outcome {v['outcome']}")
        by_outcome[v["outcome"]] += int(v["statements"])
    for o in RS_OUTCOMES:
        if by_outcome[o] != totals[o]:
            errors.append(f"rs_integration_reason.tsv: {o} sums to {by_outcome[o]}, rs_integration.tsv says {totals[o]}")
    n_mism = len(data["rs_integration_mismatch.tsv"][1])
    if n_mism != totals["mismatch"]:
        errors.append(f"rs_integration_mismatch.tsv: {n_mism} rows, rs_integration.tsv says {totals['mismatch']} mismatches")
    if rows and not data["rs_run.tsv"][1]:
        errors.append("rs_run.tsv is empty but rs_integration.tsv has rows")
    return errors


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
    return {
        "summary.md": "\n".join(s) + "\n",
        "features.md": "\n".join(f).rstrip("\n") + "\n",
        "integration.md": render_integration(q),
    }


def _cell(text, n=100):
    """A TSV field (escaped \\n, \\t) as a Markdown table cell."""
    text = text.replace("\\n", " ").replace("\\t", " ").replace("|", "\\|")
    text = re.sub(r"\s+", " ", text).strip()
    return text if len(text) <= n else text[: n - 1] + "…"


def render_integration(q):
    """reports/integration.md: R1, tidb-rs on TiDB's integration tests."""
    run = dict(q("SELECT key, value FROM rs_run"))
    o = ["# tidb-rs on TiDB's integration tests (R1)", "", "<!-- Generated by scripts/tracker.py report. Do not edit. -->", ""]
    if not run:
        o.append("No run imported yet (`scripts/tracker.py import-integration`).")
        return "\n".join(o) + "\n"
    o.append(
        f"Run: tidb-rs `{run.get('tidb_rs_commit', '?')[:10]}`"
        + (" (with uncommitted changes)" if run.get("tidb_rs_dirty") == "yes" else "")
        + f" against Go TiDB {run.get('tidb_version', '?')}, test files from TiDB `{run.get('tests_commit', '?')[:10]}`,"
        f" {run.get('date', '?')} on {run.get('runner', '?')}, {int(run.get('seconds', 0)) // 60} min.\n"
    )
    o.append(
        "Each `.test` file runs in hybrid mode on one tiup playground: every statement runs on Go TiDB (A) "
        "in the file's sessions, as mysql-tester would run it; read statements (SELECT/TABLE/VALUES/WITH, "
        "SHOW, EXPLAIN/DESC) also run on tidb-rs (B), in B sessions that mirror A's database and session "
        "statements, and B's answer is compared with A's on the same data. Rows (as multisets unless the "
        "query has a top-level ORDER BY and no `--sorted_result`), column metadata, error codes, and with "
        "`--enable_warnings` the warnings must be equal. A is the reference, not the `.result` files (those "
        "are recorded on TiDB master). Details: tidb-rs `tools/ab/README.md`.\n"
    )
    tot = dict(zip(
        ["files", "statements", "reads"] + RS_OUTCOMES + ["b_set_rejected"],
        q("SELECT count(*), sum(statements), sum(reads), " + ", ".join(f"sum({c})" for c in RS_OUTCOMES)
          + ", sum(b_set_rejected) FROM rs_integration")[0],
    ))
    scored = sum(tot[c] for c in RS_SCORED)
    o += ["## Overall", ""]
    o.append(
        "Score: the share of scored reads (match + mismatch + unsupported + b_error) that match. "
        "Reads where A itself failed unexpectedly (`a_error`, mostly features of TiDB master missing in "
        "the release) and reads not sent to B (`skipped`) are not scored.\n"
    )
    o.append(table(["", "statements", "share"], [
        ["Files", tot["files"], ""],
        ["Statements", tot["statements"], ""],
        ["… reads", tot["reads"], pct(tot["reads"], tot["statements"])],
        ["… scored reads", scored, pct(scored, tot["reads"])],
        ["**match**", f"**{tot['match']}**", f"**{pct(tot['match'], scored)}**"],
        ["mismatch", tot["mismatch"], pct(tot["mismatch"], scored)],
        ["unsupported (B: 1235)", tot["unsupported"], pct(tot["unsupported"], scored)],
        ["b_error (B failed otherwise)", tot["b_error"], pct(tot["b_error"], scored)],
        ["a_error (A failed; any statement)", tot["a_error"], ""],
        ["skipped reads", tot["skipped"], ""],
        ["write (A only)", tot["write"], ""],
        ["… session statements B rejected", tot["b_set_rejected"], ""],
    ]))

    o += ["", "## By statement class", ""]
    cls = collections.defaultdict(collections.Counter)
    for c, out, n in q("SELECT class, outcome, sum(statements) FROM rs_integration_reason GROUP BY class, outcome"):
        cls[c][out] += n
    rows = []
    for c in ["select", "show", "explain"]:
        k = cls.get(c, collections.Counter())
        sc = sum(k[x] for x in RS_SCORED)
        rows.append([c, sum(k.values()), sc] + [k[x] for x in RS_SCORED] + [pct(k["match"], sc), k["a_error"], k["skipped"]])
    o.append(table(["class", "statements", "scored", "match", "mismatch", "unsupported", "b_error", "match share", "a_error", "skipped"], rows))

    o += ["", "## Mismatches", ""]
    o.append(
        "Every statement where B answered differently from A. `rows`: wrong values or row count; `order`: "
        "same rows, different order under ORDER BY; `columns`: column names or count; `metadata`: other "
        "column metadata (type, length, flags, charset); `error`: a different error code, or B answered "
        "where A failed; `shape`: result set vs OK; `warnings`: different SHOW WARNINGS under "
        "`--enable_warnings`. `B rej.`: session statements (SET, USE, ...) B had rejected earlier in "
        "that connection, so its session may differ from A's (e.g. SET NAMES gbk).\n"
    )
    kinds = q("SELECT kind, count(*) FROM rs_integration_mismatch GROUP BY kind ORDER BY count(*) DESC, kind")
    if kinds:
        o.append(table(["kind", "statements"], kinds))
        o.append("")
        o.append(table(
            ["file:line", "kind", "B rej.", "statement", "difference"],
            [[f"`{f}:{l}`" + ("" if c == "default" else f" ({c})"), k, rj, f"`{_cell(sq, 90)}`", _cell(d, 160)]
             for f, l, c, k, rj, d, sq in q("SELECT file, line, conn, kind, b_rejected, detail, sql FROM rs_integration_mismatch ORDER BY file, line")],
            ["l", "l", "r", "l", "l"],
        ))
    else:
        o.append("None.")

    def reasons(outcome, limit, title, note):
        rows = q(
            "SELECT reason, sum(statements), count(DISTINCT class), sum(files) FROM rs_integration_reason"
            " WHERE outcome = ? GROUP BY reason ORDER BY sum(statements) DESC, reason LIMIT ?", outcome, limit)
        total = q("SELECT count(DISTINCT reason) FROM rs_integration_reason WHERE outcome = ?", outcome)[0][0]
        o.extend(["", f"## {title}", "", note + f" Top {min(limit, total)} of {total} reasons.\n"])
        o.append(table(["reason", "statements"], [[_cell(r or "-", 120), n] for r, n, _, _ in rows], ["l", "r"]))

    reasons("unsupported", 40, "Top reasons: unsupported", "B's `ER_NOT_SUPPORTED_YET` message (names replaced by ?, numbers by N).")
    reasons("b_error", 25, "Top reasons: b_error", "B's error code and message while A succeeded.")
    reasons("a_error", 25, "Top reasons: a_error", "A's own failures (statements of any class without `--error`). Mostly TiDB-master features missing in the release under test.")
    reasons("skipped", 10, "Skipped reads", "Reads that were not sent to B.")

    o += ["", "## Per file", ""]
    o.append("`share` = match / scored reads.\n")
    rows = []
    for r in q("SELECT file, statements, reads, " + ", ".join(RS_OUTCOMES) + " FROM rs_integration ORDER BY file"):
        f, st, rd = r[:3]
        k = dict(zip(RS_OUTCOMES, r[3:]))
        sc = sum(k[x] for x in RS_SCORED)
        rows.append([f"`{f}`", st, rd, sc, k["match"], pct(k["match"], sc), k["mismatch"], k["unsupported"], k["b_error"], k["a_error"], k["skipped"]])
    o.append(table(["file", "statements", "reads", "scored", "match", "share", "mismatch", "unsupported", "b_error", "a_error", "skipped"], rows))
    return "\n".join(o) + "\n"


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
    i = sub.add_parser("import-integration")
    i.add_argument("dir")
    i.set_defaults(func=cmd_import_integration)
    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
