# H 实施计划

分支 `feat/auto-curation-h`。后端实现与测试分别交给不同的 Codex 子代理（Orca 终端），前端由主代理完成。子代理不提交 Git。

## 派发 1（实现，gpt-6.2-astra）：数据、扫描、执行器、撤销、接口

允许修改：
- `backend/migrations/0067_*`
- `backend/internal/catalog/ingest/`
- `backend/internal/catalog/governance/`（新文件 `release_merge*.go`；`claims.go`；撤销分派、Describe、操作目标的注册处；`stewardtools/` 加 `preview_release_merge`、`merge_releases`）
- `backend/internal/catalog/`（发行详情的 `merged_releases`）、`backend/internal/catalog/httpapi/`
- `backend/internal/operations/`（操作类型常量）
- `backend/internal/app/`（撤销类型注册、路由、MCP 工具、接口样本登记）
- `backend/internal/personal/`（收藏按原值恢复所需的函数）
- `backend/internal/curation/suppressions.go`（只新增 `SuppressReleaseMerge`）

步骤：
1. 迁移 0067 与 down（D2）。
2. 扫描归属（D4），`layoutMergedMedia`（D3）。
3. `ReleaseMergeExecutor`、`buildMergePlan`、预览令牌、锁序（D5、D6），待认领排除。
4. 撤销（人工与自动两种规则）与不再提出标记（D7）。
5. REST 接口、发行详情字段、对话与 MCP 工具（D9、D8 人工部分）。

验证（只跑涉及的包）：`go vet ./...`、`go test ./internal/catalog/... ./internal/operations/... ./internal/personal/...`；集成按名字收窄（受影响的 `ReleaseMembership`、`ScanIncremental`、`Claim`、`Governance`、`Migration` 用例）。

## 测试 1（gpt-6.2-astra）

只写测试，做变异检查，填 `release-merge-contracts.md` 的必需测试表。

| 用例 | 断言 |
| --- | --- |
| `TestPostgreSQLReleaseMergeManual` | A1：详情、碟序、B 不可见（详情 404、列表、搜索、艺人页、待认领）、字段不变、曲目位置；认领已并入的 B 返回 `409 not_claimable` |
| `TestPostgreSQLReleaseMergeRescanKeeps` | A2：全量、增量（两边可跳过）、B 加曲目、一次扫描给 B 加两碟、删曲目、目录改名；ID 不变、无新发行 |
| `TestPostgreSQLReleaseMergeTargetGainsDisc` | A3：目标新增冲突碟号后重排，无唯一约束冲突 |
| `TestPostgreSQLLayoutMergedMedia` | D3：两个多碟来源依次并入、原碟号保留与接续 |
| `TestPostgreSQLReleaseMergeGroupMoves` | M7：B 独占一组时重扫不转收藏；C 被腾空时收藏转到 A 的组 |
| `TestPostgreSQLReleaseMergeRevert` | A4：碟、碟号、收藏（时间与快照）、可见性、B 的修正与核对、默认版本恢复；合并后做的曲目修正留在曲目上；写入 `release_merge` 标记；B 的修改记录显示合并与撤销；B 的版本被改后 `revert_conflict` |
| `TestPostgreSQLReleaseMergeFilesMovedTogether` | S5：B 的文件全部搬进 A 的目录后重扫与撤销的结果符合合同 |
| `TestPostgreSQLReleaseMergeGuards` | A5：各错误码按顺序返回且数据不变；三个上限；扫描改变后 `merge_changed` |
| `TestPostgreSQLReleaseMergeArtistOverrides` | S4：B 的发行级艺人覆盖合并期间不生效、撤销后恢复 |
| `TestPostgreSQLReleaseMergeConcurrent` | 合并、撤销与扫描写入、认领、归组、收藏与取消收藏专辑交错不死锁 |
| `TestPostgreSQLMigrationRollbackRoundTrip`（扩展） | 0067 的数据准备（已并入的碟、合并操作、转移过的收藏）；down 还原碟 |
| 接口样本 | `releases/merge-preview`、`releases/merged`、`operations/release-merge-reverted`、`errors/{release-merged,merge-chain,merge-changed,release-too-large}`；无合并时既有样本不变（A8） |
| 工具数 | C6、MCP 的治理工具数加二 |

## 派发 2（实现，gpt-6.2-astra）：自动并入

允许修改：`backend/migrations/0067_*`（补约束）、`backend/internal/curation/`（program、model、stewardtools、writer、suppressions）、`backend/internal/steward/runtime/review.go` 及审查证据、`backend/internal/app/curation_model.go`、`backend/internal/catalog/governance/release_merge*.go`（只为暴露自动写入所需的入口）。

1. 发现、确定目标、待定项与摘要、存量回填（D8）。
2. `Writer.MergeReleases` 与强证据核对、`manual_structure`、标记。
3. 后台工具 `merge_releases`、白名单、审查策略第 4 版与规范同步、调度器例外匹配、例外的采纳与不采纳。

## 测试 2（gpt-6.2-sol）

| 用例 | 断言 |
| --- | --- |
| `TestPostgreSQLCurationReleaseMergeProposed` | 同一 MB 发行的两部分只在目标上生成一条待定项，目标按规则选出；程序与模型匹配两条路径都会发现；回填只做一次；三个发行对上同一外部发行时依次排队 |
| `TestPostgreSQLCurationModelMergeReleases` | 模型提出、审查通过、写入 `release_auto_merge`、模型批次；待定项 `model_merged`；人工合并结束涉及的待定项 |
| `TestPostgreSQLCurationMergeBlocked` | 录音重叠、缺对应、佐证缺失、标记、人工分组隔开、链式分别返回约定结果 |
| `TestPostgreSQLCurationMergeRevert` | 按批与按来源撤销写标记；之后有人工结构操作时自动撤销列为 skipped |
| `TestPostgreSQLCurationExceptionMergeAcceptDecline` | 例外采纳、前态变化 `exception_stale`、不采纳写标记 |
| `TestPostgreSQLStewardReviewPolicyV4MergeEvidence` | 审查证据内容与版本号、不读外部缓存；旧版批准被拒 |
| 工具数 | 后台工具数加一 |

## 前端（主代理）

D10：管理面板入口与合并对话框（预览、错误）、已并入列表与撤销、操作记录与撤销对话框、例外与不再提出的显示、解码器与契约测试（样本）。相关 Vitest 文件、`typecheck`、`eslint`；Playwright 跑专辑页相关 spec。

## 收尾

规范（D11）、ADR 0006 “H 说明”、路线图第 12 行、父任务 `implement.md` 交接说明。全量后端单元；全量串行集成一次（扫描身份改动无法局部覆盖，理由写入验证记录）；全量 Vitest。合并到 `main`，归档任务，删分支。

## 回退点

扫描改动（D4）出问题时整体回退派发 1 的 `ingest/` 改动与迁移；无合并的库行为应与改动前一致（A8 是回归闸门）。

## 验证记录（2026-11-23）

本轮实际运行：
- 后端：`go vet ./...`、`go test ./...` 通过（首次发现 `TestPackageDependencyDirections` 拦下三处越层引用，修正后 `internal/app`、`internal/curation/...`、`internal/catalog/governance/...` 重跑通过）。
- 全量串行集成（扫描身份规则改动无法局部覆盖，所以扩大到全量）：84 个包 ok，顶层 987 个、子用例 1715 个通过，5 个跳过，0 失败。
- 测试代理 1：17 个新增用例与 0067 迁移往返通过；7 项变异全部命中（身份分配、增量跳过改回 `m.release_id`，排位改按原碟号，执行器锁组行出现 40P01 死锁，撤销不核对来源版本，自动撤销不查之后的人工结构操作，待认领谓词去掉排除）。
- 测试代理 2：自动并入的发现、写入、拦截、撤销、例外、审查第 4 版用例通过；6 项变异全部命中；新增样本 `curation/exceptions-merge`、`curation/suppressions-release-merge`。
- 前端：`typecheck`、`eslint src e2e` 通过；全量 Vitest 4618 通过，2 个失败是已知的负载敏感文件（agent-settings、steward），单独重跑 111 个全部通过；Playwright（albums、governance、default-version、curation、claims、a11y）209 通过，1 个因菜单新增一项而更新断言后通过。

未验证：真实 MusicBrainz 数据下的自动并入（留给父任务 V12）；arm64。
