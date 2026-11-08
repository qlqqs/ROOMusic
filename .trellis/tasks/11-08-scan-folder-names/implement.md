# ② 文件夹名规则：实施计划

设计见 [design.md](./design.md)。O 线第 1 项，在 O 线 worktree 里实现；③ 在本项合入线分支后开始。

## 0. 开工前

- 八号机统计已执行（[research/m8-name-stats.md](./research/m8-name-stats.md)）：中文碟号与中文附属目录名都不纳入，规则已在 design.md 定稿。
- 派活 prompt 必须写进用户全局约定：
  - 测试只测改动直接影响的部分，跑最小相关测试集，不做演练、不重复验证、不过度兜底（不为假想场景堆防御代码和回退分支）。
  - 前端文案：不写防御性说明、不加不必要的说明（本项没有前端改动）。
- 子 agent 不提交 git。

## 1. 后端（按顺序）

1. **碟号解析器** `backend/internal/catalog/ingest/discmarker.go`（新）：`parseDiscMarker`、`stripAlbumDiscMarker`、`isBonusRemainder`，规则按 design.md 第 3.2 节；英文数词与规范罗马数字作为包内小表。
2. **碟目录识别**：`organizer.go` 的 `locateObservation` 改用 `parseDiscMarker`，`observationLocation` 带上 `marker`。`scope.go` 不改。
3. **专辑名剥离**：`albumPartition` 与 Release 标题决定的 getter 改用剥离结果（design.md 第 5 节）；`OrganizeObservations` 入口对物理观察补 `album_title` 碟号证据。
4. **碟父目录独占标记**：`observation.go` 增加 `DiscParentExclusive`（与 ④ 新增字段按字母序合并）；`staging.go` 新增 `MarkDiscParentScopes`（design.md 第 4.1 节 SQL）；`library/scanner.go` 在 `AlignCueStagingScopes` **之后**调用，错误处理与 CUE 对齐相同。
5. **归组算法**：替换 `organizer.go` 的 `strictParents` 段与碟号赋值段，按 design.md 第 4.2 节：不独占时保持现在的严格多碟与 `box_leaf`（单碟目录用 Prefix 兜底标题），独占时第 1–9 步；`OrganizedCandidate` 增加 `MediumTitles`。
6. **介质标题写入**：`persist.go` 新建介质带 `title`，已有介质按需更新；没有标题写 `'Medium'`。
7. **封面**：`library/scanner.go` 的 `discoverFolderArtworkWithReader` 改为四轮查找（design.md 第 6 节）；附属子目录名单放在 `ingest`（与 `ArtworkDirectories` 同处）或 scanner 的封面常量旁，二选一，跟随现有常量位置。
8. **收藏转移**：
   - 迁移 `backend/migrations/0034_scan_observation_previous_release.sql` 与 `.down.sql`；`internal/platform/postgres/migrations_rollback_integration_test.go` 的版本表补 34。**0034 是暂定号**：S 线 ①⑫④⑪ 的迁移也暂定从 0034 起，合入时由主会话按合入顺序重新编号（文件名、down 文件、回退测试版本表一起改）。
   - `staging.go`：`StageObservation` 冲突更新清空 `previous_release_id`。
   - `identity_plan.go`：`allocateReleases` 之后写 `previous_release_id`。
   - `catalog/ingest/group_moves.go`（新）：`EmptiedGroupMoves`。
   - `personal`：`MoveAlbumFavorites`（新文件 `favorite_moves.go`）。
   - `library`：`AlbumFavoriteMover`、`SetAlbumFavoriteMover`、`transferEmptiedAlbumFavorites`（在 `scanRoot` 的 `persistStagedCandidates` 之后调用；失败只记 Warn）。
   - `app/app.go`：取出 `personalService` 变量并注入。
9. **合同同步**：
   - `.trellis/spec/backend/library-scan-contracts.md`：碟目录识别与合成规则（含父目录独占条件）、专辑名碟号剥离与 `album_title` 来源、介质标题、封面四轮查找与附属子目录、碟父目录独占标记、收藏转移步骤与失败语义、迁移 0034；把第 3 节“parser 默认的 track/disc…Disc/CD/Disk 目录”一句与第 4 节“发行目录与曲目目录都有封面”一行同步。
   - `.trellis/spec/backend/personal-contracts.md`：新增“扫描触发的专辑收藏转移”（`MoveAlbumFavorites` 签名、锁序 CATP → 收藏版本行、保留时间与保存标题、已收藏不重复、revision 加一）。
   - `.trellis/spec/backend/database-guidelines.md`：迁移 0034 一条。

## 2. 前端

无。介质小标题已经按 `title` 显示“第 N 张 · 碟名”（`frontend/src/features/catalog/format.ts` `mediumHeading`）。验收时在样本或合成素材上看一眼专辑详情页的碟名即可，不加测试。

## 3. 测试（只补改动直接影响的）

单元（`internal/catalog/ingest`，无数据库）：

- `discmarker_test.go`：design.md 第 3.3 节两张表逐行。
- `organizer_test.go` 增补：design.md 第 10 节归组表逐行（表驱动，断言候选数、种类、锚点 scope、介质位置与标题、兜底标题；独占与不独占用观察上的 `DiscParentExclusive` 区分，含 `艺人/某精选 CD1/` 旁边有别的专辑的那几行）；碟号优先级四行。
- `ArtworkDirectories` 现有测试不需要改（返回值不变）。

单元（`internal/library`，无数据库）：

- `evidence_test.go` 增补：封面第 2–4 轮（front 包含规则、附属子目录、子目录是符号链接不查、前一轮命中不看后面）；现有用例保持通过即证明第 1 轮不变。

PostgreSQL 集成（隔离库，见第 4 节）：

- `internal/library`：一个合成素材扫描用例覆盖“两个 `box_leaf` 合成一张 → 新建 Release、旧组收藏转到新组、已收藏新组的用户不重复、收藏 revision 加一”与“组内还有别的版本有曲目时不转移”；再扫一次不再转移。另一个用例覆盖 `MarkDiscParentScopes`（`A/CD1` + `A/Scans` 归父；`艺人/某精选 CD1` 旁边有 `艺人/另一张` 时锚在碟目录、封面不取 `艺人/`；`A/CD1/附录` 含音频时不独占）与介质标题写入、去掉碟名后恢复 `Medium`。
- `internal/personal`：`MoveAlbumFavorites` 一个用例（转移、合并、保留 `created_at`/`saved_title`、只增受影响用户的 revision）。
- `internal/platform/postgres`：迁移版本表补 34 后跑 `-run 'Migration'` 中与回退相关的用例。

不新增：真实服务端到端用例、前端用例、性能测试。

## 4. 验证命令（最小相关集）

```bash
cd backend
gofmt -l ./internal/catalog/ingest ./internal/library ./internal/personal ./internal/app
go vet ./internal/catalog/ingest ./internal/library ./internal/personal ./internal/app
go test -count=1 ./internal/catalog/ingest -run 'DiscMarker|Organize|AlbumDisc|Artwork'
go test -count=1 ./internal/library -run 'FolderArtwork'
# 隔离 PostgreSQL（O 线自己的 compose 项目与端口，由主会话分配；不碰业务库与其他线）
ROOMUSIC_TEST_DATABASE_URL=<O 线隔离库 URL> go test -count=1 ./internal/library -run 'DiscParent|MediumTitle|FavoriteTransfer'
ROOMUSIC_TEST_DATABASE_URL=<O 线隔离库 URL> go test -count=1 ./internal/personal -run 'MoveAlbumFavorites'
ROOMUSIC_TEST_DATABASE_URL=<O 线隔离库 URL> go test -count=1 ./internal/platform/postgres -run 'Migration'
go test -count=1 ./internal/app -run 'TestPackageDependencyDirections'
```

不跑全量：改动集中在 ingest 归组、封面发现、个人收藏转移三处，上面的集合直接覆盖；全量留给合入 `main` 后主会话的集成复核。

样本库与真实库只读 smoke（验收用，由主会话安排一次）：

- 样本库 `music/`：前后对比发行数、曲目数、诊断类别；`【推しの子】` OST 的 `Disc 1`/`Disc 2` 仍是一张。样本库里带 `Scans`/`Artwork` 的专辑目录本身都有 `Cover.jpg`，“附属目录封面被用上”由单测与一份去掉 `Cover.jpg` 的临时拷贝验证，不改仓库样本。
- 真实库：`ROOMUSIC_REAL_LIBRARY_SMOKE=1 ./scripts/real-library-smoke.sh --music-root <只读挂载> --v0-archive <固定归档>`，导出前后合并/拆开的专辑清单、剥离过碟号的专辑标题清单、新绑定封面清单，只在 `research/smoke-result.md` 写脱敏聚合，清单逐条给用户确认。

## 5. 风险文件与回滚点

| 文件 | 风险 | 回滚点 |
| --- | --- | --- |
| `catalog/ingest/organizer.go` | 归组规则核心，影响全库分组 | 步骤 5 单独提交，可单独回退 |
| `catalog/ingest/discmarker.go` | 误识别专辑名 | 与步骤 2–3 同一提交 |
| `catalog/ingest/staging.go`、`observation.go`、`library/scanner.go`（标记一行） | 标记放在 CUE 对齐之前会看到未定的 scope | 步骤 4 单独提交 |
| `library/scanner.go`（封面） | 封面结果变化 | 步骤 7 单独提交 |
| `personal/favorite_moves.go`、`library` 转移调用、迁移 0034 | 写用户数据 | 步骤 8 单独提交；回退代码即停止转移，down 迁移删暂存列 |

建议提交顺序：1–3 → 4–6 → 7 → 8 → 9（合同可随各步提交）。

## 6. 合入前核对

- [ ] 第 3 节的单元与集成用例通过，命令与结果写进回报。
- [ ] `parser/` 没有改动（与 T 线无冲突）。本项改动的共享文件：

  | 文件 | 本项改动 | 同时在改的线 |
  | --- | --- | --- |
  | `library/scanner.go` | 独占标记调用、封面四轮查找、收藏转移调用 | S（①④⑤ 遍历回调） |
  | `catalog/ingest/observation.go` | `DiscParentExclusive` 一个字段 | S ④（两个字段，按字母序合并） |
  | `catalog/ingest/staging.go` | `MarkDiscParentScopes`、冲突更新清空 `previous_release_id` | S ④ |
  | `catalog/ingest/identity_plan.go` | 写 `previous_release_id` | S ④ |
  | `catalog/ingest/persist.go` | 介质标题、`ArtworkDirectories` 不变 | S ④（`EmbeddedArtwork`） |
  | `app/app.go` | 注入收藏转移 | S（⑪ 调度等） |
  | `backend/migrations/` | 暂定 0034 | S ①⑫④⑪（同样暂定 0034 起） |
  | `personal/` | 新文件 `favorite_moves.go` | 无 |

- [ ] 迁移已按合入顺序重新编号，回退测试版本表同步。
- [ ] 版本号按 design.md 第 8 节：② ③ 一起合入时 `GroupingVersion` 只加 1 次；④ 先合入则 O 线 rebase 后加 1，并让 ④ 签名的封面行覆盖附属子目录与 `front` 规则；O 线先合入则不加，由 S 线在 ④ 里覆盖封面范围。
- [ ] 样本库 smoke 无回归；真实库 smoke 清单已给用户确认。
- [ ] 没有多余的测试、演练或兜底代码。
