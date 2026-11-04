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
| `govec/govec_test.go` | 自检：文件头、每个 op 的最少条数、Go 测试函数都有记录、oracle 与生成结果一致、两次生成逐字节相同、已提交的向量是最新的 |
| `vectors/v8.5.8/<领域>.jsonl` | 已提交的向量 |
| `scripts/govec.sh` | 重新生成 / 检查 |

## 记录格式

JSONL。第一行是文件头：

```json
{"govec":1,"domain":"decimal","tidb":"v8.5.8","commit":"8b857efa2","go":"go1.25.12","seed":20260827,"records":27641}
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

op 一览（参数和输出见 `domains/decimal.go` 中的 `decimalCoverage`）：`from_string from_int from_uint from_float64 to_int to_uint to_float64 round round_inplace shift add sub mul div mod neg compare to_bin from_bin bin_size peak to_hash_key props max_or_min marshal_json unmarshal_json`。

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

- 随机用例用固定种子（文件头 `seed`），PCG 生成器；生成顺序就是代码顺序，不遍历 map。
- 输出用结构体编码（字段顺序固定），不用 map。
- `go test ./...`（在 `govec/` 下）会生成两次并比较字节，再与已提交文件比较。

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
   - `Coverage`：op → 最少条数和对应的 Go 函数；
   - `Exec(op, args)`：解码参数、调用 Go、返回可编码的结果（用结构体，不用 map）；需要 warning 时返回 `vec.WithWarnings`；
   - `Generate(g, rng)`：用 `g.Add(src, op, args...)` 加用例。从 Go 测试抄用例表时注明 `// from: pkg/.../xxx_test.go TestXxx`，`src` 写测试函数名。
2. 在 `govec_test.go` 的 `minedGoTests` 中列出抄过的测试函数。
3. `scripts/govec.sh <名字>` 生成，提交 `vectors/v8.5.8/<名字>.jsonl`。

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

- `s`、`str`、`err` 必须一致；错误按 `code` 比对，不比对 Go 的错误对象身份。
- `st`（内部状态）也必须一致：chunk 的线上格式就是 Go `MyDecimal` 的 40 字节内存布局（N5a），所以 `digitsInt` 按字取整之类的细节在 Rust 中也要保持。
- `err.name = "panic"` 的记录：Go 在这里因 bug 而 panic（越界等），执行层 recover 后变成报错。Rust 同样 panic（映射规则 `M-PANIC` 只把控制流用途的 panic 改为 `Result`），由 task 边界转换；回放时用 `catch_unwind` 检查"发生了 panic"，不比对内容。这些记录同时作为上游 bug 候选登记。
- 向量文件超过约 10 MB 时改为 gzip 存储。

## decimal：生成时发现的 Go 行为（移植时的坑）

- **会 panic 的输入**（记录为 `err.name="panic"`）：操作数占满 9 个 word（81 位）时 `mul` 越界，如 `0.1 × 9…9(81)`；`0.9…9(72) × 0.9…9(72)` 触发 makeslice 错误；`to_bin` 在 frac > precision 或 precision = 0 时；`from_bin` 在 precision 82 或 precision 0 且输入非空时；`shift` 对 `1e-80` 做负向移位时。
- **`digitsInt` 按字取整**：`FromInt`、`FromUint`、`Round` 写到新对象时是 9，而不是实际位数（`FromInt(0)` 也是 9）；`round_inplace` 得 1。输出的值相同，内部状态不同。
- **v8.5.8 没有 HalfEven**：名为 `TestRoundWithHalfEven` 的测试实际用 `ModeHalfUp`（远离零：−15.5 → −16、2.5 → 3）。`ModeCeiling`（值为 0）对负数也远离零（−15.1 → −16），测试里有 "TODO: fix me"。
- **`String()` 与 `ToString()`**：`resultFrac` 与 `digitsFrac` 不同时输出不同（add/sub/mul/div/mod 结果中有 2,151 条）。
- **负零归一**：`"-0"` 解析为非负；`-0` 与 `0` 比较相等，哈希键相同。
- **`FromString` 的两种错误**：空串或非法输入（`""`、`"."`、`"+-1"`、`"abc"`、全角数字）返回 1292 `ErrTruncatedWrongVal`；尾部有垃圾（`"1x"`、`"0x10"`）返回 `ErrTruncated` 和已解析的值；指数超过 uint64 返回 `ErrBadNumber`；`1e2147483647` 返回 81 个 9 和 `ErrOverflow`。
- **`ToInt` 向零截断**并报 `ErrTruncated`；`ToUint("-0.4")` 报 `ErrOverflow`，不是截断。
- **错误文案保留未填充的占位符**，如 `Data truncated for column '%s' at row %d`。
- **`DecimalDiv` 的 int8 回绕**：`resultFrac = min(from1.resultFrac + int8(fracIncr), 30)`，`fracIncr` 大时会回绕。
