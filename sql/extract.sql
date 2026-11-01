-- Test -> feature -> task tracking DB for the Rust TiDB port.
-- SQLite for now (decision D4 pending); keep to portable SQL so it can move to TiDB.

CREATE TABLE IF NOT EXISTS module (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,     -- e.g. parser, planner/core
  go_pkg     TEXT NOT NULL UNIQUE,     -- path relative to tidb repo root, e.g. pkg/parser
  rust_crate TEXT,
  phase      TEXT                      -- P1 / P2 / ... / NA
);

CREATE TABLE IF NOT EXISTS feature (
  id          INTEGER PRIMARY KEY,
  module_id   INTEGER NOT NULL REFERENCES module(id),
  name        TEXT NOT NULL,
  description TEXT,
  go_symbols  TEXT,                    -- JSON array of Go func/method symbols
  status      TEXT NOT NULL DEFAULT 'todo',  -- todo / doing / done / na
  UNIQUE (module_id, name)
);

CREATE TABLE IF NOT EXISTS go_test (
  id           INTEGER PRIMARY KEY,
  kind         TEXT NOT NULL,          -- unit / integration / e2e
  repo         TEXT NOT NULL,          -- tidb / client-go / ...
  pkg          TEXT NOT NULL,          -- package dir (unit) or suite dir (integration)
  path         TEXT NOT NULL,          -- file path relative to repo root
  name         TEXT NOT NULL,          -- TestXxx or .test file stem
  line         INTEGER,
  subtests     INTEGER NOT NULL DEFAULT 0,  -- literal t.Run count (lower bound)
  uses_store   INTEGER NOT NULL DEFAULT 0,  -- builds a mock store / testkit -> SQL-level test
  uses_fp      INTEGER NOT NULL DEFAULT 0,  -- uses failpoints
  stmts        INTEGER,                -- integration: number of SQL statements
  base_commit  TEXT NOT NULL,
  UNIQUE (repo, path, name)
);

-- Filled from per-test coverage runs.
CREATE TABLE IF NOT EXISTS test_feature (
  test_id    INTEGER NOT NULL REFERENCES go_test(id),
  feature_id INTEGER NOT NULL REFERENCES feature(id),
  weight     REAL,
  PRIMARY KEY (test_id, feature_id)
);

CREATE TABLE IF NOT EXISTS rust_test (
  id         INTEGER PRIMARY KEY,
  go_test_id INTEGER REFERENCES go_test(id),
  path       TEXT NOT NULL,
  name       TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'todo',  -- todo / ported / passing / skipped
  UNIQUE (path, name)
);

CREATE TABLE IF NOT EXISTS test_run (
  id          INTEGER PRIMARY KEY,
  rust_commit TEXT NOT NULL,
  test_id     INTEGER NOT NULL REFERENCES go_test(id),
  result      TEXT NOT NULL,           -- pass / fail / skip / error
  duration_ms INTEGER,
  at          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS upstream_commit (
  sha          TEXT PRIMARY KEY,
  at           TEXT NOT NULL,
  touched_pkgs TEXT,                   -- JSON array
  feature_ids  TEXT,                   -- JSON array
  triaged      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS task (
  id         INTEGER PRIMARY KEY,
  feature_id INTEGER REFERENCES feature(id),
  raft_task  TEXT,                     -- e.g. "task #12"
  owner      TEXT,
  status     TEXT NOT NULL DEFAULT 'todo'
);

-- Raw per-test coverage (cmd/covmap): which non-test functions a test reaches,
-- net of package-init coverage. Features are defined as groups of go_func.
CREATE TABLE IF NOT EXISTS go_func (
  id   INTEGER PRIMARY KEY,
  file TEXT NOT NULL,                  -- relative to repo root
  name TEXT NOT NULL,                  -- Func or Recv.Method
  UNIQUE (file, name)
);

CREATE TABLE IF NOT EXISTS test_func (
  test_id INTEGER NOT NULL REFERENCES go_test(id),
  func_id INTEGER NOT NULL REFERENCES go_func(id),
  PRIMARY KEY (test_id, func_id)
);

-- Outcome of each test's coverage run. A test that failed stopped early, so
-- its test_func rows are a lower bound: treat its feature mapping as
-- incomplete. skipped = t.Skip or not compiled in. See sql/tracker.sql.
CREATE TABLE IF NOT EXISTS cov_status (
  test_id INTEGER PRIMARY KEY REFERENCES go_test(id),
  status  TEXT NOT NULL,               -- ok / failed / skipped
  detail  TEXT
);

-- Every function and method declared in the non-test .go files under pkg/
-- (cmd/inventory -funcs), reached by a test or not. name joins go_func.name.
CREATE TABLE IF NOT EXISTS go_func_decl (
  pkg        TEXT NOT NULL,            -- package dir relative to repo root
  file       TEXT NOT NULL,            -- relative to repo root
  name       TEXT NOT NULL,            -- Func or Recv.Method
  kind       TEXT NOT NULL,            -- func / method
  start_line INTEGER NOT NULL,         -- func keyword
  end_line   INTEGER NOT NULL,         -- closing brace
  lines      INTEGER NOT NULL,         -- end_line - start_line + 1
  generated  INTEGER NOT NULL,         -- 1 = generated file (Code generated ... DO NOT EDIT., parser.go)
  PRIMARY KEY (file, name, start_line)
);
