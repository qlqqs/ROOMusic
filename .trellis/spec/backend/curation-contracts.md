# 自动整理合同

本合同描述当前自动整理链。决定与规则依据 [ADR 0009](../../../docs/architecture/adr/0009-curation-chain-review.md)，通用事务、幂等和管理员鉴权依据 [受治理操作合同](./governance-contracts.md)。外部 HTTP 请求的重试和缓存依据 [外部源合同](./sources-contracts.md)。

## 1. 扫描交来的输入

### 1.1 扫描范围与稳定性

手动扫描和每天定时扫描覆盖活动曲库根。首次成功的整根扫描建立目录监听，文件新建、删除、修改和改名按目录合并，最后一次事件后安静 2 秒再处理。正在写入的文件持续推迟。监听不到的根按 15 分钟、1 小时、4 小时、12 小时或每天增量扫描，默认 1 小时。网络共享和监听注册失败由程序识别，监听资源用尽也归为 `registration_failed`，程序不修改系统监听上限。确认扫描只读取发生变化的专辑目录及其碟片子目录，并在成员稳定 60 秒后提交整理输入。

读文件前后各读取文件大小和原始修改时间，两次任一不同就丢弃本次结果，等待下一次事件。微秒截断只用于保存值。目录根不可用、子目录不可读、文件损坏或移动有歧义时保留旧数据。

### 1.2 标签、文件名和附属文件

扫描补读总曲数、总碟数、完整发行日期和原始发行日期、单曲与现场等媒体标记、录制或演出日期、增益与峰值、同步歌词、播客与有声书标记及常见注释字段。注释只为模型本地辅助提供编号线索，当前不直接用于显示、整理或播放。MP3 的 ID3v1 流派编号按编号表转换。`仅当 TCON 第一项为 `Podcast` 或 `Literature` 时设置对应媒体标记。`PCST`、`PCSP`、`ISPODCAST` 和 `ISAUDIOBOOK` 等键也会设置播客或有声书标记。扩展名仅预筛，音频和无扩展名文件按文件头确认格式，图片、文本和 CUE 不读文件头。未知格式与读文件错误分别记录。

艺人名只在括号外按半角或全角分号、两侧有空格的 `/`、两侧有空格的 `feat.`、`ft.`、`with`、顿号、换行和 NUL 拆分。`&`、逗号和紧贴的 `/` 保留。中文古典流派只按“古典”“古典音乐”“古典乐”名单识别。占位值如 `Unknown Artist`、`Track 01` 当作没有值。旧编码只有一种可解码时整张使用该编码，多种都可解码时保留原样并等待本地辅助。

保留按文件名推断艺人、年份、专辑和格式词的扫描规则。没有标签且没有模型时不按连字符猜艺人，曲号和格式词去掉后作为标题，专辑名取目录名，艺人为“未知艺术家”。

### 1.3 归组与排序

同目录出现多个专辑名时，依次用标签碟号与曲号、文件名碟号与曲号、两者混合、仅文件名曲号且把碟号当 1 检查是否从 1 到 N 连续。连续则归为一张。不连续时按音频格式拆分并再次检查，仍不连续才按专辑名或“艺人 + 专辑名”拆分。不使用“过半合成一张”。

`disc`、`disk`、`cd` 加编号的子目录并入上一级。带名称的碟目录记录碟标题，Bonus 和特典碟排在正碟之后。专辑名比较忽略大小写、标点、空格和变音符号。文件中的 MusicBrainz 编号不参与归组。没有专辑艺人时按过半艺人、2–4 位并列或 5 位以上群星处理。Deluxe、Remastered 等版本词单独保存，不参与归组和身份。专辑目录及 Art、Artwork、Scans、Extras 子目录中的图片和文档绑定到专辑并保存图片格式和尺寸。排序键去标点、折叠变音，并将开头的英文 `The ` 移到末尾。

缺碟号、缺曲号、无法识别碟目录名和字段打平时不推断。明确写出的 `CD2`、`1-03` 和 `A1` 保留。没有编号的曲目按文件名顺序排列但不写曲号。

## 2. 识别查找顺序与结论

### 2.1 查找准入

已匹配且本地文件未变化的发行不再查询。专辑名和艺人齐全，或存在 MusicBrainz 编号、光盘目录、条码、目录号、ISRC 或其他可检索编号时进入查询。存在任一可检索编号时不做名字查询。名字只有一半且没有编号的不查。只有专辑名的合辑仍进入查询，散放单曲走标签、ISRC、AcoustID 和录音搜索路径。播客和有声书不查。占位标题和艺人同时存在且没有编号时不按名字查，启用声纹后使用声纹。没有模型时保留程序可确定的结果，不在多个候选中猜。

### 2.2 查询顺序

同一发行按以下顺序查询，某一步无结果才进入下一步，得到结构核对通过的结果立即停止。

1. 文件中的 MusicBrainz 发行编号。
2. CUE 的 TOC 光盘目录。
3. 条码。
4. 目录号和厂牌。
5. 四首曲目的 ISRC。
6. 四首曲目的 AcoustID 声纹。
7. 完整名字查询，依次使用专辑名、艺人和曲数，去掉豪华版及编码词后不限曲数，再按艺人名查询。艺人查询只有一个结果时才按艺人编号继续。

查询只使用本地名字，或本地辅助整理后的名字。任一可检索编号存在时跳过名字查询，第 1–6 步均无结果记 `unmatched`。程序不使用模型记忆中的事实。某一步得到候选但编号、曲数、时长或发行组对不上时立即记为 `conflict`，不改用更弱线索。所有编号步骤没有佐证记 `insufficient_corroboration`，名字查询失败记 `unmatched`，路由层的 `conflict` 原因为 `different_release_groups`，结构核对仍保存具体冲突代码。查询错误不产生结论。

### 2.3 结构判定和发行组

名字查询要求每张碟曲数相同，可比时长逐首在 3 秒或 1% 内，至少一半曲目可比较。四首及以下还要求专辑名和艺人一致。编号查询要求曲数相同并有一项独立佐证。佐证按方法分别判定：`tag_id` 用曲数或时长且可按位置取子集，`barcode` 用曲数并加时长、标题或艺人之一，`catalog_number` 用曲数并加厂牌或时长，`disc_id` 用曲数并加标题或艺人，`isrc` 用曲数并加一致 ISRC，`acoustid` 用曲数及时长并加一致录音。条码、目录号、发行编号、碟数、曲序、格式或艺人标签冲突时判为冲突。冲突代码还包括方法对应的 `*_tag_mismatch`、`duration_mismatch`、`position_conflict`、`mixed_release_tags`。

同一发行组中专辑名、艺人和曲数一致的多个版本直接认到发行组。只有多于 4 首、非古典、非合辑、仅一个已知发行组且没有曲目级编号时，才可直接认发行组。部分覆盖时记录 `partial_subset` 或 `partial_superset`。合辑、古典和四首及以下短专辑逐版本核对。认到发行组时，本地推断曲名只从最早版本补齐。两张本地专辑认到同一版本时，没有重复曲目且合起来正好覆盖各碟才合并，有重复曲目则放入同一版本组，其余交模型或保持原样。

### 2.4 结论、状态与重查

整理工作状态保存在 `curation_work_states`，模型待定项保存在 `curation_pending_items`。新结论状态如下。

| 状态 | 含义 | 重查条件 |
| --- | --- | --- |
| `matched` | 已与 MusicBrainz 发行、发行组、录音或艺人对应 | 本地文件变化或规则版本要求 |
| `unmatched` | 已查询的来源没有可核对结果 | 本地变化、规则升级或 90 天到期 |
| `conflict` | 查询结果与本地证据冲突 | 本地变化、规则升级，或模型识别判断 |
| `try_later` | 来源暂停或等待重试 | 来源恢复 |
| `query_error` | 该发行连续三次发生专属查询错误 | 本地变化或规则升级 |

历史记录仍可读 `uncertain`、`no_match`、`unlisted` 和 `no_usable_candidates`，这些值只读，不在本轮迁移。`unmatched` 记录查询过的平台和时间，90 天重查走完整程序步骤并包含声纹。`insufficient_evidence` 作为 `done` 结论保存。原因是 `candidates_revoked` 的 `unmatched` 和原因是 `ambiguous_editions` 的 `conflict` 均按 90 天重查。撤销或被占用的候选归入 `unmatched` 并提示候选已撤销。规则升级默认只重查未匹配、冲突和查询出错项，已匹配项只在对应规则版本要求时重查。

### 2.5 识别结论的存储

`curation_work_states` 以 `(subject,target_id)` 为主键。`subject` 可为 `release`、`artwork`、`lyrics`、`artist_media` 或 `fingerprint`，并保存 `needs_work`、`manual_requested_at`、`queue_release_id`、`queue_class`、`queue_has_id`、`queue_gap`、`queue_artist_unlinked`、`queue_added_at`、`queue_identity`、`patrol_revision`、`local_rule_version`、`match_rule_version`、`identity_digest`、`gap_digest` 和 `requery_match_version`。发行结果原因还包括 `request_limit`、`ambiguous_editions`。其中 `artwork` 目标为发行，`lyrics` 目标为曲目。表保存 `rule_version`、`input_digest`、`file_digest`、`status`、`attempts`、`generation`、`retry_count`、`next_attempt_at`、`result`、`queue_release_id` 和 `updated_at`。发行状态约束包含 `pending`、`matched`、`uncertain`、`no_match`、`try_later`、`done`、`unlisted`、`no_usable_candidates`、`unmatched`、`conflict` 和 `query_error`。`file_digest` 由 SQL 函数 `curation_release_file_digest()` 计算，包含成员根、相对路径、字节数、微秒修改时间和 `releases.curation_member_signature`，使用 CUE 的上级路径。视图 `curation_eligible_releases` 只包含有 present 音频且不含播客或有声书标记的发行。

`curation_release_match_results` 每个发行和来源一行，保存 `release_id`、`source`、`status`（`matched`、`unmatched`、`conflict`）、`checked_at`、`reason`、`conflicts`、`snapshot_id`、`candidates` 和 `selection`。`selection` 只在状态为 `matched` 且对应仍属于这次选择时返回。`release_match_credit_parts` 保存 `release_id`、可空 `track_id`、`role`（`release_artist`、`album_artist`、`track_artist`）、`position`、`local_text`、`name`、`artist_mbid`、可空 `artist_link_id`、`basis_link_id` 和 `operation_id`。角色为 `track_artist` 时必须有曲目标识，位置从 1 开始，组合键保证每一段唯一。删除最后一条拆名段时，仅删除由 `split_credit` 方法建立且不再被其他段引用的艺人对应。

`curation_match_names` 保存通过外部对应核实的 MusicBrainz 名称。字段为 `release_id`、`target_type`（`release` 或 `track`）、`target_id`、`field`、`mb_value`、`external_id`、`basis_link_id` 和 `basis_operation_id`。`curation_artist_negative_pairs` 以无序规范化名字对为主键，保存 `reason`、`model`、`legacy` 和 `decided_at`。名字否定对永久阻止重复询问。

`curation_pending_items` 的识别项 `kind` 为 `release_match` 或 `recording_match`，状态为 `waiting_model`、`in_progress`、`resolved`、`shelved` 或 `waiting_route`。表还保存 `id`、`target_type`、`target_id`、可空 `artist_id`、`kind`、`rule_version`、`input_digest`、`identity_digest`、`route`、`evidence`、`candidates`、`conflicts`、`resolution`、`created_at`、`updated_at`、`resolved_at`、`conversation_id`、`claimed_at`、`next_attempt_at`、`attempts`、`review_retries`、`shelved_reason`、`match_rule_version`、`item_tool_calls`、`item_tokens`、`source_retry_count`、`claim_source_recovery_at`、`empty_count`、`single_request`、`reset_item_usage_on_claim`、`model_stop_code`、`artist_web_generation`、`artist_web_actor`、`artist_web_suspended` 和 `artist_web_priority`。每个目标和种类至多一条未结束项，`evidence`、`candidates` 和 `conflicts` 均为 JSON。模型和管理员读取时把外部文本按不可信文本处理。

### 2.6 识别接口与结论读取

`GET /api/v1/admin/curation/conflicts?page=&page_size=` 只返回可见且仍有整理资格的 `conflict` 结果。响应为 `{items,pagination}`，项包含发行标识、标题、来源、状态、查询时间、原因、冲突代码、候选、`snapshot_id` 和 `selection`，选择字段在列表中置空。

`GET /api/v1/admin/curation/releases/{id}/match-results` 返回 `results` 数组。每项含 `source`、`status`、`checked_at`、`reason`、`conflicts`、`candidates`、`snapshot_id` 和 `selection`。不存在或不可见的发行返回 `404 not_found`，无效标识返回 `400 invalid_input`。

候选字段包含 `entity`（`release`、`release_group` 或 `recording`）、`external_id`、`release_group_id`、`title`、`artist`、`date`、`country`、`track_count`、`barcode`、`catalog_numbers`、`method`、`source` 和 `status`。候选中的外部文本按不可信文本处理，`candidates` 为空数组而非 null。`snapshot_id` 总是出现，没有快照时为 `null`。`selection` 含 `external_id`、`selected_by`、`selected_at` 和 `operation_ids`，未选择时为 null。

`POST /api/v1/admin/curation/releases/{id}/match-selection` 接受 `{source,snapshot_id,external_id}`，要求 `Idempotency-Key`。后端在发行锁内重新读取快照、候选、外部发行和本地证据，选择必须仍是当前候选并通过结构核对。摘要、文件、来源结果或候选发生变化返回 `409 item_stale`，选择无法满足结构核对的独立佐证返回 `409 insufficient_corroboration`，选择已被撤销或同值已标记时步骤内部跳过，不返回 `nothing_to_apply`。成功响应是更新后的匹配结果并包含实际 `operation_ids`。

管理员也可在聊天或 MCP 中用 `match_release_external` 指定 MusicBrainz 编号。工具输入为 `{release_id,entity,mbid}`。程序入口是 `ModelMatcher.PreviewExternal(ctx, in ExternalMatchInput, actor string)` 和 `ModelMatcher.SelectExternal(ctx, in ExternalMatchInput, actor, key string, origin operations.Origin, requestID string)`，分别返回 `ExternalMatchPreview`、`ExternalMatchResult` 和错误。预览按当前证据展示目标版本的标题、艺人、碟曲数、对应层级和可补字段。执行重新取详情，使用与人工选候选相同的发行锁，同时绑定文件摘要、发行证据摘要、工作状态及代次。

| 条件 | 拒绝码与处置 |
| --- | --- |
| 发行不存在或不可见 | `not_found` |
| 发行可见但没有整理资格 | `not_eligible` |
| 已有版本或组级对应，或单轨散曲已有录音对应 | `already_matched`，先用 `revert_operation` 撤销 |
| 所选编号的外部对应已被撤销并标记 | `suppressed`，到自动整理设置的“不再提出”中清除 |
| UUID 无效、类型无效、专辑选录音或散曲选发行 | `invalid_input` |
| 最新详情缺少结构佐证或出现反证 | `insufficient_corroboration`，消息带具体原因，如 `track_count`、`position_conflict`、`duration_mismatch`、`recording_tag_mismatch` |
| 核对期间文件、发行证据、资格或工作结论变化 | `item_stale`，重新读取 |
| 同一操作者用同一幂等键请求其他目标 | `idempotency_conflict` |

该入口从点名的候选和空快照开始，无需冲突待定项。`release` 按结构核对版本；部分覆盖符合第 2.3 节时，或存在无法区分的同组版本时，写发行本身的 `release_group_match`，保留组级结论、公共署名及可证实的组级字段。`recording` 仅用于 `loose_unknown` 的单轨发行，核对曲名、艺人、时长及本地编号后对应到曲目。精确版本、发行组和录音的结果层级分别是 `release`、`release_group` 和 `recording`。

对应、自动字段、归组和结案在同一事务提交，各治理步骤保留独立的可撤销操作。操作来源为聊天或 MCP，保留会话、方案或令牌标识，写入方为 `source/steward`。返回实际对应的 `matched_mbid`、首条 `operation_id` 及全部 `operation_ids`，同键同输入回放原收据。结案把发行状态置为 `matched`，代次加一，使用当前 `MatchRuleVersion`，结束等待的发行与录音识别项。在途识别提交时仍须核对原摘要。

管家操作没有程序批次，`LoadProgramChanges` 的 `OtherOperation` 会触发队列重新投影。程序维护按现有对应补署名、艺人和适用的封面、歌词；管家写入的自动署名字段按版本对应操作派生的键核对来源，复用字段署名回填。管理员点名执行独立于后台总开关，仍须通过 MusicBrainz 来源开关和扫描闸门。后台运行后才执行后续补齐。

`POST /api/v1/admin/curation/releases/{id}/recheck` 不带请求体，仅对原因不是 `candidates_revoked` 的 `unmatched` 有效，写入新的待核对标记并返回 `202`，否则返回 `409 not_unlisted`。`POST /api/v1/admin/curation/model/retry` 触发当前模型失败段重试并返回模型进度。

`POST /api/v1/admin/curation/releases/{id}/adopt-name` 接受 `{target_id,field,basis_operation_id,expected_revision}` 和 `Idempotency-Key`。`target_id` 为发行或曲目的目标，`field` 为可采用的名称字段。请求标识或依据不是 UUID 返回 `400 invalid_input`，依据操作不存在或不是当前 MusicBrainz 对应返回 `409 item_stale`，预期人工版本变化返回 `409 revision_conflict`，成功写人工层并返回 `{field,effective_value,revision,operation_id}`。

### 2.7 规则版本、摘要和分类

扫描本地规则版本、外部识别规则版本和资料投影规则版本分别保存。当前外部识别 `MatchRuleVersion=106`，本地辅助版本由 `LocalRuleVersion` 提供。发行工作状态同时保存 `local_rule_version`、`match_rule_version`、`identity_digest`、`gap_digest` 和 `file_digest`。外部识别摘要取扫描值、人工修正和本地辅助写入的自动值，不读取外部资料写入的自动值，摘要变化才使当前项重新排队。人工修正、来源开关、抑制标记、扫描结束和过期重试会改变相应摘要。

`GET /api/v1/admin/curation/classifications` 返回 `{versions:{local_rule,external_match_rule},releases}`，`releases` 按可见发行统计整理分类。分类和摘要用于稳定判定、队列优先级和升级重查，接口不把内部哈希暴露给普通曲库读取。

## 3. 写入、自动层和外部内容

### 3.1 自动字段层

`release_auto_values` 以 `(release_id, field)` 保存自动值，字段为 `title`、`album_artist`、`year`、`genre`、`label`、`catalog_number`、`edition` 和 `barcode`，包含 `mode`、`text_value`、`year_value`、`list_value`、`operation_id`、`writer_level` 和可空 `scan_basis`。`mode` 为 `set` 或 `null`，标题和专辑艺人不能为 `null`，年份范围 1–9999，流派列表 1–20 项，其余文本非空且最多 4096 字节。`release_effective` 按人工修正、自动值、扫描值取第一个存在的值，明确设空不会继续向下取。`releases.auto_revision` 每次自动写入和撤销加一，扫描不写入。自动层和人工修正分别使用自己的操作记录和版本号。

`curation.Writer.Apply` 是进程内写入口。`Source.Kind` 为 `source` 或 `model`，`Provider` 符合 `^[a-z0-9._:-]{1,64}$`，`Basis` 最多 500 字节。请求含 `ReleaseID`、至少一个字段意图、可选 `BatchID`、幂等 `Key`、`RequestID` 和 `ConfirmedBy`。事务先锁幂等键、目录和发行，再核对总开关、开启者和批次，逐字段处理抑制和值相同情况，写自动层并刷新艺人投影、搜索值和操作记录。相同键和内容重放原结果。本地辅助写入或带 `ExpectedRevision` 的写入遇到人工修正返回 `409 manual_override`。

自动写入不改文件，不写人工修正和 `edit_revision`。已存在人工修正时仍可写入自动层，清除人工修正后显示自动值。自动操作类型为 `release_auto_correction`，来源记录为 `origin={source:"curation",provider_kind,provider,basis}`。

`Apply` 的事务锁顺序为幂等键、目录写锁、发行行。事务外读取开启者作为 `Spec.ActorID`，锁内以 `FOR SHARE` 重新读取设置并比较开启者。总开关关闭或开启者失效返回 `409 curation_disabled`，批次不存在或已关闭返回 `409 batch_closed`。每个字段分别过滤 `suppressed` 和 `unchanged`，返回 `Applied{OperationID,Written,Skipped}`。过滤后为空返回 `409 nothing_to_apply`，不产生操作记录。操作前后态同时记录自动值、写入者和当时有效值，重放依赖操作类型、目标、规范化字段意图和 provider 的指纹。

单条自动撤销获取目录写锁和发行行锁，不要求发行可见。原操作写入的每一格都仍指向该操作时，逐格恢复前态和原写入者，自动版本加一并刷新派生投影，否则整条返回 `409 revert_conflict`。撤销响应的 `revision` 读取发行当前人工版本。撤销本身不是可再次撤销的候选，描述中的 `revertible` 由未撤销、非撤销操作和所有格仍归属原操作共同决定。

### 3.2 对应、封面和歌词

`external_links` 保存 MusicBrainz 的发行、发行组、录音和艺人对应。唯一键为 `(source, object_type, object_id)`，移动、重扫和认领不删除对应。生产代码可通过 `Writer.ApplyLinks` 及对应流程写入；`external_links` 还保存 Wikidata 艺人和 `release_group_match`。

`ApplyArtwork` 和 `ApplyLyrics` 只补空缺。封面存 `release_external_artworks`，歌词存 `track_external_lyrics`。本地封面和可显示本地歌词优先，外部内容只在本地缺失时使用。外部内容已有、对应抑制命中或本地已有时返回 `409 nothing_to_apply`。封面数据不超过 16 MiB 且格式为 JPEG、PNG、WebP 或 GIF。歌词规范化后须有可显示行且不超过 64 KiB。封面文件和本地封面使用同一内容寻址目录，并以存储键事务锁保证写入和删除互斥。

两个进程内请求的字段为 `ArtworkRequest{ReleaseID,Data,Source,ExternalID,BatchID,Key,RequestID}` 和 `LyricsRequest{TrackID,Text,Source,ExternalID,BatchID,Key,RequestID}`。返回 `ExternalApplied{OperationID,Skipped}`，`Skipped` 为 `local_present`、`occupied` 或 `suppressed`。艺人图片资料写入另可返回 `license_missing`。封面来源的 `Provider` 允许普通源标识或 `plugin:<id>`，长度和字符按实现校验。歌词操作的目标记录为曲目当时所在的发行。

### 3.3 撤销与不再提出

自动字段、对应、封面和歌词均通过治理操作撤销。自动字段只有当当前自动层仍由原操作写入时才整条恢复，否则返回 `409 revert_conflict`。撤销写入 `curation_suppressions`，键由目标、主题和规范化值哈希组成。清除抑制后同值可再次写入。按批次或来源撤销每次最多 100 条，使用 `(created_at,id)` 倒序游标和相同幂等键续做。

### 3.4 曲目字段自动层

`track_auto_values` 以 `(track_id,field)` 为主键，只允许 `title` 和 `artist`，`mode` 固定为 `set`，文本非空且不超过 4096 字节。`track_effective` 按人工曲目修正、曲目自动值、扫描值取第一个存在的值。曲目自动操作类型为 `track_auto_correction`，目标操作记录仍挂在所属发行，`tracks.edit_revision` 和发行的 `auto_revision` 分开维护。

`Writer.ApplyTrack` 接受 `TrackRequest{TrackID,ReleaseID,ExpectedRevision,ArtistCredit,Fields,Source,BatchID,Key,RequestID,OnlyEmpty,ExpectedScan,ConversationID,ConfirmedBy}`。事务锁顺序为幂等键、目录写锁、发行行、曲目行。曲目必须仍在库中，指定发行必须仍归属该曲目，否则 `409 item_stale`。人工版本变化返回 `409 revision_conflict`。`OnlyEmpty` 遇人工行或自动行记 `occupied`，相同值记 `unchanged`，抑制标记命中记 `suppressed`，全部过滤返回 `409 nothing_to_apply`。自动写入刷新曲目搜索和艺人署名投影。单条撤销整条恢复每个字段的旧自动值和原写入者，任何字段已被覆盖或移除返回 `409 revert_conflict`。

程序只在发行匹配后补占位或文件名推断的曲名和曲目艺人。按已配对的录音编号读取 MusicBrainz 详情，字段补全使用 `OnlyEmpty=true` 和由代次及曲目标识派生的幂等键。无候选记 `none`，外部 404 记 `unavailable`，暂时错误保留原状态。补上曲目艺人后重新计算歌词查询摘要。

### 3.5 写入级别与扫描依据作废

自动写入级别按来源声明，现行级别为程序外部资料 `4`、模型外部资料 `3`、模型参考值 `2`、模型本地辅助 `1`。同级或更高级别可替换低级别值，低级别字段返回 `skipped[field]="lower_priority"`。自动值保存 `writer_level` 和对应扫描字段的 `scan_basis`。扫描不直接改自动层。整理读取时发现当前扫描值与 `scan_basis` 不同，写 `release_auto_invalidation` 或 `track_auto_invalidation` 操作，使有效值回到扫描值。作废不写抑制标记，也不触发外部重新识别。撤销作废前重新核对自动值为空和扫描值仍相同，恢复原值、级别和写入者。

### 3.6 署名拆分、艺人对应和自动合并

`catalog.LoadCurationArtistCredits` 读取人工修正或扫描层的 `release_artist`、`album_artist` 和 `track_artist`。存在 `local_credit_parts` 时读取本地辅助自动层，并返回作曲、演奏和指挥署名；这些值不作为外部识别证据。程序按位置、规范化名字或同位 MusicBrainz MBID 对齐本地和外部署名。数量不等、公共 `Various Artists` 或 `[unknown]` MBID、外部 MBID 重复或同一身份出现多个 MBID 时跳过该组。通过的拆分写 `release_match_credit_parts`，方法为 `split_credit`，每组独立操作并支持撤销。

`Writer.MergeArtists` 只接受 MusicBrainz 程序来源。它锁目录、按 UUID 排序的两方艺人行及两方的对应闭包，双方都必须有未撤销的同一 MBID 对应，闭包内不能有其他 MBID。保留有效署名条数较多者，平局保留 UUID 较小者。无强证据返回 `409 no_strong_evidence`，已重定向返回 `409 artist_redirected`，人工结构返回 `409 manual_structure`，已有合并标记返回 `409 nothing_to_apply`，目标艺人相同返回 `409 same_artist`。撤销自动合并同时恢复身份并写 `subject=artist_merge` 的不再提出标记。

已匹配发行的艺人回填只按已有发行或录音对应执行一次，结果 `artist_links` 为 `done`、`none` 或 `unavailable`。回填复核输入摘要、规则版本和代次，不改变匹配次数。身份对应建立后，艺人资料投影才进入资料队列。

### 3.7 同一外部发行的整张并入

程序在当前 MusicBrainz 发行对应、字段补全和归组完成后，通过 `ObjectsLinkedTo` 找到同一外部发行的其他本地发行。以最小 `(disc_number,position)`、创建时间和 UUID 选目标，双方录音对应集合必须非空且不相交。仅在模型已配置时创建 `release_merge` 待定项，摘要包含双方发行摘要和对应操作标识。多于两张时同一目标按候选顺序排队。

`Writer.MergeReleases` 只接受 `Source.Kind="model"`，锁目录和双方发行，重新核对待定项摘要、对应佐证、发行可见性和治理令牌。程序也可经 `MergeProgramEdition` 直接写入 `release_auto_merge`。成功写 `release_auto_merge`，保存 `operations_release_targets`、来源、会话和批次，返回 `{operation_id,release_id}`。缺少同一发行对应或录音集合相交返回 `409 no_strong_evidence`，人工结构返回 `409 manual_structure`，待定项变化返回 `409 item_stale`，已并入、形成链或超过大小限制分别返回 `release_merged`、`merge_chain`、`release_too_large`，合并目标变化、超出批次或例外待定项陈旧分别返回 `merge_changed`、`out_of_batch`、`exception_stale`。撤销恢复 `release_merges`、媒体来源和操作快照，并写一个归并抑制标记。程序只做并入，不做移出。

### 3.8 外部资料读取和文件生命周期

外部封面读取连接 `release_external_artworks`，本地封面存在时不连接外部行。外部歌词仅在本地歌词无法解析出可显示行时读取，读取来源标记为 `online`。搜索、最近添加、播放历史、待处理问题和图片接口共同使用本地优先结果。扫描、重扫、改名、移动和认领不写两张外部表。外部封面文件丢失时读取仍返回外部行，下一次写入可替换该行。

封面写入先 `Store.Describe`，在事务内获取存储键锁并落盘，再插入外部表。事务回滚或执行器失败时 `Discard` 已落盘文件。撤销提交后用 `AfterCommit` 再次统计本地封面、外部封面和艺人图片的引用，只有零引用才删除文件。封面或歌词任一抑制标记被清除时，同一事务删除同目标、同操作且非空的配对标记。歌词操作记录曲目当时所在的发行，曲目迁移后按新发行筛选时不可见。

### 3.9 三层隔离和有效读取

自动层执行器和撤销只写自动表及 `auto_revision`，人工执行器和撤销只写人工表及 `edit_revision`，扫描语句不写两层。认领、改名和移动不搬自动层。人工修正优先，其次自动层，最后扫描层。`releasesql.FieldIssueRows` 在字段有人工修正或自动值时不计为“需要关注”，自动值撤销后重新计入。`catalog.LoadReleaseCredits` 使用有效专辑艺人替换专辑级署名。

自动专辑艺人生成 `artist_credits.layer='auto'` 和 `artist_names.origin='auto'` 的事实。有效署名层按 `correction`、`auto`、`scan` 依次选择。自动值为合辑占位艺人时，发行级有效署名为空。管家读取和审查证据读取视图的有效值，管家写工具只写人工层。自动值与操作记录都不影响已有人工撤销的版本判断。

### 3.10 开启者和源闸门

打开总开关的管理员 ID 写在 `curation_settings.enabled_by` 和批次 `actor_id`，不创建系统用户。写入事务用 `FOR SHARE` 锁设置行并以 `identity.IsEnabledAdmin` 检查开启者仍启用。开启者失效或事务外读取的 ID 与锁内不同均返回 `409 curation_disabled`。当前管理员配置没有停用最后一位管理员的入口，因此接口只报告 `enabler_unavailable`，不提供接手流程。

后台源调用都经过 `curation.Sources`。源 `Check` 在事务内读取总开关、开启者和源设置，随后在缓存读取前、排队后和每次出站前再次检查。总开关或开启者失效返回 `curation_disabled`，源开关关闭返回 `source_disabled`。闸门不返回缓存结果，也不调用适配器。管家对话的 MusicBrainz 工具直接持有适配器，不经过后台闸门。

## 4. 出错、限流与队列

429 和 503 请求在 10 秒内最多重试 5 次并遵从 `Retry-After`。来源连续失败从 5 秒起倍增，成功后清零。`Retry-After` 较长时等待可超过 180 秒。来源恢复时从失败步骤继续，前面结果使用缓存。专属于一张专辑的解析或声纹错误前两次间隔 5 秒、10 秒，第三次状态为 `query_error`。移除来源失败后的额外放慢间隔。

识别、封面、歌词、艺人资料和本地声纹各有一条队列，每条一次处理一项并行运行。识别一次处理一张专辑。来源闸门在排队后、缓存读取前和每次重试前检查总开关、开启者和源开关。总开关关闭、开启者失效或源关闭时不出站，也不返回缓存结果。

后台整理运行状态包括 `idle`、`running`、`disabled`、`paused` 和 `waiting_for_scan`。扫描进行中整理进入 `waiting_for_scan`。管理员可停止模型队列，已发出的请求按当前项完成后收尾。单项输入变化会使旧待定项失效。

### 4.1 AcoustID 与声学指纹

`track_acoustic_fingerprints` 以 `track_id` 为主键，保存 `pipeline_version`、`content_fingerprint`、`source_size`、`source_modified_at`、CUE 起止帧、`status`（`done` 或 `failed`）、`fingerprint`、`duration_seconds`、`failure_category`（`too_short`、`decode_failed`、`timeout`）、`attempts` 和 `computed_at`。`curation_source_keys` 以 `(source,name)` 为主键，保存加密密文、末四位、`status`（`unchecked`、`valid`、`invalid`）、更新者和时间。

没有标签编号、条码、目录号和 ISRC 的项先进入本地声纹队列。其余项走到声纹步骤时按曲目缓存。缓存有效要求管线版本、内容指纹、大小、微秒修改时间和 CUE 区间全部相同。音频分析使用已打开的安全句柄，ffmpeg 读前 120 秒、11025 Hz、单声道 s16le，单曲 60 秒超时。向 AcoustID 发送整首或 CUE 区间的真实时长。精确时长小于 10 秒记 `too_short`，永久解码失败和超时保留诊断，源读取失败、文件变化和未知时长不写缓存。

AcoustID 来源只有工具可用、密钥填写且密钥状态有效时才 active。源状态依次可能是 `curation_disabled`、`enabler_unavailable`、`source_disabled`、`fingerprint_unavailable`、`bundled_key_rejected`、`key_missing` 或 `key_invalid`。出站成功才把本次使用且密文未变化的 `unchecked` 密钥改为 `valid`。无效密钥按 compare-and-set 标记为 `invalid`，缓存命中不验证密钥。AcoustID 请求串行，最小间隔 334 毫秒，约每秒 3 次。429/503 和上游错误码按来源合同重试，声纹查询失败不会改写发行结论。

声纹结果按曲数、碟数、录音 ID、分数和时长过滤。专辑需所有碟曲数一致、可比较曲目达到一半且没有 `acoustid_mismatch`、`acoustid_sparse`、`acoustid_spread`、`too_many` 或 `ambiguous` 冲突，得分低于 0.9 的匹配丢弃。单曲只接受唯一录音、标题相等和时长在 `max(3 秒,1%)` 容差内。声纹步骤提前查询后，识别队列直接消费缓存结果。规则版本升级只重开曾因声纹不可用而跳过且仍为遗留 `no_match` 或 `uncertain` 的发行。指纹计算或本地读取失败按专辑专属错误计次。

### 4.2 队列取项、稳定确认和中断恢复

识别、封面、歌词、艺人资料和声纹各有一条队列，队列标识固定为 `release`、`artwork`、`lyrics`、`artist_media`、`fingerprint`。每条队列一次处理一项，彼此并行。优先级依次为手动触发、新入库、可检索编号、已有缺口和本地信息完整项，同档按入库时间倒序。封面和艺人资料只有识别建立对应后才进入队列，歌词不依赖外部对应。

候选写入时保存发行成员签名和 `releases.curation_changed_at`。最近一次成功扫描的签名与上次一致且最后变化满 60 秒才稳定。确认扫描按发行成员的实际目录合并读取，同一变化窗口只领取一次。失败、取消或局部覆盖只能使确认失效，不能授予稳定资格。整理程序在取得稳定输入后重新计算 `file_digest` 和输入摘要，摘要变化则结束旧项并按新代次入队。

程序批次首次写入时打开，满 500 条、运行 1 小时、轮次结束、暂停或关闭时结束。空批次删除。每一步的幂等键由目标、主题、规则版本、代次和步骤派生，重启后回放原结果。总开关关闭、开启者失效、暂停或扫描运行时不读取证据、不出站。

### 4.3 来源退避和进度字段

各来源独立限速。429 和 503 请求在 10 秒内最多重试 5 次并遵循 `Retry-After`。其他可重试错误在队列路径立即暂停来源。来源连续失败按 5、10、20、40、80、160、180 秒暂停，随后按 `Retry-After` 等待，成功一次清零。来源恢复后从记录的失败步骤继续，前序结果走缓存。专属于一张发行的解析、参数拒绝或本地声纹错误按项计次，前两次间隔 5、10 秒，第三次状态为 `query_error`。来源等待不增加发行失败次数。

`GET /api/v1/admin/curation/progress` 的 `queues` 固定五项，每项 `id`、`state`、`remaining`、`ready`、`successful_requests_per_second`、`completed_items_per_second`、`eta_seconds`、`pause_reason`、`source`、`retry_at`。队列状态为 `running`、`idle`、`paused`、`disabled`、`unavailable` 或 `waiting_for_scan`。暂停原因可为 `disabled`、`paused`、`waiting_for_scan`、`waiting_for_stability`、`waiting_for_fingerprint`、`source_disabled`、`rate_limited`、`source_unavailable`、`invalid_response`、`invalid_query` 或 `fingerprint_failed`。声纹预查的速度统计只计实际 AcoustID 请求。

## 5. 模型的三件事与调用规则

模型在整理链只承担三类工作。

1. 本地辅助。判断旧编码、文字乱码、文件名标题、艺人拆分和归组。模型不联网，输出分为文字、编码、拆名和归组四类，由程序按固定规则逐项核对。文字必须来自同一目录成员的原始标签或路径，编码必须为整张可读取的候选编码，拆名必须按原分隔符重建署名，归组必须完整覆盖目录成员。文字和编码写入自动层最低级别，拆名写入 `local_credit_parts`，归组写入可撤销的 `local_model_groupings` 和 `local_model_grouping` 操作。
2. 识别判断。一次调用提交候选线索和本地证据，由程序使用 MusicBrainz 闸门查询、结构核对和写入。模型不自己查 MusicBrainz，也不直接写入外部事实。
3. 名字差异与艺人身份。对上 MusicBrainz 后比较名字差异，或在手动艺人联网功能中提出 MusicBrainz 艺人候选。结构核对通过才写对应，不能核对的候选进入人工确认。

模型不进行巡检、独立审查或整理链内网页联网。模型未配置时程序照常执行可确定步骤，需要选择时保持原样。

模型错误分为配置缺失、额度达到上限、临时提供方错误、请求被拒和空返回五类。配置缺失不创建会话，额度达到上限保留待定项并记录下一次可用时间，临时错误暂停整个模型段，请求被拒的单项搁置，空返回按项规则结束或重试但不写成未匹配。请求被拒的单项搁置，`temporary` 暂停整个模型段。模型日用量由 `curation_model_usage` 合并记录，单次请求默认最多发送 128000 token，设置值允许 1–10¹²。艺人联网单项最多 10 次工具调用和 50000 token。每次新项和重试前检查共享额度，实际 usage 通过 `RecordItemUsage` 记账。

本地辅助待定项状态为 `waiting_model`、`waiting_route`、`in_progress`、`shelved` 或已结束状态。提交时复核规则版本、会话、输入摘要和扫描稳定性，过期项不写入。

本地辅助的触发情况固定为七类，原因码和输入如下。

| 情况 | 原因码 |
| --- | --- |
| 无名字标签或只有文件名 | `filename_only` |
| 旧编码存在多种可解读方式 | `legacy_encoding` |
| 署名含 `&` 或逗号，需要判断拆名 | `artist_split` |
| 同目录专辑名不一致且曲号不连续 | `mixed_directory` |
| 无法识别的碟片子目录 | `unknown_disc_directory` |
| 缺曲号或多碟迹象下缺碟号 | `missing_position`、`missing_disc` |
| 复合专辑名中混有艺人名 | `compound_title`、`missing_artist` |

扫描还会把曲号和碟号推断冲突统一记为对应的 `missing_position` 或 `missing_disc`。每个目录只建立一个 `local_cleanup` 项，目录内的多张专辑一起提供证据。目录项只有在 `evidence.release_ids` 中的全部发行进入 `curation_stable_releases` 后才能领取。同一目录的本地辅助项处于 `waiting_model`、`waiting_route` 或 `in_progress` 时，目录内全部发行都不进入识别队列。目录项结案后，这些发行一起放行并分别送入 MusicBrainz 识别队列。无模型时七类仍建立本地辅助项，程序不等待这些项而继续可确定的下一步，待模型配置后再处理。

### 5.1 模型状态、额度和批次

模型进度 `state` 为 `idle`、`running`、`paused`、`disabled`、`waiting_for_scan`、`limit_reached` 或 `unavailable`，不会产生 `waiting_review`。`pause_kind` 的取值与错误类别一一对应。

| 错误类别 | `pause_kind` | 后续处理 |
| --- | --- | --- |
| 配置或模型能力拒绝 | `configuration` | 暂停到配置修改或手动探测成功 |
| 上游余额或账户额度用尽 | `provider_quota` | 暂停，每小时最多一次小探测 |
| 临时 429、5xx、超时或断流 | `temporary` | 单步短重试后按 5 秒起倍增，`Retry-After` 较长时可超过 180 秒 |
| 回复为空 | `empty_replies` | 单项按 5、10 秒重试，三次后记 `no_result`，十项连续空答暂停 |
| 请求被拒或 JSON 字段无效 | 无独立 `pause_kind`，被拒单项搁置，十项相同拒绝后为 `configuration` | 整批 HTTP 400 拆成单项，单项记搁置原因 |

模型段还返回 `configured`、`reason_detail`、`retry_at`、`can_retry`、`context_hint`、`unavailable_reason`、`model`、`today`、`daily_token_limit` 和 `items`。`unavailable_reason` 包括 `not_configured`、`key_needs_reentry`、`tool_unsupported`、`tool_unsupported_override`、`enabler_unavailable`、`source_disabled`、`configuration`、`provider_quota`、`empty_replies` 和 `provider_error`，其中 `configuration`、`provider_quota`、`empty_replies` 是模型暂停时带出的暂停类别。暂停类别 `temporary` 对外写成 `provider_error`，达到日上限时 `pause_kind` 为 `daily_limit`。整理和手动艺人联网共用 `curation_model_usage` 的服务器本地日账。主模型、模型摘要和艺人联网都计入，管家普通对话不计入。`model_concurrency` 默认 4，允许 1–16。调低并发时在途项继续，运行数降到上限以下才补位。

调度顺序固定为本地辅助、识别判断、名字差异与艺人身份，其中手动艺人联网项优先于其他项，`release_merge` 与识别判断同属该档。每批最多领取五个到期项。完整请求输入按 JSON 估算，`max_request_tokens` 默认 128000，允许 1–10¹²。输出预算为 `min(8192,max(1,floor(预算/8)))`。放不下的项留到下一批，单项继续超限则逐步保留最多三个候选并搁置为内容过长。模型请求前检查日额度，每次实际使用按上游报告记账，艺人联网每位最多 10 次工具调用和 50000 token。

请求成功决定或明确的 `undetermined` 才增加项级回答次数。失败、来源等待和缓存命中不增加次数。空返回只累计空答次数，5 秒、10 秒后重试，第三次结案为模型没有结果，不写成 `unmatched`。连续十个不同项被同一拒绝或空答时暂停当前模型段。模型批次 400 错误先拆成单项发送，单项仍被拒才按安全原因搁置。日额度达到上限时保留项和计数，进入下一日或提高额度后继续。

### 5.2 识别判断

识别判断只在程序完成 MusicBrainz 查询后对 `unmatched` 或 `conflict` 项建立 `release_match` 或 `recording_match` 待定项。一次模型调用只给出候选线索，程序再查询和结构核对。线索可为候选标题、艺人、条码、目录号、版本词和媒体类型，不能提交发行编号、录音编号或 ISRC。每项最多 6 次实际 HTTP 查询，游标和预算保存在 `curation_work_states.result.judge`，缓存命中不扣次数。候选列表按外部 ID 排序，单步最多保存 100 条，截断或候选过多直接交人工。已存在硬冲突时停在当前步骤，不继续更弱线索。

短专辑最多四首时，名字线索命中唯一外部候选后可由独立本地证据补足标题差异。模型提供的标题、艺人和编号必须分别命中外部版本、本地艺人、所有碟曲目位置、真实曲名和可比时长，并通过原有硬反证检查。无效响应连续三次后将该项记为 `query_error` 并保留原因。成功候选由程序保存 `snapshot_id`、`candidates` 和 `curation_release_match_results`，模型不会保存 `selection` 或直接建立对应；选择由管理员接口完成。

### 5.3 名字差异与艺人身份

`verify_name` 的决定为 `typo`、`keep` 或 `undetermined`。只有已对上 MusicBrainz 且本地名字与外部名字为同一文字、相差 1–2 处时才建立核对项，版本后缀和 `feat.` 不计入差异。`typo` 采用已核实的 MusicBrainz 写法并按核对等级写自动层（第 3 级或第 2 级），不写 `curation_match_names` 或人工层；`keep` 保留本地原文，`undetermined` 不写入。相同无序名字对只询问一次，否定结果永久写入 `curation_artist_negative_pairs` 并阻止重复询问。艺人身份 `same` 才建立 MusicBrainz 艺人对应或合并，`different` 写否定对，`undetermined` 保持原样。独立审查不再介入这些决定。

## 6. 整理艺人

艺人身份联网是管理员手动触发的独立功能，不依赖自动整理总开关，也不创建专辑整理项。单艺人入口为 `GET /api/v1/artists/{id}/web-search` 和 `POST /api/v1/artists/{id}/web-search`。全量队列为 `GET /api/v1/admin/curation/artist-web-search`、`POST /api/v1/admin/curation/artist-web-search` 和 `DELETE /api/v1/admin/curation/artist-web-search`。

队列状态为 `idle`、`running` 或 `stopped`，单项状态为 `none`、`queued`、`running`、`shelved` 或 `waiting_human`。输入摘要包含艺人有效姓名、别名、发行结构、文件摘要、规则版本和来源配置。摘要未变化且已有结论或候选时不重复入队。停止只阻止尚未发出的项，恢复继续当前代次。

模型只能提交 `source=musicbrainz`、合法 MBID、候选名称、说明和 1–5 个引用 URL。程序读取候选艺人及其发行，用曲数、碟曲数、曲序和时长与本地发行逐张核对。结构吻合时通过现有治理入口写入艺人、发行和曲目对应，并唤醒图片和简介投影。没有结构吻合时写入 `curation_model_exceptions` 的 `open` 候选，由管理员通过 `POST /api/v1/admin/curation/exceptions/{id}/accept` 或 `/decline` 决定。候选过期时返回 `exception_stale`。

艺人已存在对应、已重定向或未配置模型与网页来源时，单艺人接口返回相应冲突。艺人图片和简介只在 MusicBrainz 对应建立后由资料队列补齐，排在专辑识别之后。

### 6.1 艺人联网队列字段

`curation_artist_web_queue` 是固定单行表，`state` 为 `idle`、`running` 或 `stopped`，保存 `generation`、`actor_id`、`started_at` 和 `stopped_at`。艺人待定项 kind 为 `artist_identity`，保存 `artist_web_generation`、`artist_web_actor`、`artist_web_suspended` 和 `artist_web_priority`，并按代次、优先级和艺人标识建立索引。艺人状态响应为 `{configured,can_start,can_stop,queue,usage,state,candidates}`。`queue` 含 `generation`、`position`、`total`，`usage` 含 `tool_calls`、`tokens`、`tool_call_limit=10` 和 `token_limit=50000`。候选项含 `exception_id`、`source`、`external_id`、`name`、可空 `description`、`note` 和 `citations`。

单艺人 POST 未配置网页搜索返回 `409 search_unconfigured`，未配置模型返回 `409 model_not_configured`，已有对应返回 `409 artist_already_linked`，已重定向返回 `409 artist_redirected`，不在主署名范围返回 `409 artist_out_of_scope`。重复 POST 复用当前代次，不创建重复项。全量队列 POST 返回 `202` 和 `{state,generation,total,queued,running,processed,configured,can_start,can_stop,retry_at}`，DELETE 将当前代次处于 `waiting_model`、`waiting_route` 或 `in_progress` 的项挂起并标记 `artist_web_suspended=true`，GET 返回队列统计。接受或否掉只接受手动队列 `resolve_artist_identity` 候选，其他项返回 `404`。艺人候选接受和否掉沿用 `POST /api/v1/admin/curation/exceptions/{id}/accept` 与 `/decline`，接受响应为 `{exception,operation_ids}`，否掉响应为 `{exception}`。

### 6.2 艺人资料投影

已建立 MusicBrainz 艺人对应后，`artist_media` 队列按艺人家族的有效对应查询图片和简介。人工资料优先于自动资料，家族中同级或更高等级的自动资料会使当前项返回 `occupied`。资料操作目标为艺人，表为 `artist_external_artworks` 和 `artist_external_bios`，图片与简介分别使用 `artist_external_artwork` 和 `artist_external_bio`，保存 `basis_source`、`basis_id`、许可和资源或文本快照。图片大小和格式先由受管图片存储校验，简介最多 4000 个 Unicode 字符。来源条目被抑制时返回 `suppressed`，缺少许可名时返回 `license_missing`，通过 `nothing_to_apply` 结束并返回跳过原因。

资料读取按来源等级和可见身份家族合并。图片源依次为 Fanart.tv、TheAudioDB、Commons、插件，简介源依次为中文维基百科、TheAudioDB 中文、英文维基百科、TheAudioDB 英文、插件。资料队列也处理只有 Wikidata 对应的艺人以及插件来源。艺人图片和简介操作支持单条、按批次和按来源撤销，撤销标记主题为 `artist_image` 或 `artist_bio`，标记值为 `{source,external_id}`。提交后清理图片前按内容寻址存储键统计引用。

## 7. 进度、存储与接口

### 7.1 表和源闸门

整理使用 `curation_settings`、`curation_batches`、`curation_work_states`、`curation_pending_items`、`curation_model_exceptions`、`curation_model_usage`、`curation_model_state`、`curation_suppressions`、`external_links`、`release_auto_values`、`release_external_artworks`、`track_external_lyrics`、`artist_external_artworks` 和 `artist_external_bios`。外部源设置在 `curation_source_settings`，源声明由应用装配注册。MusicBrainz、Cover Art Archive、LRCLIB 和 AcoustID 按注册顺序提供能力。AcoustID 还要求指纹工具和密钥可用。治理操作类型包括 `release_auto_correction`、`track_auto_correction`、`release_external_artwork`、`track_external_lyrics`、`external_link`、`release_group_change`、`release_merge`、`release_auto_merge`、`artist_merge`、`artist_link`、`artist_manual_image`、`artist_manual_bio`、`artist_external_artwork`、`artist_external_bio` 和 `local_model_grouping`。

源接口返回 `enabled`、`active` 和 `inactive_reason`。`inactive_reason` 还包括 `fingerprint_unavailable`、`bundled_key_rejected`、`search_unconfigured`、`key_missing` 和 `key_invalid`。后台调用统一经 `curation.Sources`，管家对话里的 MusicBrainz 工具直接使用适配器。

主要表的字段如下。

| 表 | 字段和约束 |
| --- | --- |
| `release_auto_values` | `(release_id,field)` 主键，八个字段的 `mode`、`text_value`、`year_value`、`list_value`、`operation_id`、`writer_level` 和 `scan_basis`。标题和专辑艺人不能设空 |
| `releases` | `auto_revision BIGINT NOT NULL DEFAULT 1`、`curation_attempted_change_at` |
| `operations` | `batch_id UUID` 可空，`origin` 含 `provider_kind`、`provider`、`basis`。自动字段类型为 `release_auto_correction` |
| `curation_batches` | `id`、`tier`（`program` 或 `model`）、`opened_at`、`closed_at`、`actor_id`。详情实时统计 `operations` 和 `reverted` |
| `curation_suppressions` | `id`、`target_type`、`target_id`、`subject`、`value_hash`、展示用 `value`、`operation_id`、`created_by`、`created_at`，唯一键为目标、主题和值哈希 |
| `curation_settings` | 单行 `id=1`，`enabled`、`enabled_by`（`{id,username}`）、`enabled_at`、`updated_at`、`paused`、`model`、`model_daily_token_limit`、`max_request_tokens`、`model_concurrency`。每日上限默认 10,000,000。 |
| `curation_source_settings` | `source` 主键、`enabled`、`updated_by`、`updated_at` |
| `external_links` | `id`、`source`、`object_type`、`object_id`、`external_id`、`method`、`confidence`、可空 `operation_id`、`created_at`。唯一键为 `(source,object_type,object_id)` |
| `release_external_artworks` | `release_id` 主键、`content_hash`、`mime_type`、`width`、`height`、`storage_key`、`source`、`external_id`、`operation_id`、`created_at` |
| `track_external_lyrics` | `track_id` 主键、规范化 `lyrics`、`content_hash`、`source`、`external_id`、`operation_id`、`created_at` |
| `track_acoustic_fingerprints` | `track_id` 主键、`pipeline_version`、内容指纹、大小、微秒修改时间、CUE 起止帧、`status`、`fingerprint`、`duration_seconds`、`failure_category`、`attempts`、`computed_at` |
| `curation_source_keys` | `(source,name)` 主键、加密 `ciphertext`、`last_four`、`status`（`unchecked`、`valid`、`invalid`）、`updated_by`、`updated_at` |
| `release_merges` | `source_release_id` 主键、`target_release_id`、`operation_id`、`merged_at`，源与目标不能相同 |
| `operations_release_targets` | `(operation_id,release_id)` 主键，为整张并入操作保存所有涉及发行 |
| `release_match_credit_parts` | 发行、曲目、角色、位置、本地文本、姓名、MBID、对应和依据操作，删除触发器清理最后一条拆名对应 |
| `local_credit_parts` | `operation_id`、目标、角色、来源片段、原文、拆分段和连接词，用于本地辅助拆名 |
| `local_model_groupings` | `root_id`、`directory_key`、唯一 `operation_id`、源摘要、目录集合、分配 JSON 和其他路径 |
| `curation_release_match_results` | `(release_id,source)` 主键，状态、查询时间、原因、冲突、`snapshot_id`、候选和选择；`snapshot_id`、候选和选择无值时分别为 `null`、空数组和 `null`。 |
| `curation_match_names` | 发行、目标类型、目标、字段、MusicBrainz 名称、外部编号、依据对应和依据操作 |
| `curation_artist_negative_pairs` | 无序名字对主键、原因、模型、`legacy`、决定时间 |
| `curation_model_state` | 单行配置状态，字段为 `config_key`、`model_key`、`pause_kind`、`reason_code`、`reason_detail`、`retry_at`、`backoff_seconds`、`streak_key`、`streak_items`、`tools_verified`。 |
| `curation_pending_items` | 目标、`kind`、`evidence`、`input_digest`、`status`、`attempts`、`next_attempt_at`、会话摘要、`review_retries`、`shelved_reason`、`match_rule_version`、`identity_digest`、`route`、`item_tool_calls`、`item_tokens`、`source_retry_count`、`claim_source_recovery_at`、`empty_count`（0–3）、`model_stop_code`、`single_request`、`reset_item_usage_on_claim`。艺人手动队列另有 `artist_web_generation`、`artist_web_priority`、`artist_web_suspended`、`artist_web_actor`。状态 CHECK 允许 `waiting_model`、`in_progress`、`resolved`、`shelved`、`waiting_route`，打开项唯一索引不含 `verify_*` 与 `name_patrol`。|
| `curation_artist_web_queue` | 单行 `id=true`、`state`（`idle`、`running`、`stopped`）、`generation`、`actor_id`、`started_at`、`stopped_at` |

进度响应的 `releases` 含 `total`、`processed`、`matched`、`unmatched`、`conflict`、`try_later`、`query_error`，`pending` 含 `waiting_model`。每条队列含 `id`、`state`、`remaining`、`ready`、速率、`eta_seconds`、`pause_reason`、`source` 和 `retry_at`。模型进度含运行数、并发数、配置状态、暂停原因、重试时间、模型名、日用量和 `waiting_model`、`in_progress`、`shelved` 计数，`waiting_route` 计入 `waiting_model`。

### 7.2 管理员接口

所有管理员写接口要求同源请求。主要接口如下。

| 方法和路径 | 当前行为 |
| --- | --- |
| `GET /api/v1/admin/curation/settings` | 返回 `enabled`、`enabled_by`（`{id,username}`）、`enabled_at`、`active`、`inactive_reason` |
| `PUT /api/v1/admin/curation/settings` | 设置 `{enabled}`，打开时记录开启者 |
| `GET /api/v1/admin/curation/progress` | 返回程序与模型进度 |
| `GET /api/v1/admin/curation/sources` | 按注册顺序返回源能力和状态 |
| `PUT /api/v1/admin/curation/sources/{id}` | 设置源 `{enabled}` |
| `PUT /api/v1/admin/curation/sources/{id}/keys/{name}` | 设置源密钥 |
| `DELETE /api/v1/admin/curation/sources/{id}/keys/{name}` | 删除源密钥 |
| `GET /api/v1/admin/curation/batches?page=&page_size=` | 倒序分页返回批次 |
| `GET /api/v1/admin/curation/batches/{id}` | 只返回批次摘要，不包含操作明细 |
| `POST /api/v1/admin/curation/reverts` | 按批次或来源批量撤销，带 `Idempotency-Key` 和游标 |
| `GET /api/v1/admin/curation/suppressions?release_id=&page=&page_size=` | 倒序分页返回抑制标记 |
| `DELETE /api/v1/admin/curation/suppressions/{id}` | 成功 `204`，未知标记 `404 not_found` |
| `GET /api/v1/admin/curation/classifications` | 返回发行分类和规则版本 |
| `GET /api/v1/admin/curation/conflicts` | 返回冲突分页 |
| `GET /api/v1/admin/curation/releases/{id}/match-results` | 返回识别候选和核对结果 |
| `POST /api/v1/admin/curation/releases/{id}/recheck` | 请求重新核对并返回 `202` |
| `POST /api/v1/admin/curation/releases/{id}/match-selection` | 管理员选择识别候选 |
| `POST /api/v1/admin/curation/releases/{id}/adopt-name` | 采用名称建议 |
| `POST /api/v1/admin/curation/model/retry` | 重试模型队列 |
| `POST /api/v1/admin/curation/exceptions/{id}/accept` | 接受艺人候选 |
| `POST /api/v1/admin/curation/exceptions/{id}/decline` | 否掉艺人候选 |

设置响应还包含 `model_concurrency`、`model`、`daily_token_limit`、`max_request_tokens` 和 `paused`，关闭状态的 `inactive_reason` 为 `disabled`。批次项目字段为 `{id,tier,opened_at,closed_at,operations,reverted}`。批量撤销请求为 `{scope:{kind:"batch",id}|{kind:"provider",provider},cursor}`，响应为 `{reverted,skipped:[{operation_id,code}],next_cursor}`。源项目字段为 `{id,name,capabilities,enabled,active,keys,requirements,upstreams,inactive_reason}`。抑制项目字段为 `{id,target_type,target_id,subject,value_hash,value,operation_id,created_by,created_at}`。

设置 PUT 接受 `enabled`、`paused`、`model`、`daily_token_limit`、`max_request_tokens` 和 `model_concurrency` 的部分更新，至少提供一个字段。`model` 最多 200 字节，空串表示使用开启者管家模型。`daily_token_limit` 接受 null 或 1–10¹² 的整数，`max_request_tokens` 必须为 1–10¹² 的整数，`model_concurrency` 只能为 1–16。仅从关闭到开启 `enabled` 时记录调用者并清除 `paused`，关闭不清除已有自动层。提高 `max_request_tokens` 会重新打开因 `content_too_long` 搁置的项，切换模型会重置已停下的项。模型 PUT 的字段变化不改变程序输入摘要。

`GET /api/v1/admin/curation/progress` 返回 `state`、`rule_version`、`releases`、`pending`、`batch`、`queues`、`model` 和 `last_pass_finished_at`。`model` 段包含 `running`、`concurrency`、`configured`、`pause_kind`、`reason_detail`、`retry_at`、`can_retry`、`context_hint`、`state`、`unavailable_reason`、`model`、`today`、`daily_token_limit` 和 `items`。`today` 含日期、输入 token 和输出 token。没有装配程序循环时程序 `state` 和队列状态为 `unavailable`。

后台整理会话的列表和详情使用管家会话接口，整理合同只约定会话 `kind=curation`、开启者、项计数和删除时的繁忙冲突。艺人图片、简介、链接等 HTTP 写接口属于 [艺人合同](./artist-contracts.md)。管家普通对话、网页工具和审查证据分别见 [管家运行时合同](./steward-runtime-contracts.md)、[管家联网合同](./steward-web-contracts.md) 和 [管家审查合同](./steward-review-contracts.md)，不在本合同重复定义。

修正接口 `GET /api/v1/releases/{id}/corrections` 和修正成功响应中的每个字段带 `auto`，其值为 `{mode,value?,operation_id,provider_kind,provider}` 或 `null`，`effective_value` 是人工、自动、扫描三层结果。发行详情和版本对比可带 `auto_fields`，仅列有自动值且没有人工修正的字段。

发行详情的识别投影总带 `musicbrainz_names` 数组，每项为 `{target_type,target_id,field,local_value,effective_value,musicbrainz_value,basis_operation_id,external_id,revision,can_adopt}`，无项目时为空数组。艺人对应投影带 `judged_by_model`，只有 `model_identity` 建立的对应才为真，并带对应操作 `operation_id`。

### 7.3 状态、错误码和测试锚点

自动整理错误码包括 `curation_disabled`、`curation_paused`、`scan_running`、`source_disabled`、`batch_closed`、`nothing_to_apply`、`revert_conflict`、`invalid_input`、`invalid_id`、`invalid_json`、`invalid_pagination`、`not_found`、`source_unavailable`、`database_unavailable`、`exception_stale`、`exception_decided`、`item_stale`、`insufficient_corroboration`、`revision_conflict`、`manual_override`、`out_of_batch`、`no_strong_evidence`、`manual_structure`、`artist_scope_too_large`、`release_merged`、`merge_chain`、`release_too_large`、`merge_changed`、`same_artist`、`not_unlisted`、`evidence_changed`、`artist_catalog_not_ready`、`contradictory_evidence`、`approval_required`、`key_missing`、`key_invalid`、`plugin_changed`、`search_unconfigured`、`artist_web_disabled`、`model_not_configured`、`artist_already_linked`、`artist_redirected` 和 `artist_out_of_scope`。外部封面和歌词会返回 `local_present`、`occupied` 或 `suppressed` 的跳过原因，艺人图片资料另可返回 `license_missing`。未登录、非管理员、跨源、分页和数据库错误沿用治理合同错误映射。

集成测试直接调用 `curation.Writer.Apply`、`ApplyArtwork`、`ApplyLyrics` 和撤销器；`match-selection` HTTP 接口也会写入第 4 级自动值和对应。固定样本回归覆盖扫描输入、查找顺序、结构判定、自动层优先级、撤销顺序、源闸门、模型额度、艺人身份和各管理员接口。全量真实轮不作为验收门槛。

### 7.4 测试锚点

| 断言范围 | 测试锚点 |
| --- | --- |
| 八个发行字段三层优先级、自动值和人工值隔离、重扫不改自动层、自动写入并发与撤销顺序 | `TestPostgreSQLCurationFieldPriority`、`TestPostgreSQLCurationRescan`、`TestPostgreSQLCurationIndependentRevisions`、`TestPostgreSQLCurationRevertOrder` |
| 曲目字段 `OnlyEmpty`、人工优先、自动撤销、按批和按来源撤销、发行与曲目版本独立 | `TestPostgreSQLTrackAutoApply`、`TestPostgreSQLTrackAutoRevert`、`TestPostgreSQLTrackAutoBatchRevert`、`TestPostgreSQLTrackAutoIndependentRevisions` |
| 外部对应、封面、歌词的读取优先、文件回滚、重扫保持和 `AfterCommit` 清理 | `TestPostgreSQLExternalArtworkPriority`、`TestPostgreSQLExternalArtworkRescan`、`TestPostgreSQLExternalArtworkApply`、`TestPostgreSQLExternalArtworkRevert`、`TestPostgreSQLExternalLyricsPriority`、`TestPostgreSQLExternalConcurrent` |
| 程序六步查询、单曲查询、相反证据、曲目数和时长边界、命中即停 | `TestAlbumCorroboration`、`TestAlbumMethodRequirements`、`TestSingleRules`、`TestAlbumContraryEvidence`、`TestFirstHitStopsLookup` |
| 识别结论每源一行、冲突分页、快照选择、幂等和陈旧项 | `TestPostgreSQLProgramMatchResultsAPI`、`TestPostgreSQLJudgeConflictSelectionRevalidatesSnapshot`、`TestPostgreSQLProgramModelMatchSameKeyKeepsNewerConclusion` |
| 强证据归组、同外部发行整张并入、链式和人工结构冲突、撤销抑制 | `TestPostgreSQLCurationCJoinGroup`、`TestPostgreSQLCurationReleaseMergeProposed`、`TestPostgreSQLCurationMergeBlocked`、`TestPostgreSQLCurationMergeRevert` |
| 艺人署名拆分、MusicBrainz 对应、同 MBID 自动合并和已匹配发行回填 | `TestPostgreSQLCurationArtistLinks`、`TestPostgreSQLCurationAutoMergeSameMusicBrainzArtist`、`TestPostgreSQLCurationArtistLinksBackfill` |
| AcoustID 请求、缓存有效性、密钥状态、源闸门、指纹工具不可用和升级重开 | `TestLookupFingerprintRequestMappingAndCache`、`TestLookupFingerprintErrors`、`TestPostgreSQLB3AcoustIDKeyAPI`、`TestPostgreSQLB3FingerprintToolUnavailable`、`TestPostgreSQLB3LegacyWorkReopenedOnce` |
| 本地辅助七类输入、目录级一次调用、文字编码拆名归组写入与撤销、目录内多张后续识别 | `TestPostgreSQLLocalAssistSevenReasons`、`TestPostgreSQLLocalAssistGroupingRevert`、`TestPostgreSQLLocalAssistBeforeMatch`、`TestPostgreSQLCurationModelRulesDirectoryAndPriority` |
| 模型五类错误、空答、分批、共享日账、六次识别预算、名字否定对和艺人联网额度 | `TestPostgreSQLCurationModelDailyLimit`、`TestPostgreSQLCurationModelRulesBatch400`、`TestPostgreSQLJudgeHTTPBudget`、`TestPostgreSQLJudgeNameKeepUncertainAndLateCorrection`、`TestPostgreSQLArtistWebSearchOrderedQueue` |
| 总开关、开启者、暂停、扫描、源开关、进度、接口样本、分页、回退迁移和过滤执行计划 | `TestPostgreSQLCurationEnabler`、`TestPostgreSQLCurationSourcesGate`、`TestPostgreSQLCurationCProgress`、`TestPostgreSQLAPIResponseSamples`、`TestPostgreSQLMigrationRollbackRoundTrip`、`TestPostgreSQLCurationFilterPlans` |
