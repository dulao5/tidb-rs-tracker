"""Block split draft for tidb-rs porting (Go TiDB v8.5.8).

BLOCKS: id -> (domain, title, phase). Phase: P1..P5, X (not ported), EXC (exception
spec replaces it), DEFER (scope decision D1 etc.), DONE (already ported: parser grammar).
RULES: ordered (block, file_regex, symbol_regex or None). First match wins; matching is
on the Go file path (relative to repo root) and the symbol from go_funcs.tsv.
"""

BLOCKS = {
    # ---- D00 interfaces / contexts / constants (foundation layer) ----
    "D00.1": ("D00 接口与上下文", "上下文接口骨架（sessionctx、planctx、exprctx、stmtctx、errctx、base 接口）", "P1"),
    "D00.4": ("D00 接口与上下文", "contextutil（pkg/util/context：warning、plan cache 跟踪、context ID）；位于 tidb-base（D00.1 评审：types.Context 也要用）", "P1"),
    "D00.2": ("D00 接口与上下文", "常量、错误码与配置（parser/mysql、terror、errno、dbterror、config）", "P1"),
    "D00.3": ("D00 接口与上下文", "通用工具（util/*）：不单独排期，被已排期的块用到时整文件移植（宪章 4.4）", "ON-DEMAND"),
    # ---- D01 types & encoding ----
    "D01.1": ("D01 基础类型与编码", "Datum、FieldType 与类型辅助", "P1"),
    "D01.2": ("D01 基础类型与编码", "Decimal（MyDecimal）", "P1"),
    "D01.3": ("D01 基础类型与编码", "时间与 Duration", "P1"),
    "D01.4": ("D01 基础类型与编码", "类型转换（convert）与溢出、截断", "P1"),
    "D01.5": ("D01 基础类型与编码", "JSON 类型（例外 A4）", "EXC"),
    "D01.6": ("D01 基础类型与编码", "Vector 类型", "P1"),
    "D01.7": ("D01 基础类型与编码", "Chunk 与列存（内存部分按 A1）", "P1"),
    "D01.8": ("D01 基础类型与编码", "编码：util/codec（Q3：复用 TiKV，L3）", "P1"),
    "D01.11": ("D01 基础类型与编码", "表与行编码：tablecodec、rowcodec（N0 评审：从 D01.8 拆出，位于 kv 之上）", "P1"),
    "D01.9": ("D01 基础类型与编码", "Collation 与字符集", "P1"),
    "D01.10": ("D01 基础类型与编码", "落盘容器与磁盘 chunk（按 A1）", "P4"),
    # ---- D02 parser ----
    "D02.1": ("D02 SQL 解析", "AST 节点与 Restore", "DONE"),
    "D02.2": ("D02 SQL 解析", "parser 运行时（lexer、语法动作、hint parser、format）", "DONE"),
    "D02.3": ("D02 SQL 解析", "parser model/types/auth/duration 的忠实移植（PoC 中是手写替身）", "P1"),
    # ---- D03 protocol ----
    "D03.1": ("D03 协议与连接", "连接与协议（conn、conn_stmt、server、driver_tidb、internal）", "P2"),
    "D03.2": ("D03 协议与连接", "HTTP status 接口与 handler", "P5"),
    # ---- D04 privilege ----
    "D04.1": ("D04 权限与认证", "权限检查与认证", "P5"),
    # ---- D05 session ----
    "D05.1": ("D05 会话与变量", "系统变量定义与校验（N8）", "P1"),
    "D05.2": ("D05 会话与变量", "SessionVars 与 StatementContext（N2）", "P1"),
    "D05.3": ("D05 会话与变量", "会话执行主流程（session.go、tidb.go，只读部分；PoC 会话先保留）", "P4"),
    "D05.4": ("D05 会话与变量", "事务上下文（sessiontxn：isolation、staleread，只读路径）", "P2"),
    # ---- D06 meta & schema ----
    "D06.1": ("D06 元数据与 Schema", "元数据模型（meta/model）", "P1"),
    "D06.2": ("D06 元数据与 Schema", "meta 读取（meta、structure、autoid 读取）", "P2"),
    "D06.3": ("D06 元数据与 Schema", "InfoSchema 构建与缓存（v2 按 A3）", "P2"),
    "D06.4": ("D06 元数据与 Schema", "table 抽象（tables、index、partition 的读取路径）", "P2"),
    "D06.5": ("D06 元数据与 Schema", "schema 同步与 domain（schema validator、infosync）", "P2"),
    "D06.6": ("D06 元数据与 Schema", "infoschema 内存表定义与 cluster 表", "P5"),
    "D06.7": ("D06 元数据与 Schema", "系统变量的持久化与集群全局值：mysql.global_variables / mysql.tidb 的窄写入器、sysvar cache 的读取与 etcd 通知（#58 选 (c)，D05.2 §6）", "P1"),
    # ---- D07 kv / distsql ----
    "D07.0": ("D07 KV 与 DistSQL", "kv 接口与 key 类型（pkg/kv）", "P1"),
    "D07.1": ("D07 KV 与 DistSQL", "store driver（snapshot 读）", "P2"),
    "D07.2": ("D07 KV 与 DistSQL", "coprocessor 客户端（不含 batch/MPP）", "P2"),
    "D07.3": ("D07 KV 与 DistSQL", "distsql 请求构建与结果", "P2"),
    "D07.4": ("D07 KV 与 DistSQL", "coprocessor cache（A3）", "EXC"),
    # ---- D08 expression ----
    "D08.1": ("D08 表达式", "表达式框架（Expression、Column、Constant、ScalarFunction、Schema、求值与向量化接口）", "P1"),
    "D08.2": ("D08 表达式", "函数注册、类型推导、常量折叠与传播、collation 推导", "P1"),
    "D08.3": ("D08 表达式", "下推判定与 protobuf 转换（infer_pushdown、expr_to_pb、distsql_builtin）", "P2"),
    "D08.4": ("D08 表达式", "比较函数", "P1"),
    "D08.5": ("D08 表达式", "算术、数学与逻辑运算", "P1"),
    "D08.6": ("D08 表达式", "字符串函数（含 like、regexp、字符集转换）", "P5"),
    "D08.7": ("D08 表达式", "时间函数", "P5"),
    "D08.8": ("D08 表达式", "CAST", "P1"),
    "D08.9": ("D08 表达式", "控制流、信息、杂项、加密、grouping 函数", "P5"),
    "D08.10": ("D08 表达式", "JSON 函数（A4）", "EXC"),
    "D08.11": ("D08 表达式", "聚合函数描述（expression/aggregation）", "P3"),
    "D08.12": ("D08 表达式", "表达式工具（util.go 等，按引用归属后再拆）", "P1"),
    # ---- D09 logical optimization ----
    "D09.0": ("D09 逻辑优化", "planbuilder 骨架、语句分发、preprocess、名字解析", "P3"),
    "D09.1": ("D09 逻辑优化", "SELECT 构建：数据源、投影、选择、排序、limit、通配符", "P3"),
    "D09.2": ("D09 逻辑优化", "表达式改写（expression_rewriter，AST → Expression）", "P3"),
    "D09.3": ("D09 逻辑优化", "join 构建与逻辑 join/apply 算子", "P3"),
    "D09.4": ("D09 逻辑优化", "聚合、窗口、only_full_group_by 构建与逻辑算子", "P3"),
    "D09.5": ("D09 逻辑优化", "集合运算、CTE、Expand", "P3"),
    "D09.6": ("D09 逻辑优化", "子查询与去关联", "P3"),
    "D09.7": ("D09 逻辑优化", "列裁剪与投影消除（spec-kit 试点候选）", "P3"),
    "D09.8": ("D09 逻辑优化", "谓词下推、谓词简化、外连接转内连接、约束", "P3"),
    "D09.9": ("D09 逻辑优化", "聚合相关规则（下推、消除、skew、max/min）", "P3"),
    "D09.10": ("D09 逻辑优化", "join 重排与 join 消除", "P3"),
    "D09.11": ("D09 逻辑优化", "TopN / Limit 下推与推导", "P3"),
    "D09.12": ("D09 逻辑优化", "分区裁剪（仅 dynamic，B1）", "P3"),
    "D09.13": ("D09 逻辑优化", "优化器驱动、规则框架、其他规则、函数依赖", "P3"),
    "D09.14": ("D09 逻辑优化", "逻辑算子公共部分与其余逻辑算子", "P3"),
    "D09.15": ("D09 逻辑优化", "DML 计划构建（insert/update/delete/load，写路径）", "X"),
    "D09.16": ("D09 逻辑优化", "SHOW / ADMIN / SET / SIMPLE / EXPLAIN 等语句的计划构建", "P5"),
    "D09.17": ("D09 逻辑优化", "ANALYZE / DDL / IMPORT / BRIE / 分裂 region 的计划构建（由 Go TiDB 承担）", "X"),
    # ---- D10 physical ----
    "D10.1": ("D10 物理优化与代价", "物理算子定义与 task（physical_plans、task、common_plans）", "P3"),
    "D10.2": ("D10 物理优化与代价", "FindBestTask 与访问路径", "P3"),
    "D10.3": ("D10 物理优化与代价", "ExhaustPhysicalPlans（join/agg/sort 等物理实现枚举）", "P3"),
    "D10.4": ("D10 物理优化与代价", "ranger（区间构造）", "P3"),
    "D10.5": ("D10 物理优化与代价", "代价模型 v2", "P3"),
    "D10.6": ("D10 物理优化与代价", "基数估算（cardinality）", "P3"),
    "D10.7": ("D10 物理优化与代价", "IndexMerge 路径", "P3"),
    "D10.8": ("D10 物理优化与代价", "点查快速路径（point_get_plan）", "P3"),
    "D10.9": ("D10 物理优化与代价", "后处理：resolve indices、plan → pb、EXPLAIN 输出、plan 编码", "P3"),
    "D10.10": ("D10 物理优化与代价", "物理属性与优化器工具（property、planner/util）", "P3"),
    "D10.11": ("D10 物理优化与代价", "内存表谓词提取（memtable extractor）", "P5"),
    # ---- D11 stats ----
    "D11.1": ("D11 统计信息", "统计结构：直方图、TopN、表/列/索引统计（v1 专用部分按 B2）", "P3"),
    "D11.2": ("D11 统计信息", "统计信息加载、缓存与同步加载（缓存按 A3）", "P3"),
    "D11.3": ("D11 统计信息", "收集与维护：analyze、autoanalyze、存储写入、锁统计（由 Go TiDB 承担）", "X"),
    # ---- D12 executor ----
    "D12.0": ("D12 执行器", "执行器框架与构建骨架（exec 接口、builder 分发、adapter、compiler）", "P4"),
    "D12.1": ("D12 执行器", "读取类执行器（table/index reader、index lookup、point get、union scan）", "P4"),
    "D12.2": ("D12 执行器", "基础算子（projection、selection、limit、dual、max_one_row、union）", "P4"),
    "D12.3": ("D12 执行器", "聚合执行器框架（hash/stream agg）", "P4"),
    "D12.4": ("D12 执行器", "聚合函数实现（aggfuncs）", "P4"),
    "D12.5": ("D12 执行器", "排序与 TopN（sortexec）", "P4"),
    "D12.6": ("D12 执行器", "hash join（v1/v2）与 joiner", "P4"),
    "D12.7": ("D12 执行器", "index join 系列", "P4"),
    "D12.8": ("D12 执行器", "merge join", "P4"),
    "D12.9": ("D12 执行器", "window", "P4"),
    "D12.10": ("D12 执行器", "apply、CTE、shuffle、expand", "P4"),
    "D12.11": ("D12 执行器", "IndexMerge reader", "P4"),
    "D12.12": ("D12 执行器", "SHOW、EXPLAIN、SET、PREPARE/EXECUTE、SIMPLE（只读语句）", "P5"),
    "D12.13": ("D12 执行器", "infoschema / 内存表 / slow query / inspection 读取", "P5"),
    "D12.14": ("D12 执行器", "写路径执行器、DDL、ANALYZE、IMPORT、BRIE、admin（由 Go TiDB 承担或后续阶段）", "X"),
    # ---- D13 plan cache / binding / hint ----
    "D13.1": ("D13 Plan Cache、绑定、Hint", "Plan Cache（A3）", "EXC"),
    "D13.2": ("D13 Plan Cache、绑定、Hint", "SQL binding", "P5"),
    "D13.0": ("D13 Plan Cache、绑定、Hint", "hint 的数据类型 StmtHints（D13.3 的第一步；StatementContext 内嵌它，所以放在 tidb-sessionctx，D05.2 PR E）", "P1"),
    "D13.3": ("D13 Plan Cache、绑定、Hint", "hint 处理", "P3"),
    # ---- D14 observability ----
    "D14.1": ("D14 可观测性", "metrics", "DONE"),
    "D14.0": ("D14 可观测性", "执行统计数据结构（execdetails、runtime stats）", "P2"),
    "D14.2": ("D14 可观测性", "slow log、stmt summary、topsql、trace", "P5"),
    # ---- D15 resource control ----
    "D15.1": ("D15 资源控制", "内存统计与配额（A1）", "EXC"),
    "D15.2": ("D15 资源控制", "resource group / runaway", "P5"),
    "D15.3": ("D15 资源控制", "cgroup 内存部分（util/cgroup 的路径探测与内存限额/用量；CPU 部分仍为 X）。tidb-memory 中简化版 cgroup.rs 的正式替换，须在 servermemorylimit（tidb_server_memory_limit 默认值）之前完成", "P1"),
    # ---- scope decisions / not ported ----
    "DEF.MPP": ("D1 范围：TiFlash/MPP", "TiFlash / MPP 计划与执行", "DEFER"),
    "EXC.B": ("例外 B", "废弃或非默认路径：cascades（旧/新）、代价模型 v1、静态分区", "EXC"),
    "X": ("X 不移植", "DDL、lightning、BR、disttask、TTL、import、mock store、测试工具等", "X"),
}

P = r"pkg/"
RULES = [
    # --- exceptions / scope first ---
    ("EXC.B", r"pkg/planner/(cascades|memo|pattern|implementation)/", None),
    ("EXC.B", r"pkg/planner/core/plan_cost_ver1\.go$", None),
    ("DEF.MPP", r"pkg/planner/core/(fragment|tiflash_selection_late_materialization|runtime_filter|runtime_filter_generator)\.go$", None),
    ("DEF.MPP", r"pkg/executor/(mpp_gather|internal/mpp/|mppcoordmanager/)", None),
    ("DEF.MPP", r"pkg/store/copr/(mpp|mpp_probe|batch_coprocessor|batch_request_sender)\.go$", None),
    ("DEF.MPP", r"pkg/util/tiflashcompute/", None),
    # --- X: not ported ---
    # --- D15.3: memory part of pkg/util/cgroup (D15.1 review: the simplified reader in
    # tidb-memory misreads cgroup v1 outside containers); the CPU part stays X ---
    ("D15.3", r"pkg/util/cgroup/cgroup_memory\.go$", None),
    ("D15.3", r"pkg/util/cgroup/cgroup\.go$", r"^(controllerMatch|detectCgroupVersion|detectControlPath|getCgroupDetails|readFile|readInt64Value|procPath\w*|cgroupV[12]Mem\w*)$"),
    ("D15.3", r"pkg/util/cgroup/cgroup_cpu_(linux|unsupport)\.go$", r"^(InContainer|inContainer)$"),
    ("X", r"pkg/(ddl|lightning|br|disttask|ttl|timer|objstore|dumpling|owner|autoid_service|telemetry|workloadlearning|store/gcworker|store/mockstore|testkit|util/mock|parser/goyacc|parser/test_driver|executor/importer|executor/internal/testutil|executor/internal/calibrateresource|planner/indexadvisor|util/(dbutil|importer|table-filter|table-rule-selector|column-mapping|filter|schemacmp|extsort|cgroup|memoryusagealarm|collate/ucadata/generator)|expression/generator|planner/core/generator|session/(bootstrap|sync_upgrade|mock_bootstrap)|server/internal/testserverclient|server/tests|statistics/handle/(autoanalyze|storage|lockstats|usage|globalstats|ddl|history|initstats)|executor/lockstats)(/|\.go$)", None),
    ("X", r"pkg/.*(testutil|test_util|mock)[^/]*\.go$", None),
    ("X", r"pkg/.*/testutil/", None),
    # --- N0 (crate layering) review: infrastructure moved down to the layer that uses it ---
    ("D00.2", r"pkg/kv/kv\.go$", r"^(StoreType|TiKV$|TiFlash$|TiDB$|UnSpecified$)"),
    ("D06.1", r"pkg/planner/core/resolve/result\.go$", None),
    ("D14.1", r"pkg/planner/core/metrics/", None),
    ("D00.1", r"pkg/planner/util/debugtrace/", None),
    ("D00.1", r"pkg/util/ranger/(types\.go$|context/)", None),
    ("D00.1", r"pkg/distsql/context/", None),
    ("D00.1", r"pkg/util/ranger/points\.go$", r"^(FullRange|FullIntRange|FullNotNullRange)$"),
    ("D05.2", r"pkg/session/cursor/", None),
    ("D05.4", r"pkg/session/txninfo/", None),
    ("D09.13", r"pkg/planner/plannersession/", None),
    # --- D00.2 part 2 (config) review: same-rank / upward references resolved by moving down ---
    # metrics.ExecuteErrorToLabel only reads terror.Error.RFCCode: lives with terror in tidb-base.
    ("D00.2", r"pkg/metrics/server\.go$", r"^ExecuteErrorToLabel$"),
    # contextutil.WarnAppender is the warning sink of types.Context (D01.1) and errctx (D00.1);
    # an interface over base's Error with no other dependency: tidb-base (rank 0).
    ("D00.2", r"pkg/util/context/warn\.go$", r"^WarnAppender($|\.)"),
    # types.ColumnNeedRestoredData takes *model.IndexColumn / *model.ColumnInfo: belongs with meta/model.
    ("D06.1", r"pkg/types/etc\.go$", r"^ColumnNeedRestoredData$"),
    # types.EOFAsNil: no caller in v8.5.8 (D01.1 review); tidb-base has no io.EOF error value.
    ("X", r"pkg/types/etc\.go$", r"^EOFAsNil$"),
    # types.ChangeReverseResultByUpperLowerBound (and its helper getDatumBound): no non-test
    # caller in v8.5.8 (D01.1 part 2 review).
    ("X", r"pkg/types/datum\.go$", r"^(ChangeReverseResultByUpperLowerBound|getDatumBound)$"),
    # The parser driver's AST value nodes and NameSlice.FindAstColName (takes *ast.ColumnName)
    # implement parser AST pieces: they go with the parser block, not D01.1 (D01.1 part 2 review).
    ("D02.2", r"pkg/types/parser_driver/", None),
    ("D02.2", r"pkg/types/field_name\.go$", r"^NameSlice\.FindAstColName$"),
    # pkg/util/tikvutil is one variable (CommitterConcurrency) read by config.GetTiKVConfig.
    ("D00.2", r"pkg/util/tikvutil/", None),
    # --- D00 ---
    ("D05.2", r"pkg/sessionctx/sessionstates/", None),
    # pkg/util/context (contextutil) lives in tidb-base, rank 0: tidb-types' types.Context uses
    # its WarnAppender and handlers (D00.1 part 1 review, tidb-rs #65).
    ("D00.4", r"pkg/util/context/", None),
    ("D00.1", r"pkg/(sessionctx/[^/]+\.go$|errctx/|planner/planctx/|planner/core/base/|expression/exprctx/|expression/sessionexpr/|expression/exprstatic/|expression/expropt/|util/context/|sessionctx/stmtctx/)", None),
    # pkg/sessionctx/variable scoping (D05.1, 2026-11-08; N8): every variable keeps Go's
    # name / default / scope / validation (SET and SHOW behave the same). X: writes done by
    # Go TiDB (GlobalSystemVariableInitialValue: bootstrap of mysql.global_variables;
    # switchDDL / switchStats: DDL and stats owner
    # switches, tidb-rs never owns them), extension registration (RegisterSysVar,
    # UnregisterSysVar: test only; N8 3.1 item 5). The hook plumbing that needs the
    # session state (the *FromHook methods and the SetSession / GetGlobal helpers of
    # varsutil.go and sysvar.go) moves to D05.2, which ports SessionVars.
    # The non-validation hooks themselves (SetSession / SetGlobal / GetSession / GetGlobal /
    # GetStateValue) are closures in the table literals, not functions of func_blocks.tsv:
    # data/sysvar_hooks.tsv (rsgen -sysvar-hooks) assigns them to D05.2 and is its
    # acceptance checklist. Status variables registered outside this package follow their
    # registrant (variable.RegisterStatistics): pkg/server Server / compressionStats → D03.1
    # (server block, by the pkg/server rules below), bindinfo globalBindingHandle → D13.2,
    # ddl and gcworker → X.
    ("X", r"pkg/sessionctx/variable/sysvar\.go$", r"^GlobalSystemVariableInitialValue$"),
    ("X", r"pkg/sessionctx/variable/varsutil\.go$", r"^(switchDDL|switchStats)$"),
    ("X", r"pkg/sessionctx/variable/variable\.go$", r"^(RegisterSysVar|UnregisterSysVar)$"),
    # mustParseTime only initializes the TTL job window atomics (TTL jobs: X).
    ("X", r"pkg/sessionctx/variable/tidb_vars\.go$", r"^mustParseTime$"),
    # D05.2 read-path scoping (2026-11-08): parts of SessionVars / TransactionContext that
    # only serve writes done by Go TiDB. Retry of auto-increment / auto-random IDs (INSERT
    # retry), row-ID sharding (INSERT), table deltas for stats (DML), local temporary
    # tables (created by DDL on the creating session; tidb-rs runs no DDL), the write
    # statement buffers (INSERT/UPDATE/DELETE), disk-full DML options, the DDL reorg
    # priority, and the pessimistic lock bookkeeping (SELECT ... FOR UPDATE / DML locks;
    # tidb-rs takes no locks). Savepoints, for-update TS, user variables, prepared
    # statements, txn read TS and the session-state encoding stay (they are reachable from
    # read-only SQL or the server).
    # SessionVars.CleanBuffers only clears the reusable write buffers (called at insert.go:69,
    # memory only): X with WriteStmtBufs (Lucky's review of tidb-rs #81).
    ("X", r"pkg/sessionctx/variable/session\.go$", r"^(RetryInfo\.|retryInfoAutoIDs\.|NewRowIDShardGenerator$|RowIDShardGenerator\.|SessionVars\.GetRowIDShardGenerator$|TableDelta\.|NewTemporaryTableData$|temporaryTableData\.|SessionVars\.GetTemporaryTable$|WriteStmtBufs\.|SessionVars\.GetWriteStmtBufs$|SessionVars\.CleanBuffers$|SessionVars\.(Set|Get|Clear)DiskFullOpt$|SessionVars\.setDDLReorgPriority$|TransactionContext\.(AddUnchangedKeyForLock|CollectUnchangedKeysForSLock|CollectUnchangedKeysForXLock|ResetUnchangedKeysForLock|FlushStmtPessimisticLockCache|GetKeyInPessimisticLockCache|SetPessimisticLockCache|UpdateDeltaForTable|ClearDelta)$|SessionVars\.PessimisticLockEligible$)"),
    ("D05.2", r"pkg/sessionctx/variable/variable\.go$", r"^SysVar\.(Get|Set)(Global|Session)FromHook$"),
    ("D05.2", r"pkg/sessionctx/variable/varsutil\.go$", r"^(getTiDBTableValue|setTiDBTableValue|parseTSFromNumberOrTime|setReadStaleness|setSnapshotTS|setTxnReadTS)$"),
    ("D05.2", r"pkg/sessionctx/variable/sysvar\.go$", r"^setTiFlashComputeDispatchPolicy$"),
    ("D05.1", r"pkg/sessionctx/variable/(sysvar|tidb_vars|varsutil|variable|statusvar|removed|setvar_affect|sequence_state|mock_globalaccessor|noop)\.go$", None),
    ("D05.2", r"pkg/sessionctx/variable/", None),
    ("D00.2", r"pkg/(parser/mysql|parser/terror|errno|util/dbterror|config)/", None),
    # --- D02 parser ---
    # D02.3: D02.2 was marked DONE, but parser/model, parser/types, auth.UserIdentity and
    # duration only had hand-written PoC stand-ins (no provenance, no Go JSON). meta/model
    # (D06.1) needs them faithfully; so do the two ast pieces meta/model calls.
    ("D02.3", r"pkg/parser/(model|types|duration)/", None),
    ("D02.3", r"pkg/parser/auth/auth\.go$", None),
    ("D02.3", r"pkg/parser/ast/ddl\.go$", r"^(NormalizeTableAffinityLevel|TableAffinityLevel\w*)$"),
    ("D02.3", r"pkg/parser/ast/functions\.go$", r"^Vec(L2|Cosine)Distance$"),
    ("D02.1", r"pkg/parser/ast/", None),
    ("D02.2", r"pkg/parser/", None),
    # --- D01 ---
    ("D01.5", r"pkg/types/json_", None),
    ("D01.6", r"pkg/types/vector", None),
    ("D01.2", r"pkg/types/mydecimal\.go$", None),
    ("D01.3", r"pkg/types/(time|core_time|fsp)\.go$", None),
    ("D01.4", r"pkg/types/(convert|overflow|truncate)\.go$", None),
    ("D01.1", r"pkg/types/", None),
    # M-POOL (A1): the chunk reuse pool is not ported; the allocator API stays in D01.7.
    ("X", r"pkg/util/chunk/pool\.go$", None),
    # allocator.CheckReuseAllocSize only reads the configured pool sizes, and its answer is
    # visible through the reuse hook and @@last_sql_use_alloc (D05.2 PR E): ported in D01.7.
    ("D01.7", r"pkg/util/chunk/alloc\.go$", r"^allocator\.CheckReuseAllocSize$"),
    ("X", r"pkg/util/chunk/alloc\.go$", r"^(allocator|columnList|poolColumnAllocator|freeList)\.\w+$|^checkColumnType$"),
    ("X", r"pkg/util/chunk/chunk\.go$", r"^Chunk\.resetForReuse$"),
    ("D01.10", r"pkg/util/chunk/(row_container|chunk_in_disk|row_in_disk|row_container_reader|disk)", None),
    ("D01.7", r"pkg/util/chunk/", None),
    ("D01.8", r"pkg/util/codec/", None),
    # D06.7 (2026-11-09, #58 option (c), D05.2 spec §6): SET GLOBAL persists like Go. The row
    # encoders, the index value encoders and their helpers come back from X to D01.11 (the
    # narrow system-table writer needs them). D06.7 itself: the session functions that read and
    # write mysql.global_variables / mysql.tidb, the domain's sysvar cache (reload, etcd
    # notification, global config), and the autoid allocation used for _tidb_rowid.
    ("D06.7", r"pkg/session/session\.go$", r"^session\.(replaceGlobalVariablesTableValue|getTableValue|GetTiDBTableValue|SetTiDBTableValue)$"),
    ("D06.7", r"pkg/domain/domain\.go$", r"^Domain\.(LoadSysVarCacheLoop|NotifyUpdateSysVarCache|NotifyGlobalConfigChange)$"),
    ("D06.7", r"pkg/domain/sysvar_cache\.go$", r"^Domain\.(fetchTableValues|rebuildSysVarCache|rebuildSysVarCacheIfNeeded)$"),
    # The row-ID allocation path of AllocHandle (2026-11-11, PR C round 3): Alloc's checks and
    # the batch / step helpers alloc4Signed calls come with it from D06.2 (tidb-systable
    # autoid.rs; rebase4Signed, only reached with offset > 1, stays in D06.2).
    ("D06.7", r"pkg/meta/autoid/autoid\.go$", r"^(allocator\.(alloc4Signed|Alloc)|CalcNeededBatchSize|SeekToFirstAutoIDSigned|SeekToFirstAutoIDUnSigned|NextStep|validIncrementAndOffset)$"),
    # EncodeRow / EncodeOldRow come back to D01.11 (2026-11-11, PR C round 3): Go's
    # SET GLOBAL writes mysql.global_variables through an internal session of sysSessionPool,
    # which never loads the global tidb_row_format_version: row format 1 (EncodeOldRow).
    ("D01.11", r"pkg/tablecodec/tablecodec\.go$", r"^(EncodeRow|EncodeOldRow)$"),
    # tablecodec / rowcodec scoping (D01.11, 2026-11-08): tidb-rs is read-only. Reachability
    # over the function-level reference graph (work/go_refs.tsv) from the read-path blocks,
    # judged per function. X: "写路径，由 Go TiDB 承担" — the row encoders (EncodeRow,
    # EncodeOldRow, rowcodec.Encoder and its sorters, row.toBytes/init*), the index value
    # encoders (GenIndexValue*, EncodeHandleInUniqueIndexValue, encodeCommonHandle,
    # encodePartitionID, TryGetCommonPkColumnRestoredIds), the temp-index value codec
    # (DDL backfill/merge, session KeyNeedToLock, mutation checker), IsUntouchedIndexKValue,
    # IndexKVIsUnique, TempIndexKey2IndexKey; DDL/unistore/TiCDC only (CutCommonHandle,
    # DecodeIndexID, EncodeMetaKeyPrefix, TruncateToRowKeyLen, IsRowKey, row.ColumnIsNull,
    # row.CalculateRawChecksum); no caller in v8.5.8 (CutIndexKey, CutIndexPrefix,
    # CutRowKeyPrefix, EncodeMetaKey, MetaPrefix, UnflattenDatums, RowData.Encode).
    ("X", r"pkg/tablecodec/tablecodec\.go$", r"^(DecodeTempIndexValue|TempIndexValue\.\w+|TempIndexValueElem\.\w+|TempIndexValueIsUntouched|IsUntouchedIndexKValue|IndexKVIsUnique|TempIndexKey2IndexKey|CutCommonHandle|DecodeIndexID|EncodeMetaKeyPrefix|TruncateToRowKeyLen|CutIndexKey|CutIndexPrefix|CutRowKeyPrefix|EncodeMetaKey|MetaPrefix|UnflattenDatums)$"),
    ("X", r"pkg/util/rowcodec/common\.go$", r"^(IsRowKey|RowData\.Encode)$"),
    ("X", r"pkg/util/rowcodec/row\.go$", r"^row\.(ColumnIsNull|CalculateRawChecksum)$"),
    ("D01.11", r"pkg/(tablecodec|util/rowcodec)/", None),
    ("D01.9", r"pkg/util/collate/", None),
    # --- D03 ---
    ("D03.2", r"pkg/server/(http_status|http_handler|rpc_server|handler/)", None),
    ("D03.1", r"pkg/server/", None),
    ("D04.1", r"pkg/privilege/", None),
    # --- D05 ---
    ("D05.4", r"pkg/sessiontxn/", None),
    ("D05.3", r"pkg/session/", None),
    # --- D06 ---
    # DDL jobs (D06.1 scoping, 2026-11-07): tidb-rs is read-only and DDL runs in Go TiDB.
    # Kept in D06.1: the job/reorg/BDR type and const declarations (the shape that
    # Job.Decode reads for ADMIN SHOW DDL JOBS / infoschema DDL tables / SchemaDiff) and the
    # functions reached from read-path blocks (D06.2-D06.5, D12.12, D12.13). The rest is
    # X: "DDL 写路径，由 Go TiDB 承担". job_args.go: only AutoIDGroup (meta autoid reads).
    ("X", r"pkg/meta/model/(job|reorg|bdr)\.go$", r"^(?!(?:TSConvert2Time|ActionType\.String|Job\.(Decode|GetRowCount|GetWarnings|MayNeedReorg|String)|JobState\.String|JobVersion\.String|SchemaState\.String|DDLReorgMeta\.Get(BatchSize|Concurrency|MaxWriteSpeed)OrDefault|ReorgType\.String)$)(?:\w+\.\w+|init|GetJobVerInUse|ModifyTypeToString|NewJobW|NewMultiSchemaInfo|SetJobVerInUse|StrToJobState|UpdateJobArgsForTest|marshalArgs)$"),
    ("D06.1", r"pkg/meta/model/job_args\.go$", r"^AutoIDGroup$"),
    ("X", r"pkg/meta/model/job_args\.go$", None),
    ("D06.1", r"pkg/meta/model/", None),
    # meta / structure scoping (D06.2, 2026-11-11): tidb-rs is read-only and DDL / meta writes
    # run in Go TiDB. X: "DDL 与元数据写入由 Go TiDB 承担" — the structure writers (string
    # Set/Inc/Clear, hash HSet/HInc/HDel/HClear/updateHash/loadHashValue (read only by those
    # writers) and the autoid key-value helper,
    # list push/pop/set/clear and listMeta.Value) and the meta.Mutator writers (Create*,
    # Update*, Drop*, Add*, Gen*, Set*, Clear*, FinishBootstrap, EnQueue / history writes,
    # RestartSequenceValue, the *NotExists / Update-side existence checks, attachMagicByte,
    # DDLTableVersion.Bytes, the autoid accessors' Put/Inc/Del/CopyTo) and the test helper
    # DefaultGroupMeta4Test, and getDDLJob (no caller in v8.5.8). The readers (meta.Reader and what it calls) stay in D06.2.
    # When the write path is ported, loadHashValue comes back with updateHash/HDel (Lucky, tidb-rs #85).
    ("X", r"pkg/structure/string\.go$", r"^TxStructure\.(Set|Inc|Clear)$"),
    ("X", r"pkg/structure/hash\.go$", r"^TxStructure\.(HSet|HInc|HDel|HClear|updateHash|loadHashValue|hashFieldIntegerVal|EncodeHashAutoIDKeyValue)$"),
    ("X", r"pkg/structure/list\.go$", r"^(TxStructure\.(LPush|RPush|listPush|LPop|RPop|listPop|LSet|LClear)|listMeta\.Value)$"),
    ("X", r"pkg/meta/meta\.go$", r"^(Mutator\.(GenGlobalID|AdvanceGlobalIDs|GenGlobalIDs|GenPlacementPolicyID|GenAutoTableIDKeyValue|GenSchemaVersion|GenSchemaVersions|checkPolicyExists|checkPolicyNotExists|checkResourceGroupNotExists|checkResourceGroupExists|checkDBNotExists|checkTableExists|checkTableNotExists|CreatePolicy|UpdatePolicy|AddResourceGroup|UpdateResourceGroup|DropResourceGroup|CreateDatabase|UpdateDatabase|CreateTableOrView|SetBDRRole|ClearBDRRole|SetDDLTables|CreateMySQLDatabaseIfNotExists|SetMetadataLock|SetSchemaCacheSize|CreateTableAndSetAutoID|CreateSequenceAndSetSeqValue|RestartSequenceValue|DropPolicy|DropDatabase|DropSequence|DropTableOrView|UpdateTable|enQueueDDLJob|EnQueueDDLJob|addHistoryDDLJob|AddHistoryDDLJob|SetIngestMaxBatchSplitRanges|SetIngestMaxSplitRangesPerSec|SetIngestMaxInflight|SetIngestMaxPerSec|FinishBootstrap|SetSchemaDiff|SetRUStats|getDDLJob)|DefaultGroupMeta4Test|attachMagicByte|DDLTableVersion\.Bytes)$"),
    ("X", r"pkg/meta/meta_autoid\.go$", r"^(autoIDAccessor\.(Put|Inc|Del|CopyTo)|autoIDAccessors\.(Put|Del))$"),
    ("D06.2", r"pkg/(meta|structure)/", None),
    ("D06.6", r"pkg/infoschema/(tables|cluster|metrics_schema|perfschema/|metrics)\.?", None),
    ("D06.3", r"pkg/infoschema/", None),
    ("D06.4", r"pkg/table/", None),
    ("DEF.MPP", r"pkg/domain/infosync/tiflash_manager", None),
    ("X", r"pkg/domain/(plan_replayer|extract|historical_stats)", None),
    ("D06.5", r"pkg/domain/", None),
    # --- D07 ---
    ("D07.4", r"pkg/store/copr/coprocessor_cache\.go$", None),
    ("D07.2", r"pkg/store/copr/", None),
    # pkg/kv scoping (D07.0, 2026-11-08): tidb-rs is read-only. Reachability over the
    # function-level reference graph (work/go_refs.tsv, iface edges included) from the
    # read-path blocks, then judged per function. X: "写路径，由 Go TiDB 承担" (memdb key
    # flags, prewrite/commit write sources, duplicate-key errors, stmt-buffer staging,
    # commit options, IncInt64/WalkMemBuffer), the fault-injection test helper, and
    # functions with no caller on any read path in v8.5.8 (KeyRange.IsPoint: mockstore
    # only; KeyRanges.ForEachPartition/TotalRangeNum: lightning only; GetInt64,
    # NewVariables, Version.Cmp: no caller). mpp.go belongs to the deferred MPP scope
    # except GetNewestMppVersion (SessionVars.ChooseMppVersion).
    ("X", r"pkg/kv/(keyflags|fault_injection)\.go$", None),
    # cacheDB: no callers in v8.5.8; cached tables read through tables/cache.go MemBuffer
    # (GetMemCache is only used by schema_validator to Delete). Lucky, tracker #22 review.
    ("X", r"pkg/kv/cachedb\.go$", None),
    ("X", r"pkg/kv/error\.go$", r"^GenKeyExistsErr$"),
    ("X", r"pkg/kv/kv\.go$", r"^(FindKeysInStage|WithReturnCommitTS|KeyRanges\.(ForEachPartition|TotalRangeNum))$"),
    ("X", r"pkg/kv/key\.go$", r"^KeyRange\.IsPoint$"),
    ("X", r"pkg/kv/option\.go$", r"^(SetCDCWriteSource|getCDCWriteSource|isCDCWriteSourceSet|SetLossyDDLReorgSource|getLossyDDLReorgSource|isLossyDDLReorgSourceSet)$"),
    ("X", r"pkg/kv/utils\.go$", None),
    ("X", r"pkg/kv/variables\.go$", None),
    ("X", r"pkg/kv/version\.go$", r"^Version\.Cmp$"),
    ("DEF.MPP", r"pkg/kv/mpp\.go$", r"^(?!GetNewestMppVersion$)"),
    ("D07.0", r"pkg/kv/", None),
    ("D07.1", r"pkg/(store/driver|store/helper|store)/", None),
    ("D07.3", r"pkg/distsql/", None),
    # --- D08 ---
    ("D08.10", r"pkg/expression/builtin_json", None),
    ("D08.11", r"pkg/expression/aggregation/", None),
    ("D08.3", r"pkg/expression/(infer_pushdown|expr_to_pb|distsql_builtin|expression_codec)", None),
    ("D08.4", r"pkg/expression/builtin_compare", None),
    ("D08.5", r"pkg/expression/builtin_(arithmetic|math|op)", None),
    # builtin_convert_charset.go (to_binary/from_binary, HandleBinaryLiteral): CAST to STRING
    # wraps its argument with them (castAsStringFunctionClass), and newBaseBuiltinFuncWithTp
    # does for every string argument, so the file comes with CAST (D08.8 part 2b-1).
    ("D08.8", r"pkg/expression/builtin_convert_charset\.go$", None),
    ("D08.6", r"pkg/expression/builtin_(string|like|ilike|regexp)", None),
    ("D08.7", r"pkg/expression/builtin_time", None),
    ("D08.8", r"pkg/expression/builtin_cast", None),
    ("D08.9", r"pkg/expression/builtin_(control|other|info|miscellaneous|encryption|grouping)", None),
    ("D08.2", r"pkg/expression/(builtin|builtin_registry|function_traits|constant_fold|constant_propagation|collation|simple_rewriter|builtin_func_param|grouping_sets)\.go$", None),
    ("D08.12", r"pkg/expression/(util|helper|explain|extension)\.go$", None),
    ("D08.1", r"pkg/expression/", None),
    # --- D09/D10 hub files: per symbol ---
    ("D09.3", r"pkg/planner/core/logical_plan_builder\.go$", r"(build(Join|NaturalJoin|UsingClause|SemiJoin|SemiApply|ApplyWithJoinType|LateralJoin|JoinFuncDepend)|coalesceCommonColumns|findColFromNaturalUsingJoin|findJoinFullSchema|unionJoinFieldType|containsLateralTableSource|isImmediateLateralTableSource|hasMPPJoinHints|isJoinHintSupportedInMPPMode)"),
    ("D09.4", r"pkg/planner/core/logical_plan_builder\.go$", r"(Aggregat|Window|Gby|gby|OnlyFullGroupBy|FuncDepend|ColFuncDepend|SingleValue|aggColNameResolver|aggOrderByResolver|correlatedAggregateResolver|havingWindowAndOrderby|resolveHavingAndOrderBy|AggFuncs|buildDistinct|checkOrderByInDistinct|groupWindowFuncs|specEqual|mergeWindowSpec|sortWindowSpecs|resolveWindow|handleDefaultFrame|WindowFunc|WindowSpec|getWindowName|appendIfAbsentWindowSpec|checkExprInGroupByOrIsSingleValue|colResolverForOnlyFullGroupBy|replaceGroupingFunc|resolveGroupingTraverseAction|implicitProjectGroupingSetCols|buildExpand)"),
    ("D09.5", r"pkg/planner/core/logical_plan_builder\.go$", r"(CTE|Cte|cte|buildWith|SetOpr|Union|Except|Intersect|SemiJoinForSetOperator|setUnionFlen|divideUnionSelectPlans|tryToBuildSequence)"),
    ("D09.6", r"pkg/planner/core/logical_plan_builder\.go$", r"(subquery|Subqueries|buildMaxOneRow)"),
    ("D09.15", r"pkg/planner/core/logical_plan_builder\.go$", r"(Delete|Update|update|TblColPos|ColPosInfo|tblInfoFromCol|buildColumns2Handle|resolveIndicesForTblID2Handle|CheckUpdateList|GetUpdateColumnsInfo|IsDefaultExprSameColumn|extractDefaultExpr|getAssignFlag|ExtractTableList|tableListExtractor)"),
    ("D10.11", r"pkg/planner/core/logical_plan_builder\.go$", r"(buildMemTable|timeRangeForSummaryTable)"),
    ("D11.2", r"pkg/planner/core/logical_plan_builder\.go$", r"(getStatsTable|getLatestVersionFromStatsTable)"),
    ("D13.3", r"pkg/planner/core/logical_plan_builder\.go$", r"(Hint|hint)"),
    ("D09.1", r"pkg/planner/core/logical_plan_builder\.go$", None),
    ("D09.17", r"pkg/planner/core/planbuilder\.go$", r"(Analyze|analyze|DDL|Ddl|ImportInto|importInto|Split|BRIE|Backup|Restore|LoadData|LoadStats|LockStats|UnlockStats|CompactTable|DistributeTable|TableDistribution|TableRegions|PlanReplayer|calcTSForPlanReplayer|RecommendIndex|Admin|admin|CheckIndex|CheckTable|ChecksumTable|CleanupIndex|RecoverIndex|DDLJob|DDLFields|ShowNextRowID|QueryWatch|CalibrateResource|AlterDDLJob|BDRRole|Grant|Revoke|RequireSelectPrivForStatsObjects|requireSelectPrivForStatsObjects|fillDefaultDBForStatsObjects|ColumnsBasedOnPredicateColumns|PredicateColumns|MustAnalyzed|ColOffsetForAnalyze|ModifiedIndexes|generateIndexTasks|getMaxWriteSpeed|GetThreadOrBatchSize|getPhysicalID|GetPhysicalIDsAndPartitionNames|filterSkipColumnTypes|getColumnListFromSet|getColumnSetFromSpecifiedCols|getColumnNamesFromIDs|getAnalyzeColumnList|pickColumnList|combineColumnSets|getModifiedIndexes|getColsInfo|getFullAnalyze|IndexColumnInfos|HandleColumnInfos|IndexColsSchema)"),
    ("D09.15", r"pkg/planner/core/planbuilder\.go$", r"(Insert|insert|OnDup|Values|Generated|getAffectCols|getMissingColumns|convertValue|getDefaultValue|Change)"),
    ("D09.16", r"pkg/planner/core/planbuilder\.go$", r"(Show|show|Explain|Trace|Set|Simple|Prepare|Execute|Bind|bind|Digest|Do$|buildDo|Events|Procedure|Trigger|Warnings|Slow|collectStrOrUserVarList|extractPatternLike|buildColumnWithName|columnsWithNames|newColumnsWithNames|convert2OutputSchemas|findStmtAsViewSchema|fetchRecordFromClusterStmtSummary|userVariableChecker|checkForUserVariables|SelectInto)"),
    ("D10.2", r"pkg/planner/core/planbuilder\.go$", r"(AccessPath|Path|IndexLookUp|isPrimaryIndex|isTiKVIndexByName|getIndex|tryGetCommonHandleCols|tryGetPkHandleCol|tryGetPkExtraColumn|removeGlobalIndexPaths|removeIgnoredPaths|checkIsAllSpecialGlobalIndex|setExtraPhysTblIDColsOnDataSource|addColumnsWithVirtualExprs|fillContentForTablePath|genTiFlashPath|BuildHandleColsForAnalyze|handleColHelper)"),
    ("D09.0", r"pkg/planner/core/planbuilder\.go$", None),
    ("D09.0", r"pkg/planner/core/(preprocess|resolve/|util\.go$|plan\.go$|core_init|initialize|logical_initialize|stringer|debugtrace|telemetry|trace)", None),
    ("D09.2", r"pkg/planner/core/(expression_rewriter|scalar_subq_expression)\.go$", None),
    # --- D09 rules & logical ops ---
    ("D09.7", r"pkg/planner/core/(rule_column_pruning|rule_eliminate_projection|property_cols_prune)\.go$", None),
    ("D09.7", r"pkg/planner/core/operator/logicalop/logical_projection\.go$", r"PruneColumns|eliminat"),
    # Go implements each logical rule as a method on every operator; the rule's block owns
    # those methods in all logicalop files (rule axis), the operator block owns the rest.
    ("D09.7", r"pkg/planner/core/operator/logicalop/", r"\.PruneColumns|\.pruneColumns|eliminat"),
    ("D09.8", r"pkg/planner/core/operator/logicalop/", r"\.(PredicatePushDown|PredicateSimplification|ConvertOuterToInnerJoin|ConstantPropagation|PullUpConstantPredicates)"),
    ("D09.11", r"pkg/planner/core/operator/logicalop/", r"\.(PushDownTopN|DeriveTopN)|pushDownTopN"),
    ("D09.6", r"pkg/planner/core/operator/logicalop/", r"\.ExtractCorrelatedCols"),
    ("D10.6", r"pkg/planner/core/operator/logicalop/", r"\.(DeriveStats|RecursiveDeriveStats|ExtractColGroups)|deriveStats|DeriveStats"),
    ("D10.2", r"pkg/planner/core/operator/logicalop/", r"\.(FindBestTask|PreparePossibleProperties|RollBackTaskMap)"),
    ("D10.3", r"pkg/planner/core/operator/logicalop/", r"\.ExhaustPhysicalPlans"),
    ("D09.3", r"pkg/planner/core/operator/logicalop/logical_(join|apply)\.go$", None),
    ("D09.4", r"pkg/planner/core/operator/logicalop/logical_(aggregation|window|expand)\.go$", None),
    ("D09.5", r"pkg/planner/core/operator/logicalop/logical_(cte|cte_table|union_all|partition_union_all|sequence)\.go$", None),
    ("D09.11", r"pkg/planner/core/operator/logicalop/logical_(limit|top_n|sort)\.go$", None),
    ("D10.11", r"pkg/planner/core/operator/logicalop/logical_(mem_table|show|show_ddl_jobs)\.go$", None),
    ("D09.14", r"pkg/planner/core/operator/logicalop/", None),
    ("D09.6", r"pkg/planner/core/(rule_decorrelate|rule_correlate|rule_semi_join_rewrite)\.go$", None),
    ("D09.8", r"pkg/planner/core/(rule_predicate_push_down|rule_predicate_simplification|rule_outer_to_inner_join|constraint/)", None),
    ("D09.9", r"pkg/planner/core/rule_(aggregation_push_down|aggregation_elimination|aggregation_skew_rewrite|max_min_eliminate)\.go$", None),
    ("D09.10", r"pkg/planner/core/(rule_join_reorder|rule_join_elimination|joinorder/)", None),
    ("D09.11", r"pkg/planner/core/rule_(topn_push_down|derive_topn_from_window)\.go$", None),
    ("D09.12", r"pkg/planner/core/(rule_partition_processor|partition_prune|partidx/)", None),
    ("D09.13", r"pkg/planner/core/(optimizer|rule_|rule/|recheck_cte)|pkg/planner/funcdep/|pkg/planner/util/(fixcontrol|optimizetrace|debugtrace|domainmisc|tablesampler)|pkg/planner/optimize\.go|pkg/planner/[^/]+\.go$", None),
    # --- D10 ---
    ("D10.2", r"pkg/planner/core/(find_best_task|stats|index_join_path|collect_column_stats_usage|rule_collect_plan_stats)\.go$", None),
    ("D10.3", r"pkg/planner/core/exhaust_physical_plans\.go$", None),
    ("D10.7", r"pkg/planner/core/indexmerge", None),
    ("D10.8", r"pkg/planner/core/point_get_plan\.go$", None),
    ("D10.5", r"pkg/planner/core/(plan_cost_ver2|plan_cost_detail|cost/)", None),
    ("D10.9", r"pkg/planner/core/(resolve_indices|plan_to_pb|pb_to_plan|explain|flat_plan|access_object|encode|plan_clone_|hash64_equals)", None),
    ("D10.11", r"pkg/planner/core/(memtable_|show_predicate_extractor)", None),
    ("D13.1", r"pkg/planner/core/(plan_cache|plan_cacheable_checker)", None),
    ("D13.3", r"pkg/planner/core/hint_utils\.go$", None),
    ("D09.15", r"pkg/planner/core/foreign_key\.go$", None),
    ("D10.1", r"pkg/planner/core/(physical_plans|task|task_base|common_plans|operator/physicalop/)", None),
    ("D10.4", r"pkg/util/ranger/", None),
    ("D10.6", r"pkg/planner/cardinality/", None),
    ("D10.10", r"pkg/planner/(property|util)/", None),
    ("D10.1", r"pkg/planner/core/", None),
    # --- D11 ---
    ("D11.1", r"pkg/statistics/[^/]+\.go$", None),
    ("D11.2", r"pkg/statistics/(handle|asyncload)/", None),
    # --- D12 executor ---
    ("DEF.MPP", r"pkg/executor/builder\.go$", r"MPP"),
    ("D12.14", r"pkg/executor/builder\.go$", r"(Analyze|DDL|DDLJob|Import|LoadData|LoadStats|LockStats|UnlockStats|Insert|Replace|Update|Delete|Split|Checksum|CheckTable|CheckIndex|CleanupIndex|RecoverIndex|CompactTable|DistributeTable|PlanReplayer|RecommendIndex|Grant|Revoke|AdminPlugins|BDRRole|Reload|getAssignFlag|AdjustedSampleRate|ApproximateTableCount|emptySampler|buildHandleColsForSplit|IndexLookUpChecker|ShowNextRowID|SQLBind)"),
    ("D12.7", r"pkg/executor/builder\.go$", r"(IndexLookUpJoin|IndexLookUpMergeJoin|IndexNestedLoopHashJoin|ForIndexJoin|RangesForIndexJoin|KvRangesForIndexJoin|dataReaderBuilder|newDataReaderBuilder|prunePartitionForInnerExecutor)"),
    ("D12.6", r"pkg/executor/builder\.go$", r"(HashJoin|extractUsedColumnsInJoinOtherCondition)"),
    ("D12.8", r"pkg/executor/builder\.go$", r"MergeJoin"),
    ("D12.3", r"pkg/executor/builder\.go$", r"(HashAgg|StreamAgg)"),
    ("D12.5", r"pkg/executor/builder\.go$", r"(Sort|TopN)"),
    ("D12.9", r"pkg/executor/builder\.go$", r"Window"),
    ("D12.10", r"pkg/executor/builder\.go$", r"(Apply|CTE|Shuffle|Expand|corCol)"),
    ("D12.11", r"pkg/executor/builder\.go$", r"IndexMerge"),
    ("D12.13", r"pkg/executor/builder\.go$", r"MemTable"),
    ("D12.12", r"pkg/executor/builder\.go$", r"(Show|Explain|Set|Simple|Prepare|Execute|Deallocate|Trace|SelectInto)"),
    ("D12.2", r"pkg/executor/builder\.go$", r"(Projection|Selection|Limit|TableDual|MaxOneRow|UnionAll|TableSample|SelectLock)"),
    ("D12.1", r"pkg/executor/builder\.go$", r"(Reader|PointGet|UnionScan|Req|Range|Handle|Snapshot|getSnapshot|CacheTable|TemporaryTable|RowDecoder|ScanOutputOffsets|Partition|partition|kvRangeBuilder|dedupHandles|ReadAdjuster|IndexUsageReporter|isCommonHandleRead|getPhysicalTableID|markChildrenUsedCols|IdxColsConcat|mockPhysicalIndexReader|IndexLookUpPushDown|collectCol)"),
    ("D12.0", r"pkg/executor/builder\.go$", None),
    ("D12.0", r"pkg/executor/(adapter|compiler|executor|internal/exec/|internal/builder/|internal/util/|utils|errors|metrics/|staticrecordset/|internal/applycache/|internal/vecgroupchecker/|internal/pdhelper/|detach)", None),
    ("D12.1", r"pkg/executor/(table_reader|distsql|point_get|batch_point_get|union_scan|mem_reader|coprocessor|table_readers_required_rows|sample)", None),
    ("D12.2", r"pkg/executor/(projection|select|unionexec/)", None),
    ("D12.3", r"pkg/executor/aggregate/", None),
    ("D12.4", r"pkg/executor/aggfuncs/", None),
    ("D12.5", r"pkg/executor/sortexec/", None),
    ("D12.7", r"pkg/executor/join/index_lookup", None),
    ("D12.8", r"pkg/executor/join/merge_join", None),
    ("D12.6", r"pkg/executor/join/", None),
    ("D12.9", r"pkg/executor/(window|pipelined_window)", None),
    ("D12.10", r"pkg/executor/(parallel_apply|cte|shuffle|expand|cte_table_reader)", None),
    ("D12.11", r"pkg/executor/index_merge_reader", None),
    ("D12.12", r"pkg/executor/(show|explain|set|prepared|simple|trace|select_into|bind)", None),
    ("D12.13", r"pkg/executor/(infoschema_reader|memtable_reader|slow_query|inspection|metrics_reader|stmtsummary|show_slow|adapter_slow_log)", None),
    ("D12.14", r"pkg/executor/", None),
    # --- D13-D15 ---
    ("D13.2", r"pkg/bindinfo/", None),
    # StmtHints is embedded in stmtctx.StatementContext (rank 7, below tidb-planner): the data
    # type and its two data-only methods are D13.0 in tidb-sessionctx (D05.2 PR E, Lucky's
    # review of tidb-rs #81). Parsing (ParseStmtHints, addHypoIndex) stays in D13.3.
    ("D13.0", r"pkg/util/hint/hint\.go$", r"^StmtHints\.(Clone|TaskMapNeedBackUp)$"),
    ("D13.3", r"pkg/util/hint/", None),
    ("D14.1", r"pkg/metrics/", None),
    ("D00.3", r"pkg/util/logutil/", None),
    ("D14.0", r"pkg/util/(execdetails|tracing|traceevent)/", None),
    ("D14.2", r"pkg/util/(stmtsummary|topsql|plancodec)/", None),
    ("D15.1", r"pkg/util/memory/", None),
    ("D15.2", r"pkg/(resourcegroup|resourcemanager)/", None),
    # --- leftovers ---
    ("D00.3", r"pkg/util/", None),
    ("X", r"pkg/", None),
]
