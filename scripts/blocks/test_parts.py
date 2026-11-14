#!/usr/bin/env python3
"""Tests for parts.py stub-registry checks. Run: python3 scripts/blocks/test_parts.py"""
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


class StubRegistry(unittest.TestCase):
    def setUp(self):
        # parts.py finds data/ relative to itself, so work on a copy of scripts/ and data/.
        self.tmp = tempfile.mkdtemp()
        for d in ("scripts", "data", "reports"):
            if os.path.isdir(os.path.join(ROOT, d)):
                shutil.copytree(os.path.join(ROOT, d), os.path.join(self.tmp, d))
        self.stubs = os.path.join(self.tmp, "data", "stubs.tsv")
        self.tidb_rs = os.path.join(self.tmp, "tidb-rs")
        os.makedirs(os.path.join(self.tidb_rs, "crates"))

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def parts(self, *args):
        return subprocess.run([sys.executable, os.path.join(self.tmp, "scripts", "blocks", "parts.py"), *args],
                              capture_output=True, text=True)

    def rows(self):
        with open(self.stubs, encoding="utf-8") as f:
            return f.read().splitlines()

    def write(self, lines):
        with open(self.stubs, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")

    def test_main_registry_passes(self):
        r = self.parts("check")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_empty_note_fails_check(self):
        lines = self.rows()
        f = lines[1].split("\t")
        f[4] = ""
        lines[1] = "\t".join(f)
        self.write(lines)
        r = self.parts("check")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("empty note", r.stdout + r.stderr)

    def test_duplicate_key_fails_check_and_scan_write(self):
        lines = self.rows()
        f = lines[1].split("\t")
        f[4] = f[4] + " (other side)"
        lines.insert(2, "\t".join(f))  # same key, different note, still sorted
        self.write(lines)
        r = self.parts("check")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("duplicate row", r.stdout + r.stderr)
        before = self.rows()
        r = self.parts("scan", "--write", self.tidb_rs)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("duplicate:", r.stdout)
        self.assertEqual(self.rows(), before, "scan --write must not rewrite the file")


if __name__ == "__main__":
    unittest.main()
