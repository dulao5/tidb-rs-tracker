# Upstream fixes applied to the linked TiDB code

govec links TiDB v8.5.8 (`8b857efa2`) from the module proxy. A few bugs in it are
fixed on TiDB master but not backported to release-8.5; tidb-rs ports those
fixes (constitution, article 1 item 2: upstream security / data-correctness
fixes are ported). So that the vectors record the fixed behaviour, govec links
a copy of the module with the affected files replaced by the copies here
(`<path in the tidb module>.fixed`).

- `scripts/govec-fixes.sh` copies the module to `work/govec-tidb`, applies the
  `.fixed` files, writes `work/govec-modfile/go.mod` (govec/go.mod plus a
  `replace github.com/pingcap/tidb` by the copy) and prints `-modfile=<path>`;
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

Adding a fix: copy the v8.5.8 file to `fixes/<path>.fixed`, apply the upstream
change with a comment citing it, add the mapping to `scripts/govec-fixes.sh`,
a row here, and a `vec.Fix` (with a probe) on each affected domain.
