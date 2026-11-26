# 现实曲库情形的覆盖审计（2026-11-26）

只读审计。逐条核对：现有样本与测试是否覆盖、当前代码的实际行为、是否与合同或 ADR 不符。路径相对 `backend/`。现有回归库 `internal/app/testdata/curation_regression/samples.json` 共 92 个样本，全部为单碟。

| # | 情形 | 覆盖 | 当前行为与定位 | 判断 |
| --- | --- | --- | --- | --- |
| 1 | 分碟子目录；同目录两碟无 DISCNUMBER、曲号从 1 重来 | 仅单元测试 `TestOrganizeStrictMultidisc*`、`TestOrganizeDiscDirectoryDecisionTable` 与 `TestPostgreSQLCurationModelPlan` | 连续碟目录合成多碟（`internal/catalog/ingest/organizer.go:311-389,746-748`，`discmarker.go:26`）；同目录 DISCNUMBER 分碟（`continuity.go:49`）；无碟号时两碟都落第 1 碟，`position_conflict`（`internal/curation/match_album.go:150-152`），模型提交被 `readMatch` 拒（`model_matcher.go:302-304`）；无标签切半目录碟数不等（`match_snapshot.go:113`） | 缺陷 |
| 2 | 不完整专辑 | 无 | 按总曲数排除（`match_snapshot.go:126`），模型提交 `!c.countEqual` 被拒（`model_matcher.go:305-307`），最终 `unlisted`；仅 `tag_id` 允许子集（`match_album.go:47,180`） | 能力缺口 |
| 3 | 本地多 bonus 曲 | `multi_edition`、stream-03/04、`TestPostgreSQL整理模型提交同组多版须有版本标识` | 外部有同曲数豪华版即对上（`match_album.go:375-388`）；只有标准版则 `unlisted` | 能力缺口 |
| 4 | 目录名或标签噪声 | 部分 | `folderSimplePattern`（`folder_metadata.go:44,116`）把“艺人 - 1997 - 专辑”解析为专辑“1997 - 专辑”；标签里的 `[FLAC]`、`24bit-96kHz` 算真实值，零候选放宽只去版本后缀（`match_search.go:312-318,379`） | 轻度 |
| 5 | GBK 乱码、ID3v1 | 无 | 编码字节 0 按 Latin-1 逐字节转（`internal/library/parser/mp3.go:336-343,397-401`），无乱码检测；不读 ID3v1（`fingerprint.go:277-292` 只剥离）；乱码算真实值，模型提交判 `tag_text_mismatch`（`model_verification.go:68-77`），本地整理不处理（`local_items.go:63`） | 缺陷 |
| 6 | 同一艺人同名专辑 | homonym-01/02、`TestPostgreSQL整理同名跨组用四个识别请求定案` | 读详情按时长区分（`match_snapshot.go:288-306`） | 已覆盖 |
| 7 | 同名单曲与专辑 | loose-01..08 | 有专辑标签时按 1 曲专辑匹配；外部单曲带 B 面对不上 | 归入 #2 |
| 8 | feat. / & / x / 、 合作署名 | 无 | 拆分规则 `metadata/artist.go:13,84-110`；匹配比较原串宽松键（`match_album.go:77-81`、`match.go:72-82`），“A、B”与“A & B”、ft. 与 feat. 不等；艺人对应要求人数相同（`artist_links.go:24`） | 轻度 |
| 9 | 合辑无 ALBUMARTIST | 仅单元测试 `TestApplyCompilation` | 归成一张，但专辑艺人取曲目艺人多数、同票按字母序（`organizer.go:117-123`，`compilation.go:6-26`）；整理证据要求每曲艺人全不同（`curation_evidence.go:535-551`） | 缺陷 |
| 10 | 同一专辑两份（FLAC/MP3） | 单元测试 | 各自对到同一外部发行，录音重叠不并入（`release_merge_evidence.go:141`） | 无缺陷，缺样本 |
| 11 | 混放散曲目录 | 单元测试 | 过半或曲号连续即整目录归一张（`organizer.go:812-830`），少数曲目被吞，曲数不等永远对不上 | 缺陷 |
| 12 | 同名现场版 | near-01/02（3 首） | 时长反证挡住；但发行组唯一级不看时长（`match_snapshot.go:270-272`），多于 4 首会直接建组级对应 | 风险 |
| 13 | 隐藏曲、黑胶抓轨 | 无 | 容差 max(3 秒, 1%)（`match.go:141-147`）；`A1` 解析为 0（`tags.go:35-38`）改按路径推断（`organizer.go:978-1011`）；双 LP 本地单碟对外部两碟永远对不上（`model_matcher.go:296`） | 缺陷 |
| 14 | 罗马字/原文/繁简 | alias、chinese 系列 | `textnorm.Text` 统一繁简假名；艺人未建对应时译名专辑被判 `tag_text_mismatch` | 轻度 |
| 15 | 占位标签 | poor_tags 仅空值 | 只认来源为空的“未知专辑/未知艺术家”（`curation_authenticity.go:27-30`）；“Unknown Artist”“Track 01”算真实值 | 缺陷 |
| 16 | 有声书、播客、白噪音 | 无 | 无类型过滤，照常走整条链，多半 `unlisted` | 缺样本 |
| 17 | 外部同名不同人 | 无 | 本地归一（合同 27.4）；`loadMatchArtistIDs`（`curation_evidence.go:554-602`）让第二张专辑带上第一人的外部编号，`arid:` 过滤（`match_search.go:54-66`）挡住 | 缺陷（部分为已知局限） |
| 18 | 模型非法 JSON、超时、5xx | `Test整理第一档选择严格限定已发送的候选`、`TestPostgreSQLCurationModelUnhandledThreeTimes` | 一项不合格整批被拒（`curation_model.go:212`），代码块包裹也被拒；整批各记一次尝试（`scheduler.go:515-518`），三次后 `unhandled` 搁置而非升级；超时与 5xx 不计次，退避 10 分钟到 6 小时（`scheduler.go:458-468,523-527`） | 轻度 |
| 19 | 第二档/联网时 MB 持续 503 | 无 | 工具返回 `source_unavailable`（`internal/app/curation_model.go:346-353`）或 `tool_execution_failed`（`run_tools.go:170-190`）；模型放弃或触限后 `abandon`（`internal/curation/model/item.go:114-130`）在 `item.go:129` 调 `FinishUnlisted`（`unlisted.go:37-80`），不检查来源状态 | **违背 ADR 16.1/16.4 与合同 25.3** |
| 20 | 组级对应时的名字佐证 | 仅版本级 | `structuralNameValues`（`name_verification.go:19-71`）在第 20 行对组级返回空；`verification.go:225-227` 记 `no_evidence`；`sources.Group.Title` 已读入（`musicbrainz/project.go:16`）但未使用 | 能力缺口 |
| 21 | 同组多版无版本标识 | 定向用例 | `checkModelEditions`（`model_editions.go:14-103`，由 `model_matcher.go:184` 调用）降组级；第 16 行跳过未通过直接结构核对的审查路径；`backgroundPrompt`（`internal/app/curation_model.go:184-190`）与工具说明（`stewardtools/tools.go:41`）未告知模型 | 缺陷 |
