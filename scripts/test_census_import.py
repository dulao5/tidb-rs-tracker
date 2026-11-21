#!/usr/bin/env python3
"""Tests of the census import ratchet (`tracker.py import-integration`, tidb-rs #260).

Each test runs tracker.py on a scratch copy of the repository (scripts, sql, data, reports), so
the real data/ is never touched. Run by CI: `python3 scripts/test_census_import.py`.
"""

import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CENSUS_COLS = ["layer", "expected", "total", "new_stack", "fallback", "error", "skipped",
               "new_stack_equal", "negative_control", "status"]


def integration_tests():
    with open(os.path.join(ROOT, "data", "tests.tsv"), encoding="utf-8") as f:
        next(f)
        paths = [l.split("\t")[3] for l in f if l.startswith("integration\t")]
    return sorted(p[len("tests/integrationtest/t/"):-len(".test")] for p in paths)


def write_tsv(path, header, rows):
    with open(path, "w", encoding="utf-8") as f:
        f.write("\t".join(header) + "\n")
        for r in rows:
            f.write("\t".join(str(c) for c in r) + "\n")


def fake_run(d, files, new_stack=0, equal=0, census=True):
    """A scripts/integrationtest.sh output dir: one write statement per file, except `new_stack`
    reads on the new stack (the first `equal` of them match)."""
    os.makedirs(d)
    write_tsv(f"{d}/run.tsv", ["key", "value"], [("tidb_rs_commit", "0" * 40), ("date", "2026-11-21")])
    frows, srows = [], []
    for i, f in enumerate(files):
        if i < new_stack:
            outcome = "match" if i < equal else "unsupported"
            k = {"match": 0, "unsupported": 0, outcome: 1}
            frows.append([f, 1, 1, k["match"], 0, k["unsupported"], 0, 0, 0, 0, 0])
            srows.append([f, 1, "default", "select", outcome, "", "", 1, "select 1"])
        else:
            frows.append([f, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0])
            srows.append([f, 1, "default", "write", "write", "", "", 1, "insert"])
    write_tsv(f"{d}/files.tsv", ["file", "statements", "reads", "match", "mismatch", "unsupported", "b_error",
                                 "a_error", "skipped", "write", "b_set_rejected"], frows)
    write_tsv(f"{d}/statements.tsv", ["file", "line", "conn", "class", "outcome", "kind", "detail", "ms", "sql"], srows)
    open(f"{d}/mismatches.jsonl", "w").close()
    if census:
        n, skipped = len(files), len(files) - new_stack
        write_tsv(f"{d}/census-summary.tsv", CENSUS_COLS,
                  [["integrationtest", n, n, new_stack, 0, 0, skipped, equal, "failed as required", "ok"]])
        reasons = [["integrationtest", "skipped", "write", skipped]]
        if new_stack:
            reasons.append(["integrationtest", "new_stack", "ok", new_stack])
        write_tsv(f"{d}/census-reasons.tsv", ["layer", "path", "reason", "count"], reasons)


class CensusImport(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.repo = os.path.join(self.tmp, "repo")
        for d in ("scripts", "sql", "data", "reports"):
            shutil.copytree(os.path.join(ROOT, d), os.path.join(self.repo, d))
        # `report` reads the reference commit from govec/go.mod (#106).
        os.makedirs(os.path.join(self.repo, "govec"))
        shutil.copy(os.path.join(ROOT, "govec", "go.mod"), os.path.join(self.repo, "govec", "go.mod"))
        self.files = integration_tests()
        self.n = 0

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def run_tracker(self, *args):
        p = subprocess.run([sys.executable, os.path.join(self.repo, "scripts", "tracker.py"), *args],
                           capture_output=True, text=True)
        return p.returncode, p.stdout + p.stderr

    def imp(self, *flags, **run):
        self.n += 1
        d = os.path.join(self.tmp, f"run{self.n}")
        fake_run(d, **{"files": self.files, **run})
        rc, out = self.run_tracker("import-integration", d, *flags)
        if rc == 0:
            self.assertEqual(self.run_tracker("report")[0], 0)
            rc2, out2 = self.run_tracker("check")
            self.assertEqual(rc2, 0, out2)
        return rc, out

    def census(self):
        with open(os.path.join(self.repo, "data", "rs_census.tsv"), encoding="utf-8") as f:
            return f.read()

    def test_full_run_sets_the_baseline(self):
        self.assertEqual(self.imp(new_stack=3, equal=2)[0], 0)
        self.assertIn("integrationtest\t%d\t%d\t3\t" % (len(self.files), len(self.files)), self.census())

    def test_no_census_keeps_the_baseline_and_the_ratchet(self):
        self.assertEqual(self.imp(new_stack=3, equal=2)[0], 0)
        before = self.census()
        rc, out = self.imp("--no-census", census=False)
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.census(), before)  # never emptied
        # The next run with less coverage is still refused: the baseline survived.
        rc, out = self.imp(new_stack=3, equal=1)
        self.assertNotEqual(rc, 0)
        self.assertIn("new-stack coverage 1 < 2", out)

    def data(self):
        out = {}
        for n in ("rs_census.tsv", "rs_integration.tsv", "rs_run.tsv"):
            with open(os.path.join(self.repo, "data", n), encoding="utf-8") as f:
                out[n] = f.read()
        return out

    def test_fewer_files_without_partial_is_refused(self):
        self.assertEqual(self.imp(new_stack=3, equal=2)[0], 0)
        before = self.data()
        rc, out = self.imp(files=self.files[:10], new_stack=3, equal=2)
        self.assertNotEqual(rc, 0)
        self.assertIn(f"not a full run: {len(self.files) - 10} test files of tests.tsv missing", out)
        self.assertEqual(self.data(), before)

    def test_same_count_other_names_is_refused(self):
        rc, out = self.imp(files=self.files[1:] + ["no/such_test"])
        self.assertNotEqual(rc, 0)
        self.assertIn("1 not in tests.tsv (no/such_test)", out)
        self.assertIn(f"({self.files[0]})", out)

    def test_partial_run_imports_nothing(self):
        self.assertEqual(self.imp(new_stack=3, equal=2)[0], 0)
        before = self.data()
        rc, out = self.imp("--partial", files=self.files[:10], new_stack=3, equal=1)
        self.assertEqual(rc, 0, out)
        self.assertIn(f"partial run (10 of {len(self.files)} test files", out)
        self.assertIn("census integrationtest: 10 statements, new_stack 3 (equal to Go 1)", out)
        self.assertEqual(self.data(), before)

    def test_accepting_a_regression_needs_a_reason(self):
        self.assertEqual(self.imp(new_stack=3, equal=2)[0], 0)
        self.assertNotEqual(self.imp("--accept-regression", " ", new_stack=3, equal=1)[0], 0)
        rc, out = self.imp("--accept-regression", "flaky test removed upstream", new_stack=3, equal=1)
        self.assertEqual(rc, 0, out)
        with open(os.path.join(self.repo, "reports", "integration.md"), encoding="utf-8") as f:
            self.assertIn("**Regression accepted** at import: flaky test removed upstream", f.read())

    def test_without_census_needs_the_flag(self):
        self.assertNotEqual(self.imp(census=False)[0], 0)


if __name__ == "__main__":
    unittest.main()
