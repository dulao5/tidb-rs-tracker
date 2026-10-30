# tidb-rs-tracker

Test inventory and progress tracking for [tidb-rs](https://github.com/dulao5/tidb-rs), a Rust rewrite of the TiDB SQL layer.

It answers three questions:

1. **What does TiDB test?** Every Go unit test and integration test file in TiDB.
2. **What does each test exercise?** A per-test coverage map from tests to Go functions, grouped into features.
3. **How far is tidb-rs?** Which of those tests and features tidb-rs passes (not yet collected, see R1 below).

The data covers TiDB `master` at the commit in [`data/source.tsv`](data/source.tsv).

## Reports

- [Summary](reports/summary.md): test counts, coverage-mapping progress, tests by area.
- [Features](reports/features.md): each feature and how many tests exercise it as a main subject.

Reports are generated from `data/` and CI fails if they are out of date.

## Browse the data

```sh
python3 scripts/tracker.py build          # data/*.tsv -> tracker.db
uvx datasette tracker.db -m datasette/metadata.json
```

Datasette opens in the browser with filtering, faceting and SQL, plus canned queries: tests for a feature, features of a test, and tests whose mapping is incomplete. `tracker.db` is also attached to every CI run as an artifact.

## Layout

| path | content |
| --- | --- |
| `data/` | Source of truth: sorted TSV files with a header row. |
| `reports/` | Markdown generated from `data/`. Do not edit. |
| `sql/tracker.sql` | Schema of `tracker.db` built from `data/`. |
| `sql/extract.sql`, `sql/derive_features.sql` | Schema and feature derivation of the extraction DB. |
| `cmd/inventory` | Go AST scan of a TiDB checkout: unit tests and integration test files. |
| `cmd/covmap` | Per-test coverage for one Go package. |
| `scripts/` | `tracker.py` (export / build / report / check) and the extraction shell scripts. |
| `datasette/` | Datasette table descriptions and canned queries. |

### Data files

| file | key | rows |
| --- | --- | --- |
| `source.tsv` | key | TiDB commit, extraction environment, raw coverage asset and its sha256. |
| `scope.tsv` | pkg | Packages in scope for a tidb-rs phase (`P1` = prototype). Maintained by hand. |
| `tests.tsv` | kind, pkg, name, path | Unit tests (`TestXxx`) and integration test files, with flags: SQL-level (`uses_store`), failpoints (`uses_fp`). |
| `features.tsv` | feature | v0 features: one per Go source file, with the functions in it that some test reaches. |
| `cov_status.tsv` | pkg, test | Outcome of each test's coverage run. `failed` means the mapping is incomplete. |
| `test_feature.tsv` | pkg, test, feature | Which features a test exercises, with a weight (see below). |

### Why text files and not a database in git

A re-extraction must be reviewable. With sorted TSV, `git diff` shows exactly which tests appeared or disappeared and which mappings moved; a SQLite file (even through Git LFS) cannot be diffed or merged. SQLite is a local cache, rebuilt in about a second.

The raw coverage (17M test→function rows, 1.9 GB) is too large for git. It is published as a release asset (71 MB, zstd) named in `source.tsv` with its sha256; `scripts/fetch-raw.sh` downloads and verifies it.

### Feature weights

A test reaches thousands of functions, most of them shared plumbing (session setup, bootstrap, planner entry). Each function gets a specificity `idf = ln(N / n)`, where `N` is the number of mapped tests and `n` the number that reach the function. Plumbing reached by almost every test scores near zero. A test's weight for a feature is that feature's share of the test's total specificity. `test_feature.tsv` keeps links with weight ≥ 0.01 (about 51k of 2.1M); every mapped test keeps at least one.

## Refreshing the data

Needs a TiDB checkout, Go, `sqlite3` and `zstd`.

```sh
scripts/fetch-raw.sh                                   # or regenerate: scripts/run_covmap.sh <tidb> pkg/a pkg/b ...
scripts/load-raw.sh <tidb>                             # work/extract.db: inventory + raw coverage + features
python3 scripts/tracker.py export work/extract.db      # -> data/*.tsv
python3 scripts/tracker.py report                      # -> reports/*.md
```

Then review `git diff data/` and commit. A new raw coverage set goes to a new release `raw-<tidb commit>`, and `source.tsv` is updated to point at it.

Notes for macOS: Homebrew clang may fail to build cgo against the current SDK; `run_covmap.sh` uses `/usr/bin/clang`. TiDB's testkit requires `-tags=intest`; covmap uses `intest,deadlock`. Tests that need failpoints must run in a separate worktree after `make failpoint-enable`, which rewrites sources.

## Plan

### Extraction (E)

| phase | content | done when | status |
| --- | --- | --- | --- |
| E1 | Test inventory: unit tests and integration test files. | All of `pkg`, `br`, `lightning`, `dumpling`, `cmd` scanned. | done: 10,165 unit tests, 263 integration files |
| E2 | Per-test coverage for the 250 P1 packages. | Every P1 test has a coverage outcome. | done: 5,344 of 5,501 have an outcome (306 incomplete); 157 produced no coverage |
| E3 | Re-run the 306 incomplete tests with failpoints enabled; explain the 157 with no coverage. | Incomplete < 1% of P1, each remaining one has a reason. | todo |
| E4 | Coverage for all remaining unit tests (about 4,800). | Same criteria as E2/E3 for all packages. | todo |
| E5 | Integration tests: run each `.test` file against a coverage-instrumented `tidb-server`. | Every integration file has a mapping. | todo |
| E6 | Better features: cluster functions by co-coverage instead of one file per feature; spot-check samples by hand. | Spot-check precision measured and recorded. | todo |
| E7 | Upstream tracking: for new TiDB commits, list affected tests and features. | Runs in CI on a schedule and opens an issue per relevant change. | todo |

Every phase records its data-quality numbers (mapping rate, failure rate, spot-check precision) in `reports/summary.md`.

### tidb-rs results (R)

| phase | content | status |
| --- | --- | --- |
| R1 | Collect tidb-rs results per Go test / integration file (pass, fail, unsupported) from tidb-rs CI into `data/`. | todo |
| R2 | Progress reports per feature and area: share of weighted tests that pass. | todo |
| R3 | Publish a browsable site (Datasette Lite on GitHub Pages). | todo |

Tasks and milestones themselves live in tidb-rs GitHub Issues; this repo holds the test-level data they are measured against.

## Known limits

- The data is from TiDB `master` (`4cee7f2084`), while tidb-rs is compared against TiDB v8.5.8 at runtime. Test names and functions mostly overlap; re-extracting at the v8.5.8 tag is cheap once E3/E4 settle the pipeline.
- `subtests` counts literal `t.Run` calls only; table-driven loops make it a lower bound.
- Six test names exist in both a package and its external `_test` package. `go test -run` runs both, so their coverage is merged under one (pkg, name).
