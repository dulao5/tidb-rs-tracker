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

-- Every function and method declared in the non-test .go files under pkg/,
-- reached by a test or not (cmd/inventory -funcs). symbol uses the naming of
-- features.symbols: join on go_funcs.file = features.feature.
CREATE TABLE go_funcs (
  pkg        TEXT NOT NULL,     -- package dir
  file       TEXT NOT NULL,     -- relative to the TiDB repo root
  symbol     TEXT NOT NULL,     -- Func or Recv.Method (no pointer, no type parameters)
  kind       TEXT NOT NULL,     -- func / method
  start_line INTEGER NOT NULL,  -- func keyword (doc comment excluded)
  end_line   INTEGER NOT NULL,  -- closing brace
  lines      INTEGER NOT NULL,  -- end_line - start_line + 1
  generated  INTEGER NOT NULL,  -- 1 = generated file ("Code generated ... DO NOT EDIT.", pkg/parser/parser.go)
  PRIMARY KEY (pkg, file, symbol, start_line)
);
CREATE INDEX go_funcs_by_symbol ON go_funcs(symbol);

-- Outcome of each unit test's coverage run (the failpoint re-run's, if any).
--   ok       the test passed; its mapping is complete. detail is set if it
--            reached no function beyond package init, or if the process
--            failed after the test passed (e.g. a goleak check in TestMain).
--   failed   failed, panicked or timed out: the mapping is a lower bound.
--   skipped  t.Skip (detail = message), or not compiled in with these tags.
CREATE TABLE cov_status (
  pkg    TEXT NOT NULL,
  test   TEXT NOT NULL,
  status TEXT NOT NULL,         -- ok / failed / skipped (see below)
  detail TEXT,                  -- reason: error, skip message, or a note on an ok run
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

-- R1: tidb-rs on TiDB's integration tests, one run (tidb-rs scripts/integrationtest.sh).
-- Every statement runs on Go TiDB (A); reads also run on tidb-rs (B) and are compared live.
CREATE TABLE rs_run (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE rs_integration (
  file           TEXT PRIMARY KEY,   -- test name, e.g. executor/aggregate
  statements     INTEGER NOT NULL,   -- = match + mismatch + unsupported + b_error + a_error + skipped + write
  reads          INTEGER NOT NULL,   -- statements classified as reads (SELECT/TABLE/VALUES/WITH, SHOW, EXPLAIN/DESC)
  match          INTEGER NOT NULL,   -- B answered like A (same rows, columns, error code)
  mismatch       INTEGER NOT NULL,   -- B answered differently (wrong rows, metadata, error code, warnings)
  unsupported    INTEGER NOT NULL,   -- B: error 1235 "not supported yet"
  b_error        INTEGER NOT NULL,   -- B failed otherwise while A succeeded
  a_error        INTEGER NOT NULL,   -- A failed without --error (any statement); not scored
  skipped        INTEGER NOT NULL,   -- read not sent to B (in a transaction, local temporary table, ...)
  write          INTEGER NOT NULL,   -- non-read statements (A only; session statements mirrored to B)
  b_set_rejected INTEGER NOT NULL    -- mirrored session statements (SET, USE, PREPARE) B rejected
);

-- Outcome reasons: B's unsupported message, B's error, mismatch kind, A's error, skip reason
-- (names and numbers replaced by ? and N).
CREATE TABLE rs_integration_reason (
  class      TEXT NOT NULL,          -- select / show / explain / session / write
  outcome    TEXT NOT NULL,
  reason     TEXT NOT NULL,          -- "-" when there is none (match, plain write)
  statements INTEGER NOT NULL,
  files      INTEGER NOT NULL,
  PRIMARY KEY (class, outcome, reason)
);

-- Every mismatch. kind: rows (wrong values or row count), order, columns (names/count),
-- metadata (types, flags, ...), error (different error, or B answered where A failed),
-- shape (result set vs OK), warnings.
CREATE TABLE rs_integration_mismatch (
  file   TEXT NOT NULL,
  line   INTEGER NOT NULL,
  conn   TEXT NOT NULL,
  class  TEXT NOT NULL,
  kind   TEXT NOT NULL,
  b_rejected INTEGER NOT NULL,     -- session statements B had rejected earlier in this connection
  detail TEXT NOT NULL,
  sql    TEXT NOT NULL,
  PRIMARY KEY (file, line, conn)
);
