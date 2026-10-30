-- Published tracker DB, built from data/*.tsv by `scripts/tracker.py build`.
-- Natural keys only: no surrogate ids, so a query result does not change when
-- the data is re-extracted unless the data itself changed.
-- (The extraction pipeline uses its own schema, sql/extract.sql.)

CREATE TABLE source (key TEXT PRIMARY KEY, value TEXT NOT NULL);

-- Packages in scope for a tidb-rs phase (P1 = prototype). Maintained by hand.
CREATE TABLE scope (pkg TEXT PRIMARY KEY, phase TEXT NOT NULL);

CREATE TABLE tests (
  kind       TEXT NOT NULL,     -- unit / integration
  pkg        TEXT NOT NULL,     -- Go package dir, or suite dir for integration
  name       TEXT NOT NULL,     -- TestXxx, or .test file stem
  path       TEXT NOT NULL,     -- relative to the TiDB repo root
  line       INTEGER,
  subtests   INTEGER NOT NULL,  -- literal t.Run calls (lower bound)
  uses_store INTEGER NOT NULL,  -- 1 = SQL-level test (mock store / testkit)
  uses_fp    INTEGER NOT NULL,  -- 1 = uses failpoints
  stmts      INTEGER,           -- integration: SQL statements in the file
  PRIMARY KEY (kind, pkg, name, path)
);

-- v0: one feature per Go source file; symbols = space-separated functions.
CREATE TABLE features (
  feature TEXT PRIMARY KEY,
  module  TEXT NOT NULL,
  funcs   INTEGER NOT NULL,
  symbols TEXT NOT NULL
);

-- Outcome of each unit test's coverage run.
CREATE TABLE cov_status (
  pkg    TEXT NOT NULL,
  test   TEXT NOT NULL,
  status TEXT NOT NULL,         -- ok / failed (failed = mapping incomplete)
  detail TEXT,
  PRIMARY KEY (pkg, test)
);

-- weight = the feature's share of the test's specificity mass (see reports/features.md).
CREATE TABLE test_feature (
  pkg     TEXT NOT NULL,
  test    TEXT NOT NULL,
  feature TEXT NOT NULL REFERENCES features(feature),
  weight  REAL NOT NULL,
  PRIMARY KEY (pkg, test, feature)
);
CREATE INDEX test_feature_by_feature ON test_feature(feature, weight);

-- One row per unit test with its scope and mapping state.
CREATE VIEW unit_tests AS
SELECT t.pkg, t.name, t.path, t.line, t.uses_store, t.uses_fp,
       s.phase, COALESCE(c.status, 'not run') AS coverage,
       (SELECT count(*) FROM test_feature tf WHERE tf.pkg = t.pkg AND tf.test = t.name) AS features
FROM tests t
LEFT JOIN scope s ON s.pkg = t.pkg
LEFT JOIN cov_status c ON c.pkg = t.pkg AND c.test = t.name
WHERE t.kind = 'unit';
