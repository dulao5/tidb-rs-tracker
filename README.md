# tidb-rs-tracker

Test inventory and progress tracking for [tidb-rs](https://github.com/dulao5/tidb-rs), a Rust rewrite of the TiDB SQL layer.

It answers three questions:

1. **What does TiDB test?** Every Go unit test and integration test file in TiDB.
2. **What does each test exercise?** A per-test coverage map from tests to Go functions, grouped into features.
3. **How far is tidb-rs?** Which of those tests and features tidb-rs passes. R1: per-statement results of TiDB's integration tests run against tidb-rs ([report](reports/integration.md)).

The data covers TiDB **v8.5.8** (tag `v8.5.8`, `8b857efa20`), the commit in [`data/source.tsv`](data/source.tsv): the release tidb-rs is compared against. It was first extracted at `master` `4cee7f2084` (E1–E3); see [Re-extraction at v8.5.8](#re-extraction-at-v858).

## Reports

- [Summary](reports/summary.md): test counts, coverage-mapping progress, tests by area.
- [Features](reports/features.md): each feature and how many tests exercise it as a main subject.
- [Integration tests on tidb-rs](reports/integration.md) (R1): share of read statements where tidb-rs answers like Go TiDB, per file, with every mismatch and the top reasons for the rest.

Reports are generated from `data/` and CI fails if they are out of date.

## Browse the data

```sh
python3 scripts/tracker.py build          # data/*.tsv -> tracker.db
uvx datasette tracker.db -m datasette/metadata.json
```

Datasette opens in the browser with filtering, faceting and SQL, plus canned queries: tests for a feature, features of a test, and tests whose mapping is incomplete or missing (with the reason). `tracker.db` is also attached to every CI run as an artifact.

## Layout

| path | content |
| --- | --- |
| `data/` | Source of truth: sorted TSV files with a header row. |
| `reports/` | Markdown generated from `data/`. Do not edit. |
| `sql/tracker.sql` | Schema of `tracker.db` built from `data/`. |
| `sql/extract.sql`, `sql/derive_features.sql` | Schema and feature derivation of the extraction DB. |
| `cmd/inventory` | Go AST scan of a TiDB checkout: unit tests and integration test files; with `-funcs`, every function declared under `pkg/`. |
| `cmd/covmap` | Per-test coverage for one Go package. |
| `cmd/refgraph` | Reference graph of the code under `pkg/`: type-checks every package and records what each function references; also the package-level declarations table. |
| `scripts/` | `tracker.py` (export / build / report / check) and the extraction shell scripts. |
| `datasette/` | Datasette table descriptions and canned queries. |

### Data files

| file | key | rows |
| --- | --- | --- |
| `source.tsv` | key | TiDB commit, extraction environment, raw coverage asset and its sha256. |
| `scope.tsv` | pkg | Packages in scope for a tidb-rs phase (`P1` = prototype). Maintained by hand. |
| `tests.tsv` | kind, pkg, name, path | Unit tests (`TestXxx`) and integration test files, with flags: SQL-level (`uses_store`), failpoints (`uses_fp`). |
| `features.tsv` | feature | v0 features: one per Go source file, with the functions in it that some test reaches. |
| `go_funcs.tsv` | pkg, file, symbol, start_line | Every function and method in the non-test `.go` files under `pkg/`, reached by a test or not: `kind` (func / method), `start_line`, `end_line`, `lines`, `generated` (1 = `// Code generated ... DO NOT EDIT.` file, or `pkg/parser/parser.go`). `symbol` is named like `features.tsv` symbols (`Func`, `Recv.Method`): join `file` = `feature`. Several `init` in one file give duplicate symbols, hence `start_line` in the key. |
| `go_decls.tsv` | pkg, file, name | Every package-level `type`, `var` and `const` in the non-test `.go` files under `pkg/` (`kind`, `start_line`, `end_line`, `lines`). A spec in a `( ... )` group spans its own lines; an ungrouped one starts at the keyword. |
| `go_file_refs.tsv` | from_file, to_file | Reference graph at file level: how many distinct (function in `from_file`, target in `to_file`) references there are, by kind: `funcs` (functions, concrete methods), `ifaces` (interface methods), `types`, `vars_consts`. Self edges excluded. See [Reference graph](#reference-graph). |
| `cov_status.tsv` | pkg, test | Outcome of each test's coverage run: `ok` (mapping complete), `failed` (mapping incomplete, a lower bound) or `skipped` (`t.Skip` or not compiled in; no mapping), with the reason in `detail`. |
| `test_feature.tsv` | pkg, test, feature | Which features a test exercises, with a weight (see below). |
| `rs_run.tsv` | key | R1 run: tidb-rs commit, Go TiDB version, TiDB commit of the test files, date, runner, duration. |
| `rs_integration.tsv` | file | R1: per integration test file, statements, reads and outcomes (match, mismatch, unsupported, b_error, a_error, skipped, write). |
| `rs_integration_reason.tsv` | class, outcome, reason | R1: outcome counts by statement class and reason (unsupported message, error, mismatch kind, skip reason). |
| `rs_integration_mismatch.tsv` | file, line, conn | R1: every read where tidb-rs answered differently from Go TiDB. |

### Why text files and not a database in git

A re-extraction must be reviewable. With sorted TSV, `git diff` shows exactly which tests appeared or disappeared and which mappings moved; a SQLite file (even through Git LFS) cannot be diffed or merged. SQLite is a local cache, rebuilt in about a second.

The raw coverage (13.5M test→function rows, 1.4 GB at v8.5.8) is too large for git. It is published as release assets named in `source.tsv` with their sha256; `scripts/fetch-raw.sh` downloads and verifies them:

- `raw_asset` (54 MB, zstd): `covmap/<pkg>.tsv` and `covmap/status/<pkg>.tsv`, the full run of every P1 package.
- `raw_fp_asset` (5.4 MB): `covmap-fp/<pkg>.tsv` and `covmap-fp/status/<pkg>.tsv`, the re-run with failpoints enabled of every test that failed or produced no coverage in the full run (E3). `load-raw.sh` replaces those tests' coverage and outcome with the re-run's.

### Feature weights

A test reaches thousands of functions, most of them shared plumbing (session setup, bootstrap, planner entry). Each function gets a specificity `idf = ln(N / n)`, where `N` is the number of mapped tests and `n` the number that reach the function. Plumbing reached by almost every test scores near zero. A test's weight for a feature is that feature's share of the test's total specificity. `test_feature.tsv` keeps links with weight ≥ 0.01 (about 47k of 1.6M at v8.5.8); every mapped test keeps at least one.

## Reference graph

`cmd/refgraph` type-checks every non-test package under `pkg/` (including the nested `pkg/parser` module and packages in `testdata` dirs such as `pkg/testkit/testdata`) with `golang.org/x/tools/go/packages`, then walks each function and method declaration (signature and body, closures included) and resolves every identifier through `types.Info.Uses`. A reference is kept if its target is declared in a non-test file under `pkg/`:

| kind | target | `to_symbol` |
| --- | --- | --- |
| `func` | a function or concrete method; a generic instance resolves to its generic declaration | `Func`, `Recv.Method` as in `go_funcs.tsv` |
| `iface` | a method of a package-level interface: a call (or method value) through the interface. The interface type is recorded too, as `type` | `Iface.Method` (the interface that declares it, also when reached through embedding) |
| `type` / `var` / `const` | a package-level declaration | name, as in `go_decls.tsv` |

```sh
scripts/refgraph.sh <tidb>     # -> data/go_decls.tsv, data/go_file_refs.tsv, work/go_refs.tsv
```

`work/go_refs.tsv` (not committed; 180,688 rows, 21 MB at v8.5.8) has one row per distinct (function, target): `from_file, from_symbol, from_start_line, to_file, to_symbol, kind`; `from_start_line` tells apart the several `init` of a file. The script also writes the `go_funcs.tsv` rows not found among the type-checked declarations to `work/go_refs_missing.tsv` (55 of 28,214 at v8.5.8, all in files the build excludes: 23 in `//go:build ignore` generators, 11 Windows-only, 21 for linux, freebsd, wasm, `!cgo`, `intest` or `deadlock`). It builds with the patched go.mod (see below) on the host's GOOS/GOARCH, without build tags (`TAGS=` to set them) and loads dependencies from export data; about 30 s with a warm build cache.

Limits: it is a static graph. Dynamic dispatch shows up as `iface` edges to the interface method, not to its implementations; struct fields, local types and interfaces declared in a function or written inline (`x.(interface{ M() })`) are not targets; references outside functions (var initializers, struct field types, embedded interfaces) are not edges, only `go_decls` rows; files excluded by the build configuration have no edges.

## tidb-rs integration results (R1)

The run happens in tidb-rs, which owns the binary, the cluster scripts and the A/B harness: `scripts/integrationtest.sh` there starts a tiup playground, joins tidb-rs, runs `ab integrationtest` over `tests/integrationtest/t` and writes an output directory (see tidb-rs `tools/ab/README.md`, "Integration tests"). Import it here:

```sh
python3 scripts/tracker.py import-integration <tidb-rs>/target/integrationtest   # -> data/rs_*.tsv
python3 scripts/tracker.py report
```

Per-statement detail (about 52k rows) goes to `work/rs_statements.tsv`, which is not committed; the committed tables keep per-file counts, reasons and every mismatch. In CI the run would be a tidb-rs job (Linux, tiup) whose output directory is an artifact this repo imports.

## Refreshing the data

Needs a TiDB checkout, Go, `sqlite3` and `zstd`.

```sh
scripts/fetch-raw.sh                                   # or regenerate: scripts/run_covmap.sh <tidb> pkg/a pkg/b ...
scripts/load-raw.sh <tidb> [work/extract.db]           # inventory + raw coverage (+ covmap-fp overlay) + features
python3 scripts/tracker.py export work/extract.db      # -> data/*.tsv
python3 scripts/tracker.py report                      # -> reports/*.md
```

`load-raw.sh` also runs `cmd/inventory -funcs` for `go_funcs.tsv` and `cmd/refgraph -decls` for `go_decls.tsv`; `go_file_refs.tsv` needs a build, see [Reference graph](#reference-graph). Then review `git diff data/` and commit. A new raw coverage set goes to a new release `raw-<tidb commit>`, and `source.tsv` is updated to point at it.

`<tidb>` for `load-raw.sh` is a clean checkout at `tidb_commit` (it is only scanned for tests).

Packages that embed etcd do not build with plain `go test` at v8.5.8 (see below); build them with the patched go.mod:

```sh
scripts/patched-modfile.sh <tidb>                      # -> work/modfile/go.mod (+ work/patched/)
MODFILE=$PWD/work/modfile/go.mod scripts/run_covmap.sh <tidb> pkg/a ...
```

`covmap -modfile` applies it to the root module only (`pkg/parser` is a nested module with its own go.mod).

Notes for macOS: Homebrew clang may fail to build cgo against the current SDK; `run_covmap.sh` uses `/usr/bin/clang`. It also sets `TMPDIR=/tmp`: tests that listen on a unix socket in `t.TempDir()` fail (and some then hang to the timeout) under the default `/var/folders/...` path, which exceeds the 104-byte socket path limit. TiDB's testkit requires `-tags=intest`; covmap uses `intest,deadlock`.

Failpoint re-runs need a separate TiDB worktree, because `make failpoint-enable` rewrites sources in place:

```sh
git -C <tidb> worktree add --detach ../tidb-v858-fp v8.5.8 && make -C ../tidb-v858-fp failpoint-enable
OUT=covmap-fp TESTS=work/rerun.tsv MODFILE=$PWD/work/modfile/go.mod scripts/run_covmap.sh ../tidb-v858-fp pkg/a pkg/b ...   # rerun.tsv: pkg<TAB>test
```

The rewrite keeps every function's file and name (checked with `cmd/inventory -funcs` on both worktrees: same symbols in every file under `pkg/` at v8.5.8, and for all 341 rewritten files at `4cee7f2084`), so the mapping by file + function is unchanged; the generated `binding__failpoint_binding__.go` files are dropped by `load-raw.sh`.

## Re-extraction at v8.5.8

The pipeline was re-run unchanged at the v8.5.8 tag (2026-11-01..02, same machine, tags `intest,deadlock`). Differences from `master` that matter:

- **Go**: v8.5.8's go.mod needs go 1.25.12; with `GOTOOLCHAIN=auto` the go command downloads it.
- **etcd**: go.mod selects otelgrpc v0.61.0, which no longer has `otelgrpc.UnaryServerInterceptor` used by `go.etcd.io/etcd/server/v3` v3.5.15. TiDB's Bazel build patches etcd (`build/patches/io_etcd_go_etcd_server_v3.patch`); plain `go test` fails to build every package that embeds etcd. In P1 that is `pkg/executor/importer` and `pkg/server/handler/tests`; they (and the whole failpoint re-run) were built with `scripts/patched-modfile.sh`, which applies the same patch through `go -modfile` without touching the TiDB checkout.
- **Build tags**: v8.5.8 has no `nextgen` tag and no kernel-type switch, so no test is skipped as next-gen-only (master: 54 skipped, v8.5.8: 21). `intest` and `deadlock` are used the same way.
- **Layout**: `lightning` code is mostly under `pkg/lightning`; fewer test packages overall (7,976 unit tests vs 10,165).
- **Shared state in `/tmp`**: testkit caches a bootstrapped mock store in `/tmp/tidb-unistore-bootstraped-image` (made by this run) and spill tests check that TiDB's temp dir `/tmp/<uid>_tidb/.../tmp-storage` holds no spill files. A failing spill test leaves its files there and makes the next spill test fail (`util.go:47: Should be false`); `TestIssue59377` and `TestUnparallelSortSpillDisk` failed that way in the re-run and passed after the leftovers were removed and they were run again.
- `pkg/executor` temporary-table tests skip when testkit randomly enables infoschema v2 (`select @@tidb_schema_cache_size > 0`), so which of them are skipped varies between runs.

### Scope at v8.5.8

`scope.tsv` (213 packages) = the 250 master P1 packages that exist at v8.5.8 (204) + 9 v8.5.8 packages. Not widened otherwise: the 9 are in areas where master's P1 already had every test package.

- Missing at v8.5.8 with an equivalent already in scope (tests matched by name): `executor/join/test/{indexjoin,mergejoin}` → `pkg/executor/join`; `executor/test/analyzetest/{columns,options}` → `pkg/executor/test/analyzetest`; `executor/test/{infoschema,recovertest}`, `executor/windows` → `pkg/executor`; `executor/test/{plancache,planreplayer}` → partly `pkg/executor/test/executor`; `expression/test/constantpropagation` → `pkg/expression`; `planner/cascades/old` → `pkg/planner/cascades`; `planner/core/casetest/ch` → `.../casetest/tpch`; `planner/core/casetest/{indexmerge,plancache,tpcds}`, `planner/core/tests/pointget` → `pkg/planner/core`; `planner/core/generator/shallow_ref` → `.../generator/hash64_equals`; `session/test/bootstraptest2` → `pkg/session`; `sessionctx/variable/tests{,/slowlog}`, `sessionctx/vardef` → `pkg/sessionctx/variable`.
- Missing, equivalent added to scope: `planner/cascades/pattern` → `pkg/planner/pattern`; `server/internal/packetio` → `pkg/server/internal`; `session/test/{bootstraptest,clusteredindextest,nontransactionaltest,resourcegrouptest,schematest,temporarytabletest}` → `pkg/session/<same name>`.
- Missing, equivalent outside the P1 areas, not added: `infoschema/isvalidator` (→ `pkg/domain`), `session/sessmgr` (→ `pkg/util`, processinfo).
- Missing, master-only features: `executor/mviewdeltamergeagg`, `executor/test/materializedviewlog`, `metaservice`, `parser/ast/visitor_codegen`, `parser/util`, `planner/cascades/rule/apply/decorrelateapply`, `planner/core/casetest/{cascades,fulljoin,logicalplan,schema}`, `planner/core/tests/rewriter`, `planner/extstore`, `server/internal/advertisedstatus`, `server/tests/standby`, `session/syssession`.
- v8.5.8-only, added: `pkg/parser/model` (`CIStr` etc.; at master in `pkg/parser/ast/model.go`).
- v8.5.8-only in partly scoped areas, not added: `pkg/util/replayer`, `pkg/util/servicescope`.
- 12 scope packages have no tests at v8.5.8 (e.g. `pkg/statistics/handle`, `pkg/planner/core/rule`); they stay in scope and have nothing to run.

## Porting blocks

tidb-rs ports Go TiDB v8.5.8 block by block (rules: tidb-rs `CONSTITUTION.md`; plan: tidb-rs `specs/ROADMAP.md`).
The split is maintained here:

- `scripts/blocks/rules.py`: block definitions (domain, title, phase) and ordered matching rules on Go file path + function symbol. Large hub files are split per function, and a logical rule's methods on every operator belong to the rule's block.
- `data/blocks.tsv`, `data/func_blocks.tsv` (every `go_funcs.tsv` row → block), `data/block_edges.tsv` (block → block reference counts from the reference graph).

Regenerate after changing the rules (needs `work/go_refs.tsv` from `scripts/refgraph.sh`):

```sh
python3 scripts/blocks/assign.py          # blocks.tsv, func_blocks.tsv; prints sizes
python3 scripts/blocks/deps.py            # block_edges.tsv
python3 scripts/blocks/layers.py 10       # per-phase dependency layers
python3 scripts/blocks/roadmap_tables.py  # the phase tables of specs/ROADMAP.md
```

## Plan

### Extraction (E)

| phase | content | done when | status |
| --- | --- | --- | --- |
| E1 | Test inventory: unit tests and integration test files. | All of `pkg`, `br`, `lightning`, `dumpling`, `cmd` scanned. | done at v8.5.8: 7,976 unit tests, 246 integration files (master: 10,165 / 263) |
| E2 | Per-test coverage for the P1 packages. | Every P1 test has a coverage outcome. | done at v8.5.8: all 4,607 tests of the 213 packages have an outcome (after E3); full run ok 4,325 / failed 261 / skipped 21. Master: 5,501 tests in 250 packages. |
| E3 | Re-run the incomplete tests with failpoints enabled; explain the ones with no coverage. | Incomplete < 1% of P1, each remaining one has a reason. | done at v8.5.8: re-ran 308 (261 failed, 26 ok without coverage, 21 skipped) → ok 4,585 (25 reach nothing beyond package init), failed 1 (0.02%: `TestSlowLogMaxPerSec`, fails the same way at master and under plain `go test`), skipped 21 (0.46%; `t.Skip` upstream: unstable/slow/disabled, infoschema v2, one `reserved_words_test` tag), no outcome 0. Master: ok 5,446 / failed 1 / skipped 54. |
| E4 | Coverage for all remaining unit tests (about 3,400 at v8.5.8). | Same criteria as E2/E3 for all packages. | todo |
| E5 | Integration tests: run each `.test` file against a coverage-instrumented `tidb-server`. | Every integration file has a mapping. | todo |
| E6 | Better features: cluster functions by co-coverage instead of one file per feature; spot-check samples by hand. | Spot-check precision measured and recorded. | todo |
| E7 | Upstream tracking: for new TiDB commits, list affected tests and features. | Runs in CI on a schedule and opens an issue per relevant change. | todo |

Every phase records its data-quality numbers (mapping rate, failure rate, spot-check precision) in `reports/summary.md`.

### tidb-rs results (R)

| phase | content | status |
| --- | --- | --- |
| R1 | Collect tidb-rs results per Go test / integration file (pass, fail, unsupported) from tidb-rs CI into `data/`. | integration files: first run imported ([report](reports/integration.md)): 1,059 of 14,173 scored reads match Go TiDB (7.5%), 83 mismatch; Go unit tests: todo |
| R2 | Progress reports per feature and area: share of weighted tests that pass. | todo |
| R3 | Publish a browsable site (Datasette Lite on GitHub Pages): https://dulao5.github.io/tidb-rs-tracker/ | done |

Tasks and milestones themselves live in tidb-rs GitHub Issues; this repo holds the test-level data they are measured against.

## Known limits

- The extraction (tests, coverage, features, `go_funcs`) is at the v8.5.8 tag, but R1 (`rs_*`) ran the integration files of TiDB `master` (`4cee7f2084`, `rs_run.tsv` `tests_commit`) against Go TiDB v8.5.8: 4,902 statements fail on Go TiDB itself (mostly `EXPLAIN FORMAT='plan_tree'`, new in master) and are not scored, and 18 of its files do not exist at v8.5.8 (`tracker.py check` only matches R1 files against `tests.tsv` when the commits agree). The next R1 run should use the v8.5.8 tag's files.
- `go_funcs.tsv` lists declarations only: functions in files excluded by build tags (e.g. `_windows.go`) are listed too, and closures / function literals are part of their enclosing function.
- `subtests` counts literal `t.Run` calls only; table-driven loops make it a lower bound.
- Six test names exist in both a package and its external `_test` package. `go test -run` runs both, so their coverage is merged under one (pkg, name).
