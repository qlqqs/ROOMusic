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
- 2026-05-27 的最终记录为 110 个 Release 全部 `reviewed`，112 个 Medium、466 个 Track、
  466 个 file evidence；final validation 通过，reviewed-only diff 为 `No differences.`。
- 2026-05-28 曾将 SQLite candidate/golden 导入 PostgreSQL `golden` schema；记录为两套
  dataset 各 110 个 Release、各 631 条字段合同，随后 SQLite 活跃工具链被退役。

因此候选优先级为：

1. 固定 V0 归档对当前同一 corpus 重新生成的 `v0_generated_corrected` 数据库输出。
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
- 用户确认这些生产生成规则已经包含人工修正，因此重建产物标注为
  `v0_generated_corrected`，是本任务的权威行为基准；旧 review notes 不属于本次对照
  合同。
- 历史 UAT 数量和关键场景继续作为 sanity check；当前 corpus 漂移可以解释历史数量
  变化，但 V0 与当前实现必须在同一当前 corpus 摘要下逐项比较。

## 规划批准后的下一步

1. 先完成 `design.md`、`implement.md`、PRD 收敛和实施/检查上下文清单，再呈交最终
   规划摘要；得到后续明确实施批准前不启动扫描。
2. 实施时在独立 V0 PostgreSQL/Redis/临时 data 环境中只读挂载真实资产，运行固定归档
   中的正式 scanner，导出不可变的重建参考快照。
3. 用历史聚合数量、真实语料场景与 program-owned 合同交叉验证，并把成功数据库导出为
   权限受限、Git 忽略且带 hash/manifest 的本地基准。
4. 若之后发现一个或多个历史数据库，先只读计算 hash、schema/review 覆盖和聚合计数，
   仅报告非敏感差异并请求用户选择，不按时间自动决定。

## 证据来源

- `ROOMusic-V0/.planning/phases/01.1-roon-style-release-graph-refactor/01.1-UAT.md`
- `ROOMusic-V0/.planning/phases/01.1-roon-style-release-graph-refactor/01.1-01-SUMMARY.md`
- `ROOMusic-V0/.planning/phases/01.1-roon-style-release-graph-refactor/01.1-05-SUMMARY.md`
- `ROOMusic-V0/.planning/quick/260527-golden-phase-01-1-golden-review-complete/260527-golden-SUMMARY.md`
- `ROOMusic-V0/.planning/quick/260528-p2z-golden-sqlite-postgresql-golden/260528-p2z-SUMMARY.md`
