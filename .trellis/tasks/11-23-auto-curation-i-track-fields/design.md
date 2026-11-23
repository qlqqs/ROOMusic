# 自动整理 I：技术设计

初稿由规划子代理（Plan，2026-11-23）对照代码写出并审阅 `prd.md` 的 I1–I9；主代理全部采纳。审阅留给主代理的一项产品取舍（文件名推出的标题算不算空缺）已定：**算**（见 1.1）。路径省略 `backend/internal/`。

## 1. 对决定的修订

| 决定 | 级别 | 问题（文件:行） | 修订 |
| --- | --- | --- | --- |
| I1 | 必须 | `tracks` 有 35 列（0002、0010、0011、0013、0015、0039），视图不能 `t.*` 又覆盖 title/artist；以后 DROP/改列会被视图挡住 | 视图逐列写出、顺序同 `tracks`，末尾加 `edit_revision`；新增 `TestPostgreSQLTrackEffectiveColumns` 比对 `information_schema.columns`；database-guidelines 写明改 `tracks` 列要同时重建视图 |
| I4 | 必须 | `artistgraph/facts.go:29-43` 去重键 `(trackID, role, name)` 不含层；修正值与扫描值同名时 correction 层事实被跳过，`refreshEffective` 对有修正的曲目只取 correction 层 → 有效署名为空 | 去重键加 layer：`[4]string{trackID, role, name, layer}`；扫描层行为不变 |
| I5/R1 | 必须 | 歌词查询（`enrich_media.go:104,142,188,198`）与 `lyricsGap` 只读人工层，读不到 C 补上的艺人 | `CurationTrack` 加 `json:"-"` 的 `EffectiveTitle`、`EffectiveArtist`（人工 → 自动 → 扫描），不进 `ReleaseDigest`；`LyricsDigest`、歌词查询与 `lyricsGap` 用有效值（不是占位或有自动值都算非占位）；匹配判定仍只用 `Title`/`Artist`，§15.2 不变 |
| I7 | 必须 | 工具输入缺 `release_id`：scheduler 的 Escalate（`model/scheduler.go:213-225`）按 release_id 找待定项、`governance/stewardtools/commands.go:22` 对 `correct_` 前缀强制校验、`ReadReviewTargets` 与 `selectItem` 按发行查 | 输入为 `{release_id, track_id, expected_revision, changes, basis}`；锁内核对曲目属于该发行，否则后台 `409 item_stale`、对话 `404 not_found` |
| I6 | 必须 | `ModelMatcher.Plan` 也走 `planMatch`（`model_matcher.go:80-130`）；曲目补全进了 `plan.Changes` 会让已开放的匹配例外采纳时预览对不上，永远 `exception_stale` | 曲目补全只在程序层 `Worker.applyMatch` 计算；模型与采纳路径不写曲目字段、不记 `track_fields`，由第 6 节的补做处理 |
| I8 | 必须 | `release-media.tsx:123` 显示 `track.work?.movement ?? track.title`，古典曲目改了标题仍显示乐章名 | 标题有人工或自动值时显示 `track.title`；曲目信息面板同样 |
| I6 | 建议 | 照 B3 重开会重新判定（可能把 matched 降成 uncertain、多次请求） | 改为按已有外部对应补做（第 6 节），每个发行只读一次外部资料，不改状态与代次 |
| I3 | 建议 | 撤销记录目标取曲目当前所在发行，改挂后历史留在原发行（同歌词 §14.6） | 写进已知限制 |
| I2 | 建议 | 人工撤销响应的 `revision` 是曲目的；后台 `checkWrite` 提示写“发行修正版本” | 提示改中性“版本已变更”；合同写明 `revision` 含义 |

### 1.1 主代理补充：文件名推出的标题算空缺

`filename_numbers.go:158-178`：没有标题标签时标题来自文件名（`filename_fallback`），现在不算占位，I6 对标签差的文件就只能补艺人。决定：`CurationTrack` 再加 `json:"-"` 的 `TitleInferred`（观察来源是 `filename_fallback` 或 `default_fallback`），C 的曲目补全把它当空缺（仍只在没有人工或自动值时）。理由：不是标签值，匹配上时 MusicBrainz 的标题更可靠；写在自动层，可撤销，人工优先。

## 2. 迁移 0065_track_corrections

```sql
ALTER TABLE tracks ADD COLUMN edit_revision BIGINT NOT NULL DEFAULT 1 CONSTRAINT tracks_edit_revision_ck CHECK (edit_revision > 0);
CREATE TABLE track_corrections (
  track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  field TEXT NOT NULL, mode TEXT NOT NULL, text_value TEXT NOT NULL,
  operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
  PRIMARY KEY (track_id, field),
  CONSTRAINT track_corrections_field_ck CHECK (field IN ('title','artist')),
  CONSTRAINT track_corrections_mode_ck CHECK (mode = 'set'),
  CONSTRAINT track_corrections_value_ck CHECK (BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096));
-- track_auto_values 同形（显式写出，外键与约束名用 track_auto_values_*；LIKE 不复制外键）
CREATE VIEW track_effective AS
SELECT t.id, t.medium_id, t.position,
  COALESCE(c_title.text_value, a_title.text_value, t.title) AS title,
  COALESCE(c_artist.text_value, a_artist.text_value, t.artist) AS artist,
  t.disc_number, …（其余各列按 tracks 的顺序逐列写出，实现时用 \d tracks 核对）…, t.edit_revision
FROM tracks t
LEFT JOIN track_corrections c_title ON c_title.track_id=t.id AND c_title.field='title'
LEFT JOIN track_corrections c_artist ON c_artist.track_id=t.id AND c_artist.field='artist'
LEFT JOIN track_auto_values a_title ON a_title.track_id=t.id AND a_title.field='title'
LEFT JOIN track_auto_values a_artist ON a_artist.track_id=t.id AND a_artist.field='artist';
-- operations_type_ck 加 track_correction、track_auto_correction；curation_model_exceptions 的 tool CHECK 加 correct_track
```

`operations_target_type_ck` 与 `curation_suppressions` 不改（后者 `target_type` 没有 CHECK；新标记 `target_type='track'`、`subject ∈ {title, artist}`）。

down（有损，照 0061 的顺序）：DROP VIEW；置空 `steward_plan_items.operation_id` 里指向两种新类型及其撤销的引用；删 `curation_suppressions` 中 track 的 title/artist 标记；删 `tool='correct_track'` 的例外；DROP 两表；删 `artist_credits` 中 `track_id IS NOT NULL AND role='track_artist' AND layer IN ('correction','auto')`；先删两种类型的撤销再删本体；`SET CONSTRAINTS ALL IMMEDIATE` 后恢复两条 CHECK；DROP `tracks.edit_revision`。`curation_work_states.result` 的 `track_fields` 键保留。合同写明：回退后必须完整重扫。

## 3. 读取切换

| 位置 | 改法 |
| --- | --- |
| `catalog/read.go:187` `LoadReleaseMedia` | `JOIN track_effective t`；另一条 UNION ALL 查询填 `ReleaseTrack.CorrectedFields`、`AutoFields`（`omitempty`） |
| `catalog/track_summary.go:41` | `FROM track_effective t` |
| `catalog/track_placement.go:52` | `FROM track_effective tracks` |
| `searchindex/write.go:137`、`batch.go:145` | `FROM track_effective` |
| `catalog/search_sql.go:52,179`、`read.go:35` | `track_effective` |
| `catalog/curation_evidence.go:224` | 只叠加人工层；有人工行时两个 Placeholder 为假；填 `EffectiveTitle`、`EffectiveArtist`、`TitleInferred`（`json:"-"`） |
| `curation/external_lyrics.go:67` | 快照读 `track_effective` 的标题 |
| `artistgraph/facts.go:106` | LEFT JOIN 两表，加建 correction 与 auto 层 `track_artist` 事实 |
| `artistgraph/refresh.go:84` | 曲目分支 `c.role<>'track_artist' OR c.layer = CASE …`（修正 → 自动 → 扫描） |
| `maintenance/artist_batch.go:261` | 成本估计计入修正与自动值 |
| 保持扫描值 | `ingest/persist_track.go`、`scan_feedback_snapshot.go:68`、`facts.go:148-185` 恢复显示署名的那段 |

管家工具、队列、历史、收藏、歌单都经上面几处。没有修正时样本逐字不变（`ReleaseTrack` 新字段 `omitempty`，`TrackSummary` 不加字段，`get_track` 多一个 `revision` 没有样本）。两个 EXPLAIN 门禁用例的辅助表清单加两张新表。

## 4. 人工层

- catalog：`TrackCorrectableFields = [{title, "曲目标题", Required}, {artist, "曲目艺人", Required}]`；`LoadTrackCorrectionIntents`、`LoadTrackAutoValues`。
- governance：`ParseCorrectionChanges` 字段表参数化，`ParseTrackCorrectionChanges`（`null` → `invalid_correction`）；`TrackCorrectionState{track_id, release_id, revision, fields[{field, scan_value, correction, auto, effective_value, uncertain}]}`。
- `TrackCorrectionExecutor`：锁序 幂等锁 → CATP（`writegate.Lock`）→ 查所在发行（曲目 present、发行可见）`FOR UPDATE` 锁发行 → `FOR UPDATE` 锁曲目；`tracks.edit_revision` 不等 `409 revision_conflict`（“这首曲目刚被修改过，请刷新后再试”）；都没变 `400 no_changes`；写后 `edit_revision+1`、`refreshGovernedTrack`。记录目标是发行、版本是曲目的；状态 `{track_id, track_title, fields, effective}`；指纹 `{type, track_id, expected_revision, changes}`。
- `refreshGovernedTrack(tx, releaseID, trackID, artistChanged)`：艺人变了 `artistgraph.Refresh([releaseID])`；总是 `searchindex.RefreshTrack(trackID)`。
- HTTP `GET`/`PATCH /api/v1/tracks/{id}/corrections`，检查顺序照 `catalog/httpapi/corrections.go`（GET：401→403→`400 invalid_id`→`404`；PATCH：同源→401/403→`invalid_id`→`invalid_input`→`invalid_correction`→`Run` 的各错误）；成功带 `operation_id`。
- Kind `track_correction`：`Revert`（曲目 present 且 `edit_revision == ResultRevision`，否则 `409 revert_conflict`“这首曲目之后又被修改过”）、`Describe`（`{kind, track:{id,title}, fields:[{field, before:{correction, effective}, after}]}`）、`PreviewRevert`；在 `app.go` 注册。

## 5. 自动层

- `NormalizeTrackAutoFields`（只 `set`）；`LockAutoTrack`（锁序同上，`Write` 写 `track_auto_values`、发行 `auto_revision+1`、`refreshGovernedTrack`）；`LockAutoTrackForRevert`（不要求在库或可见，行不在则 `revert_conflict`）。
- `curation.Writer.ApplyTrack(TrackRequest{TrackID, ReleaseID?, ExpectedRevision, OnlyEmpty, Fields, Source, BatchID, Key, ConfirmedBy, ConversationID, RequestID})`：照 `Apply`（锁 → ReleaseID 一致否则 `item_stale` → `checkWrite` → 模型来源带版本时已有人工修正 `manual_override` → `filterTrackFields`：`occupied`、`suppressed`、`unchanged`，都没有 `nothing_to_apply`）；指纹 `{type:"track_auto_correction", track_id, fields, provider, expected_revision?, only_empty?}`；`CheckModelTrackCorrection`。
- 不再提出：`Writer.SuppressTrackValue(tx, trackID, field, intent, revertID, actor)`；governance 接口 `TrackAutoSuppressionWriter`，`TrackAutoCorrectionOperations(w)` 注册（缺写入器装配时 panic）。
- Kind `track_auto_correction`：`Revert`（逐格核对 `operation_id`、恢复前态与写入者、每格写不再提出）、`Describe`、`PreviewRevert`。按批、按来源撤销不用改。

## 6. 接入 C

- `TrackPair` 不改（按位置的字面量与 DeepEqual 用例）。新函数 `planTrackFields(e, m)`：专辑方式用 `Pairs` 的 RecordingID 在 `m.Release.Media[].Tracks` 找曲目取 `Track.Title`、`credits(Track.Artists)`（同一录音多首且不一致则跳过）；单曲方式取 `m.Recording.Title`、`credits(Artists)`（`loose_unknown` 也补）。只补 `TitlePlaceholder`/`TitleInferred` 或 `ArtistPlaceholder` 为真的字段（证据已叠加人工层），值 `NormalizeValue` 后非空 ≤4096 字节，且没被 `hasMarker("track", id, field)` 挡下。
- 写入：`matchWritePlan` 加 `tracks`，`writeMatch` 在 `fields` 之后、`group` 之前加步骤 `track_fields/<track_id>`，每首一次 `ApplyTrack(OnlyEmpty: true, Source: musicbrainz)`，`nothing_to_apply` 当作已完成；`releaseResult` 写 `track_fields:"done"`（`omitempty`）。只有 `Worker.applyMatch` 填 `tracks`。
- 补做（不升规则版本）：`needsTrackFields(state, e)`（与 `needsWork` 分开）：release、matched、规则版本与摘要一致、`result` 没有 `track_fields`、有占位（或文件名推出）的曲目且有外部对应。满足时按已有外部对应读一次（专辑 `ReleaseByID`、单曲 `RecordingByID`），按已有曲目对应配对，不重新判定、代次不变，键 `u.key("track_fields/"+id)`；之后在 RepeatableRead 事务里核对状态没变，只合并 `result.track_fields = done | none | unavailable`（外部 404 记 unavailable）；暂时失败与源关闭不写状态。
- 摘要：自动层不进 `ReleaseDigest`；`LyricsDigest` 用有效值，补上艺人会让这首曲目的歌词重查一次（预期）。

## 7. 接入 D

- 工具 `correct_track`（`governance/stewardtools/tools.go`）：`release_id, track_id, expected_revision, changes{title?, artist?}`（`set`/`clear`，人工层 `clear` 恢复扫描值或自动值——注意：两个字段必填，`clear` 的含义是删掉修正行），`command()` 构造 `TrackCorrectionExecutor` 并核对 release_id；`preview()` 新分支。
- 分路（`app/curation_model.go`）：`backgroundToolAllowed` 加 `correct_track`；`Source==curation` 时走 `curationtools.AutoCorrectTrack`（拒 `clear`、`selectItem(byRelease)`、`ApplyTrack{ReleaseID: item.TargetID, ExpectedRevision, Source: modelSource, BatchID}`）；预览前 `checkBackgroundTrackCorrection`；scheduler 的 Escalate 对 `correct_track` 按 `release_id` 匹配。
- 审查证据：`SupportsReviewEvidence` 加 `correct_track`；`ReadReviewTargets` 有 `track_id` 时加 `ReviewTarget{Type:"track", …, Data:{release_id, fields:[…], source:"catalog_effective_untrusted"}}`；审查策略版本不变。
- 例外（`curation/model/exceptions.go`）：`proposal` 加 `TrackID`；`Accept` 加 `case "correct_track"`（现在的 switch 没有 default，不加会什么都不做就成功）；采纳 `LockAutoTrack` → 核对发行 → `CheckMatchItem` → 重算前后值 `compareChanges` → `TrackCorrectionExecutor`（当前版本）→ `FinishException`；不采纳逐字段 `SuppressTrackValue`。例外 DTO 加 `track:{id, title}`（`omitempty`）。
- 依赖白名单不用改。

## 8. 前端

- governance：`fetchTrackCorrections`、`correctTrack`、`use-track-corrections`；`EditTrackDialog`（两个字段；有自动值时“使用自动补全的值”；409 与 400 文案）；`OPERATION_TYPES` 与两种 changes 解码器（带 `track`）；`change-summary.ts`（类型名“修改曲目信息”“自动补全曲目信息”、撤销名、字段标签、每行带曲目名、不能撤销原因）；`revert-operation-dialog.tsx` titles。
- catalog：曲目解码器加可选 `corrected_fields`、`auto_fields`；`release-media.tsx` 与 `track-info.tsx` 用 `FieldMarkTag` 标出并按 1 节修订显示标题；`album-detail-page.tsx` 管理员可见时 `extraActions` 加“修正曲目信息…”。
- curation：例外解码器 `tool` 加 `correct_track`、可选 `track`；`exceptionChangeLines` 与采纳说明按曲目写；`suppressionSubjectText` 对 `target_type==='track'` 用曲目字段标签。steward：`tool-labels.ts` 加 `correct_track`。
- 样本：`tracks/corrections-get`、`tracks/corrections-patched`、`releases/get-track-corrected`、`operations/track-correction-list`、`operations/track-auto-list`、`operations/track-auto-reverted`、`curation/exceptions-track`、`errors/track-revision-conflict`。

## 9. 测试

| 需求 | 用例 |
| --- | --- |
| R1 | 0065 往返、`TestPostgreSQLTrackEffectiveColumns`、API 样本逐字不变、`TestPostgreSQLTrackFieldReads`（详情、摘要、放置、两种搜索、队列、历史、收藏、艺人曲目、`get_track`） |
| R2 | `TestPostgreSQLTrackCorrectionAPI`、`…Revert`、`…Rescan`、`…ArtistFacts`（同名、艺人页、artist_refs）、`…Search` |
| R3 | `TestPostgreSQLTrackAutoApply`、`…Revert`、`…BatchRevert`、`…ProviderRevert`、`…IndependentRevisions`、`…Concurrent` |
| R4 | `TestPostgreSQLCurationProgramTrackFields`（专辑、单曲、只补空缺、文件名推出的标题）、`TestTrackFieldsBackfillOnce`、`TestB3DigestBaseline` 不变、`TestLyricsUsesEffectiveArtist` |
| R5 | `TestPostgreSQLCurationModelCorrectTrackAutoLayer`、`TestPostgreSQLStewardCorrectTrackManual`、`TestPostgreSQLCurationExceptionTrackAcceptDecline`、`TestPostgreSQLStewardReviewTrackEvidence` |
| R6 | 前端组件与页面测试、`api-contract.test` |

变异检查：去重键去掉 layer；`refreshEffective` 层条件去掉；视图人工与自动顺序对调；`OnlyEmpty` 取反；撤销不比对 `operation_id`；`needsTrackFields` 去掉 `track_fields` 判断；例外 `case` 删掉；人工执行器改成递增发行的 `edit_revision`。

## 10. 风险与回退

- 热路径每首曲目多 4 次主键 LEFT JOIN（修正行很少）；EXPLAIN 门禁合并前跑一次。
- 以后改 `tracks` 列必须重建视图（列一致测试守住）。
- 补做的出站量：每个占位的已匹配发行多一次按 ID 读取，经闸门。
- 已知限制：同路径换文件产生新曲目，旧修正留在不在库的曲目上；撤销记录挂在曲目当时所在的发行上。
- 回退：0065 down 有损，回退后完整重扫；只需关 C 的补做时不发布派发 2 的程序层改动。
