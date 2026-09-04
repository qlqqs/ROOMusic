# V0 基准资产只读盘点

## 盘点结论

当前没有找到可直接读取的 V0 SQLite、PostgreSQL dataset、dump 或 backup。用户已
确认固定 V0 代码的生成结果已经包含此前人工修正，因此可通过正式 scanner 重新生成
本次对照所需的权威行为基准；历史数据库不再是启动 Smoke 的阻塞项。

## 已检查位置

- 当前 ROOMusic 工作区及其任务归档。
- 同级 `ROOMusic-V0` 源码/规划副本，包括已被忽略或退役的 `.testdata` 约定位置。
- `/root`、`/mnt`、`/srv`、`/tmp` 和本机 Docker volume 中与 ROOMusic、golden、candidate、
  corpus answer 相关的 SQLite、dump 和 backup 常见文件名。
- `ROOMusic-migration.tar.gz` 的归档目录；归档包含 V0 源码，但不包含 `.testdata/answer`
  或 SQLite 数据库。
- Docker 当前只有现项目 `roomusic` 的 PostgreSQL 容器和 volume，没有可识别的 V0
  Compose volume。
- 已对当前 `roomusic-postgres-1` 做只读 catalog 查询：只有当前 Core 0 的 `public`
  schema，不含 `golden` schema 或 V0 dataset；其 volume 属于当前项目，不能用作 V0
  基准或本任务的临时数据库。
- 当前 Git/远端历史从新 Core 0 开始，无法恢复 V0 historical golden 工具或数据库。

盘点过程未解包归档、未启动/停止容器，仅对当前 PostgreSQL catalog 做只读查询；没有
读取或修改真实音乐内容，也没有修改任何数据库。

## 历史基准语义

V0 Phase 01.1 文档确认：

- `candidate.sqlite` 是程序生成物，可重建，不是人工真相源。
- `golden.sqlite` 是人工审查资产，禁止 candidate generation 覆盖。
- schema v2 区分 evidence、program-owned expected 和 AI/human reviewed expected。
- 历史记录存在两个口径：初始 candidate 为 110 个 Release、112 个 Medium、466 个 Track
  和 466 个 file evidence；D-51 消除 CUE+split 重复 Track 后，最新 candidate 为 110 个
  Release、112 个 Medium、407 个 Track，物理 file evidence 仍为 466。110 个 Release
  最终全部 `reviewed`，final validation 通过，reviewed-only diff 为 `No differences.`。
- 2026-05-28 曾将 SQLite candidate/golden 导入 PostgreSQL `golden` schema；记录为两套
  dataset 各 110 个 Release、各 631 条字段合同，随后 SQLite 活跃工具链被退役。

因此候选优先级为：

1. 固定 V0 归档对当前同一 corpus 重新生成的
   `v0_release_graph_generated_corrected` standalone SQLite 输出。
2. 若日后找回，经用户确认且能证明身份的 schema v2 `golden.sqlite` 或 PostgreSQL
   `golden` dataset，仅作为审计/交叉验证来源。
3. 若出现多个历史或重建候选，先盘点 hash、schema、类型、聚合计数和时间，再由用户
   选择；不得自动使用最新版本。

## V0 重建可行性

- `ROOMusic-migration.tar.gz` 的 SHA-256 为
  `fe25388328698b26991ea3b59a14406a155eb92d578a9be2a68d67d331ecf97d`，可作为 V0
  代码版本锚点。
- 归档包含 V0 正式 scanner、33 个 PostgreSQL migrations 和 PostgreSQL/Redis/asynq
  运行边界；可通过 `/api/library-paths` 注册只读目录，并通过 `/api/scan/trigger` 启动
  正式扫描。V0 Compose 已有 `/music:ro` 挂载约定。
- `internal/scanner/real_music_integration_test.go` 留有 Box leaf、WEB/CD、CUE virtual、
  散落文件和多碟等真实场景，可用来交叉验证重建结果的行为覆盖。
- 归档与 `ROOMusic-V0` 当前目录的 scanner、database、应用入口、migration 和 Compose
  关键文件已做逐文件只读比较，未发现差异。
- 用户确认这些生成规则已经包含人工修正，因此 standalone 重建产物标注为
  `v0_release_graph_generated_corrected`，是本任务的权威行为基准；旧 review notes 不属于本次对照
  合同。
- 历史 UAT 数量和关键场景继续作为 sanity check；当前 corpus 漂移可以解释历史数量
  变化，但 V0 与当前实现必须在同一当前 corpus 摘要下逐项比较。

## 已批准并实现的重建路线

1. 固定归档只解包到本轮临时目录，并单独记录归档 hash 与 smoke-owned adapter hash；
   adapter 不修改 V0 scanner，只调用其 `Walk -> parse -> BuildReleaseCandidates ->
   AssembleReleaseCandidate` 核心链路。
2. V0 exporter 运行时使用 `/music:ro`、`/output:rw`、只读根文件系统和
   `network_mode=none`，不再启动 V0 PostgreSQL、Redis、Meilisearch 或 REST。
3. exporter rows 必须先通过 normalized SQLite 的外键、唯一性、图闭合与内容校验，再从
   SQLite 回读 canonical JSON；任一步失败都发生在 current 启动之前。
4. 实施早期的合成 WAV 隔离演练跑通了上述链路与 current 首扫/重扫，且清理后本轮容器、
   volume 和临时镜像均为零；该阶段只证明工具链，不作为真实资产结论。
5. 若之后发现一个或多个历史数据库，仍先只读计算 hash、schema/review 覆盖和聚合计数，
   仅报告非敏感差异并请求用户选择，不按时间自动决定。
6. 最终真实资产验收已完成：同一 corpus 下 V0 为 68 Release/69 Medium/225 Track/284 File，
   current 首扫与重扫均为 68/69/284/284；current A/B 差异为 0，V0/current 的
   `current_regression=0`、未知分类为 0，资产前后摘要一致。

## 证据来源

- `ROOMusic-V0/.planning/phases/01.1-roon-style-release-graph-refactor/01.1-UAT.md`
- `ROOMusic-V0/.planning/phases/01.1-roon-style-release-graph-refactor/01.1-01-SUMMARY.md`
- `ROOMusic-V0/.planning/phases/01.1-roon-style-release-graph-refactor/01.1-05-SUMMARY.md`
- `ROOMusic-V0/.planning/quick/260527-golden-phase-01-1-golden-review-complete/260527-golden-SUMMARY.md`
- `ROOMusic-V0/.planning/quick/260528-p2z-golden-sqlite-postgresql-golden/260528-p2z-SUMMARY.md`
