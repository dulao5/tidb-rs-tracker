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
| `govec/govec_test.go` | 自检：文件头、每个 op 的最少条数、Go 测试函数都有记录、块内每个 Go 函数都被覆盖或豁免、oracle 与生成结果一致、两次生成逐字节相同、gzip 存储可还原、已提交的向量是最新的 |
| `vectors/v8.5.8/<领域>.jsonl`（或 `.jsonl.gz`，见"存储"） | 已提交的向量 |
| `scripts/govec.sh` | 重新生成 / 检查 |

## 记录格式

JSONL。第一行是文件头：

```json
{"govec":1,"domain":"decimal","tidb":"v8.5.8","commit":"8b857efa2","go":"go1.25.12","goos":"darwin","goarch":"arm64","seed":20260827,"records":31620}
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

op 一览（参数和输出见 `domains/decimal.go` 中的 `decimalBaseCoverage`）：`from_string from_int from_uint from_float64 to_int to_uint to_float64 round round_inplace shift add sub mul div mod neg compare to_bin from_bin bin_size peak to_hash_key props max_or_min marshal_json unmarshal_json`，以及下面两类。

**别名调用**（`to` 就是某个输入，Go 允许，`validateArgs` 先复制被别名的输入）：`add_alias sub_alias mul_alias div_alias mod_alias`，参数是原 op 的参数再加一个 `alias`：`1` 为 `f(x, y, x)`，`2` 为 `f(x, y, y)`，`3` 为 `f(x, x, x)`（此时 `b` 与 `a` 相同，只解析 `a`）。输出是调用后的 `to` 对象。`Round` 的原地调用是 `round_inplace`；`Shift` 本身就是原地的。

**链式调用** `<producer>_then_<consumer>`：前一步的结果对象（不经字符串重新解析）直接作为后一步的第一个十进制参数，用来覆盖内部状态与 `FromString` 结果不同的对象（`FromInt`/`FromUint`/`Round`/加法/乘法的 `digitsInt` 按字取整、乘法溢出后的 `-0` 等）。producer 为 `from_int from_uint round add mul`，consumer 为 `to_string to_bin to_hash_key props round round_inplace shift to_int to_float64 compare add mul div`（`to_string` 输出 `{"s","str"}`，只作 consumer 用）。`args` 是 producer 的参数后接 consumer 去掉第一个十进制参数后的参数，如 `{"op":"from_int_then_to_bin","args":[123,20,0]}`；`out` 是 consumer 的输出；`err` 取 producer 的错误，producer 无错时取 consumer 的错误（两步都执行，与忽略 `ErrTruncated` 继续计算的 Go 调用方一致）。

### Go 函数覆盖与豁免表

`Coverage.GoFuncs` 使用 tracker 的符号格式（与 `data/go_funcs.tsv`、`data/func_blocks.tsv` 一致）：`MyDecimal.FromString`、`DecimalAdd`，不写指针接收者。未导出的辅助函数列在实际调用到它们的 op 下（如 `add` 列出 `validateArgs doAdd doSub add sub`）。每个领域声明自己对应的块（decimal 为 `D01.2`），`TestBlockCoverage` 检查：块内每个函数要么出现在某个 op 的 `GoFuncs` 中，要么在豁免表 `Exempt` 里写明理由；`GoFuncs` 和豁免表中的每个符号都必须是块内的函数（防止拼错或过期）。D01.2 的 58 个函数中 56 个被覆盖（用 `go test -coverpkg` 核对过，这 56 个都确实被执行到），豁免表：

| 函数 | 理由 |
|---|---|
| `NewDecFromFloatForTest` | 测试用构造函数：`FromFloat64` 加上出错时 `log.Panic`，取值路径即 `from_float64` |
| `NewDecFromStringForTest` | 测试用构造函数：`FromString` 加上出错时 `log.Panic`，取值路径即 `from_string` |

（评审中提到的 `Reset`、`Copy` 在 v8.5.8 的 `mydecimal.go` 中不存在。）

## 重新生成

```sh
scripts/govec.sh            # 全部领域，写入 vectors/v8.5.8/
scripts/govec.sh decimal    # 只生成一个领域
scripts/govec.sh --check    # 重新生成到临时目录，与已提交文件逐字节比较（CI 跑这个）
```

工具链由 `govec/go.mod` 固定为 go 1.25.12，`GOTOOLCHAIN=auto`（默认）时 go 会自动下载。文件头记录了 Go 版本，用别的版本生成会因文件头不同而检查失败，这是有意的（`strconv`、`encoding/json` 的行为可能随版本变化）。

本地调试想直接用源码目录时，可以临时加 replace（不要提交）：

```sh
cd govec
go mod edit -replace github.com/pingcap/tidb=$HOME/src/pingcap/tidb-all-source-code/tidb-v858 \
            -replace github.com/pingcap/tidb/pkg/parser=$HOME/src/pingcap/tidb-all-source-code/tidb-v858/pkg/parser
```

macOS 上若 cgo 报找不到 `stdlib.h`（Homebrew 的 clang 在 PATH 前面），用 `CC=/usr/bin/clang scripts/govec.sh`。

## 确定性

- 随机用例用固定种子（文件头 `seed`），PCG 生成器；生成顺序就是代码顺序，不遍历 map。新加的用例组追加在已有组之后，已有记录和它们消耗的随机数序列不变。
- 输出用结构体编码（字段顺序固定），不用 map。
- `go test ./...`（在 `govec/` 下）会生成两次并比较字节，再与已提交文件比较。
- **与平台无关**：文件头的 `goos`、`goarch` 记录生成平台。已提交的文件在本地 darwin/arm64 生成，CI（ubuntu，linux/amd64）重新生成后，除这两个字段外逐字节相同（`--check` 和 `go test` 只排除这两个字段），这就是跨平台确定性的证明；CI 通过即表示向量与平台无关。
- **日志**：`domains.Quiet()` 在 `init` 中把 TiDB 全局日志级别设为 Fatal，免得 `String()` 里的 `terror.Log` 之类刷屏。它只影响日志输出，不影响结果；如果以后某个领域的 Go 代码路径依赖日志的副作用（例如某个值只在 debug 日志开启时才计算），该领域必须在自己的 Exec 中恢复日志级别，并在本 README 说明。

## 存储

向量文件按领域选择两种形式之一，由 `vec.Domain.Gzip` 决定，`govec <领域> --filename` 给出文件名：

- **不超过约 10 MB**（如 decimal，约 6 MB）：`vectors/v8.5.8/<领域>.jsonl`，直接进 git。
- **超过约 10 MB**：`vectors/v8.5.8/<领域>.jsonl.gz`，同样进 git。由 govec 自己用 Go 的 `compress/gzip` 写出：不写文件名和修改时间、固定 `BestCompression`，所以字节只取决于内容和（已固定的）Go 版本；不要用外部 `gzip` 命令重新压缩。

`scripts/govec.sh --check`（即 `govec <领域> --check <文件>`）比对的是解压后的 JSONL 内容（排除 `goos`/`goarch`），不比对压缩字节，因此压缩实现的差异不会导致误报；`TestStorage` 检查 gzip 输出确定、可还原。下游（tidb-rs）复制向量时记录 tracker commit 和所复制文件（`.jsonl` 或 `.jsonl.gz`）的 sha256。单个压缩文件超过 50 MB（GitHub 的警告线）时再改为 release asset + sha256，届时更新本节。

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
   - `Gzip`：文件超过约 10 MB 时设为 true（见"存储"）；
   - `Exec(op, args)`：解码参数、调用 Go、返回可编码的结果（用结构体，不用 map）；需要 warning 时返回 `vec.WithWarnings`；
   - `Generate(g, rng)`：用 `g.Add(src, op, args...)` 加用例。从 Go 测试抄用例表时注明 `// from: pkg/.../xxx_test.go TestXxx`，`src` 写测试函数名。
2. 在 `govec_test.go` 的 `minedGoTests` 中列出抄过的测试函数。
3. `scripts/govec.sh <名字>` 生成，提交 `vectors/v8.5.8/<名字>.jsonl`（或 `.jsonl.gz`）。

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

- `s`、`str`、`err` 必须一致。错误比对 `code` 和 `msg`，不比对 Go 的错误对象身份。`msg` 是函数本身返回的未填充模板（如 `[types:1265]Data truncated for column '%s' at row %d`），由上层填充；Rust 在这一层也必须返回同样的未填充模板，填充留给上层，否则上层的文案对照会错位。`err.name = "other"`（非 terror 错误，如 `encoding/json` 的错误）只要求 Rust 也返回不带 MySQL 错误码的错误。
- `st`（内部状态）也必须一致：chunk 的线上格式就是 Go `MyDecimal` 的 40 字节内存布局（N5a），所以 `digitsInt` 按字取整之类的细节在 Rust 中也要保持。
- 别名记录（`*_alias`、`round_inplace`）：Rust 的借用规则不允许 `&x, &y, &mut x` 同时存在，按映射规则 `M-ALIAS`，调用方先复制被别名的输入（`MyDecimal: Copy`）再调用、或使用原地入口（如 `round_in_place`）；输出必须与 Go 的别名调用一致。
- 链式记录：Rust 同样把第一步的结果对象直接交给第二步，不经字符串；错误取法同上。
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
- **`DecimalDiv` 的 int8 回绕**：`resultFrac = min(from1.resultFrac + int8(fracIncr), 30)`，`fracIncr` 大时会回绕。
