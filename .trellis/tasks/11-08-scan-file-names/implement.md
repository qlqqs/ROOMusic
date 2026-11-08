# ③ 文件名规则与同目录归组：实施计划

设计见 [design.md](./design.md)。O 线第 2 项：**在 ② 合入 O 线分支之后开始**（依赖 ② 的 `locateObservation` 碟目录识别、碟号优先级与收藏转移）。

## 0. 开工前

- 第 9 节（文件名艺人是否进艺人页）由主会话统一问用户，答案写进 prd.md 与 design.md；裁决前先做第 1 节的 1–8 步，第 9 步等答案。
- 碟号“碟目录 > 文件名”、年份防误判“首位为 0 + 同目录多数”已定稿；八号机统计已执行（[research/m8-filename-stats.md](./research/m8-filename-stats.md)），3 位/4 位分支保留。
- 派活 prompt 必须写进用户全局约定：
  - 测试只测改动直接影响的部分，跑最小相关测试集，不做演练、不重复验证、不过度兜底。
  - 前端文案：不写防御性说明、不加不必要的说明；确实需要的说明用一句日常话（本项只加一个归组方式名称）。
- 子 agent 不提交 git。

## 1. 后端（按顺序）

1. `catalog/ingest/filename_numbers.go`（新）：单文件五级规则、编号前缀、同目录采信（design.md 第 3.1–3.3 节）；标题清洗与拆艺人（第 5 节）。
2. `catalog/ingest/natural_order.go`（新）：`naturalLess`；`assignFallbackTrackPositions` 改用它（第 4 节）。
3. `organizer.go` 预处理：在 `resolveCueCandidateGroups` 之后、定位之前，接在 ② 的专辑名碟号预处理后面，按目录调用第 1 步，写回 `TrackNumber`/`DiscNumber`/`Title`/`Artist` 与来源（第 3.4、5 节）。
4. `organizer.go` 组装介质：轨位保留 `filename_fallback`；碟号优先级加 `filename_fallback` 一级；同介质冲突保护（第 3.3、3.4 节）。
5. `catalog/ingest/continuity.go`（新）：`continuousAlbum` 与两种取数；`organizer.go` 同目录拆分前先检验（第 6 节）。
6. `organizer.go` 按格式切分：`CandidateFormatSplit`、`stableAnchorPartition` 分支、`VersionGroup`（第 7.1 节）。
7. 同组写入：`OrganizedCandidate.JoinGroupID`；`persist.go` `persistRelease` 新建时用已有组；`catalog/ingest/version_group.go`（新）`PlannedVersionGroup`、`ReleaseGroupOf`；`library/scanner.go` `persistStagedCandidates` 按 `VersionGroup` 定目标组，`persistOneCandidate` 返回 Release ID（第 7.2 节）。
8. `catalog/read.go` `normalizeCandidateKind` 白名单加 `format_split`。
9. 按第 9 节裁决改 `catalog/internal/artistgraph/facts.go` 一处条件（T 线共享文件，改动只限这一处，回报里单列）。
10. 合同同步：
    - `library-scan-contracts.md`：位置 fallback 优先级（标签 > CUE > 专辑名碟号 > 碟目录 > 文件名 > 补位）、文件名五级与同目录采信、同介质冲突保护、自然序补位、标题清洗与拆艺人、同目录连续性合并、按格式切分与同组写入、`format_split` 种类。
    - `default-version-contracts.md` 第 7 节：扫描在新建发行时可以加入已有组（按格式切分的版本），仍不移动已有发行的组、不碰组的两列。

## 2. 前端（一个小任务，新开 Opus 子 agent，做完即结束）

1. `frontend/src/features/catalog/labels.ts`：`candidateKindLabel` 加 `format_split: "同一目录按格式分开"`。
2. `frontend/src/features/catalog/labels.test.ts`：表里加一行。
3. 不加别的说明文字。来源 `filename_fallback` 已有名称“文件名（推断）”，不改。

## 3. 测试（只补改动直接影响的）

单元（`internal/catalog/ingest`，无数据库）：

- `filename_numbers_test.go`：design.md 第 3.1 节五级各一到两例、零归空、4 位首位非 0 不拆、同目录采信与降级（`01 99 Luftballons`）、第 5 节清洗与拆艺人表逐行。
- `natural_order_test.go`：`1、2、10`，前导零，非数字段。
- `continuity_test.go`：连续、缺号、重号、没有第 1 碟、缺碟（1、3 碟）、只有 1 首。
- `organizer_test.go` 增补：design.md 第 11 节表逐行；同介质冲突保护一例；碟号优先级加文件名一级；`format_split` 的锚点与 `VersionGroup`。

PostgreSQL 集成（隔离库）：

- `internal/library`：一个合成素材扫描用例：同目录 flac + mp3 各 1–N → 两个 Release 同一组；首扫前已有混合 Release 时一个沿用、一个新建并加入同组；再扫一次不变。
- 第 9 步若改了 `facts.go`：在 `internal/catalog` 现有艺人整理测试里加一例，断言文件名艺人按裁决进或不进艺人。

不新增：真实服务端到端用例、性能测试、前端组件测试。

## 4. 验证命令（最小相关集）

```bash
cd backend
gofmt -l ./internal/catalog ./internal/library
go vet ./internal/catalog/ingest ./internal/library ./internal/catalog
go test -count=1 ./internal/catalog/ingest -run 'Filename|Natural|Continuity|Organize|FormatSplit'
ROOMUSIC_TEST_DATABASE_URL=<O 线隔离库 URL> go test -count=1 ./internal/library -run 'FormatSplit|VersionGroup'
# 仅在第 9 步改了 facts.go 时：
ROOMUSIC_TEST_DATABASE_URL=<O 线隔离库 URL> go test -count=1 ./internal/catalog/internal/artistgraph ./internal/catalog -run 'Filename'
cd ../frontend
npx vitest run src/features/catalog/labels.test.ts
```

不跑全量：改动集中在 organizer 纯函数、写入路径的同组分支与一个标签；上面的集合直接覆盖。全量留给合入 `main` 后主会话的集成复核。

样本库与真实库只读 smoke（验收用，由主会话安排一次）：列出轨位、标题、艺人、分组发生变化的曲目与专辑；样本库根目录的网易云文件（`艺人 - 标题.flac`）检查是否有标签，有标签的应当不变。

## 5. 风险文件与回滚点

| 文件 | 风险 | 回滚点 |
| --- | --- | --- |
| `catalog/ingest/filename_numbers.go`、`organizer.go` 预处理与介质组装 | 无标签文件的轨位、标题、艺人变化 | 步骤 1–4 一个提交 |
| `continuity.go`、`organizer.go` 同目录拆分前检验 | 不同专辑被合成一张 | 步骤 5 单独提交 |
| `organizer.go` 按格式切分、`persist.go`、`version_group.go`、`library/scanner.go` | 版本切分与组归属 | 步骤 6–8 一个提交 |
| `artistgraph/facts.go` | 与 T 线冲突 | 步骤 9 单独提交，合入时与 T 线协调 |

## 6. 合入前核对

- [ ] ② 已在 O 线分支上；本项没有改 `parser/`，也没有改 `catalog/ingest/observation.go`。本项改动的共享文件：

  | 文件 | 本项改动 | 同时在改的线 |
  | --- | --- | --- |
  | `library/scanner.go` | `persistStagedCandidates` 按版本组定目标组、`persistOneCandidate` 返回 Release ID | S（①④⑤） |
  | `catalog/ingest/persist.go` | `persistRelease` 新建时用 `JoinGroupID` | S ④（`EmbeddedArtwork`，不重叠） |
  | `catalog/read.go` | 种类白名单加 `format_split` | 无 |
  | `catalog/internal/artistgraph/facts.go` | 按裁决一处条件 | T（⑥⑦⑧） |
  | `frontend/src/features/catalog/labels.ts` | 一个种类名称 | 无 |

- [ ] 第 9 节裁决已记入 prd.md“已裁决”，`facts.go` 按答案改。
- [ ] 第 3 节用例通过，命令与结果写进回报。
- [ ] 版本号按 design.md 第 8 节：只升 `GroupingVersion`，与 ② 合并为加 1 次；④ 先合入则 O 线 rebase 后加 1，O 线先合入则不加。④ 对照表里“③ 升 `CommonVersion`”由 S 线改掉。
- [ ] 样本库 smoke 无回归；真实库 smoke 清单已给用户确认。
- [ ] 没有多余的测试、演练或兜底代码；前端没有多余说明文字。
