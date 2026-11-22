# Research: 扫描侧匹配依据与外部资料的现有接入点

- **Query**: 外部数据源（MusicBrainz、AcoustID、Cover Art Archive、LRCLIB）与自动整理的前置调研：扫描侧已有哪些可作匹配依据的标识与证据结构，外部资料在后端的现有接入点
- **Scope**: internal（只读；代码、迁移、规范、部署文件；本机参考稿目录）
- **Date**: 2026-11-22
- **路径约定**: 未特别说明时，路径相对仓库根；`backend/` 下的 Go 路径写全。

## 结论（10 行以内）

1. 标签里的 MusicBrainz ID、Discogs ID、AcoustID **完全没有解析**；后端唯一出现 MusicBrainz 的地方是管家的联网工具（`backend/internal/steward/web/`）。
2. 已解析并落库的外部可比对标识只有三样：厂牌编号（`releases.catalog_number`）、条码（`releases.barcode`）、CUE 的 ISRC（`tracks.cue_isrc`）。**标签里的 ISRC 解析了但没有落库**。
3. “内容指纹”是音频负载的哈希（FLAC/APE 用编码器写的 MD5，其余 SHA-256），不是声学指纹；仓库与镜像里没有 `fpcalc`/Chromaprint，固定的 ffmpeg 7.1.1 构建不含 chromaprint。
4. 字段证据有两层：`track_observations`（逐轨逐字段）与 `release_field_decisions`（发行级决定，含候选值）；两者每次落库先删后写，`source_kind`/`selected_source` 是无约束文本，现有取值全是本地来源。
5. 封面来源只有 `embedded` 与 `folder`（数据库 CHECK 写死），文件按内容哈希存在 `ROOMUSIC_DATA_DIR/artwork/`；扫描找不到本地封面时会删除该发行的封面关联。
6. 歌词存在 `tracks.lyrics`/`lyrics_source`（CHECK 限定五种本地来源），每次落库整体覆盖；没有外部歌词的位置。
7. 艺人只有名字、别名、合并候选与显示名投影；没有简介和图片。`artist_profiles` 是显示名与排序键的派生表。
8. 没有通用作业队列。现有后台任务是四个各自独立的常驻循环（定时扫描、音频分析、目录投影维护、备份），音频分析是“按缺口查询、单线程、扫描期间让路”的模式。
9. 出站 HTTP 统一经 `steward/egress.NewClient`（地址策略、禁用环境代理、超时、重定向上限）；MusicBrainz 有进程内每秒一次的全局限速。模型端点与搜索配置是**按用户**存的，没有实例级模型配置。
10. 生产容器根文件系统只读，可写处只有 `/data`、`/backups` 与 64 MiB 的 `/tmp`；备份只含数据库，不含 `/data`。本机参考稿目录 `~/.local/share/roomusic-reference/` **不存在**。

---

## 1. 扫描提取的可用于外部匹配的标识

### 1.1 解析结构

`backend/internal/library/parser/parser.go:13-55` 的 `AudioObservation` 是解析器输出，与外部匹配有关的字段只有：`Catalog`(:34)、`ISRC`(:35)、`Label`(:39)、`Barcode`(:40)、`RawAtoms`(:43)，以及 `Title/Artist/Album/AlbumArtist/Year/TrackNumber/DiscNumber/DurationSeconds`。

通用键入口 `setAudioTag`（`backend/internal/library/parser/tags.go:40-101`），键先转大写并去掉 `TXXX:`、`----:` 前缀(:58-60)：

| 标识 | 接受的键 | 依据 |
|---|---|---|
| 厂牌编号 | `CATALOG`、`CATALOGNUMBER`、`CATALOGNO` | `tags.go:78-79` |
| 厂牌 | `LABEL`、`PUBLISHER`、`ORGANIZATION`（首个非空） | `tags.go:80-83` |
| 条码 | `BARCODE`、`UPC`、`EAN`（首个非空） | `tags.go:84-87` |
| ISRC | `ISRC` | `tags.go:92-93` |

`switch` 之外的键不保存（`tags.go:62-98` 没有 default 分支）。FLAC、APEv2、MP3 的 TXXX 都走这个入口（`flac.go:134`、`apev2.go:103`、`mp3.go:111`）。

各格式的补充：

- MP3：帧白名单在 `backend/internal/library/parser/mp3.go:84`，含 `TSRC`（→ISRC，:150-151）、`TPUB`（→厂牌，:152-153）、`TXXX`；**不含 `UFID`**（MusicBrainz 录音 ID 在 ID3 里的常见载体）。
- M4A：自由格式原子只处理 `com.apple.iTunes`（`m4a.go:610`），其中只有 `LABEL`(:639) 与 `UPC`(:643) 被取用，其余 `default: return nil`(:647-648)。`RawAtoms` 最多保留 64 项原子文本（`parser.go:70`、`m4a.go:587-589`），但 `RawAtoms` 在解析器之外没有任何读取方（全仓库搜索只命中 `parser.go` 与 `m4a.go`）。
- CUE：`CATALOG` → `document.Catalog`（`cue.go:245-246`），`ISRC` → 轨级 `ISRC`（`cue.go:247-249`）；`REM` 只取 `GENRE`、`DATE`/`YEAR`（`cue.go:234-244`），没有 `REM DISCID` 等。CUE 的 `CATALOG` 被写进**厂牌编号**字段（`backend/internal/library/scanner.go:583`、:585-587），不是条码字段。

### 1.2 逐项结论

| 标识 | 是否解析 | 落库位置 | 依据 |
|---|---|---|---|
| MusicBrainz recording / release / release group / artist / track ID | 否 | 无 | `backend/internal/library`、`backend/internal/catalog` 内搜索 `MUSICBRAINZ`、`UFID`、`MBID` 无命中 |
| Discogs ID | 否 | 无 | 全后端搜索 `discogs` 无命中 |
| AcoustID（标签里的 ID 或指纹） | 否 | 无 | 全后端搜索 `acoustid`、`chromaprint` 无命中 |
| ISRC（标签） | 解析到 `AudioObservation.ISRC` | **不落库**：`buildPhysicalSourceObservation` 不传 ISRC | `scanner.go:557`；`SourceObservation` 只有 `CueISRC`（`backend/internal/catalog/ingest/observation.go:37`） |
| ISRC（CUE） | 是 | `tracks.cue_isrc`；`track_observations` 的 `isrc` 字段（来源 `cue_track`） | `scanner.go:584-589`、:653；`observation.go:84`；`backend/migrations/0011_scan_staging_and_identity.sql:24`；`backend/internal/catalog/ingest/persist_track.go:76-82` |
| 条码 / UPC / EAN | 是 | `releases.barcode`（可人工修正） | `backend/migrations/0012_track_credits_and_release_metadata.sql:5`、:27-31；`0017_governed_operations.sql:52` |
| 厂牌编号 | 是 | `releases.catalog_number`（可人工修正） | `0011_scan_staging_and_identity.sql:30`；`0017_governed_operations.sql:52` |
| 厂牌 | 是 | `releases.label` | `0012_track_credits_and_release_metadata.sql:4` |
| 时长、曲目数、碟号、轨号 | 是 | `tracks.duration_seconds`、`position`、`disc_number` 等 | `0010_album_organization.sql:8`；`persist_track.go:76-82` |

接口暴露：发行详情带 `catalog_number`、`edition`、`label`、`barcode`（`backend/internal/catalog/projection.go:85-100`）；曲目的 `isrc` 只在 CUE 来源时出现（`backend/internal/catalog/read.go:186`、:261-265）。

### 1.3 解析缓存

增量扫描把每个文件的观察 JSON 缓存在 `library_parse_cache`，键含 `parser_version`（`backend/migrations/0041_incremental_scan.sql:1-12`）。版本串由 `CommonVersion`（现为 3）与各格式版本组成（`backend/internal/library/parser/version.go:9-34`）；注释写明“观察结构或共用解析规则改变时递增，使所有格式的缓存失效”(:8)。

---

## 2. “内容指纹”是什么

### 2.1 性质与算法

是**音频负载的哈希**，不是声学指纹。`backend/internal/library/parser/fingerprint.go:20-21`：“内容指纹只取决于音频负载：改标签、封面或文件名不改变它，音频改变才会改变。格式为 `<种类>-v<版本>:<值>`”。

| 种类 | 算法 | 依据 |
|---|---|---|
| `flac-md5-v1` | STREAMINFO 里编码器写的音频 MD5 加总样本数、采样率、声道数、位深（只读文件头） | `fingerprint.go:23`、:137-139、:156-179 |
| `flac-frames-v1` | MD5 全零时，对最后一个元数据块之后的全部字节做 SHA-256 | `fingerprint.go:24`、:188-192 |
| `ape-md5-v1` / `ape-frames-v1` | 头里的 MD5 加样本参数；没有时 SHA-256 帧数据 | `fingerprint_lossless.go:12-15`、:32-42 |
| `m4a-mdat-v1`、`mp3-frames-v1`、`ogg-audio-v1`、`wav-data-v1`、`wavpack-blocks-v1`、`aiff-ssnd-v1`、`dsf-data-v1`、`dff-dsd-v1` | 各自音频负载区间的 SHA-256 | `fingerprint.go:25-32`、:200-237、:340-380、:466-483；`fingerprint_lossless.go:58-114`；`fingerprint_dsd.go:12-40` |

同一段录音的不同编码、不同转码、不同母带，指纹不同；它不能用来向 AcoustID 查询。

### 2.2 存储与用途

- 存储：`tracks.content_fingerprint`（TEXT，≤256 字节），同时记 `source_size`、`source_modified_at`；暂存行 `scan_observations.content_fingerprint`；索引 `tracks_root_fingerprint_index(source_root_id, content_fingerprint)`（`backend/migrations/0013_track_content_fingerprints.sql:3-5` 加列、:34-35 索引、:39-52 暂存行的列）。
- 用途一（阶段 I 身份稳定）：同一 root 内改名与移动按音频内容保守重绑定曲目（`0013_track_content_fingerprints.sql:1`）；规划入口 `backend/internal/library/identity_plan.go:15-35`、:48，端口定义 `backend/internal/catalog/ingest/identity_plan.go:21-24`。
- 用途二（播放）：对已打开的句柄重新取证（`backend/internal/library/content_fingerprint.go:10-15`）。
- 用途三（音频分析）：`track_analyses.content_fingerprint` 与曲目当前指纹不一致时重新分析（`backend/migrations/0033_track_analyses.sql:4`；`backend/internal/catalog/track_analysis.go:77-81`）。
- FLAC 与 OGG 的指纹在解析时顺带得到，其余格式需要另读一遍文件（`parser.go:44-46`）。

### 2.3 fpcalc / Chromaprint / ffmpeg

- 仓库内搜索 `chromaprint`、`fpcalc`、`acoustid`（排除 `node_modules`、`.git`、`temp`、归档任务）**无命中**。
- 生产镜像：运行层是 `alpine:3.23`，只装 `ca-certificates` 与 `postgresql18-client`，另从固定镜像复制 `/ffmpeg` 一个二进制（`deploy/production/Dockerfile:27-35`）。没有 `fpcalc`，也没有 `ffprobe`（只复制了 `/ffmpeg`）。
- ffmpeg 的固定方式：`mwader/static-ffmpeg@sha256:11a44711…`，版本前缀 `ffmpeg version 7.1.1`（`scripts/ffmpeg-pinned.sh:13-14`；`deploy/production/Dockerfile:27`）。脚本把二进制取到 `${XDG_CACHE_HOME:-$HOME/.cache}/roomusic/ffmpeg-<摘要前 12 位>/ffmpeg` 并核对版本串(:21-34)；运行时用 `ROOMUSIC_FFMPEG_PATH`（`compose.prod.yaml:25`；`backend/internal/platform/config/config.go:36-37`、:55-58）。
- **这个构建不能产出 chromaprint**：对本机已缓存的固定二进制执行 `-buildconf` 与 `-muxers`，配置串里没有 `--enable-chromaprint`，muxer 列表里没有 `chromaprint`（本次调研实测；只执行了只读的查询命令）。
- 调用方式（两处，都用 `exec.CommandContext`）：
  - 音频分析：`backend/internal/playback/analysis/analysis.go:145-160`，参数含 `-threads 1`、`-protocol_whitelist file,pipe`，来源经 `/dev/fd/3` 或 stdin 传入，环境只有 `LC_ALL=C`；启动后设 nice 19 与 idle IO 优先级(:178-179；`process_unix.go:27`、`ioprio_linux.go:7`)；单曲超时 10 分钟(:26)。
  - 播放渲染：`backend/internal/playback/render/ffmpeg/process.go:82`；能力自检在 `tool.go:75-100`、`selftest.go:26-102`。
- 找不到 ffmpeg 或自检不过时，分析与渲染不可用，应用照常启动（`config.go:36`；`backend/internal/app/analysis.go:13-30`）。

---

## 3. 字段候选、决定与证据

### 3.1 数据结构

**逐轨字段观察** `track_observations`：`track_id, scan_run_id, source_kind, inferred, observed_at, field_name, value`（`backend/migrations/0003_catalog_observations.sql:25-32`、`0004_field_provenance.sql:1-6`）；每 `(track_id, field_name)` 至多一行（`0011_scan_staging_and_identity.sql:124-125`）。`source_kind` 没有 CHECK。写入的字段名：`title, artist, album, album_artist, track_number, disc_number, year, source_type, media_type, genre, catalog_number, edition, label, barcode, isrc`（`backend/internal/catalog/ingest/observation.go:72-104`）。落库时**先删该曲目全部观察再重写**（`backend/internal/catalog/ingest/persist.go:103-110`）。

**发行级字段决定** `release_field_decisions`：`release_id, field_key, selected_value(JSONB), selected_source, confidence, action, rule_id, candidates(JSONB), reason, scan_run_id`，`UNIQUE(release_id, field_key)`（`backend/migrations/0010_album_organization.sql:17-22`）。约束：`confidence IN ('high','medium','low')`(:27-33)，`action IN ('auto_apply','uncertain_apply')`(:36-42)；`selected_source`、`rule_id` 无约束。对应 Go 结构 `FieldDecision{Field, Value, Source, Confidence, Action, RuleID, Reason, Candidates}`（`backend/internal/catalog/ingest/organizer.go:33-37`）。落库时**先删该发行全部决定再重写**（`persist.go:132-140`）。

**归组证据** `release_grouping_evidence`：`candidate_kind, rule_id, source_refs(JSONB), reason`，每发行一行（`0010_album_organization.sql:45-48`；`0011:136-137`）；`rule_id` 固定写 `organizer_v2`，`reason` 是 attention 原因用 `;` 连接（`persist.go:158-160`）。

**人工修正覆盖层** `release_corrections`（`0017_governed_operations.sql:44-61`）：可修正字段限 `title, album_artist, year, genre, label, catalog_number, edition, barcode`(:52)；“扫描只写 releases，从不碰这张表，所以重扫不会覆盖修正是结构保证”(:41-43)；读取走视图 `release_effective`(:67 起)。每条修正挂一条 `operations` 记录，可撤销（`backend/internal/operations/kind.go:9-22`、:62-93）。

### 3.2 来源种类的现有取值

解析器级 `SourceKind`（写进曲目与逐轨观察）：`flac_vorbis_comment`、`mp3_id3v2`、`ogg_vorbis_comment`、`opus_tags`、`m4a`、`wav_info`、`ape_apev2`、`wavpack_apev2`、`aiff_id3`、DSD 两种、`cue_virtual`（`flac.go:22`、`mp3.go:22`、`ogg.go:23`、:161、`m4a.go:34`、`wav.go:30`、`ape.go:32`、`wavpack.go:36`、`aiff.go:29`、`dsd.go:35`；`scanner.go:563`）。

字段级来源（`FieldSources`，覆盖解析器级）：`cue_sheet`、`cue_track`（`scanner.go:566-612`）、`folder`（`organizer.go:581-601`）、`filename_fallback` 等推断来源、`default_fallback`（`observation.go:95-100`）。

决定级 `selected_source`：默认 `tag`，否则取胜出观察的字段来源；全部为推断时取推断来源或 `default_fallback`（`organizer.go:131-152`，注释“source 表示决定级证据来源，不暴露具体解析器实现名称”）；另有 `folder_fallback`(:848)、`rip_log`(:1005)。

规则与动作：`majority_v1`（多数票；候选不一致时 `medium`/`uncertain_apply`/`inconsistent_candidates`；全是推断时 `low`/`uncertain_apply`/`inferred_value`，`organizer.go:124-156`）、`field_missing_v1`(:111)、`genre_union_v1`(:176)、`compilation_v1`（`compilation.go:25`）、`rip_log_cd_v1`（`organizer.go:1005`）。

**现有取值里没有任何外部来源。**

### 3.3 `GET /api/v1/releases/{id}/evidence`

注册于 `backend/internal/catalog/httpapi/handlers.go:67`，处理函数 :223-249：仅管理员；非 UUID 返回 400，发行不存在 404。读取实现 `backend/internal/catalog/read.go:395-466`，返回类型 `backend/internal/catalog/projection.go:102-121`：

```
ReleaseEvidence {
  release_id
  fields: [ { field, source, confidence, action, rule_id, value|null, candidates[], reason_code|null } ]
  grouping: { candidate_kind, rule_id, source_refs[], reason_codes[] } | null
  truncated
}
```

`fields` 直接来自 `release_field_decisions`，最多 100 项（`read.go:24`）；`candidates` 是字符串数组，没有逐候选的来源或分数。发行详情里另有摘要版 `evidence`（不含值与候选，`projection.go:77-83`、:95）。管家也读同一份（`backend/internal/catalog/stewardtools/details.go:127`、`review.go:89`）。

### 3.4 外部产出作为“证据”的位置

- 结构上：`track_observations.source_kind` 与 `release_field_decisions.selected_source` 是自由文本，没有约束阻止新取值；`candidates` 只是值的列表。
- 行为上：两张表都由扫描落库“先删后写”（`persist.go:103`、:132），不属于扫描产出的行会在该发行下次落库时被清掉。
- `release_field_decisions` 每 `(release_id, field_key)` 只有一行，表达的是“扫描选了哪个值”，没有“同一字段多个来源并存”的行位。
- 没有保存外部实体标识（MBID 等）与本地发行、曲目、艺人对应关系的表或列。
- 架构文档对外部提供方的既有表述：“例如 MusicBrainz；结果只作候选或证据，不覆盖人工修正”（`docs/architecture/core-1-architecture.html:825-827`，标为“扩展层 · 远期”）；路线图写“外部元数据源不作为 Core 1 前置”（`docs/architecture/core-1-roadmap.md:51-52`）。

---

## 4. 封面

### 4.1 发现顺序

`selectArtwork`（`backend/internal/library/scanner.go:1034-1092`），注释(:1010)：“依次尝试精确名、内嵌、附属图片与 Box 父目录封面”。

1. 发行目录内的精确文件名，按 `folderArtworkNames` 的顺序：`cover.jpg, cover.jpeg, cover.png, cover.webp, folder.jpg, folder.jpeg, folder.png, front.jpg, front.jpeg, front.png`（`scanner.go:1119`），大小写不敏感匹配(:1280-1281)。
2. 内嵌封面（`tryEmbedded`，:1041-1063）；同一文件内正面封面（图片类型 3）优先，否则先到先得，单张上限 16 MiB（`backend/internal/library/parser/artwork.go:4-15`）。
3. 附属子目录 `scans`、`artwork`、`art`、`covers`（只看直接子目录，不递归，不跟符号链接，`scanner.go:1121-1122`、:1252）。
4. Box 父目录的精确名（仅 `BoxParentArtwork` 为真时，:1176-1190）。

`same_dir_split`、`loose_album`、`loose_unknown` 三类候选不找文件夹封面(:1167-1170)。档位常量 `artworkTierExact=1`、`artworkTierSupplementary=3`、`artworkTierBoxParent=4`(:1153-1157)。

### 4.2 存储

- 表 `release_artworks`：`release_id`（主键）、`content_hash`、`mime_type`（jpeg/png/gif/webp）、`width`、`height`、`storage_key`、`source_type`（`backend/migrations/0005_release_artwork.sql:1-10`）；**`source_type` CHECK 限定 `('embedded','folder')`**(:8)，后续迁移没有改过这个约束。每个发行一张。
- 文件：`<ROOMUSIC_DATA_DIR>/artwork/<sha256>.<扩展名>`，内容寻址，多个发行可共用（`backend/internal/catalog/artwork/artwork.go:33-36`、:137-146；`0011:33-34`）；写入先落临时文件再 rename(:158-204)；替换或移除后无引用的文件会删除(:106-110、:224-236)。包注释(:1-2)：“只写数据目录，从不触碰音乐库”。
- 校验：≤16 MiB、按文件头识别格式、能读出宽高（`artwork.go:121-134`、:297-311）。
- 读取接口：`GET /api/v1/artworks/{id}`（`backend/internal/catalog/httpapi/handlers.go:85`），`id` 就是 `storage_key`，同时充当 ETag（`artwork.go:38-43`）。
- 没有缩略图或其他派生尺寸的存储。`media-cache` 是已废弃的派生音频缓存，启动时删除（`backend/internal/app/media_cache.go:15-63`）。

### 4.3 外部下载封面

- 没有“外部”来源：数据库约束、`Save` 的注释（`artwork.go:83`）与调用方（`scanner.go:1070`、:1077、:1085）都只有 `embedded`、`folder`。
- 扫描落库时若本地来源一张都选不出，会调用 `store.Remove` **删除该发行的封面关联**（`scanner.go:1022-1029`）。
- 管家工具 `get_music_metadata` 的 `cover-art` 只返回 Cover Art Archive 的清单（URL、front/back、缩略图地址），**不下载图片**（`backend/internal/steward/web/musicbrainz.go:103-108`、:170-175、:177-231）。

---

## 5. 歌词

- 内嵌来源键：`vorbis_lyrics`、`vorbis_unsyncedlyrics`、`mp4_lyr`、`id3_uslt`；外置 `lrc_file`（`backend/internal/library/parser/lyrics.go:14-21`）。规范化后上限 64 KiB(:24)，外置文件解码前上限 256 KiB(:27)，解码顺序 BOM → UTF-8 → GB18030(:58)。
- 外置 LRC：同目录同名 `.lrc`，精确名优先；否则按 NFC 加大小写折叠后唯一匹配（`backend/internal/library/sidecar_lyrics.go:59-71`）。存在时**覆盖内嵌歌词**并把来源写成 `lrc_file`（`backend/internal/library/scan_pipeline.go:169-170`；`lyrics.go:86-101`）。
- CUE 虚拟轨没有歌词（`backend/internal/catalog/ingest/observation.go:15`）。
- 存储：`tracks.lyrics`、`tracks.lyrics_source`；CHECK 要求两列同有同无，来源限定上述五个值（`backend/migrations/0015_track_lyrics.sql:4-19`、`0050_track_lyrics_file.sql:2-8`）。每次落库随曲目行整体写入（`persist_track.go:81-82`）。
- 接口：`GET /api/v1/tracks/{id}/lyrics`（`handlers.go:86`、:253-273），登录成员可读；`LoadTrackLyrics` 只读数据库（`backend/internal/catalog/lyrics.go:48-73`），响应 `{track_id, lyrics: {kind: synced|plain, source: embedded|external, lines[]} | null}`(:29-46)。这里的 `external` 指外置文件 `lrc_file`(:56、:66-68)，不是联网获取。
- 歌词署名：从歌词文本里提取署名写入 `track_credits`，`source` 限 `('tag','lyrics')`（`backend/migrations/0038_credit_roles.sql:7-8`）。
- **外部获取的歌词没有位置**：约束不含对应来源键，且扫描落库会覆盖这两列。规范见 `.trellis/spec/backend/lyrics-contracts.md`。

---

## 6. 艺人

### 6.1 名字与别名

表结构在 `backend/migrations/0026_artist_catalog.sql`：`artists`(:6-9)、`artist_name_keys`（规范化键）(:10-15)、`artist_names`（写法；`origin IN ('scan','correction','seed')`）(:17-21)、`artist_credits`（`layer IN ('scan','correction')`）(:22-30)、`artist_name_bindings`（人工把名字键绑到艺人）(:33-37)、`artist_assignment_overrides`(:39-45)、`artist_overrides`（显示名、重定向）(:49-56)、`artist_tag_evidence`（原始标签值）(:88-94)。内置种子别名只有一组示例(:125-131)。接口 `GET /api/v1/artists/{id}/names`（`handlers.go:58`）。

### 6.2 合并候选（“重复艺人”）

两种信号，都在发行的艺人图刷新时重算（`backend/internal/catalog/internal/artistgraph/candidates.go`）：

- `album_track_one_to_one`：一张发行的所有在库曲目都只有同一个曲目艺人，且与唯一的专辑艺人名字键不同(:13-38)。`same_track_role` 已在 0031 删除（`backend/migrations/0031_artist_alias_signals.sql:1-4`）。
- `bracket_alias`：名字形如“外(内)”，括号内或外的部分恰好是库里另一个名字键(:41-155；规则 `bracket.go:30-60`，排除乐器词等)；存在 `artist_alias_signals`（`0031:7-19`）。

列表 `GET /api/v1/artist-candidates`，治理动作 merge / split / rename / candidate-ignore（`backend/internal/catalog/httpapi/artist_governance.go:34-39`）；忽略记录在 `artist_candidate_ignores`（`0026:103-107`）。这些都是受治理操作，可撤销（`backend/internal/app/app.go:144-145`）。候选只来自库内共现与写法，没有外部依据。

### 6.3 艺人资料

- `artist_profiles` 只有 `auto_display_name, effective_display_name, sort_key, initial_group, version, applied_generation`（`0026:69-74`；`0028_artist_profile_backfill.sql:2`），是显示名与排序的派生投影。
- `backend/internal/catalog/maintenance/artist_profiles.go:8-33` 只做“发现需要回填投影的艺人”，写 `catalog_projection_work`，不涉及任何外部资料。
- 艺人详情 `ArtistDetail`：`artist, revision, name_source, names, names_pagination, role_counts, redirect_artist_id`（`backend/internal/catalog/artists_types.go:53-61`）。
- **没有简介、图片**：后端代码与迁移里搜索 `biograph`、`bio`、`portrait`、`artist_image`、`简介`、`头像` 无命中；艺人读取代码里没有 artwork 引用。

---

## 7. 定时扫描、增量扫描与后台任务

### 7.1 定时扫描

- 配置：单行表 `scan_schedule(id=1, enabled, hour, updated_at)`，默认开启、4 点；`scan_runs.trigger_kind IN ('manual','scheduled')`（`backend/migrations/0042_scheduled_scan.sql:1-9`）。
- 读写：`LoadScanSchedule`/`SaveScanSchedule`（`backend/internal/library/scan_schedule.go:61-92`），保存后经通道唤醒调度器(:87-90)；接口 `GET`/`PUT /api/v1/scan-schedule`（`backend/internal/library/httpapi/handlers.go:42-43`）。
- 调度器：`RunScanScheduler`（`backend/internal/library/scan_scheduler.go:42-92`），应用启动时起一个 goroutine（`backend/internal/app/app.go:132-140`），时区取备份配置的 `Location`（即 `ROOMUSIC_TIMEZONE`）；最长每 10 分钟轮询一次(:9)；“触发无论成功、跳过还是失败都消费该时刻，不排队、不重试”(:41)；触发的是增量扫描(:104)；已有扫描在跑则跳过(:110-111)。时钟可注入(:11-38)。

### 7.2 增量扫描

- `scan_runs.scan_mode IN ('incremental','full')`，默认 `incremental`（`0041_incremental_scan.sql:22-23`）。
- `library_parse_cache`：按 `(root_id, relative_path)` 缓存观察，凭 `source_size`、`source_modified_at`、`parser_version` 判断可否复用(:1-12)。
- `scan_candidate_states`：每个候选的签名（64 位十六进制），签名不变的候选跳过落库(:14-21)。
- 读盘并发 `ROOMUSIC_SCAN_WORKERS`（1–16，默认 4，`compose.prod.yaml:22`；`config.go:196-202`）；有界并发工具 `backend/internal/platform/workpool`（`map.go` 的 `Map`、`ordered.go`）。
- 全局同一时刻只有一个扫描：PostgreSQL advisory lock（`backend/internal/library/scans.go:40`、:61、:79）；`ActiveScan`(:438)。
- 规范：`.trellis/spec/backend/library-scan-incremental.md`、`scan-coordination-contracts.md`、`library-scan-pipeline.md`。

### 7.3 音频分析（响度、波形、静音）

- `AnalysisWorker`（`backend/internal/playback/analysis_worker.go:37-154`）：“顺序分析 catalog 提供的曲目快照；一个 worker 最多运行一个 ffmpeg”(:37)。
- **没有队列表**：每轮用 `ListAnalysisCandidates` 查“还没有可用结果”的曲目（`backend/internal/catalog/track_analysis.go:69-88`）：没有结果、分析器版本不同、指纹/大小/修改时间/CUE 区间变了，或失败不足 3 次且距上次超过 24 小时。每批 20 首（`analysis_worker.go:19`）。
- 限速与让路：扫描进行中则等待 30 秒再看(:21、:73-78)，每首之间再查一次(:101-113)；每首之后暂停“本首耗时、至少 2 秒”(:145-151)；空闲 1 分钟、数据库出错 1 分钟(:20、:22)。
- 结果表 `track_analyses`：`analyzer_version, content_fingerprint, source_size, source_modified_at, status('done','failed'), failure_category, attempts, analyzed_at` 与各项数值（`backend/migrations/0033_track_analyses.sql:1-23`）。
- 装配：`app.go:152-162`；开关 `ROOMUSIC_AUDIO_ANALYSIS`（`config.go:184-190`）；进度经 `NewAnalysisProgressProvider` 暴露(:163)。规范 `.trellis/spec/backend/audio-analysis-contracts.md`。

### 7.4 目录投影维护

`maintenance.Run`（`backend/internal/catalog/maintenance/worker.go:26-67`）：专用连接上用会话级 advisory lock 选主(:15、:41-47)，失败退避 1 秒；工作清单在 `catalog_projection_work`/`catalog_projection_phases`（`backend/migrations/0025_catalog_search.sql:42-62`；`subject_kind` 后扩展为 `release, track, artist`，`0028:5-10`），按 `(build_id, phase)` 组织；批间让出 10–50 ms（`pacing.go:5-21`）。这套清单是搜索与艺人**投影回填**专用（`capability IN ('search','artists')`，`0026:2-3`）。

### 7.5 备份

独立调度循环 `backend/internal/backup/scheduler.go:10-30`，状态与保留规则存在备份目录里的 JSON 文件（`state.go:10`、`retention.go:15`）。

### 7.6 是否有通用后台作业机制

**没有。** 上述四个循环各自实现“取下一项、处理、等待”，没有共用的作业表、调度器或重试框架。可见的共同做法：应用生命周期 context 控制退出、各有一个 done 通道供关停等待（`app.go:132-140`、:153-162、:177-187、:193）；长任务让路于扫描（分析）；按“缺口查询”而不是入队（分析）；advisory lock 保证单实例（扫描、维护、备份）。

管家另有自己的后台运行（会话级，`backend/internal/steward/runtime/supervisor.go`），启动时把运行中的会话重置（`backend/internal/app/steward.go:66-83`），它绑定用户会话，不是系统级作业。

---

## 8. 出站 HTTP

### 8.1 所有对外发请求的位置

| 位置 | 目标 | 客户端与策略 | 依据 |
|---|---|---|---|
| 管家模型调用 | 用户填写的模型端点（Chat Completions / Messages / Responses） | `OutboundClient`：管理员 `Unrestricted`，成员 `PublicOrAllowlist`；超时 5 分钟；不跟随重定向 | `backend/internal/steward/provider/client.go:86-116`；`backend/internal/steward/httpapi/outbound.go:20-47` |
| 联网搜索 SearXNG | 用户填写的实例地址 | 复用模型端点的客户端策略 | `backend/internal/steward/web/search.go:55-70`；`service.go:30-31` |
| 联网搜索 Tavily | `https://api.tavily.com/search` | `egress` 客户端 | `search.go:19` |
| 读取网页 | 模型给出的任意公网 URL | `PublicOnly`，仅 80/443，最多 5 次重定向，20 秒，正文 ≤2 MiB、文本 ≤100 KiB，通用 User-Agent | `backend/internal/steward/web/fetch.go:24`、:35-44、:54-110；`service.go:21-23` |
| MusicBrainz | `https://musicbrainz.org/ws/2` | `PublicOnly`，20 秒，全局每秒一次 | `backend/internal/steward/web/musicbrainz.go:17`、:35-60 |
| Cover Art Archive | `https://coverartarchive.org/release/{mbid}/` | `PublicOnly`，5 次重定向，只允许跳到 `coverartarchive.org`、`archive.org` 及其子域的 `/index.json` | `musicbrainz.go:18`、:177-231（重定向限制 :195） |
| `opra-update` 命令行 | `opra.roonlabs.net`、`raw.githubusercontent.com` | 维护者手动运行的独立命令，自建 `http.Client{Timeout: 2 分钟}`，不属于服务进程 | `backend/cmd/opra-update/main.go:1`、:21-23、:50 |

服务进程里所有 `http.NewRequest` 都在 `steward/` 下（`fetch.go:84`、`service.go:96`、`provider/client.go:87`）；扫描、目录、播放没有任何出站请求。AcoustID、LRCLIB、Discogs 没有任何接入。

### 8.2 共用客户端与地址校验

`backend/internal/steward/egress/client.go`：

- `NewClient(Options)`(:44-72)：只允许 `http`/`https`(:99-104)；**禁用环境代理**（注释 :42；自建 `http.Transport` 没有设置 `Proxy`，:55-58）；`MaxRedirects`、`Timeout` 由调用方给(:36-39)。
- 三种策略(:16-25)：`PublicOnly`、`PublicOrAllowlist`、`Unrestricted`。
- 地址判断在**拨号时针对实际 IP**做(:106-128、:213-236)，解析结果里只要有一个非公网地址就拒绝(:227-234)；`IsPublicIP`(:132-168) 排除私网、CGNAT、文档网段、NAT64、6to4、Teredo 等。
- `CheckEndpoint` 预检(:172-190)。错误类型 `egress.Error{Code}`，三种原因码（`errors.go:11-18`）。
- 放行名单：实例级表 `steward_egress_allowlist(host, port)`（`backend/migrations/0051_steward_runtime.sql:24-30`），管理员接口 `/api/v1/admin/steward/egress-allowlist`（`backend/internal/steward/httpapi/handlers.go:76-77`）；只对 `PublicOrAllowlist` 生效。
- 设计依据：ADR `docs/architecture/adr/0005-steward-data-and-egress.md`（出站地址规则 :100-107，网页抓取限制 :109-116，MusicBrainz 的 User-Agent :120）。

**没有代理配置项**：`egress.Options` 无代理字段，环境代理被禁用，配置里也没有相关环境变量。

### 8.3 超时与限速

- 外部资料请求统一 20 秒、响应体 ≤2 MiB（`service.go:21-22`、:105-114）。
- MusicBrainz 全局限速：进程内 `globalMusicLimiter`，单许可串行，两次请求间隔至少 1 秒，排队超过 10 秒返回 `rate_limited`，可取消（`backend/internal/steward/web/limiter.go:62-109`）；503 不重试（`musicbrainz.go:55-58`）。**Cover Art Archive 的请求不经过这个限速器**（`coverArt` 内没有 `musicLimiter.wait`，`musicbrainz.go:177-231`）。
- 按用户的每分钟额度：读取网页 30、音乐资料 20、联网搜索 30（`limiter.go:9-14`、:31-59；装配在 `tools.go:92-108`）。
- User-Agent：`ROOMusic/<版本或 dev> ( https://github.com/qlqqs/ROOMusic )`，不含实例信息（`musicbrainz.go:27-33`；`backend/internal/platform/buildinfo/buildinfo.go:23-24`）。
- 每次请求新建客户端、用完关闭空闲连接（`musicbrainz.go:38-42`），没有连接复用，没有响应缓存。
- 请求日志：事件 `steward.web.request.finished`，只记来源、域名、耗时、结果数、错误码（`service.go:79-93`）。

### 8.4 MusicBrainz 现有封装的形状

- `SearchMusic(ctx, entity, query, limit)`：实体 `artist|release|release-group|recording`，`limit` 1–10，`query` 原样作为 MusicBrainz 查询串（`musicbrainz.go:63-101`）。
- `GetMusic(ctx, entity, mbid, include)`：按 MBID 读取，`include` 按实体白名单（:20-25、:110-167）。
- 返回经裁剪的 `Metadata`：MBID、标题/名字、消歧、署名（含艺人 MBID）、日期、国家、条码、厂牌与编号、介质与曲目表（曲目 MBID、录音、时长毫秒）（`backend/internal/steward/web/metadata.go:51-100`、:118-143）。**解码结构里没有 ISRC、别名、关系、标签、评分字段**(:4-39)。
- 这两个方法是 `web.Service` 的导出方法，但 `Service` 的构造依赖管家的设置读取端口、密钥库与模型端点客户端（`service.go:25-47`），目前只作为管家工具注册（`tools.go:11-115`；`backend/internal/app/steward.go:85-87`、:109），工具输出标记为不可信外部资料（`tools.go:110-112`）。
- 合同文档：`.trellis/spec/backend/steward-web-contracts.md`（音乐资料 :153-175）。

### 8.5 应用配置存在哪

没有统一的“全局应用设置”表，配置分散在：

| 配置 | 存放 | 读写 |
|---|---|---|
| 进程级（数据目录、音乐根、ffmpeg 路径、扫描并发、分析开关、时区、备份时刻） | 环境变量 | `backend/internal/platform/config/config.go:43-99`、:184-202；`config/backup.go:23-60` |
| 定时扫描 | 单行表 `scan_schedule` | `scan_schedule.go:61-92`；`/api/v1/scan-schedule` |
| 管家实例级 | 单行表 `steward_instance_settings(members_enabled)` | `0051_steward_runtime.sql:18-22` |
| 出站放行名单 | `steward_egress_allowlist` | `runtime/allowlist.go:22-66` |
| 模型端点、密钥、协议、模型、审查模型、搜索来源与密钥 | **按用户** `steward_user_settings` | `0051:1-16`；`0055_steward_reviews.sql:1`；`0056_steward_web.sql:1-6`；`runtime/settings.go:28-50` |
| 备份保留规则 | 备份目录里的 JSON 文件 | `backend/internal/backup/retention.go:15`、:85-95 |

密钥加密：实例密钥文件 `<ROOMUSIC_DATA_DIR>/steward/instance.key`（`backend/internal/app/steward.go:84`），库里只存密文与末四位（`runtime/settings.go:11-17`）。

模型调用需要某个用户已配置的端点（`UserSettings.IsConfigured`，`settings.go:53-55`）；没有不属于任何用户的系统级模型配置。管家的修改走“方案 → 确认/审查 → 受治理操作”（`steward_plans`、`steward_plan_items`，`0054_steward_plans.sql:7-30`；`steward_reviews`，`0055:4-17`），操作记录带 `origin`（`0054:1`；`backend/internal/operations/review_origin.go:8-10`）。

---

## 9. 可写数据目录

### 9.1 生产 Compose

`compose.prod.yaml` 的 `app` 服务：

| 挂载 | 性质 | 依据 |
|---|---|---|
| `${ROOMUSIC_MUSIC_DIR}` → `/music` | bind，**只读**，不自动建宿主目录 | :34-38 |
| `./data` → `/data` | bind，可写；`ROOMUSIC_DATA_DIR=/data` | :39、:23 |
| `./secrets` → `/run/secrets` | bind，只读 | :40 |
| `${ROOMUSIC_BACKUP_DIR:-./backups}` → `/backups` | bind，可写；`ROOMUSIC_BACKUP_DIR=/backups` | :41、:24 |
| 根文件系统 | `read_only: true` | :42 |
| `/tmp` | tmpfs，64 MiB | :43 |

另有 `cap_drop: [ALL]`、`no-new-privileges`(:44-45)；容器以 uid/gid 1000 运行（`deploy/production/Dockerfile:32-37`）。网络是普通 bridge(:73-76)，没有出站限制。环境变量 `ROOMUSIC_MEDIA_CACHE_MAX_MIB`(:29) 仍在 Compose 里，但对应的 `media-cache` 目录在启动时被删除（`backend/internal/app/media_cache.go:18-63`）。

`scripts/check-production-compose.sh` 的静态断言：服务只有 `app` 与 `postgres`、不使用命名卷、所有卷都是 bind(:42-48)，`/backups` 与 `/var/lib/postgresql` 的来源路径(:57-58)，以及可覆盖的备份目录与容器名(:61-71)。新增挂载会碰到这些断言。

首次安装脚本 `scripts/production-init.sh:38`、:66-68：创建 `./secrets ./data` 与备份目录，属主 1000:1000，权限 0700。规范 `.trellis/spec/backend/deployment-environment-contracts.md:30-31`。

### 9.2 `/data` 下现有内容

| 子路径 | 内容 | 依据 |
|---|---|---|
| `artwork/` | 内容寻址的封面文件 | `backend/internal/catalog/artwork/artwork.go:35` |
| `steward/instance.key` | 管家密钥加密用的实例密钥 | `backend/internal/app/steward.go:84` |
| `media-cache/` | 旧派生缓存，启动时删除 | `backend/internal/app/media_cache.go:16-21` |
| `backups/` | 仅在未显式设置 `ROOMUSIC_BACKUP_DIR` 时作默认备份目录 | `backend/internal/platform/config/backup.go:33-39` |

`ROOMUSIC_DATA_DIR` 未设置时默认 `./data`（`config.go:44-47`）；开发示例同值（`.env.dev.example:30`）。

**备份不含 `/data`**：“不备份 `/data` 或音乐文件”（`.trellis/spec/backend/deployment-environment-contracts.md:39`）；备份是 `pg_dump`（`backend/internal/backup/pgtools.go:43`）。DSP 的 IR 文件存在数据库的 BYTEA 列里（`backend/migrations/0048_dsp_ir_files.sql:1-13`），不在数据目录。

### 9.3 开发与测试

`docs/development-environment.md:16-25`：开发数据在 `temp/dev/`，临时文件与测试部署目录在 `temp/`，按日期归档到 `temp/archive/YYYY-MM-DD/`；Docker 隔离约定见 :27-33。开发 Compose 只有 PostgreSQL，用命名卷 `postgres-data`（`compose.dev.yaml:13-23`）。

---

## 10. 本机参考稿

- 目录 `~/.local/share/roomusic-reference/` **在本机不存在**（`ls` 报 No such file or directory；`~/.local/share/` 下只有 `applications`、`go`、`mise`、`nano`）。代码里引用它的地方只有 DSP 对照测试的定位函数，目录缺失时跳过（`backend/internal/dsp/internal/refdata/refdata.go:10-24`）。因此没有可摘的参考稿章节。
- 仓库内最接近的材料是本机归档的 `.trellis/tasks/archive/2026-11/11-08-scan-reference-port/research/reference-design.md`（《媒体库文件扫描子系统 · 技术设计与逻辑说明》，6131 行，不进公开仓库）。它覆盖本地扫描、标签解析、古典、署名、检索、音频分析与文件系统，**没有联网识别、声学指纹或外部元数据匹配的章节**（搜索 `acoustid`、`chromaprint` 无命中；`musicbrainz` 只在一个标签键名里出现一次）。与本题相关的几处：
  - §10「内容摘要：与路径无关的身份」(:901-925)：摘要取自音频流结构，不受标签编辑影响，用途是改名移动后的身份稳定；与本项目内容指纹同一用途，同样不是声学指纹。
  - §21.4「文本字段的完整回退链」(:2246-2272)：产品编码 `BARCODE → UPC → EAN/UPN`；目录编号 `CATALOGNUMBER → CATALOGUE # → CATALOGNO → CATALOG NUMBER → CATALOGUE NUMBER → CATALOG → CATALOGUE → LABELNO`；ISRC `TSRC → ISRC`；另有 `WORKID`、`ALBUMUID` 两个标识键(:2261-2262)。本项目目前接受的目录编号键是其中三个（见 1.1）。
  - §21.6 数值字段表里的“服务商标识”(:2368)：三家流媒体服务的轨/专辑标识被包装为带类型码的标识。
  - §21.7 之后的“发行类型兜底”(:2394-2400)：键集合含 `MUSICBRAINZ ALBUM TYPE`，只用于判断私录标记。
  - §11.3「外部资料库的编目导入与虚拟列表」(:1020 起)：这里的“外部资料库”指另一款本地播放软件的编目 XML，不是联网元数据。

---

## 接外部源时可以直接复用的东西

只列代码与文档能确认的现成能力，不含用法建议。

| 能力 | 位置 | 现状说明 |
|---|---|---|
| 出站客户端与地址策略 | `backend/internal/steward/egress/client.go:44-72` | 与管家解耦的独立包，只依赖标准库；`PublicOnly` 策略、超时、重定向上限、禁用环境代理 |
| MusicBrainz 查询与按 MBID 读取 | `backend/internal/steward/web/musicbrainz.go:63-167` | 已实现并有测试；包在 `web.Service` 上，构造依赖管家端口 |
| MusicBrainz 响应裁剪结构 | `backend/internal/steward/web/metadata.go:4-143` | 含发行、发行组、录音、艺人署名、厂牌编号、条码、曲目表与时长 |
| Cover Art Archive 清单读取 | `musicbrainz.go:177-231` | 只取清单与受限重定向，不下载图片 |
| 每秒一次的全局限速器 | `backend/internal/steward/web/limiter.go:62-109` | 包内未导出类型，时钟可注入 |
| 符合 MusicBrainz 要求的 User-Agent | `musicbrainz.go:27-33`；`buildinfo.go:23-24` | 应用名、版本、项目地址 |
| 封面校验与内容寻址存储 | `backend/internal/catalog/artwork/artwork.go:84-112`、:121-134 | 格式嗅探、尺寸读取、16 MiB 上限、原子写入、无引用清理 |
| 歌词规范化与 LRC 解析 | `backend/internal/library/parser/lyrics.go:35-56`；`backend/internal/catalog/lyrics.go:92-` | `NormalizeLyrics`、`ParseLyrics`（synced/plain、offset、上限） |
| 内容指纹与来源大小、修改时间 | `tracks.content_fingerprint` 等（`0013:3-5`） | 可判断“音频没变”，已被音频分析用作失效依据 |
| “按缺口查询”的后台 worker 形状 | `backend/internal/playback/analysis_worker.go:61-154`；`track_analysis.go:69-88`；`0033:1-23` | 版本号、指纹失效、失败重试上限与间隔、扫描期间让路、逐项节流 |
| 定时器与可注入时钟 | `backend/internal/library/scan_scheduler.go:11-92` | 每日整点、保存即唤醒 |
| 有界并发 | `backend/internal/platform/workpool/map.go`、`ordered.go` | 保序的 worker 池 |
| 受治理操作与撤销 | `backend/internal/operations/kind.go:9-93`；`0017:7-32` | 幂等键、前后状态、逐类型撤销；现有类型见 `0026:113-116` |
| 发行字段修正覆盖层 | `release_corrections`、`release_effective`（`0017:44-61`、:67-） | 八个字段，重扫不覆盖 |
| 艺人合并、拆分、改名、忽略候选 | `backend/internal/catalog/httpapi/artist_governance.go:34-39`；`0026:33-56` | 全部走受治理操作 |
| 证据读取接口与管家读取同一份 | `handlers.go:67`；`read.go:395-466`；`stewardtools/details.go:127` | 管理员可读 |
| 管家的方案、确认、独立审查 | `0054:7-30`；`0055:4-17`；`backend/internal/app/steward.go:127-` | 模型提意图，后端执行；操作带审查来源 |
| 已有的可比对标识 | `releases.catalog_number`、`releases.barcode`、`releases.label`、`tracks.cue_isrc`、曲目时长与曲目表 | 见第 1 节 |
| 解析缓存版本机制 | `backend/internal/library/parser/version.go:8-34` | 改解析规则时用版本号使缓存失效 |
| 数据目录 | `/data`（`compose.prod.yaml:39`） | 已是可写 bind；`artwork/` 已在其下 |

## 明确缺的东西

只列从代码与文档能确认“不存在”的项。

1. **标签里的 MusicBrainz ID、Discogs ID、AcoustID 没有解析**；MP3 的 `UFID` 帧不在白名单（`mp3.go:84`）；M4A 自由格式原子只取 `LABEL`、`UPC`（`m4a.go:638-649`）。
2. **标签 ISRC 不落库**（`scanner.go:557`）；`tracks` 上只有 `cue_isrc`。
3. **没有声学指纹**：没有 Chromaprint/`fpcalc`，固定的 ffmpeg 构建不含 chromaprint；镜像里也没有 `ffprobe`。
4. **没有外部实体标识的存储**：没有任何表或列保存 MBID 等外部 ID 与本地发行、发行组、曲目、艺人的对应，也没有匹配状态、置信度、匹配时间。
5. **证据表没有外部来源的常驻位置**：`track_observations` 与 `release_field_decisions` 由扫描先删后写（`persist.go:103`、:132）；`release_field_decisions` 每字段一行，`candidates` 只有值没有来源。
6. **`release_field_decisions.action` 只有 `auto_apply`、`uncertain_apply`**（`0010:36-42`），`confidence` 只有三档文本，没有数值分数。
7. **封面没有外部来源**：`release_artworks.source_type` CHECK 只有 `embedded`、`folder`（`0005:8`）；每发行一张；扫描选不出本地封面时删除关联（`scanner.go:1022-1029`）；没有图片下载代码；没有缩略图。
8. **歌词没有外部来源**：`tracks_lyrics_ck` 只允许五种本地来源（`0050:2-8`），且每次落库覆盖这两列（`persist_track.go:81`）。
9. **艺人没有简介、图片及其存储**；艺人名字的 `origin` 只有 `scan`、`correction`、`seed`（`0026:20`）；合并候选只有库内两种信号。
10. **没有通用后台作业机制**（作业表、队列、统一重试与进度）；`catalog_projection_work` 是投影回填专用。
11. **没有实例级的外部源配置**：没有开关、密钥（AcoustID 需要应用密钥）、限速或代理设置的存放处；`web.Service` 的注释明确“不添加用户配置项”（`.trellis/spec/backend/steward-web-contracts.md:158`）。
12. **没有出站代理支持**，环境代理被显式禁用（`egress/client.go:42`、:55-58）。
13. **没有系统级模型调用入口**：模型端点、密钥、模型按用户配置（`0051:1-16`），管家运行绑定用户会话。
14. **MusicBrainz 封装的缺口**：解码结构没有 ISRC、别名、关系等字段（`metadata.go:4-39`）；没有按条码、ISRC、曲目数等的专用查询方法（只有自由查询串）；没有响应缓存；503 不重试；Cover Art Archive 不走限速器。
15. **AcoustID、LRCLIB、Discogs 没有任何代码**。
16. **备份不含 `/data`**（`deployment-environment-contracts.md:39`）：放在数据目录里的下载文件不在现有备份范围内。
17. **本机参考稿目录不存在**，无法提供参考稿里关于外部元数据或 Roon 式匹配的要点。

## Caveats / Not Found

- chromaprint 结论来自对本机缓存的固定 ffmpeg 二进制（摘要前缀 `11a44711684c`，与 Dockerfile 同一摘要）执行 `-buildconf` 与 `-muxers`；没有拉取或运行生产镜像本身。
- “CUE 的 `CATALOG` 写入厂牌编号字段”是代码事实；CUE 规范里该命令的含义（媒体目录号，通常是 13 位 UPC/EAN）来自通用知识，未在仓库内找到说明。
- Alpine 3.23 软件源里是否提供 chromaprint/`fpcalc` 包、各外部服务的当前接口与使用条款，本次没有查（任务限定只读内部调研）。
- 备份保留规则以外的备份实现、管家 supervisor 的后台运行细节、扫描反馈（`scan_feedback`）与认领（`release_claims`）没有逐行核对，只在相关处提到存在。
- 未运行任何测试；未修改任何代码、规范或配置。
