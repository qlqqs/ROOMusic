# V0 standalone Release Graph 基准研究

## 结论

推荐停止从已知 `error/failed` 的 V0 production PostgreSQL 截取部分图，改为在固定归档的
临时解包副本中注入一个 smoke-owned adapter，直接调用 V0 scanner 的 Release Graph 核心，
生成版本化 normalized SQLite 和 canonical JSON。

这不是复制或重写 V0 整理算法，而是恢复 V0 历史 golden generator 的职责边界。V0
scanner 归档仍由原 SHA-256 锁定；adapter 单独计算 hash，不能冒充归档的一部分。

## 代码证据

V0 production handler 的核心顺序见：

- `internal/tasks/scan_task_pipeline.go:154-175`：调用
  `BuildReleaseCandidates`、`AssembleReleaseCandidate`，随后保存 Release Graph；
- `internal/tasks/scan_task_pipeline.go:188-196`：图保存之后才读取 index 并保存 local
  evidence bundle，本轮 8 个 production 错误发生在这里；
- `internal/scanner/real_music_integration_test.go:163-188`：直接执行
  `Walk -> parseCorpusEvidence -> BuildReleaseCandidates -> AssembleReleaseCandidate`，不依赖
  REST、Redis 或 PostgreSQL。

V0 历史规划记录进一步说明：

- `01.1-03-SUMMARY.md` 记录 `internal/golden/generate.go` 已切换到
  `BuildReleaseCandidates -> AssembleReleaseCandidate`；
- 同一记录在 D-51 修正后分别统计 110 Release、112 Medium、407 Track 和 466 File；
- `01.1-05-SUMMARY.md` 记录真实语料测试覆盖 ZARD box leaf、WEB、CD、CUE virtual 和
  loose file，并在后续人工 reviewed golden 上得到 `No differences.`；
- 原 `cmd/golden` 和 `internal/golden` 源码已退役且不可恢复，但 scanner API、真实语料
  测试和行为记录仍存在。

因此 standalone adapter 与 production worker 在 Release Graph 生成 owner 上同源，而
绕开的是已经确认不属于本次权威范围的 queue、runtime lifecycle、artwork/lyrics、quality
badge 和 local-evidence persistence。

## 方案比较

| 路线 | 优点 | 风险 | 结论 |
| --- | --- | --- | --- |
| 从失败 PostgreSQL 截取图 | 包含 persistence 后形态，现有脚本接近可用 | 需要接受 failed scan；不能证明所有失败目录的图完整；混入 runtime/evidence 缺陷 | 撤回为基准路线，仅保留研究证据 |
| 临时修补 V0 evidence | 可让 production 终态成功 | 改变被比较的 V0 业务代码身份 | 不选 |
| standalone scanner + SQLite | 复用权威图算法；无服务依赖；成功/失败边界清楚；贴近历史 golden workflow | adapter mapping 必须防止复制算法或遗漏字段 | 推荐，以 adapter hash、合成映射测试和 round-trip 校验约束 |
| 继续寻找旧 golden | 可恢复 notes/review ledger | 当前没有文件或备份，无法推进同 corpus smoke | 只作以后审计，不阻塞 |

## 推荐数据合同

### 临时 adapter 输出

adapter 只用 Go 标准库序列化 corpus-relative、确定排序的 graph rows，不引入 SQLite driver
或修改 V0 `go.mod`。运行时输入只有 `/music:ro`，输出只有 `/output:rw`，网络关闭。

adapter 调用顺序固定为：

1. `scanner.Walk`；
2. `ParseCueSheet`、`ValidateCueFiles`、`ParseTags` 并构造 `FileEvidence`；
3. `BuildReleaseCandidates`；
4. hydrate candidate 的 tags、Cues、FileEvidence；
5. `AssembleReleaseCandidate`；
6. 只序列化 Release Graph allowlist。

任一解析或组装错误都失败，不保存部分输出。adapter 不调用 artwork、lyrics、quality badge、
local evidence、repository、REST 或 queue。

### 本地 SQLite

当前 smoke 工具把临时 rows 写入 `v0-reference.sqlite`。SQLite 至少包含：

- `baseline_manifest`；
- `releases`；
- `media`；
- `tracks`；
- `files`；
- `release_credits`；
- 明确可比的 grouping/field evidence 表。

所有 identity 使用 corpus-relative 来源构造的稳定键，不保存宿主绝对路径或 runtime UUID。
写入完成后检查 schema version、FK、唯一键、图闭合、position、文本卫生和聚合计数，再从
SQLite 以固定排序导出 canonical JSON。SQLite 与 JSON 的稳定键、行数和内容摘要必须一致。

SQLite 是本地只读审计载体，canonical JSON 是现有 comparator 的输入；二者都位于 Git
忽略目录、权限为 `0600`，不进入提交或任务文档。

## 基准身份

推荐 manifest：

```text
implementation=v0_release_graph_generated_corrected
generation_mode=standalone_scanner
baseline_scope=release_graph_only
degraded=false
```

同时记录 V0 archive hash、adapter hash、corpus hash、SQLite schema version、生成时间、
Release/Medium/Track/File 分项计数及 excluded evidence scope。

此前 production 扫描的 `done + error/failed` 与 8 个 local-evidence 错误只留在
`v0-production-scan-blocker.md`，不进入新基准门禁或身份。

## 审批记录

这是对已批准方案的实质变更：不再实现 degraded production guard，而是新增 standalone
adapter 和 SQLite writer。用户于 2026-09-03 在最新最终规划摘要之后明确回复“批准”；
实施门禁已关闭，但真实资产权限仍严格限于只读。
