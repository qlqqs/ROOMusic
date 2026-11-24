# 设计：发行并入的审查证据补上合并后的碟序

## 现状

- 合并计划 `catalog/governance/release_merge.go` 的 `buildMergePlan` 已算出合并后每张碟的位置（`MergePreviewMedium.position_after`、`from_release`、`title`、`track_count`），
  但没有逐曲信息。
- 审查预览 `curation/stewardtools/release_merge.go` 的 `previewMerge` 只把来源、目标、令牌、隐藏、收藏数交给 `program.ReleaseMergeChanges`，`plan.Media` 被丢掉。
- 审查证据 `facts.release_merge`（`curation/release_merge_evidence.go` 的 `ReadReleaseMergeEvidence`，经 `app/steward.go` 的证据读取闭包）只有双方的碟数、曲目数、外部计数与录音数，
  逐曲数据（`catalog.CurationTrack`）已经读出，只是压成了计数。
- 审查摘要与证据快照绑定，执行前重算并比对（`runtime/run_review.go`），所以新证据必须是确定的。
- `TestPostgreSQLStewardReviewPolicyV4MergeEvidence` 在审查后删掉 MusicBrainz 缓存再重读证据，证据不能依赖缓存。

## 决定

### 1. 证据：`facts.release_merge.data.layout`

在 `facts.release_merge` 的 `data` 里增加 `layout`，按合并后的碟序排列，每张碟：

| 字段 | 含义 |
| --- | --- |
| `position` | 合并后的碟号（取自合并计划的 `position_after`） |
| `from` | `target` 或 `source`（已并入过的碟记 `target`，另给 `from_release_id`） |
| `from_release_id` | 这张碟原来所属的发行 |
| `origin_position` | 这张碟在原发行里的碟号 |
| `title` | 碟标题，可空 |
| `track_count` | 曲目数 |
| `tracks` | 每首：`position`（碟内曲序）、`title`、`external_id`（这首已生效的录音对应，没有时为空串） |

另加 `layout_track_total`（合并后的总曲目数）。合并后总曲目数超过 300 时，各碟省略 `tracks`，并给 `tracks_omitted: true`；碟一级的信息照常给出。

- 碟序来自 governance 的合并计划，与执行用的是同一份计算（直接调用现有的预览，或抽出不开事务的计划函数复用），不在 curation 里另写一遍编号规则。
- 逐曲数据只取本地：曲目的有效标题与已生效的录音对应。不读 MusicBrainz 的曲目列表（依赖缓存或网络，不确定）。
  外部计数仍来自对应操作的后态，审查用它与 `layout` 的碟数、曲目数对照。
- 排序确定：碟按 `position`，碟内按曲序，再按曲目 ID。

### 2. 审查预览

预览的 changes 不改。碟序只进证据，不进 `ReleaseMergeChanges`：changes 同时用作例外的过期比对和前端展示，加进去会改变例外的过期判定与界面解码，收益只在审查一侧。

### 3. 合同

- `runtime.ReviewEvidenceFields` 中 `facts.release_merge` 一行补上 `layout` 的字段说明，与 `.trellis/spec/backend/steward-review-contracts.md` 的“证据摘要字段全文”逐字同步。
- 策略规则不变，`ReviewPolicyVersion` 保持 4：批准已绑定证据摘要，证据内容变化会让旧批准失效，不需要升版本。
- `.trellis/spec/backend/curation-contracts.md` 第 20 节（审查证据的说明）补一句 `layout`；`release-merge-contracts.md` 不变。

### 4. 大小

单个并入最多 256 张碟、10,000 首（`checkMergeLimits`）。300 首以内逐曲给出，单首约百字节，最多约 30 KB；超过时只给碟一级，最多 256 行。

## 不做

- 不把 MusicBrainz 的逐曲列表放进证据。
- 不改并入的提出规则、执行与撤销。
- 不改例外界面。
