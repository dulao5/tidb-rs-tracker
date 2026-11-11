# govec

govec 用 Go TiDB v8.5.8（commit `8b857efa2`）自己的代码跑一批输入，把输入和输出写成向量文件，供 tidb-rs 的 Rust 移植回放比对（方法论 §5 "函数级对照"）。向量都是 Go 实际运行得到的，不是从测试里抄的期望值。

第一个领域是 `decimal`（`pkg/types/mydecimal.go`，流程试点块 D01.2）。

## 目录

| 路径 | 内容 |
|---|---|
| `govec/go.mod` | 独立 Go 模块，依赖 `github.com/pingcap/tidb v1.1.0-beta.0.20260827064614-8b857efa2036`（即 tag v8.5.8 的伪版本）和同一 commit 的 `pkg/parser`；`go 1.25.12` 与 v8.5.8 的 go.mod 一致；`replace` 照抄 TiDB go.mod 中对第三方库的替换（依赖模块的 replace 不会生效） |
| `govec/vec/` | 与领域无关的部分：记录格式、领域注册、生成、oracle |
| `govec/domains/<领域>.go` | 每个领域一个文件：op 列表、参数解码、调用 Go 代码 |
| `govec/domains/<领域>_cases.go` | 输入：从 Go 测试抄来的用例表、边界用例、带种子的随机用例（可与上一个文件合并） |
| `govec/govec_test.go` | 自检：文件头、每个 op 的最少条数、Go 测试函数都有记录、块内每个 Go 函数都被覆盖或豁免、oracle 与生成结果一致、两次生成逐字节相同、gzip 存储可还原、本目标已提交的向量是最新的、FMA 模型与 `data/go_fma_sites.tsv` 一致（`TestFMA`）、每个领域两个目标的文件只在允许表 `targetdiffs_test.go` 登记的记录上不同（`TestTargetsDiffer`） |
| `vectors/v8.5.8/<目标>/<领域>.jsonl.gz`（见"目标平台"、"存储"） | 已提交的向量，每个目标平台一份（`amd64`、`arm64`） |
| `scripts/govec.sh` | 重新生成 / 检查 |

## 记录格式

JSONL。第一行是文件头：

```json
{"govec":1,"domain":"decimal","tidb":"v8.5.8","commit":"8b857efa2","go":"go1.25.12","goos":"linux","goarch":"amd64","goamd64":"v1","seed":20260827,"records":33831}
```

之后每行一次调用：

```json
{"op":"to_int","args":["-1.23"],"out":-1,"err":{"name":"ErrTruncated","code":1265,"msg":"[types:1265]Data truncated for column '%s' at row %d"},"src":"TestToInt"}
```

- `op`、`args`：调用和参数。只凭这两项就能重放（oracle 模式也只用这两项）。
- `out`：返回值。即使同时返回了错误（如截断时的 `ErrTruncated`）也照样记录，Rust 侧两者都要比。
- `err`：`null` 或 `{name, code, msg}`。`name` 是 Go 中的错误变量名（如 `ErrOverflow`），`code` 是 MySQL 错误码，`msg` 是 `err.Error()`。Go 代码 panic 时记为 `{"name":"panic","msg":...}`，`out` 为 `null`；Rust 侧对这类记录只要求"不返回与 Go 不同的成功结果"，具体怎么处理由块的 spec 决定。
- `warnings`：可选，用到语句上下文 warning 的领域才有（decimal 没有）。
- `src`：来源。Go 测试函数名（`TestXxx`）表示用例表抄自该测试；`sys/...` 是边界用例；`rand/...` 是随机用例。
- 字节串一律小写 hex；浮点数用 `strconv.FormatFloat(f, 'g', -1, 64)` 的字符串（特殊值写作 `NaN`、`+Inf`、`-Inf`）；int64/uint64 用 JSON 数字（Rust 用 serde_json 可精确解析）。

### decimal 领域

参数中的十进制数是字符串，回放时用 `FromString` 解析，解析错误忽略、使用解析出的值（与 Go 一致）。十进制结果的形式：

```json
{"s":"12.000000000","str":"12.00000","st":[2,9,5,0,12,0,0,0,0,0,0,0,0]}
```

`s` 是 `ToString()`，`str` 是 `String()`（按 resultFrac 做 HalfUp 舍入），`st` 是内部状态 `[digitsInt, digitsFrac, resultFrac, negative(0/1), wordBuf[0..8]]`。逐函数移植时内部状态也应一致。

op 一览（参数和输出见 `domains/decimal.go` 中的 `decimalBaseCoverage`）：`from_string from_int from_uint from_float64 to_int to_uint to_float64 round round_inplace shift add sub mul div mod neg compare to_bin from_bin bin_size peak to_hash_key props max_or_min marshal_json unmarshal_json`，以及下面三类。

**别名调用**（`to` 就是某个输入，Go 允许，`validateArgs` 先复制被别名的输入）：`add_alias sub_alias mul_alias div_alias mod_alias`，参数是原 op 的参数再加一个 `alias`：`1` 为 `f(x, y, x)`，`2` 为 `f(x, y, y)`，`3` 为 `f(x, x, x)`（此时 `b` 与 `a` 相同，只解析 `a`）。输出是调用后的 `to` 对象。`Round` 的原地调用是 `round_inplace`；`Shift` 本身就是原地的。

**链式调用** `<producer>_then_<consumer>`：前一步的结果对象（不经字符串重新解析）直接作为后一步的第一个十进制参数，用来覆盖内部状态与 `FromString` 结果不同的对象（`FromInt`/`FromUint`/`Round`/加法/乘法的 `digitsInt` 按字取整、乘法溢出后的 `-0` 等）。producer 为 `from_int from_uint round add mul div`（`div` 的参数是 `[a, b, fracIncr]`，它的结果 `resultFrac < digitsFrac`，见"目标平台"），consumer 为 `to_string to_bin to_hash_key props round round_inplace shift to_int to_float64 compare add mul div`（`to_string` 输出 `{"s","str"}`，只作 consumer 用）。`args` 是 producer 的参数后接 consumer 去掉第一个十进制参数后的参数，如 `{"op":"from_int_then_to_bin","args":[123,20,0]}`；`out` 是 consumer 的输出（中间对象不编码，也不调用它的 `ToString`/`String`：`9…9(81) × 123.456` 截断后的对象，`ToString` 会越界 panic，而 `to_bin`、`props`、`compare` 不会）；`err` 取 producer 的错误，producer 无错时取 consumer 的错误（两步都执行，与忽略 `ErrTruncated` 继续计算的 Go 调用方一致）。

**脏接收者** `dirty_<op>`：op 写入的对象（接收者或 `to`）不是新的 `MyDecimal`，而是先对第一个参数做 `FromString`（错误忽略）留下的对象，如负数、带小数（`resultFrac > 0`）、9 个字都用满的数。`FromString`、`FromInt`、`FromUint`、`FromBin` 等不重置自己不写的字段，所以结果依赖旧状态：接收者是 `-123.456` 时 `FromString("2")` 得 `-2`，`FromUint(5)` 得 `-5`。op 为 `from_string from_int from_uint from_float64 from_bin unmarshal_json round add sub mul div mod`。`args` 是脏值后接原 op 的参数，如 `{"op":"dirty_from_uint","args":["-1",5]}`；`out`、`err` 是原 op 的输出（写入后的接收者 / `to`）和错误。Go 调用方几乎都用 `new(MyDecimal)`，Rust 调用方更可能复用缓冲（chunk 列循环），这类记录防止 Rust 移植"顺手"加上重置。

### memory 领域

块 D15.1（`pkg/util/memory`；tracker 用到的 `pkg/util/sqlkiller`）。op：

- `script`：`args[0]` 是步骤表，作用于用整数编号的 tracker、动作和 killer。`out` 每步一项 `{"r": 结果, "fired": [...]}`，`fired` 按顺序列出这一步中执行的动作（`mock:<aid>:<label>`、`log:<aid>:<conn>`、`panic:<aid>:<conn>`）。Go 在某一步 panic 时记为 `{"panic": {code,msg}}`（`panic(err)`）或 `{"panic": "消息"}`，脚本到此结束。Rust 侧 `Consume` 的 `panic(err)` 对应 `Err`。步骤一览见 `domains/memory.go` 文件头。动作链是链表，同一个动作绑进两条链会成环，随机脚本每次绑定都用新动作。
- `format_bytes`、`bytes_to_string`：`args [n]`。

每个脚本开始时把 `MemUsageTop1Tracker` 置空，`ServerMemoryLimitSessMinSize` 恢复默认，`EnableGCAwareMemoryTrack` 关闭：GC-aware 的释放依赖 Go 的 GC 何时运行 finalizer，结果不确定，Rust 移植中立即释放（D15.1 plan §4）。顺序依赖 map 的结果（子节点列表、`SearchTrackerConsumedMoreThanNBytes`、`CountAllChildrenMemUse`）都排序后记录。meminfo / memstats 读的是机器的内存和 Go runtime 的堆统计，列入豁免表，由 Rust 单元测试覆盖。

### Go 函数覆盖与豁免表

`Coverage.GoFuncs` 使用 tracker 的符号格式（与 `data/go_funcs.tsv`、`data/func_blocks.tsv` 一致）：`MyDecimal.FromString`、`DecimalAdd`，不写指针接收者。未导出的辅助函数列在实际调用到它们的 op 下（如 `add` 列出 `validateArgs doAdd doSub add sub`）。每个领域声明自己对应的块（decimal 为 `D01.2`），`TestBlockCoverage` 检查：块内每个函数要么出现在某个 op 的 `GoFuncs` 中，要么在豁免表 `Exempt` 里写明理由；`GoFuncs` 和豁免表中的每个符号都必须是块内的函数（防止拼错或过期）。D01.2 的 58 个函数中 56 个被覆盖（用 `go test -coverpkg` 核对过，这 56 个都确实被执行到），豁免表：

| 函数 | 理由 |
|---|---|
| `NewDecFromFloatForTest` | 测试用构造函数：`FromFloat64` 加上出错时 `log.Panic`，取值路径即 `from_float64` |
| `NewDecFromStringForTest` | 测试用构造函数：`FromString` 加上出错时 `log.Panic`，取值路径即 `from_string` |

（评审中提到的 `Reset`、`Copy` 在 v8.5.8 的 `mydecimal.go` 中不存在。）

## 目标平台

向量与**代码生成目标**有关。Go 规范允许编译器把浮点 `x*y + z` 融合成一条 FMA 指令（只舍入一次），gc 是否融合取决于 GOARCH（amd64 上还取决于 GOAMD64）和具体位置，只能从汇编判断。tracker 的 `data/go_fma_sites.tsv`（`scripts/go_fma_sites.py`）逐行登记了 `pkg/types` 中被融合的位置。D01.2 中只有 `MyDecimal.ToFloat64` 的两处 `f += float64(x) * pow10off81[...]`（mydecimal.go:1118、1125）：arm64 融合（`FMADDD`），amd64 不融合（GOAMD64=v1 和 v3 都不融合）。

对 `resultFrac < digitsFrac` 的对象（`DecimalDiv` 的结果：digitsFrac 按字取整为 9，resultFrac = 被除数小数位 + fracIncr），舍入位恰好落在 .5 边界时 `math.Round(f*unit)/unit` 掩盖不了末位差别，例如 `DecimalDiv(50.77, 32, 4)` = 1.586563，`ToFloat64` 在 arm64 上得 `1.586563`，在 amd64 上得 `1.586562`。从 `FromString`、`Round`、`Mul`、`FromBin` 得到的对象 resultFrac == digitsFrac，不会走到这里。

- **每个目标一份文件**：`vectors/v8.5.8/amd64/`（GOAMD64=v1，Go 的默认值，也是基线；v8.5.8 的 Makefile 没有设置 GOAMD64）和 `vectors/v8.5.8/arm64/`。文件头写 `goarch` 和（amd64 上）`goamd64`；`govec target` 输出本二进制的目标名（`amd64`、`amd64.v3`、`arm64`），`amd64.v3` 等非基线目标不提交。
- **触发用例**（追加在最后，`decimalFMA`）：`div` 作为 producer 接所有 consumer；`sys/fma` 是 `50.77/32`、`100.54/64`、`153.81/96`、`193.08/128`（fracIncr 4）接 `to_float64`；`rand/div` 是 300 个随机 `x/j`（x 带 1–3 位小数、整数部分不超过 3 位，j ∈ [2,160)，fracIncr 4，即 `div_precision_increment = 4` 时的 `a/b`）；之后继续抽取同样形状的 `x/j`，只保留结果依赖融合的，直到共有 40 条（`rand/fma`）。"依赖融合"由 `domains.ToFloat64Model` 判断：它按 Go 源码重算 ToFloat64，两处乘加分别用 `math.FMA` 和显式 `float64(...)` 转换（禁止融合）计算，两者不同即依赖融合。判断与生成平台无关，所以两个目标的文件记录相同、只有输出不同。
- **检查**：`TestFMA` 读 `data/go_fma_sites.tsv` 得到本目标是否融合，要求每条 `div_then_to_float64` 记录的 Go 输出等于对应的模型值（汇编扫描、模型、Go 实际输出三者一致），且至少 40 条依赖融合；`TestTargetsDiffer` 比较每个领域已提交的两个文件，见下面的"两个目标的差异"。当前 33,831 条记录中两个目标有 44 条不同（4 条 `sys/fma` 和 40 条 `rand/fma`）。
- **其他平台**：登记表中 ppc64le、s390x、riscv64、loong64 也融合这两处，但没有生成向量。

## 重新生成

```sh
scripts/govec.sh                    # 全部领域、全部目标，写入 vectors/v8.5.8/<目标>/
scripts/govec.sh decimal            # 只生成一个领域
scripts/govec.sh --check            # 在本机目标上重新生成，与该目标已提交的文件比较（CI 跑这个）
scripts/govec.sh --check --all      # 检查所有目标
```

本机目标直接运行；另一个目标交叉编译成 linux 二进制（`CGO_ENABLED=0`，amd64 为 `GOAMD64=v1`），在 `docker run --platform linux/<arch>` 中运行（macOS 上 Docker Desktop 用 Rosetta 或 QEMU 模拟 amd64）；也可以用 `GOVEC_RUN_amd64="<命令前缀>"` 指定别的运行方式（例如 ssh 到一台 x86 机器）。

工具链由 `govec/go.mod` 固定为 go 1.25.12，`GOTOOLCHAIN=auto`（默认）时 go 会自动下载。文件头记录了 Go 版本，用别的版本生成会因文件头不同而检查失败，这是有意的（`strconv`、`encoding/json` 的行为可能随版本变化）。

本地调试想直接用源码目录时，可以临时加 replace（不要提交）：

```sh
cd govec
go mod edit -replace github.com/pingcap/tidb=$HOME/src/pingcap/tidb-all-source-code/tidb-v858 \
            -replace github.com/pingcap/tidb/pkg/parser=$HOME/src/pingcap/tidb-all-source-code/tidb-v858/pkg/parser
```

macOS 上若 cgo 报找不到 `stdlib.h`（Homebrew 的 clang 在 PATH 前面），用 `CC=/usr/bin/clang scripts/govec.sh`。

**上游修复**：v8.5.8 中有个别 bug 在 TiDB master 已修复、但没有回移到 release-8.5，tidb-rs 按宪章第一条第 2 项移植了这些修复。为了让向量记录修复后的行为，govec 链接的是打过这些修复的 TiDB 模块副本：`scripts/govec.sh` 和 CI 会设置 `GOFLAGS="$(scripts/govec-fixes.sh)"`。手动执行 `go build`、`go test`、`go run ./cmd/rsgen` 时也要先设置它。涉及修复的领域在 `vec.Domain.Fixes` 中登记，并带运行时探测；没有打修复的构建在生成时会直接 panic，文件头的 `fixes` 字段列出已应用的修复。清单见 [fixes/README.md](fixes/README.md)，目前只有 pingcap/tidb#67149（collate）。

## 确定性

- 随机用例用固定种子（文件头 `seed`），PCG 生成器；生成顺序就是代码顺序，不遍历 map。新加的用例组追加在已有组之后，已有记录和它们消耗的随机数序列不变。
- 输出用结构体编码（字段顺序固定），不用 map。
- `go test ./...`（在 `govec/` 下）会生成两次并比较字节，再与已提交文件比较。
- **与操作系统无关，与目标平台有关**：同一目标在不同操作系统上重新生成，除文件头的 `goos` 外逐字节相同（`--check` 和 `go test` 只排除 `goos`）。不同目标的结果可以不同（见"目标平台"），所以每个目标与自己的文件比较。CI 用 matrix 在 `ubuntu-latest`（amd64）和 `ubuntu-24.04-arm`（arm64）上各检查本机目标的文件。CI 通过只说明**现有用例**在该目标上可复现，不说明其他用例与平台无关；新增含浮点运算的 op 时要先查 `data/go_fma_sites.tsv`。
- **日志**：`domains.Quiet()` 在 `init` 中把 TiDB 全局日志级别设为 Fatal，免得 `String()` 里的 `terror.Log` 之类刷屏。它只影响日志输出，不影响结果；如果以后某个领域的 Go 代码路径依赖日志的副作用（例如某个值只在 debug 日志开启时才计算），该领域必须在自己的 Exec 中恢复日志级别，并在本 README 说明。

## 存储

向量文件一律以 `vectors/v8.5.8/<目标>/<领域>.jsonl.gz` 进 git（`vec.Domain.Gzip = true`，`govec <领域> --filename` 给出文件名）。明文 JSONL 每块几 MB（decimal 约 6.7 MB，gzip 后约 0.5 MB），上百个块会让仓库历史膨胀到几百 MB；下游（tidb-rs）也复制 `.gz` 文件，测试用 `flate2` 读。`Gzip = false`（明文 `.jsonl`）只留给调试。

`.gz` 由 govec 自己用 Go 的 `compress/gzip` 写出：不写文件名和修改时间、固定 `BestCompression`，所以字节只取决于内容和（已固定的）Go 版本；不要用外部 `gzip` 命令重新压缩。看内容用 `gzip -dc vectors/v8.5.8/decimal.jsonl.gz | head`。

`scripts/govec.sh --check`（即 `govec <领域> --check <文件>`）比对的是解压后的 JSONL 内容（只排除 `goos`），不比对压缩字节，因此压缩实现的差异不会导致误报；`TestStorage` 检查 gzip 输出确定、可还原。下游（tidb-rs）复制向量时记录 tracker commit 和所复制文件（`.jsonl` 或 `.jsonl.gz`）的 sha256。单个压缩文件超过 50 MB（GitHub 的警告线）时再改为 release asset + sha256，届时更新本节。

## Oracle 模式

用于以后的差分模糊测试：

```sh
echo '{"op":"add","args":["1.5","2.25"]}' | bin/govec decimal --oracle
```

每读一行 `{"op","args"}`，输出一行完整记录（不含文件头和 `src`），每行之后都 flush，可以用管道持续交互。

## 增加一个领域

1. 新建 `govec/domains/<名字>.go`，在 `init()` 里 `vec.Register(&vec.Domain{...})`，填：
   - `Name`、`Seed`；
   - `Errors`：要命名的哨兵错误；
   - `Coverage`：op → 最少条数和对应的 Go 函数（tracker 符号格式）；
   - `Blocks`：对应的 tracker 块；`Exempt`：块内不覆盖的函数及理由；
   - `Gzip`：设为 true（见"存储"）；
   - `Exec(op, args)`：解码参数、调用 Go、返回可编码的结果（用结构体，不用 map）；需要 warning 时返回 `vec.WithWarnings`；
   - `Generate(g, rng)`：用 `g.Add(src, op, args...)` 加用例。从 Go 测试抄用例表时注明 `// from: pkg/.../xxx_test.go TestXxx`，`src` 写测试函数名。
2. 在 `govec_test.go` 的 `minedGoTests` 中列出抄过的测试函数。
3. `scripts/govec.sh <名字>` 生成，提交 `vectors/v8.5.8/<名字>.jsonl.gz`。

## decimal：抄了哪些 Go 测试

| Go 测试（`pkg/types/mydecimal_test.go`） | op |
|---|---|
| TestFromInt / TestFromUint | from_int / from_uint |
| TestToInt / TestToUint | to_int / to_uint |
| TestFromFloat / TestToFloat | from_float64 / to_float64 |
| TestToHashKey | to_hash_key、props、to_bin（`PrecisionAndFrac` 的结果作为参数） |
| TestRemoveTrailingZeros | from_string、to_hash_key（`removeTrailingZeros` 未导出；它的 digitsFrac 就是 hash key 的最后一个字节） |
| TestRoundWithHalfEven / TestRoundWithTruncate / TestRoundWithCeil | round（"HalfEven" 测试实际调用的是 `ModeHalfUp`，v8.5.8 没有 HalfEven 模式） |
| TestToString | from_string |
| TestToBinFromBin | to_bin、from_bin（含错误参数表） |
| TestDecimalBinSize | bin_size |
| TestCompareMyDecimal | compare |
| TestMaxDecimal | max_or_min（`maxDecimal` 未导出，用 `NewMaxOrMinDec(false, ...)` 代替） |
| TestNegMyDecimal | neg |
| TestAddMyDecimal / TestSubMyDecimal / TestMulMyDecimal | add / sub / mul |
| TestDivModMyDecimal | div（fracIncr 5 和 4）、mod |
| TestMaxOrMinMyDecimal | max_or_min |
| TestShiftMyDecimal | shift（第二张表在 Go 中以 `wordBufLen=2` 运行；这是未导出的全局变量，包外无法设置，这里按默认的 9 运行，`src` 中注明） |
| TestFromStringMyDecimal | from_string（第二张表 `wordBufLen=1`，同上） |
| TestMarshalMyDecimal | marshal_json、unmarshal_json |
| `mydecimal_benchmark_test.go`：BenchmarkRound、BenchmarkMyDecimalToBin/ToHashKey、BenchmarkToFloat64 的随机输入形状 | round、to_bin、to_hash_key、to_float64（随机部分改为固定种子） |

没有抄的：`TestReset`（检查写入一个用过的 `to` 与写入新 `to` 结果相同，属于内部复用的性质，没有输入输出表；Rust 侧作为单元测试移植）。

## Rust 侧回放的比对规则（decimal）

- **按目标选文件**：Rust 的 `target_arch` 为 `x86_64` 时回放 `amd64/`，`aarch64` 时回放 `arm64/`；其他架构没有向量。Rust 的 `ToFloat64` 按映射规则 `M-FMA` 在 aarch64 上对这两处用 `f64::mul_add`，在 x86_64 上分开乘和加。

- `s`、`str`、`err` 必须一致。错误比对 `code` 和 `msg`，不比对 Go 的错误对象身份。`msg` 是函数本身返回的未填充模板（如 `[types:1265]Data truncated for column '%s' at row %d`），由上层填充；Rust 在这一层也必须返回同样的未填充模板，填充留给上层，否则上层的文案对照会错位。`err.name = "other"`（非 terror 错误，如 `encoding/json` 的错误）只要求 Rust 也返回不带 MySQL 错误码的错误。
- `st`（内部状态）也必须一致：chunk 的线上格式就是 Go `MyDecimal` 的 40 字节内存布局（N5a），所以 `digitsInt` 按字取整之类的细节在 Rust 中也要保持。
- 别名记录（`*_alias`、`round_inplace`）：Rust 的借用规则不允许 `&x, &y, &mut x` 同时存在，按映射规则 `M-ALIAS`，调用方先复制被别名的输入（`MyDecimal: Copy`）再调用、或使用原地入口（如 `round_in_place`）；输出必须与 Go 的别名调用一致。
- 链式记录：Rust 同样把第一步的结果对象直接交给第二步，不经字符串；错误取法同上。
- 脏接收者记录：Rust 先对同一个对象 `from_string(脏值)`，再在它上面调用 op（或把它作为 `to`），不重置。
- `err.name = "panic"` 的记录：Go 在这里因 bug 而 panic（越界等），执行层 recover 后变成报错。Rust 同样 panic（映射规则 `M-PANIC` 只把控制流用途的 panic 改为 `Result`），由 task 边界转换；回放时用 `catch_unwind` 检查"发生了 panic"，不比对内容。这些记录同时作为上游 bug 候选登记。

## decimal：生成时发现的 Go 行为（移植时的坑）

- **会 panic 的输入**（记录为 `err.name="panic"`）：操作数占满 9 个 word（81 位）时 `mul` 越界，如 `0.1 × 9…9(81)`；`0.9…9(72) × 0.9…9(72)` 触发 makeslice 错误；`to_bin` 在 frac > precision 或 precision = 0 时；`from_bin` 在 precision 82 或 precision 0 且输入非空时；`shift` 对 `1e-80` 做负向移位时。
- **`digitsInt` 按字取整**：`FromInt`、`FromUint`、`Round` 写到新对象时是 9，而不是实际位数（`FromInt(0)` 也是 9）；`round_inplace` 得 1。输出的值相同，内部状态不同。
- **v8.5.8 没有 HalfEven**：名为 `TestRoundWithHalfEven` 的测试实际用 `ModeHalfUp`（远离零：−15.5 → −16、2.5 → 3）。`ModeCeiling`（值为 0）对负数也远离零（−15.1 → −16），测试里有 "TODO: fix me"。
- **`String()` 与 `ToString()`**：`resultFrac` 与 `digitsFrac` 不同时输出不同（add/sub/mul/div/mod 结果中有 2,151 条）。
- **负零归一**：`"-0"` 解析为非负；`-0` 与 `0` 比较相等，哈希键相同。
- **`FromString` 的两种错误**：空串或非法输入（`""`、`"."`、`"+-1"`、`"abc"`、全角数字）返回 1292 `ErrTruncatedWrongVal`；尾部有垃圾（`"1x"`、`"0x10"`）返回 `ErrTruncated` 和已解析的值；指数超过 uint64 返回 `ErrBadNumber`；`1e2147483647` 返回 81 个 9 和 `ErrOverflow`。
- **`ToInt` 向零截断**并报 `ErrTruncated`；`ToUint("-0.4")` 报 `ErrOverflow`，不是截断。
- **错误文案保留未填充的占位符**，如 `Data truncated for column '%s' at row %d`。
- **别名调用与写入新对象结果相同**：1,795 条 `*_alias` 记录的输出、错误与对应的非别名调用完全一致（`validateArgs` 先复制被别名的输入）。会 panic 的输入在别名形式下同样 panic。
- **非 `FromString` 来源的对象，后续结果会不同**（链式记录与"把第一步结果转成字符串再解析"相比，2,184 条中约 350 条不同）：`props` 的 `digits_int` 是 9 而不是实际位数；`round_inplace`、`mul` 等结果的 `st` 继承了按字取整的 `digitsInt`（`FromInt(1)` 再乘 1，`wordBuf[1]` 残留 1）；乘法溢出（`ErrOverflow`）后结果是 `digitsInt=81` 的 `-0`，它再 `to_float64` 得 `-0`、与 0 比较得 −1、`to_hash_key` 得 `8000`；乘法截断（`ErrTruncated`，`digitsFrac=31`）后 `to_hash_key` 只得 `1f`。这些都是 Go 的实际行为，Rust 移植须保持。
- **`ToFloat64` 的结果与平台有关**（FMA，见"目标平台"）：只影响 `resultFrac < digitsFrac` 且 digitsInt + digitsFrac ≤ 12 的对象，即除法结果（及以它为输入的加减）。SQL 中 `CAST(a/b AS DOUBLE)`、对除法结果做浮点运算会走到这里。
- **`DecimalDiv` 的 int8 回绕**：`resultFrac = min(from1.resultFrac + int8(fracIncr), 30)`，`fracIncr` 大时会回绕。

## base（块 D00.2：常量、错误码与错误）

领域 `base` 覆盖 `pkg/parser/mysql`、`pkg/parser/terror`、`pkg/errno`、`pkg/util/dbterror`（含 `exeerrors`、`plannererrors`）和 `kv.StoreType`。`pkg/config` 是 D00.2 的第二部分，在领域 `config` 中（见下）。一个块可以分在几个领域：`TestBlockCoverage` 按块汇总所有列出该块的领域，块内每个函数在某个领域被覆盖或豁免即可。

### 两类记录

- **整表**（op `table`）：Go 运行时的整张表，按键排序，如 `mysql.MySQLErrName`、`errno.MySQLErrName`、`mysql.MySQLState`、字符集与排序规则表、SQL mode 表、权限表、错误注册表 `terror.ErrClassToMySQLCodes`、全部 395 个 `dbterror` 错误原型。tidb-rs 中这些表由 `cmd/rsgen` 从 Go 源码生成，这些记录核对生成结果与 Go 运行时的内容一致。
- **随机格式化**（op `sprintf_rand`，20,049 条）：随机的标志、宽度、精度、`*`、参数下标和动词，类型随机的参数（含非 UTF-8 字符串和字节、float32、特殊浮点、恰好落在两个最短表示正中间的浮点 k/2^n），以及很大的 `*` 宽度和精度（65,535 到 2,000,000，Go 的上限是 1e6）。来自 tidb-rs PR #6 评审中的差分 fuzz。
- **调用**：`fmt.Sprintf`（两张错误信息表和全部原型的每个模板，配上与动词对应、偶尔不对应的随机参数；各种动词、标志、宽度、精度、参数下标）、`mysql.NewErr/NewErrf`、`terror`/`dbterror` 的 `NewStd` 与 `GenWithStackByArgs`（三种脱敏模式）、`ToSQLError`、`EqualClass`、`ErrorEqual`、SQL mode、权限、字符集、类型默认长度、`FORMAT` 的 en_US 格式化、`errno` 的错误统计、`unicode.IsOneOf(RangeGraph)` 与 `unicode.IsDigit`。

**Go 的 string 是字节串**：`encoding/json` 会把非 UTF-8 的字符串悄悄替换成 U+FFFD。所以记录中的 Go 字符串用 `vec.GoString`：合法 UTF-8 时是 JSON 字符串，否则是 `{"hex": "<字节>"}`；回放方两种形式都要接受。`vec.CheckUTF8` 在生成时检查每条记录（参数、输出、`err.msg`），出现不是 `GoString` 的非 UTF-8 字符串就 panic，对所有领域生效。带类型的字符串参数同理写成 `{"t":"string","hex":…}`。

带类型的参数（`fmt` 和错误 API 的 `...interface{}`）编码为 `{"t": <Go 类型>, "v": <值>}`，t 取 `string int int8 … uint64 float64 float32 bool nil bytes error`（`error` 是 `errors.New(v)`，即 pingcap/errors 的 `*errors.fundamental`，它的 `Format` 方法只对 `%v %s %q` 输出）。

### 错误注册表

Go 在各个包初始化时注册错误类和错误码（`terror.ErrClass.initError`），tidb-server 调用 `terror.RegisterFinish` 冻结，此后注册会 panic。所以注册表由链接进来的包决定。`internal/protos` 空导入 `cmd/tidb-server` 的包集合（session、server、executor、ddl 等），govec 进程中的注册表与 tidb-server 相同（656 个错误码）。这使 govec 需要 cgo：macOS 上 `CC=/usr/bin/clang`；交叉编译 `CGO_ENABLED=0` 也能构建。

### 生成 Rust 表（cmd/rsgen）

```sh
cd govec
CC=/usr/bin/clang go run ./cmd/rsgen -protos     # 重写 internal/protos/protos_list.go（dbterror 原型按名字）
CC=/usr/bin/clang go run ./cmd/rsgen -out <tidb-rs>/crates/base/src
(cd <tidb-rs> && cargo fmt -p tidb-base)
```

rsgen 只生成数据，不生成逻辑：常量（名字、顺序、注释来自 Go AST，值来自 go/types 对源码求值）、map 字面量（按键排序的静态表）、错误注册表和错误原型（运行时值）、`unicode` 类别表和 `strconv.IsPrint` 的可打印范围表（对每个码位调用 Go 的 `IsPrint` 得到）。生成的文件保留 Go 标识符（映射规则 M-GONAME），文件头带 `#![allow(non_upper_case_globals)]`。函数都是手工移植的。

### 目标平台

本领域没有会被 gc 融合成 FMA 的浮点运算，两个目标的记录相同（只有文件头不同）。amd64 的文件取自 tracker CI 在 `ubuntu-latest` 上的产物（`Fresh vectors of this target` 步骤上传的 artifact），因为作者的 arm64 Mac 不能运行 amd64 程序。

## config（块 D00.2 第二部分：`pkg/config`）

领域 `config` 覆盖 `pkg/config`（`config.go`、`config_util.go`）、它使用的 BurntSushi/toml 解码器，以及归属调整后并入 D00.2 的 `metrics.ExecuteErrorToLabel`。

- **`load`**：把 TOML 文本写入临时文件 `govec-config.toml`，对 `NewConfig()` 的去别名副本（`NewConfig` 浅拷贝 `defaultConf`，Go 中写入默认切片或 map 会改到默认值本身）执行 `Load`，再执行 `Valid`。输出 `Load` 的错误及其 Go 类型、未解码项、`ConflictOptions` / `DeprecatedOptions`（每条记录前清空）、`Valid` 的错误，以及 `Valid` 前后的逐字段转储（`Config.Log.File.FileLogConfig.MaxSize=300`；字符串 `%q`，浮点 `strconv` 的 `'g'`，`nullableBool` 展开成两个字段）。解码出错时只记录错误。
- **语料**：`testdata/config/config.toml.example`（v8.5.8 的示例配置）、`testdata/config/config_test_toml.json`（`mine.go` 从 `config_test.go` 抽出的 TOML 片段），以及生成的变体：每个字段配各类 TOML 值、大小写折叠的键、未知键、已移除的键、移到 `[instance]` 的键、`Valid` 的每条边界、lexer/parser 边界、随机破坏的片段。
- **其他 op**：`GetJSONConfig`、`CloneConf`、`MergeConfigItems`、`FlattenConfigItems`、`RemovedVariableCheck`、`ContainHiddenConfig`、`nullableBool` / `AtomicBool` 的文本与 JSON、`ToLogConfig`、`ToTracingConfig`、`GetTiKVConfig`、全局配置函数、`UpdateTempStoragePath`、`ExecuteErrorToLabel`。
- **Go 的不确定性**：BurntSushi 按 Go map 的随机顺序解码表，`ErrConfigInstanceSection.Error` 和 `Valid` 的存储列表打印 map。结果依赖顺序的用例不入向量，由 `loadDeterministic` 按结构判断：每个叶子单独解码，出错的叶子不超过一个；同一表中没有大小写折叠后相同的键；每节最多一项迁移到 `[instance]`；`Valid` 不在存储名上失败。（多次运行取一致的办法不可靠：小 map 的迭代顺序并不均匀。）
- **环境**：默认的 `tmp-storage-path` 取决于 `os.TempDir()` 和用户 ID，记录中写成 `<TEMP_STORAGE_DIR>`；`temp_storage` 的结果中写成 `<TMPDIR>`、`<UID>_tidb`。记录与 `TMPDIR` 无关（已在两种 `TMPDIR` 下核对）。
- **Go 运行时与标准库**（tidb-rs 的 `gostd`）：`f2i` 是浮点到各整数类型的转换（映射规则 M-F2I，越界和 NaN 的结果随目标不同，两个目标的文件在这些记录上不同）；`simple_fold_table`、`case_table` 是全部码位的 `unicode.SimpleFold` / `ToUpper` / `ToLower`；`equal_fold` 是 `strings.EqualFold`（折叠轨道上的字符与随机组合）。rsgen 生成 `CASE_RANGES`（`unicode.CaseRanges`）和 `FOLD_ORBIT`（`SimpleFold` 不等于 ToLower/ToUpper 回退值的码位，即 Go 的 `asciiFold` 与 `caseOrbit`）。
- **豁免**：`InitializeConfig`（`os.Exit`、`terror.MustNil`）和只被它调用的 `isAllRemovedConfigItems`，由 tidb-rs 的单测覆盖。

## time（块 D01.3：`pkg/types` 的 time.go、core_time.go、fsp.go）

领域 `time` 覆盖 D01.3 的 202 个函数，以及它们依赖的 Go `time` 包行为：`time.Location` 的时区查找（`Zone`、`ZoneBounds`）、`time.Date` 的规范化和夏令时处理、`Round`、`AddDate`、`Time.String`。

**值的编码**
- `types.Time` 记成 64 位原始字（内嵌的 `coreTime`，含 fsp/类型位），即 `"raw"`。输出另加 `"s"`（`String()`）。
- `types.Duration` 记成 `{"d": 纳秒, "fsp": n}`，输出另加 `"s"`。
- `time.Time` 输入是 `{"unix", "nsec", "zone"}`；输出另加 `"loc"`（Location 名）和 `"s"`（`String()`）。
- 上下文参数是 `[flags, zone]`，Go 侧为 `types.NewContext(types.Flags(flags), loc, h)`。`h` 收集 warning，写入记录的 `warnings`。

**时区**
- zone id 有四种：
  - `UTC`：即 `time.UTC`，Go 中按指针比较。
  - `fixed:<名字>:<偏移秒>`：`time.FixedZone`。
  - `slim:<文件>`：`testdata/tz/slim/` 中的 TZif 文件，取自 Go 1.25 的 `lib/time/zoneinfo.zip`。这类文件只记录到 2007 年左右的转换，之后的时间靠文件尾的 POSIX TZ 规则计算，所以会走到 `tzset` 那条路径。
  - `fat:<文件>`：`testdata/tz/fat/` 中的 TZif 文件，取自 macOS 的 `/usr/share/zoneinfo`。转换列表一直列到 2037 年。
- 两类文件都由 `time.LoadLocationFromTZData` 加载，名字是把文件名中的 `_` 换成 `/`。
- 向量开头为每个 TZif id 生成一条 `zone` 记录，内容是文件本身。Rust 回放时加载完全相同的字节，所以结果与机器上装的 tz 数据库无关。

**用例**
- **测试中的字面量**：`testdata/time_literals.tsv` 由 `go run ./cmd/minelits <tidb>/pkg/types/{time,core_time,fsp}_test.go` 生成，是这三个测试文件中全部字符串和整数字面量，共 66 个测试函数、1354 个字面量。每个字面量都送进所有吃字符串或整数的 op；`src` 记为所在的测试函数。
- **夏令时转换**（`sys/dst`）：取 9 个有夏令时的时区在 6 个年份里的转换日，构造当天凌晨各时刻（落在跳过或重复的那段时间里）的字符串，送进 `ParseTime`（TIMESTAMP 和 DATETIME）、`Convert`、`DateTimeIsOverflow`、`RoundFrac`、`ConvertTimeZone`。
- **随机用例**：各种分隔符、紧凑数字、小数和时区后缀的日期串，畸形串，数字，浮点串，时长串，原始字（含越界字段和非法 fsp 位，`String()` 会越界 panic，记为 `panic`），格式串，以及区间值。

**豁免**：`CurrentTime`、`Duration.ConvertToTime`、`Duration.ConvertToYear` 读墙上时钟，同一路径由给定时刻的 `from_go_time`、`dur_to_time`、`dur_to_year` 覆盖。

**目标平台**：`ParseTimeFromFloat64` 中的 `int64(f)`、`uint32(...)` 按 M-F2I 依平台而定（NaN、±Inf、越界），所以 amd64 和 arm64 的文件在这几条记录上不同。

**Rust 侧比对**：`out` 按 JSON 逐项相等；错误和 warning 按错误码和消息比对。`Time.UnmarshalJSON` 的 JSON 语法错误文本没有移植（与 MyDecimal 相同），只要求同样是不带错误码的错误；类型错误（`json: cannot unmarshal ...`）的文本要一致。

## parser_types（块 D02.3：`pkg/parser` 的 model、types、auth、duration）

D02.2 标为 DONE，但 `parser/model`、`parser/types`、`auth.UserIdentity`、`duration` 在 tidb-rs 中只有 PoC 手写替身；D02.3 是它们的忠实移植，meta/model（D06.1）依赖它。op 和参数见 `domains/parsertypes.go` 文件头。

- `ft`：用 setter 构造的 FieldType（类型、flag、flen、decimal、charset/collation、元素、二进制字面量标志、array 的组合），记录所有只读方法：`CompactStr`、`InfoSchemaStr`、`String`、`Restore`、`RestoreAsCastType`、`EvalType`、`MarshalJSON` 等。`strict` 参数切换 `TiDBStrictIntegerDisplayWidth`。
- `ft_unmarshal`：`FieldType.UnmarshalJSON` 的 Go 解码规则：先精确、再按 `foldName` 不区分大小写匹配字段（包括 `ſ`→`S`），`null` 不改变字段，数字按字面量解析，溢出或非整数给出 `UnmarshalTypeError`，第一处错误之后的字段照样解码。Rust 侧语法错误只要求报错，文本不比对（`*json.SyntaxError` 的消息未移植）；类型错误比对全文。
- 元素可以不是 UTF-8（ENUM 的二进制字面量）：`MarshalJSON` 把每个无效字节写成 `�` 转义，`CompactStr`（`format.OutputFormat` 按 rune 遍历）写成 U+FFFD。
- `duration`：`ParseDuration`。很大的值经过 `time.Duration(float)` 转换，结果依赖目标平台（M-F2I）。

## vector（块 D01.6：`pkg/types` 的 vector.go、vector_functions.go）

领域 `vector` 覆盖 D01.6 的 31 个函数（`init` 豁免：Go 在 init 中检查小端，Rust 是编译期断言），以及 `ParseVectorFloat32` 用到的 jsoniter（v1.1.12）解析器。

**值的编码**
- 向量记成序列化字节（`ZeroCopySerialize`）的十六进制。输入向量由 `ZeroCopyDeserializeVectorFloat32` 还原，所以可以含 NaN、±Inf（`CreateVectorFloat32`、`ParseVectorFloat32` 拒绝它们，算术函数不检查输入）。
- 输出向量是 `{"data", "s": String(), "ts": TruncatedString(), "len", "zero": IsZeroValue(), "size": SerializedSize(), "mem": EstimatedMemUsage()}`。
- float32 输入和 float64 输出（距离、范数）记成 IEEE 位模式，因为 NaN、±Inf 不是 JSON 数。NaN 的位也要一致：Go 的 `math.NaN()` 是 `0x7FF8000000000001`；`NegativeInnerProduct` 的 `d * -1` 被 gc 改写成取负，NaN 的符号位也会翻转。

**用例**
- Go 测试中的向量文本：`pkg/types/vector_test.go`、`pkg/expression/integration_test`、`pkg/server/tests/commontest` 中出现的全部向量字面量（`src` 为 `lit/go-tests`）。
- jsoniter 解析的边界（`sys/edge`）：快路径（不超过 6 位小数时用整数除以 10 的幂，不是精确解析）与慢路径（`strconv.ParseFloat`）的分界、前导 `+`、前导零、`validateFloat` 的各条错误、`null` / `nul`、数组后的多余字节（不检查）、空白、无效 UTF-8、float32 溢出成 Inf（`[1e39]` 被接受）、float64 溢出（报错）、16383/16384 维。
- 随机向量文本、对文本的随机增删改、快慢路径分界附近的随机数字。
- 反序列化任意字节：头部长度按 uint32 计算 `elements*4 + 4`，`elements >= 2^30` 时会回绕。回绕后的长度为 0 或 4 时 Go 在 `Len` / `Elements` 中确定地越界 panic（记为 `panic`）；回绕后不小于 8 且不超过输入长度时，Go 得到长度字大于数据的向量，`Elements` 读越界（未定义行为），这类输入不生成。
- 距离、范数、`Add`/`Sub`/`Mul`、`Compare`：随机维数（多数不超过 64，少量到 300），元素取小整数、[-1,1]、两位小数、任意有限位模式、大跨度指数，部分含 NaN、±Inf、最大值、非规格化数；包括维数不同、零向量、相同向量、前缀向量。

**目标平台**：距离函数的累加 `distance += x * y`（vector_functions.go:44、71、100–102）和 `L2Norm` 的 float64 累加（:152）在 arm64 上被 gc 融合成 FMA，在 amd64（GOAMD64=v1）上不融合（`data/go_fma_sites.tsv`，M-FMA），所以两个目标在许多 `distance` 记录上不同。NaN 的结果另外受硬件规则影响（tidb-rs PR #16 的 Lucky 评审）：x86 SSE 遇到两个 NaN 时返回第一个源操作数（目标寄存器）；ARM 先按操作数顺序（FMADD 为 addend、Rn、Rm）找 SNaN，没有再找 QNaN，且 addend 是 QNaN、乘积是 ∞×0 时给出默认 NaN；无效运算的默认 NaN，x86 是 `0xFFC00000`，ARM 是 `0x7FC00000`。`targetDiffs` 按 `diffKind` 分类登记（共 1,851 条）：数值不同（M-FMA，888 条）、只有一个目标是 NaN（M-FMA：未融合的乘积溢出成 Inf 后 Inf−Inf，295 条）、只差符号位（默认 NaN，124 条）、payload 不同（两个 NaN 操作数的传播规则，544 条）。`L2Norm` 的 float64 累加虽然也被融合，但两个 float32 的乘积在 float64 中是精确的，融合与否结果相同。

**NaN 位型**（Lucky 评审后放大，种子 31337，共 91,762 条）：`vectorBits` 包括带 payload 的 QNaN（`0x7fc12345`、`0xffd00001`）、SNaN（`0x7fbfffff`、`0x7f800abc`、`0x7f800001`）、负的 SNaN（`0xff800001`、`0xffbfffff`、`0xff812345`）、全 1（`0x7fffffff`、`0xffffffff`）、±Inf、最大值等；含特殊值的向量中每个元素有 3/10 的概率取这些位型；随机部分是放大前的 10 倍。

**Rust 侧比对**：`out` 按 JSON 逐项相等，错误按消息比对（都是不带错误码的 `errors.Errorf`），`panic` 记录在 Rust 中也必须 panic。

## collate（块 D01.9：`pkg/util/collate`、`pkg/parser/charset/charset.go`、`pkg/util/stringutil`）

领域 `collate` 覆盖 D01.9 的 collate 包，加上整文件移植的 `pkg/parser/charset/charset.go`（D02.2）和 `pkg/util/stringutil/string_util.go`（D00.3），后两个通过 `vec.Domain.Files` 登记，`TestBlockCoverage` 要求这些文件的每个函数都被覆盖或豁免（`<file>:<Symbol>` 形式）。

**全局状态**：大多数 op 的第一个参数是 `enabled`，回放前先调用 `SetNewCollationEnabledForTest`，所以每条记录都不依赖其他记录（oracle 抽样回放的前提）。

**用例**
- `key_digest`：对 binary、utf8mb4_bin、utf8mb4_0900_bin、utf8mb4_general_ci、utf8mb4_unicode_ci、utf8mb4_0900_ai_ci、gbk_bin、gbk_chinese_ci，在每 0x1000 个码位的区间上做 `Key`、`KeyWithoutTrimRightSpace` 和与前一个 rune 的 `Compare`，取 SHA-256；调用 panic 时记一个 0xFF 字节；surrogate 按 Go 的 `string(rune)` 变成 U+FFFD。这一类记录对生成的权重表做全码位校验。
- 字面量：collate、stringutil、charset 的 20 个 Go 测试函数中的字符串（`cmd/minelits`，在 `minedGoTests` 中登记），在全部 13 个排序规则、新排序规则开和关两种状态下做 `key` / `compare` / `pattern`。
- 随机字符串：从一张"零件表"中拼接，零件包括 ASCII 大小写、空格、Latin-1 与扩展拉丁字母（重音、ß、连字）、大小写折叠的特例（Kelvin K U+212A、Angstrom U+212B、长 s、大写 ß U+1E9E）、希腊文、西里尔文、组合附加符、GBK 内外的汉字、韩文、符号（customGBK 拒绝的 €）、U+FFFD、补充平面（emoji、CJK 扩展 B、0900 表越界的 U+2CEA1）、控制字符（UCA 中权重为零），以及无效 UTF-8（孤立的后续字节、超长编码、截断、surrogate、大于 U+10FFFF）。有 1/5 的概率在末尾加空格（PAD SPACE 与 NO PAD 的区别）。比较用的第二个串多数取第一个的近似变体（大小写翻转、增删一个零件、末尾加空格），使比较常常相等或很晚才分出大小。
- `pattern`：随机模式（`_`、`%`、转义字符，包括 0、0x80、0xff 这类非 ASCII 的转义字节），同时记录 `CompilePattern` / `CompilePatternBinary` 的结果。
- 其余：`collator`（Go 的 `%T` 和 Clone）、`collation_id` / `collation_name` / `compatible` / `supported`、`charset` / `charset_by_id` / `charsets`、`gbk_encode`，以及 stringutil 的各函数。

字符串参数一律用 `vec.GoString`（无效 UTF-8 记为十六进制）。名字（排序规则名、字符集名）只用 UTF-8 字面量。

**目标平台**：没有浮点运算，两个目标的记录相同。amd64 的文件取自 tracker CI 的产物。

**Rust 侧比对**：单个 `#[test]` 顺序回放（排序规则开关是全局状态）；`key_digest` 在 Rust 中按同样的算法重算；`collator` 的 `%T` 按类型映射表比对；错误按错误码加消息比对；`panic` 记录在 Rust 中也必须 panic。

## json（块 D01.5：`pkg/types` 的 json_binary.go、json_binary_functions.go、json_path_expr.go；例外 A4）

`types.BinaryJSON` 与 `types.JSONPathExpression`。JSON 参数有两种写法：GoString 文本（用 `ParseBinaryJSONFromString` 解析），或 `{"k": 类型, "v": 值}`，由 `CreateBinaryJSON` 从 Go 值构造。类型有 `int64 uint64 float64(IEEE 位) string nil bool opaque([类型码, hex]) time([CoreTime 位, mysql 类型]) duration([纳秒, fsp]) array object([[键, 值], ...])`。JSON 输出是 `{"tc": 类型码, "hex": Value 的 hex, "s": String()}`。路径参数是 GoString 列表，用 `ParseJSONPathExpr` 解析，解析错误就是该记录的错误。

op：`parse valid unmarshal create type unquote unquote_string hash compare is_zero depth keys get_value peek path_parse extract modify array_insert remove merge merge_patch contains overlaps search walk`，参数和覆盖的 Go 函数见 `domains/json.go` 的 `jsonCoverage`。`valid` 是 `json.Valid`，`unmarshal` 是 `BinaryJSON.UnmarshalJSON`（只喂 `json.Valid` 通过的文本：语法错误的文案不移植）。

输入：
- **Go 测试字面量**：`testdata/json_literals.tsv` 由 `go run ./cmd/minelits <tidb>/pkg/types/{json_binary,json_binary_functions,json_path_expr}_test.go` 生成（26 个测试函数、426 个字面量）。以 `$` 开头的做 `path_parse`，首字符像 JSON 的（`{["-0-9tfn` 或空串）做 `parse`，其余（结构体字段名、消息）丢掉；同一测试函数内，文档两两做 `compare`/`contains`/`merge`，文档×路径做 `extract`/`remove`/`array_insert`/`modify`。`src` 为测试函数名，在 `minedGoTests["json"]` 登记。
- **边界**：数字边界、转义、非法 UTF-8、孤立代理项、深度 99–10001、键长 65535/65536 以及错误先后顺序；`sys/deep`：嵌套超过 `maxJSONDepth` 的文本（100–10001 层），包括出错先后顺序（前面元素、较小 key 的错误先报，对象的全部 key 先于值检查，重复 key 后者生效）和非法的深文本；`sys/dup`：父路径含 `**` 或范围、同一个值被命中两次的 `array_insert`/`remove`/`modify`/`extract`（tidb-rs #21 评审）。
- **随机**：带种子的随机文档（嵌套、重复键、空白）和它们的随机变异、随机路径；`lucky/big`：1–400 个 key 的大对象（公共前缀、多字节、200 字节长 key、重复 key），60 个、值只取标量（每条记录都带完整文档，控制向量体积）。

除 `parse`/`valid` 外，各 op 的文档参数只取 Go 能解析的文本。

生成时看到的 Go 行为（移植时的坑）：
- `\ud800` 这类孤立代理项经 `JSON_UNQUOTE`（`decodeEscapedUnicode`）或带引号的路径键会 panic：`utf8.RuneLen` 返回 -1，`char[0:-1]` 越界。记录为 panic，Rust 侧同样 panic。
- 路径 `$."\` 这类以反斜杠结尾的带引号键，`readWhile` 会越过末尾，切片越界 panic。
- `PeekBytesAsJSON` 对只有类型码的 opaque 输入越界 panic。

## chunk（块 D01.7：`pkg/util/chunk`）

`Chunk`、`Column`、`Row`、`Codec`/`Decoder`、`List`、迭代器、比较函数和行复制函数。两个 op：

- `script`：`args[0]` 是一串步骤，在若干 chunk、list、decoder（用小整数编号）上依次执行；`out` 每步一项：无结果的修改为 `null`，有结果的为值或 dump，错误为 `{"err": msg}`，Go panic 为 `{"panic": msg}` 并结束脚本（Rust 侧必须同样 panic，不比消息）。步骤列表见 `domains/chunk.go` 的 `chunkStep` 注释。
- `type_width`：`GetFixedLen` 与 `EstimateTypeWidth`。

**列类型**写作 `[tp, flag, collate, flen]`（`types.NewFieldType` 后再设置这三项）。**值**按列类型编码：整数为十进制字符串，浮点为位模式的十进制字符串，decimal 为字符串（`FromString`），时间为 `[CoreTime, tp, fsp]`（`NewTime`），duration 为 `[纳秒, fsp]`，字节串为 hex，JSON 为文本（`ParseBinaryJSONFromString`），向量为 float32 位模式列表，enum/set 为 `[名字 hex, 值]`，`null` 为 NULL。

**dump** 是 `{n, nc, v, cap, req, full, inc, sel, mem, enc, alias, cols}`：`NumRows`、`NumCols`、`GetNumVirtualRows`、`Capacity`、`RequiredRows`、`IsFull`、`IsInCompleteChunk`、`Sel`、`MemoryUsage`、`Codec.Encode` 的 hex、每个槽位第一个指向同一 `*Column` 的槽位，以及每列的 length 和四个切片（`nullBitmap`、`offsets` 的小端字节、`data`、`elemBuf`，用 reflect 读）连同各自的 cap。也就是说，Go 切片的容量（`growslice` 的增长与 size class 取整）、NULL 单元下面的字节（定长列追加 NULL 时写入的是 `elemBuf` 里上一次写入的值；`ResizeX(n, true)` 复用容量时露出的旧字节）都在比对范围内，Rust 移植按 Go 的规则复现（D01.7 spec §3）。

**生成**：生成器一边构造脚本一边在自己的状态上执行，所以下标都合法；某步在生成时 panic 就在它之前结束脚本。不会写入被两个 chunk 共享的列，也不会写入同一 chunk 中被两个槽位共享的列（`Reset` 和交换类步骤除外）：Go 通过别名写入，Rust 在 debug 构建下对这种写入 panic（N5a 3.2）。输入：
- **Go 测试场景**：chunk_test.go、column_test.go、codec_test.go、chunk_util_test.go、iterator_test.go、list_test.go 中 20 个测试的场景写成脚本，`src` 为测试名，在 `minedGoTests["chunk"]` 登记（`domains.ChunkMinedTests`）。
- **随机脚本**：1,600 个，每个 8–37 步，列类型从 21 种中随机取（各种整数、浮点、decimal、三种时间、duration、三种排序规则的字符串、blob、JSON、向量、enum、set、bit、NULL 类型），值含边界（NaN 位模式、±Inf、-0、MaxInt64、非法 UTF-8 字节）。

**Datum / MutRow**（D01.1 合并后的后续 PR，`domains/chunk_gen2.go`）：`AppendDatum`、`GetDatum*`、`ToString`、`Compare`、`LowerBound`/`UpperBound` 和 `mutrow.go` 全部，由加在原有脚本之后的 600 个随机脚本和 5 个 Go 测试场景覆盖（原有记录不变）。datum 写作 datum2 领域的 `[kind, payload…]`，Go 动态值写作 `goValueIn` 的 `[type, payload]`。Go 的 `ShallowCopyPartialRow` 让 MutRow 与源列共用缓冲，Rust 复制（spec §4 第 6 条），所以生成器在浅复制之后不再写源 chunk、也不再通过该 MutRow 写。`to_string`、`row_string`、`mr_string`、`mr_get` 在 typed getter 会读出 `cap(data)` 时记 `{"past_cap": true}`（例如对 BIT 列 `GetInt64`）；`mr_set_value(s)` 在 Go 会写出容量时（Duration/Decimal/Time 写进更短的单元）记 `past_cap` 并跳过；`AppendDatum` 只用与列同类型的 datum（更宽的定长 kind 在 Go 中会写坏内存）。

**豁免**：落盘文件和 `RowContainer` 迭代器属于 D01.10；复用池（`alloc.go` 的分配器、`Chunk.Destroy`）按 A1 的 `M-POOL` 不移植，内部实现在 tracker 中归 X，接口由 Rust 单元测试覆盖。

**读到容量之外**：`view` 步骤把列重解释成定长类型切片（`Int64s`、`Times`、`Decimals` 等，Len = length、Cap = cap(data)/宽度）。length × 宽度超过 `cap(data)` 时 Go 读到容量之外（未定义行为，读到什么取决于内存），生成器不读，记为 `{"past_cap": [length, cap]}`；Rust 侧在同样条件下必须 panic（D01.7 spec §4 第 4 条）。

**额外种子**（生成器的健壮性）：`govec chunk -seed N -o f.jsonl.gz` 用别的种子生成，输出不提交。CI 对种子 1–8 各生成一次（不能崩溃），文件作为产物 `govec-extra-seeds-<runner>` 上传；在 tidb-rs 中回放：`GOVEC_CHUNK_FILES=a.jsonl.gz,b.jsonl.gz cargo test -p tidb-chunk --test govec_chunk`。生成器构造步骤时调用 Go chunk API 的地方都在 `recover` 之内（`chunkGen.goSafe`），脚本结束（某步 panic）后构造的步骤一律被拒绝，`fill` 也不再访问未建成的 chunk。

**目标平台**：没有浮点运算，两个目标的记录应相同。amd64 的文件取自 tracker CI 的产物。

## 两个目标的差异（`TestTargetsDiffer`）

每个领域都比较已提交的 amd64 和 arm64 两个文件。不同的记录必须登记在 `targetdiffs_test.go` 的允许表 `targetDiffs` 中，并写明原因（M-FMA、M-F2I）。

**登记方式有两种**：
- 精确到一次调用：`(domain, op, args)`，其中 `args` 是记录参数的紧凑 JSON。
- 按 op 登记：`args` 写 `"*"`，并给出 `N`，即这个 op 有多少条记录不同，必须恰好相等。只用于成批生成的平台相关用例，如 decimal 的 44 条 `div_then_to_float64`、config 的 824 条 `f2i`。

**以下情况测试失败**：
- 表外的记录出现差异；
- 两个目标在同一行上的调用（op 或 args）不同；
- 表里的精确条目不再有差异，或按 op 登记的条目数量不等于 `N`。

后两种情况通常是 Go 的行为或生成器变了，需要更新表和原因。

**只有一个目标的领域**：新领域的 amd64 向量要等 PR 的第一次 CI 产出，在此之前必须登记在 `targetsPending`（领域 → 原因）中，否则测试失败；两个目标都提交后，条目必须删除，否则也失败。这样不会有领域被静默跳过。

当前登记的内容：
- decimal：44 条（M-FMA）。
- config：824 条 `f2i` 和 5 条 `load`。这 5 条都是 `grpc-keepalive-timeout` 的浮点数转无符号整数时溢出或取 `inf`（M-F2I）。
- time：3 条 `parse_time_float64`，即 NaN、+Inf、1e19（M-F2I）。
- parser_types：3 条 `duration`（`100000000000d`、`9223372036h`、`106752d`），`time.Duration(float)` 溢出 int64，amd64 得到最小值，arm64 饱和（M-F2I）。
- vector：130 条 `distance`。92 条是 arm64 上融合的 float32 累加（M-FMA）；38 条结果是 NaN：无效运算（Inf−Inf、0×Inf）产生的默认 NaN 在 amd64 上带符号位（`0xFFF8…`），在 arm64 上不带（`0x7FF8…`）。Rust 在同一硬件上得到同样的 NaN。
## model（块 D06.1：`pkg/meta/model` 的 schema 结构体与 `encoding/json`）

meta/model 的结构体以 `encoding/json` 的形式存在 TiKV，Go TiDB 与 tidb-rs 共用。核心 op 是 `roundtrip`：`json.Unmarshal` 到新值再 `json.Marshal`，tidb-rs 的解码→编码必须逐字节相同，错误也相同。op 和参数见 `domains/model.go` 文件头。

- **集群语料**：`domains/testdata/model_cluster.jsonl.gz`，从一个 v8.5.8 集群（tiup playground）的 status 端口 `/schema/{db}/{table}` 原样导出的 meta（857 张表、9 个库），建表语句是同目录的 `model_cluster_ddl.sql`：全部列类型与默认值、生成列、各种分区、placement、TTL、视图、序列、外键、check 约束、资源组。表的 JSON 是集群返回的原始字节（不经过 Python 重编码，数字文本不变）。
- **随机值**：每个结构体按反射填充（字段边界值、HTML 字符、U+2028、非 UTF-8 字节、interface 中的浮点与嵌套值、带时区的时间），`[]*T` 不含 nil 元素（tidb-rs 按值持有元素）。
- **变异**：对语料和随机值删键、重复键、改大小写、加未知键、换成错误类型或越界数字、`null`（`mutateJSON`，只在对象这一层）；Lucky 的 `luckyMutate`（评审 tidb-rs #20）在任意节点（包括数组元素）换成边界字面量，专门变异 ColumnInfo 的 interface{} 字段，做部分大写与 K/ſ 折叠的键、追加重复键，每条 1 到 3 次。种子 1618，随机值和 `mutateJSON` 各 15,000 条，`luckyMutate` 20,000 条，`into` 3,000 条。
- **出错路径**（`site/...`，93 条 `into`；Part 1 的 61 条之外，Part 2 加了 32 条：`map[ErrorID]*terror.Error` 的值 `UnmarshalJSON` 出错、`null`、旧 class，`atomic.Int64` 的 `null` / 类型错误 / 范围，`terror.Error` 的非对象值、折叠键、重复键，`json.RawMessage` 的空白与转义、`null`、标量、子 job，`HistoryInfo` 的无 tag 键，`[]*SubJob` 的 null 元素）：Go 1.25 `encoding/json` decode.go 中每个 `saveError` 和提前返回的位置各一条，基值的每个字段都有值，记录 Go 写了什么、保留了什么、是否继续解码（literalStore 的 null / bool / string / base64 / 数字进 interface、整数、浮点与错误类型；array 进非切片、元素错误、元素的 UnmarshalJSON 错误、长短数组；object 进非结构体、map 的整数键与值错误的先后；字段的未知键、重复键、折叠键、先存错误再遇 UnmarshalJSON 错误、为提升字段分配嵌入指针；CIStr、FieldType、time.Time 的 UnmarshalJSON）。
- **已知偏离**：解码后的值在某个 `[]*T` 中有 nil 元素（JSON 的 `null` 元素）时，`out` 多一个 `"known_deviation": "nil-ptr-elem"`。tidb-rs 按值持有元素，把 null 解码成零值（tidb-rs specs/D06.1-model §4.1）；这类记录还带 `"ported"`：把 nil 元素换成 `new(T)` 后的 `Marshal` 输出，回放要求 tidb-rs 的输出与它严格相等，并且在它们不再与 Go 不同时报错。
- `into`：解码到已有值（缺字段保持原值、切片重填、map 合并）。
- `sys/time`：`Time.UnmarshalJSON`（Go 1.25 的 `parseStrictRFC3339` 在快速路径失败时就是 `time.Parse(RFC3339)`，严格复查被禁用）与 `marshal_time`（`Time.MarshalJSON` 的年份、时区范围错误）。时间串不含 JSON 转义：Go 解析引号间的原始字节（go.dev/issue/47353），tidb-rs 用解码后的字符串。
- 方法 op：`table`、`column`、`column_set`、`index`、`partition`、`placement`、`resource`、`fk`、`misc`；Part 2：`job`（`Job.Decode` 与各方法；`Job.String` 中本地时区的开始时间换成 `<T>`，另给 UTC 形式，记录与机器时区无关）、`job_strings`、`reorg`、`need_restored`（新 collation 关与开）。
- **Part 2 的结构体**：`Job`、`SubJob`、`MultiSchemaInfo`、`DDLReorgMeta`、`HistoryInfo`、`TimeZoneLocation`、`TraceInfo`、`InvolvingSchemaInfo` 也参与随机值、变异与 `into`；随机值中的 terror 由 `errors.Normalize` 生成，atomic 取边界值，RawMessage 取几种合法 JSON。
- **豁免**：几个只被其他函数调用的辅助函数（经由调用者覆盖）、`IndexInfo.ConditionExpr`（返回 parser 的 AST）。Part 2 去掉了 `job.go`、`reorg.go` 和 `ColumnNeedRestoredData` 的 14 个豁免。

**Rust 侧比对**：`out` 按 JSON 逐项相等（编码结果是字符串，逐字节）；错误比对全文，JSON 语法错误只要求报错。

## sysvar（块 D05.1：`pkg/sessionctx/variable` 的定义与校验，N8）

领域 `sysvar` 覆盖全部 944 个系统变量的注册表（op `registry`）、校验（`validate` / `relaxed`，各种作用域和上下文）、`SetSysVar`、原生类型转换和 status vars。每条记录都在新 session 上执行，时区固定为 UTC、MemTotal 固定为 64 GiB，不依赖宿主机。

**首尾空白**（`sys/whitespace`，D05.1 评审）：经过 `fmt.Sscanf` 解析的变量（`parseByteSize` / `parsePercentage`：`sscanfSysvars`）和全部 TypeDuration 变量，基础输入各加上前后的空格、制表符和换行。来源是 #45：`tidb_server_memory_limit = '5 '` 被解析成 5%，而 `'5'` 是 5 字节。

**非校验 hook 的清单**：`data/sysvar_hooks.tsv`（`go run ./cmd/rsgen -sysvar-hooks ../data/sysvar_hooks.tsv`）列出每个带 SetSession / SetGlobal / GetSession / GetGlobal / GetStateValue 的变量。这些 hook 是表字面量中的闭包，不在 `func_blocks.tsv` 里，由这份清单归给 D05.2，作为 D05.2 的验收清单。

## expr（块 D08.1 第 1 部分：`pkg/expression` 的 Column、CorrelatedColumn、Constant、Schema）

- **输入**：列的类型和值沿用 chunk 领域的 `chunkTypeSpec` 与生成器（另加 BIT(8)、BIT(64)）；常量的值和 `RetType` 沿用 datum 领域的 datum 与字段类型格式，值的类型和 `RetType` 可以不同（类型推导后 Go 的计划中会出现）；`tbl` 用 model 领域的 856 份集群导出表结构（跳过含虚拟生成列的表：`BuildSimpleExpr` 由 planner 注册）。
- **调用哪些 Eval 方法**：按节点的求值类型和值的种类，只调用 Go 的调用方会调用的（`exEvalSet`），避免对不匹配的类型读出未定义的内容。
- **ParamMarker**：`order` 字段未导出，用 reflect 设置；`order` 越界时 `GetParamValue` 报错，`GetType` 返回 nil（记录为 null）。
- **豁免**：`expr_pending.go`，共 220 个：向量化方法（由第 2 部分的向量驱动取代，N4 M-EVAL）、cascades 的 `Hash64`/`Equals`（EXC.B）、`errors.go`/`context.go` 的未导出辅助函数（tidb-rs 单元测试）、第 2 部分（ScalarFunction 等）和第 3 部分（VECTOR 函数）。
- **两个目标**：3 条 `const` 记录不同（M-F2I：NaN 浮点值在 hybrid `RetType` 下走 `int64(NaN)`），登记在 `targetDiffs`。
- **固定的偏离**：`corr` 记录带 `dmem`（Data 的 `MemUsage`），tidb-rs 中关联列的值不在节点里（spec D08.1 §3.2）。

## execdetails（块 D14.0：`pkg/util/execdetails`、`pkg/util/tracing/opt_trace.go`）

- **依赖一起比对**：client-go `util` 的 detail 类型（`String` / `Merge` / `Clone`、`FormatBytes`），`influxdata/tdigest`（`Percentile` 超过 1000 个值后使用），Go 的 `math.Sin` / `math.Asin`（tdigest 的 scale 函数；`gomath` 记录比较结果位）。tdigest 和 `math` 在 arm64 上有融合乘加（`go tool objdump` 读出，tidb-rs spec D14.0 §3.1），所以两个目标的文件可能不同。
- **批量输入**：`tdigest`、`percentile`、`sync`、`coll` 的批量步骤（`gen`、`copgen`、`cops`）由 splitmix64 在两侧生成（`edSplitMix`），记录本身很小。
- **时间**：所有 duration 都是输入，不读时钟。
- **`bcop`**：`basicCopRuntimeStats` 未导出，通过 `go:linkname` 调用它的 `Clone`（`CopRuntimeStats.stats` 是第一个字段）。Go 的 `Clone` 与原对象共享百分位数的切片和 t-digest（tidb-rs 深拷贝，spec D14.0 §4 第 4 条），所以 `bcop` 步骤总在记录的最后。
- **panic**：Go 解引用 nil 或越界的地方（缺字段的 summary、nil detail 的合并、无当前规则时追加步骤、nil map、空百分位数取值）记为 panic，Rust 侧也必须 panic。分数权重下 tdigest 的 `Quantile(1)` 会越界（`processedWeight` 与累积权重按不同顺序求和），这类记录不取 q = 1。
- **豁免**：`tracing/util.go` 的 11 个函数（opentracing / `runtime/trace`，承载方式随 D05.3 与 TRACE 执行器），`MergeTiFlashRUConsumption`（DEF.MPP，需要 kvproto `resource_manager`）。
