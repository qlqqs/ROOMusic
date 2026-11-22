# 自动整理 B1：执行计划

分支 `feat/auto-curation-b1-musicbrainz`。后端实现与测试按全局约定交给不同的 Codex 子代理；前端合同登记、合同文档与验收由主代理做。
子代理不提交 Git；改动范围写进任务说明，验收时检查有无越界。

## 步骤

两条实现线分开进行：外部源一线在主工作目录，扫描标识一线在独立工作目录（`temp/` 下的 git worktree），验收后把后者的改动合到任务分支。
迁移版本必须连续，两条线也都要登记迁移回退的夹具，所以两份迁移先由步骤 P0 一起落好，之后两条线都不再碰迁移文件与夹具。

集成测试脚本用全局互斥锁（`scripts/test-integration.sh` 的 `flock -n`），两个工作目录不能同时跑：遇到锁被占用就等待后重试，不当作失败。

### 准备

- [x] P0 迁移（`gpt-6.2-sol`，主工作目录）：`backend/migrations/0059_*`（`track_identifiers`，design 7.3）与 `0060_*`
  （`curation_source_settings`、`external_links`，design 5.1、6）的正反脚本，迁移回退测试里 59、60 两个版本的夹具。
  验证：`ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$'`。主代理审阅后把这些文件复制到独立工作目录。

### 外部源一线

- [x] S1 实现（`gpt-6.2-astra`）：`design.md` 第 1–4、8 节。
  - 迁移 `steward/egress` → `platform/egress`，改全部 import 与依赖规则。
  - 新包 `sources`（声明、能力接口与类型、限速器、文件缓存、带重试的请求、日志）与 `sources/musicbrainz`（实体级接口、三种能力、disc ID）。
  - 管家两个工具改接（`web.New` 签名不变，增加 `WithMusic`）；删除管家包里的限速器与基础地址变量。
    工具级的现有测试只改注入方式与 import，不改断言、不加用例；限速器的白盒用例随限速器搬到 `sources`，断言内容不变。
  - `app` 装配与缓存清理。
  - 允许修改：`backend/internal/platform/egress/`、`backend/internal/steward/`（仅 import 与 `web` 的音乐资料部分）、
    `backend/internal/sources/`、`backend/internal/app/app.go`、`steward.go`、`dependency_rules_test.go`。
  - 验证：`go build ./...`、`go vet` 涉及的包、`go test ./internal/platform/egress/... ./internal/steward/... ./internal/sources/...`、
    `go test ./internal/app -run TestPackageDependencyDirections`。
- [x] S2 实现（同一个子代理接着做）：`design.md` 第 5、6 节。
  - `curation` 的源开关、闸门（含放行回调）、外部对应的四个函数；`curation/httpapi` 的两个接口；装配。
  - 允许修改：`backend/internal/curation/`、`backend/internal/app/app.go`、`dependency_rules_test.go`。
  - 验证：构建、`go vet`、依赖规则测试。
- [x] S3 测试（另一个子代理，`gpt-6.2-astra`）：B1-V1 到 B1-V9、B1-V14 到 B1-V16 中属于这一线的部分。
  - 受控的本地 HTTP 服务代替 MusicBrainz；限速、缓存有效期、重试的等待用注入的时钟，不靠真实时间。
  - 新的接口样本 `curation/sources.json`、`curation/source-updated.json`。
  - 允许修改：`backend/internal/sources/`、`backend/internal/steward/web/`、`backend/internal/curation/`、`backend/internal/app/`
    下的测试文件与 `testdata/api-samples/curation/`；不改非测试代码，发现实现缺陷时写进报告。
  - 变异检查：闸门的三个条件各去掉一个；去掉出站前的放行回调；后台把断网报成没有结果；限速器不让对话优先；disc ID 的补零。
  - 完整报告写到 `temp/b1-sources-test-report.md`。

### 扫描标识一线

- [x] T1 实现（`gpt-6.2-astra`，独立工作目录）：`design.md` 第 7 节。
  - `catalog/metadata` 的标识类型与规范化；解析器的三个入口与 `UFID`、`TSRC`；
    `SourceObservation` 与两处观察构造；落库；`CommonVersion` 升到 4 并同步 `library-scan-incremental.md` 里记录版本的那一行。
  - 允许修改：`backend/internal/catalog/metadata/`、`backend/internal/library/`、`backend/internal/catalog/ingest/`、
    上述规范文件的那一行。
  - 现有测试里断言版本号或观察字段清单的，按新值修改；不加新用例。
  - 验证：`go build ./...`、`go vet` 涉及的包、`go test ./internal/library/... ./internal/catalog/metadata/... ./internal/catalog/ingest/...`。
- [x] T2 测试（另一个子代理，`gpt-6.2-sol`，同一个独立工作目录）：B1-V10 到 B1-V13、B1-V15 中属于这一线的部分。
  - 解析器：格式 × 标识种类的表驱动用例（沿用 `parsertest` 与现有的夹具函数）；命名陷阱、多值、非法值、键名变体、`UFID` 的其他 owner。
  - 扫描集成：落库、改标签重扫、删标签、多轨 CUE、候选未变不重写、版本升级后的增量扫描。
  - 接口样本逐项不变：`ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$'`，不带更新参数。
  - 变异检查：“Track Id” 映射成曲目 ID；虚拟轨带上录音 ID；非法值不丢弃。
  - 完整报告写到 `temp/b1-scan-test-report.md`。

### 合并之后（主代理）

- [x] M1 把扫描标识一线的改动合到任务分支，删除独立工作目录；合并后跑一次
  `go build ./...`、依赖规则测试、迁移往返（0059 与 0060 一起）。理由：两条线各自验证时看不到对方的迁移与包。
- [x] M2 前端：`frontend/src/test/api-contract.test.ts` 把两份新样本登记为暂未消费；跑这个测试文件。
- [ ] M3 合同文档：ADR 0006 第 8、10、13 节补充说明（对话工具不受开关影响、管家工具用实体级接口、扫描补读条码与编号的例外），
  交付时请用户确认；新增 `.trellis/spec/backend/sources-contracts.md`；更新 `steward-web-contracts.md`（音乐资料一节）、
  `steward-runtime-contracts.md`（包依赖表里的 egress 路径）、`library-scan-tags.md`（外部标识的键）、`curation-contracts.md`
  （源开关、闸门、外部对应）、`database-guidelines.md`、`directory-structure.md`、`index.md`；
  ADR 0006 第 13 节与路线图 M7 的 B1 行；父任务 `implement.md` 的 B1 行。
- [x] M4 逐条验收，写 `verification.md`：每条 B1-V 由谁在哪一轮运行、结果，实现中的修订，未验证的项。
- [ ] M5 交付摘要交给用户确认后：提交、合并回 `main`、删除任务分支、归档任务。

## 验证命令的范围

- 集成测试一律用 `sg docker -c 'ROOMUSIC_TEST_RUN="<正则>" mise exec -- ./scripts/test-integration.sh'`，按用例名收窄。
- 返工只重跑失败的、被修改的测试与改动文件直接影响的包。
- 不跑全量集成测试作为门禁：本任务不改视图与现有接口。M1 的合并后检查是唯一一次扩大范围的运行，理由已写在该步骤里。

## 风险点与回退点

- 管家工具的输出必须逐字节不变：S1 的审阅重点是 `Metadata` 的投影与错误码对应。
- `CommonVersion` 升级让所有实例在下一次扫描重新解析全部文件：T2 用版本升级的集成用例确认 ID 不变。
- 两条线各自是一个回退点：扫描标识一线不依赖外部源一线，任一条出问题都可以单独推迟。
- 条码的 `OR` 查询与 `/isrc/` 的返回结构未对真实服务核实，记入 `verification.md` 的未验证项，由父任务收尾的真实试用覆盖。

## 启动前的检查

- [x] `prd.md`、`design.md`、`implement.md` 齐备；`implement.jsonl`、`check.jsonl` 已登记规范与调研。
- [x] 规划子代理的审阅完成并记入 `prd.md`（2026-11-22，五条必须改的与六条建议全部采纳）。
