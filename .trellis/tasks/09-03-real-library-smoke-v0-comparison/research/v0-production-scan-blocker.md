# V0 production scan 阻塞记录

## 结论

固定 V0 归档可以在隔离环境中完成构建、迁移、setup、登录、目录注册和正式扫描触发，
但对当前真实 corpus 的扫描不能达到 `done/succeeded`。持久化权威终态为
`error/failed`，因此当前结果不能作为 `v0_generated_corrected` 基准，也没有保留 dump、
snapshot 或失败数据库。

这不是音乐目录权限、Docker 网络或 PostgreSQL 版本问题，而是固定 V0 源码内部的 local
evidence producer/persistence 合同不一致。原设计只授权运行时与构建适配，不授权修改
V0 scanner 业务映射。用户随后批准保持 V0 不变，并只在历史图计数与已知 evidence 错误
精确匹配时采用 Release Graph-only degraded 基准；其它失败仍必须 fail closed。

## 脱敏运行证据

- V0 代码身份：固定归档 SHA-256
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`。
- 真实音乐通过 `/music` 的只读 bind mount 提供；运行时 inspect 已确认 `RW=false`。
- V0/current 数据库、V0 Redis/Meilisearch、应用数据目录、端口、network 和 Compose
  project 均为本轮临时资源；失败后已精确清理，现有 `roomusic` PostgreSQL 未复用。
- V0 共建立 66 个持久化扫描目录记录，其中 8 个为 error：
  - 7 个 `invalid_artifact_status`；
  - 1 个 `unresolved_quality_badge`。
- V0 `/api/scan/progress` 最终返回 `done`，但 `scan_runs` 为 `error/failed`。Smoke runner
  以 PostgreSQL authority 为准，正确拒绝把内存进度当作成功。
- 错误分类只使用固定 SQL 前缀 allowlist；没有读取或保存原始 `error_message`、entity key、
  文件名、绝对宿主路径、标签、CUE 或抓轨日志内容。

## 源码合同证据

### 1. Rip-log artifact 使用了错误的状态词汇

`internal/scanner/logparser.go` 的 `ParseRipLogBytes` 将 rip-log 解析状态同时写入
`RipLogEvidence.Status` 和 `LocalEvidenceArtifact.Status`。前者的合法值包括
`parsed_secure`、`present_unverified`、`partial` 和 `parse_error`。

但 `internal/models/models.go`、
`internal/database/local_evidence_validation.go` 和 migration
`000008_local_evidence_contract.up.sql` 对 artifact lifecycle 的共同 allowlist 是
`candidate/default/conflict/orphaned/unreferenced/unsupported/error`。这两个状态域语义不同，
成功 rip-log 的 producer 输出无法通过 persistence 校验，扫描目录因而进入 error。

### 2. 一个 quality badge 无法绑定已保存实体

`internal/tasks/scan_task_evidence.go` 先按 parsed file 构造 Track quality badge；
`internal/database/local_evidence_validation.go` 随后只允许通过已保存 Track 的
`file_path`、`parent_file_path` 或 medium/track position 解析 entity key。本轮有一个 badge
无法在 SaveRelease 后的索引中解析。源码进一步证明 `executeScanPipeline` 对每个拆分后的
candidate 单独 `SaveRelease`，却把目录级完整 `parsed.Files` 传给
`buildLocalEvidenceBundle`；因此 candidate 外 track 的 badge 会尝试绑定到当前 candidate
的 Track index。该结论不需要导出原始 entity key，真正 owner 是 task pipeline 的
candidate evidence scope，而不是放宽 repository resolver。

### 3. Release Graph 与 scan 成功不是同一件事

V0 pipeline 在保存 Release 后才保存 local evidence bundle，因此失败数据库可能已经包含
部分或全部 Release Graph。但 durable scan outcome 明确为 failed，不能仅凭已出现 catalog
行把它提升为成功基准；这会违反 PRD 的完整终态门禁，也无法证明失败目录的输出完整。

## 路线决策

> 后续更新：本文件记录的路线 B 曾获批准，但用户随后提出直接复用 V0 scanner 核心，
> 单独生成并保存 Release Graph。代码研究确认这与 V0 历史 golden generator 同源，因此
> 当前推荐路线已改为 `v0-standalone-reference-design.md` 所述 standalone scanner +
> SQLite。路线 B 不再实施，仅作为为何不使用 production 失败库的历史记录。

### 路线 A：极窄的临时 V0 兼容补丁（未选择）

只修改校验后临时解包的 V0 副本，并把每个被改源文件的原始 hash、适配后 hash 和总适配
hash 写入 manifest：

1. 在 scanner 到 local-evidence persistence 的边界，将 rip-log artifact lifecycle 映射为
   `candidate`/`unsupported`/`error`，同时完整保留 `RipLogEvidence.Status` 的解析语义；
2. 先以脱敏 fixture 复现 unresolved badge，再只修正 entity binding owner；
3. 证明适配前后 parser、organizer、Release/Medium/Track 和字段输出不变；
4. 重新从空数据库运行完整 V0 scan，仍严格要求 `done/succeeded`。

这条路线不改变当前 ROOMusic，也不修改固定归档，但它确实改变 V0 临时副本中的业务
映射，超出现有“只做 runtime 适配”的批准边界，必须由用户另行明确同意。

### 路线 B：严格受限的 Release Graph-only degraded 基准（已撤回）

用户于 2026-09-03 在条件式方案之后回复“批准”，曾批准更新规划并按以下全部条件继续：

1. 固定 V0 代码不变；
2. REST 必须为 `done`、PostgreSQL 必须为 `error/failed`；
3. Release/Medium/Track 必须精确为历史人工基准 `110/112/466`；
4. 目录错误必须精确为 8 个，分类多重集必须精确为
   `invalid_artifact_status=7`、`unresolved_quality_badge=1`；
5. 只导出 Release Graph allowlist，manifest 明示 `degraded=true`、
   `baseline_scope=release_graph_only` 和 excluded evidence；
6. 任一条件变化均停止，不运行 current，也不把结果称为完整
   `v0_generated_corrected`。

该选择曾同步到 PRD、技术设计和实施计划，基准名为
`v0_release_graph_degraded`。在用户提出更直接的 standalone scanner 方案后，本路线已从
当前规划撤回；未实现 degraded guard，也未据此启动 current。

### 路线 D：standalone scanner + normalized SQLite（已批准并实施）

只在 hash-locked V0 临时解包副本中增加 smoke adapter，直接调用
`Walk -> ParseTags/ParseCueSheet -> BuildReleaseCandidates -> AssembleReleaseCandidate`，
不启动 V0 production runtime，不修改 scanner，也不保存 local evidence。adapter 临时输出
经版本化 SQLite 完整性校验后导出 canonical JSON。详见
`v0-standalone-reference-design.md`。

用户随后明确批准该路线。最终真实验收以 `v0_release_graph_generated_corrected` 运行
成功，V0/current 的 `current_regression=0`、未知分类为 0；本节记录的 production 缺陷
只保留为路线决策证据，不再阻塞任务。

### 路线 C：停止 V0 重建并继续寻找历史数据库（未选择）

保持当前严格合同，不修改 V0；代价是任务继续阻塞于外部备份，无法在现有本机资产上
完成同 corpus 对照。

## 当前清理状态

- 没有保留本轮失败的数据库、dump、raw snapshot、HTTP 响应或真实文件清单。
- 没有残留 `roomusic-smoke-*` 容器、volume 或 network。
- 没有修改真实音乐、固定 V0 归档、`ROOMusic-V0` 目录或现有开发数据库。
