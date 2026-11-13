# Upstream fixes applied to the linked TiDB code

govec links TiDB v8.5.8 (`8b857efa2`) from the module proxy. A few bugs in it are
fixed upstream after v8.5.8 (on master and not backported to release-8.5, or on
release-8.5 after the v8.5.8 tag); tidb-rs ports those
fixes (constitution, article 1 item 2: upstream security / data-correctness
fixes are ported). So that the vectors record the fixed behaviour, govec links
a copy of the module with the affected files replaced by the copies here
(`<path in the tidb module>.fixed`).

- `scripts/govec-fixes.sh` copies the module to `work/govec-tidb`, applies the
  `.fixed` files, writes `work/govec-modfile/go.mod` (govec/go.mod plus a
  `replace github.com/pingcap/tidb` by the copy) and prints `-modfile=<path>`;
  `pkg/parser` is a module of its own (`github.com/pingcap/tidb/pkg/parser`,
  not in the tidb module), so `fixes/pkg/parser/<path>.fixed` go to a second
  copy, `work/govec-tidb-parser/<path>`, with its own `replace`;
  `scripts/govec.sh` and CI export it as `GOFLAGS`. (`go build -overlay` would
  be lighter, but go refuses to overlay files beneath GOMODCACHE.)
- A domain that depends on a fix lists it in `vec.Domain.Fixes` with a runtime
  probe. Records panics if the probe fails (a build without the fixes), so a
  file can never claim a fix it does not have; the header lists the applied
  fixes (`"fixes": [...]`), and records written mainly for a fix have
  `src: "fix <id>"`.
- The `.fixed` suffix keeps the go tool from compiling these copies as packages
  of this module.

| Fix | File | Change | Domains |
|---|---|---|---|
| [pingcap/tidb#67149](https://github.com/pingcap/tidb/pull/67149) (master `dfda1fd3e8`, 2026-10-19) | `pkg/util/collate/unicode_0900_ai_ci_impl.go` | `int(r) > len(MapTable4)` → `>=`: U+2CEA1 (= len) indexed past the 0900 table and panicked on user input | collate |
| [pingcap/tidb#65884](https://github.com/pingcap/tidb/pull/65884) (master `811a10e115`, 2026-01-29) | `pkg/planner/util/fixcontrol/set.go` | `ParseToMap` guards `s[0]` after a value's colon: `SET tidb_opt_fix_control='123:'` panicked (index out of range); fixed: value `""`, no error | sessionvars |
| [pingcap/tidb#62892](https://github.com/pingcap/tidb/pull/62892) (master `bc0d97a44d`, 2025-08-16; issue pingcap/tidb#62890) | `pkg/sessionctx/variable/sysvar.go` | `tidb_redact_log` becomes GLOBAL only and stores the process-wide `errors.RedactLogEnabled` in `SetGlobal` instead of `SetSession`: in v8.5.8 one session's `SET SESSION tidb_redact_log=ON` redacted the errors and warnings of every session on the instance (tidb-rs #62). `SET SESSION` now fails with 1229 | sessionvars, sysvar, exprctx |
| [pingcap/tidb#71844](https://github.com/pingcap/tidb/pull/71844) (release-8.5 `4f61995df8`, 2026-11-05, after v8.5.8; only its misc.go part) | `pkg/parser/ast/misc.go` (parser module) | `RedactURL` (`tidb_cloud_storage_uri`'s GetGlobal) also redacts `oss` secrets and azure `account-key` / `encryption-key` / `endpoint` | sessionvars |
| [pingcap/tidb#72214](https://github.com/pingcap/tidb/pull/72214) (release-8.5 `84040e1f0a`, 2026-11-10, after v8.5.8; issue pingcap/tidb#71858; only its misc.go part) | `pkg/parser/ast/misc.go` (same file, on top of #71844) | `RedactURL` drops userinfo and fragments, keeps only allowlisted query options per scheme (adds `gs`/`gcs`), drops the whole query if it does not parse, and returns `(invalid URL)` for an unparsable URL: v8.5.8 kept `user:pass@`, unknown options and unparsable URLs verbatim (credential leak) | sessionvars |

Adding a fix: copy the v8.5.8 file to `fixes/<path>.fixed`, apply the upstream
change with a comment citing it, add the mapping to `scripts/govec-fixes.sh`,
a row here, and a `vec.Fix` (with a probe) on each affected domain.
