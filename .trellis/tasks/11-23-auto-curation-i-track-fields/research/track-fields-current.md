# Research: 子任务 I 曲目级字段修正——现状与可沿用的路径

- **Query**：曲目标题、曲目艺人的人工修正层 + 自动层 + 有效值；撤销；读取处改读有效值；编辑入口；管家写工具与审查证据；接入 C、D。
  调查发行级现有分层的代码路径、所有读曲目标题与艺人的地方、署名事实、程序层与模型层接入点、前端、接口样本与风险。
- **Scope**：internal（只读代码与文档，未运行测试）
- **Date**：2026-11-23
- 约定：先列事实（带 `文件:行`），推断单独标注“推断”。路径相对仓库根目录；后端路径省略前缀 `backend/internal/`。

## 0. 依据文档

| 文件 | 相关内容 |
| --- | --- |
| `docs/architecture/adr/0006-auto-curation-and-sources.md:32-61` | 三层：人工修正 → 自动层 → 扫描值；表格第 55 行“曲目标题、曲目艺人（子任务 I）：同上，按曲目；同时新建曲目的人工修正层” |
| ADR `:221-227` | “I 照发行级的做法给曲目建修正与有效值，一开始就是三层。曲目艺人的修正要同步生成署名事实，与现有专辑艺人修正生成署名事实的做法一致。所有读曲目标题与艺人的地方（列表、搜索、播放队列、歌词匹配、管家工具）改读有效值” |
| `.trellis/tasks/11-22-auto-curation-sources/design.md:26`（U9） | 曲目标题与曲目艺人可以修正（人工与自动两层）；曲序与碟号不动 |
| design.md `:112` | 曲目标题、曲目艺人（U9）：程序只补空缺，改已有值由模型判断；曲序与碟号不可修正 |
| design.md §15 `:374-391` | 验收：“曲目标题与曲目艺人：人工可改、自动可写、人工优先、可撤销；流媒体下载的单曲能靠 ISRC 或声学指纹匹配并补全” |
| `.trellis/tasks/11-22-auto-curation-sources/implement.md:149-153` | I 的范围；验证：“没有修正时现有接口样本逐项不变；署名事实随曲目艺人修正更新；搜索、队列、歌词匹配读到有效值” |
| implement.md `:101` | C 交付：“曲目到录音的对应已建（配对可靠时），曲目标题与艺人的补全在 I 接入” |
| implement.md `:122` | B3 交付：“单曲方式经 AcoustID 对上的录音已有正式的 MusicBrainz 数据，曲目标题与艺人的补全可直接用” |
| implement.md `:140` | D 交付：后台白名单在 `app/curation_model.go`；字段类按 `Source=="curation"` 改道写自动层（照 `correct_release` 的 `AutoCorrectRelease`）；例外表 `tool` 约束只允许 `resolve_release_match`、`correct_release`，新工具要随迁移放开 |
| `.trellis/spec/backend/curation-contracts.md` §2-§8、§15、§17 | 发行级自动层合同（详见下文逐条引用） |
| `.trellis/spec/backend/governance-contracts.md` §5-§7 | 有效值投影、撤销规则、扫描不碰修正表 |
| `.trellis/spec/backend/artist-contracts.md:13,22,31` | 事实分层：修正→自动→扫描三选一（当前只用于专辑艺人） |
| `.trellis/spec/backend/library-scan-pipeline.md:70-81` | 曲目身份规则（UUID 随内容延续） |

---

## 1. 发行级字段的分层：表、视图、执行器、撤销（曲目版可照搬的路径）

### 1.1 表与迁移

| 迁移 | 内容 |
| --- | --- |
| `backend/migrations/0017_governed_operations.sql:7-32` | `operations` 表；`operations_type_ck`、`operations_target_type_ck`（当时只 `release`）、`operations_revision_ck`（`result_revision = expected_revision + 1`） |
| 0017 `:39` | `releases.edit_revision BIGINT NOT NULL DEFAULT 1`：人工修改版本号，只有受控操作递增，扫描不改 |
| 0017 `:44-61` | `release_corrections(release_id, field, mode, text_value, year_value, operation_id DEFERRABLE)`，主键 `(release_id, field)`；字段 CHECK 八个；`mode IN ('set','null')`；标题与专辑艺人不能 `null`；值约束（文本 `BTRIM<>''` 且 ≤4096 字节） |
| 0017 `:67-96` | 视图 `release_effective`：每字段一次主键 LEFT JOIN；注释说明“不被引用的列对应的连接会被优化器去掉” |
| `0032_release_genres.sql:2,33` | 重建视图（流派改为列表 `list_value`） |
| `0058_auto_curation.sql:1-21` | `release_auto_values`：形状逐条照搬 `release_corrections`（含 `list_value`），主键 `(release_id, field)`，`operation_id` 延迟外键 |
| 0058 `:23` | `releases.auto_revision BIGINT NOT NULL DEFAULT 1`（只满足 `operations_revision_ck` 与排序，不参与判断，spec §2） |
| 0058 `:25-68` | `release_effective` 再连八次 `release_auto_values`；文本字段 `COALESCE(c.text_value, a.text_value, r.x)`，可设空字段用 `CASE WHEN c.release_id IS NOT NULL ... WHEN a.release_id IS NOT NULL ... ELSE r.x` |
| 0058 `:70-73` | `artist_credits.layer` 增加 `auto`；`artist_names.origin` 增加 `auto` |
| 0058 `:75-78`，`0061:25-28`，`0062:39-42` | `operations_type_ck` 逐次重建；当前（0062）取值：`release_correction, release_auto_correction, release_external_artwork, track_external_lyrics, external_link, release_group_change, release_group_default, release_issue_review, release_claim, revert, artist_merge, artist_split, artist_rename, artist_candidate_ignore` |
| 0058 `:80-110` | `curation_batches`、`operations.batch_id`、两个部分索引、`curation_suppressions`（唯一 `(target_type, target_id, subject, value_hash)`，`target_type` 无 CHECK）、`curation_settings` |
| `0026_artist_catalog.sql:117-118` | 当前 `operations_target_type_ck CHECK(target_type IN ('release','release_group','artist'))`——**没有 `track`** |
| `0061_external_artwork_lyrics.sql:15-23` | `track_external_lyrics`（主键 `track_id`）：曲目级自动数据的先例 |

### 1.2 读取侧：有效值从哪里来

- `catalog/internal/releasesql/release.go:4-7`：`ReleaseSource = "release_effective releases"`，所有 catalog 读取以别名 `releases` 引用视图；扫描与身份规划仍读写 `releases` 本表（governance-contracts §5）。
- `catalog/fields.go:26-35`：`CorrectableFields`（顺序即接口顺序与 `correctionValueProjection` 列顺序）；`CorrectableField{Name, Label, IsYear, Required}`。
- `catalog/fields.go:47-74`：`CorrectionIntent{Mode, Value}`、`LoadCorrectionIntents`（读 `release_corrections`）。
- `catalog/fields.go:82-124`：`loadReleaseFieldNames` 用一条 `UNION ALL` 批量读两层字段名 → `corrected_fields` / `auto_fields`（人工优先）。
- `catalog/fields.go:145-183`：`AutoValue{Mode, Value, OperationID, ProviderKind, Provider}`、`LoadAutoValues`（JOIN `operations` 取 `origin->>'provider_kind'`、`provider`）。
- `catalog/read.go:135-165`：`LoadReleaseHeader` 返回 `CorrectedFields`、`AutoFields`（详情接口的 `corrected_fields`、`auto_fields`，后者为空时省略，spec §7 `:149-152`）。
- `catalog/governance/corrections.go:239-274`：`correctionValueProjection` + `loadFieldValues(source)`，source 是 `releases`（扫描值）或 `releasesql.ReleaseSource`（有效值）。
- `corrections.go:207-222`：`CorrectionField{Field, ScanValue, Correction, Auto, EffectiveValue, Uncertain}`；`ValueAfterClear()` 有自动值取自动值，否则扫描值。
- `corrections.go:278-322`：`loadCorrectionState`（先读 `edit_revision`，再读扫描值、有效值、修正、自动值、不确定标记）。

### 1.3 人工修正的写入：`CorrectionExecutor`

- `corrections.go:61-103` `ParseCorrectionChanges`：`set|null|clear`；文本经 `metadata.NormalizeValue`（`:130-139`），不含控制字符、≤4096 字节；`Required` 字段不能 `null`；错误码 `invalid_correction`。
- `corrections.go:184-203` `CorrectionRequest.Fingerprint()`：含类型、小写 ID、预期版本、每字段完整语义。
- `corrections.go:369-381` `lockVisibleRelease`：先 `writegate.Lock`（目录写锁，CATP），再 `SELECT ... FROM releases ... FOR UPDATE`，要求可见。
- `corrections.go:391-460` `CorrectionExecutor`：核对 `edit_revision == ExpectedRevision`（否则 `409 revision_conflict`）→ 逐字段 `nextIntent`/`sameIntent` 过滤（全无变化 `400 no_changes`）→ `writeIntent` → `UPDATE releases SET edit_revision = edit_revision + 1` → `refreshGovernedRelease` → 读新状态 → `Outcome{TargetType: release, ExpectedRevision, ResultRevision, Before/After: correctionRecordState{Fields, Effective}}`。
- `corrections.go:499-522` `writeFieldIntent(tx, table, releaseID, field, next, operationID)`：表名参数化，两层共用（`release_corrections` / `release_auto_values`）；`next == nil` 删除行，否则 `INSERT ... ON CONFLICT (release_id, field) DO UPDATE`。
- `catalog/governance/artist_state.go:226-231` `refreshGovernedRelease`：`artistgraph.Refresh(tx, [id])` + `searchindex.RefreshRelease(tx, id)`——**只刷新发行的搜索值，不刷新曲目的搜索值**。
- HTTP：`catalog/httpapi/handlers.go:68-69` `GET/PATCH /api/v1/releases/{id}/corrections`；`catalog/httpapi/corrections.go:35-55`（读取，管理员）、`:57-` `correctRelease`（同源、管理员、`Idempotency-Key`、`expected_revision` + `changes`）。

### 1.4 人工修正的撤销与展示：`correctionKind`

- `catalog/governance/revert.go:26` `CorrectionOperations()`；`:65-88` `restoredIntent`（年份 float64→int，旧流派字符串→列表）。
- `revert.go:91-154` `Revert`：锁发行、要求 `edit_revision == original.ResultRevision`（否则 `409 revert_conflict`）、按前态意图写回、`edit_revision + 1`、刷新派生。
- `revert.go:187-212` `Describe`：`changes = {kind:"release_correction", fields:[{field, before:{correction, effective}, after:{...}}]}`；`revert.go:352-359` `describe`：`Revertible = 可见 ∧ revision == ResultRevision`。
- `revert.go:327-349` `readReleaseInfos`：从视图读有效标题，供操作记录显示目标。

### 1.5 自动层的写入：`governance.LockAutoRelease` / `NormalizeAutoFields` / `curation.Writer.Apply`

- `catalog/governance/auto_values.go:19-44` `NormalizeAutoFields`：拒绝 `clear`，复用 `ParseCorrectionChanges` 与 `nextIntent`。
- `auto_values.go:48-55` `AutoValueKey`：意图 JSON + SHA-256（`unchanged` 比较与不再提出 `value_hash` 共用）。
- `auto_values.go:57-64` `LockedAutoRelease{tx, id, revision(auto), editRevision, current map[field]AutoValue}`。
- `auto_values.go:67-99` `LockAutoRelease` → `writegate.Lock` → `SELECT auto_revision, edit_revision FROM releases ... FOR UPDATE` → `LoadAutoValues`。
- `auto_values.go:101-115` **`LockAutoReleaseOfTrack(tx, trackID)`**：已有的曲目入口——按 `tracks → media.release_id` 找到发行（要求曲目 `present`），再锁发行行（B2 歌词用）。
- `auto_values.go:117-119` `LockAutoReleaseForRevert`（不要求可见）；`:124-128` `Bump`（`auto_revision + 1`，外部封面/歌词/对应用）。
- `auto_values.go:136-148`：记录形状 `autoRecordState{Fields map[field]{Auto *ownedAutoIntent{mode,value,operation_id}, Effective}}`。
- `auto_values.go:151-206` `Write` / `replace`：写前读有效值、`writeFieldIntent("release_auto_values", ...)`、`auto_revision + 1`、`refreshGovernedRelease`、写后读有效值；`Outcome.TargetType = release`，版本号用 `auto_revision`。
- `curation/writer.go:24-34` `Request{ExpectedRevision, ConfirmedBy, ConversationID, OnlyEmpty, ReleaseID, Fields, Source, BatchID, Key, RequestID}`。
- `curation/writer.go:44-122` `Apply`：校验 → `NormalizeAutoFields` → `writerActor` → 指纹（类型、发行、字段、provider，可含 `expected_revision`、`only_empty`）→ `operations.Run`：`LockAutoRelease` → `checkWrite`（开关/开启者/批次）→ 模型来源且带 `ExpectedRevision` 时有人工修正即 `409 manual_override`（`:84-94`）→ `filterFields` → `locked.Write`；`nothing_to_apply` 时仍返回 `Applied.Skipped`。
- `curation/writer.go:124-175` `filterFields`：`OnlyEmpty` 时有人工或自动行记 `occupied`；命中 `suppressionExists(tx, "release", releaseID, field, hash)` 记 `suppressed`；与当前自动值同哈希记 `unchanged`。
- 原样可复用的曲目级先例：`curation/external_lyrics.go:38-98` `ApplyLyrics` 用 `LockAutoReleaseOfTrack`、目标仍是**发行**（`curation/external_artwork.go:80-86` `externalOutcome` → `TargetRelease` + `Bump`），不再提出标记用 `target_type='track'`（`external_lyrics.go:54`）。

### 1.6 自动层的撤销、展示、预览：`autoCorrectionKind`

- `catalog/governance/auto_revert.go:15-28` `AutoSuppressionWriter.SuppressReleaseValue(...)` 接口 + `AutoCorrectionOperations(writer)`（缺写入器装配时 panic）。
- `auto_revert.go:32-54` `readAutoState`（只接受 `CorrectableFields` 里的字段名）。
- `auto_revert.go:56-92` `Revert`：`lockAutoRelease(requireVisible=false)`；逐格核对 `current.OperationID == original.ID`，否则 `409 revert_conflict`（不做部分撤销）；`replace(next=前态含原写入者)`；为每格写不再提出；`ResponseRevision = editRevision`。
- `auto_revert.go:94-151` `Describe`：批量读 `release_auto_values` 的写入者，`changes = {kind:"release_auto_correction", fields:[{field, before, after}]}`，`Revertible` 要求每格仍是这条操作写的。
- `auto_revert.go:153-185` `PreviewRevert`：撤销后显示 = 人工修正 → 前态自动值 → 扫描值。
- `curation/suppressions.go:29-38` `SuppressReleaseValue`：`INSERT ... VALUES('release', ...) ON CONFLICT DO NOTHING`（`target_type` 写死 `release`）。
- `curation/suppressions.go:40-55` `Suppressions(releaseID)`：已同时列出 `target_type='track'` 且属于该发行曲目的标记（C 加的）。
- 按批/按来源撤销：`curation/revert.go:37-105`（候选不按操作类型取，spec §5.2：“以后的结构类自动操作不用改这里”）。

### 1.7 操作类型常量

- `operations/operations.go:37-55`：`TypeReleaseAutoCorrection`、`TypeReleaseCorrection`、`TypeTrackExternalLyrics` 等；目标常量只有 `TargetRelease`、`TargetReleaseGroup`、`TargetArtist`。
- `operations/operations.go:161-172` `Outcome`（`Targets` 只把 `artist` 类型写进 `operations_artist_targets`，`:338-362`）。
- 前端操作类型与 `changes` 解码：`frontend/src/features/governance/api/decoders.ts:204,235,353`；文案 `frontend/src/features/governance/change-summary.ts:29-64,188,231`；撤销确认文案 `frontend/src/features/governance/components/revert-operation-dialog.tsx:49`。

---

## 2. 所有读曲目标题与曲目艺人的地方

`tracks.title`、`tracks.artist` 是扫描列（`backend/migrations/0002_core_slice.sql:49-61`，均 `NOT NULL`）。没有曲目的有效值视图或修正表（`grep track_corrections|track_effective` 无结果）。

### 2.1 写入方（不是读取，但决定“扫描值”是什么）

| 位置 | 说明 |
| --- | --- |
| `catalog/ingest/persist_track.go:73-82` | 新建 `INSERT` / 沿用 `UPDATE tracks SET ... title=$3, artist=$4 ...`；`artist = metadata.CanonicalTrackArtist(observation.Artist)` |
| `catalog/internal/artistgraph/facts.go:148-185` | **artistgraph 也会改写 `tracks.artist`**：按原始标签证据重算展示串，`display != t.artist` 时 `UPDATE tracks t SET artist=v.artist`，随后 `searchindex.RefreshTracks(changedIDs)` |

### 2.2 读取方清单

| # | 位置 | 读法 | 对外表现 |
| --- | --- | --- | --- |
| 1 | `catalog/read.go:187-199` `LoadReleaseMedia` | `t.title, t.artist` 直读 + `releasesql.TrackArtistRefsProjection` | 发行详情 `media[].tracks[]`（`catalog/httpapi/handlers.go:184`）；管家 `get_release`、`get_track`（`catalog/stewardtools/details.go:42,97`）；分区管家工具 `zones/stewardtools/tools.go:164` |
| 2 | `catalog/track_summary.go:40-57` `loadTrackSummariesSQL` | `selected_tracks` CTE 直读 `t.title, t.artist`；`SelectedTrackArtistRefsCTE` 取 refs | `TrackSummary`。调用方：`POST /api/v1/tracks/summaries`（`catalog/httpapi/track_summaries.go:28`）、搜索曲目结果（`catalog/search_complete.go:18`）、艺人曲目（`catalog/artists_collections.go:178`）、播放历史（`listening/history.go:85`）、收藏与歌单（`personal/projection.go:70`）、**播放分区队列**（`zones/service.go:214-233` → `queueProjection`）、管家 `get_track`（`stewardtools/details.go:87`）、分区工具输出（`zones/stewardtools/output.go:22-23`） |
| 3 | `catalog/track_placement.go:52-54` `LoadTrackPlacements` | `tracks.title` 直读 | 最近播放（`listening/recent.go:55`）、渲染（`app/renderer.go:89`，只用发行 ID） |
| 4 | `catalog/internal/searchindex/write.go:137-149` `loadValues`（单条，`RefreshTrack`） | `SELECT title, artist ... FROM tracks` → `track_title`、`track_artist` 搜索值；`search_title`（排序键）由 `normalizeValues` 产生（`write.go:75,99`） | 规范化搜索 |
| 5 | `catalog/internal/searchindex/batch.go:143-160` `loadTrackValues`（批量，`RefreshTracks` 与维护回填） | 同上 | 规范化搜索、全量重建 |
| 6 | `catalog/search_sql.go:41-55` 旧式（非 normalized）命中 | `('track_title', t.title)`，曲目搜索加 `('track_artist', t.artist)`，`FROM tracks t ... ILIKE` | 搜索投影未就绪时的回退 |
| 7 | `catalog/search_sql.go:179` | 旧式排序 `LOWER(t.title), LOWER(t.artist), t.id` | 同上 |
| 8 | `catalog/read.go:29-40` `releaseSearchPredicate` | `search_track.title ILIKE` | `/releases`、`/release-groups` 的旧式 `q`（`listReleasesLegacy` `:91-127`） |
| 9 | `catalog/curation_evidence.go:224-243` `curationTrackSQL` | `t.title, t.artist` 直读；占位判断 `track_observations` 的 `default_fallback`（`:225-227`） | 程序层匹配与歌词证据 `CurationTrack`（`:46-61`） |
| 10 | `curation/program/enrich_media.go:104,142,188,198` | `CurationTrack.Title/Artist` → `sources.LyricsQuery`、接受规则 | **歌词匹配**（LRCLIB） |
| 11 | `curation/program/digest.go:46-57` `LyricsDigest`；`:26` `ReleaseDigest`（整个 `CurationEvidence` 的哈希） | 摘要含曲目标题与艺人 | 变化即重做 |
| 12 | `curation/program/match_single.go:44-48`、`match_album.go`（`title_artist_mismatch` 等） | `t.Title` 与外部录音标题比较 | 匹配判定 |
| 13 | `curation/external_lyrics.go:67-68` | `SELECT t.title ...`，写进操作记录快照 `track_title` | 整理记录展示 |
| 14 | `catalog/internal/artistgraph/facts.go:106-168` | `t.artist` + `track_observations('artist')` + `artist_tag_evidence('track_artist')` → `track_artist` 事实（layer 恒为 `scan`） | 署名事实、艺人页、曲目 `artist_refs` |
| 15 | `catalog/maintenance/artist_batch.go:261-279` | `t.artist` 计入艺人回填的输入成本估计 | 后台回填预算 |
| 16 | `catalog/ingest/scan_feedback_snapshot.go:66-69` | `md5(t.title, t.artist, ...)` | 扫描反馈“元数据变了”的判断（扫描前后对比，属扫描值语义） |
| 17 | `app/steward.go:315-325` `reviewExternalRelease` | 只取曲目 ID | 审查外部证据（不含标题） |

读取不经 catalog 的地方：`playback`、`personal`、`listening`、`zones` 都没有直接 SQL 读 `tracks.title/artist`（`grep` 只命中上表的调用）。导出/备份：`backup/service.go:262` 是整库 `pg_dump`，没有表清单。MCP 复用同一工具注册表（管家工具）。

### 2.3 曲目艺人的 `artist_refs`（不是读 `tracks.artist`，读有效署名）

- `catalog/internal/releasesql/artist_refs.go:42-50` `TrackArtistRefsProjection`：`artist_effective_credits e WHERE e.track_id=t.id AND e.role='track_artist'`。
- `track_summary.go:48` 用 `releasesql.SelectedTrackArtistRefsCTE`（同一来源）。
- 艺人曲目列表、艺人页“署名”：`catalog/artists_collections.go:130-131`、`catalog/artists_summary_sql.go:15-26` 都读 `artist_effective_credits`。
- 艺人候选“专辑与曲目一对一”：`catalog/internal/artistgraph/candidates.go:18-34` 读 `artist_effective_credits` 的 `track_artist`。

### 2.4 搜索索引对曲目艺人的依赖

- `searchindex/values.go:14`：`track_artist` 与 `release_artist`、`album_artist` 一样按保守拆分多名、分别带 position（search-contracts `:136`）。
- `catalog_search_values_field_ck`（`0025_catalog_search.sql:77-79`）：曲目字段只允许 `track_title, track_artist, credit`（之后加了 `work_title|work_catalog`，search-contracts `:51`）。
- 曲目搜索值的刷新入口：`searchindex.RefreshTrack`（`write.go:16-18`）、`RefreshTracks`（`batch.go:17`）；调用方只有扫描 `ingest/persist.go:125` 与 artistgraph `facts.go:185`。**治理写入后的 `refreshGovernedRelease` 不调用它们**（`artist_state.go:226-231`）。
- 艺人别名搜索经 `artist_search_names` 与有效署名联结传播（`artist_state.go:239` 注释），不经 `catalog_search_values`。

---

## 3. 曲目艺人与艺人实体、署名事实

### 3.1 表

- `artist_credits`（`0026_artist_catalog.sql:22-30`）：`release_id | track_id` 二选一；曲目角色 `track_artist, lyricist, composer, arranger, conductor, performer, producer`（`0038_credit_roles.sql:17-20`）；`layer IN ('scan','correction','auto')`（0058）；唯一 `(release_id, track_id, role, name_id, layer)`。
- `artist_effective_credits`（0026 `:58-68`）：解析后的身份关联，读取都走它。
- `track_credits`（`0012:36-43`，0038 扩角色、加 `detail`、`source IN ('tag','lyrics')`）：**不含 `track_artist`**；曲目艺人只在 `tracks.artist` + `artist_tag_evidence(role='track_artist')` + `track_observations(field_name='artist')`。
- `artist_tag_evidence`（0026 `:88-94`）：原始标签多值证据。
- `artist_assignment_overrides`（0026 `:39-48`）：可按 `track_id + key_id` 做作用域指派（艺人拆分用）。

### 3.2 事实生成（`artistgraph.refreshFacts`，`facts.go:25-221`）

- 发行级（`:58-104`）：读 `releases` + `release_corrections('album_artist')` + `release_auto_values('album_artist')`；扫描层 `release_artist`、`album_artist` 各建 `scan` 事实；修正值与自动值各自为 `release_artist`、`album_artist` 建 `correction`、`auto` 层事实（占位名跳过）。
- 曲目级（`:106-168`）：只建 `scan` 层的 `track_artist` 与 `track_credits` 各角色；`inferred`（推断）且非 `filename_fallback` 的不建。
- 落库（`:207-219`）：本批发行范围内**不在本次事实集合里的 `artist_credits` 行全部删除**，再 upsert。所以修正/自动层事实必须每次由 `refreshFacts` 从修正表重新生成，否则重扫会删掉它们。
- 名字来源：`resolver.ensure(name, layer)`（`:197`），`artist_names.origin` 取层名。

### 3.3 有效署名的层选择（`artistgraph/refresh.go:67-95`）

- 发行级（`:78-82`）：`c.layer = CASE WHEN 有 release_corrections('album_artist') THEN 'correction' WHEN 有 release_auto_values('album_artist') THEN 'auto' ELSE 'scan' END`。
- 曲目级（`:84-85`）：`FROM media m JOIN tracks t ... JOIN artist_credits c ON c.track_id=t.id`——**不按层过滤**（因为目前曲目只有 scan 层）。
- 解析：`track+key 指派 → release+key 指派 → 全局 binding → initial_artist`，再解 redirect（`:87-93`，artist-contracts `:22`）。
- `Refresh`（`:11-48`）之后刷新 profiles、candidates、`artist_projection_releases`。

### 3.4 “署名事实随曲目艺人修正更新”需要的东西（事实 + 推断）

事实：发行级专辑艺人的做法 = 修正表/自动表里的值 → `refreshFacts` 生成 `correction`/`auto` 层事实 → `refreshEffective` 按“修正→自动→扫描”三选一 → profiles/candidates/搜索名字随之更新；`LoadReleaseCredits`（`catalog/read.go:339-350`）把专辑级 `album_artist` 署名替换为有效值。

推断（曲目版需要）：
1. `refreshFacts` 的曲目查询（`facts.go:106-110`）连接曲目修正表与自动表，为曲目艺人修正值、自动值各建 `track_artist` 的 `correction`/`auto` 层事实；其余角色（作曲等）仍只有 scan。
2. `refreshEffective` 的曲目分支（`refresh.go:84-85`）对 `role='track_artist'` 按曲目三选一层；其它曲目角色保持 scan。
3. 曲目艺人修正后 `tracks.artist` 的显示串不应被 `facts.go:158-185` 的“恢复显示署名”覆盖到读取结果上——若有效值来自另一张表，这一段只改扫描列，不冲突。
4. 写入后需同时刷新发行（artistgraph 以发行为单位，`Refresh(tx, [releaseID])`）与该曲目的搜索值（`searchindex.RefreshTrack`），当前 `refreshGovernedRelease` 只做前者加发行搜索。
5. 艺人拆分的作用域指派按 `key_id` 匹配（`refresh.go:89-90`），修正值变成新名字键时原指派不再命中——与发行级专辑艺人修正的现有行为一致。

---

## 4. 程序层（子任务 C）可填曲目字段的位置

### 4.1 现状

- 补空缺只针对发行：`curation/program/enrich.go:75-103` `missingFields(e, sources.Release)` 只产出 `title`（占位时）、`album_artist`、`label`、`catalog_number`、`barcode`、`year`；`vacant` = 没有人工修正行且没有自动行。
- `enrich.go:197-228` `planMatch`：专辑方式有 `m.Release`；单曲方式只有 `m.Recording`（`:216-220`），**单曲方式不补任何字段**；两种方式都把 `m.Pairs` 里有录音 ID 的曲目写成 `track → recording` 外部对应（`:222-226`）。
- `enrich.go:232-297` `writeMatch`：步骤 `links` → `fields`（`writer.Apply(OnlyEmpty: true)`，`:262-271`）→ `group`；每步幂等键由 `write(ctx, step, ...)` 派生（spec §15.3：UUIDv5(目标、主题、规则版本、代次、步骤)）。
- 配对只留 ID：`curation/program/match.go:16-18` `TrackPair{TrackID, RecordingID}`；专辑配对在 `match_album.go:185` 由 `other sources.Track` 生成（此时有外部曲目的完整数据，但只存了 `RecordingID`）；单曲 `match_single.go:52`；模型层 `model_matcher.go:165,191`。
- spec §15.2：“**输入只取人工修正与扫描值**，不读自动层”；`LoadCurationEvidence` 发行头部合并了人工修正（`curation_evidence.go:125-149`），曲目部分只读扫描列（`:224-243`）。
- spec §16.2 `:530`（B3）：“`catalog.CurationEvidence`、`catalog.CurationTrack` 与曲目查询的标识种类**没有新增任何字段**：发行摘要是整个证据结构的哈希，加字段会让全库重做。”
- 零散单曲 `loose_unknown` “只对录音，不补发行字段、不取封面”（spec §15.2 `:418`）。

### 4.2 外部数据里有什么

- `sources/sources.go:177-185` `Track{ID, Position, Number, Title, LengthMS, Artists []Credit, RecordingID, RecordingTitle, ISRCs}`——发行内的曲目有**曲目标题**（MusicBrainz track title，可与录音标题不同）和**曲目署名**。
- `sources/sources.go:186-192` `Recording{ID, Title, LengthMS, Artists []Credit, ISRCs, Releases}`。
- `sources/sources.go:167` `Credit{ArtistID, Name, JoinPhrase}`；拼接成展示串：`curation/program/match.go:55-62` `credits()`（`Name + JoinPhrase` 依次相连）。
- MusicBrainz 映射：`sources/musicbrainz/project.go:28-31`（发行曲目：`Title`、`Artists: credits(t.ArtistCredit)`、`RecordingTitle`）、`:50`（录音）。
- 请求的 `inc`：`sources/musicbrainz/finders.go:31`（按 ID 读发行：`recordings isrcs artist-credits labels release-groups media discids`）、`:88`（按 ID 读录音：`artist-credits isrcs releases`）、`:105`（ISRC：`artist-credits releases`）。
- B3：单曲 AcoustID 路径逐个用 `RecordingByID` 读回正式数据后交给 `DecideSingle`（spec §16.4 `:587`）。

### 4.3 曲目“空缺”的现有定义

- `curation_evidence.go:225-227`：`ArtistPlaceholder` = 曲目艺人观察的 `source_kind='default_fallback'`，或没有观察且 `t.artist='未知艺术家'`；`TitlePlaceholder` = 标题观察 `default_fallback`。
- 扫描对标题的回退：`catalog/ingest/filename_numbers.go:158-178`（文件名回退记 `filename_fallback`，不算占位）；`catalog/ingest/observation.go:96-101`（推断且无来源记 `default_fallback`）。

---

## 5. 模型层（子任务 D）

### 5.1 `correct_release` 的两条路

- 对话与 MCP：`catalog/governance/stewardtools/tools.go:52-60`（工具定义，`changes` 的八个字段 schema 写在 `:55-58`）、`commands.go:28-34`（→ `CorrectionExecutor`，人工层）、`preview.go:24-55`（预览，清除时用 `ValueAfterClear`）。
- 后台：`app/curation_model.go:104-111` `backgroundToolAllowed` 白名单；`:114-160` `withBackgroundPolicy` 对 `correct_release` 在 `caller.Source == tool.SourceCuration` 时改走 `curationtools.AutoCorrectRelease`，预览前 `checkBackgroundCorrection`（`:162-197`：核对模型运行、开启者、本批领取的发行，调 `Writer.CheckModelCorrection`）。
- `curation/stewardtools/correct.go:13-63` `AutoCorrectRelease`：解析输入 → `claimed` 本批项 → `selectItem(items, release_id)` → 拒绝 `clear` → `writer.Apply{ExpectedRevision, Source: modelSource(...), BatchID: run.Batch, Key: c.IdempotencyKey}`。
- `curation/model_api.go:47-` `CheckModelCorrection`：锁发行、核对 `edit_revision`、人工修正存在即 `manual_override`、`filterFields`。
- 例外：`curation/model/exceptions.go:76-79` 采纳按 `e.Tool` 分派（`correct_release` → `acceptCorrection`，经 `CorrectionExecutor` 写人工层，spec §17.4）；`:308-316` 不采纳时按字段写 `SuppressReleaseValue`；`curation/model/scheduler.go:222` 按 `tool` 与 `release_id` 关联例外与待定项。
- 例外表约束：`backend/migrations/0064_auto_curation_model.sql:24` `tool text NOT NULL CHECK (tool IN ('resolve_release_match','correct_release'))`；前端 `frontend/src/features/curation/api/decoders.ts:169` 同样 `oneOf(["resolve_release_match","correct_release"])`；前端例外区 `frontend/src/features/curation/components/curation-exceptions-section.tsx:154` 按 `correct_release` 分支显示。

### 5.2 审查证据

- `catalog/stewardtools/review.go:41-48` `SupportsReviewEvidence`：`correct_release, change_release_group, set_default_release, review_release_issue, claim_release`。
- `review.go:51-120` `ReadReviewTargets`：按输入里的 `release_id`（及 `source_release_id`）读 `LoadCorrectionState` 的有效值（来源标 `catalog_effective_untrusted`）、扫描证据摘要、未结问题；目标类型只有 `release`。
- `app/steward.go:274-277`：不在名单的工具 → `runtime.ErrReviewEvidenceUnsupported`。
- `app/steward.go:294-325` `reviewExternal`：审查策略第 2 版的外部对应证据，按发行列出（含曲目 ID 集合）。
- `steward/runtime/review.go:20,27` `ReviewPolicyVersion = 2` 与 `ReviewPolicyRules`；D 交付说明字段类工具不需要“强证据”那一条（只结构类需要）。
- 管家工具标签：`frontend/src/features/steward/tool-labels.ts:116,243`（`correct_release` 的标签与参数摘要）。

### 5.3 推断：`correct_track` 需要的东西

- 输入：`release_id`（审查证据与 `selectItem` 都按发行；待定项目标是发行）+ `track_id` + `expected_revision` + `changes{title?, artist?}`；对话/MCP 写人工层，后台写自动层（照 `withBackgroundPolicy` 的分派）。
- `SupportsReviewEvidence` 加入新工具名；`ReadReviewTargets` 可沿用 `release_id`，再补该曲目的扫描值/修正/自动/有效值（目前证据里没有任何曲目字段）。
- `backgroundToolAllowed` 白名单加入；0064 的 `tool` CHECK 随新迁移放开；例外采纳/不采纳按工具分派（人工层执行器、曲目字段的不再提出标记）；前端解码器的 `oneOf` 一并放开。
- 预览 `preview.go` 加分支（`Target` 取曲目有效标题）；`ValueAfterClear` 的曲目版。

---

## 6. 前端

### 6.1 曲目标题与艺人显示处

| 文件 | 读法 |
| --- | --- |
| `frontend/src/features/catalog/components/release-media.tsx:123-125,161-176` | 发行详情曲目行：`track.work?.movement ?? track.title`；`track.artist` + `track.artist_refs`；空串显示“（无标题）”；与专辑艺人相同时标 `data-same` |
| `frontend/src/features/catalog/components/track-info.tsx:46-71` | “曲目信息”面板 |
| `frontend/src/features/catalog/api/track-summary.ts:47,52,62` | `trackTitleOf`、`trackArtistOf`、`trackArtistRefsOf`（空艺人回退专辑艺人） |
| `frontend/src/features/player/engine/tracks.ts:6-14` | 播放器曲目对象（标题、艺人、refs、专辑名） |
| `frontend/src/features/player/components/player-bar.tsx:67-69`、`player-queue.tsx:70-80,178,195`、`fullscreen-player.tsx`、`up-next-list.tsx` | 播放条、队列 |
| `frontend/src/features/player/output/media-session-binding.ts:73-74` | 系统媒体会话 |
| `frontend/src/features/search/components/track-results.tsx:32-35`、`match-note.ts:10,23-24` | 搜索曲目结果与命中说明（`track_title`“曲目”、`track_artist`“曲目艺人”） |
| `frontend/src/features/personal/components/track-table.tsx:83`、`playlist-detail-view.tsx` | 收藏、歌单、历史表格 |
| `frontend/src/app/routes/{favorites-page,history-playback,overview-playback,playlist-detail-page,search-playback,artist-detail-page,track-actions}.tsx` | 各页面的播放与操作标签 |

### 6.2 发行字段编辑入口（曲目可照做）

- `frontend/src/features/governance/components/release-admin-actions.tsx:49,70,117`：“更多操作”→“管理”分组→“编辑发行信息”→ `EditReleaseDialog`。
- `frontend/src/features/governance/components/edit-release-dialog.tsx`（256 行）：`:34-35` 注释——有自动值时“不用人工修正”一项叫“使用自动补全的值”并写出值与来源、不出现“确认扫描值”；`:43` `modeLabel`；`:72` “自动补全 · {sourceName(provider)}”。
- `frontend/src/features/governance/field-draft.ts`（158 行）：字段草稿与提交体；`hooks/use-release-corrections.ts`；`api/endpoints.ts`（`/releases/{id}/corrections`）。
- 曲目行现有操作：`frontend/src/app/routes/track-actions.tsx:49,59,133`（“更多操作”菜单，收藏、加入队列等），目前没有管理员分组。

### 6.3 “自动补全”标记（E1）

- 接口：发行详情与版本对比的 `auto_fields`（可选，空时省略）；解码 `frontend/src/features/catalog/api/decoders.ts:135,253`。
- 判断：`frontend/src/features/catalog/format.ts:81-91` `fieldMarksOf`、`isFieldAutoFilled`（人工修正优先；`artist` 跟随 `album_artist`）。
- 组件：`frontend/src/features/catalog/components/auto-filled-tag.tsx:8`、`field-mark-tag.tsx:5`（人工修正优先，其次自动补全）、`manual-correction-tag.tsx`；版本对比 `frontend/src/features/catalog/comparison.ts:110`；说明文字 `evidence-section.tsx:158-159`。
- 曲目 DTO（`catalog/projection.go:37-54` `ReleaseTrack`；`track_summary.go:27-37` `TrackSummary`）**没有**任何修正/自动标记字段。

---

## 7. 含曲目标题与艺人的接口样本（无修正时必须逐字不变）

样本目录 `backend/internal/app/testdata/api-samples/`，由 `backend/internal/app/api_samples_integration_test.go:31-35` 用真实 handler 生成后逐字比较；前端 `api-contract.test.ts` 往返解码。含曲目标题/艺人或 `artist_refs` 的：

- `releases/`：`get.json, get-auto.json, get-claimed.json, get-corrected.json, get-cue.json, get-dff.json, get-dsf.json, get-single.json, get-work.json`
- `release-groups/`：`get.json, get-default.json, get-multiple.json`；`release-claims/get.json`
- `tracks/`（`summaries` 等）；`search/`（58 个，其中 41 个含 `title`）
- `listening/`：`history-queue*.json, history-tracks*.json, recent-releases.json`
- `favorites/`：`tracks.json, tracks-missing.json, queue.json, albums.json`
- `playlists/`：`items*.json, queue*.json`
- `artists/`：`queue*.json, role-tracks*.json, albums.json, adjudication/*`
- `playback-zones/responses/*`、`playback-zones/events/*`（队列条目带曲目摘要）
- `steward/`（15 个含 `title`）、`operations/external-list.json` 等（歌词记录的 `track.title` 快照）、`curation/*`

---

## 8. 风险点（事实 + 推断）

### 8.1 热路径性能

事实：
- 曲目读取都是对 `tracks` 的直读：队列（`zones/service.go:225`，每批 1000 个 ID）、搜索结果页（`search_complete.go:18`）、艺人曲目（`artists_collections.go:178`）、发行详情（`read.go:187-199`，上限 10000 行）。
- 门禁：`catalog/personal_reads_explain_test.go:14,113`、`catalog/track_collection_stats_explain_integration_test.go:17`、`catalog/recent_additions_explain_integration_test.go:34`、`app/catalog_read_performance_integration_test.go`（spec §13 `:252-253`：设 `ROOMUSIC_CATALOG_EXPLAIN=1` 等变量才运行，不设是跳过）。
- 规范化搜索命中只查 `catalog_search_values`，不连 `tracks` 的标题列；排序用 `catalog_search_values` 的 `track_title` 规范化值（`search_sql.go:191-195`）或 `catalog_projection_subject_state.search_title`（`search_probe_sql.go:50-73`、`search_tiers_sql.go:70-81`）。所以只要投影写入时读有效值，规范化搜索热路径不多一次连接。
- 发行视图的做法是“每字段一次主键 LEFT JOIN，未引用的列连接被优化器去掉”（0017 `:66` 注释），governance-contracts §11 `:206` 记“物化有效值表”不在 M3（修正行很少）。

推断：
- 曲目修正行同样很少；两个字段 × 两层 = 每曲目至多 4 次主键 LEFT JOIN。队列/摘要一次 ≤1000 曲目，用 `(track_id, field)` 主键是索引查找。旧式 `ILIKE` 搜索（`search_sql.go:52-55`）若改读视图，是对 `tracks` 全表扫描再多连接——只在投影未就绪时用。
- 若选“物化到 `tracks` 新列”，扫描的 `UPDATE tracks SET ... title, artist`（`persist_track.go:81`）与 artistgraph 的 `UPDATE tracks SET artist`（`facts.go:177`）会与之并存，需要保证扫描不写新列；视图方式没有这个问题。

### 8.2 重扫与修正的关系

事实：
- 扫描只写 `tracks` 本表列（`persist_track.go:76-82`），不碰任何修正/自动表（发行级的结构保证见 governance-contracts §7 `:152-153`、curation §3 `:50`）。新增的曲目修正表只要扫描语句不涉及，重扫后自然保留。
- `artistgraph.refreshFacts` 每次删除“不在本次事实集合里”的署名行（`facts.go:207-215`），所以曲目艺人的修正/自动事实必须在 `refreshFacts` 内由修正表重新生成，否则每次重扫都会被删。
- 每次候选写入替换 track observations、credits（library-scan-pipeline `:81`）——与修正表无关。
- 扫描反馈的元数据摘要读扫描列（`scan_feedback_snapshot.go:68`），反映的是“文件里的标签变了没有”。

### 8.3 曲目身份与 CUE

事实：
- 曲目 UUID 在同一 root 内随音频内容延续（library-scan-pipeline `:70-81`）：先按来源身份匹配；同路径换内容 → 新建曲目，原曲目失去来源身份/置 `missing`；完整遍历时按内容指纹两侧唯一才沿用 UUID；跨 root 不重绑定；“相似度、标题、时长从不用于推断身份”。
- CUE 虚拟轨的来源身份是 sheet、父来源、track 号与 `INDEX 01`，配对键另含起止帧（同上 `:71-74`）。
- 代码中没有删除 `tracks` 行的语句（只删空的 `media`，`ingest/persist.go:130`）；`favorite_tracks`、`personal_playlist_items`、`play_history` 对 `tracks` 是 `ON DELETE RESTRICT`（`0021:6-11`、`0022:17`、`0016:10`）。
- 沿用的曲目可能被规划到另一个发行（`UPDATE tracks SET medium_id=...`，`persist_track.go:81`）。
- `external_links` 的 `object_type='track'` 不设外键，扫描/认领/人工修正都不涉及（curation spec §12 `:211-213`）。

推断：
- 以 `track_id` 为键的修正随曲目走：改路径、移动、换发行都保留；同路径换了内容会产生新曲目，旧修正留在旧（missing）曲目上——与发行级 G8 的已知限制同类。
- 认领（claim）目前只搬发行级修正（`catalog/governance/claim.go:225,592`），曲目修正不需要认领（曲目 ID 通常延续），但“旧发行上的曲目修正”需不需要出现在认领预览里要定。
- 曲目移到别的发行后，操作记录目标若按发行记（像歌词那样），历史记录挂在旧发行上；锁也按当前所属发行（`LockAutoReleaseOfTrack` 每次重新查所属发行）。

### 8.4 其他

- `operations_target_type_ck` 没有 `track`；外部歌词的先例是目标记为发行（`external_artwork.go:85`），修订号用发行的 `auto_revision`。
- 人工层如果也记在发行上并递增 `releases.edit_revision`，曲目修正会让同发行更早的人工修正“不再是最近一次修改”而撤不了（governance §6 `:143`），反之亦然；如果给曲目自己的版本号则互不影响（推断，需取舍）。
- 程序层的发行摘要是整个 `CurationEvidence` 的 JSON 哈希（`digest.go:26`），`CurationTrack` 加字段会改变全部摘要、全库重做（spec §16.2 `:530`）。推断：曲目人工修正应**改写现有的 `Title`/`Artist` 值**（输入取“人工修正与扫描值”，与发行头部做法一致），不加新字段；或新字段用 `omitempty`。人工修正改变值的那些发行会重新判定，这与发行级人工修正“经变化标记触发重判”（spec §15.8 `RescanAndChanges`）一致。
- 歌词摘要 `LyricsDigest` 含曲目标题与艺人（`digest.go:51-56`）：修正后会重新查询歌词，属预期行为（推断）。
- `artistgraph` 的输入成本估计（`maintenance/artist_batch.go:261-279`）列出所有名字输入，加入曲目修正/自动的艺人值才完整（推断）。

---

## 9. I 的最小方案建议（推断）

以下全部是推断，供规划时取舍。

### 9.1 数据模型

- 新迁移（0065）：
  - `track_corrections(track_id, field, mode, text_value, operation_id DEFERRABLE)`，主键 `(track_id, field)`，`field IN ('title','artist')`，`mode IN ('set')`（两个字段都是必填，照 `release_corrections_required_field_ck` 的规则二者都不能设空，于是只剩 `set`）；文本约束同 `release_corrections_value_ck`。`ON DELETE CASCADE` 随曲目。
  - `track_auto_values`：形状照搬上表。
  - 视图 `track_effective`：`SELECT t.*` 的列（或只列被读的列）+ `COALESCE(c_title.text_value, a_title.text_value, t.title) AS title`、`COALESCE(c_artist..., a_artist..., t.artist) AS artist`；四次主键 LEFT JOIN。列名、类型与 `tracks` 相同，读取处把 `FROM tracks t` 换成 `FROM track_effective t`。
  - `operations_type_ck` 加 `track_correction`、`track_auto_correction`；目标沿用 `release`（与 `track_external_lyrics` 一致，`target_id` 是所属发行，记录里存 `track_id`），不改 `operations_target_type_ck`。
  - `curation_model_exceptions.tool` CHECK 加 `correct_track`。
  - 版本号：人工层建议给 `tracks` 加 `edit_revision`（曲目各自的“最近一次修改”，不占发行的版本号）；自动层沿用发行的 `auto_revision`（`Bump` 现成）。若选择共用 `releases.edit_revision`，则修正曲目会挡住同发行更早修正的撤销——需在 PRD 里明确选哪一种。
  - 回退脚本：删两表与视图、两种操作类型及其撤销记录、`track` 目标的 `subject in (title,artist)` 标记、`layer in (correction,auto)` 的 `track_artist` 事实；与 0058 一样注明需完整重扫刷新投影。

### 9.2 有效值策略：视图而不是物化列

- 理由（事实依据见 8.1、8.2）：扫描与 artistgraph 都会写 `tracks.title/artist`；视图让“扫描只写扫描列、修正只写修正表、自动只写自动表”成为结构保证，与发行级完全同形；修正行稀少，主键 LEFT JOIN 代价小；规范化搜索走投影值，不在热路径上多连接。
- 读取改动点（第 2 节编号）：1 `LoadReleaseMedia`、2 `loadTrackSummariesSQL` 的 `selected_tracks`、3 `LoadTrackPlacements`、4/5 搜索投影 `loadValues`/`loadTrackValues`、6/7/8 旧式搜索、9 `curationTrackSQL`（只叠加人工层，不叠自动层，保持 §15.2 不变量）、13 歌词记录快照、管家工具随 1/2 自动得到。保持扫描值语义的：16 扫描反馈、`persist_track.go`。
- 署名：`refreshFacts` 的曲目查询连接两表生成 `correction`/`auto` 层 `track_artist` 事实；`refreshEffective` 曲目分支对 `track_artist` 三选一。
- 刷新：新的 `refreshGovernedTrack(tx, releaseID, trackID)` = `artistgraph.Refresh([releaseID])` + `searchindex.RefreshTrack(trackID)`（标题改动时发行搜索值不变）。
- 标记：`ReleaseTrack`、`TrackSummary` 可加 `corrected_fields`/`auto_fields`（`omitempty`，没有时省略），保证无修正时样本逐字不变。

### 9.3 写入与撤销

- 人工：`TrackCorrectionExecutor`（照 `CorrectionExecutor`）；HTTP `GET/PATCH /api/v1/tracks/{id}/corrections`（照 `/releases/{id}/corrections` 的检查顺序与错误码）；`Kind` 的 `Revert`/`Describe`（`changes.kind = "track_correction"`，带 `track:{id,title}`）。
- 自动：`curation.Writer.ApplyTrack`（照 `Apply`：`LockAutoReleaseOfTrack` → `checkWrite` → `OnlyEmpty`/不再提出/`unchanged` 过滤 → 写 `track_auto_values` → `Bump`）；`AutoSuppressionWriter` 加 `SuppressTrackValue`（`target_type='track'`、`subject='title'|'artist'`，`Suppressions(release_id)` 已能列出曲目标记）；`Kind` 的 `Revert`（按写入者判断）、`Describe`、`PreviewRevert`。按批/按来源撤销不用改（候选不按类型取）。
- 前端：`change-summary.ts`、`revert-operation-dialog.tsx`、governance 解码器加两种类型。

### 9.4 接入 C 与 D

- C：`TrackPair` 增加外部曲目标题与署名（专辑方式取 `sources.Track.Title`/`credits(Artists)`，单曲方式取 `Recording.Title`/`credits(Artists)`）；`writeMatch` 在 `fields` 之后加 `track_fields` 步骤，只补空缺（`TitlePlaceholder`、`ArtistPlaceholder`，且没有人工/自动行），查不再提出标记。是否需要提高 `program.RuleVersion`（会让全库重做）或像 B3 一样只按条件重开，需规划时定；`CurationTrack` 不加新字段（见 8.4）。
- D：`correct_track` 工具（对话与 MCP 写人工层，后台写自动层）；`backgroundToolAllowed` 白名单；`SupportsReviewEvidence` 与 `ReadReviewTargets` 的曲目字段证据；例外的采纳（人工层执行器）、不采纳（`SuppressTrackValue`）；0064 的 CHECK 与前端 `oneOf` 放开。审查策略版本不需要改（字段类工具不需要强证据那一条）。

### 9.5 建议顺序

1. 迁移 + 视图 + 读取模型（`catalog` 的 `TrackCorrectableFields`、`LoadTrackCorrectionIntents`/`LoadTrackAutoValues`）；所有读取处切到视图；接口样本逐字不变的测试先过。
2. 人工层：执行器、HTTP、撤销与 `Describe`、署名事实与搜索刷新、管家对话/MCP 的 `correct_track`；断言“署名事实随曲目艺人修正更新；搜索、队列、歌词匹配读到有效值；重扫后保留”。
3. 自动层：`Writer.ApplyTrack`、自动撤销与不再提出、按批撤销覆盖到新类型。
4. 接入 C（补空缺）与 D（后台工具、审查证据、例外）。
5. 前端：曲目行“管理”入口与编辑对话框（照 `EditReleaseDialog`）、曲目上的“人工修正/自动补全”标记、操作记录与例外的文案。

## Caveats / Not Found

- 未运行任何测试；性能结论没有实测数据，只依据现有代码结构与 spec 注释。
- 没有找到任何现有的曲目级修正、曲目视图或曲目 `edit_revision`。
- `sources.Track.Title` 与 `RecordingTitle` 选哪个作为曲目标题、多名署名的拼接是否与 `metadata.CanonicalTrackArtist` 的规范写法一致，本次未核对。
- 前端曲目行的管理员入口放在哪个菜单、曲目标记的视觉形式，本次只列了现有组件，未看设计文档 `frontend/DESIGN*.md`。
- `catalog/stewardtools/review.go` 第 120 行之后的其余审查目标（归组、默认版本）未逐行阅读。
