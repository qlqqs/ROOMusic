# 设计

需求编号见 `prd.md`，代码定位见 `research/coverage-audit-2026-11-26.md`。路径相对 `backend/`。

## 分工与边界

| 工作 | 内容 | 主要位置 |
| --- | --- | --- |
| W1 整理与模型 | R1–R3、R6 的整理证据判据、R9 的放宽去噪、R11–R16、规则版本与升级补查 | `internal/curation/**`、`internal/curation/model/**`、`internal/curation/stewardtools/**`、`internal/app/curation_model.go` 与相关的 `internal/app/*.go`、`internal/steward/runtime/review.go`、新迁移 |
| W2 扫描 | R4、R5、R6 的组织器部分、R7、R8、R9 的目录解析与推断去噪、R10 | `internal/library/parser/**`、`internal/catalog/ingest/**`、`internal/catalog/metadata/**`；`internal/curation/local_items.go` 只改乱码候选片段的接入 |
| W3 测试 | R17–R19 的样本与度量、W1/W2 的定向集成用例 | `internal/app/*_test.go`、`internal/app/testdata/curation_regression/samples.json`，必要的 `internal/upstreammock/**` |
| W4 真实试用 | R20 的曲库扩充、长网页回环、三轮试跑与报告 | `cmd/roomusic-curation-bench/**`、`internal/app/curation_live*_test.go`、任务的 `research/` |

补充分工：

- R21（首条消息内容与新增工具）归 W1。
- R22 的限信息 oracle 归 W3：在 `internal/upstreammock/**` 中实现，oracle 只从本次请求历史与工具结果中挑选值。
- R23 的失败归因归 W3/W4：读取 `steward_messages` 与工具结果，对照标注。

### R21 信息完整

- 列出后端判定与核实所需的每一类信息，与首条消息字段、工具能力逐项对照，并把这张对照表写进合同第 26 节。目前已知的缺口：
  - 发行组标题与版本列表：给 `get_music_metadata` 增加 `release_group` 类型，返回标题和带结构摘要的版本列表；
  - 艺人别名：给 `get_music_metadata` 的艺人类型增加别名；
  - 首条消息补上此前各档的原因和后端接受写入的佐证要求。
- 来源不可用的工具错误要写清楚，模型不应因此放弃（与 R1 配合）。

W1 与 W2 互不修改对方的文件；共同约定见下节。W3 可以从基线并行起步，先写样本与度量（这时预期会失败），待 W1、W2 合入后跑通。

## 共同约定

- **真实性**（`catalog/metadata/curation_authenticity.go`）：
  - 新增来源标记 `legacy_encoding`：唯一解码时写入本地层的值，属于推断。
  - 新增 `garbled`：多解码歧义时的扫描值。按占位处理，不进入查询，可以进入本地整理。
  - 英文占位词并入占位判定。
- **本地整理片段**：乱码歧义时，各候选解码作为额外的“本地片段”提供给模型，`submit_local_cleanup` 的复核仍要求值出现在片段里。
- **推断的碟号与曲位**（R7、R8）：沿用现有的推断曲位标记，只能走结构吻合这一级（合同 23.2）。

## 关键设计

### R1 来源不可用

- 在单项的运行记录中，累计本项工具结果里 `source_unavailable` 的次数。`tool_execution_failed` 中由来源 5xx、429 或网络错误引起的，也计入；需要把来源错误透传为可区分的错误码。
- `abandon` 与触限路径的处理：本项有来源不可用记录、又没有成功提交时，改走 `try_later`：
  - 待定项保持 `waiting_route` 与原路线；
  - `next_attempt_at = max(来源恢复时刻, now + 退避)`；
  - 不增加尝试次数与代次。
- 不改 `FinishUnlisted` 的语义。
- 模型看到的错误文本要写明：来源暂不可用，稍后会自动重试，不需要放弃。

### R2 组级标题佐证

- `structuralNameValues` 遇到组级对应时，读取发行组（复用现有的 MB 读取与缓存），取 `sources.Group.Title`。
- 同时读取组内版本列表（复用 `checkModelEditions` 的读取）。只要有一个版本的标题规范化后等于本地原值，就不提供佐证。
- 佐证只适用于专辑标题字段。审查证据写明 `release_group` 标题、版本数和结构吻合结论；审查策略升到 v7，同步更新 `steward-review-contracts.md` 与 `TestReviewPolicyRulesMatchSpec`。

### R3 提示与审查路径

- 修改 `backgroundPrompt` 的 LookupCandidate 与 WebFallback 文案，以及 `resolve_release_match` 的说明：
  - 结构吻合而分不出版本时，提交任一吻合版本，后端只建立发行组级对应；
  - 不要因为分不出版本而放弃。
- `model_editions.go:16` 跳过的审查路径改为：审查批准后、写入前，同样调用同组多版判断。

### R11 子集与超集

- 在结构核对中新增“组级部分吻合”。只在完整结构吻合失败、没有版本标识时才尝试。
- 判定只读已经取到的候选详情，不另发请求。条件见 PRD R11。
- 结论写 `release_group_match`，`rule` 为 `partial_subset` 或 `partial_superset`。
- 程序层和 `ModelMatcher.Plan` 共用这一判定。模型提交子集或超集时，同样只能落到组级。

### R12–R16

- R12：在 `match.go` 的宽松键前加入署名规范化函数，程序与模型核对共用。
- R13：`match_snapshot.go:270-272` 增加时长检查；没有可比时长时，维持现状。
- R14：`model_verification.go` 判反证时，比较的名字集合加入外部艺人别名（复用已有的 `inc=aliases` 读取和缓存）和巡检规范名。
- R15：`match_search.go` 带 `arid:` 的查询没有结构通过时，补一次按名字的查询；请求数在度量里体现。
- R16：在 `curation_model.go:212` 去掉代码块包裹后逐项解析；`scheduler.go:515-518` 只给不合格项记次；三次不合格时升到 `waiting_route/lookup`。

### 规则版本与升级

- MatchRuleVersion 升到 103，LocalRuleVersion 升到 3。
- 升级补查只针对两类状态：
  - `unlisted`、`uncertain`，以及冲突码为 `track_count`、`position_conflict`、`artist_mismatch`、`tag_text_mismatch` 的待定项，登记一次 `requery_match_version`；
  - 本地层：识别摘要按新规则重算，变化的重新入队。
- 已 `matched` 的不重查（合同 23.6）。迁移只加枚举或索引，不改数据。

## 兼容与风险

- R10 会改变少数混放目录的分组：受影响的本地发行会重新识别，但不会产生错连。风险在于发行编号变化，文件锚点沿用组织器现有的稳定锚规则。
- R4 只把解码值写入本地层，扫描值不变；撤销即可回到原值。
- R11 只产生组级对应，组级不能写版本字段，也不能参与并入（合同 23.3），错误面有限。
- 回滚：每个工作单独合并，可以逐个 revert。迁移只加枚举，回滚时保留。
